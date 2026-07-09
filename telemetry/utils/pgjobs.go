package utils

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
	vcputils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	JOB_STATUS_SCHEDULED   = "new"
	JOB_STATUS_IN_PROGRESS = "in_progress"
	JOB_STATUS_FINISHED    = "finished"
	JOB_STATUS_FAILED      = "failed"
)

var errJobPanicked = errors.New("job panicked")

var PollInterval = 1 * time.Second

const JobsTableName = "jobs"

// errLostJobOwnership signals that a worker no longer owns a job it tried to
// finalize (its lease expired and the sweeper, or another worker, took over).
// In that case the finalize is skipped to avoid clobbering the reclaimed job.
var errLostJobOwnership = errors.New("queue: lost job ownership before finalize")

// errLeaseLost signals that extendLease matched no row (lease expired, job
// reclaimed, max-runtime ceiling, or worker_id no longer owns the job).
var errLeaseLost = errors.New("queue: lease extend lost ownership")

// JobQueueConfig holds the lease/heartbeat/sweeper tunables that let a job run
// outside of a DB transaction while remaining recoverable after a crash.
type JobQueueConfig struct {
	// LeaseTTL is how long a claimed job is considered alive before the sweeper
	// may reclaim it. It must exceed the longest legitimate job runtime; the
	// heartbeat keeps extending it while the worker stays healthy.
	LeaseTTL time.Duration
	// HeartbeatInterval is how often a running worker extends its lease.
	HeartbeatInterval time.Duration
	// MaxRuntime is the absolute ceiling on how long the heartbeat will keep a
	// job's lease alive. Past this (measured from started_at) the heartbeat stops
	// extending, so a hung-but-alive job's lease expires and it gets reclaimed.
	MaxRuntime time.Duration
	// SweeperInterval is how often the background sweeper scans for expired leases.
	SweeperInterval time.Duration
	// SweeperEnabled gates the background reclaim loop.
	SweeperEnabled bool
	// UnlimitedMaxRuntimeQueues lists queues that skip the MaxRuntime started_at
	// ceiling in extendLease (heartbeat can extend indefinitely).
	UnlimitedMaxRuntimeQueues map[string]struct{}
}

// DefaultJobQueueConfig builds the queue configuration from the environment with
// safe defaults. LeaseTTL must be larger than the slowest job (e.g. BizOps CSV).
func DefaultJobQueueConfig() JobQueueConfig {
	leaseTTL := env.GetDuration("JOB_LEASE_TTL", 15*time.Minute)
	heartbeatInterval := env.GetDuration("JOB_LEASE_HEARTBEAT_INTERVAL", leaseTTL/3)
	return JobQueueConfig{
		LeaseTTL:          leaseTTL,
		HeartbeatInterval: heartbeatInterval,
		MaxRuntime:        env.GetDuration("JOB_MAX_RUNTIME", 1*time.Hour),
		SweeperInterval:   env.GetDuration("JOB_SWEEPER_INTERVAL", 1*time.Minute),
		SweeperEnabled:    env.GetBool("ENABLE_JOB_SWEEPER", true),
		UnlimitedMaxRuntimeQueues: vcputils.ParseCommaSeparatedStringToMap(
			env.GetString("JOB_UNLIMITED_MAX_RUNTIME_QUEUES", UsageQueue+","+BillingRetryQueue+","+BizOpsReportQueue),
		),
	}
}

// normalized returns the config with any invalid/misconfigured values clamped to
// safe values, so a bad env (e.g. JOB_LEASE_TTL=0) cannot cause a reclaim storm
// or reclaim healthy jobs.
func (c JobQueueConfig) normalized() JobQueueConfig {
	if c.LeaseTTL < time.Minute {
		c.LeaseTTL = 15 * time.Minute
	}
	if c.HeartbeatInterval <= 0 || c.HeartbeatInterval >= c.LeaseTTL {
		c.HeartbeatInterval = c.LeaseTTL / 3
	}
	if c.MaxRuntime < c.LeaseTTL {
		c.MaxRuntime = 4 * c.LeaseTTL
	}
	if c.SweeperInterval <= 0 {
		c.SweeperInterval = time.Minute
	}
	return c
}

type Job interface {
	Perform(processor interface{}, attempt int32) error
	Load(data string) (Job, error)
}

type JobQueue struct {
	db        *sql.DB
	processor interface{}

	registryMu      sync.RWMutex
	typeRegistry    map[string]reflect.Type
	metricsRecorder monitoring.MetricsRecorder
	config          JobQueueConfig
}

