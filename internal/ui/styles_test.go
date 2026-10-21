package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Colour is how a table says what a cell means, and the mapping is the part
// worth pinning down; the palette itself is a preference.
func TestStateAndScopeStyles(t *testing.T) {
	if stateStyle("ESTABLISHED").GetForeground() == stateStyle("TIME_WAIT").GetForeground() {
		t.Error("an established connection is drawn like one being torn down")
	}
	for _, teardown := range []string{"TIME_WAIT", "CLOSE_WAIT", "FIN_WAIT1", "LAST_ACK", "NONE", "-", ""} {
		if stateStyle(teardown).GetForeground() != subtleStyle.GetForeground() {
			t.Errorf("state %q is not dimmed", teardown)
		}
	}
	if stateStyle("SYN_SENT").GetForeground() != statePendStyle.GetForeground() {
		t.Error("a handshake in flight is not highlighted")
	}

	if scopeStyle("public").GetForeground() == scopeStyle("local").GetForeground() {
		t.Error("a public remote is drawn like a loopback one")
	}
	if scopeStyle("cgnat").GetForeground() != scopeStyle("private").GetForeground() {
		t.Error("CGNAT and private should read the same")
	}
	if exposureStyle("exposed").GetForeground() == exposureStyle("local").GetForeground() {
		t.Error("an exposed listener is drawn like a loopback one")
	}
	// An address that does not parse gets the plain style rather than a
	// misleading one.
	plain := lipgloss.NewStyle().GetForeground()
	if exposureStyle("").GetForeground() != plain || scopeStyle("").GetForeground() != plain {
		t.Error("an unclassifiable address was given a colour")
	}
}

func TestStripANSI(t *testing.T) {
	styled := okStyle.Render("hello") + " " + errStyle.Render("world")
	if got := stripANSI(styled); got != "hello world" {
		t.Errorf("stripANSI = %q, want %q", got, "hello world")
	}
	if got := stripANSI("plain"); got != "plain" {
		t.Errorf("stripANSI mangled unstyled text: %q", got)
	}
}

// --- themes --------------------------------------------------------------

func TestThemeCycleAndApply(t *testing.T) {
	defer applyTheme(defaultTheme)

	names := ThemeNames()
	if len(names) < 2 {
		t.Fatal("there is only one theme, so cycling is meaningless")
	}
	seen := map[string]bool{}
	cur := names[0]
	for range names {
		seen[cur] = true
		cur = nextTheme(cur)
	}
	if len(seen) != len(names) {
		t.Errorf("cycling visited %d of %d themes", len(seen), len(names))
	}
	if cur != names[0] {
		t.Errorf("cycling did not return to the start: %q", cur)
	}

	// An unknown name must fall back rather than render everything unstyled.
	applyTheme("no-such-theme")
	if !validTheme("default") || validTheme("no-such-theme") {
		t.Error("validTheme is wrong")
	}
}

func TestThemeChangesStyles(t *testing.T) {
	withColor(t)
	defer applyTheme(defaultTheme)

	applyTheme("default")
	before := okStyle.Render("x")
	applyTheme("contrast")
	after := okStyle.Render("x")
	if before == after {
		t.Error("switching theme did not change the rendered styling")
	}
}

func TestThemePersists(t *testing.T) {
	defer applyTheme(defaultTheme)

	m := NewModel()
	m.theme = "contrast"
	m.saveConfig()

	loaded := NewModel().WithConfig()
	if loaded.theme != "contrast" {
		t.Errorf("theme = %q after a reload, want contrast", loaded.theme)
	}
}
