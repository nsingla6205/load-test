package workflows

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// splitPollInterval is how long we sleep between GetOntapJob polls.
const splitPollInterval = 5 * time.Second

type volumePollSplitWorkflow struct {
	// add fields needed for split volume workflow
	BaseWorkflow
}

// retryableErrorEntry defines a single retryable error pattern.
// Match is done by error type AND/OR message substring.
type retryableErrorEntry struct {
	// ErrorType matches temporal ApplicationError.Type()
	// Empty string means "don't match on type"
	ErrorType string
	// MessageContains matches if error message contains this substring (case-insensitive)
	// Empty string means "don't match on message"
	MessageContains string
}

// Enforcing the WorkflowInterface on volumePollSplitWorkflow
var _ WorkflowInterface = &volumePollSplitWorkflow{}

// retryableErrors defines the retryable errors in this workflow
// To add a new retryable error, simply add an entry here.
var retryableErrors = []retryableErrorEntry{
	{
		ErrorType:       "TimeoutErr",
		MessageContains: "Retries exhausted when attempting to reach the storage server",
	},
}

// VolumePollSplitWorkflow polls the ONTAP split job that was already initiated synchronously
// by _splitStartVolume, then cleans up the clone snapshot once the split succeeds.
//
// The workflow uses ContinueAsNew to avoid hitting Temporal's event history limit for
// long-running splits (large volumes can take hours or days). Each continuation receives
// the same arguments so polling resumes seamlessly.
//
// Arguments:
//   - volume:       the volume being split (carries CloneParentInfo for snapshot cleanup)
//   - node:         the ONTAP node to use when polling the job
//   - ontapJobUUID: the ONTAP job UUID returned by InitiateSplitVolume; may be empty if
//     ONTAP completed synchronously (no polling needed in that case)
func VolumePollSplitWorkflow(ctx workflow.Context, volume *datamodel.Volume, node *models.Node, ontapJobUUID string) error {
	log := util.GetLogger(ctx)
	volumeWf := new(volumePollSplitWorkflow)
	err := volumeWf.Setup(ctx, volume)
	if err != nil {
		log.Errorf("Volume split workflow setup executed with error: %v", err)
		return err
	}
	volumeWf.Status = WorkflowStatusRunning
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Processing for VolumePollSplitWorkflow, continuing as new:%v", err)
		return workflow.NewContinueAsNewError(ctx, VolumePollSplitWorkflow, volume, node, ontapJobUUID)
	}

	_, errRun := volumeWf.Run(ctx, volume, node, ontapJobUUID)
	if errRun != nil {
		// ContinueAsNew is not a real failure — propagate it immediately without marking
		// the job as ERROR or running any cleanup. The new execution takes over.
		if workflow.IsContinueAsNewError(errRun.OriginalErr) {
			return errRun.OriginalErr
		}
		log.Errorf("VolumePollSplitWorkflow completed with error: %v", errRun)
		volumeWf.Status = WorkflowStatusFailed
		err2 := volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), errRun)
		if err2 != nil {
			log.Errorf("Failed to update job status to ERROR for VolumePollSplitWorkflow: %v", err2)
			return err2
		}
		return errRun
	}

	volumeWf.Status = WorkflowStatusCompleted
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Done for VolumePollSplitWorkflow, continuing as new:  %v", err)
		return workflow.NewContinueAsNewError(ctx, VolumePollSplitWorkflow, volume, node, ontapJobUUID)
	}
	return nil
}

