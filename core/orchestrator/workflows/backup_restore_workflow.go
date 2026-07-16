package workflows

import (
	"fmt"
	"time"

	"github.com/pkg/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Volume State constants
const (
	VolumeStateRW = "rw"
	VolumeStateDP = "dp"
	VolumeStateLS = "ls"
)

var (
	WaitForRestore                           = time.Duration(10) * time.Second
	backupRestoreWorkflowHeartbeatTimeoutSec = env.GetUint64("BACKUP_RESTORE_WORKFLOW_HEARTBEAT_TIMEOUT_SEC", 600)
	restoredVolumeDPToRWTimeout              = time.Duration(env.GetUint64("RESTORED_VOLUME_DP_TO_RW_TIMEOUT_MINUTES", 30)) * time.Minute
)

type restoreBackupWorkflow struct {
	BaseWorkflow
}

// Enforcing the WorkflowInterface on backupRestoreWorkflow
var _ WorkflowInterface = &restoreBackupWorkflow{}

// RestoreBackupWorkflow Restore Workflow process backup restore related requests from a customer.
func RestoreBackupWorkflow(ctx workflow.Context, params *common.CreateVolumeParams, volume *datamodel.Volume, backupVault *datamodel.BackupVault, backup *datamodel.Backup, hostParams []*common.HostParams, volCreateResponse *vsa.VolumeResponse, backupVaultAccountName string) (gcpgenserver.V1betaDescribeVolumeRes, error) {
	log := util.GetLogger(ctx)
	restoreWf := new(restoreBackupWorkflow)
	err := restoreWf.Setup(ctx, params)
	if err != nil {
		log.Errorf("Failed to setup RestoreBackupWorkflow: %v", err)
		return nil, err
	}
	if err := restoreWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}
	restoreWf.Status = WorkflowStatusRunning
	err = restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Processing for RestoreBackupWorkflow: %v", err)
		return nil, err
	}
	var customErr *vsaerrors.CustomError
	_, customErr = restoreWf.Run(ctx, volume, params, backupVault, backup, hostParams, volCreateResponse, backupVaultAccountName)
	if customErr != nil {
		// Check if the error is a ContinueAsNewError - if so, don't call revert
		if workflow.IsContinueAsNewError(customErr.OriginalErr) {
			return nil, customErr
		}
		log.Errorf("RestoreBackupWorkflow completed with error: %v", customErr.OriginalErr.Error())
		restoreWf.Status = WorkflowStatusFailed
		err2 := restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			log.Errorf("Failed to update job status to Done with error for RestoreBackupWorkflow: %v", err2)
			return nil, err2
		}
		return nil, customErr
	}
	restoreWf.Status = WorkflowStatusCompleted
	err = restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Done for RestoreBackupWorkflow: %v", err)
	}
	return nil, err
}

func RestoreBackupWorkflowWithContext(ctx workflow.Context, backupActivitiesContext *activities.BackupActivitiesContext, params *common.CreateVolumeParams, hostParams []*common.HostParams, volCreateResponse *vsa.VolumeResponse) (gcpgenserver.V1betaDescribeVolumeRes, error) {
	log := util.GetLogger(ctx)
	restoreWf := new(restoreBackupWorkflow)
	err := restoreWf.Setup(ctx, params)
	if err != nil {
		log.Errorf("Failed to setup RestoreBackupWorkflow: %v", err)
		return nil, err
	}
	if err := restoreWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}
	restoreWf.Status = WorkflowStatusRunning
	err = restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Processing for RestoreBackupWorkflow: %v", err)
		return nil, err
	}
	var customErr *vsaerrors.CustomError
	_, customErr = restoreWf.RunWithContext(ctx, backupActivitiesContext, params, hostParams, volCreateResponse)
	if customErr != nil {
		// Check if the error is a ContinueAsNewError - if so, don't call revert
		if workflow.IsContinueAsNewError(customErr.OriginalErr) {
			return nil, customErr
		}
		log.Errorf("RestoreBackupWorkflow completed with error: %v", customErr.OriginalErr.Error())
		restoreWf.Status = WorkflowStatusFailed
		err2 := restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			log.Errorf("Failed to update job status to Done with error for RestoreBackupWorkflow: %v", err2)
			return nil, err2
		}
		return nil, customErr
	}
	restoreWf.Status = WorkflowStatusCompleted
	err = restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Done for RestoreBackupWorkflow: %v", err)
	}
	return nil, err
}

