package ui

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

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
	headerH = 1
	footerH = 1
)

type tickMsg time.Time
type extIPTickMsg time.Time

type externalIPMsg struct {
	ip  string
	err error
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func extIPTickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return extIPTickMsg(t) })
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

	activeTab  tab
	netSampler *probe.NetSampler

	lastSnap probe.NetSnapshot
	err      error

	// Interfaces list
	ifaceList      list.Model
	selectedIface  string
	rxHist, txHist []float64

	// Ports / procs
	ports []probe.ListenPort
	procs []probe.ProcNet

	// Viewports
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

	externalIP          string
	externalIPErr       error
	externalIPUpdatedAt time.Time
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
		fetchExternalIPCmd(),
		extIPTickEvery(30*time.Second),
		tickEvery(1*time.Second),
	)
}

// bodyHeight returns height available for the tab body area.
// We also subtract a small constant for borders/padding breathing room.
func (m Model) bodyHeight() int {
	return max(8, m.h-headerH-footerH-2)
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

func fetchExternalIPCmd() tea.Cmd {
	return func() tea.Msg {
		const url = "https://api.ipify.org"

		// Fresh transport each time avoids stale keep-alive sockets after VPN / route changes.
		tr := &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: 3 * time.Second,
		}

		c := &http.Client{
			Timeout:   4 * time.Second,
			Transport: tr,
		}

		resp, err := c.Get(url)
		if err != nil {
			return externalIPMsg{"", err}
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return externalIPMsg{"", fmt.Errorf("external ip: http %d", resp.StatusCode)}
		}

		b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		if err != nil {
			return externalIPMsg{"", err}
		}

		ip := strings.TrimSpace(string(b))
		if ip == "" {
			return externalIPMsg{"", fmt.Errorf("external ip: empty response")}
		}

		return externalIPMsg{ip: ip, err: nil}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height

		leftW := max(26, m.w/3)
		bodyH := m.bodyHeight()

		m.ifaceList.SetSize(
			max(1, leftW-2),
			max(1, bodyH-2),
		)

		// Ports
		portsW := min(m.w-2, 120)
		m.portsVP.Width = max(10, portsW-2)
		m.portsVP.Height = max(5, bodyH-4)

		// Procs
		procsW := min(m.w-2, 120)
		m.procsVP.Width = max(10, procsW-2)
		m.procsVP.Height = max(5, bodyH-4)

		// Interfaces right
		rightW := m.w - leftW - 3
		m.ifaceDetailsVP.Width = max(10, rightW-2)
		m.ifaceDetailsVP.Height = max(5, bodyH-2)

		m.ifaceDetailsVP.SetContent(
			hardClipLinesToWidth(m.ifaceDetailsText, m.ifaceDetailsVP.Width),
		)
		m.portsVP.SetContent(
			hardClipLinesToWidth(m.portsText, m.portsVP.Width),
		)
		m.procsVP.SetContent(
			hardClipLinesToWidth(m.procsText, m.procsVP.Width),
		)

		return m, nil

	case externalIPMsg:
		if msg.err != nil {
			m.externalIPErr = msg.err
			return m, nil
		}
		m.externalIP = msg.ip
		m.externalIPErr = nil
		m.externalIPUpdatedAt = time.Now()
		return m, nil

	case tickMsg:
		cmds := []tea.Cmd{m.refreshCmd(), tickEvery(1 * time.Second)}
		if time.Now().Unix()%5 == 0 {
			cmds = append(cmds, fetchPortsCmd(), fetchProcsCmd())
		}
		return m, tea.Batch(cmds...)

	case extIPTickMsg:
		return m, tea.Batch(fetchExternalIPCmd(), extIPTickEvery(30*time.Second))

	case snapMsg:
		m.lastSnap = probe.NetSnapshot(msg)
		m.err = nil

		prevSel := m.selectedIface
		prevIndex := m.ifaceList.Index()

		innerW := m.ifaceList.Width()
		if innerW <= 0 {
			innerW = 30
		}

		descMax := max(10, innerW-6)

		items := make([]list.Item, 0, len(m.lastSnap.Ifaces))
		for _, ii := range m.lastSnap.Ifaces {
			desc := fmt.Sprintf(
				"MAC %s  RX %s  TX %s",
				ii.Hardware,
				probe.HumanBytesPerSec(ii.RxBps),
				probe.HumanBytesPerSec(ii.TxBps),
			)

			desc = trunc(desc, descMax)
			items = append(items, ifaceItem{
				name: ii.Name,
				desc: desc,
			})
		}

		m.ifaceList.SetItems(items)

		if m.selectedIface == "" && len(items) > 0 {
			m.ifaceList.Select(0)
			m.selectedIface = items[0].(ifaceItem).name
		} else if prevSel != "" {
			for i, it := range items {
				if it.(ifaceItem).name == prevSel {
					m.ifaceList.Select(i)
					break
				}
			}
		} else if prevIndex >= 0 && prevIndex < len(items) {
			m.ifaceList.Select(prevIndex)
			m.selectedIface = items[prevIndex].(ifaceItem).name
		}

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

		m.ifaceDetailsText = m.renderIfaceDetailsText()
		m.ifaceDetailsText = hardClipLinesToWidth(
			m.ifaceDetailsText,
			m.ifaceDetailsVP.Width,
		)
		m.ifaceDetailsVP.SetContent(m.ifaceDetailsText)

		return m, nil

	case portsMsg:
		m.ports = msg
		m.portsText = m.renderPortsText()
		m.portsText = hardClipLinesToWidth(m.portsText, m.portsVP.Width)
		m.portsVP.SetContent(m.portsText)
		return m, nil

	case procsMsg:
		m.procs = msg
		m.procsText = m.renderProcsText()
		m.procsText = hardClipLinesToWidth(m.procsText, m.procsVP.Width)
		m.procsVP.SetContent(m.procsText)
		return m, nil

	case errMsg:
		m.err = msg.error
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			// disabled (no-op)
			return m, nil
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

		case "ctrl+u":
			if m.activeTab == tabPorts && !m.portsSearching {
				m.portsQuery = ""
				m.portsSearch.SetValue("")
				m.portsText = m.renderPortsText()
				m.portsVP.SetContent(m.portsText)
				return m, nil
			}
			if m.activeTab == tabProcs && !m.procsSearching {
				m.procsQuery = ""
				m.procsSearch.SetValue("")
				m.procsText = m.renderProcsText()
				m.procsVP.SetContent(m.procsText)
				return m, nil
			}

		case "ctrl+e":
			return m, fetchExternalIPCmd()
		}
	}

	// Interfaces tab
	if m.activeTab == tabIfaces {
		var cmd tea.Cmd
		m.ifaceList, cmd = m.ifaceList.Update(msg)

		needExtRefresh := false

		// Auto-follow selection
		if it, ok := m.ifaceList.SelectedItem().(ifaceItem); ok {
			if m.selectedIface != it.name {
				m.selectedIface = it.name
				m.rxHist, m.txHist = nil, nil
				needExtRefresh = true

				m.ifaceDetailsText = m.renderIfaceDetailsText()
				m.ifaceDetailsText = hardClipLinesToWidth(m.ifaceDetailsText, m.ifaceDetailsVP.Width)
				m.ifaceDetailsVP.SetContent(m.ifaceDetailsText)
			}
		}

		var cmd2 tea.Cmd
		m.ifaceDetailsVP, cmd2 = m.ifaceDetailsVP.Update(msg)

		if needExtRefresh {
			return m, tea.Batch(cmd, cmd2, fetchExternalIPCmd())
		}
		return m, tea.Batch(cmd, cmd2)
	}

	// Ports search mode
	if m.activeTab == tabPorts && m.portsSearching {
		var cmd tea.Cmd
		m.portsSearch, cmd = m.portsSearch.Update(msg)

		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "enter":
				m.portsQuery = strings.TrimSpace(m.portsSearch.Value())
				m.portsSearching = false
				m.portsSearch.Blur()
				m.portsText = m.renderPortsText()
				m.portsVP.SetContent(m.portsText)
				return m, nil

			case "esc":
				m.portsSearching = false
				m.portsSearch.Blur()
				return m, nil

			case "ctrl+u":
				// Clear input while editing; keep search mode active.
				m.portsSearch.SetValue("")
				return m, cmd
			}
		}
		return m, cmd
	}

	// Procs search mode
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

	footer := subtleStyle.Render("Keys: tab/shift+tab • ←/→ • / search • Ctrl+u clear • ctrl+e ext-ip")
	if m.err != nil {
		footer = errStyle.Render("Error: " + m.err.Error())
	}

	footer = clampToWidthOneLine(footer, m.w)
	footer = lipgloss.NewStyle().Width(m.w).Render(footer)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer) + "\x1b[0m"
}

