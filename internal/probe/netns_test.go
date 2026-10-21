//go:build linux

package probe

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// procFixture builds a fake /proc tree under a temp dir.
type procFixture struct {
	t    *testing.T
	root string
}

func newProcFixture(t *testing.T) *procFixture {
	return &procFixture{t: t, root: t.TempDir()}
}

func (f *procFixture) file(rel, contents string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *procFixture) link(rel, target string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		f.t.Fatal(err)
	}
}

// proc adds a process in namespace ino holding the given socket inodes.
func (f *procFixture) proc(pid int, ino uint64, sockets ...int) {
	f.link(fmt.Sprintf("%d/ns/net", pid), fmt.Sprintf("net:[%d]", ino))
	for i, s := range sockets {
		f.link(fmt.Sprintf("%d/fd/%d", pid, i+3), fmt.Sprintf("socket:[%d]", s))
	}
	// A non-socket fd, which attribution must skip.
	f.link(fmt.Sprintf("%d/fd/0", pid), "/dev/null")
}

const procNetHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

func procNetRow(local, remote, state string, inode int) string {
	return fmt.Sprintf("   0: %s %s %s 00000000:00000000 00:00000000 00000000     0        0 %d 1 0000000000000000 100 0 0 10 0\n",
		local, remote, state, inode)
}

// The fixtures spell addresses the way a little-endian kernel prints them.
func skipBigEndian(t *testing.T) {
	var b [2]byte
	binary.NativeEndian.PutUint16(b[:], 1)
	if b[0] != 1 {
		t.Skip("fixtures are written in little-endian /proc notation")
	}
}

