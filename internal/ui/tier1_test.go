package ui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

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

// The daemon's only way out is this message, and it has to take the same exit
// path as `q` — otherwise the day's usage totals are lost on every restart.
func TestDaemonShutdownQuits(t *testing.T) {
	m := NewModel()
	_, cmd := m.Update(DaemonShutdownMsg{})
	if cmd == nil {
		t.Fatal("DaemonShutdownMsg produced no command, so the daemon would never exit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("DaemonShutdownMsg did not quit, got %T", cmd())
	}
}

// --- live remote ---------------------------------------------------------

func TestIsRemoteTarget(t *testing.T) {
	cases := map[string]bool{
		"ssh://host":       true,
		"ssh://user@host":  true,
		"snapshot.json":    false,
		"-":                false,
		"/tmp/ssh://weird": false,
		"sshx://host":      false,
	}
	for in, want := range cases {
		if got := IsRemoteTarget(in); got != want {
			t.Errorf("IsRemoteTarget(%q) = %v, want %v", in, got, want)
		}
	}
}

// The remote command is built from a fixed template: nothing from the target
// becomes an argument of its own, so a host name cannot smuggle in options.
func TestRemoteArgv(t *testing.T) {
	argv, host, err := remoteArgv("ssh://ops@10.0.0.9")
	if err != nil {
		t.Fatalf("remoteArgv: %v", err)
	}
	if host != "ops@10.0.0.9" {
		t.Errorf("host = %q", host)
	}
	want := []string{"ssh", "-o", "BatchMode=yes", "ops@10.0.0.9", "ducknetview", "--json"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv = %v, want %v", argv, want)
		}
	}
}

func TestRemoteArgvRejectsBadTargets(t *testing.T) {
	for _, target := range []string{"ssh://", "ssh://host with spaces", "ssh://-oProxyCommand=evil"} {
		if _, _, err := remoteArgv(target); err == nil {
			t.Errorf("remoteArgv(%q) accepted a target it should refuse", target)
		}
	}
}

// A remote session must keep every local collector off — it is still not this
// host's data — while staying unpaused, because there is always another sample.
func TestWithRemoteDoesNotSampleLocally(t *testing.T) {
	m, err := NewModel().WithRemote("ssh://faraway")
	if err != nil {
		t.Fatalf("WithRemote: %v", err)
	}
	if !m.snapshotOnly {
		t.Error("a remote session must keep snapshotOnly set, or local data leaks in")
	}
	if m.paused {
		t.Error("a remote session must not start paused")
	}
	if !m.remoteLive() {
		t.Error("remoteLive = false after WithRemote")
	}

	// The guards added in the 0.3.0 review must still hold: a stray local
	// message cannot overwrite the remote host's data.
	m.lastSnap = probe.NetSnapshot{Hostname: "faraway"}
	m2, _ := m.Update(snapMsg(probe.NetSnapshot{Hostname: "localhost"}))
	if got := m2.(Model).lastSnap.Hostname; got != "faraway" {
		t.Errorf("a local sample overwrote the remote host: hostname = %q", got)
	}
	m3, _ := m.Update(connInfoMsg(probe.ConnInfo{Conns: []probe.Conn{{Local: "127.0.0.1:1"}}}))
	if len(m3.(Model).conns) != 0 {
		t.Error("a local socket enumeration leaked into a remote session")
	}
	m4, _ := m.Update(externalIPMsg{ip: "203.0.113.9"})
	if m4.(Model).externalIP != "" {
		t.Error("this host's external IP leaked into a remote session")
	}
}

