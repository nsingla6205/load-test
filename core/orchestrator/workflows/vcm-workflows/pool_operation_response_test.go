package vcm_workflows

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestPoolOperationResponsePayload_FullLayout(t *testing.T) {
	cfg := vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{
					Name:            "vm1",
					NodeIndex:       1,
					Zone:            "us-central1-a",
					VMInstanceID:    "instance-1",
					VSAManagementIP: "10.0.0.10",
					SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeNodeMgmt:     {IP: "10.0.0.10"},
						vlm.LIFTypeCluster:      {IP: "198.18.0.10"},
						vlm.LIFTypeIC:           {IP: "198.18.32.7"},
						vlm.LIFTypeInterCluster: {IP: "10.10.0.40"},
						vlm.LIFTypeRSM:          {IP: "198.18.16.42"},
					},
					// The device names below are deliberately not reported:
					// gceDiskId stays empty until VLM supplies a real disk ID.
					SystemDisks: []vlm.DiskConfig{{
						Name:          "vm1-boot",
						Size:          100,
						Zone:          "us-central1-a",
						GCPDiskConfig: vlm.GCPDiskConfig{DeviceName: "persistent-disk-0"},
					}},
					DataDisks: []vlm.DiskConfig{{
						Name:          "vm1-data",
						Size:          1024,
						GCPDiskConfig: vlm.GCPDiskConfig{DeviceName: "persistent-disk-1"},
					}},
				},
				VM2: vlm.VMConfig{
					Name:            "vm2",
					NodeIndex:       2,
					Zone:            "us-central1-b",
					VMInstanceID:    "instance-2",
					VSAManagementIP: "10.0.0.11",
					SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeNodeMgmt: {IP: "10.0.0.11"},
					},
				},
				// The mediator is reported with only the fields it owns.
				Mediator: vlm.VMConfig{
					Name:         "mediator",
					IsMediator:   true,
					Zone:         "us-central1-c",
					VMInstanceID: "instance-mediator",
					SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeRSM: {IP: "198.18.16.41"},
					},
					SystemDisks: []vlm.DiskConfig{{Name: "mediator-boot", Size: 10}},
				},
			}},
		},
		Deployment: vlm.DeploymentConfig{
			Zone: vlm.ZoneInfo{
				Zone1:        "us-central1-a",
				Zone2:        "us-central1-b",
				MediatorZone: "us-central1-c",
			},
		},
		Svm: map[string]vlm.SvmConfig{
			"gcnv-test-svm-01": {
				SVMLIFs: vlm.SvmLIFConfigs{
					vlm.LIFTypeSan: {
						{IP: "10.33.182.252"},
						{IP: "10.33.182.249"},
					},
					vlm.LIFTypeIlbNas: {
						{IP: "10.33.182.243"},
					},
				},
			},
		},
	}

	params := &commonparams.VCMCreatePoolParams{
		ThroughputMibps:    2640,
		Iops:               12872,
		SizeInBytes:        1319413953331,
		QosType:            "AUTO",
		Mode:               "ONTAP",
		RegionIdentifier:   "us-c1",
		AutoTierBucketName: "gcnv-bucket-01",
	}

	payload, err := poolOperationResponsePayload(params, cfg, "gcnv-test-svm-01")
	require.NoError(t, err)
	require.NotNil(t, payload)

	assert.JSONEq(t, `{
		"clusterShape": {
			"provisionedThroughputMibps": 2640,
			"provisionedIops": 12872,
			"provisionedSizeInBytes": 1319413953331,
			"qosType": "AUTO",
			"mode": "ONTAP",
			"regionIdentifier": "us-c1",
			"autoTieringConfig": {
				"autoTierBucketName": "gcnv-bucket-01"
			}
		},
		"clusterLayout": {
			"nodes": [
				{
					"gceVmName": "vm1",
					"gceInstanceId": "instance-1",
					"nodeManagementIp": "10.0.0.10",
					"gceVmLocation": "us-central1-a",
					"ipConfig": {
						"clusterManagementLifIp": "198.18.0.10",
						"haInterconnectLifIp": "198.18.32.7",
						"interclusterLifIp": "10.10.0.40",
						"nodemgmtLifIp": "10.0.0.10",
						"rsmLifIp": "198.18.16.42",
						"dataLif": {
							"nasIlbIp": "10.33.182.243",
							"sanIps": ["10.33.182.252","10.33.182.249"]
						}
					}
				},
				{
					"gceVmName": "vm2",
					"gceInstanceId": "instance-2",
					"nodeManagementIp": "10.0.0.11",
					"gceVmLocation": "us-central1-b",
					"ipConfig": {
						"clusterManagementLifIp": "",
						"haInterconnectLifIp": "",
						"interclusterLifIp": "",
						"nodemgmtLifIp": "10.0.0.11",
						"rsmLifIp": "",
						"dataLif": {
							"nasIlbIp": "10.33.182.243",
							"sanIps": ["10.33.182.252","10.33.182.249"]
						}
					}
				},
				{
					"gceVmName": "mediator",
					"gceInstanceId": "instance-mediator",
					"nodeManagementIp": "",
					"gceVmLocation": "us-central1-c",
					"ipConfig": {
						"clusterManagementLifIp": "",
						"haInterconnectLifIp": "",
						"interclusterLifIp": "",
						"nodemgmtLifIp": "",
						"rsmLifIp": "198.18.16.41",
						"dataLif": {
							"nasIlbIp": "",
							"sanIps": []
						}
					}
				}
			],
			"disks": [
				{
					"name": "vm1-boot",
					"gceDiskId": "",
					"sizeBytes": "107374182400",
					"location": "us-central1-a"
				},
				{
					"name": "vm1-data",
					"gceDiskId": "",
					"sizeBytes": "1099511627776",
					"location": "us-central1-a"
				},
				{
					"name": "mediator-boot",
					"gceDiskId": "",
					"sizeBytes": "10737418240",
					"location": "us-central1-c"
				}
			],
			"activeZone": "us-central1-a",
			"standbyZone": "us-central1-b"
		}
	}`, *payload)
}

