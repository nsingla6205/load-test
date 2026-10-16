package opsinventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	commonpb "go.temporal.io/api/common/v1"
	deploymentpb "go.temporal.io/api/deployment/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	sdkpb "go.temporal.io/api/sdk/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func payload(s string) *commonpb.Payloads {
	return &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: []byte(s)}}}
}

func describe(workflowID string, status enumspb.WorkflowExecutionStatus) *workflowservice.DescribeWorkflowExecutionResponse {
	start := timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	resp := &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
			Execution:     &commonpb.WorkflowExecution{WorkflowId: workflowID, RunId: "run-1"},
			Type:          &commonpb.WorkflowType{Name: "CreatePoolWorkflow"},
			TaskQueue:     "customer-workflows",
			Status:        status,
			StartTime:     start,
			ExecutionTime: start,
			HistoryLength: 42,
		},
	}
	if status != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		resp.WorkflowExecutionInfo.CloseTime = timestamppb.New(time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC))
	}
	return resp
}

// describeWithPayloads fills every field this API must never map with the sentinel.
func describeWithPayloads(_ context.Context, workflowID string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	resp := describe(workflowID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING)
	resp.WorkflowExecutionInfo.Memo = &commonpb.Memo{Fields: map[string]*commonpb.Payload{"m": {Data: []byte(sentinel)}}}
	resp.WorkflowExecutionInfo.SearchAttributes = &commonpb.SearchAttributes{IndexedFields: map[string]*commonpb.Payload{"s": {Data: []byte(sentinel)}}}
	resp.PendingActivities = []*workflowpb.PendingActivityInfo{{
		ActivityId:       "a1",
		ActivityType:     &commonpb.ActivityType{Name: "CreateOntapVolume"},
		State:            enumspb.PENDING_ACTIVITY_STATE_STARTED,
		Attempt:          3,
		HeartbeatDetails: payload(sentinel),
		LastFailure: &failurepb.Failure{
			Message: sentinel, StackTrace: sentinel,
			FailureInfo: &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
				Type: "OntapError", Details: payload(sentinel),
			}},
		},
	}}
	resp.PendingChildren = []*workflowpb.PendingChildExecutionInfo{{WorkflowId: "child-1", WorkflowTypeName: "ChildWorkflow"}}
	resp.ExecutionConfig = &workflowpb.WorkflowExecutionConfig{UserMetadata: &sdkpb.UserMetadata{Summary: &commonpb.Payload{Data: []byte(sentinel)}, Details: &commonpb.Payload{Data: []byte(sentinel)}}}
	return resp, nil
}

// describeFull fills every execution metadata field the API maps.
func describeFull() *workflowservice.DescribeWorkflowExecutionResponse {
	at := func(h int) *timestamppb.Timestamp {
		return timestamppb.New(time.Date(2026, 10, 1, h, 0, 0, 0, time.UTC))
	}
	resp := describe("wf-full", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING)
	info := resp.WorkflowExecutionInfo
	info.HistorySizeBytes = 2048
	info.StateTransitionCount = 17
	info.FirstRunId = "run-0"
	info.ParentExecution = &commonpb.WorkflowExecution{WorkflowId: "parent-wf", RunId: "parent-run"}
	info.RootExecution = &commonpb.WorkflowExecution{WorkflowId: "root-wf", RunId: "root-run"}
	info.VersioningInfo = &workflowpb.WorkflowExecutionVersioningInfo{
		Behavior:          enumspb.VERSIONING_BEHAVIOR_PINNED,
		DeploymentVersion: &deploymentpb.WorkerDeploymentVersion{DeploymentName: "vcp-worker", BuildId: "build-42"},
	}
	resp.ExecutionConfig = &workflowpb.WorkflowExecutionConfig{
		WorkflowExecutionTimeout:   durationpb.New(24 * time.Hour),
		WorkflowRunTimeout:         durationpb.New(2 * time.Hour),
		DefaultWorkflowTaskTimeout: durationpb.New(10 * time.Second),
		UserMetadata:               &sdkpb.UserMetadata{Summary: &commonpb.Payload{Data: []byte(sentinel)}},
	}
	resp.WorkflowExtendedInfo = &workflowpb.WorkflowExecutionExtendedInfo{
		ExecutionExpirationTime: at(20), RunExpirationTime: at(2), CancelRequested: true, LastResetTime: at(1),
	}
	resp.PendingWorkflowTask = &workflowpb.PendingWorkflowTaskInfo{
		State: enumspb.PENDING_WORKFLOW_TASK_STATE_STARTED, Attempt: 4, ScheduledTime: at(3), OriginalScheduledTime: at(1), StartedTime: at(3),
	}
	resp.PendingActivities = []*workflowpb.PendingActivityInfo{{
		ActivityId: "7", ActivityType: &commonpb.ActivityType{Name: "WaitForOntapJob"}, State: enumspb.PENDING_ACTIVITY_STATE_SCHEDULED,
		Attempt: 5, MaximumAttempts: 10, ScheduledTime: at(4), LastStartedTime: at(3), LastHeartbeatTime: at(3),
		LastAttemptCompleteTime: at(3), NextAttemptScheduleTime: at(5), CurrentRetryInterval: durationpb.New(1500 * time.Millisecond),
		ExpirationTime: at(6), Paused: true, LastWorkerIdentity: "1@vcp-worker-abc", HeartbeatDetails: payload(sentinel),
	}}
	resp.PendingChildren = []*workflowpb.PendingChildExecutionInfo{{
		WorkflowId: "child-1", RunId: "child-run", WorkflowTypeName: "ChildWorkflow", ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	}}
	return resp
}

