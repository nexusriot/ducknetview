package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
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

	ifaceList     list.Model
	selectedIface string

	// history for selected iface
	rxHist []float64
	txHist []float64

	ports        []probe.ListenPort
	procs        []probe.ProcNet
	pickingIface bool
	showDown     bool
	showLoop     bool
	showBridges  bool
	showVeth     bool
	showDocker   bool
}

func NewModel() Model {
	ls := list.New([]list.Item{}, list.NewDefaultDelegate(), 30, 10)
	ls.Title = "Interfaces"
	ls.SetShowHelp(false)

	return Model{
		activeTab:   tabOverview,
		netSampler:  probe.NewNetSampler(),
		ifaceList:   ls,
		showDown:    false,
		showLoop:    false,
		showBridges: false,
		showVeth:    false,
		showDocker:  false,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.refreshCmd(),
		tickEvery(1*time.Second),
	)
}

func (m Model) ifaceVisible(ii probe.IfaceInfo) bool {
	if !m.showDown && !ii.IsUp {
		return false
	}
	switch ii.Kind {
	case probe.IfaceLoopback:
		return m.showLoop
	case probe.IfaceLinuxBridge:
		return m.showBridges
	case probe.IfaceVeth:
		return m.showVeth
	case probe.IfaceDockerBridge:
		return m.showDocker
	default:
		return true
	}
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
		procs, err := probe.TopProcsByConnections(30)
		if err != nil {
			return errMsg{err}
		}
		return procsMsg(procs)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {

	if m.pickingIface {
		return m.updateIfacePicker(msg)
	}

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.ifaceList.SetSize(max(20, m.w/3), max(8, m.h-10))
		return m, nil

	case tickMsg:
		// refresh net snapshot every second
		cmds := []tea.Cmd{m.refreshCmd(), tickEvery(1 * time.Second)}
		// refresh ports/procs less often
		if time.Now().Unix()%5 == 0 {
			cmds = append(cmds, fetchPortsCmd(), fetchProcsCmd())
		}
		return m, tea.Batch(cmds...)

	case snapMsg:
		m.lastSnap = probe.NetSnapshot(msg)
		m.err = nil

		// update iface list
		items := make([]list.Item, 0, len(m.lastSnap.Ifaces))
		for _, ii := range m.lastSnap.Ifaces {

			if !m.ifaceVisible(ii) {
				continue
			}
			desc := fmt.Sprintf("MAC %s  RX %s  TX %s",
				ii.Hardware,
				probe.HumanBytesPerSec(ii.RxBps),
				probe.HumanBytesPerSec(ii.TxBps),
			)
			items = append(items, ifaceItem{name: ii.Name, desc: desc})
		}
		m.ifaceList.SetItems(items)

		if m.selectedIface != "" {
			found := false
			for _, ii := range m.lastSnap.Ifaces {
				if ii.Name == m.selectedIface {
					found = true
					break
				}
			}
			if !found && len(m.lastSnap.Ifaces) > 0 {
				m.selectedIface = m.lastSnap.Ifaces[0].Name
			}
		}

		// default selected iface
		if m.selectedIface == "" && len(m.lastSnap.Ifaces) > 0 {
			m.selectedIface = m.lastSnap.Ifaces[0].Name
		}

		// update history for selected iface
		for _, ii := range m.lastSnap.Ifaces {
			if ii.Name == m.selectedIface {
				m.rxHist = append(m.rxHist, ii.RxBps)
				m.txHist = append(m.txHist, ii.TxBps)
				m.rxHist = probe.ClampHistory(m.rxHist, max(10, m.w/2))
				m.txHist = probe.ClampHistory(m.txHist, max(10, m.w/2))
				break
			}
		}

		return m, nil

	case portsMsg:
		m.ports = []probe.ListenPort(msg)
		return m, nil

	case procsMsg:
		m.procs = []probe.ProcNet(msg)
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
		case "1":
			m.activeTab = tabOverview
			return m, nil
		case "2":
			m.activeTab = tabIfaces
			return m, nil
		case "3":
			m.activeTab = tabPorts
			return m, nil
		case "4":
			m.activeTab = tabProcs
			return m, nil
		case "i":
			m.pickingIface = !m.pickingIface
			return m, nil
		case "d":
			m.showDown = !m.showDown
			return m, nil
		case "l":
			m.showLoop = !m.showLoop
			return m, nil
		case "b":
			m.showBridges = !m.showBridges
			return m, nil
		case "v":
			m.showVeth = !m.showVeth
			return m, nil
		case "o": // docker
			m.showDocker = !m.showDocker
			return m, nil

		}
	}

	// allow list navigation only on iface tab
	if m.activeTab == tabIfaces {
		var cmd tea.Cmd
		m.ifaceList, cmd = m.ifaceList.Update(msg)

		// enter selects interface
		if km, ok := msg.(tea.KeyMsg); ok && km.String() == "enter" {
			if it, ok := m.ifaceList.SelectedItem().(ifaceItem); ok {
				m.selectedIface = it.name
				m.rxHist = nil
				m.txHist = nil
			}
		}
		return m, cmd
	}

	return m, nil
}

