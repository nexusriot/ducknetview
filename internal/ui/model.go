package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nexusriot/ducknetview/internal/probe"
)

type tab int

const (
	tabOverview tab = iota
	tabIfaces
	tabPorts
	tabProcs
)

type tickMsg time.Time

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type ifaceItem struct {
	name string
	desc string
}

func (i ifaceItem) Title() string       { return i.name }
func (i ifaceItem) Description() string { return i.desc }
func (i ifaceItem) FilterValue() string { return i.name }

type Model struct {
	w, h int

	activeTab tab

	netSampler *probe.NetSampler

	lastSnap probe.NetSnapshot
	err      error

	// Interfaces list + selection
	ifaceList      list.Model
	selectedIface  string
	rxHist, txHist []float64

	// Ports / procs data
	ports []probe.ListenPort
	procs []probe.ProcNet

	// Viewports for scrolling
	portsVP   viewport.Model
	portsText string

	procsVP   viewport.Model
	procsText string

	ifaceDetailsVP   viewport.Model
	ifaceDetailsText string

	portsSearch    textinput.Model
	portsSearching bool
	portsQuery     string

	procsSearch    textinput.Model
	procsSearching bool
	procsQuery     string
}

func NewModel() Model {
	ls := list.New([]list.Item{}, list.NewDefaultDelegate(), 30, 10)
	ls.Title = "Interfaces"
	ls.SetShowHelp(false)

	// viewports (sizes are set on WindowSizeMsg)
	pvp := viewport.New(0, 0)
	kvp := viewport.New(0, 0)
	dvp := viewport.New(0, 0)

	ps := textinput.New()
	ps.Placeholder = "search port / address / process"
	ps.Prompt = "/ "
	ps.CharLimit = 64

	qs := textinput.New()
	qs.Placeholder = "search process name"
	qs.Prompt = "/ "
	qs.CharLimit = 64

	return Model{
		activeTab:  tabOverview,
		netSampler: probe.NewNetSampler(),

		ifaceList: ls,

		portsVP:        pvp,
		procsVP:        kvp,
		ifaceDetailsVP: dvp,
		portsSearch:    ps,
		procsSearch:    qs,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.refreshCmd(),
		fetchPortsCmd(),
		fetchProcsCmd(),
		tickEvery(1*time.Second),
	)
}

func (m Model) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		snap, err := m.netSampler.Sample()
		if err != nil {
			return errMsg{err}
		}
		return snapMsg(snap)
	}
}

type errMsg struct{ error }
type snapMsg probe.NetSnapshot
type portsMsg []probe.ListenPort
type procsMsg []probe.ProcNet

func fetchPortsCmd() tea.Cmd {
	return func() tea.Msg {
		ports, err := probe.ListListening()
		if err != nil {
			return errMsg{err}
		}
		return portsMsg(ports)
	}
}

