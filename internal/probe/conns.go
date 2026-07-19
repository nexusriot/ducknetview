package probe

import (
	"syscall"

	gnet "github.com/shirou/gopsutil/v4/net"
)

type Conn struct {
	Proto   string
	Local   string // ip:port
	Remote  string // ip:port
	State   string
	PID     int32
	Process string
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
