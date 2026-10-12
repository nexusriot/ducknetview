package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// A snapshot is what --json already emits. Being able to read one back turns
// the same binary into a viewer for another host:
//
//	ssh host ducknetview --json | ducknetview --from -
//
// which is a great deal cheaper than teaching it to talk to remote agents.

// Snapshot is the --json output: main.go marshals this type rather than
// assembling a map, so what --from can read is by construction what --json
// writes.
type Snapshot struct {
	Hostname      string             `json:"hostname"`
	UptimeSeconds int64              `json:"uptime_seconds"`
	TakenAt       string             `json:"taken_at"`
	Version       string             `json:"version,omitempty"`
	Interfaces    []probe.IfaceInfo  `json:"interfaces"`
	Ports         []probe.ListenPort `json:"ports"`
	Processes     []probe.ProcNet    `json:"processes"`
	Connections   []probe.Conn       `json:"connections"`

	// The host's own configuration. Older snapshots have none of this, which
	// is why every field is omitempty and the Routes tab says what it is
	// looking at rather than assuming.
	Routes     []probe.Route             `json:"routes,omitempty"`
	Neighbours []probe.Neighbour         `json:"neighbours,omitempty"`
	Resolvers  []probe.Resolver          `json:"resolvers,omitempty"`
	Wifi       map[string]probe.WifiLink `json:"wifi,omitempty"`
	Latency    []probe.LatencySample     `json:"latency,omitempty"`
}

// HasHostState reports whether the snapshot carries the host's configuration,
// which snapshots written before 0.4.0 do not.
func (s Snapshot) HasHostState() bool {
	return len(s.Routes) > 0 || len(s.Resolvers) > 0 || len(s.Neighbours) > 0
}

// LoadSnapshot reads a snapshot from a file, or from stdin when path is "-".
func LoadSnapshot(path string) (Snapshot, error) {
	var r io.Reader = os.Stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return Snapshot{}, err
		}
		defer f.Close()
		r = f
	}

	b, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return Snapshot{}, err
	}
	s, err := decodeSnapshot(b)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// decodeSnapshot parses snapshot JSON. It is separate from LoadSnapshot because
// a live remote session gets the same bytes from a pipe rather than a file.
func decodeSnapshot(b []byte) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return Snapshot{}, fmt.Errorf("not a ducknetview --json snapshot: %w", err)
	}
	if s.Hostname == "" && len(s.Interfaces) == 0 && len(s.Connections) == 0 {
		return Snapshot{}, fmt.Errorf("snapshot contains no data")
	}
	return s, nil
}

// remoteNetSnapshot is the interface half of a snapshot, in the shape the
// sampler would have produced.
func remoteNetSnapshot(s Snapshot, takenAt time.Time) probe.NetSnapshot {
	return probe.NetSnapshot{
		Hostname: s.Hostname,
		Uptime:   time.Duration(s.UptimeSeconds) * time.Second,
		Ifaces:   s.Interfaces,
		TakenAt:  takenAt,
	}
}

// remoteConnInfo is the socket half, in the shape probe.Collect would have
// produced.
func remoteConnInfo(s Snapshot) probe.ConnInfo {
	return probe.ConnInfo{Ports: s.Ports, Procs: s.Processes, Conns: s.Connections}
}

// WithSnapshot loads a snapshot into the model and freezes it: there is nothing
// to refresh, and pretending otherwise would replace the file's contents with
// the local host's on the first tick.
func (m Model) WithSnapshot(s Snapshot) Model {
	takenAt := time.Now()
	if t, err := time.Parse(time.RFC3339, s.TakenAt); err == nil {
		takenAt = t
	}

	m.lastSnap = probe.NetSnapshot{
		Hostname: s.Hostname,
		Uptime:   time.Duration(s.UptimeSeconds) * time.Second,
		Ifaces:   s.Interfaces,
		TakenAt:  takenAt,
	}
	m.ports = s.Ports
	m.procs = s.Processes
	m.conns = s.Connections
	m.snapshotOnly = true
	m.paused = true // a file has no next sample

	// Cumulative counters are present, so the throughput columns are meaningful
	// as totals even though no rate can be derived from a single sample.
	for _, c := range s.Connections {
		if c.RxBytes > 0 || c.TxBytes > 0 {
			m.hasThroughput = true
			m.showTotals = true
			break
		}
	}

	// The snapshot carries both interfaces and connections, so a socket can be
	// attributed to its link even though nothing is being sampled.
	m.connIface = make(map[string]string, len(s.Connections))
	for _, c := range s.Connections {
		m.connIface[connKey(c)] = probe.IfaceForIP(s.Interfaces, stripPort(c.Local))
	}

	// The host's own configuration, when the snapshot is new enough to carry
	// it. This is what lets the Routes tab describe the remote host instead of
	// explaining why it cannot.
	m.routes = s.Routes
	m.neighbours = s.Neighbours
	m.resolvers = s.Resolvers
	m.wifi = s.Wifi
	m.snapshotHostState = s.HasHostState()
	if len(s.Latency) > 0 {
		m.latency = s.Latency
	}

	m.rebuildIfaceList()
	m.status = fmt.Sprintf("Snapshot of %s taken %s — frozen", s.Hostname, takenAt.Format("2006-01-02 15:04:05"))
	return m
}
