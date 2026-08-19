package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func ratesModel() Model {
	m := NewModel()
	m.connsVP.Width = 150
	m.connsVP.Height = 20
	m.procsVP.Width = 120
	m.procsVP.Height = 20
	m.portsVP.Width = 120
	m.portsVP.Height = 20
	return m
}

func TestUpdateRatesDiffsCounters(t *testing.T) {
	m := ratesModel()
	t0 := time.Now()

	conns := []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:5000", Remote: "1.2.3.4:443", PID: 10, RxBytes: 1000, TxBytes: 500},
	}
	m.updateRates(conns, t0)

	// The first enumeration only establishes a baseline.
	if r := m.connRate(conns[0]); r.any() {
		t.Errorf("first sample produced a rate: %+v", r)
	}
	if !m.hasThroughput {
		t.Error("non-zero counters should latch hasThroughput on")
	}

	conns[0].RxBytes, conns[0].TxBytes = 3000, 1500
	m.updateRates(conns, t0.Add(2*time.Second))

	r := m.connRate(conns[0])
	if r.rx != 1000 || r.tx != 500 {
		t.Errorf("rate = %+v, want rx 1000 tx 500 B/s", r)
	}
	// Per-process throughput is the sum over that process's connections.
	if pr := m.procRate(10); pr.rx != 1000 || pr.tx != 500 {
		t.Errorf("proc rate = %+v, want rx 1000 tx 500 B/s", pr)
	}
}

func TestUpdateRatesAggregatesPerProcess(t *testing.T) {
	m := ratesModel()
	t0 := time.Now()

	conns := []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", PID: 7, RxBytes: 100},
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", PID: 7, RxBytes: 200},
		{Proto: "tcp", Local: "10.0.0.1:3", Remote: "3.3.3.3:443", PID: 9, RxBytes: 50},
	}
	m.updateRates(conns, t0)

	conns[0].RxBytes = 1100 // +1000
	conns[1].RxBytes = 2200 // +2000
	conns[2].RxBytes = 550  // +500
	m.updateRates(conns, t0.Add(time.Second))

	if pr := m.procRate(7); pr.rx != 3000 {
		t.Errorf("PID 7 rx = %v, want 3000 (sum of both connections)", pr.rx)
	}
	if pr := m.procRate(9); pr.rx != 500 {
		t.Errorf("PID 9 rx = %v, want 500", pr.rx)
	}
}

// A socket that vanishes and a counter that goes backwards must not produce
// nonsense rates (the unsigned subtraction would otherwise wrap).
func TestUpdateRatesHandlesResetsAndGaps(t *testing.T) {
	m := ratesModel()
	t0 := time.Now()

	conns := []probe.Conn{{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", PID: 1, RxBytes: 1_000_000}}
	m.updateRates(conns, t0)

	conns[0].RxBytes = 10 // counter reset (PID recycled onto the same tuple)
	m.updateRates(conns, t0.Add(time.Second))
	if r := m.connRate(conns[0]); r.rx != 0 {
		t.Errorf("counter reset produced rate %v, want 0", r.rx)
	}

	// A brand-new connection has nothing to diff against.
	fresh := probe.Conn{Proto: "tcp", Local: "10.0.0.1:9", Remote: "9.9.9.9:443", PID: 2, RxBytes: 5000}
	m.updateRates([]probe.Conn{fresh}, t0.Add(2*time.Second))
	if r := m.connRate(fresh); r.any() {
		t.Errorf("unseen connection produced rate %+v, want zero", r)
	}
}

func TestConnsTableThroughputColumns(t *testing.T) {
	m := ratesModel()
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:5000", Remote: "1.2.3.4:443", State: "ESTABLISHED", PID: 10, Process: "curl"},
	}

	if strings.Contains(m.renderConnsText(), " RX ") {
		t.Error("RX column shown before any throughput data was available")
	}

	m.hasThroughput = true
	m.connRates = map[string]rateSample{connKey(m.conns[0]): {rx: 2 * 1024 * 1024, tx: 4096}}

	text := m.renderConnsText()
	if !strings.Contains(text, "RX") || !strings.Contains(text, "TX") {
		t.Fatalf("throughput columns missing:\n%s", text)
	}
	// Rates render compactly so the columns stay narrow.
	if !strings.Contains(text, "2.0M") || !strings.Contains(text, "4.0k") {
		t.Errorf("compact rates missing from row:\n%s", text)
	}
	// The remote port's service name is resolved for connections too.
	if !strings.Contains(text, "https") {
		t.Errorf("SVC column missing https for port 443:\n%s", text)
	}
}

func TestProcsTableUserAndContainerColumns(t *testing.T) {
	m := ratesModel()
	m.procs = []probe.ProcNet{{PID: 42, Name: "nginx", User: "www-data", ConnCount: 3}}

	text := m.renderProcsText()
	if !strings.Contains(text, "USER") || !strings.Contains(text, "www-data") {
		t.Fatalf("USER column missing:\n%s", text)
	}
	if strings.Contains(text, "CONTAINER") {
		t.Error("CONTAINER column shown although no process is containerised")
	}

	m.procs[0].Container = "3f2a1b4c5d6e"
	text = m.renderProcsText()
	if !strings.Contains(text, "CONTAINER") || !strings.Contains(text, "3f2a1b4c5d6e") {
		t.Fatalf("CONTAINER column missing once a container is present:\n%s", text)
	}
}

func TestPortsTableUserColumn(t *testing.T) {
	m := ratesModel()
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd", User: "root"}}

	text := m.renderPortsText()
	if !strings.Contains(text, "USER") || !strings.Contains(text, "root") {
		t.Fatalf("USER column missing:\n%s", text)
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

func TestSortByThroughput(t *testing.T) {
	m := ratesModel()
	slow := probe.Conn{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", PID: 1}
	fast := probe.Conn{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", PID: 2}
	m.conns = []probe.Conn{slow, fast}
	m.connRates = map[string]rateSample{
		connKey(slow): {rx: 10},
		connKey(fast): {rx: 9999},
	}
	m.connsSortCol = indexOfCol(t, connsSortCols, "rx")

	rows := m.filteredConns()
	if rows[0].PID != 2 {
		t.Errorf("rx sort put PID %d first, want the busiest (2)", rows[0].PID)
	}

	m.connsSortDesc = true
	rows = m.filteredConns()
	if rows[0].PID != 1 {
		t.Errorf("reversed rx sort put PID %d first, want the quietest (1)", rows[0].PID)
	}

	m.procs = []probe.ProcNet{{PID: 1}, {PID: 2}}
	m.procRates = map[int32]rateSample{1: {tx: 5}, 2: {tx: 500}}
	m.procsSortCol = indexOfCol(t, procsSortCols, "tx")
	m.procsSortDesc = true // the processes tab defaults to descending
	if got := m.filteredProcs()[0].PID; got != 2 {
		t.Errorf("tx sort put PID %d first, want the busiest (2)", got)
	}
}

func indexOfCol(t *testing.T, cols []string, name string) int {
	t.Helper()
	for i, c := range cols {
		if c == name {
			return i
		}
	}
	t.Fatalf("sort column %q not found in %v", name, cols)
	return 0
}
