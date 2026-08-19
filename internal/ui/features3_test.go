package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Aggregate bandwidth totals sum real interfaces and exclude loopback.
func TestTotalsExcludeLoopback(t *testing.T) {
	m := NewModel()
	m.w = 80
	snap := probe.NetSnapshot{
		TakenAt: time.Unix(1, 0),
		Ifaces: []probe.IfaceInfo{
			{Name: "eth0", Kind: probe.IfacePhysical, IsUp: true, RxBps: 100, TxBps: 200},
			{Name: "wg0", Kind: probe.IfaceVirt, IsUp: true, RxBps: 50, TxBps: 25},
			{Name: "lo", Kind: probe.IfaceLoopback, IsUp: true, RxBps: 9999, TxBps: 9999},
		},
	}
	model, _ := m.Update(snapMsg(snap))
	m = model.(Model)
	if len(m.totalRxHist) != 1 || m.totalRxHist[0] != 150 {
		t.Fatalf("total RX should be 150 (loopback excluded), got %v", m.totalRxHist)
	}
	if m.totalTxHist[0] != 225 {
		t.Fatalf("total TX should be 225, got %v", m.totalTxHist)
	}
}

// Connection deltas: first enumeration is a baseline (nothing new); the second
// flags appeared/closed connections.
func TestConnDelta(t *testing.T) {
	m := NewModel()
	c1 := probe.Conn{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:80", State: "ESTABLISHED", PID: 1}
	c2 := probe.Conn{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:80", State: "ESTABLISHED", PID: 2}

	model, _ := m.Update(connInfoMsg(probe.ConnInfo{Conns: []probe.Conn{c1, c2}}))
	m = model.(Model)
	if m.connAdded != 0 || len(m.newConns) != 0 {
		t.Fatalf("baseline enumeration should flag nothing new: added=%d new=%d", m.connAdded, len(m.newConns))
	}

	c3 := probe.Conn{Proto: "tcp", Local: "10.0.0.1:3", Remote: "3.3.3.3:80", State: "ESTABLISHED", PID: 3}
	model, _ = m.Update(connInfoMsg(probe.ConnInfo{Conns: []probe.Conn{c1, c3}}))
	m = model.(Model)
	if m.connAdded != 1 || m.connRemoved != 1 {
		t.Fatalf("expected +1/-1, got +%d/-%d", m.connAdded, m.connRemoved)
	}
	if !m.newConns[connKey(c3)] {
		t.Fatal("c3 should be flagged new")
	}
	if m.newConns[connKey(c1)] {
		t.Fatal("c1 (still present) should not be flagged new")
	}
}

func sampleFilterModel() Model {
	m := NewModel()
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "1.1.1.1:80", State: "ESTABLISHED", PID: 1},
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "2.2.2.2:80", State: "TIME_WAIT", PID: 2},
		{Proto: "udp", Local: "10.0.0.1:3", Remote: "3.3.3.3:53", State: "", PID: 3},
		{Proto: "tcp", Local: "fe80::1:4", Remote: "2606:4700::1:443", State: "ESTABLISHED", PID: 4},
	}
	return m
}

func TestConnFilters(t *testing.T) {
	m := sampleFilterModel()
	m.protoFilter = 1 // tcp
	if n := len(m.filteredConns()); n != 3 {
		t.Fatalf("tcp filter: expected 3, got %d", n)
	}

	m = sampleFilterModel()
	m.connStateFilter = 1 // established only
	if n := len(m.filteredConns()); n != 2 {
		t.Fatalf("established filter: expected 2, got %d", n)
	}

	m = sampleFilterModel()
	m.connStateFilter = 2 // active (exclude teardown)
	for _, c := range m.filteredConns() {
		if c.State == "TIME_WAIT" {
			t.Fatal("active filter must drop TIME_WAIT")
		}
	}
	if n := len(m.filteredConns()); n != 3 {
		t.Fatalf("active filter: expected 3, got %d", n)
	}

	m = sampleFilterModel()
	m.ipVerFilter = 2 // IPv6
	if n := len(m.filteredConns()); n != 1 {
		t.Fatalf("ipv6 filter: expected 1, got %d", n)
	}
	m.ipVerFilter = 1 // IPv4
	if n := len(m.filteredConns()); n != 3 {
		t.Fatalf("ipv4 filter: expected 3, got %d", n)
	}
}

// Arming a kill and pressing a signal key issues a command and clears the
// confirmation; any other key cancels.
func TestKillSignalChoice(t *testing.T) {
	arm := func() Model {
		m := NewModel()
		m.activeTab = tabProcs
		m.procs = []probe.ProcNet{{PID: 424242, Name: "victim", ConnCount: 1}}
		model, _ := m.Update(key("x"))
		return model.(Model)
	}

	for _, k := range []string{"t", "k", "h", "y"} {
		m := arm()
		if !m.confirmingKill {
			t.Fatalf("kill should be armed before %q", k)
		}
		model, cmd := m.Update(key(k))
		m = model.(Model)
		if m.confirmingKill {
			t.Fatalf("%q should resolve the confirmation", k)
		}
		if cmd == nil {
			t.Fatalf("%q should issue a signal command", k)
		}
	}

	m := arm()
	model, cmd := m.Update(key("n"))
	m = model.(Model)
	if m.confirmingKill || cmd != nil {
		t.Fatal("'n' should cancel with no command")
	}
}

func TestProcsSortByCPU(t *testing.T) {
	m := NewModel()
	m.procs = []probe.ProcNet{
		{PID: 1, Name: "a", CPU: 5},
		{PID: 2, Name: "b", CPU: 50},
		{PID: 3, Name: "c", CPU: 20},
	}
	m.procsSortCol = sortIndex(procsSortCols, "cpu")
	m.procsSortDesc = true
	if got := procNames(m.filteredProcs()); !eqStrs(got, []string{"b", "c", "a"}) {
		t.Fatalf("cpu desc order wrong: %v", got)
	}
}

// Export writes a CSV of the current view to the working directory.
func TestExportWritesCSV(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	m := NewModel()
	m.activeTab = tabPorts
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:443", PID: 1, Process: "nginx"}}

	cmd := exportViewCmd(m)
	if cmd == nil {
		t.Fatal("expected an export command")
	}
	res, ok := cmd().(exportResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("export failed: %+v", res)
	}
	b, err := os.ReadFile(res.path)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "proto,local,service,exposure,in_baseline,user,pid,process") || !strings.Contains(s, "nginx") {
		t.Fatalf("unexpected CSV contents:\n%s", s)
	}
}
