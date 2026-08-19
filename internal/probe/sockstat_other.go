//go:build !linux

package probe

// SockStats has no portable equivalent: per-socket byte counters come from
// Linux's sock_diag netlink interface. Elsewhere throughput columns stay empty
// rather than the tool refusing to run.
func SockStats() (map[string]SockStat, error) {
	return nil, nil
}
