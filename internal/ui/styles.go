package ui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	subtleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	boxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57"))
	cursorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213")) // row cursor marker

	stateEstabStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))  // green
	stateListenStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))  // cyan
	statePendStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // yellow (handshake/in-flight)

	newConnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true) // connection new since last poll

	scopePublicStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("208")) // routable / internet
	scopePrivateStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245")) // RFC1918 / ULA / CGNAT
	scopeLocalStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // loopback / link-local

	exposedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))            // listener reachable off-host
	lanStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("179"))            // listener on a private address
	watchStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("201")).Bold(true) // watchlist hit
)

// exposureStyle colors a listening socket's bind address by reachability (see
// probe.ListenerExposure) so off-host-reachable listeners stand out.
func exposureStyle(exposure string) lipgloss.Style {
	switch exposure {
	case "exposed":
		return exposedStyle
	case "lan":
		return lanStyle
	case "local":
		return scopeLocalStyle
	default:
		return lipgloss.NewStyle()
	}
}

// scopeStyle colors a remote address by its reachability scope (see
// probe.IPScope) so routable/internet peers stand out from local traffic.
func scopeStyle(scope string) lipgloss.Style {
	switch scope {
	case "public":
		return scopePublicStyle
	case "private", "cgnat":
		return scopePrivateStyle
	case "local", "link":
		return scopeLocalStyle
	default:
		return lipgloss.NewStyle()
	}
}

// stateStyle maps a TCP/UDP connection state to a display style.
// Established traffic is highlighted, handshake/in-flight states are warned,
// and closing/teardown states are dimmed so the active rows stand out.
func stateStyle(state string) lipgloss.Style {
	switch state {
	case "ESTABLISHED":
		return stateEstabStyle
	case "LISTEN":
		return stateListenStyle
	case "SYN_SENT", "SYN_RECV":
		return statePendStyle
	case "TIME_WAIT", "CLOSE_WAIT", "CLOSE", "CLOSING",
		"FIN_WAIT1", "FIN_WAIT2", "LAST_ACK", "NONE", "-", "":
		return subtleStyle
	default:
		return lipgloss.NewStyle()
	}
}
