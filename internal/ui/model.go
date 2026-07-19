package ui

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/atotto/clipboard"
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
	tabConns
	headerH  = 1
	footerH  = 1
	tabCount = 5
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

	// Ports / procs / conns
	ports []probe.ListenPort
	procs []probe.ProcNet
	conns []probe.Conn

	// Viewports
	portsVP   viewport.Model
	portsText string

	procsVP   viewport.Model
	procsText string

	connsVP   viewport.Model
	connsText string

	ifaceDetailsVP   viewport.Model
	ifaceDetailsText string

	portsSearch    textinput.Model
	portsSearching bool
	portsQuery     string

	procsSearch    textinput.Model
	procsSearching bool
	procsQuery     string

	connsSearch    textinput.Model
	connsSearching bool
	connsQuery     string

	// Row cursors (index into the filtered/displayed rows of each table tab)
	portsCursor int
	procsCursor int
	connsCursor int

	// Sort state per table tab: an index into the tab's sort-column list plus
	// a descending flag. groupSort* applies to the grouped connections view.
	portsSortCol  int
	portsSortDesc bool
	procsSortCol  int
	procsSortDesc bool
	connsSortCol  int
	connsSortDesc bool
	groupSortCol  int
	groupSortDesc bool

	// Hide loopback/docker/bridge/veth and down interfaces from the list.
	hideNoise bool

	// Transient status line (e.g. "copied to clipboard"), cleared on next key.
	status string

	// Help overlay (toggled with `?`).
	showHelp bool

	// Conns extras
	connsRevDNS bool
	connsGroup  bool
	dnsCache    map[string]string // ip -> hostname (empty string means resolved but no PTR)
	dnsTried    map[string]bool   // ip -> already attempted (don't retry within session)

	externalIP          string
	externalIPErr       error
	externalIPUpdatedAt time.Time

	// Refresh cadence control.
	refreshInterval time.Duration
	paused          bool
	lastConnFetch   time.Time

	// Row-detail overlay (toggled with `enter` on a table tab).
	showDetail bool
	detailText string

	// Kill-process confirmation (armed with `x`, resolved with a signal key).
	confirmingKill bool
	killPID        int32
	killName       string

	// Aggregate bandwidth history across interfaces (Overview total sparkline).
	totalRxHist, totalTxHist []float64

	// Connection tracking across enumerations: first-seen time per connection
	// key drives both the AGE column and the new/closed delta counters.
	connFirstSeen map[string]time.Time
	newConns      map[string]bool
	connSeen      bool
	connAdded     int
	connRemoved   int

	// Listening-port tracking across enumerations: newly-appeared listeners are
	// highlighted (a new listener can mean a backdoor or a regression).
	prevPortKeys map[string]bool
	newPorts     map[string]bool
	portSeen     bool

	// Connections/ports quick filters (session-only, not persisted).
	connStateFilter int  // 0 all · 1 established · 2 active (exclude teardown)
	protoFilter     int  // 0 all · 1 tcp · 2 udp
	ipVerFilter     int  // 0 all · 1 IPv4 · 2 IPv6
	publicOnly      bool // show only connections to public (routable) remotes

	// Watchlist of remote addresses to highlight (loaded from config).
	watch        *watchList
	watchlistRaw []string
}

// refreshSteps are the selectable auto-refresh intervals, cycled with `+`/`-`.
var refreshSteps = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	3 * time.Second,
	5 * time.Second,
	10 * time.Second,
}

// connFetchInterval is the minimum spacing between socket enumerations; the
// ports/procs/conns views refresh no faster than this regardless of the chosen
// interface-sample interval, since enumeration is comparatively expensive.
const connFetchInterval = 5 * time.Second

func NewModel() Model {
	ls := list.New([]list.Item{}, list.NewDefaultDelegate(), 30, 10)
	ls.Title = "Interfaces"
	ls.SetShowHelp(false)

	// viewports (sizes are set on WindowSizeMsg)
	pvp := viewport.New(0, 0)
	kvp := viewport.New(0, 0)
	dvp := viewport.New(0, 0)
	cvp := viewport.New(0, 0)

	ps := textinput.New()
	ps.Placeholder = "search port / address / process"
	ps.Prompt = "/ "
	ps.CharLimit = 64

	qs := textinput.New()
	qs.Placeholder = "search process name"
	qs.Prompt = "/ "
	qs.CharLimit = 64

	cs := textinput.New()
	cs.Placeholder = "search local / remote / process / state"
	cs.Prompt = "/ "
	cs.CharLimit = 64

	m := Model{
		activeTab:  tabOverview,
		netSampler: probe.NewNetSampler(),

		ifaceList: ls,

		portsVP:        pvp,
		procsVP:        kvp,
		connsVP:        cvp,
		ifaceDetailsVP: dvp,
		portsSearch:    ps,
		procsSearch:    qs,
		connsSearch:    cs,

		// Default to the previous fixed orderings: procs & grouped conns by
		// connection count descending; ports & conns ascending by their first
		// column.
		procsSortDesc: true,
		groupSortDesc: true,

		refreshInterval: refreshSteps[0],
		lastConnFetch:   time.Now(),
	}
	return m
}

// WithConfig overlays persisted settings (sort keys, toggles, refresh rate)
// onto the model and returns it. Kept separate from NewModel so tests get clean
// defaults independent of any on-disk config. Call it from main.
func (m Model) WithConfig() Model {
	m.applyConfig()
	return m
}

func isRefreshStep(d time.Duration) bool {
	for _, s := range refreshSteps {
		if s == d {
			return true
		}
	}
	return false
}

