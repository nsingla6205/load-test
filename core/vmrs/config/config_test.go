package config

import (
	// testify - assert and mock packages for testing.
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
)

func TestParseConfig(t *testing.T) {
	cases := []struct {
		name           string
		configFilename string
		expectedError  string
		expectedConfig *vmrs.VMRSConfig
	}{
		{
			name:           "ValidConfig",
			configFilename: "testdata/valid.yaml",
			expectedError:  "",
			expectedConfig: &vmrs.VMRSConfig{
				HyperscalerPerfLimits: vmrs.HyperscalerPerfLimits{
					VMSelectionStrategy: vmrs.LeastCostSingleVM,
					MaxNumHAPairs:       12,
					MaxDiskOverprovisioningFactors: vmrs.PerfAmplificationFactors{
						IOPS:       1.1,
						Throughput: 1.1,
					},
					OntapOverheads: vmrs.OntapOverheads{
						AmplificationFactors: vmrs.AmplificationFactors{
							PerfAmplificationFactors: vmrs.PerfAmplificationFactors{
								IOPS:       1.1,
								Throughput: 1.2,
							},
							Capacity: 1.3,
						},
						NumDisksPerZone: 4,
						WorkloadHeadroom: []vmrs.WorkloadHeadroom{
							{
								WorkloadName: "workload1",
								Headroom: vmrs.PerfAmplificationFactors{
									IOPS:       1.4,
									Throughput: 1.5,
								},
							},
							{
								WorkloadName: "workload2",
								Headroom: vmrs.PerfAmplificationFactors{
									IOPS:       1.7,
									Throughput: 1.8,
								},
							},
						},
						HotspotPreventionFactors: vmrs.PerfAmplificationFactors{
							IOPS:       1.9,
							Throughput: 2.0,
						},
					},
					DiskPerfLimits: []vmrs.DiskTypePerfLimit{
						{
							DiskType: "hyperdisk_balanced",
							QualifiedVMs: []vmrs.VMPerfLimit{
								{
									VMType: "c4-standard-4",
									OntapLimits: vmrs.OntapPerfLimit{
										IOPS:             1,
										ThroughputInMiBs: 2,
										CapacityInGiB:    3,
									},
									DiskLimits: vmrs.DiskPerfLimit{
										IOPS:             4,
										ThroughputInMiBs: 5,
										CapacityInGiB:    6,
									},
									RelativeCost: 4.0,
								},
								{
									VMType: "c4-standard-8",
									OntapLimits: vmrs.OntapPerfLimit{
										IOPS:             8,
										ThroughputInMiBs: 9,
										CapacityInGiB:    10,
									},
									DiskLimits: vmrs.DiskPerfLimit{
										IOPS:             11,
										ThroughputInMiBs: 12,
										CapacityInGiB:    13,
									},
									RelativeCost: 8.0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:           "ValidConfigWithMissingHeadroom",
			configFilename: "testdata/valid_missing_headroom.yaml",
			expectedError:  "",
			expectedConfig: &vmrs.VMRSConfig{
				HyperscalerPerfLimits: vmrs.HyperscalerPerfLimits{
					VMSelectionStrategy: vmrs.LeastCostSingleVM,
					MaxNumHAPairs:       12,
					MaxDiskOverprovisioningFactors: vmrs.PerfAmplificationFactors{
						IOPS:       1.1,
						Throughput: 1.1,
					},
					OntapOverheads: vmrs.OntapOverheads{
						AmplificationFactors: vmrs.AmplificationFactors{
							PerfAmplificationFactors: vmrs.PerfAmplificationFactors{
								IOPS:       1.1,
								Throughput: 1.2,
							},
							Capacity: 1.3,
						},
						NumDisksPerZone: 4,
						HotspotPreventionFactors: vmrs.PerfAmplificationFactors{
							IOPS:       1.9,
							Throughput: 2.0,
						},
					},
					DiskPerfLimits: []vmrs.DiskTypePerfLimit{
						{
							DiskType: "hyperdisk_balanced",
							QualifiedVMs: []vmrs.VMPerfLimit{
								{
									VMType: "c4-standard-4",
									OntapLimits: vmrs.OntapPerfLimit{
										IOPS:             1,
										ThroughputInMiBs: 2,
										CapacityInGiB:    3,
									},
									DiskLimits: vmrs.DiskPerfLimit{
										IOPS:             4,
										ThroughputInMiBs: 5,
										CapacityInGiB:    6,
									},
									RelativeCost: 4.0,
								},
								{
									VMType: "c4-standard-8",
									OntapLimits: vmrs.OntapPerfLimit{
										IOPS:             8,
										ThroughputInMiBs: 9,
										CapacityInGiB:    10,
									},
									DiskLimits: vmrs.DiskPerfLimit{
										IOPS:             11,
										ThroughputInMiBs: 12,
										CapacityInGiB:    13,
									},
									RelativeCost: 8.0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:           "MissingFile",
			configFilename: "testdata/missing_file.yaml",
			expectedError:  `[vmrs] ConfigParseError: failed to read config file due to error: open testdata/missing_file.yaml: no such file or directory (path: testdata/missing_file.yaml)`,
			expectedConfig: nil,
		},
		{
			name:           "MissingDiskTypeField",
			configFilename: "testdata/missing_disk_type.yaml",
			expectedError: `[vmrs] ConfigParseError: failed to parse config file due to error: [3:5] Key: 'DiskTypePerfLimit.DiskType' Error:Field validation for 'DiskType' failed on the 'required' tag
   1 | vmrs:
   2 |   disk_limits:
>  3 |     - qualified_vms:
           ^
   4 |       - vm_type: c4-standard-4
   5 |         ontap_perf:
   6 |           iops: 1
   7 |            (path: testdata/missing_disk_type.yaml)`,
			expectedConfig: nil,
		},
		{
			name:           "MissingDiskPerfLimitsField",
			configFilename: "testdata/missing_disk_perf.yaml",
			expectedError: `[vmrs] ConfigParseError: failed to parse config file due to error: Key: 'VMPerfLimit.DiskLimits.IOPS' Error:Field validation for 'IOPS' failed on the 'required' tag
Key: 'VMPerfLimit.DiskLimits.ThroughputInMiBs' Error:Field validation for 'ThroughputInMiBs' failed on the 'required' tag
Key: 'VMPerfLimit.DiskLimits.CapacityInGiB' Error:Field validation for 'CapacityInGiB' failed on the 'required' tag (path: testdata/missing_disk_perf.yaml)`,
			expectedConfig: nil,
		},
		{
			name:           "MissingRelativeCostField",
			configFilename: "testdata/missing_relative_cost.yaml",
			expectedError: `[vmrs] ConfigParseError: failed to parse config file due to error: [23:7] Key: 'VMPerfLimit.RelativeCost' Error:Field validation for 'RelativeCost' failed on the 'required' tag
  20 |   disk_limits:
  21 |     - disk_type: hyperdisk_balanced
  22 |       qualified_vms:
> 23 |       - vm_type: c4-standard-4
             ^
  24 |         ontap_perf:
  25 |           iops: 1
  26 |           throughput_in_mibs: 2
  27 |            (path: testdata/missing_relative_cost.yaml)`,
			expectedConfig: nil,
		},
		{
			name:           "MissingOntapOverheadsField",
			configFilename: "testdata/missing_ontap_overheads.yaml",
			expectedError: `[vmrs] ConfigParseError: failed to parse config file due to error: Key: 'HyperscalerPerfLimits.OntapOverheads.AmplificationFactors.PerfAmplificationFactors.IOPS' Error:Field validation for 'IOPS' failed on the 'required' tag
Key: 'HyperscalerPerfLimits.OntapOverheads.AmplificationFactors.PerfAmplificationFactors.Throughput' Error:Field validation for 'Throughput' failed on the 'required' tag
Key: 'HyperscalerPerfLimits.OntapOverheads.AmplificationFactors.Capacity' Error:Field validation for 'Capacity' failed on the 'required' tag
Key: 'HyperscalerPerfLimits.OntapOverheads.NumDisksPerZone' Error:Field validation for 'NumDisksPerZone' failed on the 'required' tag
Key: 'HyperscalerPerfLimits.OntapOverheads.HotspotPreventionFactors.IOPS' Error:Field validation for 'IOPS' failed on the 'required' tag
Key: 'HyperscalerPerfLimits.OntapOverheads.HotspotPreventionFactors.Throughput' Error:Field validation for 'Throughput' failed on the 'required' tag (path: testdata/missing_ontap_overheads.yaml)`,
			expectedConfig: nil,
		},
		{
			name:           "MissingMaxNumHAPairsField",
			configFilename: "testdata/missing_max_num_ha_pairs.yaml",
			expectedError: `[vmrs] ConfigParseError: failed to parse config file due to error: [1:5] Key: 'HyperscalerPerfLimits.MaxNumHAPairs' Error:Field validation for 'MaxNumHAPairs' failed on the 'required' tag
>  1 | vmrs:
           ^
   2 |   ontap_overheads:
   3 |     amplification_factors:
   4 |       iops: 1.1
   5 |        (path: testdata/missing_max_num_ha_pairs.yaml)`,
			expectedConfig: nil,
		},
		{
			name:           "MissingVMSelectionStrategyField",
			configFilename: "testdata/missing_vm_selection_strategy.yaml",
			expectedError: `[vmrs] ConfigParseError: failed to parse config file due to error: [1:5] Key: 'HyperscalerPerfLimits.VMSelectionStrategy' Error:Field validation for 'VMSelectionStrategy' failed on the 'required' tag
>  1 | vmrs:
           ^
   2 |   ontap_overheads:
   3 |     amplification_factors:
   4 |       iops: 1.1
   5 |        (path: testdata/missing_vm_selection_strategy.yaml)`,
			expectedConfig: nil,
		},
		{
			name:           "MissingMaxDiskOverprovisioningFactorsField",
			configFilename: "testdata/missing_disk_overprovisioning_factors.yaml",
			expectedError: `[vmrs] ConfigParseError: failed to parse config file due to error: Key: 'HyperscalerPerfLimits.MaxDiskOverprovisioningFactors.IOPS' Error:Field validation for 'IOPS' failed on the 'required' tag
Key: 'HyperscalerPerfLimits.MaxDiskOverprovisioningFactors.Throughput' Error:Field validation for 'Throughput' failed on the 'required' tag (path: testdata/missing_disk_overprovisioning_factors.yaml)`,
			expectedConfig: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := LoadConfig(tc.configFilename)

			if tc.expectedError != "" {
				assert.NotNil(t, err)
				assert.EqualError(t, err, tc.expectedError)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tc.expectedConfig, config)
			}
		})
	}
}

// TestProductionGCPConfigsLoad guards the two production GCP VMRS catalogs that
// the pool workflows pick between by ONTAP version: vmrs_gcp.yaml (ONTAP <
// 9.19.1, c3-only) and vmrs_gcp_v2.yaml (ONTAP >= 9.19.1, c3 + c4). It asserts
// both parse and that only v2 qualifies the c4 machine families, so a malformed
// catalog or an accidental c4 entry in the c3-only file is caught at build time.
func TestProductionGCPConfigsLoad(t *testing.T) {
	familiesIn := func(cfg *vmrs.VMRSConfig) map[string]bool {
		seen := map[string]bool{}
		for _, dl := range cfg.HyperscalerPerfLimits.DiskPerfLimits {
			for _, vm := range dl.QualifiedVMs {
				switch {
				case strings.HasPrefix(vm.VMType, "c3-"):
					seen["c3"] = true
				case strings.HasPrefix(vm.VMType, "c4-"):
					seen["c4"] = true
				}
			}
		}
		return seen
	}

	v1, err := LoadConfig("../../../config/vmrs_gcp.yaml")
	require.NoError(t, err, "production vmrs_gcp.yaml must load without error")
	v1Families := familiesIn(v1)
	assert.True(t, v1Families["c3"], "vmrs_gcp.yaml must qualify c3 machine families")
	assert.False(t, v1Families["c4"], "vmrs_gcp.yaml (ONTAP < 9.19.1) must not qualify c4 machine families")

	v2, err := LoadConfig("../../../config/vmrs_gcp_v2.yaml")
	require.NoError(t, err, "production vmrs_gcp_v2.yaml must load without error")
	v2Families := familiesIn(v2)
	assert.True(t, v2Families["c3"], "vmrs_gcp_v2.yaml must qualify c3 machine families")
	assert.True(t, v2Families["c4"], "vmrs_gcp_v2.yaml (ONTAP >= 9.19.1) must qualify c4 machine families")
}
