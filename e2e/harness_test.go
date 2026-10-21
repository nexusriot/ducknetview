//go:build e2e

// Package e2e drives the real binary against a real host. It is meant to run
// inside the container built by e2e/Dockerfile, which is the host under test:
// its network namespace is its own, so the interfaces, routes, listeners and
// connections are whatever this suite puts there. Nothing is mocked — the
// netlink parsing, the /proc readers and every non-interactive mode are
// exercised as shipped, which is the half of the program the unit tests
// cannot reach.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/ui"
)

// binPath is the binary under test. The image puts it on PATH and names it in
// the environment; outside the image the suite skips rather than testing
// whatever happens to be installed.
var binPath string

func TestMain(m *testing.M) {
	// The namespace test re-executes this binary as the resident of a fresh
	// network namespace (see netns_test.go); that copy runs no tests.
	if os.Getenv(netnsChildEnv) != "" {
		os.Exit(netnsChild())
	}
	binPath = os.Getenv("DUCKNETVIEW_BIN")
	if binPath == "" {
		fmt.Fprintln(os.Stderr,
			"e2e: DUCKNETVIEW_BIN is not set — run this suite with e2e/run.sh, which builds the container it belongs in")
		os.Exit(0)
	}
	if _, err := os.Stat(binPath); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %s: %v\n", binPath, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// result is one invocation of the binary.
type result struct {
	stdout, stderr string
	code           int
}

func (r result) out() string { return r.stdout + r.stderr }

// run invokes the binary with its own config directory, so no test can see
// another's saved state. Long modes (--top, --metrics) take a couple of
// seconds, so the timeout is generous rather than tight.
func run(t *testing.T, cfgDir string, args ...string) result {
	t.Helper()
	return runEnv(t, cfgDir, nil, args...)
}

func runEnv(t *testing.T, cfgDir string, extraEnv []string, args ...string) result {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Env = append(append(os.Environ(), "XDG_CONFIG_HOME="+cfgDir), extraEnv...)

	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("%s %v: %v\n%s", binPath, args, err, stderr.String())
	}
	if ctx.Err() != nil {
		t.Fatalf("%s %v timed out", binPath, args)
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// configDir is a private config directory for one test.
func configDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ducknetview"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeConfig puts a config file in a test's own config directory.
func writeConfig(t *testing.T, cfgDir, body string) {
	t.Helper()
	p := filepath.Join(cfgDir, "ducknetview", "config.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// snapshot runs --json and decodes it into the viewer's own type, which is
// what --from reads: if this stops decoding, --from has stopped working.
func snapshot(t *testing.T, cfgDir string) ui.Snapshot {
	t.Helper()
	r := run(t, cfgDir, "--json")
	if r.code != 0 {
		t.Fatalf("--json exited %d: %s", r.code, r.stderr)
	}
	var s ui.Snapshot
	if err := json.Unmarshal([]byte(r.stdout), &s); err != nil {
		t.Fatalf("--json did not produce a decodable snapshot: %v\n%s", err, tail(r.stdout, 500))
	}
	return s
}

// fixture is a known set of sockets held open for the duration of a test: two
// TCP listeners on different exposures, a UDP socket, and a connected pair
// that has actually moved bytes. Every assertion about what the tool sees is
// against these.
type fixture struct {
	exposedPort  int // bound to 0.0.0.0, so "exposed"
	loopbackPort int // bound to 127.0.0.1, so "local"
	udpPort      int

	// The client end of a live connection, and how many bytes crossed it.
	clientLocal string
	transferred int

	closers []io.Closer
	wg      sync.WaitGroup
}

func (f *fixture) close() {
	for _, c := range f.closers {
		_ = c.Close()
	}
	f.wg.Wait()
}

// newFixture opens the sockets and pushes data across one of them. The data
// matters: the per-socket byte counters come from the kernel's tcp_info, and a
// connection that has sent nothing proves nothing about reading them.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	t.Cleanup(f.close)

	// tcp4, not tcp: Go's dual-stack default binds the IPv6 wildcard and the
	// socket then reads as ":::port", which is a different assertion than the
	// one this fixture is making about an IPv4 wildcard bind.
	exposed, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	f.closers = append(f.closers, exposed)
	f.exposedPort = exposed.Addr().(*net.TCPAddr).Port

	loopback, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.closers = append(f.closers, loopback)
	f.loopbackPort = loopback.Addr().(*net.TCPAddr).Port

	udp, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	f.closers = append(f.closers, udp)
	f.udpPort = udp.LocalAddr().(*net.UDPAddr).Port

	// Echo whatever arrives on the exposed listener, so the client's writes
	// are acknowledged and tcpi_bytes_acked actually advances.
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			c, err := exposed.Accept()
			if err != nil {
				return
			}
			f.wg.Add(1)
			go func(c net.Conn) {
				defer f.wg.Done()
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", f.exposedPort))
	if err != nil {
		t.Fatal(err)
	}
	f.closers = append(f.closers, client)
	f.clientLocal = client.LocalAddr().String()

	payload := strings.Repeat("ducknetview", 8192) // ~90 kB, well clear of any rounding
	if n, err := client.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	} else {
		f.transferred = n
	}
	// Read the echo back so both directions have moved data before anything
	// looks at the counters.
	buf := make([]byte, len(payload))
	_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatalf("echo did not come back: %v", err)
	}
	_ = client.SetReadDeadline(time.Time{})

	return f
}

// freePort reserves a port by binding and releasing it, for the daemon's
// metrics listener.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// waitFile polls until a file contains want, which is how the asynchronous
// parts (the event log, an alert hook) are observed without sleeping blindly.
func waitFile(t *testing.T, path, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			last = string(b)
			if want == "" || strings.Contains(last, want) {
				return last
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in %s; contents:\n%s", want, path, tail(last, 2000))
	return ""
}
