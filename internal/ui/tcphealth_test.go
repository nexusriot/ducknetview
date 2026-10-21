package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func tcpHealthModel() Model {
	m := NewModel()
	m.w, m.h = 160, 45
	return m
}

func eventsOfKind(m Model, kind string) []event {
	var out []event
	for _, e := range m.events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// --- accept queue -----------------------------------------------------------

func queuedPorts(depth uint32) []probe.ListenPort {
	return []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:8080", PID: 42, Process: "api", AcceptQueue: depth, Backlog: 128},
		{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd", Backlog: 128},
		{Proto: "udp", Local: "0.0.0.0:53", PID: 2, Process: "dns"}, // no queue at all
	}
}

func TestListenQueueAlertFiresAndClears(t *testing.T) {
	m := tcpHealthModel()
	m.alertRules.listenQueuePct = 80
	start := time.Now()

	m.applyConnInfo(probe.ConnInfo{Ports: queuedPorts(120)}, start)
	got := eventsOfKind(m, "listen_queue")
	if len(got) != 1 {
		t.Fatalf("listen_queue events = %d, want 1: %+v", len(got), m.events)
	}
	if got[0].Subject != "tcp 0.0.0.0:8080" {
		t.Errorf("subject = %q", got[0].Subject)
	}
	for _, want := range []string{"120/128", "pid 42 (api)"} {
		if !strings.Contains(got[0].Detail, want) {
			t.Errorf("detail %q lacks %q", got[0].Detail, want)
		}
	}

	// Drained and staying drained for the clear interval: one recovery.
	m.applyConnInfo(probe.ConnInfo{Ports: queuedPorts(0)}, start.Add(30*time.Second))
	m.applyConnInfo(probe.ConnInfo{Ports: queuedPorts(0)}, start.Add(alertClearAfter+time.Second))
	if n := len(eventsOfKind(m, "listen_queue_cleared")); n != 1 {
		t.Errorf("listen_queue_cleared events = %d, want 1", n)
	}
}

func TestListenQueueAlertBelowThresholdAndOff(t *testing.T) {
	m := tcpHealthModel()
	m.alertRules.listenQueuePct = 80
	m.applyConnInfo(probe.ConnInfo{Ports: queuedPorts(64)}, time.Now()) // 50%
	if n := len(eventsOfKind(m, "listen_queue")); n != 0 {
		t.Errorf("fired at 50%% with an 80%% threshold")
	}

	off := tcpHealthModel()
	off.applyConnInfo(probe.ConnInfo{Ports: queuedPorts(128)}, time.Now())
	if n := len(eventsOfKind(off, "listen_queue")); n != 0 {
		t.Errorf("fired with the rule off")
	}
}

// --- CLOSE_WAIT -------------------------------------------------------------

func TestCloseWaitAlertFiresAndClears(t *testing.T) {
	m := tcpHealthModel()
	m.alertRules.closeWait = 50
	start := time.Now()
	leaky := func(n int) probe.ConnInfo {
		return probe.ConnInfo{Procs: []probe.ProcNet{
			{PID: 7, Name: "leaky", ConnCount: n + 1, CloseWait: n},
			{PID: 8, Name: "fine", ConnCount: 10, CloseWait: 2},
		}}
	}

	m.applyConnInfo(leaky(60), start)
	got := eventsOfKind(m, "close_wait")
	if len(got) != 1 || got[0].Subject != "pid 7 (leaky)" {
		t.Fatalf("close_wait events = %+v, want one for pid 7", got)
	}
	if !strings.Contains(got[0].Detail, "60") {
		t.Errorf("detail %q lacks the count", got[0].Detail)
	}

	// Still over the threshold a moment later: suppressed, not repeated.
	m.applyConnInfo(leaky(70), start.Add(10*time.Second))
	if n := len(eventsOfKind(m, "close_wait")); n != 1 {
		t.Errorf("close_wait fired %d times within the re-fire window", n)
	}

	m.applyConnInfo(leaky(0), start.Add(10*time.Second+alertClearAfter+time.Second))
	if n := len(eventsOfKind(m, "close_wait_cleared")); n != 1 {
		t.Errorf("close_wait_cleared events = %d, want 1", n)
	}
}

func TestCloseWaitAlertOffByDefault(t *testing.T) {
	m := tcpHealthModel()
	m.applyConnInfo(probe.ConnInfo{Procs: []probe.ProcNet{{PID: 7, Name: "leaky", CloseWait: 10000}}}, time.Now())
	if n := len(eventsOfKind(m, "close_wait")); n != 0 {
		t.Errorf("fired with the rule off")
	}
}

// Grouped process rows must not lose the count.
func TestAggregatedProcsSumCloseWait(t *testing.T) {
	m := tcpHealthModel()
	m.aggregateProcs = true
	m.procs = []probe.ProcNet{
		{PID: 1, Name: "worker", CloseWait: 3},
		{PID: 2, Name: "worker", CloseWait: 4},
	}
	rows := m.filteredProcs()
	if len(rows) != 1 || rows[0].CloseWait != 7 {
		t.Fatalf("rows = %+v, want one worker row with 7 in CLOSE_WAIT", rows)
	}
}

// --- listen drops -----------------------------------------------------------

func tcpSnap(at time.Time, drops, overflows uint64) probe.NetSnapshot {
	return probe.NetSnapshot{
		Hostname: "web1",
		TakenAt:  at,
		Ifaces:   []probe.IfaceInfo{{Name: "eth0", IsUp: true}},
		TCP:      &probe.TCPHealth{Totals: probe.TCPCounters{ListenDrops: drops, ListenOverflows: overflows}},
	}
}

func TestListenDropAlert(t *testing.T) {
	m := tcpHealthModel()
	m.alertRules.listenDropsPerMin = 10
	start := time.Now()

	// Counters are cumulative since boot: the first sample is only a baseline.
	m.applyNetSnapshot(tcpSnap(start, 5000, 4000))
	if n := len(eventsOfKind(m, "listen_drops")); n != 0 {
		t.Fatalf("the baseline sample fired %d events", n)
	}

	m.applyNetSnapshot(tcpSnap(start.Add(30*time.Second), 5030, 4020)) // 60/min
	got := eventsOfKind(m, "listen_drops")
	if len(got) != 1 || got[0].Subject != "web1" {
		t.Fatalf("listen_drops events = %+v", got)
	}
	for _, want := range []string{"60 listen drops/min", "40/min from full accept queues"} {
		if !strings.Contains(got[0].Detail, want) {
			t.Errorf("detail %q lacks %q", got[0].Detail, want)
		}
	}

	// Quiet for longer than the clear interval: the recovery is announced on
	// the next socket poll.
	quiet := start.Add(30*time.Second + alertClearAfter + time.Second)
	m.applyNetSnapshot(tcpSnap(quiet, 5030, 4020))
	m.applyConnInfo(probe.ConnInfo{}, quiet)
	if n := len(eventsOfKind(m, "listen_drops_cleared")); n != 1 {
		t.Errorf("listen_drops_cleared events = %d, want 1", n)
	}
}

func TestListenDropAlertBelowThresholdOffAndMissing(t *testing.T) {
	start := time.Now()

	m := tcpHealthModel()
	m.alertRules.listenDropsPerMin = 10
	m.applyNetSnapshot(tcpSnap(start, 0, 0))
	m.applyNetSnapshot(tcpSnap(start.Add(time.Minute), 5, 0)) // 5/min
	if n := len(eventsOfKind(m, "listen_drops")); n != 0 {
		t.Errorf("fired below the threshold")
	}

	off := tcpHealthModel()
	off.applyNetSnapshot(tcpSnap(start, 0, 0))
	off.applyNetSnapshot(tcpSnap(start.Add(time.Minute), 100000, 0))
	if n := len(eventsOfKind(off, "listen_drops")); n != 0 {
		t.Errorf("fired with the rule off")
	}

	// A host without the counters (non-Linux, restricted /proc) must not panic
	// or fire.
	none := tcpHealthModel()
	none.alertRules.listenDropsPerMin = 10
	none.applyNetSnapshot(probe.NetSnapshot{TakenAt: start})
	none.applyNetSnapshot(probe.NetSnapshot{TakenAt: start.Add(time.Minute)})
	if n := len(eventsOfKind(none, "listen_drops")); n != 0 {
		t.Errorf("fired without counters")
	}
}

// --- display ----------------------------------------------------------------

func TestTCPHealthPanelOnlyWhenNonZero(t *testing.T) {
	m := tcpHealthModel()
	if got := m.renderTCPHealth(100); got != "" {
		t.Errorf("panel without counters = %q", got)
	}
	m.lastSnap.TCP = &probe.TCPHealth{Totals: probe.TCPCounters{ListenDrops: 99}}
	if got := m.renderTCPHealth(100); got != "" {
		t.Errorf("panel with only idle counters = %q", got)
	}
	m.lastSnap.TCP.PerSec.ListenDrops = 0.5
	got := m.renderTCPHealth(100)
	if !strings.Contains(m.renderTCPHealth(100), "Host TCP") {
		t.Errorf("panel has no title: %q", got)
	}
	m.lastSnap.TCP.PerSec.OutRsts = 0.05 // 3/min: small rates keep a decimal
	if !strings.Contains(m.renderTCPHealth(100), "3.0/min") {
		t.Errorf("panel = %q", m.renderTCPHealth(100))
	}
	m.lastSnap.TCP.PerSec.OutRsts = 0
	if !strings.Contains(got, "listen drops") || !strings.Contains(got, "30/min") || !strings.Contains(got, "total 99") {
		t.Errorf("panel = %q", got)
	}
	if strings.Contains(got, "resets sent") {
		t.Errorf("an idle counter is listed: %q", got)
	}
}

func TestPortsQueueColumn(t *testing.T) {
	m := tcpHealthModel()
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}}
	if m.portsColumns().width("queue") != 0 {
		t.Error("QUEUE column shown although no listener has queue figures")
	}

	m.ports = queuedPorts(120)
	if m.portsColumns().width("queue") == 0 {
		t.Fatal("no QUEUE column with queue figures present")
	}
	text := m.renderPortsText()
	if !strings.Contains(text, "QUEUE") || !strings.Contains(text, "120/128") {
		t.Errorf("ports text lacks the queue:\n%s", text)
	}

	m.hiddenCols = map[string]bool{"queue": true}
	if m.portsColumns().width("queue") != 0 {
		t.Error("hidden_columns cannot hide the QUEUE column")
	}
}

