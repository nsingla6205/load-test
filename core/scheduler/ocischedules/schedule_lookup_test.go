package ocischedules

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

// The fix's core guarantee: reconciling an existing schedule re-points its
// workflow action to the current task queue (and syncs the cron), instead of
// leaving it on the release-scoped queue it was created with.
func TestSyncScheduleSpecAndTaskQueue_RepointsTaskQueueAndSpec(t *testing.T) {
	ctx := context.Background()
	const scheduleID = "sched-repoint"
	const newQueue = "customer-workflows-26074-0-0-oci-rc-12"
	newSpec := client.ScheduleSpec{CronExpressions: []string{"0 5 * * *"}}

	var captured *client.Schedule
	handle := &mockScheduleHandle{
		updateFunc: func(_ context.Context, opts client.ScheduleUpdateOptions) error {
			input := client.ScheduleUpdateInput{
				Description: client.ScheduleDescription{
					Schedule: client.Schedule{
						Action: &client.ScheduleWorkflowAction{
							ID:        "wf-id",
							TaskQueue: "customer-workflows-26074-0-0-oci-rc-11", // stale queue with no workers
							Workflow:  "VerifyOciTempPasswordsWorkflow",
						},
						Spec: &client.ScheduleSpec{CronExpressions: []string{"0 1 * * *"}},
					},
				},
			}
			upd, err := opts.DoUpdate(input)
			if err != nil {
				return err
			}
			captured = upd.Schedule
			return nil
		},
	}

	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, scheduleID).Return(handle).Once()

	err := syncScheduleSpecAndTaskQueue(ctx, schedCl, scheduleID, newSpec, newQueue)
	require.NoError(t, err)
	require.NotNil(t, captured)

	action, ok := captured.Action.(*client.ScheduleWorkflowAction)
	require.True(t, ok)
	assert.Equal(t, newQueue, action.TaskQueue, "task queue must be re-pointed to the current release queue")
	assert.Equal(t, "wf-id", action.ID, "workflow id must be preserved")
	assert.Equal(t, "VerifyOciTempPasswordsWorkflow", action.Workflow, "workflow must be preserved")
	require.NotNil(t, captured.Spec)
	assert.Equal(t, newSpec.CronExpressions, captured.Spec.CronExpressions, "cron spec must be synced")
	schedCl.AssertExpectations(t)
}

// A schedule carrying an action this package did not create must surface an
// error rather than silently leaving the queue stale. A nil action is not a
// *client.ScheduleWorkflowAction, so it exercises the failed type assertion.
func TestSyncScheduleSpecAndTaskQueue_NonWorkflowActionErrors(t *testing.T) {
	ctx := context.Background()
	const scheduleID = "sched-bad-action"

	handle := &mockScheduleHandle{
		updateFunc: func(_ context.Context, opts client.ScheduleUpdateOptions) error {
			input := client.ScheduleUpdateInput{
				Description: client.ScheduleDescription{Schedule: client.Schedule{Action: nil}},
			}
			_, err := opts.DoUpdate(input)
			return err
		},
	}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, scheduleID).Return(handle).Once()

	err := syncScheduleSpecAndTaskQueue(ctx, schedCl, scheduleID, client.ScheduleSpec{}, "some-queue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "want *client.ScheduleWorkflowAction")
	schedCl.AssertExpectations(t)
}

func TestSyncScheduleSpecAndTaskQueue_PropagatesUpdateError(t *testing.T) {
	ctx := context.Background()
	const scheduleID = "sched-update-err"

	handle := &mockScheduleHandle{
		updateFunc: func(context.Context, client.ScheduleUpdateOptions) error {
			return errors.New("temporal update rejected")
		},
	}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, scheduleID).Return(handle).Once()

	err := syncScheduleSpecAndTaskQueue(ctx, schedCl, scheduleID, client.ScheduleSpec{}, "some-queue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temporal update rejected")
	schedCl.AssertExpectations(t)
}
