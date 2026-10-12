package workflows

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	WorkflowStatusCreated   = "CREATED"
	WorkflowStatusRunning   = "RUNNING"
	WorkflowStatusCompleted = "COMPLETED"
	WorkflowStatusFailed    = "FAILED"
	WorkflowStatusCancelled = "CANCELLED"
	WorkflowStatusTimeout   = "TIMEOUT"
	WorkflowStatusRetry     = "RETRY"
	WorkflowStatusPaused    = "PAUSED"
	WorkflowStatusResumed   = "RESUMED"
	WorkflowStatusAborted   = "ABORTED"
	WorkflowStatusPending   = "PENDING"

	StatusQueryName            = "status"
	RestoreStartToCloseTimeout = 6 * 24 * time.Hour // 6 days
	pollDBJobWaitTimeSecond    = 30

	RbacActivityStartToClose   = 3 * time.Minute
	RbacActivityInitialBackoff = 5 * time.Second
	RbacActivityMaxInterval    = 2 * time.Minute
	RbacActivityMaxAttempts    = 3
	initialPollInterval        = 5 * time.Second
	maxPollInterval            = 15 * time.Minute
)

var (
	StartToCloseTimeout                               = env.GetString("START_TO_CLOSE_WORKFLOW_TIMEOUT", "55m")
	StartToCloseTimeoutLV                             = env.GetString("START_TO_CLOSE_WORKFLOW_TIMEOUT_LV", "60m")
	StartToCloseTimeoutCmekBackupRotate               = env.GetString("START_TO_CLOSE_WORKFLOW_TIMEOUT_CMEK_BACKUP_ROTATE", "8640m")
	StartToCloseTimeoutForReplicationActivities       = env.GetInt("START_TO_CLOSE_TIMEOUT_FOR_REPLICATION_ACTIVITIES", 300)
	StartToCloseTimeoutForReplicationCreateActivities = env.GetInt("START_TO_CLOSE_TIMEOUT_FOR_REPLICATION_CREATE_ACTIVITIES", 600)
	StartToCloseTimeoutDataSubnetCreate               = env.GetString("START_TO_CLOSE_WORKFLOW_TIMEOUT_DATA_SUBNET_CREATE", "20m")
	StartToCloseTimeoutDataSubnetDelete               = env.GetString("START_TO_CLOSE_WORKFLOW_TIMEOUT_DATA_SUBNET_DELETE", "5m")
	StartToCloseTimeoutDataSubnetActivities           = env.GetString("START_TO_CLOSE_TIMEOUT_DATA_SUBNET_ACTIVITIES", "5m")
	StartToCloseTimeoutForHyperscaler                 = env.GetString("START_TO_CLOSE_TIMEOUT_FOR_HYPERSCALER", "5m")
	BackoffCoefficientForReplicationActivities        = env.GetFloat64("BACKOFF_COEFFICIENT_FOR_REPLICATION_ACTIVITIES", 1.5)
	StartToCloseTimeoutUpgrade                        = env.GetString("START_TO_CLOSE_WORKFLOW_TIMEOUT_UPGRADE", "300m")
	RetryInterval                                     = env.GetString("RETRY_INTERVAL", "5s")
	RetryMaxAttempts                                  = env.GetInt("RETRY_MAX_ATTEMPTS", 3)
	RetryMaxInterval                                  = env.GetString("RETRY_MAX_INTERVAL", "5m")
	RetryBackoff                                      = env.GetString("RETRY_BACKOFF_COEFFICIENT", "2.0")
	ActivityHeartBeatTimeout                          = env.GetString("POOL_ACTIVITY_HEARTBEAT_TIMEOUT", "5m")
	maxRetryAttemptsForSDEPollJob                     = env.GetInt("MAX_RETRY_ATTEMPTS_FOR_SDE_POLL_JOB", 20)
	StartToCloseTimeoutUpgradeLV                      = env.GetString("START_TO_CLOSE_WORKFLOW_TIMEOUT_UPGRADE_LV", "360m")

	// Service Account specific retry policy configurations
	SARetryStartToCloseTimeout = env.GetString("SA_RETRY_START_TO_CLOSE_TIMEOUT", "15m")
	SARetryInitialInterval     = env.GetString("SA_RETRY_INITIAL_INTERVAL", "10s")
	SARetryMaximumAttempts     = env.GetInt("SA_RETRY_MAXIMUM_ATTEMPTS", 12)
	SARetryMaximumInterval     = env.GetString("SA_RETRY_MAXIMUM_INTERVAL", "60s")
	SARetryBackoffCoefficient  = env.GetString("SA_RETRY_BACKOFF_COEFFICIENT", "2.0")

	// Active Directory specific retry policy configurations
	AdScheduleToCloseTimeout       = env.GetString("AD_SCHEDULE_TO_CLOSE_WORKFLOW_TIMEOUT", "35m")
	adSyncHeartBeatTimeout         = env.GetString("AD_SYNC_HEARTBEAT_TIMEOUT", "2m")
	adSyncRetryStartToCloseTimeout = env.GetString("AD_SYNC_RETRY_START_TO_CLOSE_TIMEOUT", "10m")
	adSyncRetryInitialInterval     = env.GetString("AD_SYNC_RETRY_INITIAL_INTERVAL", "10s")
	adSyncRetryBackoffCoefficient  = env.GetString("AD_SYNC_RETRY_BACKOFF_COEFFICIENT", "2.0")
	adSyncRetryMaxInterval         = env.GetString("AD_SYNC_RETRY_MAX_INTERVAL", "60s")
	adSyncRetryMaxAttempts         = env.GetInt("AD_SYNC_RETRY_MAX_ATTEMPTS", 10)

	// Timeout for when transfer is stuck (no progress in bytes transferred)
	snapmirrorTransferStuckTimeout = time.Duration(env.GetUint64("SNAPMIRROR_TRANSFER_STUCK_TIMEOUT_MINUTES", 10)) * time.Minute // Default 10 minutes

	kmsLockHeartbeatInterval = 5 * time.Second

	executeActivity = workflow.ExecuteActivity
)

