package ui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// `--from file` is frozen by design: a file has no next sample. But the trick
// that makes it useful — the remote host needs no agent, only the binary and a
// shell — works just as well repeatedly. Re-running the same command on the
// refresh interval turns the snapshot viewer into a live remote view, and
// because two consecutive snapshots carry the same cumulative counters the
// whole rate, totals, events and alert machinery works on the far host exactly
// as it does locally.

// remotePrefix marks a --from argument as a command to re-run rather than a
// file to read.
const remotePrefix = "ssh://"

// remoteTimeout bounds one remote fetch. A hung ssh must not wedge the session,
// and must not leave a second one starting behind it.
const remoteTimeout = 20 * time.Second

// remoteSnapshotMsg carries one fetch from the remote host.
type remoteSnapshotMsg struct {
	snap Snapshot
	err  error
}

// IsRemoteTarget reports whether a --from argument names a host to poll rather
// than a file to read.
func IsRemoteTarget(from string) bool {
	return strings.HasPrefix(from, remotePrefix)
}

// remoteArgv builds the command for a target. The remote binary name is fixed
// and the arguments are not taken from the target string, so a hostile-looking
// host name cannot turn into extra arguments; ssh itself is executed directly,
// never through a shell.
func remoteArgv(target string) ([]string, string, error) {
	host := strings.TrimPrefix(target, remotePrefix)
	host = strings.TrimSuffix(host, "/")
	if host == "" {
		return nil, "", fmt.Errorf("%s: no host given (want ssh://[user@]host)", target)
	}
	if strings.ContainsAny(host, " \t\n") {
		return nil, "", fmt.Errorf("%s: host must not contain whitespace", target)
	}
	// A leading dash would be read by ssh as an option.
	if strings.HasPrefix(host, "-") {
		return nil, "", fmt.Errorf("%s: host must not start with '-'", target)
	}
	return []string{"ssh", "-o", "BatchMode=yes", host, "ducknetview", "--json"}, host, nil
}

// WithRemote configures a live remote session. snapshotOnly stays set — the
// data is still not this host's, and every local collector must stay off — but
// the session is not paused, because there is always another sample coming.
func (m Model) WithRemote(target string) (Model, error) {
	argv, host, err := remoteArgv(target)
	if err != nil {
		return m, err
	}
	m.remoteArgv = argv
	m.remoteHost = host
	m.snapshotOnly = true
	m.paused = false
	m.status = "Connecting to " + host + "…"
	return m, nil
}

// remoteLive reports whether this session polls a remote host.
func (m Model) remoteLive() bool { return len(m.remoteArgv) > 0 }

// fetchRemoteCmd runs one remote snapshot. Failures are carried in the message
// rather than ending the session: a dropped link should show as a stale view
// with an error, not as an exit.
func fetchRemoteCmd(argv []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			detail := strings.TrimSpace(stderr.String())
			if detail != "" {
				// ssh's own diagnostics are the useful part ("Permission
				// denied", "command not found"), not the exit status.
				return remoteSnapshotMsg{err: fmt.Errorf("%s", firstLine(detail))}
			}
			return remoteSnapshotMsg{err: err}
		}

		snap, err := decodeSnapshot(stdout.Bytes())
		if err != nil {
			return remoteSnapshotMsg{err: err}
		}
		return remoteSnapshotMsg{snap: snap}
	}
}

// firstLine keeps an error message to one line, since it lands on the status
// bar.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// applyRemote folds a freshly fetched remote snapshot into the model. Unlike
// WithSnapshot it does not freeze anything: the cumulative counters in two
// consecutive fetches are exactly what the rate machinery needs.
func (m *Model) applyRemote(s Snapshot) []tea.Cmd {
	takenAt := time.Now()
	if t, err := time.Parse(time.RFC3339, s.TakenAt); err == nil {
		takenAt = t
	}

	m.applyNetSnapshot(remoteNetSnapshot(s, takenAt))

	cmds := m.applyConnInfo(remoteConnInfo(s), time.Now())

	m.routes = s.Routes
	m.neighbours = s.Neighbours
	m.resolvers = s.Resolvers
	m.wifi = s.Wifi
	m.snapshotHostState = s.HasHostState()
	if len(s.Latency) > 0 {
		m.recordLatency(s.Latency)
	}
	m.routesText = hardClipLinesToWidth(m.renderRoutesText(), m.routesVP.Width)
	m.routesVP.SetContent(m.routesText)

	for _, c := range s.Connections {
		if c.RxBytes > 0 || c.TxBytes > 0 {
			m.hasThroughput = true
			break
		}
	}

	m.remoteErr = nil
	m.remoteAt = takenAt
	return cmds
}
