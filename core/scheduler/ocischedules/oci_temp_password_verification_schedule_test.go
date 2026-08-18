package ocischedules

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

func setTempPasswordVerificationEnabled(t *testing.T, enabled bool) {
	t.Helper()
	orig := env.EnableOCITempPasswordResetCron
	env.EnableOCITempPasswordResetCron = enabled
	t.Cleanup(func() { env.EnableOCITempPasswordResetCron = orig })
}

func TestEnsureOciTempPasswordVerificationSchedule_Disabled_NoExistingSchedule(t *testing.T) {
	setTempPasswordVerificationEnabled(t, false)
	ctx := context.Background()

	schedCl := &mocks.ScheduleClient{}
	handle := &mockScheduleHandle{id: OciTempPasswordVerificationScheduleID, describeFunc: describeNotFound}
	schedCl.On("GetHandle", ctx, OciTempPasswordVerificationScheduleID).Return(handle).Once()

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciTempPasswordVerificationSchedule(ctx, cl, log.NewLogger())
	require.NoError(t, err)
	schedCl.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	schedCl.AssertExpectations(t)
}

func TestEnsureOciTempPasswordVerificationSchedule_Disabled_DeletesExisting(t *testing.T) {
	setTempPasswordVerificationEnabled(t, false)
	ctx := context.Background()

	var deleted bool
	handle := &mockScheduleHandle{
		id:           OciTempPasswordVerificationScheduleID,
		describeFunc: describeFound,
		deleteFunc:   func(context.Context) error { deleted = true; return nil },
	}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciTempPasswordVerificationScheduleID).Return(handle)

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciTempPasswordVerificationSchedule(ctx, cl, log.NewLogger())
	require.NoError(t, err)
	require.True(t, deleted, "existing schedule must be deleted when verification is disabled")
}

func TestEnsureOciTempPasswordVerificationSchedule_Enabled_CreatesWhenMissing(t *testing.T) {
	setTempPasswordVerificationEnabled(t, true)
	ctx := context.Background()

	handle := &mockScheduleHandle{id: OciTempPasswordVerificationScheduleID, describeFunc: describeNotFound}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciTempPasswordVerificationScheduleID).Return(handle).Once()
	schedCl.On("Create", ctx, mock.Anything).Return(handle, nil).Once()

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciTempPasswordVerificationSchedule(ctx, cl, log.NewLogger())
	require.NoError(t, err)
	schedCl.AssertExpectations(t)
}

func TestEnsureOciTempPasswordVerificationSchedule_Enabled_UpdatesWhenPresent(t *testing.T) {
	setTempPasswordVerificationEnabled(t, true)
	ctx := context.Background()

	var updated bool
	var repointedQueue string
	handle := &mockScheduleHandle{
		id:           OciTempPasswordVerificationScheduleID,
		describeFunc: describeFound,
		updateFunc: func(_ context.Context, opts client.ScheduleUpdateOptions) error {
			updated = true
			// Drive the reconcile callback against a schedule sitting on a stale
			// queue to prove the existing schedule gets re-pointed, not just spec-synced.
			input := client.ScheduleUpdateInput{
				Description: client.ScheduleDescription{
					Schedule: client.Schedule{
						Action: &client.ScheduleWorkflowAction{TaskQueue: "stale-queue"},
					},
				},
			}
			upd, err := opts.DoUpdate(input)
			if err != nil {
				return err
			}
			repointedQueue = upd.Schedule.Action.(*client.ScheduleWorkflowAction).TaskQueue
			return nil
		},
	}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciTempPasswordVerificationScheduleID).Return(handle)

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciTempPasswordVerificationSchedule(ctx, cl, log.NewLogger())
	require.NoError(t, err)
	require.True(t, updated, "existing schedule must be synced when verification is enabled")
	assert.Equal(t, workflowengine.CustomerTaskQueue, repointedQueue, "schedule must be re-pointed to the customer task queue")
	schedCl.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestEnsureOciTempPasswordVerificationSchedule_Enabled_CreateAlreadyExistsTolerated(t *testing.T) {
	setTempPasswordVerificationEnabled(t, true)
	ctx := context.Background()

	handle := &mockScheduleHandle{id: OciTempPasswordVerificationScheduleID, describeFunc: describeNotFound}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciTempPasswordVerificationScheduleID).Return(handle).Once()
	schedCl.On("Create", ctx, mock.Anything).Return(nil, errors.New("schedule already running"))

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciTempPasswordVerificationSchedule(ctx, cl, log.NewLogger())
	require.NoError(t, err, "a concurrent create race must be tolerated")
	schedCl.AssertExpectations(t)
}

func TestEnsureOciTempPasswordVerificationSchedule_Disabled_DeleteFails(t *testing.T) {
	setTempPasswordVerificationEnabled(t, false)
	ctx := context.Background()

	handle := &mockScheduleHandle{
		id:           OciTempPasswordVerificationScheduleID,
		describeFunc: describeFound,
		deleteFunc:   func(context.Context) error { return errors.New("delete failed") },
	}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciTempPasswordVerificationScheduleID).Return(handle)

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciTempPasswordVerificationSchedule(ctx, cl, log.NewLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")
}

func TestEnsureOciTempPasswordVerificationSchedule_Enabled_UpdateFails(t *testing.T) {
	setTempPasswordVerificationEnabled(t, true)
	ctx := context.Background()

	handle := &mockScheduleHandle{
		id:           OciTempPasswordVerificationScheduleID,
		describeFunc: describeFound,
		updateFunc:   func(context.Context, client.ScheduleUpdateOptions) error { return errors.New("update failed") },
	}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciTempPasswordVerificationScheduleID).Return(handle)

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciTempPasswordVerificationSchedule(ctx, cl, log.NewLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update failed")
}

// A transient lookup failure must not be read as "schedule absent": on the
// disable path that would silently skip the delete and leave it firing.
func TestEnsureOciTempPasswordVerificationSchedule_Disabled_LookupFails(t *testing.T) {
	setTempPasswordVerificationEnabled(t, false)
	ctx := context.Background()

	handle := &mockScheduleHandle{
		id:           OciTempPasswordVerificationScheduleID,
		describeFunc: describeTransientError,
	}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciTempPasswordVerificationScheduleID).Return(handle)

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciTempPasswordVerificationSchedule(ctx, cl, log.NewLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temporal unavailable")
}

func TestEnsureOciTempPasswordVerificationSchedule_Enabled_LookupFails(t *testing.T) {
	setTempPasswordVerificationEnabled(t, true)
	ctx := context.Background()

	handle := &mockScheduleHandle{
		id:           OciTempPasswordVerificationScheduleID,
		describeFunc: describeTransientError,
	}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciTempPasswordVerificationScheduleID).Return(handle)

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciTempPasswordVerificationSchedule(ctx, cl, log.NewLogger())
	require.Error(t, err)
	schedCl.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestEnsureOciTempPasswordVerificationSchedule_Enabled_CreateFails(t *testing.T) {
	setTempPasswordVerificationEnabled(t, true)
	ctx := context.Background()

	handle := &mockScheduleHandle{id: OciTempPasswordVerificationScheduleID, describeFunc: describeNotFound}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciTempPasswordVerificationScheduleID).Return(handle)
	// Create is retried up to DefaultMaxRetries; keep the expectation unbounded.
	schedCl.On("Create", ctx, mock.Anything).Return(nil, errors.New("permission denied"))

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciTempPasswordVerificationSchedule(ctx, cl, log.NewLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}
