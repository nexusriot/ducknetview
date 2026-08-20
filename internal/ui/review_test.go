package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// A snapshot loaded with --from must not be replaced by the local host. The
// commands Init issues at startup arrive as ordinary messages, so the handlers
// have to refuse them.
func TestSnapshotIsNotOverwrittenByLiveData(t *testing.T) {
	m := routesModel().WithSnapshot(Snapshot{
		Hostname:   "remote-host",
		TakenAt:    time.Now().Format(time.RFC3339),
		Interfaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true, Addrs: []string{"10.0.0.5/24"}}},
		Ports:      []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}},
		Connections: []probe.Conn{
			{Proto: "tcp", Local: "10.0.0.5:5000", Remote: "1.2.3.4:443", PID: 2, Process: "curl", RxBytes: 10},
		},
	})

	// Whatever the local host reports must be ignored.
	model, _ := m.Update(snapMsg(probe.NetSnapshot{
		Hostname: "local-host",
		Ifaces:   []probe.IfaceInfo{{Name: "wlan0", IsUp: true}},
		TakenAt:  time.Now(),
	}))
	m = model.(Model)
	if m.lastSnap.Hostname != "remote-host" {
		t.Errorf("a live sample replaced the snapshot's host with %q", m.lastSnap.Hostname)
	}

	model, _ = m.Update(connInfoMsg(probe.ConnInfo{
		Ports: []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:9999", PID: 9, Process: "local-daemon"}},
		Conns: []probe.Conn{{Proto: "tcp", Local: "192.168.1.5:1", Remote: "9.9.9.9:443", PID: 9}},
	}))
	m = model.(Model)
	if len(m.ports) != 1 || m.ports[0].Process != "sshd" {
		t.Errorf("live sockets replaced the snapshot's listeners: %+v", m.ports)
	}
	if len(m.conns) != 1 || m.conns[0].Process != "curl" {
		t.Errorf("live sockets replaced the snapshot's connections: %+v", m.conns)
	}

	// The same goes for anything else describing the local machine.
	model, _ = m.Update(externalIPMsg{ip: "203.0.113.7"})
	if got := model.(Model).externalIP; got != "" {
		t.Errorf("external IP of the local host leaked into a remote snapshot: %q", got)
	}
	model, _ = m.Update(hostStateMsg{
		routes:    []probe.Route{{Destination: "default", Gateway: "192.168.1.1", Iface: "wlan0"}},
		resolvers: []probe.Resolver{{Address: "192.168.1.1"}},
	})
	m = model.(Model)
	if len(m.routes) != 0 {
		t.Errorf("local routing table leaked into a remote snapshot: %+v", m.routes)
	}
	if !strings.Contains(stripANSI(m.renderRoutesText()), "snapshot") {
		t.Errorf("the routes tab should explain why it is empty for a snapshot:\n%s",
			stripANSI(m.renderRoutesText()))
	}
}

// Startup must not even ask for live data when a snapshot is being shown.
func TestSnapshotInitDoesNotSample(t *testing.T) {
	m := routesModel().WithSnapshot(Snapshot{
		Hostname:    "remote-host",
		Connections: []probe.Conn{{Proto: "tcp", Local: "10.0.0.5:1", Remote: "1.2.3.4:443"}},
	})
	if m.Init() != nil {
		t.Error("Init should issue no commands for a frozen snapshot")
	}
}

// The suppression map is only pruned by the throughput path, which returns
// early when no throughput threshold is set — so a host with only quality rules
// grows it forever.
func TestAlertSuppressionPrunedWithoutThroughputRules(t *testing.T) {
	m := eventsModel()
	m.alertRules = alertRules{rttMs: 1} // quality rules only, no throughput ones
	m.alertFired = map[string]time.Time{"rtt:stale": time.Now().Add(-10 * alertRefire)}

	m.noteQualityAlerts(nil, time.Now())

	if _, stale := m.alertFired["rtt:stale"]; stale {
		t.Error("quality alerts never prune the suppression map, so it grows forever")
	}
}

