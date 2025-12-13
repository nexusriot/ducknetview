package probe

import "strings"

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
