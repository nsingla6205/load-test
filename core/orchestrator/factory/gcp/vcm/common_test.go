package vcm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
)

func TestCreateJobOrGet_CreatesNewJob(t *testing.T) {
	store := &jobTestStore{}
	job := &vcmdb.Job{
		ID:         "op-1",
		ResourceID: "hg-1",
		Type:       "CREATE_HOSTGROUP",
		State:      vcmdb.JobStateNew,
		DeadlineAt: time.Now().UTC().Add(time.Hour),
	}

	alreadyExists, err := createJobOrGet(context.Background(), store, job)

	require.NoError(t, err)
	assert.False(t, alreadyExists)
	require.NotNil(t, store.created)
	assert.Equal(t, "op-1", store.created.ID)
}

func TestCreateJobOrGet_ReturnsExistingJob(t *testing.T) {
	existing := &vcmdb.Job{
		ID:    "op-1",
		State: vcmdb.JobStateProcessing,
	}
	store := &jobTestStore{
		createErr:   fmt.Errorf("%w: op-1", vcmdb.ErrJobAlreadyExists),
		existingJob: existing,
	}
	job := &vcmdb.Job{ID: "op-1"}

	alreadyExists, err := createJobOrGet(context.Background(), store, job)

	require.NoError(t, err)
	assert.True(t, alreadyExists)
}

func TestCreateJobOrGet_CreateError(t *testing.T) {
	store := &jobTestStore{createErr: errors.New("database unavailable")}
	job := &vcmdb.Job{ID: "op-1"}

	_, err := createJobOrGet(context.Background(), store, job)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist job")
}

func TestCreateJobOrGet_GetJobError(t *testing.T) {
	store := &jobTestStore{
		createErr: fmt.Errorf("%w: op-1", vcmdb.ErrJobAlreadyExists),
		getJobErr: errors.New("database unavailable"),
	}
	job := &vcmdb.Job{ID: "op-1"}

	_, err := createJobOrGet(context.Background(), store, job)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get existing job")
}

type jobTestStore struct {
	created     *vcmdb.Job
	createErr   error
	existingJob *vcmdb.Job
	getJobErr   error
}

func (s *jobTestStore) NodeProviderInput() (vsa.NodeProviderInput, error) {
	return vsa.NodeProviderInput{}, errors.New("not implemented")
}

func (s *jobTestStore) CreateJob(_ context.Context, job *vcmdb.Job) error {
	s.created = job
	return s.createErr
}

func (s *jobTestStore) GetJob(_ context.Context, _ string) (*vcmdb.Job, error) {
	if s.getJobErr != nil {
		return nil, s.getJobErr
	}
	return s.existingJob, nil
}

func (s *jobTestStore) UpdateJobProgress(_ context.Context, _ vcmdb.UpdateJobProgressParams) error {
	return nil
}
