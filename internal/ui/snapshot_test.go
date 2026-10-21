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

// A command line can carry a password, and a snapshot gets piped around.
func TestCommandLineIsNotSerialised(t *testing.T) {
	m := NewModel()
	m.procs = []probe.ProcNet{{PID: 1, Name: "mysql", Cmdline: "mysql -psecret"}}
	snap := Snapshot{Hostname: "h", Processes: m.procs}

	b, err := marshalSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") {
		t.Errorf("the command line reached the snapshot:\n%s", b)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")

	want := Snapshot{
		Hostname:      "remote-host",
		UptimeSeconds: 3600,
		TakenAt:       time.Now().Format(time.RFC3339),
		Interfaces: []probe.IfaceInfo{
			{Name: "eth0", IsUp: true, Kind: probe.IfacePhysical, Addrs: []string{"10.0.0.5/24"}},
		},
		Ports:     []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}},
		Processes: []probe.ProcNet{{PID: 1, Name: "sshd", ConnCount: 1}},
		Connections: []probe.Conn{
			{Proto: "tcp", Local: "10.0.0.5:5000", Remote: "1.2.3.4:443", State: "ESTABLISHED",
				PID: 2, Process: "curl", RxBytes: 4096, TxBytes: 1024},
		},
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSnapshot(path)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if got.Hostname != want.Hostname || len(got.Connections) != 1 {
		t.Fatalf("loaded %+v", got)
	}
	// The interface kind survives as a label, not an opaque integer.
	if got.Interfaces[0].Kind != probe.IfacePhysical {
		t.Errorf("interface kind = %v, want physical", got.Interfaces[0].Kind)
	}

	m := routesModel().WithSnapshot(got)
	if !m.snapshotOnly || !m.paused {
		t.Error("a snapshot must be frozen: there is nothing to refresh")
	}
	if m.lastSnap.Hostname != "remote-host" || len(m.conns) != 1 {
		t.Error("snapshot data did not reach the model")
	}
	if !m.hasThroughput || !m.showTotals {
		t.Error("a snapshot with byte counters should show them as totals")
	}
	// Interface attribution works offline, from the snapshot's own addresses.
	if got := m.ifaceFor(m.conns[0]); got != "eth0" {
		t.Errorf("connection attributed to %q, want eth0", got)
	}
	if !strings.Contains(m.View(), "SNAPSHOT") {
		t.Error("the header should say the view is frozen")
	}

	// Ticks must not replace the file's contents with the local host's.
	model, _ := m.Update(tickMsg(time.Now()))
	if h := model.(Model).lastSnap.Hostname; h != "remote-host" {
		t.Errorf("a tick overwrote the snapshot with %q", h)
	}
	// And resuming is refused rather than silently doing nothing.
	model, _ = m.Update(key(" "))
	if !strings.Contains(model.(Model).status, "nothing to resume") {
		t.Errorf("status = %q", model.(Model).status)
	}
}

func TestLoadSnapshotRejectsRubbish(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(bad); err == nil {
		t.Error("a non-JSON file should be rejected")
	}

	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(empty); err == nil {
		t.Error("an empty snapshot should be rejected rather than shown as a blank host")
	}
	if _, err := LoadSnapshot(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("a missing file should be an error")
	}
}

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
