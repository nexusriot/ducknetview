package probe

import (
	"syscall"
	"testing"

	gnet "github.com/shirou/gopsutil/v4/net"
)

// gopsutil renders an unconnected socket's remote as "0.0.0.0:0" or "[::]:0",
// never as an empty address, so a bound UDP socket must not count as a
// connection.
func TestIsActiveSkipsUnconnectedSockets(t *testing.T) {
	cases := []struct {
		name string
		c    gnet.ConnectionStat
		want bool
	}{
		{"udp v4 unconnected", gnet.ConnectionStat{Type: syscall.SOCK_DGRAM, Laddr: gnet.Addr{IP: "0.0.0.0", Port: 5353}, Raddr: gnet.Addr{IP: "0.0.0.0"}}, false},
		{"udp v6 unconnected", gnet.ConnectionStat{Type: syscall.SOCK_DGRAM, Laddr: gnet.Addr{IP: "::", Port: 5353}, Raddr: gnet.Addr{IP: "::"}}, false},
		{"udp connected", gnet.ConnectionStat{Type: syscall.SOCK_DGRAM, Laddr: gnet.Addr{IP: "10.0.0.5", Port: 50000}, Raddr: gnet.Addr{IP: "1.1.1.1", Port: 443}}, true},
		{"tcp listen", gnet.ConnectionStat{Type: syscall.SOCK_STREAM, Status: "LISTEN", Laddr: gnet.Addr{IP: "0.0.0.0", Port: 22}, Raddr: gnet.Addr{IP: "0.0.0.0"}}, false},
		{"tcp established", gnet.ConnectionStat{Type: syscall.SOCK_STREAM, Status: "ESTABLISHED", Laddr: gnet.Addr{IP: "10.0.0.5", Port: 41000}, Raddr: gnet.Addr{IP: "1.1.1.1", Port: 443}}, true},
	}
	for _, tc := range cases {
		if got := isActive(tc.c); got != tc.want {
			t.Errorf("%s: isActive = %v, want %v", tc.name, got, tc.want)
		}
	}
}
