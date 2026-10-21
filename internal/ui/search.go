package ui

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Search has two useful shapes. Filtering answers "show me only these"; it is
// the default because it is what you want when a table has three hundred rows.
// Highlighting answers "where are these, in context" — it keeps every row and
// walks the cursor between matches, which is what you want when the surrounding
// rows are the point. `\` switches between them.

// regexPrefix marks a query as a regular expression rather than a substring.
const regexPrefix = "re:"

// matcher tests rows against a query. A query starting with "re:" is compiled
// as a case-insensitive regular expression; anything else is a case-insensitive
// substring. An invalid expression matches nothing and reports why.
type matcher struct {
	query string
	re    *regexp.Regexp
	err   error
}

func newMatcher(query string) matcher {
	query = strings.TrimSpace(query)
	if !strings.HasPrefix(query, regexPrefix) {
		return matcher{query: query}
	}

	expr := strings.TrimSpace(strings.TrimPrefix(query, regexPrefix))
	if expr == "" {
		return matcher{query: query}
	}
	re, err := regexp.Compile("(?i)" + expr)
	if err != nil {
		return matcher{query: query, err: err}
	}
	return matcher{query: query, re: re}
}

// empty reports whether the matcher would accept everything.
func (m matcher) empty() bool { return m.query == "" }

// match reports whether any of the given fields matches.
func (m matcher) match(fields ...string) bool {
	if m.empty() {
		return true
	}
	if m.err != nil {
		return false // a broken expression matches nothing, rather than everything
	}
	for _, f := range fields {
		if m.re != nil {
			if m.re.MatchString(f) {
				return true
			}
			continue
		}
		if containsFold(f, m.query) {
			return true
		}
	}
	return false
}

// highlight marks the matching part of a cell. Regex matches are highlighted
// wherever they occur; substring matches reuse the fold-insensitive highlighter.
func (m matcher) highlight(s string) string {
	switch {
	case m.empty() || m.err != nil:
		return s
	case m.re == nil:
		return highlightFold(s, m.query)
	}

	// Same byte-offset caution as highlightFold: only splice when the offsets
	// the regexp reports line up with the string we are slicing.
	locs := m.re.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var out strings.Builder
	last := 0
	for _, loc := range locs {
		if loc[0] < last {
			continue
		}
		out.WriteString(s[last:loc[0]])
		out.WriteString(hlStyle.Render(s[loc[0]:loc[1]]))
		last = loc[1]
	}
	out.WriteString(s[last:])
	return out.String()
}

// searchMode decides what an active query does to the table.
type searchMode int

const (
	searchFilter    searchMode = iota // hide non-matching rows
	searchHighlight                   // keep every row, mark the matches
)

func (s searchMode) String() string {
	if s == searchHighlight {
		return "highlight"
	}
	return "filter"
}

// activeQuery returns the query for the active tab.
func (m Model) activeQuery() string {
	switch m.activeTab {
	case tabPorts:
		return m.portsQuery
	case tabProcs:
		return m.procsQuery
	case tabConns:
		return m.connsQuery
	case tabEvents:
		return m.eventsQuery
	}
	return ""
}

// filterQuery is the query the row filters apply — empty in highlight mode, so
// every row survives and only the styling changes.
func (m Model) filterQuery(q string) string {
	if m.searchMode == searchHighlight {
		return ""
	}
	return q
}

// jumpToMatch moves the active tab's cursor to the next (or previous) row
// matching the query, wrapping at the ends. It is what makes highlight mode
// navigable; in filter mode every visible row already matches, so the cursor
// simply steps by one.
func (m *Model) jumpToMatch(forward bool) bool {
	count := m.activeRowCount()
	if count == 0 {
		return false
	}
	cursor, _ := m.activeTableState()
	if cursor == nil {
		return false
	}

	matches := m.matchingRows()
	if len(matches) == 0 {
		return false
	}

	step := 1
	if !forward {
		step = -1
	}
	for i := 1; i <= count; i++ {
		idx := ((*cursor+i*step)%count + count) % count
		if matches[idx] {
			*cursor = idx
			m.rerenderActiveTable()
			if _, vp := m.activeTableState(); vp != nil {
				ensureCursorVisible(vp, *cursor)
			}
			return true
		}
	}
	return false
}

// matchingRows reports, per displayed row, whether it matches the active query.
func (m Model) matchingRows() []bool {
	q := newMatcher(m.activeQuery())
	if q.empty() {
		return nil
	}

	switch m.activeTab {
	case tabPorts:
		rows := m.filteredPorts()
		out := make([]bool, len(rows))
		for i, p := range rows {
			out[i] = q.match(p.Local, p.Process, p.Proto, p.User, p.Unit,
				probe.ServiceName(portOf(p.Local), p.Proto))
		}
		return out

	case tabProcs:
		rows := m.filteredProcs()
		out := make([]bool, len(rows))
		for i, p := range rows {
			out[i] = q.match(p.Name, strconv.Itoa(int(p.PID)), p.User, p.Container, p.Unit, p.Cmdline)
		}
		return out

	case tabConns:
		if m.showClosed {
			rows := m.closedRows()
			out := make([]bool, len(rows))
			for i, cc := range rows {
				c := cc.conn
				out[i] = q.match(c.Local, c.Remote, m.dnsDisplay(c.Remote), c.Process, c.Proto, c.User)
			}
			return out
		}
		if m.connsGroup {
			rows := m.filteredGroups()
			out := make([]bool, len(rows))
			for i, g := range rows {
				out[i] = q.match(g.ip, strings.Join(g.procs, ","), formatStates(g.states))
			}
			return out
		}
		rows := m.filteredConns()
		out := make([]bool, len(rows))
		for i, c := range rows {
			out[i] = q.match(c.Local, c.Remote, m.dnsDisplay(c.Remote), c.Process, c.State, c.Proto, c.User)
		}
		return out

	case tabEvents:
		rows := m.eventRows()
		out := make([]bool, len(rows))
		for i, e := range rows {
			out[i] = q.match(e.Kind, e.Subject, e.Detail, e.Level.String())
		}
		return out
	}
	return nil
}

// searchStatus renders the line above a table describing the active query: what
// it is, whether it filters or highlights, and why it is not working if the
// expression failed to compile.
func (m Model) searchStatus(query string) string {
	mt := newMatcher(query)
	line := subtleStyle.Render(m.searchMode.String()+": ") + titleStyle.Render(query)
	if mt.err != nil {
		return line + "  " + errStyle.Render("invalid regex: "+mt.err.Error())
	}
	if mt.re != nil {
		line += subtleStyle.Render("  (regex)")
	}
	if m.searchMode == searchHighlight {
		line += subtleStyle.Render("  n/N jump")
	}
	return line + subtleStyle.Render("  (/ change · \\ mode · ctrl+u clear)")
}
