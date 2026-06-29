package adminbackgroundjobs

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/temporal"
	"gorm.io/gorm"
)

func TestLoadJobSpecs(t *testing.T) {
	t.Run("WhenDataHasValidJSON", func(tt *testing.T) {
		data = []byte(`{"SYNC_VSA_CLUSTER_STATUS": {"JobType": "SYNC_VSA_CLUSTER_STATUS", "CronExpression": "*/5 * * * *", "State": "CREATING"}}`)
		err := LoadJobSpecs()
		assert.NoError(tt, err)
		assert.Len(tt, adminJobSpecs, 1)
		adminJobSpecs = make(map[string]*datamodel.AdminJobSpec) // Reset for next test
	})
	t.Run("WhenDataHasEmptyJSON", func(tt *testing.T) {
		data = []byte(`{}`)
		err := LoadJobSpecs()
		assert.NoError(tt, err)
		assert.Len(tt, adminJobSpecs, 0)
		adminJobSpecs = make(map[string]*datamodel.AdminJobSpec)
	})
	t.Run("WhenDataHasInvalidJSON", func(tt *testing.T) {
		data = []byte(`{Name: John Doe, Age: 30}`)
		err := LoadJobSpecs()
		assert.Error(tt, err)
		assert.Len(tt, adminJobSpecs, 0)
		adminJobSpecs = make(map[string]*datamodel.AdminJobSpec)
	})
	t.Run("WhenDELETE_RESOURCES_CRON_EXPRESSION_IsSet", func(tt *testing.T) {
		// Set environment variable
		err := os.Setenv("DELETE_RESOURCES_CRON_EXPRESSION", "0 2,14 * * *")
		assert.NoError(tt, err)
		defer func() {
			err := os.Unsetenv("DELETE_RESOURCES_CRON_EXPRESSION")
			assert.NoError(tt, err)
		}()

		// Load JSON with DELETE_RESOURCES job
		data = []byte(`{"DELETE_RESOURCES": {"jobType": "DELETE_RESOURCES", "cronExpression": "0 1,13 * * *", "state": "CREATING"}}`)
		err = LoadJobSpecs()
		assert.NoError(tt, err)
		assert.Len(tt, adminJobSpecs, 1)

		// Verify the cron expression was overridden
		deleteResourcesSpec, exists := adminJobSpecs["DELETE_RESOURCES"]
		assert.True(tt, exists)
		assert.Equal(tt, "0 2,14 * * *", deleteResourcesSpec.CronExpression)

		adminJobSpecs = make(map[string]*datamodel.AdminJobSpec) // Reset for next test
	})

	t.Run("WhenDELETE_RESOURCES_CRON_EXPRESSION_IsNotSet", func(tt *testing.T) {
		// Ensure environment variable is not set
		err := os.Unsetenv("DELETE_RESOURCES_CRON_EXPRESSION")
		assert.NoError(tt, err)

		// Load JSON with DELETE_RESOURCES job
		data = []byte(`{"DELETE_RESOURCES": {"jobType": "DELETE_RESOURCES", "cronExpression": "0 1,13 * * *", "state": "CREATING"}}`)
		err = LoadJobSpecs()
		assert.NoError(tt, err)
		assert.Len(tt, adminJobSpecs, 1)

		// Verify the cron expression was not overridden
		deleteResourcesSpec, exists := adminJobSpecs["DELETE_RESOURCES"]
		assert.True(tt, exists)
		assert.Equal(tt, "0 1,13 * * *", deleteResourcesSpec.CronExpression)

		adminJobSpecs = make(map[string]*datamodel.AdminJobSpec) // Reset for next test
	})

	t.Run("WhenDELETE_RESOURCES_CRON_EXPRESSION_IsEmpty", func(tt *testing.T) {
		// Set empty environment variable
		err := os.Setenv("DELETE_RESOURCES_CRON_EXPRESSION", "")
		assert.NoError(tt, err)
		defer func() {
			err := os.Unsetenv("DELETE_RESOURCES_CRON_EXPRESSION")
			assert.NoError(tt, err)
		}()

		// Load JSON with DELETE_RESOURCES job
		data = []byte(`{"DELETE_RESOURCES": {"jobType": "DELETE_RESOURCES", "cronExpression": "0 1,13 * * *", "state": "CREATING"}}`)
		err = LoadJobSpecs()
		assert.NoError(tt, err)
		assert.Len(tt, adminJobSpecs, 1)

		// Verify the cron expression was not overridden (empty env var should not override)
		deleteResourcesSpec, exists := adminJobSpecs["DELETE_RESOURCES"]
		assert.True(tt, exists)
		assert.Equal(tt, "0 1,13 * * *", deleteResourcesSpec.CronExpression)

		adminJobSpecs = make(map[string]*datamodel.AdminJobSpec) // Reset for next test
	})

	t.Run("WhenCERTIFICATE_ROTATION_CRON_EXPRESSION_IsSet", func(tt *testing.T) {
		err := os.Setenv("CERTIFICATE_ROTATION_CRON_EXPRESSION", "0 3 * * *")
		assert.NoError(tt, err)
		defer func() {
			_ = os.Unsetenv("CERTIFICATE_ROTATION_CRON_EXPRESSION")
		}()

		data = []byte(`{"ROTATE_VSA_CERTIFICATE_AND_PASSWORD": {"jobType": "ROTATE_VSA_CERTIFICATE_AND_PASSWORD", "cronExpression": "0 0 * * *", "state": "CREATING"}}`)
		err = LoadJobSpecs()
		assert.NoError(tt, err)
		assert.Len(tt, adminJobSpecs, 1)

		spec, exists := adminJobSpecs["ROTATE_VSA_CERTIFICATE_AND_PASSWORD"]
		assert.True(tt, exists)
		assert.Equal(tt, "0 3 * * *", spec.CronExpression)

		adminJobSpecs = make(map[string]*datamodel.AdminJobSpec)
	})

	t.Run("WhenCERTIFICATE_ROTATION_CRON_EXPRESSION_IsNotSet", func(tt *testing.T) {
		_ = os.Unsetenv("CERTIFICATE_ROTATION_CRON_EXPRESSION")

		data = []byte(`{"ROTATE_VSA_CERTIFICATE_AND_PASSWORD": {"jobType": "ROTATE_VSA_CERTIFICATE_AND_PASSWORD", "cronExpression": "0 0 * * *", "state": "CREATING"}}`)
		err := LoadJobSpecs()
		assert.NoError(tt, err)
		assert.Len(tt, adminJobSpecs, 1)

		spec, exists := adminJobSpecs["ROTATE_VSA_CERTIFICATE_AND_PASSWORD"]
		assert.True(tt, exists)
		assert.Equal(tt, "0 0 * * *", spec.CronExpression)

		adminJobSpecs = make(map[string]*datamodel.AdminJobSpec)
	})

	t.Run("WhenCERTIFICATE_ROTATION_CRON_EXPRESSION_IsEmpty", func(tt *testing.T) {
		err := os.Setenv("CERTIFICATE_ROTATION_CRON_EXPRESSION", "")
		assert.NoError(tt, err)
		defer func() {
			_ = os.Unsetenv("CERTIFICATE_ROTATION_CRON_EXPRESSION")
		}()

		data = []byte(`{"ROTATE_VSA_CERTIFICATE_AND_PASSWORD": {"jobType": "ROTATE_VSA_CERTIFICATE_AND_PASSWORD", "cronExpression": "0 0 * * *", "state": "CREATING"}}`)
		err = LoadJobSpecs()
		assert.NoError(tt, err)
		assert.Len(tt, adminJobSpecs, 1)

		spec, exists := adminJobSpecs["ROTATE_VSA_CERTIFICATE_AND_PASSWORD"]
		assert.True(tt, exists)
		assert.Equal(tt, "0 0 * * *", spec.CronExpression)

		adminJobSpecs = make(map[string]*datamodel.AdminJobSpec)
	})

	t.Run("WhenHarvestPollerDriftReconcileDisabled_JobRemoved", func(tt *testing.T) {
		err := os.Setenv("ENABLE_HARVEST_POLLER_DRIFT_RECONCILE", "false")
		assert.NoError(tt, err)
		defer func() {
			_ = os.Unsetenv("ENABLE_HARVEST_POLLER_DRIFT_RECONCILE")
		}()

		data = []byte(`{"HARVEST_POLLER_DRIFT_RECONCILE_JOB": {"jobType": "HARVEST_POLLER_DRIFT_RECONCILE_JOB", "cronExpression": "0 0 * * *", "state": "CREATING"}}`)
		err = LoadJobSpecs()
		assert.NoError(tt, err)
		_, exists := adminJobSpecs["HARVEST_POLLER_DRIFT_RECONCILE_JOB"]
		assert.False(tt, exists)
		adminJobSpecs = make(map[string]*datamodel.AdminJobSpec)
	})

	t.Run("WhenHarvestPollerDriftReconcileEnabled_UsesConfiguredCronAndTimezone", func(tt *testing.T) {
		err := os.Setenv("ENABLE_HARVEST_POLLER_DRIFT_RECONCILE", "true")
		assert.NoError(tt, err)
		err = os.Setenv("HARVEST_RECONCILE_CRON_EXPRESSION", "*/15 * * * *")
		assert.NoError(tt, err)
		err = os.Setenv("HARVEST_RECONCILE_TIMEZONE", "UTC")
		assert.NoError(tt, err)
		defer func() {
			_ = os.Unsetenv("ENABLE_HARVEST_POLLER_DRIFT_RECONCILE")
			_ = os.Unsetenv("HARVEST_RECONCILE_CRON_EXPRESSION")
			_ = os.Unsetenv("HARVEST_RECONCILE_TIMEZONE")
		}()

		data = []byte(`{"HARVEST_POLLER_DRIFT_RECONCILE_JOB": {"jobType": "HARVEST_POLLER_DRIFT_RECONCILE_JOB", "cronExpression": "0 0 * * *", "state": "CREATING"}}`)
		err = LoadJobSpecs()
		assert.NoError(tt, err)
		spec, exists := adminJobSpecs["HARVEST_POLLER_DRIFT_RECONCILE_JOB"]
		assert.True(tt, exists)
		assert.Equal(tt, "CRON_TZ=UTC */15 * * * *", spec.CronExpression)
		adminJobSpecs = make(map[string]*datamodel.AdminJobSpec)
	})
}

