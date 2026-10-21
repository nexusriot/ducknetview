package ui

import (
	"strings"
	"testing"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// --- snapshot diff -------------------------------------------------------

func diffFixture() (Snapshot, Snapshot) {
	from := Snapshot{
		Hostname: "web1", TakenAt: "2026-10-10T09:00:00Z",
		Ports: []probe.ListenPort{
			{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"},
			{Proto: "tcp", Local: "127.0.0.1:5432", PID: 2, Process: "postgres"},
		},
		Processes:   []probe.ProcNet{{PID: 1, Name: "sshd"}, {PID: 2, Name: "postgres"}},
		Connections: []probe.Conn{{Proto: "tcp", Remote: "10.0.0.9:443"}},
		Interfaces:  []probe.IfaceInfo{{Name: "eth0", IsUp: true}, {Name: "wg0", IsUp: true}},
	}
	to := Snapshot{
		Hostname: "web1", TakenAt: "2026-10-11T09:00:00Z",
		Ports: []probe.ListenPort{
			{Proto: "tcp", Local: "0.0.0.0:22", PID: 9, Process: "sshd"},
			{Proto: "tcp", Local: "0.0.0.0:5432", PID: 8, Process: "postgres"},
			{Proto: "tcp", Local: "0.0.0.0:8080", PID: 7, Process: "nginx"},
		},
		Processes:   []probe.ProcNet{{PID: 9, Name: "sshd"}, {PID: 8, Name: "postgres"}, {PID: 7, Name: "nginx"}},
		Connections: []probe.Conn{{Proto: "tcp", Remote: "203.0.113.5:443"}},
		Interfaces:  []probe.IfaceInfo{{Name: "eth0", IsUp: false}},
	}
	return from, to
}

func TestDiffSnapshots(t *testing.T) {
	d := DiffSnapshots(diffFixture())

	if !d.Changed() {
		t.Fatal("Changed = false for two clearly different snapshots")
	}
	// A PID change is a restart and must not register as a change at all.
	if len(d.ProcsAdded) != 1 || d.ProcsAdded[0] != "nginx" {
		t.Errorf("ProcsAdded = %v, want just nginx (a restart is not a change)", d.ProcsAdded)
	}
	if len(d.ProcsRemoved) != 0 {
		t.Errorf("ProcsRemoved = %v, want none", d.ProcsRemoved)
	}

	// A service moving from loopback to the world is the case worth catching:
	// one removal and one addition, not an unchanged row.
	added, removed := strings.Join(d.ListenersAdded, "\n"), strings.Join(d.ListenersRemoved, "\n")
	if !strings.Contains(added, "0.0.0.0:5432") || !strings.Contains(removed, "127.0.0.1:5432") {
		t.Errorf("a listener that widened its exposure was not reported:\n+%s\n-%s", added, removed)
	}
	if !strings.Contains(added, "0.0.0.0:8080") {
		t.Errorf("new listener missing from %q", added)
	}
	// sshd did not move, so it must appear in neither list.
	if strings.Contains(added, ":22") || strings.Contains(removed, ":22") {
		t.Error("an unchanged listener was reported as a difference")
	}

	if len(d.HostsAdded) != 1 || d.HostsAdded[0] != "203.0.113.5" {
		t.Errorf("HostsAdded = %v", d.HostsAdded)
	}
	if len(d.HostsRemoved) != 1 || d.HostsRemoved[0] != "10.0.0.9" {
		t.Errorf("HostsRemoved = %v", d.HostsRemoved)
	}
	if len(d.IfacesRemoved) != 1 || d.IfacesRemoved[0] != "wg0" {
		t.Errorf("IfacesRemoved = %v", d.IfacesRemoved)
	}
	if len(d.IfacesChanged) != 1 || !strings.Contains(d.IfacesChanged[0], "eth0") {
		t.Errorf("IfacesChanged = %v", d.IfacesChanged)
	}
}

func TestDiffIdenticalSnapshots(t *testing.T) {
	from, _ := diffFixture()
	d := DiffSnapshots(from, from)
	if d.Changed() {
		t.Errorf("Changed = true comparing a snapshot with itself: %+v", d)
	}
	if !strings.Contains(FormatDiff(d), "no differences") {
		t.Error("FormatDiff does not say so when nothing changed")
	}
}

// Comparing two different machines is almost always a mistake, and silently
// rendering it would be worse than saying so.
func TestDiffWarnsOnDifferentHosts(t *testing.T) {
	from, to := diffFixture()
	to.Hostname = "web2"
	d := DiffSnapshots(from, to)
	if !d.DifferentHosts {
		t.Fatal("DifferentHosts = false for snapshots of two machines")
	}
	if !strings.Contains(FormatDiff(d), "different hosts") {
		t.Error("FormatDiff does not warn about comparing two machines")
	}
}

func TestFormatDiffRendersEverySection(t *testing.T) {
	out := FormatDiff(DiffSnapshots(diffFixture()))
	for _, want := range []string{"listeners", "processes with sockets", "remote hosts", "interfaces",
		"+ ", "- ", "~ ", "web1", "2026-10-10 09:00:00"} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatDiff output missing %q:\n%s", want, out)
		}
	}
}

// Ephemeral UDP client sockets churn with ordinary traffic; counting them as
// listeners would make every diff noise.
func TestDiffIgnoresEphemeralSockets(t *testing.T) {
	from := Snapshot{Hostname: "h", Interfaces: []probe.IfaceInfo{{Name: "eth0"}}}
	to := Snapshot{
		Hostname:   "h",
		Interfaces: []probe.IfaceInfo{{Name: "eth0"}},
		Ports: []probe.ListenPort{
			{Proto: "udp", Local: "10.0.0.2:51837", PID: 3, Process: "chrome"},
		},
	}
	if d := DiffSnapshots(from, to); len(d.ListenersAdded) != 0 {
		t.Errorf("an ephemeral UDP socket was reported as a new listener: %v", d.ListenersAdded)
	}
}
