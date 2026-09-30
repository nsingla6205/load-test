package vcm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	cbackend "github.com/cschleiden/go-workflows/backend"
	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// fakeStarter is a hand-written GoWorkflowStarter stub. It records what it was
// called with and returns canned values. An ErrInstanceAlreadyExists error
// models an idempotent reattach to an in-flight run.
type fakeStarter struct {
	called      bool
	gotInstance string
	gotCtx      context.Context
	gotArgs     []any
	err         error
}

func (f *fakeStarter) Start(ctx context.Context, instanceID string, _ interface{}, args ...any) (*gowf.Instance, error) {
	f.called = true
	f.gotInstance = instanceID
	f.gotCtx = ctx
	f.gotArgs = args
	return nil, f.err
}

// fakeStore records the durable-job calls so job creation and the
// failed-to-start ERROR cleanup can be asserted without a real Spanner backend.
type fakeStore struct {
	created   *vcmdb.Job
	createErr error
	updated   []vcmdb.UpdateJobProgressParams
	updateErr error
	// existing is returned by GetJob (used on the duplicate-request path).
	existing *vcmdb.Job
	getErr   error
	// ongoing is returned by GetOngoingJobByResource.
	ongoing         *vcmdb.Job
	ongoingErr      error
	ongoingResource string
	ongoingJobType  string
}

func (f *fakeStore) NodeProviderInput() (vsa.NodeProviderInput, error) {
	return vsa.NodeProviderInput{}, nil
}

func (f *fakeStore) CreateJob(_ context.Context, job *vcmdb.Job) error {
	f.created = job
	return f.createErr
}

func (f *fakeStore) GetJob(_ context.Context, _ string) (*vcmdb.Job, error) {
	return f.existing, f.getErr
}

func (f *fakeStore) GetOngoingJobByResource(_ context.Context, resourceID, jobType string) (*vcmdb.Job, error) {
	f.ongoingResource = resourceID
	f.ongoingJobType = jobType
	return f.ongoing, f.ongoingErr
}

func (f *fakeStore) UpdateJobProgress(_ context.Context, params vcmdb.UpdateJobProgressParams) error {
	f.updated = append(f.updated, params)
	return f.updateErr
}

func newTestOrchestrator(store Store, starter GoWorkflowStarter) *VCMOrchestrator {
	return NewVCMOrchestrator(store, starter, log.NewLogger())
}

func validParams() *commonparams.CreatePoolParams {
	return &commonparams.CreatePoolParams{
		VCM: &commonparams.VCMCreatePoolParams{
			DeploymentID: "gcnv-123",
			RequestID:    "3fa85f64-5717-4562-b3fc-2c963f66afa6",
			Deadline:     time.Now().UTC().Add(time.Hour),
		},
	}
}

func TestCreatePool_Success_ReturnsRequestIDAsOperationID(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	params := validParams()
	pool, operationID, err := o.CreatePool(context.Background(), params)

	require.NoError(t, err)
	assert.Nil(t, pool, "VCM is async; no pool model is returned")
	assert.Equal(t, "3fa85f64-5717-4562-b3fc-2c963f66afa6", operationID,
		"operation ID must be the caller-supplied request ID (AIP-155)")
	assert.True(t, starter.called)
	assert.Equal(t, "3fa85f64-5717-4562-b3fc-2c963f66afa6", starter.gotInstance,
		"request/operation ID must be used as the idempotent workflow instance ID so the workflow drives its own job")

	// The durable job is created in NEW before dispatch, and no error cleanup runs.
	require.NotNil(t, store.created, "job must be persisted before dispatch")
	assert.Equal(t, operationID, store.created.ID)
	assert.Equal(t, "gcnv-123", store.created.ResourceID)
	assert.Equal(t, vcmdb.JobTypePoolCreate, store.created.Type)
	assert.Equal(t, vcmdb.JobStateNew, store.created.State)
	assert.True(t, store.created.DeadlineAt.Equal(params.VCM.Deadline.UTC()))
	assert.Empty(t, store.updated, "no ERROR cleanup expected on the success path")
}

