package ui

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"sort"
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
	"github.com/nexusriot/ducknetview/internal/version"
)

type tab int

const (
	tabOverview tab = iota
	tabIfaces
	tabPorts
	tabProcs
	tabConns
	tabEvents
	tabRoutes
	headerH  = 1
	footerH  = 1
	tabCount = 7
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

	eventsSearch     textinput.Model
	eventsSearching  bool
	eventsQuery      string
	eventLevelFilter int // 0 all · 1 warn and above · 2 alerts only

	// Whether a query hides non-matching rows or merely marks them (see
	// search.go). Highlight mode is what n/N navigate.
	searchMode searchMode

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

	// Settings overlay (toggled with `,`): the thresholds and lists that used
	// to be hand-edited JSON (see settings.go).
	showSettings    bool
	settingsCursor  int
	settingsEditing bool
	settingsInput   textinput.Model

	// Selected colour palette (see styles.go) and the key bindings in force
	// (see keymap.go).
	theme  string
	keymap keymap

	// Arrange the Processes tab as a parent/child tree (see proctree.go).
	procTree bool

	// Network ownership lookups (see probe/asn.go): the configured database
	// path, the loaded table and why it could not be loaded.
	asnPath string
	asnDB   *probe.ASNDB
	asnErr  error

	// Conns extras
	connsRevDNS bool
	connsGroup  bool

	// Retained history of connections that have gone away (see closed.go),
	// shown in place of the active list when showClosed is on.
	showClosed     bool
	closedConns    []closedConn
	prevConns      map[string]probe.Conn
	closedSortCol  int
	closedSortDesc bool

	dnsCache map[string]string // ip -> hostname (empty string means resolved but no PTR)
	dnsTried map[string]bool   // ip -> already attempted (don't retry within session)

	externalIP          string
	externalIPErr       error
	externalIPUpdatedAt time.Time

	// Refresh cadence control.
	refreshInterval time.Duration
	paused          bool
	lastConnFetch   time.Time

	// Session bookkeeping, reported by the metrics endpoint (see stats.go).
	// lastPollAt is the one number nothing else can give: a daemon that is up
	// but no longer polling looks healthy in every other metric.
	startedAt  time.Time
	lastPollAt time.Time
	polls      int

	// Row-detail overlay (toggled with `enter` on a row-cursor tab). It lives
	// in its own viewport so long command lines can wrap and scroll.
	showDetail bool
	detailText string
	detailVP   viewport.Model

	// Kill-process confirmation (armed with `x`, resolved with a signal key).
	confirmingKill bool
	killPID        int32
	killName       string

	// Aggregate bandwidth history across interfaces (Overview total sparkline).
	totalRxHist, totalTxHist []float64

	// A day of per-minute peaks that survives a restart (see trend.go).
	trend        trendHistory
	trendFlushed time.Time

	// Peaks for the whole session. The history slices are clamped to the chart
	// width, so their maximum is only the peak of what is still on screen.
	totalPeak  rateSample
	ifacePeaks map[string]rateSample

	// Connection tracking across enumerations: first-seen time per connection
	// key drives both the AGE column and the new/closed delta counters.
	connFirstSeen map[string]time.Time
	newConns      map[string]bool
	connSeen      bool
	connAdded     int
	connRemoved   int

	// Listening-port tracking across enumerations: newly-appeared listeners are
	// highlighted (a new listener can mean a backdoor or a regression). The
	// previous set keeps whole rows so a listener that goes away can still be
	// described in the event log.
	prevPorts map[string]probe.ListenPort
	newPorts  map[string]bool
	portSeen  bool

	// Event log (see events.go): a ring buffer of things that changed, plus the
	// bookkeeping needed to detect those changes.
	events        []event
	eventsVP      viewport.Model
	eventsText    string
	eventsCursor  int
	eventLog      *os.File
	eventLogPath  string
	eventLogBytes int64
	eventLogMax   int64 // rotation threshold; eventLogMaxBytes when zero
	eventLogErr   error
	eventEcho     io.Writer      // headless sessions mirror events here (see daemon.go)
	eventCounts   map[string]int // events recorded this session, by kind (see stats.go)
	alertCount    int
	hookErrors    int
	dstSeen       map[string]bool // public remotes already reported this session
	prevIfaceUp   map[string]bool

	// Throughput alert thresholds in bytes/sec (0 disables), the per-subject
	// re-fire suppression, and the optional external hook (see alerts.go).
	alertProcBps float64
	alertConnBps float64
	alertFired   map[string]time.Time
	alertHook    string
	pendingHooks []event

	// Non-throughput alert rules and the state their detection needs (see
	// alerts.go): retransmit deltas, per-process connection counts and how many
	// consecutive polls each process has been growing for.
	alertRules        alertRules
	prevIfaceProblems map[string]uint64
	lastIfaceSample   time.Time
	prevRetrans       map[string]uint32
	prevConnCounts    map[int32]int
	connGrowth        map[int32]int
	lastQualitySample time.Time

	// A drill from a process or listener into its connections (see drill.go),
	// nil when nothing is being followed.
	drill *drillTarget

	// Connections/ports quick filters (session-only, not persisted).
	connStateFilter int  // 0 all · 1 established · 2 active (exclude teardown)
	protoFilter     int  // 0 all · 1 tcp · 2 udp
	ipVerFilter     int  // 0 all · 1 IPv4 · 2 IPv6
	publicOnly      bool // show only connections to public (routable) remotes
	onlyMine        bool // show only rows owned by the current user

	// Username this process runs as, resolved once; "" when unavailable, which
	// disables the onlyMine filter.
	currentUser string

	// Per-socket throughput derived from consecutive enumerations of the
	// kernel's cumulative byte counters (see rates.go). hasThroughput latches
	// once the platform proves it can account for sockets, so the RX/TX columns
	// are hidden entirely where they would only ever read "-".
	hasThroughput  bool
	prevSockBytes  map[string]sockBytes
	connRates      map[string]rateSample
	procRates      map[int32]rateSample
	lastRateSample time.Time

	// Rolling per-row throughput history, for the detail-overlay sparklines.
	connRateHist map[string]*rateHistory
	procRateHist map[int32]*rateHistory

	// Cumulative session traffic (see totals.go). showTotals swaps the RX/TX
	// columns — and what the rx/tx sort keys order by — from current rate to
	// bytes moved this session.
	showTotals   bool
	sessionTotal byteTotals

	// Collapse processes that share a name into one row (see procrows.go).
	aggregateProcs bool

	// Columns the user has hidden in the config, by column key.
	hiddenCols map[string]bool

	// Which interface each connection's local address belongs to, resolved once
	// per poll, plus the interface the `i` filter is pinned to ("" for all).
	connIface   map[string]string
	ifaceFilter string

	// Whether throughput is worded in bytes or bits per second.
	rateUnit probe.RateUnit

	// Connections opened and closed per poll, for the Overview churn chart.
	churnUpHist, churnDownHist []float64

	// Alerts recorded before this moment are considered read, so the tab badge
	// can be cleared without destroying the log.
	alertsAckedAt time.Time

	// Quit confirmation, armed by `q`.
	confirmingQuit bool

	// Viewing a snapshot loaded from a file rather than the live host: nothing
	// is sampled, and the header says so. snapshotHostState records whether the
	// file was new enough to carry the remote host's routing configuration, so
	// the Routes tab can tell "not collected" from "nothing to show".
	snapshotOnly      bool
	snapshotHostState bool

	// A live remote session (see remote.go): the argv that produces a fresh
	// snapshot, the host it names, and the outcome of the last fetch.
	remoteArgv []string
	remoteHost string
	remoteErr  error
	remoteAt   time.Time
	remoteBusy bool

	// Host network configuration (see routes.go), refreshed on its own slow
	// cadence because it rarely changes.
	routes        []probe.Route
	neighbours    []probe.Neighbour
	resolvers     []probe.Resolver
	wifi          map[string]probe.WifiLink
	hostStateErr  error
	lastHostState time.Time
	routesVP      viewport.Model
	routesText    string

	// Path latency (see latency.go): the probed targets' most recent samples
	// and a short history each.
	latency      []probe.LatencySample
	latencyHist  map[string][]float64
	latencyExtra []string // extra targets from the config, beyond the gateway
	procTotals   map[int32]*talker
	hostTotals   map[string]*talker

	// Watchlist of remote addresses to highlight (loaded from config).
	watch        *watchList
	watchlistRaw []string

	// Expected listener set (see baseline.go), persisted so that "unexpected
	// listener" survives a restart.
	baseline    map[string]bool
	baselineRaw []string
	baselineAt  time.Time
}