func TestLaunchJobManagerWorkflow(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	t.Run("WhenJobManagerWorkflowLaunchesSuccessfully", func(tt *testing.T) {
		client := workflow_engine.NewMockTemporalTestClient(t)
		client.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		err := LaunchJobManagerWorkflow(context.TODO(), client, logger)
		assert.NoError(tt, err)
	})
	t.Run("WhenJobManagerWorkflowLaunchFails", func(tt *testing.T) {
		client := workflow_engine.NewMockTemporalTestClient(t)
		client.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(nil, temporal.ErrScheduleAlreadyRunning)

		err := LaunchJobManagerWorkflow(context.TODO(), client, logger)
		assert.Error(tt, err)
	})
}

func TestDeleteAllAdminSchedules(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}

	// Create a mock temporal client that returns our mock schedule client
	mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
	mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

	specScheduled := &datamodel.AdminJobSpec{BaseModel: datamodel.BaseModel{UUID: "uuid-1"}, JobType: "JOB1", State: scheduler.JobStatusScheduled}
	specCreating := &datamodel.AdminJobSpec{BaseModel: datamodel.BaseModel{UUID: "uuid-2"}, JobType: "JOB2", State: scheduler.JobStatusCreating}

	// Storage returns two specs across states
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusScheduled).Return([]*datamodel.AdminJobSpec{specScheduled}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusCreating).Return([]*datamodel.AdminJobSpec{specCreating}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusUpdating).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusDeleting).Return([]*datamodel.AdminJobSpec{}, nil)

	// Schedule deletes for both UUIDs
	h1 := &mocks.ScheduleHandle{}
	h2 := &mocks.ScheduleHandle{}
	mockScheduleClient.On("GetHandle", mock.Anything, "uuid-1").Return(h1)
	mockScheduleClient.On("GetHandle", mock.Anything, "uuid-2").Return(h2)
	h1.On("Delete", mock.Anything).Return(nil)
	h2.On("Delete", mock.Anything).Return(nil)

	// Updating storage to DELETED should be called twice
	mockStorage.On("UpdateAdminJobSpec", ctx, mock.Anything).Return(nil).Twice()

	err := DeleteAllAdminSchedules(ctx, mockStorage, mockTemporal, logger)
	assert.NoError(t, err)

	mockScheduleClient.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