func (m Model) updateIfacePicker(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.ifaceList, cmd = m.ifaceList.Update(msg)

	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "enter":
			if it, ok := m.ifaceList.SelectedItem().(ifaceItem); ok {
				m.selectedIface = it.name
				m.rxHist, m.txHist = nil, nil
			}
			m.pickingIface = false
		case "esc":
			m.pickingIface = false
		}
	}
	return m, cmd
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

	footer := subtleStyle.Render("Keys: 1-4 tabs • i pick iface • d/l/b/v/o filters • q quit")

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
	left := titleStyle.Render("ducknetview🦆 0.0.2 PoC") + " " + subtleStyle.Render(fmt.Sprintf("(%dx%d)", m.w, m.h))
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

	var shown int
	for _, ii := range m.lastSnap.Ifaces {
		if m.ifaceVisible(ii) {
			shown++
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Host: %s\n", okStyle.Render(m.lastSnap.Hostname)))
	b.WriteString(fmt.Sprintf("Uptime: %s\n", m.lastSnap.Uptime.Truncate(time.Second)))
	b.WriteString(fmt.Sprintf("Iface: %s  (press %s to change)\n",
		titleStyle.Render(m.selectedIface),
		subtleStyle.Render("i"),
	))
	b.WriteString(fmt.Sprintf("Interfaces: total %d, shown %d\n\n", len(m.lastSnap.Ifaces), shown))

	// print only selected iface
	var sel *probe.IfaceInfo
	for k := range m.lastSnap.Ifaces {
		if m.lastSnap.Ifaces[k].Name == m.selectedIface {
			sel = &m.lastSnap.Ifaces[k]
			break
		}
	}
	if sel == nil {
		b.WriteString("Selected interface not found.\n")
		return boxStyle.Width(min(m.w-2, 120)).Render(b.String())
	}

	state := "DOWN"
	st := subtleStyle
	if sel.IsUp {
		state = "UP"
		st = okStyle
	}
	b.WriteString(fmt.Sprintf("%s %s  MTU %d  MAC %s\n", st.Render(state), titleStyle.Render(sel.Name), sel.MTU, sel.Hardware))
	if len(sel.Addrs) > 0 {
		b.WriteString("  addrs: " + strings.Join(sel.Addrs, ", ") + "\n")
	}
	b.WriteString(fmt.Sprintf("  RX %s  TX %s\n\n",
		probe.HumanBytesPerSec(sel.RxBps),
		probe.HumanBytesPerSec(sel.TxBps),
	))

	// add mini charts here too
	chartW := max(30, min(80, m.w-20))
	b.WriteString("  RX " + Spark(m.rxHist, chartW) + "\n")
	b.WriteString("  TX " + Spark(m.txHist, chartW) + "\n")

	// optional: show filter hints
	b.WriteString("\n")
	b.WriteString(subtleStyle.Render("Filters: d=down l=loop b=bridge v=veth o=docker (toggle)\n"))

	content := boxStyle.Width(min(m.w-2, 120)).Render(b.String())

	// if picker open, draw it under overview
	if m.pickingIface {
		p := boxStyle.Width(min(m.w-2, 120)).Render("Select interface (↑↓ enter, esc)\n\n" + m.ifaceList.View())
		return lipgloss.JoinVertical(lipgloss.Left, content, p)
	}
	return content
}

func (m Model) viewIfaces() string {
	left := boxStyle.Width(max(24, m.w/3)).Height(max(8, m.h-6)).Render(m.ifaceList.View())

	// right side: selected iface + mini chart
	var ii *probe.IfaceInfo
	for k := range m.lastSnap.Ifaces {
		if m.lastSnap.Ifaces[k].Name == m.selectedIface {
			ii = &m.lastSnap.Ifaces[k]
			break
		}
	}

	rightContent := "Select an interface…"
	if ii != nil {
		width := max(20, m.w-(m.w/3)-6)
		chartW := max(20, width-10)
		rx := Spark(m.rxHist, chartW)
		tx := Spark(m.txHist, chartW)

		rightContent = fmt.Sprintf(
			"%s\nMAC: %s\nAddrs: %s\n\nRX: %s\n%s\n\nTX: %s\n%s\n",
			titleStyle.Render("Monitoring: "+ii.Name),
			ii.Hardware,
			strings.Join(ii.Addrs, ", "),
			probe.HumanBytesPerSec(ii.RxBps),
			rx,
			probe.HumanBytesPerSec(ii.TxBps),
			tx,
		)
	}

	right := boxStyle.Width(m.w - max(24, m.w/3) - 3).Height(max(8, m.h-6)).Render(rightContent)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m Model) viewPorts() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Open listening ports") + "\n")
	b.WriteString(subtleStyle.Render("Note: PID/process mapping may require elevated permissions.\n\n"))

	if len(m.ports) == 0 {
		b.WriteString("No data (yet)…\n")
	} else {
		for _, p := range m.ports {
			proc := p.Process
			if proc == "" {
				proc = "-"
			}
			b.WriteString(fmt.Sprintf("%-4s %-22s pid=%-6d %s\n", p.Proto, p.Local, p.PID, proc))
		}
	}

	return boxStyle.Width(min(m.w-2, 120)).Height(max(8, m.h-6)).Render(b.String())
}

func (m Model) viewProcs() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Processes by network connections (proxy for activity)") + "\n")
	b.WriteString(subtleStyle.Render("For true per-process bandwidth, consider eBPF/netlink accounting.\n\n"))

	if len(m.procs) == 0 {
		b.WriteString("No data (yet)…\n")
	} else {
		b.WriteString(fmt.Sprintf("%-7s %-28s %-10s %-10s\n", "PID", "NAME", "CONNS", "LISTEN"))
		b.WriteString(strings.Repeat("─", 64) + "\n")
		for _, p := range m.procs {
			name := p.Name
			if name == "" {
				name = "-"
			}
			if len(name) > 28 {
				name = name[:28]
			}
			b.WriteString(fmt.Sprintf("%-7d %-28s %-10d %-10d\n", p.PID, name, p.ConnCount, p.ListenCount))
		}
	}

	return boxStyle.Width(min(m.w-2, 120)).Height(max(8, m.h-6)).Render(b.String())
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
