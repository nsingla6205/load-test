package database

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	getJobWithDetails = _getJobWithDetails
)

// INVARIANT: Terminal state transitions (DONE / ERROR / CANCELLED) for
// datamodel.Job MUST flow through one of the four methods below, each of
// which emits the vcp.job.status counter and vcp.job.duration histogram
// via recordJobTerminalMetric (see job_metrics.go):
//
//   1. UpdateJob                       — main state-transition write
//   2. DeleteJob                       — soft-delete forces state = ERROR
//   3. CancelRunningJobsForResource    — bulk cancel on resource delete
//   4. CancelPrepopulateJobsForVolume  — bulk cancel on volume delete
//
// If you add a new method (or a raw GORM path) that transitions a job into
// a terminal state, you MUST either route it through one of the above OR
// call recordJobTerminalMetric after your DB commit succeeds and after
// verifying the strict non-terminal -> terminal transition. Otherwise the
// metric will silently under-count and dashboards / alerts built on it
// will lie.
//
// Wrappers (retry engine in sewrapper.go, PersistenceStore in
// persistance_store.go) delegate to these four methods, so they are
// covered automatically. The workflow supervisor's cleanup path
// (core/tasks/workflow_supervisor_task.go: markJobAsError) also routes
// through UpdateJob (path #1), so supervisor-driven ERROR finalizations
// are covered automatically.

func (d *DataStoreRepository) CreateJob(ctx context.Context, job *datamodel.Job) (*datamodel.Job, error) {
	return d.createJobImpl(ctx, job, "")
}

// CreateJobWithWorkflowID persists a job with workflow_id set to workflowID (FlexCache child jobs).
func (d *DataStoreRepository) CreateJobWithWorkflowID(ctx context.Context, job *datamodel.Job, workflowID string) (*datamodel.Job, error) {
	return d.createJobImpl(ctx, job, workflowID)
}

func (d *DataStoreRepository) createJobImpl(ctx context.Context, job *datamodel.Job, workflowID string) (*datamodel.Job, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	if job.UUID == "" {
		job.UUID = utils.RandomUUID()
	}
	job.CreatedAt = time.Now()
	job.UpdatedAt = job.CreatedAt
	if workflowID != "" {
		job.WorkflowID = workflowID
	} else {
		job.WorkflowID = job.UUID
	}
	if job.JobAttributes != nil {
		job.ResourceUUID = job.JobAttributes.ResourceUUID
	}

	if err := tx.Create(job).Error; err != nil {
		return nil, err
	}

	dbJob, err := getJobWithDetails(tx, &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: job.UUID}})
	if err != nil {
		return nil, err
	}

	return dbJob, nil
}

func (d *DataStoreRepository) GetJob(ctx context.Context, id string) (*datamodel.Job, error) {
	return getJobWithDetails(d.db.GORM().WithContext(ctx), &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: id}})
}

// UpdateJobOpts carries optional metadata that modifies how UpdateJob emits
// the terminal-transition metric. It is deliberately a variadic parameter on
// UpdateJob so existing (workflow-driven) callers do not need to change; only
// callers whose semantics are NOT "workflow or activity finalized the job"
// need to opt in and set a specific Reason.
//
// Only the first element is honored when callers pass multiple opts; this is
// intentional so helper wrappers can chain through without special-casing.
type UpdateJobOpts struct {
	// Reason overrides the default `reason` label value on the emitted
	// vcp.job.status / vcp.job.duration metrics. Leave zero (empty string)
	// to inherit the default (JobTerminalReasonWorkflowError). The
	// supervisor cleanup path MUST set this to
	// JobTerminalReasonSupervisorTimeout.
	Reason JobTerminalReason
}

