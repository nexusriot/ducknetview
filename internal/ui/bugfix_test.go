package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func typeKey(t *testing.T, m Model, s string) Model {
	t.Helper()
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return model.(Model)
}

// While a search box is focused, global single-key bindings (q, s, tab, /, and
// the arrow keys) must reach the text input instead of being swallowed or
// switching tabs. Regression test for the global key switch stealing keys.
func TestSearchKeysReachInput(t *testing.T) {
	m := NewModel()
	m.activeTab = tabProcs

	m = typeKey(t, m, "/")
	if !m.procsSearching {
		t.Fatal("'/' should open the procs search box")
	}

	// 's' and 'q' are global bindings (sort / quit-noop) that previously got
	// captured; every character here must land in the input.
	for _, r := range "squid" {
		m = typeKey(t, m, string(r))
	}
	if got := m.procsSearch.Value(); got != "squid" {
		t.Fatalf("search input = %q, want %q (global keys still swallowed?)", got, "squid")
	}

	// Arrow keys must edit within the input, not switch tabs.
	before := m.activeTab
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = model.(Model)
	if m.activeTab != before {
		t.Fatalf("left arrow switched tabs while searching: %v", m.activeTab)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = model.(Model)
	if !m.procsSearching {
		t.Fatal("'q' while searching should not exit/alter search mode")
	}
}

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