func (m Model) renderHeader() string {
	tabs := []string{
		renderTab("1 Overview", m.activeTab == tabOverview),
		renderTab("2 Interfaces", m.activeTab == tabIfaces),
		renderTab("3 Ports", m.activeTab == tabPorts),
		renderTab("4 Processes", m.activeTab == tabProcs),
	}

	left := titleStyle.Render("ducknetview 🦆 0.0.4") + " " + subtleStyle.Render(fmt.Sprintf("(%dx%d)", m.w, m.h))

	rem := m.w - lipgloss.Width(left)
	if rem < 0 {
		rem = 0
	}

	right := joinTabsWithinWidth(tabs, rem)

	line := left + padTo(rem, right)
	return lipgloss.NewStyle().Width(m.w).Render(line)
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

	b.WriteString(titleStyle.Render("Selected interface") + "\n")
	b.WriteString(m.renderIfaceDetailsText())
	b.WriteString("\n")

	ext := m.externalIP
	if ext == "" {
		ext = "…"
	}
	line := fmt.Sprintf("External IP: %s", ext)
	if !m.externalIPUpdatedAt.IsZero() {
		line += fmt.Sprintf("  (updated %s)", m.externalIPUpdatedAt.Format("15:04:05"))
	}
	b.WriteString(line + "\n")
	if m.externalIPErr != nil {
		b.WriteString(fmt.Sprintf("External IP error: %s\n", subtleStyle.Render(m.externalIPErr.Error())))
	}

	return boxStyle.Width(min(m.w-2, 120)).Height(m.bodyHeight()).Render(b.String())
}

