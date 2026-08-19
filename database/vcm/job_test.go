package vcm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"gorm.io/gorm"
)

func openJobTestStore(t *testing.T) Storage {
	t.Helper()
	store, err := Open(context.Background(), Config{
		Driver: DriverSQLite,
		SQLite: SQLConfig{DSN: uniqueSQLiteDSN(t)},
	}, log.NewLogger(), true)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	return store
}

func TestJobLifecycle(t *testing.T) {
	ctx := context.Background()
	store := openJobTestStore(t)
	deadlineAt := time.Date(2026, 7, 30, 14, 30, 0, 0, time.UTC)
	job := &Job{
		ID:         "550e8400-e29b-41d4-a716-446655440000",
		ResourceID: "550e8400-e29b-41d4-a716-446655440001",
		Type:       string(datamodel.JobTypeCreatePool),
		DeadlineAt: deadlineAt,
	}

	require.NoError(t, store.CreateJob(ctx, job))
	assert.Equal(t, JobStateNew, job.State)

	created, err := store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, deadlineAt, created.DeadlineAt)
	assert.Empty(t, created.CurrentStep)
	assert.False(t, created.IsTerminal())

	require.NoError(t, store.UpdateJobProgress(ctx, UpdateJobProgressParams{
		JobID:     job.ID,
		Step:      "CREATE_CLUSTER",
		State:     JobStateProcessing,
		Retriable: true,
	}))
	updated, err := store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, JobStateProcessing, updated.State)
	assert.Equal(t, "CREATE_CLUSTER", updated.CurrentStep)
	assert.True(t, updated.Retriable)

	ongoing, err := store.ListOngoingJobs(ctx)
	require.NoError(t, err)
	require.Len(t, ongoing, 1)
	assert.Equal(t, job.ID, ongoing[0].ID)

	require.NoError(t, store.UpdateJobProgress(ctx, UpdateJobProgressParams{
		JobID: job.ID,
		Step:  "COMPLETED",
		State: JobStateDone,
	}))
	completed, err := store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.True(t, completed.IsTerminal())
	assert.False(t, completed.Retriable)

	ongoing, err = store.ListOngoingJobs(ctx)
	require.NoError(t, err)
	assert.Empty(t, ongoing)
}

func TestJobPersistsFailureDetails(t *testing.T) {
	ctx := context.Background()
	store := openJobTestStore(t)
	job := &Job{
		ID:         "550e8400-e29b-41d4-a716-446655440002",
		ResourceID: "550e8400-e29b-41d4-a716-446655440003",
		Type:       string(datamodel.JobTypeCreatePool),
		DeadlineAt: time.Now().UTC().Add(time.Hour),
	}
	require.NoError(t, store.CreateJob(ctx, job))

	const trackingID = 7008
	require.NoError(t, store.UpdateJobProgress(ctx, UpdateJobProgressParams{
		JobID:        job.ID,
		Step:         "CREATE_CLUSTER",
		State:        JobStateError,
		TrackingID:   trackingID,
		ErrorMessage: "validation failed",
	}))

	failed, err := store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, trackingID, failed.TrackingID)
	assert.Equal(t, "validation failed", failed.ErrorMessage)
	assert.False(t, failed.Retriable)
}

func TestJobStoreErrors(t *testing.T) {
	ctx := context.Background()
	store := openJobTestStore(t)

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "nil job",
			run: func() error {
				return store.CreateJob(ctx, nil)
			},
		},
		{
			name: "missing required fields",
			run: func() error {
				return store.CreateJob(ctx, &Job{})
			},
		},
		{
			name: "invalid initial state",
			run: func() error {
				return store.CreateJob(ctx, &Job{
					ID:         "job",
					ResourceID: "resource",
					Type:       string(datamodel.JobTypeCreatePool),
					State:      JobState("INVALID"),
				})
			},
		},
		{
			name: "invalid update state",
			run: func() error {
				return store.UpdateJobProgress(ctx, UpdateJobProgressParams{
					JobID: "job",
					Step:  "step",
					State: JobState("INVALID"),
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Error(t, test.run())
		})
	}

	_, err := store.GetJob(ctx, "missing")
	assert.ErrorIs(t, err, ErrJobNotFound)

	err = store.UpdateJobProgress(ctx, UpdateJobProgressParams{
		JobID: "missing",
		Step:  "step",
		State: JobStateProcessing,
	})
	assert.True(t, errors.Is(err, ErrJobNotFound))
}

func TestCreateJob_Duplicate_ReturnsJobAlreadyExists(t *testing.T) {
	ctx := context.Background()
	store := openJobTestStore(t)
	job := &Job{
		ID:         "550e8400-e29b-41d4-a716-446655440099",
		ResourceID: "550e8400-e29b-41d4-a716-446655440098",
		Type:       string(datamodel.JobTypeCreatePool),
		DeadlineAt: time.Now().UTC().Add(time.Hour),
	}
	require.NoError(t, store.CreateJob(ctx, job))

	// Re-inserting the same primary key must surface the typed
	// ErrJobAlreadyExists sentinel (mapped from the driver's unique violation),
	// carrying the offending job id.
	dup := &Job{
		ID:         job.ID,
		ResourceID: job.ResourceID,
		Type:       job.Type,
		DeadlineAt: job.DeadlineAt,
	}
	err := store.CreateJob(ctx, dup)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrJobAlreadyExists)
	assert.Contains(t, err.Error(), job.ID)
}

func TestIsUniqueViolation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "gorm duplicated key sentinel",
			err:  gorm.ErrDuplicatedKey,
			want: true,
		},
		{
			name: "postgres unique constraint",
			err:  errors.New(`ERROR: duplicate key value violates unique constraint "jobs_pkey"`),
			want: true,
		},
		{
			name: "sqlite unique constraint",
			err:  errors.New("UNIQUE constraint failed: jobs.id"),
			want: true,
		},
		{
			name: "spanner already exists",
			err:  errors.New(`spanner: code = "AlreadyExists", desc = "Row [3fa85f64-5717-4562-b3fc-2c963f66afa9] in table jobs already exists", requestID = "1.17eaa3c554d7382c.1.2.14.1"`),
			want: true,
		},
		{
			name: "unrelated error",
			err:  errors.New("connection refused"),
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, isUniqueViolation(test.err))
		})
	}
}

func TestJobIsExpired(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	job := Job{
		DeadlineAt: now.Add(-time.Hour),
	}

	assert.True(t, job.IsExpired(now))
	assert.False(t, job.IsExpired(now.Add(-90*time.Minute)))

	jobWithoutDeadline := Job{}
	assert.False(t, jobWithoutDeadline.IsExpired(now))
}
