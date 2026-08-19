package vcm

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

type stubJobStore struct {
	params    vcmdb.UpdateJobProgressParams
	updateErr error
	called    bool

	// getJob* configure the GetJob response and record the requested id.
	getJob      *vcmdb.Job
	getJobErr   error
	gotJobID    string
	getJobCalls int
}

func (s *stubJobStore) CreateJob(context.Context, *vcmdb.Job) error { return nil }

func (s *stubJobStore) GetJob(_ context.Context, jobID string) (*vcmdb.Job, error) {
	s.getJobCalls++
	s.gotJobID = jobID
	return s.getJob, s.getJobErr
}

func (s *stubJobStore) ListOngoingJobs(context.Context) ([]vcmdb.Job, error) { return nil, nil }

func (s *stubJobStore) UpdateJobProgress(_ context.Context, params vcmdb.UpdateJobProgressParams) error {
	s.called = true
	s.params = params
	return s.updateErr
}

func TestJobActivity_mapErrorToJobProgressParams_CustomError(t *testing.T) {
	originalErr := errors.New("snapshot with that name already exists")
	customErr := vsaerrors.NewVCPError(vsaerrors.ErrCreateSnapshotConflict, originalErr)

	params := mapErrorToJobProgressParams(vcmdb.UpdateJobProgressParams{
		JobID: "job-1",
	}, customErr)

	assert.Equal(t, vcmdb.JobStateError, params.State)
	assert.Equal(t, vsaerrors.ErrCreateSnapshotConflict, params.TrackingID)
	assert.Equal(t, originalErr.Error(), params.ErrorMessage)
	assert.False(t, params.Retriable)
}

func TestJobActivity_mapErrorToJobProgressParams_PlainError(t *testing.T) {
	params := mapErrorToJobProgressParams(vcmdb.UpdateJobProgressParams{
		JobID: "job-2",
	}, errors.New("generic failure"))

	assert.Equal(t, vcmdb.JobStateError, params.State)
	assert.Equal(t, 0, params.TrackingID)
	assert.Equal(t, "generic failure", params.ErrorMessage)
	assert.False(t, params.Retriable)
}

func TestJobActivity_UpdateJobProgress_Success(t *testing.T) {
	store := &stubJobStore{}
	activity := JobActivity{Store: store}

	err := activity.UpdateJobProgress(
		context.Background(),
		vcmdb.UpdateJobProgressParams{
			JobID: "job-1",
			Step:  "RESOLVE_SVM",
			State: vcmdb.JobStateProcessing,
		},
	)

	require.NoError(t, err)
	assert.True(t, store.called)
	assert.Equal(t, "job-1", store.params.JobID)
	assert.Equal(t, "RESOLVE_SVM", store.params.Step)
	assert.Equal(t, vcmdb.JobStateProcessing, store.params.State)
	assert.Equal(t, 0, store.params.TrackingID)
	assert.Empty(t, store.params.ErrorMessage)
	assert.False(t, store.params.Retriable)
}

func TestJobActivity_UpdateJobProgress_PersistsWorkflowErrorFields(t *testing.T) {
	store := &stubJobStore{}
	activity := JobActivity{Store: store}

	err := activity.UpdateJobProgress(
		context.Background(),
		mapErrorToJobProgressParams(vcmdb.UpdateJobProgressParams{
			JobID: "job-2",
		}, vsaerrors.NewVCPError(
			vsaerrors.ErrCreateSnapshotConflict,
			errors.New("snapshot with that name already exists"),
		)),
	)

	require.NoError(t, err)
	assert.Equal(t, "job-2", store.params.JobID)
	assert.Equal(t, vcmdb.JobStateError, store.params.State)
	assert.Equal(t, vsaerrors.ErrCreateSnapshotConflict, store.params.TrackingID)
	assert.Equal(t, "snapshot with that name already exists", store.params.ErrorMessage)
	assert.False(t, store.params.Retriable)
}

func TestJobActivity_UpdateJobProgress_ErrorMessageSetsErrorState(t *testing.T) {
	store := &stubJobStore{}
	activity := JobActivity{Store: store}

	err := activity.UpdateJobProgress(
		context.Background(),
		vcmdb.UpdateJobProgressParams{
			JobID:        "job-legacy",
			ErrorMessage: "debug details only",
			TrackingID:   vsaerrors.ErrSVMNotFound,
		},
	)

	require.NoError(t, err)
	assert.Equal(t, vcmdb.JobStateError, store.params.State)
	assert.Equal(t, vsaerrors.ErrSVMNotFound, store.params.TrackingID)
	assert.Equal(t, "debug details only", store.params.ErrorMessage)
}

func TestJobActivity_UpdateJobProgress_NilStore(t *testing.T) {
	activity := JobActivity{}

	err := activity.UpdateJobProgress(
		context.Background(),
		vcmdb.UpdateJobProgressParams{
			JobID: "job-3",
			State: vcmdb.JobStateDone,
		},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "job store is not configured")
}

func TestJobActivity_GetJob_Success(t *testing.T) {
	want := &vcmdb.Job{ID: "job-1", State: vcmdb.JobStateNew}
	store := &stubJobStore{getJob: want}
	activity := JobActivity{Store: store}

	got, err := activity.GetJob(context.Background(), "job-1")

	require.NoError(t, err)
	assert.Same(t, want, got)
	assert.Equal(t, "job-1", store.gotJobID)
	assert.Equal(t, 1, store.getJobCalls)
}

func TestJobActivity_GetJob_NilStore(t *testing.T) {
	activity := JobActivity{}

	_, err := activity.GetJob(context.Background(), "job-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "job store is not configured")
}

func TestJobActivity_GetJob_StoreError(t *testing.T) {
	store := &stubJobStore{getJobErr: errors.New("record not found")}
	activity := JobActivity{Store: store}

	_, err := activity.GetJob(context.Background(), "missing-job")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get vcm job")
	assert.Contains(t, err.Error(), "missing-job")
	assert.Contains(t, err.Error(), "record not found")
}

func TestJobActivity_UpdateJobProgress_StoreError(t *testing.T) {
	store := &stubJobStore{updateErr: errors.New("database unavailable")}
	activity := JobActivity{Store: store}

	err := activity.UpdateJobProgress(
		context.Background(),
		vcmdb.UpdateJobProgressParams{
			JobID: "job-4",
			State: vcmdb.JobStateDone,
		},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "database unavailable")
	assert.True(t, store.called)
}
