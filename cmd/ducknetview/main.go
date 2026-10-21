package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	_ "github.com/gdamore/tcell/v2" // keep tcell in the build; Bubble Tea already owns the terminal
	"github.com/muesli/termenv"

	"github.com/nexusriot/ducknetview/internal/probe"
	"github.com/nexusriot/ducknetview/internal/ui"
	"github.com/nexusriot/ducknetview/internal/version"
)

// Exit codes for the non-interactive modes, so cron and monitoring can act on
// them without parsing output.
const (
	exitOK        = 0
	exitOffending = 1 // --check-baseline found listeners outside the baseline
	// exitDifferences is the same code for the same reason diff(1) uses it:
	// "the comparison succeeded and the answer is no".
	exitDifferences = 1 // --diff found changes between the two snapshots
	exitUsage       = 2
)

// fatal reports an error and exits with the usage/collection status the man
// page documents. log.Fatal would exit 1, which scripts read as "differences
// found" for --diff and --check-baseline.
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ducknetview: "+format+"\n", args...)
	os.Exit(exitUsage)
}

// flagSet reports whether a flag was given on the command line, which is the
// only way to tell an explicit empty value from an absent one.
func flagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func main() {
	once := flag.Bool("once", false, "print a one-shot snapshot and exit (no TUI)")
	jsonOut := flag.Bool("json", false, "output the snapshot as JSON (implies -once)")
	checkBaseline := flag.Bool("check-baseline", false, "compare listeners against the saved baseline and exit non-zero if any are unexpected")
	baselineFile := flag.String("baseline", "", "baseline file to check against, instead of the saved per-user baseline")
	saveBaseline := flag.String("save-baseline", "", "write the current listeners to a portable baseline file and exit")
	logEvents := flag.String("log-events", "", "append events (new listeners, watchlist hits, interface changes) to a file as JSON lines")
	onAlert := flag.String("on-alert", "", "command to run for each alert-level event (receives kind, subject and detail as arguments)")
	startTab := flag.String("tab", "", "tab to open on start: "+strings.Join(ui.TabNames(), ", "))
	startView := flag.String("view", "", "saved view to open on start (see the V overlay)")
	fromFile := flag.String("from", "", "browse a --json snapshot instead of the live host (\"-\" reads stdin); ssh://[user@]host polls that host live")
	metrics := flag.Bool("metrics", false, "print the host's network state in Prometheus text format and exit")
	top := flag.Int("top", 0, "print the N busiest processes and hosts and exit")
	usage := flag.Int("usage", 0, "print the recorded daily usage for the last N days and exit (see -usage-all)")
	showUsage := flag.Bool("usage-all", false, "print all recorded daily usage and exit")
	daemon := flag.Bool("daemon", false, "run headless: poll, apply the alert rules and log events, with no TUI (SIGHUP reloads the config)")
	metricsListen := flag.String("metrics-listen", "", "serve Prometheus metrics at /metrics on this address (e.g. 127.0.0.1:9847)")
	diff := flag.Bool("diff", false, "compare two --json snapshots given as arguments and exit")
	checkConfig := flag.Bool("check-config", false, "report everything wrong with the saved config and exit non-zero if anything is")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("ducknetview " + version.Version)
		return
	}

	// An explicitly empty --view lists the saved views, one per line, and
	// exits. Shell completion uses it; without it, completing --view started
	// the whole TUI.
	if *startView == "" && flagSet("view") {
		for _, name := range ui.ViewNames() {
			fmt.Println(name)
		}
		return
	}

	if *diff {
		os.Exit(runDiff(flag.Args()))
	}

	// Without these, a mistyped invocation fell through to the TUI or
	// silently did something other than what was asked.
	if flag.NArg() > 0 {
		fatal("unexpected argument %q (only --diff takes positional arguments)", flag.Arg(0))
	}
	if *top < 0 || *usage < 0 {
		fatal("--top and --usage take a positive count")
	}
	if *daemon && *fromFile != "" {
		fatal("--daemon monitors this host; it cannot be combined with --from")
	}

	if *checkConfig {
		os.Exit(runCheckConfig())
	}

	if *saveBaseline != "" {
		os.Exit(runSaveBaseline(*saveBaseline))
	}

	if *metrics {
		if err := runMetrics(); err != nil {
			fatal("%v", err)
		}
		return
	}

	if *daemon {
		os.Exit(runDaemon(*logEvents, *onAlert, *metricsListen))
	}

	if *top > 0 {
		if err := runTop(*top); err != nil {
			fatal("%v", err)
		}
		return
	}

	if *usage > 0 || *showUsage {
		days := *usage
		if *showUsage {
			days = 0
		}
		fmt.Print(ui.FormatUsage(days))
		return
	}

	if *checkBaseline {
		os.Exit(runBaselineCheck(*baselineFile))
	}

	if *once || *jsonOut {
		if err := runSnapshot(*jsonOut); err != nil {
			fatal("%v", err)
		}
		return
	}

	// Serving metrics without --daemon is a perfectly good exporter on its own,
	// so it runs alongside the TUI rather than instead of it. The listener is
	// bound here, before the program is built, so a taken port is reported on
	// a usable terminal rather than from inside the alt screen.
	var exporter *metricsServer
	if *metricsListen != "" {
		var err error
		if exporter, err = serveMetrics(*metricsListen); err != nil {
			fatal("metrics listener: %v", err)
		}
		defer exporter.Close()
	}

	// NO_COLOR is a convention worth honouring explicitly rather than hoping the
	// renderer notices it (https://no-color.org).
	if os.Getenv("NO_COLOR") != "" {
		lipgloss.SetColorProfile(termenv.Ascii)
	}

	m := ui.NewModel().WithConfig()
	if *fromFile != "" {
		if ui.IsRemoteTarget(*fromFile) {
			var err error
			if m, err = m.WithRemote(*fromFile); err != nil {
				fatal("%v", err)
			}
		} else {
			snap, err := ui.LoadSnapshot(*fromFile)
			if err != nil {
				fatal("%v", err)
			}
			m = m.WithSnapshot(snap)
		}
	}
	if *logEvents != "" {
		var err error
		if m, err = m.OpenEventLog(*logEvents); err != nil {
			fatal("open event log: %v", err)
		}
	}
	if *onAlert != "" {
		m = m.WithAlertHook(*onAlert)
	}
	// Applied before --tab, so the two compose: a view sets the whole
	// arrangement and --tab then says which part of it to look at first.
	if *startView != "" {
		var ok bool
		if m, ok = m.WithView(*startView); !ok {
			names := ui.ViewNames()
			if len(names) == 0 {
				fatal("no views are saved — set the screen up and press V to save one")
			}
			fatal("unknown view %q (saved: %s)", *startView, strings.Join(names, ", "))
		}
	}
	if *startTab != "" {
		var ok bool
		if m, ok = m.WithTab(*startTab); !ok {
			fatal("unknown tab %q (want one of: %s)", *startTab, strings.Join(ui.TabNames(), ", "))
		}
	}

	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
	)
	if exporter != nil {
		// The TUI's model is as live as the daemon's, so a scrape should read
		// it rather than enumerate every socket a second time.
		exporter.Attach(p)
	}

	if _, err := p.Run(); err != nil {
		fatal("%v", err)
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
		fmt.Printf("baseline clean: %d listeners compared, none unexpected", ui.BaselineComparable(info.Ports))
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

// sampleTwice takes two socket enumerations a second apart, which is what any
// rate has to be derived from.
func sampleTwice() (probe.ConnInfo, probe.ConnInfo, float64, error) {
	first, err := probe.Collect(0)
	if err != nil {
		return probe.ConnInfo{}, probe.ConnInfo{}, 0, err
	}
	start := time.Now()
	time.Sleep(time.Second)
	second, err := probe.Collect(0)
	if err != nil {
		return probe.ConnInfo{}, probe.ConnInfo{}, 0, err
	}
	return first, second, time.Since(start).Seconds(), nil
}

// runMetrics prints one scrape's worth of metrics. Rates need two samples, so
// this takes a second to run — which is fine for a cron-driven textfile.
func runMetrics() error {
	s := probe.NewNetSampler()
	if _, err := s.Sample(); err != nil { // prime the interface counters
		return err
	}
	time.Sleep(time.Second)
	snap, err := s.Sample()
	if err != nil {
		return err
	}
	info, err := probe.Collect(0)
	if err != nil {
		return err
	}

	offending, _, known := ui.BaselineCheck(info.Ports)
	fmt.Print(ui.FormatMetrics(snap, info, len(offending), known))
	return nil
}

// runTop prints the busiest processes and hosts, for a quick answer without the
// TUI.
func runTop(n int) error {
	first, second, seconds, err := sampleTwice()
	if err != nil {
		return err
	}
	fmt.Print(ui.FormatTopTalkers(first, second, seconds, n))
	return nil
}

// collectHostState reads the host's routing configuration for a snapshot. It
// runs while the sampler is waiting out its one-second interval, so carrying it
// costs no extra wall time. Failures are not fatal: a snapshot without routes
// is still a useful snapshot, and the viewer says which it is looking at.
func collectHostState(snap *ui.Snapshot) {
	if routes, err := probe.Routes(); err == nil {
		snap.Routes = routes
	}
	if ns, err := probe.Neighbours(); err == nil {
		snap.Neighbours = ns
	}
	if rs, err := probe.Resolvers(); err == nil {
		snap.Resolvers = rs
	}
	if w, err := probe.WifiLinks(); err == nil && len(w) > 0 {
		snap.Wifi = w
	}
	// One measurement of the first hop, which is what the live Overview probes
	// by default. Nothing else is dialled, so a snapshot still sends no traffic
	// off the LAN.
	if t, ok := probe.GatewayTarget(snap.Routes); ok {
		snap.Latency = []probe.LatencySample{probe.MeasureLatency(t, snapshotLatencyTimeout)}
	}
}

// snapshotLatencyTimeout bounds the one gateway probe a snapshot takes, so a
// --json on a host with no reachable gateway is not noticeably slower.
const snapshotLatencyTimeout = 900 * time.Millisecond

// takeSnapshot samples the system once. It takes two interface samples ~1s
// apart so RX/TX rates are non-zero, and reads the host's configuration during
// that interval.
func takeSnapshot() (ui.Snapshot, probe.NetSnapshot, probe.ConnInfo, error) {
	s := probe.NewNetSampler()
	if _, err := s.Sample(); err != nil { // prime the counters
		return ui.Snapshot{}, probe.NetSnapshot{}, probe.ConnInfo{}, err
	}

	var out ui.Snapshot
	done := make(chan struct{})
	go func() {
		defer close(done)
		collectHostState(&out)
	}()

	time.Sleep(time.Second)
	snap, err := s.Sample()
	if err != nil {
		<-done
		return ui.Snapshot{}, probe.NetSnapshot{}, probe.ConnInfo{}, err
	}
	info, err := probe.Collect(0) // no process limit for a snapshot
	if err != nil {
		<-done
		return ui.Snapshot{}, probe.NetSnapshot{}, probe.ConnInfo{}, err
	}
	<-done

	out.Hostname = snap.Hostname
	out.UptimeSeconds = int64(snap.Uptime.Seconds())
	out.TakenAt = snap.TakenAt.Format(time.RFC3339)
	out.Version = version.Version
	out.Interfaces = snap.Ifaces
	out.TCP = snap.TCP
	out.Ports = info.Ports
	out.Processes = info.Procs
	out.Connections = info.Conns
	return out, snap, info, nil
}

// runSnapshot samples the system once and prints it to stdout, for scripting /
// piping.
func runSnapshot(asJSON bool) error {
	out, snap, info, err := takeSnapshot()
	if err != nil {
		return err
	}

	if asJSON {
		// Marshalling the viewer's own type is what keeps --json and --from
		// from drifting apart.
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
		_, port := probe.SplitHostPort(p.Local)
		fmt.Printf("  %-4s %-24s %-10s %-8s %-10s %d %s%s\n",
			p.Proto, p.Local, probe.ServiceName(port, p.Proto),
			p.Exposure(), p.User, p.PID, p.Process, netnsSuffix(p.NetNS))
	}

	fmt.Printf("\nconnections (%d):\n", len(info.Conns))
	for _, c := range info.Conns {
		fmt.Printf("  %-4s %-24s -> %-24s %-12s %d %s%s\n",
			c.Proto, c.Local, c.Remote, c.State, c.PID, c.Process, netnsSuffix(c.NetNS))
	}

	// Said on stderr, so the listing itself stays the same shape for scripts.
	if info.NetNS.Limited != "" {
		fmt.Fprintln(os.Stderr, "ducknetview: other network namespaces:", info.NetNS.Limited)
	}

	return nil
}

// netnsSuffix tags a --once row from another network namespace (a container)
// with that namespace; our own rows stay as they always were.
func netnsSuffix(ns string) string {
	if ns == "" {
		return ""
	}
	return "  [netns " + ns + "]"
}
