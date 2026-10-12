package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// --- drill-down ----------------------------------------------------------

func drillModel(t *testing.T) Model {
	t.Helper()
	m := NewModel()
	m.w, m.h = 160, 45
	m.connsVP.Width, m.connsVP.Height = 140, 30
	m.procsVP.Width, m.procsVP.Height = 140, 30
	m.portsVP.Width, m.portsVP.Height = 140, 30

	m.procs = []probe.ProcNet{
		{PID: 10, Name: "firefox", ConnCount: 2},
		{PID: 11, Name: "syncthing", ConnCount: 1},
	}
	m.ports = []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:8080", PID: 12, Process: "nginx"},
		{Proto: "tcp", Local: "0.0.0.0:22", PID: 13, Process: "sshd"},
	}
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:4001", Remote: "1.1.1.1:443", State: "ESTABLISHED", PID: 10, Process: "firefox"},
		{Proto: "tcp", Local: "10.0.0.2:4002", Remote: "8.8.8.8:443", State: "ESTABLISHED", PID: 10, Process: "firefox"},
		{Proto: "tcp", Local: "10.0.0.2:4003", Remote: "9.9.9.9:443", State: "ESTABLISHED", PID: 11, Process: "syncthing"},
		{Proto: "tcp", Local: "10.0.0.2:8080", Remote: "203.0.113.7:51000", State: "ESTABLISHED", PID: 12, Process: "nginx"},
	}
	return m
}

func TestDrillFromProcess(t *testing.T) {
	m := drillModel(t)
	m.activeTab = tabProcs
	m.procsCursor = 0 // firefox

	status, ok := m.drillInto()
	if !ok {
		t.Fatalf("drillInto failed: %s", status)
	}
	if m.activeTab != tabConns {
		t.Errorf("did not switch to the Connections tab, on %v", m.activeTab)
	}

	rows := m.filteredConns()
	if len(rows) != 2 {
		t.Fatalf("drill showed %d connections, want firefox's 2", len(rows))
	}
	for _, c := range rows {
		if c.PID != 10 {
			t.Errorf("drill included pid %d", c.PID)
		}
	}
	if !strings.Contains(m.filterLabel(), "firefox") {
		t.Errorf("filter label %q does not name the drill", m.filterLabel())
	}
	if !m.anyFilterActive() {
		t.Error("anyFilterActive = false while a drill is in force, so the subtitle would hide it")
	}

	// Pressing it again goes back where it came from.
	status, ok = m.drillInto()
	if !ok {
		t.Fatalf("going back failed: %s", status)
	}
	if m.activeTab != tabProcs {
		t.Errorf("did not return to Processes, on %v", m.activeTab)
	}
	if m.drill != nil {
		t.Error("drill not cleared")
	}
	if len(m.filteredConns()) != 4 {
		t.Error("connections are still filtered after going back")
	}
}

// Folding by name gives a row that stands for several PIDs; following it has
// to follow all of them.
func TestDrillFromAggregatedProcessFollowsEveryPID(t *testing.T) {
	m := drillModel(t)
	m.procs = []probe.ProcNet{
		{PID: 10, Name: "firefox", ConnCount: 1},
		{PID: 11, Name: "firefox", ConnCount: 1},
	}
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:4001", Remote: "1.1.1.1:443", PID: 10, Process: "firefox"},
		{Proto: "tcp", Local: "10.0.0.2:4002", Remote: "8.8.8.8:443", PID: 11, Process: "firefox"},
		{Proto: "tcp", Local: "10.0.0.2:4003", Remote: "9.9.9.9:443", PID: 12, Process: "other"},
	}
	m.aggregateProcs = true
	m.activeTab = tabProcs
	m.procsCursor = 0

	if _, ok := m.drillInto(); !ok {
		t.Fatal("drillInto failed on an aggregated row")
	}
	if n := len(m.filteredConns()); n != 2 {
		t.Errorf("drill on a folded row showed %d connections, want both PIDs' 2", n)
	}
}

