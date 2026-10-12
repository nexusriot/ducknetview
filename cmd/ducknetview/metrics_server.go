package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
	"github.com/nexusriot/ducknetview/internal/ui"
	"github.com/nexusriot/ducknetview/internal/version"
)

// --metrics writes one scrape to a file, which is the cron-and-textfile half of
// feeding Prometheus. The other half is being scraped directly, and that is a
// handler over the same function.

const (
	// metricsCacheTTL is how long a scrape is reused. Producing one costs a
	// second of wall time (rates need two samples), so two scrapers arriving
	// together, or one with a short interval, would otherwise keep the host
	// enumerating sockets continuously.
	metricsCacheTTL = 5 * time.Second
	// metricsReadTimeout and metricsWriteTimeout keep a stuck client from
	// holding a connection open indefinitely. The write budget has to exceed
	// the sampling second with room to spare.
	metricsReadTimeout  = 5 * time.Second
	metricsWriteTimeout = 30 * time.Second
)

// statsTimeout bounds the wait for a running session to answer. It only
// expires if the update loop is stuck, which is itself worth knowing — so the
// scrape falls back to sampling rather than failing. A variable so a test can
// exercise that fallback without waiting two seconds for it.
var statsTimeout = 2 * time.Second

// statsSource is a running session that can be asked for its own state. A
// standalone exporter has none, and samples instead.
type statsSource interface {
	Send(msg tea.Msg)
}

// metricsCache serialises scrapes and reuses a recent one.
type metricsCache struct {
	mu   sync.Mutex
	body string
	at   time.Time

	// When set, the scrape is answered from this session's live state rather
	// than by sampling the host again. It is attached after the listener is
	// bound, because binding has to be able to fail before a program exists.
	session statsSource
}

// attach points the cache at a running session. Scrapes arriving before this
// sample the host, which is what the first second of a session looks like.
func (c *metricsCache) attach(s statsSource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = s
	c.body, c.at = "", time.Time{} // a sampled body must not be served as live
}

func (c *metricsCache) get() (string, error) {
	// Held across the sampling on purpose: concurrent scrapes should queue and
	// then share one result rather than each enumerate every socket.
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.at) < metricsCacheTTL && c.body != "" {
		return c.body, nil
	}

	body, err := c.produce()
	if err != nil {
		return "", err
	}
	c.body, c.at = body, time.Now()
	return body, nil
}

// produce renders one scrape, from the running session when there is one. The
// caller holds c.mu.
func (c *metricsCache) produce() (string, error) {
	if c.session == nil {
		return metricsBody()
	}

	// The session owns the model, so it is asked for a copy rather than read
	// directly. A session that is wedged must not hang the scrape: it falls
	// back to sampling, which is slower but always answerable.
	reply := make(chan ui.SessionStats, 1)
	c.session.Send(ui.StatsRequest{Reply: reply})
	select {
	case stats := <-reply:
		return ui.FormatLiveMetrics(stats, version.Version), nil
	case <-time.After(statsTimeout):
		return metricsBody()
	}
}

// metricsBody produces one scrape's worth of text. Rates need two samples, so
// this takes a second.
func metricsBody() (string, error) {
	s := probe.NewNetSampler()
	if _, err := s.Sample(); err != nil { // prime the interface counters
		return "", err
	}
	time.Sleep(time.Second)
	snap, err := s.Sample()
	if err != nil {
		return "", err
	}
	info, err := probe.Collect(0)
	if err != nil {
		return "", err
	}

	offending, _, known := ui.BaselineCheck(info.Ports)
	return ui.FormatMetrics(snap, info, len(offending), known), nil
}

// metricsHandler serves the text exposition format at /metrics, with a plain
// pointer at the root so a human who opens the address is not met with a 404.
func metricsHandler(cache *metricsCache) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		body, err := cache.get()
		if err != nil {
			http.Error(w, "ducknetview: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, body)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "ducknetview %s\nmetrics: /metrics\n", version.Version)
	})

	return mux
}

// metricsServer is a bound exporter. Binding is separate from attaching the
// session so a taken port fails before anything else is started.
type metricsServer struct {
	srv   *http.Server
	cache *metricsCache
}

func (m *metricsServer) Close() error { return m.srv.Close() }

// Attach points the exporter at a running session, after which scrapes are
// answered from what that session already knows rather than by sampling the
// host all over again.
func (m *metricsServer) Attach(s statsSource) { m.cache.attach(s) }

// serveMetrics starts the exporter. It returns once the listener is bound, so
// a session that also serves metrics fails loudly on a taken port instead of
// running on silently without an exporter.
func serveMetrics(addr string) (*metricsServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	cache := &metricsCache{}
	srv := &http.Server{
		Handler:      metricsHandler(cache),
		ReadTimeout:  metricsReadTimeout,
		WriteTimeout: metricsWriteTimeout,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("ducknetview: metrics listener stopped: %v", err)
		}
	}()
	fmt.Fprintf(os.Stderr, "ducknetview: serving metrics on http://%s/metrics\n", ln.Addr())
	return &metricsServer{srv: srv, cache: cache}, nil
}
