package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestStatsRequestAnsweredFromTheUpdateLoop(t *testing.T) {
	m := statsModel()
	reply := make(chan SessionStats, 1)

	out, _ := m.Update(StatsRequest{Reply: reply})
	if out == nil {
		t.Fatal("StatsRequest returned no model")
	}

	select {
	case s := <-reply:
		if s.Snap.Hostname != "statshost" {
			t.Errorf("Snap.Hostname = %q", s.Snap.Hostname)
		}
		if len(s.Info.Conns) != 1 || len(s.Info.Ports) != 1 || len(s.Info.Procs) != 1 {
			t.Errorf("Info did not carry the tables: %+v", s.Info)
		}
		if s.Polls != 7 {
			t.Errorf("Polls = %d", s.Polls)
		}
		if s.Totals.rx != 111 || s.Totals.tx != 222 {
			t.Errorf("Totals = %+v", s.Totals)
		}
	default:
		t.Fatal("no reply was sent")
	}
}

// A scraper that has already timed out leaves an unread channel behind; the
// update loop must not block on it.
func TestStatsRequestDoesNotBlock(t *testing.T) {
	m := statsModel()
	full := make(chan SessionStats) // unbuffered, nobody receiving

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Update(StatsRequest{Reply: full})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Update blocked on an abandoned reply channel")
	}
}

// The age of the last poll is the one number that can reveal a daemon which is
// up but no longer working, so "never polled" must be distinguishable from
// "polled just now".
func TestLastPollAgeBeforeTheFirstPoll(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 45
	out := FormatSessionMetrics(m.sessionStats(), "0")
	if !strings.Contains(out, "ducknetview_last_poll_age_seconds -1") {
		t.Errorf("want -1 before the first poll, got:\n%s", out)
	}

	m.lastPollAt = time.Now()
	out = FormatSessionMetrics(m.sessionStats(), "0")
	if strings.Contains(out, "ducknetview_last_poll_age_seconds -1") {
		t.Errorf("still -1 after a poll:\n%s", out)
	}
}

func TestStatsBaselineCountsComeFromTheSession(t *testing.T) {
	m := statsModel()
	if s := m.sessionStats(); s.BaselineKnown {
		t.Error("BaselineKnown = true with no baseline saved")
	}

	m.baseline = map[string]bool{"tcp|0.0.0.0:22": true}
	m.baselineRaw = []string{"tcp|0.0.0.0:22"}
	m.ports = append(m.ports, probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:8080", PID: 2, Process: "nginx"})

	s := m.sessionStats()
	if !s.BaselineKnown {
		t.Fatal("BaselineKnown = false with a baseline saved")
	}
	if s.OffBaseline != 1 {
		t.Errorf("OffBaseline = %d, want 1", s.OffBaseline)
	}
}
