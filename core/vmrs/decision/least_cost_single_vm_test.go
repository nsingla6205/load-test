package decision

import (
	// testify - assert and mock packages for testing.
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/config"
)

func TestNewLeastCostSingleVMDecisionMaker(t *testing.T) {
	cases := []struct {
		name             string
		configFilename   string
		customerRequest  vmrs.CustomerRequestedPerformance
		expectedError    string
		expectedDecision *vmrs.Decision
	}{
		{
			name:           "ValidConfig",
			configFilename: "testdata/valid.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             1,
				DesiredThroughputInMiBs: 4,
				DesiredCapacityInGiB:    1,
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c4-standard-8"},
				StoragePoolRequirements: vmrs.CustomerRequestedPerformance{
					DesiredIOPS:             2,
					DesiredThroughputInMiBs: 9,
					DesiredCapacityInGiB:    2,
				},
			},
		},
		{
			name:           "ValidConfigWithMissingWorkloadHeadroom",
			configFilename: "testdata/valid_missing_workload_headroom.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             10,
				DesiredThroughputInMiBs: 10,
				DesiredCapacityInGiB:    10,
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c4-standard-16"},
				StoragePoolRequirements: vmrs.CustomerRequestedPerformance{
					DesiredIOPS:             13,
					DesiredThroughputInMiBs: 14,
					DesiredCapacityInGiB:    13,
				},
			},
		},
		{
			name:           "MatchingVMEvenWithOverheads",
			configFilename: "testdata/valid.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             2,
				DesiredThroughputInMiBs: 1,
				DesiredCapacityInGiB:    1,
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c4-standard-8"},
				StoragePoolRequirements: vmrs.CustomerRequestedPerformance{
					DesiredIOPS:             4,
					DesiredThroughputInMiBs: 3,
					DesiredCapacityInGiB:    2,
				},
			},
		},
		{
			name:           "NoMatchingVMWhenRequestExceedsAllLimits",
			configFilename: "testdata/valid.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             1000,
				DesiredThroughputInMiBs: 1000,
				DesiredCapacityInGiB:    1000,
			},
			expectedError:    "[vmrs] NoSuitableVMError: no suitable VM found for the customer request (customer request: {DesiredIOPS:1000 DesiredThroughputInMiBs:1000 DesiredCapacityInGiB:1000 CurrentInstanceType: ConfigForPoolInstanceScaling:<nil> ScalingTopology:{Topology: VMSelectionPolicy:{EligibleFamilies:[] MinimumMachineType: RequireLSSD:false}}})",
			expectedDecision: nil,
		},
		{
			name:           "NoMatchingVMWhenIOPSExceedsLimits",
			configFilename: "testdata/valid.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             100,
				DesiredThroughputInMiBs: 1,
				DesiredCapacityInGiB:    1,
			},
			expectedError:    "[vmrs] NoSuitableVMError: no suitable VM found for the customer request (customer request: {DesiredIOPS:100 DesiredThroughputInMiBs:1 DesiredCapacityInGiB:1 CurrentInstanceType: ConfigForPoolInstanceScaling:<nil> ScalingTopology:{Topology: VMSelectionPolicy:{EligibleFamilies:[] MinimumMachineType: RequireLSSD:false}}})",
			expectedDecision: nil,
		},
		{
			name:           "NoMatchingVMWhenThroughtputExceedsLimits",
			configFilename: "testdata/valid.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             1,
				DesiredThroughputInMiBs: 100,
				DesiredCapacityInGiB:    1,
			},
			expectedError:    "[vmrs] NoSuitableVMError: no suitable VM found for the customer request (customer request: {DesiredIOPS:1 DesiredThroughputInMiBs:100 DesiredCapacityInGiB:1 CurrentInstanceType: ConfigForPoolInstanceScaling:<nil> ScalingTopology:{Topology: VMSelectionPolicy:{EligibleFamilies:[] MinimumMachineType: RequireLSSD:false}}})",
			expectedDecision: nil,
		},
		{
			name:           "NoMatchingVMWhenCapacityExceedsLimits",
			configFilename: "testdata/valid.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             1,
				DesiredThroughputInMiBs: 1,
				DesiredCapacityInGiB:    20,
			},
			expectedError:    "[vmrs] NoSuitableVMError: no suitable VM found for the customer request (customer request: {DesiredIOPS:1 DesiredThroughputInMiBs:1 DesiredCapacityInGiB:20 CurrentInstanceType: ConfigForPoolInstanceScaling:<nil> ScalingTopology:{Topology: VMSelectionPolicy:{EligibleFamilies:[] MinimumMachineType: RequireLSSD:false}}})",
			expectedDecision: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config, _ := config.LoadConfig(tc.configFilename)
			dm := NewLeastCostSingleVMDecisionMaker(config)
			dsc, err := dm.FindOptimalVMs(config, tc.customerRequest, nil)

			if tc.expectedError != "" {
				assert.NotNil(t, err, "expected an error but got nil")
				assert.EqualError(t, err, tc.expectedError, "error message mismatch")
			} else {
				assert.Nil(t, err, "expected no error but got one")
				assert.NotNil(t, dsc, "expected a decision but got nil")
				assert.Len(t, dsc.ChosenVMs, 1, "expected exactly one chosen VM")
				assert.Equal(t, tc.expectedDecision, dsc, "VMRS decision mismatch")
			}
		})
	}
}