// The interface filter claims to be active on the closed-connections view but
// was never applied there, so `i` then `z` showed everything.
func TestInterfaceFilterAppliesToClosedConnections(t *testing.T) {
	m := routesModel()
	m.activeTab = tabConns
	m.lastSnap = probe.NetSnapshot{Ifaces: []probe.IfaceInfo{
		{Name: "eth0", Addrs: []string{"192.168.1.5/24"}},
		{Name: "tun0", Addrs: []string{"10.8.0.2/24"}},
	}}

	lan := conn(1, "curl", "192.168.1.5:5000", "1.1.1.1:443", 10, 10)
	vpn := conn(2, "curl", "10.8.0.2:6000", "2.2.2.2:443", 20, 20)
	m = feed(t, m, nil, []probe.Conn{lan, vpn})
	m = feed(t, m, nil, nil) // both close

	if len(m.closedConns) != 2 {
		t.Fatalf("closed %d connections, want 2", len(m.closedConns))
	}
	m.showClosed = true
	if got := len(m.closedRows()); got != 2 {
		t.Fatalf("unfiltered closed rows = %d, want 2", got)
	}

	m.ifaceFilter = "tun0"
	rows := m.closedRows()
	if len(rows) != 1 || rows[0].conn.Local != vpn.Local {
		t.Errorf("iface filter kept %d closed rows, want just the VPN one", len(rows))
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

// An outbound DNS query or a QUIC socket is a client, not a service. Counting
// them as "exposed listeners" makes the security summary and the Prometheus
// gauge fluctuate for no reason — and it is the same distinction the baseline
// and the event log already make.
func TestExposedCountsIgnoreEphemeralUDPClients(t *testing.T) {
	m := eventsModel()
	m.w, m.h = 160, 45
	service := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	udpService := probe.ListenPort{Proto: "udp", Local: "0.0.0.0:53", PID: 2, Process: "resolved"}
	udpClient := probe.ListenPort{Proto: "udp", Local: "0.0.0.0:47572", PID: 3, Process: "firefox"}
	m.ports = []probe.ListenPort{service, udpService, udpClient}

	exposed, _, _ := m.securityCounts()
	if exposed != 2 {
		t.Errorf("exposed listeners = %d, want 2 (the client socket is not a service)", exposed)
	}

	out := FormatMetrics(probe.NetSnapshot{}, probe.ConnInfo{Ports: m.ports}, 0, false)
	if !strings.Contains(out, `ducknetview_listeners{exposure="exposed"} 2`) {
		t.Errorf("metric counted client sockets as exposed listeners:\n%s", out)
	}
}

// The "N new" badge on the Ports tab should agree with the event log, which
// already ignores ephemeral UDP churn.
func TestNewListenerBadgeIgnoresEphemeralUDP(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabPorts
	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}

	m = feed(t, m, []probe.ListenPort{sshd}, nil)
	m = feed(t, m, []probe.ListenPort{sshd,
		{Proto: "udp", Local: "0.0.0.0:47572", PID: 3, Process: "firefox"}}, nil)

	if len(m.newPorts) != 0 {
		t.Errorf("new-listener set = %v, want the ephemeral UDP socket ignored", m.newPorts)
	}
	if strings.Contains(m.renderPortsText(), "new") {
		t.Errorf("ports subtitle claims new listeners:\n%s", m.renderPortsText())
	}

	// A real new service still registers.
	m = feed(t, m, []probe.ListenPort{sshd, {Proto: "tcp", Local: "0.0.0.0:8080", PID: 4, Process: "app"}}, nil)
	if len(m.newPorts) != 1 {
		t.Errorf("a new TCP listener should still be flagged, got %v", m.newPorts)
	}
}

// Browsing someone else's snapshot must not rewrite this user's saved
// preferences: the mode force-sets some of them itself.
func TestSnapshotModeDoesNotPersistSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	live := NewModel()
	live.activeTab = tabPorts
	live.saveConfig()

	snap := routesModel().WithSnapshot(Snapshot{
		Hostname:    "remote-host",
		Connections: []probe.Conn{{Proto: "tcp", Local: "10.0.0.5:1", Remote: "1.2.3.4:443", RxBytes: 10}},
	})
	snap.activeTab = tabConns
	snap.quit()

	reloaded := NewModel().WithConfig()
	if reloaded.showTotals {
		t.Error("snapshot mode persisted its forced totals view into the user's config")
	}
	if reloaded.activeTab != tabPorts {
		t.Errorf("last tab = %v, want the live session's %v", reloaded.activeTab, tabPorts)
	}
}
