package workflowquery

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
)

func TestVsaClusterChildMetadataFromPayloads(t *testing.T) {
	t.Parallel()

	rawObj := map[string]interface{}{
		"vlm_config": map[string]interface{}{
			"cloud": map[string]interface{}{
				"ha_pair": []interface{}{
					map[string]interface{}{
						"vm1": map[string]interface{}{
							"name":              "FsnIdocnv-vm-01",
							"serial_number":     "1234501",
							"vsa_management_ip": "150.136.212.147",
							"vm_instance_id":    "ocid1.instance.oc1.iad.vm01",
							"lifs": map[string]interface{}{
								"intercluster":     map[string]interface{}{"ip": "10.38.25.146"},
								"nodemgmtinternal": map[string]interface{}{"ip": "10.38.0.1"},
							},
						},
						"vm2": map[string]interface{}{
							"name":              "FsnIdocnv-vm-02",
							"serial_number":     "1234502",
							"vsa_management_ip": "158.101.109.167",
							"vm_instance_id":    "ocid1.instance.oc1.iad.vm02",
							"lifs": map[string]interface{}{
								"intercluster":     map[string]interface{}{"ip": "10.38.1.218"},
								"nodemgmtinternal": map[string]interface{}{"ip": "10.38.0.2"},
							},
						},
						"mediator": map[string]interface{}{
							"name":           "FsnIdocnv-mediator-01",
							"vm_instance_id": "ocid1.instance.oc1.iad.mediator01",
							"lifs": map[string]interface{}{
								"intercluster": map[string]interface{}{"ip": ""},
								"rsm":          map[string]interface{}{"ip": "10.20.27.35"},
							},
						},
					},
				},
			},
		"deployment": map[string]interface{}{
			"labels": map[string]interface{}{
				"pool_uuid": "b5fb9baf-953b-9c65-19d5-31e3365cc2e3",
				"pool_ocid": "ocid1.pool.oc1.ashburn-1.testpool",
				"pool_name": "my-pool",
			},
				"spconfig": map[string]interface{}{
					"size": "5500Gi",
					"iops": 15248,
					"tput": 954,
				},
				"provider_config": map[string]interface{}{
					"cmek_ocid":     "ocid1.key.oc1.iad.testkey",
					"customer_nsgs": []interface{}{"ocid1.networksecuritygroup.oc1.iad.nsg1"},
					"customer_security_attributes": map[string]interface{}{
						"vcp_zpr": map[string]interface{}{
							"ns1": map[string]interface{}{
								"mode":  "enforce",
								"value": `{"app": {"value": "app1", "mode": "enforce"}}`,
							},
						},
					},
					"fabric_pool_config": map[string]interface{}{
						"bucket_name":    "bucket-test",
						"secret_ocid":    "ocid1.vaultsecret.oc1.iad.secret1",
						"secret_version": 7,
						"namespace":      "ns-test",
						"server_url":     "ns-test.compat.objectstorage.us-ashburn-1.oci.customer-oci.com",
					},
				},
			},
		},
		"workflow_type": "OCICreatePoolWorkflow",
	}
	body, err := json.Marshal(rawObj)
	require.NoError(t, err)

	out := vsaClusterChildMetadataFromPayloads([]*commonpb.Payload{{Data: body}})
	require.NotNil(t, out)
	require.Equal(t, "b5fb9baf-953b-9c65-19d5-31e3365cc2e3", out.PoolUUID)
	require.Equal(t, "ocid1.pool.oc1.ashburn-1.testpool", out.PoolOCID,
		"PoolOCID must be sourced from deployment.labels[pool_ocid] so the API response can echo the customer-provided pool OCID")
	require.Equal(t, "my-pool", out.ResourceName,
		"ResourceName must be sourced from deployment.labels[pool_name]")

	require.Equal(t, int64(15248), out.IOPS,
		"pool-level IOPS must be sourced from deployment.spconfig.iops, not per-VM data_disks")
	require.Equal(t, MBpsToGBps(954), out.ThroughputGBps,
		"pool-level throughput must be converted from deployment.spconfig.tput (decimal MB/s) to GBps")
	require.NotNil(t, out.Mediator, "mediator must be present when configured")
	require.Equal(t, "FsnIdocnv-mediator-01", out.Mediator.Name,
		"mediator name must be sourced from cloud.ha_pair[*].mediator.name (mediators are excluded from vms)")
	require.Equal(t, "10.20.27.35", out.Mediator.IP,
		"mediator ip must be sourced from cloud.ha_pair[*].mediator.lifs.rsm.ip")
	require.Equal(t, "ha_pair-1", out.Mediator.HAPair,
		"mediator on the first HA pair must report the 1-indexed label ha_pair-1")
	require.Equal(t, "ocid1.instance.oc1.iad.mediator01", out.Mediator.InstanceID,
		"mediator instance OCID must be sourced from cloud.ha_pair[*].mediator.vm_instance_id")

	require.Equal(t, []OCICreatePoolVMMetadata{
		{
			Name:            "FsnIdocnv-vm-01",
			SerialNumber:    "1234501",
			VSAManagementIP: "150.136.212.147",
			InterclusterIP:  "10.38.25.146",
			HAPair:          "ha_pair-1",
			InstanceID:      "ocid1.instance.oc1.iad.vm01",
		},
		{
			Name:            "FsnIdocnv-vm-02",
			SerialNumber:    "1234502",
			VSAManagementIP: "158.101.109.167",
			InterclusterIP:  "10.38.1.218",
			HAPair:          "ha_pair-1",
			InstanceID:      "ocid1.instance.oc1.iad.vm02",
		},
	}, out.Vms)

	require.Equal(t, "ocid1.key.oc1.iad.testkey", out.KmsID,
		"KmsID must be sourced from deployment.provider_config.cmek_ocid")
	require.Equal(t, []string{"ocid1.networksecuritygroup.oc1.iad.nsg1"}, out.NsgIDs,
		"NsgIDs must be sourced from deployment.provider_config.customer_nsgs")
	require.Equal(t, map[string]map[string]OCISecurityAttributeMetadata{
		"vcp_zpr": {
			"ns1": {Value: `{"app": {"value": "app1", "mode": "enforce"}}`, Mode: "enforce"},
		},
	}, out.SecurityAttributes,
		"SecurityAttributes must be sourced from deployment.provider_config.customer_security_attributes")
	require.NotNil(t, out.FabricPoolConfig,
		"FabricPoolConfig must be present when deployment.provider_config.fabric_pool_config is set")
	require.Equal(t, "bucket-test", out.FabricPoolConfig.BucketName)
	require.Equal(t, "ocid1.vaultsecret.oc1.iad.secret1", out.FabricPoolConfig.SecretOCID)
	require.NotNil(t, out.FabricPoolConfig.SecretVersion)
	require.Equal(t, int64(7), *out.FabricPoolConfig.SecretVersion)
	require.Equal(t, "ns-test", out.FabricPoolConfig.Namespace)
	require.Equal(t, "ns-test.compat.objectstorage.us-ashburn-1.oci.customer-oci.com", out.FabricPoolConfig.ServerURL)
}