func TestDrillFromListener(t *testing.T) {
	m := drillModel(t)
	m.activeTab = tabPorts
	// The table is sorted, so the row index is found rather than assumed.
	for i, p := range m.filteredPorts() {
		if p.Local == "0.0.0.0:8080" {
			m.portsCursor = i
		}
	}

	if status, ok := m.drillInto(); !ok {
		t.Fatalf("drillInto failed: %s", status)
	}
	rows := m.filteredConns()
	if len(rows) != 1 || rows[0].Local != "10.0.0.2:8080" {
		t.Fatalf("drill on a listener showed %+v, want the one connection on port 8080", rows)
	}
	if !strings.Contains(m.filterLabel(), "8080") {
		t.Errorf("filter label %q does not name the listener", m.filterLabel())
	}
}

// A drill is a filter on the live table; landing inside the closed-connection
// history would answer a different question than the one asked.
func TestDrillLeavesClosedView(t *testing.T) {
	m := drillModel(t)
	m.showClosed = true
	m.activeTab = tabProcs
	m.procsCursor = 0

	if _, ok := m.drillInto(); !ok {
		t.Fatal("drillInto failed")
	}
	if m.showClosed {
		t.Error("drill left the closed-connections view on")
	}
}

func TestDrillReportsWhenThereIsNothingToFollow(t *testing.T) {
	m := drillModel(t)
	m.activeTab = tabConns
	status, ok := m.drillInto()
	if ok {
		t.Error("drillInto succeeded on the Connections tab with no drill in force")
	}
	if !strings.Contains(status, "Nothing to go back to") {
		t.Errorf("status = %q", status)
	}
}

// The key has to reach the drill through the ordinary key path.
func TestDrillKeyBinding(t *testing.T) {
	m := drillModel(t)
	m.activeTab = tabProcs
	m.procsCursor = 1 // syncthing

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
	got := out.(Model)
	if got.activeTab != tabConns || got.drill == nil {
		t.Fatal("F did not follow the highlighted process")
	}
	if n := len(got.filteredConns()); n != 1 {
		t.Errorf("showed %d connections, want syncthing's 1", n)
	}
}

// --- settings overlay ----------------------------------------------------

func TestSettingsEditsAThreshold(t *testing.T) {
	m := NewModel()
	m.w, m.h = 120, 40
	m.openSettings()
	if !m.showSettings {
		t.Fatal("overlay did not open")
	}

	// Find the RTT row and edit it.
	rows := settingsRows()
	for i, r := range rows {
		if r.key == "alert_rtt_ms" {
			m.settingsCursor = i
		}
	}
	if _, handled := m.settingsKey("enter"); !handled {
		t.Fatal("enter did not start editing")
	}
	if !m.settingsEditing {
		t.Fatal("settingsEditing = false after enter")
	}

	m.settingsInput.SetValue("250")
	status, _ := m.settingsKey("enter")
	if m.alertRules.rttMs != 250 {
		t.Errorf("rttMs = %v, want 250", m.alertRules.rttMs)
	}
	if !strings.Contains(status, "250") {
		t.Errorf("status = %q", status)
	}
	if m.settingsEditing {
		t.Error("still editing after enter")
	}

	// "off" disables it again.
	m.settingsKey("enter")
	m.settingsInput.SetValue("off")
	m.settingsKey("enter")
	if m.alertRules.rttMs != 0 {
		t.Errorf("rttMs = %v after 'off', want 0", m.alertRules.rttMs)
	}
}

