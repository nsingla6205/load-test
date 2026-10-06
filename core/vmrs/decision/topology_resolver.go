package decision

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
)

// activeActiveFloorByFamily returns the base machine type an active-active pool may use in each
// machine family it is qualified on.
var activeActiveFloorByFamily = map[vmrs.MachineFamilyType]string{
	vmrs.MachineFamilyC3LSSD:    "c3-standard-22-lssd",
	vmrs.MachineFamilyC3NonLSSD: "c3-standard-22",
	vmrs.MachineFamilyC4LSSD:    "c4-standard-24-lssd",
	vmrs.MachineFamilyC4NonLSSD: "c4-standard-24",
}

type scalingTopologyKey struct {
	isTrueZonal     bool
	isLargeCapacity bool
}

var topologyByPool = map[scalingTopologyKey]vmrs.DeploymentTopology{
	{isTrueZonal: false, isLargeCapacity: false}: vmrs.TopologyActivePassive,
	{isTrueZonal: false, isLargeCapacity: true}:  vmrs.TopologyActivePassive,
	{isTrueZonal: true, isLargeCapacity: false}:  vmrs.TopologyZonalActivePassive,
	{isTrueZonal: true, isLargeCapacity: true}:   vmrs.TopologyActiveActive,
}

// ActiveActiveVMSelectionPolicy returns the VM candidate restriction an active-active pool
// deploying into family must satisfy.
func ActiveActiveVMSelectionPolicy(family vmrs.MachineFamilyType) vmrs.VMSelectionPolicy {
	return vmrs.VMSelectionPolicy{MinimumMachineType: activeActiveFloorByFamily[family]}
}

// ResolveScalingTopology derives the ScalingTopology for a pool from its true-zonal
// flag and whether it is large-capacity.
func ResolveScalingTopology(isTrueZonal, isLargeCapacity bool) vmrs.ScalingTopology {
	return vmrs.ScalingTopology{
		Topology: topologyByPool[scalingTopologyKey{isTrueZonal: isTrueZonal, isLargeCapacity: isLargeCapacity}],
	}
}
