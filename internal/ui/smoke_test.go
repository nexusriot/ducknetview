package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// This round added two overlays, a tree view, a conditional column and a drill
// filter, all of which size themselves against the terminal. A key that
// panics only on an 80-column terminal is the kind of thing no targeted test
// catches, so every binding is pressed on every tab at several sizes.

func smokeModel() Model {
	m := NewModel()
	m.lastSnap = probe.NetSnapshot{
		Hostname: "smoke", Uptime: time.Hour, TakenAt: time.Now(),
		Ifaces: []probe.IfaceInfo{
			{Name: "eth0", IsUp: true, RxBps: 1000, TxBps: 2000, Addrs: []string{"10.0.0.2/24"}},
			{Name: "wg0", IsUp: true, Addrs: []string{"10.1.0.2/24"}},
			{Name: "lo", IsUp: true, Kind: probe.IfaceLoopback},
		},
	}
	m.ports = []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:8080", PID: 12, Process: "nginx", User: "www"},
		{Proto: "udp", Local: "127.0.0.1:53", PID: 13, Process: "resolved", User: "root"},
	}
	m.procs = []probe.ProcNet{
		{PID: 10, PPID: 1, Name: "firefox", ConnCount: 2, User: "vlad", Container: "abc123def456"},
		{PID: 11, PPID: 10, Name: "firefox", ConnCount: 1, User: "vlad"},
	}
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:4001", Remote: "8.8.8.8:443", State: "ESTABLISHED", PID: 10, Process: "firefox", RxBytes: 100, TxBytes: 200, RTT: time.Millisecond},
		{Proto: "tcp", Local: "10.1.0.2:4002", Remote: "1.1.1.1:443", State: "TIME_WAIT", PID: 11, Process: "firefox"},
		{Proto: "udp", Local: "10.0.0.2:4003", Remote: "192.168.1.1:53", State: "NONE", PID: 13, Process: "resolved"},
	}
	m.routes = []probe.Route{{Destination: "default", Gateway: "10.0.0.1", Iface: "eth0"}}
	m.neighbours = []probe.Neighbour{{IP: "fe80::1", MAC: "e0:1f:2b:51:1c:31", Iface: "eth0", IPv6: true, State: "stale"}}
	m.resolvers = []probe.Resolver{{Address: "127.0.0.53", Stub: true}}
	m.hasThroughput = true
	m.connSeen, m.portSeen = true, true
	m.events = []event{{At: time.Now(), Level: levelAlert, Kind: "listener_up", Subject: "tcp 0.0.0.0:8080"}}
	m.trend.record(time.Now().Add(-time.Minute), 1000, 2000)
	m.trend.record(time.Now(), 3000, 4000)
	m.recordLatency([]probe.LatencySample{{Label: "gateway", Addr: "10.0.0.1:53", RTT: 2 * time.Millisecond, At: time.Now()}})
	return m
}

// smokeKeys is every binding the tool has, plus navigation.
var smokeKeys = []string{
	"?", ",", "1", "2", "3", "4", "5", "6", "7", "tab", "shift+tab",
	"up", "down", "pgup", "pgdown", "home", "end",
	"/", "esc", "ctrl+u", "\\", "n", "N",
	"s", "S", "t", "U", "A", "T", "F", "h", "r", "g", "z", "i",
	"p", "v", "f", "o", "u", "a", "w", "b", "B", "l", "d", "c",
	"enter", "y", "e", "x", "R", " ", "+", "-", "ctrl+e",
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

func TestEveryKeyOnEveryTabAtEverySize(t *testing.T) {
	sizes := [][2]int{{80, 24}, {100, 30}, {160, 45}, {220, 60}}

	for _, size := range sizes {
		for startTab := tab(0); startTab < tabCount; startTab++ {
			for _, key := range smokeKeys {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("%dx%d tab %v key %q panicked: %v",
								size[0], size[1], startTab, key, r)
						}
					}()

					m := smokeModel()
					sized, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
					m = sized.(Model)
					m.activeTab = startTab

					out, _ := m.Update(keyMsg(key))
					got := out.(Model)
					// Rendering is where a bad width actually bites.
					if v := got.View(); v == "" {
						t.Fatalf("%dx%d tab %v key %q rendered nothing", size[0], size[1], startTab, key)
					}
				}()
			}
		}
	}
}

// The overlays size themselves independently of the tables, and a short
// terminal is where a fixed-height box overflows.
func TestOverlaysRenderAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{80, 10}, {80, 24}, {200, 60}} {
		m := smokeModel()
		out, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = out.(Model)

		for name, render := range map[string]func() string{
			"help":     m.renderHelp,
			"settings": func() string { m.openSettings(); return m.renderSettings() },
			"overview": m.viewOverview,
			"routes":   m.viewRoutes,
		} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%dx%d %s panicked: %v", size[0], size[1], name, r)
					}
				}()
				txt := render()
				if strings.TrimSpace(stripANSI(txt)) == "" {
					t.Errorf("%dx%d %s rendered nothing", size[0], size[1], name)
				}
			}()
		}
	}
}

// A drill, a tree and an owner column all change the layout; rendering with
// each on is the combination most likely to overflow a narrow terminal.
func TestNarrowTerminalWithEverythingOn(t *testing.T) {
	m := smokeModel()
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(Model)
	m.procTree = true
	m.activeTab = tabProcs
	if _, ok := m.drillInto(); !ok {
		t.Fatal("could not drill from the process list")
	}

	for _, line := range strings.Split(stripANSI(m.View()), "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("line of %d runes on an 80-column terminal: %q", len([]rune(line)), line)
		}
	}
}
