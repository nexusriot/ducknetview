package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func routesModel() Model {
	m := eventsModel()
	m.w, m.h = 160, 45
	m.routesVP.Width, m.routesVP.Height = 130, 30
	m.ifaceDetailsVP.Width, m.ifaceDetailsVP.Height = 80, 20
	return m
}

func TestRoutesTabRendersHostConfiguration(t *testing.T) {
	m := routesModel()
	m.activeTab = tabRoutes
	m.routes = []probe.Route{
		{Destination: "default", Gateway: "192.168.1.1", Iface: "eth0", Metric: 100},
		{Destination: "192.168.1.0/24", Iface: "eth0"},
	}
	m.neighbours = []probe.Neighbour{
		{IP: "192.168.1.1", MAC: "b8:27:eb:11:22:33", Iface: "eth0", Vendor: "Raspberry Pi", State: "reachable"},
		{IP: "192.168.1.9", MAC: "00:00:00:00:00:00", Iface: "eth0", Stale: true, State: "incomplete"},
		{IP: "fe80::e21f:2bff:fe51:1c31", MAC: "e0:1f:2b:51:1c:31", Iface: "eth0", IPv6: true, State: "stale"},
	}
	m.resolvers = []probe.Resolver{{Address: "127.0.0.53", Stub: true}, {Address: "1.1.1.1"}}

	text := stripANSI(m.renderRoutesText())
	for _, want := range []string{"Default route", "192.168.1.1 via eth0", "Nameservers",
		"local stub resolver", "1.1.1.1", "Routes (2)", "192.168.1.0/24", "on-link",
		"Neighbours (3)", "Raspberry Pi", "incomplete"} {
		if !strings.Contains(text, want) {
			t.Errorf("routes view missing %q:\n%s", want, text)
		}
	}
	// IPv6 neighbours used to be a documented gap; they come from netlink now,
	// so the address, its state and the family split all have to be there.
	for _, want := range []string{"fe80::e21f:2bff:fe51:1c31", "stale", "2 IPv4 · 1 IPv6"} {
		if !strings.Contains(text, want) {
			t.Errorf("routes view missing IPv6 neighbour detail %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "IPv6 neighbours are not shown") {
		t.Error("the view still claims IPv6 neighbours are not covered")
	}

	if got := stripANSI(m.gatewayLine()); !strings.Contains(got, "192.168.1.1") || !strings.Contains(got, "DNS") {
		t.Errorf("gateway line = %q", got)
	}
	if !strings.Contains(stripANSI(m.viewRoutes()), "Default route") {
		t.Error("viewRoutes should render the text")
	}
}

func TestRoutesTabWithNoDefaultRoute(t *testing.T) {
	m := routesModel()
	m.routes = []probe.Route{{Destination: "10.0.0.0/8", Iface: "eth0"}}

	if !strings.Contains(stripANSI(m.renderRoutesText()), "cannot reach anything off-link") {
		t.Error("a host with no default route should be told so plainly")
	}
	if !strings.Contains(stripANSI(m.gatewayLine()), "no default route") {
		t.Errorf("gateway line = %q", stripANSI(m.gatewayLine()))
	}
}

func TestRoutesTabIsReachableAndScrolls(t *testing.T) {
	m := routesModel()
	model, _ := m.Update(key("7"))
	m = model.(Model)
	if m.activeTab != tabRoutes {
		t.Fatalf("7 selected tab %v, want routes", m.activeTab)
	}
	// It scrolls but has no cursor to move.
	if m.hasRowCursor() {
		t.Error("the routes tab should not claim a row cursor")
	}
	m.routes = make([]probe.Route, 80)
	for i := range m.routes {
		m.routes[i] = probe.Route{Destination: "10.0.0.0/8", Iface: "eth0"}
	}
	m.routesText = m.renderRoutesText()
	m.routesVP.SetContent(m.routesText)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if model.(Model).routesVP.YOffset == 0 {
		t.Error("page-down should scroll the routes viewport")
	}
}

func TestWifiLineInInterfaceDetails(t *testing.T) {
	m := routesModel()
	m.selectedIface = "wlan0"
	m.lastSnap = probe.NetSnapshot{Ifaces: []probe.IfaceInfo{
		{Name: "wlan0", IsUp: true, Kind: probe.IfacePhysical, MTU: 1500},
	}}
	m.wifi = map[string]probe.WifiLink{
		"wlan0": {Iface: "wlan0", Quality: 62, Signal: -48, Noise: -92},
	}

	text := stripANSI(m.renderIfaceDetailsText(80))
	for _, want := range []string{"Wi-Fi", "-48 dBm", "excellent", "88%", "noise -92"} {
		if !strings.Contains(text, want) {
			t.Errorf("interface details missing %q:\n%s", want, text)
		}
	}

	// A wired interface gets no Wi-Fi line at all.
	m.selectedIface = "eth0"
	m.lastSnap.Ifaces = append(m.lastSnap.Ifaces, probe.IfaceInfo{Name: "eth0", IsUp: true})
	if strings.Contains(stripANSI(m.renderIfaceDetailsText(80)), "Wi-Fi") {
		t.Error("a wired interface should not report radio state")
	}
}

func TestLatencyPanel(t *testing.T) {
	m := routesModel()
	m.routes = []probe.Route{{Destination: "default", Gateway: "192.168.1.1", Iface: "eth0", Metric: 100}}
	m.latencyExtra = []string{"1.1.1.1:443"}

	targets := m.latencyTargets()
	if len(targets) != 2 || targets[0].Label != "gateway" || targets[1].Addr != "1.1.1.1:443" {
		t.Fatalf("targets = %+v, want the gateway plus the configured extra", targets)
	}

	if m.renderLatency(80) != "" {
		t.Error("the panel should stay hidden before anything is measured")
	}

	m.recordLatency([]probe.LatencySample{
		{Label: "gateway", RTT: 2500 * time.Microsecond},
		{Label: "1.1.1.1:443", Err: os.ErrDeadlineExceeded},
	})
	m.recordLatency([]probe.LatencySample{
		{Label: "gateway", RTT: 3 * time.Millisecond},
		{Label: "1.1.1.1:443", RTT: 240 * time.Millisecond},
	})

	panel := stripANSI(m.renderLatency(80))
	if !strings.Contains(panel, "Path latency") || !strings.Contains(panel, "gateway") {
		t.Fatalf("latency panel:\n%s", panel)
	}
	if !strings.Contains(panel, "3.0ms") || !strings.Contains(panel, "240.0ms") {
		t.Errorf("panel should show both round trips:\n%s", panel)
	}
	if len(m.latencyHist["gateway"]) != 2 {
		t.Errorf("history holds %d samples, want 2", len(m.latencyHist["gateway"]))
	}

	// A failed probe says so rather than showing a zero.
	m.recordLatency([]probe.LatencySample{{Label: "gateway", Err: os.ErrDeadlineExceeded}})
	if !strings.Contains(stripANSI(m.renderLatency(80)), "no response") {
		t.Error("a failed probe should be reported, not rendered as 0ms")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")

	want := Snapshot{
		Hostname:      "remote-host",
		UptimeSeconds: 3600,
		TakenAt:       time.Now().Format(time.RFC3339),
		Interfaces: []probe.IfaceInfo{
			{Name: "eth0", IsUp: true, Kind: probe.IfacePhysical, Addrs: []string{"10.0.0.5/24"}},
		},
		Ports:     []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}},
		Processes: []probe.ProcNet{{PID: 1, Name: "sshd", ConnCount: 1}},
		Connections: []probe.Conn{
			{Proto: "tcp", Local: "10.0.0.5:5000", Remote: "1.2.3.4:443", State: "ESTABLISHED",
				PID: 2, Process: "curl", RxBytes: 4096, TxBytes: 1024},
		},
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSnapshot(path)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if got.Hostname != want.Hostname || len(got.Connections) != 1 {
		t.Fatalf("loaded %+v", got)
	}
	// The interface kind survives as a label, not an opaque integer.
	if got.Interfaces[0].Kind != probe.IfacePhysical {
		t.Errorf("interface kind = %v, want physical", got.Interfaces[0].Kind)
	}

	m := routesModel().WithSnapshot(got)
	if !m.snapshotOnly || !m.paused {
		t.Error("a snapshot must be frozen: there is nothing to refresh")
	}
	if m.lastSnap.Hostname != "remote-host" || len(m.conns) != 1 {
		t.Error("snapshot data did not reach the model")
	}
	if !m.hasThroughput || !m.showTotals {
		t.Error("a snapshot with byte counters should show them as totals")
	}
	// Interface attribution works offline, from the snapshot's own addresses.
	if got := m.ifaceFor(m.conns[0]); got != "eth0" {
		t.Errorf("connection attributed to %q, want eth0", got)
	}
	if !strings.Contains(m.View(), "SNAPSHOT") {
		t.Error("the header should say the view is frozen")
	}

	// Ticks must not replace the file's contents with the local host's.
	model, _ := m.Update(tickMsg(time.Now()))
	if h := model.(Model).lastSnap.Hostname; h != "remote-host" {
		t.Errorf("a tick overwrote the snapshot with %q", h)
	}
	// And resuming is refused rather than silently doing nothing.
	model, _ = m.Update(key(" "))
	if !strings.Contains(model.(Model).status, "nothing to resume") {
		t.Errorf("status = %q", model.(Model).status)
	}
}

func TestLoadSnapshotRejectsRubbish(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(bad); err == nil {
		t.Error("a non-JSON file should be rejected")
	}

	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(empty); err == nil {
		t.Error("an empty snapshot should be rejected rather than shown as a blank host")
	}
	if _, err := LoadSnapshot(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("a missing file should be an error")
	}
}

func TestFormatMetrics(t *testing.T) {
	snap := probe.NetSnapshot{Ifaces: []probe.IfaceInfo{
		{Name: "eth0", IsUp: true, Kind: probe.IfacePhysical, RxBps: 1000, TxBps: 500, Errin: 2, Dropout: 3},
		{Name: "eth1", IsUp: false, Kind: probe.IfacePhysical},
	}}
	info := probe.ConnInfo{
		Ports: []probe.ListenPort{
			{Proto: "tcp", Local: "0.0.0.0:22"},
			{Proto: "tcp", Local: "127.0.0.1:5432"},
		},
		Conns: []probe.Conn{
			{Proto: "tcp", Local: "10.0.0.1:1", Remote: "8.8.8.8:443", Retrans: 4},
			{Proto: "tcp", Local: "10.0.0.1:2", Remote: "10.0.0.9:443"},
		},
		Procs: []probe.ProcNet{{PID: 1}},
	}

	out := FormatMetrics(snap, info, 2, true)
	for _, want := range []string{
		`ducknetview_iface_receive_bytes_per_second{iface="eth0",kind="physical"} 1000`,
		`ducknetview_iface_up{iface="eth1"} 0`,
		`ducknetview_iface_errors_total{iface="eth0",direction="in"} 2`,
		`ducknetview_listeners{exposure="exposed"} 1`,
		`ducknetview_listeners{exposure="local"} 1`,
		`ducknetview_connections{scope="public"} 1`,
		"ducknetview_connection_retransmits_total 4",
		"ducknetview_processes_with_sockets 1",
		"ducknetview_listeners_off_baseline 2",
		"# TYPE ducknetview_iface_up gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics missing %q:\n%s", want, out)
		}
	}

	// No baseline is different from a clean one, and says so.
	if !strings.Contains(FormatMetrics(snap, info, 0, false), "ducknetview_listeners_off_baseline -1") {
		t.Error("an unknown baseline should export -1, not 0")
	}
}

