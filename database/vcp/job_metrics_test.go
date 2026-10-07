package database

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"gorm.io/gorm"
)

// capturedJobMetric records a single invocation of the terminal-metric hook so
// tests can assert on the job_type, state label, reason label, and duration
// bucket.
type capturedJobMetric struct {
	jobType         string
	terminalState   string
	reason          string
	durationSeconds float64
}

// newJobMetricRecorder returns a thread-safe capture function together with a
// getter for the accumulated events. Meant to be passed to
// setJobTerminalMetricRecorderForTest.
func newJobMetricRecorder() (record func(string, string, string, float64), snapshot func() []capturedJobMetric) {
	var (
		mu     sync.Mutex
		events []capturedJobMetric
	)
	record = func(jobType, terminalState, reason string, durationSeconds float64) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, capturedJobMetric{
			jobType:         jobType,
			terminalState:   terminalState,
			reason:          reason,
			durationSeconds: durationSeconds,
		})
	}
	snapshot = func() []capturedJobMetric {
		mu.Lock()
		defer mu.Unlock()
		out := make([]capturedJobMetric, len(events))
		copy(out, events)
		return out
	}
	return record, snapshot
}

func TestClassifyJobTerminalState(t *testing.T) {
	cases := []struct {
		name       string
		state      string
		wantLabel  string
		wantIsTerm bool
	}{
		{"DONE maps to done", string(datamodel.JobsStateDONE), jobMetricStateDone, true},
		{"ERROR maps to error", string(datamodel.JobsStateERROR), jobMetricStateError, true},
		{"CANCELLED maps to canceled", string(datamodel.JobsStateCANCELLED), jobMetricStateCanceled, true},
		{"NEW is not terminal", string(datamodel.JobsStateNEW), "", false},
		{"PROCESSING is not terminal", string(datamodel.JobsStatePROCESSING), "", false},
		{"WAIT_FOR_TEMPORAL is not terminal", string(datamodel.JobsStateWaitForTemporal), "", false},
		{"unknown state is not terminal", "GARBAGE", "", false},
		{"empty state is not terminal", "", "", false},
		// Case-insensitive contract: mixed/lower case must map to the same
		// label as the canonical uppercase constant.
		{"lowercase done maps to done", "done", jobMetricStateDone, true},
		{"mixed-case Done maps to done", "Done", jobMetricStateDone, true},
		{"lowercase error maps to error", "error", jobMetricStateError, true},
		{"lowercase cancelled maps to canceled", "cancelled", jobMetricStateCanceled, true},
		{"scrambled-case CanCelLeD maps to canceled", "CanCelLeD", jobMetricStateCanceled, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			label, terminal := classifyJobTerminalState(tc.state)
			assert.Equal(t, tc.wantLabel, label)
			assert.Equal(t, tc.wantIsTerm, terminal)

			// Sanity: uppercase equivalent of the same input must produce
			// the same result. Confirms the case-insensitive contract.
			upperLabel, upperTerminal := classifyJobTerminalState(strings.ToUpper(tc.state))
			assert.Equal(t, upperLabel, label, "case-insensitive parity")
			assert.Equal(t, upperTerminal, terminal, "case-insensitive parity")
		})
	}
}

func TestIsNonTerminalJobState(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{string(datamodel.JobsStateNEW), true},
		{string(datamodel.JobsStatePROCESSING), true},
		{string(datamodel.JobsStateWaitForTemporal), true},
		{string(datamodel.JobsStateDONE), false},
		{string(datamodel.JobsStateERROR), false},
		{string(datamodel.JobsStateCANCELLED), false},
		{"UNKNOWN", false},
		{"", false},
		// Case-insensitive contract.
		{"done", false},
		{"Done", false},
		{"error", false},
		{"cancelled", false},
		{"CanCelLeD", false},
		{"new", true},
		{"processing", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.state, func(t *testing.T) {
			assert.Equal(t, tc.want, isNonTerminalJobState(tc.state))
			// Case-insensitive parity check.
			assert.Equal(t, tc.want, isNonTerminalJobState(strings.ToUpper(tc.state)))
		})
	}
}