func TestSortPortsByQueueFill(t *testing.T) {
	rows := []probe.ListenPort{
		{Local: "a:1", AcceptQueue: 100, Backlog: 4096},
		{Local: "b:2", AcceptQueue: 4, Backlog: 5},
		{Local: "c:3"},
	}
	if cmpPorts(rows[1], rows[0], "queue") <= 0 {
		t.Error("4/5 does not sort above 100/4096")
	}
	if cmpPorts(rows[2], rows[0], "queue") >= 0 {
		t.Error("an unknown queue does not sort below a known one")
	}
}

func TestDetailShowsQueueAndCloseWait(t *testing.T) {
	m := tcpHealthModel()
	m.ports = queuedPorts(120)
	m.activeTab = tabPorts
	m.portsSortCol = 1 // port: 22, 53, 8080
	m.portsCursor = 2
	body, ok := m.detailContent()
	if !ok || !strings.Contains(body, "120 waiting of 128 backlog") {
		t.Errorf("port detail lacks the queue:\n%s", body)
	}

	m.procs = []probe.ProcNet{{PID: 7, Name: "leaky", ConnCount: 30, CloseWait: 25}}
	m.activeTab = tabProcs
	m.procsCursor = 0
	body, ok = m.detailContent()
	if !ok || !strings.Contains(body, "CloseWait") || !strings.Contains(body, "25") {
		t.Errorf("process detail lacks CLOSE_WAIT:\n%s", body)
	}
}