func TestFormatTopTalkers(t *testing.T) {
	before := probe.ConnInfo{Conns: []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", PID: 1, Process: "curl", RxBytes: 0},
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", PID: 2, Process: "quiet", RxBytes: 0},
	}}
	after := probe.ConnInfo{Conns: []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:443", PID: 1, Process: "curl", RxBytes: 2_000_000},
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:443", PID: 2, Process: "quiet", RxBytes: 100},
	}}

	out := FormatTopTalkers(before, after, 2, 5)
	if !strings.Contains(out, "top processes") || !strings.Contains(out, "top hosts") {
		t.Fatalf("output:\n%s", out)
	}
	// The busiest comes first in both lists.
	procIdx := strings.Index(out, "curl")
	quietIdx := strings.Index(out, "quiet")
	if procIdx < 0 || quietIdx < 0 || procIdx > quietIdx {
		t.Errorf("expected curl ranked above quiet:\n%s", out)
	}
	if !strings.Contains(out, "1.1.1.1") {
		t.Errorf("hosts section should name the remote:\n%s", out)
	}

	// Nothing moving is stated, not left blank.
	if !strings.Contains(FormatTopTalkers(before, before, 1, 5), "nothing moved") {
		t.Error("an idle sample should say so")
	}
}

func TestUsageHistoryMerge(t *testing.T) {
	h := usageHistory{}
	h = h.mergeSession("2026-08-10", byteTotals{rx: 100, tx: 50},
		map[string]uint64{"curl": 150}, map[string]uint64{"1.1.1.1": 150})
	h = h.mergeSession("2026-08-10", byteTotals{rx: 200, tx: 100},
		map[string]uint64{"curl": 300, "wget": 60}, map[string]uint64{"1.1.1.1": 360})

	if len(h.Days) != 1 {
		t.Fatalf("days = %d, want the two sessions folded into one day", len(h.Days))
	}
	d := h.Days[0]
	if d.RxBytes != 300 || d.TxBytes != 150 {
		t.Errorf("day totals = %+v, want rx 300 tx 150", d)
	}
	if d.Processes["curl"] != 450 || d.Processes["wget"] != 60 {
		t.Errorf("per-process totals = %v", d.Processes)
	}

	// Older days are trimmed, newest kept.
	for i := 0; i < historyDays+10; i++ {
		day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format("2006-01-02")
		h = h.mergeSession(day, byteTotals{rx: 1}, nil, nil)
	}
	if len(h.Days) != historyDays {
		t.Errorf("history holds %d days, want the %d cap", len(h.Days), historyDays)
	}
	for i := 1; i < len(h.Days); i++ {
		if h.Days[i-1].Date > h.Days[i].Date {
			t.Fatalf("days out of order at %d: %s then %s", i, h.Days[i-1].Date, h.Days[i].Date)
		}
	}
}

