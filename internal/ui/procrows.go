package ui

import (
	"fmt"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// A browser is forty processes called firefox-bin, and no single one of them
// answers "what is this application doing to my network". Collapsing rows that
// share a name (toggled with `A`) makes the table answer that question, while
// the un-aggregated view stays available for the cases where the individual
// process is the point.

// procRow is one row of the Processes table: a single process, or several
// sharing a name folded together.
type procRow struct {
	probe.ProcNet
	members int     // how many processes are folded in
	pids    []int32 // their PIDs, for summing throughput
	depth   int     // indentation in the tree view (see proctree.go)
}

// mixedValue marks a column whose folded members disagree — a name shared by
// processes running as different users, say.
const mixedValue = "*"

// aggregateByName folds rows sharing a process name into one. Counts sum;
// columns whose members disagree are marked rather than silently showing the
// first value.
func aggregateByName(rows []procRow) []procRow {
	byName := make(map[string]*procRow, len(rows))
	order := make([]string, 0, len(rows))

	for _, r := range rows {
		name := r.Name
		if name == "" {
			name = "-"
		}

		agg, ok := byName[name]
		if !ok {
			copied := r
			copied.Name = name
			byName[name] = &copied
			order = append(order, name)
			continue
		}

		agg.ConnCount += r.ConnCount
		agg.ListenCount += r.ListenCount
		agg.CPU += r.CPU
		agg.Mem += r.Mem
		agg.members += r.members
		agg.pids = append(agg.pids, r.pids...)
		if r.PID < agg.PID {
			agg.PID = r.PID // the lowest PID stands for the group
		}
		if agg.User != r.User {
			agg.User = mixedValue
		}
		if agg.Container != r.Container {
			agg.Container = mixedValue
		}
	}

	out := make([]procRow, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out
}

// rowRate is a row's current throughput: the process's own, or the sum of the
// group's.
func (m Model) rowRate(r procRow) rateSample {
	if r.members <= 1 {
		return m.procRate(r.PID)
	}
	var out rateSample
	for _, pid := range r.pids {
		s := m.procRate(pid)
		out.rx += s.rx
		out.tx += s.tx
	}
	return out
}

// rowTotal is a row's session traffic, summed the same way.
func (m Model) rowTotal(r procRow) byteTotals {
	if r.members <= 1 {
		return m.procTotal(r.PID)
	}
	var out byteTotals
	for _, pid := range r.pids {
		t := m.procTotal(pid)
		out.rx += t.rx
		out.tx += t.tx
	}
	return out
}

// pidCell renders the PID column: the number for a single process, or how many
// processes the row stands for.
func (r procRow) pidCell() string {
	if r.members > 1 {
		return fmt.Sprintf("×%d", r.members)
	}
	return fmt.Sprintf("%d", r.PID)
}

// rowHistory is the throughput history to chart for a row. A group has no
// single history, so only individual processes get a sparkline.
func (m Model) rowHistory(r procRow) *rateHistory {
	if r.members > 1 {
		return nil
	}
	return m.procRateHist[r.PID]
}