// Two consecutive remote fetches carry the same cumulative counters a local
// poll would, so the whole rate/total machinery has to work on them.
func TestRemoteFetchesProduceRates(t *testing.T) {
	m, err := NewModel().WithRemote("ssh://faraway")
	if err != nil {
		t.Fatal(err)
	}
	m.w, m.h = 160, 45

	conn := probe.Conn{Proto: "tcp", Local: "10.0.0.2:22", Remote: "10.0.0.9:51000", State: "ESTABLISHED", PID: 7}
	first := Snapshot{
		Hostname:   "faraway",
		TakenAt:    time.Now().Add(-time.Second).Format(time.RFC3339),
		Interfaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true}},
		Routes:     []probe.Route{{Destination: "default", Gateway: "10.0.0.1", Iface: "eth0"}},
	}
	c1 := conn
	c1.RxBytes, c1.TxBytes = 1000, 2000
	first.Connections = []probe.Conn{c1}

	out, _ := m.Update(remoteSnapshotMsg{snap: first})
	m = out.(Model)
	if m.lastSnap.Hostname != "faraway" {
		t.Fatalf("remote snapshot not applied: %q", m.lastSnap.Hostname)
	}
	if !m.snapshotHostState {
		t.Error("remote host state not recorded")
	}

	// Second fetch, a second later, with larger counters.
	m.lastRateSample = time.Now().Add(-time.Second)
	c2 := conn
	c2.RxBytes, c2.TxBytes = 11000, 12000
	second := first
	second.TakenAt = time.Now().Format(time.RFC3339)
	second.Connections = []probe.Conn{c2}

	out, _ = m.Update(remoteSnapshotMsg{snap: second})
	m = out.(Model)

	if r := m.connRate(c2); r.rx <= 0 || r.tx <= 0 {
		t.Errorf("no throughput derived from two remote fetches: %+v", r)
	}
	if !m.sessionTotal.any() {
		t.Error("session totals did not accumulate from remote fetches")
	}
	if !m.hasThroughput {
		t.Error("hasThroughput not latched, so the RX/TX columns would stay hidden")
	}
}

// A failed fetch leaves the previous view in place and says so, rather than
// ending the session or blanking the tables.
func TestRemoteFetchFailureKeepsLastView(t *testing.T) {
	m, err := NewModel().WithRemote("ssh://faraway")
	if err != nil {
		t.Fatal(err)
	}
	m.w, m.h = 160, 45

	out, _ := m.Update(remoteSnapshotMsg{snap: Snapshot{
		Hostname:    "faraway",
		TakenAt:     time.Now().Format(time.RFC3339),
		Interfaces:  []probe.IfaceInfo{{Name: "eth0", IsUp: true}},
		Connections: []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:22", Remote: "10.0.0.9:51000"}},
	}})
	m = out.(Model)

	out, _ = m.Update(remoteSnapshotMsg{err: errString("Permission denied (publickey)")})
	m = out.(Model)

	if len(m.conns) != 1 {
		t.Error("a failed fetch discarded the previous view")
	}
	if m.remoteErr == nil {
		t.Error("remoteErr not set after a failed fetch")
	}
	if !strings.Contains(m.status, "Permission denied") {
		t.Errorf("status = %q, want the ssh diagnostic", m.status)
	}
	if !strings.Contains(stripANSI(m.renderHeader()), "stale") {
		t.Error("the header does not mark a remote session as stale after a failure")
	}
}

// A slow ssh must not have a second one started behind it, or a wedged link
// turns into a pile of processes.
func TestRemoteDoesNotOverlapFetches(t *testing.T) {
	m, err := NewModel().WithRemote("ssh://faraway")
	if err != nil {
		t.Fatal(err)
	}

	out, _ := m.Update(tickMsg(time.Now()))
	m = out.(Model)
	if !m.remoteBusy {
		t.Fatal("the first tick did not start a fetch")
	}

	// While busy, a tick must re-arm the timer but start nothing.
	out, cmd := m.Update(tickMsg(time.Now()))
	m = out.(Model)
	if cmd == nil {
		t.Fatal("the ticker was not re-armed")
	}
	if !m.remoteBusy {
		t.Error("remoteBusy cleared by a tick rather than by a result")
	}

	out, _ = m.Update(remoteSnapshotMsg{err: errString("timeout")})
	if out.(Model).remoteBusy {
		t.Error("remoteBusy not cleared when the fetch came back")
	}
}