func (wf *volumePollSplitWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	volume := input.(*datamodel.Volume)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = volume.Account.Name
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *volumePollSplitWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := util.GetLogger(ctx)
	volume := args[0].(*datamodel.Volume)
	node := args[1].(*models.Node)
	ontapJobUUID := args[2].(string)

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	options := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	// runErr is set before every early-return so the cleanup defer can inspect the outcome.
	var runErr *vsaerrors.CustomError

	// Cleanup defer: always reconcile clone state and clones_shared_bytes when Run exits.
	//   - Success        → remove CloneParentInfo entirely (volume is no longer a clone)
	//   - ONTAP failure  → set state to "SPLIT_FAILED", keep bytes at 0, record ONTAP error in stateDetails
	//   - ContinueAsNew  → skip entirely; the new execution is still polling, nothing to reconcile
	defer func() {
		if volume.VolumeAttributes == nil || volume.VolumeAttributes.CloneParentInfo == nil {
			return
		}
		// Do not update clone state when ContinueAsNew is in progress — the split is still running.
		if runErr != nil && workflow.IsContinueAsNewError(runErr.OriginalErr) {
			return
		}
		cloneState := ""
		bytesToSet := uint64(0)
		stateDetails := ""
		removeCloneInfo := runErr == nil
		if runErr != nil {
			// All errors here are ONTAP poll failures (the ONTAP call already succeeded
			// synchronously before the workflow was dispatched).
			// classifyONTAPSplitError sets the user-facing message in CustomError.Message
			// and stores the raw ONTAP text in OriginalErr (already logged at poll time).
			cloneState = datamodel.CloneStateErrorInSplitting
			stateDetails = runErr.GetMessage()
		}
		if updateErr := workflow.ExecuteActivity(
			ctx,
			activities.VolumeCreateActivity.UpdateCloneParentStateInDB,
			volume.UUID,
			cloneState,
			bytesToSet,
			stateDetails,
			removeCloneInfo,
		).Get(ctx, nil); updateErr != nil {
			log.Errorf("Failed to update clone parent state for volume %s: %v", volume.UUID, updateErr)
		}
	}()

	// Poll the ONTAP split job until it completes (or we need to ContinueAsNew).
	// If ontapJobUUID is empty, ONTAP completed synchronously — nothing to poll.
	if ontapJobUUID != "" {
		if err = pollONTAPSplitJob(ctx, volume, node, ontapJobUUID); err != nil {
			runErr = ConvertToVSAError(err)
			return nil, runErr
		}
		log.Infof("ONTAP split job %s completed successfully for volume %s", ontapJobUUID, volume.Name)
	} else {
		log.Infof("No ONTAP job UUID provided for volume %s; ONTAP split completed synchronously", volume.Name)
	}

	// Clean up the clone snapshot that was created when the volume was cloned.
	if err = workflow.ExecuteActivity(ctx, activities.VolumeSplitActivity.CleanupSplitSnapshot, volume).Get(ctx, nil); err != nil {
		// Snapshot cleanup failure is non-fatal — the split itself succeeded.
		log.Warnf("Failed to clean up split snapshot for volume %s: %v", volume.Name, err)
	}

	return nil, nil
}

// ontapSplitErrorCode constants for ONTAP error codes relevant to clone split.
const (
	ontapErrCodeNoSpace   = "458753"
	ontapErrCodeJobKilled = "460765"
)

// ClassifyONTAPSplitError converts a raw ONTAP split job failure (message + error code) into
// a user-facing CustomError with a 4xx HTTP code. The exact ONTAP message is returned via
// ontapMsg so callers can log it separately without exposing it to end users.
//
//   - 458753 (no space)  → ErrSplitCloneNoSpace  (400) — instructs user to free pool space
//   - 460765 (killed)    → ErrSplitCloneJobKilled (400) — job interrupted, retry
//   - anything else      → ErrSplitCloneJobFailed (400) — generic backend failure
func ClassifyONTAPSplitError(ontapMessage, ontapCode string, clonesSharedBytes uint64) (userErr *vsaerrors.CustomError, ontapMsg string) {
	if ontapCode != "" {
		ontapMsg = fmt.Sprintf("%s (ONTAP error code: %s)", ontapMessage, ontapCode)
	} else {
		ontapMsg = ontapMessage
	}

	switch ontapCode {
	case ontapErrCodeNoSpace:
		humanBytes := fmt.Sprintf("%d bytes", clonesSharedBytes)
		return vsaerrors.NewVCPErrorWithArgs(vsaerrors.ErrSplitCloneNoSpace, vsaerrors.New(ontapMsg), humanBytes), ontapMsg
	case ontapErrCodeJobKilled:
		return vsaerrors.NewVCPError(vsaerrors.ErrSplitCloneJobKilled, vsaerrors.New(ontapMsg)), ontapMsg
	default:
		return vsaerrors.NewVCPError(vsaerrors.ErrSplitCloneJobFailed, vsaerrors.New(ontapMsg)), ontapMsg
	}
}

// ClassifyGetONTAPJobError  converts a failure from the GetOntapJob activity into a
// permanent user-facing CustomError when the error is terminal (bad/unknown UUID).
//
//   - message  "entry not found"                     → ErrONTAPJobNotFound   (job UUID not found)
//   - message "is an invalid value for field ... <UUID>" → ErrONTAPJobInvalidUUID (malformed UUID)
func ClassifyGetONTAPJobError(err error) (userErr *vsaerrors.CustomError, permanent bool) {
	msg := err.Error()
	if strings.Contains(msg, "entry not found") {
		return vsaerrors.NewVCPError(vsaerrors.ErrONTAPJobNotFound, err), true
	}
	if strings.Contains(msg, "is an invalid value for field") && strings.Contains(msg, "<UUID>") {
		return vsaerrors.NewVCPError(vsaerrors.ErrONTAPJobInvalidUUID, err), true
	}
	return nil, false
}

// classifyONTAPSplitError is the unexported alias used within this package.
func classifyONTAPSplitError(ontapMessage, ontapCode string, clonesSharedBytes uint64) (userErr *vsaerrors.CustomError, ontapMsg string) {
	return ClassifyONTAPSplitError(ontapMessage, ontapCode, clonesSharedBytes)
}

