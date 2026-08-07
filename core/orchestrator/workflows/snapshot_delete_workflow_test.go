package workflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type SnapshotDeleteTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *SnapshotDeleteTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	s.env.SetHeader(mockHeader)

	// Register workflow
	s.env.RegisterWorkflow(DeleteSnapshotWorkflow)
}

func (s *SnapshotDeleteTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func (s *SnapshotDeleteTestSuite) Test_DeleteSnapshotWorkflow_Success() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.SnapshotDeleteActivity{SE: mockStorage}

	// Mock UpdateJob method calls
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "DONE", 0, "").Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshot)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshot, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	snapshot := &datamodel.Snapshot{
		Volume: &datamodel.Volume{
			Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				}},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
	}
	deleteSnapParams := &common.DeleteSnapshotParams{}
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, deleteSnapParams, snapshot)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotDeleteTestSuite) Test_DeleteSnapshotWorkflow_Failure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.SnapshotDeleteActivity{SE: mockStorage}

	// Mock UpdateJob method calls
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "ERROR", 1011, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshot)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshot, mock.Anything, mock.Anything).Return(errors.New("failed to update snapshot details"))
	// Snapshot has no ExternalUUID so ONTAP is untouched when DeleteSnapshot fails.
	// Rollback restores snapshot to READY via UpdateDeleteSnapshotDetails.
	s.env.OnActivity(deleteActivity.UpdateDeleteSnapshotDetails, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	snapshot := &datamodel.Snapshot{
		Volume: &datamodel.Volume{
			Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				}},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
	}
	deleteSnapParams := &common.DeleteSnapshotParams{}
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, deleteSnapParams, snapshot)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "workflow execution error")
}

func (s *SnapshotDeleteTestSuite) TestDeleteSnapshotWorkflowFailsOnSetupError() {
	// Test with invalid params to trigger setup error
	params := &common.DeleteSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "", // Empty account name to trigger error
		},
	}
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{ID: int64(1)},
		Account:   &datamodel.Account{Name: "test-account"},
		Volume:    &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: int64(1)}, Name: "test-volume"},
	}

	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotDeleteTestSuite) TestDeleteSnapshotWorkflowFailsOnJobStatusUpdateError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteSnapshotActivity := activities.SnapshotDeleteActivity{SE: mockStorage}

	params := &common.DeleteSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{ID: int64(1)},
		Account:   &datamodel.Account{Name: "test-account"},
		Volume:    &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: int64(1)}, Name: "test-volume"},
	}

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(deleteSnapshotActivity.DeleteSnapshotInONTAP)
	s.env.RegisterActivity(deleteSnapshotActivity.DeleteSnapshot)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Mock UpdateJob to return error for PROCESSING state
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(assert.AnError)

	// Execute workflow
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotDeleteTestSuite) TestDeleteSnapshotWorkflowCompletesDespiteFinalJobStatusUpdateError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteSnapshotActivity := activities.SnapshotDeleteActivity{SE: mockStorage}

	params := &common.DeleteSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{ID: int64(1)},
		Account:   &datamodel.Account{Name: "test-account"},
		Volume: &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			Name:      "test-volume",
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
	}

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(deleteSnapshotActivity.DeleteSnapshotInONTAP)
	s.env.RegisterActivity(deleteSnapshotActivity.DeleteSnapshot)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Mock UpdateJob method calls
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "DONE", 0, "").Return(assert.AnError)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteSnapshotActivity.DeleteSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteSnapshotActivity.DeleteSnapshot, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotDeleteTestSuite) TestDeleteSnapshotWorkflowFailsOnActivityError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteSnapshotActivity := activities.SnapshotDeleteActivity{SE: mockStorage}

	params := &common.DeleteSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	// Snapshot has no ExternalUUID — GetNode fails, no ONTAP side effect.
	// Rollback must restore snapshot to READY via UpdateDeleteSnapshotDetails.
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{ID: int64(1)},
		Account:   &datamodel.Account{Name: "test-account"},
		Volume:    &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: int64(1)}, Name: "test-volume"},
	}

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(deleteSnapshotActivity.DeleteSnapshotInONTAP)
	s.env.RegisterActivity(deleteSnapshotActivity.DeleteSnapshot)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Mock UpdateJob method calls
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "ERROR", 1011, mock.Anything).Return(nil)

	// GetNode fails — this is the activity that errors in this test.
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return(nil, assert.AnError)
	// Rollback fires with READY state (no ONTAP side effect when GetNode fails).
	s.env.OnActivity(deleteSnapshotActivity.UpdateDeleteSnapshotDetails, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotDeleteTestSuite) TestDeleteSnapshotWorkflowFailsOnJobInErrorState() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}

	params := &common.DeleteSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{ID: int64(1)},
		Account:   &datamodel.Account{Name: "test-account"},
		Volume:    &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: int64(1)}, Name: "test-volume"},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)

	// Mock GetJob to return job in ERROR state
	jobInErrorState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateERROR),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInErrorState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInErrorState, nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, params, snapshot)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "job default-test-workflow-id is in state ERROR; expected NEW")
}

