package probe

import (
	"strings"
	"testing"
)

func TestServiceName(t *testing.T) {
	// Invalid ports resolve to "" regardless of /etc/services contents.
	if got := ServiceName(0, "tcp"); got != "" {
		t.Fatalf("port 0 should be unknown, got %q", got)
	}
	if got := ServiceName(-1, "tcp"); got != "" {
		t.Fatalf("negative port should be unknown, got %q", got)
	}

	// Well-known ports must resolve (via /etc/services or the built-in fallback)
	// and never contain a slash (proto is stripped from the key).
	for _, port := range []int{22, 80, 443} {
		got := ServiceName(port, "tcp")
		if got == "" {
			t.Fatalf("port %d should resolve to a service name", port)
		}
		if strings.ContainsAny(got, "/ \t") {
			t.Fatalf("service name for %d looks malformed: %q", port, got)
		}
	}

	// A high, effectively-unassigned port should be unknown.
	if got := ServiceName(64999, "tcp"); got != "" {
		t.Logf("note: port 64999 resolved to %q (present in /etc/services)", got)
	}
}
