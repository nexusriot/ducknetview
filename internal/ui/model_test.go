package ui

import (
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nexusriot/ducknetview/internal/probe"
)

// The global bindings must still work when no search box is focused.
func TestGlobalKeysWhenNotSearching(t *testing.T) {
	m := NewModel()
	m.activeTab = tabOverview

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = model.(Model)
	if m.activeTab != tabIfaces {
		t.Fatalf("right should advance Overview -> Interfaces, got %v", m.activeTab)
	}

	// 'q' is intentionally a no-op (only ctrl+c quits).
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd != nil {
		t.Fatal("'q' should be a no-op when not searching")
	}

	m = typeKey(t, m, "/")
	// '/' has no meaning on the Interfaces tab, so no search should start.
	if m.anySearching() {
		t.Fatal("'/' should not start a search on the Interfaces tab")
	}
}

// Every rendered table row is prefixed with a 2-column cursor gutter. The
// column math must reserve that width so rows fit inside the viewport and are
// not hard-clipped on the right (which used to drop 2 chars off the last col).
func TestTableRowsFitWidth(t *testing.T) {
	const wdt = 100

	build := func() Model {
		m := NewModel()
		m.ports = []probe.ListenPort{
			{Proto: "tcp", Local: "0.0.0.0:8080", PID: 1234567, Process: "a-really-long-listener-process-name"},
		}
		m.procs = []probe.ProcNet{
			{PID: 1234567, Name: "a-really-long-process-name-here", ConnCount: 42, ListenCount: 7},
		}
		m.conns = []probe.Conn{
			{Proto: "tcp", Local: "10.0.0.1:5000", Remote: "203.0.113.9:443", State: "ESTABLISHED", PID: 1234567, Process: "a-really-long-client-process-name"},
		}
		m.portsVP.Width = wdt
		m.procsVP.Width = wdt
		m.connsVP.Width = wdt
		return m
	}

	check := func(t *testing.T, name, out string) {
		for _, ln := range strings.Split(out, "\n") {
			if w := lipgloss.Width(ln); w > wdt {
				t.Fatalf("%s: line exceeds viewport width %d: got %d %q", name, wdt, w, ln)
			}
		}
	}

	m := build()
	check(t, "ports", m.renderPortsText())
	check(t, "procs", m.renderProcsText())
	check(t, "conns", m.renderConnsText())

	m.connsGroup = true
	check(t, "conns-grouped", m.renderConnsText())
}

// The interface details sparklines must be sized to the width the caller passes
// (Overview box vs. Interfaces pane), so they never overrun their container. At
// realistic widths the whole block (incl. the widest dynamic line, the chart)
// must fit; short fixed lines like MAC are hard-clipped by callers separately.
func TestIfaceDetailsRespectsWidth(t *testing.T) {
	m := NewModel()
	m.selectedIface = "eth0"
	m.lastSnap = probe.NetSnapshot{Ifaces: []probe.IfaceInfo{
		{Name: "eth0", IsUp: true, MTU: 1500, Kind: probe.IfacePhysical, Hardware: "aa:bb:cc:dd:ee:ff"},
	}}
	m.rxHist = []float64{1, 2, 3, 4, 5, 6, 7, 8}
	m.txHist = []float64{8, 7, 6, 5, 4, 3, 2, 1}

	for _, avail := range []int{40, 80, 120} {
		out := m.renderIfaceDetailsText(avail)
		for _, ln := range strings.Split(out, "\n") {
			if w := lipgloss.Width(ln); w > avail {
				t.Fatalf("avail=%d: details line exceeds width: got %d %q", avail, w, ln)
			}
		}
	}
}

// View must never panic across tabs and terminal sizes, including degenerate
// widths (guards the column / strings.Repeat width math against negatives).
func TestViewNoPanicAcrossSizes(t *testing.T) {
	sizes := [][2]int{{0, 0}, {1, 1}, {5, 3}, {20, 10}, {80, 24}, {200, 50}}
	tabs := []tab{tabOverview, tabIfaces, tabPorts, tabProcs, tabConns}

	for _, sz := range sizes {
		m := NewModel()
		m.lastSnap = probe.NetSnapshot{
			Hostname: "h", TakenAt: time.Unix(1, 0),
			Ifaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true, MTU: 1500, Kind: probe.IfacePhysical}},
		}
		m.selectedIface = "eth0"
		m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}}
		m.procs = []probe.ProcNet{{PID: 1, Name: "sshd", ConnCount: 1, ListenCount: 1}}
		m.conns = []probe.Conn{{Proto: "tcp", Local: "10.0.0.1:5", Remote: "1.2.3.4:443", State: "ESTABLISHED", PID: 1, Process: "sshd"}}

		model, _ := m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		m = model.(Model)
		m.rebuildIfaceList()

		for _, tb := range tabs {
			m.activeTab = tb
			_ = m.View()
		}
		// Exercise the connections variants too.
		m.activeTab = tabConns
		m.connsGroup = true
		_ = m.View()
		m.connsRevDNS = true
		_ = m.View()
		m.showHelp = true
		_ = m.View()
	}
}