func TestUsageHistoryTrimsPerDayNames(t *testing.T) {
	big := map[string]uint64{}
	for i := 0; i < historyTopN+40; i++ {
		big[string(rune('a'+i%26))+strings.Repeat("x", i)] = uint64(i + 1)
	}
	h := usageHistory{}.mergeSession("2026-08-10", byteTotals{rx: 1}, big, big)

	d := h.Days[0]
	if len(d.Processes) != historyTopN {
		t.Errorf("kept %d processes, want the %d cap", len(d.Processes), historyTopN)
	}
	// The biggest survive the trim.
	var smallest uint64 = ^uint64(0)
	for _, n := range d.Processes {
		if n < smallest {
			smallest = n
		}
	}
	if smallest <= 40 {
		t.Errorf("trim kept a small entry (%d); the biggest should survive", smallest)
	}
}

func TestUsageSavedOnQuit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := routesModel()
	m.sessionTotal = byteTotals{rx: 4096, tx: 2048}
	m.procTotals = map[int32]*talker{10: {label: "curl", totals: byteTotals{rx: 4096, tx: 2048}}}
	m.hostTotals = map[string]*talker{"1.2.3.4": {key: "1.2.3.4", label: "1.2.3.4", totals: byteTotals{rx: 4096}}}

	m.quit()

	h := LoadUsageHistory()
	if len(h.Days) != 1 {
		t.Fatalf("history has %d days, want 1", len(h.Days))
	}
	if h.Days[0].RxBytes != 4096 {
		t.Errorf("recorded %d received, want 4096", h.Days[0].RxBytes)
	}
	out := FormatUsage(0)
	if !strings.Contains(out, "curl") || !strings.Contains(out, "4.0 KiB") {
		t.Errorf("usage report:\n%s", out)
	}

	// Nothing to record is not an empty file full of zeroes.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	routesModel().quit()
	if got := FormatUsage(0); !strings.Contains(got, "no usage recorded") {
		t.Errorf("empty history report = %q", got)
	}
}

