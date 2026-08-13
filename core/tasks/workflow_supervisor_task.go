// Package tasks provides background job supervisors and supporting helpers that
// coordinate Temporal workflow cleanup when jobs stall or time out.
package tasks

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	supervisorhandler "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/tasks/supervisor-handler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowEngine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	temporalDescribeTimeout  = 15 * time.Second
	resourceCleanupTimeout   = 30 * time.Second
	temporalTerminateTimeout = 10 * time.Second
)

var (
	// WorkflowSupervisorTask is the entry point used by cron/background runners.
	WorkflowSupervisorTask = runWorkflowSupervisorTask
	// workflowNotFoundGracePeriod controls how long the supervisor waits before
	// treating a missing Temporal workflow as an error condition.
	workflowNotFoundGracePeriod = env.GetDuration("WORKFLOW_SUPERVISOR_NOT_FOUND_GRACE_PERIOD", 5*time.Minute)
	// processingTimeoutGracePeriod controls how long after the workflow global timeout
	// the supervisor waits before treating a PROCESSING state job as timed out.
	processingTimeoutGracePeriod = env.GetDuration("WORKFLOW_SUPERVISOR_PROCESSING_TIMEOUT_GRACE_PERIOD", 5*time.Minute)
	// processingTimeoutEnabled controls whether the PROCESSING state timeout
	// scan runs at all. Set to false to disable; defaults to true (enabled).
	processingTimeoutEnabled = env.GetBool("WORKFLOW_SUPERVISOR_PROCESSING_TIMEOUT_ENABLED", true)

	// processingStateEligibleJobTypes defines the set of job types for which
	// the PROCESSING state timeout scan is enabled. Pool and volume delete
	// operations are supported; create operations are generally excluded because
	// a timed-out create workflow may have partially provisioned external
	// resources (ONTAP/GCP) that require manual investigation rather than
	// automatic state transition. Active Directory create is an exception: its
	// handler only transitions the resource to ERROR (never to a healthy state),
	// so a timed-out create is surfaced as failed instead of being left stuck in
	// CREATING, while still leaving any partial external resources for follow-up.
	processingStateEligibleJobTypes = map[datamodel.JobType]struct{}{
		datamodel.JobTypeDeletePool:            {},
		datamodel.JobTypeDeleteLargePool:       {},
		datamodel.JobTypeDeleteVolume:          {},
		datamodel.JobTypeDeleteLargeVolume:     {},
		datamodel.JobTypeDeleteActiveDirectory: {},
		datamodel.JobTypeCreateActiveDirectory: {},
		// Host group update: a timed-out workflow leaves the host group in UPDATING;
		// the handler transitions it to ERROR so it is not left stuck.
		datamodel.JobTypeUpdateHostGroup: {},
	}
)

type workflowSupervisorTaskRunner struct {
	storage       database.Storage
	temporal      client.Client
	correlationID string

	handlers   map[string]supervisorhandler.Handler
	handlersMu sync.RWMutex
}

