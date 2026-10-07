package temporal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
)

// capturedSubmission records a single invocation of the submission metric
// hook so tests can assert on the label triple.
type capturedSubmission struct {
	workflowType string
	result       string
	reason       string
}

// newSubmissionRecorder returns a thread-safe capture function together with a
// snapshot getter. Meant to be passed to setWorkflowSubmissionRecorderForTest.
func newSubmissionRecorder() (record func(string, string, string), snapshot func() []capturedSubmission) {
	var (
		mu     sync.Mutex
		events []capturedSubmission
	)
	record = func(workflowType, result, reason string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, capturedSubmission{
			workflowType: workflowType,
			result:       result,
			reason:       reason,
		})
	}
	snapshot = func() []capturedSubmission {
		mu.Lock()
		defer mu.Unlock()
		out := make([]capturedSubmission, len(events))
		copy(out, events)
		return out
	}
	return record, snapshot
}

func TestClassifyWorkflowSubmissionResult(t *testing.T) {
	// Table-driven per the task spec. Each row covers one canonical input the
	// production classifier must handle: nil for the accepted path, one entry
	// per Temporal serviceerror we map, both context.* sentinels, one wrapped
	// error to prove errors.As unwrapping works, and a generic error for the
	// "other" fallback bucket.
	cases := []struct {
		name       string
		err        error
		wantResult string
		wantReason string
	}{
		{
			name:       "nil error yields accepted",
			err:        nil,
			wantResult: submissionResultAccepted,
			wantReason: submissionReasonNone,
		},
		{
			name:       "WorkflowExecutionAlreadyStarted maps to already_started",
			err:        &serviceerror.WorkflowExecutionAlreadyStarted{},
			wantResult: submissionResultRejected,
			wantReason: submissionReasonAlreadyStarted,
		},
		{
			name:       "Unavailable maps to unavailable",
			err:        &serviceerror.Unavailable{},
			wantResult: submissionResultRejected,
			wantReason: submissionReasonUnavailable,
		},
		{
			name:       "ResourceExhausted maps to resource_exhausted",
			err:        &serviceerror.ResourceExhausted{},
			wantResult: submissionResultRejected,
			wantReason: submissionReasonResourceExhausted,
		},
		{
			name:       "PermissionDenied maps to permission_denied",
			err:        &serviceerror.PermissionDenied{},
			wantResult: submissionResultRejected,
			wantReason: submissionReasonPermissionDenied,
		},
		{
			name:       "NamespaceNotFound maps to namespace_not_found",
			err:        &serviceerror.NamespaceNotFound{},
			wantResult: submissionResultRejected,
			wantReason: submissionReasonNamespaceNotFound,
		},
		{
			name:       "Temporal serviceerror DeadlineExceeded maps to deadline_exceeded",
			err:        &serviceerror.DeadlineExceeded{},
			wantResult: submissionResultRejected,
			wantReason: submissionReasonDeadlineExceeded,
		},
		{
			name:       "context.DeadlineExceeded maps to deadline_exceeded",
			err:        context.DeadlineExceeded,
			wantResult: submissionResultRejected,
			wantReason: submissionReasonDeadlineExceeded,
		},
		{
			name:       "context.Canceled maps to canceled",
			err:        context.Canceled,
			wantResult: submissionResultRejected,
			wantReason: submissionReasonCanceled,
		},
		{
			// Proves the classifier walks the unwrap chain. Wrapping is the
			// common shape once vsaerrors.WrapAsTemporalApplicationError or
			// fmt.Errorf("...: %w", ...) has run over the raw error.
			name:       "wrapped WorkflowExecutionAlreadyStarted still classifies as already_started",
			err:        fmt.Errorf("wrapped: %w", &serviceerror.WorkflowExecutionAlreadyStarted{}),
			wantResult: submissionResultRejected,
			wantReason: submissionReasonAlreadyStarted,
		},
		{
			name:       "generic error falls through to other",
			err:        errors.New("random failure"),
			wantResult: submissionResultRejected,
			wantReason: submissionReasonOther,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotResult, gotReason := ClassifyWorkflowSubmissionResult(tc.err)
			assert.Equal(t, tc.wantResult, gotResult, "result label")
			assert.Equal(t, tc.wantReason, gotReason, "reason label")
		})
	}
}

func TestRecordWorkflowSubmission_UsesHook(t *testing.T) {
	// Verifies the (workflowType, result, reason) triple is forwarded verbatim
	// for both the accepted path (err=nil) and one representative rejected
	// path. Covers the sync.RWMutex handoff between recorder install and
	// production emission implicitly by exercising the hook end-to-end.
	record, snapshot := newSubmissionRecorder()
	restore := setWorkflowSubmissionRecorderForTest(record)
	defer restore()

	RecordWorkflowSubmission(context.Background(), "CreatePoolWorkflow", nil)
	RecordWorkflowSubmission(context.Background(), "PoolDataSubnetWorkFlow", &serviceerror.WorkflowExecutionAlreadyStarted{})

	got := snapshot()
	require.Len(t, got, 2)
	assert.Equal(t, capturedSubmission{
		workflowType: "CreatePoolWorkflow",
		result:       submissionResultAccepted,
		reason:       submissionReasonNone,
	}, got[0])
	assert.Equal(t, capturedSubmission{
		workflowType: "PoolDataSubnetWorkFlow",
		result:       submissionResultRejected,
		reason:       submissionReasonAlreadyStarted,
	}, got[1])
}

func TestRecordWorkflowSubmission_EmptyWorkflowTypeStillEmits(t *testing.T) {
	// The interceptor falls back to "" when it cannot extract a workflow name
	// from a schedule action. That is intentionally observable rather than
	// dropped so operators notice the mislabel; codify that contract here.
	record, snapshot := newSubmissionRecorder()
	restore := setWorkflowSubmissionRecorderForTest(record)
	defer restore()

	RecordWorkflowSubmission(context.Background(), "", errors.New("boom"))

	got := snapshot()
	require.Len(t, got, 1)
	assert.Equal(t, capturedSubmission{
		workflowType: "",
		result:       submissionResultRejected,
		reason:       submissionReasonOther,
	}, got[0])
}

func TestRecordWorkflowSubmission_NoHook_NoPanic(t *testing.T) {
	// When no hook is installed, RecordWorkflowSubmission must fall through to
	// the OTEL counter path without panicking, even if the counter has not been
	// registered yet (sync.Once + nil-check guard). Real emission is not
	// observable here without a real MeterProvider; the assertion is simply
	// that the function returns cleanly.
	// Defensively clear any leftover hook from another test in the same package.
	restore := setWorkflowSubmissionRecorderForTest(nil)
	defer restore()

	assert.NotPanics(t, func() {
		RecordWorkflowSubmission(context.Background(), "SomeWorkflow", nil)
		RecordWorkflowSubmission(context.Background(), "SomeWorkflow", errors.New("boom"))
	})
}
