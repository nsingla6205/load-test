package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	vmrs_oci "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	envs "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func init() {
	vlm.SetActiveProvider(vlm.OCICloud)
}

const (
	testVSAImageOCID      = "ocid1.image.oc1.iad.aaaaaaaaef2bc4g6vf4rvsa4vd2e4pnqw2ot2qicxrjo5a3ohglr6i4exdjq"
	testMediatorImageOCID = "ocid1.image.oc1.iad.aaaaaaaagakcrtyceuuvl6ts7xhqzzrdk3lv4z7tcqif3xpa6qsvppzflaaq"
	testOCIOntapVersion   = "9.18.1"
)

// withVSAImageOCIDs sets package-level image OCIDs for the test (init-time env is not re-read; tests must assign).
func withVSAImageOCIDs(t *testing.T, vsa, mediator string) {
	t.Helper()
	origV, origM := vsaImageName, vsaMediatorImageName
	vsaImageName, vsaMediatorImageName = vsa, mediator
	t.Cleanup(func() {
		vsaImageName, vsaMediatorImageName = origV, origM
	})
}

// withOCIOntapVersionDetails pins the package-level ONTAP version source that
// utils.GetOntapVersionBasedOnAllowlisting reads from, and clears the
// experimental override so non-allowlisted accounts deterministically receive
// `current` regardless of the test's account name. Originals are restored via
// t.Cleanup.
func withOCIOntapVersionDetails(t *testing.T, current string) {
	t.Helper()
	origCurrent := envs.CurrentOntapVersionDetails
	origExperimental := envs.ExperimentalOntapVersionDetails
	envs.CurrentOntapVersionDetails = current
	envs.ExperimentalOntapVersionDetails = ""
	t.Cleanup(func() {
		envs.CurrentOntapVersionDetails = origCurrent
		envs.ExperimentalOntapVersionDetails = origExperimental
	})
}

func setTestOCIImageEnv(t *testing.T) {
	t.Helper()
	withVSAImageOCIDs(t, testVSAImageOCID, testMediatorImageOCID)
	withOCIOntapVersionDetails(t, testOCIOntapVersion)
}

func registerOCIUpdatePoolNodePersistMocks(env *testsuite.TestWorkflowEnvironment, nodes []*datamodel.Node) {
	if nodes == nil {
		nodes = []*datamodel.Node{}
	}
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(nodes, nil)
	env.OnActivity("UpdateOCINodesFromVLMConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("RestoreOCINodesPreUpdateFields", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
}

// setOCIExpertModePassword overrides the package-level ociExpertModePassword
// so the workflow skips the GetExpertModeCredentialsForOCI activity and
// uses the preset password directly. The original value is restored via t.Cleanup.
func setOCIExpertModePassword(t *testing.T, pw string) {
	t.Helper()
	orig := ociExpertModePassword
	ociExpertModePassword = pw
	t.Cleanup(func() { ociExpertModePassword = orig })
}

// withOCIVMRSEnabled overrides the package-level ociVMRSEnabled toggle so a
// test can drive OCICreatePoolWorkflow's VMRS branch (workflow.SideEffect
// captures the toggle at start; this helper flips the value the SideEffect
// observes). Original value is restored via t.Cleanup.
func withOCIVMRSEnabled(t *testing.T, enabled bool) {
	t.Helper()
	orig := ociVMRSEnabled
	ociVMRSEnabled = enabled
	t.Cleanup(func() { ociVMRSEnabled = orig })
}

// registerOCICreatePoolVLMRollbackWorkflows registers the VLM delete child workflow used when OCICreatePoolWorkflow
// rolls back after CreateVSAClusterDeployment (or later steps) fail.
func registerOCICreatePoolVLMRollbackWorkflows(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, req *vlm.DeleteVSAClusterDeploymentRequest, _ string) error {
			return nil
		},
		workflow.RegisterOptions{Name: vlm.DeleteVSAClusterDeploymentWorkflowName},
	)
}

func withOCIWorkerStartupEnv(t *testing.T, vsa, mediator, adminPassword, region, secret string) {
	t.Helper()
	origVsa, origMediator := vsaImageName, vsaMediatorImageName
	origAdminPassword, origRegion, origSecret := ociOntapAdminPassword, localRegion, secretURI
	vsaImageName = vsa
	vsaMediatorImageName = mediator
	ociOntapAdminPassword = adminPassword
	localRegion = region
	secretURI = secret
	t.Cleanup(func() {
		vsaImageName, vsaMediatorImageName = origVsa, origMediator
		ociOntapAdminPassword, localRegion, secretURI = origAdminPassword, origRegion, origSecret
	})
}

// registerOCICreatePoolOntapCredentialMocks stubs OCI Vault admin credential activities so tests do
// not call real OCI APIs. The OCI create-pool workflow always invokes CreateOnTapCredentialsForOCI
// (the activity itself decides whether to provision a vault secret based on pool.PoolCredentials.AuthType).
// Call only after env.RegisterActivity(&activities.PoolActivity{...}) (Temporal test suite requirement).
func registerOCICreatePoolOntapCredentialMocks(env *testsuite.TestWorkflowEnvironment, mockStorage *database.MockStorage) {
	env.OnActivity("CreateOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&activities.OCICreatePoolCredentials{
			OntapCredentials: vlm.OntapCredentials{AdminPassword: "test-ontap-admin-password"},
			Secret:           &datamodel.ExternalCredRef{Name: "test-secret", Version: 1, ExternalIdentifier: "ocid1.vaultsecret.oc1..testsecretocid"},
		}, nil).Maybe()
	env.OnActivity("DeleteOnTapCredentialsForOCI", mock.Anything, mock.Anything).Return(nil).Maybe()
	// Separate client mTLS cert setup is on by default; stub it so cert-auth
	// create-pool workflow tests exercise the step without calling real OCI/ONTAP.
	env.OnActivity("SetupClientCertificateForOCI", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.ExternalCredRef{Name: "test-client-cert", Version: 1, ExternalIdentifier: "ocid1.certificate.oc1..testclientcert"}, nil).Maybe()
	mockStorage.On("GetFaultDomainCountsByZone", mock.Anything).Return(map[string]map[string]int64{}, nil).Maybe()
}

func TestValidateOCIWorkerStartupEnv(t *testing.T) {
	t.Run("ok when all required vars are present", func(t *testing.T) {
		withOCIWorkerStartupEnv(t, testVSAImageOCID, testMediatorImageOCID, "Netapp1!", "us-ashburn-1", "ocid1.vaultsecret.oc1..aaa")
		assert.NoError(t, ValidateOCIWorkerStartupEnv())
	})

	t.Run("fails and lists missing vars", func(t *testing.T) {
		withOCIWorkerStartupEnv(t, "", "", "", "", "")
		err := ValidateOCIWorkerStartupEnv()
		assert.Error(t, err)
		assert.True(t, utilserrors.IsUserInputValidationErr(err))
		assert.Contains(t, err.Error(), "VSA_IMAGE_NAME")
		assert.Contains(t, err.Error(), "VSA_MEDIATOR_IMAGE_NAME")
		assert.Contains(t, err.Error(), "LOCAL_REGION")
		assert.Contains(t, err.Error(), "SECRET_URI")
	})

	t.Run("fails when dataDiskCount is not positive", func(t *testing.T) {
		withOCIWorkerStartupEnv(t, testVSAImageOCID, testMediatorImageOCID, "Netapp1!", "us-ashburn-1", "ocid1.vaultsecret.oc1..aaa")
		origDataDiskCount := dataDiskCount
		dataDiskCount = 0
		t.Cleanup(func() { dataDiskCount = origDataDiskCount })
		err := ValidateOCIWorkerStartupEnv()
		assert.Error(t, err)
		assert.True(t, utilserrors.IsUserInputValidationErr(err))
		assert.Contains(t, err.Error(), "OCI_VSA_DATA_DISK_COUNT must be > 0")
	})

	t.Run("fails when VSA_IMAGE_NAME is not a valid OCID", func(t *testing.T) {
		withOCIWorkerStartupEnv(t, "not-an-ocid", testMediatorImageOCID, "Netapp1!", "us-ashburn-1", "ocid1.vaultsecret.oc1..aaa")
		err := ValidateOCIWorkerStartupEnv()
		assert.Error(t, err)
		assert.True(t, utilserrors.IsUserInputValidationErr(err))
		assert.Contains(t, err.Error(), "not valid OCIDs")
		assert.Contains(t, err.Error(), "VSA_IMAGE_NAME")
	})

	t.Run("fails when VSA_MEDIATOR_IMAGE_NAME is not a valid OCID", func(t *testing.T) {
		withOCIWorkerStartupEnv(t, testVSAImageOCID, "bad-mediator", "Netapp1!", "us-ashburn-1", "ocid1.vaultsecret.oc1..aaa")
		err := ValidateOCIWorkerStartupEnv()
		assert.Error(t, err)
		assert.True(t, utilserrors.IsUserInputValidationErr(err))
		assert.Contains(t, err.Error(), "not valid OCIDs")
		assert.Contains(t, err.Error(), "VSA_MEDIATOR_IMAGE_NAME")
	})

	t.Run("fails when both image env vars are invalid OCIDs", func(t *testing.T) {
		withOCIWorkerStartupEnv(t, "bad-vsa", "bad-mediator", "Netapp1!", "us-ashburn-1", "ocid1.vaultsecret.oc1..aaa")
		err := ValidateOCIWorkerStartupEnv()
		assert.Error(t, err)
		assert.True(t, utilserrors.IsUserInputValidationErr(err))
		assert.Contains(t, err.Error(), "VSA_IMAGE_NAME")
		assert.Contains(t, err.Error(), "VSA_MEDIATOR_IMAGE_NAME")
	})
}

func TestPrepareVLMConfig_CustomPerformanceAndHardcodedSerialPrefix(t *testing.T) {
	setTestOCIImageEnv(t)
	iops := int64(5000)
	params := &common.CreatePoolParams{
		AccountName:     "acct",
		SizeInBytes:     300 * 1024 * 1024 * 1024,
		PrimaryZone:     "ad1",
		SecondaryZone:   "ad2",
		MediatorZone:    "ad3",
		VendorSubNetID:  "subnet",
		CompartmentOCID: "comp",
		HAPairs:         1,
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 128,
			Iops:            &iops,
		},
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		DeploymentName: "dep1",
		Name:           "pool1",
		Account:        &datamodel.Account{Name: "acct"},
	}
	cfg, err := prepareVLMConfig(params, pool, nil)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, int64(128), cfg.Deployment.SPConfig.Throughput)
	assert.Equal(t, int64(5000), cfg.Deployment.SPConfig.IOps)

	assert.Equal(t, ociSerialNumberLeadingPrefix+ociSerialNumberPrefix, cfg.Deployment.SerialNumberPrefix,
		"SerialNumberPrefix must be the hardcoded \"955\"+15 zeros emitted by the workflow; "+
			"the API field is currently ignored end-to-end and the workflow is the single source of truth")
}

func TestPrepareVLMConfig_DerivesIopsFromThroughputWhenNil(t *testing.T) {
	setTestOCIImageEnv(t)
	params := &common.CreatePoolParams{
		AccountName:     "acct",
		SizeInBytes:     300 * 1024 * 1024 * 1024,
		PrimaryZone:     "ad1",
		SecondaryZone:   "ad2",
		MediatorZone:    "ad3",
		VendorSubNetID:  "subnet",
		CompartmentOCID: "comp",
		HAPairs:         1,
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 128,
			Iops:            nil, // derived by the validator
		},
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		DeploymentName: "dep1",
		Name:           "pool1",
		Account:        &datamodel.Account{Name: "acct"},
	}

	cfg, err := prepareVLMConfig(params, pool, nil)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	const expectedDerivedIOPS = int64(2048)
	assert.Equal(t, int64(128), cfg.Deployment.SPConfig.Throughput)
	assert.Equal(t, expectedDerivedIOPS, cfg.Deployment.SPConfig.IOps,
		"SPConfig.IOps must equal the validator-derived IOPS (ThroughputMibps * IopsPerMiBps)")
}

func TestPrepareVLMConfig_ReturnsErrorWhenIopsValidationFails(t *testing.T) {
	setTestOCIImageEnv(t)
	belowMin := int64(100) // below MinCustomIops (1024)
	params := &common.CreatePoolParams{
		AccountName:     "acct",
		SizeInBytes:     300 * 1024 * 1024 * 1024,
		PrimaryZone:     "ad1",
		SecondaryZone:   "ad2",
		MediatorZone:    "ad3",
		VendorSubNetID:  "subnet",
		CompartmentOCID: "comp",
		HAPairs:         1,
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 128,
			Iops:            &belowMin,
		},
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		DeploymentName: "dep1",
		Name:           "pool1",
		Account:        &datamodel.Account{Name: "acct"},
	}

	cfg, err := prepareVLMConfig(params, pool, nil)
	assert.Error(t, err, "validator must reject IOPS below MinCustomIops")
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "derive iops from throughput")
}

func TestPrepareVLMConfig_RejectsZeroHAPairs(t *testing.T) {
	setTestOCIImageEnv(t)
	iops := int64(5000)
	params := &common.CreatePoolParams{
		AccountName:     "acct",
		SizeInBytes:     100 * 1024 * 1024 * 1024,
		PrimaryZone:     "ad1",
		SecondaryZone:   "ad2",
		MediatorZone:    "ad3",
		VendorSubNetID:  "subnet",
		CompartmentOCID: "comp",
		// HAPairs intentionally omitted (zero value).
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 128,
			Iops:            &iops,
		},
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		DeploymentName: "dep1",
		Name:           "pool1",
		Account:        &datamodel.Account{Name: "acct"},
	}

	cfg, err := prepareVLMConfig(params, pool, nil)
	require.Error(t, err, "zero HAPairs must be rejected")
	assert.Nil(t, cfg)
	assert.True(t, utilserrors.IsUserInputValidationErr(err),
		"error must be a UserInputValidationErr so it surfaces as a 4xx, not a 5xx")
	assert.Contains(t, err.Error(), "haPairs",
		"error message must mention the offending field")
}

// TestPrepareVLMConfig_RejectsPerDiskSizeBelowMinimum locks the VCP-side
// guard that blocks pools whose per-disk size (totalGi / totalActiveVMs /
// dataDiskCount, where totalActiveVMs = haPairCount * activeVMsPerPair) is
// strictly below OCI_VSA_MIN_PER_DISK_SIZE_GI. Values equal to the minimum
// are accepted. This mirrors the VLM-side rejection so callers get a fast
// 4xx instead of a late failure deep in provisioning. dataDiskCount is
// pinned so the test does not depend on the process-wide env default.
//
// Non-shared HA (AP, IsRegionalHA=true) runs 1 active VM per pair;
// shared HA (AA, IsRegionalHA=false) runs 2 — activeVMsPerPair must double
// accordingly (same split as computeOCIPerVMRSInput), so both deployment
// types are covered here with distinct expected values.
func TestPrepareVLMConfig_RejectsPerDiskSizeBelowMinimum(t *testing.T) {
	setTestOCIImageEnv(t)

	origDDC := dataDiskCount
	dataDiskCount = 2
	t.Cleanup(func() { dataDiskCount = origDDC })

	origMin := minPerDiskSizeGi
	minPerDiskSizeGi = 50
	t.Cleanup(func() { minPerDiskSizeGi = origMin })

	iops := int64(5000)
	newParams := func(sizeInBytes uint64, haPairs uint64, isRegionalHA bool) *common.CreatePoolParams {
		return &common.CreatePoolParams{
			AccountName:     "acct",
			SizeInBytes:     sizeInBytes,
			PrimaryZone:     "ad1",
			SecondaryZone:   "ad2",
			MediatorZone:    "ad3",
			VendorSubNetID:  "subnet",
			CompartmentOCID: "comp",
			HAPairs:         haPairs,
			IsRegionalHA:    isRegionalHA,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            &iops,
			},
		}
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		DeploymentName: "dep1",
		Name:           "pool1",
		Account:        &datamodel.Account{Name: "acct"},
	}

	cases := []struct {
		name         string
		sizeInBytes  uint64
		haPairs      uint64
		isRegionalHA bool
		wantErr      bool
	}{
		// Non-shared HA (AP): 1 active VM per pair -> totalActiveVMs = haPairs.
		// 98Gi / 1 / 2 = 49Gi per disk -> 49 < 50 -> reject.
		{name: "AP_BelowMinimum", sizeInBytes: 98 * 1024 * 1024 * 1024, haPairs: 1, isRegionalHA: true, wantErr: true},
		// 196Gi / 2 / 2 = 49Gi per disk -> 49 < 50 -> reject.
		{name: "AP_BelowMinimumMultiHA", sizeInBytes: 196 * 1024 * 1024 * 1024, haPairs: 2, isRegionalHA: true, wantErr: true},
		// 100Gi / 1 / 2 = 50Gi per disk -> 50 == 50 -> accept (at minimum is allowed).
		{name: "AP_AtMinimum", sizeInBytes: 100 * 1024 * 1024 * 1024, haPairs: 1, isRegionalHA: true, wantErr: false},
		// 200Gi / 2 / 2 = 50Gi per disk -> 50 == 50 -> accept.
		{name: "AP_AtMinimumMultiHA", sizeInBytes: 200 * 1024 * 1024 * 1024, haPairs: 2, isRegionalHA: true, wantErr: false},
		// 102Gi / 1 / 2 = 51Gi per disk -> 51 > 50 -> accept.
		{name: "AP_AboveMinimum", sizeInBytes: 102 * 1024 * 1024 * 1024, haPairs: 1, isRegionalHA: true, wantErr: false},

		// Shared HA (AA): 2 active VMs per pair -> totalActiveVMs = haPairs * 2.
		// A size that would pass the (buggy) undoubled AP formula must still
		// reject here: 100Gi / (1*2) / 2 = 25Gi -> 25 < 50 -> reject.
		{name: "AA_BelowMinimum_WouldPassIfUndoubled", sizeInBytes: 100 * 1024 * 1024 * 1024, haPairs: 1, isRegionalHA: false, wantErr: true},
		// 200Gi / (1*2) / 2 = 50Gi per disk -> 50 == 50 -> accept.
		{name: "AA_AtMinimum", sizeInBytes: 200 * 1024 * 1024 * 1024, haPairs: 1, isRegionalHA: false, wantErr: false},
		// 400Gi / (2*2) / 2 = 50Gi per disk -> 50 == 50 -> accept.
		{name: "AA_AtMinimumMultiHA", sizeInBytes: 400 * 1024 * 1024 * 1024, haPairs: 2, isRegionalHA: false, wantErr: false},
		// 204Gi / (1*2) / 2 = 51Gi per disk -> 51 > 50 -> accept.
		{name: "AA_AboveMinimum", sizeInBytes: 204 * 1024 * 1024 * 1024, haPairs: 1, isRegionalHA: false, wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			cfg, err := prepareVLMConfig(newParams(tc.sizeInBytes, tc.haPairs, tc.isRegionalHA), pool, nil)
			if !tc.wantErr {
				require.NoError(tt, err)
				require.NotNil(tt, cfg)
				return
			}
			require.Error(tt, err, "per-disk size below the minimum must be rejected")
			assert.Nil(tt, cfg)
			assert.True(tt, utilserrors.IsUserInputValidationErr(err),
				"error must be a UserInputValidationErr so it surfaces as a 4xx, not a 5xx")
			assert.Contains(tt, err.Error(), "per-disk size",
				"error message must explain the per-disk size violation")
		})
	}
}

// TestComputeOCIVMRSInput_RejectsMissingOrZeroThroughput is the
// regression guard for the nil-deref panic that existed when
// computeOCIVMRSInput read params.CustomPerformanceParams.ThroughputMibps
// without a nil check. The headline case is the nil sub-test: previously
// it crashed the workflow worker (Temporal would mark the activity as
// non-deterministic and the workflow would get stuck); now it must
// return a user-input validation error so the API surfaces a 4xx.
// The zero-throughput sub-test is the sibling: a present-but-empty
// CustomPerformanceParams would have sailed through here and tripped
// Select's "DesiredThroughputGBs must be > 0" deeper in the stack with
// a less-actionable error.
func TestComputeOCIVMRSInput_RejectsMissingOrZeroThroughput(t *testing.T) {
	// Pin dataDiskCount so this test doesn't depend on
	// OCI_VSA_DATA_DISK_COUNT in the test runner's environment.
	origDDC := dataDiskCount
	dataDiskCount = 2
	t.Cleanup(func() { dataDiskCount = origDDC })

	base := func() *common.CreatePoolParams {
		return &common.CreatePoolParams{
			HAPairs:     2,
			SizeInBytes: 4 * 1_000_000_000_000, // 4 decimal TB
		}
	}

	cases := []struct {
		name           string
		mutate         func(*common.CreatePoolParams)
		wantMsgInclude string
	}{
		{
			name: "NilCustomPerformanceParams",
			mutate: func(p *common.CreatePoolParams) {
				p.CustomPerformanceParams = nil
			},
			wantMsgInclude: "customPerformanceParams is required",
		},
		{
			name: "ZeroThroughputMibps",
			mutate: func(p *common.CreatePoolParams) {
				p.CustomPerformanceParams = &common.CustomPerformanceParams{ThroughputMibps: 0}
			},
			wantMsgInclude: "throughputMibps must be > 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			params := base()
			tc.mutate(params)

			perVMCap, perVMThru, err := computeOCIVMRSInput(params)

			require.Error(tt, err, "must reject before dereferencing throughput")
			assert.Zero(tt, perVMCap)
			assert.Zero(tt, perVMThru)
			assert.True(tt, utilserrors.IsUserInputValidationErr(err),
				"error must be a UserInputValidationErr so it surfaces as a 4xx, not a panic")
			assert.Contains(tt, err.Error(), tc.wantMsgInclude,
				"error message must point at the offending field")
		})
	}
}

// TestComputeOCIVMRSInput_RejectsTopologyInputs covers the
// shape/size validators that fire BEFORE CustomPerformanceParams is read.
// Each sub-test exercises exactly one invalid input; the others stay valid
// so the failing field is the only candidate, anchoring both the precondition
// order and the error message that surfaces.
// Note: dataDiskCount validation is tested in TestValidateOCIWorkerStartupEnv
// since it's checked at startup rather than during request processing.
func TestComputeOCIVMRSInput_RejectsTopologyInputs(t *testing.T) {
	origDDC := dataDiskCount
	t.Cleanup(func() { dataDiskCount = origDDC })

	validCPP := func() *common.CustomPerformanceParams {
		return &common.CustomPerformanceParams{ThroughputMibps: 1024}
	}

	cases := []struct {
		name           string
		setup          func()
		params         *common.CreatePoolParams
		wantMsgInclude string
		wantUserInput  bool // true → UserInputValidationErr (4xx)
	}{
		{
			name:  "ZeroHAPairs",
			setup: func() { dataDiskCount = 2 },
			params: &common.CreatePoolParams{
				HAPairs:                 0, // <- offending field
				SizeInBytes:             4 * 1_000_000_000_000,
				CustomPerformanceParams: validCPP(),
			},
			wantMsgInclude: "haPairs must be > 0",
			wantUserInput:  true,
		},
		{
			name:  "ZeroSizeInBytes",
			setup: func() { dataDiskCount = 2 },
			params: &common.CreatePoolParams{
				HAPairs:                 2,
				SizeInBytes:             0, // <- offending field
				CustomPerformanceParams: validCPP(),
			},
			wantMsgInclude: "SizeInBytes must be > 0",
			wantUserInput:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			tc.setup()
			perVMCap, perVMThru, err := computeOCIVMRSInput(tc.params)
			require.Error(tt, err)
			assert.Zero(tt, perVMCap)
			assert.Zero(tt, perVMThru)
			assert.True(tt, utilserrors.IsUserInputValidationErr(err),
				"caller-supplied field errors must surface as UserInputValidationErr (4xx)")
			assert.Contains(tt, err.Error(), tc.wantMsgInclude)
		})
	}
}

