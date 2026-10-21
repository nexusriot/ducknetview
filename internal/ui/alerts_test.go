package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// alertModel is a model with one sustained rule armed and a terminal size, so
// the renderers have somewhere to draw.
func alertModel() Model {
	m := NewModel()
	m.w, m.h = 160, 48
	m.alertConnBps = 1000
	m.hasThroughput = true
	return m
}

func hasKind(events []event, kind string) bool { return countKind(events, kind) > 0 }

// A rule that fires and then goes quiet leaves a pager paged and never
// cleared. Falling back under the threshold has to say so.
func TestThroughputAlertClears(t *testing.T) {
	m := alertModel()
	start := time.Now()

	conn := func(rx uint64) []probe.Conn {
		return []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:40000", Remote: "1.2.3.4:443", State: "ESTABLISHED", RxBytes: rx}}
	}

	// Two polls a second apart: the second moves 10 kB, which is over the
	// 1 kB/s threshold.
	m.applyConnInfo(probe.ConnInfo{Conns: conn(0)}, start)
	m.applyConnInfo(probe.ConnInfo{Conns: conn(10_000)}, start.Add(time.Second))
	if !hasKind(m.events, "throughput_connection") {
		t.Fatalf("the threshold should have fired: %v", kindsOf(m.events))
	}
	if hasKind(m.events, "throughput_connection_cleared") {
		t.Fatal("nothing has recovered yet")
	}

	// Quiet from here on. The clear waits out alertClearAfter rather than
	// firing on the first poll under the threshold, so a rate sitting on the
	// line cannot flap.
	m.applyConnInfo(probe.ConnInfo{Conns: conn(10_000)}, start.Add(2*time.Second))
	if hasKind(m.events, "throughput_connection_cleared") {
		t.Fatal("the clear fired before the hold-down had elapsed")
	}

	m.applyConnInfo(probe.ConnInfo{Conns: conn(10_000)}, start.Add(alertClearAfter+2*time.Second))
	if !hasKind(m.events, "throughput_connection_cleared") {
		t.Errorf("the recovery was never announced: %v", kindsOf(m.events))
	}
	if len(m.alertActive) != 0 {
		t.Errorf("the firing alert should have been forgotten: %v", m.alertActive)
	}
	for _, e := range m.events {
		if e.Kind == "throughput_connection_cleared" && e.Level != levelInfo {
			t.Errorf("a recovery is not news: level %v", e.Level)
		}
	}
}

// The recovery worth hearing about most is the one where the subject stopped
// existing — the transfer finished, the process exited.
func TestAlertClearsWhenTheConnectionGoesAway(t *testing.T) {
	m := alertModel()
	start := time.Now()

	conns := []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:40000", Remote: "1.2.3.4:443", State: "ESTABLISHED"}}
	m.applyConnInfo(probe.ConnInfo{Conns: conns}, start)
	busy := []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:40000", Remote: "1.2.3.4:443", State: "ESTABLISHED", RxBytes: 10_000}}
	m.applyConnInfo(probe.ConnInfo{Conns: busy}, start.Add(time.Second))
	if !hasKind(m.events, "throughput_connection") {
		t.Fatalf("the threshold should have fired: %v", kindsOf(m.events))
	}

	// The connection closes and never appears again.
	m.applyConnInfo(probe.ConnInfo{}, start.Add(alertClearAfter+2*time.Second))
	if !hasKind(m.events, "throughput_connection_cleared") {
		t.Errorf("a subject that vanished should still clear: %v", kindsOf(m.events))
	}
}

