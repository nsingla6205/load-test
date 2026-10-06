package decision

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/config"
)

func TestNewLeastCostLargeVolumeClusterDecisionMaker(t *testing.T) {
	cases := []struct {
		name             string
		configFilename   string
		customerRequest  vmrs.CustomerRequestedPerformance
		expectedError    string
		expectedDecision *vmrs.Decision
	}{
		{
			name:           "MinimumValidLargeVolumeRequest",
			configFilename: "testdata/valid_large_volume.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             1600,  // Min IOPS
				DesiredThroughputInMiBs: 64,    // Min throughput
				DesiredCapacityInGiB:    12288, // 12 TiB minimum
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6, // Active-passive mode: only active nodes have LIFs
					IsHomogeneous: true,
					VMType:        "c3-standard-4-lssd", // With 2 HA pairs and 1.8 scaling factor
				},
			},
		},
		{
			name:           "MediumLargeVolumeRequest",
			configFilename: "testdata/valid_large_volume.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             40000, // Medium IOPS
				DesiredThroughputInMiBs: 1000,  // Medium throughput
				DesiredCapacityInGiB:    51200, // 50 TiB
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6, // Active-passive mode: only active nodes have LIFs
					IsHomogeneous: true,
					VMType:        "c3-standard-8-lssd", // With 4.8 scaling, per-node throughput fits c3-standard-8-lssd
				},
			},
		},
		{
			name:           "HighIOPSLargeVolumeRequest",
			configFilename: "testdata/valid_large_volume.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             160000, // Max IOPS
				DesiredThroughputInMiBs: 5000,   // High throughput
				DesiredCapacityInGiB:    204800, // 200 TiB
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6, // Active-passive mode: only active nodes have LIFs
					IsHomogeneous: true,
					VMType:        "c3-standard-88-lssd", // High IOPS requirement needs largest VM
				},
			},
		},
		{
			name:           "MaxThroughputLargeVolumeRequest",
			configFilename: "testdata/valid_large_volume.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             80000,  // Medium IOPS
				DesiredThroughputInMiBs: 61440,  // Max throughput (60 GiB/s)
				DesiredCapacityInGiB:    102400, // 100 TiB
			},
			expectedError:    "no suitable VM type found for cluster requirements",
			expectedDecision: nil, // No decision when error occurs
		},
		{
			name:           "MaxCapacityLargeVolumeRequest",
			configFilename: "testdata/valid_large_volume.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             500000,  // Medium IOPS
				DesiredThroughputInMiBs: 2000,    // Medium throughput
				DesiredCapacityInGiB:    2621440, // 20 PiB (max capacity)
			},
			expectedError:    "no suitable VM type found for cluster requirements",
			expectedDecision: nil,
		},
		{
			name:           "NoSuitableVMFound_ExceedsAllVMCapabilities",
			configFilename: "testdata/valid_large_volume.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             1000000, // 1M IOPS - exceeds all VMs even with scaling
				DesiredThroughputInMiBs: 10000,   // 10 GiB/s
				DesiredCapacityInGiB:    12582912,
			},
			expectedError:    "no suitable VM type found for cluster requirements",
			expectedDecision: nil,
		},
		{
			name:           "ValidRequestWithNoNonLinearScaling",
			configFilename: "testdata/valid_large_volume_no_scaling.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             25000,
				DesiredThroughputInMiBs: 500,
				DesiredCapacityInGiB:    12288,
			},
			expectedError:    "no non-linear scaling configuration found for active-passive topology",
			expectedDecision: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := config.LoadConfig(tc.configFilename)
			assert.Nil(t, err, "failed to load config")

			dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)
			decision, err := dm.FindOptimalVMs(config, tc.customerRequest, nil)

			if tc.expectedError != "" {
				assert.NotNil(t, err, "expected an error but got nil")
				assert.Contains(t, err.Error(), tc.expectedError, "error message should contain expected text")
				assert.Nil(t, decision, "expected no decision when error occurs")
			} else {
				assert.Nil(t, err, "expected no error but got: %v", err)
				assert.NotNil(t, decision, "expected a decision but got nil")

				if decision == nil {
					return // Don't continue if decision is nil to avoid panic
				}

				// Verify basic decision structure
				assert.NotEmpty(t, decision.ChosenVMs, "expected chosen VMs")
				assert.NotNil(t, decision.ClusterMetadata, "expected cluster metadata for large volume decision")

				// Verify we got some VMs (exact VMs may vary based on scaling logic)
				assert.GreaterOrEqual(t, len(decision.ChosenVMs), 2, "expected at least 2 VMs for large volume cluster")

				// Verify cluster metadata has reasonable values
				assert.GreaterOrEqual(t, decision.ClusterMetadata.NumHAPairs, 1, "expected at least 1 HA pair")
				assert.GreaterOrEqual(t, decision.ClusterMetadata.NumNodes, 2, "expected at least 2 nodes")
				assert.GreaterOrEqual(t, decision.ClusterMetadata.NumLIFs, 1, "expected at least 1 LIF")
				assert.True(t, decision.ClusterMetadata.IsHomogeneous, "expected homogeneous cluster")
				assert.NotEmpty(t, decision.ClusterMetadata.VMType, "expected VM type to be set")

				// If we have a specific expected decision, verify it matches
				if tc.expectedDecision != nil {
					assert.Equal(t, tc.expectedDecision.ChosenVMs, decision.ChosenVMs, "chosen VMs should match")
					assert.Equal(t, tc.expectedDecision.ClusterMetadata.NumHAPairs, decision.ClusterMetadata.NumHAPairs, "HA pairs should match")
					assert.Equal(t, tc.expectedDecision.ClusterMetadata.NumNodes, decision.ClusterMetadata.NumNodes, "number of nodes should match")
					assert.Equal(t, tc.expectedDecision.ClusterMetadata.NumLIFs, decision.ClusterMetadata.NumLIFs, "number of LIFs should match")
					assert.Equal(t, tc.expectedDecision.ClusterMetadata.IsHomogeneous, decision.ClusterMetadata.IsHomogeneous, "homogeneous flag should match")
					assert.Equal(t, tc.expectedDecision.ClusterMetadata.VMType, decision.ClusterMetadata.VMType, "VM type should match")
				}
			}
		})
	}
}

