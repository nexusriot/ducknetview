//go:build linux

package probe

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"syscall"
	"time"
)

// Per-socket byte counters come from the kernel's sock_diag netlink interface
// (the same source `ss -ti` reads). Unlike /proc/net/tcp it reports each
// socket's tcp_info, which carries cumulative acked/received byte counts — the
// only way to get per-connection throughput without packet capture or eBPF.
const (
	netlinkInetDiag  = 4  // NETLINK_INET_DIAG
	sockDiagByFamily = 20 // SOCK_DIAG_BY_FAMILY request type
	inetDiagInfo     = 2  // INET_DIAG_INFO attribute type

	sizeofDiagReqV2 = 56
	sizeofDiagMsg   = 72

	// Byte offsets into struct tcp_info (linux/uapi/linux/tcp.h). Everything up
	// to tcpi_bytes_received has been stable since Linux 4.1; newer fields are
	// appended, so a length check is enough to stay forward compatible.
	tcpInfoRTT           = 68  // tcpi_rtt, microseconds
	tcpInfoTotalRetrans  = 100 // tcpi_total_retrans
	tcpInfoBytesAcked    = 120 // tcpi_bytes_acked
	tcpInfoBytesReceived = 128 // tcpi_bytes_received
	tcpInfoMinRTT        = 148 // tcpi_min_rtt, microseconds (Linux 4.6+)
	tcpInfoMinLen        = tcpInfoBytesReceived + 8
	tcpInfoMinRTTLen     = tcpInfoMinRTT + 4

	// Offsets into inet_diag_msg itself, after the 48-byte sockid at offset 4.
	diagMsgRecvQ = 56
	diagMsgSendQ = 60

	diagRecvTimeout = 2 * time.Second
	diagMaxReads    = 512 // backstop against a runaway multipart dump
)

// SockStats returns per-connection kernel counters keyed by SockKey. TCP only:
// UDP sockets have no equivalent accounting. Sockets are reported for every
// user (the same visibility `ss` has), but a failure is never fatal — callers
// treat an empty map as "throughput unavailable".
func SockStats() (map[string]SockStat, error) {
	out := make(map[string]SockStat, 128)

	var firstErr error
	for _, family := range []uint8{syscall.AF_INET, syscall.AF_INET6} {
		if err := dumpTCPDiag(family, out); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func dumpTCPDiag(family uint8, out map[string]SockStat) error {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM, netlinkInetDiag)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)

	// Without a receive timeout a lost or dropped multipart dump would block the
	// refresh command goroutine forever.
	tv := syscall.NsecToTimeval(int64(diagRecvTimeout))
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		return err
	}

	if err := syscall.Sendto(fd, diagRequest(family), 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return err
	}

	buf := make([]byte, 1<<20)
	for reads := 0; reads < diagMaxReads; reads++ {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			return err
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return err
		}
		for _, msg := range msgs {
			switch msg.Header.Type {
			case syscall.NLMSG_DONE:
				return nil
			case syscall.NLMSG_ERROR:
				if len(msg.Data) >= 4 {
					if errno := int32(binary.NativeEndian.Uint32(msg.Data[0:4])); errno != 0 {
						return fmt.Errorf("inet_diag: %w", syscall.Errno(-errno))
					}
				}
				return nil
			}
			if key, st, ok := parseDiagMessage(msg.Data); ok {
				out[key] = st
			}
		}
	}
	return nil
}

// diagRequest builds an INET_DIAG_REQ_V2 dump request for all TCP sockets of
// the given address family, asking for the INET_DIAG_INFO (tcp_info) extension.
func diagRequest(family uint8) []byte {
	req := make([]byte, syscall.SizeofNlMsghdr+sizeofDiagReqV2)

	binary.NativeEndian.PutUint32(req[0:4], uint32(len(req)))
	binary.NativeEndian.PutUint16(req[4:6], sockDiagByFamily)
	binary.NativeEndian.PutUint16(req[6:8], syscall.NLM_F_REQUEST|syscall.NLM_F_DUMP)
	binary.NativeEndian.PutUint32(req[8:12], 1) // sequence

	body := req[syscall.SizeofNlMsghdr:]
	body[0] = family
	body[1] = syscall.IPPROTO_TCP
	body[2] = 1 << (inetDiagInfo - 1) // ext bitmask
	binary.NativeEndian.PutUint32(body[4:8], ^uint32(0))

	return req
}

