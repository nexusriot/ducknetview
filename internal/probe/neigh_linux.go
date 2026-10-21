//go:build linux

package probe

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"time"
)

// /proc/net/arp is IPv4 only — the kernel never exposed the IPv6 neighbour
// cache there, which is why `ip -6 neigh` exists and `arp` does not have an
// equivalent. The cache itself is a netlink dump, and this package already
// speaks netlink for sock_diag, so the same shape of code reaches both
// families without adding a dependency.
const (
	// Attribute types inside an ndmsg (linux/neighbour.h). Only the two that
	// carry the mapping itself are needed.
	ndaDst    = 1
	ndaLLAddr = 2

	// sizeofNdMsg is struct ndmsg: family, two pads, ifindex, state, flags,
	// type.
	sizeofNdMsg = 12

	// Neighbour cache states (linux/neighbour.h). A cache entry exists in all
	// of them; only some mean the address has actually been resolved.
	nudIncomplete = 0x01
	nudReachable  = 0x02
	nudStale      = 0x04
	nudDelay      = 0x08
	nudProbe      = 0x10
	nudFailed     = 0x20
	nudNoARP      = 0x40
	nudPermanent  = 0x80

	neighRecvTimeout = 2 * time.Second
	neighMaxReads    = 256
)

// neighboursNetlink dumps the kernel's neighbour cache for both address
// families. ifaceName resolves an interface index to a name; it is a parameter
// so the parsing can be tested without the host's real interfaces.
func neighboursNetlink(ifaceName func(int) string) ([]Neighbour, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM, syscall.NETLINK_ROUTE)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)

	// As with the sock_diag dump: without a receive timeout a dropped multipart
	// message would block the collecting goroutine for the life of the process.
	tv := syscall.NsecToTimeval(int64(neighRecvTimeout))
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		return nil, err
	}

	if err := syscall.Sendto(fd, neighRequest(), 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return nil, err
	}

	var out []Neighbour
	buf := make([]byte, 1<<20)
	for reads := 0; reads < neighMaxReads; reads++ {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			return out, err
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return out, err
		}
		for _, msg := range msgs {
			switch msg.Header.Type {
			case syscall.NLMSG_DONE:
				return out, nil
			case syscall.NLMSG_ERROR:
				if len(msg.Data) >= 4 {
					if errno := int32(binary.NativeEndian.Uint32(msg.Data[0:4])); errno != 0 {
						return out, fmt.Errorf("rtnetlink neigh: %w", syscall.Errno(-errno))
					}
				}
				return out, nil
			case syscall.RTM_NEWNEIGH:
				if nb, ok := parseNeighMessage(msg.Data, ifaceName); ok {
					out = append(out, nb)
				}
			}
		}
	}
	return out, nil
}

// neighRequest builds an RTM_GETNEIGH dump request. Family AF_UNSPEC asks for
// every family at once, so IPv4 and IPv6 arrive in one dump.
func neighRequest() []byte {
	req := make([]byte, syscall.SizeofNlMsghdr+sizeofNdMsg)

	binary.NativeEndian.PutUint32(req[0:4], uint32(len(req)))
	binary.NativeEndian.PutUint16(req[4:6], syscall.RTM_GETNEIGH)
	binary.NativeEndian.PutUint16(req[6:8], syscall.NLM_F_REQUEST|syscall.NLM_F_DUMP)
	binary.NativeEndian.PutUint32(req[8:12], 1) // sequence

	req[syscall.SizeofNlMsghdr] = syscall.AF_UNSPEC
	return req
}

// parseNeighMessage decodes one ndmsg and its attributes. ok is false for
// messages that are too short, carry no destination address, or describe a
// cache entry that is not a neighbour at all (NOARP covers loopback and
// point-to-point links, which have nothing to resolve).
func parseNeighMessage(data []byte, ifaceName func(int) string) (Neighbour, bool) {
	if len(data) < sizeofNdMsg {
		return Neighbour{}, false
	}

	family := data[0]
	ifindex := int(int32(binary.NativeEndian.Uint32(data[4:8])))
	state := binary.NativeEndian.Uint16(data[8:10])

	if state&nudNoARP != 0 {
		return Neighbour{}, false
	}

	var ip net.IP
	var mac string
	attrs := data[sizeofNdMsg:]
	for len(attrs) >= 4 {
		length := int(binary.NativeEndian.Uint16(attrs[0:2]))
		typ := binary.NativeEndian.Uint16(attrs[2:4])
		if length < 4 || length > len(attrs) {
			break
		}
		payload := attrs[4:length]

		switch typ {
		case ndaDst:
			switch {
			case family == syscall.AF_INET && len(payload) >= 4:
				ip = net.IP(append([]byte(nil), payload[:4]...))
			case family == syscall.AF_INET6 && len(payload) >= 16:
				ip = net.IP(append([]byte(nil), payload[:16]...))
			}
		case ndaLLAddr:
			if len(payload) == 6 {
				mac = net.HardwareAddr(payload).String()
			}
		}

		aligned := (length + 3) &^ 3
		if aligned > len(attrs) {
			break
		}
		attrs = attrs[aligned:]
	}

	if ip == nil {
		return Neighbour{}, false
	}

	nb := Neighbour{
		IP:     ip.String(),
		MAC:    mac,
		Iface:  ifaceName(ifindex),
		Vendor: MACVendor(mac),
		IPv6:   ip.To4() == nil,
		State:  neighStateName(state),
	}
	// "Stale" here keeps the meaning the /proc reader gave it: the kernel holds
	// an entry but no confirmed mapping. A genuinely stale-but-known address
	// still answers "which machine is this", so it does not qualify.
	nb.Stale = mac == "" || state&(nudIncomplete|nudFailed) != 0
	return nb, true
}

// neighStateName renders the cache state the way `ip neigh` does, so what the
// tool shows can be checked against it.
func neighStateName(state uint16) string {
	switch {
	case state&nudReachable != 0:
		return "reachable"
	case state&nudPermanent != 0:
		return "permanent"
	case state&nudStale != 0:
		return "stale"
	case state&nudDelay != 0:
		return "delay"
	case state&nudProbe != 0:
		return "probe"
	case state&nudFailed != 0:
		return "failed"
	case state&nudIncomplete != 0:
		return "incomplete"
	default:
		return ""
	}
}

// ifaceNameByIndex resolves an interface index, returning "" rather than an
// error: a neighbour on an interface that has just gone away is still worth
// listing.
func ifaceNameByIndex(index int) string {
	ii, err := net.InterfaceByIndex(index)
	if err != nil {
		return ""
	}
	return ii.Name
}

// readNeighbourCache is the platform entry point used by Neighbours. It prefers
// netlink, which covers both families, and falls back to /proc/net/arp when the
// netlink dump is unavailable (a restricted container, say) so an IPv4-only
// answer is still better than none.
func readNeighbourCache() ([]Neighbour, error) {
	out, err := neighboursNetlink(ifaceNameByIndex)
	// A partial dump still carries IPv6 entries the ARP file never will.
	if len(out) > 0 {
		return out, nil
	}
	arp, arpErr := readNeighbours(procNetARP)
	if arpErr != nil {
		if err != nil {
			return nil, err
		}
		return nil, arpErr
	}
	return arp, nil
}
