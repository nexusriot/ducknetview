package probe

import (
	"syscall"

	gnet "github.com/shirou/gopsutil/v4/net"
)

type ListenPort struct {
	Proto   string
	Local   string // ip:port
	PID     int32
	Process string
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
