package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// The socket views answer "who is talking"; this tab answers "how would
// anything leave here at all" — the default route, the first hop, who resolves
// names, and which machines the host has actually spoken to on the LAN.

// hostStateInterval is how often the routing table, neighbours, resolvers and
// radio state are re-read. All of it changes rarely — a VPN coming up is the
// interesting case — so it is deliberately lazier than the socket views.
const hostStateInterval = 10 * time.Second

// hostStateMsg carries one refresh of the host's network configuration.
type hostStateMsg struct {
	routes     []probe.Route
	neighbours []probe.Neighbour
	resolvers  []probe.Resolver
	wifi       map[string]probe.WifiLink
	err        error
}

func fetchHostStateCmd() tea.Cmd {
	return func() tea.Msg {
		msg := hostStateMsg{}

		routes, err := probe.Routes()
		if err != nil {
			msg.err = err
		}
		msg.routes = routes

		// A missing neighbour or resolver table is worth reporting but must not
		// hide the rest.
		if ns, err := probe.Neighbours(); err == nil {
			msg.neighbours = ns
		} else if msg.err == nil {
			msg.err = err
		}
		if rs, err := probe.Resolvers(); err == nil {
			msg.resolvers = rs
		} else if msg.err == nil {
			msg.err = err
		}
		if w, err := probe.WifiLinks(); err == nil {
			msg.wifi = w
		}
		return msg
	}
}

// gatewayLine summarises the default route for the Overview.
func (m Model) gatewayLine() string {
	if m.snapshotOnly {
		return subtleStyle.Render("Gateway: not carried in a snapshot")
	}
	def, ok := probe.DefaultRoute(m.routes)
	if !ok {
		return "Gateway: " + warnStyle.Render("no default route")
	}
	line := fmt.Sprintf("Gateway: %s via %s", okStyle.Render(def.Gateway), def.Iface)

	var servers []string
	for _, r := range m.resolvers {
		label := r.Address
		if r.Stub {
			label += subtleStyle.Render(" (stub)")
		}
		servers = append(servers, label)
	}
	if len(servers) > 0 {
		line += "   DNS: " + strings.Join(servers, ", ")
	}
	return line
}

func (m Model) renderRoutesText() string {
	var b strings.Builder

	w := m.routesVP.Width
	if w <= 0 {
		w = 120
	}

	if m.snapshotOnly {
		return "This is a snapshot of another host.\n\n" +
			subtleStyle.Render("Routes, neighbours, nameservers and radio state are read from /proc, so they\n"+
				"describe the machine the tool is running on. Showing this host's routing\n"+
				"table next to another host's sockets would be worse than showing none, so\n"+
				"the snapshot format does not carry them and this tab stays empty.")
	}

	if m.hostStateErr != nil {
		b.WriteString(errStyle.Render("Could not read the host's network configuration: "+m.hostStateErr.Error()) + "\n\n")
	}

	// Default route first: it is the answer to the most common question.
	b.WriteString(titleStyle.Render("Default route") + "\n")
	if def, ok := probe.DefaultRoute(m.routes); ok {
		b.WriteString(fmt.Sprintf("  %s via %s, metric %d\n",
			okStyle.Render(def.Gateway), def.Iface, def.Metric))
	} else {
		b.WriteString("  " + warnStyle.Render("none — this host cannot reach anything off-link") + "\n")
	}

	b.WriteString("\n" + titleStyle.Render("Nameservers") + "\n")
	if len(m.resolvers) == 0 {
		b.WriteString("  " + subtleStyle.Render("none configured") + "\n")
	}
	for _, r := range m.resolvers {
		note := ""
		if r.Stub {
			// Worth flagging: the real upstream is configured somewhere else.
			note = subtleStyle.Render("  local stub resolver — upstream is configured elsewhere")
		}
		b.WriteString("  " + r.Address + note + "\n")
	}

	b.WriteString("\n" + titleStyle.Render(fmt.Sprintf("Routes (%d)", len(m.routes))) + "\n")
	b.WriteString("  " + subtleStyle.Render(fmt.Sprintf("%-34s %-24s %-10s %s",
		"DESTINATION", "GATEWAY", "IFACE", "METRIC")) + "\n")
	for _, r := range m.routes {
		gw := r.Gateway
		if gw == "" {
			gw = subtleStyle.Render("on-link")
		}
		dest := r.Destination
		if r.IsDefault() {
			dest = okStyle.Render(padRight(dest, 34))
		} else {
			dest = padRight(trunc(dest, 34), 34)
		}
		b.WriteString(fmt.Sprintf("  %s %-24s %-10s %d\n",
			dest, trunc(gw, 24), trunc(r.Iface, 10), r.Metric))
	}

	b.WriteString("\n" + titleStyle.Render(fmt.Sprintf("Neighbours (%d)", len(m.neighbours))) + "\n")
	if len(m.neighbours) == 0 {
		b.WriteString("  " + subtleStyle.Render("none — nothing has been spoken to on the LAN yet") + "\n")
	} else {
		b.WriteString("  " + subtleStyle.Render(fmt.Sprintf("%-18s %-19s %-10s %s",
			"ADDRESS", "MAC", "IFACE", "VENDOR")) + "\n")
	}
	for _, n := range m.neighbours {
		mac := n.MAC
		if n.Stale {
			// The kernel has an entry but no confirmed mapping.
			mac = subtleStyle.Render(padRight("incomplete", 19))
		} else {
			mac = padRight(mac, 19)
		}
		b.WriteString(fmt.Sprintf("  %-18s %s %-10s %s\n",
			trunc(n.IP, 18), mac, trunc(n.Iface, 10), n.Vendor))
	}

	b.WriteString("\n" + subtleStyle.Render("IPv6 neighbours are not shown: the kernel keeps them in netlink only, not /proc.") + "\n")

	return b.String()
}

func (m Model) viewRoutes() string {
	routesW := min(m.w-2, 140)

	if m.routesText == "" {
		m.routesText = m.renderRoutesText()
		m.routesVP.SetContent(m.routesText)
	}
	head := subtleStyle.Render(fmt.Sprintf("Host network configuration · re-read every %s · ↑↓ scroll", hostStateInterval))

	return boxStyle.Width(routesW).Height(m.bodyHeight()).Render(head + "\n\n" + m.routesVP.View())
}
