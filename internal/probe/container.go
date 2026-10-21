package probe

import (
	"os"
	"strconv"
	"strings"
)

// containerIDLen is how much of a container's 64-hex id is kept for display —
// the same short form docker/podman themselves show.
const containerIDLen = 12

// CgroupInfo returns a PID's short container id and its systemd unit from a
// single read of /proc/<pid>/cgroup — both are encoded in the same path, so
// asking for them separately would only read the file twice. Either is "" when
// the process is not in one (or /proc is unreadable). One small file is cheap
// enough to read per displayed process.
func CgroupInfo(pid int32) (container, unit string) {
	if pid <= 0 {
		return "", ""
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(int(pid)) + "/cgroup")
	if err != nil {
		return "", ""
	}
	return parseCgroupContainer(string(b)), parseCgroupUnit(string(b))
}

// parseCgroupUnit extracts the systemd unit a process runs in — the innermost
// service or scope on its cgroup path ("nginx.service", "session-2.scope",
// "pipewire.service" under user@1000.service). Slices are grouping, not units
// anyone starts or stops, so they are skipped; a container's own scope is
// skipped too, because the CONTAINER column already says that better.
//
// The unified hierarchy ("0::") is preferred, then cgroup v1's name=systemd
// hierarchy, which is the one systemd lays out by unit; other v1 controllers
// are only a fallback, since they are often left at the root.
func parseCgroupUnit(contents string) string {
	var unified, named, other string
	for _, line := range strings.Split(contents, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		u := unitFromPath(parts[2])
		if u == "" {
			continue
		}
		switch {
		case parts[0] == "0" && parts[1] == "":
			unified = u
		case parts[1] == "name=systemd":
			named = u
		case other == "":
			other = u
		}
	}
	switch {
	case unified != "":
		return unified
	case named != "":
		return named
	}
	return other
}

// unitFromPath returns the innermost unit segment of one cgroup path. Segments
// below the unit (a service's own sub-cgroups, which systemd allows under
// delegation) are not units and are passed over.
func unitFromPath(path string) string {
	segs := strings.Split(path, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		seg := segs[i]
		if !strings.HasSuffix(seg, ".service") && !strings.HasSuffix(seg, ".scope") {
			continue
		}
		if isContainerScope(seg) {
			return ""
		}
		return seg
	}
	return ""
}

// isContainerScope reports a runtime's per-container scope, such as
// "docker-<id>.scope" or "cri-containerd-<id>.scope".
func isContainerScope(seg string) bool {
	seg = strings.TrimSuffix(seg, ".scope")
	if i := strings.LastIndex(seg, "-"); i >= 0 {
		seg = seg[i+1:]
	}
	return isHex64(seg)
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
