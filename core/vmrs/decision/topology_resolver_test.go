package decision

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
)

func TestResolveScalingTopology_LargeCapacityTrueZonalIsActiveActive(t *testing.T) {
	policy := ResolveScalingTopology(true, true)

	assert.Equal(t, vmrs.TopologyActiveActive, policy.Topology)
	assert.True(t, policy.Topology.UsesSharedHADeployment(), "true-zonal deployments are shared_ha")
	assert.False(t, policy.Topology.RequiresMediator(), "true-zonal deployments do not use a mediator")
	assert.True(t, policy.Topology.EnableAAConfig())
	assert.True(t, policy.VMSelectionPolicy.IsUnrestricted(),
		"the active-active floor is machine-family specific and is applied once the family is known")
}

// A regular zonal pool shares the mediator-less shared_ha deployment with active-active, but
// must not turn on active-active config and must not carry a VM selection restriction.
func TestResolveScalingTopology_RegularTrueZonalIsSharedHAActivePassive(t *testing.T) {
	policy := ResolveScalingTopology(true, false)

	assert.Equal(t, vmrs.TopologyZonalActivePassive, policy.Topology)
	assert.True(t, policy.Topology.UsesSharedHADeployment(), "regular zonal pools are shared_ha")
	assert.False(t, policy.Topology.RequiresMediator(), "shared_ha has no mediator VM")
	assert.False(t, policy.Topology.EnableAAConfig(), "regular zonal pools are active-passive")
	assert.True(t, policy.VMSelectionPolicy.IsUnrestricted(), "regular zonal keeps the region's machine family")
}

func TestResolveScalingTopology_NonTrueZonalIsActivePassiveWithMediator(t *testing.T) {
	tests := []struct {
		name            string
		isLargeCapacity bool
	}{
		{name: "large-capacity", isLargeCapacity: true},
		{name: "standard", isLargeCapacity: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := ResolveScalingTopology(false, tt.isLargeCapacity)

			assert.Equal(t, vmrs.TopologyActivePassive, policy.Topology)
			assert.False(t, policy.Topology.UsesSharedHADeployment(), "cross-zone pools are non_shared_ha")
			assert.True(t, policy.Topology.RequiresMediator(), "active-passive deployments provision a mediator")
			assert.False(t, policy.Topology.EnableAAConfig())
			assert.True(t, policy.VMSelectionPolicy.IsUnrestricted(), "non-true-zonal imposes no VM restriction")
		})
	}
}

func TestActiveActiveVMSelectionPolicy(t *testing.T) {
	cases := []struct {
		name      string
		family    vmrs.MachineFamilyType
		wantFloor string
	}{
		{"c3-lssd", vmrs.MachineFamilyC3LSSD, "c3-standard-22-lssd"},
		{"c3-non-lssd", vmrs.MachineFamilyC3NonLSSD, "c3-standard-22"},
		{"c4-lssd", vmrs.MachineFamilyC4LSSD, "c4-standard-24-lssd"},
		{"c4-non-lssd", vmrs.MachineFamilyC4NonLSSD, "c4-standard-24"},
		{"unset family", vmrs.MachineFamilyUnset, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := ActiveActiveVMSelectionPolicy(tc.family)

			assert.Equal(t, tc.wantFloor, policy.MinimumMachineType)
			assert.Empty(t, policy.EligibleFamilies, "family scoping belongs to FilterVMsByMachineFamily")
			assert.False(t, policy.RequireLSSD, "LSSD scoping belongs to FilterVMsByMachineFamily")
			assert.Equal(t, tc.wantFloor == "", policy.IsUnrestricted())
		})
	}
}

// Every floor must belong to the family it is filed under. A mismatch leaves the floor outside
// the family-scoped catalog, and FilterCandidatesByPolicy then fails every active-active pool in
// that family with "minimum machine type not found".
func TestActiveActiveFloorBelongsToItsFamily(t *testing.T) {
	for family, floor := range activeActiveFloorByFamily {
		assert.Equal(t, family, vmrs.MachineFamilyForVMType(floor),
			"floor %q is filed under family %q", floor, family)
	}
}

// The floor is only half of the restriction: the catalog is scoped to the pool's family first,
// so the two together must leave only same-family candidates at or above the floor.
func TestActiveActiveVMSelectionPolicy_AppliedToFamilyScopedCatalog(t *testing.T) {
	catalog := []vmrs.VMPerfLimit{
		{VMType: "c4-standard-4-lssd", RelativeCost: 1},
		{VMType: "c4-standard-8-lssd", RelativeCost: 2},
		{VMType: "c4-standard-24-lssd", RelativeCost: 3},
		{VMType: "c4-standard-48-lssd", RelativeCost: 4},
		{VMType: "c4-standard-48", RelativeCost: 4},
		{VMType: "c3-standard-44-lssd", RelativeCost: 4},
	}

	scoped := vmrs.FilterVMsByMachineFamily(catalog, vmrs.MachineFamilyC4LSSD)
	eligible, err := vmrs.FilterCandidatesByPolicy(scoped, ActiveActiveVMSelectionPolicy(vmrs.MachineFamilyC4LSSD))

	require.NoError(t, err)
	require.NotEmpty(t, eligible)
	for _, vm := range eligible {
		assert.NotEqual(t, "c4-standard-4-lssd", vm.VMType, "candidates below the floor must be dropped")
		assert.NotEqual(t, "c4-standard-8-lssd", vm.VMType, "candidates below the floor must be dropped")
		assert.True(t, strings.HasPrefix(vm.VMType, "c4-"), "eligible VM %q must be c4", vm.VMType)
		assert.True(t, strings.HasSuffix(vm.VMType, vmrs.LssdVMTypeSuffix), "eligible VM %q must be LSSD", vm.VMType)
	}
}