func TestSettingsRejectsRubbish(t *testing.T) {
	m := NewModel()
	m.openSettings()
	for i, r := range settingsRows() {
		if r.key == "alert_fanout_hosts" {
			m.settingsCursor = i
		}
	}
	m.settingsKey("enter")
	m.settingsInput.SetValue("twenty")
	status, _ := m.settingsKey("enter")
	if m.alertRules.fanoutHosts != 0 {
		t.Errorf("fanoutHosts = %d, want it left alone", m.alertRules.fanoutHosts)
	}
	if !strings.Contains(status, "Not a number") {
		t.Errorf("status = %q, want an explanation", status)
	}

	// A negative threshold would fire on every poll.
	m.settingsKey("enter")
	m.settingsInput.SetValue("-5")
	m.settingsKey("enter")
	if m.alertRules.fanoutHosts != 0 {
		t.Errorf("fanoutHosts = %d, a negative threshold was accepted", m.alertRules.fanoutHosts)
	}
}

func TestSettingsLatencyTargetsAddAndRemove(t *testing.T) {
	m := NewModel()
	m.openSettings()
	for i, r := range settingsRows() {
		if r.key == "latency_targets" {
			m.settingsCursor = i
		}
	}

	add := func(v string) string {
		m.settingsKey("enter")
		m.settingsInput.SetValue(v)
		status, _ := m.settingsKey("enter")
		return status
	}

	add("1.1.1.1:53")
	if len(m.latencyExtra) != 1 || m.latencyExtra[0] != "1.1.1.1:53" {
		t.Fatalf("latencyExtra = %v", m.latencyExtra)
	}
	// The same value again removes it, which is how the list rows work.
	add("1.1.1.1:53")
	if len(m.latencyExtra) != 0 {
		t.Errorf("latencyExtra = %v, want the entry removed", m.latencyExtra)
	}
	// A target with no port is silently never measured, so it is refused here.
	if status := add("example.com"); !strings.Contains(status, "host:port") {
		t.Errorf("status = %q, want an explanation of the required shape", status)
	}
	if len(m.latencyExtra) != 0 {
		t.Errorf("a portless target was accepted: %v", m.latencyExtra)
	}
}

func TestSettingsHiddenColumns(t *testing.T) {
	m := NewModel()
	m.openSettings()
	for i, r := range settingsRows() {
		if r.key == "hidden_columns" {
			m.settingsCursor = i
		}
	}
	set := func(v string) string {
		m.settingsKey("enter")
		m.settingsInput.SetValue(v)
		status, _ := m.settingsKey("enter")
		return status
	}

	set("svc")
	if !m.hiddenCols["svc"] {
		t.Error("svc was not hidden")
	}
	set("svc")
	if m.hiddenCols["svc"] {
		t.Error("svc was not shown again")
	}
	// Hiding a column that does not exist would silently do nothing.
	if status := set("nonsense"); !strings.Contains(status, "Unknown column") {
		t.Errorf("status = %q", status)
	}
}

// The overlay must own the keyboard while it is open, or a key falls through
// to a table nobody can see.
func TestSettingsOverlayCapturesKeys(t *testing.T) {
	m := NewModel()
	m.w, m.h = 120, 40
	m.activeTab = tabConns
	m.openSettings()

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	got := out.(Model)
	if got.showClosed {
		t.Error("a key leaked through the settings overlay to the table underneath")
	}

	// esc closes it.
	out, _ = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if out.(Model).showSettings {
		t.Error("esc did not close the overlay")
	}
}

func TestSettingsRendersEveryRow(t *testing.T) {
	m := NewModel()
	m.w, m.h = 120, 45
	m.openSettings()
	txt := stripANSI(m.renderSettings())
	for _, r := range settingsRows() {
		if !strings.Contains(txt, r.title) {
			t.Errorf("settings overlay missing row %q", r.title)
		}
	}
}

// The `,` key opens it through the ordinary key path.
func TestSettingsKeyBinding(t *testing.T) {
	m := NewModel()
	m.w, m.h = 120, 40
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(",")})
	if !out.(Model).showSettings {
		t.Error(", did not open the settings overlay")
	}
}

// --- themes --------------------------------------------------------------