// newMockJobReader returns a job reader over a mock store, for failure paths
// the seeded database cannot express.
func newMockJobReader(t *testing.T, job *datamodel.Job, d WorkflowDescriber) (*jobReader, *readstore.MockReadStore) {
	store := readstore.NewMockReadStore(t)
	store.EXPECT().GetJobByUUID(mock.Anything, job.UUID).Return(job, nil)
	if job.AccountID.Valid {
		store.EXPECT().GetAccountsByIDs(mock.Anything, []int64{job.AccountID.Int64}).
			Return(map[int64]*datamodel.Account{job.AccountID.Int64: {Name: "1111"}}, nil).Maybe()
	}
	return NewJobReader(store, d).(*jobReader), store
}

func newSeqJob(attrs *datamodel.JobAttributes, account sql.NullInt64) *datamodel.Job {
	return &datamodel.Job{
		BaseModel: datamodel.BaseModel{ID: 9, UUID: "job-x"}, Type: string(datamodel.JobTypeDeleteVolume), State: "NEW",
		WorkflowID: "wf-x", AccountID: account, JobAttributes: attrs,
	}
}

func TestJobSequencerLookupFailures(t *testing.T) {
	valid := sql.NullInt64{Int64: 7, Valid: true}
	for _, tc := range []struct {
		name  string
		job   *datamodel.Job
		setup func(store *readstore.MockReadStore)
	}{
		{"admin job without account", newSeqJob(&datamodel.JobAttributes{PoolUUID: "p"}, sql.NullInt64{}), nil},
		{"no pool uuid", newSeqJob(&datamodel.JobAttributes{}, valid), nil},
		{"no job attributes", newSeqJob(nil, valid), nil},
		{"pool not found", newSeqJob(&datamodel.JobAttributes{PoolUUID: "p"}, valid), func(s *readstore.MockReadStore) {
			s.EXPECT().GetPoolByUUID(mock.Anything, "p").Return(nil, readstore.ErrNotFound)
		}},
		{"pool of another account", newSeqJob(&datamodel.JobAttributes{PoolUUID: "p"}, valid), func(s *readstore.MockReadStore) {
			s.EXPECT().GetPoolByUUID(mock.Anything, "p").Return(&datamodel.Pool{AccountID: 8, VendorID: "/projects/1/locations/l/pools/p"}, nil)
		}},
		{"bad vendor id", newSeqJob(&datamodel.JobAttributes{PoolUUID: "p"}, valid), func(s *readstore.MockReadStore) {
			s.EXPECT().GetPoolByUUID(mock.Anything, "p").Return(&datamodel.Pool{AccountID: 7, VendorID: "garbage"}, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDescriber{}
			r, store := newMockJobReader(t, tc.job, d)
			if tc.setup != nil {
				tc.setup(store)
			}
			res, err := r.Get(context.Background(), GetRequest{UUID: tc.job.UUID})
			require.NoError(t, err)
			assert.Equal(t, oasgenserver.OpsJobTemporalV1EnrichmentStatusNOTFOUND, res.Value.OpsJobV1.Temporal.EnrichmentStatus)
			assert.Equal(t, []string{"wf-x"}, d.calls, "no sequencer describe")
		})
	}
}

func TestJobTemporalBudget(t *testing.T) {
	t.Run("timeout is UNAVAILABLE", func(t *testing.T) {
		d := &fakeDescriber{fn: func(ctx context.Context, _ string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		r, _ := newMockJobReader(t, newSeqJob(nil, sql.NullInt64{}), d)
		r.budget = 20 * time.Millisecond
		start := time.Now()
		res, err := r.Get(context.Background(), GetRequest{UUID: "job-x"})
		require.NoError(t, err)
		assert.Less(t, time.Since(start), time.Second)
		temporal := res.Value.OpsJobV1.Temporal
		assert.Equal(t, oasgenserver.OpsJobTemporalV1EnrichmentStatusUNAVAILABLE, temporal.EnrichmentStatus)
		assert.Equal(t, "temporal describe timed out", temporal.Reason.Or(""))
	})

	t.Run("both describes share one deadline", func(t *testing.T) {
		job := newSeqJob(&datamodel.JobAttributes{PoolUUID: "p"}, sql.NullInt64{Int64: 7, Valid: true})
		d := &fakeDescriber{fn: func(_ context.Context, id string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
			if id == "wf-x" {
				time.Sleep(10 * time.Millisecond)
				return nil, serviceerror.NewNotFound("not found")
			}
			return describe(id, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING), nil
		}}
		r, store := newMockJobReader(t, job, d)
		store.EXPECT().GetPoolByUUID(mock.Anything, "p").Return(&datamodel.Pool{AccountID: 7, Name: "pool", VendorID: "/projects/1/locations/us-east4/pools/pool"}, nil)
		res, err := r.Get(context.Background(), GetRequest{UUID: "job-x"})
		require.NoError(t, err)
		assert.Equal(t, oasgenserver.OpsJobTemporalV1EnrichmentStatusQUEUEDINSEQUENCER, res.Value.OpsJobV1.Temporal.EnrichmentStatus)
		require.Len(t, d.deadlines, 2)
		assert.False(t, d.deadlines[0].IsZero())
		assert.Equal(t, d.deadlines[0], d.deadlines[1], "the sequencer describe only gets what is left of the budget")
		assert.WithinDuration(t, time.Now().Add(temporalBudget), d.deadlines[0], time.Second)
	})

	t.Run("sequencer timeout keeps NOT_FOUND", func(t *testing.T) {
		job := newSeqJob(&datamodel.JobAttributes{PoolUUID: "p"}, sql.NullInt64{Int64: 7, Valid: true})
		d := &fakeDescriber{fn: func(ctx context.Context, id string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
			if id == "wf-x" {
				return nil, serviceerror.NewNotFound("not found")
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		r, store := newMockJobReader(t, job, d)
		r.budget = 20 * time.Millisecond
		store.EXPECT().GetPoolByUUID(mock.Anything, "p").Return(&datamodel.Pool{AccountID: 7, Name: "pool", VendorID: "/projects/1/locations/us-east4/pools/pool"}, nil)
		res, err := r.Get(context.Background(), GetRequest{UUID: "job-x"})
		require.NoError(t, err)
		assert.Equal(t, oasgenserver.OpsJobTemporalV1EnrichmentStatusNOTFOUND, res.Value.OpsJobV1.Temporal.EnrichmentStatus)
	})
}

func TestStateMismatch(t *testing.T) {
	closed := []enumspb.WorkflowExecutionStatus{
		enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, enumspb.WORKFLOW_EXECUTION_STATUS_FAILED,
		enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED, enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT,
		enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED,
	}
	for _, s := range closed {
		assert.True(t, stateMismatch("NEW", s), s.String())
		assert.True(t, stateMismatch("PROCESSING", s), s.String())
		assert.True(t, stateMismatch("WAIT_FOR_TEMPORAL", s), "a job waiting for a workflow whose run is closed is stuck: "+s.String())
		assert.False(t, stateMismatch("DONE", s), s.String())
		assert.False(t, stateMismatch("CANCELLED", s), s.String())
		assert.False(t, stateMismatch("ERROR", s), s.String())
	}
	assert.False(t, stateMismatch("PROCESSING", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING))
	assert.False(t, stateMismatch("WAIT_FOR_TEMPORAL", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING))
	assert.False(t, stateMismatch("NEW", enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW))
}

func TestFailureTypeAndReason(t *testing.T) {
	assert.Equal(t, "", failureType(nil))
	assert.Equal(t, "TimeoutFailure", failureType(&failurepb.Failure{Message: sentinel, FailureInfo: &failurepb.Failure_TimeoutFailureInfo{TimeoutFailureInfo: &failurepb.TimeoutFailureInfo{}}}))
	assert.Equal(t, "ApplicationFailure", failureType(&failurepb.Failure{FailureInfo: &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{}}}))
	assert.Equal(t, "Failure", failureType(&failurepb.Failure{Message: sentinel}))
	assert.Equal(t, "temporal describe failed: errorString", unavailableReason(context.Background(), errors.New(sentinel)))
}

func TestNewJobReader_Spec(t *testing.T) {
	spec := NewJobReader(nil, nil).Spec()
	assert.Equal(t, ResourceTypeJob, spec.Type)
	assert.True(t, spec.ProjectOptional, "admin jobs have no project")
	assert.False(t, spec.GetByName)
	assert.False(t, spec.NameFilter)
	assert.Equal(t, ParentSpec{Column: readstore.ColResourceUUID}, spec.ListParents[oasgenserver.OpsParentTypeV1Resource])
	assert.Equal(t, map[string]readstore.Column{FilterCorrelationID: readstore.ColCorrelationID, FilterType: readstore.ColType}, spec.ExtraFilters)
	for _, s := range []string{"NEW", "PROCESSING", "ERROR", "DONE", "WAIT_FOR_TEMPORAL", "CANCELLED"} {
		assert.Contains(t, spec.States, s)
	}
	assert.Len(t, spec.States, 6)
}

func TestJobReaderGet(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetJobByUUID(mock.Anything, "x").Return(nil, readstore.ErrNotFound)
		_, err := NewJobReader(store, &fakeDescriber{}).Get(ctx, GetRequest{UUID: "x"})
		assert.ErrorIs(t, err, readstore.ErrNotFound)
	})

	t.Run("WhenProjectLookupFails", func(t *testing.T) {
		boom := errors.New("db down")
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetJobByUUID(mock.Anything, "x").Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "x"}, AccountID: sql.NullInt64{Int64: 1, Valid: true}}, nil)
		store.EXPECT().GetAccountsByIDs(mock.Anything, []int64{1}).Return(nil, boom)
		_, err := NewJobReader(store, &fakeDescriber{}).Get(ctx, GetRequest{UUID: "x"})
		assert.ErrorIs(t, err, boom)
	})

	t.Run("WhenJobBelongsToAnotherProjectTemporalIsNotCalled", func(t *testing.T) {
		for name, account := range map[string]sql.NullInt64{"other project": {Int64: 2, Valid: true}, "admin job": {}} {
			store := readstore.NewMockReadStore(t)
			store.EXPECT().GetJobByUUID(mock.Anything, "x").Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "x"}, WorkflowID: "wf", AccountID: account}, nil)
			d := &fakeDescriber{}
			_, err := NewJobReader(store, d).Get(ctx, GetRequest{UUID: "x", Account: &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}})
			assert.ErrorIs(t, err, readstore.ErrNotFound, name)
			assert.Empty(t, d.calls, name)
		}
	})

	t.Run("WhenJobBelongsToTheRequestProject", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetJobByUUID(mock.Anything, "x").Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "x"}, WorkflowID: "wf", AccountID: sql.NullInt64{Int64: 1, Valid: true}}, nil)
		d := &fakeDescriber{}
		res, err := NewJobReader(store, d).Get(ctx, GetRequest{UUID: "x", Account: &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "1111"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"wf"}, d.calls)
		assert.Equal(t, "1111", res.Value.OpsJobV1.Job.ProjectNumber.Or(""), "the request project needs no account query")
	})

	t.Run("WhenDescriberIsNotConfigured", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetJobByUUID(mock.Anything, "x").Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "x"}, WorkflowID: "wf"}, nil)
		res, err := NewJobReader(store, nil).Get(ctx, GetRequest{UUID: "x"})
		require.NoError(t, err)
		assert.Equal(t, oasgenserver.OpsJobTemporalV1EnrichmentStatusSKIPPED, res.Value.OpsJobV1.Temporal.EnrichmentStatus)
		assert.Equal(t, oasgenserver.OpsSourceV1VcpDb, res.Value.OpsJobV1.Source)
		assert.Equal(t, "SKIPPED", res.EnrichmentStatus)
	})

	t.Run("WhenDescribeSucceeds", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetJobByUUID(mock.Anything, "x").Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "x"}, WorkflowID: "wf", State: "DONE"}, nil)
		d := &fakeDescriber{fn: func(_ context.Context, id string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
			return describe(id, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED), nil
		}}
		res, err := NewJobReader(store, d).Get(ctx, GetRequest{UUID: "x"})
		require.NoError(t, err)
		assert.Equal(t, oasgenserver.OpsSourceV1VcpDbAndTemporal, res.Value.OpsJobV1.Source)
		assert.Equal(t, "COMPLETE", res.EnrichmentStatus)
		assert.False(t, res.Value.OpsJobV1.Temporal.StateMismatch.Or(true), "DONE job with a closed run is not stuck")
	})

	t.Run("WhenDescribeReturnsNoInfo", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetJobByUUID(mock.Anything, "x").Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "x"}, WorkflowID: "wf"}, nil)
		d := &fakeDescriber{fn: func(context.Context, string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
			return &workflowservice.DescribeWorkflowExecutionResponse{}, nil
		}}
		res, err := NewJobReader(store, d).Get(ctx, GetRequest{UUID: "x"})
		require.NoError(t, err)
		temporal := res.Value.OpsJobV1.Temporal
		assert.Equal(t, oasgenserver.OpsJobTemporalV1EnrichmentStatusUNAVAILABLE, temporal.EnrichmentStatus)
		assert.Equal(t, "empty describe response", temporal.Reason.Or(""))
	})
}