func TestVsaClusterChildMetadataFromPayloads_NoProviderConfig(t *testing.T) {
	t.Parallel()
	rawObj := map[string]interface{}{
		"vlm_config": map[string]interface{}{
			"deployment": map[string]interface{}{
				"labels": map[string]interface{}{"pool_uuid": "u1"},
			},
		},
	}
	body, err := json.Marshal(rawObj)
	require.NoError(t, err)

	out := vsaClusterChildMetadataFromPayloads([]*commonpb.Payload{{Data: body}})
	require.NotNil(t, out)
	require.Empty(t, out.KmsID)
	require.Nil(t, out.NsgIDs)
	require.Nil(t, out.SecurityAttributes)
	require.Nil(t, out.FabricPoolConfig)
}

func TestVsaClusterChildMetadataFromPayloads_NoVlmConfigPassthrough(t *testing.T) {
	t.Parallel()
	in := map[string]interface{}{"cluster": "c1"}
	body, err := json.Marshal(in)
	require.NoError(t, err)
	out := vsaClusterChildMetadataFromPayloads([]*commonpb.Payload{{Data: body}})
	require.Nil(t, out)
}

func TestVsaClusterChildMetadataFromPayloads_Base64WrappedPayload(t *testing.T) {
	t.Parallel()
	inner := map[string]interface{}{
		"vlm_config": map[string]interface{}{
			"cloud": map[string]interface{}{
				"ha_pair": []interface{}{
					map[string]interface{}{
						"vm1": map[string]interface{}{
							"name":              "single-vm",
							"serial_number":     "9001",
							"vsa_management_ip": "10.0.0.3",
							"lifs": map[string]interface{}{
								"intercluster":     map[string]interface{}{"ip": "10.0.0.1"},
								"nodemgmtinternal": map[string]interface{}{"ip": "10.0.0.2"},
							},
						},
					},
				},
			},
		},
	}
	innerBytes, err := json.Marshal(inner)
	require.NoError(t, err)
	wrapped := []byte(base64.StdEncoding.EncodeToString(innerBytes))

	out := vsaClusterChildMetadataFromPayloads([]*commonpb.Payload{{Data: wrapped}})
	require.NotNil(t, out)
	require.Equal(t, []OCICreatePoolVMMetadata{
		{
			Name:            "single-vm",
			SerialNumber:    "9001",
			VSAManagementIP: "10.0.0.3",
			InterclusterIP:  "10.0.0.1",
			HAPair:          "ha_pair-1",
		},
	}, out.Vms)
}

func TestVsaClusterChildMetadataFromPayloads_InvalidJSON(t *testing.T) {
	t.Parallel()
	require.Nil(t, vsaClusterChildMetadataFromPayloads([]*commonpb.Payload{{Data: []byte(`not-json`)}}))
	require.Nil(t, vsaClusterChildMetadataFromPayloads([]*commonpb.Payload{{Data: []byte(`{"vlm_config":null}`)}}))
	require.Nil(t, vsaClusterChildMetadataFromPayloads([]*commonpb.Payload{{Data: []byte(`{"vlm_config":"not-an-object"}`)}}))
}
