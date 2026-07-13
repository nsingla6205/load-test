package decision

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
)

func TestResolveScalingTopology_TrueZonalIsActiveActive(t *testing.T) {
	policy := ResolveScalingTopology(true)

	assert.Equal(t, vmrs.TopologyActiveActive, policy.Topology)
	assert.False(t, policy.Topology.RequiresMediator(), "true-zonal deployments do not use a mediator")
	assert.True(t, policy.Topology.EnableAAConfig())
	assert.Equal(t, []string{"c3"}, policy.VMSelectionPolicy.EligibleFamilies)
	assert.Equal(t, "c3-standard-22-lssd", policy.VMSelectionPolicy.MinimumMachineType)
	assert.True(t, policy.VMSelectionPolicy.RequireLSSD)
}

func TestResolveScalingTopology_NonTrueZonalIsActivePassiveWithMediator(t *testing.T) {
	policy := ResolveScalingTopology(false)

	assert.Equal(t, vmrs.TopologyActivePassive, policy.Topology)
	assert.True(t, policy.Topology.RequiresMediator(), "active-passive deployments provision a mediator")
	assert.False(t, policy.Topology.EnableAAConfig())
	assert.True(t, policy.VMSelectionPolicy.IsUnrestricted(), "non-true-zonal imposes no VM restriction")
}