func TestDeleteAllAdminSchedules_FetchError(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	mockStorage := database.NewMockStorage(t)
	// Note: No need to mock ScheduleClient since the function returns early on the first error
	// and never creates the Temporal scheduler

	// Mock all states that the function iterates through
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusScheduled).Return(nil, errors.New("db error"))
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusCreating).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusUpdating).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusDeleting).Return([]*datamodel.AdminJobSpec{}, nil)

	err := DeleteAllAdminSchedules(ctx, mockStorage, nil, logger)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteAllAdminSchedules_DeleteError_Continues(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
	mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

	spec := &datamodel.AdminJobSpec{BaseModel: datamodel.BaseModel{UUID: "uuid-err"}, JobType: "JOB_ERR", State: scheduler.JobStatusScheduled}
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusScheduled).Return([]*datamodel.AdminJobSpec{spec}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusCreating).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusUpdating).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusDeleting).Return([]*datamodel.AdminJobSpec{}, nil)

	h := &mocks.ScheduleHandle{}
	mockScheduleClient.On("GetHandle", mock.Anything, "uuid-err").Return(h)
	h.On("Delete", mock.Anything).Return(errors.New("delete failed"))

	// Should still mark spec as deleted
	mockStorage.On("UpdateAdminJobSpec", ctx, mock.Anything).Return(nil)

	err := DeleteAllAdminSchedules(ctx, mockStorage, mockTemporal, logger)
	assert.NoError(t, err)

	mockScheduleClient.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

