package probe

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// ConnInfo bundles the three connection-derived views so that a single
// gnet.Connections("all") enumeration can feed all of them.
type ConnInfo struct {
	Ports []ListenPort
	Procs []ProcNet
	Conns []Conn

	// NetNS reports on the sockets enumerated from other network namespaces.
	NetNS NetNSStatus
}

// byteRankBonus is how many extra processes may be kept beyond procLimit
// purely because they are moving the most data. Ranking by connection count
// alone would drop a process holding one socket that happens to be saturating
// the link — exactly the process a throughput view exists to show.
const byteRankBonus = 20

// truncateProcs applies procLimit to a connection-count-ranked list, then adds
// back any heavy-transfer process the cut would have hidden. procs must already
// be sorted by connection count, descending.
func truncateProcs(procs []ProcNet, procBytes map[int32]uint64, procLimit int) []ProcNet {
	if procLimit <= 0 || len(procs) <= procLimit {
		return procs
	}

	kept, dropped := procs[:procLimit], procs[procLimit:]

	// Of the processes the cut removed, keep the ones moving the most bytes.
	sort.SliceStable(dropped, func(i, j int) bool {
		bi, bj := procBytes[dropped[i].PID], procBytes[dropped[j].PID]
		if bi != bj {
			return bi > bj
		}
		return dropped[i].PID < dropped[j].PID
	})
	for _, p := range dropped {
		if len(kept) >= procLimit+byteRankBonus || procBytes[p.PID] == 0 {
			break
		}
		kept = append(kept, p)
	}
	return kept
}

// Collect enumerates all sockets exactly once and derives listening ports,
// active connections and per-process connection counts from that single pass,
// sharing one process-name cache across all three. This replaces three separate
// gnet.Connections("all") calls with one.
//
// procLimit is a soft cap on the number of processes returned (<= 0 means no
// limit): up to byteRankBonus further processes are kept when they are moving
// data, so the heaviest transfer on the host can never be truncated away.
func Collect(procLimit int) (ConnInfo, error) {
	raw, err := gnet.Connections("all")
	if err != nil {
		return ConnInfo{}, err
	}

	// Per-call caches: a PID is looked up once no matter how many sockets,
	// listeners and processes reference it, and nothing goes stale between
	// polls (PIDs are recycled).
	nameCache := make(map[int32]string, 64)
	nameOf := func(pid int32) string {
		if pid <= 0 {
			return ""
		}
		if n, ok := nameCache[pid]; ok {
			return n
		}
		n := ""
		if p, e := process.NewProcess(pid); e == nil {
			if nm, e2 := p.Name(); e2 == nil {
				n = nm
			}
		}
		nameCache[pid] = n
		return n
	}

	userCache := make(map[int32]string, 64)
	userOf := func(pid int32) string {
		if pid <= 0 {
			return ""
		}
		if u, ok := userCache[pid]; ok {
			return u
		}
		u := ""
		if p, e := process.NewProcess(pid); e == nil {
			if un, e2 := p.Username(); e2 == nil {
				u = un
			}
		}
		userCache[pid] = u
		return u
	}

	// The unit is what a daemon's operator knows a listener by ("nginx.service"
	// rather than a PID), so listeners carry it as well as processes; the cache
	// keeps it to one /proc/<pid>/cgroup read per PID per poll.
	type cgroupIDs struct{ container, unit string }
	cgroupCache := make(map[int32]cgroupIDs, 64)
	cgroupOf := func(pid int32) cgroupIDs {
		if c, ok := cgroupCache[pid]; ok {
			return c
		}
		var c cgroupIDs
		c.container, c.unit = CgroupInfo(pid)
		cgroupCache[pid] = c
		return c
	}

	b := &connBuilder{
		nameOf:  nameOf,
		userOf:  userOf,
		unitOf:  func(pid int32) string { return cgroupOf(pid).unit },
		ports:   make([]ListenPort, 0, 64),
		conns:   make([]Conn, 0, 128),
		procMap: make(map[int32]*ProcNet, 64),
	}
	b.add(raw, "")

	// Attach the kernel's per-socket byte counters where the platform provides
	// them; throughput is a bonus, never a reason to fail the whole collection.
	if stats, err := SockStats(); err == nil && len(stats) > 0 {
		attachSockStats(b.conns, stats)
		attachListenStats(b.ports, stats)
	}

	nsStatus := b.addNamespaces(procRoot)
	ports, conns, procMap := b.ports, b.conns, b.procMap

	// Bytes moved per process, used to make sure the truncation below cannot
	// drop the host's heaviest transfer.
	procBytes := make(map[int32]uint64, len(procMap))
	for _, c := range conns {
		if c.PID > 0 {
			procBytes[c.PID] += c.RxBytes + c.TxBytes
		}
	}

	procs := make([]ProcNet, 0, len(procMap))
	for pid, pn := range procMap {
		pn.Name = nameOf(pid)
		procs = append(procs, *pn)
	}

	// Rank by connection count so the procLimit truncation keeps the busiest
	// processes. (Final display order is decided by the UI's sort controls.)
	sort.Slice(procs, func(i, j int) bool {
		if procs[i].ConnCount != procs[j].ConnCount {
			return procs[i].ConnCount > procs[j].ConnCount
		}
		return procs[i].PID < procs[j].PID
	})
	procs = truncateProcs(procs, procBytes, procLimit)

	// Enrich only the (bounded) set we will actually display with the more
	// expensive per-process attributes (CPU%, mem%, user, container, unit).
	keep := make(map[int32]bool, len(procs))
	for i := range procs {
		EnrichProc(&procs[i])
		procs[i].User = userOf(procs[i].PID)
		cg := cgroupOf(procs[i].PID)
		procs[i].Container, procs[i].Unit = cg.container, cg.unit
		keep[procs[i].PID] = true
	}
	pruneCPUSamples(keep)

	return ConnInfo{Ports: ports, Procs: procs, Conns: conns, NetNS: nsStatus}, nil
}

