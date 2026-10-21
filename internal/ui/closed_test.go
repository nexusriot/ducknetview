package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestClosedConnectionsAreRetained(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns

	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 1_000_000, 250_000)
	m = feed(t, m, nil, []probe.Conn{c})
	if len(m.closedConns) != 0 {
		t.Fatalf("first enumeration recorded %d closures", len(m.closedConns))
	}

	// The connection goes away.
	m = feed(t, m, nil, nil)

	if len(m.closedConns) != 1 {
		t.Fatalf("closed connections = %d, want 1", len(m.closedConns))
	}
	cc := m.closedConns[0]
	if cc.totals.rx != 1_000_000 || cc.totals.tx != 250_000 {
		t.Errorf("final totals = %+v, want the socket's lifetime counters", cc.totals)
	}
	if cc.conn.Process != "curl" || cc.conn.Remote != "1.2.3.4:443" {
		t.Errorf("closed row lost its identity: %+v", cc.conn)
	}
	if cc.firstSeen.IsZero() || cc.closedAt.IsZero() {
		t.Errorf("closed row missing timestamps: opened %v closed %v", cc.firstSeen, cc.closedAt)
	}
}

func TestClosedViewRendersAndToggles(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns

	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 900_000_000, 1000)
	m = feed(t, m, nil, []probe.Conn{c})
	m = feed(t, m, nil, nil)

	if strings.Contains(m.renderConnsText(), "Recently closed") {
		t.Fatal("the active view should not show closed connections")
	}

	model, _ := m.Update(key("z"))
	m = model.(Model)
	if !m.showClosed {
		t.Fatal("z should switch to the closed view")
	}

	text := m.renderConnsText()
	if !strings.Contains(text, "Recently closed") {
		t.Fatalf("closed view header missing:\n%s", text)
	}
	if !strings.Contains(text, "858M") || !strings.Contains(text, "curl") {
		t.Errorf("closed row missing its totals or process:\n%s", text)
	}
	if !strings.Contains(text, "LIFE") || !strings.Contains(text, "CLOSED") {
		t.Errorf("closed view is missing its lifetime columns:\n%s", text)
	}

	// The row is selectable, copyable and exportable like any other.
	if n := m.connsRowCount(); n != 1 {
		t.Errorf("closed row count = %d, want 1", n)
	}
	txt, what, ok := m.copyTarget()
	if !ok || what != "closed connection" || !strings.Contains(txt, "1.2.3.4:443") {
		t.Errorf("copyTarget = (%q, %q, %v)", txt, what, ok)
	}
	if _, ok := m.detailContent(); !ok {
		t.Error("detail overlay should render a closed connection")
	}

	model, _ = m.Update(key("z"))
	if model.(Model).showClosed {
		t.Error("z should toggle back to the active view")
	}
}

// Signalling from a closed row is unsafe: the PID may already belong to an
// unrelated process.
func TestClosedViewDisallowsSignalling(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns
	m.showClosed = true
	m.closedConns = []closedConn{{
		conn:     conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 1, 1),
		closedAt: time.Now(),
	}}

	if _, _, ok := m.selectedPID(); ok {
		t.Error("a closed connection should not offer a signal target")
	}
}

func TestClosedHistoryIsCappedAndOrdered(t *testing.T) {
	m := totalsModel()
	base := time.Now()

	for i := 0; i < closedConnLimit+40; i++ {
		m.closedConns = append(m.closedConns, closedConn{
			conn:     conn(int32(i), "p", "10.0.0.1:"+strconv.Itoa(i), "1.1.1.1:443", uint64(i), 0),
			closedAt: base.Add(time.Duration(i) * time.Second),
		})
	}
	m.closedConns = m.closedConns[len(m.closedConns)-closedConnLimit:]

	m.activeTab = tabConns
	m.showClosed = true
	rows := m.closedRows()
	if len(rows) != closedConnLimit {
		t.Fatalf("closed rows = %d, want the %d cap", len(rows), closedConnLimit)
	}
	// Newest first by default.
	if !rows[0].closedAt.After(rows[1].closedAt) {
		t.Errorf("closed rows are not newest-first: %v then %v", rows[0].closedAt, rows[1].closedAt)
	}

	m.closedSortCol = indexOfCol(t, closedSortCols, "bytes")
	if got := m.closedRows()[0].totals.rx; got != rows[0].totals.rx && got < rows[len(rows)-1].totals.rx {
		t.Errorf("bytes sort did not surface the biggest transfer, got %d", got)
	}
}

// A closure detected in the same poll as the rate update must still see the
// previous counters — the ordering of noteClosedConns and updateRates matters.
func TestClosedTotalsSurviveRateUpdateOrdering(t *testing.T) {
	m := totalsModel()
	m.activeTab = tabConns

	c := conn(10, "curl", "10.0.0.1:5000", "1.2.3.4:443", 5_000, 100)
	m = feed(t, m, nil, []probe.Conn{c})
	c.RxBytes = 4_000_000
	m = feed(t, m, nil, []probe.Conn{c})
	m = feed(t, m, nil, nil)

	if len(m.closedConns) != 1 {
		t.Fatalf("closed connections = %d, want 1", len(m.closedConns))
	}
	if got := m.closedConns[0].totals.rx; got != 4_000_000 {
		t.Errorf("closed total = %d, want the last observed counter (4000000)", got)
	}
}

// The interface filter claims to be active on the closed-connections view but
// was never applied there, so `i` then `z` showed everything.
func TestInterfaceFilterAppliesToClosedConnections(t *testing.T) {
	m := routesModel()
	m.activeTab = tabConns
	m.lastSnap = probe.NetSnapshot{Ifaces: []probe.IfaceInfo{
		{Name: "eth0", Addrs: []string{"192.168.1.5/24"}},
		{Name: "tun0", Addrs: []string{"10.8.0.2/24"}},
	}}

	lan := conn(1, "curl", "192.168.1.5:5000", "1.1.1.1:443", 10, 10)
	vpn := conn(2, "curl", "10.8.0.2:6000", "2.2.2.2:443", 20, 20)
	m = feed(t, m, nil, []probe.Conn{lan, vpn})
	m = feed(t, m, nil, nil) // both close

	if len(m.closedConns) != 2 {
		t.Fatalf("closed %d connections, want 2", len(m.closedConns))
	}
	m.showClosed = true
	if got := len(m.closedRows()); got != 2 {
		t.Fatalf("unfiltered closed rows = %d, want 2", got)
	}

	m.ifaceFilter = "tun0"
	rows := m.closedRows()
	if len(rows) != 1 || rows[0].conn.Local != vpn.Local {
		t.Errorf("iface filter kept %d closed rows, want just the VPN one", len(rows))
	}
}
