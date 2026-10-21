package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A live reload runs on the update loop, so the check it reports from must not
// resolve hostnames or load the ASN table. It still has to catch everything
// that costs nothing to see.
func TestCheckConfigFastSkipsTheExpensiveChecks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := map[string]any{
		"asn_database": filepath.Join(dir, "does-not-exist.tsv"),
		"theme":        "not-a-theme",
	}
	b, _ := json.Marshal(cfg)
	writeConfig(t, string(b))

	full := CheckConfig()
	fast := CheckConfigFast()

	hasKey := func(r ConfigReport, key string) bool {
		for _, p := range r.Problems {
			if p.Key == key {
				return true
			}
		}
		return false
	}
	if !hasKey(full, "asn_database") {
		t.Error("the full check should report an unloadable ASN database")
	}
	if hasKey(fast, "asn_database") {
		t.Error("the fast check must not load the ASN table")
	}
	if !hasKey(fast, "theme") {
		t.Error("the fast check should still report a cheap problem")
	}
}

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

// encoding/json applies a key that differs only in case, so the check must not
// call it ignored or fail on it.
func TestCheckConfigCaseVariantKeyIsWarning(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	p, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"Alert_Process_Bps": 1000}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := CheckConfig()
	if !r.OK() {
		t.Fatalf("case-variant key failed the check: %+v", r.Problems)
	}
	if len(r.Problems) != 1 || !r.Problems[0].Warning {
		t.Fatalf("want one warning, got %+v", r.Problems)
	}
	if out := FormatConfigReport(r); !strings.Contains(out, "warning") || strings.Contains(out, "ignored at startup") {
		t.Errorf("report:\n%s", out)
	}
}