// UpdateJob uses a NAMED return (`err error`) so the deferred
// commitOrRollbackOnError below can propagate a commit/rollback failure back
// to the caller (an explicit `return nil` would otherwise clobber the error
// set by the defer via *err). The named return also lets the metric defer
// observe the final err state, which is why no terminal metric is emitted
// when the commit itself failed.
//
// opts is variadic for backward compatibility: pass nothing and the terminal
// metric (if emitted) is tagged with reason=workflow_error, which is correct
// for every workflow/activity caller. The supervisor cleanup path passes
// UpdateJobOpts{Reason: JobTerminalReasonSupervisorTimeout} to attribute
// its finalizations distinctly. Non-workflow future callers MUST pass a
// dedicated reason rather than inheriting the default — see the
// JobTerminalReason godoc for the invariant.
func (d *DataStoreRepository) UpdateJob(ctx context.Context, id, status string, trackingID int, errorDetails string, opts ...UpdateJobOpts) (err error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)

	// Metric emission for a terminal transition happens after the DB commit
	// completes. Deferring FIRST here means (Go LIFO defer order) this closure
	// runs AFTER commitOrRollbackOnError below, so we observe the final `err`
	// including any commit failure and emit only when the transition actually
	// persisted.
	var (
		emitTerminal   bool
		emitStateLabel string
		emitJobType    string
		emitCreatedAt  time.Time
	)
	// Resolve the reason label from caller opts up front so the metric defer
	// closes over a plain string. Honors only the first opts element; see
	// UpdateJobOpts godoc for the "first-wins" rationale.
	callerReason := jobMetricReasonWorkflowError
	if len(opts) > 0 && opts[0].Reason != "" {
		callerReason = string(opts[0].Reason)
	}
	defer func() {
		if err == nil && emitTerminal {
			// Successful completions carry an empty reason regardless of what
			// the caller supplied — the DONE series stays clean. Only error /
			// canceled transitions carry the resolved reason (workflow,
			// supervisor_timeout, or any future caller-declared value).
			reason := jobMetricReasonNone
			if emitStateLabel != jobMetricStateDone {
				reason = callerReason
			}
			recordJobTerminalMetric(ctx, emitJobType, emitStateLabel, reason, emitCreatedAt, time.Now())
		}
	}()
	defer commitOrRollbackOnError(logger, tx, &err)

	job, err := getJobWithDetails(tx, &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: id}})
	if err != nil {
		return err
	}

	// Capture the old state BEFORE we mutate the struct so the strict
	// transition check (non-terminal -> terminal) can be evaluated.
	oldState := job.State

	job.UpdatedAt = time.Now()
	job.State = status
	job.TrackingID = trackingID
	job.ErrorDetails = errorDetails
	if err = tx.Updates(job).Error; err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	// Strict transition check: only emit when a running job (NEW / PROCESSING
	// / WAIT_FOR_TEMPORAL) reaches a terminal state (DONE / ERROR / CANCELLED).
	// State churn within non-terminal states or terminal -> terminal writes
	// (e.g. supervisor re-finalizing an already-failed job) do NOT emit.
	if isNonTerminalJobState(oldState) {
		if label, terminal := classifyJobTerminalState(status); terminal {
			emitTerminal = true
			emitStateLabel = label
			emitJobType = job.Type
			emitCreatedAt = job.CreatedAt
		}
	}
	return nil
}

func (d *DataStoreRepository) UpdateJobAttributes(ctx context.Context, uuid string, jobAttributes *datamodel.JobAttributes) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	job, err := getJobWithDetails(tx, &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: uuid}})
	if err != nil {
		return err
	}
	job.UpdatedAt = time.Now()
	job.JobAttributes = jobAttributes
	if jobAttributes != nil {
		job.ResourceUUID = jobAttributes.ResourceUUID
	}
	if err = tx.Updates(job).Error; err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// DeleteJob uses a NAMED return (`err error`) so a deferred commit failure in
// commitOrRollbackOnError is visible to the caller. See UpdateJob for the full
// rationale.
func (d *DataStoreRepository) DeleteJob(ctx context.Context, id, errorDetails string) (err error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)

	// See UpdateJob for the defer-ordering rationale: this closure runs AFTER
	// the commit defer below and inspects the final `err`.
	var (
		emitTerminal  bool
		emitJobType   string
		emitCreatedAt time.Time
	)
	defer func() {
		if err == nil && emitTerminal {
			recordJobTerminalMetric(ctx, emitJobType, jobMetricStateError, jobMetricReasonJobDeleted, emitCreatedAt, time.Now())
		}
	}()
	defer commitOrRollbackOnError(logger, tx, &err)

	job, err := getJobWithDetails(tx, &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: id}})
	if err != nil {
		return err
	}

	oldState := job.State

	job.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	job.State = string(datamodel.JobsStateERROR)
	job.ErrorDetails = errorDetails
	if err = tx.Updates(job).Error; err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	// Strict transition: only emit if the job was still running. DeleteJob is
	// occasionally called on already-terminal jobs during cleanup, and we do
	// NOT want to over-count those as fresh failures.
	if isNonTerminalJobState(oldState) {
		emitTerminal = true
		emitJobType = job.Type
		emitCreatedAt = job.CreatedAt
	}
	return nil
}

