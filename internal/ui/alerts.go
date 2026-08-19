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