func TestDeleteAllAdminSchedules_UpdateError(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
	mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

	spec := &datamodel.AdminJobSpec{BaseModel: datamodel.BaseModel{UUID: "uuid-upd"}, JobType: "JOB_UPD", State: scheduler.JobStatusScheduled}
	// Mock all states that the function iterates through
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusScheduled).Return([]*datamodel.AdminJobSpec{spec}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusCreating).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusUpdating).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusDeleting).Return([]*datamodel.AdminJobSpec{}, nil)

	h := &mocks.ScheduleHandle{}
	mockScheduleClient.On("GetHandle", mock.Anything, "uuid-upd").Return(h)
	h.On("Delete", mock.Anything).Return(nil)

	mockStorage.On("UpdateAdminJobSpec", ctx, mock.Anything).Return(vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, errors.New("update failed")))

	err := DeleteAllAdminSchedules(ctx, mockStorage, mockTemporal, logger)
	assert.Error(t, err)

	mockScheduleClient.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

func TestRecreateAdminSchedules_Success_CreateAndUpdate(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	// Embedded specs to upsert
	adminJobSpecs = map[string]*datamodel.AdminJobSpec{
		"JOB_CREATE": {
			JobType:        "JOB_CREATE",
			CronExpression: "0 */5 * * * *",
			State:          scheduler.JobStatusCreating,
		},
		"JOB_UPDATE": {
			JobType:        "JOB_UPDATE",
			CronExpression: "0 */10 * * * *",
			State:          scheduler.JobStatusCreating,
		},
	}

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
	mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

	// Existing schedules to be deleted across states
	specScheduled := &datamodel.AdminJobSpec{BaseModel: datamodel.BaseModel{UUID: "uuid-del-1"}, JobType: "JOB1", State: scheduler.JobStatusScheduled}
	specCreating := &datamodel.AdminJobSpec{BaseModel: datamodel.BaseModel{UUID: "uuid-del-2"}, JobType: "JOB2", State: scheduler.JobStatusCreating}
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusScheduled).Return([]*datamodel.AdminJobSpec{specScheduled}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusCreating).Return([]*datamodel.AdminJobSpec{specCreating}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusUpdating).Return([]*datamodel.AdminJobSpec{}, nil)

	// Expect temporal deletes
	h1 := &mocks.ScheduleHandle{}
	h2 := &mocks.ScheduleHandle{}
	mockScheduleClient.On("GetHandle", mock.Anything, "uuid-del-1").Return(h1)
	mockScheduleClient.On("GetHandle", mock.Anything, "uuid-del-2").Return(h2)
	h1.On("Delete", mock.Anything).Return(nil)
	h2.On("Delete", mock.Anything).Return(nil)

	// Upsert: JOB_CREATE not found -> create; JOB_UPDATE found -> update
	mockStorage.On("GetAdminJobSpecByJobType", ctx, "JOB_CREATE").Return(nil, gorm.ErrRecordNotFound)
	mockStorage.On("CreateAdminJobSpec", ctx, mock.Anything).Return(&datamodel.AdminJobSpec{JobType: "JOB_CREATE"}, nil)

	existing := &datamodel.AdminJobSpec{BaseModel: datamodel.BaseModel{ID: 10, UUID: "uuid-existing"}, JobType: "JOB_UPDATE", CronExpression: "0 */3 * * * *"}
	mockStorage.On("GetAdminJobSpecByJobType", ctx, "JOB_UPDATE").Return(existing, nil)
	mockStorage.On("UpdateAdminJobSpec", ctx, mock.Anything).Return(nil)

	err := RecreateAdminSchedules(ctx, mockStorage, mockTemporal, logger)
	assert.NoError(t, err)

	mockScheduleClient.AssertExpectations(t)
	mockTemporal.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

