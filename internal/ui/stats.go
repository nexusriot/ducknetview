package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// The metrics endpoint used to re-sample the host on every scrape, which is
// correct but throws away everything a running daemon is already holding: the
// current interface rates, the live socket tables, the session's accumulated
// totals, how many events of each kind have been recorded, and — the one
// nothing else can report — how long it has been since the last successful
// poll. Without that last number there is no way to alert on the monitor
// itself having stopped, which is the failure that hides every other failure.

// StatsRequest asks the update loop for a copy of the session's state. The
// model is owned by that loop, so an HTTP goroutine asks for a copy rather
// than reaching into it: a mutex over a model this size would have to be
// taken by every renderer too.
type StatsRequest struct {
	Reply chan<- SessionStats
}

// SessionStats is a consistent snapshot of what the session knows, taken
// between updates.
type SessionStats struct {
	// The host's state as of the last poll, in the shape FormatMetrics
	// already takes.
	Snap          probe.NetSnapshot
	Info          probe.ConnInfo
	OffBaseline   int
	BaselineKnown bool

	// The session's own state.
	StartedAt   time.Time
	LastPollAt  time.Time
	Polls       int
	Totals      byteTotals
	EventCounts map[string]int
	Alerts      int
	HookErrors  int
	Paused      bool

	// Set for a live remote session, so a scrape says whose numbers these are
	// and whether the link to them is working.
	RemoteHost string
	RemoteOK   bool
}

// sessionStats copies the state the metrics endpoint needs. It runs on the
// update loop, so nothing can change underneath it.
func (m Model) sessionStats() SessionStats {
	offending, _, known := m.baselineOffenders()

	counts := make(map[string]int, len(m.eventCounts))
	for kind, n := range m.eventCounts {
		counts[kind] = n
	}

	return SessionStats{
		Snap:          m.lastSnap,
		Info:          probe.ConnInfo{Ports: m.ports, Procs: m.procs, Conns: m.conns},
		OffBaseline:   offending,
		BaselineKnown: known,

		StartedAt:   m.startedAt,
		LastPollAt:  m.lastPollAt,
		Polls:       m.polls,
		Totals:      m.sessionTotal,
		EventCounts: counts,
		Alerts:      m.alertCount,
		HookErrors:  m.hookErrors,
		Paused:      m.paused,

		RemoteHost: m.remoteHost,
		RemoteOK:   m.remoteLive() && m.remoteErr == nil,
	}
}

// baselineOffenders counts listeners outside the saved baseline using the
// session's own baseline rather than re-reading the config file.
func (m Model) baselineOffenders() (int, time.Time, bool) {
	if !m.hasBaseline() {
		return 0, time.Time{}, false
	}
	return m.offBaselineCount(), m.baselineAt, true
}

// FormatSessionMetrics renders the metrics only a running session can report,
// in the Prometheus text format. It is appended to FormatMetrics' host
// metrics rather than replacing them.
func FormatSessionMetrics(s SessionStats, version string) string {
	var b strings.Builder

	metric := func(name, help, typ string) {
		fmt.Fprintf(&b, "# HELP ducknetview_%s %s\n# TYPE ducknetview_%s %s\n", name, help, name, typ)
	}

	metric("build_info", "Version of the running ducknetview, always 1.", "gauge")
	fmt.Fprintf(&b, "ducknetview_build_info{version=\"%s\"} 1\n", escapeLabel(version))

	metric("session_uptime_seconds", "How long this session has been running.", "gauge")
	fmt.Fprintf(&b, "ducknetview_session_uptime_seconds %.0f\n", secondsSince(s.StartedAt))

	// The number to alert on: a daemon that is up but no longer polling looks
	// exactly like a healthy one in every other metric here.
	metric("last_poll_age_seconds", "Seconds since the last completed socket enumeration (-1 before the first).", "gauge")
	if s.LastPollAt.IsZero() {
		b.WriteString("ducknetview_last_poll_age_seconds -1\n")
	} else {
		fmt.Fprintf(&b, "ducknetview_last_poll_age_seconds %.0f\n", secondsSince(s.LastPollAt))
	}

	metric("polls_total", "Socket enumerations completed this session.", "counter")
	fmt.Fprintf(&b, "ducknetview_polls_total %d\n", s.Polls)

	metric("paused", "Whether sampling is paused.", "gauge")
	fmt.Fprintf(&b, "ducknetview_paused %d\n", boolGauge(s.Paused))

	metric("session_bytes_total", "Bytes accounted to sockets while this session has been watching.", "counter")
	fmt.Fprintf(&b, "ducknetview_session_bytes_total{direction=\"rx\"} %d\n", s.Totals.rx)
	fmt.Fprintf(&b, "ducknetview_session_bytes_total{direction=\"tx\"} %d\n", s.Totals.tx)

	metric("events_total", "Events recorded this session, by kind.", "counter")
	kinds := make([]string, 0, len(s.EventCounts))
	for kind := range s.EventCounts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		fmt.Fprintf(&b, "ducknetview_events_total{kind=\"%s\"} %d\n", escapeLabel(kind), s.EventCounts[kind])
	}

	metric("alerts_total", "Alert-level events raised this session.", "counter")
	fmt.Fprintf(&b, "ducknetview_alerts_total %d\n", s.Alerts)

	metric("alert_hook_failures_total", "Alert hook invocations that failed.", "counter")
	fmt.Fprintf(&b, "ducknetview_alert_hook_failures_total %d\n", s.HookErrors)

	// Only meaningful for a remote session, and misleading as a constant 1 for
	// a local one — so it is simply absent locally.
	if s.RemoteHost != "" {
		metric("remote_up", "Whether the last fetch from the watched remote host succeeded.", "gauge")
		fmt.Fprintf(&b, "ducknetview_remote_up{host=\"%s\"} %d\n",
			escapeLabel(s.RemoteHost), boolGauge(s.RemoteOK))
	}

	return b.String()
}

// FormatLiveMetrics is the whole scrape for a running session: the host
// metrics from its last poll, plus what only it can report.
func FormatLiveMetrics(s SessionStats, version string) string {
	return FormatMetrics(s.Snap, s.Info, s.OffBaseline, s.BaselineKnown) +
		FormatSessionMetrics(s, version)
}

func secondsSince(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return time.Since(t).Seconds()
}

func boolGauge(b bool) int {
	if b {
		return 1
	}
	return 0
}
