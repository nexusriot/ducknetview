package probe

import (
	"os"
	"os/exec"
	"syscall"
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

// Signalling is the one thing this tool does that changes the host rather than
// reporting on it, and it was the last function in the package that nothing
// exercised — including the end-to-end suite, which has no process it would be
// safe to kill.

// TestSignalHelperProcess is not a test: it is the child the test below
// signals. It sleeps until something ends it.
func TestSignalHelperProcess(t *testing.T) {
	if os.Getenv("DNV_SIGNAL_HELPER") != "1" {
		t.Skip("helper process, run by TestSignalProcess")
	}
	time.Sleep(2 * time.Minute)
}

func TestSignalProcess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestSignalHelperProcess", "-test.timeout=5m")
	cmd.Env = append(os.Environ(), "DNV_SIGNAL_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the helper: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	pid := int32(cmd.Process.Pid)
	if err := SignalProcess(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM to the helper: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done: // it died, which is the whole assertion
	case <-time.After(30 * time.Second):
		t.Fatal("the helper ignored SIGTERM")
	}

	// A PID that does not exist is an error rather than a silent success: the
	// status line reports it, and "sent" would be a lie.
	if err := SignalProcess(pid, syscall.SIGTERM); err == nil {
		t.Error("signalling a process that has exited reported success")
	}
	if err := SignalProcess(-1, syscall.SIGTERM); err == nil {
		t.Error("signalling PID -1 reported success")
	}
}
