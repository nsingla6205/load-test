package temporal

import (
	"context"
	"log"
	"sync"

	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.temporal.io/api/serviceerror"
)

// Metric name and label vocabulary for Temporal workflow submission attempts.
// One increment corresponds to exactly one call to a Temporal dispatch verb
// (ExecuteWorkflow, SignalWithStartWorkflow, or ScheduleClient.Create); a
// retry loop above this layer will produce multiple increments, and that is
// intentional -- each Temporal call is its own submission.
const (
	metricNameWorkflowSubmission = "vcp.workflow.submission"

	// Label names. Kept low-cardinality: workflow_type is bounded by the
	// registered workflow set, result has exactly two values, and reason is
	// a small closed enum defined below.
	submissionLabelWorkflowType = "workflow_type"
	submissionLabelResult       = "result"
	submissionLabelReason       = "reason"

	// Values for the `result` label.
	submissionResultAccepted = "accepted"
	submissionResultRejected = "rejected"

	// Values for the `reason` label. The empty string is used when
	// result=accepted; every rejected submission carries one of the enum
	// values below so dashboards can filter out expected rejections
	// (already_started) from the true-failure panels.
	submissionReasonNone              = ""
	submissionReasonAlreadyStarted    = "already_started"
	submissionReasonUnavailable       = "unavailable"
	submissionReasonResourceExhausted = "resource_exhausted"
	submissionReasonPermissionDenied  = "permission_denied"
	submissionReasonNamespaceNotFound = "namespace_not_found"
	submissionReasonDeadlineExceeded  = "deadline_exceeded"
	submissionReasonCanceled          = "canceled"
	submissionReasonOther             = "other"
)

var (
	workflowSubmissionMetricsInitOnce sync.Once
	workflowSubmissionCounter         metric.Int64Counter

	// workflowSubmissionRecorderMu guards workflowSubmissionRecorder so tests
	// can swap in a capturing recorder without racing production emissions.
	workflowSubmissionRecorderMu sync.RWMutex
	workflowSubmissionRecorder   func(workflowType, result, reason string)
)

// registerWorkflowSubmissionMetrics lazily initializes the OTEL counter. It is
// safe to call concurrently; the sync.Once guarantees single initialization.
// A failed instrument creation is logged and swallowed so the dispatch path
// never fails because of a metrics setup problem.
func registerWorkflowSubmissionMetrics() {
	workflowSubmissionMetricsInitOnce.Do(func() {
		meter := otel.Meter("github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal")

		var err error
		workflowSubmissionCounter, err = meter.Int64Counter(
			metricNameWorkflowSubmission,
			metric.WithDescription("Total number of Temporal workflow submission attempts, partitioned by workflow_type, result (accepted|rejected), and rejection reason."),
			metric.WithUnit("{submission}"),
		)
		if err != nil {
			log.Printf("Failed to create %s counter: %v", metricNameWorkflowSubmission, err)
		}
	})
}

// ClassifyWorkflowSubmissionResult maps a Temporal dispatch verb's error return
// into the (result, reason) label pair emitted by vcp.workflow.submission. A
// nil error is always ("accepted", ""); any non-nil error is a rejection and
// maps to one of the small closed reason enum values, defaulting to "other"
// when no known Temporal serviceerror or context error matches.
//
// The classification uses errors.As / errors.Is (via vsaerrors) so wrapped
// errors — e.g. errors passed through fmt.Errorf("...: %w", ...) or through
// vsaerrors.WrapAsTemporalApplicationError — still classify correctly.
//
// Special note on WorkflowExecutionAlreadyStarted: it is treated as a
// rejection because Temporal refused to start a new execution, but dashboards
// should filter reason=already_started out of the "true failure" panel because
// this is often expected (idempotent dispatch / dedup on retry).
func ClassifyWorkflowSubmissionResult(err error) (result string, reason string) {
	if err == nil {
		return submissionResultAccepted, submissionReasonNone
	}

	// Order matters only insofar as the checks are mutually exclusive; each
	// Temporal serviceerror is a distinct concrete type, and context errors
	// use sentinel values, so an errors.As / errors.Is match against any one
	// of them is unambiguous.
	var (
		alreadyStarted    *serviceerror.WorkflowExecutionAlreadyStarted
		unavailable       *serviceerror.Unavailable
		resourceExhausted *serviceerror.ResourceExhausted
		permissionDenied  *serviceerror.PermissionDenied
		namespaceNotFound *serviceerror.NamespaceNotFound
		deadlineExceeded  *serviceerror.DeadlineExceeded
	)

	switch {
	case vsaerrors.As(err, &alreadyStarted):
		return submissionResultRejected, submissionReasonAlreadyStarted
	case vsaerrors.As(err, &unavailable):
		return submissionResultRejected, submissionReasonUnavailable
	case vsaerrors.As(err, &resourceExhausted):
		return submissionResultRejected, submissionReasonResourceExhausted
	case vsaerrors.As(err, &permissionDenied):
		return submissionResultRejected, submissionReasonPermissionDenied
	case vsaerrors.As(err, &namespaceNotFound):
		return submissionResultRejected, submissionReasonNamespaceNotFound
	// Both context.DeadlineExceeded and the Temporal serviceerror
	// DeadlineExceeded collapse into a single reason so dashboards need only
	// one bucket for "the client-side call timed out".
	case vsaerrors.As(err, &deadlineExceeded), vsaerrors.Is(err, context.DeadlineExceeded):
		return submissionResultRejected, submissionReasonDeadlineExceeded
	case vsaerrors.Is(err, context.Canceled):
		return submissionResultRejected, submissionReasonCanceled
	default:
		return submissionResultRejected, submissionReasonOther
	}
}

// RecordWorkflowSubmission emits one increment of vcp.workflow.submission for
// a single Temporal dispatch attempt. It never returns an error and never
// affects the dispatch result: on any internal issue (test hook not installed,
// OTEL instrument failed to register, etc.) it silently no-ops.
//
// workflowType is the Temporal-normalized workflow name (already a string by
// the time the interceptor sees it); passing an empty string is allowed and
// will emit workflow_type="" so it is still observable in dashboards.
func RecordWorkflowSubmission(ctx context.Context, workflowType string, err error) {
	result, reason := ClassifyWorkflowSubmissionResult(err)

	if hook := currentWorkflowSubmissionRecorder(); hook != nil {
		hook(workflowType, result, reason)
		return
	}

	registerWorkflowSubmissionMetrics()

	if workflowSubmissionCounter == nil {
		return
	}

	workflowSubmissionCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String(submissionLabelWorkflowType, workflowType),
		attribute.String(submissionLabelResult, result),
		attribute.String(submissionLabelReason, reason),
	))
}

func currentWorkflowSubmissionRecorder() func(string, string, string) {
	workflowSubmissionRecorderMu.RLock()
	defer workflowSubmissionRecorderMu.RUnlock()
	return workflowSubmissionRecorder
}

// setWorkflowSubmissionRecorderForTest installs a capture function that
// replaces the OTEL emission path. Tests MUST defer the returned restore
// closure to avoid leaking state into other tests.
func setWorkflowSubmissionRecorderForTest(fn func(workflowType, result, reason string)) (restore func()) {
	workflowSubmissionRecorderMu.Lock()
	prev := workflowSubmissionRecorder
	workflowSubmissionRecorder = fn
	workflowSubmissionRecorderMu.Unlock()
	return func() {
		workflowSubmissionRecorderMu.Lock()
		workflowSubmissionRecorder = prev
		workflowSubmissionRecorderMu.Unlock()
	}
}
