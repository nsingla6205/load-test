package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultUpgradeJobPickupTimeout      = 60 * time.Second
	defaultUpgradeJobRunningGracePeriod = 15 * time.Minute
	upgradePickupTimeoutErrorCode       = "UPGRADE_PICKUP_TIMEOUT"
	upgradePickupTimeoutErrorType       = "UPGRADE_TIMEOUT"
)

type upgradeJobSupervisorConfig struct {
	PickupTimeout time.Duration
	// RunningGracePeriod bounds how long a workflow that Temporal still reports
	// as RUNNING may leave its job PENDING. It is measured from the Temporal
	// execution start time, not from job creation, and is deliberately much
	// longer than PickupTimeout: a running workflow is claiming the job, so
	// worker backlog or activity retries must not look like an abandoned job.
	RunningGracePeriod time.Duration
	DescribeTimeout    time.Duration
	TerminateTimeout   time.Duration
}

type upgradeJobPickupSupervisor struct {
	storage  database.Storage
	temporal client.Client
	config   upgradeJobSupervisorConfig
}

func defaultUpgradeJobSupervisorConfig() upgradeJobSupervisorConfig {
	return upgradeJobSupervisorConfig{
		PickupTimeout:      env.GetDuration("UPGRADE_JOB_PICKUP_TIMEOUT", defaultUpgradeJobPickupTimeout),
		RunningGracePeriod: env.GetDuration("UPGRADE_JOB_RUNNING_GRACE_PERIOD", defaultUpgradeJobRunningGracePeriod),
		DescribeTimeout:    temporalDescribeTimeout,
		TerminateTimeout:   temporalTerminateTimeout,
	}
}

func (s *upgradeJobPickupSupervisor) scan(ctx context.Context) {
	logger := util.GetLogger(ctx)
	if s.storage == nil || s.temporal == nil {
		logger.Error("upgrade-job-supervisor: storage and Temporal client are required")
		return
	}
	if s.config.PickupTimeout <= 0 {
		logger.Warn("upgrade-job-supervisor: pickup timeout is disabled")
		return
	}

	cutoff := time.Now().UTC().Add(-s.config.PickupTimeout)
	jobs, err := s.storage.ListPendingUpgradeJobsCreatedBefore(ctx, cutoff)
	if err != nil {
		logger.Error("upgrade-job-supervisor: failed to list stale pending upgrade jobs", "error", err)
		return
	}

	for i := range jobs {
		s.process(ctx, jobs[i], cutoff)
	}
}

func (s *upgradeJobPickupSupervisor) process(ctx context.Context, job database.PendingUpgradeJob, cutoff time.Time) {
	workflowID := s.workflowIDFor(job)
	logger := util.GetLogger(ctx).With(log.Fields{
		"upgradeJobID":   job.UUID,
		"upgradeJobKind": string(job.Kind),
		"workflowID":     workflowID,
	})

	terminateWorkflow, proceed := s.shouldFailPendingJob(ctx, job, workflowID, logger)
	if !proceed {
		return
	}

	if err := s.markPendingJobFailed(ctx, job, cutoff); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Debug("upgrade-job-supervisor: job started or changed state before timeout cleanup")
			return
		}
		logger.Error("upgrade-job-supervisor: failed to clean up stale pending upgrade job", "error", err)
		return
	}

	// Terminate outside the DB transaction so row locks are not held across the RPC.
	if err := s.terminateWorkflowIfNeeded(ctx, workflowID, terminateWorkflow); err != nil {
		logger.Warn("upgrade-job-supervisor: job marked FAILED but Temporal terminate failed", "error", err)
	}

	logger.Warn("upgrade-job-supervisor: stale pending upgrade job marked FAILED")
}

// workflowIDFor returns the Temporal execution that owns the job. Top-level jobs
// use the job UUID as their workflow ID; a batch child is executed under the
// batch child workflow ID instead.
func (s *upgradeJobPickupSupervisor) workflowIDFor(job database.PendingUpgradeJob) string {
	if job.Kind == database.UpgradeJobKindBatchChild {
		return workflows.BatchChildUpgradeWorkflowID(job.BatchUpgradeID, job.ClusterID)
	}
	return job.UUID
}

