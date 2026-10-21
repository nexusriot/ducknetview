package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nexusriot/ducknetview/internal/probe"
)

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
