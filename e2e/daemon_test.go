//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The daemon is the whole model with nothing drawing it, and it is the mode
// that runs unattended for weeks. Everything it does — polling, event
// detection, the alert rules, the hook, the exporter, the reload, the clean
// shutdown — only happens in a real process, so this is the only place any of
// it is exercised.
func TestDaemonLifecycle(t *testing.T) {
	f := newFixture(t)
	cfg := configDir(t)
	dir := t.TempDir()

	eventLog := filepath.Join(dir, "events.jsonl")
	hookLog := filepath.Join(dir, "hook.log")
	hook := filepath.Join(dir, "hook.sh")
	writeFile(t, hook, "#!/bin/sh\n"+
		"printf '%s|%s|%s|%s\\n' \"$DNV_EVENT_KIND\" \"$DNV_EVENT_LEVEL\" \"$DNV_EVENT_SUBJECT\" \"$1\" >> "+hookLog+"\n")

	// A threshold low enough that the fixture's own traffic trips it, so the
	// alert path and the hook are exercised rather than merely compiled.
	writeConfig(t, cfg, `{"refresh_ms": 1000, "alert_connection_bps": 1024}`)

	port := freePort(t)
	cmd := exec.Command(binPath,
		"--daemon",
		"--log-events", eventLog,
		"--on-alert", hook,
		"--metrics-listen", fmt.Sprintf("127.0.0.1:%d", port))
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+cfg)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	var out, errOut strings.Builder
	go io.Copy(&out, stdout)
	go io.Copy(&errOut, stderr)

	killed := false
	defer func() {
		if !killed {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	// The exporter answers once the daemon is up, and from the session's own
	// state rather than by sampling the host again.
	scrape := waitForScrape(t, port, 30*time.Second)
	for _, want := range []string{
		"ducknetview_build_info{version=",
		"ducknetview_last_poll_age_seconds",
		"ducknetview_polls_total",
		"ducknetview_session_uptime_seconds",
		"ducknetview_paused 0",
	} {
		if !strings.Contains(scrape, want) {
			t.Errorf("the live scrape has no %q:\n%s", want, tail(scrape, 1500))
		}
	}
	assertValidExposition(t, scrape)

	// The root page exists so a human who opens the address is not met with a
	// 404, and anything else is one.
	if body, code := get(t, fmt.Sprintf("http://127.0.0.1:%d/", port)); code != 200 || !strings.Contains(body, "/metrics") {
		t.Errorf("GET / = %d %q", code, body)
	}
	if _, code := get(t, fmt.Sprintf("http://127.0.0.1:%d/nope", port)); code != 404 {
		t.Errorf("GET /nope = %d, want 404", code)
	}

	// A new listener is the event the log exists for.
	extra := listen(t, "0.0.0.0:0")
	extraPort := portOf(extra)
	line := waitFile(t, eventLog, fmt.Sprintf(":%d", extraPort), 30*time.Second)
	ev := findEvent(t, line, "listener_up", fmt.Sprintf(":%d", extraPort))
	if ev.Level != "warn" && ev.Level != "alert" {
		t.Errorf("listener_up came in at level %q", ev.Level)
	}

	// A daemon's stdout is its log, or a unit file is silent about the thing
	// it exists to notice.
	if !strings.Contains(out.String(), "listener_up") {
		t.Errorf("the event was not echoed to stdout:\n%s", tail(out.String(), 1000))
	}

	// The traffic the fixture is about to move trips alert_connection_bps,
	// which runs the hook.
	busy := keepBusy(t, f.exposedPort)
	waitFile(t, eventLog, "throughput_connection", 40*time.Second)
	hookOut := waitFile(t, hookLog, "throughput_connection", 30*time.Second)
	if !strings.Contains(hookOut, "|alert|") {
		t.Errorf("the hook did not receive the level: %q", hookOut)
	}
	busy()

	// Once the traffic stops the rule has to say so, or a pager stays paged.
	// The hold-down is a minute, so this is the slow assertion in the suite.
	waitFile(t, eventLog, "throughput_connection_cleared", 2*time.Minute)

	// SIGHUP re-reads the config in place and says that it did.
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	waitFile(t, eventLog, "config_reloaded", 20*time.Second)

	// Polling survived the reload: the session was not restarted.
	after := waitForScrape(t, port, 10*time.Second)
	if metricValue(t, after, "ducknetview_polls_total") < 1 {
		t.Error("the daemon stopped polling after the reload")
	}
	if age := metricValue(t, after, "ducknetview_last_poll_age_seconds"); age < 0 || age > 30 {
		t.Errorf("last poll was %v seconds ago", age)
	}

	// SIGTERM takes the normal exit path, which is what writes the day's usage.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		killed = true
		if err != nil {
			t.Errorf("the daemon exited with %v\nstderr:\n%s", err, tail(errOut.String(), 1000))
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the daemon did not stop on SIGTERM")
	}

	// The exporter's socket goes with it, rather than being left bound.
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", port)); err == nil {
		t.Error("the metrics listener outlived the daemon")
	}

	// A session that accounted traffic records it, which is what --usage reads.
	if r := run(t, cfg, "--usage-all"); !strings.Contains(r.stdout, "total across") {
		t.Errorf("the session's usage was not recorded:\n%s", r.out())
	}
}

