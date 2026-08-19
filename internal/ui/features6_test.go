package ui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// feed pushes a socket enumeration through Update the way the refresh command
// does, so the event/diff bookkeeping runs exactly as it does at runtime.
func feed(t *testing.T, m Model, ports []probe.ListenPort, conns []probe.Conn) Model {
	t.Helper()
	model, _ := m.Update(connInfoMsg(probe.ConnInfo{Ports: ports, Conns: conns}))
	return model.(Model)
}

func eventsModel() Model {
	m := NewModel()
	m.portsVP.Width, m.portsVP.Height = 120, 20
	m.connsVP.Width, m.connsVP.Height = 150, 20
	m.procsVP.Width, m.procsVP.Height = 120, 20
	m.eventsVP.Width, m.eventsVP.Height = 130, 20
	return m
}

func kindsOf(events []event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

func countKind(events []event, kind string) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// The first enumeration is a baseline: a host that already has fifty listeners
// must not produce fifty "listener up" events on startup.
func TestFirstEnumerationDoesNotFloodEvents(t *testing.T) {
	m := feed(t, eventsModel(),
		[]probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}}, nil)

	if n := countKind(m.events, "listener_up"); n != 0 {
		t.Errorf("startup produced %d listener_up events, want 0 (kinds: %v)", n, kindsOf(m.events))
	}
}

func TestListenerEventsUpAndDown(t *testing.T) {
	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	backdoor := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:4444", PID: 66, Process: "nc"}

	m := feed(t, eventsModel(), []probe.ListenPort{sshd}, nil)
	m = feed(t, m, []probe.ListenPort{sshd, backdoor}, nil)

	if countKind(m.events, "listener_up") != 1 {
		t.Fatalf("expected one listener_up, got kinds %v", kindsOf(m.events))
	}
	up := m.events[len(m.events)-1]
	if !strings.Contains(up.Subject, "4444") || !strings.Contains(up.Detail, "nc") {
		t.Errorf("listener_up event = %+v, want the new listener and its process", up)
	}
	if up.Level != levelWarn {
		t.Errorf("new exposed listener level = %v, want warn", up.Level)
	}

	m = feed(t, m, []probe.ListenPort{sshd}, nil)
	if countKind(m.events, "listener_down") != 1 {
		t.Fatalf("expected a listener_down event, got kinds %v", kindsOf(m.events))
	}
}

