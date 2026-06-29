package jobmanageractivities

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/testsuite"
)

func TestCreateScheduleActivity_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	temporalScheduler := scheduler.NewTemporalScheduler(mockScheduleClient)

	jobManagerActivity := &JobManagerActivity{SE: mockStorage, Scheduler: temporalScheduler}
	env.RegisterActivity(jobManagerActivity.CreateScheduleActivity)

	jobs := []*datamodel.AdminJobSpec{
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-1"},
			JobType:        "SYNC_VSA_SNAPSHOTS",
			CronExpression: "* * * * *",
			State:          scheduler.JobStatusCreating,
		},
	}
	mockStorage.On("GetAdminJobSpecsByState", mock.Anything, scheduler.JobStatusCreating).Return(jobs, nil)
	mockStorage.On("UpdateAdminJobSpec", mock.Anything, jobs[0]).Return(nil)

	mockScheduleClient.On("Create", mock.Anything, mock.Anything).Return(mockScheduleHandle, nil)
	mockScheduleHandle.On("GetID").Return("schedule-1")

	_, err := env.ExecuteActivity(jobManagerActivity.CreateScheduleActivity)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockScheduleClient.AssertExpectations(t)
}

func TestCreateScheduleActivity_HardDeleteResourcesAndAccount(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	temporalScheduler := scheduler.NewTemporalScheduler(mockScheduleClient)

	jobManagerActivity := &JobManagerActivity{SE: mockStorage, Scheduler: temporalScheduler}
	env.RegisterActivity(jobManagerActivity.CreateScheduleActivity)

	jobs := []*datamodel.AdminJobSpec{
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-hard-delete"},
			JobType:        HardDeleteResourcesAndAccount,
			CronExpression: "0 2 * * *",
			State:          scheduler.JobStatusCreating,
		},
	}
	mockStorage.On("GetAdminJobSpecsByState", mock.Anything, scheduler.JobStatusCreating).Return(jobs, nil)
	mockStorage.On("UpdateAdminJobSpec", mock.Anything, jobs[0]).Return(nil)

	mockScheduleClient.On("Create", mock.Anything, mock.Anything).Return(mockScheduleHandle, nil)
	mockScheduleHandle.On("GetID").Return("schedule-hard-delete")

	_, err := env.ExecuteActivity(jobManagerActivity.CreateScheduleActivity)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockScheduleClient.AssertExpectations(t)
}

func TestCreateScheduleActivity_MultipleJobTypes(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	temporalScheduler := scheduler.NewTemporalScheduler(mockScheduleClient)

	jobManagerActivity := &JobManagerActivity{SE: mockStorage, Scheduler: temporalScheduler}
	env.RegisterActivity(jobManagerActivity.CreateScheduleActivity)

	jobs := []*datamodel.AdminJobSpec{
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-1"},
			JobType:        SyncVsaSnapshots,
			CronExpression: "* * * * *",
			State:          scheduler.JobStatusCreating,
		},
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-2"},
			JobType:        RotateKmsServiceAccounts,
			CronExpression: "0 0 * * *",
			State:          scheduler.JobStatusCreating,
		},
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-3"},
			JobType:        HardDeleteResourcesAndAccount,
			CronExpression: "0 2 * * *",
			State:          scheduler.JobStatusCreating,
		},
	}
	mockStorage.On("GetAdminJobSpecsByState", mock.Anything, scheduler.JobStatusCreating).Return(jobs, nil)
	mockStorage.On("UpdateAdminJobSpec", mock.Anything, mock.Anything).Return(nil).Times(3)

	mockScheduleClient.On("Create", mock.Anything, mock.Anything).Return(mockScheduleHandle, nil).Times(3)
	mockScheduleHandle.On("GetID").Return("schedule-id")

	_, err := env.ExecuteActivity(jobManagerActivity.CreateScheduleActivity)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockScheduleClient.AssertExpectations(t)
}

