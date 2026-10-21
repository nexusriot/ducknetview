package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestConnsTableThroughputColumns(t *testing.T) {
	m := ratesModel()
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:5000", Remote: "1.2.3.4:443", State: "ESTABLISHED", PID: 10, Process: "curl"},
	}

	if strings.Contains(m.renderConnsText(), " RX ") {
		t.Error("RX column shown before any throughput data was available")
	}

	m.hasThroughput = true
	m.connRates = map[string]rateSample{connKey(m.conns[0]): {rx: 2 * 1024 * 1024, tx: 4096}}

	text := m.renderConnsText()
	if !strings.Contains(text, "RX") || !strings.Contains(text, "TX") {
		t.Fatalf("throughput columns missing:\n%s", text)
	}
	// Rates render compactly so the columns stay narrow.
	if !strings.Contains(text, "2.0M") || !strings.Contains(text, "4.0k") {
		t.Errorf("compact rates missing from row:\n%s", text)
	}
	// The remote port's service name is resolved for connections too.
	if !strings.Contains(text, "https") {
		t.Errorf("SVC column missing https for port 443:\n%s", text)
	}
}

func TestProcsTableUserAndContainerColumns(t *testing.T) {
	m := ratesModel()
	m.procs = []probe.ProcNet{{PID: 42, Name: "nginx", User: "www-data", ConnCount: 3}}

	text := m.renderProcsText()
	if !strings.Contains(text, "USER") || !strings.Contains(text, "www-data") {
		t.Fatalf("USER column missing:\n%s", text)
	}
	if strings.Contains(text, "CONTAINER") {
		t.Error("CONTAINER column shown although no process is containerised")
	}

	m.procs[0].Container = "3f2a1b4c5d6e"
	text = m.renderProcsText()
	if !strings.Contains(text, "CONTAINER") || !strings.Contains(text, "3f2a1b4c5d6e") {
		t.Fatalf("CONTAINER column missing once a container is present:\n%s", text)
	}
}

func TestPortsTableUserColumn(t *testing.T) {
	m := ratesModel()
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd", User: "root"}}

	text := m.renderPortsText()
	if !strings.Contains(text, "USER") || !strings.Contains(text, "root") {
		t.Fatalf("USER column missing:\n%s", text)
	}
}

func TestConnsQualityColumns(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns
	m.hasThroughput = true
	m.conns = []probe.Conn{{
		Proto: "tcp", Local: "10.0.0.1:5000", Remote: "1.2.3.4:443", State: "ESTABLISHED",
		PID: 10, Process: "curl", RxBytes: 1, RTT: 134 * time.Millisecond, Retrans: 7,
	}}

	text := m.renderConnsText()
	if !strings.Contains(text, "RTT") || !strings.Contains(text, "RTX") {
		t.Fatalf("quality columns missing:\n%s", text)
	}
	if !strings.Contains(text, "134ms") || !strings.Contains(text, "7") {
		t.Errorf("RTT/retransmits missing from the row:\n%s", text)
	}
}

func TestColumnHitTesting(t *testing.T) {
	cols := colSpecs{
		{key: "a", title: "AA", width: 4, sort: "alpha"},
		{key: "b", title: "BB", width: 3},
		{key: "c", title: "CC", width: 5, sort: "gamma"},
	}

	// Layout: [0,4) a, [4,6) gap, [6,9) b, [9,11) gap, [11,16) c
	cases := map[int]string{0: "alpha", 3: "alpha", 4: "", 6: "", 8: "", 11: "gamma", 15: "gamma", 99: ""}
	for x, want := range cases {
		got, ok := cols.keyAt(x)
		if want == "" && ok {
			t.Errorf("keyAt(%d) = %q, want no hit", x, got)
		}
		if want != "" && (!ok || got != want) {
			t.Errorf("keyAt(%d) = (%q, %v), want %q", x, got, ok, want)
		}
	}

	if got := cols.span(); got != 16 {
		t.Errorf("span = %d, want 16", got)
	}
}

