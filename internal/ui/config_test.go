package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Every write the tool makes goes through this, including to places it may not
// be allowed to write.
func TestWriteFileAtomicFailures(t *testing.T) {
	dir := t.TempDir()

	// A directory that cannot be created.
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(blocker, "nested", "config.json"), []byte("y"), 0o644); err == nil {
		t.Error("writing underneath a regular file should fail")
	}

	// The permissions asked for are the permissions set, whatever the umask.
	p := filepath.Join(dir, "perms.json")
	if err := writeFileAtomic(p, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %v, want 0600", got)
	}
}

// A view is hand-editable in the config, so every index has to be clamped.
func TestSavedViewFromAHandEditedConfigIsClamped(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, `{"views":[
		{"name":"broken","tab":99,"conns_sort_col":999,"proto_filter":7},
		{"name":"","tab":0},
		{"name":"broken","tab":1}
	]}`)

	m := NewModel().WithConfig()
	if len(m.views) != 1 {
		t.Fatalf("the unnamed and duplicate views should have been dropped: %+v", m.views)
	}
	m2, ok := m.WithView("broken")
	if !ok {
		t.Fatal("the view should still be usable")
	}
	if int(m2.activeTab) > tabCount-1 || m2.connsSortCol >= len(connsSortCols) || m2.protoFilter > 2 {
		t.Errorf("an out-of-range view was applied unclamped: tab=%d sort=%d proto=%d",
			m2.activeTab, m2.connsSortCol, m2.protoFilter)
	}
}

// Config persistence round-trips through an isolated config dir.
func TestConfigRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := NewModel()
	m.hideNoise = true
	m.connsGroup = true
	m.refreshInterval = refreshSteps[2]
	m.procsSortCol = 2
	m.procsSortDesc = false
	m.saveConfig()

	got := NewModel().WithConfig()
	if !got.hideNoise || !got.connsGroup {
		t.Fatalf("toggles not persisted: hideNoise=%v group=%v", got.hideNoise, got.connsGroup)
	}
	if got.refreshInterval != refreshSteps[2] {
		t.Fatalf("refresh interval not persisted: %v", got.refreshInterval)
	}
	if got.procsSortCol != 2 || got.procsSortDesc {
		t.Fatalf("sort state not persisted: col=%d desc=%v", got.procsSortCol, got.procsSortDesc)
	}
}

// Toggling a persisted setting through the real key path writes the config
// immediately (save-on-change), so settings survive even a signal-kill quit.
func TestTogglePersistsImmediately(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := NewModel()
	m.activeTab = tabConns
	model, _ := m.Update(key("g")) // enable grouping
	m = model.(Model)
	if !m.connsGroup {
		t.Fatal("'g' should enable grouping")
	}

	c, state := loadConfig()
	if state != configLoaded || !c.ConnsGroup {
		t.Fatalf("group toggle should persist immediately: state=%v conns_group=%v", state, c.ConnsGroup)
	}
}

// A hand-edited/corrupt config must never wedge the UI: out-of-range values are
// clamped and bad JSON is ignored.
func TestConfigIgnoresGarbage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Out-of-range sort column + unknown refresh interval.
	writeConfig(t, `{"procs_sort_col": 999, "refresh_ms": 123}`)
	m := NewModel().WithConfig()
	if m.procsSortCol < 0 || m.procsSortCol >= len(procsSortCols) {
		t.Fatalf("procsSortCol not clamped: %d", m.procsSortCol)
	}
	if !isRefreshStep(m.refreshInterval) {
		t.Fatalf("bogus refresh interval accepted: %v", m.refreshInterval)
	}

	// Corrupt JSON -> defaults preserved.
	writeConfig(t, `{not json`)
	def := NewModel()
	m2 := NewModel().WithConfig()
	if m2.procsSortDesc != def.procsSortDesc || m2.refreshInterval != def.refreshInterval {
		t.Fatal("corrupt config should leave defaults intact")
	}
}

func TestConfigWatchlistRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := NewModel()
	m.watchlistRaw = []string{"45.0.0.0/8", "baddomain"}
	m.saveConfig()

	got := NewModel().WithConfig()
	if got.watch == nil {
		t.Fatal("watchlist not loaded from config")
	}
	if !got.watch.match("45.1.2.3", "") {
		t.Fatal("CIDR watchlist entry not applied after reload")
	}
	if !got.watch.match("1.2.3.4", "x.baddomain.net") {
		t.Fatal("regex watchlist entry not applied after reload")
	}
	if !eqStrs(got.watchlistRaw, []string{"45.0.0.0/8", "baddomain"}) {
		t.Fatalf("raw watchlist not preserved: %v", got.watchlistRaw)
	}
}

// A hand-edited config must not be able to wedge the UI.
func TestConfigClampsOutOfRangeFilters(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := os.MkdirAll(filepath.Join(dir, "ducknetview"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"proto_filter":99,"ipver_filter":-3,"conn_state_filter":42,"last_tab":77}`
	if err := os.WriteFile(filepath.Join(dir, "ducknetview", "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewModel().WithConfig()
	if m.protoFilter > 2 || m.ipVerFilter < 0 || m.connStateFilter > 2 {
		t.Errorf("filters not clamped: proto=%d ipver=%d state=%d", m.protoFilter, m.ipVerFilter, m.connStateFilter)
	}
	if int(m.activeTab) >= tabCount {
		t.Errorf("last tab %d is out of range", m.activeTab)
	}
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

// --- config wiring -------------------------------------------------------

// Every one of this round's features is reachable only if applyConfig actually
// wires it to something. A feature that loads but is never called is the
// failure mode that unit tests of the feature itself cannot see.
func TestConfigWiresEveryNewFeature(t *testing.T) {
	defer applyTheme(defaultTheme)

	dir := t.TempDir()
	asn := filepath.Join(dir, "ip2asn.tsv")
	if err := os.WriteFile(asn, []byte("8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Written through saveConfig so the round trip, not just the loader, is
	// what is being tested.
	seed := NewModel()
	seed.asnPath = asn
	seed.theme = "contrast"
	seed.procTree = true
	seed.alertRules.ifaceDropsPerMin = 90
	seed.keymap = newKeymap(map[string]string{"closed": "Z"})
	seed.saveConfig()

	m := NewModel().WithConfig()

	if m.asnPath != asn {
		t.Errorf("asn_database = %q, want %q", m.asnPath, asn)
	}
	if m.asnDB.Len() == 0 {
		t.Errorf("the ASN database was configured but never loaded (err: %v)", m.asnErr)
	}
	if got := m.ownerOf("8.8.8.8:443"); got != "GOOGLE" {
		t.Errorf("ownerOf = %q after a config round trip", got)
	}
	if m.theme != "contrast" {
		t.Errorf("theme = %q", m.theme)
	}
	if !m.procTree {
		t.Error("process_tree did not survive the round trip")
	}
	if m.alertRules.ifaceDropsPerMin != 90 {
		t.Errorf("alert_iface_drops_per_min = %v", m.alertRules.ifaceDropsPerMin)
	}
	if got := m.keymap.keyFor("closed"); got != "Z" {
		t.Errorf("keys did not survive the round trip: closed is bound to %q", got)
	}
	// And the binding reaches the handler, not just the struct.
	m.w, m.h = 160, 45
	m.activeTab = tabConns
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Z")})
	if !out.(Model).showClosed {
		t.Error("the persisted key binding does not reach the key handler")
	}
}

// A config naming a database that is not there must still start, with the
// column simply absent.
func TestConfigWithBrokenASNPathStillStarts(t *testing.T) {
	seed := NewModel()
	seed.asnPath = filepath.Join(t.TempDir(), "gone.tsv")
	seed.saveConfig()

	m := NewModel().WithConfig()
	if m.asnErr == nil {
		t.Error("a missing database was not reported")
	}
	m.w, m.h = 160, 45
	m.connsVP.Width = 140
	for _, c := range m.connsColumns() {
		if c.key == "owner" {
			t.Error("the OWNER column appears although the database failed to load")
		}
	}
}
