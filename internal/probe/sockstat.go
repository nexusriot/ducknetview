package probe

import (
	"net"
	"strconv"
	"strings"
	"time"
)

// SockStat holds the kernel's per-socket byte counters and smoothed round-trip
// time for one TCP connection. Counters are cumulative for the life of the
// socket; callers derive rates by diffing consecutive samples.
type SockStat struct {
	TxBytes uint64 // bytes acknowledged by the peer (tcpi_bytes_acked)
	RxBytes uint64 // bytes received from the peer (tcpi_bytes_received)
	RTT     time.Duration
	MinRTT  time.Duration // best RTT seen on this socket (tcpi_min_rtt)

	// Retrans is the socket's lifetime retransmit count (tcpi_total_retrans).
	// Together with the queues it separates "the network is bad" from "the
	// peer is slow": retransmits mean loss, a stuck send queue means the peer
	// (or the path) is not accepting data as fast as we produce it.
	Retrans uint32
	RecvQ   uint32
	SendQ   uint32
}

// SplitHostPort splits an "ip:port" string as produced by gopsutil, where IPv6
// addresses are unbracketed (e.g. "::1:443"). The port is 0 when absent or
// unparseable.
func SplitHostPort(hp string) (string, int) {
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		return hp, 0
	}
	port, err := strconv.Atoi(hp[i+1:])
	if err != nil {
		return hp[:i], 0
	}
	return hp[:i], port
}

// normalizeHostPort canonicalizes an "ip:port" pair so that the same socket
// keys identically whether it came from gopsutil's /proc parsing or from
// netlink. IPv4-mapped IPv6 addresses collapse to their IPv4 form; addresses
// that don't parse are passed through unchanged.
func normalizeHostPort(hp string) string {
	host, port := SplitHostPort(hp)
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			host = v4.String()
		} else {
			host = ip.String()
		}
	}
	return host + ":" + strconv.Itoa(port)
}

// SockKey is the lookup key for a connection's kernel counters, built from its
// local and remote "ip:port" endpoints.
func SockKey(local, remote string) string {
	return normalizeHostPort(local) + "|" + normalizeHostPort(remote)
}
