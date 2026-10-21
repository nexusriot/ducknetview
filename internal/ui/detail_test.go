package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nexusriot/ducknetview/internal/probe"
)

// enter opens the row-detail overlay (with the resolved service name), and any
// key closes it.
func TestDetailOverlay(t *testing.T) {
	m := NewModel()
	m.activeTab = tabPorts
	m.portsVP.Width = 100
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:443", PID: 1, Process: "nginx"}}

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if !m.showDetail {
		t.Fatal("enter should open the detail overlay")
	}
	if !strings.Contains(m.detailText, "nginx") || !strings.Contains(m.detailText, "443") {
		t.Fatalf("detail missing fields: %q", m.detailText)
	}
	if svc := probe.ServiceName(443, "tcp"); svc != "" && !strings.Contains(m.detailText, svc) {
		t.Fatalf("detail should include service %q: %q", svc, m.detailText)
	}

	// Navigation keys scroll the overlay instead of dismissing it, so long
	// command lines can be read in full.
	model, _ = m.Update(key("j"))
	m = model.(Model)
	if !m.showDetail {
		t.Fatal("j should scroll the detail overlay, not close it")
	}

	model, _ = m.Update(key("z"))
	m = model.(Model)
	if m.showDetail {
		t.Fatal("any non-navigation key should close the detail overlay")
	}
}

func TestDetailOverlayCopiesPlainText(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabPorts
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:443", PID: 1, Process: "nginx"}}

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if !m.showDetail {
		t.Fatal("enter should open the overlay")
	}

	plain := stripANSI(m.detailText)
	if strings.Contains(plain, "\x1b") {
		t.Errorf("stripANSI left escape sequences: %q", plain)
	}
	if !strings.Contains(plain, "nginx") {
		t.Errorf("stripped detail lost its content: %q", plain)
	}
}
