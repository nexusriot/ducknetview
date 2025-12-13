package probe

import (
	"sort"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

type ProcNet struct {
	PID         int32
	Name        string
	ConnCount   int
	ListenCount int
}

func TopProcsByConnections(limit int) ([]ProcNet, error) {
	conns, err := gnet.Connections("all")
	if err != nil {
		return nil, err
	}

	m := map[int32]*ProcNet{}
	for _, c := range conns {
		if c.Pid <= 0 {
			continue
		}
		pn := m[c.Pid]
		if pn == nil {
			pn = &ProcNet{PID: c.Pid}
			m[c.Pid] = pn
		}
		pn.ConnCount++
		if c.Status == "LISTEN" {
			pn.ListenCount++
		}
	}

	out := make([]ProcNet, 0, len(m))
	for pid, pn := range m {
		if pn.Name == "" {
			if p, e := process.NewProcess(pid); e == nil {
				if n, e2 := p.Name(); e2 == nil {
					pn.Name = n
				}
			}
		}
		out = append(out, *pn)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].ConnCount != out[j].ConnCount {
			return out[i].ConnCount > out[j].ConnCount
		}
		return out[i].PID < out[j].PID
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