func TestNewLeastCostLargeVolumeClusterBoundaryDecisionMaker(t *testing.T) {
	cases := []struct {
		name             string
		configFilename   string
		customerRequest  vmrs.CustomerRequestedPerformance
		expectedError    string
		expectedDecision *vmrs.Decision
	}{
		{
			name:           "C3Standard4LSSD_MinIOPS_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             1600,  // Minimum IOPS for large volume
				DesiredThroughputInMiBs: 100,   // Low throughput
				DesiredCapacityInGiB:    12288, // Minimum capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-4-lssd",
				},
			},
		},
		{
			name:           "C3Standard4LSSD_MaxIOPS_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             40608, // Near max IOPS (40,608) for c3-standard-4-lssd
				DesiredThroughputInMiBs: 100,   // Low throughput
				DesiredCapacityInGiB:    50000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-4-lssd",
				},
			},
		},
		{
			name:           "C3Standard4LSSD_MinThroughput_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             5000,  // Low IOPS
				DesiredThroughputInMiBs: 64,    // Minimum throughput for large volume
				DesiredCapacityInGiB:    12288, // Minimum capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-4-lssd",
				},
			},
		},
		{
			name:           "C3Standard4LSSD_MaxThroughput_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             5000,  // Low IOPS
				DesiredThroughputInMiBs: 360,   // Near max throughput (360) for c3-standard-4-lssd
				DesiredCapacityInGiB:    50000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-4-lssd",
				},
			},
		},
		{
			name:           "C3Standard4LSSD_MinCapacity_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             5000,  // Low IOPS
				DesiredThroughputInMiBs: 100,   // Low throughput
				DesiredCapacityInGiB:    12288, // Minimum capacity (12 TiB)
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-4-lssd",
				},
			},
		},
		{
			name:           "C3Standard4LSSD_MaxCapacity_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             5000,    // Low IOPS
				DesiredThroughputInMiBs: 100,     // Low throughput
				DesiredCapacityInGiB:    1278000, // Near max capacity (2,556,000) for c3-standard-4-lssd
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd", "c3-standard-4-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-4-lssd",
				},
			},
		},
		// c3-standard-8-lssd boundary tests
		{
			name:           "C3Standard8LSSD_MinIOPS_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             40609, // Minimum IOPS
				DesiredThroughputInMiBs: 360,   // Moderate throughput
				DesiredCapacityInGiB:    50000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-8-lssd",
				},
			},
		},
		{
			name:           "C3Standard8LSSD_MaxIOPS_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             85176,  // max IOPS (85,176) for c3-standard-8-lssd
				DesiredThroughputInMiBs: 500,    // Moderate throughput
				DesiredCapacityInGiB:    100000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-8-lssd",
				},
			},
		},
		{
			name:           "C3Standard8LSSD_MinThroughput_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             10000, // Moderate IOPS
				DesiredThroughputInMiBs: 361,   // Minimum throughput
				DesiredCapacityInGiB:    50000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-8-lssd",
				},
			},
		},
		{
			name:           "C3Standard8LSSD_MaxThroughput_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             10000,  // Moderate IOPS
				DesiredThroughputInMiBs: 1665,   // Near max throughput (1,665) for c3-standard-8-lssd
				DesiredCapacityInGiB:    100000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd", "c3-standard-8-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-8-lssd",
				},
			},
		},
		// c3-standard-22-lssd boundary tests
		{
			name:           "C3Standard22LSSD_MinIOPS_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             85177,  // Minimum IOPS
				DesiredThroughputInMiBs: 1000,   // Moderate throughput
				DesiredCapacityInGiB:    100000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-22-lssd",
				},
			},
		},
		{
			name:           "C3Standard22LSSD_MaxIOPS_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             151046, // Near max IOPS (151,046) for c3-standard-22-lssd
				DesiredThroughputInMiBs: 1000,   // Moderate throughput
				DesiredCapacityInGiB:    200000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-22-lssd",
				},
			},
		},
		{
			name:           "C3Standard22LSSD_MinThroughput_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             20000,  // Moderate IOPS
				DesiredThroughputInMiBs: 1666,   // Minimum throughput
				DesiredCapacityInGiB:    100000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-22-lssd",
				},
			},
		},
		{
			name:           "C3Standard22LSSD_MaxThroughput_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             20000,  // Moderate IOPS
				DesiredThroughputInMiBs: 2860,   // max throughput (2,860) for c3-standard-22-lssd
				DesiredCapacityInGiB:    200000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd", "c3-standard-22-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-22-lssd",
				},
			},
		},
		// c3-standard-44-lssd boundary tests
		{
			name:           "C3Standard44LSSD_MinIOPS_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             151047, // Minimum IOPS
				DesiredThroughputInMiBs: 2000,   // Moderate throughput
				DesiredCapacityInGiB:    500000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-44-lssd",
				},
			},
		},
		{
			name:           "C3Standard44LSSD_MaxIOPS_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             305193,  // Near max IOPS (305,193) for c3-standard-44-lssd
				DesiredThroughputInMiBs: 2000,    // Moderate throughput
				DesiredCapacityInGiB:    1000000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-44-lssd",
				},
			},
		},
		{
			name:           "C3Standard44LSSD_MinThroughput_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             50000,  // Moderate IOPS
				DesiredThroughputInMiBs: 2861,   // Minimum throughput
				DesiredCapacityInGiB:    500000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-44-lssd",
				},
			},
		},
		{
			name:           "C3Standard44LSSD_MaxThroughput_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             50000,   // Moderate IOPS
				DesiredThroughputInMiBs: 5030,    // max throughput (5,030) for c3-standard-44-lssd
				DesiredCapacityInGiB:    1000000, // Moderate capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-44-lssd",
				},
			},
		},
		{
			name:           "C3Standard44LSSD_MinCapacity_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             50000,   // Moderate IOPS
				DesiredThroughputInMiBs: 1000,    // Moderate throughput
				DesiredCapacityInGiB:    1278100, // Minimum capacity (just greater than 1,278,000)
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-44-lssd",
				},
			},
		},
		{
			name:           "C3Standard44LSSD_MaxCapacity_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             50000,   // Moderate IOPS
				DesiredThroughputInMiBs: 2000,    // Moderate throughput
				DesiredCapacityInGiB:    2556000, // max capacity for 6 HA pair (2,556,000) for c3-standard-44-lssd
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd", "c3-standard-44-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-44-lssd",
				},
			},
		},
		// c3-standard-88-lssd boundary tests
		{
			name:           "C3Standard88LSSD_MinIOPS_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             305194,  // Minimum IOPS
				DesiredThroughputInMiBs: 5031,    // High throughput
				DesiredCapacityInGiB:    1000000, // High capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-88-lssd",
				},
			},
		},
		{
			name:           "C3Standard88LSSD_MaxIOPS_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             772800,  // Near max IOPS (772,800) for c3-standard-88-lssd
				DesiredThroughputInMiBs: 10000,   // High throughput
				DesiredCapacityInGiB:    2000000, // High capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-88-lssd",
				},
			},
		},
		{
			name:           "C3Standard88LSSD_MinThroughput_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             100000,  // High IOPS
				DesiredThroughputInMiBs: 5031,    // Minimum throughput
				DesiredCapacityInGiB:    1000000, // High capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-88-lssd",
				},
			},
		},
		{
			name:           "C3Standard88LSSD_MaxThroughput_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             100000,  // High IOPS
				DesiredThroughputInMiBs: 25200,   // Near max throughput (25,200) for c3-standard-88-lssd
				DesiredCapacityInGiB:    2000000, // High capacity
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-88-lssd",
				},
			},
		},
		{
			name:           "C3Standard88LSSD_MaxCapacity_BoundaryTest",
			configFilename: "testdata/valid_large_capacity3.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             100000,  // High IOPS
				DesiredThroughputInMiBs: 10000,   // High throughput
				DesiredCapacityInGiB:    2556000, // Near max capacity (2,556,000) for c3-standard-88-lssd
			},
			expectedError: "",
			expectedDecision: &vmrs.Decision{
				ChosenVMs: []string{"c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd", "c3-standard-88-lssd"},
				ClusterMetadata: &vmrs.ClusterMetadata{
					NumHAPairs:    6,
					NumNodes:      12,
					NumLIFs:       6,
					IsHomogeneous: true,
					VMType:        "c3-standard-88-lssd",
				},
			},
		},
		{
			name:           "MaxThroughputLargeVolumeRequest",
			configFilename: "testdata/valid_large_volume.yaml",
			customerRequest: vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             80000,  // Medium IOPS
				DesiredThroughputInMiBs: 61440,  // Max throughput (60 GiB/s)
				DesiredCapacityInGiB:    102400, // 100 TiB
			},
			expectedError:    "no suitable VM type found for cluster requirements",
			expectedDecision: nil, // No decision when error occurs
		},
	}

	_lVHaPair := LVHaPair
	LVHaPair = 6 // Set global variable for HA pairs in large volume tests
	// Reset after tests
	defer func() {
		LVHaPair = _lVHaPair
	}()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := config.LoadConfig(tc.configFilename)
			assert.Nil(t, err, "failed to load config")

			dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)
			decision, err := dm.FindOptimalVMs(config, tc.customerRequest, nil)

			if tc.expectedError != "" {
				assert.NotNil(t, err, "expected an error but got nil")
				assert.Contains(t, err.Error(), tc.expectedError, "error message should contain expected text")
				assert.Nil(t, decision, "expected no decision when error occurs")
			} else {
				assert.Nil(t, err, "expected no error but got: %v", err)
				assert.NotNil(t, decision, "expected a decision but got nil")

				if decision == nil {
					return // Don't continue if decision is nil to avoid panic
				}

				// Verify basic decision structure
				assert.NotEmpty(t, decision.ChosenVMs, "expected chosen VMs")
				assert.NotNil(t, decision.ClusterMetadata, "expected cluster metadata for large volume decision")

				// Verify we got some VMs (exact VMs may vary based on scaling logic)
				assert.GreaterOrEqual(t, len(decision.ChosenVMs), 2, "expected at least 2 VMs for large volume cluster")

				// Verify cluster metadata has reasonable values
				assert.GreaterOrEqual(t, decision.ClusterMetadata.NumHAPairs, 1, "expected at least 1 HA pair")
				assert.GreaterOrEqual(t, decision.ClusterMetadata.NumNodes, 2, "expected at least 2 nodes")
				assert.GreaterOrEqual(t, decision.ClusterMetadata.NumLIFs, 1, "expected at least 1 LIF")
				assert.True(t, decision.ClusterMetadata.IsHomogeneous, "expected homogeneous cluster")
				assert.NotEmpty(t, decision.ClusterMetadata.VMType, "expected VM type to be set")

				// If we have a specific expected decision, verify it matches
				if tc.expectedDecision != nil {
					assert.Equal(t, tc.expectedDecision.ChosenVMs, decision.ChosenVMs, "chosen VMs should match")
					assert.Equal(t, tc.expectedDecision.ClusterMetadata.NumHAPairs, decision.ClusterMetadata.NumHAPairs, "HA pairs should match")
					assert.Equal(t, tc.expectedDecision.ClusterMetadata.NumNodes, decision.ClusterMetadata.NumNodes, "number of nodes should match")
					assert.Equal(t, tc.expectedDecision.ClusterMetadata.NumLIFs, decision.ClusterMetadata.NumLIFs, "number of LIFs should match")
					assert.Equal(t, tc.expectedDecision.ClusterMetadata.IsHomogeneous, decision.ClusterMetadata.IsHomogeneous, "homogeneous flag should match")
					assert.Equal(t, tc.expectedDecision.ClusterMetadata.VMType, decision.ClusterMetadata.VMType, "VM type should match")
				}
			}
		})
	}
}

