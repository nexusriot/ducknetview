package ui

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Six alert thresholds, the latency targets and the hidden columns had no key
// binding at all: the only way to discover they existed was to read the
// documentation, and the only way to set one was to quit, edit JSON and start
// again. That is the same gap `a` and `w` closed for the baseline and the
// watchlist, left open for everything that happens to be a number.

// settingKind is how a row is edited.
type settingKind int

const (
	settingNumber settingKind = iota // typed in, 0 disables
	settingToggle                    // flipped with enter / space
	settingList                      // a set of strings, added and removed
)

// settingRow is one editable setting.
type settingRow struct {
	key   string
	title string
	help  string
	kind  settingKind

	get func(m *Model) string
	set func(m *Model, v string) (string, bool)
}

// settingsRows is the overlay's content. Each row writes through the model and
// saves, so there is no apply step to forget.
func settingsRows() []settingRow {
	numeric := func(key, title, help string, get func(*Model) float64, set func(*Model, float64)) settingRow {
		return settingRow{
			key: key, title: title, help: help, kind: settingNumber,
			get: func(m *Model) string {
				v := get(m)
				if v <= 0 {
					return "off"
				}
				return trimFloat(v)
			},
			set: func(m *Model, v string) (string, bool) {
				v = strings.TrimSpace(v)
				if v == "" || strings.EqualFold(v, "off") {
					set(m, 0)
					return title + " disabled", true
				}
				f, err := strconv.ParseFloat(v, 64)
				// ParseFloat accepts "NaN" and "Inf": a NaN threshold never
				// fires, and Inf overflows the integer-valued rules.
				if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
					return "Not a number: " + v, false
				}
				set(m, f)
				return fmt.Sprintf("%s = %s", title, trimFloat(f)), true
			},
		}
	}

	return []settingRow{
		numeric("alert_process_bps", "Process throughput", "bytes/s moved by one process before alerting",
			func(m *Model) float64 { return m.alertProcBps },
			func(m *Model, v float64) { m.alertProcBps = v }),
		numeric("alert_connection_bps", "Connection throughput", "bytes/s moved by one connection before alerting",
			func(m *Model) float64 { return m.alertConnBps },
			func(m *Model, v float64) { m.alertConnBps = v }),
		numeric("alert_rtt_ms", "Round-trip time", "milliseconds before a connection counts as slow",
			func(m *Model) float64 { return m.alertRules.rttMs },
			func(m *Model, v float64) { m.alertRules.rttMs = v }),
		numeric("alert_retrans_per_min", "Retransmits", "retransmits per minute on one socket",
			func(m *Model) float64 { return m.alertRules.retransPerMin },
			func(m *Model, v float64) { m.alertRules.retransPerMin = v }),
		numeric("alert_conn_growth_polls", "Connection growth", "consecutive polls of growth before calling it a leak",
			func(m *Model) float64 { return float64(m.alertRules.connGrowth) },
			func(m *Model, v float64) { m.alertRules.connGrowth = int(v) }),
		numeric("alert_fanout_hosts", "Fan-out", "new remote hosts in one poll before calling it a scan",
			func(m *Model) float64 { return float64(m.alertRules.fanoutHosts) },
			func(m *Model, v float64) { m.alertRules.fanoutHosts = int(v) }),
		numeric("alert_iface_drops_per_min", "Interface drops", "errors and drops per minute on one link",
			func(m *Model) float64 { return m.alertRules.ifaceDropsPerMin },
			func(m *Model, v float64) { m.alertRules.ifaceDropsPerMin = v }),
		numeric("alert_listen_queue_pct", "Accept queue", "percent of a listener's backlog waiting to be accepted",
			func(m *Model) float64 { return m.alertRules.listenQueuePct },
			func(m *Model, v float64) { m.alertRules.listenQueuePct = v }),
		numeric("alert_listen_drops_per_min", "Listen drops", "connections the host dropped on the listen path, per minute",
			func(m *Model) float64 { return m.alertRules.listenDropsPerMin },
			func(m *Model, v float64) { m.alertRules.listenDropsPerMin = v }),
		numeric("alert_close_wait", "CLOSE_WAIT", "sockets in CLOSE_WAIT held by one process",
			func(m *Model) float64 { return float64(m.alertRules.closeWait) },
			func(m *Model, v float64) { m.alertRules.closeWait = int(v) }),
		{
			key: "latency_targets", title: "Latency targets", kind: settingList,
			help: "extra host:port probes; the gateway is always probed",
			get: func(m *Model) string {
				if len(m.latencyExtra) == 0 {
					return "(gateway only)"
				}
				return strings.Join(m.latencyExtra, ", ")
			},
			set: func(m *Model, v string) (string, bool) {
				v = strings.TrimSpace(v)
				if v == "" {
					return "", false
				}
				for i, t := range m.latencyExtra {
					if t == v {
						m.latencyExtra = append(m.latencyExtra[:i], m.latencyExtra[i+1:]...)
						return "Removed latency target " + v, true
					}
				}
				if !dialable(v) {
					return v + " needs a host:port", false
				}
				m.latencyExtra = append(m.latencyExtra, v)
				return "Added latency target " + v, true
			},
		},
		{
			key: "hidden_columns", title: "Hidden columns", kind: settingList,
			help: "column keys left out of the tables, for narrow terminals",
			get: func(m *Model) string {
				if len(m.hiddenCols) == 0 {
					return "(none)"
				}
				keys := make([]string, 0, len(m.hiddenCols))
				for k := range m.hiddenCols {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				return strings.Join(keys, ", ")
			},
			set: func(m *Model, v string) (string, bool) {
				v = strings.ToLower(strings.TrimSpace(v))
				if v == "" {
					return "", false
				}
				if !knownColumnKey(v) {
					return "Unknown column " + v + " — see the help overlay", false
				}
				if m.hiddenCols == nil {
					m.hiddenCols = map[string]bool{}
				}
				if m.hiddenCols[v] {
					delete(m.hiddenCols, v)
					return "Showing column " + v, true
				}
				m.hiddenCols[v] = true
				return "Hiding column " + v, true
			},
		},
		{
			key: "theme", title: "Theme", kind: settingToggle,
			help: "colour palette (enter cycles)",
			get:  func(m *Model) string { return m.theme },
			set: func(m *Model, _ string) (string, bool) {
				m.theme = nextTheme(m.theme)
				applyTheme(m.theme)
				return "Theme: " + m.theme, true
			},
		},
	}
}

