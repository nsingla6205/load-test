package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	supervisorhandler "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/tasks/supervisor-handler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	workflowEngine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	enumspb "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestWorkflowSupervisorTaskRunnerEvaluateJobTimedOutForUpdateJob(t *testing.T) {
	storage := database.NewMockStorage(t)
	temporal := workflowEngine.NewMockTemporalTestClient(t)
	handler := supervisorhandler.NewPoolUpdateHandler()

	runner := &workflowSupervisorTaskRunner{
		storage:       storage,
		temporal:      temporal,
		correlationID: "corr-id",
		handlers: map[string]supervisorhandler.Handler{
			string(datamodel.JobTypeUpdatePool): handler,
		},
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-update-pool"},
		WorkflowID: "wf-update-pool",
		Type:       string(datamodel.JobTypeUpdatePool),
		State:      string(datamodel.JobsStateNEW),
		TrackingID: 100,
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: "pool-uuid",
		},
	}

	detail := supervisorhandler.WorkflowTimeoutDetail
	expectJobLock(t, storage, job)
	temporal.EXPECT().TerminateWorkflow(mock.Anything, job.WorkflowID, "", supervisorhandler.WorkflowTimeoutDetail).Return(nil)
	storage.EXPECT().UpdateJob(mock.Anything, job.UUID, string(datamodel.JobsStateERROR), job.TrackingID, detail, database.UpdateJobOpts{Reason: database.JobTerminalReasonSupervisorTimeout}).Return(nil)

	temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, job.WorkflowID, "").Return(&workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT},
	}, nil)

	// Mock pool update handler
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateUpdating,
	}
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(pool, nil).Once()

	runner.evaluateJob(context.Background(), job, handler, datamodel.JobsStateNEW)

	storage.AssertExpectations(t)
}

func TestWorkflowSupervisorTaskRunnerEvaluateJobTimedOutForDeleteJob(t *testing.T) {
	storage := database.NewMockStorage(t)
	temporal := workflowEngine.NewMockTemporalTestClient(t)
	handler := supervisorhandler.NewPoolDeleteHandler()

	runner := &workflowSupervisorTaskRunner{
		storage:       storage,
		temporal:      temporal,
		correlationID: "corr-id",
		handlers: map[string]supervisorhandler.Handler{
			string(datamodel.JobTypeDeletePool): handler,
		},
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-delete-pool"},
		WorkflowID: "wf-delete-pool",
		Type:       string(datamodel.JobTypeDeletePool),
		State:      string(datamodel.JobsStateNEW),
		TrackingID: 101,
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: "pool-uuid",
		},
	}

	detail := supervisorhandler.WorkflowTimeoutDetail
	expectJobLock(t, storage, job)
	temporal.EXPECT().TerminateWorkflow(mock.Anything, job.WorkflowID, "", supervisorhandler.WorkflowTimeoutDetail).Return(nil)
	storage.EXPECT().UpdateJob(mock.Anything, job.UUID, string(datamodel.JobsStateERROR), job.TrackingID, detail, database.UpdateJobOpts{Reason: database.JobTerminalReasonSupervisorTimeout}).Return(nil)

	temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, job.WorkflowID, "").Return(&workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT},
	}, nil)

	// Mock pool delete handler
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateDeleting,
	}
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(pool, nil).Once()

	runner.evaluateJob(context.Background(), job, handler, datamodel.JobsStateNEW)

	storage.AssertExpectations(t)
}

func TestWorkflowSupervisorTaskRunnerEvaluateJobNilResponseForUpdateDelete(t *testing.T) {
	storage := database.NewMockStorage(t)
	temporal := workflowEngine.NewMockTemporalTestClient(t)
	handler := supervisorhandler.NewPoolUpdateHandler()

	runner := &workflowSupervisorTaskRunner{
		storage:       storage,
		temporal:      temporal,
		correlationID: "corr-id",
		handlers: map[string]supervisorhandler.Handler{
			string(datamodel.JobTypeUpdatePool): handler,
		},
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-10"},
		WorkflowID: "wf-10",
		Type:       string(datamodel.JobTypeUpdatePool),
		State:      string(datamodel.JobsStateNEW),
	}

	// Nil response
	temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, job.WorkflowID, "").Return((*workflowservice.DescribeWorkflowExecutionResponse)(nil), nil)

	runner.evaluateJob(context.Background(), job, handler, datamodel.JobsStateNEW)

	storage.AssertNotCalled(t, "UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestWorkflowSupervisorTaskRunnerCleanupJobNoTemporalClient(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := newTestHandler(datamodel.JobTypeCreatePool)

	runner := &workflowSupervisorTaskRunner{
		storage:  storage,
		temporal: nil, // No temporal client
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-11"},
		WorkflowID: "wf-11",
		TrackingID: 50,
	}

	storage.EXPECT().UpdateJob(mock.Anything, job.UUID, string(datamodel.JobsStateERROR), job.TrackingID, supervisorhandler.WorkflowTimeoutDetail, database.UpdateJobOpts{Reason: database.JobTerminalReasonSupervisorTimeout}).Return(nil)

	runner.cleanupJob(context.Background(), job, handler, supervisorhandler.EventTimeout, datamodel.JobsStateNEW, util.GetLogger(context.Background()))

	require.Len(t, handler.events, 1)
	require.Equal(t, supervisorhandler.EventTimeout, handler.events[0])
}