// setRefreshInterval moves the refresh cadence one step faster or slower and
// records a status line. The change takes effect on the next tick.
func (m *Model) setRefreshInterval(faster bool) {
	idx := 0
	for i, s := range refreshSteps {
		if s == m.refreshInterval {
			idx = i
			break
		}
	}
	if faster && idx > 0 {
		idx--
	} else if !faster && idx < len(refreshSteps)-1 {
		idx++
	}
	m.refreshInterval = refreshSteps[idx]
	m.status = "Refresh interval: " + m.refreshInterval.String()
	m.saveConfig()
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.refreshCmd(),
		fetchConnInfoCmd(),
		fetchExternalIPCmd(),
		extIPTickEvery(30*time.Second),
		tickEvery(m.refreshInterval),
	)
}

// quit persists settings and ends the program. Used for every quit path so the
// config is always written on a clean exit.
func (m Model) quit() (tea.Model, tea.Cmd) {
	m.saveConfig()
	return m, tea.Quit
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

// connInfoMsg carries the ports / procs / conns views derived from a single
// socket enumeration (see probe.Collect).
type connInfoMsg probe.ConnInfo

// copyResultMsg reports the outcome of an async clipboard write.
type copyResultMsg struct {
	what string
	err  error
}

func copyToClipboardCmd(text, what string) tea.Cmd {
	return func() tea.Msg {
		return copyResultMsg{what: what, err: clipboard.WriteAll(text)}
	}
}

// killResultMsg reports the outcome of an async signal delivery.
type killResultMsg struct {
	pid  int32
	name string
	sig  syscall.Signal
	err  error
}

func killProcessCmd(pid int32, name string, sig syscall.Signal) tea.Cmd {
	return func() tea.Msg {
		return killResultMsg{pid: pid, name: name, sig: sig, err: probe.SignalProcess(pid, sig)}
	}
}

func sigName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGHUP:
		return "SIGHUP"
	default:
		return sig.String()
	}
}