func (m Model) viewIfaces() string {
	leftW := max(26, m.w/3)
	bodyH := m.bodyHeight()

	left := boxStyle.Width(leftW).Height(bodyH).Render(m.ifaceList.View())

	rightW := m.w - leftW - 3

	right := boxStyle.Width(rightW).Height(bodyH).Render(m.ifaceDetailsVP.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m Model) viewPorts() string {
	portsW := min(m.w-2, 120)
	portsH := m.bodyHeight()

	if m.portsText == "" {
		m.portsText = m.renderPortsText()
		m.portsVP.SetContent(m.portsText)
	}

	searchLine := subtleStyle.Render("Press / to search")
	if m.portsQuery != "" {
		searchLine = subtleStyle.Render("Filter: ") + titleStyle.Render(m.portsQuery) + subtleStyle.Render("  (press / to change, ctrl+u to clear)")
	}
	if m.portsSearching {
		searchLine = m.portsSearch.View()
	}

	content := searchLine + "\n\n" + m.portsVP.View()
	return boxStyle.Width(portsW).Height(portsH).Render(content)
}

func (m Model) viewProcs() string {
	procsW := min(m.w-2, 120)
	procsH := m.bodyHeight()

	if m.procsText == "" {
		m.procsText = m.renderProcsText()
		m.procsVP.SetContent(m.procsText)
	}

	searchLine := subtleStyle.Render("Press / to search")
	if m.procsQuery != "" {
		searchLine = subtleStyle.Render("Filter: ") + titleStyle.Render(m.procsQuery) + subtleStyle.Render("  (press / to change, ctrl+u to clear)")
	}
	if m.procsSearching {
		searchLine = m.procsSearch.View()
	}

	content := searchLine + "\n\n" + m.procsVP.View()
	return boxStyle.Width(procsW).Height(procsH).Render(content)
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

		if q != "" && !(containsFold(local, q) || containsFold(proc, q) || containsFold(p.Proto, q)) {
			continue
		}

		proto := padRight(trunc(p.Proto, colProto), colProto)

		localTr := padRight(trunc(local, colLocal), colLocal)
		procTr := proc
		rest := w - (colProto + 2 + colLocal + 2 + colPID + 1)
		if rest < 5 {
			rest = 5
		}
		procTr = trunc(procTr, rest)

		localTr = highlightFold(localTr, q)
		procTr = highlightFold(procTr, q)

		pid := padRight(fmt.Sprintf("%d", p.PID), colPID)

		b.WriteString(fmt.Sprintf("%s  %s  %s %s\n", proto, localTr, pid, procTr))
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

		if q != "" && !(containsFold(name, q) || containsFold(fmt.Sprintf("%d", p.PID), q)) {
			continue
		}

		pidS := padRight(trunc(fmt.Sprintf("%d", p.PID), colPID), colPID)
		nameS := padRight(trunc(name, colName), colName)
		conS := padRight(trunc(fmt.Sprintf("%d", p.ConnCount), colConns), colConns)
		lisS := padRight(trunc(fmt.Sprintf("%d", p.ListenCount), colListen), colListen)

		nameS = highlightFold(nameS, q)
		pidS = highlightFold(pidS, q)

		b.WriteString(fmt.Sprintf("%s  %s  %s  %s\n", pidS, nameS, conS, lisS))
	}

	return b.String()
}

