package ui

import (
	"github.com/nexusriot/ducknetview/internal/probe"
)

// A remote gets a scope and, if it has a PTR record, a name. Most cloud
// addresses have neither anything useful in DNS nor any clue who runs them, so
// "who is 13.107.42.14" still meant reaching for another tool. The answer is
// the address's autonomous system, which is a static table — opt-in, read from
// disk, and no lookup ever leaves the host.

// loadASNDB loads the configured ownership table, if there is one. A failure is
// recorded rather than fatal: the column simply does not appear, and the status
// line says why.
func (m *Model) loadASNDB() {
	if m.asnPath == "" {
		m.asnDB, m.asnErr = nil, nil
		return
	}
	// A reload re-applies the whole config, and the table is the one setting
	// that costs hundreds of megabytes and seconds of a blocked update loop to
	// re-read. An unchanged path already has its answer loaded.
	if m.asnDB != nil && m.asnDB.Source == m.asnPath {
		m.asnErr = nil
		return
	}
	db, err := probe.LoadASNDB(m.asnPath)
	if err != nil {
		m.asnDB, m.asnErr = nil, err
		return
	}
	m.asnDB, m.asnErr = db, nil
}

// ownerOf is the network that announces a remote address, "" when there is no
// database or no answer (which is every private address).
func (m Model) ownerOf(remote string) string {
	if m.asnDB.Len() == 0 {
		return ""
	}
	info, ok := m.asnDB.Lookup(stripPort(remote))
	if !ok {
		return ""
	}
	return info.Label()
}

// ownerDetail is the fuller form for the detail overlay, where there is room
// for the AS number and country alongside the name.
func (m Model) ownerDetail(remote string) string {
	if m.asnDB.Len() == 0 {
		return ""
	}
	info, ok := m.asnDB.Lookup(stripPort(remote))
	if !ok {
		return ""
	}
	out := info.Label()
	if info.Number > 0 && info.Name != "" {
		out += " (AS" + itoa(info.Number) + ")"
	}
	if info.Country != "" {
		out += " · " + info.Country
	}
	return out
}

// itoa avoids importing strconv for one conversion in a file that otherwise
// does not need it.
func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