func (s *SnapshotDeleteTestSuite) TestDeleteSnapshotWorkflowSucceedsWhenJobNotInErrorState() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.SnapshotDeleteActivity{SE: mockStorage}

	// Mock UpdateJob method calls
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "DONE", 0, "").Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshot)

	// Mock GetJob to return job in NEW state (not ERROR)
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshot, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	snapshot := &datamodel.Snapshot{
		Volume: &datamodel.Volume{
			Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				}},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
	}
	deleteSnapParams := &common.DeleteSnapshotParams{}
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, deleteSnapParams, snapshot)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotDeleteTestSuite) Test_DeleteSnapshotWorkflow_CancellationHandlingWhenSnapshotInCreatingState() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.SnapshotDeleteActivity{SE: mockStorage}
	poolActivity := &activities.PoolActivity{}
	cancellationActivity := &activities.CancellationActivity{}

	// Mock UpdateJob method calls
	// 1. For the delete workflow job status updates
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "DONE", 0, "").Return(nil)
	// 2. For the create job update (from cancellation handler) - TrackingID is ErrInternalServerError (1011)
	mockStorage.On("UpdateJob", mock.Anything, "create-job-uuid", "ERROR", 1011, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshot)
	s.env.RegisterActivity(poolActivity.GetCreateJobByResourceUUID)
	s.env.RegisterActivity(cancellationActivity.IsWorkflowRunningActivity)
	s.env.RegisterActivity(cancellationActivity.SendCancelSignalActivity)
	s.env.RegisterActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity)
	s.env.RegisterActivity(cancellationActivity.ForceCancelWorkflowActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Mock GetSnapshot to return snapshot in CREATING state
	dbSnapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
		Name:      "test-snapshot",
		State:     datamodel.LifeCycleStateCreating,
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				}},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
	}

	// Mock GetCreateJobByResourceUUID to return create job
	createJobResult := &common.CreateJobResult{
		JobUUID:    "create-job-uuid",
		WorkflowID: "create-workflow-id",
	}
	s.env.OnActivity(poolActivity.GetCreateJobByResourceUUID, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(createJobResult, nil)

	// Mock cancellation activities
	s.env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	// Don't mock UpdateJobStatus activity - let it execute and use the storage mock
	// The activity will call mockStorage.UpdateJob which we've already mocked above

	// Mock GetNode activity (needed for the delete workflow to continue after cancellation handling)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// Mock delete activities (snapshot has no external UUID, so DeleteSnapshotInONTAP won't be called)
	s.env.OnActivity(deleteActivity.DeleteSnapshot, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	deleteSnapParams := &common.DeleteSnapshotParams{}
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, deleteSnapParams, dbSnapshot)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotDeleteTestSuite) Test_DeleteSnapshotWorkflow_CancellationErrorHandling() {
	// Test to cover line 125: when cancellation handling returns an error, it should log a warning and proceed
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.SnapshotDeleteActivity{SE: mockStorage}
	poolActivity := &activities.PoolActivity{}
	cancellationActivity := &activities.CancellationActivity{}

	// Mock UpdateJob method calls
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "DONE", 0, "").Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshot)
	s.env.RegisterActivity(poolActivity.GetCreateJobByResourceUUID)
	s.env.RegisterActivity(cancellationActivity.IsWorkflowRunningActivity)
	s.env.RegisterActivity(cancellationActivity.SendCancelSignalActivity)
	s.env.RegisterActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity)
	s.env.RegisterActivity(cancellationActivity.ForceCancelWorkflowActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Create snapshot in CREATING state to trigger cancellation handling
	dbSnapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
		Name:      "test-snapshot",
		State:     datamodel.LifeCycleStateCreating,
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
	}

	// Mock GetCreateJobByResourceUUID to return create job
	createJobResult := &common.CreateJobResult{
		JobUUID:    "create-job-uuid",
		WorkflowID: "create-workflow-id",
	}
	s.env.OnActivity(poolActivity.GetCreateJobByResourceUUID, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(createJobResult, nil)

	// Mock cancellation activities to return an error - this will trigger line 125
	s.env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, mock.Anything).Return(false, errors.New("failed to check workflow status"))

	// Mock GetNode activity (needed for the delete workflow to continue after cancellation handling)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// Mock delete activities (snapshot has no external UUID, so DeleteSnapshotInONTAP won't be called)
	s.env.OnActivity(deleteActivity.DeleteSnapshot, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	deleteSnapParams := &common.DeleteSnapshotParams{}
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, deleteSnapParams, dbSnapshot)

	// Assert workflow completed successfully despite cancellation error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotDeleteTestSuite) Test_DeleteSnapshotWorkflow_WithExternalUUID() {
	// Test to cover lines 149-151: DeleteSnapshotInONTAP when snapshot has external UUID
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.SnapshotDeleteActivity{SE: mockStorage}

	// Mock UpdateJob method calls
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "DONE", 0, "").Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshot)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Create snapshot with external UUID to trigger DeleteSnapshotInONTAP
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
		Name:      "test-snapshot",
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			ExternalUUID: "external-uuid-123",
		},
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
	}

	// Mock activities - GetNode must return nodes
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// Mock DeleteSnapshotInONTAP - this should be called because snapshot has external UUID (lines 149-151)
	s.env.OnActivity(deleteActivity.DeleteSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	// Mock DeleteSnapshot
	s.env.OnActivity(deleteActivity.DeleteSnapshot, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	deleteSnapParams := &common.DeleteSnapshotParams{}
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, deleteSnapParams, snapshot)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_DeleteSnapshotWorkflow_OntapDeletedDBDeleteFails covers the D2 compensation path:
// DeleteSnapshotInONTAP succeeds (ONTAP snapshot is gone) but the subsequent DB soft-delete
// fails. In this case the rollback must set the snapshot to ERROR (not READY) because restoring
// to READY would create a phantom DB record for a non-existent ONTAP snapshot.
func (s *SnapshotDeleteTestSuite) Test_DeleteSnapshotWorkflow_OntapDeletedDBDeleteFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.SnapshotDeleteActivity{SE: mockStorage}

	// Mock UpdateJob method calls
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "ERROR", 1011, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshot)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Snapshot with ExternalUUID: DeleteSnapshotInONTAP will be called.
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
		Name:      "test-snapshot",
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			ExternalUUID: "external-uuid-123",
		},
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
		Account: &datamodel.Account{Name: "test_account"},
	}

	// ONTAP delete succeeds — snapshot is gone from ONTAP after this point.
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// DB soft-delete fails — triggers rollback with ERROR state (not READY).
	s.env.OnActivity(deleteActivity.DeleteSnapshot, mock.Anything, mock.Anything).Return(errors.New("database connection error"))
	// Rollback: snapshot must be set to ERROR, not READY, since the ONTAP snapshot no longer exists.
	// arg 0 = ctx, arg 1 = snapshot pointer (check State == ERROR), arg 2 = errorMessage appended by RollbackManager.
	s.env.OnActivity(deleteActivity.UpdateDeleteSnapshotDetails, mock.Anything, mock.MatchedBy(func(snap *datamodel.Snapshot) bool {
		return snap.State == datamodel.LifeCycleStateError
	}), mock.Anything).Return(nil)

	deleteSnapParams := &common.DeleteSnapshotParams{}
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, deleteSnapParams, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "workflow execution error")
}

