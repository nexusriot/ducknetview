package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// --- --check-config ------------------------------------------------------

// writeRawConfig puts a literal config file where the checker will find it.
// The tests write JSON by hand rather than through saveConfig because the
// point is what happens to a file the loader would silently half-ignore.
func writeRawConfig(t *testing.T, body string) string {
	t.Helper()
	p, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(p) })
	return p
}

func TestCheckConfigNoFile(t *testing.T) {
	p, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(p)

	r := CheckConfig()
	if !r.OK() {
		t.Errorf("a missing config is not a problem: %+v", r.Problems)
	}
	if r.Exists {
		t.Error("Exists = true with no file")
	}
	if out := FormatConfigReport(r); !strings.Contains(out, "no config file yet") {
		t.Errorf("report = %q", out)
	}
}

func TestCheckConfigValid(t *testing.T) {
	writeRawConfig(t, `{
	  "refresh_ms": 2000,
	  "theme": "contrast",
	  "watchlist": ["45.9.0.0/16", "8.8.8.8", "amazonaws|\\.ru$"],
	  "latency_targets": ["1.1.1.1:53"],
	  "hidden_columns": ["svc"],
	  "keys": {"closed": "Z"},
	  "alert_rtt_ms": 250,
	  "baseline": ["tcp|0.0.0.0:22"],
	  "baseline_at": "2026-10-11T09:00:00Z"
	}`)

	r := CheckConfig()
	if !r.OK() {
		for _, p := range r.Problems {
			t.Errorf("unexpected problem: %s", p)
		}
	}
	if !strings.Contains(FormatConfigReport(r), "config is valid") {
		t.Error("a valid config does not say so")
	}
}

// Each of these is a value the loader quietly discards, which is the whole
// reason the command exists.
func TestCheckConfigFindsSilentFailures(t *testing.T) {
	writeRawConfig(t, `{
	  "refresh_ms": 1234,
	  "proto_filter": 9,
	  "conns_sort_col": 99,
	  "hidden_columns": ["nonsense"],
	  "latency_targets": ["example.com"],
	  "watchlist": ["*broken("],
	  "baseline": ["malformed"],
	  "baseline_at": "yesterday",
	  "theme": "neon",
	  "alert_rtt_ms": -5,
	  "keys": {"frobnicate": "Q"},
	  "colour_scheme": "blue"
	}`)

	r := CheckConfig()
	if r.Fatal {
		t.Fatal("a readable file was reported as fatal")
	}

	byKey := map[string]string{}
	for _, p := range r.Problems {
		byKey[p.Key] = p.String()
	}
	for _, want := range []string{
		"refresh_ms", "proto_filter", "conns_sort_col", "hidden_columns",
		"latency_targets", "watchlist", "baseline", "baseline_at", "theme",
		"alert_rtt_ms", "keys", "colour_scheme",
	} {
		if _, ok := byKey[want]; !ok {
			t.Errorf("%s was not reported; problems: %v", want, r.Problems)
		}
	}

	// A typo'd key is the quietest failure of all, so it must say so plainly.
	if !strings.Contains(byKey["colour_scheme"], "unknown key") {
		t.Errorf("unknown key message = %q", byKey["colour_scheme"])
	}
	// Each message should say what was done about it, not just that it is wrong.
	for _, p := range r.Problems {
		if p.Detail == "" {
			t.Errorf("problem for %q has no explanation", p.Key)
		}
	}
}