// NewQueue creates a JobQueue using lease/heartbeat/sweeper defaults from the
// environment. Prefer NewQueueWithConfig when explicit tuning is needed.
func NewQueue(db *sql.DB, p interface{}, metricRecorder monitoring.MetricsRecorder) *JobQueue {
	return NewQueueWithConfig(db, p, metricRecorder, DefaultJobQueueConfig())
}

// NewQueueWithConfig creates a JobQueue with an explicit configuration.
func NewQueueWithConfig(db *sql.DB, p interface{}, metricRecorder monitoring.MetricsRecorder, config JobQueueConfig) *JobQueue {
	return &JobQueue{
		db:              db,
		processor:       p,
		typeRegistry:    make(map[string]reflect.Type),
		metricsRecorder: metricRecorder,
		config:          config.normalized(),
	}
}

func (j *JobQueue) EnqueueAt(ctx context.Context, job Job, queue string, at time.Time) error {
	typeName := j.typeName(job)

	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("queue: failed marshaling: %v", err)
	}

	if _, err = j.db.ExecContext(
		ctx,
		`INSERT INTO `+JobsTableName+` (type_name, status, queue, data, scheduled_at) VALUES ($1, $2, $3, $4, $5)`,
		typeName,
		JOB_STATUS_SCHEDULED,
		queue,
		data,
		at,
	); err != nil {
		return fmt.Errorf("queue: failed inserting job: %w", err)
	}

	return nil
}

func (j *JobQueue) Enqueue(ctx context.Context, job Job, queue string) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("queue: enqueing queue=%v job=%+v", queue, job)

	typeName := j.typeName(job)

	data, err := json.Marshal(job)
	metricParams := &monitoring.MetricRecorderParams{
		QueueName: queue,
		JobType:   typeName,
	}
	if err != nil {
		metricParams.JobStatus = "marshal_failed"
		j.metricsRecorder.RecordJobEnqueued(metricParams)
		return fmt.Errorf("queue: failed marshaling: %v", err)
	}

	if _, err = j.db.ExecContext(
		ctx,
		`INSERT INTO `+JobsTableName+` (type_name, status, queue, data) VALUES ($1, $2, $3, $4)`,
		typeName,
		JOB_STATUS_SCHEDULED,
		queue,
		data,
	); err != nil {
		metricParams.JobStatus = "db_insertion_failed"
		j.metricsRecorder.RecordJobEnqueued(metricParams)
		return fmt.Errorf("queue: failed inserting job: %w", err)
	}
	metricParams.JobStatus = "success"
	j.metricsRecorder.RecordJobEnqueued(metricParams)

	return nil
}