func TestDNSCachePruning(t *testing.T) {
	m := eventsModel()
	m.dnsCache = map[string]string{}
	m.dnsTried = map[string]bool{}
	// Genuinely distinct keys: the pruner only runs once the cap is exceeded.
	for i := 0; i < dnsCacheMax+50; i++ {
		ip := "198.51." + strconv.Itoa(i/256) + "." + strconv.Itoa(i%256)
		m.dnsCache[ip] = "host"
		m.dnsTried[ip] = true
	}
	if len(m.dnsCache) <= dnsCacheMax {
		t.Fatalf("test set up only %d entries, need more than the %d cap", len(m.dnsCache), dnsCacheMax)
	}

	live := conn(1, "curl", "10.0.0.1:1", "9.9.9.9:443", 0, 0)
	m.dnsCache["9.9.9.9"] = "dns9"
	m.pruneDNSCache([]probe.Conn{live})

	if len(m.dnsCache) > 10 {
		t.Errorf("cache still holds %d entries after pruning", len(m.dnsCache))
	}
	if m.dnsCache["9.9.9.9"] != "dns9" {
		t.Error("pruning dropped an address that is still connected")
	}
	if len(m.dnsTried) > 10 {
		t.Errorf("tried-set still holds %d entries", len(m.dnsTried))
	}
}

func TestEventLogRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	m, err := eventsModel().OpenEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	m.eventLogMax = 512 // rotate quickly rather than after 8 MiB

	for i := 0; i < 40; i++ {
		m.recordEvent(event{At: time.Now(), Level: levelInfo, Kind: "test",
			Subject: strings.Repeat("x", 40), Detail: "padding"})
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("previous generation missing: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	if info.Size() >= 512*2 {
		t.Errorf("current log is %d bytes; rotation did not reset it", info.Size())
	}
	if m.eventLogErr != nil {
		t.Errorf("rotation reported an error: %v", m.eventLogErr)
	}
	_ = m.eventLog.Close()
}
