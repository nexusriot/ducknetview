//go:build !linux

package probe

// Elsewhere there is no /proc and no rtnetlink, so the neighbour table is
// simply unavailable rather than wrong.
func readNeighbourCache() ([]Neighbour, error) {
	return readNeighbours(procNetARP)
}
