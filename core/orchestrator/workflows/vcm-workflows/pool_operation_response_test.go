package vcm_workflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
)

func TestPoolOperationResponsePayload_IncludesVmAndDataLifs(t *testing.T) {
	cfg := vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{
					Name:      "vm1",
					NodeIndex: 1,
					SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeNodeMgmt: {IP: "10.0.0.10"},
						vlm.LIFTypeCluster:  {IP: "198.18.0.10"},
					},
				},
				VM2: vlm.VMConfig{
					Name:      "vm2",
					NodeIndex: 2,
					SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeNodeMgmt: {IP: "10.0.0.11"},
					},
				},
			}},
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

	payload, err := poolOperationResponsePayload("gcnv-test", cfg, "gcnv-test-svm-01")
	require.NoError(t, err)
	require.NotNil(t, payload)

	assert.JSONEq(t, `{
		"pool": {
			"deploymentId": "gcnv-test",
			"forwardingRuleIp": "10.0.0.10",
			"ipConfig": {
				"vmLifs": [
					{"name":"vm1","clus":"198.18.0.10","nodemgmt":"10.0.0.10"},
					{"name":"vm2","nodemgmt":"10.0.0.11"}
				],
				"dataLif": {
					"san": ["10.33.182.252","10.33.182.249"],
					"ilbnas": "10.33.182.243"
				}
			}
		}
	}`, *payload)
}

func TestPoolOperationResponsePayload_VmLifsOnlyBeforeSVMCreate(t *testing.T) {
	cfg := vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{
					Name: "vm1",
					SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeNodeMgmt: {IP: "10.0.0.10"},
					},
				},
			}},
		},
	}

	payload, err := poolOperationResponsePayload("gcnv-test", cfg, "")
	require.NoError(t, err)
	require.NotNil(t, payload)

	assert.JSONEq(t, `{
		"pool": {
			"deploymentId": "gcnv-test",
			"forwardingRuleIp": "10.0.0.10",
			"ipConfig": {
				"vmLifs": [
					{"name":"vm1","nodemgmt":"10.0.0.10"}
				]
			}
		}
	}`, *payload)
}

func TestConfigureVCMVLMConfigForPoolCreate_EnablesIlbSupport(t *testing.T) {
	cfg := &vlm.VLMConfig{}
	configureVCMVLMConfigForPoolCreate(cfg)
	assert.True(t, cfg.Deployment.DevFlags.EnableIlbSupport)
	assert.True(t, cfg.Deployment.DeploymentConfigFlags.EnableIlbSupport)
	assert.Equal(t, "true", cfg.Deployment.DeploymentConfigFlags.EnableNfsV364BitIdentifier)
}
