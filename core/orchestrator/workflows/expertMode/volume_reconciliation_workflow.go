package expertMode

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	VolumeReconciliationExpertModeRetryMaxAttempts       = env.GetUint64("VOLUME_RECONCILIATION_EXPERT_MODE_ACTIVITIES_MAX_ATTEMPTS", 8)
	VolumeReconciliationExpertModeStartToCloseTimeoutSec = env.GetUint64("VOLUME_RECONCILIATION_EXPERT_MODE_START_TO_CLOSE_TIMEOUT_SEC", 60)
	VolumeReconciliationExpertModeBackoffCoefficient     = env.GetFloat64("VOLUME_RECONCILIATION_EXPERT_MODE_BACKOFF_COEFFICIENT", 1.5)
)

type volumeCreateReconciliationWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &volumeCreateReconciliationWorkflow{}

type volumeDeleteReconciliationWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &volumeDeleteReconciliationWorkflow{}

type volumeUpdateReconciliationWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &volumeUpdateReconciliationWorkflow{}

func VolumeCreateReconciliationWorkflow(ctx workflow.Context, volume *datamodel.ExpertModeVolumes) (interface{}, error) {
	wf := new(volumeCreateReconciliationWorkflow)
	if err := wf.Setup(ctx, volume); err != nil {
		return nil, err
	}
	if err := wf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	wf.Status = workflows.WorkflowStatusRunning
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil); err != nil {
		wf.Status = workflows.WorkflowStatusFailed
		log := workflow.GetLogger(ctx)
		log.Error("Failed to update job status to PROCESSING, attempting to update to ERROR", "error", err)
		err2 := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		if err2 != nil {
			log.Error("Failed to update job status to ERROR", "error", err2)
		}
		return nil, err
	}
	_, cerr := wf.Run(ctx, volume)
	if cerr != nil {
		wf.Status = workflows.WorkflowStatusFailed
		log := workflow.GetLogger(ctx)
		err2 := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), cerr)
		if err2 != nil {
			log.Error("Failed to update job status to ERROR", "error", err2)
		}
		return nil, cerr
	}
	wf.Status = workflows.WorkflowStatusCompleted
	return nil, wf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
}

func (wf *volumeCreateReconciliationWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	volume := input.(*datamodel.ExpertModeVolumes)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = volume.Account.Name

	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "volumeName": volume.Name})
	wf.Logger = util.GetLogger(ctx)
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{ID: wf.ID, Status: wf.Status, CustomerID: wf.CustomerID}, nil
	})
}

func (wf *volumeCreateReconciliationWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	volume := args[0].(*datamodel.ExpertModeVolumes)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	expertModeStartToCloseTimeout := time.Duration(VolumeReconciliationExpertModeStartToCloseTimeoutSec) * time.Second

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: expertModeStartToCloseTimeout,
		HeartbeatTimeout:    expertModeStartToCloseTimeout / 2,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     VolumeReconciliationExpertModeBackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	ao1 := ao
	ao1.RetryPolicy.MaximumAttempts = int32(VolumeReconciliationExpertModeRetryMaxAttempts)
	ctx1 := workflow.WithActivityOptions(ctx, ao1)

	pool := volume.Pool

	var node *models.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.ResolveOntapNodeForPool, pool).Get(ctx, &node)
	if err != nil {
		log.Error("Failed to resolve ONTAP node for pool", "poolID", pool.ID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	activity := &expertmodeactivities.ExpertModeVolumeActivity{}

	var updatedVolume *datamodel.ExpertModeVolumes
	err = workflow.ExecuteActivity(ctx1, activity.FetchOntapVolumeByName, volume, node).Get(ctx1, &updatedVolume)
	// Note: This error handling logic only executes after all activity retries (as per the retry policy)
	// have been exhausted. If FetchOntapVolumeByName fails with a retryable error, Temporal will
	// automatically retry the activity according to the retry policy configured in ctx1. Only when
	// all retry attempts are exhausted will the error be returned here.
	if err != nil {
		vsaErr := workflows.ConvertToVSAError(err)

		// If the volume could not be confirmed in ONTAP after all retries (for any reason: not found,
		// ONTAP unreachable, activity timeout, etc.), mark it DELETED instead of leaving it stuck in
		// CREATING. This is safe because ONTAP is the source of truth: the background expert mode
		// volume reconciler (runs ~every 10 min) re-creates the DB row as AVAILABLE if the volume
		// actually exists in ONTAP, and leaves it deleted otherwise.
		log.Info("Volume could not be confirmed in ONTAP after max retries, marking as DELETED", "volume", volume.Name, "error", vsaErr)
		err2 := workflow.ExecuteActivity(ctx, activity.DeleteExpertModeVolumeInDB, volume.UUID).Get(ctx, nil)
		if err2 != nil {
			log.Error("Failed to delete volume in DB", "error", err2)
		} else {
			log.Info("ExpertMode volume marked as DELETED (not confirmed in ONTAP after all retries exhausted)", "volume", volume.Name)
		}

		return nil, vsaErr
	}

	// Use the updated volume returned from the activity
	err = workflow.ExecuteActivity(ctx, activity.UpdateExpertModeVolumeInDB, updatedVolume).Get(ctx, nil)
	if err != nil {
		log.Error("UpdateExpertModeVolumeInDB activity failed", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	log.Info("ExpertMode volume state updated to AVAILABLE",
		"size", updatedVolume.SizeInBytes, "name", updatedVolume.Name, "style", updatedVolume.Style)

	return nil, nil
}

func VolumeDeleteReconciliationWorkflow(ctx workflow.Context, volume *datamodel.ExpertModeVolumes) (interface{}, error) {
	wf := new(volumeDeleteReconciliationWorkflow)
	if err := wf.Setup(ctx, volume); err != nil {
		return nil, err
	}
	if err := wf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	wf.Status = workflows.WorkflowStatusRunning
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil); err != nil {
		wf.Status = workflows.WorkflowStatusFailed
		log := workflow.GetLogger(ctx)
		log.Error("Failed to update job status to PROCESSING, attempting to update to ERROR", "error", err)
		err2 := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		if err2 != nil {
			log.Error("Failed to update job status to ERROR", "error", err2)
		}
		return nil, err
	}
	_, cerr := wf.Run(ctx, volume)
	if cerr != nil {
		wf.Status = workflows.WorkflowStatusFailed
		log := workflow.GetLogger(ctx)
		err2 := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), cerr)
		if err2 != nil {
			log.Error("Failed to update job status to ERROR", "error", err2)
		}
		return nil, cerr
	}
	wf.Status = workflows.WorkflowStatusCompleted
	return nil, wf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
}

func (wf *volumeDeleteReconciliationWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	volume := input.(*datamodel.ExpertModeVolumes)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = volume.Account.Name

	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "volumeName": volume.Name})
	wf.Logger = util.GetLogger(ctx)
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{ID: wf.ID, Status: wf.Status, CustomerID: wf.CustomerID}, nil
	})
}

func (wf *volumeDeleteReconciliationWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	volume := args[0].(*datamodel.ExpertModeVolumes)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	expertModeStartToCloseTimeout := time.Duration(VolumeReconciliationExpertModeStartToCloseTimeoutSec) * time.Second

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: expertModeStartToCloseTimeout,
		HeartbeatTimeout:    expertModeStartToCloseTimeout / 2,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     VolumeReconciliationExpertModeBackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	ao1 := ao
	ao1.RetryPolicy.MaximumAttempts = int32(VolumeReconciliationExpertModeRetryMaxAttempts)
	ctx1 := workflow.WithActivityOptions(ctx, ao1)

	pool := volume.Pool

	var node *models.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.ResolveOntapNodeForPool, pool).Get(ctx, &node)
	if err != nil {
		log.Error("Failed to resolve ONTAP node for pool", "poolID", pool.ID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	activity := &expertmodeactivities.ExpertModeVolumeActivity{}

	err = workflow.ExecuteActivity(ctx1, activity.CheckVolumeDeletedInOntap, volume, node).Get(ctx1, nil)
	if err != nil {
		vsaErr := workflows.ConvertToVSAError(err)

		// If error is ErrResourceStateConflictError, it means volume still exists after max retries
		// Update the volume state to available in DB since it still exists in ONTAP
		if vsaErr != nil && vsaErr.TrackingID == vsaerrors.ErrResourceStateConflictError {
			log.Info("Volume still exists in ONTAP after max activity retries, updating state to AVAILABLE in DB", "volume", volume.Name)
			volume.State = datamodel.LifeCycleStateAvailable
			err2 := workflow.ExecuteActivity(ctx, activity.UpdateExpertModeVolumeInDB, volume).Get(ctx, nil)
			if err2 != nil {
				log.Error("Failed to update volume state in DB to AVAILABLE; volume still exists in ONTAP, reconciliation complete", "error", err2)
			} else {
				log.Info("ExpertMode volume marked as AVAILABLE in DB (still exists in ONTAP after max retries)", "volume", volume.Name)
			}
		}
		return nil, vsaErr
	}

	log.Info("Volume not found in ONTAP, deletion is complete; marking as DELETED in DB", "volume", volume.Name)
	err = workflow.ExecuteActivity(ctx, activity.DeleteExpertModeVolumeInDB, volume.UUID).Get(ctx, nil)
	if err != nil {
		log.Error("DeleteExpertModeVolumeInDB activity failed", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}
	return nil, nil
}

func VolumeUpdateReconciliationWorkflow(ctx workflow.Context, volume *datamodel.ExpertModeVolumes, oldVolume *datamodel.ExpertModeVolumes) (interface{}, error) {
	wf := new(volumeUpdateReconciliationWorkflow)
	if err := wf.Setup(ctx, volume); err != nil {
		return nil, err
	}
	if err := wf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	wf.Status = workflows.WorkflowStatusRunning
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil); err != nil {
		wf.Status = workflows.WorkflowStatusFailed
		log := workflow.GetLogger(ctx)
		log.Error("Failed to update job status to PROCESSING, attempting to update to ERROR", "error", err)
		jobErr := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		if jobErr != nil {
			log.Error("Failed to update job status to ERROR", "error", jobErr)
		}
		return nil, err
	}
	_, cerr := wf.Run(ctx, volume, oldVolume)
	if cerr != nil {
		wf.Status = workflows.WorkflowStatusFailed
		log := workflow.GetLogger(ctx)
		jobErr := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), cerr)
		if jobErr != nil {
			log.Error("Failed to update job status to ERROR", "error", jobErr)
		}
		return nil, cerr
	}
	wf.Status = workflows.WorkflowStatusCompleted
	return nil, wf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
}

func (wf *volumeUpdateReconciliationWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	volume := input.(*datamodel.ExpertModeVolumes)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = volume.Account.Name

	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "volumeName": volume.Name})
	wf.Logger = util.GetLogger(ctx)
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{ID: wf.ID, Status: wf.Status, CustomerID: wf.CustomerID}, nil
	})
}

func (wf *volumeUpdateReconciliationWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	volume := args[0].(*datamodel.ExpertModeVolumes)
	oldVolume := args[1].(*datamodel.ExpertModeVolumes)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	expertModeStartToCloseTimeout := time.Duration(VolumeReconciliationExpertModeStartToCloseTimeoutSec) * time.Second

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: expertModeStartToCloseTimeout,
		HeartbeatTimeout:    expertModeStartToCloseTimeout / 2,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     VolumeReconciliationExpertModeBackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	ao1 := ao
	ao1.RetryPolicy.MaximumAttempts = int32(VolumeReconciliationExpertModeRetryMaxAttempts)
	ctx1 := workflow.WithActivityOptions(ctx, ao1)

	pool := volume.Pool

	var node *models.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.ResolveOntapNodeForPool, pool).Get(ctx, &node)
	if err != nil {
		log.Error("Failed to resolve ONTAP node for pool", "poolID", pool.ID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	activity := &expertmodeactivities.ExpertModeVolumeActivity{}

	updatedVolume := &datamodel.ExpertModeVolumes{}
	err = workflow.ExecuteActivity(ctx1, activity.ValidateONTAPVolumeUpdate, volume, node).Get(ctx1, &updatedVolume)
	// Note: This error handling logic only executes after all activity retries (as per the retry policy)
	// have been exhausted. If FetchOntapVolumeByName fails with a retryable error, Temporal will
	// automatically retry the activity according to the retry policy configured in ctx1. Only when
	// all retry attempts are exhausted will the error be returned here.
	if err != nil {
		vsaErr := workflows.ConvertToVSAError(err)

		// if the error is ErrResourceStateConflictError, it means volume still isn't updated even after max retries
		// Update the volume state to available in DB since it's available and with older values in ONTAP
		if vsaErr != nil {
			log.Info("Volume not updated in ONTAP after max retries, marking as AVAILABLE", "volumeUUID", volume.UUID, "state", datamodel.LifeCycleStateAvailable)
			err = workflow.ExecuteActivity(ctx1, activity.FetchOntapVolumeByUUID, volume, node).Get(ctx1, &updatedVolume)
			if err != nil {
				log.Error("Failed to fetch volume from ONTAP", "error", err)
			}
			if updatedVolume == nil || err != nil {
				log.Debug("setting the volume to old volume since fetching volume from ONTAP failed", "externalUUID", oldVolume.ExternalUUID, "oldVolumeName", oldVolume.Name, "error", err)
				updatedVolume = oldVolume
			}
			updatedVolume.State = datamodel.LifeCycleStateAvailable
			updateErr := workflow.ExecuteActivity(ctx, activity.UpdateExpertModeVolumeInDB, updatedVolume).Get(ctx, nil)
			if updateErr != nil {
				log.Error("Failed to update volume state in DB to AVAILABLE; volume isn't updated in ONTAP, reconciliation complete", "error", updateErr)
			} else {
				log.Info("ExpertMode volume marked as AVAILABLE (not updated in ONTAP after max retries)", "volume", updatedVolume.Name)
			}
		}
		return nil, vsaErr
	}

	// Use the updated volume returned from the activity
	updatedVolume.State = datamodel.LifeCycleStateAvailable
	err = workflow.ExecuteActivity(ctx, activity.UpdateExpertModeVolumeInDB, updatedVolume).Get(ctx, nil)
	if err != nil {
		log.Error("UpdateExpertModeVolumeInDB activity failed", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	log.Info("ExpertMode volume state updated to AVAILABLE",
		"size", updatedVolume.SizeInBytes, "name", updatedVolume.Name, "style", updatedVolume.Style)

	return nil, nil
}
