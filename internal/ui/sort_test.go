package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

func procNames(ps []procRow) []string {
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

// The scope ordering is what a "scope" sort means: the internet first,
// loopback last.
func TestScopeRankOrdering(t *testing.T) {
	order := []string{"public", "cgnat", "private", "link", "local"}
	for i := 1; i < len(order); i++ {
		if scopeRank(order[i-1]) >= scopeRank(order[i]) {
			t.Errorf("%s does not sort before %s", order[i-1], order[i])
		}
	}
	if scopeRank("nonsense") <= scopeRank("local") {
		t.Error("an unknown scope should sort last")
	}
}

func TestProcsSortByCPU(t *testing.T) {
	m := NewModel()
	m.procs = []probe.ProcNet{
		{PID: 1, Name: "a", CPU: 5},
		{PID: 2, Name: "b", CPU: 50},
		{PID: 3, Name: "c", CPU: 20},
	}
	m.procsSortCol = sortIndex(procsSortCols, "cpu")
	m.procsSortDesc = true
	if got := procNames(m.filteredProcs()); !eqStrs(got, []string{"b", "c", "a"}) {
		t.Fatalf("cpu desc order wrong: %v", got)
	}
}

func TestConnSortByScope(t *testing.T) {
	m := NewModel()
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "10.0.0.9:80", State: "ESTABLISHED", PID: 1},  // private
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "8.8.8.8:80", State: "ESTABLISHED", PID: 2},   // public
		{Proto: "tcp", Local: "10.0.0.1:3", Remote: "127.0.0.1:80", State: "ESTABLISHED", PID: 3}, // local
	}
	m.connsSortCol = sortIndex(connsSortCols, "scope")
	m.connsSortDesc = false
	got := m.filteredConns()
	if probe.IPScope(stripPort(got[0].Remote)) != "public" {
		t.Fatalf("scope sort should put public first, got %s", got[0].Remote)
	}
	if probe.IPScope(stripPort(got[2].Remote)) != "local" {
		t.Fatalf("scope sort should put local last, got %s", got[2].Remote)
	}
}

func TestSortByThroughput(t *testing.T) {
	m := ratesModel()
	slow := probe.Conn{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", PID: 1}
	fast := probe.Conn{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", PID: 2}
	m.conns = []probe.Conn{slow, fast}
	m.connRates = map[string]rateSample{
		connKey(slow): {rx: 10},
		connKey(fast): {rx: 9999},
	}
	m.connsSortCol = indexOfCol(t, connsSortCols, "rx")

	rows := m.filteredConns()
	if rows[0].PID != 2 {
		t.Errorf("rx sort put PID %d first, want the busiest (2)", rows[0].PID)
	}

	m.connsSortDesc = true
	rows = m.filteredConns()
	if rows[0].PID != 1 {
		t.Errorf("reversed rx sort put PID %d first, want the quietest (1)", rows[0].PID)
	}

	m.procs = []probe.ProcNet{{PID: 1}, {PID: 2}}
	m.procRates = map[int32]rateSample{1: {tx: 5}, 2: {tx: 500}}
	m.procsSortCol = indexOfCol(t, procsSortCols, "tx")
	m.procsSortDesc = true // the processes tab defaults to descending
	if got := m.filteredProcs()[0].PID; got != 2 {
		t.Errorf("tx sort put PID %d first, want the busiest (2)", got)
	}
}

func TestSortByRTTAndRetrans(t *testing.T) {
	m := totalsModel()
	quick := probe.Conn{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", PID: 1, RTT: time.Millisecond}
	slow := probe.Conn{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", PID: 2, RTT: 900 * time.Millisecond, Retrans: 12}
	m.conns = []probe.Conn{quick, slow}

	m.connsSortCol = indexOfCol(t, connsSortCols, "rtt")
	if got := m.filteredConns()[0].PID; got != 2 {
		t.Errorf("rtt sort put PID %d first, want the slowest connection", got)
	}

	m.connsSortCol = indexOfCol(t, connsSortCols, "retrans")
	if got := m.filteredConns()[0].PID; got != 2 {
		t.Errorf("retrans sort put PID %d first, want the lossiest connection", got)
	}
}

func TestSortIndicatorMarksActiveColumn(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabPorts
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd", User: "root"}}

	m.portsSortCol = indexOfCol(t, portsSortCols, "port")
	m.portsSortDesc = false
	text := stripANSI(m.renderPortsText())
	if !strings.Contains(text, "LOCAL↑") {
		t.Errorf("ascending arrow missing from the sorted column:\n%s", text)
	}

	m.portsSortDesc = true
	if text = stripANSI(m.renderPortsText()); !strings.Contains(text, "LOCAL↓") {
		t.Errorf("descending arrow missing:\n%s", text)
	}
	// Only the active column is marked.
	if strings.Contains(text, "PID↓") || strings.Contains(text, "USER↓") {
		t.Errorf("arrow drawn on an inactive column:\n%s", text)
	}

	// The arrow must not widen the column, or every row below it misaligns.
	for _, line := range strings.Split(stripANSI(m.renderPortsText()), "\n") {
		if w := lipgloss.Width(line); w > m.portsVP.Width {
			t.Fatalf("line wider than the viewport (%d): %q", w, line)
		}
	}
}

func TestClickingHeaderSortsAndReverses(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabProcs
	m.procs = []probe.ProcNet{{PID: 2, Name: "b", ConnCount: 1}, {PID: 1, Name: "a", ConnCount: 5}}

	cols := m.procsColumns()
	// x of the NAME column: gutter + the PID column + the separator.
	x := tableContentX + cols.width("pid") + len(colSep)
	y := 4 + headerLineIndex

	click := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: x, Y: y}
	model, _ := m.Update(click)
	m = model.(Model)

	if got := m.sortKey(); got != "name" {
		t.Fatalf("clicking the NAME header sorted by %q", got)
	}
	firstDir := m.sortDesc()

	// Clicking the active column again reverses it, as a spreadsheet would.
	model, _ = m.Update(click)
	m = model.(Model)
	if m.sortKey() != "name" || m.sortDesc() == firstDir {
		t.Errorf("second click gave (%q, desc=%v), want the same column reversed", m.sortKey(), m.sortDesc())
	}

	// A click on an unsortable column changes nothing.
	m.activeTab = tabProcs
	m.procs[0].Container = "abc123def456"
	before := m.sortKey()
	cols = m.procsColumns()
	contX := tableContentX + cols.width("pid") + len(colSep) + cols.width("name") + len(colSep) +
		cols.width("user") + len(colSep)
	model, _ = m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: contX, Y: y})
	if got := model.(Model).sortKey(); got != before {
		t.Errorf("clicking CONTAINER changed the sort to %q", got)
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