func TestRecordJobTerminalMetric_UsesHookAndForwardsAllArgs(t *testing.T) {
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	created := time.Now().Add(-45 * time.Second)
	terminal := created.Add(45 * time.Second)

	recordJobTerminalMetric(context.Background(), "CreatePoolWorkflow", jobMetricStateDone, jobMetricReasonNone, created, terminal)

	got := snapshot()
	require.Len(t, got, 1)
	assert.Equal(t, "CreatePoolWorkflow", got[0].jobType)
	assert.Equal(t, jobMetricStateDone, got[0].terminalState)
	assert.Equal(t, jobMetricReasonNone, got[0].reason)
	assert.InDelta(t, 45.0, got[0].durationSeconds, 0.001)
}

func TestRecordJobTerminalMetric_ClampsNegativeDuration(t *testing.T) {
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	// Clock skew or zero-value CreatedAt could produce a "future" created.
	future := time.Now().Add(10 * time.Second)
	terminal := time.Now()

	recordJobTerminalMetric(context.Background(), "TypeX", jobMetricStateError, jobMetricReasonWorkflowError, future, terminal)

	got := snapshot()
	require.Len(t, got, 1)
	assert.GreaterOrEqual(t, got[0].durationSeconds, 0.0, "negative durations must be clamped to zero")
}

func TestUpdateJob_EmitsMetricOnNonTerminalToTerminal(t *testing.T) {
	// UpdateJob is the single-row terminal-write path, so it always emits with
	// reason=workflow regardless of which terminal state the caller requested.
	// The {supervisor timeout vs activity failure} split is intentionally not
	// encoded here; dashboards use the {service, pod} Prometheus labels to tell
	// supervisor-driven writes (core-api pod) apart from activity-driven ones
	// (worker pod).
	terminalCases := []struct {
		name       string
		toState    datamodel.JobState
		wantLabel  string
		wantReason string
	}{
		{"PROCESSING to DONE", datamodel.JobsStateDONE, jobMetricStateDone, jobMetricReasonNone},
		{"PROCESSING to ERROR", datamodel.JobsStateERROR, jobMetricStateError, jobMetricReasonWorkflowError},
		// No production path writes CANCELLED through UpdateJob today
		// (bulk-cancel paths use CancelRunningJobsForResource with
		// reason=resource_deleted). This case is retained as a defensive
		// guardrail: if a future caller does opt into UpdateJob+CANCELLED,
		// it will land as reason=workflow_error alongside the other
		// terminal transitions through this path.
		{"PROCESSING to CANCELLED", datamodel.JobsStateCANCELLED, jobMetricStateCanceled, jobMetricReasonWorkflowError},
	}

	for _, tc := range terminalCases {
		tc := tc
		t.Run(tc.name, func(tt *testing.T) {
			store := newStoreForMetricTest(tt)
			record, snapshot := newJobMetricRecorder()
			restore := setJobTerminalMetricRecorderForTest(record)
			defer restore()

			created := time.Now().Add(-10 * time.Second)
			job := &datamodel.Job{
				BaseModel: datamodel.BaseModel{
					ID:        100,
					UUID:      "job-transition-uuid",
					CreatedAt: created,
					UpdatedAt: created,
				},
				State: string(datamodel.JobsStatePROCESSING),
				Type:  string(datamodel.JobTypeCreatePool),
			}
			require.NoError(tt, store.db.Create(job).Error())

			require.NoError(tt, store.UpdateJob(context.Background(), job.UUID, string(tc.toState), 1234, ""))

			got := snapshot()
			require.Len(tt, got, 1, "expected exactly one emission for a fresh terminal transition")
			assert.Equal(tt, string(datamodel.JobTypeCreatePool), got[0].jobType)
			assert.Equal(tt, tc.wantLabel, got[0].terminalState)
			// UpdateJob emits reason=workflow for error/canceled and the
			// empty-string sentinel for done; this is the invariant dashboards
			// depend on when they split error series by reason.
			if tc.wantLabel == jobMetricStateDone {
				assert.Equal(tt, jobMetricReasonNone, got[0].reason, "done emissions carry the empty reason label")
			} else {
				assert.Equal(tt, tc.wantReason, got[0].reason)
			}
			// Duration should reflect the ~10 second gap between CreatedAt and now.
			assert.GreaterOrEqual(tt, got[0].durationSeconds, 9.0)
			assert.Less(tt, got[0].durationSeconds, 120.0, "test should not have taken 2 minutes")
		})
	}
}