func TestCheckConfigKeyBindingProblems(t *testing.T) {
	writeRawConfig(t, `{"keys": {"closed": "Z", "group": "Z", "quit": "tab", "search": ""}}`)

	r := CheckConfig()
	joined := strings.Join(problemStrings(r), "\n")
	for _, want := range []string{"already bound", "reserved", "empty key"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestCheckConfigASNDatabase(t *testing.T) {
	writeRawConfig(t, `{"asn_database": "/does/not/exist.tsv"}`)
	if joined := strings.Join(problemStrings(CheckConfig()), "\n"); !strings.Contains(joined, "asn_database") {
		t.Errorf("a missing ASN database was not reported: %s", joined)
	}

	// A real one must not be reported.
	db := filepath.Join(t.TempDir(), "ip2asn.tsv")
	if err := os.WriteFile(db, []byte("8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRawConfig(t, `{"asn_database": "`+db+`"}`)
	if r := CheckConfig(); !r.OK() {
		t.Errorf("a loadable database was reported as a problem: %v", r.Problems)
	}
}

// A file that cannot be parsed means every setting in it is ignored, which is
// a different and worse thing than one bad value.
func TestCheckConfigCorrupt(t *testing.T) {
	writeRawConfig(t, `{not json`)

	r := CheckConfig()
	if !r.Fatal {
		t.Fatal("corrupt JSON was not reported as fatal")
	}
	if r.OK() {
		t.Error("OK = true for a corrupt file")
	}
	if out := FormatConfigReport(r); !strings.Contains(out, "ERROR") || !strings.Contains(out, "whole file") {
		t.Errorf("report = %q", out)
	}
}

func TestFormatConfigReportCountsProblems(t *testing.T) {
	writeRawConfig(t, `{"theme": "neon", "alert_rtt_ms": -1}`)
	out := FormatConfigReport(CheckConfig())
	if !strings.Contains(out, "2 problems found") {
		t.Errorf("report does not count the problems:\n%s", out)
	}
	if !strings.Contains(out, "being ignored at startup") {
		t.Errorf("report does not say what the consequence is:\n%s", out)
	}
}

// configKeys is read off the struct tags; if that stops working every key in
// the file would be reported as unknown.
func TestConfigKeysMatchTheStruct(t *testing.T) {
	keys := configKeys()
	for _, want := range []string{"hide_noise", "refresh_ms", "watchlist", "baseline",
		"alert_rtt_ms", "alert_iface_drops_per_min", "theme", "keys", "asn_database", "process_tree"} {
		if !keys[want] {
			t.Errorf("configKeys is missing %q", want)
		}
	}
	if keys["colour_scheme"] {
		t.Error("configKeys invented a key the struct does not have")
	}
}

func problemStrings(r ConfigReport) []string {
	out := make([]string, 0, len(r.Problems))
	for _, p := range r.Problems {
		out = append(out, p.String())
	}
	return out
}

// --- SIGHUP reload -------------------------------------------------------

func TestReloadAppliesNewSettings(t *testing.T) {
	defer applyTheme(defaultTheme)

	writeRawConfig(t, `{"alert_rtt_ms": 100, "theme": "default"}`)
	m := NewModel().WithConfig()
	m.w, m.h = 160, 45
	if m.alertRules.rttMs != 100 {
		t.Fatalf("setup: rttMs = %v", m.alertRules.rttMs)
	}

	writeRawConfig(t, `{"alert_rtt_ms": 250, "alert_fanout_hosts": 20, "theme": "contrast"}`)
	m.reloadConfig()

	if m.alertRules.rttMs != 250 {
		t.Errorf("rttMs = %v after reload, want 250", m.alertRules.rttMs)
	}
	if m.alertRules.fanoutHosts != 20 {
		t.Errorf("fanoutHosts = %d after reload", m.alertRules.fanoutHosts)
	}
	if m.theme != "contrast" {
		t.Errorf("theme = %q after reload", m.theme)
	}
}

// Not losing the session is the entire reason to reload rather than restart.
func TestReloadKeepsSessionState(t *testing.T) {
	writeRawConfig(t, `{"alert_rtt_ms": 100}`)
	m := NewModel().WithConfig()
	m.w, m.h = 160, 45

	m.sessionTotal = byteTotals{rx: 1234, tx: 5678}
	m.polls = 42
	m.lastPollAt = time.Now().Add(-time.Second)
	m.recordEvent(event{At: time.Now(), Kind: "listener_up", Subject: "tcp 0.0.0.0:22"})
	m.trend.record(time.Now(), 1000, 2000)
	eventsBefore := len(m.events)

	writeRawConfig(t, `{"alert_rtt_ms": 250}`)
	m.reloadConfig()

	if m.sessionTotal.rx != 1234 || m.sessionTotal.tx != 5678 {
		t.Errorf("session totals were reset: %+v", m.sessionTotal)
	}
	if m.polls != 42 {
		t.Errorf("poll count was reset to %d", m.polls)
	}
	if len(m.events) < eventsBefore {
		t.Error("the event ring was discarded")
	}
	if len(m.trend.Points) == 0 {
		t.Error("the trend history was discarded")
	}
	if m.eventCounts["listener_up"] != 1 {
		t.Errorf("event counters were reset: %v", m.eventCounts)
	}
}

func TestReloadRecordsAnEvent(t *testing.T) {
	writeRawConfig(t, `{"alert_rtt_ms": 100}`)
	m := NewModel().WithConfig()
	m.w, m.h = 160, 45

	m.reloadConfig()
	last := m.events[len(m.events)-1]
	if last.Kind != "config_reloaded" || last.Level != levelInfo {
		t.Errorf("clean reload event = %+v, want an info config_reloaded", last)
	}

	// A reload that silently drops half the config must say so, which is the
	// same silence --check-config exists to break.
	writeRawConfig(t, `{"theme": "neon", "alert_rtt_ms": -1}`)
	m.reloadConfig()
	last = m.events[len(m.events)-1]
	if last.Kind != "config_reloaded" || last.Level != levelWarn {
		t.Errorf("reload with problems = %+v, want a warn", last)
	}
	if !strings.Contains(last.Detail, "2 problems") || !strings.Contains(last.Detail, "--check-config") {
		t.Errorf("detail = %q, want the count and what to run", last.Detail)
	}
}

// A snapshot or remote session is showing another host; reloading would
// replace its forced settings with this machine's.
func TestReloadIgnoredForForeignSessions(t *testing.T) {
	writeRawConfig(t, `{"alert_rtt_ms": 100}`)
	m := NewModel().WithConfig()
	m.w, m.h = 160, 45
	m.snapshotOnly = true
	before := m.alertRules.rttMs

	writeRawConfig(t, `{"alert_rtt_ms": 999}`)
	m.reloadConfig()

	if m.alertRules.rttMs != before {
		t.Errorf("a snapshot session reloaded this host's config: rttMs = %v", m.alertRules.rttMs)
	}
	if !strings.Contains(m.status, "another host") {
		t.Errorf("status = %q, want it to say why", m.status)
	}
}

func TestReloadMsgReachesTheHandler(t *testing.T) {
	writeRawConfig(t, `{"alert_rtt_ms": 100}`)
	m := NewModel().WithConfig()
	m.w, m.h = 160, 45

	writeRawConfig(t, `{"alert_rtt_ms": 250}`)
	out, _ := m.Update(ReloadMsg{})
	if got := out.(Model).alertRules.rttMs; got != 250 {
		t.Errorf("rttMs = %v after ReloadMsg, want 250", got)
	}
}

// --- live metrics --------------------------------------------------------

func statsModel() Model {
	m := NewModel()
	m.w, m.h = 160, 45
	m.lastSnap = probe.NetSnapshot{
		Hostname: "statshost", TakenAt: time.Now(),
		Ifaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true, RxBps: 1000, TxBps: 2000}},
	}
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}}
	m.procs = []probe.ProcNet{{PID: 1, Name: "sshd"}}
	m.conns = []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:4001", Remote: "8.8.8.8:443", State: "ESTABLISHED", PID: 1}}
	m.sessionTotal = byteTotals{rx: 111, tx: 222}
	m.polls = 7
	m.lastPollAt = time.Now().Add(-3 * time.Second)
	return m
}

