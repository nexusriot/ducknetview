package probe

import (
	"encoding/json"
	"errors"
	"net"
	"syscall"
	"time"
)

// Round-trip time to a socket's peer only exists for sockets you already have.
// To answer "is the network itself healthy right now", something has to be
// probed on purpose — and the honest default is the first hop, because it needs
// no traffic to leave the LAN.

// LatencyTarget is a host to measure the round trip to.
type LatencyTarget struct {
	Label string
	Addr  string // "host:port"
}

// LatencySample is one measurement of one target.
type LatencySample struct {
	Label string
	Addr  string
	RTT   time.Duration
	At    time.Time
	Err   error
}

// OK reports whether the sample carries a usable round-trip time.
func (s LatencySample) OK() bool { return s.Err == nil && s.RTT > 0 }

// latencySampleJSON is the on-the-wire shape. An error interface cannot be
// unmarshalled back, so a snapshot that carried one would fail to load
// entirely; the message is what matters anyway.
type latencySampleJSON struct {
	Label string    `json:"label"`
	Addr  string    `json:"addr,omitempty"`
	RTTNs int64     `json:"rtt_ns"`
	At    time.Time `json:"at"`
	Err   string    `json:"error,omitempty"`
}

func (s LatencySample) MarshalJSON() ([]byte, error) {
	out := latencySampleJSON{Label: s.Label, Addr: s.Addr, RTTNs: int64(s.RTT), At: s.At}
	if s.Err != nil {
		out.Err = s.Err.Error()
	}
	return json.Marshal(out)
}

func (s *LatencySample) UnmarshalJSON(b []byte) error {
	var in latencySampleJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	s.Label, s.Addr, s.RTT, s.At = in.Label, in.Addr, time.Duration(in.RTTNs), in.At
	s.Err = nil
	if in.Err != "" {
		s.Err = errors.New(in.Err)
	}
	return nil
}

// MeasureLatency times a TCP handshake against the target. A refused
// connection counts as a success: the RST travelled the same path a SYN-ACK
// would have, so the timing is just as valid and no service needs to be
// listening on the far end.
func MeasureLatency(t LatencyTarget, timeout time.Duration) LatencySample {
	s := LatencySample{Label: t.Label, Addr: t.Addr, At: time.Now()}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", t.Addr, timeout)
	elapsed := time.Since(start)

	if conn != nil {
		_ = conn.Close()
	}
	switch {
	case err == nil, errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET):
		s.RTT = elapsed
	default:
		s.Err = err
	}
	return s
}

// GatewayTarget builds a probe target for the host's default gateway. Port 53
// is a reasonable knock: consumer routers usually answer it, and a refused
// connection still times the round trip. ok is false when there is no default
// route to probe.
func GatewayTarget(routes []Route) (LatencyTarget, bool) {
	targets := GatewayTargets(routes)
	if len(targets) == 0 {
		return LatencyTarget{}, false
	}
	return targets[0], true
}

// GatewayTargets builds a probe target for each family's default gateway. A
// dual-stack host has two first hops and they are frequently not the same box,
// so one number for "the gateway" hides half the answer.
func GatewayTargets(routes []Route) []LatencyTarget {
	var out []LatencyTarget
	for _, f := range []struct {
		label string
		v6    bool
	}{{"gateway", false}, {"gateway6", true}} {
		def, ok := DefaultRouteFamily(routes, f.v6)
		if !ok {
			continue
		}
		addr := def.GatewayAddress()
		if addr == "" {
			continue // a directly-attached default route has no hop to probe
		}
		out = append(out, LatencyTarget{Label: f.label, Addr: net.JoinHostPort(addr, "53")})
	}
	return out
}
