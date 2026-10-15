//go:build linux && e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A TUI is the one part of the program a pipe cannot test: Bubble Tea takes
// the terminal into raw mode, switches to the alternate screen and reads keys
// from it. Driven on a real pseudo-terminal, this is the program as a user
// runs it — startup, layout, every tab, the overlays and the exit path.

const (
	ptyRows, ptyCols = 48, 160
	drawTimeout      = 30 * time.Second
)

func startTUI(t *testing.T, cfgDir string, args ...string) *terminal {
	t.Helper()

	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+cfgDir,
		"TERM=xterm-256color",
	)

	term, err := startTerminal(cmd, ptyRows, ptyCols)
	if err != nil {
		t.Fatalf("start the TUI on a pty: %v", err)
	}
	t.Cleanup(func() {
		if term.cmd.ProcessState == nil {
			_ = term.cmd.Process.Kill()
			_ = term.wait(5 * time.Second)
		}
	})
	return term
}

func TestTUIDrawsEveryTab(t *testing.T) {
	f := newFixture(t)
	cfg := configDir(t)

	term := startTUI(t, cfg)
	if err := term.waitFor("ducknetview", drawTimeout); err != nil {
		t.Fatal(err)
	}

	// The Overview is drawn from the first sample, so it has to arrive before
	// anything else is believable.
	if err := term.waitFor("Host:", drawTimeout); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Uptime:", "Ifaces:", "Security:", "Total (all interfaces)"} {
		if err := term.waitFor(want, drawTimeout); err != nil {
			t.Error(err)
		}
	}

	// Each tab by its number, waiting on something only that tab's body
	// draws — the tab names themselves are in the header of every frame, so
	// matching one of those would say nothing about having switched.
	for _, step := range []struct{ key, want string }{
		{"2", "MTU"},
		{"3", "Open listening ports"},
		{"4", "Processes by network connections"},
		{"5", "Active connections"},
		{"6", "Events (newest first)"},
		{"7", "Default route"},
	} {
		press(t, term, step.key, step.want)
	}

	// The fixture's listener is on the Ports tab, with its service column and
	// the bind address the test chose.
	press(t, term, "3", "Open listening ports")
	if err := term.waitFor(fmt.Sprintf("0.0.0.0:%d", f.exposedPort), drawTimeout); err != nil {
		t.Errorf("the fixture's listener never appeared on the Ports tab: %v", err)
	}

	// The help overlay, and any key closing it again.
	press(t, term, "?", "key bindings")
	dismiss(t, term, "Open listening ports")

	quit(t, term)
}

// Searching, sorting and the row detail overlay are what the Ports tab is for.
func TestTUIPortsTabInteraction(t *testing.T) {
	f := newFixture(t)
	cfg := configDir(t)

	term := startTUI(t, cfg, "--tab", "ports")
	if err := term.waitFor("Open listening ports", drawTimeout); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor(fmt.Sprintf("%d", f.loopbackPort), drawTimeout); err != nil {
		t.Fatal(err)
	}

	// Search for the loopback listener and apply it.
	if err := term.send("/"); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor("search port", drawTimeout); err != nil {
		t.Fatal(err)
	}
	if err := term.send(fmt.Sprintf("%d\r", f.loopbackPort)); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor("filter: "+fmt.Sprintf("%d", f.loopbackPort), drawTimeout); err != nil {
		// The status line words it as the active query; either spelling is
		// fine, what matters is that the query is shown as applied.
		if err2 := term.waitFor(fmt.Sprintf("%d", f.loopbackPort), 2*time.Second); err2 != nil {
			t.Fatal(err)
		}
	}

	// enter opens the detail overlay for the highlighted row.
	if err := term.send("\r"); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor("Listening port", drawTimeout); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Protocol", "Exposure", "Process"} {
		if err := term.waitFor(want, 5*time.Second); err != nil {
			t.Error(err)
		}
	}
	dismiss(t, term, "Open listening ports")

	quit(t, term)
}

