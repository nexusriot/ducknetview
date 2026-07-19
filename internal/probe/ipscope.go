package probe

import "net"

// IPScope classifies a bare IP address (no port) into a coarse reachability
// scope: "local" (loopback/unspecified), "link" (link-local), "private"
// (RFC1918 / ULA), "cgnat" (100.64.0.0/10), or "public". It returns "" when the
// string is not a valid IP.
func IPScope(ip string) string {
	addr := net.ParseIP(ip)
	if addr == nil {
		return ""
	}
	switch {
	case addr.IsLoopback() || addr.IsUnspecified():
		return "local"
	case addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast():
		return "link"
	case addr.IsPrivate():
		return "private"
	case cgnat.Contains(addr):
		return "cgnat"
	default:
		return "public"
	}
}

// cgnat is the shared carrier-grade NAT range (RFC 6598), which net.IP.IsPrivate
// does not cover.
var _, cgnat, _ = net.ParseCIDR("100.64.0.0/10")

// ListenerExposure classifies where a listening socket bound to ip can be
// reached from: "exposed" (wildcard 0.0.0.0/:: or a routable address — reachable
// off-host), "lan" (a specific private/link-local address), "local" (loopback),
// or "" for an unparseable address. Unlike IPScope, a wildcard bind is treated
// as exposed rather than local, since it accepts traffic on every interface.
func ListenerExposure(ip string) string {
	addr := net.ParseIP(ip)
	if addr == nil {
		return ""
	}
	switch {
	case addr.IsUnspecified():
		return "exposed"
	case addr.IsLoopback():
		return "local"
	case addr.IsPrivate() || addr.IsLinkLocalUnicast() || cgnat.Contains(addr):
		return "lan"
	default:
		return "exposed"
	}
}
