package probe

import (
	"strings"
	"testing"
)

// Trimmed from a real 6.x kernel: the column order differs between kernels,
// which is why the parser goes by header name and never by position.
const snmpFixture = `Ip: Forwarding DefaultTTL InReceives
Ip: 1 64 123456
Icmp: InMsgs InErrors
Icmp: 10 0
Tcp: RtoAlgorithm RtoMin RtoMax MaxConn ActiveOpens PassiveOpens AttemptFails EstabResets CurrEstab InSegs OutSegs RetransSegs InErrs OutRsts InCsumErrors
Tcp: 1 200 120000 -1 5000 300 12 40 25 900000 850000 1234 7 4321 0
Udp: InDatagrams NoPorts
Udp: 100 2
`

const netstatFixture = `TcpExt: SyncookiesSent SyncookiesRecv SyncookiesFailed ListenOverflows ListenDrops TCPTimeouts
TcpExt: 3 1 0 55 60 99
IpExt: InNoRoutes InTruncatedPkts
IpExt: 0 0
`

func TestParseTCPCounters(t *testing.T) {
	c, ok := parseTCPCounters(strings.NewReader(snmpFixture), strings.NewReader(netstatFixture))
	if !ok {
		t.Fatal("ok = false for well-formed input")
	}
	want := TCPCounters{
		ListenOverflows: 55, ListenDrops: 60, RetransSegs: 1234, InErrs: 7,
		OutRsts: 4321, Timeouts: 99, SyncookiesSent: 3,
	}
	if c != want {
		t.Errorf("got %+v\nwant %+v", c, want)
	}
}

// Some containers expose /proc/net/snmp but not /proc/net/netstat; the half
// that is there must still be read.
func TestParseTCPCountersSnmpOnly(t *testing.T) {
	c, ok := parseTCPCounters(strings.NewReader(snmpFixture), nil)
	if !ok {
		t.Fatal("ok = false with only the snmp half")
	}
	if c.RetransSegs != 1234 || c.ListenDrops != 0 {
		t.Errorf("got %+v", c)
	}
}

func TestParseTCPCountersRejectsGarbage(t *testing.T) {
	for name, in := range map[string]string{
		"empty":      "",
		"no tcp":     "Ip: Forwarding\nIp: 1\n",
		"no colon":   "hello world\n",
		"lone label": "Tcp: RetransSegs\n",
	} {
		if _, ok := parseTCPCounters(strings.NewReader(in), nil); ok {
			t.Errorf("%s: ok = true", name)
		}
	}
}

// A value line shorter than its header (a truncated read) fills what it can
// rather than shifting columns.
func TestParseProcNetPairsShortValues(t *testing.T) {
	got := map[string]map[string]uint64{}
	parseProcNetPairs(strings.NewReader("Tcp: A B C\nTcp: 1 2\n"), got)
	if got["Tcp"]["A"] != 1 || got["Tcp"]["B"] != 2 {
		t.Errorf("got %v", got)
	}
	if _, ok := got["Tcp"]["C"]; ok {
		t.Error("C was set from a missing column")
	}
}

func TestTCPRates(t *testing.T) {
	prev := TCPCounters{ListenDrops: 10, RetransSegs: 100, OutRsts: 50}
	cur := TCPCounters{ListenDrops: 30, RetransSegs: 100, OutRsts: 40}
	r := TCPRates(cur, prev, 2)
	if r.ListenDrops != 10 {
		t.Errorf("ListenDrops = %v, want 10/s", r.ListenDrops)
	}
	if r.RetransSegs != 0 {
		t.Errorf("RetransSegs = %v, want 0", r.RetransSegs)
	}
	if r.OutRsts != 0 {
		t.Errorf("a counter that went backwards must read 0, got %v", r.OutRsts)
	}
}

func TestTCPHealthCountersCoverEveryField(t *testing.T) {
	h := TCPHealth{
		Totals: TCPCounters{1, 2, 3, 4, 5, 6, 7},
	}
	seen := map[uint64]bool{}
	for _, c := range h.Counters() {
		if c.Key == "" || c.Label == "" {
			t.Errorf("counter %+v has no key or label", c)
		}
		seen[c.Total] = true
	}
	for v := uint64(1); v <= 7; v++ {
		if !seen[v] {
			t.Errorf("field with value %d is not listed by Counters", v)
		}
	}
}

func TestListenPortQueueRatio(t *testing.T) {
	if _, ok := (ListenPort{AcceptQueue: 3}).QueueRatio(); ok {
		t.Error("ok = true with no backlog")
	}
	if r, ok := (ListenPort{AcceptQueue: 32, Backlog: 128}).QueueRatio(); !ok || r != 0.25 {
		t.Errorf("ratio = %v, %v; want 0.25, true", r, ok)
	}
}

func TestListenKeyNormalizes(t *testing.T) {
	if ListenKey("::ffff:10.0.0.1:80") != ListenKey("10.0.0.1:80") {
		t.Error("an IPv4-mapped listener keys differently from its IPv4 form")
	}
	if ListenKey("0.0.0.0:80") == SockKey("0.0.0.0:80", "0.0.0.0:0") {
		t.Error("listener keys collide with connection keys")
	}
}
