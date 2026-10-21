package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestRoutesTabRendersHostConfiguration(t *testing.T) {
	m := routesModel()
	m.activeTab = tabRoutes
	m.routes = []probe.Route{
		{Destination: "default", Gateway: "192.168.1.1", Iface: "eth0", Metric: 100},
		{Destination: "192.168.1.0/24", Iface: "eth0"},
	}
	m.neighbours = []probe.Neighbour{
		{IP: "192.168.1.1", MAC: "b8:27:eb:11:22:33", Iface: "eth0", Vendor: "Raspberry Pi", State: "reachable"},
		{IP: "192.168.1.9", MAC: "00:00:00:00:00:00", Iface: "eth0", Stale: true, State: "incomplete"},
		{IP: "fe80::e21f:2bff:fe51:1c31", MAC: "e0:1f:2b:51:1c:31", Iface: "eth0", IPv6: true, State: "stale"},
	}
	m.resolvers = []probe.Resolver{{Address: "127.0.0.53", Stub: true}, {Address: "1.1.1.1"}}

	text := stripANSI(m.renderRoutesText())
	for _, want := range []string{"Default route", "192.168.1.1 via eth0", "Nameservers",
		"local stub resolver", "1.1.1.1", "Routes (2)", "192.168.1.0/24", "on-link",
		"Neighbours (3)", "Raspberry Pi", "incomplete"} {
		if !strings.Contains(text, want) {
			t.Errorf("routes view missing %q:\n%s", want, text)
		}
	}
	// IPv6 neighbours used to be a documented gap; they come from netlink now,
	// so the address, its state and the family split all have to be there.
	for _, want := range []string{"fe80::e21f:2bff:fe51:1c31", "stale", "2 IPv4 · 1 IPv6"} {
		if !strings.Contains(text, want) {
			t.Errorf("routes view missing IPv6 neighbour detail %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "IPv6 neighbours are not shown") {
		t.Error("the view still claims IPv6 neighbours are not covered")
	}

	if got := stripANSI(m.gatewayLine()); !strings.Contains(got, "192.168.1.1") || !strings.Contains(got, "DNS") {
		t.Errorf("gateway line = %q", got)
	}
	if !strings.Contains(stripANSI(m.viewRoutes()), "Default route") {
		t.Error("viewRoutes should render the text")
	}
}

func TestRoutesTabWithNoDefaultRoute(t *testing.T) {
	m := routesModel()
	m.routes = []probe.Route{{Destination: "10.0.0.0/8", Iface: "eth0"}}

	if !strings.Contains(stripANSI(m.renderRoutesText()), "cannot reach anything off-link") {
		t.Error("a host with no default route should be told so plainly")
	}
	if !strings.Contains(stripANSI(m.gatewayLine()), "no default route") {
		t.Errorf("gateway line = %q", stripANSI(m.gatewayLine()))
	}
}

func TestRoutesTabIsReachableAndScrolls(t *testing.T) {
	m := routesModel()
	model, _ := m.Update(key("7"))
	m = model.(Model)
	if m.activeTab != tabRoutes {
		t.Fatalf("7 selected tab %v, want routes", m.activeTab)
	}
	// It scrolls but has no cursor to move.
	if m.hasRowCursor() {
		t.Error("the routes tab should not claim a row cursor")
	}
	m.routes = make([]probe.Route, 80)
	for i := range m.routes {
		m.routes[i] = probe.Route{Destination: "10.0.0.0/8", Iface: "eth0"}
	}
	m.routesText = m.renderRoutesText()
	m.routesVP.SetContent(m.routesText)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if model.(Model).routesVP.YOffset == 0 {
		t.Error("page-down should scroll the routes viewport")
	}
}
