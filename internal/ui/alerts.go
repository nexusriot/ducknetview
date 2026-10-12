package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Thresholds turn the tool from something you watch into something that tells
// you. They are opt-in through the config file, because the right number is
// entirely site-specific.

const (
	// alertRefire is how long the same subject stays quiet after firing. A
	// threshold breach usually spans many polls, and one event per poll would
	// bury everything else.
	alertRefire = 60 * time.Second
	// alertHookTimeout bounds an external hook, so a hanging script cannot
	// accumulate processes for the life of the session.
	alertHookTimeout = 10 * time.Second
)

// alertHookResultMsg reports a failed hook invocation.
type alertHookResultMsg struct {
	err error
}

// noteThroughputAlerts fires events for processes and connections whose current
// rate exceeds the configured thresholds.
func (m *Model) noteThroughputAlerts(conns []probe.Conn, now time.Time) {
	if m.alertProcBps <= 0 && m.alertConnBps <= 0 {
		return
	}
	if m.alertFired == nil {
		m.alertFired = map[string]time.Time{}
	}

	rec := m.newEventRecorder(now)
	defer rec.flush()

	if m.alertProcBps > 0 {
		for pid, r := range m.procRates {
			total := r.rx + r.tx
			if total < m.alertProcBps {
				continue
			}
			name := ""
			if t := m.procTotals[pid]; t != nil {
				name = t.label
			}
			subject := procLabel(pid, name)
			if subject == "" {
				subject = fmt.Sprintf("pid %d", pid)
			}
			if m.shouldFire("proc:"+subject, now) {
				rec.add(levelAlert, "throughput_process", subject,
					fmt.Sprintf("%s over threshold %s",
						probe.HumanBytesPerSec(total), probe.HumanBytesPerSec(m.alertProcBps)))
			}
		}
	}

	if m.alertConnBps > 0 {
		for _, c := range conns {
			r := m.connRate(c)
			total := r.rx + r.tx
			if total < m.alertConnBps {
				continue
			}
			subject := c.Local + " → " + m.dnsDisplay(c.Remote)
			if m.shouldFire("conn:"+subject, now) {
				rec.add(levelAlert, "throughput_connection", subject,
					joinNonEmpty(fmt.Sprintf("%s over threshold %s",
						probe.HumanBytesPerSec(total), probe.HumanBytesPerSec(m.alertConnBps)),
						procLabel(c.PID, c.Process)))
			}
		}
	}

	m.pruneAlertHistory(now)
}

// shouldFire reports whether a subject may raise an alert now, and records the
// firing when it may.
func (m *Model) shouldFire(subject string, now time.Time) bool {
	if last, ok := m.alertFired[subject]; ok && now.Sub(last) < alertRefire {
		return false
	}
	m.alertFired[subject] = now
	return true
}

// pruneAlertHistory drops suppression entries that can no longer suppress
// anything, keeping the map bounded on a long-running session.
func (m *Model) pruneAlertHistory(now time.Time) {
	for subject, at := range m.alertFired {
		if now.Sub(at) > 2*alertRefire {
			delete(m.alertFired, subject)
		}
	}
}

// alertHookCmd runs the configured hook for one alert. The event is passed as
// arguments and environment variables, never through a shell, so a process name
// or remote address can't be interpreted as a command.
func alertHookCmd(hook string, e event) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), alertHookTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, hook, e.Kind, e.Subject, e.Detail)
		cmd.Env = append(os.Environ(),
			"DNV_EVENT_KIND="+e.Kind,
			"DNV_EVENT_LEVEL="+e.Level.String(),
			"DNV_EVENT_SUBJECT="+e.Subject,
			"DNV_EVENT_DETAIL="+e.Detail,
			"DNV_EVENT_AT="+e.At.Format(time.RFC3339),
		)
		if err := cmd.Run(); err != nil {
			return alertHookResultMsg{err: err}
		}
		return alertHookResultMsg{}
	}
}

