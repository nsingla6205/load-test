package opsinventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/tasks"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// temporalBudget is the total time all Temporal calls of one get-job may take,
// well under the Core HTTP timeout.
const temporalBudget = 5 * time.Second

// WorkflowDescriber is the only Temporal call the job reader can make. The
// Temporal client.Client satisfies it; the reader cannot start, signal,
// cancel or terminate workflows because those methods are not in this interface.
type WorkflowDescriber interface {
	DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error)
}

var jobStates = stateSet([]string{
	string(datamodel.JobsStateNEW), string(datamodel.JobsStatePROCESSING), string(datamodel.JobsStateERROR),
	string(datamodel.JobsStateDONE), string(datamodel.JobsStateWaitForTemporal), string(datamodel.JobsStateCANCELLED),
})

type jobReader struct {
	store     readstore.ReadStore
	describer WorkflowDescriber
	budget    time.Duration
}

// NewJobReader returns the job reader. Get adds a read-only Temporal describe
// of the job's workflow; list is DB only.
func NewJobReader(store readstore.ReadStore, describer WorkflowDescriber) Reader {
	return &jobReader{store: store, describer: describer, budget: temporalBudget}
}

func (r *jobReader) Spec() Spec {
	return Spec{
		Type:          ResourceTypeJob,
		AccountColumn: readstore.ColAccountID,
		// Admin jobs have no account and are only reachable without a project.
		ProjectOptional: true,
		ListParents: map[ParentType]ParentSpec{
			// resource_uuid may point at any resource type, so it is a plain match.
			oasgenserver.OpsParentTypeV1Resource: {Column: readstore.ColResourceUUID},
		},
		StateColumn: readstore.ColState,
		States:      jobStates,
		TimeFields:  baseTimeFields,
		ExtraFilters: map[string]readstore.Column{
			FilterCorrelationID: readstore.ColCorrelationID,
			FilterType:          readstore.ColType,
		},
	}
}

func (r *jobReader) Get(ctx context.Context, req GetRequest) (Resource, error) {
	job, err := r.store.GetJobByUUID(ctx, req.UUID)
	if err != nil {
		return Resource{}, err
	}
	// Check the project before Temporal: a job of another project must not
	// cost a describe. The handler checks ownership again for every type.
	if req.Account != nil && (!job.AccountID.Valid || job.AccountID.Int64 != req.Account.ID) {
		return Resource{}, readstore.ErrNotFound
	}
	accounts, err := loadAccounts(ctx, r.store, req.Account, jobAccountIDs([]datamodel.Job{*job}))
	if err != nil {
		return Resource{}, err
	}
	res := jobResource(job, accounts)
	temporal := r.describe(ctx, job)
	res.Value.OpsJobV1.Temporal = temporal
	if temporal.EnrichmentStatus != oasgenserver.OpsJobTemporalV1EnrichmentStatusSKIPPED {
		res.Value.OpsJobV1.Source = oasgenserver.OpsSourceV1VcpDbAndTemporal
	}
	res.EnrichmentStatus = string(temporal.EnrichmentStatus)
	return res, nil
}

// List is DB only: one describe per row would be up to 50 Temporal calls per page.
func (r *jobReader) List(ctx context.Context, req ListRequest) ([]Resource, error) {
	rows, err := r.store.ListJobs(ctx, req.Options)
	if err != nil {
		return nil, err
	}
	// One account query for the whole page, not one per row.
	accounts, err := loadAccounts(ctx, r.store, req.Account, jobAccountIDs(rows))
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(rows))
	for i := range rows {
		out = append(out, jobResource(&rows[i], accounts))
	}
	return out, nil
}

func jobAccountIDs(jobs []datamodel.Job) []int64 {
	ids := make([]int64, 0, len(jobs))
	for _, job := range jobs {
		if job.AccountID.Valid {
			ids = append(ids, job.AccountID.Int64)
		}
	}
	return ids
}