// runWorkflowSupervisorTask is the cron entry point that scans for stalled jobs
// and delegates cleanup to registered supervisor handlers.
func runWorkflowSupervisorTask(ctx context.Context, storage database.Storage, temporal client.Client, correlationID string, handlers ...supervisorhandler.Handler) {
	loggerFields := log.Fields{string(middleware.RequestCorrelationID): correlationID}

	ctx = context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)
	ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, loggerFields)

	logger := util.GetLogger(ctx)
	logger.Infof("[WorkflowSupervisorTask] Starting workflow supervisor task - CorrelationID: %s", correlationID)

	runner := &workflowSupervisorTaskRunner{
		storage:       storage,
		temporal:      temporal,
		correlationID: correlationID,
		handlers:      make(map[string]supervisorhandler.Handler),
	}

	if len(handlers) == 0 {
		handlers = append(
			handlers,
			supervisorhandler.NewCmekHandler(),
			supervisorhandler.NewPoolHandler(),
			supervisorhandler.NewPoolUpdateHandler(),
			supervisorhandler.NewEncryptPoolHandler(),
			supervisorhandler.NewPoolDeleteHandler(),
			supervisorhandler.NewVolumeHandler(),
			supervisorhandler.NewVolumeUpdateHandler(),
			supervisorhandler.NewVolumeDeleteHandler(),
			supervisorhandler.NewBackupHandler(),
			supervisorhandler.NewBackupUpdateHandler(),
			supervisorhandler.NewBackupDeleteHandler(),
			supervisorhandler.NewBackupVaultHandler(),
			supervisorhandler.NewBackupVaultUpdateHandler(),
			supervisorhandler.NewBackupVaultDeleteHandler(),
			supervisorhandler.NewBackupPolicyHandler(),
			supervisorhandler.NewBackupPolicyUpdateHandler(),
			supervisorhandler.NewBackupPolicyDeleteHandler(),
			supervisorhandler.NewSnapshotHandler(),
			supervisorhandler.NewSnapshotDeleteHandler(),
			supervisorhandler.NewReplicationHandler(),
			supervisorhandler.NewReplicationUpdateHandler(),
			supervisorhandler.NewReplicationDeleteHandler(),
			supervisorhandler.NewKmsDeleteHandler(),
			supervisorhandler.NewKmsMigrateHandler(),
			supervisorhandler.NewNetworkHandler(),
			supervisorhandler.NewActiveDirectoryDeleteHandler(),
			supervisorhandler.NewActiveDirectoryCreateHandler(),
			supervisorhandler.NewQuotaRuleHandler(),
			supervisorhandler.NewQuotaRuleUpdateHandler(),
			supervisorhandler.NewQuotaRuleDeleteHandler(),
			supervisorhandler.NewHostGroupUpdateHandler(),
		)
	}

	runner.registerHandlers(handlers...)
	runner.scan(ctx)
}

// registerHandlers installs one or more handler implementations keyed by their
// advertised job types.
func (r *workflowSupervisorTaskRunner) registerHandlers(handlers ...supervisorhandler.Handler) {
	r.handlersMu.Lock()
	defer r.handlersMu.Unlock()

	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		for _, jobType := range handler.JobTypes() {
			r.handlers[string(jobType)] = handler
		}
	}
}

// handlerFor retrieves the handler registered for the supplied job type.
func (r *workflowSupervisorTaskRunner) handlerFor(jobType string) (supervisorhandler.Handler, bool) {
	r.handlersMu.RLock()
	handler, ok := r.handlers[jobType]
	r.handlersMu.RUnlock()
	return handler, ok
}

// supportedJobTypes returns the set of job type identifiers known to the runner.
func (r *workflowSupervisorTaskRunner) supportedJobTypes() []string {
	r.handlersMu.RLock()
	defer r.handlersMu.RUnlock()

	jobTypes := make([]string, 0, len(r.handlers))
	for jobType := range r.handlers {
		jobTypes = append(jobTypes, jobType)
	}
	return jobTypes
}

// scan locates timed-out jobs and invokes the registered handler for each.
func (r *workflowSupervisorTaskRunner) scan(ctx context.Context) {
	logger := util.GetLogger(ctx).With(log.Fields{string(middleware.RequestCorrelationID): r.correlationID})
	jobTypes := r.supportedJobTypes()
	if len(jobTypes) == 0 {
		logger.Debug("workflow-supervisor-task: no handlers registered")
		return
	}

	// Scan for NEW state jobs (existing behavior)
	r.scanNewStateJobs(ctx, jobTypes)

	// Scan for PROCESSING state jobs that may have timed out
	r.scanProcessingStateTimeouts(ctx, jobTypes)
}

// jobVisitorFunc processes a single job that passed all common filtering.
type jobVisitorFunc func(ctx context.Context, job *datamodel.Job, handler supervisorhandler.Handler)

// jobSkipFunc returns true if a job should be skipped during iteration, beyond
// the common override-grace-period check. Used by the PROCESSING scan for
// per-job timeout filtering.
type jobSkipFunc func(job *datamodel.Job, now time.Time, logger log.Logger) bool