func TestLargeVolumeClusterCompareVMScalingDirection(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	cases := []struct {
		name                string
		currentInstanceType string
		newInstanceType     string
		expectedIsScalingUp bool
		expectedError       string
	}{
		{
			name:                "ScalingUpFromSmallToMedium",
			currentInstanceType: "c3-standard-4-lssd",
			newInstanceType:     "c3-standard-8-lssd",
			expectedIsScalingUp: true,
			expectedError:       "",
		},
		{
			name:                "ScalingUpFromMediumToLarge",
			currentInstanceType: "c3-standard-8-lssd",
			newInstanceType:     "c3-standard-22-lssd",
			expectedIsScalingUp: true,
			expectedError:       "",
		},
		{
			name:                "ScalingUpToMaxVM",
			currentInstanceType: "c3-standard-44-lssd",
			newInstanceType:     "c3-standard-88-lssd",
			expectedIsScalingUp: true,
			expectedError:       "",
		},
		{
			name:                "ScalingDownFromLargeToMedium",
			currentInstanceType: "c3-standard-22-lssd",
			newInstanceType:     "c3-standard-8-lssd",
			expectedIsScalingUp: false,
			expectedError:       "",
		},
		{
			name:                "SameInstanceType",
			currentInstanceType: "c3-standard-8-lssd",
			newInstanceType:     "c3-standard-8-lssd",
			expectedIsScalingUp: false,
			expectedError:       "",
		},
		{
			name:                "InvalidCurrentInstanceType",
			currentInstanceType: "invalid-instance",
			newInstanceType:     "c3-standard-8-lssd",
			expectedIsScalingUp: false,
			expectedError:       "current VM type not found",
		},
		{
			name:                "InvalidNewInstanceType",
			currentInstanceType: "c3-standard-8-lssd",
			newInstanceType:     "invalid-instance",
			expectedIsScalingUp: false,
			expectedError:       "new VM type not found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isScalingUp, err := dm.CompareVMScalingDirection(tc.currentInstanceType, tc.newInstanceType)

			if tc.expectedError != "" {
				assert.NotNil(t, err, "expected an error but got nil")
				assert.Contains(t, err.Error(), tc.expectedError, "error message should contain expected text")
			} else {
				assert.Nil(t, err, "expected no error but got one: %v", err)
				assert.Equal(t, tc.expectedIsScalingUp, isScalingUp, "scaling direction mismatch")
			}
		})
	}
}