func fetchProcsCmd() tea.Cmd {
	return func() tea.Msg {
		procs, err := probe.TopProcsByConnections(80)
		if err != nil {
			return errMsg{err}
		}
		return procsMsg(procs)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height

		// Layout sizing
		leftW := max(26, m.w/3)
		bodyH := max(8, m.h-6)

		m.ifaceList.SetSize(leftW, bodyH)

		// Ports viewport inside a bordered box with padding(0,1) => subtract ~2
		portsW := min(m.w-2, 120)
		portsH := bodyH
		m.portsVP.Width = max(10, portsW-2)
		m.portsVP.Height = max(5, portsH-2)

		// Procs viewport
		procsW := min(m.w-2, 120)
		procsH := bodyH
		m.procsVP.Width = max(10, procsW-2)
		m.procsVP.Height = max(5, procsH-2)

		// Iface details viewport (right panel)
		rightW := m.w - leftW - 3
		m.ifaceDetailsVP.Width = max(10, rightW-2)
		m.ifaceDetailsVP.Height = max(5, bodyH-2)

		return m, nil

	case tickMsg:
		// refresh snapshot every second
		cmds := []tea.Cmd{m.refreshCmd(), tickEvery(1 * time.Second)}

		// refresh ports & procs every 5 seconds
		if time.Now().Unix()%5 == 0 {
			cmds = append(cmds, fetchPortsCmd(), fetchProcsCmd())
		}
		return m, tea.Batch(cmds...)

	case snapMsg:
		m.lastSnap = probe.NetSnapshot(msg)
		m.err = nil

		// build interface list
		items := make([]list.Item, 0, len(m.lastSnap.Ifaces))
		for _, ii := range m.lastSnap.Ifaces {
			desc := fmt.Sprintf("MAC %s  RX %s  TX %s",
				ii.Hardware,
				probe.HumanBytesPerSec(ii.RxBps),
				probe.HumanBytesPerSec(ii.TxBps),
			)
			items = append(items, ifaceItem{name: ii.Name, desc: desc})
		}
		m.ifaceList.SetItems(items)

		// default selected iface
		if m.selectedIface == "" && len(m.lastSnap.Ifaces) > 0 {
			m.selectedIface = m.lastSnap.Ifaces[0].Name
		}

		// Update hist for currently selected iface
		for _, ii := range m.lastSnap.Ifaces {
			if ii.Name == m.selectedIface {
				m.rxHist = append(m.rxHist, ii.RxBps)
				m.txHist = append(m.txHist, ii.TxBps)

				maxHist := max(30, min(200, m.w/2))
				m.rxHist = probe.ClampHistory(m.rxHist, maxHist)
				m.txHist = probe.ClampHistory(m.txHist, maxHist)
				break
			}
		}

		// update iface details viewport content
		m.ifaceDetailsText = m.renderIfaceDetailsText()
		m.ifaceDetailsVP.SetContent(m.ifaceDetailsText)

		return m, nil

	case portsMsg:
		m.ports = []probe.ListenPort(msg)
		m.portsText = m.renderPortsText()
		m.portsVP.SetContent(m.portsText)
		return m, nil

	case procsMsg:
		m.procs = []probe.ProcNet(msg)
		m.procsText = m.renderProcsText()
		m.procsVP.SetContent(m.procsText)
		return m, nil

	case errMsg:
		m.err = msg.error
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			m.activeTab = (m.activeTab + 1) % 4
			return m, nil
		case "shift+tab":
			m.activeTab = (m.activeTab + 3) % 4
			return m, nil
		case "right":
			m.activeTab = (m.activeTab + 1) % 4
			return m, nil
		case "left":
			m.activeTab = (m.activeTab + 3) % 4
			return m, nil
		case "/":
			if m.activeTab == tabPorts {
				m.portsSearching = true
				m.portsSearch.Focus()
				m.portsSearch.SetValue(m.portsQuery)
				return m, nil
			}
			if m.activeTab == tabProcs {
				m.procsSearching = true
				m.procsSearch.Focus()
				m.procsSearch.SetValue(m.procsQuery)
				return m, nil
			}

		}
	}

	if m.activeTab == tabIfaces {
		var cmd tea.Cmd
		m.ifaceList, cmd = m.ifaceList.Update(msg)

		// Auto-follow selection
		if it, ok := m.ifaceList.SelectedItem().(ifaceItem); ok {
			if m.selectedIface != it.name {
				m.selectedIface = it.name
				m.rxHist, m.txHist = nil, nil

				m.ifaceDetailsText = m.renderIfaceDetailsText()
				m.ifaceDetailsVP.SetContent(m.ifaceDetailsText)
			}
		}

		// Allow scrolling inside right details panel (if content long)
		var cmd2 tea.Cmd
		m.ifaceDetailsVP, cmd2 = m.ifaceDetailsVP.Update(msg)

		return m, tea.Batch(cmd, cmd2)
	}

	if m.activeTab == tabPorts && m.portsSearching {
		var cmd tea.Cmd
		m.portsSearch, cmd = m.portsSearch.Update(msg)

		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "enter":
				m.portsQuery = strings.TrimSpace(m.portsSearch.Value())
				m.portsSearching = false
				m.portsSearch.Blur()
				m.portsText = m.renderPortsText() // re-render with filter+highlight
				m.portsVP.SetContent(m.portsText)
				return m, nil
			case "esc":
				m.portsSearching = false
				m.portsSearch.Blur()
				return m, nil
			case "ctrl+u":
				m.portsSearch.SetValue("")
				return m, cmd
			}
		}
		return m, cmd
	}

	if m.activeTab == tabProcs && m.procsSearching {
		var cmd tea.Cmd
		m.procsSearch, cmd = m.procsSearch.Update(msg)

		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "enter":
				m.procsQuery = strings.TrimSpace(m.procsSearch.Value())
				m.procsSearching = false
				m.procsSearch.Blur()
				m.procsText = m.renderProcsText()
				m.procsVP.SetContent(m.procsText)
				return m, nil
			case "esc":
				m.procsSearching = false
				m.procsSearch.Blur()
				return m, nil
			case "ctrl+u":
				m.procsSearch.SetValue("")
				return m, cmd
			}
		}
		return m, cmd
	}

	// Ports tab: scroll via viewport
	if m.activeTab == tabPorts {
		var cmd tea.Cmd
		m.portsVP, cmd = m.portsVP.Update(msg)
		return m, cmd
	}

	// Procs tab: scroll via viewport
	if m.activeTab == tabProcs {
		var cmd tea.Cmd
		m.procsVP, cmd = m.procsVP.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) View() string {
	header := m.renderHeader()

	var body string
	switch m.activeTab {
	case tabOverview:
		body = m.viewOverview()
	case tabIfaces:
		body = m.viewIfaces()
	case tabPorts:
		body = m.viewPorts()
	case tabProcs:
		body = m.viewProcs()
	}

	footer := subtleStyle.Render("Keys: 1-4 tabs • tab/shift+tab • (Ports/Procs) ↑↓ PgUp/PgDn Home/End • q quit")
	if m.err != nil {
		footer = errStyle.Render("Error: " + m.err.Error())
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) renderHeader() string {
	tabs := []string{
		renderTab("1 Overview", m.activeTab == tabOverview),
		renderTab("2 Interfaces", m.activeTab == tabIfaces),
		renderTab("3 Ports", m.activeTab == tabPorts),
		renderTab("4 Processes", m.activeTab == tabProcs),
	}
	left := titleStyle.Render("ducknetview 🦆 0.0.3") + " " + subtleStyle.Render(fmt.Sprintf("(%dx%d)", m.w, m.h))
	right := strings.Join(tabs, " ")
	line := lipgloss.NewStyle().Width(m.w).Render(left + padTo(m.w-lipgloss.Width(left), right))
	return line
}

func renderTab(s string, active bool) string {
	if active {
		return selectedStyle.Padding(0, 1).Render(s)
	}
	return subtleStyle.Padding(0, 1).Render(s)
}

func (m Model) viewOverview() string {
	if m.lastSnap.TakenAt.IsZero() {
		return boxStyle.Render("Collecting data…")
	}

	up, down := 0, 0
	for _, ii := range m.lastSnap.Ifaces {
		if ii.IsUp {
			up++
		} else {
			down++
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Host: %s\n", okStyle.Render(m.lastSnap.Hostname)))
	b.WriteString(fmt.Sprintf("Uptime: %s\n", m.lastSnap.Uptime.Truncate(time.Second)))
	b.WriteString(fmt.Sprintf("Time: %s\n", m.lastSnap.TakenAt.Format("2006-01-02 15:04:05 -07:00")))
	b.WriteString(fmt.Sprintf("Ifaces: %d total  (%s up, %s down)\n\n",
		len(m.lastSnap.Ifaces),
		okStyle.Render(fmt.Sprintf("%d", up)),
		subtleStyle.Render(fmt.Sprintf("%d", down)),
	))

	// Selected interface card (same as right panel)
	b.WriteString(titleStyle.Render("Selected interface") + "\n")
	b.WriteString(m.renderIfaceDetailsText())

	return boxStyle.Width(min(m.w-2, 120)).Height(max(8, m.h-6)).Render(b.String())
}

func (m Model) viewIfaces() string {
	leftW := max(26, m.w/3)
	bodyH := max(8, m.h-6)
	left := boxStyle.Width(leftW).Height(bodyH).Render(m.ifaceList.View())

	rightW := m.w - leftW - 3
	m.ifaceDetailsVP.Width = max(10, rightW-2)
	m.ifaceDetailsVP.Height = max(5, bodyH-2)

	right := boxStyle.Width(rightW).Height(bodyH).Render(m.ifaceDetailsVP.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m Model) viewPorts() string {
	portsW := min(m.w-2, 120)
	portsH := max(8, m.h-6)

	m.portsVP.Width = max(10, portsW-2)
	m.portsVP.Height = max(5, portsH-2)

	if m.portsText == "" {
		m.portsText = m.renderPortsText()
		m.portsVP.SetContent(m.portsText)
	}
	searchLine := subtleStyle.Render("Press / to search")
	if m.portsQuery != "" {
		searchLine = subtleStyle.Render("Filter: ") + titleStyle.Render(m.portsQuery) + subtleStyle.Render("  (press / to change)")
	}
	if m.portsSearching {
		searchLine = m.portsSearch.View()
	}

	content := searchLine + "\n\n" + m.portsVP.View()

	return boxStyle.Width(portsW).Height(portsH).Render(content)
}

func (m Model) viewProcs() string {
	procsW := min(m.w-2, 120)
	procsH := max(8, m.h-6)

	m.procsVP.Width = max(10, procsW-2)
	m.procsVP.Height = max(5, procsH-2)

	if m.procsText == "" {
		m.procsText = m.renderProcsText()
		m.procsVP.SetContent(m.procsText)
	}

	return boxStyle.Width(procsW).Height(procsH).Render(m.procsVP.View())
}

func (m Model) renderPortsText() string {
	var b strings.Builder

	w := m.portsVP.Width
	if w <= 0 {
		w = 120
	}

	colProto := 4
	colLocal := min(38, max(18, w-4-2-7-1-12))
	colPID := 7

	b.WriteString("Open listening ports\n")
	b.WriteString("Scroll: ↑↓ PgUp/PgDn Home/End\n\n")

	hProto := padRight("PR", colProto)
	hLocal := padRight("LOCAL", colLocal)
	hPID := padRight("PID", colPID)
	hProc := "PROCESS"
	b.WriteString(fmt.Sprintf("%s  %s  %s %s\n", hProto, hLocal, hPID, hProc))
	b.WriteString(strings.Repeat("─", min(w, colProto+2+colLocal+2+colPID+1+len(hProc))) + "\n")

	if len(m.ports) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := m.portsQuery

	for _, p := range m.ports {
		proc := p.Process
		if proc == "" {
			proc = "-"
		}
		local := p.Local
		if local == ":" || local == "0.0.0.0:0" {
			local = "-"
		}

		// filter
		if q != "" && !(containsFold(local, q) || containsFold(proc, q) || containsFold(p.Proto, q)) {
			continue
		}

		// truncate/pad FIRST (so columns stay aligned), then highlight
		proto := padRight(trunc(p.Proto, colProto), colProto)

		localTr := padRight(trunc(local, colLocal), colLocal)
		procTr := proc
		rest := w - (colProto + 2 + colLocal + 2 + colPID + 1)
		if rest < 5 {
			rest = 5
		}
		procTr = trunc(procTr, rest)

		// highlight in visible fields
		localTr = highlightFold(localTr, q)
		procTr = highlightFold(procTr, q)

		pid := padRight(fmt.Sprintf("%d", p.PID), colPID)

		b.WriteString(fmt.Sprintf("%s  %s  %s %s\n", proto, localTr, pid, procTr))
	}

	if m.portsQuery != "" {
		b.WriteString("Filter: " + m.portsQuery + "  (press / to edit, Ctrl+u to clear while editing)\n\n")
	}
	return b.String()
}

func (m Model) renderProcsText() string {
	var b strings.Builder

	w := m.procsVP.Width
	if w <= 0 {
		w = 120
	}

	colPID := 7
	colConns := 6
	colListen := 6

	minName := 16
	colName := w - (colPID + 2 + colConns + 2 + colListen)
	if colName < minName {
		colName = minName
	}
	if colName > 40 {
		colName = 40
	}

	b.WriteString("Processes by network connections (proxy)\n")
	b.WriteString("Scroll: ↑↓ PgUp/PgDn Home/End\n\n")

	h := fmt.Sprintf("%s  %s  %s  %s\n",
		padRight("PID", colPID),
		padRight("NAME", colName),
		padRight("CONNS", colConns),
		padRight("LISTEN", colListen),
	)
	b.WriteString(h)
	b.WriteString(strings.Repeat("─", min(w, colPID+2+colName+2+colConns+2+colListen)) + "\n")

	if len(m.procs) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := m.procsQuery

	for _, p := range m.procs {
		name := p.Name
		if name == "" {
			name = "-"
		}

		// filter by name (and PID)
		if q != "" && !(containsFold(name, q) || containsFold(fmt.Sprintf("%d", p.PID), q)) {
			continue
		}

		pidS := padRight(trunc(fmt.Sprintf("%d", p.PID), colPID), colPID)
		nameS := padRight(trunc(name, colName), colName)
		conS := padRight(trunc(fmt.Sprintf("%d", p.ConnCount), colConns), colConns)
		lisS := padRight(trunc(fmt.Sprintf("%d", p.ListenCount), colListen), colListen)

		nameS = highlightFold(nameS, q)
		pidS = highlightFold(pidS, q) // optional, if searching PID

		b.WriteString(fmt.Sprintf("%s  %s  %s  %s\n", pidS, nameS, conS, lisS))
	}

	return b.String()
}

func (m Model) renderIfaceDetailsText() string {
	var ii *probe.IfaceInfo
	for k := range m.lastSnap.Ifaces {
		if m.lastSnap.Ifaces[k].Name == m.selectedIface {
			ii = &m.lastSnap.Ifaces[k]
			break
		}
	}
	if ii == nil {
		return "Select an interface…\n"
	}

	state := "DOWN"
	st := subtleStyle
	if ii.IsUp {
		state = "UP"
		st = okStyle
	}

	chartW := max(30, min(80, m.w/2))
	rx := Spark(m.rxHist, chartW)
	tx := Spark(m.txHist, chartW)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %s  MTU %d\n", st.Render(state), titleStyle.Render(ii.Name), ii.MTU))
	b.WriteString(fmt.Sprintf("MAC: %s\n", ii.Hardware))
	if len(ii.Addrs) > 0 {
		b.WriteString("Addrs: " + strings.Join(ii.Addrs, ", ") + "\n")
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("RX: %s\n%s\n\n", probe.HumanBytesPerSec(ii.RxBps), rx))
	b.WriteString(fmt.Sprintf("TX: %s\n%s\n", probe.HumanBytesPerSec(ii.TxBps), tx))
	return b.String()
}

// helpers
func padTo(width int, s string) string {
	if width <= 0 {
		return ""
	}
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return strings.Repeat(" ", width-w) + s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func containsFold(s, q string) bool {
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(q))
}

func highlightFold(s, q string) string {
	if q == "" {
		return s
	}
	ls := strings.ToLower(s)
	lq := strings.ToLower(q)

	var out strings.Builder
	i := 0
	for {
		j := strings.Index(ls[i:], lq)
		if j < 0 {
			out.WriteString(s[i:])
			break
		}
		j += i
		out.WriteString(s[i:j])
		// \x1b[7m = reverse, \x1b[0m reset
		out.WriteString("\x1b[7m")
		out.WriteString(s[j : j+len(q)])
		out.WriteString("\x1b[0m")
		i = j + len(q)
	}
	return out.String()
}
