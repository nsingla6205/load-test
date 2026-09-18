package workflows

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
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
	log := workflow.GetLogger(ctx)
	restoreWf := new(restoreBackupWorkflow)
	err := restoreWf.Setup(ctx, params)
	if err != nil {
		log.Error("Failed to setup RestoreBackupWorkflow", "error", err)
		return nil, err
	}
	if err := restoreWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}
	restoreWf.Status = WorkflowStatusRunning
	err = restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Error("Failed to update job status to Processing for RestoreBackupWorkflow", "error", err)
		return nil, err
	}
	var customErr *vsaerrors.CustomError
	_, customErr = restoreWf.Run(ctx, volume, params, backupVault, backup, hostParams, volCreateResponse, backupVaultAccountName)
	if customErr != nil {
		// Check if the error is a ContinueAsNewError - if so, don't call revert
		if workflow.IsContinueAsNewError(customErr.OriginalErr) {
			return nil, customErr
		}
		log.Error("RestoreBackupWorkflow completed with error", "error", customErr.OriginalErr)
		restoreWf.Status = WorkflowStatusFailed
		err2 := restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			log.Error("Failed to update job status to Done with error for RestoreBackupWorkflow", "error", err2)
			return nil, err2
		}
		return nil, customErr
	}
	restoreWf.Status = WorkflowStatusCompleted
	err = restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Error("Failed to update job status to Done for RestoreBackupWorkflow", "error", err)
	}
	return nil, err
}

func RestoreBackupWorkflowWithContext(ctx workflow.Context, backupActivitiesContext *activities.BackupActivitiesContext, params *common.CreateVolumeParams, hostParams []*common.HostParams, volCreateResponse *vsa.VolumeResponse) (gcpgenserver.V1betaDescribeVolumeRes, error) {
	log := workflow.GetLogger(ctx)
	restoreWf := new(restoreBackupWorkflow)
	err := restoreWf.Setup(ctx, params)
	if err != nil {
		log.Error("Failed to setup RestoreBackupWorkflow", "error", err)
		return nil, err
	}
	if err := restoreWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}
	restoreWf.Status = WorkflowStatusRunning
	err = restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Error("Failed to update job status to Processing for RestoreBackupWorkflow", "error", err)
		return nil, err
	}
	var customErr *vsaerrors.CustomError
	_, customErr = restoreWf.RunWithContext(ctx, backupActivitiesContext, params, hostParams, volCreateResponse)
	if customErr != nil {
		// Check if the error is a ContinueAsNewError - if so, don't call revert
		if workflow.IsContinueAsNewError(customErr.OriginalErr) {
			return nil, customErr
		}
		log.Error("RestoreBackupWorkflow completed with error", "error", customErr.OriginalErr)
		restoreWf.Status = WorkflowStatusFailed
		err2 := restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			log.Error("Failed to update job status to Done with error for RestoreBackupWorkflow", "error", err2)
			return nil, err2
		}
		return nil, customErr
	}
	restoreWf.Status = WorkflowStatusCompleted
	err = restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Error("Failed to update job status to Done for RestoreBackupWorkflow", "error", err)
	}
	return nil, err
}

