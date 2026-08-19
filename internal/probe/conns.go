package probe

import (
	"syscall"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
)

type Conn struct {
	Proto   string
	Local   string // ip:port
	Remote  string // ip:port
	State   string
	PID     int32
	Process string
	User    string

	// Cumulative kernel counters and health for this socket, from SockStats.
	// TCP-only and Linux-only; zero when unavailable.
	TxBytes uint64
	RxBytes uint64
	RTT     time.Duration
	MinRTT  time.Duration
	Retrans uint32
	RecvQ   uint32
	SendQ   uint32
}

// SocketHealthy reports whether a connection shows no sign of loss or
// backpressure. Retransmits mean the path is dropping packets; a standing send
// queue means the peer is not draining what we hand it.
func (c Conn) SocketHealthy() bool {
	return c.Retrans == 0 && c.SendQ == 0
}

func isActive(c gnet.ConnectionStat) bool {
	// Skip pure listening sockets and zero-remote rows.
	if c.Type == syscall.SOCK_STREAM && c.Status == "LISTEN" {
		return false
	}
	if c.Raddr.IP == "" && c.Raddr.Port == 0 {
		return false
	}
	return true
}
