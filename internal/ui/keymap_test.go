package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Two actions bound to the same key are resolved first-wins. Walking a Go map
// to decide which is first hands the same config file a different keyboard on
// every start.
func TestKeymapIsDeterministic(t *testing.T) {
	overrides := map[string]string{"copy": "m", "export": "m", "watch": "m"}
	want := newKeymap(overrides).keyFor("copy")
	for i := 0; i < 200; i++ {
		if got := newKeymap(overrides).keyFor("copy"); got != want {
			t.Fatalf("binding changed between runs: %q then %q", want, got)
		}
	}
}

// A custom key that is another action's default must win every time, not
// whichever the map iteration happened to write last.
func TestKeymapCustomBindingBeatsDefault(t *testing.T) {
	for i := 0; i < 100; i++ {
		km := newKeymap(map[string]string{"copy": "x"})
		if got := km.translate("x"); got != "y" {
			t.Fatalf("run %d: translate(x) = %q, want copy's y", i, got)
		}
	}
}

// The kill prompt's letters are fixed; a rebinding elsewhere must not turn
// its `h` (SIGHUP) into a cancel.
func TestKillPromptIgnoresRebinding(t *testing.T) {
	m := routesModel()
	m.keymap = newKeymap(map[string]string{"hide-noise": "H"})
	m.confirmingKill = true
	m.killPID, m.killName = 999999, "nothing"

	next, cmd := m.Update(keyMsg("h"))
	nm := next.(Model)
	if cmd == nil || nm.status == "Kill cancelled" {
		t.Fatalf("h in the kill prompt was cancelled (status %q)", nm.status)
	}
	// The command would really signal a PID; it is deliberately not run.
}

// --- help overlay honesty ------------------------------------------------

// The overlay tells the reader these are their own bindings. That is only true
// if every row comes from the keymap: a row that hardcodes its key shows the
// default to somebody who rebound it, which is worse than showing nothing.
func TestHelpOverlayShowsEveryReboundKey(t *testing.T) {
	// Give every action an unmistakable binding, then check the overlay shows
	// it rather than the default.
	letters := "abcdefghijklmnopqrstuvwxyz"
	overrides := map[string]string{}
	i := 0
	for _, action := range Actions() {
		// Alt-prefixed keys collide with nothing and are never reserved.
		overrides[action] = "alt+" + string(letters[i%len(letters)]) + strings.Repeat("'", i/len(letters))
		i++
	}

	m := NewModel()
	m.w, m.h = 200, 60
	m.keymap = newKeymap(overrides)

	m.openHelp()
	txt := stripANSI(m.helpText())
	for _, action := range Actions() {
		key := m.keymap.keyFor(action)
		if key != overrides[action] {
			t.Fatalf("setup: %s is bound to %q, want %q", action, key, overrides[action])
		}
		if !strings.Contains(txt, key) {
			t.Errorf("the help overlay does not show %q, the key bound to %q", key, action)
		}
	}
}

// Every rebindable action must name a key the handler actually dispatches on,
// or the binding silently does nothing.
func TestEveryActionReachesTheHandler(t *testing.T) {
	m := drillModel(t)
	m.activeTab = tabConns
	for _, action := range Actions() {
		def := defaultKeys[action]
		if def == "" {
			t.Errorf("action %q has no default key", action)
			continue
		}
		// translate maps a bound key onto the default the switch is written
		// against; with no overrides it must be the identity.
		if got := m.keymap.translate(def); got != def {
			t.Errorf("default key %q for %q does not reach the handler (got %q)", def, action, got)
		}
	}
}

// --- remappable keys -----------------------------------------------------

func TestKeymapRebinds(t *testing.T) {
	km := newKeymap(map[string]string{"closed": "Z"})

	if got := km.translate("Z"); got != "z" {
		t.Errorf("translate(Z) = %q, want z", got)
	}
	// The default key must stop doing its old job, or one press does two
	// things.
	if got := km.translate("z"); got == "z" {
		t.Error("the default key still fires its action after being rebound")
	}
	if got := km.keyFor("closed"); got != "Z" {
		t.Errorf("keyFor(closed) = %q", got)
	}
	// Unrelated keys pass through untouched.
	if got := km.translate("g"); got != "g" {
		t.Errorf("translate(g) = %q, want g", got)
	}
}

func TestKeymapRefusesDangerousBindings(t *testing.T) {
	km := newKeymap(map[string]string{
		"closed":     "ctrl+c", // would leave no way out
		"group":      "tab",    // would break navigation
		"quit":       "up",     // ditto
		"not-a-verb": "Q",      // unknown action
	})
	for _, reserved := range []string{"ctrl+c", "tab", "up"} {
		if got := km.translate(reserved); got != reserved {
			t.Errorf("reserved key %q was rebound to %q", reserved, got)
		}
	}
	if len(km.overrides()) != 0 {
		t.Errorf("overrides = %v, want every bad binding dropped", km.overrides())
	}
	// Everything still works on its default key.
	if got := km.translate("z"); got != "z" {
		t.Errorf("translate(z) = %q after the bad config was dropped", got)
	}
}

// Two actions bound to one key would make a press ambiguous.
func TestKeymapRefusesDuplicates(t *testing.T) {
	km := newKeymap(map[string]string{"closed": "Z", "group": "Z"})
	if n := len(km.overrides()); n != 1 {
		t.Errorf("overrides = %v, want only one of the two", km.overrides())
	}
}

func TestKeymapDrivesTheKeyHandler(t *testing.T) {
	m := drillModel(t)
	m.activeTab = tabConns
	m.keymap = newKeymap(map[string]string{"closed": "Z"})

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Z")})
	if !out.(Model).showClosed {
		t.Error("the rebound key did not reach the handler")
	}

	// And the old key no longer does it.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if out.(Model).showClosed {
		t.Error("the old key still toggles the view it was unbound from")
	}
}
