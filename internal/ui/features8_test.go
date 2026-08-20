package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestAcceptListenerIntoBaseline(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	newSvc := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:8080", PID: 2, Process: "app"}

	m := eventsModel()
	m.ports = []probe.ListenPort{sshd, newSvc}

	// Accepting one listener is meaningless before a baseline exists.
	if msg, ok := m.acceptListener(newSvc); ok || !strings.Contains(msg, "press b") {
		t.Errorf("accept without a baseline = (%q, %v), want a refusal that says what to do", msg, ok)
	}

	m.ports = []probe.ListenPort{sshd}
	m.setBaseline()
	m.ports = []probe.ListenPort{sshd, newSvc}
	if !m.offBaseline(newSvc) {
		t.Fatal("the new service should start off-baseline")
	}

	// Accepting it blesses only that one listener.
	msg, ok := m.acceptListener(newSvc)
	if !ok || !strings.Contains(msg, "8080") {
		t.Fatalf("accept = (%q, %v)", msg, ok)
	}
	if m.offBaseline(newSvc) {
		t.Error("accepted listener is still off-baseline")
	}
	if len(m.baselineRaw) != 2 {
		t.Errorf("baseline holds %d entries, want 2", len(m.baselineRaw))
	}

	// Pressing it again takes it back out.
	if msg, ok := m.acceptListener(newSvc); !ok || !strings.Contains(msg, "Removed") {
		t.Errorf("second accept = (%q, %v), want a removal", msg, ok)
	}
	if !m.offBaseline(newSvc) {
		t.Error("removed listener should be off-baseline again")
	}

	// It survives a restart.
	m.ports = []probe.ListenPort{sshd, newSvc}
	m.acceptListener(newSvc)
	reloaded := NewModel().WithConfig()
	if reloaded.offBaseline(newSvc) {
		t.Error("accepted listener did not survive a reload")
	}

	// Ephemeral UDP sockets are not part of the service surface.
	udp := probe.ListenPort{Proto: "udp", Local: "10.0.0.1:47572", PID: 3, Process: "firefox"}
	if msg, ok := m.acceptListener(udp); ok || !strings.Contains(msg, "Ephemeral") {
		t.Errorf("accepting an ephemeral socket = (%q, %v), want a refusal", msg, ok)
	}
}

func TestAcceptListenerKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := eventsModel()
	m.activeTab = tabPorts
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}}
	m.setBaseline()
	m.ports = append(m.ports, probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:9999", PID: 2, Process: "app"})
	m.portsSortCol = indexOfCol(t, portsSortCols, "port")

	// Put the cursor on the off-baseline row and accept it.
	rows := m.filteredPorts()
	for i, p := range rows {
		if strings.Contains(p.Local, "9999") {
			m.portsCursor = i
		}
	}
	model, _ := m.Update(key("a"))
	m = model.(Model)
	if !strings.Contains(m.status, "Accepted") {
		t.Errorf("status = %q, want an acceptance", m.status)
	}
	if m.offBaselineCount() != 0 {
		t.Errorf("off-baseline count = %d, want 0 after accepting", m.offBaselineCount())
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

func TestLatencyAndLossAlerts(t *testing.T) {
	m := eventsModel()
	m.alertRules = alertRules{rttMs: 200, retransPerMin: 30}
	t0 := time.Now()

	slow := probe.Conn{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443",
		State: "ESTABLISHED", PID: 1, Process: "curl", RTT: 450 * time.Millisecond, Retrans: 5}
	m.noteQualityAlerts([]probe.Conn{slow}, t0)

	if countKind(m.events, "latency") != 1 {
		t.Fatalf("expected a latency alert, got %v", kindsOf(m.events))
	}
	e := m.events[len(m.events)-1]
	if e.Level != levelWarn || !strings.Contains(e.Detail, "threshold") {
		t.Errorf("latency event = %+v", e)
	}
	// No retransmit alert yet: the first poll only establishes the baseline.
	if countKind(m.events, "packet_loss") != 0 {
		t.Errorf("loss alerted without a delta: %v", kindsOf(m.events))
	}

	// 60 more retransmits in 30s is 120/min, well over the threshold.
	slow.Retrans = 65
	m.noteQualityAlerts([]probe.Conn{slow}, t0.Add(30*time.Second))
	if countKind(m.events, "packet_loss") != 1 {
		t.Fatalf("expected a packet_loss alert, got %v", kindsOf(m.events))
	}
	if !strings.Contains(m.events[len(m.events)-1].Detail, "retransmits/min") {
		t.Errorf("loss event = %+v", m.events[len(m.events)-1])
	}

	// A healthy connection raises nothing.
	m2 := eventsModel()
	m2.alertRules = alertRules{rttMs: 200, retransPerMin: 30}
	fine := probe.Conn{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", RTT: 12 * time.Millisecond}
	m2.noteQualityAlerts([]probe.Conn{fine}, t0)
	m2.noteQualityAlerts([]probe.Conn{fine}, t0.Add(time.Minute))
	if len(m2.events) != 0 {
		t.Errorf("healthy connection produced %v", kindsOf(m2.events))
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

// A service moving from loopback to a wildcard bind is one event, and an alert.
func TestListenerMovedCorrelation(t *testing.T) {
	local := probe.ListenPort{Proto: "tcp", Local: "127.0.0.1:5432", PID: 10, Process: "postgres"}
	exposed := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:5432", PID: 10, Process: "postgres"}

	m := feed(t, eventsModel(), []probe.ListenPort{local}, nil)
	m = feed(t, m, []probe.ListenPort{exposed}, nil)

	if n := countKind(m.events, "listener_moved"); n != 1 {
		t.Fatalf("expected one listener_moved, got %v", kindsOf(m.events))
	}
	if countKind(m.events, "listener_up") != 0 || countKind(m.events, "listener_down") != 0 {
		t.Errorf("the move should replace the up/down pair, got %v", kindsOf(m.events))
	}
	e := m.events[len(m.events)-1]
	if e.Level != levelAlert {
		t.Errorf("widening exposure = %v, want an alert", e.Level)
	}
	if !strings.Contains(e.Detail, "127.0.0.1:5432") || !strings.Contains(e.Detail, "local → exposed") {
		t.Errorf("move detail = %q, want both addresses and the exposure change", e.Detail)
	}

	// Moving back the other way is information, not an alert.
	m = feed(t, m, []probe.ListenPort{local}, nil)
	back := m.events[len(m.events)-1]
	if back.Kind != "listener_moved" || back.Level != levelAlert && back.Level != levelInfo {
		t.Fatalf("unexpected event %+v", back)
	}
	if back.Level != levelInfo {
		t.Errorf("narrowing exposure = %v, want info", back.Level)
	}
}

// Unrelated listeners appearing and disappearing must not be mistaken for a
// move just because they happened in the same poll.
func TestUnrelatedListenersAreNotAMove(t *testing.T) {
	a := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	b := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:8080", PID: 2, Process: "app"}

	m := feed(t, eventsModel(), []probe.ListenPort{a}, nil)
	m = feed(t, m, []probe.ListenPort{b}, nil)

	if countKind(m.events, "listener_moved") != 0 {
		t.Errorf("different ports/processes were correlated as a move: %v", kindsOf(m.events))
	}
	if countKind(m.events, "listener_up") != 1 || countKind(m.events, "listener_down") != 1 {
		t.Errorf("expected one up and one down, got %v", kindsOf(m.events))
	}
}

func TestProcessAggregationByName(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabProcs
	m.hasThroughput = true
	m.procs = []probe.ProcNet{
		{PID: 10, Name: "firefox", User: "vlad", ConnCount: 3, ListenCount: 0, CPU: 5, Mem: 2},
		{PID: 11, Name: "firefox", User: "vlad", ConnCount: 4, ListenCount: 1, CPU: 3, Mem: 1},
		{PID: 12, Name: "firefox", User: "root", ConnCount: 1, CPU: 1, Mem: 1},
		{PID: 20, Name: "sshd", User: "root", ConnCount: 2, ListenCount: 1},
	}
	m.procRates = map[int32]rateSample{10: {rx: 1000}, 11: {rx: 2000}, 12: {rx: 500}}
	m.procTotals = map[int32]*talker{
		10: {label: "firefox", totals: byteTotals{rx: 10}},
		11: {label: "firefox", totals: byteTotals{rx: 20}},
	}

	if got := len(m.filteredProcs()); got != 4 {
		t.Fatalf("un-aggregated rows = %d, want 4", got)
	}

	model, _ := m.Update(key("A"))
	m = model.(Model)
	if !m.aggregateProcs {
		t.Fatal("A should turn on aggregation")
	}

	rows := m.filteredProcs()
	if len(rows) != 2 {
		t.Fatalf("aggregated rows = %d, want 2", len(rows))
	}

	var ff procRow
	for _, r := range rows {
		if r.Name == "firefox" {
			ff = r
		}
	}
	if ff.members != 3 {
		t.Errorf("firefox row folds %d processes, want 3", ff.members)
	}
	if ff.ConnCount != 8 || ff.ListenCount != 1 {
		t.Errorf("counts = conns %d listen %d, want 8 and 1", ff.ConnCount, ff.ListenCount)
	}
	if ff.CPU != 9 {
		t.Errorf("CPU = %v, want the sum (9)", ff.CPU)
	}
	if ff.User != mixedValue {
		t.Errorf("user = %q, want the mixed marker for differing owners", ff.User)
	}
	if ff.PID != 10 {
		t.Errorf("representative PID = %d, want the lowest (10)", ff.PID)
	}
	// Throughput sums across the members.
	if got := m.rowRate(ff); got.rx != 3500 {
		t.Errorf("group rate = %v, want 3500", got.rx)
	}
	if got := m.rowTotal(ff); got.rx != 30 {
		t.Errorf("group total = %v, want 30", got.rx)
	}

	// The PID column says how many processes the row stands for.
	if got := ff.pidCell(); got != "×3" {
		t.Errorf("pid cell = %q, want ×3", got)
	}
	text := m.renderProcsText()
	if !strings.Contains(text, "×3") || !strings.Contains(text, "firefox") {
		t.Errorf("aggregated row missing from the table:\n%s", text)
	}

	// Signalling a group is refused: it stands for several processes.
	for i, r := range rows {
		if r.Name == "firefox" {
			m.procsCursor = i
		}
	}
	if _, _, ok := m.selectedPID(); ok {
		t.Error("a folded group should not offer a signal target")
	}
	detail, ok := m.detailContent()
	if !ok || !strings.Contains(detail, "Process group") || !strings.Contains(detail, "10") {
		t.Errorf("group detail should name the members:\n%s", detail)
	}

	// And it survives a restart.
	m.saveConfig()
	if !NewModel().WithConfig().aggregateProcs {
		t.Error("aggregation toggle did not persist")
	}
}

func TestAggregationSortsBySummedThroughput(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabProcs
	m.hasThroughput = true
	m.aggregateProcs = true
	m.procs = []probe.ProcNet{
		{PID: 1, Name: "chatty", ConnCount: 50},
		{PID: 2, Name: "heavy", ConnCount: 1},
		{PID: 3, Name: "heavy", ConnCount: 1},
	}
	// Neither "heavy" process beats "chatty" alone, but together they do.
	m.procRates = map[int32]rateSample{1: {rx: 900}, 2: {rx: 600}, 3: {rx: 600}}
	m.procsSortCol = indexOfCol(t, procsSortCols, "rx")
	m.procsSortDesc = true

	if got := m.filteredProcs()[0].Name; got != "heavy" {
		t.Errorf("sorted first = %q, want the group with the larger summed rate", got)
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

func TestHiddenColumns(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabConns
	m.conns = []probe.Conn{conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 0, 0)}

	if !strings.Contains(m.renderConnsText(), "SVC") {
		t.Fatal("the service column should be shown by default")
	}

	m.hiddenCols = map[string]bool{"svc": true, "age": true}
	text := m.renderConnsText()
	if strings.Contains(text, "SVC") || strings.Contains(text, "AGE") {
		t.Errorf("hidden columns still rendered:\n%s", text)
	}
	if !strings.Contains(text, "REMOTE") {
		t.Errorf("hiding two columns should not disturb the rest:\n%s", text)
	}
	// Rows stay aligned with the header: nothing may exceed the viewport. Width
	// is measured in display columns, not bytes — the rule is drawn with a
	// three-byte rune.
	for _, line := range strings.Split(stripANSI(text), "\n") {
		if w := lipgloss.Width(line); w > m.connsVP.Width {
			t.Errorf("line %d columns wide, viewport is %d: %q", w, m.connsVP.Width, line)
		}
	}
	// A hidden column is still sortable, since the data is only off the screen.
	if _, ok := m.connsColumns().keyAt(0); !ok {
		t.Error("remaining columns should still be hit-testable")
	}

	// Hiding everything would look like a bug, so the layout refuses.
	m.hiddenCols = map[string]bool{}
	for _, c := range m.connsColumns() {
		m.hiddenCols[c.key] = true
	}
	if len(m.connsColumns()) == 0 {
		t.Error("hiding every column should fall back to showing them")
	}
}

func TestHiddenColumnsPersist(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := eventsModel()
	m.hiddenCols = map[string]bool{"svc": true}
	m.saveConfig()

	reloaded := NewModel().WithConfig()
	if !reloaded.hiddenCols["svc"] {
		t.Errorf("hidden columns did not survive a reload: %v", reloaded.hiddenCols)
	}
}

func TestInterfaceAttributionAndFilter(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabConns
	m.lastSnap = probe.NetSnapshot{Ifaces: []probe.IfaceInfo{
		{Name: "eth0", Addrs: []string{"192.168.1.5/24"}},
		{Name: "tun0", Addrs: []string{"10.8.0.2/24"}},
	}}

	lan := conn(1, "curl", "192.168.1.5:5000", "1.1.1.1:443", 0, 0)
	vpn := conn(2, "curl", "10.8.0.2:6000", "2.2.2.2:443", 0, 0)
	m = feed(t, m, nil, []probe.Conn{lan, vpn})

	if got := m.ifaceFor(lan); got != "eth0" {
		t.Errorf("LAN connection attributed to %q, want eth0", got)
	}
	if got := m.ifaceFor(vpn); got != "tun0" {
		t.Errorf("VPN connection attributed to %q, want tun0", got)
	}
	if !m.multiIface() {
		t.Error("two interfaces in use should show the IFACE column")
	}
	if !strings.Contains(m.renderConnsText(), "tun0") {
		t.Errorf("interface column missing:\n%s", m.renderConnsText())
	}

	// i cycles: all -> eth0 -> tun0 -> all.
	model, _ := m.Update(key("i"))
	m = model.(Model)
	if m.ifaceFilter != "eth0" {
		t.Fatalf("first press selected %q, want eth0", m.ifaceFilter)
	}
	if rows := m.filteredConns(); len(rows) != 1 || rows[0].Local != lan.Local {
		t.Errorf("eth0 filter kept %d rows, want just the LAN connection", len(rows))
	}
	if !strings.Contains(m.filterLabel(), "eth0") {
		t.Errorf("filter label = %q", m.filterLabel())
	}

	model, _ = m.Update(key("i"))
	m = model.(Model)
	if m.ifaceFilter != "tun0" {
		t.Fatalf("second press selected %q, want tun0", m.ifaceFilter)
	}
	model, _ = m.Update(key("i"))
	m = model.(Model)
	if m.ifaceFilter != "" {
		t.Errorf("third press selected %q, want back to all", m.ifaceFilter)
	}
	if len(m.filteredConns()) != 2 {
		t.Error("clearing the filter should show every connection again")
	}
}

// On a single-NIC host the column would say the same thing on every row.
func TestInterfaceColumnHiddenOnSingleLink(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabConns
	m.lastSnap = probe.NetSnapshot{Ifaces: []probe.IfaceInfo{{Name: "eth0", Addrs: []string{"192.168.1.5/24"}}}}
	m = feed(t, m, nil, []probe.Conn{
		conn(1, "a", "192.168.1.5:1", "1.1.1.1:443", 0, 0),
		conn(2, "b", "192.168.1.5:2", "2.2.2.2:443", 0, 0),
	})

	if m.multiIface() {
		t.Error("a single interface should not warrant the column")
	}
	if strings.Contains(m.renderConnsText(), "IFACE") {
		t.Errorf("IFACE column shown on a single-link host:\n%s", m.renderConnsText())
	}
}

func TestChurnChart(t *testing.T) {
	m := eventsModel()
	m.w, m.h = 160, 45
	m.lastSnap = probe.NetSnapshot{TakenAt: time.Now(), Hostname: "host"}

	if m.renderChurn(80) != "" {
		t.Error("the churn chart should stay hidden until there is churn")
	}

	// Two polls: one connection appears, then it goes away.
	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 0, 0)
	m = feed(t, m, nil, nil)
	m = feed(t, m, nil, []probe.Conn{c})
	m = feed(t, m, nil, nil)

	chart := m.renderChurn(80)
	if !strings.Contains(chart, "Connection churn") {
		t.Fatalf("churn chart missing:\n%s", chart)
	}
	if !strings.Contains(chart, "opened") || !strings.Contains(chart, "closed") {
		t.Errorf("chart should show both directions:\n%s", chart)
	}
	if !strings.Contains(m.viewOverview(), "Connection churn") {
		t.Error("the Overview should carry the churn panel")
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

func TestQuitConfirmation(t *testing.T) {
	m := eventsModel()

	model, cmd := m.Update(key("q"))
	m = model.(Model)
	if !m.confirmingQuit {
		t.Fatal("q should ask rather than quit outright")
	}
	if cmd != nil {
		t.Error("q alone should not issue a quit command")
	}
	if !strings.Contains(m.View(), "Quit ducknetview?") {
		t.Error("the confirmation should be visible")
	}

	// Anything else cancels.
	model, _ = m.Update(key("x"))
	m = model.(Model)
	if m.confirmingQuit {
		t.Error("a stray key should cancel the confirmation")
	}

	// y confirms.
	model, _ = m.Update(key("q"))
	m = model.(Model)
	if _, cmd = m.Update(key("y")); cmd == nil {
		t.Error("y should confirm the quit")
	}
}
