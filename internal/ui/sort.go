package ui

import (
	"sort"
	"strings"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Sort columns offered per table tab, cycled with `s` (direction toggled with
// `S`). The first entry of each is the default sort key.
var (
	portsSortCols = []string{"proto", "port", "pid", "process", "user"}
	procsSortCols = []string{"conns", "listen", "cpu", "mem", "rx", "tx", "pid", "name", "user"}
	connsSortCols = []string{"pid", "proto", "state", "local", "remote", "process", "scope", "age", "rx", "tx", "rtt", "retrans", "owner"}
	groupSortCols = []string{"conns", "host", "bytes"}
)

// rateSortCols are the sort keys backed by throughput, which lives in the
// model's rate maps rather than on the probe rows (like "age").
func isRateSortCol(col string) bool { return col == "rx" || col == "tx" }

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
	case "user":
		if c := cmpStr(a.User, b.User); c != 0 {
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
	case "user":
		if c := cmpStr(a.User, b.User); c != 0 {
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
	case "rtt":
		// Slowest first: a sort on latency is a hunt for the bad connection.
		if c := -cmpInt(int(a.RTT), int(b.RTT)); c != 0 {
			return c
		}
	case "retrans":
		if c := -cmpInt(int(a.Retrans), int(b.Retrans)); c != 0 {
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

func (m Model) sortProcs(rows []procRow) {
	col := procsSortCols[m.procsSortCol]
	if isRateSortCol(col) {
		// Throughput lives in the model's rate and totals maps, not on the row,
		// and follows whichever the RX/TX columns are showing. The tab defaults
		// to descending, so this ordering puts the busiest first.
		sort.SliceStable(rows, func(i, j int) bool {
			var c int
			if m.showTotals {
				c = cmpTotals(m.rowTotal(rows[i]), m.rowTotal(rows[j]), col)
			} else {
				c = cmpRate(m.rowRate(rows[i]), m.rowRate(rows[j]), col)
			}
			if c == 0 {
				c = cmpInt(int(rows[i].PID), int(rows[j].PID))
			}
			if m.procsSortDesc {
				c = -c
			}
			return c < 0
		})
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		c := cmpProcs(rows[i].ProcNet, rows[j].ProcNet, col)
		if m.procsSortDesc {
			c = -c
		}
		return c < 0
	})
}

// cmpRate orders two throughput samples on the given key, ascending.
func cmpRate(a, b rateSample, col string) int {
	if col == "tx" {
		return cmpFloat(a.tx, b.tx)
	}
	return cmpFloat(a.rx, b.rx)
}

// cmpTotals orders two cumulative traffic totals on the given key, ascending.
func cmpTotals(a, b byteTotals, col string) int {
	if col == "tx" {
		return cmpInt64(a.tx, b.tx)
	}
	return cmpInt64(a.rx, b.rx)
}

func cmpInt64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func (m Model) sortConns(rows []probe.Conn) {
	col := connsSortCols[m.connsSortCol]
	if isRateSortCol(col) {
		// This tab defaults to ascending, so negate to surface the busiest
		// connections first — the same convention the "age" key uses.
		sort.SliceStable(rows, func(i, j int) bool {
			var c int
			if m.showTotals {
				c = -cmpTotals(m.connTotal(rows[i]), m.connTotal(rows[j]), col)
			} else {
				c = -cmpRate(m.connRate(rows[i]), m.connRate(rows[j]), col)
			}
			if c == 0 {
				c = cmpConns(rows[i], rows[j], "pid")
			}
			if m.connsSortDesc {
				c = -c
			}
			return c < 0
		})
		return
	}
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
	if col == "owner" {
		// Ownership comes from the loaded table rather than the Conn, and
		// grouping a session's peers by network is the point of the column.
		sort.SliceStable(rows, func(i, j int) bool {
			c := cmpStr(m.ownerOf(rows[i].Remote), m.ownerOf(rows[j].Remote))
			if c == 0 {
				c = cmpConns(rows[i], rows[j], "remote")
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
	if col == "bytes" {
		// Ranks remote hosts by traffic — the grouped view's answer to "who am
		// I actually talking to the most".
		sort.SliceStable(rows, func(i, j int) bool {
			var c int
			if m.showTotals {
				c = cmpInt64(m.hostTotal(rows[i].ip).sum(), m.hostTotal(rows[j].ip).sum())
			} else {
				ri, rj := m.groupRate(rows[i]), m.groupRate(rows[j])
				c = cmpFloat(ri.rx+ri.tx, rj.rx+rj.tx)
			}
			if c == 0 {
				c = cmpStr(rows[i].ip, rows[j].ip)
			}
			if m.groupSortDesc {
				c = -c
			}
			return c < 0
		})
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		c := cmpGroups(rows[i], rows[j], col)
		if m.groupSortDesc {
			c = -c
		}
		return c < 0
	})
}

// sortKey returns the active tab's current sort column, and sortDesc its
// direction. The column header marks that column with an arrow.
func (m Model) sortKey() string {
	col, _ := m.activeSort()
	return col
}

func (m Model) sortDesc() bool {
	_, desc := m.activeSort()
	return desc
}

// setSortColumn sorts the active tab by a column key, or reverses the direction
// when that column is already the active one — what clicking a header twice is
// expected to do.
func (m *Model) setSortColumn(key string) bool {
	var (
		cols []string
		idx  *int
		desc *bool
	)
	switch m.activeTab {
	case tabPorts:
		cols, idx, desc = portsSortCols, &m.portsSortCol, &m.portsSortDesc
	case tabProcs:
		cols, idx, desc = procsSortCols, &m.procsSortCol, &m.procsSortDesc
	case tabConns:
		switch {
		case m.showClosed:
			cols, idx, desc = closedSortCols, &m.closedSortCol, &m.closedSortDesc
		case m.connsGroup:
			cols, idx, desc = groupSortCols, &m.groupSortCol, &m.groupSortDesc
		default:
			cols, idx, desc = connsSortCols, &m.connsSortCol, &m.connsSortDesc
		}
	default:
		return false
	}

	for i, c := range cols {
		if c != key {
			continue
		}
		if *idx == i {
			*desc = !*desc
		} else {
			*idx = i
		}
		m.resetTableCursor()
		m.rerenderActiveTable()
		m.scrollActiveTableTop()
		m.status = "Sort: " + m.sortLabel()
		m.saveConfig()
		return true
	}
	return false
}

// activeSort returns the current sort column and direction for the active tab.
func (m Model) activeSort() (string, bool) {
	var col string
	var desc bool
	switch m.activeTab {
	case tabPorts:
		col, desc = portsSortCols[m.portsSortCol], m.portsSortDesc
	case tabProcs:
		col, desc = procsSortCols[m.procsSortCol], m.procsSortDesc
	case tabConns:
		switch {
		case m.showClosed:
			col, desc = closedSortCols[m.closedSortCol], m.closedSortDesc
		case m.connsGroup:
			col, desc = groupSortCols[m.groupSortCol], m.groupSortDesc
		default:
			col, desc = connsSortCols[m.connsSortCol], m.connsSortDesc
		}
	default:
		return "", false
	}
	return col, desc
}

// sortLabel describes the active table tab's current sort, e.g. "conns ↓".
func (m Model) sortLabel() string {
	col, desc := m.activeSort()
	if col == "" {
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
		switch {
		case m.showClosed:
			m.closedSortCol = (m.closedSortCol + 1) % len(closedSortCols)
		case m.connsGroup:
			m.groupSortCol = (m.groupSortCol + 1) % len(groupSortCols)
		default:
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
		switch {
		case m.showClosed:
			m.closedSortDesc = !m.closedSortDesc
		case m.connsGroup:
			m.groupSortDesc = !m.groupSortDesc
		default:
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