// Every key the layout contract requires is present even when the underlying
// config is empty, so CLH sees a stable shape rather than a varying one.
func TestPoolOperationResponsePayload_RequiredKeysAlwaysPresent(t *testing.T) {
	payload, err := poolOperationResponsePayload(&commonparams.VCMCreatePoolParams{}, vlm.VLMConfig{}, "")
	require.NoError(t, err)
	require.NotNil(t, payload)

	assert.JSONEq(t, `{
		"clusterShape": {
			"provisionedThroughputMibps": 0,
			"provisionedIops": 0,
			"provisionedSizeInBytes": 0,
			"qosType": "",
			"mode": "",
			"regionIdentifier": "",
			"autoTieringConfig": {
				"autoTierBucketName": ""
			}
		},
		"clusterLayout": {
			"nodes": [],
			"disks": [],
			"activeZone": "",
			"standbyZone": ""
		}
	}`, *payload)
}

// The shape echoes only what provisioning consumed. Fields the API accepts but
// the workflow ignores must not reappear as though they had taken effect.
func TestBuildClusterShape_OmitsFieldsTheWorkflowDiscards(t *testing.T) {
	shape := buildClusterShape(&commonparams.VCMCreatePoolParams{
		ThroughputMibps: 2640,
		Mode:            "ONTAP",
	})

	assert.Equal(t, int64(2640), shape["provisionedThroughputMibps"])
	assert.Equal(t, "ONTAP", shape["mode"])
	for _, discarded := range []string{
		"infrastructureSpec",
		"kmsConfig",
		"ontapModeRbac",
		"userThroughputMibps",
		"userIops",
		"userSizeInBytes",
	} {
		assert.NotContains(t, shape, discarded)
	}

	// Only the bucket name is read out of autoTieringConfig.
	assert.Equal(t, map[string]any{"autoTierBucketName": ""}, shape["autoTieringConfig"])
}