func TestThemeCycleAndApply(t *testing.T) {
	defer applyTheme(defaultTheme)

	names := ThemeNames()
	if len(names) < 2 {
		t.Fatal("there is only one theme, so cycling is meaningless")
	}
	seen := map[string]bool{}
	cur := names[0]
	for range names {
		seen[cur] = true
		cur = nextTheme(cur)
	}
	if len(seen) != len(names) {
		t.Errorf("cycling visited %d of %d themes", len(seen), len(names))
	}
	if cur != names[0] {
		t.Errorf("cycling did not return to the start: %q", cur)
	}

	// An unknown name must fall back rather than render everything unstyled.
	applyTheme("no-such-theme")
	if !validTheme("default") || validTheme("no-such-theme") {
		t.Error("validTheme is wrong")
	}
}

func TestThemeChangesStyles(t *testing.T) {
	withColor(t)
	defer applyTheme(defaultTheme)

	applyTheme("default")
	before := okStyle.Render("x")
	applyTheme("contrast")
	after := okStyle.Render("x")
	if before == after {
		t.Error("switching theme did not change the rendered styling")
	}
}

func TestThemePersists(t *testing.T) {
	defer applyTheme(defaultTheme)

	m := NewModel()
	m.theme = "contrast"
	m.saveConfig()

	loaded := NewModel().WithConfig()
	if loaded.theme != "contrast" {
		t.Errorf("theme = %q after a reload, want contrast", loaded.theme)
	}
}

// --- remappable keys -----------------------------------------------------

func TestKeymapRebinds(t *testing.T) {
	km := newKeymap(map[string]string{"closed": "Z"})

	if got := km.translate("Z"); got != "z" {
		t.Errorf("translate(Z) = %q, want z", got)
	}
	// The default key must stop doing its old job, or one press does two
	// things.
	if got := km.translate("z"); got == "z" {
		t.Error("the default key still fires its action after being rebound")
	}
	if got := km.keyFor("closed"); got != "Z" {
		t.Errorf("keyFor(closed) = %q", got)
	}
	// Unrelated keys pass through untouched.
	if got := km.translate("g"); got != "g" {
		t.Errorf("translate(g) = %q, want g", got)
	}
}

func TestKeymapRefusesDangerousBindings(t *testing.T) {
	km := newKeymap(map[string]string{
		"closed":     "ctrl+c", // would leave no way out
		"group":      "tab",    // would break navigation
		"quit":       "up",     // ditto
		"not-a-verb": "Q",      // unknown action
	})
	for _, reserved := range []string{"ctrl+c", "tab", "up"} {
		if got := km.translate(reserved); got != reserved {
			t.Errorf("reserved key %q was rebound to %q", reserved, got)
		}
	}
	if len(km.overrides()) != 0 {
		t.Errorf("overrides = %v, want every bad binding dropped", km.overrides())
	}
	// Everything still works on its default key.
	if got := km.translate("z"); got != "z" {
		t.Errorf("translate(z) = %q after the bad config was dropped", got)
	}
}

// Two actions bound to one key would make a press ambiguous.
func TestKeymapRefusesDuplicates(t *testing.T) {
	km := newKeymap(map[string]string{"closed": "Z", "group": "Z"})
	if n := len(km.overrides()); n != 1 {
		t.Errorf("overrides = %v, want only one of the two", km.overrides())
	}
}

func TestKeymapDrivesTheKeyHandler(t *testing.T) {
	m := drillModel(t)
	m.activeTab = tabConns
	m.keymap = newKeymap(map[string]string{"closed": "Z"})

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Z")})
	if !out.(Model).showClosed {
		t.Error("the rebound key did not reach the handler")
	}

	// And the old key no longer does it.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if out.(Model).showClosed {
		t.Error("the old key still toggles the view it was unbound from")
	}
}

