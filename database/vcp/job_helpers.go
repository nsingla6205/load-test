package database

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// GetExistingDeleteJobForDeletingState checks for an existing delete job when resource is in DELETING state.
func GetExistingDeleteJobForDeletingState(ctx context.Context, se Storage, resourceUUID string, deleteJobType datamodel.JobType, logger log.Logger) (existingJobUUID string) {
	existingJob, err := se.GetJobByResourceUUID(ctx, resourceUUID, string(deleteJobType))
	if err == nil && existingJob != nil {
		if existingJob.State != string(datamodel.JobsStateDONE) && existingJob.State != string(datamodel.JobsStateERROR) {
			logger.Infof("Delete job already in progress for %s, returning existing job UUID: %s", resourceUUID, existingJob.UUID)
			return existingJob.UUID
		}
	}
	return ""
}

// ValidateCorrelationIDForCreatingResource handles CREATING state logic for delete operations.
func ValidateCorrelationIDForCreatingResource(ctx context.Context, se Storage, resourceUUID string, resourceType string, createJobType datamodel.JobType, deleteJobType datamodel.JobType, logger log.Logger) (existingDeleteJobUUID string, isCleanupDelete bool, err error) {
	correlationID := utils.GetCoRelationIDFromContext(ctx)
	if correlationID == "" {
		logger.Warnf("Correlation ID is empty for delete request on %s %s in CREATING state", resourceType, resourceUUID)
		return "", false, customerrors.NewConflictErr(fmt.Sprintf("Error deleting %s - %s is already transitioning between states", resourceType, resourceType))
	}

	existingDeleteJob, err := se.GetJobByResourceUUID(ctx, resourceUUID, string(deleteJobType))
	if err == nil && existingDeleteJob != nil {
		if existingDeleteJob.CorrelationID != correlationID {
			logger.Warnf("Correlation ID mismatch: create job correlation ID %s does not match delete request correlation ID %s", existingDeleteJob.CorrelationID, correlationID)
			return "", false, customerrors.NewConflictErr(fmt.Sprintf("Error deleting %s - %s is already transitioning between states", resourceType, resourceType))
		}
		if existingDeleteJob.State != string(datamodel.JobsStateDONE) && existingDeleteJob.State != string(datamodel.JobsStateERROR) {
			logger.Infof("Delete job already in progress for %s %s, returning existing job UUID: %s", resourceType, resourceUUID, existingDeleteJob.UUID)
			return existingDeleteJob.UUID, false, nil
		}
	}

	createJob, err := se.GetJobByResourceUUID(ctx, resourceUUID, string(createJobType))
	if err != nil || createJob == nil {
		logger.Warnf("Create job not found for %s %s in CREATING state", resourceType, resourceUUID)
		return "", false, customerrors.NewConflictErr(fmt.Sprintf("Error deleting %s - %s is already transitioning between states", resourceType, resourceType))
	}

	if createJob.CorrelationID != correlationID {
		logger.Warnf("Correlation ID mismatch: create job correlation ID %s does not match delete request correlation ID %s", createJob.CorrelationID, correlationID)
		return "", false, customerrors.NewConflictErr(fmt.Sprintf("Error deleting %s - %s is already transitioning between states", resourceType, resourceType))
	}

	return "", true, nil
}

// GetActiveJobForResource checks if there is any job in an active (non-terminal) state
// for the given resource UUID and job type. Returns the UUID of the active job if found.
// This prevents duplicate job creation during retry scenarios. The lookup error is returned
// so callers do not create a duplicate job when the job state could not be determined.
func GetActiveJobForResource(ctx context.Context, se Storage, resourceUUID string, jobType datamodel.JobType, logger log.Logger) (activeJobUUID string, err error) {
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("resource_uuid", "=", resourceUUID),
		dbutils.NewFilterCondition("type", "=", string(jobType)),
		dbutils.NewFilterCondition("state", "IN", []string{
			string(datamodel.JobsStateNEW),
			string(datamodel.JobsStatePROCESSING),
			string(datamodel.JobsStateWaitForTemporal),
		}),
	)
	jobs, err := se.GetJobsWithCondition(ctx, *filter)
	if err != nil {
		logger.Errorf("Failed to look up active %s job for resource %s: %v", jobType, resourceUUID, err)
		return "", err
	}
	if len(jobs) > 0 {
		logger.Infof("Active job found for resource %s: job UUID %s (state: %s)", resourceUUID, jobs[0].UUID, jobs[0].State)
		return jobs[0].UUID, nil
	}
	return "", nil
}