func TestCreatePool_PersistsRequestPayloadForRecovery(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	params := validParams()
	_, _, err := o.CreatePool(context.Background(), params)
	require.NoError(t, err)

	require.NotNil(t, store.created)
	require.NotEmpty(t, store.created.RequestPayload,
		"the request payload must be persisted so a crash-recovery restart can rebuild and re-dispatch the workflow")

	var got commonparams.VCMCreatePoolParams
	require.NoError(t, json.Unmarshal([]byte(store.created.RequestPayload), &got),
		"persisted payload must round-trip into the workflow input type")
	assert.Equal(t, params.VCM.DeploymentID, got.DeploymentID)
	assert.Equal(t, params.VCM.RequestID, got.RequestID)
}

func TestCreatePool_PayloadMarshalError_IsWrappedAndSkipsJobCreate(t *testing.T) {
	// If the request payload cannot be serialized, the job must not be created
	// and no workflow is dispatched. time.Time.MarshalJSON fails for a year
	// outside [0,9999], which deterministically forces the marshal to error.
	starter := &fakeStarter{}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	params := validParams()
	params.VCM.Deadline = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)

	_, operationID, err := o.CreatePool(context.Background(), params)

	require.Error(t, err)
	assert.Empty(t, operationID, "no operation id is returned when the payload cannot be marshaled")
	assert.Contains(t, err.Error(), "failed to marshal request payload")
	assert.Contains(t, err.Error(), params.VCM.RequestID, "the error must identify the job")
	assert.Nil(t, store.created, "job must not be persisted when the payload cannot be marshaled")
	assert.False(t, starter.called, "dispatch must not run when the payload cannot be marshaled")
	assert.Empty(t, store.updated, "no ERROR cleanup expected when the job was never created")
}

func TestCreatePool_JobCreateError_IsWrappedAndSkipsDispatch(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{createErr: errors.New("spanner down")}
	o := newTestOrchestrator(store, starter)

	_, _, err := o.CreatePool(context.Background(), validParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to persist pool creation job")
	assert.Contains(t, err.Error(), "spanner down")
	assert.False(t, starter.called, "dispatch must not run when job creation fails")
	assert.Empty(t, store.updated, "no ERROR cleanup when the job was never created")
}

func TestCreatePool_DuplicateJob_ReturnsConflictAndSkipsDispatch(t *testing.T) {
	// A duplicate request (an operation already exists for this idempotency key)
	// must be rejected with a conflict instead of starting a second workflow
	// that would fail its NEW-state precondition and strand the caller.
	starter := &fakeStarter{}
	store := &fakeStore{
		createErr: fmt.Errorf("wrap: %w", vcmdb.ErrJobAlreadyExists),
		existing: &vcmdb.Job{
			ID:    "3fa85f64-5717-4562-b3fc-2c963f66afa6",
			State: vcmdb.JobStateProcessing,
		},
	}
	o := newTestOrchestrator(store, starter)

	_, operationID, err := o.CreatePool(context.Background(), validParams())

	require.Error(t, err)
	assert.Empty(t, operationID, "a rejected duplicate returns no operation id")
	assert.False(t, starter.called, "a duplicate request must not dispatch a workflow")
	assert.Empty(t, store.updated, "no job update expected on the duplicate path")

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr), "duplicate must surface a CustomError")
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError),
		"duplicate must map to a resource-state conflict")
	require.NotNil(t, customErr.OriginalErr, "conflict must wrap the detailed cause")
	assert.Contains(t, customErr.OriginalErr.Error(), string(vcmdb.JobStateProcessing),
		"the conflict must carry the existing job's state")
}

