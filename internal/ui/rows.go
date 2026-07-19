package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// tableHeaderLines is the number of fixed lines each table tab renders before
// its first data row (title, hint, blank, column header, separator).
const tableHeaderLines = 5

// gutterWidth is the visible width of the row-cursor gutter (see gutter). Table
// renderers must reserve it out of the viewport width so rows don't overrun the
// viewport and get hard-clipped on the right.
const gutterWidth = 2

// gutter renders the 2-column left margin that carries the row cursor.
func gutter(selected bool) string {
	if selected {
		return cursorStyle.Render("▸") + " "
	}
	return "  "
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// connKey identifies a connection by its proto + local + remote tuple, used to
// diff consecutive enumerations for new/closed detection.
func connKey(c probe.Conn) string {
	return c.Proto + "|" + c.Local + "|" + c.Remote
}

// portKey identifies a listening socket by its proto + bind address, used to
// detect newly-appeared listeners between enumerations.
func portKey(p probe.ListenPort) string {
	return p.Proto + "|" + p.Local
}

// securityCounts summarizes the current security posture across all (unfiltered)
// listeners and connections: off-host-reachable listeners, connections to public
// remotes, and connections matching the watchlist.
func (m Model) securityCounts() (exposed, public, watch int) {
	for _, p := range m.ports {
		if probe.ListenerExposure(stripPort(p.Local)) == "exposed" {
			exposed++
		}
	}
	for _, c := range m.conns {
		if probe.IPScope(stripPort(c.Remote)) == "public" {
			public++
		}
		if m.isWatched(c.Remote) {
			watch++
		}
	}
	return
}

// isTeardownState reports whether a TCP state is a closing/teardown state that
// the "active" connection filter hides.
func isTeardownState(state string) bool {
	switch state {
	case "TIME_WAIT", "CLOSE_WAIT", "CLOSE", "CLOSING", "FIN_WAIT1", "FIN_WAIT2", "LAST_ACK":
		return true
	}
	return false
}

// passProtoFilter / passIPVerFilter apply the protocol and IP-version quick
// filters shared by the ports and connections tabs.
func (m Model) passProtoFilter(proto string) bool {
	switch m.protoFilter {
	case 1:
		return proto == "tcp"
	case 2:
		return proto == "udp"
	}
	return true
}

func (m Model) passIPVerFilter(hostport string) bool {
	if m.ipVerFilter == 0 {
		return true
	}
	ip := stripPort(hostport)
	isV6 := strings.Contains(ip, ":")
	if m.ipVerFilter == 1 {
		return !isV6 // IPv4
	}
	return isV6 // IPv6
}

// passConnFilters combines the protocol, IP-version and state quick filters for
// a connection row (used by both the flat and grouped connection views).
func (m Model) passConnFilters(c probe.Conn) bool {
	if !m.passProtoFilter(c.Proto) || !m.passIPVerFilter(c.Remote) {
		return false
	}
	if m.publicOnly && probe.IPScope(stripPort(c.Remote)) != "public" {
		return false
	}
	switch m.connStateFilter {
	case 1:
		return c.State == "ESTABLISHED"
	case 2:
		return !isTeardownState(c.State)
	}
	return true
}

// isWatched reports whether a remote "ip:port" matches the configured watchlist.
func (m Model) isWatched(remote string) bool {
	return m.isWatchedIP(stripPort(remote))
}

// isWatchedIP matches a bare IP (no port) against the watchlist, testing the raw
// IP and (when known) its resolved hostname.
func (m Model) isWatchedIP(ip string) bool {
	if m.watch == nil {
		return false
	}
	host := ""
	if m.dnsCache != nil {
		host = m.dnsCache[ip]
	}
	return m.watch.match(ip, host)
}

// anyFilterActive reports whether a non-default quick filter is set (used to
// decide whether to show the filter summary line).
func (m Model) anyFilterActive() bool {
	return m.connStateFilter != 0 || m.protoFilter != 0 || m.ipVerFilter != 0 || m.publicOnly
}

// filterLabel describes the active quick filters, e.g. "tcp · established".
func (m Model) filterLabel() string {
	var parts []string
	switch m.protoFilter {
	case 1:
		parts = append(parts, "tcp")
	case 2:
		parts = append(parts, "udp")
	}
	switch m.ipVerFilter {
	case 1:
		parts = append(parts, "ipv4")
	case 2:
		parts = append(parts, "ipv6")
	}
	switch m.connStateFilter {
	case 1:
		parts = append(parts, "established")
	case 2:
		parts = append(parts, "active")
	}
	if m.publicOnly {
		parts = append(parts, "public")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " · ")
}

// isNoiseIface reports whether an interface is "noise" that the hide-noise
// toggle filters out: anything down, plus loopback/docker/bridge/veth.
func isNoiseIface(ii probe.IfaceInfo) bool {
	if !ii.IsUp {
		return true
	}
	switch ii.Kind {
	case probe.IfaceLoopback, probe.IfaceDockerBridge, probe.IfaceLinuxBridge, probe.IfaceVeth:
		return true
	}
	return false
}

// The filtered* methods produce the row set each table renders: the raw probe
// data with the active search query and quick filters applied, then sorted.
// They are shared by the renderers, the row cursor and the copy/export paths.
func (m Model) filteredPorts() []probe.ListenPort {
	q := m.portsQuery
	out := make([]probe.ListenPort, 0, len(m.ports))
	for _, p := range m.ports {
		if !m.passProtoFilter(p.Proto) || !m.passIPVerFilter(p.Local) {
			continue
		}
		proc := p.Process
		if proc == "" {
			proc = "-"
		}
		local := p.Local
		if local == ":" || local == "0.0.0.0:0" {
			local = "-"
		}
		svc := probe.ServiceName(portOf(p.Local), p.Proto)
		if q != "" && !(containsFold(local, q) || containsFold(proc, q) || containsFold(p.Proto, q) || containsFold(svc, q)) {
			continue
		}
		out = append(out, p)
	}
	m.sortPorts(out)
	return out
}

func (m Model) filteredProcs() []probe.ProcNet {
	q := m.procsQuery
	out := make([]probe.ProcNet, 0, len(m.procs))
	for _, p := range m.procs {
		name := p.Name
		if name == "" {
			name = "-"
		}
		if q != "" && !(containsFold(name, q) || containsFold(fmt.Sprintf("%d", p.PID), q)) {
			continue
		}
		out = append(out, p)
	}
	m.sortProcs(out)
	return out
}

func (m Model) filteredConns() []probe.Conn {
	q := m.connsQuery
	out := make([]probe.Conn, 0, len(m.conns))
	for _, c := range m.conns {
		if !m.passConnFilters(c) {
			continue
		}
		proc := c.Process
		if proc == "" {
			proc = "-"
		}
		remoteDisp := m.dnsDisplay(c.Remote)
		svc := probe.ServiceName(portOf(c.Remote), c.Proto)
		if q != "" && !(containsFold(c.Local, q) || containsFold(c.Remote, q) || containsFold(remoteDisp, q) || containsFold(proc, q) || containsFold(c.State, q) || containsFold(c.Proto, q) || containsFold(svc, q)) {
			continue
		}
		out = append(out, c)
	}
	m.sortConns(out)
	return out
}

func (m Model) filteredGroups() []*connGroup {
	groups := m.buildConnGroups()
	q := m.connsQuery
	if q == "" {
		m.sortGroups(groups)
		return groups
	}
	out := make([]*connGroup, 0, len(groups))
	for _, g := range groups {
		host := g.ip
		if m.connsRevDNS && m.dnsCache != nil {
			if name, ok := m.dnsCache[g.ip]; ok && name != "" {
				host = name
			}
		}
		states := formatStates(g.states)
		procs := strings.Join(g.procs, ",")
		if containsFold(host, q) || containsFold(g.ip, q) || containsFold(states, q) || containsFold(procs, q) {
			out = append(out, g)
		}
	}
	m.sortGroups(out)
	return out
}

func (m Model) connsRowCount() int {
	if m.connsGroup {
		return len(m.filteredGroups())
	}
	return len(m.filteredConns())
}

func (m Model) activeRowCount() int {
	switch m.activeTab {
	case tabPorts:
		return len(m.filteredPorts())
	case tabProcs:
		return len(m.filteredProcs())
	case tabConns:
		return m.connsRowCount()
	}
	return 0
}

// activeTableState returns pointers to the cursor and viewport of the active
// table tab, or (nil, nil) on a non-table tab.
func (m *Model) activeTableState() (*int, *viewport.Model) {
	switch m.activeTab {
	case tabPorts:
		return &m.portsCursor, &m.portsVP
	case tabProcs:
		return &m.procsCursor, &m.procsVP
	case tabConns:
		return &m.connsCursor, &m.connsVP
	}
	return nil, nil
}

// mouseRowIndex maps a screen Y coordinate to a data-row index within the
// active table's viewport. The layout above the viewport is fixed: header (1) +
// box top border (1) + search line (1) + blank (1) = 4 rows, then the table's
// own header block (tableHeaderLines) scrolls inside the viewport.
func mouseRowIndex(vp *viewport.Model, y int) (int, bool) {
	const topOffset = 4
	if vp.Height <= 0 || y < topOffset || y >= topOffset+vp.Height {
		return 0, false
	}
	textLine := (y - topOffset) + vp.YOffset
	if textLine < tableHeaderLines {
		return 0, false
	}
	return textLine - tableHeaderLines, true
}

// tableNav handles navigation for the table tabs. It returns handled=false only
// for tabs it does not manage. Navigation keys move the row cursor (and scroll
// the viewport to keep it visible); a left click selects the clicked row; any
// other message is forwarded to the active viewport so wheel scrolling works.
func (m *Model) tableNav(msg tea.Msg) (bool, tea.Cmd) {
	cursor, vp := m.activeTableState()
	if cursor == nil {
		return false, nil
	}

	if mm, ok := msg.(tea.MouseMsg); ok {
		if mm.Action == tea.MouseActionPress && mm.Button == tea.MouseButtonLeft {
			if i, ok := mouseRowIndex(vp, mm.Y); ok {
				if count := m.activeRowCount(); count > 0 && i < count {
					*cursor = i
					m.rerenderActiveTable()
					ensureCursorVisible(vp, *cursor)
				}
			}
			return true, nil
		}
		// Wheel / other mouse events: let the viewport scroll.
		return m.passToActiveVP(msg)
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m.passToActiveVP(msg)
	}

	count := m.activeRowCount()
	page := max(1, vp.Height-1)

	switch km.String() {
	case "up", "k":
		*cursor--
	case "down", "j":
		*cursor++
	case "pgup":
		*cursor -= page
	case "pgdown":
		*cursor += page
	case "home":
		*cursor = 0
	case "end":
		*cursor = count - 1
	default:
		return m.passToActiveVP(msg)
	}

	if count <= 0 {
		*cursor = 0
	} else {
		*cursor = clampInt(*cursor, 0, count-1)
	}

	m.rerenderActiveTable()
	ensureCursorVisible(vp, *cursor)
	return true, nil
}

func (m *Model) passToActiveVP(msg tea.Msg) (bool, tea.Cmd) {
	var cmd tea.Cmd
	switch m.activeTab {
	case tabPorts:
		m.portsVP, cmd = m.portsVP.Update(msg)
	case tabProcs:
		m.procsVP, cmd = m.procsVP.Update(msg)
	case tabConns:
		m.connsVP, cmd = m.connsVP.Update(msg)
	default:
		return false, nil
	}
	return true, cmd
}

func (m *Model) rerenderActiveTable() {
	switch m.activeTab {
	case tabPorts:
		m.portsText = hardClipLinesToWidth(m.renderPortsText(), m.portsVP.Width)
		m.portsVP.SetContent(m.portsText)
	case tabProcs:
		m.procsText = hardClipLinesToWidth(m.renderProcsText(), m.procsVP.Width)
		m.procsVP.SetContent(m.procsText)
	case tabConns:
		m.connsText = hardClipLinesToWidth(m.renderConnsText(), m.connsVP.Width)
		m.connsVP.SetContent(m.connsText)
	}
}

// scrollActiveTableTop returns the active table's viewport to the top. Used when
// the row set is reshaped (sort change, filter apply/clear, view toggle) and the
// cursor jumps back to the first row, so the header and top rows are shown
// instead of leaving the viewport scrolled past an off-screen cursor.
func (m *Model) scrollActiveTableTop() {
	if _, vp := m.activeTableState(); vp != nil {
		vp.SetYOffset(0)
	}
}

// resetTableCursor moves the active table's row cursor back to the first row.
func (m *Model) resetTableCursor() {
	if cursor, _ := m.activeTableState(); cursor != nil {
		*cursor = 0
	}
}

// applyFilterChange re-renders the active table after a quick-filter toggle:
// reset the cursor, rerender, scroll to top and report the active filters.
func (m *Model) applyFilterChange() {
	m.resetTableCursor()
	m.rerenderActiveTable()
	m.scrollActiveTableTop()
	m.status = "Filter: " + m.filterLabel()
}

func ensureCursorVisible(vp *viewport.Model, cursor int) {
	h := vp.Height
	if h <= 0 {
		return
	}
	line := tableHeaderLines + cursor
	top := vp.YOffset
	switch {
	case line < top:
		vp.SetYOffset(line)
	case line >= top+h:
		vp.SetYOffset(line - h + 1)
	}
}

// copyTarget returns the tab-separated text for the currently highlighted row
// (or, on the Overview/Interfaces tabs, the selected interface) plus a short
// label for the status line. ok is false when there is nothing to copy.
func (m Model) copyTarget() (text, what string, ok bool) {
	switch m.activeTab {
	case tabOverview, tabIfaces:
		for k := range m.lastSnap.Ifaces {
			ii := &m.lastSnap.Ifaces[k]
			if ii.Name != m.selectedIface {
				continue
			}
			state := "DOWN"
			if ii.IsUp {
				state = "UP"
			}
			text = fmt.Sprintf("%s\t%s\t%s\tMTU=%d\tMAC=%s\taddrs=[%s]\tRX=%s (total %s)\tTX=%s (total %s)",
				ii.Name, state, ii.Kind.Label(), ii.MTU, ii.Hardware, strings.Join(ii.Addrs, ", "),
				probe.HumanBytesPerSec(ii.RxBps), probe.HumanBytes(ii.RxTotal),
				probe.HumanBytesPerSec(ii.TxBps), probe.HumanBytes(ii.TxTotal))
			return text, "interface " + ii.Name, true
		}
		return "", "", false

	case tabPorts:
		rows := m.filteredPorts()
		if m.portsCursor < 0 || m.portsCursor >= len(rows) {
			return "", "", false
		}
		p := rows[m.portsCursor]
		proc := p.Process
		if proc == "" {
			proc = "-"
		}
		local := p.Local
		if local == ":" || local == "0.0.0.0:0" {
			local = "-"
		}
		return fmt.Sprintf("%s\t%s\t%d\t%s", p.Proto, local, p.PID, proc), "port row", true

	case tabProcs:
		rows := m.filteredProcs()
		if m.procsCursor < 0 || m.procsCursor >= len(rows) {
			return "", "", false
		}
		p := rows[m.procsCursor]
		name := p.Name
		if name == "" {
			name = "-"
		}
		return fmt.Sprintf("%d\t%s\tconns=%d\tlisten=%d", p.PID, name, p.ConnCount, p.ListenCount), "process row", true

	case tabConns:
		if m.connsGroup {
			rows := m.filteredGroups()
			if m.connsCursor < 0 || m.connsCursor >= len(rows) {
				return "", "", false
			}
			g := rows[m.connsCursor]
			host := g.ip
			if m.connsRevDNS && m.dnsCache != nil {
				if name, ok := m.dnsCache[g.ip]; ok && name != "" {
					host = name
				}
			}
			return fmt.Sprintf("%s\tconns=%d\t%s\t%s", host, g.count, formatStates(g.states), strings.Join(g.procs, ",")), "connection group", true
		}
		rows := m.filteredConns()
		if m.connsCursor < 0 || m.connsCursor >= len(rows) {
			return "", "", false
		}
		c := rows[m.connsCursor]
		proc := c.Process
		if proc == "" {
			proc = "-"
		}
		return fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%s", c.Proto, c.Local, m.dnsDisplay(c.Remote), c.State, c.PID, proc), "connection row", true
	}
	return "", "", false
}

// rebuildIfaceList repopulates the interfaces list from the latest snapshot,
// applying the hide-noise filter and preserving the current selection where
// possible (falling back to the first row when the selected interface is gone).
func (m *Model) rebuildIfaceList() {
	prevSel := m.selectedIface
	prevIndex := m.ifaceList.Index()

	innerW := m.ifaceList.Width()
	if innerW <= 0 {
		innerW = 30
	}
	descMax := max(10, innerW-6)

	items := make([]list.Item, 0, len(m.lastSnap.Ifaces))
	for _, ii := range m.lastSnap.Ifaces {
		if m.hideNoise && isNoiseIface(ii) {
			continue
		}
		desc := fmt.Sprintf(
			"%s  RX %s  TX %s",
			ii.Kind.Label(),
			probe.HumanBytesPerSec(ii.RxBps),
			probe.HumanBytesPerSec(ii.TxBps),
		)
		desc = trunc(desc, descMax)
		items = append(items, ifaceItem{name: ii.Name, desc: desc})
	}

	m.ifaceList.SetItems(items)

	switch {
	case len(items) == 0:
		m.selectedIface = ""
	case prevSel != "":
		found := false
		for i, it := range items {
			if it.(ifaceItem).name == prevSel {
				m.ifaceList.Select(i)
				found = true
				break
			}
		}
		if !found {
			m.ifaceList.Select(0)
			m.selectedIface = items[0].(ifaceItem).name
		}
	case prevIndex >= 0 && prevIndex < len(items):
		m.ifaceList.Select(prevIndex)
		m.selectedIface = items[prevIndex].(ifaceItem).name
	default:
		m.ifaceList.Select(0)
		m.selectedIface = items[0].(ifaceItem).name
	}
}
