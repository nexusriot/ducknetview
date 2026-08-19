package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func totalsModel() Model {
	m := ratesModel()
	m.w, m.h = 160, 45
	return m
}

// conn builds a connection with cumulative counters, as the probe layer
// reports it.
func conn(pid int32, proc, local, remote string, rx, tx uint64) probe.Conn {
	return probe.Conn{
		Proto: "tcp", Local: local, Remote: remote, State: "ESTABLISHED",
		PID: pid, Process: proc, RxBytes: rx, TxBytes: tx,
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

func TestTopTalkersPanel(t *testing.T) {
	m := totalsModel()
	m.lastSnap = probe.NetSnapshot{TakenAt: time.Now(), Hostname: "host"}

	if strings.Contains(m.viewOverview(), "Top talkers") {
		t.Error("the panel should stay hidden until traffic has been accounted")
	}

	t0 := time.Now()
	c := conn(10, "firefox", "10.0.0.1:5000", "1.2.3.4:443", 0, 0)
	m.updateRates([]probe.Conn{c}, t0)
	c.RxBytes = 500_000_000
	m.updateRates([]probe.Conn{c}, t0.Add(time.Second))

	out := m.viewOverview()
	if !strings.Contains(out, "Top talkers") {
		t.Fatalf("top talkers panel missing:\n%s", out)
	}
	if !strings.Contains(out, "firefox") || !strings.Contains(out, "1.2.3.4") {
		t.Errorf("panel should list the top process and host:\n%s", out)
	}

	// On a short terminal the panel must yield rather than push the rest of the
	// Overview out of its box.
	m.h = 16
	if strings.Contains(m.viewOverview(), "Top talkers") {
		t.Error("panel rendered on a terminal too short to hold it")
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

func TestClosedConnectionsAreRetained(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns

	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 1_000_000, 250_000)
	m = feed(t, m, nil, []probe.Conn{c})
	if len(m.closedConns) != 0 {
		t.Fatalf("first enumeration recorded %d closures", len(m.closedConns))
	}

	// The connection goes away.
	m = feed(t, m, nil, nil)

	if len(m.closedConns) != 1 {
		t.Fatalf("closed connections = %d, want 1", len(m.closedConns))
	}
	cc := m.closedConns[0]
	if cc.totals.rx != 1_000_000 || cc.totals.tx != 250_000 {
		t.Errorf("final totals = %+v, want the socket's lifetime counters", cc.totals)
	}
	if cc.conn.Process != "curl" || cc.conn.Remote != "1.2.3.4:443" {
		t.Errorf("closed row lost its identity: %+v", cc.conn)
	}
	if cc.firstSeen.IsZero() || cc.closedAt.IsZero() {
		t.Errorf("closed row missing timestamps: opened %v closed %v", cc.firstSeen, cc.closedAt)
	}
}

func TestClosedViewRendersAndToggles(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns

	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 900_000_000, 1000)
	m = feed(t, m, nil, []probe.Conn{c})
	m = feed(t, m, nil, nil)

	if strings.Contains(m.renderConnsText(), "Recently closed") {
		t.Fatal("the active view should not show closed connections")
	}

	model, _ := m.Update(key("z"))
	m = model.(Model)
	if !m.showClosed {
		t.Fatal("z should switch to the closed view")
	}

	text := m.renderConnsText()
	if !strings.Contains(text, "Recently closed") {
		t.Fatalf("closed view header missing:\n%s", text)
	}
	if !strings.Contains(text, "858M") || !strings.Contains(text, "curl") {
		t.Errorf("closed row missing its totals or process:\n%s", text)
	}
	if !strings.Contains(text, "LIFE") || !strings.Contains(text, "CLOSED") {
		t.Errorf("closed view is missing its lifetime columns:\n%s", text)
	}

	// The row is selectable, copyable and exportable like any other.
	if n := m.connsRowCount(); n != 1 {
		t.Errorf("closed row count = %d, want 1", n)
	}
	txt, what, ok := m.copyTarget()
	if !ok || what != "closed connection" || !strings.Contains(txt, "1.2.3.4:443") {
		t.Errorf("copyTarget = (%q, %q, %v)", txt, what, ok)
	}
	if _, ok := m.detailContent(); !ok {
		t.Error("detail overlay should render a closed connection")
	}

	model, _ = m.Update(key("z"))
	if model.(Model).showClosed {
		t.Error("z should toggle back to the active view")
	}
}

// Signalling from a closed row is unsafe: the PID may already belong to an
// unrelated process.
func TestClosedViewDisallowsSignalling(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns
	m.showClosed = true
	m.closedConns = []closedConn{{
		conn:     conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 1, 1),
		closedAt: time.Now(),
	}}

	if _, _, ok := m.selectedPID(); ok {
		t.Error("a closed connection should not offer a signal target")
	}
}

func TestClosedHistoryIsCappedAndOrdered(t *testing.T) {
	m := totalsModel()
	base := time.Now()

	for i := 0; i < closedConnLimit+40; i++ {
		m.closedConns = append(m.closedConns, closedConn{
			conn:     conn(int32(i), "p", "10.0.0.1:"+strconv.Itoa(i), "1.1.1.1:443", uint64(i), 0),
			closedAt: base.Add(time.Duration(i) * time.Second),
		})
	}
	m.closedConns = m.closedConns[len(m.closedConns)-closedConnLimit:]

	m.activeTab = tabConns
	m.showClosed = true
	rows := m.closedRows()
	if len(rows) != closedConnLimit {
		t.Fatalf("closed rows = %d, want the %d cap", len(rows), closedConnLimit)
	}
	// Newest first by default.
	if !rows[0].closedAt.After(rows[1].closedAt) {
		t.Errorf("closed rows are not newest-first: %v then %v", rows[0].closedAt, rows[1].closedAt)
	}

	m.closedSortCol = indexOfCol(t, closedSortCols, "bytes")
	if got := m.closedRows()[0].totals.rx; got != rows[0].totals.rx && got < rows[len(rows)-1].totals.rx {
		t.Errorf("bytes sort did not surface the biggest transfer, got %d", got)
	}
}

// A closure detected in the same poll as the rate update must still see the
// previous counters — the ordering of noteClosedConns and updateRates matters.
func TestClosedTotalsSurviveRateUpdateOrdering(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns

	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 5_000, 100)
	m = feed(t, m, nil, []probe.Conn{c})
	c.RxBytes = 4_000_000
	m = feed(t, m, nil, []probe.Conn{c})
	m = feed(t, m, nil, nil)

	if len(m.closedConns) != 1 {
		t.Fatalf("closed connections = %d, want 1", len(m.closedConns))
	}
	if got := m.closedConns[0].totals.rx; got != 4_000_000 {
		t.Errorf("closed total = %d, want the last observed counter (4000000)", got)
	}
}