func TestCreatePool_DuplicateJob_GetJobError_IsWrapped(t *testing.T) {
	// If the existing job cannot be read, surface a clear error rather than
	// silently dispatching a doomed workflow.
	starter := &fakeStarter{}
	store := &fakeStore{
		createErr: fmt.Errorf("wrap: %w", vcmdb.ErrJobAlreadyExists),
		getErr:    errors.New("spanner read failed"),
	}
	o := newTestOrchestrator(store, starter)

	_, _, err := o.CreatePool(context.Background(), validParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists but could not be read")
	assert.Contains(t, err.Error(), "spanner read failed")
	assert.False(t, starter.called, "dispatch must not run when the duplicate job cannot be read")
}

func TestCreatePool_InvalidParams_ReturnsError(t *testing.T) {
	tests := []struct {
		name    string
		params  *commonparams.CreatePoolParams
		wantMsg string
	}{
		{
			name:    "nil params",
			params:  nil,
			wantMsg: "deployment id missing",
		},
		{
			name:    "nil VCM",
			params:  &commonparams.CreatePoolParams{},
			wantMsg: "deployment id missing",
		},
		{
			name:    "empty deployment id",
			params:  &commonparams.CreatePoolParams{VCM: &commonparams.VCMCreatePoolParams{RequestID: "r"}},
			wantMsg: "deployment id missing",
		},
		{
			name:    "empty request id",
			params:  &commonparams.CreatePoolParams{VCM: &commonparams.VCMCreatePoolParams{DeploymentID: "d"}},
			wantMsg: "request id missing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			starter := &fakeStarter{}
			store := &fakeStore{}
			o := newTestOrchestrator(store, starter)

			_, _, err := o.CreatePool(context.Background(), tc.params)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.False(t, starter.called, "starter must not be invoked on validation failure")
			assert.Nil(t, store.created, "job must not be created on validation failure")
		})
	}
}

