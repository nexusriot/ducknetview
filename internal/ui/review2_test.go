package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// runeIndex is strings.Index in characters rather than bytes, so a comparison
// against a column position is not thrown off by the cursor gutter's arrow.
func runeIndex(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return len([]rune(s[:i]))
}

// A config file that cannot be parsed must survive the session that could not
// read it. Overwriting it on quit replaces every hand-written setting —
// watchlist, thresholds, key bindings — with this session's defaults.
func TestCorruptConfigIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	const corrupt = `{"watchlist": ["10.0.0.0/8"], "alert_rtt_ms": 250,` // truncated mid-object
	writeConfig(t, corrupt)

	m := NewModel().WithConfig()
	if !m.configUnsafe {
		t.Fatal("an unparseable config should have been noticed")
	}

	m.saveConfig()
	m.quit()

	p, _ := configPath()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(b) != corrupt {
		t.Errorf("the corrupt config was overwritten:\n%s", b)
	}
}

// The ordinary case still writes.
func TestValidConfigStillSaves(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, `{"hide_noise": false}`)

	m := NewModel().WithConfig()
	if m.configUnsafe {
		t.Fatal("a valid config must not be treated as unreadable")
	}
	m.hideNoise = true
	m.saveConfig()

	c, state := loadConfig()
	if state != configLoaded || !c.HideNoise {
		t.Fatalf("save did not take: state=%v hide_noise=%v", state, c.HideNoise)
	}
}

// The config is rewritten on nearly every keypress, so the write has to be
// atomic: a partial file is what makes the case above possible in the first
// place.
func TestWriteFileAtomicReplacesAndLeavesNoLitter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "config.json")

	if err := writeFileAtomic(p, []byte("one"), 0o644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeFileAtomic(p, []byte("two"), 0o644); err != nil {
		t.Fatalf("second write: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "two" {
		t.Fatalf("content = %q, err = %v", b, err)
	}

	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("temporary files left behind: %v", names)
	}
}

// Two actions bound to the same key are resolved first-wins. Walking a Go map
// to decide which is first hands the same config file a different keyboard on
// every start.
func TestKeymapIsDeterministic(t *testing.T) {
	overrides := map[string]string{"copy": "m", "export": "m", "watch": "m"}
	want := newKeymap(overrides).keyFor("copy")
	for i := 0; i < 200; i++ {
		if got := newKeymap(overrides).keyFor("copy"); got != want {
			t.Fatalf("binding changed between runs: %q then %q", want, got)
		}
	}
}

// Hiding a column drops it from the header, so the rows have to drop it too.
func TestHiddenColumnKeepsProcessRowsAligned(t *testing.T) {
	m := NewModel()
	m.w, m.h = 160, 48
	m.procsVP.Width = 120
	m.procs = []probe.ProcNet{
		{PID: 42, Name: "curl", User: "vlad", ConnCount: 7, CPU: 1.5, Mem: 0.25},
	}
	m.hiddenCols = map[string]bool{"cpu": true}

	lines := strings.Split(stripANSI(m.renderProcsText()), "\n")
	if len(lines) < 6 {
		t.Fatalf("unexpected table shape: %q", lines)
	}
	header, row := lines[3], lines[5]
	if strings.Contains(header, "CPU%") {
		t.Fatalf("hidden column still in the header: %q", header)
	}
	for _, col := range []struct{ title, cell string }{
		{"USER", "vlad"}, {"MEM%", "0.2"}, {"CONNS", "7"},
	} {
		if h, r := runeIndex(header, col.title), runeIndex(row, col.cell); h != r {
			t.Errorf("%s: header at %d, cell at %d\nhdr %q\nrow %q", col.title, h, r, header, row)
		}
	}
}

// A live reload runs on the update loop, so the check it reports from must not
// resolve hostnames or load the ASN table. It still has to catch everything
// that costs nothing to see.
func TestCheckConfigFastSkipsTheExpensiveChecks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := map[string]any{
		"asn_database": filepath.Join(dir, "does-not-exist.tsv"),
		"theme":        "not-a-theme",
	}
	b, _ := json.Marshal(cfg)
	writeConfig(t, string(b))

	full := CheckConfig()
	fast := CheckConfigFast()

	hasKey := func(r ConfigReport, key string) bool {
		for _, p := range r.Problems {
			if p.Key == key {
				return true
			}
		}
		return false
	}
	if !hasKey(full, "asn_database") {
		t.Error("the full check should report an unloadable ASN database")
	}
	if hasKey(fast, "asn_database") {
		t.Error("the fast check must not load the ASN table")
	}
	if !hasKey(fast, "theme") {
		t.Error("the fast check should still report a cheap problem")
	}
}

// Re-applying the config must not re-read an ASN table that has not changed:
// it is the one setting that costs seconds of a blocked update loop.
func TestASNDatabaseIsNotReloadedWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asn.tsv")
	if err := os.WriteFile(path, []byte("8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewModel()
	m.asnPath = path
	m.loadASNDB()
	first := m.asnDB
	if first == nil {
		t.Fatal("the table should have loaded")
	}

	// Remove the file: a reload that re-reads would now fail and drop the
	// column, which is exactly what must not happen.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	m.loadASNDB()
	if m.asnDB != first {
		t.Error("an unchanged path re-read the table")
	}
	if m.asnErr != nil {
		t.Errorf("unexpected error: %v", m.asnErr)
	}
}
