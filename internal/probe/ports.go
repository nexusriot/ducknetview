package probe

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"

	gnet "github.com/shirou/gopsutil/v4/net"
)

type ListenPort struct {
	Proto   string
	Local   string // ip:port
	PID     int32
	Process string
	User    string
	Unit    string // systemd unit of the owning process, "" when unknown

	// NetNS names the network namespace the socket lives in (a container id,
	// an `ip netns` name or "netns:<inode>"); "" is ducknetview's own.
	NetNS string `json:",omitempty"`
	// Published is the host "ip:port" a namespaced listener is forwarded from
	// (docker-proxy), "" when nothing on the host reaches it.
	Published string `json:",omitempty"`

	// Accept-queue depth and its limit (the effective listen() backlog), TCP
	// and Linux only; Backlog is 0 when unknown. A queue near its limit is a
	// service that is not calling accept() fast enough: the kernel has
	// finished the handshakes and the application is not picking them up,
	// and once full, new connections are dropped before the application ever
	// sees them. Several SO_REUSEPORT sockets on one address share a row here
	// and report one of their queues.
	AcceptQueue uint32
	Backlog     uint32
}

// QueueRatio is how full the accept queue is, 0–1 (it can exceed 1 briefly,
// since the kernel admits one connection past the limit). ok is false when
// the limit is unknown.
func (p ListenPort) QueueRatio() (float64, bool) {
	if p.Backlog == 0 {
		return 0, false
	}
	return float64(p.AcceptQueue) / float64(p.Backlog), true
}

func connProto(c gnet.ConnectionStat) string {
	switch c.Type {
	case syscall.SOCK_STREAM:
		return "tcp"
	case syscall.SOCK_DGRAM:
		return "udp"
	default:
		return "net"
	}
}

// IsServiceListener reports whether a listening socket represents a service the
// host offers, rather than a client socket that merely has a local port. UDP has
// no LISTEN state, so an outbound DNS query is indistinguishable from a UDP
// service by socket state alone; the ephemeral port range is the only signal
// available, and treating those as services makes every count that includes them
// fluctuate with ordinary traffic.
func IsServiceListener(p ListenPort) bool {
	if p.Proto != "udp" {
		return true
	}
	_, port := SplitHostPort(p.Local)
	return !IsEphemeralPort(port)
}

var (
	ephemeralOnce sync.Once
	ephemeralLow  = 32768 // conservative default if the sysctl is unreadable
)

// IsEphemeralPort reports whether a port falls in the kernel's local port
// range — the pool client sockets are assigned from. It matters because UDP has
// no LISTEN state, so an outbound DNS query is indistinguishable from a UDP
// service by socket state alone; the port range is the only signal available.
func IsEphemeralPort(port int) bool {
	ephemeralOnce.Do(func() {
		b, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
		if err != nil {
			return
		}
		if fields := strings.Fields(string(b)); len(fields) >= 1 {
			if low, err := strconv.Atoi(fields[0]); err == nil && low > 0 {
				ephemeralLow = low
			}
		}
	})
	return port >= ephemeralLow
}

func isListening(c gnet.ConnectionStat) bool {
	// TCP: LISTEN
	if c.Type == syscall.SOCK_STREAM {
		return c.Status == "LISTEN"
	}

	// UDP doesn't have LISTEN in the same way; gopsutil often reports empty or "NONE".
	// We treat any UDP socket with a local port as "listening".
	if c.Type == syscall.SOCK_DGRAM {
		return c.Laddr.Port != 0
	}

	return false
}