func TestCreatePool_DispatcherError_IsWrappedAndMarksJobError(t *testing.T) {
	starter := &fakeStarter{err: errors.New("boom")}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	_, _, err := o.CreatePool(context.Background(), validParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start pool workflow")
	assert.Contains(t, err.Error(), "boom")

	// A dispatch failure must not strand the job in NEW: it is marked ERROR.
	require.NotNil(t, store.created)
	require.Len(t, store.updated, 1, "the failed-to-start guard must mark the job ERROR once")
	assert.Equal(t, store.created.ID, store.updated[0].JobID)
	assert.Equal(t, vcmdb.JobStateError, store.updated[0].State)
	assert.Contains(t, store.updated[0].ErrorMessage, "boom")
}

func TestCreatePool_DispatcherError_UpdateFailure_StillReturnsDispatchError(t *testing.T) {
	// When dispatch fails AND the follow-up "mark job ERROR" write also fails,
	// the update error is only logged (best-effort); the caller still gets the
	// original dispatch failure so the job is not silently swallowed.
	starter := &fakeStarter{err: errors.New("dispatch boom")}
	store := &fakeStore{updateErr: errors.New("spanner write failed")}
	o := newTestOrchestrator(store, starter)

	_, operationID, err := o.CreatePool(context.Background(), validParams())

	require.Error(t, err)
	assert.Empty(t, operationID)
	assert.Contains(t, err.Error(), "failed to start pool workflow")
	assert.Contains(t, err.Error(), "dispatch boom")
	// The ERROR-marking write was attempted even though it failed.
	require.Len(t, store.updated, 1)
	assert.Equal(t, vcmdb.JobStateError, store.updated[0].State)
}

func validUpdateParams() *commonparams.UpdatePoolParams {
	return &commonparams.UpdatePoolParams{
		VCM: &commonparams.VCMUpdatePoolParams{
			VCMCreatePoolParams: commonparams.VCMCreatePoolParams{
				DeploymentID: "gcnv-123",
				RequestID:    "3fa85f64-5717-4562-b3fc-2c963f66afa6",
				Deadline:     time.Now().UTC().Add(time.Hour),
			},
			UpdateMask: "clusterShape.throughputMibps,clusterShape.iops,clusterShape.sizeGib",
			ChangeType: "SCALE",
		},
	}
}

func TestUpdatePool_Success_ReturnsRequestIDAsOperationID(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	params := validUpdateParams()
	pool, operationID, err := o.UpdatePool(context.Background(), params)

	require.NoError(t, err)
	assert.Nil(t, pool, "VCM is async; no pool model is returned")
	assert.Equal(t, "3fa85f64-5717-4562-b3fc-2c963f66afa6", operationID)
	assert.True(t, starter.called)
	assert.Equal(t, "3fa85f64-5717-4562-b3fc-2c963f66afa6", starter.gotInstance)

	require.NotNil(t, store.created)
	assert.Equal(t, operationID, store.created.ID)
	assert.Equal(t, "gcnv-123", store.created.ResourceID)
	assert.Equal(t, vcmdb.JobTypePoolUpdate, store.created.Type)
	assert.Equal(t, vcmdb.JobStateNew, store.created.State)
	assert.True(t, store.created.DeadlineAt.Equal(params.VCM.Deadline.UTC()))
	assert.Empty(t, store.updated)
	assert.Equal(t, "gcnv-123", store.ongoingResource)
	assert.Equal(t, vcmdb.JobTypePoolUpdate, store.ongoingJobType)
}

func TestUpdatePool_PersistsRequestPayloadForRecovery(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	params := validUpdateParams()
	_, _, err := o.UpdatePool(context.Background(), params)
	require.NoError(t, err)

	require.NotNil(t, store.created)
	var got commonparams.VCMUpdatePoolParams
	require.NoError(t, json.Unmarshal([]byte(store.created.RequestPayload), &got))
	assert.Equal(t, params.VCM.DeploymentID, got.DeploymentID)
	assert.Equal(t, params.VCM.RequestID, got.RequestID)
	assert.Equal(t, params.VCM.UpdateMask, got.UpdateMask)
	assert.Equal(t, params.VCM.ChangeType, got.ChangeType)
}

func TestUpdatePool_InvalidParams_ReturnsError(t *testing.T) {
	tests := []struct {
		name    string
		params  *commonparams.UpdatePoolParams
		wantMsg string
	}{
		{name: "nil params", params: nil, wantMsg: "deployment id missing"},
		{name: "nil VCM", params: &commonparams.UpdatePoolParams{}, wantMsg: "deployment id missing"},
		{
			name:    "empty deployment id",
			params:  &commonparams.UpdatePoolParams{VCM: &commonparams.VCMUpdatePoolParams{VCMCreatePoolParams: commonparams.VCMCreatePoolParams{RequestID: "r"}}},
			wantMsg: "deployment id missing",
		},
		{
			name:    "empty request id",
			params:  &commonparams.UpdatePoolParams{VCM: &commonparams.VCMUpdatePoolParams{VCMCreatePoolParams: commonparams.VCMCreatePoolParams{DeploymentID: "d"}}},
			wantMsg: "request id missing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			starter := &fakeStarter{}
			store := &fakeStore{}
			o := newTestOrchestrator(store, starter)

			_, _, err := o.UpdatePool(context.Background(), tc.params)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.False(t, starter.called)
			assert.Nil(t, store.created)
		})
	}
}

func TestUpdatePool_InFlightJob_ReturnsConflictAndSkipsDispatch(t *testing.T) {
	tests := []struct {
		name  string
		state vcmdb.JobState
	}{
		{name: "new", state: vcmdb.JobStateNew},
		{name: "processing", state: vcmdb.JobStateProcessing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			starter := &fakeStarter{}
			store := &fakeStore{ongoing: &vcmdb.Job{
				ID:         "existing-update",
				ResourceID: "gcnv-123",
				Type:       vcmdb.JobTypePoolUpdate,
				State:      tc.state,
			}}
			o := newTestOrchestrator(store, starter)

			params := validUpdateParams()
			params.VCM.RequestID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
			_, operationID, err := o.UpdatePool(context.Background(), params)

			require.Error(t, err)
			assert.Empty(t, operationID)
			assert.False(t, starter.called, "an in-flight update must not start another workflow")
			assert.Nil(t, store.created, "an in-flight update must not persist a second job")
			assert.Equal(t, "gcnv-123", store.ongoingResource)
			assert.Equal(t, vcmdb.JobTypePoolUpdate, store.ongoingJobType)

			var customErr *vsaerrors.CustomError
			require.True(t, vsaerrors.As(err, &customErr))
			assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
			require.NotNil(t, customErr.OriginalErr)
			assert.Contains(t, customErr.OriginalErr.Error(), "existing-update")
			assert.Contains(t, customErr.OriginalErr.Error(), string(tc.state))
		})
	}
}