func TestThroughputAlertsFireOncePerWindow(t *testing.T) {
	m := totalsModel()
	m.alertProcBps = 1_000_000
	m.alertConnBps = 0
	t0 := time.Now()

	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 0, 0)
	m = feed(t, m, nil, []probe.Conn{c})

	// 5 MB in one second: five times the threshold.
	c.RxBytes = 5_000_000
	m.prevSockBytes = map[string]sockBytes{connKey(c): {rx: 0}}
	m.lastRateSample = t0
	m.updateRates([]probe.Conn{c}, t0.Add(time.Second))
	m.noteThroughputAlerts([]probe.Conn{c}, t0.Add(time.Second))

	if n := countKind(m.events, "throughput_process"); n != 1 {
		t.Fatalf("throughput alerts = %d, want 1 (kinds %v)", n, kindsOf(m.events))
	}
	e := m.events[len(m.events)-1]
	if e.Level != levelAlert {
		t.Errorf("threshold breach level = %v, want alert", e.Level)
	}
	if !strings.Contains(e.Subject, "curl") || !strings.Contains(e.Detail, "threshold") {
		t.Errorf("alert event = %+v, want the process and the threshold", e)
	}

	// Still over the threshold on the next poll, but within the quiet window.
	m.noteThroughputAlerts([]probe.Conn{c}, t0.Add(2*time.Second))
	if n := countKind(m.events, "throughput_process"); n != 1 {
		t.Errorf("alert re-fired inside the suppression window: %d events", n)
	}

	// Once the window has passed it may fire again.
	m.noteThroughputAlerts([]probe.Conn{c}, t0.Add(alertRefire+2*time.Second))
	if n := countKind(m.events, "throughput_process"); n != 2 {
		t.Errorf("alert did not re-fire after the window: %d events", n)
	}
}