// refreshSteps are the selectable auto-refresh intervals, cycled with `+`/`-`.
var refreshSteps = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	3 * time.Second,
	5 * time.Second,
	10 * time.Second,
}

// connFetchFloor is the fastest the ports/procs/conns views are ever
// re-enumerated. Enumerating every socket costs roughly twenty times an
// interface sample, so the socket views follow the chosen refresh rate but
// never run faster than this.
const connFetchFloor = 2 * time.Second

// socketInterval is how often the socket-derived views are refreshed.
func (m Model) socketInterval() time.Duration {
	if m.refreshInterval > connFetchFloor {
		return m.refreshInterval
	}
	return connFetchFloor
}

func NewModel() Model {
	ls := list.New([]list.Item{}, list.NewDefaultDelegate(), 30, 10)
	ls.Title = "Interfaces"
	ls.SetShowHelp(false)

	// viewports (sizes are set on WindowSizeMsg)
	pvp := viewport.New(0, 0)
	kvp := viewport.New(0, 0)
	dvp := viewport.New(0, 0)
	cvp := viewport.New(0, 0)
	evp := viewport.New(0, 0)
	rvp := viewport.New(0, 0)

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

	es := textinput.New()
	es.Placeholder = "search kind / subject / detail"
	es.Prompt = "/ "
	es.CharLimit = 64

	ss := textinput.New()
	ss.Placeholder = "value"
	ss.Prompt = "> "
	ss.CharLimit = 128

	m := Model{
		activeTab:  tabOverview,
		netSampler: probe.NewNetSampler(),

		ifaceList: ls,

		portsVP:        pvp,
		procsVP:        kvp,
		connsVP:        cvp,
		eventsVP:       evp,
		routesVP:       rvp,
		ifaceDetailsVP: dvp,
		portsSearch:    ps,
		procsSearch:    qs,
		connsSearch:    cs,
		eventsSearch:   es,

		// Default to the previous fixed orderings: procs & grouped conns by
		// connection count descending; ports & conns ascending by their first
		// column.
		procsSortDesc: true,
		groupSortDesc: true,

		refreshInterval: refreshSteps[0],
		lastConnFetch:   time.Now(),
		startedAt:       time.Now(),
		currentUser:     currentUsername(),
		settingsInput:   ss,
		theme:           defaultTheme,
	}
	return m
}

// currentUsername resolves the user this process runs as, for the "only my
// processes" filter. An error just leaves the filter inert.
func currentUsername() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

// WithConfig overlays persisted settings (sort keys, toggles, refresh rate)
// onto the model and returns it. Kept separate from NewModel so tests get clean
// defaults independent of any on-disk config. Call it from main.
func (m Model) WithConfig() Model {
	m.applyConfig()
	// Loaded here rather than in NewModel for the same reason the config is:
	// tests get a clean model, and only a real session reads the user's files.
	m.trend = loadTrend()
	m.trendFlushed = time.Now()
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
	// A live remote session samples the far host, never this one.
	if m.remoteLive() {
		return tea.Batch(fetchRemoteCmd(m.remoteArgv), tickEvery(m.refreshInterval))
	}
	// A snapshot describes another machine at another time. Sampling this one
	// would quietly replace it — so nothing is even asked for.
	if m.snapshotOnly {
		return nil
	}
	return tea.Batch(
		m.refreshCmd(),
		fetchConnInfoCmd(),
		fetchExternalIPCmd(),
		fetchHostStateCmd(),
		extIPTickEvery(30*time.Second),
		latencyTickEvery(latencyInterval),
		tickEvery(m.refreshInterval),
	)
}

