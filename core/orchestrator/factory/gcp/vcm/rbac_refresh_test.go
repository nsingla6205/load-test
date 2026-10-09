package vcm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

const rbacRefreshRequestID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

// isConflictError reports whether err is a VCP resource-state-conflict error.
func isConflictError(err error) bool {
	var ce *vsaerrors.CustomError
	return errors.As(err, &ce) && ce.IsError(vsaerrors.ErrResourceStateConflictError)
}

func validRbacRefreshParams() *commonparams.RefreshRbacForPoolParams {
	return &commonparams.RefreshRbacForPoolParams{
		PoolID:      "gcnv-123",
		RbacFileURL: "gs://bucket/rbac.txt",
		VCM: &commonparams.VCMCreatePoolParams{
			DeploymentID:          "gcnv-123",
			RequestID:             rbacRefreshRequestID,
			Deadline:              time.Now().UTC().Add(time.Hour),
			OntapModeRbacFileURL:  "gs://bucket/rbac.txt",
			OntapModeRbacChecksum: "abc123",
		},
	}
}

func TestUpdateRbacForPoolById_Success_PersistsJobAndDispatches(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	params := validRbacRefreshParams()
	operationID, err := o.UpdateRbacForPoolById(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, rbacRefreshRequestID, operationID)
	assert.True(t, starter.called)
	assert.Equal(t, "gcnv-123", starter.gotInstance, "refresh workflow instance must be keyed on the deployment id so refreshes serialize per cluster")

	require.NotNil(t, store.created)
	assert.Equal(t, rbacRefreshRequestID, store.created.ID)
	assert.Equal(t, "gcnv-123", store.created.ResourceID)
	assert.Equal(t, vcmdb.JobTypeOntapModeRbacRefresh, store.created.Type)
	assert.Equal(t, vcmdb.JobStateNew, store.created.State)
	assert.True(t, store.created.DeadlineAt.Equal(params.VCM.Deadline.UTC()))
	assert.Empty(t, store.updated)

	var persisted commonparams.VCMCreatePoolParams
	require.NoError(t, json.Unmarshal([]byte(store.created.RequestPayload), &persisted))
	assert.Equal(t, "gs://bucket/rbac.txt", persisted.OntapModeRbacFileURL)
	assert.Equal(t, "abc123", persisted.OntapModeRbacChecksum)
}

func TestUpdateRbacForPoolById_InvalidParams_ReturnsError(t *testing.T) {
	tests := []struct {
		name    string
		params  *commonparams.RefreshRbacForPoolParams
		wantMsg string
	}{
		{name: "nil params", params: nil, wantMsg: "deployment id missing"},
		{name: "nil VCM", params: &commonparams.RefreshRbacForPoolParams{}, wantMsg: "deployment id missing"},
		{
			name:    "empty deployment id",
			params:  &commonparams.RefreshRbacForPoolParams{VCM: &commonparams.VCMCreatePoolParams{RequestID: "r"}},
			wantMsg: "deployment id missing",
		},
		{
			name:    "empty request id",
			params:  &commonparams.RefreshRbacForPoolParams{VCM: &commonparams.VCMCreatePoolParams{DeploymentID: "d"}},
			wantMsg: "request id missing",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			starter := &fakeStarter{}
			store := &fakeStore{}
			o := newTestOrchestrator(store, starter)

			_, err := o.UpdateRbacForPoolById(context.Background(), tc.params)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.False(t, starter.called)
			assert.Nil(t, store.created)
		})
	}
}

func TestUpdateRbacForPoolById_InFlightRefresh_RejectsNewRequest(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{ongoing: &vcmdb.Job{
		ID:         "existing-refresh",
		ResourceID: "gcnv-123",
		Type:       vcmdb.JobTypeOntapModeRbacRefresh,
		State:      vcmdb.JobStateProcessing,
	}}
	o := newTestOrchestrator(store, starter)

	operationID, err := o.UpdateRbacForPoolById(context.Background(), validRbacRefreshParams())

	require.Error(t, err)
	assert.True(t, isConflictError(err), "an in-flight refresh must be rejected with a resource-state conflict")
	assert.Equal(t, "existing-refresh", operationID, "the conflicting (in-progress) operation id must be returned so the caller can report it")
	assert.Equal(t, "gcnv-123", store.ongoingResource)
	assert.Equal(t, vcmdb.JobTypeOntapModeRbacRefresh, store.ongoingJobType)
	assert.False(t, starter.called, "a refresh already in flight for the deployment must not start a second workflow")
	assert.Nil(t, store.created, "no new job is persisted when a refresh is already in progress")
}