func TestUpdateJob_SupervisorTimeoutReasonTagsTerminalError(t *testing.T) {
	// Supervisor cleanup opts in to reason=supervisor_timeout. Without this
	// opt-in the metric would be emitted with the default reason=workflow,
	// which would hide stuck-workflow finalizations inside the main failure
	// bucket. Guard the opt-in contract here so a future refactor can't
	// quietly drop the opts parameter and collapse the series.
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	created := time.Now().Add(-30 * time.Minute)
	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "sup-uuid", CreatedAt: created, UpdatedAt: created},
		State:     string(datamodel.JobsStatePROCESSING),
		Type:      string(datamodel.JobTypeCreatePool),
	}
	require.NoError(t, store.db.Create(job).Error())

	require.NoError(t, store.UpdateJob(
		context.Background(),
		job.UUID,
		string(datamodel.JobsStateERROR),
		1234,
		"workflow supervisor timeout",
		UpdateJobOpts{Reason: JobTerminalReasonSupervisorTimeout},
	))

	got := snapshot()
	require.Len(t, got, 1)
	assert.Equal(t, jobMetricStateError, got[0].terminalState)
	assert.Equal(t, jobMetricReasonSupervisorTimeout, got[0].reason)
}

func TestUpdateJob_ExplicitWorkflowReasonMatchesDefault(t *testing.T) {
	// Passing UpdateJobOpts{Reason: JobTerminalReasonWorkflowError} explicitly
	// must behave identically to passing no opts at all. This covers helper
	// wrappers that always synthesize an opts value for consistency.
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "explicit-wf-uuid", CreatedAt: time.Now()},
		State:     string(datamodel.JobsStatePROCESSING),
		Type:      string(datamodel.JobTypeCreatePool),
	}
	require.NoError(t, store.db.Create(job).Error())

	require.NoError(t, store.UpdateJob(
		context.Background(),
		job.UUID,
		string(datamodel.JobsStateERROR),
		0,
		"",
		UpdateJobOpts{Reason: JobTerminalReasonWorkflowError},
	))

	got := snapshot()
	require.Len(t, got, 1)
	assert.Equal(t, jobMetricReasonWorkflowError, got[0].reason)
}

func TestUpdateJob_EmptyOptsReasonFallsBackToDefault(t *testing.T) {
	// A caller that constructs UpdateJobOpts but leaves Reason unset (zero
	// value) must get the same default as the no-opts call. Prevents a
	// future refactor that chains opts through wrappers from silently
	// emitting reason="" on error series.
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "zero-opts-uuid", CreatedAt: time.Now()},
		State:     string(datamodel.JobsStatePROCESSING),
		Type:      string(datamodel.JobTypeCreatePool),
	}
	require.NoError(t, store.db.Create(job).Error())

	require.NoError(t, store.UpdateJob(
		context.Background(),
		job.UUID,
		string(datamodel.JobsStateCANCELLED),
		0,
		"",
	))

	got := snapshot()
	require.Len(t, got, 1)
	assert.Equal(t, jobMetricStateCanceled, got[0].terminalState)
	assert.Equal(t, jobMetricReasonWorkflowError, got[0].reason)
}