// jobResource never maps ErrorDetails, payload attributes or state details:
// the error is returned as trackingId plus the catalog message.
func jobResource(job *datamodel.Job, accounts map[int64]*datamodel.Account) Resource {
	details := oasgenserver.OpsJobDetailsV1{
		UUID:          job.UUID,
		Type:          optStr(job.Type),
		State:         optStr(job.State),
		TrackingId:    oasgenserver.NewOptInt64(int64(job.TrackingID)),
		ErrorMessage:  catalogMessage(job.TrackingID),
		CorrelationId: optStr(job.CorrelationID),
		RequestId:     optStr(job.RequestID),
		ResourceUuid:  optStr(job.ResourceUUID),
		ResourceName:  optStr(job.ResourceName),
		WorkflowId:    optStr(job.WorkflowID),
		IsAdminJob:    oasgenserver.NewOptBool(job.IsAdminJob),
		ScheduledAt:   optTime(job.ScheduledAt),
		CreatedAt:     job.CreatedAt.UTC(),
		UpdatedAt:     job.UpdatedAt.UTC(),
		DeletedAt:     deletedAt(job.DeletedAt),
	}
	if job.JobAttributes != nil {
		details.PoolUuid = optStr(job.JobAttributes.PoolUUID)
	}
	var owner *int64
	if job.AccountID.Valid {
		owner = ownerID(job.AccountID.Int64)
		details.ProjectNumber = accountName(accounts[job.AccountID.Int64])
	}
	dto := oasgenserver.OpsJobV1{
		ResourceType: string(ResourceTypeJob),
		Source:       oasgenserver.OpsSourceV1VcpDb,
		Job:          details,
		Temporal:     oasgenserver.OpsJobTemporalV1{EnrichmentStatus: oasgenserver.OpsJobTemporalV1EnrichmentStatusSKIPPED},
	}
	return Resource{
		ID:             job.ID,
		OwnerAccountID: owner,
		Value:          oasgenserver.NewOpsJobV1OpsResourceV1(dto),
	}
}

// describe returns the allowlisted Temporal view of the job workflow. It never
// fails the request: Temporal errors become NOT_FOUND or UNAVAILABLE.
func (r *jobReader) describe(ctx context.Context, job *datamodel.Job) oasgenserver.OpsJobTemporalV1 {
	if job.WorkflowID == "" || r.describer == nil {
		return oasgenserver.OpsJobTemporalV1{EnrichmentStatus: oasgenserver.OpsJobTemporalV1EnrichmentStatusSKIPPED}
	}
	// One budget for every Temporal call of this request.
	ctx, cancel := context.WithTimeout(ctx, r.budget)
	defer cancel()

	resp, err := r.describer.DescribeWorkflowExecution(ctx, job.WorkflowID, "")
	var notFound *serviceerror.NotFound
	switch {
	case errors.As(err, &notFound):
		out := oasgenserver.OpsJobTemporalV1{
			EnrichmentStatus: oasgenserver.OpsJobTemporalV1EnrichmentStatusNOTFOUND,
			WorkflowId:       optStr(job.WorkflowID),
		}
		r.checkSequencer(ctx, job, &out)
		return out
	case err != nil:
		return oasgenserver.OpsJobTemporalV1{
			EnrichmentStatus: oasgenserver.OpsJobTemporalV1EnrichmentStatusUNAVAILABLE,
			WorkflowId:       optStr(job.WorkflowID),
			Reason:           oasgenserver.NewOptString(unavailableReason(ctx, err)),
		}
	case resp.GetWorkflowExecutionInfo() == nil:
		return oasgenserver.OpsJobTemporalV1{
			EnrichmentStatus: oasgenserver.OpsJobTemporalV1EnrichmentStatusUNAVAILABLE,
			WorkflowId:       optStr(job.WorkflowID),
			Reason:           oasgenserver.NewOptString("empty describe response"),
		}
	}
	return mapDescribe(job, resp)
}

