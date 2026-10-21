package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Prometheus's text exposition format is the cheapest way to make everything
// this tool measures scrapeable: node_exporter reads a directory of such files,
// so `--metrics > /var/lib/node_exporter/ducknetview.prom` from cron is a
// complete integration.

// escapeLabel quotes a Prometheus label value.
func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return strings.ReplaceAll(v, "\n", " ")
}

// FormatMetrics renders one sample of the host's network state in Prometheus
// text format. Rates come from the caller having sampled twice.
func FormatMetrics(snap probe.NetSnapshot, info probe.ConnInfo, offBaseline int, baselineKnown bool) string {
	var b strings.Builder

	metric := func(name, help, typ string) {
		fmt.Fprintf(&b, "# HELP ducknetview_%s %s\n# TYPE ducknetview_%s %s\n", name, help, name, typ)
	}

	metric("iface_receive_bytes_per_second", "Current receive rate per interface.", "gauge")
	for _, ii := range snap.Ifaces {
		fmt.Fprintf(&b, "ducknetview_iface_receive_bytes_per_second{iface=\"%s\",kind=\"%s\"} %.0f\n",
			escapeLabel(ii.Name), escapeLabel(ii.Kind.Label()), ii.RxBps)
	}
	metric("iface_transmit_bytes_per_second", "Current transmit rate per interface.", "gauge")
	for _, ii := range snap.Ifaces {
		fmt.Fprintf(&b, "ducknetview_iface_transmit_bytes_per_second{iface=\"%s\",kind=\"%s\"} %.0f\n",
			escapeLabel(ii.Name), escapeLabel(ii.Kind.Label()), ii.TxBps)
	}
	metric("iface_errors_total", "Interface errors and drops since boot.", "counter")
	for _, ii := range snap.Ifaces {
		fmt.Fprintf(&b, "ducknetview_iface_errors_total{iface=\"%s\",direction=\"in\"} %d\n",
			escapeLabel(ii.Name), ii.Errin+ii.Dropin)
		fmt.Fprintf(&b, "ducknetview_iface_errors_total{iface=\"%s\",direction=\"out\"} %d\n",
			escapeLabel(ii.Name), ii.Errout+ii.Dropout)
	}
	metric("iface_up", "Whether the interface is up.", "gauge")
	for _, ii := range snap.Ifaces {
		up := 0
		if ii.IsUp {
			up = 1
		}
		fmt.Fprintf(&b, "ducknetview_iface_up{iface=\"%s\"} %d\n", escapeLabel(ii.Name), up)
	}

	// Listener exposure is the security-relevant summary: a rising "exposed"
	// count is a host that started offering something to the network.
	exposure := map[string]int{"exposed": 0, "lan": 0, "local": 0, "netns": 0}
	for _, p := range info.Ports {
		if !probe.IsServiceListener(p) {
			continue // a client socket is not part of the host's exposure
		}
		if e := p.Exposure(); e != "" {
			exposure[e]++
		}
	}
	metric("listeners", "Listening sockets by reachability.", "gauge")
	for _, key := range []string{"exposed", "lan", "local", "netns"} {
		fmt.Fprintf(&b, "ducknetview_listeners{exposure=\"%s\"} %d\n", key, exposure[key])
	}

	// Accept-queue fill per TCP listener. A ratio rather than the raw depth is
	// what a single alert rule can be written against, whatever each
	// service's backlog happens to be.
	// Forked workers inherit their parent's listening socket, so the same
	// address and process name can appear once per PID; a repeated label set
	// would make Prometheus reject the whole scrape. It is one queue anyway.
	var queued []probe.ListenPort
	seenQueue := map[string]bool{}
	for _, p := range info.Ports {
		if _, ok := p.QueueRatio(); !ok {
			continue
		}
		if k := p.Local + "|" + p.Process; !seenQueue[k] {
			seenQueue[k] = true
			queued = append(queued, p)
		}
	}
	if len(queued) > 0 {
		metric("listener_queue_ratio", "Accept-queue depth as a fraction of the listen backlog, per TCP listener.", "gauge")
		for _, p := range queued {
			ratio, _ := p.QueueRatio()
			fmt.Fprintf(&b, "ducknetview_listener_queue_ratio{local=\"%s\",process=\"%s\"} %g\n",
				escapeLabel(p.Local), escapeLabel(p.Process), ratio)
		}
		metric("listener_accept_queue", "Connections waiting to be accepted, per TCP listener.", "gauge")
		for _, p := range queued {
			fmt.Fprintf(&b, "ducknetview_listener_accept_queue{local=\"%s\",process=\"%s\"} %d\n",
				escapeLabel(p.Local), escapeLabel(p.Process), p.AcceptQueue)
		}
	}

	if snap.TCP != nil {
		metric("tcp_counter_total", "Host-wide TCP counters since boot, from /proc/net/snmp and /proc/net/netstat.", "counter")
		for _, c := range snap.TCP.Counters() {
			fmt.Fprintf(&b, "ducknetview_tcp_counter_total{counter=\"%s\"} %d\n", c.Key, c.Total)
		}
	}

	scopes := map[string]int{}
	var retrans uint64
	for _, c := range info.Conns {
		scopes[probe.IPScope(stripPort(c.Remote))]++
		retrans += uint64(c.Retrans)
	}
	metric("connections", "Active connections by remote scope.", "gauge")
	keys := make([]string, 0, len(scopes))
	for k := range scopes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		label := k
		if label == "" {
			label = "unknown"
		}
		fmt.Fprintf(&b, "ducknetview_connections{scope=\"%s\"} %d\n", escapeLabel(label), scopes[k])
	}

	metric("connection_retransmits_total", "Retransmits summed over live TCP sockets.", "gauge")
	fmt.Fprintf(&b, "ducknetview_connection_retransmits_total %d\n", retrans)

	metric("processes_with_sockets", "Processes holding at least one socket.", "gauge")
	fmt.Fprintf(&b, "ducknetview_processes_with_sockets %d\n", len(info.Procs))

	// A baseline check is the one thing worth alerting on directly, so it is
	// exported as a number even when no baseline exists (-1 says "unknown",
	// which is different from "clean").
	metric("listeners_off_baseline", "Listeners not present in the saved baseline (-1 when no baseline is saved).", "gauge")
	if baselineKnown {
		fmt.Fprintf(&b, "ducknetview_listeners_off_baseline %d\n", offBaseline)
	} else {
		b.WriteString("ducknetview_listeners_off_baseline -1\n")
	}

	return b.String()
}

