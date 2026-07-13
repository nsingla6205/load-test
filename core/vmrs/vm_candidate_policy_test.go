package vmrs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterCandidatesByPolicy_NoPolicyReturnsUnchanged(t *testing.T) {
	vms := []VMPerfLimit{
		{VMType: "c3-standard-4-lssd", RelativeCost: 4},
		{VMType: "c4-standard-4", RelativeCost: 4},
	}

	filtered, err := FilterCandidatesByPolicy(vms, VMSelectionPolicy{})
	assert.NoError(t, err)
	assert.Equal(t, vms, filtered)
}

func TestFilterCandidatesByPolicy_RestrictsFamilyLSSDAndFloor(t *testing.T) {
	vms := []VMPerfLimit{
		{VMType: "c3-standard-4-lssd", RelativeCost: 4},
		{VMType: "c3-standard-22-lssd", RelativeCost: 22},
		{VMType: "c3-standard-44-lssd", RelativeCost: 44},
		{VMType: "c3-standard-44", RelativeCost: 40},
		{VMType: "c4-standard-88-lssd", RelativeCost: 88},
	}
	policy := VMSelectionPolicy{
		EligibleFamilies:   []string{"c3"},
		MinimumMachineType: "c3-standard-22-lssd",
		RequireLSSD:        true,
	}

	filtered, err := FilterCandidatesByPolicy(vms, policy)
	assert.NoError(t, err)

	got := make([]string, 0, len(filtered))
	for _, vm := range filtered {
		got = append(got, vm.VMType)
	}
	// c3 family + lssd + at/above floor (22): keeps 22 and 44-lssd only.
	assert.ElementsMatch(t, []string{"c3-standard-22-lssd", "c3-standard-44-lssd"}, got)
}

func TestFilterCandidatesByPolicy_MissingMinimumMachineTypeErrors(t *testing.T) {
	vms := []VMPerfLimit{
		{VMType: "c3-standard-44-lssd", RelativeCost: 44},
	}
	policy := VMSelectionPolicy{
		EligibleFamilies:   []string{"c3"},
		MinimumMachineType: "c3-standard-22-lssd",
		RequireLSSD:        true,
	}

	filtered, err := FilterCandidatesByPolicy(vms, policy)
	assert.Error(t, err)
	assert.Nil(t, filtered)
	assert.Contains(t, err.Error(), "not found in VMRS catalog")
}

func TestFilterCandidatesByPolicy_ZeroCandidatesRemainErrors(t *testing.T) {
	vms := []VMPerfLimit{
		{VMType: "c3-standard-22-lssd", RelativeCost: 22},
		{VMType: "c4-standard-88", RelativeCost: 88},
	}
	// Restrict to a family that only the floor belongs to, but require local SSD on a
	// non-lssd-only set so nothing above the floor qualifies.
	policy := VMSelectionPolicy{
		EligibleFamilies:   []string{"c4"},
		MinimumMachineType: "c3-standard-22-lssd",
		RequireLSSD:        true,
	}

	filtered, err := FilterCandidatesByPolicy(vms, policy)
	assert.Error(t, err)
	assert.Nil(t, filtered)
	assert.Contains(t, err.Error(), "no VM candidates remain")
}
