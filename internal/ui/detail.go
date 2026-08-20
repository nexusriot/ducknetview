package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

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
			// A folded group stands for many processes; signalling "the group"
			// would be a guess about which one the user meant.
			if p := rows[m.procsCursor]; p.PID > 0 && p.members <= 1 {
				return p.PID, p.Name, true
			}
		}
	case tabConns:
		// Grouped rows span several processes, and a closed connection's PID
		// may already have been recycled onto an unrelated process — neither is
		// safe to signal.
		if !m.connsGroup && !m.showClosed {
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

// formatPIDs lists a group's PIDs compactly, cut off before it fills the line.
func formatPIDs(pids []int32) string {
	const show = 6
	parts := make([]string, 0, show+1)
	for i, pid := range pids {
		if i == show {
			parts = append(parts, fmt.Sprintf("+%d more", len(pids)-show))
			break
		}
		parts = append(parts, fmt.Sprintf("%d", pid))
	}
	return strings.Join(parts, " ")
}

// timeOrDash formats a timestamp that may not have been recorded.
func timeOrDash(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
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
		if m.offBaseline(p) {
			kv("Baseline", "NOT IN BASELINE")
		}
		kv("PID", fmt.Sprintf("%d", p.PID))
		kv("Process", p.Process)
		writeProcDetail(&b, kv, p.PID)

	case tabProcs:
		rows := m.filteredProcs()
		if m.procsCursor < 0 || m.procsCursor >= len(rows) {
			return "", false
		}
		p := rows[m.procsCursor]
		if p.members > 1 {
			b.WriteString(titleStyle.Render("Process group") + "\n\n")
			kv("Name", p.Name)
			kv("Processes", fmt.Sprintf("%d (%s)", p.members, formatPIDs(p.pids)))
		} else {
			b.WriteString(titleStyle.Render("Process") + "\n\n")
			kv("PID", fmt.Sprintf("%d", p.PID))
			kv("Name", p.Name)
		}
		kv("User", p.User)
		kv("Container", p.Container)
		kv("CPU %", fmt.Sprintf("%.1f", p.CPU))
		kv("Mem %", fmt.Sprintf("%.1f", p.Mem))
		if m.hasThroughput {
			r := m.rowRate(p)
			t := m.rowTotal(p)
			kv("RX", m.rate(r.rx)+"   session "+probe.HumanBytes(t.rx))
			kv("TX", m.rate(r.tx)+"   session "+probe.HumanBytes(t.tx))
			writeRateSparks(&b, m.rowHistory(p), m.detailWidth())
		}
		kv("Conns", fmt.Sprintf("%d", p.ConnCount))
		kv("Listening", fmt.Sprintf("%d", p.ListenCount))
		if p.members <= 1 {
			if d, err := probe.ProcessDetail(p.PID); err == nil {
				kv("PPID", fmt.Sprintf("%d", d.Ppid))
				kv("Command", d.Cmdline)
			}
		}

	case tabConns:
		if m.showClosed {
			rows := m.closedRows()
			if m.connsCursor < 0 || m.connsCursor >= len(rows) {
				return "", false
			}
			cc := rows[m.connsCursor]
			c := cc.conn
			b.WriteString(titleStyle.Render("Closed connection") + "\n\n")
			kv("Protocol", c.Proto)
			kv("Local", c.Local)
			kv("Remote", m.dnsDisplay(c.Remote))
			kv("Scope", probe.IPScope(stripPort(c.Remote)))
			kv("Service", probe.ServiceName(portOf(c.Remote), c.Proto))
			kv("Last state", c.State)
			kv("Opened", timeOrDash(cc.firstSeen))
			kv("Closed", cc.closedAt.Format("2006-01-02 15:04:05"))
			kv("Lifetime", cc.lifetimeLabel())
			kv("RX", probe.HumanBytes(cc.totals.rx))
			kv("TX", probe.HumanBytes(cc.totals.tx))
			if c.Retrans > 0 {
				kv("Retrans", fmt.Sprintf("%d", c.Retrans))
			}
			kv("PID", fmt.Sprintf("%d", c.PID))
			kv("Process", c.Process)
			kv("User", c.User)
			break
		}
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
			kv("Scope", probe.IPScope(g.ip))
			kv("Conns", fmt.Sprintf("%d", g.count))
			kv("States", formatStates(g.states))
			kv("Processes", strings.Join(g.procs, ", "))
			if m.hasThroughput {
				r := m.groupRate(g)
				t := m.hostTotal(g.ip)
				kv("RX", m.rate(r.rx)+"   session "+probe.HumanBytes(t.rx))
				kv("TX", m.rate(r.tx)+"   session "+probe.HumanBytes(t.tx))
			}
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
			kv("Age", formatAge(time.Now(), m.connFirstSeen[connKey(c)]))
			if m.hasThroughput {
				r := m.connRate(c)
				kv("RX", m.rate(r.rx)+"  (total "+probe.HumanBytes(c.RxBytes)+")")
				kv("TX", m.rate(r.tx)+"  (total "+probe.HumanBytes(c.TxBytes)+")")
				if c.RTT > 0 {
					rtt := c.RTT.Round(time.Millisecond / 10).String()
					if c.MinRTT > 0 {
						rtt += "   min " + c.MinRTT.Round(time.Millisecond/10).String()
					}
					kv("RTT", rtt)
				}
				// Retransmits mean loss on the path; a standing send queue means
				// the peer is not draining what we hand it.
				quality := fmt.Sprintf("%d retransmits · recv-q %d · send-q %d", c.Retrans, c.RecvQ, c.SendQ)
				if !c.SocketHealthy() {
					quality = warnStyle.Render(quality)
				}
				kv("Quality", quality)
				writeRateSparks(&b, m.connRateHist[connKey(c)], m.detailWidth())
			}
			kv("PID", fmt.Sprintf("%d", c.PID))
			kv("Process", c.Process)
			writeProcDetail(&b, kv, c.PID)
		}

	case tabEvents:
		rows := m.eventRows()
		if m.eventsCursor < 0 || m.eventsCursor >= len(rows) {
			return "", false
		}
		e := rows[m.eventsCursor]
		b.WriteString(titleStyle.Render("Event") + "\n\n")
		kv("Time", e.At.Format("2006-01-02 15:04:05 -07:00"))
		kv("Level", e.Level.String())
		kv("Kind", e.Kind)
		kv("Subject", e.Subject)
		kv("Detail", e.Detail)

	default:
		return "", false
	}

	b.WriteString("\n" + subtleStyle.Render("↑↓ scroll • y copy • any other key closes"))
	return b.String(), true
}

