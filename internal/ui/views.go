package ui

import (
	"fmt"
	"strings"
)

// Half the keys in the tool exist to narrow what is on screen: a tab, a sort,
// a search, five quick filters and a handful of view toggles. Getting back to
// "the TLS connections leaving over the VPN, busiest first" meant pressing six
// of them in the right order, every session. All of that state already lives
// in the model and most of it is already persisted — naming a combination and
// recalling it is the missing half.

const (
	// maxSavedViews bounds the list: it is a shelf of working setups, not a
	// database, and the overlay has to stay readable.
	maxSavedViews = 32
	// maxViewNameLen keeps a name to something the overlay can render.
	maxViewNameLen = 40
)

// savedView is one named arrangement of the tables. It deliberately does not
// capture the drill: that one points at live PIDs, which mean nothing by the
// time the view is recalled.
type savedView struct {
	Name string `json:"name"`
	Tab  int    `json:"tab"`

	PortsQuery      string `json:"ports_query,omitempty"`
	ProcsQuery      string `json:"procs_query,omitempty"`
	ConnsQuery      string `json:"conns_query,omitempty"`
	EventsQuery     string `json:"events_query,omitempty"`
	SearchHighlight bool   `json:"search_highlight,omitempty"`

	PortsSortCol   int  `json:"ports_sort_col,omitempty"`
	PortsSortDesc  bool `json:"ports_sort_desc,omitempty"`
	ProcsSortCol   int  `json:"procs_sort_col,omitempty"`
	ProcsSortDesc  bool `json:"procs_sort_desc,omitempty"`
	ConnsSortCol   int  `json:"conns_sort_col,omitempty"`
	ConnsSortDesc  bool `json:"conns_sort_desc,omitempty"`
	GroupSortCol   int  `json:"group_sort_col,omitempty"`
	GroupSortDesc  bool `json:"group_sort_desc,omitempty"`
	ClosedSortCol  int  `json:"closed_sort_col,omitempty"`
	ClosedSortDesc bool `json:"closed_sort_desc,omitempty"`

	ProtoFilter     int    `json:"proto_filter,omitempty"`
	IPVerFilter     int    `json:"ipver_filter,omitempty"`
	ConnStateFilter int    `json:"conn_state_filter,omitempty"`
	PublicOnly      bool   `json:"public_only,omitempty"`
	OnlyMine        bool   `json:"only_mine,omitempty"`
	IfaceFilter     string `json:"iface_filter,omitempty"`
	EventLevel      int    `json:"event_level,omitempty"`

	ConnsGroup  bool `json:"conns_group,omitempty"`
	ShowClosed  bool `json:"show_closed,omitempty"`
	ShowTotals  bool `json:"show_totals,omitempty"`
	AggProcs    bool `json:"aggregate_processes,omitempty"`
	ProcTree    bool `json:"process_tree,omitempty"`
	HideNoise   bool `json:"hide_noise,omitempty"`
	ConnsRevDNS bool `json:"conns_rev_dns,omitempty"`
}

// captureView records everything about how the tables are currently arranged.
func (m Model) captureView(name string) savedView {
	return savedView{
		Name: name,
		Tab:  int(m.activeTab),

		PortsQuery:      m.portsQuery,
		ProcsQuery:      m.procsQuery,
		ConnsQuery:      m.connsQuery,
		EventsQuery:     m.eventsQuery,
		SearchHighlight: m.searchMode == searchHighlight,

		PortsSortCol:   m.portsSortCol,
		PortsSortDesc:  m.portsSortDesc,
		ProcsSortCol:   m.procsSortCol,
		ProcsSortDesc:  m.procsSortDesc,
		ConnsSortCol:   m.connsSortCol,
		ConnsSortDesc:  m.connsSortDesc,
		GroupSortCol:   m.groupSortCol,
		GroupSortDesc:  m.groupSortDesc,
		ClosedSortCol:  m.closedSortCol,
		ClosedSortDesc: m.closedSortDesc,

		ProtoFilter:     m.protoFilter,
		IPVerFilter:     m.ipVerFilter,
		ConnStateFilter: m.connStateFilter,
		PublicOnly:      m.publicOnly,
		OnlyMine:        m.onlyMine,
		IfaceFilter:     m.ifaceFilter,
		EventLevel:      m.eventLevelFilter,

		ConnsGroup:  m.connsGroup,
		ShowClosed:  m.showClosed,
		ShowTotals:  m.showTotals,
		AggProcs:    m.aggregateProcs,
		ProcTree:    m.procTree,
		HideNoise:   m.hideNoise,
		ConnsRevDNS: m.connsRevDNS,
	}
}