func TestJobReaderList(t *testing.T) {
	ctx := context.Background()
	opts := readstore.ListOptions{Limit: 5}
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "1111"}
	rows := []datamodel.Job{
		{BaseModel: datamodel.BaseModel{ID: 1, UUID: "j1"}, WorkflowID: "wf-1", AccountID: sql.NullInt64{Int64: 1, Valid: true}},
		{BaseModel: datamodel.BaseModel{ID: 2, UUID: "j2"}, AccountID: sql.NullInt64{Int64: 2, Valid: true}},
		{BaseModel: datamodel.BaseModel{ID: 3, UUID: "j3"}, AccountID: sql.NullInt64{Int64: 2, Valid: true}},
		{BaseModel: datamodel.BaseModel{ID: 4, UUID: "admin"}, IsAdminJob: true},
	}

	t.Run("WhenRowsAreReturnedItNeverCallsTemporal", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListJobs(mock.Anything, opts).Return(rows, nil)
		store.EXPECT().GetAccountsByIDs(mock.Anything, []int64{2}).
			Return(map[int64]*datamodel.Account{2: {Name: "2222"}}, nil).Once()
		d := &fakeDescriber{}
		out, err := NewJobReader(store, d).List(ctx, ListRequest{Account: account, Options: opts})
		require.NoError(t, err)
		require.Len(t, out, 4)
		assert.Empty(t, d.calls)
		for _, r := range out {
			assert.Equal(t, oasgenserver.OpsJobTemporalV1EnrichmentStatusSKIPPED, r.Value.OpsJobV1.Temporal.EnrichmentStatus)
		}
		assert.Equal(t, "1111", out[0].Value.OpsJobV1.Job.ProjectNumber.Or(""))
		assert.Equal(t, "2222", out[2].Value.OpsJobV1.Job.ProjectNumber.Or(""), "one account query for the page")
		assert.Nil(t, out[3].OwnerAccountID, "admin jobs have no owner")
		assert.False(t, out[3].Value.OpsJobV1.Job.ProjectNumber.IsSet())
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListJobs(mock.Anything, opts).Return(nil, readstore.ErrInvalidQuery)
		_, err := NewJobReader(store, nil).List(ctx, ListRequest{Options: opts})
		assert.ErrorIs(t, err, readstore.ErrInvalidQuery)
	})
}