// fetchAndProcessJobs is the shared fetch → sort → iterate pipeline used by
// both scanNewStateJobs and scanProcessingStateTimeouts.
func (r *workflowSupervisorTaskRunner) fetchAndProcessJobs(
	ctx context.Context,
	filter dbutils.Filter,
	stateLabel string,
	skipFn jobSkipFunc,
	visitFn jobVisitorFunc,
) {
	logger := util.GetLogger(ctx).With(log.Fields{string(middleware.RequestCorrelationID): r.correlationID})

	jobs, err := r.storage.GetJobsWithCondition(ctx, filter)
	if err != nil {
		logger.Errorf("workflow-supervisor-task: failed to fetch %s state jobs: %v", stateLabel, err)
		return
	}

	if len(jobs) == 0 {
		logger.Infof("workflow-supervisor-task: no candidate %s state jobs found", stateLabel)
		return
	}

	sort.SliceStable(jobs, func(i, j int) bool {
		return jobs[i].ID < jobs[j].ID
	})

	now := time.Now().UTC()
	for _, job := range jobs {
		if skipFn != nil && skipFn(job, now, logger) {
			continue
		}

		if skip, resumeAt, grace := shouldSkipJobForOverrideGracePeriod(job, now); skip {
			logger.With(log.Fields{
				"jobUUID":      job.UUID,
				"jobType":      job.Type,
				"createdAt":    job.CreatedAt.UTC(),
				"resumeAt":     resumeAt,
				"overrideWait": grace.String(),
			}).Infof("workflow-supervisor-task: deferring %s state job due to override grace period", stateLabel)
			continue
		}

		handler, ok := r.handlerFor(job.Type)
		if !ok {
			logger.With(log.Fields{"jobUUID": job.UUID, "jobType": job.Type}).Warnf(
				"workflow-supervisor-task: no handler registered for %s state job type", stateLabel,
			)
			continue
		}
		logger.Infof("workflow-supervisor-task: processing %s state job type %s", stateLabel, job.Type)
		visitFn(ctx, job, handler)
	}
}

// scanNewStateJobs handles jobs stuck in NEW state after the grace period.
func (r *workflowSupervisorTaskRunner) scanNewStateJobs(ctx context.Context, jobTypes []string) {
	cutoffTimestamp := time.Now().Add(-workflowNotFoundGracePeriod).UTC().Format(time.RFC3339)
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("state", "=", string(datamodel.JobsStateNEW)),
		dbutils.NewFilterCondition("type", "IN", jobTypes),
		dbutils.NewFilterCondition("created_at", "<=", cutoffTimestamp),
	)

	r.fetchAndProcessJobs(ctx, *filter, "NEW", nil, func(ctx context.Context, job *datamodel.Job, handler supervisorhandler.Handler) {
		r.evaluateJob(ctx, job, handler, datamodel.JobsStateNEW)
	})
}

// getWorkflowTimeoutForJobType returns the workflow timeout duration for the given job type.
// Eligible PROCESSING job types currently share the global VCP workflow timeout.
func getWorkflowTimeoutForJobType(_ string) time.Duration {
	return workflowEngine.GetWorkflowGlobalTimeout()
}

// scanProcessingStateTimeouts handles jobs in PROCESSING state where the Temporal
// workflow has timed out. This catches cases where the workflow exceeded its
// WorkflowRunTimeout and was terminated by Temporal, leaving the job stuck.
// Only job types listed in processingStateEligibleJobTypes are scanned.
func (r *workflowSupervisorTaskRunner) scanProcessingStateTimeouts(ctx context.Context, jobTypes []string) {
	if !processingTimeoutEnabled {
		util.GetLogger(ctx).Info("workflow-supervisor-task: PROCESSING state timeout scan is disabled")
		return
	}

	eligible := filterEligibleProcessingJobTypes(jobTypes)
	if len(eligible) == 0 {
		util.GetLogger(ctx).Info("workflow-supervisor-task: no eligible job types for PROCESSING state scan")
		return
	}

	// Use the minimum workflow timeout as the initial filter cutoff.
	// Each job will be individually checked against its specific workflow timeout.
	minTimeout := workflowEngine.GetWorkflowGlobalTimeout()
	for _, jobType := range eligible {
		timeout := getWorkflowTimeoutForJobType(jobType)
		if timeout < minTimeout {
			minTimeout = timeout
		}
	}

	// Compute the earliest created_at for which a job could still be within its timeout window.
	// Jobs created before (now - minTimeout - gracePeriod) have exceeded even the shortest
	// workflow timeout plus grace period, so they are candidates for the DB query. Individual
	// jobs are then re-checked against their specific timeout in fetchAndProcessJobs.
	cutoffTimestamp := time.Now().Add(-minTimeout - processingTimeoutGracePeriod).UTC().Format(time.RFC3339)
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("state", "=", string(datamodel.JobsStatePROCESSING)),
		dbutils.NewFilterCondition("type", "IN", eligible),
		dbutils.NewFilterCondition("created_at", "<=", cutoffTimestamp),
	)

	r.fetchAndProcessJobs(ctx, *filter, "PROCESSING",
		func(job *datamodel.Job, now time.Time, logger log.Logger) bool {
			jobTimeout := getWorkflowTimeoutForJobType(job.Type)
			jobCutoff := now.Add(-jobTimeout - processingTimeoutGracePeriod)
			if job.CreatedAt.After(jobCutoff) {
				logger.With(log.Fields{
					"jobUUID":         job.UUID,
					"jobType":         job.Type,
					"createdAt":       job.CreatedAt.UTC(),
					"workflowTimeout": jobTimeout.String(),
					"cutoff":          jobCutoff.UTC(),
				}).Debug("workflow-supervisor-task: job has not exceeded its workflow timeout yet")
				return true
			}
			return false
		},
		func(ctx context.Context, job *datamodel.Job, handler supervisorhandler.Handler) {
			r.evaluateProcessingJob(ctx, job, handler)
		},
	)
}

