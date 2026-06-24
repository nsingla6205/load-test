package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	ontapmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const cvpHost = "sde.example.com"

func TestCreateActiveDirectoryWorkflow(t *testing.T) {
	t.Run("VcpSuccess", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		adCreateActivity := active_directory_activities.ActiveDirectoryCreateActivity{SE: mockStorage}
		env.RegisterActivity(adCreateActivity.CreateVcpActiveDirectory)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &common.CreateActiveDirectoryParams{
			AccountId:          "123",
			ResourceId:         "test-ad",
			Domain:             "test.example.com",
			DNS:                "8.8.8.8",
			NetBIOS:            "TESTNETBIOS",
			OrganizationalUnit: "OU=test",
			Username:           "admin",
			Password:           "password123",
		}
		adRecord := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "test-ad-uuid-123",
			},
			AccountId: 123,
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		env.OnActivity(adCreateActivity.CreateVcpActiveDirectory, mock.Anything, params, adRecord).Return(nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(CreateActiveDirectoryWorkflow, params, adRecord)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SdeSuccess", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		adCreateActivity := active_directory_activities.ActiveDirectoryCreateActivity{SE: mockStorage}
		env.RegisterActivity(adCreateActivity.CreateSdeActiveDirectory)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		params := &common.CreateActiveDirectoryParams{
			AccountId:          "123",
			ResourceId:         "test-ad",
			Domain:             "test.example.com",
			DNS:                "8.8.8.8",
			NetBIOS:            "TESTNETBIOS",
			OrganizationalUnit: "OU=test",
			Username:           "admin",
			Password:           "password123",
		}
		adRecord := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "test-ad-uuid-456",
			},
			AccountId: 456,
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity(adCreateActivity.CreateSdeActiveDirectory, mock.Anything, params).Return(nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(CreateActiveDirectoryWorkflow, params, adRecord)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("VcpActivityFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		adCreateActivity := active_directory_activities.ActiveDirectoryCreateActivity{SE: mockStorage}
		env.RegisterActivity(adCreateActivity.CreateVcpActiveDirectory)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &common.CreateActiveDirectoryParams{
			AccountId:          "123",
			ResourceId:         "test-ad",
			Domain:             "test.example.com",
			DNS:                "8.8.8.8",
			NetBIOS:            "TESTNETBIOS",
			OrganizationalUnit: "OU=test",
			Username:           "admin",
			Password:           "password123",
		}

		adRecord := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "test-ad-uuid-fail",
			},
			AccountId: 789,
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		expectedError := vsaerrors.New("Error")
		env.OnActivity(adCreateActivity.CreateVcpActiveDirectory, mock.Anything, params, adRecord).Return(expectedError)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(CreateActiveDirectoryWorkflow, params, adRecord)
	})

	t.Run("SdeActivityFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		adCreateActivity := active_directory_activities.ActiveDirectoryCreateActivity{SE: mockStorage}
		env.RegisterActivity(adCreateActivity.CreateSdeActiveDirectory)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		params := &common.CreateActiveDirectoryParams{
			AccountId:          "123",
			ResourceId:         "test-ad",
			Domain:             "test.example.com",
			DNS:                "8.8.8.8",
			NetBIOS:            "TESTNETBIOS",
			OrganizationalUnit: "OU=test",
			Username:           "admin",
			Password:           "password123",
		}
		adRecord := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "test-ad-uuid-sde-fail",
			},
			AccountId: 999,
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() { cvp.CVP_HOST = originalHost }()

		expectedError := vsaerrors.New("Error")
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity(adCreateActivity.CreateSdeActiveDirectory, mock.Anything, params).Return(expectedError)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(CreateActiveDirectoryWorkflow, params, adRecord)
	})

	t.Run("SetupFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		// Pass invalid params to cause setup failure
		invalidParams := &common.CreateActiveDirectoryParams{}
		adRecord := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "test-ad-uuid",
			},
			AccountId: 123,
		}

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(CreateActiveDirectoryWorkflow, invalidParams, adRecord)
	})

	t.Run("UpdateJobStatusFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryCreateActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &common.CreateActiveDirectoryParams{
			AccountId:  "123",
			ResourceId: "test-ad",
		}
		adRecord := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "test-ad-uuid",
			},
			AccountId: 123,
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(errors.New("job status update failed"))

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(CreateActiveDirectoryWorkflow, params, adRecord)
	})
}

func TestActiveDirectoryCreateWorkflow_Setup(t *testing.T) {
	t.Run("SuccessfulSetup", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		params := &common.CreateActiveDirectoryParams{
			AccountId:  "123",
			ResourceId: "test-ad",
		}

		var setupErr error
		var wf *ActiveDirectoryCreateWorkflow
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf = &ActiveDirectoryCreateWorkflow{}
			setupErr = wf.Setup(ctx, params)
			return setupErr
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf = &ActiveDirectoryCreateWorkflow{}
			setupErr = wf.Setup(ctx, params)
			return setupErr
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, setupErr)
		assert.NoError(t, env.GetWorkflowError())
		assert.Equal(t, WorkflowStatusCreated, wf.Status)
		assert.Equal(t, "123", wf.CustomerID)
		assert.Equal(t, "default-test-workflow-id", wf.ID)
	})

	t.Run("QueryHandlerTest", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		params := &common.CreateActiveDirectoryParams{
			AccountId:  "456",
			ResourceId: "test-ad-query",
		}

		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryCreateWorkflow{}
			err := wf.Setup(ctx, params)
			if err != nil {
				return err
			}
			wf.Status = WorkflowStatusRunning
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryCreateWorkflow{}
			err := wf.Setup(ctx, params)
			if err != nil {
				return err
			}
			wf.Status = WorkflowStatusRunning
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())

		queryResult, err := env.QueryWorkflow("status")
		assert.NoError(t, err)
		assert.NotNil(t, queryResult)

		var status WorkflowStatus
		err = queryResult.Get(&status)
		assert.NoError(t, err)
		assert.Equal(t, "456", status.CustomerID)
		assert.Equal(t, "default-test-workflow-id", status.ID)
	})
}

func TestActiveDirectoryCreateWorkflow_Run(t *testing.T) {
	t.Run("SuccessfulVcpRun", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryCreateActivity{})

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		params := &common.CreateActiveDirectoryParams{
			AccountId:  "123",
			ResourceId: "test-ad",
		}
		adRecord := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
			},
			AccountId: 123,
		}

		env.OnActivity("CreateVcpActiveDirectory", mock.Anything, params, adRecord).Return(nil)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryCreateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryCreateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		assert.Nil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("SuccessfulSdeRun", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryCreateActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		params := &common.CreateActiveDirectoryParams{
			AccountId:  "456",
			ResourceId: "test-ad-sde",
		}
		adRecord := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid-sde",
			},
			AccountId: 456,
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity("CreateSdeActiveDirectory", mock.Anything, params).Return(nil)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryCreateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryCreateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		assert.Nil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("ActivityExecutionFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryCreateActivity{})

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		params := &common.CreateActiveDirectoryParams{
			AccountId:  "789",
			ResourceId: "test-ad-activity-fail",
		}
		adRecord := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid-activity-fail",
			},
			AccountId: 789,
		}

		activityError := vsaerrors.New("activity execution failed")
		env.OnActivity("CreateVcpActiveDirectory", mock.Anything, params, adRecord).Return(activityError)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryCreateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryCreateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.NotNil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})
}