func TestJobResource(t *testing.T) {
	scheduled := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{ID: 9, UUID: "job-1"}, Type: "CREATE_POOL", State: "ERROR", TrackingID: 1003,
		ErrorDetails: sentinel, CorrelationID: "corr", RequestID: "req", ResourceUUID: "pool-1", ResourceName: "p1",
		WorkflowID: "wf", ScheduledAt: scheduled, AccountID: sql.NullInt64{Int64: 1, Valid: true},
		JobAttributes: &datamodel.JobAttributes{PoolUUID: "pool-1", PreviousStateDetails: sentinel, PayloadAttributes: map[string]any{"password": sentinel}},
	}
	res := jobResource(job, map[int64]*datamodel.Account{1: {Name: "1111"}})
	got := encoded(t, res.Value)
	details := got["job"].(map[string]any)
	assert.Equal(t, "job", got["resourceType"])
	assert.Equal(t, "Resource not found", details["errorMessage"])
	assert.Equal(t, float64(1003), details["trackingId"])
	assert.Equal(t, "pool-1", details["poolUuid"])
	assert.Equal(t, "1111", details["projectNumber"])
	assert.Equal(t, "2026-10-01T00:00:00Z", details["scheduledAt"])
	assert.NotContains(t, details, "errorDetails")
	assertNoSecrets(t, res.Value)
}