func TestRecreateAdminSchedules_DeleteError_Continues(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	adminJobSpecs = map[string]*datamodel.AdminJobSpec{
		"JOB_CREATE": {JobType: "JOB_CREATE", CronExpression: "0 */5 * * * *", State: scheduler.JobStatusCreating},
	}

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
	mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

	specScheduled := &datamodel.AdminJobSpec{BaseModel: datamodel.BaseModel{UUID: "uuid-del-err"}, JobType: "JOB1", State: scheduler.JobStatusScheduled}
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusScheduled).Return([]*datamodel.AdminJobSpec{specScheduled}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusCreating).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusUpdating).Return([]*datamodel.AdminJobSpec{}, nil)

	h := &mocks.ScheduleHandle{}
	mockScheduleClient.On("GetHandle", mock.Anything, "uuid-del-err").Return(h)
	h.On("Delete", mock.Anything).Return(errors.New("delete failed"))

	mockStorage.On("GetAdminJobSpecByJobType", ctx, "JOB_CREATE").Return(nil, gorm.ErrRecordNotFound)
	mockStorage.On("CreateAdminJobSpec", ctx, mock.Anything).Return(&datamodel.AdminJobSpec{JobType: "JOB_CREATE"}, nil)

	err := RecreateAdminSchedules(ctx, mockStorage, mockTemporal, logger)
	assert.NoError(t, err)

	mockScheduleClient.AssertExpectations(t)
	mockTemporal.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