// parseDiagMessage decodes one inet_diag_msg and its attributes. ok is false
// for messages that are too short or carry no tcp_info (e.g. TIME_WAIT sockets,
// which the kernel reports without the extension).
func parseDiagMessage(data []byte) (string, SockStat, bool) {
	if len(data) < sizeofDiagMsg {
		return "", SockStat{}, false
	}

	st, ok := parseDiagInfo(data[sizeofDiagMsg:])
	if !ok {
		return "", SockStat{}, false
	}

	// inet_diag_msg: family, state, timer, retrans, then inet_diag_sockid at
	// offset 4 (sport, dport big-endian; 16-byte src and dst).
	family := data[0]
	local := diagEndpoint(family, data[8:24], binary.BigEndian.Uint16(data[4:6]))
	remote := diagEndpoint(family, data[24:40], binary.BigEndian.Uint16(data[6:8]))
	if local == "" || remote == "" {
		return "", SockStat{}, false
	}

	st.RecvQ = binary.NativeEndian.Uint32(data[diagMsgRecvQ : diagMsgRecvQ+4])
	st.SendQ = binary.NativeEndian.Uint32(data[diagMsgSendQ : diagMsgSendQ+4])

	return SockKey(local, remote), st, true
}

// parseDiagInfo walks the netlink attributes following an inet_diag_msg and
// extracts the counters from INET_DIAG_INFO.
func parseDiagInfo(attrs []byte) (SockStat, bool) {
	for len(attrs) >= 4 {
		length := int(binary.NativeEndian.Uint16(attrs[0:2]))
		typ := binary.NativeEndian.Uint16(attrs[2:4])
		if length < 4 || length > len(attrs) {
			return SockStat{}, false
		}

		if typ == inetDiagInfo {
			payload := attrs[4:length]
			if len(payload) < tcpInfoMinLen {
				return SockStat{}, false
			}
			st := SockStat{
				TxBytes: binary.NativeEndian.Uint64(payload[tcpInfoBytesAcked : tcpInfoBytesAcked+8]),
				RxBytes: binary.NativeEndian.Uint64(payload[tcpInfoBytesReceived : tcpInfoBytesReceived+8]),
				RTT:     time.Duration(binary.NativeEndian.Uint32(payload[tcpInfoRTT:tcpInfoRTT+4])) * time.Microsecond,
				Retrans: binary.NativeEndian.Uint32(payload[tcpInfoTotalRetrans : tcpInfoTotalRetrans+4]),
			}
			// tcpi_min_rtt only exists on Linux 4.6+; older kernels simply send
			// a shorter tcp_info.
			if len(payload) >= tcpInfoMinRTTLen {
				st.MinRTT = time.Duration(binary.NativeEndian.Uint32(payload[tcpInfoMinRTT:tcpInfoMinRTT+4])) * time.Microsecond
			}
			return st, true
		}

		aligned := (length + 3) &^ 3
		if aligned > len(attrs) {
			break
		}
		attrs = attrs[aligned:]
	}
	return SockStat{}, false
}

// diagEndpoint renders a netlink sockid address as the unbracketed "ip:port"
// form the rest of the package uses.
func diagEndpoint(family uint8, raw []byte, port uint16) string {
	var ip net.IP
	switch family {
	case syscall.AF_INET:
		if len(raw) < 4 {
			return ""
		}
		ip = net.IP(raw[:4])
	case syscall.AF_INET6:
		if len(raw) < 16 {
			return ""
		}
		ip = net.IP(raw[:16])
	default:
		return ""
	}
	return ip.String() + ":" + strconv.Itoa(int(port))
}
