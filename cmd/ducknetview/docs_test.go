package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
	"github.com/nexusriot/ducknetview/internal/ui"
	"github.com/nexusriot/ducknetview/internal/version"
)

// The man page, the completions and the README are the only description of the
// command-line surface a user ever reads, and nothing in the build notices when
// a flag is added without them. These tests are that notice.

const (
	manPath      = "../../man/ducknetview.1"
	bashCompPath = "../../completions/ducknetview.bash"
	zshCompPath  = "../../completions/_ducknetview"
	readmePath   = "../../README.md"
)

var flagDefRe = regexp.MustCompile(`flag\.(?:Bool|String|Int|Int64|Float64|Duration)\("([a-z0-9-]+)"`)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// definedFlags lists the flags main.go registers. It reads the source rather
// than the flag package's global set, because flag.Parse has already run by the
// time a test could inspect it and the registrations live inside main().
func definedFlags(t *testing.T) []string {
	t.Helper()
	src := readFile(t, "main.go")
	matches := flagDefRe.FindAllStringSubmatch(src, -1)
	if len(matches) < 10 {
		t.Fatalf("found only %d flag definitions in main.go — the pattern has stopped matching", len(matches))
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// mentionsFlag reports whether text documents exactly this flag. A plain
// substring search is not enough: "--metrics" occurs inside "--metrics-listen",
// so documenting only the longer one would look like documenting both.
func mentionsFlag(text, name string) bool {
	for _, m := range regexp.MustCompile(`--[a-z0-9-]+`).FindAllString(text, -1) {
		if m == "--"+name {
			return true
		}
	}
	return false
}

func TestFlagsDocumentedInManPage(t *testing.T) {
	// Roff escapes every hyphen as \-, so the escapes come out before the
	// search rather than being written into each expected string.
	man := strings.ReplaceAll(readFile(t, manPath), `\-`, "-")
	for _, name := range definedFlags(t) {
		if !mentionsFlag(man, name) {
			t.Errorf("flag --%s is not documented in %s", name, manPath)
		}
	}
}

func TestFlagsInCompletions(t *testing.T) {
	bash := readFile(t, bashCompPath)
	zsh := readFile(t, zshCompPath)
	for _, name := range definedFlags(t) {
		if !mentionsFlag(bash, name) {
			t.Errorf("flag --%s is missing from %s", name, bashCompPath)
		}
		// zsh spells each one '--name[description]', so the bracket is what
		// distinguishes a real entry from a mention inside another.
		if !strings.Contains(zsh, "'--"+name+"[") {
			t.Errorf("flag --%s is missing from %s", name, zshCompPath)
		}
	}
}

// TestTabNamesInCompletions keeps both completions offering every accepted
// --tab value, including the aliases: a completion that knows fewer values than
// the flag teaches users the flag is narrower than it is.
func TestTabNamesInCompletions(t *testing.T) {
	bash := readFile(t, bashCompPath)
	zsh := readFile(t, zshCompPath)
	for _, name := range ui.TabNames() {
		if !strings.Contains(bash, name) {
			t.Errorf("tab %q is missing from %s", name, bashCompPath)
		}
		if !strings.Contains(zsh, name) {
			t.Errorf("tab %q is missing from %s", name, zshCompPath)
		}
	}
}

func TestVersionIsConsistent(t *testing.T) {
	v := version.Version

	readme := readFile(t, readmePath)
	if want := "Current version: **" + v + "**"; !strings.Contains(readme, want) {
		t.Errorf("%s does not state %q", readmePath, want)
	}

	man := readFile(t, manPath)
	if want := `"ducknetview ` + v + `"`; !strings.Contains(man, want) {
		t.Errorf("%s header does not state %q", manPath, want)
	}

	changelog := readFile(t, "../../CHANGELOG.md")
	if want := "## " + v + " "; !strings.Contains(changelog, want) {
		t.Errorf("CHANGELOG.md has no entry for %s", v)
	}
}

// The metrics and the event kinds are an interface other software is written
// against: a Prometheus rule matches a series name, a hook matches a kind.
// Both are documented in README tables, and both are easy to add to without
// remembering the table exists.

// renderedMetricNames returns the series a scrape actually contains.
func renderedMetricNames(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^# TYPE (ducknetview_[a-z_]+)`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestEveryMetricIsDocumented(t *testing.T) {
	readme := readFile(t, readmePath)

	snap := probe.NetSnapshot{
		Hostname: "docs", TakenAt: time.Now(),
		Ifaces: []probe.IfaceInfo{{Name: "eth0", IsUp: true}},
	}
	info := probe.ConnInfo{
		Ports: []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}},
		Procs: []probe.ProcNet{{PID: 1, Name: "sshd"}},
		Conns: []probe.Conn{{Proto: "tcp", Local: "10.0.0.2:1", Remote: "8.8.8.8:443"}},
	}
	// RemoteHost set so the one conditional series is rendered too.
	stats := ui.SessionStats{Snap: snap, Info: info, RemoteHost: "faraway", EventCounts: map[string]int{"listener_up": 1}}

	names := append(
		renderedMetricNames(ui.FormatMetrics(snap, info, 0, false)),
		renderedMetricNames(ui.FormatLiveMetrics(stats, version.Version))...)
	if len(names) < 15 {
		t.Fatalf("only %d metrics rendered — the extraction has stopped working", len(names))
	}

	for _, name := range names {
		if !strings.Contains(readme, "`"+name+"`") {
			t.Errorf("metric %s is exported but not in the README's metrics table", name)
		}
	}
}

// eventKindsInSource reads the kinds the ui package can record. They are string
// literals at the call sites rather than a declared set, so the source is where
// the list lives.
func eventKindsInSource(t *testing.T) []string {
	t.Helper()

	files, err := filepath.Glob("../../internal/ui/*.go")
	if err != nil {
		t.Fatal(err)
	}
	add := regexp.MustCompile(`rec\.add\([a-zA-Z]+,\s*"([a-z_]+)"`)
	direct := regexp.MustCompile(`Kind:\s*"([a-z_]+)"`)

	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range []*regexp.Regexp{add, direct} {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				seen[m[1]] = true
			}
		}
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestEveryEventKindIsDocumented(t *testing.T) {
	readme := readFile(t, readmePath)

	kinds := eventKindsInSource(t)
	if len(kinds) < 10 {
		t.Fatalf("only %d event kinds found — the extraction has stopped working", len(kinds))
	}
	for _, kind := range kinds {
		if !strings.Contains(readme, "`"+kind+"`") {
			t.Errorf("event kind %q can be recorded but is not in the README's event-kind table", kind)
		}
	}
}

// A count written into prose is a drift source of its own: this one was wrong
// within a day of being written.
func TestActionCountInREADME(t *testing.T) {
	readme := readFile(t, readmePath)
	m := regexp.MustCompile(`rebinds any of the (\d+) actions`).FindStringSubmatch(readme)
	if m == nil {
		t.Fatal("the README no longer states how many actions are rebindable")
	}
	if want := strconv.Itoa(len(ui.Actions())); m[1] != want {
		t.Errorf("README says %s rebindable actions, there are %s", m[1], want)
	}
}