// ConvertToVSAError converts a regular error to *vsaerrors.CustomError.
// If the error is already a *vsaerrors.CustomError, it returns it as is.
// Otherwise, it wraps it in a generic workflow error.
// NOTE: This should NOT be used in child workflows when returning errors.
// Use WrapErrorForChildWorkflow instead to preserve tracking IDs.
func ConvertToVSAError(err error) *vsaerrors.CustomError {
	if err == nil {
		return nil
	}
	return vsaerrors.ExtractCustomError(err)
}

// WrapErrorForChildWorkflow extracts a CustomError from the given error and wraps it
// as a Temporal ApplicationError. This should be used when returning errors from
// child workflows to ensure the tracking ID is preserved across the workflow boundary.
//
// When a child workflow returns a *CustomError directly, Temporal serializes it
// without preserving the "CustomError" type. The parent workflow then cannot
// extract the tracking ID, falling back to ErrInternalServerError (1011).
//
// By wrapping as ApplicationError with CustomErrorType, the tracking ID survives
// the serialization and can be properly extracted by the parent workflow.
func WrapErrorForChildWorkflow(err error) error {
	if err == nil {
		return nil
	}
	customErr := vsaerrors.ExtractCustomError(err)
	return vsaerrors.WrapAsTemporalApplicationError(customErr)
}

type WorkflowRetryPolicy struct {
	InitialInterval     time.Duration
	BackoffCoefficient  float64
	MaximumInterval     time.Duration
	MaximumAttempts     int
	StartToCloseTimeout time.Duration
	HeartBeatTimeout    time.Duration
}

// WorkflowInterface defines the common methods for all workflows.
type WorkflowInterface interface {
	Setup(ctx workflow.Context, input interface{}) error
	Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError)
	UpdateJobStatus(ctx workflow.Context, status string, err error) error
	EnsureJobState(ctx workflow.Context, expected datamodel.JobState) error
}