// procRoot is where process and namespace information is read from; a
// variable so tests can point the namespace pass at a fixture tree.
var procRoot = "/proc"

// connBuilder accumulates the three views from one or more socket
// enumerations, so the host's gopsutil pass and each foreign namespace's
// /proc/<pid>/net pass go through exactly the same classification.
type connBuilder struct {
	nameOf, userOf, unitOf func(int32) string

	ports   []ListenPort
	conns   []Conn
	procMap map[int32]*ProcNet
}

// unit is the owning process's systemd unit, or "" when no lookup is set.
func (b *connBuilder) unit(pid int32) string {
	if b.unitOf == nil {
		return ""
	}
	return b.unitOf(pid)
}

// add classifies one namespace's sockets; netns is "" for our own namespace.
func (b *connBuilder) add(raw []gnet.ConnectionStat, netns string) {
	for _, c := range raw {
		if c.Pid > 0 {
			pn := b.procMap[c.Pid]
			if pn == nil {
				pn = &ProcNet{PID: c.Pid}
				b.procMap[c.Pid] = pn
			}
			pn.ConnCount++
			switch c.Status {
			case "LISTEN":
				pn.ListenCount++
			case "CLOSE_WAIT":
				pn.CloseWait++
			}
		}

		if isListening(c) {
			b.ports = append(b.ports, ListenPort{
				Proto:   connProto(c),
				Local:   fmt.Sprintf("%s:%d", c.Laddr.IP, c.Laddr.Port),
				PID:     c.Pid,
				Process: b.nameOf(c.Pid),
				User:    b.userOf(c.Pid),
				Unit:    b.unit(c.Pid),
				NetNS:   netns,
			})
		}

		if isActive(c) {
			state := c.Status
			if c.Type == syscall.SOCK_DGRAM && state == "" {
				state = "-"
			}
			b.conns = append(b.conns, Conn{
				Proto:   connProto(c),
				Local:   fmt.Sprintf("%s:%d", c.Laddr.IP, c.Laddr.Port),
				Remote:  fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port),
				State:   state,
				PID:     c.Pid,
				Process: b.nameOf(c.Pid),
				User:    b.userOf(c.Pid),
				NetNS:   netns,
			})
		}
	}
}

// attachSockStats copies sock_diag counters onto the connections they match.
func attachSockStats(conns []Conn, stats map[string]SockStat) {
	for i := range conns {
		// The counters are TCP-only, and TCP and UDP port spaces are
		// separate: a UDP socket sharing a TCP socket's 4-tuple is not it.
		if conns[i].Proto != "tcp" {
			continue
		}
		if st, ok := stats[SockKey(conns[i].Local, conns[i].Remote)]; ok {
			conns[i].TxBytes = st.TxBytes
			conns[i].RxBytes = st.RxBytes
			conns[i].RTT = st.RTT
			conns[i].MinRTT = st.MinRTT
			conns[i].Retrans = st.Retrans
			conns[i].RecvQ = st.RecvQ
			conns[i].SendQ = st.SendQ
		}
	}
}