// TestComputeOCIVMRSInput_ActivePassiveSplit anchors the AP-mode
// branch: when IsRegionalHA=true (non-shared HA), only the primary VM
// in each pair serves I/O, so totalActiveVMs = HAPairs (not 2 *
// HAPairs) and BOTH per-VM capacity AND per-VM throughput are exactly
// double the AA value for the same total. This sub-test paired with
// the AA happy path guarantees the two modes can't accidentally
// converge during a refactor of the activeVMsPerPair branch — and it
// is the regression guard for the historical bug where the capacity
// formula divided by HAPairs * dataDiskCount instead of
// totalActiveVMs (which only coincidentally produced the correct AA
// number when dataDiskCount==2 and which was 2x off in AP).
func TestComputeOCIVMRSInput_ActivePassiveSplit(t *testing.T) {
	origDDC := dataDiskCount
	dataDiskCount = 2
	t.Cleanup(func() { dataDiskCount = origDDC })

	params := &common.CreatePoolParams{
		HAPairs:      2,
		IsRegionalHA: true, // Active-Passive: only primary serves
		SizeInBytes:  4 * 1_000_000_000_000,
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 1024, // ≈ 1.073741824 GB/s
		},
	}

	perVMCap, perVMThru, err := computeOCIVMRSInput(params)
	require.NoError(t, err)
	// AP → totalActiveVMs = HAPairs = 2 → perVM capacity = 4 / 2 = 2.0
	// (exactly 2x the AA case which divides by 4 = totalActiveVMs in AA).
	// Pre-fix this returned 1.0 because the formula used
	// HAPairs * dataDiskCount (= 4) as the denominator regardless of
	// AA/AP — the failure mode this test guards against.
	assert.InDelta(t, 2.0, perVMCap, 1e-9,
		"AP per-VM capacity must be total / HAPairs (not / 2*HAPairs and not / HAPairs*dataDiskCount)")
	// AP → totalActiveVMs = HAPairs = 2 → perVM throughput = 0.536870912
	// (exactly 2x the AA case which divides by 4)
	assert.InDelta(t, 0.536870912, perVMThru, 1e-9,
		"AP per-VM throughput must be total / HAPairs (not / 2*HAPairs)")
}

// TestPrepareVLMConfig_AppliesDecisionFlipsAllFourOCIFields is the
// regression guard for the `if decision != nil` block inside
// prepareVLMConfig — the four-field VMRS override that gets activated
// only when OCI_VMRS_ENABLED=true. Without this test the entire block
// (VMRS catalogue → OCIConfig) is reachable only by an end-to-end
// workflow test, and the existing prepareVLMConfig tests all pass nil
// for the decision argument.
func TestPrepareVLMConfig_AppliesDecisionFlipsAllFourOCIFields(t *testing.T) {
	setTestOCIImageEnv(t)
	// CustomPerformanceParams settings here only need to satisfy
	// validators.ValidateIops — they're orthogonal to the VMRS
	// decision-flip behavior we're asserting. Mirroring the values used
	// in TestPrepareVLMConfig_RejectsZeroHAPairs keeps the IOPS floor
	// happy without coupling this test to the validator's exact math.
	iops := int64(5000)
	params := &common.CreatePoolParams{
		AccountName: "acct",
		// IsRegionalHA defaults to false (shared HA / AA -> 2 active VMs
		// per pair), so the per-disk-size guard divides by
		// haPairs(2) * activeVMsPerPair(2) * dataDiskCount(2) = 8. 800Gi
		// keeps this comfortably above the 50Gi-per-disk env default
		// (800/8=100Gi) — this test isn't exercising per-disk sizing, so
		// give it enough headroom that unrelated guard changes don't
		// couple back into VMRS decision-flip coverage.
		SizeInBytes:     800 * 1024 * 1024 * 1024,
		PrimaryZone:     "ad1",
		SecondaryZone:   "ad2",
		MediatorZone:    "ad3",
		VendorSubNetID:  "subnet",
		CompartmentOCID: "comp",
		HAPairs:         2,
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 128,
			Iops:            &iops,
		},
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		DeploymentName: "dep1",
		Name:           "pool1",
		Account:        &datamodel.Account{Name: "acct"},
	}
	// Synthetic VMRS Decision that intentionally uses values DIFFERENT
	// from the env defaults so a regression flipping a single field
	// back to the env path can't pass this test.
	decision := &vmrs_oci.Decision{
		VMShape:   "VM.DenseIO.Custom.Flex",
		OCPUs:     40,
		MemoryGBs: 480,
		VPU:       90,
		IOPS:      786600,
	}

	cfg, err := prepareVLMConfig(params, pool, decision)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	oc, err := cfg.Deployment.ProviderConfig.AsOCI()
	require.NoError(t, err)
	assert.Equal(t, "VM.DenseIO.Custom.Flex", oc.VSAInstanceShape,
		"VMRS-chosen shape must land on OCIConfig.VSAInstanceShape")
	assert.Equal(t, float32(40), oc.VSAFlexOcpus,
		"VMRS-chosen OCPUs must land on OCIConfig.VSAFlexOcpus")
	assert.Equal(t, float32(480), oc.VSAFlexMemoryInGBs,
		"VMRS-chosen memory must land on OCIConfig.VSAFlexMemoryInGBs verbatim (no per-OCPU derivation)")
	require.NotNil(t, oc.DataDiskVpus, "VMRS-chosen VPU must be set on OCIConfig.DataDiskVpus")
	assert.Equal(t, int64(90), *oc.DataDiskVpus,
		"VMRS-chosen VPU must land on OCIConfig.DataDiskVpus")
	// The deployment-level VSAInstanceType is wired off OCIConfig.VSAInstanceShape
	// inside ociDeploymentConfig — if the decision shape doesn't flow
	// there, the VM that actually gets launched will be wrong even
	// though OCIConfig looks right.
	assert.Equal(t, "VM.DenseIO.Custom.Flex", cfg.Deployment.VSAInstanceType,
		"VMRS shape must also flow into Deployment.VSAInstanceType so the launched VM matches the Decision")
	// IOPS lands on SPConfig (sibling of OCIConfig under Deployment),
	// not on OCIConfig. The fixture sets params.CustomPerformanceParams.Iops = 5000
	// so the validator-derived fallback would have produced 5000; the
	// VMRS branch must override that to decision.IOPS (786600) so the
	// catalogue-listed IOPS for the chosen (throughput-tier × VPU) cell
	// is what VLM actually receives on the wire. Mirrors the update-side
	// guard in TestPrepareOCIUpdateVSAClusterDeploymentRequest_DecisionPopulatesAllFourFields.
	assert.Equal(t, int64(786600), cfg.Deployment.SPConfig.IOps,
		"VMRS IOPS must override the validator-derived SPConfig.IOps so VLM uses the catalogue value")
}

// TestComputeOCIVMRSInput_HappyPath asserts the topology math on a
// well-formed Active-Active 2-HA-pair request. Anchors the per-VM
// throughput AND per-VM capacity formulas so future refactors of the
// AA/AP split can't quietly change what the OCI VMRS activity sees.
//
// Pair this with TestComputeOCIVMRSInput_ActivePassiveSplit: same
// inputs except for IsRegionalHA, and AP must show exactly 2x the
// per-VM numbers AA does (because AP halves the count of serving VMs).
func TestComputeOCIVMRSInput_HappyPath(t *testing.T) {
	origDDC := dataDiskCount
	dataDiskCount = 2
	t.Cleanup(func() { dataDiskCount = origDDC })

	params := &common.CreatePoolParams{
		HAPairs:      2,
		IsRegionalHA: false, // Active-Active: both VMs in each pair serve
		SizeInBytes:  4 * 1_000_000_000_000,
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 1024, // ≈ 1.073741824 GB/s
		},
	}

	perVMCap, perVMThru, err := computeOCIVMRSInput(params)
	require.NoError(t, err)
	// totalCapacityTB = 4.0; AA → 2 active VMs per pair →
	// totalActiveVMs = 4 → perVM capacity = 4.0 / 4 = 1.0 TB.
	assert.InDelta(t, 1.0, perVMCap, 1e-9,
		"AA per-VM capacity = total / (2 * HAPairs)")
	// totalThroughputGBs = 1024 MiB/s * 1.048576 / 1000 = 1.073741824
	// AA → 2 active VMs per pair → totalActiveVMs = 4 → perVM = 0.268435456
	assert.InDelta(t, 0.268435456, perVMThru, 1e-9,
		"AA per-VM throughput = total / (2 * HAPairs)")
}

func TestOCIDeploymentConfig(t *testing.T) {
	setTestOCIImageEnv(t)

	cases := []struct {
		name               string
		isRegionalHA       bool
		wantDeploymentTyp  string
		wantEnableAAConfig bool
	}{
		{
			name:               "RegionalHA_NonShared_AADisabled",
			isRegionalHA:       true,
			wantDeploymentTyp:  vlm.DeploymentTypeNonSharedHA,
			wantEnableAAConfig: false,
		},
		{
			name:               "ZonalHA_Shared_AAEnabled",
			isRegionalHA:       false,
			wantDeploymentTyp:  vlm.DeploymentTypeSharedHA,
			wantEnableAAConfig: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			params := &common.CreatePoolParams{
				AccountName:     "acct",
				PrimaryZone:     "ad1",
				SecondaryZone:   "ad2",
				MediatorZone:    "ad3",
				CompartmentOCID: "comp",
				VendorSubNetID:  "subnet",
				DataNICSubnetID: "data-subnet",
				HAPairs:         2,
				IsRegionalHA:    tc.isRegionalHA,
			}
			pool := &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{UUID: "pool-uuid-deploy"},
				Name:           "pool-name",
				DeploymentName: "dep-deploy",
			}
			ociConfig := vlm.OCIConfig{
				CompartmentID:    params.CompartmentOCID,
				SubnetID:         params.VendorSubNetID,
				DataNICSubnetID:  params.DataNICSubnetID,
				VSAInstanceShape: ociVSAInstanceType,
			}

			got := ociDeploymentConfig(params, pool, "100Gi", 256, 8192, ociConfig)

			// HA-mode-derived behavior: both DeploymentType and EnableAAConfig
			// must follow IsRegionalHA in lockstep. See the doc comment above for
			// why these two are intentionally coupled.
			assert.Equal(tt, tc.wantDeploymentTyp, got.DeploymentType,
				"DeploymentType must be derived from params.IsRegionalHA")
			assert.Equal(tt, tc.wantEnableAAConfig, got.DeploymentConfigFlags.EnableAAConfig,
				"DeploymentConfigFlags.EnableAAConfig must be the inverse of params.IsRegionalHA: AA on iff shared HA")
			assert.False(tt, got.DeploymentConfigFlags.EnableIlbSupport,
				"EnableIlbSupport is not wired for OCI; should be false in both HA modes")
			assert.Empty(tt, got.DeploymentConfigFlags.EnableNfsV364BitIdentifier,
				"EnableNfsV364BitIdentifier is not wired for OCI; should be empty in both HA modes")

			// Invariant fields - regression guard so the IsRegionalHA refactor cannot
			// silently break the rest of the deployment-config wiring.
			assert.Equal(tt, vlm.OCICloud, got.Provider)
			assert.Equal(tt, pool.DeploymentName, got.DeploymentID)
			assert.Equal(tt, ociSerialNumberLeadingPrefix+ociSerialNumberPrefix, got.SerialNumberPrefix,
				"SerialNumberPrefix must be the hardcoded \"955\"+15 zeros — the workflow owns the format, params.SerialNumberPrefix no longer exists and the API value is ignored")
			assert.Equal(tt, localRegion, got.Region)
			assert.Equal(tt, vsaImageName, got.Images.VSAImageName)
			assert.Equal(tt, vsaMediatorImageName, got.Images.MediatorImageName)
			assert.Equal(tt, ociVSAUserBootargs, got.UserBootargs)
			assert.Equal(tt, pool.Name, got.Labels["pool_name"])
			assert.Equal(tt, pool.UUID, got.Labels["pool_uuid"])
			assert.Equal(tt, params.AccountName, got.Labels["account_id"])
			// pool_ocid is added later by prepareCreateVSAClusterDeploymentRequest;
			// ociDeploymentConfig itself should not set it.
			_, hasPoolOCID := got.Labels["pool_ocid"]
			assert.False(tt, hasPoolOCID, "ociDeploymentConfig must not set pool_ocid label; it is added downstream")
			assert.Equal(tt, int(params.HAPairs), got.NumHAPair)
			assert.Equal(tt, ociVSAInstanceType, got.VSAInstanceType)
			assert.Equal(tt, ociMediatorInstanceType, got.MediatorInstanceType)
			assert.Equal(tt, dataDiskCount, got.DataDiskCount)
			gotOCI, ociErr := got.ProviderConfig.AsOCI()
			require.NoError(tt, ociErr)
			assert.Equal(tt, ociConfig, gotOCI)
			assert.Equal(tt, "100Gi", got.SPConfig.Size)
			assert.Equal(tt, int64(256), got.SPConfig.Throughput)
			assert.Equal(tt, int64(8192), got.SPConfig.IOps)
			assert.Equal(tt, extIPForNodeMgmt, got.DevFlags.ExtIPForNodeMgmt)
			gotDevFlags, devFlagsErr := got.DevFlags.ProviderDevFlags.AsOCI()
			require.NoError(tt, devFlagsErr)
			assert.Equal(tt, allowNonDenseShapeForVSA, gotDevFlags.AllowNonDenseShapeForVsa)
			assert.Equal(tt, useSecondaryIPsForLIFs, gotDevFlags.UseSecondaryIPsForLIFs)
		})
	}
}

// TestPrepareVLMConfig_DeploymentTypeReflectsIsRegionalHA confirms that the
func TestPrepareVLMConfig_DeploymentTypeReflectsIsRegionalHA(t *testing.T) {
	setTestOCIImageEnv(t)

	cases := []struct {
		name               string
		isRegionalHA       bool
		wantDeploymentTyp  string
		wantEnableAAConfig bool
	}{
		{
			name:               "RegionalHA_NonShared_AADisabled",
			isRegionalHA:       true,
			wantDeploymentTyp:  vlm.DeploymentTypeNonSharedHA,
			wantEnableAAConfig: false,
		},
		{
			name:               "ZonalHA_Shared_AAEnabled",
			isRegionalHA:       false,
			wantDeploymentTyp:  vlm.DeploymentTypeSharedHA,
			wantEnableAAConfig: true,
		},
	}

	iops := int64(5000)
	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			params := &common.CreatePoolParams{
				AccountName:     "acct",
				SizeInBytes:     300 * 1024 * 1024 * 1024,
				PrimaryZone:     "ad1",
				SecondaryZone:   "ad2",
				MediatorZone:    "ad3",
				VendorSubNetID:  "subnet",
				CompartmentOCID: "comp",
				HAPairs:         1,
				IsRegionalHA:    tc.isRegionalHA,
				CustomPerformanceParams: &common.CustomPerformanceParams{
					ThroughputMibps: 128,
					Iops:            &iops,
				},
			}
			pool := &datamodel.Pool{
				BaseModel:      datamodel.BaseModel{UUID: "u1"},
				DeploymentName: "dep1",
				Name:           "pool1",
				Account:        &datamodel.Account{Name: "acct"},
			}

			cfg, err := prepareVLMConfig(params, pool, nil)
			require.NoError(tt, err)
			require.NotNil(tt, cfg)
			assert.Equal(tt, tc.wantDeploymentTyp, cfg.Deployment.DeploymentType,
				"prepareVLMConfig must propagate IsRegionalHA into Deployment.DeploymentType")
			assert.Equal(tt, tc.wantEnableAAConfig, cfg.Deployment.DeploymentConfigFlags.EnableAAConfig,
				"prepareVLMConfig must propagate the IsRegionalHA-derived AA flag into Deployment.DeploymentConfigFlags.EnableAAConfig (active-active is on iff shared HA)")
		})
	}
}

func TestOCIDeploymentConfig_HAModeAndAAConfigAreInverselyCoupled(t *testing.T) {
	setTestOCIImageEnv(t)

	build := func(isRegionalHA bool) vlm.DeploymentConfig {
		params := &common.CreatePoolParams{
			AccountName:     "acct",
			PrimaryZone:     "ad1",
			SecondaryZone:   "ad2",
			MediatorZone:    "ad3",
			CompartmentOCID: "comp",
			VendorSubNetID:  "subnet",
			DataNICSubnetID: "data-subnet",
			HAPairs:         1,
			IsRegionalHA:    isRegionalHA,
		}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "pool-coupling"},
			Name:           "pool-coupling",
			DeploymentName: "dep-coupling",
		}
		return ociDeploymentConfig(params, pool, "100Gi", 256, 8192, vlm.OCIConfig{})
	}

	regional := build(true)
	zonal := build(false)

	// Direct inversion check: AA config is the boolean negation of "is regional HA".
	assert.NotEqual(t, regional.DeploymentConfigFlags.EnableAAConfig,
		zonal.DeploymentConfigFlags.EnableAAConfig,
		"EnableAAConfig must differ between the two HA modes; identical values means the IsRegionalHA branch was bypassed for this flag")

	// And the type must also differ — otherwise the test below devolves to
	// asserting two unrelated boolean swings happen to match, which would
	// not catch a copy-paste mistake.
	assert.NotEqual(t, regional.DeploymentType, zonal.DeploymentType,
		"DeploymentType must differ between the two HA modes")

	// Coupling: the regional pair must be (NonShared, AA-off) and the zonal
	// pair must be (Shared, AA-on). Asserting both pairs together catches the
	// case where someone flips just one half of the invariant.
	assert.Equal(t, vlm.DeploymentTypeNonSharedHA, regional.DeploymentType)
	assert.False(t, regional.DeploymentConfigFlags.EnableAAConfig,
		"regional HA implies non-shared deployment, which must NOT use active-active config")

	assert.Equal(t, vlm.DeploymentTypeSharedHA, zonal.DeploymentType)
	assert.True(t, zonal.DeploymentConfigFlags.EnableAAConfig,
		"zonal HA implies shared deployment, which MUST use active-active config")
}

// TestOCIDeploymentConfig_SerialNumberPrefixIsHardcoded pins the workflow's
// single source of truth for the VM serial-number prefix. The endpoint no
// longer forwards a caller value (the API field is accepted but ignored), and
// common.CreatePoolParams no longer carries a SerialNumberPrefix. So whatever
// the inputs look like, the workflow must always emit the same "955" + 15
// zeros value; any deviation here would silently change downstream VLM VM
// serial generation.
func TestOCIDeploymentConfig_SerialNumberPrefixIsHardcoded(t *testing.T) {
	setTestOCIImageEnv(t)

	params := &common.CreatePoolParams{
		AccountName:     "acct",
		PrimaryZone:     "ad1",
		SecondaryZone:   "ad2",
		MediatorZone:    "ad3",
		CompartmentOCID: "comp",
		VendorSubNetID:  "subnet",
		DataNICSubnetID: "data-subnet",
		HAPairs:         2,
		IsRegionalHA:    false,
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "pool-uuid-prefix"},
		Name:           "pool-name-prefix",
		DeploymentName: "dep-name-prefix",
	}
	ociConfig := vlm.OCIConfig{
		CompartmentID:   params.CompartmentOCID,
		SubnetID:        params.VendorSubNetID,
		DataNICSubnetID: params.DataNICSubnetID,
	}

	got := ociDeploymentConfig(params, pool, "100Gi", 256, 8192, ociConfig)

	assert.Equal(t, "955000000000000000", got.SerialNumberPrefix,
		"workflow must emit the hardcoded 18-character VLM serial-number prefix; the value (\"955\" + 15 zeros) is what VLM expects and is not configurable from params or the API")
	assert.Equal(t, ociSerialNumberLeadingPrefix+ociSerialNumberPrefix, got.SerialNumberPrefix,
		"the emitted prefix must equal the concatenation of the two package-level constants; if either constant changes, this regression test will surface it loudly")
}

// TestOCIDeploymentConfig_LegacySerialPrefixIsExactly18Chars locks in the two
// constituent constants. Changing either is a coordinated rollout with VLM:
// the leading "955" is an OCI realm marker, and the trailing 15 zeros are
// load-bearing for VLM's serial-collision detection — neither should drift
// unilaterally.
func TestOCIDeploymentConfig_LegacySerialPrefixIsExactly18Chars(t *testing.T) {
	assert.Equal(t, "955", ociSerialNumberLeadingPrefix,
		"the leading prefix is the OCI realm marker; changing it requires a coordinated VLM rollout, not a unilateral constant edit")
	assert.Equal(t, "000000000000000", ociSerialNumberPrefix,
		"the trailing portion must remain exactly 15 zero digits — both the count and the all-zero pattern matter to VLM's serial-collision detection")
	assert.Equal(t, "955000000000000000", ociSerialNumberLeadingPrefix+ociSerialNumberPrefix,
		"the combined 18-character prefix must equal the historical value VLM has been seeing; any deviation breaks downstream VM serial generation silently")
}

func TestPrepareOCIDeleteVSAClusterDeploymentRequest(t *testing.T) {
	req := &vlm.DeleteVSAClusterDeploymentRequest{}
	pool := &datamodel.Pool{
		DeploymentName: "dep-1",
		ClusterDetails: datamodel.ClusterDetails{CompartmentOCID: "comp-from-pool"},
	}
	prepareOCIDeleteVSAClusterDeploymentRequest(req, pool, "tenancy-ocid")
	assert.Equal(t, vlm.OCICloud, req.CloudProvider)
	assert.Equal(t, "dep-1", req.DeploymentID)
	assert.Equal(t, "tenancy-ocid", req.ProjectID)
	ociCfg, err := req.ProviderConfig.AsOCI()
	require.NoError(t, err)
	assert.Equal(t, "comp-from-pool", ociCfg.CompartmentID)

	pool.ClusterDetails.CompartmentOCID = "comp-updated"
	prepareOCIDeleteVSAClusterDeploymentRequest(req, pool, "tenancy-2")
	ociCfg, err = req.ProviderConfig.AsOCI()
	require.NoError(t, err)
	assert.Equal(t, "comp-updated", ociCfg.CompartmentID)
	assert.Equal(t, "tenancy-2", req.ProjectID)
}

