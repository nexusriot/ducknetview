package ui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// A snapshot has to survive the round trip through JSON intact — the whole
// point of --from is that the file describes the remote host, so anything lost
// in transit is silently replaced by nothing.
func TestSnapshotHostStateRoundTrip(t *testing.T) {
	in := Snapshot{
		Hostname:      "faraway",
		UptimeSeconds: 3600,
		TakenAt:       time.Now().Format(time.RFC3339),
		Interfaces:    []probe.IfaceInfo{{Name: "eth0", IsUp: true}},
		Connections:   []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:22", Remote: "10.0.0.9:51000", State: "ESTABLISHED"}},
		Routes: []probe.Route{
			{Destination: "default", Gateway: "10.0.0.1", Iface: "eth0", Metric: 100},
			{Destination: "default", Gateway: "fe80::1", Iface: "eth0", IPv6: true},
		},
		Neighbours: []probe.Neighbour{
			{IP: "10.0.0.1", MAC: "e0:1f:2b:51:1c:31", Iface: "eth0", State: "reachable"},
			{IP: "fe80::1", MAC: "e0:1f:2b:51:1c:31", Iface: "eth0", IPv6: true, State: "stale"},
		},
		Resolvers: []probe.Resolver{{Address: "127.0.0.53", Stub: true}},
		Wifi:      map[string]probe.WifiLink{"wlan0": {Iface: "wlan0", Signal: -55, Quality: 48}},
		Latency: []probe.LatencySample{
			{Label: "gateway", Addr: "10.0.0.1:53", RTT: 3 * time.Millisecond, At: time.Now()},
			{Label: "dead", Addr: "10.0.0.250:53", Err: errors.New("i/o timeout")},
		},
	}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p := filepath.Join(t.TempDir(), "snap.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := LoadSnapshot(p)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}

	if len(out.Routes) != 2 || out.Routes[0].Gateway != "10.0.0.1" || !out.Routes[1].IPv6 {
		t.Errorf("routes did not survive: %+v", out.Routes)
	}
	if len(out.Neighbours) != 2 || !out.Neighbours[1].IPv6 || out.Neighbours[0].State != "reachable" {
		t.Errorf("neighbours did not survive: %+v", out.Neighbours)
	}
	if len(out.Resolvers) != 1 || !out.Resolvers[0].Stub {
		t.Errorf("resolvers did not survive: %+v", out.Resolvers)
	}
	if w, ok := out.Wifi["wlan0"]; !ok || w.Signal != -55 {
		t.Errorf("wifi did not survive: %+v", out.Wifi)
	}
	if !out.HasHostState() {
		t.Error("HasHostState = false for a snapshot that carries routes")
	}

	// An error is an interface, which JSON cannot unmarshal back; a snapshot
	// carrying a failed probe must still load.
	if len(out.Latency) != 2 {
		t.Fatalf("latency did not survive: %+v", out.Latency)
	}
	if !out.Latency[0].OK() || out.Latency[0].RTT != 3*time.Millisecond {
		t.Errorf("good sample did not survive: %+v", out.Latency[0])
	}
	if out.Latency[1].OK() || out.Latency[1].Err == nil {
		t.Errorf("failed sample lost its error: %+v", out.Latency[1])
	}
}

// Snapshots written before 0.4.0 have no host state; the viewer must say so
// rather than showing this machine's routes next to another host's sockets.
func TestSnapshotWithoutHostState(t *testing.T) {
	s := Snapshot{
		Hostname:   "oldhost",
		TakenAt:    time.Now().Format(time.RFC3339),
		Interfaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true}},
	}
	if s.HasHostState() {
		t.Fatal("HasHostState = true for a snapshot with no routing data")
	}

	m := NewModel().WithSnapshot(s)
	if m.snapshotHostState {
		t.Error("model claims the snapshot carried host state")
	}
	if got := m.renderRoutesText(); !strings.Contains(got, "does not carry") {
		t.Errorf("Routes tab does not explain the gap:\n%s", got)
	}
	if got := m.gatewayLine(); !strings.Contains(got, "not carried") {
		t.Errorf("gateway line = %q, want it to say the snapshot has no routes", got)
	}
}

// With host state, the tab describes the remote host rather than apologising.
func TestSnapshotWithHostStateRendersRoutes(t *testing.T) {
	s := Snapshot{
		Hostname:   "faraway",
		TakenAt:    time.Now().Format(time.RFC3339),
		Interfaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true}},
		Routes:     []probe.Route{{Destination: "default", Gateway: "10.0.0.1", Iface: "eth0"}},
		Neighbours: []probe.Neighbour{{IP: "fe80::1", MAC: "e0:1f:2b:51:1c:31", Iface: "eth0", IPv6: true, State: "reachable"}},
		Resolvers:  []probe.Resolver{{Address: "1.1.1.1"}},
	}

	m := NewModel().WithSnapshot(s)
	if !m.snapshotHostState {
		t.Fatal("model did not record that the snapshot carried host state")
	}
	txt := m.renderRoutesText()
	for _, want := range []string{"10.0.0.1", "fe80::1", "1.1.1.1"} {
		if !strings.Contains(txt, want) {
			t.Errorf("Routes tab is missing %q:\n%s", want, txt)
		}
	}
	if !strings.Contains(m.gatewayLine(), "10.0.0.1") {
		t.Errorf("gateway line = %q, want the snapshot's gateway", m.gatewayLine())
	}
}