func TestUpdatePool_OngoingLookupError_SkipsDispatch(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{ongoingErr: errors.New("spanner unavailable")}
	o := newTestOrchestrator(store, starter)

	_, operationID, err := o.UpdatePool(context.Background(), validUpdateParams())

	require.Error(t, err)
	assert.Empty(t, operationID)
	assert.False(t, starter.called)
	assert.Nil(t, store.created)
	assert.Contains(t, err.Error(), "failed to check in-flight pool update")
	assert.Contains(t, err.Error(), "spanner unavailable")
}

func TestUpdatePool_DuplicateJob_ReturnsConflictAndSkipsDispatch(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{
		createErr: fmt.Errorf("wrap: %w", vcmdb.ErrJobAlreadyExists),
		existing: &vcmdb.Job{
			ID:    "3fa85f64-5717-4562-b3fc-2c963f66afa6",
			State: vcmdb.JobStateProcessing,
		},
	}
	o := newTestOrchestrator(store, starter)

	_, operationID, err := o.UpdatePool(context.Background(), validUpdateParams())

	require.Error(t, err)
	assert.Empty(t, operationID)
	assert.False(t, starter.called)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
	require.NotNil(t, customErr.OriginalErr)
	assert.Contains(t, customErr.OriginalErr.Error(), string(vcmdb.JobStateProcessing))
}

func TestUpdatePool_DispatcherError_IsWrappedAndMarksJobError(t *testing.T) {
	starter := &fakeStarter{err: errors.New("boom")}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	_, _, err := o.UpdatePool(context.Background(), validUpdateParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start pool update workflow")
	assert.Contains(t, err.Error(), "boom")
	require.NotNil(t, store.created)
	require.Len(t, store.updated, 1)
	assert.Equal(t, vcmdb.JobStateError, store.updated[0].State)
}

func TestCreatePool_AttachedToExistingRun_StillSucceeds(t *testing.T) {
	// ErrInstanceAlreadyExists means an instance for this operation was already
	// running; that is an idempotent retry, not an error.
	starter := &fakeStarter{err: cbackend.ErrInstanceAlreadyExists}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	_, operationID, err := o.CreatePool(context.Background(), validParams())

	require.NoError(t, err)
	assert.Equal(t, "3fa85f64-5717-4562-b3fc-2c963f66afa6", operationID)
	assert.Empty(t, store.updated, "attach is a success; no ERROR cleanup expected")
}

// CreatePool must hand the workflow starter a context carrying the inbound
// correlation id and the deployment id on the propagator keys, so workflow,
// activity and VLM child logs are all attributable.
func TestCreatePool_PropagatesCorrelationAndDeployment(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-abc")

	_, _, err := o.CreatePool(ctx, validParams())
	require.NoError(t, err)

	require.NotNil(t, starter.gotCtx)
	assert.Equal(t, "corr-abc", orchestrator.CorrelationIDFromContext(starter.gotCtx))
	assert.Equal(t, "gcnv-123", orchestrator.DeploymentIDFromContext(starter.gotCtx))
}

// UpdatePool must hand the workflow starter the same request context as
// CreatePool: the inbound correlation id plus the deployment id on the
// propagator key.
func TestUpdatePool_PropagatesCorrelationAndDeployment(t *testing.T) {
	starter := &fakeStarter{}
	store := &fakeStore{}
	o := newTestOrchestrator(store, starter)

	ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-abc")

	_, _, err := o.UpdatePool(ctx, validUpdateParams())
	require.NoError(t, err)

	require.NotNil(t, starter.gotCtx)
	assert.Equal(t, "corr-abc", orchestrator.CorrelationIDFromContext(starter.gotCtx))
	assert.Equal(t, "gcnv-123", orchestrator.DeploymentIDFromContext(starter.gotCtx))
}
