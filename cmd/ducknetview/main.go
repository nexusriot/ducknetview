package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	_ "github.com/gdamore/tcell/v2" // keep tcell in the build; Bubble Tea already owns the terminal

	"github.com/nexusriot/ducknetview/internal/probe"
	"github.com/nexusriot/ducknetview/internal/ui"
	"github.com/nexusriot/ducknetview/internal/version"
)

// Exit codes for the non-interactive modes, so cron and monitoring can act on
// them without parsing output.
const (
	exitOK        = 0
	exitOffending = 1 // --check-baseline found listeners outside the baseline
	exitUsage     = 2
)

func main() {
	once := flag.Bool("once", false, "print a one-shot snapshot and exit (no TUI)")
	jsonOut := flag.Bool("json", false, "output the snapshot as JSON (implies -once)")
	checkBaseline := flag.Bool("check-baseline", false, "compare listeners against the saved baseline and exit non-zero if any are unexpected")
	baselineFile := flag.String("baseline", "", "baseline file to check against, instead of the saved per-user baseline")
	saveBaseline := flag.String("save-baseline", "", "write the current listeners to a portable baseline file and exit")
	logEvents := flag.String("log-events", "", "append events (new listeners, watchlist hits, interface changes) to a file as JSON lines")
	onAlert := flag.String("on-alert", "", "command to run for each alert-level event (receives kind, subject and detail as arguments)")
	startTab := flag.String("tab", "", "tab to open on start: "+strings.Join(ui.TabNames(), ", "))
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("ducknetview " + version.Version)
		return
	}

	if *saveBaseline != "" {
		os.Exit(runSaveBaseline(*saveBaseline))
	}

	if *checkBaseline {
		os.Exit(runBaselineCheck(*baselineFile))
	}

	if *once || *jsonOut {
		if err := runSnapshot(*jsonOut); err != nil {
			log.Fatal(err)
		}
		return
	}

	m := ui.NewModel().WithConfig()
	if *logEvents != "" {
		var err error
		if m, err = m.OpenEventLog(*logEvents); err != nil {
			log.Fatalf("open event log: %v", err)
		}
	}
	if *onAlert != "" {
		m = m.WithAlertHook(*onAlert)
	}
	if *startTab != "" {
		var ok bool
		if m, ok = m.WithTab(*startTab); !ok {
			log.Fatalf("unknown tab %q (want one of: %s)", *startTab, strings.Join(ui.TabNames(), ", "))
		}
	}

	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
	)

	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

// runSaveBaseline writes a portable baseline: build one on a known-good host,
// ship the file to the fleet, and check every host against it.
func runSaveBaseline(path string) int {
	info, err := probe.Collect(0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ducknetview:", err)
		return exitUsage
	}
	host, _ := os.Hostname()

	n, err := ui.SaveBaselineFile(path, host, info.Ports)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ducknetview:", err)
		return exitUsage
	}
	fmt.Printf("wrote %d listeners to %s\n", n, path)
	return exitOK
}

// runBaselineCheck reports listeners that are not in the baseline. It is the
// scriptable half of the `b` key: run it from cron to be told when a host
// starts listening on something nobody approved. With a file it compares
// against that instead of the saved per-user baseline.
func runBaselineCheck(file string) int {
	info, err := probe.Collect(0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ducknetview:", err)
		return exitUsage
	}

	var (
		offending []probe.ListenPort
		savedAt   time.Time
	)
	if file != "" {
		offending, savedAt, err = ui.BaselineCheckFile(file, info.Ports)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ducknetview:", err)
			return exitUsage
		}
	} else {
		var ok bool
		offending, savedAt, ok = ui.BaselineCheck(info.Ports)
		if !ok {
			fmt.Fprintln(os.Stderr, "ducknetview: no baseline saved — press 'b' in the TUI, or build a portable one with --save-baseline")
			return exitUsage
		}
	}

	if len(offending) == 0 {
		fmt.Printf("baseline clean: %d listeners, none unexpected", len(info.Ports))
		if !savedAt.IsZero() {
			fmt.Printf(" (baseline saved %s)", savedAt.Format("2006-01-02 15:04:05"))
		}
		fmt.Println()
		return exitOK
	}

	fmt.Printf("%d listener(s) not in baseline:\n", len(offending))
	for _, p := range offending {
		host, port := probe.SplitHostPort(p.Local)
		fmt.Printf("  %-4s %-24s %-10s %-8s pid %d %s\n",
			p.Proto, p.Local, probe.ServiceName(port, p.Proto),
			probe.ListenerExposure(host), p.PID, p.Process)
	}
	return exitOffending
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
			"version":        version.Version,
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
		line := fmt.Sprintf("  %-16s %-4s %-9s RX %-12s TX %s",
			ii.Name, state, ii.Kind.Label(),
			probe.HumanBytesPerSec(ii.RxBps), probe.HumanBytesPerSec(ii.TxBps))
		if n := ii.LinkProblems(); n > 0 {
			line += fmt.Sprintf("   errors/drops %d", n)
		}
		fmt.Println(line)
	}

	fmt.Printf("\nlistening ports (%d):\n", len(info.Ports))
	for _, p := range info.Ports {
		host, port := probe.SplitHostPort(p.Local)
		fmt.Printf("  %-4s %-24s %-10s %-8s %-10s %d %s\n",
			p.Proto, p.Local, probe.ServiceName(port, p.Proto),
			probe.ListenerExposure(host), p.User, p.PID, p.Process)
	}

	fmt.Printf("\nconnections (%d):\n", len(info.Conns))
	for _, c := range info.Conns {
		fmt.Printf("  %-4s %-24s -> %-24s %-12s %d %s\n",
			c.Proto, c.Local, c.Remote, c.State, c.PID, c.Process)
	}

	return nil
}
