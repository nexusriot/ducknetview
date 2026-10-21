//go:build e2e

package e2e

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// A container is a process in a network namespace of its own, and that is all
// this test builds: the test binary re-executes itself into a fresh namespace
// (CLONE_NEWNET, no runtime involved), listens there, moves data over its own
// loopback, and the snapshot taken from outside has to contain those sockets.
// Creating a namespace needs CAP_SYS_ADMIN and configuring its loopback
// CAP_NET_ADMIN, which run.sh grants the container; without them the test
// skips rather than failing on an environment question.

// netnsChildEnv marks the re-executed test binary as the namespace's resident.
const netnsChildEnv = "DUCKNETVIEW_E2E_NETNS_CHILD"

// netnsChild runs inside the new namespace: loopback up, a wildcard listener
// that echoes, a client that has pushed data through it, then the ports on
// stdout and a wait for stdin to close.
func netnsChild() int {
	if err := loopbackUp(); err != nil {
		if errors.Is(err, unix.EPERM) {
			fmt.Println("skip: bringing loopback up needs CAP_NET_ADMIN")
			return 0
		}
		fmt.Fprintln(os.Stderr, "lo up:", err)
		return 1
	}
	l, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		return 1
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { _, _ = io.Copy(c, c) }(c)
		}
	}()

	port := l.Addr().(*net.TCPAddr).Port
	c, err := net.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		return 1
	}
	payload := []byte(strings.Repeat("netns", 16384)) // 80 kB
	if _, err := c.Write(payload); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		return 1
	}
	if _, err := io.ReadFull(c, make([]byte, len(payload))); err != nil {
		fmt.Fprintln(os.Stderr, "echo:", err)
		return 1
	}

	fmt.Printf("ready %d %s\n", port, c.LocalAddr())
	_, _ = io.Copy(io.Discard, os.Stdin) // until the test is done with us
	return 0
}

// loopbackUp brings lo up in a fresh namespace, where it starts down; the
// image has no iproute2, and two ioctls are all `ip link set lo up` does.
func loopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifr); err != nil {
		return err
	}
	ifr.SetUint16(ifr.Uint16() | unix.IFF_UP)
	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
}

// startNetnsChild launches the resident and waits for its ports.
func startNetnsChild(t *testing.T) (pid, port int, client string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), netnsChildEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET}
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("creating a network namespace needs CAP_SYS_ADMIN — run the suite with e2e/run.sh")
		}
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lines <- line
	}()
	select {
	case line := <-lines:
		if reason, ok := strings.CutPrefix(line, "skip: "); ok {
			t.Skip(strings.TrimSpace(reason) + " — run the suite with e2e/run.sh")
		}
		if _, err := fmt.Sscanf(line, "ready %d %s", &port, &client); err != nil {
			t.Fatalf("namespace child said %q: %v", line, err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("namespace child never became ready")
	}
	return cmd.Process.Pid, port, client
}

func TestSnapshotSeesOtherNetworkNamespaces(t *testing.T) {
	childPID, port, client := startNetnsChild(t)
	cfg := configDir(t)

	snap := snapshot(t, cfg)

	l := findListener(snap, "tcp", port)
	if l == nil || l.NetNS == "" {
		t.Fatalf("listener :%d inside the namespace is missing or untagged: %+v", port, l)
	}
	if int(l.PID) != childPID {
		t.Errorf("namespace listener attributed to pid %d, want %d", l.PID, childPID)
	}
	// 0.0.0.0 inside a namespace nothing publishes is not host exposure.
	if got := l.Exposure(); got != "netns" {
		t.Errorf("namespace wildcard listener classed %q, want netns", got)
	}

	var conn *probe.Conn
	for i, c := range snap.Connections {
		if c.Local == client && c.NetNS != "" {
			conn = &snap.Connections[i]
		}
	}
	if conn == nil {
		t.Fatalf("the namespace's connection from %s is missing", client)
	}
	if conn.NetNS != l.NetNS {
		t.Errorf("connection labelled %q, listener %q — same namespace", conn.NetNS, l.NetNS)
	}
	// The suite runs as root, so counters come from sock_diag inside the
	// namespace: 80 kB went each way.
	if conn.TxBytes < 80000 || conn.RxBytes < 80000 {
		t.Errorf("namespace connection counters tx=%d rx=%d, want ≥ 80000 each", conn.TxBytes, conn.RxBytes)
	}

	// Our own sockets are untouched: same tuple space, no label.
	f := newFixture(t)
	snap = snapshot(t, cfg)
	if own := findListener(snap, "tcp", f.exposedPort); own == nil || own.NetNS != "" {
		t.Errorf("own-namespace listener mislabelled: %+v", own)
	}
}

// --once is the plain listing: a namespaced row carries its label, and its
// wildcard listener is not reported as exposed.
func TestOnceTagsNamespacedRows(t *testing.T) {
	_, port, _ := startNetnsChild(t)
	r := run(t, configDir(t), "--once")
	if r.code != 0 {
		t.Fatalf("--once exited %d: %s", r.code, r.out())
	}
	var line string
	for _, l := range strings.Split(r.stdout, "\n") {
		if strings.Contains(l, fmt.Sprintf("0.0.0.0:%d ", port)) {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("--once does not list the namespace's listener :%d:\n%s", port, r.stdout)
	}
	if !strings.Contains(line, "[netns ") || !strings.Contains(line, " netns ") || strings.Contains(line, "exposed") {
		t.Errorf("namespaced listener row %q lacks its label or is called exposed", line)
	}
}