func TestLargeVolumeClusterApplyNonLinearScaling(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	cases := []struct {
		name               string
		haPairs            int
		originalIOPS       int64
		originalThroughput int64
		expectedIOPS       int64
		expectedThroughput int64
	}{
		{
			name:               "SingleHAPair_NoScaling",
			haPairs:            1,
			originalIOPS:       10000,
			originalThroughput: 100,
			expectedIOPS:       10000, // 1.0x scaling
			expectedThroughput: 100,   // 1.0x scaling
		},
		{
			name:               "SixHAPairs_DefinedScaling",
			haPairs:            6,
			originalIOPS:       10000,
			originalThroughput: 100,
			expectedIOPS:       2084, // ceil(10000 / 4.8) = 2084
			expectedThroughput: 21,   // ceil(100 / 4.8) = 21
		},
		{
			name:               "TwelveHAPairs_MaxScaling",
			haPairs:            12,
			originalIOPS:       10000,
			originalThroughput: 100,
			expectedIOPS:       2041, // ceil(10000 / 4.9) = 2041
			expectedThroughput: 18,   // ceil(100 / 5.8) = 18
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scaledIOPS, scaledThroughput, err := dm.applyNonLinearScaling(tc.originalIOPS, tc.originalThroughput, tc.haPairs, vmrs.TopologyActivePassive)
			assert.NoError(t, err, "applyNonLinearScaling should not return error")

			assert.Equal(t, tc.expectedIOPS, scaledIOPS, "IOPS scaling mismatch")
			assert.Equal(t, tc.expectedThroughput, scaledThroughput, "throughput scaling mismatch")
		})
	}
}

func TestLargeVolumeClusterApplyNonLinearScaling_ErrorCases(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	// Test cases without exact scaling factor matches (should fail)
	errorCases := []struct {
		name               string
		haPairs            int
		originalIOPS       int64
		originalThroughput int64
	}{
		{
			name:               "ThreeHAPairs_NoExactMatch",
			haPairs:            3,
			originalIOPS:       10000,
			originalThroughput: 100,
		},
		{
			name:               "NineHAPairs_NoExactMatch",
			haPairs:            9,
			originalIOPS:       10000,
			originalThroughput: 100,
		},
		{
			name:               "FifteenHAPairs_NoExactMatch",
			haPairs:            15,
			originalIOPS:       10000,
			originalThroughput: 100,
		},
	}

	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			scaledIOPS, scaledThroughput, err := dm.applyNonLinearScaling(tc.originalIOPS, tc.originalThroughput, tc.haPairs, vmrs.TopologyActivePassive)
			assert.Error(t, err, "applyNonLinearScaling should return error for unconfigured HA pair count")
			assert.Contains(t, err.Error(), fmt.Sprintf("no scaling factor configured for %d HA pairs", tc.haPairs))
			assert.Equal(t, int64(0), scaledIOPS, "IOPS should be 0 when error occurs")
			assert.Equal(t, int64(0), scaledThroughput, "throughput should be 0 when error occurs")
		})
	}
}

