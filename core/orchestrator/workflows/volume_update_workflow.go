package workflows

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/flexcache_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	factorycommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/temporalbackend"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type volumeUpdateWorkflow struct {
	// add fields needed for volume workflow
	BaseWorkflow
}

// Enforcing the WorkflowInterface on volumeUpdateWorkflow
var _ WorkflowInterface = &volumeUpdateWorkflow{}
var (
	convertCacheParameters               = _convertCacheParameters
	isUpdateFlexCacheRequired            = _isUpdateFlexCacheRequired
	isUpdateFlexCachePrepopulateRequired = _isUpdateFlexCachePrepopulateRequired

	// operationBudget is the in-workflow time limit for UpdateVolumeWorkflow. When
	// OperationBudgetExceeded fires, the workflow fails and defer runs rollback + READY reset.
	// triggering a clean rollback well before Temporal's safety-net WorkflowRunTimeout
	// (2× this budget) fires. Defaults to the global workflow timeout so it aligns
	// with the historical per-operation time expectation.
	// Override in tests via: operationBudget = N * time.Minute.
	operationBudget = workflowengine.GetWorkflowGlobalTimeout()
)

// UpdateVolumeWorkflow Update Volume Workflow process volume related requests from a customer.
func UpdateVolumeWorkflow(ctx workflow.Context, params *common.UpdateVolumeParams, volume *datamodel.Volume) error {
	log := workflow.GetLogger(ctx)
	volumeWf := new(volumeUpdateWorkflow)
	err := volumeWf.Setup(ctx, volume)
	if err != nil {
		log.Error("Volume update workflow setup executed with error", "error", err)
		return err
	}
	if err = volumeWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return err
	}
	volumeWf.Status = WorkflowStatusRunning
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Error("Failed to update job status to Processing for UpdateVolumeWorkflow", "error", err)
		return err
	}

	_, customErr := volumeWf.Run(ctx, params, volume)
	if customErr != nil {
		log.Error("UpdateVolumeWorkflow completed with error", "error", customErr)
		volumeWf.Status = WorkflowStatusFailed
		err2 := volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			log.Error("Failed to update job status to Done with err for UpdateVolumeWorkflow", "error", err2)
			return err2
		}
		return customErr
	}

	volumeWf.Status = WorkflowStatusCompleted
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Error("Failed to update job status to Done for UpdateVolumeWorkflow", "error", err)
	}
	return err
}

