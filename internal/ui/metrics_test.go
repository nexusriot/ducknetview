package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestFormatMetrics(t *testing.T) {
	snap := probe.NetSnapshot{Ifaces: []probe.IfaceInfo{
		{Name: "eth0", IsUp: true, Kind: probe.IfacePhysical, RxBps: 1000, TxBps: 500, Errin: 2, Dropout: 3},
		{Name: "eth1", IsUp: false, Kind: probe.IfacePhysical},
	}}
	info := probe.ConnInfo{
		Ports: []probe.ListenPort{
			{Proto: "tcp", Local: "0.0.0.0:22"},
			{Proto: "tcp", Local: "127.0.0.1:5432"},
		},
		Conns: []probe.Conn{
			{Proto: "tcp", Local: "10.0.0.1:1", Remote: "8.8.8.8:443", Retrans: 4},
			{Proto: "tcp", Local: "10.0.0.1:2", Remote: "10.0.0.9:443"},
		},
		Procs: []probe.ProcNet{{PID: 1}},
	}

	out := FormatMetrics(snap, info, 2, true)
	for _, want := range []string{
		`ducknetview_iface_receive_bytes_per_second{iface="eth0",kind="physical"} 1000`,
		`ducknetview_iface_up{iface="eth1"} 0`,
		`ducknetview_iface_errors_total{iface="eth0",direction="in"} 2`,
		`ducknetview_listeners{exposure="exposed"} 1`,
		`ducknetview_listeners{exposure="local"} 1`,
		`ducknetview_connections{scope="public"} 1`,
		"ducknetview_connection_retransmits_total 4",
		"ducknetview_processes_with_sockets 1",
		"ducknetview_listeners_off_baseline 2",
		"# TYPE ducknetview_iface_up gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics missing %q:\n%s", want, out)
		}
	}

	// No baseline is different from a clean one, and says so.
	if !strings.Contains(FormatMetrics(snap, info, 0, false), "ducknetview_listeners_off_baseline -1") {
		t.Error("an unknown baseline should export -1, not 0")
	}
}

func TestFormatTopTalkers(t *testing.T) {
	before := probe.ConnInfo{Conns: []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", PID: 1, Process: "curl", RxBytes: 0},
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", PID: 2, Process: "quiet", RxBytes: 0},
	}}
	after := probe.ConnInfo{Conns: []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", PID: 1, Process: "curl", RxBytes: 2_000_000},
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", PID: 2, Process: "quiet", RxBytes: 100},
	}}

	out := FormatTopTalkers(before, after, 2, 5)
	if !strings.Contains(out, "top processes") || !strings.Contains(out, "top hosts") {
		t.Fatalf("output:\n%s", out)
	}
	// The busiest comes first in both lists.
	procIdx := strings.Index(out, "curl")
	quietIdx := strings.Index(out, "quiet")
	if procIdx < 0 || quietIdx < 0 || procIdx > quietIdx {
		t.Errorf("expected curl ranked above quiet:\n%s", out)
	}
	if !strings.Contains(out, "1.1.1.1") {
		t.Errorf("hosts section should name the remote:\n%s", out)
	}

	// Nothing moving is stated, not left blank.
	if !strings.Contains(FormatTopTalkers(before, before, 1, 5), "nothing moved") {
		t.Error("an idle sample should say so")
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
