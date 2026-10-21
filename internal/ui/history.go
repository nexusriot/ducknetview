package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Session totals die with the session, which is no use for the question people
// actually ask on a metered link: where did this month's data go? Merging each
// session's totals into a per-day file answers it without keeping a database.

const (
	// historyDays is how much history is kept. A month is the billing period
	// that matters; a little more gives context.
	historyDays = 40
	// historyTopN is how many processes and hosts are kept per day. Keeping
	// everything would grow the file without making the answer any better.
	historyTopN = 50
)

// dayUsage is one day's accounted traffic.
type dayUsage struct {
	Date      string            `json:"date"` // YYYY-MM-DD
	RxBytes   uint64            `json:"rx_bytes"`
	TxBytes   uint64            `json:"tx_bytes"`
	Processes map[string]uint64 `json:"processes,omitempty"`
	Hosts     map[string]uint64 `json:"hosts,omitempty"`
}

// usageHistory is the on-disk shape: newest day last.
type usageHistory struct {
	Days []dayUsage `json:"days"`
}

func historyPath() (string, error) {
	p, err := configPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "usage.json"), nil
}

// LoadUsageHistory reads the recorded daily totals. A missing or unreadable file
// is an empty history, not an error: this is a convenience, not a ledger.
func LoadUsageHistory() usageHistory {
	p, err := historyPath()
	if err != nil {
		return usageHistory{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return usageHistory{}
	}
	var h usageHistory
	if json.Unmarshal(b, &h) != nil {
		return usageHistory{}
	}
	return h
}

// mergeSession folds a stretch of counted traffic into the given day.
func (h usageHistory) mergeSession(day string, total byteTotals, procs, hosts map[string]uint64) usageHistory {
	idx := -1
	for i := range h.Days {
		if h.Days[i].Date == day {
			idx = i
			break
		}
	}
	if idx < 0 {
		h.Days = append(h.Days, dayUsage{Date: day})
		idx = len(h.Days) - 1
	}

	d := &h.Days[idx]
	d.RxBytes += total.rx
	d.TxBytes += total.tx
	d.Processes = mergeCounts(d.Processes, procs, historyTopN)
	d.Hosts = mergeCounts(d.Hosts, hosts, historyTopN)

	// Oldest days first, then trim.
	sort.Slice(h.Days, func(i, j int) bool { return h.Days[i].Date < h.Days[j].Date })
	if len(h.Days) > historyDays {
		h.Days = h.Days[len(h.Days)-historyDays:]
	}
	return h
}

// mergeCounts adds the session's per-name totals into a day's, keeping only the
// biggest few so the file cannot grow without bound.
func mergeCounts(into, add map[string]uint64, keep int) map[string]uint64 {
	if into == nil {
		into = map[string]uint64{}
	}
	for name, n := range add {
		into[name] += n
	}
	if len(into) <= keep {
		return into
	}

	type entry struct {
		name  string
		bytes uint64
	}
	all := make([]entry, 0, len(into))
	for name, n := range into {
		all = append(all, entry{name, n})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].bytes != all[j].bytes {
			return all[i].bytes > all[j].bytes
		}
		return all[i].name < all[j].name
	})

	trimmed := make(map[string]uint64, keep)
	for _, e := range all[:keep] {
		trimmed[e.name] = e.bytes
	}
	return trimmed
}

// usagePending is traffic counted since the history file was last written.
// Flushing what accumulated, rather than the session's running totals, is what
// lets the file be written more than once per session without counting
// anything twice.
type usagePending struct {
	total        byteTotals
	procs, hosts map[string]uint64
}

// bookUsage records one connection's byte delta for the next history flush.
func (m *Model) bookUsage(c probe.Conn, rx, tx uint64) {
	// A snapshot or remote session describes another machine, and is never
	// written to this host's history.
	if m.snapshotOnly {
		return
	}
	p := &m.usagePending
	if p.procs == nil {
		p.procs = map[string]uint64{}
		p.hosts = map[string]uint64{}
	}
	p.total.rx += rx
	p.total.tx += tx
	if c.PID > 0 {
		name := c.Process
		if name == "" {
			name = "-"
		}
		p.procs[name] += rx + tx
	}
	if host := stripPort(c.Remote); host != "" {
		p.hosts[host] += rx + tx
	}
}

// saveUsage merges the traffic counted since the last flush into today's entry
// of the history file. It runs on quit and alongside the trend flush, so a
// daemon that is killed, or one running for weeks, neither loses its history
// nor books a fortnight onto the day it stopped. Best-effort: failing to record
// usage must never stop the tool from exiting.
func (m *Model) saveUsage() {
	if !m.usagePending.total.any() {
		return
	}
	p, err := historyPath()
	if err != nil {
		return
	}

	pend := m.usagePending
	h := LoadUsageHistory().mergeSession(time.Now().Format("2006-01-02"), pend.total, pend.procs, pend.hosts)
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return
	}
	if writeFileAtomic(p, append(b, '\n'), 0o644) == nil {
		m.usagePending = usagePending{}
	}
}

// FormatUsage renders the recorded history for `--usage`, newest day first.
func FormatUsage(days int) string {
	h := LoadUsageHistory()
	if len(h.Days) == 0 {
		return "no usage recorded yet — run the TUI for a while first\n"
	}
	if days > 0 && len(h.Days) > days {
		h.Days = h.Days[len(h.Days)-days:]
	}

	var out []byte
	var totalRx, totalTx uint64
	out = append(out, "date          received       sent           top process\n"...)
	for i := len(h.Days) - 1; i >= 0; i-- {
		d := h.Days[i]
		totalRx += d.RxBytes
		totalTx += d.TxBytes

		top, topBytes := "-", uint64(0)
		for name, n := range d.Processes {
			if n > topBytes {
				top, topBytes = name, n
			}
		}
		out = append(out, []byte(padRight(d.Date, 14)+
			padRight(probe.HumanBytes(d.RxBytes), 15)+
			padRight(probe.HumanBytes(d.TxBytes), 15)+
			top+"\n")...)
	}
	out = append(out, []byte("\ntotal across "+strconv.Itoa(len(h.Days))+" "+plural(len(h.Days), "day")+
		": "+probe.HumanBytes(totalRx)+" received, "+probe.HumanBytes(totalTx)+" sent\n")...)
	return string(out)
}
