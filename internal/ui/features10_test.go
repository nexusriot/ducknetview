package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

// The command line is what separates forty rows of the same name.
func TestProcessSearchCoversTheCommandLine(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 48
	m.procs = []probe.ProcNet{
		{PID: 1, Name: "python3", Cmdline: "python3 /opt/billing/worker.py"},
		{PID: 2, Name: "python3", Cmdline: "python3 /usr/bin/ansible"},
	}

	m.procsQuery = "billing"
	rows := m.filteredProcs()
	if len(rows) != 1 || rows[0].PID != 1 {
		t.Fatalf("searching the command line matched %d rows: %+v", len(rows), rows)
	}

	// And the name still matches both.
	m.procsQuery = "python3"
	if rows := m.filteredProcs(); len(rows) != 2 {
		t.Errorf("the name search should still match both, got %d", len(rows))
	}
}

// A command line can carry a password, and a snapshot gets piped around.
func TestCommandLineIsNotSerialised(t *testing.T) {
	m := NewModel()
	m.procs = []probe.ProcNet{{PID: 1, Name: "mysql", Cmdline: "mysql -psecret"}}
	snap := Snapshot{Hostname: "h", Processes: m.procs}

	b, err := marshalSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") {
		t.Errorf("the command line reached the snapshot:\n%s", b)
	}
}

// Unprivileged, another user's sockets have no PID. Saying nothing makes the
// tool look wrong rather than restricted.
func TestUnattributedSocketsAreReported(t *testing.T) {
	restore := isPrivileged
	isPrivileged = func() bool { return false }
	defer func() { isPrivileged = restore }()

	m := NewModel()
	m.w, m.h = 160, 48
	m.procsVP.Width = 120
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:1", Remote: "1.1.1.1:443", PID: 0},
		{Proto: "tcp", Local: "10.0.0.2:2", Remote: "2.2.2.2:443", PID: 44},
	}
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 0}}

	if n := m.unattributedSockets(); n != 2 {
		t.Errorf("counted %d unattributed sockets, want 2", n)
	}
	if head := stripANSI(m.renderProcsText()); !strings.Contains(head, "2 sockets owned by another user") {
		t.Errorf("the Processes tab says nothing about it:\n%s", strings.SplitN(head, "\n", 2)[0])
	}

	// Root can see everything, so there is nothing to suggest.
	isPrivileged = func() bool { return true }
	if n := m.unattributedSockets(); n != 0 {
		t.Errorf("root should have nothing unattributed, got %d", n)
	}
	if m.attributionNote() != "" {
		t.Error("root should not be told to run as root")
	}
}

// The note is worth saying once, not once per poll.
func TestAttributionNoteIsRecordedOnce(t *testing.T) {
	restore := isPrivileged
	isPrivileged = func() bool { return false }
	defer func() { isPrivileged = restore }()

	m := NewModel()
	m.w, m.h = 160, 48
	info := probe.ConnInfo{Conns: []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:1", Remote: "1.1.1.1:443"}}}

	now := time.Now()
	m.applyConnInfo(info, now)
	m.applyConnInfo(info, now.Add(2*time.Second))

	n := 0
	for _, e := range m.events {
		if e.Kind == "attribution_limited" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("recorded the attribution note %d times, want 1", n)
	}
}

// A view is the whole arrangement under a name, and it has to come back.
func TestSavedViewRoundTrips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, `{}`)

	m := NewModel().WithConfig()
	m.w, m.h = 160, 48
	m.activeTab = tabConns
	m.connsQuery = "443"
	m.protoFilter = 1
	m.publicOnly = true
	m.connsGroup = true
	m.showTotals = true
	m.connsSortCol = 3
	m.connsSortDesc = true
	m.ifaceFilter = "wg0"

	if msg, ok := m.saveView("vpn"); !ok {
		t.Fatalf("saving failed: %s", msg)
	}

	// Change everything, then recall.
	m.activeTab = tabOverview
	m.connsQuery = ""
	m.protoFilter, m.publicOnly, m.connsGroup, m.showTotals = 0, false, false, false
	m.connsSortCol, m.connsSortDesc, m.ifaceFilter = 0, false, ""

	m2, ok := m.WithView("VPN") // names are matched case-insensitively
	if !ok {
		t.Fatal("the saved view was not found")
	}
	switch {
	case m2.activeTab != tabConns:
		t.Error("the tab did not come back")
	case m2.connsQuery != "443":
		t.Error("the search did not come back")
	case m2.protoFilter != 1 || !m2.publicOnly:
		t.Error("the quick filters did not come back")
	case !m2.connsGroup || !m2.showTotals:
		t.Error("the view toggles did not come back")
	case m2.connsSortCol != 3 || !m2.connsSortDesc:
		t.Error("the sort did not come back")
	case m2.ifaceFilter != "wg0":
		t.Error("the interface filter did not come back")
	}

	// And it survives a restart, because it is in the config.
	if names := ViewNames(); len(names) != 1 || names[0] != "vpn" {
		t.Errorf("the view was not persisted: %v", names)
	}
	if _, ok := NewModel().WithConfig().WithView("vpn"); !ok {
		t.Error("a fresh session could not find the saved view")
	}
}