func TestStatsRequestAnsweredFromTheUpdateLoop(t *testing.T) {
	m := statsModel()
	reply := make(chan SessionStats, 1)

	out, _ := m.Update(StatsRequest{Reply: reply})
	if out == nil {
		t.Fatal("StatsRequest returned no model")
	}

	select {
	case s := <-reply:
		if s.Snap.Hostname != "statshost" {
			t.Errorf("Snap.Hostname = %q", s.Snap.Hostname)
		}
		if len(s.Info.Conns) != 1 || len(s.Info.Ports) != 1 || len(s.Info.Procs) != 1 {
			t.Errorf("Info did not carry the tables: %+v", s.Info)
		}
		if s.Polls != 7 {
			t.Errorf("Polls = %d", s.Polls)
		}
		if s.Totals.rx != 111 || s.Totals.tx != 222 {
			t.Errorf("Totals = %+v", s.Totals)
		}
	default:
		t.Fatal("no reply was sent")
	}
}

// A scraper that has already timed out leaves an unread channel behind; the
// update loop must not block on it.
func TestStatsRequestDoesNotBlock(t *testing.T) {
	m := statsModel()
	full := make(chan SessionStats) // unbuffered, nobody receiving

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Update(StatsRequest{Reply: full})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Update blocked on an abandoned reply channel")
	}
}

