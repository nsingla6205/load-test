package vlm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSPConfig_DecodesAggrConfigs covers the schema VLM emits in
// vlm_config.deployment.spconfig.sp_ha_pair_config[].aggr_configs[]. After
// unifying the per-pair aggregate schema with the top-level data_aggr schema,
// the same DataAggrConfig type is used in both places: numeric `size` (uint64
// GB), `name` (NOT "aggr_name"), optional `uuid`, and `home_node`. The
// per-pair entries no longer carry standalone `iops` / `throughput` — those
// live at the pool-level SPConfig only.
func TestSPConfig_DecodesAggrConfigs(t *testing.T) {
	// Mirrors the post-unification VLM UpdateVSAClusterDeployment response.
	raw := []byte(`{
		"size": "3000Gi",
		"iops": 30512,
		"tput": 1907,
		"is_heterogeneous": false,
		"sp_ha_pair_config": [
			{
				"instance_type": "VM.DenseIO.E5.Flex"
			}
		]
	}`)

	var sp SPConfig
	require.NoError(t, json.Unmarshal(raw, &sp))
	assert.Equal(t, "3000Gi", sp.Size)
	assert.Equal(t, int64(30512), sp.IOps)
	assert.Equal(t, int64(1907), sp.Throughput)
	require.Len(t, sp.HAPairConfigs, 1)
	assert.Equal(t, "VM.DenseIO.E5.Flex", sp.HAPairConfigs[0].InstanceType)
}

// TestDataAggrConfig_DecodesNumericSize anchors the top-level data_aggr
// schema. Kept as a sibling to TestSPConfig_DecodesAggrConfigs even though
// the two now share DataAggrConfig — the value of the assertion is that the
// shared type successfully decodes both wire locations without divergence.
func TestDataAggrConfig_DecodesNumericSize(t *testing.T) {
	raw := []byte(`{
		"data_aggr": [
			{"name": "aggr_a", "uuid": "uuid-a", "size": 1024, "home_node": "node-1"}
		]
	}`)

	var cfg VLMConfig
	require.NoError(t, json.Unmarshal(raw, &cfg))
	require.Len(t, cfg.DataAggr, 1)
	assert.Equal(t, "aggr_a", cfg.DataAggr[0].Name)
	assert.Equal(t, "uuid-a", cfg.DataAggr[0].Aggruuid)
	assert.Equal(t, uint64(1024), cfg.DataAggr[0].Size)
	assert.Equal(t, "node-1", cfg.DataAggr[0].HomeNode)
}

// TestSPConfig_HomogeneousSendShape locks in what VCP emits on the wire today:
// the heterogeneous fields default to (false, nil) so the request is a clean
// homogeneous payload, and sp_ha_pair_config is omitted entirely via omitempty.
func TestSPConfig_HomogeneousSendShape(t *testing.T) {
	sp := SPConfig{Size: "3000GB", IOps: 1000, Throughput: 64}
	out, err := json.Marshal(sp)
	require.NoError(t, err)
	got := string(out)
	assert.Contains(t, got, `"is_heterogeneous":false`)
	assert.NotContains(t, got, `"sp_ha_pair_config"`)
}

// --- Provider factory tests ---

func resetFactory() {
	ResetActiveProvider()
}

func restoreOCIFactory() {
	SetActiveProvider(OCICloud)
}

func TestSetActiveProvider_OCI(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()

	SetActiveProvider(OCICloud)
	f, err := getFactory()
	require.NoError(t, err)
	assert.NotNil(t, f.NewProviderConfig)
	assert.NotNil(t, f.NewProviderDiskConfig)
	assert.NotNil(t, f.NewProviderNetworkConfig)
	assert.NotNil(t, f.NewProviderDevFlags)
	assert.NotNil(t, f.NewMediatorProviderConfig)
}

func TestSetActiveProvider_GCP(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()

	SetActiveProvider(GCPCloud)
	f, err := getFactory()
	require.NoError(t, err)

	cfg := f.NewProviderConfig()
	_, ok := cfg.(*GCPConfig)
	assert.True(t, ok)

	dc := f.NewProviderDiskConfig()
	_, ok = dc.(*GCPDiskConfig)
	assert.True(t, ok)

	nc := f.NewProviderNetworkConfig()
	_, ok = nc.(*GCPNetworkConfig)
	assert.True(t, ok)

	df := f.NewProviderDevFlags()
	_, ok = df.(*GCPDevFlags)
	assert.True(t, ok)

	mpc := f.NewMediatorProviderConfig()
	_, ok = mpc.(*GCPMediatorProviderConfig)
	assert.True(t, ok)
}