func TestPrepareCreateVSAClusterDeploymentRequest_InitsNilLabels(t *testing.T) {
	setTestOCIImageEnv(t)
	req := &vlm.CreateVSAClusterDeploymentRequest{}
	vlmConfig := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			Labels: nil,
		},
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "u1"},
		Name:                   "pn",
		PoolExternalIdentifier: "ocid.pool",
		Account:                &datamodel.Account{Name: "aname"},
	}
	cred := vlm.OntapCredentials{}
	prepareCreateVSAClusterDeploymentRequest(req, vlmConfig, cred, pool)
	require.NotNil(t, req.VLMConfig.Deployment.Labels)
	assert.Equal(t, "pn", req.VLMConfig.Deployment.Labels["pool_name"])
	assert.Equal(t, "ocid.pool", req.VLMConfig.Deployment.Labels["pool_ocid"])
	assert.Equal(t, "aname", req.VLMConfig.Deployment.Labels["account_id"])
}

func TestOCIDeletePoolWorkflow_Success(t *testing.T) {
	setTestOCIImageEnv(t)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.DeletePoolParams{
		AccountName: "test-account",
		PoolID:      "pool-uuid-del",
	}

	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "pool-uuid-del"},
		Name:           "p",
		DeploymentName: "dep-ocnv-abc",
		ClusterDetails: datamodel.ClusterDetails{CompartmentOCID: "comp-ocid"},
		Account:        &datamodel.Account{Name: "test-account"},
	}

	env.OnActivity("DeleteOnTapCredentialsForOCI", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeletePoolResources", mock.Anything, mock.Anything).Return(nil, nil)

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("DeleteVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.ExecuteWorkflow(OCIDeletePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIDeletePoolWorkflow_VLMDeleteFailure(t *testing.T) {
	setTestOCIImageEnv(t)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.DeletePoolParams{
		AccountName: "test-account",
		PoolID:      "pool-uuid-del",
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "pool-uuid-del"},
		Name:           "p",
		DeploymentName: "dep-ocnv-abc",
		ClusterDetails: datamodel.ClusterDetails{CompartmentOCID: "comp-ocid"},
		Account:        &datamodel.Account{Name: "test-account"},
	}

	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("DeleteVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.ExecuteWorkflow(OCIDeletePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestOCIDeletePoolWorkflow_DBCleanupFailure(t *testing.T) {
	setTestOCIImageEnv(t)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.DeletePoolParams{
		AccountName: "test-account",
		PoolID:      "pool-uuid-del",
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "pool-uuid-del"},
		Name:           "p",
		DeploymentName: "dep-ocnv-abc",
		ClusterDetails: datamodel.ClusterDetails{CompartmentOCID: "comp-ocid"},
		Account:        &datamodel.Account{Name: "test-account"},
	}

	env.OnActivity("DeletePoolResources", mock.Anything, mock.Anything).Return(nil, assert.AnError)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("DeleteVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.ExecuteWorkflow(OCIDeletePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

// TestOCIDeletePoolWorkflow_CertAuth_CleansUpDNS verifies that deleting a
// cert-auth pool on the legacy DNS-records path (OCIUseTLSSNIOverride=false)
// fetches the persisted IP→FQDN map and deletes each record.
//
// For non-cert pools (USERNAME_PWD or unset) the DNS activities must NOT run
// (already exercised by TestOCIDeletePoolWorkflow_Success above). The SNI-
// override default is covered by TestOCIDeletePoolWorkflow_CertAuth_SNIOverride_StillCleansUpDNS.
func TestOCIDeletePoolWorkflow_CertAuth_CleansUpDNS(t *testing.T) {
	setTestOCIImageEnv(t)
	withOCIUseTLSSNIOverride(t, false)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.DeletePoolParams{
		AccountName: "test-account",
		PoolID:      "pool-uuid-del-cert",
	}

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{ID: 99, UUID: "pool-uuid-del-cert"},
		Name:            "p-cert",
		DeploymentName:  "dep-cert",
		ClusterDetails:  datamodel.ClusterDetails{CompartmentOCID: "comp-ocid"},
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{AuthType: envs.USER_CERTIFICATE},
	}

	env.OnActivity("DeleteOnTapCredentialsForOCI", mock.Anything, mock.Anything).Return(nil)
	expectedHostMap := map[string]string{
		"10.0.0.1": "dns-1.dep-cert.vsa.netapp.internal.",
		"10.0.0.2": "dns-2.dep-cert.vsa.netapp.internal.",
	}
	env.OnActivity("GetCloudDNSRecords", mock.Anything, pool.ID, envs.USER_CERTIFICATE).Return(&expectedHostMap, nil)
	// MatchedBy lets us assert the map content we just returned propagates
	// into the delete activity unchanged — this is the wiring guarantee we
	// most care about.
	env.OnActivity("OCIDeleteCloudDNSRecords",
		mock.Anything,
		mock.MatchedBy(func(m map[string]string) bool {
			return len(m) == 2 && m["10.0.0.1"] == "dns-1.dep-cert.vsa.netapp.internal."
		}),
		envs.USER_CERTIFICATE,
	).Return(nil)
	env.OnActivity("DeletePoolResources", mock.Anything, mock.Anything).Return(nil, nil)

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("DeleteVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.ExecuteWorkflow(OCIDeletePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIDeletePoolWorkflow_CertAuth_SNIOverride_StillCleansUpDNS locks in the
// delete-path behaviour when env.OCIUseTLSSNIOverride is on (the production
// default): unlike the create path, pool teardown does NOT gate DNS cleanup on
// the SNI flag. The delete workflow always runs GetCloudDNSRecords →
// OCIDeleteCloudDNSRecords for cert-auth pools so that any OCI Private DNS
// records that may exist (e.g. pools created before the SNI flag was flipped on,
// or via the legacy fallback path) are cleaned up. GetCloudDNSRecords returns an
// empty map when there is nothing to delete, and OCIDeleteCloudDNSRecords is a
// no-op on an empty map. Failing this test means the SNI-flag gate has been
// re-introduced on the delete path and would leak DNS records for such pools.
func TestOCIDeletePoolWorkflow_CertAuth_SNIOverride_StillCleansUpDNS(t *testing.T) {
	setTestOCIImageEnv(t)
	withOCIUseTLSSNIOverride(t, true)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.DeletePoolParams{
		AccountName: "test-account",
		PoolID:      "pool-uuid-del-cert-sni",
	}

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{ID: 101, UUID: "pool-uuid-del-cert-sni"},
		Name:            "p-cert-sni",
		DeploymentName:  "dep-cert-sni",
		ClusterDetails:  datamodel.ClusterDetails{CompartmentOCID: "comp-ocid"},
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{AuthType: envs.USER_CERTIFICATE},
	}

	env.OnActivity("DeleteOnTapCredentialsForOCI", mock.Anything, mock.Anything).Return(nil)
	// DNS cleanup runs regardless of the SNI flag on the delete path. Here the
	// pool has no published records, so GetCloudDNSRecords returns an empty map
	// and OCIDeleteCloudDNSRecords is invoked with it as a no-op.
	emptyHostMap := map[string]string{}
	env.OnActivity("GetCloudDNSRecords", mock.Anything, pool.ID, envs.USER_CERTIFICATE).Return(&emptyHostMap, nil)
	env.OnActivity("OCIDeleteCloudDNSRecords", mock.Anything, mock.Anything, envs.USER_CERTIFICATE).Return(nil)
	env.OnActivity("DeletePoolResources", mock.Anything, mock.Anything).Return(nil, nil)

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("DeleteVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.ExecuteWorkflow(OCIDeletePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_Success(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	// Serial allocation defaults on in production; its behaviour is covered by the
	// dedicated SerialAllocation_* tests. Gate it off here so this test stays focused
	// on the general happy path without needing serial env + GetNextSerialNumber mocks.
	withOCIVSASerialAllocationFlag(t, false)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	// Set up test data
	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024, // 1 TB
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "test-pool-uuid",
		},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	// Mock GetJob activity - return NEW state for workflow job (EnsureJobState)
	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Node)(nil), nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	// UpdatePoolFields stamps build_info after the pool is marked READY.
	// Permissive mock — the dedicated TestOCICreatePoolWorkflow_PersistsBuildInfo
	// test below asserts on the actual captured arguments.
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	// Assert workflow execution
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCICreatePoolWorkflow_VMRSEnabled_RunsVMRSBranch is the coverage and
// behavioral guard for the OCI_VMRS_ENABLED=true branch of
// OCICreatePoolWorkflow.Run. With the toggle off (the default exercised by
// TestOCICreatePoolWorkflow_Success) the whole block between
// workflow.SideEffect and prepareVLMConfig is skipped — meaning
// computeOCIVMRSInput, the dedicated short-timeout vmrsAO, and the
// IdentifyOCIResources activity dispatch are all reachable only when the
// toggle flips. This test flips it and asserts:
//
//  1. IdentifyOCIResources is invoked exactly once with per-VM capacity and
//     per-VM throughput derived from the pool's total size + topology
//     (regression guard for the per-VM unit contract documented on
//     computeOCIVMRSInput and IdentifyOCIResourcesRequest).
//  2. The Decision returned by the activity flows downstream so the rest of
//     the workflow runs to completion — i.e. enabling VMRS does not change
//     the workflow's terminal state.
func TestOCICreatePoolWorkflow_VMRSEnabled_RunsVMRSBranch(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVMRSEnabled(t, true)
	// Keep this test scoped to the VMRS branch; serial allocation is covered separately.
	withOCIVSASerialAllocationFlag(t, false)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	// CustomPerformanceParams is required to reach computeOCIVMRSInput's
	// arithmetic — validateOCIVMRSInput rejects nil CPP and any non-positive
	// ThroughputMibps before the math runs.
	//
	// IOPS must satisfy the pool validator's floor of 16 × ThroughputMibps
	// (see core/orchestrator/validators/pool_validator.go ValidateIops);
	// for 1024 MiBps that's 16384 IOPS. Any lower value would trip
	// prepareVLMConfig — invoked right after the VMRS branch — and fail
	// the workflow before this test can assert the IdentifyOCIResources
	// payload. computeOCIVMRSInput does not read Iops, so this value does
	// not affect the per-VM assertions below.
	iops := int64(16384)
	params := &common.CreatePoolParams{
		Name:        "test-pool-vmrs",
		AccountName: "test-account",
		SizeInBytes: 4 * 1_000_000_000_000, // 4 TB total
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     2,
		// IsRegionalHA=false (AA) → totalActiveVMs = 2 * HAPairs = 4.
		IsRegionalHA: false,
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 1024,
			Iops:            &iops,
		},
	}

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid-vmrs"},
		Name:            "test-pool-vmrs",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// IdentifyOCIResources is the activity that owns the OCI VMRS catalogue
	// selector. Asserting on its request payload pins the per-VM unit
	// contract end-to-end: 4 TB / 4 active VMs = 1.0 TB/VM, and 1024 MiB/s
	// converted to GB/s then divided by 4 = 0.268435456 GB/s/VM. Any
	// regression that re-introduces per-disk slicing or drops the AA
	// doubling will fail here.
	const expectedPerVMCapacityTB = 1.0
	const expectedPerVMThroughputGBs = 0.268435456
	vmrsDecision := &vmrs_oci.Decision{
		VMShape:   "VM.DenseIO.E4.Flex",
		OCPUs:     16,
		MemoryGBs: 256,
		VPU:       40,
		IOPS:      30000,
	}
	env.OnActivity("IdentifyOCIResources", mock.Anything,
		mock.MatchedBy(func(req activities.IdentifyOCIResourcesRequest) bool {
			return req.PoolUUID == pool.UUID &&
				approxEqual(req.PerVMCapacityTB, expectedPerVMCapacityTB, 1e-9) &&
				approxEqual(req.PerVMThroughputGBs, expectedPerVMThroughputGBs, 1e-9)
		}),
	).Return(vmrsDecision, nil).Once()

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Node)(nil), nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// approxEqual is a tiny float helper for mock.MatchedBy assertions on the
// VMRS per-VM inputs. Using a delta (instead of ==) avoids spurious test
// flakes from the MiB/s → GB/s float conversion inside computeOCIVMRSInput.
func approxEqual(a, b, delta float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= delta
}

// TestOCICreatePoolWorkflow_PersistsExternalSecretOnPoolCredentials is the regression
// guard for the steady-state OCI vault rehydration bug.
//
// CreateOnTapCredentialsForOCI is the only place that knows the Name / OCID /
// Version of the freshly created OCI Vault secret — that information exists
// nowhere else in the system. The workflow must copy it onto
// pool.PoolCredentials.ExternalSecret before exiting, so the JSONB credentials blob
// persisted by CreatedPool's se.UpdatedPool call carries the reference for
// every future operation that loads the pool from the DB.
//
// Without this write-back, downstream callers that build *models.Node via
// vsa.CreateNodeForProvider — and the unified _saveNodeDetails, which
// also reads ExternalSecret off PoolCredentials — see
// PoolCredentials.ExternalSecret == nil, which gets copied (as nil) onto the
// runtime node, which causes _getProviderByNode → _getPasswordFromCacheOrOCIVault
// (when env.GetHyperscaler()=="oci") to return "OCI vault reference is empty"
// wrapped as ErrOCIResourceFetchError. That failure mode is what this test
// prevents from ever silently re-emerging.
func TestOCICreatePoolWorkflow_PersistsExternalSecretOnPoolCredentials(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)

	var ts testsuite.WorkflowTestSuite
	wfEnv := ts.NewTestWorkflowEnvironment()
	wfEnv.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(wfEnv)

	mockStorage := database.NewMockStorage(t)
	wfEnv.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	wfEnv.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	mockStorage.On("GetFaultDomainCountsByZone", mock.Anything).Return(map[string]map[string]int64{}, nil).Maybe()

	// The secret coordinates the vault returns to the activity. These three
	// fields, taken together, are the entire payload that has to round-trip
	// from the activity through the workflow into the persisted pool row.
	expectedRef := &datamodel.ExternalCredRef{
		Name:               "ocnv-regression-secret",
		Version:            7,
		ExternalIdentifier: "ocid1.vaultsecret.oc1..regression",
	}

	// Stub CreateOnTapCredentialsForOCI to return the known ref. We do NOT
	// reuse registerOCICreatePoolOntapCredentialMocks here because that helper
	// uses .Maybe() and a different secret payload; this test wants a strict
	// expectation on the returned ref so the assertion below is unambiguous.
	wfEnv.OnActivity("CreateOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&activities.OCICreatePoolCredentials{
			OntapCredentials: vlm.OntapCredentials{AdminPassword: "test-ontap-admin-password"},
			Secret:           expectedRef,
		}, nil)
	wfEnv.OnActivity("DeleteOnTapCredentialsForOCI", mock.Anything, mock.Anything).Return(nil).Maybe()

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:      "test-pool",
		AccountID: 12345,
		VendorID:  "test-vendor",
		Account:   &datamodel.Account{Name: "test-account"},
		// PoolCredentials must be non-nil (the workflow rejects nil at
		// line 191) but ExternalSecret is intentionally nil here — that's the
		// production starting state we are testing the workflow can heal.
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	wfEnv.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	// Capture the pool that arrives at SaveVSANodeDetails as well as
	// CreatedPool. We assert against both: the former proves the mutation is
	// in place by the time the node-save activity runs (so the unified
	// _saveNodeDetails — which now reads pool.PoolCredentials.ExternalSecret
	// and propagates it onto the in-memory Node so the OCI vault lookup can
	// resolve — sees it), and the latter proves the mutation is in place by
	// the time the JSONB column is actually persisted via se.UpdatedPool.
	//
	// SaveVSANodeDetails takes (ctx, pool, vlmConfig, deploymentName, hostMap) → 5 matchers.
	var saveNodeDetailsPool, createdPoolPool *datamodel.Pool
	wfEnv.OnActivity("SaveVSANodeDetails",
		mock.Anything,
		mock.MatchedBy(func(p *datamodel.Pool) bool {
			saveNodeDetailsPool = p
			return true
		}),
		mock.Anything, mock.Anything, mock.Anything,
	).Return((*datamodel.Node)(nil), nil)
	wfEnv.OnActivity("CreatedPool",
		mock.Anything,
		mock.MatchedBy(func(p *datamodel.Pool) bool {
			createdPoolPool = p
			return true
		}),
		mock.Anything,
	).Return(pool, nil)
	// The workflow now calls UpdatePoolFields twice on the happy path:
	//   1. "pool_credentials" — the OCI vault secret-ref write-back that
	//      this test owns.
	//   2. "build_info" — covered by TestOCICreatePoolWorkflow_PersistsBuildInfo.
	// We register a single permissive stub that inspects the updates map on
	// every call and captures the typed PoolCredentials payload from the
	// pool_credentials call. Temporal's data converter erases the in-workflow
	// *PoolCredentials into a generic map[string]interface{} on the wire (the
	// activity signature is map[string]interface{}), so we round-trip it
	// through JSON to recover the typed view that GORM would actually
	// persist into the JSONB column.
	var (
		capturedCredentialsUpdateUUID string
		capturedCredentialsUpdate     *datamodel.PoolCredentials
	)
	wfEnv.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			updates, ok := args[2].(map[string]interface{})
			if !ok {
				return
			}
			raw, ok := updates["pool_credentials"]
			if !ok {
				return
			}
			encoded, mErr := json.Marshal(raw)
			if mErr != nil {
				return
			}
			var pc datamodel.PoolCredentials
			if uErr := json.Unmarshal(encoded, &pc); uErr != nil {
				return
			}
			capturedCredentialsUpdate = &pc
			capturedCredentialsUpdateUUID, _ = args[1].(string)
		}).
		Return(nil)

	wfEnv.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())

	// Primary assertion: the pool that reaches CreatedPool — and therefore
	// the pool whose JSONB credentials blob is persisted — must carry the
	// ExternalSecret produced by CreateOnTapCredentialsForOCI. Field-by-field
	// equality (not pointer identity) because Temporal serializes activity
	// arguments through its data converter.
	if assert.NotNil(t, createdPoolPool, "CreatedPool must be invoked with the workflow's pool") {
		if assert.NotNil(t, createdPoolPool.PoolCredentials, "PoolCredentials must be present on the persisted pool") {
			if assert.NotNil(t, createdPoolPool.PoolCredentials.ExternalSecret,
				"ExternalSecret must be persisted onto PoolCredentials before the workflow exits — "+
					"otherwise CreateNodeForProvider will re-hydrate nil and downstream OCI vault "+
					"lookups fail with \"OCI vault reference is empty\"") {
				assert.Equal(t, expectedRef.Name, createdPoolPool.PoolCredentials.ExternalSecret.Name)
				assert.Equal(t, expectedRef.Version, createdPoolPool.PoolCredentials.ExternalSecret.Version)
				assert.Equal(t, expectedRef.ExternalIdentifier, createdPoolPool.PoolCredentials.ExternalSecret.ExternalIdentifier)
			}
			// ExternalCertificate stays nil today (the cert flow is a TODO in
			// CreateOnTapCredentialsForOCI). When that lands, replace this
			// nil-check with a positive assertion mirroring ExternalSecret above.
			assert.Nil(t, createdPoolPool.PoolCredentials.ExternalCertificate,
				"ExternalCertificate is expected to be nil until the OCI certificate flow is wired up; "+
					"if this assertion fires, mirror the ExternalSecret assertions above for the cert ref")
		}
	}

	// Secondary assertion: the same mutation is visible to
	// SaveVSANodeDetails, which runs before CreatedPool. This pins the
	// ordering invariant — if anyone moves the credential write-back below
	// SaveVSANodeDetails, this assertion will catch it even though the
	// primary assertion above might still pass.
	if assert.NotNil(t, saveNodeDetailsPool, "SaveVSANodeDetails must be invoked with the workflow's pool") {
		if assert.NotNil(t, saveNodeDetailsPool.PoolCredentials) {
			assert.NotNil(t, saveNodeDetailsPool.PoolCredentials.ExternalSecret,
				"the credential write-back must happen before SaveVSANodeDetails runs, "+
					"so _saveNodeDetails (which reads ExternalSecret off the passed-in pool "+
					"and copies it onto the in-memory models.Node) sees the populated value")
		}
	}

	// Tertiary assertion — the actual DB-level persistence contract. The
	// primary/secondary assertions only prove the workflow's *in-memory*
	// pool object carries ExternalSecret as it flows through activity
	// invocations; they do NOT prove the JSONB column on disk gets the
	// reference. The only path that actually writes to pool_credentials
	// JSONB before CreatedPool refetches the row is an explicit
	// UpdatePoolFields(pool_credentials=...) activity call. Without this
	// assertion, the bug (CreatedPool refetching from DB and the subsequent
	// UpdatedPool re-serializing stale PoolCredentials, silently dropping
	// ExternalSecret) would slip past the in-memory checks above.
	if assert.NotNil(t, capturedCredentialsUpdate,
		"UpdatePoolFields must be invoked with a pool_credentials column update — "+
			"this is the only path that writes ExternalSecret into the JSONB column on disk; "+
			"without it CreatedPool's refetch overwrites the in-memory mutation and the "+
			"secret reference is silently lost between the workflow and the DB") {
		assert.Equal(t, "test-pool-uuid", capturedCredentialsUpdateUUID,
			"the UpdatePoolFields(pool_credentials=...) call must target the exact pool the workflow is creating")
		if assert.NotNil(t, capturedCredentialsUpdate.ExternalSecret,
			"pool_credentials.ExternalSecret must be present in the JSONB write so future "+
				"operations that load the pool from DB can resolve the OCI Vault secret") {
			assert.Equal(t, expectedRef.Name, capturedCredentialsUpdate.ExternalSecret.Name)
			assert.Equal(t, expectedRef.Version, capturedCredentialsUpdate.ExternalSecret.Version)
			assert.Equal(t, expectedRef.ExternalIdentifier, capturedCredentialsUpdate.ExternalSecret.ExternalIdentifier)
		}
		// ExternalCertificate stays nil today (cert flow is a TODO in
		// CreateOnTapCredentialsForOCI). When that lands, mirror the
		// ExternalSecret assertions above for the persisted cert ref.
		assert.Nil(t, capturedCredentialsUpdate.ExternalCertificate,
			"ExternalCertificate is expected to be nil in the JSONB write until the OCI "+
				"certificate flow is wired up; if this fires, mirror the ExternalSecret "+
				"assertions above for the persisted cert ref")
	}

	wfEnv.AssertExpectations(t)
}

