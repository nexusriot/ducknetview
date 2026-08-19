package probe

import "testing"

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
	}{
		{"10.0.0.1:443", "10.0.0.1", 443},
		{"::1:8080", "::1", 8080},
		{"0.0.0.0:0", "0.0.0.0", 0},
		{"nonsense", "nonsense", 0},
		{"10.0.0.1:notaport", "10.0.0.1", 0},
	}
	for _, c := range cases {
		host, port := SplitHostPort(c.in)
		if host != c.host || port != c.port {
			t.Errorf("SplitHostPort(%q) = (%q, %d), want (%q, %d)", c.in, host, port, c.host, c.port)
		}
	}
}

// The netlink and /proc views of the same socket can spell an address
// differently (IPv4-mapped IPv6, uncompressed zero groups), so keys are built
// from the parsed address rather than the raw string.
func TestSockKeyNormalizesAddresses(t *testing.T) {
	if got, want := SockKey("::ffff:10.0.0.1:80", "8.8.8.8:53"), "10.0.0.1:80|8.8.8.8:53"; got != want {
		t.Errorf("v4-mapped: got %q, want %q", got, want)
	}
	if SockKey("2001:0db8:0000:0000:0000:0000:0000:0001:443", "::1:22") !=
		SockKey("2001:db8::1:443", "::1:22") {
		t.Error("equivalent IPv6 spellings produced different keys")
	}
	// An address that doesn't parse is passed through rather than dropped, so a
	// malformed row can never collide with a real socket's key.
	if got, want := SockKey("garbage", "also:garbage"), "garbage:0|also:0"; got != want {
		t.Errorf("unparseable passthrough: got %q, want %q", got, want)
	}
}

func TestIsEphemeralPort(t *testing.T) {
	// Whatever the host's configured range, well-known service ports must never
	// be treated as ephemeral and very high ports always must be.
	for _, port := range []int{22, 53, 80, 443, 8080} {
		if IsEphemeralPort(port) {
			t.Errorf("port %d classified as ephemeral", port)
		}
	}
	if !IsEphemeralPort(61000) {
		t.Error("port 61000 should be ephemeral")
	}
}