func fetchConnInfoCmd() tea.Cmd {
	return func() tea.Msg {
		info, err := probe.Collect(80)
		if err != nil {
			return errMsg{err}
		}
		return connInfoMsg(info)
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

		// Conns
		connsW := min(m.w-2, 160)
		m.connsVP.Width = max(10, connsW-2)
		m.connsVP.Height = max(5, bodyH-4)

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
		m.connsVP.SetContent(
			hardClipLinesToWidth(m.connsText, m.connsVP.Width),
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
		// Always re-arm the ticker (so pause can be lifted and interval changes
		// take effect); only actually sample when not paused.
		cmds := []tea.Cmd{tickEvery(m.refreshInterval)}
		if !m.paused {
			cmds = append(cmds, m.refreshCmd())
			if now := time.Now(); now.Sub(m.lastConnFetch) >= connFetchInterval {
				m.lastConnFetch = now
				cmds = append(cmds, fetchConnInfoCmd())
			}
		}
		return m, tea.Batch(cmds...)

	case extIPTickMsg:
		return m, tea.Batch(fetchExternalIPCmd(), extIPTickEvery(30*time.Second))

	case snapMsg:
		m.lastSnap = probe.NetSnapshot(msg)
		m.err = nil

		m.rebuildIfaceList()

		histMax := max(30, min(200, m.w/2))

		for _, ii := range m.lastSnap.Ifaces {
			if ii.Name == m.selectedIface {
				m.rxHist = probe.ClampHistory(append(m.rxHist, ii.RxBps), histMax)
				m.txHist = probe.ClampHistory(append(m.txHist, ii.TxBps), histMax)
				break
			}
		}

		// Aggregate bandwidth across real interfaces (loopback excluded) for the
		// Overview total sparkline.
		var totRx, totTx float64
		for _, ii := range m.lastSnap.Ifaces {
			if ii.Kind == probe.IfaceLoopback {
				continue
			}
			totRx += ii.RxBps
			totTx += ii.TxBps
		}
		m.totalRxHist = probe.ClampHistory(append(m.totalRxHist, totRx), histMax)
		m.totalTxHist = probe.ClampHistory(append(m.totalTxHist, totTx), histMax)

		m.ifaceDetailsText = m.renderIfaceDetailsText(m.ifaceDetailsVP.Width)
		m.ifaceDetailsText = hardClipLinesToWidth(
			m.ifaceDetailsText,
			m.ifaceDetailsVP.Width,
		)
		m.ifaceDetailsVP.SetContent(m.ifaceDetailsText)

		return m, nil

	case connInfoMsg:
		m.ports = msg.Ports
		m.procs = msg.Procs
		m.conns = msg.Conns

		// Track each connection's first-seen time across enumerations: this
		// drives the AGE column and the new/closed delta counters. The first
		// enumeration establishes a baseline (nothing is "new" on startup).
		now := time.Now()
		next := make(map[string]time.Time, len(msg.Conns))
		curKeys := make(map[string]bool, len(msg.Conns))
		m.newConns = map[string]bool{}
		m.connAdded, m.connRemoved = 0, 0
		for _, c := range msg.Conns {
			k := connKey(c)
			curKeys[k] = true
			if t, ok := m.connFirstSeen[k]; ok {
				next[k] = t
			} else {
				next[k] = now
				if m.connSeen {
					m.newConns[k] = true
					m.connAdded++
				}
			}
		}
		if m.connSeen {
			for k := range m.connFirstSeen {
				if !curKeys[k] {
					m.connRemoved++
				}
			}
		}
		m.connFirstSeen = next
		m.connSeen = true

		// Flag listeners that appeared since the previous enumeration.
		curPortKeys := make(map[string]bool, len(msg.Ports))
		for _, p := range msg.Ports {
			curPortKeys[portKey(p)] = true
		}
		m.newPorts = map[string]bool{}
		if m.portSeen {
			for k := range curPortKeys {
				if !m.prevPortKeys[k] {
					m.newPorts[k] = true
				}
			}
		}
		m.prevPortKeys = curPortKeys
		m.portSeen = true

		m.portsCursor = clampInt(m.portsCursor, 0, len(m.filteredPorts())-1)
		m.procsCursor = clampInt(m.procsCursor, 0, len(m.filteredProcs())-1)
		m.connsCursor = clampInt(m.connsCursor, 0, m.connsRowCount()-1)

		cmds := m.kickConnLookups()

		m.portsText = hardClipLinesToWidth(m.renderPortsText(), m.portsVP.Width)
		m.portsVP.SetContent(m.portsText)
		m.procsText = hardClipLinesToWidth(m.renderProcsText(), m.procsVP.Width)
		m.procsVP.SetContent(m.procsText)
		m.connsText = hardClipLinesToWidth(m.renderConnsText(), m.connsVP.Width)
		m.connsVP.SetContent(m.connsText)

		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case dnsMsg:
		if m.dnsCache == nil {
			m.dnsCache = map[string]string{}
		}
		m.dnsCache[msg.ip] = msg.name
		if m.activeTab == tabConns {
			m.connsText = m.renderConnsText()
			m.connsText = hardClipLinesToWidth(m.connsText, m.connsVP.Width)
			m.connsVP.SetContent(m.connsText)
		}
		return m, nil

	case copyResultMsg:
		if msg.err != nil {
			m.status = "Copy failed: " + msg.err.Error()
		} else {
			m.status = "Copied " + msg.what + " to clipboard"
		}
		return m, nil

	case killResultMsg:
		who := fmt.Sprintf("PID %d", msg.pid)
		if msg.name != "" {
			who += " (" + msg.name + ")"
		}
		if msg.err != nil {
			m.status = sigName(msg.sig) + " → " + who + " failed: " + msg.err.Error()
		} else {
			m.status = "Sent " + sigName(msg.sig) + " to " + who
			// Refresh the socket views soon so the change is reflected.
			m.lastConnFetch = time.Time{}
		}
		return m, nil

	case exportResultMsg:
		if msg.err != nil {
			m.status = "Export failed: " + msg.err.Error()
		} else {
			m.status = "Exported to " + msg.path
		}
		return m, nil

	case errMsg:
		m.err = msg.error
		return m, nil

	case tea.KeyMsg:
		// A status line ("copied…") lives until the next keypress.
		m.status = ""

		// While the help overlay is open, any key closes it (ctrl+c still quits).
		if m.showHelp {
			if msg.String() == "ctrl+c" {
				return m.quit()
			}
			m.showHelp = false
			return m, nil
		}

		// Likewise for the row-detail overlay.
		if m.showDetail {
			if msg.String() == "ctrl+c" {
				return m.quit()
			}
			m.showDetail = false
			return m, nil
		}

		// The kill confirmation captures all keys: only 'y' proceeds, ctrl+c
		// quits, everything else cancels (fail-safe for a destructive action).
		if m.confirmingKill {
			var sig syscall.Signal
			switch msg.String() {
			case "ctrl+c":
				return m.quit()
			case "t", "y", "Y":
				sig = syscall.SIGTERM
			case "k":
				sig = syscall.SIGKILL
			case "h":
				sig = syscall.SIGHUP
			default:
				m.confirmingKill = false
				m.status = "Kill cancelled"
				return m, nil
			}
			pid, name := m.killPID, m.killName
			m.confirmingKill = false
			return m, killProcessCmd(pid, name, sig)
		}

		switch msg.String() {
		case "ctrl+c":
			return m.quit()

		case "?":
			if m.anySearching() {
				break // a focused search box should receive the key
			}
			m.showHelp = true
			return m, nil
		case "q":
			if m.anySearching() {
				break
			}
			// disabled (no-op) so a stray 'q' can't quit
			return m, nil
		case "tab", "right":
			if m.anySearching() {
				break
			}
			m.activeTab = (m.activeTab + 1) % tabCount
			return m, nil
		case "shift+tab", "left":
			if m.anySearching() {
				break
			}
			m.activeTab = (m.activeTab + tabCount - 1) % tabCount
			return m, nil

		case "1", "2", "3", "4", "5":
			// Jump straight to a tab via its number (matches the "1 Overview" …
			// labels in the header). While a search box is focused let the digit
			// fall through to the text input instead of switching tabs.
			if !m.anySearching() {
				m.activeTab = tab(msg.String()[0] - '1')
				return m, nil
			}

		case "/":
			if m.anySearching() {
				break // a focused search box should receive a literal '/'
			}
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
			if m.activeTab == tabConns {
				m.connsSearching = true
				m.connsSearch.Focus()
				m.connsSearch.SetValue(m.connsQuery)
				return m, nil
			}

		case "ctrl+u":
			if m.activeTab == tabPorts && !m.portsSearching {
				m.portsQuery = ""
				m.portsSearch.SetValue("")
				m.portsCursor = 0
				m.portsText = m.renderPortsText()
				m.portsVP.SetContent(m.portsText)
				m.portsVP.SetYOffset(0)
				return m, nil
			}
			if m.activeTab == tabProcs && !m.procsSearching {
				m.procsQuery = ""
				m.procsSearch.SetValue("")
				m.procsCursor = 0
				m.procsText = m.renderProcsText()
				m.procsVP.SetContent(m.procsText)
				m.procsVP.SetYOffset(0)
				return m, nil
			}
			if m.activeTab == tabConns && !m.connsSearching {
				m.connsQuery = ""
				m.connsSearch.SetValue("")
				m.connsCursor = 0
				m.connsText = m.renderConnsText()
				m.connsVP.SetContent(m.connsText)
				m.connsVP.SetYOffset(0)
				return m, nil
			}

		case "ctrl+e":
			if m.anySearching() {
				break // let the input handle ctrl+e (jump to line end)
			}
			return m, fetchExternalIPCmd()

		case "r":
			if m.activeTab == tabConns && !m.connsSearching {
				m.connsRevDNS = !m.connsRevDNS
				m.connsCursor = 0
				var cmds []tea.Cmd
				if m.connsRevDNS {
					cmds = m.kickConnLookups()
				}
				m.connsText = m.renderConnsText()
				m.connsText = hardClipLinesToWidth(m.connsText, m.connsVP.Width)
				m.connsVP.SetContent(m.connsText)
				m.connsVP.SetYOffset(0)
				m.saveConfig()
				if len(cmds) > 0 {
					return m, tea.Batch(cmds...)
				}
				return m, nil
			}

		case "g":
			if m.activeTab == tabConns && !m.connsSearching {
				m.connsGroup = !m.connsGroup
				m.connsCursor = 0
				m.connsText = m.renderConnsText()
				m.connsText = hardClipLinesToWidth(m.connsText, m.connsVP.Width)
				m.connsVP.SetContent(m.connsText)
				m.connsVP.SetYOffset(0)
				m.saveConfig()
				return m, nil
			}

		case "h":
			// Hide-noise toggle: drop loopback/docker/bridge/veth and down
			// interfaces from the Interfaces list. Only meaningful off-search.
			if !m.anySearching() {
				m.hideNoise = !m.hideNoise
				m.rebuildIfaceList()
				m.ifaceDetailsText = hardClipLinesToWidth(m.renderIfaceDetailsText(m.ifaceDetailsVP.Width), m.ifaceDetailsVP.Width)
				m.ifaceDetailsVP.SetContent(m.ifaceDetailsText)
				m.status = "Noise filter: " + onOff(m.hideNoise)
				m.saveConfig()
				return m, nil
			}

		case "y":
			// Copy the highlighted row (or selected interface) to the clipboard.
			if !m.anySearching() {
				if text, what, ok := m.copyTarget(); ok {
					return m, copyToClipboardCmd(text, what)
				}
				return m, nil
			}

		case "s":
			// Cycle the sort column on the active table tab.
			if !m.anySearching() && m.isTableTab() {
				m.cycleSortColumn()
				return m, nil
			}

		case "S":
			// Toggle the sort direction on the active table tab.
			if !m.anySearching() && m.isTableTab() {
				m.toggleSortDir()
				return m, nil
			}

		case " ":
			// Pause / resume auto-refresh.
			if !m.anySearching() {
				m.paused = !m.paused
				if m.paused {
					m.status = "Auto-refresh paused"
				} else {
					m.status = "Auto-refresh resumed"
				}
				return m, nil
			}

		case "+", "=":
			if !m.anySearching() {
				m.setRefreshInterval(true)
				return m, nil
			}

		case "-", "_":
			if !m.anySearching() {
				m.setRefreshInterval(false)
				return m, nil
			}

		case "enter":
			// Show full, un-truncated details for the highlighted row.
			if !m.anySearching() && m.isTableTab() {
				if txt, ok := m.detailContent(); ok {
					m.detailText = txt
					m.showDetail = true
				}
				return m, nil
			}

		case "x":
			// Arm the kill-process confirmation for the highlighted row's PID.
			if !m.anySearching() && m.isTableTab() {
				if pid, name, ok := m.selectedPID(); ok {
					m.confirmingKill = true
					m.killPID = pid
					m.killName = name
				} else {
					m.status = "No killable process for this row"
				}
				return m, nil
			}

		case "f":
			// Cycle the connection state filter (Connections tab only).
			if !m.anySearching() && m.activeTab == tabConns {
				m.connStateFilter = (m.connStateFilter + 1) % 3
				m.applyFilterChange()
				return m, nil
			}

		case "o":
			// Toggle public-remotes-only (Connections tab only).
			if !m.anySearching() && m.activeTab == tabConns {
				m.publicOnly = !m.publicOnly
				m.applyFilterChange()
				return m, nil
			}

		case "p":
			// Cycle the protocol filter (Ports / Connections tabs).
			if !m.anySearching() && (m.activeTab == tabPorts || m.activeTab == tabConns) {
				m.protoFilter = (m.protoFilter + 1) % 3
				m.applyFilterChange()
				return m, nil
			}

		case "v":
			// Cycle the IP-version filter (Ports / Connections tabs).
			if !m.anySearching() && (m.activeTab == tabPorts || m.activeTab == tabConns) {
				m.ipVerFilter = (m.ipVerFilter + 1) % 3
				m.applyFilterChange()
				return m, nil
			}

		case "e":
			// Export the current (filtered/sorted) table view to a CSV file.
			if !m.anySearching() && m.isTableTab() {
				return m, exportViewCmd(m)
			}
		}
	}

	// Interfaces tab
	if m.activeTab == tabIfaces {
		var cmd tea.Cmd
		m.ifaceList, cmd = m.ifaceList.Update(msg)

		// Auto-follow selection
		if it, ok := m.ifaceList.SelectedItem().(ifaceItem); ok {
			if m.selectedIface != it.name {
				m.selectedIface = it.name
				m.rxHist, m.txHist = nil, nil

				m.ifaceDetailsText = m.renderIfaceDetailsText(m.ifaceDetailsVP.Width)
				m.ifaceDetailsText = hardClipLinesToWidth(m.ifaceDetailsText, m.ifaceDetailsVP.Width)
				m.ifaceDetailsVP.SetContent(m.ifaceDetailsText)
			}
		}

		var cmd2 tea.Cmd
		m.ifaceDetailsVP, cmd2 = m.ifaceDetailsVP.Update(msg)

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
				m.portsCursor = 0
				m.portsText = m.renderPortsText()
				m.portsVP.SetContent(m.portsText)
				m.portsVP.SetYOffset(0)
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
				m.procsCursor = 0
				m.procsText = m.renderProcsText()
				m.procsVP.SetContent(m.procsText)
				m.procsVP.SetYOffset(0)
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

	// Conns search mode
	if m.activeTab == tabConns && m.connsSearching {
		var cmd tea.Cmd
		m.connsSearch, cmd = m.connsSearch.Update(msg)

		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "enter":
				m.connsQuery = strings.TrimSpace(m.connsSearch.Value())
				m.connsSearching = false
				m.connsSearch.Blur()
				m.connsCursor = 0
				m.connsText = m.renderConnsText()
				m.connsVP.SetContent(m.connsText)
				m.connsVP.SetYOffset(0)
				return m, nil

			case "esc":
				m.connsSearching = false
				m.connsSearch.Blur()
				return m, nil

			case "ctrl+u":
				m.connsSearch.SetValue("")
				return m, cmd
			}
		}
		return m, cmd
	}

	// Table tabs (ports / procs / conns): row-cursor navigation, falling back
	// to viewport scrolling for non-navigation messages (e.g. mouse wheel).
	if m.isTableTab() {
		if handled, cmd := m.tableNav(msg); handled {
			return m, cmd
		}
		return m, nil
	}

	return m, nil
}

