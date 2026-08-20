package probe

import (
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
	def, ok := DefaultRoute(routes)
	if !ok || def.Gateway == "" {
		return LatencyTarget{}, false
	}
	return LatencyTarget{Label: "gateway", Addr: net.JoinHostPort(def.Gateway, "53")}, true
}
