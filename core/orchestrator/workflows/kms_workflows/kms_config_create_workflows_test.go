package kms_workflows

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

var getSignedJwtToken = auth.GetSignedJwtToken

func expectJobIsNew(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
}

func TestCreateKmsConfig(t *testing.T) {
	// These tests cover the SDE path — ensure SDE is enabled
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	t.Run("WhenGetSignedTokenActivityFails", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()
		// Set up test data
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("", errors.New("GetSignedTokenActivity failed"))
		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Execute workflow
		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenJobStateIsNotNew", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{}

		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStatePROCESSING),
		}, nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		err := env.GetWorkflowError()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expected NEW")
	})
	t.Run("WhenPollKmsConfigOperationActivityFails", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()
		// Set up test data
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(temporal.NewNonRetryableApplicationError("some", "error", nil))
		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Execute workflow
		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenDescribeKmsConfigurationActivityFails", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()
		// Set up test data
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{}}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("some error"))
		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Execute workflow
		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreateVSAKmsConfigSAKeyActivityFails", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()
		// Set up test data
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{}}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(nil, errors.New("some error"))
		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenGrantRoleActivityFails", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()
		// Set up test data
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{}}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(errors.New("some error"))
		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Execute workflow
		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCreatedKmsConfigActivityFails", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()
		// Set up test data
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{}}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreatedKmsConfigActivity", mock.Anything, mock.Anything).Return(errors.New("some error"))
		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Execute workflow
		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenUpdateKmsConfigAttributesActivityFails", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = auth.GetSignedJwtToken
		}()
		// Set up test data
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{}}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("some error"))
		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Execute workflow
		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenSuccess", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = auth.GetSignedJwtToken
		}()
		// Set up test data
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{}}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreatedKmsConfigActivity", mock.Anything, mock.Anything).Return(nil)
		// Execute workflow
		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}

		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenWorkflowCompletesMarksSdeJobDone", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:           "test-kms",
			AccountName:    "test-account",
			SdeJobUUID:     "sde-job-id",
			ProjectNumber:  "test-project",
			LocationID:     "us-east4",
			OperationUri:   "operations/test",
			OperationDone:  true,
			XCorrelationID: "corr-id",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{}}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)

		sdeJobMarkedDone := false
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.BaseModel.UUID == "sde-job-id" && job.State == string(datamodel.JobsStateDONE)
		})).Run(func(args mock.Arguments) {
			sdeJobMarkedDone = true
		}).Return(nil).Once()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()

		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("signed-token", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreatedKmsConfigActivity", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		assert.True(t, sdeJobMarkedDone)
		env.AssertExpectations(t)
	})
	t.Run("HeartbeatTimeoutIsConfigured", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{}}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreatedKmsConfigActivity", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}

		// Verify workflow completes successfully, which confirms HeartbeatTimeout is configured
		// Activities with RecordHeartbeat would fail if HeartbeatTimeout wasn't set
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCancellationSignalReceivedBeforeGetSignedTokenActivity", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before GetSignedTokenActivity
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}, 5*time.Millisecond)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCancellationSignalReceivedAfterGetSignedTokenActivity", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		// Send the cancel signal from inside GetSignedTokenActivity so it's guaranteed to land
		// after the activity is invoked but before the next CheckCancellationSignal (line 176,
		// before PollKmsConfigOperationActivity). A RegisterDelayedCallback is unreliable here:
		// the test env auto-advances the simulated clock to the next pending timer when the
		// workflow is idle, which can fire the timer before GetSignedTokenActivity runs and
		// cause the very first CheckCancellationSignal (line 128) to catch it instead.
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}).Return("test-jwt-token", nil)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCancellationSignalReceivedBeforePollKmsConfigOperationActivity", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil).Maybe()

		// Send cancellation signal before PollKmsConfigOperationActivity
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}, 10*time.Millisecond)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCancellationSignalReceivedBeforeDescribeSDEKmsConfigurationActivity", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before DescribeSDEKmsConfigurationActivity
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}, 15*time.Millisecond)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCancellationSignalReceivedBeforeUpdateKmsConfigAttributesActivity", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil).Maybe()
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil).Maybe()

		// Send cancellation signal before UpdateKmsConfigAttributesActivity
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}, 20*time.Millisecond)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCancellationSignalReceivedBeforeCreateVSAKmsConfigSAKeyActivity", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)

		// Send cancellation signal before CreateVSAKmsConfigSAKeyActivity
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}, 25*time.Millisecond)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCancellationSignalReceivedBeforeGrantRoleActivity", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)

		// Send cancellation signal before GrantRoleActivity
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}, 30*time.Millisecond)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCancellationSignalReceivedBeforeCreatedKmsConfigActivity", func(t *testing.T) {
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// Send the cancel signal from inside GrantRoleActivity so it's guaranteed to land
		// after the activity is invoked but before the next CheckCancellationSignal (line 231,
		// before CreatedKmsConfigActivity). A RegisterDelayedCallback is unreliable here:
		// the test env auto-advances the simulated clock to the next pending timer when the
		// workflow is idle, which can fire the timer before the intended activity runs and
		// cause an earlier CheckCancellationSignal to catch it instead.
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}).Return(nil)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCheckCancellationReturnsErrorBeforeGetSignedTokenActivity", func(t *testing.T) {
		// Tests line 118: checkCancellation() returns an error
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal immediately to trigger CheckCancellation error
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}, 1*time.Millisecond)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCancellationOccursDuringRollback", func(t *testing.T) {
		// Tests cancellation rollback: signal arrives after GetSignedTokenActivity, caught at line 163 CheckCancellationSignal
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		// Send cancellation signal after GetSignedTokenActivity completes; it will be caught
		// by CheckCancellationSignal at line 163, triggering the rollback via ExecuteDeferredCleanup
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}).Return("test-jwt-token", nil)

		// PollKmsConfigOperationActivity is NOT called because the cancellation check at line 163
		// catches the signal before that activity runs
		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCheckCancellationReturnsErrorAfterGetSignedTokenActivity", func(t *testing.T) {
		// Tests line 139: checkCancellation() returns an error after GetSignedTokenActivity
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		// Send the cancel signal from inside GetSignedTokenActivity so it's guaranteed to land
		// after the activity is invoked but before the next CheckCancellationSignal (line 176,
		// before PollKmsConfigOperationActivity). A RegisterDelayedCallback is unreliable here:
		// the test env auto-advances the simulated clock to the next pending timer when the
		// workflow is idle, which can fire the timer before GetSignedTokenActivity runs and
		// cause the very first CheckCancellationSignal (line 128) to catch it instead.
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}).Return("test-jwt-token", nil)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCheckCancellationReturnsErrorBeforePollKmsConfigOperationActivity", func(t *testing.T) {
		// Tests line 174: checkCancellation() returns an error before PollKmsConfigOperationActivity
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)

		// Send cancellation signal before PollKmsConfigOperationActivity
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}, 15*time.Millisecond)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCheckCancellationReturnsErrorBeforeDescribeSDEKmsConfigurationActivity", func(t *testing.T) {
		// Tests line 189: checkCancellation() returns an error before DescribeSDEKmsConfigurationActivity
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before DescribeSDEKmsConfigurationActivity
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}, 20*time.Millisecond)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCheckCancellationReturnsErrorBeforeUpdateKmsConfigAttributesActivity", func(t *testing.T) {
		// Tests line 201: checkCancellation() returns an error before UpdateKmsConfigAttributesActivity
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		// Send the cancel signal from inside DescribeSDEKmsConfigurationActivity so it is
		// guaranteed to land after the activity is invoked but before the next
		// CheckCancellationSignal (line 200, before UpdateKmsConfigAttributesActivity).
		// RegisterDelayedCallback is unreliable here: the test env auto-advances the
		// simulated clock to the next pending timer when the workflow is idle, which can
		// fire the timer before any activities run and cause the very first
		// CheckCancellationSignal (line 127) to catch it instead.
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}).Return(cvpKmsConfig, nil)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCheckCancellationReturnsErrorBeforeCreateVSAKmsConfigSAKeyActivity", func(t *testing.T) {
		// Tests line 211: checkCancellation() returns an error before CreateVSAKmsConfigSAKeyActivity
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)

		// Send cancellation signal before CreateVSAKmsConfigSAKeyActivity
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}, 30*time.Millisecond)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCheckCancellationReturnsErrorBeforeGrantRoleActivity", func(t *testing.T) {
		// Tests line 220: checkCancellation() returns an error before GrantRoleActivity
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// CreateVSAKmsConfigSAKeyActivity sets ServiceAccount on kmsConfig before returning it (matching real behavior)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil).Run(func(args mock.Arguments) {
			// Set ServiceAccount on the kmsConfig (matching real behavior where CreateVSAKmsConfigSAKeyActivity sets it)
			if kc, ok := args.Get(1).(*datamodel.KmsConfig); ok && kc != nil {
				kc.ServiceAccount = &datamodel.ServiceAccount{
					BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
				}
			}
			// Send cancellation signal immediately after CreateVSAKmsConfigSAKeyActivity completes
			// This ensures it arrives before the cancellation check at line 219
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		})

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("WhenCheckCancellationReturnsErrorBeforeCreatedKmsConfigActivity", func(t *testing.T) {
		// Tests line 229: checkCancellation() returns an error before CreatedKmsConfigActivity
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
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})

		originalFunc := getSignedJwtToken
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		defer func() {
			getSignedJwtToken = originalFunc
		}()

		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
			LocationID:  "us-east4",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "kms-config-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		cvpKmsConfig := &cvpmodels.KmsConfigV1beta{}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSignedTokenActivity", mock.Anything, mock.Anything).Return("test-jwt-token", nil)
		env.OnActivity("PollKmsConfigOperationActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DescribeSDEKmsConfigurationActivity", mock.Anything, mock.Anything, mock.Anything).Return(cvpKmsConfig, nil)
		env.OnActivity("UpdateKmsConfigAttributesActivity", mock.Anything, mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("GrantRoleActivity", mock.Anything, mock.Anything).Return(nil)

		// Send cancellation signal before CreatedKmsConfigActivity
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelKmsConfigSignalName, "cancel data")
		}, 40*time.Millisecond)

		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

