package ui

import (
	"context"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// dnsMsg is the result of a single reverse-DNS lookup.
type dnsMsg struct {
	ip   string
	name string
}

func lookupAddrCmd(ip string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		names, err := net.DefaultResolver.LookupAddr(ctx, ip)
		if err != nil || len(names) == 0 {
			return dnsMsg{ip: ip, name: ""}
		}
		return dnsMsg{ip: ip, name: strings.TrimSuffix(names[0], ".")}
	}
}

// stripPort removes the port suffix from "ip:port" / "[v6]:port" / "v6:port".
// gopsutil emits IPv6 unbracketed, e.g. "::1:443" — last-colon split still gives "::1".
func stripPort(hp string) string {
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		return hp
	}
	return hp[:i]
}

// kickConnLookups schedules reverse-DNS lookups for every unique remote IP in
// the current snapshot that hasn't been tried yet. Idempotent.
func (m *Model) kickConnLookups() []tea.Cmd {
	if !m.connsRevDNS {
		return nil
	}
	if m.dnsCache == nil {
		m.dnsCache = map[string]string{}
	}
	if m.dnsTried == nil {
		m.dnsTried = map[string]bool{}
	}
	var cmds []tea.Cmd
	seen := map[string]bool{}
	for _, c := range m.conns {
		ip := stripPort(c.Remote)
		if ip == "" || ip == "0.0.0.0" || ip == "::" {
			continue
		}
		if seen[ip] || m.dnsTried[ip] {
			continue
		}
		seen[ip] = true
		m.dnsTried[ip] = true
		cmds = append(cmds, lookupAddrCmd(ip))
	}
	return cmds
}

// dnsDisplay returns the host portion to display for a remote "ip:port",
// substituting the resolved name when reverse-DNS is enabled and known.
func (m Model) dnsDisplay(remote string) string {
	if !m.connsRevDNS || m.dnsCache == nil {
		return remote
	}
	i := strings.LastIndex(remote, ":")
	if i < 0 {
		return remote
	}
	ip := remote[:i]
	port := remote[i:]
	if name, ok := m.dnsCache[ip]; ok && name != "" {
		return name + port
	}
	return remote
}

type connGroup struct {
	ip        string
	count     int
	states    map[string]int
	procs     []string
	seenProcs map[string]bool
	conns     []probe.Conn
}

// groupRate sums the current throughput of every connection to a remote host,
// so the grouped view ranks hosts the same way the flat view ranks sockets.
func (m Model) groupRate(g *connGroup) rateSample {
	var out rateSample
	for _, c := range g.conns {
		r := m.connRate(c)
		out.rx += r.rx
		out.tx += r.tx
	}
	return out
}

func (m Model) buildConnGroups() []*connGroup {
	groups := map[string]*connGroup{}
	order := []string{}
	for _, c := range m.conns {
		if !m.passConnFilters(c) {
			continue
		}
		ip := stripPort(c.Remote)
		g, ok := groups[ip]
		if !ok {
			g = &connGroup{ip: ip, states: map[string]int{}, seenProcs: map[string]bool{}}
			groups[ip] = g
			order = append(order, ip)
		}
		g.count++
		g.conns = append(g.conns, c)
		if c.State != "" {
			g.states[c.State]++
		}
		proc := c.Process
		if proc == "" {
			proc = "-"
		}
		if !g.seenProcs[proc] {
			g.seenProcs[proc] = true
			g.procs = append(g.procs, proc)
		}
	}
	out := make([]*connGroup, 0, len(order))
	for _, ip := range order {
		out = append(out, groups[ip])
	}
	// Sort by descending connection count, then by IP.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].ip < out[j].ip
	})
	return out
}

func formatStates(states map[string]int) string {
	type kv struct {
		k string
		v int
	}
	xs := make([]kv, 0, len(states))
	for k, v := range states {
		xs = append(xs, kv{k, v})
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].v != xs[j].v {
			return xs[i].v > xs[j].v
		}
		return xs[i].k < xs[j].k
	})
	parts := make([]string, 0, len(xs))
	for _, x := range xs {
		parts = append(parts, x.k+":"+strconv.Itoa(x.v))
	}
	return strings.Join(parts, " ")
}
