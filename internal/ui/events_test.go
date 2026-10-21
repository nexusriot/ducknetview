package ui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// The first enumeration is a baseline: a host that already has fifty listeners
// must not produce fifty "listener up" events on startup.
func TestFirstEnumerationDoesNotFloodEvents(t *testing.T) {
	m := feed(t, eventsModel(),
		[]probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}}, nil)

	if n := countKind(m.events, "listener_up"); n != 0 {
		t.Errorf("startup produced %d listener_up events, want 0 (kinds: %v)", n, kindsOf(m.events))
	}
}

func TestListenerEventsUpAndDown(t *testing.T) {
	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	backdoor := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:4444", PID: 66, Process: "nc"}

	m := feed(t, eventsModel(), []probe.ListenPort{sshd}, nil)
	m = feed(t, m, []probe.ListenPort{sshd, backdoor}, nil)

	if countKind(m.events, "listener_up") != 1 {
		t.Fatalf("expected one listener_up, got kinds %v", kindsOf(m.events))
	}
	up := m.events[len(m.events)-1]
	if !strings.Contains(up.Subject, "4444") || !strings.Contains(up.Detail, "nc") {
		t.Errorf("listener_up event = %+v, want the new listener and its process", up)
	}
	if up.Level != levelWarn {
		t.Errorf("new exposed listener level = %v, want warn", up.Level)
	}

	m = feed(t, m, []probe.ListenPort{sshd}, nil)
	if countKind(m.events, "listener_down") != 1 {
		t.Fatalf("expected a listener_down event, got kinds %v", kindsOf(m.events))
	}
}

// A listener outside the saved baseline is an alert, not a mere warning.
func TestListenerEventLevelUsesBaseline(t *testing.T) {
	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	backdoor := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:4444", PID: 66, Process: "nc"}

	m := feed(t, eventsModel(), []probe.ListenPort{sshd}, nil)
	m.setBaseline()
	m = feed(t, m, []probe.ListenPort{sshd, backdoor}, nil)

	last := m.events[len(m.events)-1]
	if last.Level != levelAlert || !strings.Contains(last.Detail, "baseline") {
		t.Errorf("off-baseline listener event = %+v, want an alert mentioning the baseline", last)
	}
}

// One poll may not be allowed to fill the whole ring buffer, and the truncation
// has to be visible rather than silent.
func TestEventsPerPollCapIsReported(t *testing.T) {
	m := eventsModel()
	m.watch = parseWatchlist([]string{"0.0.0.0/0"})

	var conns []probe.Conn
	for i := 0; i < eventsPerPollLimit+7; i++ {
		conns = append(conns, probe.Conn{
			Proto: "tcp", Local: "10.0.0.1:" + strconv.Itoa(i), Remote: "8.8.8." + strconv.Itoa(i) + ":443",
			State: "ESTABLISHED", PID: int32(i),
		})
	}
	m = feed(t, m, nil, conns)

	if n := countKind(m.events, "watchlist_hit"); n != eventsPerPollLimit+1 {
		t.Errorf("watchlist_hit events = %d, want %d capped rows plus one summary", n, eventsPerPollLimit+1)
	}
	summary := m.events[len(m.events)-1]
	if !strings.Contains(summary.Subject, "more") {
		t.Errorf("last event = %+v, want a summary of the suppressed events", summary)
	}
}

func TestEventRingBufferIsCapped(t *testing.T) {
	m := eventsModel()
	for i := 0; i < eventLimit+50; i++ {
		m.recordEvent(event{At: time.Now(), Kind: "test", Subject: strconv.Itoa(i)})
	}
	if len(m.events) != eventLimit {
		t.Fatalf("ring buffer holds %d events, want %d", len(m.events), eventLimit)
	}
	// The oldest entries are the ones dropped.
	if m.events[0].Subject != strconv.Itoa(50) {
		t.Errorf("oldest retained event = %q, want %q", m.events[0].Subject, strconv.Itoa(50))
	}
}

