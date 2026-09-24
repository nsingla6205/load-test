package backgroundworkflows

import (
	"fmt"
	"time"

	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	certificateMaintenanceTrackingTimeout      = 55 * time.Minute
	certificateMaintenanceWaitHeartbeatTimeout = 30 * time.Second
	certificateMaintenanceWaitTimeoutBuffer    = time.Minute
)

func certificateMaintenanceWaitActivityOptions(timeout time.Duration) workflow.ActivityOptions {
	if timeout <= 0 {
		timeout = certificateMaintenanceTrackingTimeout
	}
	return workflow.ActivityOptions{
		StartToCloseTimeout: timeout + certificateMaintenanceWaitTimeoutBuffer,
		HeartbeatTimeout:    certificateMaintenanceWaitHeartbeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        time.Second,
			BackoffCoefficient:     2,
			MaximumInterval:        time.Minute,
			MaximumAttempts:        2,
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	}
}

func updateCertificateMaintenanceJob(
	ctx workflow.Context,
	jobID string,
	state datamodel.JobState,
	jobErr error,
) error {
	errorDetails := ""
	if jobErr != nil {
		errorDetails = jobErr.Error()
	}
	return workflow.ExecuteActivity(ctx, (&activities.CommonActivities{}).UpdateJobStatus, &datamodel.Job{
		BaseModel:    datamodel.BaseModel{UUID: jobID},
		State:        string(state),
		ErrorDetails: errorDetails,
	}).Get(ctx, nil)
}

func createCertificateMaintenanceChildJob(
	ctx workflow.Context,
	parentJobID, poolUUID, workflowID string,
	jobType datamodel.JobType,
	operation coremodels.CertificateMaintenanceOperation,
) (string, error) {
	job := &datamodel.Job{
		Type:         string(jobType),
		State:        string(datamodel.JobsStateNEW),
		IsAdminJob:   true,
		ResourceUUID: poolUUID,
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: poolUUID,
			PoolUUID:     poolUUID,
			ParentJobID:  parentJobID,
			Operation:    string(operation),
		},
	}
	var created *datamodel.Job
	if err := workflow.ExecuteActivity(
		ctx,
		(&activities.CommonActivities{}).CreateJobWithWorkflowID,
		job,
		workflowID,
	).Get(ctx, &created); err != nil {
		return "", fmt.Errorf("create certificate maintenance child job for pool %s: %w", poolUUID, err)
	}
	if created == nil || created.UUID == "" {
		return "", fmt.Errorf("create certificate maintenance child job for pool %s returned no job ID", poolUUID)
	}
	return created.UUID, nil
}

func waitForCertificateMaintenanceJobs(
	ctx workflow.Context,
	childJobIDs []string,
	timeout time.Duration,
) error {
	if len(childJobIDs) == 0 {
		return nil
	}
	ctx = workflow.WithActivityOptions(ctx, certificateMaintenanceWaitActivityOptions(timeout))
	return workflow.ExecuteActivity(
		ctx,
		(&activities.CommonActivities{}).WaitForJobs,
		childJobIDs,
		timeout,
	).Get(ctx, nil)
}

func finishCertificateMaintenanceJob(
	ctx workflow.Context,
	jobID string,
	runErr error,
) error {
	state := datamodel.JobsStateDONE
	if runErr != nil {
		state = datamodel.JobsStateERROR
	}
	if updateErr := updateCertificateMaintenanceJob(ctx, jobID, state, runErr); updateErr != nil {
		if runErr != nil {
			return fmt.Errorf("%w; update parent job status: %v", runErr, updateErr)
		}
		return fmt.Errorf("update parent job status: %w", updateErr)
	}
	return runErr
}