// TestOCICreatePoolWorkflow_SelectFaultDomainsFails covers the error-log and
// early-return path at pool_workflows.go lines 310-311 when the
// SelectFaultDomains activity returns an error.
func TestOCICreatePoolWorkflow_SelectFaultDomainsFails(t *testing.T) {
	setTestOCIImageEnv(t)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	// GetFaultDomainCountsByZone fails → SelectFaultDomains activity fails → workflow error path.
	mockStorage.On("GetFaultDomainCountsByZone", mock.Anything).
		Return(nil, assert.AnError)

	// ONTAP credential activities are mocked permissively; they are never reached.
	env.OnActivity("CreateOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(nil, nil).Maybe()
	env.OnActivity("DeleteOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(nil).Maybe()

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_SetupError(t *testing.T) {
	setTestOCIImageEnv(t)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	// Set up test data with invalid params to cause setup error
	params := &common.CreatePoolParams{
		Name:        "",
		AccountName: "",
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "test-pool-uuid",
		},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	// Mock GetJob activity - return NEW state for workflow job (EnsureJobState)
	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJob on storage (called by UpdateJobStatus activity)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	// Workflow should complete (setup may succeed but workflow should handle it)
	assert.True(t, env.IsWorkflowCompleted())
	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_EnsureJobStateError(t *testing.T) {
	setTestOCIImageEnv(t)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	// Set up test data
	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "test-pool-uuid",
		},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	// Mock GetJob activity to return ERROR state (should cause EnsureJobState to fail)
	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-workflow-id"},
		State:     string(datamodel.JobsStateERROR),
	}, nil).Maybe()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	// Workflow should complete with error
	assert.True(t, env.IsWorkflowCompleted())
	// Should have error because job is in ERROR state
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_UpdateJobStatusError(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	// Set up test data
	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "test-pool-uuid",
		},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	// Mock GetJob activity - return NEW state for workflow job
	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Node)(nil), nil)
	// The OCI create-pool workflow now persists pool_credentials via
	// UpdatePoolFields BEFORE CreatedPool runs (write-back for the OCI
	// vault secret-ref). This test exercises the CreatedPool failure
	// branch; the upstream credentials write must succeed so the workflow
	// actually reaches CreatedPool. Permissive stub.
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Pool)(nil), assert.AnError)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	// Workflow should complete with error
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_SaveVSANodeDetailsFailure(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Node)(nil), assert.AnError)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_RunMethodCalled(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	// Set up test data
	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "test-pool-uuid",
		},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	// Mock GetJob activity - return NEW state for workflow job
	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Node)(nil), nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	// UpdatePoolFields stamps build_info after the pool is marked READY.
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	// Assert workflow execution completed successfully
	// The Run method should be called and return nil, nil
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_ExpertModePasswordFromEnv(t *testing.T) {
	setTestOCIImageEnv(t)
	withOCIVSASerialAllocationFlag(t, false)

	// Simulate OCI_EXPERT_MODE_PASSWORD being set before the binary starts by directly
	// overriding the package-level var (same package, so accessible).
	orig := ociExpertModePassword
	ociExpertModePassword = "preset-env-password"
	defer func() { ociExpertModePassword = orig }()

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	// CreateVSAExpertModeUser is always called; the preset password is used directly
	// without executing the GetExpertModeCredentialsForOCI activity.
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	// GetExpertModeCredentialsForOCI must NOT be called when the env var is pre-set.
	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Node)(nil), nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	// UpdatePoolFields stamps build_info after the pool is marked READY.
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_CreateExpertModeCredentialsFails(t *testing.T) {
	setTestOCIImageEnv(t)
	withOCIVSASerialAllocationFlag(t, false)
	// Ensure ociExpertModePassword is empty so the workflow takes the
	// GetExpertModeCredentialsForOCI activity path.
	setOCIExpertModePassword(t, "")

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
		OciAdminPassword: &common.OciAdminPassword{
			Ocid:    "ocid1.vaultsecret.oc1..testadminpw",
			Version: 1,
		},
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	// Simulate the activity returning an error (e.g. OCI secret fetch failure).
	env.OnActivity("GetExpertModeCredentialsForOCI", mock.Anything, mock.Anything, mock.Anything).
		Return((*vlm.OntapCredentials)(nil), assert.AnError)
	// Rollback path: ErroredPool is called; VLM deploy and later steps are never reached.
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	mockVlmWorkflowClient.AssertNotCalled(t, "CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_CreateVSAExpertModeUserFails(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	// Password comes from env var, but expert-mode user creation fails.
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, assert.AnError)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	// Rollback path: ErroredPool is called; SaveVSANodeDetails and CreatedPool are never reached.
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCICreatePoolWorkflow_RollbackDeletesOntapSecretAfterVLMCreateFails asserts that when
// CreateOnTapCredentialsForOCI has created a vault secret, a failure at CreateVSAClusterDeployment
// triggers rollback: VLM delete child workflow runs first (LIFO), then DeleteOnTapCredentialsForOCI.
func TestOCICreatePoolWorkflow_RollbackDeletesOntapSecretAfterVLMCreateFails(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	mockStorage.On("GetFaultDomainCountsByZone", mock.Anything).Return(map[string]map[string]int64{}, nil).Maybe()

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:            "test-pool",
		DeploymentName:  "dep-rollback-test",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "rollback-pass"},
	}

	env.OnActivity("CreateOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&activities.OCICreatePoolCredentials{
			OntapCredentials: vlm.OntapCredentials{AdminPassword: "vault-generated-password"},
			Secret:           &datamodel.ExternalCredRef{Name: "dep-rollback-test-secret", Version: 1, ExternalIdentifier: "ocid1.vaultsecret.oc1..rollbacksecretocid"},
		}, nil).Once()
	env.OnActivity("DeleteOnTapCredentialsForOCI", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil).Once()

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return((*vlm.CreateVSAClusterDeploymentResponse)(nil), assert.AnError)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_NilPoolCredentialsRejected(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	mockStorage.On("GetFaultDomainCountsByZone", mock.Anything).Return(map[string]map[string]int64{}, nil).Maybe()

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:      "test-pool",
		AccountID: 12345,
		VendorID:  "test-vendor",
		Account:   &datamodel.Account{Name: "test-account"},
	}

	// Rollback fires when Run returns an error.
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pool credentials are required",
		"workflow should fail with the new pool-credentials guard, not some downstream error")
	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_RunArgsValidation(t *testing.T) {
	validParams := &common.CreatePoolParams{Name: "p", AccountName: "a", HAPairs: 1}
	validPool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "u"}, Name: "p"}

	cases := []struct {
		name             string
		args             []interface{}
		wantOriginalSubs string // substring expected in OriginalErr.Error()
		wantTrackingID   int    // 0 = don't assert; otherwise must match
	}{
		{
			name:             "ZeroArgs",
			args:             nil,
			wantOriginalSubs: "expected 2 args, got 0",
		},
		{
			name:             "OneArg",
			args:             []interface{}{validParams},
			wantOriginalSubs: "expected 2 args, got 1",
		},
		{
			name:             "Args0WrongType",
			args:             []interface{}{"not-params", validPool},
			wantOriginalSubs: "args[0] has unexpected type string",
		},
		{
			name:             "Args0TypedNil",
			args:             []interface{}{(*common.CreatePoolParams)(nil), validPool},
			wantOriginalSubs: "args[0] (*common.CreatePoolParams) must not be nil",
			wantTrackingID:   vsaerrors.ErrResourceEmptyError,
		},
		{
			name:             "Args1WrongType",
			args:             []interface{}{validParams, "not-pool"},
			wantOriginalSubs: "args[1] has unexpected type string",
		},
		{
			name:             "Args1TypedNil",
			args:             []interface{}{validParams, (*datamodel.Pool)(nil)},
			wantOriginalSubs: "args[1] (*datamodel.Pool) must not be nil",
			wantTrackingID:   vsaerrors.ErrResourceEmptyError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			wf := &ociCreatePoolWorkflow{}
			out, customErr := wf.Run(nil, tc.args...)

			assert.Nil(tt, out, "validation failure should not return a payload")
			require.NotNil(tt, customErr, "expected a *vsaerrors.CustomError, got nil")
			require.NotNil(tt, customErr.OriginalErr, "OriginalErr should preserve the descriptive validation message")
			assert.Contains(tt, customErr.OriginalErr.Error(), tc.wantOriginalSubs)
			if tc.wantTrackingID != 0 {
				assert.Equal(tt, tc.wantTrackingID, customErr.TrackingID,
					"typed-nil cases should be classified as ErrResourceEmptyError so they aren't lumped with generic internal errors")
			}
		})
	}
}

// TestDeriveUpdateTargetSPConfig_BasicFieldMapping verifies the top-level SPConfig override
// derived from request params. Per-node sizing, instance type, flex shape, OCIConfig, dev
// flags etc. are intrinsic to the persisted VLM config and intentionally NOT touched by
// this helper — they ride pristine through req.VLMConfig instead.
func TestDeriveUpdateTargetSPConfig_BasicFieldMapping(t *testing.T) {
	params := &common.UpdatePoolParams{
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024, // 2 TiB
		TotalThroughputMibps: 256,
		AccountName:          "acct",
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		DeploymentName: "dep1",
		Name:           "pool1",
		SizeInBytes:    1 * 1024 * 1024 * 1024 * 1024,
		PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 128},
	}

	sp, err := deriveUpdateTargetSPConfig(params, pool)
	require.NoError(t, err)

	assert.Equal(t, "2048Gi", sp.Size,
		"Size must come from params.SizeInBytes (2 TiB) and override pool.SizeInBytes")
	assert.Empty(t, sp.Throughput)
	assert.Zero(t, sp.IOps)
	assert.Nil(t, sp.HAPairConfigs,
		"empty currentVlmConfig (no HA pairs) must not populate HAPairConfigs")
}

func TestDeriveUpdateTargetSPConfig_OmitsSizeWhenNoCapacityChange(t *testing.T) {
	params := &common.UpdatePoolParams{
		TotalThroughputMibps: 1907,
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		DeploymentName: "dep1",
		Name:           "pool1",
		SizeInBytes:    1024 * 1024 * 1024 * 1024,
		PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 128},
	}

	sp, err := deriveUpdateTargetSPConfig(params, pool)
	require.NoError(t, err)
	assert.Empty(t, sp.Size)
	assert.Empty(t, sp.Throughput)
	assert.Zero(t, sp.IOps)
}

func TestDeriveUpdateTargetSPConfig_NodeCapacitiesEmitSizeEvenWithoutSizeInBytes(t *testing.T) {
	params := &common.UpdatePoolParams{
		NodeCapacities: []common.NodeCapacity{{SizeInGiB: 5500}},
	}
	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{UUID: "u1"},
		SizeInBytes: 4096 * 1024 * 1024 * 1024,
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 954,
			IsRegionalHA:    true,
		},
	}

	sp, err := deriveUpdateTargetSPConfig(params, pool)
	require.NoError(t, err)
	assert.Equal(t, "4096Gi", sp.Size)
}

// TestDeriveUpdateTargetSPConfig_NeverPopulatesHAPairConfigs is the regression guard
// for the homogeneous-only update contract: VCP intentionally does NOT emit per-pair
// overrides on UpdateVSAClusterDeploymentRequest today. The wire field
// SPConfig.HAPairConfigs is `omitempty`, so leaving it nil drops it from the JSON
// payload entirely and VLM applies the pool-level Size/Throughput/IOps uniformly to
// every HA pair. This test pins that no combination of inputs — populated
// NodeCapacities, multi-pair stored clusters, fully-populated DataAggr —
// causes the helper to start producing a per-pair plan again.
func TestDeriveUpdateTargetSPConfig_NeverPopulatesHAPairConfigs(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 256},
	}
	params := &common.UpdatePoolParams{
		SizeInBytes:          1200 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 3814,
	}

	cases := []struct {
		name             string
		nodeCapacities   []common.NodeCapacity
		currentVlmConfig vlm.VLMConfig
	}{
		{
			name:             "no NodeCapacities, empty VLMConfig",
			nodeCapacities:   nil,
			currentVlmConfig: vlm.VLMConfig{},
		},
		{
			name:           "single-pair cluster with NodeCapacity (would have emitted HAPairConfigs[1] historically)",
			nodeCapacities: []common.NodeCapacity{{SizeInGiB: 1200}},
			currentVlmConfig: vlm.VLMConfig{
				Deployment: vlm.DeploymentConfig{VSAInstanceType: "VM.DenseIO.E5.Flex"},
				Cloud: vlm.CloudConfig{HAPairs: []vlm.HAPair{
					{VM1: vlm.VMConfig{Name: "vm-1"}, VM2: vlm.VMConfig{Name: "vm-2"}},
				}},
				DataAggr: []vlm.DataAggrConfig{{Name: "aggr1", HomeNode: "vm-1"}},
			},
		},
		{
			name: "two-pair cluster with NodeCapacities (would have emitted HAPairConfigs[2] historically)",
			nodeCapacities: []common.NodeCapacity{
				{SizeInGiB: 500},
				{SizeInGiB: 700},
			},
			currentVlmConfig: vlm.VLMConfig{
				Deployment: vlm.DeploymentConfig{VSAInstanceType: "VM.DenseIO.E5.Flex"},
				Cloud: vlm.CloudConfig{HAPairs: []vlm.HAPair{
					{VM1: vlm.VMConfig{Name: "vm-1"}, VM2: vlm.VMConfig{Name: "vm-2"}},
					{VM1: vlm.VMConfig{Name: "vm-3"}, VM2: vlm.VMConfig{Name: "vm-4"}},
				}},
				DataAggr: []vlm.DataAggrConfig{
					{Name: "aggr1", HomeNode: "vm-1"},
					{Name: "aggr2", HomeNode: "vm-3"},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			localParams := *params
			localParams.NodeCapacities = tc.nodeCapacities

			sp, err := deriveUpdateTargetSPConfig(&localParams, pool)
			require.NoError(tt, err)
			assert.Equal(tt, "1200Gi", sp.Size, "pool-level Size must still be emitted")
			assert.Empty(tt, sp.Throughput)
			assert.Zero(tt, sp.IOps)
			assert.Nil(tt, sp.HAPairConfigs,
				"HAPairConfigs must stay nil so the omitempty JSON tag drops it from the VLM payload")
		})
	}
}

// TestDeriveUpdateTargetSPConfig_DoesNotMutateCurrentVlmConfig pins the read-only contract
// on currentVlmConfig: the helper must never write to any field of the stored config (it
// only reads Deployment.VSAInstanceType and len(Cloud.HAPairs)). This guards against an
// accidental append/in-place mutation that would corrupt the workflow's pristine copy.
func TestDeriveUpdateTargetSPConfig_DoesNotMutateCurrentVlmConfig(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 256},
	}
	currentVlmConfig := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			VSAInstanceType: "VM.DenseIO.E5.Flex",
			SPConfig: vlm.SPConfig{
				Size:       "200Gi",
				IOps:       4096,
				Throughput: 256,
			},
		},
		Cloud: vlm.CloudConfig{HAPairs: []vlm.HAPair{
			{VM1: vlm.VMConfig{HostName: "vm-1"}, VM2: vlm.VMConfig{HostName: "vm-2"}},
		}},
	}
	snapshot, err := json.Marshal(currentVlmConfig)
	require.NoError(t, err)

	params := &common.UpdatePoolParams{
		SizeInBytes:          1000 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 3814,
		NodeCapacities:       []common.NodeCapacity{{SizeInGiB: 500}},
	}
	_, err = deriveUpdateTargetSPConfig(params, pool)
	require.NoError(t, err)

	after, err := json.Marshal(currentVlmConfig)
	require.NoError(t, err)
	assert.JSONEq(t, string(snapshot), string(after),
		"deriveUpdateTargetSPConfig must treat currentVlmConfig as read-only so the workflow's pristine copy is never corrupted")
}

func testOCIProviderConfig() vlm.ProviderConfigWrapper {
	return vlm.ProviderConfigWrapper{
		ProviderConfig: &vlm.OCIConfig{
			CompartmentID: "ocid1.compartment.oc1..test",
			SubnetID:      "ocid1.subnet.oc1..test",
		},
	}
}

func storedVLMConfigWithHAPairs(n int) *vlm.VLMConfig {
	haPairs := make([]vlm.HAPair, n)
	for i := range haPairs {
		haPairs[i] = vlm.HAPair{
			VM1: vlm.VMConfig{Name: fmt.Sprintf("vm1-ha%d", i)},
			VM2: vlm.VMConfig{Name: fmt.Sprintf("vm2-ha%d", i)},
		}
	}
	return &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			NumHAPair:       n,
			VSAInstanceType: "VM.DenseIO.E5.Flex",
			ProviderConfig:  testOCIProviderConfig(),
		},
		Cloud: vlm.CloudConfig{HAPairs: haPairs},
	}
}

func ociUpdateNodes(n int, sizes ...int64) []*datamodel.Node {
	nodes := make([]*datamodel.Node, 0, 2*n)
	for i := 0; i < 2*n; i++ {
		var size int64
		if i < len(sizes) {
			size = sizes[i]
		}
		nodes = append(nodes, &datamodel.Node{
			BaseModel:      datamodel.BaseModel{UUID: fmt.Sprintf("uuid-node-%d", i)},
			NodeAttributes: &datamodel.NodeDetails{SizeInGiB: size},
		})
	}
	return nodes
}

// storedVLMConfigEmptyCloud matches DB rows where deployment.num_ha_pair is set but cloud.ha_pair was never persisted (JSON null).
func storedVLMConfigEmptyCloud() *vlm.VLMConfig {
	return &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{NumHAPair: 1},
		Cloud:      vlm.CloudConfig{HAPairs: nil},
	}
}

func TestOCIUpdatePoolWorkflow_RejectsEmptyStoredCloudHAPairs(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-upd"},
		Name:                   "p",
		DeploymentName:         "dep-upd",
		PoolExternalIdentifier: "ocid1.pool.oc1..upd",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "pw"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigEmptyCloud(), nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloud.ha_pair")
	env.AssertExpectations(t)
}

func TestOCIUpdatePoolWorkflow_NilPoolCredentialsRejected(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-upd"},
		Name:                   "p",
		DeploymentName:         "dep-upd",
		PoolExternalIdentifier: "ocid1.pool.oc1..upd",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	// With credential resolution delegated to the GetOnTapCredentialsForOCI activity,
	// a nil PoolCredentials is rejected inside that activity (fetchOnTapCredentialsForOCI)
	// rather than by an in-workflow helper. The workflow must still fail and run the
	// ErroredPool compensation.
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIUpdatePoolWorkflow_FetchCredentialsFails asserts that when the
// GetOnTapCredentialsForOCI activity fails (e.g. OCI Vault unreachable), the
// update workflow aborts before any VLM call and runs the ErroredPool
// compensation. The pre-update snapshot restore is not yet registered at the
// credential-fetch step, so ErroredPool is the only compensation expected.
func TestOCIUpdatePoolWorkflow_FetchCredentialsFails(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-upd"},
		Name:                   "p",
		DeploymentName:         "dep-upd",
		PoolExternalIdentifier: "ocid1.pool.oc1..upd",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{AuthType: envs.USERNAME_PWD_SEC_MGR},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return((*vlm.OntapCredentials)(nil), assert.AnError)

	// VLM must never be invoked if credentials cannot be resolved.
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	mockVlm.AssertNotCalled(t, "UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

// TestOCIUpdatePoolWorkflow_UsesFetchedCredentials pins the contract that the
// update flow forwards the credentials returned by GetOnTapCredentialsForOCI
// (e.g. a password resolved from OCI Vault) to VLM, rather than reading
// pool.PoolCredentials.Password directly. The pool uses secret-manager auth
// with an empty Password, so the only way AdminPassword can reach VLM is via
// the activity result.
func TestOCIUpdatePoolWorkflow_UsesFetchedCredentials(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-upd"},
		Name:                   "p",
		DeploymentName:         "dep-upd",
		PoolExternalIdentifier: "ocid1.pool.oc1..upd",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{AuthType: envs.USERNAME_PWD_SEC_MGR},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-generated-pw"}, nil)

	var capturedReq *vlm.UpdateVSAClusterDeploymentRequest
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			capturedReq = args.Get(1).(*vlm.UpdateVSAClusterDeploymentRequest)
		}).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	registerOCIUpdatePoolNodePersistMocks(env, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	require.NotNil(t, capturedReq, "VLM update request must have been captured")
	assert.Equal(t, "vault-generated-pw", capturedReq.OntapCredentials.AdminPassword,
		"OntapCredentials.AdminPassword must come from the GetOnTapCredentialsForOCI activity, not pool.PoolCredentials.Password")
	mockVlm.AssertExpectations(t)
	env.AssertExpectations(t)
}

func TestOCIUpdatePoolWorkflow_VLMRequestFields(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-upd"},
		Name:                   "p",
		DeploymentName:         "dep-upd",
		PoolExternalIdentifier: "ocid1.pool.oc1..upd",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "secret-ontap-pw"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)

	var capturedReq *vlm.UpdateVSAClusterDeploymentRequest
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			capturedReq = args.Get(1).(*vlm.UpdateVSAClusterDeploymentRequest)
		}).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	registerOCIUpdatePoolNodePersistMocks(env, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	require.NotNil(t, capturedReq, "VLM update request must have been captured")
	assert.Equal(t, "secret-ontap-pw", capturedReq.OntapCredentials.AdminPassword,
		"OntapCredentials.AdminPassword must come from pool.PoolCredentials")
	assert.Equal(t, 1, capturedReq.NumHAPair,
		"NumHAPair must come from stored VLM config")
	assert.Equal(t, []int{1}, capturedReq.HAPairIndices,
		"HAPairIndices must contain [1] for single HA pair (VLM contract is 1-based)")
	mockVlm.AssertExpectations(t)
	env.AssertExpectations(t)
}

func TestOCIUpdatePoolWorkflow_ScaleOutSetsTargetNumHAPair(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
		HAPairs:              4,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-scale"},
		Name:                   "p-scale",
		DeploymentName:         "dep-scale",
		PoolExternalIdentifier: "ocid1.pool.oc1..scale",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "pw"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)

	var capturedReqs []*vlm.UpdateVSAClusterDeploymentRequest
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			req := args.Get(1).(*vlm.UpdateVSAClusterDeploymentRequest)
			cp := *req
			capturedReqs = append(capturedReqs, &cp)
		}).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
			Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
		}}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	registerOCIUpdatePoolNodePersistMocks(env, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	require.NotEmpty(t, capturedReqs, "VLM update must have been called")
	seen := map[int]struct{}{}
	for _, r := range capturedReqs {
		assert.Equal(t, 4, r.NumHAPair, "scale-out (dataEndpointCount=8) must send target NumHAPair=4 on every batch")
		for _, idx := range r.HAPairIndices {
			seen[idx] = struct{}{}
		}
	}
	assert.Equal(t, map[int]struct{}{1: {}, 2: {}, 3: {}, 4: {}}, seen, "all 4 target HA pairs must be batched")
	mockVlm.AssertExpectations(t)
	env.AssertExpectations(t)
}

func TestOCIUpdatePoolWorkflow_VLMRequestFieldsFromStoredConfig(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-upd"},
		Name:                   "p",
		DeploymentName:         "dep-upd",
		PoolExternalIdentifier: "ocid1.pool.oc1..upd",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "secret-ontap-pw"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(3), nil)

	var capturedReqs []*vlm.UpdateVSAClusterDeploymentRequest
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			capturedReqs = append(capturedReqs, args.Get(1).(*vlm.UpdateVSAClusterDeploymentRequest))
		}).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
			Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
		}}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	registerOCIUpdatePoolNodePersistMocks(env, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	require.Len(t, capturedReqs, 3, "VLM update runs one batch per HA pair for numHAPairs=3 with default ITC parallelism")
	assert.Equal(t, 3, capturedReqs[0].NumHAPair,
		"NumHAPair must come from stored VLM config")
	assert.Equal(t, []int{1}, capturedReqs[0].HAPairIndices)
	assert.Equal(t, []int{2}, capturedReqs[1].HAPairIndices)
	assert.Equal(t, []int{3}, capturedReqs[2].HAPairIndices)
	require.Len(t, capturedReqs[0].VLMConfig.Cloud.HAPairs, 3,
		"Cloud.HAPairs must be carried over from stored VLM config")
	assert.Equal(t, "vm1-ha0", capturedReqs[0].VLMConfig.Cloud.HAPairs[0].VM1.Name,
		"Cloud.HAPairs VM details must match stored config")
	mockVlm.AssertExpectations(t)
	env.AssertExpectations(t)
}

