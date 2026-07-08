package backgroundactivities

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

type ResourceDeleteActivity struct {
	SE database.Storage
}

// ParentChildWorkflowError represents a resource error with resource name and error message
type ParentChildWorkflowError struct {
	ResourceName string
	Error        string
}

// ResourceCleanupBatchReturnValue represents the result of a resource cleanup batch
type ResourceCleanupBatchReturnValue struct {
	TotalProcessed       int
	Successful           int
	Failed               int
	FailedResourceNames  []string
	FailedResourceErrors []ParentChildWorkflowError
}

// GetTotalResourceCount returns the total count of pending resource deletions
func (a *ResourceDeleteActivity) GetTotalResourceCount(ctx context.Context) (int, error) {
	logger := util.GetLogger(ctx)
	se := a.SE
	activity.RecordHeartbeat(ctx, "Getting total resource count")

	// Use optimized CountPools method instead of fetching all pool data
	count, err := se.GetResourcesCount(ctx)
	if err != nil {
		logger.Errorf("Failed to count resources: %v", err)
		return 0, fmt.Errorf("failed to count resources")
	}
	activity.RecordHeartbeat(ctx, "Resource count retrieved successfully")

	logger.Infof("Total resources count: %d", count)
	return int(count), nil
}

// ListResourcesPaginated returns a paginated list of pending resource deletions
func (a *ResourceDeleteActivity) ListResourcesPaginated(ctx context.Context, offset, limit int) ([]*datamodel.PendingResourceDeletions, error) {
	logger := util.GetLogger(ctx)
	se := a.SE
	activity.RecordHeartbeat(ctx, "Listing resources paginated")

	resources, err := se.ListPendingResourceDeletions(ctx, offset, limit)
	if err != nil {
		logger.Errorf("Failed to list resources: %v", err)
		return nil, fmt.Errorf("failed to list resources")
	}
	activity.RecordHeartbeat(ctx, "Resources listed successfully")

	logger.Infof("Found %d resources (offset: %d, limit: %d)", len(resources), offset, limit)
	return resources, nil
}

// CleanupPendingResources processes a batch of pending resource deletions
func (a *ResourceDeleteActivity) CleanupPendingResources(ctx context.Context, resources []*datamodel.PendingResourceDeletions) (*ResourceCleanupBatchReturnValue, error) {
	logger := util.GetLogger(ctx)
	se := a.SE
	activity.RecordHeartbeat(ctx, "Starting resource cleanup batch")

	if len(resources) == 0 {
		return &ResourceCleanupBatchReturnValue{}, nil
	}

	logger.Infof("Starting batch processing of %d resources for cleanup", len(resources))
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Processing %d resources", len(resources)))

	// Get the cloud service instance once outside the loop for better performance
	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		logger.Error("Failed to initialize cloud service", "Error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("error initializing cloud service: %w", err))
	}

	result := &ResourceCleanupBatchReturnValue{
		TotalProcessed:       len(resources),
		FailedResourceNames:  []string{},
		FailedResourceErrors: []ParentChildWorkflowError{},
	}

	// Process each resource in the batch
	for i, resource := range resources {
		logger.Infof("Starting deletion process for resource %d: ID=%d, Type='%s', Name='%s'", i+1, resource.ID, resource.ResourceType, resource.ResourceName)
		resourceName := resource.ResourceName
		resourceType := resource.ResourceType

		var isDeleted bool
		var err error

		// Call appropriate deletion function based on resource type
		switch resourceType {
		case models.ResourceTypeStringBucket:
			isDeleted, err = activities.DeleteGCPBucket(ctx, resourceName, cloudService)
		default:
			logger.Infof("Unsupported resource type: %s", resourceType)
			result.Failed++
			result.FailedResourceNames = append(result.FailedResourceNames, resourceName)
			result.FailedResourceErrors = append(result.FailedResourceErrors, ParentChildWorkflowError{
				ResourceName: resourceName,
				Error:        fmt.Sprintf("Unsupported resource type: %s", resourceType),
			})
			continue
		}

		if isDeleted {
			logger.Infof("Resource %v of type %s deleted successfully", resourceName, resourceType)
			if _, updateErr := se.UpdatePendingResourceDeletion(ctx, resource.ID, true, ""); updateErr != nil {
				logger.Error("Failed to update resource status after successful deletion", "ResourceID", resource.ID, "Error", updateErr)
			} else {
				result.Successful++
			}
		} else {
			logger.Warnf("Resource %v of type %s is not deleted yet; incrementing retry counter", resourceName, resourceType)

			// Extract the original error message from VCP wrapped errors (same pattern as UpdateJobStatus)
			var errorMessage string
			if err != nil {
				var customError *vsaerrors.CustomError
				if vsaerrors.As(err, &customError) {
					errorMessage = customError.OriginalErr.Error()
				} else {
					errorMessage = err.Error()
				}
			} else {
				errorMessage = "Resource deletion failed"
			}

			if _, updateErr := se.UpdatePendingResourceDeletion(ctx, resource.ID, false, errorMessage); updateErr != nil {
				logger.Error("Failed to update resource status after failed deletion attempt", "ResourceID", resource.ID, "Error", updateErr)
			}
			result.Failed++
			result.FailedResourceNames = append(result.FailedResourceNames, resourceName)
			result.FailedResourceErrors = append(result.FailedResourceErrors, ParentChildWorkflowError{
				ResourceName: resourceName,
				Error:        errorMessage,
			})
		}
	}

	activity.RecordHeartbeat(ctx, "Resource cleanup batch completed")
	logger.Infof("Batch processing completed. Total: %d, Successful: %d, Failed: %d",
		result.TotalProcessed, result.Successful, result.Failed)

	return result, nil
}
