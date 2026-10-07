package decision

import (
	"encoding/json"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

const (
	defaultActiveActiveFloorByFamily = "{\"c3-lssd\":\"c3-standard-22-lssd\",\"c3-non-lssd\":\"c3-standard-22\",\"c4-lssd\":\"c4-standard-24-lssd\",\"c4-non-lssd\":\"c4-standard-24\"}"
)

// activeActiveFloorByFamily is the effective floor map, read once at process start.
var activeActiveFloorByFamily = loadActiveActiveFloorByFamily(env.GetString("ACTIVE_ACTIVE_POOL_FLOOR_BY_FAMILY", defaultActiveActiveFloorByFamily))

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
// deploying into family must satisfy. The floor is the machine type recorded for family in
// activeActiveFloorByFamily.
func ActiveActiveVMSelectionPolicy(family vmrs.MachineFamilyType) vmrs.VMSelectionPolicy {
	return vmrs.VMSelectionPolicy{MinimumMachineType: activeActiveFloorByFamily[family]}
}

func loadActiveActiveFloorByFamily(raw string) map[vmrs.MachineFamilyType]string {
	floors := map[vmrs.MachineFamilyType]string{}
	if err := json.Unmarshal([]byte(raw), &floors); err != nil {
		log.NewLogger().Warn(
			"invalid ACTIVE_ACTIVE_FLOOR_BY_FAMILY; keeping default floors",
			"configured", raw,
			"error", err,
		)
		_ = json.Unmarshal([]byte(defaultActiveActiveFloorByFamily), &floors)
	}
	return floors
}

// ResolveScalingTopology derives the ScalingTopology for a pool from its true-zonal
// flag and whether it is large-capacity.
func ResolveScalingTopology(isTrueZonal, isLargeCapacity bool) vmrs.ScalingTopology {
	return vmrs.ScalingTopology{
		Topology: topologyByPool[scalingTopologyKey{isTrueZonal: isTrueZonal, isLargeCapacity: isLargeCapacity}],
	}
}