// checkSequencer repeats the workflow supervisor check: a NEW job of a
// sequencer-backed type whose workflow is not found is likely still queued in
// the per-pool SequenceWorkflow. Any failure leaves the status NOT_FOUND.
func (r *jobReader) checkSequencer(ctx context.Context, job *datamodel.Job, out *oasgenserver.OpsJobTemporalV1) {
	if job.State != string(datamodel.JobsStateNEW) || !tasks.IsSequencerBackedJobType(job.Type) {
		return
	}
	if job.JobAttributes == nil || job.JobAttributes.PoolUUID == "" || !job.AccountID.Valid {
		return
	}
	pool, err := r.store.GetPoolByUUID(ctx, job.JobAttributes.PoolUUID)
	if err != nil || pool.AccountID != job.AccountID.Int64 {
		return
	}
	sequenceID, err := tasks.SequenceWorkflowID(job.AccountID.Int64, pool.VendorID, pool.Name)
	if err != nil {
		return
	}
	resp, err := r.describer.DescribeWorkflowExecution(ctx, sequenceID, "")
	if err != nil || resp.GetWorkflowExecutionInfo() == nil {
		return
	}
	status := resp.GetWorkflowExecutionInfo().GetStatus()
	if status != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		return
	}
	out.EnrichmentStatus = oasgenserver.OpsJobTemporalV1EnrichmentStatusQUEUEDINSEQUENCER
	out.SequenceWorkflowId = oasgenserver.NewOptString(sequenceID)
	out.SequenceWorkflowStatus = oasgenserver.NewOptString(workflowStatus(status))
}

