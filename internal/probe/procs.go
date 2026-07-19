package probe

import (
	"syscall"

	"github.com/shirou/gopsutil/v4/process"
)

type ProcNet struct {
	PID         int32
	Name        string
	ConnCount   int
	ListenCount int
	User        string  // owning user (best-effort)
	CPU         float64 // percent, averaged over the process lifetime
	Mem         float32 // percent of physical memory (RSS)
}

// ProcDetail holds the extra, comparatively expensive process attributes shown
// in the row-detail overlay. Fetched on demand for a single PID rather than for
// every process each refresh.
type ProcDetail struct {
	User    string
	Cmdline string
	Ppid    int32
	CPU     float64
	Mem     float32
}

// EnrichProc fills the best-effort user/CPU/mem fields of pn in place. Errors
// for individual attributes are ignored (the field stays zero/empty).
func EnrichProc(pn *ProcNet) {
	p, err := process.NewProcess(pn.PID)
	if err != nil {
		return
	}
	if u, e := p.Username(); e == nil {
		pn.User = u
	}
	if c, e := p.CPUPercent(); e == nil {
		pn.CPU = c
	}
	if mp, e := p.MemoryPercent(); e == nil {
		pn.Mem = mp
	}
}

// ProcessDetail returns the extended attributes for a single PID.
func ProcessDetail(pid int32) (ProcDetail, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return ProcDetail{}, err
	}
	d := ProcDetail{}
	if u, e := p.Username(); e == nil {
		d.User = u
	}
	if cl, e := p.Cmdline(); e == nil {
		d.Cmdline = cl
	}
	if pp, e := p.Ppid(); e == nil {
		d.Ppid = pp
	}
	if c, e := p.CPUPercent(); e == nil {
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