func (wf *restoreBackupWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createVolParams := input.(*common.CreateVolumeParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createVolParams.AccountName
	wf.Status = WorkflowStatusCreated
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
	log := workflow.GetLogger(ctx)
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
				log.Error("Failed to revert backup restore count", "error", decrementErr)
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
		log.Info("Resuming backup workflow from continuation",
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
			log.Error("Failed to update backup restore count", "error", incrementErr)
			return nil, ConvertToVSAError(incrementErr)
		}

		// Execute VPC pool restoration activity to handle cross-project permissions
		err = workflow.ExecuteActivity(ctx, volumeActivity.CrossPoolOrVPCRestorationActivity, backupActivitiesContext.BackupWorkflowInit.Volume.Pool, backupActivitiesContext.BackupWorkflowInit.Backup).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		var node *models.Node
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.ResolveOntapNodeForPool,
			backupActivitiesContext.BackupWorkflowInit.Volume.Pool).Get(ctx, &node)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
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
		log.Debug("Snapmirror destination path", "smDestinationPath", smDestinationPath)
		log.Debug("Snapmirror source path", "smSourcePath", smSourcePath)

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
		if backupActivitiesContext.BackupWorkflowInit.Volume.Pool.IsDedicatedCapacity() {
			err = workflow.ExecuteActivity(ctx, volumeActivity.PopulateBucketDetailsForTemporaryRestoreActivity,
				bucketDetails,
				backupActivitiesContext.BackupWorkflowInit.Backup,
				backupActivitiesContext.BackupWorkflowInit.BackupVault,
				createVolumeParams.BackupPath,
			).Get(ctx, &bucketDetails)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		}
		bucketName := bucketDetails.BucketName

		err = workflow.Sleep(ctx, 60*time.Second)
		if err != nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to sleep before starting snapmirror restore: %w", err))
		}
		temporaryHmac, err := provisionTemporaryRestoreHmacIfNeeded(ctx, cleanupManager,
			backupActivitiesContext.BackupWorkflowInit.Volume.Pool, bucketDetails, backupActivitiesContext.TemporaryRestoreHmac)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		backupActivitiesContext.TemporaryRestoreHmac = temporaryHmac
		err = workflow.ExecuteActivity(ctx, activities.BackupActivity.GetOrCreateObjectStoreForRestore,
			node, objStoreName, bucketName, bucketDetails, backupActivitiesContext.BackupWorkflowInit.Volume.Pool, temporaryHmac).Get(ctx, &objStore)
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
			log.Info("Restore snapmirror relationship not found after transfer completion")
		} else {
			return nil, customErr
		}
	}

	if smRelationship != nil && smRelationship.Healthy != nil && !*smRelationship.Healthy {
		if smRelationship.UnhealthyReason != nil && len(*smRelationship.UnhealthyReason) > 0 {
			log.Info("Snapmirror relationship is unhealthy", "reasons", *smRelationship.UnhealthyReason)
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
			log.Error("Volume failed to transition from DP/LS to RW state within timeout period",
				"volumeUUID", backupActivitiesContext.BackupWorkflowInit.Volume.UUID, "timeout", restoredVolumeDPToRWTimeout)
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
			log.Debug("Get volume from ONTAP failed", "error", err)
			return nil, ConvertToVSAError(err)
		}
		if volResponse.Type == VolumeStateRW {
			log.Debug("Volume is available as RW in ONTAP", "volumeUUID", backupActivitiesContext.BackupWorkflowInit.Volume.UUID)
			volumeTypeUpdateDone = true
		} else if volResponse.Type == VolumeStateDP || volResponse.Type == VolumeStateLS {
			log.Debug("Volume is still DP/LS and not available as RW in ONTAP", "volumeUUID", backupActivitiesContext.BackupWorkflowInit.Volume.UUID)
			err = workflow.Sleep(ctx, WaitForRestore) // Wait before polling again
			if err != nil {
				return nil, ConvertToVSAError(fmt.Errorf("failed to sleep during volume availability polling: %w", err))
			}
		} else {
			log.Debug("Volume type is not correct", "volumeUUID", backupActivitiesContext.BackupWorkflowInit.Volume.UUID, "ontapState", volResponse.Type)
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

func provisionTemporaryRestoreHmacIfNeeded(
	ctx workflow.Context,
	cleanupManager *common.RollbackManager,
	pool *datamodel.Pool,
	bucketDetails *datamodel.BucketDetails,
	existing *common.TemporaryRestoreHmacHandle,
) (*common.TemporaryRestoreHmacHandle, error) {
	if existing != nil {
		return existing, nil
	}
	if !activities.RequiresTemporaryRestoreHmac(pool, bucketDetails) {
		return nil, nil
	}
	if bucketDetails == nil || bucketDetails.TenantProjectNumber == "" || bucketDetails.BucketName == "" {
		return nil, ConvertToVSAError(utilerrors.NewUserInputValidationErr(
			"backup tenant project and bucket name required for cross-pool HW restore"))
	}

	log := workflow.GetLogger(ctx)
	adcActivity := &activities.ADCActivity{}
	backupActivity := &activities.BackupActivity{}
	tenantProject := bucketDetails.TenantProjectNumber
	serviceAccountEmail := strings.TrimSpace(bucketDetails.ServiceAccountName)

	saCtx, err := withServiceAccountActivityOptions(ctx)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	var serviceAccountID string
	if serviceAccountEmail == "" {
		if err = workflow.ExecuteActivity(saCtx, backupActivity.TemporaryRestoreServiceAccountID, bucketDetails).
			Get(saCtx, &serviceAccountID); err != nil {
			return nil, ConvertToVSAError(err)
		}

		var serviceAccount *hyperscalermodels.ServiceAccount
		if err = workflow.ExecuteActivity(saCtx, adcActivity.CreateServiceAccount, tenantProject, serviceAccountID,
			activities.TemporaryRestoreServiceAccountDisplay).Get(saCtx, &serviceAccount); err != nil {
			log.Error("Failed to create temporary restore service account", "error", err)
			return nil, ConvertToVSAError(err)
		}
		cleanupManager.AddActivity(adcActivity.DeleteSA, tenantProject, serviceAccountID)
		serviceAccountEmail = serviceAccount.Email

		var isCreated bool
		if err = workflow.ExecuteActivity(saCtx, adcActivity.IsServiceAccountCreated, serviceAccountEmail).
			Get(saCtx, &isCreated); err != nil {
			return nil, ConvertToVSAError(err)
		}
		if !isCreated {
			return nil, ConvertToVSAError(fmt.Errorf("temporary restore service account %s is not created", serviceAccountEmail))
		}

		if err = workflow.ExecuteActivity(saCtx, adcActivity.AttachRolesToServiceAccount, tenantProject, serviceAccountEmail,
			activities.TemporaryRestoreServiceAccountRoles).Get(saCtx, nil); err != nil {
			log.Error("Failed to attach roles to temporary restore service account", "error", err)
			return nil, ConvertToVSAError(err)
		}
		cleanupManager.AddActivity(adcActivity.RemoveRolesFromServiceAccount, tenantProject, serviceAccountID,
			activities.TemporaryRestoreServiceAccountRoles)

		if err = workflow.Sleep(ctx, activities.TemporaryRestoreIAMPropagationWait); err != nil {
			return nil, ConvertToVSAError(fmt.Errorf("sleep interrupted before temporary restore HMAC creation: %w", err))
		}
	} else {
		var isCreated bool
		if err = workflow.ExecuteActivity(saCtx, adcActivity.IsServiceAccountCreated, serviceAccountEmail).
			Get(saCtx, &isCreated); err != nil {
			return nil, ConvertToVSAError(err)
		}
		if !isCreated {
			return nil, ConvertToVSAError(fmt.Errorf("backup vault service account %s not found", serviceAccountEmail))
		}
	}

	var encodedHmacKeys *common.HmacKeys
	if err = workflow.ExecuteActivity(saCtx, adcActivity.CreateHmacKeys, &common.HmacKeyCreateParams{
		ServiceAccount: serviceAccountEmail,
		ProjectNumber:  tenantProject,
	}).Get(saCtx, &encodedHmacKeys); err != nil {
		log.Error("Failed to create temporary restore HMAC keys", "error", err)
		return nil, ConvertToVSAError(err)
	}

	accessKeyIDBytes, err := base64.StdEncoding.DecodeString(encodedHmacKeys.AccessKey)
	if err != nil {
		return nil, ConvertToVSAError(fmt.Errorf("failed to decode temporary restore access key: %w", err))
	}

	handle := &common.TemporaryRestoreHmacHandle{
		AccessKeyID:         string(accessKeyIDBytes),
		EncodedAccessKey:    encodedHmacKeys.AccessKey,
		EncodedSecretKey:    encodedHmacKeys.SecretKey,
		ServiceAccount:      serviceAccountEmail,
		TenantProjectNumber: tenantProject,
		ServiceAccountID:    serviceAccountID,
	}
	cleanupManager.AddActivity(backupActivity.DeleteTemporaryRestoreHmacKey, handle)
	return handle, nil
}

// executeTemporaryRestoreCleanup deletes temporary restore HMAC keys and any short-lived service account.
// Used on success paths that do not run a rollback manager (for example SFR).
func executeTemporaryRestoreCleanup(ctx workflow.Context, handle *common.TemporaryRestoreHmacHandle) *vsaerrors.CustomError {
	if handle == nil {
		return nil
	}
	backupActivity := &activities.BackupActivity{}
	adcActivity := &activities.ADCActivity{}
	if err := workflow.ExecuteActivity(ctx, backupActivity.DeleteTemporaryRestoreHmacKey, handle).Get(ctx, nil); err != nil {
		return ConvertToVSAError(err)
	}
	if handle.ServiceAccountID == "" {
		return nil
	}
	if err := workflow.ExecuteActivity(ctx, adcActivity.RemoveRolesFromServiceAccount, handle.TenantProjectNumber,
		handle.ServiceAccountID, activities.TemporaryRestoreServiceAccountRoles).Get(ctx, nil); err != nil {
		return ConvertToVSAError(err)
	}
	if err := workflow.ExecuteActivity(ctx, adcActivity.DeleteSA, handle.TenantProjectNumber, handle.ServiceAccountID).Get(ctx, nil); err != nil {
		return ConvertToVSAError(err)
	}
	return nil
}

// PollTransferStatusWithContinueAsNew polls transfer status with automatic ContinueAsNew when history limits are reached
// This function is specifically designed for backup restore workflows
func (wf *restoreBackupWorkflow) PollTransferStatusWithContinueAsNew(ctx workflow.Context, backupActivitiesContext *activities.BackupActivitiesContext, params *common.CreateVolumeParams, hostParams []*common.HostParams, volCreateResponse *vsa.VolumeResponse) error {
	return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, RestoreBackupWorkflowWithContext, backupActivitiesContext, params, hostParams, volCreateResponse)
}
