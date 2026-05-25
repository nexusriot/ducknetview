package probe

import (
	"fmt"
	"sort"
	"syscall"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
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

func ListConnections() ([]Conn, error) {
	conns, err := gnet.Connections("all")
	if err != nil {
		return nil, err
	}

	out := make([]Conn, 0, 128)
	nameCache := make(map[int32]string, 32)

	for _, c := range conns {
		if !isActive(c) {
			continue
		}

		state := c.Status
		if c.Type == syscall.SOCK_DGRAM && state == "" {
			state = "-"
		}

		cn := Conn{
			Proto:  connProto(c),
			Local:  fmt.Sprintf("%s:%d", c.Laddr.IP, c.Laddr.Port),
			Remote: fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port),
			State:  state,
			PID:    c.Pid,
		}

		if c.Pid > 0 {
			if n, ok := nameCache[c.Pid]; ok {
				cn.Process = n
			} else if p, e := process.NewProcess(c.Pid); e == nil {
				if n, e2 := p.Name(); e2 == nil {
					cn.Process = n
					nameCache[c.Pid] = n
				}
			}
		}

		out = append(out, cn)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].PID != out[j].PID {
			return out[i].PID < out[j].PID
		}
		if out[i].Proto != out[j].Proto {
			return out[i].Proto < out[j].Proto
		}
		if out[i].Remote != out[j].Remote {
			return out[i].Remote < out[j].Remote
		}
		return out[i].Local < out[j].Local
	})

	return out, nil
}
