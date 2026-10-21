package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestLatencyAndLossAlerts(t *testing.T) {
	m := eventsModel()
	m.alertRules = alertRules{rttMs: 200, retransPerMin: 30}
	t0 := time.Now()

	slow := probe.Conn{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443",
		State: "ESTABLISHED", PID: 1, Process: "curl", RTT: 450 * time.Millisecond, Retrans: 5}
	m.noteQualityAlerts([]probe.Conn{slow}, t0)

	if countKind(m.events, "latency") != 1 {
		t.Fatalf("expected a latency alert, got %v", kindsOf(m.events))
	}
	e := m.events[len(m.events)-1]
	if e.Level != levelWarn || !strings.Contains(e.Detail, "threshold") {
		t.Errorf("latency event = %+v", e)
	}
	// No retransmit alert yet: the first poll only establishes the baseline.
	if countKind(m.events, "packet_loss") != 0 {
		t.Errorf("loss alerted without a delta: %v", kindsOf(m.events))
	}

	// 60 more retransmits in 30s is 120/min, well over the threshold.
	slow.Retrans = 65
	m.noteQualityAlerts([]probe.Conn{slow}, t0.Add(30*time.Second))
	if countKind(m.events, "packet_loss") != 1 {
		t.Fatalf("expected a packet_loss alert, got %v", kindsOf(m.events))
	}
	if !strings.Contains(m.events[len(m.events)-1].Detail, "retransmits/min") {
		t.Errorf("loss event = %+v", m.events[len(m.events)-1])
	}

	// A healthy connection raises nothing.
	m2 := eventsModel()
	m2.alertRules = alertRules{rttMs: 200, retransPerMin: 30}
	fine := probe.Conn{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", RTT: 12 * time.Millisecond}
	m2.noteQualityAlerts([]probe.Conn{fine}, t0)
	m2.noteQualityAlerts([]probe.Conn{fine}, t0.Add(time.Minute))
	if len(m2.events) != 0 {
		t.Errorf("healthy connection produced %v", kindsOf(m2.events))
	}
}

func TestLatencyPanel(t *testing.T) {
	m := routesModel()
	m.routes = []probe.Route{{Destination: "default", Gateway: "192.168.1.1", Iface: "eth0", Metric: 100}}
	m.latencyExtra = []string{"1.1.1.1:443"}

	targets := m.latencyTargets()
	if len(targets) != 2 || targets[0].Label != "gateway" || targets[1].Addr != "1.1.1.1:443" {
		t.Fatalf("targets = %+v, want the gateway plus the configured extra", targets)
	}

	if m.renderLatency(80) != "" {
		t.Error("the panel should stay hidden before anything is measured")
	}

	m.recordLatency([]probe.LatencySample{
		{Label: "gateway", RTT: 2500 * time.Microsecond},
		{Label: "1.1.1.1:443", Err: os.ErrDeadlineExceeded},
	})
	m.recordLatency([]probe.LatencySample{
		{Label: "gateway", RTT: 3 * time.Millisecond},
		{Label: "1.1.1.1:443", RTT: 240 * time.Millisecond},
	})

	panel := stripANSI(m.renderLatency(80))
	if !strings.Contains(panel, "Path latency") || !strings.Contains(panel, "gateway") {
		t.Fatalf("latency panel:\n%s", panel)
	}
	if !strings.Contains(panel, "3.0ms") || !strings.Contains(panel, "240.0ms") {
		t.Errorf("panel should show both round trips:\n%s", panel)
	}
	if len(m.latencyHist["gateway"]) != 2 {
		t.Errorf("history holds %d samples, want 2", len(m.latencyHist["gateway"]))
	}

	// A failed probe says so rather than showing a zero.
	m.recordLatency([]probe.LatencySample{{Label: "gateway", Err: os.ErrDeadlineExceeded}})
	if !strings.Contains(stripANSI(m.renderLatency(80)), "no response") {
		t.Error("a failed probe should be reported, not rendered as 0ms")
	}
}

// A latency target without a port can never connect, and said nothing about
// why: the panel just showed "no response" forever.
func TestInvalidLatencyTargetsAreReported(t *testing.T) {
	m := routesModel()
	m.latencyExtra = []string{"1.1.1.1:443", "1.1.1.1", "nonsense::::"}

	targets := m.latencyTargets()
	for _, tg := range targets {
		if tg.Addr == "1.1.1.1" {
			t.Error("a target with no port should not be probed")
		}
	}
	if len(targets) != 1 {
		t.Errorf("probing %d targets, want only the valid one: %+v", len(targets), targets)
	}

	m.recordLatency([]probe.LatencySample{{Label: "1.1.1.1:443", RTT: time.Millisecond}})
	panel := stripANSI(m.renderLatency(90))
	if !strings.Contains(panel, "1.1.1.1") {
		t.Errorf("valid target missing from the panel:\n%s", panel)
	}
	if !strings.Contains(panel, "unusable") {
		t.Errorf("the panel should say which configured targets cannot be probed:\n%s", panel)
	}
}