func (d *DataStoreRepository) ListOngoingPoolJobsWithKmsConfigId(ctx context.Context, kmsId, accountId int64) ([]*datamodel.Job, error) {
	db := d.db.GORM().WithContext(ctx)
	jobs := make([]*datamodel.Job, 0)

	err := db.Joins("INNER JOIN pools on pools.name = jobs.resource_name").Where("jobs.state = ? and jobs.type IN (?, ?) and pools.kms_config_id = ? and pools.account_id = ?", datamodel.JobsStatePROCESSING, datamodel.JobTypeCreatePool, datamodel.JobTypeCreateLargePool, kmsId, accountId).Find(&jobs).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return jobs, nil
}

func (d *DataStoreRepository) GetJobsWithCondition(ctx context.Context, filter utils2.Filter) ([]*datamodel.Job, error) {
	db := d.db.ApplyFilter(filter.Apply()).GORM().WithContext(ctx)
	jobs := make([]*datamodel.Job, 0)
	err := db.Find(&jobs).Error
	if err != nil {
		return nil, err
	}
	return jobs, nil
}

// GetJobByResourceUUID retrieves the job by its resource UUID.
// When utils.EnableJobResourceUUIDIndex is true, queries the indexed resource_uuid column.
// Otherwise uses the JSONB path job_attributes->>'resource_uuid'.
func (d *DataStoreRepository) GetJobByResourceUUID(ctx context.Context, resourceUUID string, jobType string) (*datamodel.Job, error) {
	job := &datamodel.Job{}
	var query *gorm.DB
	if utils.EnableJobResourceUUIDIndex {
		query = d.db.GORM().WithContext(ctx).Where("resource_uuid = ?", resourceUUID)
	} else {
		query = d.db.GORM().WithContext(ctx).Where("job_attributes ->> 'resource_uuid' = ?", resourceUUID)
	}
	if jobType != "" {
		query = query.Where("type = ?", jobType)
	}

	err := query.First(job).Error
	if err != nil {
		return nil, errors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "Job", nil)
	}
	return job, nil
}

func _getJobWithDetails(db *gorm.DB, query *datamodel.Job) (*datamodel.Job, error) {
	job := &datamodel.Job{}
	err := db.First(&job, query).Error
	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.NewNotFoundErr("job", nil))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return job, nil
}

func (d *DataStoreRepository) GetOngoingMigrateKmsConfigJob(ctx context.Context, accountId int64) (*datamodel.Job, error) {
	var job datamodel.Job
	err := d.db.GORM().WithContext(ctx).Where(
		"account_id = ? AND type = ? AND (state = ? OR state = ?)",
		accountId, datamodel.JobTypeMigrateKmsConfig, datamodel.JobsStateNEW, datamodel.JobsStatePROCESSING,
	).First(&job).Error

	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.NewNotFoundErr("job", nil))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return &job, nil
}

func (d *DataStoreRepository) GetOngoingEncryptPoolJobForAccount(ctx context.Context, accountId int64) (*datamodel.Job, error) {
	var job datamodel.Job
	err := d.db.GORM().WithContext(ctx).Where(
		"account_id = ? AND type = ? AND (state = ? OR state = ?)",
		accountId, datamodel.JobTypeEncryptPool, datamodel.JobsStateNEW, datamodel.JobsStatePROCESSING,
	).First(&job).Error

	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.NewNotFoundErr("job", nil))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return &job, nil
}

func cmekJobConflictMessage(jobType string) string {
	switch jobType {
	case string(datamodel.JobTypeEncryptPool):
		return "A pool encryption (EncryptPool) is already in progress for this account"
	case string(datamodel.JobTypeMigrateKmsConfig):
		return "A CMEK migration (EncryptVolumes) is already in progress for this account"
	default:
		return fmt.Sprintf("a %s job is already in progress for this account", jobType)
	}
}

// CreateCMEKJob atomically enforces the account-wide invariant that at most one CMEK job
// (ENCRYPT_POOL or MIGRATE_KMS_CONFIG) may be NEW/PROCESSING per account at a time,
func (d *DataStoreRepository) CreateCMEKJob(ctx context.Context, accountId int64, job *datamodel.Job) (*datamodel.Job, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	var account datamodel.Account
	if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", accountId).
		First(&account).Error; err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			err = errors.NewNotFoundErr("account", nil)
		}
		return nil, err
	}

	var ongoing datamodel.Job
	findErr := tx.Where(
		"account_id = ? AND type IN (?, ?) AND (state = ? OR state = ?)",
		accountId, datamodel.JobTypeEncryptPool, datamodel.JobTypeMigrateKmsConfig,
		datamodel.JobsStateNEW, datamodel.JobsStatePROCESSING,
	).First(&ongoing).Error
	if findErr == nil {
		err = errors.NewConflictErr(cmekJobConflictMessage(ongoing.Type))
		return nil, err
	}
	if !vsaerrors.Is(findErr, gorm.ErrRecordNotFound) {
		err = findErr
		return nil, err
	}

	if job.UUID == "" {
		job.UUID = utils.RandomUUID()
	}
	job.CreatedAt = time.Now()
	job.UpdatedAt = job.CreatedAt
	job.WorkflowID = job.UUID
	if job.JobAttributes != nil {
		job.ResourceUUID = job.JobAttributes.ResourceUUID
	}

	if err = tx.Create(job).Error; err != nil {
		return nil, err
	}

	dbJob, err := getJobWithDetails(tx, &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: job.UUID}})
	if err != nil {
		return nil, err
	}

	return dbJob, nil
}