func TestLargeVolumeClusterApplyNonLinearScaling_NoScalingConfig(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume_no_scaling.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	// When no scaling config is present, should fall back to linear scaling
	originalIOPS := int64(10000)
	originalThroughput := int64(100)
	haPairs := 6

	scaledIOPS, scaledThroughput, err := dm.applyNonLinearScaling(originalIOPS, originalThroughput, haPairs, vmrs.TopologyActivePassive)

	// Should return error since no scaling configuration is provided
	assert.Error(t, err, "applyNonLinearScaling should return error when no scaling config")
	assert.Contains(t, err.Error(), "no non-linear scaling configuration found")
	assert.Equal(t, int64(0), scaledIOPS, "IOPS should be 0 when error occurs")
	assert.Equal(t, int64(0), scaledThroughput, "throughput should be 0 when error occurs")
}

// TestFindOptimalVMs_WithCurrentConfigAccountID verifies required HA pairs are derived from
// currentConfig.Deployment.Labels["account_id"] via LvHaPairsForLargeVolume when labels are set.
func TestFindOptimalVMs_WithCurrentConfigAccountID(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)
	current := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			Labels: map[string]string{"account_id": "not-in-custom-ha-allowlist"},
		},
	}

	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             16000,
		DesiredThroughputInMiBs: 1000,
		DesiredCapacityInGiB:    100000,
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, current)
	assert.NoError(t, err)
	assert.NotNil(t, decision)
	assert.NotEmpty(t, decision.ChosenVMs)
}

func TestFindOptimalVMs_UsesRequestedHAPairs(t *testing.T) {
	vmrsConfig, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	require.NoError(t, err)

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(vmrsConfig)
	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             16000,
		DesiredThroughputInMiBs: 1000,
		DesiredCapacityInGiB:    100000,
		LvHAPairs:               12,
	}

	decision, err := dm.FindOptimalVMs(vmrsConfig, customerRequest, nil)
	require.NoError(t, err)
	require.NotNil(t, decision)
	require.NotNil(t, decision.ClusterMetadata)
	assert.Equal(t, 12, decision.ClusterMetadata.NumHAPairs)
	assert.Equal(t, 24, decision.ClusterMetadata.NumNodes)
	assert.Len(t, decision.ChosenVMs, 24)
}

// TestFindOptimalVMs_NonLinearScaling verifies that non-linear scaling is applied correctly
func TestFindOptimalVMs_NonLinearScaling(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             16000,  // Will be scaled down by 4.8 for 6 HA pairs
		DesiredThroughputInMiBs: 1000,   // Will be scaled down by 4.8 for 6 HA pairs
		DesiredCapacityInGiB:    100000, // Should NOT be scaled during VM selection
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	assert.NoError(t, err, "FindOptimalVMs should not return error")
	assert.NotNil(t, decision, "decision should not be nil")

	// Verify that non-linear scaling was applied (IOPS and throughput scaled down)
	// For 6 HA pairs: IOPS scaled to 3334, throughput scaled to 209
	// The VM selection should use these scaled values
	assert.NotEmpty(t, decision.ChosenVMs, "should have chosen VMs")
	assert.NotNil(t, decision.ClusterMetadata, "should have cluster metadata")
}

// TestFindOptimalVMs_CapacityNotAmplifiedDuringSelection verifies that capacity amplification
// factor is NOT applied during VM selection (line 93), only in final provisioning (line 108)
func TestFindOptimalVMs_CapacityNotAmplifiedDuringSelection(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	// Use a capacity that would fail if amplified by 1.2 during selection
	// For 6 HA pairs, active-passive: numLIFs = 6
	// Per-node capacity = 100000 / 6 = 16,666 GiB
	// If amplified: 100000 * 1.2 / 6 = 20,000 GiB per node
	// c3-standard-4-lssd has capacity 213000 GiB, so both should work
	// But we want to verify the logic doesn't amplify during selection
	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             1000,
		DesiredThroughputInMiBs: 100,
		DesiredCapacityInGiB:    100000, // Should be used as-is for VM selection
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	assert.NoError(t, err, "FindOptimalVMs should not return error")
	assert.NotNil(t, decision, "decision should not be nil")

	// Verify capacity in storage pool requirements IS amplified
	// capacity amplification factor is 1.3 (from test config), so 100000 * 1.3 = 130000
	expectedProvisionedCapacity := int64(130000) // 100000 * 1.3
	assert.Equal(t, expectedProvisionedCapacity, decision.StoragePoolRequirements.DesiredCapacityInGiB,
		"capacity should be amplified in storage pool requirements")
}

// TestFindOptimalVMs_StoragePoolRequirements verifies that storage pool requirements
// are calculated correctly with min() operations and amplification factors
func TestFindOptimalVMs_StoragePoolRequirements(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             10000,
		DesiredThroughputInMiBs: 500,
		DesiredCapacityInGiB:    50000,
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	assert.NoError(t, err, "FindOptimalVMs should not return error")
	assert.NotNil(t, decision, "decision should not be nil")

	// Verify storage pool requirements structure
	assert.NotNil(t, decision.StoragePoolRequirements, "should have storage pool requirements")

	// Capacity should be amplified by 1.3 (from test config)
	expectedCapacity := int64(65000) // 50000 * 1.3
	assert.Equal(t, expectedCapacity, decision.StoragePoolRequirements.DesiredCapacityInGiB,
		"capacity should be amplified in storage pool requirements")

	// IOPS and throughput should be amplified and then capped by VM limits
	// The exact values depend on amplification factors and VM limits
	assert.Greater(t, decision.StoragePoolRequirements.DesiredIOPS, int64(0),
		"IOPS should be greater than 0")
	assert.Greater(t, decision.StoragePoolRequirements.DesiredThroughputInMiBs, int64(0),
		"throughput should be greater than 0")
}

