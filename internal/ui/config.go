package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// config is the subset of model state persisted between runs. It is loaded in
// NewModel and written on quit.
type config struct {
	HideNoise   bool `json:"hide_noise"`
	RefreshMs   int  `json:"refresh_ms"`
	ConnsRevDNS bool `json:"conns_rev_dns"`
	ConnsGroup  bool `json:"conns_group"`
	ShowTotals  bool `json:"show_totals"`
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
		HideNoise:   m.hideNoise,
		RefreshMs:   int(m.refreshInterval / time.Millisecond),
		ConnsRevDNS: m.connsRevDNS,
		ConnsGroup:  m.connsGroup,
		ShowTotals:  m.showTotals,

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
	}
	if !m.baselineAt.IsZero() {
		c.BaselineAt = m.baselineAt.Format(time.RFC3339)
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
