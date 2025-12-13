package probe

import (
	"net"
	"time"

	"github.com/shirou/gopsutil/v4/host"
	gnet "github.com/shirou/gopsutil/v4/net"
)

type IfaceInfo struct {
	Name     string
	MTU      int
	Hardware string
	Addrs    []string
	IsUp     bool
	RxBps    float64
	TxBps    float64
	RxTotal  uint64
	TxTotal  uint64
	Kind     IfaceKind
}

type NetSnapshot struct {
	Hostname string
	Uptime   time.Duration
	Ifaces   []IfaceInfo
	TakenAt  time.Time
}

// keep last totals to compute deltas
type NetSampler struct {
	last   map[string]gnet.IOCountersStat
	lastAt time.Time
}

func NewNetSampler() *NetSampler {
	return &NetSampler{
		last:   map[string]gnet.IOCountersStat{},
		lastAt: time.Time{},
	}
}

func (s *NetSampler) Sample() (NetSnapshot, error) {
	now := time.Now()

	hi, _ := host.Info()
	hostName := ""
	uptime := time.Duration(0)
	if hi != nil {
		hostName = hi.Hostname
		uptime = time.Duration(hi.Uptime) * time.Second
	}

	// interface basics from stdlib
	ifs, err := net.Interfaces()
	if err != nil {
		return NetSnapshot{}, err
	}

	// bytes counters from gopsutil
	counters, err := gnet.IOCounters(true)
	if err != nil {
		return NetSnapshot{}, err
	}
	cur := map[string]gnet.IOCountersStat{}
	for _, c := range counters {
		cur[c.Name] = c
	}

	dt := now.Sub(s.lastAt).Seconds()
	if dt <= 0 {
		dt = 1
	}

	out := make([]IfaceInfo, 0, len(ifs))
	for _, nif := range ifs {
		ii := IfaceInfo{
			Name:     nif.Name,
			MTU:      nif.MTU,
			Hardware: nif.HardwareAddr.String(),
			IsUp:     (nif.Flags&net.FlagUp != 0),
		}

		addrs, _ := nif.Addrs()
		for _, a := range addrs {
			ii.Addrs = append(ii.Addrs, a.String())
		}

		if c, ok := cur[nif.Name]; ok {
			ii.RxTotal = c.BytesRecv
			ii.TxTotal = c.BytesSent

			if prev, ok2 := s.last[nif.Name]; ok2 {
				ii.RxBps = float64(c.BytesRecv-prev.BytesRecv) / dt
				ii.TxBps = float64(c.BytesSent-prev.BytesSent) / dt
			}
		}
		ii.Kind = ClassifyIface(nif.Name)

		out = append(out, ii)
	}

	// update sampler state
	s.last = cur
	s.lastAt = now

	return NetSnapshot{
		Hostname: hostName,
		Uptime:   uptime,
		Ifaces:   out,
		TakenAt:  now,
	}, nil
}
