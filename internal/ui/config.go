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
	HideNoise     bool `json:"hide_noise"`
	RefreshMs     int  `json:"refresh_ms"`
	ConnsRevDNS   bool `json:"conns_rev_dns"`
	ConnsGroup    bool `json:"conns_group"`
	PortsSortCol  int  `json:"ports_sort_col"`
	PortsSortDesc bool `json:"ports_sort_desc"`
	ProcsSortCol  int  `json:"procs_sort_col"`
	ProcsSortDesc bool `json:"procs_sort_desc"`
	ConnsSortCol  int  `json:"conns_sort_col"`
	ConnsSortDesc bool `json:"conns_sort_desc"`
	GroupSortCol  int  `json:"group_sort_col"`
	GroupSortDesc bool `json:"group_sort_desc"`
	// Watchlist entries (CIDR / IP / regex) whose matching remotes are
	// highlighted. Edited by hand in the config file; preserved on save.
	Watchlist []string `json:"watchlist"`
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
}

// saveConfig writes the current persisted settings, best-effort (errors are
// swallowed: a monitoring tool must never fail to quit because of config I/O).
func (m Model) saveConfig() {
	c := config{
		HideNoise:     m.hideNoise,
		RefreshMs:     int(m.refreshInterval / time.Millisecond),
		ConnsRevDNS:   m.connsRevDNS,
		ConnsGroup:    m.connsGroup,
		PortsSortCol:  m.portsSortCol,
		PortsSortDesc: m.portsSortDesc,
		ProcsSortCol:  m.procsSortCol,
		ProcsSortDesc: m.procsSortDesc,
		ConnsSortCol:  m.connsSortCol,
		ConnsSortDesc: m.connsSortDesc,
		GroupSortCol:  m.groupSortCol,
		GroupSortDesc: m.groupSortDesc,
		Watchlist:     m.watchlistRaw,
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
