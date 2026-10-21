package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Aggregate bandwidth totals sum real interfaces and exclude loopback.
func TestTotalsExcludeLoopback(t *testing.T) {
	m := NewModel()
	m.w = 80
	snap := probe.NetSnapshot{
		TakenAt: time.Unix(1, 0),
		Ifaces: []probe.IfaceInfo{
			{Name: "eth0", Kind: probe.IfacePhysical, IsUp: true, RxBps: 100, TxBps: 200},
			{Name: "wg0", Kind: probe.IfaceVirt, IsUp: true, RxBps: 50, TxBps: 25},
			{Name: "lo", Kind: probe.IfaceLoopback, IsUp: true, RxBps: 9999, TxBps: 9999},
		},
	}
	model, _ := m.Update(snapMsg(snap))
	m = model.(Model)
	if len(m.totalRxHist) != 1 || m.totalRxHist[0] != 150 {
		t.Fatalf("total RX should be 150 (loopback excluded), got %v", m.totalRxHist)
	}
	if m.totalTxHist[0] != 225 {
		t.Fatalf("total TX should be 225, got %v", m.totalTxHist)
	}
}

func TestSessionTotalsAccumulate(t *testing.T) {
	m := totalsModel()
	t0 := time.Now()

	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 1000, 500)
	m.updateRates([]probe.Conn{c}, t0)

	c.RxBytes, c.TxBytes = 3000, 1500
	m.updateRates([]probe.Conn{c}, t0.Add(2*time.Second))

	if got := m.procTotal(10); got.rx != 2000 || got.tx != 1000 {
		t.Errorf("process total = %+v, want rx 2000 tx 1000", got)
	}
	if got := m.hostTotal("1.2.3.4"); got.rx != 2000 || got.tx != 1000 {
		t.Errorf("host total = %+v, want rx 2000 tx 1000", got)
	}
	if m.sessionTotal.rx != 2000 || m.sessionTotal.tx != 1000 {
		t.Errorf("session total = %+v, want rx 2000 tx 1000", m.sessionTotal)
	}

	c.RxBytes += 500
	m.updateRates([]probe.Conn{c}, t0.Add(4*time.Second))
	if got := m.procTotal(10).rx; got != 2500 {
		t.Errorf("process total after third poll = %d, want 2500", got)
	}
}

// The point of totals is that they outlive the connection: a finished download
// has no rate at all but is exactly what the user is looking for.
func TestTotalsOutliveTheConnection(t *testing.T) {
	m := totalsModel()
	t0 := time.Now()

	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 0, 0)
	m.updateRates([]probe.Conn{c}, t0)
	c.RxBytes = 9_000_000
	m.updateRates([]probe.Conn{c}, t0.Add(time.Second))

	// The connection goes away entirely.
	m.updateRates(nil, t0.Add(2*time.Second))

	if got := m.procTotal(10).rx; got != 9_000_000 {
		t.Errorf("process total after the connection closed = %d, want it retained", got)
	}
	if got := m.hostTotal("1.2.3.4").rx; got != 9_000_000 {
		t.Errorf("host total after the connection closed = %d, want it retained", got)
	}
	if m.connRate(c).any() {
		t.Error("a closed connection should have no current rate")
	}
}

// Totals accumulate across sockets, so a process opening a fresh connection to
// the same host keeps adding to the same running total.
func TestTotalsAggregateAcrossSockets(t *testing.T) {
	m := totalsModel()
	t0 := time.Now()

	first := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 0, 0)
	m.updateRates([]probe.Conn{first}, t0)
	first.RxBytes = 1000
	m.updateRates([]probe.Conn{first}, t0.Add(time.Second))

	second := conn(10, "curl", "10.0.0.1:5001", "1.2.3.4:443", 0, 0)
	m.updateRates([]probe.Conn{second}, t0.Add(2*time.Second))
	second.RxBytes = 4000
	m.updateRates([]probe.Conn{second}, t0.Add(3*time.Second))

	if got := m.procTotal(10).rx; got != 5000 {
		t.Errorf("process total across two sockets = %d, want 5000", got)
	}
	if got := m.hostTotal("1.2.3.4").rx; got != 5000 {
		t.Errorf("host total across two sockets = %d, want 5000", got)
	}
}

// A day of browsing touches thousands of hosts; the tables must stay bounded
// without losing the biggest talkers.
func TestTalkerTablesAreBounded(t *testing.T) {
	m := totalsModel()
	t0 := time.Now()

	whale := "203.0.113.99"
	m.hostTotals = map[string]*talker{whale: {key: whale, label: whale, totals: byteTotals{rx: 1 << 40}}}

	for i := 0; i < maxTalkers+200; i++ {
		host := "198.51." + strconv.Itoa(i/256) + "." + strconv.Itoa(i%256)
		c := conn(int32(i), "p", "10.0.0.1:1", host+":443", 0, 0)
		m.updateRates([]probe.Conn{c}, t0)
		c.RxBytes = uint64(i + 1)
		m.updateRates([]probe.Conn{c}, t0.Add(time.Second))
	}

	if len(m.hostTotals) > maxTalkers {
		t.Errorf("host totals grew to %d entries, past the %d cap", len(m.hostTotals), maxTalkers)
	}
	if len(m.procTotals) > maxTalkers {
		t.Errorf("process totals grew to %d entries, past the %d cap", len(m.procTotals), maxTalkers)
	}
	if got := m.hostTotal(whale).rx; got != 1<<40 {
		t.Error("eviction dropped the biggest talker, which is the one worth keeping")
	}
}

func TestTotalsToggleSwapsColumnsAndSort(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabProcs
	m.hasThroughput = true
	m.procs = []probe.ProcNet{{PID: 1, Name: "fast-now"}, {PID: 2, Name: "big-earlier"}}
	// PID 1 is busy right now; PID 2 moved far more over the session.
	m.procRates = map[int32]rateSample{1: {rx: 5_000_000}}
	m.procTotals = map[int32]*talker{
		1: {label: "fast-now", totals: byteTotals{rx: 5_000_000}},
		2: {label: "big-earlier", totals: byteTotals{rx: 900_000_000}},
	}
	m.procsSortCol = indexOfCol(t, procsSortCols, "rx")
	m.procsSortDesc = true

	if got := m.filteredProcs()[0].PID; got != 1 {
		t.Errorf("rate sort put PID %d first, want the currently-busy process", got)
	}
	if strings.Contains(m.renderProcsText(), "RX∑") {
		t.Error("totals header shown while in rate mode")
	}

	model, _ := m.Update(key("t"))
	m = model.(Model)

	if !m.showTotals {
		t.Fatal("t should switch to session totals")
	}
	text := m.renderProcsText()
	if !strings.Contains(text, "RX∑") {
		t.Errorf("totals header missing after the toggle:\n%s", text)
	}
	if got := m.filteredProcs()[0].PID; got != 2 {
		t.Errorf("totals sort put PID %d first, want the biggest session transfer", got)
	}
	if !strings.Contains(text, "858M") {
		t.Errorf("session total missing from the row:\n%s", text)
	}
}