func TestHelpShowsBoundKeys(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 50
	m.keymap = newKeymap(map[string]string{"closed": "Z"})
	txt := stripANSI(m.renderHelp())
	if !strings.Contains(txt, "Z ") {
		t.Error("the help overlay does not show the rebound key")
	}
	if !strings.Contains(txt, "your own bindings") {
		t.Error("the help overlay does not say the bindings are customised")
	}
}

// --- process tree --------------------------------------------------------

func TestArrangeTree(t *testing.T) {
	rows := []procRow{
		{ProcNet: probe.ProcNet{PID: 100, PPID: 1, Name: "systemd-child"}},
		{ProcNet: probe.ProcNet{PID: 200, PPID: 100, Name: "worker-a"}},
		{ProcNet: probe.ProcNet{PID: 201, PPID: 100, Name: "worker-b"}},
		{ProcNet: probe.ProcNet{PID: 300, PPID: 999, Name: "orphan"}}, // parent not on screen
	}
	out := arrangeTree(rows)
	if len(out) != len(rows) {
		t.Fatalf("tree has %d rows, want %d — nothing may be dropped", len(out), len(rows))
	}

	order := make([]int32, len(out))
	depth := map[int32]int{}
	for i, r := range out {
		order[i] = r.PID
		depth[r.PID] = r.depth
	}
	if order[0] != 100 || order[1] != 200 || order[2] != 201 {
		t.Errorf("order = %v, want the parent followed by its children", order)
	}
	if depth[100] != 0 || depth[200] != 1 || depth[201] != 1 {
		t.Errorf("depths = %v", depth)
	}
	// A process whose parent holds no sockets is a root, not a hidden row.
	if depth[300] != 0 {
		t.Errorf("orphan depth = %d, want 0", depth[300])
	}
}

