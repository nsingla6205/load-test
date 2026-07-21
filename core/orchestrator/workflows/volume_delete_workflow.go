package workflows

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	volumeDeleteJobsRetryMaxAttempts = env.GetInt("REPLICATION_JOBS_RETRY_MAX_ATTEMPTS", 10)
	enableSmb                        = env.GetBool("ENABLE_SMB", false)
	enableQuotaRule                  = env.GetBool("ENABLE_QUOTA_RULE", true)
	enableVolDeleteProtection        = env.GetBool("VOL_DEL_PROTECTION_ENABLED", true)
)

const (
	restoreVolumeStateFromJobChangeID = "restoreVolumeStateFromJob"
	restoreVolumeStateFromJobVersion  = workflow.Version(1)
)

type volumeDeleteWorkflow struct {
	// add fields needed for volume workflow
	BaseWorkflow
}

// Enforcing the WorkflowInterface on volumeDeleteWorkflow
var _ WorkflowInterface = &volumeDeleteWorkflow{}

// ResolveVolumeReference loads UUID inputs while preserving legacy volume-object inputs for replay.
func ResolveVolumeReference(ctx workflow.Context, volumeReference interface{}) (*datamodel.Volume, error) {
	switch input := volumeReference.(type) {
	case string:
		ctx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Duration(volumeStartToCloseTimeoutSec) * time.Second,
			HeartbeatTimeout:    time.Duration(dbHeartbeatTimeoutSec) * time.Second,
		})
		var volume *datamodel.Volume
		err := workflow.ExecuteActivity(ctx, (&activities.VolumeCreateActivity{}).GetVolumeByVolumeID, input).Get(ctx, &volume)
		return volume, err
	case *datamodel.Volume:
		return input, nil
	case map[string]interface{}:
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("marshal legacy volume input: %w", err)
		}
		volume := &datamodel.Volume{}
		if err := json.Unmarshal(encoded, volume); err != nil {
			return nil, fmt.Errorf("unmarshal legacy volume input: %w", err)
		}
		return volume, nil
	default:
		return nil, fmt.Errorf("invalid volume input type %T", volumeReference)
	}
}

// MarkVolumeResolutionFailure records a failed UUID lookup against the current delete job.
func MarkVolumeResolutionFailure(ctx workflow.Context, resolveErr error) {
	baseWf := &BaseWorkflow{ID: workflow.GetInfo(ctx).WorkflowExecution.ID, Status: WorkflowStatusFailed}
	if err := baseWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), resolveErr); err != nil {
		workflow.GetLogger(ctx).Error("Failed to mark job ERROR after volume resolution failure", "error", err)
	}
}

// DeleteVolumeWorkflow processes a volume delete request.
func DeleteVolumeWorkflow(ctx workflow.Context, volumeReference interface{}) error {
	log := workflow.GetLogger(ctx)
	volume, err := ResolveVolumeReference(ctx, volumeReference)
	if err != nil {
		MarkVolumeResolutionFailure(ctx, err)
		log.Error("Failed to resolve volume for DeleteVolumeWorkflow", "error", err)
		return err
	}

	volumeWf := new(volumeDeleteWorkflow)
	if err = volumeWf.Setup(ctx, volume); err != nil {
		log.Error("Volume delete workflow setup executed with error", "error", err)
		return err
	}
	if err = volumeWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return err
	}

	volumeWf.Status = WorkflowStatusRunning
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Error("Failed to update job status to Processing for DeleteVolumeWorkflow", "error", err)
		return err
	}

	_, customErr := volumeWf.Run(ctx, volume)
	if customErr != nil {
		log.Error("DeleteVolumeWorkflow completed with error", "error", customErr)
		volumeWf.Status = WorkflowStatusFailed
		err2 := volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			log.Error("Failed to update job status to Done with err for DeleteVolumeWorkflow", "error", err)
			return err2
		}
		return customErr
	}

	volumeWf.Status = WorkflowStatusCompleted
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Error("Failed to update job status to Done for DeleteVolumeWorkflow", "error", err)
	}
	return err
}