// applyView restores a saved arrangement. Every index is clamped, because a
// view is written to the config file and the config file is hand-editable.
func (m *Model) applyView(v savedView) {
	m.activeTab = tab(clampInt(v.Tab, 0, tabCount-1))

	m.portsQuery, m.procsQuery = v.PortsQuery, v.ProcsQuery
	m.connsQuery, m.eventsQuery = v.ConnsQuery, v.EventsQuery
	m.searchMode = searchFilter
	if v.SearchHighlight {
		m.searchMode = searchHighlight
	}

	m.portsSortCol = clampInt(v.PortsSortCol, 0, len(portsSortCols)-1)
	m.procsSortCol = clampInt(v.ProcsSortCol, 0, len(procsSortCols)-1)
	m.connsSortCol = clampInt(v.ConnsSortCol, 0, len(connsSortCols)-1)
	m.groupSortCol = clampInt(v.GroupSortCol, 0, len(groupSortCols)-1)
	m.closedSortCol = clampInt(v.ClosedSortCol, 0, len(closedSortCols)-1)
	m.portsSortDesc, m.procsSortDesc = v.PortsSortDesc, v.ProcsSortDesc
	m.connsSortDesc, m.groupSortDesc = v.ConnsSortDesc, v.GroupSortDesc
	m.closedSortDesc = v.ClosedSortDesc

	m.protoFilter = clampInt(v.ProtoFilter, 0, 2)
	m.ipVerFilter = clampInt(v.IPVerFilter, 0, 2)
	m.connStateFilter = clampInt(v.ConnStateFilter, 0, 2)
	m.eventLevelFilter = clampInt(v.EventLevel, 0, 2)
	m.publicOnly, m.onlyMine = v.PublicOnly, v.OnlyMine
	m.ifaceFilter = v.IfaceFilter

	m.connsGroup, m.showClosed = v.ConnsGroup, v.ShowClosed
	m.showTotals, m.aggregateProcs = v.ShowTotals, v.AggProcs
	// The tree and name-folding arrange the same rows two different ways.
	m.procTree = v.ProcTree && !v.AggProcs
	m.hideNoise, m.connsRevDNS = v.HideNoise, v.ConnsRevDNS

	// A view that narrows the rows has to leave the cursor somewhere real, and
	// a drill from before it points at a subject the view says nothing about.
	m.drill = nil
	m.portsCursor, m.procsCursor, m.connsCursor, m.eventsCursor = 0, 0, 0, 0

	m.rebuildIfaceList()
	m.ifaceDetailsText = hardClipLinesToWidth(
		m.renderIfaceDetailsText(m.ifaceDetailsVP.Width), m.ifaceDetailsVP.Width)
	m.ifaceDetailsVP.SetContent(m.ifaceDetailsText)
	m.rerenderActiveTable()
	m.scrollActiveTableTop()
}