func (wf *restoreBackupWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createVolParams := input.(*common.CreateVolumeParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createVolParams.AccountName
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

func (wf *restoreBackupWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	createVolumeParams := args[1].(*common.CreateVolumeParams)
	hostParams := args[4].([]*common.HostParams)
	volCreateResponse := args[5].(*vsa.VolumeResponse)
	volume := args[0].(*datamodel.Volume)

	var backupVaultAccountName string
	if len(args) > 6 {
		backupVaultAccountName, _ = args[6].(string)
	}
	if backupVaultAccountName == "" {
		backupVaultAccountName = volume.Account.Name
	}

	backupActivitiesContext := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:                 args[3].(*datamodel.Backup),
			BackupVault:            args[2].(*datamodel.BackupVault),
			Volume:                 volume,
			BackupVaultAccountName: backupVaultAccountName,
		},
	}

	return wf.RunWithContext(ctx, backupActivitiesContext, createVolumeParams, hostParams, volCreateResponse)
}

func (wf *restoreBackupWorkflow) RunWithContext(ctx workflow.Context, backupActivitiesContext *activities.BackupActivitiesContext, createVolumeParams *common.CreateVolumeParams, hostParams []*common.HostParams, volCreateResponse *vsa.VolumeResponse) (interface{}, *vsaerrors.CustomError) {
	log := util.GetLogger(ctx)
	isRestoreFromBackup := createVolumeParams.BackupPath != ""
	volumeActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}
	expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
	backupActivity := &activities.BackupActivity{}
	var err error
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: RestoreStartToCloseTimeout,
		HeartbeatTimeout:    time.Duration(backupRestoreWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	rollbackManager := common.NewRollbackManager()
	cleanupManager := common.NewRollbackManager()

	var incrementErr error
	defer func() {
		// Decrement backup restore count after the workflow is complete
		if incrementErr == nil {
			decrementErr := workflow.ExecuteActivity(ctx, backupActivity.UpdateBackupRestoreCount,
				backupActivitiesContext.BackupWorkflowInit.BackupVault.UUID,
				backupActivitiesContext.BackupWorkflowInit.Backup.UUID,
				backupActivitiesContext.BackupWorkflowInit.BackupVaultAccountName, activities.BackupRestoreCountDecrement).Get(ctx, nil)
			if decrementErr != nil {
				log.Errorf("Failed to revert backup restore count: %v", decrementErr)
			}
		}

		disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
		if err != nil {
			if workflow.IsContinueAsNewError(err) {
				return
			}
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
		cleanupManager.ExecuteRollback(disconnectedCtx, err)
	}()

	info := workflow.GetInfo(ctx)
	isContinuation := info.ContinuedExecutionRunID != ""

	var smSourcePath, smDestinationPath string
	if isContinuation {
		wf.Logger.Info("Resuming backup workflow from continuation",
			"workflowID", wf.ID,
			"continuedFromRunID", info.OriginalRunID,
			"snapshotName", backupActivitiesContext.SnapshotName,
			"transferStatus", backupActivitiesContext.TransferStatus)
	} else {
		if createVolumeParams.IsExpertModeRestore {
			rollbackManager.AddActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB, backupActivitiesContext.BackupWorkflowInit.Volume.UUID, datamodel.LifeCycleStateError, datamodel.LifeCycleStateCreationErrorDetails)
		} else {
			rollbackManager.AddActivity(volumeActivity.UpdateVolumeStateInDB, backupActivitiesContext.BackupWorkflowInit.Volume.UUID, datamodel.LifeCycleStateError, datamodel.LifeCycleStateCreationErrorDetails)
		}

		// Increment restore count to indicate that a volume restoration is in-progress for the backup
		incrementErr = workflow.ExecuteActivity(ctx, backupActivity.UpdateBackupRestoreCount,
			backupActivitiesContext.BackupWorkflowInit.BackupVault.UUID,
			backupActivitiesContext.BackupWorkflowInit.Backup.UUID,
			backupActivitiesContext.BackupWorkflowInit.BackupVaultAccountName, activities.BackupRestoreCountIncrement).Get(ctx, nil)
		if incrementErr != nil {
			log.Errorf("Failed to update backup restore count: %v", incrementErr)
			return nil, ConvertToVSAError(incrementErr)
		}

		// Execute VPC pool restoration activity to handle cross-project permissions
		err = workflow.ExecuteActivity(ctx, volumeActivity.CrossPoolOrVPCRestorationActivity, backupActivitiesContext.BackupWorkflowInit.Volume.Pool, backupActivitiesContext.BackupWorkflowInit.Backup).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		var dbNodes []*datamodel.Node
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, &backupActivitiesContext.BackupWorkflowInit.Volume.Pool.ID).Get(ctx, &dbNodes)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
			Nodes:            dbNodes,
			DeploymentName:   backupActivitiesContext.BackupWorkflowInit.Volume.Pool.DeploymentName,
			OntapCredentials: backupActivitiesContext.BackupWorkflowInit.Volume.Pool.PoolCredentials,
		})
		backupActivitiesContext.Node = node

		var objStoreName string
		err = workflow.ExecuteActivity(ctx, backupActivity.GenerateObjectStoreNameForRestore, backupActivitiesContext.BackupWorkflowInit.BackupVault, backupActivitiesContext.BackupWorkflowInit.Backup).Get(ctx, &objStoreName)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		backupActivitiesContext.ObjStoreName = objStoreName

		objStore := &common.CloudTarget{}
		err = workflow.ExecuteActivity(ctx, backupActivity.GetSmSourcePathActivity, backupActivitiesContext.BackupWorkflowInit.Volume).Get(ctx, &smDestinationPath)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		if backupActivitiesContext.BackupWorkflowInit.Backup.Attributes == nil || backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotName == "" {
			err = vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, vsaerrors.New("could not find snapshot name in backup attributes"))
			return nil, ConvertToVSAError(err)
		}
		smSourcePath = fmt.Sprintf("%s:/objstore/%s", objStoreName, backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotName)
		log.Debugf("smDestinationPath: %v", smDestinationPath)
		log.Debugf("smSourcePath: %v", smSourcePath)

		snapmirrorRelationship := &common.SnapmirrorRelationship{}
		SnapmirrorRelationshipParams := &common.SnapmirrorRelationshipParams{
			SourcePath:      smSourcePath,
			DestinationPath: smDestinationPath,
			SourceUUID:      &backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.EndpointUUID,
			IsRestore:       true,
		}

		var bucketDetails *datamodel.BucketDetails
		bucketDetails, err = activities.GetBucketDetailsFromBackup(backupActivitiesContext.BackupWorkflowInit.BackupVault, backupActivitiesContext.BackupWorkflowInit.Backup)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		bucketName := bucketDetails.BucketName

		err = workflow.Sleep(ctx, 60*time.Second)
		if err != nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to sleep before starting snapmirror restore: %w", err))
		}
		err = workflow.ExecuteActivity(ctx, activities.BackupActivity.GetOrCreateObjectStore, node, objStoreName, bucketName).Get(ctx, &objStore)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		cleanupManager.AddActivity(volumeActivity.DeleteRestoreObjectStore, backupActivitiesContext.Node, backupActivitiesContext.ObjStoreName)

		err = workflow.ExecuteActivity(ctx, activities.BackupActivity.SnapmirrorGetOrCreate, node, &SnapmirrorRelationshipParams).Get(ctx, &snapmirrorRelationship)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		cleanupManager.AddActivity(activities.BackupActivity.DeleteSnapmirror, node, snapmirrorRelationship.UUID)

		err = workflow.ExecuteActivity(ctx, activities.BackupActivity.SnapmirrorTransfer, node, snapmirrorRelationship.UUID, backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotName).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		backupActivitiesContext.SnapmirrorRelationship = snapmirrorRelationship
	}

	err = wf.PollTransferStatusWithContinueAsNew(ctx, backupActivitiesContext, createVolumeParams, hostParams, volCreateResponse)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	err = workflow.Sleep(ctx, 30*time.Second)
	if err != nil {
		return nil, ConvertToVSAError(fmt.Errorf("failed to sleep before getting snapmirror: %w", err))
	}

	// Get snapmirror relationship to check health status
	var smRelationship *common.SnapmirrorRelationship
	err = workflow.ExecuteActivity(ctx, backupActivity.GetSnapmirror, backupActivitiesContext.Node, smSourcePath, smDestinationPath).Get(ctx, &smRelationship)
	if err != nil {
		customErr := ConvertToVSAError(err)
		if customErr != nil && customErr.TrackingID == vsaerrors.ErrResourceNotFound {
			wf.Logger.Infof("Restore snapmirror relationship not found after transfer completion")
		} else {
			return nil, customErr
		}
	}

	if smRelationship != nil && smRelationship.Healthy != nil && !*smRelationship.Healthy {
		if smRelationship.UnhealthyReason != nil && len(*smRelationship.UnhealthyReason) > 0 {
			wf.Logger.Infof("Snapmirror relationship is unhealthy. Reasons: %v", *smRelationship.UnhealthyReason)
		}
		err = vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, vsaerrors.New("snapmirror relationship is unhealthy"))
		return nil, ConvertToVSAError(err)
	}

	volResponse := &vsa.VolumeResponse{}
	volumeTypeUpdateDone := false  // reset for polling volume state change to RW
	startTime := workflow.Now(ctx) // Track start time for timeout
	for !volumeTypeUpdateDone {
		// Check if the timeout has been reached
		if workflow.Now(ctx).Sub(startTime) > restoredVolumeDPToRWTimeout {
			log.Errorf("Volume %s failed to transition from DP/LS to RW state within timeout period of %v",
				backupActivitiesContext.BackupWorkflowInit.Volume.UUID, restoredVolumeDPToRWTimeout)
			err = vsaerrors.NewVCPError(vsaerrors.ErrTimeLimitExceeded,
				fmt.Errorf("volume %s failed to transition from DP/LS to RW state within timeout period of %v",
					backupActivitiesContext.BackupWorkflowInit.Volume.UUID, restoredVolumeDPToRWTimeout))
			return nil, ConvertToVSAError(err)
		}

		if errors.Is(ctx.Err(), workflow.ErrCanceled) {
			err = ctx.Err()
			return nil, ConvertToVSAError(err)
		}
		err = workflow.ExecuteActivity(ctx, volumeUpdateActivity.GetVolumeFromONTAP, backupActivitiesContext.BackupWorkflowInit.Volume, backupActivitiesContext.Node, true).Get(ctx, &volResponse)
		if err != nil {
			log.Debugf("Get Volume from Ontap error : %s", err.Error())
			return nil, ConvertToVSAError(err)
		}
		if volResponse.Type == VolumeStateRW {
			log.Debugf("Volume %s is available as RW in ONTAP", backupActivitiesContext.BackupWorkflowInit.Volume.UUID)
			volumeTypeUpdateDone = true
		} else if volResponse.Type == VolumeStateDP || volResponse.Type == VolumeStateLS {
			log.Debugf("Volume %s is still DP/LS and not available as RW in ONTAP", backupActivitiesContext.BackupWorkflowInit.Volume.UUID)
			err = workflow.Sleep(ctx, WaitForRestore) // Wait before polling again
			if err != nil {
				return nil, ConvertToVSAError(fmt.Errorf("failed to sleep during volume availability polling: %w", err))
			}
		} else {
			log.Debugf("Type of volume %s is not correct. Current state in ONTAP is: %s", backupActivitiesContext.BackupWorkflowInit.Volume.UUID, volResponse.Type)
			err = fmt.Errorf("failed to move the volume type of volume %s to RW", backupActivitiesContext.BackupWorkflowInit.Volume.UUID)
			return nil, ConvertToVSAError(err)
		}
	}

	// Post-provisioning child workflow
	ontapVersion := activities.GetOntapVersionFromPool(backupActivitiesContext.BackupWorkflowInit.Volume.Pool)
	backupActivitiesContext.BackupWorkflowInit.Volume.VolumeAttributes.ExternalUUID = volCreateResponse.ExternalUUID

	if !createVolumeParams.IsExpertModeRestore {
		// Set the junction path before post-provisioning so the volume is mounted
		// in the SVM namespace. PostFileVolumeWorkflowForSMB needs this to create
		// CIFS shares that reference the junction path.
		err = workflow.ExecuteActivity(ctx, volumeUpdateActivity.UpdateVolumeJunctionpath, backupActivitiesContext.BackupWorkflowInit.Volume, backupActivitiesContext.Node).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		var postWorkflowFunc interface{}
		postWorkflowFunc, err = selectVolumeChildWorkflow(backupActivitiesContext.BackupWorkflowInit.Volume.VolumeAttributes.Protocols, PhasePost, ontapVersion)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		var updatedVolume *datamodel.Volume
		err = workflow.ExecuteChildWorkflow(ctx, postWorkflowFunc, backupActivitiesContext.BackupWorkflowInit.Volume, backupActivitiesContext.Node, hostParams, volCreateResponse, isRestoreFromBackup, false).Get(ctx, &updatedVolume)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Update the dbVolume with the changes from the child workflow
		if updatedVolume != nil {
			backupActivitiesContext.BackupWorkflowInit.Volume = updatedVolume
		}
	}

	var ontapAsyncResponse *vsa.OntapAsyncResponse
	err = workflow.ExecuteActivity(ctx, volumeActivity.DeleteRestoreObjectStore, backupActivitiesContext.Node, backupActivitiesContext.ObjStoreName).Get(ctx, &ontapAsyncResponse)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	err = WaitForONTAPJob(ctx, ontapAsyncResponse, backupActivitiesContext.Node, time.Minute*10)
	if err != nil {
		return nil, ConvertToVSAError(fmt.Errorf("failed to delete cloud endpoint: %w", err))
	}

	// Finalise restored volume: update volumes table for VSA-managed restores, or expert_mode_volumes for expert mode restores
	volume := backupActivitiesContext.BackupWorkflowInit.Volume
	if createVolumeParams.IsExpertModeRestore {
		expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
		err = workflow.ExecuteActivity(ctx, expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB, volume.UUID, datamodel.LifeCycleStateAvailable).Get(ctx, nil)
	} else {
		err = workflow.ExecuteActivity(ctx, volumeActivity.FinaliseRestoredVolume, volume).Get(ctx, nil)
	}
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	// No need to defer rollback manager cleanup here, as it will be handled by the workflow engine
	return nil, nil
}

// PollTransferStatusWithContinueAsNew polls transfer status with automatic ContinueAsNew when history limits are reached
// This function is specifically designed for backup restore workflows
func (wf *restoreBackupWorkflow) PollTransferStatusWithContinueAsNew(ctx workflow.Context, backupActivitiesContext *activities.BackupActivitiesContext, params *common.CreateVolumeParams, hostParams []*common.HostParams, volCreateResponse *vsa.VolumeResponse) error {
	return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, RestoreBackupWorkflowWithContext, backupActivitiesContext, params, hostParams, volCreateResponse)
}