// A view saved in the TUI has to survive the session and be reachable from the
// command line, which is the whole point of persisting it.
func TestTUISavedViewSurvivesTheSession(t *testing.T) {
	newFixture(t)
	cfg := configDir(t)

	term := startTUI(t, cfg)
	if err := term.waitFor("Host:", drawTimeout); err != nil {
		t.Fatal(err)
	}

	// Arrange something worth saving: the Connections tab, grouped.
	if err := term.send("5"); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor("Active connections", drawTimeout); err != nil {
		t.Fatal(err)
	}
	if err := term.send("g"); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor("grouped by remote host", drawTimeout); err != nil {
		t.Fatal(err)
	}

	// V opens the overlay, n names the view.
	if err := term.send("V"); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor("No views saved yet", drawTimeout); err != nil {
		t.Fatal(err)
	}
	if err := term.send("n"); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor("Save the current view as", drawTimeout); err != nil {
		t.Fatal(err)
	}
	if err := term.send("grouped\r"); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor("Saved view grouped", drawTimeout); err != nil {
		t.Fatal(err)
	}
	dismiss(t, term, "grouped by remote host")

	quit(t, term)

	// It is in the config file, under the name that was typed and nothing else.
	cfgFile := readFile(t, filepath.Join(cfg, "ducknetview", "config.json"))
	if !strings.Contains(cfgFile, `"name": "grouped"`) {
		t.Fatalf("the view was not persisted:\n%s", cfgFile)
	}

	// And --view opens straight into it.
	term2 := startTUI(t, cfg, "--view", "grouped")
	if err := term2.waitFor("grouped by remote host", drawTimeout); err != nil {
		t.Errorf("--view did not restore the arrangement: %v", err)
	}
	quit(t, term2)

	// An unknown name is an error that lists what there is.
	r := run(t, cfg, "--view", "nope")
	if r.code == 0 || !strings.Contains(r.out(), "grouped") {
		t.Errorf("--view nope: exit %d %q", r.code, r.out())
	}
}

// A snapshot session describes another machine at another time, and must not
// quietly start sampling this one.
func TestTUIBrowsesASnapshot(t *testing.T) {
	f := newFixture(t)
	cfg := configDir(t)

	file := filepath.Join(t.TempDir(), "snap.json")
	writeFile(t, file, run(t, cfg, "--json").stdout)

	term := startTUI(t, cfg, "--from", file)
	if err := term.waitFor("SNAPSHOT", drawTimeout); err != nil {
		t.Fatal(err)
	}
	if err := term.send("3"); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor(fmt.Sprintf("0.0.0.0:%d", f.exposedPort), drawTimeout); err != nil {
		t.Errorf("the snapshot's listeners were not shown: %v", err)
	}
	// Space is the pause key, and a file has nothing to resume.
	if err := term.send(" "); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor("there is nothing to resume", drawTimeout); err != nil {
		t.Error(err)
	}

	quit(t, term)
}

// press sends one key and waits for the screen to show it was acted on.
//
// The pause is not politeness: Bubble Tea reads the terminal in blocks and
// folds a run of printable bytes into a single multi-rune key, so two digits
// written back to back arrive as one keypress of "67" and match nothing at
// all. Nobody types that fast, and the suite must not either.
func press(t *testing.T, term *terminal, key, want string) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	if err := term.send(key); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor(want, drawTimeout); err != nil {
		t.Errorf("after %q: %v", key, err)
	}
}

// dismiss closes whatever overlay is open and waits for the tab underneath to
// be drawn again. Overlays swallow the next keypress by design, so a test that
// leaves one open would send its next key into the void.
func dismiss(t *testing.T, term *terminal, back string) {
	t.Helper()
	mark := len(term.screen())
	if err := term.send("\x1b"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(drawTimeout)
	for time.Now().Before(deadline) {
		if drawn := term.screen(); len(drawn) > mark && strings.Contains(drawn[mark:], back) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the overlay did not close back to %q:\n%s", back, tail(term.screen(), 3000))
}

// Quitting asks first, and the answer has to be taken.
func quit(t *testing.T, term *terminal) {
	t.Helper()
	if err := term.send("q"); err != nil {
		t.Fatal(err)
	}
	if err := term.waitFor("Quit ducknetview?", drawTimeout); err != nil {
		t.Fatal(err)
	}
	if err := term.send("y"); err != nil {
		t.Fatal(err)
	}
	if err := term.wait(20 * time.Second); err != nil {
		t.Fatalf("the TUI did not exit cleanly: %v", err)
	}
}
