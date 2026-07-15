package workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// hostGroupHeartbeatTimeoutSec bounds how long UpdateIGroups may run without a
// heartbeat before Temporal considers the activity stuck and retries it.
var hostGroupHeartbeatTimeoutSec = env.GetUint64("HOSTGROUP_ACTIVITIES_HEARTBEAT_TIMEOUT_SEC", 300)

type hostGroupUpdateWorkflow struct {
	// add fields needed for volume workflow
	BaseWorkflow
	SE *database.Storage
}

// Enforcing the WorkflowInterface on hostGroupUpdateWorkflow
var _ WorkflowInterface = &hostGroupUpdateWorkflow{}

// UpdateHostGroupWorkflow updates the specified host group and returns it
func UpdateHostGroupWorkflow(ctx workflow.Context, hostGroup *datamodel.HostGroup) (gcpgenserver.V1betaUpdateHostGroupRes, error) {
	log := util.GetLogger(ctx)
	hgWf := new(hostGroupUpdateWorkflow)
	err := hgWf.Setup(ctx, hostGroup)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Guard against stale or duplicate runs: only proceed if the job is still NEW.
	// This lets the workflow supervisor safely own timed-out/never-started jobs.
	if err = hgWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}

	hgWf.Status = WorkflowStatusRunning
	err = hgWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	defer func() {
		if err != nil {
			hgWf.Status = WorkflowStatusFailed
			err = hgWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		} else {
			hgWf.Status = WorkflowStatusCompleted
			err = hgWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
		}
	}()

	_, errRun := hgWf.Run(ctx, hostGroup)
	if errRun != nil {
		// Assign to err so the deferred job-status update records ERROR (not DONE).
		err = errRun
		log.Errorf("HostGroup update workflow completed with error: %v", errRun)
		return nil, ConvertToVSAError(errRun)
	}
	log.Infof("HostGroup update workflow completed successfully")
	return nil, nil
}

func (wf *hostGroupUpdateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	hostGroup := input.(*datamodel.HostGroup)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = hostGroup.Account.Name
	wf.Status = "created"
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

func (wf *hostGroupUpdateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	hg := args[0].(*datamodel.HostGroup)
	updateActivity := &activities.HostGroupUpdateActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Read the env-derived heartbeat timeout via SideEffect so the value is recorded
	// in workflow history and stays replay-deterministic. hostGroupHeartbeatTimeoutSec
	// is initialized from the worker's process environment at startup and could change
	// between an in-flight workflow's original execution and a later replay (e.g. a
	// redeploy mid-flight). SideEffect replays the value-at-decision-time from history.
	var heartbeatTimeoutSec uint64
	if err = workflow.SideEffect(ctx, func(_ workflow.Context) interface{} {
		return hostGroupHeartbeatTimeoutSec
	}).Get(&heartbeatTimeoutSec); err != nil {
		return nil, ConvertToVSAError(err)
	}

	options := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    time.Duration(heartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	err = workflow.ExecuteActivity(ctx, updateActivity.UpdateIGroups, hg).Get(ctx, nil)
	if err != nil {
		// Move the host group out of the transitional UPDATING state to ERROR so it
		// is not left stuck; retries are still handled by the activity retry policy.
		if stateErr := workflow.ExecuteActivity(ctx, updateActivity.UpdateHostGroupState, hg,
			datamodel.LifeCycleStateError, datamodel.LifeCycleStateUpdateErrorDetails).Get(ctx, nil); stateErr != nil {
			workflow.GetLogger(ctx).Error("failed to set host group state to ERROR after update failure", "error", stateErr)
		}
		return nil, ConvertToVSAError(err)
	}

	// Update succeeded: return the host group to READY.
	if stateErr := workflow.ExecuteActivity(ctx, updateActivity.UpdateHostGroupState, hg,
		datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Get(ctx, nil); stateErr != nil {
		return nil, ConvertToVSAError(stateErr)
	}

	return nil, nil
}