// The header has to say whose numbers are on screen.
func TestRemoteHeaderNamesTheHost(t *testing.T) {
	m, err := NewModel().WithRemote("ssh://ops@10.0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	m.w, m.h = 160, 45
	if got := stripANSI(m.renderHeader()); !strings.Contains(got, "ops@10.0.0.9") {
		t.Errorf("header = %q, want the remote host named", got)
	}
}

// fetchRemoteCmd runs an argv and parses its stdout; pointing it at `cat` is
// the whole round trip without needing a second machine.
func TestFetchRemoteCmdParsesOutput(t *testing.T) {
	snap := Snapshot{
		Hostname:   "faraway",
		TakenAt:    time.Now().Format(time.RFC3339),
		Interfaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true}},
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "snap.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}

	msg := fetchRemoteCmd([]string{"cat", p})().(remoteSnapshotMsg)
	if msg.err != nil {
		t.Fatalf("fetch failed: %v", msg.err)
	}
	if msg.snap.Hostname != "faraway" {
		t.Errorf("hostname = %q", msg.snap.Hostname)
	}

	// A command that fails must surface its diagnostic, not just its status.
	bad := fetchRemoteCmd([]string{"sh", "-c", "echo 'ducknetview: command not found' >&2; exit 127"})().(remoteSnapshotMsg)
	if bad.err == nil || !strings.Contains(bad.err.Error(), "command not found") {
		t.Errorf("err = %v, want the command's own diagnostic", bad.err)
	}

	// Output that is not a snapshot must be reported as such.
	garbage := fetchRemoteCmd([]string{"sh", "-c", "echo not json"})().(remoteSnapshotMsg)
	if garbage.err == nil {
		t.Error("non-snapshot output was accepted")
	}
}

// --- snapshot diff -------------------------------------------------------