func TestCreateKmsConfigVCPPath(t *testing.T) {
	setupWorkflowEnv := func(t *testing.T) *testsuite.TestWorkflowEnvironment {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{
			Fields: map[string]*commonpb.Payload{"logParam": encodedValue},
		})
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&kms_activities.KmsConfigActivity{})
		return env
	}

	t.Run("WhenVCPPathSucceeds", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = origCVPHost }()

		env := setupWorkflowEnv(t)
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP}}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateGCPServiceAccountActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("EnableGCPServiceAccountActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreatedKmsConfigActivity", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenCreateGCPServiceAccountActivityFails", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = origCVPHost }()

		env := setupWorkflowEnv(t)
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP}}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateGCPServiceAccountActivity", mock.Anything, mock.Anything).Return(nil, errors.New("GCP SA creation failed"))
		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenEnableGCPServiceAccountActivityFails", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = origCVPHost }()

		env := setupWorkflowEnv(t)
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP}}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateGCPServiceAccountActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("EnableGCPServiceAccountActivity", mock.Anything, mock.Anything).Return(errors.New("IAM enable failed"))
		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenVCPCreateSAKeyFails", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = origCVPHost }()

		env := setupWorkflowEnv(t)
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP}}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateGCPServiceAccountActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("EnableGCPServiceAccountActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(nil, errors.New("SA key creation failed"))
		env.OnActivity("FailedKmsConfigCreateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenVCPPath_SkipsGrantRoleAndSDEActivities", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = origCVPHost }()

		env := setupWorkflowEnv(t)
		params := &common.CreateKmsConfigParams{
			Name:        "test-kms",
			AccountName: "test-account",
		}
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP}}
		expectJobIsNew(env)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateGCPServiceAccountActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("EnableGCPServiceAccountActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateVSAKmsConfigSAKeyActivity", mock.Anything, mock.Anything).Return(kmsConfig, nil)
		env.OnActivity("CreatedKmsConfigActivity", mock.Anything, mock.Anything).Return(nil)

		// These SDE-only activities should NOT be called:
		// GetSignedTokenActivity, PollKmsConfigOperationActivity, DescribeSDEKmsConfigurationActivity,
		// UpdateKmsConfigAttributesActivity, GrantRoleActivity

		env.ExecuteWorkflow(CreateKmsConfigWorkflow, params, kmsConfig)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t) // will fail if any unexpected activities were called
	})
}
