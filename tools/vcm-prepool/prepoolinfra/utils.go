package prepoolinfra

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/retry"
)

func waitForOperation(
	ctx context.Context,
	resourceType string,
	operationName string,
	getStatus func() (*models.ComputeOperation, error),
) (*models.ComputeOperation, error) {
	for attempt := 1; attempt <= operationPollAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("poll %s operation %s: %w", resourceType, operationName, err)
		}

		operation, err := getStatus()
		switch {
		case err != nil && !retry.ShouldRetry(err):
			return nil, fmt.Errorf("poll %s operation %s: %w", resourceType, operationName, err)
		case err != nil:
			// A transient front-end error says nothing about the operation
			// itself, so keep polling instead of failing the cleanup.
			slog.Warn("transient error polling operation",
				"resourceType", resourceType, "operation", operationName, "attempt", attempt, "error", err)
		case operation != nil && operation.Done:
			if operation.ErrorResponse != "" {
				return nil, fmt.Errorf("%s operation %s failed: %s", resourceType, operationName, operation.ErrorResponse)
			}
			return operation, nil
		}
		if attempt == operationPollAttempts {
			break
		}

		slog.Info("waiting for operation", "resourceType", resourceType, "operation", operationName, "attempt", attempt)
		timer := time.NewTimer(operationPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, fmt.Errorf("poll %s operation %s: %w", resourceType, operationName, ctx.Err())
		case <-timer.C:
		}
	}

	return nil, fmt.Errorf("%s operation %s timed out after 5 minutes", resourceType, operationName)
}

// retryTransient re-runs fn while it returns an error the shared Google retry
// classifier considers transient (5xx, rate limits, IAM policy conflicts).
func retryTransient(ctx context.Context, operation string, fn func() error) error {
	var err error
	for attempt := 1; attempt <= transientRetryAttempts; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%s: %w", operation, ctxErr)
		}

		if err = fn(); err == nil {
			return nil
		}
		if !retry.ShouldRetry(err) || attempt == transientRetryAttempts {
			return err
		}

		backoff := transientRetryInitialBackoff * (1 << (attempt - 1))
		slog.Warn("retrying transient GCP error",
			"operation", operation, "attempt", attempt, "backoff", backoff, "error", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", operation, ctx.Err())
		case <-time.After(backoff):
		}
	}
	return err
}
