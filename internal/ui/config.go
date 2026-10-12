package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// config is the subset of model state persisted between runs. It is loaded in
// NewModel and written on quit.
type config struct {
	HideNoise   bool `json:"hide_noise"`
	RefreshMs   int  `json:"refresh_ms"`
	ConnsRevDNS bool `json:"conns_rev_dns"`
	ConnsGroup  bool `json:"conns_group"`
	ShowTotals  bool `json:"show_totals"`
	AggProcs    bool `json:"aggregate_processes"`
	// Column keys to leave out of the tables, and whether rates are worded in
	// bits per second. Both hand-editable.
	HiddenColumns []string `json:"hidden_columns,omitempty"`
	RatesInBits   bool     `json:"rates_in_bits,omitempty"`
	// Extra latency probe targets ("host:port"). The default gateway is always
	// probed; anything here is additional and may leave the LAN, so it is opt-in.
	LatencyTargets []string `json:"latency_targets,omitempty"`
	// Quick filters and the tab that was open, so a restart resumes where the
	// last session left off.
	ProtoFilter     int  `json:"proto_filter"`
	IPVerFilter     int  `json:"ipver_filter"`
	ConnStateFilter int  `json:"conn_state_filter"`
	PublicOnly      bool `json:"public_only"`
	OnlyMine        bool `json:"only_mine"`
	LastTab         int  `json:"last_tab"`
	SearchHighlight bool `json:"search_highlight"`
	PortsSortCol    int  `json:"ports_sort_col"`
	PortsSortDesc   bool `json:"ports_sort_desc"`
	ProcsSortCol    int  `json:"procs_sort_col"`
	ProcsSortDesc   bool `json:"procs_sort_desc"`
	ConnsSortCol    int  `json:"conns_sort_col"`
	ConnsSortDesc   bool `json:"conns_sort_desc"`
	GroupSortCol    int  `json:"group_sort_col"`
	GroupSortDesc   bool `json:"group_sort_desc"`
	// Watchlist entries (CIDR / IP / regex) whose matching remotes are
	// highlighted. Edited by hand in the config file; preserved on save.
	Watchlist []string `json:"watchlist"`
	// Expected listeners ("proto|bind_address"), saved with `b`.
	Baseline   []string `json:"baseline,omitempty"`
	BaselineAt string   `json:"baseline_at,omitempty"`
	// Throughput alert thresholds in bytes/sec; 0 or absent disables them.
	// Hand-edited: the right number is entirely site-specific.
	AlertProcessBps float64 `json:"alert_process_bps,omitempty"`
	AlertConnBps    float64 `json:"alert_connection_bps,omitempty"`
	// Quality rules, all opt-in: round-trip time (ms), retransmits per minute
	// on one socket, consecutive polls of connection growth before calling it a
	// leak, and new remote hosts per poll per process before calling it fan-out.
	AlertRTTMs         float64 `json:"alert_rtt_ms,omitempty"`
	AlertRetransPerMin float64 `json:"alert_retrans_per_min,omitempty"`
	AlertConnGrowth    int     `json:"alert_conn_growth_polls,omitempty"`
	AlertFanoutHosts   int     `json:"alert_fanout_hosts,omitempty"`
	// Link health: errors and drops per minute on one interface.
	AlertIfaceDropsPerMin float64 `json:"alert_iface_drops_per_min,omitempty"`

	// Appearance and interaction.
	Theme       string            `json:"theme,omitempty"`
	Keys        map[string]string `json:"keys,omitempty"`
	ASNDatabase string            `json:"asn_database,omitempty"`
	ProcTree    bool              `json:"process_tree,omitempty"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ducknetview", "config.json"), nil
}

// loadConfig reads the persisted config. ok is false when there is no readable,
// valid config file (first run, unreadable dir, corrupt JSON) — callers should
// then keep their defaults.
func loadConfig() (config, bool) {
	p, err := configPath()
	if err != nil {
		return config{}, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return config{}, false
	}
	var c config
	if json.Unmarshal(b, &c) != nil {
		return config{}, false
	}
	return c, true
}

// applyConfig overlays persisted settings onto the model, ignoring anything out
// of range so a hand-edited or stale file can't wedge the UI.
func (m *Model) applyConfig() {
	c, ok := loadConfig()
	if !ok {
		return
	}
	m.hideNoise = c.HideNoise
	m.connsRevDNS = c.ConnsRevDNS
	m.connsGroup = c.ConnsGroup
	m.showTotals = c.ShowTotals
	m.aggregateProcs = c.AggProcs
	if len(c.HiddenColumns) > 0 {
		hidden := make(map[string]bool, len(c.HiddenColumns))
		for _, key := range c.HiddenColumns {
			hidden[strings.ToLower(strings.TrimSpace(key))] = true
		}
		m.hiddenCols = hidden
	}
	if c.RatesInBits {
		m.rateUnit = probe.UnitBits
	}
	m.latencyExtra = c.LatencyTargets
	m.protoFilter = clampInt(c.ProtoFilter, 0, 2)
	m.ipVerFilter = clampInt(c.IPVerFilter, 0, 2)
	m.connStateFilter = clampInt(c.ConnStateFilter, 0, 2)
	m.publicOnly = c.PublicOnly
	m.onlyMine = c.OnlyMine
	m.activeTab = tab(clampInt(c.LastTab, 0, tabCount-1))
	if c.SearchHighlight {
		m.searchMode = searchHighlight
	}
	if d := time.Duration(c.RefreshMs) * time.Millisecond; isRefreshStep(d) {
		m.refreshInterval = d
	}
	m.portsSortCol = clampInt(c.PortsSortCol, 0, len(portsSortCols)-1)
	m.procsSortCol = clampInt(c.ProcsSortCol, 0, len(procsSortCols)-1)
	m.connsSortCol = clampInt(c.ConnsSortCol, 0, len(connsSortCols)-1)
	m.groupSortCol = clampInt(c.GroupSortCol, 0, len(groupSortCols)-1)
	m.portsSortDesc = c.PortsSortDesc
	m.procsSortDesc = c.ProcsSortDesc
	m.connsSortDesc = c.ConnsSortDesc
	m.groupSortDesc = c.GroupSortDesc
	m.watchlistRaw = c.Watchlist
	m.watch = parseWatchlist(c.Watchlist)

	m.alertProcBps = maxFloat(0, c.AlertProcessBps)
	m.alertConnBps = maxFloat(0, c.AlertConnBps)
	m.alertRules = alertRules{
		rttMs:            maxFloat(0, c.AlertRTTMs),
		retransPerMin:    maxFloat(0, c.AlertRetransPerMin),
		connGrowth:       max(0, c.AlertConnGrowth),
		fanoutHosts:      max(0, c.AlertFanoutHosts),
		ifaceDropsPerMin: maxFloat(0, c.AlertIfaceDropsPerMin),
	}

	// An unknown theme name falls back to the default rather than rendering
	// everything unstyled.
	if validTheme(c.Theme) {
		m.theme = c.Theme
	}
	applyTheme(m.theme)

	m.keymap = newKeymap(c.Keys)
	m.procTree = c.ProcTree
	m.asnPath = c.ASNDatabase
	m.loadASNDB()

	if len(c.Baseline) > 0 {
		set := make(map[string]bool, len(c.Baseline))
		for _, k := range c.Baseline {
			set[k] = true
		}
		m.baseline = set
		m.baselineRaw = c.Baseline
		if t, err := time.Parse(time.RFC3339, c.BaselineAt); err == nil {
			m.baselineAt = t
		}
	}
}

// saveConfig writes the current persisted settings, best-effort (errors are
// swallowed: a monitoring tool must never fail to quit because of config I/O).
func (m Model) saveConfig() {
	c := config{
		HideNoise:      m.hideNoise,
		RefreshMs:      int(m.refreshInterval / time.Millisecond),
		ConnsRevDNS:    m.connsRevDNS,
		ConnsGroup:     m.connsGroup,
		ShowTotals:     m.showTotals,
		AggProcs:       m.aggregateProcs,
		RatesInBits:    m.rateUnit == probe.UnitBits,
		LatencyTargets: m.latencyExtra,

		ProtoFilter:     m.protoFilter,
		IPVerFilter:     m.ipVerFilter,
		ConnStateFilter: m.connStateFilter,
		PublicOnly:      m.publicOnly,
		OnlyMine:        m.onlyMine,
		LastTab:         int(m.activeTab),
		SearchHighlight: m.searchMode == searchHighlight,
		PortsSortCol:    m.portsSortCol,
		PortsSortDesc:   m.portsSortDesc,
		ProcsSortCol:    m.procsSortCol,
		ProcsSortDesc:   m.procsSortDesc,
		ConnsSortCol:    m.connsSortCol,
		ConnsSortDesc:   m.connsSortDesc,
		GroupSortCol:    m.groupSortCol,
		GroupSortDesc:   m.groupSortDesc,
		Watchlist:       m.watchlistRaw,
		Baseline:        m.baselineRaw,

		AlertProcessBps: m.alertProcBps,
		AlertConnBps:    m.alertConnBps,

		AlertRTTMs:            m.alertRules.rttMs,
		AlertRetransPerMin:    m.alertRules.retransPerMin,
		AlertConnGrowth:       m.alertRules.connGrowth,
		AlertFanoutHosts:      m.alertRules.fanoutHosts,
		AlertIfaceDropsPerMin: m.alertRules.ifaceDropsPerMin,

		Theme:       m.theme,
		Keys:        m.keymap.overrides(),
		ASNDatabase: m.asnPath,
		ProcTree:    m.procTree,
	}
	if !m.baselineAt.IsZero() {
		c.BaselineAt = m.baselineAt.Format(time.RFC3339)
	}
	if len(m.hiddenCols) > 0 {
		for key := range m.hiddenCols {
			c.HiddenColumns = append(c.HiddenColumns, key)
		}
		sort.Strings(c.HiddenColumns)
	}
	p, err := configPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(p, b, 0o644)
}

// maxFloat clamps a hand-edited config value, so a negative threshold can't
// make every poll fire an alert.
func maxFloat(lo, v float64) float64 {
	if v < lo {
		return lo
	}
	return v
}