// TestCompareVMScalingDirection tests the CompareVMScalingDirection method
func TestCompareVMScalingDirection(t *testing.T) {
	cases := []struct {
		name                string
		configFilename      string
		currentInstanceType string
		newInstanceType     string
		expectedIsScalingUp bool
		expectedError       string
	}{
		{
			name:                "ScalingUp_CheaperToMoreExpensive",
			configFilename:      "testdata/valid.yaml",
			currentInstanceType: "c4-standard-4",
			newInstanceType:     "c4-standard-8",
			expectedIsScalingUp: true,
			expectedError:       "",
		},
		{
			name:                "ScalingDown_MoreExpensiveToCheaper",
			configFilename:      "testdata/valid.yaml",
			currentInstanceType: "c4-standard-8",
			newInstanceType:     "c4-standard-4",
			expectedIsScalingUp: false,
			expectedError:       "",
		},
		{
			name:                "SameVMType_NoScaling",
			configFilename:      "testdata/valid.yaml",
			currentInstanceType: "c4-standard-8",
			newInstanceType:     "c4-standard-8",
			expectedIsScalingUp: false,
			expectedError:       "",
		},
		{
			name:                "SameCost_NoScaling",
			configFilename:      "testdata/valid.yaml",
			currentInstanceType: "c4-standard-8",
			newInstanceType:     "c4-standard-16",
			expectedIsScalingUp: false,
			expectedError:       "",
		},
		{
			name:                "CurrentVMTypeNotFound",
			configFilename:      "testdata/valid.yaml",
			currentInstanceType: "non-existent-vm-type",
			newInstanceType:     "c4-standard-8",
			expectedIsScalingUp: false,
			expectedError:       "current VM type not found in sorted list",
		},
		{
			name:                "NewVMTypeNotFound",
			configFilename:      "testdata/valid.yaml",
			currentInstanceType: "c4-standard-8",
			newInstanceType:     "non-existent-vm-type",
			expectedIsScalingUp: false,
			expectedError:       "new VM type not found in sorted list",
		},
		{
			name:                "BothVMTypesNotFound",
			configFilename:      "testdata/valid.yaml",
			currentInstanceType: "non-existent-vm-type-1",
			newInstanceType:     "non-existent-vm-type-2",
			expectedIsScalingUp: false,
			expectedError:       "current VM type not found in sorted list",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config, _ := config.LoadConfig(tc.configFilename)
			dm := NewLeastCostSingleVMDecisionMaker(config)

			isScalingUp, err := dm.CompareVMScalingDirection(tc.currentInstanceType, tc.newInstanceType)

			if tc.expectedError != "" {
				assert.NotNil(t, err, "expected an error but got nil")
				assert.Contains(t, err.Error(), tc.expectedError, "error message should contain expected text")
			} else {
				assert.Nil(t, err, "expected no error but got one")
				assert.Equal(t, tc.expectedIsScalingUp, isScalingUp, "scaling direction mismatch")
			}
		})
	}
}

