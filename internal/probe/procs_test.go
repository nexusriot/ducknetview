package probe

import (
	"testing"
	"time"
)

func TestCPUDelta(t *testing.T) {
	base := time.Now()

	// Half a CPU-second consumed over one wall second is 50% of a core.
	prev := cpuSample{total: 10, at: base}
	if pct, ok := cpuDelta(prev, 10.5, base.Add(time.Second)); !ok || pct < 49.9 || pct > 50.1 {
		t.Errorf("cpuDelta = (%.2f, %v), want ~50%%", pct, ok)
	}

	// Busier than one core is legitimate on a multi-threaded process.
	if pct, ok := cpuDelta(cpuSample{total: 0, at: base}, 4, base.Add(2*time.Second)); !ok || pct < 199 || pct > 201 {
		t.Errorf("multi-core cpuDelta = (%.2f, %v), want ~200%%", pct, ok)
	}

	if _, ok := cpuDelta(cpuSample{}, 5, base); ok {
		t.Error("first sample should report ok=false so the caller can fall back")
	}
	if _, ok := cpuDelta(cpuSample{total: 10, at: base}, 10, base); ok {
		t.Error("zero elapsed time should report ok=false rather than divide by zero")
	}
	// A recycled PID can present a smaller total than the one we recorded.
	if _, ok := cpuDelta(cpuSample{total: 100, at: base}, 1, base.Add(time.Second)); ok {
		t.Error("counter going backwards should report ok=false")
	}
}

func TestSampleCPUAndPrune(t *testing.T) {
	const pid = int32(424242)
	base := time.Now()

	if _, ok := sampleCPU(pid, 1, base); ok {
		t.Error("first sampleCPU should report ok=false")
	}
	pct, ok := sampleCPU(pid, 2, base.Add(time.Second))
	if !ok || pct < 99 || pct > 101 {
		t.Errorf("second sampleCPU = (%.2f, %v), want ~100%%", pct, ok)
	}

	// The detail overlay reads back the sampled value instead of starting its
	// own measurement window.
	if got, ok := lastCPUPercent(pid); !ok || got != pct {
		t.Errorf("lastCPUPercent = (%.2f, %v), want (%.2f, true)", got, ok, pct)
	}

	pruneCPUSamples(map[int32]bool{})
	if _, ok := lastCPUPercent(pid); ok {
		t.Error("pruneCPUSamples should have dropped the entry")
	}
}