func TestRecreateAdminSchedules_FetchStateError(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	adminJobSpecs = map[string]*datamodel.AdminJobSpec{}

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
	mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusScheduled).Return(nil, errors.New("db fetch error"))

	err := RecreateAdminSchedules(ctx, mockStorage, mockTemporal, logger)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestRecreateAdminSchedules_GetByJobTypeError(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	adminJobSpecs = map[string]*datamodel.AdminJobSpec{
		"JOB_ERR": {JobType: "JOB_ERR", CronExpression: "* * * * *", State: scheduler.JobStatusCreating},
	}

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
	mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusScheduled).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusCreating).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusUpdating).Return([]*datamodel.AdminJobSpec{}, nil)

	mockStorage.On("GetAdminJobSpecByJobType", ctx, "JOB_ERR").Return(nil, gorm.ErrInvalidDB)

	err := RecreateAdminSchedules(ctx, mockStorage, mockTemporal, logger)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestRecreateAdminSchedules_CreateError(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	adminJobSpecs = map[string]*datamodel.AdminJobSpec{
		"JOB_CREATE": {JobType: "JOB_CREATE", CronExpression: "* * * * *", State: scheduler.JobStatusCreating},
	}

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
	mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusScheduled).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusCreating).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusUpdating).Return([]*datamodel.AdminJobSpec{}, nil)

	mockStorage.On("GetAdminJobSpecByJobType", ctx, "JOB_CREATE").Return(nil, gorm.ErrRecordNotFound)
	mockStorage.On("CreateAdminJobSpec", ctx, mock.Anything).Return(nil, errors.New("create failed"))

	err := RecreateAdminSchedules(ctx, mockStorage, mockTemporal, logger)
	assert.Error(t, err)
}

func TestRecreateAdminSchedules_UpdateError(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	adminJobSpecs = map[string]*datamodel.AdminJobSpec{
		"JOB_UPDATE": {JobType: "JOB_UPDATE", CronExpression: "* * * * *", State: scheduler.JobStatusCreating},
	}

	mockStorage := database.NewMockStorage(t)
	mockScheduleClient := &mocks.ScheduleClient{}
	mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
	mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusScheduled).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusCreating).Return([]*datamodel.AdminJobSpec{}, nil)
	mockStorage.On("GetAdminJobSpecsByState", ctx, scheduler.JobStatusUpdating).Return([]*datamodel.AdminJobSpec{}, nil)

	existing := &datamodel.AdminJobSpec{BaseModel: datamodel.BaseModel{ID: 99, UUID: "uuid-ex"}, JobType: "JOB_UPDATE"}
	mockStorage.On("GetAdminJobSpecByJobType", ctx, "JOB_UPDATE").Return(existing, nil)
	mockStorage.On("UpdateAdminJobSpec", ctx, mock.Anything).Return(errors.New("update failed"))

	err := RecreateAdminSchedules(ctx, mockStorage, mockTemporal, logger)
	assert.Error(t, err)
}

