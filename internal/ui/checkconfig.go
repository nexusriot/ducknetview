package ui

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// The config is around thirty-five keys now, and the loader is deliberately
// forgiving: an out-of-range value falls back to its default and a malformed
// one is skipped, because a hand-edited file must never wedge the UI. The cost
// of that forgiveness is silence — a watchlist regex that does not compile, a
// latency target with no port, a hidden column key that names nothing and a
// binding for an action that does not exist all simply do nothing, for ever.
// Every one of those is already detected somewhere in the code; this is the
// command that says so out loud.

// ConfigProblem is one thing wrong with the config file.
type ConfigProblem struct {
	Key    string // the config key it was found under, "" for the file itself
	Value  string // the offending value, when there is one
	Detail string // what is wrong and what was done about it
}

func (p ConfigProblem) String() string {
	var b strings.Builder
	if p.Key != "" {
		b.WriteString(p.Key)
		if p.Value != "" {
			b.WriteString(": " + p.Value)
		}
		b.WriteString(" — ")
	}
	b.WriteString(p.Detail)
	return b.String()
}

// ConfigReport is the outcome of checking the file.
type ConfigReport struct {
	Path     string
	Exists   bool
	Problems []ConfigProblem
	// Fatal is set when the file could not be read or parsed at all, in which
	// case every setting in it is being ignored rather than just one.
	Fatal bool
}

// OK reports whether the config is entirely usable.
func (r ConfigReport) OK() bool { return !r.Fatal && len(r.Problems) == 0 }

// configKeys is the set of recognised JSON keys, read off the struct tags so
// the check cannot drift from what the loader actually understands.
func configKeys() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(config{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			tag = tag[:comma]
		}
		out[tag] = true
	}
	return out
}

// CheckConfig validates the saved configuration and reports everything wrong
// with it at once.
func CheckConfig() ConfigReport {
	r := ConfigReport{}

	p, err := configPath()
	if err != nil {
		r.Fatal = true
		r.Problems = append(r.Problems, ConfigProblem{Detail: "cannot determine the config path: " + err.Error()})
		return r
	}
	r.Path = p

	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			// No file is not a problem: every setting has a default, and this
			// is what a first run looks like.
			return r
		}
		r.Fatal = true
		r.Problems = append(r.Problems, ConfigProblem{Detail: "cannot read the config file: " + err.Error()})
		return r
	}
	r.Exists = true

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		r.Fatal = true
		r.Problems = append(r.Problems, ConfigProblem{
			Detail: "not valid JSON, so the whole file is being ignored: " + err.Error()})
		return r
	}

	var c config
	if err := json.Unmarshal(b, &c); err != nil {
		r.Fatal = true
		r.Problems = append(r.Problems, ConfigProblem{
			Detail: "the file parses as JSON but not as a config, so all of it is being ignored: " + err.Error()})
		return r
	}

	add := func(key, value, detail string) {
		r.Problems = append(r.Problems, ConfigProblem{Key: key, Value: value, Detail: detail})
	}

	// A typo'd key is the quietest failure of all: the file looks right and
	// the setting simply does not exist.
	known := configKeys()
	unknown := make([]string, 0, len(raw))
	for key := range raw {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		add(key, "", "unknown key, ignored — check the spelling against the documented keys")
	}

	checkRanges(&c, raw, add)
	checkLists(&c, add)
	checkAlerts(&c, add)
	checkAppearance(&c, add)

	return r
}