// mapDescribe copies only execution metadata. Input, result, memo, search
// attribute values, user metadata, heartbeat details, failure messages and
// history are never read.
func mapDescribe(job *datamodel.Job, resp *workflowservice.DescribeWorkflowExecutionResponse) oasgenserver.OpsJobTemporalV1 {
	info := resp.GetWorkflowExecutionInfo()
	status := info.GetStatus()
	config := resp.GetExecutionConfig()
	extended := resp.GetWorkflowExtendedInfo()
	out := oasgenserver.OpsJobTemporalV1{
		EnrichmentStatus:                oasgenserver.OpsJobTemporalV1EnrichmentStatusCOMPLETE,
		WorkflowId:                      optStr(info.GetExecution().GetWorkflowId()),
		RunId:                           optStr(info.GetExecution().GetRunId()),
		WorkflowType:                    optStr(info.GetType().GetName()),
		TaskQueue:                       optStr(info.GetTaskQueue()),
		Status:                          optStr(workflowStatus(status)),
		StartTime:                       optTimestamp(info.GetStartTime()),
		ExecutionTime:                   optTimestamp(info.GetExecutionTime()),
		HistoryLength:                   oasgenserver.NewOptInt64(info.GetHistoryLength()),
		HistorySizeBytes:                oasgenserver.NewOptInt64(info.GetHistorySizeBytes()),
		StateTransitionCount:            oasgenserver.NewOptInt64(info.GetStateTransitionCount()),
		ExecutionDurationSeconds:        optSeconds(info.GetExecutionDuration()),
		FirstRunId:                      optStr(info.GetFirstRunId()),
		ParentWorkflowId:                optStr(info.GetParentExecution().GetWorkflowId()),
		ParentRunId:                     optStr(info.GetParentExecution().GetRunId()),
		RootWorkflowId:                  optStr(info.GetRootExecution().GetWorkflowId()),
		RootRunId:                       optStr(info.GetRootExecution().GetRunId()),
		WorkerDeploymentName:            optStr(workerDeploymentName(info)),
		BuildId:                         optStr(buildID(info)),
		WorkflowExecutionTimeoutSeconds: optSeconds(config.GetWorkflowExecutionTimeout()),
		WorkflowRunTimeoutSeconds:       optSeconds(config.GetWorkflowRunTimeout()),
		WorkflowTaskTimeoutSeconds:      optSeconds(config.GetDefaultWorkflowTaskTimeout()),
		ExecutionExpirationTime:         optTimestamp(extended.GetExecutionExpirationTime()),
		RunExpirationTime:               optTimestamp(extended.GetRunExpirationTime()),
		CancelRequested:                 oasgenserver.NewOptBool(extended.GetCancelRequested()),
		LastResetTime:                   optTimestamp(extended.GetLastResetTime()),
		StateMismatch:                   oasgenserver.NewOptBool(stateMismatch(job.State, status)),
	}
	if versioning := info.GetVersioningInfo(); versioning != nil {
		out.VersioningBehavior = optStr(enumName(enumspb.VersioningBehavior_name, int32(versioning.GetBehavior()), "VERSIONING_BEHAVIOR_"))
	}
	if wt := resp.GetPendingWorkflowTask(); wt != nil {
		out.PendingWorkflowTask = oasgenserver.NewOptOpsPendingWorkflowTaskV1(oasgenserver.OpsPendingWorkflowTaskV1{
			State:                 optStr(enumName(enumspb.PendingWorkflowTaskState_name, int32(wt.GetState()), "PENDING_WORKFLOW_TASK_STATE_")),
			Attempt:               oasgenserver.NewOptInt32(wt.GetAttempt()),
			ScheduledTime:         optTimestamp(wt.GetScheduledTime()),
			OriginalScheduledTime: optTimestamp(wt.GetOriginalScheduledTime()),
			StartedTime:           optTimestamp(wt.GetStartedTime()),
		})
	}
	if closeTime := info.GetCloseTime(); closeTime != nil && closeTime.IsValid() {
		out.CloseTime = oasgenserver.NewOptNilDateTime(closeTime.AsTime().UTC())
	} else {
		out.CloseTime.SetToNull()
	}
	for _, pa := range resp.GetPendingActivities() {
		out.PendingActivities = append(out.PendingActivities, oasgenserver.OpsPendingActivityV1{
			ActivityId:                  optStr(pa.GetActivityId()),
			ActivityType:                optStr(pa.GetActivityType().GetName()),
			State:                       optStr(enumName(enumspb.PendingActivityState_name, int32(pa.GetState()), "PENDING_ACTIVITY_STATE_")),
			Attempt:                     oasgenserver.NewOptInt32(pa.GetAttempt()),
			MaximumAttempts:             oasgenserver.NewOptInt32(pa.GetMaximumAttempts()),
			ScheduledTime:               optTimestamp(pa.GetScheduledTime()),
			LastStartedTime:             optTimestamp(pa.GetLastStartedTime()),
			LastHeartbeatTime:           optTimestamp(pa.GetLastHeartbeatTime()),
			LastAttemptCompleteTime:     optTimestamp(pa.GetLastAttemptCompleteTime()),
			NextAttemptScheduleTime:     optTimestamp(pa.GetNextAttemptScheduleTime()),
			CurrentRetryIntervalSeconds: optSeconds(pa.GetCurrentRetryInterval()),
			ExpirationTime:              optTimestamp(pa.GetExpirationTime()),
			Paused:                      oasgenserver.NewOptBool(pa.GetPaused()),
			LastWorkerIdentity:          optStr(pa.GetLastWorkerIdentity()),
			LastFailureType:             optStr(failureType(pa.GetLastFailure())),
		})
	}
	for _, child := range resp.GetPendingChildren() {
		out.PendingChildren = append(out.PendingChildren, oasgenserver.OpsPendingChildV1{
			WorkflowId:        optStr(child.GetWorkflowId()),
			RunId:             optStr(child.GetRunId()),
			WorkflowType:      optStr(child.GetWorkflowTypeName()),
			ParentClosePolicy: optStr(enumName(enumspb.ParentClosePolicy_name, int32(child.GetParentClosePolicy()), "PARENT_CLOSE_POLICY_")),
		})
	}
	return out
}