// TestUpdateActiveDirectoryWorkflow tests the UpdateActiveDirectoryWorkflow function
func TestUpdateActiveDirectoryWorkflow(t *testing.T) {
	t.Run("VcpSuccess", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})

		oldAd := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 456}, // Add ID field
			AdName:    "test-ad",
			Domain:    "example.com",
		}

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		// Mock Setup will be called by the workflow
		env.OnActivity("GetSvmsForAd", mock.Anything, int64(456)).Return([]*datamodel.Svm{}, nil)
		env.OnActivity("GetActiveDirectoryStateFromSVMUsage", mock.Anything, int64(456)).Return(common.ActiveDirectoryStateResult{State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateReadyDetails}, nil)
		env.OnActivity("UpdateVcpActiveDirectory", mock.Anything, params, oldAd, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, oldAd)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, oldAd)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Nil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("SdeSuccess", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		adUpdateActivity := active_directory_activities.ActiveDirectoryUpdateActivity{SE: mockStorage}
		env.RegisterActivity(adUpdateActivity.UpdateSdeActiveDirectory)
		env.RegisterActivity(adUpdateActivity.PollSdeUpdateActivity)
		env.RegisterActivity(adUpdateActivity.MarkVcpAdToUpdatingActivity)
		env.RegisterActivity(adUpdateActivity.UpdateVcpActiveDirectory)

		adActivity := active_directory_activities.ActiveDirectoryActivity{SE: mockStorage}
		env.RegisterActivity(adActivity.GetActiveDirectoryStateFromSVMUsage)
		env.RegisterActivity(adActivity.GetSvmsForAd)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		params := &common.UpdateActiveDirectoryParams{
			AccountId: "456",
			Domain:    &[]string{"sde-updated.example.com"}[0],
		}
		adRecord := &models.ActiveDirectory{
			BaseModel: models.BaseModel{
				ID:   2,
				UUID: "test-ad-uuid-update-sde-456",
			},
			State: "available",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		sdeResult := &cvpModels.OperationV1beta{
			Name: "update-ad-operation",
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity(adUpdateActivity.UpdateSdeActiveDirectory, mock.Anything, params).Return(sdeResult, nil)
		env.OnActivity(adUpdateActivity.PollSdeUpdateActivity, mock.Anything, params, sdeResult).Return(nil)
		env.OnActivity(adUpdateActivity.MarkVcpAdToUpdatingActivity, mock.Anything, params, adRecord).Return(nil)
		env.OnActivity(adActivity.GetActiveDirectoryStateFromSVMUsage, mock.Anything, int64(2)).Return(common.ActiveDirectoryStateResult{State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateReadyDetails}, nil)
		env.OnActivity(adUpdateActivity.UpdateVcpActiveDirectory, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(adActivity.GetSvmsForAd, mock.Anything, mock.Anything).Return([]*datamodel.Svm{}, nil)

		env.ExecuteWorkflow(UpdateActiveDirectoryWorkflow, params, adRecord)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_SetupError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		oldAd := &models.ActiveDirectory{
			AdName: "test-ad",
			Domain: "example.com",
		}

		env.ExecuteWorkflow(UpdateActiveDirectoryWorkflow, params, oldAd)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
	})

	t.Run("Failure_SdeUpdateError", func(t *testing.T) {
		originalCvpHost := cvp.CVP_HOST
		cvp.CVP_HOST = "sde.example.com"
		defer func() { cvp.CVP_HOST = originalCvpHost }()

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		adUpdateActivity := active_directory_activities.ActiveDirectoryUpdateActivity{SE: mockStorage}
		env.RegisterActivity(adUpdateActivity.UpdateSdeActiveDirectory)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		oldAd := &models.ActiveDirectory{
			AdName: "test-ad-sde-error",
			Domain: "example.com",
		}

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity(adUpdateActivity.UpdateSdeActiveDirectory, mock.Anything, params).Return(nil, vsaerrors.New("SDE update failed"))
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(UpdateActiveDirectoryWorkflow, params, oldAd)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_SdePollingError", func(t *testing.T) {
		originalCvpHost := cvp.CVP_HOST
		cvp.CVP_HOST = "sde.example.com"
		defer func() { cvp.CVP_HOST = originalCvpHost }()

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		adUpdateActivity := active_directory_activities.ActiveDirectoryUpdateActivity{SE: mockStorage}
		env.RegisterActivity(adUpdateActivity.UpdateSdeActiveDirectory)
		env.RegisterActivity(adUpdateActivity.PollSdeUpdateActivity)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		oldAd := &models.ActiveDirectory{
			AdName: "test-ad-poll-error",
			Domain: "example.com",
		}

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		sdeResult := &cvpModels.OperationV1beta{
			Name: "operations/op-123",
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity(adUpdateActivity.UpdateSdeActiveDirectory, mock.Anything, params).Return(sdeResult, nil)
		env.OnActivity(adUpdateActivity.PollSdeUpdateActivity, mock.Anything, params, sdeResult).Return(vsaerrors.New("polling failed"))
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(UpdateActiveDirectoryWorkflow, params, oldAd)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_VcpUpdateError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		adUpdateActivity := active_directory_activities.ActiveDirectoryUpdateActivity{SE: mockStorage}
		env.RegisterActivity(adUpdateActivity.UpdateVcpActiveDirectory)
		env.RegisterActivity(adUpdateActivity.MarkVcpAdToErrorActivity)

		adActivity := active_directory_activities.ActiveDirectoryActivity{SE: mockStorage}
		env.RegisterActivity(adActivity.GetActiveDirectoryStateFromSVMUsage)
		env.RegisterActivity(adActivity.GetSvmsForAd)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		oldAd := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 123}, // Add ID field
			AdName:    "test-ad-vcp-error",
			Domain:    "example.com",
		}

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		env.OnActivity(adActivity.GetSvmsForAd, mock.Anything, int64(123)).Return([]*datamodel.Svm{}, nil)
		env.OnActivity(adActivity.GetActiveDirectoryStateFromSVMUsage, mock.Anything, int64(123)).Return(common.ActiveDirectoryStateResult{State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateReadyDetails}, nil)
		env.OnActivity(adUpdateActivity.UpdateVcpActiveDirectory, mock.Anything, params, oldAd, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(vsaerrors.New("VCP update failed"))
		env.OnActivity(adUpdateActivity.MarkVcpAdToErrorActivity, mock.Anything, params, oldAd).Return(nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(UpdateActiveDirectoryWorkflow, params, oldAd)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("VcpPath_GetActiveDirectoryStateFromSVMUsageFails_WorkflowFails", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		oldAd := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 321},
			AdName:    "test-ad",
			Domain:    "example.com",
		}
		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
		}

		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSvmsForAd", mock.Anything, int64(321)).Return([]*datamodel.Svm{}, nil)
		env.OnActivity("GetActiveDirectoryStateFromSVMUsage", mock.Anything, int64(321)).
			Return(common.ActiveDirectoryStateResult{}, vsaerrors.New("state fetch failed"))

		env.ExecuteWorkflow(UpdateActiveDirectoryWorkflow, params, oldAd)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_AuthTokenError", func(t *testing.T) {
		originalCvpHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() { cvp.CVP_HOST = originalCvpHost }()

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		oldAd := &models.ActiveDirectory{
			AdName: "test-ad-auth-fail",
			Domain: "example.com",
		}

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("", vsaerrors.New("auth token error"))
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(UpdateActiveDirectoryWorkflow, params, oldAd)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_NilSdeResult", func(t *testing.T) {
		originalCvpHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() { cvp.CVP_HOST = originalCvpHost }()

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		adUpdateActivity := active_directory_activities.ActiveDirectoryUpdateActivity{SE: mockStorage}
		env.RegisterActivity(adUpdateActivity.UpdateSdeActiveDirectory)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		oldAd := &models.ActiveDirectory{
			AdName: "test-ad-nil-result",
			Domain: "example.com",
		}

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity(adUpdateActivity.UpdateSdeActiveDirectory, mock.Anything, params).Return(nil, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(UpdateActiveDirectoryWorkflow, params, oldAd)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_MarkVcpAdToUpdatingError", func(t *testing.T) {
		originalCvpHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() { cvp.CVP_HOST = originalCvpHost }()

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		adUpdateActivity := active_directory_activities.ActiveDirectoryUpdateActivity{SE: mockStorage}
		env.RegisterActivity(adUpdateActivity.UpdateSdeActiveDirectory)
		env.RegisterActivity(adUpdateActivity.PollSdeUpdateActivity)
		env.RegisterActivity(adUpdateActivity.MarkVcpAdToUpdatingActivity)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		oldAd := &models.ActiveDirectory{
			AdName: "test-ad-mark-fail",
			Domain: "example.com",
		}

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
		}

		sdeResult := &cvpModels.OperationV1beta{Name: "operations/mark-update-fail"}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity(adUpdateActivity.UpdateSdeActiveDirectory, mock.Anything, params).Return(sdeResult, nil)
		env.OnActivity(adUpdateActivity.PollSdeUpdateActivity, mock.Anything, params, sdeResult).Return(nil)
		env.OnActivity(adUpdateActivity.MarkVcpAdToUpdatingActivity, mock.Anything, params, oldAd).Return(vsaerrors.New("failed to mark updating"))
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(UpdateActiveDirectoryWorkflow, params, oldAd)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_SdePollingError_CVPErrorPreserved", func(t *testing.T) {
		originalCvpHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() { cvp.CVP_HOST = originalCvpHost }()

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		adUpdateActivity := active_directory_activities.ActiveDirectoryUpdateActivity{SE: mockStorage}
		env.RegisterActivity(adUpdateActivity.UpdateSdeActiveDirectory)
		env.RegisterActivity(adUpdateActivity.PollSdeUpdateActivity)

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		oldAd := &models.ActiveDirectory{
			AdName: "test-ad-cvp-error",
			Domain: "example.com",
		}

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
		}

		sdeResult := &cvpModels.OperationV1beta{Name: "operations/cvp-error-propagation"}

		cvpErr := temporal.NewNonRetryableApplicationError(
			"An internal error occurred",
			vsaerrors.CustomErrorType,
			nil,
			vsaerrors.ErrCVPInternalServerError,
			"Failed to modify the CIFS server. Reason: SecD Error: no server available.",
		)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity(adUpdateActivity.UpdateSdeActiveDirectory, mock.Anything, params).Return(sdeResult, nil)
		env.OnActivity(adUpdateActivity.PollSdeUpdateActivity, mock.Anything, params, sdeResult).Return(cvpErr)
		// First call: PROCESSING status
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStatePROCESSING)
		})).Return(nil).Once()
		// Second call: ERROR status with CVP error details preserved
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStateERROR) &&
				job.TrackingID == vsaerrors.ErrCVPInternalServerError &&
				job.ErrorDetails == "Failed to modify the CIFS server. Reason: SecD Error: no server available."
		})).Return(nil).Once()

		env.ExecuteWorkflow(UpdateActiveDirectoryWorkflow, params, oldAd)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("VcpPath_StateActivityReturnsInUse_UpdateVcpCalledWithInUse", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		oldAd := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 555},
			AdName:    "test-ad",
			Domain:    "example.com",
		}
		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
		}

		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSvmsForAd", mock.Anything, int64(555)).Return([]*datamodel.Svm{}, nil)
		env.OnActivity("GetActiveDirectoryStateFromSVMUsage", mock.Anything, int64(555)).
			Return(common.ActiveDirectoryStateResult{State: datamodel.LifeCycleStateInUse, StateDetails: datamodel.LifeCycleStateInUseDetails}, nil)
		env.OnActivity("UpdateVcpActiveDirectory", mock.Anything, params, oldAd, mock.AnythingOfType("string"), datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails).Return(nil)

		env.ExecuteWorkflow(UpdateActiveDirectoryWorkflow, params, oldAd)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