func (d *DataStoreRepository) CheckAndFetchDuplicateJobs(ctx context.Context, jobType string, correlationID string) (*datamodel.Job, error) {
	var job datamodel.Job
	err := d.db.GORM().Unscoped().WithContext(ctx).Where(
		"correlation_id = ? AND type = ?",
		correlationID, jobType,
	).First(&job).Error

	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			// do not return any error if no job is found
			return nil, nil
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return &job, nil
}

// CancelRunningJobsForResource uses a NAMED return (`err error`) so a deferred
// commit failure in commitOrRollbackOnError is visible to the caller. See
// UpdateJob for the full rationale.
func (d *DataStoreRepository) CancelRunningJobsForResource(ctx context.Context, resourceUUID string) (err error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)

	// Emit one metric per row that actually transitioned; runs after commit
	// (see UpdateJob for the defer-ordering rationale).
	var updated []datamodel.Job
	defer func() {
		if err != nil || len(updated) == 0 {
			return
		}
		now := time.Now()
		for i := range updated {
			j := &updated[i]
			recordJobTerminalMetric(ctx, j.Type, jobMetricStateCanceled, jobMetricReasonResourceDeleted, j.CreatedAt, now)
		}
	}()
	defer commitOrRollbackOnError(logger, tx, &err)

	var whereClause string
	if utils.EnableJobResourceUUIDIndex {
		whereClause = "resource_uuid = ? AND state IN (?, ?)"
	} else {
		whereClause = "job_attributes ->> 'resource_uuid' = ? AND state IN (?, ?)"
	}

	// Single UPDATE ... RETURNING * atomically mutates matching rows and
	// returns them, so the metric write set is exactly the DB write set with
	// no window for a concurrent insert to slip through between a preload
	// and the bulk update.
	if err = tx.Model(&updated).
		Clauses(clause.Returning{}).
		Where(whereClause, resourceUUID, datamodel.JobsStateNEW, datamodel.JobsStatePROCESSING).
		Update("state", string(datamodel.JobsStateCANCELLED)).Error; err != nil {
		updated = nil
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// CancelPrepopulateJobsForVolume cancels all active prepopulate jobs for a given volume UUID.
// This is called when a volume is deleted to prevent orphaned prepopulate jobs.
//
// Uses a NAMED return (`err error`) so a deferred commit failure in
// commitOrRollbackOnError is visible to the caller. See UpdateJob for the full
// rationale.
func (d *DataStoreRepository) CancelPrepopulateJobsForVolume(ctx context.Context, volumeUUID string) (err error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	errorDetails := fmt.Sprintf("Volume %s was deleted, prepopulate job cannot complete", volumeUUID)

	// Emit one ERROR-state metric per row that actually transitioned; runs
	// after commit (see UpdateJob for the defer-ordering rationale).
	var updated []datamodel.Job
	defer func() {
		if err != nil || len(updated) == 0 {
			return
		}
		now := time.Now()
		for i := range updated {
			j := &updated[i]
			recordJobTerminalMetric(ctx, j.Type, jobMetricStateError, jobMetricReasonResourceDeleted, j.CreatedAt, now)
		}
	}()
	defer commitOrRollbackOnError(logger, tx, &err)

	nonTerminal := []string{string(datamodel.JobsStateNEW), string(datamodel.JobsStatePROCESSING)}

	// Single UPDATE ... RETURNING * atomically mutates matching rows and
	// returns them, so the metric write set is exactly the DB write set with
	// no window for a concurrent insert to slip through between a preload
	// and the bulk update.
	if err = tx.Model(&updated).
		Clauses(clause.Returning{}).
		Where("type = ? AND resource_name = ? AND state IN ?",
			datamodel.JobTypeFlexCachePrePopulate, volumeUUID, nonTerminal).
		Updates(map[string]interface{}{
			"state":         string(datamodel.JobsStateERROR),
			"error_details": errorDetails,
		}).Error; err != nil {
		updated = nil
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}
