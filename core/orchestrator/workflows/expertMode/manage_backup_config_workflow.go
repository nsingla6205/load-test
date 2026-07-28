package expertMode

import (
	"fmt"
	"strconv"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type manageBackupConfigWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &manageBackupConfigWorkflow{}

// ManageBackupConfigWorkflow attaches a backup vault and optional backup policy to an expert mode volume.
// It creates a GCS bucket, sets up CRB/cross-region permissions, creates a Temporal scheduler for
// policy-based scheduled backups, and persists the backup config on the volume.
func ManageBackupConfigWorkflow(ctx workflow.Context, volume *datamodel.ExpertModeVolumes, params *commonparams.ManageBackupConfigForExpertModeVolumeParams) error {
	wf := new(manageBackupConfigWorkflow)
	if err := wf.Setup(ctx, volume); err != nil {
		return err
	}
	if err := wf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return workflows.ConvertToVSAError(err)
	}
	wf.Status = workflows.WorkflowStatusRunning
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil); err != nil {
		wf.Status = workflows.WorkflowStatusFailed
		log := util.GetLogger(ctx)
		log.Errorf("Failed to update job status to PROCESSING: %v", err)
		jobErr := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		if jobErr != nil {
			log.Errorf("Failed to update job status to ERROR: %v", jobErr)
		}
		return err
	}

	_, cerr := wf.Run(ctx, volume, params)
	if cerr != nil {
		wf.Status = workflows.WorkflowStatusFailed
		log := util.GetLogger(ctx)
		jobErr := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), cerr)
		if jobErr != nil {
			log.Errorf("Failed to update job status to ERROR: %v", jobErr)
		}
		return cerr
	}

	wf.Status = workflows.WorkflowStatusCompleted
	return wf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
}

