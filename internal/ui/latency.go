package ui

import (
	"fmt"
	"net"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// A socket's RTT only exists once something is already talking. To answer "is
// the path healthy right now" the tool has to probe on purpose — and the
// default is the first hop only, because that keeps the measurement on the LAN
// unless the user asks for more.

const (
	// latencyInterval is how often the targets are probed.
	latencyInterval = 5 * time.Second
	// latencyTimeout bounds one probe; anything slower is reported as loss.
	latencyTimeout = 2 * time.Second
	// latencyHistoryLen is how many samples the sparkline keeps.
	latencyHistoryLen = 60
)

type latencyTickMsg time.Time

// latencyResultMsg carries one round of measurements.
type latencyResultMsg struct {
	samples []probe.LatencySample
}

func latencyTickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return latencyTickMsg(t) })
}

// latencyTargets is the gateway (unless it could not be determined) plus the
// configured extras that are actually dialable.
func (m Model) latencyTargets() []probe.LatencyTarget {
	var targets []probe.LatencyTarget
	if t, ok := probe.GatewayTarget(m.routes); ok {
		targets = append(targets, t)
	}
	for _, addr := range m.latencyExtra {
		if !dialable(addr) {
			continue
		}
		targets = append(targets, probe.LatencyTarget{Label: addr, Addr: addr})
	}
	return targets
}

// unusableTargets are configured entries that cannot be dialed — almost always
// a missing port. Silently never measuring them looks like packet loss.
func (m Model) unusableTargets() []string {
	var bad []string
	for _, addr := range m.latencyExtra {
		if !dialable(addr) {
			bad = append(bad, addr)
		}
	}
	return bad
}

// dialable reports whether a configured target has the host:port shape
// net.Dial needs.
func dialable(addr string) bool {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	return err == nil && host != "" && port != ""
}

// measureLatencyCmd probes every target concurrently, off the UI loop.
func measureLatencyCmd(targets []probe.LatencyTarget) tea.Cmd {
	if len(targets) == 0 {
		return nil
	}
	return func() tea.Msg {
		type result struct {
			i int
			s probe.LatencySample
		}
		ch := make(chan result, len(targets))
		for i, t := range targets {
			go func(i int, t probe.LatencyTarget) {
				ch <- result{i, probe.MeasureLatency(t, latencyTimeout)}
			}(i, t)
		}

		samples := make([]probe.LatencySample, len(targets))
		for range targets {
			r := <-ch
			samples[r.i] = r.s
		}
		return latencyResultMsg{samples: samples}
	}
}

// recordLatency stores one round of samples and their history.
func (m *Model) recordLatency(samples []probe.LatencySample) {
	m.latency = samples
	if m.latencyHist == nil {
		m.latencyHist = map[string][]float64{}
	}
	for _, s := range samples {
		ms := 0.0
		if s.OK() {
			ms = float64(s.RTT) / float64(time.Millisecond)
		}
		m.latencyHist[s.Label] = probe.ClampHistory(append(m.latencyHist[s.Label], ms), latencyHistoryLen)
	}
}

// renderLatency draws one line per probed target: the current round trip, a
// short history, and whether the last probe came back at all.
func (m Model) renderLatency(width int) string {
	bad := m.unusableTargets()
	if len(m.latency) == 0 && len(bad) == 0 {
		return ""
	}

	chartW := max(8, min(width-40, 40))
	var b strings.Builder
	b.WriteString(titleStyle.Render("Path latency") + "  " +
		subtleStyle.Render(fmt.Sprintf("TCP handshake, every %s", latencyInterval)) + "\n")

	for _, s := range m.latency {
		label := padRight(trunc(s.Label, 16), 16)
		if !s.OK() {
			b.WriteString(fmt.Sprintf("  %s %s\n", label, errStyle.Render("no response")))
			continue
		}
		ms := float64(s.RTT) / float64(time.Millisecond)
		value := fmt.Sprintf("%.1fms", ms)
		switch {
		case ms >= 200:
			value = warnStyle.Render(value)
		case ms < 20:
			value = okStyle.Render(value)
		}
		b.WriteString(fmt.Sprintf("  %s %-9s %s\n", label, value, Spark(m.latencyHist[s.Label], chartW)))
	}
	if len(bad) > 0 {
		b.WriteString("  " + warnStyle.Render("unusable targets: "+strings.Join(bad, ", ")) +
			subtleStyle.Render("  (needs host:port)") + "\n")
	}
	b.WriteString("\n")

	return b.String()
}