// /proc has been known to report odd parentage; a cycle must not hang the
// renderer or lose rows.
func TestArrangeTreeSurvivesCycles(t *testing.T) {
	rows := []procRow{
		{ProcNet: probe.ProcNet{PID: 1, PPID: 2, Name: "a"}},
		{ProcNet: probe.ProcNet{PID: 2, PPID: 1, Name: "b"}},
		{ProcNet: probe.ProcNet{PID: 3, PPID: 3, Name: "self-parent"}},
	}
	done := make(chan []procRow, 1)
	go func() { done <- arrangeTree(rows) }()
	select {
	case out := <-done:
		if len(out) != 3 {
			t.Errorf("cycle lost rows: %d of 3", len(out))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("arrangeTree did not terminate on a cycle")
	}
}

func TestProcTreeRendersIndented(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 45
	m.procsVP.Width, m.procsVP.Height = 140, 30
	m.procs = []probe.ProcNet{
		{PID: 100, PPID: 1, Name: "supervisor", ConnCount: 1},
		{PID: 200, PPID: 100, Name: "worker", ConnCount: 1},
	}
	m.procTree = true

	txt := stripANSI(m.renderProcsText())
	if !strings.Contains(txt, "└ worker") {
		t.Errorf("the child is not indented under its parent:\n%s", txt)
	}
	rows := m.filteredProcs()
	if len(rows) != 2 || rows[0].PID != 100 {
		t.Errorf("rows = %+v, want the parent first", rows)
	}
}

// Folding by name discards the parent relationships, so the two views cannot
// both be on.
func TestProcTreeAndAggregateAreExclusive(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 45
	m.procsVP.Width = 140
	m.activeTab = tabProcs
	m.aggregateProcs = true

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	got := out.(Model)
	if !got.procTree || got.aggregateProcs {
		t.Errorf("procTree=%v aggregateProcs=%v, want tree on and grouping off", got.procTree, got.aggregateProcs)
	}

	out, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("A")})
	got = out.(Model)
	if got.procTree || !got.aggregateProcs {
		t.Errorf("procTree=%v aggregateProcs=%v, want grouping on and tree off", got.procTree, got.aggregateProcs)
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

// --- network ownership ---------------------------------------------------

func ownerModel(t *testing.T) Model {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ip2asn.tsv")
	body := "1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET\n" +
		"8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewModel()
	m.w, m.h = 170, 45
	m.connsVP.Width, m.connsVP.Height = 150, 30
	m.asnPath = p
	m.loadASNDB()
	if m.asnErr != nil {
		t.Fatalf("loadASNDB: %v", m.asnErr)
	}
	return m
}

func TestOwnerColumnAppearsOnlyWithADatabase(t *testing.T) {
	plain := NewModel()
	plain.w, plain.h = 170, 45
	plain.connsVP.Width = 150
	for _, c := range plain.connsColumns() {
		if c.key == "owner" {
			t.Fatal("the OWNER column appears with no database loaded")
		}
	}

	m := ownerModel(t)
	found := false
	for _, c := range m.connsColumns() {
		if c.key == "owner" {
			found = true
		}
	}
	if !found {
		t.Fatal("the OWNER column is missing although a database is loaded")
	}
}

func TestOwnerLookup(t *testing.T) {
	m := ownerModel(t)
	if got := m.ownerOf("8.8.8.8:443"); got != "GOOGLE" {
		t.Errorf("ownerOf = %q, want GOOGLE", got)
	}
	// A private address has no owner, and must not borrow a neighbour's.
	if got := m.ownerOf("192.168.1.5:443"); got != "" {
		t.Errorf("ownerOf(private) = %q, want empty", got)
	}
	if got := m.ownerDetail("8.8.8.8:443"); !strings.Contains(got, "AS15169") || !strings.Contains(got, "US") {
		t.Errorf("ownerDetail = %q", got)
	}
}

func TestOwnerColumnRenders(t *testing.T) {
	m := ownerModel(t)
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:4001", Remote: "8.8.8.8:443", State: "ESTABLISHED", PID: 1, Process: "x"},
		{Proto: "tcp", Local: "10.0.0.2:4002", Remote: "192.168.1.5:443", State: "ESTABLISHED", PID: 2, Process: "y"},
	}
	txt := stripANSI(m.renderConnsText())
	if !strings.Contains(txt, "OWNER") {
		t.Errorf("header has no OWNER column:\n%s", txt)
	}
	if !strings.Contains(txt, "GOOGLE") {
		t.Errorf("the owner is not rendered:\n%s", txt)
	}
}

// A broken or missing database must not take the session with it.
func TestOwnerDatabaseFailureIsNotFatal(t *testing.T) {
	m := NewModel()
	m.asnPath = filepath.Join(t.TempDir(), "missing.tsv")
	m.loadASNDB()
	if m.asnErr == nil {
		t.Error("a missing database was not reported")
	}
	if m.asnDB.Len() != 0 {
		t.Error("a failed load left a database behind")
	}
	if got := m.ownerOf("8.8.8.8:443"); got != "" {
		t.Errorf("ownerOf = %q with no database", got)
	}
}

func TestOwnerSort(t *testing.T) {
	m := ownerModel(t)
	rows := []probe.Conn{
		{Remote: "8.8.8.8:443"},   // GOOGLE
		{Remote: "1.0.0.1:443"},   // CLOUDFLARENET
		{Remote: "192.168.1.1:1"}, // no owner
	}
	for i, col := range connsSortCols {
		if col == "owner" {
			m.connsSortCol = i
		}
	}
	m.sortConns(rows)
	if m.ownerOf(rows[0].Remote) != "" {
		t.Errorf("sorted = %v, want the unowned row first ascending", rows)
	}
	if m.ownerOf(rows[1].Remote) != "CLOUDFLARENET" || m.ownerOf(rows[2].Remote) != "GOOGLE" {
		t.Errorf("owners out of order: %v", []string{
			m.ownerOf(rows[1].Remote), m.ownerOf(rows[2].Remote)})
	}
}

// --- 24h trend -----------------------------------------------------------

