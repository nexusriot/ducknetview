package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nexusriot/ducknetview/internal/ui"
	"github.com/nexusriot/ducknetview/internal/version"
)

// runDaemon runs the model headless until a signal arrives. It is the same
// model the TUI runs — the same polling, rates, event detection and alert
// rules — with nothing drawing it, which is what makes the alerting usable
// from a unit file rather than only while somebody is watching.
func runDaemon(logEvents, onAlert, metricsListen string) int {
	m := ui.NewModel().WithConfig()

	if logEvents != "" {
		var err error
		if m, err = m.OpenEventLog(logEvents); err != nil {
			fmt.Fprintln(os.Stderr, "ducknetview:", err)
			return exitUsage
		}
	}
	if onAlert != "" {
		m = m.WithAlertHook(onAlert)
	}
	// A daemon's stdout is its log. Without this it would be entirely silent
	// about the events it exists to notice, which is no use under journald.
	m = m.WithEventEcho(os.Stdout)

	// Bound before the program is built, so a taken port is a startup failure
	// rather than something discovered later.
	var exporter *metricsServer
	if metricsListen != "" {
		var err error
		if exporter, err = serveMetrics(metricsListen); err != nil {
			fmt.Fprintf(os.Stderr, "ducknetview: metrics listener: %v\n", err)
			return exitUsage
		}
		defer exporter.Close()
	}

	p := ui.DaemonProgram(m)
	if exporter != nil {
		// From here a scrape is answered from the daemon's own state: the
		// current rates, the live socket tables, the session's totals and how
		// long it has been since the last poll.
		exporter.Attach(p)
	}

	// The signal is handled here rather than by Bubble Tea's own handler
	// because a daemon has to take the normal exit path: the day's usage totals
	// are written on quit, and a hard stop would lose them.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	go func() {
		for sig := range sigs {
			if sig == syscall.SIGHUP {
				// Re-read the config in place. Restarting to pick up a new
				// threshold would throw away the session totals and the event
				// ring, which is most of what a long-running daemon is for.
				p.Send(ui.ReloadMsg{})
				continue
			}
			p.Send(ui.DaemonShutdownMsg{})
			return
		}
	}()

	fmt.Fprintf(os.Stderr, "ducknetview %s: watching (pid %d)\n", version.Version, os.Getpid())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ducknetview:", err)
		return exitUsage
	}
	return exitOK
}

// runCheckConfig reports everything wrong with the saved config. It exits
// non-zero on any problem, so a unit can refuse to start rather than running
// with half its rules quietly disabled.
func runCheckConfig() int {
	r := ui.CheckConfig()
	fmt.Print(ui.FormatConfigReport(r))
	if r.OK() {
		return exitOK
	}
	if r.Fatal {
		return exitUsage
	}
	return exitOffending
}

// runDiff compares two snapshots. It exits like diff(1): 0 when they match, 1
// when they do not, so a deploy check can simply test the status.
func runDiff(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: ducknetview --diff OLD.json NEW.json")
		return exitUsage
	}

	from, err := ui.LoadSnapshot(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ducknetview:", err)
		return exitUsage
	}
	to, err := ui.LoadSnapshot(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ducknetview:", err)
		return exitUsage
	}

	d := ui.DiffSnapshots(from, to)
	fmt.Print(ui.FormatDiff(d))
	if d.Changed() {
		return exitDifferences
	}
	return exitOK
}
