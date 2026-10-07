package database

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metric names emitted from the job persistence layer whenever a job crosses from
// a non-terminal state (NEW / PROCESSING / WAIT_FOR_TEMPORAL) into a terminal state
// (DONE / ERROR / CANCELLED). Both metrics share the same
// {job_type, state, reason} label set so dashboards can pivot between
// "how many jobs finished" and "how long did they take" and split the
// error/canceled buckets by what triggered the terminal write.
const (
	metricNameJobStatus   = "vcp.job.status"
	metricNameJobDuration = "vcp.job.duration"

	// Values used for the `state` label. Lowercase forms of the DB terminal
	// states (DONE / ERROR / CANCELLED) so a dashboard value matches the row.
	jobMetricStateDone     = "done"
	jobMetricStateError    = "error"
	jobMetricStateCanceled = "canceled"

	// Values used for the `reason` label. Kept intentionally small so dashboards
	// can split error/canceled series without a cardinality blow-up.
	//   "" (empty string)   -> the terminal state is DONE (no reason needed);
	//                          dashboards see this as reason="" on the done
	//                          series. See jobMetricReasonNone below.
	//   workflow_error      -> the owning workflow or an activity marked the
	//                          job terminal via UpdateJob after hitting a
	//                          failure (or, in theory, a cancel — no
	//                          production path writes CANCELLED through
	//                          UpdateJob today, cancellation goes through the
	//                          bulk-cancel path below). This is the default
	//                          when a caller does not supply a reason.
	//   supervisor_timeout  -> the workflow supervisor finalized a stuck job
	//                          after the workflow timeout fired. Callers must
	//                          opt in by passing UpdateJobOpts{Reason:
	//                          JobTerminalReasonSupervisorTimeout}; the DB
	//                          layer cannot infer this from the write alone.
	//   resource_deleted    -> the job was terminated as a side effect of its
	//                          parent resource being deleted (bulk cancels in
	//                          CancelRunningJobsForResource and
	//                          CancelPrepopulateJobsForVolume).
	//   job_deleted         -> the job itself was soft-deleted via DeleteJob,
	//                          which forces state = ERROR independent of the
	//                          workflow outcome.
	jobMetricReasonNone              = ""
	jobMetricReasonWorkflowError     = "workflow_error"
	jobMetricReasonSupervisorTimeout = "supervisor_timeout"
	jobMetricReasonResourceDeleted   = "resource_deleted"
	jobMetricReasonJobDeleted        = "job_deleted"

	jobMetricLabelJobType = "job_type"
	jobMetricLabelState   = "state"
	jobMetricLabelReason  = "reason"
)

// JobTerminalReason tags the terminal-write path that drove a job into its
// final state. Callers pass this via UpdateJobOpts.Reason so the metric
// emission can attribute error/canceled series to the right actor
// (workflow/activity, supervisor timeout, etc.). Keep the exported set small
// and well-defined: every new value adds a new series per {job_type, state}
// to the vcp.job.* metrics.
//
// Only values documented here are considered stable for dashboards and
// alerts; the empty string is reserved for state=done.
type JobTerminalReason string

const (
	// JobTerminalReasonWorkflowError is the default when a caller does not
	// supply a reason. It means "the owning workflow or one of its activities
	// finalized this job via UpdateJob after an error" — the overwhelming
	// majority of UpdateJob callers. Non-workflow callers (new admin paths,
	// cron jobs, etc.) MUST NOT rely on this default; add a dedicated
	// JobTerminalReason value instead so the metric remains truthful.
	JobTerminalReasonWorkflowError JobTerminalReason = jobMetricReasonWorkflowError

	// JobTerminalReasonSupervisorTimeout is set by the workflow supervisor
	// when it finalizes a job whose owning workflow blew past the timeout.
	// Dashboards use this to track stuck-workflow cleanup rate without
	// having to filter by scrape pod/service labels.
	JobTerminalReasonSupervisorTimeout JobTerminalReason = jobMetricReasonSupervisorTimeout
)

// jobDurationBucketBoundariesSeconds spans typical VCP job runtimes:
// short API-driven jobs finish in seconds, long provisioning workflows
// can run for minutes, and supervisor-closed jobs can exceed one hour
// (60m workflow timeout + grace + the delay until the next supervisor
// scan picks the job up). The 4h ceiling keeps those finite instead of
// bucketing them all into +Inf.
var jobDurationBucketBoundariesSeconds = []float64{5, 30, 60, 300, 900, 1800, 3600, 7200, 14400}

var (
	jobMetricsInitOnce   sync.Once
	jobStatusCounter     metric.Int64Counter
	jobDurationHistogram metric.Float64Histogram

	// jobTerminalMetricRecorderMu guards jobTerminalMetricRecorder so tests can
	// swap in a capturing recorder without racing with production emissions.
	jobTerminalMetricRecorderMu sync.RWMutex
	jobTerminalMetricRecorder   func(jobType, terminalState, reason string, durationSeconds float64)
)

