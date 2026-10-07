package temporal

import (
	"context"
	"reflect"
	"runtime"
	"strings"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
)

// NewWorkflowSubmissionInterceptor returns a Temporal client interceptor that
// emits vcp.workflow.submission (see submission_metrics.go) exactly once per
// call to a Temporal dispatch verb. The interceptor is the lowest-level
// chokepoint in the process: every ExecuteWorkflow, SignalWithStartWorkflow,
// and ScheduleClient.Create eventually funnels through the Temporal client's
// outbound interceptor chain, so instrumenting here catches every caller
// (WorkflowExecutor, ExecuteWorkflowSequentially, direct
// temporal.ExecuteWorkflow calls in the GCP/OCI factories, admin background
// launches, leaked-resource detectors, etc.) uniformly and avoids
// double-counting from wrapper-on-wrapper code paths.
//
// The interceptor is registered in _createClientOptionsFromEnv alongside the
// existing tracing interceptor, so it is enabled for every production
// WorkflowEngine client. Tests that construct a client without going through
// InitializeClient (mocked client.Client, MockTemporalTestClient, etc.) do
// NOT get the interceptor, and RecordWorkflowSubmission uses the sync.Once /
// test-hook pattern so those tests remain unaffected.
func NewWorkflowSubmissionInterceptor() interceptor.ClientInterceptor {
	return &workflowSubmissionClientInterceptor{}
}

// workflowSubmissionClientInterceptor implements interceptor.ClientInterceptor
// by embedding the SDK-provided base (required by Temporal to safely handle
// future interface additions) and returning our outbound wrapper.
type workflowSubmissionClientInterceptor struct {
	interceptor.ClientInterceptorBase
}

func (w *workflowSubmissionClientInterceptor) InterceptClient(
	next interceptor.ClientOutboundInterceptor,
) interceptor.ClientOutboundInterceptor {
	return &workflowSubmissionOutboundInterceptor{
		ClientOutboundInterceptorBase: interceptor.ClientOutboundInterceptorBase{Next: next},
	}
}

// workflowSubmissionOutboundInterceptor overrides only the three dispatch
// verbs that count as "submitting a workflow to Temporal". All other
// interceptor methods fall through to the embedded base and forward to Next.
//
// Dispatch verb coverage:
//   - ExecuteWorkflow: fires on every client.Client.ExecuteWorkflow, which
//     covers WorkflowExecutor.ExecuteWorkflowSingle and every direct
//     temporal.ExecuteWorkflow(ctx, ...) call across the GCP/OCI factories,
//     background workflow launchers, leaked-resource detectors, admin jobs,
//     and the google-proxy app boot.
//   - SignalWithStartWorkflow: fires on every client.Client.SignalWithStartWorkflow,
//     which covers both branches of ExecuteWorkflowSequentially (the control
//     workflow path) as well as any future direct callers.
//   - CreateSchedule: fires on every ScheduleClient.Create, which covers
//     TemporalScheduler.Create (retry loop inside; each attempt is its own
//     submission — see workflow_type mapping note below) plus any direct use
//     of scheduleClient.Create. This is the closest current equivalent to the
//     older ScheduleWorkflow dispatch verb; Temporal moved this API to
//     ScheduleClient.Create in the v1.x SDK series and there is no
//     StartWorkflowExecution low-level gRPC call in the tree, so this is the
//     complete coverage of "schedule a workflow to run on a cadence".
//
// Anti-double-counting: because this is the lowest-level chokepoint in the
// process (the Temporal client itself), no higher-level wrapper needs to (or
// should) emit vcp.workflow.submission on its own. Adding a manual
// RecordWorkflowSubmission call in WorkflowExecutor.ExecuteWorkflowSingle,
// _executeWorkflowSequentially, or a factory would double-count.
type workflowSubmissionOutboundInterceptor struct {
	interceptor.ClientOutboundInterceptorBase
}

// ExecuteWorkflow intercepts client.Client.ExecuteWorkflow. The SDK
// normalizes the workflow argument (function or string) into a plain string
// in in.WorkflowType before the interceptor is invoked, so we can use it
// directly as the workflow_type label value.
func (w *workflowSubmissionOutboundInterceptor) ExecuteWorkflow(
	ctx context.Context,
	in *interceptor.ClientExecuteWorkflowInput,
) (client.WorkflowRun, error) {
	run, err := w.Next.ExecuteWorkflow(ctx, in)
	RecordWorkflowSubmission(ctx, in.WorkflowType, err)
	return run, err
}