// TestCleanupSingleJobSpec tests the cleanupSingleJobSpec function
func TestCleanupSingleJobSpec(t *testing.T) {
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	t.Run("WhenScheduleDeleteSucceedsAndUpdateSucceeds", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockScheduleClient := &mocks.ScheduleClient{}
		mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
		mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

		spec := &datamodel.AdminJobSpec{
			BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
			JobType:   "TEST_JOB",
			State:     scheduler.JobStatusScheduled,
		}

		// Mock successful schedule deletion
		mockHandle := &mocks.ScheduleHandle{}
		mockScheduleClient.On("GetHandle", ctx, "test-uuid").Return(mockHandle)
		mockHandle.On("Delete", ctx).Return(nil)

		// Mock successful database update
		mockStorage.On("UpdateAdminJobSpec", ctx, mock.Anything).Return(nil)

		err := cleanupSingleJobSpec(ctx, mockStorage, mockTemporal, spec, logger)
		assert.NoError(tt, err)

		// Verify the spec was updated correctly
		assert.Equal(tt, scheduler.JobStatusDeleted, spec.State)
		assert.NotNil(tt, spec.DeletedAt)
		assert.True(tt, spec.DeletedAt.Valid)

		mockScheduleClient.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenScheduleDeleteFailsButUpdateSucceeds", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockScheduleClient := &mocks.ScheduleClient{}
		mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
		mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

		spec := &datamodel.AdminJobSpec{
			BaseModel: datamodel.BaseModel{UUID: "test-uuid-fail"},
			JobType:   "TEST_JOB_FAIL",
			State:     scheduler.JobStatusScheduled,
		}

		// Mock failed schedule deletion
		mockHandle := &mocks.ScheduleHandle{}
		mockScheduleClient.On("GetHandle", ctx, "test-uuid-fail").Return(mockHandle)
		mockHandle.On("Delete", ctx).Return(errors.New("schedule delete failed"))

		// Mock successful database update (should still succeed despite schedule delete failure)
		mockStorage.On("UpdateAdminJobSpec", ctx, mock.Anything).Return(nil)

		err := cleanupSingleJobSpec(ctx, mockStorage, mockTemporal, spec, logger)
		assert.NoError(tt, err)

		// Verify the spec was still updated correctly
		assert.Equal(tt, scheduler.JobStatusDeleted, spec.State)
		assert.NotNil(tt, spec.DeletedAt)
		assert.True(tt, spec.DeletedAt.Valid)

		mockScheduleClient.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenScheduleDeleteSucceedsButUpdateFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockScheduleClient := &mocks.ScheduleClient{}
		mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
		mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

		spec := &datamodel.AdminJobSpec{
			BaseModel: datamodel.BaseModel{UUID: "test-uuid-update-fail"},
			JobType:   "TEST_JOB_UPDATE_FAIL",
			State:     scheduler.JobStatusScheduled,
		}

		// Mock successful schedule deletion
		mockHandle := &mocks.ScheduleHandle{}
		mockScheduleClient.On("GetHandle", ctx, "test-uuid-update-fail").Return(mockHandle)
		mockHandle.On("Delete", ctx).Return(nil)

		// Mock failed database update
		mockStorage.On("UpdateAdminJobSpec", ctx, mock.Anything).Return(errors.New("database update failed"))

		err := cleanupSingleJobSpec(ctx, mockStorage, mockTemporal, spec, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to mark admin job spec as deleted for jobType TEST_JOB_UPDATE_FAIL")

		mockScheduleClient.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenBothScheduleDeleteAndUpdateFail", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockScheduleClient := &mocks.ScheduleClient{}
		mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
		mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

		spec := &datamodel.AdminJobSpec{
			BaseModel: datamodel.BaseModel{UUID: "test-uuid-both-fail"},
			JobType:   "TEST_JOB_BOTH_FAIL",
			State:     scheduler.JobStatusScheduled,
		}

		// Mock failed schedule deletion
		mockHandle := &mocks.ScheduleHandle{}
		mockScheduleClient.On("GetHandle", ctx, "test-uuid-both-fail").Return(mockHandle)
		mockHandle.On("Delete", ctx).Return(errors.New("schedule delete failed"))

		// Mock failed database update
		mockStorage.On("UpdateAdminJobSpec", ctx, mock.Anything).Return(errors.New("database update failed"))

		err := cleanupSingleJobSpec(ctx, mockStorage, mockTemporal, spec, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to mark admin job spec as deleted for jobType TEST_JOB_BOTH_FAIL")

		mockScheduleClient.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSpecHasEmptyUUID", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockScheduleClient := &mocks.ScheduleClient{}
		mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
		mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

		spec := &datamodel.AdminJobSpec{
			BaseModel: datamodel.BaseModel{UUID: ""},
			JobType:   "TEST_JOB_EMPTY_UUID",
			State:     scheduler.JobStatusScheduled,
		}

		// Mock successful schedule deletion (even with empty UUID)
		mockHandle := &mocks.ScheduleHandle{}
		mockScheduleClient.On("GetHandle", ctx, "").Return(mockHandle)
		mockHandle.On("Delete", ctx).Return(nil)

		// Mock successful database update
		mockStorage.On("UpdateAdminJobSpec", ctx, mock.Anything).Return(nil)

		err := cleanupSingleJobSpec(ctx, mockStorage, mockTemporal, spec, logger)
		assert.NoError(tt, err)

		// Verify the spec was updated correctly
		assert.Equal(tt, scheduler.JobStatusDeleted, spec.State)
		assert.NotNil(tt, spec.DeletedAt)
		assert.True(tt, spec.DeletedAt.Valid)

		mockScheduleClient.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSpecHasSpecialCharactersInJobType", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockScheduleClient := &mocks.ScheduleClient{}
		mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
		mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

		spec := &datamodel.AdminJobSpec{
			BaseModel: datamodel.BaseModel{UUID: "test-uuid-special"},
			JobType:   "TEST_JOB_WITH_SPECIAL_CHARS_!@#$%^&*()",
			State:     scheduler.JobStatusScheduled,
		}

		// Mock successful schedule deletion
		mockHandle := &mocks.ScheduleHandle{}
		mockScheduleClient.On("GetHandle", ctx, "test-uuid-special").Return(mockHandle)
		mockHandle.On("Delete", ctx).Return(nil)

		// Mock successful database update
		mockStorage.On("UpdateAdminJobSpec", ctx, mock.Anything).Return(nil)

		err := cleanupSingleJobSpec(ctx, mockStorage, mockTemporal, spec, logger)
		assert.NoError(tt, err)

		// Verify the spec was updated correctly
		assert.Equal(tt, scheduler.JobStatusDeleted, spec.State)
		assert.NotNil(tt, spec.DeletedAt)
		assert.True(tt, spec.DeletedAt.Valid)

		mockScheduleClient.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenContextIsCancelled", func(tt *testing.T) {
		// Create a cancelled context
		cancelledCtx, cancel := context.WithCancel(context.TODO())
		cancel() // Cancel immediately

		mockStorage := database.NewMockStorage(t)
		mockScheduleClient := &mocks.ScheduleClient{}
		mockTemporal := workflow_engine.NewMockTemporalTestClient(t)
		mockTemporal.On("ScheduleClient").Return(mockScheduleClient)

		spec := &datamodel.AdminJobSpec{
			BaseModel: datamodel.BaseModel{UUID: "test-uuid-cancelled"},
			JobType:   "TEST_JOB_CANCELLED",
			State:     scheduler.JobStatusScheduled,
		}

		// Mock schedule deletion with context cancellation error
		mockHandle := &mocks.ScheduleHandle{}
		mockScheduleClient.On("GetHandle", cancelledCtx, "test-uuid-cancelled").Return(mockHandle)
		mockHandle.On("Delete", cancelledCtx).Return(context.Canceled)

		// Mock successful database update (should still succeed despite schedule delete failure)
		mockStorage.On("UpdateAdminJobSpec", cancelledCtx, mock.Anything).Return(nil)

		err := cleanupSingleJobSpec(cancelledCtx, mockStorage, mockTemporal, spec, logger)
		assert.NoError(tt, err)

		// Verify the spec was still updated correctly
		assert.Equal(tt, scheduler.JobStatusDeleted, spec.State)
		assert.NotNil(tt, spec.DeletedAt)
		assert.True(tt, spec.DeletedAt.Valid)

		mockScheduleClient.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
}