func TestIfaceEvents(t *testing.T) {
	m := eventsModel()
	snap := func(ifaces ...probe.IfaceInfo) probe.NetSnapshot {
		return probe.NetSnapshot{Ifaces: ifaces, TakenAt: time.Now()}
	}
	eth := probe.IfaceInfo{Name: "eth0", IsUp: true}

	m.noteIfaceEvents(snap(eth))
	if len(m.events) != 0 {
		t.Fatalf("first snapshot produced events: %v", kindsOf(m.events))
	}

	m.noteIfaceEvents(snap(probe.IfaceInfo{Name: "eth0", IsUp: false}))
	if countKind(m.events, "iface_state") != 1 {
		t.Errorf("expected an iface_state event, got %v", kindsOf(m.events))
	}

	m.noteIfaceEvents(snap(probe.IfaceInfo{Name: "eth0", IsUp: false}, probe.IfaceInfo{Name: "tun0", IsUp: true}))
	if countKind(m.events, "iface_added") != 1 {
		t.Errorf("expected an iface_added event for the VPN, got %v", kindsOf(m.events))
	}

	m.noteIfaceEvents(snap(probe.IfaceInfo{Name: "eth0", IsUp: false}))
	if countKind(m.events, "iface_removed") != 1 {
		t.Errorf("expected an iface_removed event, got %v", kindsOf(m.events))
	}
}

