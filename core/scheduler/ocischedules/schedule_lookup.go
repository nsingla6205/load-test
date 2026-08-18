package ocischedules

import (
	"context"
	"errors"
	"fmt"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

func scheduleExists(ctx context.Context, scheduleClient client.ScheduleClient, scheduleID string) (bool, error) {
	if _, err := scheduleClient.GetHandle(ctx, scheduleID).Describe(ctx); err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func syncScheduleSpecAndTaskQueue(ctx context.Context, scheduleClient client.ScheduleClient, scheduleID string, spec client.ScheduleSpec, taskQueue string) error {
	handle := scheduleClient.GetHandle(ctx, scheduleID)
	return handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			input.Description.Schedule.Spec = &spec

			action, ok := input.Description.Schedule.Action.(*client.ScheduleWorkflowAction)
			if !ok {
				return nil, fmt.Errorf("schedule %q action is %T, want *client.ScheduleWorkflowAction", scheduleID, input.Description.Schedule.Action)
			}
			action.TaskQueue = taskQueue

			return &client.ScheduleUpdate{
				Schedule: &input.Description.Schedule,
			}, nil
		},
	})
}
