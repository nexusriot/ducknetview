package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// A baseline is the set of listening sockets a host is expected to have.
// Saving one turns the Ports tab into a change detector that survives
// restarts — the per-poll "new listener" highlight only catches a listener
// that appears while you happen to be watching.

// baselineEligible reports whether a listener is stable enough to be worth
// baselining. UDP has no LISTEN state, so every outbound DNS query looks like a
// new "listener" on a random high port; baselining those would mean a
// permanently dirty baseline. They stay visible in the Ports table — they are
// just not treated as part of the host's expected service surface.
func baselineEligible(p probe.ListenPort) bool {
	if p.Proto != "udp" {
		return true
	}
	_, port := probe.SplitHostPort(p.Local)
	return !probe.IsEphemeralPort(port)
}

// setBaseline records the current listener set as the expected one and
// persists it.
func (m *Model) setBaseline() {
	keys := make([]string, 0, len(m.ports))
	set := make(map[string]bool, len(m.ports))
	for _, p := range m.ports {
		if !baselineEligible(p) {
			continue
		}
		k := portKey(p)
		if set[k] {
			continue
		}
		set[k] = true
		keys = append(keys, k)
	}
	// Sorted so the config file stays diffable between saves.
	sort.Strings(keys)

	m.baseline = set
	m.baselineRaw = keys
	m.baselineAt = time.Now()
	m.saveConfig()
}

// clearBaseline forgets the saved listener set.
func (m *Model) clearBaseline() {
	m.baseline = nil
	m.baselineRaw = nil
	m.baselineAt = time.Time{}
	m.saveConfig()
}

// hasBaseline reports whether a listener set has been saved.
func (m Model) hasBaseline() bool { return len(m.baseline) > 0 }

// offBaseline reports whether a listener is absent from the saved baseline.
// Always false when there is no baseline, so the alert styling stays off until
// the user opts in.
func (m Model) offBaseline(p probe.ListenPort) bool {
	if !m.hasBaseline() || !baselineEligible(p) {
		return false
	}
	return !m.baseline[portKey(p)]
}

// offBaselineCount counts listeners not in the baseline, across all listeners
// rather than the filtered view.
func (m Model) offBaselineCount() int {
	if !m.hasBaseline() {
		return 0
	}
	n := 0
	for _, p := range m.ports {
		if m.offBaseline(p) {
			n++
		}
	}
	return n
}

// BaselineCheck compares a freshly collected listener set against the saved
// baseline, for the non-interactive `--check-baseline` mode. ok is false when
// no baseline has been saved, which callers report as a usage error rather
// than as "nothing unexpected".
func BaselineCheck(ports []probe.ListenPort) (offending []probe.ListenPort, savedAt time.Time, ok bool) {
	c, loaded := loadConfig()
	if !loaded || len(c.Baseline) == 0 {
		return nil, time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, c.BaselineAt); err == nil {
		savedAt = t
	}
	return compareBaseline(c.Baseline, ports), savedAt, true
}

// compareBaseline returns the listeners not present in a baseline key set.
func compareBaseline(baseline []string, ports []probe.ListenPort) []probe.ListenPort {
	set := make(map[string]bool, len(baseline))
	for _, k := range baseline {
		set[k] = true
	}
	var offending []probe.ListenPort
	for _, p := range ports {
		if baselineEligible(p) && !set[portKey(p)] {
			offending = append(offending, p)
		}
	}
	return offending
}

// baselineFile is the on-disk format for a baseline that is meant to travel:
// built once on a known-good host, then shipped to the fleet and checked with
// `--baseline FILE --check-baseline`. The per-user config keeps its own copy
// for interactive use.
type baselineFile struct {
	SavedAt   string   `json:"saved_at"`
	Host      string   `json:"host,omitempty"`
	Listeners []string `json:"listeners"`
}

// SaveBaselineFile writes the given listeners as a portable baseline and
// returns how many were recorded.
func SaveBaselineFile(path, host string, ports []probe.ListenPort) (int, error) {
	keys := make([]string, 0, len(ports))
	seen := map[string]bool{}
	for _, p := range ports {
		if !baselineEligible(p) {
			continue
		}
		if k := portKey(p); !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	b, err := json.MarshalIndent(baselineFile{
		SavedAt:   time.Now().Format(time.RFC3339),
		Host:      host,
		Listeners: keys,
	}, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return 0, err
	}
	return len(keys), nil
}

// BaselineCheckFile compares listeners against a baseline file rather than the
// user's config.
func BaselineCheckFile(path string, ports []probe.ListenPort) (offending []probe.ListenPort, savedAt time.Time, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	var bf baselineFile
	if err := json.Unmarshal(b, &bf); err != nil {
		return nil, time.Time{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(bf.Listeners) == 0 {
		return nil, time.Time{}, fmt.Errorf("%s: baseline contains no listeners", path)
	}
	if t, perr := time.Parse(time.RFC3339, bf.SavedAt); perr == nil {
		savedAt = t
	}
	return compareBaseline(bf.Listeners, ports), savedAt, nil
}