func (wf *manageBackupConfigWorkflow) Setup(ctx workflow.Context, input interface{}) error {
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

func (wf *manageBackupConfigWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := util.GetLogger(ctx)
	volume := args[0].(*datamodel.ExpertModeVolumes)
	params := args[1].(*commonparams.ManageBackupConfigForExpertModeVolumeParams)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// StartToCloseTimeout is set to 3 minutes to accommodate slow GCP API calls (bucket
	// creation, IAM bindings). HeartbeatTimeout is omitted: none of the activities in this
	// workflow call activity.RecordHeartbeat, so a heartbeat timeout would never fire.
	// Once heartbeats are added to GCP-heavy activities, switch to a smaller HeartbeatTimeout
	// (e.g. 60s) as the liveness detector and increase StartToCloseTimeout as a safety net.
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	updateActivity := &activities.VolumeUpdateActivity{}
	expertModeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
	backupConfigActivity := &expertmodeactivities.ExpertModeBackupConfigActivity{}

	// Defer: on failure only, restore volume to READY (mirrors volume_update_workflow).
	defer func() {
		if err != nil {
			if restoreErr := workflow.ExecuteActivity(ctx, expertModeActivity.UpdateExpertModeVolumeStateInDB,
				volume.UUID, datamodel.LifeCycleStateAvailable).Get(ctx, nil); restoreErr != nil {
				log.Errorf("Failed to restore expert mode volume %s state to AVAILABLE after failure: %v", volume.UUID, restoreErr)
			}
		}
	}()

	// Build a minimal *datamodel.Volume from the expert mode volume so we can reuse
	// the existing backup-vault activities that operate on *datamodel.Volume.
	volumeForActivities := buildVolumeFromExpertMode(volume, params)

	// ── Step 1: Obtain an auth token (non-local only) ─────────────────────────
	if !env.IsLocalEnv() {
		var token string
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetAuthJWTToken, params.AccountName).Get(ctx, &token)
		if err != nil {
			log.Errorf("Failed to get auth token for account %s: %v", params.AccountName, err)
			return nil, workflows.ConvertToVSAError(err)
		}
		ctx = workflow.WithValue(ctx, middleware.AuthorizationToken, token)
	}

	// ── Step 1b: Vault switch – tear down SnapMirror for old vault ──────────
	// When the flag is on and the volume already has a different vault attached,
	// delete the SnapMirror relationship and clean up the ONTAP-side backup
	// snapshot before provisioning the new vault.
	isVaultSwitch := utils.EnableBackupVaultSwitching &&
		params.BackupVaultID != nil && *params.BackupVaultID != "" &&
		volume.BackupConfig != nil && volume.BackupConfig.BackupVaultID != "" &&
		volume.BackupConfig.BackupVaultID != *params.BackupVaultID
	if isVaultSwitch {
		oldVaultID := volume.BackupConfig.BackupVaultID
		log.Infof("Vault switch detected for expert mode volume %s (old=%s new=%s); tearing down SnapMirror",
			volume.UUID, oldVaultID, *params.BackupVaultID)

		commonActivity := &activities.CommonActivities{}
		deleteActivity := &activities.VolumeDeleteActivity{}
		backupActivity := &activities.BackupActivity{}

		var dbNodes []*datamodel.Node
		err = workflow.ExecuteActivity(ctx, commonActivity.GetNode, volume.Pool.ID).Get(ctx, &dbNodes)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
			Nodes:            dbNodes,
			DeploymentName:   volume.Pool.DeploymentName,
			OntapCredentials: volume.Pool.PoolCredentials,
		})

		oldVolumeForActivities := buildVolumeFromExpertMode(volume, &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			BackupVaultID: &oldVaultID,
			Region:        params.Region,
		})

		var ontapAsyncResponse *vsa.OntapAsyncResponse
		err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteSnapmirrorInONTAP, oldVolumeForActivities, &node).Get(ctx, &ontapAsyncResponse)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		err = workflows.WaitForONTAPJob(ctx, ontapAsyncResponse, node, time.Minute*10)
		if err != nil {
			return nil, workflows.ConvertToVSAError(fmt.Errorf("failed to delete snapmirror during vault switch: %w", err))
		}

		var oldBackupVault *datamodel.BackupVault
		err = workflow.ExecuteActivity(ctx, backupActivity.GetBackupVault, oldVaultID).Get(ctx, &oldBackupVault)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		var latestBackup *datamodel.Backup
		err = workflow.ExecuteActivity(ctx, backupActivity.GetLatestBackupByVolumeAndVault,
			volume.ExternalUUID, oldBackupVault.ID).Get(ctx, &latestBackup)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		if latestBackup != nil && latestBackup.Attributes != nil {
			err = workflow.ExecuteActivity(ctx, backupActivity.DeleteSnapshotForBackup,
				&node, latestBackup.Attributes.SnapshotID, volume.ExternalUUID,
				latestBackup.Attributes.UseExistingSnapshot).Get(ctx, nil)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}
		}

		log.Infof("Vault switch SnapMirror teardown complete for expert mode volume %s", volume.UUID)
	}

	// Steps 2-6 only run when a real vault UUID is being set. When BackupVaultID is nil
	// (no-op) or &"" (detach) there is nothing vault-specific to provision.
	if params.BackupVaultID != nil && *params.BackupVaultID != "" {
		// ── Step 2: Verify the backup vault exists in VCP ─────────────────────
		var backupVault *datamodel.BackupVault
		err = workflow.ExecuteActivity(ctx, updateActivity.CheckBackupVaultExistInVCP, volumeForActivities, &params.Region).Get(ctx, &backupVault)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		backupRegion := params.Region
		if backupVault.BackupVaultType == activities.CrossRegionBackupType && backupVault.BackupRegionName != nil && *backupVault.BackupRegionName != "" {
			backupRegion = *backupVault.BackupRegionName
		}

		// ── Step 3: Resolve tenancy details ──────────────────────────────────
		tenancyDetails := &commonparams.TenancyInfo{}
		if backupVault.ServiceType != activities.GCBDRServiceType {
			err = workflow.ExecuteActivity(ctx, updateActivity.FindTenancyDetails,
				volumeForActivities.VolumeAttributes.VendorSubnetID, params.AccountName, backupRegion).Get(ctx, &tenancyDetails)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}
		} else {
			if len(backupVault.BucketDetails) > 0 {
				tenancyDetails.RegionalTenantProject = backupVault.BucketDetails[0].TenantProjectNumber
			} else {
				log.Errorf("GCBDR vault %s has no bucket details with tenant project", backupVault.UUID)
				return nil, workflows.ConvertToVSAError(fmt.Errorf("GCBDR vault has no tenant project information"))
			}
		}

		// ── Step 4: Check for existing bucket – create if absent ─────────────
		var bucketDetails *commonparams.BucketDetails
		err = workflow.ExecuteActivity(ctx, updateActivity.CheckBucketResourceName, volumeForActivities).Get(ctx, &bucketDetails)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		if needsExpertModeBackupBucketProvisioning(bucketDetails, volumeForActivities.Pool) {
			resourceName := &commonparams.ResourceNames{}
			err = workflow.ExecuteActivity(ctx, updateActivity.GenerateResourceNamesForBackupVault,
				volumeForActivities, tenancyDetails, params.Region).Get(ctx, &resourceName)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}

			var kmsGrant *string
			if !nillable.IsNilOrEmpty(params.KmsGrant) {
				kmsGrant = params.KmsGrant
			}

			// FLEX and dedicated-capacity pools share the same attach flow; dedicated pools
			// additionally provision backup SA, HMAC keys, Secret Manager secret, and persist
			// those fields on the vault bucket details before the common post-create steps.
			if isDedicatedCapacityExpertModePool(volumeForActivities.Pool) {
				bucketDetails, err = provisionHmacForDedicatedCapacityBackupBucket(ctx, backupConfigActivity, updateActivity, resourceName, tenancyDetails, backupRegion, kmsGrant)
			} else {
				err = workflow.ExecuteActivity(ctx, updateActivity.CreateBucketForBackupVault,
					resourceName, tenancyDetails, backupRegion, kmsGrant).Get(ctx, &bucketDetails)
			}
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}

			if backupVault.ServiceType != activities.GCBDRServiceType {
				bucketDetails.VendorSubnetID = volumeForActivities.VolumeAttributes.VendorSubnetID
			}

			// Sync bucket PZI/PZS fields from GCP.
			if err = workflows.SyncBucketDetailsWithGCP(ctx, bucketDetails); err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}

			err = workflow.ExecuteActivity(ctx, updateActivity.UpdateBucketDetailsOfBackupVault,
				volumeForActivities, bucketDetails).Get(ctx, nil)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}

			// ── GCBDR: grant pool SA access to bucket ─────────────────────────
			if backupVault.ServiceType == activities.GCBDRServiceType {
				if volume.Pool == nil {
					log.Errorf("Pool details not available for volume %s", volume.UUID)
					return nil, workflows.ConvertToVSAError(fmt.Errorf("pool details required for GCBDR bucket permissions"))
				}
				volumeCreateActivity := &activities.VolumeCreateActivity{}
				err = workflow.ExecuteActivity(ctx, volumeCreateActivity.SetupCrossProjectBackupPermissions,
					volumeForActivities.Pool, bucketDetails).Get(ctx, nil)
				if err != nil {
					log.Errorf("Failed to setup cross-project backup permissions: %v", err)
					return nil, workflows.ConvertToVSAError(err)
				}
				log.Infof("Granted pool SA access to GCBDR bucket %s for volume %s", bucketDetails.BucketName, volume.UUID)
			}

			volumeActivity := &activities.VolumeCreateActivity{}
			var remoteBV *datamodel.BackupVault
			err = workflow.ExecuteActivity(ctx, volumeActivity.CheckOrCreateRemoteBackupVaultInVCP,
				volumeForActivities, backupVault, bucketDetails).Get(ctx, &remoteBV)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}

			err = workflow.ExecuteActivity(ctx, volumeActivity.UpdateRemoteBackupVaultWithBucketDetails,
				volumeForActivities, backupVault, remoteBV, bucketDetails).Get(ctx, nil)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}
		}

		// ── Step 5: Cross-region backup permissions ───────────────────────────
		if backupVault.BackupVaultType == activities.CrossRegionBackupType && backupVault.BackupRegionName != nil && *backupVault.BackupRegionName != "" {
			volumeCreateActivity := &activities.VolumeCreateActivity{}
			err = workflow.ExecuteActivity(ctx, volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity,
				backupVault, volumeForActivities.Pool, bucketDetails).Get(ctx, nil)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}
			// Allow time for the service account to become ready.
			if err = workflow.Sleep(ctx, 90*time.Second); err != nil {
				log.Errorf("Sleep interrupted after cross-region backup permissions: %v", err)
			}
		}

		// ── Step 6: Backup policy – create schedule if a new policy is being attached ──
		if params.BackupPolicyID != nil && *params.BackupPolicyID != "" {
			var backupPolicyExists bool
			err = workflow.ExecuteActivity(ctx, updateActivity.VerifyIfBackupPolicyExistsInVCP,
				*params.BackupPolicyID, volume.AccountID).Get(ctx, &backupPolicyExists)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}

			if !backupPolicyExists {
				backupPolicyActivity := &activities.BackupPolicyActivity{}
				var vcpBackupPolicy *datamodel.BackupPolicy
				err = workflow.ExecuteActivity(ctx, updateActivity.FetchAndCreateBackupPolicyFromSDE,
					volumeForActivities, params.Region).Get(ctx, &vcpBackupPolicy)
				if err != nil {
					return nil, workflows.ConvertToVSAError(err)
				}

				err = workflow.ExecuteActivity(ctx, updateActivity.CreateScheduleForBackupPolicy,
					vcpBackupPolicy, params.BackupSchedule).Get(ctx, nil)
				if err != nil {
					return nil, workflows.ConvertToVSAError(err)
				}

				if !vcpBackupPolicy.PolicyEnabled {
					err = workflow.ExecuteActivity(ctx, backupPolicyActivity.PauseBackupPolicySchedule,
						vcpBackupPolicy).Get(ctx, nil)
					if err != nil {
						return nil, workflows.ConvertToVSAError(err)
					}
				}
			}
		}
	} // end: if params.BackupVaultID != nil && *params.BackupVaultID != ""

	// ── Step 7: Persist backup config on the expert mode volume ──────────────
	// Patch semantics: only overwrite a field when the caller explicitly provided it.
	// nil = not provided → preserve the existing persisted value unchanged.
	// Only allocate BackupConfig (and write to DB) when at least one patch field is present,
	// so a fully-empty request stays idempotent and never converts a NULL config to an empty object.
	hasBackupConfigPatch := params.BackupVaultID != nil ||
		params.BackupPolicyID != nil ||
		params.ScheduledBackupEnabled != nil ||
		params.KmsGrant != nil
	if hasBackupConfigPatch {
		if volume.BackupConfig == nil {
			volume.BackupConfig = &datamodel.DataProtection{}
		}
		if params.BackupVaultID != nil {
			volume.BackupConfig.BackupVaultID = *params.BackupVaultID // "" detaches, "uuid" sets
		}
		if params.BackupPolicyID != nil {
			volume.BackupConfig.BackupPolicyID = *params.BackupPolicyID // "" clears, "uuid" sets
		}
		if params.ScheduledBackupEnabled != nil {
			volume.BackupConfig.ScheduledBackupEnabled = params.ScheduledBackupEnabled
		}
		if params.KmsGrant != nil {
			if *params.KmsGrant == "" {
				volume.BackupConfig.KmsGrant = nil // "" clears
			} else {
				volume.BackupConfig.KmsGrant = params.KmsGrant // "key" sets
			}
		}

		err = workflow.ExecuteActivity(ctx, expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, volume).Get(ctx, nil)
		if err != nil {
			log.Errorf("Failed to persist backup config for volume %s: %v", volume.UUID, err)
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	// Restore volume state to AVAILABLE on success (the defer only runs on failure).
	err = workflow.ExecuteActivity(ctx, expertModeActivity.UpdateExpertModeVolumeStateInDB,
		volume.UUID, datamodel.LifeCycleStateAvailable).Get(ctx, nil)
	if err != nil {
		log.Errorf("Failed to update expert mode volume %s state to AVAILABLE: %v", volume.UUID, err)
		return nil, workflows.ConvertToVSAError(err)
	}

	effectiveVault := ""
	if params.BackupVaultID != nil {
		effectiveVault = *params.BackupVaultID
	}
	effectivePolicy := ""
	if params.BackupPolicyID != nil {
		effectivePolicy = *params.BackupPolicyID
	}
	workflow.GetLogger(ctx).Info("Successfully managed backup config for expert mode volume",
		"volumeUUID", volume.UUID, "vaultID", effectiveVault, "policyID", effectivePolicy)

	return nil, nil
}

// buildVolumeFromExpertMode constructs a minimal *datamodel.Volume from an expert mode volume so that
// backup-vault activities (which accept *datamodel.Volume) can be reused without modification.
func buildVolumeFromExpertMode(em *datamodel.ExpertModeVolumes, params *commonparams.ManageBackupConfigForExpertModeVolumeParams) *datamodel.Volume {
	volumeAttributes := &datamodel.VolumeAttributes{
		ExternalUUID: em.ExternalUUID,
	}
	if em.Pool != nil && em.Pool.VendorID != "" {
		volumeAttributes.VendorSubnetID = em.Pool.Network
	}

	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID:      em.UUID,
			ID:        em.ID,
			CreatedAt: em.CreatedAt,
			UpdatedAt: em.UpdatedAt,
		},
		Name:             em.Name,
		AccountID:        em.AccountID,
		PoolID:           em.PoolID,
		State:            em.State,
		Account:          em.Account,
		Pool:             em.Pool,
		VolumeAttributes: volumeAttributes,
	}
	if em.Svm != nil {
		vol.SvmID = em.Svm.ID
		vol.Svm = em.Svm
	}

	// Set DataProtection so CheckBackupVaultExistInVCP can locate the vault.
	// For KmsGrant, only forward a real (non-empty) key; &"" means "clear", not a key to use.
	kmsGrantForActivities := params.KmsGrant
	if kmsGrantForActivities != nil && *kmsGrantForActivities == "" {
		kmsGrantForActivities = nil
	}
	vaultIDForActivities := ""
	if params.BackupVaultID != nil {
		vaultIDForActivities = *params.BackupVaultID
	}
	vol.DataProtection = &datamodel.DataProtection{
		BackupVaultID: vaultIDForActivities,
		KmsGrant:      kmsGrantForActivities,
	}
	if params.BackupPolicyID != nil {
		vol.DataProtection.BackupPolicyID = *params.BackupPolicyID
	}
	if params.ScheduledBackupEnabled != nil {
		vol.DataProtection.ScheduledBackupEnabled = params.ScheduledBackupEnabled
	}

	return vol
}

