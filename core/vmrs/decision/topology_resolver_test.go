package decision

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestMachineFamilyForTopology(t *testing.T) {
	cases := []struct {
		name       string
		topology   vmrs.DeploymentTopology
		configured vmrs.MachineFamilyType
		want       vmrs.MachineFamilyType
	}{
		{"active-active in a c4-lssd region", vmrs.TopologyActiveActive, vmrs.MachineFamilyC4LSSD, vmrs.MachineFamilyC3LSSD},
		{"active-active in a c4-non-lssd region", vmrs.TopologyActiveActive, vmrs.MachineFamilyC4NonLSSD, vmrs.MachineFamilyC3LSSD},
		{"active-active in a c3-non-lssd region", vmrs.TopologyActiveActive, vmrs.MachineFamilyC3NonLSSD, vmrs.MachineFamilyC3LSSD},
		{"active-active in a c3-lssd region", vmrs.TopologyActiveActive, vmrs.MachineFamilyC3LSSD, vmrs.MachineFamilyC3LSSD},
		{"active-active with no configured family", vmrs.TopologyActiveActive, vmrs.MachineFamilyUnset, vmrs.MachineFamilyC3LSSD},
		{"active-passive keeps the configured family", vmrs.TopologyActivePassive, vmrs.MachineFamilyC4LSSD, vmrs.MachineFamilyC4LSSD},
		{"unset topology keeps the configured family", "", vmrs.MachineFamilyC4LSSD, vmrs.MachineFamilyC4LSSD},
		{"unset topology keeps an unset family", "", vmrs.MachineFamilyUnset, vmrs.MachineFamilyUnset},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, MachineFamilyForTopology(tc.topology, tc.configured))
		})
	}
}

func TestMachineFamilyForTopology_ActiveActiveFamilyMatchesSelectionPolicy(t *testing.T) {
	family := MachineFamilyForTopology(vmrs.TopologyActiveActive, vmrs.MachineFamilyC4LSSD)
	catalog := []vmrs.VMPerfLimit{
		{VMType: floorMachineTypeC3LSSD, RelativeCost: 1},
		{VMType: "c3-standard-44-lssd", RelativeCost: 2},
		{VMType: "c3-standard-44", RelativeCost: 2},
		{VMType: "c4-standard-48-lssd", RelativeCost: 2},
	}

	scoped := vmrs.FilterVMsByMachineFamily(catalog, family)
	eligible, err := vmrs.FilterCandidatesByPolicy(scoped, activeActiveC3LSSDVMSelectionPolicy)

	require.NoError(t, err)
	require.NotEmpty(t, eligible)
	for _, vm := range eligible {
		assert.True(t, strings.HasPrefix(vm.VMType, machineFamilyC3+"-"), "eligible VM %q must be c3", vm.VMType)
		assert.True(t, strings.HasSuffix(vm.VMType, vmrs.LssdVMTypeSuffix), "eligible VM %q must be LSSD", vm.VMType)
	}
}
