package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Unprivileged, another user's sockets have no PID. Saying nothing makes the
// tool look wrong rather than restricted.
func TestUnattributedSocketsAreReported(t *testing.T) {
	restore := isPrivileged
	isPrivileged = func() bool { return false }
	defer func() { isPrivileged = restore }()

	m := NewModel()
	m.w, m.h = 160, 48
	m.procsVP.Width = 120
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:1", Remote: "1.1.1.1:443", PID: 0},
		{Proto: "tcp", Local: "10.0.0.2:2", Remote: "2.2.2.2:443", PID: 44},
	}
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 0}}

	if n := m.unattributedSockets(); n != 2 {
		t.Errorf("counted %d unattributed sockets, want 2", n)
	}
	if head := stripANSI(m.renderProcsText()); !strings.Contains(head, "2 sockets owned by another user") {
		t.Errorf("the Processes tab says nothing about it:\n%s", strings.SplitN(head, "\n", 2)[0])
	}

	// Root can see everything, so there is nothing to suggest.
	isPrivileged = func() bool { return true }
	if n := m.unattributedSockets(); n != 0 {
		t.Errorf("root should have nothing unattributed, got %d", n)
	}
	if m.attributionNote() != "" {
		t.Error("root should not be told to run as root")
	}
}

// The note is worth saying once, not once per poll.
func TestAttributionNoteIsRecordedOnce(t *testing.T) {
	restore := isPrivileged
	isPrivileged = func() bool { return false }
	defer func() { isPrivileged = restore }()

	m := NewModel()
	m.w, m.h = 160, 48
	info := probe.ConnInfo{Conns: []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:1", Remote: "1.1.1.1:443"}}}

	now := time.Now()
	m.applyConnInfo(info, now)
	m.applyConnInfo(info, now.Add(2*time.Second))

	n := 0
	for _, e := range m.events {
		if e.Kind == "attribution_limited" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("recorded the attribution note %d times, want 1", n)
	}
}

func TestInterfaceAttributionAndFilter(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabConns
	m.lastSnap = probe.NetSnapshot{Ifaces: []probe.IfaceInfo{
		{Name: "eth0", Addrs: []string{"192.168.1.5/24"}},
		{Name: "tun0", Addrs: []string{"10.8.0.2/24"}},
	}}

	lan := conn(1, "curl", "192.168.1.5:5000", "1.1.1.1:443", 0, 0)
	vpn := conn(2, "curl", "10.8.0.2:6000", "2.2.2.2:443", 0, 0)
	m = feed(t, m, nil, []probe.Conn{lan, vpn})

	if got := m.ifaceFor(lan); got != "eth0" {
		t.Errorf("LAN connection attributed to %q, want eth0", got)
	}
	if got := m.ifaceFor(vpn); got != "tun0" {
		t.Errorf("VPN connection attributed to %q, want tun0", got)
	}
	if !m.multiIface() {
		t.Error("two interfaces in use should show the IFACE column")
	}
	if !strings.Contains(m.renderConnsText(), "tun0") {
		t.Errorf("interface column missing:\n%s", m.renderConnsText())
	}

	// i cycles: all -> eth0 -> tun0 -> all.
	model, _ := m.Update(key("i"))
	m = model.(Model)
	if m.ifaceFilter != "eth0" {
		t.Fatalf("first press selected %q, want eth0", m.ifaceFilter)
	}
	if rows := m.filteredConns(); len(rows) != 1 || rows[0].Local != lan.Local {
		t.Errorf("eth0 filter kept %d rows, want just the LAN connection", len(rows))
	}
	if !strings.Contains(m.filterLabel(), "eth0") {
		t.Errorf("filter label = %q", m.filterLabel())
	}

	model, _ = m.Update(key("i"))
	m = model.(Model)
	if m.ifaceFilter != "tun0" {
		t.Fatalf("second press selected %q, want tun0", m.ifaceFilter)
	}
	model, _ = m.Update(key("i"))
	m = model.(Model)
	if m.ifaceFilter != "" {
		t.Errorf("third press selected %q, want back to all", m.ifaceFilter)
	}
	if len(m.filteredConns()) != 2 {
		t.Error("clearing the filter should show every connection again")
	}
}