func TestTrendRecordsPerMinutePeak(t *testing.T) {
	var h trendHistory
	base := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)

	h.record(base, 100, 10)
	h.record(base.Add(20*time.Second), 500, 5) // same minute, higher peak
	h.record(base.Add(time.Minute), 50, 50)

	if len(h.Points) != 2 {
		t.Fatalf("%d points, want 2 minutes", len(h.Points))
	}
	if h.Points[0].RxPeak != 500 {
		t.Errorf("RxPeak = %v, want the minute's peak (500), not the latest", h.Points[0].RxPeak)
	}
	if h.Points[0].TxPeak != 10 {
		t.Errorf("TxPeak = %v, want 10", h.Points[0].TxPeak)
	}
}

func TestTrendTrimsToWindow(t *testing.T) {
	var h trendHistory
	now := time.Now()
	h.record(now.Add(-48*time.Hour), 1, 1)
	h.record(now.Add(-30*time.Hour), 2, 2)
	h.record(now, 3, 3)

	if len(h.Points) != 1 {
		t.Fatalf("%d points, want only the one inside the 24h window", len(h.Points))
	}
	if h.Points[0].RxPeak != 3 {
		t.Errorf("kept the wrong point: %+v", h.Points[0])
	}
}

// A gap must render as a gap, not be compressed away — otherwise a restart
// silently rewrites the time axis.
func TestTrendSeriesFillsGaps(t *testing.T) {
	var h trendHistory
	now := time.Now()
	h.record(now.Add(-10*time.Minute), 100, 100)
	h.record(now, 200, 200)

	rx, tx := h.series(now, 11)
	if len(rx) != 11 || len(tx) != 11 {
		t.Fatalf("series lengths = %d/%d, want 11", len(rx), len(tx))
	}
	if rx[0] != 100 {
		t.Errorf("rx[0] = %v, want the ten-minutes-ago sample", rx[0])
	}
	if rx[len(rx)-1] != 200 {
		t.Errorf("rx[last] = %v, want the latest sample", rx[len(rx)-1])
	}
	for i := 1; i < len(rx)-1; i++ {
		if rx[i] != 0 {
			t.Errorf("rx[%d] = %v, want a zero-filled gap", i, rx[i])
		}
	}
}

func TestTrendSurvivesARestart(t *testing.T) {
	var h trendHistory
	now := time.Now()
	h.record(now.Add(-2*time.Minute), 1234, 5678)
	h.record(now, 10, 20)
	h.save()

	loaded := loadTrend()
	if len(loaded.Points) != 2 {
		t.Fatalf("%d points after a reload, want 2", len(loaded.Points))
	}
	if loaded.Points[0].RxPeak != 1234 || loaded.Points[0].TxPeak != 5678 {
		t.Errorf("point did not survive: %+v", loaded.Points[0])
	}
}

// A snapshot or a remote session describes another machine; writing its
// numbers into this host's history would corrupt the record silently.
func TestTrendIgnoresForeignData(t *testing.T) {
	m := NewModel()
	m.snapshotOnly = true
	m.recordTrend(1e9, 1e9, time.Now())
	if len(m.trend.Points) != 0 {
		t.Error("a snapshot's throughput was written into this host's history")
	}
}

func TestTrendPanelNeedsHistory(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 45
	if m.renderTrend(120) != "" {
		t.Error("the trend panel rendered with no history")
	}

	now := time.Now()
	m.trend.record(now.Add(-time.Minute), 1000, 2000)
	m.trend.record(now, 3000, 4000)
	txt := stripANSI(m.renderTrend(120))
	if !strings.Contains(txt, "RX") || !strings.Contains(txt, "TX") {
		t.Errorf("trend panel missing its series:\n%s", txt)
	}
	if !strings.Contains(txt, "peak") {
		t.Errorf("trend panel does not state the peak:\n%s", txt)
	}
}

// --- column key catalogue ------------------------------------------------