// drainAlertHooks converts queued alerts into commands. Alerts are queued
// rather than run inline because recordEvent is called from deep inside the
// update path, where there is nowhere to return a command from.
func (m *Model) drainAlertHooks() []tea.Cmd {
	if len(m.pendingHooks) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(m.pendingHooks))
	for _, e := range m.pendingHooks {
		cmds = append(cmds, alertHookCmd(m.alertHook, e))
	}
	m.pendingHooks = nil
	return cmds
}

// WithAlertHook configures a command to run for every alert-level event.
func (m Model) WithAlertHook(path string) Model {
	m.alertHook = path
	return m
}

// alertRules is the set of thresholds beyond throughput. Every one of these
// reads data the probe layer already collects — the point is that a monitor
// should notice them without the user staring at a table.
type alertRules struct {
	rttMs         float64 // round-trip time above this is worth saying
	retransPerMin float64 // retransmits per minute on one socket
	connGrowth    int     // consecutive polls of growth before calling it a leak
	fanoutHosts   int     // distinct new remotes for one process in one poll
	// Link health. Errors and drops were collected and displayed from the
	// start but could not fire anything, which made the interface the one
	// measured thing the tool could not tell you about.
	ifaceDropsPerMin float64
}

func (r alertRules) any() bool {
	return r.rttMs > 0 || r.retransPerMin > 0 || r.connGrowth > 0 || r.fanoutHosts > 0 ||
		r.ifaceDropsPerMin > 0
}

// noteQualityAlerts applies the non-throughput rules. It runs after the rate
// bookkeeping, so the retransmit deltas line up with the same interval.
func (m *Model) noteQualityAlerts(conns []probe.Conn, now time.Time) {
	if !m.alertRules.any() {
		return
	}
	if m.alertFired == nil {
		m.alertFired = map[string]time.Time{}
	}

	rec := m.newEventRecorder(now)
	defer rec.flush()

	dt := now.Sub(m.lastQualitySample).Seconds()
	haveBaseline := !m.lastQualitySample.IsZero() && dt > 0
	curRetrans := make(map[string]uint32, len(conns))

	// Distinct remotes reached by each process in this poll, for fan-out.
	fanout := map[int32]map[string]bool{}
	connCounts := map[int32]int{}

	for _, c := range conns {
		key := connKey(c)
		curRetrans[key] = c.Retrans
		if c.PID > 0 {
			connCounts[c.PID]++
		}

		if m.alertRules.rttMs > 0 && c.RTT > 0 {
			if ms := float64(c.RTT) / float64(time.Millisecond); ms >= m.alertRules.rttMs {
				subject := c.Local + " → " + m.dnsDisplay(c.Remote)
				if m.shouldFire("rtt:"+subject, now) {
					rec.add(levelWarn, "latency", subject,
						joinNonEmpty(fmt.Sprintf("RTT %s over threshold %.0fms", c.RTT.Round(time.Millisecond), m.alertRules.rttMs),
							procLabel(c.PID, c.Process)))
				}
			}
		}

		if m.alertRules.retransPerMin > 0 && haveBaseline {
			if prev, ok := m.prevRetrans[key]; ok {
				if delta := float64(deltaBytes(uint64(c.Retrans), uint64(prev))); delta > 0 {
					perMin := delta / dt * 60
					if perMin >= m.alertRules.retransPerMin {
						subject := c.Local + " → " + m.dnsDisplay(c.Remote)
						if m.shouldFire("retrans:"+subject, now) {
							rec.add(levelWarn, "packet_loss", subject,
								joinNonEmpty(fmt.Sprintf("%.0f retransmits/min over threshold %.0f", perMin, m.alertRules.retransPerMin),
									procLabel(c.PID, c.Process)))
						}
					}
				}
			}
		}

		if m.alertRules.fanoutHosts > 0 && c.PID > 0 && m.newConns[connKey(c)] {
			if fanout[c.PID] == nil {
				fanout[c.PID] = map[string]bool{}
			}
			fanout[c.PID][stripPort(c.Remote)] = true
		}
	}

	// Fan-out: one process reaching many new hosts at once is what a scan (or a
	// crawler, or a peer-to-peer client) looks like from the inside.
	for pid, hosts := range fanout {
		if len(hosts) < m.alertRules.fanoutHosts {
			continue
		}
		subject := procLabel(pid, m.procNameFor(pid))
		if m.shouldFire("fanout:"+subject, now) {
			rec.add(levelAlert, "fanout", subject,
				fmt.Sprintf("%d new remote hosts in one poll, threshold %d", len(hosts), m.alertRules.fanoutHosts))
		}
	}

	// Connection leak: a count that only ever goes up. One poll of growth is
	// normal; a dozen in a row is a process that never closes anything.
	if m.alertRules.connGrowth > 0 {
		if m.connGrowth == nil {
			m.connGrowth = map[int32]int{}
		}
		for pid, count := range connCounts {
			prev, seen := m.prevConnCounts[pid]
			switch {
			case seen && count > prev:
				m.connGrowth[pid]++
			case seen && count < prev:
				m.connGrowth[pid] = 0
			}
			if m.connGrowth[pid] >= m.alertRules.connGrowth {
				subject := procLabel(pid, m.procNameFor(pid))
				if m.shouldFire("leak:"+subject, now) {
					rec.add(levelWarn, "conn_growth", subject,
						fmt.Sprintf("connections grew for %d consecutive polls, now %d — possible leak",
							m.connGrowth[pid], count))
				}
				m.connGrowth[pid] = 0
			}
		}
		// Forget processes that are gone, so the map cannot grow forever.
		for pid := range m.connGrowth {
			if _, ok := connCounts[pid]; !ok {
				delete(m.connGrowth, pid)
			}
		}
	}

	m.prevRetrans = curRetrans
	m.prevConnCounts = connCounts
	m.lastQualitySample = now
	// The throughput path prunes too, but it returns early when no throughput
	// threshold is set — a host with only quality rules would never prune.
	m.pruneAlertHistory(now)
}