func TestUpdateScheduleActivity_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	temporalScheduler := scheduler.NewTemporalScheduler(mockScheduleClient)

	jobManagerActivity := &JobManagerActivity{SE: mockStorage, Scheduler: temporalScheduler}
	env.RegisterActivity(jobManagerActivity.UpdateScheduleActivity)

	jobs := []*datamodel.AdminJobSpec{
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-2"},
			JobType:        "SYNC_VSA_SNAPSHOTS",
			CronExpression: "*/5 * * * *",
			State:          scheduler.JobStatusUpdating,
		},
	}

	mockStorage.On("GetAdminJobSpecsByState", mock.Anything, scheduler.JobStatusUpdating).Return(jobs, nil)
	mockStorage.On("UpdateAdminJobSpec", mock.Anything, jobs[0]).Return(nil)

	mockScheduleClient.On("GetHandle", mock.Anything, "job-2").Return(mockScheduleHandle)
	mockScheduleHandle.On("Update", mock.Anything, mock.Anything).Return(nil)

	_, err := env.ExecuteActivity(jobManagerActivity.UpdateScheduleActivity)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockScheduleClient.AssertExpectations(t)
	mockScheduleHandle.AssertExpectations(t)
}

func TestUpdateScheduleActivity_HardDeleteResourcesAndAccount(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	temporalScheduler := scheduler.NewTemporalScheduler(mockScheduleClient)

	jobManagerActivity := &JobManagerActivity{SE: mockStorage, Scheduler: temporalScheduler}
	env.RegisterActivity(jobManagerActivity.UpdateScheduleActivity)

	jobs := []*datamodel.AdminJobSpec{
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-hard-delete-update"},
			JobType:        HardDeleteResourcesAndAccount,
			CronExpression: "0 3 * * *",
			State:          scheduler.JobStatusUpdating,
		},
	}

	mockStorage.On("GetAdminJobSpecsByState", mock.Anything, scheduler.JobStatusUpdating).Return(jobs, nil)
	mockStorage.On("UpdateAdminJobSpec", mock.Anything, jobs[0]).Return(nil)

	mockScheduleClient.On("GetHandle", mock.Anything, "job-hard-delete-update").Return(mockScheduleHandle)
	mockScheduleHandle.On("Update", mock.Anything, mock.Anything).Return(nil)

	_, err := env.ExecuteActivity(jobManagerActivity.UpdateScheduleActivity)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockScheduleClient.AssertExpectations(t)
	mockScheduleHandle.AssertExpectations(t)
}

func TestDeleteScheduleActivity_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	temporalScheduler := scheduler.NewTemporalScheduler(mockScheduleClient)

	jobManagerActivity := &JobManagerActivity{SE: mockStorage, Scheduler: temporalScheduler}
	env.RegisterActivity(jobManagerActivity.DeleteScheduleActivity)

	jobs := []*datamodel.AdminJobSpec{
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-3"},
			JobType:        "SYNC_VSA_SNAPSHOTS",
			CronExpression: "0 0 * * *",
			State:          scheduler.JobStatusDeleting,
		},
	}

	mockStorage.On("GetAdminJobSpecsByState", mock.Anything, scheduler.JobStatusDeleting).Return(jobs, nil)
	mockStorage.On("UpdateAdminJobSpec", mock.Anything, jobs[0]).Return(nil)

	mockScheduleClient.On("GetHandle", mock.Anything, "job-3").Return(mockScheduleHandle)
	mockScheduleHandle.On("Delete", mock.Anything).Return(nil)

	_, err := env.ExecuteActivity(jobManagerActivity.DeleteScheduleActivity)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockScheduleClient.AssertExpectations(t)
	mockScheduleHandle.AssertExpectations(t)
}

func TestDeleteScheduleActivity_HardDeleteResourcesAndAccount(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	temporalScheduler := scheduler.NewTemporalScheduler(mockScheduleClient)

	jobManagerActivity := &JobManagerActivity{SE: mockStorage, Scheduler: temporalScheduler}
	env.RegisterActivity(jobManagerActivity.DeleteScheduleActivity)

	jobs := []*datamodel.AdminJobSpec{
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-hard-delete-del"},
			JobType:        HardDeleteResourcesAndAccount,
			CronExpression: "0 2 * * *",
			State:          scheduler.JobStatusDeleting,
		},
	}

	mockStorage.On("GetAdminJobSpecsByState", mock.Anything, scheduler.JobStatusDeleting).Return(jobs, nil)
	mockStorage.On("UpdateAdminJobSpec", mock.Anything, jobs[0]).Return(nil)

	mockScheduleClient.On("GetHandle", mock.Anything, "job-hard-delete-del").Return(mockScheduleHandle)
	mockScheduleHandle.On("Delete", mock.Anything).Return(nil)

	_, err := env.ExecuteActivity(jobManagerActivity.DeleteScheduleActivity)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockScheduleClient.AssertExpectations(t)
	mockScheduleHandle.AssertExpectations(t)
}

