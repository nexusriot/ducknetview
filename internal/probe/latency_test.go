package probe

import (
	"net"
	"testing"
	"time"
)

func TestMeasureLatencyAgainstListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	s := MeasureLatency(LatencyTarget{Label: "local", Addr: ln.Addr().String()}, time.Second)
	if !s.OK() {
		t.Fatalf("sample = %+v, want a usable measurement", s)
	}
	if s.RTT <= 0 || s.RTT > time.Second {
		t.Errorf("loopback RTT = %s, want something small and positive", s.RTT)
	}
	if s.Label != "local" {
		t.Errorf("label = %q, want it carried through", s.Label)
	}
}

// A refused connection is a valid measurement: the RST came back over the same
// path a SYN-ACK would have, so no service needs to be listening.
func TestMeasureLatencyRefusedIsStillATiming(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing is listening there now

	s := MeasureLatency(LatencyTarget{Label: "closed", Addr: addr}, time.Second)
	if !s.OK() {
		t.Errorf("refused connection = %+v, want it counted as a timing", s)
	}
}

func TestMeasureLatencyUnreachable(t *testing.T) {
	// 203.0.113.0/24 is reserved for documentation and never routed.
	s := MeasureLatency(LatencyTarget{Label: "void", Addr: "203.0.113.1:9"}, 150*time.Millisecond)
	if s.OK() {
		t.Errorf("unreachable target reported a timing: %+v", s)
	}
	if s.Err == nil {
		t.Error("unreachable target should carry the error")
	}
}

func TestGatewayTarget(t *testing.T) {
	routes := []Route{{Destination: "default", Gateway: "192.168.1.1", Iface: "eth0", Metric: 100}}
	target, ok := GatewayTarget(routes)
	if !ok {
		t.Fatal("expected a gateway target")
	}
	if target.Addr != "192.168.1.1:53" || target.Label != "gateway" {
		t.Errorf("target = %+v, want the gateway on port 53", target)
	}

	// A directly-attached default route has no gateway to probe.
	if _, ok := GatewayTarget([]Route{{Destination: "default", Iface: "ppp0"}}); ok {
		t.Error("a gatewayless default route should not produce a target")
	}
	if _, ok := GatewayTarget(nil); ok {
		t.Error("no routes should not produce a target")
	}
}
