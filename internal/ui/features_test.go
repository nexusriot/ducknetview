package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func sampleConnsModel() Model {
	m := NewModel()
	m.connsVP.Width = 120
	m.connsVP.Height = 20
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:5000", Remote: "1.2.3.4:443", State: "ESTABLISHED", PID: 10, Process: "curl"},
		{Proto: "tcp", Local: "10.0.0.1:6000", Remote: "5.6.7.8:80", State: "TIME_WAIT", PID: 20, Process: "wget"},
	}
	return m
}

func TestCopyTargetConn(t *testing.T) {
	m := sampleConnsModel()
	m.activeTab = tabConns
	m.connsCursor = 0
	text, what, ok := m.copyTarget()
	if !ok {
		t.Fatal("expected a copy target")
	}
	if what != "connection row" {
		t.Fatalf("what = %q", what)
	}
	if !strings.Contains(text, "1.2.3.4:443") || !strings.Contains(text, "curl") {
		t.Fatalf("unexpected copy text: %q", text)
	}
}

func TestCopyTargetPort(t *testing.T) {
	m := NewModel()
	m.activeTab = tabPorts
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 99, Process: "sshd"}}
	text, what, ok := m.copyTarget()
	if !ok || what != "port row" {
		t.Fatalf("ok=%v what=%q", ok, what)
	}
	if !strings.Contains(text, "0.0.0.0:22") || !strings.Contains(text, "sshd") {
		t.Fatalf("unexpected copy text: %q", text)
	}
}

func TestCopyTargetOutOfRange(t *testing.T) {
	m := NewModel()
	m.activeTab = tabConns
	m.connsCursor = 5 // no conns loaded
	if _, _, ok := m.copyTarget(); ok {
		t.Fatal("expected no copy target when cursor is out of range")
	}
}

func TestHideNoiseFilter(t *testing.T) {
	m := NewModel()
	m.lastSnap = probe.NetSnapshot{Ifaces: []probe.IfaceInfo{
		{Name: "eth0", Kind: probe.IfacePhysical, IsUp: true},
		{Name: "lo", Kind: probe.IfaceLoopback, IsUp: true},
		{Name: "docker0", Kind: probe.IfaceDockerBridge, IsUp: true},
		{Name: "veth123", Kind: probe.IfaceVeth, IsUp: true},
		{Name: "eth1", Kind: probe.IfacePhysical, IsUp: false}, // down
		{Name: "wg0", Kind: probe.IfaceVirt, IsUp: true},
	}}

	m.rebuildIfaceList()
	if got := len(m.ifaceList.Items()); got != 6 {
		t.Fatalf("expected 6 interfaces unfiltered, got %d", got)
	}

	m.hideNoise = true
	m.rebuildIfaceList()
	names := make([]string, 0)
	for _, it := range m.ifaceList.Items() {
		names = append(names, it.(ifaceItem).name)
	}
	// Keep up physical (eth0) and up virtual/VPN (wg0); drop loopback, docker,
	// veth and the down interface.
	if len(names) != 2 {
		t.Fatalf("expected [eth0 wg0] after hide-noise, got %v", names)
	}
}

func TestTableNavCursorClamp(t *testing.T) {
	m := sampleConnsModel()
	m.activeTab = tabConns

	m.tableNav(tea.KeyMsg{Type: tea.KeyDown})
	if m.connsCursor != 1 {
		t.Fatalf("after down: cursor=%d want 1", m.connsCursor)
	}
	m.tableNav(tea.KeyMsg{Type: tea.KeyDown}) // clamp at last row
	if m.connsCursor != 1 {
		t.Fatalf("after second down: cursor=%d want clamped 1", m.connsCursor)
	}
	m.tableNav(tea.KeyMsg{Type: tea.KeyUp})
	if m.connsCursor != 0 {
		t.Fatalf("after up: cursor=%d want 0", m.connsCursor)
	}
	m.tableNav(tea.KeyMsg{Type: tea.KeyEnd})
	if m.connsCursor != 1 {
		t.Fatalf("after end: cursor=%d want 1", m.connsCursor)
	}
	m.tableNav(tea.KeyMsg{Type: tea.KeyHome})
	if m.connsCursor != 0 {
		t.Fatalf("after home: cursor=%d want 0", m.connsCursor)
	}
}