// BaseWorkflow provides common functionalities for all workflows.
type BaseWorkflow struct {
	ID         string
	Status     string
	CustomerID string
	Logger     log.Logger
}

type WorkflowStatus struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	CustomerID string `json:"customer_id"`
}

func (bw *BaseWorkflow) GetDefaultActivityOptions(ctx workflow.Context) workflow.ActivityOptions {
	// Set default activity options
	return workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
}

func PopulateRetryPolicyParams(largeCapacity ...bool) (*WorkflowRetryPolicy, error) {
	// Determine if this is for a large capacity pool
	isLargeCapacity := false
	if len(largeCapacity) > 0 {
		isLargeCapacity = largeCapacity[0]
	}

	// Choose timeout based on pool type
	timeout := StartToCloseTimeout
	if isLargeCapacity {
		timeout = StartToCloseTimeoutLV
	}

	activityStartToCloseTimeout, err := time.ParseDuration(timeout)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	activityRetryInterval, err := time.ParseDuration(RetryInterval)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	activityRetryMaxAttempts := RetryMaxAttempts
	activityRetryMaxInterval, err := time.ParseDuration(RetryMaxInterval)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	activityRetryBackoff, err := strconv.ParseFloat(RetryBackoff, 64)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	activityHeartBeatTimeout, err := time.ParseDuration(ActivityHeartBeatTimeout)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	return &WorkflowRetryPolicy{
		InitialInterval:     activityRetryInterval,
		StartToCloseTimeout: activityStartToCloseTimeout,
		BackoffCoefficient:  activityRetryBackoff,
		MaximumInterval:     activityRetryMaxInterval,
		MaximumAttempts:     activityRetryMaxAttempts,
		HeartBeatTimeout:    activityHeartBeatTimeout,
	}, nil
}

// PopulateRotationRetryPolicyParams returns retry policy for certificate and password rotation activities
// This ensures all rotation activities can be retried once (MaximumAttempts = 2: 1 initial attempt + 1 retry)
// Activity timeout is set to 5 minutes
func PopulateRotationRetryPolicyParams(largeCapacity ...bool) (*WorkflowRetryPolicy, error) {
	// Activity timeout is fixed at 5 minutes for rotation activities
	activityStartToCloseTimeout := 5 * time.Minute

	activityRetryInterval, err := time.ParseDuration(RetryInterval)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	// Rotation activities should be retried once (1 initial attempt + 1 retry = 2 total attempts)
	activityRetryMaxAttempts := 2
	activityRetryMaxInterval, err := time.ParseDuration(RetryMaxInterval)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	activityRetryBackoff, err := strconv.ParseFloat(RetryBackoff, 64)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	return &WorkflowRetryPolicy{
		InitialInterval:     activityRetryInterval,
		StartToCloseTimeout: activityStartToCloseTimeout,
		BackoffCoefficient:  activityRetryBackoff,
		MaximumInterval:     activityRetryMaxInterval,
		MaximumAttempts:     activityRetryMaxAttempts,
	}, nil
}

// PopulateADSyncRetryPolicyParams returns retry policy specific to AD Sync operations
// with custom values: backoff coefficient 2.0, max attempts 10, max interval 60 seconds
// to ensure AD Sync Activities run for ~10 min
func PopulateADSyncRetryPolicyParams() *WorkflowRetryPolicy {
	activityStartToCloseTimeout, err := time.ParseDuration(adSyncRetryStartToCloseTimeout)
	if err != nil {
		activityStartToCloseTimeout = 10 * time.Minute
	}
	activityInitialInterval, err := time.ParseDuration(adSyncRetryInitialInterval)
	if err != nil {
		activityInitialInterval = 10 * time.Second
	}
	activityRetryMaxInterval, err := time.ParseDuration(adSyncRetryMaxInterval)
	if err != nil {
		activityRetryMaxInterval = 60 * time.Second
	}
	activityRetryBackoff, err := strconv.ParseFloat(adSyncRetryBackoffCoefficient, 64)
	if err != nil {
		activityRetryBackoff = 2.0
	}
	activityHeartBeatTimeout, err := time.ParseDuration(adSyncHeartBeatTimeout)
	if err != nil {
		activityHeartBeatTimeout = 2 * time.Minute
	}
	return &WorkflowRetryPolicy{
		HeartBeatTimeout:    activityHeartBeatTimeout,
		InitialInterval:     activityInitialInterval,
		StartToCloseTimeout: activityStartToCloseTimeout,
		BackoffCoefficient:  activityRetryBackoff,
		MaximumInterval:     activityRetryMaxInterval,
		MaximumAttempts:     adSyncRetryMaxAttempts,
	}
}

