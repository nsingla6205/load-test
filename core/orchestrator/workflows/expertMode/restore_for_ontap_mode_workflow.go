package expertMode

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type restoreForOntapModeVolumeWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &restoreForOntapModeVolumeWorkflow{}

const ontapVolumeStyleFlexgroup = "flexgroup"

// convertExpertModeVolumeToVolume converts ExpertModeVolumes to Volume for use in restore workflows.
// NOTE: This creates a partial Volume - VolumeAttributes will be missing FileProperties and BlockProperties.
func convertExpertModeVolumeToVolume(emv *datamodel.ExpertModeVolumes) *datamodel.Volume {
	volumeAttributes := &datamodel.VolumeAttributes{ExternalUUID: emv.ExternalUUID}
	return &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: emv.UUID},
		Name:             emv.Name,
		Description:      emv.Description,
		State:            emv.State,
		SizeInBytes:      emv.SizeInBytes,
		AccountID:        emv.AccountID,
		PoolID:           emv.PoolID,
		SvmID:            emv.SvmID,
		Account:          emv.Account,
		Pool:             emv.Pool,
		Svm:              emv.Svm,
		VolumeAttributes: volumeAttributes,
		DataProtection:   emv.BackupConfig,
	}
}

// RestoreForOntapModeVolumeWorkflow runs full volume restore from backup for ONTAP mode (expert mode) volumes.
// It calls Setup (job and query handler), then Run which contains the core logic and activity invocations.
func RestoreForOntapModeVolumeWorkflow(ctx workflow.Context, params *common.RestoreForOntapModeParams) error {
	log := util.GetLogger(ctx)
	wf := &restoreForOntapModeVolumeWorkflow{}
	if err := wf.Setup(ctx, params); err != nil {
		log.Errorf("Failed to setup RestoreForOntapModeVolumeWorkflow: %v", err)
		return err
	}
	if err := wf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return workflows.ConvertToVSAError(err).OriginalErr
	}
	wf.Status = workflows.WorkflowStatusRunning
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil); err != nil {
		log.Errorf("Failed to update job status to PROCESSING: %v", err)
		return err
	}

	_, customErr := wf.Run(ctx, params)
	if customErr != nil {
		if !workflow.IsContinueAsNewError(customErr.OriginalErr) {
			log.Errorf("RestoreForOntapModeVolumeWorkflow run failed: %v", customErr.OriginalErr)
			wf.Status = workflows.WorkflowStatusFailed
			if updateErr := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr); updateErr != nil {
				log.Errorf("Failed to update job state to ERROR: %v", updateErr)
			}
			return customErr.OriginalErr
		}
		return customErr.OriginalErr
	}
	wf.Status = workflows.WorkflowStatusCompleted
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil); err != nil {
		log.Errorf("Failed to update job state to DONE: %v", err)
		return err
	}
	return nil
}