// Every view of the Connections tab must hit-test against its own layout.
func TestActiveColumnsFollowTheConnsView(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns

	if got := m.activeColumns().width("state"); got == 0 {
		t.Error("flat view columns not selected")
	}
	m.connsGroup = true
	if got := m.activeColumns().width("states"); got == 0 {
		t.Error("grouped view columns not selected")
	}
	m.showClosed = true
	if got := m.activeColumns().width("life"); got == 0 {
		t.Error("closed view columns not selected")
	}
}

func TestHiddenColumns(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabConns
	m.conns = []probe.Conn{conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 0, 0)}

	if !strings.Contains(m.renderConnsText(), "SVC") {
		t.Fatal("the service column should be shown by default")
	}

	m.hiddenCols = map[string]bool{"svc": true, "age": true}
	text := m.renderConnsText()
	if strings.Contains(text, "SVC") || strings.Contains(text, "AGE") {
		t.Errorf("hidden columns still rendered:\n%s", text)
	}
	if !strings.Contains(text, "REMOTE") {
		t.Errorf("hiding two columns should not disturb the rest:\n%s", text)
	}
	// Rows stay aligned with the header: nothing may exceed the viewport. Width
	// is measured in display columns, not bytes — the rule is drawn with a
	// three-byte rune.
	for _, line := range strings.Split(stripANSI(text), "\n") {
		if w := lipgloss.Width(line); w > m.connsVP.Width {
			t.Errorf("line %d columns wide, viewport is %d: %q", w, m.connsVP.Width, line)
		}
	}
	// A hidden column is still sortable, since the data is only off the screen.
	if _, ok := m.connsColumns().keyAt(0); !ok {
		t.Error("remaining columns should still be hit-testable")
	}

	// Hiding everything would look like a bug, so the layout refuses.
	m.hiddenCols = map[string]bool{}
	for _, c := range m.connsColumns() {
		m.hiddenCols[c.key] = true
	}
	if len(m.connsColumns()) == 0 {
		t.Error("hiding every column should fall back to showing them")
	}
}

func TestHiddenColumnsPersist(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := eventsModel()
	m.hiddenCols = map[string]bool{"svc": true}
	m.saveConfig()

	reloaded := NewModel().WithConfig()
	if !reloaded.hiddenCols["svc"] {
		t.Errorf("hidden columns did not survive a reload: %v", reloaded.hiddenCols)
	}
}

// On a single-NIC host the column would say the same thing on every row.
func TestInterfaceColumnHiddenOnSingleLink(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabConns
	m.lastSnap = probe.NetSnapshot{Ifaces: []probe.IfaceInfo{{Name: "eth0", Addrs: []string{"192.168.1.5/24"}}}}
	m = feed(t, m, nil, []probe.Conn{
		conn(1, "a", "192.168.1.5:1", "1.1.1.1:443", 0, 0),
		conn(2, "b", "192.168.1.5:2", "2.2.2.2:443", 0, 0),
	})

	if m.multiIface() {
		t.Error("a single interface should not warrant the column")
	}
	if strings.Contains(m.renderConnsText(), "IFACE") {
		t.Errorf("IFACE column shown on a single-link host:\n%s", m.renderConnsText())
	}
}

func TestNetNSColumnOnlyWithNamespaces(t *testing.T) {
	m := eventsModel()
	m = feed(t, m, netnsPorts()[:2], nil)
	if m.anyNetNS() || strings.Contains(m.renderPortsText(), "NETNS") {
		t.Fatal("NETNS column shown on a host with no other namespace")
	}

	m = feed(t, m, netnsPorts(), []probe.Conn{
		conn(10, "nginx", "127.0.0.1:80", "127.0.0.1:40000", 0, 0),
	})
	m.connsVP.Width = 200
	ports := m.renderPortsText()
	for _, want := range []string{"NETNS", "4f1a2b3c4d5e", "9e8d7c6b5a4f", probe.HostNetNSLabel} {
		if !strings.Contains(ports, want) {
			t.Errorf("ports table lacks %q:\n%s", want, ports)
		}
	}
	if !strings.Contains(m.renderConnsText(), "NETNS") {
		t.Errorf("connections table lacks the NETNS column:\n%s", m.renderConnsText())
	}
}

// runeIndex is strings.Index in characters rather than bytes, so a comparison
// against a column position is not thrown off by the cursor gutter's arrow.
func runeIndex(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return len([]rune(s[:i]))
}

