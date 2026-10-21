package ui

import (
	"testing"
)

// Sparklines are anchored at zero so bar height means "share of the peak" and
// an idle stretch is visibly empty.
func TestSparkIsZeroAnchored(t *testing.T) {
	if got := Spark([]float64{0, 0, 0, 100}, 4); got != "▁▁▁█" {
		t.Errorf("Spark = %q, want idle bars low and the peak full", got)
	}
	// A steady stream is at its peak the whole way, not flat along the bottom.
	if got := Spark([]float64{50, 50, 50}, 3); got != "███" {
		t.Errorf("steady series = %q, want full-height bars", got)
	}
	if got := Spark([]float64{0, 0}, 2); got != "▁▁" {
		t.Errorf("all-zero series = %q, want the lowest block", got)
	}
	if got := Spark([]float64{1, 2}, 5); len([]rune(got)) != 5 {
		t.Errorf("Spark(width 5) rendered %d cells", len([]rune(got)))
	}
}
