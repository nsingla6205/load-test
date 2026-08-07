package ocischedules

import (
	"context"
	"errors"

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
