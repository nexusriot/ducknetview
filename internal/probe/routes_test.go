package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// /proc/net/route stores addresses as little-endian hex, which is the easiest
// thing in this file to get backwards.
func TestReadRoutes4(t *testing.T) {
	body := `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
enp3s0	00000000	0102A8C0	0003	0	0	100	00000000	0	0	0
enp3s0	0002A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
docker0	000011AC	00000000	0001	0	0	0	0000FFFF	0	0	0
`
	routes, err := readRoutes4(writeTemp(t, "route", body))
	if err != nil {
		t.Fatalf("readRoutes4: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("parsed %d routes, want 3", len(routes))
	}

	def := routes[0]
	if !def.IsDefault() {
		t.Errorf("first route = %+v, want the default route", def)
	}
	if def.Gateway != "192.168.2.1" {
		t.Errorf("gateway = %q, want 192.168.2.1 (little-endian decode)", def.Gateway)
	}
	if def.Iface != "enp3s0" || def.Metric != 100 {
		t.Errorf("default route = %+v, want enp3s0 metric 100", def)
	}

	if routes[1].Destination != "192.168.2.0/24" {
		t.Errorf("LAN route = %q, want 192.168.2.0/24", routes[1].Destination)
	}
	if routes[1].Gateway != "" {
		t.Errorf("directly-attached route should have no gateway, got %q", routes[1].Gateway)
	}
	if routes[2].Destination != "172.17.0.0/16" {
		t.Errorf("docker route = %q, want 172.17.0.0/16", routes[2].Destination)
	}
}

func TestReadRoutes6(t *testing.T) {
	body := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000400 00000001 00000000 00000003 enp3s0\n" +
		"fe800000000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000000 00000001 00000001 enp3s0\n"

	routes, err := readRoutes6(writeTemp(t, "ipv6_route", body))
	if err != nil {
		t.Fatalf("readRoutes6: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("parsed %d routes, want 2", len(routes))
	}
	if !routes[0].IsDefault() || !routes[0].IPv6 {
		t.Errorf("first route = %+v, want the IPv6 default", routes[0])
	}
	if routes[0].Gateway != "fe80::1" {
		t.Errorf("v6 gateway = %q, want fe80::1", routes[0].Gateway)
	}
	if routes[1].Destination != "fe80::/64" {
		t.Errorf("v6 prefix = %q, want fe80::/64", routes[1].Destination)
	}
}

// The default route the host is using is the lowest-metric one, not the first.
func TestDefaultRoutePicksLowestMetric(t *testing.T) {
	routes := []Route{
		{Destination: "default", Gateway: "10.0.0.1", Iface: "wlan0", Metric: 600},
		{Destination: "192.168.1.0/24", Iface: "eth0"},
		{Destination: "default", Gateway: "192.168.1.1", Iface: "eth0", Metric: 100},
		{Destination: "default", Gateway: "fe80::1", Iface: "eth0", Metric: 1, IPv6: true},
	}
	def, ok := DefaultRoute(routes)
	if !ok {
		t.Fatal("no default route found")
	}
	if def.Gateway != "192.168.1.1" {
		t.Errorf("default gateway = %q, want the lowest-metric IPv4 one", def.Gateway)
	}

	if _, ok := DefaultRoute([]Route{{Destination: "10.0.0.0/8"}}); ok {
		t.Error("a table with no default route should report none")
	}
}

func TestReadNeighbours(t *testing.T) {
	body := `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.1      0x1         0x2         b8:27:eb:11:22:33     *        enp3s0
192.168.1.9      0x1         0x0         00:00:00:00:00:00     *        enp3s0
192.168.1.20     0x1         0x2         de:ad:be:ef:00:01     *        wlan0
`
	ns, err := readNeighbours(writeTemp(t, "arp", body))
	if err != nil {
		t.Fatalf("readNeighbours: %v", err)
	}
	if len(ns) != 3 {
		t.Fatalf("parsed %d neighbours, want 3", len(ns))
	}
	if ns[0].IP != "192.168.1.1" || ns[0].Iface != "enp3s0" {
		t.Errorf("neighbour = %+v", ns[0])
	}
	if ns[0].Vendor != "Raspberry Pi" {
		t.Errorf("vendor = %q, want the OUI lookup to resolve it", ns[0].Vendor)
	}
	if !ns[1].Stale {
		t.Error("an unresolved entry (flags 0x0) should be marked stale")
	}
	if ns[2].Stale {
		t.Error("a resolved entry should not be marked stale")
	}
}

func TestReadResolvers(t *testing.T) {
	body := `# a comment
nameserver 127.0.0.53
nameserver 1.1.1.1   # inline comment
search example.com
options edns0
`
	rs, err := readResolvers(writeTemp(t, "resolv.conf", body))
	if err != nil {
		t.Fatalf("readResolvers: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("parsed %d resolvers, want 2", len(rs))
	}
	if !rs[0].Stub {
		t.Error("a loopback nameserver is a local stub and should say so")
	}
	if rs[1].Address != "1.1.1.1" || rs[1].Stub {
		t.Errorf("upstream resolver = %+v", rs[1])
	}
}

func TestMACVendor(t *testing.T) {
	if got := MACVendor("B8:27:EB:11:22:33"); got != "Raspberry Pi" {
		t.Errorf("MACVendor = %q, want Raspberry Pi (case-insensitive)", got)
	}
	if got := MACVendor("08:00:27:aa:bb:cc"); got != "VirtualBox" {
		t.Errorf("MACVendor = %q, want VirtualBox", got)
	}
	// A locally-administered address is randomised and has no vendor at all.
	if got := MACVendor("de:ad:be:ef:00:01"); got != "(randomised)" {
		t.Errorf("MACVendor = %q, want the randomised marker", got)
	}
	if got := MACVendor("00:00:5e:00:00:01"); got != "" {
		t.Errorf("MACVendor = %q, want empty for an unknown global OUI", got)
	}
	if got := MACVendor("short"); got != "" {
		t.Errorf("MACVendor(%q) = %q, want empty", "short", got)
	}
}

func TestIfaceForIP(t *testing.T) {
	ifaces := []IfaceInfo{
		{Name: "lo", Addrs: []string{"127.0.0.1/8", "::1/128"}},
		{Name: "enp3s0", Addrs: []string{"192.168.1.5/24", "fe80::1234/64"}},
	}
	cases := map[string]string{
		"192.168.1.5": "enp3s0",
		"127.0.0.1":   "lo",
		"fe80::1234":  "enp3s0",
		"10.0.0.1":    "",
		"nonsense":    "",
	}
	for ip, want := range cases {
		if got := IfaceForIP(ifaces, ip); got != want {
			t.Errorf("IfaceForIP(%q) = %q, want %q", ip, got, want)
		}
	}
}

// The live tables must at least parse on the machine running the tests.
func TestRoutesSmoke(t *testing.T) {
	routes, err := Routes()
	if err != nil {
		t.Skipf("routing table unavailable: %v", err)
	}
	for _, r := range routes {
		if r.Destination == "" || r.Iface == "" {
			t.Errorf("incomplete route: %+v", r)
		}
	}
}

// An IPv6-only host still has a default route, and used to be told it had
// none at all.
func TestDefaultRouteFallsBackToIPv6(t *testing.T) {
	routes := []Route{
		{Destination: "fe80::/64", Iface: "eth0", IPv6: true},
		{Destination: "default", Gateway: "fe80::1", Iface: "eth0", Metric: 1024, IPv6: true},
	}
	def, ok := DefaultRoute(routes)
	if !ok {
		t.Fatal("an IPv6 default route should be found")
	}
	if def.Gateway != "fe80::1" || !def.IPv6 {
		t.Fatalf("got %+v", def)
	}

	// Dual stack still prefers IPv4.
	dual := append(routes, Route{Destination: "default", Gateway: "192.168.1.1", Iface: "eth0", Metric: 100})
	if def, ok := DefaultRoute(dual); !ok || def.Gateway != "192.168.1.1" {
		t.Fatalf("dual-stack should prefer IPv4, got %+v ok=%v", def, ok)
	}
	if def, ok := DefaultRouteFamily(dual, true); !ok || def.Gateway != "fe80::1" {
		t.Fatalf("the IPv6 default should still be reachable by family: %+v ok=%v", def, ok)
	}
}

// A link-local next hop means nothing without the link it is on.
func TestGatewayAddressCarriesTheZone(t *testing.T) {
	cases := []struct {
		route Route
		want  string
	}{
		{Route{Gateway: "fe80::1", Iface: "eth0", IPv6: true}, "fe80::1%eth0"},
		{Route{Gateway: "fe80::1%eth0", Iface: "eth0", IPv6: true}, "fe80::1%eth0"},
		{Route{Gateway: "2001:db8::1", Iface: "eth0", IPv6: true}, "2001:db8::1"},
		{Route{Gateway: "192.168.1.1", Iface: "eth0"}, "192.168.1.1"},
		{Route{Iface: "ppp0"}, ""},
	}
	for _, c := range cases {
		if got := c.route.GatewayAddress(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.route, got, c.want)
		}
	}
}

// Both first hops are probed on a dual-stack host.
func TestGatewayTargetsCoversBothFamilies(t *testing.T) {
	routes := []Route{
		{Destination: "default", Gateway: "192.168.1.1", Iface: "eth0", Metric: 100},
		{Destination: "default", Gateway: "fe80::1", Iface: "eth0", Metric: 1024, IPv6: true},
	}
	got := GatewayTargets(routes)
	if len(got) != 2 {
		t.Fatalf("want two targets, got %+v", got)
	}
	if got[0].Label != "gateway" || got[0].Addr != "192.168.1.1:53" {
		t.Errorf("IPv4 target = %+v", got[0])
	}
	if got[1].Label != "gateway6" || got[1].Addr != "[fe80::1%eth0]:53" {
		t.Errorf("IPv6 target = %+v", got[1])
	}
	if len(GatewayTargets(nil)) != 0 {
		t.Error("no routes should mean no targets")
	}
}
