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

	// Packet rates and cumulative link-health counters. Non-zero errors or
	// drops usually mean a bad cable, a saturated ring buffer or an overloaded
	// driver, so they are worth showing next to the throughput.
	RxPps     float64
	TxPps     float64
	RxPackets uint64
	TxPackets uint64
	Errin     uint64
	Errout    uint64
	Dropin    uint64
	Dropout   uint64
}

// LinkProblems reports the total number of interface errors and drops seen
// since boot. Zero on a healthy link.
func (i IfaceInfo) LinkProblems() uint64 {
	return i.Errin + i.Errout + i.Dropin + i.Dropout
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

// Rate returns (cur-prev)/dt in units per second. It guards against counter resets
// (cur < prev, e.g. after an interface bounce or driver reload) which would
// otherwise wrap the unsigned subtraction into a huge spurious spike.
func Rate(cur, prev uint64, dt float64) float64 {
	if cur < prev || dt <= 0 {
		return 0
	}
	return float64(cur-prev) / dt
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
			ii.RxPackets = c.PacketsRecv
			ii.TxPackets = c.PacketsSent
			ii.Errin, ii.Errout = c.Errin, c.Errout
			ii.Dropin, ii.Dropout = c.Dropin, c.Dropout

			if prev, ok2 := s.last[nif.Name]; ok2 {
				ii.RxBps = Rate(c.BytesRecv, prev.BytesRecv, dt)
				ii.TxBps = Rate(c.BytesSent, prev.BytesSent, dt)
				ii.RxPps = Rate(c.PacketsRecv, prev.PacketsRecv, dt)
				ii.TxPps = Rate(c.PacketsSent, prev.PacketsSent, dt)
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