// loadViews takes the views from a config file: unnamed entries are dropped
// (they could never be recalled), later duplicates of a name lose to the
// first, and the list is capped — all three are things a hand-edited file can
// contain and the overlay cannot show.
func loadViews(in []savedView) []savedView {
	out := make([]savedView, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		v.Name = strings.TrimSpace(v.Name)
		if v.Name == "" || seen[strings.ToLower(v.Name)] {
			continue
		}
		seen[strings.ToLower(v.Name)] = true
		out = append(out, v)
		if len(out) == maxSavedViews {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ViewNames lists the saved views, for the --view flag's error message.
func ViewNames() []string {
	c, state := loadConfig()
	if state != configLoaded {
		return nil
	}
	// The cleaned list, not the file's: an unnamed or duplicate entry is
	// dropped at load, and offering it as a name to type would be a lie.
	views := loadViews(c.Views)
	out := make([]string, 0, len(views))
	for _, v := range views {
		out = append(out, v.Name)
	}
	return out
}

// WithView starts the session in a saved view. ok is false for a name that is
// not saved, which the caller reports rather than silently ignoring.
func (m Model) WithView(name string) (Model, bool) {
	i := indexOfView(m.views, name)
	if i < 0 {
		return m, false
	}
	m.applyView(m.views[i])
	return m, true
}

// indexOfView finds a view by name, case-insensitively: it is typed on a
// command line as often as it is picked from the overlay.
func indexOfView(views []savedView, name string) int {
	name = strings.TrimSpace(name)
	for i, v := range views {
		if strings.EqualFold(v.Name, name) {
			return i
		}
	}
	return -1
}

// saveView stores the current arrangement under a name, replacing a view of
// the same name rather than accumulating duplicates.
func (m *Model) saveView(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "A view needs a name", false
	}
	name = trunc(name, maxViewNameLen)

	v := m.captureView(name)
	if i := indexOfView(m.views, name); i >= 0 {
		m.views[i] = v
		m.saveConfig()
		return "Replaced view " + name, true
	}
	if len(m.views) >= maxSavedViews {
		return fmt.Sprintf("Already holding %d views — delete one first", maxSavedViews), false
	}
	m.views = append(m.views, v)
	m.viewsCursor = len(m.views) - 1
	m.saveConfig()
	return "Saved view " + name, true
}

// deleteView forgets the view at an index.
func (m *Model) deleteView(i int) (string, bool) {
	if i < 0 || i >= len(m.views) {
		return "No view selected", false
	}
	name := m.views[i].Name
	m.views = append(m.views[:i:i], m.views[i+1:]...)
	m.viewsCursor = clampInt(m.viewsCursor, 0, len(m.views)-1)
	m.saveConfig()
	return "Deleted view " + name, true
}

// openViews arms the overlay.
func (m *Model) openViews() {
	m.showViews = true
	m.viewsNaming = false
	m.viewsInput.SetValue("")
	m.viewsCursor = clampInt(m.viewsCursor, 0, len(m.views)-1)
}

// viewsKey handles a keypress while the overlay is open. handled is false for
// keys the overlay does not claim, which close it.
func (m *Model) viewsKey(key string) (status string, handled bool) {
	if m.viewsNaming {
		switch key {
		case "enter":
			msg, _ := m.saveView(m.viewsInput.Value())
			m.viewsNaming = false
			m.viewsInput.SetValue("")
			m.viewsInput.Blur()
			return msg, true
		case "esc":
			m.viewsNaming = false
			m.viewsInput.SetValue("")
			m.viewsInput.Blur()
			return "Cancelled", true
		}
		return "", true // every other key belongs to the text input
	}

	switch key {
	case "up", "k":
		m.viewsCursor = clampInt(m.viewsCursor-1, 0, len(m.views)-1)
		return "", true
	case "down", "j":
		m.viewsCursor = clampInt(m.viewsCursor+1, 0, len(m.views)-1)
		return "", true
	case "n":
		m.viewsNaming = true
		m.viewsInput.SetValue("")
		m.viewsInput.Focus()
		return "", true
	case "d":
		msg, _ := m.deleteView(m.viewsCursor)
		return msg, true
	case "enter":
		if len(m.views) == 0 {
			return "No views saved yet — press n to save this one", true
		}
		v := m.views[clampInt(m.viewsCursor, 0, len(m.views)-1)]
		m.applyView(v)
		m.showViews = false
		return "View: " + v.Name, true
	case "esc", "q", "V":
		m.showViews = false
		return "", true
	}
	return "", false
}

// describeView is the one-line summary under a view's name: what it actually
// does, so a list of names is still readable a month later.
func describeView(v savedView) string {
	parts := []string{tabLabel(tab(clampInt(v.Tab, 0, tabCount-1)))}

	switch v.ProtoFilter {
	case 1:
		parts = append(parts, "tcp")
	case 2:
		parts = append(parts, "udp")
	}
	switch v.IPVerFilter {
	case 1:
		parts = append(parts, "ipv4")
	case 2:
		parts = append(parts, "ipv6")
	}
	switch v.ConnStateFilter {
	case 1:
		parts = append(parts, "established")
	case 2:
		parts = append(parts, "active")
	}
	if v.PublicOnly {
		parts = append(parts, "public")
	}
	if v.OnlyMine {
		parts = append(parts, "mine")
	}
	if v.IfaceFilter != "" {
		parts = append(parts, v.IfaceFilter)
	}
	if v.ConnsGroup {
		parts = append(parts, "grouped")
	}
	if v.ShowClosed {
		parts = append(parts, "closed")
	}
	if v.AggProcs {
		parts = append(parts, "by name")
	}
	if v.ProcTree {
		parts = append(parts, "tree")
	}
	if v.ShowTotals {
		parts = append(parts, "totals")
	}
	if v.HideNoise {
		parts = append(parts, "hide noise")
	}
	if q := viewQuery(v); q != "" {
		parts = append(parts, "/"+q)
	}
	return strings.Join(parts, " · ")
}

// viewQuery is the search the view's own tab carries, which is the part of it
// worth repeating in the summary.
func viewQuery(v savedView) string {
	switch tab(clampInt(v.Tab, 0, tabCount-1)) {
	case tabPorts:
		return v.PortsQuery
	case tabProcs:
		return v.ProcsQuery
	case tabConns:
		return v.ConnsQuery
	case tabEvents:
		return v.EventsQuery
	}
	return ""
}

// visibleViewRange is the slice of the list that fits, kept around the cursor
// so the highlighted view is always one of the ones on screen.
func (m Model) visibleViewRange() (int, int) {
	// Chrome: the title and its blank line, the blank line and footer at the
	// bottom, and the two "N more" markers the window itself may need.
	rows := m.bodyHeight() - 6
	if m.viewsNaming {
		rows -= 4 // the name field and its two hint lines, plus their blank
	}
	if rows < 1 {
		rows = 1
	}
	if len(m.views) <= rows {
		return 0, len(m.views)
	}

	first := m.viewsCursor - rows/2
	if first < 0 {
		first = 0
	}
	if first > len(m.views)-rows {
		first = len(m.views) - rows
	}
	return first, first + rows
}

// renderViews draws the overlay.
func (m Model) renderViews() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Views") + "  " +
		subtleStyle.Render("↑↓ move · enter apply · n save this one · d delete · esc close") + "\n\n")

	if len(m.views) == 0 {
		b.WriteString(subtleStyle.Render(
			"No views saved yet.\n\n"+
				"A view is the whole arrangement of the tables — the tab, the sort, the search,\n"+
				"the quick filters and the toggles — under a name. Set the screen up the way you\n"+
				"want it, press n, and it comes back with one keypress or with --view NAME.") + "\n")
	}

	// The list is capped at maxSavedViews, which is taller than a short
	// terminal — so it is windowed around the cursor rather than drawn in
	// full and allowed to push its own header off the screen.
	first, last := m.visibleViewRange()
	if first > 0 {
		b.WriteString("  " + subtleStyle.Render(fmt.Sprintf("↑ %d more", first)) + "\n")
	}
	for i := first; i < last; i++ {
		v := m.views[i]
		cursor := "  "
		name := padRight(trunc(v.Name, maxViewNameLen), 24)
		if i == m.viewsCursor {
			cursor = cursorStyle.Render("▸ ")
			name = selectedStyle.Render(name)
		}
		b.WriteString(cursor + name + " " + subtleStyle.Render(describeView(v)) + "\n")
	}
	if last < len(m.views) {
		b.WriteString("  " + subtleStyle.Render(fmt.Sprintf("↓ %d more", len(m.views)-last)) + "\n")
	}

	if m.viewsNaming {
		b.WriteString("\n" + titleStyle.Render("Save the current view as") + "\n")
		b.WriteString("  " + m.viewsInput.View() + "\n")
		b.WriteString("  " + subtleStyle.Render("enter saves · esc cancels · an existing name is replaced") + "\n")
	}

	b.WriteString("\n" + subtleStyle.Render(fmt.Sprintf(
		"%d of %d saved, in %s.", len(m.views), maxSavedViews, configFileLabel())) + "\n")
	return boxStyle.Width(min(m.w-2, 100)).Height(m.bodyHeight()).Render(b.String())
}
