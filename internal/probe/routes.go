package probe

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"strings"
)

// Routing state answers questions the socket views cannot: which way traffic
// leaves the host, who the first hop is, and who is answering DNS. It comes
// from /proc, so it is Linux-only; the tab reports what it could not read
// rather than pretending the host has no routes.

// Route is one entry of the kernel routing table.
type Route struct {
	Destination string // "default" or CIDR
	Gateway     string // "" for a directly-attached network
	Iface       string
	Metric      int
	IPv6        bool
}

// IsDefault reports whether the route is a default route.
func (r Route) IsDefault() bool { return r.Destination == "default" }

// Neighbour is one entry of the ARP/neighbour table.
type Neighbour struct {
	IP     string
	MAC    string
	Iface  string
	Vendor string // best-effort OUI lookup, "" when unknown
	Stale  bool   // the kernel has no confirmed mapping
	IPv6   bool
	State  string // the kernel's cache state, as `ip neigh` words it
}

// Resolver is a nameserver the host is configured to use.
type Resolver struct {
	Address string
	Stub    bool // a local stub resolver (systemd-resolved, dnsmasq) rather than a real upstream
}

// Routes reads the IPv4 and IPv6 routing tables. A failure to read one family
// is not fatal: the other is still reported.
func Routes() ([]Route, error) {
	var out []Route
	var firstErr error

	if v4, err := readRoutes4("/proc/net/route"); err != nil {
		firstErr = err
	} else {
		out = append(out, v4...)
	}
	if v6, err := readRoutes6("/proc/net/ipv6_route"); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else {
		out = append(out, v6...)
	}

	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// DefaultRoute returns the lowest-metric default route, which is the one the
// host is actually using, and the interface it leaves by.
func DefaultRoute(routes []Route) (Route, bool) {
	best := Route{Metric: -1}
	for _, r := range routes {
		if !r.IsDefault() || r.IPv6 {
			continue
		}
		if best.Metric < 0 || r.Metric < best.Metric {
			best = r
		}
	}
	return best, best.Metric >= 0
}

// readRoutes4 parses /proc/net/route, whose addresses are little-endian hex.
func readRoutes4(path string) ([]Route, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Route
	sc := bufio.NewScanner(f)
	for first := true; sc.Scan(); {
		if first { // header
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 {
			continue
		}

		dest, ok1 := hexIPv4(fields[1])
		mask, ok2 := hexIPv4(fields[7])
		gw, ok3 := hexIPv4(fields[2])
		if !ok1 || !ok2 || !ok3 {
			continue
		}

		r := Route{Iface: fields[0]}
		ones, _ := net.IPMask(mask.To4()).Size()
		if dest.Equal(net.IPv4zero) && ones == 0 {
			r.Destination = "default"
		} else {
			r.Destination = dest.String() + "/" + strconv.Itoa(ones)
		}
		if !gw.Equal(net.IPv4zero) {
			r.Gateway = gw.String()
		}
		if m, err := strconv.Atoi(fields[6]); err == nil {
			r.Metric = m
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// hexIPv4 decodes the little-endian hex form /proc/net/route uses.
func hexIPv4(s string) (net.IP, bool) {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return nil, false
	}
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(v))
	return net.IP(b), true
}

// readRoutes6 parses /proc/net/ipv6_route: destination, prefix length, source,
// source prefix length, next hop, metric, … , interface.
func readRoutes6(path string) ([]Route, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Route
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}

		dest, ok := hexIPv6(fields[0])
		if !ok {
			continue
		}
		prefix, err := strconv.ParseInt(fields[1], 16, 32)
		if err != nil {
			continue
		}

		r := Route{IPv6: true, Iface: fields[len(fields)-1]}
		if dest.Equal(net.IPv6unspecified) && prefix == 0 {
			r.Destination = "default"
		} else {
			r.Destination = dest.String() + "/" + strconv.FormatInt(prefix, 10)
		}
		if gw, ok := hexIPv6(fields[4]); ok && !gw.Equal(net.IPv6unspecified) {
			r.Gateway = gw.String()
		}
		if m, err := strconv.ParseInt(fields[5], 16, 32); err == nil {
			r.Metric = int(m)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

func hexIPv6(s string) (net.IP, bool) {
	if len(s) != 32 {
		return nil, false
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, false
	}
	return net.IP(b), true
}

// procNetARP is the IPv4-only fallback source, used when the netlink dump is
// unavailable.
const procNetARP = "/proc/net/arp"

// Neighbours reads the kernel's neighbour cache for both address families. On
// Linux this is an rtnetlink dump, because /proc/net/arp has never carried IPv6.
func Neighbours() ([]Neighbour, error) {
	return readNeighbourCache()
}

func readNeighbours(path string) ([]Neighbour, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Neighbour
	sc := bufio.NewScanner(f)
	for first := true; sc.Scan(); {
		if first { // header
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 6 {
			continue
		}
		n := Neighbour{
			IP:     fields[0],
			MAC:    fields[3],
			Iface:  fields[5],
			Vendor: MACVendor(fields[3]),
		}
		// Flags 0x0 means the kernel has an entry but no confirmed mapping.
		n.Stale = fields[2] == "0x0" || n.MAC == "00:00:00:00:00:00"
		out = append(out, n)
	}
	return out, sc.Err()
}

// Resolvers reads the configured nameservers from /etc/resolv.conf.
func Resolvers() ([]Resolver, error) {
	return readResolvers("/etc/resolv.conf")
}

func readResolvers(path string) ([]Resolver, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Resolver
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		addr := fields[1]
		ip := net.ParseIP(addr)
		out = append(out, Resolver{
			Address: addr,
			// A loopback nameserver is a local stub (systemd-resolved, dnsmasq),
			// which means the real upstream is configured elsewhere.
			Stub: ip != nil && ip.IsLoopback(),
		})
	}
	return out, sc.Err()
}

// builtinOUIs is a small, deliberately partial map of MAC prefixes to vendors:
// enough to recognise the usual suspects on a home or office LAN without
// embedding the full IEEE registry.
var builtinOUIs = map[string]string{
	"00:1a:11": "Google", "00:1b:63": "Apple", "00:03:93": "Apple",
	"00:0c:29": "VMware", "00:50:56": "VMware", "00:05:69": "VMware",
	"08:00:27": "VirtualBox", "52:54:00": "QEMU/KVM", "00:16:3e": "Xen",
	"02:42:ac": "Docker", "b8:27:eb": "Raspberry Pi", "dc:a6:32": "Raspberry Pi",
	"e4:5f:01": "Raspberry Pi", "28:cd:c1": "Raspberry Pi",
	"00:1d:0f": "TP-Link", "50:c7:bf": "TP-Link", "a4:2b:b0": "TP-Link",
	"00:18:e7": "Netgear", "00:26:f2": "Netgear", "20:4e:7f": "Netgear",
	"00:14:bf": "Cisco/Linksys", "00:25:9c": "Cisco/Linksys",
	"00:0d:b9": "PC Engines", "00:15:5d": "Hyper-V",
	"fc:ec:da": "Ubiquiti", "24:5a:4c": "Ubiquiti", "78:8a:20": "Ubiquiti",
	"00:1c:42": "Parallels", "00:e0:4c": "Realtek", "00:15:af": "AzureWave",
	"3c:5a:b4": "Google", "f4:f5:d8": "Google", "da:a1:19": "Google",
	"00:24:d7": "Intel", "34:13:e8": "Intel", "a0:88:69": "Intel",
	"00:90:4c": "Epigram", "00:1f:3b": "Intel",
}

// MACVendor returns a best-effort vendor for a MAC address, or "" when the
// prefix is not in the built-in list. A locally-administered address (the
// second-least-significant bit of the first octet) is randomised and has no
// vendor at all, which is worth saying out loud.
func MACVendor(mac string) string {
	mac = strings.ToLower(strings.TrimSpace(mac))
	if len(mac) < 8 {
		return ""
	}
	if v, ok := builtinOUIs[mac[:8]]; ok {
		return v
	}
	if b, err := strconv.ParseUint(mac[:2], 16, 8); err == nil && b&0x02 != 0 {
		return "(randomised)"
	}
	return ""
}

// IfaceForIP reports which of the given interfaces owns a local address, so a
// socket can be attributed to the link it actually uses.
func IfaceForIP(ifaces []IfaceInfo, ip string) string {
	addr := net.ParseIP(ip)
	if addr == nil {
		return ""
	}
	for _, ii := range ifaces {
		for _, a := range ii.Addrs {
			// Addrs are CIDR strings ("192.168.1.5/24").
			if ipStr, _, err := net.ParseCIDR(a); err == nil {
				if ipStr.Equal(addr) {
					return ii.Name
				}
				continue
			}
			if net.ParseIP(a).Equal(addr) {
				return ii.Name
			}
		}
	}
	return ""
}
