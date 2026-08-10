package decision

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
)

const (
	machineFamilyC3        = "c3"
	floorMachineTypeC3LSSD = "c3-standard-22-lssd"
)

// activeActiveC3LSSDVMSelectionPolicy is the VM candidate restriction applied to
// (active-active) deployments: selection is constrained to the c3 machine family, must use
// local SSD, and must be at or above the c3-standard-22-lssd floor.
var activeActiveC3LSSDVMSelectionPolicy = vmrs.VMSelectionPolicy{
	EligibleFamilies:   []string{machineFamilyC3},
	MinimumMachineType: floorMachineTypeC3LSSD,
	RequireLSSD:        true,
}

// MachineFamilyForTopology returns the machine family VMRS catalog selection must be scoped
// to for topology, given the family the caller would otherwise use (the region's
// MACHINE_FAMILY_TYPE, or an existing pool's own family on an update).
//
// true-zonal pools are pinned to c3-lssd so the catalog scope cannot contradict
// activeActiveC3LSSDVMSelectionPolicy. Every other topology uses configured as-is.
func MachineFamilyForTopology(topology vmrs.DeploymentTopology, configuredMachineFamily vmrs.MachineFamilyType) vmrs.MachineFamilyType {
	if topology == vmrs.TopologyActiveActive {
		return vmrs.MachineFamilyC3LSSD
	}
	return configuredMachineFamily
}

// ResolveScalingTopology derives the ScalingTopology for a pool from its isActiveActive flag.
// True-zonal pools deploy active-active with restricted VM selection, all other pools deploy
// active-passive with the full VMRS catalog.
func ResolveScalingTopology(isActiveActive bool) vmrs.ScalingTopology {
	if isActiveActive {
		return vmrs.ScalingTopology{
			Topology:          vmrs.TopologyActiveActive,
			VMSelectionPolicy: activeActiveC3LSSDVMSelectionPolicy,
		}
	}

	// Active-passive pools impose no VM candidate restriction: VMSelectionPolicy is left as its
	// zero value, which IsUnrestricted() reports as true, so FilterCandidatesByPolicy considers
	// the full VMRS catalog.
	return vmrs.ScalingTopology{
		Topology: vmrs.TopologyActivePassive,
	}
}