// checkRanges reports values the loader would silently clamp or discard.
func checkRanges(c *config, raw map[string]json.RawMessage, add func(key, value, detail string)) {
	if _, set := raw["refresh_ms"]; set {
		if d := time.Duration(c.RefreshMs) * time.Millisecond; !isRefreshStep(d) {
			steps := make([]string, 0, len(refreshSteps))
			for _, s := range refreshSteps {
				steps = append(steps, fmt.Sprintf("%d", s/time.Millisecond))
			}
			add("refresh_ms", fmt.Sprintf("%d", c.RefreshMs),
				"not one of the selectable intervals ("+strings.Join(steps, ", ")+"), so the default is used")
		}
	}

	ranged := []struct {
		key      string
		value    int
		lo, hi   int
		set      bool
		meanings string
	}{
		{"proto_filter", c.ProtoFilter, 0, 2, true, "0 all, 1 tcp, 2 udp"},
		{"ipver_filter", c.IPVerFilter, 0, 2, true, "0 all, 1 v4, 2 v6"},
		{"conn_state_filter", c.ConnStateFilter, 0, 2, true, "0 all, 1 established, 2 active"},
		{"last_tab", c.LastTab, 0, tabCount - 1, true, ""},
		{"ports_sort_col", c.PortsSortCol, 0, len(portsSortCols) - 1, true, ""},
		{"procs_sort_col", c.ProcsSortCol, 0, len(procsSortCols) - 1, true, ""},
		{"conns_sort_col", c.ConnsSortCol, 0, len(connsSortCols) - 1, true, ""},
		{"group_sort_col", c.GroupSortCol, 0, len(groupSortCols) - 1, true, ""},
	}
	for _, f := range ranged {
		if !f.set || (f.value >= f.lo && f.value <= f.hi) {
			continue
		}
		detail := fmt.Sprintf("out of range %d–%d, so it is clamped", f.lo, f.hi)
		if f.meanings != "" {
			detail += " (" + f.meanings + ")"
		}
		add(f.key, fmt.Sprintf("%d", f.value), detail)
	}
}

// checkLists reports entries of the array-valued keys that are quietly dropped.
func checkLists(c *config, add func(key, value, detail string)) {
	for _, key := range c.HiddenColumns {
		k := strings.ToLower(strings.TrimSpace(key))
		if k == "" {
			add("hidden_columns", key, "empty entry, ignored")
			continue
		}
		if !knownColumnKey(k) {
			add("hidden_columns", key,
				"not a column key any table renders, so nothing is hidden — valid keys: "+
					strings.Join(AllColumnKeys(), " "))
		}
	}

	for _, target := range c.LatencyTargets {
		t := strings.TrimSpace(target)
		if !dialable(t) {
			add("latency_targets", target,
				"not a host:port, so it can never be measured and shows as unusable")
			continue
		}
		// A host that does not resolve is worth saying, but is not an error:
		// DNS may simply be down at the moment the check runs.
		host, _, _ := net.SplitHostPort(t)
		if net.ParseIP(host) == nil {
			if _, err := net.LookupHost(host); err != nil {
				add("latency_targets", target,
					"the host does not resolve right now, so the probe will report no response")
			}
		}
	}

	for _, entry := range c.Watchlist {
		e := strings.TrimSpace(entry)
		if e == "" {
			add("watchlist", entry, "empty entry, ignored")
			continue
		}
		if _, _, err := net.ParseCIDR(e); err == nil {
			continue
		}
		if net.ParseIP(e) != nil {
			continue
		}
		if _, err := regexp.Compile(e); err != nil {
			add("watchlist", entry,
				"not a CIDR, not an IP and not a valid regular expression ("+err.Error()+"), so it matches nothing")
		}
	}

	for _, entry := range c.Baseline {
		if proto, addr, ok := strings.Cut(entry, "|"); !ok || proto == "" || addr == "" {
			add("baseline", entry, `not in the "proto|bind_address" form, so this listener can never match`)
		}
	}
	if c.BaselineAt != "" {
		if _, err := time.Parse(time.RFC3339, c.BaselineAt); err != nil {
			add("baseline_at", c.BaselineAt, "not an RFC 3339 timestamp, so the baseline's age is not shown")
		}
	}
}

// checkAlerts reports thresholds that cannot do what they look like they do.
func checkAlerts(c *config, add func(key, value, detail string)) {
	negatives := []struct {
		key   string
		value float64
	}{
		{"alert_process_bps", c.AlertProcessBps},
		{"alert_connection_bps", c.AlertConnBps},
		{"alert_rtt_ms", c.AlertRTTMs},
		{"alert_retrans_per_min", c.AlertRetransPerMin},
		{"alert_conn_growth_polls", float64(c.AlertConnGrowth)},
		{"alert_fanout_hosts", float64(c.AlertFanoutHosts)},
		{"alert_iface_drops_per_min", c.AlertIfaceDropsPerMin},
	}
	for _, f := range negatives {
		if f.value < 0 {
			add(f.key, trimFloat(f.value), "negative, so it is clamped to 0 (off) rather than firing on every poll")
		}
	}
}

