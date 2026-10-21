package probe

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

// Host-wide TCP counters. The per-socket view shows what is happening on the
// sockets that exist; these show what happened to the ones that never got to
// exist — a SYN dropped because a listener's accept queue was full, a reset
// sent to a port nobody listens on — and the host's overall retransmit and
// timeout rate. They live in /proc/net/snmp (the RFC 1213 MIB) and
// /proc/net/netstat (Linux's own extensions), both as pairs of lines: a header
// naming the fields and a line of values in the same order.

// TCPCounters are cumulative since boot. JSON names are explicit because they
// are part of the --json snapshot, which other tools read.
type TCPCounters struct {
	// The accept queue of some listener was full (ListenOverflows), and the
	// connection was dropped (ListenDrops, which also counts other drops on
	// the listen path, so it is the one to alert on).
	ListenOverflows uint64 `json:"listen_overflows"`
	ListenDrops     uint64 `json:"listen_drops"`
	RetransSegs     uint64 `json:"retrans_segs"`
	InErrs          uint64 `json:"in_errs"`
	OutRsts         uint64 `json:"out_rsts"`
	Timeouts        uint64 `json:"timeouts"`
	// SYN cookies are the kernel coping with a full SYN queue: a flood, or a
	// service that cannot keep up.
	SyncookiesSent uint64 `json:"syncookies_sent"`
}

// TCPCounterRates are the same counters as per-second rates over the last
// sample interval.
type TCPCounterRates struct {
	ListenOverflows float64 `json:"listen_overflows"`
	ListenDrops     float64 `json:"listen_drops"`
	RetransSegs     float64 `json:"retrans_segs"`
	InErrs          float64 `json:"in_errs"`
	OutRsts         float64 `json:"out_rsts"`
	Timeouts        float64 `json:"timeouts"`
	SyncookiesSent  float64 `json:"syncookies_sent"`
}

// TCPHealth is one sample of the host's TCP counters. Rates are zero on the
// first sample, which has nothing to diff against.
type TCPHealth struct {
	Totals TCPCounters     `json:"totals"`
	PerSec TCPCounterRates `json:"per_second"`
}

// TCPCounter is one counter in display order, for the code that walks all of
// them (the Overview panel, the metrics).
type TCPCounter struct {
	Key    string // snake_case, as in the JSON and the metric label
	Label  string // human wording
	Total  uint64
	PerSec float64
}

// tcpCounterDefs is the one place the counters are enumerated: where each
// comes from, and how it is presented.
var tcpCounterDefs = []struct {
	key, label     string
	section, field string // /proc header prefix and column
	total          func(*TCPCounters) *uint64
	rate           func(*TCPCounterRates) *float64
}{
	{"listen_overflows", "listen overflows", "TcpExt", "ListenOverflows",
		func(c *TCPCounters) *uint64 { return &c.ListenOverflows }, func(r *TCPCounterRates) *float64 { return &r.ListenOverflows }},
	{"listen_drops", "listen drops", "TcpExt", "ListenDrops",
		func(c *TCPCounters) *uint64 { return &c.ListenDrops }, func(r *TCPCounterRates) *float64 { return &r.ListenDrops }},
	{"syncookies_sent", "SYN cookies sent", "TcpExt", "SyncookiesSent",
		func(c *TCPCounters) *uint64 { return &c.SyncookiesSent }, func(r *TCPCounterRates) *float64 { return &r.SyncookiesSent }},
	{"retrans_segs", "retransmitted segments", "Tcp", "RetransSegs",
		func(c *TCPCounters) *uint64 { return &c.RetransSegs }, func(r *TCPCounterRates) *float64 { return &r.RetransSegs }},
	{"timeouts", "retransmit timeouts", "TcpExt", "TCPTimeouts",
		func(c *TCPCounters) *uint64 { return &c.Timeouts }, func(r *TCPCounterRates) *float64 { return &r.Timeouts }},
	{"in_errs", "bad segments received", "Tcp", "InErrs",
		func(c *TCPCounters) *uint64 { return &c.InErrs }, func(r *TCPCounterRates) *float64 { return &r.InErrs }},
	{"out_rsts", "resets sent", "Tcp", "OutRsts",
		func(c *TCPCounters) *uint64 { return &c.OutRsts }, func(r *TCPCounterRates) *float64 { return &r.OutRsts }},
}

// Counters lists every counter with its total and rate, in display order.
func (h TCPHealth) Counters() []TCPCounter {
	out := make([]TCPCounter, 0, len(tcpCounterDefs))
	for _, d := range tcpCounterDefs {
		out = append(out, TCPCounter{
			Key: d.key, Label: d.label,
			Total: *d.total(&h.Totals), PerSec: *d.rate(&h.PerSec),
		})
	}
	return out
}

// TCPRates derives per-second rates between two samples dt seconds apart. A
// counter that went backwards (a container restart, a namespace switch) reads
// as zero, as Rate does for interfaces.
func TCPRates(cur, prev TCPCounters, dt float64) TCPCounterRates {
	var r TCPCounterRates
	for _, d := range tcpCounterDefs {
		*d.rate(&r) = Rate(*d.total(&cur), *d.total(&prev), dt)
	}
	return r
}

// parseProcNetPairs reads the header/value line pairs of /proc/net/snmp or
// /proc/net/netstat into section → field → value. Fields that are not
// non-negative integers (Tcp's MaxConn is -1) are skipped.
func parseProcNetPairs(r io.Reader, into map[string]map[string]uint64) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var header []string
	var section string
	for sc.Scan() {
		prefix, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			header = nil
			continue
		}
		fields := strings.Fields(rest)
		// A header is followed by its values under the same prefix; anything
		// else (a lone line, a mismatched pair) restarts the pairing.
		if header == nil || prefix != section {
			header, section = fields, prefix
			continue
		}
		vals := into[section]
		if vals == nil {
			vals = map[string]uint64{}
			into[section] = vals
		}
		for i, name := range header {
			if i >= len(fields) {
				break
			}
			if v, err := strconv.ParseUint(fields[i], 10, 64); err == nil {
				vals[name] = v
			}
		}
		header = nil
	}
}

// parseTCPCounters builds TCPCounters from the contents of /proc/net/snmp and
// /proc/net/netstat. ok is false when neither carried any TCP section, which
// is what a non-Linux host or a restricted /proc looks like.
func parseTCPCounters(snmp, netstat io.Reader) (TCPCounters, bool) {
	sections := map[string]map[string]uint64{}
	if snmp != nil {
		parseProcNetPairs(snmp, sections)
	}
	if netstat != nil {
		parseProcNetPairs(netstat, sections)
	}
	if sections["Tcp"] == nil && sections["TcpExt"] == nil {
		return TCPCounters{}, false
	}
	var c TCPCounters
	for _, d := range tcpCounterDefs {
		*d.total(&c) = sections[d.section][d.field]
	}
	return c, true
}

// ReadTCPCounters reads the host's TCP counters. The error is non-nil only
// when nothing could be read; a missing netstat file (some containers) still
// yields the snmp half.
func ReadTCPCounters() (TCPCounters, error) {
	snmp, err := os.Open("/proc/net/snmp")
	if err != nil {
		return TCPCounters{}, err
	}
	defer snmp.Close()

	var netstat io.Reader
	if f, err := os.Open("/proc/net/netstat"); err == nil {
		defer f.Close()
		netstat = f
	}

	c, ok := parseTCPCounters(snmp, netstat)
	if !ok {
		return TCPCounters{}, os.ErrNotExist
	}
	return c, nil
}