func TestThroughputAlertsDisabledByDefault(t *testing.T) {
	m := totalsModel()
	t0 := time.Now()

	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 0, 0)
	m.updateRates([]probe.Conn{c}, t0)
	c.RxBytes = 900_000_000
	m.updateRates([]probe.Conn{c}, t0.Add(time.Second))
	m.noteThroughputAlerts([]probe.Conn{c}, t0.Add(time.Second))

	if n := countKind(m.events, "throughput_process"); n != 0 {
		t.Errorf("alerts fired with no threshold configured: %d", n)
	}
}

func TestAlertSuppressionMapIsPruned(t *testing.T) {
	m := totalsModel()
	m.alertFired = map[string]time.Time{"proc:old": time.Now().Add(-10 * alertRefire)}
	m.pruneAlertHistory(time.Now())
	if len(m.alertFired) != 0 {
		t.Errorf("stale suppression entries kept: %v", m.alertFired)
	}
}

// The hook must only be queued for alerts, and only when one is configured.
func TestAlertHookQueueing(t *testing.T) {
	m := totalsModel()
	m.recordEvent(event{At: time.Now(), Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:4444"})
	if len(m.pendingHooks) != 0 {
		t.Fatal("hook queued although none was configured")
	}

	m = m.WithAlertHook("/bin/true")
	m.recordEvent(event{At: time.Now(), Level: levelInfo, Kind: "new_destination", Subject: "1.2.3.4:443"})
	if len(m.pendingHooks) != 0 {
		t.Error("info-level event queued a hook; only alerts should")
	}
	m.recordEvent(event{At: time.Now(), Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:4444"})
	if len(m.pendingHooks) != 1 {
		t.Fatalf("pending hooks = %d, want 1", len(m.pendingHooks))
	}

	cmds := m.drainAlertHooks()
	if len(cmds) != 1 {
		t.Fatalf("drained %d commands, want 1", len(cmds))
	}
	if len(m.pendingHooks) != 0 {
		t.Error("draining should empty the queue")
	}
	if msg, ok := cmds[0]().(alertHookResultMsg); !ok || msg.err != nil {
		t.Errorf("hook result = %+v, want a clean run of /bin/true", msg)
	}
}

// The hook receives the event as arguments and environment, never via a shell.
func TestAlertHookPassesEventToTheCommand(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "captured")
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script,
		[]byte("#!/bin/sh\nprintf '%s|%s|%s|%s\\n' \"$1\" \"$2\" \"$DNV_EVENT_LEVEL\" \"$DNV_EVENT_DETAIL\" > "+out+"\n"),
		0o755); err != nil {
		t.Fatal(err)
	}

	e := event{At: time.Now(), Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:4444", Detail: "exposed"}
	if msg, ok := alertHookCmd(script, e)().(alertHookResultMsg); !ok || msg.err != nil {
		t.Fatalf("hook failed: %+v", msg)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "listener_up|tcp 0.0.0.0:4444|alert|exposed\n"; string(got) != want {
		t.Errorf("hook received %q, want %q", got, want)
	}
}

func TestPortableBaselineRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")

	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	dnsClient := probe.ListenPort{Proto: "udp", Local: "10.0.0.1:47572", PID: 2, Process: "firefox"}
	backdoor := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:4444", PID: 66, Process: "nc"}

	n, err := SaveBaselineFile(path, "golden-host", []probe.ListenPort{sshd, dnsClient})
	if err != nil {
		t.Fatalf("SaveBaselineFile: %v", err)
	}
	// The ephemeral UDP socket is excluded, exactly as in the interactive path.
	if n != 1 {
		t.Errorf("wrote %d listeners, want 1", n)
	}

	offending, savedAt, err := BaselineCheckFile(path, []probe.ListenPort{sshd, backdoor})
	if err != nil {
		t.Fatalf("BaselineCheckFile: %v", err)
	}
	if len(offending) != 1 || offending[0].Local != backdoor.Local {
		t.Errorf("offending = %+v, want just the backdoor", offending)
	}
	if savedAt.IsZero() {
		t.Error("baseline file lost its timestamp")
	}

	if _, _, err := BaselineCheckFile(filepath.Join(t.TempDir(), "nope.json"), nil); err == nil {
		t.Error("a missing baseline file should be an error, not an empty baseline")
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := BaselineCheckFile(bad, nil); err == nil {
		t.Error("a baseline with no listeners should be rejected rather than passing everything")
	}
}

func TestConnsQualityColumns(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns
	m.hasThroughput = true
	m.conns = []probe.Conn{{
		Proto: "tcp", Local: "10.0.0.1:5000", Remote: "1.2.3.4:443", State: "ESTABLISHED",
		PID: 10, Process: "curl", RxBytes: 1, RTT: 134 * time.Millisecond, Retrans: 7,
	}}

	text := m.renderConnsText()
	if !strings.Contains(text, "RTT") || !strings.Contains(text, "RTX") {
		t.Fatalf("quality columns missing:\n%s", text)
	}
	if !strings.Contains(text, "134ms") || !strings.Contains(text, "7") {
		t.Errorf("RTT/retransmits missing from the row:\n%s", text)
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

func TestSortByRTTAndRetrans(t *testing.T) {
	m := totalsModel()
	quick := probe.Conn{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", PID: 1, RTT: time.Millisecond}
	slow := probe.Conn{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", PID: 2, RTT: 900 * time.Millisecond, Retrans: 12}
	m.conns = []probe.Conn{quick, slow}

	m.connsSortCol = indexOfCol(t, connsSortCols, "rtt")
	if got := m.filteredConns()[0].PID; got != 2 {
		t.Errorf("rtt sort put PID %d first, want the slowest connection", got)
	}

	m.connsSortCol = indexOfCol(t, connsSortCols, "retrans")
	if got := m.filteredConns()[0].PID; got != 2 {
		t.Errorf("retrans sort put PID %d first, want the lossiest connection", got)
	}
}

func TestEventsSearchAndLevelFilter(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabEvents
	now := time.Now()
	m.recordEvent(event{At: now, Level: levelInfo, Kind: "new_destination", Subject: "1.2.3.4:443"})
	m.recordEvent(event{At: now, Level: levelWarn, Kind: "listener_up", Subject: "tcp 0.0.0.0:8080"})
	m.recordEvent(event{At: now, Level: levelAlert, Kind: "watchlist_hit", Subject: "45.9.1.2:443"})

	if got := len(m.eventRows()); got != 3 {
		t.Fatalf("unfiltered events = %d, want 3", got)
	}

	// l cycles all -> warn+ -> alerts only.
	model, _ := m.Update(key("l"))
	m = model.(Model)
	if got := len(m.eventRows()); got != 2 {
		t.Errorf("warn+ filter kept %d events, want 2", got)
	}
	model, _ = m.Update(key("l"))
	m = model.(Model)
	rows := m.eventRows()
	if len(rows) != 1 || rows[0].Kind != "watchlist_hit" {
		t.Errorf("alerts-only filter kept %v, want just the watchlist hit", kindsOf(rows))
	}
	if got := m.activeRowCount(); got != 1 {
		t.Errorf("activeRowCount = %d, want it to follow the filter", got)
	}
	model, _ = m.Update(key("l"))
	m = model.(Model)
	if got := len(m.eventRows()); got != 3 {
		t.Errorf("filter did not cycle back to all: %d events", got)
	}

	// Search narrows by kind, subject or detail.
	m.eventsQuery = "listener"
	rows = m.eventRows()
	if len(rows) != 1 || rows[0].Kind != "listener_up" {
		t.Errorf("search kept %v, want the listener event", kindsOf(rows))
	}
	if !strings.Contains(m.renderEventsText(), "level: all") {
		t.Error("the events subtitle should report the level filter")
	}
}

func TestEventsSearchBoxCapturesKeys(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabEvents
	m.recordEvent(event{At: time.Now(), Level: levelWarn, Kind: "listener_up", Subject: "tcp 0.0.0.0:8080"})

	model, _ := m.Update(key("/"))
	m = model.(Model)
	if !m.eventsSearching || !m.anySearching() {
		t.Fatal("/ should focus the events search box")
	}

	// Keys that are commands elsewhere must reach the input while it is focused.
	for _, k := range []string{"l", "c", "t", "z"} {
		model, _ = m.Update(key(k))
		m = model.(Model)
	}
	if m.eventLevelFilter != 0 || len(m.events) == 0 {
		t.Error("command keys leaked past the focused search box")
	}
	if got := m.eventsSearch.Value(); got != "lctz" {
		t.Errorf("search box contains %q, want %q", got, "lctz")
	}
}

func TestSortIndicatorMarksActiveColumn(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabPorts
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd", User: "root"}}

	m.portsSortCol = indexOfCol(t, portsSortCols, "port")
	m.portsSortDesc = false
	text := stripANSI(m.renderPortsText())
	if !strings.Contains(text, "LOCAL↑") {
		t.Errorf("ascending arrow missing from the sorted column:\n%s", text)
	}

	m.portsSortDesc = true
	if text = stripANSI(m.renderPortsText()); !strings.Contains(text, "LOCAL↓") {
		t.Errorf("descending arrow missing:\n%s", text)
	}
	// Only the active column is marked.
	if strings.Contains(text, "PID↓") || strings.Contains(text, "USER↓") {
		t.Errorf("arrow drawn on an inactive column:\n%s", text)
	}

	// The arrow must not widen the column, or every row below it misaligns.
	for _, line := range strings.Split(stripANSI(m.renderPortsText()), "\n") {
		if w := lipgloss.Width(line); w > m.portsVP.Width {
			t.Fatalf("line wider than the viewport (%d): %q", w, line)
		}
	}
}

func TestColumnHitTesting(t *testing.T) {
	cols := colSpecs{
		{key: "a", title: "AA", width: 4, sort: "alpha"},
		{key: "b", title: "BB", width: 3},
		{key: "c", title: "CC", width: 5, sort: "gamma"},
	}

	// Layout: [0,4) a, [4,6) gap, [6,9) b, [9,11) gap, [11,16) c
	cases := map[int]string{0: "alpha", 3: "alpha", 4: "", 6: "", 8: "", 11: "gamma", 15: "gamma", 99: ""}
	for x, want := range cases {
		got, ok := cols.keyAt(x)
		if want == "" && ok {
			t.Errorf("keyAt(%d) = %q, want no hit", x, got)
		}
		if want != "" && (!ok || got != want) {
			t.Errorf("keyAt(%d) = (%q, %v), want %q", x, got, ok, want)
		}
	}

	if got := cols.span(); got != 16 {
		t.Errorf("span = %d, want 16", got)
	}
}

func TestClickingHeaderSortsAndReverses(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabProcs
	m.procs = []probe.ProcNet{{PID: 2, Name: "b", ConnCount: 1}, {PID: 1, Name: "a", ConnCount: 5}}

	cols := m.procsColumns()
	// x of the NAME column: gutter + the PID column + the separator.
	x := tableContentX + cols.width("pid") + len(colSep)
	y := 4 + headerLineIndex

	click := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: x, Y: y}
	model, _ := m.Update(click)
	m = model.(Model)

	if got := m.sortKey(); got != "name" {
		t.Fatalf("clicking the NAME header sorted by %q", got)
	}
	firstDir := m.sortDesc()

	// Clicking the active column again reverses it, as a spreadsheet would.
	model, _ = m.Update(click)
	m = model.(Model)
	if m.sortKey() != "name" || m.sortDesc() == firstDir {
		t.Errorf("second click gave (%q, desc=%v), want the same column reversed", m.sortKey(), m.sortDesc())
	}

	// A click on an unsortable column changes nothing.
	m.activeTab = tabProcs
	m.procs[0].Container = "abc123def456"
	before := m.sortKey()
	cols = m.procsColumns()
	contX := tableContentX + cols.width("pid") + len(colSep) + cols.width("name") + len(colSep) +
		cols.width("user") + len(colSep)
	model, _ = m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: contX, Y: y})
	if got := model.(Model).sortKey(); got != before {
		t.Errorf("clicking CONTAINER changed the sort to %q", got)
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

// Every view of the Connections tab must hit-test against its own layout.
func TestActiveColumnsFollowTheConnsView(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns

	if got := m.activeColumns().width("state"); got == 0 {
		t.Error("flat view columns not selected")
	}
	m.connsGroup = true
	if got := m.activeColumns().width("states"); got == 0 {
		t.Error("grouped view columns not selected")
	}
	m.showClosed = true
	if got := m.activeColumns().width("life"); got == 0 {
		t.Error("closed view columns not selected")
	}
}

func TestMatcherLiteralAndRegex(t *testing.T) {
	lit := newMatcher("Curl")
	if !lit.match("curl") || !lit.match("CURL") {
		t.Error("literal matching should be case-insensitive")
	}
	if lit.match("wget") {
		t.Error("literal matcher matched an unrelated field")
	}

	re := newMatcher(`re:^10\.0\..*:443$`)
	if re.err != nil {
		t.Fatalf("valid expression rejected: %v", re.err)
	}
	if !re.match("10.0.0.5:443") {
		t.Error("regex should match the address it describes")
	}
	if re.match("10.0.0.5:80") || re.match("192.168.1.5:443") {
		t.Error("regex matched an address outside its pattern")
	}
	// Regexes are case-insensitive too.
	if !newMatcher("re:FIREFOX").match("firefox-bin") {
		t.Error("regex should be case-insensitive")
	}

	// A broken expression matches nothing and says why, rather than matching
	// everything and looking like the filter is broken.
	bad := newMatcher("re:[unclosed")
	if bad.err == nil {
		t.Fatal("invalid expression accepted")
	}
	if bad.match("anything") {
		t.Error("invalid expression should match nothing")
	}

	if !newMatcher("").match("anything") {
		t.Error("an empty query must accept everything")
	}
}

// withColor forces a color profile for the duration of a test: with no TTY
// lipgloss renders every style as plain text, so styling assertions would
// otherwise always fail.
func withColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestMatcherHighlight(t *testing.T) {
	withColor(t)

	got := stripANSI(newMatcher("curl").highlight("curl and curl"))
	if got != "curl and curl" {
		t.Errorf("highlighting changed the text: %q", got)
	}
	if !strings.Contains(newMatcher("curl").highlight("curl"), "\x1b") {
		t.Error("literal match was not styled")
	}
	if !strings.Contains(newMatcher("re:cu.l").highlight("curl"), "\x1b") {
		t.Error("regex match was not styled")
	}
	if strings.Contains(newMatcher("re:[bad").highlight("curl"), "\x1b") {
		t.Error("a broken expression should not style anything")
	}
}

func TestRegexSearchFiltersRows(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns
	m.conns = []probe.Conn{
		conn(1, "curl", "10.0.0.1:5000", "1.2.3.4:443", 0, 0),
		conn(2, "wget", "10.0.0.1:6000", "5.6.7.8:80", 0, 0),
	}

	m.connsQuery = `re:(curl|nope)`
	rows := m.filteredConns()
	if len(rows) != 1 || rows[0].Process != "curl" {
		t.Fatalf("regex filter kept %d rows, want just curl", len(rows))
	}

	// The failing expression hides everything and the status line explains it.
	m.connsQuery = "re:[bad"
	if got := len(m.filteredConns()); got != 0 {
		t.Errorf("broken expression kept %d rows, want none", got)
	}
	if !strings.Contains(m.searchStatus(m.connsQuery), "invalid regex") {
		t.Errorf("search status should explain the bad expression: %q", m.searchStatus(m.connsQuery))
	}
}

func TestHighlightModeKeepsRowsAndJumps(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabProcs
	m.procs = []probe.ProcNet{
		{PID: 1, Name: "alpha"}, {PID: 2, Name: "target"},
		{PID: 3, Name: "beta"}, {PID: 4, Name: "target-two"},
	}
	m.procsQuery = "target"
	withColor(t)
	m.procsSortCol = indexOfCol(t, procsSortCols, "pid")
	m.procsSortDesc = false

	// Filter mode hides the non-matching rows.
	if got := len(m.filteredProcs()); got != 2 {
		t.Fatalf("filter mode kept %d rows, want 2", got)
	}

	model, _ := m.Update(key("\\"))
	m = model.(Model)
	if m.searchMode != searchHighlight {
		t.Fatal("\\ should switch to highlight mode")
	}
	if got := len(m.filteredProcs()); got != 4 {
		t.Errorf("highlight mode kept %d rows, want all 4", got)
	}
	// The matches are still marked.
	if !strings.Contains(m.renderProcsText(), "\x1b") {
		t.Error("highlight mode should style the matching rows")
	}

	// n walks to the next matching row, wrapping at the end.
	m.procsCursor = 0
	model, _ = m.Update(key("n"))
	m = model.(Model)
	if m.procsCursor != 1 {
		t.Errorf("n moved the cursor to %d, want the first match (1)", m.procsCursor)
	}
	model, _ = m.Update(key("n"))
	m = model.(Model)
	if m.procsCursor != 3 {
		t.Errorf("n moved the cursor to %d, want the second match (3)", m.procsCursor)
	}
	model, _ = m.Update(key("n"))
	m = model.(Model)
	if m.procsCursor != 1 {
		t.Errorf("n should wrap to the first match, got %d", m.procsCursor)
	}
	// N walks backwards.
	model, _ = m.Update(key("N"))
	m = model.(Model)
	if m.procsCursor != 3 {
		t.Errorf("N moved the cursor to %d, want the previous match (3)", m.procsCursor)
	}

	// With no query there is nothing to jump between.
	m.procsQuery = ""
	model, _ = m.Update(key("n"))
	if got := model.(Model).status; !strings.Contains(got, "No search query") {
		t.Errorf("status = %q, want it to explain there is no query", got)
	}
}

func TestQuickFiltersAndTabPersist(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := totalsModel()
	m.activeTab = tabConns
	m.protoFilter = 1
	m.ipVerFilter = 2
	m.connStateFilter = 1
	m.publicOnly = true
	m.onlyMine = true
	m.currentUser = "vlad"
	m.searchMode = searchHighlight
	m.applyFilterChange()
	m.saveConfig()

	restored := NewModel().WithConfig()
	if restored.protoFilter != 1 || restored.ipVerFilter != 2 || restored.connStateFilter != 1 {
		t.Errorf("filters not restored: proto=%d ipver=%d state=%d",
			restored.protoFilter, restored.ipVerFilter, restored.connStateFilter)
	}
	if !restored.publicOnly || !restored.onlyMine {
		t.Error("boolean filters not restored")
	}
	if restored.activeTab != tabConns {
		t.Errorf("last tab = %v, want the connections tab", restored.activeTab)
	}
	if restored.searchMode != searchHighlight {
		t.Error("search mode not restored")
	}
}

// A hand-edited config must not be able to wedge the UI.
func TestConfigClampsOutOfRangeFilters(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := os.MkdirAll(filepath.Join(dir, "ducknetview"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"proto_filter":99,"ipver_filter":-3,"conn_state_filter":42,"last_tab":77}`
	if err := os.WriteFile(filepath.Join(dir, "ducknetview", "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewModel().WithConfig()
	if m.protoFilter > 2 || m.ipVerFilter < 0 || m.connStateFilter > 2 {
		t.Errorf("filters not clamped: proto=%d ipver=%d state=%d", m.protoFilter, m.ipVerFilter, m.connStateFilter)
	}
	if int(m.activeTab) >= tabCount {
		t.Errorf("last tab %d is out of range", m.activeTab)
	}
}

func TestWithTab(t *testing.T) {
	for name, want := range map[string]tab{
		"overview": tabOverview, "ports": tabPorts, "conns": tabConns,
		"connections": tabConns, "events": tabEvents, "  Events  ": tabEvents,
	} {
		m, ok := NewModel().WithTab(name)
		if !ok {
			t.Errorf("WithTab(%q) was rejected", name)
			continue
		}
		if m.activeTab != want {
			t.Errorf("WithTab(%q) selected %v, want %v", name, m.activeTab, want)
		}
	}
	if _, ok := NewModel().WithTab("nonsense"); ok {
		t.Error("an unknown tab name should be rejected, not ignored")
	}
	if len(TabNames()) == 0 {
		t.Error("TabNames should list the accepted values for the flag error")
	}
}

func TestSocketHealthyDrivesTheQualityWarning(t *testing.T) {
	withColor(t)
	m := totalsModel()
	m.activeTab = tabConns
	m.hasThroughput = true

	healthy := conn(1, "curl", "10.0.0.1:1", "1.1.1.1:443", 10, 10)
	healthy.RTT = 20 * time.Millisecond
	m.conns = []probe.Conn{healthy}
	m.connsCursor = 0
	txt, ok := m.detailContent()
	if !ok {
		t.Fatal("expected detail content")
	}
	if !strings.Contains(stripANSI(txt), "0 retransmits") {
		t.Errorf("quality line missing:\n%s", stripANSI(txt))
	}

	// A lossy socket, and one that is merely backed up, both warn.
	for name, bad := range map[string]probe.Conn{
		"retransmits": {Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", RxBytes: 1, Retrans: 3},
		"send queue":  {Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", RxBytes: 1, SendQ: 4096},
	} {
		if bad.SocketHealthy() {
			t.Errorf("%s: socket should not count as healthy", name)
		}
		m.conns = []probe.Conn{bad}
		txt, _ = m.detailContent()
		if !strings.Contains(txt, "\x1b") {
			t.Errorf("%s: quality line was not highlighted", name)
		}
	}
	if !healthy.SocketHealthy() {
		t.Error("a clean socket should count as healthy")
	}
}
