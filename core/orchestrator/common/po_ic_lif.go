package common

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

// IsICLifSetComplete reports whether existingLifs covers every node in nodeIDs
// with exactly one active LIF per node (no missing, no duplicates, no extras).
func IsICLifSetComplete(existingLifs []*datamodel.Lif, nodeIDs map[string]int64) bool {
	if len(nodeIDs) == 0 || len(existingLifs) != len(nodeIDs) {
		return false
	}
	counts := make(map[int64]int, len(nodeIDs))
	for _, lif := range existingLifs {
		if lif == nil {
			return false
		}
		counts[lif.NodeID]++
	}
	// If every nodeID has exactly one LIF and lif-count == nodeID-count,
	// counts cannot contain extras. No further check needed.
	for _, id := range nodeIDs {
		if counts[id] != 1 {
			return false
		}
	}
	return true
}