func (wf *volumeUpdateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	volume := input.(*datamodel.Volume)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = volume.Account.Name
	wf.Status = WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *volumeUpdateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	params := args[0].(*common.UpdateVolumeParams)
	volume := args[1].(*datamodel.Volume)
	sanitizeUpdateParamsForFlexCache(params, volume)
	updateActivity := &activities.VolumeUpdateActivity{}
	deleteActivity := &activities.VolumeDeleteActivity{}
	flexCacheUpdateActivity := &flexcache_activities.FlexCacheVolumeUpdateActivity{}
	commonActivities := activities.CommonActivities{}
	volumeCreateActivity := activities.VolumeCreateActivity{}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	// Use LV-specific timeout for large capacity volumes. Activity timeout/retry policy stays on
	// workflow.Context (Temporal SDK) — not orchestrator.ActivityOptions — so we do not imply
	// go-workflows parity for StartToCloseTimeout, HeartbeatTimeout, or NonRetryableErrorTypes.
	startToCloseTimeout := getVolumeStartToCloseTimeout(volume)
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(startToCloseTimeout) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})
	orchCtx := temporalbackend.NewTemporalContext(ctx)
	budgetStart := orchCtx.WorkflowStartTime()
	if budgetStart.IsZero() {
		// Temporal test env may leave WorkflowStartTime unset; anchor the budget to now.
		budgetStart = orchestrator.Now(orchCtx)
	}
	operationBudgetEnd := budgetStart.Add(operationBudget)
	rollbackManager := common.NewRollbackManager()
	orchCtx = orchCtx.WithOperationBudgetCheck(func() bool { return orchestrator.Now(orchCtx).After(operationBudgetEnd) })

	// Forward steps use orchCtx + RunActivity for the operation budget; re-bridge ctx so
	// legacy helpers (WaitForONTAPJob, workflowSleep, rollback) keep the same Temporal context.
	ctx = temporalbackend.WorkflowCtx(orchCtx)
	defer func() {
		if err != nil {
			disconnectedOrchCtx := orchestrator.NewDisconnectedContext(orchCtx).ClearOperationBudgetCheck()
			if runErr := orchestrator.RunActivity(disconnectedOrchCtx, volumeCreateActivity.UpdateVolumeStateInDB, nil,
				volume.UUID, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails); runErr != nil {
				log.Error("Failed to update volume state in DB to READY", "error", runErr)
			}
			// rollbackManager.ExecuteRollback still takes workflow.Context
			rollbackManager.ExecuteRollback(temporalbackend.WorkflowCtx(disconnectedOrchCtx), err)
		}
	}()

	// Bail out immediately if the workflow spent its entire budget waiting in the
	// queue — no activities have run yet so there is nothing to roll back, but
	// the deferred state-reset (READY) still needs to fire via err != nil.
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded: workflow was queued too long")
		return nil, ConvertToVSAError(err)
	}

	var dbNodes []*datamodel.Node
	if err = orchestrator.RunActivity(orchCtx, commonActivities.GetNode, &dbNodes, volume.Pool.ID); err != nil {
		return nil, ConvertToVSAError(err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   volume.Pool.DeploymentName,
		OntapCredentials: volume.Pool.PoolCredentials,
	})

	// when flag is on and vault is changed or removed, delete snapmirror for the
	// current vault, then set volume.DataProtection.BackupVaultID to the new value (or empty). The orchestrator
	// leaves BackupVaultID unchanged so this workflow receives the volume with the old vault ID.
	var backupVaultSwitched bool
	if utils.EnableBackupVaultSwitching && params.DataProtection != nil && params.DataProtection.BackupVaultID != nil &&
		volume.DataProtection != nil && volume.DataProtection.BackupVaultID != "" &&
		(*params.DataProtection.BackupVaultID == "" || *params.DataProtection.BackupVaultID != volume.DataProtection.BackupVaultID) {
		oldBackupVaultID := volume.DataProtection.BackupVaultID
		newBackupVaultID := *params.DataProtection.BackupVaultID
		var ontapAsyncResponse *vsa.OntapAsyncResponse
		if err = orchestrator.RunActivity(orchCtx, deleteActivity.DeleteSnapmirrorInONTAP, &ontapAsyncResponse, volume, node); err != nil {
			return nil, ConvertToVSAError(err)
		}
		if err = WaitForONTAPJob(ctx, ontapAsyncResponse, node, time.Minute*10); err != nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to delete snapmirror: %w", err))
		}
		// Load-bearing operation-budget check: WaitForONTAPJob uses wall-clock polling and is not
		// interrupted mid-wait by the operation budget (see also FlexCache wait below).
		if orchCtx.OperationBudgetExceeded() {
			err = fmt.Errorf("operation budget exceeded after snapmirror deletion wait")
			return nil, ConvertToVSAError(err)
		}

		// Remove the latest backup snapshot for the vault being left (same pattern as delete backup workflow).
		backupActivity := &activities.BackupActivity{}
		var oldBackupVault *datamodel.BackupVault
		if err = orchestrator.RunActivity(orchCtx, backupActivity.GetBackupVault, &oldBackupVault, volume.DataProtection.BackupVaultID); err != nil {
			return nil, ConvertToVSAError(err)
		}
		var latestBackup *datamodel.Backup
		if err = orchestrator.RunActivity(orchCtx, backupActivity.GetLatestBackupByVolumeAndVault, &latestBackup, volume.UUID, oldBackupVault.ID); err != nil {
			return nil, ConvertToVSAError(err)
		}
		if latestBackup != nil && latestBackup.Attributes != nil && volume.VolumeAttributes != nil {
			var isExpertModeVolume bool
			if err = orchestrator.RunActivity(orchCtx, backupActivity.IsExpertModeVolume, &isExpertModeVolume, volume.UUID); err != nil {
				return nil, ConvertToVSAError(err)
			}
			if err = orchestrator.RunActivity(orchCtx, backupActivity.DeleteSnapshotForBackup, nil, node, latestBackup.Attributes.SnapshotID, volume.VolumeAttributes.ExternalUUID, latestBackup.Attributes.UseExistingSnapshot); err != nil {
				return nil, ConvertToVSAError(err)
			}
			if !isExpertModeVolume {
				if snapshotErr := orchestrator.RunActivity(orchCtx.ClearOperationBudgetCheck(), backupActivity.DeleteBackupSnapshotFromDB, nil, latestBackup); snapshotErr != nil {
					log.Error("Failed to delete snapshot from database", "error", snapshotErr)
				}
			}

			// Hydrate snapshot deletion to CCFE (same pattern as DeleteBackupWorkflow last-backup direct ONTAP path).
			if latestBackup.Attributes != nil && volume.Account != nil && !isExpertModeVolume {
				snapshot := &datamodel.Snapshot{
					BaseModel: datamodel.BaseModel{
						UUID:      latestBackup.Attributes.SnapshotID,
						CreatedAt: latestBackup.CreatedAt,
					},
					Name:         latestBackup.Name,
					State:        datamodel.LifeCycleStateDeleted,
					StateDetails: datamodel.LifeCycleStateDeletedDetails,
					Description:  latestBackup.Description,
					Volume:       volume,
					Account:      volume.Account,
					SnapshotAttributes: &datamodel.SnapshotAttributes{
						SizeInBytes: latestBackup.SizeInBytes,
					},
				}

				location := utils.GetLocation(*snapshot)
				if hydrateSnapshotErr := orchestrator.RunActivity(orchCtx.ClearOperationBudgetCheck(), backupActivity.HydrateSnapshotDeletionToCCFEActivity, nil,
					snapshot,
					volume.Name,
					location,
					volume.Account.Name); hydrateSnapshotErr != nil {
					log.Error("Failed to hydrate snapshot deletion to CCFE for backup", "name", latestBackup.Name, "error", hydrateSnapshotErr)
				}
			}
		}

		volume.DataProtection.BackupVaultID = newBackupVaultID
		backupVaultSwitched = true
		log.Info("Backup vault switched for volume; backup_chain_bytes unchanged until DB recalc", "uuid", volume.UUID, "old_vault", oldBackupVaultID, "new_vault", newBackupVaultID)
	}
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded after backup vault switch")
		return nil, ConvertToVSAError(err)
	}

	// Update the snapshot policy if it is provided in the params
	if params.SnapshotPolicy != nil && params.SnapshotPolicy.Name != "" && !volume.VolumeAttributes.IsDataProtection {
		updatingPolicy := factorycommon.PopulateSnapshotPolicyFromParams(params.SnapshotPolicy)

		// If the volume does not have an existing snapshot policy, we need to create a new one using the provided snapshot policy
		if volume.SnapshotPolicy == nil || volume.SnapshotPolicy.Name == "" {
			createActivity := &activities.VolumeCreateActivity{}
			volume.SnapshotPolicy = updatingPolicy

			if err = orchestrator.RunActivity(orchCtx, createActivity.CreateSnapshotPolicyInONTAP, nil, volume, node); err != nil {
				return nil, ConvertToVSAError(err)
			}
			rollbackManager.AddActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, volume.SnapshotPolicy.Name, node)
		} else // If the volume has an existing snapshot policy, we need to update it with only the changes
		{
			if len(updatingPolicy.Schedules) == 0 {
				// If the schedules are not populated in update, we want to set them as the existing schedules
				// This is done because ONTAP cannot update the snapshot policy without any schedules
				// This will happen when the user is trying to enable/disable the snapshot policy, without any change to schedules
				updatingPolicy.Schedules = volume.SnapshotPolicy.Schedules
			}
			// Register rollback before forward (consistent with UpdateVolumeInONTAP and other
			// ONTAP mutations). On immediate failure the rollback step is a no-op.
			rollbackManager.AddActivity(updateActivity.UpdateSnapshotPolicyInOntap, node, updatingPolicy, volume.SnapshotPolicy)
			if err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateSnapshotPolicyInOntap, nil, node, volume.SnapshotPolicy, updatingPolicy); err != nil {
				return nil, ConvertToVSAError(err)
			}
			volume.SnapshotPolicy = updatingPolicy
		}
	}
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded after snapshot policy update")
		return nil, ConvertToVSAError(err)
	}

	volResponse := &vsa.VolumeResponse{}
	if err = orchestrator.RunActivity(orchCtx, updateActivity.GetVolumeFromONTAP, &volResponse, volume, node); err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Validate volume size reduction against actual used space from ONTAP
	if params.QuotaInBytes > 0 && params.QuotaInBytes < volResponse.Size {
		// Size reduction requested - validate against actual used space
		if volResponse.UsedBytes > params.QuotaInBytes {
			err = fmt.Errorf("cannot reduce volume size to %s as it is currently using %s. "+
				"Free up space or choose a larger size",
				utils.FmtUint64Bytes(uint64(params.QuotaInBytes)),
				utils.FmtUint64Bytes(uint64(volResponse.UsedBytes)))
			log.Error("cannot reduce volume size below used space", "uuid", volume.UUID, "error", err)
			return nil, ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrVolumeSizeTooSmall, err))
		}
		log.Debug("Volume size reduction validation passed", "uuid", volume.UUID, "new_size", params.QuotaInBytes, "used_bytes", volResponse.UsedBytes)
	}

	if factorycommon.IsVolumeUpdateRequired(volResponse, params, volume) {
		rollbackManager.AddActivity(updateActivity.UpdateVolumeInONTAP, volume, getUpdateParamsForRollback(volResponse, volume), node)
		if err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateVolumeInONTAP, nil, volume, params, node); err != nil {
			return nil, ConvertToVSAError(err)
		}
	}
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded after volume update in ONTAP")
		return nil, ConvertToVSAError(err)
	}

	if isUpdateFlexCacheRequired(volume, params) {
		var ontapAsyncResponse *vsa.OntapAsyncResponse
		rollbackManager.AddActivity(flexCacheUpdateActivity.UpdateFlexCacheVolumeInONTAP, volume,
			getUpdateParamsForRollback(volResponse, volume), node)
		if err = orchestrator.RunActivity(orchCtx, flexCacheUpdateActivity.UpdateFlexCacheVolumeInONTAP, &ontapAsyncResponse, volume, params, node); err != nil {
			return nil, ConvertToVSAError(err)
		}

		if err = WaitForONTAPJob(ctx, ontapAsyncResponse, node, 30*time.Minute); err != nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to update FlexCache volume: %w", err))
		}
		// Load-bearing operation-budget check after ONTAP async wait (not checked during the wait).
		if orchCtx.OperationBudgetExceeded() {
			err = fmt.Errorf("operation budget exceeded after FlexCache volume update")
			return nil, ConvertToVSAError(err)
		}
	}

	// Handle prepopulate separately if requested
	if isUpdateFlexCachePrepopulateRequired(volume, params) {
		if prepopErr := orchestrator.RunActivity(orchCtx, flexCacheUpdateActivity.UpdatePrepopulateState, nil,
			volume.UUID, cvpModels.FlexCacheConfigV1betaCachePrePopulateStateCACHEPREPOPULATESTATEUNSPECIFIED); prepopErr != nil {
			log.Error("Failed to update prepopulate state to UNSPECIFIED", "error", prepopErr)
		}

		// Start the ONTAP prepopulate job
		var ontapJobUUID string
		if prepopErr := orchestrator.RunActivity(orchCtx, flexCacheUpdateActivity.StartFlexCachePrepopulate, &ontapJobUUID,
			volume, params, node); prepopErr != nil {
			log.Error("Failed to start prepopulate in ONTAP", "error", prepopErr)
			if stateErr := orchestrator.RunActivity(orchCtx, flexCacheUpdateActivity.UpdatePrepopulateState, nil,
				volume.UUID, cvpModels.FlexCacheConfigV1betaCachePrePopulateStateERROR); stateErr != nil {
				log.Error("Failed to update prepopulate state to ERROR after ONTAP failure", "error", stateErr)
			}
			log.Warn("Prepopulate failed to start but continuing with volume update - prepopulate is best-effort")
		} else if ontapJobUUID == "" {
			if stateErr := orchestrator.RunActivity(orchCtx, flexCacheUpdateActivity.UpdatePrepopulateState, nil,
				volume.UUID, cvpModels.FlexCacheConfigV1betaCachePrePopulateStateCOMPLETE); stateErr != nil {
				log.Error("Failed to update prepopulate state to COMPLETE", "error", stateErr)
			}
		} else {
			// Create a Job record for tracking with ONTAP UUID in job attributes
			var createdJobUUID string
			if prepopErr := orchestrator.RunActivity(orchCtx, flexCacheUpdateActivity.CreatePrepopulateJob, &createdJobUUID,
				volume, ontapJobUUID); prepopErr != nil {
				log.Error("Failed to create job record", "error", prepopErr)
				if stateErr := orchestrator.RunActivity(orchCtx, flexCacheUpdateActivity.UpdatePrepopulateState, nil,
					volume.UUID, cvpModels.FlexCacheConfigV1betaCachePrePopulateStateERROR); stateErr != nil {
					log.Error("Failed to update prepopulate state to ERROR after job creation failure", "error", stateErr)
				}
				log.Warn("ONTAP prepopulate job started but cannot be tracked - job will run to completion in ONTAP for volume", "ontapJobUUID", ontapJobUUID, "uuid", volume.UUID)
			}
		}
	}
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded after FlexCache prepopulate")
		return nil, ConvertToVSAError(err)
	}

	// Avoid updating the lun if the size is not changed
	if volume.VolumeAttributes != nil && len(volume.VolumeAttributes.Protocols) > 0 && utils.IsSANProtocol(volume.VolumeAttributes.Protocols[0]) && (params.QuotaInBytes > volume.SizeInBytes || (params.SnapReserve != nil && volume.VolumeAttributes != nil && *params.SnapReserve != volume.VolumeAttributes.SnapReserve)) && !volume.VolumeAttributes.IsDataProtection {
		updatedLun := &vsa.LunResponse{}
		if err = orchestrator.RunActivity(orchCtx, updateActivity.GetVolumeFromONTAP, &volResponse, volume, node); err != nil {
			return nil, ConvertToVSAError(err)
		}
		if err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateLun, &updatedLun, volume, volResponse, node); err != nil {
			return nil, ConvertToVSAError(err)
		}
		splitLunName := strings.Split(updatedLun.Name, "/")
		lunName := splitLunName[len(splitLunName)-1]
		if volume.VolumeAttributes != nil && volume.VolumeAttributes.BlockDevices != nil && len(*volume.VolumeAttributes.BlockDevices) > 0 {
			blockDevices := *volume.VolumeAttributes.BlockDevices
			for i := range blockDevices {
				if blockDevices[i].Name == lunName {
					blockDevice := &common.BlockDevice{
						Name:            blockDevices[i].Name,
						SizeInBytes:     updatedLun.Size,
						OSType:          blockDevices[i].OSType,
						LunSerialNumber: blockDevices[i].Identifier,
						LunUUID:         blockDevices[i].LunUUID,
					}
					volumeAttachedHG := utils.GetHgUUIDs(blockDevices[i].HostGroupDetails)
					blockDevice.HostGroups = volumeAttachedHG
					updateOrAddBlockDevice(params, blockDevice)
				}
			}
		}
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		// No rollback for LUN because we cannot decrease the size of a LUN in ONTAP.
	}
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded after LUN update")
		return nil, ConvertToVSAError(err)
	}

	// Check BlockDevices first, then fallback to BlockProperties for host group updates
	if len(params.BlockDevices) > 0 {
		// Use BlockDevices as primary source for host group updates
		// For now, we'll use the first BlockDevice's host groups for consistency
		// In the future, this could be extended to handle multiple BlockDevices
		primaryBlockDevice := params.BlockDevices[0]

		// Get current host groups from BlockDevices if available, otherwise fallback to BlockProperties
		var volumeAttachedHG []string
		if volume.VolumeAttributes.BlockDevices != nil && len(*volume.VolumeAttributes.BlockDevices) > 0 {
			primaryVolumeBlockDevice := (*volume.VolumeAttributes.BlockDevices)[0]
			volumeAttachedHG = utils.GetHgUUIDs(primaryVolumeBlockDevice.HostGroupDetails)
		}

		if !utils.IsSliceEqual(primaryBlockDevice.HostGroups, volumeAttachedHG) {
			toCreate, toDelete := activities.HostGroupsUpdateDiffForVolume(volumeAttachedHG, primaryBlockDevice.HostGroups)

			// Ensure the lun iGroup maps to delete created
			if len(toDelete) > 0 {
				if err = orchestrator.RunActivity(orchCtx, updateActivity.UnmapHostGroupFromDisk, nil, volume, toDelete, node); err != nil {
					return nil, ConvertToVSAError(err)
				}
			}

			if len(toCreate) > 0 {
				if err = orchestrator.RunActivity(orchCtx, updateActivity.EnsureHostGroupsExistsAndMapDisk, nil, volume, toCreate, node); err != nil {
					return nil, ConvertToVSAError(err)
				}
			}
		}
	} else if params.BlockProperties != nil {
		// Fallback: Use BlockProperties if BlockDevices are not provided
		volumeAttachedHG := utils.GetHgUUIDs(volume.VolumeAttributes.BlockProperties.HostGroupDetails)
		if !utils.IsSliceEqual(params.BlockProperties.HostGroupUUIDs, volumeAttachedHG) {
			toCreate, toDelete := activities.HostGroupsUpdateDiffForVolume(volumeAttachedHG, params.BlockProperties.HostGroupUUIDs)

			if len(toCreate) > 0 {
				if err = orchestrator.RunActivity(orchCtx, updateActivity.EnsureHostGroupsExistsAndMapDisk, nil, volume, toCreate, node); err != nil {
					return nil, ConvertToVSAError(err)
				}
			}

			// Ensure the lun iGroup maps to delete created
			if len(toDelete) > 0 {
				if err = orchestrator.RunActivity(orchCtx, updateActivity.UnmapHostGroupFromDisk, nil, volume, toDelete, node); err != nil {
					return nil, ConvertToVSAError(err)
				}
			}
		}
	}
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded after host group update")
		return nil, ConvertToVSAError(err)
	}

	if volume.VolumeAttributes != nil && volume.VolumeAttributes.FileProperties != nil {
		// Update junction path only if it is provided in the params
		if params.FileProperties != nil && params.FileProperties.JunctionPath != "" && params.FileProperties.JunctionPath != volume.VolumeAttributes.FileProperties.JunctionPath {
			rollbackManager.AddActivity(updateActivity.UpdateJunctionPathInONTAP, volume, volume.VolumeAttributes.FileProperties.JunctionPath, node)
			if err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateJunctionPathInONTAP, nil, volume, params.FileProperties.JunctionPath, node); err != nil {
				return nil, ConvertToVSAError(err)
			}
			volume.VolumeAttributes.FileProperties.JunctionPath = params.FileProperties.JunctionPath
		}
		// Update export policy rules only if it is provided in the params
		if params.FileProperties != nil && factorycommon.IsExportPolicyRulesUpdateRequired(volume.VolumeAttributes.FileProperties.ExportPolicy, params.FileProperties.ExportPolicy) {
			if params.FileProperties.ExportPolicy == nil {
				params.FileProperties.ExportPolicy = &models.ExportPolicy{}
			}
			params.FileProperties.ExportPolicy.ExportPolicyName = volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName
			rollbackManager.AddActivity(updateActivity.UpdateExportPolicyRulesInONTAP, volume, volume.VolumeAttributes.FileProperties.ExportPolicy, node)
			if err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateExportPolicyRulesInONTAP, nil, volume, params.FileProperties.ExportPolicy, node); err != nil {
				return nil, ConvertToVSAError(err)
			}
			volume.VolumeAttributes.FileProperties.ExportPolicy = getUpdatedExportPolicy(params.FileProperties.ExportPolicy)
		}
		if enableSmb && volume.VolumeAttributes != nil && utils.IsSMBProtocols(volume.VolumeAttributes.Protocols) && len(params.SMBShareSettings) != 0 {
			if err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateSMBShareSettings, nil, volume, params, node); err != nil {
				return nil, ConvertToVSAError(err)
			}
		}
	}
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded after file properties update")
		return nil, ConvertToVSAError(err)
	}

	if volume.DataProtection != nil && volume.DataProtection.BackupVaultID != "" {
		if !env.IsLocalEnv() {
			var token string
			if err = orchestrator.RunActivity(orchCtx, commonActivities.GetAuthJWTToken, &token, params.AccountName); err != nil {
				log.Error("Failed to get token for account", "accountName", params.AccountName, "error", err)
				return nil, ConvertToVSAError(err)
			}
			orchCtx = orchestrator.WithValue(orchCtx, middleware.AuthorizationToken, token)
			ctx = temporalbackend.WorkflowCtx(orchCtx)
		}

		var backupVault *datamodel.BackupVault
		if err = orchestrator.RunActivity(orchCtx, updateActivity.CheckBackupVaultExistInVCP, &backupVault, volume, params.Region); err != nil {
			return nil, ConvertToVSAError(err)
		}

		volumeActivity := &activities.VolumeCreateActivity{}
		var remoteBV *datamodel.BackupVault
		if err = orchestrator.RunActivity(orchCtx, volumeActivity.CheckOrCreateRemoteBackupVaultInVCP, &remoteBV, volume, backupVault, nil); err != nil {
			return nil, ConvertToVSAError(err)
		}

		backupRegion := params.Region
		if backupVault.BackupVaultType == activities.CrossRegionBackupType && *backupVault.BackupRegionName != "" {
			backupRegion = *backupVault.BackupRegionName
		}

		var tenancyDetails *common.TenancyInfo
		if backupVault.ServiceType != activities.GCBDRServiceType {
			if err = orchestrator.RunActivity(orchCtx, updateActivity.FindTenancyDetails, &tenancyDetails, volume.VolumeAttributes.VendorSubnetID, volume.Account.Name, &backupRegion); err != nil {
				return nil, ConvertToVSAError(err)
			}
		} else {
			if backupVault.BucketDetails != nil && len(backupVault.BucketDetails) > 0 {
				tenancyDetails = &common.TenancyInfo{
					RegionalTenantProject: backupVault.BucketDetails[0].TenantProjectNumber,
				}
			} else {
				log.Error("GCBDR vault has no bucket details with tenant project", "uuid", backupVault.UUID)
				err = fmt.Errorf("GCBDR vault has no tenant project information")
				return nil, ConvertToVSAError(err)
			}
		}

		bucketDetails := &common.BucketDetails{}
		if err = orchestrator.RunActivity(orchCtx, updateActivity.CheckBucketResourceName, &bucketDetails, volume); err != nil {
			return nil, ConvertToVSAError(err)
		}

		if bucketDetails.BucketName == "" && bucketDetails.ServiceAccountName == "" && bucketDetails.TenantProjectNumber == "" {
			resourceName := &common.ResourceNames{}
			if err = orchestrator.RunActivity(orchCtx, updateActivity.GenerateResourceNamesForBackupVault, &resourceName, volume, tenancyDetails, params.Region); err != nil {
				return nil, ConvertToVSAError(err)
			}

			var kmsGrant *string
			if params.DataProtection != nil && !nillable.IsNilOrEmpty(params.DataProtection.KmsGrant) {
				kmsGrant = params.DataProtection.KmsGrant
			}

			if err = orchestrator.RunActivity(orchCtx, updateActivity.CreateBucketForBackupVault, &bucketDetails, resourceName, tenancyDetails, backupRegion, kmsGrant); err != nil {
				return nil, ConvertToVSAError(err)
			}

			if backupVault.ServiceType != activities.GCBDRServiceType {
				bucketDetails.VendorSubnetID = volume.VolumeAttributes.VendorSubnetID
			}

			// Setting the 'satisfiesPzi' and 'satisfiesPzs' fields in bucketDetails by fetching the latest info from GCP
			err = syncBucketDetailsWithGCP(ctx, bucketDetails)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
			if orchCtx.OperationBudgetExceeded() {
				err = fmt.Errorf("operation budget exceeded after sync bucket details with GCP")
				return nil, ConvertToVSAError(err)
			}

			if err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateBucketDetailsOfBackupVault, nil, volume, bucketDetails); err != nil {
				return nil, ConvertToVSAError(err)
			}
		}

		if err = orchestrator.RunActivity(orchCtx, volumeActivity.UpdateRemoteBackupVaultWithBucketDetails, nil, volume, backupVault, remoteBV, bucketDetails); err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Grant pool SA access to GCBDR bucket unconditionally — runs on every volume operation
		// so that a pool attaching to an already-provisioned vault still receives the IAM grant.
		if backupVault.ServiceType == activities.GCBDRServiceType {
			if volume.Pool == nil {
				log.Error("Pool details not available for volume", "uuid", volume.UUID)
				err = fmt.Errorf("pool details required for GCBDR bucket permissions")
				return nil, ConvertToVSAError(err)
			}
			volumeCreateActivity := &activities.VolumeCreateActivity{}
			if err = orchestrator.RunActivity(orchCtx, volumeCreateActivity.SetupCrossProjectBackupPermissions, nil, volume.Pool, bucketDetails); err != nil {
				log.Error("Failed to setup cross-project backup permissions", "error", err)
				return nil, ConvertToVSAError(err)
			}
			log.Info("Successfully granted pool SA access to GCBDR bucket", "bucketName", bucketDetails.BucketName)
		}

		// TODO: Optimize this to avoid running for each volume call.
		// This is currently unoptimized and runs for every volume operation.
		// Consider optimizing to run once per pool or caching the results.
		if backupVault.BackupVaultType == activities.CrossRegionBackupType && backupVault.BackupRegionName != nil && *backupVault.BackupRegionName != "" {
			volumeCreateActivity := &activities.VolumeCreateActivity{}
			if err = orchestrator.RunActivity(orchCtx, volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity, nil, backupVault, volume.Pool, bucketDetails); err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Wait for service account to be ready
			err = workflowSleep(ctx, time.Second*90)
			if err != nil {
				log.Error("Failed to sleep after cross-region backup permissions are created", "error", err)
			}
			if orchCtx.OperationBudgetExceeded() {
				err = fmt.Errorf("operation budget exceeded after cross-region permission sleep")
				return nil, ConvertToVSAError(err)
			}
		}
	}
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded after backup vault provisioning")
		return nil, ConvertToVSAError(err)
	}

	if volume.DataProtection != nil && params.DataProtection != nil && !nillable.IsNilOrEmpty(params.DataProtection.BackupPolicyId) {
		// Assigning backup policy to the volume
		if !env.IsLocalEnv() {
			var token string
			if err = orchestrator.RunActivity(orchCtx, commonActivities.GetAuthJWTToken, &token, params.AccountName); err != nil {
				log.Error("Failed to get token for account", "accountName", params.AccountName, "error", err)
				return nil, ConvertToVSAError(err)
			}
			orchCtx = orchestrator.WithValue(orchCtx, middleware.AuthorizationToken, token)
			ctx = temporalbackend.WorkflowCtx(orchCtx)
		}

		var backupPolicyExists bool
		if err = orchestrator.RunActivity(orchCtx, updateActivity.VerifyIfBackupPolicyExistsInVCP, &backupPolicyExists, *params.DataProtection.BackupPolicyId, volume.AccountID); err != nil {
			return nil, ConvertToVSAError(err)
		}

		if !backupPolicyExists {
			// Check if VCP region should be used
			if !utils.IsCVPHostConfigured() {
				log.Warn("Backup policy does not exist in VCP in a VCP-only region (SDE unavailable); volume update requires an existing VCP backup policy", "backupPolicyId", *params.DataProtection.BackupPolicyId)
				return nil, ConvertToVSAError(customerrors.NewNotFoundErr(
					fmt.Sprintf("Backup policy %s not found", *params.DataProtection.BackupPolicyId),
					nil,
				))
			}
			backupPolicyActivity := &activities.BackupPolicyActivity{}
			var vcpBackupPolicy *datamodel.BackupPolicy
			if err = orchestrator.RunActivity(orchCtx, updateActivity.FetchAndCreateBackupPolicyFromSDE, &vcpBackupPolicy, volume, params.Region); err != nil {
				return nil, ConvertToVSAError(err)
			}
			if err = orchestrator.RunActivity(orchCtx, updateActivity.CreateScheduleForBackupPolicy, nil, vcpBackupPolicy, params.BackupSchedule); err != nil {
				return nil, ConvertToVSAError(err)
			}

			if !vcpBackupPolicy.PolicyEnabled {
				if err = orchestrator.RunActivity(orchCtx, backupPolicyActivity.PauseBackupPolicySchedule, nil, vcpBackupPolicy); err != nil {
					return nil, ConvertToVSAError(err)
				}
			}
		} else {
			// Backup policy exists in VCP - ensure schedule exists (create only once)
			backupPolicyActivity := &activities.BackupPolicyActivity{}
			// Get the backup policy from VCP
			var vcpBackupPolicy *datamodel.BackupPolicy
			if err = orchestrator.RunActivity(orchCtx, backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, &vcpBackupPolicy, *params.DataProtection.BackupPolicyId, volume.AccountID); err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Check if schedule already exists
			var scheduleExists bool
			if err = orchestrator.RunActivity(orchCtx, backupPolicyActivity.CheckIfBackupPolicyScheduleExists, &scheduleExists, vcpBackupPolicy.UUID); err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Create schedule only if it doesn't exist
			if !scheduleExists {
				if err = orchestrator.RunActivity(orchCtx, updateActivity.CreateScheduleForBackupPolicy, nil, vcpBackupPolicy, params.BackupSchedule); err != nil {
					return nil, ConvertToVSAError(err)
				}
			}

			// Handle policy enabled/disabled state
			if !vcpBackupPolicy.PolicyEnabled {
				if err = orchestrator.RunActivity(orchCtx, backupPolicyActivity.PauseBackupPolicySchedule, nil, vcpBackupPolicy); err != nil {
					return nil, ConvertToVSAError(err)
				}
			} else {
				// If policy is enabled, ensure schedule is unpaused
				if err = orchestrator.RunActivity(orchCtx, backupPolicyActivity.UnpauseBackupPolicySchedule, nil, vcpBackupPolicy); err != nil {
					return nil, ConvertToVSAError(err)
				}
			}
		}
	}
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded after backup policy assignment")
		return nil, ConvertToVSAError(err)
	}

	// If the request is to update individual qos for a volume
	if params.ThroughputMibps != nil || params.Iops != nil {
		if !volume.VolumePerformanceGroupID.Valid || volume.VolumePerformanceGroup == nil {
			return nil, ConvertToVSAError(fmt.Errorf("volume %s has no VolumePerformanceGroupID", volume.UUID))
		}
		// Determine the new values (use current if param is nil)
		newThroughput := volume.VolumePerformanceGroup.ThroughputMibps
		if params.ThroughputMibps != nil {
			newThroughput = *params.ThroughputMibps
		}
		newIops := volume.VolumePerformanceGroup.Iops
		if params.Iops != nil {
			newIops = *params.Iops
		}

		// If the request is to update individual qos for a volume and it is already assigned to its autogenerated VPG, then we just need to update the existing qosPolicyGroup's parameters.
		if volume.VolumePerformanceGroup.IsAutoGen {
			// Validate that OntapQosPolicyID exists
			if volume.VolumePerformanceGroup.OntapQosPolicyID == "" {
				return nil, ConvertToVSAError(fmt.Errorf("volume %s has autogenerated VPG but missing OntapQosPolicyID", volume.UUID))
			}

			// Capture rollback args before mutation then run via orchestrator for uniform pattern.
			currentThroughput := volume.VolumePerformanceGroup.ThroughputMibps
			currentIops := volume.VolumePerformanceGroup.Iops

			rollbackManager.AddActivity(updateActivity.UpdateQoSPolicyGroupForVolume,
				volume, &currentThroughput, &currentIops, node)
			if err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateQoSPolicyGroupForVolume, nil,
				volume, newThroughput, newIops, node); err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Update the VPG in the database with new values
			volume.VolumePerformanceGroup.ThroughputMibps = newThroughput
			volume.VolumePerformanceGroup.Iops = newIops
			if err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateVolumePerformanceGroupInDB, nil, volume.VolumePerformanceGroup); err != nil {
				return nil, ConvertToVSAError(err)
			}
		} else {
			// Non-autogen VPG: create a new auto-gen QoS + VPG and re-point the volume.
			// The existing VPG is left untouched (other volumes may share it).

			oldVPG := &datamodel.VolumePerformanceGroup{
				BaseModel:        volume.VolumePerformanceGroup.BaseModel,
				Name:             volume.VolumePerformanceGroup.Name,
				PoolID:           volume.VolumePerformanceGroup.PoolID,
				AllocationType:   volume.VolumePerformanceGroup.AllocationType,
				IsAutoGen:        volume.VolumePerformanceGroup.IsAutoGen,
				ThroughputMibps:  volume.VolumePerformanceGroup.ThroughputMibps,
				Iops:             volume.VolumePerformanceGroup.Iops,
				OntapQosPolicyID: volume.VolumePerformanceGroup.OntapQosPolicyID,
			}

			if err = orchestrator.RunActivity(orchCtx, updateActivity.UnassignQoSPolicyFromVolume, nil, volume, node); err != nil {
				return nil, ConvertToVSAError(err)
			}

			var newQosPolicy *vsa.QoSGroupPolicyResponse
			if err = orchestrator.RunActivity(orchCtx, updateActivity.CreateAutoGeneratedQoSPolicyGroupForVolume, &newQosPolicy, volume, newThroughput, newIops, node); err != nil {
				return nil, ConvertToVSAError(err)
			}

			if err = orchestrator.RunActivity(orchCtx, updateActivity.AssignQoSPolicyToVolume, nil, volume, newQosPolicy.Name, node); err != nil {
				return nil, ConvertToVSAError(err)
			}

			vpgActivity := &activities.VolumePerformanceGroupActivity{}
			newAutoGenVPG := &datamodel.VolumePerformanceGroup{
				Name:             newQosPolicy.Name,
				PoolID:           volume.PoolID,
				AllocationType:   models.AllocationTypePerVolume,
				IsAutoGen:        true,
				ThroughputMibps:  newThroughput,
				Iops:             newIops,
				OntapQosPolicyID: newQosPolicy.UUID,
			}
			var createdVPG *datamodel.VolumePerformanceGroup
			if err = orchestrator.RunActivity(orchCtx, vpgActivity.CreateVPGInDB, &createdVPG, newAutoGenVPG); err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Register rollback immediately so the new VPG is cleaned up if any later step fails.
			rollbackManager.AddActivity(vpgActivity.HardDeleteVPGInDB, createdVPG)
			rollbackManager.AddActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, volume.UUID, oldVPG)
			if oldVPG.OntapQosPolicyID != "" {
				rollbackManager.AddActivity(updateActivity.AssignQoSPolicyToVolume, volume, oldVPG.Name, node)
			}

			if err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateVolumePerformanceGroupInDBForVolume, nil, volume.UUID, createdVPG); err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Update in-memory state so downstream activities see the correct VPG.
			volume.VolumePerformanceGroup = createdVPG
			volume.VolumePerformanceGroupID = sql.NullInt64{Int64: createdVPG.ID, Valid: true}
		}
	}
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded after QoS update")
		return nil, ConvertToVSAError(err)
	}

	// If current VPG doesnt match the provided VPGId, clean up the old VPG
	if volume.VolumePerformanceGroupID.Valid &&
		volume.VolumePerformanceGroup != nil &&
		params.VolumePerformanceGroupId != nil &&
		*params.VolumePerformanceGroupId != volume.VolumePerformanceGroup.UUID {
		oldVPG := volume.VolumePerformanceGroup
		oldQosPolicyID := oldVPG.OntapQosPolicyID

		// Remove the volume from the old qosPolicyGroup
		if err = orchestrator.RunActivity(orchCtx, updateActivity.UnassignQoSPolicyFromVolume, nil, volume, node); err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Find the VPG by the provided VPGId
		var newVPG *datamodel.VolumePerformanceGroup
		if err = orchestrator.RunActivity(orchCtx, (&activities.VolumePerformanceGroupActivity{}).GetVolumePerformanceGroupByUUID, &newVPG, *params.VolumePerformanceGroupId); err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Validate that the new VPG belongs to the same pool as the volume
		if newVPG.PoolID != volume.PoolID {
			return nil, ConvertToVSAError(fmt.Errorf("VolumePerformanceGroup %s does not belong to the same pool as volume %s", newVPG.UUID, volume.UUID))
		}

		// Get the new VPG's qosPolicyGroup
		if newVPG.OntapQosPolicyID == "" {
			return nil, ConvertToVSAError(fmt.Errorf("VolumePerformanceGroup %s has no OntapQosPolicyID", newVPG.UUID))
		}

		var newQosPolicy *vsa.QoSGroupPolicyResponse
		if err = orchestrator.RunActivity(orchCtx, updateActivity.FindQoSGroupPolicyForVolume, &newQosPolicy, newVPG.Name, volume.Svm.Name, node); err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Assign the volume to the VPG's qosPolicyGroup
		if err = orchestrator.RunActivity(orchCtx, updateActivity.AssignQoSPolicyToVolume, nil, volume, newQosPolicy.Name, node); err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Update the volume in the db to point at the new VPG
		if err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateVolumePerformanceGroupInDBForVolume, nil, volume.UUID, newVPG); err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Update the volume object in memory to reflect the new VPG
		volume.VolumePerformanceGroup = newVPG
		volume.VolumePerformanceGroupID = sql.NullInt64{Int64: newVPG.ID, Valid: true}

		// Add rollback activities to restore old state
		rollbackManager.AddActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, volume.UUID, oldVPG)
		if oldQosPolicyID != "" {
			rollbackManager.AddActivity(updateActivity.AssignQoSPolicyToVolume, volume, oldVPG.Name, node)
		}
	}
	if orchCtx.OperationBudgetExceeded() {
		err = fmt.Errorf("operation budget exceeded after VPG reassignment")
		return nil, ConvertToVSAError(err)
	}

	// Final DB persist: operation-budget check cleared by design — all forward mutations are
	// done and we must complete the job regardless of elapsed time.
	orchCtx = orchCtx.ClearOperationBudgetCheck()
	err = orchestrator.RunActivity(orchCtx, updateActivity.UpdateVolumeInDB, nil, volume, params)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if backupVaultSwitched {
		backupActivity := &activities.BackupActivity{}

		if recalcErr := orchestrator.RunActivity(orchCtx, backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity, nil, volume.UUID); recalcErr != nil {
			return nil, ConvertToVSAError(recalcErr)
		}
	}

	// Update BackupMetadata labels if an entry exists for this volume
	backupActivity := &activities.BackupActivity{}
	metadataErr := orchestrator.RunActivity(orchCtx, backupActivity.UpdateBackupMetadataIfExistsActivity, nil, volume)
	if metadataErr != nil {
		// Log the error but don't fail the entire volume update workflow
		log.Error("Failed to update BackupMetadata for volume", "uuid", volume.UUID, "error", metadataErr)
	}

	return nil, ConvertToVSAError(err)
}

