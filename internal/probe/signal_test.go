package probe

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

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