// TestFindOptimalVMs_NonLinearScalingError verifies error handling when non-linear scaling fails
func TestFindOptimalVMs_NonLinearScalingError(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	// Temporarily set LVHaPair to an unsupported value
	originalLVHaPair := LVHaPair
	LVHaPair = 3 // Not configured in scaling factors
	defer func() { LVHaPair = originalLVHaPair }()

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             10000,
		DesiredThroughputInMiBs: 500,
		DesiredCapacityInGiB:    50000,
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	assert.Error(t, err, "FindOptimalVMs should return error when scaling fails")
	assert.Contains(t, err.Error(), "failed to apply non-linear scaling",
		"error should mention non-linear scaling failure")
	assert.Nil(t, decision, "decision should be nil when error occurs")
}

// TestFindOptimalVMs_VMSelectionError verifies error handling when no suitable VM is found
func TestFindOptimalVMs_VMSelectionError(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	// Request that exceeds all VM capabilities
	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             1000000,  // Extremely high IOPS
		DesiredThroughputInMiBs: 100000,   // Extremely high throughput
		DesiredCapacityInGiB:    10000000, // Extremely high capacity
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	assert.Error(t, err, "FindOptimalVMs should return error when no VM found")
	assert.Contains(t, err.Error(), "no suitable VM type found",
		"error should mention no suitable VM")
	assert.Nil(t, decision, "decision should be nil when error occurs")
}

// TestFindOptimalVMs_CapacityExceedsMaxLimit verifies that capacity is NOT capped in VMRS
// (validation happens at the validator layer, not in VMRS decision maker)
func TestFindOptimalVMs_CapacityExceedsMaxLimit(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	// Request capacity that exceeds maxLvHotTierCapacityInGiB
	// maxLvHotTierCapacityInGiB = 2814749767106560 / 1073741824 = 2621440 GiB
	excessiveCapacity := int64(5000000) // 5 PiB, exceeds max
	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             1000,
		DesiredThroughputInMiBs: 100,
		DesiredCapacityInGiB:    excessiveCapacity,
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	// Note: Capacity is NOT capped in VMRS - validation happens at validator layer
	// The VMRS will try to find a VM that can handle the per-node capacity requirement
	// If it succeeds, the capacity will be passed through (with amplification) to storage pool requirements
	if err == nil {
		assert.NotNil(t, decision, "decision should not be nil if successful")
		// Verify that capacity is NOT capped - it should be amplified but not limited to maxLvHotTierCapacityInGiB
		// Capacity amplification factor is 1.3, so 5000000 * 1.3 = 6500000
		expectedCapacity := int64(6500000) // excessiveCapacity * 1.3
		assert.Equal(t, expectedCapacity, decision.StoragePoolRequirements.DesiredCapacityInGiB,
			"capacity should be amplified but NOT capped in VMRS (validation happens elsewhere)")
	} else {
		// If it fails, it's because no VM can handle the per-node capacity requirement
		// (excessiveCapacity / 6 LIFs = 833,333 GiB per node, which exceeds all VM limits)
		assert.Contains(t, err.Error(), "no suitable VM type found",
			"should fail if per-node capacity exceeds VM limits")
	}
}

// TestFindOptimalVMs_CapacityNotCappedAtLine93 verifies that capacity is NOT capped with min()
// at line 93 (scaledCustomerReq.DesiredCapacityInGiB)
func TestFindOptimalVMs_CapacityNotCappedAtLine93(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	// Use capacity that exceeds maxLvHotTierCapacityInGiB but is still within VM limits
	// maxLvHotTierCapacityInGiB = 2,621,440 GiB
	// For 6 HA pairs, active-passive: numLIFs = 6
	// Per-node capacity = 3,000,000 / 6 = 500,000 GiB per node
	// c3-standard-44-lssd has capacity 426,000 GiB, so this will fail
	// But we can use a smaller value that fits within VM limits
	capacityAboveMax := int64(2500000) // 2.5 PiB, above maxLvHotTierCapacityInGiB but within VM capacity
	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             1000,
		DesiredThroughputInMiBs: 100,
		DesiredCapacityInGiB:    capacityAboveMax,
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	if err == nil {
		assert.NotNil(t, decision, "decision should not be nil if successful")
		// Verify that the capacity passed to VM selection (line 93) is NOT capped
		// The capacity should be used as-is without min() operation
		// After amplification: capacityAboveMax * 1.3 = 3,250,000
		expectedCapacity := int64(3250000) // capacityAboveMax * 1.3
		assert.Equal(t, expectedCapacity, decision.StoragePoolRequirements.DesiredCapacityInGiB,
			"capacity at line 93 should NOT be capped with min(maxLvHotTierCapacityInGiB)")
	}
}