func TestParseNetNSLink(t *testing.T) {
	for in, want := range map[string]uint64{
		"net:[4026531840]": 4026531840,
		"net:[1]":          1,
	} {
		if got, ok := parseNetNSLink(in); !ok || got != want {
			t.Errorf("parseNetNSLink(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "mnt:[4026531840]", "net:[]", "net:[x]", "net:[12"} {
		if _, ok := parseNetNSLink(bad); ok {
			t.Errorf("parseNetNSLink(%q) accepted a malformed link", bad)
		}
	}
}

func TestParseProcNet(t *testing.T) {
	skipBigEndian(t)

	tcp := procNetHeader +
		procNetRow("00000000:0050", "00000000:0000", "0A", 11) +
		procNetRow("020011AC:0050", "010011AC:15B3", "01", 12) +
		"   garbage line\n" +
		procNetRow("XYZ:0050", "00000000:0000", "0A", 13)
	got := parseProcNet([]byte(tcp), syscall.AF_INET, syscall.SOCK_STREAM)
	if len(got) != 2 {
		t.Fatalf("parsed %d rows, want 2 (malformed rows skipped): %+v", len(got), got)
	}
	if l := got[0].stat; l.Laddr.IP != "0.0.0.0" || l.Laddr.Port != 80 || l.Status != "LISTEN" || got[0].inode != "11" {
		t.Errorf("listener parsed as %+v inode %s", l, got[0].inode)
	}
	if c := got[1].stat; c.Laddr.IP != "172.17.0.2" || c.Raddr.IP != "172.17.0.1" || c.Raddr.Port != 5555 || c.Status != "ESTABLISHED" {
		t.Errorf("connection parsed as %+v", c)
	}

	tcp6 := procNetHeader +
		procNetRow("00000000000000000000000001000000:1F90", "00000000000000000000000000000000:0000", "0A", 21) +
		procNetRow("0000000000000000FFFF00000100007F:0050", "00000000000000000000000000000000:0000", "0A", 22)
	got = parseProcNet([]byte(tcp6), syscall.AF_INET6, syscall.SOCK_STREAM)
	if len(got) != 2 || got[0].stat.Laddr.IP != "::1" || got[0].stat.Laddr.Port != 8080 {
		t.Fatalf("tcp6 parsed as %+v", got)
	}
	// IPv4-mapped renders in dotted form, as gopsutil renders it.
	if got[1].stat.Laddr.IP != "127.0.0.1" {
		t.Errorf("v4-mapped address rendered as %q", got[1].stat.Laddr.IP)
	}

	udp := procNetHeader + procNetRow("00000000:0035", "00000000:0000", "07", 31)
	got = parseProcNet([]byte(udp), syscall.AF_INET, syscall.SOCK_DGRAM)
	if len(got) != 1 || got[0].stat.Status != "NONE" || got[0].stat.Type != syscall.SOCK_DGRAM {
		t.Errorf("udp parsed as %+v", got)
	}
}

func TestGroupByNetNS(t *testing.T) {
	f := newProcFixture(t)
	f.proc(1, 100)
	f.proc(7, 100)
	f.proc(42, 200)
	f.proc(40, 200)
	f.proc(50, 300)
	f.file("self/ignored", "") // non-numeric entries are not processes
	f.file("99/status", "")    // a process whose ns link has gone

	groups, denied := groupByNetNS(f.root)
	if denied != 0 {
		t.Errorf("denied = %d, want 0", denied)
	}
	if len(groups) != 3 || len(groups[100]) != 2 || fmt.Sprint(groups[200]) != "[40 42]" {
		t.Fatalf("groups = %v", groups)
	}

	foreign := foreignNamespaces(groups, 100)
	if len(foreign) != 2 || foreign[0].Inode != 200 || foreign[1].Inode != 300 {
		t.Errorf("foreign namespaces = %+v, want 200 then 300, self excluded", foreign)
	}
}

func TestNetNSLabel(t *testing.T) {
	f := newProcFixture(t)
	id := strings.Repeat("ab", 32)
	f.file("10/cgroup", "0::/system.slice/docker-"+id+".scope\n")
	f.file("20/cgroup", "0::/user.slice\n")

	if got := netNSLabel(f.root, netNamespace{Inode: 1, PIDs: []int32{5, 10}}, nil); got != id[:12] {
		t.Errorf("container namespace labelled %q, want %q", got, id[:12])
	}
	named := map[uint64]string{2: "blue"}
	if got := netNSLabel(f.root, netNamespace{Inode: 2, PIDs: []int32{20}}, named); got != "blue" {
		t.Errorf("named namespace labelled %q", got)
	}
	if got := netNSLabel(f.root, netNamespace{Inode: 3, PIDs: []int32{20}}, named); got != "netns:3" {
		t.Errorf("anonymous namespace labelled %q", got)
	}
}

func TestNamedNetNS(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blue"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(dir, "blue"))
	ino := fi.Sys().(*syscall.Stat_t).Ino
	if got := namedNetNS(dir); got[ino] != "blue" {
		t.Errorf("namedNetNS = %v, want inode %d named blue", got, ino)
	}
	if got := namedNetNS(filepath.Join(dir, "missing")); len(got) != 0 {
		t.Errorf("missing dir gave %v", got)
	}
}

func TestDockerProxyPublication(t *testing.T) {
	args := strings.Fields("/usr/bin/docker-proxy -proto tcp -host-ip 0.0.0.0 -host-port 8080 -container-ip 172.17.0.2 -container-port 80 -use-listen-fd")
	pub, ok := parseDockerProxyArgs(args)
	if !ok || pub.proto != "tcp" || pub.host != "0.0.0.0:8080" || pub.containerIP != "172.17.0.2" || pub.containerPort != 80 {
		t.Fatalf("parseDockerProxyArgs = %+v, %v", pub, ok)
	}
	if _, ok := parseDockerProxyArgs([]string{"docker-proxy", "-proto", "tcp"}); ok {
		t.Error("incomplete arguments accepted")
	}

	fib := []byte(`Main:
  +-- 0.0.0.0/0 3 0 5
     |-- 0.0.0.0
        /0 universe UNICAST
     +-- 172.17.0.0/16 2 0 2
        |-- 172.17.0.0
           /16 link UNICAST
        |-- 172.17.0.2
           /32 host LOCAL
        |-- 172.17.255.255
           /32 link BROADCAST
`)
	addrs := parseFibTrieLocal(fib)
	if !addrs["172.17.0.2"] || len(addrs) != 1 {
		t.Fatalf("fib_trie local addresses = %v", addrs)
	}

	pubs := []publication{pub}
	cases := []struct {
		port ListenPort
		want string
	}{
		{ListenPort{Proto: "tcp", Local: "0.0.0.0:80"}, "0.0.0.0:8080"},
		{ListenPort{Proto: "tcp", Local: "172.17.0.2:80"}, "0.0.0.0:8080"},
		{ListenPort{Proto: "tcp", Local: ":::80"}, "0.0.0.0:8080"},
		{ListenPort{Proto: "tcp", Local: "127.0.0.1:80"}, ""}, // loopback is not forwarded to
		{ListenPort{Proto: "udp", Local: "0.0.0.0:80"}, ""},   // other protocol
		{ListenPort{Proto: "tcp", Local: "0.0.0.0:81"}, ""},   // other port
	}
	for _, c := range cases {
		if got := publishedAs(c.port, addrs, pubs); got != c.want {
			t.Errorf("publishedAs(%s %s) = %q, want %q", c.port.Proto, c.port.Local, got, c.want)
		}
	}
	// A different container on the same port is not published by this proxy.
	if got := publishedAs(ListenPort{Proto: "tcp", Local: "0.0.0.0:80"}, map[string]bool{"172.17.0.3": true}, pubs); got != "" {
		t.Errorf("listener of another container matched: %q", got)
	}
}

func TestListenPortExposure(t *testing.T) {
	cases := []struct {
		p    ListenPort
		want string
	}{
		{ListenPort{Local: "0.0.0.0:80"}, "exposed"},
		{ListenPort{Local: "127.0.0.1:80"}, "local"},
		{ListenPort{Local: "192.168.1.5:80"}, "lan"},
		// Inside a namespace, a wildcard is only as exposed as the host makes it.
		{ListenPort{Local: "0.0.0.0:80", NetNS: "abc"}, "netns"},
		{ListenPort{Local: "172.17.0.2:80", NetNS: "abc"}, "netns"},
		{ListenPort{Local: "127.0.0.1:80", NetNS: "abc"}, "local"},
		{ListenPort{Local: "0.0.0.0:80", NetNS: "abc", Published: "0.0.0.0:8080"}, "exposed"},
		{ListenPort{Local: "0.0.0.0:80", NetNS: "abc", Published: "127.0.0.1:8080"}, "local"},
	}
	for _, c := range cases {
		if got := c.p.Exposure(); got != c.want {
			t.Errorf("%+v.Exposure() = %q, want %q", c.p, got, c.want)
		}
	}
}

// containerFixture is a host namespace (100) running a docker-proxy that
// publishes host 8080 to a container namespace (200) listening on 0.0.0.0:80
// and :9000 (unpublished), plus a connection into the container.
func containerFixture(t *testing.T) (*procFixture, string) {
	skipBigEndian(t)
	f := newProcFixture(t)
	id := strings.Repeat("cd", 32)

	f.proc(100, 1000)
	f.file("100/cmdline", strings.Join(strings.Fields(
		"docker-proxy -proto tcp -host-ip 0.0.0.0 -host-port 8080 -container-ip 172.17.0.2 -container-port 80"), "\x00")+"\x00")

	f.proc(200, 2000, 5001, 5002)
	f.proc(201, 2000, 5003)
	f.file("200/cgroup", "0::/system.slice/docker-"+id+".scope\n")
	f.file("200/net/tcp", procNetHeader+
		procNetRow("00000000:0050", "00000000:0000", "0A", 5001)+
		procNetRow("020011AC:0050", "010011AC:15B3", "01", 5002)+
		procNetRow("00000000:2328", "00000000:0000", "0A", 5003))
	f.file("200/net/udp", procNetHeader)
	f.file("200/net/fib_trie", "Local:\n  |-- 172.17.0.2\n     /32 host LOCAL\n")
	return f, id[:12]
}

func newTestBuilder() *connBuilder {
	none := func(int32) string { return "" }
	return &connBuilder{nameOf: none, userOf: none, procMap: map[int32]*ProcNet{}}
}

func stubNSStats(t *testing.T, fn func(int32) (map[string]SockStat, error)) {
	t.Helper()
	old := nsStats
	nsStats = fn
	t.Cleanup(func() { nsStats = old })
}

func TestAddNamespacesFixture(t *testing.T) {
	f, label := containerFixture(t)
	stubNSStats(t, func(pid int32) (map[string]SockStat, error) {
		if pid != 200 {
			t.Errorf("counters fetched through pid %d, want the namespace's first member 200", pid)
		}
		return map[string]SockStat{
			SockKey("172.17.0.2:80", "172.17.0.1:5555"): {TxBytes: 1000, RxBytes: 50},
		}, nil
	})

	b := newTestBuilder()
	// The host pass: docker-proxy listening on 8080.
	b.ports = append(b.ports, ListenPort{Proto: "tcp", Local: "0.0.0.0:8080", PID: 100, Process: "docker-proxy"})

	st := b.addNamespacesFrom(f.root, 1000, t.TempDir())
	if st.Seen != 1 || st.Scanned != 1 || st.Limited != "" {
		t.Errorf("status = %+v", st)
	}

	byPort := map[string]ListenPort{}
	for _, p := range b.ports {
		byPort[p.Local] = p
	}
	web, ok := byPort["0.0.0.0:80"]
	if !ok || web.NetNS != label || web.PID != 200 {
		t.Fatalf("container listener = %+v (ok=%v), want netns %s pid 200", web, ok, label)
	}
	if web.Published != "0.0.0.0:8080" || web.Exposure() != "exposed" {
		t.Errorf("published listener: Published=%q Exposure=%q", web.Published, web.Exposure())
	}
	// Attributed to the second member, and not published: inside only.
	if p := byPort["0.0.0.0:9000"]; p.PID != 201 || p.Published != "" || p.Exposure() != "netns" {
		t.Errorf("unpublished listener = %+v exposure %q", p, p.Exposure())
	}
	if host := byPort["0.0.0.0:8080"]; host.NetNS != "" || host.Exposure() != "exposed" {
		t.Errorf("host listener changed: %+v", host)
	}

	if len(b.conns) != 1 {
		t.Fatalf("conns = %+v", b.conns)
	}
	c := b.conns[0]
	if c.NetNS != label || c.State != "ESTABLISHED" || c.TxBytes != 1000 || c.RxBytes != 50 {
		t.Errorf("container connection = %+v", c)
	}
	if pn := b.procMap[200]; pn == nil || pn.ConnCount != 2 || pn.ListenCount != 1 {
		t.Errorf("process 200 counted as %+v", pn)
	}
}

func TestAddNamespacesWithoutPrivilege(t *testing.T) {
	f, _ := containerFixture(t)
	calls := 0
	stubNSStats(t, func(int32) (map[string]SockStat, error) {
		calls++
		return nil, errNetNSPrivilege
	})
	// A second container, so the refusal can be shown to stick for the poll.
	f.proc(300, 3000, 6001)
	f.file("300/net/tcp", procNetHeader+procNetRow("030011AC:0050", "010011AC:15B4", "01", 6001))

	b := newTestBuilder()
	st := b.addNamespacesFrom(f.root, 1000, t.TempDir())
	if calls != 1 {
		t.Errorf("setns attempted %d times after being refused, want 1", calls)
	}
	if len(b.conns) != 2 {
		t.Fatalf("rows dropped without privileges: %+v", b.conns)
	}
	for _, c := range b.conns {
		if c.TxBytes != 0 || c.RxBytes != 0 {
			t.Errorf("counters on %+v", c)
		}
	}
	if !strings.Contains(st.Limited, "2 TCP connections") || !strings.Contains(st.Limited, "root") {
		t.Errorf("Limited = %q, want the missing counters explained", st.Limited)
	}
	// No docker-proxy on the host this time: nothing is published.
	for _, p := range b.ports {
		if p.Published != "" {
			t.Errorf("%+v published without a proxy", p)
		}
	}
}

func TestAddNamespacesCap(t *testing.T) {
	skipBigEndian(t)
	stubNSStats(t, func(int32) (map[string]SockStat, error) { return nil, errors.New("unused") })
	f := newProcFixture(t)
	f.proc(1, 1000)
	for i := 0; i < maxNetNS+3; i++ {
		pid := 1000 + i
		f.proc(pid, uint64(5000+i), 7000+i)
		f.file(fmt.Sprintf("%d/net/tcp", pid), procNetHeader+procNetRow("00000000:0050", "00000000:0000", "0A", 7000+i))
	}

	b := newTestBuilder()
	st := b.addNamespacesFrom(f.root, 1000, t.TempDir())
	if st.Seen != maxNetNS+3 || st.Scanned != maxNetNS {
		t.Errorf("status = %+v, want %d seen and %d scanned", st, maxNetNS+3, maxNetNS)
	}
	if len(b.ports) != maxNetNS {
		t.Errorf("%d listeners enumerated, want %d", len(b.ports), maxNetNS)
	}
	if !strings.Contains(st.Limited, fmt.Sprintf("first %d of %d", maxNetNS, maxNetNS+3)) {
		t.Errorf("Limited = %q", st.Limited)
	}
}
