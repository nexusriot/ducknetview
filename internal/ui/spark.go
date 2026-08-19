package ui

import "strings"

// sparkline blocks: low -> high
var blocks = []rune("▁▂▃▄▅▆▇█")

// Spark renders a series as a sparkline scaled from zero to the window's peak.
// Anchoring at zero (rather than at the window minimum) is what makes the chart
// readable next to a peak label: a bar's height is its share of the peak, an
// empty stretch really is idle, and a steady stream doesn't masquerade as a
// flat line at the bottom of the chart.
func Spark(values []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(values) == 0 {
		return strings.Repeat(" ", width)
	}
	// take last width points
	if len(values) > width {
		values = values[len(values)-width:]
	}

	maxV := 0.0
	for _, v := range values {
		if v > maxV {
			maxV = v
		}
	}
	if maxV <= 0 {
		return strings.Repeat(string(blocks[0]), len(values)) + strings.Repeat(" ", width-len(values))
	}

	var b strings.Builder
	for _, v := range values {
		if v < 0 {
			v = 0
		}
		idx := int(v / maxV * float64(len(blocks)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		b.WriteRune(blocks[idx])
	}
	// pad to width
	if len(values) < width {
		b.WriteString(strings.Repeat(" ", width-len(values)))
	}
	return b.String()
}
