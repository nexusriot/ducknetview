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

// TestMain sandboxes all config I/O to a throwaway dir so tests never read or
// clobber the developer's real ~/.config/ducknetview/config.json.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dnv-ui-test")
	if err != nil {
		os.Exit(m.Run())
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("HOME", dir) // macOS UserConfigDir ignores XDG and uses $HOME
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func writeConfig(t *testing.T, contents string) {
	t.Helper()
	p, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
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

// enter opens the row-detail overlay (with the resolved service name), and any
// key closes it.
func TestDetailOverlay(t *testing.T) {
	m := NewModel()
	m.activeTab = tabPorts
	m.portsVP.Width = 100
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:443", PID: 1, Process: "nginx"}}

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if !m.showDetail {
		t.Fatal("enter should open the detail overlay")
	}
	if !strings.Contains(m.detailText, "nginx") || !strings.Contains(m.detailText, "443") {
		t.Fatalf("detail missing fields: %q", m.detailText)
	}
	if svc := probe.ServiceName(443, "tcp"); svc != "" && !strings.Contains(m.detailText, svc) {
		t.Fatalf("detail should include service %q: %q", svc, m.detailText)
	}

	// Navigation keys scroll the overlay instead of dismissing it, so long
	// command lines can be read in full.
	model, _ = m.Update(key("j"))
	m = model.(Model)
	if !m.showDetail {
		t.Fatal("j should scroll the detail overlay, not close it")
	}

	model, _ = m.Update(key("z"))
	m = model.(Model)
	if m.showDetail {
		t.Fatal("any non-navigation key should close the detail overlay")
	}
}

// Config persistence round-trips through an isolated config dir.
func TestConfigRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := NewModel()
	m.hideNoise = true
	m.connsGroup = true
	m.refreshInterval = refreshSteps[2]
	m.procsSortCol = 2
	m.procsSortDesc = false
	m.saveConfig()

	got := NewModel().WithConfig()
	if !got.hideNoise || !got.connsGroup {
		t.Fatalf("toggles not persisted: hideNoise=%v group=%v", got.hideNoise, got.connsGroup)
	}
	if got.refreshInterval != refreshSteps[2] {
		t.Fatalf("refresh interval not persisted: %v", got.refreshInterval)
	}
	if got.procsSortCol != 2 || got.procsSortDesc {
		t.Fatalf("sort state not persisted: col=%d desc=%v", got.procsSortCol, got.procsSortDesc)
	}
}

// Toggling a persisted setting through the real key path writes the config
// immediately (save-on-change), so settings survive even a signal-kill quit.
func TestTogglePersistsImmediately(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := NewModel()
	m.activeTab = tabConns
	model, _ := m.Update(key("g")) // enable grouping
	m = model.(Model)
	if !m.connsGroup {
		t.Fatal("'g' should enable grouping")
	}

	c, state := loadConfig()
	if state != configLoaded || !c.ConnsGroup {
		t.Fatalf("group toggle should persist immediately: state=%v conns_group=%v", state, c.ConnsGroup)
	}
}

// A hand-edited/corrupt config must never wedge the UI: out-of-range values are
// clamped and bad JSON is ignored.
func TestConfigIgnoresGarbage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Out-of-range sort column + unknown refresh interval.
	writeConfig(t, `{"procs_sort_col": 999, "refresh_ms": 123}`)
	m := NewModel().WithConfig()
	if m.procsSortCol < 0 || m.procsSortCol >= len(procsSortCols) {
		t.Fatalf("procsSortCol not clamped: %d", m.procsSortCol)
	}
	if !isRefreshStep(m.refreshInterval) {
		t.Fatalf("bogus refresh interval accepted: %v", m.refreshInterval)
	}

	// Corrupt JSON -> defaults preserved.
	writeConfig(t, `{not json`)
	def := NewModel()
	m2 := NewModel().WithConfig()
	if m2.procsSortDesc != def.procsSortDesc || m2.refreshInterval != def.refreshInterval {
		t.Fatal("corrupt config should leave defaults intact")
	}
}
