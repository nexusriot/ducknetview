package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/nexusriot/ducknetview/internal/probe"
)

func typeKey(t *testing.T, m Model, s string) Model {
	t.Helper()
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return model.(Model)
}

// TestMain sandboxes all config I/O to a throwaway dir so tests never read or
// clobber the developer's real ~/.config/ducknetview/config.json.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dnv-ui-test")
	if err != nil {
		os.Exit(m.Run())
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("HOME", dir) // macOS UserConfigDir ignores XDG and uses $HOME
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func writeConfig(t *testing.T, contents string) {
	t.Helper()
	p, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func ratesModel() Model {
	m := NewModel()
	m.connsVP.Width = 150
	m.connsVP.Height = 20
	m.procsVP.Width = 120
	m.procsVP.Height = 20
	m.portsVP.Width = 120
	m.portsVP.Height = 20
	return m
}

func indexOfCol(t *testing.T, cols []string, name string) int {
	t.Helper()
	for i, c := range cols {
		if c == name {
			return i
		}
	}
	t.Fatalf("sort column %q not found in %v", name, cols)
	return 0
}

// feed pushes a socket enumeration through Update the way the refresh command
// does, so the event/diff bookkeeping runs exactly as it does at runtime.
func feed(t *testing.T, m Model, ports []probe.ListenPort, conns []probe.Conn) Model {
	t.Helper()
	model, _ := m.Update(connInfoMsg(probe.ConnInfo{Ports: ports, Conns: conns}))
	return model.(Model)
}

func eventsModel() Model {
	m := NewModel()
	m.portsVP.Width, m.portsVP.Height = 120, 20
	m.connsVP.Width, m.connsVP.Height = 150, 20
	m.procsVP.Width, m.procsVP.Height = 120, 20
	m.eventsVP.Width, m.eventsVP.Height = 130, 20
	return m
}

func kindsOf(events []event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

func countKind(events []event, kind string) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func totalsModel() Model {
	m := ratesModel()
	m.w, m.h = 160, 45
	return m
}

// conn builds a connection with cumulative counters, as the probe layer
// reports it.
func conn(pid int32, proc, local, remote string, rx, tx uint64) probe.Conn {
	return probe.Conn{
		Proto: "tcp", Local: local, Remote: remote, State: "ESTABLISHED",
		PID: pid, Process: proc, RxBytes: rx, TxBytes: tx,
	}
}

// withColor forces a color profile for the duration of a test: with no TTY
// lipgloss renders every style as plain text, so styling assertions would
// otherwise always fail.
func withColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func routesModel() Model {
	m := eventsModel()
	m.w, m.h = 160, 45
	m.routesVP.Width, m.routesVP.Height = 130, 30
	m.ifaceDetailsVP.Width, m.ifaceDetailsVP.Height = 80, 20
	return m
}

// A host running one container: nginx inside it on 0.0.0.0:80 (published by
// docker-proxy on host 8080) and redis on 0.0.0.0:6379 (not published), plus a
// host sshd. Both containers' loopback connections share a tuple with the
// host's, which is what the namespace in the keys is for.
func netnsPorts() []probe.ListenPort {
	return []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"},
		{Proto: "tcp", Local: "0.0.0.0:8080", PID: 2, Process: "docker-proxy"},
		{Proto: "tcp", Local: "0.0.0.0:80", PID: 10, Process: "nginx", NetNS: "4f1a2b3c4d5e", Published: "0.0.0.0:8080"},
		{Proto: "tcp", Local: "0.0.0.0:6379", PID: 20, Process: "redis-server", NetNS: "9e8d7c6b5a4f"},
	}
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+e":
		return tea.KeyMsg{Type: tea.KeyCtrlE}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// --- live metrics --------------------------------------------------------

func statsModel() Model {
	m := NewModel()
	m.w, m.h = 160, 45
	m.lastSnap = probe.NetSnapshot{
		Hostname: "statshost", TakenAt: time.Now(),
		Ifaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true, RxBps: 1000, TxBps: 2000}},
	}
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}}
	m.procs = []probe.ProcNet{{PID: 1, Name: "sshd"}}
	m.conns = []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:4001", Remote: "8.8.8.8:443", State: "ESTABLISHED", PID: 1}}
	m.sessionTotal = byteTotals{rx: 111, tx: 222}
	m.polls = 7
	m.lastPollAt = time.Now().Add(-3 * time.Second)
	return m
}

// tea.Msg satisfaction is what lets main send these through the program.
var (
	_ tea.Msg = StatsRequest{}
	_ tea.Msg = ReloadMsg{}
	_ tea.Msg = DaemonShutdownMsg{}
)

// --- drill-down ----------------------------------------------------------

func drillModel(t *testing.T) Model {
	t.Helper()
	m := NewModel()
	m.w, m.h = 160, 45
	m.connsVP.Width, m.connsVP.Height = 140, 30
	m.procsVP.Width, m.procsVP.Height = 140, 30
	m.portsVP.Width, m.portsVP.Height = 140, 30

	m.procs = []probe.ProcNet{
		{PID: 10, Name: "firefox", ConnCount: 2},
		{PID: 11, Name: "syncthing", ConnCount: 1},
	}
	m.ports = []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:8080", PID: 12, Process: "nginx"},
		{Proto: "tcp", Local: "0.0.0.0:22", PID: 13, Process: "sshd"},
	}
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:4001", Remote: "1.1.1.1:443", State: "ESTABLISHED", PID: 10, Process: "firefox"},
		{Proto: "tcp", Local: "10.0.0.2:4002", Remote: "8.8.8.8:443", State: "ESTABLISHED", PID: 10, Process: "firefox"},
		{Proto: "tcp", Local: "10.0.0.2:4003", Remote: "9.9.9.9:443", State: "ESTABLISHED", PID: 11, Process: "syncthing"},
		{Proto: "tcp", Local: "10.0.0.2:8080", Remote: "203.0.113.7:51000", State: "ESTABLISHED", PID: 12, Process: "nginx"},
	}
	return m
}

// --- network ownership ---------------------------------------------------

func ownerModel(t *testing.T) Model {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ip2asn.tsv")
	body := "1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET\n" +
		"8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewModel()
	m.w, m.h = 170, 45
	m.connsVP.Width, m.connsVP.Height = 150, 30
	m.asnPath = p
	m.loadASNDB()
	if m.asnErr != nil {
		t.Fatalf("loadASNDB: %v", m.asnErr)
	}
	return m
}
