package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nexusriot/ducknetview/internal/probe"
)

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