func TestUpdateJob_SupervisorReasonIgnoredOnDoneState(t *testing.T) {
	// Even when the caller passes a non-default reason, state=done emissions
	// carry the empty reason label. Keeps the success series clean and
	// prevents a future supervisor-triggered DONE path (e.g. the supervisor
	// finalizing a job that actually completed) from showing up as
	// {state=done, reason=supervisor_timeout}.
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "done-with-opts-uuid", CreatedAt: time.Now()},
		State:     string(datamodel.JobsStatePROCESSING),
		Type:      string(datamodel.JobTypeCreatePool),
	}
	require.NoError(t, store.db.Create(job).Error())

	require.NoError(t, store.UpdateJob(
		context.Background(),
		job.UUID,
		string(datamodel.JobsStateDONE),
		0,
		"",
		UpdateJobOpts{Reason: JobTerminalReasonSupervisorTimeout},
	))

	got := snapshot()
	require.Len(t, got, 1)
	assert.Equal(t, jobMetricStateDone, got[0].terminalState)
	assert.Equal(t, jobMetricReasonNone, got[0].reason)
}

func TestUpdateJob_NoEmitOnNonTerminalToNonTerminal(t *testing.T) {
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "churn-uuid", CreatedAt: time.Now()},
		State:     string(datamodel.JobsStateNEW),
		Type:      string(datamodel.JobTypeCreatePool),
	}
	require.NoError(t, store.db.Create(job).Error())

	// NEW -> PROCESSING is a legitimate state advance but not terminal.
	require.NoError(t, store.UpdateJob(context.Background(), job.UUID, string(datamodel.JobsStatePROCESSING), 0, ""))
	// PROCESSING -> PROCESSING (state churn from a retry/heartbeat).
	require.NoError(t, store.UpdateJob(context.Background(), job.UUID, string(datamodel.JobsStatePROCESSING), 0, ""))

	assert.Empty(t, snapshot(), "no emission expected for non-terminal transitions")
}

func TestUpdateJob_NoEmitWhenAlreadyTerminal(t *testing.T) {
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "already-done-uuid", CreatedAt: time.Now()},
		State:     string(datamodel.JobsStateDONE),
		Type:      string(datamodel.JobTypeCreatePool),
	}
	require.NoError(t, store.db.Create(job).Error())

	// DONE -> DONE (idempotent finalize from a retried caller).
	require.NoError(t, store.UpdateJob(context.Background(), job.UUID, string(datamodel.JobsStateDONE), 0, ""))
	// DONE -> ERROR (supervisor re-finalizing an already-terminal job).
	require.NoError(t, store.UpdateJob(context.Background(), job.UUID, string(datamodel.JobsStateERROR), 0, "resurrected"))

	assert.Empty(t, snapshot(), "terminal->terminal transitions must not re-emit")
}

func TestUpdateJob_StrictDedupAcrossRepeatedFinalization(t *testing.T) {
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "dedup-uuid", CreatedAt: time.Now()},
		State:     string(datamodel.JobsStatePROCESSING),
		Type:      string(datamodel.JobTypeCreatePool),
	}
	require.NoError(t, store.db.Create(job).Error())

	// First finalize: PROCESSING -> DONE — emits.
	require.NoError(t, store.UpdateJob(context.Background(), job.UUID, string(datamodel.JobsStateDONE), 1, ""))
	// Second finalize: DONE -> DONE — must not emit.
	require.NoError(t, store.UpdateJob(context.Background(), job.UUID, string(datamodel.JobsStateDONE), 2, ""))
	// Third finalize: DONE -> ERROR — must not emit (already terminal).
	require.NoError(t, store.UpdateJob(context.Background(), job.UUID, string(datamodel.JobsStateERROR), 3, "late supervisor sweep"))

	got := snapshot()
	require.Len(t, got, 1, "strict transition check must dedup repeated finalizations")
	assert.Equal(t, jobMetricStateDone, got[0].terminalState)
}

