package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nexusriot/ducknetview/internal/probe"
)

func sampleFilterModel() Model {
	m := NewModel()
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:80", State: "ESTABLISHED", PID: 1},
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:80", State: "TIME_WAIT", PID: 2},
		{Proto: "udp", Local: "10.0.0.1:3", Remote: "3.3.3.3:53", State: "", PID: 3},
		{Proto: "tcp", Local: "fe80::1:4", Remote: "2606:4700::1:443", State: "ESTABLISHED", PID: 4},
	}
	return m
}

func TestConnFilters(t *testing.T) {
	m := sampleFilterModel()
	m.protoFilter = 1 // tcp
	if n := len(m.filteredConns()); n != 3 {
		t.Fatalf("tcp filter: expected 3, got %d", n)
	}

	m = sampleFilterModel()
	m.connStateFilter = 1 // established only
	if n := len(m.filteredConns()); n != 2 {
		t.Fatalf("established filter: expected 2, got %d", n)
	}

	m = sampleFilterModel()
	m.connStateFilter = 2 // active (exclude teardown)
	for _, c := range m.filteredConns() {
		if c.State == "TIME_WAIT" {
			t.Fatal("active filter must drop TIME_WAIT")
		}
	}
	if n := len(m.filteredConns()); n != 3 {
		t.Fatalf("active filter: expected 3, got %d", n)
	}

	m = sampleFilterModel()
	m.ipVerFilter = 2 // IPv6
	if n := len(m.filteredConns()); n != 1 {
		t.Fatalf("ipv6 filter: expected 1, got %d", n)
	}
	m.ipVerFilter = 1 // IPv4
	if n := len(m.filteredConns()); n != 3 {
		t.Fatalf("ipv4 filter: expected 3, got %d", n)
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

func TestOnlyMineFilter(t *testing.T) {
	m := ratesModel()
	m.currentUser = "vlad"
	m.ports = []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd", User: "root"},
		{Proto: "tcp", Local: "127.0.0.1:8384", PID: 2, Process: "syncthing", User: "vlad"},
		{Proto: "tcp", Local: "0.0.0.0:9", PID: 3, Process: "unknown"}, // owner unreadable
	}
	m.procs = []probe.ProcNet{
		{PID: 1, Name: "sshd", User: "root"},
		{PID: 2, Name: "syncthing", User: "vlad"},
	}
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", PID: 1, User: "root"},
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", PID: 2, User: "vlad"},
	}

	if got := len(m.filteredPorts()); got != 3 {
		t.Fatalf("unfiltered ports = %d, want 3", got)
	}

	m.onlyMine = true
	// Rows whose owner could not be read are kept rather than silently dropped.
	if got := len(m.filteredPorts()); got != 2 {
		t.Errorf("filtered ports = %d, want 2 (mine + unknown owner)", got)
	}
	if got := len(m.filteredProcs()); got != 1 {
		t.Errorf("filtered procs = %d, want 1", got)
	}
	if got := len(m.filteredConns()); got != 1 {
		t.Errorf("filtered conns = %d, want 1", got)
	}
	if !strings.Contains(m.filterLabel(), "mine") {
		t.Errorf("filterLabel = %q, want it to mention mine", m.filterLabel())
	}

	// Without a resolvable current user the filter must be inert, not empty out
	// every table.
	m.currentUser = ""
	if got := len(m.filteredProcs()); got != 2 {
		t.Errorf("procs with unknown current user = %d, want 2 (filter inert)", got)
	}
}

func TestGroupedViewRanksHostsByTraffic(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns
	m.connsGroup = true
	m.hasThroughput = true

	quiet := conn(1, "a", "10.0.0.1:1", "1.1.1.1:443", 0, 0)
	loud := conn(2, "b", "10.0.0.1:2", "2.2.2.2:443", 0, 0)
	m.conns = []probe.Conn{quiet, loud}
	m.connRates = map[string]rateSample{
		connKey(quiet): {rx: 10},
		connKey(loud):  {rx: 10_000_000},
	}
	m.groupSortCol = indexOfCol(t, groupSortCols, "bytes")
	m.groupSortDesc = true

	groups := m.filteredGroups()
	if groups[0].ip != "2.2.2.2" {
		t.Errorf("bytes sort put %s first, want the busiest host", groups[0].ip)
	}
	if !strings.Contains(m.renderConnsText(), "9.5M") {
		t.Errorf("grouped view is missing the host throughput column:\n%s", m.renderConnsText())
	}
}

