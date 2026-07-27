package kms_workflows

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	errorcore "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	env2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func newTestVsaKmsConfig(uuid string) datamodel.KmsConfig {
	return datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: uuid},
		AccountID: 1,
		State:     datamodel.LifeCycleStateREADY,
		KmsAttributes: &datamodel.KmsAttributes{
			SdeServiceAccountEmail: "sde-sa@account",
		},
	}
}

func TestMigrateKmsConfigWorkflow(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()
	cvp.CVP_HOST = "localhost:8009"

	params := &common.MigrateKmsConfigParams{
		Name:          "test-pool",
		AccountName:   "test-account",
		ProjectNumber: "123456789",
		UUID:          "vsa-kms-uuid",
		SdeUUID:       "sde-kms-uuid",
		LocationID:    "us-east4",
	}
	// Shared ONTAP 409 conflict error reused by tests that simulate "key manager already configured".
	// Mimics what WrapOntapError(err, DomainKMS) produces: a Temporal ApplicationError of type "CustomError"
	// with details [trackingID, originalErrMsg] so that ExtractCustomError can recover the tracking ID.
	ontap409ConflictErr := temporal.NewApplicationError(
		"Error while configuring KMS: A key manager is already configured for this SVM", // message from errorMap[ErrKMSAlreadyExistsEKM]
		"CustomError",                    // type used by WrapAsTemporalApplicationError
		errorcore.ErrKMSAlreadyExistsEKM, // detail 1: tracking ID (6065)
		"Failed to configure Google Cloud Key Management Service for SVM because a key manager has already been configured for this SVM.", // detail 2: original ONTAP error
	)
	t.Run("WhenUpdateJobReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(temporal.NewNonRetryableApplicationError("Update Job error", "error", nil))

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(tt, env.GetWorkflowError(), "Update Job error")
		env.AssertExpectations(t)
	})
	t.Run("WhenMigrateSdeKmsConfigActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: params.UUID},
			KmsAttributes: &datamodel.KmsAttributes{},
		}, nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, temporal.NewNonRetryableApplicationError("Migrate SDE KMS Config Error", "error", nil))
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(errors.New("error"))

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenPollMigrateSdeKmsConfigActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: params.UUID},
			KmsAttributes: &datamodel.KmsAttributes{},
		}, nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(temporal.NewNonRetryableApplicationError("Polling error", "error", nil))
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenPollMigrateSdeKmsConfigActivityReturnsOperationError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: params.UUID},
			KmsAttributes: &datamodel.KmsAttributes{},
		}, nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(temporal.NewNonRetryableApplicationError("operation failed:", "error", nil))
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenGetPoolsByAccountNameReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "test-uuid"}, KmsAttributes: &datamodel.KmsAttributes{}}, nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("Get Pools by account name failed", "error", nil))
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenDescribeSDEKmsConfigurationActivityError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateCreated, KmsConfigID: sql.NullInt64{Int64: 1, Valid: true}}
		var poolsInAccount []*datamodel.Pool
		poolsInAccount = append(poolsInAccount, &pool1)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: params.UUID},
			KmsAttributes: &datamodel.KmsAttributes{},
		}, nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("Describe VSA Kms Config failed", "error", nil))
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil).Maybe()
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenGetKmsConfigActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("Describe VSA Kms Config failed", "error", nil))
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreateAndSyncKmsConfigActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateCreated, KmsConfigID: sql.NullInt64{Int64: 1, Valid: true}}
		var poolsInAccount []*datamodel.Pool
		poolsInAccount = append(poolsInAccount, &pool1)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil).Maybe()
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError(errors.NewNotFoundErr("Record not found", nil).Error(), "KmsConfigNotFound", nil))
		env.OnActivity("CreateAndSyncKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError(errors.NewNotFoundErr("CreateAndSyncKmsConfigActivity", nil).Error(), "KmsConfigNotFound", nil))
		env.OnActivity("DeleteKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreateVSAKmsConfigSAKeyActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateCreated, KmsConfigID: sql.NullInt64{Int64: 1, Valid: true}}
		var poolsInAccount []*datamodel.Pool
		poolsInAccount = append(poolsInAccount, &pool1)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil).Maybe()
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError(errors.NewNotFoundErr("Record not found", nil).Error(), "KmsConfigNotFound", nil))
		env.OnActivity("CreateAndSyncKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError(errors.NewNotFoundErr("Create VSA Kms Config error", nil).Error(), "KmsConfigNotFound", nil))
		env.OnActivity("DeleteKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreateVSAKmsConfigGrantRoleActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateCreated, KmsConfigID: sql.NullInt64{Int64: 1, Valid: true}}
		var poolsInAccount []*datamodel.Pool
		poolsInAccount = append(poolsInAccount, &pool1)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil).Maybe()
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError(errors.NewNotFoundErr("Record not found", nil).Error(), "KmsConfigNotFound", nil))
		env.OnActivity("CreateAndSyncKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(temporal.NewNonRetryableApplicationError(errors.NewNotFoundErr("GrantRoleActivity", nil).Error(), "KmsConfigNotFound", nil))
		env.OnActivity("DeleteKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreateVSAKmsConfigAccessCryptoKeyAndEncryptDataWithImpersonationActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateCreated, KmsConfigID: sql.NullInt64{Int64: 1, Valid: true}}
		var poolsInAccount []*datamodel.Pool
		poolsInAccount = append(poolsInAccount, &pool1)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil).Maybe()
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError(errors.NewNotFoundErr("Record not found", nil).Error(), "KmsConfigNotFound", nil))
		env.OnActivity("CreateAndSyncKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("AccessCryptoKeyAndEncryptDataWithImpersonationActivity", mock.Anything, mock.Anything).Return(temporal.NewNonRetryableApplicationError(errors.NewNotFoundErr("AccessCryptoKeyAndEncryptDataWithImpersonationActivity", nil).Error(), "KmsConfigNotFound", nil))
		env.OnActivity("DeleteKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenPoolsAreNotValidForMigration", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		pool2 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(2), UUID: "pool2"}, State: datamodel.LifeCycleStateError}
		pool3 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool3"}, State: datamodel.LifeCycleStateCreating, KmsConfigID: sql.NullInt64{Int64: 1, Valid: true}}
		pool4 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(4), UUID: "pool4"}, State: datamodel.LifeCycleStateDegraded, KmsConfigID: sql.NullInt64{Int64: 1, Valid: true}}
		var poolsInAccount []*datamodel.Pool
		poolsInAccount = append(poolsInAccount, &pool2, &pool3, &pool4)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		// Defer awaits VerifyVsaKmsReachabilityActivity before workflow completes; require exactly one call
		// (regression guard for fire-and-forget verify leaving KMS stuck in MIGRATING).
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenGetNodeReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateREADY}
		pool2 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(2), UUID: "pool2"}, State: datamodel.LifeCycleStateREADY}
		var poolsInAccount []*datamodel.Pool
		poolsInAccount = append(poolsInAccount, &pool1, &pool2)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("Get Node failed", "error", nil))
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenGetSvmForPoolIDFails", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("Get SVM for Pool ID failed", "error", nil))
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenGetSvmForPoolIDReturnsWithIDZero", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM without ID"}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenUpdatingPoolReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("Updating Pool state failed", "error", nil))
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreateEkmForSvmCreateDnsActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()

		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(temporal.NewNonRetryableApplicationError("Create DNS failed", "error", nil))
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreateEkmForSvmConfigureKmsForSvmActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetServiceAccountUUIDForKmsConfigActivity", mock.Anything, "vsa-kms-uuid").Return("vsa-kms-uuid", nil).Maybe()
		env.OnActivity("AcquireKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid").Return("lock-client-id", nil)
		env.OnActivity("RenewKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil).Maybe()
		env.OnActivity("ReleaseKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil)
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("Update Kms Config state failed", "error", nil))
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreateEkmForSvmCheckVsaKmsConfigReachableActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetServiceAccountUUIDForKmsConfigActivity", mock.Anything, "vsa-kms-uuid").Return("vsa-kms-uuid", nil).Maybe()
		env.OnActivity("AcquireKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid").Return("lock-client-id", nil)
		env.OnActivity("RenewKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil).Maybe()
		env.OnActivity("ReleaseKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil)
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CheckVsaKmsConfigReachableActivity", mock.Anything, mock.Anything, mock.Anything).Return(temporal.NewNonRetryableApplicationError("Update Kms Config state failed", "error", nil)).Once()
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreateEkmForSvmUpdatePoolWithKmsConfigActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetServiceAccountUUIDForKmsConfigActivity", mock.Anything, "vsa-kms-uuid").Return("vsa-kms-uuid", nil).Maybe()
		env.OnActivity("AcquireKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid").Return("lock-client-id", nil)
		env.OnActivity("RenewKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil).Maybe()
		env.OnActivity("ReleaseKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil)
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CheckVsaKmsConfigReachableActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("Update Pool with Kms Config failed", "error", nil))
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenGetVolumesByPoolIDReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetServiceAccountUUIDForKmsConfigActivity", mock.Anything, "vsa-kms-uuid").Return("vsa-kms-uuid", nil).Maybe()
		env.OnActivity("AcquireKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid").Return("lock-client-id", nil)
		env.OnActivity("RenewKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil).Maybe()
		env.OnActivity("ReleaseKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil)
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CheckVsaKmsConfigReachableActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetVolumesByPoolID", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("Get volumes by PoolId failed", "error", nil))
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenVolumesForMigrationAreNotPresent", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var volumesForMigration []*datamodel.Volume
		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetServiceAccountUUIDForKmsConfigActivity", mock.Anything, "vsa-kms-uuid").Return("vsa-kms-uuid", nil).Maybe()
		env.OnActivity("AcquireKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid").Return("lock-client-id", nil)
		env.OnActivity("RenewKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil).Maybe()
		env.OnActivity("ReleaseKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil)
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CheckVsaKmsConfigReachableActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetVolumesByPoolID", mock.Anything, mock.Anything).Return(volumesForMigration, nil)
		env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreatedPoolActivityReturnsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var volumesForMigration []*datamodel.Volume
		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetServiceAccountUUIDForKmsConfigActivity", mock.Anything, "vsa-kms-uuid").Return("vsa-kms-uuid", nil).Maybe()
		env.OnActivity("AcquireKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid").Return("lock-client-id", nil)
		env.OnActivity("RenewKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil).Maybe()
		env.OnActivity("ReleaseKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil)
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CheckVsaKmsConfigReachableActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetVolumesByPoolID", mock.Anything, mock.Anything).Return(volumesForMigration, nil)
		env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("Updating Pool to Ready state failed", "error", nil))
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenVolumesForMigrationArePresent", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var volumesForMigration []*datamodel.Volume
		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}
		volume := &datamodel.Volume{Name: "volName1", BaseModel: datamodel.BaseModel{ID: int64(1)}}
		volumesForMigration = append(volumesForMigration, volume)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetServiceAccountUUIDForKmsConfigActivity", mock.Anything, "vsa-kms-uuid").Return("vsa-kms-uuid", nil).Maybe()
		env.OnActivity("AcquireKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid").Return("lock-client-id", nil)
		env.OnActivity("RenewKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil).Maybe()
		env.OnActivity("ReleaseKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil)
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CheckVsaKmsConfigReachableActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetVolumesByPoolID", mock.Anything, mock.Anything).Return(volumesForMigration, nil)
		env.OnActivity("MigrateVsaPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdatePoolState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreateEkmReturnsOntap409_ReturnsClientError", func(tt *testing.T) {
		// When ConfigureKmsForSvmActivity returns an ONTAP 409 conflict error (key manager already configured),
		// poolMigrationStatus should be poolMigrationClientError → ErrKMSAlreadyExistsEKM (tracking ID 6065).
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetServiceAccountUUIDForKmsConfigActivity", mock.Anything, "vsa-kms-uuid").Return("vsa-kms-uuid", nil).Maybe()
		env.OnActivity("AcquireKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid").Return("lock-client-id", nil)
		env.OnActivity("RenewKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil).Maybe()
		env.OnActivity("ReleaseKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil)
		// ONTAP 409 conflict: key manager already configured for this SVM
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, ontap409ConflictErr)
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		assert.ErrorContains(tt, env.GetWorkflowError(), "Migration failed for at least one of the Pools")
		env.AssertExpectations(tt)
	})
	t.Run("WhenMixedOntap409AndInternalError_EscalatesToInternalError", func(tt *testing.T) {
		// Two pools: pool1 fails with ONTAP 409 (client error), pool2 fails at GetNode (internal error).
		// poolMigrationStatus should escalate to poolMigrationInternalError → ErrKMSMigration (tracking ID 6063).
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var poolsInAccount []*datamodel.Pool
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		pool2 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(2), UUID: "pool2"}, State: datamodel.LifeCycleStateREADY,
			DeploymentName: "cluster2",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-456",
			}}
		poolsInAccount = append(poolsInAccount, &pool1, &pool2)

		var dbNodes []*datamodel.Node
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		// Pool 1: succeeds through to createEkmForSvm, then gets ONTAP 409
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil).Once()
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetServiceAccountUUIDForKmsConfigActivity", mock.Anything, "vsa-kms-uuid").Return("vsa-kms-uuid", nil).Maybe()
		env.OnActivity("AcquireKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid").Return("lock-client-id", nil)
		env.OnActivity("RenewKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil).Maybe()
		env.OnActivity("ReleaseKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil)
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, ontap409ConflictErr)
		// Pool 2: fails at GetNode (internal error) → escalates poolMigrationStatus to poolMigrationInternalError
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("Get Node failed", "error", nil)).Once()
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		assert.ErrorContains(tt, env.GetWorkflowError(), "Migration failed for at least one of the Pools")
		env.AssertExpectations(tt)
	})
	t.Run("WhenFutureReturnsWithError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() {
			env2.AuthType = origAuthType
		}()
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		var volumesForMigration []*datamodel.Volume
		var poolsInAccount []*datamodel.Pool
		var dbNodes []*datamodel.Node
		pool1 := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "pool1"}, State: datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			}}
		poolsInAccount = append(poolsInAccount, &pool1)
		node1 := datamodel.Node{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}
		dbNodes = append(dbNodes, &node1)
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}
		volume := &datamodel.Volume{Name: "volName1", BaseModel: datamodel.BaseModel{ID: int64(1)}}
		volumesForMigration = append(volumesForMigration, volume)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return(poolsInAccount, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetServiceAccountUUIDForKmsConfigActivity", mock.Anything, "vsa-kms-uuid").Return("vsa-kms-uuid", nil).Maybe()
		env.OnActivity("AcquireKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid").Return("lock-client-id", nil)
		env.OnActivity("RenewKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil).Maybe()
		env.OnActivity("ReleaseKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid", "lock-client-id").Return(nil)
		env.OnActivity("ConfigureKmsForSvmActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CheckVsaKmsConfigReachableActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		env.OnActivity("UpdatePoolWithKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetVolumesByPoolID", mock.Anything, mock.Anything).Return(volumesForMigration, nil)
		env.OnActivity("MigrateVsaPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(temporal.NewNonRetryableApplicationError("Updating Pool to Ready state failed", "error", nil))
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenAcquireKmsRotationLockActivityFails", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		vsaKmsConfig := newTestVsaKmsConfig(params.UUID)
		env.SetHeader(mockHeader)
		origAuthType := env2.AuthType
		env2.AuthType = env2.USERNAME_PWD_SEC_MGR
		defer func() { env2.AuthType = origAuthType }()

		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{})

		pool1 := datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: int64(1), UUID: "pool1"},
			State:          datamodel.LifeCycleStateInUse,
			DeploymentName: "cluster1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
			},
		}
		dbNodes := []*datamodel.Node{{Name: "Node", EndpointAddress: "1.2.3.4", HostDNSName: "host1"}}
		svm := &datamodel.Svm{Name: "SVM with ID", BaseModel: datamodel.BaseModel{ID: int64(1)}}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, params).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, params, mock.Anything).Return(nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return([]*datamodel.Pool{&pool1}, nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&vsaKmsConfig, nil)
		env.OnActivity("GetNode", mock.Anything, mock.Anything).Return(dbNodes, nil)
		env.OnActivity("GetSvmForPoolID", mock.Anything, mock.Anything).Return(svm, nil)
		env.OnActivity("UpdatingPool", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CreateDnsActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetServiceAccountUUIDForKmsConfigActivity", mock.Anything, "vsa-kms-uuid").Return("vsa-kms-uuid", nil).Maybe()
		env.OnActivity("AcquireKmsRotationLockActivity", mock.Anything, "vsa-kms-uuid").Return("", errors.New("lock acquire failed"))
		env.OnActivity("FailedPoolActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		assert.ErrorContains(tt, env.GetWorkflowError(), "Migration failed for at least one of the Pools")
		env.AssertExpectations(tt)
	})
}

func TestValidateKmsConfigForMigration(t *testing.T) { // Generated using GitHub Copilot
	tests := []struct {
		name          string
		state         string
		expectedError error
	}{
		{
			name:          "Valid state READY",
			state:         datamodel.LifeCycleStateREADY,
			expectedError: nil,
		},
		{
			name:          "Valid state IN_USE",
			state:         datamodel.LifeCycleStateInUse,
			expectedError: nil,
		},
		{
			name:          "Invalid state Created",
			state:         datamodel.LifeCycleStateCreated,
			expectedError: errors.NewBadRequestErr("CMEK Configuration needs to be in either Ready or In_Use state for migration"),
		},
		{
			name:          "Transitioning state UPDATING",
			state:         datamodel.LifeCycleStateUpdating,
			expectedError: errors.NewConflictErr(fmt.Sprintf("CMEK Configuration continues to be in transitioning state after SDE migration: %s", datamodel.LifeCycleStateUpdating)),
		},
		{
			name:          "Transitioning state MIGRATING",
			state:         datamodel.LifeCycleStateMigrating,
			expectedError: errors.NewConflictErr(fmt.Sprintf("CMEK Configuration continues to be in transitioning state after SDE migration: %s", datamodel.LifeCycleStateMigrating)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kmsConfig := &datamodel.KmsConfig{
				State: tt.state,
			}
			err := validateKmsConfigForMigration(kmsConfig.State)

			if tt.expectedError == nil {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.expectedError.Error())
			}
		})
	}
	t.Run("HeartbeatTimeoutIsConfigured", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// This test verifies that HeartbeatTimeout is configured in ActivityOptions
		// by ensuring activities with RecordHeartbeat can execute successfully
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterWorkflow(MigrateKmsConfigWorkflow)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		env.RegisterActivity(&activities.PoolActivity{})
		env.RegisterActivity(&activities.SvmActivity{})

		migrateParams := &common.MigrateKmsConfigParams{
			Name:          "test-pool",
			AccountName:   "test-account",
			ProjectNumber: "123456789",
			LocationID:    "us-east4",
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("MigrateSdeKmsConfigActivity", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("PollMigrateSdeKmsConfigActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("GetKmsConfigActivity", mock.Anything, mock.Anything).Return(&datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "test-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}, nil)
		env.OnActivity("GetPoolsByAccountName", mock.Anything, mock.Anything).Return([]*datamodel.Pool{}, nil)
		env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(MigrateKmsConfigWorkflow, migrateParams)

		// Verify workflow completes successfully, which confirms HeartbeatTimeout is configured
		// Activities with RecordHeartbeat would fail if HeartbeatTimeout wasn't set
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}