// TestActiveDirectoryUpdateWorkflow_Setup tests the Setup method
func TestActiveDirectoryUpdateWorkflow_Setup(t *testing.T) {
	t.Run("SuccessfulSetup", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		wf := &ActiveDirectoryUpdateWorkflow{}

		env.RegisterWorkflow(func(ctx workflow.Context) error {
			return wf.Setup(ctx, params)
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return wf.Setup(ctx, params)
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		assert.NotEmpty(t, wf.ID)
		assert.Equal(t, "test-account", wf.CustomerID)
		assert.Equal(t, WorkflowStatusCreated, wf.Status)
		assert.NotNil(t, wf.Logger)
	})

	t.Run("QueryHandlerTest", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		params := &common.CreateActiveDirectoryParams{
			AccountId:  "456",
			ResourceId: "test-ad-query",
		}

		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryCreateWorkflow{}
			err := wf.Setup(ctx, params)
			if err != nil {
				return err
			}
			wf.Status = WorkflowStatusRunning
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryCreateWorkflow{}
			err := wf.Setup(ctx, params)
			if err != nil {
				return err
			}
			wf.Status = WorkflowStatusRunning
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())

		queryResult, err := env.QueryWorkflow("status")
		assert.NoError(t, err)
		assert.NotNil(t, queryResult)

		var status WorkflowStatus
		err = queryResult.Get(&status)
		assert.NoError(t, err)
		assert.Equal(t, "456", status.CustomerID)
		assert.Equal(t, "default-test-workflow-id", status.ID)
	})
}