// procNameFor is the best name known for a PID, from this poll's process list
// or the session totals if the process has since gone.
func (m Model) procNameFor(pid int32) string {
	for _, p := range m.procs {
		if p.PID == pid {
			return p.Name
		}
	}
	if t := m.procTotals[pid]; t != nil {
		return t.label
	}
	return ""
}

// noteIfaceAlerts fires on a link that is dropping or erroring packets. The
// counters are cumulative since boot, so the rule is a rate: a link that lost
// a handful of packets last week is not a problem, and one losing them now is.
func (m *Model) noteIfaceAlerts(snap probe.NetSnapshot) {
	if m.alertRules.ifaceDropsPerMin <= 0 {
		return
	}
	if m.alertFired == nil {
		m.alertFired = map[string]time.Time{}
	}

	now := snap.TakenAt
	if now.IsZero() {
		now = time.Now()
	}
	cur := make(map[string]uint64, len(snap.Ifaces))
	for _, ii := range snap.Ifaces {
		cur[ii.Name] = ii.LinkProblems()
	}

	dt := now.Sub(m.lastIfaceSample).Seconds()
	// The first sample is the baseline: without one, every counter accumulated
	// since boot would read as having happened in this instant.
	if m.lastIfaceSample.IsZero() || dt <= 0 {
		m.prevIfaceProblems, m.lastIfaceSample = cur, now
		return
	}

	rec := m.newEventRecorder(now)
	defer rec.flush()

	for _, ii := range snap.Ifaces {
		prev, ok := m.prevIfaceProblems[ii.Name]
		if !ok {
			continue
		}
		delta := deltaBytes(cur[ii.Name], prev)
		if delta == 0 {
			continue
		}
		perMin := float64(delta) / dt * 60
		if perMin < m.alertRules.ifaceDropsPerMin {
			continue
		}
		if m.shouldFire("iface:"+ii.Name, now) {
			rec.add(levelWarn, "link_errors", ii.Name,
				fmt.Sprintf("%.0f errors+drops/min over threshold %.0f",
					perMin, m.alertRules.ifaceDropsPerMin))
		}
	}

	m.prevIfaceProblems, m.lastIfaceSample = cur, now
	m.pruneAlertHistory(now)
}