func sanitizeUpdateParamsForFlexCache(params *common.UpdateVolumeParams, volume *datamodel.Volume) {
	// FlexCache volumes don't support snapshot directory and snap reserve updates.
	if volume.CacheParameters != nil {
		params.SnapshotDirectoryAccess = nil
		params.SnapReserve = nil
	}
}

// updateOrAddBlockDevice updates existing BlockDevice or adds new one to params
func updateOrAddBlockDevice(
	params *common.UpdateVolumeParams,
	blockDevice *common.BlockDevice,
) {
	// Nil checks for safety
	if params == nil || blockDevice == nil {
		return
	}

	// Check if BlockDevice with same name already exists
	for i, existingDevice := range params.BlockDevices {
		if existingDevice.Name == blockDevice.Name {
			// Replace existing BlockDevice
			paramsHostGroups := params.BlockDevices[i].HostGroups
			params.BlockDevices[i] = blockDevice
			params.BlockDevices[i].HostGroups = paramsHostGroups
			return
		}
	}
	// BlockDevice not found, append new one
	params.BlockDevices = append(params.BlockDevices, blockDevice)
}

func _isUpdateFlexCacheRequired(existingVolume *datamodel.Volume, params *common.UpdateVolumeParams) bool {
	// TODO: Refactor this and _applyFlexCacheUpdateParams to a common location.
	// No incoming FlexCache intent
	if params == nil || params.CacheParameters == nil || params.CacheParameters.CacheConfig == nil {
		return false
	}
	inCfg := params.CacheParameters.CacheConfig

	// Not a FlexCache volume (cannot add via update)
	if existingVolume == nil || existingVolume.CacheParameters == nil {
		return false
	}

	// Existing FlexCache params but missing config: treat as add
	if existingVolume.CacheParameters.CacheConfig == nil {
		return true
	}
	exCfg := existingVolume.CacheParameters.CacheConfig

	// Compare fields, ignoring nils in input
	changedBool := func(in, ex *bool) bool {
		if in == nil {
			return false
		}
		return ex == nil || *in != *ex
	}
	changedInt := func(in, ex *int16) bool {
		if in == nil {
			return false
		}
		return ex == nil || *in != *ex
	}

	if changedBool(inCfg.WritebackEnabled, exCfg.WritebackEnabled) ||
		changedBool(inCfg.AtimeScrubEnabled, exCfg.AtimeScrubEnabled) ||
		changedInt(inCfg.AtimeScrubDays, exCfg.AtimeScrubDays) ||
		changedBool(inCfg.CifsChangeNotifyEnabled, exCfg.CifsChangeNotifyEnabled) {
		return true
	}

	return false
}

