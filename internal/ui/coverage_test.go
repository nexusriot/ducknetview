package ui

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// The small pure functions below are each one line of logic that the rest of
// the suite only reaches incidentally — a wrong answer in any of them shows up
// as a mislabelled cell rather than as a failure, which is the kind of bug
// that survives for releases.

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{0, "0s"},
		{42 * time.Second, "42s"},
		{3*time.Minute + 7*time.Second, "3m07s"},
		{time.Hour + 5*time.Minute, "1h05m"},
		{49 * time.Hour, "2d01h"},
	}
	for _, c := range cases {
		if got := formatDuration(c.in); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}

	if got := formatAge(time.Now(), time.Time{}); got != "-" {
		t.Errorf("a connection never seen has age %q, want -", got)
	}
	now := time.Now()
	if got := formatAge(now, now.Add(-90*time.Second)); got != "1m30s" {
		t.Errorf("formatAge = %q", got)
	}
}

func TestFormatCountAndRoundDuration(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want string
	}{{0, "0"}, {999, "999"}, {1500, "1.5k"}, {2_500_000, "2.5M"}} {
		if got := formatCount(c.in); got != c.want {
			t.Errorf("formatCount(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, c := range []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "2m0s"},
		{95 * time.Minute, "2h0m0s"},
	} {
		if got := roundDuration(c.in); got != c.want {
			t.Errorf("roundDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The scope ordering is what a "scope" sort means: the internet first,
// loopback last.
func TestScopeRankOrdering(t *testing.T) {
	order := []string{"public", "cgnat", "private", "link", "local"}
	for i := 1; i < len(order); i++ {
		if scopeRank(order[i-1]) >= scopeRank(order[i]) {
			t.Errorf("%s does not sort before %s", order[i-1], order[i])
		}
	}
	if scopeRank("nonsense") <= scopeRank("local") {
		t.Error("an unknown scope should sort last")
	}
}

// Colour is how a table says what a cell means, and the mapping is the part
// worth pinning down; the palette itself is a preference.
func TestStateAndScopeStyles(t *testing.T) {
	if stateStyle("ESTABLISHED").GetForeground() == stateStyle("TIME_WAIT").GetForeground() {
		t.Error("an established connection is drawn like one being torn down")
	}
	for _, teardown := range []string{"TIME_WAIT", "CLOSE_WAIT", "FIN_WAIT1", "LAST_ACK", "NONE", "-", ""} {
		if stateStyle(teardown).GetForeground() != subtleStyle.GetForeground() {
			t.Errorf("state %q is not dimmed", teardown)
		}
	}
	if stateStyle("SYN_SENT").GetForeground() != statePendStyle.GetForeground() {
		t.Error("a handshake in flight is not highlighted")
	}

	if scopeStyle("public").GetForeground() == scopeStyle("local").GetForeground() {
		t.Error("a public remote is drawn like a loopback one")
	}
	if scopeStyle("cgnat").GetForeground() != scopeStyle("private").GetForeground() {
		t.Error("CGNAT and private should read the same")
	}
	if exposureStyle("exposed").GetForeground() == exposureStyle("local").GetForeground() {
		t.Error("an exposed listener is drawn like a loopback one")
	}
	// An address that does not parse gets the plain style rather than a
	// misleading one.
	plain := lipgloss.NewStyle().GetForeground()
	if exposureStyle("").GetForeground() != plain || scopeStyle("").GetForeground() != plain {
		t.Error("an unclassifiable address was given a colour")
	}
}

// Both of these splice byte offsets into strings that may contain escape
// sequences and multi-byte runes, which is where mis-slicing would panic.
func TestANSIHelpersOnAwkwardInput(t *testing.T) {
	const esc = "\x1b"
	cases := []struct{ in, plain string }{
		{"plain", "plain"},
		{esc + "[31mred" + esc + "[0m", "red"},
		{esc + "]0;a title" + "\x07" + "after", "after"},
		{esc + "]0;a title" + esc + `\` + "after", "after"},
		{"naïve ünïcode", "naïve ünïcode"},
		{"日本語", "日本語"},
		{esc, ""},
		{esc + "[", ""},
	}
	for _, c := range cases {
		if got := stripANSI(c.in); got != c.plain {
			t.Errorf("stripANSI(%q) = %q, want %q", c.in, got, c.plain)
		}
	}

	// Truncation counts display cells, not bytes, and never cuts an escape
	// sequence in half.
	if got := ansiSafeTruncate("abcdef", 3); got != "abc" {
		t.Errorf("ansiSafeTruncate = %q", got)
	}
	if got := ansiSafeTruncate("", 5); got != "" {
		t.Errorf("ansiSafeTruncate(empty) = %q", got)
	}
	if got := ansiSafeTruncate("abc", 0); got != "" {
		t.Errorf("ansiSafeTruncate(width 0) = %q", got)
	}
	coloured := esc + "[31m" + "abcdef" + esc + "[0m"
	got := ansiSafeTruncate(coloured, 3)
	if stripANSI(got) != "abc" {
		t.Errorf("truncating coloured text gave %q (%q plain)", got, stripANSI(got))
	}
	if !strings.HasPrefix(got, esc+"[31m") {
		t.Errorf("the colour was dropped: %q", got)
	}
	// Wide runes take two cells.
	if plain := stripANSI(ansiSafeTruncate("日本語", 4)); plain != "日本" {
		t.Errorf("wide-rune truncation gave %q", plain)
	}
	// Invalid UTF-8 must not panic or loop.
	_ = ansiSafeTruncate("\xff\xfe abc", 4)
}

func TestSigName(t *testing.T) {
	for _, c := range []struct {
		sig  syscall.Signal
		want string
	}{
		{syscall.SIGTERM, "SIGTERM"},
		{syscall.SIGKILL, "SIGKILL"},
		{syscall.SIGHUP, "SIGHUP"},
	} {
		if got := sigName(c.sig); got != c.want {
			t.Errorf("sigName(%v) = %q, want %q", c.sig, got, c.want)
		}
	}
	if got := sigName(syscall.SIGUSR1); got == "" {
		t.Error("an unlisted signal should still have a name")
	}
}

func TestTabLabels(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < tabCount; i++ {
		label := tabLabel(tab(i))
		if label == "" || label == "?" {
			t.Errorf("tab %d has no label", i)
		}
		if seen[label] {
			t.Errorf("two tabs are both called %q", label)
		}
		seen[label] = true
	}
	if tabLabel(tab(99)) != "?" {
		t.Error("an impossible tab should not be given a name")
	}
}

// The "all clear" line quotes a count, and quoting one that includes the
// ephemeral sockets the check ignores would be a lie.
func TestBaselineComparableIgnoresEphemeralUDP(t *testing.T) {
	ports := []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:22"},
		{Proto: "udp", Local: "0.0.0.0:53"},
		{Proto: "udp", Local: "0.0.0.0:54321"}, // ephemeral: a client socket
	}
	if got := BaselineComparable(ports); got != 2 {
		t.Errorf("BaselineComparable = %d, want 2", got)
	}
	if got := BaselineComparable(nil); got != 0 {
		t.Errorf("BaselineComparable(nil) = %d", got)
	}
}

// A view's one-line summary is what makes a list of names readable a month
// later.
func TestDescribeView(t *testing.T) {
	got := describeView(savedView{
		Name: "vpn", Tab: int(tabConns),
		ProtoFilter: 1, IPVerFilter: 2, ConnStateFilter: 1,
		PublicOnly: true, OnlyMine: true, IfaceFilter: "wg0",
		ConnsGroup: true, ShowTotals: true, HideNoise: true,
		ConnsQuery: "443",
	})
	for _, want := range []string{"Connections", "tcp", "ipv6", "established", "public", "mine", "wg0", "grouped", "totals", "hide noise", "/443"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeView does not mention %q: %q", want, got)
		}
	}

	// A plain view describes itself with just its tab.
	if got := describeView(savedView{Tab: int(tabPorts)}); got != "Ports" {
		t.Errorf("describeView(plain) = %q", got)
	}
	// An out-of-range tab from a hand-edited config must not panic.
	if got := describeView(savedView{Tab: 99}); got == "" {
		t.Error("an out-of-range tab produced no description")
	}

	// The query shown is the one the view's own tab carries.
	for _, c := range []struct {
		v    savedView
		want string
	}{
		{savedView{Tab: int(tabPorts), PortsQuery: "p"}, "p"},
		{savedView{Tab: int(tabProcs), ProcsQuery: "q"}, "q"},
		{savedView{Tab: int(tabConns), ConnsQuery: "c"}, "c"},
		{savedView{Tab: int(tabEvents), EventsQuery: "e"}, "e"},
		{savedView{Tab: int(tabOverview), ConnsQuery: "c"}, ""},
	} {
		if got := viewQuery(c.v); got != c.want {
			t.Errorf("viewQuery(tab %d) = %q, want %q", c.v.Tab, got, c.want)
		}
	}
}

// n/N walk between matches on every tab that has rows, including the two
// alternative shapes the Connections tab can take.
func TestMatchingRowsOnEveryTab(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 48
	m.searchMode = searchHighlight // so nothing is filtered out
	m.ports = []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"},
		{Proto: "tcp", Local: "0.0.0.0:443", PID: 2, Process: "nginx"},
	}
	m.procs = []probe.ProcNet{
		{PID: 1, Name: "sshd"},
		{PID: 2, Name: "nginx", Cmdline: "nginx -g daemon off"},
	}
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:1", Remote: "1.1.1.1:443", State: "ESTABLISHED", PID: 2, Process: "nginx"},
		{Proto: "tcp", Local: "10.0.0.2:2", Remote: "2.2.2.2:22", State: "ESTABLISHED", PID: 1, Process: "sshd"},
	}
	m.closedConns = []closedConn{
		{conn: probe.Conn{Proto: "tcp", Local: "10.0.0.2:3", Remote: "3.3.3.3:443", Process: "nginx"}},
	}
	m.events = []event{
		{At: time.Now(), Level: levelWarn, Kind: "listener_up", Subject: "tcp 0.0.0.0:443"},
		{At: time.Now(), Level: levelInfo, Kind: "iface_state", Subject: "eth0"},
	}

	// The row order is whatever the active sort chose, so the assertion is
	// about which row matched rather than which index did.
	matched := func(name string, total int) int {
		t.Helper()
		got := m.matchingRows()
		if len(got) != total {
			t.Fatalf("%s: %d rows, want %d", name, len(got), total)
		}
		idx := -1
		for i, ok := range got {
			if !ok {
				continue
			}
			if idx >= 0 {
				t.Fatalf("%s: more than one row matched: %v", name, got)
			}
			idx = i
		}
		if idx < 0 {
			t.Fatalf("%s: nothing matched", name)
		}
		return idx
	}

	m.activeTab, m.portsQuery = tabPorts, "nginx"
	if got := m.filteredPorts()[matched("ports", 2)]; got.Process != "nginx" {
		t.Errorf("ports: matched %q", got.Process)
	}

	// The command line, which is the only thing telling two python3 rows apart.
	m.activeTab, m.procsQuery = tabProcs, "daemon off"
	if got := m.filteredProcs()[matched("procs", 2)]; got.Name != "nginx" {
		t.Errorf("procs: matched %q", got.Name)
	}

	m.activeTab, m.connsQuery = tabConns, "sshd"
	if got := m.filteredConns()[matched("conns", 2)]; got.Process != "sshd" {
		t.Errorf("conns: matched %q", got.Process)
	}

	m.connsGroup, m.connsQuery = true, "1.1.1.1"
	if got := m.filteredGroups()[matched("grouped", 2)]; got.ip != "1.1.1.1" {
		t.Errorf("grouped: matched %q", got.ip)
	}
	m.connsGroup = false

	m.showClosed, m.connsQuery = true, "3.3.3.3"
	if got := m.closedRows()[matched("closed", 1)]; got.conn.Remote != "3.3.3.3:443" {
		t.Errorf("closed: matched %q", got.conn.Remote)
	}
	m.showClosed = false

	m.activeTab, m.eventsQuery = tabEvents, "iface_state"
	if got := m.eventRows()[matched("events", 2)]; got.Kind != "iface_state" {
		t.Errorf("events: matched %q", got.Kind)
	}

	// No query means nothing to navigate between.
	m.eventsQuery = ""
	if got := m.matchingRows(); got != nil {
		t.Errorf("an empty query produced %v", got)
	}
	// And a tab with no rows at all.
	m.activeTab = tabOverview
	if got := m.matchingRows(); got != nil {
		t.Errorf("the Overview produced %v", got)
	}
}

// The line above a searched table has to say what the query is doing, and why
// it is doing nothing when the expression is broken.
func TestSearchStatus(t *testing.T) {
	m := NewModel()

	plain := stripANSI(m.searchStatus("nginx"))
	if !strings.Contains(plain, "filter: ") || !strings.Contains(plain, "nginx") {
		t.Errorf("searchStatus = %q", plain)
	}

	m.searchMode = searchHighlight
	if got := stripANSI(m.searchStatus("nginx")); !strings.Contains(got, "highlight: ") || !strings.Contains(got, "n/N jump") {
		t.Errorf("highlight mode: %q", got)
	}

	if got := stripANSI(m.searchStatus("re:^ngin")); !strings.Contains(got, "(regex)") {
		t.Errorf("a regex query is not announced: %q", got)
	}
	if got := stripANSI(m.searchStatus("re:*broken(")); !strings.Contains(got, "invalid regex") {
		t.Errorf("a broken regex does not say so: %q", got)
	}
}

// The settings overlay is the only way most people will ever set a threshold.
func TestSettingsOverlayEditing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, `{}`)

	m := NewModel().WithConfig()
	m.w, m.h = 160, 48
	m.openSettings()

	// Moving the cursor stays in range at both ends.
	for i := 0; i < 50; i++ {
		m.settingsKey("down")
	}
	if m.settingsCursor != len(settingsRows())-1 {
		t.Errorf("the cursor ran past the last row: %d", m.settingsCursor)
	}
	for i := 0; i < 50; i++ {
		m.settingsKey("up")
	}
	if m.settingsCursor != 0 {
		t.Errorf("the cursor ran past the first row: %d", m.settingsCursor)
	}

	// Typing a number into the first row (process throughput) sets it.
	m.settingsKey("enter")
	if !m.settingsEditing {
		t.Fatal("enter should start editing")
	}
	m.settingsInput.SetValue("2048")
	if status, _ := m.settingsKey("enter"); !strings.Contains(status, "2048") {
		t.Errorf("setting a threshold said %q", status)
	}
	if m.alertProcBps != 2048 {
		t.Errorf("alertProcBps = %v", m.alertProcBps)
	}

	// "off" disables it again.
	m.settingsKey("enter")
	m.settingsInput.SetValue("off")
	m.settingsKey("enter")
	if m.alertProcBps != 0 {
		t.Errorf("off left the threshold at %v", m.alertProcBps)
	}

	// Rubbish is refused with a reason, and changes nothing.
	m.settingsKey("enter")
	m.settingsInput.SetValue("soon")
	status, _ := m.settingsKey("enter")
	if !strings.Contains(status, "Not a number") {
		t.Errorf("a non-number was accepted: %q", status)
	}
	if m.alertProcBps != 0 {
		t.Errorf("a refused value was applied: %v", m.alertProcBps)
	}

	// Escape abandons an edit.
	m.settingsKey("enter")
	m.settingsInput.SetValue("999")
	if status, _ := m.settingsKey("esc"); status != "Cancelled" {
		t.Errorf("esc said %q", status)
	}
	if m.alertProcBps != 0 {
		t.Errorf("a cancelled edit was applied: %v", m.alertProcBps)
	}

	// A key the overlay does not claim closes it.
	if _, handled := m.settingsKey("Z"); handled {
		t.Error("the overlay claimed a key it has no use for")
	}
}

// Every write the tool makes goes through this, including to places it may not
// be allowed to write.
func TestWriteFileAtomicFailures(t *testing.T) {
	dir := t.TempDir()

	// A directory that cannot be created.
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(blocker, "nested", "config.json"), []byte("y"), 0o644); err == nil {
		t.Error("writing underneath a regular file should fail")
	}

	// The permissions asked for are the permissions set, whatever the umask.
	p := filepath.Join(dir, "perms.json")
	if err := writeFileAtomic(p, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %v, want 0600", got)
	}
}

// The interface list's items are what the list widget filters and draws.
func TestIfaceItem(t *testing.T) {
	it := ifaceItem{name: "eth0", desc: "physical  RX 1 B/s  TX 2 B/s"}
	if it.Title() != "eth0" || it.FilterValue() != "eth0" {
		t.Errorf("ifaceItem identifies itself as %q/%q", it.Title(), it.FilterValue())
	}
	if it.Description() == "" {
		t.Error("ifaceItem has no description")
	}
}

// The tickers are one line each, and a wrong one silently stops the session
// refreshing.
func TestTickersProduceTheirOwnMessages(t *testing.T) {
	for name, cmd := range map[string]tea.Cmd{
		"tick":      tickEvery(time.Millisecond),
		"extIP":     extIPTickEvery(time.Millisecond),
		"latency":   latencyTickEvery(time.Millisecond),
		"clipboard": copyToClipboardCmd("x", "thing"),
	} {
		msg := cmd()
		if msg == nil {
			t.Errorf("%s produced no message", name)
		}
	}
	if _, ok := tickEvery(time.Millisecond)().(tickMsg); !ok {
		t.Error("the refresh ticker does not produce a tickMsg")
	}
	if _, ok := extIPTickEvery(time.Millisecond)().(extIPTickMsg); !ok {
		t.Error("the external-IP ticker does not produce an extIPTickMsg")
	}
	if _, ok := latencyTickEvery(time.Millisecond)().(latencyTickMsg); !ok {
		t.Error("the latency ticker does not produce a latencyTickMsg")
	}
	// Measuring nothing is not a command at all, rather than one that returns
	// an empty round.
	if measureLatencyCmd(nil) != nil {
		t.Error("measuring no targets should produce no command")
	}
}
