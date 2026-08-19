package probe

import (
	"os"
	"strconv"
	"strings"
)

// containerIDLen is how much of a container's 64-hex id is kept for display —
// the same short form docker/podman themselves show.
const containerIDLen = 12

// ContainerID returns a short container identifier for a PID, or "" when the
// process is not in a recognised container (or /proc is unreadable). Reading a
// single cgroup file is cheap enough to do per displayed process.
func ContainerID(pid int32) string {
	if pid <= 0 {
		return ""
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(int(pid)) + "/cgroup")
	if err != nil {
		return ""
	}
	return parseCgroupContainer(string(b))
}

// parseCgroupContainer extracts a container id from the contents of
// /proc/<pid>/cgroup. Docker, podman, containerd and CRI-O all embed the full
// 64-hex container id in the cgroup path (under varying prefixes), so the hex
// run is what we look for; LXC uses a plain name instead.
func parseCgroupContainer(contents string) string {
	for _, line := range strings.Split(contents, "\n") {
		// Format: hierarchy-ID:controller-list:cgroup-path
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]

		for _, seg := range strings.Split(path, "/") {
			// Strip the systemd scope wrapper and any runtime prefix, e.g.
			// "docker-<id>.scope", "libpod-<id>.scope", "cri-containerd-<id>.scope".
			seg = strings.TrimSuffix(seg, ".scope")
			seg = strings.TrimSuffix(seg, ".slice")
			if i := strings.LastIndex(seg, "-"); i >= 0 {
				seg = seg[i+1:]
			}
			if isHex64(seg) {
				return seg[:containerIDLen]
			}
		}

		if name := lxcName(path); name != "" {
			return name
		}
	}
	return ""
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// lxcName pulls the container name out of an LXC cgroup path ("/lxc/web01").
func lxcName(path string) string {
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		if seg == "lxc" && i+1 < len(segs) && segs[i+1] != "" {
			return segs[i+1]
		}
	}
	return ""
}
