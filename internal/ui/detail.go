package ui

import (
	"fmt"
	"strings"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// selectedPID returns the PID (and best-effort name) of the highlighted row on
// a table tab, when it maps to a single process. Grouped connections span
// multiple processes, so they return ok=false.
func (m Model) selectedPID() (int32, string, bool) {
	switch m.activeTab {
	case tabPorts:
		rows := m.filteredPorts()
		if m.portsCursor >= 0 && m.portsCursor < len(rows) {
			if p := rows[m.portsCursor]; p.PID > 0 {
				return p.PID, p.Process, true
			}
		}
	case tabProcs:
		rows := m.filteredProcs()
		if m.procsCursor >= 0 && m.procsCursor < len(rows) {
			if p := rows[m.procsCursor]; p.PID > 0 {
				return p.PID, p.Name, true
			}
		}
	case tabConns:
		if !m.connsGroup {
			rows := m.filteredConns()
			if m.connsCursor >= 0 && m.connsCursor < len(rows) {
				if c := rows[m.connsCursor]; c.PID > 0 {
					return c.PID, c.Process, true
				}
			}
		}
	}
	return 0, "", false
}

// detailContent builds the multi-line body for the row-detail overlay for the
// current selection. ok is false when there is nothing selected.
func (m Model) detailContent() (string, bool) {
	var b strings.Builder
	kv := func(k, v string) {
		if v == "" {
			v = "-"
		}
		b.WriteString("  " + okStyle.Render(padRight(k, 10)) + v + "\n")
	}

	switch m.activeTab {
	case tabPorts:
		rows := m.filteredPorts()
		if m.portsCursor < 0 || m.portsCursor >= len(rows) {
			return "", false
		}
		p := rows[m.portsCursor]
		local := p.Local
		if local == ":" || local == "0.0.0.0:0" {
			local = "-"
		}
		b.WriteString(titleStyle.Render("Listening port") + "\n\n")
		kv("Protocol", p.Proto)
		kv("Local", local)
		if local != "-" {
			kv("Exposure", probe.ListenerExposure(stripPort(p.Local)))
		}
		kv("Service", probe.ServiceName(portOf(p.Local), p.Proto))
		kv("PID", fmt.Sprintf("%d", p.PID))
		kv("Process", p.Process)
		writeProcDetail(&b, kv, p.PID)

	case tabProcs:
		rows := m.filteredProcs()
		if m.procsCursor < 0 || m.procsCursor >= len(rows) {
			return "", false
		}
		p := rows[m.procsCursor]
		b.WriteString(titleStyle.Render("Process") + "\n\n")
		kv("PID", fmt.Sprintf("%d", p.PID))
		kv("Name", p.Name)
		kv("User", p.User)
		kv("CPU %", fmt.Sprintf("%.1f", p.CPU))
		kv("Mem %", fmt.Sprintf("%.1f", p.Mem))
		kv("Conns", fmt.Sprintf("%d", p.ConnCount))
		kv("Listening", fmt.Sprintf("%d", p.ListenCount))
		if d, err := probe.ProcessDetail(p.PID); err == nil {
			kv("PPID", fmt.Sprintf("%d", d.Ppid))
			kv("Command", d.Cmdline)
		}

	case tabConns:
		if m.connsGroup {
			rows := m.filteredGroups()
			if m.connsCursor < 0 || m.connsCursor >= len(rows) {
				return "", false
			}
			g := rows[m.connsCursor]
			host := g.ip
			if m.connsRevDNS && m.dnsCache != nil {
				if name, ok := m.dnsCache[g.ip]; ok && name != "" {
					host = name + " (" + g.ip + ")"
				}
			}
			b.WriteString(titleStyle.Render("Remote host group") + "\n\n")
			kv("Host", host)
			kv("Conns", fmt.Sprintf("%d", g.count))
			kv("States", formatStates(g.states))
			kv("Processes", strings.Join(g.procs, ", "))
		} else {
			rows := m.filteredConns()
			if m.connsCursor < 0 || m.connsCursor >= len(rows) {
				return "", false
			}
			c := rows[m.connsCursor]
			b.WriteString(titleStyle.Render("Connection") + "\n\n")
			kv("Protocol", c.Proto)
			kv("Local", c.Local)
			kv("Remote", m.dnsDisplay(c.Remote))
			kv("Scope", probe.IPScope(stripPort(c.Remote)))
			if m.isWatched(c.Remote) {
				kv("Watchlist", "MATCH")
			}
			kv("Service", probe.ServiceName(portOf(c.Remote), c.Proto))
			kv("State", c.State)
			kv("PID", fmt.Sprintf("%d", c.PID))
			kv("Process", c.Process)
			writeProcDetail(&b, kv, c.PID)
		}

	default:
		return "", false
	}

	b.WriteString("\n" + subtleStyle.Render("press any key to close"))
	return b.String(), true
}

// writeProcDetail appends the owning user and command line for a PID, fetched
// on demand (skipped silently if the process is gone or unreadable).
func writeProcDetail(b *strings.Builder, kv func(k, v string), pid int32) {
	if pid <= 0 {
		return
	}
	d, err := probe.ProcessDetail(pid)
	if err != nil {
		return
	}
	kv("User", d.User)
	kv("Command", d.Cmdline)
}

func (m Model) renderDetail() string {
	boxW := min(m.w-2, 120)
	return boxStyle.Width(boxW).Height(m.bodyHeight()).
		Render(hardClipLinesToWidth(m.detailText, boxW-2))
}

func (m Model) renderKillConfirm() string {
	name := m.killName
	if name == "" {
		name = "(unknown)"
	}
	var b strings.Builder
	b.WriteString(warnStyle.Render("Signal process?") + "\n\n")
	b.WriteString(fmt.Sprintf("  Target: PID %s (%s)\n\n",
		titleStyle.Render(fmt.Sprintf("%d", m.killPID)),
		name,
	))
	sigKey := func(k, d string) string { return okStyle.Render(k) + subtleStyle.Render(" "+d) }
	b.WriteString("  " + sigKey("t", "SIGTERM") + "   " +
		sigKey("k", "SIGKILL") + "   " +
		sigKey("h", "SIGHUP") + "   " +
		sigKey("n/esc", "cancel"))
	return boxStyle.Width(min(m.w-2, 80)).Height(m.bodyHeight()).Render(b.String())
}