// TestActiveDirectoryUpdateWorkflow_Run tests the Run method
func TestActiveDirectoryUpdateWorkflow_Run(t *testing.T) {
	t.Run("SuccessfulVcpOnlyRun", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})

		oldAd := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 789}, // Add ID
			AdName:    "test-ad",
			Domain:    "example.com",
		}

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		env.OnActivity("GetSvmsForAd", mock.Anything, int64(789)).Return([]*datamodel.Svm{}, nil)
		env.OnActivity("GetActiveDirectoryStateFromSVMUsage", mock.Anything, int64(789)).Return(common.ActiveDirectoryStateResult{State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateReadyDetails}, nil)
		env.OnActivity("UpdateVcpActiveDirectory", mock.Anything, params, oldAd, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)

		wf := &ActiveDirectoryUpdateWorkflow{}
		var result interface{}
		var err *vsaerrors.CustomError

		env.RegisterWorkflow(func(ctx workflow.Context) error {
			result, err = wf.Run(ctx, params, oldAd)
			if err != nil {
				return err
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			result, err = wf.Run(ctx, params, oldAd)
			if err != nil {
				return err
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		assert.Nil(t, err)
		assert.Nil(t, result)
		env.AssertExpectations(t)
	})

	t.Run("SuccessfulSdeRun", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		params := &common.UpdateActiveDirectoryParams{
			AccountId: "456",
		}
		adRecord := &models.ActiveDirectory{
			BaseModel: models.BaseModel{
				ID:   2,
				UUID: "test-uuid-sde-run",
			},
			State: "available",
		}

		sdeResult := &cvpModels.OperationV1beta{
			Name: "update-ad-sde-operation",
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity("UpdateSdeActiveDirectory", mock.Anything, params).Return(sdeResult, nil)
		env.OnActivity("PollSdeUpdateActivity", mock.Anything, params, sdeResult).Return(nil)
		env.OnActivity("MarkVcpAdToUpdatingActivity", mock.Anything, params, adRecord).Return(nil)
		env.OnActivity("GetActiveDirectoryStateFromSVMUsage", mock.Anything, int64(2)).Return(common.ActiveDirectoryStateResult{State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateReadyDetails}, nil)
		env.OnActivity("UpdateVcpActiveDirectory", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSvmsForAd", mock.Anything, int64(2)).Return([]*datamodel.Svm{}, nil)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		assert.Nil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("VcpUpdateExecutionFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		params := &common.UpdateActiveDirectoryParams{
			AccountId: "789",
		}
		adRecord := &models.ActiveDirectory{
			BaseModel: models.BaseModel{
				ID:   3,
				UUID: "test-uuid-vcp-run-fail",
			},
			State: "available",
		}

		activityError := vsaerrors.New("VCP update execution failed")
		env.OnActivity("GetSvmsForAd", mock.Anything, int64(3)).Return(nil, activityError)
		env.OnActivity("MarkVcpAdToErrorActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.NotNil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("SdeResultNilFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		params := &common.UpdateActiveDirectoryParams{
			AccountId: "999",
		}
		adRecord := &models.ActiveDirectory{
			BaseModel: models.BaseModel{
				ID:   4,
				UUID: "test-uuid-nil-result",
			},
			State: "available",
		}

		// Return nil result from SDE update
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity("UpdateSdeActiveDirectory", mock.Anything, params).Return((*cvpModels.OperationV1beta)(nil), nil)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.NotNil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("InsufficientArguments", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)

		params := &common.UpdateActiveDirectoryParams{
			AccountId: "test-account",
		}

		var runErr *vsaerrors.CustomError
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			_, runErr = wf.Run(ctx, params)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.NotNil(t, runErr)
	})

	t.Run("SdeUpdateActivityFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		params := &common.UpdateActiveDirectoryParams{
			AccountId: "test-account",
		}
		adRecord := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 5, UUID: "test-uuid-sde-fail"},
			State:     "available",
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity("UpdateSdeActiveDirectory", mock.Anything, params).Return(nil, vsaerrors.New("SDE update call failed"))

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.NotNil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("SdeAuthTokenFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		params := &common.UpdateActiveDirectoryParams{
			AccountId: "test-account",
		}
		adRecord := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 6, UUID: "test-uuid-auth-fail"},
			State:     "available",
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("", vsaerrors.New("auth token failure"))

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.NotNil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("SdePollingFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		params := &common.UpdateActiveDirectoryParams{
			AccountId: "test-account",
		}
		adRecord := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 7, UUID: "test-uuid-poll-fail"},
			State:     "available",
		}

		sdeResult := &cvpModels.OperationV1beta{Name: "operations/poll-fail-op"}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity("UpdateSdeActiveDirectory", mock.Anything, params).Return(sdeResult, nil)
		env.OnActivity("PollSdeUpdateActivity", mock.Anything, params, sdeResult).Return(vsaerrors.New("polling failed"))

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.NotNil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("SdePollingFailure_WithCVPError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		params := &common.UpdateActiveDirectoryParams{
			AccountId: "test-account",
		}
		adRecord := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 8, UUID: "test-uuid-cvp-poll-fail"},
			State:     "available",
		}

		sdeResult := &cvpModels.OperationV1beta{Name: "operations/cvp-error-op"}

		cvpErr := temporal.NewNonRetryableApplicationError(
			"An internal error occurred",
			vsaerrors.CustomErrorType,
			nil,
			vsaerrors.ErrCVPInternalServerError,
			"Failed to modify the CIFS server. Reason: SecD Error: no server available.",
		)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity("UpdateSdeActiveDirectory", mock.Anything, params).Return(sdeResult, nil)
		env.OnActivity("PollSdeUpdateActivity", mock.Anything, params, sdeResult).Return(cvpErr)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		require.NotNil(t, runErr)
		assert.True(t, vsaerrors.IsCVPError(runErr.TrackingID),
			"expected CVP error range (14200-14399), got tracking ID %d", runErr.TrackingID)
		assert.Equal(t, vsaerrors.ErrCVPInternalServerError, runErr.TrackingID)
		assert.Contains(t, runErr.OriginalErr.Error(), "Failed to modify the CIFS server")
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("SdeMarkVcpAdToUpdatingFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		params := &common.UpdateActiveDirectoryParams{
			AccountId: "test-account",
		}
		adRecord := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 9, UUID: "test-uuid-mark-fail"},
			State:     "available",
		}

		sdeResult := &cvpModels.OperationV1beta{Name: "operations/mark-fail-op"}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity("UpdateSdeActiveDirectory", mock.Anything, params).Return(sdeResult, nil)
		env.OnActivity("PollSdeUpdateActivity", mock.Anything, params, sdeResult).Return(nil)
		env.OnActivity("MarkVcpAdToUpdatingActivity", mock.Anything, params, adRecord).Return(vsaerrors.New("failed to mark as updating"))

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.NotNil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("SdeVcpUpdateFailureAfterPoll", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		params := &common.UpdateActiveDirectoryParams{
			AccountId: "test-account",
		}
		adRecord := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 10, UUID: "test-uuid-vcp-fail-after-poll"},
			State:     "available",
		}

		sdeResult := &cvpModels.OperationV1beta{Name: "operations/vcp-fail-op"}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.AccountId).Return("test-jwt-token", nil)
		env.OnActivity("UpdateSdeActiveDirectory", mock.Anything, params).Return(sdeResult, nil)
		env.OnActivity("PollSdeUpdateActivity", mock.Anything, params, sdeResult).Return(nil)
		env.OnActivity("MarkVcpAdToUpdatingActivity", mock.Anything, params, adRecord).Return(nil)
		env.OnActivity("GetSvmsForAd", mock.Anything, int64(10)).Return(nil, vsaerrors.New("failed to get SVMs"))
		env.OnActivity("MarkVcpAdToErrorActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryUpdateWorkflow{}
			runResult, runErr = wf.Run(ctx, params, adRecord)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.NotNil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})
}

