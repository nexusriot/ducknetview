package ui

import (
	"fmt"
	"os"
)

// Every table in the tool answers "which process", and unprivileged it
// frequently cannot: /proc/<pid>/fd is readable only by the process's owner,
// so another user's sockets arrive with no PID attached and the PROCESS column
// shows a dash. Nothing said so, which made the tool look wrong rather than
// restricted — the single most likely thing for somebody to file a bug about.

// unattributedSockets counts the current sockets with no owning process. It is
// always zero for root, which can see every /proc entry there is.
func (m Model) unattributedSockets() int {
	if isPrivileged() {
		// A socket root cannot attribute has genuinely lost its process (a
		// kernel socket, or one whose owner exited): nothing to suggest.
		return 0
	}
	n := 0
	for _, c := range m.conns {
		if c.PID <= 0 {
			n++
		}
	}
	for _, p := range m.ports {
		if p.PID <= 0 {
			n++
		}
	}
	return n
}

// attributionNote is the one-line explanation shown on the Processes tab, "" when
// everything on screen has an owner.
func (m Model) attributionNote() string {
	n := m.unattributedSockets()
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d %s owned by another user — run as root for full attribution",
		n, plural(n, "socket"))
}

// isPrivileged reports whether this process can read every other process's
// /proc entry. A variable so a test can exercise both sides of the hint.
var isPrivileged = func() bool { return os.Geteuid() == 0 }

// noteAttribution records the limitation once per session, so a headless
// session's log explains its own dashes too. Repeating it every poll would be
// noise: the answer never changes within a session.
func (m *Model) noteAttribution(rec *eventRecorder) {
	if m.attribNoted {
		return
	}
	note := m.attributionNote()
	if note == "" {
		return
	}
	m.attribNoted = true
	rec.add(levelInfo, "attribution_limited", note,
		"sockets belonging to other users have no PID, so they cannot be named or signalled")
}

// Containers live in network namespaces of their own, and seeing into them
// needs privileges an ordinary user lacks: their processes' namespace links
// are unreadable, and their byte counters need setns. The rows that can be
// seen are still shown; noteNetNS says once why the rest are missing.

// noteNetNS records the namespace limitation once per session, for the same
// reason noteAttribution does: it does not change between polls.
func (m *Model) noteNetNS(rec *eventRecorder) {
	if m.netnsNoted || m.netns.Limited == "" {
		return
	}
	m.netnsNoted = true
	rec.add(levelInfo, "netns_limited", m.netns.Limited,
		"sockets inside containers and other network namespaces may be missing or show no throughput")
}