func TestMapDescribe(t *testing.T) {
	resp, err := describeWithPayloads(context.Background(), "wf")
	require.NoError(t, err)
	out := mapDescribe(&datamodel.Job{State: "PROCESSING"}, resp)
	assert.Equal(t, oasgenserver.OpsJobTemporalV1EnrichmentStatusCOMPLETE, out.EnrichmentStatus)
	assert.Equal(t, "RUNNING", out.Status.Or(""))
	assert.True(t, out.CloseTime.IsSet())
	assert.True(t, out.CloseTime.Null, "running workflows have closeTime null")
	require.Len(t, out.PendingActivities, 1)
	assert.Equal(t, "STARTED", out.PendingActivities[0].State.Or(""))
	assert.Equal(t, "OntapError", out.PendingActivities[0].LastFailureType.Or(""))
	require.Len(t, out.PendingChildren, 1)
	b, err := oasgenserver.NewOpsJobV1OpsResourceV1(oasgenserver.OpsJobV1{ResourceType: "job", Source: oasgenserver.OpsSourceV1VcpDbAndTemporal, Temporal: out}).MarshalJSON()
	require.NoError(t, err)
	assert.NotContains(t, string(b), sentinel)
}

func TestEnumName(t *testing.T) {
	assert.Equal(t, "RUNNING", workflowStatus(enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING))
	assert.Equal(t, "UNKNOWN", workflowStatus(enumspb.WorkflowExecutionStatus(999)))
	assert.Equal(t, "SCHEDULED", enumName(enumspb.PendingActivityState_name, int32(enumspb.PENDING_ACTIVITY_STATE_SCHEDULED), "PENDING_ACTIVITY_STATE_"))
}

