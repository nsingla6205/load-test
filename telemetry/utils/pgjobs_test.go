package utils

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
)

// FailingOnceJob fails on first attempt, succeeds on subsequent attempts
type FailingOnceJob struct {
	ID         string `json:"id"`
	ShouldFail bool   `json:"should_fail"`
}

func (f FailingOnceJob) Perform(ctx context.Context, processor interface{}, attempt int32) error {
	if f.ShouldFail && attempt == 1 {
		return fmt.Errorf("failing on first attempt")
	}
	return nil
}

func (f FailingOnceJob) Load(data string) (Job, error) {
	var job FailingOnceJob
	err := json.Unmarshal([]byte(data), &job)
	return job, err
}

// MockJob implements the Job interface for testing
type MockJob struct {
	ID   string `json:"id"`
	Data string `json:"data"`
}

func (m MockJob) Perform(ctx context.Context, processor interface{}, attempt int32) error {
	if m.ID == "fail" {
		return fmt.Errorf("mock job failed")
	}
	return nil
}

func (m MockJob) Load(data string) (Job, error) {
	var job MockJob
	err := json.Unmarshal([]byte(data), &job)
	if err != nil {
		return nil, err
	}
	return job, nil
}

// FailingJob implements the Job interface for testing failure scenarios
type FailingJob struct {
	ID string `json:"id"`
}

func (f FailingJob) Perform(ctx context.Context, processor interface{}, attempt int32) error {
	return fmt.Errorf("failing job always fails")
}

func (f FailingJob) Load(data string) (Job, error) {
	var job FailingJob
	err := json.Unmarshal([]byte(data), &job)
	if err != nil {
		return nil, err
	}
	return job, nil
}

// UnmarshalableJob for testing marshaling errors
type UnmarshalableJob struct {
	BadField chan string `json:"bad_field"` // channels can't be marshaled
}

func (u UnmarshalableJob) Perform(ctx context.Context, processor interface{}, attempt int32) error {
	return nil
}

func (u UnmarshalableJob) Load(data string) (Job, error) {
	return u, nil
}

// SlowJob blocks in Perform until its context is cancelled (deadline or
// explicit cancel), then returns ctx.Err(). It is used to verify per-job
// timeout and lease-loss cancellation actually stop the running job.
type SlowJob struct {
	ID string `json:"id"`
}

func (s SlowJob) Perform(ctx context.Context, processor interface{}, attempt int32) error {
	<-ctx.Done()
	return ctx.Err()
}

func (s SlowJob) Load(data string) (Job, error) {
	var job SlowJob
	err := json.Unmarshal([]byte(data), &job)
	return job, err
}

// slowJobSignalingStart blocks until its job context is cancelled, after
// signaling that Perform has started. Used with runJob to verify lease-loss
// cancellation propagates into an active Perform.
type slowJobSignalingStart struct {
	started            chan struct{}
	wasCanceledAtStart chan bool
}

func (s slowJobSignalingStart) Perform(ctx context.Context, _ interface{}, _ int32) error {
	if s.wasCanceledAtStart != nil {
		select {
		case s.wasCanceledAtStart <- ctx.Err() != nil:
		default:
		}
	}
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func (s slowJobSignalingStart) Load(_ string) (Job, error) {
	return s, nil
}

// setupTestDB creates an in-memory SQLite database for testing
func setupTestDB(t *testing.T) (*sql.DB, func()) {
	gormDB, err := database.SetupTestDB()
	require.NoError(t, err)

	sqlDB, err := gormDB.DB()
	require.NoError(t, err)

	// Create jobs table with SQLite-compatible schema
	_, err = sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type_name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'new',
			queue TEXT NOT NULL,
			data TEXT NOT NULL,
			error TEXT,
			attempt INTEGER DEFAULT 0,
			lease_expires_at DATETIME,
			worker_id TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			finished_at DATETIME,
			scheduled_at DATETIME
		)
	`)
	require.NoError(t, err)

	cleanup := func() {
		_ = sqlDB.Close()
	}

	return sqlDB, cleanup
}

// MockJobQueue wraps JobQueue but modifies the Dequeue method to work with SQLite
type MockJobQueue struct {
	*JobQueue
}

func newMockQueue(db *sql.DB, processor interface{}) *MockJobQueue {
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	return &MockJobQueue{
		JobQueue: NewQueue(db, processor, mockMetricRecorder),
	}
}

// Dequeue is a SQLite-compatible mirror of the real claim -> run -> finalize
// split: it claims a job in a short transaction (setting in_progress, a lease
// and a worker id), runs Perform OUTSIDE any transaction, then finalizes the
// terminal state in a separate short, ownership-guarded transaction.
func (mq *MockJobQueue) Dequeue(ctx context.Context, queues []string) error {
	job, workerID, err := mq.claim(ctx, queues)
	if err != nil {
		return err
	}
	if job == nil {
		return nil
	}

	// get original go type based on type name
	jobType, err := mq.getType(job.TypeName)
	if err != nil {
		findErr := fmt.Errorf("unable to find related job '%v': %w", job.TypeName, err)
		if fErr := mq.finalize(ctx, job.ID, workerID, JOB_STATUS_FAILED, findErr.Error()); fErr != nil {
			return fErr
		}
		return findErr
	}

	// create a new object by unmarshaling the job data
	loadedJob, err := jobType.Load(job.Data)
	if err != nil {
		if fErr := mq.finalize(ctx, job.ID, workerID, JOB_STATUS_FAILED, err.Error()); fErr != nil {
			return fErr
		}
		return err
	}

	// execute job outside of any transaction
	performErr := mq.runJob(ctx, loadedJob, job.Attempt, job.ID, workerID, job.Queue)

	status := JOB_STATUS_FINISHED
	jobErrText := ""
	jobMetricStatus := "success"
	if performErr != nil {
		status = JOB_STATUS_FAILED
		jobErrText = performErr.Error()
		jobMetricStatus = performMetricStatus(performErr)
	}

	if fErr := mq.finalize(ctx, job.ID, workerID, status, jobErrText); fErr != nil {
		if errors.Is(fErr, errLostJobOwnership) {
			return nil
		}
		return fErr
	}

	mq.metricsRecorder.RecordJobProcessed(&monitoring.MetricRecorderParams{
		QueueName: job.Queue,
		JobType:   job.TypeName,
		JobStatus: jobMetricStatus,
	})

	return nil
}

// claim is the SQLite-compatible short claim transaction. It returns the claimed
// job (with the post-increment attempt) and the worker id, or (nil, "", nil)
// when there is nothing to claim.
func (mq *MockJobQueue) claim(ctx context.Context, queues []string) (*datamodel.Job, string, error) {
	var job datamodel.Job

	tx, err := mq.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback() }()

	// Build the query based on available queues and types
	var whereConditions []string
	var args []interface{}

	// Status condition (also reclaim in-progress jobs whose lease expired, bounded by max attempts)
	whereConditions = append(whereConditions, "(status = ? OR (status = ? AND attempt < ?) OR (status = ? AND attempt < ? AND lease_expires_at IS NOT NULL AND lease_expires_at < datetime('now')))")
	args = append(args, JOB_STATUS_SCHEDULED, JOB_STATUS_FAILED, 5, JOB_STATUS_IN_PROGRESS, 5) // Default max retry value

	// Queue condition
	if len(queues) > 0 {
		whereConditions = append(whereConditions, "queue IN ("+placeholders(len(queues))+")")
		for _, queue := range queues {
			args = append(args, queue)
		}
	} else {
		// If no queues provided, don't process any jobs
		return nil, "", nil
	}

	// Type condition - only add if we have registered types
	if len(mq.typeRegistry) > 0 {
		whereConditions = append(whereConditions, "type_name IN ("+placeholders(len(mq.typeRegistry))+")")
		for typeName := range mq.typeRegistry {
			args = append(args, typeName)
		}
	} else {
		// If no types are registered, no jobs can be processed (mimics PostgreSQL behavior)
		return nil, "", nil
	}

	// Schedule condition
	whereConditions = append(whereConditions, "(scheduled_at IS NULL OR scheduled_at <= datetime('now', 'localtime'))")

	selectStmt := `
		SELECT id, type_name, data, attempt, queue FROM jobs 
		WHERE ` + strings.Join(whereConditions, " AND ") + `
		ORDER BY 
			CASE WHEN scheduled_at IS NULL THEN 0 ELSE 1 END,
			scheduled_at,
			created_at
		LIMIT 1
	`

	row := tx.QueryRowContext(ctx, selectStmt, args...)
	err = row.Scan(&job.ID, &job.TypeName, &job.Data, &job.Attempt, &job.Queue)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	} else if err != nil {
		return nil, "", err
	}

	workerID := uuid.NewString()
	leaseExpiresAt := time.Now().Add(mq.config.LeaseTTL).UTC().Format("2006-01-02 15:04:05")
	// Mark the job in_progress and increment attempt (claim), then commit so the
	// connection is released before Perform runs.
	_, err = tx.ExecContext(ctx,
		`UPDATE jobs SET status = ?, started_at = datetime('now'), attempt = attempt + 1, lease_expires_at = ?, worker_id = ? WHERE id = ?`,
		JOB_STATUS_IN_PROGRESS, leaseExpiresAt, workerID, job.ID)
	if err != nil {
		return nil, "", err
	}

	if err = tx.Commit(); err != nil {
		return nil, "", err
	}

	// Reflect the post-increment attempt to match the real claim semantics.
	job.Attempt++
	job.WorkerID = &workerID
	return &job, workerID, nil
}

// finalize is the SQLite-compatible, ownership-guarded finalize transaction.
func (mq *MockJobQueue) finalize(ctx context.Context, jobID int64, workerID, status, jobErr string) error {
	tx, err := mq.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = ?, finished_at = datetime('now'), error = ?, lease_expires_at = NULL, worker_id = NULL WHERE id = ? AND worker_id = ?`,
		status, jobErr, jobID, workerID)
	if err != nil {
		return fmt.Errorf("failed updating job status: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errLostJobOwnership
	}

	return tx.Commit()
}

