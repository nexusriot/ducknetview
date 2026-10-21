package ui

import (
	"strings"
	"testing"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestProcessAggregationByName(t *testing.T) {
	m := eventsModel()
	m.activeTab = tabProcs
	m.hasThroughput = true
	m.procs = []probe.ProcNet{
		{PID: 10, Name: "firefox", User: "vlad", ConnCount: 3, ListenCount: 0, CPU: 5, Mem: 2},
		{PID: 11, Name: "firefox", User: "vlad", ConnCount: 4, ListenCount: 1, CPU: 3, Mem: 1},
		{PID: 12, Name: "firefox", User: "root", ConnCount: 1, CPU: 1, Mem: 1},
		{PID: 20, Name: "sshd", User: "root", ConnCount: 2, ListenCount: 1},
	}
	m.procRates = map[int32]rateSample{10: {rx: 1000}, 11: {rx: 2000}, 12: {rx: 500}}
	m.procTotals = map[int32]*talker{
		10: {label: "firefox", totals: byteTotals{rx: 10}},
		11: {label: "firefox", totals: byteTotals{rx: 20}},
	}

	if got := len(m.filteredProcs()); got != 4 {
		t.Fatalf("un-aggregated rows = %d, want 4", got)
	}

	model, _ := m.Update(key("A"))
	m = model.(Model)
	if !m.aggregateProcs {
		t.Fatal("A should turn on aggregation")
	}

	rows := m.filteredProcs()
	if len(rows) != 2 {
		t.Fatalf("aggregated rows = %d, want 2", len(rows))
	}

	var ff procRow
	for _, r := range rows {
		if r.Name == "firefox" {
			ff = r
		}
	}
	if ff.members != 3 {
		t.Errorf("firefox row folds %d processes, want 3", ff.members)
	}
	if ff.ConnCount != 8 || ff.ListenCount != 1 {
		t.Errorf("counts = conns %d listen %d, want 8 and 1", ff.ConnCount, ff.ListenCount)
	}
	if ff.CPU != 9 {
		t.Errorf("CPU = %v, want the sum (9)", ff.CPU)
	}
	if ff.User != mixedValue {
		t.Errorf("user = %q, want the mixed marker for differing owners", ff.User)
	}
	if ff.PID != 10 {
		t.Errorf("representative PID = %d, want the lowest (10)", ff.PID)
	}
	// Throughput sums across the members.
	if got := m.rowRate(ff); got.rx != 3500 {
		t.Errorf("group rate = %v, want 3500", got.rx)
	}
	if got := m.rowTotal(ff); got.rx != 30 {
		t.Errorf("group total = %v, want 30", got.rx)
	}

	// The PID column says how many processes the row stands for.
	if got := ff.pidCell(); got != "×3" {
		t.Errorf("pid cell = %q, want ×3", got)
	}
	text := m.renderProcsText()
	if !strings.Contains(text, "×3") || !strings.Contains(text, "firefox") {
		t.Errorf("aggregated row missing from the table:\n%s", text)
	}

	// Signalling a group is refused: it stands for several processes.
	for i, r := range rows {
		if r.Name == "firefox" {
			m.procsCursor = i
		}
	}
	if _, _, ok := m.selectedPID(); ok {
		t.Error("a folded group should not offer a signal target")
	}
	detail, ok := m.detailContent()
	if !ok || !strings.Contains(detail, "Process group") || !strings.Contains(detail, "10") {
		t.Errorf("group detail should name the members:\n%s", detail)
	}

	// And it survives a restart.
	m.saveConfig()
	if !NewModel().WithConfig().aggregateProcs {
		t.Error("aggregation toggle did not persist")
	}
}
