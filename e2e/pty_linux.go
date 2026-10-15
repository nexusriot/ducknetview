//go:build linux && e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The TUI only exists on a terminal: Bubble Tea puts the terminal in raw mode,
// switches to the alternate screen and reads keys from it, so a pipe tests
// nothing. A pseudo-terminal is the only way to drive the program a user
// actually runs — and it is a dozen ioctls, which is cheaper than a dependency.

// ioctl numbers, stable across the Linux architectures this builds for.
const (
	tiocGPTN   = 0x80045430 // TIOCGPTN: which /dev/pts/N this master owns
	tiocSPTLCK = 0x40045431 // TIOCSPTLCK: unlock the slave
	tiocSWINSZ = 0x5414     // TIOCSWINSZ: set the window size
)

type winsize struct {
	rows, cols, x, y uint16
}

// terminal is a program running on a pseudo-terminal, with everything it has
// drawn so far collected for the test to assert on.
type terminal struct {
	cmd    *exec.Cmd
	master *os.File

	mu  sync.Mutex
	buf strings.Builder

	done chan struct{}
}

// startTerminal runs a command on a fresh pty of a fixed size. The size is
// fixed because the layout is computed from it: an assertion about a column
// means nothing if the width is whatever the CI runner happened to have.
func startTerminal(cmd *exec.Cmd, rows, cols uint16) (*terminal, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	var unlock int32
	if err := ioctl(master.Fd(), tiocSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		master.Close()
		return nil, fmt.Errorf("unlock pty: %w", err)
	}
	var n int32
	if err := ioctl(master.Fd(), tiocGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		master.Close()
		return nil, fmt.Errorf("pty number: %w", err)
	}
	ws := winsize{rows: rows, cols: cols}
	if err := ioctl(master.Fd(), tiocSWINSZ, uintptr(unsafe.Pointer(&ws))); err != nil {
		master.Close()
		return nil, fmt.Errorf("set window size: %w", err)
	}

	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("open slave: %w", err)
	}
	defer slave.Close()

	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}

	t := &terminal{cmd: cmd, master: master, done: make(chan struct{})}
	go t.pump()
	return t, nil
}

// pump collects everything the program draws. It runs until the pty closes,
// which happens when the program exits.
func (t *terminal) pump() {
	defer close(t.done)
	b := make([]byte, 4096)
	for {
		n, err := t.master.Read(b)
		if n > 0 {
			t.mu.Lock()
			t.buf.Write(b[:n])
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// screen is everything drawn so far, with the escape sequences removed: the
// alternate screen, the cursor moves and the colours are not what a test is
// trying to say something about.
func (t *terminal) screen() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return stripEscapes(t.buf.String())
}

// send types a string at the program.
func (t *terminal) send(s string) error {
	_, err := t.master.WriteString(s)
	return err
}

// waitFor blocks until the program has drawn text containing want. The whole
// scrollback is searched rather than the last frame, because a TUI redraws
// continuously and a frame boundary is not something the test can see.
func (t *terminal) waitFor(want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(t.screen(), want) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s waiting for %q; drawn so far:\n%s",
		timeout, want, tail(t.screen(), 4000))
}

// wait reaps the program, killing it if it overstays.
func (t *terminal) wait(timeout time.Duration) error {
	errc := make(chan error, 1)
	go func() { errc <- t.cmd.Wait() }()
	select {
	case err := <-errc:
		t.master.Close()
		<-t.done
		return err
	case <-time.After(timeout):
		_ = t.cmd.Process.Kill()
		<-errc
		t.master.Close()
		return fmt.Errorf("the program did not exit within %s; drawn so far:\n%s",
			timeout, tail(t.screen(), 4000))
	}
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

// stripEscapes removes CSI and OSC sequences so a screen can be searched for
// plain text. It is deliberately a separate implementation from the one in the
// ui package: a test that reuses the code under test cannot catch it being
// wrong.
func stripEscapes(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			out.WriteByte(s[i])
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j + 1
			continue
		}
		if i+1 < len(s) && s[i+1] == ']' {
			j := i + 2
			for j < len(s) && s[j] != 0x07 {
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					j++
					break
				}
				j++
			}
			i = j + 1
			continue
		}
		i += 2
	}
	return out.String()
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