// A burst is a moment, not a state: nothing can know a scan has stopped.
func TestFanoutHasNoRecovery(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 48
	m.alertRules.fanoutHosts = 2
	start := time.Now()

	m.applyConnInfo(probe.ConnInfo{Conns: []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:1", Remote: "1.1.1.1:443", PID: 7},
	}}, start)
	m.applyConnInfo(probe.ConnInfo{Conns: []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:2", Remote: "2.2.2.2:443", PID: 7},
		{Proto: "tcp", Local: "10.0.0.2:3", Remote: "3.3.3.3:443", PID: 7},
		{Proto: "tcp", Local: "10.0.0.2:4", Remote: "4.4.4.4:443", PID: 7},
	}}, start.Add(time.Second))
	if !hasKind(m.events, "fanout") {
		t.Fatalf("fan-out should have fired: %v", kindsOf(m.events))
	}

	m.applyConnInfo(probe.ConnInfo{}, start.Add(2*alertClearAfter))
	if hasKind(m.events, "fanout_cleared") {
		t.Error("a burst must not pretend to recover")
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

func TestFanoutAlert(t *testing.T) {
	m := eventsModel()
	m.alertRules = alertRules{fanoutHosts: 5}
	m.procs = []probe.ProcNet{{PID: 7, Name: "scanner"}}

	var conns []probe.Conn
	m.newConns = map[string]bool{}
	for i := 0; i < 6; i++ {
		c := probe.Conn{Proto: "tcp", Local: "10.0.0.1:" + string(rune('a'+i)),
			Remote: "203.0.113." + string(rune('0'+i)) + ":443", State: "SYN_SENT", PID: 7}
		conns = append(conns, c)
		m.newConns[connKey(c)] = true
	}
	m.noteQualityAlerts(conns, time.Now())

	if countKind(m.events, "fanout") != 1 {
		t.Fatalf("expected a fanout alert, got %v", kindsOf(m.events))
	}
	e := m.events[len(m.events)-1]
	if e.Level != levelAlert || !strings.Contains(e.Subject, "scanner") {
		t.Errorf("fanout event = %+v, want an alert naming the process", e)
	}

	// Connections that are not new do not count as fan-out.
	m2 := eventsModel()
	m2.alertRules = alertRules{fanoutHosts: 5}
	m2.noteQualityAlerts(conns, time.Now())
	if countKind(m2.events, "fanout") != 0 {
		t.Errorf("pre-existing connections triggered fanout: %v", kindsOf(m2.events))
	}
}

func TestConnectionLeakAlert(t *testing.T) {
	m := eventsModel()
	m.alertRules = alertRules{connGrowth: 3}
	m.procs = []probe.ProcNet{{PID: 9, Name: "leaky"}}
	t0 := time.Now()

	// Four polls, each with one more connection than the last.
	for poll := 1; poll <= 5; poll++ {
		var conns []probe.Conn
		for i := 0; i < poll; i++ {
			conns = append(conns, probe.Conn{Proto: "tcp", Local: "10.0.0.1:" + string(rune('a'+i)),
				Remote: "1.1.1.1:443", State: "ESTABLISHED", PID: 9})
		}
		m.noteQualityAlerts(conns, t0.Add(time.Duration(poll)*time.Second))
	}

	if countKind(m.events, "conn_growth") == 0 {
		t.Fatalf("sustained growth did not raise a leak alert: %v", kindsOf(m.events))
	}
	if !strings.Contains(m.events[len(m.events)-1].Detail, "consecutive polls") {
		t.Errorf("leak event = %+v", m.events[len(m.events)-1])
	}

	// A count that goes back down resets the streak.
	m2 := eventsModel()
	m2.alertRules = alertRules{connGrowth: 3}
	counts := []int{1, 2, 1, 2, 1, 2}
	for poll, n := range counts {
		var conns []probe.Conn
		for i := 0; i < n; i++ {
			conns = append(conns, probe.Conn{Proto: "tcp", Local: "10.0.0.1:" + string(rune('a'+i)),
				Remote: "1.1.1.1:443", PID: 9})
		}
		m2.noteQualityAlerts(conns, t0.Add(time.Duration(poll)*time.Second))
	}
	if countKind(m2.events, "conn_growth") != 0 {
		t.Errorf("a fluctuating count was reported as a leak: %v", kindsOf(m2.events))
	}
}

func TestAcknowledgeAlerts(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabEvents
	m.recordEvent(event{At: time.Now(), Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:4444"})
	m.recordEvent(event{At: time.Now(), Level: levelInfo, Kind: "new_destination", Subject: "1.2.3.4:443"})

	if got := m.unreadAlerts(); got != 1 {
		t.Fatalf("unread alerts = %d, want 1", got)
	}
	if !strings.Contains(eventsTabLabel(m.unreadAlerts()), "!1") {
		t.Error("the tab badge should show the unread count")
	}

	model, _ := m.Update(key("d"))
	m = model.(Model)

	if got := m.unreadAlerts(); got != 0 {
		t.Errorf("unread alerts after ack = %d, want 0", got)
	}
	// Acknowledging must not destroy the log — that is what `c` is for.
	if len(m.events) != 2 {
		t.Errorf("ack dropped events: %d left, want 2", len(m.events))
	}
	if eventsTabLabel(m.unreadAlerts()) != "6 Events" {
		t.Errorf("badge = %q, want it cleared", eventsTabLabel(m.unreadAlerts()))
	}

	// A new alert after the acknowledgement counts again.
	m.recordEvent(event{At: time.Now().Add(time.Second), Level: levelAlert, Kind: "watchlist_hit", Subject: "8.8.8.8:443"})
	if got := m.unreadAlerts(); got != 1 {
		t.Errorf("unread after a fresh alert = %d, want 1", got)
	}
}

// The suppression map is only pruned by the throughput path, which returns
// early when no throughput threshold is set — so a host with only quality rules
// grows it forever.
func TestAlertSuppressionPrunedWithoutThroughputRules(t *testing.T) {
	m := eventsModel()
	m.alertRules = alertRules{rttMs: 1} // quality rules only, no throughput ones
	m.alertFired = map[string]time.Time{"rtt:stale": time.Now().Add(-10 * alertRefire)}

	m.noteQualityAlerts(nil, time.Now())

	if _, stale := m.alertFired["rtt:stale"]; stale {
		t.Error("quality alerts never prune the suppression map, so it grows forever")
	}
}

// --- interface alert rule ------------------------------------------------

func ifaceAlertModel() Model {
	m := NewModel()
	m.w, m.h = 160, 45
	m.alertRules.ifaceDropsPerMin = 60
	return m
}

func TestIfaceDropAlert(t *testing.T) {
	m := ifaceAlertModel()
	start := time.Now()

	// The first sample is the baseline: counters are cumulative since boot, so
	// without one every drop since startup would read as happening now.
	m.applyNetSnapshot(probe.NetSnapshot{
		TakenAt: start,
		Ifaces:  []probe.IfaceInfo{{Name: "eth0", IsUp: true, Dropin: 1000}},
	})
	if n := len(m.events); n != 0 {
		t.Fatalf("the first sample produced %d events, want 0 (it is the baseline)", n)
	}

	// 120 more drops over a minute is 120/min, over the threshold.
	m.applyNetSnapshot(probe.NetSnapshot{
		TakenAt: start.Add(time.Minute),
		Ifaces:  []probe.IfaceInfo{{Name: "eth0", IsUp: true, Dropin: 1120}},
	})
	found := false
	for _, e := range m.events {
		if e.Kind == "link_errors" && e.Subject == "eth0" {
			found = true
			if !strings.Contains(e.Detail, "120") {
				t.Errorf("detail = %q, want the measured rate", e.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("no link_errors event: %+v", m.events)
	}
}

func TestIfaceDropAlertStaysQuietBelowThreshold(t *testing.T) {
	m := ifaceAlertModel()
	start := time.Now()
	m.applyNetSnapshot(probe.NetSnapshot{TakenAt: start,
		Ifaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true, Dropin: 1000}}})
	m.applyNetSnapshot(probe.NetSnapshot{TakenAt: start.Add(time.Minute),
		Ifaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true, Dropin: 1010}}}) // 10/min

	for _, e := range m.events {
		if e.Kind == "link_errors" {
			t.Errorf("fired below the threshold: %+v", e)
		}
	}
}

func TestIfaceDropAlertOffByDefault(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 45
	start := time.Now()
	m.applyNetSnapshot(probe.NetSnapshot{TakenAt: start,
		Ifaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true}}})
	m.applyNetSnapshot(probe.NetSnapshot{TakenAt: start.Add(time.Minute),
		Ifaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true, Dropin: 100000}}})

	for _, e := range m.events {
		if e.Kind == "link_errors" {
			t.Errorf("fired with no threshold configured: %+v", e)
		}
	}
}