// The Overview body must fit inside its box on a wide terminal — the sparkline
// used to be sized from the Interfaces pane width and overflowed here.
func TestOverviewFitsWidth(t *testing.T) {
	m := NewModel()
	m.w, m.h = 200, 40 // wide terminal: interfaces-pane width would exceed the box
	m.selectedIface = "eth0"
	m.lastSnap = probe.NetSnapshot{
		Hostname: "host",
		TakenAt:  time.Unix(1, 0),
		Ifaces: []probe.IfaceInfo{
			{Name: "eth0", IsUp: true, MTU: 1500, Kind: probe.IfacePhysical, Hardware: "aa:bb:cc:dd:ee:ff"},
		},
	}
	for i := 0; i < 50; i++ {
		m.rxHist = append(m.rxHist, float64(i))
		m.txHist = append(m.txHist, float64(50-i))
	}

	boxW := min(m.w-2, 120)
	for _, ln := range strings.Split(m.viewOverview(), "\n") {
		if w := lipgloss.Width(ln); w > boxW+2 { // +2 = box border
			t.Fatalf("overview line exceeds box width %d: got %d %q", boxW+2, w, ln)
		}
	}
}

// highlightFold previously sliced a []rune with byte offsets from strings.Index,
// which mis-highlighted and could panic on multi-byte UTF-8. These inputs would
// have triggered the old bug.
func TestHighlightFoldNoPanicMultibyte(t *testing.T) {
	cases := []struct{ s, q string }{
		{"café", "é"},
		{"café", "fé"},
		{"СерверПроцесс", "сервер"},
		{"日本語procd", "proc"},
		{"plain-ascii-proc", "proc"},
		{"", "x"},
		{"abc", ""},
	}
	for _, c := range cases {
		out := highlightFold(c.s, c.q) // must not panic
		// The visible text (ignoring injected ANSI) must still contain the source.
		if c.q == "" && out != c.s {
			t.Fatalf("empty query should return input unchanged: %q -> %q", c.s, out)
		}
	}
}

func TestGroupedConnsRender(t *testing.T) {
	m := NewModel()
	m.connsGroup = true
	m.connsVP.Width = 120
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:5000", Remote: "1.2.3.4:443", State: "ESTABLISHED", PID: 10, Process: "curl"},
		{Proto: "tcp", Local: "10.0.0.1:5001", Remote: "1.2.3.4:443", State: "TIME_WAIT", PID: 10, Process: "curl"},
		{Proto: "tcp", Local: "10.0.0.1:6000", Remote: "5.6.7.8:80", State: "ESTABLISHED", PID: 20, Process: "wget"},
	}
	out := m.renderConnsText() // dispatches to renderConnsGrouped
	if !strings.Contains(out, "grouped by remote host") {
		t.Fatalf("expected grouped header, got:\n%s", out)
	}
	if !strings.Contains(out, "1.2.3.4") || !strings.Contains(out, "5.6.7.8") {
		t.Fatalf("expected both remote hosts in grouped output:\n%s", out)
	}
	// 1.2.3.4 has 2 conns and should sort first (descending count).
	i14 := strings.Index(out, "1.2.3.4")
	i58 := strings.Index(out, "5.6.7.8")
	if i14 > i58 {
		t.Fatalf("expected busier host (1.2.3.4) listed first")
	}
}

// The small pure functions below are each one line of logic that the rest of
// the suite only reaches incidentally — a wrong answer in any of them shows up
// as a mislabelled cell rather than as a failure, which is the kind of bug
// that survives for releases.

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{0, "0s"},
		{42 * time.Second, "42s"},
		{3*time.Minute + 7*time.Second, "3m07s"},
		{time.Hour + 5*time.Minute, "1h05m"},
		{49 * time.Hour, "2d01h"},
	}
	for _, c := range cases {
		if got := formatDuration(c.in); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}

	if got := formatAge(time.Now(), time.Time{}); got != "-" {
		t.Errorf("a connection never seen has age %q, want -", got)
	}
	now := time.Now()
	if got := formatAge(now, now.Add(-90*time.Second)); got != "1m30s" {
		t.Errorf("formatAge = %q", got)
	}
}