// populateServiceAccountRetryPolicyParams returns retry policy specific to service account operations
// with custom values: backoff coefficient 2.0, max attempts 12, max interval 60 seconds
func populateServiceAccountRetryPolicyParams() (*WorkflowRetryPolicy, error) {
	activityStartToCloseTimeout, err := time.ParseDuration(SARetryStartToCloseTimeout)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	activityRetryInterval, err := time.ParseDuration(SARetryInitialInterval)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	activityRetryMaxAttempts := SARetryMaximumAttempts
	activityRetryMaxInterval, err := time.ParseDuration(SARetryMaximumInterval)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	activityRetryBackoff, err := strconv.ParseFloat(SARetryBackoffCoefficient, 64)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	return &WorkflowRetryPolicy{
		InitialInterval:     activityRetryInterval,
		StartToCloseTimeout: activityStartToCloseTimeout,
		BackoffCoefficient:  activityRetryBackoff,
		MaximumInterval:     activityRetryMaxInterval,
		MaximumAttempts:     activityRetryMaxAttempts,
	}, nil
}

// withServiceAccountActivityOptions returns a child context with retry policy tuned for GCP IAM operations.
func withServiceAccountActivityOptions(ctx workflow.Context) (workflow.Context, error) {
	saRetryPolicy, err := populateServiceAccountRetryPolicyParams()
	if err != nil {
		return nil, err
	}
	saAO := workflow.ActivityOptions{
		StartToCloseTimeout: saRetryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        saRetryPolicy.InitialInterval,
			BackoffCoefficient:     saRetryPolicy.BackoffCoefficient,
			MaximumInterval:        saRetryPolicy.MaximumInterval,
			MaximumAttempts:        int32(saRetryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	return workflow.WithActivityOptions(ctx, saAO), nil
}

func (bw *BaseWorkflow) UpdateJobStatus(ctx workflow.Context, status string, err error) error {
	if bw.ID == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError,
			errors.New("job uuid cannot be empty"))
	}

	updatedJob := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: bw.ID},
		State:     status,
	}
	if err != nil {
		var customError *vsaerrors.CustomError
		if vsaerrors.As(err, &customError) {
			updatedJob.TrackingID = customError.TrackingID
			updatedJob.ErrorDetails = customError.OriginalErr.Error()
		} else {
			// If the error is not a custom error, we set the tracking ID to 0 and the error details to the error message.
			// This is required so that generic errors that are not of type CustomError do not get lost.
			updatedJob.TrackingID = 0
			updatedJob.ErrorDetails = err.Error()
		}
	}

	commonActivity := activities.CommonActivities{}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})
	return executeActivity(ctx, commonActivity.UpdateJobStatus, updatedJob).Get(ctx, nil)
}

func (bw *BaseWorkflow) EnsureJobState(ctx workflow.Context, expected datamodel.JobState) error {
	if bw.ID == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError,
			errors.New("job uuid cannot be empty"))
	}

	commonActivity := activities.CommonActivities{}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})

	var job *datamodel.Job
	if err := executeActivity(ctx, commonActivity.GetJob, bw.ID).Get(ctx, &job); err != nil {
		return err
	}

	if job == nil {
		return vsaerrors.NewVCPError(
			vsaerrors.ErrDatabaseDataNotFoundError,
			fmt.Errorf("job %s not found", bw.ID),
		)
	}

	if job.State != string(expected) {
		return vsaerrors.NewVCPError(
			vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("job %s is in state %s; expected %s", bw.ID, job.State, expected),
		)
	}

	return nil
}