// Setup initializes the workflow with the necessary parameters and sets up a query handler for status updates.
func (wf *restoreForOntapModeVolumeWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	params, ok := input.(*common.RestoreForOntapModeParams)
	if !ok || params == nil {
		return fmt.Errorf("invalid input: expected *common.RestoreForOntapModeParams")
	}
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = params.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	wf.Logger = util.GetLogger(ctx)

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

// Run executes the restore workflow: convert expert mode volume, fetch backup metadata, enrich from ONTAP, and start RestoreBackupWorkflow.
func (wf *restoreForOntapModeVolumeWorkflow) Run(ctx workflow.Context, args ...interface{}) (_ interface{}, retErr *vsaerrors.CustomError) {
	params := args[0].(*common.RestoreForOntapModeParams)
	log := wf.Logger

	volumeActivity := &activities.VolumeCreateActivity{}
	expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
	ontapRestoreActivity := &activities.OntapModeRestoreActivity{}
	commonActivity := &activities.CommonActivities{}

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: workflows.RestoreStartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var volume *datamodel.Volume
	defer func() {
		// Only restore volume to READY on failure (rollback). On success, RestoreBackupWorkflow sets READY when the actual restore completes.
		if retErr != nil {
			if err2 := workflow.ExecuteActivity(ctx, expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB, volume.UUID, datamodel.LifeCycleStateAvailable).Get(ctx, nil); err2 != nil {
				log.Errorf("Failed to restore expert mode volume state to READY: %v", err2)
			}
		}
	}()

	// 1. Convert ExpertModeVolumes to Volume
	volume = convertExpertModeVolumeToVolume(params.ExpertModeVolume)

	// 2. Resolve ONTAP node (dedicated-capacity uses the pinned host via pool.HostUUID; FLEX uses pool nodes).
	var node *models.Node
	err = workflow.ExecuteActivity(ctx, commonActivity.ResolveOntapNodeForPool, volume.Pool).Get(ctx, &node)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// 3. Get JWT for CVP if not local
	if !env.IsLocalEnv() {
		var token string
		err = workflow.ExecuteActivity(ctx, commonActivity.GetAuthJWTToken, params.AccountName).Get(ctx, &token)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		ctx = workflow.WithValue(ctx, middleware.AuthorizationToken, token)
	}

	// 4. Fetch backup vault metadata for restore
	var backupVault *datamodel.BackupVault
	err = workflow.ExecuteActivity(ctx, volumeActivity.FetchBackupVaultMetadataForRestore, params.BackupPath, volume, params.Region).Get(ctx, &backupVault)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	if backupVault == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("failed to fetch backup vault metadata: received nil response"))
	}

	// 5. Fetch backup metadata for restore
	var backup *datamodel.Backup
	err = workflow.ExecuteActivity(ctx, volumeActivity.FetchBackupMetadataForRestore, params.BackupPath, backupVault, volume, params.Region).Get(ctx, &backup)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	if backup == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("failed to fetch backup metadata: received nil response"))
	}

	// 6. Verify restore target is not the same as backup source volume
	if backup.VolumeUUID == volume.UUID {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("cannot restore backup to the same volume it was created from"))
	}

	// 7. Validate backup is in available or ready state (SDE uses READY, VCP uses AVAILABLE)
	if backup.State != datamodel.LifeCycleStateAvailable && backup.State != datamodel.LifeCycleStateREADY {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("cannot restore from backup '%s' which is not in available or ready state (current state: %s)",
			backup.Name, backup.State))
	}

	// 8. For large volume (flexgroup) backup: fetch restore target constituent count and verify it matches backup
	if backup.Attributes != nil && backup.Attributes.OntapVolumeStyle == ontapVolumeStyleFlexgroup {
		var restoreTargetConstituentCount int32
		err = workflow.ExecuteActivity(ctx, ontapRestoreActivity.FetchConstituentCountForLargeVolume, volume, node).Get(ctx, &restoreTargetConstituentCount)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		err = workflow.ExecuteActivity(ctx, ontapRestoreActivity.VerifyCVCountForLargeVolume, backup, restoreTargetConstituentCount).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	// 7. Fetch bucket metadata for restore (returns updated backupVault with bucket details)
	err = workflow.ExecuteActivity(ctx, volumeActivity.FetchBucketMetadataForRestore, backup, backupVault).Get(ctx, &backupVault)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	backup.BackupVault = backupVault

	// 8. Validate Volume Size
	var backupAttributes datamodel.BackupAttributes
	if backup.Attributes != nil {
		backupAttributes = *backup.Attributes
	}
	requiredVolumeSize := utils.CalculateRequiredVolumeSize(backup.SizeInBytes, backupAttributes)
	if volume.SizeInBytes < requiredVolumeSize {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInsufficientRestoreVolumeSize,
			fmt.Errorf("restored volume size should be greater than or equal to the logical size of the backup: %d bytes", requiredVolumeSize))
	}
	volume.VolumeAttributes.RestoredBackupID = backup.UUID

	// 9. Build CreateVolumeParams for restore
	createVolumeParams := &common.CreateVolumeParams{
		AccountName:         params.AccountName,
		Region:              params.Region,
		BackupPath:          params.BackupPath,
		PoolID:              volume.Pool.UUID,
		PoolDBID:            volume.Pool.ID,
		Name:                volume.Name,
		QuotaInBytes:        uint64(volume.SizeInBytes),
		Protocols:           volume.VolumeAttributes.Protocols,
		IsExpertModeRestore: true,
	}

	// 10. Build volCreateResponse
	volCreateResponse := &vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			Name:         volume.Name,
			ExternalUUID: volume.VolumeAttributes.ExternalUUID,
		},
	}

	// 11. Create Restore Workflow (starts RestoreBackupWorkflow asynchronously)
	err = workflow.ExecuteActivity(ctx, volumeActivity.CreateRestoreWorkflow,
		createVolumeParams, volume, ([]*common.HostParams)(nil), backupVault, backup, volCreateResponse).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	return nil, nil
}

func (wf *restoreForOntapModeVolumeWorkflow) UpdateJobStatus(ctx workflow.Context, status string, err error) error {
	return wf.BaseWorkflow.UpdateJobStatus(ctx, status, err)
}