// A zero-valued VM slot is not yet provisioned and must not surface as a node,
// while the mediator is reported even though it holds almost nothing.
func TestPoolOperationResponsePayload_ExcludesUnprovisionedSlots(t *testing.T) {
	cfg := vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{Name: "vm1", VSAManagementIP: "10.0.0.10"},
				// VM2 is zero-valued: not yet provisioned.
				Mediator: vlm.VMConfig{IsMediator: true, VMInstanceID: "instance-mediator"},
			}},
		},
	}

	payload, err := poolOperationResponsePayload(nil, cfg, "")
	require.NoError(t, err)

	layout := decodeClusterLayout(t, *payload)
	nodes, ok := layout["nodes"].([]any)
	require.True(t, ok)
	require.Len(t, nodes, 2, "vm1 and the mediator, but not the empty VM2 slot")
	assert.Equal(t, "vm1", nodes[0].(map[string]any)["gceVmName"])
	assert.Equal(t, "mediator", nodes[1].(map[string]any)["gceVmName"],
		"an unnamed mediator falls back to its role as the display name")
}

// An HA pair with no mediator provisioned reports only its ONTAP nodes.
func TestPoolOperationResponsePayload_OmitsAbsentMediator(t *testing.T) {
	cfg := vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{Name: "vm1"},
				VM2: vlm.VMConfig{Name: "vm2"},
			}},
		},
	}

	payload, err := poolOperationResponsePayload(nil, cfg, "")
	require.NoError(t, err)

	layout := decodeClusterLayout(t, *payload)
	nodes := layout["nodes"].([]any)
	require.Len(t, nodes, 2)
	assert.Equal(t, "vm1", nodes[0].(map[string]any)["gceVmName"])
	assert.Equal(t, "vm2", nodes[1].(map[string]any)["gceVmName"])
}

// The data LIFs are SVM-scoped, so each node repeats the same set.
func TestPoolOperationResponsePayload_ReplicatesDataLifAcrossNodes(t *testing.T) {
	cfg := vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{Name: "vm1"},
				VM2: vlm.VMConfig{Name: "vm2"},
			}},
		},
		Svm: map[string]vlm.SvmConfig{
			"svm-01": {
				SVMLIFs: vlm.SvmLIFConfigs{
					vlm.LIFTypeIlbNas: {{IP: "10.33.182.243"}},
					vlm.LIFTypeSan:    {{IP: "10.33.182.252"}},
				},
			},
		},
	}

	payload, err := poolOperationResponsePayload(nil, cfg, "svm-01")
	require.NoError(t, err)

	layout := decodeClusterLayout(t, *payload)
	nodes, ok := layout["nodes"].([]any)
	require.True(t, ok)
	require.Len(t, nodes, 2)
	for _, node := range nodes {
		ipConfig := node.(map[string]any)["ipConfig"].(map[string]any)
		assert.Equal(t, map[string]any{
			"nasIlbIp": "10.33.182.243",
			"sanIps":   []any{"10.33.182.252"},
		}, ipConfig["dataLif"])
	}
}

func TestPoolOperationResponsePayload_DiskLocationFallsBackToVMZone(t *testing.T) {
	cfg := vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{
					Name: "vm1",
					Zone: "us-central1-a",
					DataDisks: []vlm.DiskConfig{
						{Name: "with-zone", Size: 1, Zone: "us-central1-f"},
						{Name: "without-zone", Size: 1},
					},
				},
			}},
		},
	}

	payload, err := poolOperationResponsePayload(nil, cfg, "")
	require.NoError(t, err)

	layout := decodeClusterLayout(t, *payload)
	disks, ok := layout["disks"].([]any)
	require.True(t, ok)
	require.Len(t, disks, 2)
	assert.Equal(t, "us-central1-f", disks[0].(map[string]any)["location"])
	assert.Equal(t, "us-central1-a", disks[1].(map[string]any)["location"])
}

// Disk sizes are recorded in GB and reported as a string-encoded byte count.
func TestPoolOperationResponsePayload_DiskSizeConvertedToBytes(t *testing.T) {
	cfg := vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{
					Name:      "vm1",
					DataDisks: []vlm.DiskConfig{{Name: "data", Size: 20}},
				},
			}},
		},
	}

	payload, err := poolOperationResponsePayload(nil, cfg, "")
	require.NoError(t, err)

	layout := decodeClusterLayout(t, *payload)
	disks := layout["disks"].([]any)
	require.Len(t, disks, 1)
	assert.Equal(t, "21474836480", disks[0].(map[string]any)["sizeBytes"])
}

