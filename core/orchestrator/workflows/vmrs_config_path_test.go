package workflows

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	envs "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// TestResolveVMRSConfigPath verifies that the VMRS catalog is selected purely by
// the major.minor of the deployed ONTAP version:
// major.minor < 9.19 stays on the c3-only vmrsConfigPath (vmrs_gcp.yaml),
// major.minor >= 9.19 moves to the c3 + c4 vmrsConfigPathV2 (vmrs_gcp_v2.yaml), and
// an empty/garbage version falls back to the legacy c3-only catalog.
func TestResolveVMRSConfigPath(t *testing.T) {
	cases := []struct {
		name            string
		ontapVersion    string
		expectedPath    string
		expectedVersion string
	}{
		{name: "below threshold minor", ontapVersion: "9.18.1", expectedPath: vmrsConfigPath, expectedVersion: vmrsConfigVersionV1},
		{name: "below threshold with patch", ontapVersion: "9.18.1P3", expectedPath: vmrsConfigPath, expectedVersion: vmrsConfigVersionV1},
		{name: "exactly threshold", ontapVersion: "9.19.1", expectedPath: vmrsConfigPathV2, expectedVersion: vmrsConfigVersionV2},
		{name: "threshold with P patch", ontapVersion: "9.19.1P2", expectedPath: vmrsConfigPathV2, expectedVersion: vmrsConfigVersionV2},
		{name: "threshold with X build suffix", ontapVersion: "9.19.1x22", expectedPath: vmrsConfigPathV2, expectedVersion: vmrsConfigVersionV2},
		{name: "above threshold", ontapVersion: "9.20.1", expectedPath: vmrsConfigPathV2, expectedVersion: vmrsConfigVersionV2},
		{name: "two-part below threshold", ontapVersion: "9.18", expectedPath: vmrsConfigPath, expectedVersion: vmrsConfigVersionV1},
		// Only major.minor matters now, so a bare "9.19" is at the threshold -> v2.
		{name: "two-part equal to threshold", ontapVersion: "9.19", expectedPath: vmrsConfigPathV2, expectedVersion: vmrsConfigVersionV2},
		{name: "two-part above threshold", ontapVersion: "9.20", expectedPath: vmrsConfigPathV2, expectedVersion: vmrsConfigVersionV2},
		{name: "two-part with surrounding spaces", ontapVersion: "  9.20  ", expectedPath: vmrsConfigPathV2, expectedVersion: vmrsConfigVersionV2},
		// Float compare treats "9.9" as 9.90, i.e. >= 9.19, so it resolves to v2.
		{name: "single-digit minor treated as .90 under float compare", ontapVersion: "9.9.1", expectedPath: vmrsConfigPathV2, expectedVersion: vmrsConfigVersionV2},
		{name: "empty version falls back to legacy", ontapVersion: "", expectedPath: vmrsConfigPath, expectedVersion: vmrsConfigVersionV1},
		{name: "unparseable version falls back to legacy", ontapVersion: "not-a-version", expectedPath: vmrsConfigPath, expectedVersion: vmrsConfigVersionV1},
		{name: "one-part version falls back to legacy", ontapVersion: "9", expectedPath: vmrsConfigPath, expectedVersion: vmrsConfigVersionV1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, version := resolveVMRSConfigPath(tc.ontapVersion)
			assert.Equal(t, tc.expectedPath, path)
			assert.Equal(t, tc.expectedVersion, version)
		})
	}
}

// TestVMRSConfigVersionLabels pins the exact catalog filenames persisted to
// pool BuildInfo.VMRSVersion at create and cluster upgrade. These values are
// stored in the database, so an accidental rename would silently change what
// new pools record; this guards against that.
func TestVMRSConfigVersionLabels(t *testing.T) {
	assert.Equal(t, "vmrs_gcp.yaml", vmrsConfigVersionV1)
	assert.Equal(t, "vmrs_gcp_v2.yaml", vmrsConfigVersionV2)
	assert.Equal(t, vmrsConfigVersionV1, vmrsCatalogVersionLabel(vmrsConfigPath))
	assert.Equal(t, vmrsConfigVersionV2, vmrsCatalogVersionLabel(vmrsConfigPathV2))
}

// TestOntapVersionForVMRS_PoolUpdateUsesDeployedVersion verifies that pool
// update resolves the VMRS catalog from the pool's BuildInfo.OntapVersion even
// when the region's ONTAP_VERSION_DETAILS has moved ahead (e.g. 9.19.1).
func TestOntapVersionForVMRS_PoolUpdateUsesDeployedVersion(t *testing.T) {
	pool := &datamodel.Pool{
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			VMRSVersion:  vmrsConfigVersionV1,
		},
	}
	assert.Equal(t, "9.18.1", ontapVersionForVMRS(pool))

	path, version := resolveVMRSConfigPath(ontapVersionForVMRS(pool))
	assert.Equal(t, vmrsConfigPath, path)
	assert.Equal(t, vmrsConfigVersionV1, version)
}

