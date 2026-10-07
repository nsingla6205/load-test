package temporal

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
)

// fakeNextOutbound is a minimal interceptor.ClientOutboundInterceptor that
// records the three dispatch verbs we care about and returns configurable
// results. It embeds interceptor.ClientOutboundInterceptorBase so it satisfies
// the interface (and the mustEmbed guard) without having to spell out every
// method the SDK might add in the future; the methods we do NOT call from the
// interceptor under test are never invoked, so a nil embedded Next is safe.
type fakeNextOutbound struct {
	interceptor.ClientOutboundInterceptorBase

	// Configured return values.
	executeErr           error
	signalWithStartErr   error
	createScheduleErr    error
	executeRun           client.WorkflowRun
	signalWithStartRun   client.WorkflowRun
	createScheduleHandle client.ScheduleHandle

	// Captured inputs so tests can assert the interceptor forwarded them
	// verbatim.
	lastExecute         *interceptor.ClientExecuteWorkflowInput
	lastSignalWithStart *interceptor.ClientSignalWithStartWorkflowInput
	lastCreateSchedule  *interceptor.ScheduleClientCreateInput
}

func (f *fakeNextOutbound) ExecuteWorkflow(
	_ context.Context,
	in *interceptor.ClientExecuteWorkflowInput,
) (client.WorkflowRun, error) {
	f.lastExecute = in
	return f.executeRun, f.executeErr
}

func (f *fakeNextOutbound) SignalWithStartWorkflow(
	_ context.Context,
	in *interceptor.ClientSignalWithStartWorkflowInput,
) (client.WorkflowRun, error) {
	f.lastSignalWithStart = in
	return f.signalWithStartRun, f.signalWithStartErr
}

func (f *fakeNextOutbound) CreateSchedule(
	_ context.Context,
	in *interceptor.ScheduleClientCreateInput,
) (client.ScheduleHandle, error) {
	f.lastCreateSchedule = in
	return f.createScheduleHandle, f.createScheduleErr
}

// newInterceptorUnderTest wires up the production interceptor's outbound layer
// with a controllable fake Next so each test can drive nil / already-started /
// generic-error return paths deterministically.
func newInterceptorUnderTest(t *testing.T, next *fakeNextOutbound) *workflowSubmissionOutboundInterceptor {
	t.Helper()
	interceptorImpl := NewWorkflowSubmissionInterceptor()
	outbound := interceptorImpl.InterceptClient(next)
	// InterceptClient returns interceptor.ClientOutboundInterceptor; cast to
	// the concrete production type so tests can call the three overridden
	// methods without ambiguity.
	casted, ok := outbound.(*workflowSubmissionOutboundInterceptor)
	require.True(t, ok, "InterceptClient must return *workflowSubmissionOutboundInterceptor")
	return casted
}

// executeDispatchCases feeds three canonical error shapes through a single
// verb so each dispatch verb's test stays compact.
type executeDispatchCase struct {
	name       string
	nextErr    error
	wantResult string
	wantReason string
}

func standardDispatchCases() []executeDispatchCase {
	return []executeDispatchCase{
		{"success", nil, submissionResultAccepted, submissionReasonNone},
		{"already_started", &serviceerror.WorkflowExecutionAlreadyStarted{}, submissionResultRejected, submissionReasonAlreadyStarted},
		{"generic_error", errors.New("boom"), submissionResultRejected, submissionReasonOther},
	}
}