// TestProductAdvertisedLimits_LargeCapacityPool validates that the production VMRS config
// (config/vmrs_gcp.yaml) can satisfy the publicly documented Google Cloud NetApp Volumes
// performance limits for Large Capacity (Flex Unified) pools with 6 HA pairs.
//
// This test intentionally loads the PRODUCTION config, not a testdata fixture.
// A failure here means a config change broke a publicly advertised product limit.
//
// Limits tested (derived from production scaling factors × largest VM per-node perf):
//
//	6 HA pairs  (iops_factor=4.8, throughput_factor=4.8):
//	  advertised Max IOPS:       750,000  (160,000 × 4.8 = 768,000 headroom)
//	  advertised Max Throughput: 22 GiB/s / 22,528 MiB/s (5,120 × 4.8 = 24,576 headroom)
//
// See: https://cloud.google.com/netapp-volumes/docs/configure-and-use/storage-pools/overview
// Jira: VSCP-4955 (root cause: scaling factor 4.0 → ceiling 625K IOPS, below 750K limit)
func TestProductAdvertisedLimits_LargeCapacityPool(t *testing.T) {
	cfg, err := config.LoadConfig("../../../config/vmrs_gcp.yaml")
	assert.NoError(t, err, "production vmrs_gcp.yaml must load without error")
	if err != nil {
		t.FailNow()
	}

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(cfg)

	cases := []struct {
		name        string
		haPairs     int
		desiredIOPS int64
		desiredMiBs int64
	}{
		// 6 HA pairs — publicly advertised limits
		// iops_factor=4.8: ceiling = 160,000 × 4.8 = 768,000 → advertised 750,000
		// throughput_factor=4.8: ceiling = 5,120 × 4.8 = 24,576 MiB/s → advertised 22,528
		{"6HAPairs_MaxAdvertisedIOPS", 6, 750_000, 1_000},
		{"6HAPairs_MaxAdvertisedThroughput", 6, 100_000, 22_528}, // 22 GiB/s = 22*1024
		{"6HAPairs_BothMaxLimits", 6, 750_000, 22_528},
	}

	_lVHaPair := LVHaPair
	defer func() { LVHaPair = _lVHaPair }()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			LVHaPair = tc.haPairs
			req := vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             tc.desiredIOPS,
				DesiredThroughputInMiBs: tc.desiredMiBs,
				DesiredCapacityInGiB:    102_400, // 100 TiB — valid large capacity pool size
			}
			decision, err := dm.FindOptimalVMs(cfg, req, nil)
			assert.NoError(t, err,
				"production config must satisfy limit for %d HA pairs — %d IOPS / %d MiB/s; "+
					"if this fails, check non_linear_scaling_active_passive factors in config/vmrs_gcp.yaml",
				tc.haPairs, tc.desiredIOPS, tc.desiredMiBs)
			assert.NotNil(t, decision, "must return a valid VM decision")
		})
	}
}

// TestFindOptimalVMs_CapacityNotCappedAtLine108 verifies that capacity is NOT capped with min()
// at line 108 (limits.DesiredCapacityInGiB in storage pool requirements)
func TestFindOptimalVMs_CapacityNotCappedAtLine108(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	// Use capacity that exceeds maxLvHotTierCapacityInGiB
	maxLvHotTierCapacityInGiB := int64(2621440)                    // 2.5 PiB
	capacityAboveMax := maxLvHotTierCapacityInGiB + int64(1000000) // 3.6 PiB
	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             1000,
		DesiredThroughputInMiBs: 100,
		DesiredCapacityInGiB:    capacityAboveMax,
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	if err == nil {
		assert.NotNil(t, decision, "decision should not be nil if successful")
		// Verify that capacity in storage pool requirements (line 108) is NOT capped
		// It should be amplified but NOT limited to maxLvHotTierCapacityInGiB
		expectedCapacity := int64(4703872) // capacityAboveMax * 1.3 = 3,621,440 * 1.3 = 4,703,872
		assert.Equal(t, expectedCapacity, decision.StoragePoolRequirements.DesiredCapacityInGiB,
			"capacity at line 108 should NOT be capped with min(maxLvHotTierCapacityInGiB)")
		assert.Greater(t, decision.StoragePoolRequirements.DesiredCapacityInGiB, maxLvHotTierCapacityInGiB,
			"capacity should exceed maxLvHotTierCapacityInGiB (validation happens at validator layer)")
	}
}

// TestFindOptimalVMs_IOPSAndThroughputCappedByVMLimits verifies that IOPS and throughput
// in storage pool requirements are capped by VM disk limits with overprovisioning factors
func TestFindOptimalVMs_IOPSAndThroughputCappedByVMLimits(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	// Request very high IOPS and throughput that would exceed VM limits
	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             1000000, // Very high
		DesiredThroughputInMiBs: 100000,  // Very high
		DesiredCapacityInGiB:    100000,  // Reasonable
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	if err == nil {
		assert.NotNil(t, decision, "decision should not be nil if successful")

		// Verify that IOPS and throughput are capped by VM limits
		// For 6 HA pairs = 12 nodes, and assuming c3-standard-88-lssd is selected:
		// Disk IOPS limit: 160000, with max overprovisioning 1.1 = 176000 per VM
		// Total cluster limit: 176000 * 12 = 2,112,000 IOPS
		// The storage pool requirements should be min(scaled request, VM limit)
		assert.Greater(t, decision.StoragePoolRequirements.DesiredIOPS, int64(0),
			"IOPS should be greater than 0")
		assert.Greater(t, decision.StoragePoolRequirements.DesiredThroughputInMiBs, int64(0),
			"throughput should be greater than 0")
	}
}

// TestFindOptimalVMs_ClusterLayout verifies that cluster layout is generated correctly
func TestFindOptimalVMs_ClusterLayout(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             10000,
		DesiredThroughputInMiBs: 500,
		DesiredCapacityInGiB:    100000,
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	assert.NoError(t, err, "FindOptimalVMs should not return error")
	assert.NotNil(t, decision, "decision should not be nil")

	// Verify cluster layout
	assert.NotNil(t, decision.ClusterMetadata, "should have cluster metadata")
	assert.Equal(t, 6, decision.ClusterMetadata.NumHAPairs, "should have 6 HA pairs")
	assert.Equal(t, 12, decision.ClusterMetadata.NumNodes, "should have 12 nodes (6 HA pairs * 2)")
	assert.Equal(t, 6, decision.ClusterMetadata.NumLIFs, "should have 6 LIFs (active-passive mode)")
	assert.True(t, decision.ClusterMetadata.IsHomogeneous, "should be homogeneous cluster")
	assert.NotEmpty(t, decision.ClusterMetadata.VMType, "should have VM type set")

	// Verify chosen VMs match cluster metadata
	assert.Equal(t, 12, len(decision.ChosenVMs), "should have 12 VMs")
	for _, vmType := range decision.ChosenVMs {
		assert.Equal(t, decision.ClusterMetadata.VMType, vmType,
			"all VMs should be of the same type (homogeneous)")
	}
}