// Mirrors a live GCP deployment: VLM reports disk sizes as GiB integers, the
// mediator carries only an RSM LIF and no management IP, and no GCE instance ID
// is present on any VM.
func TestPoolOperationResponsePayload_MatchesLiveGCPConfig(t *testing.T) {
	systemDisks := []vlm.DiskConfig{
		{Name: "gcnv-01-disk-boot", Size: 10, Zone: "us-central1-a"},
		{Name: "gcnv-01-disk-nvram", Size: 50, Zone: "us-central1-a"},
		{Name: "gcnv-01-disk-core", Size: 347, Zone: "us-central1-a"},
		{Name: "gcnv-01-disk-root", Size: 64, Zone: "us-central1-a",
			GCPDiskConfig: vlm.GCPDiskConfig{DeviceName: "pd-61e2ef72-01-disk-root"}},
		{Name: "gcnv-02-disk-rootcopy", Size: 64, Zone: "us-central1-a",
			GCPDiskConfig: vlm.GCPDiskConfig{DeviceName: "pd-ef72-02-disk-rootcopy"}},
	}
	dataDisks := make([]vlm.DiskConfig, 0, 8)
	for i := range 8 {
		dataDisks = append(dataDisks, vlm.DiskConfig{
			Name: fmt.Sprintf("gcnv-01-disk-data-%d", i),
			Size: 154,
			Zone: "us-central1-a",
		})
	}

	cfg := vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{
					Name:            "gcnv-588999861e2ef72-01",
					NodeIndex:       1,
					Zone:            "us-central1-a",
					VSAManagementIP: "35.238.14.93",
					SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeCluster:      {IP: "198.18.0.2"},
						vlm.LIFTypeIC:           {IP: "198.18.32.9"},
						vlm.LIFTypeInterCluster: {IP: "10.83.93.163"},
						vlm.LIFTypeNodeMgmt:     {IP: "35.238.14.93"},
						// Deliberately not surfaced: the contract has no field for it.
						vlm.LIFTypeNodeMgmtInternal: {IP: "198.18.0.4"},
						vlm.LIFTypeRSM:              {IP: "198.18.16.3"},
					},
					SystemDisks: systemDisks,
					DataDisks:   dataDisks,
				},
				Mediator: vlm.VMConfig{
					Name:       "gcnv-588999861e2ef72-mediator1",
					IsMediator: true,
					Zone:       "us-central1-a",
					SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeRSM: {IP: "198.18.16.4"},
					},
					SystemDisks: []vlm.DiskConfig{{Name: "mediator1-disk-boot", Size: 10}},
				},
			}},
		},
		Deployment: vlm.DeploymentConfig{
			Zone: vlm.ZoneInfo{Zone1: "us-central1-a", Zone2: "us-central1-c"},
		},
	}

	payload, err := poolOperationResponsePayload(nil, cfg, "")
	require.NoError(t, err)

	layout := decodeClusterLayout(t, *payload)

	nodes := layout["nodes"].([]any)
	require.Len(t, nodes, 2, "the ONTAP node and the mediator")
	node := nodes[0].(map[string]any)
	assert.Equal(t, "gcnv-588999861e2ef72-01", node["gceVmName"])
	assert.Equal(t, "us-central1-a", node["gceVmLocation"])
	// VLM records no GCE instance ID, so the field is present but empty.
	assert.Equal(t, "", node["gceInstanceId"])

	ipConfig := node["ipConfig"].(map[string]any)
	assert.Equal(t, "198.18.0.2", ipConfig["clusterManagementLifIp"])
	assert.Equal(t, "198.18.32.9", ipConfig["haInterconnectLifIp"])
	assert.Equal(t, "10.83.93.163", ipConfig["interclusterLifIp"])
	assert.Equal(t, "198.18.16.3", ipConfig["rsmLifIp"])
	// ext_ip_for_node_mgmt routes node management over the external address.
	assert.Equal(t, "35.238.14.93", ipConfig["nodemgmtLifIp"])
	assert.Equal(t, "35.238.14.93", node["nodeManagementIp"])

	// The mediator owns a name, a zone and an RSM LIF; everything else, including
	// its management IP and the SVM data addressing, is empty.
	mediator := nodes[1].(map[string]any)
	assert.Equal(t, "gcnv-588999861e2ef72-mediator1", mediator["gceVmName"])
	assert.Equal(t, "us-central1-a", mediator["gceVmLocation"])
	assert.Equal(t, "", mediator["gceInstanceId"])
	assert.Equal(t, "", mediator["nodeManagementIp"])
	assert.Equal(t, map[string]any{
		"clusterManagementLifIp": "",
		"haInterconnectLifIp":    "",
		"interclusterLifIp":      "",
		"nodemgmtLifIp":          "",
		"rsmLifIp":               "198.18.16.4",
		"dataLif":                map[string]any{"nasIlbIp": "", "sanIps": []any{}},
	}, mediator["ipConfig"])

	// 5 system + 8 data for the node, plus the mediator's boot disk.
	disks := layout["disks"].([]any)
	require.Len(t, disks, 14)
	byName := map[string]string{}
	for _, disk := range disks {
		entry := disk.(map[string]any)
		byName[entry["name"].(string)] = entry["sizeBytes"].(string)
	}
	assert.Equal(t, "10737418240", byName["gcnv-01-disk-boot"])    // 10 GiB
	assert.Equal(t, "53687091200", byName["gcnv-01-disk-nvram"])   // 50 GiB
	assert.Equal(t, "372588412928", byName["gcnv-01-disk-core"])   // 347 GiB
	assert.Equal(t, "165356240896", byName["gcnv-01-disk-data-0"]) // 154 GiB
	assert.Equal(t, "10737418240", byName["mediator1-disk-boot"])  // 10 GiB

	assert.Equal(t, "us-central1-a", layout["activeZone"])
	assert.Equal(t, "us-central1-c", layout["standbyZone"])
}