func TestDeleteJob_EmitsErrorOnNonTerminal(t *testing.T) {
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	created := time.Now().Add(-30 * time.Second)
	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "del-uuid", CreatedAt: created, UpdatedAt: created},
		State:     string(datamodel.JobsStatePROCESSING),
		Type:      string(datamodel.JobTypeDeletePool),
	}
	require.NoError(t, store.db.Create(job).Error())

	require.NoError(t, store.DeleteJob(context.Background(), job.UUID, "reason"))

	got := snapshot()
	require.Len(t, got, 1)
	assert.Equal(t, string(datamodel.JobTypeDeletePool), got[0].jobType)
	assert.Equal(t, jobMetricStateError, got[0].terminalState)
	// DeleteJob is a distinct terminal-write path: a running job being
	// soft-deleted. We tag with reason=job_deleted so dashboards can tell
	// "job row was deleted" apart from "workflow failed" even though both
	// land as state=error.
	assert.Equal(t, jobMetricReasonJobDeleted, got[0].reason)
	assert.GreaterOrEqual(t, got[0].durationSeconds, 29.0)
}

func TestDeleteJob_NoEmitWhenAlreadyTerminal(t *testing.T) {
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "del-done-uuid", CreatedAt: time.Now()},
		State:     string(datamodel.JobsStateDONE),
		Type:      string(datamodel.JobTypeDeletePool),
	}
	require.NoError(t, store.db.Create(job).Error())

	require.NoError(t, store.DeleteJob(context.Background(), job.UUID, "cleanup"))

	assert.Empty(t, snapshot(), "DeleteJob on an already-terminal job must not re-emit")
}

func TestCancelRunningJobsForResource_EmitsPerAffectedRow(t *testing.T) {
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	resourceUUID := "resource-abc"
	// CancelRunningJobsForResource matches against the JSONB
	// job_attributes->>'resource_uuid' path (the code toggles to an indexed
	// column when EnableJobResourceUUIDIndex is set, which stays false in
	// tests by default). Populate JobAttributes so CreateJob copies the UUID
	// into both the JSON blob and the indexed column.
	newJob := &datamodel.Job{
		BaseModel:     datamodel.BaseModel{ID: 1, UUID: "new-uuid", CreatedAt: time.Now().Add(-20 * time.Second)},
		ResourceName:  "resA",
		State:         string(datamodel.JobsStateNEW),
		Type:          string(datamodel.JobTypeCreatePool),
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: resourceUUID},
	}
	procJob := &datamodel.Job{
		BaseModel:     datamodel.BaseModel{ID: 2, UUID: "proc-uuid", CreatedAt: time.Now().Add(-15 * time.Second)},
		ResourceName:  "resB",
		State:         string(datamodel.JobsStatePROCESSING),
		Type:          string(datamodel.JobTypeDeletePool),
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: resourceUUID},
	}
	// Terminal — filtered out by WHERE, must not emit.
	doneJob := &datamodel.Job{
		BaseModel:     datamodel.BaseModel{ID: 3, UUID: "done-uuid", CreatedAt: time.Now()},
		ResourceName:  "resC",
		State:         string(datamodel.JobsStateDONE),
		Type:          string(datamodel.JobTypeCreatePool),
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: resourceUUID},
	}
	// Different resource — must not emit.
	otherJob := &datamodel.Job{
		BaseModel:     datamodel.BaseModel{ID: 4, UUID: "other-uuid", CreatedAt: time.Now()},
		ResourceName:  "resD",
		State:         string(datamodel.JobsStatePROCESSING),
		Type:          string(datamodel.JobTypeCreatePool),
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "different-resource"},
	}
	_, err := store.CreateJob(context.Background(), newJob)
	require.NoError(t, err)
	_, err = store.CreateJob(context.Background(), procJob)
	require.NoError(t, err)
	_, err = store.CreateJob(context.Background(), doneJob)
	require.NoError(t, err)
	_, err = store.CreateJob(context.Background(), otherJob)
	require.NoError(t, err)

	require.NoError(t, store.CancelRunningJobsForResource(context.Background(), resourceUUID))

	got := snapshot()
	require.Len(t, got, 2, "expected one emission per running job for the target resource")
	for _, ev := range got {
		assert.Equal(t, jobMetricStateCanceled, ev.terminalState)
		// Bulk-cancel-on-resource-delete emits reason=resource_deleted so
		// dashboards can split {parent resource was deleted} out of the
		// otherwise-opaque canceled bucket.
		assert.Equal(t, jobMetricReasonResourceDeleted, ev.reason)
	}
	// Order is not guaranteed by GORM; check that both job types were emitted.
	types := map[string]bool{got[0].jobType: true, got[1].jobType: true}
	assert.True(t, types[string(datamodel.JobTypeCreatePool)])
	assert.True(t, types[string(datamodel.JobTypeDeletePool)])
}