// A view is hand-editable in the config, so every index has to be clamped.
func TestSavedViewFromAHandEditedConfigIsClamped(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, `{"views":[
		{"name":"broken","tab":99,"conns_sort_col":999,"proto_filter":7},
		{"name":"","tab":0},
		{"name":"broken","tab":1}
	]}`)

	m := NewModel().WithConfig()
	if len(m.views) != 1 {
		t.Fatalf("the unnamed and duplicate views should have been dropped: %+v", m.views)
	}
	m2, ok := m.WithView("broken")
	if !ok {
		t.Fatal("the view should still be usable")
	}
	if int(m2.activeTab) > tabCount-1 || m2.connsSortCol >= len(connsSortCols) || m2.protoFilter > 2 {
		t.Errorf("an out-of-range view was applied unclamped: tab=%d sort=%d proto=%d",
			m2.activeTab, m2.connsSortCol, m2.protoFilter)
	}
}

// The overlay is the only way most people will meet the feature.
func TestViewsOverlayKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, `{}`)

	m := NewModel().WithConfig()
	m.w, m.h = 160, 48
	m.activeTab = tabPorts

	model, _ := m.Update(key("V"))
	m = model.(Model)
	if !m.showViews {
		t.Fatal("V should open the views overlay")
	}
	if !strings.Contains(stripANSI(m.View()), "No views saved yet") {
		t.Error("an empty overlay should explain itself")
	}

	// n names the current view; the name goes to the text input.
	model, _ = m.Update(key("n"))
	m = model.(Model)
	if !m.viewsNaming {
		t.Fatal("n should start naming")
	}
	for _, r := range "ports" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(Model)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if len(m.views) != 1 || m.views[0].Name != "ports" {
		t.Fatalf("the view was not saved: %+v", m.views)
	}

	// enter applies it and closes the overlay.
	m.activeTab = tabOverview
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if m.showViews {
		t.Error("applying a view should close the overlay")
	}
	if m.activeTab != tabPorts {
		t.Errorf("the view was not applied: tab %v", m.activeTab)
	}

	// d deletes.
	model, _ = m.Update(key("V"))
	m = model.(Model)
	model, _ = m.Update(key("d"))
	m = model.(Model)
	if len(m.views) != 0 {
		t.Errorf("d should have deleted the view: %+v", m.views)
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

// The same overflow, in the overlay that can grow without bound.
func TestViewsOverlayFitsTheTerminal(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 24
	for i := 0; i < maxSavedViews; i++ {
		m.views = append(m.views, savedView{Name: "view" + string(rune('a'+i%26)) + string(rune('0'+i/26))})
	}
	m.openViews()
	m.viewsCursor = len(m.views) - 1

	drawn := strings.Count(m.renderViews(), "\n") + 1
	if drawn > m.h {
		t.Errorf("%d saved views draw %d rows on a %d-row terminal", len(m.views), drawn, m.h)
	}
	// Whichever view is highlighted has to be one of the ones on screen.
	if !strings.Contains(stripANSI(m.renderViews()), m.views[m.viewsCursor].Name) {
		t.Error("the highlighted view is not on screen")
	}
}
