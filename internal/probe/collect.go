package probe

import (
	"fmt"
	"sort"
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

	ports := make([]ListenPort, 0, 64)
	conns := make([]Conn, 0, 128)
	procMap := make(map[int32]*ProcNet, 64)

	for _, c := range raw {
		if c.Pid > 0 {
			pn := procMap[c.Pid]
			if pn == nil {
				pn = &ProcNet{PID: c.Pid}
				procMap[c.Pid] = pn
			}
			pn.ConnCount++
			if c.Status == "LISTEN" {
				pn.ListenCount++
			}
		}

		if isListening(c) {
			ports = append(ports, ListenPort{
				Proto:   connProto(c),
				Local:   fmt.Sprintf("%s:%d", c.Laddr.IP, c.Laddr.Port),
				PID:     c.Pid,
				Process: nameOf(c.Pid),
				User:    userOf(c.Pid),
			})
		}

		if isActive(c) {
			state := c.Status
			if c.Type == syscall.SOCK_DGRAM && state == "" {
				state = "-"
			}
			conns = append(conns, Conn{
				Proto:   connProto(c),
				Local:   fmt.Sprintf("%s:%d", c.Laddr.IP, c.Laddr.Port),
				Remote:  fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port),
				State:   state,
				PID:     c.Pid,
				Process: nameOf(c.Pid),
				User:    userOf(c.Pid),
			})
		}
	}

	// Attach the kernel's per-socket byte counters where the platform provides
	// them; throughput is a bonus, never a reason to fail the whole collection.
	if stats, err := SockStats(); err == nil && len(stats) > 0 {
		for i := range conns {
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
	// expensive per-process attributes (CPU%, mem%, user, container).
	keep := make(map[int32]bool, len(procs))
	for i := range procs {
		EnrichProc(&procs[i])
		procs[i].User = userOf(procs[i].PID)
		procs[i].Container = ContainerID(procs[i].PID)
		keep[procs[i].PID] = true
	}
	pruneCPUSamples(keep)

	return ConnInfo{Ports: ports, Procs: procs, Conns: conns}, nil
}