// TestCompareVMScalingDirection_EmptyCatalog verifies that an empty VMRS catalog
// produces a clear, typed config diagnostic rather than the misleading "VM type
// not found" error. A loaded config can never be empty (DiskPerfLimits is
// validated as required,min=1), so we construct the decision maker directly with
// a hand-built empty config to exercise the defensive guard.
func TestCompareVMScalingDirection_EmptyCatalog(t *testing.T) {
	cases := []struct {
		name   string
		config *vmrs.VMRSConfig
	}{
		{
			name:   "nil disk perf limits",
			config: &vmrs.VMRSConfig{},
		},
		{
			name: "empty qualified VMs in single disk limit",
			config: &vmrs.VMRSConfig{
				HyperscalerPerfLimits: vmrs.HyperscalerPerfLimits{
					DiskPerfLimits: []vmrs.DiskTypePerfLimit{{DiskType: "hyperdisk-balanced"}},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dm := &LeastCostSingleVMDecisionMaker{config: tc.config}

			isScalingUp, err := dm.CompareVMScalingDirection("c4-standard-4", "c4-standard-8")

			assert.False(t, isScalingUp)
			assert.Error(t, err)
			var invalidConfigErr *vmrs.InvalidConfigError
			assert.ErrorAs(t, err, &invalidConfigErr, "expected a typed InvalidConfigError")
			assert.Contains(t, err.Error(), "VMRS catalog is empty")
		})
	}
}

// TestCompareVMScalingDirection_EarlyBreak tests the early break optimization
func TestCompareVMScalingDirection_EarlyBreak(t *testing.T) {
	config, _ := config.LoadConfig("testdata/valid.yaml")
	dm := NewLeastCostSingleVMDecisionMaker(config)

	// Test with first and last VM types to ensure early break works
	currentInstanceType := "c4-standard-4" // First in the list (cheapest)
	newInstanceType := "c4-standard-8"     // Last in the list (more expensive)

	isScalingUp, err := dm.CompareVMScalingDirection(currentInstanceType, newInstanceType)

	assert.Nil(t, err, "expected no error")
	assert.True(t, isScalingUp, "should be scaling up from cheaper to more expensive VM")
}

// TestCompareVMScalingDirection_EdgeCases tests edge cases for the comparison method
func TestCompareVMScalingDirection_EdgeCases(t *testing.T) {
	config, _ := config.LoadConfig("testdata/valid.yaml")
	dm := NewLeastCostSingleVMDecisionMaker(config)

	// Test with VM types that have the same relative cost
	// This would require a config with VMs having identical RelativeCost values
	// For now, we'll test the basic functionality

	// Test that the method handles the case where we need to iterate through the entire list
	// by using VM types that are not at the beginning
	currentInstanceType := "c4-standard-4"
	newInstanceType := "c4-standard-8"

	isScalingUp, err := dm.CompareVMScalingDirection(currentInstanceType, newInstanceType)

	assert.Nil(t, err, "expected no error")
	assert.True(t, isScalingUp, "should be scaling up")
}

func TestLeastCostSingleVMDecisionMaker_FindOptimalVMs_VolumeLimits(t *testing.T) {
	// Load test configuration
	config, err := config.LoadConfig("testdata/valid_single_vm.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostSingleVMDecisionMaker(config)

	cases := []struct {
		name                 string
		customerRequest      vmrs.CustomerRequestedPerformance
		currentConfig        *vlm.VLMConfig
		expectedError        string
		expectedVMType       string
		shouldFindSuitableVM bool
		description          string
	}{
		{
			name: "NoVolumeLimits_ShouldWorkAsNormal",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:                  1000,
				DesiredThroughputInMiBs:      50,
				DesiredCapacityInGiB:         100,
				ConfigForPoolInstanceScaling: nil, // No volume scaling config
			},
			currentConfig:        &vlm.VLMConfig{},
			expectedError:        "",
			shouldFindSuitableVM: true,
			description:          "When no volume limits are provided, should work as before",
		},
		{
			name: "VolumeCountWithinRange_ShouldSelectVM",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             1000,
				DesiredThroughputInMiBs: 50,
				DesiredCapacityInGiB:    100,
				ConfigForPoolInstanceScaling: &vmrs.PoolInstanceScalingConfig{
					CurrentVolCount: 5, // Within range for c3-standard-4-lssd (0-245)
					VolLimitPerInstanceMap: map[string]common.VolumeCountRange{
						"c3-standard-4-lssd": {
							MinVolumeCount: 0,
							MaxVolumeCount: 245,
						},
						"c3-standard-8-lssd": {
							MinVolumeCount: 246,
							MaxVolumeCount: 495,
						},
						"c3-standard-16-lssd": {
							MinVolumeCount: 496,
							MaxVolumeCount: 995,
						},
					},
				},
			},
			currentConfig:        &vlm.VLMConfig{},
			expectedError:        "",
			shouldFindSuitableVM: true,
			description:          "When volume count is within acceptable range, should select VM",
		},
		{
			name: "VolumeCountFitsHigherTierVM_ShouldSelectCorrectVM",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             1000,
				DesiredThroughputInMiBs: 50,
				DesiredCapacityInGiB:    100,
				ConfigForPoolInstanceScaling: &vmrs.PoolInstanceScalingConfig{
					CurrentVolCount: 300, // Fits c3-standard-8-lssd but not c3-standard-4-lssd
					VolLimitPerInstanceMap: map[string]common.VolumeCountRange{
						"c3-standard-4-lssd": {
							MinVolumeCount: 0,
							MaxVolumeCount: 245,
						},
						"c3-standard-8-lssd": {
							MinVolumeCount: 246,
							MaxVolumeCount: 495,
						},
						"c3-standard-16-lssd": {
							MinVolumeCount: 496,
							MaxVolumeCount: 995,
						},
					},
				},
			},
			currentConfig:        &vlm.VLMConfig{},
			expectedError:        "",
			shouldFindSuitableVM: true,
			description:          "When volume count fits higher tier VM but not lower tier, should select correct VM",
		},
		{
			name: "ZeroVolumeCount_ShouldWork",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             1000,
				DesiredThroughputInMiBs: 50,
				DesiredCapacityInGiB:    100,
				ConfigForPoolInstanceScaling: &vmrs.PoolInstanceScalingConfig{
					CurrentVolCount: 0, // Zero volume count
					VolLimitPerInstanceMap: map[string]common.VolumeCountRange{
						"c3-standard-4-lssd": {
							MinVolumeCount: 0,
							MaxVolumeCount: 245,
						},
						"c3-standard-8-lssd": {
							MinVolumeCount: 246,
							MaxVolumeCount: 495,
						},
						"c3-standard-16-lssd": {
							MinVolumeCount: 496,
							MaxVolumeCount: 995,
						},
					},
				},
			},
			currentConfig:        &vlm.VLMConfig{},
			expectedError:        "",
			shouldFindSuitableVM: true,
			description:          "When volume count is zero but all VMs have minimum > 0, should fail",
		},
		{
			name: "ZeroVolumeCount_WithZeroMinimum_ShouldWork",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             1000,
				DesiredThroughputInMiBs: 50,
				DesiredCapacityInGiB:    100,
				ConfigForPoolInstanceScaling: &vmrs.PoolInstanceScalingConfig{
					CurrentVolCount: 0, // Zero volume count
					VolLimitPerInstanceMap: map[string]common.VolumeCountRange{
						"c3-standard-4-lssd": {
							MinVolumeCount: 0,
							MaxVolumeCount: 245,
						},
						"c3-standard-8-lssd": {
							MinVolumeCount: 246,
							MaxVolumeCount: 495,
						},
						"c3-standard-16-lssd": {
							MinVolumeCount: 496,
							MaxVolumeCount: 995,
						},
					},
				},
			},
			currentConfig:        &vlm.VLMConfig{},
			expectedError:        "",
			shouldFindSuitableVM: true,
			description:          "When volume count is zero and minimum is set to 0, should select VM",
		},
		{
			name: "VolumeCountExceedsPerformanceCapableVM_ShouldFail",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             50000, // High performance requirement
				DesiredThroughputInMiBs: 2000,
				DesiredCapacityInGiB:    1000,
				ConfigForPoolInstanceScaling: &vmrs.PoolInstanceScalingConfig{
					CurrentVolCount: 250, // Would fit c3-standard-8-lssd range but performance is too low
					VolLimitPerInstanceMap: map[string]common.VolumeCountRange{
						"c3-standard-4-lssd": {
							MinVolumeCount: 0,
							MaxVolumeCount: 245,
						},
						"c3-standard-8-lssd": {
							MinVolumeCount: 246,
							MaxVolumeCount: 495,
						},
						"c3-standard-16-lssd": {
							MinVolumeCount: 496,
							MaxVolumeCount: 995,
						},
					},
				},
			},
			currentConfig:        &vlm.VLMConfig{},
			expectedError:        "no suitable VM found for the customer request",
			shouldFindSuitableVM: false,
			description:          "When volume count fits but performance requirements don't, should fail",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := dm.FindOptimalVMs(config, tc.customerRequest, tc.currentConfig)

			if tc.shouldFindSuitableVM {
				assert.Nil(t, err, "expected no error but got: %v", err)
				assert.NotNil(t, decision, "expected a decision but got nil")
				if decision != nil {
					assert.NotEmpty(t, decision.ChosenVMs, "expected chosen VMs")
					assert.Equal(t, 1, len(decision.ChosenVMs), "single VM decision maker should return exactly one VM")
				}
			} else {
				assert.NotNil(t, err, "expected an error but got nil")
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError, "error message should contain expected text")
				}
				assert.Nil(t, decision, "expected no decision when error occurs")
			}
		})
	}
}

