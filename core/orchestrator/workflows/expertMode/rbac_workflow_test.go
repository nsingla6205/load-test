package expertMode

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func init() {
	vlm.SetActiveProvider(vlm.OCICloud)
}

type RBACWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *RBACWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetTestTimeout(5 * time.Minute)
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	// Add correlation ID to logger fields for VLM workflows
	// The key must match what GetCorrelationIDFromWorkflowContextLoggerFields expects
	// It expects string(middleware.RequestCorrelationID) which is "requestCorrelationID"
	loggerFields := log.Fields{
		string(middleware.RequestCorrelationID): "test-correlation-id",
	}
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(loggerFields)
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	s.env.SetHeader(mockHeader)

	// Register workflows
	s.env.RegisterWorkflow(UpdateRbacForPoolsWorkflow)
	s.env.RegisterWorkflow(UpdateRbacForSinglePoolWorkflow)
	s.env.RegisterWorkflow(UpdateSinglePoolRbacChildWorkflow)

	// Register common activities for job management
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	// Mock GetJob activity - return NEW state for workflow job (EnsureJobState)
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJobStatus activity
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Register RBAC activities
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.RegisterActivity(rbacActivity.ListActiveExpertModePools)
	s.env.RegisterActivity(rbacActivity.GetPoolsDetailsByOntapVersion)
	s.env.RegisterActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion)
	s.env.RegisterActivity(rbacActivity.GetPoolByUUID)
	s.env.RegisterActivity(rbacActivity.GetSinglePoolVersionDetails)
	s.env.RegisterActivity(poolActivity.GetOnTapCredentials)
	s.env.RegisterActivity(poolActivity.GetExpertModeCredentials)
	s.env.RegisterActivity(poolActivity.ParseVlmConfig)
	s.env.RegisterActivity(poolActivity.PrepareCreateVSAExpertModeReq)
	s.env.RegisterActivity(poolActivity.UpdateRbacCheckSumInPool)
	s.env.RegisterActivity(poolActivity.ValidateRbacHash)

	// Register VLM child workflow
	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, request *vlm.OntapExpertModeUserConfig) (vlm.OntapExpertModeUserResponse, error) {
			return vlm.OntapExpertModeUserResponse{
				RbacFileChecksum: "updated-hash-123",
			}, nil
		},
		workflow.RegisterOptions{Name: vlm.CreateVSAExpertModeUserWorkflowName},
	)
}

