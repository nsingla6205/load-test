package backgroundworkflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// getBackupLogicalSizeSyncEnabled returns whether backup logical size sync is enabled
// This function can be overridden in tests for better testability
var getBackupLogicalSizeSyncEnabled = func() bool {
	return env.GetBool("BACKUP_LOGICAL_SIZE_SYNC_ENABLED", true)
}

var getGcbdrBackupChainBackpropEnabled = func() bool {
	return env.GetBool("GCBDR_BACKUP_CHAIN_BACKPROP_ENABLED", false)
}

type SyncLatestBackupLogicalSizeToVolumeAndBackupWF struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &SyncLatestBackupLogicalSizeToVolumeAndBackupWF{}

func SyncLatestBackupLogicalSizeWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	// Check if backup logical size sync is enabled using the getter function
	if !getBackupLogicalSizeSyncEnabled() {
		logger.Debug("Backup logical size sync disabled. Skipping workflow execution.")
		return nil
	}

	syncLatestBackupLogicalSizeWF := new(SyncLatestBackupLogicalSizeToVolumeAndBackupWF)
	err := syncLatestBackupLogicalSizeWF.Setup(ctx, nil)
	if err != nil {
		return err
	}
	syncLatestBackupLogicalSizeWF.Status = workflows.WorkflowStatusRunning
	_, errRun := syncLatestBackupLogicalSizeWF.Run(ctx, nil)
	if errRun != nil {
		syncLatestBackupLogicalSizeWF.Status = workflows.WorkflowStatusFailed
		syncLatestBackupLogicalSizeWF.Logger.Error("Failed to sync latest backup logical size to volume and backup", "Error", errRun)
		return errRun
	}
	syncLatestBackupLogicalSizeWF.Status = workflows.WorkflowStatusCompleted
	syncLatestBackupLogicalSizeWF.Logger.Info("Sync latest backup logical size to volume and backup completed successfully")
	return nil
}

func (wf *SyncLatestBackupLogicalSizeToVolumeAndBackupWF) Setup(ctx workflow.Context, input interface{}) error {
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = "system"
	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, workflows.StatusQueryName, func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *SyncLatestBackupLogicalSizeToVolumeAndBackupWF) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := wf.Logger
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	syncActivity := &backgroundactivities.VolumeBackupSyncActivity{}
	logger.Infof("Starting synchronization of latest backup logical size to volume and backup")

	// Get all volume-backup mappings
	var volumeBackupMap map[int64]*datamodel.VolumeLatestBackup
	err = workflow.ExecuteActivity(ctx, syncActivity.GetVolumeLatestBackupMapActivity).Get(ctx, &volumeBackupMap)
	if err != nil {
		logger.Error("GetVolumeLatestBackupMapActivity activity execution failed.", "Error", err)
		return nil, vsaerrors.ExtractCustomError(err)
	}

	logger.Infof("Found %d volumes to sync", len(volumeBackupMap))

	for volumeID, volumeBackup := range volumeBackupMap {
		// Get object store endpoint info (single latest per volume)
		var objStoreEndpointInfo *vsa.SmObjectStoreEndpointt
		err = workflow.ExecuteActivity(ctx, syncActivity.GetObjectStoreEndpointInfoActivity, volumeBackup).Get(ctx, &objStoreEndpointInfo)
		if err != nil {
			logger.Errorf("Failed to get object store endpoint info for volume %s (ID: %d): %v", volumeBackup.Volume.Name, volumeID, err)
			continue
		}

		if objStoreEndpointInfo == nil || objStoreEndpointInfo.LogicalSize == nil {
			logger.Errorf("Object store endpoint info is nil or has nil logical size for volume %s (ID: %d)", volumeBackup.Volume.Name, volumeID)
			continue
		}

		logicalSize := *objStoreEndpointInfo.LogicalSize

		// Update backup and volume with logical size
		err = workflow.ExecuteActivity(ctx, syncActivity.UpdateBackupAndVolumeActivity, volumeBackup, logicalSize).Get(ctx, nil)
		if err != nil {
			logger.Errorf("Failed to update backup and volume for volume %s (ID: %d): %v", volumeBackup.Volume.Name, volumeID, err)
			continue
		}

		if getGcbdrBackupChainBackpropEnabled() {
			err = workflow.ExecuteActivity(ctx, syncActivity.BackPropagateGcbdrBackupChainSizesActivity, volumeBackup, logicalSize).Get(ctx, nil)
			if err != nil {
				logger.Errorf("Failed to back-propagate GCBDR backup chain sizes for volume ID %d: %v", volumeID, err)
				continue
			}
		}
	}

	logger.Infof("Latest backup logical size sync to volume and backup completed")

	return nil, nil
}
