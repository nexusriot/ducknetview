//go:build !linux

package probe

import "errors"

// Network namespaces are a Linux concept; elsewhere there is only the one
// socket table gopsutil already reads.

func ownNetNS() (uint64, error) { return 0, errors.ErrUnsupported }

func namedNetNS(string) map[uint64]string { return nil }

var errNetNSPrivilege = errors.ErrUnsupported

func nsSockStats(int32) (map[string]SockStat, error) { return nil, errors.ErrUnsupported }