// Test_DeleteSnapshotWorkflow_OntapDeleteFailsWithExternalUUID covers the case where the
// snapshot has an ExternalUUID but DeleteSnapshotInONTAP fails. Because the ONTAP snapshot
// still exists, the rollback must restore the record to READY (not ERROR). If it were
// set to ERROR the DB would show an error state even though the ONTAP snapshot is intact.
func (s *SnapshotDeleteTestSuite) Test_DeleteSnapshotWorkflow_OntapDeleteFailsWithExternalUUID() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.SnapshotDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "ERROR", 1011, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotInONTAP)

	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
		Name:      "test-snapshot",
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			ExternalUUID: "external-uuid-123",
		},
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				BaseModel:       datamodel.BaseModel{ID: int64(1)},
				PoolCredentials: &datamodel.PoolCredentials{Password: "password"},
			},
		},
		Account: &datamodel.Account{Name: "test_account"},
	}

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	// ONTAP delete fails — snapshot still exists on ONTAP.
	s.env.OnActivity(deleteActivity.DeleteSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("ontap unreachable"))
	// Rollback: snapshot must remain READY because ONTAP snapshot was NOT deleted.
	// arg 0 = ctx, arg 1 = snapshot pointer (check State == READY), arg 2 = errorMessage from RollbackManager.
	s.env.OnActivity(deleteActivity.UpdateDeleteSnapshotDetails, mock.Anything, mock.MatchedBy(func(snap *datamodel.Snapshot) bool {
		return snap.State == datamodel.LifeCycleStateREADY
	}), mock.Anything).Return(nil)

	deleteSnapParams := &common.DeleteSnapshotParams{}
	s.env.ExecuteWorkflow(DeleteSnapshotWorkflow, deleteSnapParams, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "workflow execution error")
}

func TestSnapshotDeleteTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotDeleteTestSuite))
}
