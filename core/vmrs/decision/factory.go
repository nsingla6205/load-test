// This file defines the factory function to create a DecisionMaker instance based on the VMRSConfig.

package decision

import (
	"errors"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
)

// NewDecisionMaker creates a DecisionMaker instance based on the VM selection strategy specified in the VMRSConfig.
// It supports two strategies: LeastCostSingleVM for regular deployments and LeastCostLargeVolumeCluster for large volume (FlexGroup) deployments.
// The catalog is scoped to the process-wide MACHINE_FAMILY_TYPE (vmrs.MachineFamily).
// Returns an error if the config is nil or specifies an unsupported strategy.
func NewDecisionMaker(config *vmrs.VMRSConfig) (vmrs.DecisionMaker, error) {
	return NewDecisionMakerForFamily(config, vmrs.MachineFamily)
}

// NewDecisionMakerForFamily is like NewDecisionMaker but scopes catalog selection
// to the supplied machine family instead of the process-wide MACHINE_FAMILY_TYPE.
// Pool updates use this to keep an existing pool on the machine family it was
// created with, even after the region's MACHINE_FAMILY_TYPE has been re-pointed
// at a different family. Passing vmrs.MachineFamilyUnset preserves the unfiltered
// legacy behavior.
func NewDecisionMakerForFamily(config *vmrs.VMRSConfig, family vmrs.MachineFamilyType) (vmrs.DecisionMaker, error) {
	if config == nil {
		return nil, errors.New("VMRSConfig cannot be nil")
	}

	switch config.HyperscalerPerfLimits.VMSelectionStrategy {
	case vmrs.LeastCostSingleVM:
		return NewLeastCostSingleVMDecisionMakerForFamily(config, family), nil
	case vmrs.LeastCostLargeVolumeCluster:
		return NewLeastCostLargeVolumeClusterDecisionMakerForFamily(config, family), nil
	default:
		return nil, &vmrs.InvalidConfigError{
			Message: fmt.Sprintf("unsupported VM selection strategy: %s", config.HyperscalerPerfLimits.VMSelectionStrategy),
		}
	}
}
