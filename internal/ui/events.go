package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// The tables show what is true right now; the event log records what changed.
// A listener that appears and disappears between two glances at the screen is
// invisible in a live view but is exactly what you want to know about later.

const (
	// eventLimit caps the in-memory ring buffer. Old events are dropped, which
	// is why --log-events exists for anything that must be kept.
	eventLimit = 500
	// eventsPerPollLimit bounds how many events one enumeration can produce, so
	// a broad watchlist or a burst of connections can't flood the buffer.
	eventsPerPollLimit = 20
)

type eventLevel int

const (
	levelInfo eventLevel = iota
	levelWarn
	levelAlert
)

func (l eventLevel) String() string {
	switch l {
	case levelWarn:
		return "warn"
	case levelAlert:
		return "alert"
	default:
		return "info"
	}
}

func (l eventLevel) style() lipgloss.Style {
	switch l {
	case levelWarn:
		return warnStyle
	case levelAlert:
		return errStyle
	default:
		return subtleStyle
	}
}

type event struct {
	At      time.Time
	Level   eventLevel
	Kind    string
	Subject string
	Detail  string
}

// MarshalJSON keeps the on-disk log flat and greppable.
func (e event) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		At      string `json:"at"`
		Level   string `json:"level"`
		Kind    string `json:"kind"`
		Subject string `json:"subject"`
		Detail  string `json:"detail,omitempty"`
	}{
		At:      e.At.Format(time.RFC3339),
		Level:   e.Level.String(),
		Kind:    e.Kind,
		Subject: e.Subject,
		Detail:  e.Detail,
	})
}