// TestPushAdUpdatesToSVMWorkflow tests the PushAdUpdatesToSVMWorkflow function
func TestPushAdUpdatesToSVMWorkflow(t *testing.T) {
	t.Run("SuccessfulWithNoSvms", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})

		oldAd := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 100}, // Add ID
			AdName:    "test-ad-no-svms",
			Domain:    "example.com",
		}

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		changeId := "change-id-123"

		env.OnActivity("GetSvmsForAd", mock.Anything, int64(100)).Return([]*datamodel.Svm{}, nil)

		var result error
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			result = PushAdUpdatesToSVMWorkflow(ctx, oldAd, params, changeId)
			return result
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PushAdUpdatesToSVMWorkflow(ctx, oldAd, params, changeId)
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, result)
		env.AssertExpectations(t)
	})

	t.Run("SuccessfulWithSingleSvm", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		env.RegisterActivity(&activities.CommonActivities{})

		oldAd := models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 200}, // Add ID
			AdName:    "test-ad-single",
			Domain:    "example.com",
		}

		params := common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		changeId := "change-id-single"

		svms := []*datamodel.Svm{
			{
				Name:   "svm-1",
				PoolID: int64(1),
				SvmDetails: &datamodel.SvmDetails{
					ExternalUUID: "svm-uuid-1",
				},
			},
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: int64(1),
			},
			DeploymentName: "test-deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
			},
		}

		nodes := []*datamodel.Node{
			{
				BaseModel: datamodel.BaseModel{
					ID: int64(1),
				},
				EndpointAddress: "10.0.0.1",
			},
		}

		cifs := ontapRest.CifsService{
			CifsService: ontapmodels.CifsService{
				Name: nillable.GetStringPtr("cifs-1"),
			},
		}

		updateParams := vsa.UpdateActiveDirectoryCredentialsParams{
			NewCredentials: &vsa.ActiveDirectory{
				Password: "new-password",
			},
		}

		env.OnActivity("GetSvmsForAd", mock.Anything, int64(200)).Return(svms, nil)
		env.OnActivity("GenerateUpdateAdCredentialsParams", mock.Anything, oldAd, params).Return(&updateParams, nil)
		env.OnActivity("GetPoolBySvmPoolId", mock.Anything, int64(1)).Return(pool, nil)
		env.OnActivity("GetNode", mock.Anything, int64(1)).Return(nodes, nil)
		env.OnActivity("GetCifsService", mock.Anything, mock.Anything, "svm-1", "svm-uuid-1").Return(&cifs, nil)
		env.OnActivity("UpdateAdCredentialsForSvm", mock.Anything, mock.Anything, updateParams, "svm-1", "svm-uuid-1", cifs).Return(nil)
		env.OnActivity("PropagateAdChangeIdToPool", mock.Anything, pool, changeId).Return(nil)

		var result error
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			result = PushAdUpdatesToSVMWorkflow(ctx, &oldAd, &params, changeId)
			return result
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PushAdUpdatesToSVMWorkflow(ctx, &oldAd, &params, changeId)
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, result)
		env.AssertExpectations(t)
	})

	t.Run("SuccessfulWithMultipleSvmBatches", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		env.RegisterActivity(&activities.CommonActivities{})

		oldAd := models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 300}, // Add ID
			AdName:    "test-ad-batches",
			Domain:    "example.com",
		}

		params := common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		changeId := "change-id-batches"

		// Create 15 SVMs to test batching (2 batches)
		svms := make([]*datamodel.Svm, 15)
		for i := 0; i < 15; i++ {
			svms[i] = &datamodel.Svm{
				Name:   fmt.Sprintf("svm-%d", i+1),
				PoolID: int64(1),
				SvmDetails: &datamodel.SvmDetails{
					ExternalUUID: fmt.Sprintf("svm-uuid-%d", i+1),
				},
			}
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: int64(1),
			},
			DeploymentName: "test-deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
			},
		}

		nodes := []*datamodel.Node{
			{
				BaseModel: datamodel.BaseModel{
					ID: int64(1),
				},
				EndpointAddress: "10.0.0.1",
			},
		}

		cifs := ontapRest.CifsService{
			CifsService: ontapmodels.CifsService{
				Name: nillable.GetStringPtr("cifs-test"),
			},
		}

		updateParams := vsa.UpdateActiveDirectoryCredentialsParams{
			NewCredentials: &vsa.ActiveDirectory{
				Password: "new-password",
			},
		}

		env.OnActivity("GetSvmsForAd", mock.Anything, int64(300)).Return(svms, nil)
		env.OnActivity("GenerateUpdateAdCredentialsParams", mock.Anything, oldAd, params).Return(&updateParams, nil).Once()

		// Setup mocks for all 15 SVMs
		for i := 0; i < 15; i++ {
			svmName := fmt.Sprintf("svm-%d", i+1)
			svmUUID := fmt.Sprintf("svm-uuid-%d", i+1)
			env.OnActivity("GetPoolBySvmPoolId", mock.Anything, int64(1)).Return(pool, nil).Once()
			env.OnActivity("GetNode", mock.Anything, int64(1)).Return(nodes, nil).Once()
			env.OnActivity("GetCifsService", mock.Anything, mock.Anything, svmName, svmUUID).Return(&cifs, nil).Once()
			env.OnActivity("UpdateAdCredentialsForSvm", mock.Anything, mock.Anything, updateParams, svmName, svmUUID, cifs).Return(nil).Once()
			env.OnActivity("PropagateAdChangeIdToPool", mock.Anything, pool, changeId).Return(nil).Once()
		}

		var result error
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			result = PushAdUpdatesToSVMWorkflow(ctx, &oldAd, &params, changeId)
			return result
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PushAdUpdatesToSVMWorkflow(ctx, &oldAd, &params, changeId)
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, result)
		env.AssertExpectations(t)
	})

	t.Run("Failure_NilActiveDirectory", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		changeId := "change-id-nil"

		var result error
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			result = PushAdUpdatesToSVMWorkflow(ctx, nil, params, changeId)
			return result
		})

		env.ExecuteWorkflow(PushAdUpdatesToSVMWorkflow, nil, params, changeId)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.Contains(t, env.GetWorkflowError().Error(), "Active Directory is nil")
	})

	t.Run("Failure_GetSvmsForAdError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})

		oldAd := &models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 400}, // Add ID
			AdName:    "test-ad-get-error",
			Domain:    "example.com",
		}

		params := &common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		changeId := "change-id-get-error"

		env.OnActivity("GetSvmsForAd", mock.Anything, int64(400)).Return(nil, vsaerrors.New("failed to get SVMs"))

		var result error
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			result = PushAdUpdatesToSVMWorkflow(ctx, oldAd, params, changeId)
			return result
		})

		env.ExecuteWorkflow(PushAdUpdatesToSVMWorkflow, oldAd, params, changeId)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.Contains(t, env.GetWorkflowError().Error(), "failed to get SVMs")
		env.AssertExpectations(t)
	})

	t.Run("Failure_UpdateAdCredentialsError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		env.RegisterActivity(&activities.CommonActivities{})

		oldAd := models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 500}, // Add ID
			AdName:    "test-ad-update-error",
			Domain:    "example.com",
		}

		params := common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		changeId := "change-id-update-error"

		svms := []*datamodel.Svm{
			{
				Name:   "svm-error",
				PoolID: int64(1),
				SvmDetails: &datamodel.SvmDetails{
					ExternalUUID: "svm-uuid-error",
				},
			},
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: int64(1),
			},
			DeploymentName: "test-deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
			},
		}

		nodes := []*datamodel.Node{
			{
				BaseModel: datamodel.BaseModel{
					ID: int64(1),
				},
				EndpointAddress: "10.0.0.1",
			},
		}

		cifs := ontapRest.CifsService{
			CifsService: ontapmodels.CifsService{
				Name: nillable.GetStringPtr("cifs-error"),
			},
		}

		updateParams := vsa.UpdateActiveDirectoryCredentialsParams{
			NewCredentials: &vsa.ActiveDirectory{
				Password: "new-password",
			},
		}

		env.OnActivity("GetSvmsForAd", mock.Anything, int64(500)).Return(svms, nil)
		env.OnActivity("GenerateUpdateAdCredentialsParams", mock.Anything, oldAd, params).Return(&updateParams, nil)
		env.OnActivity("GetPoolBySvmPoolId", mock.Anything, int64(1)).Return(pool, nil)
		env.OnActivity("GetNode", mock.Anything, int64(1)).Return(nodes, nil)
		env.OnActivity("GetCifsService", mock.Anything, mock.Anything, "svm-error", "svm-uuid-error").Return(&cifs, nil)
		env.OnActivity("UpdateAdCredentialsForSvm", mock.Anything, mock.Anything, updateParams, "svm-error", "svm-uuid-error", cifs).Return(vsaerrors.New("failed to update AD credentials"))

		var result error
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			result = PushAdUpdatesToSVMWorkflow(ctx, &oldAd, &params, changeId)
			return result
		})

		env.ExecuteWorkflow(PushAdUpdatesToSVMWorkflow, &oldAd, &params, changeId)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.Contains(t, env.GetWorkflowError().Error(), "failed to update AD credentials")
		env.AssertExpectations(t)
	})

	t.Run("Failure_PropagateChangeIdError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{"log-fields": encodedValue},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{})
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{})
		env.RegisterActivity(&activities.CommonActivities{})

		oldAd := models.ActiveDirectory{
			BaseModel: models.BaseModel{ID: 600}, // Add ID
			AdName:    "test-ad-propagate-error",
			Domain:    "example.com",
		}

		params := common.UpdateActiveDirectoryParams{
			AccountId:         "test-account",
			ActiveDirectoryId: "ad-uuid",
			Password:          nillable.GetStringPtr("new-password"),
		}

		changeId := "change-id-propagate-error"

		svms := []*datamodel.Svm{
			{
				Name:   "svm-propagate",
				PoolID: int64(1),
				SvmDetails: &datamodel.SvmDetails{
					ExternalUUID: "svm-uuid-propagate",
				},
			},
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: int64(1),
			},
			DeploymentName: "test-deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
			},
		}

		nodes := []*datamodel.Node{
			{
				BaseModel: datamodel.BaseModel{
					ID: int64(1),
				},
				EndpointAddress: "10.0.0.1",
			},
		}

		cifs := ontapRest.CifsService{
			CifsService: ontapmodels.CifsService{
				Name: nillable.GetStringPtr("cifs-propagate"),
			},
		}

		updateParams := vsa.UpdateActiveDirectoryCredentialsParams{
			NewCredentials: &vsa.ActiveDirectory{
				Password: "new-password",
			},
		}

		env.OnActivity("GetSvmsForAd", mock.Anything, int64(600)).Return(svms, nil)
		env.OnActivity("GenerateUpdateAdCredentialsParams", mock.Anything, oldAd, params).Return(&updateParams, nil)
		env.OnActivity("GetPoolBySvmPoolId", mock.Anything, int64(1)).Return(pool, nil)
		env.OnActivity("GetNode", mock.Anything, int64(1)).Return(nodes, nil)
		env.OnActivity("GetCifsService", mock.Anything, mock.Anything, "svm-propagate", "svm-uuid-propagate").Return(&cifs, nil)
		env.OnActivity("UpdateAdCredentialsForSvm", mock.Anything, mock.Anything, updateParams, "svm-propagate", "svm-uuid-propagate", cifs).Return(nil)
		env.OnActivity("PropagateAdChangeIdToPool", mock.Anything, pool, changeId).Return(vsaerrors.New("failed to propagate change ID"))
		var result error
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			result = PushAdUpdatesToSVMWorkflow(ctx, &oldAd, &params, changeId)
			return result
		})

		env.ExecuteWorkflow(PushAdUpdatesToSVMWorkflow, &oldAd, &params, changeId)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.Contains(t, env.GetWorkflowError().Error(), "failed to propagate change ID")
		env.AssertExpectations(t)
	})
}

