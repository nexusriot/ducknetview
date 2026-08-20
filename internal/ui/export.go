package ui

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// exportResultMsg reports the outcome of an async CSV export.
type exportResultMsg struct {
	path string
	err  error
}

// exportViewCmd snapshots the current tab's filtered/sorted rows (on the main
// loop, to avoid touching model state off it) and returns a command that writes
// them to a timestamped CSV file in the working directory.
func exportViewCmd(m Model) tea.Cmd {
	var name string
	var header []string
	var records [][]string

	switch m.activeTab {
	case tabPorts:
		name = "ports"
		header = []string{"proto", "local", "service", "exposure", "in_baseline", "user", "pid", "process"}
		for _, p := range m.filteredPorts() {
			records = append(records, []string{
				p.Proto, p.Local, probe.ServiceName(portOf(p.Local), p.Proto),
				probe.ListenerExposure(stripPort(p.Local)), strconv.FormatBool(!m.offBaseline(p)),
				p.User, strconv.Itoa(int(p.PID)), p.Process,
			})
		}
	case tabProcs:
		name = "processes"
		header = []string{"pid", "processes", "name", "user", "container", "cpu_percent", "mem_percent", "rx_bps", "tx_bps", "conns", "listen"}
		for _, p := range m.filteredProcs() {
			r := m.rowRate(p)
			records = append(records, []string{
				strconv.Itoa(int(p.PID)), strconv.Itoa(p.members), p.Name, p.User, p.Container,
				strconv.FormatFloat(p.CPU, 'f', 1, 64),
				strconv.FormatFloat(float64(p.Mem), 'f', 1, 32),
				strconv.FormatFloat(r.rx, 'f', 0, 64), strconv.FormatFloat(r.tx, 'f', 0, 64),
				strconv.Itoa(p.ConnCount), strconv.Itoa(p.ListenCount),
			})
		}
	case tabConns:
		if m.showClosed {
			name = "closed_connections"
			header = []string{"proto", "local", "remote", "service", "scope", "last_state",
				"opened", "closed", "lifetime_seconds", "rx_bytes", "tx_bytes", "retrans", "user", "pid", "process"}
			for _, cc := range m.closedRows() {
				c := cc.conn
				opened := ""
				if !cc.firstSeen.IsZero() {
					opened = cc.firstSeen.Format(time.RFC3339)
				}
				records = append(records, []string{
					c.Proto, c.Local, m.dnsDisplay(c.Remote),
					probe.ServiceName(portOf(c.Remote), c.Proto), probe.IPScope(stripPort(c.Remote)),
					c.State, opened, cc.closedAt.Format(time.RFC3339),
					strconv.FormatFloat(cc.lifetime().Seconds(), 'f', 1, 64),
					strconv.FormatUint(cc.totals.rx, 10), strconv.FormatUint(cc.totals.tx, 10),
					strconv.FormatUint(uint64(c.Retrans), 10), c.User,
					strconv.Itoa(int(c.PID)), c.Process,
				})
			}
			break
		}
		name = "connections"
		header = []string{"proto", "local", "remote", "service", "scope", "state", "first_seen",
			"rx_bps", "tx_bps", "rx_bytes", "tx_bytes", "rtt_ms", "user", "pid", "process"}
		for _, c := range m.filteredConns() {
			firstSeen := ""
			if t, ok := m.connFirstSeen[connKey(c)]; ok && !t.IsZero() {
				firstSeen = t.Format(time.RFC3339)
			}
			rtt := ""
			if c.RTT > 0 {
				rtt = strconv.FormatFloat(float64(c.RTT)/float64(time.Millisecond), 'f', 1, 64)
			}
			r := m.connRate(c)
			records = append(records, []string{
				c.Proto, c.Local, m.dnsDisplay(c.Remote),
				probe.ServiceName(portOf(c.Remote), c.Proto), probe.IPScope(stripPort(c.Remote)),
				c.State, firstSeen,
				strconv.FormatFloat(r.rx, 'f', 0, 64), strconv.FormatFloat(r.tx, 'f', 0, 64),
				strconv.FormatUint(c.RxBytes, 10), strconv.FormatUint(c.TxBytes, 10), rtt,
				c.User, strconv.Itoa(int(c.PID)), c.Process,
			})
		}
	case tabEvents:
		name = "events"
		header = []string{"time", "level", "kind", "subject", "detail"}
		for _, e := range m.eventRows() {
			records = append(records, []string{
				e.At.Format(time.RFC3339), e.Level.String(), e.Kind, e.Subject, e.Detail,
			})
		}
	default:
		return nil
	}

	fname := fmt.Sprintf("ducknetview_%s_%s.csv", name, time.Now().Format("20060102-150405"))

	return func() tea.Msg {
		f, err := os.Create(fname)
		if err != nil {
			return exportResultMsg{err: err}
		}
		defer f.Close()

		w := csv.NewWriter(f)
		_ = w.Write(header)
		_ = w.WriteAll(records)
		w.Flush()
		if err := w.Error(); err != nil {
			return exportResultMsg{err: err}
		}

		abs, e := filepath.Abs(fname)
		if e != nil {
			abs = fname
		}
		return exportResultMsg{path: abs}
	}
}