func TestMetricsCarryTCPHealth(t *testing.T) {
	snap := probe.NetSnapshot{TCP: &probe.TCPHealth{Totals: probe.TCPCounters{ListenDrops: 12}}}
	info := probe.ConnInfo{Ports: queuedPorts(32)}
	out := FormatMetrics(snap, info, 0, false)
	for _, want := range []string{
		`ducknetview_tcp_counter_total{counter="listen_drops"} 12`,
		`ducknetview_listener_queue_ratio{local="0.0.0.0:8080",process="api"} 0.25`,
		`ducknetview_listener_accept_queue{local="0.0.0.0:8080",process="api"} 32`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics lack %q", want)
		}
	}
	if strings.Contains(out, `process="dns"`) {
		t.Error("a UDP socket was given a queue ratio")
	}

	bare := FormatMetrics(probe.NetSnapshot{}, probe.ConnInfo{}, 0, false)
	if strings.Contains(bare, "tcp_counter_total") || strings.Contains(bare, "listener_queue_ratio") {
		t.Error("TCP series rendered without data")
	}
}

func TestTCPThresholdsRoundTripConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := tcpHealthModel()
	m.alertRules.listenQueuePct = 80
	m.alertRules.listenDropsPerMin = 5
	m.alertRules.closeWait = 100
	m.saveConfig()

	n := NewModel().WithConfig()
	if n.alertRules.listenQueuePct != 80 || n.alertRules.listenDropsPerMin != 5 || n.alertRules.closeWait != 100 {
		t.Errorf("rules after reload = %+v", n.alertRules)
	}
}

// The counters travel in --json and come back out of --from.
func TestSnapshotCarriesTCPHealth(t *testing.T) {
	s, err := decodeSnapshot([]byte(`{"hostname":"h","tcp":{"totals":{"listen_drops":7},"per_second":{"listen_drops":0.5}}}`))
	if err != nil {
		t.Fatal(err)
	}
	m := tcpHealthModel().WithSnapshot(s)
	if m.lastSnap.TCP == nil || m.lastSnap.TCP.Totals.ListenDrops != 7 || m.lastSnap.TCP.PerSec.ListenDrops != 0.5 {
		t.Fatalf("lastSnap.TCP = %+v", m.lastSnap.TCP)
	}
}

// Workers sharing their parent's listener must not produce duplicate series.
func TestMetricsQueueSeriesAreUnique(t *testing.T) {
	ports := []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:80", PID: 10, Process: "nginx", Backlog: 511},
		{Proto: "tcp", Local: "0.0.0.0:80", PID: 11, Process: "nginx", Backlog: 511},
	}
	out := FormatMetrics(probe.NetSnapshot{}, probe.ConnInfo{Ports: ports}, 0, false)
	if n := strings.Count(out, `ducknetview_listener_queue_ratio{local="0.0.0.0:80",process="nginx"}`); n != 1 {
		t.Errorf("queue ratio series rendered %d times, want 1", n)
	}
}