func TestDeleteActiveDirectoryWorkflow_EnsureJobStateError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	mockStorage := database.NewMockStorage(t)

	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logger": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	commonActivity := activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterActivity(commonActivity.UpdateJobStatus)

	// Return a job in PROCESSING state (not NEW) so EnsureJobState returns an error.
	env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING),
	}, nil)

	params := &common.DeleteActiveDirectoryParams{
		AccountId:           int64(1),
		ActiveDirectoryUUID: "ad-uuid-ensure-state",
		ProjectNumber:       "test-project-ensure",
	}

	env.SetTestTimeout(1 * time.Minute)
	env.ExecuteWorkflow(DeleteActiveDirectoryWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

// TestDeleteActiveDirectoryWorkflow tests the DeleteActiveDirectoryWorkflow
func TestDeleteActiveDirectoryWorkflow(t *testing.T) {
	t.Run("Success_VcpOnly_ADExists", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		accountID := int64(123)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-123",
			ProjectNumber:       "test-project-123",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: true,
		}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("DeleteVcpActiveDirectory", mock.Anything, params).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(DeleteActiveDirectoryWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Success_SDE_ADExistsAtBothPlaces", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		accountID := int64(456)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-456",
			ProjectNumber:       "test-project-456",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: true,
		}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.ProjectNumber).Return("test-jwt-token", nil)
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("DeleteSdeActiveDirectory", mock.Anything, params).Return(nil)
		env.OnActivity("DeleteVcpActiveDirectory", mock.Anything, params).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(DeleteActiveDirectoryWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Success_SDE_ADNotFoundAtVCP", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		accountID := int64(789)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-789",
			ProjectNumber:       "test-project-789",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		// When AD doesn't exist at VCP, DeletionAllowed is true (no SVMs using it)
		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        false,
			DeletionAllowed: true,
		}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.ProjectNumber).Return("test-jwt-token", nil)
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("DeleteSdeActiveDirectory", mock.Anything, params).Return(nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(DeleteActiveDirectoryWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_CheckDeletionNotAllowed", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		accountID := int64(999)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-in-use",
			ProjectNumber:       "test-project-999",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: false,
		}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(DeleteActiveDirectoryWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		wfErr := env.GetWorkflowError()
		assert.Error(t, wfErr)
		// VSCP-4490: when AD is in use by pool(s), workflow must fail with the expected message (and optionally VCP error 14000)
		assert.Contains(t, wfErr.Error(), "Active Directory credentials are in use by Storage Pool(s)",
			"expected workflow error to indicate AD is in use by pool(s)")
		// If the error was preserved as CustomError across the workflow boundary, verify the code
		if customErr := vsaerrors.ExtractCustomError(wfErr); customErr != nil && customErr.IsError(vsaerrors.ErrActiveDirectoryDeleteErrorDueToInUseByPool) {
			assert.Contains(t, customErr.GetMessage(), "Active Directory credentials are in use by Storage Pool(s)")
		}
		env.AssertExpectations(t)
	})

	t.Run("Failure_CheckDeletionError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		accountID := int64(888)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-check-error",
			ProjectNumber:       "test-project-888",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(nil, errors.New("database error"))
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(DeleteActiveDirectoryWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_DeleteVcpError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		accountID := int64(777)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-vcp-error",
			ProjectNumber:       "test-project-777",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: true,
		}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("DeleteVcpActiveDirectory", mock.Anything, params).Return(errors.New("VCP deletion failed"))
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(DeleteActiveDirectoryWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_DeleteSdeError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		accountID := int64(666)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-sde-error",
			ProjectNumber:       "test-project-666",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: true,
		}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.ProjectNumber).Return("test-jwt-token", nil)
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("DeleteSdeActiveDirectory", mock.Anything, params).Return(errors.New("SDE deletion failed"))
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(DeleteActiveDirectoryWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_UpdateJobStatusError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		accountID := int64(555)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-job-error",
			ProjectNumber:       "test-project-555",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(errors.NewUserInputValidationErr("job status update failed"))

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(DeleteActiveDirectoryWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("Failure_DeleteVcpWhenSDEEnabled", func(t *testing.T) {
		// Test to cover lines 285-286: DeleteVcpActiveDirectory fails when SDE is enabled
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		accountID := int64(222)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-vcp-fail-sde",
			ProjectNumber:       "test-project-222",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost // Enable SDE
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: true,
		}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.ProjectNumber).Return("test-jwt-token", nil)
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("DeleteSdeActiveDirectory", mock.Anything, params).Return(nil)
		env.OnActivity("DeleteVcpActiveDirectory", mock.Anything, params).Return(errors.New("VCP deletion failed in SDE mode"))
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(DeleteActiveDirectoryWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.Contains(t, env.GetWorkflowError().Error(), "VCP deletion failed in SDE mode")
		env.AssertExpectations(t)
	})

	t.Run("Failure_DeleteSdeWhenADNotFoundAtVCP", func(t *testing.T) {
		// Test to cover lines 255-256: DeleteSdeActiveDirectory fails when AD not found at VCP
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		accountID := int64(111)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-sde-fail-notfound",
			ProjectNumber:       "test-project-111",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost // Enable SDE
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        false, // AD not found at VCP
			DeletionAllowed: true,
		}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.ProjectNumber).Return("test-jwt-token", nil)
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("DeleteSdeActiveDirectory", mock.Anything, params).Return(errors.New("SDE deletion failed"))
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		env.SetTestTimeout(1 * time.Minute)
		env.ExecuteWorkflow(DeleteActiveDirectoryWorkflow, params)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.Contains(t, env.GetWorkflowError().Error(), "SDE deletion failed")
		env.AssertExpectations(t)
	})
}