func TestInterceptor_ExecuteWorkflow_EmitsAndForwardsInput(t *testing.T) {
	for _, tc := range standardDispatchCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			record, snapshot := newSubmissionRecorder()
			restore := setWorkflowSubmissionRecorderForTest(record)
			defer restore()

			next := &fakeNextOutbound{executeErr: tc.nextErr}
			w := newInterceptorUnderTest(t, next)

			in := &interceptor.ClientExecuteWorkflowInput{
				Options:      &client.StartWorkflowOptions{ID: "wf-id-1", TaskQueue: "customer-workflows"},
				WorkflowType: "CreatePoolWorkflow",
				Args:         []interface{}{"arg1"},
			}

			_, err := w.ExecuteWorkflow(context.Background(), in)

			// Error must be forwarded verbatim: the interceptor observes but
			// never transforms the dispatch result.
			assert.Equal(t, tc.nextErr, err)
			// Input must be forwarded to Next unchanged so downstream
			// interceptors (tracing) see the original values.
			require.NotNil(t, next.lastExecute)
			assert.Equal(t, "CreatePoolWorkflow", next.lastExecute.WorkflowType)
			assert.Equal(t, "wf-id-1", next.lastExecute.Options.ID)

			got := snapshot()
			require.Len(t, got, 1, "exactly one metric emission per dispatch call")
			assert.Equal(t, capturedSubmission{
				workflowType: "CreatePoolWorkflow",
				result:       tc.wantResult,
				reason:       tc.wantReason,
			}, got[0])
		})
	}
}

func TestInterceptor_SignalWithStartWorkflow_EmitsAndForwardsInput(t *testing.T) {
	for _, tc := range standardDispatchCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			record, snapshot := newSubmissionRecorder()
			restore := setWorkflowSubmissionRecorderForTest(record)
			defer restore()

			next := &fakeNextOutbound{signalWithStartErr: tc.nextErr}
			w := newInterceptorUnderTest(t, next)

			in := &interceptor.ClientSignalWithStartWorkflowInput{
				SignalName:   "TriggerSignal",
				SignalArg:    "payload",
				Options:      &client.StartWorkflowOptions{ID: "seq-wf-id", TaskQueue: "customer-workflows"},
				WorkflowType: "SequenceWorkflow",
			}

			_, err := w.SignalWithStartWorkflow(context.Background(), in)

			assert.Equal(t, tc.nextErr, err)
			require.NotNil(t, next.lastSignalWithStart)
			assert.Equal(t, "TriggerSignal", next.lastSignalWithStart.SignalName)
			assert.Equal(t, "SequenceWorkflow", next.lastSignalWithStart.WorkflowType)

			got := snapshot()
			require.Len(t, got, 1)
			assert.Equal(t, capturedSubmission{
				workflowType: "SequenceWorkflow",
				result:       tc.wantResult,
				reason:       tc.wantReason,
			}, got[0])
		})
	}
}

func TestInterceptor_CreateSchedule_UsesWorkflowActionType(t *testing.T) {
	for _, tc := range standardDispatchCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			record, snapshot := newSubmissionRecorder()
			restore := setWorkflowSubmissionRecorderForTest(record)
			defer restore()

			next := &fakeNextOutbound{createScheduleErr: tc.nextErr}
			w := newInterceptorUnderTest(t, next)

			in := &interceptor.ScheduleClientCreateInput{
				Options: &client.ScheduleOptions{
					ID: "sched-id",
					Action: &client.ScheduleWorkflowAction{
						ID:        "wf-run",
						Workflow:  "UpdateBackupScheduleWorkflow",
						TaskQueue: "background-workflows",
					},
				},
			}

			_, err := w.CreateSchedule(context.Background(), in)

			assert.Equal(t, tc.nextErr, err)
			require.NotNil(t, next.lastCreateSchedule)

			got := snapshot()
			require.Len(t, got, 1)
			assert.Equal(t, capturedSubmission{
				workflowType: "UpdateBackupScheduleWorkflow",
				result:       tc.wantResult,
				reason:       tc.wantReason,
			}, got[0])
		})
	}
}

// exampleScheduledWorkflow is a stand-in workflow function used only to
// exercise the reflection-based function-name extraction path in
// scheduleWorkflowTypeName; it is never registered with a real Temporal
// worker.
func exampleScheduledWorkflow() error { return nil }