// EnqueueBatch enqueues multiple jobs in a single database transaction for better performance
func (j *JobQueue) EnqueueBatch(ctx context.Context, jobs []Job, queue string) error {
	if len(jobs) == 0 {
		return nil
	}

	logger := util.GetLogger(ctx)
	logger.Debugf("queue: batch enqueuing %d jobs to queue=%v", len(jobs), queue)

	metricParams := &monitoring.MetricRecorderParams{
		QueueName: queue,
	}
	// Start a transaction for atomic batch insert
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		metricParams.JobStatus = "begin_transaction_failed"
		j.metricsRecorder.RecordJobBatchEnqueued(metricParams)
		return fmt.Errorf("queue: failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Try PostgreSQL approach first, fallback to SQLite if it fails
	err = j.enqueueBatchPostgres(ctx, tx, jobs, queue)
	if err != nil {
		metricParams.JobStatus = "enqueue_batch_postgres_failed"
		j.metricsRecorder.RecordJobBatchEnqueued(metricParams)
		// If PostgreSQL approach fails, try SQLite approach
		err = j.enqueueBatchSQLite(ctx, tx, jobs, queue)
	}

	if err != nil {
		return err
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		metricParams.JobStatus = "commit_transaction_failed"
		j.metricsRecorder.RecordJobBatchEnqueued(metricParams)
		return fmt.Errorf("queue: failed to commit batch transaction: %w", err)
	}

	metricParams.JobStatus = "success"
	metricParams.JobQuantity = len(jobs)

	j.metricsRecorder.RecordJobBatchEnqueued(metricParams)
	logger.Debugf("queue: successfully batch enqueued %d jobs to queue=%v", len(jobs), queue)
	return nil
}

// enqueueBatchPostgres uses PostgreSQL-specific bulk insert with arrays
func (j *JobQueue) enqueueBatchPostgres(ctx context.Context, tx *sql.Tx, jobs []Job, queue string) error {
	// Prepare arrays for bulk insert
	typeNames := make([]string, len(jobs))
	statuses := make([]string, len(jobs))
	queues := make([]string, len(jobs))
	dataValues := make([]string, len(jobs))

	for i, job := range jobs {
		typeNames[i] = j.typeName(job)
		statuses[i] = JOB_STATUS_SCHEDULED
		queues[i] = queue

		data, err := json.Marshal(job)
		if err != nil {
			return fmt.Errorf("queue: failed marshaling job %d: %v", i, err)
		}
		dataValues[i] = string(data)
	}

	// Prepare PostgreSQL arrays
	typeNamesArray, err := pqArray(typeNames)
	if err != nil {
		return fmt.Errorf("queue: failed to create type names array: %w", err)
	}
	statusesArray, err := pqArray(statuses)
	if err != nil {
		return fmt.Errorf("queue: failed to create statuses array: %w", err)
	}
	queuesArray, err := pqArray(queues)
	if err != nil {
		return fmt.Errorf("queue: failed to create queues array: %w", err)
	}
	dataValuesArray, err := pqArray(dataValues)
	if err != nil {
		return fmt.Errorf("queue: failed to create data values array: %w", err)
	}

	// Execute single bulk INSERT using PostgreSQL arrays
	query := fmt.Sprintf(`
		INSERT INTO %s (type_name, status, queue, data)
		SELECT unnest($1::text[]), unnest($2::text[]), unnest($3::text[]), unnest($4::text[])
	`, JobsTableName)

	if _, err = tx.ExecContext(ctx, query, typeNamesArray, statusesArray, queuesArray, dataValuesArray); err != nil {
		return fmt.Errorf("queue: failed inserting batch jobs: %w", err)
	}

	return nil
}

// enqueueBatchSQLite uses SQLite-compatible individual inserts
func (j *JobQueue) enqueueBatchSQLite(ctx context.Context, tx *sql.Tx, jobs []Job, queue string) error {
	query := fmt.Sprintf(`
		INSERT INTO %s (type_name, status, queue, data) VALUES (?, ?, ?, ?)
	`, JobsTableName)

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("queue: failed to prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i, job := range jobs {
		typeName := j.typeName(job)
		data, err := json.Marshal(job)
		if err != nil {
			return fmt.Errorf("queue: failed marshaling job %d: %v", i, err)
		}

		if _, err = stmt.ExecContext(ctx, typeName, JOB_STATUS_SCHEDULED, queue, data); err != nil {
			return fmt.Errorf("queue: failed inserting job %d: %w", i, err)
		}
	}

	return nil
}

// Dequeue claims a single ready job, runs it OUTSIDE of any DB transaction, then
// finalizes its terminal state in a separate short transaction. This ensures a
// long-running job never holds a pooled connection / open transaction / row lock
// for its whole duration. Crash recovery is handled by the lease + sweeper.
func (j *JobQueue) Dequeue(ctx context.Context, queues []string) error {
	logger := util.GetLogger(ctx)

	// Phase 1: claim the job in a short transaction and release the connection.
	job, workerID, err := j.claimJob(ctx, queues)
	if err != nil {
		return err
	}
	if job == nil {
		return nil
	}

	logger.Debugf("Dequeued job: id=%v type=%v attempt=%v queue=%v", job.ID, job.TypeName, job.Attempt, queues)

	// Resolve the concrete go type for the job.
	jobType, err := j.getType(job.TypeName)
	if err != nil {
		findErr := fmt.Errorf("unable to find related job '%v': %w", job.TypeName, err)
		if fErr := j.finalizeJob(ctx, job.ID, workerID, JOB_STATUS_FAILED, findErr.Error()); fErr != nil {
			return j.handleFinalizeError(ctx, job.ID, fErr, findErr)
		}
		return findErr
	}

	// Unmarshal the job payload.
	loadedJob, err := jobType.Load(job.Data)
	if err != nil {
		if fErr := j.finalizeJob(ctx, job.ID, workerID, JOB_STATUS_FAILED, err.Error()); fErr != nil {
			return j.handleFinalizeError(ctx, job.ID, fErr, err)
		}
		return err
	}

	// Phase 2: run the job with NO transaction held. A heartbeat keeps the lease
	// fresh and a recover() turns a panicking job into a normal failure.
	performErr := j.runJob(ctx, loadedJob, job.Attempt, job.ID, workerID, job.Queue)

	// Phase 3: finalize terminal state in a short, ownership-guarded transaction.
	status := JOB_STATUS_FINISHED
	jobErrText := ""
	jobMetricStatus := "success"
	if performErr != nil {
		status = JOB_STATUS_FAILED
		jobErrText = performErr.Error()
		jobMetricStatus = performMetricStatus(performErr)
	}

	if fErr := j.finalizeJob(ctx, job.ID, workerID, status, jobErrText); fErr != nil {
		if errors.Is(fErr, errLostJobOwnership) {
			// The job was reclaimed mid-run (lease expired); another worker will
			// re-run it. Do not bubble this as a dequeue error.
			logger.Warnf("queue: lost ownership of job %d before finalize (likely reclaimed by sweeper); skipping status write", job.ID)
			return nil
		}
		return fErr
	}

	j.recordProcessed(job.Queue, job.TypeName, jobMetricStatus)
	if performErr == nil {
		j.metricsRecorder.RecordJobDequeued(&monitoring.MetricRecorderParams{QueueName: job.Queue, JobType: job.TypeName, JobStatus: jobMetricStatus})
	}

	// A job failure is persisted (status=failed) and recorded; it is not an
	// infrastructure error, so Dequeue itself reports success here.
	return nil
}

// claimJob atomically claims one ready job in a short transaction: it marks the
// job in_progress, increments the attempt, sets a fresh lease and a unique
// worker id, then commits immediately so the pooled connection is released
// before the (potentially long) job work begins. Returns (nil, "", nil) when no
// job is ready.
func (j *JobQueue) claimJob(ctx context.Context, queues []string) (*datamodel.Job, string, error) {
	queueArray, err := pqArray(queues)
	if err != nil {
		return nil, "", err
	}

	typesArray, err := pqArray(j.registeredTypeNames())
	if err != nil {
		return nil, "", err
	}

	workerID := uuid.NewString()
	maxRetry := env.GetInt64("MAX_GOOGLE_BILLING_PUSH_RETRY", 5)
	leaseSeconds := int64(j.config.LeaseTTL.Seconds())

	sqlStmt := `
		UPDATE
		  ` + JobsTableName + `
		SET
		  status = $1,
		  started_at = clock_timestamp(),
		  attempt = attempt + 1,
		  lease_expires_at = now() + ($2 * interval '1 second'),
		  worker_id = $3
		WHERE
		  id IN (
			SELECT
			  id FROM ` + JobsTableName + ` j
			WHERE
			  (j.status = $4
			   or (j.status = $5 and j.attempt < $6)
			   or (j.status = $1 and j.attempt < $6 and j.lease_expires_at is not null and j.lease_expires_at < now()))
			  AND j.queue = any($7)
			  AND j.type_name = any($8)
			  AND (j.scheduled_at is null or (j.scheduled_at <= now()))
			ORDER BY
			  j.scheduled_at, j.created_at
			FOR UPDATE SKIP LOCKED
		  LIMIT 1)
		RETURNING id, type_name, data, attempt, queue
	`

	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		j.metricsRecorder.RecordJobDequeued(&monitoring.MetricRecorderParams{JobStatus: "begin_transaction_failed"})
		return nil, "", err
	}
	defer func() { _ = tx.Rollback() }()

	var job datamodel.Job
	row := tx.QueryRowContext(
		ctx,
		sqlStmt,
		JOB_STATUS_IN_PROGRESS,
		leaseSeconds,
		workerID,
		JOB_STATUS_SCHEDULED,
		JOB_STATUS_FAILED,
		int(maxRetry),
		queueArray,
		typesArray,
	)
	err = row.Scan(&job.ID, &job.TypeName, &job.Data, &job.Attempt, &job.Queue)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	} else if err != nil {
		return nil, "", err
	}

	if err = tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("failed to commit job claim: %w", err)
	}

	job.WorkerID = &workerID
	return &job, workerID, nil
}