// TestActiveDirectoryDeleteWorkflow_Setup tests the Setup method
func TestActiveDirectoryDeleteWorkflow_Setup(t *testing.T) {
	t.Run("SuccessfulSetup", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		accountID := int64(123)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-123",
			ProjectNumber:       "test-project-123",
		}

		var setupErr error
		var wf *ActiveDirectoryDeleteWorkflow
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf = &ActiveDirectoryDeleteWorkflow{}
			setupErr = wf.Setup(ctx, params)
			return setupErr
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf = &ActiveDirectoryDeleteWorkflow{}
			setupErr = wf.Setup(ctx, params)
			return setupErr
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, setupErr)
		assert.NoError(t, env.GetWorkflowError())
		assert.Equal(t, WorkflowStatusCreated, wf.Status)
		assert.Equal(t, "test-project-123", wf.CustomerID)
		assert.Equal(t, "default-test-workflow-id", wf.ID)
	})

	t.Run("QueryHandlerTest", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)

		accountID := int64(456)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-query",
			ProjectNumber:       "test-project-456",
		}

		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			err := wf.Setup(ctx, params)
			if err != nil {
				return err
			}
			wf.Status = WorkflowStatusRunning
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			err := wf.Setup(ctx, params)
			if err != nil {
				return err
			}
			wf.Status = WorkflowStatusRunning
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())

		queryResult, err := env.QueryWorkflow("status")
		assert.NoError(t, err)
		assert.NotNil(t, queryResult)

		var status WorkflowStatus
		err = queryResult.Get(&status)
		assert.NoError(t, err)
		assert.Equal(t, "test-project-456", status.CustomerID)
		assert.Equal(t, "default-test-workflow-id", status.ID)
	})
}