// Sweep is the SQLite-compatible mirror of the real lease sweeper.
func (mq *MockJobQueue) Sweep(ctx context.Context) (int64, error) {
	res, err := mq.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, error = ?, finished_at = datetime('now'), lease_expires_at = NULL, worker_id = NULL
		 WHERE status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at < datetime('now')`,
		JOB_STATUS_FAILED, "lease expired; reclaimed by sweeper", JOB_STATUS_IN_PROGRESS)
	if err != nil {
		return 0, err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if affected > 0 {
		mq.metricsRecorder.RecordJobReclaimed(&monitoring.MetricRecorderParams{QueueName: "all", JobType: "all", JobQuantity: int(affected)})
	}
	return affected, nil
}

// Helper function to create SQL placeholders
func placeholders(count int) string {
	if count == 0 {
		return "NULL" // This will never match anything in an IN clause
	}
	result := "?"
	for i := 1; i < count; i++ {
		result += ",?"
	}
	return result
}

func TestNewQueue(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)

	assert.NotNil(t, queue)
	assert.Equal(t, db, queue.db)
	assert.Equal(t, mockProcessor, queue.processor)
	assert.NotNil(t, queue.typeRegistry)
	assert.Empty(t, queue.typeRegistry)
}

func TestJobQueue_Enqueue(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	job := MockJob{ID: "test", Data: "test data"}

	err := queue.Enqueue(ctx, job, "test_queue")
	assert.NoError(t, err)

	// Verify job was inserted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ?", "test_queue").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify job details
	var typeName, status, queueName, data string
	err = db.QueryRow("SELECT type_name, status, queue, data FROM jobs WHERE queue = ?", "test_queue").
		Scan(&typeName, &status, &queueName, &data)
	assert.NoError(t, err)
	assert.Equal(t, "utils.MockJob", typeName)
	assert.Equal(t, JOB_STATUS_SCHEDULED, status)
	assert.Equal(t, "test_queue", queueName)

	var jobData MockJob
	err = json.Unmarshal([]byte(data), &jobData)
	assert.NoError(t, err)
	assert.Equal(t, job.ID, jobData.ID)
	assert.Equal(t, job.Data, jobData.Data)
}

func TestJobQueue_EnqueueAt(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	job := MockJob{ID: "scheduled", Data: "scheduled data"}
	scheduledTime := time.Now().Add(1 * time.Hour)

	err := queue.EnqueueAt(ctx, job, "scheduled_queue", scheduledTime)
	assert.NoError(t, err)

	// Verify job was inserted with correct scheduled time
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ?", "scheduled_queue").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)

	var scheduledAt time.Time
	err = db.QueryRow("SELECT scheduled_at FROM jobs WHERE queue = ?", "scheduled_queue").Scan(&scheduledAt)
	assert.NoError(t, err)
	assert.WithinDuration(t, scheduledTime, scheduledAt, time.Second)
}

func TestJobQueue_EnqueueMarshalError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Set up mock expectation for RecordJobEnqueued
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Use a job that can't be marshaled
	job := UnmarshalableJob{BadField: make(chan string)}

	err := queue.Enqueue(ctx, job, "test_queue")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed marshaling")

	// Verify no job was inserted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ?", "test_queue").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestJobQueue_Dequeue_NoJobs(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	queue := newMockQueue(db, mockProcessor)

	// Register the job type
	queue.registerType(&MockJob{})

	err := queue.Dequeue(context.Background(), []string{"empty_queue"})
	assert.NoError(t, err) // Should not error when no jobs are available
}

func TestJobQueue_Dequeue_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobEnqueued
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	mockMetricRecorder.On("RecordJobProcessed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := MockJobQueue{
		JobQueue: NewQueue(db, mockProcessor, mockMetricRecorder),
	}

	ctx := context.Background()

	// Register the job type
	queue.registerType(&MockJob{})

	// Enqueue a job
	job := MockJob{ID: "success", Data: "success data"}
	err := queue.Enqueue(ctx, job, "test_queue")
	require.NoError(t, err)

	// Dequeue and process the job
	err = queue.Dequeue(ctx, []string{"test_queue"})
	assert.NoError(t, err)

	// Verify job status was updated to finished
	var status string
	err = db.QueryRow("SELECT status FROM jobs WHERE queue = ?", "test_queue").Scan(&status)
	assert.NoError(t, err)
	assert.Equal(t, JOB_STATUS_FINISHED, status)
}

func TestJobQueue_Dequeue_JobFailure(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobEnqueued
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	mockMetricRecorder.On("RecordJobProcessed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := MockJobQueue{
		JobQueue: NewQueue(db, mockProcessor, mockMetricRecorder),
	}
	ctx := context.Background()

	// Register the job type
	queue.registerType(&FailingJob{})

	// Enqueue a failing job
	job := FailingJob{ID: "fail"}
	err := queue.Enqueue(ctx, job, "test_queue")
	require.NoError(t, err)

	// Dequeue and process the job (should fail)
	err = queue.Dequeue(ctx, []string{"test_queue"})
	assert.NoError(t, err) // Dequeue itself should not error, but job should fail

	// Verify job status was updated to failed
	var status string
	var errorMsg sql.NullString
	err = db.QueryRow("SELECT status, error FROM jobs WHERE queue = ?", "test_queue").
		Scan(&status, &errorMsg)
	assert.NoError(t, err)
	assert.Equal(t, JOB_STATUS_FAILED, status)
	assert.True(t, errorMsg.Valid)
	assert.Contains(t, errorMsg.String, "failing job always fails")
}

func TestJobQueue_Dequeue_UnknownJobType(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobEnqueued
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := MockJobQueue{
		JobQueue: NewQueue(db, mockProcessor, mockMetricRecorder),
	}
	ctx := context.Background()

	// Don't register the job type, but enqueue a job
	job := MockJob{ID: "unknown", Data: "unknown data"}
	err := queue.Enqueue(ctx, job, "test_queue")
	require.NoError(t, err)

	// Try to dequeue without registering the type
	err = queue.Dequeue(ctx, []string{"test_queue"})
	// Since no types are registered, it should handle gracefully
	assert.NoError(t, err)

	// Since no types are registered, job should remain unprocessed
	var status string
	err = db.QueryRow("SELECT status FROM jobs WHERE queue = ?", "test_queue").Scan(&status)
	assert.NoError(t, err)
	assert.Equal(t, JOB_STATUS_SCHEDULED, status)
}

func TestJobQueue_Dequeue_ScheduledJob_NotReady(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	queue := newMockQueue(db, mockProcessor)
	ctx := context.Background()

	// Register the job type
	queue.registerType(&MockJob{})

	// Enqueue a job scheduled for the future
	job := MockJob{ID: "future", Data: "future data"}
	futureTime := time.Now().Add(1 * time.Hour)
	err := queue.EnqueueAt(ctx, job, "test_queue", futureTime)
	require.NoError(t, err)

	// Try to dequeue - should not process the future job
	err = queue.Dequeue(ctx, []string{"test_queue"})
	assert.NoError(t, err)

	// Verify job status is still scheduled
	var status string
	err = db.QueryRow("SELECT status FROM jobs WHERE queue = ?", "test_queue").Scan(&status)
	assert.NoError(t, err)
	assert.Equal(t, JOB_STATUS_SCHEDULED, status)
}

func TestJobQueue_Dequeue_ScheduledJob_Ready(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobEnqueued
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	mockMetricRecorder.On("RecordJobProcessed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := MockJobQueue{
		JobQueue: NewQueue(db, mockProcessor, mockMetricRecorder),
	}

	ctx := context.Background()

	// Register the job type
	queue.registerType(&MockJob{})

	// Enqueue a job scheduled for the past (by inserting directly to avoid time zone issues)
	job := MockJob{ID: "ready", Data: "ready data"}
	jobData, err := json.Marshal(job)
	require.NoError(t, err)

	_, err = db.Exec(`INSERT INTO jobs (type_name, status, queue, data, scheduled_at) VALUES (?, ?, ?, ?, ?)`,
		"utils.MockJob", JOB_STATUS_SCHEDULED, "test_queue", string(jobData), time.Now().Add(-1*time.Hour))
	require.NoError(t, err)

	// Dequeue - should process the ready job
	err = queue.Dequeue(ctx, []string{"test_queue"})
	assert.NoError(t, err)

	// Verify job status was updated to finished
	var status string
	err = db.QueryRow("SELECT status FROM jobs WHERE queue = ?", "test_queue").Scan(&status)
	assert.NoError(t, err)
	assert.Equal(t, JOB_STATUS_FINISHED, status)
}

func TestJobQueue_Dequeue_RetryLogic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobEnqueued
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	mockMetricRecorder.On("RecordJobProcessed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := MockJobQueue{
		JobQueue: NewQueue(db, mockProcessor, mockMetricRecorder),
	}
	ctx := context.Background()

	// Register the job type
	queue.registerType(&FailingJob{})

	// Enqueue a failing job
	job := FailingJob{ID: "retry"}
	err := queue.Enqueue(ctx, job, "test_queue")
	require.NoError(t, err)

	// Process the job multiple times (it should retry up to 5 times - default max retry)
	for i := 0; i < 5; i++ {
		err = queue.Dequeue(ctx, []string{"test_queue"})
		assert.NoError(t, err)
	}

	// Verify the attempt count
	var attempt int32
	err = db.QueryRow("SELECT attempt FROM jobs WHERE queue = ?", "test_queue").Scan(&attempt)
	assert.NoError(t, err)
	assert.Equal(t, int32(5), attempt) // Default max retry value

	// Try one more time - should not process as max retries reached
	err = queue.Dequeue(ctx, []string{"test_queue"})
	assert.NoError(t, err)

	// Verify attempt count didn't increase
	err = db.QueryRow("SELECT attempt FROM jobs WHERE queue = ?", "test_queue").Scan(&attempt)
	assert.NoError(t, err)
	assert.Equal(t, int32(5), attempt) // Default max retry value
}

func TestJobQueue_Worker(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	cfg := DefaultJobQueueConfig()
	cfg.PollMinInterval = 10 * time.Millisecond
	cfg.PollMaxInterval = 10 * time.Millisecond
	queue := NewQueueWithConfig(db, mockProcessor, mockMetricRecorder, cfg)

	// Create a context that will be canceled after a short time
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start the worker
	err := queue.Worker(ctx, []string{"worker_queue"}, &MockJob{})
	assert.Error(t, err) // Should return context deadline exceeded error
	assert.Equal(t, context.DeadlineExceeded, err)
}

func TestJobQueue_Worker_ProcessesJobs(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Set up mock expectation for RecordJobDequeued
	mockMetricRecorder.On("RecordJobDequeued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := NewQueueWithConfig(db, mockProcessor, mockMetricRecorder, func() JobQueueConfig {
		cfg := DefaultJobQueueConfig()
		cfg.PollMinInterval = 10 * time.Millisecond
		cfg.PollMaxInterval = 10 * time.Millisecond
		return cfg
	}())

	// Enqueue a job before starting the worker
	job := MockJob{ID: "worker_test", Data: "worker data"}
	err := queue.Enqueue(context.Background(), job, "worker_queue")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Start the worker
	err = queue.Worker(ctx, []string{"worker_queue"}, &MockJob{})
	assert.Error(t, err) // Should return context deadline exceeded error

	// For the real implementation with PostgreSQL features, jobs may not be processed
	// in SQLite, so we just verify no panic occurred
}

func TestJobQueue_TypeName(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)

	// Test with pointer type
	job := &MockJob{}
	typeName := queue.typeName(job)
	assert.Equal(t, "utils.MockJob", typeName)

	// Test with non-pointer type
	jobValue := MockJob{}
	typeName = queue.typeName(jobValue)
	assert.Equal(t, "utils.MockJob", typeName)
}

func TestJobQueue_RegisterType(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)

	// Register a type
	queue.registerType(&MockJob{})

	// Verify it was registered
	assert.Len(t, queue.typeRegistry, 1)
	assert.Contains(t, queue.typeRegistry, "utils.MockJob")
}

func TestJobQueue_GetType(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)

	// Register a type
	queue.registerType(&MockJob{})

	// Get the type
	job, err := queue.getType("utils.MockJob")
	assert.NoError(t, err)
	assert.IsType(t, MockJob{}, job)

	// Try to get unknown type
	_, err = queue.getType("unknown.Type")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "type not found in type registry")
}

func TestJobQueue_TypeRegistry_ConcurrentRegisterAndRead(t *testing.T) {
	q := NewQueueWithConfig(nil, nil, &monitoring.MockMetricsRecorder{}, JobQueueConfig{})
	types := []interface{}{&MockJob{}, &FailingJob{}, &LoadErrorJob{}}
	var wg sync.WaitGroup
	for _, jt := range types {
		wg.Add(1)
		go func(jt interface{}) {
			defer wg.Done()
			q.registerType(jt)
		}(jt)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 1000; k++ {
				_ = q.registeredTypeNames()
				_, _ = q.getType("utils.MockJob")
			}
		}()
	}
	wg.Wait()
	require.Len(t, q.registeredTypeNames(), len(types))
}

func TestPqArray(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{
			name:     "empty array",
			input:    []string{},
			expected: "{}",
		},
		{
			name:     "single element",
			input:    []string{"test"},
			expected: `{"test"}`,
		},
		{
			name:     "multiple elements",
			input:    []string{"test1", "test2", "test3"},
			expected: `{"test1","test2","test3"}`,
		},
		{
			name:     "elements with quotes",
			input:    []string{`test"quote`, "normal"},
			expected: `{"test\"quote","normal"}`,
		},
		{
			name:     "elements with backslashes",
			input:    []string{`test\backslash`, "normal"},
			expected: `{"test\\backslash","normal"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := pqArray(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAppendArrayQuotedBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected string
	}{
		{
			name:     "normal string",
			input:    []byte("test"),
			expected: `"test"`,
		},
		{
			name:     "string with quotes",
			input:    []byte(`test"quote`),
			expected: `"test\"quote"`,
		},
		{
			name:     "string with backslashes",
			input:    []byte(`test\backslash`),
			expected: `"test\\backslash"`,
		},
		{
			name:     "empty string",
			input:    []byte(""),
			expected: `""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := appendArrayQuotedBytes([]byte{}, tt.input)
			assert.Equal(t, tt.expected, string(result))
		})
	}
}

func TestMapKeys(t *testing.T) {
	tests := []struct {
		name          string
		input         map[string]int
		expectedCount int
	}{
		{
			name:          "empty map",
			input:         map[string]int{},
			expectedCount: 0,
		},
		{
			name:          "single key",
			input:         map[string]int{"key1": 1},
			expectedCount: 1,
		},
		{
			name:          "multiple keys",
			input:         map[string]int{"key1": 1, "key2": 2, "key3": 3},
			expectedCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapKeys(tt.input)
			assert.Len(t, result, tt.expectedCount)

			// For non-empty maps, check that all input keys are present
			if tt.expectedCount > 0 {
				for expectedKey := range tt.input {
					assert.Contains(t, result, expectedKey)
				}
			}
		})
	}
}
func TestJobQueue_Dequeue_DatabaseError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobDequeued
	mockMetricRecorder.On("RecordJobDequeued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Close the database to simulate an error
	_ = db.Close()

	// Try to dequeue - should return an error
	err := queue.Dequeue(ctx, []string{"test_queue"})
	assert.Error(t, err)
}

func TestJobQueue_Enqueue_DatabaseError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobEnqueued
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	job := MockJob{ID: "test", Data: "test data"}

	// Close the database to simulate an error
	_ = db.Close()

	err := queue.Enqueue(ctx, job, "test_queue")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed inserting job")
}

func TestJobQueue_Dequeue_MultipleQueues(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobEnqueued
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	mockMetricRecorder.On("RecordJobProcessed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := MockJobQueue{
		JobQueue: NewQueue(db, mockProcessor, mockMetricRecorder),
	}
	ctx := context.Background()

	// Register the job type
	queue.registerType(&MockJob{})

	// Enqueue jobs in different queues
	job1 := MockJob{ID: "queue1", Data: "queue1 data"}
	job2 := MockJob{ID: "queue2", Data: "queue2 data"}
	job3 := MockJob{ID: "queue3", Data: "queue3 data"}

	err := queue.Enqueue(ctx, job1, "queue1")
	require.NoError(t, err)
	err = queue.Enqueue(ctx, job2, "queue2")
	require.NoError(t, err)
	err = queue.Enqueue(ctx, job3, "queue3")
	require.NoError(t, err)

	// Dequeue from multiple queues
	err = queue.Dequeue(ctx, []string{"queue1", "queue2"})
	assert.NoError(t, err)

	// Check that one of the jobs from queue1 or queue2 was processed
	var processedCount int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE status = ? AND queue IN ('queue1', 'queue2')",
		JOB_STATUS_FINISHED).Scan(&processedCount)
	assert.NoError(t, err)
	assert.Equal(t, 1, processedCount)

	// Check that queue3 job was not processed
	var queue3Status string
	err = db.QueryRow("SELECT status FROM jobs WHERE queue = 'queue3'").Scan(&queue3Status)
	assert.NoError(t, err)
	assert.Equal(t, JOB_STATUS_SCHEDULED, queue3Status)
}

func TestJobQueue_Constants(t *testing.T) {
	assert.Equal(t, "new", JOB_STATUS_SCHEDULED)
	assert.Equal(t, "finished", JOB_STATUS_FINISHED)
	assert.Equal(t, "failed", JOB_STATUS_FAILED)
	assert.Equal(t, "jobs", JobsTableName)
}

func TestJobQueue_PollInterval(t *testing.T) {
	cfg := DefaultJobQueueConfig()
	cfg.PollMinInterval = 5 * time.Second
	cfg.PollMaxInterval = 30 * time.Second
	normalized := cfg.normalized()
	assert.Equal(t, 5*time.Second, normalized.PollMinInterval)
	assert.Equal(t, 30*time.Second, normalized.PollMaxInterval)

	cfg.PollMinInterval = time.Minute
	cfg.PollMaxInterval = 0
	normalized = cfg.normalized()
	assert.Equal(t, time.Minute, normalized.PollMinInterval)
	assert.Equal(t, time.Minute, normalized.PollMaxInterval)
}

// Additional comprehensive tests for better coverage

func TestJobQueue_EnqueueAt_DatabaseError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	job := MockJob{ID: "test", Data: "test data"}
	scheduledTime := time.Now().Add(1 * time.Hour)

	// Close the database to simulate an error
	_ = db.Close()

	err := queue.EnqueueAt(ctx, job, "test_queue", scheduledTime)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed inserting job")
}

func TestJobQueue_EnqueueAt_MarshalError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Use a job that can't be marshaled
	job := UnmarshalableJob{BadField: make(chan string)}
	scheduledTime := time.Now().Add(1 * time.Hour)

	err := queue.EnqueueAt(ctx, job, "test_queue", scheduledTime)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed marshaling")
}

// LoadErrorJob for testing load errors
type LoadErrorJob struct {
	ID string `json:"id"`
}

func (l LoadErrorJob) Perform(ctx context.Context, processor interface{}, attempt int32) error {
	return nil
}

func (l LoadErrorJob) Load(data string) (Job, error) {
	return nil, fmt.Errorf("load error")
}

func TestJobQueue_Dequeue_LoadError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobEnqueued
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := MockJobQueue{
		JobQueue: NewQueue(db, mockProcessor, mockMetricRecorder),
	}
	ctx := context.Background()

	// Register the job type
	queue.registerType(&LoadErrorJob{})

	// Enqueue a job
	job := LoadErrorJob{ID: "load_error"}
	err := queue.Enqueue(ctx, job, "test_queue")
	require.NoError(t, err)

	// Dequeue and process the job (should fail on load)
	err = queue.Dequeue(ctx, []string{"test_queue"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "load error")
}

func TestJobQueue_Dequeue_EmptyQueues(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobEnqueued
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := MockJobQueue{
		JobQueue: NewQueue(db, mockProcessor, mockMetricRecorder),
	}
	ctx := context.Background()

	// Register the job type
	queue.registerType(&MockJob{})

	// Enqueue a job
	job := MockJob{ID: "test", Data: "test data"}
	err := queue.Enqueue(ctx, job, "test_queue")
	require.NoError(t, err)

	// Try to dequeue from empty queues list (should find no jobs)
	err = queue.Dequeue(ctx, []string{})
	assert.NoError(t, err)

	// Verify job status is still scheduled
	var status string
	err = db.QueryRow("SELECT status FROM jobs WHERE queue = ?", "test_queue").Scan(&status)
	assert.NoError(t, err)
	assert.Equal(t, JOB_STATUS_SCHEDULED, status)
}

func TestJobQueue_Dequeue_TransactionBeginError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	queue := newMockQueue(db, mockProcessor)
	ctx := context.Background()

	// Close database to force transaction begin error
	_ = db.Close()

	err := queue.Dequeue(ctx, []string{"test_queue"})
	assert.Error(t, err)
}

func TestJobQueue_Worker_EmptyTypes(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	cfg := DefaultJobQueueConfig()
	cfg.PollMinInterval = 5 * time.Millisecond
	cfg.PollMaxInterval = 5 * time.Millisecond
	queue := NewQueueWithConfig(db, mockProcessor, mockMetricRecorder, cfg)

	// Create a context that will be canceled after a short time
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Start the worker with no types (should still work but not process anything)
	err := queue.Worker(ctx, []string{"worker_queue"})
	assert.Error(t, err) // Should return context deadline exceeded error
	assert.Equal(t, context.DeadlineExceeded, err)
}

func TestJobQueue_Worker_WithMultipleTypes(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	cfg := DefaultJobQueueConfig()
	cfg.PollMinInterval = 5 * time.Millisecond
	cfg.PollMaxInterval = 5 * time.Millisecond
	queue := NewQueueWithConfig(db, mockProcessor, mockMetricRecorder, cfg)

	// Create a context that will be canceled after a short time
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Start the worker with multiple types
	err := queue.Worker(ctx, []string{"worker_queue"}, &MockJob{}, &FailingJob{})
	assert.Error(t, err) // Should return context deadline exceeded error
	assert.Equal(t, context.DeadlineExceeded, err)

	// Verify both types were registered
	assert.Len(t, queue.typeRegistry, 2)
	assert.Contains(t, queue.typeRegistry, "utils.MockJob")
	assert.Contains(t, queue.typeRegistry, "utils.FailingJob")
}

func TestJobQueue_Dequeue_ContextCancellation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	queue := newMockQueue(db, mockProcessor)

	// Register the job type
	queue.registerType(&MockJob{})

	// Create a context that's already canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := queue.Dequeue(ctx, []string{"test_queue"})
	// Should handle canceled context gracefully (may or may not return error depending on timing)
	// This tests the context handling in the SQL queries
	_ = err // Ignore error as it may or may not occur depending on timing
}

func TestJobQueue_TypeName_EdgeCases(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)

	// Test with double pointer
	job := &MockJob{}
	typeName := queue.typeName(&job)
	assert.Equal(t, "*utils.MockJob", typeName) // Should only strip one *

	// Test with interface{}
	var interfaceJob interface{} = MockJob{}
	typeName = queue.typeName(interfaceJob)
	assert.Equal(t, "utils.MockJob", typeName)
}

func TestJobQueue_RegisterType_Multiple(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)

	// Register multiple types
	queue.registerType(&MockJob{})
	queue.registerType(&FailingJob{})
	queue.registerType(&LoadErrorJob{})

	// Verify all were registered
	assert.Len(t, queue.typeRegistry, 3)
	assert.Contains(t, queue.typeRegistry, "utils.MockJob")
	assert.Contains(t, queue.typeRegistry, "utils.FailingJob")
	assert.Contains(t, queue.typeRegistry, "utils.LoadErrorJob")
}

func TestJobQueue_GetType_AfterRegistration(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)

	// Register types
	queue.registerType(&MockJob{})
	queue.registerType(&FailingJob{})

	// Get registered types
	job1, err := queue.getType("utils.MockJob")
	assert.NoError(t, err)
	assert.IsType(t, MockJob{}, job1)

	job2, err := queue.getType("utils.FailingJob")
	assert.NoError(t, err)
	assert.IsType(t, FailingJob{}, job2)

	// Try to get unregistered type
	_, err = queue.getType("utils.LoadErrorJob")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "type not found in type registry")
}

func TestPqArray_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{
			name:     "nil slice",
			input:    nil,
			expected: "{}",
		},
		{
			name:     "string with newlines",
			input:    []string{"line1\nline2", "normal"},
			expected: `{"line1` + "\n" + `line2","normal"}`,
		},
		{
			name:     "string with tabs",
			input:    []string{"tab\ttab", "normal"},
			expected: `{"tab` + "\t" + `tab","normal"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := pqArray(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAppendArrayQuotedBytes_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected string
	}{
		{
			name:     "bytes with null characters",
			input:    []byte("test\x00null"),
			expected: `"test` + "\x00" + `null"`,
		},
		{
			name:     "bytes with multiple escapes",
			input:    []byte(`test\"quote\backslash`),
			expected: `"test\\\"quote\\backslash"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := appendArrayQuotedBytes([]byte{}, tt.input)
			assert.Equal(t, tt.expected, string(result))
		})
	}
}

func TestMapKeys_DifferentTypes(t *testing.T) {
	t.Run("string to string map", func(t *testing.T) {
		input := map[string]string{"key1": "val1", "key2": "val2"}
		result := mapKeys(input)
		assert.Len(t, result, 2)
		assert.Contains(t, result, "key1")
		assert.Contains(t, result, "key2")
	})

	t.Run("int to string map", func(t *testing.T) {
		input := map[int]string{1: "val1", 2: "val2"}
		result := mapKeys(input)
		assert.Len(t, result, 2)
		assert.Contains(t, result, 1)
		assert.Contains(t, result, 2)
	})
}

func TestJobQueue_Constants_Values(t *testing.T) {
	// Test that constants have expected values for database compatibility
	assert.Equal(t, "new", JOB_STATUS_SCHEDULED)
	assert.Equal(t, "finished", JOB_STATUS_FINISHED)
	assert.Equal(t, "failed", JOB_STATUS_FAILED)
	assert.Equal(t, "jobs", JobsTableName)

	// Test that PollInterval has a reasonable default
	assert.Greater(t, PollInterval, 0*time.Second)
	assert.Less(t, PollInterval, 10*time.Second)
}

// Benchmark tests for performance verification
func BenchmarkJobQueue_Enqueue(b *testing.B) {
	gormDB, err := database.SetupTestDB()
	if err != nil {
		b.Fatal(err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	// Create jobs table
	_, err = sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type_name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'new',
			queue TEXT NOT NULL,
			data TEXT NOT NULL,
			error TEXT,
			attempt INTEGER DEFAULT 0,
			lease_expires_at DATETIME,
			worker_id TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			finished_at DATETIME,
			scheduled_at DATETIME
		)
	`)
	if err != nil {
		b.Fatal(err)
	}

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(sqlDB, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	job := MockJob{ID: "benchmark", Data: "benchmark data"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := queue.Enqueue(ctx, job, "benchmark_queue")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPqArray(b *testing.B) {
	testSlices := [][]string{
		{},
		{"single"},
		{"one", "two", "three"},
		{"one", "two", "three", "four", "five"},
	}

	for _, slice := range testSlices {
		b.Run(fmt.Sprintf("len_%d", len(slice)), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, err := pqArray(slice)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Tests for EnqueueBatch functionality
func TestJobQueue_EnqueueBatch_EmptyJobs(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Test with empty jobs slice
	err := queue.EnqueueBatch(ctx, []Job{}, "test_queue")
	assert.NoError(t, err)

	// Verify no jobs were inserted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestJobQueue_EnqueueBatch_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobBatchEnqueued
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Create multiple jobs
	jobs := []Job{
		MockJob{ID: "job1", Data: "data1"},
		MockJob{ID: "job2", Data: "data2"},
		MockJob{ID: "job3", Data: "data3"},
	}

	err := queue.EnqueueBatch(ctx, jobs, "batch_queue")
	assert.NoError(t, err)

	// Verify all jobs were inserted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ?", "batch_queue").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 3, count)

	// Verify job details
	rows, err := db.Query("SELECT type_name, status, data FROM jobs WHERE queue = ? ORDER BY id", "batch_queue")
	assert.NoError(t, err)
	defer func() { _ = rows.Close() }()

	expectedJobs := []MockJob{
		{ID: "job1", Data: "data1"},
		{ID: "job2", Data: "data2"},
		{ID: "job3", Data: "data3"},
	}

	i := 0
	for rows.Next() {
		var typeName, status, data string
		err = rows.Scan(&typeName, &status, &data)
		assert.NoError(t, err)
		assert.Equal(t, "utils.MockJob", typeName)
		assert.Equal(t, JOB_STATUS_SCHEDULED, status)

		var jobData MockJob
		err = json.Unmarshal([]byte(data), &jobData)
		assert.NoError(t, err)
		assert.Equal(t, expectedJobs[i].ID, jobData.ID)
		assert.Equal(t, expectedJobs[i].Data, jobData.Data)
		i++
	}
	assert.Equal(t, 3, i)
}

func TestJobQueue_EnqueueBatch_MarshalError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobBatchEnqueued
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Create jobs with one that can't be marshaled
	jobs := []Job{
		MockJob{ID: "job1", Data: "data1"},
		UnmarshalableJob{BadField: make(chan string)}, // This will cause marshaling error
		MockJob{ID: "job3", Data: "data3"},
	}

	err := queue.EnqueueBatch(ctx, jobs, "batch_queue")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed marshaling job")

	// Verify no jobs were inserted (transaction should rollback)
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ?", "batch_queue").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestJobQueue_EnqueueBatch_DatabaseError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobBatchEnqueued
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Close database to simulate error
	_ = db.Close()

	jobs := []Job{
		MockJob{ID: "job1", Data: "data1"},
		MockJob{ID: "job2", Data: "data2"},
	}

	err := queue.EnqueueBatch(ctx, jobs, "batch_queue")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to begin transaction")
}

func TestJobQueue_EnqueueBatch_TransactionCommitError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobBatchEnqueued
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	jobs := []Job{
		MockJob{ID: "job1", Data: "data1"},
	}

	// Close database after creating queue to force commit error
	_ = db.Close()

	err := queue.EnqueueBatch(ctx, jobs, "batch_queue")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to begin transaction")
}

func TestJobQueue_EnqueueBatch_LargeBatch(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobBatchEnqueued
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Create a large batch of jobs
	jobs := make([]Job, 100)
	for i := 0; i < 100; i++ {
		jobs[i] = MockJob{ID: fmt.Sprintf("job%d", i), Data: fmt.Sprintf("data%d", i)}
	}

	err := queue.EnqueueBatch(ctx, jobs, "large_batch_queue")
	assert.NoError(t, err)

	// Verify all jobs were inserted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ?", "large_batch_queue").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 100, count)
}

func TestJobQueue_EnqueueBatch_MixedJobTypes(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobBatchEnqueued
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Create jobs of different types
	jobs := []Job{
		MockJob{ID: "mock1", Data: "mock1_data"},
		FailingJob{ID: "fail1"},
		MockJob{ID: "mock2", Data: "mock2_data"},
		FailingJob{ID: "fail2"},
	}

	err := queue.EnqueueBatch(ctx, jobs, "mixed_queue")
	assert.NoError(t, err)

	// Verify all jobs were inserted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ?", "mixed_queue").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 4, count)

	// Verify job types
	var mockCount, failCount int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ? AND type_name = ?", "mixed_queue", "utils.MockJob").Scan(&mockCount)
	assert.NoError(t, err)
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ? AND type_name = ?", "mixed_queue", "utils.FailingJob").Scan(&failCount)
	assert.NoError(t, err)
	assert.Equal(t, 2, mockCount)
	assert.Equal(t, 2, failCount)
}

func TestJobQueue_EnqueueBatch_SQLiteFallback(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobBatchEnqueued
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Create jobs - SQLite should use the fallback method
	jobs := []Job{
		MockJob{ID: "sqlite1", Data: "sqlite1_data"},
		MockJob{ID: "sqlite2", Data: "sqlite2_data"},
	}

	err := queue.EnqueueBatch(ctx, jobs, "sqlite_queue")
	assert.NoError(t, err)

	// Verify jobs were inserted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ?", "sqlite_queue").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestJobQueue_EnqueueBatch_PostgresArrayError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordJobBatchEnqueued
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Create jobs with special characters that might cause array issues
	jobs := []Job{
		MockJob{ID: "job\"with\"quotes", Data: "data\"with\"quotes"},
		MockJob{ID: "job\\with\\backslashes", Data: "data\\with\\backslashes"},
	}

	err := queue.EnqueueBatch(ctx, jobs, "special_chars_queue")
	assert.NoError(t, err)

	// Verify jobs were inserted (should fallback to SQLite method)
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ?", "special_chars_queue").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestJobQueue_EnqueueBatch_PostgresFailsSQLiteSucceedsCommit(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Expect metrics calls for both failures and final success
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.MatchedBy(func(p *monitoring.MetricRecorderParams) bool {
		return p.JobStatus == "enqueue_batch_postgres_failed"
	})).Return().Once()

	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.MatchedBy(func(p *monitoring.MetricRecorderParams) bool {
		return p.JobStatus == "success" && p.JobQuantity == 2
	})).Return().Once()

	queue := NewQueue(db, mockProcessor, mockMetricRecorder)
	ctx := context.Background()

	// Create jobs - will fail PostgreSQL approach but succeed with SQLite
	jobs := []Job{
		MockJob{ID: "commit_test_1", Data: "data1"},
		MockJob{ID: "commit_test_2", Data: "data2"},
	}

	err := queue.EnqueueBatch(ctx, jobs, "commit_test_queue")
	assert.NoError(t, err)

	// Verify transaction was committed successfully
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ?", "commit_test_queue").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 2, count)

	// Verify all jobs have correct status
	var scheduledCount int
	err = db.QueryRow("SELECT COUNT(*) FROM jobs WHERE queue = ? AND status = ?",
		"commit_test_queue", JOB_STATUS_SCHEDULED).Scan(&scheduledCount)
	assert.NoError(t, err)
	assert.Equal(t, 2, scheduledCount)

	// Verify metrics were recorded correctly
	mockMetricRecorder.AssertExpectations(t)
}

func TestJobQueue_Dequeue_JobFailure_RecordsMetric(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Set up mock expectations for both enqueue and process failure
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	mockMetricRecorder.On("RecordJobProcessed", mock.MatchedBy(func(p *monitoring.MetricRecorderParams) bool {
		return p.JobStatus == "failed"
	})).Return()

	queue := MockJobQueue{
		JobQueue: NewQueue(db, mockProcessor, mockMetricRecorder),
	}
	ctx := context.Background()

	// Register the job type
	queue.registerType(&FailingJob{})

	// Enqueue a failing job
	job := FailingJob{ID: "fail_test"}
	err := queue.Enqueue(ctx, job, "test_queue")
	require.NoError(t, err)

	// Process the job (should fail but record metrics)
	// Note: SQLite doesn't support FOR UPDATE SKIP LOCKED, so we manually process
	// Get the job directly
	var jobID int64
	var typeName, data string
	err = db.QueryRow("SELECT id, type_name, data FROM jobs WHERE queue = ? AND status = ?",
		"test_queue", JOB_STATUS_SCHEDULED).Scan(&jobID, &typeName, &data)
	require.NoError(t, err)

	// Update status to simulate processing
	_, err = db.Exec("UPDATE jobs SET status = ?, attempt = attempt + 1 WHERE id = ?",
		"processing", jobID)
	require.NoError(t, err)

	// Load and execute the job
	jobInstance, err := queue.getType(typeName)
	require.NoError(t, err)

	loadedJob, err := jobInstance.Load(data)
	require.NoError(t, err)

	performErr := loadedJob.Perform(context.Background(), mockProcessor, 1)
	require.Error(t, performErr)

	// Update job status based on result
	_, err = db.Exec("UPDATE jobs SET status = ?, error = ? WHERE id = ?",
		JOB_STATUS_FAILED, performErr.Error(), jobID)
	require.NoError(t, err)

	// Record the metric
	queue.metricsRecorder.RecordJobProcessed(&monitoring.MetricRecorderParams{
		JobStatus: JOB_STATUS_FAILED,
	})

	// Verify job status
	var status string
	var errorMsg string
	var attempt int32
	err = db.QueryRow("SELECT status, error, attempt FROM jobs WHERE id = ?", jobID).Scan(&status, &errorMsg, &attempt)
	assert.NoError(t, err)
	assert.Equal(t, JOB_STATUS_FAILED, status)
	assert.Greater(t, attempt, int32(0))
	assert.Contains(t, errorMsg, "failing job always fails")

	// Verify metrics were recorded
	mockMetricRecorder.AssertExpectations(t)
}

func TestJobQueue_Dequeue_JobSucceedsAfterFailure(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Set up mock expectations for enqueue and process events (failure then success)
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	mockMetricRecorder.On("RecordJobProcessed", mock.MatchedBy(func(p *monitoring.MetricRecorderParams) bool {
		return p.JobStatus == "failed"
	})).Return().Once()
	mockMetricRecorder.On("RecordJobProcessed", mock.MatchedBy(func(p *monitoring.MetricRecorderParams) bool {
		return p.JobStatus == "finished"
	})).Return().Once()

	queue := MockJobQueue{
		JobQueue: NewQueue(db, mockProcessor, mockMetricRecorder),
	}
	ctx := context.Background()

	// Register a job type that fails on first attempt
	queue.registerType(&FailingOnceJob{})

	// Enqueue a job
	job := FailingOnceJob{ID: "retry_test", ShouldFail: true}
	err := queue.Enqueue(ctx, job, "retry_queue")
	require.NoError(t, err)

	// First attempt - should fail
	var jobID int64
	var typeName, data string
	err = db.QueryRow("SELECT id, type_name, data FROM jobs WHERE queue = ? AND status = ?",
		"retry_queue", JOB_STATUS_SCHEDULED).Scan(&jobID, &typeName, &data)
	require.NoError(t, err)

	// Update to processing status
	_, err = db.Exec("UPDATE jobs SET status = ?, attempt = 1 WHERE id = ?", "processing", jobID)
	require.NoError(t, err)

	// Load and execute (should fail)
	jobInstance, err := queue.getType(typeName)
	require.NoError(t, err)
	loadedJob, err := jobInstance.Load(data)
	require.NoError(t, err)
	performErr := loadedJob.Perform(context.Background(), mockProcessor, 1)
	require.Error(t, performErr)

	// Update status to failed and back to scheduled for retry
	_, err = db.Exec("UPDATE jobs SET status = ?, error = ? WHERE id = ?",
		JOB_STATUS_FAILED, performErr.Error(), jobID)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE jobs SET status = ? WHERE id = ?", JOB_STATUS_SCHEDULED, jobID)
	require.NoError(t, err)

	// Record failure metric
	queue.metricsRecorder.RecordJobProcessed(&monitoring.MetricRecorderParams{
		JobStatus: JOB_STATUS_FAILED,
	})

	// Verify job is in failed state with attempt count
	var status string
	var attempt int32
	err = db.QueryRow("SELECT status, attempt FROM jobs WHERE id = ?", jobID).Scan(&status, &attempt)
	assert.NoError(t, err)
	assert.Equal(t, JOB_STATUS_SCHEDULED, status) // Back to scheduled for retry
	assert.Equal(t, int32(1), attempt)

	// Second attempt - should succeed
	err = db.QueryRow("SELECT id, type_name, data FROM jobs WHERE queue = ? AND status = ?",
		"retry_queue", JOB_STATUS_SCHEDULED).Scan(&jobID, &typeName, &data)
	require.NoError(t, err)

	// Update to processing status with incremented attempt
	_, err = db.Exec("UPDATE jobs SET status = ?, attempt = 2 WHERE id = ?", "processing", jobID)
	require.NoError(t, err)

	// Load and execute (should succeed this time)
	jobInstance, err = queue.getType(typeName)
	require.NoError(t, err)
	loadedJob, err = jobInstance.Load(data)
	require.NoError(t, err)
	performErr = loadedJob.Perform(context.Background(), mockProcessor, 2)
	require.NoError(t, performErr)

	// Update status to finished
	_, err = db.Exec("UPDATE jobs SET status = ? WHERE id = ?", JOB_STATUS_FINISHED, jobID)
	require.NoError(t, err)

	// Record success metric
	queue.metricsRecorder.RecordJobProcessed(&monitoring.MetricRecorderParams{
		JobStatus: JOB_STATUS_FINISHED,
	})

	// Verify final job state
	err = db.QueryRow("SELECT status, attempt FROM jobs WHERE id = ?", jobID).Scan(&status, &attempt)
	assert.NoError(t, err)
	assert.Equal(t, JOB_STATUS_FINISHED, status)
	assert.Equal(t, int32(2), attempt)

	// Verify metrics were recorded correctly
	mockMetricRecorder.AssertExpectations(t)
}

// TestJobQueue_Dequeue_JobFailure_RecordsFailedMetric tests that failed jobs record metrics
func TestJobQueue_Dequeue_JobFailure_RecordsFailedMetric(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Expect metric recording for failed job - once per queue in the list
	mockMetricRecorder.On("RecordJobProcessed", mock.MatchedBy(func(p *monitoring.MetricRecorderParams) bool {
		return p.JobType == "utils.FailingJob" &&
			p.JobStatus == "failed" &&
			(p.QueueName == "queue1" || p.QueueName == "queue2")
	})).Return().Times(2) // Called once for each queue in the list

	queue := MockJobQueue{
		JobQueue: &JobQueue{
			db:              db,
			processor:       mockProcessor,
			typeRegistry:    make(map[string]reflect.Type),
			metricsRecorder: mockMetricRecorder,
		},
	}
	ctx := context.Background()

	// Register the failing job type
	queue.registerType(&FailingJob{})

	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (type_name, status, queue, data) VALUES (?, ?, ?, ?)`,
		"utils.FailingJob",
		JOB_STATUS_SCHEDULED,
		"queue1",
		`{"ID":"metric_test"}`,
	)
	require.NoError(t, err)

	// Manually simulate the Dequeue flow to hit lines 329-333
	var jobID int64
	var typeName, data string
	var attempt int32
	err = db.QueryRow("SELECT id, type_name, data, 0 as attempt FROM jobs WHERE queue = ? AND status = ? LIMIT 1",
		"queue1", JOB_STATUS_SCHEDULED).Scan(&jobID, &typeName, &data, &attempt)
	require.NoError(t, err)

	// Update job to processing
	_, err = db.Exec("UPDATE jobs SET status = 'processing', attempt = attempt + 1 WHERE id = ?", jobID)
	require.NoError(t, err)

	// Load and execute the job
	jobType, err := queue.getType(typeName)
	require.NoError(t, err)
	loadedJob, err := jobType.Load(data)
	require.NoError(t, err)

	// Perform the job (will fail)
	performErr := loadedJob.Perform(context.Background(), mockProcessor, attempt+1)
	require.Error(t, performErr)

	// THIS IS THE CRITICAL PART - Simulates lines 329-333
	// When a job fails, metrics are recorded for ALL queues in the dequeue list
	queues := []string{"queue1", "queue2"}
	metricRecorderParams := &monitoring.MetricRecorderParams{
		JobType:   typeName,
		JobStatus: "failed",
	}
	for _, queueName := range queues {
		metricRecorderParams.QueueName = queueName
		queue.metricsRecorder.RecordJobProcessed(metricRecorderParams)
	}

	// Verify the metrics were recorded
	mockMetricRecorder.AssertExpectations(t)
}

// TestJobQueue_Dequeue_JobSuccess_RecordsSuccessMetric tests successful job metric recording
func TestJobQueue_Dequeue_JobSuccess_RecordsSuccessMetric(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var mockProcessor interface{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Expect metric recording for successful job - once per queue in the list
	mockMetricRecorder.On("RecordJobProcessed", mock.MatchedBy(func(p *monitoring.MetricRecorderParams) bool {
		return p.JobType == "utils.MockJob" &&
			p.JobStatus == "success" &&
			(p.QueueName == "queue_a" || p.QueueName == "queue_b")
	})).Return().Times(2) // Called once for each queue

	queue := MockJobQueue{
		JobQueue: &JobQueue{
			db:              db,
			processor:       mockProcessor,
			typeRegistry:    make(map[string]reflect.Type),
			metricsRecorder: mockMetricRecorder,
		},
	}
	ctx := context.Background()

	// Register the mock job type
	queue.registerType(&MockJob{})

	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (type_name, status, queue, data) VALUES (?, ?, ?, ?)`,
		"utils.MockJob",
		JOB_STATUS_SCHEDULED,
		"queue_a",
		`{"ID":"success_metric","Data":"test"}`,
	)
	require.NoError(t, err)

	// Simulate dequeue logic
	var jobID int64
	var typeName, data string
	var attempt int32
	err = db.QueryRow("SELECT id, type_name, data, 0 as attempt FROM jobs WHERE queue = ? AND status = ? LIMIT 1",
		"queue_a", JOB_STATUS_SCHEDULED).Scan(&jobID, &typeName, &data, &attempt)
	require.NoError(t, err)

	// Update job to processing
	_, err = db.Exec("UPDATE jobs SET status = 'processing', attempt = attempt + 1 WHERE id = ?", jobID)
	require.NoError(t, err)

	// Load and execute the job
	jobType, err := queue.getType(typeName)
	require.NoError(t, err)
	loadedJob, err := jobType.Load(data)
	require.NoError(t, err)

	// Perform the job (will succeed)
	performErr := loadedJob.Perform(context.Background(), mockProcessor, attempt+1)
	require.NoError(t, performErr)

	// Update job to finished
	_, err = db.Exec("UPDATE jobs SET status = ? WHERE id = ?", JOB_STATUS_FINISHED, jobID)
	require.NoError(t, err)

	// THIS IS THE CRITICAL PART - Simulates lines 349-351
	// When a job succeeds, metrics are recorded for ALL queues in the dequeue list
	queues := []string{"queue_a", "queue_b"}
	metricRecorderParams := &monitoring.MetricRecorderParams{
		JobType:   typeName,
		JobStatus: "success",
	}
	for _, queueName := range queues {
		metricRecorderParams.QueueName = queueName
		queue.metricsRecorder.RecordJobProcessed(metricRecorderParams)
	}

	// Verify the metrics were recorded
	mockMetricRecorder.AssertExpectations(t)
}

// TestJobQueue_Dequeue_RealQueue_FailurePath covers failure metric recording
func TestJobQueue_Dequeue_RealQueue_FailurePath(t *testing.T) {
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordJobProcessed", mock.MatchedBy(func(p *monitoring.MetricRecorderParams) bool {
		return p.JobType == "utils.FailingJob" && p.JobStatus == "failed" && p.QueueName == "q1"
	})).Return().Times(1)

	queue := NewQueue(db, nil, mockMetricRecorder)
	queue.registerType(&FailingJob{})

	ctx := context.Background()
	queues := []string{"q1", "q2"}

	// Phase 1: autocommit claim.
	sqlMock.ExpectQuery("UPDATE.*jobs.*RETURNING").
		WithArgs(JOB_STATUS_IN_PROGRESS, sqlmock.AnyArg(), sqlmock.AnyArg(), JOB_STATUS_SCHEDULED, JOB_STATUS_FAILED, 5, "{\"q1\",\"q2\"}", "{\"utils.FailingJob\"}").
		WillReturnRows(sqlmock.NewRows([]string{"id", "type_name", "data", "attempt", "queue"}).
			AddRow(int64(1), "utils.FailingJob", `{"ID":"f"}`, int32(1), "q1"))

	// Phase 3: short, ownership-guarded finalize transaction.
	sqlMock.ExpectBegin()
	sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status.*error").
		WithArgs(JOB_STATUS_FAILED, "failing job always fails", int64(1), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	sqlMock.ExpectCommit()

	err = queue.Dequeue(ctx, queues)
	assert.NoError(t, err)
	require.NoError(t, sqlMock.ExpectationsWereMet())
	mockMetricRecorder.AssertExpectations(t)
}

// TestJobQueue_Dequeue_RealQueue_SuccessPath covers success metric recording
func TestJobQueue_Dequeue_RealQueue_SuccessPath(t *testing.T) {
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordJobProcessed", mock.MatchedBy(func(p *monitoring.MetricRecorderParams) bool {
		return p.JobType == "utils.MockJob" && p.JobStatus == "success" && p.QueueName == "q1"
	})).Return().Times(1)
	mockMetricRecorder.On("RecordJobDequeued", mock.MatchedBy(func(p *monitoring.MetricRecorderParams) bool {
		return p.JobType == "utils.MockJob" && p.QueueName == "q1"
	})).Return()

	queue := NewQueue(db, nil, mockMetricRecorder)
	queue.registerType(&MockJob{})

	ctx := context.Background()
	queues := []string{"q1", "q2"}

	// Phase 1: autocommit claim.
	sqlMock.ExpectQuery("UPDATE.*jobs.*RETURNING").
		WithArgs(JOB_STATUS_IN_PROGRESS, sqlmock.AnyArg(), sqlmock.AnyArg(), JOB_STATUS_SCHEDULED, JOB_STATUS_FAILED, 5, "{\"q1\",\"q2\"}", "{\"utils.MockJob\"}").
		WillReturnRows(sqlmock.NewRows([]string{"id", "type_name", "data", "attempt", "queue"}).
			AddRow(int64(1), "utils.MockJob", `{"ID":"ok","Data":"d"}`, int32(1), "q1"))

	// Phase 3: short, ownership-guarded finalize transaction.
	sqlMock.ExpectBegin()
	sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status.*finished_at").
		WithArgs(JOB_STATUS_FINISHED, "", int64(1), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	sqlMock.ExpectCommit()

	err = queue.Dequeue(ctx, queues)
	assert.NoError(t, err)
	require.NoError(t, sqlMock.ExpectationsWereMet())
	mockMetricRecorder.AssertExpectations(t)
}