var hlStyle = lipgloss.NewStyle().Reverse(true)

func highlightFold(s, q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return s
	}

	rs := []rune(s)
	rq := []rune(q)

	ls := strings.ToLower(string(rs))
	lq := strings.ToLower(string(rq))

	var out strings.Builder
	i := 0
	for {
		j := strings.Index(ls[i:], lq)
		if j < 0 {
			out.WriteString(string(rs[i:]))
			break
		}
		j += i

		out.WriteString(string(rs[i:j]))

		end := j + len([]rune(lq))
		if end > len(rs) {
			end = len(rs)
		}
		out.WriteString(hlStyle.Render(string(rs[j:end])))

		i = end
	}

	return out.String()
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

	avail := m.ifaceDetailsVP.Width
	if avail <= 0 {
		avail = 40
	}
	chartW := max(10, avail-6)

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

func ansiSafeTruncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	var out strings.Builder
	visible := 0

	for i := 0; i < len(s); {
		if s[i] == 0x1b { // ESC
			// CSI: ESC [ ... < 0x40-0x7E>
			if i+1 < len(s) && s[i+1] == '[' {
				j := i + 2
				for j < len(s) {
					b := s[j]
					// CSI 0x40..0x7E
					if b >= 0x40 && b <= 0x7E {
						j++
						break
					}
					j++
				}
				out.WriteString(s[i:j])
				i = j
				continue
			}

			if i+1 < len(s) && s[i+1] == ']' {
				j := i + 2
				for j < len(s) {
					// BEL terminator
					if s[j] == 0x07 {
						j++
						break
					}
					// ST terminator: ESC
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				out.WriteString(s[i:j])
				i = j
				continue
			}

			if i+1 < len(s) {
				out.WriteByte(s[i])
				out.WriteByte(s[i+1])
				i += 2
			} else {
				out.WriteByte(s[i])
				i++
			}
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}

		w := lipgloss.Width(string(r))
		if visible+w > maxWidth {
			break
		}

		out.WriteRune(r)
		visible += w
		i += size
	}

	return out.String()
}

func joinTabsWithinWidth(tabs []string, maxW int) string {
	if maxW <= 0 || len(tabs) == 0 {
		return ""
	}

	var out strings.Builder
	used := 0
	sep := " "

	for i, t := range tabs {
		tw := lipgloss.Width(t)
		addSep := i > 0
		sepW := 0
		if addSep {
			sepW = lipgloss.Width(sep)
		}

		if used+sepW+tw > maxW {
			ell := subtleStyle.Render("…")
			ellW := lipgloss.Width(ell)
			if used > 0 && used+sepW+ellW <= maxW {
				out.WriteString(sep)
				out.WriteString(ell)
			} else if used == 0 && ellW <= maxW {
				out.WriteString(ell)
			}
			break
		}

		if addSep {
			out.WriteString(sep)
			used += sepW
		}

		out.WriteString(t)
		used += tw
	}

	return out.String()
}

func hardClipLinesToWidth(s string, w int) string {
	if w <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = ansiSafeTruncate(lines[i], w)
	}
	return strings.Join(lines, "\n")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

func clampToWidthOneLine(s string, w int) string {
	s = oneLine(s)
	if w <= 0 {
		return ""
	}
	return ansiSafeTruncate(s, w)
}