// quit persists settings and ends the program. Used for every quit path so the
// config is always written on a clean exit.
func (m Model) quit() (tea.Model, tea.Cmd) {
	// A snapshot session forces some settings (totals, paused) and describes
	// another machine; persisting any of it would surprise the next live run.
	if !m.snapshotOnly {
		m.saveConfig()
		m.saveUsage()
		m.trend.save()
	}
	if m.eventLog != nil {
		_ = m.eventLog.Close()
	}
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

		// Events
		eventsW := min(m.w-2, 140)
		m.eventsVP.Width = max(10, eventsW-2)
		m.eventsVP.Height = max(5, bodyH-4)

		// Routes
		routesW := min(m.w-2, 140)
		m.routesVP.Width = max(10, routesW-2)
		m.routesVP.Height = max(5, bodyH-4)
		m.routesVP.SetContent(m.routesText)

		// Row-detail overlay
		if m.showDetail {
			m.setDetail(m.detailText)
		}

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
		if m.snapshotOnly {
			return m, nil // this host's address says nothing about the snapshot's
		}
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
		if m.remoteLive() {
			if !m.paused && !m.remoteBusy {
				m.remoteBusy = true // never start a second ssh behind a slow one
				cmds = append(cmds, fetchRemoteCmd(m.remoteArgv))
			}
			return m, tea.Batch(cmds...)
		}
		if m.snapshotOnly {
			return m, tea.Batch(cmds...) // a file has nothing more to give
		}
		if !m.paused {
			cmds = append(cmds, m.refreshCmd())
			if now := time.Now(); now.Sub(m.lastConnFetch) >= m.socketInterval() {
				m.lastConnFetch = now
				cmds = append(cmds, fetchConnInfoCmd())
			}
			if now := time.Now(); now.Sub(m.lastHostState) >= hostStateInterval {
				m.lastHostState = now
				cmds = append(cmds, fetchHostStateCmd())
			}
		}
		return m, tea.Batch(cmds...)

	case extIPTickMsg:
		return m, tea.Batch(fetchExternalIPCmd(), extIPTickEvery(30*time.Second))

	case snapMsg:
		if m.snapshotOnly {
			return m, nil
		}
		m.applyNetSnapshot(probe.NetSnapshot(msg))
		// The link-health rule fires from here, so its hooks have to be
		// launched from here too — the socket path may be seconds away.
		if cmds := m.drainAlertHooks(); len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case connInfoMsg:
		if m.snapshotOnly {
			return m, nil
		}
		cmds := m.applyConnInfo(probe.ConnInfo(msg), time.Now())
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil
	case remoteSnapshotMsg:
		m.remoteBusy = false
		if msg.err != nil {
			m.remoteErr = msg.err
			m.status = "Remote fetch failed: " + msg.err.Error()
			return m, nil
		}
		cmds := m.applyRemote(msg.snap)
		m.status = ""
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case DaemonShutdownMsg:
		// A headless session has no key handler, so this is its only way out —
		// and it must take the same exit path, or the day's usage is lost.
		return m.quit()

	case ReloadMsg:
		m.reloadConfig()
		return m, nil

	case StatsRequest:
		// Answered from the update loop, so the copy is consistent: the model
		// is never read from the HTTP goroutine. A non-blocking send means a
		// scraper that has already given up cannot wedge the loop.
		select {
		case msg.Reply <- m.sessionStats():
		default:
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

	case hostStateMsg:
		if m.snapshotOnly {
			return m, nil // the routing table of the wrong machine is worse than none
		}
		m.routes = msg.routes
		m.neighbours = msg.neighbours
		m.resolvers = msg.resolvers
		m.wifi = msg.wifi
		m.hostStateErr = msg.err
		m.routesText = hardClipLinesToWidth(m.renderRoutesText(), m.routesVP.Width)
		m.routesVP.SetContent(m.routesText)
		// The selected interface's radio state lives in the details pane.
		m.ifaceDetailsText = hardClipLinesToWidth(
			m.renderIfaceDetailsText(m.ifaceDetailsVP.Width), m.ifaceDetailsVP.Width)
		m.ifaceDetailsVP.SetContent(m.ifaceDetailsText)
		return m, nil

	case latencyTickMsg:
		cmds := []tea.Cmd{latencyTickEvery(latencyInterval)}
		if !m.paused {
			if probes := m.latencyTargets(); len(probes) > 0 {
				cmds = append(cmds, measureLatencyCmd(probes))
			}
		}
		return m, tea.Batch(cmds...)

	case latencyResultMsg:
		if m.snapshotOnly {
			return m, nil
		}
		m.recordLatency(msg.samples)
		return m, nil

	case alertHookResultMsg:
		if msg.err != nil {
			m.hookErrors++
			m.status = "Alert hook failed: " + msg.err.Error()
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

		// Rebinding happens here and nowhere else: every case below is written
		// against the default key, and a remapped press is dispatched as that
		// key. The original message is left alone, because the viewports and
		// text inputs below still need the key that was actually pressed.
		key := msg.String()
		if !m.anySearching() && !m.settingsEditing {
			key = m.keymap.translate(key)
		}

		// While the help overlay is open, any key closes it (ctrl+c still quits).
		if m.showHelp {
			if key == "ctrl+c" {
				return m.quit()
			}
			m.showHelp = false
			return m, nil
		}

		// The row-detail overlay scrolls and can be copied; any other key closes
		// it (the same dismiss-on-keypress behaviour as the help overlay).
		if m.showDetail {
			switch key {
			case "ctrl+c":
				return m.quit()
			case "up", "down", "k", "j", "pgup", "pgdown", "home", "end":
				var cmd tea.Cmd
				m.detailVP, cmd = m.detailVP.Update(msg)
				return m, cmd
			case "y":
				return m, copyToClipboardCmd(stripANSI(m.detailText), "row details")
			}
			m.showDetail = false
			return m, nil
		}

		// The settings overlay owns the keyboard while it is open: it has a
		// cursor and a text input of its own, and a key that fell through to
		// the tab underneath would act on a table nobody can see.
		if m.showSettings {
			if key == "ctrl+c" {
				return m.quit()
			}
			status, handled := m.settingsKey(key)
			if status != "" {
				m.status = status
			}
			if handled {
				if m.settingsEditing {
					var cmd tea.Cmd
					m.settingsInput, cmd = m.settingsInput.Update(msg)
					return m, cmd
				}
				return m, nil
			}
			m.showSettings = false
			return m, nil
		}

		// The quit confirmation captures all keys: y/q/enter leave, anything
		// else stays.
		if m.confirmingQuit {
			switch key {
			case "y", "Y", "q", "enter", "ctrl+c":
				return m.quit()
			}
			m.confirmingQuit = false
			m.status = "Still here"
			return m, nil
		}

		// The kill confirmation captures all keys: only 'y' proceeds, ctrl+c
		// quits, everything else cancels (fail-safe for a destructive action).
		if m.confirmingKill {
			var sig syscall.Signal
			switch key {
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

		switch key {
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
			// A bare `q` used to be a no-op so a stray keypress could not end the
			// session; asking instead is less surprising than doing nothing.
			m.confirmingQuit = true
			return m, nil

		case "U":
			// Bytes per second or bits per second: NICs and ISPs quote bits.
			if !m.anySearching() {
				if m.rateUnit == probe.UnitBytes {
					m.rateUnit = probe.UnitBits
					m.status = "Rates in bits/s"
				} else {
					m.rateUnit = probe.UnitBytes
					m.status = "Rates in bytes/s"
				}
				m.rerenderActiveTable()
				m.saveConfig()
				return m, nil
			}

		case "d":
			// Mark the alerts seen, without destroying the log.
			if !m.anySearching() && m.activeTab == tabEvents {
				m.alertsAckedAt = time.Now()
				n := m.unreadAlerts()
				m.rerenderActiveTable()
				m.status = fmt.Sprintf("Acknowledged alerts (%d unread)", n)
				return m, nil
			}

		case "i":
			// Cycle which interface's connections are shown.
			if !m.anySearching() && m.activeTab == tabConns {
				m.cycleIfaceFilter()
				return m, nil
			}
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

		case "1", "2", "3", "4", "5", "6", "7":
			// Jump straight to a tab via its number (matches the "1 Overview" …
			// labels in the header). While a search box is focused let the digit
			// fall through to the text input instead of switching tabs.
			if !m.anySearching() {
				m.activeTab = tab(key[0] - '1')
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
			if m.activeTab == tabEvents {
				m.eventsSearching = true
				m.eventsSearch.Focus()
				m.eventsSearch.SetValue(m.eventsQuery)
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
			if m.activeTab == tabEvents && !m.eventsSearching {
				m.eventsQuery = ""
				m.eventsSearch.SetValue("")
				m.eventsCursor = 0
				m.rerenderActiveTable()
				m.scrollActiveTableTop()
				return m, nil
			}

		case "ctrl+e":
			if m.anySearching() {
				break // let the input handle ctrl+e (jump to line end)
			}
			return m, fetchExternalIPCmd()

		case "R":
			// Refresh everything now, without waiting for the next tick.
			if !m.anySearching() {
				m.lastConnFetch = time.Now()
				m.lastHostState = time.Now()
				m.status = "Refreshing…"
				return m, tea.Batch(m.refreshCmd(), fetchConnInfoCmd(), fetchHostStateCmd(),
					measureLatencyCmd(m.latencyTargets()))
			}

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

		case "z":
			// Switch the Connections tab between the live list and the history
			// of connections that have closed.
			if m.activeTab == tabConns && !m.connsSearching {
				m.showClosed = !m.showClosed
				m.connsCursor = 0
				m.rerenderActiveTable()
				m.scrollActiveTableTop()
				if m.showClosed {
					m.status = fmt.Sprintf("Closed connections (%d retained)", len(m.closedConns))
				} else {
					m.status = "Active connections"
				}
				return m, nil
			}

		case "g":
			if m.activeTab == tabConns && !m.connsSearching {
				if m.showClosed {
					m.status = "Grouping applies to active connections (z to go back)"
					return m, nil
				}
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
				if m.snapshotOnly {
					m.status = "Viewing a snapshot — there is nothing to resume"
					return m, nil
				}
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
			if !m.anySearching() && m.hasRowCursor() {
				if txt, ok := m.detailContent(); ok {
					m.setDetail(txt)
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

		case "u":
			// Toggle "only my processes" (Ports / Processes / Connections).
			if !m.anySearching() && m.isTableTab() {
				if m.currentUser == "" {
					m.status = "Cannot determine the current user"
					return m, nil
				}
				m.onlyMine = !m.onlyMine
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
			if !m.anySearching() && m.hasRowCursor() {
				return m, exportViewCmd(m)
			}

		case "\\":
			// Switch between hiding non-matching rows and marking them in place.
			if !m.anySearching() && m.hasRowCursor() {
				if m.searchMode == searchFilter {
					m.searchMode = searchHighlight
				} else {
					m.searchMode = searchFilter
				}
				m.resetTableCursor()
				m.rerenderActiveTable()
				m.scrollActiveTableTop()
				m.status = "Search mode: " + m.searchMode.String()
				m.saveConfig()
				return m, nil
			}

		case "n", "N":
			// Walk the cursor between matching rows, which is what makes
			// highlight mode navigable.
			if !m.anySearching() && m.hasRowCursor() {
				if m.activeQuery() == "" {
					m.status = "No search query"
					return m, nil
				}
				if !m.jumpToMatch(key == "n") {
					m.status = "No matches"
				}
				return m, nil
			}

		case "A":
			// Fold processes that share a name into one row, and back.
			if !m.anySearching() && m.activeTab == tabProcs {
				m.aggregateProcs = !m.aggregateProcs
				if m.aggregateProcs {
					m.procTree = false // the two arrange the same rows differently
				}
				m.resetTableCursor()
				m.rerenderActiveTable()
				m.scrollActiveTableTop()
				if m.aggregateProcs {
					m.status = "Grouping processes by name"
				} else {
					m.status = "Showing individual processes"
				}
				m.saveConfig()
				return m, nil
			}

		case "t":
			// Swap the throughput columns between current rate and session total.
			if !m.anySearching() {
				m.showTotals = !m.showTotals
				m.rerenderActiveTable()
				if m.showTotals {
					m.status = "Showing session totals"
				} else {
					m.status = "Showing current rates"
				}
				m.saveConfig()
				return m, nil
			}

		case "l":
			// Cycle the event level filter (Events tab only).
			if !m.anySearching() && m.activeTab == tabEvents {
				m.eventLevelFilter = (m.eventLevelFilter + 1) % 3
				m.eventsCursor = 0
				m.rerenderActiveTable()
				m.scrollActiveTableTop()
				m.status = "Events: " + m.eventLevelLabel()
				return m, nil
			}

		case "c":
			// Clear the event log (Events tab only).
			if !m.anySearching() && m.activeTab == tabEvents {
				n := len(m.events)
				m.events = nil
				m.eventsCursor = 0
				m.rerenderActiveTable()
				m.scrollActiveTableTop()
				m.status = fmt.Sprintf("Cleared %d events", n)
				return m, nil
			}

		case "a":
			// Accept (or un-accept) the highlighted listener as expected.
			if !m.anySearching() && m.activeTab == tabPorts {
				rows := m.filteredPorts()
				if m.portsCursor < 0 || m.portsCursor >= len(rows) {
					m.status = "No listener selected"
					return m, nil
				}
				msg, ok := m.acceptListener(rows[m.portsCursor])
				m.status = msg
				if ok {
					m.rerenderActiveTable()
				}
				return m, nil
			}

		case "w":
			// Add (or remove) the highlighted remote from the watchlist.
			if !m.anySearching() && m.activeTab == tabConns {
				ip, ok := m.watchTarget()
				if !ok {
					m.status = "No connection selected"
					return m, nil
				}
				msg, _ := m.toggleWatch(ip)
				m.status = msg
				m.rerenderActiveTable()
				return m, nil
			}

		case ",":
			// The thresholds, the latency targets and the hidden columns had
			// no binding at all until now.
			if !m.anySearching() {
				m.openSettings()
				return m, nil
			}

		case "T":
			// Arrange processes by what started them.
			if !m.anySearching() && m.activeTab == tabProcs {
				m.procTree = !m.procTree
				if m.procTree && m.aggregateProcs {
					// Folding by name has already discarded the relationships
					// the tree is drawn from, so one has to give way.
					m.aggregateProcs = false
					m.status = "Process tree (name grouping off)"
				} else if m.procTree {
					m.status = "Process tree"
				} else {
					m.status = "Flat process list"
				}
				m.procsCursor = 0
				m.applyFilterChange()
				m.saveConfig()
				return m, nil
			}

		case "F":
			// Follow the highlighted row into the connections it accounts for.
			if !m.anySearching() && m.isTableTab() {
				msg, _ := m.drillInto()
				m.status = msg
				m.rerenderActiveTable()
				return m, nil
			}

		case "b":
			// Record the current listeners as the expected set.
			if !m.anySearching() {
				if len(m.ports) == 0 {
					m.status = "No listeners collected yet"
					return m, nil
				}
				m.setBaseline()
				m.rerenderActiveTable()
				m.status = fmt.Sprintf("Baseline saved: %d listeners", len(m.baselineRaw))
				return m, nil
			}

		case "B":
			// Forget the expected listener set.
			if !m.anySearching() {
				if !m.hasBaseline() {
					m.status = "No baseline saved"
					return m, nil
				}
				m.clearBaseline()
				m.rerenderActiveTable()
				m.status = "Baseline cleared"
				return m, nil
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

	// Events search mode
	if m.activeTab == tabEvents && m.eventsSearching {
		var cmd tea.Cmd
		m.eventsSearch, cmd = m.eventsSearch.Update(msg)

		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "enter":
				m.eventsQuery = strings.TrimSpace(m.eventsSearch.Value())
				m.eventsSearching = false
				m.eventsSearch.Blur()
				m.eventsCursor = 0
				m.rerenderActiveTable()
				m.scrollActiveTableTop()
				return m, nil

			case "esc":
				m.eventsSearching = false
				m.eventsSearch.Blur()
				return m, nil

			case "ctrl+u":
				m.eventsSearch.SetValue("")
				return m, cmd
			}
		}
		return m, cmd
	}

	// The routes tab is reference material: it scrolls, but has no rows to
	// select, sort or act on.
	if m.activeTab == tabRoutes {
		var cmd tea.Cmd
		m.routesVP, cmd = m.routesVP.Update(msg)
		return m, cmd
	}

	// Row-cursor tabs: cursor navigation, falling back to viewport scrolling
	// for non-navigation messages (e.g. mouse wheel).
	if m.hasRowCursor() {
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
	case m.confirmingQuit:
		body = m.renderQuitConfirm()
	case m.confirmingKill:
		body = m.renderKillConfirm()
	case m.showDetail:
		body = m.renderDetail()
	case m.showHelp:
		body = m.renderHelp()
	case m.showSettings:
		body = m.renderSettings()
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
	case m.activeTab == tabEvents:
		body = m.viewEvents()
	case m.activeTab == tabRoutes:
		body = m.viewRoutes()
	}

	footer := subtleStyle.Render("Keys: ? help • , settings • 1-7 tabs • / search • ↑↓ select • enter details • s/S sort • F follow • t totals • U units • A group • T tree • z closed • i/f/o/p/v/u filter • a/b baseline • w watch • d ack • y copy • e export • x kill • q quit")
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
		renderTab(eventsTabLabel(m.unreadAlerts()), m.activeTab == tabEvents),
		renderTab("7 Routes", m.activeTab == tabRoutes),
	}

	left := titleStyle.Render("ducknetview 🦆 "+version.Version) + " " + subtleStyle.Render(fmt.Sprintf("(%dx%d)", m.w, m.h))
	switch {
	case m.remoteLive():
		// A remote session is live, but it is emphatically not this host — the
		// header has to say whose numbers these are.
		label := "⇄ " + m.remoteHost
		if m.remoteErr != nil {
			left += " " + errStyle.Render(label+" (stale)")
		} else if m.paused {
			left += " " + warnStyle.Render(label+" ⏸ PAUSED")
		} else {
			left += " " + okStyle.Render(label)
		}
	case m.snapshotOnly:
		left += " " + warnStyle.Render("⏸ SNAPSHOT")
	case m.paused:
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
	// Rendered from the keymap, so a rebound key is shown as it is bound now
	// rather than as it shipped.
	bound := func(action, d string) { row(m.keymap.keyFor(action), d) }

	b.WriteString(titleStyle.Render("ducknetview 🦆 — key bindings") + "\n\n")

	section("Navigation")
	row("1–7", "jump directly to a tab")
	row("←/→, tab", "switch / cycle tabs")
	row("↑↓ (k/j)", "move row cursor / interface selection")
	row("PgUp/PgDn", "page the cursor")
	row("Home/End", "first / last row")
	row("mouse", "click a row to select it; wheel scrolls")
	b.WriteString("\n")

	section("Actions")
	bound("details", "row details (↑↓ scrolls, y copies, any key closes)")
	bound("copy", "copy highlighted row / selected interface")
	bound("export", "export the current table to a CSV file")
	bound("signal", "signal the row's process (t/k/h = TERM/KILL/HUP)")
	bound("follow", "follow a process or listener into its connections")
	row(m.keymap.keyFor("sort")+" / "+m.keymap.keyFor("sort-reverse"), "cycle sort column / reverse direction")
	row("click", "click a column header to sort by it, again to reverse")
	bound("totals", "throughput columns: current rate / session total")
	bound("rate-unit", "rates in bytes/s or bits/s")
	bound("aggregate", "processes: fold rows sharing a name into one")
	bound("tree", "processes: arrange by what started them")
	bound("hide-noise", "toggle hide-noise interface filter")
	bound("external-ip", "refresh external IP now")
	b.WriteString("\n")

	section("Search")
	bound("search", "search (Enter apply · Esc exit · Ctrl+u clear)")
	row("re:…", "treat the query as a regular expression")
	bound("search-mode", "switch between filtering rows and highlighting them")
	row(m.keymap.keyFor("search-next")+" / "+m.keymap.keyFor("search-prev"), "jump to the next / previous match")
	b.WriteString("\n")

	section("Refresh")
	row(spaceLabel(m.keymap.keyFor("pause")), "pause / resume auto-refresh")
	row("+ / -", "faster / slower refresh interval")
	bound("refresh", "refresh everything now")
	b.WriteString("\n")

	section("Filters (Ports / Processes / Connections)")
	bound("filter-proto", "cycle protocol filter (all / tcp / udp)")
	bound("filter-ipver", "cycle IP version filter (all / v4 / v6)")
	bound("filter-state", "cycle connection state (all / established / active)")
	bound("filter-public", "connections: show public remotes only")
	bound("filter-mine", "show only my processes")
	b.WriteString("\n")

	section("Connections tab")
	bound("reverse-dns", "toggle reverse DNS for remote addresses")
	bound("group", "toggle grouping by remote host")
	bound("closed", "show connections that have closed, with final totals")
	bound("filter-iface", "filter by the interface the connection uses")
	bound("watch", "add the highlighted remote to the watchlist")
	bound("follow", "go back to the row this view was opened from")
	b.WriteString("\n")

	section("Listener baseline")
	bound("baseline-save", "save the current listeners as the expected set")
	bound("baseline-accept", "accept just the highlighted listener into the baseline")
	bound("baseline-clear", "clear the saved baseline")
	b.WriteString("\n")

	section("Events tab")
	bound("events-level", "cycle the level filter (all / warn+ / alerts)")
	bound("events-ack", "acknowledge alerts (clears the badge, keeps the log)")
	bound("events-clear", "clear the recorded events")
	b.WriteString("\n")

	section("General")
	bound("help", "toggle this help")
	bound("settings", "settings: alert thresholds, latency targets, columns, theme")
	bound("quit", "quit (asks first)")
	row("ctrl+c", "quit immediately")
	if len(m.keymap.overrides()) > 0 {
		b.WriteString(subtleStyle.Render("Keys shown are your own bindings, from \"keys\" in the config file.") + "\n\n")
	}
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
	if m.hasBaseline() {
		if n := m.offBaselineCount(); n > 0 {
			sec += " · " + errStyle.Render(fmt.Sprintf("%d off-baseline", n))
		} else {
			sec += " · " + okStyle.Render("baseline clean")
		}
	}
	b.WriteString(sec + "\n")
	b.WriteString(clampToWidthOneLine(m.gatewayLine(), boxW-2) + "\n\n")

	b.WriteString(titleStyle.Render("Selected interface") + "\n")
	// The box's text capacity is boxW minus its horizontal padding (2); size the
	// sparklines to that and hard-clip the rest so nothing wraps out of the box.
	details := boxW - 2
	b.WriteString(hardClipLinesToWidth(m.renderIfaceDetailsText(details), details))
	b.WriteString("\n")

	// Aggregate throughput across all real interfaces (loopback excluded).
	chartW := max(10, details-6)
	b.WriteString(titleStyle.Render("Total (all interfaces)") + "\n")
	b.WriteString(fmt.Sprintf("RX %s  (session peak %s)\n%s\n",
		m.rate(lastOr0(m.totalRxHist)),
		subtleStyle.Render(m.rate(m.totalPeak.rx)),
		Spark(m.totalRxHist, chartW)))
	b.WriteString(fmt.Sprintf("TX %s  (session peak %s)\n%s\n\n",
		m.rate(lastOr0(m.totalTxHist)),
		subtleStyle.Render(m.rate(m.totalPeak.tx)),
		Spark(m.totalTxHist, chartW)))

	// Top talkers are only worth the vertical space once traffic has been
	// accounted, and only when the box is tall enough to hold them without
	// pushing the external IP out of view.
	for _, panel := range []string{m.renderTrend(details), m.renderLatency(details), m.renderTopTalkers(details), m.renderChurn(details)} {
		if panel == "" {
			continue
		}
		used := strings.Count(b.String(), "\n")
		if used+strings.Count(panel, "\n")+3 <= m.bodyHeight() {
			b.WriteString(panel)
		}
	}

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
		searchLine = m.searchStatus(m.portsQuery)
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
		searchLine = m.searchStatus(m.procsQuery)
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
		searchLine = m.searchStatus(m.connsQuery)
	}
	if m.connsSearching {
		searchLine = m.connsSearch.View()
	}

	content := searchLine + "\n\n" + m.connsVP.View()
	return boxStyle.Width(connsW).Height(connsH).Render(content)
}

// connsColumns is the flat Connections table's layout.
func (m Model) connsColumns() colSpecs {
	w := m.connsVP.Width
	if w <= 0 {
		w = 140
	}
	w -= gutterWidth

	colProto := 4
	colSvc := 9
	colState := 11
	colAge := 6
	colRate := 5
	colRTT := 6
	colRTX := 4
	colIface := 8
	colPID := 7
	colOwner := 18

	// The interface column only earns its width when connections actually span
	// more than one link — on a single-NIC host every row would say the same
	// thing.
	showIface := m.multiIface()
	// The owner column exists only when a database was loaded, so a host
	// without one is not given a column of dashes.
	showOwner := m.asnDB.Len() > 0

	// Remaining width split between LOCAL, REMOTE, PROCESS.
	fixed := colProto + 2 + colSvc + 2 + colState + 2 + colAge + 2 + colPID + 2
	if showIface {
		fixed += colIface + 2
	}
	if showOwner {
		fixed += colOwner + 2
	}
	if m.hasThroughput {
		fixed += 2*(colRate+2) + colRTT + 2 + colRTX + 2
	}
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

	cols := colSpecs{
		{key: "proto", title: "PR", width: colProto, sort: "proto"},
		{key: "local", title: "LOCAL", width: colLocal, sort: "local"},
		{key: "remote", title: "REMOTE", width: colRemote, sort: "remote"},
		{key: "svc", title: "SVC", width: colSvc},
		{key: "state", title: "STATE", width: colState, sort: "state"},
		{key: "age", title: "AGE", width: colAge, sort: "age"},
	}
	if showOwner {
		cols = append(cols, colSpec{key: "owner", title: "OWNER", width: colOwner, sort: "owner"})
	}
	if showIface {
		cols = append(cols, colSpec{key: "iface", title: "IFACE", width: colIface, sort: "iface"})
	}
	if m.hasThroughput {
		rxHdr, txHdr := m.trafficHeaders()
		cols = append(cols,
			colSpec{key: "rx", title: rxHdr, width: colRate, sort: "rx"},
			colSpec{key: "tx", title: txHdr, width: colRate, sort: "tx"},
			colSpec{key: "rtt", title: "RTT", width: colRTT, sort: "rtt"},
			colSpec{key: "rtx", title: "RTX", width: colRTX, sort: "retrans"})
	}
	return m.visible(append(cols,
		colSpec{key: "pid", title: "PID", width: colPID, sort: "pid"},
		colSpec{key: "process", title: "PROCESS", width: colProc, sort: "process"}))
}

func (m Model) renderConnsText() string {
	if m.showClosed {
		return m.renderConnsClosed()
	}
	if m.connsGroup {
		return m.renderConnsGrouped()
	}

	var b strings.Builder

	w := m.connsVP.Width
	if w <= 0 {
		w = 140
	}
	w -= gutterWidth // rows are prefixed with the cursor gutter

	cols := m.connsColumns()
	colProto := cols.width("proto")
	colLocal := cols.width("local")
	colRemote := cols.width("remote")
	colSvc := cols.width("svc")
	colState := cols.width("state")
	colAge := cols.width("age")
	colRate := cols.width("rx")
	colRTT := cols.width("rtt")
	colRTX := cols.width("rtx")
	colIface := cols.width("iface")
	colOwner := cols.width("owner")
	colPID := cols.width("pid")
	colProc := cols.width("process")

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

	b.WriteString(gutter(false) + cols.header(m.sortKey(), m.sortDesc()) + "\n")
	b.WriteString(gutter(false) + strings.Repeat("─", min(w, cols.span())) + "\n")

	rows := m.filteredConns()
	if len(rows) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := newMatcher(m.connsQuery)
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
		local := q.highlight(padRight(trunc(c.Local, colLocal), colLocal))
		// Watchlist hits win the color; otherwise color the remote by IP scope
		// when not searching, or show the search highlight (mixing scope color
		// with reverse-video is unreadable).
		remoteCell := padRight(trunc(remoteDisp, colRemote), colRemote)
		var remote string
		switch {
		case m.isWatched(c.Remote):
			remote = watchStyle.Render(remoteCell)
		case q.empty():
			remote = scopeStyle(probe.IPScope(stripPort(c.Remote))).Render(remoteCell)
		default:
			remote = q.highlight(remoteCell)
		}
		svc := q.highlight(padRight(trunc(probe.ServiceName(portOf(c.Remote), c.Proto), colSvc), colSvc))
		// Pad before coloring so the ANSI codes don't throw off rune-based width.
		state := stateStyle(c.State).Render(padRight(trunc(c.State, colState), colState))
		age := padRight(trunc(formatAge(now, m.connFirstSeen[connKey(c)]), colAge), colAge)
		pid := padRight(fmt.Sprintf("%d", c.PID), colPID)
		procTr := q.highlight(trunc(proc, colProc))

		cells := map[string]string{
			"proto": proto, "local": local, "remote": remote, "svc": svc,
			"state": state, "age": age, "pid": pid, "process": procTr,
			"iface": padRight(trunc(dashIfEmpty(m.ifaceFor(c)), colIface), colIface),
			"owner": q.highlight(padRight(trunc(dashIfEmpty(m.ownerOf(c.Remote)), colOwner), colOwner)),
		}
		if m.hasThroughput {
			cells["rx"], cells["tx"] = m.trafficCells(m.connRate(c), m.connTotal(c), colRate)
			cells["rtt"] = padRight(formatRTT(c.RTT), colRTT)
			// Retransmits are the difference between "slow" and "lossy", so a
			// non-zero count is called out rather than just printed.
			rtx := padRight(formatRetrans(c.Retrans), colRTX)
			if c.Retrans > 0 {
				cells["rtx"] = warnStyle.Render(rtx)
			} else {
				cells["rtx"] = subtleStyle.Render(rtx)
			}
		}

		b.WriteString(gutter(i == m.connsCursor) + cols.row(cells) + "\n")
	}

	return b.String()
}

// groupColumns is the grouped-by-host view's layout.
func (m Model) groupColumns() colSpecs {
	w := m.connsVP.Width
	if w <= 0 {
		w = 140
	}
	w -= gutterWidth

	colRemote := max(18, min(40, w/3))
	colCount := 6
	colRate := 5

	fixed := colRemote + 2 + colCount + 2
	if m.hasThroughput {
		fixed += 2 * (colRate + 2)
	}
	rest := w - fixed
	if rest < 20 {
		rest = 20
	}
	colStates := max(12, rest/2)
	colProc := rest - colStates - 2
	if colProc < 8 {
		colProc = 8
	}

	cols := colSpecs{
		{key: "host", title: "REMOTE", width: colRemote, sort: "host"},
		{key: "count", title: "CONNS", width: colCount, sort: "conns"},
	}
	if m.hasThroughput {
		rxHdr, txHdr := m.trafficHeaders()
		cols = append(cols,
			colSpec{key: "rx", title: rxHdr, width: colRate, sort: "bytes"},
			colSpec{key: "tx", title: txHdr, width: colRate, sort: "bytes"})
	}
	return m.visible(append(cols,
		colSpec{key: "states", title: "STATES", width: colStates},
		colSpec{key: "procs", title: "PROCESSES", width: colProc}))
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
	w -= gutterWidth

	cols := m.groupColumns()
	colRemote := cols.width("host")
	colCount := cols.width("count")
	colRate := cols.width("rx")
	colStates := cols.width("states")
	colProc := cols.width("procs")

	sub := "↑↓ select • s/S sort: " + m.sortLabel() + " • y copy"
	if m.anyFilterActive() {
		sub += " • filter: " + m.filterLabel()
	}
	b.WriteString("Active connections grouped by remote host\n")
	b.WriteString(sub + "\n\n")

	b.WriteString(gutter(false) + cols.header(m.sortKey(), m.sortDesc()) + "\n")
	b.WriteString(gutter(false) + strings.Repeat("─", min(w, cols.span())) + "\n")

	groups := m.filteredGroups()
	if len(groups) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := newMatcher(m.connsQuery)

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
		case q.empty():
			remoteCol = scopeStyle(probe.IPScope(g.ip)).Render(hostCell)
		default:
			remoteCol = q.highlight(hostCell)
		}
		countCol := padRight(fmt.Sprintf("%d", g.count), colCount)
		statesCol := padRight(trunc(states, colStates), colStates)
		procCol := q.highlight(trunc(procs, colProc))

		cells := map[string]string{
			"host": remoteCol, "count": countCol, "states": statesCol, "procs": procCol,
		}
		if m.hasThroughput {
			cells["rx"], cells["tx"] = m.trafficCells(m.groupRate(g), m.hostTotal(g.ip), colRate)
		}

		b.WriteString(gutter(i == m.connsCursor) + cols.row(cells) + "\n")
	}

	return b.String()
}

// portsColumns is the Ports table's layout: the single description the header,
// the rows and the click-to-sort hit-testing all read.
func (m Model) portsColumns() colSpecs {
	w := m.portsVP.Width
	if w <= 0 {
		w = 120
	}
	w -= gutterWidth // rows are prefixed with the cursor gutter

	colProto := 4
	colSvc := 10
	colUser := 10
	colPID := 7
	colLocal := min(30, max(16, w-colProto-colSvc-colUser-colPID-2-2-2-2-1-10))
	rest := w - (colProto + 2 + colLocal + 2 + colSvc + 2 + colUser + 2 + colPID + 2)
	if rest < 5 {
		rest = 5
	}

	return m.visible(colSpecs{
		{key: "proto", title: "PR", width: colProto, sort: "proto"},
		{key: "local", title: "LOCAL", width: colLocal, sort: "port"},
		{key: "svc", title: "SERVICE", width: colSvc},
		{key: "user", title: "USER", width: colUser, sort: "user"},
		{key: "pid", title: "PID", width: colPID, sort: "pid"},
		{key: "process", title: "PROCESS", width: rest, sort: "process"},
	})
}

func (m Model) renderPortsText() string {
	var b strings.Builder

	w := m.portsVP.Width
	if w <= 0 {
		w = 120
	}
	w -= gutterWidth

	cols := m.portsColumns()
	colProto := cols.width("proto")
	colLocal := cols.width("local")
	colSvc := cols.width("svc")
	colUser := cols.width("user")
	colPID := cols.width("pid")
	rest := cols.width("process")

	sub := "↑↓ select • s/S sort: " + m.sortLabel() + " • y copy"
	if m.portSeen && len(m.newPorts) > 0 {
		sub += " • " + newConnStyle.Render(fmt.Sprintf("%d new", len(m.newPorts)))
	}
	if n := m.offBaselineCount(); n > 0 {
		sub += " • " + errStyle.Render(fmt.Sprintf("%d off-baseline", n))
	}
	if m.protoFilter != 0 || m.ipVerFilter != 0 || m.onlyMine {
		sub += " • filter: " + m.filterLabel()
	}
	b.WriteString("Open listening ports\n")
	b.WriteString(sub + "\n\n")

	b.WriteString(gutter(false) + cols.header(m.sortKey(), m.sortDesc()) + "\n")
	b.WriteString(gutter(false) + strings.Repeat("─", min(w, cols.span())) + "\n")

	rows := m.filteredPorts()
	if len(rows) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := newMatcher(m.portsQuery)

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
		switch {
		case m.offBaseline(p):
			// Unexpected listeners outrank merely-new ones: this one is not in
			// the set the user declared normal.
			proto = errStyle.Render(protoCell)
		case m.newPorts[portKey(p)]:
			proto = newConnStyle.Render(protoCell) // listener new since last poll
		}
		// Color the bind address by exposure when not searching (exposed listeners
		// stand out); fall back to the search highlight while searching.
		localCell := padRight(trunc(local, colLocal), colLocal)
		var localTr string
		switch {
		case !q.empty():
			localTr = q.highlight(localCell)
		case local != "-":
			localTr = exposureStyle(probe.ListenerExposure(stripPort(p.Local))).Render(localCell)
		default:
			localTr = localCell
		}
		svcTr := q.highlight(padRight(trunc(probe.ServiceName(portOf(p.Local), p.Proto), colSvc), colSvc))
		userTr := q.highlight(padRight(trunc(userOrDash(p.User), colUser), colUser))
		procTr := q.highlight(trunc(proc, rest))
		pid := padRight(fmt.Sprintf("%d", p.PID), colPID)

		b.WriteString(gutter(i == m.portsCursor) + cols.row(map[string]string{
			"proto": proto, "local": localTr, "svc": svcTr,
			"user": userTr, "pid": pid, "process": procTr,
		}) + "\n")
	}

	return b.String()
}

// procsColumns is the Processes table's layout. The CONTAINER column only earns
// its width on a host actually running containers, and the throughput columns
// only where the kernel can account for sockets.
func (m Model) procsColumns() colSpecs {
	w := m.procsVP.Width
	if w <= 0 {
		w = 120
	}
	w -= gutterWidth

	colPID := 7
	colUser := 10
	colCont := 12
	colCPU := 6
	colMem := 6
	colRate := 5
	colConns := 6
	colListen := 6

	showCont := m.anyContainer()

	// Seven base columns, so six two-space separators.
	fixed := colPID + colUser + colCPU + colMem + colConns + colListen + 12
	if showCont {
		fixed += colCont + 2
	}
	if m.hasThroughput {
		fixed += 2 * (colRate + 2)
	}
	colName := clampInt(w-fixed, 14, 34)

	cols := colSpecs{
		{key: "pid", title: "PID", width: colPID, sort: "pid"},
		{key: "name", title: "NAME", width: colName, sort: "name"},
		{key: "user", title: "USER", width: colUser, sort: "user"},
	}
	if showCont {
		cols = append(cols, colSpec{key: "container", title: "CONTAINER", width: colCont})
	}
	cols = append(cols,
		colSpec{key: "cpu", title: "CPU%", width: colCPU, sort: "cpu"},
		colSpec{key: "mem", title: "MEM%", width: colMem, sort: "mem"})
	if m.hasThroughput {
		rxHdr, txHdr := m.trafficHeaders()
		cols = append(cols,
			colSpec{key: "rx", title: rxHdr, width: colRate, sort: "rx"},
			colSpec{key: "tx", title: txHdr, width: colRate, sort: "tx"})
	}
	return m.visible(append(cols,
		colSpec{key: "conns", title: "CONNS", width: colConns, sort: "conns"},
		colSpec{key: "listen", title: "LISTEN", width: colListen, sort: "listen"}))
}

func (m Model) renderProcsText() string {
	var b strings.Builder

	w := m.procsVP.Width
	if w <= 0 {
		w = 120
	}
	w -= gutterWidth

	cols := m.procsColumns()
	showCont := m.anyContainer()
	colPID := cols.width("pid")
	colName := cols.width("name")
	colUser := cols.width("user")
	colCont := cols.width("container")
	colCPU := cols.width("cpu")
	colMem := cols.width("mem")
	colRate := cols.width("rx")
	colConns := cols.width("conns")
	colListen := cols.width("listen")

	b.WriteString("Processes by network connections (proxy)\n")
	sub := "↑↓ select • s/S sort: " + m.sortLabel() + " • y copy • A "
	if m.aggregateProcs {
		sub += "per-process"
	} else {
		sub += "group by name"
	}
	sub += " • T "
	if m.procTree {
		sub += "flat"
	} else {
		sub += "tree"
	}
	if m.onlyMine {
		sub += " • filter: " + m.filterLabel()
	}
	b.WriteString(sub + "\n\n")

	b.WriteString(gutter(false) + cols.header(m.sortKey(), m.sortDesc()) + "\n")
	b.WriteString(gutter(false) + strings.Repeat("─", min(w, cols.span())) + "\n")

	rows := m.filteredProcs()
	if len(rows) == 0 {
		b.WriteString("No data (yet)…\n")
		return b.String()
	}

	q := newMatcher(m.procsQuery)

	for i, p := range rows {
		name := p.Name
		if name == "" {
			name = "-"
		}
		// The tree indents the name rather than adding a column: the table is
		// already as wide as it can be on an 80-column terminal.
		name = treePrefix(p.depth) + name

		pidS := q.highlight(padRight(trunc(p.pidCell(), colPID), colPID))
		nameS := q.highlight(padRight(trunc(name, colName), colName))
		userS := q.highlight(padRight(trunc(userOrDash(p.User), colUser), colUser))
		cpuS := padRight(trunc(fmt.Sprintf("%.1f", p.CPU), colCPU), colCPU)
		memS := padRight(trunc(fmt.Sprintf("%.1f", p.Mem), colMem), colMem)
		conS := padRight(trunc(fmt.Sprintf("%d", p.ConnCount), colConns), colConns)
		lisS := padRight(trunc(fmt.Sprintf("%d", p.ListenCount), colListen), colListen)

		contS := ""
		if showCont {
			cell := padRight(trunc(dashIfEmpty(p.Container), colCont), colCont)
			if p.Container == "" {
				cell = subtleStyle.Render(cell)
			} else {
				cell = q.highlight(cell)
			}
			contS = cell + colSep
		}

		rateSeg := ""
		if m.hasThroughput {
			rx, tx := m.trafficCells(m.procRate(p.PID), m.procTotal(p.PID), colRate)
			rateSeg = rx + colSep + tx + colSep
		}

		b.WriteString(gutter(i == m.procsCursor) + fmt.Sprintf("%s  %s  %s  %s%s  %s  %s%s\n",
			pidS, nameS, userS, contS, cpuS, memS, rateSeg, conS+colSep+lisS))
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
	if w, ok := m.wifi[ii.Name]; ok {
		// On a laptop or a handheld, "the network is slow" is very often "the
		// radio is weak", which no amount of socket accounting reveals.
		line := fmt.Sprintf("Wi-Fi: %.0f dBm (%s) · quality %d%%", w.Signal, w.Label(), w.SignalPercent())
		if w.Noise != 0 {
			line += fmt.Sprintf(" · noise %.0f dBm", w.Noise)
		}
		if w.SignalPercent() < 40 {
			line = warnStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if len(ii.Addrs) > 0 {
		b.WriteString("Addrs: " + strings.Join(ii.Addrs, ", ") + "\n")
	}
	b.WriteString("\n")

	peak := m.ifacePeaks[ii.Name]
	b.WriteString(fmt.Sprintf("RX: %s  (total %s, peak %s)\n%s\n\n",
		m.rate(ii.RxBps),
		subtleStyle.Render(probe.HumanBytes(ii.RxTotal)),
		subtleStyle.Render(m.rate(peak.rx)), rx))
	b.WriteString(fmt.Sprintf("TX: %s  (total %s, peak %s)\n%s\n",
		m.rate(ii.TxBps),
		subtleStyle.Render(probe.HumanBytes(ii.TxTotal)),
		subtleStyle.Render(m.rate(peak.tx)), tx))

	b.WriteString("\n" + fitLine(avail,
		fmt.Sprintf("Packets: %s in/s · %s out/s", formatCount(ii.RxPps), formatCount(ii.TxPps)),
		fmt.Sprintf("Pkts %s/%s", formatCount(ii.RxPps), formatCount(ii.TxPps)),
	) + "\n")

	// Errors and drops are cumulative since boot; a non-zero count is worth
	// pointing at even when the link is currently healthy.
	link := fitLine(avail,
		fmt.Sprintf("Errors: %d in / %d out · Drops: %d in / %d out", ii.Errin, ii.Errout, ii.Dropin, ii.Dropout),
		fmt.Sprintf("Err %d/%d · Drop %d/%d", ii.Errin, ii.Errout, ii.Dropin, ii.Dropout),
	)
	if ii.LinkProblems() > 0 {
		link = warnStyle.Render(link)
	} else {
		link = subtleStyle.Render(link)
	}
	b.WriteString(link + "\n")

	return b.String()
}

// fitLine picks the fullest wording that fits the available width, falling back
// to truncation if even the short form is too wide.
func fitLine(avail int, long, short string) string {
	if lipgloss.Width(long) <= avail {
		return long
	}
	if lipgloss.Width(short) <= avail {
		return short
	}
	return trunc(short, avail)
}

// formatCount renders a packets-per-second figure compactly.
func formatCount(v float64) string {
	switch {
	case v < 1000:
		return fmt.Sprintf("%.0f", v)
	case v < 1_000_000:
		return fmt.Sprintf("%.1fk", v/1000)
	default:
		return fmt.Sprintf("%.1fM", v/1_000_000)
	}
}

func max64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// helpers

// anySearching reports whether a search input is currently focused on any tab.
func (m Model) anySearching() bool {
	return m.portsSearching || m.procsSearching || m.connsSearching || m.eventsSearching
}

// isTableTab reports whether the active tab is one of the sortable, cursor-
// driven table tabs (ports / procs / conns) that support sorting, filtering,
// row details and process actions.
func (m Model) isTableTab() bool {
	return m.activeTab == tabPorts || m.activeTab == tabProcs || m.activeTab == tabConns
}

// hasRowCursor reports whether the active tab renders selectable rows. The
// events tab has a cursor (for copy and export) but none of the table tabs'
// sorting or process actions.
func (m Model) hasRowCursor() bool {
	return m.isTableTab() || m.activeTab == tabEvents
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

// formatDuration renders a duration compactly: "42s", "3m07s", "1h05m",
// "2d03h".
func formatDuration(d time.Duration) string {
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

// formatAge renders how long ago a connection was first seen. A zero
// first-seen time renders as "-".
func formatAge(now, since time.Time) string {
	if since.IsZero() {
		return "-"
	}
	return formatDuration(now.Sub(since))
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

// stripANSI removes escape sequences so styled text can go to the clipboard as
// plain text.
func stripANSI(s string) string {
	var out strings.Builder
	out.Grow(len(s))

	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			out.WriteByte(s[i])
			i++
			continue
		}
		// CSI: ESC [ … final byte in 0x40-0x7E
		if i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
				j++
			}
			i = min(j+1, len(s))
			continue
		}
		// OSC: ESC ] … BEL or ST
		if i+1 < len(s) && s[i+1] == ']' {
			j := i + 2
			for j < len(s) {
				if s[j] == 0x07 {
					j++
					break
				}
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
			continue
		}
		i = min(i+2, len(s))
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

// tabNames maps the --tab flag's values to tabs, so a session can start on the
// view the user actually cares about.
var tabNames = map[string]tab{
	"overview":    tabOverview,
	"interfaces":  tabIfaces,
	"ports":       tabPorts,
	"processes":   tabProcs,
	"procs":       tabProcs,
	"connections": tabConns,
	"conns":       tabConns,
	"events":      tabEvents,
	"routes":      tabRoutes,
	"neighbours":  tabRoutes,
}

// TabNames lists the accepted --tab values, for the flag's error message.
func TabNames() []string {
	out := make([]string, 0, len(tabNames))
	for name := range tabNames {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// WithTab selects the initial tab by name. ok is false for an unknown name,
// which the caller reports rather than silently ignoring.
func (m Model) WithTab(name string) (Model, bool) {
	t, ok := tabNames[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return m, false
	}
	m.activeTab = t
	return m, true
}

// applyNetSnapshot folds one interface sample into the model: link events, the
// sparkline histories and the peaks. It is a method rather than the body of a
// case because a live remote session feeds the same data in from a different
// message.
func (m *Model) applyNetSnapshot(snap probe.NetSnapshot) {
	m.lastSnap = snap
	m.err = nil

	m.noteIfaceEvents(m.lastSnap)
	m.noteIfaceAlerts(m.lastSnap)
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
	if m.ifacePeaks == nil {
		m.ifacePeaks = map[string]rateSample{}
	}
	for _, ii := range m.lastSnap.Ifaces {
		peak := m.ifacePeaks[ii.Name]
		peak.rx = max64(peak.rx, ii.RxBps)
		peak.tx = max64(peak.tx, ii.TxBps)
		m.ifacePeaks[ii.Name] = peak

		if ii.Kind == probe.IfaceLoopback {
			continue
		}
		totRx += ii.RxBps
		totTx += ii.TxBps
	}
	m.totalRxHist = probe.ClampHistory(append(m.totalRxHist, totRx), histMax)
	m.totalTxHist = probe.ClampHistory(append(m.totalTxHist, totTx), histMax)
	m.totalPeak.rx = max64(m.totalPeak.rx, totRx)
	m.totalPeak.tx = max64(m.totalPeak.tx, totTx)
	m.recordTrend(totRx, totTx, snap.TakenAt)

	m.ifaceDetailsText = m.renderIfaceDetailsText(m.ifaceDetailsVP.Width)
	m.ifaceDetailsText = hardClipLinesToWidth(
		m.ifaceDetailsText,
		m.ifaceDetailsVP.Width,
	)
	m.ifaceDetailsVP.SetContent(m.ifaceDetailsText)
}

// applyConnInfo folds one socket enumeration into the model — rates, closures,
// age tracking, churn, events and alerts — and returns the commands that work
// produced. Shared by the live host and by a live remote session.
func (m *Model) applyConnInfo(info probe.ConnInfo, now time.Time) []tea.Cmd {
	msg := info

	m.ports = msg.Ports
	m.procs = msg.Procs
	m.conns = msg.Conns
	m.polls++
	m.lastPollAt = now

	// Ordering matters: closures are detected from the previous poll's rows
	// and counters, which updateRates is about to overwrite.
	m.noteClosedConns(msg.Conns, now)
	m.updateRates(msg.Conns, now)

	// Track each connection's first-seen time across enumerations: this
	// drives the AGE column and the new/closed delta counters. The first
	// enumeration establishes a baseline (nothing is "new" on startup).
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

	churnMax := max(30, min(200, m.w/2))
	m.churnUpHist = probe.ClampHistory(append(m.churnUpHist, float64(m.connAdded)), churnMax)
	m.churnDownHist = probe.ClampHistory(append(m.churnDownHist, float64(m.connRemoved)), churnMax)

	curConns := make(map[string]probe.Conn, len(msg.Conns))
	ifaceOf := make(map[string]string, len(msg.Conns))
	for _, c := range msg.Conns {
		curConns[connKey(c)] = c
		// Resolved once per poll rather than per render: the answer only
		// changes when the addresses do.
		ifaceOf[connKey(c)] = probe.IfaceForIP(m.lastSnap.Ifaces, stripPort(c.Local))
	}
	m.prevConns = curConns
	m.connIface = ifaceOf

	// Flag listeners that appeared since the previous enumeration.
	curPorts := make(map[string]probe.ListenPort, len(msg.Ports))
	for _, p := range msg.Ports {
		curPorts[portKey(p)] = p
	}
	m.newPorts = map[string]bool{}
	if m.portSeen {
		for k, p := range curPorts {
			// Ephemeral UDP sockets churn with ordinary traffic; the event
			// log already ignores them, and so must the badge.
			if !probe.IsServiceListener(p) {
				continue
			}
			if _, known := m.prevPorts[k]; !known {
				m.newPorts[k] = true
			}
		}
	}
	prevPorts, firstRun := m.prevPorts, !m.portSeen
	m.prevPorts = curPorts
	m.portSeen = true

	m.noteSocketEvents(prevPorts, msg.Conns, firstRun, now)
	m.noteThroughputAlerts(msg.Conns, now)
	m.noteQualityAlerts(msg.Conns, now)

	m.portsCursor = clampInt(m.portsCursor, 0, len(m.filteredPorts())-1)
	m.procsCursor = clampInt(m.procsCursor, 0, len(m.filteredProcs())-1)
	m.connsCursor = clampInt(m.connsCursor, 0, m.connsRowCount()-1)
	m.eventsCursor = clampInt(m.eventsCursor, 0, len(m.eventRows())-1)

	m.pruneDNSCache(msg.Conns)
	cmds := m.kickConnLookups()

	m.portsText = hardClipLinesToWidth(m.renderPortsText(), m.portsVP.Width)
	m.portsVP.SetContent(m.portsText)
	m.procsText = hardClipLinesToWidth(m.renderProcsText(), m.procsVP.Width)
	m.procsVP.SetContent(m.procsText)
	m.connsText = hardClipLinesToWidth(m.renderConnsText(), m.connsVP.Width)
	m.connsVP.SetContent(m.connsText)
	m.eventsText = hardClipLinesToWidth(m.renderEventsText(), m.eventsVP.Width)
	m.eventsVP.SetContent(m.eventsText)
	m.refreshDetail()
	cmds = append(cmds, m.drainAlertHooks()...)

	return cmds
}