var QueryWorkflowStatus = _queryWorkflowStatus

// QueryWorkflowStatus queries the status of a workflow using its ID and run ID.
func _queryWorkflowStatus(ctx context.Context, tempClient client.Client, workflowID, runID string) (*WorkflowStatus, error) {
	var status WorkflowStatus
	encVal, err := tempClient.QueryWorkflow(ctx, workflowID, runID, StatusQueryName)
	if err != nil {
		return nil, err
	}
	err = encVal.Get(&status)
	if err != nil {
		return nil, err
	}

	return &status, nil
}

func getSnapshotPolicyName(volume *datamodel.Volume) string {
	if volume != nil && volume.SnapshotPolicy != nil && volume.SnapshotPolicy.Name != "" {
		return volume.SnapshotPolicy.Name
	}
	return ""
}

// WaitForONTAPJob waits for an ONTAP job to complete, taking a workflow context, job UUID, node, and timeout as input.
func WaitForONTAPJob(ctx workflow.Context, jobResponse *vsa.OntapAsyncResponse, node *models.Node, timeout time.Duration) error {
	if jobResponse == nil || jobResponse.JobUUID == "" {
		return nil
	}
	var job *vsa.OntapJob
	startTime := time.Now()

	for {
		// Check if the timeout has been reached
		if time.Since(startTime) > timeout {
			return fmt.Errorf("ontap job %s timed out after %v", jobResponse.JobUUID, timeout)
		}

		// Execute the activity to get the ONTAP job status
		err := workflow.ExecuteActivity(ctx, activities.CommonActivities.GetOntapJob, jobResponse.JobUUID, node).Get(ctx, &job)
		if err != nil {
			return err
		}

		// Check the job state
		switch job.State {
		case "failure":
			if job.Error != nil {
				return errors.New(job.Error.Message)
			}
			return fmt.Errorf("ontap job %s failed with no error message", job.UUID)
		case "success":
			return nil
		}

		// Sleep for a short duration before checking again
		err = workflow.Sleep(ctx, 5*time.Second)
		if err != nil {
			return fmt.Errorf("failed to sleep while waiting for ONTAP job %s: %w", jobResponse.JobUUID, err)
		}
	}
}

// PollOnDBJob waits for a db job to complete, taking a workflow context, job UUID, and timeout as input.
func PollOnDBJob(ctx workflow.Context, jobUUID string, timeout time.Duration) error {
	startTime := workflow.Now(ctx)
	commonActivity := activities.CommonActivities{}

	for {
		// Check if the timeout has been reached.
		if workflow.Now(ctx).Sub(startTime) > timeout {
			return fmt.Errorf("db job %s timed out after %v", jobUUID, timeout)
		}

		// Execute GetJob as an activity to fetch the latest job state.
		var job *datamodel.Job
		err := executeActivity(ctx, commonActivity.GetJob, jobUUID).Get(ctx, &job)
		if err != nil {
			return fmt.Errorf("failed to get db job status for %s: %w", jobUUID, err)
		}

		// Check the job state.
		if job.State == string(datamodel.JobsStateDONE) {
			if job.ErrorDetails != "" {
				return errors.New("job completed with error: " + job.ErrorDetails)
			}
			return nil
		}

		if job.State == string(datamodel.JobsStateERROR) {
			return vsaerrors.NewVCPError(job.TrackingID, errors.New(job.ErrorDetails))
		}

		// Sleep for a some duration before checking again.
		err = workflow.Sleep(ctx, pollDBJobWaitTimeSecond*time.Second)
		if err != nil {
			return fmt.Errorf("failed to sleep while waiting for db job %s: %w", jobUUID, err)
		}
	}
}