func TestOptTimestamp(t *testing.T) {
	assert.False(t, optTimestamp(nil).IsSet())
	assert.False(t, optTimestamp(&timestamppb.Timestamp{Seconds: -1, Nanos: -1}).IsSet())
	assert.True(t, optTimestamp(timestamppb.Now()).IsSet())
}

func TestMapDescribeExecutionMetadata(t *testing.T) {
	t.Run("WhenEveryMetadataFieldIsSet", func(t *testing.T) {
		out := mapDescribe(&datamodel.Job{State: "PROCESSING"}, describeFull())
		b, err := oasgenserver.NewOpsJobV1OpsResourceV1(oasgenserver.OpsJobV1{ResourceType: "job", Source: oasgenserver.OpsSourceV1VcpDbAndTemporal, Temporal: out}).MarshalJSON()
		require.NoError(t, err)
		var got struct {
			Temporal map[string]any `json:"temporal"`
		}
		require.NoError(t, json.Unmarshal(b, &got))
		tm := got.Temporal
		for k, want := range map[string]any{
			"historySizeBytes": float64(2048), "stateTransitionCount": float64(17), "firstRunId": "run-0",
			"parentWorkflowId": "parent-wf", "parentRunId": "parent-run", "rootWorkflowId": "root-wf", "rootRunId": "root-run",
			"workerDeploymentName": "vcp-worker", "buildId": "build-42", "versioningBehavior": "PINNED",
			"workflowExecutionTimeoutSeconds": float64(86400), "workflowRunTimeoutSeconds": float64(7200), "workflowTaskTimeoutSeconds": float64(10),
			"executionExpirationTime": "2026-10-01T20:00:00Z", "runExpirationTime": "2026-10-01T02:00:00Z",
			"cancelRequested": true, "lastResetTime": "2026-10-01T01:00:00Z",
		} {
			assert.Equal(t, want, tm[k], k)
		}
		assert.NotContains(t, tm, "executionDurationSeconds", "only set when the run is closed")
		assert.Equal(t, map[string]any{"state": "STARTED", "attempt": float64(4), "scheduledTime": "2026-10-01T03:00:00Z",
			"originalScheduledTime": "2026-10-01T01:00:00Z", "startedTime": "2026-10-01T03:00:00Z"}, tm["pendingWorkflowTask"])
		activity := tm["pendingActivities"].([]any)[0].(map[string]any)
		for k, want := range map[string]any{
			"activityId": "7", "activityType": "WaitForOntapJob", "state": "SCHEDULED", "attempt": float64(5), "maximumAttempts": float64(10),
			"lastHeartbeatTime": "2026-10-01T03:00:00Z", "lastAttemptCompleteTime": "2026-10-01T03:00:00Z",
			"nextAttemptScheduleTime": "2026-10-01T05:00:00Z", "currentRetryIntervalSeconds": 1.5, "expirationTime": "2026-10-01T06:00:00Z",
			"paused": true, "lastWorkerIdentity": "1@vcp-worker-abc",
		} {
			assert.Equal(t, want, activity[k], k)
		}
		assert.Equal(t, map[string]any{"workflowId": "child-1", "runId": "child-run", "workflowType": "ChildWorkflow", "parentClosePolicy": "ABANDON"},
			tm["pendingChildren"].([]any)[0])
		assert.NotContains(t, string(b), sentinel, "user metadata and heartbeat payloads are never mapped")
	})

	t.Run("WhenTheRunIsClosed", func(t *testing.T) {
		resp := describe("wf", enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED)
		resp.WorkflowExecutionInfo.ExecutionDuration = durationpb.New(90 * time.Second)
		out := mapDescribe(&datamodel.Job{State: "DONE"}, resp)
		assert.Equal(t, 90.0, out.ExecutionDurationSeconds.Or(0))
	})

	t.Run("WhenOptionalPartsAreMissing", func(t *testing.T) {
		out := mapDescribe(&datamodel.Job{State: "PROCESSING"}, describe("wf", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING))
		assert.False(t, out.PendingWorkflowTask.IsSet())
		assert.False(t, out.VersioningBehavior.IsSet())
		assert.False(t, out.BuildId.IsSet())
		assert.False(t, out.WorkflowRunTimeoutSeconds.IsSet(), "no execution config")
		assert.False(t, out.ParentWorkflowId.IsSet())
		assert.Equal(t, false, out.CancelRequested.Or(true))
	})
}

