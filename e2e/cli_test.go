//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Everything the tool reports about the live host comes from /proc and
// netlink, and none of it can be exercised without a host whose sockets the
// test controls. This is that test: the fixture opens known sockets, and the
// snapshot has to contain them.
func TestSnapshotSeesTheHostsSockets(t *testing.T) {
	f := newFixture(t)
	cfg := configDir(t)

	snap := snapshot(t, cfg)

	if snap.Hostname == "" {
		t.Error("the snapshot has no hostname")
	}
	if snap.Version == "" {
		t.Error("the snapshot does not say which version wrote it")
	}
	if snap.UptimeSeconds <= 0 {
		t.Errorf("uptime = %d", snap.UptimeSeconds)
	}

	// Interfaces: the container always has loopback, and the default bridge
	// gives it one more.
	byName := map[string]probe.IfaceInfo{}
	for _, ii := range snap.Interfaces {
		byName[ii.Name] = ii
	}
	lo, ok := byName["lo"]
	if !ok {
		t.Fatalf("no loopback interface in %v", ifaceNames(snap.Interfaces))
	}
	if !lo.IsUp {
		t.Error("loopback is reported down")
	}
	if lo.Kind != probe.IfaceLoopback {
		t.Errorf("loopback classified as %v", lo.Kind.Label())
	}
	if len(snap.Interfaces) < 2 {
		t.Errorf("only the loopback was found: %v", ifaceNames(snap.Interfaces))
	}

	// The fixture's listeners, with the exposure the bind address implies.
	exposed := findListener(snap, "tcp", f.exposedPort)
	if exposed == nil {
		t.Fatalf("the 0.0.0.0:%d listener is missing from %d ports", f.exposedPort, len(snap.Ports))
	}
	if got := probe.ListenerExposure(hostOf(exposed.Local)); got != "exposed" {
		t.Errorf("0.0.0.0:%d reads as %q, want exposed", f.exposedPort, got)
	}
	if exposed.PID <= 0 || !strings.Contains(exposed.Process, "test") {
		t.Errorf("the listener was not attributed to this test: pid %d %q", exposed.PID, exposed.Process)
	}
	if exposed.User == "" {
		t.Error("the listener has no owning user")
	}

	loopbackListener := findListener(snap, "tcp", f.loopbackPort)
	if loopbackListener == nil {
		t.Fatalf("the 127.0.0.1:%d listener is missing", f.loopbackPort)
	}
	if got := probe.ListenerExposure(hostOf(loopbackListener.Local)); got != "local" {
		t.Errorf("127.0.0.1:%d reads as %q, want local", f.loopbackPort, got)
	}

	if findListener(snap, "udp", f.udpPort) == nil {
		t.Errorf("the UDP socket on :%d is missing", f.udpPort)
	}

	// The live connection, both ends of it.
	var client *probe.Conn
	for i := range snap.Connections {
		if snap.Connections[i].Local == f.clientLocal {
			client = &snap.Connections[i]
			break
		}
	}
	if client == nil {
		t.Fatalf("the connection from %s is missing from %d connections", f.clientLocal, len(snap.Connections))
	}
	if client.State != "ESTABLISHED" {
		t.Errorf("connection state = %q", client.State)
	}
	if client.PID <= 0 {
		t.Error("the connection was not attributed to a process")
	}

	// The per-socket byte counters are the netlink half of the tool, and the
	// one thing no unit test can prove works against a real kernel.
	if client.TxBytes < uint64(f.transferred) {
		t.Errorf("tcpi_bytes_acked = %d, want at least the %d bytes written",
			client.TxBytes, f.transferred)
	}
	if client.RxBytes < uint64(f.transferred) {
		t.Errorf("tcpi_bytes_received = %d, want at least the %d bytes echoed back",
			client.RxBytes, f.transferred)
	}
	if client.RTT <= 0 {
		t.Error("tcpi_rtt came back as zero on an established connection")
	}

	// Processes: this test holds the sockets, so it has to be in the list.
	found := false
	for _, p := range snap.Processes {
		if p.PID == int32(os.Getpid()) {
			found = true
			if p.ConnCount <= 0 {
				t.Errorf("this process is listed with %d connections", p.ConnCount)
			}
			if p.ListenCount < 2 {
				t.Errorf("this process holds 3 listening sockets, reported %d", p.ListenCount)
			}
		}
	}
	if !found {
		t.Errorf("this test process (%d) is not in the process list", os.Getpid())
	}

	// Host configuration, read from /proc and netlink.
	if len(snap.Routes) == 0 {
		t.Error("no routes were read")
	}
	if len(snap.Resolvers) == 0 {
		t.Error("no nameservers were read from /etc/resolv.conf")
	}
}