func decodeClusterLayout(t *testing.T, payload string) map[string]any {
	t.Helper()
	var decoded struct {
		ClusterLayout map[string]any `json:"clusterLayout"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &decoded))
	return decoded.ClusterLayout
}

func TestConfigureVCMVLMConfigForPoolCreate_EnablesIlbSupport(t *testing.T) {
	cfg := &vlm.VLMConfig{}
	cfg.Deployment.DevFlags.ExtIPForNodeMgmt = !extIPForNodeMgmt
	configureVCMVLMConfigForPoolCreate(cfg)
	assert.True(t, cfg.Deployment.DevFlags.EnableIlbSupport)
	assert.True(t, cfg.Deployment.DeploymentConfigFlags.EnableIlbSupport)
	assert.Equal(t, "true", cfg.Deployment.DeploymentConfigFlags.EnableNfsV364BitIdentifier)
	assert.Equal(t, extIPForNodeMgmt, cfg.Deployment.DevFlags.ExtIPForNodeMgmt)
}

func TestConfigureVCMVLMConfigForPoolUpdate_EnablesIlbSupport(t *testing.T) {
	cfg := &vlm.VLMConfig{}
	configureVCMVLMConfigForPoolUpdate(cfg)
	assert.True(t, cfg.Deployment.DevFlags.EnableIlbSupport)
	assert.True(t, cfg.Deployment.DeploymentConfigFlags.EnableIlbSupport)
	assert.Equal(t, "true", cfg.Deployment.DeploymentConfigFlags.EnableNfsV364BitIdentifier)
}

func TestConfigureVCMVLMConfigForPoolUpdate_SetsProviderFromEnv(t *testing.T) {
	prevHyperscaler := env.Hyperscaler
	t.Cleanup(func() { env.Hyperscaler = prevHyperscaler })
	env.Hyperscaler = vlm.GCPCloud

	cfg := &vlm.VLMConfig{}
	configureVCMVLMConfigForPoolUpdate(cfg)
	assert.Equal(t, vlm.GCPCloud, cfg.Deployment.Provider)
}

func TestConfigureVCMVLMConfigForPoolUpdate_KeepsExistingProvider(t *testing.T) {
	cfg := &vlm.VLMConfig{}
	cfg.Deployment.Provider = vlm.OCICloud
	configureVCMVLMConfigForPoolUpdate(cfg)
	assert.Equal(t, vlm.OCICloud, cfg.Deployment.Provider)
}
