package probe

import (
	"fmt"
	"sort"
	"syscall"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
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

func ListListening() ([]ListenPort, error) {
	conns, err := gnet.Connections("all")
	if err != nil {
		return nil, err
	}

	out := make([]ListenPort, 0, 128)

	for _, c := range conns {
		if !isListening(c) {
			continue
		}

		lp := ListenPort{
			Proto: connProto(c),
			Local: fmt.Sprintf("%s:%d", c.Laddr.IP, c.Laddr.Port),
			PID:   c.Pid,
		}

		// Best-effort process name (may require privileges depending on OS)
		if c.Pid > 0 {
			if p, e := process.NewProcess(c.Pid); e == nil {
				if n, e2 := p.Name(); e2 == nil {
					lp.Process = n
				}
			}
		}

		out = append(out, lp)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Proto != out[j].Proto {
			return out[i].Proto < out[j].Proto
		}
		if out[i].Local != out[j].Local {
			return out[i].Local < out[j].Local
		}
		return out[i].PID < out[j].PID
	})

	return out, nil
}
