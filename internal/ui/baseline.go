package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// A baseline is the set of listening sockets a host is expected to have.
// Saving one turns the Ports tab into a change detector that survives
// restarts — the per-poll "new listener" highlight only catches a listener
// that appears while you happen to be watching.

// baselineEligible reports whether a listener belongs in the host's expected
// service surface. Ephemeral UDP client sockets do not: baselining those would
// mean a permanently dirty baseline. They stay visible in the Ports table.
//
// Nor do listeners inside other network namespaces: what a container offers
// the network is the host port that forwards to it (docker-proxy), which is a
// host listener and baselined as one. Its own sockets are keyed by a container
// id that changes on every recreate, and a baseline saved before namespaces
// were enumerated would otherwise flag every one of them.
func baselineEligible(p probe.ListenPort) bool {
	return p.NetNS == "" && probe.IsServiceListener(p)
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
	c, state := loadConfig()
	if state != configLoaded || len(c.Baseline) == 0 {
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
	if err := writeFileAtomic(path, append(b, '\n'), 0o644); err != nil {
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

// acceptListener folds a single listener into the baseline, or drops it again
// if it is already there. Re-saving the whole baseline to bless one new service
// would also bless anything else that appeared since, which is the opposite of
// what a baseline is for.
func (m *Model) acceptListener(p probe.ListenPort) (string, bool) {
	if !m.hasBaseline() {
		return "No baseline saved — press b to record one first", false
	}
	if p.NetNS != "" {
		return "Listeners inside containers are not part of the baseline — accept the host port that publishes them", false
	}
	if !baselineEligible(p) {
		return "Ephemeral UDP sockets are not part of the baseline", false
	}

	key := portKey(p)
	if m.baseline[key] {
		delete(m.baseline, key)
		m.baselineRaw = removeString(m.baselineRaw, key)
		m.saveConfig()
		return "Removed " + p.Proto + " " + p.Local + " from the baseline", true
	}

	m.baseline[key] = true
	m.baselineRaw = append(m.baselineRaw, key)
	sort.Strings(m.baselineRaw)
	m.saveConfig()
	return "Accepted " + p.Proto + " " + p.Local + " into the baseline", true
}

// removeString returns the slice without the first occurrence of s.
func removeString(list []string, s string) []string {
	for i, v := range list {
		if v == s {
			return append(list[:i:i], list[i+1:]...)
		}
	}
	return list
}

// BaselineComparable counts the listeners a baseline check actually looks at,
// so the "all clear" message does not quote a total that includes the ephemeral
// UDP sockets it deliberately ignores.
func BaselineComparable(ports []probe.ListenPort) int {
	n := 0
	for _, p := range ports {
		if baselineEligible(p) {
			n++
		}
	}
	return n
}

// baselineDriftListLimit is how many off-baseline listeners the startup drift
// event names before summarising the rest; an alert hook gets the detail as
// one line, and a line that wraps a terminal three times is not read.
const baselineDriftListLimit = 5

// noteBaselineDrift raises one alert for listeners already outside the
// baseline when a session starts. The first poll is deliberately the
// "new listener" baseline, so without this a daemon restarted onto a host that
// has been listening on something unexpected for a week would say nothing.
// One event rather than one per listener: it is a single finding, and a hook
// should fire once for it.
func (m *Model) noteBaselineDrift(rec *eventRecorder) {
	if !m.hasBaseline() {
		return
	}
	var off []probe.ListenPort
	for _, p := range m.ports {
		if m.offBaseline(p) {
			off = append(off, p)
		}
	}
	if len(off) == 0 {
		return
	}
	// Ordered by port so the same drift reads the same on every restart.
	sort.SliceStable(off, func(i, j int) bool { return cmpPorts(off[i], off[j], "port") < 0 })

	items := make([]string, 0, baselineDriftListLimit+1)
	for i, p := range off {
		if i == baselineDriftListLimit {
			items = append(items, fmt.Sprintf("%d more", len(off)-i))
			break
		}
		item := p.Proto + " " + p.Local
		if label := procUnitLabel(p.PID, p.Process, p.Unit); label != "" {
			item += " " + label
		}
		items = append(items, item)
	}
	detail := strings.Join(items, "; ")
	if !m.baselineAt.IsZero() {
		detail += " · baseline saved " + m.baselineAt.Format("2006-01-02")
	}
	n := len(off)
	rec.add(levelAlert, "baseline_drift",
		fmt.Sprintf("%d %s outside the baseline at startup", n, plural(n, "listener")), detail)
}