// FetchAndSetAuthToken fetches a JWT token for the given account and sets it in the workflow context.
func FetchAndSetAuthToken(ctx workflow.Context, accountName string, logger log.Logger) (workflow.Context, error) {
	if env.IsLocalEnv() {
		return ctx, nil
	}

	var token string
	err := workflow.ExecuteActivity(ctx, activities.CommonActivities.GetAuthJWTToken, accountName).Get(ctx, &token)
	if err != nil {
		if logger != nil {
			logger.Errorf("Failed to get token for account %s: %v", accountName, err)
		} else {
			workflow.GetLogger(ctx).Error("Failed to get token for account", "account", accountName, "error", err)
		}
		return ctx, err
	}
	return workflow.WithValue(ctx, middleware.AuthorizationToken, token), nil
}

func PollTransferStatusWithContinueAsNewCommon(ctx workflow.Context, backupActivitiesContext *activities.BackupActivitiesContext, continueAsNewFunc interface{}, continueAsNewArgs ...interface{}) error {
	backupActivity := &activities.BackupActivity{}

	// Set up activity options with retry policy
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	done := false
	waitTime := initialPollInterval // Start with 5 seconds
	pollCount := 0
	logger := util.GetLogger(ctx)

	// Track transfer progress for stuck detection
	var lastBytesTransferred *int64
	var lastProgressTime time.Time         // Time when we last saw progress (zero value means not set)
	transferStartTime := workflow.Now(ctx) // Track overall transfer start time for fallback timeout

	for !done {
		pollCount++

		// Get current event history count
		info := workflow.GetInfo(ctx)
		eventHistoryCount := info.GetCurrentHistoryLength()

		// Create input for the polling activity
		pollInput := &activities.PollTransferStatusInput{
			BackupActivitiesContext: backupActivitiesContext,
			Node:                    backupActivitiesContext.Node,
			SnapmirrorRelationship:  backupActivitiesContext.SnapmirrorRelationship,
			SnapshotName:            backupActivitiesContext.SnapshotName, // This will be empty for restore, which is fine
			EventHistoryCount:       eventHistoryCount,
			NextWaitTime:            waitTime,
		}

		currentTime := workflow.Now(ctx)

		// Execute the polling activity
		var pollOutput *activities.PollTransferStatusOutput
		err = workflow.ExecuteActivity(ctx, backupActivity.PollTransferStatusWithHistoryCheckActivity, pollInput, currentTime).Get(ctx, &pollOutput)
		if err != nil {
			return err
		}

		// Update context with the result
		backupActivitiesContext = pollOutput.BackupActivitiesContext

		// Check if we should continue as new
		if pollOutput.ShouldContinueAsNew {
			logger.Info("Triggering ContinueAsNew due to event history limits",
				"reason", pollOutput.ContinueAsNewReason,
				"eventHistoryCount", eventHistoryCount,
				"pollCount", pollCount)
			return workflow.NewContinueAsNewError(ctx, continueAsNewFunc, continueAsNewArgs...)
		}

		// Check if transfer is complete first (skip progress tracking if already complete)
		if pollOutput.TransferComplete {
			done = true
			logger.Info("Transfer completed successfully", "snapshotName", backupActivitiesContext.SnapshotName)
		} else {
			// Log current transfer state for debugging
			var transferStatus string
			var bytesTransferred *int64
			if pollOutput.TransferStatus != nil {
				transferStatus = pollOutput.TransferStatus.Status
				bytesTransferred = pollOutput.TransferStatus.BytesTransferred
			}
			logger.Info("Transfer in progress",
				"pollCount", pollCount,
				"status", transferStatus,
				"bytesTransferred", bytesTransferred,
				"timeSinceStart", workflow.Now(ctx).Sub(transferStartTime))

			// Transfer still in progress - check for stuck transfer via bytes progress tracking
			// Only check progress if transfer status indicates it's still transferring
			if pollOutput.TransferStatus != nil && pollOutput.TransferStatus.Status == activities.SmStatusTransferring {
				if pollOutput.TransferStatus.BytesTransferred != nil {
					// We have bytes transferred - check if transfer is making progress
					currentBytes := pollOutput.TransferStatus.BytesTransferred
					if lastBytesTransferred == nil || *currentBytes != *lastBytesTransferred {
						// Transfer is making progress (first time seeing bytes or bytes changed) - reset the stuck timeout
						// Any change (increase or decrease) is treated as progress to reset the timeout
						if lastBytesTransferred != nil {
							logger.Info("Transfer progress", "bytesDelta", *currentBytes-*lastBytesTransferred)
						}
						lastBytesTransferred = currentBytes
						lastProgressTime = workflow.Now(ctx)
					} else {
						// Transfer size hasn't changed - check if it's stuck
						// lastProgressTime should already be set from when we first saw this value
						logger.Debug("Bytes transferred unchanged", "bytes", *currentBytes)
						if !lastProgressTime.IsZero() {
							// Check if transfer has been stuck for too long
							timeSinceLastProgress := workflow.Now(ctx).Sub(lastProgressTime)
							if timeSinceLastProgress > snapmirrorTransferStuckTimeout {
								logger.Errorf("Transfer stuck - no progress for %v (bytes: %d). Continuing to poll...", timeSinceLastProgress, *currentBytes)
							} else {
								logger.Info("No bytes progress", "timeSinceLastProgress", timeSinceLastProgress, "stuckTimeout", snapmirrorTransferStuckTimeout)
							}
						} else {
							// First time we see this value, set the progress time
							lastProgressTime = workflow.Now(ctx)
							logger.Debug("Initialized progress tracking", "bytes", *currentBytes)
						}
					}
				} else {
					// BytesTransferred not available during active transfer
					// Initialize progress tracking on first nil encounter
					if lastProgressTime.IsZero() {
						lastProgressTime = workflow.Now(ctx)
						logger.Debug("BytesTransferred is nil, starting stuck timeout tracking")
					} else {
						// Check if we've been stuck with nil bytes for too long
						timeSinceNil := workflow.Now(ctx).Sub(lastProgressTime)
						if timeSinceNil > snapmirrorTransferStuckTimeout {
							logger.Errorf("Transfer stuck - BytesTransferred remains nil for %v. Continuing to poll...", timeSinceNil)
						} else {
							logger.Info("BytesTransferred still nil", "timeSinceNil", timeSinceNil, "stuckTimeout", snapmirrorTransferStuckTimeout)
						}
					}

					// Also apply global fallback timeout
					timeSinceTransferStart := workflow.Now(ctx).Sub(transferStartTime)
					globalFallbackTimeout := snapmirrorTransferStuckTimeout * 6 // 60 minutes by default
					if timeSinceTransferStart > globalFallbackTimeout {
						logger.Errorf("Transfer exceeded fallback timeout: %v. Continuing to poll...", timeSinceTransferStart)
					} else {
						logger.Debug("BytesTransferred not available during active transfer, relying on global timeout",
							"timeSinceStart", timeSinceTransferStart,
							"globalFallbackTimeout", globalFallbackTimeout)
					}
				}
			} else {
				// Transfer status is not "transferring" (e.g., "queued", "idle", nil, etc.)
				// Apply global fallback timeout to prevent infinite loops in unexpected states
				timeSinceTransferStart := workflow.Now(ctx).Sub(transferStartTime)
				globalFallbackTimeout := snapmirrorTransferStuckTimeout * 6 // 60 minutes by default
				var statusStr string
				if pollOutput.TransferStatus != nil {
					statusStr = pollOutput.TransferStatus.Status
				}
				if timeSinceTransferStart > globalFallbackTimeout {
					logger.Errorf("Transfer in unexpected state '%v' for %v. Continuing to poll...", statusStr, timeSinceTransferStart)
				} else {
					logger.Debug("Transfer in non-transferring state, monitoring with fallback timeout",
						"status", statusStr,
						"timeSinceStart", timeSinceTransferStart,
						"timeoutLimit", globalFallbackTimeout)
				}
			}

			// Transfer still in progress, sleep with exponential backoff
			err = workflow.Sleep(ctx, waitTime)
			if err != nil {
				return fmt.Errorf("failed to sleep during snapmirror transfer polling: %w", err)
			}

			// Exponential backoff: double the wait time, but cap it at maxWaitTime
			waitTime = time.Duration(float64(waitTime) * 2)
			if waitTime > maxPollInterval { // Cap at 15 minutes
				waitTime = maxPollInterval
			}
		}
	}

	return nil
}