// evaluateProcessingJob checks if a PROCESSING state job's workflow has timed out
// and triggers cleanup if so. Unlike NEW state jobs, we only clean up if Temporal
// explicitly reports the workflow as TIMED_OUT.
func (r *workflowSupervisorTaskRunner) evaluateProcessingJob(ctx context.Context, job *datamodel.Job, handler supervisorhandler.Handler) {
	describeCtx, cancel := context.WithTimeout(ctx, temporalDescribeTimeout)
	defer cancel()

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"workflowID":                            job.WorkflowID,
		"jobType":                               job.Type,
		"jobState":                              job.State,
		string(middleware.RequestCorrelationID): r.correlationID,
	})

	resp, err := r.temporal.DescribeWorkflowExecution(describeCtx, job.WorkflowID, "")
	if err != nil {
		// For PROCESSING jobs, we don't clean up on describe errors - only on explicit TIMED_OUT status
		// This is more conservative than NEW state handling because the workflow may still be running
		logger.Warnf("workflow-supervisor-task: temporal describe failed for PROCESSING job; skipping: %v", err)
		return
	}

	if resp == nil || resp.WorkflowExecutionInfo == nil {
		logger.Warnf("workflow-supervisor-task: describe response missing execution info for PROCESSING job")
		return
	}

	status := resp.WorkflowExecutionInfo.GetStatus()
	logger.Infof("workflow-supervisor-task: PROCESSING job workflow: %s status: %s", job.WorkflowID, status)

	// Only handle TIMED_OUT status for PROCESSING jobs
	if status != enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT {
		logger.Debugf("workflow-supervisor-task: PROCESSING job workflow status=%s; not timed out, skipping", status.String())
		return
	}

	logger.Warnf("workflow-supervisor-task: detected timed-out workflow for PROCESSING job; starting cleanup")
	r.cleanupJob(ctx, job, handler, supervisorhandler.EventTimeout, datamodel.JobsStatePROCESSING, logger)
}

// evaluateJob inspects Temporal state for a job and triggers cleanup when
// timeout conditions are met. The expectedState parameter indicates which job
// state we expect to find when acquiring the lock for cleanup.
func (r *workflowSupervisorTaskRunner) evaluateJob(ctx context.Context, job *datamodel.Job, handler supervisorhandler.Handler, expectedState datamodel.JobState) {
	describeCtx, cancel := context.WithTimeout(ctx, temporalDescribeTimeout)
	defer cancel()

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"workflowID":                            job.WorkflowID,
		"jobType":                               job.Type,
		"jobState":                              job.State,
		string(middleware.RequestCorrelationID): r.correlationID,
	})

	resp, err := r.temporal.DescribeWorkflowExecution(describeCtx, job.WorkflowID, "")
	if err != nil {
		logger.Errorf("workflow-supervisor-task: temporal describe failed; starting cleanup: %v", err)
		r.cleanupJob(ctx, job, handler, supervisorhandler.EventTimeout, expectedState, logger)
		return
	}

	if resp == nil || resp.WorkflowExecutionInfo == nil {
		logger.Warnf("workflow-supervisor-task: describe response missing execution info")
		return
	}

	status := resp.WorkflowExecutionInfo.GetStatus()
	logger.Infof("workflow-supervisor-task: workflow: %s status: %s", job.WorkflowID, status)
	if status != enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT {
		logger.Debugf("workflow-supervisor-task: unsupported workflow status=%s; ignoring", status.String())
		return
	}

	logger.Warnf("workflow-supervisor-task: detected timed-out workflow; starting cleanup")

	r.cleanupJob(ctx, job, handler, supervisorhandler.EventTimeout, expectedState, logger)
}