// TestActiveDirectoryDeleteWorkflow_Run tests the Run method
func TestActiveDirectoryDeleteWorkflow_Run(t *testing.T) {
	t.Run("SuccessfulVcpOnlyRun", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		accountID := int64(123)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-run",
			ProjectNumber:       "test-project-123",
		}

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: true,
		}

		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("DeleteVcpActiveDirectory", mock.Anything, params).Return(nil)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			runResult, runErr = wf.Run(ctx, params)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			runResult, runErr = wf.Run(ctx, params)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		assert.Nil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("SuccessfulSdeRun", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		accountID := int64(456)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-sde-run",
			ProjectNumber:       "test-project-456",
		}

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: true,
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.ProjectNumber).Return("test-jwt-token", nil)
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("DeleteSdeActiveDirectory", mock.Anything, params).Return(nil)
		env.OnActivity("DeleteVcpActiveDirectory", mock.Anything, params).Return(nil)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			runResult, runErr = wf.Run(ctx, params)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			runResult, runErr = wf.Run(ctx, params)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		assert.Nil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("Failure_ActivityError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		accountID := int64(789)
		params := &common.DeleteActiveDirectoryParams{
			AccountId:           accountID,
			ActiveDirectoryUUID: "ad-uuid-run-error",
			ProjectNumber:       "test-project-789",
		}

		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(nil, errors.New("check deletion failed"))

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			runResult, runErr = wf.Run(ctx, params)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			runResult, runErr = wf.Run(ctx, params)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.NotNil(t, runErr)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("Failure_SdeConflict_ADNotFoundAtVCP_Returns409", func(t *testing.T) {
		// Verify that when DeleteSdeActiveDirectory returns a CustomError with
		// ErrActiveDirectoryDeleteErrorDueToInUseByPool (14000), the workflow
		// properly surfaces it. This simulates CVP returning 409 Conflict.
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		params := &common.DeleteActiveDirectoryParams{
			AccountId:           int64(444),
			ActiveDirectoryUUID: "ad-uuid-cvp-conflict",
			ProjectNumber:       "test-project-444",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        false,
			DeletionAllowed: true,
		}

		// CVP 409 Conflict maps to generic conflict
		sdeConflictErr := temporal.NewNonRetryableApplicationError(
			"Error deleting active directory - Active Directory credentials are in use by Storage Pool(s)",
			vsaerrors.CustomErrorType,
			nil,
			vsaerrors.ErrCVPConflict,
			"Active Directory deletion conflict: AD credentials are in use by Storage Pool(s)",
		)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.ProjectNumber).Return("test-jwt-token", nil)
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("DeleteSdeActiveDirectory", mock.Anything, params).Return(sdeConflictErr)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			runResult, runErr = wf.Run(ctx, params)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		require.NotNil(t, runErr, "expected CustomError from Run()")
		assert.True(t, runErr.IsError(vsaerrors.ErrCVPConflict),
			"expected tracking ID %d (CVP Conflict), got %d", vsaerrors.ErrCVPConflict, runErr.TrackingID)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("Failure_SdeConflict_ADExistsAtBoth_Returns409", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(t)

		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)

		params := &common.DeleteActiveDirectoryParams{
			AccountId:           int64(555),
			ActiveDirectoryUUID: "ad-uuid-cvp-conflict-both",
			ProjectNumber:       "test-project-555-conflict",
		}

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = cvpHost
		defer func() {
			cvp.CVP_HOST = originalHost
		}()

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: true,
		}

		// CVP 409 Conflict maps to generic conflict
		sdeConflictErr := temporal.NewNonRetryableApplicationError(
			"Error deleting active directory - Active Directory credentials are in use by Storage Pool(s)",
			vsaerrors.CustomErrorType,
			nil,
			vsaerrors.ErrCVPConflict,
			"Active Directory deletion conflict: AD credentials are in use by Storage Pool(s)",
		)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, params.ProjectNumber).Return("test-jwt-token", nil)
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)
		env.OnActivity("DeleteSdeActiveDirectory", mock.Anything, params).Return(sdeConflictErr)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			runResult, runErr = wf.Run(ctx, params)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		require.NotNil(t, runErr, "expected CustomError from Run()")
		assert.True(t, runErr.IsError(vsaerrors.ErrCVPConflict),
			"expected tracking ID %d (CVP Conflict), got %d", vsaerrors.ErrCVPConflict, runErr.TrackingID)
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})

	t.Run("DeletionNotAllowed_ReturnsErrActiveDirectoryDeleteErrorDueToInUseByPool", func(t *testing.T) {
		// VSCP-4490: when AD is in use by pool(s), Run must return VCP error 14000 with the correct message
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logger": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{})

		originalHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalHost }()

		params := &common.DeleteActiveDirectoryParams{
			AccountId:           int64(333),
			ActiveDirectoryUUID: "ad-uuid-in-use-by-pool",
			ProjectNumber:       "test-project-333",
		}

		checkResult := &active_directory_activities.CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: false,
		}
		env.OnActivity("CheckDeletionAllowed", mock.Anything, params).Return(checkResult, nil)

		var runResult interface{}
		var runErr *vsaerrors.CustomError
		env.RegisterWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			runResult, runErr = wf.Run(ctx, params)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			wf := &ActiveDirectoryDeleteWorkflow{}
			runResult, runErr = wf.Run(ctx, params)
			if runErr != nil {
				return runErr
			}
			return nil
		})

		assert.True(t, env.IsWorkflowCompleted())
		require.NotNil(t, runErr, "expected CustomError when deletion is not allowed (workflow error: %v)", env.GetWorkflowError())
		assert.True(t, runErr.IsError(vsaerrors.ErrActiveDirectoryDeleteErrorDueToInUseByPool),
			"expected ErrActiveDirectoryDeleteErrorDueToInUseByPool (14000), got TrackingID %d", runErr.TrackingID)
		assert.Contains(t, runErr.GetMessage(), "Active Directory credentials are in use by Storage Pool(s)")
		assert.Nil(t, runResult)
		env.AssertExpectations(t)
	})
}
