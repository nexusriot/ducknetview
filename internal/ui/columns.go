package ui

import "strings"

// Each table describes its columns once, and both the header line and the
// mouse hit-testing read that description. Without a shared description the
// two drift, and a click lands on the wrong column as soon as a conditional
// column (throughput, container) appears or disappears.

// colSep is the gap rendered between columns.
const colSep = "  "

// tableContentX is the screen column where a table's first character sits: the
// box border and padding (2), then the cursor gutter (2).
const tableContentX = 4

// colSpec is one rendered column. key identifies it (so the renderer can look
// its width back up); sort is the sort key clicking it selects, empty when the
// column is not sortable. Two columns may share a sort key — the grouped view's
// RX and TX both sort by total bytes.
type colSpec struct {
	key   string
	title string
	width int
	sort  string
}

type colSpecs []colSpec

// width returns the rendered width of a column by key.
func (cs colSpecs) width(key string) int {
	for _, c := range cs {
		if c.key == key {
			return c.width
		}
	}
	return 0
}

// span is the total width of the columns plus their separators, which is what
// the header rule is drawn across.
func (cs colSpecs) span() int {
	total := 0
	for i, c := range cs {
		if i > 0 {
			total += len(colSep)
		}
		total += c.width
	}
	return total
}

// header renders the column header line, marking the active sort column with a
// direction arrow so the ordering is visible without reading the subtitle.
func (cs colSpecs) header(sortKey string, desc bool) string {
	var b strings.Builder
	for i, c := range cs {
		if i > 0 {
			b.WriteString(colSep)
		}

		title := c.title
		if c.sort != "" && c.sort == sortKey {
			arrow := "↑"
			if desc {
				arrow = "↓"
			}
			// The arrow has to fit inside the column, so the title gives way to
			// it rather than pushing the row alignment out.
			title = trunc(title, max(1, c.width-1)) + arrow
			b.WriteString(titleStyle.Render(padRight(title, c.width)))
			continue
		}
		if c.width == 0 {
			b.WriteString(title)
			continue
		}
		b.WriteString(padRight(trunc(title, c.width), c.width))
	}
	return b.String()
}

// keyAt maps an x offset (relative to the first column) to a sortable column
// key. ok is false for gaps between columns and for unsortable columns.
func (cs colSpecs) keyAt(x int) (string, bool) {
	if x < 0 {
		return "", false
	}
	at := 0
	for i, c := range cs {
		if i > 0 {
			at += len(colSep)
			if x < at {
				return "", false // in the gap between two columns
			}
		}
		// A zero-width final column extends to the end of the line.
		if c.width == 0 {
			if c.sort == "" {
				return "", false
			}
			return c.sort, true
		}
		if x < at+c.width {
			if c.sort == "" {
				return "", false
			}
			return c.sort, true
		}
		at += c.width
	}
	return "", false
}

// headerLineIndex is the content line the column header occupies inside a
// table's viewport: title, subtitle, blank, header, rule.
const headerLineIndex = 3

// activeColumns returns the column layout the active tab last rendered with, so
// the mouse hit-testing and the header agree even as conditional columns come
// and go. It is derived by re-running the layout for the current width, which is
// exactly what the renderer does.
func (m Model) activeColumns() colSpecs {
	switch m.activeTab {
	case tabPorts:
		return m.portsColumns()
	case tabProcs:
		return m.procsColumns()
	case tabConns:
		switch {
		case m.showClosed:
			return m.closedColumns()
		case m.connsGroup:
			return m.groupColumns()
		default:
			return m.connsColumns()
		}
	}
	return nil
}

// mouseHeaderKey reports the sortable column a click landed on, given the
// click's screen coordinates and the viewport's scroll offset.
func mouseHeaderKey(cols colSpecs, yOffset, x, y int) (string, bool) {
	const topOffset = 4 // header row, box border, search line, blank
	if y != topOffset+headerLineIndex-yOffset {
		return "", false
	}
	return cols.keyAt(x - tableContentX)
}
