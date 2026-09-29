package recovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

type stubJobStore struct {
	jobs          []vcmdb.Job
	listErr       error
	updateErr     error
	progressCalls []vcmdb.UpdateJobProgressParams
}

func (s *stubJobStore) CreateJob(context.Context, *vcmdb.Job) error {
	return nil
}

func (s *stubJobStore) GetJob(context.Context, string) (*vcmdb.Job, error) {
	return nil, nil
}

func (s *stubJobStore) GetOngoingJobByResource(context.Context, string, string) (*vcmdb.Job, error) {
	return nil, nil
}

func (s *stubJobStore) ListOngoingJobs(context.Context) ([]vcmdb.Job, error) {
	return s.jobs, s.listErr
}

func (s *stubJobStore) UpdateJobProgress(_ context.Context, params vcmdb.UpdateJobProgressParams) error {
	s.progressCalls = append(s.progressCalls, params)
	return s.updateErr
}

func TestCoordinatorResumeOngoingJobs_DispatchesRegisteredTypes(t *testing.T) {
	store := &stubJobStore{jobs: []vcmdb.Job{
		{ID: "job-1", Type: "TYPE_A", State: vcmdb.JobStateNew},
		{ID: "job-2", Type: "UNREGISTERED", State: vcmdb.JobStateProcessing},
		{ID: "job-3", Type: "TYPE_A", State: vcmdb.JobStateProcessing},
	}}
	coordinator := NewCoordinator(store, nil)

	var resumed []string
	require.NoError(t, coordinator.Register("TYPE_A", HandlerFunc(func(_ context.Context, job *vcmdb.Job) error {
		resumed = append(resumed, job.ID)
		return nil
	})))

	require.NoError(t, coordinator.ResumeOngoingJobs(context.Background()))
	assert.Equal(t, []string{"job-1", "job-3"}, resumed)
}

func TestCoordinatorResumeOngoingJobs_SkipsTerminalJobs(t *testing.T) {
	store := &stubJobStore{jobs: []vcmdb.Job{
		{ID: "job-new", Type: "TYPE_A", State: vcmdb.JobStateNew},
		{ID: "job-error", Type: "TYPE_A", State: vcmdb.JobStateError},
		{ID: "job-done", Type: "TYPE_A", State: vcmdb.JobStateDone},
		{ID: "job-processing", Type: "TYPE_A", State: vcmdb.JobStateProcessing},
	}}
	coordinator := NewCoordinator(store, nil)

	var resumed []string
	require.NoError(t, coordinator.Register("TYPE_A", HandlerFunc(func(_ context.Context, job *vcmdb.Job) error {
		resumed = append(resumed, job.ID)
		return nil
	})))

	require.NoError(t, coordinator.ResumeOngoingJobs(context.Background()))
	assert.Equal(t, []string{"job-new", "job-processing"}, resumed)
	assert.Empty(t, store.progressCalls)
}

func TestCoordinatorResumeOngoingJobs_ExpiresJobWithoutDispatching(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store := &stubJobStore{jobs: []vcmdb.Job{{
		ID:          "job-1",
		Type:        "TYPE_A",
		CurrentStep: "create-vm",
		DeadlineAt:  now.Add(-time.Minute),
	}}}
	coordinator := NewCoordinator(store, nil)
	coordinator.now = func() time.Time { return now }

	called := false
	require.NoError(t, coordinator.Register("TYPE_A", HandlerFunc(func(context.Context, *vcmdb.Job) error {
		called = true
		return nil
	})))

	require.NoError(t, coordinator.ResumeOngoingJobs(context.Background()))
	assert.False(t, called)
	require.Len(t, store.progressCalls, 1)
	assert.Equal(t, vcmdb.UpdateJobProgressParams{
		JobID:        "job-1",
		Step:         "create-vm",
		State:        vcmdb.JobStateError,
		TrackingID:   vsaerrors.ErrOperationDeadlineExceeded,
		ErrorMessage: "job deadline exceeded at 2026-08-24T11:59:00Z",
		Retriable:    false,
	}, store.progressCalls[0])
}

func TestCoordinatorResumeOngoingJobs_ContinuesAfterHandlerFailure(t *testing.T) {
	store := &stubJobStore{jobs: []vcmdb.Job{
		{ID: "job-1", Type: "TYPE_A"},
		{ID: "job-2", Type: "TYPE_A"},
	}}
	coordinator := NewCoordinator(store, nil)

	var resumed []string
	require.NoError(t, coordinator.Register("TYPE_A", HandlerFunc(func(_ context.Context, job *vcmdb.Job) error {
		resumed = append(resumed, job.ID)
		if job.ID == "job-1" {
			return errors.New("dispatch failed")
		}
		return nil
	})))

	err := coordinator.ResumeOngoingJobs(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "job-1")
	assert.Equal(t, []string{"job-1", "job-2"}, resumed)
}

func TestCoordinatorResumeOngoingJobs_ListFailure(t *testing.T) {
	coordinator := NewCoordinator(&stubJobStore{listErr: errors.New("database unavailable")}, nil)

	err := coordinator.ResumeOngoingJobs(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "list ongoing jobs")
}

func TestCoordinatorRegister_RejectsInvalidAndDuplicateHandlers(t *testing.T) {
	coordinator := NewCoordinator(&stubJobStore{}, nil)
	handler := HandlerFunc(func(context.Context, *vcmdb.Job) error { return nil })

	require.Error(t, coordinator.Register("", handler))
	require.Error(t, coordinator.Register("TYPE_A", nil))
	require.NoError(t, coordinator.Register("TYPE_A", handler))
	require.Error(t, coordinator.Register("TYPE_A", handler))
}