func diffFixture() (Snapshot, Snapshot) {
	from := Snapshot{
		Hostname: "web1", TakenAt: "2026-10-10T09:00:00Z",
		Ports: []probe.ListenPort{
			{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"},
			{Proto: "tcp", Local: "127.0.0.1:5432", PID: 2, Process: "postgres"},
		},
		Processes:   []probe.ProcNet{{PID: 1, Name: "sshd"}, {PID: 2, Name: "postgres"}},
		Connections: []probe.Conn{{Proto: "tcp", Remote: "10.0.0.9:443"}},
		Interfaces:  []probe.IfaceInfo{{Name: "eth0", IsUp: true}, {Name: "wg0", IsUp: true}},
	}
	to := Snapshot{
		Hostname: "web1", TakenAt: "2026-10-11T09:00:00Z",
		Ports: []probe.ListenPort{
			{Proto: "tcp", Local: "0.0.0.0:22", PID: 9, Process: "sshd"},
			{Proto: "tcp", Local: "0.0.0.0:5432", PID: 8, Process: "postgres"},
			{Proto: "tcp", Local: "0.0.0.0:8080", PID: 7, Process: "nginx"},
		},
		Processes:   []probe.ProcNet{{PID: 9, Name: "sshd"}, {PID: 8, Name: "postgres"}, {PID: 7, Name: "nginx"}},
		Connections: []probe.Conn{{Proto: "tcp", Remote: "203.0.113.5:443"}},
		Interfaces:  []probe.IfaceInfo{{Name: "eth0", IsUp: false}},
	}
	return from, to
}

func TestDiffSnapshots(t *testing.T) {
	d := DiffSnapshots(diffFixture())

	if !d.Changed() {
		t.Fatal("Changed = false for two clearly different snapshots")
	}
	// A PID change is a restart and must not register as a change at all.
	if len(d.ProcsAdded) != 1 || d.ProcsAdded[0] != "nginx" {
		t.Errorf("ProcsAdded = %v, want just nginx (a restart is not a change)", d.ProcsAdded)
	}
	if len(d.ProcsRemoved) != 0 {
		t.Errorf("ProcsRemoved = %v, want none", d.ProcsRemoved)
	}

	// A service moving from loopback to the world is the case worth catching:
	// one removal and one addition, not an unchanged row.
	added, removed := strings.Join(d.ListenersAdded, "\n"), strings.Join(d.ListenersRemoved, "\n")
	if !strings.Contains(added, "0.0.0.0:5432") || !strings.Contains(removed, "127.0.0.1:5432") {
		t.Errorf("a listener that widened its exposure was not reported:\n+%s\n-%s", added, removed)
	}
	if !strings.Contains(added, "0.0.0.0:8080") {
		t.Errorf("new listener missing from %q", added)
	}
	// sshd did not move, so it must appear in neither list.
	if strings.Contains(added, ":22") || strings.Contains(removed, ":22") {
		t.Error("an unchanged listener was reported as a difference")
	}

	if len(d.HostsAdded) != 1 || d.HostsAdded[0] != "203.0.113.5" {
		t.Errorf("HostsAdded = %v", d.HostsAdded)
	}
	if len(d.HostsRemoved) != 1 || d.HostsRemoved[0] != "10.0.0.9" {
		t.Errorf("HostsRemoved = %v", d.HostsRemoved)
	}
	if len(d.IfacesRemoved) != 1 || d.IfacesRemoved[0] != "wg0" {
		t.Errorf("IfacesRemoved = %v", d.IfacesRemoved)
	}
	if len(d.IfacesChanged) != 1 || !strings.Contains(d.IfacesChanged[0], "eth0") {
		t.Errorf("IfacesChanged = %v", d.IfacesChanged)
	}
}

func TestDiffIdenticalSnapshots(t *testing.T) {
	from, _ := diffFixture()
	d := DiffSnapshots(from, from)
	if d.Changed() {
		t.Errorf("Changed = true comparing a snapshot with itself: %+v", d)
	}
	if !strings.Contains(FormatDiff(d), "no differences") {
		t.Error("FormatDiff does not say so when nothing changed")
	}
}

// Comparing two different machines is almost always a mistake, and silently
// rendering it would be worse than saying so.
func TestDiffWarnsOnDifferentHosts(t *testing.T) {
	from, to := diffFixture()
	to.Hostname = "web2"
	d := DiffSnapshots(from, to)
	if !d.DifferentHosts {
		t.Fatal("DifferentHosts = false for snapshots of two machines")
	}
	if !strings.Contains(FormatDiff(d), "different hosts") {
		t.Error("FormatDiff does not warn about comparing two machines")
	}
}

func TestFormatDiffRendersEverySection(t *testing.T) {
	out := FormatDiff(DiffSnapshots(diffFixture()))
	for _, want := range []string{"listeners", "processes with sockets", "remote hosts", "interfaces",
		"+ ", "- ", "~ ", "web1", "2026-10-10 09:00:00"} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatDiff output missing %q:\n%s", want, out)
		}
	}
}

// Ephemeral UDP client sockets churn with ordinary traffic; counting them as
// listeners would make every diff noise.
func TestDiffIgnoresEphemeralSockets(t *testing.T) {
	from := Snapshot{Hostname: "h", Interfaces: []probe.IfaceInfo{{Name: "eth0"}}}
	to := Snapshot{
		Hostname:   "h",
		Interfaces: []probe.IfaceInfo{{Name: "eth0"}},
		Ports: []probe.ListenPort{
			{Proto: "udp", Local: "10.0.0.2:51837", PID: 3, Process: "chrome"},
		},
	}
	if d := DiffSnapshots(from, to); len(d.ListenersAdded) != 0 {
		t.Errorf("an ephemeral UDP socket was reported as a new listener: %v", d.ListenersAdded)
	}
}

// errString is a minimal error so tests need not import errors for one line.
type errString string

func (e errString) Error() string { return string(e) }