// TestOCIUpdatePoolWorkflow_VLMConfigIsPristineOnTheWire pins down the contract that the
// stored vlm_config is read-only for VCP: when the user updates the pool with a new
// size/throughput, req.VLMConfig.Deployment.SPConfig must STILL carry the DB-stored
// values (the override lives only on top-level req.SPConfig). VLM treats vlm_config
// as authoritative state and we must never overwrite intrinsic deployment fields on
// the wire.
func TestOCIUpdatePoolWorkflow_VLMConfigIsPristineOnTheWire(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	// Target update: bump pool size from 200 GiB (DB) to 3000 GiB. NodeCapacities is
	// pre-populated to match the 1-HA-pair stored config (the factory layer publishes
	// this slice in production); the workflow currently emits a pool-level-only
	// SPConfig override regardless.
	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          3000 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 3814,
		NodeCapacities:       []common.NodeCapacity{{SizeInGiB: 3000}},
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-pristine"},
		Name:                   "p-pristine",
		DeploymentName:         "dep-pristine",
		PoolExternalIdentifier: "ocid1.pool.oc1..pristine",
		SizeInBytes:            200 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 256},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "pw"},
	}

	// Stored config mirrors what a real cluster row looks like: a populated Cloud.HAPairs
	// plus intrinsic Deployment fields (size, instance type, flex shape) we must NOT touch.
	// DataAggr is set on the fixture for realism only; the update workflow does not read
	// it today because SPConfig.HAPairConfigs is intentionally left nil on the wire.
	storedVlmConfig := storedVLMConfigWithHAPairs(1)
	storedVlmConfig.Deployment.SPConfig = vlm.SPConfig{
		Size:       "200Gi",
		IOps:       4096,
		Throughput: 256,
	}
	storedVlmConfig.Deployment.VSAInstanceType = "VM.DenseIO.E5.Flex"
	storedVlmConfig.Deployment.ProviderConfig = vlm.ProviderConfigWrapper{
		ProviderConfig: vlm.OCIConfig{
			VSAFlexOcpus:       8,
			VSAFlexMemoryInGBs: 96,
		},
	}
	storedVlmConfig.DataAggr = []vlm.DataAggrConfig{
		{Name: "aggr1", HomeNode: "vm1-ha0"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVlmConfig, nil)
	var captured *vlm.UpdateVSAClusterDeploymentRequest
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			req := args.Get(1).(*vlm.UpdateVSAClusterDeploymentRequest)
			cp := *req
			captured = &cp
		}).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	registerOCIUpdatePoolNodePersistMocks(env, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NotNil(t, captured, "VLM UpdateVSAClusterDeployment must have been called")

	// req.VLMConfig must be byte-for-byte the stored DB config — including the OLD SPConfig.
	assert.Equal(t, "200Gi", captured.VLMConfig.Deployment.SPConfig.Size,
		"req.VLMConfig.Deployment.SPConfig.Size must carry the DB-stored value, not the update target")
	assert.Equal(t, int64(256), captured.VLMConfig.Deployment.SPConfig.Throughput,
		"req.VLMConfig.Deployment.SPConfig.Throughput must carry the DB-stored value")
	assert.Equal(t, int64(4096), captured.VLMConfig.Deployment.SPConfig.IOps,
		"req.VLMConfig.Deployment.SPConfig.IOps must carry the DB-stored value")
	assert.Equal(t, "VM.DenseIO.E5.Flex", captured.VLMConfig.Deployment.VSAInstanceType,
		"req.VLMConfig.Deployment.VSAInstanceType must come from the stored config, not env/defaults")
	capturedOCI, capturedOCIErr := captured.VLMConfig.Deployment.ProviderConfig.AsOCI()
	require.NoError(t, capturedOCIErr)
	assert.Equal(t, float32(8), capturedOCI.VSAFlexOcpus,
		"req.VLMConfig.Deployment.ProviderConfig VSAFlexOcpus must come from the stored config")
	assert.Equal(t, float32(96), capturedOCI.VSAFlexMemoryInGBs,
		"req.VLMConfig.Deployment.ProviderConfig VSAFlexMemoryInGBs must come from the stored config")

	// Top-level override is the ONLY place the update target appears.
	assert.Equal(t, "3000Gi", captured.SPConfig.Size,
		"req.SPConfig.Size must carry the update target (params.SizeInBytes)")
	assert.Zero(t, captured.SPConfig.Throughput,
		"req.SPConfig.Throughput must not be sent on OCI update")
	assert.Zero(t, captured.SPConfig.IOps,
		"req.SPConfig.IOps must not be sent on OCI update")
	assert.Equal(t, 1, captured.NumHAPair,
		"req.NumHAPair must reflect the current cluster HA-pair count")

	// SPConfig.HAPairConfigs is intentionally NOT populated today (homogeneous-only
	// update contract). The wire field carries `omitempty`, so a nil slice is dropped
	// from the JSON payload entirely and VLM applies the pool-level Size/Throughput/IOps
	// uniformly to every HA pair. SPConfig.IsHeterogeneous likewise rides the zero value.
	assert.Nil(t, captured.SPConfig.HAPairConfigs,
		"VCP must not populate per-pair overrides on update; pool-level SPConfig is the only override")
	mockVlm.AssertExpectations(t)
	env.AssertExpectations(t)
}

// TestOCIUpdatePoolWorkflow_UpdateStatusPartialFailureRejected asserts that a 200 OK
// response from VLM carrying a non-empty UpdateStatus (e.g. SPUpdateFail) is treated as
// a partial-update failure: the workflow must NOT persist vlm_config, NOT advance to
// subsequent batches, and fail with ErrIncorrectVSAClusterState surfacing the failed
// sub-operations (rollback only restores pool-level fields).
func TestOCIUpdatePoolWorkflow_UpdateStatusPartialFailureRejected(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-partial"},
		Name:                   "p-partial",
		DeploymentName:         "dep-partial",
		PoolExternalIdentifier: "ocid1.pool.oc1..partial",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "pw"},
	}

	// 3-HA-pair cluster so we can also prove subsequent batches are NOT issued after
	// the first batch returns a partial-failure UpdateStatus.
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(3), nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Track how many times VLM is invoked: must be exactly 1 (the failing batch); the
	// remaining 2 batches must NOT be issued.
	var vlmCalls int
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { vlmCalls++ }).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{
			VLMConfig: vlm.VLMConfig{
				Deployment: vlm.DeploymentConfig{NumHAPair: 3},
			},
			UpdateStatus: vlm.DeploymentUpdateStatus{
				SPUpdateFail: true,
				LifUpFail:    true,
			},
		}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err, "partial UpdateStatus must surface as a workflow error")
	assert.Contains(t, err.Error(), "partial update failure",
		"error message must identify it as a partial update failure")
	assert.Contains(t, err.Error(), "sp_update",
		"error message must enumerate the sp_update failure flag")
	assert.Contains(t, err.Error(), "lif_up",
		"error message must enumerate the lif_up failure flag")

	assert.Equal(t, 1, vlmCalls,
		"only the first (failing) batch must be issued to VLM; the workflow must NOT advance to subsequent batches after a partial-failure UpdateStatus")

	mockVlm.AssertExpectations(t)
	env.AssertNotCalled(t, "UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestUpdateStatusFailureFlags pins the unit-level mapping from DeploymentUpdateStatus
// booleans to the human-readable sub-operation names surfaced in errors and logs.
func TestUpdateStatusFailureFlags(t *testing.T) {
	t.Run("returns empty slice when no flags are set", func(tt *testing.T) {
		assert.Empty(tt, updateStatusFailureFlags(vlm.DeploymentUpdateStatus{}))
	})

	t.Run("preserves declaration order across multiple flags", func(tt *testing.T) {
		// Set every flag and assert the canonical order so log/error output is stable.
		got := updateStatusFailureFlags(vlm.DeploymentUpdateStatus{
			DetachFail:   true,
			SPUpdateFail: true,
			AttachFail:   true,
			LifDownFail:  true,
			AggrDownFail: true,
			AggrUpFail:   true,
			LifUpFail:    true,
		})
		assert.Equal(tt, []string{"detach", "sp_update", "attach", "lif_down", "aggr_down", "aggr_up", "lif_up"}, got)
	})

	t.Run("returns only the set flags", func(tt *testing.T) {
		got := updateStatusFailureFlags(vlm.DeploymentUpdateStatus{
			SPUpdateFail: true,
			AggrUpFail:   true,
		})
		assert.Equal(tt, []string{"sp_update", "aggr_up"}, got)
	})
}

// stageCounter is a small helper that reads the current value of the
// oci_workflow_stage_total counter for a given (stage, result) label set. The
// metric is process-global, so tests must compare deltas (after-before) rather
// than absolute values to remain robust against parallel/preceding tests.
func stageCounter(stage, result string) float64 {
	return testutil.ToFloat64(workflowStageTotal.WithLabelValues(wfUpdatePool, queueCustomer, stage, result))
}

// TestOCIUpdatePoolWorkflow_PerBatchVLMFailure_EmitsStageVLMUpdate is the partner
// to the DB-persist test above: when the VLM call itself fails inside the batch
// loop, the metric is correctly stamped stageVLMUpdate=failure (at the inner
// emit site, with no double-counting from the outer caller, which no longer
// re-emits on every inner error after the H5 fix).
func TestOCIUpdatePoolWorkflow_PerBatchVLMFailure_EmitsStageVLMUpdate(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-h5-vlmfail"},
		Name:                   "p-h5-vlmfail",
		DeploymentName:         "dep-h5-vlmfail",
		PoolExternalIdentifier: "ocid1.pool.oc1..h5-vlmfail",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "pw"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)
	env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return((*vlm.UpdateVSAClusterDeploymentResponse)(nil), errors.New("simulated VLM call failure"))
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	vlmUpdateBefore := stageCounter(stageVLMUpdate, resultFailure)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError(), "per-batch VLM failure must surface as a workflow error")

	vlmUpdateAfter := stageCounter(stageVLMUpdate, resultFailure)

	assert.InDelta(t, 1.0, vlmUpdateAfter-vlmUpdateBefore, 0,
		"per-batch VLM call failure must emit stage=vlm_update,result=failure exactly once (from the inner emit site, no double-count)")
	env.AssertNotCalled(t, "UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestOCIUpdatePoolWorkflow_Success(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-upd"},
		Name:                   "p",
		DeploymentName:         "dep-upd",
		PoolExternalIdentifier: "ocid1.pool.oc1..upd",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "test-ontap-password"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	registerOCIUpdatePoolNodePersistMocks(env, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIUpdatePoolWorkflow_PersistsNodeSizeAndInstanceType(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-node-persist", ID: 77},
		Name:                   "p",
		DeploymentName:         "dep-node-persist",
		PoolExternalIdentifier: "ocid1.pool.oc1..node",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "test-ontap-password"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)

	respConfig := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{VSAInstanceType: "VM.DenseIO.E5.Flex"},
		Cloud: vlm.CloudConfig{HAPairs: []vlm.HAPair{
			{
				VM1: vlm.VMConfig{HostName: "vm-01", DataDisks: []vlm.DiskConfig{{Size: 1000}, {Size: 1000}}},
				VM2: vlm.VMConfig{HostName: "vm-02", DataDisks: []vlm.DiskConfig{{Size: 1500}, {Size: 1500}}},
			},
		}},
	}

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: respConfig}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{Name: "vm-01", NodeAttributes: &datamodel.NodeDetails{SizeInGiB: 1000, InstanceType: "old-type"}},
		{Name: "vm-02", NodeAttributes: &datamodel.NodeDetails{SizeInGiB: 1500, InstanceType: "old-type"}},
	}, nil)
	env.OnActivity("RestoreOCINodesPreUpdateFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	var captured map[string]datamodel.NodeDetails
	mockStorage.On("UpdateNodesSizeAndInstanceType", mock.Anything, int64(77), mock.Anything).
		Run(func(args mock.Arguments) {
			captured = args.Get(2).(map[string]datamodel.NodeDetails)
		}).Return(nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.Equal(t, datamodel.NodeDetails{InstanceType: "VM.DenseIO.E5.Flex", SizeInGiB: 2000}, captured["vm-01"])
	assert.Equal(t, datamodel.NodeDetails{InstanceType: "VM.DenseIO.E5.Flex", SizeInGiB: 3000}, captured["vm-02"])
	mockStorage.AssertExpectations(t)
}

func TestOCIUpdatePoolWorkflow_NodeSizePersistFails(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-node-persist-fail", ID: 88},
		Name:                   "p",
		DeploymentName:         "dep-node-persist-fail",
		PoolExternalIdentifier: "ocid1.pool.oc1..nodefail",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "test-ontap-password"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)

	respConfig := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{VSAInstanceType: "VM.DenseIO.E5.Flex"},
		Cloud: vlm.CloudConfig{HAPairs: []vlm.HAPair{
			{
				VM1: vlm.VMConfig{HostName: "vm-01", DataDisks: []vlm.DiskConfig{{Size: 1000}}},
				VM2: vlm.VMConfig{HostName: "vm-02", DataDisks: []vlm.DiskConfig{{Size: 1000}}},
			},
		}},
	}

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: respConfig}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	snapshotNodes := []*datamodel.Node{
		{Name: "vm-01", NodeAttributes: &datamodel.NodeDetails{SizeInGiB: 1000, InstanceType: "old-type"}},
		{Name: "vm-02", NodeAttributes: &datamodel.NodeDetails{SizeInGiB: 1000, InstanceType: "old-type"}},
	}
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(snapshotNodes, nil)
	var restoreNodeSnapshot map[string]datamodel.NodeDetails
	env.OnActivity("UpdateOCINodesFromVLMConfig", mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
	env.OnActivity("RestoreOCINodesPreUpdateFields", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			restoreNodeSnapshot = args.Get(2).(map[string]datamodel.NodeDetails)
		}).
		Return(nil)

	env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Equal(t, datamodel.NodeDetails{SizeInGiB: 1000, InstanceType: "old-type"}, restoreNodeSnapshot["vm-01"])
}

func TestOCIUpdatePoolWorkflow_VLMUpdateFails(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-upd"},
		Name:                   "p",
		DeploymentName:         "dep-upd",
		PoolExternalIdentifier: "ocid1.pool.oc1..upd",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "test-ontap-password"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return((*vlm.UpdateVSAClusterDeploymentResponse)(nil), assert.AnError)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	// On VLM failure, the rollback chain runs RestorePoolPreUpdatePoolLevelFields → ErroredPool.
	env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIUpdatePoolWorkflow_PersistFails(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-upd"},
		Name:                   "p",
		DeploymentName:         "dep-upd",
		PoolExternalIdentifier: "ocid1.pool.oc1..upd",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "test-ontap-password"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Pool)(nil), assert.AnError)
	// When the final pool-level persist fails after VLM succeeded, the rollback chain
	// must run RestorePoolPreUpdatePoolLevelFields before ErroredPool stamps the final state.
	env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIUpdatePoolWorkflow_FinalPersistUsesLastBatchVLMConfig asserts that vlm_config
// is written only once at the end via UpdatedPoolWithVLMConfig, using the last batch's
// resp.VLMConfig after every batch succeeds.
func TestOCIUpdatePoolWorkflow_FinalPersistUsesLastBatchVLMConfig(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-batched"},
		Name:                   "p-batched",
		DeploymentName:         "dep-batched",
		PoolExternalIdentifier: "ocid1.pool.oc1..batched",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "pw"},
	}

	// 3 HA pairs with default ITC parallelism = 4 → 3 batches of 1 pair each.
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(3), nil)

	// Make each batch return a distinguishable VLMConfig so we can assert that the
	// per-batch persist captured THAT batch's response, not the seed/initial config.
	batchVLMConfigs := []vlm.VLMConfig{
		{Deployment: vlm.DeploymentConfig{DeploymentID: "after-batch-1", ProviderConfig: testOCIProviderConfig()}},
		{Deployment: vlm.DeploymentConfig{DeploymentID: "after-batch-2", ProviderConfig: testOCIProviderConfig()}},
		{Deployment: vlm.DeploymentConfig{DeploymentID: "after-batch-3", ProviderConfig: testOCIProviderConfig()}},
	}
	var batchCall int32
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ workflow.Context, _ *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
			i := batchCall
			batchCall++
			return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: batchVLMConfigs[i]}
		}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	var finalVLMConfig vlm.VLMConfig
	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			finalVLMConfig = args.Get(2).(vlm.VLMConfig)
		}).
		Return(pool, nil)
	registerOCIUpdatePoolNodePersistMocks(env, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, batchVLMConfigs[2].Deployment.DeploymentID, finalVLMConfig.Deployment.DeploymentID,
		"final persist must use the last batch's VLMConfig, not an intermediate batch")
	env.AssertExpectations(t)
}

// TestOCIUpdatePoolWorkflow_BatchFailureLeavesVLMConfigUnchanged asserts that when
// batch N fails, vlm_config is never written (no per-batch persist), the final
// UpdatedPoolWithVLMConfig is never called, and rollback restores only pool-level fields.
func TestOCIUpdatePoolWorkflow_BatchFailureLeavesVLMConfigUnchanged(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-partial"},
		Name:                   "p-partial",
		DeploymentName:         "dep-partial",
		PoolExternalIdentifier: "ocid1.pool.oc1..partial",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "pw"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(3), nil)

	// 3 batches: batch 1 succeeds, batch 2 fails. Batch 3 must never be attempted.
	var batchCall int32
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, _ *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				i := batchCall
				batchCall++
				if i == 0 {
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
						Deployment: vlm.DeploymentConfig{DeploymentID: "after-batch-1", ProviderConfig: testOCIProviderConfig()},
					}}
				}
				return nil
			},
			func(_ workflow.Context, _ *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if batchCall == 2 {
					return assert.AnError
				}
				return nil
			},
		)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	// Compensation chain: must run RestorePoolPreUpdatePoolLevelFields then ErroredPool.
	env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	// The final pool-level persist must NEVER be called when a VLM batch fails.
	// We do not register UpdatedPoolWithVLMConfig and rely on env.AssertExpectations
	// plus the workflow erroring out to confirm it stayed unreached.

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Equal(t, int32(2), batchCall, "VLM should have been attempted for batch 1 (success) and batch 2 (failure); batch 3 must NOT be attempted")
	env.AssertExpectations(t)
	env.AssertNotCalled(t, "UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestOCIUpdatePoolWorkflow_DBPersistFailureCompensates asserts that when the
// final pool-level UpdatedPoolWithVLMConfig fails after every VLM batch has
// succeeded, rollback runs RestorePoolPreUpdatePoolLevelFields to rewind the pre-update
// snapshot (pool-level fields and vlm_config), then ErroredPool stamps the final error.
// (The cluster is intentionally NOT rolled back — mid-flight shrink is unsafe.)
func TestOCIUpdatePoolWorkflow_DBPersistFailureCompensates(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
		TotalThroughputMibps: 256,
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-compensate"},
		Name:                   "p-compensate",
		DeploymentName:         "dep-compensate",
		PoolExternalIdentifier: "ocid1.pool.oc1..compensate",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "pw"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(1), nil)

	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: "after-batch-1"}}}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), assert.AnError)

	var compensationPoolUUIDs []string
	env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			compensationPoolUUIDs = append(compensationPoolUUIDs, args.Get(1).(string))
		}).
		Return(nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.NotEmpty(t, compensationPoolUUIDs, "DB compensation RestorePoolPreUpdatePoolLevelFields must run when final pool-level persist fails")
	assert.Equal(t, "pool-uuid-compensate", compensationPoolUUIDs[0],
		"compensation must target the pre-update pool snapshot (same UUID as the input pool)")
	env.AssertExpectations(t)
}

func storedVLMConfigWithHostNamedHAPairs(n int) *vlm.VLMConfig {
	haPairs := make([]vlm.HAPair, n)
	for i := range haPairs {
		haPairs[i] = vlm.HAPair{
			VM1: vlm.VMConfig{Name: fmt.Sprintf("vm1-ha%d", i), HostName: fmt.Sprintf("host-vm1-ha%d", i)},
			VM2: vlm.VMConfig{Name: fmt.Sprintf("vm2-ha%d", i), HostName: fmt.Sprintf("host-vm2-ha%d", i)},
		}
	}
	return &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{NumHAPair: n},
		Cloud:      vlm.CloudConfig{HAPairs: haPairs},
	}
}