func TestEventsTabRendersAndClears(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabEvents
	m.recordEvent(event{At: time.Now(), Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:4444", Detail: "pid 66 (nc)"})

	text := m.renderEventsText()
	if !strings.Contains(text, "listener_up") || !strings.Contains(text, "4444") {
		t.Fatalf("event row missing from the table:\n%s", text)
	}
	if !strings.Contains(eventsTabLabel(m.unreadAlerts()), "!1") {
		t.Errorf("tab label = %q, want an alert count", eventsTabLabel(m.unreadAlerts()))
	}

	model, _ := m.Update(key("c"))
	m = model.(Model)
	if len(m.events) != 0 {
		t.Errorf("c should clear the events, %d left", len(m.events))
	}
}

func TestEventLogWritesJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")

	m, err := eventsModel().OpenEventLog(path)
	if err != nil {
		t.Fatalf("OpenEventLog: %v", err)
	}
	m.recordEvent(event{At: time.Now(), Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:4444", Detail: "pid 66 (nc)"})
	m.recordEvent(event{At: time.Now(), Level: levelInfo, Kind: "iface_state", Subject: "eth0"})
	if m.eventLogErr != nil {
		t.Fatalf("event log write failed: %v", m.eventLogErr)
	}
	_ = m.eventLog.Close()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	var lines int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("line %d is not JSON: %v (%q)", lines+1, err, sc.Text())
		}
		if rec["kind"] == "" || rec["at"] == "" || rec["level"] == "" {
			t.Errorf("line %d missing required fields: %v", lines+1, rec)
		}
		lines++
	}
	if lines != 2 {
		t.Errorf("log has %d lines, want 2", lines)
	}
}

func TestEphemeralUDPListenersDoNotLogEvents(t *testing.T) {
	dns := probe.ListenPort{Proto: "udp", Local: "0.0.0.0:53", PID: 1, Process: "resolved"}
	client := probe.ListenPort{Proto: "udp", Local: "192.168.1.5:47572", PID: 2, Process: "firefox"}

	m := feed(t, eventsModel(), []probe.ListenPort{dns}, nil)
	m = feed(t, m, []probe.ListenPort{dns, client}, nil)
	if n := countKind(m.events, "listener_up"); n != 0 {
		t.Errorf("ephemeral UDP socket produced %d listener_up events, want 0", n)
	}

	m = feed(t, m, []probe.ListenPort{dns}, nil)
	if n := countKind(m.events, "listener_down"); n != 0 {
		t.Errorf("ephemeral UDP socket produced %d listener_down events, want 0", n)
	}
}

func TestEventsSearchAndLevelFilter(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabEvents
	now := time.Now()
	m.recordEvent(event{At: now, Level: levelInfo, Kind: "new_destination", Subject: "1.2.3.4:443"})
	m.recordEvent(event{At: now, Level: levelWarn, Kind: "listener_up", Subject: "tcp 0.0.0.0:8080"})
	m.recordEvent(event{At: now, Level: levelAlert, Kind: "watchlist_hit", Subject: "45.9.1.2:443"})

	if got := len(m.eventRows()); got != 3 {
		t.Fatalf("unfiltered events = %d, want 3", got)
	}

	// l cycles all -> warn+ -> alerts only.
	model, _ := m.Update(key("l"))
	m = model.(Model)
	if got := len(m.eventRows()); got != 2 {
		t.Errorf("warn+ filter kept %d events, want 2", got)
	}
	model, _ = m.Update(key("l"))
	m = model.(Model)
	rows := m.eventRows()
	if len(rows) != 1 || rows[0].Kind != "watchlist_hit" {
		t.Errorf("alerts-only filter kept %v, want just the watchlist hit", kindsOf(rows))
	}
	if got := m.activeRowCount(); got != 1 {
		t.Errorf("activeRowCount = %d, want it to follow the filter", got)
	}
	model, _ = m.Update(key("l"))
	m = model.(Model)
	if got := len(m.eventRows()); got != 3 {
		t.Errorf("filter did not cycle back to all: %d events", got)
	}

	// Search narrows by kind, subject or detail.
	m.eventsQuery = "listener"
	rows = m.eventRows()
	if len(rows) != 1 || rows[0].Kind != "listener_up" {
		t.Errorf("search kept %v, want the listener event", kindsOf(rows))
	}
	if !strings.Contains(m.renderEventsText(), "level: all") {
		t.Error("the events subtitle should report the level filter")
	}
}

func TestEventsSearchBoxCapturesKeys(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabEvents
	m.recordEvent(event{At: time.Now(), Level: levelWarn, Kind: "listener_up", Subject: "tcp 0.0.0.0:8080"})

	model, _ := m.Update(key("/"))
	m = model.(Model)
	if !m.eventsSearching || !m.anySearching() {
		t.Fatal("/ should focus the events search box")
	}

	// Keys that are commands elsewhere must reach the input while it is focused.
	for _, k := range []string{"l", "c", "t", "z"} {
		model, _ = m.Update(key(k))
		m = model.(Model)
	}
	if m.eventLevelFilter != 0 || len(m.events) == 0 {
		t.Error("command keys leaked past the focused search box")
	}
	if got := m.eventsSearch.Value(); got != "lctz" {
		t.Errorf("search box contains %q, want %q", got, "lctz")
	}
}

// A service moving from loopback to a wildcard bind is one event, and an alert.
func TestListenerMovedCorrelation(t *testing.T) {
	local := probe.ListenPort{Proto: "tcp", Local: "127.0.0.1:5432", PID: 10, Process: "postgres"}
	exposed := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:5432", PID: 10, Process: "postgres"}

	m := feed(t, eventsModel(), []probe.ListenPort{local}, nil)
	m = feed(t, m, []probe.ListenPort{exposed}, nil)

	if n := countKind(m.events, "listener_moved"); n != 1 {
		t.Fatalf("expected one listener_moved, got %v", kindsOf(m.events))
	}
	if countKind(m.events, "listener_up") != 0 || countKind(m.events, "listener_down") != 0 {
		t.Errorf("the move should replace the up/down pair, got %v", kindsOf(m.events))
	}
	e := m.events[len(m.events)-1]
	if e.Level != levelAlert {
		t.Errorf("widening exposure = %v, want an alert", e.Level)
	}
	if !strings.Contains(e.Detail, "127.0.0.1:5432") || !strings.Contains(e.Detail, "local → exposed") {
		t.Errorf("move detail = %q, want both addresses and the exposure change", e.Detail)
	}

	// Moving back the other way is information, not an alert.
	m = feed(t, m, []probe.ListenPort{local}, nil)
	back := m.events[len(m.events)-1]
	if back.Kind != "listener_moved" || back.Level != levelAlert && back.Level != levelInfo {
		t.Fatalf("unexpected event %+v", back)
	}
	if back.Level != levelInfo {
		t.Errorf("narrowing exposure = %v, want info", back.Level)
	}
}

func TestEventLogRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	m, err := eventsModel().OpenEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	m.eventLogMax = 512 // rotate quickly rather than after 8 MiB

	for i := 0; i < 40; i++ {
		m.recordEvent(event{At: time.Now(), Level: levelInfo, Kind: "test",
			Subject: strings.Repeat("x", 40), Detail: "padding"})
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("previous generation missing: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	if info.Size() >= 512*2 {
		t.Errorf("current log is %d bytes; rotation did not reset it", info.Size())
	}
	if m.eventLogErr != nil {
		t.Errorf("rotation reported an error: %v", m.eventLogErr)
	}
	_ = m.eventLog.Close()
}

// --- headless mode -------------------------------------------------------

// A daemon's stdout is its log. Without the echo it would be completely silent
// about the events it exists to notice.
func TestEventEcho(t *testing.T) {
	var buf bytes.Buffer
	m := NewModel().WithEventEcho(&buf)

	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	m.recordEvent(event{At: at, Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:8080", Detail: "pid 42 (nginx)"})
	m.recordEvent(event{At: at, Level: levelInfo, Kind: "iface_state", Subject: "wg0"})

	out := buf.String()
	for _, want := range []string{"2026-10-11T09:00:00Z", "alert", "listener_up", "tcp 0.0.0.0:8080", "pid 42 (nginx)", "iface_state", "wg0"} {
		if !strings.Contains(out, want) {
			t.Errorf("echoed log missing %q:\n%s", want, out)
		}
	}
	if n := len(strings.Split(strings.TrimSpace(out), "\n")); n != 2 {
		t.Errorf("echoed %d lines, want 2", n)
	}
}

// A model with no echo writer configured must not panic — the TUI never sets
// one.
func TestEventEchoOptional(t *testing.T) {
	m := NewModel()
	m.recordEvent(event{At: time.Now(), Kind: "test", Subject: "x"})
	if len(m.events) != 1 {
		t.Fatalf("event not recorded")
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

func TestListenerEventsNameTheUnit(t *testing.T) {
	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	web := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:80", PID: 7, Process: "nginx", Unit: "nginx.service"}

	m := feed(t, eventsModel(), []probe.ListenPort{sshd}, nil)
	m = feed(t, m, []probe.ListenPort{sshd, web}, nil)
	m = feed(t, m, []probe.ListenPort{sshd}, nil)

	for _, kind := range []string{"listener_up", "listener_down"} {
		found := false
		for _, e := range m.events {
			if e.Kind == kind {
				found = true
				if !strings.Contains(e.Detail, "pid 7 (nginx, nginx.service)") {
					t.Errorf("%s detail = %q, want the unit beside the process", kind, e.Detail)
				}
			}
		}
		if !found {
			t.Errorf("no %s event (kinds: %v)", kind, kindsOf(m.events))
		}
	}
}

func TestProcUnitLabel(t *testing.T) {
	cases := []struct {
		pid        int32
		name, unit string
		want       string
	}{
		{7, "nginx", "nginx.service", "pid 7 (nginx, nginx.service)"},
		{7, "", "nginx.service", "pid 7 (nginx.service)"},
		{7, "nginx", "", "pid 7 (nginx)"},
		{0, "nginx", "nginx.service", ""},
	}
	for _, c := range cases {
		if got := procUnitLabel(c.pid, c.name, c.unit); got != c.want {
			t.Errorf("procUnitLabel(%d, %q, %q) = %q, want %q", c.pid, c.name, c.unit, got, c.want)
		}
	}
}