func TestScheduleWorkflowTypeName(t *testing.T) {
	cases := []struct {
		name string
		in   *interceptor.ScheduleClientCreateInput
		want string
	}{
		{
			name: "nil input yields empty label",
			in:   nil,
			want: "",
		},
		{
			name: "nil options yields empty label",
			in:   &interceptor.ScheduleClientCreateInput{Options: nil},
			want: "",
		},
		{
			name: "non-workflow action yields empty label",
			in: &interceptor.ScheduleClientCreateInput{
				Options: &client.ScheduleOptions{Action: nil},
			},
			want: "",
		},
		{
			name: "workflow action with string name yields the string",
			in: &interceptor.ScheduleClientCreateInput{
				Options: &client.ScheduleOptions{
					Action: &client.ScheduleWorkflowAction{Workflow: "MyScheduledWorkflow"},
				},
			},
			want: "MyScheduledWorkflow",
		},
		{
			name: "workflow action with nil workflow yields empty label",
			in: &interceptor.ScheduleClientCreateInput{
				Options: &client.ScheduleOptions{
					Action: &client.ScheduleWorkflowAction{Workflow: nil},
				},
			},
			want: "",
		},
		{
			name: "workflow action with function pointer yields trailing function name",
			in: &interceptor.ScheduleClientCreateInput{
				Options: &client.ScheduleOptions{
					Action: &client.ScheduleWorkflowAction{Workflow: exampleScheduledWorkflow},
				},
			},
			want: "exampleScheduledWorkflow",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, scheduleWorkflowTypeName(tc.in))
		})
	}
}

func TestWorkflowFunctionName(t *testing.T) {
	// Function pointer path: name gets trimmed to the trailing identifier.
	assert.Equal(t, "exampleScheduledWorkflow", workflowFunctionName(exampleScheduledWorkflow))
	// Non-function inputs must return "" rather than panic under reflection.
	assert.Equal(t, "", workflowFunctionName(nil))
	assert.Equal(t, "", workflowFunctionName("not-a-function"))
	assert.Equal(t, "", workflowFunctionName(42))
}

// fakeSignalWorkflowParams mirrors the subset of SignalWorkflowParams the
// interceptor reflects on (see core/orchestrator/workflows.
// SignalWorkflowParams). Kept local so this test package doesn't need to
// import the workflows package and create a cycle with workflow_engine.
type fakeSignalWorkflowParams struct {
	Function string
	Args     []interface{}
}

// extraneousFieldsSignal verifies the reflection path works even when the
// caller nests arbitrary other fields alongside Function; only the field name
// matters, not the layout.
type extraneousFieldsSignal struct {
	ExtraA   int
	Function string
	ExtraB   string
}

