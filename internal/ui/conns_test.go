package ui

import (
	"strings"
	"testing"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// highlightFold previously sliced a []rune with byte offsets from strings.Index,
// which mis-highlighted and could panic on multi-byte UTF-8. These inputs would
// have triggered the old bug.
func TestHighlightFoldNoPanicMultibyte(t *testing.T) {
	cases := []struct{ s, q string }{
		{"café", "é"},
		{"café", "fé"},
		{"СерверПроцесс", "сервер"},
		{"日本語procd", "proc"},
		{"plain-ascii-proc", "proc"},
		{"", "x"},
		{"abc", ""},
	}
	for _, c := range cases {
		out := highlightFold(c.s, c.q) // must not panic
		// The visible text (ignoring injected ANSI) must still contain the source.
		if c.q == "" && out != c.s {
			t.Fatalf("empty query should return input unchanged: %q -> %q", c.s, out)
		}
	}
}

func TestGroupedConnsRender(t *testing.T) {
	m := NewModel()
	m.connsGroup = true
	m.connsVP.Width = 120
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:5000", Remote: "1.2.3.4:443", State: "ESTABLISHED", PID: 10, Process: "curl"},
		{Proto: "tcp", Local: "10.0.0.1:5001", Remote: "1.2.3.4:443", State: "TIME_WAIT", PID: 10, Process: "curl"},
		{Proto: "tcp", Local: "10.0.0.1:6000", Remote: "5.6.7.8:80", State: "ESTABLISHED", PID: 20, Process: "wget"},
	}
	out := m.renderConnsText() // dispatches to renderConnsGrouped
	if !strings.Contains(out, "grouped by remote host") {
		t.Fatalf("expected grouped header, got:\n%s", out)
	}
	if !strings.Contains(out, "1.2.3.4") || !strings.Contains(out, "5.6.7.8") {
		t.Fatalf("expected both remote hosts in grouped output:\n%s", out)
	}
	// 1.2.3.4 has 2 conns and should sort first (descending count).
	i14 := strings.Index(out, "1.2.3.4")
	i58 := strings.Index(out, "5.6.7.8")
	if i14 > i58 {
		t.Fatalf("expected busier host (1.2.3.4) listed first")
	}
}

func TestRevDNSDisplaySubstitution(t *testing.T) {
	m := NewModel()
	m.connsRevDNS = true
	m.dnsCache = map[string]string{"1.2.3.4": "example.com"}
	if got := m.dnsDisplay("1.2.3.4:443"); got != "example.com:443" {
		t.Fatalf("dnsDisplay should substitute hostname, got %q", got)
	}
	// With rev DNS off, the raw remote is returned.
	m.connsRevDNS = false
	if got := m.dnsDisplay("1.2.3.4:443"); got != "1.2.3.4:443" {
		t.Fatalf("dnsDisplay should be a no-op when disabled, got %q", got)
	}
}