func TestWorkflowSupervisorTaskRunnerCleanupJobEmptyWorkflowID(t *testing.T) {
	storage := database.NewMockStorage(t)
	temporal := workflowEngine.NewMockTemporalTestClient(t)
	handler := newTestHandler(datamodel.JobTypeCreatePool)

	runner := &workflowSupervisorTaskRunner{
		storage:  storage,
		temporal: temporal,
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-12"},
		WorkflowID: "", // Empty workflow ID
		TrackingID: 51,
	}

	storage.EXPECT().UpdateJob(mock.Anything, job.UUID, string(datamodel.JobsStateERROR), job.TrackingID, supervisorhandler.WorkflowTimeoutDetail, database.UpdateJobOpts{Reason: database.JobTerminalReasonSupervisorTimeout}).Return(nil)

	runner.cleanupJob(context.Background(), job, handler, supervisorhandler.EventTimeout, datamodel.JobsStateNEW, util.GetLogger(context.Background()))

	require.Len(t, handler.events, 1)
	temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestWorkflowSupervisorTaskRunnerMarkJobAsError(t *testing.T) {
	storage := database.NewMockStorage(t)
	runner := &workflowSupervisorTaskRunner{storage: storage}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-13"},
		TrackingID: 52,
	}

	storage.EXPECT().UpdateJob(mock.Anything, job.UUID, string(datamodel.JobsStateERROR), job.TrackingID, supervisorhandler.WorkflowTimeoutDetail, database.UpdateJobOpts{Reason: database.JobTerminalReasonSupervisorTimeout}).Return(nil)

	err := runner.markJobAsError(context.Background(), job)
	require.NoError(t, err)
}

func TestWorkflowSupervisorTaskRunnerMarkJobAsErrorFailure(t *testing.T) {
	storage := database.NewMockStorage(t)
	runner := &workflowSupervisorTaskRunner{storage: storage}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-14"},
		TrackingID: 53,
	}

	expectedErr := errors.New("update failed")
	storage.EXPECT().UpdateJob(mock.Anything, job.UUID, string(datamodel.JobsStateERROR), job.TrackingID, supervisorhandler.WorkflowTimeoutDetail, database.UpdateJobOpts{Reason: database.JobTerminalReasonSupervisorTimeout}).Return(expectedErr)

	err := runner.markJobAsError(context.Background(), job)
	require.Error(t, err)
	require.Equal(t, expectedErr, err)
}

