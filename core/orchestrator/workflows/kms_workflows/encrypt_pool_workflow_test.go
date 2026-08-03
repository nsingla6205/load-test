package kms_workflows

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	env2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// newTestEncryptHeader builds the temporal header carrying the logger fields the workflow expects.
func newTestEncryptHeader() *commonpb.Header {
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	return &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
}

// newTestEncryptKmsConfig returns a VCP-created KMS config so verifyKmsReachabilityBeforeEncrypt
// skips GrantRoleActivity.
func newTestEncryptKmsConfig(uuid string) *datamodel.KmsConfig {
	return &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: uuid},
		AccountID: 1,
		State:     datamodel.LifeCycleStateREADY,
		KmsAttributes: &datamodel.KmsAttributes{
			CreationMode: datamodel.KmsCreationModeVCP,
		},
	}
}

func TestEncryptPoolWorkflow_CmekToCmekRekey(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()
	cvp.CVP_HOST = "localhost:8009"

	params := &common.EncryptPoolParams{
		PoolUUID:    "pool1",
		KmsConfigID: "vsa-kms-uuid",
		AccountName: "test-account",
	}

	newRekeyPool := func() *datamodel.Pool {
		return &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: int64(1), UUID: "pool1"},
			State:          datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			},
			// KmsConfigID.Valid=true routes the workflow into the CMEK -> CMEK rekey branch.
			KmsConfigID: sql.NullInt64{Int64: 1, Valid: true},
			KmsConfig:   &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "previous-kms-uuid"}},
		}
	}

	dbNodes := []*datamodel.Node{{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}}
	svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

	t.Run("WhenRekeySucceeds_PoolBecomesReady", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() { env2.AuthType = origAuthType }()

		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		kmsConfig := newTestEncryptKmsConfig(params.KmsConfigID)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		// verifyKmsReachabilityBeforeEncrypt pre-flight
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// pool selection + rekey leg
		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(newRekeyPool(), nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("RekeyKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(svm, nil)
		// UpdatePoolWithKmsConfigActivity returns the freshly repointed pool (now on the target
		// KMS config); the workflow reuses it for the final UpdatePoolState transition.
		repointedPool := newRekeyPool()
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(repointedPool, nil)
		env.OnActivity("UpdatePoolState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		env.ExecuteWorkflow(EncryptPoolWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("WhenPostRekeyVerifyTargetFails_BestEffortStillSucceeds", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() { env2.AuthType = origAuthType }()

		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		kmsConfig := newTestEncryptKmsConfig(params.KmsConfigID)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil).Maybe()
		// pre-flight verify
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, params.KmsConfigID, true).Return(nil).Once()

		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(newRekeyPool(), nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("RekeyKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(svm, nil)
		repointedPool := newRekeyPool()
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(repointedPool, nil)
		env.OnActivity("UpdatePoolState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		// outer defer: target verify fails (best-effort) then previous still runs
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, params.KmsConfigID, false).
			Return(temporal.NewNonRetryableApplicationError("verify failed", "error", nil)).
			Once()
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, "previous-kms-uuid", false).Return(nil).Once()

		env.ExecuteWorkflow(EncryptPoolWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("WhenPostRekeyVerifyPreviousFails_BestEffortStillSucceeds", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() { env2.AuthType = origAuthType }()

		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		kmsConfig := newTestEncryptKmsConfig(params.KmsConfigID)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil).Maybe()
		// pre-flight verify
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, params.KmsConfigID, true).Return(nil).Once()

		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(newRekeyPool(), nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("RekeyKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(svm, nil)
		repointedPool := newRekeyPool()
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(repointedPool, nil)
		env.OnActivity("UpdatePoolState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		// outer defer: target succeeds, previous fails (best-effort)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, params.KmsConfigID, false).Return(nil).Once()
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, "previous-kms-uuid", false).
			Return(temporal.NewNonRetryableApplicationError("previous verify failed", "error", nil)).
			Once()

		env.ExecuteWorkflow(EncryptPoolWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("WhenRekeyKmsForSvmActivityFails_MarksPoolErrorAndErrors", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() { env2.AuthType = origAuthType }()

		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		kmsConfig := newTestEncryptKmsConfig(params.KmsConfigID)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(newRekeyPool(), nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("RekeyKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, temporal.NewNonRetryableApplicationError("Rekey failed", "error", nil))
		// The rekey activity itself was invoked (and failed) - ONTAP may be left in an intermediate
		// state, so the workflow marks the pool Error rather than reverting it to Available.
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(EncryptPoolWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("WhenUpdatePoolFailsAfterRekey_ReconcilesFkAndDoesNotRevertLifecycle", func(tt *testing.T) {
		// Rekey succeeded (ONTAP/SVM on target key) but pool FK update failed. Failure handling
		// must compensate by repointing the pool FK — not revert lifecycle to the pre-encrypt snapshot.
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() { env2.AuthType = origAuthType }()

		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		kmsConfig := newTestEncryptKmsConfig(params.KmsConfigID)
		updatePoolStateCalls := 0

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(newRekeyPool(), nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("RekeyKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(svm, nil)
		// First attempt (in rekeySingleVsaPool) fails; compensation path retries and succeeds.
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).
			Return(nil, temporal.NewNonRetryableApplicationError("Update pool KMS FK failed", "error", nil)).
			Once()
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).
			Return(newRekeyPool(), nil).
			Once()
		env.OnActivity("UpdatePoolState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) { updatePoolStateCalls++ }).
			Return(nil, nil).
			Maybe()

		env.ExecuteWorkflow(EncryptPoolWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		assert.Equal(tt, 0, updatePoolStateCalls, "must not revert pool lifecycle after successful ONTAP rekey")
		env.AssertExpectations(tt)
	})

	t.Run("WhenTargetKmsConfigSameAsCurrentPool_NoRekey", func(tt *testing.T) {
		// Pool already carries KmsConfigID.Int64 == 1; GetKmsConfigActivity returns a config with
		// the same integer ID → the workflow must revert the pool and return DONE without rekeying.
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() { env2.AuthType = origAuthType }()

		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		// Return a KmsConfig whose integer ID matches the pool's KmsConfigID.Int64.
		sameKmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{ID: int64(1), UUID: params.KmsConfigID},
			KmsAttributes: &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(sameKmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(sameKmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(newRekeyPool(), nil)
		// Pool is already on the target key → must revert to previous state.
		env.OnActivity("UpdatePoolState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		// No rekey-specific activities should be called.
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
		env.OnActivity("RekeyKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()

		env.ExecuteWorkflow(EncryptPoolWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("WhenPoolNotFoundInVcp_ReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())

		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		kmsConfig := newTestEncryptKmsConfig(params.KmsConfigID)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// No pool with the requested UUID -> VCP is self-sufficient and the workflow fails with not-found.
		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("pool not found", "NotFound", nil))
		// Failure path marks the pool as Error (best-effort) - GetPool is a pool-dependent lookup too.
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(EncryptPoolWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})
}

func TestEncryptPoolWorkflow_AdditionalBranches(t *testing.T) {
	params := &common.EncryptPoolParams{
		PoolUUID:    "pool-branch",
		KmsConfigID: "kms-branch",
		AccountName: "acct-branch",
	}

	t.Run("InitialUpdateJobStatusFailure", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).
			Return(temporal.NewNonRetryableApplicationError("status update failed", "error", nil)).
			Once()

		env.ExecuteWorkflow(EncryptPoolWorkflow, params)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("FinalUpdateJobStatusFailureAfterSuccessPath", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		env.RegisterActivity(&activities.SvmActivity{})

		// First status update succeeds, final DONE update fails.
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Once()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).
			Return(temporal.NewNonRetryableApplicationError("final status update failed", "error", nil)).
			Once()

		// Reachability precheck succeeds and the pool is already on the requested key (no-op success path).
		sameKmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{ID: int64(1), UUID: params.KmsConfigID},
			KmsAttributes: &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP},
		}
		pool := &datamodel.Pool{
			BaseModel:   datamodel.BaseModel{ID: int64(1), UUID: params.PoolUUID},
			KmsConfigID: sql.NullInt64{Int64: 1, Valid: true},
		}
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(sameKmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(sameKmsConfig, nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(pool, nil)
		env.OnActivity("UpdatePoolState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()

		env.ExecuteWorkflow(EncryptPoolWorkflow, params)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("ReachabilityPrecheckFailureTriggersPoolErrorAndErrorStatus", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Once()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Once()

		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).
			Return(nil, temporal.NewNonRetryableApplicationError("kms fetch failed", "error", nil)).
			Once()
		// Failure path marks the pool as Error.
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// deferred best-effort reachability verify call
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(EncryptPoolWorkflow, params)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("EmptyKmsConfigIDSkipsPrecheckAndNoPoolPathReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		emptyKmsParams := &common.EncryptPoolParams{
			PoolUUID:    "pool-no-kms",
			KmsConfigID: "",
			AccountName: "acct-branch",
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("pool not found", "NotFound", nil)).Once()
		// Failure path marks the pool as Error; deferred verify still executes with empty UUID.
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(EncryptPoolWorkflow, emptyKmsParams)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("MigratePathWithNoVolumesMarksPoolReady", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		migrateParams := &common.EncryptPoolParams{
			PoolUUID:    "pool-migrate",
			KmsConfigID: "",
			AccountName: "acct-branch",
		}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: int64(7), UUID: "pool-migrate"},
			DeploymentName: "cluster-a",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-1",
			},
			KmsConfigID: sql.NullInt64{Valid: false},
		}
		dbNodes := []*datamodel.Node{{Name: "node1", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}}
		svm := &datamodel.Svm{Name: "svm1", BaseModel: datamodel.BaseModel{ID: int64(9)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Twice()
		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(pool, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("CheckVsaKmsConfigReachableActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil)
		env.OnActivity("GetVolumesByPoolID", mock.Anything, mock.Anything).Return([]*datamodel.Volume{}, nil)
		env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(EncryptPoolWorkflow, migrateParams)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})

	t.Run("MigratePathFailsWhenNodeHasNoEndpoint", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		migrateParams := &common.EncryptPoolParams{
			PoolUUID:    "pool-migrate-err",
			KmsConfigID: "",
			AccountName: "acct-branch",
		}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: int64(8), UUID: "pool-migrate-err"},
			DeploymentName: "cluster-a",
			KmsConfigID:    sql.NullInt64{Valid: false},
		}
		// EndpointAddress and HostDNSName both empty -> nodeForPool.EndpointAddressesToHostNameMap becomes empty.
		dbNodes := []*datamodel.Node{{Name: "node1"}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Twice()
		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(pool, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		// No endpoint address is a pool-dependent lookup failure too - marks the pool as Error.
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(EncryptPoolWorkflow, migrateParams)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("MigratePathFailsAfterEkmAssociation_MarksPoolErrorInsteadOfReverting", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		env.SetHeader(newTestEncryptHeader())
		env.RegisterWorkflow(EncryptPoolWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		migrateParams := &common.EncryptPoolParams{
			PoolUUID:    "pool-migrate-partial",
			KmsConfigID: "",
			AccountName: "acct-branch",
		}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: int64(9), UUID: "pool-migrate-partial"},
			DeploymentName: "cluster-a",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-1",
			},
			KmsConfigID: sql.NullInt64{Valid: false},
		}
		dbNodes := []*datamodel.Node{{Name: "node1", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}}
		svm := &datamodel.Svm{Name: "svm1", BaseModel: datamodel.BaseModel{ID: int64(10)}}
		volumes := []*datamodel.Volume{{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "vol1"}}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Twice()
		env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(pool, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("CheckVsaKmsConfigReachableActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil)
		env.OnActivity("GetVolumesByPoolID", mock.Anything, mock.Anything).Return(volumes, nil)
		env.OnActivity("MigrateVsaPoolActivity", mock.Anything, mock.Anything, mock.Anything).
			Return(temporal.NewNonRetryableApplicationError("volume migration failed", "error", nil))
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		// EKM was already associated with the SVM before the volume migration failed, so the pool's
		// actual encryption state may be inconsistent - it must be marked Error, not reverted.
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(EncryptPoolWorkflow, migrateParams)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		env.AssertNotCalled(tt, "UpdatePoolState", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}
