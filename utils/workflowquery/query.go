// Package workflowquery queries a Temporal workflow by ID and returns status, error message (on failure), and metadata (on success).
package workflowquery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	commonpb "go.temporal.io/api/common/v1"
	enums "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
)

// historyFetcher is the minimal Temporal API used to page workflow execution history (test doubles implement this).
type historyFetcher interface {
	GetWorkflowExecutionHistory(ctx context.Context, in *workflowservice.GetWorkflowExecutionHistoryRequest, opts ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error)
}

// temporalQuerier is the subset of client.Client used by Query (test doubles implement this).
type temporalQuerier interface {
	DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error)
	History() historyFetcher
}

type sdkClientWrap struct{ c client.Client }

var temporalNamespace = env.GetString("TEMPORAL_NAMESPACE", "default")

func (w sdkClientWrap) DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	return w.c.DescribeWorkflowExecution(ctx, workflowID, runID)
}

func (w sdkClientWrap) History() historyFetcher {
	return w.c.WorkflowService()
}

// WorkflowError holds a structured error with a primary message and an optional cause.
type WorkflowError struct {
	Message string `json:"message"`
	Cause   string `json:"cause,omitempty"`
}

type OCICreatePoolVMMetadata struct {
	Name            string
	SerialNumber    string
	VSAManagementIP string
	InterclusterIP  string
	HAPair          string
	SizeInGiB       int64
	InstanceID      string
}

type OCICredentialRefMetadata struct {
	Ocid    string `json:"ocid,omitempty"`
	Version string `json:"version,omitempty"`
}

// OCICreatePoolCredentialsMetadata captures the OCI Vault references created during
// the OCICreatePoolWorkflow.
type OCICreatePoolCredentialsMetadata struct {
	Secret      *OCICredentialRefMetadata `json:"secret,omitempty"`
	Certificate *OCICredentialRefMetadata `json:"certificate,omitempty"`
}

type OCICreatePoolMediatorMetadata struct {
	Name       string `json:"name"`
	IP         string `json:"ip"`
	HAPair     string `json:"haPair"`
	InstanceID string `json:"instanceId"`
}

type OCISecurityAttributeMetadata struct {
	Value string `json:"value"`
	Mode  string `json:"mode"`
}