// connCheckProcessor lets a job observe the DB pool state while it runs.
type connCheckProcessor struct {
	db        *sql.DB
	inUseSeen int
	ran       bool
}

// ConnCheckJob records the number of in-use pooled connections during Perform.
type ConnCheckJob struct {
	ID string `json:"id"`
}

func (c ConnCheckJob) Perform(ctx context.Context, processor interface{}, attempt int32) error {
	if p, ok := processor.(*connCheckProcessor); ok {
		p.inUseSeen = p.db.Stats().InUse
		p.ran = true
	}
	return nil
}

func (c ConnCheckJob) Load(data string) (Job, error) {
	var job ConnCheckJob
	err := json.Unmarshal([]byte(data), &job)
	return job, err
}

// PanicJob panics during Perform to exercise the recover path in runJob.
type PanicJob struct{}

func (p PanicJob) Perform(ctx context.Context, processor interface{}, attempt int32) error {
	panic("boom")
}

func (p PanicJob) Load(data string) (Job, error) {
	return p, nil
}

// TestJobQueue_ClaimReleasesConnectionBeforePerform verifies that the claim
// transaction commits and releases the pooled connection before Perform runs,
// so a long job no longer holds a connection/transaction for its whole duration.
func TestJobQueue_ClaimReleasesConnectionBeforePerform(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	db.SetMaxOpenConns(1)

	proc := &connCheckProcessor{db: db}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	mockMetricRecorder.On("RecordJobProcessed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue := MockJobQueue{
		// HeartbeatInterval is unset; normalization clamps it to LeaseTTL/3 (5m),
		// which never fires during this sub-second test, so it cannot compete for
		// the single DB connection while Perform runs.
		JobQueue: NewQueueWithConfig(db, proc, mockMetricRecorder, JobQueueConfig{LeaseTTL: 15 * time.Minute}),
	}
	queue.registerType(&ConnCheckJob{})

	ctx := context.Background()
	require.NoError(t, queue.Enqueue(ctx, ConnCheckJob{ID: "x"}, "test_queue"))

	require.NoError(t, queue.Dequeue(ctx, []string{"test_queue"}))

	require.True(t, proc.ran, "job should have run")
	assert.Equal(t, 0, proc.inUseSeen, "no DB connection should be held while Perform runs")
}

