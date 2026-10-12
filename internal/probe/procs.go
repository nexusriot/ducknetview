package probe

import (
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

type ProcNet struct {
	PID         int32
	PPID        int32 // parent, for the process tree ("" relationships are roots)
	Name        string
	ConnCount   int
	ListenCount int
	User        string  // owning user (best-effort)
	Container   string  // short container id, "" when not containerised
	CPU         float64 // percent of one core, measured since the previous poll
	Mem         float32 // percent of physical memory (RSS)
}

// ProcDetail holds the extra, comparatively expensive process attributes shown
// in the row-detail overlay. Fetched on demand for a single PID rather than for
// every process each refresh.
type ProcDetail struct {
	User      string
	Cmdline   string
	Container string
	Ppid      int32
	CPU       float64
	Mem       float32
}

// cpuSample is a PID's previous CPU-time reading. gopsutil's CPUPercent() is
// averaged over the whole process lifetime, which badly understates a process
// that just started hammering the CPU; diffing consecutive readings gives the
// usage over the poll interval instead.
type cpuSample struct {
	total float64 // user+system seconds
	at    time.Time
	pct   float64 // percentage computed at that reading
}

var (
	cpuMu   sync.Mutex
	cpuPrev = map[int32]cpuSample{}
)

// cpuDelta converts two CPU-time readings into a percentage of one core. ok is
// false when there is no usable previous sample (first sighting of the PID, or
// a counter that went backwards because the PID was recycled).
func cpuDelta(prev cpuSample, total float64, now time.Time) (float64, bool) {
	if prev.at.IsZero() {
		return 0, false
	}
	dt := now.Sub(prev.at).Seconds()
	if dt <= 0 || total < prev.total {
		return 0, false
	}
	return (total - prev.total) / dt * 100, true
}

// sampleCPU records a reading for a PID and returns the usage since the
// previous one.
func sampleCPU(pid int32, total float64, now time.Time) (float64, bool) {
	cpuMu.Lock()
	defer cpuMu.Unlock()

	pct, ok := cpuDelta(cpuPrev[pid], total, now)
	cpuPrev[pid] = cpuSample{total: total, at: now, pct: pct}
	return pct, ok
}

// lastCPUPercent returns the most recently sampled usage for a PID, so the
// detail overlay reports the same number as the process table rather than
// starting its own measurement window.
func lastCPUPercent(pid int32) (float64, bool) {
	cpuMu.Lock()
	defer cpuMu.Unlock()

	s, ok := cpuPrev[pid]
	if !ok || s.at.IsZero() {
		return 0, false
	}
	return s.pct, true
}

// pruneCPUSamples drops readings for PIDs that are no longer being displayed,
// so long sessions don't accumulate an entry per process that ever appeared.
func pruneCPUSamples(keep map[int32]bool) {
	cpuMu.Lock()
	defer cpuMu.Unlock()

	for pid := range cpuPrev {
		if !keep[pid] {
			delete(cpuPrev, pid)
		}
	}
}

// EnrichProc fills the best-effort CPU/memory fields of pn in place. Errors for
// individual attributes are ignored (the field stays zero). User and Container
// are filled by Collect, which shares one lookup cache across all its views.
func EnrichProc(pn *ProcNet) {
	p, err := process.NewProcess(pn.PID)
	if err != nil {
		return
	}

	measured := false
	if t, e := p.Times(); e == nil {
		if pct, ok := sampleCPU(pn.PID, t.User+t.System, time.Now()); ok {
			pn.CPU = pct
			measured = true
		}
	}
	if !measured {
		// First sighting of this PID: fall back to the lifetime average so the
		// column isn't blank until the next poll.
		if c, e := p.CPUPercent(); e == nil {
			pn.CPU = c
		}
	}

	if mp, e := p.MemoryPercent(); e == nil {
		pn.Mem = mp
	}
	// The parent is what makes a tree possible. It is one more read of
	// /proc/<pid>/stat per process per poll, which is the same file Times()
	// just read — cheap enough at this list size, and there is no way to get
	// the relationship without it.
	if pp, e := p.Ppid(); e == nil {
		pn.PPID = pp
	}
}

// ProcessDetail returns the extended attributes for a single PID.
func ProcessDetail(pid int32) (ProcDetail, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return ProcDetail{}, err
	}
	d := ProcDetail{Container: ContainerID(pid)}
	if u, e := p.Username(); e == nil {
		d.User = u
	}
	if cl, e := p.Cmdline(); e == nil {
		d.Cmdline = cl
	}
	if pp, e := p.Ppid(); e == nil {
		d.Ppid = pp
	}
	if pct, ok := lastCPUPercent(pid); ok {
		d.CPU = pct
	} else if c, e := p.CPUPercent(); e == nil {
		d.CPU = c
	}
	if mp, e := p.MemoryPercent(); e == nil {
		d.Mem = mp
	}
	return d, nil
}

// SignalProcess sends sig to the given PID (best-effort, via gopsutil). It may
// fail with a permission error when the target is owned by another user and the
// tool is not running with sufficient privileges.
func SignalProcess(pid int32, sig syscall.Signal) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	return p.SendSignal(sig)
}
