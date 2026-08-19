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
	// procLimit is a soft cap: heavy transfers are kept beyond it (see
	// truncateProcs), but never more than the documented bonus.
	if len(info.Procs) > 5+byteRankBonus {
		t.Fatalf("procLimit=5 not respected: got %d processes", len(info.Procs))
	}
	// The first procLimit entries are the connection-count ranking.
	head := info.Procs
	if len(head) > 5 {
		head = head[:5]
	}
	for i := 1; i < len(head); i++ {
		if head[i-1].ConnCount < head[i].ConnCount {
			t.Fatalf("procs not ranked by ConnCount desc at %d", i)
		}
	}
}

// Ranking by connection count alone can hide the host's heaviest transfer: a
// download holds one socket while a chatty daemon holds fifty.
func TestTruncateProcsKeepsHeavyTransfers(t *testing.T) {
	procs := make([]ProcNet, 0, 12)
	bytes := map[int32]uint64{}
	for i := 1; i <= 12; i++ {
		pid := int32(i)
		procs = append(procs, ProcNet{PID: pid, ConnCount: 100 - i})
		bytes[pid] = 0
	}
	// The least-connected process is moving by far the most data.
	whale := int32(12)
	bytes[whale] = 5 << 30

	kept := truncateProcs(procs, bytes, 5)

	if len(kept) != 6 {
		t.Fatalf("kept %d processes, want 5 by connection count plus the heavy one", len(kept))
	}
	found := false
	for _, p := range kept {
		if p.PID == whale {
			found = true
		}
	}
	if !found {
		t.Error("the heaviest transfer was truncated away")
	}
}

func TestTruncateProcsRespectsTheBonusCap(t *testing.T) {
	procs := make([]ProcNet, 0, 200)
	bytes := map[int32]uint64{}
	for i := 1; i <= 200; i++ {
		pid := int32(i)
		procs = append(procs, ProcNet{PID: pid, ConnCount: 300 - i})
		bytes[pid] = uint64(i) // every dropped process has traffic
	}

	kept := truncateProcs(procs, bytes, 80)
	if len(kept) != 80+byteRankBonus {
		t.Fatalf("kept %d processes, want the %d cap", len(kept), 80+byteRankBonus)
	}
}

// Idle processes must not be added back: the bonus exists for heavy transfers.
func TestTruncateProcsIgnoresIdleProcesses(t *testing.T) {
	procs := make([]ProcNet, 0, 10)
	for i := 1; i <= 10; i++ {
		procs = append(procs, ProcNet{PID: int32(i), ConnCount: 20 - i})
	}
	kept := truncateProcs(procs, map[int32]uint64{}, 5)
	if len(kept) != 5 {
		t.Errorf("kept %d processes, want exactly the limit when nothing is transferring", len(kept))
	}
}