func TestResolveSignalWorkflowType(t *testing.T) {
	cases := []struct {
		name string
		in   *interceptor.ClientSignalWithStartWorkflowInput
		want string
	}{
		{
			name: "nil input yields empty label",
			in:   nil,
			want: "",
		},
		{
			name: "non-sequencer workflow passes WorkflowType through unchanged",
			in: &interceptor.ClientSignalWithStartWorkflowInput{
				WorkflowType: "RotatePoolCertificateWorkflow",
				SignalArg:    fakeSignalWorkflowParams{Function: "ignored"},
			},
			want: "RotatePoolCertificateWorkflow",
		},
		{
			name: "sequencer v1 unwraps the child Function field",
			in: &interceptor.ClientSignalWithStartWorkflowInput{
				WorkflowType: "SequenceWorkflow",
				SignalArg:    fakeSignalWorkflowParams{Function: "CreateVolumeWorkflow"},
			},
			want: "CreateVolumeWorkflow",
		},
		{
			name: "sequencer v2 unwraps the child Function field",
			in: &interceptor.ClientSignalWithStartWorkflowInput{
				WorkflowType: "SequenceWorkflowV2",
				SignalArg:    fakeSignalWorkflowParams{Function: "DeleteVolumeWorkflow"},
			},
			want: "DeleteVolumeWorkflow",
		},
		{
			name: "sequencer with pointer-to-struct signal arg unwraps the field",
			in: &interceptor.ClientSignalWithStartWorkflowInput{
				WorkflowType: "SequenceWorkflowV2",
				SignalArg:    &fakeSignalWorkflowParams{Function: "UpdateVolumeWorkflow"},
			},
			want: "UpdateVolumeWorkflow",
		},
		{
			name: "sequencer with foreign struct carrying Function still unwraps",
			in: &interceptor.ClientSignalWithStartWorkflowInput{
				WorkflowType: "SequenceWorkflow",
				SignalArg:    extraneousFieldsSignal{Function: "SnapshotRestoreWorkflow"},
			},
			want: "SnapshotRestoreWorkflow",
		},
		{
			name: "sequencer with nil signal arg falls back to host workflow type",
			in: &interceptor.ClientSignalWithStartWorkflowInput{
				WorkflowType: "SequenceWorkflow",
				SignalArg:    nil,
			},
			want: "SequenceWorkflow",
		},
		{
			name: "sequencer with nil pointer signal arg falls back",
			in: &interceptor.ClientSignalWithStartWorkflowInput{
				WorkflowType: "SequenceWorkflowV2",
				SignalArg:    (*fakeSignalWorkflowParams)(nil),
			},
			want: "SequenceWorkflowV2",
		},
		{
			name: "sequencer with non-struct signal arg falls back",
			in: &interceptor.ClientSignalWithStartWorkflowInput{
				WorkflowType: "SequenceWorkflow",
				SignalArg:    "opaque-payload",
			},
			want: "SequenceWorkflow",
		},
		{
			name: "sequencer with struct missing Function field falls back",
			in: &interceptor.ClientSignalWithStartWorkflowInput{
				WorkflowType: "SequenceWorkflow",
				SignalArg:    struct{ Other string }{Other: "x"},
			},
			want: "SequenceWorkflow",
		},
		{
			name: "sequencer with Function field of wrong type falls back",
			in: &interceptor.ClientSignalWithStartWorkflowInput{
				WorkflowType: "SequenceWorkflowV2",
				SignalArg:    struct{ Function int }{Function: 42},
			},
			want: "SequenceWorkflowV2",
		},
		{
			name: "sequencer with empty-string Function falls back to host",
			in: &interceptor.ClientSignalWithStartWorkflowInput{
				WorkflowType: "SequenceWorkflow",
				SignalArg:    fakeSignalWorkflowParams{Function: ""},
			},
			want: "SequenceWorkflow",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveSignalWorkflowType(tc.in))
		})
	}
}

// TestInterceptor_SignalWithStart_SequenceWorkflow_UnwrapsChildName verifies
// end-to-end that the submission metric emitted for a sequenced dispatch
// carries the child workflow name as workflow_type, not the sequencer host.
// This guards against regressing to a single "SequenceWorkflowV2" series that
// collapses every logical operation together.
func TestInterceptor_SignalWithStart_SequenceWorkflow_UnwrapsChildName(t *testing.T) {
	record, snapshot := newSubmissionRecorder()
	restore := setWorkflowSubmissionRecorderForTest(record)
	defer restore()

	next := &fakeNextOutbound{}
	w := newInterceptorUnderTest(t, next)

	in := &interceptor.ClientSignalWithStartWorkflowInput{
		SignalName:   "TriggerSignal",
		SignalArg:    fakeSignalWorkflowParams{Function: "RotatePoolCertificateWorkflow"},
		Options:      &client.StartWorkflowOptions{ID: "seq-wf-id", TaskQueue: "customer-workflows"},
		WorkflowType: "SequenceWorkflowV2",
	}

	_, err := w.SignalWithStartWorkflow(context.Background(), in)
	require.NoError(t, err)

	got := snapshot()
	require.Len(t, got, 1)
	assert.Equal(t, capturedSubmission{
		workflowType: "RotatePoolCertificateWorkflow",
		result:       submissionResultAccepted,
		reason:       submissionReasonNone,
	}, got[0])
}