// attachListenStats copies each TCP listener's accept-queue depth and backlog
// from the sock_diag dump, which reports them as a LISTEN socket's queues.
func attachListenStats(ports []ListenPort, stats map[string]SockStat) {
	for i := range ports {
		if ports[i].Proto != "tcp" {
			continue
		}
		if st, ok := stats[ListenKey(ports[i].Local)]; ok {
			ports[i].AcceptQueue = st.RecvQ
			ports[i].Backlog = st.SendQ
		}
	}
}

// addNamespaces enumerates every network namespace other than our own (up to
// maxNetNS of them). The namespace map is built once per poll and serves the
// enumeration, the labels and the publication check alike. Every failure here
// degrades to showing less, and says why in the returned status.
func (b *connBuilder) addNamespaces(root string) NetNSStatus {
	self, err := ownNetNS()
	if err != nil {
		return NetNSStatus{} // not Linux, or no /proc: there is only one table to read
	}
	return b.addNamespacesFrom(root, self, namedNetNSDir)
}

// nsStats fetches one namespace's sock_diag counters; a variable so tests
// never setns into a real process's namespace.
var nsStats = nsSockStats

func (b *connBuilder) addNamespacesFrom(root string, self uint64, namedDir string) NetNSStatus {
	var st NetNSStatus
	groups, denied := groupByNetNS(root)
	all := foreignNamespaces(groups, self)
	st.Seen = len(all)

	var limits []string
	if denied > 0 && os.Geteuid() != 0 {
		limits = append(limits, fmt.Sprintf(
			"%d %s owned by other users hide their network namespace — run as root to include containers",
			denied, pluralize(denied, "process", "processes")))
	}
	if len(all) > maxNetNS {
		limits = append(limits, fmt.Sprintf("only the first %d of %d network namespaces are scanned", maxNetNS, len(all)))
		all = all[:maxNetNS]
	}
	st.Scanned = len(all)

	var named map[uint64]string
	var pubs []publication
	pubsLoaded := false
	countersDenied := false
	noCounters := 0

	for _, ns := range all {
		raw := namespaceSockets(root, ns)
		if len(raw) == 0 {
			continue
		}
		if named == nil {
			named = namedNetNS(namedDir)
		}
		label := netNSLabel(root, ns, named)

		firstPort, firstConn := len(b.ports), len(b.conns)
		b.add(raw, label)
		nsConns := b.conns[firstConn:]
		nsPorts := b.ports[firstPort:]

		// One setns per namespace, and none at all once it has been refused:
		// the answer does not change within a poll.
		if !countersDenied && (countTCP(nsConns) > 0 || len(nsPorts) > 0) {
			stats, err := nsStats(ns.PIDs[0])
			switch {
			case err == nil:
				attachSockStats(nsConns, stats)
				attachListenStats(nsPorts, stats)
			case errors.Is(err, errNetNSPrivilege):
				countersDenied = true
			}
		}
		if countersDenied {
			noCounters += countTCP(nsConns)
		}

		if len(nsPorts) == 0 {
			continue
		}
		if !pubsLoaded {
			pubs, pubsLoaded = hostPublications(root, b.ports[:hostPortCount(b.ports)]), true
		}
		if len(pubs) == 0 {
			continue
		}
		addrs := namespaceAddrs(root, ns.PIDs[0])
		for i := range nsPorts {
			nsPorts[i].Published = publishedAs(nsPorts[i], addrs, pubs)
		}
	}

	if noCounters > 0 {
		limits = append(limits, fmt.Sprintf(
			"%d TCP %s in other namespaces have no byte counters — reading them needs root",
			noCounters, pluralize(noCounters, "connection", "connections")))
	}
	st.Limited = strings.Join(limits, "; ")
	return st
}

// hostPortCount is how many leading ports belong to our own namespace: the
// host pass runs first, so they form a prefix.
func hostPortCount(ports []ListenPort) int {
	for i, p := range ports {
		if p.NetNS != "" {
			return i
		}
	}
	return len(ports)
}

// countTCP counts the connections sock_diag could have counters for.
func countTCP(conns []Conn) int {
	n := 0
	for _, c := range conns {
		if c.Proto == "tcp" {
			n++
		}
	}
	return n
}

func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
