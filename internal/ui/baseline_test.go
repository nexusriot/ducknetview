package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// The "all clear" line quotes a count, and quoting one that includes the
// ephemeral sockets the check ignores would be a lie.
func TestBaselineComparableIgnoresEphemeralUDP(t *testing.T) {
	ports := []probe.ListenPort{
		{Proto: "tcp", Local: "0.0.0.0:22"},
		{Proto: "udp", Local: "0.0.0.0:53"},
		{Proto: "udp", Local: "0.0.0.0:54321"}, // ephemeral: a client socket
	}
	if got := BaselineComparable(ports); got != 2 {
		t.Errorf("BaselineComparable = %d, want 2", got)
	}
	if got := BaselineComparable(nil); got != 0 {
		t.Errorf("BaselineComparable(nil) = %d", got)
	}
}

func TestBaselineDetectsUnexpectedListener(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	backdoor := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:4444", PID: 66, Process: "nc"}

	m := eventsModel()
	m.ports = []probe.ListenPort{sshd}

	if m.offBaseline(backdoor) {
		t.Error("nothing should be off-baseline before a baseline exists")
	}

	m.setBaseline()
	if !m.hasBaseline() {
		t.Fatal("baseline was not recorded")
	}
	if m.offBaseline(sshd) {
		t.Error("a listener present when the baseline was taken is not off-baseline")
	}
	if !m.offBaseline(backdoor) {
		t.Error("a listener absent from the baseline should be flagged")
	}

	m.ports = []probe.ListenPort{sshd, backdoor}
	if got := m.offBaselineCount(); got != 1 {
		t.Errorf("offBaselineCount = %d, want 1", got)
	}
	m.portsCursor = 0
	if !strings.Contains(m.renderPortsText(), "off-baseline") {
		t.Error("the ports subtitle should report off-baseline listeners")
	}

	// The saved baseline is what the scriptable check reads.
	offending, _, ok := BaselineCheck([]probe.ListenPort{sshd, backdoor})
	if !ok {
		t.Fatal("BaselineCheck found no saved baseline")
	}
	if len(offending) != 1 || offending[0].Local != backdoor.Local {
		t.Errorf("BaselineCheck offending = %+v, want just the backdoor", offending)
	}

	m.clearBaseline()
	if m.hasBaseline() || m.offBaseline(backdoor) {
		t.Error("clearBaseline should switch the alerting back off")
	}
	if _, _, ok := BaselineCheck([]probe.ListenPort{backdoor}); ok {
		t.Error("BaselineCheck should report no baseline after it was cleared")
	}
}

func TestBaselineSurvivesReload(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := eventsModel()
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}}
	m.setBaseline()

	reloaded := NewModel().WithConfig()
	if !reloaded.hasBaseline() {
		t.Fatal("baseline did not survive a restart")
	}
	if reloaded.offBaseline(m.ports[0]) {
		t.Error("restored baseline does not recognise the listener it was built from")
	}
	if reloaded.baselineAt.IsZero() {
		t.Error("baseline timestamp was not restored")
	}
}

// UDP has no LISTEN state, so gopsutil reports every outbound DNS socket as a
// listener. Baselining those would leave the baseline permanently dirty.
func TestBaselineIgnoresEphemeralUDP(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dns := probe.ListenPort{Proto: "udp", Local: "0.0.0.0:53", PID: 1, Process: "resolved"}
	client := probe.ListenPort{Proto: "udp", Local: "192.168.1.5:47572", PID: 2, Process: "firefox"}
	tcpHigh := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:52000", PID: 3, Process: "app"}

	m := eventsModel()
	m.ports = []probe.ListenPort{dns}
	m.setBaseline()

	if m.offBaseline(client) {
		t.Error("an ephemeral UDP client socket must not count as an unexpected listener")
	}
	// A TCP listener on a high port is a real service and still counts.
	if !m.offBaseline(tcpHigh) {
		t.Error("a TCP listener outside the baseline should be flagged whatever its port")
	}

	m.ports = []probe.ListenPort{dns, client, tcpHigh}
	if got := m.offBaselineCount(); got != 1 {
		t.Errorf("offBaselineCount = %d, want 1 (only the TCP listener)", got)
	}

	offending, _, ok := BaselineCheck([]probe.ListenPort{dns, client, tcpHigh})
	if !ok || len(offending) != 1 || offending[0].Local != tcpHigh.Local {
		t.Errorf("BaselineCheck offending = %+v, want just the TCP listener", offending)
	}
}

func TestPortableBaselineRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")

	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	dnsClient := probe.ListenPort{Proto: "udp", Local: "10.0.0.1:47572", PID: 2, Process: "firefox"}
	backdoor := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:4444", PID: 66, Process: "nc"}

	n, err := SaveBaselineFile(path, "golden-host", []probe.ListenPort{sshd, dnsClient})
	if err != nil {
		t.Fatalf("SaveBaselineFile: %v", err)
	}
	// The ephemeral UDP socket is excluded, exactly as in the interactive path.
	if n != 1 {
		t.Errorf("wrote %d listeners, want 1", n)
	}

	offending, savedAt, err := BaselineCheckFile(path, []probe.ListenPort{sshd, backdoor})
	if err != nil {
		t.Fatalf("BaselineCheckFile: %v", err)
	}
	if len(offending) != 1 || offending[0].Local != backdoor.Local {
		t.Errorf("offending = %+v, want just the backdoor", offending)
	}
	if savedAt.IsZero() {
		t.Error("baseline file lost its timestamp")
	}

	if _, _, err := BaselineCheckFile(filepath.Join(t.TempDir(), "nope.json"), nil); err == nil {
		t.Error("a missing baseline file should be an error, not an empty baseline")
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := BaselineCheckFile(bad, nil); err == nil {
		t.Error("a baseline with no listeners should be rejected rather than passing everything")
	}
}

func TestAcceptListenerIntoBaseline(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}
	newSvc := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:8080", PID: 2, Process: "app"}

	m := eventsModel()
	m.ports = []probe.ListenPort{sshd, newSvc}

	// Accepting one listener is meaningless before a baseline exists.
	if msg, ok := m.acceptListener(newSvc); ok || !strings.Contains(msg, "press b") {
		t.Errorf("accept without a baseline = (%q, %v), want a refusal that says what to do", msg, ok)
	}

	m.ports = []probe.ListenPort{sshd}
	m.setBaseline()
	m.ports = []probe.ListenPort{sshd, newSvc}
	if !m.offBaseline(newSvc) {
		t.Fatal("the new service should start off-baseline")
	}

	// Accepting it blesses only that one listener.
	msg, ok := m.acceptListener(newSvc)
	if !ok || !strings.Contains(msg, "8080") {
		t.Fatalf("accept = (%q, %v)", msg, ok)
	}
	if m.offBaseline(newSvc) {
		t.Error("accepted listener is still off-baseline")
	}
	if len(m.baselineRaw) != 2 {
		t.Errorf("baseline holds %d entries, want 2", len(m.baselineRaw))
	}

	// Pressing it again takes it back out.
	if msg, ok := m.acceptListener(newSvc); !ok || !strings.Contains(msg, "Removed") {
		t.Errorf("second accept = (%q, %v), want a removal", msg, ok)
	}
	if !m.offBaseline(newSvc) {
		t.Error("removed listener should be off-baseline again")
	}

	// It survives a restart.
	m.ports = []probe.ListenPort{sshd, newSvc}
	m.acceptListener(newSvc)
	reloaded := NewModel().WithConfig()
	if reloaded.offBaseline(newSvc) {
		t.Error("accepted listener did not survive a reload")
	}

	// Ephemeral UDP sockets are not part of the service surface.
	udp := probe.ListenPort{Proto: "udp", Local: "10.0.0.1:47572", PID: 3, Process: "firefox"}
	if msg, ok := m.acceptListener(udp); ok || !strings.Contains(msg, "Ephemeral") {
		t.Errorf("accepting an ephemeral socket = (%q, %v), want a refusal", msg, ok)
	}
}

func TestAcceptListenerKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := eventsModel()
	m.activeTab = tabPorts
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}}
	m.setBaseline()
	m.ports = append(m.ports, probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:9999", PID: 2, Process: "app"})
	m.portsSortCol = indexOfCol(t, portsSortCols, "port")

	// Put the cursor on the off-baseline row and accept it.
	rows := m.filteredPorts()
	for i, p := range rows {
		if strings.Contains(p.Local, "9999") {
			m.portsCursor = i
		}
	}
	model, _ := m.Update(key("a"))
	m = model.(Model)
	if !strings.Contains(m.status, "Accepted") {
		t.Errorf("status = %q, want an acceptance", m.status)
	}
	if m.offBaselineCount() != 0 {
		t.Errorf("off-baseline count = %d, want 0 after accepting", m.offBaselineCount())
	}
}

func TestContainerListenersStayOutOfBaseline(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	m := feed(t, eventsModel(), netnsPorts(), nil)
	m.setBaseline()
	for _, k := range m.baselineRaw {
		if strings.Contains(k, "4f1a2b3c4d5e") || strings.Contains(k, "9e8d7c6b5a4f") {
			t.Errorf("baseline holds container listener %q", k)
		}
	}
	for _, p := range netnsPorts() {
		if m.offBaseline(p) {
			t.Errorf("%s %s reported off-baseline", p.Local, p.NetNS)
		}
	}
	if msg, ok := m.acceptListener(netnsPorts()[3]); ok || !strings.Contains(msg, "container") {
		t.Errorf("accepting a container listener: %q, %v", msg, ok)
	}
}