func TestSetActiveProvider_Unknown(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()

	SetActiveProvider("unknown-cloud")
	_, err := getFactory()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

// The provider is resolved from the environment on every lookup, so the most
// recent SetActiveProvider call wins rather than the first one.
func TestSetActiveProvider_LastCallWins(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()

	SetActiveProvider(OCICloud)
	SetActiveProvider(GCPCloud)

	f, err := getFactory()
	require.NoError(t, err)
	cfg := f.NewProviderConfig()
	_, ok := cfg.(*GCPConfig)
	assert.True(t, ok, "factory should follow the most recent SetActiveProvider call")
}

func TestResetActiveProvider(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()

	SetActiveProvider(OCICloud)
	ResetActiveProvider()

	_, err := getFactory()
	assert.Error(t, err)
}

func TestGetFactory_NoProviderReturnsError(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()

	_, err := getFactory()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

// --- ProviderConfigWrapper tests ---

func TestProviderConfigWrapper_UnmarshalJSON_OCI(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	data := []byte(`{"compartment_id":"ocid1.compartment.oc1..test","subnet_id":"ocid1.subnet.oc1..test"}`)
	var w ProviderConfigWrapper
	err := w.UnmarshalJSON(data)
	require.NoError(t, err)

	oci, err := w.AsOCI()
	require.NoError(t, err)
	assert.Equal(t, "ocid1.compartment.oc1..test", oci.CompartmentID)
	assert.Equal(t, "ocid1.subnet.oc1..test", oci.SubnetID)
}

func TestProviderConfigWrapper_UnmarshalJSON_GCP(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(GCPCloud)

	data := []byte(`{"project_id":"my-gcp-project","image_project_id":"img-project"}`)
	var w ProviderConfigWrapper
	err := w.UnmarshalJSON(data)
	require.NoError(t, err)

	gcp, err := w.AsGCP()
	require.NoError(t, err)
	assert.Equal(t, "my-gcp-project", gcp.ProjectID)
	assert.Equal(t, "img-project", gcp.ImageProjectID)
}

func TestProviderConfigWrapper_UnmarshalJSON_NoFactory(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()

	var w ProviderConfigWrapper
	err := w.UnmarshalJSON([]byte(`{}`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestProviderConfigWrapper_UnmarshalJSON_InvalidJSON(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	var w ProviderConfigWrapper
	err := w.UnmarshalJSON([]byte(`{invalid`))
	assert.Error(t, err)
}

func TestProviderConfigWrapper_MarshalJSON_Nil(t *testing.T) {
	w := ProviderConfigWrapper{}
	data, err := w.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, "null", string(data))
}

func TestProviderConfigWrapper_MarshalJSON_OCI(t *testing.T) {
	w := ProviderConfigWrapper{ProviderConfig: &OCIConfig{CompartmentID: "test-compartment"}}
	data, err := w.MarshalJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"compartment_id":"test-compartment"`)
}

func TestProviderConfigWrapper_AsOCI_WrongType(t *testing.T) {
	w := ProviderConfigWrapper{ProviderConfig: &GCPConfig{ProjectID: "proj"}}
	_, err := w.AsOCI()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected vlm.OCIConfig")
}

func TestProviderConfigWrapper_AsGCP_WrongType(t *testing.T) {
	w := ProviderConfigWrapper{ProviderConfig: &OCIConfig{CompartmentID: "comp"}}
	_, err := w.AsGCP()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected vlm.GCPConfig")
}

// An unset provider config is not an error: callers get the zero value so an
// absent provider_config behaves like an empty one.
func TestProviderConfigWrapper_AsOCI_Nil(t *testing.T) {
	w := ProviderConfigWrapper{}
	oci, err := w.AsOCI()
	require.NoError(t, err)
	assert.Equal(t, OCIConfig{}, oci)
}

// --- ProviderDiskConfigWrapper tests ---

func TestProviderDiskConfigWrapper_UnmarshalJSON_OCI(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	data := []byte(`{"device_name":"sda","availability_domain":"AD-1","vpus":20}`)
	var w ProviderDiskConfigWrapper
	err := w.UnmarshalJSON(data)
	require.NoError(t, err)

	oci, err := w.AsOCI()
	require.NoError(t, err)
	assert.Equal(t, "sda", oci.DeviceName)
	assert.Equal(t, "AD-1", oci.AvailabilityDomain)
	assert.Equal(t, int64(20), oci.Vpus)
}

func TestProviderDiskConfigWrapper_UnmarshalJSON_GCP(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(GCPCloud)

	data := []byte(`{"device_name":"disk-0"}`)
	var w ProviderDiskConfigWrapper
	err := w.UnmarshalJSON(data)
	require.NoError(t, err)

	gcp, err := w.AsGCP()
	require.NoError(t, err)
	assert.Equal(t, "disk-0", gcp.DeviceName)
}

func TestProviderDiskConfigWrapper_UnmarshalJSON_NoFactory(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()

	var w ProviderDiskConfigWrapper
	err := w.UnmarshalJSON([]byte(`{}`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestProviderDiskConfigWrapper_UnmarshalJSON_InvalidJSON(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	var w ProviderDiskConfigWrapper
	err := w.UnmarshalJSON([]byte(`not-json`))
	assert.Error(t, err)
}

func TestProviderDiskConfigWrapper_MarshalJSON_Nil(t *testing.T) {
	w := ProviderDiskConfigWrapper{}
	data, err := w.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, "null", string(data))
}

func TestProviderDiskConfigWrapper_MarshalJSON_OCI(t *testing.T) {
	w := ProviderDiskConfigWrapper{ProviderDiskConfig: &OCIDiskConfig{DeviceName: "sda"}}
	data, err := w.MarshalJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"device_name":"sda"`)
}

func TestProviderDiskConfigWrapper_AsOCI_WrongType(t *testing.T) {
	w := ProviderDiskConfigWrapper{ProviderDiskConfig: &GCPDiskConfig{DeviceName: "d"}}
	_, err := w.AsOCI()
	assert.Error(t, err)
}

func TestProviderDiskConfigWrapper_AsGCP_WrongType(t *testing.T) {
	w := ProviderDiskConfigWrapper{ProviderDiskConfig: &OCIDiskConfig{DeviceName: "d"}}
	_, err := w.AsGCP()
	assert.Error(t, err)
}

// --- ProviderNetworkConfigWrapper tests ---

func TestProviderNetworkConfigWrapper_UnmarshalJSON_OCI(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	data := []byte(`{"subnet_ocid":"ocid1.subnet.test"}`)
	var w ProviderNetworkConfigWrapper
	err := w.UnmarshalJSON(data)
	require.NoError(t, err)

	oci, err := w.AsOCI()
	require.NoError(t, err)
	assert.Equal(t, "ocid1.subnet.test", oci.SubnetOCID)
}

func TestProviderNetworkConfigWrapper_UnmarshalJSON_GCP(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(GCPCloud)

	data := []byte(`{"subnet_project_id":"gcp-proj-123"}`)
	var w ProviderNetworkConfigWrapper
	err := w.UnmarshalJSON(data)
	require.NoError(t, err)

	gcp, err := w.AsGCP()
	require.NoError(t, err)
	assert.Equal(t, "gcp-proj-123", gcp.SubnetProjectID)
}

func TestProviderNetworkConfigWrapper_UnmarshalJSON_NoFactory(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()

	var w ProviderNetworkConfigWrapper
	err := w.UnmarshalJSON([]byte(`{}`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestProviderNetworkConfigWrapper_UnmarshalJSON_InvalidJSON(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	var w ProviderNetworkConfigWrapper
	err := w.UnmarshalJSON([]byte(`[broken`))
	assert.Error(t, err)
}

func TestProviderNetworkConfigWrapper_MarshalJSON_Nil(t *testing.T) {
	w := ProviderNetworkConfigWrapper{}
	data, err := w.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, "null", string(data))
}

func TestProviderNetworkConfigWrapper_MarshalJSON_GCP(t *testing.T) {
	w := ProviderNetworkConfigWrapper{ProviderNetworkConfig: &GCPNetworkConfig{SubnetProjectID: "proj-456"}}
	data, err := w.MarshalJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"subnet_project_id":"proj-456"`)
}

func TestProviderNetworkConfigWrapper_AsOCI_WrongType(t *testing.T) {
	w := ProviderNetworkConfigWrapper{ProviderNetworkConfig: &GCPNetworkConfig{}}
	_, err := w.AsOCI()
	assert.Error(t, err)
}

func TestProviderNetworkConfigWrapper_AsGCP_WrongType(t *testing.T) {
	w := ProviderNetworkConfigWrapper{ProviderNetworkConfig: &OCINetworkConfig{}}
	_, err := w.AsGCP()
	assert.Error(t, err)
}

// --- ProviderDevFlagsWrapper tests ---

func TestProviderDevFlagsWrapper_UnmarshalJSON_OCI(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	data := []byte(`{"allow_non_dense_shape_for_vsa":true,"use_secondary_ips_for_lifs":true}`)
	var w ProviderDevFlagsWrapper
	err := w.UnmarshalJSON(data)
	require.NoError(t, err)

	oci, err := w.AsOCI()
	require.NoError(t, err)
	assert.True(t, oci.AllowNonDenseShapeForVsa)
	assert.True(t, oci.UseSecondaryIPsForLIFs)
}

func TestProviderDevFlagsWrapper_UnmarshalJSON_GCP(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(GCPCloud)

	data := []byte(`{}`)
	var w ProviderDevFlagsWrapper
	err := w.UnmarshalJSON(data)
	require.NoError(t, err)

	gcp, err := w.AsGCP()
	require.NoError(t, err)
	assert.Equal(t, GCPDevFlags{}, gcp)
}

func TestProviderDevFlagsWrapper_UnmarshalJSON_NoFactory(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()

	var w ProviderDevFlagsWrapper
	err := w.UnmarshalJSON([]byte(`{}`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestProviderDevFlagsWrapper_UnmarshalJSON_InvalidJSON(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	var w ProviderDevFlagsWrapper
	err := w.UnmarshalJSON([]byte(`!!!`))
	assert.Error(t, err)
}

func TestProviderDevFlagsWrapper_MarshalJSON_Nil(t *testing.T) {
	w := ProviderDevFlagsWrapper{}
	data, err := w.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, "null", string(data))
}

func TestProviderDevFlagsWrapper_MarshalJSON_OCI(t *testing.T) {
	w := ProviderDevFlagsWrapper{ProviderDevFlags: &OCIDevFlags{AllowNonDenseShapeForVsa: true}}
	data, err := w.MarshalJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"allow_non_dense_shape_for_vsa":true`)
}

func TestProviderDevFlagsWrapper_AsOCI_WrongType(t *testing.T) {
	w := ProviderDevFlagsWrapper{ProviderDevFlags: &GCPDevFlags{}}
	_, err := w.AsOCI()
	assert.Error(t, err)
}

func TestProviderDevFlagsWrapper_AsGCP_WrongType(t *testing.T) {
	w := ProviderDevFlagsWrapper{ProviderDevFlags: &OCIDevFlags{}}
	_, err := w.AsGCP()
	assert.Error(t, err)
}

func TestProviderDevFlagsWrapper_AsOCI_Nil(t *testing.T) {
	w := ProviderDevFlagsWrapper{}
	flags, err := w.AsOCI()
	require.NoError(t, err)
	assert.Equal(t, OCIDevFlags{}, flags)
}

// --- GetProvider / GetDiskConfigProvider / GetNetConfigProvider / GetDevFlagsProvider tests ---

func TestGetProvider(t *testing.T) {
	assert.Equal(t, GCPCloud, GCPConfig{}.GetProvider())
	assert.Equal(t, OCICloud, OCIConfig{}.GetProvider())
}

func TestGetDiskConfigProvider(t *testing.T) {
	assert.Equal(t, GCPCloud, GCPDiskConfig{}.GetDiskConfigProvider())
	assert.Equal(t, OCICloud, OCIDiskConfig{}.GetDiskConfigProvider())
}

func TestGetNetConfigProvider(t *testing.T) {
	assert.Equal(t, GCPCloud, GCPNetworkConfig{}.GetNetConfigProvider())
	assert.Equal(t, OCICloud, OCINetworkConfig{}.GetNetConfigProvider())
}

func TestGetDevFlagsProvider(t *testing.T) {
	assert.Equal(t, OCICloud, OCIDevFlags{}.GetDevFlagsProvider())
	assert.Equal(t, GCPCloud, GCPDevFlags{}.GetDevFlagsProvider())
}

// --- AsProviderType generic function tests ---

func TestAsProviderType_PointerToValue(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	cfg := &OCIConfig{CompartmentID: "ptr-test"}
	result, err := AsProviderType[OCIConfig](cfg)
	require.NoError(t, err)
	assert.Equal(t, "ptr-test", result.CompartmentID)
}

func TestAsProviderType_DirectValue(t *testing.T) {
	cfg := OCIConfig{CompartmentID: "val-test"}
	result, err := AsProviderType[OCIConfig](cfg)
	require.NoError(t, err)
	assert.Equal(t, "val-test", result.CompartmentID)
}

func TestAsProviderType_Nil(t *testing.T) {
	cfg, err := AsProviderType[OCIConfig](nil)
	require.NoError(t, err)
	assert.Equal(t, OCIConfig{}, cfg)
}

func TestAsProviderType_WrongType(t *testing.T) {
	_, err := AsProviderType[OCIConfig](&GCPConfig{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected vlm.OCIConfig")
}

// --- unmarshalWithFactory tests ---

func TestUnmarshalWithFactory_EmptyPayload(t *testing.T) {
	newOCI := func() any { return &OCIConfig{} }

	v, err := unmarshalWithFactory(newOCI, nil)
	require.NoError(t, err)
	assert.Nil(t, v)

	v, err = unmarshalWithFactory(newOCI, []byte(`null`))
	require.NoError(t, err)
	assert.Nil(t, v)
}

func TestUnmarshalWithFactory_InvalidJSON(t *testing.T) {
	_, err := unmarshalWithFactory(func() any { return &OCIConfig{} }, []byte(`{oops`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal")
}

// --- Round-trip marshal/unmarshal integration tests ---

func TestProviderConfigWrapper_RoundTrip_OCI(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	original := ProviderConfigWrapper{ProviderConfig: &OCIConfig{
		CompartmentID: "ocid1.compartment.oc1..roundtrip",
		SubnetID:      "ocid1.subnet.oc1..roundtrip",
	}}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ProviderConfigWrapper
	require.NoError(t, json.Unmarshal(data, &decoded))

	oci, err := decoded.AsOCI()
	require.NoError(t, err)
	assert.Equal(t, "ocid1.compartment.oc1..roundtrip", oci.CompartmentID)
	assert.Equal(t, "ocid1.subnet.oc1..roundtrip", oci.SubnetID)
}

func TestProviderDiskConfigWrapper_RoundTrip_GCP(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(GCPCloud)

	original := ProviderDiskConfigWrapper{ProviderDiskConfig: &GCPDiskConfig{DeviceName: "gcp-disk-1"}}
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ProviderDiskConfigWrapper
	require.NoError(t, json.Unmarshal(data, &decoded))

	gcp, err := decoded.AsGCP()
	require.NoError(t, err)
	assert.Equal(t, "gcp-disk-1", gcp.DeviceName)
}

func TestProviderNetworkConfigWrapper_RoundTrip_OCI(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	original := ProviderNetworkConfigWrapper{ProviderNetworkConfig: &OCINetworkConfig{SubnetOCID: "ocid1.subnet"}}
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ProviderNetworkConfigWrapper
	require.NoError(t, json.Unmarshal(data, &decoded))

	oci, err := decoded.AsOCI()
	require.NoError(t, err)
	assert.Equal(t, "ocid1.subnet", oci.SubnetOCID)
}

func TestProviderDevFlagsWrapper_RoundTrip_OCI(t *testing.T) {
	resetFactory()
	defer restoreOCIFactory()
	SetActiveProvider(OCICloud)

	original := ProviderDevFlagsWrapper{ProviderDevFlags: &OCIDevFlags{
		AllowNonDenseShapeForVsa: true,
		UseSecondaryIPsForLIFs:   true,
	}}
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ProviderDevFlagsWrapper
	require.NoError(t, json.Unmarshal(data, &decoded))

	oci, err := decoded.AsOCI()
	require.NoError(t, err)
	assert.True(t, oci.AllowNonDenseShapeForVsa)
	assert.True(t, oci.UseSecondaryIPsForLIFs)
}

// --- OCIRequestContext / OCIRequestContextIDs ---

func TestOCIRequestContext_GettersTrimSpace(t *testing.T) {
	ctx := OCIRequestContext{
		OpcWorkRequestID: "  wr-1  ",
		OpcRequestID:     "  req-1  ",
	}
	assert.Equal(t, "wr-1", ctx.GetOpcWorkRequestID())
	assert.Equal(t, "req-1", ctx.GetOpcRequestID())
}

func TestOCIRequestContextIDs_FromOCIConfig(t *testing.T) {
	pc := ProviderConfigWrapper{ProviderConfig: OCIConfig{
		OCIRequestContext: OCIRequestContext{
			OpcWorkRequestID: "wr-abc",
			OpcRequestID:     "req-xyz",
		},
	}}
	workRequestID, opcRequestID := OCIRequestContextIDs(pc)
	assert.Equal(t, "wr-abc", workRequestID)
	assert.Equal(t, "req-xyz", opcRequestID)
}

func TestOCIRequestContextIDs_NonOCIReturnsEmpty(t *testing.T) {
	pc := ProviderConfigWrapper{ProviderConfig: GCPConfig{ProjectID: "proj"}}
	workRequestID, opcRequestID := OCIRequestContextIDs(pc)
	assert.Empty(t, workRequestID)
	assert.Empty(t, opcRequestID)
}

func TestNetworkConfig_GCNVOmitsAzureConfig(t *testing.T) {
	cfg := NetworkConfig{
		Subnet:           "mgmt-e0a-subnet-01",
		VPC:              "mgmt-e0a-vpc-01",
		Gateway:          "198.18.0.1",
		GCPNetworkConfig: GCPNetworkConfig{SubnetProjectID: "gcnv-tenant"},
	}

	encoded, err := json.Marshal(cfg)
	require.NoError(t, err)
	text := string(encoded)
	assert.Contains(t, text, `"gcp_network_config":{"subnet_project_id":"gcnv-tenant"}`)
	assert.NotContains(t, text, "azure")
}

func TestNetworkConfig_LegacyEmptyAzureConfigIsIgnored(t *testing.T) {
	const oldGCNV = `{
		"subnet": "mgmt-e0a-subnet-01",
		"vpc": "mgmt-e0a-vpc-01",
		"gateway": "198.18.0.1",
		"gcp_network_config": {"subnet_project_id": "gcnv-tenant"},
		"azure_network_config": {}
	}`

	var cfg NetworkConfig
	require.NoError(t, json.Unmarshal([]byte(oldGCNV), &cfg))
	assert.Equal(t, "gcnv-tenant", cfg.GCPNetworkConfig.SubnetProjectID)

	encoded, err := json.Marshal(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"gcp_network_config"`)
	assert.NotContains(t, string(encoded), "azure")
}

func TestNetworkConfig_OCIOmitsEmptyGCPConfig(t *testing.T) {
	cfg := NetworkConfig{
		Subnet:  "ocid1.subnet.node",
		Gateway: "10.0.0.1",
	}

	encoded, err := json.Marshal(cfg)
	require.NoError(t, err)
	text := string(encoded)
	assert.Contains(t, text, `"subnet":"ocid1.subnet.node"`)
	assert.NotContains(t, text, "gcp_network_config")
	assert.NotContains(t, text, "subnet_project_id")
}

func TestNetworkConfig_LegacyEmptyGCPConfigIsDropped(t *testing.T) {
	const oldOCI = `{
		"subnet": "ocid1.subnet.node",
		"gateway": "10.0.0.1",
		"gcp_network_config": {"subnet_project_id": ""}
	}`

	var cfg NetworkConfig
	require.NoError(t, json.Unmarshal([]byte(oldOCI), &cfg))
	assert.Empty(t, cfg.GCPNetworkConfig.SubnetProjectID)

	encoded, err := json.Marshal(cfg)
	require.NoError(t, err)
	text := string(encoded)
	assert.Contains(t, text, `"subnet":"ocid1.subnet.node"`)
	assert.NotContains(t, text, "gcp_network_config")
}

func TestNetworkConfig_KeepsNonZeroGCPConfig(t *testing.T) {
	cfg := NetworkConfig{GCPNetworkConfig: GCPNetworkConfig{IsSecondaryIP: true}}

	encoded, err := json.Marshal(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"gcp_network_config":{"is_secondary_ip":true,"subnet_project_id":""}`)
}