// FormatTopTalkers renders the busiest processes and hosts for `--top`, from two
// samples taken by the caller.
func FormatTopTalkers(prev, cur probe.ConnInfo, seconds float64, n int) string {
	if seconds <= 0 {
		seconds = 1
	}

	before := make(map[string]sockBytes, len(prev.Conns))
	for _, c := range prev.Conns {
		before[connKey(c)] = sockBytes{rx: c.RxBytes, tx: c.TxBytes}
	}

	type talkerRate struct {
		label  string
		rx, tx float64
	}
	procs := map[string]*talkerRate{}
	hosts := map[string]*talkerRate{}

	for _, c := range cur.Conns {
		p, ok := before[connKey(c)]
		if !ok {
			continue
		}
		rx := float64(deltaBytes(c.RxBytes, p.rx)) / seconds
		tx := float64(deltaBytes(c.TxBytes, p.tx)) / seconds
		if rx == 0 && tx == 0 {
			continue
		}

		name := c.Process
		if name == "" {
			name = fmt.Sprintf("pid %d", c.PID)
		}
		add := func(into map[string]*talkerRate, key string) {
			t := into[key]
			if t == nil {
				t = &talkerRate{label: key}
				into[key] = t
			}
			t.rx += rx
			t.tx += tx
		}
		add(procs, name)
		add(hosts, stripPort(c.Remote))
	}

	rank := func(in map[string]*talkerRate) []*talkerRate {
		out := make([]*talkerRate, 0, len(in))
		for _, t := range in {
			out = append(out, t)
		}
		sort.Slice(out, func(i, j int) bool {
			return out[i].rx+out[i].tx > out[j].rx+out[j].tx
		})
		if len(out) > n {
			out = out[:n]
		}
		return out
	}

	var b strings.Builder
	section := func(title string, rows []*talkerRate) {
		fmt.Fprintf(&b, "%s\n", title)
		if len(rows) == 0 {
			b.WriteString("  (nothing moved during the sample)\n")
			return
		}
		for _, t := range rows {
			fmt.Fprintf(&b, "  %-40s RX %-12s TX %s\n",
				trunc(t.label, 40), probe.HumanBytesPerSec(t.rx), probe.HumanBytesPerSec(t.tx))
		}
	}
	section("top processes", rank(procs))
	b.WriteString("\n")
	section("top hosts", rank(hosts))
	return b.String()
}
