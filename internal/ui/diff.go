package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Two snapshots either side of a change answer the question people actually
// ask — "what did that deploy do to this host?" — which neither a live table
// nor a single snapshot can. The comparison is deliberately about the things
// that are stable enough to compare: listeners, which processes hold sockets,
// which remote hosts are being talked to, and the links themselves. Individual
// connections churn by the second and would drown everything else.

// DiffResult is what changed between two snapshots.
type DiffResult struct {
	From, To     string // hostnames
	FromAt, ToAt string // timestamps, as recorded

	ListenersAdded   []string
	ListenersRemoved []string
	ProcsAdded       []string
	ProcsRemoved     []string
	HostsAdded       []string
	HostsRemoved     []string
	IfacesAdded      []string
	IfacesRemoved    []string
	IfacesChanged    []string

	// DifferentHosts is set when the two snapshots are not of the same machine,
	// which is almost always a mistake worth saying out loud rather than
	// rendering a diff of two unrelated hosts.
	DifferentHosts bool
}

// Changed reports whether anything differs, which is what the exit code is
// derived from.
func (d DiffResult) Changed() bool {
	return len(d.ListenersAdded)+len(d.ListenersRemoved)+
		len(d.ProcsAdded)+len(d.ProcsRemoved)+
		len(d.HostsAdded)+len(d.HostsRemoved)+
		len(d.IfacesAdded)+len(d.IfacesRemoved)+len(d.IfacesChanged) > 0
}

// listenerLabel describes a listener the way the diff reports it: what is
// listening where, under which process.
func listenerLabel(p probe.ListenPort) string {
	_, port := probe.SplitHostPort(p.Local)
	label := fmt.Sprintf("%-4s %-24s %s", p.Proto, p.Local, probe.ServiceName(port, p.Proto))
	if p.Process != "" {
		label += "  " + p.Process
	}
	return strings.TrimRight(label, " ")
}

// DiffSnapshots compares two snapshots, oldest first.
func DiffSnapshots(from, to Snapshot) DiffResult {
	d := DiffResult{
		From: from.Hostname, To: to.Hostname,
		FromAt: from.TakenAt, ToAt: to.TakenAt,
		DifferentHosts: from.Hostname != "" && to.Hostname != "" && from.Hostname != to.Hostname,
	}

	// Listeners are keyed the way the baseline keys them, so "a service moved
	// from loopback to the world" shows up as one removal and one addition
	// rather than being folded into a single unchanged row.
	oldPorts := map[string]probe.ListenPort{}
	for _, p := range from.Ports {
		if baselineEligible(p) {
			oldPorts[portKey(p)] = p
		}
	}
	newPorts := map[string]probe.ListenPort{}
	for _, p := range to.Ports {
		if baselineEligible(p) {
			newPorts[portKey(p)] = p
		}
	}
	for k, p := range newPorts {
		if _, ok := oldPorts[k]; !ok {
			d.ListenersAdded = append(d.ListenersAdded, listenerLabel(p))
		}
	}
	for k, p := range oldPorts {
		if _, ok := newPorts[k]; !ok {
			d.ListenersRemoved = append(d.ListenersRemoved, listenerLabel(p))
		}
	}

	// Processes are compared by name, not PID: a restart changes the PID and
	// changes nothing anyone cares about.
	d.ProcsAdded, d.ProcsRemoved = diffSets(procNameSet(from.Processes), procNameSet(to.Processes))

	// Remote hosts, not connections: the set of peers is stable enough to
	// compare, while the sockets to them are not.
	d.HostsAdded, d.HostsRemoved = diffSets(remoteHostSet(from.Connections), remoteHostSet(to.Connections))

	oldIf := map[string]probe.IfaceInfo{}
	for _, ii := range from.Interfaces {
		oldIf[ii.Name] = ii
	}
	newIf := map[string]probe.IfaceInfo{}
	for _, ii := range to.Interfaces {
		newIf[ii.Name] = ii
	}
	for name, ii := range newIf {
		prev, ok := oldIf[name]
		switch {
		case !ok:
			d.IfacesAdded = append(d.IfacesAdded, name+" ("+upDown(ii.IsUp)+")")
		case prev.IsUp != ii.IsUp:
			d.IfacesChanged = append(d.IfacesChanged, name+": "+upDown(prev.IsUp)+" → "+upDown(ii.IsUp))
		}
	}
	for name := range oldIf {
		if _, ok := newIf[name]; !ok {
			d.IfacesRemoved = append(d.IfacesRemoved, name)
		}
	}

	for _, s := range []*[]string{
		&d.ListenersAdded, &d.ListenersRemoved,
		&d.ProcsAdded, &d.ProcsRemoved,
		&d.HostsAdded, &d.HostsRemoved,
		&d.IfacesAdded, &d.IfacesRemoved, &d.IfacesChanged,
	} {
		sort.Strings(*s)
	}
	return d
}

func procNameSet(procs []probe.ProcNet) map[string]bool {
	out := make(map[string]bool, len(procs))
	for _, p := range procs {
		name := p.Name
		if name == "" {
			continue
		}
		out[name] = true
	}
	return out
}

func remoteHostSet(conns []probe.Conn) map[string]bool {
	out := make(map[string]bool, len(conns))
	for _, c := range conns {
		if host := stripPort(c.Remote); host != "" {
			out[host] = true
		}
	}
	return out
}

// diffSets returns what is in b but not a, and what is in a but not b.
func diffSets(a, b map[string]bool) (added, removed []string) {
	for k := range b {
		if !a[k] {
			added = append(added, k)
		}
	}
	for k := range a {
		if !b[k] {
			removed = append(removed, k)
		}
	}
	return added, removed
}

// FormatDiff renders a comparison for `--diff`. Colour is deliberately absent:
// this is output for a terminal that may well be a cron mail.
func FormatDiff(d DiffResult) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s  %s\n", padRight(nonEmpty(d.From, "?"), 24), shortTime(d.FromAt))
	fmt.Fprintf(&b, "%s  %s\n", padRight(nonEmpty(d.To, "?"), 24), shortTime(d.ToAt))
	if d.DifferentHosts {
		fmt.Fprintf(&b, "\nwarning: these snapshots are of different hosts (%s and %s)\n", d.From, d.To)
	}
	b.WriteString("\n")

	section := func(title string, added, removed []string) {
		if len(added) == 0 && len(removed) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s\n", title)
		for _, s := range added {
			fmt.Fprintf(&b, "  + %s\n", s)
		}
		for _, s := range removed {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
		b.WriteString("\n")
	}

	section("listeners", d.ListenersAdded, d.ListenersRemoved)
	section("processes with sockets", d.ProcsAdded, d.ProcsRemoved)
	section("remote hosts", d.HostsAdded, d.HostsRemoved)

	if len(d.IfacesAdded)+len(d.IfacesRemoved)+len(d.IfacesChanged) > 0 {
		b.WriteString("interfaces\n")
		for _, s := range d.IfacesAdded {
			fmt.Fprintf(&b, "  + %s\n", s)
		}
		for _, s := range d.IfacesRemoved {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
		for _, s := range d.IfacesChanged {
			fmt.Fprintf(&b, "  ~ %s\n", s)
		}
		b.WriteString("\n")
	}

	if !d.Changed() {
		b.WriteString("no differences in listeners, processes, remote hosts or interfaces\n")
	}
	return b.String()
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// shortTime renders a recorded RFC 3339 timestamp readably, leaving anything
// unparseable exactly as it was written.
func shortTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Format("2006-01-02 15:04:05")
}