// A click on a data row must still select it rather than sorting.
func TestClickingRowStillSelects(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabProcs
	m.procs = []probe.ProcNet{{PID: 1, Name: "a"}, {PID: 2, Name: "b"}, {PID: 3, Name: "c"}}

	before := m.sortKey()
	model, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: tableContentX, Y: 4 + tableHeaderLines + 1,
	})
	m = model.(Model)
	if m.procsCursor != 1 {
		t.Errorf("row cursor = %d, want the clicked row (1)", m.procsCursor)
	}
	if m.sortKey() != before {
		t.Error("clicking a data row changed the sort")
	}
}

func sampleConnsModel() Model {
	m := NewModel()
	m.connsVP.Width = 120
	m.connsVP.Height = 20
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:5000", Remote: "1.2.3.4:443", State: "ESTABLISHED", PID: 10, Process: "curl"},
		{Proto: "tcp", Local: "10.0.0.1:6000", Remote: "5.6.7.8:80", State: "TIME_WAIT", PID: 20, Process: "wget"},
	}
	return m
}

func TestCopyTargetConn(t *testing.T) {
	m := sampleConnsModel()
	m.activeTab = tabConns
	m.connsCursor = 0
	text, what, ok := m.copyTarget()
	if !ok {
		t.Fatal("expected a copy target")
	}
	if what != "connection row" {
		t.Fatalf("what = %q", what)
	}
	if !strings.Contains(text, "1.2.3.4:443") || !strings.Contains(text, "curl") {
		t.Fatalf("unexpected copy text: %q", text)
	}
}

func TestCopyTargetPort(t *testing.T) {
	m := NewModel()
	m.activeTab = tabPorts
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 99, Process: "sshd"}}
	text, what, ok := m.copyTarget()
	if !ok || what != "port row" {
		t.Fatalf("ok=%v what=%q", ok, what)
	}
	if !strings.Contains(text, "0.0.0.0:22") || !strings.Contains(text, "sshd") {
		t.Fatalf("unexpected copy text: %q", text)
	}
}

func TestCopyTargetOutOfRange(t *testing.T) {
	m := NewModel()
	m.activeTab = tabConns
	m.connsCursor = 5 // no conns loaded
	if _, _, ok := m.copyTarget(); ok {
		t.Fatal("expected no copy target when cursor is out of range")
	}
}

func TestTableNavCursorClamp(t *testing.T) {
	m := sampleConnsModel()
	m.activeTab = tabConns

	m.tableNav(tea.KeyMsg{Type: tea.KeyDown})
	if m.connsCursor != 1 {
		t.Fatalf("after down: cursor=%d want 1", m.connsCursor)
	}
	m.tableNav(tea.KeyMsg{Type: tea.KeyDown}) // clamp at last row
	if m.connsCursor != 1 {
		t.Fatalf("after second down: cursor=%d want clamped 1", m.connsCursor)
	}
	m.tableNav(tea.KeyMsg{Type: tea.KeyUp})
	if m.connsCursor != 0 {
		t.Fatalf("after up: cursor=%d want 0", m.connsCursor)
	}
	m.tableNav(tea.KeyMsg{Type: tea.KeyEnd})
	if m.connsCursor != 1 {
		t.Fatalf("after end: cursor=%d want 1", m.connsCursor)
	}
	m.tableNav(tea.KeyMsg{Type: tea.KeyHome})
	if m.connsCursor != 0 {
		t.Fatalf("after home: cursor=%d want 0", m.connsCursor)
	}
}

