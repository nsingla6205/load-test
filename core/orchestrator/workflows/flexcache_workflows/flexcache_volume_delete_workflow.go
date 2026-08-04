package flexcache_workflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/flexcache_activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/flexcache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	enableSmb  = env.GetBool("ENABLE_SMB", false)
	enableLdap = env.GetBool("ENABLE_LDAP", false)
)

type flexCacheVolumeDeleteWorkflow struct {
	workflows.BaseWorkflow
}

// Enforcing the WorkflowInterface on flexCacheVolumeDeleteWorkflow
var _ workflows.WorkflowInterface = &flexCacheVolumeDeleteWorkflow{}

// DeleteFlexCacheVolumeWorkflow Delete FlexCache Volume Workflow process volume related requests from a customer.
func DeleteFlexCacheVolumeWorkflow(ctx workflow.Context, volume *datamodel.Volume) error {
	log := util.GetLogger(ctx)
	flexCacheWf := new(flexCacheVolumeDeleteWorkflow)
	err := flexCacheWf.Setup(ctx, volume)
	if err != nil {
		log.Errorf("FlexCache volume delete workflow setup executed with error: %v", err)
		return err
	}
	if err := flexCacheWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return err
	}
	flexCacheWf.Status = workflows.WorkflowStatusRunning
	err = flexCacheWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Processing for DeleteFlexCacheVolumeWorkflow: %v", err)
		return err
	}

	_, customErr := flexCacheWf.Run(ctx, volume)
	if customErr != nil {
		log.Errorf("DeleteFlexCacheVolumeWorkflow completed with error: %v", customErr)
		flexCacheWf.Status = workflows.WorkflowStatusFailed
		err2 := flexCacheWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			log.Errorf("Failed to update job status to Done with err for DeleteFlexCacheVolumeWorkflow: %v", err2)
			return err2
		}
		return customErr
	}

	flexCacheWf.Status = workflows.WorkflowStatusCompleted
	err = flexCacheWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Done for DeleteFlexCacheVolumeWorkflow: %v", err)
	}
	return err
}