// A daemon started onto a host already listening outside its baseline must
// say so once, at alert level, so the hook fires — the first poll is otherwise
// silent by design.
func TestBaselineDriftOnFirstPoll(t *testing.T) {
	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd", Unit: "ssh.service"}
	web := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:8080", PID: 7, Process: "nginx", Unit: "nginx.service"}

	m := eventsModel()
	m.baseline = map[string]bool{portKey(sshd): true}
	m.baselineAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	m = feed(t, m, []probe.ListenPort{sshd, web}, nil)

	if n := countKind(m.events, "baseline_drift"); n != 1 {
		t.Fatalf("baseline_drift events = %d, want 1 (kinds: %v)", n, kindsOf(m.events))
	}
	if n := countKind(m.events, "listener_up"); n != 0 {
		t.Errorf("first poll produced %d listener_up events, want 0", n)
	}
	e := m.events[len(m.events)-1]
	if e.Level != levelAlert {
		t.Errorf("drift level = %v, want alert", e.Level)
	}
	if !strings.HasPrefix(e.Subject, "1 listener ") {
		t.Errorf("subject = %q, want it to count one listener", e.Subject)
	}
	for _, want := range []string{"tcp 0.0.0.0:8080", "nginx.service", "2026-10-01"} {
		if !strings.Contains(e.Detail, want) {
			t.Errorf("detail %q is missing %q", e.Detail, want)
		}
	}
	if strings.Contains(e.Detail, ":22") {
		t.Errorf("detail %q names a listener that is in the baseline", e.Detail)
	}
	if m.alertCount != 1 {
		t.Errorf("alert count = %d, want 1", m.alertCount)
	}

	// Later polls report changes through listener_up as before, not drift again.
	m = feed(t, m, []probe.ListenPort{sshd, web}, nil)
	if n := countKind(m.events, "baseline_drift"); n != 1 {
		t.Errorf("baseline_drift raised again on a later poll (%d total)", n)
	}
}

// The drift alert is what --on-alert exists for; Update drains the queue into
// commands, so the queue is checked on the event path directly.
func TestBaselineDriftQueuesAlertHook(t *testing.T) {
	m := eventsModel()
	m.alertHook = "/bin/true"
	m.baseline = map[string]bool{"tcp|0.0.0.0:22": true}
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:8080", PID: 7, Process: "nginx"}}

	m.noteSocketEvents(nil, nil, true, time.Now())

	if len(m.pendingHooks) != 1 || m.pendingHooks[0].Kind != "baseline_drift" {
		t.Errorf("pending hooks = %+v, want the drift alert queued", m.pendingHooks)
	}
}

func TestBaselineDriftCapsTheList(t *testing.T) {
	ports := make([]probe.ListenPort, 0, 8)
	for i := 0; i < 8; i++ {
		ports = append(ports, probe.ListenPort{Proto: "tcp", Local: fmt.Sprintf("0.0.0.0:%d", 9000+i), PID: int32(100 + i), Process: "svc"})
	}
	m := eventsModel()
	m.baseline = map[string]bool{"tcp|0.0.0.0:22": true}

	m = feed(t, m, ports, nil)

	if n := countKind(m.events, "baseline_drift"); n != 1 {
		t.Fatalf("baseline_drift events = %d, want 1", n)
	}
	e := m.events[len(m.events)-1]
	if !strings.HasPrefix(e.Subject, "8 listeners ") {
		t.Errorf("subject = %q, want 8 listeners", e.Subject)
	}
	if !strings.Contains(e.Detail, "3 more") || strings.Contains(e.Detail, ":9005") {
		t.Errorf("detail %q should list five and summarise three", e.Detail)
	}
	if !strings.Contains(e.Detail, ":9000") {
		t.Errorf("detail %q should start from the lowest port", e.Detail)
	}
}

func TestNoBaselineDriftWithoutBaselineOrDrift(t *testing.T) {
	sshd := probe.ListenPort{Proto: "tcp", Local: "0.0.0.0:22", PID: 1, Process: "sshd"}

	if m := feed(t, eventsModel(), []probe.ListenPort{sshd}, nil); countKind(m.events, "baseline_drift") != 0 {
		t.Error("drift raised with no baseline saved")
	}

	m := eventsModel()
	m.baseline = map[string]bool{portKey(sshd): true}
	if m = feed(t, m, []probe.ListenPort{sshd}, nil); countKind(m.events, "baseline_drift") != 0 {
		t.Error("drift raised for a host matching its baseline")
	}
}