// registerJobStatusMetrics lazily initializes the OTEL instruments. It is
// safe to call concurrently; the sync.Once guarantees single initialization.
// Callers should not rely on either instrument being non-nil after this call:
// if OTEL rejects the definition we log and continue so the DB write path
// never fails due to a metrics setup problem.
func registerJobStatusMetrics() {
	jobMetricsInitOnce.Do(func() {
		meter := otel.Meter("github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp")

		var err error
		jobStatusCounter, err = meter.Int64Counter(
			metricNameJobStatus,
			metric.WithDescription("Total number of VCP jobs that transitioned into a terminal state, partitioned by job_type, state, and reason."),
			metric.WithUnit("{job}"),
		)
		if err != nil {
			log.Printf("Failed to create %s counter: %v", metricNameJobStatus, err)
		}

		jobDurationHistogram, err = meter.Float64Histogram(
			metricNameJobDuration,
			metric.WithDescription("End-to-end duration of VCP jobs, measured from CreatedAt to the terminal UpdatedAt, in seconds; partitioned by job_type, state, and reason."),
			metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(jobDurationBucketBoundariesSeconds...),
		)
		if err != nil {
			log.Printf("Failed to create %s histogram: %v", metricNameJobDuration, err)
		}
	})
}

// classifyJobTerminalState maps a datamodel job state to the `state` metric
// label. The second return value is false when the state is non-terminal so
// callers can cheaply skip emission without duplicating the state table.
//
// Matching is case-insensitive: callers may pass any casing of the state
// string (e.g. "DONE", "done", "Done") and it will map to the same label.
// This guards against silent under-counting when a caller normalizes state
// differently than the canonical uppercase constants in datamodel.
func classifyJobTerminalState(state string) (label string, terminal bool) {
	switch datamodel.JobState(strings.ToUpper(state)) {
	case datamodel.JobsStateDONE:
		return jobMetricStateDone, true
	case datamodel.JobsStateERROR:
		return jobMetricStateError, true
	case datamodel.JobsStateCANCELLED:
		return jobMetricStateCanceled, true
	default:
		return "", false
	}
}

// isNonTerminalJobState returns true for the states we expect a running job to
// occupy before reaching a terminal value. Anything not in this set (including
// unknown/garbage values) is treated as terminal so we never re-emit for a job
// that has already been finalized.
//
// Matching is case-insensitive: see classifyJobTerminalState for rationale.
func isNonTerminalJobState(state string) bool {
	switch datamodel.JobState(strings.ToUpper(state)) {
	case datamodel.JobsStateNEW,
		datamodel.JobsStatePROCESSING,
		datamodel.JobsStateWaitForTemporal:
		return true
	default:
		return false
	}
}

// recordJobTerminalMetric emits both the status counter and the duration
// histogram for a single job that has just transitioned into a terminal state.
// The caller MUST have verified the transition was non-terminal -> terminal
// (strict transition check) so this function is idempotent-by-construction:
// a repeated call with the same {jobType, terminalStateLabel, reason}
// indicates a caller bug, not a metric bug.
//
// `reason` is one of jobMetricReason* and describes what caused the terminal
// write; it is emitted as an empty string for state=done. Callers should pass
// jobMetricReasonNone (the empty string) in that case; the function does not
// enforce the state↔reason pairing because the caller already knows both.
//
// durationSeconds is derived from (terminalAt - createdAt) and clamped at 0
// to guard against clock skew or a zero-value CreatedAt producing a negative
// bucket assignment.
func recordJobTerminalMetric(ctx context.Context, jobType, terminalStateLabel, reason string, createdAt, terminalAt time.Time) {
	durationSeconds := terminalAt.Sub(createdAt).Seconds()
	if durationSeconds < 0 {
		durationSeconds = 0
	}

	// Test-only hook (set via setJobTerminalMetricRecorderForTest); nil in prod.
	if hook := currentJobTerminalMetricRecorder(); hook != nil {
		hook(jobType, terminalStateLabel, reason, durationSeconds)
		return
	}

	registerJobStatusMetrics()

	attrs := metric.WithAttributes(
		attribute.String(jobMetricLabelJobType, jobType),
		attribute.String(jobMetricLabelState, terminalStateLabel),
		attribute.String(jobMetricLabelReason, reason),
	)

	if jobStatusCounter != nil {
		jobStatusCounter.Add(ctx, 1, attrs)
	}
	if jobDurationHistogram != nil {
		jobDurationHistogram.Record(ctx, durationSeconds, attrs)
	}
}

func currentJobTerminalMetricRecorder() func(string, string, string, float64) {
	jobTerminalMetricRecorderMu.RLock()
	defer jobTerminalMetricRecorderMu.RUnlock()
	return jobTerminalMetricRecorder
}

// setJobTerminalMetricRecorderForTest installs a capture function that
// replaces the OTEL emission path. Tests MUST defer the returned restore
// closure to avoid leaking state into other tests.
func setJobTerminalMetricRecorderForTest(fn func(jobType, terminalState, reason string, durationSeconds float64)) (restore func()) {
	jobTerminalMetricRecorderMu.Lock()
	prev := jobTerminalMetricRecorder
	jobTerminalMetricRecorder = fn
	jobTerminalMetricRecorderMu.Unlock()
	return func() {
		jobTerminalMetricRecorderMu.Lock()
		jobTerminalMetricRecorder = prev
		jobTerminalMetricRecorderMu.Unlock()
	}
}