// runJob runs Perform outside any DB tx; heartbeat keeps the lease; recover turns panics into errors.
func (j *JobQueue) runJob(ctx context.Context, loadedJob Job, attempt int32, jobID int64, workerID, queue string) (err error) {
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go j.heartbeat(heartbeatCtx, jobID, workerID, queue)

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", errJobPanicked, r)
			util.GetLogger(ctx).ErrorContext(ctx, "queue: job panicked",
				"job_id", jobID,
				"worker_id", workerID,
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}
	}()

	return loadedJob.Perform(j.processor, attempt)
}

func performMetricStatus(performErr error) string {
	if errors.Is(performErr, errJobPanicked) {
		return "panic"
	}
	return "failed"
}

// heartbeat periodically extends the lease for a running job until the context
// is cancelled. The update is ownership-guarded so it never resurrects a job
// that has already been reclaimed or finalized. On errLeaseLost it returns so
// the ticker does not keep running.
func (j *JobQueue) heartbeat(ctx context.Context, jobID int64, workerID, queue string) {
	if j.config.HeartbeatInterval <= 0 {
		return
	}

	ticker := time.NewTicker(j.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := j.extendLease(ctx, jobID, workerID, queue); err != nil {
				util.GetLogger(ctx).Warnf("queue: heartbeat failed for job %d (worker %s): %v", jobID, workerID, err)
				if errors.Is(err, errLeaseLost) {
					return
				}
			}
		}
	}
}