func (wf *volumeDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
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

// shouldUpdateVolumeStateToError checks if the error should trigger updating the volume state to error
// Some errors are legitimate business logic errors that should not mark the volume as having a deletion error
func shouldUpdateVolumeStateToError(err error) bool {
	// Check for specific VCP errors that should not update volume state to error
	convertedError := ConvertToVSAError(err)
	var customError *vsaerrors.CustomError
	if vsaerrors.As(convertedError, &customError) {
		// ErrDeleteVolumeWhenInSplitState (7005) - volume has clones/replication
		switch customError.TrackingID {
		case vsaerrors.ErrDeleteVolumeWhenInSplitState,
			vsaerrors.ErrDeleteVolumeRestrictedAction:
			return false
		}
	}

	// For all other errors, update the volume state to error
	return true
}

// SmbTeardownWorkflow consolidates all SMB teardown activities into a single workflow.
// It determines the SMB teardown context, deletes CIFS server and DNS records if unused,
// and unsets SVM Active Directory if needed.
func SmbTeardownWorkflow(ctx workflow.Context, volume *datamodel.Volume, node *models.Node) error {
	deleteActivity := &activities.VolumeDeleteActivity{}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return ConvertToVSAError(err)
	}
	options := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(volumeStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	var smbTeardownCtx *activities.SmbTeardownContext
	err = workflow.ExecuteActivity(ctx, deleteActivity.DetermineSmbTeardownContext, &volume, &node).Get(ctx, &smbTeardownCtx)
	if err != nil {
		return ConvertToVSAError(err)
	}
	if smbTeardownCtx == nil {
		smbTeardownCtx = &activities.SmbTeardownContext{}
	}

	err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteCifsServerIfUnused, smbTeardownCtx, &node).Get(ctx, nil)
	if err != nil {
		return ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteDnsRecordIfUnused, smbTeardownCtx, &node).Get(ctx, nil)
	if err != nil {
		return ConvertToVSAError(err)
	}

	if smbTeardownCtx.ShouldDelete {
		var dbSvm *datamodel.Svm
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetSVM, smbTeardownCtx.PoolID).Get(ctx, &dbSvm)
		if err != nil {
			return ConvertToVSAError(err)
		}
		if dbSvm != nil {
			err = workflow.ExecuteActivity(ctx, activities.CommonActivities.UnsetSvmActiveDirectory, dbSvm).Get(ctx, nil)
			if err != nil {
				return ConvertToVSAError(err)
			}
		}
	}

	return nil
}

