package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	_ "github.com/gdamore/tcell/v2" // keep tcell in the build; Bubble Tea already owns the terminal

	"github.com/nexusriot/ducknetview/internal/probe"
	"github.com/nexusriot/ducknetview/internal/ui"
)

func main() {
	once := flag.Bool("once", false, "print a one-shot snapshot and exit (no TUI)")
	jsonOut := flag.Bool("json", false, "output the snapshot as JSON (implies -once)")
	flag.Parse()

	if *once || *jsonOut {
		if err := runSnapshot(*jsonOut); err != nil {
			log.Fatal(err)
		}
		return
	}

	m := ui.NewModel().WithConfig()

	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
	)

	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

// runSnapshot samples the system once and prints it to stdout, for scripting /
// piping. It takes two interface samples ~1s apart so RX/TX rates are non-zero.
func runSnapshot(asJSON bool) error {
	s := probe.NewNetSampler()
	if _, err := s.Sample(); err != nil { // prime the counters
		return err
	}
	time.Sleep(time.Second)
	snap, err := s.Sample()
	if err != nil {
		return err
	}
	info, err := probe.Collect(0) // no process limit for a snapshot
	if err != nil {
		return err
	}

	if asJSON {
		out := map[string]any{
			"hostname":       snap.Hostname,
			"uptime_seconds": int64(snap.Uptime.Seconds()),
			"taken_at":       snap.TakenAt.Format(time.RFC3339),
			"interfaces":     snap.Ifaces,
			"ports":          info.Ports,
			"processes":      info.Procs,
			"connections":    info.Conns,
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("host %s   uptime %s   %s\n",
		snap.Hostname, snap.Uptime.Truncate(time.Second), snap.TakenAt.Format("2006-01-02 15:04:05"))

	fmt.Printf("\ninterfaces (%d):\n", len(snap.Ifaces))
	for _, ii := range snap.Ifaces {
		state := "down"
		if ii.IsUp {
			state = "up"
		}
		fmt.Printf("  %-16s %-4s %-9s RX %-12s TX %s\n",
			ii.Name, state, ii.Kind.Label(),
			probe.HumanBytesPerSec(ii.RxBps), probe.HumanBytesPerSec(ii.TxBps))
	}

	fmt.Printf("\nlistening ports (%d):\n", len(info.Ports))
	for _, p := range info.Ports {
		svc := probe.ServiceName(portFromLocal(p.Local), p.Proto)
		fmt.Printf("  %-4s %-24s %-10s %d %s\n", p.Proto, p.Local, svc, p.PID, p.Process)
	}

	fmt.Printf("\nconnections (%d):\n", len(info.Conns))
	for _, c := range info.Conns {
		fmt.Printf("  %-4s %-24s -> %-24s %-12s %d %s\n",
			c.Proto, c.Local, c.Remote, c.State, c.PID, c.Process)
	}

	return nil
}

// portFromLocal extracts the numeric port from an "ip:port" string.
func portFromLocal(hp string) int {
	for i := len(hp) - 1; i >= 0; i-- {
		if hp[i] == ':' {
			n := 0
			for _, r := range hp[i+1:] {
				if r < '0' || r > '9' {
					return 0
				}
				n = n*10 + int(r-'0')
			}
			return n
		}
	}
	return 0
}