func (j *JobQueue) isUnlimitedMaxRuntime(queue string) bool {
	if len(j.config.UnlimitedMaxRuntimeQueues) == 0 {
		return false
	}
	_, ok := j.config.UnlimitedMaxRuntimeQueues[queue]
	return ok
}

// extendLease pushes the job's lease forward by LeaseTTL. It is ownership-guarded
// so it never resurrects a job already reclaimed or finalized by someone else.
// Queues in UnlimitedMaxRuntimeQueues skip the MaxRuntime started_at ceiling.
func (j *JobQueue) extendLease(ctx context.Context, jobID int64, workerID, queue string) error {
	leaseSeconds := int64(j.config.LeaseTTL.Seconds())
	sqlStmt := `UPDATE ` + JobsTableName + `
		 SET lease_expires_at = now() + ($1 * interval '1 second')
		 WHERE id = $2 AND status = $3 AND worker_id = $4`
	args := []any{leaseSeconds, jobID, JOB_STATUS_IN_PROGRESS, workerID}

	if !j.isUnlimitedMaxRuntime(queue) {
		sqlStmt += `
		   AND started_at > now() - ($5 * interval '1 second')`
		args = append(args, int64(j.config.MaxRuntime.Seconds()))
	}

	res, err := j.db.ExecContext(ctx, sqlStmt, args...)
	if err != nil {
		return err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed reading rows affected on extend lease: %w", err)
	}
	if affected == 0 {
		return errLeaseLost
	}

	return nil
}

// finalizeJob writes the terminal state for a job in a short transaction. The
// update is guarded by worker_id so that a worker which lost its lease (e.g. the
// sweeper already reclaimed the job) does not clobber a job another worker may
// be re-running; in that case errLostJobOwnership is returned.
func (j *JobQueue) finalizeJob(ctx context.Context, jobID int64, workerID, status, jobErr string) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(
		ctx,
		`UPDATE `+JobsTableName+`
		 SET status = $1, finished_at = clock_timestamp(), error = $2, lease_expires_at = NULL, worker_id = NULL
		 WHERE id = $3 AND worker_id = $4`,
		status, jobErr, jobID, workerID,
	)
	if err != nil {
		return fmt.Errorf("failed updating job status: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed reading rows affected on finalize: %w", err)
	}
	if affected == 0 {
		return errLostJobOwnership
	}

	return tx.Commit()
}

// handleFinalizeError formats a combined error when finalizing a failed job
// itself fails, downgrading a lost-ownership outcome to the original error.
func (j *JobQueue) handleFinalizeError(ctx context.Context, jobID int64, finalizeErr, originalErr error) error {
	if errors.Is(finalizeErr, errLostJobOwnership) {
		util.GetLogger(ctx).Warnf("queue: lost ownership of job %d before finalize (likely reclaimed by sweeper); skipping status write", jobID)
		return originalErr
	}
	return errors.Join(originalErr, fmt.Errorf("additionally failed to finalize job: %w", finalizeErr))
}

// recordProcessed emits the processed metric for a finished job, attributed to
// the job's own queue.
func (j *JobQueue) recordProcessed(queue, jobType, status string) {
	j.metricsRecorder.RecordJobProcessed(&monitoring.MetricRecorderParams{
		QueueName: queue,
		JobType:   jobType,
		JobStatus: status,
	})
}