// TestOCIUpdatePoolWorkflow_HomogeneousNodeCapacities_FlatSPConfig asserts that
// when the request carries nodeCapacities with all-equal sizes, the workflow emits a
// plain homogeneous SPConfig: just Size/IOps/Throughput, with HAPairConfigs left nil
// (the `omitempty` JSON tag drops it from the wire). The factory-derived
// params.SizeInBytes follows the per-pair-sum convention (each HA pair owns one
// mirrored aggregate; summing per-node would double-count the mirror) — same
// semantics as create — and that drives SPConfig.Size on the wire. The batch plan
// covers all HA pairs in the cluster.
func TestOCIUpdatePoolWorkflow_HomogeneousNodeCapacities_FlatSPConfig(t *testing.T) {
	setTestOCIImageEnv(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	// SizeInBytes is what the factory would have set after deriving the per-pair plan:
	// 1 HA pair × 4096 GiB per-pair aggregate = 4096 GiB pool-total (4 TiB). Summing
	// per-node (8192) would double-count the mirror. The workflow no longer reads
	// NodeCapacities directly — they're listed here only to mirror the request the
	// factory would have validated.
	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		TotalThroughputMibps: 256,
		SizeInBytes:          uint64(4096) * 1024 * 1024 * 1024,
		NodeCapacities: []common.NodeCapacity{
			{NodeUUID: "uuid-vm1-ha0", SizeInGiB: 4096},
			{NodeUUID: "uuid-vm2-ha0", SizeInGiB: 4096},
		},
	}
	pool := &datamodel.Pool{
		BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-homo-nc"},
		Name:                   "p-homo-nc",
		DeploymentName:         "dep-homo-nc",
		PoolExternalIdentifier: "ocid1.pool.oc1..homo-nc",
		SizeInBytes:            1024 * 1024 * 1024 * 1024,
		Network:                "subnet-1",
		Account:                &datamodel.Account{Name: "test-account"},
		PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
		PoolCredentials:        &datamodel.PoolCredentials{Password: "pw"},
	}

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHostNamedHAPairs(1), nil)
	var captured *vlm.UpdateVSAClusterDeploymentRequest
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			req := args.Get(1).(*vlm.UpdateVSAClusterDeploymentRequest)
			cp := *req
			captured = &cp
		}).
		Return(&vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	defer func() { workflows.GetNewVSAClientWorkflowManager = orig }()

	env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	registerOCIUpdatePoolNodePersistMocks(env, nil)

	env.ExecuteWorkflow(OCIUpdatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NotNil(t, captured, "VLM UpdateVSAClusterDeployment must have been called")

	sp := captured.SPConfig
	assert.Equal(t, "4096Gi", sp.Size, "homogeneous Size must come from params.SizeInBytes (pool-total = sum of per-pair aggregates; matches the VLM/CREATE convention)")
	assert.Zero(t, sp.Throughput)
	assert.Zero(t, sp.IOps)
	assert.Equal(t, []int{1}, captured.HAPairIndices, "single-HA-pair cluster must target pair 1 (VLM contract is 1-based)")
	env.AssertExpectations(t)
}

func TestOCIUpdatePoolWorkflow_RunArgsValidation(t *testing.T) {
	validParams := &common.UpdatePoolParams{AccountName: "a"}
	validPool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "u"}, Name: "p"}

	cases := []struct {
		name             string
		args             []interface{}
		wantOriginalSubs string
		wantTrackingID   int
	}{
		{
			name:             "ZeroArgs",
			args:             nil,
			wantOriginalSubs: "expected 2 args, got 0",
		},
		{
			name:             "OneArg",
			args:             []interface{}{validParams},
			wantOriginalSubs: "expected 2 args, got 1",
		},
		{
			name:             "Args0WrongType",
			args:             []interface{}{"not-params", validPool},
			wantOriginalSubs: "args[0] has unexpected type string",
		},
		{
			name:             "Args0TypedNil",
			args:             []interface{}{(*common.UpdatePoolParams)(nil), validPool},
			wantOriginalSubs: "args[0] (*common.UpdatePoolParams) must not be nil",
			wantTrackingID:   vsaerrors.ErrResourceEmptyError,
		},
		{
			name:             "Args1WrongType",
			args:             []interface{}{validParams, "not-pool"},
			wantOriginalSubs: "args[1] has unexpected type string",
		},
		{
			name:             "Args1TypedNil",
			args:             []interface{}{validParams, (*datamodel.Pool)(nil)},
			wantOriginalSubs: "args[1] (*datamodel.Pool) must not be nil",
			wantTrackingID:   vsaerrors.ErrResourceEmptyError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			wf := &ociUpdatePoolWorkflow{}
			out, customErr := wf.Run(nil, tc.args...)

			assert.Nil(tt, out, "validation failure should not return a payload")
			require.NotNil(tt, customErr, "expected a *vsaerrors.CustomError, got nil")
			require.NotNil(tt, customErr.OriginalErr, "OriginalErr should preserve the descriptive validation message")
			assert.Contains(tt, customErr.OriginalErr.Error(), tc.wantOriginalSubs)
			if tc.wantTrackingID != 0 {
				assert.Equal(tt, tc.wantTrackingID, customErr.TrackingID)
			}
		})
	}
}

func TestOCIUpdatePoolWorkflow_SetupRejectsWrongType(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	env.RegisterWorkflow(func(ctx workflow.Context) error {
		wf := &ociUpdatePoolWorkflow{}
		return wf.Setup(ctx, "not-a-params-struct")
	})
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		wf := &ociUpdatePoolWorkflow{}
		return wf.Setup(ctx, "not-a-params-struct")
	})

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid input type")
}

func TestNewPoolBuildInfo(t *testing.T) {
	t.Run("StampsImagesAndCurrentOntapVersionForNonAllowlistedAccount", func(tt *testing.T) {
		withOCIOntapVersionDetails(tt, testOCIOntapVersion)
		utils.SetExperimentalVersionAllowlistedAccountsForTesting("")
		tt.Cleanup(func() { utils.SetExperimentalVersionAllowlistedAccountsForTesting("") })

		stamp := time.Date(2026, time.May, 4, 10, 0, 0, 0, time.UTC)
		vlmCfg := vlm.VLMConfig{
			Deployment: vlm.DeploymentConfig{
				Images: vlm.ImageConfig{
					VSAImageName:      testVSAImageOCID,
					MediatorImageName: testMediatorImageOCID,
				},
			},
		}

		got := NewPoolBuildInfo(stamp, vlmCfg, "non-allowlisted-account")

		require.NotNil(tt, got)
		assert.Equal(tt, testVSAImageOCID, got.VSABuildImage,
			"VSABuildImage must come from VLMConfig.Deployment.Images.VSAImageName")
		assert.Equal(tt, testMediatorImageOCID, got.MediatorBuildImage,
			"MediatorBuildImage must come from VLMConfig.Deployment.Images.MediatorImageName")
		assert.Equal(tt, testOCIOntapVersion, got.OntapVersion,
			"non-allowlisted accounts must receive env.CurrentOntapVersionDetails after ExtractOntapVersion")
		assert.Equal(tt, stamp, got.BuildTimestamp,
			"BuildTimestamp must echo the caller-supplied time (replay-safety contract)")
		assert.Empty(tt, got.RbacFileHash, "RbacFileHash should remain empty until the OCI RBAC validation flow lands")
		assert.Empty(tt, got.RbacFileUrl, "RbacFileUrl should remain empty until the OCI RBAC validation flow lands")
	})

	t.Run("AllowlistedAccountReceivesExperimentalOntapVersion", func(tt *testing.T) {
		const (
			currentVersion      = "9.17.1P2"
			experimentalVersion = "9.18.1"
			allowlistedAccount  = "experimental-account"
		)

		origCurrent := envs.CurrentOntapVersionDetails
		origExperimental := envs.ExperimentalOntapVersionDetails
		envs.CurrentOntapVersionDetails = currentVersion
		envs.ExperimentalOntapVersionDetails = experimentalVersion
		tt.Cleanup(func() {
			envs.CurrentOntapVersionDetails = origCurrent
			envs.ExperimentalOntapVersionDetails = origExperimental
		})

		utils.SetExperimentalVersionAllowlistedAccountsForTesting(allowlistedAccount)
		tt.Cleanup(func() { utils.SetExperimentalVersionAllowlistedAccountsForTesting("") })

		vlmCfg := vlm.VLMConfig{
			Deployment: vlm.DeploymentConfig{
				Images: vlm.ImageConfig{
					VSAImageName:      testVSAImageOCID,
					MediatorImageName: testMediatorImageOCID,
				},
			},
		}

		got := NewPoolBuildInfo(time.Now(), vlmCfg, allowlistedAccount)

		require.NotNil(tt, got)
		assert.Equal(tt, experimentalVersion, got.OntapVersion,
			"allowlisted accounts must receive env.ExperimentalOntapVersionDetails (matches the delete-path call to GetOntapVersionBasedOnAllowlisting)")
	})

	t.Run("EmptyVersionDetailsProduceEmptyOntapVersion", func(tt *testing.T) {
		withOCIOntapVersionDetails(tt, "")
		stamp := time.Now()

		vlmCfg := vlm.VLMConfig{}

		got := NewPoolBuildInfo(stamp, vlmCfg, "any-account")

		require.NotNil(tt, got)
		assert.Empty(tt, got.VSABuildImage)
		assert.Empty(tt, got.MediatorBuildImage)
		assert.Empty(tt, got.OntapVersion,
			"with no current/experimental version configured, OntapVersion must be empty (no spurious default)")
		assert.Equal(tt, stamp, got.BuildTimestamp)
	})
}

func TestOCICreatePoolWorkflow_PersistsBuildInfo(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	// The OCI create-pool workflow now always invokes CreateOnTapCredentialsForOCI
	// before SaveVSANodeDetails / CreatedPool. Without a stub the activity tries
	// to initialise a real OCI Vault client and fails its retries, rolling the
	// workflow back before UpdatePoolFields is ever reached.
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	const wantPoolUUID = "test-pool-uuid-buildinfo"
	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: wantPoolUUID},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
			Deployment: vlm.DeploymentConfig{
				Images: vlm.ImageConfig{
					VSAImageName:      testVSAImageOCID,
					MediatorImageName: testMediatorImageOCID,
				},
			},
		}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Node)(nil), nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	var (
		gotPoolUUID  string
		gotBuildInfo *datamodel.PoolBuildInfo
	)
	// The workflow now calls UpdatePoolFields twice on the happy path:
	// once for "pool_credentials" (the OCI vault secret-ref write-back)
	// and once for "build_info" (what this test owns). A single permissive
	// stub captures only the build_info call by filtering on the updates
	// map key; the pool_credentials call passes through with no capture
	// and a nil return. Cannot use .Once() here because two invocations
	// are expected, and cannot scope to "build_info" via MatchedBy at the
	// registration level without complicating the existing matcher shape.
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			updates, ok := args[2].(map[string]interface{})
			if !ok {
				return
			}
			raw, ok := updates["build_info"]
			if !ok {
				return
			}
			gotPoolUUID, _ = args[1].(string)
			encoded, err := json.Marshal(raw)
			if err != nil {
				return
			}
			var bi datamodel.PoolBuildInfo
			if err := json.Unmarshal(encoded, &bi); err != nil {
				return
			}
			gotBuildInfo = &bi
		}).
		Return(nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	assert.Equal(t, wantPoolUUID, gotPoolUUID,
		"UpdatePoolFields must target the exact pool the workflow is creating")

	require.NotNil(t, gotBuildInfo,
		"UpdatePoolFields was never invoked with a build_info payload — the persistence step is missing")
	assert.Equal(t, testVSAImageOCID, gotBuildInfo.VSABuildImage,
		"VSABuildImage must come from the VLM response VLMConfig.Deployment.Images.VSAImageName")
	assert.Equal(t, testMediatorImageOCID, gotBuildInfo.MediatorBuildImage,
		"MediatorBuildImage must come from the VLM response VLMConfig.Deployment.Images.MediatorImageName")
	assert.Equal(t, testOCIOntapVersion, gotBuildInfo.OntapVersion,
		"OntapVersion must reflect env.CurrentOntapVersionDetails for non-allowlisted accounts (mirrors the delete-path call to utils.GetOntapVersionBasedOnAllowlisting)")
	assert.False(t, gotBuildInfo.BuildTimestamp.IsZero(),
		"BuildTimestamp must be stamped with workflow.Now(ctx), not left as the zero value")

	env.AssertExpectations(t)
}

func TestOCICreatePoolWorkflow_BuildInfoPersistFailureIsNonFatal(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	// See sibling TestOCICreatePoolWorkflow_PersistsBuildInfo: without this stub
	// the OCI ontap-credential activity hits a real OCI Vault client init and
	// the workflow rolls back before reaching the UpdatePoolFields call this
	// test is targeting.
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid-buildinfo-fail"},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Node)(nil), nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	// The workflow makes TWO UpdatePoolFields calls on the happy path:
	//   1. "pool_credentials" — BEFORE CreatedPool. This is FATAL on failure
	//      (the pool isn't marked ready yet, so a rollback is the safe
	//      response). This test is not exercising that branch; it must
	//      succeed so the workflow reaches the build_info call below.
	//   2. "build_info" — AFTER CreatedPool. This is NON-FATAL on failure
	//      (the pool is already marked ready; failing the workflow would
	//      invalidate a usable pool). That is the contract under test.
	// Route the two calls by inspecting the updates-map key.
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything,
		mock.MatchedBy(func(updates map[string]interface{}) bool {
			_, ok := updates["pool_credentials"]
			return ok
		}),
	).Return(nil)

	updatePoolFieldsAttempted := false
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything,
		mock.MatchedBy(func(updates map[string]interface{}) bool {
			_, ok := updates["build_info"]
			return ok
		}),
	).Run(func(args mock.Arguments) { updatePoolFieldsAttempted = true }).
		Return(assert.AnError)

	erroredPoolCalled := false
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { erroredPoolCalled = true }).
		Return(pool, nil).
		Maybe()

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError(),
		"build_info persistence failure must be swallowed (logged as non-critical), not surfaced as a workflow error")

	assert.True(t, updatePoolFieldsAttempted,
		"the build_info UpdatePoolFields call must actually be exercised — otherwise this test proves nothing about the failure branch")
	assert.False(t, erroredPoolCalled,
		"ErroredPool/rollback must NOT fire on build_info persistence failure: the pool is already marked ready by CreatedPool, so a rollback here would invalidate a usable pool")
}

// TestOCIDeletePoolWorkflow_RunArgsValidation mirrors the Create-side coverage
// for (*ociDeletePoolWorkflow).Run. Same validation block, same rationale —
// keeping the two workflows in lock-step for reviewers.
func TestOCIDeletePoolWorkflow_RunArgsValidation(t *testing.T) {
	validParams := &common.DeletePoolParams{}
	validPool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "u"}, Name: "p"}

	cases := []struct {
		name             string
		args             []interface{}
		wantOriginalSubs string
		wantTrackingID   int
	}{
		{
			name:             "ZeroArgs",
			args:             nil,
			wantOriginalSubs: "expected 2 args, got 0",
		},
		{
			name:             "OneArg",
			args:             []interface{}{validParams},
			wantOriginalSubs: "expected 2 args, got 1",
		},
		{
			name:             "Args0WrongType",
			args:             []interface{}{"not-params", validPool},
			wantOriginalSubs: "args[0] has unexpected type string",
		},
		{
			name:             "Args0TypedNil",
			args:             []interface{}{(*common.DeletePoolParams)(nil), validPool},
			wantOriginalSubs: "args[0] (*common.DeletePoolParams) must not be nil",
			wantTrackingID:   vsaerrors.ErrResourceEmptyError,
		},
		{
			name:             "Args1WrongType",
			args:             []interface{}{validParams, "not-pool"},
			wantOriginalSubs: "args[1] has unexpected type string",
		},
		{
			name:             "Args1TypedNil",
			args:             []interface{}{validParams, (*datamodel.Pool)(nil)},
			wantOriginalSubs: "args[1] (*datamodel.Pool) must not be nil",
			wantTrackingID:   vsaerrors.ErrResourceEmptyError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			wf := &ociDeletePoolWorkflow{}
			out, customErr := wf.Run(nil, tc.args...)

			assert.Nil(tt, out, "validation failure should not return a payload")
			require.NotNil(tt, customErr, "expected a *vsaerrors.CustomError, got nil")
			require.NotNil(tt, customErr.OriginalErr, "OriginalErr should preserve the descriptive validation message")
			assert.Contains(tt, customErr.OriginalErr.Error(), tc.wantOriginalSubs)
			if tc.wantTrackingID != 0 {
				assert.Equal(tt, tc.wantTrackingID, customErr.TrackingID,
					"typed-nil cases should be classified as ErrResourceEmptyError so they aren't lumped with generic internal errors")
			}
		})
	}
}

func withOCIVSASerialAllocationFlag(t *testing.T, enabled bool) {
	t.Helper()
	orig := ociVSASerialAllocationEnabled
	ociVSASerialAllocationEnabled = enabled
	t.Cleanup(func() { ociVSASerialAllocationEnabled = orig })
}

func withOCISerialAllocationEnv(t *testing.T, region, cell string) {
	t.Helper()
	origRegion := activities.RegionNumber
	origCell := ociCellNumber
	activities.RegionNumber = region
	ociCellNumber = cell
	t.Cleanup(func() {
		activities.RegionNumber = origRegion
		ociCellNumber = origCell
	})
}

func TestValidateNumericCode(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		want    int
		wantErr bool
		errSubs string
	}{
		{name: "exact length numeric", code: "42", want: 2, wantErr: false},
		{name: "too short", code: "4", want: 2, wantErr: true, errSubs: "expected 2 digits, got 1"},
		{name: "too long", code: "423", want: 2, wantErr: true, errSubs: "expected 2 digits, got 3"},
		{name: "empty", code: "", want: 2, wantErr: true, errSubs: "expected 2 digits, got 0"},
		{name: "non-digit", code: "4a", want: 2, wantErr: true, errSubs: "must contain only digits"},
		{name: "leading zero ok", code: "01", want: 2, wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			err := validateNumericCode("test", tc.code, tc.want)
			if !tc.wantErr {
				assert.NoError(tt, err)
				return
			}
			require.Error(tt, err)
			assert.True(tt, utilserrors.IsUserInputValidationErr(err),
				"length/charset failures must surface as 4xx UserInputValidationErr, not internal errors")
			assert.Contains(tt, err.Error(), tc.errSubs)
		})
	}
}

func TestBuildOCISerialPrefix(t *testing.T) {
	cases := []struct {
		name       string
		regionCode string
		cellCode   string
		want       string
		wantErr    bool
		errSubs    string
	}{
		{name: "valid", regionCode: "34", cellCode: "01", want: "9553401"},
		{name: "leading zero region", regionCode: "01", cellCode: "02", want: "9550102"},
		{name: "region too short", regionCode: "3", cellCode: "01", wantErr: true, errSubs: "region"},
		{name: "cell too long", regionCode: "34", cellCode: "001", wantErr: true, errSubs: "cell"},
		{name: "region non-digit", regionCode: "3a", cellCode: "01", wantErr: true, errSubs: "region"},
		{name: "cell empty", regionCode: "34", cellCode: "", wantErr: true, errSubs: "cell"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			got, err := buildOCISerialPrefix(tc.regionCode, tc.cellCode)
			if !tc.wantErr {
				require.NoError(tt, err)
				assert.Equal(tt, tc.want, got)
				assert.Len(tt, got, ociSerialPrefixLen,
					"prefix must be exactly PPP+RR+CC = 7 digits; downstream counter formatting depends on this width")
				return
			}
			require.Error(tt, err)
			assert.True(tt, utilserrors.IsUserInputValidationErr(err))
			assert.Contains(tt, err.Error(), tc.errSubs)
		})
	}
}

func TestAllocateOCIVMSerialNumbers_EarlyValidationErrors(t *testing.T) {
	cases := []struct {
		name      string
		region    string
		cell      string
		numHAPair int
		errSubs   string
	}{
		{name: "missing region", region: "", cell: "01", numHAPair: 1, errSubs: "region number is not set"},
		{name: "missing cell", region: "34", cell: "", numHAPair: 1, errSubs: "cell number is not set"},
		{name: "zero ha pairs", region: "34", cell: "01", numHAPair: 0, errSubs: "invalid VM count for serial allocation"},
		{name: "negative ha pairs", region: "34", cell: "01", numHAPair: -1, errSubs: "invalid VM count for serial allocation"},
		{name: "non-numeric region", region: "3a", cell: "01", numHAPair: 1, errSubs: "region"},
		{name: "wrong-length cell", region: "34", cell: "001", numHAPair: 1, errSubs: "cell"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			withOCISerialAllocationEnv(tt, tc.region, tc.cell)
			req := &vlm.CreateVSAClusterDeploymentRequest{
				VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{NumHAPair: tc.numHAPair},
				},
			}

			err := allocateOCIVMSerialNumbers(nil, nil, req)

			require.Error(tt, err)
			assert.True(tt, utilserrors.IsUserInputValidationErr(err),
				"missing-config and bad-arity failures must surface as 4xx UserInputValidationErr so callers see a configuration problem, not an internal error")
			assert.Contains(tt, err.Error(), tc.errSubs)
			assert.Empty(tt, req.VLMConfig.Deployment.VMSerialNumbers,
				"failed allocation must not partially populate VMSerialNumbers")
		})
	}
}

func TestOCICreatePoolWorkflow_SerialAllocation_FlagOffSkipsAllocation(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)
	withOCISerialAllocationEnv(t, "34", "01")

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	getNextSerialNumberCalled := false
	env.OnActivity("GetNextSerialNumber", mock.Anything).
		Run(func(args mock.Arguments) { getNextSerialNumberCalled = true }).
		Return(int64(0), nil).
		Maybe()

	var capturedDeployment vlm.DeploymentConfig
	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			req, ok := args.Get(1).(*vlm.CreateVSAClusterDeploymentRequest)
			if ok && req != nil {
				capturedDeployment = req.VLMConfig.Deployment
			}
		}).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Node)(nil), nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	assert.False(t, getNextSerialNumberCalled,
		"GetNextSerialNumber must NOT be invoked when the allocation flag is off; "+
			"this is the load-bearing assertion for the rollout gate — if it fires, the workflow has bypassed the feature flag")

	assert.Equal(t, ociSerialNumberLeadingPrefix+ociSerialNumberPrefix, capturedDeployment.SerialNumberPrefix,
		"with the gate off, SerialNumberPrefix must remain the hardcoded \"955\"+15 zeros so VLM can generate VM serials from the prefix")
	assert.Empty(t, capturedDeployment.VMSerialNumbers,
		"VMSerialNumbers must be empty when allocation is gated off; VLM honors VMSerialNumbers only when SerialNumberPrefix is empty")
}

func TestOCICreatePoolWorkflow_SerialAllocation_FlagOnAllocatesSerials(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, true)
	withOCISerialAllocationEnv(t, "34", "01")

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	const haPairs = 2
	wantNumVMs := haPairs * activities.VMsPerHAPair

	counters := []int64{1, 2, 3, 4}
	require.Equal(t, wantNumVMs, len(counters), "test must enumerate one counter per allocated VM")

	var counterIdx int
	env.OnActivity("GetNextSerialNumber", mock.Anything).
		Return(func(_ context.Context) (int64, error) {
			n := counters[counterIdx]
			counterIdx++
			return n, nil
		}).
		Times(wantNumVMs)

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     haPairs,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	var capturedDeployment vlm.DeploymentConfig
	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			req, ok := args.Get(1).(*vlm.CreateVSAClusterDeploymentRequest)
			if ok && req != nil {
				capturedDeployment = req.VLMConfig.Deployment
			}
		}).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Node)(nil), nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	assert.Equal(t, wantNumVMs, counterIdx,
		"GetNextSerialNumber must be invoked exactly NumHAPair*VMsPerHAPair times when the flag is on; "+
			"a different count means the per-VM loop changed without updating this test")

	assert.Empty(t, capturedDeployment.SerialNumberPrefix,
		"with the gate on, SerialNumberPrefix must be cleared so VLM honors VMSerialNumbers instead")
	wantSerials := []string{
		"9553401" + "0000000000001",
		"9553401" + "0000000000002",
		"9553401" + "0000000000003",
		"9553401" + "0000000000004",
	}
	assert.Equal(t, wantSerials, capturedDeployment.VMSerialNumbers,
		"VMSerialNumbers must be PPP(955)+RR(34)+CC(01)+13-digit zero-padded counter for each VM, in allocation order")
}

func TestOCICreatePoolWorkflow_SerialAllocation_FlagOnMissingRegionRollsBack(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, true)
	withOCISerialAllocationEnv(t, "", "01")

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:            "test-pool",
		DeploymentName:  "dep-serial-rollback",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	erroredPoolCalled := false
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { erroredPoolCalled = true }).
		Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError(),
		"allocation failure must surface as a workflow error so the caller doesn't observe a half-created pool")
	assert.True(t, erroredPoolCalled,
		"rollback must run on allocation failure to mark the pool as errored and undo the ONTAP credential side effect")
	mockVlmWorkflowClient.AssertNotCalled(t, "CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything)
}