func TestOntapVersionForVMRS_FallsBackToRegionWhenBuildInfoMissing(t *testing.T) {
	orig := envs.CurrentOntapVersionDetails
	envs.CurrentOntapVersionDetails = "9.19.1"
	t.Cleanup(func() { envs.CurrentOntapVersionDetails = orig })

	assert.Equal(t, "9.19.1", ontapVersionForVMRS(nil))
	assert.Equal(t, "9.19.1", ontapVersionForVMRS(&datamodel.Pool{}))
}

// TestOntapVersionForVMRS_RegionAheadOfPool verifies pool update catalog selection
// uses the pool's deployed ONTAP version even when the region has moved ahead.
func TestOntapVersionForVMRS_RegionAheadOfPool(t *testing.T) {
	orig := envs.CurrentOntapVersionDetails
	envs.CurrentOntapVersionDetails = "9.19.1"
	t.Cleanup(func() { envs.CurrentOntapVersionDetails = orig })

	pool := &datamodel.Pool{
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			VMRSVersion:  vmrsConfigVersionV1,
		},
	}

	assert.Equal(t, "9.18.1", ontapVersionForVMRS(pool))

	path, version := resolveVMRSConfigPath(ontapVersionForVMRS(pool))
	assert.Equal(t, vmrsConfigPath, path, "pool at 9.18.1 must stay on v1 catalog despite region at 9.19.1")
	assert.Equal(t, vmrsConfigVersionV1, version)
	assert.Equal(t, vmrsConfigVersionV1, pool.BuildInfo.VMRSVersion, "pool update must not mutate stored vmrsVersion")
}

func TestVmrsCatalogVersionLabel(t *testing.T) {
	assert.Equal(t, "vmrs_gcp.yaml", vmrsCatalogVersionLabel("/config/vmrs_gcp.yaml"))
	assert.Equal(t, "vmrs_gcp_v2.yaml", vmrsCatalogVersionLabel("/config/vmrs_gcp_v2.yaml"))
	assert.Equal(t, "custom_catalog.yaml", vmrsCatalogVersionLabel("/staging/custom_catalog.yaml"))
}

// TestVMRSVersionFromClusterUpgradeTarget documents the vmrsVersion written to
// buildInfo when cluster upgrade completes at the given target ONTAP version.
func TestVMRSVersionFromClusterUpgradeTarget(t *testing.T) {
	cases := []struct {
		name            string
		targetVersion   string
		expectedVMRS    string
		expectedCatalog string
	}{
		{name: "upgrade to 9.17.1 stays v1", targetVersion: "9.17.1", expectedVMRS: vmrsConfigVersionV1, expectedCatalog: vmrsConfigPath},
		{name: "upgrade to 9.18.1 stays v1", targetVersion: "9.18.1", expectedVMRS: vmrsConfigVersionV1, expectedCatalog: vmrsConfigPath},
		{name: "upgrade to 9.19.1 moves to v2", targetVersion: "9.19.1", expectedVMRS: vmrsConfigVersionV2, expectedCatalog: vmrsConfigPathV2},
		{name: "upgrade to 9.20.1 stays v2", targetVersion: "9.20.1", expectedVMRS: vmrsConfigVersionV2, expectedCatalog: vmrsConfigPathV2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, version := resolveVMRSConfigPath(tc.targetVersion)
			assert.Equal(t, tc.expectedCatalog, path)
			assert.Equal(t, tc.expectedVMRS, version)
		})
	}
}

