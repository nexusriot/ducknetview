package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Host TCP health is about the connections that go wrong before, or after, any
// socket table can show them: a SYN dropped because a service's accept queue
// was full never becomes a row, and a socket the peer closed but the process
// never did sits in CLOSE_WAIT looking perfectly ordinary. Each signal here is
// read from data the probe layer already collects.

// noteTCPHealthAlerts applies the per-listener and per-process rules: an
// accept queue filling up, and a process accumulating CLOSE_WAIT sockets. Both
// are states rather than moments, so both announce their recovery.
func (m *Model) noteTCPHealthAlerts(ports []probe.ListenPort, procs []probe.ProcNet, now time.Time) {
	if m.alertRules.listenQueuePct <= 0 && m.alertRules.closeWait <= 0 {
		return
	}
	if m.alertFired == nil {
		m.alertFired = map[string]time.Time{}
	}

	rec := m.newEventRecorder(now)
	defer rec.flush()

	if pct := m.alertRules.listenQueuePct; pct > 0 {
		for _, p := range ports {
			ratio, ok := p.QueueRatio()
			// An empty queue never alerts, whatever the threshold: a tiny
			// backlog makes any non-zero percentage meaningless otherwise.
			if !ok || p.AcceptQueue == 0 || ratio*100 < pct {
				continue
			}
			subject := p.Proto + " " + p.Local
			key := "listenq:" + subject
			m.noteBreach(key, "listen_queue", subject, now)
			if m.shouldFire(key, now) {
				rec.add(levelWarn, "listen_queue", subject,
					joinNonEmpty(fmt.Sprintf("accept queue %d/%d (%.0f%%) over threshold %s%% — the service is not accepting fast enough",
						p.AcceptQueue, p.Backlog, ratio*100, trimFloat(pct)),
						procLabel(p.PID, p.Process)))
			}
		}
	}

	if limit := m.alertRules.closeWait; limit > 0 {
		for _, p := range procs {
			if p.CloseWait < limit {
				continue
			}
			subject := procLabel(p.PID, p.Name)
			if subject == "" {
				continue // a socket with no owner is no one's bug to fix
			}
			key := "closewait:" + subject
			m.noteBreach(key, "close_wait", subject, now)
			if m.shouldFire(key, now) {
				rec.add(levelWarn, "close_wait", subject,
					fmt.Sprintf("%d sockets in CLOSE_WAIT, threshold %d — the peer closed them and the process has not",
						p.CloseWait, limit))
			}
		}
	}

	m.pruneAlertHistory(now)
}

// noteListenDropAlerts fires when the host is dropping connections on the
// listen path — accept queues overflowing, SYNs refused. The counter is
// host-wide and cumulative since boot, so the rule is a rate between two
// samples, and the first sample only sets the baseline.
func (m *Model) noteListenDropAlerts(snap probe.NetSnapshot) {
	if m.alertRules.listenDropsPerMin <= 0 || snap.TCP == nil {
		return
	}
	if m.alertFired == nil {
		m.alertFired = map[string]time.Time{}
	}

	now := snap.TakenAt
	if now.IsZero() {
		now = time.Now()
	}
	cur := snap.TCP.Totals
	prev, prevAt := m.prevTCP, m.lastTCPSample
	m.prevTCP, m.lastTCPSample = &cur, now

	dt := now.Sub(prevAt).Seconds()
	if prev == nil || dt <= 0 {
		return
	}
	delta := deltaBytes(cur.ListenDrops, prev.ListenDrops)
	if delta == 0 {
		return
	}
	perMin := float64(delta) / dt * 60
	if perMin < m.alertRules.listenDropsPerMin {
		return
	}

	rec := m.newEventRecorder(now)
	defer rec.flush()

	subject := snap.Hostname
	if subject == "" {
		subject = "host"
	}
	key := "listendrops:" + subject
	m.noteBreach(key, "listen_drops", subject, now)
	if m.shouldFire(key, now) {
		detail := fmt.Sprintf("%.0f listen drops/min over threshold %s", perMin, trimFloat(m.alertRules.listenDropsPerMin))
		if d := deltaBytes(cur.ListenOverflows, prev.ListenOverflows); d > 0 {
			detail += fmt.Sprintf(", %.0f/min from full accept queues", float64(d)/dt*60)
		}
		rec.add(levelWarn, "listen_drops", subject, detail)
	}
	m.pruneAlertHistory(now)
}

// tcpProblemCounter reports whether a non-zero rate of this counter is a
// problem in itself, rather than background noise any busy host has.
func tcpProblemCounter(key string) bool {
	switch key {
	case "listen_overflows", "listen_drops", "syncookies_sent", "in_errs":
		return true
	}
	return false
}

// perMinLabel words a per-second rate as a per-minute one: these counters
// move a few times a minute on a healthy host, and "0.1/s" reads as nothing.
func perMinLabel(perSec float64) string {
	perMin := perSec * 60
	if perMin < 10 {
		return fmt.Sprintf("%.1f/min", perMin)
	}
	return fmt.Sprintf("%.0f/min", perMin)
}

// renderTCPHealth is the Overview panel for the host-wide TCP counters. Like
// the other panels it appears only when there is something to say: every
// counter idle means a quiet host, and a panel of zeros would only push the
// rest of the Overview off screen.
func (m Model) renderTCPHealth(width int) string {
	h := m.lastSnap.TCP
	if h == nil {
		return ""
	}
	var lines []string
	for _, c := range h.Counters() {
		if c.PerSec <= 0 {
			continue
		}
		rate := padRight(perMinLabel(c.PerSec), 12)
		if tcpProblemCounter(c.Key) {
			rate = warnStyle.Render(rate)
		}
		lines = append(lines, clampToWidthOneLine(
			fmt.Sprintf("%-24s %s %s", c.Label, rate, subtleStyle.Render(fmt.Sprintf("total %d", c.Total))), width))
	}
	if len(lines) == 0 {
		return ""
	}
	return titleStyle.Render("Host TCP") + "  " + subtleStyle.Render("since the last sample") + "\n" +
		strings.Join(lines, "\n") + "\n\n"
}