// pollONTAPSplitJob polls the ONTAP job until it succeeds, fails, or a ContinueAsNew
// condition is met. In the last case it returns a ContinueAsNewError so the caller can
// restart the workflow with the same ontapJobUUID.
func pollONTAPSplitJob(ctx workflow.Context, volume *datamodel.Volume, node *models.Node, ontapJobUUID string) error {
	// Capture the run-start time once using workflow.Now (replay-safe) so the poll loop
	// can trigger ContinueAsNew after a configurable elapsed duration, well before the
	// WorkflowRunTimeout would kill the run.
	runStart := workflow.Now(ctx)
	return pollONTAPSplitJobInternal(ctx, volume, node, ontapJobUUID, -1, runStart)
}

// pollONTAPSplitJobInternal is the testable implementation of pollONTAPSplitJob.
//
// maxHistoryLength is an optional fallback threshold used in tests to trigger ContinueAsNew
// by history size; pass -1 to disable it (production uses the time-based trigger instead).
//
// runStart is the time this workflow run began (workflow.Now at run entry). ContinueAsNew
// is triggered when the elapsed time exceeds GetSplitVolumeRunContinueAsNewDuration(),
// ensuring every run restarts well before WorkflowRunTimeout regardless of history size.
func pollONTAPSplitJobInternal(ctx workflow.Context, volume *datamodel.Volume, node *models.Node, ontapJobUUID string, maxHistoryLength int, runStart time.Time) error {
	log := util.GetLogger(ctx)
	continueAsNewAfter := workflowengine.GetSplitVolumeRunContinueAsNewDuration()

	for {
		info := workflow.GetInfo(ctx)
		elapsed := workflow.Now(ctx).Sub(runStart)

		// Trigger ContinueAsNew when:
		//   1. Temporal server recommends it (history approaching server-side limit), OR
		//   2. This run has been alive longer than the configured threshold — ensures we
		//      restart before WorkflowRunTimeout even for splits that complete in a single
		//      run without ever hitting the history limit (e.g. a 120-minute split with a
		//      70-minute run timeout would previously be killed; now it restarts at ~60m), OR
		//   3. A test-supplied maxHistoryLength threshold is reached.
		if info.GetContinueAsNewSuggested() ||
			elapsed >= continueAsNewAfter ||
			(maxHistoryLength >= 0 && info.GetCurrentHistoryLength() >= maxHistoryLength) {
			log.Infof(
				"ContinueAsNew triggered for split job %s on volume %s (elapsed=%s, historyLen=%d)",
				ontapJobUUID, volume.Name, elapsed, info.GetCurrentHistoryLength(),
			)
			return workflow.NewContinueAsNewError(ctx, VolumePollSplitWorkflow, volume, node, ontapJobUUID)
		}

		var job *vsa.OntapJob
		err := workflow.ExecuteActivity(ctx, activities.CommonActivities.GetOntapJob, ontapJobUUID, node).Get(ctx, &job)
		if err != nil {
			if classifiedErr, terminalFailure := ClassifyGetONTAPJobError(err); terminalFailure {
				log.Errorf("GetOntapJob terminal failure for job %s on volume %s: %v", ontapJobUUID, volume.Name, classifiedErr)
				return classifiedErr
			}
			// Check whitelist — only retry on known retryable errors
			if IsRetryableError(err) {
				log.Warnf("GetOntapJob retryable error for job %s on volume %s: %v — triggering ContinueAsNew",
					ontapJobUUID, volume.Name, err)
				return workflow.NewContinueAsNewError(ctx, VolumePollSplitWorkflow, volume, node, ontapJobUUID)
			}
			return err
		}

		switch job.State {
		case "success":
			return nil
		case "failure":
			var ontapMessage, ontapCode string
			if job.Error != nil {
				ontapMessage = job.Error.Message
				ontapCode = job.Error.Code
			} else {
				ontapMessage = fmt.Sprintf("ONTAP split job %s failed with no error message", ontapJobUUID)
			}
			classifiedErr, ontapMsg := classifyONTAPSplitError(ontapMessage, ontapCode, volume.ClonesSharedBytes)
			log.Errorf("ONTAP split job %s failed for volume %s: %s", ontapJobUUID, volume.Name, ontapMsg)
			return classifiedErr
		}

		// Job is still running — sleep before the next poll.
		if err = workflow.Sleep(ctx, splitPollInterval); err != nil {
			return fmt.Errorf("failed to sleep while waiting for ONTAP split job %s: %w", ontapJobUUID, err)
		}
	}
}

// IsRetryableError checks whether the given error matches any entry
// in the retryable errors whitelist.
// Returns true  → ContinueAsNew (retry the workflow)
// Returns false → treat as non-retryable (fail or propagate)
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		// Not an ApplicationError — not retryable by default
		return false
	}
	errType := appErr.Type()
	errMsg := strings.ToLower(appErr.Message())
	for _, entry := range retryableErrors {
		typeMatch := entry.ErrorType == "" || entry.ErrorType == errType
		msgMatch := entry.MessageContains == "" || strings.Contains(errMsg, strings.ToLower(entry.MessageContains))
		// Both conditions must be true (empty = wildcard)
		if typeMatch && msgMatch {
			return true
		}
	}
	return false
}