// stateMismatch is the stuck-job signal: the DB job is still active but the
// Temporal run is closed. It is computed only, never repaired. Active states
// match isNonTerminalJobState (database/vcp/job_metrics.go): NEW, PROCESSING
// and WAIT_FOR_TEMPORAL (workflow start failed, the orphan-job processor retries).
func stateMismatch(dbState string, status enumspb.WorkflowExecutionStatus) bool {
	active := dbState == string(datamodel.JobsStateNEW) || dbState == string(datamodel.JobsStatePROCESSING) ||
		dbState == string(datamodel.JobsStateWaitForTemporal)
	switch status {
	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, enumspb.WORKFLOW_EXECUTION_STATUS_FAILED,
		enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED, enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT,
		enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return active
	}
	return false
}

func workflowStatus(status enumspb.WorkflowExecutionStatus) string {
	return enumName(enumspb.WorkflowExecutionStatus_name, int32(status), "WORKFLOW_EXECUTION_STATUS_")
}

func enumName(names map[int32]string, value int32, prefix string) string {
	name, ok := names[value]
	if !ok {
		return "UNKNOWN"
	}
	return strings.TrimPrefix(name, prefix)
}

// failureType returns the failure kind or application error type, never the
// message, stack trace or details.
func failureType(f *failurepb.Failure) string {
	switch {
	case f == nil:
		return ""
	case f.GetApplicationFailureInfo() != nil:
		if t := f.GetApplicationFailureInfo().GetType(); t != "" {
			return t
		}
		return "ApplicationFailure"
	case f.GetTimeoutFailureInfo() != nil:
		return "TimeoutFailure"
	case f.GetCanceledFailureInfo() != nil:
		return "CanceledFailure"
	case f.GetTerminatedFailureInfo() != nil:
		return "TerminatedFailure"
	case f.GetServerFailureInfo() != nil:
		return "ServerFailure"
	case f.GetActivityFailureInfo() != nil:
		return "ActivityFailure"
	case f.GetChildWorkflowExecutionFailureInfo() != nil:
		return "ChildWorkflowExecutionFailure"
	case f.GetResetWorkflowFailureInfo() != nil:
		return "ResetWorkflowFailure"
	default:
		return "Failure"
	}
}

// unavailableReason is a short, fixed description of a Temporal error. The
// raw error text is not returned.
func unavailableReason(ctx context.Context, err error) string {
	var grpcDeadline *serviceerror.DeadlineExceeded
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.As(err, &grpcDeadline) {
		return "temporal describe timed out"
	}
	name := fmt.Sprintf("%T", err)
	name = name[strings.LastIndex(name, ".")+1:]
	return "temporal describe failed: " + name
}

// optSeconds returns a duration in seconds. Nil and zero (Temporal's "no
// limit") are left out.
func optSeconds(d *durationpb.Duration) oasgenserver.OptFloat64 {
	if d == nil || !d.IsValid() || d.AsDuration() <= 0 {
		return oasgenserver.OptFloat64{}
	}
	return oasgenserver.NewOptFloat64(d.AsDuration().Seconds())
}

// buildID is the worker build of the workflow: the Worker Deployment version
// when versioning is used, else the assigned build id, else the last worker stamp.
func buildID(info *workflowpb.WorkflowExecutionInfo) string {
	if id := info.GetVersioningInfo().GetDeploymentVersion().GetBuildId(); id != "" {
		return id
	}
	if id := info.GetAssignedBuildId(); id != "" {
		return id
	}
	return info.GetMostRecentWorkerVersionStamp().GetBuildId()
}

func workerDeploymentName(info *workflowpb.WorkflowExecutionInfo) string {
	if name := info.GetWorkerDeploymentName(); name != "" {
		return name
	}
	return info.GetVersioningInfo().GetDeploymentVersion().GetDeploymentName()
}

func optTimestamp(ts *timestamppb.Timestamp) oasgenserver.OptDateTime {
	if ts == nil || !ts.IsValid() {
		return oasgenserver.OptDateTime{}
	}
	return optTime(ts.AsTime())
}