func TestUpdateRbacForPoolById_InFlightRefresh_SameRequestIDIsIdempotent(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{
		ongoing:   &vcmdb.Job{ID: rbacRefreshRequestID, ResourceID: "gcnv-123", Type: vcmdb.JobTypeOntapModeRbacRefresh, State: vcmdb.JobStateProcessing},
		createErr: fmt.Errorf("wrap: %w", vcmdb.ErrJobAlreadyExists),
		existing:  &vcmdb.Job{ID: rbacRefreshRequestID, State: vcmdb.JobStateProcessing},
	}
	o := newTestOrchestrator(store, starter)

	operationID, err := o.UpdateRbacForPoolById(context.Background(), validRbacRefreshParams())

	require.NoError(t, err)
	assert.Equal(t, rbacRefreshRequestID, operationID)
	assert.False(t, starter.called, "a retry of the same request id must not start a second workflow")
}

func TestUpdateRbacForPoolById_DuplicateRequestID_ReturnsSameOperation(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{
		createErr: fmt.Errorf("wrap: %w", vcmdb.ErrJobAlreadyExists),
		existing:  &vcmdb.Job{ID: rbacRefreshRequestID, State: vcmdb.JobStateDone},
	}
	o := newTestOrchestrator(store, starter)

	operationID, err := o.UpdateRbacForPoolById(context.Background(), validRbacRefreshParams())

	require.NoError(t, err)
	assert.Equal(t, rbacRefreshRequestID, operationID)
	assert.False(t, starter.called)
}

func TestUpdateRbacForPoolById_ConcurrentStart_RejectsAsConflict(t *testing.T) {
	// Two requests race past the in-flight check; the engine rejects the second
	// Start because the deployment-keyed instance already exists. The loser must
	// surface a conflict error, mark its own job ERROR, and report the winner's
	// operation id (found by re-querying after its own job is marked ERROR).
	starter := &fakeStarter{err: cbackend.ErrInstanceAlreadyExists}
	store := &fakeStore{ongoingSeq: []*vcmdb.Job{
		nil, // in-flight check: neither request has registered yet
		{ID: "winner-refresh", ResourceID: "gcnv-123", Type: vcmdb.JobTypeOntapModeRbacRefresh, State: vcmdb.JobStateProcessing}, // re-query: the winner
	}}
	o := newTestOrchestrator(store, starter)

	operationID, err := o.UpdateRbacForPoolById(context.Background(), validRbacRefreshParams())

	require.Error(t, err)
	assert.True(t, isConflictError(err), "a losing concurrent start must be rejected with a resource-state conflict")
	assert.Equal(t, "winner-refresh", operationID, "the winning operation id must be returned so the caller can report it")
	require.Len(t, store.updated, 1)
	assert.Equal(t, vcmdb.JobStateError, store.updated[0].State)
	assert.Equal(t, jobStepFailedToStart, store.updated[0].Step)
	assert.False(t, store.updated[0].Retriable)
}

func TestUpdateRbacForPoolById_DispatcherError_MarksJobError(t *testing.T) {
	starter := &fakeStarter{err: errors.New("boom")}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	operationID, err := o.UpdateRbacForPoolById(context.Background(), validRbacRefreshParams())

	require.Error(t, err)
	assert.Empty(t, operationID)
	assert.Contains(t, err.Error(), "failed to start RBAC refresh workflow")
	require.Len(t, store.updated, 1)
	assert.Equal(t, vcmdb.JobStateError, store.updated[0].State)
	assert.Equal(t, jobStepFailedToStart, store.updated[0].Step)
}