// trimFloat renders a threshold without trailing zeros, so 250 does not read as
// 250.000000.
func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// knownColumnKey reports whether a string names a column any table renders.
// Hiding a key that does not exist would silently do nothing.
func knownColumnKey(key string) bool {
	for _, k := range AllColumnKeys() {
		if k == key {
			return true
		}
	}
	return false
}

// openSettings arms the overlay.
func (m *Model) openSettings() {
	m.showSettings = true
	m.settingsCursor = clampInt(m.settingsCursor, 0, len(settingsRows())-1)
	m.settingsEditing = false
	m.settingsInput.SetValue("")
}

// settingsKey handles a keypress while the overlay is open. handled is false
// for keys the overlay does not claim, which close it.
func (m *Model) settingsKey(key string) (status string, handled bool) {
	rows := settingsRows()
	row := rows[clampInt(m.settingsCursor, 0, len(rows)-1)]

	if m.settingsEditing {
		switch key {
		case "enter":
			msg, ok := row.set(m, m.settingsInput.Value())
			m.settingsEditing = false
			m.settingsInput.SetValue("")
			m.settingsInput.Blur()
			if ok {
				m.saveConfig()
				m.rerenderActiveTable()
			}
			return msg, true
		case "esc":
			m.settingsEditing = false
			m.settingsInput.SetValue("")
			m.settingsInput.Blur()
			return "Cancelled", true
		}
		return "", true // every other key belongs to the text input
	}

	switch key {
	case "up", "k":
		m.settingsCursor = clampInt(m.settingsCursor-1, 0, len(rows)-1)
		return "", true
	case "down", "j":
		m.settingsCursor = clampInt(m.settingsCursor+1, 0, len(rows)-1)
		return "", true
	case "enter", " ":
		if row.kind == settingToggle {
			msg, ok := row.set(m, "")
			if ok {
				m.saveConfig()
				m.rerenderActiveTable()
			}
			return msg, true
		}
		m.settingsEditing = true
		m.settingsInput.SetValue("")
		m.settingsInput.Focus()
		return "", true
	case "esc", ",", "q":
		m.showSettings = false
		return "", true
	}
	return "", false
}

// renderSettings draws the overlay.
func (m Model) renderSettings() string {
	rows := settingsRows()
	var b strings.Builder

	b.WriteString(titleStyle.Render("Settings") + "  " +
		subtleStyle.Render("↑↓ move · enter edit · esc close · saved as you go") + "\n\n")

	for i, r := range rows {
		cursor := "  "
		title := padRight(trunc(r.title, 24), 24)
		if i == m.settingsCursor {
			cursor = cursorStyle.Render("▸ ")
			title = selectedStyle.Render(title)
		}
		value := r.get(&m)
		styled := value
		if value == "off" || value == "(none)" || value == "(gateway only)" {
			styled = subtleStyle.Render(value)
		} else {
			styled = okStyle.Render(value)
		}
		b.WriteString(cursor + title + " " + styled + "\n")
		if i == m.settingsCursor {
			b.WriteString("    " + subtleStyle.Render(r.help) + "\n")
			switch r.kind {
			case settingList:
				b.WriteString("    " + subtleStyle.Render("enter a value to add it, or the same value again to remove it") + "\n")
			case settingNumber:
				b.WriteString("    " + subtleStyle.Render("a number, or 'off' / empty to disable") + "\n")
			}
		}
	}

	if m.settingsEditing {
		b.WriteString("\n" + titleStyle.Render(rows[m.settingsCursor].title) + "\n")
		b.WriteString("  " + m.settingsInput.View() + "\n")
		b.WriteString("  " + subtleStyle.Render("enter applies · esc cancels") + "\n")
	}

	b.WriteString("\n" + subtleStyle.Render("Every threshold is off at 0. Changes are written to "+configFileLabel()+" immediately.") + "\n")
	return boxStyle.Width(min(m.w-2, 100)).Height(m.bodyHeight()).Render(b.String())
}

// configFileLabel is the config path for the overlay's footer, shortened to the
// part worth reading.
func configFileLabel() string {
	p, err := configPath()
	if err != nil {
		return "the config file"
	}
	return p
}

// AllColumnKeys lists every column key any table can render, so the help
// overlay and the settings validation agree with the renderers.
func AllColumnKeys() []string {
	return []string{
		"age", "closed", "conns", "container", "count", "cpu", "host", "iface",
		"life", "listen", "local", "mem", "name", "netns", "owner", "pid",
		"process", "procs", "proto", "queue", "remote", "rtt", "rtx", "rx",
		"state", "states", "svc", "tx", "unit", "user",
	}
}

// columnKeysInUse is what the renderers actually declare, which the test
// compares against AllColumnKeys so the two cannot drift.
func (m Model) columnKeysInUse() []string {
	seen := map[string]bool{}
	for _, cols := range []colSpecs{
		m.portsColumns(), m.procsColumns(), m.connsColumns(),
		m.groupColumns(), m.closedColumns(),
	} {
		for _, c := range cols {
			seen[c.key] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
