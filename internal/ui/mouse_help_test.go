package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func TestMouseRowIndex(t *testing.T) {
	vp := viewport.New(80, 10)

	// Above the data area (header block / borders) is not a data row.
	if _, ok := mouseRowIndex(&vp, 3); ok {
		t.Fatal("y=3 (above content) should be invalid")
	}
	if _, ok := mouseRowIndex(&vp, 4); ok {
		t.Fatal("y=4 (inside the table header block) should be invalid")
	}
	// First data row sits at y = topOffset(4) + tableHeaderLines(5) = 9.
	if i, ok := mouseRowIndex(&vp, 9); !ok || i != 0 {
		t.Fatalf("y=9 -> (%d,%v), want (0,true)", i, ok)
	}
	if i, ok := mouseRowIndex(&vp, 10); !ok || i != 1 {
		t.Fatalf("y=10 -> (%d,%v), want (1,true)", i, ok)
	}
	// Below the viewport (height 10 => rows [4,14)) is invalid.
	if _, ok := mouseRowIndex(&vp, 14); ok {
		t.Fatal("y=14 (below viewport) should be invalid")
	}

	// When scrolled, the same screen row maps to a later data row.
	vp.SetContent(strings.Repeat("line\n", 40))
	vp.SetYOffset(5)
	if i, ok := mouseRowIndex(&vp, 4); !ok || i != 0 {
		t.Fatalf("scrolled y=4 -> (%d,%v), want (0,true)", i, ok)
	}
}

func TestHelpToggle(t *testing.T) {
	m := NewModel()

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = model.(Model)
	if !m.showHelp {
		t.Fatal("? should open the help overlay")
	}

	// The overlay is longer than a terminal, so the scroll keys scroll it
	// rather than closing it — the same contract the detail overlay has.
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = model.(Model)
	if !m.showHelp {
		t.Fatal("a scroll key should scroll the help overlay, not close it")
	}

	// Any other key closes it again.
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = model.(Model)
	if m.showHelp {
		t.Fatal("a keypress should close the help overlay")
	}
}