// A taken port has to fail at startup, where it can be reported, rather than
// leaving a daemon running with no exporter.
func TestDaemonRefusesATakenMetricsPort(t *testing.T) {
	cfg := configDir(t)
	l := listen(t, "127.0.0.1:0")

	r := run(t, cfg, "--daemon", "--metrics-listen", l.Addr().String())
	if r.code == 0 {
		t.Fatalf("a taken port was accepted:\n%s", r.out())
	}
	if !strings.Contains(r.out(), "metrics listener") {
		t.Errorf("the failure does not explain itself: %q", r.out())
	}
}

// eventLine is one line of the --log-events JSONL.
type eventLine struct {
	At      string `json:"at"`
	Level   string `json:"level"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// findEvent returns the first event of a kind whose subject contains want,
// failing with the whole log when there is none. It also checks that every
// line in the log is decodable, since the format is a public interface.
func findEvent(t *testing.T, log, kind, want string) eventLine {
	t.Helper()
	var found *eventLine
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		if line == "" {
			continue
		}
		var ev eventLine
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("the event log is not valid JSONL: %v\n%s", err, line)
		}
		if ev.At == "" || ev.Kind == "" {
			t.Errorf("an event is missing its timestamp or kind: %s", line)
		}
		if found == nil && ev.Kind == kind && strings.Contains(ev.Subject, want) {
			e := ev
			found = &e
		}
	}
	if found == nil {
		t.Fatalf("no %s event mentioning %q in:\n%s", kind, want, tail(log, 2000))
	}
	return *found
}

func waitForScrape(t *testing.T, port int, timeout time.Duration) string {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", port)
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		body, code := tryGet(url)
		if code == 200 && strings.Contains(body, "ducknetview_polls_total") {
			return body
		}
		last = body
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("the exporter never answered on %s; last body:\n%s", url, tail(last, 500))
	return ""
}

func get(t *testing.T, url string) (string, int) {
	t.Helper()
	body, code := tryGet(url)
	if code == 0 {
		t.Fatalf("GET %s failed", url)
	}
	return body, code
}

func tryGet(url string) (string, int) {
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return "", 0
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b), resp.StatusCode
}

// keepBusy pushes data over a connection until the returned function is
// called, so a throughput threshold has something to trip on.
func keepBusy(t *testing.T, port int) func() {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := netDial(port)
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64<<10)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := c.Write(buf); err != nil {
				return
			}
			if _, err := io.ReadFull(c, buf); err != nil {
				return
			}
		}
	}()
	stopped := false
	return func() {
		if stopped {
			return
		}
		stopped = true
		close(stop)
		<-done
	}
}