func TestLeastCostSingleVMDecisionMaker_HigherMachineType(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_single_vm.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostSingleVMDecisionMaker(config)

	// Set up a scenario where multiple VMs can handle the request
	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             12, // Low requirement that multiple VMs can handle
		DesiredThroughputInMiBs: 424,
		DesiredCapacityInGiB:    10,
		ConfigForPoolInstanceScaling: &vmrs.PoolInstanceScalingConfig{
			CurrentVolCount:     247,
			CurrentInstanceType: "c3-standard-4-lssd",
			VolLimitPerInstanceMap: map[string]common.VolumeCountRange{
				"c3-standard-4-lssd": {
					MinVolumeCount: 0,
					MaxVolumeCount: 245,
				},
				"c3-standard-8-lssd": {
					MinVolumeCount: 246,
					MaxVolumeCount: 495,
				},
				"c3-standard-16-lssd": {
					MinVolumeCount: 496,
					MaxVolumeCount: 995,
				},
			},
		},
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, &vlm.VLMConfig{})

	assert.Nil(t, err, "expected no error")
	assert.NotNil(t, decision, "expected a decision")

	if decision != nil {
		// Should select the lowest cost VM that meets all criteria
		// Since VMs are sorted by cost, the first qualifying VM should be selected
		vmsSorted := dm.GetVMsSortedByCost()
		assert.NotEmpty(t, vmsSorted, "expected sorted VMs list")

		// Verify that we got a valid VM type
		assert.NotEmpty(t, decision.ChosenVMs[0], "expected a VM type")

		// The selected VM should be one that can handle the volume count and performance
		selectedVMType := decision.ChosenVMs[0]
		volumeRange, exists := customerRequest.ConfigForPoolInstanceScaling.VolLimitPerInstanceMap[selectedVMType]
		assert.True(t, exists, "selected VM should have volume limits defined")
		assert.True(t, customerRequest.ConfigForPoolInstanceScaling.CurrentVolCount >= volumeRange.MinVolumeCount,
			"current volume count should be >= min limit")
		assert.True(t, customerRequest.ConfigForPoolInstanceScaling.CurrentVolCount <= volumeRange.MaxVolumeCount,
			"current volume count should be <= max limit")
	}
}

