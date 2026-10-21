package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Every sparkline in the tool dies with the session, and usage.json keeps only
// whole days — so "when did that spike happen?" is unanswerable the moment you
// restart, which is exactly when you tend to ask it. A minute-resolution ring
// covering a day is one small file and answers it.

const (
	// trendResolution is how long one bucket covers. A minute is fine enough to
	// see a transfer and coarse enough that a day is a small file.
	trendResolution = time.Minute
	// trendWindow is how much history is kept.
	trendWindow = 24 * time.Hour
	// trendBuckets is the resulting ring size.
	trendBuckets = int(trendWindow / trendResolution)
	// trendFlushEvery is how often the file is rewritten. Writing every minute
	// would be pointless churn on a laptop's disk.
	trendFlushEvery = 5 * time.Minute
)

// trendPoint is one minute of aggregate throughput: the peak seen in that
// minute rather than the mean, because a 10-second burst averaged over 60
// seconds looks like nothing.
type trendPoint struct {
	Minute int64   `json:"m"` // unix minute
	RxPeak float64 `json:"rx"`
	TxPeak float64 `json:"tx"`
}

// trendHistory is the on-disk shape: oldest first.
type trendHistory struct {
	Points []trendPoint `json:"points"`
}

func trendPath() (string, error) {
	p, err := configPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "trend.json"), nil
}

// loadTrend reads the recorded history. A missing or corrupt file is an empty
// history, not an error — this is a convenience, not a ledger.
func loadTrend() trendHistory {
	p, err := trendPath()
	if err != nil {
		return trendHistory{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return trendHistory{}
	}
	var h trendHistory
	if json.Unmarshal(b, &h) != nil {
		return trendHistory{}
	}
	h.trim(time.Now())
	return h
}

// trim drops points outside the window and keeps the ring ordered.
func (h *trendHistory) trim(now time.Time) {
	cutoff := now.Add(-trendWindow).Unix() / 60
	kept := h.Points[:0]
	for _, p := range h.Points {
		if p.Minute >= cutoff {
			kept = append(kept, p)
		}
	}
	h.Points = kept
	sort.Slice(h.Points, func(i, j int) bool { return h.Points[i].Minute < h.Points[j].Minute })
	if len(h.Points) > trendBuckets {
		h.Points = h.Points[len(h.Points)-trendBuckets:]
	}
}

// record folds one sample into the current minute's bucket.
func (h *trendHistory) record(at time.Time, rx, tx float64) {
	minute := at.Unix() / 60
	if n := len(h.Points); n > 0 && h.Points[n-1].Minute == minute {
		p := &h.Points[n-1]
		p.RxPeak = max64(p.RxPeak, rx)
		p.TxPeak = max64(p.TxPeak, tx)
		return
	}
	h.Points = append(h.Points, trendPoint{Minute: minute, RxPeak: rx, TxPeak: tx})
	h.trim(at)
}

// series returns the last n minutes as plain slices for the sparkline, with
// gaps filled as zero so a restart does not compress the time axis.
func (h trendHistory) series(now time.Time, minutes int) (rx, tx []float64) {
	if minutes <= 0 {
		return nil, nil
	}
	end := now.Unix() / 60
	start := end - int64(minutes) + 1

	byMinute := make(map[int64]trendPoint, len(h.Points))
	for _, p := range h.Points {
		byMinute[p.Minute] = p
	}
	rx = make([]float64, 0, minutes)
	tx = make([]float64, 0, minutes)
	for m := start; m <= end; m++ {
		p := byMinute[m] // zero value is exactly the right answer for a gap
		rx = append(rx, p.RxPeak)
		tx = append(tx, p.TxPeak)
	}
	return rx, tx
}

// span reports the oldest and newest recorded minutes, for the panel's label.
func (h trendHistory) span() (time.Time, time.Time, bool) {
	if len(h.Points) == 0 {
		return time.Time{}, time.Time{}, false
	}
	return time.Unix(h.Points[0].Minute*60, 0),
		time.Unix(h.Points[len(h.Points)-1].Minute*60, 0), true
}

// save writes the history, best-effort.
func (h trendHistory) save() {
	p, err := trendPath()
	if err != nil {
		return
	}
	b, err := json.Marshal(h)
	if err != nil {
		return
	}
	_ = writeFileAtomic(p, append(b, '\n'), 0o644)
}

// recordTrend folds one interface sample into the long history, flushing to
// disk occasionally rather than on every sample.
func (m *Model) recordTrend(rx, tx float64, now time.Time) {
	// A snapshot or a remote session describes another machine; writing its
	// numbers into this host's history would corrupt the record silently.
	if m.snapshotOnly {
		return
	}
	m.trend.record(now, rx, tx)
	if now.Sub(m.trendFlushed) < trendFlushEvery {
		return
	}
	m.trendFlushed = now
	m.trend.save()
	m.saveUsage()
}

// renderTrend draws the day-long view. It is the one panel that can answer a
// question about something that happened before the session started.
func (m Model) renderTrend(width int) string {
	if len(m.trend.Points) < 2 {
		return "" // one point is not a history
	}

	chartW := max(20, min(width-8, 110))
	rx, tx := m.trend.series(time.Now(), chartW)

	var peak float64
	for i := range rx {
		peak = max64(peak, max64(rx[i], tx[i]))
	}

	label := "Last 24 hours"
	if from, _, ok := m.trend.span(); ok {
		if age := time.Since(from); age < trendWindow {
			label = "Last " + roundDuration(age)
		}
	}

	out := titleStyle.Render(label) + "  " +
		subtleStyle.Render("per-minute peak across all links · peak "+m.rate(peak)) + "\n"
	out += "RX " + Spark(rx, chartW) + "\n"
	out += "TX " + Spark(tx, chartW) + "\n\n"
	return out
}

// roundDuration words an age for the panel title without decimals.
func roundDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return d.Round(time.Hour).String()
	case d >= time.Minute:
		return d.Round(time.Minute).String()
	default:
		return d.Round(time.Second).String()
	}
}
