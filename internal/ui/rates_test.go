package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

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

// Speeding up the refresh rate must speed up the socket views too, but never
// past the floor that keeps enumeration affordable.
func TestSocketIntervalFollowsRefreshRate(t *testing.T) {
	m := NewModel()

	m.refreshInterval = time.Second
	if got := m.socketInterval(); got != connFetchFloor {
		t.Errorf("socketInterval at 1s refresh = %s, want the %s floor", got, connFetchFloor)
	}

	m.refreshInterval = 10 * time.Second
	if got := m.socketInterval(); got != 10*time.Second {
		t.Errorf("socketInterval at 10s refresh = %s, want 10s", got)
	}
}

func TestRateHistoryFeedsSparklinesAndIsPruned(t *testing.T) {
	m := totalsModel()
	t0 := time.Now()

	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 0, 0)
	for i := 1; i <= 4; i++ {
		c.RxBytes = uint64(i) * 1000
		m.updateRates([]probe.Conn{c}, t0.Add(time.Duration(i)*time.Second))
	}

	h := m.connRateHist[connKey(c)]
	if h == nil || len(h.rx) != 3 {
		t.Fatalf("connection history = %+v, want three samples (the first poll is a baseline)", h)
	}
	if m.procRateHist[10] == nil {
		t.Error("process history was not recorded")
	}

	// History is keyed by live socket, so a closed connection releases it.
	m.updateRates(nil, t0.Add(10*time.Second))
	if _, ok := m.connRateHist[connKey(c)]; ok {
		t.Error("history for a closed connection was not pruned")
	}
}

func TestRateHistoryIsCapped(t *testing.T) {
	h := &rateHistory{}
	for i := 0; i < rateHistoryLen+25; i++ {
		h.add(rateSample{rx: float64(i), tx: float64(i)})
	}
	if len(h.rx) != rateHistoryLen || len(h.tx) != rateHistoryLen {
		t.Fatalf("history holds %d/%d samples, want %d", len(h.rx), len(h.tx), rateHistoryLen)
	}
	if h.rx[len(h.rx)-1] != float64(rateHistoryLen+24) {
		t.Error("the newest sample should be retained")
	}
}

func TestFormatRTTAndRetrans(t *testing.T) {
	rtt := map[time.Duration]string{
		0:                       "-",
		500 * time.Microsecond:  "<1ms",
		134 * time.Millisecond:  "134ms",
		2500 * time.Millisecond: "2.5s",
	}
	for d, want := range rtt {
		if got := formatRTT(d); got != want {
			t.Errorf("formatRTT(%s) = %q, want %q", d, got, want)
		}
		if len(formatRTT(d)) > 6 {
			t.Errorf("formatRTT(%s) = %q, wider than its column", d, formatRTT(d))
		}
	}

	retrans := map[uint32]string{0: "-", 7: "7", 999: "999", 4500: "4k"}
	for n, want := range retrans {
		if got := formatRetrans(n); got != want {
			t.Errorf("formatRetrans(%d) = %q, want %q", n, got, want)
		}
		if len(formatRetrans(n)) > 4 {
			t.Errorf("formatRetrans(%d) = %q, wider than its column", n, formatRetrans(n))
		}
	}
}

func TestRateUnitToggle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := eventsModel()
	m.activeTab = tabConns
	m.hasThroughput = true
	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 1, 1)
	m.conns = []probe.Conn{c}
	m.connRates = map[string]rateSample{connKey(c): {rx: 1024 * 1024}}

	if !strings.Contains(m.renderConnsText(), "1.0M") {
		t.Errorf("bytes mode should show 1.0M:\n%s", m.renderConnsText())
	}

	model, _ := m.Update(key("U"))
	m = model.(Model)
	text := m.renderConnsText()
	// The same rate is 8.4 Mbit/s, and the header says which unit it is.
	if !strings.Contains(text, "8.4M") {
		t.Errorf("bits mode should show 8.4M:\n%s", text)
	}
	if !strings.Contains(text, "RXb") {
		t.Errorf("header should mark the unit:\n%s", text)
	}
	if !strings.Contains(m.status, "bits/s") {
		t.Errorf("status = %q", m.status)
	}
	if !NewModel().WithConfig().rateUnitIsBits() {
		t.Error("the unit choice did not persist")
	}

	model, _ = m.Update(key("U"))
	if model.(Model).rateUnitIsBits() {
		t.Error("U should toggle back to bytes")
	}
}