func TestDeleteScheduleActivity_GetAdminJobsByStateError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	temporalScheduler := scheduler.NewTemporalScheduler(mockScheduleClient)
	jobManagerActivity := &JobManagerActivity{SE: mockStorage, Scheduler: temporalScheduler}
	env.RegisterActivity(jobManagerActivity.DeleteScheduleActivity)

	mockStorage.On("GetAdminJobSpecsByState", mock.Anything, scheduler.JobStatusDeleting).Return(nil, errors.New("db error"))

	_, err := env.ExecuteActivity(jobManagerActivity.DeleteScheduleActivity)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteScheduleActivity_SchedulerDeleteError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	temporalScheduler := scheduler.NewTemporalScheduler(mockScheduleClient)
	jobManagerActivity := &JobManagerActivity{SE: mockStorage, Scheduler: temporalScheduler}
	env.RegisterActivity(jobManagerActivity.DeleteScheduleActivity)

	jobs := []*datamodel.AdminJobSpec{
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-3"},
			JobType:        "SYNC_VSA_SNAPSHOTS",
			CronExpression: "0 0 * * *",
			State:          scheduler.JobStatusDeleting,
		},
	}
	mockStorage.On("GetAdminJobSpecsByState", mock.Anything, scheduler.JobStatusDeleting).Return(jobs, nil)
	mockScheduleClient.On("GetHandle", mock.Anything, "job-3").Return(mockScheduleHandle)
	mockScheduleHandle.On("Delete", mock.Anything).Return(errors.New("delete error"))

	_, err := env.ExecuteActivity(jobManagerActivity.DeleteScheduleActivity)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
	mockScheduleClient.AssertExpectations(t)
	mockScheduleHandle.AssertExpectations(t)
}

func TestDeleteScheduleActivity_UpdateAdminJobSpecError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	temporalScheduler := scheduler.NewTemporalScheduler(mockScheduleClient)
	jobManagerActivity := &JobManagerActivity{SE: mockStorage, Scheduler: temporalScheduler}
	env.RegisterActivity(jobManagerActivity.DeleteScheduleActivity)

	jobs := []*datamodel.AdminJobSpec{
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-3"},
			JobType:        "SYNC_VSA_SNAPSHOTS",
			CronExpression: "0 0 * * *",
			State:          scheduler.JobStatusDeleting,
		},
	}
	mockStorage.On("GetAdminJobSpecsByState", mock.Anything, scheduler.JobStatusDeleting).Return(jobs, nil)
	mockScheduleClient.On("GetHandle", mock.Anything, "job-3").Return(mockScheduleHandle)
	mockScheduleHandle.On("Delete", mock.Anything).Return(nil)
	mockStorage.On("UpdateAdminJobSpec", mock.Anything, jobs[0]).Return(errors.New("update error"))

	_, err := env.ExecuteActivity(jobManagerActivity.DeleteScheduleActivity)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
	mockScheduleClient.AssertExpectations(t)
	mockScheduleHandle.AssertExpectations(t)
}

func TestCreateScheduleActivity_UnknownJobType(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	temporalScheduler := scheduler.NewTemporalScheduler(mockScheduleClient)

	jobManagerActivity := &JobManagerActivity{SE: mockStorage, Scheduler: temporalScheduler}
	env.RegisterActivity(jobManagerActivity.CreateScheduleActivity)

	jobs := []*datamodel.AdminJobSpec{
		{
			BaseModel:      datamodel.BaseModel{UUID: "job-unknown"},
			JobType:        "UNKNOWN_JOB_TYPE",
			CronExpression: "* * * * *",
			State:          scheduler.JobStatusCreating,
		},
	}
	mockStorage.On("GetAdminJobSpecsByState", mock.Anything, scheduler.JobStatusCreating).Return(jobs, nil)

	// No schedule should be created for unknown job type
	_, err := env.ExecuteActivity(jobManagerActivity.CreateScheduleActivity)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	// Ensure Create was never called on mockScheduleClient
	mockScheduleClient.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestJobTypeToWorkflow_IncludesTrialAccountSync(t *testing.T) {
	workflowFunc, ok := JobTypeToWorkflow[TrialAccountSync]
	assert.True(t, ok)
	assert.NotNil(t, workflowFunc)
}

func TestJobTypeToWorkflow_IncludesHarvestPollerDriftReconcile(t *testing.T) {
	workflowFunc, ok := JobTypeToWorkflow[HarvestPollerDriftReconcileJob]
	assert.True(t, ok)
	assert.NotNil(t, workflowFunc)
}