func (m Model) View() string {
	header := m.renderHeader()

	var body string
	switch {
	case m.confirmingKill:
		body = m.renderKillConfirm()
	case m.showDetail:
		body = m.renderDetail()
	case m.showHelp:
		body = m.renderHelp()
	case m.activeTab == tabOverview:
		body = m.viewOverview()
	case m.activeTab == tabIfaces:
		body = m.viewIfaces()
	case m.activeTab == tabPorts:
		body = m.viewPorts()
	case m.activeTab == tabProcs:
		body = m.viewProcs()
	case m.activeTab == tabConns:
		body = m.viewConns()
	}

	footer := subtleStyle.Render("Keys: ? help • 1-5 tabs • / search • ↑↓ select • enter details • s/S sort • f/o/p/v filter • y copy • e export • x kill • space pause • +/- rate")
	if m.status != "" {
		footer = okStyle.Render(m.status)
	}
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
		renderTab("5 Connections", m.activeTab == tabConns),
	}

	left := titleStyle.Render("ducknetview 🦆 0.0.5") + " " + subtleStyle.Render(fmt.Sprintf("(%dx%d)", m.w, m.h))
	if m.paused {
		left += " " + warnStyle.Render("⏸ PAUSED")
	}

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

func (m Model) renderHelp() string {
	var b strings.Builder

	section := func(name string) { b.WriteString(titleStyle.Render(name) + "\n") }
	row := func(k, d string) {
		b.WriteString("  " + okStyle.Render(padRight(k, 14)) + subtleStyle.Render(d) + "\n")
	}

	b.WriteString(titleStyle.Render("ducknetview 🦆 — key bindings") + "\n\n")

	section("Navigation")
	row("1–5", "jump directly to a tab")
	row("←/→, tab", "switch / cycle tabs")
	row("↑↓ (k/j)", "move row cursor / interface selection")
	row("PgUp/PgDn", "page the cursor")
	row("Home/End", "first / last row")
	row("mouse", "click a row to select it; wheel scrolls")
	b.WriteString("\n")

	section("Actions")
	row("enter", "show full details for the highlighted row")
	row("y", "copy highlighted row / selected interface")
	row("e", "export the current table to a CSV file")
	row("x", "signal the row's process (t/k/h = TERM/KILL/HUP)")
	row("s / S", "cycle sort column / reverse direction")
	row("/", "search (Enter apply · Esc exit · Ctrl+u clear)")
	row("h", "toggle hide-noise interface filter")
	row("ctrl+e", "refresh external IP now")
	b.WriteString("\n")

	section("Refresh")
	row("space", "pause / resume auto-refresh")
	row("+ / -", "faster / slower refresh interval")
	b.WriteString("\n")

	section("Filters (Ports / Connections)")
	row("p", "cycle protocol filter (all / tcp / udp)")
	row("v", "cycle IP version filter (all / v4 / v6)")
	row("f", "cycle connection state (all / established / active)")
	row("o", "connections: show public remotes only")
	b.WriteString("\n")

	section("Connections tab")
	row("r", "toggle reverse DNS for remote addresses")
	row("g", "toggle grouping by remote host")
	b.WriteString("\n")

	section("General")
	row("?", "toggle this help")
	row("ctrl+c", "quit")
	b.WriteString("\n")
	b.WriteString(subtleStyle.Render("press any key to close"))

	return boxStyle.Width(min(m.w-2, 120)).Height(m.bodyHeight()).Render(b.String())
}