func TestBuildIDAndDeploymentName(t *testing.T) {
	assert.Equal(t, "", buildID(&workflowpb.WorkflowExecutionInfo{}))
	assert.Equal(t, "stamp", buildID(&workflowpb.WorkflowExecutionInfo{MostRecentWorkerVersionStamp: &commonpb.WorkerVersionStamp{BuildId: "stamp"}}))
	assert.Equal(t, "assigned", buildID(&workflowpb.WorkflowExecutionInfo{AssignedBuildId: "assigned", MostRecentWorkerVersionStamp: &commonpb.WorkerVersionStamp{BuildId: "stamp"}}))
	assert.Equal(t, "dv", buildID(&workflowpb.WorkflowExecutionInfo{AssignedBuildId: "assigned",
		VersioningInfo: &workflowpb.WorkflowExecutionVersioningInfo{DeploymentVersion: &deploymentpb.WorkerDeploymentVersion{BuildId: "dv"}}}))
	assert.Equal(t, "top", workerDeploymentName(&workflowpb.WorkflowExecutionInfo{WorkerDeploymentName: "top"}))
	assert.Equal(t, "from-version", workerDeploymentName(&workflowpb.WorkflowExecutionInfo{
		VersioningInfo: &workflowpb.WorkflowExecutionVersioningInfo{DeploymentVersion: &deploymentpb.WorkerDeploymentVersion{DeploymentName: "from-version"}}}))
}

func TestOptSeconds(t *testing.T) {
	assert.False(t, optSeconds(nil).IsSet())
	assert.False(t, optSeconds(durationpb.New(0)).IsSet(), "zero means no limit in Temporal")
	assert.Equal(t, 0.25, optSeconds(durationpb.New(250*time.Millisecond)).Or(0))
}