func TestWorkflowSupervisorTaskRunnerScanWithUpdateDeleteJobTypes(t *testing.T) {
	enableProcessingTimeout(t)
	storage := database.NewMockStorage(t)
	temporal := workflowEngine.NewMockTemporalTestClient(t)

	updateHandler := supervisorhandler.NewPoolUpdateHandler()
	deleteHandler := supervisorhandler.NewPoolDeleteHandler()

	runner := &workflowSupervisorTaskRunner{
		storage:       storage,
		temporal:      temporal,
		correlationID: "corr-id",
		handlers:      make(map[string]supervisorhandler.Handler),
	}

	runner.registerHandlers(updateHandler, deleteHandler)

	updateJob := &datamodel.Job{
		BaseModel: datamodel.BaseModel{
			ID:        1,
			UUID:      "update-job-uuid",
			CreatedAt: time.Now().Add(-10 * time.Minute),
		},
		WorkflowID: "update-wf-id",
		Type:       string(datamodel.JobTypeUpdatePool),
		State:      string(datamodel.JobsStateNEW),
		TrackingID: 1,
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: "pool-uuid",
		},
	}

	deleteJob := &datamodel.Job{
		BaseModel: datamodel.BaseModel{
			ID:        2,
			UUID:      "delete-job-uuid",
			CreatedAt: time.Now().Add(-10 * time.Minute),
		},
		WorkflowID: "delete-wf-id",
		Type:       string(datamodel.JobTypeDeletePool),
		State:      string(datamodel.JobsStateNEW),
		TrackingID: 2,
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: "pool-uuid",
		},
	}

	storage.EXPECT().GetJobsWithCondition(mock.Anything, mock.MatchedBy(func(filter dbutils.Filter) bool {
		// Verify filter includes UPDATE and DELETE job types
		for _, condition := range filter.Conditions {
			if condition.Field == "type" && condition.Op == "IN" {
				if types, ok := condition.Value.([]string); ok {
					hasUpdate := false
					hasDelete := false
					for _, t := range types {
						if t == string(datamodel.JobTypeUpdatePool) || t == string(datamodel.JobTypeUpdateLargePool) {
							hasUpdate = true
						}
						if t == string(datamodel.JobTypeDeletePool) || t == string(datamodel.JobTypeDeleteLargePool) {
							hasDelete = true
						}
					}
					return hasUpdate && hasDelete
				}
			}
		}
		return false
	})).Return([]*datamodel.Job{updateJob, deleteJob}, nil).Once()

	// Mock describe for update job
	temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, updateJob.WorkflowID, "").Return(&workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT},
	}, nil).Once()

	// Mock cleanup for update job
	storage.EXPECT().WithTransaction(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, fn func(dbutils.Transaction) error) error {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&datamodel.Job{}))

		dbJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      updateJob.UUID,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			WorkflowID: updateJob.WorkflowID,
			State:      updateJob.State,
			TrackingID: updateJob.TrackingID,
		}
		require.NoError(t, db.Create(dbJob).Error)

		tx := dbutils.NewMockTransaction(t)
		tx.EXPECT().GORM().Return(db)
		return fn(tx)
	}).Once()
	temporal.EXPECT().TerminateWorkflow(mock.Anything, updateJob.WorkflowID, "", supervisorhandler.WorkflowTimeoutDetail).Return(nil).Once()
	storage.EXPECT().UpdateJob(mock.Anything, updateJob.UUID, string(datamodel.JobsStateERROR), updateJob.TrackingID, supervisorhandler.WorkflowTimeoutDetail, database.UpdateJobOpts{Reason: database.JobTerminalReasonSupervisorTimeout}).Return(nil).Once()
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateUpdating,
	}
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(pool, nil).Once()

	// Mock describe for delete job
	temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, deleteJob.WorkflowID, "").Return(&workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT},
	}, nil).Once()

	// Mock cleanup for delete job
	storage.EXPECT().WithTransaction(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, fn func(dbutils.Transaction) error) error {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&datamodel.Job{}))

		dbJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      deleteJob.UUID,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			WorkflowID: deleteJob.WorkflowID,
			State:      deleteJob.State,
			TrackingID: deleteJob.TrackingID,
		}
		require.NoError(t, db.Create(dbJob).Error)

		tx := dbutils.NewMockTransaction(t)
		tx.EXPECT().GORM().Return(db)
		return fn(tx)
	}).Once()
	temporal.EXPECT().TerminateWorkflow(mock.Anything, deleteJob.WorkflowID, "", supervisorhandler.WorkflowTimeoutDetail).Return(nil).Once()
	storage.EXPECT().UpdateJob(mock.Anything, deleteJob.UUID, string(datamodel.JobsStateERROR), deleteJob.TrackingID, supervisorhandler.WorkflowTimeoutDetail, database.UpdateJobOpts{Reason: database.JobTerminalReasonSupervisorTimeout}).Return(nil).Once()
	pool2 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateDeleting,
	}
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool2, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool2, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(pool2, nil).Once()

	// Mock for PROCESSING state jobs scan (no jobs found)
	storage.EXPECT().GetJobsWithCondition(mock.Anything, mock.MatchedBy(func(filter dbutils.Filter) bool {
		for _, condition := range filter.Conditions {
			if condition.Field == "state" && condition.Op == "=" && condition.Value == string(datamodel.JobsStatePROCESSING) {
				return true
			}
		}
		return false
	})).Return([]*datamodel.Job{}, nil).Once()

	runner.scan(context.Background())

	storage.AssertExpectations(t)
}