// AllColumnKeys drives the settings validation and the documentation; if it
// drifts from what the renderers declare, hiding a real column silently fails.
func TestAllColumnKeysMatchesTheRenderers(t *testing.T) {
	m := ownerModel(t) // a model wide enough to declare every conditional column
	m.procsVP.Width, m.portsVP.Width = 150, 150
	m.hasThroughput = true
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:1", Remote: "8.8.8.8:443"},
		{Proto: "tcp", Local: "10.1.0.2:2", Remote: "1.0.0.1:443"},
	}
	m.lastSnap.Ifaces = []probe.IfaceInfo{
		{Name: "eth0", Addrs: []string{"10.0.0.2/24"}},
		{Name: "wg0", Addrs: []string{"10.1.0.2/24"}},
	}
	m.connIface = map[string]string{
		connKey(m.conns[0]): "eth0",
		connKey(m.conns[1]): "wg0",
	}
	m.procs = []probe.ProcNet{{PID: 1, Name: "x", Container: "abc123"}}

	declared := map[string]bool{}
	for _, k := range AllColumnKeys() {
		declared[k] = true
	}
	for _, k := range m.columnKeysInUse() {
		if !declared[k] {
			t.Errorf("column %q is rendered but missing from AllColumnKeys", k)
		}
	}
}

// --- config wiring -------------------------------------------------------

// Every one of this round's features is reachable only if applyConfig actually
// wires it to something. A feature that loads but is never called is the
// failure mode that unit tests of the feature itself cannot see.
func TestConfigWiresEveryNewFeature(t *testing.T) {
	defer applyTheme(defaultTheme)

	dir := t.TempDir()
	asn := filepath.Join(dir, "ip2asn.tsv")
	if err := os.WriteFile(asn, []byte("8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Written through saveConfig so the round trip, not just the loader, is
	// what is being tested.
	seed := NewModel()
	seed.asnPath = asn
	seed.theme = "contrast"
	seed.procTree = true
	seed.alertRules.ifaceDropsPerMin = 90
	seed.keymap = newKeymap(map[string]string{"closed": "Z"})
	seed.saveConfig()

	m := NewModel().WithConfig()

	if m.asnPath != asn {
		t.Errorf("asn_database = %q, want %q", m.asnPath, asn)
	}
	if m.asnDB.Len() == 0 {
		t.Errorf("the ASN database was configured but never loaded (err: %v)", m.asnErr)
	}
	if got := m.ownerOf("8.8.8.8:443"); got != "GOOGLE" {
		t.Errorf("ownerOf = %q after a config round trip", got)
	}
	if m.theme != "contrast" {
		t.Errorf("theme = %q", m.theme)
	}
	if !m.procTree {
		t.Error("process_tree did not survive the round trip")
	}
	if m.alertRules.ifaceDropsPerMin != 90 {
		t.Errorf("alert_iface_drops_per_min = %v", m.alertRules.ifaceDropsPerMin)
	}
	if got := m.keymap.keyFor("closed"); got != "Z" {
		t.Errorf("keys did not survive the round trip: closed is bound to %q", got)
	}
	// And the binding reaches the handler, not just the struct.
	m.w, m.h = 160, 45
	m.activeTab = tabConns
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Z")})
	if !out.(Model).showClosed {
		t.Error("the persisted key binding does not reach the key handler")
	}
}

// A config naming a database that is not there must still start, with the
// column simply absent.
func TestConfigWithBrokenASNPathStillStarts(t *testing.T) {
	seed := NewModel()
	seed.asnPath = filepath.Join(t.TempDir(), "gone.tsv")
	seed.saveConfig()

	m := NewModel().WithConfig()
	if m.asnErr == nil {
		t.Error("a missing database was not reported")
	}
	m.w, m.h = 160, 45
	m.connsVP.Width = 140
	for _, c := range m.connsColumns() {
		if c.key == "owner" {
			t.Error("the OWNER column appears although the database failed to load")
		}
	}
}