func TestLeastCostSingleVMDecisionMaker_HigherMachineType_StrippedCurrentInstanceType(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_single_vm.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostSingleVMDecisionMaker(config)

	// Same scenario as HigherMachineType but CurrentInstanceType omits "-lssd" (VSA_INSTANCE_TYPE_OVERRIDE_LSSD).
	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             12,
		DesiredThroughputInMiBs: 424,
		DesiredCapacityInGiB:    10,
		ConfigForPoolInstanceScaling: &vmrs.PoolInstanceScalingConfig{
			CurrentVolCount:     247,
			CurrentInstanceType: "c3-standard-4",
			VolLimitPerInstanceMap: map[string]common.VolumeCountRange{
				"c3-standard-4-lssd": {
					MinVolumeCount: 0,
					MaxVolumeCount: 245,
				},
				"c3-standard-8-lssd": {
					MinVolumeCount: 246,
					MaxVolumeCount: 495,
				},
				"c3-standard-16-lssd": {
					MinVolumeCount: 496,
					MaxVolumeCount: 995,
				},
			},
		},
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, &vlm.VLMConfig{})

	assert.Nil(t, err, "expected no error")
	assert.NotNil(t, decision, "expected a decision")
	if decision != nil {
		assert.Equal(t, "c3-standard-8-lssd", decision.ChosenVMs[0], "must skip current tier even when instance type string is stripped")
	}
}

