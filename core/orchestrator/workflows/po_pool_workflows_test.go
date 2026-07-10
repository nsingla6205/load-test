package workflows

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// newPOTestEnv builds a workflow test env wired with the activities the PO pool
// path uses, with logging context propagation set up. It enables the CVN feature
// flag and points the cluster mgmt IP at a fixed value for the duration of the
// test.
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

	// Job lifecycle (EnsureJobState / UpdateJobStatus).
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

func TestCreatePOPoolWorkflow_HappyPath(t *testing.T) {
	env := newPOTestEnv(t)

	datapath := &common.DataPathResponse{DatapathID: "dp-1", VlanID: "100", IPRange: "10.1.1.0/24", Gateway: "10.1.1.1", IpRangeUUID: "range-1"}

	env.OnActivity("CreateDataPath", mock.Anything, mock.MatchedBy(func(p common.DataPathParams) bool {
		return p.AccountUUID == "test-account-uuid" &&
			p.RemoteAccountID == "projects/p/global/networks/customer-net" &&
			p.MicrozoneUUID == "host-uuid-1" &&
			p.SubnetMaskSize == 27 &&
			p.VlanBandwidth == common.VlanBandwidth50G &&
			p.IsFlexGroup
	})).Return(datapath, nil).Once()
	env.OnActivity("GetPONodeNames", mock.Anything, mock.Anything).Return([]string{"node-1", "node-2"}, nil).Once()
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.MatchedBy(func(in activities.EnsurePOIPSpaceInput) bool {
		return in.Name == "ipspace_testaccountuuid_100"
	})).Return(nil).Once()
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.MatchedBy(func(in activities.EnsurePOBroadcastDomainInput) bool {
		return in.IPSpace == "ipspace_testaccountuuid_100" && in.Name == "bdomain_testaccountuuid_100"
	})).Return(nil).Once()
	// One VLAN port per node is created (bound to the broadcast domain) before
	// the SVM and data LIFs are provisioned in the pool ipspace.
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.MatchedBy(func(in activities.EnsurePOVlanPortsInput) bool {
		return in.VlanTag == 100 && in.IPSpace == "ipspace_testaccountuuid_100" &&
			in.BroadcastDomain == "bdomain_testaccountuuid_100" && len(in.NodeNames) == 2
	})).Return(nil).Once()
	env.OnActivity("AllocatePOSVMName", mock.Anything, mock.Anything).Return("svm_testaccountuuid_0a1b2c3d", nil).Once()
	env.OnActivity("CreatePOSvm", mock.Anything, mock.MatchedBy(func(in activities.CreatePOSvmInput) bool {
		return in.EnableNfs && in.IPSpace == "ipspace_testaccountuuid_100"
	})).Return(&activities.CreatePOSvmOutput{SvmName: "po-svm-01", SvmUUID: "svm-uuid"}, nil).Once()
	env.OnActivity("EnsurePOSvmMgmtPolicies", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("SavePOSVMRecord", mock.Anything, mock.MatchedBy(func(in activities.SavePOSVMRecordInput) bool {
		return in.PoolID == 1 && in.AccountID == 1 &&
			in.SvmName == "svm_testaccountuuid_0a1b2c3d" &&
			in.SvmUUID == "svm-uuid" &&
			in.IPSpace == "ipspace_testaccountuuid_100"
	})).Return(&datamodel.Svm{Name: "svm_testaccountuuid_0a1b2c3d"}, nil).Once()
	// Eager multi-LIF: 2 nodes × NFS = 2 data LIFs, each homed on a node with a
	// distinct real address from the CVN range.
	env.OnActivity("CreatePODataLifs", mock.Anything, mock.MatchedBy(func(in activities.CreatePODataLifsInput) bool {
		if len(in.Lifs) != 2 {
			return false
		}
		seenAddrs := map[string]bool{}
		for _, lif := range in.Lifs {
			if lif.HomeNode == "" || lif.IPAddress == "" || lif.Netmask == "" || seenAddrs[lif.IPAddress] {
				return false
			}
			if !strings.HasPrefix(lif.Name, "svm_testaccountuuid_0a1b2c3d_") || len(lif.Name) != len("svm_testaccountuuid_0a1b2c3d_")+8 {
				return false
			}
			seenAddrs[lif.IPAddress] = true
		}
		return true
	})).Return(nil).Once()
	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Once()

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// VLM-only activities must never be invoked on the PO path.
	env.AssertNotCalled(t, "FindTenancyProject", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "IdentifyVMs", mock.Anything)
	env.AssertNotCalled(t, "CreateAutoTierBucket", mock.Anything)
	env.AssertExpectations(t)
}

func TestCreatePOPoolWorkflow_RollbackOnFailure(t *testing.T) {
	env := newPOTestEnv(t)

	datapath := &common.DataPathResponse{DatapathID: "dp-1", VlanID: "100", IPRange: "10.1.1.0/24", Gateway: "10.1.1.1", IpRangeUUID: "range-1"}

	env.OnActivity("CreateDataPath", mock.Anything, mock.Anything).Return(datapath, nil)
	env.OnActivity("AllocatePOSVMName", mock.Anything, mock.Anything).Return("svm_testaccountuuid_0a1b2c3d", nil)
	env.OnActivity("CreatePOSvm", mock.Anything, mock.Anything).Return(&activities.CreatePOSvmOutput{SvmName: "po-svm-01", SvmUUID: "svm-uuid"}, nil)
	env.OnActivity("EnsurePOSvmMgmtPolicies", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("SavePOSVMRecord", mock.Anything, mock.Anything).Return(&datamodel.Svm{Name: "svm_testaccountuuid_0a1b2c3d"}, nil)
	env.OnActivity("GetPONodeNames", mock.Anything, mock.Anything).Return([]string{"node-1", "node-2"}, nil)
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreatePODataLifs", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.Anything).Return(nil)
	// Fail after the SVM and datapath have been created so SVM rollback fires.
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("mark ready failed", "NonRetryableErr", nil))

	// Rollback (LIFO): DeletePOSvm, DeletePoolResourcesOnRollback, ErroredPool.
	env.OnActivity("DeletePOSvm", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("DeletePoolResourcesOnRollback", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil).Maybe()

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())

	env.AssertCalled(t, "DeletePOSvm", mock.Anything, mock.Anything)
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
	env.OnActivity("GetPOSvmForPoolID", mock.Anything, int64(1)).Return(&datamodel.Svm{Name: "po-svm-01"}, nil).Once()
	env.OnActivity("DeletePOSvm", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("SoftDeleteSvm", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, "kms-1", false).Return(nil).Once()
	env.OnActivity("DeletePoolResources", mock.Anything, mock.Anything).Return(pool, nil).Once()

	params := &common.DeletePoolParams{PoolID: "po-pool-uuid", AccountName: "test-account"}
	env.ExecuteWorkflow(DeletePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// VLM teardown must not run for a PO pool.
	env.AssertNotCalled(t, "DeleteServiceAccount", mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "DeleteAutoTierBucket", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "GetCloudDNSRecords", mock.Anything, mock.Anything, mock.Anything)
	env.AssertCalled(t, "DeletePOSvm", mock.Anything, mock.Anything)
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
	// FailedPool rollback should fire on error.
	env.OnActivity("FailedPool", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	params := &common.DeletePoolParams{PoolID: "po-pool-uuid", AccountName: "test-account"}
	env.ExecuteWorkflow(DeletePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
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