func TestOCICreatePoolWorkflow_SerialAllocation_ActivityErrorRollsBack(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, true)
	withOCISerialAllocationEnv(t, "34", "01")

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	env.OnActivity("GetNextSerialNumber", mock.Anything).
		Return(int64(0), errors.New("simulated GetNextSerialNumber DB failure"))

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:            "test-pool",
		DeploymentName:  "dep-serial-activity-err",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	erroredPoolCalled := false
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { erroredPoolCalled = true }).
		Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError(),
		"GetNextSerialNumber failure must surface as a workflow error so the pool is marked errored")
	assert.True(t, erroredPoolCalled,
		"rollback must run when the per-VM serial allocator fails — otherwise we'd ask VLM to deploy with no/partial serials")
	mockVlmWorkflowClient.AssertNotCalled(t, "CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything)
}

func TestOCICreatePoolWorkflow_SerialAllocation_CounterOverflowRollsBack(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, true)
	withOCISerialAllocationEnv(t, "34", "01")

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	env.OnActivity("GetNextSerialNumber", mock.Anything).
		Return(ociSerialCounterMax, nil)

	params := &common.CreatePoolParams{
		Name:        "test-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
		Name:            "test-pool",
		DeploymentName:  "dep-serial-overflow",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{Password: "test-pool-password"},
	}

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	erroredPoolCalled := false
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { erroredPoolCalled = true }).
		Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError(),
		"counter overflow must surface as a workflow error; silently emitting a 21-digit serial would break the VLM contract")
	assert.True(t, erroredPoolCalled,
		"rollback must run on counter overflow so the pool isn't left half-created")
	mockVlmWorkflowClient.AssertNotCalled(t, "CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything)
}

// vlmConfigForVMRSUpdate builds the minimal VLMConfig that
func vlmConfigForVMRSUpdate(numHAPairs int) vlm.VLMConfig {
	pairs := make([]vlm.HAPair, numHAPairs)
	return vlm.VLMConfig{
		Cloud: vlm.CloudConfig{HAPairs: pairs},
	}
}

// TestComputeOCIVMRSInputForUpdate_HappyPath_AA pins the Active-Active
// topology math for the update flow against the same fixture as
// TestComputeOCIVMRSInput_HappyPath: 4 TB total / (2 HA pairs * 2 active
// VMs/pair) → 1.0 TB/VM, and 1024 MiB/s total / 4 active VMs →
// 0.268435456 GB/s/VM. The matching numbers across create and update
// guard the catalogue lookup against asymmetric drift if one of the two
// formulas is refactored.
func TestComputeOCIVMRSInputForUpdate_HappyPath_AA(t *testing.T) {
	origDDC := dataDiskCount
	dataDiskCount = 2
	t.Cleanup(func() { dataDiskCount = origDDC })

	params := &common.UpdatePoolParams{
		SizeInBytes:          4 * 1_000_000_000_000,
		TotalThroughputMibps: 1024,
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		PoolAttributes: &datamodel.PoolAttributes{IsRegionalHA: false},
	}
	cfg := vlmConfigForVMRSUpdate(2)

	perVMCap, perVMThru, err := computeOCIVMRSInputForUpdate(params, pool, cfg)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, perVMCap, 1e-9,
		"AA per-VM capacity = totalCapacityTB / (2 * numHAPairs); must match the CREATE-flow happy path so VMRS lookups are symmetric")
	assert.InDelta(t, 0.268435456, perVMThru, 1e-9,
		"AA per-VM throughput = totalThroughputGBs / (2 * numHAPairs); must match the CREATE-flow happy path")
}

func TestComputeOCIVMRSInputForUpdate_ScaleOutUsesTargetHAPairCount(t *testing.T) {
	origDDC := dataDiskCount
	dataDiskCount = 2
	t.Cleanup(func() { dataDiskCount = origDDC })

	params := &common.UpdatePoolParams{
		SizeInBytes:          4 * 1_000_000_000_000,
		TotalThroughputMibps: 1024,
		HAPairs:              4,
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		PoolAttributes: &datamodel.PoolAttributes{IsRegionalHA: false},
	}
	cfg := vlmConfigForVMRSUpdate(2)

	perVMCap, perVMThru, err := computeOCIVMRSInputForUpdate(params, pool, cfg)
	require.NoError(t, err)
	assert.InDelta(t, 0.5, perVMCap, 1e-9,
		"target HAPairs=4 must drive per-VM capacity = totalCapacityTB / (2 * 4), half the 2-pair value")
	assert.InDelta(t, 0.134217728, perVMThru, 1e-9,
		"target HAPairs=4 must drive per-VM throughput = totalThroughputGBs / (2 * 4)")
}

func TestComputeOCIVMRSInputForUpdate_HAPairsNotGreaterThanCurrentIgnored(t *testing.T) {
	origDDC := dataDiskCount
	dataDiskCount = 2
	t.Cleanup(func() { dataDiskCount = origDDC })

	params := &common.UpdatePoolParams{
		SizeInBytes:          4 * 1_000_000_000_000,
		TotalThroughputMibps: 1024,
		HAPairs:              2,
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		PoolAttributes: &datamodel.PoolAttributes{IsRegionalHA: false},
	}
	cfg := vlmConfigForVMRSUpdate(2)

	perVMCap, perVMThru, err := computeOCIVMRSInputForUpdate(params, pool, cfg)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, perVMCap, 1e-9,
		"HAPairs equal to current count must not change sizing (stays at 2 pairs)")
	assert.InDelta(t, 0.268435456, perVMThru, 1e-9,
		"HAPairs equal to current count must not change throughput slicing")
}

// TestComputeOCIVMRSInputForUpdate_HappyPath_AP anchors the
// Active-Passive slicing path: only the primary VM in each pair serves
// I/O, so totalActiveVMs = numHAPairs (not 2 * numHAPairs). Per-VM
// capacity and per-VM throughput are therefore both exactly 2x the AA
// values for the same total — encoding the AA/AP halving rule end-to-end.
// Regression guard for the historical bug where the per-VM capacity
// divisor was numHAPairs*dataDiskCount (correct only in AA where
// dataDiskCount happens to equal activeVMsPerPair=2; off by 2x in AP).
func TestComputeOCIVMRSInputForUpdate_HappyPath_AP(t *testing.T) {
	origDDC := dataDiskCount
	dataDiskCount = 2
	t.Cleanup(func() { dataDiskCount = origDDC })

	params := &common.UpdatePoolParams{
		SizeInBytes:          4 * 1_000_000_000_000,
		TotalThroughputMibps: 1024,
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		PoolAttributes: &datamodel.PoolAttributes{IsRegionalHA: true},
	}
	cfg := vlmConfigForVMRSUpdate(2)

	perVMCap, perVMThru, err := computeOCIVMRSInputForUpdate(params, pool, cfg)
	require.NoError(t, err)
	assert.InDelta(t, 2.0, perVMCap, 1e-9,
		"AP per-VM capacity = totalCapacityTB / numHAPairs (exactly 2x AA for the same total)")
	assert.InDelta(t, 0.536870912, perVMThru, 1e-9,
		"AP per-VM throughput = totalThroughputGBs / numHAPairs (exactly 2x AA for the same total)")
}

// TestComputeOCIVMRSInputForUpdate_FallsBackToPoolFields covers the
// "leave size/throughput unchanged" wire convention: callers can
// resize without re-sending size or throughput, and VMRS must still
// select against a defined target by falling back to the persisted
// pool row. Mirrors deriveUpdateTargetSPConfig's same-named fallbacks
// so the two helpers always see the same total before slicing.
func TestComputeOCIVMRSInputForUpdate_FallsBackToPoolFields(t *testing.T) {
	origDDC := dataDiskCount
	dataDiskCount = 2
	t.Cleanup(func() { dataDiskCount = origDDC })

	params := &common.UpdatePoolParams{
		SizeInBytes:          0,
		TotalThroughputMibps: 0,
	}
	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{UUID: "u1"},
		SizeInBytes: 4 * 1_000_000_000_000,
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 1024,
			IsRegionalHA:    false,
		},
	}
	cfg := vlmConfigForVMRSUpdate(2)

	perVMCap, perVMThru, err := computeOCIVMRSInputForUpdate(params, pool, cfg)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, perVMCap, 1e-9,
		"per-VM capacity must use pool.SizeInBytes when params.SizeInBytes is 0 (resize-without-resize wire convention)")
	assert.InDelta(t, 0.268435456, perVMThru, 1e-9,
		"per-VM throughput must use pool.PoolAttributes.ThroughputMibps when params.TotalThroughputMibps is 0")
}

func TestComputeOCIVMRSInputForUpdate_CapacityChangeUsesExistingThroughput(t *testing.T) {
	origDDC := dataDiskCount
	dataDiskCount = 2
	t.Cleanup(func() { dataDiskCount = origDDC })

	params := &common.UpdatePoolParams{
		SizeInBytes:          8 * 1_000_000_000_000,
		TotalThroughputMibps: 0,
	}
	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{UUID: "u1"},
		SizeInBytes: 4 * 1_000_000_000_000,
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 1024,
			IsRegionalHA:    false,
		},
	}
	cfg := vlmConfigForVMRSUpdate(2)

	perVMCap, perVMThru, err := computeOCIVMRSInputForUpdate(params, pool, cfg)
	require.NoError(t, err)
	assert.InDelta(t, 2.0, perVMCap, 1e-9,
		"per-VM capacity must use the requested size when capacity is changing")
	assert.InDelta(t, 0.268435456, perVMThru, 1e-9,
		"per-VM throughput must fall back to pool throughput when not provided in the update request")
}

// TestComputeOCIVMRSInputForUpdate_RejectsInvalidInputs anchors every
// validator branch fired by validateOCIVMRSInputForUpdate. Each
// sub-test makes exactly ONE field invalid so the failing branch is
// unambiguous and the surfaced error message stays actionable to API
// callers.
// Note: dataDiskCount validation is tested in TestValidateOCIWorkerStartupEnv
// since it's checked at startup rather than during request processing.
func TestComputeOCIVMRSInputForUpdate_RejectsInvalidInputs(t *testing.T) {
	origDDC := dataDiskCount
	t.Cleanup(func() { dataDiskCount = origDDC })

	cases := []struct {
		name           string
		setupDDC       int
		params         *common.UpdatePoolParams
		pool           *datamodel.Pool
		cfg            vlm.VLMConfig
		wantMsgInclude string
		wantUserInput  bool // true → UserInputValidationErr (4xx)
	}{
		{
			name:     "ZeroHAPairs",
			setupDDC: 2,
			params: &common.UpdatePoolParams{
				SizeInBytes:          4 * 1_000_000_000_000,
				TotalThroughputMibps: 1024,
			},
			pool:           &datamodel.Pool{PoolAttributes: &datamodel.PoolAttributes{}},
			cfg:            vlmConfigForVMRSUpdate(0),
			wantMsgInclude: "stored VLM config has 0 HA pairs",
			wantUserInput:  true,
		},
		{
			name:     "ZeroSize_NoPoolFallback",
			setupDDC: 2,
			params: &common.UpdatePoolParams{
				SizeInBytes:          0,
				TotalThroughputMibps: 1024,
			},
			pool:           &datamodel.Pool{SizeInBytes: 0, PoolAttributes: &datamodel.PoolAttributes{}},
			cfg:            vlmConfigForVMRSUpdate(2),
			wantMsgInclude: "size is required for OCI VMRS",
			wantUserInput:  true,
		},
		{
			name:     "ZeroThroughput_NoPoolFallback",
			setupDDC: 2,
			params: &common.UpdatePoolParams{
				SizeInBytes:          4 * 1_000_000_000_000,
				TotalThroughputMibps: 0,
			},
			pool:           &datamodel.Pool{PoolAttributes: nil},
			cfg:            vlmConfigForVMRSUpdate(2),
			wantMsgInclude: "throughput is required for OCI VMRS",
			wantUserInput:  true,
		},
		{
			name:     "NilPoolAttributes",
			setupDDC: 2,
			params: &common.UpdatePoolParams{
				SizeInBytes:          4 * 1_000_000_000_000,
				TotalThroughputMibps: 1024,
			},
			pool:           &datamodel.Pool{PoolAttributes: nil},
			cfg:            vlmConfigForVMRSUpdate(2),
			wantMsgInclude: "pool.PoolAttributes is required",
			wantUserInput:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			dataDiskCount = tc.setupDDC
			perVMCap, perVMThru, err := computeOCIVMRSInputForUpdate(tc.params, tc.pool, tc.cfg)

			require.Error(tt, err)
			assert.Zero(tt, perVMCap)
			assert.Zero(tt, perVMThru)
			assert.True(tt, utilserrors.IsUserInputValidationErr(err),
				"caller-supplied field errors must surface as UserInputValidationErr (4xx)")
			assert.Contains(tt, err.Error(), tc.wantMsgInclude)
		})
	}
}

// TestPrepareOCIUpdateVSAClusterDeploymentRequest_NilDecisionLeavesOverridesUnset
// is the regression guard for the "VMRS off → leave VLM-stored values
// alone" contract: when decision is nil (OCI_VMRS_ENABLED=false), the
// four VMRS override fields on UpdateVSAClusterDeploymentRequest MUST
// stay at their zero values so VLM keeps the stored instance shape /
// flex / VPU and the JSON `omitempty` tag drops them off the wire.
func TestPrepareOCIUpdateVSAClusterDeploymentRequest_NilDecisionLeavesOverridesUnset(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			VSAInstanceType: "VM.DenseIO.E5.Flex",
			ProviderConfig:  vlm.ProviderConfigWrapper{ProviderConfig: vlm.OCIConfig{}},
		},
	}
	target := vlm.SPConfig{Size: "100Gi", IOps: 5000, Throughput: 128}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 2, creds, nil, &common.UpdatePoolParams{}))

	// VLMConfig is passed through near-pristine; only
	// Deployment.SPConfig.HAPairConfigs is stripped on the wire (see fn
	// doc). Compare every other field; assert HAPairConfigs is nil
	// regardless of whatever current had so VLM's per-pair AggrConfigs
	// count check doesn't trip on a non-empty stored blob.
	wantVLMConfig := current
	wantVLMConfig.Deployment.SPConfig.HAPairConfigs = nil
	assert.Equal(t, wantVLMConfig, req.VLMConfig,
		"VLMConfig must be passed through pristine except SPConfig.HAPairConfigs which must be stripped")
	assert.Nil(t, req.VLMConfig.Deployment.SPConfig.HAPairConfigs,
		"req.VLMConfig.Deployment.SPConfig.HAPairConfigs must be nil so VLM's count check matches req.SPConfig.HAPairConfigs (also nil)")
	assert.Equal(t, 2, req.NumHAPair)
	assert.Equal(t, target, req.SPConfig)
	assert.Equal(t, creds, req.OntapCredentials)
	assert.Empty(t, req.BucketName, "OCI must not ship a bucket")
	assert.Equal(t, int64(-1), req.AutoTierThreshold,
		"AutoTierThreshold sentinel must be -1 for OCI (no bucket/auto-tier path)")
	assert.Empty(t, req.NewInstanceType,
		"nil decision MUST NOT override NewInstanceType — VLM keeps the stored shape")
	assert.Nil(t, req.VSAFlexOcpus,
		"nil decision MUST leave VSAFlexOcpus unset so omitempty drops it from the wire")
	assert.Nil(t, req.VSAFlexMemoryInGBs,
		"nil decision MUST leave VSAFlexMemoryInGBs unset so omitempty drops it from the wire")
	assert.Nil(t, req.DataDiskVpus,
		"nil decision MUST leave DataDiskVpus unset so omitempty drops it from the wire")
}

// TestPrepareOCIUpdateVSAClusterDeploymentRequest_DecisionPopulatesAllFourFields
// is the regression guard for the VMRS-on update path: the four VMRS
// decision fields (shape, ocpus, memory, vpu) MUST all flow onto the
// request when decision != nil. Anchors the create/update symmetry —
// the create-side counterpart
// TestPrepareVLMConfig_AppliesDecisionFlipsAllFourOCIFields covers the
// same four fields landing on OCIConfig.
func TestPrepareOCIUpdateVSAClusterDeploymentRequest_DecisionPopulatesAllFourFields(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			VSAInstanceType: "VM.DenseIO.E5.Flex",
			ProviderConfig:  vlm.ProviderConfigWrapper{ProviderConfig: vlm.OCIConfig{}},
		},
	}
	target := vlm.SPConfig{Size: "100Gi", IOps: 5000, Throughput: 128}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}
	// Synthetic values that intentionally differ from the env defaults
	// so a regression flipping a single field back to the no-op path
	// can't pass this test.
	decision := &vmrs_oci.Decision{
		VMShape:   "VM.DenseIO.Custom.Flex",
		OCPUs:     40,
		MemoryGBs: 480,
		VPU:       90,
		IOPS:      786600,
	}

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 2, creds, decision, &common.UpdatePoolParams{}))

	assert.Equal(t, "VM.DenseIO.Custom.Flex", req.NewInstanceType,
		"VMRS shape must land on NewInstanceType so VLM swaps the running VM")
	require.NotNil(t, req.VSAFlexOcpus, "VSAFlexOcpus must be set when decision != nil")
	assert.Equal(t, float32(40), *req.VSAFlexOcpus,
		"VMRS OCPUs must land on VSAFlexOcpus verbatim (no per-tier multiplier)")
	require.NotNil(t, req.VSAFlexMemoryInGBs, "VSAFlexMemoryInGBs must be set when decision != nil")
	assert.Equal(t, float32(480), *req.VSAFlexMemoryInGBs,
		"VMRS memory must land on VSAFlexMemoryInGBs verbatim (no per-OCPU derivation)")
	require.NotNil(t, req.DataDiskVpus, "DataDiskVpus must be set when decision != nil")
	assert.Equal(t, int64(90), *req.DataDiskVpus,
		"VMRS VPU must land on DataDiskVpus")
	// VLMConfig is passed through near-pristine; only
	// Deployment.SPConfig.HAPairConfigs is stripped on the wire (see fn doc).
	wantVLMConfig := current
	wantVLMConfig.Deployment.SPConfig.HAPairConfigs = nil
	assert.Equal(t, wantVLMConfig, req.VLMConfig,
		"VLMConfig must still be passed through pristine except SPConfig.HAPairConfigs which must be stripped")
	// SPConfig is sourced from `target` (validator/throughput-derived
	// IOPS) BUT IOps is overridden by decision.IOPS when VMRS is
	// enabled — VMRS is the source of truth and `target.IOps` is just
	// the validator-derived fallback used when decision == nil. All
	// other SPConfig fields must still ride through unchanged.
	assert.Equal(t, target.Size, req.SPConfig.Size,
		"SPConfig.Size must pass through from target unchanged")
	assert.Equal(t, target.Throughput, req.SPConfig.Throughput,
		"SPConfig.Throughput must pass through from target unchanged")
	assert.Equal(t, target.IsHeterogeneous, req.SPConfig.IsHeterogeneous,
		"SPConfig.IsHeterogeneous must pass through from target unchanged")
	assert.Equal(t, target.HAPairConfigs, req.SPConfig.HAPairConfigs,
		"SPConfig.HAPairConfigs must pass through from target unchanged")
	assert.Equal(t, target.IOps, req.SPConfig.IOps)
}

func TestPrepareOCIUpdateVSAClusterDeploymentRequest_PreservesStoredHAPairConfigs(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	storedHAPairs := []vlm.SPHAPairConfig{
		{InstanceType: "VM.DenseIO.E5.Flex", AggrConfigs: []vlm.AggrConfig{{Size: "600Gi", IOps: 15248, Throughput: 954, AggrName: "aggr1"}}},
	}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			VSAInstanceType: "VM.DenseIO.E5.Flex",
			ProviderConfig:  vlm.ProviderConfigWrapper{ProviderConfig: vlm.OCIConfig{}},
			SPConfig: vlm.SPConfig{
				Size:          "600Gi",
				IOps:          15248,
				Throughput:    954,
				HAPairConfigs: storedHAPairs,
			},
		},
	}
	target := vlm.SPConfig{Size: "1000Gi", IOps: 25000, Throughput: 2000}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 1, creds, nil, &common.UpdatePoolParams{}))

	assert.Equal(t, storedHAPairs, req.VLMConfig.Deployment.SPConfig.HAPairConfigs,
		"req.VLMConfig must carry the stored sp_ha_pair_config verbatim (current cluster state)")
	assert.Nil(t, req.SPConfig.HAPairConfigs,
		"req.SPConfig.HAPairConfigs stays nil for a homogeneous update; the update rides on Size/IOps/Throughput")

	assert.Equal(t, "600Gi", req.VLMConfig.Deployment.SPConfig.Size)
	assert.Equal(t, int64(15248), req.VLMConfig.Deployment.SPConfig.IOps)
	assert.Equal(t, int64(954), req.VLMConfig.Deployment.SPConfig.Throughput)

	assert.Equal(t, "1000Gi", req.SPConfig.Size)
	assert.Equal(t, int64(25000), req.SPConfig.IOps)
	assert.Equal(t, int64(2000), req.SPConfig.Throughput)

	assert.Equal(t, storedHAPairs, current.Deployment.SPConfig.HAPairConfigs,
		"caller's stored config must remain unchanged")
}

func TestPrepareOCIUpdateVSAClusterDeploymentRequest_HeterogeneousTargetRidesOnSPConfig(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	storedHAPairs := []vlm.SPHAPairConfig{
		{InstanceType: "VM.DenseIO.E5.Flex", AggrConfigs: []vlm.AggrConfig{{Size: "600Gi"}}},
	}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			ProviderConfig: vlm.ProviderConfigWrapper{ProviderConfig: vlm.OCIConfig{}},
			SPConfig: vlm.SPConfig{
				Size:          "600Gi",
				HAPairConfigs: storedHAPairs,
			},
		},
	}
	targetHAPairs := []vlm.SPHAPairConfig{
		{InstanceType: "VM.DenseIO.E5.Flex", AggrConfigs: []vlm.AggrConfig{{Size: "4096Gi"}}},
		{InstanceType: "VM.DenseIO.E5.Flex", AggrConfigs: []vlm.AggrConfig{{Size: "8192Gi"}}},
	}
	target := vlm.SPConfig{
		Size:            "12288Gi",
		IsHeterogeneous: true,
		HAPairConfigs:   targetHAPairs,
	}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 2, creds, nil, &common.UpdatePoolParams{}))

	assert.True(t, req.SPConfig.IsHeterogeneous)
	assert.Equal(t, targetHAPairs, req.SPConfig.HAPairConfigs,
		"heterogeneous target HAPairConfigs ride on req.SPConfig")
	assert.Equal(t, storedHAPairs, req.VLMConfig.Deployment.SPConfig.HAPairConfigs,
		"req.VLMConfig keeps the stored current state verbatim")
	assert.Equal(t, storedHAPairs, current.Deployment.SPConfig.HAPairConfigs,
		"caller stored config must remain unchanged")
}