func TestCancelRunningJobsForResource_NoEmitWhenNothingMatched(t *testing.T) {
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	require.NoError(t, store.CancelRunningJobsForResource(context.Background(), "no-such-resource"))

	assert.Empty(t, snapshot())
}

func TestCancelPrepopulateJobsForVolume_EmitsPerAffectedRow(t *testing.T) {
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restore := setJobTerminalMetricRecorderForTest(record)
	defer restore()

	volumeUUID := "volume-xyz"
	newJob := &datamodel.Job{
		BaseModel:    datamodel.BaseModel{ID: 1, UUID: "pp-new", CreatedAt: time.Now().Add(-25 * time.Second)},
		ResourceName: volumeUUID,
		State:        string(datamodel.JobsStateNEW),
		Type:         string(datamodel.JobTypeFlexCachePrePopulate),
	}
	procJob := &datamodel.Job{
		BaseModel:    datamodel.BaseModel{ID: 2, UUID: "pp-proc", CreatedAt: time.Now().Add(-10 * time.Second)},
		ResourceName: volumeUUID,
		State:        string(datamodel.JobsStatePROCESSING),
		Type:         string(datamodel.JobTypeFlexCachePrePopulate),
	}
	// Terminal — filtered out.
	doneJob := &datamodel.Job{
		BaseModel:    datamodel.BaseModel{ID: 3, UUID: "pp-done", CreatedAt: time.Now()},
		ResourceName: volumeUUID,
		State:        string(datamodel.JobsStateDONE),
		Type:         string(datamodel.JobTypeFlexCachePrePopulate),
	}
	// Wrong type for the same volume — filtered out.
	wrongType := &datamodel.Job{
		BaseModel:    datamodel.BaseModel{ID: 4, UUID: "wrong-type", CreatedAt: time.Now()},
		ResourceName: volumeUUID,
		State:        string(datamodel.JobsStatePROCESSING),
		Type:         string(datamodel.JobTypeCreatePool),
	}
	require.NoError(t, store.db.Create(newJob).Error())
	require.NoError(t, store.db.Create(procJob).Error())
	require.NoError(t, store.db.Create(doneJob).Error())
	require.NoError(t, store.db.Create(wrongType).Error())

	require.NoError(t, store.CancelPrepopulateJobsForVolume(context.Background(), volumeUUID))

	got := snapshot()
	require.Len(t, got, 2)
	for _, ev := range got {
		assert.Equal(t, jobMetricStateError, ev.terminalState)
		assert.Equal(t, string(datamodel.JobTypeFlexCachePrePopulate), ev.jobType)
		// Prepopulate jobs land in state=ERROR (not CANCELLED) when their
		// parent volume is deleted, but the reason is the same bulk-cancel
		// signal as CancelRunningJobsForResource. The state/reason pair
		// {error, resource_deleted} is the fingerprint for this path.
		assert.Equal(t, jobMetricReasonResourceDeleted, ev.reason)
	}
}