// A listener outside the saved baseline is an alert, not a mere warning.
func TestListenerEventLevelUsesBaseline(t *testing.T) {
	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	backdoor := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:4444", PID: 66, Process: "nc"}

	m := feed(t, eventsModel(), []probe.ListenPort{sshd}, nil)
	m.setBaseline()
	m = feed(t, m, []probe.ListenPort{sshd, backdoor}, nil)

	last := m.events[len(m.events)-1]
	if last.Level != levelAlert || !strings.Contains(last.Detail, "baseline") {
		t.Errorf("off-baseline listener event = %+v, want an alert mentioning the baseline", last)
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

// One poll may not be allowed to fill the whole ring buffer, and the truncation
// has to be visible rather than silent.
func TestEventsPerPollCapIsReported(t *testing.T) {
	m := eventsModel()
	m.watch = parseWatchlist([]string{"0.0.0.0/0"})

	var conns []probe.Conn
	for i := 0; i < eventsPerPollLimit+7; i++ {
		conns = append(conns, probe.Conn{
			Proto: "tcp", Local: "10.0.0.1:" + strconv.Itoa(i), Remote: "8.8.8." + strconv.Itoa(i) + ":443",
			State: "ESTABLISHED", PID: int32(i),
		})
	}
	m = feed(t, m, nil, conns)

	if n := countKind(m.events, "watchlist_hit"); n != eventsPerPollLimit+1 {
		t.Errorf("watchlist_hit events = %d, want %d capped rows plus one summary", n, eventsPerPollLimit+1)
	}
	summary := m.events[len(m.events)-1]
	if !strings.Contains(summary.Subject, "more") {
		t.Errorf("last event = %+v, want a summary of the suppressed events", summary)
	}
}

func TestEventRingBufferIsCapped(t *testing.T) {
	m := eventsModel()
	for i := 0; i < eventLimit+50; i++ {
		m.recordEvent(event{At: time.Now(), Kind: "test", Subject: strconv.Itoa(i)})
	}
	if len(m.events) != eventLimit {
		t.Fatalf("ring buffer holds %d events, want %d", len(m.events), eventLimit)
	}
	// The oldest entries are the ones dropped.
	if m.events[0].Subject != strconv.Itoa(50) {
		t.Errorf("oldest retained event = %q, want %q", m.events[0].Subject, strconv.Itoa(50))
	}
}

func TestIfaceEvents(t *testing.T) {
	m := eventsModel()
	snap := func(ifaces ...probe.IfaceInfo) probe.NetSnapshot {
		return probe.NetSnapshot{Ifaces: ifaces, TakenAt: time.Now()}
	}
	eth := probe.IfaceInfo{Name: "eth0", IsUp: true}

	m.noteIfaceEvents(snap(eth))
	if len(m.events) != 0 {
		t.Fatalf("first snapshot produced events: %v", kindsOf(m.events))
	}

	m.noteIfaceEvents(snap(probe.IfaceInfo{Name: "eth0", IsUp: false}))
	if countKind(m.events, "iface_state") != 1 {
		t.Errorf("expected an iface_state event, got %v", kindsOf(m.events))
	}

	m.noteIfaceEvents(snap(probe.IfaceInfo{Name: "eth0", IsUp: false}, probe.IfaceInfo{Name: "tun0", IsUp: true}))
	if countKind(m.events, "iface_added") != 1 {
		t.Errorf("expected an iface_added event for the VPN, got %v", kindsOf(m.events))
	}

	m.noteIfaceEvents(snap(probe.IfaceInfo{Name: "eth0", IsUp: false}))
	if countKind(m.events, "iface_removed") != 1 {
		t.Errorf("expected an iface_removed event, got %v", kindsOf(m.events))
	}
}

func TestEventsTabRendersAndClears(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabEvents
	m.recordEvent(event{At: time.Now(), Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:4444", Detail: "pid 66 (nc)"})

	text := m.renderEventsText()
	if !strings.Contains(text, "listener_up") || !strings.Contains(text, "4444") {
		t.Fatalf("event row missing from the table:\n%s", text)
	}
	if !strings.Contains(eventsTabLabel(m.events), "!1") {
		t.Errorf("tab label = %q, want an alert count", eventsTabLabel(m.events))
	}

	model, _ := m.Update(key("c"))
	m = model.(Model)
	if len(m.events) != 0 {
		t.Errorf("c should clear the events, %d left", len(m.events))
	}
}

func TestEventLogWritesJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")

	m, err := eventsModel().OpenEventLog(path)
	if err != nil {
		t.Fatalf("OpenEventLog: %v", err)
	}
	m.recordEvent(event{At: time.Now(), Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:4444", Detail: "pid 66 (nc)"})
	m.recordEvent(event{At: time.Now(), Level: levelInfo, Kind: "iface_state", Subject: "eth0"})
	if m.eventLogErr != nil {
		t.Fatalf("event log write failed: %v", m.eventLogErr)
	}
	_ = m.eventLog.Close()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	var lines int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("line %d is not JSON: %v (%q)", lines+1, err, sc.Text())
		}
		if rec["kind"] == "" || rec["at"] == "" || rec["level"] == "" {
			t.Errorf("line %d missing required fields: %v", lines+1, rec)
		}
		lines++
	}
	if lines != 2 {
		t.Errorf("log has %d lines, want 2", lines)
	}
}

func TestBaselineDetectsUnexpectedListener(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	backdoor := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:4444", PID: 66, Process: "nc"}

	m := eventsModel()
	m.ports = []probe.ListenPort{sshd}

	if m.offBaseline(backdoor) {
		t.Error("nothing should be off-baseline before a baseline exists")
	}

	m.setBaseline()
	if !m.hasBaseline() {
		t.Fatal("baseline was not recorded")
	}
	if m.offBaseline(sshd) {
		t.Error("a listener present when the baseline was taken is not off-baseline")
	}
	if !m.offBaseline(backdoor) {
		t.Error("a listener absent from the baseline should be flagged")
	}

	m.ports = []probe.ListenPort{sshd, backdoor}
	if got := m.offBaselineCount(); got != 1 {
		t.Errorf("offBaselineCount = %d, want 1", got)
	}
	m.portsCursor = 0
	if !strings.Contains(m.renderPortsText(), "off-baseline") {
		t.Error("the ports subtitle should report off-baseline listeners")
	}

	// The saved baseline is what the scriptable check reads.
	offending, _, ok := BaselineCheck([]probe.ListenPort{sshd, backdoor})
	if !ok {
		t.Fatal("BaselineCheck found no saved baseline")
	}
	if len(offending) != 1 || offending[0].Local != backdoor.Local {
		t.Errorf("BaselineCheck offending = %+v, want just the backdoor", offending)
	}

	m.clearBaseline()
	if m.hasBaseline() || m.offBaseline(backdoor) {
		t.Error("clearBaseline should switch the alerting back off")
	}
	if _, _, ok := BaselineCheck([]probe.ListenPort{backdoor}); ok {
		t.Error("BaselineCheck should report no baseline after it was cleared")
	}
}

func TestBaselineSurvivesReload(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := eventsModel()
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}}
	m.setBaseline()

	reloaded := NewModel().WithConfig()
	if !reloaded.hasBaseline() {
		t.Fatal("baseline did not survive a restart")
	}
	if reloaded.offBaseline(m.ports[0]) {
		t.Error("restored baseline does not recognise the listener it was built from")
	}
	if reloaded.baselineAt.IsZero() {
		t.Error("baseline timestamp was not restored")
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

func TestManualRefreshKey(t *testing.T) {
	m := NewModel()
	m.lastConnFetch = time.Time{}

	model, cmd := m.Update(key("R"))
	m = model.(Model)
	if cmd == nil {
		t.Fatal("R should issue refresh commands")
	}
	if m.lastConnFetch.IsZero() {
		t.Error("R should reset the socket-refresh timer")
	}
}

// Sparklines are anchored at zero so bar height means "share of the peak" and
// an idle stretch is visibly empty.
func TestSparkIsZeroAnchored(t *testing.T) {
	if got := Spark([]float64{0, 0, 0, 100}, 4); got != "▁▁▁█" {
		t.Errorf("Spark = %q, want idle bars low and the peak full", got)
	}
	// A steady stream is at its peak the whole way, not flat along the bottom.
	if got := Spark([]float64{50, 50, 50}, 3); got != "███" {
		t.Errorf("steady series = %q, want full-height bars", got)
	}
	if got := Spark([]float64{0, 0}, 2); got != "▁▁" {
		t.Errorf("all-zero series = %q, want the lowest block", got)
	}
	if got := Spark([]float64{1, 2}, 5); len([]rune(got)) != 5 {
		t.Errorf("Spark(width 5) rendered %d cells", len([]rune(got)))
	}
}

func TestSessionPeakOutlivesTheChartWindow(t *testing.T) {
	m := NewModel()
	m.w, m.h = 100, 40

	burst := probe.NetSnapshot{TakenAt: time.Now(), Ifaces: []probe.IfaceInfo{
		{Name: "eth0", IsUp: true, Kind: probe.IfacePhysical, RxBps: 5_000_000},
	}}
	model, _ := m.Update(snapMsg(burst))
	m = model.(Model)

	// Push the burst out of the (clamped) history window.
	for i := 0; i < 300; i++ {
		idle := probe.NetSnapshot{TakenAt: time.Now(), Ifaces: []probe.IfaceInfo{
			{Name: "eth0", IsUp: true, Kind: probe.IfacePhysical, RxBps: 0},
		}}
		model, _ = m.Update(snapMsg(idle))
		m = model.(Model)
	}

	if m.totalPeak.rx != 5_000_000 {
		t.Errorf("session peak = %v, want the 5 MB/s burst to be remembered", m.totalPeak.rx)
	}
	if maxOf(m.totalRxHist) != 0 {
		t.Fatal("the burst should have aged out of the chart window (test is not exercising the peak)")
	}
	if got := m.ifacePeaks["eth0"].rx; got != 5_000_000 {
		t.Errorf("per-interface peak = %v, want 5 MB/s", got)
	}
}

func TestDetailOverlayCopiesPlainText(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabPorts
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:443", PID: 1, Process: "nginx"}}

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if !m.showDetail {
		t.Fatal("enter should open the overlay")
	}

	plain := stripANSI(m.detailText)
	if strings.Contains(plain, "\x1b") {
		t.Errorf("stripANSI left escape sequences: %q", plain)
	}
	if !strings.Contains(plain, "nginx") {
		t.Errorf("stripped detail lost its content: %q", plain)
	}
}

func TestStripANSI(t *testing.T) {
	styled := okStyle.Render("hello") + " " + errStyle.Render("world")
	if got := stripANSI(styled); got != "hello world" {
		t.Errorf("stripANSI = %q, want %q", got, "hello world")
	}
	if got := stripANSI("plain"); got != "plain" {
		t.Errorf("stripANSI mangled unstyled text: %q", got)
	}
}

// UDP has no LISTEN state, so gopsutil reports every outbound DNS socket as a
// listener. Baselining those would leave the baseline permanently dirty.
func TestBaselineIgnoresEphemeralUDP(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dns := probe.ListenPort{Proto: "udp", Local: "0.0.0.0:53", PID: 1, Process: "resolved"}
	client := probe.ListenPort{Proto: "udp", Local: "192.168.1.5:47572", PID: 2, Process: "firefox"}
	tcpHigh := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:52000", PID: 3, Process: "app"}

	m := eventsModel()
	m.ports = []probe.ListenPort{dns}
	m.setBaseline()

	if m.offBaseline(client) {
		t.Error("an ephemeral UDP client socket must not count as an unexpected listener")
	}
	// A TCP listener on a high port is a real service and still counts.
	if !m.offBaseline(tcpHigh) {
		t.Error("a TCP listener outside the baseline should be flagged whatever its port")
	}

	m.ports = []probe.ListenPort{dns, client, tcpHigh}
	if got := m.offBaselineCount(); got != 1 {
		t.Errorf("offBaselineCount = %d, want 1 (only the TCP listener)", got)
	}

	offending, _, ok := BaselineCheck([]probe.ListenPort{dns, client, tcpHigh})
	if !ok || len(offending) != 1 || offending[0].Local != tcpHigh.Local {
		t.Errorf("BaselineCheck offending = %+v, want just the TCP listener", offending)
	}
}

func TestEphemeralUDPListenersDoNotLogEvents(t *testing.T) {
	dns := probe.ListenPort{Proto: "udp", Local: "0.0.0.0:53", PID: 1, Process: "resolved"}
	client := probe.ListenPort{Proto: "udp", Local: "192.168.1.5:47572", PID: 2, Process: "firefox"}

	m := feed(t, eventsModel(), []probe.ListenPort{dns}, nil)
	m = feed(t, m, []probe.ListenPort{dns, client}, nil)
	if n := countKind(m.events, "listener_up"); n != 0 {
		t.Errorf("ephemeral UDP socket produced %d listener_up events, want 0", n)
	}

	m = feed(t, m, []probe.ListenPort{dns}, nil)
	if n := countKind(m.events, "listener_down"); n != 0 {
		t.Errorf("ephemeral UDP socket produced %d listener_down events, want 0", n)
	}
}