func TestMouseRowIndex(t *testing.T) {
	vp := viewport.New(80, 10)

	// Above the data area (header block / borders) is not a data row.
	if _, ok := mouseRowIndex(&vp, 3); ok {
		t.Fatal("y=3 (above content) should be invalid")
	}
	if _, ok := mouseRowIndex(&vp, 4); ok {
		t.Fatal("y=4 (inside the table header block) should be invalid")
	}
	// First data row sits at y = topOffset(4) + tableHeaderLines(5) = 9.
	if i, ok := mouseRowIndex(&vp, 9); !ok || i != 0 {
		t.Fatalf("y=9 -> (%d,%v), want (0,true)", i, ok)
	}
	if i, ok := mouseRowIndex(&vp, 10); !ok || i != 1 {
		t.Fatalf("y=10 -> (%d,%v), want (1,true)", i, ok)
	}
	// Below the viewport (height 10 => rows [4,14)) is invalid.
	if _, ok := mouseRowIndex(&vp, 14); ok {
		t.Fatal("y=14 (below viewport) should be invalid")
	}

	// When scrolled, the same screen row maps to a later data row.
	vp.SetContent(strings.Repeat("line\n", 40))
	vp.SetYOffset(5)
	if i, ok := mouseRowIndex(&vp, 4); !ok || i != 0 {
		t.Fatalf("scrolled y=4 -> (%d,%v), want (0,true)", i, ok)
	}
}

func TestNetNSIsSearchable(t *testing.T) {
	m := eventsModel()
	m = feed(t, m, netnsPorts(), []probe.Conn{
		{Proto: "tcp", Local: "172.17.0.2:80", Remote: "172.17.0.1:5555", State: "ESTABLISHED", PID: 10, NetNS: "4f1a2b3c4d5e"},
		{Proto: "tcp", Local: "10.0.0.2:5000", Remote: "1.1.1.1:443", State: "ESTABLISHED", PID: 30},
	})

	m.portsQuery = "4f1a2b"
	if got := m.filteredPorts(); len(got) != 1 || got[0].Process != "nginx" {
		t.Errorf("searching a container id gave %+v", got)
	}
	m.portsQuery = probe.HostNetNSLabel
	if got := m.filteredPorts(); len(got) != 2 {
		t.Errorf(`searching "host" gave %d listeners, want the 2 host ones`, len(got))
	}
	m.connsQuery = "4f1a2b"
	if got := m.filteredConns(); len(got) != 1 || got[0].PID != 10 {
		t.Errorf("searching connections by container id gave %+v", got)
	}

	// Without other namespaces, "host" must not match everything invisibly.
	m2 := feed(t, eventsModel(), netnsPorts()[:1], nil)
	m2.portsQuery = probe.HostNetNSLabel
	if got := m2.filteredPorts(); len(got) != 0 {
		t.Errorf(`"host" matched %d rows on a host with no NETNS column`, len(got))
	}
}

func TestNamespacedRowsKeyApart(t *testing.T) {
	host := probe.Conn{Proto: "tcp", Local: "127.0.0.1:80", Remote: "127.0.0.1:40000"}
	inside := host
	inside.NetNS = "4f1a2b3c4d5e"
	if connKey(host) == connKey(inside) {
		t.Error("the same tuple in two namespaces shares a key")
	}
	if connKey(host) != "tcp|127.0.0.1:80|127.0.0.1:40000" {
		t.Errorf("host key changed shape: %q", connKey(host))
	}

	a := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:80", NetNS: "a"}
	b := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:80", NetNS: "b"}
	if portKey(a) == portKey(b) {
		t.Error("two containers' listeners on the same port share a key")
	}
}

// An outbound DNS query or a QUIC socket is a client, not a service. Counting
// them as "exposed listeners" makes the security summary and the Prometheus
// gauge fluctuate for no reason — and it is the same distinction the baseline
// and the event log already make.
func TestExposedCountsIgnoreEphemeralUDPClients(t *testing.T) {
	m := eventsModel()
	m.w, m.h = 160, 45
	service := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	udpService := probe.ListenPort{Proto: "udp", Local: "0.0.0.0:53", PID: 2, Process: "resolved"}
	udpClient := probe.ListenPort{Proto: "udp", Local: "0.0.0.0:47572", PID: 3, Process: "firefox"}
	m.ports = []probe.ListenPort{service, udpService, udpClient}

	exposed, _, _ := m.securityCounts()
	if exposed != 2 {
		t.Errorf("exposed listeners = %d, want 2 (the client socket is not a service)", exposed)
	}

	out := FormatMetrics(probe.NetSnapshot{}, probe.ConnInfo{Ports: m.ports}, 0, false)
	if !strings.Contains(out, `ducknetview_listeners{exposure="exposed"} 2`) {
		t.Errorf("metric counted client sockets as exposed listeners:\n%s", out)
	}
}
