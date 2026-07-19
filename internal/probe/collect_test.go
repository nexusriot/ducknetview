package probe

import "testing"

// TestCollectProcLimit exercises the single-enumeration collector against the
// real system. Socket enumeration may be unavailable in restricted sandboxes,
// so a probe error is treated as a skip rather than a failure.
func TestCollectProcLimit(t *testing.T) {
	info, err := Collect(5)
	if err != nil {
		t.Skipf("Collect unavailable in this environment: %v", err)
	}
	if len(info.Procs) > 5 {
		t.Fatalf("procLimit=5 not respected: got %d processes", len(info.Procs))
	}
	// Procs must be ranked by connection count (descending) before truncation.
	for i := 1; i < len(info.Procs); i++ {
		if info.Procs[i-1].ConnCount < info.Procs[i].ConnCount {
			t.Fatalf("procs not ranked by ConnCount desc at %d", i)
		}
	}
}
