package workflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type BackupCreateWorkflow struct {
	BaseWorkflow
	SE *database.Storage
}

type BackupDeleteWorkflow struct {
	BaseWorkflow
	SE              *database.Storage
	deleteInitiated bool
}

type backupUpdateWorkflow struct {
	BaseWorkflow
	SE database.Storage
}

var (
	_                                       WorkflowInterface = &backupUpdateWorkflow{}
	_                                       WorkflowInterface = &BackupCreateWorkflow{}
	_                                       WorkflowInterface = &BackupDeleteWorkflow{}
	Wait                                                      = time.Duration(env.GetUint("ONTAP_REST_ASYNC_POLL_WAIT_SECONDS", 3)) * time.Second
	createBackupWorkflowHeartbeatTimeoutSec                   = env.GetUint64("CREATE_BACKUP_WORKFLOW_HEARTBEAT_TIMEOUT_SEC", 600)
	deleteBackupWorkflowHeartbeatTimeoutSec                   = env.GetUint64("DELETE_BACKUP_WORKFLOW_HEARTBEAT_TIMEOUT_SEC", 600)
	updateBackupWorkflowHeartbeatTimeoutSec                   = env.GetUint64("UPDATE_BACKUP_WORKFLOW_HEARTBEAT_TIMEOUT_SEC", 600)
)

const (
	adcWorkflowTimeout = 7 * 24 * time.Hour // 7 days timeout to accommodate 6-day max sleep + buffer
)

// CreateBackupWorkflow  process backup related requests from a customer.
func CreateBackupWorkflow(ctx workflow.Context, params *commonparams.CreateBackupParams, backup *datamodel.Backup, backupVault *datamodel.BackupVault, volume *datamodel.Volume) (interface{}, error) {
	logger := workflow.GetLogger(ctx)
	backupWf := new(BackupCreateWorkflow)
	err := backupWf.Setup(ctx, params)
	if err != nil {
		return nil, err
	}
	if err := backupWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}
	backupWf.Status = WorkflowStatusRunning
	err = backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, err
	}
	_, customErr := backupWf.Run(ctx, backup, backupVault, volume, params)

	if customErr != nil {
		if workflow.IsContinueAsNewError(customErr.OriginalErr) {
			return nil, customErr
		}
		err2 := backupWf.Revert(ctx, backup, volume, customErr.OriginalErr.Error())
		if err2 != nil {
			logger.Error("Failed to execute rollback for workflow", "workflowID", backupWf.ID, "error", err2)
		}
		backupWf.Status = WorkflowStatusFailed
		err2 = backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			logger.Error("Failed to update job status for workflow", "workflowID", backupWf.ID, "error", err2)
		}
		return nil, customErr
	}
	backupWf.Status = WorkflowStatusCompleted
	err2 := backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		logger.Error("Failed to update job status for workflow", "workflowID", backupWf.ID, "error", err2)
		return nil, ConvertToVSAError(err2)
	}
	return nil, nil
}

// CreateBackupWorkflowWithContext process backup with context for continuation
func CreateBackupWorkflowWithContext(ctx workflow.Context, backupActivitiesContext *activities.BackupActivitiesContext, params *commonparams.CreateBackupParams) (interface{}, error) {
	logger := workflow.GetLogger(ctx)
	backupWf := new(BackupCreateWorkflow)

	// Setup workflow
	if err := backupWf.Setup(ctx, params); err != nil {
		return nil, err
	}

	backupWf.Status = WorkflowStatusRunning
	err := backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, err
	}

	_, customErr := backupWf.RunBackupCreateWithContext(ctx, backupActivitiesContext, params)

	if customErr != nil {
		// Check if the error is a ContinueAsNewError - if so, don't call revert
		if workflow.IsContinueAsNewError(customErr.OriginalErr) {
			return nil, customErr
		}

		err2 := backupWf.Revert(ctx, backupActivitiesContext.BackupWorkflowInit.Backup, backupActivitiesContext.BackupWorkflowInit.Volume, customErr.OriginalErr.Error())
		if err2 != nil {
			logger.Error("Failed to execute rollback for workflow", "workflowID", backupWf.ID, "error", err2)
		}
		backupWf.Status = WorkflowStatusFailed
		err2 = backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			logger.Error("Failed to update job status for workflow", "workflowID", backupWf.ID, "error", err2)
		}
		return nil, customErr
	}
	backupWf.Status = WorkflowStatusCompleted
	err2 := backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		logger.Error("Failed to update job status for workflow", "workflowID", backupWf.ID, "error", err2)
		return nil, ConvertToVSAError(err2)
	}
	return nil, nil
}

func (wf *BackupCreateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createBackupParams := input.(*commonparams.CreateBackupParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createBackupParams.AccountName
	wf.Status = WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *BackupCreateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	// Initialize backupActivitiesContext with input arguments
	backupActivitiesContext := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      args[0].(*datamodel.Backup),
			BackupVault: args[1].(*datamodel.BackupVault),
			Volume:      args[2].(*datamodel.Volume),
		},
	}

	params := args[3].(*commonparams.CreateBackupParams)
	return wf.RunBackupCreateWithContext(ctx, backupActivitiesContext, params)
}