func TestFormatSessionMetrics(t *testing.T) {
	m := statsModel()
	m.recordEvent(event{At: time.Now(), Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:8080"})
	m.recordEvent(event{At: time.Now(), Level: levelInfo, Kind: "new_destination", Subject: "8.8.8.8:443"})
	m.hookErrors = 2

	out := FormatSessionMetrics(m.sessionStats(), "9.9.9")
	for _, want := range []string{
		`ducknetview_build_info{version="9.9.9"} 1`,
		"ducknetview_polls_total 7",
		`ducknetview_session_bytes_total{direction="rx"} 111`,
		`ducknetview_session_bytes_total{direction="tx"} 222`,
		`ducknetview_events_total{kind="listener_up"} 1`,
		`ducknetview_events_total{kind="new_destination"} 1`,
		"ducknetview_alerts_total 1",
		"ducknetview_alert_hook_failures_total 2",
		"ducknetview_paused 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	// Every metric must be preceded by its HELP and TYPE, or a strict parser
	// rejects the whole scrape.
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			t.Error("blank line in the exposition")
		}
	}
	if n := strings.Count(out, "# HELP "); n != strings.Count(out, "# TYPE ") {
		t.Errorf("%d HELP lines but %d TYPE lines", n, strings.Count(out, "# TYPE "))
	}
}

// The age of the last poll is the one number that can reveal a daemon which is
// up but no longer working, so "never polled" must be distinguishable from
// "polled just now".
func TestLastPollAgeBeforeTheFirstPoll(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 45
	out := FormatSessionMetrics(m.sessionStats(), "0")
	if !strings.Contains(out, "ducknetview_last_poll_age_seconds -1") {
		t.Errorf("want -1 before the first poll, got:\n%s", out)
	}

	m.lastPollAt = time.Now()
	out = FormatSessionMetrics(m.sessionStats(), "0")
	if strings.Contains(out, "ducknetview_last_poll_age_seconds -1") {
		t.Errorf("still -1 after a poll:\n%s", out)
	}
}

// applyConnInfo is what a real poll goes through, so the counters have to be
// driven from there rather than set by hand anywhere else.
func TestPollCountersAdvanceOnRealPolls(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 45

	before := m.polls
	m.applyConnInfo(probe.ConnInfo{
		Ports: []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}},
		Conns: []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:1", Remote: "8.8.8.8:443"}},
	}, time.Now())

	if m.polls != before+1 {
		t.Errorf("polls = %d, want %d", m.polls, before+1)
	}
	if m.lastPollAt.IsZero() {
		t.Error("lastPollAt was not set by a poll")
	}
}

// The event ring holds 500; a long session records far more, and the counters
// are what the metrics report.
func TestEventCountersOutliveTheRing(t *testing.T) {
	m := NewModel()
	for i := 0; i < eventLimit+50; i++ {
		m.recordEvent(event{At: time.Now(), Kind: "new_destination", Subject: "x"})
	}
	if len(m.events) != eventLimit {
		t.Fatalf("ring holds %d, want %d", len(m.events), eventLimit)
	}
	if got := m.eventCounts["new_destination"]; got != eventLimit+50 {
		t.Errorf("counter = %d, want %d — it must not be derived from the ring", got, eventLimit+50)
	}
}

// The live scrape has to be a superset of the sampled one, or switching to it
// would silently drop series from existing dashboards.
func TestLiveMetricsIncludeTheHostMetrics(t *testing.T) {
	m := statsModel()
	s := m.sessionStats()

	sampled := FormatMetrics(s.Snap, s.Info, s.OffBaseline, s.BaselineKnown)
	live := FormatLiveMetrics(s, "0.0.0")

	for _, line := range strings.Split(strings.TrimSpace(sampled), "\n") {
		if !strings.Contains(live, line) {
			t.Errorf("the live scrape is missing a sampled line: %q", line)
		}
	}
	if !strings.Contains(live, "ducknetview_polls_total") {
		t.Error("the live scrape does not add the session metrics")
	}
}

// A remote session's metrics must say whose host they describe, and whether
// the link to it is working.
func TestRemoteMetrics(t *testing.T) {
	m, err := NewModel().WithRemote("ssh://faraway")
	if err != nil {
		t.Fatal(err)
	}
	m.w, m.h = 160, 45

	out := FormatSessionMetrics(m.sessionStats(), "0")
	if !strings.Contains(out, `ducknetview_remote_up{host="faraway"} 1`) {
		t.Errorf("want remote_up 1 for a healthy session:\n%s", out)
	}

	m.remoteErr = errString("Permission denied")
	out = FormatSessionMetrics(m.sessionStats(), "0")
	if !strings.Contains(out, `ducknetview_remote_up{host="faraway"} 0`) {
		t.Errorf("want remote_up 0 after a failed fetch:\n%s", out)
	}

	// A local session has no remote, and a constant 1 would be a lie.
	if out := FormatSessionMetrics(statsModel().sessionStats(), "0"); strings.Contains(out, "remote_up") {
		t.Error("a local session exported remote_up")
	}
}

