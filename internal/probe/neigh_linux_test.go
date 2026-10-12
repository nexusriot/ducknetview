//go:build linux

package probe

import (
	"encoding/binary"
	"net"
	"syscall"
	"testing"
)

// ndMessage builds a synthetic RTM_NEWNEIGH payload: an ndmsg followed by
// NDA_DST and, optionally, NDA_LLADDR.
func ndMessage(family uint8, ifindex int32, state uint16, dst []byte, lladdr []byte) []byte {
	msg := make([]byte, sizeofNdMsg)
	msg[0] = family
	binary.NativeEndian.PutUint32(msg[4:8], uint32(ifindex))
	binary.NativeEndian.PutUint16(msg[8:10], state)

	attr := func(typ uint16, payload []byte) []byte {
		length := 4 + len(payload)
		a := make([]byte, (length+3)&^3)
		binary.NativeEndian.PutUint16(a[0:2], uint16(length))
		binary.NativeEndian.PutUint16(a[2:4], typ)
		copy(a[4:], payload)
		return a
	}

	msg = append(msg, attr(ndaDst, dst)...)
	if lladdr != nil {
		msg = append(msg, attr(ndaLLAddr, lladdr)...)
	}
	return msg
}

func staticIfaceName(string) func(int) string {
	return func(int) string { return "eth0" }
}

func TestParseNeighMessageIPv6(t *testing.T) {
	ip := net.ParseIP("fe80::1c2b:3aff:fe4d:5e6f")
	mac := []byte{0x1e, 0x2b, 0x3a, 0x4d, 0x5e, 0x6f}

	nb, ok := parseNeighMessage(
		ndMessage(syscall.AF_INET6, 2, nudReachable, ip.To16(), mac),
		staticIfaceName("eth0"))
	if !ok {
		t.Fatal("parseNeighMessage returned ok=false for a well-formed IPv6 entry")
	}
	if nb.IP != ip.String() {
		t.Errorf("IP = %q, want %q", nb.IP, ip.String())
	}
	if nb.MAC != "1e:2b:3a:4d:5e:6f" {
		t.Errorf("MAC = %q, want 1e:2b:3a:4d:5e:6f", nb.MAC)
	}
	if !nb.IPv6 {
		t.Error("IPv6 = false for an AF_INET6 entry")
	}
	if nb.Stale {
		t.Error("a reachable entry with a MAC must not be reported as stale")
	}
	if nb.State != "reachable" {
		t.Errorf("State = %q, want reachable", nb.State)
	}
	if nb.Iface != "eth0" {
		t.Errorf("Iface = %q, want eth0", nb.Iface)
	}
}

func TestParseNeighMessageIPv4(t *testing.T) {
	nb, ok := parseNeighMessage(
		ndMessage(syscall.AF_INET, 3, nudStale, []byte{192, 168, 1, 1}, []byte{0xb8, 0x27, 0xeb, 1, 2, 3}),
		staticIfaceName("eth0"))
	if !ok {
		t.Fatal("parseNeighMessage returned ok=false for a well-formed IPv4 entry")
	}
	if nb.IP != "192.168.1.1" {
		t.Errorf("IP = %q, want 192.168.1.1", nb.IP)
	}
	if nb.IPv6 {
		t.Error("IPv6 = true for an AF_INET entry")
	}
	// A stale cache entry still answers "which machine is this", so it must not
	// be rendered as an unresolved one.
	if nb.Stale {
		t.Error("a stale-but-resolved entry must not be reported as having no mapping")
	}
	if nb.Vendor != "Raspberry Pi" {
		t.Errorf("Vendor = %q, want Raspberry Pi", nb.Vendor)
	}
}

// An entry the kernel holds but could not resolve has no NDA_LLADDR; the view
// relies on Stale to say so rather than printing an empty MAC column.
func TestParseNeighMessageIncomplete(t *testing.T) {
	nb, ok := parseNeighMessage(
		ndMessage(syscall.AF_INET, 2, nudIncomplete, []byte{10, 0, 0, 9}, nil),
		staticIfaceName("eth0"))
	if !ok {
		t.Fatal("parseNeighMessage returned ok=false for an incomplete entry")
	}
	if !nb.Stale {
		t.Error("an INCOMPLETE entry with no link-layer address must be reported as stale")
	}
	if nb.State != "incomplete" {
		t.Errorf("State = %q, want incomplete", nb.State)
	}
}

// NOARP covers loopback and point-to-point links, where there is nothing to
// resolve: listing them as neighbours would be noise.
func TestParseNeighMessageSkipsNoARP(t *testing.T) {
	if _, ok := parseNeighMessage(
		ndMessage(syscall.AF_INET6, 1, nudNoARP|nudPermanent, net.ParseIP("::1").To16(), nil),
		staticIfaceName("lo")); ok {
		t.Error("a NOARP entry must be skipped")
	}
}

func TestParseNeighMessageRejectsMalformed(t *testing.T) {
	cases := map[string][]byte{
		"truncated header": make([]byte, sizeofNdMsg-1),
		"no destination":   make([]byte, sizeofNdMsg),
		"short v6 address": ndMessage(syscall.AF_INET6, 2, nudReachable, []byte{1, 2, 3, 4}, nil),
	}
	for name, data := range cases {
		if _, ok := parseNeighMessage(data, staticIfaceName("eth0")); ok {
			t.Errorf("%s: parseNeighMessage returned ok=true", name)
		}
	}
}

func TestNeighRequestShape(t *testing.T) {
	req := neighRequest()
	if got := binary.NativeEndian.Uint32(req[0:4]); int(got) != len(req) {
		t.Errorf("length field = %d, want %d", got, len(req))
	}
	if got := binary.NativeEndian.Uint16(req[4:6]); got != syscall.RTM_GETNEIGH {
		t.Errorf("type = %d, want RTM_GETNEIGH", got)
	}
	if got := binary.NativeEndian.Uint16(req[6:8]); got&syscall.NLM_F_DUMP == 0 {
		t.Error("request is not a dump request, so only one entry would come back")
	}
	// AF_UNSPEC is what makes one dump cover both families.
	if req[syscall.SizeofNlMsghdr] != syscall.AF_UNSPEC {
		t.Errorf("family = %d, want AF_UNSPEC", req[syscall.SizeofNlMsghdr])
	}
}

// The real cache on this host must parse without error, which is the only
// check that the offsets match the running kernel.
func TestNeighboursNetlinkLive(t *testing.T) {
	out, err := neighboursNetlink(ifaceNameByIndex)
	if err != nil {
		t.Skipf("netlink neighbour dump unavailable here: %v", err)
	}
	for _, nb := range out {
		if net.ParseIP(nb.IP) == nil {
			t.Errorf("neighbour has unparseable IP %q", nb.IP)
		}
		if nb.MAC != "" {
			if _, err := net.ParseMAC(nb.MAC); err != nil {
				t.Errorf("neighbour %s has unparseable MAC %q", nb.IP, nb.MAC)
			}
		}
	}
	t.Logf("parsed %d neighbours from the live cache", len(out))
}
