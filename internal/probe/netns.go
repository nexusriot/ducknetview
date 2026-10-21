package probe

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	gnet "github.com/shirou/gopsutil/v4/net"
)

// gopsutil reads /proc/net/*, which is the socket table of ducknetview's own
// network namespace. A container on a bridge network has a namespace of its
// own, so everything it listens on or talks to was invisible apart from the
// docker-proxy that fronts a published port. /proc/<pid>/net/* is the same
// table seen from that pid's namespace, and reading it needs no setns — so the
// other namespaces are enumerated by grouping processes by namespace and
// reading one member's files per group.

// maxNetNS caps how many foreign namespaces one poll enumerates. A CI runner or
// a Kubernetes node can carry hundreds, each costing four file reads plus an fd
// walk of its members; past the cap the rest are reported, not scanned.
const maxNetNS = 32

// namedNetNSDir is where `ip netns add` bind-mounts the namespaces it names.
const namedNetNSDir = "/run/netns"

// HostNetNSLabel is what the NETNS column shows for ducknetview's own
// namespace, whose rows carry an empty NetNS.
const HostNetNSLabel = "host"

// NetNSStatus summarises the foreign-namespace pass of one poll, so the UI can
// say what it could not see instead of silently showing less.
type NetNSStatus struct {
	Seen    int // foreign namespaces found
	Scanned int // of those, how many were enumerated (at most maxNetNS)

	// Limited explains why namespaced data is incomplete ("" when it is not):
	// unreadable namespace links, no privileges for byte counters, the cap.
	Limited string
}

// netNamespace is one foreign network namespace and the processes in it.
type netNamespace struct {
	Inode uint64
	PIDs  []int32 // ascending
	Label string
}

// parseNetNSLink extracts the inode from a /proc/<pid>/ns/net link target,
// which reads "net:[4026531840]".
func parseNetNSLink(link string) (uint64, bool) {
	rest, ok := strings.CutPrefix(link, "net:[")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, "]")
	if !ok {
		return 0, false
	}
	ino, err := strconv.ParseUint(rest, 10, 64)
	return ino, err == nil
}

// netNSOf returns the network namespace inode of a pid under root.
func netNSOf(root string, pid string) (uint64, error) {
	link, err := os.Readlink(filepath.Join(root, pid, "ns", "net"))
	if err != nil {
		return 0, err
	}
	ino, ok := parseNetNSLink(link)
	if !ok {
		return 0, errors.New("unrecognised netns link " + link)
	}
	return ino, nil
}

// groupByNetNS walks root (normally /proc) once and groups every pid by
// network namespace. denied counts processes whose namespace link could not be
// read: without privileges that is every other user's process, and it is how
// the caller knows the picture is partial.
func groupByNetNS(root string) (groups map[uint64][]int32, denied int) {
	groups = make(map[uint64][]int32, 8)
	entries, err := os.ReadDir(root)
	if err != nil {
		return groups, 0
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		ino, err := netNSOf(root, e.Name())
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				denied++
			}
			continue // also: the process exited mid-walk
		}
		groups[ino] = append(groups[ino], int32(pid))
	}
	for _, pids := range groups {
		sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	}
	return groups, denied
}

// foreignNamespaces turns the grouping into the namespaces to enumerate:
// everything except self, ordered by lowest member pid (a stable order, so the
// cap drops the same namespaces each poll rather than a random subset).
func foreignNamespaces(groups map[uint64][]int32, self uint64) []netNamespace {
	out := make([]netNamespace, 0, len(groups))
	for ino, pids := range groups {
		if ino == self || len(pids) == 0 {
			continue
		}
		out = append(out, netNamespace{Inode: ino, PIDs: pids})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PIDs[0] < out[j].PIDs[0] })
	return out
}

// netNSLabel names a namespace for display: the container its members run in
// when the cgroup says, else the `ip netns` name, else the bare inode.
func netNSLabel(root string, ns netNamespace, named map[uint64]string) string {
	for _, pid := range ns.PIDs {
		b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(int(pid)), "cgroup"))
		if err != nil {
			continue
		}
		if id := parseCgroupContainer(string(b)); id != "" {
			return id
		}
	}
	if name := named[ns.Inode]; name != "" {
		return name
	}
	return "netns:" + strconv.FormatUint(ns.Inode, 10)
}