// checkAppearance reports a theme, a key binding or a database that will not
// take effect.
func checkAppearance(c *config, add func(key, value, detail string)) {
	if c.Theme != "" && !validTheme(c.Theme) {
		add("theme", c.Theme, "not a known theme, so the default is used — try "+strings.Join(ThemeNames(), ", "))
	}

	// The keymap drops bindings it cannot honour; this reports each reason.
	seen := map[string]string{}
	actions := make([]string, 0, len(c.Keys))
	for action := range c.Keys {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		key := strings.TrimSpace(c.Keys[action])
		if _, known := defaultKeys[action]; !known {
			add("keys", action,
				"not a rebindable action, so the binding does nothing — valid actions: "+strings.Join(Actions(), " "))
			continue
		}
		switch {
		case key == "":
			add("keys", action, "empty key, so the binding is ignored")
		case reservedKeys[key]:
			add("keys", action+" = "+key,
				"that key is reserved for navigation and quitting, so the binding is ignored")
		default:
			if other, clash := seen[key]; clash {
				add("keys", action+" = "+key,
					"already bound to \""+other+"\", so only one of the two takes effect")
				continue
			}
			seen[key] = action
		}
	}

	if c.ASNDatabase != "" {
		if _, err := probe.LoadASNDB(c.ASNDatabase); err != nil {
			add("asn_database", c.ASNDatabase,
				"cannot be loaded, so the OWNER column will not appear: "+err.Error())
		}
	}
}

// FormatConfigReport renders the outcome for `--check-config`.
func FormatConfigReport(r ConfigReport) string {
	var b strings.Builder

	switch {
	case r.Path == "":
		b.WriteString("config: path unknown\n")
	case !r.Exists:
		fmt.Fprintf(&b, "%s\n  no config file yet — every setting is at its default\n", r.Path)
		return b.String()
	default:
		fmt.Fprintf(&b, "%s\n", r.Path)
	}

	if r.OK() {
		b.WriteString("  config is valid\n")
		return b.String()
	}

	if r.Fatal {
		for _, p := range r.Problems {
			fmt.Fprintf(&b, "  ERROR  %s\n", p.String())
		}
		return b.String()
	}

	for _, p := range r.Problems {
		fmt.Fprintf(&b, "  %s\n", p.String())
	}
	fmt.Fprintf(&b, "\n%d %s found. Every one of these is being ignored at startup.\n",
		len(r.Problems), plural(len(r.Problems), "problem"))
	return b.String()
}

// reloadConfig re-reads the config file in place. Everything the session has
// accumulated — totals, the event ring, rate baselines, the trend — is
// deliberately left alone: those are the reason not to restart.
func (m *Model) reloadConfig() {
	// A snapshot or remote session forces several settings; reloading would
	// quietly replace them with this host's.
	if m.snapshotOnly {
		m.status = "Config reload ignored: this session is viewing another host"
		return
	}

	report := CheckConfig()
	m.applyConfig()
	m.rerenderActiveTable()

	detail := "thresholds, watchlist, columns and bindings re-read"
	level := levelInfo
	if n := len(report.Problems); n > 0 {
		// The valid half is applied either way — saying nothing about the rest
		// would be the same silence --check-config exists to break.
		detail = fmt.Sprintf("%d %s in the config were ignored — run --check-config",
			n, plural(n, "problem"))
		level = levelWarn
	}
	if report.Fatal {
		detail = "the config file could not be read, so nothing changed — run --check-config"
		level = levelWarn
	}

	m.recordEvent(event{At: time.Now(), Level: level, Kind: "config_reloaded", Subject: report.Path, Detail: detail})
	m.status = "Config reloaded: " + detail
}
