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
		header = []string{"proto", "local", "service", "pid", "process"}
		for _, p := range m.filteredPorts() {
			records = append(records, []string{
				p.Proto, p.Local, probe.ServiceName(portOf(p.Local), p.Proto),
				strconv.Itoa(int(p.PID)), p.Process,
			})
		}
	case tabProcs:
		name = "processes"
		header = []string{"pid", "name", "user", "cpu_percent", "mem_percent", "conns", "listen"}
		for _, p := range m.filteredProcs() {
			records = append(records, []string{
				strconv.Itoa(int(p.PID)), p.Name, p.User,
				strconv.FormatFloat(p.CPU, 'f', 1, 64),
				strconv.FormatFloat(float64(p.Mem), 'f', 1, 32),
				strconv.Itoa(p.ConnCount), strconv.Itoa(p.ListenCount),
			})
		}
	case tabConns:
		name = "connections"
		header = []string{"proto", "local", "remote", "scope", "state", "first_seen", "pid", "process"}
		for _, c := range m.filteredConns() {
			firstSeen := ""
			if t, ok := m.connFirstSeen[connKey(c)]; ok && !t.IsZero() {
				firstSeen = t.Format(time.RFC3339)
			}
			records = append(records, []string{
				c.Proto, c.Local, m.dnsDisplay(c.Remote), probe.IPScope(stripPort(c.Remote)),
				c.State, firstSeen, strconv.Itoa(int(c.PID)), c.Process,
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