type OCIFabricPoolConfigMetadata struct {
	BucketName    string `json:"bucketName,omitempty"`
	SecretOCID    string `json:"secretOcid,omitempty"`
	SecretVersion *int64 `json:"secretVersion,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	ServerURL     string `json:"serverUrl,omitempty"`
}

type OCICreatePoolMetadata struct {
	PoolUUID       string                         `json:"poolUUID,omitempty"`
	PoolOCID       string                         `json:"poolOCID,omitempty"`
	ResourceName   string                         `json:"resourceName,omitempty"`
	ClusterIP      string                         `json:"clusterIP,omitempty"`
	Vms            []OCICreatePoolVMMetadata      `json:"vms,omitempty"`
	IOPS           int64                          `json:"iops,omitempty"`
	ThroughputGBps float64                        `json:"throughputGBps,omitempty"`
	Mediator       *OCICreatePoolMediatorMetadata `json:"mediator,omitempty"`
	// Credentials surfaces OCI Vault references captured from the
	// CreateOnTapCredentialsForOCI activity completion event. It is populated
	// only after the parent OCICreatePoolWorkflow has terminated successfully.
	Credentials        *OCICreatePoolCredentialsMetadata                  `json:"credentials,omitempty"`
	KmsID              string                                             `json:"kmsId,omitempty"`
	NsgIDs             []string                                           `json:"nsgIds,omitempty"`
	SecurityAttributes map[string]map[string]OCISecurityAttributeMetadata `json:"securityAttributes,omitempty"`
	FabricPoolConfig   *OCIFabricPoolConfigMetadata                       `json:"fabricPoolConfig,omitempty"`
}

type OCICreateSVMLifMetadata struct {
	Name      string   `json:"name"`
	IP        string   `json:"ipAddress"`
	Node      string   `json:"node"`
	NodeUUID  string   `json:"nodeUUID"`
	HaPair    *string  `json:"haPair,omitempty"`
	Protocols []string `json:"protocols"`
}

type OCICreateSVMMetadata struct {
	Name    string                    `json:"name"`
	SvmOCID string                    `json:"svmOCID"`
	Lifs    []OCICreateSVMLifMetadata `json:"lifs,omitempty"`
}

type WorkflowStatus string

const (
	WorkflowStatusInProgress WorkflowStatus = "in_progress"
	WorkflowStatusCompleted  WorkflowStatus = "completed"
	WorkflowStatusFailed     WorkflowStatus = "failed"
	WorkflowStatusTimedOut   WorkflowStatus = "timed_out"
)

// Result is the JSON-friendly workflow query result: status, optional error,
// and optional metadata. PoolMetadata and SvmMetadata are populated only once
// the workflow has terminated successfully and represent the final,
// authoritative payload of the OCICreatePoolWorkflow / OCICreateSVMWorkflow
// respectively.
type Result struct {
	Status       WorkflowStatus         `json:"status"`
	WorkflowType string                 `json:"workflow_type,omitempty"`
	Error        *WorkflowError         `json:"error,omitempty"`
	PoolMetadata *OCICreatePoolMetadata `json:"poolMetadata,omitempty"`
	SvmMetadata  *OCICreateSVMMetadata  `json:"svmMetadata,omitempty"`
}

// Query returns the workflow status result for the given workflow ID (and optional run ID).
// It reuses the Temporal client's underlying gRPC connection for both Describe and history RPCs.
func Query(ctx context.Context, c client.Client, workflowID, runID string) (Result, error) {
	return queryWithClient(ctx, sdkClientWrap{c}, workflowID, runID)
}

func queryWithClient(ctx context.Context, c temporalQuerier, workflowID, runID string) (Result, error) {
	out := Result{}
	resp, err := c.DescribeWorkflowExecution(ctx, workflowID, runID)
	if err != nil {
		return out, err
	}
	if resp == nil || resp.WorkflowExecutionInfo == nil {
		out.Status = WorkflowStatusFailed
		out.Error = &WorkflowError{Message: "no workflow execution info"}
		return out, nil
	}
	info := resp.WorkflowExecutionInfo
	exec := info.GetExecution()
	if exec != nil {
		workflowID = exec.GetWorkflowId()
		runID = exec.GetRunId()
	}
	if wt := info.GetType(); wt != nil {
		out.WorkflowType = wt.GetName()
	}

	out.Status = normalizedStatus(info.GetStatus())

	svc := c.History()

	switch out.Status {
	case WorkflowStatusFailed, WorkflowStatusTimedOut:
		out.Error = getWorkflowFailureReason(ctx, svc, temporalNamespace, workflowID, runID)
	case WorkflowStatusCompleted:
		out.PoolMetadata, out.SvmMetadata = getCompletedWorkflowMetadata(ctx, svc, temporalNamespace, workflowID, runID)
	}
	return out, nil
}

func normalizedStatus(s enums.WorkflowExecutionStatus) WorkflowStatus {
	switch s {
	case enums.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return WorkflowStatusCompleted
	case enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return WorkflowStatusTimedOut
	case enums.WORKFLOW_EXECUTION_STATUS_FAILED,
		enums.WORKFLOW_EXECUTION_STATUS_CANCELED,
		enums.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return WorkflowStatusFailed
	case enums.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return WorkflowStatusInProgress
	default:
		return WorkflowStatusInProgress
	}
}

// Activity / child-workflow type names whose results we extract into metadata.
const (
	ociCreatePoolWorkflowName                         = "OCICreatePoolWorkflow"
	ociUpdatePoolWorkflowName                         = "OCIUpdatePoolWorkflow"
	ociExecutePoolMaintenanceWorkflowName             = "OCIExecutePoolMaintenanceWorkflow"
	ociCreateSVMWorkflowName                          = "OCICreateSVMWorkflow"
	createVSAClusterDeploymentWorkflowName            = "vlm.CreateVSAClusterDeploymentWorkflow"
	updateVSAClusterDeploymentWorkflowName            = "vlm.UpdateVSAClusterDeploymentWorkflow"
	maintenanceWorkflowName                           = "vlm.MaintenanceWorkflow"
	mediatorMaintenanceRecreateWorkflowName           = "vlm.MediatorMaintenanceRecreateWorkflow"
	mediatorMaintenanceRebootMigrateWorkflowName      = "vlm.MediatorMaintenanceRebootMigrateWorkflow"
	createOnTapCredentialsForOCIActivityName          = "CreateOnTapCredentialsForOCI"
)

// historyPageSize controls how many history events are fetched per RPC when
// walking a terminated workflow. Both the failure-reason and completed-metadata
// paths page through the entire history once, so a larger page size minimises
// round-trips on long workflows.
const historyPageSize int32 = 200

// getCompletedWorkflowMetadata inspects Temporal history events of a completed
// workflow and returns pool metadata, SVM metadata, or both nils depending on
// the workflow type. For an OCICreatePoolWorkflow it surfaces Vms (from the
// VLM child completion) and Credentials (from the CreateOnTapCredentialsForOCI
// activity completion). For an OCICreateSVMWorkflow it surfaces the SVM
// payload from the workflow's terminal completion event.
func getCompletedWorkflowMetadata(ctx context.Context, svc historyFetcher, namespace, workflowID, runID string) (*OCICreatePoolMetadata, *OCICreateSVMMetadata) {
	allEvents, err := fetchAllHistory(ctx, svc, namespace, workflowID, runID)
	if err != nil || len(allEvents) == 0 {
		return nil, nil
	}

	var poolMeta *OCICreatePoolMetadata
	var svmMeta *OCICreateSVMMetadata
	var parentWorkflowType string
	activityNameByScheduledEventID := make(map[int64]string)

	ensurePoolMeta := func() *OCICreatePoolMetadata {
		if poolMeta == nil {
			poolMeta = &OCICreatePoolMetadata{}
		}
		return poolMeta
	}

	for _, ev := range allEvents {
		switch ev.EventType {
		case enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
			a := ev.GetWorkflowExecutionStartedEventAttributes()
			if a == nil {
				continue
			}
			if a.GetWorkflowType() != nil && a.GetWorkflowType().GetName() != "" {
				parentWorkflowType = a.GetWorkflowType().GetName()
			}

		case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			a := ev.GetActivityTaskScheduledEventAttributes()
			if a == nil || a.GetActivityType() == nil {
				continue
			}
			activityNameByScheduledEventID[ev.GetEventId()] = a.GetActivityType().GetName()

		case enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			if parentWorkflowType != ociCreatePoolWorkflowName {
				continue
			}
			a := ev.GetActivityTaskCompletedEventAttributes()
			if a == nil {
				continue
			}
			if activityNameByScheduledEventID[a.GetScheduledEventId()] != createOnTapCredentialsForOCIActivityName {
				continue
			}
			if creds := ociCreatePoolCredentialsFromPayloads(a.GetResult().GetPayloads()); creds != nil {
				ensurePoolMeta().Credentials = creds
			}

		case enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED:
			if !isPoolMetadataParentWorkflow(parentWorkflowType) {
				continue
			}
			a := ev.GetChildWorkflowExecutionCompletedEventAttributes()
			if a == nil || a.GetWorkflowType() == nil {
				continue
			}
			childName := a.GetWorkflowType().GetName()
			if !isPoolMetadataChildWorkflow(childName) {
				continue
			}
			if a.GetResult() == nil || len(a.GetResult().GetPayloads()) == 0 {
				continue
			}
			if childMeta := vsaClusterChildMetadataFromPayloads(a.GetResult().GetPayloads()); childMeta != nil {
				m := ensurePoolMeta()
				m.PoolUUID = childMeta.PoolUUID
				m.PoolOCID = childMeta.PoolOCID
				m.ResourceName = childMeta.ResourceName
				m.ClusterIP = childMeta.ClusterIP
				m.Vms = childMeta.Vms
				m.IOPS = childMeta.IOPS
				m.ThroughputGBps = childMeta.ThroughputGBps
				m.Mediator = childMeta.Mediator
			}

		case enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
			if parentWorkflowType != ociCreateSVMWorkflowName {
				continue
			}
			a := ev.GetWorkflowExecutionCompletedEventAttributes()
			if a == nil || a.GetResult() == nil {
				continue
			}
			if m := svmResultFromPayloads(a.GetResult().GetPayloads()); m != nil {
				svmMeta = m
			}
		}
	}

	if svmMeta != nil {
		return nil, svmMeta
	}
	return poolMeta, nil
}

func isPoolMetadataParentWorkflow(name string) bool {
	switch name {
	case ociCreatePoolWorkflowName, ociUpdatePoolWorkflowName, ociExecutePoolMaintenanceWorkflowName:
		return true
	default:
		return false
	}
}

func isPoolMetadataChildWorkflow(name string) bool {
	switch name {
	case createVSAClusterDeploymentWorkflowName,
		updateVSAClusterDeploymentWorkflowName,
		maintenanceWorkflowName,
		mediatorMaintenanceRecreateWorkflowName,
		mediatorMaintenanceRebootMigrateWorkflowName:
		return true
	default:
		return false
	}
}

// svmResultFromPayloads deserializes the OCICreateSVMWorkflow result into SVM metadata.
func svmResultFromPayloads(payloads []*commonpb.Payload) *OCICreateSVMMetadata {
	data := payloadDataJSONBytes(payloads)
	if len(data) == 0 {
		return nil
	}
	var meta OCICreateSVMMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil
	}
	if meta.Name == "" && meta.SvmOCID == "" {
		return nil
	}
	return &meta
}

// pageHistory walks workflow execution history one page at a time and invokes
// processPage for each. processPage returns true to stop pagination early
// (the caller has all the data it needs); returning false continues to the
// next page. Iteration also stops naturally when Temporal hands back an empty
// NextPageToken or a nil History.
func pageHistory(
	ctx context.Context,
	svc historyFetcher,
	namespace, workflowID, runID string,
	pageSize int32,
	processPage func(events []*historypb.HistoryEvent) (done bool),
) error {
	req := &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace:       namespace,
		Execution:       &commonpb.WorkflowExecution{WorkflowId: workflowID, RunId: runID},
		MaximumPageSize: pageSize,
	}
	for {
		resp, err := svc.GetWorkflowExecutionHistory(ctx, req)
		if err != nil {
			return err
		}
		if resp == nil || resp.History == nil {
			return nil
		}
		if processPage(resp.History.Events) {
			return nil
		}
		if len(resp.NextPageToken) == 0 {
			return nil
		}
		req.NextPageToken = resp.NextPageToken
	}
}

// fetchAllHistory pages through the entire workflow history. Used by the
// failure-reason extractor and the completed-workflow metadata extractor,
// both of which need to see every event of a terminated workflow.
func fetchAllHistory(ctx context.Context, svc historyFetcher, namespace, workflowID, runID string) ([]*historypb.HistoryEvent, error) {
	var allEvents []*historypb.HistoryEvent
	err := pageHistory(ctx, svc, namespace, workflowID, runID, historyPageSize, func(events []*historypb.HistoryEvent) bool {
		allEvents = append(allEvents, events...)
		return false
	})
	if err != nil {
		return nil, err
	}
	return allEvents, nil
}

// payloadDataJSONBytes returns the workflow result JSON bytes from the first Temporal payload
// (same unwrap as decodeWorkflowInput: raw JSON or base64-wrapped JSON in Data).
func payloadDataJSONBytes(payloads []*commonpb.Payload) []byte {
	if len(payloads) == 0 || payloads[0] == nil {
		return nil
	}
	data := payloads[0].GetData()
	if len(data) == 0 {
		return nil
	}
	decoded := data
	if b, err := base64.StdEncoding.DecodeString(string(data)); err == nil && len(b) > 0 {
		decoded = b
	}
	return decoded
}

func decodeWorkflowInput(payloads []*commonpb.Payload) map[string]interface{} {
	decoded := payloadDataJSONBytes(payloads)
	if len(decoded) == 0 {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(decoded, &out); err != nil {
		return nil
	}
	return out
}

func getWorkflowFailureReason(ctx context.Context, svc historyFetcher, namespace, workflowID, runID string) *WorkflowError {
	allEvents, err := fetchAllHistory(ctx, svc, namespace, workflowID, runID)
	if err != nil {
		return &WorkflowError{Message: fmt.Sprintf("could not fetch history: %v", err)}
	}

	var lastFailure *historypb.HistoryEvent
	for _, ev := range allEvents {
		switch ev.EventType {
		case enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
			enums.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT,
			enums.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED,
			enums.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
			lastFailure = ev
		}
	}
	if lastFailure == nil {
		return nil
	}
	return formatFailureEvent(lastFailure)
}

func formatFailureEvent(ev *historypb.HistoryEvent) *WorkflowError {
	msg := ""
	var cause string
	var retryState, errorType string

	switch ev.EventType {
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
		a := ev.GetWorkflowExecutionFailedEventAttributes()
		if a != nil && a.GetFailure() != nil {
			f := a.GetFailure()
			msg = f.GetMessage()
			if a.RetryState != enums.RETRY_STATE_UNSPECIFIED {
				retryState = a.RetryState.String()
			}
			if f.GetApplicationFailureInfo() != nil {
				errorType = f.GetApplicationFailureInfo().GetType()
			}
			if f.GetCause() != nil && f.GetCause().GetMessage() != "" {
				cause = f.GetCause().GetMessage()
			}
		}
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:
		msg = "workflow execution timed out"
		if a := ev.GetWorkflowExecutionTimedOutEventAttributes(); a != nil && a.RetryState != enums.RETRY_STATE_UNSPECIFIED {
			retryState = a.RetryState.String()
		}
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:
		if a := ev.GetWorkflowExecutionCanceledEventAttributes(); a != nil && a.GetDetails() != nil {
			if payloads := a.GetDetails().GetPayloads(); len(payloads) > 0 && payloads[0] != nil {
				msg = fmt.Sprintf("canceled (details: %s)", string(payloads[0].GetData()))
			} else {
				msg = "workflow execution canceled"
			}
		} else {
			msg = "workflow execution canceled"
		}
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
		if a := ev.GetWorkflowExecutionTerminatedEventAttributes(); a != nil && a.GetReason() != "" {
			msg = a.GetReason()
		} else {
			msg = "workflow execution terminated"
		}
	}

	if msg == "" {
		return nil
	}
	out := msg
	if errorType != "" {
		out = fmt.Sprintf("[%s] %s", errorType, out)
	}
	if retryState != "" {
		out = fmt.Sprintf("%s (retry: %s)", out, retryState)
	}
	return &WorkflowError{Message: out, Cause: cause}
}