func _isUpdateFlexCachePrepopulateRequired(existingVolume *datamodel.Volume, params *common.UpdateVolumeParams) bool {
	if params == nil || params.CacheParameters == nil || params.CacheParameters.CacheConfig == nil {
		return false
	}
	if existingVolume == nil || existingVolume.CacheParameters == nil {
		return false
	}

	inCfg := params.CacheParameters.CacheConfig
	exCfg := existingVolume.CacheParameters.CacheConfig

	if inCfg.CachePrePopulate != nil {
		if exCfg == nil || exCfg.CachePrePopulate == nil {
			return true
		}

		changedBool := func(in, ex *bool) bool {
			if in == nil {
				return false
			}
			return ex == nil || *in != *ex
		}

		sliceChanged := func(in, ex []string) bool {
			if in == nil {
				return false
			}
			if len(in) == 0 {
				return len(ex) != 0
			}

			// Set comparison (order ignored, duplicates not distinguished).
			inSet := make(map[string]struct{}, len(in))
			for _, v := range in {
				inSet[v] = struct{}{}
			}
			exSet := make(map[string]struct{}, len(ex))
			for _, v := range ex {
				exSet[v] = struct{}{}
			}

			if len(inSet) != len(exSet) {
				return true
			}
			for v := range inSet {
				if _, ok := exSet[v]; !ok {
					return true
				}
			}
			return false
		}

		return sliceChanged(inCfg.CachePrePopulate.PathList, exCfg.CachePrePopulate.PathList) ||
			sliceChanged(inCfg.CachePrePopulate.ExcludePathList, exCfg.CachePrePopulate.ExcludePathList) ||
			changedBool(inCfg.CachePrePopulate.Recursion, exCfg.CachePrePopulate.Recursion)
	}
	return false
}