// SignalWithStartWorkflow intercepts client.Client.SignalWithStartWorkflow.
// The signal itself (which simply delivers a payload if the workflow already
// exists) is not treated as a separate submission — the metric fires once per
// SignalWithStartWorkflow call regardless of whether Temporal started a new
// run or reused an existing one, matching the "one Temporal call = one
// submission" contract.
//
// VCP routes many logical operations through a long-running SequenceWorkflow
// / SequenceWorkflowV2 by SignalWithStart'ing the sequencer and letting it
// dispatch the child by name (see core/orchestrator/workflows.
// SignalWorkflowParams.Function). If we labeled by the host workflow type we'd
// collapse every sequenced submission into "SequenceWorkflow(V2)" and lose
// the per-operation signal. resolveSignalWorkflowType inspects the signal
// payload and surfaces the real child workflow name instead for those cases.
func (w *workflowSubmissionOutboundInterceptor) SignalWithStartWorkflow(
	ctx context.Context,
	in *interceptor.ClientSignalWithStartWorkflowInput,
) (client.WorkflowRun, error) {
	run, err := w.Next.SignalWithStartWorkflow(ctx, in)
	RecordWorkflowSubmission(ctx, resolveSignalWorkflowType(in), err)
	return run, err
}

// resolveSignalWorkflowType returns the workflow_type label value to emit for
// a SignalWithStartWorkflow call. For a plain SignalWithStart it is simply the
// SDK-normalized WorkflowType; for the sequencer wrapper workflows it is the
// child workflow name carried by SignalWorkflowParams.Function on the signal
// payload. If reflection cannot find a usable child name we fall back to the
// host workflow type so the submission is still counted (visible as
// "SequenceWorkflow" / "SequenceWorkflowV2").
//
// Reflection avoids pulling the workflows package into workflow_engine (which
// would create an import cycle: workflows -> workflow_engine for the client).
// The field layout we care about — a `Function string` on the signal arg — is
// documented at SignalWorkflowParams, and the sequencer host names are stable
// workflow identifiers registered in worker/main.go.
func resolveSignalWorkflowType(in *interceptor.ClientSignalWithStartWorkflowInput) string {
	if in == nil {
		return ""
	}
	if !isSequencerWorkflow(in.WorkflowType) {
		return in.WorkflowType
	}
	if name := signalWorkflowFunctionName(in.SignalArg); name != "" {
		return name
	}
	return in.WorkflowType
}

// isSequencerWorkflow identifies the two sequencer host workflows whose
// SignalWithStart'ed submissions carry the real workflow name in the signal
// payload rather than the WorkflowType field.
func isSequencerWorkflow(name string) bool {
	return name == "SequenceWorkflow" || name == "SequenceWorkflowV2"
}

// signalWorkflowFunctionName extracts a Function-field string value from a
// signal argument via reflection, tolerating both value and pointer receivers.
// Returns "" if the argument is nil, is not a struct, has no Function field,
// or the Function field is not a string. Matches the SignalWorkflowParams
// shape defined in core/orchestrator/workflows/control_workflow.go.
func signalWorkflowFunctionName(arg interface{}) string {
	if arg == nil {
		return ""
	}
	v := reflect.ValueOf(arg)
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	f := v.FieldByName("Function")
	if !f.IsValid() || f.Kind() != reflect.String {
		return ""
	}
	return f.String()
}

// CreateSchedule intercepts ScheduleClient.Create. The ScheduleClientCreateInput
// carries the full ScheduleOptions; the workflow_type label is extracted from
// the schedule's action when it is a ScheduleWorkflowAction (the only action
// type used in this codebase) via scheduleWorkflowTypeName below. This maps
// the older "ScheduleWorkflow" dispatch verb name onto its current
// ScheduleClient.Create equivalent, documented at the interceptor comment above.
func (w *workflowSubmissionOutboundInterceptor) CreateSchedule(
	ctx context.Context,
	in *interceptor.ScheduleClientCreateInput,
) (client.ScheduleHandle, error) {
	handle, err := w.Next.CreateSchedule(ctx, in)
	RecordWorkflowSubmission(ctx, scheduleWorkflowTypeName(in), err)
	return handle, err
}

// scheduleWorkflowTypeName extracts a stable workflow_type label for a
// schedule submission. When the schedule action is a ScheduleWorkflowAction
// (the common case for TemporalScheduler.Create and the OCI schedule
// reconcilers) we prefer the Workflow field, which may be either a workflow
// function pointer or a workflow-name string; when it is not a workflow action
// (unlikely in this codebase but permitted by the SDK) we return an empty
// string so the emission still occurs with a visible "unknown" label rather
// than dropping the submission entirely.
func scheduleWorkflowTypeName(in *interceptor.ScheduleClientCreateInput) string {
	if in == nil || in.Options == nil {
		return ""
	}
	action, ok := in.Options.Action.(*client.ScheduleWorkflowAction)
	if !ok || action == nil {
		return ""
	}
	if name, isString := action.Workflow.(string); isString {
		return name
	}
	if action.Workflow == nil {
		return ""
	}
	return workflowFunctionName(action.Workflow)
}

// workflowFunctionName mirrors the extraction convention used elsewhere in
// this repo (see workflows.getWorkflowName): trim the fully-qualified package
// path down to the trailing function name so the label matches what humans
// see in the Temporal UI (e.g. "CreatePoolWorkflow" instead of
// "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows.CreatePoolWorkflow").
func workflowFunctionName(fn interface{}) string {
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.Kind() != reflect.Func {
		return ""
	}
	full := runtime.FuncForPC(v.Pointer()).Name()
	if full == "" {
		return ""
	}
	parts := strings.Split(full, ".")
	return parts[len(parts)-1]
}