// writeRateSparks draws the recent throughput of the selected row, so a
// connection that is stalling (or that just finished a burst) is visible in the
// overlay rather than only as a single instantaneous number.
func writeRateSparks(b *strings.Builder, h *rateHistory, width int) {
	if h == nil || len(h.rx) < 2 {
		return
	}
	chartW := max(10, min(60, width-16))
	b.WriteString("  " + okStyle.Render(padRight("RX chart", 10)) +
		Spark(h.rx, chartW) + "  " + subtleStyle.Render("peak "+probe.CompactRate(maxOf(h.rx))) + "\n")
	b.WriteString("  " + okStyle.Render(padRight("TX chart", 10)) +
		Spark(h.tx, chartW) + "  " + subtleStyle.Render("peak "+probe.CompactRate(maxOf(h.tx))) + "\n")
}

// writeProcDetail appends the owning user, container and command line for a
// PID, fetched on demand (skipped silently if the process is gone or
// unreadable).
func writeProcDetail(b *strings.Builder, kv func(k, v string), pid int32) {
	if pid <= 0 {
		return
	}
	d, err := probe.ProcessDetail(pid)
	if err != nil {
		return
	}
	kv("User", d.User)
	if d.Container != "" {
		kv("Container", d.Container)
	}
	kv("Command", d.Cmdline)
}

// detailWidth is the text width available inside the detail box.
func (m Model) detailWidth() int {
	return max(20, min(m.w-2, 120)-2)
}

// setDetail installs content in the overlay, wrapping it to the box width so
// long command lines are readable in full instead of being cut off, and resets
// the scroll position.
func (m *Model) setDetail(text string) {
	m.detailText = text
	m.detailVP.Width = m.detailWidth()
	m.detailVP.Height = max(3, m.bodyHeight()-2)
	m.detailVP.SetContent(lipgloss.NewStyle().Width(m.detailVP.Width).Render(text))
	m.detailVP.SetYOffset(0)
}

// refreshDetail re-renders the open overlay against the latest poll, so a
// connection's throughput and state keep updating while it is being read.
func (m *Model) refreshDetail() {
	if !m.showDetail {
		return
	}
	txt, ok := m.detailContent()
	if !ok {
		return
	}
	offset := m.detailVP.YOffset
	m.setDetail(txt)
	m.detailVP.SetYOffset(offset)
}

func (m Model) renderDetail() string {
	return boxStyle.Width(min(m.w-2, 120)).Height(m.bodyHeight()).Render(m.detailVP.View())
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

func (m Model) renderQuitConfirm() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Quit ducknetview?") + "\n\n")
	if m.sessionTotal.any() {
		// Worth saying: the session totals and the event log do not survive.
		b.WriteString(subtleStyle.Render(fmt.Sprintf(
			"  This session accounted %s in and %s out, and %d events.\n\n",
			probe.HumanBytes(m.sessionTotal.rx), probe.HumanBytes(m.sessionTotal.tx), len(m.events))))
	}
	b.WriteString("  " + okStyle.Render("y") + subtleStyle.Render(" quit") + "   " +
		okStyle.Render("any other key") + subtleStyle.Render(" stay"))
	return boxStyle.Width(min(m.w-2, 80)).Height(m.bodyHeight()).Render(b.String())
}
