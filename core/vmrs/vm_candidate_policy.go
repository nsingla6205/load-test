package vmrs

import (
	"fmt"
	"slices"
	"strings"
)

// FilterCandidatesByPolicy returns the subset of vms that satisfies policy. When policy is
// Unrestricted it returns vms unchanged. When MinimumMachineType is set it must resolve to a
// catalog row, candidates cheaper than that floor (by RelativeCost) are discarded.
func FilterCandidatesByPolicy(vms []VMPerfLimit, policy VMSelectionPolicy) ([]VMPerfLimit, error) {
	if policy.IsUnrestricted() {
		return vms, nil
	}

	var floor *VMPerfLimit
	if policy.MinimumMachineType != "" {
		floor = resolveVMPerfLimit(vms, policy.MinimumMachineType)
		if floor == nil {
			return nil, fmt.Errorf("minimum machine type %q not found in VMRS catalog", policy.MinimumMachineType)
		}
	}

	filtered := make([]VMPerfLimit, 0, len(vms))
	for i := range vms {
		vm := vms[i]
		if !matchesEligibleFamily(vm.VMType, policy.EligibleFamilies) {
			continue
		}
		if policy.RequireLSSD && !strings.HasSuffix(vm.VMType, LssdVMTypeSuffix) {
			continue
		}
		if floor != nil && vm.RelativeCost < floor.RelativeCost {
			continue
		}
		filtered = append(filtered, vm)
	}

	if len(filtered) == 0 {
		return nil, fmt.Errorf("no VM candidates remain after applying VM selection policy")
	}
	return filtered, nil
}

// matchesEligibleFamily reports whether machineType belongs to one of the eligible families.
// An empty families list matches any machine type. Matching is done on the machine-family
// prefix (the segment before the first hyphen, e.g. "c3" for "c3-standard-22-lssd").
func matchesEligibleFamily(machineType string, families []string) bool {
	if len(families) == 0 {
		return true
	}
	return slices.Contains(families, familyFromMachineType(machineType))
}

// familyFromMachineType returns the machine-family prefix of a machine type string
// (e.g. "c3" from "c3-standard-22-lssd"). Returns "" when the input is empty or malformed.
func familyFromMachineType(machineType string) string {
	segments := strings.SplitN(strings.TrimSpace(machineType), "-", 2)
	if len(segments) == 0 {
		return ""
	}
	return segments[0]
}
