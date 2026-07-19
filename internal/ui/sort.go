package ui

import (
	"sort"
	"strings"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Sort columns offered per table tab, cycled with `s` (direction toggled with
// `S`). The first entry of each is the default sort key.
var (
	portsSortCols = []string{"proto", "port", "pid", "process"}
	procsSortCols = []string{"conns", "listen", "cpu", "mem", "pid", "name"}
	connsSortCols = []string{"pid", "proto", "state", "local", "remote", "process", "scope", "age"}
	groupSortCols = []string{"conns", "host"}
)

func cmpStr(a, b string) int {
	a, b = strings.ToLower(a), strings.ToLower(b)
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// portOf extracts the numeric port from an "ip:port" string (0 if absent).
func portOf(hp string) int {
	i := strings.LastIndex(hp, ":")
	if i < 0 || i+1 >= len(hp) {
		return 0
	}
	n := 0
	for _, r := range hp[i+1:] {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// Comparators return -1/0/1 for the given sort column, appending a stable
// tiebreak so equal keys keep a deterministic order.
func cmpPorts(a, b probe.ListenPort, col string) int {
	switch col {
	case "proto":
		if c := cmpStr(a.Proto, b.Proto); c != 0 {
			return c
		}
	case "port":
		if c := cmpInt(portOf(a.Local), portOf(b.Local)); c != 0 {
			return c
		}
	case "pid":
		if c := cmpInt(int(a.PID), int(b.PID)); c != 0 {
			return c
		}
	case "process":
		if c := cmpStr(a.Process, b.Process); c != 0 {
			return c
		}
	}
	if c := cmpStr(a.Local, b.Local); c != 0 {
		return c
	}
	return cmpInt(int(a.PID), int(b.PID))
}

func cmpProcs(a, b probe.ProcNet, col string) int {
	switch col {
	case "conns":
		if c := cmpInt(a.ConnCount, b.ConnCount); c != 0 {
			return c
		}
	case "listen":
		if c := cmpInt(a.ListenCount, b.ListenCount); c != 0 {
			return c
		}
	case "cpu":
		if c := cmpFloat(a.CPU, b.CPU); c != 0 {
			return c
		}
	case "mem":
		if c := cmpFloat(float64(a.Mem), float64(b.Mem)); c != 0 {
			return c
		}
	case "pid":
		if c := cmpInt(int(a.PID), int(b.PID)); c != 0 {
			return c
		}
	case "name":
		if c := cmpStr(a.Name, b.Name); c != 0 {
			return c
		}
	}
	return cmpInt(int(a.PID), int(b.PID))
}

func cmpConns(a, b probe.Conn, col string) int {
	switch col {
	case "pid":
		if c := cmpInt(int(a.PID), int(b.PID)); c != 0 {
			return c
		}
	case "proto":
		if c := cmpStr(a.Proto, b.Proto); c != 0 {
			return c
		}
	case "state":
		if c := cmpStr(a.State, b.State); c != 0 {
			return c
		}
	case "local":
		if c := cmpInt(portOf(a.Local), portOf(b.Local)); c != 0 {
			return c
		}
	case "remote":
		if c := cmpStr(a.Remote, b.Remote); c != 0 {
			return c
		}
	case "process":
		if c := cmpStr(a.Process, b.Process); c != 0 {
			return c
		}
	case "scope":
		if c := cmpInt(scopeRank(probe.IPScope(stripPort(a.Remote))), scopeRank(probe.IPScope(stripPort(b.Remote)))); c != 0 {
			return c
		}
	}
	if c := cmpInt(int(a.PID), int(b.PID)); c != 0 {
		return c
	}
	if c := cmpStr(a.Remote, b.Remote); c != 0 {
		return c
	}
	return cmpStr(a.Local, b.Local)
}

// scopeRank orders IP scopes most-routable first, so a "scope" sort surfaces
// public/internet peers ahead of private and local ones.
func scopeRank(scope string) int {
	switch scope {
	case "public":
		return 0
	case "cgnat":
		return 1
	case "private":
		return 2
	case "link":
		return 3
	case "local":
		return 4
	}
	return 5
}

func cmpGroups(a, b *connGroup, col string) int {
	switch col {
	case "conns":
		if c := cmpInt(a.count, b.count); c != 0 {
			return c
		}
	case "host":
		if c := cmpStr(a.ip, b.ip); c != 0 {
			return c
		}
	}
	return cmpStr(a.ip, b.ip)
}

func (m Model) sortPorts(rows []probe.ListenPort) {
	col := portsSortCols[m.portsSortCol]
	sort.SliceStable(rows, func(i, j int) bool {
		c := cmpPorts(rows[i], rows[j], col)
		if m.portsSortDesc {
			c = -c
		}
		return c < 0
	})
}

func (m Model) sortProcs(rows []probe.ProcNet) {
	col := procsSortCols[m.procsSortCol]
	sort.SliceStable(rows, func(i, j int) bool {
		c := cmpProcs(rows[i], rows[j], col)
		if m.procsSortDesc {
			c = -c
		}
		return c < 0
	})
}

func (m Model) sortConns(rows []probe.Conn) {
	col := connsSortCols[m.connsSortCol]
	if col == "age" {
		// Age lives in the model's first-seen map, not on the Conn, so sort it
		// here: default (ascending) shows the newest connections first.
		sort.SliceStable(rows, func(i, j int) bool {
			ti := m.connFirstSeen[connKey(rows[i])]
			tj := m.connFirstSeen[connKey(rows[j])]
			c := 0
			switch {
			case ti.After(tj):
				c = -1
			case ti.Before(tj):
				c = 1
			default:
				c = cmpConns(rows[i], rows[j], "pid")
			}
			if m.connsSortDesc {
				c = -c
			}
			return c < 0
		})
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		c := cmpConns(rows[i], rows[j], col)
		if m.connsSortDesc {
			c = -c
		}
		return c < 0
	})
}

func (m Model) sortGroups(rows []*connGroup) {
	col := groupSortCols[m.groupSortCol]
	sort.SliceStable(rows, func(i, j int) bool {
		c := cmpGroups(rows[i], rows[j], col)
		if m.groupSortDesc {
			c = -c
		}
		return c < 0
	})
}

// sortLabel describes the active table tab's current sort, e.g. "conns ↓".
func (m Model) sortLabel() string {
	var col string
	var desc bool
	switch m.activeTab {
	case tabPorts:
		col, desc = portsSortCols[m.portsSortCol], m.portsSortDesc
	case tabProcs:
		col, desc = procsSortCols[m.procsSortCol], m.procsSortDesc
	case tabConns:
		if m.connsGroup {
			col, desc = groupSortCols[m.groupSortCol], m.groupSortDesc
		} else {
			col, desc = connsSortCols[m.connsSortCol], m.connsSortDesc
		}
	default:
		return ""
	}
	if desc {
		return col + " ↓"
	}
	return col + " ↑"
}

// cycleSortColumn advances the active table tab's sort column (wrapping) and
// rerenders. No-op on non-table tabs.
func (m *Model) cycleSortColumn() {
	switch m.activeTab {
	case tabPorts:
		m.portsSortCol = (m.portsSortCol + 1) % len(portsSortCols)
		m.portsCursor = 0
	case tabProcs:
		m.procsSortCol = (m.procsSortCol + 1) % len(procsSortCols)
		m.procsCursor = 0
	case tabConns:
		if m.connsGroup {
			m.groupSortCol = (m.groupSortCol + 1) % len(groupSortCols)
		} else {
			m.connsSortCol = (m.connsSortCol + 1) % len(connsSortCols)
		}
		m.connsCursor = 0
	default:
		return
	}
	m.rerenderActiveTable()
	m.scrollActiveTableTop()
	m.status = "Sort: " + m.sortLabel()
	m.saveConfig()
}

// toggleSortDir flips the active table tab's sort direction and rerenders.
func (m *Model) toggleSortDir() {
	switch m.activeTab {
	case tabPorts:
		m.portsSortDesc = !m.portsSortDesc
		m.portsCursor = 0
	case tabProcs:
		m.procsSortDesc = !m.procsSortDesc
		m.procsCursor = 0
	case tabConns:
		if m.connsGroup {
			m.groupSortDesc = !m.groupSortDesc
		} else {
			m.connsSortDesc = !m.connsSortDesc
		}
		m.connsCursor = 0
	default:
		return
	}
	m.rerenderActiveTable()
	m.scrollActiveTableTop()
	m.status = "Sort: " + m.sortLabel()
	m.saveConfig()
}