func TestFormatCountAndRoundDuration(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want string
	}{{0, "0"}, {999, "999"}, {1500, "1.5k"}, {2_500_000, "2.5M"}} {
		if got := formatCount(c.in); got != c.want {
			t.Errorf("formatCount(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, c := range []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "2m0s"},
		{95 * time.Minute, "2h0m0s"},
	} {
		if got := roundDuration(c.in); got != c.want {
			t.Errorf("roundDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Both of these splice byte offsets into strings that may contain escape
// sequences and multi-byte runes, which is where mis-slicing would panic.
func TestANSIHelpersOnAwkwardInput(t *testing.T) {
	const esc = "\x1b"
	cases := []struct{ in, plain string }{
		{"plain", "plain"},
		{esc + "[31mred" + esc + "[0m", "red"},
		{esc + "]0;a title" + "\x07" + "after", "after"},
		{esc + "]0;a title" + esc + `\` + "after", "after"},
		{"naïve ünïcode", "naïve ünïcode"},
		{"日本語", "日本語"},
		{esc, ""},
		{esc + "[", ""},
	}
	for _, c := range cases {
		if got := stripANSI(c.in); got != c.plain {
			t.Errorf("stripANSI(%q) = %q, want %q", c.in, got, c.plain)
		}
	}

	// Truncation counts display cells, not bytes, and never cuts an escape
	// sequence in half.
	if got := ansiSafeTruncate("abcdef", 3); got != "abc" {
		t.Errorf("ansiSafeTruncate = %q", got)
	}
	if got := ansiSafeTruncate("", 5); got != "" {
		t.Errorf("ansiSafeTruncate(empty) = %q", got)
	}
	if got := ansiSafeTruncate("abc", 0); got != "" {
		t.Errorf("ansiSafeTruncate(width 0) = %q", got)
	}
	coloured := esc + "[31m" + "abcdef" + esc + "[0m"
	got := ansiSafeTruncate(coloured, 3)
	if stripANSI(got) != "abc" {
		t.Errorf("truncating coloured text gave %q (%q plain)", got, stripANSI(got))
	}
	if !strings.HasPrefix(got, esc+"[31m") {
		t.Errorf("the colour was dropped: %q", got)
	}
	// Wide runes take two cells.
	if plain := stripANSI(ansiSafeTruncate("日本語", 4)); plain != "日本" {
		t.Errorf("wide-rune truncation gave %q", plain)
	}
	// Invalid UTF-8 must not panic or loop.
	_ = ansiSafeTruncate("\xff\xfe abc", 4)
}

func TestSigName(t *testing.T) {
	for _, c := range []struct {
		sig  syscall.Signal
		want string
	}{
		{syscall.SIGTERM, "SIGTERM"},
		{syscall.SIGKILL, "SIGKILL"},
		{syscall.SIGHUP, "SIGHUP"},
	} {
		if got := sigName(c.sig); got != c.want {
			t.Errorf("sigName(%v) = %q, want %q", c.sig, got, c.want)
		}
	}
	if got := sigName(syscall.SIGUSR1); got == "" {
		t.Error("an unlisted signal should still have a name")
	}
}

func TestTabLabels(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < tabCount; i++ {
		label := tabLabel(tab(i))
		if label == "" || label == "?" {
			t.Errorf("tab %d has no label", i)
		}
		if seen[label] {
			t.Errorf("two tabs are both called %q", label)
		}
		seen[label] = true
	}
	if tabLabel(tab(99)) != "?" {
		t.Error("an impossible tab should not be given a name")
	}
}

// A view's one-line summary is what makes a list of names readable a month
// later.
func TestDescribeView(t *testing.T) {
	got := describeView(savedView{
		Name: "vpn", Tab: int(tabConns),
		ProtoFilter: 1, IPVerFilter: 2, ConnStateFilter: 1,
		PublicOnly: true, OnlyMine: true, IfaceFilter: "wg0",
		ConnsGroup: true, ShowTotals: true, HideNoise: true,
		ConnsQuery: "443",
	})
	for _, want := range []string{"Connections", "tcp", "ipv6", "established", "public", "mine", "wg0", "grouped", "totals", "hide noise", "/443"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeView does not mention %q: %q", want, got)
		}
	}

	// A plain view describes itself with just its tab.
	if got := describeView(savedView{Tab: int(tabPorts)}); got != "Ports" {
		t.Errorf("describeView(plain) = %q", got)
	}
	// An out-of-range tab from a hand-edited config must not panic.
	if got := describeView(savedView{Tab: 99}); got == "" {
		t.Error("an out-of-range tab produced no description")
	}

	// The query shown is the one the view's own tab carries.
	for _, c := range []struct {
		v    savedView
		want string
	}{
		{savedView{Tab: int(tabPorts), PortsQuery: "p"}, "p"},
		{savedView{Tab: int(tabProcs), ProcsQuery: "q"}, "q"},
		{savedView{Tab: int(tabConns), ConnsQuery: "c"}, "c"},
		{savedView{Tab: int(tabEvents), EventsQuery: "e"}, "e"},
		{savedView{Tab: int(tabOverview), ConnsQuery: "c"}, ""},
	} {
		if got := viewQuery(c.v); got != c.want {
			t.Errorf("viewQuery(tab %d) = %q, want %q", c.v.Tab, got, c.want)
		}
	}
}

// The interface list's items are what the list widget filters and draws.
func TestIfaceItem(t *testing.T) {
	it := ifaceItem{name: "eth0", desc: "physical  RX 1 B/s  TX 2 B/s"}
	if it.Title() != "eth0" || it.FilterValue() != "eth0" {
		t.Errorf("ifaceItem identifies itself as %q/%q", it.Title(), it.FilterValue())
	}
	if it.Description() == "" {
		t.Error("ifaceItem has no description")
	}
}

// The tickers are one line each, and a wrong one silently stops the session
// refreshing.
func TestTickersProduceTheirOwnMessages(t *testing.T) {
	for name, cmd := range map[string]tea.Cmd{
		"tick":      tickEvery(time.Millisecond),
		"extIP":     extIPTickEvery(time.Millisecond),
		"latency":   latencyTickEvery(time.Millisecond),
		"clipboard": copyToClipboardCmd("x", "thing"),
	} {
		msg := cmd()
		if msg == nil {
			t.Errorf("%s produced no message", name)
		}
	}
	if _, ok := tickEvery(time.Millisecond)().(tickMsg); !ok {
		t.Error("the refresh ticker does not produce a tickMsg")
	}
	if _, ok := extIPTickEvery(time.Millisecond)().(extIPTickMsg); !ok {
		t.Error("the external-IP ticker does not produce an extIPTickMsg")
	}
	if _, ok := latencyTickEvery(time.Millisecond)().(latencyTickMsg); !ok {
		t.Error("the latency ticker does not produce a latencyTickMsg")
	}
	// Measuring nothing is not a command at all, rather than one that returns
	// an empty round.
	if measureLatencyCmd(nil) != nil {
		t.Error("measuring no targets should produce no command")
	}
}

// The keypress that opens a text field must not also be typed into it: `n`
// named every view "n…", and `space` put a stray space in front of every
// threshold typed into the settings overlay.
func TestOpeningATextFieldDoesNotTypeIntoIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, `{}`)

	m := NewModel().WithConfig()
	m.w, m.h = 160, 48

	model, _ := m.Update(key("V"))
	m = model.(Model)
	model, _ = m.Update(key("n"))
	m = model.(Model)
	if got := m.viewsInput.Value(); got != "" {
		t.Errorf("the views name field starts with %q", got)
	}
	m.showViews = false

	model, _ = m.Update(key(","))
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = model.(Model)
	if !m.settingsEditing {
		t.Fatal("space should open the settings editor")
	}
	if got := m.settingsInput.Value(); got != "" {
		t.Errorf("the settings value field starts with %q", got)
	}
}

// The key list is longer than any terminal, and the overlay had no scrolling:
// its first two sections could not be read at all. Caught by the e2e suite
// driving a real 48-row terminal.
func TestHelpOverlayFitsTheTerminal(t *testing.T) {
	for _, h := range []int{24, 48, 80} {
		m := NewModel()
		m.w, m.h = 160, h
		m.openHelp()

		drawn := strings.Count(m.renderHelp(), "\n") + 1
		if drawn > h {
			t.Errorf("on a %d-row terminal the help overlay draws %d rows", h, drawn)
		}
		// The top of the list is what used to be unreachable.
		if !strings.Contains(stripANSI(m.renderHelp()), "key bindings") {
			t.Errorf("on a %d-row terminal the help overlay does not start at the top", h)
		}
	}

	// And it scrolls, which is the only way to reach the rest.
	m := NewModel()
	m.w, m.h = 160, 24
	m.openHelp()
	top := m.renderHelp()
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = model.(Model)
	if !m.showHelp {
		t.Fatal("paging closed the overlay instead of scrolling it")
	}
	if m.renderHelp() == top {
		t.Error("the help overlay did not scroll")
	}

	// Anything else still closes it.
	model, _ = m.Update(key("x"))
	if model.(Model).showHelp {
		t.Error("a key that is not a scroll should close the overlay")
	}
}

// space pauses/resumes and +/- step the refresh interval within bounds.
func TestRefreshControls(t *testing.T) {
	m := NewModel()
	if m.paused {
		t.Fatal("should start unpaused")
	}

	model, _ := m.Update(key(" "))
	m = model.(Model)
	if !m.paused {
		t.Fatal("space should pause")
	}
	model, _ = m.Update(key(" "))
	m = model.(Model)
	if m.paused {
		t.Fatal("space should resume")
	}

	// Default is the fastest step; '+' can't go faster, '-' goes slower.
	start := m.refreshInterval
	model, _ = m.Update(key("+"))
	m = model.(Model)
	if m.refreshInterval != start {
		t.Fatalf("'+' at fastest step changed interval: %v", m.refreshInterval)
	}
	model, _ = m.Update(key("-"))
	m = model.(Model)
	if m.refreshInterval <= start {
		t.Fatalf("'-' should slow the interval, got %v", m.refreshInterval)
	}
	// Clamp at the slowest step no matter how many times we slow down.
	for i := 0; i < 20; i++ {
		model, _ = m.Update(key("-"))
		m = model.(Model)
	}
	if m.refreshInterval != refreshSteps[len(refreshSteps)-1] {
		t.Fatalf("interval not clamped to slowest step: %v", m.refreshInterval)
	}
}

// When paused, a tick must not schedule a sample/enumeration, but must still
// re-arm the ticker so resume works.
func TestPausedTickSkipsWork(t *testing.T) {
	m := NewModel()
	m.paused = true
	_, cmd := m.Update(tickMsg(time.Unix(100, 0)))
	if cmd == nil {
		t.Fatal("tick should still re-arm the ticker while paused")
	}
	// (We can't easily assert the batch contents, but a nil cmd would mean the
	// ticker died — the important regression to guard.)
}

// 'x' arms the kill confirmation for the selected PID; 'y' fires the kill, 'n'
// cancels. Grouped connections have no single PID and must not arm.
func TestKillConfirmFlow(t *testing.T) {
	m := NewModel()
	m.activeTab = tabProcs
	m.procs = []probe.ProcNet{{PID: 4242, Name: "victim", ConnCount: 1}}

	model, _ := m.Update(key("x"))
	m = model.(Model)
	if !m.confirmingKill || m.killPID != 4242 {
		t.Fatalf("'x' should arm kill for PID 4242, got confirming=%v pid=%d", m.confirmingKill, m.killPID)
	}

	// 'n' cancels without issuing a command.
	model, cmd := m.Update(key("n"))
	m = model.(Model)
	if m.confirmingKill {
		t.Fatal("'n' should cancel the kill confirmation")
	}
	if cmd != nil {
		t.Fatal("'n' should not issue a kill command")
	}

	// Re-arm and confirm with 'y' -> a command is returned.
	model, _ = m.Update(key("x"))
	m = model.(Model)
	model, cmd = m.Update(key("y"))
	m = model.(Model)
	if m.confirmingKill {
		t.Fatal("'y' should clear the confirmation")
	}
	if cmd == nil {
		t.Fatal("'y' should issue a kill command")
	}

	// Grouped connections: no single PID, so 'x' must not arm.
	g := NewModel()
	g.activeTab = tabConns
	g.connsGroup = true
	g.conns = []probe.Conn{{Proto: "tcp", Local: "1.1.1.1:1", Remote: "2.2.2.2:2", State: "ESTABLISHED", PID: 1, Process: "p"}}
	model, _ = g.Update(key("x"))
	g = model.(Model)
	if g.confirmingKill {
		t.Fatal("'x' must not arm kill in grouped connections view")
	}
}

// Connection deltas: first enumeration is a baseline (nothing new); the second
// flags appeared/closed connections.
func TestConnDelta(t *testing.T) {
	m := NewModel()
	c1 := probe.Conn{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:80", State: "ESTABLISHED", PID: 1}
	c2 := probe.Conn{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:80", State: "ESTABLISHED", PID: 2}

	model, _ := m.Update(connInfoMsg(probe.ConnInfo{Conns: []probe.Conn{c1, c2}}))
	m = model.(Model)
	if m.connAdded != 0 || len(m.newConns) != 0 {
		t.Fatalf("baseline enumeration should flag nothing new: added=%d new=%d", m.connAdded, len(m.newConns))
	}

	c3 := probe.Conn{Proto: "tcp", Local: "10.0.0.1:3", Remote: "3.3.3.3:80", State: "ESTABLISHED", PID: 3}
	model, _ = m.Update(connInfoMsg(probe.ConnInfo{Conns: []probe.Conn{c1, c3}}))
	m = model.(Model)
	if m.connAdded != 1 || m.connRemoved != 1 {
		t.Fatalf("expected +1/-1, got +%d/-%d", m.connAdded, m.connRemoved)
	}
	if !m.newConns[connKey(c3)] {
		t.Fatal("c3 should be flagged new")
	}
	if m.newConns[connKey(c1)] {
		t.Fatal("c1 (still present) should not be flagged new")
	}
}

// Arming a kill and pressing a signal key issues a command and clears the
// confirmation; any other key cancels.
func TestKillSignalChoice(t *testing.T) {
	arm := func() Model {
		m := NewModel()
		m.activeTab = tabProcs
		m.procs = []probe.ProcNet{{PID: 424242, Name: "victim", ConnCount: 1}}
		model, _ := m.Update(key("x"))
		return model.(Model)
	}

	for _, k := range []string{"t", "k", "h", "y"} {
		m := arm()
		if !m.confirmingKill {
			t.Fatalf("kill should be armed before %q", k)
		}
		model, cmd := m.Update(key(k))
		m = model.(Model)
		if m.confirmingKill {
			t.Fatalf("%q should resolve the confirmation", k)
		}
		if cmd == nil {
			t.Fatalf("%q should issue a signal command", k)
		}
	}

	m := arm()
	model, cmd := m.Update(key("n"))
	m = model.(Model)
	if m.confirmingKill || cmd != nil {
		t.Fatal("'n' should cancel with no command")
	}
}

// First-seen times are recorded per connection and carried over across
// enumerations (feeding both the AGE column and new/closed detection).
func TestConnAgeTracking(t *testing.T) {
	m := NewModel()
	c1 := probe.Conn{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:80", State: "ESTABLISHED", PID: 1}

	model, _ := m.Update(connInfoMsg(probe.ConnInfo{Conns: []probe.Conn{c1}}))
	m = model.(Model)
	t1, ok := m.connFirstSeen[connKey(c1)]
	if !ok || t1.IsZero() {
		t.Fatal("c1 first-seen time not recorded")
	}

	c2 := probe.Conn{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:80", State: "ESTABLISHED", PID: 2}
	model, _ = m.Update(connInfoMsg(probe.ConnInfo{Conns: []probe.Conn{c1, c2}}))
	m = model.(Model)
	if got := m.connFirstSeen[connKey(c1)]; !got.Equal(t1) {
		t.Fatalf("c1 first-seen should be carried over unchanged: %v vs %v", got, t1)
	}
	if _, ok := m.connFirstSeen[connKey(c2)]; !ok {
		t.Fatal("c2 first-seen not recorded")
	}
	if !m.newConns[connKey(c2)] {
		t.Fatal("c2 should be flagged new")
	}
	// A dropped connection must be pruned from the first-seen map.
	model, _ = m.Update(connInfoMsg(probe.ConnInfo{Conns: []probe.Conn{c2}}))
	m = model.(Model)
	if _, ok := m.connFirstSeen[connKey(c1)]; ok {
		t.Fatal("dropped c1 should be pruned from first-seen map")
	}
	if m.connRemoved != 1 {
		t.Fatalf("expected 1 closed connection, got %d", m.connRemoved)
	}
}

func TestOverviewSecurityLine(t *testing.T) {
	m := NewModel()
	m.w, m.h = 100, 30
	m.lastSnap = probe.NetSnapshot{
		Hostname: "h", TakenAt: time.Unix(1, 0),
		Ifaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true, Kind: probe.IfacePhysical}},
	}
	m.selectedIface = "eth0"
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:80", PID: 1}}
	m.conns = []probe.Conn{{Proto: "tcp", Local: "10.0.0.1:1", Remote: "8.8.8.8:443", State: "ESTABLISHED", PID: 1}}

	out := m.viewOverview()
	if !strings.Contains(out, "Security:") {
		t.Fatalf("overview should include a Security line:\n%s", out)
	}
	if !strings.Contains(out, "exposed listeners") || !strings.Contains(out, "public conns") {
		t.Fatalf("security line missing expected metrics:\n%s", out)
	}
}

func TestNewListenerDetection(t *testing.T) {
	m := NewModel()
	p1 := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:80", PID: 1}

	model, _ := m.Update(connInfoMsg(probe.ConnInfo{Ports: []probe.ListenPort{p1}}))
	m = model.(Model)
	if len(m.newPorts) != 0 {
		t.Fatalf("baseline enumeration should flag no new listeners, got %d", len(m.newPorts))
	}

	p2 := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:4444", PID: 2}
	model, _ = m.Update(connInfoMsg(probe.ConnInfo{Ports: []probe.ListenPort{p1, p2}}))
	m = model.(Model)
	if !m.newPorts[portKey(p2)] {
		t.Fatal("p2 should be flagged as a new listener")
	}
	if m.newPorts[portKey(p1)] {
		t.Fatal("p1 (still present) should not be flagged new")
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

func TestWifiLineInInterfaceDetails(t *testing.T) {
	m := routesModel()
	m.selectedIface = "wlan0"
	m.lastSnap = probe.NetSnapshot{Ifaces: []probe.IfaceInfo{
		{Name: "wlan0", IsUp: true, Kind: probe.IfacePhysical, MTU: 1500},
	}}
	m.wifi = map[string]probe.WifiLink{
		"wlan0": {Iface: "wlan0", Quality: 62, Signal: -48, Noise: -92},
	}

	text := stripANSI(m.renderIfaceDetailsText(80))
	for _, want := range []string{"Wi-Fi", "-48 dBm", "excellent", "88%", "noise -92"} {
		if !strings.Contains(text, want) {
			t.Errorf("interface details missing %q:\n%s", want, text)
		}
	}

	// A wired interface gets no Wi-Fi line at all.
	m.selectedIface = "eth0"
	m.lastSnap.Ifaces = append(m.lastSnap.Ifaces, probe.IfaceInfo{Name: "eth0", IsUp: true})
	if strings.Contains(stripANSI(m.renderIfaceDetailsText(80)), "Wi-Fi") {
		t.Error("a wired interface should not report radio state")
	}
}

func TestHideNoiseFilter(t *testing.T) {
	m := NewModel()
	m.lastSnap = probe.NetSnapshot{Ifaces: []probe.IfaceInfo{
		{Name: "eth0", Kind: probe.IfacePhysical, IsUp: true},
		{Name: "lo", Kind: probe.IfaceLoopback, IsUp: true},
		{Name: "docker0", Kind: probe.IfaceDockerBridge, IsUp: true},
		{Name: "veth123", Kind: probe.IfaceVeth, IsUp: true},
		{Name: "eth1", Kind: probe.IfacePhysical, IsUp: false}, // down
		{Name: "wg0", Kind: probe.IfaceVirt, IsUp: true},
	}}

	m.rebuildIfaceList()
	if got := len(m.ifaceList.Items()); got != 6 {
		t.Fatalf("expected 6 interfaces unfiltered, got %d", got)
	}

	m.hideNoise = true
	m.rebuildIfaceList()
	names := make([]string, 0)
	for _, it := range m.ifaceList.Items() {
		names = append(names, it.(ifaceItem).name)
	}
	// Keep up physical (eth0) and up virtual/VPN (wg0); drop loopback, docker,
	// veth and the down interface.
	if len(names) != 2 {
		t.Fatalf("expected [eth0 wg0] after hide-noise, got %v", names)
	}
}

func TestHelpToggle(t *testing.T) {
	m := NewModel()

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = model.(Model)
	if !m.showHelp {
		t.Fatal("? should open the help overlay")
	}

	// The overlay is longer than a terminal, so the scroll keys scroll it
	// rather than closing it — the same contract the detail overlay has.
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = model.(Model)
	if !m.showHelp {
		t.Fatal("a scroll key should scroll the help overlay, not close it")
	}

	// Any other key closes it again.
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = model.(Model)
	if m.showHelp {
		t.Fatal("a keypress should close the help overlay")
	}
}

func TestContainerListenerIsNotHostExposure(t *testing.T) {
	m := feed(t, eventsModel(), netnsPorts(), nil)

	// sshd, docker-proxy and the published nginx are exposed; the unpublished
	// redis is reachable only inside its namespace.
	exposed, _, _ := m.securityCounts()
	if exposed != 3 {
		t.Errorf("exposed listeners = %d, want 3 (unpublished container listener excluded)", exposed)
	}

	metrics := FormatMetrics(probe.NetSnapshot{}, probe.ConnInfo{Ports: netnsPorts()}, 0, false)
	for _, want := range []string{
		`ducknetview_listeners{exposure="exposed"} 3`,
		`ducknetview_listeners{exposure="netns"} 1`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics lack %s:\n%s", want, metrics)
		}
	}
}

func TestNetNSLimitationRecordedOnce(t *testing.T) {
	m := eventsModel()
	info := probe.ConnInfo{
		Ports: netnsPorts(),
		NetNS: probe.NetNSStatus{Seen: 2, Scanned: 2, Limited: "3 TCP connections in other namespaces have no byte counters — reading them needs root"},
	}
	now := time.Now()
	m.applyConnInfo(info, now)
	m.applyConnInfo(info, now.Add(2*time.Second))

	n := 0
	for _, e := range m.events {
		if e.Kind == "netns_limited" {
			n++
			if !strings.Contains(e.Subject, "root") {
				t.Errorf("note does not say why: %+v", e)
			}
		}
	}
	if n != 1 {
		t.Errorf("recorded the namespace note %d times, want 1", n)
	}
}

// The interface list's own quit binding returned tea.Quit straight past
// m.quit(), so esc on the Interfaces tab ended the session without saving.
func TestEscOnInterfacesDoesNotQuit(t *testing.T) {
	m := routesModel()
	m.activeTab = tabIfaces
	for _, k := range []string{"esc", "/"} {
		next, cmd := m.Update(keyMsg(k))
		if cmd != nil {
			if _, quit := cmd().(tea.QuitMsg); quit {
				t.Fatalf("%q on Interfaces quit the program", k)
			}
		}
		m = next.(Model)
	}
}

// The "N new" badge on the Ports tab should agree with the event log, which
// already ignores ephemeral UDP churn.
func TestNewListenerBadgeIgnoresEphemeralUDP(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabPorts
	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}

	m = feed(t, m, []probe.ListenPort{sshd}, nil)
	m = feed(t, m, []probe.ListenPort{sshd,
		{Proto: "udp", Local: "0.0.0.0:47572", PID: 3, Process: "firefox"}}, nil)

	if len(m.newPorts) != 0 {
		t.Errorf("new-listener set = %v, want the ephemeral UDP socket ignored", m.newPorts)
	}
	if strings.Contains(m.renderPortsText(), "new") {
		t.Errorf("ports subtitle claims new listeners:\n%s", m.renderPortsText())
	}

	// A real new service still registers.
	m = feed(t, m, []probe.ListenPort{sshd, {Proto: "tcp", Local: "0.0.0.0:8080", PID: 4, Process: "app"}}, nil)
	if len(m.newPorts) != 1 {
		t.Errorf("a new TCP listener should still be flagged, got %v", m.newPorts)
	}
}

// This round added two overlays, a tree view, a conditional column and a drill
// filter, all of which size themselves against the terminal. A key that
// panics only on an 80-column terminal is the kind of thing no targeted test
// catches, so every binding is pressed on every tab at several sizes.

func smokeModel() Model {
	m := NewModel()
	m.lastSnap = probe.NetSnapshot{
		Hostname: "smoke", Uptime: time.Hour, TakenAt: time.Now(),
		Ifaces: []probe.IfaceInfo{
			{Name: "eth0", IsUp: true, RxBps: 1000, TxBps: 2000, Addrs: []string{"10.0.0.2/24"}},
			{Name: "wg0", IsUp: true, Addrs: []string{"10.1.0.2/24"}},
			{Name: "lo", IsUp: true, Kind: probe.IfaceLoopback},
		},
	}
	m.ports = []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:8080", PID: 12, Process: "nginx", User: "www"},
		{Proto: "udp", Local: "127.0.0.1:53", PID: 13, Process: "resolved", User: "root"},
	}
	m.procs = []probe.ProcNet{
		{PID: 10, PPID: 1, Name: "firefox", ConnCount: 2, User: "vlad", Container: "abc123def456"},
		{PID: 11, PPID: 10, Name: "firefox", ConnCount: 1, User: "vlad"},
	}
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:4001", Remote: "8.8.8.8:443", State: "ESTABLISHED", PID: 10, Process: "firefox", RxBytes: 100, TxBytes: 200, RTT: time.Millisecond},
		{Proto: "tcp", Local: "10.1.0.2:4002", Remote: "1.1.1.1:443", State: "TIME_WAIT", PID: 11, Process: "firefox"},
		{Proto: "udp", Local: "10.0.0.2:4003", Remote: "192.168.1.1:53", State: "NONE", PID: 13, Process: "resolved"},
	}
	m.routes = []probe.Route{{Destination: "default", Gateway: "10.0.0.1", Iface: "eth0"}}
	m.neighbours = []probe.Neighbour{{IP: "fe80::1", MAC: "e0:1f:2b:51:1c:31", Iface: "eth0", IPv6: true, State: "stale"}}
	m.resolvers = []probe.Resolver{{Address: "127.0.0.53", Stub: true}}
	m.hasThroughput = true
	m.connSeen, m.portSeen = true, true
	m.events = []event{{At: time.Now(), Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:8080"}}
	m.trend.record(time.Now().Add(-time.Minute), 1000, 2000)
	m.trend.record(time.Now(), 3000, 4000)
	m.recordLatency([]probe.LatencySample{{Label: "gateway", Addr: "10.0.0.1:53", RTT: 2 * time.Millisecond, At: time.Now()}})
	return m
}

// smokeKeys is every binding the tool has, plus navigation.
var smokeKeys = []string{
	"?", ",", "1", "2", "3", "4", "5", "6", "7", "tab", "shift+tab",
	"up", "down", "pgup", "pgdown", "home", "end",
	"/", "esc", "ctrl+u", "\\", "n", "N",
	"s", "S", "t", "U", "A", "T", "F", "h", "r", "g", "z", "i",
	"p", "v", "f", "o", "u", "a", "w", "b", "B", "l", "d", "c",
	"enter", "y", "e", "x", "R", " ", "+", "-", "ctrl+e",
}

func TestEveryKeyOnEveryTabAtEverySize(t *testing.T) {
	sizes := [][2]int{{80, 24}, {100, 30}, {160, 45}, {220, 60}}

	for _, size := range sizes {
		for startTab := tab(0); startTab < tabCount; startTab++ {
			for _, key := range smokeKeys {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("%dx%d tab %v key %q panicked: %v",
								size[0], size[1], startTab, key, r)
						}
					}()

					m := smokeModel()
					sized, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
					m = sized.(Model)
					m.activeTab = startTab

					out, _ := m.Update(keyMsg(key))
					got := out.(Model)
					// Rendering is where a bad width actually bites.
					if v := got.View(); v == "" {
						t.Fatalf("%dx%d tab %v key %q rendered nothing", size[0], size[1], startTab, key)
					}
				}()
			}
		}
	}
}

// The overlays size themselves independently of the tables, and a short
// terminal is where a fixed-height box overflows.
func TestOverlaysRenderAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{80, 10}, {80, 24}, {200, 60}} {
		m := smokeModel()
		out, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = out.(Model)

		for name, render := range map[string]func() string{
			"help":     m.renderHelp,
			"settings": func() string { m.openSettings(); return m.renderSettings() },
			"overview": m.viewOverview,
			"routes":   m.viewRoutes,
		} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%dx%d %s panicked: %v", size[0], size[1], name, r)
					}
				}()
				txt := render()
				if strings.TrimSpace(stripANSI(txt)) == "" {
					t.Errorf("%dx%d %s rendered nothing", size[0], size[1], name)
				}
			}()
		}
	}
}

// A drill, a tree and an owner column all change the layout; rendering with
// each on is the combination most likely to overflow a narrow terminal.
func TestNarrowTerminalWithEverythingOn(t *testing.T) {
	m := smokeModel()
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(Model)
	m.procTree = true
	m.activeTab = tabProcs
	if _, ok := m.drillInto(); !ok {
		t.Fatal("could not drill from the process list")
	}

	for _, line := range strings.Split(stripANSI(m.View()), "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("line of %d runes on an 80-column terminal: %q", len([]rune(line)), line)
		}
	}
}

// applyConnInfo is what a real poll goes through, so the counters have to be
// driven from there rather than set by hand anywhere else.
func TestPollCountersAdvanceOnRealPolls(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 45

	before := m.polls
	m.applyConnInfo(probe.ConnInfo{
		Ports: []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}},
		Conns: []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:1", Remote: "8.8.8.8:443"}},
	}, time.Now())

	if m.polls != before+1 {
		t.Errorf("polls = %d, want %d", m.polls, before+1)
	}
	if m.lastPollAt.IsZero() {
		t.Error("lastPollAt was not set by a poll")
	}
}

// The space bar has no printable form; showing an empty cell would read as a
// missing binding.
func TestHelpNamesTheSpaceBar(t *testing.T) {
	m := NewModel()
	m.w, m.h = 200, 60
	if !strings.Contains(stripANSI(m.helpText()), "space") {
		t.Error("the help overlay does not name the space bar")
	}
	if got := spaceLabel("Z"); got != "Z" {
		t.Errorf("spaceLabel(Z) = %q", got)
	}
}

func TestHelpShowsBoundKeys(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 50
	m.keymap = newKeymap(map[string]string{"closed": "Z"})
	m.openHelp()
	txt := stripANSI(m.helpText())
	if !strings.Contains(txt, "Z ") {
		t.Error("the help overlay does not show the rebound key")
	}
	if !strings.Contains(txt, "your own bindings") {
		t.Error("the help overlay does not say the bindings are customised")
	}
}
