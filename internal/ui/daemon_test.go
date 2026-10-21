package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The daemon's only way out is this message, and it has to take the same exit
// path as `q` — otherwise the day's usage totals are lost on every restart.
func TestDaemonShutdownQuits(t *testing.T) {
	m := NewModel()
	_, cmd := m.Update(DaemonShutdownMsg{})
	if cmd == nil {
		t.Fatal("DaemonShutdownMsg produced no command, so the daemon would never exit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("DaemonShutdownMsg did not quit, got %T", cmd())
	}
}