// Hiding a column drops it from the header, so the rows have to drop it too.
func TestHiddenColumnKeepsProcessRowsAligned(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 48
	m.procsVP.Width = 120
	m.procs = []probe.ProcNet{
		{PID: 42, Name: "curl", User: "vlad", ConnCount: 7, CPU: 1.5, Mem: 0.25},
	}
	m.hiddenCols = map[string]bool{"cpu": true}

	lines := strings.Split(stripANSI(m.renderProcsText()), "\n")
	if len(lines) < 6 {
		t.Fatalf("unexpected table shape: %q", lines)
	}
	header, row := lines[3], lines[5]
	if strings.Contains(header, "CPU%") {
		t.Fatalf("hidden column still in the header: %q", header)
	}
	for _, col := range []struct{ title, cell string }{
		{"USER", "vlad"}, {"MEM%", "0.2"}, {"CONNS", "7"},
	} {
		if h, r := runeIndex(header, col.title), runeIndex(row, col.cell); h != r {
			t.Errorf("%s: header at %d, cell at %d\nhdr %q\nrow %q", col.title, h, r, header, row)
		}
	}
}

// --- column key catalogue ------------------------------------------------

// AllColumnKeys drives the settings validation and the documentation; if it
// drifts from what the renderers declare, hiding a real column silently fails.
func TestAllColumnKeysMatchesTheRenderers(t *testing.T) {
	m := ownerModel(t) // a model wide enough to declare every conditional column
	m.procsVP.Width, m.portsVP.Width = 150, 150
	m.hasThroughput = true
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:1", Remote: "8.8.8.8:443"},
		{Proto: "tcp", Local: "10.1.0.2:2", Remote: "1.0.0.1:443"},
	}
	m.lastSnap.Ifaces = []probe.IfaceInfo{
		{Name: "eth0", Addrs: []string{"10.0.0.2/24"}},
		{Name: "wg0", Addrs: []string{"10.1.0.2/24"}},
	}
	m.connIface = map[string]string{
		connKey(m.conns[0]): "eth0",
		connKey(m.conns[1]): "wg0",
	}
	m.procs = []probe.ProcNet{{PID: 1, Name: "x", Container: "abc123"}}

	declared := map[string]bool{}
	for _, k := range AllColumnKeys() {
		declared[k] = true
	}
	for _, k := range m.columnKeysInUse() {
		if !declared[k] {
			t.Errorf("column %q is rendered but missing from AllColumnKeys", k)
		}
	}
}

// The UNIT column appears only when some row has a unit, sorts, hides, and
// takes part in search.
func TestUnitColumn(t *testing.T) {
	m := eventsModel()
	m.procs = []probe.ProcNet{{PID: 1, Name: "sshd"}, {PID: 2, Name: "nginx"}}
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}}
	if m.procsColumns().width("unit") != 0 || m.portsColumns().width("unit") != 0 {
		t.Fatal("UNIT column shown with no units")
	}

	m.procs[1].Unit = "nginx.service"
	m.ports[0].Unit = "ssh.service"
	for name, cols := range map[string]colSpecs{"procs": m.procsColumns(), "ports": m.portsColumns()} {
		var unit *colSpec
		for i := range cols {
			if cols[i].key == "unit" {
				unit = &cols[i]
			}
		}
		if unit == nil || unit.sort != "unit" || unit.title != "UNIT" {
			t.Errorf("%s: UNIT column = %+v, want a sortable UNIT column", name, unit)
		}
	}
	if !strings.Contains(m.renderProcsText(), "nginx.service") || !strings.Contains(m.renderPortsText(), "ssh.service") {
		t.Error("unit names not rendered")
	}

	m.procsQuery = "nginx.service"
	if rows := m.filteredProcs(); len(rows) != 1 || rows[0].PID != 2 {
		t.Errorf("search by unit = %+v, want only nginx", rows)
	}
	m.portsQuery = "ssh.service"
	if rows := m.filteredPorts(); len(rows) != 1 {
		t.Errorf("ports search by unit = %d rows, want 1", len(rows))
	}

	m.hiddenCols = map[string]bool{"unit": true}
	if m.procsColumns().width("unit") != 0 {
		t.Error("hidden UNIT column still rendered")
	}
}
