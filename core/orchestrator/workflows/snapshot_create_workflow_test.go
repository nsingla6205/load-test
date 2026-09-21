package workflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type SnapshotUnitTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *SnapshotUnitTestSuite) SetupTest() {
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
	s.env.RegisterWorkflow(CreateSnapshotWorkflow)
}

func (s *SnapshotUnitTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func (s *SnapshotUnitTestSuite) TestCreateSnapshotWorkflowWorkflowExecutesSuccessfully() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	snapshotCreateActivity := activities.SnapshotCreateActivity{SE: mockStorage}

	params := &common.CreateSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	snapshot := &datamodel.Snapshot{
		Name: "test-snapshot",
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			SizeInBytes:            0,
			LogicalSizeUsedInBytes: 0,
		},
	}

	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&snapshotCreateActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Mock UpdateJob method calls - these are called by the UpdateJobStatus activity
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "DONE", 0, "").Return(nil)
	var persistedSnapshot *datamodel.Snapshot

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(snapshotCreateActivity.CreateSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snapshot-uuid"}, SizeInBytes: 1024, LogicalSizeInBytes: 1024}, nil)
	s.env.OnActivity(snapshotCreateActivity.UpdateSnapshotDetails, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			persistedSnapshot, _ = args[1].(*datamodel.Snapshot)
		}).Return(nil)

	s.env.ExecuteWorkflow(CreateSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.Require().NotNil(persistedSnapshot)
	assert.Equal(s.T(), "snapshot-uuid", persistedSnapshot.ExternalUUID)
	s.Require().NotNil(persistedSnapshot.SnapshotAttributes)
	assert.Equal(s.T(), "snapshot-uuid", persistedSnapshot.SnapshotAttributes.ExternalUUID)
}

func (s *SnapshotUnitTestSuite) TestCreateSnapshotWorkflowFailsOnActivityError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	snapshotCreateActivity := activities.SnapshotCreateActivity{SE: mockStorage}

	params := &common.CreateSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		Name: "test-snapshot",
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			SizeInBytes:            0,
			LogicalSizeUsedInBytes: 0,
		},
	}

	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&snapshotCreateActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Mock UpdateJob method calls - use mock.Anything for error details since they may vary
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "ERROR", 1011, mock.Anything).Return(nil)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(snapshotCreateActivity.CreateSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)
	s.env.OnActivity(snapshotCreateActivity.UpdateSnapshotDetails, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(CreateSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotUnitTestSuite) TestCreateSnapshotWorkflowFailsOnSetupError() {
	// Test with invalid params to trigger setup error
	params := &common.CreateSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "", // Empty account name to trigger error
		},
	}
	snapshot := &datamodel.Snapshot{
		Name: "test-snapshot",
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			SizeInBytes:            0,
			LogicalSizeUsedInBytes: 0,
		},
	}

	// No need to register activities for setup error test since it fails before activities are called

	s.env.ExecuteWorkflow(CreateSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotUnitTestSuite) TestCreateSnapshotWorkflowFailsOnJobStatusUpdateError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	snapshotCreateActivity := activities.SnapshotCreateActivity{SE: mockStorage}

	params := &common.CreateSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	snapshot := &datamodel.Snapshot{
		Name: "test-snapshot",
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			SizeInBytes:            0,
			LogicalSizeUsedInBytes: 0,
		},
	}

	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&snapshotCreateActivity)

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
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	s.env.ExecuteWorkflow(CreateSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotUnitTestSuite) TestCreateSnapshotWorkflowFailsOnFinalJobStatusUpdateError() {
	commonActivity := activities.CommonActivities{}
	snapshotCreateActivity := activities.SnapshotCreateActivity{}

	params := &common.CreateSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	snapshot := &datamodel.Snapshot{
		Name: "test-snapshot",
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			SizeInBytes:            0,
			LogicalSizeUsedInBytes: 0,
		},
	}

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(snapshotCreateActivity.CreateSnapshotInONTAP)
	s.env.RegisterActivity(snapshotCreateActivity.UpdateSnapshotDetails)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(snapshotCreateActivity.CreateSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snapshot-uuid"}, SizeInBytes: 1024, LogicalSizeInBytes: 1024}, nil)
	s.env.OnActivity(snapshotCreateActivity.UpdateSnapshotDetails, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock UpdateJobStatus to return error for DONE state
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(assert.AnError)

	s.env.ExecuteWorkflow(CreateSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

// TestSnapshotCreateWorkflowRollbackOnONTAPFailure verifies that when ONTAP creation fails,
// only UpdateSnapshotDetails(error) runs — DeleteSnapshotInONTAP is not registered yet.
func (s *SnapshotUnitTestSuite) TestSnapshotCreateWorkflowRollbackOnONTAPFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	snapshotCreateActivity := activities.SnapshotCreateActivity{SE: mockStorage}

	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&snapshotCreateActivity)

	params := &common.CreateSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	snapshot := &datamodel.Snapshot{
		Name: "test-snapshot",
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		},
	}

	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "ERROR", 1011, mock.Anything).Return(nil)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(snapshotCreateActivity.CreateSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("snapshot creation failed"))
	// Rollback must call UpdateSnapshotDetails(error) — DeleteSnapshotInONTAP must NOT be called.
	s.env.OnActivity(snapshotCreateActivity.UpdateSnapshotDetails, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)

	s.env.ExecuteWorkflow(CreateSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

// TestSnapshotCreateWorkflowRollbackOnDBWriteFailure verifies the Saga compensation path:
// when ONTAP creation succeeds but the DB write fails, the rollback manager runs
// DeleteSnapshotInONTAP (LIFO first) then UpdateSnapshotDetails(error), preventing an
// orphaned ONTAP snapshot with no valid DB record.
func (s *SnapshotUnitTestSuite) TestSnapshotCreateWorkflowRollbackOnDBWriteFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	snapshotCreateActivity := activities.SnapshotCreateActivity{SE: mockStorage}
	snapshotDeleteActivity := activities.SnapshotDeleteActivity{SE: mockStorage}

	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&snapshotCreateActivity)
	s.env.RegisterActivity(&snapshotDeleteActivity)

	params := &common.CreateSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	snapshot := &datamodel.Snapshot{
		Name: "test-snapshot",
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		},
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "ERROR", mock.Anything, mock.Anything).Return(nil)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(snapshotCreateActivity.CreateSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snapshot-uuid"}}, nil)
	// First call (success write) fails with a non-retryable error so Temporal does not retry it
	// and immediately triggers the rollback. A plain errors.New would be retried (MaximumAttempts=3
	// by default), consuming the second mock before the rollback ever fires.
	s.env.OnActivity(snapshotCreateActivity.UpdateSnapshotDetails, mock.Anything, mock.Anything, mock.Anything).
		Return(temporal.NewNonRetryableApplicationError("db write failed", vsaerrors.CustomErrorType, nil)).Once()
	// Rollback calls UpdateSnapshotDetails again with nil response (marks snapshot as error).
	s.env.OnActivity(snapshotCreateActivity.UpdateSnapshotDetails, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()
	// Rollback must also call DeleteSnapshotInONTAP (LIFO: runs before the DB error write).
	s.env.OnActivity(snapshotDeleteActivity.DeleteSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Times(1)

	s.env.ExecuteWorkflow(CreateSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *SnapshotUnitTestSuite) TestCreateSnapshotWorkflowFailsOnJobInErrorState() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}

	params := &common.CreateSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	snapshot := &datamodel.Snapshot{
		Name: "test-snapshot",
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			SizeInBytes:            0,
			LogicalSizeUsedInBytes: 0,
		},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)

	// Mock GetJob to return job in ERROR state
	jobInErrorState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateERROR),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInErrorState, nil)

	// Execute workflow
	s.env.ExecuteWorkflow(CreateSnapshotWorkflow, params, snapshot)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "job default-test-workflow-id is in state ERROR; expected NEW")
}