func (wf *volumeDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	volume := args[0].(*datamodel.Volume)
	deleteActivity := &activities.VolumeDeleteActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	// Use LV-specific timeout for large capacity volumes
	startToCloseTimeout := getVolumeStartToCloseTimeout(volume)
	options := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(startToCloseTimeout) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)
	ao1 := options
	ao1.RetryPolicy.MaximumAttempts = int32(volumeDeleteJobsRetryMaxAttempts)
	ao1.RetryPolicy.NonRetryableErrorTypes = append(ao1.RetryPolicy.NonRetryableErrorTypes, vsaerrors.DeleteVolumeInONTAPError)
	ctx1 := workflow.WithActivityOptions(ctx, ao1)

	// Handle cancellation only if volume is in CREATING state
	cancellationActivity := &activities.CancellationActivity{}
	commonActivity := &activities.CommonActivities{}
	poolActivity := &activities.PoolActivity{}
	ackTimeout, forceTimeout := common.GetCancellationTimeouts("VOLUME")
	// Determine the correct create job type based on volume type
	createJobType := datamodel.JobTypeCreateVolume
	if volume.LargeVolumeAttributes != nil && volume.LargeVolumeAttributes.LargeCapacity {
		createJobType = datamodel.JobTypeCreateLargeVolume
	}
	if cancelErr := common.HandleCancellationForCreatingResource(ctx, log,
		common.HandleCancellationForCreatingResourceParams{
			ResourceUUID:               volume.UUID,
			ResourceState:              volume.State,
			CreateJobType:              createJobType,
			SignalName:                 CancelVolumeSignalName,
			CancellationAckTimeout:     ackTimeout,
			ForceTerminationAckTimeout: forceTimeout,
		},
		poolActivity.GetCreateJobByResourceUUID,
		cancellationActivity,
		commonActivity,
	); cancelErr != nil {
		log.Warn("Error handling cancellation, proceeding with deletion", "error", cancelErr)
	}

	rollbackManager := common.NewRollbackManager()
	defer func() {
		if err != nil {
			if shouldUpdateVolumeStateToError(err) {
				err2 := workflow.ExecuteActivity(ctx, activities.VolumeCreateActivity.UpdateVolumeStateInDB, volume.UUID, datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails).Get(ctx, nil)
				if err2 != nil {
					log.Error("Failed to update volume state in DB to error", "error", err2)
				}
			} else {
				previousState, previousStateDetails := volume.State, volume.StateDetails
				restoreFromJobVersion := workflow.GetVersion(ctx, restoreVolumeStateFromJobChangeID,
					workflow.DefaultVersion, restoreVolumeStateFromJobVersion)
				if restoreFromJobVersion != workflow.DefaultVersion {
					var job *datamodel.Job
					if jobErr := workflow.ExecuteActivity(ctx, activities.CommonActivities.GetJob, wf.ID).Get(ctx, &job); jobErr != nil {
						log.Error("Failed to load delete job for state restore", "error", jobErr)
					} else if job != nil && job.JobAttributes != nil && job.JobAttributes.PreviousState != "" {
						previousState = job.JobAttributes.PreviousState
						previousStateDetails = job.JobAttributes.PreviousStateDetails
					}
				}
				err2 := workflow.ExecuteActivity(ctx, activities.VolumeCreateActivity.UpdateVolumeStateInDB, volume.UUID, previousState, previousStateDetails).Get(ctx, nil)
				if err2 != nil {
					log.Error("Failed to restore volume state to previous state", "error", err2)
				}
			}
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	// For volumes in CREATING state, check for the existence of partially created resources before accessing them
	hasExternalUUID := volume.VolumeAttributes != nil && volume.VolumeAttributes.ExternalUUID != ""
	hasFileProperties := volume.VolumeAttributes != nil && volume.VolumeAttributes.FileProperties != nil
	hasBlockDevices := volume.VolumeAttributes != nil && volume.VolumeAttributes.BlockDevices != nil && len(*volume.VolumeAttributes.BlockDevices) > 0
	hasBlockProperties := volume.VolumeAttributes != nil && volume.VolumeAttributes.BlockProperties != nil && len(volume.VolumeAttributes.BlockProperties.HostGroupDetails) > 0

	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, &volume.Pool.ID).Get(ctx, &dbNodes)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   volume.Pool.DeploymentName,
		OntapCredentials: volume.Pool.PoolCredentials,
	})

	if enableVolDeleteProtection {
		err = workflow.ExecuteActivity(ctx, deleteActivity.EnforceDeleteProtection, volume, node).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	// Only perform ONTAP operations if volume was at least partially created
	if hasExternalUUID {
		var ontapAsyncResponse *vsa.OntapAsyncResponse
		err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteSnapmirrorInONTAP, &volume, &node).Get(ctx, &ontapAsyncResponse)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		err = WaitForONTAPJob(ctx, ontapAsyncResponse, node, time.Minute*10)
		if err != nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to delete snapmirror in ontap: %w", err))
		}

		// Delete CIFS share before volume delete when SMB volume has AD and creation token.
		err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteCifsShareIfSMB, &volume, &node).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// DeleteVolume polls the job internally, so we just wait for the activity to complete
		err = workflow.ExecuteActivity(ctx1, deleteActivity.DeleteVolumeInONTAP, volume.VolumeAttributes.ExternalUUID, volume.Name, node).Get(ctx1, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		if hasFileProperties && volume.VolumeAttributes.FileProperties.ExportPolicy != nil &&
			len(volume.VolumeAttributes.FileProperties.ExportPolicy.ExportRules) > 0 {
			err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteExportPolicy, &volume, &node).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		}

		if hasBlockDevices {
			err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteIgroups, volume, node).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		} else if hasBlockProperties {
			err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteIgroupsFromBlockProperties, volume, node).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		}

		SnapshotPolicyName := getSnapshotPolicyName(volume)
		err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteSnapshotPolicyInONTAP, SnapshotPolicyName, &node).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteVolumeAssociatedSnapshots, volume.ID).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Only perform LDAP and SMB cleanup if volume was at least partially created
	if hasExternalUUID {
		if enableLdap && volume.Pool.PoolAttributes != nil && volume.Pool.PoolAttributes.LdapEnabled {
			var isLastFilesVolume bool
			err = workflow.ExecuteActivity(ctx, deleteActivity.DetermineIfVolumeIsLastFilesVolume, volume, node).Get(ctx, &isLastFilesVolume)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
			if isLastFilesVolume {
				err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteLDAPConfiguration, volume, node).Get(ctx, nil)
				if err != nil {
					return nil, ConvertToVSAError(err)
				}
			}
		}

		if enableSmb {
			err = workflow.ExecuteChildWorkflow(ctx, SmbTeardownWorkflow, volume, node).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		}

		if volume.VolumeAttributes != nil &&
			((utils.IsNFSProtocols(volume.VolumeAttributes.Protocols) && volume.Pool.PoolAttributes != nil && volume.Pool.PoolAttributes.LdapEnabled) ||
				utils.IsSMBProtocols(volume.VolumeAttributes.Protocols)) {
			if volume.Pool.ActiveDirectory == nil {
				return nil, ConvertToVSAError(fmt.Errorf("volume %s has no active directory", volume.Name))
			}
			var dbSvms []*datamodel.Svm
			err = workflow.ExecuteActivity(ctx, active_directory_activities.ActiveDirectoryActivity.GetSvmsForAd, volume.Pool.ActiveDirectory.ID).Get(ctx, &dbSvms)

			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			if dbSvms != nil && len(dbSvms) == 0 {
				err = workflow.ExecuteActivity(ctx, active_directory_activities.ActiveDirectoryActivity.UpdateActiveDirectoryState, volume.Pool.ActiveDirectory.UUID, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Get(ctx, nil)
				if err != nil {
					return nil, ConvertToVSAError(err)
				}
			}
		}
	}

	if enableQuotaRule && volume.VolumeAttributes.FileProperties != nil {
		err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteAssociatedQuotaRules, volume.ID).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteVolume, &volume).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Delete all billing ledger entries for the volume after successful deletion
	billingActivity := &activities.BillingActivity{}
	err = workflow.ExecuteActivity(ctx, billingActivity.DeleteVolumeBillingLedger, volume).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Clean up auto-generated VPG if this was the last volume using it (only if MQOS is enabled)
	if env.GetBool("ENABLE_MQOS", true) && volume.VolumePerformanceGroupID.Valid {
		err = workflow.ExecuteActivity(ctx, deleteActivity.CleanupAutoGeneratedVPG, volume.VolumePerformanceGroupID.Int64, &node).Get(ctx, nil)
		if err != nil {
			// Log error but don't fail the workflow - VPG cleanup is best effort
			log.Error("Failed to cleanup auto-generated VPG", "error", err)
		}
	}

	return nil, ConvertToVSAError(err)
}