// markJobAsError updates the job state and detail to record a timeout failure.
func (r *workflowSupervisorTaskRunner) markJobAsError(ctx context.Context, job *datamodel.Job) error {
	trackingID := job.TrackingID
	// If the job never started processing (TrackingID is 0), use the supervisor timeout error code
	// so customers get a proper error message instead of "undefined error"
	if trackingID == 0 {
		trackingID = vsaerrors.ErrWorkflowSupervisorTimeout
	}
	return r.storage.UpdateJob(ctx, job.UUID, string(datamodel.JobsStateERROR), trackingID, supervisorhandler.WorkflowTimeoutDetail)
}

// cleanupJob terminates the workflow if needed, delegates compensating actions,
// and marks the job as failed. The expectedState parameter specifies which job
// state to look for when acquiring the row lock - this prevents race conditions
// where the job state changed between scan and cleanup.
func (r *workflowSupervisorTaskRunner) cleanupJob(ctx context.Context, job *datamodel.Job, handler supervisorhandler.Handler, event supervisorhandler.Event, expectedState datamodel.JobState, logger log.Logger) {
	if r.temporal != nil && job.WorkflowID != "" {
		lockErr := r.storage.WithTransaction(ctx, func(tx dbutils.Transaction) error {
			var dbJob datamodel.Job
			if err := tx.GORM().Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("uuid = ? AND state = ?", job.UUID, string(expectedState)).
				First(&dbJob).Error; err != nil {
				return err
			}

			terminateCtx, cancelTerminate := context.WithTimeout(ctx, temporalTerminateTimeout)
			defer cancelTerminate()

			if err := r.temporal.TerminateWorkflow(terminateCtx, job.WorkflowID, "", supervisorhandler.WorkflowTimeoutDetail); err != nil {
				var notFound *serviceerror.NotFound
				if errors.As(err, &notFound) {
					logger.Debugf("workflow-supervisor-task: terminate skipped; workflow already missing")
				}
			}
			return nil
		})

		if lockErr != nil {
			if errors.Is(lockErr, gorm.ErrRecordNotFound) {
				logger.Debugf("workflow-supervisor-task: job missing or state changed while acquiring lock; skipping terminate and cleanup")
			} else {
				logger.Warnf("workflow-supervisor-task: failed to lock job for terminate: %v", lockErr)
			}
			return
		}
	}

	cleanupCtx, cancelCleanup := context.WithTimeout(ctx, resourceCleanupTimeout)
	defer cancelCleanup()

	if err := handler.Handle(cleanupCtx, job, event, r.storage); err != nil {
		logger.Errorf("workflow-supervisor-task: cleanup failed: %v", err)
		return
	}

	if err := r.markJobAsError(ctx, job); err != nil {
		logger.Errorf("workflow-supervisor-task: failed to mark job error after cleanup: %v", err)
	}
}

// filterEligibleProcessingJobTypes returns only the job types from the input
// that are present in processingStateEligibleJobTypes.
func filterEligibleProcessingJobTypes(jobTypes []string) []string {
	var eligible []string
	for _, jt := range jobTypes {
		if _, ok := processingStateEligibleJobTypes[datamodel.JobType(jt)]; ok {
			eligible = append(eligible, jt)
		}
	}
	return eligible
}

// shouldSkipJobForOverrideGracePeriod reports whether a job should be deferred
// based on the override grace period attribute.
func shouldSkipJobForOverrideGracePeriod(job *datamodel.Job, now time.Time) (bool, time.Time, time.Duration) {
	if job == nil || job.JobAttributes == nil || job.JobAttributes.SupervisorAttributes == nil {
		return false, time.Time{}, 0
	}

	grace := job.JobAttributes.SupervisorAttributes.OverrideGracePeriod
	if grace <= 0 {
		return false, time.Time{}, 0
	}

	createdAt := job.CreatedAt
	if createdAt.IsZero() {
		return false, time.Time{}, 0
	}

	resumeAt := createdAt.Add(grace)
	if now.Before(resumeAt) {
		return true, resumeAt, grace
	}

	return false, time.Time{}, 0
}
