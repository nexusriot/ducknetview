package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Rates answer "what is happening now"; totals answer "what happened while I
// was watching". A transfer that finished thirty seconds ago has no rate at
// all, but it is usually the thing you opened the tool to find.

const (
	// maxTalkers bounds the per-host and per-process tables. A browsing session
	// touches thousands of hosts over a day, and an unbounded map would grow
	// for as long as the process runs.
	maxTalkers = 2048
	// talkerEvictTo is how far the tables are cut back when they overflow, so
	// eviction is amortised rather than running on every poll.
	talkerEvictTo = 1536
)

// byteTotals is a cumulative traffic total.
type byteTotals struct {
	rx, tx uint64
}

func (b byteTotals) sum() uint64 { return b.rx + b.tx }

func (b byteTotals) any() bool { return b.rx > 0 || b.tx > 0 }

// talker is one named traffic total: a process or a remote host.
type talker struct {
	key    string
	label  string
	totals byteTotals
	last   time.Time
}

// deltaBytes returns the increase between two readings of a cumulative
// counter, guarding the reset case (a recycled socket key) that would otherwise
// wrap the unsigned subtraction.
func deltaBytes(cur, prev uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

// addTotals books one connection's byte delta against its process, its remote
// host and the session grand total.
func (m *Model) addTotals(c probe.Conn, rx, tx uint64, now time.Time) {
	if rx == 0 && tx == 0 {
		return
	}

	m.sessionTotal.rx += rx
	m.sessionTotal.tx += tx
	m.bookUsage(c, rx, tx)

	if c.PID > 0 {
		if m.procTotals == nil {
			m.procTotals = map[int32]*talker{}
		}
		t := m.procTotals[c.PID]
		if t == nil {
			t = &talker{label: c.Process}
			m.procTotals[c.PID] = t
		}
		if t.label == "" {
			t.label = c.Process
		}
		t.totals.rx += rx
		t.totals.tx += tx
		t.last = now
	}

	if host := stripPort(c.Remote); host != "" {
		if m.hostTotals == nil {
			m.hostTotals = map[string]*talker{}
		}
		t := m.hostTotals[host]
		if t == nil {
			t = &talker{key: host, label: host}
			m.hostTotals[host] = t
		}
		t.totals.rx += rx
		t.totals.tx += tx
		t.last = now
	}

	m.evictTalkers()
}

// evictTalkers keeps the totals tables bounded, dropping the quietest entries
// first so the top talkers — the reason the tables exist — always survive.
func (m *Model) evictTalkers() {
	if len(m.hostTotals) > maxTalkers {
		keep := topTalkers(mapValues(m.hostTotals), talkerEvictTo)
		next := make(map[string]*talker, len(keep))
		for _, t := range keep {
			next[t.key] = t
		}
		m.hostTotals = next
	}
	if len(m.procTotals) > maxTalkers {
		type pidTalker struct {
			pid int32
			t   *talker
		}
		all := make([]pidTalker, 0, len(m.procTotals))
		for pid, t := range m.procTotals {
			all = append(all, pidTalker{pid, t})
		}
		sort.Slice(all, func(i, j int) bool {
			return all[i].t.totals.sum() > all[j].t.totals.sum()
		})
		next := make(map[int32]*talker, talkerEvictTo)
		for _, pt := range all[:talkerEvictTo] {
			next[pt.pid] = pt.t
		}
		m.procTotals = next
	}
}

func mapValues(m map[string]*talker) []*talker {
	out := make([]*talker, 0, len(m))
	for _, t := range m {
		out = append(out, t)
	}
	return out
}

// topTalkers returns the n biggest totals, largest first.
func topTalkers(all []*talker, n int) []*talker {
	sort.Slice(all, func(i, j int) bool {
		if a, b := all[i].totals.sum(), all[j].totals.sum(); a != b {
			return a > b
		}
		return all[i].label < all[j].label
	})
	if len(all) > n {
		all = all[:n]
	}
	return all
}

// procTotal returns a process's session traffic.
func (m Model) procTotal(pid int32) byteTotals {
	if t := m.procTotals[pid]; t != nil {
		return t.totals
	}
	return byteTotals{}
}

// hostTotal returns a remote host's session traffic.
func (m Model) hostTotal(ip string) byteTotals {
	if t := m.hostTotals[ip]; t != nil {
		return t.totals
	}
	return byteTotals{}
}

// connTotal returns a single connection's traffic. TCP counters are cumulative
// for the life of the socket, so this is the connection's whole history, not
// just what was seen since the tool started.
func (m Model) connTotal(c probe.Conn) byteTotals {
	return byteTotals{rx: c.RxBytes, tx: c.TxBytes}
}

// topProcTalkers / topHostTalkers back the Overview panel.
func (m Model) topProcTalkers(n int) []*talker {
	all := make([]*talker, 0, len(m.procTotals))
	for _, t := range m.procTotals {
		all = append(all, t)
	}
	return topTalkers(all, n)
}

func (m Model) topHostTalkers(n int) []*talker {
	return topTalkers(mapValues(m.hostTotals), n)
}

// topTalkerRows is how many processes and how many hosts the Overview panel
// lists. Small on purpose: it is a summary, and the tables are one keypress
// away for the full ranking.
const topTalkerRows = 3

// renderTopTalkers draws the Overview's session-traffic summary. It returns ""
// when nothing has been accounted yet, so the panel appears only once it has
// something to say.
func (m Model) renderTopTalkers(width int) string {
	procs := m.topProcTalkers(topTalkerRows)
	hosts := m.topHostTalkers(topTalkerRows)
	if !m.sessionTotal.any() || (len(procs) == 0 && len(hosts) == 0) {
		return ""
	}

	labelW := max(12, min(28, width-30))

	var b strings.Builder
	b.WriteString(titleStyle.Render("Top talkers (session)") + "  " +
		subtleStyle.Render("↓"+probe.HumanBytes(m.sessionTotal.rx)+" ↑"+probe.HumanBytes(m.sessionTotal.tx)) + "\n")

	row := func(kind string, t *talker) {
		label := t.label
		if label == "" {
			label = "-"
		}
		b.WriteString(fmt.Sprintf("  %s %s  ↓ %-8s ↑ %s\n",
			subtleStyle.Render(padRight(kind, 4)),
			padRight(trunc(label, labelW), labelW),
			probe.CompactBytes(t.totals.rx),
			probe.CompactBytes(t.totals.tx)))
	}
	for _, t := range procs {
		row("proc", t)
	}
	for _, t := range hosts {
		row("host", t)
	}
	b.WriteString("\n")

	return b.String()
}

// totalCells renders a row's cumulative RX/TX columns, dimming rows that have
// moved nothing.
func totalCells(t byteTotals, width int) (string, string) {
	rx := padRight(probe.CompactBytes(t.rx), width)
	tx := padRight(probe.CompactBytes(t.tx), width)
	if t.rx == 0 {
		rx = subtleStyle.Render(rx)
	}
	if t.tx == 0 {
		tx = subtleStyle.Render(tx)
	}
	return rx, tx
}

// trafficCells renders the RX/TX pair for a row in whichever mode is active:
// current rate, or cumulative session bytes (toggled with `t`).
func (m Model) trafficCells(rate rateSample, total byteTotals, width int) (string, string) {
	if m.showTotals {
		return totalCells(total, width)
	}
	return m.rateCells(rate, width)
}

// trafficHeaders labels those columns for the active mode and unit, so the
// numbers underneath are never ambiguous.
func (m Model) trafficHeaders() (string, string) {
	if m.showTotals {
		return "RX∑", "TX∑"
	}
	if m.rateUnit == probe.UnitBits {
		return "RXb", "TXb"
	}
	return "RX", "TX"
}

// rate formats a throughput figure in the active unit.
func (m Model) rate(bps float64) string {
	return probe.FormatRate(bps, m.rateUnit)
}

// renderChurn draws how many connections opened and closed per poll. A spike is
// how a connection leak or a scan announces itself long before the tables look
// unusual.
func (m Model) renderChurn(width int) string {
	if len(m.churnUpHist) < 2 {
		return ""
	}
	if maxOf(m.churnUpHist) == 0 && maxOf(m.churnDownHist) == 0 {
		return "" // nothing has opened or closed yet
	}

	chartW := max(10, min(width-24, 60))
	var b strings.Builder
	b.WriteString(titleStyle.Render("Connection churn") + "  " +
		subtleStyle.Render(fmt.Sprintf("per poll, peak +%.0f/-%.0f",
			maxOf(m.churnUpHist), maxOf(m.churnDownHist))) + "\n")
	b.WriteString(fmt.Sprintf("opened  %-5.0f %s\n", lastOr0(m.churnUpHist), Spark(m.churnUpHist, chartW)))
	b.WriteString(fmt.Sprintf("closed  %-5.0f %s\n\n", lastOr0(m.churnDownHist), Spark(m.churnDownHist, chartW)))
	return b.String()
}

// rateUnitIsBits reports whether throughput is currently worded in bits per
// second.
func (m Model) rateUnitIsBits() bool { return m.rateUnit == probe.UnitBits }
