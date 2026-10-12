package ui

import (
	"sort"

	"github.com/charmbracelet/lipgloss"
)

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

// Every colour in the tool is defined above and nowhere else, which makes a
// theme a matter of reassigning those variables rather than touching any
// renderer. NO_COLOR is still honoured independently: it turns the whole
// profile off, whatever theme is selected.

// palette is one theme's colours.
type palette struct {
	subtle, ok, warn, err     string
	selectedFg, selectedBg    string
	cursor, newConn           string
	estab, listen, pending    string
	scopePublic, scopePrivate string
	scopeLocal, exposed, lan  string
	watch                     string
}

// themes are the selectable palettes. "default" is what the tool has always
// looked like; "contrast" exists because the 240-245 greys that carry most of
// the dimming are illegible on a light terminal or a projector.
var themes = map[string]palette{
	"default": {
		subtle: "241", ok: "42", warn: "214", err: "196",
		selectedFg: "229", selectedBg: "57",
		cursor: "213", newConn: "82",
		estab: "42", listen: "39", pending: "214",
		scopePublic: "208", scopePrivate: "245", scopeLocal: "240",
		exposed: "208", lan: "179", watch: "201",
	},
	"contrast": {
		subtle: "244", ok: "46", warn: "226", err: "201",
		selectedFg: "16", selectedBg: "255",
		cursor: "51", newConn: "46",
		estab: "46", listen: "51", pending: "226",
		scopePublic: "214", scopePrivate: "252", scopeLocal: "244",
		exposed: "214", lan: "226", watch: "201",
	},
	"mono": {
		subtle: "243", ok: "255", warn: "250", err: "255",
		selectedFg: "16", selectedBg: "250",
		cursor: "255", newConn: "255",
		estab: "255", listen: "250", pending: "248",
		scopePublic: "255", scopePrivate: "248", scopeLocal: "243",
		exposed: "255", lan: "250", watch: "255",
	},
}

// defaultTheme is the palette used when the config names none.
const defaultTheme = "default"

// ThemeNames lists the selectable themes, in a stable order.
func ThemeNames() []string {
	out := make([]string, 0, len(themes))
	for name := range themes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// validTheme reports whether a name selects a known palette.
func validTheme(name string) bool {
	_, ok := themes[name]
	return ok
}

// nextTheme cycles to the following palette, so the settings overlay needs no
// list widget for three entries.
func nextTheme(current string) string {
	names := ThemeNames()
	for i, n := range names {
		if n == current {
			return names[(i+1)%len(names)]
		}
	}
	return names[0]
}

// applyTheme reassigns the package's styles. It is global because the styles
// are: lipgloss styles are values, and every renderer reads these variables at
// render time.
func applyTheme(name string) {
	p, ok := themes[name]
	if !ok {
		p = themes[defaultTheme]
	}

	c := func(v string) lipgloss.Color { return lipgloss.Color(v) }
	subtleStyle = subtleStyle.Foreground(c(p.subtle))
	okStyle = okStyle.Foreground(c(p.ok))
	warnStyle = warnStyle.Foreground(c(p.warn))
	errStyle = errStyle.Foreground(c(p.err))
	selectedStyle = selectedStyle.Foreground(c(p.selectedFg)).Background(c(p.selectedBg))
	cursorStyle = cursorStyle.Foreground(c(p.cursor))
	newConnStyle = newConnStyle.Foreground(c(p.newConn))

	stateEstabStyle = stateEstabStyle.Foreground(c(p.estab))
	stateListenStyle = stateListenStyle.Foreground(c(p.listen))
	statePendStyle = statePendStyle.Foreground(c(p.pending))

	scopePublicStyle = scopePublicStyle.Foreground(c(p.scopePublic))
	scopePrivateStyle = scopePrivateStyle.Foreground(c(p.scopePrivate))
	scopeLocalStyle = scopeLocalStyle.Foreground(c(p.scopeLocal))

	exposedStyle = exposedStyle.Foreground(c(p.exposed))
	lanStyle = lanStyle.Foreground(c(p.lan))
	watchStyle = watchStyle.Foreground(c(p.watch))
}