// TestUpgradePhase_PersistsVMRSVersionFromTarget verifies cluster upgrade writes
// buildInfo.VMRSVersion from the target ONTAP version during the VSA upgrade build_info write.
func TestUpgradePhase_PersistsVMRSVersionFromTarget(t *testing.T) {
	cases := []struct {
		name          string
		targetVersion string
		expectedVMRS  string
		initialVMRS   string
	}{
		{
			name:          "upgrade below c4 threshold keeps v1 label",
			targetVersion: "9.18.1",
			expectedVMRS:  vmrsConfigVersionV1,
			initialVMRS:   vmrsConfigVersionV1,
		},
		{
			name:          "upgrade to c4 threshold moves to v2 label",
			targetVersion: "9.19.1",
			expectedVMRS:  vmrsConfigVersionV2,
			initialVMRS:   vmrsConfigVersionV1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()
			env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
			env.SetTestTimeout(time.Minute)

			mockVlm := &vlm.MockVlmWorkflowClient{}
			poolActivity := &activities.PoolActivity{}
			commonActivity := &activities.CommonActivities{}
			env.RegisterActivity(poolActivity.UpdatePoolFields)
			env.RegisterActivity(poolActivity.HasNasLifInVLMConfig)
			env.RegisterActivity(commonActivity.GenerateVSASignedURLActivity)
			env.RegisterActivity(commonActivity.GetNode)
			env.RegisterActivity(poolActivity.GetOntapVersion)

			liveOntap := tc.targetVersion
			var capturedBuildInfos []datamodel.PoolBuildInfo
			env.OnActivity(poolActivity.UpdatePoolFields, mock.Anything, mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					updates, ok := args[2].(map[string]interface{})
					if !ok {
						return
					}
					raw, ok := updates["build_info"]
					if !ok {
						return
					}
					encoded, err := json.Marshal(raw)
					if err != nil {
						return
					}
					var bi datamodel.PoolBuildInfo
					if err := json.Unmarshal(encoded, &bi); err != nil {
						return
					}
					capturedBuildInfos = append(capturedBuildInfos, bi)
				}).
				Return(nil)
			env.OnActivity(poolActivity.HasNasLifInVLMConfig, mock.Anything, mock.Anything).Return(false, nil)
			env.OnActivity(commonActivity.GenerateVSASignedURLActivity, mock.Anything, mock.Anything).Return("https://signed-url.example.com", nil)
			env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
			env.OnActivity(poolActivity.GetOntapVersion, mock.Anything, mock.Anything).Return(&liveOntap, nil)

			testWorkflow := func(ctx workflow.Context) error {
				ao := workflow.ActivityOptions{
					StartToCloseTimeout: 30 * time.Second,
					RetryPolicy: &temporal.RetryPolicy{
						MaximumAttempts:        1,
						NonRetryableErrorTypes: []string{"PanicError"},
					},
				}
				ctx = workflow.WithActivityOptions(ctx, ao)

				wf := &clusterUpgradeWorkflow{
					BaseWorkflow: BaseWorkflow{Logger: util.GetLogger(ctx)},
				}
				upgradeContext := &UpgradeContext{
					Params: &ClusterUpgradeWorkflowParams{
						JobID:             "test-job",
						ClusterID:         "test-cluster",
						TargetVersion:     tc.targetVersion,
						MediatorImageName: "mediator-image",
						VSAImageName:      "vsa-image",
						VSAImagePath:      "test-image.tgz",
					},
					NeedsMediatorUpgrade: true,
					NeedsVSAUpgrade:      true,
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid", ID: 1},
						BuildInfo: &datamodel.PoolBuildInfo{
							VSABuildImage:      "existing-vsa-image",
							MediatorBuildImage: "existing-mediator-image",
							OntapVersion:       "9.17.1",
							VMRSVersion:        tc.initialVMRS,
						},
						PoolCredentials: &datamodel.PoolCredentials{
							Password:      "password",
							SecretID:      "secret-id",
							CertificateID: "cert-id",
							AuthType:      1,
						},
						DeploymentName: "test-deployment",
					},
					CurrentVlmConfig: &vlm.VLMConfig{
						Cloud: vlm.CloudConfig{
							HAPairs: []vlm.HAPair{{
								VM1: vlm.VMConfig{SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
									vlm.LIFTypeNodeMgmt: {IP: "10.0.0.1"},
								}},
								VM2: vlm.VMConfig{SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
									vlm.LIFTypeNodeMgmt: {IP: "10.0.0.2"},
								}},
							}},
						},
					},
					Credentials: &vlm.OntapCredentials{AdminPassword: "password"},
					VlmClient:   mockVlm,
				}

				result, customErr := wf.upgradePhase(ctx, upgradeContext)
				if customErr != nil {
					return customErr
				}
				if !result.Success {
					return errors.New("upgrade failed")
				}
				return nil
			}

			mockVlm.On("UpgradeVSAMediatorWorkflow", mock.Anything, mock.Anything).
				Return(&vlm.UpdateMediatorResponse{VLMConfig: vlm.VLMConfig{}}, nil)
			mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
				Return(&vlm.UpgradeVSAClusterDeploymentResponse{
					VLMConfig:    vlm.VLMConfig{},
					OntapVersion: tc.targetVersion,
				}, nil)

			env.ExecuteWorkflow(testWorkflow)
			assert.True(t, env.IsWorkflowCompleted())
			assert.NoError(t, env.GetWorkflowError())
			require.Len(t, capturedBuildInfos, 2, "expected build_info writes after mediator and VSA upgrades")

			mediatorPhaseBuildInfo := capturedBuildInfos[0]
			assert.Equal(t, tc.targetVersion, mediatorPhaseBuildInfo.OntapVersion)
			assert.Empty(t, mediatorPhaseBuildInfo.VMRSVersion, "VMRS version is set only during VSA upgrade build_info write")

			vsaPhaseBuildInfo := capturedBuildInfos[1]
			assert.Equal(t, tc.targetVersion, vsaPhaseBuildInfo.OntapVersion)
			assert.Equal(t, tc.expectedVMRS, vsaPhaseBuildInfo.VMRSVersion)
			mockVlm.AssertExpectations(t)
		})
	}
}
