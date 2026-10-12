package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
	"github.com/nexusriot/ducknetview/internal/ui"
)

// fakeSession stands in for a running program: it answers StatsRequest the way
// the update loop does, or — when silent is set — not at all.
type fakeSession struct {
	mu      sync.Mutex
	silent  bool
	asked   int
	stats   ui.SessionStats
	replied chan struct{}
}

func (f *fakeSession) Send(msg tea.Msg) {
	req, ok := msg.(ui.StatsRequest)
	if !ok {
		return
	}
	f.mu.Lock()
	f.asked++
	silent, stats := f.silent, f.stats
	f.mu.Unlock()
	if silent {
		return
	}
	go func() {
		req.Reply <- stats
		if f.replied != nil {
			close(f.replied)
		}
	}()
}

func (f *fakeSession) asks() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked
}

func liveStats() ui.SessionStats {
	return ui.SessionStats{
		Snap: probe.NetSnapshot{
			Hostname: "fake", TakenAt: time.Now(),
			Ifaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true, RxBps: 1234}},
		},
		Info:       probe.ConnInfo{Conns: []probe.Conn{{Proto: "tcp", Remote: "8.8.8.8:443"}}},
		StartedAt:  time.Now().Add(-time.Minute),
		LastPollAt: time.Now(),
		Polls:      11,
	}
}

// The whole point of the feature: a scrape answered from the session, not by
// enumerating every socket again.
func TestMetricsCacheUsesTheSession(t *testing.T) {
	f := &fakeSession{stats: liveStats()}
	c := &metricsCache{}
	c.attach(f)

	body, err := c.get()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if f.asks() != 1 {
		t.Errorf("the session was asked %d times, want 1", f.asks())
	}
	for _, want := range []string{"ducknetview_polls_total 11", `iface="eth0"`, "ducknetview_build_info"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in the scrape:\n%s", want, body)
		}
	}
}

// A second scrape inside the TTL must not ask again, or a short scrape
// interval keeps the update loop answering instead of working.
func TestMetricsCacheReuses(t *testing.T) {
	f := &fakeSession{stats: liveStats()}
	c := &metricsCache{}
	c.attach(f)

	first, _ := c.get()
	second, _ := c.get()
	if first != second {
		t.Error("two scrapes inside the TTL returned different bodies")
	}
	if f.asks() != 1 {
		t.Errorf("the session was asked %d times for two scrapes", f.asks())
	}
}

// A wedged update loop must not wedge the exporter: it falls back to sampling,
// which is slower but always answerable.
func TestMetricsCacheFallsBackWhenTheSessionIsSilent(t *testing.T) {
	restore := statsTimeout
	statsTimeout = 50 * time.Millisecond
	defer func() { statsTimeout = restore }()

	c := &metricsCache{}
	c.attach(&fakeSession{silent: true})

	body, err := c.get()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(body, "ducknetview_iface_receive_bytes_per_second") {
		t.Errorf("the fallback did not sample the host:\n%s", body)
	}
	// Sampling cannot produce the session metrics, and must not pretend to.
	if strings.Contains(body, "ducknetview_polls_total") {
		t.Error("the sampled fallback claimed session metrics")
	}
}

// attach must not let a body produced by sampling be served afterwards as
// though it came from the session.
func TestAttachInvalidatesASampledBody(t *testing.T) {
	c := &metricsCache{}
	if _, err := c.get(); err != nil {
		t.Fatalf("sampled get: %v", err)
	}

	f := &fakeSession{stats: liveStats()}
	c.attach(f)

	body, err := c.get()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if f.asks() != 1 {
		t.Error("the stale sampled body was served instead of asking the session")
	}
	if !strings.Contains(body, "ducknetview_polls_total 11") {
		t.Error("the scrape after attach is not live")
	}
}

// Concurrent scrapes must queue behind one measurement rather than each
// starting their own.
func TestMetricsCacheConcurrentScrapes(t *testing.T) {
	f := &fakeSession{stats: liveStats()}
	c := &metricsCache{}
	c.attach(f)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.get(); err != nil {
				t.Errorf("get: %v", err)
			}
		}()
	}
	wg.Wait()

	if n := f.asks(); n != 1 {
		t.Errorf("8 concurrent scrapes asked the session %d times, want 1", n)
	}
}

func TestMetricsHandlerRoutes(t *testing.T) {
	c := &metricsCache{}
	c.attach(&fakeSession{stats: liveStats()})
	h := metricsHandler(c)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/metrics = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/metrics") {
		t.Errorf("root = %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("/nope = %d, want 404", rec.Code)
	}
}

// Binding has to fail before anything else starts, so a taken port is a
// startup error rather than a silently missing exporter.
func TestServeMetricsReportsATakenPort(t *testing.T) {
	first, err := serveMetrics("127.0.0.1:0")
	if err != nil {
		t.Fatalf("serveMetrics: %v", err)
	}
	defer first.Close()

	if _, err := serveMetrics("127.0.0.1:1"); err == nil {
		t.Error("serveMetrics accepted a port it cannot bind")
	}
}
