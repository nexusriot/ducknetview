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

func TestWatchlistAndDestinationEvents(t *testing.T) {
	m := eventsModel()
	m.watch = parseWatchlist([]string{"45.9.0.0/16"})

	conns := []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "45.9.1.2:443", State: "ESTABLISHED", PID: 5, Process: "curl"},
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "10.0.0.9:443", State: "ESTABLISHED", PID: 6, Process: "curl"},
	}
	m = feed(t, m, nil, conns)

	if countKind(m.events, "watchlist_hit") != 1 {
		t.Errorf("expected one watchlist_hit, got kinds %v", kindsOf(m.events))
	}
	// Only the public remote is a new destination; the private one is not.
	if countKind(m.events, "new_destination") != 1 {
		t.Errorf("expected one new_destination, got kinds %v", kindsOf(m.events))
	}

	// A second connection to the same host is not news again.
	conns = append(conns, probe.Conn{Proto: "tcp", Local: "10.0.0.1:3", Remote: "45.9.1.2:443", State: "ESTABLISHED", PID: 5})
	m = feed(t, m, nil, conns)
	if countKind(m.events, "new_destination") != 1 {
		t.Errorf("repeat connection to a known host logged again: %v", kindsOf(m.events))
	}
}

func TestToggleWatchFromTheTable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := eventsModel()
	m.activeTab = tabConns
	m.conns = []probe.Conn{conn(10, "curl", "10.0.0.1:5000", "45.9.1.2:443", 0, 0)}
	m.connsCursor = 0

	ip, ok := m.watchTarget()
	if !ok || ip != "45.9.1.2" {
		t.Fatalf("watchTarget = (%q, %v), want the remote IP", ip, ok)
	}

	model, _ := m.Update(key("w"))
	m = model.(Model)
	if !strings.Contains(m.status, "Watching 45.9.1.2") {
		t.Fatalf("status = %q", m.status)
	}
	if !m.isWatched("45.9.1.2:443") {
		t.Error("the remote should now match the watchlist")
	}
	// It persists, so the highlight survives a restart.
	if reloaded := NewModel().WithConfig(); !reloaded.isWatched("45.9.1.2:443") {
		t.Error("watchlist entry did not survive a reload")
	}

	// Pressing it again stops watching.
	model, _ = m.Update(key("w"))
	m = model.(Model)
	if m.isWatched("45.9.1.2:443") {
		t.Error("second press should have removed the entry")
	}
	if !strings.Contains(m.status, "Removed") {
		t.Errorf("status = %q, want a removal", m.status)
	}
}

func TestWatchTargetAcrossConnViews(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabConns
	m.conns = []probe.Conn{conn(10, "curl", "10.0.0.1:5000", "8.8.8.8:443", 0, 0)}
	m.connsCursor = 0

	if ip, _ := m.watchTarget(); ip != "8.8.8.8" {
		t.Errorf("flat view target = %q", ip)
	}
	m.connsGroup = true
	if ip, _ := m.watchTarget(); ip != "8.8.8.8" {
		t.Errorf("grouped view target = %q", ip)
	}
	m.connsGroup = false
	m.showClosed = true
	m.closedConns = []closedConn{{conn: conn(10, "curl", "10.0.0.1:1", "1.2.3.4:443", 1, 1), closedAt: time.Now()}}
	if ip, _ := m.watchTarget(); ip != "1.2.3.4" {
		t.Errorf("closed view target = %q", ip)
	}

	// Not a connections tab, or nothing selected.
	m.activeTab = tabPorts
	if _, ok := m.watchTarget(); ok {
		t.Error("watchTarget should only apply to the connections tab")
	}
}