func (wf *flexCacheVolumeDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	volume := input.(*datamodel.Volume)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = volume.Account.Name
	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *flexCacheVolumeDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := util.GetLogger(ctx)
	dbVolume := args[0].(*datamodel.Volume)
	fcDeleteActivity := &flexcache_activities.FlexCacheVolumeDeleteActivity{}
	deleteActivity := &activities.VolumeDeleteActivity{}

	// Handle cancellation if volume is in CREATING state
	cancellationActivity := &activities.CancellationActivity{}
	commonActivity := &activities.CommonActivities{}
	poolActivity := &activities.PoolActivity{}
	ackTimeout, forceTimeout := commonparams.GetCancellationTimeouts("FLEXCACHE")
	if cancelErr := commonparams.HandleCancellationForCreatingResource(ctx, log,
		commonparams.HandleCancellationForCreatingResourceParams{
			ResourceUUID:               dbVolume.UUID,
			ResourceState:              dbVolume.State,
			CreateJobType:              datamodel.JobTypeFlexCacheCreateVolume,
			SignalName:                 CancelFlexCacheSignalName,
			CancellationAckTimeout:     ackTimeout,
			ForceTerminationAckTimeout: forceTimeout,
		},
		poolActivity.GetCreateJobByResourceUUID,
		cancellationActivity,
		commonActivity,
	); cancelErr != nil {
		log.Warnf("Error handling cancellation: %v, proceeding with normal delete", cancelErr)
	}

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
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
	defer func() {
		if err != nil {
			err2 := workflow.ExecuteActivity(ctx, activities.VolumeCreateActivity.UpdateVolumeStateInDB, dbVolume.UUID, datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails).Get(ctx, nil)
			if err2 != nil {
				log.Errorf("Failed to update volume state in DB to error: %v", err2)
			}
		}
	}()

	// Cancel any active prepopulate jobs for this volume to prevent orphaned jobs
	if cancelErr := workflow.ExecuteActivity(ctx, fcDeleteActivity.CancelPrepopulateJobsForVolume, dbVolume.UUID).Get(ctx, nil); cancelErr != nil {
		log.Warnf("Failed to cancel prepopulate jobs for volume %s: %v, continuing with delete", dbVolume.UUID, cancelErr)
		// Don't fail delete if cleanup fails - prepopulate is best-effort
	} else {
		log.Infof("Successfully cancelled prepopulate jobs for volume %s", dbVolume.UUID)
	}

	var dbNodes []*datamodel.Node
	if err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, &dbVolume.Pool.ID).Get(ctx, &dbNodes); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   dbVolume.Pool.DeploymentName,
		OntapCredentials: dbVolume.Pool.PoolCredentials,
	})

	flexCacheResult := flexcache.DeleteFlexCacheResult{
		DBVolume: dbVolume,
		Node:     node,
	}

	if err = workflow.ExecuteActivity(ctx, fcDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, &flexCacheResult).Get(ctx, nil); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if err = workflow.ExecuteActivity(ctx, fcDeleteActivity.WaitForFlexCacheCreateWorkflowTerminalActivity, &flexCacheResult).Get(ctx, nil); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Reload volume from DB after create workflow has finished cancelling so we have the most up to date volume information
	if err = workflow.ExecuteActivity(ctx, fcDeleteActivity.RefreshDBVolumeForDeleteActivity, &flexCacheResult).Get(ctx, &flexCacheResult); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	dbVolume = flexCacheResult.DBVolume

	if err = workflow.ExecuteActivity(ctx, fcDeleteActivity.UnmountVolumeInOntapActivity, &flexCacheResult).Get(ctx, &flexCacheResult); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if err = workflows.WaitForONTAPJob(ctx, flexCacheResult.UnmountJobResponse, node, time.Minute*10); err != nil {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("failed to unmount volume: %w", err))
	}

	if err = workflow.ExecuteActivity(ctx, fcDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, &flexCacheResult).Get(ctx, &flexCacheResult); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if err = workflows.WaitForONTAPJob(ctx, flexCacheResult.DeleteJobResponse, node, time.Minute*10); err != nil {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("failed to delete FlexCache volume: %w", err))
	}

	if err = workflow.ExecuteActivity(ctx, activities.VolumeDeleteActivity.DeleteExportPolicy, &flexCacheResult.DBVolume, &flexCacheResult.Node).Get(ctx, nil); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if err = workflow.ExecuteActivity(ctx, fcDeleteActivity.DeleteSVMPeeringInOntapActivity, &flexCacheResult).Get(ctx, &flexCacheResult); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if err = workflow.ExecuteActivity(ctx, fcDeleteActivity.GetClusterPeeringFromDBActivity, &flexCacheResult).Get(ctx, &flexCacheResult); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if err = workflow.ExecuteActivity(ctx, fcDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, &flexCacheResult).Get(ctx, &flexCacheResult); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if flexCacheResult.VolumeReplicationCountOnClusterPeering == 0 && flexCacheResult.FlexCacheVolumeCountOnClusterPeering == 1 {
		if err = workflow.ExecuteActivity(ctx, fcDeleteActivity.DeleteClusterPeerInOntapActivity, &flexCacheResult).Get(ctx, &flexCacheResult); err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		if err = workflow.ExecuteActivity(ctx, fcDeleteActivity.UpdateClusterPeeringRowStateDeletedInDBActivity, &flexCacheResult).Get(ctx, &flexCacheResult); err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		if err = workflow.ExecuteActivity(ctx, fcDeleteActivity.DeleteClusterPeeringRowInDBActivity, &flexCacheResult).Get(ctx, &flexCacheResult); err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	if enableLdap && dbVolume.Pool.PoolAttributes.LdapEnabled {
		var isLastFilesVolume bool
		err = workflow.ExecuteActivity(ctx, deleteActivity.DetermineIfVolumeIsLastFilesVolume, dbVolume, node).Get(ctx, &isLastFilesVolume)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		if isLastFilesVolume {
			err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteLDAPConfiguration, dbVolume, node).Get(ctx, nil)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}
		}
	}

	if enableSmb && utils.IsSMBProtocols(dbVolume.VolumeAttributes.Protocols) {
		err = workflow.ExecuteChildWorkflow(ctx, workflows.SmbTeardownWorkflow, dbVolume, node).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	if err = workflow.ExecuteActivity(ctx, activities.VolumeDeleteActivity.DeleteVolume, &dbVolume).Get(ctx, nil); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	return nil, nil
}