// procNetFiles are the per-namespace socket tables enumerated, with the
// address family and socket type each one holds.
var procNetFiles = []struct {
	name     string
	family   uint32
	sockType uint32
}{
	{"tcp", syscall.AF_INET, syscall.SOCK_STREAM},
	{"tcp6", syscall.AF_INET6, syscall.SOCK_STREAM},
	{"udp", syscall.AF_INET, syscall.SOCK_DGRAM},
	{"udp6", syscall.AF_INET6, syscall.SOCK_DGRAM},
}

// tcpStates maps /proc/net/tcp's hex state column to the names gopsutil uses,
// so namespaced rows are indistinguishable from host rows downstream.
var tcpStates = map[string]string{
	"01": "ESTABLISHED", "02": "SYN_SENT", "03": "SYN_RECV", "04": "FIN_WAIT1",
	"05": "FIN_WAIT2", "06": "TIME_WAIT", "07": "CLOSE", "08": "CLOSE_WAIT",
	"09": "LAST_ACK", "0A": "LISTEN", "0B": "CLOSING",
}

// procSock is one row of a /proc/net/{tcp,udp}[6] table.
type procSock struct {
	stat  gnet.ConnectionStat
	inode string
}

// parseProcNet parses a /proc/net/{tcp,tcp6,udp,udp6} table. Rows that do not
// parse are skipped, as gopsutil does: one odd line must not lose the table.
func parseProcNet(data []byte, family, sockType uint32) []procSock {
	var out []procSock
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	first := true
	for sc.Scan() {
		if first { // header
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 10 {
			continue
		}
		la, err := decodeProcAddr(family, f[1])
		if err != nil {
			continue
		}
		ra, err := decodeProcAddr(family, f[2])
		if err != nil {
			continue
		}
		status := "NONE"
		if sockType == syscall.SOCK_STREAM {
			status = tcpStates[f[3]]
		}
		out = append(out, procSock{
			stat: gnet.ConnectionStat{
				Family: family, Type: sockType,
				Laddr: la, Raddr: ra, Status: status,
			},
			inode: f[9],
		})
	}
	return out
}

// decodeProcAddr decodes "0100007F:0035". The kernel prints each 32-bit word
// of the address with %08X of its in-memory value, so on a little-endian host
// every word reads byte-reversed.
func decodeProcAddr(family uint32, s string) (gnet.Addr, error) {
	host, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return gnet.Addr{}, errors.New("no port in " + s)
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return gnet.Addr{}, err
	}
	raw, err := hex.DecodeString(host)
	if err != nil {
		return gnet.Addr{}, err
	}
	want := 4
	if family == syscall.AF_INET6 {
		want = 16
	}
	if len(raw) != want {
		return gnet.Addr{}, errors.New("bad address length in " + s)
	}
	ip := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.NativeEndian.PutUint32(ip[i:], binary.BigEndian.Uint32(raw[i:]))
	}
	return gnet.Addr{IP: ip.String(), Port: uint32(port)}, nil
}

// socketOwners maps socket inodes to the lowest pid holding them, from the
// members' /proc/<pid>/fd links — the same attribution gopsutil does for the
// host namespace, restricted to this namespace's processes.
func socketOwners(root string, pids []int32) map[string]int32 {
	owners := make(map[string]int32, 32)
	for _, pid := range pids {
		dir := filepath.Join(root, strconv.Itoa(int(pid)), "fd")
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue // another user's process, or it exited
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(dir, fd.Name()))
			if err != nil {
				continue
			}
			ino, ok := strings.CutPrefix(link, "socket:[")
			if !ok {
				continue
			}
			ino = strings.TrimSuffix(ino, "]")
			if _, seen := owners[ino]; !seen {
				owners[ino] = pid
			}
		}
	}
	return owners
}

// namespaceSockets enumerates a foreign namespace's sockets through one
// member's /proc/<pid>/net tables, attributed to the namespace's processes.
// The first member whose tables are readable is used; they all show the same
// namespace.
func namespaceSockets(root string, ns netNamespace) []gnet.ConnectionStat {
	var socks []procSock
	for _, pid := range ns.PIDs {
		base := filepath.Join(root, strconv.Itoa(int(pid)), "net")
		got, any := []procSock(nil), false
		for _, f := range procNetFiles {
			b, err := os.ReadFile(filepath.Join(base, f.name))
			if err != nil {
				continue // tcp6/udp6 are absent when IPv6 is disabled
			}
			any = true
			got = append(got, parseProcNet(b, f.family, f.sockType)...)
		}
		if any {
			socks = got
			break
		}
	}
	if len(socks) == 0 {
		return nil
	}

	owners := socketOwners(root, ns.PIDs)
	out := make([]gnet.ConnectionStat, 0, len(socks))
	for _, s := range socks {
		st := s.stat
		st.Pid = owners[s.inode]
		out = append(out, st)
	}
	return out
}

