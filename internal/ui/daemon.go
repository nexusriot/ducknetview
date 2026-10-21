package ui

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// Everything the alerting work of 0.2.0 and 0.3.0 added — thresholds, quality
// rules, the event log, the hook — only ran while somebody was sitting in front
// of the TUI, which is backwards: the moment you stop watching is the moment
// you want to be told. A headless session is the same model, driven by the same
// messages, with nothing drawing it.

// DaemonShutdownMsg asks a headless session to persist its state and exit. It
// is exported because the signal handler lives in main.
type DaemonShutdownMsg struct{}

// ReloadMsg asks the session to re-read the config file. Restarting to pick up
// a new threshold would throw away the session totals, the event ring and the
// rate baselines, which is most of what a long-running daemon accumulates.
type ReloadMsg struct{}

// daemonWidth and daemonHeight are the terminal size a headless session
// pretends to have. Nothing is drawn, but the model sizes viewports and clips
// rows against these, and a zero width would make every table one column wide.
const (
	daemonWidth  = 160
	daemonHeight = 48
)

// WithEventEcho makes the session mirror every event to a writer as it
// happens. A daemon's output is its log, so without this a systemd unit would
// be completely silent about the thing it exists to notice.
func (m Model) WithEventEcho(w io.Writer) Model {
	m.eventEcho = w
	return m
}

// echoEvent writes one line per event, in a shape that reads well in
// journalctl: time, level, kind, subject, detail.
func (m *Model) echoEvent(e event) {
	if m.eventEcho == nil {
		return
	}
	line := fmt.Sprintf("%s  %-5s %-20s %s",
		e.At.Format(time.RFC3339), e.Level.String(), e.Kind, printable(e.Subject))
	if e.Detail != "" {
		line += "  " + printable(e.Detail)
	}
	fmt.Fprintln(m.eventEcho, line)
}

// DaemonProgram builds a Bubble Tea program with no renderer and no input, so
// the update loop runs — ticks, polls, rates, events, alerts and the hook —
// with nothing drawn and no terminal touched.
func DaemonProgram(m Model) *tea.Program {
	m.headless = true
	p := tea.NewProgram(m,
		tea.WithoutRenderer(),
		tea.WithInput(nil),
		tea.WithoutSignalHandler(), // main owns the signals, so quit can persist first
	)
	// The model lays out against the terminal size it was last told about, and
	// headless it is never told. Sending one keeps every width positive.
	go p.Send(tea.WindowSizeMsg{Width: daemonWidth, Height: daemonHeight})
	return p
}

// printable escapes control characters. A process name or a PTR record is
// chosen by someone else, and a newline in one would forge a journal line.
func printable(s string) string {
	if strings.IndexFunc(s, unicode.IsControl) < 0 {
		return s
	}
	q := strconv.Quote(s)
	return q[1 : len(q)-1]
}
