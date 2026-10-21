package ui

import (
	"strings"
	"testing"
	"time"
)

// --- 24h trend -----------------------------------------------------------

func TestTrendRecordsPerMinutePeak(t *testing.T) {
	var h trendHistory
	base := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)

	h.record(base, 100, 10)
	h.record(base.Add(20*time.Second), 500, 5) // same minute, higher peak
	h.record(base.Add(time.Minute), 50, 50)

	if len(h.Points) != 2 {
		t.Fatalf("%d points, want 2 minutes", len(h.Points))
	}
	if h.Points[0].RxPeak != 500 {
		t.Errorf("RxPeak = %v, want the minute's peak (500), not the latest", h.Points[0].RxPeak)
	}
	if h.Points[0].TxPeak != 10 {
		t.Errorf("TxPeak = %v, want 10", h.Points[0].TxPeak)
	}
}

func TestTrendTrimsToWindow(t *testing.T) {
	var h trendHistory
	now := time.Now()
	h.record(now.Add(-48*time.Hour), 1, 1)
	h.record(now.Add(-30*time.Hour), 2, 2)
	h.record(now, 3, 3)

	if len(h.Points) != 1 {
		t.Fatalf("%d points, want only the one inside the 24h window", len(h.Points))
	}
	if h.Points[0].RxPeak != 3 {
		t.Errorf("kept the wrong point: %+v", h.Points[0])
	}
}

// A gap must render as a gap, not be compressed away — otherwise a restart
// silently rewrites the time axis.
func TestTrendSeriesFillsGaps(t *testing.T) {
	var h trendHistory
	now := time.Now()
	h.record(now.Add(-10*time.Minute), 100, 100)
	h.record(now, 200, 200)

	rx, tx := h.series(now, 11)
	if len(rx) != 11 || len(tx) != 11 {
		t.Fatalf("series lengths = %d/%d, want 11", len(rx), len(tx))
	}
	if rx[0] != 100 {
		t.Errorf("rx[0] = %v, want the ten-minutes-ago sample", rx[0])
	}
	if rx[len(rx)-1] != 200 {
		t.Errorf("rx[last] = %v, want the latest sample", rx[len(rx)-1])
	}
	for i := 1; i < len(rx)-1; i++ {
		if rx[i] != 0 {
			t.Errorf("rx[%d] = %v, want a zero-filled gap", i, rx[i])
		}
	}
}

func TestTrendSurvivesARestart(t *testing.T) {
	var h trendHistory
	now := time.Now()
	h.record(now.Add(-2*time.Minute), 1234, 5678)
	h.record(now, 10, 20)
	h.save()

	loaded := loadTrend()
	if len(loaded.Points) != 2 {
		t.Fatalf("%d points after a reload, want 2", len(loaded.Points))
	}
	if loaded.Points[0].RxPeak != 1234 || loaded.Points[0].TxPeak != 5678 {
		t.Errorf("point did not survive: %+v", loaded.Points[0])
	}
}

// A snapshot or a remote session describes another machine; writing its
// numbers into this host's history would corrupt the record silently.
func TestTrendIgnoresForeignData(t *testing.T) {
	m := NewModel()
	m.snapshotOnly = true
	m.recordTrend(1e9, 1e9, time.Now())
	if len(m.trend.Points) != 0 {
		t.Error("a snapshot's throughput was written into this host's history")
	}
}

func TestTrendPanelNeedsHistory(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 45
	if m.renderTrend(120) != "" {
		t.Error("the trend panel rendered with no history")
	}

	now := time.Now()
	m.trend.record(now.Add(-time.Minute), 1000, 2000)
	m.trend.record(now, 3000, 4000)
	txt := stripANSI(m.renderTrend(120))
	if !strings.Contains(txt, "RX") || !strings.Contains(txt, "TX") {
		t.Errorf("trend panel missing its series:\n%s", txt)
	}
	if !strings.Contains(txt, "peak") {
		t.Errorf("trend panel does not state the peak:\n%s", txt)
	}
}
