package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestWatchlist(t *testing.T) {
	wl := parseWatchlist([]string{"10.0.0.0/8", "8.8.8.8", `evil\.example`, "  ", "2606:4700::/32"})
	if wl == nil {
		t.Fatal("expected a non-nil watchlist")
	}
	cases := []struct {
		ip, host string
		want     bool
	}{
		{"10.1.2.3", "", true},                // CIDR
		{"8.8.8.8", "", true},                 // exact IP
		{"9.9.9.9", "", false},                // no match
		{"1.2.3.4", "evil.example.com", true}, // regex on hostname
		{"1.2.3.4", "safe.example", false},
		{"2606:4700::1111", "", true}, // IPv6 CIDR
	}
	for _, c := range cases {
		if got := wl.match(c.ip, c.host); got != c.want {
			t.Errorf("match(%q,%q) = %v, want %v", c.ip, c.host, got, c.want)
		}
	}

	if parseWatchlist(nil) != nil {
		t.Fatal("nil entries should yield a nil watchlist")
	}
	if parseWatchlist([]string{"", "   "}) != nil {
		t.Fatal("blank-only entries should yield a nil watchlist")
	}
	// nil receiver is safe.
	var none *watchList
	if none.match("8.8.8.8", "") {
		t.Fatal("nil watchlist should never match")
	}
}

func TestPublicOnlyFilter(t *testing.T) {
	m := NewModel()
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "8.8.8.8:80", State: "ESTABLISHED", PID: 1},   // public
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "10.0.0.9:80", State: "ESTABLISHED", PID: 2},  // private
		{Proto: "tcp", Local: "10.0.0.1:3", Remote: "127.0.0.1:80", State: "ESTABLISHED", PID: 3}, // local
	}
	m.publicOnly = true
	got := m.filteredConns()
	if len(got) != 1 || probe.IPScope(stripPort(got[0].Remote)) != "public" {
		t.Fatalf("public-only should keep exactly the public remote, got %d rows", len(got))
	}
}

func TestConnSortByScope(t *testing.T) {
	m := NewModel()
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "10.0.0.9:80", State: "ESTABLISHED", PID: 1},  // private
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "8.8.8.8:80", State: "ESTABLISHED", PID: 2},   // public
		{Proto: "tcp", Local: "10.0.0.1:3", Remote: "127.0.0.1:80", State: "ESTABLISHED", PID: 3}, // local
	}
	m.connsSortCol = sortIndex(connsSortCols, "scope")
	m.connsSortDesc = false
	got := m.filteredConns()
	if probe.IPScope(stripPort(got[0].Remote)) != "public" {
		t.Fatalf("scope sort should put public first, got %s", got[0].Remote)
	}
	if probe.IPScope(stripPort(got[2].Remote)) != "local" {
		t.Fatalf("scope sort should put local last, got %s", got[2].Remote)
	}
}

