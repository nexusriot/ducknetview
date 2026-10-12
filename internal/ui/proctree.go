package ui

import (
	"sort"
	"strings"
)

// `A` folds processes that share a name, which answers "what is this
// application doing". It cannot answer "what started this", and that is the
// other question a list of forty sockets provokes — a stray curl, a worker
// under a supervisor, a shell under a session. The parent is already collected
// for the detail overlay; arranging the rows by it is the whole feature.

// treeIndent is the prefix depth costs. Kept small: the PID column is narrow
// and a deep tree must not push the name out of the table.
const treeIndent = 2

// treeDepthLimit bounds how deep the indentation goes. Anything deeper is
// still shown, just not pushed further right.
const treeDepthLimit = 6

// arrangeTree orders rows parent-before-child and records each row's depth.
// Rows whose parent is not in the list (the common case: the parent holds no
// sockets) are roots, so nothing is ever hidden by being reparented.
func arrangeTree(rows []procRow) []procRow {
	if len(rows) <= 1 {
		return rows
	}

	byPID := make(map[int32]int, len(rows))
	for i, r := range rows {
		byPID[r.PID] = i
	}

	children := make(map[int32][]int, len(rows))
	var roots []int
	for i, r := range rows {
		// A process is only a child here if its parent is also on screen, and
		// never its own parent — /proc has been known to report pid 2's parent
		// as pid 2 in odd namespaces.
		if parent, ok := byPID[r.PPID]; ok && r.PPID != r.PID && parent != i {
			children[r.PPID] = append(children[r.PPID], i)
			continue
		}
		roots = append(roots, i)
	}

	// Children keep the order the sort put them in, so the active sort still
	// decides which sibling comes first.
	for pid := range children {
		sort.SliceStable(children[pid], func(a, b int) bool { return children[pid][a] < children[pid][b] })
	}

	out := make([]procRow, 0, len(rows))
	seen := make(map[int]bool, len(rows))

	var walk func(i, depth int)
	walk = func(i, depth int) {
		if seen[i] {
			return // a cycle in /proc must not become an infinite tree
		}
		seen[i] = true
		r := rows[i]
		r.depth = depth
		out = append(out, r)
		for _, c := range children[rows[i].PID] {
			walk(c, depth+1)
		}
	}
	for _, i := range roots {
		walk(i, 0)
	}
	// Anything left over was part of a cycle; appending it keeps the table
	// complete rather than silently dropping rows.
	for i := range rows {
		if !seen[i] {
			out = append(out, rows[i])
		}
	}
	return out
}

// treePrefix is the indentation for a row's name cell.
func treePrefix(depth int) string {
	if depth <= 0 {
		return ""
	}
	if depth > treeDepthLimit {
		depth = treeDepthLimit
	}
	return strings.Repeat(" ", (depth-1)*treeIndent) + "└ "
}
