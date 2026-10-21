package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nexusriot/ducknetview/internal/probe"
)

// While a search box is focused, global single-key bindings (q, s, tab, /, and
// the arrow keys) must reach the text input instead of being swallowed or
// switching tabs. Regression test for the global key switch stealing keys.
func TestSearchKeysReachInput(t *testing.T) {
	m := NewModel()
	m.activeTab = tabProcs

	m = typeKey(t, m, "/")
	if !m.procsSearching {
		t.Fatal("'/' should open the procs search box")
	}

	// 's' and 'q' are global bindings (sort / quit-noop) that previously got
	// captured; every character here must land in the input.
	for _, r := range "squid" {
		m = typeKey(t, m, string(r))
	}
	if got := m.procsSearch.Value(); got != "squid" {
		t.Fatalf("search input = %q, want %q (global keys still swallowed?)", got, "squid")
	}

	// Arrow keys must edit within the input, not switch tabs.
	before := m.activeTab
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = model.(Model)
	if m.activeTab != before {
		t.Fatalf("left arrow switched tabs while searching: %v", m.activeTab)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = model.(Model)
	if !m.procsSearching {
		t.Fatal("'q' while searching should not exit/alter search mode")
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

// The command line is what separates forty rows of the same name.
func TestProcessSearchCoversTheCommandLine(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 48
	m.procs = []probe.ProcNet{
		{PID: 1, Name: "python3", Cmdline: "python3 /opt/billing/worker.py"},
		{PID: 2, Name: "python3", Cmdline: "python3 /usr/bin/ansible"},
	}

	m.procsQuery = "billing"
	rows := m.filteredProcs()
	if len(rows) != 1 || rows[0].PID != 1 {
		t.Fatalf("searching the command line matched %d rows: %+v", len(rows), rows)
	}

	// And the name still matches both.
	m.procsQuery = "python3"
	if rows := m.filteredProcs(); len(rows) != 2 {
		t.Errorf("the name search should still match both, got %d", len(rows))
	}
}

func TestMatcherLiteralAndRegex(t *testing.T) {
	lit := newMatcher("Curl")
	if !lit.match("curl") || !lit.match("CURL") {
		t.Error("literal matching should be case-insensitive")
	}
	if lit.match("wget") {
		t.Error("literal matcher matched an unrelated field")
	}

	re := newMatcher(`re:^10\.0\..*:443$`)
	if re.err != nil {
		t.Fatalf("valid expression rejected: %v", re.err)
	}
	if !re.match("10.0.0.5:443") {
		t.Error("regex should match the address it describes")
	}
	if re.match("10.0.0.5:80") || re.match("192.168.1.5:443") {
		t.Error("regex matched an address outside its pattern")
	}
	// Regexes are case-insensitive too.
	if !newMatcher("re:FIREFOX").match("firefox-bin") {
		t.Error("regex should be case-insensitive")
	}

	// A broken expression matches nothing and says why, rather than matching
	// everything and looking like the filter is broken.
	bad := newMatcher("re:[unclosed")
	if bad.err == nil {
		t.Fatal("invalid expression accepted")
	}
	if bad.match("anything") {
		t.Error("invalid expression should match nothing")
	}

	if !newMatcher("").match("anything") {
		t.Error("an empty query must accept everything")
	}
}

func TestMatcherHighlight(t *testing.T) {
	withColor(t)

	got := stripANSI(newMatcher("curl").highlight("curl and curl"))
	if got != "curl and curl" {
		t.Errorf("highlighting changed the text: %q", got)
	}
	if !strings.Contains(newMatcher("curl").highlight("curl"), "\x1b") {
		t.Error("literal match was not styled")
	}
	if !strings.Contains(newMatcher("re:cu.l").highlight("curl"), "\x1b") {
		t.Error("regex match was not styled")
	}
	if strings.Contains(newMatcher("re:[bad").highlight("curl"), "\x1b") {
		t.Error("a broken expression should not style anything")
	}
}

func TestRegexSearchFiltersRows(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns
	m.conns = []probe.Conn{
		conn(1, "curl", "10.0.0.1:5000", "1.2.3.4:443", 0, 0),
		conn(2, "wget", "10.0.0.1:6000", "5.6.7.8:80", 0, 0),
	}

	m.connsQuery = `re:(curl|nope)`
	rows := m.filteredConns()
	if len(rows) != 1 || rows[0].Process != "curl" {
		t.Fatalf("regex filter kept %d rows, want just curl", len(rows))
	}

	// The failing expression hides everything and the status line explains it.
	m.connsQuery = "re:[bad"
	if got := len(m.filteredConns()); got != 0 {
		t.Errorf("broken expression kept %d rows, want none", got)
	}
	if !strings.Contains(m.searchStatus(m.connsQuery), "invalid regex") {
		t.Errorf("search status should explain the bad expression: %q", m.searchStatus(m.connsQuery))
	}
}

func TestHighlightModeKeepsRowsAndJumps(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabProcs
	m.procs = []probe.ProcNet{
		{PID: 1, Name: "alpha"}, {PID: 2, Name: "target"},
		{PID: 3, Name: "beta"}, {PID: 4, Name: "target-two"},
	}
	m.procsQuery = "target"
	withColor(t)
	m.procsSortCol = indexOfCol(t, procsSortCols, "pid")
	m.procsSortDesc = false

	// Filter mode hides the non-matching rows.
	if got := len(m.filteredProcs()); got != 2 {
		t.Fatalf("filter mode kept %d rows, want 2", got)
	}

	model, _ := m.Update(key("\\"))
	m = model.(Model)
	if m.searchMode != searchHighlight {
		t.Fatal("\\ should switch to highlight mode")
	}
	if got := len(m.filteredProcs()); got != 4 {
		t.Errorf("highlight mode kept %d rows, want all 4", got)
	}
	// The matches are still marked.
	if !strings.Contains(m.renderProcsText(), "\x1b") {
		t.Error("highlight mode should style the matching rows")
	}

	// n walks to the next matching row, wrapping at the end.
	m.procsCursor = 0
	model, _ = m.Update(key("n"))
	m = model.(Model)
	if m.procsCursor != 1 {
		t.Errorf("n moved the cursor to %d, want the first match (1)", m.procsCursor)
	}
	model, _ = m.Update(key("n"))
	m = model.(Model)
	if m.procsCursor != 3 {
		t.Errorf("n moved the cursor to %d, want the second match (3)", m.procsCursor)
	}
	model, _ = m.Update(key("n"))
	m = model.(Model)
	if m.procsCursor != 1 {
		t.Errorf("n should wrap to the first match, got %d", m.procsCursor)
	}
	// N walks backwards.
	model, _ = m.Update(key("N"))
	m = model.(Model)
	if m.procsCursor != 3 {
		t.Errorf("N moved the cursor to %d, want the previous match (3)", m.procsCursor)
	}

	// With no query there is nothing to jump between.
	m.procsQuery = ""
	model, _ = m.Update(key("n"))
	if got := model.(Model).status; !strings.Contains(got, "No search query") {
		t.Errorf("status = %q, want it to explain there is no query", got)
	}
}

func TestQuickFiltersAndTabPersist(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := totalsModel()
	m.activeTab = tabConns
	m.protoFilter = 1
	m.ipVerFilter = 2
	m.connStateFilter = 1
	m.publicOnly = true
	m.onlyMine = true
	m.currentUser = "vlad"
	m.searchMode = searchHighlight
	m.applyFilterChange()
	m.saveConfig()

	restored := NewModel().WithConfig()
	if restored.protoFilter != 1 || restored.ipVerFilter != 2 || restored.connStateFilter != 1 {
		t.Errorf("filters not restored: proto=%d ipver=%d state=%d",
			restored.protoFilter, restored.ipVerFilter, restored.connStateFilter)
	}
	if !restored.publicOnly || !restored.onlyMine {
		t.Error("boolean filters not restored")
	}
	if restored.activeTab != tabConns {
		t.Errorf("last tab = %v, want the connections tab", restored.activeTab)
	}
	if restored.searchMode != searchHighlight {
		t.Error("search mode not restored")
	}
}

// Match navigation was the one pair of keys the handler understood but the
// keymap did not, which made the "rebind any action" claim untrue.
func TestSearchNavigationIsRebindable(t *testing.T) {
	m := drillModel(t)
	m.activeTab = tabConns
	m.connsQuery = "8.8.8.8"
	m.searchMode = searchHighlight
	m.keymap = newKeymap(map[string]string{"search-next": "]", "search-prev": "["})

	if got := m.keymap.translate("]"); got != "n" {
		t.Errorf("translate(]) = %q, want n", got)
	}
	if got := m.keymap.translate("["); got != "N" {
		t.Errorf("translate([) = %q, want N", got)
	}

	before := m.connsCursor
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if out.(Model).connsCursor == before && len(m.filteredConns()) > 1 {
		t.Error("the rebound match-navigation key did not move the cursor")
	}
}
