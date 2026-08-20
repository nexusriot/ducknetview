package probe

import (
	"encoding/json"
	"strings"
)

type IfaceKind int

const (
	IfaceUnknown IfaceKind = iota
	IfaceLoopback
	IfaceDockerBridge
	IfaceLinuxBridge
	IfaceVeth
	IfaceTunTap
	IfaceVirt
	IfacePhysical
)

// Label returns a short human-readable name for the interface kind.
func (k IfaceKind) Label() string {
	switch k {
	case IfaceLoopback:
		return "loopback"
	case IfaceDockerBridge:
		return "docker"
	case IfaceLinuxBridge:
		return "bridge"
	case IfaceVeth:
		return "veth"
	case IfaceTunTap:
		return "tun/tap"
	case IfaceVirt:
		return "virtual"
	case IfacePhysical:
		return "physical"
	default:
		return "unknown"
	}
}

// MarshalJSON emits the label rather than the enum's integer value, so a
// snapshot consumed by another tool (or fed back with --from) is self-
// describing.
func (k IfaceKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.Label())
}

// UnmarshalJSON accepts the label form MarshalJSON writes, and tolerates the
// bare integer that older snapshots contain.
func (k *IfaceKind) UnmarshalJSON(b []byte) error {
	var label string
	if err := json.Unmarshal(b, &label); err == nil {
		*k = kindByLabel(label)
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	if n < 0 || n > int(IfacePhysical) {
		n = int(IfaceUnknown)
	}
	*k = IfaceKind(n)
	return nil
}

// kindByLabel is the inverse of Label.
func kindByLabel(label string) IfaceKind {
	for k := IfaceUnknown; k <= IfacePhysical; k++ {
		if k.Label() == label {
			return k
		}
	}
	return IfaceUnknown
}

func ClassifyIface(name string) IfaceKind {
	switch {
	case name == "lo" || strings.HasPrefix(name, "lo"):
		return IfaceLoopback
	case name == "docker0" || strings.HasPrefix(name, "docker"):
		return IfaceDockerBridge
	case strings.HasPrefix(name, "br-") || name == "virbr0" || strings.HasPrefix(name, "virbr"):
		return IfaceLinuxBridge
	case strings.HasPrefix(name, "veth"):
		return IfaceVeth
	case strings.HasPrefix(name, "tun") || strings.HasPrefix(name, "tap"):
		return IfaceTunTap
	case strings.HasPrefix(name, "wg"):
		return IfaceVirt
	case strings.HasPrefix(name, "en") || strings.HasPrefix(name, "eth") || strings.HasPrefix(name, "wl"):
		return IfacePhysical
	default:
		return IfaceUnknown
	}
}
