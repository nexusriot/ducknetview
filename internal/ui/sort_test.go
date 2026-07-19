package ui

import (
	"testing"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func sortIndex(cols []string, name string) int {
	for i, c := range cols {
		if c == name {
			return i
		}
	}
	return -1
}

func procNames(ps []probe.ProcNet) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSortProcs(t *testing.T) {
	m := NewModel()
	m.procs = []probe.ProcNet{
		{PID: 1, Name: "zsh", ConnCount: 5},
		{PID: 2, Name: "apache", ConnCount: 1},
		{PID: 3, Name: "mongo", ConnCount: 3},
	}

	// Default: connection count, descending.
	if got := procNames(m.filteredProcs()); !eqStrs(got, []string{"zsh", "mongo", "apache"}) {
		t.Fatalf("default (conns desc): %v", got)
	}

	// Name ascending.
	m.procsSortCol = sortIndex(procsSortCols, "name")
	m.procsSortDesc = false
	if got := procNames(m.filteredProcs()); !eqStrs(got, []string{"apache", "mongo", "zsh"}) {
		t.Fatalf("name asc: %v", got)
	}

	// Name descending.
	m.procsSortDesc = true
	if got := procNames(m.filteredProcs()); !eqStrs(got, []string{"zsh", "mongo", "apache"}) {
		t.Fatalf("name desc: %v", got)
	}
}

func TestSortPortsByPort(t *testing.T) {
	m := NewModel()
	m.ports = []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:443", PID: 1},
		{Proto: "tcp", Local: "0.0.0.0:22", PID: 2},
		{Proto: "udp", Local: "0.0.0.0:53", PID: 3},
	}
	m.portsSortCol = sortIndex(portsSortCols, "port")
	m.portsSortDesc = false

	got := m.filteredPorts()
	wantPorts := []int{22, 53, 443}
	for i, p := range got {
		if portOf(p.Local) != wantPorts[i] {
			t.Fatalf("port asc order wrong at %d: %v", i, got)
		}
	}
}

func TestPortOf(t *testing.T) {
	cases := map[string]int{
		"1.2.3.4:443": 443,
		"0.0.0.0:0":   0,
		"::1:8080":    8080,
		"-":           0,
		"noport":      0,
	}
	for in, want := range cases {
		if got := portOf(in); got != want {
			t.Fatalf("portOf(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestCycleSortAndToggle(t *testing.T) {
	m := NewModel()
	m.activeTab = tabPorts
	m.portsCursor = 3

	start := m.portsSortCol
	m.cycleSortColumn()
	if m.portsSortCol != (start+1)%len(portsSortCols) {
		t.Fatalf("sort column not advanced: %d", m.portsSortCol)
	}
	if m.portsCursor != 0 {
		t.Fatalf("cursor not reset on re-sort: %d", m.portsCursor)
	}

	before := m.portsSortDesc
	m.toggleSortDir()
	if m.portsSortDesc == before {
		t.Fatal("sort direction not toggled")
	}
}