func TestPrepareOCIUpdateVSAClusterDeploymentRequest_PropagatesCmekOcidAndOciConfig(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	savedOCIConfig := vlm.OCIConfig{
		CompartmentID:   "ocid1.compartment.oc1..saved",
		SubnetID:        "ocid1.subnet.oc1.iad.saved",
		DataNICSubnetID: "ocid1.subnet.oc1.iad.saved-data",
		CmekOcid:        "ocid1.key.oc1.iad.stored-cmek",
	}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			VSAInstanceType: "VM.DenseIO.E5.Flex",
			ProviderConfig:  vlm.ProviderConfigWrapper{ProviderConfig: savedOCIConfig},
		},
	}
	target := vlm.SPConfig{Size: "100Gi", IOps: 5000, Throughput: 128}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}
	const newCmek = "ocid1.key.oc1.iad.new-cmek"

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 2, creds, nil, &common.UpdatePoolParams{KmsKeyId: newCmek}))

	assert.Equal(t, newCmek, req.CmekOcid,
		"kmsKeyId (UpdatePoolParams.KmsKeyId) must land on req.CmekOcid — the field VLM consumes to update the CMEK key")
	gotOCI, ociErr := req.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	assert.Equal(t, savedOCIConfig, gotOCI,
		"the saved OCI config from the stored VLM config must be carried verbatim on req.ProviderConfig as reference data")
}

func TestPrepareOCIUpdateVSAClusterDeploymentRequest_EmptyKmsKeyIdYieldsEmptyCmekOcid(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	savedOCIConfig := vlm.OCIConfig{CompartmentID: "ocid1.compartment.oc1..saved"}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{ProviderConfig: vlm.ProviderConfigWrapper{ProviderConfig: savedOCIConfig}},
	}
	target := vlm.SPConfig{Size: "100Gi"}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 1, creds, nil, &common.UpdatePoolParams{}))

	assert.Empty(t, req.CmekOcid,
		"empty kmsKeyId must leave req.CmekOcid empty; the workflow does not silently inject a default key")
	gotOCI, ociErr := req.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	assert.Equal(t, savedOCIConfig, gotOCI,
		"the saved OCI config must still be carried on req.ProviderConfig even when no CMEK key is supplied")
}

func TestPrepareOCIUpdateVSAClusterDeploymentRequest_PropagatesNsgIdsOntoOciConfig(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			ProviderConfig: vlm.ProviderConfigWrapper{ProviderConfig: vlm.OCIConfig{
				CustomerNSGs: []string{"ocid1.networksecuritygroup.oc1.iad.stored"},
			}},
		},
	}
	target := vlm.SPConfig{Size: "100Gi"}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}
	newNsgs := []string{
		"ocid1.networksecuritygroup.oc1.iad.nsg-A",
		"ocid1.networksecuritygroup.oc1.iad.nsg-B",
	}

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 1, creds, nil, &common.UpdatePoolParams{NsgIds: newNsgs}))

	gotOCI, ociErr := req.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	assert.Equal(t, newNsgs, gotOCI.CustomerNSGs,
		"UpdatePoolParams.NsgIds must overwrite ProviderConfig CustomerNSGs (order and values) so VLM applies the requested NSGs")
}

func TestPrepareOCIUpdateVSAClusterDeploymentRequest_NilNsgIdsPreservesStoredOciConfigNSGs(t *testing.T) {
	stored := []string{"ocid1.networksecuritygroup.oc1.iad.stored"}
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			ProviderConfig: vlm.ProviderConfigWrapper{ProviderConfig: vlm.OCIConfig{
				CustomerNSGs: stored,
			}},
		},
	}
	target := vlm.SPConfig{Size: "100Gi"}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 1, creds, nil, &common.UpdatePoolParams{}))

	gotOCI, ociErr := req.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	assert.Equal(t, stored, gotOCI.CustomerNSGs,
		"nil NsgIds on params must leave stored CustomerNSGs unchanged for VLM")
}

func TestPrepareOCIUpdateVSAClusterDeploymentRequest_EmptyNsgIdsClearsOciConfigNSGs(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			ProviderConfig: vlm.ProviderConfigWrapper{ProviderConfig: vlm.OCIConfig{
				CustomerNSGs: []string{"ocid1.networksecuritygroup.oc1.iad.stored"},
			}},
		},
	}
	target := vlm.SPConfig{Size: "100Gi"}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 1, creds, nil, &common.UpdatePoolParams{NsgIds: []string{}}))

	gotOCI, ociErr := req.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	assert.Empty(t, gotOCI.CustomerNSGs,
		"explicit empty NsgIds must clear CustomerNSGs on the VLM request")
}

func TestPrepareOCIUpdateVSAClusterDeploymentRequest_PropagatesSecurityAttributesOntoOciConfig(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{ProviderConfig: vlm.ProviderConfigWrapper{ProviderConfig: vlm.OCIConfig{}}},
	}
	target := vlm.SPConfig{Size: "100Gi"}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}
	attrs := map[string]map[string]interface{}{
		"ns1": {
			"app": map[string]string{"value": "app1", "mode": "enforce"},
		},
	}

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 1, creds, nil, &common.UpdatePoolParams{SecurityAttributes: attrs}))

	gotOCI, ociErr := req.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	assert.Equal(t, attrs, gotOCI.CustomerSecurityAttributes,
		"UpdatePoolParams.SecurityAttributes must land on ProviderConfig CustomerSecurityAttributes verbatim; the workflow is a pass-through to VLM")
}

func TestPrepareOCIUpdateVSAClusterDeploymentRequest_NilSecurityAttributesPreservesStored(t *testing.T) {
	stored := map[string]map[string]interface{}{
		"ns1": {"app": map[string]string{"value": "stored", "mode": "enforce"}},
	}
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			ProviderConfig: vlm.ProviderConfigWrapper{ProviderConfig: vlm.OCIConfig{CustomerSecurityAttributes: stored}},
		},
	}
	target := vlm.SPConfig{Size: "100Gi"}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 1, creds, nil, &common.UpdatePoolParams{}))

	gotOCI, ociErr := req.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	assert.Equal(t, stored, gotOCI.CustomerSecurityAttributes,
		"nil SecurityAttributes on params must leave stored CustomerSecurityAttributes unchanged for VLM")
}

func TestPrepareOCIUpdateVSAClusterDeploymentRequest_EmptySecurityAttributesClearsStored(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	current := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			ProviderConfig: vlm.ProviderConfigWrapper{ProviderConfig: vlm.OCIConfig{
				CustomerSecurityAttributes: map[string]map[string]interface{}{
					"ns1": {"app": map[string]string{"value": "stored", "mode": "enforce"}},
				},
			}},
		},
	}
	target := vlm.SPConfig{Size: "100Gi"}
	creds := vlm.OntapCredentials{AdminPassword: "pw"}

	require.NoError(t, prepareOCIUpdateVSAClusterDeploymentRequest(req, current, target, 1, creds, nil, &common.UpdatePoolParams{
		SecurityAttributes: map[string]map[string]interface{}{},
	}))

	gotOCI, ociErr := req.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	assert.Empty(t, gotOCI.CustomerSecurityAttributes,
		"explicit empty SecurityAttributes must clear CustomerSecurityAttributes on the VLM request")
}

func validPrepareVLMConfigInputs() (*common.CreatePoolParams, *datamodel.Pool) {
	params := &common.CreatePoolParams{
		AccountName:     "acct",
		SizeInBytes:     300 * 1024 * 1024 * 1024,
		PrimaryZone:     "ad1",
		SecondaryZone:   "ad2",
		MediatorZone:    "ad3",
		VendorSubNetID:  "subnet",
		CompartmentOCID: "comp",
		HAPairs:         1,
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 128,
		},
	}
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "u1"},
		DeploymentName: "dep1",
		Name:           "pool1",
		Account:        &datamodel.Account{Name: "acct"},
	}
	return params, pool
}

func TestPrepareVLMConfig_PropagatesKmsKeyIdAsCmekOcid(t *testing.T) {
	t.Run("non-empty KmsKeyId is propagated to vlm.OCIConfig.CmekOcid", func(tt *testing.T) {
		setTestOCIImageEnv(tt)
		const cmek = "ocid1.key.oc1.iad.test-cmek"
		params, pool := validPrepareVLMConfigInputs()
		params.KmsKeyId = cmek

		cfg, err := prepareVLMConfig(params, pool, nil)
		require.NoError(tt, err)
		require.NotNil(tt, cfg)

		ociCfg, ociErr := cfg.Deployment.ProviderConfig.AsOCI()
		require.NoError(tt, ociErr)
		assert.Equal(tt, cmek, ociCfg.CmekOcid,
			"CreatePoolParams.KmsKeyId (API-layer name) must round-trip into vlm.OCIConfig.CmekOcid (VLM-wire name) so VLM provisions BYOK-encrypted block volumes")
	})

	t.Run("empty KmsKeyId is propagated as empty (no defaulting)", func(tt *testing.T) {
		setTestOCIImageEnv(tt)
		params, pool := validPrepareVLMConfigInputs()
		// KmsKeyId left zero string

		cfg, err := prepareVLMConfig(params, pool, nil)
		require.NoError(tt, err)

		ociCfg, ociErr := cfg.Deployment.ProviderConfig.AsOCI()
		require.NoError(tt, ociErr)
		assert.Equal(tt, "", ociCfg.CmekOcid,
			"empty KmsKeyId must propagate as empty CmekOcid; the workflow does not silently inject a default key")
	})
}

func TestPrepareVLMConfig_PropagatesNsgIdsAsCustomerNSGs(t *testing.T) {
	setTestOCIImageEnv(t)
	nsgs := []string{
		"ocid1.networksecuritygroup.oc1.iad.nsg-A",
		"ocid1.networksecuritygroup.oc1.iad.nsg-B",
	}
	params, pool := validPrepareVLMConfigInputs()
	params.NsgIds = nsgs

	cfg, err := prepareVLMConfig(params, pool, nil)
	require.NoError(t, err)

	ociCfg, ociErr := cfg.Deployment.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	assert.Equal(t, nsgs, ociCfg.CustomerNSGs,
		"CreatePoolParams.NsgIds (API-layer name) must propagate verbatim (order and values) into vlm.OCIConfig.CustomerNSGs (VLM-wire name)")
}

func TestPrepareVLMConfig_PropagatesSecurityAttributesAsCustomerSecurityAttributes(t *testing.T) {
	setTestOCIImageEnv(t)
	attrs := map[string]map[string]interface{}{
		"ns1": {
			"app": map[string]string{"value": "app1", "mode": "enforce"},
		},
	}
	params, pool := validPrepareVLMConfigInputs()
	params.SecurityAttributes = attrs

	cfg, err := prepareVLMConfig(params, pool, nil)
	require.NoError(t, err)

	ociCfg, ociErr := cfg.Deployment.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	assert.Equal(t, attrs, ociCfg.CustomerSecurityAttributes,
		"CreatePoolParams.SecurityAttributes (API-layer name) must round-trip the wire-shape map verbatim into vlm.OCIConfig.CustomerSecurityAttributes (VLM-wire name); the workflow is a pass-through to VLM")
}

func TestPrepareVLMConfig_FabricPoolConfigSet_PropagatesAllFields(t *testing.T) {
	setTestOCIImageEnv(t)
	wantVersion := int64(4)
	fp := &common.FabricPoolConfig{
		BucketName:    "fp-bucket",
		SecretOcid:    "ocid1.vaultsecret.oc1.iad.fp-secret",
		SecretVersion: &wantVersion,
		Namespace:     "fp-ns",
		ServerURL:     "compat.objectstorage.us-ashburn-1.oraclecloud.com",
	}
	params, pool := validPrepareVLMConfigInputs()
	params.FabricPoolConfig = fp

	cfg, err := prepareVLMConfig(params, pool, nil)
	require.NoError(t, err)

	ociCfg, ociErr := cfg.Deployment.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	got := ociCfg.FabricPoolConfig
	assert.Equal(t, fp.BucketName, got.BucketName, "BucketName must round-trip")
	assert.Equal(t, fp.SecretOcid, got.SecretOcid, "SecretOcid must round-trip")
	require.NotNil(t, got.SecretVersion, "SecretVersion pointer must round-trip")
	assert.Equal(t, *fp.SecretVersion, *got.SecretVersion, "SecretVersion must round-trip")
	assert.Equal(t, fp.Namespace, got.Namespace, "Namespace must round-trip")
	assert.Equal(t, fp.ServerURL, got.ServerURL, "ServerURL must round-trip")
}

func TestPrepareVLMConfig_FabricPoolConfigNil_LeavesZeroValueFabricPool(t *testing.T) {
	setTestOCIImageEnv(t)
	params, pool := validPrepareVLMConfigInputs()
	// params.FabricPoolConfig left nil

	cfg, err := prepareVLMConfig(params, pool, nil)
	require.NoError(t, err)

	ociCfg, ociErr := cfg.Deployment.ProviderConfig.AsOCI()
	require.NoError(t, ociErr)
	got := ociCfg.FabricPoolConfig
	assert.Equal(t, vlm.FabricPoolConfig{}, got,
		"nil FabricPoolConfig on params must leave vlm.OCIConfig.FabricPoolConfig as zero value (tiering disabled); "+
			"the workflow guards the assignment with an explicit nil check")
}

// TestOCICreatePoolWorkflow_CertAuth_RegistersDNS verifies that for a
// USER_CERTIFICATE-auth pool, OCICreatePoolWorkflow wires the DNS-bring-up
// path end-to-end: OCICreateCloudDNSRecords runs, its hostMap flows into
// both SaveVSANodeDetails (persistence) and WaitForNodeDNS (readiness probe),
// and the workflow completes cleanly. The dual-call (Create then Wait) is
// the regression guard we care about — dropping WaitForNodeDNS would race
// VLM's DNS publish and break cluster_get probes downstream.
//
// This test exercises the legacy DNS-records branch, which is the path
// selected when OCI_USE_TLS_SNI_OVERRIDE=false. The SNI-override path
// (default for OCI) is covered by TestOCICreatePoolWorkflow_CertAuth_SNIOverride_SkipsDNS.
func TestOCICreatePoolWorkflow_CertAuth_RegistersDNS(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)
	withOCIUseTLSSNIOverride(t, false)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	params := &common.CreatePoolParams{
		Name:        "cert-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "cert-pool-uuid"},
		Name:            "cert-pool",
		DeploymentName:  "dep-cert",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{AuthType: envs.USER_CERTIFICATE, Password: "x"},
	}

	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	expectedHostMap := map[string]string{
		"10.0.0.1": "dns-1.dep-cert.vsa.netapp.internal.",
		"10.0.0.2": "dns-2.dep-cert.vsa.netapp.internal.",
	}
	// AuthType==USER_CERTIFICATE → OCICreateCloudDNSRecords must run.
	env.OnActivity("OCICreateCloudDNSRecords", mock.Anything, mock.Anything, pool.DeploymentName, envs.USER_CERTIFICATE).
		Return(&expectedHostMap, nil).Once()
	// WaitForNodeDNS guards SaveVSANodeDetails against VLM publish race;
	// we mock it because the real activity does live DNS lookups.
	// Pool is mutated upstream (ExternalSecret), so match by anything.
	env.OnActivity("WaitForNodeDNS", mock.Anything, mock.Anything, mock.Anything, pool.DeploymentName).
		Return(nil).Once()
	// SaveVSANodeDetails receives the same hostMap the create activity returned.
	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, pool.DeploymentName,
		mock.MatchedBy(func(m map[string]string) bool {
			return m["10.0.0.1"] == expectedHostMap["10.0.0.1"] && m["10.0.0.2"] == expectedHostMap["10.0.0.2"]
		}),
	).Return((*datamodel.Node)(nil), nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCICreatePoolWorkflow_CertAuth_DNSCreateFailsRollsBack verifies that
// when OCICreateCloudDNSRecords fails for a cert-auth pool, the workflow
// surfaces the error and triggers rollback of upstream-registered resources.
// WaitForNodeDNS must NOT be invoked after the create activity fails. Like
// TestOCICreatePoolWorkflow_CertAuth_RegistersDNS, this exercises the
// legacy DNS-records branch and therefore explicitly turns the SNI flag off.
func TestOCICreatePoolWorkflow_CertAuth_DNSCreateFailsRollsBack(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)
	withOCIUseTLSSNIOverride(t, false)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	params := &common.CreatePoolParams{
		Name:        "cert-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "cert-pool-uuid-fail"},
		Name:            "cert-pool",
		DeploymentName:  "dep-cert",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{AuthType: envs.USER_CERTIFICATE, Password: "x"},
	}

	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	env.OnActivity("OCICreateCloudDNSRecords", mock.Anything, mock.Anything, pool.DeploymentName, envs.USER_CERTIFICATE).
		Return((*map[string]string)(nil), errors.New("oci dns boom"))
	// Rollback: ErroredPool runs after DNS create fails. SaveVSANodeDetails,
	// CreatedPool, UpdatePoolFields, and WaitForNodeDNS must NOT be reached.
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

// withOCIUseTLSSNIOverride flips the package-level env.OCIUseTLSSNIOverride
// for the duration of the test. The flag is captured into a package-level
// variable at process start, so tests must overwrite it directly. Original
// value is restored via t.Cleanup.
func withOCIUseTLSSNIOverride(t *testing.T, enabled bool) {
	t.Helper()
	orig := envs.OCIUseTLSSNIOverride
	envs.OCIUseTLSSNIOverride = enabled
	t.Cleanup(func() { envs.OCIUseTLSSNIOverride = orig })
}

// TestOCICreatePoolWorkflow_CertAuth_SNIOverride_SkipsDNS locks in the SNI-
// override branch of the OCI pool-create workflow: when
// env.OCIUseTLSSNIOverride is on, OCICreateCloudDNSRecords and WaitForNodeDNS
// must NOT be scheduled for a cert-auth pool, SaveVSANodeDetails must still
// run with an empty hostMap (so _saveNodeDetails synthesises the SNI), and
// the workflow must complete cleanly. Failing this test means we are
// re-introducing the OCI Private DNS round-trip on the SNI path.
func TestOCICreatePoolWorkflow_CertAuth_SNIOverride_SkipsDNS(t *testing.T) {
	setTestOCIImageEnv(t)
	setOCIExpertModePassword(t, "preset-test-password")
	withOCIVSASerialAllocationFlag(t, false)
	withOCIUseTLSSNIOverride(t, true)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	registerOCICreatePoolVLMRollbackWorkflows(env)

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	registerOCICreatePoolOntapCredentialMocks(env, mockStorage)

	params := &common.CreatePoolParams{
		Name:        "cert-pool",
		AccountName: "test-account",
		SizeInBytes: 1024 * 1024 * 1024 * 1024,
		Region:      "us-ashburn-1",
		PrimaryZone: "us-ashburn-1-ad-1",
		HAPairs:     1,
	}

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "cert-pool-uuid-sni"},
		Name:            "cert-pool",
		DeploymentName:  "dep-cert-sni",
		AccountID:       12345,
		VendorID:        "test-vendor",
		Account:         &datamodel.Account{Name: "test-account"},
		PoolCredentials: &datamodel.PoolCredentials{AuthType: envs.USER_CERTIFICATE, Password: "x"},
	}

	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	mockVlmWorkflowClient := vlm.NewMockVlmWorkflowClient(t)
	mockVlmWorkflowClient.On("CreateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.CreateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{}}, nil)
	mockVlmWorkflowClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil)
	origVSAClientFactory := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmWorkflowClient }
	defer func() { workflows.GetNewVSAClientWorkflowManager = origVSAClientFactory }()

	// Hard guard: if either DNS activity is invoked while the SNI flag is
	// on, the workflow has regressed. We fail the test loudly from inside
	// the activity rather than relying on AssertExpectations to surface a
	// missing-but-allowed call, because Temporal's test env will happily
	// fan out to "any activity" if no OnActivity matches.
	env.OnActivity("OCICreateCloudDNSRecords", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ mock.Arguments) {
			t.Fatalf("OCICreateCloudDNSRecords must NOT run when OCI_USE_TLS_SNI_OVERRIDE=true")
		}).
		Return((*map[string]string)(nil), nil).Maybe()
	env.OnActivity("WaitForNodeDNS", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ mock.Arguments) {
			t.Fatalf("WaitForNodeDNS must NOT run when OCI_USE_TLS_SNI_OVERRIDE=true")
		}).
		Return(nil).Maybe()

	// SaveVSANodeDetails receives an empty hostMap on the SNI path —
	// _saveNodeDetails materialises the synthetic SNI itself.
	env.OnActivity("SaveVSANodeDetails", mock.Anything, mock.Anything, mock.Anything, pool.DeploymentName,
		mock.MatchedBy(func(m map[string]string) bool { return len(m) == 0 }),
	).Return((*datamodel.Node)(nil), nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(OCICreatePoolWorkflow, params, pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// handleActivityError
// ---------------------------------------------------------------------------

func TestHandleActivityError_NilError(t *testing.T) {
	logger := log.NewLogger()
	result := handleActivityError(logger, nil, "CreateVSA", "pool-123")
	assert.Nil(t, result)
}

func TestHandleActivityError_TimeoutError(t *testing.T) {
	logger := log.NewLogger()
	timeoutErr := temporal.NewTimeoutError(enums.TIMEOUT_TYPE_START_TO_CLOSE, fmt.Errorf("activity deadline exceeded"))

	result := handleActivityError(logger, timeoutErr, "CreateVSAClusterDeployment", "pool-uuid-abc")

	require.NotNil(t, result)
	assert.Equal(t, vsaerrors.ErrWorkflowSupervisorTimeout, result.TrackingID)
	require.NotNil(t, result.OriginalErr)
	assert.Contains(t, result.OriginalErr.Error(), "CreateVSAClusterDeployment timed out for pool-uuid-abc")
}

func TestHandleActivityError_NonTimeoutError(t *testing.T) {
	logger := log.NewLogger()
	plainErr := fmt.Errorf("some VLM deployment failure")

	result := handleActivityError(logger, plainErr, "CreateVSAClusterDeployment", "pool-uuid-abc")

	require.NotNil(t, result)
	assert.NotEqual(t, vsaerrors.ErrWorkflowSupervisorTimeout, result.TrackingID,
		"non-timeout errors must not be classified as supervisor timeout")
}

func TestHandleActivityError_WrappedTimeoutError(t *testing.T) {
	logger := log.NewLogger()
	inner := temporal.NewTimeoutError(enums.TIMEOUT_TYPE_HEARTBEAT, fmt.Errorf("heartbeat missed"))
	wrapped := fmt.Errorf("child workflow failed: %w", inner)

	result := handleActivityError(logger, wrapped, "UpgradeVSA", "pool-xyz")

	require.NotNil(t, result)
	assert.Equal(t, vsaerrors.ErrWorkflowSupervisorTimeout, result.TrackingID,
		"wrapped timeout errors must still be classified as supervisor timeout")
}

func TestHandleActivityError_ScheduleToCloseTimeout(t *testing.T) {
	logger := log.NewLogger()
	timeoutErr := temporal.NewTimeoutError(enums.TIMEOUT_TYPE_SCHEDULE_TO_CLOSE, fmt.Errorf("schedule deadline exceeded"))

	result := handleActivityError(logger, timeoutErr, "DeleteSVM", "svm-456")

	require.NotNil(t, result)
	assert.Equal(t, vsaerrors.ErrWorkflowSupervisorTimeout, result.TrackingID)
	require.NotNil(t, result.OriginalErr)
	assert.Contains(t, result.OriginalErr.Error(), "DeleteSVM timed out for svm-456")
}
