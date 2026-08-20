package ui

import (
	"strconv"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// rateSample is throughput in bytes per second.
type rateSample struct {
	rx, tx float64
}

func (r rateSample) any() bool { return r.rx > 0 || r.tx > 0 }

// sockBytes is one poll's cumulative counters for a single socket.
type sockBytes struct {
	rx, tx uint64
}

// updateRates turns the kernel's cumulative per-socket byte counters into
// throughput by diffing consecutive enumerations, and aggregates the result per
// process. The first enumeration only establishes a baseline: rates appear on
// the poll after that.
func (m *Model) updateRates(conns []probe.Conn, now time.Time) {
	cur := make(map[string]sockBytes, len(conns))
	rates := make(map[string]rateSample, len(conns))
	procs := make(map[int32]rateSample, 16)

	dt := now.Sub(m.lastRateSample).Seconds()
	haveBaseline := !m.lastRateSample.IsZero() && dt > 0

	for _, c := range conns {
		if !m.hasThroughput && (c.RxBytes > 0 || c.TxBytes > 0) {
			// Latches on: once the platform has shown it can account for
			// sockets, the columns stay put instead of vanishing when traffic
			// goes quiet.
			m.hasThroughput = true
		}

		key := connKey(c)
		cur[key] = sockBytes{rx: c.RxBytes, tx: c.TxBytes}
		if !haveBaseline {
			continue
		}
		prev, ok := m.prevSockBytes[key]
		if !ok {
			continue // first sighting: nothing to diff against yet
		}

		rxDelta, txDelta := deltaBytes(c.RxBytes, prev.rx), deltaBytes(c.TxBytes, prev.tx)
		m.addTotals(c, rxDelta, txDelta, now)

		r := rateSample{rx: float64(rxDelta) / dt, tx: float64(txDelta) / dt}
		if !r.any() {
			continue
		}
		rates[key] = r
		m.recordRateHistory(key, c.PID, r)

		if c.PID > 0 {
			agg := procs[c.PID]
			agg.rx += r.rx
			agg.tx += r.tx
			procs[c.PID] = agg
		}
	}

	m.prevSockBytes = cur
	m.connRates = rates
	m.procRates = procs
	m.lastRateSample = now
	m.pruneRateHistory(cur)
}

// rateHistoryLen is how many samples of per-row throughput are kept to draw the
// sparklines in the detail overlay.
const rateHistoryLen = 40

// recordRateHistory appends a sample to a connection's (and its process's)
// rolling throughput history.
func (m *Model) recordRateHistory(key string, pid int32, r rateSample) {
	if m.connRateHist == nil {
		m.connRateHist = map[string]*rateHistory{}
	}
	h := m.connRateHist[key]
	if h == nil {
		h = &rateHistory{}
		m.connRateHist[key] = h
	}
	h.add(r)

	if pid > 0 {
		if m.procRateHist == nil {
			m.procRateHist = map[int32]*rateHistory{}
		}
		ph := m.procRateHist[pid]
		if ph == nil {
			ph = &rateHistory{}
			m.procRateHist[pid] = ph
		}
		ph.add(r)
	}
}

// pruneRateHistory drops history for sockets that no longer exist, which keeps
// the map bounded by the number of live connections.
func (m *Model) pruneRateHistory(live map[string]sockBytes) {
	for key := range m.connRateHist {
		if _, ok := live[key]; !ok {
			delete(m.connRateHist, key)
		}
	}
	if len(m.procRateHist) > maxTalkers {
		m.procRateHist = map[int32]*rateHistory{}
	}
}

// rateHistory is a short rolling window of throughput samples.
type rateHistory struct {
	rx, tx []float64
}

func (h *rateHistory) add(r rateSample) {
	h.rx = probe.ClampHistory(append(h.rx, r.rx), rateHistoryLen)
	h.tx = probe.ClampHistory(append(h.tx, r.tx), rateHistoryLen)
}

// connRate returns the throughput measured for a connection over the last poll
// interval (zero when idle, unknown or unsupported).
func (m Model) connRate(c probe.Conn) rateSample {
	return m.connRates[connKey(c)]
}

// procRate returns the summed throughput of every connection owned by a PID.
func (m Model) procRate(pid int32) rateSample {
	return m.procRates[pid]
}

// formatRTT renders a round-trip time in at most six columns: "-", "<1ms",
// "134ms", "1.2s".
func formatRTT(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Millisecond:
		return "<1ms"
	case d < time.Second:
		return strconv.Itoa(int(d.Round(time.Millisecond)/time.Millisecond)) + "ms"
	default:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	}
}

// formatRetrans renders a retransmit count in at most four columns.
func formatRetrans(n uint32) string {
	switch {
	case n == 0:
		return "-"
	case n < 1000:
		return strconv.FormatUint(uint64(n), 10)
	default:
		return strconv.FormatUint(uint64(n)/1000, 10) + "k"
	}
}

// rateCells renders a row's RX/TX columns, dimming idle rows so the ones
// actually moving data stand out.
func (m Model) rateCells(r rateSample, width int) (string, string) {
	rx := padRight(probe.CompactRateUnit(r.rx, m.rateUnit), width)
	tx := padRight(probe.CompactRateUnit(r.tx, m.rateUnit), width)
	if r.rx <= 0 {
		rx = subtleStyle.Render(rx)
	}
	if r.tx <= 0 {
		tx = subtleStyle.Render(tx)
	}
	return rx, tx
}