// First-seen times are recorded per connection and carried over across
// enumerations (feeding both the AGE column and new/closed detection).
func TestConnAgeTracking(t *testing.T) {
	m := NewModel()
	c1 := probe.Conn{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:80", State: "ESTABLISHED", PID: 1}

	model, _ := m.Update(connInfoMsg(probe.ConnInfo{Conns: []probe.Conn{c1}}))
	m = model.(Model)
	t1, ok := m.connFirstSeen[connKey(c1)]
	if !ok || t1.IsZero() {
		t.Fatal("c1 first-seen time not recorded")
	}

	c2 := probe.Conn{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:80", State: "ESTABLISHED", PID: 2}
	model, _ = m.Update(connInfoMsg(probe.ConnInfo{Conns: []probe.Conn{c1, c2}}))
	m = model.(Model)
	if got := m.connFirstSeen[connKey(c1)]; !got.Equal(t1) {
		t.Fatalf("c1 first-seen should be carried over unchanged: %v vs %v", got, t1)
	}
	if _, ok := m.connFirstSeen[connKey(c2)]; !ok {
		t.Fatal("c2 first-seen not recorded")
	}
	if !m.newConns[connKey(c2)] {
		t.Fatal("c2 should be flagged new")
	}
	// A dropped connection must be pruned from the first-seen map.
	model, _ = m.Update(connInfoMsg(probe.ConnInfo{Conns: []probe.Conn{c2}}))
	m = model.(Model)
	if _, ok := m.connFirstSeen[connKey(c1)]; ok {
		t.Fatal("dropped c1 should be pruned from first-seen map")
	}
	if m.connRemoved != 1 {
		t.Fatalf("expected 1 closed connection, got %d", m.connRemoved)
	}
}

func TestSecurityCounts(t *testing.T) {
	m := NewModel()
	m.watch = parseWatchlist([]string{"8.8.8.8"})
	m.ports = []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:80", PID: 1},     // exposed
		{Proto: "tcp", Local: "127.0.0.1:5432", PID: 2}, // local
		{Proto: "tcp", Local: "192.168.1.5:22", PID: 3}, // lan (not exposed)
	}
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "8.8.8.8:443", State: "ESTABLISHED", PID: 1},  // public + watch
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "1.1.1.1:443", State: "ESTABLISHED", PID: 2},  // public
		{Proto: "tcp", Local: "10.0.0.1:3", Remote: "10.0.0.9:443", State: "ESTABLISHED", PID: 3}, // private
	}
	exposed, public, watch := m.securityCounts()
	if exposed != 1 {
		t.Fatalf("exposed listeners = %d, want 1", exposed)
	}
	if public != 2 {
		t.Fatalf("public connections = %d, want 2", public)
	}
	if watch != 1 {
		t.Fatalf("watchlist hits = %d, want 1", watch)
	}
}

func TestOverviewSecurityLine(t *testing.T) {
	m := NewModel()
	m.w, m.h = 100, 30
	m.lastSnap = probe.NetSnapshot{
		Hostname: "h", TakenAt: time.Unix(1, 0),
		Ifaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true, Kind: probe.IfacePhysical}},
	}
	m.selectedIface = "eth0"
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:80", PID: 1}}
	m.conns = []probe.Conn{{Proto: "tcp", Local: "10.0.0.1:1", Remote: "8.8.8.8:443", State: "ESTABLISHED", PID: 1}}

	out := m.viewOverview()
	if !strings.Contains(out, "Security:") {
		t.Fatalf("overview should include a Security line:\n%s", out)
	}
	if !strings.Contains(out, "exposed listeners") || !strings.Contains(out, "public conns") {
		t.Fatalf("security line missing expected metrics:\n%s", out)
	}
}

func TestNewListenerDetection(t *testing.T) {
	m := NewModel()
	p1 := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:80", PID: 1}

	model, _ := m.Update(connInfoMsg(probe.ConnInfo{Ports: []probe.ListenPort{p1}}))
	m = model.(Model)
	if len(m.newPorts) != 0 {
		t.Fatalf("baseline enumeration should flag no new listeners, got %d", len(m.newPorts))
	}

	p2 := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:4444", PID: 2}
	model, _ = m.Update(connInfoMsg(probe.ConnInfo{Ports: []probe.ListenPort{p1, p2}}))
	m = model.(Model)
	if !m.newPorts[portKey(p2)] {
		t.Fatal("p2 should be flagged as a new listener")
	}
	if m.newPorts[portKey(p1)] {
		t.Fatal("p1 (still present) should not be flagged new")
	}
}

func TestConfigWatchlistRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := NewModel()
	m.watchlistRaw = []string{"45.0.0.0/8", "baddomain"}
	m.saveConfig()

	got := NewModel().WithConfig()
	if got.watch == nil {
		t.Fatal("watchlist not loaded from config")
	}
	if !got.watch.match("45.1.2.3", "") {
		t.Fatal("CIDR watchlist entry not applied after reload")
	}
	if !got.watch.match("1.2.3.4", "x.baddomain.net") {
		t.Fatal("regex watchlist entry not applied after reload")
	}
	if !eqStrs(got.watchlistRaw, []string{"45.0.0.0/8", "baddomain"}) {
		t.Fatalf("raw watchlist not preserved: %v", got.watchlistRaw)
	}
}