func (m Model) viewOverview() string {
	if m.lastSnap.TakenAt.IsZero() {
		return boxStyle.Render("Collecting data…")
	}

	boxW := min(m.w-2, 120)

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
	b.WriteString(fmt.Sprintf("Ifaces: %d total  (%s up, %s down)\n",
		len(m.lastSnap.Ifaces),
		okStyle.Render(fmt.Sprintf("%d", up)),
		subtleStyle.Render(fmt.Sprintf("%d", down)),
	))

	refresh := fmt.Sprintf("Refresh: every %s", m.refreshInterval)
	if m.paused {
		refresh += "  " + warnStyle.Render("[PAUSED]")
	}
	b.WriteString(refresh + "\n")

	exposed, public, watch := m.securityCounts()
	countStyled := func(n int, warn bool) string {
		s := fmt.Sprintf("%d", n)
		if warn && n > 0 {
			return warnStyle.Render(s)
		}
		return s
	}
	sec := fmt.Sprintf("Security: %s exposed listeners · %s public conns",
		countStyled(exposed, true), countStyled(public, false))
	if watch > 0 {
		sec += " · " + errStyle.Render(fmt.Sprintf("%d watchlist hits", watch))
	}
	b.WriteString(sec + "\n\n")

	b.WriteString(titleStyle.Render("Selected interface") + "\n")
	// The box's text capacity is boxW minus its horizontal padding (2); size the
	// sparklines to that and hard-clip the rest so nothing wraps out of the box.
	details := boxW - 2
	b.WriteString(hardClipLinesToWidth(m.renderIfaceDetailsText(details), details))
	b.WriteString("\n")

	// Aggregate throughput across all real interfaces (loopback excluded).
	chartW := max(10, details-6)
	b.WriteString(titleStyle.Render("Total (all interfaces)") + "\n")
	b.WriteString(fmt.Sprintf("RX %s  (peak %s)\n%s\n",
		probe.HumanBytesPerSec(lastOr0(m.totalRxHist)),
		subtleStyle.Render(probe.HumanBytesPerSec(maxOf(m.totalRxHist))),
		Spark(m.totalRxHist, chartW)))
	b.WriteString(fmt.Sprintf("TX %s  (peak %s)\n%s\n\n",
		probe.HumanBytesPerSec(lastOr0(m.totalTxHist)),
		subtleStyle.Render(probe.HumanBytesPerSec(maxOf(m.totalTxHist))),
		Spark(m.totalTxHist, chartW)))

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

	return boxStyle.Width(boxW).Height(m.bodyHeight()).Render(b.String())
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

func (m Model) viewConns() string {
	connsW := min(m.w-2, 160)
	connsH := m.bodyHeight()

	if m.connsText == "" {
		m.connsText = m.renderConnsText()
		m.connsVP.SetContent(m.connsText)
	}

	searchLine := subtleStyle.Render("Press / to search")
	if m.connsQuery != "" {
		searchLine = subtleStyle.Render("Filter: ") + titleStyle.Render(m.connsQuery) + subtleStyle.Render("  (press / to change, ctrl+u to clear)")
	}
	if m.connsSearching {
		searchLine = m.connsSearch.View()
	}

	content := searchLine + "\n\n" + m.connsVP.View()
	return boxStyle.Width(connsW).Height(connsH).Render(content)
}

func (m Model) renderConnsText() string {
	if m.connsGroup {
		return m.renderConnsGrouped()
	}

	var b strings.Builder

	w := m.connsVP.Width
	if w <= 0 {
		w = 140
	}
	w -= gutterWidth // rows are prefixed with the cursor gutter

	colProto := 4
	colState := 11
	colAge := 6
	colPID := 7

	// Remaining width split between LOCAL, REMOTE, PROCESS.
	rest := w - (colProto + 2 + colState + 2 + colAge + 2 + colPID + 1)
	if rest < 30 {
		rest = 30
	}
	colLocal := max(16, min(28, rest/3))
	colRemote := max(18, min(32, rest/3))
	colProc := rest - colLocal - colRemote - 4
	if colProc < 8 {
		colProc = 8
	}

	sub := "↑↓ select • s/S sort: " + m.sortLabel() + " • y copy"
	if m.connSeen && (m.connAdded > 0 || m.connRemoved > 0) {
		sub += fmt.Sprintf(" • Δ %s/%s",
			newConnStyle.Render(fmt.Sprintf("+%d", m.connAdded)),
			subtleStyle.Render(fmt.Sprintf("-%d", m.connRemoved)))
	}
	if m.anyFilterActive() {
		sub += " • filter: " + m.filterLabel()
	}
	b.WriteString("Active connections (non-listening)\n")
	b.WriteString(sub + "\n\n")

	h := fmt.Sprintf("%s  %s  %s  %s  %s  %s %s\n",
		padRight("PR", colProto),
		padRight("LOCAL", colLocal),
		padRight("REMOTE", colRemote),
		padRight("STATE", colState),
		padRight("AGE", colAge),
		padRight("PID", colPID),
		"PROCESS",
	)
	b.WriteString(gutter(false) + h)
	b.WriteString(gutter(false) + strings.Repeat("─", min(w, colProto+2+colLocal+2+colRemote+2+colState+2+colAge+2+colPID+1+colProc)) + "\n")

	rows := m.filteredConns()
	if len(rows) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := m.connsQuery
	now := time.Now()

	for i, c := range rows {
		proc := c.Process
		if proc == "" {
			proc = "-"
		}

		remoteDisp := m.dnsDisplay(c.Remote)

		protoCell := padRight(trunc(c.Proto, colProto), colProto)
		proto := protoCell
		if m.newConns[connKey(c)] {
			proto = newConnStyle.Render(protoCell) // connection new since last poll
		}
		local := highlightFold(padRight(trunc(c.Local, colLocal), colLocal), q)
		// Watchlist hits win the color; otherwise color the remote by IP scope
		// when not searching, or show the search highlight (mixing scope color
		// with reverse-video is unreadable).
		remoteCell := padRight(trunc(remoteDisp, colRemote), colRemote)
		var remote string
		switch {
		case m.isWatched(c.Remote):
			remote = watchStyle.Render(remoteCell)
		case q == "":
			remote = scopeStyle(probe.IPScope(stripPort(c.Remote))).Render(remoteCell)
		default:
			remote = highlightFold(remoteCell, q)
		}
		// Pad before coloring so the ANSI codes don't throw off rune-based width.
		state := stateStyle(c.State).Render(padRight(trunc(c.State, colState), colState))
		age := padRight(trunc(formatAge(now, m.connFirstSeen[connKey(c)]), colAge), colAge)
		pid := padRight(fmt.Sprintf("%d", c.PID), colPID)
		procTr := highlightFold(trunc(proc, colProc), q)

		b.WriteString(gutter(i == m.connsCursor) + fmt.Sprintf("%s  %s  %s  %s  %s  %s %s\n", proto, local, remote, state, age, pid, procTr))
	}

	return b.String()
}

// renderConnsGrouped renders the connections tab collapsed by remote host:
// one row per remote IP/hostname with the connection count, a breakdown of
// connection states, and the set of processes talking to it.
func (m Model) renderConnsGrouped() string {
	var b strings.Builder

	w := m.connsVP.Width
	if w <= 0 {
		w = 140
	}
	w -= gutterWidth // rows are prefixed with the cursor gutter

	colRemote := max(18, min(40, w/3))
	colCount := 6

	rest := w - (colRemote + 2 + colCount + 2)
	if rest < 20 {
		rest = 20
	}
	colStates := max(12, rest/2)
	colProc := rest - colStates - 2
	if colProc < 8 {
		colProc = 8
	}

	sub := "↑↓ select • s/S sort: " + m.sortLabel() + " • y copy"
	if m.anyFilterActive() {
		sub += " • filter: " + m.filterLabel()
	}
	b.WriteString("Active connections grouped by remote host\n")
	b.WriteString(sub + "\n\n")

	b.WriteString(gutter(false) + fmt.Sprintf("%s  %s  %s  %s\n",
		padRight("REMOTE", colRemote),
		padRight("CONNS", colCount),
		padRight("STATES", colStates),
		"PROCESSES",
	))
	b.WriteString(gutter(false) + strings.Repeat("─", min(w, colRemote+2+colCount+2+colStates+2+colProc)) + "\n")

	groups := m.filteredGroups()
	if len(groups) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := m.connsQuery

	for i, g := range groups {
		host := g.ip
		if m.connsRevDNS && m.dnsCache != nil {
			if name, ok := m.dnsCache[g.ip]; ok && name != "" {
				host = name
			}
		}

		states := formatStates(g.states)
		procs := strings.Join(g.procs, ",")

		hostCell := padRight(trunc(host, colRemote), colRemote)
		var remoteCol string
		switch {
		case m.isWatchedIP(g.ip):
			remoteCol = watchStyle.Render(hostCell)
		case q == "":
			remoteCol = scopeStyle(probe.IPScope(g.ip)).Render(hostCell)
		default:
			remoteCol = highlightFold(hostCell, q)
		}
		countCol := padRight(fmt.Sprintf("%d", g.count), colCount)
		statesCol := padRight(trunc(states, colStates), colStates)
		procCol := highlightFold(trunc(procs, colProc), q)

		b.WriteString(gutter(i == m.connsCursor) + fmt.Sprintf("%s  %s  %s  %s\n", remoteCol, countCol, statesCol, procCol))
	}

	return b.String()
}

func (m Model) renderPortsText() string {
	var b strings.Builder

	w := m.portsVP.Width
	if w <= 0 {
		w = 120
	}
	w -= gutterWidth // rows are prefixed with the cursor gutter

	colProto := 4
	colSvc := 10
	colPID := 7
	colLocal := min(30, max(16, w-colProto-colSvc-colPID-2-2-2-1-10))

	sub := "↑↓ select • s/S sort: " + m.sortLabel() + " • y copy"
	if m.portSeen && len(m.newPorts) > 0 {
		sub += " • " + newConnStyle.Render(fmt.Sprintf("%d new", len(m.newPorts)))
	}
	if m.protoFilter != 0 || m.ipVerFilter != 0 {
		sub += " • filter: " + m.filterLabel()
	}
	b.WriteString("Open listening ports\n")
	b.WriteString(sub + "\n\n")

	b.WriteString(gutter(false) + fmt.Sprintf("%s  %s  %s  %s %s\n",
		padRight("PR", colProto),
		padRight("LOCAL", colLocal),
		padRight("SERVICE", colSvc),
		padRight("PID", colPID),
		"PROCESS",
	))
	b.WriteString(gutter(false) + strings.Repeat("─", min(w, colProto+2+colLocal+2+colSvc+2+colPID+1+7)) + "\n")

	rows := m.filteredPorts()
	if len(rows) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := m.portsQuery
	rest := w - (colProto + 2 + colLocal + 2 + colSvc + 2 + colPID + 1)
	if rest < 5 {
		rest = 5
	}

	for i, p := range rows {
		proc := p.Process
		if proc == "" {
			proc = "-"
		}
		local := p.Local
		if local == ":" || local == "0.0.0.0:0" {
			local = "-"
		}

		protoCell := padRight(trunc(p.Proto, colProto), colProto)
		proto := protoCell
		if m.newPorts[portKey(p)] {
			proto = newConnStyle.Render(protoCell) // listener new since last poll
		}
		// Color the bind address by exposure when not searching (exposed listeners
		// stand out); fall back to the search highlight while searching.
		localCell := padRight(trunc(local, colLocal), colLocal)
		var localTr string
		switch {
		case q != "":
			localTr = highlightFold(localCell, q)
		case local != "-":
			localTr = exposureStyle(probe.ListenerExposure(stripPort(p.Local))).Render(localCell)
		default:
			localTr = localCell
		}
		svcTr := highlightFold(padRight(trunc(probe.ServiceName(portOf(p.Local), p.Proto), colSvc), colSvc), q)
		procTr := highlightFold(trunc(proc, rest), q)
		pid := padRight(fmt.Sprintf("%d", p.PID), colPID)

		b.WriteString(gutter(i == m.portsCursor) + fmt.Sprintf("%s  %s  %s  %s %s\n", proto, localTr, svcTr, pid, procTr))
	}

	return b.String()
}

func (m Model) renderProcsText() string {
	var b strings.Builder

	w := m.procsVP.Width
	if w <= 0 {
		w = 120
	}
	w -= gutterWidth // rows are prefixed with the cursor gutter

	colPID := 7
	colCPU := 6
	colMem := 6
	colConns := 6
	colListen := 6

	minName := 14
	// 5 two-space separators between the six columns.
	colName := w - (colPID + colCPU + colMem + colConns + colListen + 10)
	if colName < minName {
		colName = minName
	}
	if colName > 34 {
		colName = 34
	}

	b.WriteString("Processes by network connections (proxy)\n")
	b.WriteString("↑↓ select • s/S sort: " + m.sortLabel() + " • y copy\n\n")

	h := fmt.Sprintf("%s  %s  %s  %s  %s  %s\n",
		padRight("PID", colPID),
		padRight("NAME", colName),
		padRight("CPU%", colCPU),
		padRight("MEM%", colMem),
		padRight("CONNS", colConns),
		padRight("LISTEN", colListen),
	)
	b.WriteString(gutter(false) + h)
	b.WriteString(gutter(false) + strings.Repeat("─", min(w, colPID+2+colName+2+colCPU+2+colMem+2+colConns+2+colListen)) + "\n")

	rows := m.filteredProcs()
	if len(rows) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := m.procsQuery

	for i, p := range rows {
		name := p.Name
		if name == "" {
			name = "-"
		}

		pidS := highlightFold(padRight(trunc(fmt.Sprintf("%d", p.PID), colPID), colPID), q)
		nameS := highlightFold(padRight(trunc(name, colName), colName), q)
		cpuS := padRight(trunc(fmt.Sprintf("%.1f", p.CPU), colCPU), colCPU)
		memS := padRight(trunc(fmt.Sprintf("%.1f", p.Mem), colMem), colMem)
		conS := padRight(trunc(fmt.Sprintf("%d", p.ConnCount), colConns), colConns)
		lisS := padRight(trunc(fmt.Sprintf("%d", p.ListenCount), colListen), colListen)

		b.WriteString(gutter(i == m.procsCursor) + fmt.Sprintf("%s  %s  %s  %s  %s  %s\n", pidS, nameS, cpuS, memS, conS, lisS))
	}

	return b.String()
}

var hlStyle = lipgloss.NewStyle().Reverse(true)

func highlightFold(s, q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return s
	}

	ls := strings.ToLower(s)
	lq := strings.ToLower(q)

	// strings.Index returns byte offsets into ls, so we must slice s with the
	// same byte offsets. That is only valid when lowercasing preserves byte
	// length (true for ASCII and the vast majority of text). If a rune changed
	// width under ToLower, the offsets would no longer line up — bail out and
	// return the original string unhighlighted rather than mis-slice or panic.
	if len(ls) != len(s) {
		return s
	}

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

		end := j + len(lq)
		out.WriteString(hlStyle.Render(s[j:end]))

		i = end
	}

	return out.String()
}

