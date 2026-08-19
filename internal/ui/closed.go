package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// A live connection table can only answer questions about connections that
// still exist. "What was that 900 MB transfer that just finished?" needs the
// rows that disappeared — so the last few hundred are kept with their final
// byte counts and lifetimes.

// closedConnLimit bounds the retained history.
const closedConnLimit = 200

type closedConn struct {
	conn      probe.Conn
	firstSeen time.Time
	closedAt  time.Time
	totals    byteTotals
}

// lifetime is how long the connection existed, as far as this session saw it.
func (c closedConn) lifetime() time.Duration {
	if c.firstSeen.IsZero() {
		return 0
	}
	return c.closedAt.Sub(c.firstSeen)
}

// lifetimeLabel distinguishes "existed briefly" from "we never saw it open",
// which a bare duration would render identically as 0s.
func (c closedConn) lifetimeLabel() string {
	if c.firstSeen.IsZero() {
		return "-"
	}
	return formatDuration(c.lifetime())
}

// noteClosedConns records connections present in the previous enumeration but
// absent from this one. It must run before updateRates, which overwrites the
// counters it reads. The socket's cumulative counters are its lifetime totals,
// so no accumulation is needed here.
func (m *Model) noteClosedConns(conns []probe.Conn, now time.Time) {
	if len(m.prevConns) == 0 {
		return // first enumeration: nothing has closed yet
	}

	live := make(map[string]bool, len(conns))
	for _, c := range conns {
		live[connKey(c)] = true
	}

	var closed []closedConn
	for key, c := range m.prevConns {
		if live[key] {
			continue
		}
		closed = append(closed, closedConn{
			conn:      c,
			firstSeen: m.connFirstSeen[key],
			closedAt:  now,
			totals:    byteTotals{rx: c.RxBytes, tx: c.TxBytes},
		})
	}
	if len(closed) == 0 {
		return
	}

	// Within one poll the closures are indistinguishable in time, so order them
	// by size — the big transfer is the interesting one.
	sort.SliceStable(closed, func(i, j int) bool {
		return closed[i].totals.sum() > closed[j].totals.sum()
	})
	m.closedConns = probe.ClampHistory(append(m.closedConns, closed...), closedConnLimit)
}

// closedRows returns the retained closed connections, newest first, with the
// active search and quick filters applied.
func (m Model) closedRows() []closedConn {
	out := make([]closedConn, 0, len(m.closedConns))
	for i := len(m.closedConns) - 1; i >= 0; i-- {
		cc := m.closedConns[i]
		c := cc.conn
		if !m.passProtoFilter(c.Proto) || !m.passIPVerFilter(c.Remote) || !m.passUserFilter(c.User) {
			continue
		}
		if m.publicOnly && probe.IPScope(stripPort(c.Remote)) != "public" {
			continue
		}
		proc := c.Process
		if proc == "" {
			proc = "-"
		}
		if !newMatcher(m.filterQuery(m.connsQuery)).match(c.Local, c.Remote, m.dnsDisplay(c.Remote),
			proc, c.Proto, probe.ServiceName(portOf(c.Remote), c.Proto), c.User) {
			continue
		}
		out = append(out, cc)
	}
	m.sortClosed(out)
	return out
}

// closedSortCols are this view's own sort keys: the flat view's keys (state,
// age) do not apply to a connection that no longer exists.
var closedSortCols = []string{"closed", "bytes", "lifetime", "process", "remote"}

func (m Model) sortClosed(rows []closedConn) {
	col := closedSortCols[m.closedSortCol]
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		var c int
		switch col {
		case "bytes":
			c = -cmpInt64(a.totals.sum(), b.totals.sum())
		case "lifetime":
			c = -cmpInt(int(a.lifetime()), int(b.lifetime()))
		case "process":
			c = cmpStr(a.conn.Process, b.conn.Process)
		case "remote":
			c = cmpStr(a.conn.Remote, b.conn.Remote)
		default: // "closed": most recently closed first
			switch {
			case a.closedAt.After(b.closedAt):
				c = -1
			case a.closedAt.Before(b.closedAt):
				c = 1
			}
		}
		if c == 0 {
			c = cmpStr(a.conn.Local, b.conn.Local)
		}
		if m.closedSortDesc {
			c = -c
		}
		return c < 0
	})
}