func isDedicatedCapacityExpertModePool(pool *datamodel.Pool) bool {
	return pool != nil && pool.IsDedicatedCapacity()
}

func needsExpertModeBackupBucketProvisioning(bucket *commonparams.BucketDetails, pool *datamodel.Pool) bool {
	if bucket == nil {
		return true
	}
	if isDedicatedCapacityExpertModePool(pool) {
		return bucket.BucketName == "" || bucket.ServiceAccountName == "" ||
			bucket.TenantProjectNumber == "" || bucket.HmacSecretID == ""
	}
	return bucket.BucketName == "" && bucket.ServiceAccountName == "" && bucket.TenantProjectNumber == ""
}

func serviceAccountActivityContext(ctx workflow.Context) (workflow.Context, error) {
	startToClose, err := time.ParseDuration(env.GetString("SA_RETRY_START_TO_CLOSE_TIMEOUT", "15m"))
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	initialInterval, err := time.ParseDuration(env.GetString("SA_RETRY_INITIAL_INTERVAL", "1s"))
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	maxInterval, err := time.ParseDuration(env.GetString("SA_RETRY_MAXIMUM_INTERVAL", "60s"))
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	maxAttempts, err := strconv.Atoi(env.GetString("SA_RETRY_MAXIMUM_ATTEMPTS", "12"))
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	backoff, err := strconv.ParseFloat(env.GetString("SA_RETRY_BACKOFF_COEFFICIENT", "2.0"), 64)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}

	saAO := workflow.ActivityOptions{
		StartToCloseTimeout: startToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        initialInterval,
			BackoffCoefficient:     backoff,
			MaximumInterval:        maxInterval,
			MaximumAttempts:        int32(maxAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	return workflow.WithActivityOptions(ctx, saAO), nil
}

// provisionHmacForDedicatedCapacityBackupBucket creates SA, HMAC keys, Secret Manager secret, then reuses
// the shared bucket-create path for dedicated-capacity (non-FLEX) expert mode pools.
func provisionHmacForDedicatedCapacityBackupBucket(ctx workflow.Context, backupConfigActivity *expertmodeactivities.ExpertModeBackupConfigActivity, updateActivity *activities.VolumeUpdateActivity, resourceName *commonparams.ResourceNames, tenancyDetails *commonparams.TenancyInfo, region string, kmsGrant *string) (*commonparams.BucketDetails, error) {
	log := util.GetLogger(ctx)
	adcActivity := &activities.ADCActivity{}

	saCtx, err := serviceAccountActivityContext(ctx)
	if err != nil {
		return nil, err
	}

	var serviceAccount *hyperscalermodels.ServiceAccount
	err = workflow.ExecuteActivity(saCtx, backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, resourceName, tenancyDetails).Get(saCtx, &serviceAccount)
	if err != nil {
		log.Errorf("Failed to create dedicated-capacity backup service account: %v", err)
		return nil, err
	}

	var isCreated bool
	err = workflow.ExecuteActivity(saCtx, adcActivity.IsServiceAccountCreated, serviceAccount.Email).Get(saCtx, &isCreated)
	if err != nil {
		log.Errorf("Failed to check if backup service account is created: %v", err)
		return nil, err
	}
	if !isCreated {
		return nil, fmt.Errorf("service account %s is not created", serviceAccount.Email)
	}

	err = workflow.ExecuteActivity(saCtx, adcActivity.AttachRolesToServiceAccount,
		tenancyDetails.RegionalTenantProject, serviceAccount.Email, commonparams.BackupHMACServiceAccountRoles).Get(saCtx, nil)
	if err != nil {
		log.Errorf("Failed to attach roles to dedicated-capacity backup service account: %v", err)
		return nil, err
	}

	var credentialDetails *commonparams.BucketDetails
	err = workflow.ExecuteActivity(ctx, backupConfigActivity.GetDedicatedCapacityBackupHmacSecret, resourceName).Get(ctx, &credentialDetails)
	if err != nil {
		log.Errorf("Failed to check for existing dedicated-capacity backup HMAC secret: %v", err)
		return nil, err
	}

	if credentialDetails == nil {
		if err = workflow.Sleep(ctx, 60*time.Second); err != nil {
			log.Errorf("Sleep interrupted before dedicated-capacity backup HMAC key creation: %v", err)
			return nil, err
		}

		// CreateHmacKeys is not idempotent: each call creates a new GCP HMAC key, and the
		// secret is only returned at creation time. A deterministic Secret Manager entry
		// is the idempotency record; do not retry the create call itself.
		hmacCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: 3 * time.Minute,
			RetryPolicy: &temporal.RetryPolicy{
				MaximumAttempts:        1,
				NonRetryableErrorTypes: []string{"PanicError"},
			},
		})

		var encodedHmacKeys *commonparams.HmacKeys
		err = workflow.ExecuteActivity(hmacCtx, adcActivity.CreateHmacKeys, &commonparams.HmacKeyCreateParams{
			ServiceAccount: serviceAccount.Email,
			ProjectNumber:  tenancyDetails.RegionalTenantProject,
		}).Get(hmacCtx, &encodedHmacKeys)
		if err != nil {
			log.Errorf("Failed to create dedicated-capacity backup HMAC keys: %v", err)
			return nil, err
		}

		err = workflow.ExecuteActivity(ctx, backupConfigActivity.StoreDedicatedCapacityBackupHmacSecret,
			resourceName, encodedHmacKeys).Get(ctx, &credentialDetails)
		if err != nil {
			log.Errorf("Failed to store dedicated-capacity backup HMAC secret: %v", err)
			return nil, err
		}
	}

	var bucketDetails *commonparams.BucketDetails
	err = workflow.ExecuteActivity(ctx, updateActivity.CreateBucketForBackupVault,
		resourceName, tenancyDetails, region, kmsGrant).Get(ctx, &bucketDetails)
	if err != nil {
		log.Errorf("Failed to create dedicated-capacity backup bucket: %v", err)
		return nil, err
	}

	bucketDetails.ServiceAccountName = credentialDetails.ServiceAccountName
	bucketDetails.HmacSecretID = credentialDetails.HmacSecretID
	return bucketDetails, nil
}