// shouldFailPendingJob decides whether a job that is still PENDING past the
// pickup timeout should be failed, and whether its workflow needs terminating.
// A workflow that is still RUNNING owns the job, so it is only failed once it has
// been running past RunningGracePeriod without claiming; anything shorter would
// abort upgrades that are merely waiting on worker backlog or an activity retry.
func (s *upgradeJobPickupSupervisor) shouldFailPendingJob(
	ctx context.Context,
	job database.PendingUpgradeJob,
	workflowID string,
	logger log.Logger,
) (terminateWorkflow bool, proceed bool) {
	describeCtx, cancelDescribe := context.WithTimeout(ctx, s.config.DescribeTimeout)
	response, err := s.temporal.DescribeWorkflowExecution(describeCtx, workflowID, "")
	cancelDescribe()

	if err != nil {
		var notFound *serviceerror.NotFound
		if !errors.As(err, &notFound) {
			logger.Warn("upgrade-job-supervisor: unable to verify Temporal workflow; deferring cleanup", "error", err)
			return false, false
		}
		if job.Kind == database.UpgradeJobKindBatchChild && s.batchParentCanStillStartChild(ctx, job, logger) {
			logger.Info("upgrade-job-supervisor: batch child workflow has not started yet and its batch is still active; deferring cleanup")
			return false, false
		}
		logger.Warn("upgrade-job-supervisor: Temporal workflow is missing; failing stale pending job")
		return false, true
	}

	if response == nil || response.WorkflowExecutionInfo == nil {
		logger.Warn("upgrade-job-supervisor: Temporal describe response is incomplete; deferring cleanup")
		return false, false
	}

	status := response.WorkflowExecutionInfo.GetStatus()
	switch status {
	case enumspb.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED:
		logger.Warn("upgrade-job-supervisor: Temporal workflow status is unspecified; deferring cleanup")
		return false, false

	case enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING:
		if s.config.RunningGracePeriod <= 0 {
			logger.Debug("upgrade-job-supervisor: workflow is running and the running grace period is disabled; deferring cleanup")
			return false, false
		}
		startTime := response.WorkflowExecutionInfo.GetStartTime()
		if startTime == nil {
			logger.Warn("upgrade-job-supervisor: running workflow has no start time; deferring cleanup")
			return false, false
		}
		runningFor := time.Since(startTime.AsTime())
		if runningFor < s.config.RunningGracePeriod {
			logger.Info("upgrade-job-supervisor: workflow is running but has not claimed the job yet; deferring cleanup",
				"runningFor", runningFor, "runningGracePeriod", s.config.RunningGracePeriod)
			return false, false
		}
		logger.Warn("upgrade-job-supervisor: workflow has been running without claiming its job; failing and terminating",
			"runningFor", runningFor, "runningGracePeriod", s.config.RunningGracePeriod)
		return true, true

	default:
		// Any other status is terminal: the workflow ended without claiming the
		// job, so nothing will ever move it out of PENDING.
		logger.Warn("upgrade-job-supervisor: Temporal workflow closed without claiming its job; failing stale pending job",
			"workflowStatus", status.String())
		return false, true
	}
}

// batchParentCanStillStartChild reports whether the batch that owns a child row
// may yet start that child's workflow. PrepareBatchUpgradeWaveActivity commits
// the child rows before the parent calls ExecuteChildWorkflow, and that gap can
// outlive PickupTimeout when the wave is large or the activity is retried, so a
// missing child execution only means the row is abandoned once the parent can no
// longer start it. Ambiguity is reported as "still active" so a database blip
// never fails a child of a healthy batch.
func (s *upgradeJobPickupSupervisor) batchParentCanStillStartChild(
	ctx context.Context,
	job database.PendingUpgradeJob,
	logger log.Logger,
) bool {
	if job.BatchUpgradeID == "" {
		return false
	}

	parent, err := s.storage.GetBatchClusterUpgradeJobByUUID(ctx, job.BatchUpgradeID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false
		}
		logger.Warn("upgrade-job-supervisor: unable to read the parent batch upgrade job; treating the batch as active",
			"batchUpgradeID", job.BatchUpgradeID, "error", err)
		return true
	}
	if parent == nil {
		return false
	}

	switch parent.Status {
	case string(models.UpgradeStatusPending), string(models.UpgradeStatusInProgress):
		return true
	default:
		return false
	}
}

