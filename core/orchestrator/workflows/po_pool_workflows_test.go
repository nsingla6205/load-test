package workflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// newPOTestEnv builds a workflow test env wired with the activities the PO pool
// path uses, with logging context propagation set up.
func newPOTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	origVerifyKMS := verifyKmsConfigReachability
	verifyKmsConfigReachability = func(ctx workflow.Context, kmsConfigID string) error { return nil }
	t.Cleanup(func() { verifyKmsConfigReachability = origVerifyKMS })

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

	env.RegisterActivity(&activities.CommonActivities{})
	env.RegisterActivity(&activities.PoolActivity{})
	env.RegisterActivity(&activities.SvmActivity{})
	env.RegisterActivity(&activities.POOntapActivity{})
	env.RegisterActivity(activities.NewCVNActivity())
	env.RegisterActivity(&activities.IPRangeActivity{})
	env.RegisterActivity(&activities.PODataLifActivity{})

	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("GetJob", mock.Anything, "default-test-workflow-id").Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	return env
}

func newPOParams() *common.CreatePoolParams {
	return &common.CreatePoolParams{
		Name:           "po-pool",
		AccountName:    "test-account",
		Region:         "us-central1",
		PrimaryZone:    "us-central1-a",
		VendorSubNetID: "projects/p/global/networks/customer-net",
		ClusterName:    "test-cluster",
		HostUUID:       "host-uuid-1",
		ServiceLevel:   common.ServiceLevelExtreme,
		SizeInBytes:    uint64(common.BytesPerTiB),
		Mode:           common.ONTAPMode,
	}
}

func newPOPool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "po-pool-uuid"},
		AccountID: 1,
		HostUUID:  "host-uuid-1",
		Account:   &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"}, Name: "test-account"},
	}
}

func poAllocatedLifs() []*datamodel.Lif {
	return []*datamodel.Lif{
		{Name: "svm_testaccountuuid_0a1b2c3d_aabbccdd", IPAddress: "10.1.1.4", SubnetMask: "255.255.255.0"},
		{Name: "svm_testaccountuuid_0a1b2c3d_11223344", IPAddress: "10.1.1.5", SubnetMask: "255.255.255.0"},
	}
}

func poDatapathResponse() *common.DataPathResponse {
	return &common.DataPathResponse{
		DatapathID:  "range-1",
		VlanID:      "100",
		IPRange:     "10.1.1.0/24",
		Gateway:     "10.1.1.1",
		IpRangeUUID: "range-1",
	}
}

func stubPOTenancyFetch(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("FindTenancyProject", mock.Anything, mock.Anything).Return("tenant-project-123", nil).Once()
	env.OnActivity("GetSnHostProject", mock.Anything, "tenant-project-123").Return("sn-host-project-456", nil).Once()
	env.OnActivity("SavePoolWithClusterDetails", mock.Anything, mock.Anything, mock.MatchedBy(func(details *datamodel.ClusterDetails) bool {
		return details.ExternalName == "test-cluster" &&
			details.RegionalTenantProject == "tenant-project-123" &&
			details.SnHostProject == "sn-host-project-456"
	})).Return(nil).Once()
}

func stubPOCreatePrereqs(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("GetPONodeNames", mock.Anything, mock.Anything).Return([]string{"node-1", "node-2"}, nil)
	env.OnActivity("PreSelectDataLifIPRange", mock.Anything, mock.Anything).Return("range-1", nil)
	env.OnActivity("CreateDataPath", mock.Anything, mock.Anything).Return(poDatapathResponse(), nil)
	stubPOTenancyFetch(env)
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).Return(nil)
}