// Sweep reclaims in-progress jobs whose lease has expired by marking them
// failed, so the normal retry path (status=failed AND attempt<max) re-runs them
// (and exhausted ones dead-letter). Returns the number of jobs reclaimed.
func (j *JobQueue) Sweep(ctx context.Context) (int64, error) {
	res, err := j.db.ExecContext(
		ctx,
		`UPDATE `+JobsTableName+`
		 SET status = $1, error = $2, finished_at = clock_timestamp(), lease_expires_at = NULL, worker_id = NULL
		 WHERE status = $3 AND lease_expires_at IS NOT NULL AND lease_expires_at < now()`,
		JOB_STATUS_FAILED, "lease expired; reclaimed by sweeper", JOB_STATUS_IN_PROGRESS,
	)
	if err != nil {
		return 0, fmt.Errorf("queue: sweeper reclaim failed: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("queue: sweeper rows affected failed: %w", err)
	}

	if affected > 0 {
		// The sweep is a single bulk reclaim across all queues/types, so use
		// explicit "all" placeholders to avoid an ambiguous empty-label series.
		j.metricsRecorder.RecordJobReclaimed(&monitoring.MetricRecorderParams{QueueName: "all", JobType: "all", JobQuantity: int(affected)})
	}

	return affected, nil
}

// RunSweeper runs the lease sweeper loop until ctx is done. It is a no-op when
// the sweeper is disabled in the configuration.
func (j *JobQueue) RunSweeper(ctx context.Context) error {
	if !j.config.SweeperEnabled {
		log.Println("queue: job sweeper disabled")
		return nil
	}

	interval := j.config.SweeperInterval
	if interval <= 0 {
		interval = time.Minute
	}

	logger := util.GetLogger(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("job queue sweeper stopped")
			return ctx.Err()
		case <-ticker.C:
			reclaimed, err := j.Sweep(ctx)
			if err != nil {
				logger.Errorf("queue: sweep failed: %v", err)
				continue
			}
			if reclaimed > 0 {
				logger.Infof("queue: sweeper reclaimed %d expired-lease job(s)", reclaimed)
			}
		}
	}
}

func (j *JobQueue) Worker(ctx context.Context, queues []string, types ...interface{}) error {
	// register all passed types in a type type registry.
	// this allows to map job types back to their corresponding go type
	// to execute the Perform() action.
	for _, t := range types {
		j.registerType(t)
	}

	tm := time.NewTicker(PollInterval)
	defer tm.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("job queue worker stopped")
			return ctx.Err()
		case <-tm.C:
			if err := j.Dequeue(ctx, queues); err != nil {
				log.Println("queue: dequeue failed", err)
			}
		}
	}
}

func (j *JobQueue) typeName(typedNil interface{}) string {
	name := reflect.TypeOf(typedNil).String()
	name = strings.TrimPrefix(name, "*")

	return name
}

func (j *JobQueue) registerType(typedNil interface{}) {
	t := reflect.TypeOf(typedNil).Elem()
	name := j.typeName(typedNil)

	j.registryMu.Lock()
	defer j.registryMu.Unlock()
	j.typeRegistry[name] = t
}

func (j *JobQueue) getType(name string) (Job, error) {
	j.registryMu.RLock()
	item, ok := j.typeRegistry[name]
	j.registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("type not found in type registry. did you register the job?")
	}

	return reflect.New(item).Elem().Interface().(Job), nil
}

func (j *JobQueue) registeredTypeNames() []string {
	j.registryMu.RLock()
	defer j.registryMu.RUnlock()
	return mapKeys(j.typeRegistry)
}

// pqArray and appendArrayQuotedBytes func extracted from https://github.com/lib/pq
// to remove dependency on lib/pq
func pqArray(a []string) (string, error) {
	if n := len(a); n > 0 {
		// There will be at least two curly brackets, 2*N bytes of quotes,
		// and N-1 bytes of delimiters.
		b := make([]byte, 1, 1+3*n)
		b[0] = '{'

		b = appendArrayQuotedBytes(b, []byte(a[0]))
		for i := 1; i < n; i++ {
			b = append(b, ',')
			b = appendArrayQuotedBytes(b, []byte(a[i]))
		}

		return string(append(b, '}')), nil
	}

	return "{}", nil
}

func appendArrayQuotedBytes(b, v []byte) []byte {
	b = append(b, '"')
	for {
		i := bytes.IndexAny(v, `"\`)
		if i < 0 {
			b = append(b, v...)
			break
		}
		if i > 0 {
			b = append(b, v[:i]...)
		}
		b = append(b, '\\', v[i])
		v = v[i+1:]
	}
	return append(b, '"')
}

func mapKeys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