// markPendingJobFailed locks the still-pending row and marks it FAILED in a short
// transaction. Temporal RPCs must not run while this lock is held.
func (s *upgradeJobPickupSupervisor) markPendingJobFailed(
	ctx context.Context,
	job database.PendingUpgradeJob,
	cutoff time.Time,
) error {
	return s.storage.WithTransaction(ctx, func(tx dbutils.Transaction) error {
		now := time.Now().UTC()
		errorDetails := &datamodel.UpgradeErrorDetails{
			ErrorCode:    upgradePickupTimeoutErrorCode,
			ErrorMessage: fmt.Sprintf("Upgrade workflow was not picked up within %s", s.config.PickupTimeout),
			ErrorType:    upgradePickupTimeoutErrorType,
			Retryable:    true,
		}

		updates, err := s.lockAndBuildTimeoutUpdates(tx, job, cutoff, now, errorDetails)
		if err != nil {
			return err
		}
		return s.persistTimeoutUpdates(tx, job.Kind, job.UUID, updates)
	})
}

func (s *upgradeJobPickupSupervisor) lockAndBuildTimeoutUpdates(
	tx dbutils.Transaction,
	job database.PendingUpgradeJob,
	cutoff time.Time,
	now time.Time,
	errorDetails *datamodel.UpgradeErrorDetails,
) (map[string]interface{}, error) {
	updates := map[string]interface{}{
		"status":        string(models.UpgradeStatusFailed),
		"error_details": errorDetails,
		"completed_at":  now,
		"updated_at":    now,
		"deleted_at":    gorm.DeletedAt{Time: now, Valid: true},
	}

	switch job.Kind {
	case database.UpgradeJobKindSingle:
		var upgradeJob datamodel.ClusterUpgradeJob
		if err := tx.GORM().Unscoped().
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("uuid = ? AND status = ? AND started_at IS NULL AND batch_upgrade_id IS NULL AND created_at <= ?",
				job.UUID, string(models.UpgradeStatusPending), cutoff).
			First(&upgradeJob).Error; err != nil {
			return nil, err
		}
		return updates, nil

	case database.UpgradeJobKindBatchChild:
		var childJob datamodel.ClusterUpgradeJob
		if err := tx.GORM().Unscoped().
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("uuid = ? AND status = ? AND started_at IS NULL AND batch_upgrade_id IS NOT NULL AND created_at <= ?",
				job.UUID, string(models.UpgradeStatusPending), cutoff).
			First(&childJob).Error; err != nil {
			return nil, err
		}
		return updates, nil

	case database.UpgradeJobKindBatch:
		var batchJob datamodel.BatchClusterUpgradeJob
		if err := tx.GORM().Unscoped().
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("uuid = ? AND status = ? AND started_at IS NULL AND created_at <= ?",
				job.UUID, string(models.UpgradeStatusPending), cutoff).
			First(&batchJob).Error; err != nil {
			return nil, err
		}
		updates["skipped_count"] = batchJob.TotalClusters
		return updates, nil

	default:
		return nil, fmt.Errorf("unsupported upgrade job kind %q", job.Kind)
	}
}

func (s *upgradeJobPickupSupervisor) persistTimeoutUpdates(
	tx dbutils.Transaction,
	kind database.UpgradeJobKind,
	jobUUID string,
	updates map[string]interface{},
) error {
	var model interface{}
	switch kind {
	case database.UpgradeJobKindSingle, database.UpgradeJobKindBatchChild:
		model = &datamodel.ClusterUpgradeJob{}
	case database.UpgradeJobKindBatch:
		model = &datamodel.BatchClusterUpgradeJob{}
	default:
		return fmt.Errorf("unsupported upgrade job kind %q", kind)
	}

	return tx.GORM().Unscoped().Model(model).Where("uuid = ?", jobUUID).Updates(updates).Error
}

func (s *upgradeJobPickupSupervisor) terminateWorkflowIfNeeded(ctx context.Context, workflowID string, terminate bool) error {
	if !terminate {
		return nil
	}

	terminateCtx, cancelTerminate := context.WithTimeout(ctx, s.config.TerminateTimeout)
	defer cancelTerminate()

	err := s.temporal.TerminateWorkflow(
		terminateCtx,
		workflowID,
		"",
		"Upgrade workflow pickup timeout exceeded",
	)
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("terminate stale upgrade workflow %s: %w", workflowID, err)
	}
	return nil
}
