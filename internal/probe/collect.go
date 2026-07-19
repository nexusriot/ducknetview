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

// Collect enumerates all sockets exactly once and derives listening ports,
// active connections and per-process connection counts from that single pass,
// sharing one process-name cache across all three. procLimit caps the number
// of processes returned (<= 0 means no limit). This replaces three separate
// gnet.Connections("all") calls with one.
func Collect(procLimit int) (ConnInfo, error) {
	raw, err := gnet.Connections("all")
	if err != nil {
		return ConnInfo{}, err
	}

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
			})
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
	if procLimit > 0 && len(procs) > procLimit {
		procs = procs[:procLimit]
	}

	// Enrich only the (bounded) set we will actually display with the more
	// expensive per-process attributes (user, CPU%, mem%).
	for i := range procs {
		EnrichProc(&procs[i])
	}

	return ConnInfo{Ports: ports, Procs: procs, Conns: conns}, nil
}