// A command line can carry a password, and a snapshot is made to be piped
// somewhere else. The unit test asserts the struct tag; this asserts the bytes
// that actually leave a real process.
func TestSnapshotNeverCarriesCommandLines(t *testing.T) {
	newFixture(t)
	cfg := configDir(t)

	r := run(t, cfg, "--json")
	if r.code != 0 {
		t.Fatalf("--json exited %d", r.code)
	}
	var raw struct {
		Processes []map[string]any `json:"processes"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Processes) == 0 {
		t.Fatal("no processes in the snapshot")
	}
	for _, p := range raw.Processes {
		for key := range p {
			if strings.Contains(strings.ToLower(key), "cmd") {
				t.Errorf("a process carries %q into the snapshot: %v", key, p)
			}
		}
	}
	if strings.Contains(r.stdout, "-test.timeout") {
		t.Error("this test binary's own arguments reached the snapshot")
	}
}

// --once is the human-readable half of the same collection.
func TestOnceListsTheFixture(t *testing.T) {
	f := newFixture(t)
	r := run(t, configDir(t), "--once")

	if r.code != 0 {
		t.Fatalf("--once exited %d: %s", r.code, r.stderr)
	}
	for _, want := range []string{
		"interfaces (",
		"listening ports (",
		"connections (",
		fmt.Sprintf(":%d", f.exposedPort),
		fmt.Sprintf(":%d", f.loopbackPort),
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("--once output does not mention %q", want)
		}
	}
}

// The metrics are an interface other software is written against, so the
// exposition itself is what gets checked, against a real host's numbers.
func TestMetricsExposition(t *testing.T) {
	f := newFixture(t)
	r := run(t, configDir(t), "--metrics")

	if r.code != 0 {
		t.Fatalf("--metrics exited %d: %s", r.code, r.stderr)
	}
	for _, want := range []string{
		"ducknetview_iface_receive_bytes_per_second{iface=\"lo\"",
		"ducknetview_iface_up{iface=\"lo\"} 1",
		"ducknetview_listeners{exposure=\"exposed\"}",
		"ducknetview_connections{scope=",
		"ducknetview_processes_with_sockets ",
		"ducknetview_listeners_off_baseline -1", // no baseline saved: unknown, not clean
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the scrape does not contain %q", want)
		}
	}
	// Every series is declared before it is used, and nothing is NaN.
	assertValidExposition(t, r.stdout)

	if n := metricValue(t, r.stdout, "ducknetview_listeners{exposure=\"exposed\"}"); n < 1 {
		t.Errorf("the fixture's exposed listener is not counted: %v", n)
	}
	_ = f
}

// --top derives rates from two enumerations a second apart, which is a
// different path from everything above.
func TestTopReportsTheBusyProcess(t *testing.T) {
	f := newFixture(t)

	// Keep the connection busy while --top takes its two samples.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		c, err := netDial(f.exposedPort)
		if err != nil {
			return
		}
		defer c.Close()
		payload := make([]byte, 64<<10)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := c.Write(payload); err != nil {
				return
			}
			_, _ = c.Read(payload)
		}
	}()

	r := run(t, configDir(t), "--top", "5")
	if r.code != 0 {
		t.Fatalf("--top exited %d: %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "top processes") || !strings.Contains(r.stdout, "top hosts") {
		t.Fatalf("unexpected --top output:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "(nothing moved during the sample)") {
		t.Errorf("a connection moving 64 kB a cycle registered as idle:\n%s", r.stdout)
	}
}

// The baseline is the scriptable half of the Ports tab, and its exit codes are
// what a cron job acts on.
func TestBaselineLifecycle(t *testing.T) {
	f := newFixture(t)
	cfg := configDir(t)
	file := filepath.Join(t.TempDir(), "baseline.json")

	// No baseline saved at all is "unknown", which is exit 2 rather than clean.
	if r := run(t, cfg, "--check-baseline"); r.code != 2 {
		t.Errorf("--check-baseline with no baseline exited %d, want 2:\n%s", r.code, r.out())
	}

	r := run(t, cfg, "--save-baseline", file)
	if r.code != 0 {
		t.Fatalf("--save-baseline exited %d: %s", r.code, r.out())
	}
	if !strings.Contains(r.stdout, "wrote ") {
		t.Errorf("unexpected --save-baseline output: %q", r.stdout)
	}

	// The host has not changed, so it is clean.
	if r := run(t, cfg, "--check-baseline", "--baseline", file); r.code != 0 {
		t.Fatalf("a freshly saved baseline is not clean: exit %d\n%s", r.code, r.out())
	}

	// A new listener is exactly what the baseline exists to catch.
	extra := listen(t, "0.0.0.0:0")
	extraPort := portOf(extra)

	r = run(t, cfg, "--check-baseline", "--baseline", file)
	if r.code != 1 {
		t.Fatalf("a new listener did not fail the check: exit %d\n%s", r.code, r.out())
	}
	if !strings.Contains(r.stdout, fmt.Sprintf(":%d", extraPort)) {
		t.Errorf("the check does not name the new listener :%d:\n%s", extraPort, r.stdout)
	}
	_ = f
}

// --diff is how two snapshots are compared, and it exits like diff(1).
func TestDiffBetweenTwoSnapshots(t *testing.T) {
	f := newFixture(t)
	cfg := configDir(t)
	dir := t.TempDir()

	before := filepath.Join(dir, "before.json")
	writeFile(t, before, run(t, cfg, "--json").stdout)

	// Identical input is no difference at all.
	if r := run(t, cfg, "--diff", before, before); r.code != 0 {
		t.Errorf("a snapshot differs from itself: exit %d\n%s", r.code, r.out())
	}

	extra := listen(t, "0.0.0.0:0")
	extraPort := portOf(extra)

	after := filepath.Join(dir, "after.json")
	writeFile(t, after, run(t, cfg, "--json").stdout)

	r := run(t, cfg, "--diff", before, after)
	if r.code != 1 {
		t.Fatalf("a new listener is not reported as a difference: exit %d\n%s", r.code, r.out())
	}
	if !strings.Contains(r.stdout, fmt.Sprintf(":%d", extraPort)) {
		t.Errorf("the diff does not mention the new listener :%d:\n%s", extraPort, r.stdout)
	}
	_ = f
}

// Reading a snapshot back is what --from does; a snapshot that cannot be
// decoded has to say so rather than opening an empty viewer.
func TestFromRejectsRubbish(t *testing.T) {
	cfg := configDir(t)
	bad := filepath.Join(t.TempDir(), "bad.json")
	writeFile(t, bad, "this is not a snapshot\n")

	r := run(t, cfg, "--from", bad)
	if r.code == 0 {
		t.Errorf("a malformed snapshot was accepted:\n%s", r.out())
	}
	if !strings.Contains(r.out(), "snapshot") {
		t.Errorf("the error does not explain itself: %q", r.out())
	}
}

// --check-config is the command that breaks the loader's deliberate silence.
func TestCheckConfig(t *testing.T) {
	cfg := configDir(t)

	// No file is not a problem.
	if r := run(t, cfg, "--check-config"); r.code != 0 {
		t.Errorf("a missing config is reported as a problem: exit %d\n%s", r.code, r.out())
	}

	writeConfig(t, cfg, `{
	  "theme": "nonsense",
	  "latency_targets": ["no-port-here"],
	  "hidden_columns": ["nonexistent"],
	  "watchlist": ["*broken("],
	  "keys": {"not-an-action": "Z"},
	  "views": [{"name": ""}],
	  "refresh_ms": 123
	}`)
	r := run(t, cfg, "--check-config")
	if r.code != 1 {
		t.Fatalf("a config full of mistakes exited %d, want 1:\n%s", r.code, r.out())
	}
	for _, want := range []string{"theme", "latency_targets", "hidden_columns", "watchlist", "keys", "views", "refresh_ms"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("--check-config says nothing about %s:\n%s", want, r.stdout)
		}
	}

	// A file that is not JSON at all is a different, harder failure.
	writeConfig(t, cfg, `{"theme":`)
	if r := run(t, cfg, "--check-config"); r.code != 2 {
		t.Errorf("unparseable config exited %d, want 2:\n%s", r.code, r.out())
	}
}

// The config a session could not read must survive that session, which is only
// really provable end to end: the process has to start, run and exit.
func TestCorruptConfigSurvivesARun(t *testing.T) {
	cfg := configDir(t)
	const corrupt = `{"watchlist": ["10.0.0.0/8"], "alert_rtt_ms": 250,`
	writeConfig(t, cfg, corrupt)

	// --once goes through the same config load and save path as any other run.
	if r := run(t, cfg, "--once"); r.code != 0 {
		t.Fatalf("--once exited %d with a corrupt config: %s", r.code, r.out())
	}
	got := readFile(t, filepath.Join(cfg, "ducknetview", "config.json"))
	if got != corrupt {
		t.Errorf("the corrupt config was rewritten:\n%s", got)
	}
}

func TestUsageAndVersion(t *testing.T) {
	cfg := configDir(t)

	r := run(t, cfg, "--usage-all")
	if r.code != 0 || !strings.Contains(r.stdout, "no usage recorded yet") {
		t.Errorf("--usage-all on a fresh config: exit %d\n%s", r.code, r.out())
	}

	r = run(t, cfg, "--version")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "ducknetview ") {
		t.Errorf("--version: exit %d %q", r.code, r.stdout)
	}
}
