package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestUsageHistoryMerge(t *testing.T) {
	h := usageHistory{}
	h = h.mergeSession("2026-08-10", byteTotals{rx: 100, tx: 50},
		map[string]uint64{"curl": 150}, map[string]uint64{"1.1.1.1": 150})
	h = h.mergeSession("2026-08-10", byteTotals{rx: 200, tx: 100},
		map[string]uint64{"curl": 300, "wget": 60}, map[string]uint64{"1.1.1.1": 360})

	if len(h.Days) != 1 {
		t.Fatalf("days = %d, want the two sessions folded into one day", len(h.Days))
	}
	d := h.Days[0]
	if d.RxBytes != 300 || d.TxBytes != 150 {
		t.Errorf("day totals = %+v, want rx 300 tx 150", d)
	}
	if d.Processes["curl"] != 450 || d.Processes["wget"] != 60 {
		t.Errorf("per-process totals = %v", d.Processes)
	}

	// Older days are trimmed, newest kept.
	for i := 0; i < historyDays+10; i++ {
		day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format("2006-01-02")
		h = h.mergeSession(day, byteTotals{rx: 1}, nil, nil)
	}
	if len(h.Days) != historyDays {
		t.Errorf("history holds %d days, want the %d cap", len(h.Days), historyDays)
	}
	for i := 1; i < len(h.Days); i++ {
		if h.Days[i-1].Date > h.Days[i].Date {
			t.Fatalf("days out of order at %d: %s then %s", i, h.Days[i-1].Date, h.Days[i].Date)
		}
	}
}

func TestUsageHistoryTrimsPerDayNames(t *testing.T) {
	big := map[string]uint64{}
	for i := 0; i < historyTopN+40; i++ {
		big[string(rune('a'+i%26))+strings.Repeat("x", i)] = uint64(i + 1)
	}
	h := usageHistory{}.mergeSession("2026-08-10", byteTotals{rx: 1}, big, big)

	d := h.Days[0]
	if len(d.Processes) != historyTopN {
		t.Errorf("kept %d processes, want the %d cap", len(d.Processes), historyTopN)
	}
	// The biggest survive the trim.
	var smallest uint64 = ^uint64(0)
	for _, n := range d.Processes {
		if n < smallest {
			smallest = n
		}
	}
	if smallest <= 40 {
		t.Errorf("trim kept a small entry (%d); the biggest should survive", smallest)
	}
}

func TestUsageSavedOnQuit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := routesModel()
	m.addTotals(probe.Conn{PID: 10, Process: "curl", Remote: "1.2.3.4:443"}, 4096, 2048, time.Now())

	m.quit()

	h := LoadUsageHistory()
	if len(h.Days) != 1 {
		t.Fatalf("history has %d days, want 1", len(h.Days))
	}
	if h.Days[0].RxBytes != 4096 {
		t.Errorf("recorded %d received, want 4096", h.Days[0].RxBytes)
	}
	out := FormatUsage(0)
	if !strings.Contains(out, "curl") || !strings.Contains(out, "4.0 KiB") {
		t.Errorf("usage report:\n%s", out)
	}

	// Nothing to record is not an empty file full of zeroes.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	routesModel().quit()
	if got := FormatUsage(0); !strings.Contains(got, "no usage recorded") {
		t.Errorf("empty history report = %q", got)
	}
}

// The history is flushed periodically as well as on quit; each flush must
// write only what was counted since the last one.
func TestUsageFlushIsIncremental(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := routesModel()
	c := probe.Conn{PID: 10, Process: "curl", Remote: "1.2.3.4:443"}
	m.addTotals(c, 1000, 0, time.Now())
	m.saveUsage()
	m.addTotals(c, 500, 0, time.Now())
	m.saveUsage()
	m.saveUsage() // nothing new: no change
	m.quit()

	h := LoadUsageHistory()
	if len(h.Days) != 1 || h.Days[0].RxBytes != 1500 {
		t.Fatalf("history = %+v, want one day with 1500 received", h.Days)
	}
	if got := h.Days[0].Processes["curl"]; got != 1500 {
		t.Errorf("curl booked %d, want 1500", got)
	}
}