// closedColumns is the closed-connections view's layout.
func (m Model) closedColumns() colSpecs {
	w := m.connsVP.Width
	if w <= 0 {
		w = 140
	}
	w -= gutterWidth

	colProto := 4
	colSvc := 9
	colLife := 7
	colClosed := 7
	colBytes := 5
	colPID := 7

	fixed := colProto + 2 + colSvc + 2 + colLife + 2 + colClosed + 2 + 2*(colBytes+2) + colPID + 2
	rest := w - fixed
	if rest < 30 {
		rest = 30
	}
	colLocal := max(16, min(28, rest/3))
	colRemote := max(18, min(32, rest/3))
	colProc := rest - colLocal - colRemote - 4
	if colProc < 8 {
		colProc = 8
	}

	return colSpecs{
		{key: "proto", title: "PR", width: colProto},
		{key: "local", title: "LOCAL", width: colLocal},
		{key: "remote", title: "REMOTE", width: colRemote, sort: "remote"},
		{key: "svc", title: "SVC", width: colSvc},
		{key: "life", title: "LIFE", width: colLife, sort: "lifetime"},
		{key: "closed", title: "CLOSED", width: colClosed, sort: "closed"},
		{key: "rx", title: "RX∑", width: colBytes, sort: "bytes"},
		{key: "tx", title: "TX∑", width: colBytes, sort: "bytes"},
		{key: "pid", title: "PID", width: colPID},
		{key: "process", title: "PROCESS", width: colProc, sort: "process"},
	}
}

func (m Model) renderConnsClosed() string {
	var b strings.Builder

	w := m.connsVP.Width
	if w <= 0 {
		w = 140
	}
	w -= gutterWidth

	cols := m.closedColumns()
	colProto := cols.width("proto")
	colLocal := cols.width("local")
	colRemote := cols.width("remote")
	colSvc := cols.width("svc")
	colLife := cols.width("life")
	colClosed := cols.width("closed")
	colBytes := cols.width("rx")
	colPID := cols.width("pid")
	colProc := cols.width("process")

	b.WriteString("Recently closed connections\n")
	sub := fmt.Sprintf("↑↓ select • s/S sort: %s • y copy • z back to active • keeping the last %d",
		m.sortLabel(), closedConnLimit)
	if m.anyFilterActive() {
		sub += " • filter: " + m.filterLabel()
	}
	b.WriteString(sub + "\n\n")

	b.WriteString(gutter(false) + cols.header(m.sortKey(), m.sortDesc()) + "\n")
	b.WriteString(gutter(false) + strings.Repeat("─", min(w, cols.span())) + "\n")

	rows := m.closedRows()
	if len(rows) == 0 {
		b.WriteString("No connections have closed yet…\n")
		return b.String()
	}

	q := newMatcher(m.connsQuery)
	now := time.Now()

	for i, cc := range rows {
		c := cc.conn
		proc := c.Process
		if proc == "" {
			proc = "-"
		}

		remoteCell := padRight(trunc(m.dnsDisplay(c.Remote), colRemote), colRemote)
		var remote string
		switch {
		case m.isWatched(c.Remote):
			remote = watchStyle.Render(remoteCell)
		case q.empty():
			remote = scopeStyle(probe.IPScope(stripPort(c.Remote))).Render(remoteCell)
		default:
			remote = q.highlight(remoteCell)
		}

		rx, tx := totalCells(cc.totals, colBytes)

		b.WriteString(gutter(i == m.connsCursor) + fmt.Sprintf("%s  %s  %s  %s  %s  %s  %s  %s  %s  %s\n",
			padRight(trunc(c.Proto, colProto), colProto),
			q.highlight(padRight(trunc(c.Local, colLocal), colLocal)),
			remote,
			q.highlight(padRight(trunc(probe.ServiceName(portOf(c.Remote), c.Proto), colSvc), colSvc)),
			padRight(trunc(cc.lifetimeLabel(), colLife), colLife),
			padRight(trunc(formatAge(now, cc.closedAt), colClosed), colClosed),
			rx, tx,
			padRight(fmt.Sprintf("%d", c.PID), colPID),
			q.highlight(trunc(proc, colProc)),
		))
	}

	return b.String()
}