// renderIfaceDetailsText renders the selected interface's details. avail is the
// text width available to the caller's container (used to size the sparklines);
// it differs between the Interfaces detail pane and the Overview box, so it is
// passed in rather than read from a fixed viewport.
func (m Model) renderIfaceDetailsText(avail int) string {
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

	if avail <= 0 {
		avail = 40
	}
	chartW := max(10, avail-6)

	rx := Spark(m.rxHist, chartW)
	tx := Spark(m.txHist, chartW)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %s  %s  MTU %d\n", st.Render(state), titleStyle.Render(ii.Name), subtleStyle.Render(ii.Kind.Label()), ii.MTU))
	b.WriteString(fmt.Sprintf("MAC: %s\n", ii.Hardware))
	if len(ii.Addrs) > 0 {
		b.WriteString("Addrs: " + strings.Join(ii.Addrs, ", ") + "\n")
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("RX: %s  (total %s)\n%s\n\n",
		probe.HumanBytesPerSec(ii.RxBps), subtleStyle.Render(probe.HumanBytes(ii.RxTotal)), rx))
	b.WriteString(fmt.Sprintf("TX: %s  (total %s)\n%s\n",
		probe.HumanBytesPerSec(ii.TxBps), subtleStyle.Render(probe.HumanBytes(ii.TxTotal)), tx))
	return b.String()
}

// helpers

// anySearching reports whether a search input is currently focused on any tab.
func (m Model) anySearching() bool {
	return m.portsSearching || m.procsSearching || m.connsSearching
}

// isTableTab reports whether the active tab is one of the sortable, cursor-
// driven table tabs (ports / procs / conns).
func (m Model) isTableTab() bool {
	return m.activeTab == tabPorts || m.activeTab == tabProcs || m.activeTab == tabConns
}

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

// formatAge renders how long ago a connection was first seen, compactly (e.g.
// "42s", "3m07s", "1h05m", "2d03h"). A zero first-seen time renders as "-".
func formatAge(now, since time.Time) string {
	if since.IsZero() {
		return "-"
	}
	d := now.Sub(since)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// lastOr0 returns the most recent value of a history series, or 0 if empty.
func lastOr0(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	return s[len(s)-1]
}

// maxOf returns the largest value in a series, or 0 if empty.
func maxOf(s []float64) float64 {
	m := 0.0
	for _, v := range s {
		if v > m {
			m = v
		}
	}
	return m
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