func (s *SnapshotUnitTestSuite) TestCreateSnapshotWorkflowSucceedsWhenJobNotInErrorState() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	snapshotCreateActivity := activities.SnapshotCreateActivity{SE: mockStorage}

	params := &common.CreateSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	snapshot := &datamodel.Snapshot{
		Name: "test-snapshot",
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			SizeInBytes:            0,
			LogicalSizeUsedInBytes: 0,
		},
	}

	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&snapshotCreateActivity)

	// Mock UpdateJob method calls
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "DONE", 0, "").Return(nil)

	// Mock GetJob to return job in NEW state (not ERROR)
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(snapshotCreateActivity.CreateSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snapshot-uuid"}, SizeInBytes: 1024, LogicalSizeInBytes: 1024}, nil)
	s.env.OnActivity(snapshotCreateActivity.UpdateSnapshotDetails, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(CreateSnapshotWorkflow, params, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *SnapshotUnitTestSuite) TestCreateSnapshotWorkflow_CancellationHandling() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	snapshotCreateActivity := activities.SnapshotCreateActivity{SE: mockStorage}

	params := &common.CreateSnapshotParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			AccountName: "test-account",
		},
	}
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
		Name:      "test-snapshot",
		Volume: &datamodel.Volume{
			PoolID: 1,
			Pool: &datamodel.Pool{
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "",
					CertificateID: "",
				},
			},
		},
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			SizeInBytes:            0,
			LogicalSizeUsedInBytes: 0,
		},
	}

	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&snapshotCreateActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Mock UpdateJobStatus activity calls (activity-level mocking for Temporal test framework)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	// Mock UpdateJob method calls (storage-level mocking as fallback)
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "PROCESSING", 0, "").Return(nil).Maybe()
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", "ERROR", 0, mock.Anything).Return(nil).Maybe()

	// Send cancellation signal when GetNode completes, so it's available at the next cancellation check (before CreateSnapshotInONTAP)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		s.env.SignalWorkflow(CancelSnapshotSignalName, "cancellation requested")
	}).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// CreateSnapshotInONTAP should not be called if cancellation is detected before it
	s.env.OnActivity(snapshotCreateActivity.CreateSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snapshot-uuid"}, SizeInBytes: 1024, LogicalSizeInBytes: 1024}, nil).Maybe()

	// Rollback fires on cancellation — UpdateSnapshotDetails marks the snapshot as ERROR in DB
	s.env.OnActivity(snapshotCreateActivity.UpdateSnapshotDetails, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(CreateSnapshotWorkflow, params, snapshot)

	// Verify workflow completed with cancellation error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	assert.Error(s.T(), err)
	if err != nil {
		assert.Contains(s.T(), err.Error(), "snapshot creation cancelled")
	}
}

func TestSnapshotUnitTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotUnitTestSuite))
}