// recordEvent appends to the ring buffer and mirrors the event to the log file
// when one was configured. Log write errors are surfaced once, on the status
// line, rather than repeatedly.
func (m *Model) recordEvent(e event) {
	m.events = probe.ClampHistory(append(m.events, e), eventLimit)
	m.echoEvent(e)

	// Counted rather than derived from the ring, which is capped at 500 and so
	// forgets most of a long session.
	if m.eventCounts == nil {
		m.eventCounts = map[string]int{}
	}
	m.eventCounts[e.Kind]++
	if e.Level == levelAlert {
		m.alertCount++
	}

	// Hooks run for alerts only, and are queued for the update loop to launch.
	if e.Level == levelAlert && m.alertHook != "" {
		m.pendingHooks = append(m.pendingHooks, e)
	}

	if m.eventLog == nil {
		return
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	n, err := m.eventLog.Write(append(b, '\n'))
	if err != nil && m.eventLogErr == nil {
		m.eventLogErr = err
		m.status = "Event log write failed: " + err.Error()
		return
	}
	m.eventLogBytes += int64(n)
	m.rotateEventLog()
}

// eventLogMaxBytes is when the log is rolled over. A chatty host left running
// for a week would otherwise append until the disk complained.
const eventLogMaxBytes = 8 << 20

// rotateEventLog moves the log aside and starts a fresh one once it grows past
// the cap, keeping exactly one previous generation.
func (m *Model) rotateEventLog() {
	limit := m.eventLogMax
	if limit <= 0 {
		limit = eventLogMaxBytes
	}
	if m.eventLog == nil || m.eventLogBytes < limit {
		return
	}

	_ = m.eventLog.Close()
	m.eventLog = nil

	if err := os.Rename(m.eventLogPath, m.eventLogPath+".1"); err != nil && m.eventLogErr == nil {
		m.eventLogErr = err
		m.status = "Event log rotation failed: " + err.Error()
		return
	}
	f, err := os.OpenFile(m.eventLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		if m.eventLogErr == nil {
			m.eventLogErr = err
			m.status = "Event log reopen failed: " + err.Error()
		}
		return
	}
	m.eventLog = f
	m.eventLogBytes = 0
	m.status = "Event log rotated to " + m.eventLogPath + ".1"
}

// eventRecorder batches one poll's events so the per-poll cap can be applied
// per kind, with an explicit summary when it truncates — a silent cap would
// read as "nothing else happened".
type eventRecorder struct {
	m       *Model
	at      time.Time
	counts  map[string]int
	dropped map[string]int
}

func (m *Model) newEventRecorder(at time.Time) *eventRecorder {
	return &eventRecorder{m: m, at: at, counts: map[string]int{}, dropped: map[string]int{}}
}

func (r *eventRecorder) add(level eventLevel, kind, subject, detail string) {
	if r.counts[kind] >= eventsPerPollLimit {
		r.dropped[kind]++
		return
	}
	r.counts[kind]++
	r.m.recordEvent(event{At: r.at, Level: level, Kind: kind, Subject: subject, Detail: detail})
}

// flush emits the "…and N more" summaries for any kind that hit the cap.
func (r *eventRecorder) flush() {
	for kind, n := range r.dropped {
		if n > 0 {
			r.m.recordEvent(event{
				At:      r.at,
				Level:   levelInfo,
				Kind:    kind,
				Subject: fmt.Sprintf("%d more %s events", n, kind),
				Detail:  "suppressed to keep the log readable",
			})
		}
	}
}

// noteSocketEvents records what changed between two socket enumerations. It
// runs after the model's new-listener and new-connection diffs, reusing them.
func (m *Model) noteSocketEvents(prevPorts map[string]probe.ListenPort, conns []probe.Conn, firstRun bool, at time.Time) {
	rec := m.newEventRecorder(at)
	defer rec.flush()

	// Listeners that went away this poll, indexed by the identity a service
	// keeps when it merely rebinds: protocol, port and process name.
	gone := map[string][]probe.ListenPort{}
	if !firstRun {
		for key, p := range prevPorts {
			if _, still := m.prevPorts[key]; still || !baselineEligible(p) {
				continue
			}
			id := listenerIdentity(p)
			gone[id] = append(gone[id], p)
		}
	}

	for _, p := range m.ports {
		key := portKey(p)
		// Ephemeral UDP sockets churn constantly (every DNS query is one), so
		// they would drown the log without saying anything.
		if !m.newPorts[key] || !baselineEligible(p) {
			continue
		}

		// A listener vanishing and reappearing on the same port under the same
		// process is one event, not two: postgres moving from 127.0.0.1 to
		// 0.0.0.0 is the single most important thing this log can tell you, and
		// as separate up/down lines it reads like noise.
		if id := listenerIdentity(p); len(gone[id]) > 0 {
			was := gone[id][0]
			gone[id] = gone[id][1:]

			before := probe.ListenerExposure(stripPort(was.Local))
			after := probe.ListenerExposure(stripPort(p.Local))
			level, verb := levelInfo, "rebound"
			if exposureRank(after) > exposureRank(before) {
				level, verb = levelAlert, "now reachable from further away"
			}
			rec.add(level, "listener_moved", p.Proto+" "+p.Local,
				joinNonEmpty(fmt.Sprintf("was %s (%s → %s), %s", was.Local, before, after, verb),
					procLabel(p.PID, p.Process)))
			continue
		}

		level, note := levelWarn, probe.ListenerExposure(stripPort(p.Local))
		if m.offBaseline(p) {
			level = levelAlert
			note += ", not in baseline"
		}
		rec.add(level, "listener_up", p.Proto+" "+p.Local, joinNonEmpty(note, procLabel(p.PID, p.Process)))
	}

	// Whatever is left really did go away.
	for _, ps := range gone {
		for _, p := range ps {
			rec.add(levelInfo, "listener_down", p.Proto+" "+p.Local, procLabel(p.PID, p.Process))
		}
	}

	m.noteAttribution(rec)

	if m.dstSeen == nil {
		m.dstSeen = map[string]bool{}
	}
	for _, c := range conns {
		isNew := firstRun || m.newConns[connKey(c)]
		if !isNew {
			continue
		}
		remoteIP := stripPort(c.Remote)

		if m.isWatched(c.Remote) {
			rec.add(levelAlert, "watchlist_hit", m.dnsDisplay(c.Remote), procLabel(c.PID, c.Process))
		}
		// One event per remote host per session: a browser opens dozens of
		// connections to the same endpoint, and only the first is news.
		if probe.IPScope(remoteIP) == "public" && !m.dstSeen[remoteIP] {
			m.dstSeen[remoteIP] = true
			rec.add(levelInfo, "new_destination", m.dnsDisplay(c.Remote), procLabel(c.PID, c.Process))
		}
	}
}

// noteIfaceEvents records interfaces coming up, going down, appearing (a VPN
// connecting) or vanishing.
func (m *Model) noteIfaceEvents(snap probe.NetSnapshot) {
	cur := make(map[string]bool, len(snap.Ifaces))
	for _, ii := range snap.Ifaces {
		cur[ii.Name] = ii.IsUp
	}

	// The first snapshot is the baseline, not a burst of "interface appeared".
	if m.prevIfaceUp == nil {
		m.prevIfaceUp = cur
		return
	}

	rec := m.newEventRecorder(snap.TakenAt)
	defer rec.flush()

	for name, up := range cur {
		prev, known := m.prevIfaceUp[name]
		switch {
		case !known:
			rec.add(levelInfo, "iface_added", name, "state "+upDown(up))
		case prev != up:
			level := levelInfo
			if !up {
				level = levelWarn
			}
			rec.add(level, "iface_state", name, upDown(prev)+" → "+upDown(up))
		}
	}
	for name := range m.prevIfaceUp {
		if _, still := cur[name]; !still {
			rec.add(levelWarn, "iface_removed", name, "")
		}
	}

	m.prevIfaceUp = cur
}

// listenerIdentity is what stays the same when a service rebinds: the process
// name rather than the PID, since a restart gets a new one.
func listenerIdentity(p probe.ListenPort) string {
	_, port := probe.SplitHostPort(p.Local)
	return p.Proto + "|" + strconv.Itoa(port) + "|" + p.Process
}

// exposureRank orders reachability so a move can be judged as widening or
// narrowing rather than merely different.
func exposureRank(exposure string) int {
	switch exposure {
	case "local":
		return 0
	case "lan":
		return 1
	case "exposed":
		return 2
	}
	return 0
}

func upDown(up bool) string {
	if up {
		return "up"
	}
	return "down"
}

// joinNonEmpty joins the parts of an event's detail that are actually present.
func joinNonEmpty(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

// plural returns word with an "s" when n is not 1.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func procLabel(pid int32, name string) string {
	if pid <= 0 {
		return ""
	}
	if name == "" {
		return fmt.Sprintf("pid %d", pid)
	}
	return fmt.Sprintf("pid %d (%s)", pid, name)
}

// unreadAlerts counts alerts raised since they were last acknowledged, so the
// badge can be cleared with `d` without throwing the log away.
func (m Model) unreadAlerts() int {
	n := 0
	for _, e := range m.events {
		if e.Level == levelAlert && e.At.After(m.alertsAckedAt) {
			n++
		}
	}
	return n
}

// eventsTabLabel appends the unread alert count to the tab title so a listener
// coming up is noticeable from any other tab.
func eventsTabLabel(unread int) string {
	if unread == 0 {
		return "6 Events"
	}
	return fmt.Sprintf("6 Events !%d", unread)
}

// eventLevelFilter values: 0 everything, 1 warn and above, 2 alerts only.
func (m Model) minEventLevel() eventLevel {
	switch m.eventLevelFilter {
	case 1:
		return levelWarn
	case 2:
		return levelAlert
	default:
		return levelInfo
	}
}

func (m Model) eventLevelLabel() string {
	switch m.eventLevelFilter {
	case 1:
		return "warn+"
	case 2:
		return "alerts"
	default:
		return "all"
	}
}

// eventRows returns the events newest first — the order they are displayed,
// copied and exported in — with the level filter and search query applied.
func (m Model) eventRows() []event {
	minLevel := m.minEventLevel()
	q := newMatcher(m.filterQuery(m.eventsQuery))

	out := make([]event, 0, len(m.events))
	for i := len(m.events) - 1; i >= 0; i-- {
		e := m.events[i]
		if e.Level < minLevel {
			continue
		}
		if !q.match(e.Kind, e.Subject, e.Detail, e.Level.String()) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (m Model) renderEventsText() string {
	var b strings.Builder

	w := m.eventsVP.Width
	if w <= 0 {
		w = 120
	}
	w -= gutterWidth

	colTime := 8
	colLevel := 5
	colKind := 16
	rest := w - (colTime + 2 + colLevel + 2 + colKind + 2)
	if rest < 20 {
		rest = 20
	}
	colSubject := max(18, min(46, rest/2))
	colDetail := max(10, rest-colSubject-2)

	b.WriteString("Events (newest first)\n")
	sub := "↑↓ select • / search • l level: " + m.eventLevelLabel() + " • d ack • y copy • e export • c clear"
	if m.eventLog != nil {
		sub += " • logging to " + m.eventLogPath
	}
	b.WriteString(sub + "\n\n")

	b.WriteString(gutter(false) + fmt.Sprintf("%s  %s  %s  %s  %s\n",
		padRight("TIME", colTime),
		padRight("LVL", colLevel),
		padRight("KIND", colKind),
		padRight("SUBJECT", colSubject),
		"DETAIL",
	))
	b.WriteString(gutter(false) + strings.Repeat("─", min(w, colTime+2+colLevel+2+colKind+2+colSubject+2+colDetail)) + "\n")

	rows := m.eventRows()
	if len(rows) == 0 {
		b.WriteString("No events yet — listeners, watchlist hits and interface changes appear here.\n")
		return b.String()
	}

	q := newMatcher(m.eventsQuery)
	for i, e := range rows {
		level := e.Level.style().Render(padRight(trunc(e.Level.String(), colLevel), colLevel))
		b.WriteString(gutter(i == m.eventsCursor) + fmt.Sprintf("%s  %s  %s  %s  %s\n",
			padRight(e.At.Format("15:04:05"), colTime),
			level,
			q.highlight(padRight(trunc(e.Kind, colKind), colKind)),
			q.highlight(padRight(trunc(e.Subject, colSubject), colSubject)),
			q.highlight(trunc(e.Detail, colDetail)),
		))
	}

	return b.String()
}

func (m Model) viewEvents() string {
	eventsW := min(m.w-2, 140)
	eventsH := m.bodyHeight()

	if m.eventsText == "" {
		m.eventsText = m.renderEventsText()
		m.eventsVP.SetContent(m.eventsText)
	}

	alerts := m.unreadAlerts()
	head := subtleStyle.Render(fmt.Sprintf("%d %s", len(m.events), plural(len(m.events), "event")))
	if alerts > 0 {
		head += " " + errStyle.Render(fmt.Sprintf("(%d unread %s)", alerts, plural(alerts, "alert")))
	}
	switch {
	case m.eventsSearching:
		head = m.eventsSearch.View()
	case m.eventsQuery != "":
		head += "  " + subtleStyle.Render("Filter: ") + titleStyle.Render(m.eventsQuery) +
			subtleStyle.Render("  (/ to change, ctrl+u to clear)")
	}

	return boxStyle.Width(eventsW).Height(eventsH).Render(head + "\n\n" + m.eventsVP.View())
}

// OpenEventLog appends events to path as JSON lines. A failure to open is
// reported to the caller rather than silently dropping the log.
func (m Model) OpenEventLog(path string) (Model, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return m, err
	}
	m.eventLog = f
	m.eventLogPath = path
	// Count what is already there, so an existing log is rotated on its real
	// size rather than only on what this session appends.
	if info, err := f.Stat(); err == nil {
		m.eventLogBytes = info.Size()
	}
	return m, nil
}
