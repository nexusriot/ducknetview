//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/nexusriot/ducknetview/internal/probe"
	"github.com/nexusriot/ducknetview/internal/ui"
)

func ifaceNames(ifaces []probe.IfaceInfo) []string {
	out := make([]string, 0, len(ifaces))
	for _, ii := range ifaces {
		out = append(out, ii.Name)
	}
	return out
}

// findListener looks for a listening socket by protocol and port, whatever
// address it is bound to.
func findListener(s ui.Snapshot, proto string, port int) *probe.ListenPort {
	for i := range s.Ports {
		p := s.Ports[i]
		if p.Proto != proto {
			continue
		}
		if _, got := probe.SplitHostPort(p.Local); got == port {
			return &s.Ports[i]
		}
	}
	return nil
}

func hostOf(hostport string) string {
	h, _ := probe.SplitHostPort(hostport)
	return h
}

// listen opens a listener that stays open until the test ends.
func listen(t *testing.T, addr string) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func portOf(l net.Listener) int { return l.Addr().(*net.TCPAddr).Port }

func netDial(port int) (net.Conn, error) {
	return net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertValidExposition checks the shape of a Prometheus scrape: every sample
// belongs to a declared series, and every value parses as a number. A scrape
// that a parser rejects is worse than no scrape at all, and nothing else in
// the suite would notice.
func assertValidExposition(t *testing.T, body string) {
	t.Helper()

	declared := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, "# TYPE "):
			fields := strings.Fields(line)
			if len(fields) != 4 {
				t.Errorf("malformed TYPE line: %q", line)
				continue
			}
			declared[fields[2]] = true
		case strings.HasPrefix(line, "# HELP "):
		case strings.HasPrefix(line, "#"):
			t.Errorf("unrecognised comment line: %q", line)
		default:
			name, value, ok := strings.Cut(line, " ")
			if !ok {
				t.Errorf("sample with no value: %q", line)
				continue
			}
			if brace := strings.IndexByte(name, '{'); brace >= 0 {
				if !strings.HasSuffix(name, "}") {
					t.Errorf("unterminated label set: %q", line)
				}
				name = name[:brace]
			}
			if !declared[name] {
				t.Errorf("sample for undeclared series %q: %q", name, line)
			}
			if _, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err != nil {
				t.Errorf("value is not a number: %q", line)
			}
		}
	}
	if len(declared) < 8 {
		t.Errorf("only %d series declared, the scrape looks truncated", len(declared))
	}
}

// metricValue reads one sample's value out of a scrape.
func metricValue(t *testing.T, body, sample string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, sample+" ") {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, sample)), 64)
			if err != nil {
				t.Fatalf("%q: %v", line, err)
			}
			return v
		}
	}
	t.Fatalf("no sample %q in the scrape:\n%s", sample, body)
	return 0
}