func getUpdateParamsForRollback(volResponse *vsa.VolumeResponse, existingVolume *datamodel.Volume) *common.UpdateVolumeParams {
	params := &common.UpdateVolumeParams{
		// Set the necessary parameters for rolling back the volume update
		QuotaInBytes: volResponse.Size,
	}

	// Set AutoTieringPolicy if it exists
	if existingVolume.AutoTieringPolicy != nil {
		params.AutoTieringPolicy = &common.AutoTieringPolicy{
			AutoTieringEnabled:   existingVolume.AutoTieringEnabled,
			CoolingThresholdDays: existingVolume.AutoTieringPolicy.CoolingThresholdDays,
			TieringPolicy:        existingVolume.AutoTieringPolicy.TieringPolicy,
			RetrievalPolicy:      existingVolume.AutoTieringPolicy.RetrievalPolicy,
		}
	}

	if volResponse.SnapshotPolicyName != "" {
		params.SnapshotPolicy = &models.SnapshotPolicy{
			Name: volResponse.SnapshotPolicyName,
		}
	}

	if existingVolume.CacheParameters != nil {
		params.CacheParameters = convertCacheParameters(existingVolume.CacheParameters)
	}

	return params
}

func _convertCacheParameters(src *datamodel.CacheParameters) *models.CacheParameters {
	if src == nil {
		return nil
	}

	dst := &models.CacheParameters{
		PeerVolumeName:        src.PeerVolumeName,
		PeerClusterName:       src.PeerClusterName,
		PeerSvmName:           src.PeerSvmName,
		PeerIPAddresses:       src.PeerIpAddresses, // ensure field name matches models
		CacheState:            src.CacheState,
		CacheStateDetails:     src.CacheStateDetails,
		CacheStateDetailsCode: src.CacheStateDetailsCode,
	}

	if src.CacheConfig != nil {
		cc := &models.CacheConfig{
			WritebackEnabled:        src.CacheConfig.WritebackEnabled,
			AtimeScrubEnabled:       src.CacheConfig.AtimeScrubEnabled,
			AtimeScrubDays:          src.CacheConfig.AtimeScrubDays,
			CifsChangeNotifyEnabled: src.CacheConfig.CifsChangeNotifyEnabled,
		}
		if src.CacheConfig.CachePrePopulate != nil {
			cc.CachePrePopulate = &models.CachePrePopulate{
				PathList:        src.CacheConfig.CachePrePopulate.PathList,
				ExcludePathList: src.CacheConfig.CachePrePopulate.ExcludePathList,
				Recursion:       src.CacheConfig.CachePrePopulate.Recursion,
			}
		}
		dst.CacheConfig = cc
	}

	return dst
}

