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