func (wf *BackupCreateWorkflow) RunBackupCreateWithContext(ctx workflow.Context, backupActivitiesContext *activities.BackupActivitiesContext, params *commonparams.CreateBackupParams) (_ interface{}, customErr *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	backupActivity := &activities.BackupActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    time.Duration(createBackupWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rollbackManager := commonparams.NewRollbackManager()
	defer func() {
		// ContinueAsNew is not a failure: the backup continues in the next run.
		if customErr != nil && !workflow.IsContinueAsNewError(customErr.OriginalErr) {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, customErr)
		}
	}()

	// Check if this is a continuation workflow
	info := workflow.GetInfo(ctx)
	isContinuation := info.ContinuedExecutionRunID != ""

	if isContinuation {
		workflow.GetLogger(ctx).Info("Resuming backup workflow from continuation",
			"workflowID", wf.ID,
			"continuedFromRunID", info.OriginalRunID,
			"snapshotName", backupActivitiesContext.SnapshotName,
			"transferStatus", backupActivitiesContext.TransferStatus)
		registerExpertModeSnapmirrorRollback(rollbackManager, backupActivity, backupActivitiesContext)
		registerExpertModeSnapshotRollback(rollbackManager, backupActivity, backupActivitiesContext)
	} else {
		var node *models.Node
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.ResolveOntapNodeForPool, backupActivitiesContext.BackupWorkflowInit.Volume.Pool).Get(ctx, &node)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		backupActivitiesContext.Node = node
		backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SourceVolumeZone = backupActivitiesContext.BackupWorkflowInit.Volume.Pool.PoolAttributes.PrimaryZone
		backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.IsRegionalHA = backupActivitiesContext.BackupWorkflowInit.Volume.Pool.PoolAttributes.IsRegionalHA

		// Check if backup vault is attached to volume, if not create bucket and attach it
		// Only execute for expert mode volumes
		workflow.GetLogger(ctx).Info("IsExpertModeVolumeVolume:", "isExpertModeVolume", params.IsExpertModeVolume)
		if params.IsExpertModeVolume {
			// Initialize DataProtection if it's nil
			if backupActivitiesContext.BackupWorkflowInit.Volume.DataProtection == nil {
				backupActivitiesContext.BackupWorkflowInit.Volume.DataProtection = &datamodel.DataProtection{}
			}
			err = workflow.ExecuteActivity(ctx, backupActivity.CheckAndAttachBackupVaultToVolume, backupActivitiesContext, params.LocationID).Get(ctx, &backupActivitiesContext)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			if backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotID != "" {
				err = workflow.ExecuteActivity(ctx, backupActivity.GetSnapshotNameByUUIDActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
				if err != nil {
					return nil, ConvertToVSAError(err)
				}
			}

			// Get volumes from provider and fetch constituent count
			err = workflow.ExecuteActivity(ctx, backupActivity.GetVolumesAndConstituentCountActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Get volume protocols from ONTAP for expert mode volumes
			err = workflow.ExecuteActivity(ctx, backupActivity.GetVolumeProtocolsFromOntapActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		}
		backupActivitiesContext.IsExpertMode = params.IsExpertModeVolume

		// Prepare object store details
		err = workflow.ExecuteActivity(ctx, backupActivity.PrepareObjectStoreActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Get or create object store
		err = workflow.ExecuteActivity(ctx, backupActivity.GetOrCreateObjectStoreActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Prepare snapmirror paths
		err = workflow.ExecuteActivity(ctx, backupActivity.PrepareSnapmirrorActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Create snapmirror relationship
		err = workflow.ExecuteActivity(ctx, backupActivity.CreateSnapmirrorRelationshipActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// In ONTAP mode the customer owns volume delete, and ONTAP refuses to delete a volume that
		// still has a backup relationship, so a backup that never completes must not leave one behind.
		registerExpertModeSnapmirrorRollback(rollbackManager, backupActivity, backupActivitiesContext)

		// Creating snapshot in DB
		err = workflow.ExecuteActivity(ctx, backupActivity.CreatingSnapshotActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		defer func() {
			// Update snapshot details in DB via a disconnected context so it completes
			// even when the parent context is cancelled or timed out.
			if !backupActivitiesContext.IsExpertMode {
				disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
				if updateErr := workflow.ExecuteActivity(disconnectedCtx, backupActivity.UpdateSnapshotActivity, backupActivitiesContext).Get(disconnectedCtx, &backupActivitiesContext); updateErr != nil {
					logger.Error("Failed to update snapshot state", "error", updateErr)
				}
			}
		}()

		// Create snapshot
		err = workflow.ExecuteActivity(ctx, backupActivity.CreateSnapshotActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Expert mode only: GCNV keeps the snapshot in the database too, and UpdateSnapshotActivity
		// marks that row READY, so deleting it here would leave a snapshot the customer can see
		// but ONTAP no longer has. DeleteSnapshotForBackup skips customer-supplied snapshots.
		registerExpertModeSnapshotRollback(rollbackManager, backupActivity, backupActivitiesContext)

		// Transfer snapshot
		err = workflow.ExecuteActivity(ctx, backupActivity.TransferSnapshotActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	// Poll for transfer completion with event history monitoring and ContinueAsNew capability
	err = wf.PollTransferStatusWithContinueAsNew(ctx, backupActivitiesContext, params)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Wait for 30 seconds before proceeding
	err = workflow.Sleep(ctx, 30*time.Second)
	if err != nil {
		return nil, ConvertToVSAError(fmt.Errorf("failed to sleep before getting the snapmirror: %w", err))
	}

	// Get snapmirror relationship to check health status
	var smRelationship *commonparams.SnapmirrorRelationship
	err = workflow.ExecuteActivity(ctx, backupActivity.GetSnapmirror, backupActivitiesContext.Node, backupActivitiesContext.SmSourcePath, backupActivitiesContext.SmDestinationPath).Get(ctx, &smRelationship)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if smRelationship.State != nil && *smRelationship.State != datamodel.OntapSnapmirrored {
		unhealthyMsg := ""
		if smRelationship.Healthy != nil && !*smRelationship.Healthy && smRelationship.UnhealthyReason != nil && len(*smRelationship.UnhealthyReason) > 0 {
			unhealthyMsg = fmt.Sprintf(" Unhealthy reasons: %v", *smRelationship.UnhealthyReason)
		}
		workflow.GetLogger(ctx).Info(fmt.Sprintf("Snapmirror relationship state is %s, expected %s.%s", *smRelationship.State, datamodel.OntapSnapmirrored, unhealthyMsg))
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, vsaerrors.New("snapmirror relationship state is not snapmirrored"))
	}

	if smRelationship.Healthy != nil && !*smRelationship.Healthy {
		if smRelationship.UnhealthyReason != nil && len(*smRelationship.UnhealthyReason) > 0 {
			workflow.GetLogger(ctx).Info(fmt.Sprintf("Snapmirror relationship is unhealthy. Reasons: %v", *smRelationship.UnhealthyReason))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, vsaerrors.New("snapmirror relationship is unhealthy"))
	}

	// Update ConstituentCount for a backup from Volume
	if backupActivitiesContext.BackupWorkflowInit.Volume.LargeVolumeAttributes != nil && backupActivitiesContext.BackupWorkflowInit.Volume.LargeVolumeAttributes.LargeCapacity {
		err = workflow.ExecuteActivity(ctx, backupActivity.UpdateConstituentCountForBackup, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	if smRelationship.TotalTransferBytes != nil && *smRelationship.TotalTransferBytes > 0 {
		backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.TotalTransferBytes = *smRelationship.TotalTransferBytes
	}

	backupActivitiesContext.BackupWorkflowInit.Backup.AssetMetadata = &datamodel.AssetMetadata{
		ChildAssets: []datamodel.ChildAsset{
			{
				AssetType:  commonparams.BackupAssetType,
				AssetNames: []string{fmt.Sprintf("//storage.googleapis.com/%s", backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.BucketName)},
			},
		},
	}
	// Finish backup now as the transfer has completed
	err = workflow.ExecuteActivity(ctx, backupActivity.FinishBackupActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// No need to check for error as if there is an error it will be caught in the above step itself
	// After this step the backup is considered successful
	// Get snapshot from object store
	err = workflow.ExecuteActivity(ctx, backupActivity.GetObjectStoreSnapshotActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
	if err != nil {
		// Log the error but don't fail the entire backup workflow
		workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to get snapshot from object store for volume %s: %v", backupActivitiesContext.BackupWorkflowInit.Volume.Name, err))
	}
	// Get object store endpoint info
	err = workflow.ExecuteActivity(ctx, backupActivity.GetObjectStoreEndpointActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
	if err != nil {
		// Log the error but don't fail the entire backup workflow
		workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to get object store endpoint info for volume %s: %v", backupActivitiesContext.BackupWorkflowInit.Volume.Name, err))
	}

	err = workflow.ExecuteActivity(ctx, backupActivity.UpdateBackupSizeActivity, backupActivitiesContext).Get(ctx, &backupActivitiesContext)
	if err != nil {
		// Log the error but don't fail the entire backup workflow
		workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to update backup size fields for volume %s: %v", backupActivitiesContext.BackupWorkflowInit.Volume.Name, err))
	}

	// Create remote backup from VCP if this is a cross-region backup
	// This is done after UpdateBackupSizeActivity to ensure ExternalUUID is set
	err = workflow.ExecuteActivity(ctx, backupActivity.CreateRemoteBackupFromVCPActivity, backupActivitiesContext).Get(ctx, nil)
	if err != nil {
		// Log the error but don't fail the entire backup workflow
		workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to create remote backup from VCP for backup %s: %v", backupActivitiesContext.BackupWorkflowInit.Backup.UUID, err))
	}

	// Cleanup older adhoc-backup snapshots for this volume
	if !backupActivitiesContext.IsExpertMode {
		err = workflow.ExecuteActivity(ctx, backupActivity.CleanupOldBackupSnapshotsActivity, backupActivitiesContext.BackupWorkflowInit.Volume, backupActivitiesContext.Node).Get(ctx, nil)
		if err != nil {
			// Log the error but don't fail the entire backup workflow
			workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to cleanup older backup snapshots for volume %s: %v", backupActivitiesContext.BackupWorkflowInit.Volume.Name, err))
		}
	} else {
		// cleanup expertmode backup snapshot from ontap, no snapshot metadata/entry in DB for expert mode backups snapshots
		err = workflow.ExecuteActivity(ctx, backupActivity.CleanupOldExpertModeSnapshotActivity, backupActivitiesContext.BackupWorkflowInit.Volume, backupActivitiesContext.Node).Get(ctx, nil)
		if err != nil {
			// Log the error but don't fail the entire backup workflow
			workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to cleanup older expert mode backup snapshots for volume %s: %v", backupActivitiesContext.BackupWorkflowInit.Volume.Name, err))
		}
	}

	// Hydrate snapshot to CCFE if not expert mode
	if backupActivitiesContext.DbSnapshot != nil &&
		backupActivitiesContext.BackupWorkflowInit.Volume != nil &&
		backupActivitiesContext.BackupWorkflowInit.BackupVault != nil &&
		backupActivitiesContext.BackupWorkflowInit.Volume.Account != nil && !backupActivitiesContext.IsExpertMode {
		location := utils.GetLocation(*backupActivitiesContext.DbSnapshot)
		err = workflow.ExecuteActivity(ctx, backupActivity.HydrateSnapshotToCCFEActivity,
			backupActivitiesContext.DbSnapshot,
			backupActivitiesContext.BackupWorkflowInit.Volume.Name,
			location,
			backupActivitiesContext.BackupWorkflowInit.Volume.Account.Name).Get(ctx, nil)
		if err != nil {
			// Log the error but don't fail the entire backup workflow
			workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to hydrate snapshot to CCFE for volume %s: %v", backupActivitiesContext.BackupWorkflowInit.Volume.Name, err))
		}
	}

	// Create BackupMetadata entry if this is the first backup for the volume
	err = workflow.ExecuteActivity(ctx, backupActivity.CreateBackupMetadataIfFirstBackupActivity, backupActivitiesContext.BackupWorkflowInit.Volume, backupActivitiesContext.IsExpertMode).Get(ctx, nil)
	if err != nil {
		// Log the error but don't fail the entire backup workflow
		workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to create BackupMetadata for volume %s: %v", backupActivitiesContext.BackupWorkflowInit.Volume.UUID, err))
	}

	return backupActivitiesContext, nil
}

func (wf *BackupCreateWorkflow) Revert(ctx workflow.Context, backup *datamodel.Backup, volume *datamodel.Volume, errString string) error {
	backupActivity := &activities.BackupActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return err
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
	disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
	disconnectedCtx = workflow.WithActivityOptions(disconnectedCtx, ao)
	err = workflow.ExecuteActivity(disconnectedCtx, backupActivity.UpdateBackupError, &backup, errString).Get(disconnectedCtx, nil)
	if err != nil {
		return err
	}
	return nil
}

// PollTransferStatusWithContinueAsNew polls transfer status with automatic ContinueAsNew when history limits are reached
func (wf *BackupCreateWorkflow) PollTransferStatusWithContinueAsNew(ctx workflow.Context, backupActivitiesContext *activities.BackupActivitiesContext, params *commonparams.CreateBackupParams) error {
	return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, CreateBackupWorkflowWithContext, backupActivitiesContext, params)
}

func registerExpertModeSnapmirrorRollback(rollbackManager *commonparams.RollbackManager, backupActivity *activities.BackupActivity, ctx *activities.BackupActivitiesContext) {
	if ctx == nil || !ctx.IsExpertMode || ctx.SnapmirrorRelationship == nil ||
		ctx.BackupWorkflowInit == nil || ctx.BackupWorkflowInit.Backup == nil {
		return
	}
	backup := ctx.BackupWorkflowInit.Backup
	rollbackManager.AddActivity(backupActivity.DeleteSnapmirrorIfUnused, ctx.Node, backup.VolumeUUID,
		ctx.SnapmirrorRelationship.EndpointUUID(), []string{backup.UUID}, ctx.SnapmirrorRelationship.UUID)
}

func registerExpertModeSnapshotRollback(rollbackManager *commonparams.RollbackManager, backupActivity *activities.BackupActivity, ctx *activities.BackupActivitiesContext) {
	if ctx == nil || !ctx.IsExpertMode || ctx.BackupWorkflowInit == nil ||
		ctx.BackupWorkflowInit.Backup == nil || ctx.BackupWorkflowInit.Backup.Attributes == nil ||
		ctx.BackupWorkflowInit.Volume == nil || ctx.BackupWorkflowInit.Volume.VolumeAttributes == nil {
		return
	}
	attributes := ctx.BackupWorkflowInit.Backup.Attributes
	rollbackManager.AddActivity(backupActivity.DeleteSnapshotForBackup, ctx.Node, attributes.SnapshotID,
		ctx.BackupWorkflowInit.Volume.VolumeAttributes.ExternalUUID, attributes.UseExistingSnapshot)
}

func getBucketDetailsForBucket(backupVault *datamodel.BackupVault, bucketName string) (*datamodel.BucketDetails, error) {
	for _, bucketDetail := range backupVault.BucketDetails {
		if bucketDetail.BucketName == bucketName {
			return bucketDetail, nil
		}
	}
	return nil, ConvertToVSAError(fmt.Errorf("no matching bucket details found for bucket %s in backup vault %s", bucketName, backupVault.Name))
}

func backupDeleteEndpointMismatch(rel *commonparams.SnapmirrorRelationship, dbBackup *datamodel.Backup) bool {
	return !commonparams.BackupEndpointMatches(rel.EndpointUUID(), commonparams.BackupEndpointUUID(dbBackup))
}

func backupAttributesTrimmedEndpointUUID(dbBackup *datamodel.Backup) string {
	return commonparams.BackupEndpointUUID(dbBackup)
}

func DeleteBackupWorkflow(ctx workflow.Context, params *commonparams.DeleteBackupParams) (interface{}, error) {
	logger := workflow.GetLogger(ctx)
	backupWf := new(BackupDeleteWorkflow)
	err := backupWf.Setup(ctx, params)
	if err != nil {
		return nil, err
	}
	if err := backupWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}
	backupWf.Status = WorkflowStatusRunning
	err = backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, err
	}
	_, customErr := backupWf.Run(ctx, params)

	if customErr != nil {
		// backup backupActivitiesContext to error
		err2 := backupWf.HandleError(ctx, params, customErr.OriginalErr.Error())
		if err2 != nil {
			// If revert fails, log the error but do not return it
			logger.Error("Failed to revert backup delete workflow", "error", err2)
		}
		backupWf.Status = WorkflowStatusFailed
		err2 = backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			logger.Error("Failed to update job status for workflow", "workflowID", backupWf.ID, "error", err2)
		}
		return nil, customErr
	}
	backupWf.Status = WorkflowStatusCompleted
	err2 := backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		logger.Error("Failed to update job status for workflow", "workflowID", backupWf.ID, "error", err2)
		return nil, ConvertToVSAError(err2)
	}
	return nil, nil
}

func (wf *BackupDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	deleteBackupParams := input.(*commonparams.DeleteBackupParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = deleteBackupParams.AccountName
	wf.Status = WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *BackupDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	deleteBackupParams := args[0].(*commonparams.DeleteBackupParams)
	backupActivity := &activities.BackupActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    time.Duration(deleteBackupWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// fetch account by name
	account := &datamodel.Account{}
	err = workflow.ExecuteActivity(ctx, backupActivity.GetAccountByName, deleteBackupParams.AccountName).Get(ctx, account)
	if err != nil {
		return nil, ConvertToVSAError(fmt.Errorf("failed to get account by name %s: %w", deleteBackupParams.AccountName, err))
	}

	// check if backup Vault is present in VSA
	dbBackupVault := &datamodel.BackupVault{}
	err = workflow.ExecuteActivity(ctx, backupActivity.GetBackupVault, deleteBackupParams.BackupVaultUUID).Get(ctx, &dbBackupVault)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	dbBackupVault.Account = account

	dbBackup := &datamodel.Backup{}
	err = workflow.ExecuteActivity(ctx, backupActivity.GetBackup, deleteBackupParams.BackupVaultUUID, deleteBackupParams.BackupUUID, deleteBackupParams.AccountName).Get(ctx, &dbBackup)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	var isVolumeDeleted bool
	err = workflow.ExecuteActivity(ctx, backupActivity.IsVolumeDeleted, dbBackup.VolumeUUID).Get(ctx, &isVolumeDeleted)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	bucketDetails, err := getBucketDetailsForBucket(dbBackupVault, dbBackup.Attributes.BucketName)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	var isExpertModeVolume bool
	err = workflow.ExecuteActivity(ctx, backupActivity.IsExpertModeVolume, dbBackup.VolumeUUID).Get(ctx, &isExpertModeVolume)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	var volume *datamodel.Volume
	var node *models.Node
	var smSourcePath string
	var smDestinationPath string
	var smPrecheck *commonparams.SnapmirrorDeletePrecheckResult
	if !isVolumeDeleted {
		err = workflow.ExecuteActivity(ctx, backupActivity.GetVolume, dbBackup.VolumeUUID).Get(ctx, &volume)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.ResolveOntapNodeForPool, volume.Pool).Get(ctx, &node)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, backupActivity.GetSmSourcePathActivity, volume).Get(ctx, &smSourcePath)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, backupActivity.GetSmDestinationPathActivity, dbBackupVault, volume).Get(ctx, &smDestinationPath)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		params := &commonparams.SnapmirrorRelationshipParams{
			SourcePath:      smSourcePath,
			DestinationPath: smDestinationPath,
		}

		err = workflow.ExecuteActivity(ctx, backupActivity.IsSnapmirrorDeleted, node, params).Get(ctx, &smPrecheck)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	// ADC unless we can use the direct ONTAP delete path: volume exists, relationship found, endpoint matches backup row.
	directOntapDelete := !isVolumeDeleted && smPrecheck != nil &&
		!smPrecheck.RelationshipMissing &&
		!backupDeleteEndpointMismatch(smPrecheck.Relationship, dbBackup)
	useADC := !directOntapDelete

	// When vault switching is enabled, block deletion of the latest backup if there
	// is an active snapmirror relationship whose endpoint matches (i.e. the vault is
	// still attached).
	if directOntapDelete && utils.EnableBackupVaultSwitching {
		endpointUUID := backupAttributesTrimmedEndpointUUID(dbBackup)
		var isLatest bool
		err = workflow.ExecuteActivity(ctx, backupActivity.IsLatestBackupInVaultAndInEndpointActivity,
			dbBackup.UUID, dbBackup.VolumeUUID, dbBackup.BackupVaultID, endpointUUID).Get(ctx, &isLatest)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		if isLatest {
			var count int64
			err = workflow.ExecuteActivity(ctx, backupActivity.GetBackupCountByVolumeVaultAndEndpoint,
				dbBackup.VolumeUUID, dbBackup.BackupVaultID, endpointUUID).Get(ctx, &count)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
			if count > 1 {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrCannotDeleteLatestBackup, customerrors.NewUserInputValidationErr("Cannot delete latest backup"))
			}
		}
	}

	if useADC {
		cloudDeletionIntiated := false
		// Create a new context for ADCWorkflow with extended timeout to accommodate 6-day maximum sleep duration
		adcWorkflowCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowExecutionTimeout: adcWorkflowTimeout, // 7 days timeout
		})

		// if volume is deleted then we need to delete the backup with adc
		err = workflow.ExecuteChildWorkflow(adcWorkflowCtx, ADCWorkflow, deleteBackupParams, dbBackupVault, dbBackup, account).Get(adcWorkflowCtx, &cloudDeletionIntiated)
		if err != nil {
			workflow.GetLogger(ctx).Error(fmt.Sprintf("Backup deletion failed with ADC, backupUUID: %s, error: %v", dbBackup.UUID, err))
			wf.deleteInitiated = true
			return nil, ConvertToVSAError(err)
		}
		// Preserve ADC child-workflow outcome so HandleError can set backup state correctly on later failures.
		if cloudDeletionIntiated {
			wf.deleteInitiated = true
		}
	} else {
		var backupCount int64
		if utils.EnableBackupVaultSwitching {
			err = workflow.ExecuteActivity(ctx, backupActivity.GetBackupCountByVolumeVaultAndEndpoint, dbBackup.VolumeUUID, dbBackup.BackupVaultID, backupAttributesTrimmedEndpointUUID(dbBackup)).Get(ctx, &backupCount)
		} else {
			err = workflow.ExecuteActivity(ctx, backupActivity.GetBackupCountByVolumeUUID, dbBackup.VolumeUUID).Get(ctx, &backupCount)
		}
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		var objStore *commonparams.CloudTarget
		err = workflow.ExecuteActivity(ctx, backupActivity.GetObjectStore, node, bucketDetails.BucketName).Get(ctx, &objStore)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		var ontapAsyncResponse *vsa.OntapAsyncResponse
		if backupCount == 1 {
			var snapmirrorRelationship *commonparams.SnapmirrorRelationship
			err = workflow.ExecuteActivity(ctx, backupActivity.GetSnapmirror, node, smSourcePath, smDestinationPath).Get(ctx, &snapmirrorRelationship)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			err = workflow.ExecuteActivity(ctx, backupActivity.DeleteSnapmirror, node, snapmirrorRelationship.UUID).Get(ctx, &ontapAsyncResponse)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
			err = WaitForONTAPJob(ctx, ontapAsyncResponse, node, time.Minute*10)
			if err != nil {
				return nil, ConvertToVSAError(fmt.Errorf("failed to delete snapmirror: %w", err))
			}

			err = workflow.ExecuteActivity(ctx, backupActivity.UpdateVolumeLatestLogicalBackupSize, volume, 0).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			wf.deleteInitiated = true
			err = workflow.ExecuteActivity(ctx, backupActivity.DeleteCloudEndpoint, node, objStore.UUID, dbBackup.Attributes.EndpointUUID).Get(ctx, &ontapAsyncResponse)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			err = workflow.ExecuteActivity(ctx, backupActivity.DeleteSnapshotForBackup, node, dbBackup.Attributes.SnapshotID, volume.VolumeAttributes.ExternalUUID, dbBackup.Attributes.UseExistingSnapshot).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Try to delete snapshot from DB, but don't fail the workflow if this fails
			if !isExpertModeVolume {
				snapshotErr := workflow.ExecuteActivity(ctx, backupActivity.DeleteBackupSnapshotFromDB, dbBackup).Get(ctx, nil)
				if snapshotErr != nil {
					workflow.GetLogger(ctx).Error("Failed to delete snapshot from database", "error", snapshotErr)
				}
			}

			// Hydrate snapshot deletion to CCFE.
			// The snapshot is an ONTAP snapshot on the source volume, so it lives in the
			// source volume's project (volume.Account), not the backup-vault's project
			// (account, derived from the DeleteBackup request). These differ for
			// cross-project backup vaults, so we must use the source volume's account to
			// build the correct CCFE resource path (matching the create/hydration path).
			// volume.Account is preloaded by GetVolume; fall back to the request account
			// only if it isn't available.
			if dbBackup.Attributes != nil && volume != nil && account != nil && !isExpertModeVolume {
				snapshotAccount := account
				if volume.Account != nil {
					snapshotAccount = volume.Account
				}
				snapshot := &datamodel.Snapshot{
					BaseModel: datamodel.BaseModel{
						UUID:      dbBackup.Attributes.SnapshotID,
						CreatedAt: dbBackup.CreatedAt,
					},
					Name:         dbBackup.Name,
					State:        datamodel.LifeCycleStateDeleted,
					StateDetails: datamodel.LifeCycleStateDeletedDetails,
					Description:  dbBackup.Description,
					Volume:       volume,
					Account:      snapshotAccount,
					SnapshotAttributes: &datamodel.SnapshotAttributes{
						SizeInBytes: dbBackup.SizeInBytes,
					},
				}

				location := utils.GetLocation(*snapshot)
				hydrateSnapshotErr := workflow.ExecuteActivity(ctx, backupActivity.HydrateSnapshotDeletionToCCFEActivity,
					snapshot,
					volume.Name,
					location,
					snapshotAccount.Name).Get(ctx, nil)
				if hydrateSnapshotErr != nil {
					// Log the error but don't fail the entire backup deletion workflow
					workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to hydrate snapshot deletion to CCFE for backup %s: %v", dbBackup.Name, hydrateSnapshotErr))
				}
			}
		} else {
			var isBackupShared bool
			err = workflow.ExecuteActivity(ctx, backupActivity.IsBackupShared, dbBackup).Get(ctx, &isBackupShared)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
			if !isBackupShared {
				wf.deleteInitiated = true
				// Only proceed if backup attributes are not nil
				if dbBackup.Attributes != nil {
					err = workflow.ExecuteActivity(ctx, backupActivity.DeleteSnapshotFromObjectStore, node, objStore.UUID, dbBackup.Attributes.EndpointUUID, dbBackup.Attributes.SnapshotID).Get(ctx, &ontapAsyncResponse)
					if err != nil {
						return nil, ConvertToVSAError(err)
					}
					err = WaitForONTAPJob(ctx, ontapAsyncResponse, node, time.Minute*120)
					if err != nil {
						return nil, ConvertToVSAError(fmt.Errorf("failed to delete cloud endpoint: %w", err))
					}
				}
			}
		}
	}

	err = workflow.ExecuteActivity(ctx, backupActivity.DeleteBackup, deleteBackupParams.BackupUUID).Get(ctx, &dbBackup)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Delete remote backup from VCP if this is a cross-region backup
	if dbBackupVault.BackupVaultType == activities.CrossRegionBackupType && dbBackupVault.BackupRegionName != nil {
		remoteBackupErr := workflow.ExecuteActivity(ctx, backupActivity.DeleteRemoteBackupFromVCPActivity,
			dbBackup.UUID,
			dbBackupVault.UUID,
			deleteBackupParams.AccountName,
			*dbBackupVault.BackupRegionName).Get(ctx, nil)
		if remoteBackupErr != nil {
			workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to delete remote backup from VCP for backup %s: %v", dbBackup.UUID, remoteBackupErr))
		}
	}

	// Detach backup vault from expert mode volume if this is the last backup deletion
	var currentBackupCount int64
	if !isVolumeDeleted && volume != nil {
		if isExpertModeVolume {
			countErr := workflow.ExecuteActivity(ctx, backupActivity.GetBackupCountByVolumeVaultAndEndpoint,
				dbBackup.VolumeUUID, dbBackup.BackupVaultID, backupAttributesTrimmedEndpointUUID(dbBackup)).Get(ctx, &currentBackupCount)
			if countErr != nil {
				workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to get current backup count for volume %s: %v", dbBackup.VolumeUUID, countErr))
			} else if currentBackupCount == 0 {
				// This was the last backup, detach the backup vault from the volume
				detachErr := workflow.ExecuteActivity(ctx, backupActivity.DetachBackupVaultFromVolume, volume, dbBackupVault).Get(ctx, nil)
				if detachErr != nil {
					workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to detach backup vault from expert mode volume %s: %v", volume.UUID, detachErr))
				}
			}
		}
	}

	// Try to delete snapshot from DB, but don't fail the workflow if this fails
	if !isExpertModeVolume {
		snapshotErr := workflow.ExecuteActivity(ctx, backupActivity.DeleteBackupSnapshotFromDB, dbBackup).Get(ctx, nil)
		if snapshotErr != nil {
			workflow.GetLogger(ctx).Error("Failed to delete snapshot from database", "error", snapshotErr)
		}
	}

	// Delete BackupMetadata entry if this was the last backup for the volume
	metadataErr := workflow.ExecuteActivity(ctx, backupActivity.DeleteBackupMetadataIfLastBackupActivity, dbBackup.VolumeUUID).Get(ctx, nil)
	if metadataErr != nil {
		// Log the error but don't fail the entire backup deletion workflow
		workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to delete BackupMetadata for volume %s: %v", dbBackup.VolumeUUID, metadataErr))
	}
	return nil, ConvertToVSAError(err)
}

func (wf *BackupDeleteWorkflow) HandleError(ctx workflow.Context, params *commonparams.DeleteBackupParams, errString string) error {
	backupActivity := &activities.BackupActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return err
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
	disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
	disconnectedCtx = workflow.WithActivityOptions(disconnectedCtx, ao)

	dbBackup := &datamodel.Backup{}
	err = workflow.ExecuteActivity(disconnectedCtx, backupActivity.GetBackup, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Get(disconnectedCtx, &dbBackup)
	if err != nil {
		return fmt.Errorf("failed to get backup: %w", err)
	}
	dbBackup.Attributes.DeleteInitiated = wf.deleteInitiated
	if wf.deleteInitiated {
		workflow.GetLogger(ctx).Error(fmt.Sprintf("Backup to error state as delete has been initiated but failed to complete, backupUUID: %s", dbBackup.UUID))
		err = workflow.ExecuteActivity(disconnectedCtx, backupActivity.UpdateBackupError, dbBackup, errString).Get(disconnectedCtx, nil)
		if err != nil {
			return ConvertToVSAError(err)
		}

		dbBackupVault := dbBackup.BackupVault
		// Delete remote backup from VCP if this is a cross-region backup
		if dbBackupVault.BackupVaultType == activities.CrossRegionBackupType && dbBackup.ExternalUUID != "" && dbBackupVault.ExternalUUID != nil && dbBackupVault.BackupRegionName != nil {
			remoteBackupErr := workflow.ExecuteActivity(disconnectedCtx, backupActivity.DeleteRemoteBackupFromVCPActivity,
				dbBackup.ExternalUUID,
				*dbBackupVault.ExternalUUID,
				params.AccountName,
				*dbBackupVault.BackupRegionName).Get(disconnectedCtx, nil)
			if remoteBackupErr != nil {
				workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to delete remote backup from VCP for errored backup %s: %v", dbBackup.UUID, remoteBackupErr))
			}
		}
	} else {
		workflow.GetLogger(ctx).Info(fmt.Sprintf("Reverting backup state to available as delete was not initiated, backupUUID: %s", dbBackup.UUID))
		err = workflow.ExecuteActivity(disconnectedCtx, backupActivity.MarkBackupAvailable, dbBackup).Get(disconnectedCtx, nil)
		if err != nil {
			return ConvertToVSAError(err)
		}
	}

	return nil
}

// UpdateBackupWorkflow Backup Workflow process backup related requests from a customer.
func UpdateBackupWorkflow(ctx workflow.Context, backup *datamodel.Backup) (gcpgenserver.V1betaUpdateBackupRes, error) {
	logger := workflow.GetLogger(ctx)
	backupWf := new(backupUpdateWorkflow)
	err := backupWf.Setup(ctx, backup)
	if err != nil {
		logger.Info("Backup update workflow setup executed with error", "error", err)
		return nil, err
	}
	if err := backupWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}
	backupWf.Status = WorkflowStatusRunning
	err = backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		logger.Info("Update job status for backup executed with error", "error", err)
		return nil, err
	}
	_, customErr := backupWf.Run(ctx, backup)

	if customErr != nil {
		backupWf.Status = WorkflowStatusFailed
		err2 := backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			logger.Error("Failed to update job status", "error", err2)
		}
		return nil, customErr
	}
	backupWf.Status = WorkflowStatusCompleted
	err2 := backupWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		logger.Error("Failed to update job status", "error", err2)
		return nil, ConvertToVSAError(err2)
	}
	logger.Debug("Backup update workflow completed successfully")
	return nil, nil
}

// Setup initializes the workflow with the necessary parameters and sets up a query handler for status updates.
func (wf *backupUpdateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	backupParams := input.(*datamodel.Backup)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = backupParams.Name
	wf.Status = WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

// Run executes the backup creation workflow, including creating the backup and updating its details.
func (wf *backupUpdateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	backup := args[0].(*datamodel.Backup)
	backupUpdateActivity := &activities.BackupActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    time.Duration(updateBackupWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			if updateErr := workflow.ExecuteActivity(disconnectedCtx, backupUpdateActivity.UpdateBackupError, backup, err.Error()).Get(disconnectedCtx, nil); updateErr != nil {
				logger.Error("Failed to update backup state to ERROR", "error", updateErr)
			}
		}
	}()
	err = workflow.ExecuteActivity(ctx, backupUpdateActivity.UpdateBackup, backup).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Update remote backup from VCP if this is a cross-region backup
	err = workflow.ExecuteActivity(ctx, backupUpdateActivity.UpdateRemoteBackupFromVCPActivity, backup).Get(ctx, nil)
	if err != nil {
		// Log the error but don't fail the entire backup workflow
		workflow.GetLogger(ctx).Error(fmt.Sprintf("Failed to update remote backup from VCP for backup %s: %v", backup.UUID, err))
	}

	return nil, nil
}