func TestStatsBaselineCountsComeFromTheSession(t *testing.T) {
	m := statsModel()
	if s := m.sessionStats(); s.BaselineKnown {
		t.Error("BaselineKnown = true with no baseline saved")
	}

	m.baseline = map[string]bool{"tcp|0.0.0.0:22": true}
	m.baselineRaw = []string{"tcp|0.0.0.0:22"}
	m.ports = append(m.ports, probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:8080", PID: 2, Process: "nginx"})

	s := m.sessionStats()
	if !s.BaselineKnown {
		t.Fatal("BaselineKnown = false with a baseline saved")
	}
	if s.OffBaseline != 1 {
		t.Errorf("OffBaseline = %d, want 1", s.OffBaseline)
	}
}

// tea.Msg satisfaction is what lets main send these through the program.
var (
	_ tea.Msg = StatsRequest{}
	_ tea.Msg = ReloadMsg{}
	_ tea.Msg = DaemonShutdownMsg{}
)

// --- help overlay honesty ------------------------------------------------

// The overlay tells the reader these are their own bindings. That is only true
// if every row comes from the keymap: a row that hardcodes its key shows the
// default to somebody who rebound it, which is worse than showing nothing.
func TestHelpOverlayShowsEveryReboundKey(t *testing.T) {
	// Give every action an unmistakable binding, then check the overlay shows
	// it rather than the default.
	letters := "abcdefghijklmnopqrstuvwxyz"
	overrides := map[string]string{}
	i := 0
	for _, action := range Actions() {
		// Alt-prefixed keys collide with nothing and are never reserved.
		overrides[action] = "alt+" + string(letters[i%len(letters)]) + strings.Repeat("'", i/len(letters))
		i++
	}

	m := NewModel()
	m.w, m.h = 200, 60
	m.keymap = newKeymap(overrides)

	txt := stripANSI(m.renderHelp())
	for _, action := range Actions() {
		key := m.keymap.keyFor(action)
		if key != overrides[action] {
			t.Fatalf("setup: %s is bound to %q, want %q", action, key, overrides[action])
		}
		if !strings.Contains(txt, key) {
			t.Errorf("the help overlay does not show %q, the key bound to %q", key, action)
		}
	}
}

// Every rebindable action must name a key the handler actually dispatches on,
// or the binding silently does nothing.
func TestEveryActionReachesTheHandler(t *testing.T) {
	m := drillModel(t)
	m.activeTab = tabConns
	for _, action := range Actions() {
		def := defaultKeys[action]
		if def == "" {
			t.Errorf("action %q has no default key", action)
			continue
		}
		// translate maps a bound key onto the default the switch is written
		// against; with no overrides it must be the identity.
		if got := m.keymap.translate(def); got != def {
			t.Errorf("default key %q for %q does not reach the handler (got %q)", def, action, got)
		}
	}
}

// The space bar has no printable form; showing an empty cell would read as a
// missing binding.
func TestHelpNamesTheSpaceBar(t *testing.T) {
	m := NewModel()
	m.w, m.h = 200, 60
	if !strings.Contains(stripANSI(m.renderHelp()), "space") {
		t.Error("the help overlay does not name the space bar")
	}
	if got := spaceLabel("Z"); got != "Z" {
		t.Errorf("spaceLabel(Z) = %q", got)
	}
}

// Match navigation was the one pair of keys the handler understood but the
// keymap did not, which made the "rebind any action" claim untrue.
func TestSearchNavigationIsRebindable(t *testing.T) {
	m := drillModel(t)
	m.activeTab = tabConns
	m.connsQuery = "8.8.8.8"
	m.searchMode = searchHighlight
	m.keymap = newKeymap(map[string]string{"search-next": "]", "search-prev": "["})

	if got := m.keymap.translate("]"); got != "n" {
		t.Errorf("translate(]) = %q, want n", got)
	}
	if got := m.keymap.translate("["); got != "N" {
		t.Errorf("translate([) = %q, want N", got)
	}

	before := m.connsCursor
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if out.(Model).connsCursor == before && len(m.filteredConns()) > 1 {
		t.Error("the rebound match-navigation key did not move the cursor")
	}
}