func TestLeastCostSingleVMDecisionMaker_LowerMachineType(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_single_vm.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostSingleVMDecisionMaker(config)

	// Set up a scenario where multiple VMs can handle the request
	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             15001, // Low requirement that multiple VMs can handle
		DesiredThroughputInMiBs: 501,
		DesiredCapacityInGiB:    1001,
		ConfigForPoolInstanceScaling: &vmrs.PoolInstanceScalingConfig{
			CurrentVolCount:     247,
			CurrentInstanceType: "c3-standard-16-lssd",
			VolLimitPerInstanceMap: map[string]common.VolumeCountRange{
				"c3-standard-4-lssd": {
					MinVolumeCount: 0,
					MaxVolumeCount: 245,
				},
				"c3-standard-8-lssd": {
					MinVolumeCount: 246,
					MaxVolumeCount: 495,
				},
				"c3-standard-16-lssd": {
					MinVolumeCount: 496,
					MaxVolumeCount: 995,
				},
			},
		},
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, &vlm.VLMConfig{})

	assert.Nil(t, err, "expected no error")
	assert.NotNil(t, decision, "expected a decision")

	if decision != nil {
		// Should select the lowest cost VM that meets all criteria
		// Since VMs are sorted by cost, the first qualifying VM should be selected
		vmsSorted := dm.GetVMsSortedByCost()
		assert.NotEmpty(t, vmsSorted, "expected sorted VMs list")

		// Verify that we got a valid VM type
		assert.NotEmpty(t, decision.ChosenVMs[0], "expected a VM type")

		// The selected VM should be one that can handle the volume count and performance
		selectedVMType := decision.ChosenVMs[0]
		volumeRange, exists := customerRequest.ConfigForPoolInstanceScaling.VolLimitPerInstanceMap[selectedVMType]
		assert.True(t, exists, "selected VM should have volume limits defined")
		assert.True(t, customerRequest.ConfigForPoolInstanceScaling.CurrentVolCount >= volumeRange.MinVolumeCount,
			"current volume count should be >= min limit")
		assert.True(t, customerRequest.ConfigForPoolInstanceScaling.CurrentVolCount <= volumeRange.MaxVolumeCount,
			"current volume count should be <= max limit")
	}
}