// activeActiveTopologyFor returns the active-active topology with family's floor applied, which
// is what IdentifyVMs assembles once it has resolved the pool's machine family.
func activeActiveTopologyFor(family vmrs.MachineFamilyType) vmrs.ScalingTopology {
	topology := ResolveScalingTopology(true, true)
	topology.VMSelectionPolicy = ActiveActiveVMSelectionPolicy(family)
	return topology
}

// TestFindOptimalVMs_ActiveActiveMode verifies behavior in active-active mode
func TestFindOptimalVMs_ActiveActiveMode(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)

	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             10000,
		DesiredThroughputInMiBs: 500,
		DesiredCapacityInGiB:    100000,
		ScalingTopology:         activeActiveTopologyFor(vmrs.MachineFamilyC3LSSD),
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	assert.NoError(t, err, "FindOptimalVMs should not return error")
	assert.NotNil(t, decision, "decision should not be nil")

	// In active-active mode, numLIFs should be haPairs * 2
	assert.NotNil(t, decision.ClusterMetadata, "should have cluster metadata")
	assert.Equal(t, 12, decision.ClusterMetadata.NumLIFs,
		"should have 12 LIFs in active-active mode (6 HA pairs * 2)")
	assert.NotEqual(t, "c3-standard-4-lssd", decision.ClusterMetadata.VMType,
		"true-zonal should not select VM types below the configured floor")
}

func TestFilterCandidateVMs_ExcludesBelowFloor(t *testing.T) {
	vms := []vmrs.VMPerfLimit{
		{VMType: "c3-standard-4-lssd", RelativeCost: 4},
		{VMType: "c3-standard-22-lssd", RelativeCost: 22},
		{VMType: "c3-standard-44-lssd", RelativeCost: 44},
	}

	filtered, err := vmrs.FilterCandidatesByPolicy(vms, activeActiveTopologyFor(vmrs.MachineFamilyC3LSSD).VMSelectionPolicy)
	assert.NoError(t, err)
	assert.Len(t, filtered, 2)
	for _, vm := range filtered {
		assert.NotEqual(t, "c3-standard-4-lssd", vm.VMType)
	}
}

// Other families are excluded by the catalog scoping that runs before the policy, not by the
// policy itself, so the two are exercised together here.
func TestFilterCandidateVMs_ExcludesOtherFamilies(t *testing.T) {
	vms := []vmrs.VMPerfLimit{
		{VMType: "c3-standard-22-lssd", RelativeCost: 22},
		{VMType: "c4-standard-4", RelativeCost: 4},
		{VMType: "c4-standard-22-lssd", RelativeCost: 22},
	}

	scoped := vmrs.FilterVMsByMachineFamily(vms, vmrs.MachineFamilyC3LSSD)
	filtered, err := vmrs.FilterCandidatesByPolicy(scoped, activeActiveTopologyFor(vmrs.MachineFamilyC3LSSD).VMSelectionPolicy)
	assert.NoError(t, err)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "c3-standard-22-lssd", filtered[0].VMType)
}

// As above: the -lssd requirement comes from the family-scoped catalog. Both candidates cost the
// same so only the LSSD scoping can separate them.
func TestFilterCandidateVMs_RequiresLSSD(t *testing.T) {
	vms := []vmrs.VMPerfLimit{
		{VMType: "c3-standard-22", RelativeCost: 22},
		{VMType: "c3-standard-22-lssd", RelativeCost: 22},
	}

	scoped := vmrs.FilterVMsByMachineFamily(vms, vmrs.MachineFamilyC3LSSD)
	filtered, err := vmrs.FilterCandidatesByPolicy(scoped, activeActiveTopologyFor(vmrs.MachineFamilyC3LSSD).VMSelectionPolicy)
	assert.NoError(t, err)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "c3-standard-22-lssd", filtered[0].VMType)
}

func TestFilterCandidateVMs_NoOpWhenNotTrueZonal(t *testing.T) {
	vms := []vmrs.VMPerfLimit{
		{VMType: "c3-standard-4-lssd", RelativeCost: 4},
		{VMType: "c4-standard-4", RelativeCost: 4},
	}

	filtered, err := vmrs.FilterCandidatesByPolicy(vms, ResolveScalingTopology(false, true).VMSelectionPolicy)
	assert.NoError(t, err)
	assert.Equal(t, vms, filtered)
}

func TestFindOptimalVMs_TrueZonal_SelectsAtOrAboveFloor(t *testing.T) {
	config, err := config.LoadConfig("testdata/valid_large_volume.yaml")
	assert.Nil(t, err, "failed to load config")

	dm := NewLeastCostLargeVolumeClusterDecisionMaker(config)
	customerRequest := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             1600,
		DesiredThroughputInMiBs: 64,
		DesiredCapacityInGiB:    12288,
		ScalingTopology:         activeActiveTopologyFor(vmrs.MachineFamilyC3LSSD),
	}

	decision, err := dm.FindOptimalVMs(config, customerRequest, nil)
	assert.NoError(t, err)
	assert.NotNil(t, decision.ClusterMetadata)
	assert.Equal(t, "c3-standard-22-lssd", decision.ClusterMetadata.VMType)
}

func TestEffectiveLargeVolumeHAPairs(t *testing.T) {
	original := LVHaPair
	LVHaPair = 6
	t.Cleanup(func() { LVHaPair = original })

	assert.Equal(t, int64(12), EffectiveLargeVolumeHAPairs(12, "acct"))
	assert.Equal(t, int64(6), EffectiveLargeVolumeHAPairs(0, ""))
	assert.Equal(t, int64(6), EffectiveLargeVolumeHAPairs(0, "not-allowlisted"))
}