// namespaceAddrs lists the local addresses of a namespace, from one member's
// fib_trie (IPv4 "/32 host LOCAL" entries) and if_inet6. It is how a
// container's listener is matched to the docker-proxy publishing it.
func namespaceAddrs(root string, pid int32) map[string]bool {
	base := filepath.Join(root, strconv.Itoa(int(pid)), "net")
	addrs := map[string]bool{}
	if b, err := os.ReadFile(filepath.Join(base, "fib_trie")); err == nil {
		for a := range parseFibTrieLocal(b) {
			addrs[a] = true
		}
	}
	if b, err := os.ReadFile(filepath.Join(base, "if_inet6")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 0 || len(f[0]) != 32 {
				continue
			}
			raw, err := hex.DecodeString(f[0])
			if err != nil {
				continue
			}
			addrs[net.IP(raw).String()] = true
		}
	}
	return addrs
}

// parseFibTrieLocal extracts local IPv4 addresses from /proc/net/fib_trie: an
// address line ("|-- 172.17.0.2") followed by "/32 host LOCAL".
func parseFibTrieLocal(data []byte) map[string]bool {
	out := map[string]bool{}
	last := ""
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if a, ok := strings.CutPrefix(t, "|-- "); ok {
			last = a
			continue
		}
		if strings.HasPrefix(t, "/32 host LOCAL") && last != "" {
			out[last] = true
		}
	}
	return out
}

// publication is one port docker-proxy forwards from the host into a
// container.
type publication struct {
	proto         string
	host          string // host "ip:port"
	containerIP   string
	containerPort int
}

// parseDockerProxyArgs reads docker-proxy's command line, e.g.
//
//	docker-proxy -proto tcp -host-ip 0.0.0.0 -host-port 8080 -container-ip 172.17.0.2 -container-port 80
//
// ok is false when the arguments are not a complete forwarding.
func parseDockerProxyArgs(args []string) (publication, bool) {
	var p publication
	hostIP, hostPort := "", ""
	for i := 0; i+1 < len(args); i++ {
		v := args[i+1]
		switch strings.TrimLeft(args[i], "-") {
		case "proto":
			p.proto = v
		case "host-ip":
			hostIP = v
		case "host-port":
			hostPort = v
		case "container-ip":
			p.containerIP = v
		case "container-port":
			p.containerPort, _ = strconv.Atoi(v)
		default:
			continue
		}
		i++
	}
	if p.proto == "" || hostIP == "" || hostPort == "" || p.containerIP == "" || p.containerPort == 0 {
		return publication{}, false
	}
	if ip := net.ParseIP(p.containerIP); ip != nil {
		p.containerIP = ip.String()
	}
	p.host = hostIP + ":" + hostPort
	return p, true
}

// hostPublications collects the forwardings of every docker-proxy among the
// host namespace's listeners. Docker run with userland-proxy=false publishes
// through iptables alone; those ports cannot be seen this way, and their
// container listeners stay classed as namespace-internal.
func hostPublications(root string, ports []ListenPort) []publication {
	var out []publication
	seen := map[int32]bool{}
	for _, p := range ports {
		if p.Process != "docker-proxy" || p.PID <= 0 || seen[p.PID] {
			continue
		}
		seen[p.PID] = true
		b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(int(p.PID)), "cmdline"))
		if err != nil {
			continue
		}
		if pub, ok := parseDockerProxyArgs(strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")); ok {
			out = append(out, pub)
		}
	}
	return out
}

// publishedAs returns the host address a namespaced listener is published on,
// "" when it is not. A wildcard bind accepts the container address; a specific
// bind must be that address.
func publishedAs(p ListenPort, addrs map[string]bool, pubs []publication) string {
	host, port := SplitHostPort(p.Local)
	bindIP := net.ParseIP(host)
	for _, pub := range pubs {
		if pub.proto != p.Proto || pub.containerPort != port || !addrs[pub.containerIP] {
			continue
		}
		if bindIP == nil || bindIP.IsUnspecified() || bindIP.String() == pub.containerIP {
			return pub.host
		}
	}
	return ""
}
