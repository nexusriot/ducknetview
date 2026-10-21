package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A view is the whole arrangement under a name, and it has to come back.
func TestSavedViewRoundTrips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, `{}`)

	m := NewModel().WithConfig()
	m.w, m.h = 160, 48
	m.activeTab = tabConns
	m.connsQuery = "443"
	m.protoFilter = 1
	m.publicOnly = true
	m.connsGroup = true
	m.showTotals = true
	m.connsSortCol = 3
	m.connsSortDesc = true
	m.ifaceFilter = "wg0"

	if msg, ok := m.saveView("vpn"); !ok {
		t.Fatalf("saving failed: %s", msg)
	}

	// Change everything, then recall.
	m.activeTab = tabOverview
	m.connsQuery = ""
	m.protoFilter, m.publicOnly, m.connsGroup, m.showTotals = 0, false, false, false
	m.connsSortCol, m.connsSortDesc, m.ifaceFilter = 0, false, ""

	m2, ok := m.WithView("VPN") // names are matched case-insensitively
	if !ok {
		t.Fatal("the saved view was not found")
	}
	switch {
	case m2.activeTab != tabConns:
		t.Error("the tab did not come back")
	case m2.connsQuery != "443":
		t.Error("the search did not come back")
	case m2.protoFilter != 1 || !m2.publicOnly:
		t.Error("the quick filters did not come back")
	case !m2.connsGroup || !m2.showTotals:
		t.Error("the view toggles did not come back")
	case m2.connsSortCol != 3 || !m2.connsSortDesc:
		t.Error("the sort did not come back")
	case m2.ifaceFilter != "wg0":
		t.Error("the interface filter did not come back")
	}

	// And it survives a restart, because it is in the config.
	if names := ViewNames(); len(names) != 1 || names[0] != "vpn" {
		t.Errorf("the view was not persisted: %v", names)
	}
	if _, ok := NewModel().WithConfig().WithView("vpn"); !ok {
		t.Error("a fresh session could not find the saved view")
	}
}

// The overlay is the only way most people will meet the feature.
func TestViewsOverlayKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, `{}`)

	m := NewModel().WithConfig()
	m.w, m.h = 160, 48
	m.activeTab = tabPorts

	model, _ := m.Update(key("V"))
	m = model.(Model)
	if !m.showViews {
		t.Fatal("V should open the views overlay")
	}
	if !strings.Contains(stripANSI(m.View()), "No views saved yet") {
		t.Error("an empty overlay should explain itself")
	}

	// n names the current view; the name goes to the text input.
	model, _ = m.Update(key("n"))
	m = model.(Model)
	if !m.viewsNaming {
		t.Fatal("n should start naming")
	}
	for _, r := range "ports" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(Model)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if len(m.views) != 1 || m.views[0].Name != "ports" {
		t.Fatalf("the view was not saved: %+v", m.views)
	}

	// enter applies it and closes the overlay.
	m.activeTab = tabOverview
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if m.showViews {
		t.Error("applying a view should close the overlay")
	}
	if m.activeTab != tabPorts {
		t.Errorf("the view was not applied: tab %v", m.activeTab)
	}

	// d deletes.
	model, _ = m.Update(key("V"))
	m = model.(Model)
	model, _ = m.Update(key("d"))
	m = model.(Model)
	if len(m.views) != 0 {
		t.Errorf("d should have deleted the view: %+v", m.views)
	}
}

// The same overflow, in the overlay that can grow without bound.
func TestViewsOverlayFitsTheTerminal(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 24
	for i := 0; i < maxSavedViews; i++ {
		m.views = append(m.views, savedView{Name: "view" + string(rune('a'+i%26)) + string(rune('0'+i/26))})
	}
	m.openViews()
	m.viewsCursor = len(m.views) - 1

	drawn := strings.Count(m.renderViews(), "\n") + 1
	if drawn > m.h {
		t.Errorf("%d saved views draw %d rows on a %d-row terminal", len(m.views), drawn, m.h)
	}
	// Whichever view is highlighted has to be one of the ones on screen.
	if !strings.Contains(stripANSI(m.renderViews()), m.views[m.viewsCursor].Name) {
		t.Error("the highlighted view is not on screen")
	}
}