// getUpdatedExportPolicy converts models.ExportPolicy to datamodel.ExportPolicy
func getUpdatedExportPolicy(updatePolicy *models.ExportPolicy) *datamodel.ExportPolicy {
	if updatePolicy == nil {
		return nil
	}

	exportPolicy := &datamodel.ExportPolicy{
		ExportPolicyName: updatePolicy.ExportPolicyName,
		ExportRules:      make([]*datamodel.ExportRule, 0, len(updatePolicy.ExportRules)),
	}

	for _, rule := range updatePolicy.ExportRules {
		dataRule := &datamodel.ExportRule{
			AllowedClients:      rule.AllowedClients,
			AnonymousUser:       rule.AnonymousUser,
			Index:               rule.Index,
			ChownMode:           rule.ChownMode,
			AccessType:          rule.AccessType,
			CIFS:                rule.CIFS,
			NFSv3:               rule.NFSv3,
			NFSv4:               rule.NFSv4,
			S3:                  rule.S3,
			UnixReadOnly:        rule.UnixReadOnly,
			UnixReadWrite:       rule.UnixReadWrite,
			Kerberos5ReadOnly:   rule.Kerberos5ReadOnly,
			Kerberos5ReadWrite:  rule.Kerberos5ReadWrite,
			Kerberos5iReadOnly:  rule.Kerberos5iReadOnly,
			Kerberos5iReadWrite: rule.Kerberos5iReadWrite,
			Kerberos5pReadOnly:  rule.Kerberos5pReadOnly,
			Kerberos5pReadWrite: rule.Kerberos5pReadWrite,
			Superuser:           rule.Superuser,
			AllSquash:           rule.AllSquash,
			AnonUid:             rule.AnonUid,
		}
		exportPolicy.ExportRules = append(exportPolicy.ExportRules, dataRule)
	}

	return exportPolicy
}
