package ui

import (
	"net"
	"regexp"
	"strings"
)

// watchList matches remote addresses against user-supplied rules (from config).
// Each entry is interpreted as a CIDR, then a bare IP, then — failing those — a
// regular expression tested against the raw IP and any resolved hostname.
type watchList struct {
	ips  []net.IP
	nets []*net.IPNet
	res  []*regexp.Regexp
}

// parseWatchlist builds a matcher from raw config entries. Unparseable regex
// entries are skipped. Returns nil when there are no usable rules, so callers
// can cheaply treat "no watchlist" as a nil pointer.
func parseWatchlist(entries []string) *watchList {
	wl := &watchList{}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(e); err == nil {
			wl.nets = append(wl.nets, n)
			continue
		}
		if ip := net.ParseIP(e); ip != nil {
			wl.ips = append(wl.ips, ip)
			continue
		}
		if re, err := regexp.Compile(e); err == nil {
			wl.res = append(wl.res, re)
		}
	}
	if len(wl.ips) == 0 && len(wl.nets) == 0 && len(wl.res) == 0 {
		return nil
	}
	return wl
}

// match reports whether a remote IP (and optional resolved hostname) is on the
// watchlist. Safe to call on a nil receiver.
func (w *watchList) match(ip, host string) bool {
	if w == nil {
		return false
	}
	addr := net.ParseIP(ip)
	if addr != nil {
		for _, x := range w.ips {
			if x.Equal(addr) {
				return true
			}
		}
		for _, n := range w.nets {
			if n.Contains(addr) {
				return true
			}
		}
	}
	for _, re := range w.res {
		if re.MatchString(ip) || (host != "" && re.MatchString(host)) {
			return true
		}
	}
	return false
}

// toggleWatch adds a bare IP to the watchlist, or removes it if it is already
// listed verbatim. The watchlist used to be editable only by hand, which meant
// noticing a suspicious remote and then leaving the tool to act on it.
func (m *Model) toggleWatch(ip string) (string, bool) {
	if net.ParseIP(ip) == nil {
		return "Not a usable address to watch: " + ip, false
	}

	for i, entry := range m.watchlistRaw {
		if strings.TrimSpace(entry) == ip {
			m.watchlistRaw = append(m.watchlistRaw[:i:i], m.watchlistRaw[i+1:]...)
			m.watch = parseWatchlist(m.watchlistRaw)
			m.saveConfig()
			return "Removed " + ip + " from the watchlist", true
		}
	}

	m.watchlistRaw = append(m.watchlistRaw, ip)
	m.watch = parseWatchlist(m.watchlistRaw)
	m.saveConfig()
	return "Watching " + ip, true
}

// watchTarget returns the remote address the `w` key would act on for the
// current selection, across the flat, grouped and closed connection views.
func (m Model) watchTarget() (string, bool) {
	if m.activeTab != tabConns {
		return "", false
	}
	switch {
	case m.showClosed:
		rows := m.closedRows()
		if m.connsCursor < 0 || m.connsCursor >= len(rows) {
			return "", false
		}
		return stripPort(rows[m.connsCursor].conn.Remote), true
	case m.connsGroup:
		rows := m.filteredGroups()
		if m.connsCursor < 0 || m.connsCursor >= len(rows) {
			return "", false
		}
		return rows[m.connsCursor].ip, true
	default:
		rows := m.filteredConns()
		if m.connsCursor < 0 || m.connsCursor >= len(rows) {
			return "", false
		}
		return stripPort(rows[m.connsCursor].Remote), true
	}
}
