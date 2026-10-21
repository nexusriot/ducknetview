package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The settings overlay is the only way most people will ever set a threshold.
func TestSettingsOverlayEditing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, `{}`)

	m := NewModel().WithConfig()
	m.w, m.h = 160, 48
	m.openSettings()

	// Moving the cursor stays in range at both ends.
	for i := 0; i < 50; i++ {
		m.settingsKey("down")
	}
	if m.settingsCursor != len(settingsRows())-1 {
		t.Errorf("the cursor ran past the last row: %d", m.settingsCursor)
	}
	for i := 0; i < 50; i++ {
		m.settingsKey("up")
	}
	if m.settingsCursor != 0 {
		t.Errorf("the cursor ran past the first row: %d", m.settingsCursor)
	}

	// Typing a number into the first row (process throughput) sets it.
	m.settingsKey("enter")
	if !m.settingsEditing {
		t.Fatal("enter should start editing")
	}
	m.settingsInput.SetValue("2048")
	if status, _ := m.settingsKey("enter"); !strings.Contains(status, "2048") {
		t.Errorf("setting a threshold said %q", status)
	}
	if m.alertProcBps != 2048 {
		t.Errorf("alertProcBps = %v", m.alertProcBps)
	}

	// "off" disables it again.
	m.settingsKey("enter")
	m.settingsInput.SetValue("off")
	m.settingsKey("enter")
	if m.alertProcBps != 0 {
		t.Errorf("off left the threshold at %v", m.alertProcBps)
	}

	// Rubbish is refused with a reason, and changes nothing.
	m.settingsKey("enter")
	m.settingsInput.SetValue("soon")
	status, _ := m.settingsKey("enter")
	if !strings.Contains(status, "Not a number") {
		t.Errorf("a non-number was accepted: %q", status)
	}
	if m.alertProcBps != 0 {
		t.Errorf("a refused value was applied: %v", m.alertProcBps)
	}

	// Escape abandons an edit.
	m.settingsKey("enter")
	m.settingsInput.SetValue("999")
	if status, _ := m.settingsKey("esc"); status != "Cancelled" {
		t.Errorf("esc said %q", status)
	}
	if m.alertProcBps != 0 {
		t.Errorf("a cancelled edit was applied: %v", m.alertProcBps)
	}

	// A key the overlay does not claim closes it.
	if _, handled := m.settingsKey("Z"); handled {
		t.Error("the overlay claimed a key it has no use for")
	}
}

// --- settings overlay ----------------------------------------------------

func TestSettingsEditsAThreshold(t *testing.T) {
	m := NewModel()
	m.w, m.h = 120, 40
	m.openSettings()
	if !m.showSettings {
		t.Fatal("overlay did not open")
	}

	// Find the RTT row and edit it.
	rows := settingsRows()
	for i, r := range rows {
		if r.key == "alert_rtt_ms" {
			m.settingsCursor = i
		}
	}
	if _, handled := m.settingsKey("enter"); !handled {
		t.Fatal("enter did not start editing")
	}
	if !m.settingsEditing {
		t.Fatal("settingsEditing = false after enter")
	}

	m.settingsInput.SetValue("250")
	status, _ := m.settingsKey("enter")
	if m.alertRules.rttMs != 250 {
		t.Errorf("rttMs = %v, want 250", m.alertRules.rttMs)
	}
	if !strings.Contains(status, "250") {
		t.Errorf("status = %q", status)
	}
	if m.settingsEditing {
		t.Error("still editing after enter")
	}

	// "off" disables it again.
	m.settingsKey("enter")
	m.settingsInput.SetValue("off")
	m.settingsKey("enter")
	if m.alertRules.rttMs != 0 {
		t.Errorf("rttMs = %v after 'off', want 0", m.alertRules.rttMs)
	}
}