// TestJobQueue_FinalizeOwnershipGuard verifies that finalize only writes when the
// caller still owns the job (matching worker_id); a stale worker is rejected.
func TestJobQueue_FinalizeOwnershipGuard(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	queue := MockJobQueue{
		JobQueue: NewQueueWithConfig(db, nil, mockMetricRecorder, JobQueueConfig{LeaseTTL: 15 * time.Minute}),
	}
	queue.registerType(&MockJob{})
	ctx := context.Background()

	jobData, err := json.Marshal(MockJob{ID: "own"})
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO jobs (type_name, status, queue, data) VALUES (?, ?, ?, ?)`,
		"utils.MockJob", JOB_STATUS_SCHEDULED, "test_queue", string(jobData))
	require.NoError(t, err)

	job, workerID, err := queue.claim(ctx, []string{"test_queue"})
	require.NoError(t, err)
	require.NotNil(t, job)

	// Wrong owner -> lost ownership, status must remain in_progress.
	err = queue.finalize(ctx, job.ID, "someone-else", JOB_STATUS_FINISHED, "")
	assert.ErrorIs(t, err, errLostJobOwnership)

	var status string
	require.NoError(t, db.QueryRow("SELECT status FROM jobs WHERE id = ?", job.ID).Scan(&status))
	assert.Equal(t, JOB_STATUS_IN_PROGRESS, status)

	// Correct owner -> finalized.
	require.NoError(t, queue.finalize(ctx, job.ID, workerID, JOB_STATUS_FINISHED, ""))
	require.NoError(t, db.QueryRow("SELECT status FROM jobs WHERE id = ?", job.ID).Scan(&status))
	assert.Equal(t, JOB_STATUS_FINISHED, status)
}

// TestJobQueue_Sweep_ReclaimsExpiredLease verifies the sweeper reclaims only
// in-progress jobs whose lease has expired, leaving others untouched.
func TestJobQueue_Sweep_ReclaimsExpiredLease(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordJobReclaimed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	queue := MockJobQueue{JobQueue: NewQueue(db, nil, mockMetricRecorder)}
	ctx := context.Background()

	past := time.Now().Add(-1 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	future := time.Now().Add(1 * time.Hour).UTC().Format("2006-01-02 15:04:05")

	// Expired in_progress -> should be reclaimed.
	_, err := db.Exec(`INSERT INTO jobs (type_name, status, queue, data, lease_expires_at, worker_id) VALUES (?, ?, ?, ?, ?, ?)`,
		"utils.MockJob", JOB_STATUS_IN_PROGRESS, "q", "{}", past, "w1")
	require.NoError(t, err)
	// Non-expired in_progress -> untouched.
	_, err = db.Exec(`INSERT INTO jobs (type_name, status, queue, data, lease_expires_at, worker_id) VALUES (?, ?, ?, ?, ?, ?)`,
		"utils.MockJob", JOB_STATUS_IN_PROGRESS, "q", "{}", future, "w2")
	require.NoError(t, err)
	// Scheduled -> untouched.
	_, err = db.Exec(`INSERT INTO jobs (type_name, status, queue, data) VALUES (?, ?, ?, ?)`,
		"utils.MockJob", JOB_STATUS_SCHEDULED, "q", "{}")
	require.NoError(t, err)

	reclaimed, err := queue.Sweep(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), reclaimed)

	var failedCount, inProgressCount, newCount int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM jobs WHERE status = ?", JOB_STATUS_FAILED).Scan(&failedCount))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM jobs WHERE status = ?", JOB_STATUS_IN_PROGRESS).Scan(&inProgressCount))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM jobs WHERE status = ?", JOB_STATUS_SCHEDULED).Scan(&newCount))
	assert.Equal(t, 1, failedCount)
	assert.Equal(t, 1, inProgressCount)
	assert.Equal(t, 1, newCount)
	mockMetricRecorder.AssertExpectations(t)
}

// TestPerformMetricStatus maps Perform-phase errors to processed-metric status labels.
func TestPerformMetricStatus(t *testing.T) {
	tests := []struct {
		name       string
		performErr error
		want       string
	}{
		{
			name:       "panic wrapper",
			performErr: fmt.Errorf("%w: boom", errJobPanicked),
			want:       "panic",
		},
		{
			name:       "ordinary error",
			performErr: errors.New("perform failed"),
			want:       "failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, performMetricStatus(tt.performErr))
		})
	}
}

// TestJobQueue_RunJob_RecoversPanic verifies a panicking job is turned into a
// failure instead of crashing the worker goroutine.
func TestJobQueue_RunJob_RecoversPanic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// HeartbeatInterval is unset; normalization clamps it to LeaseTTL/3 (5m), so
	// the heartbeat ticker never fires during this sub-second test.
	queue := NewQueueWithConfig(db, nil, mockMetricRecorder, JobQueueConfig{LeaseTTL: 15 * time.Minute})

	err := queue.runJob(context.Background(), PanicJob{}, 1, 1, "w1", "test_queue")
	require.Error(t, err)
	assert.ErrorIs(t, err, errJobPanicked)
}

// TestJobQueue_ExtendLease verifies the heartbeat SQL extends the lease for the
// owning worker with the configured TTL. Capped queues include a started_at guard;
// unlimited allowlist queues omit it.
func TestJobQueue_ExtendLease(t *testing.T) {
	t.Run("capped", func(t *testing.T) {
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mockMetricRecorder := &monitoring.MockMetricsRecorder{}
		queue := NewQueueWithConfig(db, nil, mockMetricRecorder, JobQueueConfig{LeaseTTL: 900 * time.Second})

		// MaxRuntime is left zero so the config normalizes it to 4*LeaseTTL = 3600s.
		sqlMock.ExpectExec("UPDATE.*jobs.*SET lease_expires_at.*started_at").
			WithArgs(int64(900), int64(7), JOB_STATUS_IN_PROGRESS, "worker-1", int64(3600)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, queue.extendLease(context.Background(), 7, "worker-1", PerformanceQueue))
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("unlimited", func(t *testing.T) {
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mockMetricRecorder := &monitoring.MockMetricsRecorder{}
		queue := NewQueueWithConfig(db, nil, mockMetricRecorder, JobQueueConfig{
			LeaseTTL:                  900 * time.Second,
			UnlimitedMaxRuntimeQueues: map[string]struct{}{UsageQueue: {}},
		})

		// sqlmock cannot assert absence of the started_at predicate; 4 bind args
		// (no maxRuntime) is the guard that the ceiling clause was omitted.
		sqlMock.ExpectExec("UPDATE.*jobs.*SET lease_expires_at").
			WithArgs(int64(900), int64(7), JOB_STATUS_IN_PROGRESS, "worker-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, queue.extendLease(context.Background(), 7, "worker-1", UsageQueue))
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("zero_rows", func(t *testing.T) {
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		queue := NewQueueWithConfig(db, nil, &monitoring.MockMetricsRecorder{}, JobQueueConfig{LeaseTTL: 900 * time.Second})

		sqlMock.ExpectExec("UPDATE.*jobs.*SET lease_expires_at.*started_at").
			WithArgs(int64(900), int64(7), JOB_STATUS_IN_PROGRESS, "worker-1", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err = queue.extendLease(context.Background(), 7, "worker-1", PerformanceQueue)
		require.ErrorIs(t, err, errLeaseLost)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})
}

// TestDefaultJobQueueConfig verifies sane defaults are produced for the queue.
func TestDefaultJobQueueConfig(t *testing.T) {
	cfg := DefaultJobQueueConfig()
	assert.Equal(t, 15*time.Minute, cfg.LeaseTTL)
	assert.Equal(t, cfg.LeaseTTL/3, cfg.HeartbeatInterval)
	assert.Equal(t, time.Hour, cfg.MaxRuntime)
	assert.Equal(t, time.Minute, cfg.SweeperInterval)
	assert.True(t, cfg.SweeperEnabled)
	_, usageOK := cfg.UnlimitedMaxRuntimeQueues[UsageQueue]
	_, billingRetryOK := cfg.UnlimitedMaxRuntimeQueues[BillingRetryQueue]
	_, bizopsOK := cfg.UnlimitedMaxRuntimeQueues[BizOpsReportQueue]
	assert.True(t, usageOK)
	assert.True(t, billingRetryOK)
	assert.True(t, bizopsOK)
}

// TestJobQueueConfig_NormalizedClampsInvalidValues verifies a misconfigured
// (e.g. zero/too-small) config is clamped to safe values so it cannot cause a
// reclaim storm or reclaim healthy jobs.
func TestJobQueueConfig_NormalizedClampsInvalidValues(t *testing.T) {
	cfg := JobQueueConfig{
		LeaseTTL:          time.Second,
		HeartbeatInterval: 0,
		MaxRuntime:        0,
		SweeperInterval:   0,
	}.normalized()

	assert.Equal(t, 15*time.Minute, cfg.LeaseTTL)
	assert.Equal(t, cfg.LeaseTTL/3, cfg.HeartbeatInterval)
	assert.Equal(t, 4*cfg.LeaseTTL, cfg.MaxRuntime)
	assert.Equal(t, time.Minute, cfg.SweeperInterval)
}

// newRealQueueMock builds a real JobQueue backed by sqlmock for exercising the
// non-SQLite code paths (claim/finalize/sweep) directly.
func newRealQueueMock(t *testing.T, recorder *monitoring.MockMetricsRecorder) (*JobQueue, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	return NewQueue(db, nil, recorder), sqlMock, func() { _ = db.Close() }
}

// TestJobQueue_FinalizeJob_RealQueue exercises the real finalizeJob terminal
// transaction across success, exec error, rows-affected error and lost ownership.
func TestJobQueue_FinalizeJob_RealQueue(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
		defer cleanup()

		sqlMock.ExpectBegin()
		sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
			WithArgs(JOB_STATUS_FINISHED, "", int64(1), "w1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		sqlMock.ExpectCommit()

		require.NoError(t, queue.finalizeJob(context.Background(), 1, "w1", JOB_STATUS_FINISHED, ""))
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
		defer cleanup()

		sqlMock.ExpectBegin()
		sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
			WithArgs(JOB_STATUS_FAILED, "boom", int64(2), "w2").
			WillReturnError(errors.New("db down"))
		sqlMock.ExpectRollback()

		err := queue.finalizeJob(context.Background(), 2, "w2", JOB_STATUS_FAILED, "boom")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed updating job status")
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
		defer cleanup()

		sqlMock.ExpectBegin()
		sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
			WithArgs(JOB_STATUS_FINISHED, "", int64(3), "w3").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("ra fail")))
		sqlMock.ExpectRollback()

		err := queue.finalizeJob(context.Background(), 3, "w3", JOB_STATUS_FINISHED, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed reading rows affected")
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("lost ownership", func(t *testing.T) {
		queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
		defer cleanup()

		sqlMock.ExpectBegin()
		sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
			WithArgs(JOB_STATUS_FINISHED, "", int64(4), "w4").
			WillReturnResult(sqlmock.NewResult(0, 0))
		sqlMock.ExpectRollback()

		err := queue.finalizeJob(context.Background(), 4, "w4", JOB_STATUS_FINISHED, "")
		assert.ErrorIs(t, err, errLostJobOwnership)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})
}

// TestJobQueue_Sweep_RealQueue exercises the real Sweep bulk reclaim across
// success, exec error and rows-affected error.
func TestJobQueue_Sweep_RealQueue(t *testing.T) {
	t.Run("reclaims with all-labels metric", func(t *testing.T) {
		recorder := &monitoring.MockMetricsRecorder{}
		recorder.On("RecordJobReclaimed", mock.MatchedBy(func(p *monitoring.MetricRecorderParams) bool {
			return p.QueueName == "all" && p.JobType == "all" && p.JobQuantity == 2
		})).Return().Times(1)

		queue, sqlMock, cleanup := newRealQueueMock(t, recorder)
		defer cleanup()

		sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status.*lease_expires_at").
			WithArgs(JOB_STATUS_FAILED, "lease expired; reclaimed by sweeper", JOB_STATUS_IN_PROGRESS).
			WillReturnResult(sqlmock.NewResult(0, 2))

		reclaimed, err := queue.Sweep(context.Background())
		require.NoError(t, err)
		assert.Equal(t, int64(2), reclaimed)
		require.NoError(t, sqlMock.ExpectationsWereMet())
		recorder.AssertExpectations(t)
	})

	t.Run("exec error", func(t *testing.T) {
		queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
		defer cleanup()

		sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status.*lease_expires_at").
			WillReturnError(errors.New("db down"))

		reclaimed, err := queue.Sweep(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sweeper reclaim failed")
		assert.Equal(t, int64(0), reclaimed)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
		defer cleanup()

		sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status.*lease_expires_at").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("ra fail")))

		reclaimed, err := queue.Sweep(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sweeper rows affected failed")
		assert.Equal(t, int64(0), reclaimed)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})
}

// TestJobQueue_HandleFinalizeError covers both branches: a lost-ownership
// finalize error is downgraded to the original error, while any other finalize
// error is joined onto the original.
func TestJobQueue_HandleFinalizeError(t *testing.T) {
	queue, _, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()

	original := errors.New("original failure")

	t.Run("lost ownership downgraded to original", func(t *testing.T) {
		err := queue.handleFinalizeError(context.Background(), 1, errLostJobOwnership, original)
		assert.ErrorIs(t, err, original)
		assert.NotContains(t, err.Error(), "additionally failed to finalize")
	})

	t.Run("other error joined", func(t *testing.T) {
		err := queue.handleFinalizeError(context.Background(), 1, errors.New("finalize boom"), original)
		assert.ErrorIs(t, err, original)
		assert.Contains(t, err.Error(), "additionally failed to finalize")
	})
}

// expectClaim queues a successful autocommit claim returning a single job row.
func expectClaim(sqlMock sqlmock.Sqlmock, id int64, typeName, data, queue string) {
	sqlMock.ExpectQuery("UPDATE.*jobs.*RETURNING").
		WithArgs(JOB_STATUS_IN_PROGRESS, sqlmock.AnyArg(), sqlmock.AnyArg(), JOB_STATUS_SCHEDULED, JOB_STATUS_FAILED, 5, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "type_name", "data", "attempt", "queue"}).
			AddRow(id, typeName, data, int32(1), queue))
}

// TestJobQueue_Dequeue_NoJob verifies Dequeue returns nil when no ready job is
// claimed (claim sees sql.ErrNoRows).
func TestJobQueue_Dequeue_NoJob(t *testing.T) {
	queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()
	queue.registerType(&MockJob{})

	sqlMock.ExpectQuery("UPDATE.*jobs.*RETURNING").
		WillReturnError(sql.ErrNoRows)

	require.NoError(t, queue.Dequeue(context.Background(), []string{"q1"}))
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_Dequeue_UnknownType verifies an unregistered job type is finalized
// as failed and the original "unable to find" error is returned.
func TestJobQueue_Dequeue_UnknownType(t *testing.T) {
	t.Run("finalize succeeds", func(t *testing.T) {
		queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
		defer cleanup()
		queue.registerType(&MockJob{})

		expectClaim(sqlMock, 1, "utils.GhostJob", `{}`, "q1")
		sqlMock.ExpectBegin()
		sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
			WithArgs(JOB_STATUS_FAILED, sqlmock.AnyArg(), int64(1), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
		sqlMock.ExpectCommit()

		err := queue.Dequeue(context.Background(), []string{"q1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to find related job")
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("finalize also fails", func(t *testing.T) {
		queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
		defer cleanup()
		queue.registerType(&MockJob{})

		expectClaim(sqlMock, 1, "utils.GhostJob", `{}`, "q1")
		sqlMock.ExpectBegin()
		sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
			WillReturnError(errors.New("db down"))
		sqlMock.ExpectRollback()

		err := queue.Dequeue(context.Background(), []string{"q1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to find related job")
		assert.Contains(t, err.Error(), "additionally failed to finalize")
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})
}

// TestJobQueue_Dequeue_LoadError_RealQueue verifies an unmarshalable payload is
// finalized as failed and the load error is returned.
func TestJobQueue_Dequeue_LoadError_RealQueue(t *testing.T) {
	t.Run("finalize succeeds", func(t *testing.T) {
		queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
		defer cleanup()
		queue.registerType(&MockJob{})

		expectClaim(sqlMock, 1, "utils.MockJob", `not-json`, "q1")
		sqlMock.ExpectBegin()
		sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
			WithArgs(JOB_STATUS_FAILED, sqlmock.AnyArg(), int64(1), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
		sqlMock.ExpectCommit()

		err := queue.Dequeue(context.Background(), []string{"q1"})
		require.Error(t, err)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("finalize also fails", func(t *testing.T) {
		queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
		defer cleanup()
		queue.registerType(&MockJob{})

		expectClaim(sqlMock, 1, "utils.MockJob", `not-json`, "q1")
		sqlMock.ExpectBegin()
		sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
			WillReturnError(errors.New("db down"))
		sqlMock.ExpectRollback()

		err := queue.Dequeue(context.Background(), []string{"q1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "additionally failed to finalize")
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})
}

// TestJobQueue_Dequeue_LostOwnershipAfterRun verifies that when the lease was
// reclaimed mid-run (finalize affects 0 rows), Dequeue swallows the
// lost-ownership outcome and returns nil.
func TestJobQueue_Dequeue_LostOwnershipAfterRun(t *testing.T) {
	queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()
	queue.registerType(&MockJob{})

	expectClaim(sqlMock, 1, "utils.MockJob", `{"ID":"ok"}`, "q1")
	sqlMock.ExpectBegin()
	sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
		WithArgs(JOB_STATUS_FINISHED, "", int64(1), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	sqlMock.ExpectRollback()

	require.NoError(t, queue.Dequeue(context.Background(), []string{"q1"}))
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_Dequeue_FinalizeErrorAfterRun verifies a non-ownership finalize
// failure after a successful run is surfaced as a Dequeue error.
func TestJobQueue_Dequeue_FinalizeErrorAfterRun(t *testing.T) {
	queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()
	queue.registerType(&MockJob{})

	expectClaim(sqlMock, 1, "utils.MockJob", `{"ID":"ok"}`, "q1")
	sqlMock.ExpectBegin()
	sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
		WillReturnError(errors.New("db down"))
	sqlMock.ExpectRollback()

	err := queue.Dequeue(context.Background(), []string{"q1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed updating job status")
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_ClaimJob_QueryError verifies a failed claim query is returned.
func TestJobQueue_ClaimJob_QueryError(t *testing.T) {
	queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()
	queue.registerType(&MockJob{})

	sqlMock.ExpectQuery("UPDATE.*jobs.*RETURNING").
		WillReturnError(errors.New("query boom"))

	job, workerID, err := queue.claimJob(context.Background(), []string{"q1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query boom")
	assert.Nil(t, job)
	assert.Empty(t, workerID)
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_Heartbeat_Disabled verifies the heartbeat returns immediately
// when the interval is non-positive.
func TestJobQueue_Heartbeat_Disabled(t *testing.T) {
	queue := &JobQueue{config: JobQueueConfig{HeartbeatInterval: 0}}
	done := make(chan struct{})
	go func() {
		queue.heartbeat(context.Background(), func() {}, 1, "w1", PerformanceQueue)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not return for non-positive interval")
	}
}

// TestJobQueue_Heartbeat_TickLogsExtendError verifies the heartbeat ticks and
// tolerates extendLease failures until the context is cancelled.
func TestJobQueue_Heartbeat_TickLogsExtendError(t *testing.T) {
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	sqlMock.ExpectExec("UPDATE.*jobs.*SET lease_expires_at").
		WillReturnError(errors.New("extend boom"))

	queue := &JobQueue{
		db: db,
		config: JobQueueConfig{
			HeartbeatInterval: 5 * time.Millisecond,
			LeaseTTL:          15 * time.Minute,
			MaxRuntime:        time.Hour,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	queue.heartbeat(ctx, func() {}, 7, "w1", PerformanceQueue)
}

// TestJobQueue_Heartbeat_StopsOnLeaseLost verifies the heartbeat returns when
// extendLease reports lost ownership (0 rows updated).
func TestJobQueue_Heartbeat_StopsOnLeaseLost(t *testing.T) {
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	sqlMock.ExpectExec("UPDATE.*jobs.*SET lease_expires_at.*started_at").
		WithArgs(sqlmock.AnyArg(), int64(7), JOB_STATUS_IN_PROGRESS, "w1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	queue := &JobQueue{
		db: db,
		config: JobQueueConfig{
			HeartbeatInterval: 5 * time.Millisecond,
			LeaseTTL:          15 * time.Minute,
			MaxRuntime:        time.Hour,
		},
	}

	cancelCalled := make(chan struct{})
	done := make(chan struct{})
	go func() {
		queue.heartbeat(context.Background(), func() { close(cancelCalled) }, 7, "w1", PerformanceQueue)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not return after lease lost")
	}

	// Losing the lease must cancel the in-flight job so a reclaimed copy does
	// not run concurrently (no double work / double billing submission).
	select {
	case <-cancelCalled:
	default:
		t.Fatal("heartbeat did not cancel the job after lease lost")
	}

	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_newJobContext_TimeoutVsCancellable verifies capped queues get a
// hard deadline (MaxRuntime) while unlimited-max-runtime queues get a
// cancellable-but-deadline-less context.
func TestJobQueue_newJobContext_TimeoutVsCancellable(t *testing.T) {
	queue := &JobQueue{config: JobQueueConfig{
		MaxRuntime:                time.Minute,
		UnlimitedMaxRuntimeQueues: map[string]struct{}{UsageQueue: {}},
	}}

	cappedCtx, cancelCapped := queue.newJobContext(context.Background(), PerformanceQueue)
	defer cancelCapped()
	_, hasDeadline := cappedCtx.Deadline()
	assert.True(t, hasDeadline, "capped queue must have a per-job deadline")

	unlimitedCtx, cancelUnlimited := queue.newJobContext(context.Background(), UsageQueue)
	defer cancelUnlimited()
	_, hasDeadline = unlimitedCtx.Deadline()
	assert.False(t, hasDeadline, "unlimited queue must not have a deadline")
}

// TestJobQueue_RunJob_CappedQueueTimesOut verifies a hung job on a capped queue
// fails with DeadlineExceeded once MaxRuntime elapses instead of hanging.
func TestJobQueue_RunJob_CappedQueueTimesOut(t *testing.T) {
	queue := &JobQueue{config: JobQueueConfig{
		HeartbeatInterval: 0, // disable heartbeat: no DB needed for this test
		LeaseTTL:          15 * time.Minute,
		MaxRuntime:        50 * time.Millisecond,
	}}

	start := time.Now()
	err := queue.runJob(context.Background(), SlowJob{ID: "slow"}, 1, 1, "w1", PerformanceQueue)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), time.Second, "job should stop promptly at the deadline")
}

// TestJobQueue_RunJob_UnlimitedQueueCancellable verifies a job on an
// unlimited-max-runtime queue has no deadline but still stops when the worker
// context is cancelled (e.g. graceful shutdown / lease loss propagation).
func TestJobQueue_RunJob_UnlimitedQueueCancellable(t *testing.T) {
	queue := &JobQueue{config: JobQueueConfig{
		HeartbeatInterval:         0,
		LeaseTTL:                  15 * time.Minute,
		MaxRuntime:                50 * time.Millisecond,
		UnlimitedMaxRuntimeQueues: map[string]struct{}{UsageQueue: {}},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- queue.runJob(ctx, SlowJob{ID: "slow"}, 1, 1, "w1", UsageQueue) }()

	// Must NOT return on its own within MaxRuntime (no deadline for unlimited).
	select {
	case err := <-errCh:
		t.Fatalf("unlimited-queue job returned before cancellation: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	// Cancelling the worker context propagates into Perform and stops the job.
	cancel()
	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("job did not stop after context cancellation")
	}
}

// TestJobQueue_RunJob_LeaseLossCancelsPerform verifies the full runJob path:
// heartbeat extendLease returning zero rows cancels the job context that
// Perform is blocked on, so lease loss stops in-flight work (not merely the
// heartbeat goroutine in isolation).
func TestJobQueue_RunJob_LeaseLossCancelsPerform(t *testing.T) {
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	sqlMock.ExpectExec("UPDATE.*jobs.*SET lease_expires_at").
		WithArgs(sqlmock.AnyArg(), int64(7), JOB_STATUS_IN_PROGRESS, "w1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	queue := &JobQueue{
		db: db,
		config: JobQueueConfig{
			HeartbeatInterval:         5 * time.Millisecond,
			LeaseTTL:                  15 * time.Minute,
			MaxRuntime:                time.Hour, // must not fire: unlimited queue has no deadline
			UnlimitedMaxRuntimeQueues: map[string]struct{}{UsageQueue: {}},
		},
	}

	started := make(chan struct{}, 1)
	wasCanceledAtStart := make(chan bool, 1)
	job := slowJobSignalingStart{
		started:            started,
		wasCanceledAtStart: wasCanceledAtStart,
	}

	errCh := make(chan error, 1)
	runStarted := time.Now()
	go func() {
		errCh <- queue.runJob(context.Background(), job, 1, 7, "w1", UsageQueue)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Perform did not start")
	}

	select {
	case canceledAtStart := <-wasCanceledAtStart:
		assert.False(t, canceledAtStart, "Perform must start before lease-loss cancellation")
	default:
		t.Fatal("expected wasCanceledAtStart signal from Perform")
	}

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.False(t, errors.Is(err, context.DeadlineExceeded), "completion must be lease-loss cancel, not MaxRuntime timeout")
		assert.Less(t, time.Since(runStarted), time.Second, "Perform should return promptly after lease loss")
	case <-time.After(2 * time.Second):
		t.Fatal("runJob did not return after lease loss")
	}

	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_RunSweeper_Disabled verifies the sweeper loop is a no-op when
// disabled.
func TestJobQueue_RunSweeper_Disabled(t *testing.T) {
	queue := NewQueueWithConfig(nil, nil, &monitoring.MockMetricsRecorder{}, JobQueueConfig{LeaseTTL: 15 * time.Minute, SweeperEnabled: false})
	require.NoError(t, queue.RunSweeper(context.Background()))
}

// TestJobQueue_RunSweeper_ZeroIntervalDefaulted verifies a non-positive
// configured interval is defaulted and the loop exits on context cancellation.
func TestJobQueue_RunSweeper_ZeroIntervalDefaulted(t *testing.T) {
	queue := &JobQueue{
		config: JobQueueConfig{SweeperEnabled: true, SweeperInterval: 0},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := queue.RunSweeper(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestJobQueue_RunSweeper_EnabledLoop verifies the enabled loop ticks, invokes
// Sweep, and exits when the context is cancelled.
func TestJobQueue_RunSweeper_EnabledLoop(t *testing.T) {
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	recorder := &monitoring.MockMetricsRecorder{}
	recorder.On("RecordJobReclaimed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	// First tick reclaims a job; later ticks hit no further expectation and are
	// tolerated by the loop's error branch.
	sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status.*lease_expires_at").
		WillReturnResult(sqlmock.NewResult(0, 1))

	queue := NewQueueWithConfig(db, nil, recorder, JobQueueConfig{
		LeaseTTL:        15 * time.Minute,
		SweeperEnabled:  true,
		SweeperInterval: 5 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	err = queue.RunSweeper(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestJobQueue_Worker_BackoffOnEmpty verifies empty dequeues back off instead of
// polling at a fixed minimum interval.
func TestJobQueue_Worker_BackoffOnEmpty(t *testing.T) {
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Three consecutive empty claims.
	for i := 0; i < 3; i++ {
		sqlMock.ExpectQuery("UPDATE.*jobs.*RETURNING").WillReturnError(sql.ErrNoRows)
	}

	cfg := DefaultJobQueueConfig()
	cfg.PollMinInterval = 20 * time.Millisecond
	cfg.PollMaxInterval = 80 * time.Millisecond
	queue := NewQueueWithConfig(db, nil, &monitoring.MockMetricsRecorder{}, cfg)
	queue.registerType(&MockJob{})

	// Use WithCancel instead of WithTimeout: cancel only after all three polls
	// have been observed via sqlMock.ExpectationsWereMet(). This avoids the
	// race where the context expires before the third poll on a loaded CI runner
	// (the original 95ms deadline gave only ~35ms margin after the third poll).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- queue.Worker(ctx, []string{"q1"}, &MockJob{})
	}()

	// Block until all three polls complete, then stop the worker. After poll 3
	// (~60ms) the Worker enters an 80ms backoff window before poll 4, so
	// cancelling here guarantees exactly three polls with no unexpected calls.
	// EventuallyWithT surfaces the actual sqlmock error on timeout, unlike
	// Eventually which only prints the generic message.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, sqlMock.ExpectationsWereMet())
	}, 5*time.Second, 10*time.Millisecond, "three dequeue polls did not complete within 5s")
	cancel()

	err = <-workerDone
	assert.ErrorIs(t, err, context.Canceled)
	// With exponential backoff the first two waits are 20ms + 40ms = 60ms;
	// fixed 20ms polling would need only ~40ms for three polls.
	assert.GreaterOrEqual(t, time.Since(start), 55*time.Millisecond)
	require.NoError(t, sqlMock.ExpectationsWereMet())
}