func stubPOSvmCreate(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("AllocatePOSVMName", mock.Anything, mock.Anything).Return("svm_testaccountuuid_0a1b2c3d", nil)
	env.OnActivity("CreatePOSvm", mock.Anything, mock.Anything).Return(&activities.CreatePOSvmOutput{SvmName: "po-svm-01", SvmUUID: "svm-uuid"}, nil)
	env.OnActivity("EnsurePOSvmMgmtPolicies", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("SavePOSVMRecord", mock.Anything, mock.Anything).Return(&datamodel.Svm{
		BaseModel: datamodel.BaseModel{ID: 99},
		Name:      "svm_testaccountuuid_0a1b2c3d",
	}, nil)
}

func stubPODataLifAllocation(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("ResolvePONodeIDs", mock.Anything, mock.Anything).Return(map[string]int64{"node-1": 1, "node-2": 2}, nil)
	env.OnActivity("AllocatePODataLifIPs", mock.Anything, mock.Anything).Return(poAllocatedLifs(), nil)
}

func TestCreatePOPoolWorkflow_HappyPath(t *testing.T) {
	env := newPOTestEnv(t)
	stubPOCreatePrereqs(env)
	stubPOSvmCreate(env)
	stubPODataLifAllocation(env)

	env.OnActivity("CreatePODataLifs", mock.Anything, mock.MatchedBy(func(in activities.CreatePODataLifsInput) bool {
		return len(in.Lifs) == 2 &&
			in.Lifs[0].IPAddress == "10.1.1.4" &&
			in.Lifs[1].IPAddress == "10.1.1.5"
	})).Return(nil).Once()
	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Once()

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	env.AssertCalled(t, "FindTenancyProject", mock.Anything, mock.Anything)
	env.AssertCalled(t, "GetSnHostProject", mock.Anything, "tenant-project-123")
	env.AssertCalled(t, "SavePoolWithClusterDetails", mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "IdentifyVMs", mock.Anything)
	env.AssertNotCalled(t, "CreateAutoTierBucket", mock.Anything)
	env.AssertExpectations(t)
}

// TestCreatePOPoolWorkflow_PreservesExistingClusterDetails guards the in-place
// ClusterDetails update: the tenancy/SN-host save must set the newly resolved
// fields without clearing any values already present on the pool (the DB layer
// replaces the whole cluster_details JSON, so the struct handed to it must carry
// the prior fields too).
func TestCreatePOPoolWorkflow_PreservesExistingClusterDetails(t *testing.T) {
	env := newPOTestEnv(t)

	env.OnActivity("GetPONodeNames", mock.Anything, mock.Anything).Return([]string{"node-1", "node-2"}, nil)
	env.OnActivity("PreSelectDataLifIPRange", mock.Anything, mock.Anything).Return("range-1", nil)
	env.OnActivity("CreateDataPath", mock.Anything, mock.Anything).Return(poDatapathResponse(), nil)
	env.OnActivity("FindTenancyProject", mock.Anything, mock.Anything).Return("tenant-project-123", nil).Once()
	env.OnActivity("GetSnHostProject", mock.Anything, "tenant-project-123").Return("sn-host-project-456", nil).Once()

	env.OnActivity("SavePoolWithClusterDetails", mock.Anything, mock.Anything, mock.MatchedBy(func(details *datamodel.ClusterDetails) bool {
		return details.ExternalName == "test-cluster" &&
			details.RegionalTenantProject == "tenant-project-123" &&
			details.SnHostProject == "sn-host-project-456" &&
			details.OntapVersion == "9.99.9" &&
			details.AllocatedSubnetCIDR == "10.9.9.0/29"
	})).Return(nil).Once()

	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).Return(nil)

	stubPOSvmCreate(env)
	stubPODataLifAllocation(env)
	env.OnActivity("CreatePODataLifs", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Once()

	pool := newPOPool()
	pool.ClusterDetails = datamodel.ClusterDetails{
		OntapVersion:        "9.99.9",
		AllocatedSubnetCIDR: "10.9.9.0/29",
	}

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertCalled(t, "SavePoolWithClusterDetails", mock.Anything, mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

func TestCreatePOPoolWorkflow_RollbackOnLifCreateFailure(t *testing.T) {
	env := newPOTestEnv(t)
	stubPOCreatePrereqs(env)
	stubPOSvmCreate(env)
	stubPODataLifAllocation(env)

	env.OnActivity("CreatePODataLifs", mock.Anything, mock.Anything).
		Return(temporal.NewNonRetryableApplicationError("lif create failed", "NonRetryableErr", nil))
	env.OnActivity("ReleaseDataLifReservations", mock.Anything, activities.ReleaseDataLifReservationsInput{SvmID: 99}).Return(nil)
	env.OnActivity("DeletePOSvm", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeletePoolResourcesOnRollback", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Maybe()

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertCalled(t, "ReleaseDataLifReservations", mock.Anything, activities.ReleaseDataLifReservationsInput{SvmID: 99})
}

func TestCreatePOPoolWorkflow_RollbackOnFailure(t *testing.T) {
	env := newPOTestEnv(t)
	stubPOCreatePrereqs(env)
	stubPOSvmCreate(env)
	stubPODataLifAllocation(env)

	env.OnActivity("CreatePODataLifs", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("mark ready failed", "NonRetryableErr", nil))

	env.OnActivity("ReleaseDataLifReservations", mock.Anything, activities.ReleaseDataLifReservationsInput{SvmID: 99}).Return(nil)
	env.OnActivity("DeletePOSvm", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeletePoolResourcesOnRollback", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Maybe()

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertCalled(t, "DeletePOSvm", mock.Anything, mock.Anything)
	env.AssertCalled(t, "ReleaseDataLifReservations", mock.Anything, activities.ReleaseDataLifReservationsInput{SvmID: 99})
}

func TestCreatePOPoolWorkflow_HappyPathWithoutZones(t *testing.T) {
	env := newPOTestEnv(t)

	datapath := &common.DataPathResponse{DatapathID: "dp-1", VlanID: "100", IPRange: "10.1.1.0/24", Gateway: "10.1.1.1", IpRangeUUID: "range-1"}

	env.OnActivity("GetPONodeNames", mock.Anything, mock.Anything).Return([]string{"node-1"}, nil).Once()
	env.OnActivity("PreSelectDataLifIPRange", mock.Anything, mock.Anything).Return("range-1", nil).Once()
	env.OnActivity("CreateDataPath", mock.Anything, mock.Anything).Return(datapath, nil).Once()
	stubPOTenancyFetch(env)
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("AllocatePOSVMName", mock.Anything, mock.Anything).Return("svm_testaccountuuid_0a1b2c3d", nil).Once()
	env.OnActivity("CreatePOSvm", mock.Anything, mock.Anything).Return(&activities.CreatePOSvmOutput{SvmName: "po-svm-01", SvmUUID: "svm-uuid"}, nil).Once()
	env.OnActivity("EnsurePOSvmMgmtPolicies", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("SavePOSVMRecord", mock.Anything, mock.Anything).Return(&datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 99}, Name: "svm_testaccountuuid_0a1b2c3d"}, nil).Once()
	env.OnActivity("ResolvePONodeIDs", mock.Anything, mock.Anything).Return(map[string]int64{"node-1": 1}, nil).Once()
	env.OnActivity("AllocatePODataLifIPs", mock.Anything, mock.Anything).Return([]*datamodel.Lif{
		{Name: "svm_testaccountuuid_0a1b2c3d_aabbccdd", IPAddress: "10.1.1.4", SubnetMask: "255.255.255.0"},
	}, nil).Once()
	env.OnActivity("CreatePODataLifs", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Once()

	params := newPOParams()
	params.PrimaryZone = ""
	params.SecondaryZone = ""
	params.IsRegionalHA = true

	env.ExecuteWorkflow(CreatePoolWorkflow, params, newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestCreatePOPoolWorkflow_MissingHostUUID(t *testing.T) {
	env := newPOTestEnv(t)

	env.OnActivity("DeletePoolResourcesOnRollback", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Maybe()

	pool := newPOPool()
	pool.HostUUID = ""

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), pool)

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), common.POValidationHostUUIDMsg)
	env.AssertNotCalled(t, "CreateDataPath", mock.Anything, mock.Anything)
}

func newPODeletePool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:    datamodel.BaseModel{ID: 1, UUID: "po-pool-uuid"},
		AccountID:    1,
		HostUUID:     "host-uuid-1",
		Name:         "po-pool",
		ServiceLevel: "EXTREME",
		Network:      "projects/p/global/networks/customer-net",
		Account:      &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"}, Name: "test-account"},
		PoolAttributes: &datamodel.PoolAttributes{
			ServiceLevel: "EXTREME",
		},
		KmsConfig: &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-1"}},
	}
}

func TestDeletePOPoolWorkflow_HappyPath(t *testing.T) {
	env := newPOTestEnv(t)
	env.RegisterActivity(&kms_activities.KmsConfigActivity{})
	env.RegisterActivity(&activities.CancellationActivity{})

	pool := newPODeletePool()
	env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("DeletingPoolResources", mock.Anything, mock.Anything).Return(pool, nil).Once()
	env.OnActivity("GetPOSvmForPoolID", mock.Anything, int64(1)).Return(&datamodel.Svm{
		BaseModel: datamodel.BaseModel{ID: 99},
		Name:      "po-svm-01",
	}, nil).Once()
	env.OnActivity("DeletePOSvm", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("ReleaseDataLifReservations", mock.Anything, activities.ReleaseDataLifReservationsInput{SvmID: 99}).Return(nil).Once()
	env.OnActivity("SoftDeleteSvm", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, "kms-1", false).Return(nil).Once()
	env.OnActivity("DeletePoolResources", mock.Anything, mock.Anything).Return(pool, nil).Once()

	params := &common.DeletePoolParams{PoolID: "po-pool-uuid", AccountName: "test-account"}
	env.ExecuteWorkflow(DeletePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	env.AssertNotCalled(t, "DeleteServiceAccount", mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "DeleteAutoTierBucket", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "GetCloudDNSRecords", mock.Anything, mock.Anything, mock.Anything)
	env.AssertCalled(t, "DeletePOSvm", mock.Anything, mock.Anything)
	env.AssertCalled(t, "ReleaseDataLifReservations", mock.Anything, activities.ReleaseDataLifReservationsInput{SvmID: 99})
}

func TestDeletePOPoolWorkflow_SvmDeleteFailure(t *testing.T) {
	env := newPOTestEnv(t)
	env.RegisterActivity(&kms_activities.KmsConfigActivity{})
	env.RegisterActivity(&activities.CancellationActivity{})

	pool := newPODeletePool()
	env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("DeletingPoolResources", mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("GetPOSvmForPoolID", mock.Anything, int64(1)).Return(&datamodel.Svm{Name: "po-svm-01"}, nil)
	env.OnActivity("DeletePOSvm", mock.Anything, mock.Anything).
		Return(temporal.NewNonRetryableApplicationError("svm delete failed", "PanicError", nil))
	env.OnActivity("FailedPool", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	params := &common.DeletePoolParams{PoolID: "po-pool-uuid", AccountName: "test-account"}
	env.ExecuteWorkflow(DeletePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "ReleaseDataLifReservations", mock.Anything, mock.Anything)
}

func TestCreatePOPoolWorkflow_MissingAccountUUID(t *testing.T) {
	env := newPOTestEnv(t)

	env.OnActivity("DeletePoolResourcesOnRollback", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Maybe()

	pool := newPOPool()
	pool.Account = &datamodel.Account{Name: "test-account"}

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), pool)

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pool account uuid must not be empty")
}

func TestCreatePOPoolWorkflow_KmsReachabilityFailure(t *testing.T) {
	env := newPOTestEnv(t)

	origVerifyKMS := verifyKmsConfigReachability
	verifyKmsConfigReachability = func(ctx workflow.Context, kmsConfigID string) error {
		return temporal.NewNonRetryableApplicationError("kms unreachable", "NonRetryableErr", nil)
	}
	t.Cleanup(func() { verifyKmsConfigReachability = origVerifyKMS })

	env.OnActivity("DeletePoolResourcesOnRollback", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Maybe()

	params := newPOParams()
	params.KmsConfigId = "kms-1"

	env.ExecuteWorkflow(CreatePoolWorkflow, params, newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "kms unreachable")
}

func TestCreatePOPoolWorkflow_TenantProjectNotFound(t *testing.T) {
	env := newPOTestEnv(t)
	env.OnActivity("GetPONodeNames", mock.Anything, mock.Anything).Return([]string{"node-1", "node-2"}, nil)
	env.OnActivity("PreSelectDataLifIPRange", mock.Anything, mock.Anything).Return("range-1", nil)
	env.OnActivity("CreateDataPath", mock.Anything, mock.Anything).Return(poDatapathResponse(), nil)
	env.OnActivity("FindTenancyProject", mock.Anything, mock.Anything).Return("", temporal.NewNonRetryableApplicationError(
		"tenancy not found", "NonRetryableErr", vsaerrors.NewVCPError(vsaerrors.ErrPSAPeeringNotFoundError, errors.New("vpc peering not found"))))
	env.OnActivity("DeletePoolResourcesOnRollback", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Maybe()

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "GetSnHostProject", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "SavePoolWithClusterDetails", mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatedPool", mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOIPSpace", mock.Anything, mock.Anything)
}

// TestCreatePOPoolWorkflow_TenantProjectEmptyWithoutError covers the explicit
// validation branch where FindTenancyProject returns an empty tenant project
// number with a nil error: the workflow must fail non-retryably and skip all
// downstream steps rather than proceeding with an empty tenant project.
func TestCreatePOPoolWorkflow_TenantProjectEmptyWithoutError(t *testing.T) {
	env := newPOTestEnv(t)
	env.OnActivity("GetPONodeNames", mock.Anything, mock.Anything).Return([]string{"node-1", "node-2"}, nil)
	env.OnActivity("PreSelectDataLifIPRange", mock.Anything, mock.Anything).Return("range-1", nil)
	env.OnActivity("CreateDataPath", mock.Anything, mock.Anything).Return(poDatapathResponse(), nil)
	env.OnActivity("FindTenancyProject", mock.Anything, mock.Anything).Return("", nil)
	env.OnActivity("DeletePoolResourcesOnRollback", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Maybe()

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "tenant project not found")
	env.AssertNotCalled(t, "GetSnHostProject", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "SavePoolWithClusterDetails", mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatedPool", mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOIPSpace", mock.Anything, mock.Anything)
}

func TestCreatePOPoolWorkflow_SnHostProjectNotFound(t *testing.T) {
	env := newPOTestEnv(t)
	env.OnActivity("GetPONodeNames", mock.Anything, mock.Anything).Return([]string{"node-1", "node-2"}, nil)
	env.OnActivity("PreSelectDataLifIPRange", mock.Anything, mock.Anything).Return("range-1", nil)
	env.OnActivity("CreateDataPath", mock.Anything, mock.Anything).Return(poDatapathResponse(), nil)
	env.OnActivity("FindTenancyProject", mock.Anything, mock.Anything).Return("tenant-project-123", nil)
	env.OnActivity("GetSnHostProject", mock.Anything, "tenant-project-123").Return("", nil)
	env.OnActivity("DeletePoolResourcesOnRollback", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Maybe()

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "SN host project not found")
	env.AssertNotCalled(t, "SavePoolWithClusterDetails", mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatedPool", mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOIPSpace", mock.Anything, mock.Anything)
}

func TestUpdatePOPoolWorkflow_MetadataAndSizeOnly(t *testing.T) {
	env := newPOTestEnv(t)

	origThroughput := int64(128)
	origIops := int64(2048)
	pool := &datamodel.Pool{
		BaseModel:     datamodel.BaseModel{ID: 1, UUID: "po-pool-uuid"},
		HostUUID:      "host-uuid-1",
		SizeInBytes:   int64(common.BytesPerTiB),
		ServiceLevel:  common.ServiceLevelExtreme,
		APIAccessMode: common.ONTAPMode,
		Description:   "before",
		PoolAttributes: &datamodel.PoolAttributes{
			ServiceLevel:    common.ServiceLevelExtreme,
			Iops:            origIops,
			ThroughputMibps: origThroughput,
		},
	}
	newSize := uint64(2 * int64(common.BytesPerTiB))
	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		PoolId:               "po-pool-uuid",
		Description:          "after",
		SizeInBytes:          newSize,
		TotalThroughputMibps: origThroughput,
		TotalIops:            &origIops,
	}

	env.OnActivity("UpdatedPool", mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
		return p.Description == "after" &&
			p.SizeInBytes == int64(newSize) &&
			p.PoolAttributes != nil &&
			p.PoolAttributes.Iops == origIops &&
			p.PoolAttributes.ThroughputMibps == origThroughput
	})).Return(pool, nil).Once()

	env.ExecuteWorkflow(UpdatePoolWorkflow, params, pool, nil)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "ParseVlmConfig", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "ModifyQoSPolicyAndApplyToSVM", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

func TestUpdatePOPoolWorkflow_MissingHostUUID(t *testing.T) {
	env := newPOTestEnv(t)

	pool := &datamodel.Pool{
		BaseModel:     datamodel.BaseModel{ID: 1, UUID: "po-pool-uuid"},
		HostUUID:      "",
		SizeInBytes:   int64(common.BytesPerTiB),
		ServiceLevel:  common.ServiceLevelExtreme,
		APIAccessMode: common.ONTAPMode,
	}
	params := &common.UpdatePoolParams{
		AccountName: "test-account",
		PoolId:      "po-pool-uuid",
		Description: "after",
	}

	env.ExecuteWorkflow(UpdatePoolWorkflow, params, pool, nil)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "UpdatedPool", mock.Anything, mock.Anything)
}

func TestUpdatePOPoolWorkflow_LabelsCreatePoolAttributes(t *testing.T) {
	env := newPOTestEnv(t)

	labels := datamodel.JSONB{"env": "test"}
	pool := &datamodel.Pool{
		BaseModel:     datamodel.BaseModel{ID: 1, UUID: "po-pool-uuid"},
		HostUUID:      "host-uuid-1",
		SizeInBytes:   int64(common.BytesPerTiB),
		ServiceLevel:  common.ServiceLevelExtreme,
		APIAccessMode: common.ONTAPMode,
	}
	params := &common.UpdatePoolParams{
		AccountName: "test-account",
		PoolId:      "po-pool-uuid",
		Labels:      &labels,
	}

	env.OnActivity("UpdatedPool", mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
		return p.PoolAttributes != nil &&
			p.PoolAttributes.Labels != nil &&
			(*p.PoolAttributes.Labels)["env"] == "test"
	})).Return(pool, nil).Once()

	env.ExecuteWorkflow(UpdatePoolWorkflow, params, pool, nil)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestUpdatePOPoolWorkflow_UpdatedPoolFailure(t *testing.T) {
	env := newPOTestEnv(t)

	pool := &datamodel.Pool{
		BaseModel:     datamodel.BaseModel{ID: 1, UUID: "po-pool-uuid"},
		HostUUID:      "host-uuid-1",
		SizeInBytes:   int64(common.BytesPerTiB),
		ServiceLevel:  common.ServiceLevelExtreme,
		APIAccessMode: common.ONTAPMode,
	}
	params := &common.UpdatePoolParams{
		AccountName: "test-account",
		PoolId:      "po-pool-uuid",
		Description: "after",
	}

	env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("db failed", "NonRetryableErr", nil)).Once()

	env.ExecuteWorkflow(UpdatePoolWorkflow, params, pool, nil)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}