// TestCancelRunningJobsForResource_CommitFailurePropagatesAndEmitsNoMetric
// locks down the named-return contract on CancelRunningJobsForResource: if the
// deferred commit fails, the error must surface to the caller AND no terminal
// metric must be emitted. Without the named return `(err error)` on the
// method, the explicit `return nil` after the Update would mask the commit
// failure set by the defer, and the metric defer would see err == nil and
// double-count the transition.
func TestCancelRunningJobsForResource_CommitFailurePropagatesAndEmitsNoMetric(t *testing.T) {
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restoreRecorder := setJobTerminalMetricRecorderForTest(record)
	defer restoreRecorder()

	resourceUUID := "resource-commit-fail"
	// Seed one in-flight job that WOULD have been cancelled on a successful
	// commit, so the only reason the recorder stays empty is the commit
	// failure (not a WHERE miss).
	job := &datamodel.Job{
		BaseModel:     datamodel.BaseModel{ID: 1, UUID: "job-commit-fail", CreatedAt: time.Now().Add(-5 * time.Second)},
		ResourceName:  "resA",
		State:         string(datamodel.JobsStateNEW),
		Type:          string(datamodel.JobTypeCreatePool),
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: resourceUUID},
	}
	_, err := store.CreateJob(context.Background(), job)
	require.NoError(t, err)

	forced := errors.New("forced commit failure")
	origCommit := commitOrRollbackTransaction
	commitOrRollbackTransaction = func(_ log.Logger, _ *gorm.DB, _ *error) error {
		return forced
	}
	defer func() { commitOrRollbackTransaction = origCommit }()

	gotErr := store.CancelRunningJobsForResource(context.Background(), resourceUUID)
	require.Error(t, gotErr, "commit failure must propagate (named-return contract)")
	assert.ErrorIs(t, gotErr, forced, "commit failure must surface the exact underlying error")
	assert.Empty(t, snapshot(), "no terminal metric must be emitted when the commit fails")
}

// TestCancelPrepopulateJobsForVolume_CommitFailurePropagatesAndEmitsNoMetric
// mirrors the above assertion for the prepopulate bulk-cancel path. Same
// contract: commit failures must reach the caller and must not emit metrics.
func TestCancelPrepopulateJobsForVolume_CommitFailurePropagatesAndEmitsNoMetric(t *testing.T) {
	store := newStoreForMetricTest(t)
	record, snapshot := newJobMetricRecorder()
	restoreRecorder := setJobTerminalMetricRecorderForTest(record)
	defer restoreRecorder()

	volumeUUID := "volume-commit-fail"
	// Seed one in-flight prepopulate job; see sibling test for rationale.
	job := &datamodel.Job{
		BaseModel:    datamodel.BaseModel{ID: 1, UUID: "pp-commit-fail", CreatedAt: time.Now().Add(-5 * time.Second)},
		ResourceName: volumeUUID,
		State:        string(datamodel.JobsStateNEW),
		Type:         string(datamodel.JobTypeFlexCachePrePopulate),
	}
	require.NoError(t, store.db.Create(job).Error())

	forced := errors.New("forced commit failure")
	origCommit := commitOrRollbackTransaction
	commitOrRollbackTransaction = func(_ log.Logger, _ *gorm.DB, _ *error) error {
		return forced
	}
	defer func() { commitOrRollbackTransaction = origCommit }()

	gotErr := store.CancelPrepopulateJobsForVolume(context.Background(), volumeUUID)
	require.Error(t, gotErr, "commit failure must propagate (named-return contract)")
	assert.ErrorIs(t, gotErr, forced, "commit failure must surface the exact underlying error")
	assert.Empty(t, snapshot(), "no terminal metric must be emitted when the commit fails")
}

// newStoreForMetricTest wires up an in-memory GORM store and returns the
// DataStoreRepository ready for a clean test.
func newStoreForMetricTest(t *testing.T) *DataStoreRepository {
	t.Helper()
	db, err := SetupTestDB()
	require.NoError(t, err, "failed to set up test database")
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	require.NoError(t, ClearInMemoryDB(store.db.GORM()), "failed to clean up test database")
	return store
}