func TestSettingsRejectsRubbish(t *testing.T) {
	m := NewModel()
	m.openSettings()
	for i, r := range settingsRows() {
		if r.key == "alert_fanout_hosts" {
			m.settingsCursor = i
		}
	}
	m.settingsKey("enter")
	m.settingsInput.SetValue("twenty")
	status, _ := m.settingsKey("enter")
	if m.alertRules.fanoutHosts != 0 {
		t.Errorf("fanoutHosts = %d, want it left alone", m.alertRules.fanoutHosts)
	}
	if !strings.Contains(status, "Not a number") {
		t.Errorf("status = %q, want an explanation", status)
	}

	// A negative threshold would fire on every poll.
	m.settingsKey("enter")
	m.settingsInput.SetValue("-5")
	m.settingsKey("enter")
	if m.alertRules.fanoutHosts != 0 {
		t.Errorf("fanoutHosts = %d, a negative threshold was accepted", m.alertRules.fanoutHosts)
	}
}

func TestSettingsLatencyTargetsAddAndRemove(t *testing.T) {
	m := NewModel()
	m.openSettings()
	for i, r := range settingsRows() {
		if r.key == "latency_targets" {
			m.settingsCursor = i
		}
	}

	add := func(v string) string {
		m.settingsKey("enter")
		m.settingsInput.SetValue(v)
		status, _ := m.settingsKey("enter")
		return status
	}

	add("1.1.1.1:53")
	if len(m.latencyExtra) != 1 || m.latencyExtra[0] != "1.1.1.1:53" {
		t.Fatalf("latencyExtra = %v", m.latencyExtra)
	}
	// The same value again removes it, which is how the list rows work.
	add("1.1.1.1:53")
	if len(m.latencyExtra) != 0 {
		t.Errorf("latencyExtra = %v, want the entry removed", m.latencyExtra)
	}
	// A target with no port is silently never measured, so it is refused here.
	if status := add("example.com"); !strings.Contains(status, "host:port") {
		t.Errorf("status = %q, want an explanation of the required shape", status)
	}
	if len(m.latencyExtra) != 0 {
		t.Errorf("a portless target was accepted: %v", m.latencyExtra)
	}
}

func TestSettingsHiddenColumns(t *testing.T) {
	m := NewModel()
	m.openSettings()
	for i, r := range settingsRows() {
		if r.key == "hidden_columns" {
			m.settingsCursor = i
		}
	}
	set := func(v string) string {
		m.settingsKey("enter")
		m.settingsInput.SetValue(v)
		status, _ := m.settingsKey("enter")
		return status
	}

	set("svc")
	if !m.hiddenCols["svc"] {
		t.Error("svc was not hidden")
	}
	set("svc")
	if m.hiddenCols["svc"] {
		t.Error("svc was not shown again")
	}
	// Hiding a column that does not exist would silently do nothing.
	if status := set("nonsense"); !strings.Contains(status, "Unknown column") {
		t.Errorf("status = %q", status)
	}
}

// The overlay must own the keyboard while it is open, or a key falls through
// to a table nobody can see.
func TestSettingsOverlayCapturesKeys(t *testing.T) {
	m := NewModel()
	m.w, m.h = 120, 40
	m.activeTab = tabConns
	m.openSettings()

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	got := out.(Model)
	if got.showClosed {
		t.Error("a key leaked through the settings overlay to the table underneath")
	}

	// esc closes it.
	out, _ = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if out.(Model).showSettings {
		t.Error("esc did not close the overlay")
	}
}

func TestSettingsRendersEveryRow(t *testing.T) {
	m := NewModel()
	m.w, m.h = 120, 45
	m.openSettings()
	txt := stripANSI(m.renderSettings())
	for _, r := range settingsRows() {
		if !strings.Contains(txt, r.title) {
			t.Errorf("settings overlay missing row %q", r.title)
		}
	}
}

// The `,` key opens it through the ordinary key path.
func TestSettingsKeyBinding(t *testing.T) {
	m := NewModel()
	m.w, m.h = 120, 40
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(",")})
	if !out.(Model).showSettings {
		t.Error(", did not open the settings overlay")
	}
}
