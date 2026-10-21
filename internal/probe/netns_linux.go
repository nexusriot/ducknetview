//go:build linux

package probe

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// ownNetNS is the inode of ducknetview's own network namespace.
func ownNetNS() (uint64, error) {
	return netNSOf("/proc", "self")
}

// namedNetNS maps namespace inodes to their `ip netns` names. A missing
// directory is the normal case and yields an empty map.
func namedNetNS(dir string) map[uint64]string {
	out := map[uint64]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		fi, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			out[st.Ino] = e.Name()
		}
	}
	return out
}

// errNetNSPrivilege is returned when this process may not enter another
// namespace, which needs CAP_SYS_ADMIN.
var errNetNSPrivilege = errors.New("entering another network namespace needs root")

// nsSockStats fetches the sock_diag counters of the namespace pid lives in.
// sock_diag only reports the namespace its socket was created in, so the
// netlink socket is opened after setns on a locked OS thread; the thread then
// returns to its own namespace before the (slower) dump runs. If it cannot
// return, the thread is left locked so the Go runtime retires it with the
// goroutine instead of handing a thread in the wrong namespace to other code.
func nsSockStats(pid int32) (map[string]SockStat, error) {
	if os.Geteuid() != 0 {
		return nil, errNetNSPrivilege
	}

	type result struct {
		fd  int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		fd, restored, err := openDiagSocketIn(pid)
		if restored {
			runtime.UnlockOSThread()
		}
		ch <- result{fd, err}
	}()
	r := <-ch
	if r.err != nil {
		return nil, r.err
	}
	defer syscall.Close(r.fd)

	out := make(map[string]SockStat, 32)
	var firstErr error
	for _, family := range []uint8{syscall.AF_INET, syscall.AF_INET6} {
		if err := dumpTCPDiagOn(r.fd, family, out); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// openDiagSocketIn must run on a locked OS thread. restored reports whether the
// thread is back in its original namespace (true when it never left).
func openDiagSocketIn(pid int32) (fd int, restored bool, err error) {
	self, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		return -1, true, err
	}
	defer self.Close()
	target, err := os.Open("/proc/" + strconv.Itoa(int(pid)) + "/ns/net")
	if err != nil {
		return -1, true, err
	}
	defer target.Close()

	if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
		if errors.Is(err, syscall.EPERM) {
			err = errNetNSPrivilege
		}
		return -1, true, err
	}
	fd, err = openDiagSocket()
	if rerr := unix.Setns(int(self.Fd()), unix.CLONE_NEWNET); rerr != nil {
		if fd >= 0 {
			syscall.Close(fd)
		}
		return -1, false, rerr
	}
	return fd, true, err
}
