package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// A live remote session is snapshotOnly but polls, so it can be paused.
func TestRemoteSessionCanPause(t *testing.T) {
	m := routesModel()
	m.snapshotOnly = true
	m.remoteArgv = []string{"ssh", "host", "ducknetview", "--json"}

	next, _ := m.Update(keyMsg(" "))
	if !next.(Model).paused {
		t.Fatal("space did not pause a live remote session")
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

// errString is a minimal error so tests need not import errors for one line.
type errString string

func (e errString) Error() string { return string(e) }

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