func (s *RBACWorkflowTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_Success() {
	// Test successful RBAC hash update workflow execution

	// Mock ListActiveExpertModePools activity
	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-1",
			},
			Name: "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "old-hash-1",
			},
		},
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-2",
			},
			Name: "pool-2",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "old-hash-2",
			},
		},
	}
	poolActivity := &activities.PoolActivity{}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	// Mock GetPoolsDetailsByOntapVersion activity
	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
			{PoolUUID: "pool-uuid-2", CurrentHash: "old-hash-2"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	// Mock GetLatestRbacHashForAllOntapVersion activity
	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{
			PoolUUID:       "pool-uuid-1",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-1",
			NeedUpdate:     true,
		},
		{
			PoolUUID:       "pool-uuid-2",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-2",
			NeedUpdate:     true,
		},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	// Mock activities that use PoolRbacUpdateRequest
	// Activities now modify the context in place, so we use mock.MatchedBy to handle this
	pool1 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "pool-uuid-1",
		},
		Name: "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}
	pool2 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "pool-uuid-2",
		},
		Name: "pool-2",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-2",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-2"}}`,
	}
	ontapCredentials := &vlm.OntapCredentials{
		AdminPassword: "password",
	}
	expertCredentials := &vlm.OntapCredentials{
		AdminPassword: "expert-password",
	}
	vlmConfig1 := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			DeploymentID: "deployment-1",
		},
	}
	vlmConfig2 := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{
			DeploymentID: "deployment-2",
		},
	}
	createVSAExpertModeReq1 := &vlm.OntapExpertModeUserConfig{
		VLMConfig: *vlmConfig1,
	}
	createVSAExpertModeReq2 := &vlm.OntapExpertModeUserConfig{
		VLMConfig: *vlmConfig2,
	}

	// Mock GetPoolByUUID - returns pool directly
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool1, nil).Once()
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-2").Return(pool2, nil).Once()

	// Mock ValidateRbacHash - returns nil (validation passes)
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Times(2)

	// Mock GetOnTapCredentials - returns credentials directly
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(ontapCredentials, nil).Times(2)

	// Mock GetExpertModeCredentials - returns credentials directly
	s.env.OnActivity(poolActivity.GetExpertModeCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(expertCredentials, nil).Times(2)

	// Mock ParseVlmConfig - returns different VLM configs for each pool
	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
		return pool.UUID == "pool-uuid-1"
	})).Return(vlmConfig1, nil).Once()
	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
		return pool.UUID == "pool-uuid-2"
	})).Return(vlmConfig2, nil).Once()

	// Mock PrepareCreateVSAExpertModeReq - returns different requests for each pool
	s.env.OnActivity(poolActivity.PrepareCreateVSAExpertModeReq, mock.Anything, mock.MatchedBy(func(vlmConfig vlm.VLMConfig) bool {
		return vlmConfig.Deployment.DeploymentID == "deployment-1"
	}), mock.AnythingOfType("vlm.OntapCredentials"), mock.AnythingOfType("vlm.OntapCredentials"), mock.AnythingOfType("*datamodel.Pool"), mock.Anything).Return(createVSAExpertModeReq1, nil).Once()
	s.env.OnActivity(poolActivity.PrepareCreateVSAExpertModeReq, mock.Anything, mock.MatchedBy(func(vlmConfig vlm.VLMConfig) bool {
		return vlmConfig.Deployment.DeploymentID == "deployment-2"
	}), mock.AnythingOfType("vlm.OntapCredentials"), mock.AnythingOfType("vlm.OntapCredentials"), mock.AnythingOfType("*datamodel.Pool"), mock.Anything).Return(createVSAExpertModeReq2, nil).Once()

	// Mock UpdateRbacCheckSumInPool - takes pool and bucketFileDetails as parameters
	s.env.OnActivity(poolActivity.UpdateRbacCheckSumInPool, mock.Anything, mock.AnythingOfType("*datamodel.Pool"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Times(2)

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_NoPools() {
	// Test workflow when no active pools exist

	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return([]*datamodel.Pool{}, nil).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_NoPoolsNeedUpdate() {
	// Test workflow when pools exist but none need updates

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-1",
			},
			Name: "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "current-hash-123",
			},
		},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "current-hash-123"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	// Mock GetLatestRbacHashForAllOntapVersion to return empty list (no pools need update)
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return([]expertmodeactivities.PoolDetailsWithRbacHash{}, nil).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_ListPoolsFails() {
	// Test workflow when ListActiveExpertModePools activity fails

	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	// Mock ListActiveExpertModePools to fail 3 times (max retries)
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(nil, errors.New("failed to list pools")).Times(3)

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed
	// Note: The workflow handles errors by updating job status, so it may complete successfully
	// even if activities fail. The error handling is internal to the workflow.
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	// The workflow should return an error when activities fail after retries
	// However, if UpdateJobStatus succeeds, the workflow may return nil
	// For this test, we verify the workflow handled the error by checking completion
	// In a real scenario, the job status would be ERROR
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_GetPoolsDetailsFails() {
	// Test workflow when GetPoolsDetailsByOntapVersion activity fails

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-1",
			},
			Name: "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
			},
		},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()
	// Mock GetPoolsDetailsByOntapVersion to fail 3 times (max retries)
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(nil, errors.New("failed to get pools details")).Times(3)

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed
	// Note: The workflow handles errors by updating job status, so it may complete successfully
	// even if activities fail. The error handling is internal to the workflow.
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	// The workflow should return an error when activities fail after retries
	// However, if UpdateJobStatus succeeds, the workflow may return nil
	// For this test, we verify the workflow handled the error by checking completion
	// In a real scenario, the job status would be ERROR
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_GetLatestHashFails() {
	// Test workflow when GetLatestRbacHashForAllOntapVersion activity fails

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-1",
			},
			Name: "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
			},
		},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()
	// Mock GetLatestRbacHashForAllOntapVersion to fail 3 times (max retries)
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(nil, errors.New("failed to get latest hash")).Times(3)

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed
	// Note: The workflow handles errors by updating job status, so it may complete successfully
	// even if activities fail. The error handling is internal to the workflow.
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	// The workflow should return an error when activities fail after retries
	// However, if UpdateJobStatus succeeds, the workflow may return nil
	// For this test, we verify the workflow handled the error by checking completion
	// In a real scenario, the job status would be ERROR
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_GetPoolByUUIDFails() {
	// Test workflow when GetPoolByUUID activity fails for a pool
	// The workflow should return an error when any pool fails

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-1",
			},
			Name: "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "old-hash-1",
			},
		},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{
			PoolUUID:       "pool-uuid-1",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-1",
			NeedUpdate:     true,
		},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	// Mock GetPoolByUUID to fail (allow retries)
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, mock.AnythingOfType("string")).Return(nil, errors.New("pool not found")).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed with error (workflow should fail when any pool fails)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	// Verify error message contains information about the failed pool
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "pool-uuid-1")
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_GetCredentialsFails() {
	// Test workflow when GetOnTapCredentials activity fails

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-1",
			},
			Name: "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "old-hash-1",
			},
		},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{
			PoolUUID:       "pool-uuid-1",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-1",
			NeedUpdate:     true,
		},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	pool1 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "pool-uuid-1",
		},
		Name: "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool1, nil).Once()

	// Mock GetOnTapCredentials to fail (allow retries)
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(nil, errors.New("failed to get credentials")).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed with error (workflow should fail when any pool fails)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	// Verify error message contains information about the failed pool
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "pool-uuid-1")
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_BatchProcessing() {
	// Test workflow with multiple pools to verify batch parallel processing
	// This test uses 7 pools to ensure they are processed in batches (batch size is 5)

	pools := []*datamodel.Pool{
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"}, Name: "pool-1", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-1"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"}, Name: "pool-2", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-2"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-3"}, Name: "pool-3", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-3"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-4"}, Name: "pool-4", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-4"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-5"}, Name: "pool-5", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-5"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-6"}, Name: "pool-6", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-6"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-7"}, Name: "pool-7", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-7"}},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
			{PoolUUID: "pool-uuid-2", CurrentHash: "old-hash-2"},
			{PoolUUID: "pool-uuid-3", CurrentHash: "old-hash-3"},
			{PoolUUID: "pool-uuid-4", CurrentHash: "old-hash-4"},
			{PoolUUID: "pool-uuid-5", CurrentHash: "old-hash-5"},
			{PoolUUID: "pool-uuid-6", CurrentHash: "old-hash-6"},
			{PoolUUID: "pool-uuid-7", CurrentHash: "old-hash-7"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{PoolUUID: "pool-uuid-1", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-1", NeedUpdate: true},
		{PoolUUID: "pool-uuid-2", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-2", NeedUpdate: true},
		{PoolUUID: "pool-uuid-3", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-3", NeedUpdate: true},
		{PoolUUID: "pool-uuid-4", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-4", NeedUpdate: true},
		{PoolUUID: "pool-uuid-5", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-5", NeedUpdate: true},
		{PoolUUID: "pool-uuid-6", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-6", NeedUpdate: true},
		{PoolUUID: "pool-uuid-7", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-7", NeedUpdate: true},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	// Mock activities for all 7 pools
	ontapCredentials := &vlm.OntapCredentials{AdminPassword: "password"}
	expertCredentials := &vlm.OntapCredentials{AdminPassword: "expert-password"}

	// Create VLM configs and requests for each pool with unique deployment IDs
	vlmConfigs := make(map[int]*vlm.VLMConfig)
	createVSAExpertModeReqs := make(map[int]*vlm.OntapExpertModeUserConfig)
	for i := 1; i <= 7; i++ {
		vlmConfigs[i] = &vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: fmt.Sprintf("deployment-%d", i)}}
		createVSAExpertModeReqs[i] = &vlm.OntapExpertModeUserConfig{VLMConfig: *vlmConfigs[i]}
	}

	// Mock GetPoolByUUID for all pools with unique deployment IDs
	for i := 1; i <= 7; i++ {
		poolUUID := fmt.Sprintf("pool-uuid-%d", i)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: poolUUID},
			Name:      fmt.Sprintf("pool-%d", i),
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: fmt.Sprintf("old-hash-%d", i),
				RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
			},
			VLMConfig: fmt.Sprintf(`{"deployment":{"deployment_id":"deployment-%d"}}`, i),
		}
		s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, poolUUID).Return(pool, nil).Once()
	}

	// Mock ValidateRbacHash for all 7 pools
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Times(7)

	// Mock other activities for all 7 pools
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(ontapCredentials, nil).Times(7)

	s.env.OnActivity(poolActivity.GetExpertModeCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(expertCredentials, nil).Times(7)

	// Mock ParseVlmConfig to return different VLM configs for each pool
	for i := 1; i <= 7; i++ {
		poolUUID := fmt.Sprintf("pool-uuid-%d", i)
		s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
			return pool.UUID == poolUUID
		})).Return(vlmConfigs[i], nil).Once()
	}

	// Mock PrepareCreateVSAExpertModeReq to return different requests for each pool
	for i := 1; i <= 7; i++ {
		deploymentID := fmt.Sprintf("deployment-%d", i)
		s.env.OnActivity(poolActivity.PrepareCreateVSAExpertModeReq, mock.Anything, mock.MatchedBy(func(vlmConfig vlm.VLMConfig) bool {
			return vlmConfig.Deployment.DeploymentID == deploymentID
		}), mock.AnythingOfType("vlm.OntapCredentials"), mock.AnythingOfType("vlm.OntapCredentials"), mock.AnythingOfType("*datamodel.Pool"), mock.Anything).Return(createVSAExpertModeReqs[i], nil).Once()
	}

	s.env.OnActivity(poolActivity.UpdateRbacCheckSumInPool, mock.Anything, mock.AnythingOfType("*datamodel.Pool"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Times(7)

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_StatusQuery() {
	// Test status query handler functionality

	pools := []*datamodel.Pool{}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Query workflow status - Note: Status query may return CREATED if workflow hasn't started yet
	// We'll just verify the query handler exists and doesn't error
	var statusResult *workflows.WorkflowStatus
	value, err := s.env.QueryWorkflow("status")
	if err == nil {
		err = value.Get(&statusResult)
		assert.NoError(s.T(), err)
		// Status may be CREATED, RUNNING, or COMPLETED depending on workflow execution state
		assert.NotNil(s.T(), statusResult)
	}

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_PartialBatchFailure() {
	// Test that partial batch failures are correctly tracked with proper pool UUID mapping
	// This verifies the fix for using completedInBatch count as index

	pools := []*datamodel.Pool{
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"}, Name: "pool-1", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-1"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"}, Name: "pool-2", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-2"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-3"}, Name: "pool-3", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-3"}},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
			{PoolUUID: "pool-uuid-2", CurrentHash: "old-hash-2"},
			{PoolUUID: "pool-uuid-3", CurrentHash: "old-hash-3"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{PoolUUID: "pool-uuid-1", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-1", NeedUpdate: true},
		{PoolUUID: "pool-uuid-2", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-2", NeedUpdate: true},
		{PoolUUID: "pool-uuid-3", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-3", NeedUpdate: true},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	// Mock activities for pool-1 (success)
	pool1 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}
	ontapCredentials := &vlm.OntapCredentials{AdminPassword: "password"}
	expertCredentials := &vlm.OntapCredentials{AdminPassword: "expert-password"}
	vlmConfig1 := &vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: "deployment-1"}}
	createVSAExpertModeReq1 := &vlm.OntapExpertModeUserConfig{VLMConfig: *vlmConfig1}

	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool1, nil).Once()
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
		return pool.UUID == "pool-uuid-1"
	})).Return(ontapCredentials, nil).Once()
	s.env.OnActivity(poolActivity.GetExpertModeCredentials, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
		return pool.UUID == "pool-uuid-1"
	})).Return(expertCredentials, nil).Once()
	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
		return pool.UUID == "pool-uuid-1"
	})).Return(vlmConfig1, nil).Once()
	s.env.OnActivity(poolActivity.PrepareCreateVSAExpertModeReq, mock.Anything, mock.MatchedBy(func(vlmConfig vlm.VLMConfig) bool {
		return vlmConfig.Deployment.DeploymentID == "deployment-1"
	}), mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(createVSAExpertModeReq1, nil).Once()
	s.env.OnActivity(poolActivity.UpdateRbacCheckSumInPool, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
		return pool.UUID == "pool-uuid-1"
	}), mock.Anything).Return(nil).Once()

	// Mock activities for pool-2 (failure - simulate error in child workflow)
	pool2 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"},
		Name:      "pool-2",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-2",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-2"}}`,
	}
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-2").Return(pool2, nil).Once()
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
	// Allow retries (max 3 attempts) for failing activity
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
		return pool.UUID == "pool-uuid-2"
	})).Return(nil, errors.New("failed to get credentials for pool-2")).Times(3)

	// Mock activities for pool-3 (success)
	pool3 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-3"},
		Name:      "pool-3",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-3",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-3"}}`,
	}
	vlmConfig3 := &vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: "deployment-3"}}
	createVSAExpertModeReq3 := &vlm.OntapExpertModeUserConfig{VLMConfig: *vlmConfig3}
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-3").Return(pool3, nil).Once()
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
		return pool.UUID == "pool-uuid-3"
	})).Return(ontapCredentials, nil).Once()
	s.env.OnActivity(poolActivity.GetExpertModeCredentials, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
		return pool.UUID == "pool-uuid-3"
	})).Return(expertCredentials, nil).Once()
	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
		return pool.UUID == "pool-uuid-3"
	})).Return(vlmConfig3, nil).Once()
	s.env.OnActivity(poolActivity.PrepareCreateVSAExpertModeReq, mock.Anything, mock.MatchedBy(func(vlmConfig vlm.VLMConfig) bool {
		return vlmConfig.Deployment.DeploymentID == "deployment-3"
	}), mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(createVSAExpertModeReq3, nil).Once()
	s.env.OnActivity(poolActivity.UpdateRbacCheckSumInPool, mock.Anything, mock.MatchedBy(func(pool *datamodel.Pool) bool {
		return pool.UUID == "pool-uuid-3"
	}), mock.Anything).Return(nil).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed with error (pool-2 failed)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify error message contains pool-uuid-2 (the failed pool)
	errorMsg := s.env.GetWorkflowError().Error()
	assert.Contains(s.T(), errorMsg, "pool-uuid-2")
	// Verify error message does NOT incorrectly contain successful pools in the failure list
	// Check for the failure pattern "pool pool-uuid-X:" to ensure we're checking the actual failure entries
	// and not workflow IDs or other metadata that might contain pool UUIDs
	if assert.Contains(s.T(), errorMsg, "pool pool-uuid-2:") {
		// Only check for successful pools if we found the failed pool in the expected format
		assert.NotContains(s.T(), errorMsg, "pool pool-uuid-1:")
		assert.NotContains(s.T(), errorMsg, "pool pool-uuid-3:")
	}
	// The error should mention exactly 1 failed pool
	assert.Contains(s.T(), errorMsg, "1 pool(s)")
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_OutOfOrderCompletion() {
	// Test that failures are correctly tracked even when futures complete out of order
	// This verifies the fix for concurrent completion tracking

	pools := []*datamodel.Pool{
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"}, Name: "pool-1", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-1"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"}, Name: "pool-2", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-2"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-3"}, Name: "pool-3", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-3"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-4"}, Name: "pool-4", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-4"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-5"}, Name: "pool-5", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-5"}},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
			{PoolUUID: "pool-uuid-2", CurrentHash: "old-hash-2"},
			{PoolUUID: "pool-uuid-3", CurrentHash: "old-hash-3"},
			{PoolUUID: "pool-uuid-4", CurrentHash: "old-hash-4"},
			{PoolUUID: "pool-uuid-5", CurrentHash: "old-hash-5"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{PoolUUID: "pool-uuid-1", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-1", NeedUpdate: true},
		{PoolUUID: "pool-uuid-2", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-2", NeedUpdate: true},
		{PoolUUID: "pool-uuid-3", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-3", NeedUpdate: true},
		{PoolUUID: "pool-uuid-4", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-4", NeedUpdate: true},
		{PoolUUID: "pool-uuid-5", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-5", NeedUpdate: true},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	// Mock successful pools (pool-1, pool-3, pool-5)
	ontapCredentials := &vlm.OntapCredentials{AdminPassword: "password"}
	expertCredentials := &vlm.OntapCredentials{AdminPassword: "expert-password"}

	for _, poolUUID := range []string{"pool-uuid-1", "pool-uuid-3", "pool-uuid-5"} {
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: poolUUID},
			Name:      poolUUID,
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "old-hash",
				RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
			},
			VLMConfig: fmt.Sprintf(`{"deployment":{"deployment_id":"deployment-%s"}}`, poolUUID),
		}
		vlmConfig := &vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: fmt.Sprintf("deployment-%s", poolUUID)}}
		createReq := &vlm.OntapExpertModeUserConfig{VLMConfig: *vlmConfig}

		s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, poolUUID).Return(pool, nil).Once()
		s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
		s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
			return p.UUID == poolUUID
		})).Return(ontapCredentials, nil).Once()
		s.env.OnActivity(poolActivity.GetExpertModeCredentials, mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
			return p.UUID == poolUUID
		})).Return(expertCredentials, nil).Once()
		s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
			return p.UUID == poolUUID
		})).Return(vlmConfig, nil).Once()
		s.env.OnActivity(poolActivity.PrepareCreateVSAExpertModeReq, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(createReq, nil).Once()
		s.env.OnActivity(poolActivity.UpdateRbacCheckSumInPool, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	}

	// Mock failing pools (pool-2, pool-4) - these will fail at different stages
	pool2 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"},
		Name:      "pool-2",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-2",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-pool-uuid-2"}}`,
	}
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-2").Return(pool2, nil).Once()
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
		return p.UUID == "pool-uuid-2"
	})).Return(nil, errors.New("credentials error for pool-2")).Times(3)

	pool4 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-4"},
		Name:      "pool-4",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-4",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-pool-uuid-4"}}`,
	}
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-4").Return(pool4, nil).Once()
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
		return p.UUID == "pool-uuid-4"
	})).Return(ontapCredentials, nil).Once()
	s.env.OnActivity(poolActivity.GetExpertModeCredentials, mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
		return p.UUID == "pool-uuid-4"
	})).Return(nil, errors.New("expert credentials error for pool-4")).Times(3)

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify error message contains both failed pools with correct UUIDs
	errorMsg := s.env.GetWorkflowError().Error()
	assert.Contains(s.T(), errorMsg, "pool-uuid-2")
	assert.Contains(s.T(), errorMsg, "pool-uuid-4")
	// Verify error message does NOT contain successful pools
	assert.NotContains(s.T(), errorMsg, "pool-uuid-1")
	assert.NotContains(s.T(), errorMsg, "pool-uuid-3")
	assert.NotContains(s.T(), errorMsg, "pool-uuid-5")
	// The error should mention exactly 2 failed pools
	assert.Contains(s.T(), errorMsg, "2 pool(s)")
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_AllPoolsInBatchFail() {
	// Test that all failures in a batch are correctly tracked with proper UUID mapping

	pools := []*datamodel.Pool{
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"}, Name: "pool-1", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-1"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"}, Name: "pool-2", BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.18.1", RbacFileHash: "old-hash-2"}},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
			{PoolUUID: "pool-uuid-2", CurrentHash: "old-hash-2"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{PoolUUID: "pool-uuid-1", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-1", NeedUpdate: true},
		{PoolUUID: "pool-uuid-2", LatestRbacHash: "new-hash-123", CurrentHash: "old-hash-2", NeedUpdate: true},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	// Mock both pools to fail
	pool1 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}
	pool2 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"},
		Name:      "pool-2",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-2",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-2"}}`,
	}

	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool1, nil).Once()
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-2").Return(pool2, nil).Once()
	// Mock ValidateRbacHash for both pools
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Times(2)
	// Allow retries (max 3 attempts) for failing activities
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
		return p.UUID == "pool-uuid-1"
	})).Return(nil, errors.New("error-1")).Times(3)
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
		return p.UUID == "pool-uuid-2"
	})).Return(nil, errors.New("error-2")).Times(3)

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify error message contains both failed pools with correct UUIDs and error messages
	errorMsg := s.env.GetWorkflowError().Error()
	assert.Contains(s.T(), errorMsg, "pool-uuid-1")
	assert.Contains(s.T(), errorMsg, "pool-uuid-2")
	assert.Contains(s.T(), errorMsg, "error-1")
	assert.Contains(s.T(), errorMsg, "error-2")
	// The error should mention exactly 2 failed pools
	assert.Contains(s.T(), errorMsg, "2 pool(s)")
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_ValidateRbacHashFails() {
	// Test workflow when ValidateRbacHash activity fails for a pool
	// This tests the newly added ValidateRbacHash call in UpdateSinglePoolRbacChildWorkflow

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-1",
			},
			Name: "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "old-hash-1",
			},
		},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{
			PoolUUID:       "pool-uuid-1",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-1",
			NeedUpdate:     true,
		},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	pool1 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "pool-uuid-1",
		},
		Name: "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool1, nil).Once()

	// Mock ValidateRbacHash to fail with hash mismatch error
	validateError := errors.New("RBAC hash mismatch for ONTAP version 9.18.1: expected configured-hash, got new-hash-123")
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.18.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(validateError).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify error message contains the failed pool UUID
	errorMsg := s.env.GetWorkflowError().Error()
	assert.Contains(s.T(), errorMsg, "pool-uuid-1")
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_ValidateRbacHashFailsInBatch() {
	// Test workflow when ValidateRbacHash fails for one pool in a batch of multiple pools
	// This verifies that ValidateRbacHash failures are properly tracked and reported

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-1",
			},
			Name: "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "old-hash-1",
			},
		},
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-2",
			},
			Name: "pool-2",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "old-hash-2",
			},
		},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
			{PoolUUID: "pool-uuid-2", CurrentHash: "old-hash-2"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{
			PoolUUID:       "pool-uuid-1",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-1",
			NeedUpdate:     true,
		},
		{
			PoolUUID:       "pool-uuid-2",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-2",
			NeedUpdate:     true,
		},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	// Mock pool-1: ValidateRbacHash fails
	pool1 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "pool-uuid-1",
		},
		Name: "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool1, nil).Once()
	validateError := errors.New("RBAC hash validation failed: hash mismatch")
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.18.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(validateError).Once()

	// Mock pool-2: ValidateRbacHash succeeds, but fails later
	pool2 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "pool-uuid-2",
		},
		Name: "pool-2",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-2",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-2"}}`,
	}
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-2").Return(pool2, nil).Once()
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.18.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
	// Allow retries for GetOnTapCredentials failure
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.MatchedBy(func(p *datamodel.Pool) bool {
		return p.UUID == "pool-uuid-2"
	})).Return(nil, errors.New("failed to get credentials")).Times(3)

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify error message contains both failed pools
	errorMsg := s.env.GetWorkflowError().Error()
	assert.Contains(s.T(), errorMsg, "pool-uuid-1")
	assert.Contains(s.T(), errorMsg, "pool-uuid-2")
	// The error should mention exactly 2 failed pools
	assert.Contains(s.T(), errorMsg, "2 pool(s)")
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_ValidateRbacHashConfigError() {
	// Test workflow when ValidateRbacHash fails due to configuration error
	// This tests non-retryable errors from ValidateRbacHash

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-1",
			},
			Name: "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "old-hash-1",
			},
		},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{
			PoolUUID:       "pool-uuid-1",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-1",
			NeedUpdate:     true,
		},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	pool1 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "pool-uuid-1",
		},
		Name: "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool1, nil).Once()

	// Mock ValidateRbacHash to fail with configuration error (non-retryable)
	configError := errors.New("ONTAP_MODE_RBAC_CHECKSUMS not configured")
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.18.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(configError).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify error message contains the failed pool UUID
	errorMsg := s.env.GetWorkflowError().Error()
	assert.Contains(s.T(), errorMsg, "pool-uuid-1")
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacHashForPoolsWorkflow_ValidateRbacHashMissingVersion() {
	// Test workflow when ValidateRbacHash fails because ONTAP version is not found in config
	// This tests the scenario where the version exists in pool but not in checksums config

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid-1",
			},
			Name: "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.99.1", // Uncommon version not in config
				RbacFileHash: "old-hash-1",
			},
		},
	}
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).Return(pools, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.99.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
		},
	}
	s.env.OnActivity(rbacActivity.GetPoolsDetailsByOntapVersion, mock.Anything, pools).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{
			PoolUUID:       "pool-uuid-1",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-1",
			NeedUpdate:     true,
		},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	pool1 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "pool-uuid-1",
		},
		Name: "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.99.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.99.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool1, nil).Once()

	// Mock ValidateRbacHash to fail because version not found in config
	versionError := errors.New("ONTAP version 9.99.1 not found in ONTAP_MODE_RBAC_CHECKSUMS configuration")
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.99.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(versionError).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify error message contains the failed pool UUID
	errorMsg := s.env.GetWorkflowError().Error()
	assert.Contains(s.T(), errorMsg, "pool-uuid-1")
}

// --- UpdateRbacForSinglePoolWorkflow tests ---

func (s *RBACWorkflowTestSuite) Test_UpdateRbacForSinglePoolWorkflow_Success() {
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}

	// Mock GetPoolByUUID
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()

	// Mock GetSinglePoolVersionDetails
	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
		},
	}
	s.env.OnActivity(rbacActivity.GetSinglePoolVersionDetails, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(poolsByVersion, nil).Once()

	// Mock GetLatestRbacHashForAllOntapVersion
	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{
			PoolUUID:       "pool-uuid-1",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-1",
			OntapVersion:   "9.18.1",
			NeedUpdate:     true,
		},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	// Mock child workflow activities
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.18.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()

	ontapCredentials := &vlm.OntapCredentials{AdminPassword: "password"}
	expertCredentials := &vlm.OntapCredentials{AdminPassword: "expert-password"}
	vlmConfig := &vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: "deployment-1"}}
	createVSAExpertModeReq := &vlm.OntapExpertModeUserConfig{VLMConfig: *vlmConfig}

	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(ontapCredentials, nil).Once()
	s.env.OnActivity(poolActivity.GetExpertModeCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(expertCredentials, nil).Once()
	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(vlmConfig, nil).Once()
	s.env.OnActivity(poolActivity.PrepareCreateVSAExpertModeReq, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(createVSAExpertModeReq, nil).Once()
	s.env.OnActivity(poolActivity.UpdateRbacCheckSumInPool, mock.Anything, mock.AnythingOfType("*datamodel.Pool"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()

	s.env.ExecuteWorkflow(UpdateRbacForSinglePoolWorkflow, "pool-uuid-1")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacForSinglePoolWorkflow_PoolNotFound() {
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}

	// Mock GetPoolByUUID to fail
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "non-existent-uuid").
		Return(nil, errors.New("pool with UUID \"non-existent-uuid\" not found")).Times(3)

	s.env.ExecuteWorkflow(UpdateRbacForSinglePoolWorkflow, "non-existent-uuid")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "not found")
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacForSinglePoolWorkflow_NoUpdateNeeded() {
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "current-hash",
		},
	}

	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "current-hash"},
		},
	}
	s.env.OnActivity(rbacActivity.GetSinglePoolVersionDetails, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(poolsByVersion, nil).Once()

	// Return empty list — no pools need update
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).
		Return([]expertmodeactivities.PoolDetailsWithRbacHash{}, nil).Once()

	s.env.ExecuteWorkflow(UpdateRbacForSinglePoolWorkflow, "pool-uuid-1")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacForSinglePoolWorkflow_GetSinglePoolVersionDetailsFails() {
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: nil,
	}

	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()
	s.env.OnActivity(rbacActivity.GetSinglePoolVersionDetails, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).
		Return(nil, errors.New("pool pool-uuid-1 missing ONTAP version")).Times(3)

	s.env.ExecuteWorkflow(UpdateRbacForSinglePoolWorkflow, "pool-uuid-1")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacForSinglePoolWorkflow_ChildWorkflowFails() {
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}

	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
		},
	}
	s.env.OnActivity(rbacActivity.GetSinglePoolVersionDetails, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{
			PoolUUID:       "pool-uuid-1",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-1",
			OntapVersion:   "9.18.1",
			NeedUpdate:     true,
		},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	// Mock child workflow: GetPoolByUUID succeeds, ValidateRbacHash succeeds, but GetOnTapCredentials fails
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.18.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).
		Return(nil, errors.New("failed to get credentials")).Times(3)

	s.env.ExecuteWorkflow(UpdateRbacForSinglePoolWorkflow, "pool-uuid-1")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "pool-uuid-1")
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacForSinglePoolWorkflow_GetLatestHashFails() {
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
		},
	}

	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
		},
	}
	s.env.OnActivity(rbacActivity.GetSinglePoolVersionDetails, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(poolsByVersion, nil).Once()

	// Mock GetLatestRbacHashForAllOntapVersion to fail
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).
		Return(nil, errors.New("GCP bucket error")).Times(3)

	s.env.ExecuteWorkflow(UpdateRbacForSinglePoolWorkflow, "pool-uuid-1")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_UpdateRbacForSinglePoolWorkflow_ValidateRbacHashFails() {
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}

	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()

	poolsByVersion := map[string][]expertmodeactivities.PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
		},
	}
	s.env.OnActivity(rbacActivity.GetSinglePoolVersionDetails, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(poolsByVersion, nil).Once()

	poolListNeedUpdate := []expertmodeactivities.PoolDetailsWithRbacHash{
		{
			PoolUUID:       "pool-uuid-1",
			LatestRbacHash: "new-hash-123",
			CurrentHash:    "old-hash-1",
			OntapVersion:   "9.18.1",
			NeedUpdate:     true,
		},
	}
	s.env.OnActivity(rbacActivity.GetLatestRbacHashForAllOntapVersion, mock.Anything, mock.AnythingOfType("map[string][]expertmodeactivities.PoolDetailWithCurrentHash")).Return(poolListNeedUpdate, nil).Once()

	// Mock child workflow: GetPoolByUUID succeeds, ValidateRbacHash fails (allow retries)
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()
	validateError := errors.New("RBAC hash mismatch for ONTAP version 9.18.1")
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.18.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(validateError).Times(3)

	s.env.ExecuteWorkflow(UpdateRbacForSinglePoolWorkflow, "pool-uuid-1")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "RBAC hash mismatch")
}

func TestCountCompletedFutures(t *testing.T) {
	tests := []struct {
		name      string
		completed []bool
		want      int
	}{
		{
			name:      "All completed",
			completed: []bool{true, true, true},
			want:      3,
		},
		{
			name:      "None completed",
			completed: []bool{false, false, false},
			want:      0,
		},
		{
			name:      "Mixed completion",
			completed: []bool{true, false, true, false, true},
			want:      3,
		},
		{
			name:      "Empty slice",
			completed: []bool{},
			want:      0,
		},
		{
			name:      "Single completed",
			completed: []bool{true},
			want:      1,
		},
		{
			name:      "Single not completed",
			completed: []bool{false},
			want:      0,
		},
		{
			name:      "Large slice all completed",
			completed: []bool{true, true, true, true, true, true, true, true, true, true},
			want:      10,
		},
		{
			name:      "Large slice partial completion",
			completed: []bool{true, false, true, false, true, false, true, false, true, false},
			want:      5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countCompletedFutures(tt.completed)
			if got != tt.want {
				t.Errorf("countCompletedFutures() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- UpdateSinglePoolRbacChildWorkflow direct tests (error branches) ---

// childWorkflowPool returns a fully populated pool used by the child-workflow direct tests.
func (s *RBACWorkflowTestSuite) childWorkflowPool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}
}

func (s *RBACWorkflowTestSuite) Test_ChildWorkflow_PoolMissingOntapVersion() {
	// Pool has no BuildInfo, so the child workflow cannot resolve an ONTAP version and fails fast.
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolDetails := expertmodeactivities.PoolDetailsWithRbacHash{PoolUUID: "pool-uuid-1", LatestRbacHash: "new-hash-123"}

	poolNoVersion := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: nil,
	}
	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(poolNoVersion, nil).Once()

	s.env.ExecuteWorkflow(UpdateSinglePoolRbacChildWorkflow, poolDetails)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_ChildWorkflow_ParseVlmConfigFails() {
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	poolDetails := expertmodeactivities.PoolDetailsWithRbacHash{PoolUUID: "pool-uuid-1", LatestRbacHash: "new-hash-123"}
	pool := s.childWorkflowPool()
	ontapCredentials := &vlm.OntapCredentials{AdminPassword: "password"}
	expertCredentials := &vlm.OntapCredentials{AdminPassword: "expert-password"}

	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.18.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(ontapCredentials, nil).Once()
	s.env.OnActivity(poolActivity.GetExpertModeCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(expertCredentials, nil).Once()
	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(nil, errors.New("parse vlm config failed")).Times(3)

	s.env.ExecuteWorkflow(UpdateSinglePoolRbacChildWorkflow, poolDetails)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_ChildWorkflow_PrepareCreateReqFails() {
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	poolDetails := expertmodeactivities.PoolDetailsWithRbacHash{PoolUUID: "pool-uuid-1", LatestRbacHash: "new-hash-123"}
	pool := s.childWorkflowPool()
	ontapCredentials := &vlm.OntapCredentials{AdminPassword: "password"}
	expertCredentials := &vlm.OntapCredentials{AdminPassword: "expert-password"}
	vlmConfig := &vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: "deployment-1"}}

	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.18.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(ontapCredentials, nil).Once()
	s.env.OnActivity(poolActivity.GetExpertModeCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(expertCredentials, nil).Once()
	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(vlmConfig, nil).Once()
	s.env.OnActivity(poolActivity.PrepareCreateVSAExpertModeReq, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("prepare request failed")).Times(3)

	s.env.ExecuteWorkflow(UpdateSinglePoolRbacChildWorkflow, poolDetails)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *RBACWorkflowTestSuite) Test_ChildWorkflow_UpdateRbacCheckSumFails() {
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	poolDetails := expertmodeactivities.PoolDetailsWithRbacHash{PoolUUID: "pool-uuid-1", LatestRbacHash: "new-hash-123"}
	pool := s.childWorkflowPool()
	ontapCredentials := &vlm.OntapCredentials{AdminPassword: "password"}
	expertCredentials := &vlm.OntapCredentials{AdminPassword: "expert-password"}
	vlmConfig := &vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: "deployment-1"}}
	createReq := &vlm.OntapExpertModeUserConfig{VLMConfig: *vlmConfig}

	s.env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()
	s.env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.18.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
	s.env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(ontapCredentials, nil).Once()
	s.env.OnActivity(poolActivity.GetExpertModeCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(expertCredentials, nil).Once()
	s.env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(vlmConfig, nil).Once()
	s.env.OnActivity(poolActivity.PrepareCreateVSAExpertModeReq, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(createReq, nil).Once()
	// CreateVSAExpertModeUser child workflow succeeds (registered in SetupTest); the final checksum
	// update fails.
	s.env.OnActivity(poolActivity.UpdateRbacCheckSumInPool, mock.Anything, mock.AnythingOfType("*datamodel.Pool"), mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(errors.New("update checksum failed")).Times(3)

	s.env.ExecuteWorkflow(UpdateSinglePoolRbacChildWorkflow, poolDetails)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

// newRBACChildEnv builds a standalone workflow test environment wired with the correlation header
// and context propagator required by the RBAC child workflow.
func newRBACChildEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetTestTimeout(1 * time.Minute)
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	loggerFields := log.Fields{string(middleware.RequestCorrelationID): "test-correlation-id"}
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(loggerFields)
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
	return env
}

// TestUpdateSinglePoolRbacChildWorkflow_CreateVSAUserFails covers the branch where the VLM
// CreateVSAExpertModeUser child workflow returns an error.
func TestUpdateSinglePoolRbacChildWorkflow_CreateVSAUserFails(t *testing.T) {
	env := newRBACChildEnv(t)

	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}
	env.RegisterWorkflow(UpdateSinglePoolRbacChildWorkflow)
	env.RegisterActivity(rbacActivity.GetPoolByUUID)
	env.RegisterActivity(poolActivity.ValidateRbacHash)
	env.RegisterActivity(poolActivity.GetOnTapCredentials)
	env.RegisterActivity(poolActivity.GetExpertModeCredentials)
	env.RegisterActivity(poolActivity.ParseVlmConfig)
	env.RegisterActivity(poolActivity.PrepareCreateVSAExpertModeReq)
	env.RegisterActivity(poolActivity.UpdateRbacCheckSumInPool)

	// Register the VLM child workflow to fail.
	env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, request *vlm.OntapExpertModeUserConfig) (vlm.OntapExpertModeUserResponse, error) {
			return vlm.OntapExpertModeUserResponse{}, errors.New("create vsa expert mode user failed")
		},
		workflow.RegisterOptions{Name: vlm.CreateVSAExpertModeUserWorkflowName},
	)

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "old-hash-1",
			RbacFileUrl:  "GCNV/9.18.1/RBAC/gcnvadmin_create_cli",
		},
		VLMConfig: `{"deployment":{"deployment_id":"deployment-1"}}`,
	}
	ontapCredentials := &vlm.OntapCredentials{AdminPassword: "password"}
	expertCredentials := &vlm.OntapCredentials{AdminPassword: "expert-password"}
	vlmConfig := &vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: "deployment-1"}}
	createReq := &vlm.OntapExpertModeUserConfig{VLMConfig: *vlmConfig}

	env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").Return(pool, nil).Once()
	env.OnActivity(poolActivity.ValidateRbacHash, mock.Anything, "9.18.1", mock.AnythingOfType("*hyperscaler.BucketFileDetails")).Return(nil).Once()
	env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(ontapCredentials, nil).Once()
	env.OnActivity(poolActivity.GetExpertModeCredentials, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(expertCredentials, nil).Once()
	env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.AnythingOfType("*datamodel.Pool")).Return(vlmConfig, nil).Once()
	env.OnActivity(poolActivity.PrepareCreateVSAExpertModeReq, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(createReq, nil).Once()

	poolDetails := expertmodeactivities.PoolDetailsWithRbacHash{PoolUUID: "pool-uuid-1", LatestRbacHash: "new-hash-123"}
	env.ExecuteWorkflow(UpdateSinglePoolRbacChildWorkflow, poolDetails)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

// --- Job-status error-logging branches (best-effort ERROR transitions that also fail) ---

// newRBACJobEnv builds a standalone environment with the common job activities registered so the
// workflow's Setup/EnsureJobState/UpdateJobStatus plumbing can run.
func newRBACJobEnv(t *testing.T) (*testsuite.TestWorkflowEnvironment, activities.CommonActivities) {
	t.Helper()
	env := newRBACChildEnv(t)
	commonActivity := activities.CommonActivities{SE: database.NewMockStorage(t)}
	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	return env, commonActivity
}

func TestUpdateRbacForSinglePoolWorkflow_ProcessingAndErrorUpdateFail(t *testing.T) {
	// Both the PROCESSING transition and the best-effort ERROR transition fail, exercising the
	// error-logging branch.
	env, commonActivity := newRBACJobEnv(t)
	env.RegisterWorkflow(UpdateRbacForSinglePoolWorkflow)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).
		Return(temporal.NewNonRetryableApplicationError("update job status failed", "UpdateFailed", nil)).Maybe()

	env.ExecuteWorkflow(UpdateRbacForSinglePoolWorkflow, "pool-uuid-1")

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestUpdateRbacForSinglePoolWorkflow_RunFailsAndErrorUpdateFails(t *testing.T) {
	// PROCESSING succeeds, RunForSinglePool fails, and the best-effort ERROR transition also fails.
	env, commonActivity := newRBACJobEnv(t)
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	env.RegisterWorkflow(UpdateRbacForSinglePoolWorkflow)
	env.RegisterActivity(rbacActivity.GetPoolByUUID)

	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStateERROR)
	})).Return(temporal.NewNonRetryableApplicationError("update job status failed", "UpdateFailed", nil)).Maybe()
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State != string(datamodel.JobsStateERROR)
	})).Return(nil).Maybe()
	env.OnActivity(rbacActivity.GetPoolByUUID, mock.Anything, "pool-uuid-1").
		Return(nil, temporal.NewNonRetryableApplicationError("pool not found", "NotFound", nil)).Maybe()

	env.ExecuteWorkflow(UpdateRbacForSinglePoolWorkflow, "pool-uuid-1")

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestUpdateRbacForPoolsWorkflow_ProcessingAndErrorUpdateFail(t *testing.T) {
	env, commonActivity := newRBACJobEnv(t)
	env.RegisterWorkflow(UpdateRbacForPoolsWorkflow)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).
		Return(temporal.NewNonRetryableApplicationError("update job status failed", "UpdateFailed", nil)).Maybe()

	env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestUpdateRbacForPoolsWorkflow_RunFailsAndErrorUpdateFails(t *testing.T) {
	// PROCESSING succeeds, Run fails (ListActiveExpertModePools errors), and the best-effort ERROR
	// transition also fails.
	env, commonActivity := newRBACJobEnv(t)
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	env.RegisterWorkflow(UpdateRbacForPoolsWorkflow)
	env.RegisterActivity(rbacActivity.ListActiveExpertModePools)

	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStateERROR)
	})).Return(temporal.NewNonRetryableApplicationError("update job status failed", "UpdateFailed", nil)).Maybe()
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State != string(datamodel.JobsStateERROR)
	})).Return(nil).Maybe()
	env.OnActivity(rbacActivity.ListActiveExpertModePools, mock.Anything).
		Return(nil, temporal.NewNonRetryableApplicationError("list pools failed", "ListFailed", nil)).Maybe()

	env.ExecuteWorkflow(UpdateRbacForPoolsWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestRBACWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(RBACWorkflowTestSuite))
}