// WithKmsRotationLock acquires the KMS rotation lock at service-account scope, starts a heartbeat goroutine that renews the lease
// every kmsLockHeartbeatInterval, and returns a release function. The lock is ALWAYS keyed by the service-account UUID so
// rotation, pool KMS configure, and EKM config migrate serialize even when multiple KMS configs share one SA;  callers with only a KMS config UUID
// must use WithKmsRotationLockForKmsConfig.  Call defer release() (idempotent) or release() explicitly before continuing; defer covers error paths. Workflow code only.
func WithKmsRotationLock(ctx workflow.Context, activity *backgroundactivities.RotateKmsSAKeyActivity, serviceAccountUUID string) (lockClientID string, release func(), err error) {
	logger := util.GetLogger(ctx)
	err = workflow.ExecuteActivity(ctx, activity.AcquireKmsRotationLockActivity, serviceAccountUUID).Get(ctx, &lockClientID)
	if err != nil {
		return "", nil, err
	}

	heartbeatCtx, cancelHeartbeat := workflow.WithCancel(ctx)
	workflow.Go(heartbeatCtx, func(hctx workflow.Context) {
		for {
			if sleepErr := workflow.Sleep(hctx, kmsLockHeartbeatInterval); sleepErr != nil {
				return
			}
			if hctx.Err() != nil {
				return
			}
			renewErr := workflow.ExecuteActivity(hctx, activity.RenewKmsRotationLockActivity, serviceAccountUUID, lockClientID).Get(hctx, nil)
			if renewErr != nil {
				logger.Warn("RenewKmsRotationLockActivity failed (non-fatal)", "serviceAccountUUID", serviceAccountUUID, "lockClientID", lockClientID, "error", renewErr)
			}
		}
	})

	released := false
	release = func() {
		if released {
			return
		}
		released = true
		cancelHeartbeat()
		releaseCtx, releaseCancel := workflow.NewDisconnectedContext(ctx)
		defer releaseCancel()
		if releaseErr := workflow.ExecuteActivity(releaseCtx, activity.ReleaseKmsRotationLockActivity, serviceAccountUUID, lockClientID).Get(releaseCtx, nil); releaseErr != nil {
			logger.Warn("ReleaseKmsRotationLockActivity failed (non-fatal)", "serviceAccountUUID", serviceAccountUUID, "lockClientID", lockClientID, "error", releaseErr)
		}
	}
	return lockClientID, release, nil
}

// WithKmsRotationLockForKmsConfig resolves the owning service account for kmsConfigUUID and acquires the lock via WithKmsRotationLock, so configure/migrate call sites contend on the same SA-scoped lock as scheduled rotation. Workflow code only.
func WithKmsRotationLockForKmsConfig(ctx workflow.Context, activity *backgroundactivities.RotateKmsSAKeyActivity, kmsConfigUUID string) (lockClientID string, release func(), err error) {
	var serviceAccountUUID string
	err = workflow.ExecuteActivity(ctx, activity.GetServiceAccountUUIDForKmsConfigActivity, kmsConfigUUID).Get(ctx, &serviceAccountUUID)
	if err != nil {
		return "", nil, err
	}
	return WithKmsRotationLock(ctx, activity, serviceAccountUUID)
}
