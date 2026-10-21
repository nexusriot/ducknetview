package ui

import (
	"fmt"
	"strings"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// The Processes tab says a process is moving 40 MB/s; the obvious next question
// is where to. Until now the answer meant retyping its name into the
// Connections search by hand, which is the same gap `a` and `w` closed for the
// baseline and the watchlist — a reading of the data the tool could act on, and
// didn't.

type drillKind int

const (
	drillNone drillKind = iota
	drillPID            // the connections of one process (or one folded group)
	drillPort           // the connections served by one listener
)

// drillTarget narrows the Connections tab to the row that was highlighted on
// another tab, remembering where to go back to.
type drillTarget struct {
	kind  drillKind
	pids  []int32
	label string

	port  int
	proto string

	backTab    tab
	backCursor int
}

// matches reports whether a connection belongs to the drilled-into subject.
func (d *drillTarget) matches(c probe.Conn) bool {
	if d == nil {
		return true
	}
	switch d.kind {
	case drillPID:
		for _, pid := range d.pids {
			if c.PID == pid {
				return true
			}
		}
		return false
	case drillPort:
		if d.proto != "" && !strings.EqualFold(d.proto, c.Proto) {
			return false
		}
		_, port := probe.SplitHostPort(c.Local)
		return port == d.port
	}
	return true
}

// describe is how the filter line words the drill.
func (d *drillTarget) describe() string {
	if d == nil {
		return ""
	}
	switch d.kind {
	case drillPID:
		if len(d.pids) > 1 {
			return fmt.Sprintf("process %s (%d pids)", d.label, len(d.pids))
		}
		return "process " + d.label
	case drillPort:
		return fmt.Sprintf("listener %s/%d", d.proto, d.port)
	}
	return ""
}

// drillInto narrows the Connections tab to the highlighted row and switches to
// it. ok is false when the row has nothing to follow, which is reported rather
// than silently doing nothing.
func (m *Model) drillInto() (string, bool) {
	switch m.activeTab {
	case tabProcs:
		rows := m.filteredProcs()
		if m.procsCursor < 0 || m.procsCursor >= len(rows) {
			return "No process selected", false
		}
		r := rows[m.procsCursor]
		pids := r.pids
		if len(pids) == 0 {
			pids = []int32{r.PID}
		}
		label := r.Name
		if label == "" {
			label = fmt.Sprintf("pid %d", r.PID)
		}
		m.setDrill(&drillTarget{kind: drillPID, pids: pids, label: label})
		return "Connections of " + label, true

	case tabPorts:
		rows := m.filteredPorts()
		if m.portsCursor < 0 || m.portsCursor >= len(rows) {
			return "No listener selected", false
		}
		p := rows[m.portsCursor]
		_, port := probe.SplitHostPort(p.Local)
		if port <= 0 {
			return "That row has no port to follow", false
		}
		m.setDrill(&drillTarget{kind: drillPort, port: port, proto: p.Proto})
		return fmt.Sprintf("Connections served by %s/%d", p.Proto, port), true

	case tabConns:
		// Pressing it again on the tab it lands on is how you get back, which
		// is the only binding that would otherwise have nothing to do here.
		if m.drill == nil {
			return "Nothing to go back to — press F on a process or listener", false
		}
		return m.clearDrill(), true
	}
	return "F follows a process or listener into its connections", false
}

// setDrill applies a drill and jumps to the Connections tab, remembering where
// it came from.
func (m *Model) setDrill(d *drillTarget) {
	d.backTab = m.activeTab
	switch m.activeTab {
	case tabProcs:
		d.backCursor = m.procsCursor
	case tabPorts:
		d.backCursor = m.portsCursor
	}
	m.drill = d
	m.activeTab = tabConns
	// A drill that landed inside the closed-connections history would answer a
	// different question than the one that was asked.
	m.showClosed = false
	m.connsCursor = 0
	m.applyFilterChange()
	m.scrollActiveTableTop()
}

// clearDrill removes the filter and returns to the tab the drill started from.
func (m *Model) clearDrill() string {
	back, cursor := m.drill.backTab, m.drill.backCursor
	m.drill = nil
	m.activeTab = back
	switch back {
	case tabProcs:
		m.procsCursor = clampInt(cursor, 0, max(0, len(m.filteredProcs())-1))
	case tabPorts:
		m.portsCursor = clampInt(cursor, 0, max(0, len(m.filteredPorts())-1))
	}
	// Not applyFilterChange: that resets the cursor this just restored.
	m.rerenderActiveTable()
	if c, vp := m.activeTableState(); c != nil {
		ensureCursorVisible(vp, *c)
	}
	return "Back to " + tabLabel(back)
}

// tabLabel names a tab for a status message.
func tabLabel(t tab) string {
	switch t {
	case tabOverview:
		return "Overview"
	case tabIfaces:
		return "Interfaces"
	case tabPorts:
		return "Ports"
	case tabProcs:
		return "Processes"
	case tabConns:
		return "Connections"
	case tabEvents:
		return "Events"
	case tabRoutes:
		return "Routes"
	}
	return "?"
}
