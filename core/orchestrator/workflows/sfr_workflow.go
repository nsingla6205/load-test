package workflows

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type RestoreFilesFromBackupWorkflowStruct struct {
	BaseWorkflow
}

var (
	sfrWorkflowHeartbeatTimeoutSec = env.GetUint64("SFR_WORKFLOW_HEARTBEAT_TIMEOUT_SEC", 600)
)

// ontapErrorMapping defines the error code and user message for a matched pattern
type ontapErrorMapping struct {
	ErrorCode   int
	UserMessage string
}

// snapmirrorErrorPatternMap maps error substrings to their corresponding error codes and messages.
var snapmirrorErrorPatternMap = map[string]ontapErrorMapping{
	"Incomplete path to file": {
		ErrorCode:   vsaerrors.ErrSFRIncorrectDestinationPath,
		UserMessage: "Incorrect destination path",
	},
}

// matchErrorPattern checks if the error message contains any known patterns
// and returns the appropriate VSA error, or nil if no match is found
func matchErrorPattern(errMsg string, patternMap map[string]ontapErrorMapping) *vsaerrors.CustomError {
	for pattern, mapping := range patternMap {
		if strings.Contains(errMsg, pattern) {
			return vsaerrors.NewVCPError(mapping.ErrorCode, errors.New(mapping.UserMessage))
		}
	}
	return nil
}

// Enforcing the WorkflowInterface on RestoreFilesFromBackupWorkflowStruct
var _ WorkflowInterface = &RestoreFilesFromBackupWorkflowStruct{}

// RestoreFilesFromBackupWorkflow processes restore files from backup requests from a customer.
func RestoreFilesFromBackupWorkflow(ctx workflow.Context, params *commonparams.RestoreFilesFromBackupParams, volume *datamodel.Volume) (interface{}, error) {
	restoreWf := new(RestoreFilesFromBackupWorkflowStruct)
	err := restoreWf.Setup(ctx, params)
	if err != nil {
		restoreWf.Logger.Errorf("Failed to setup RestoreFilesFromBackupWorkflow: %v", err)
		return nil, err
	}
	if err := restoreWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}
	restoreWf.Status = WorkflowStatusRunning
	err = restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		restoreWf.Logger.Errorf("Failed to update job status to Processing for RestoreFilesFromBackupWorkflow: %v", err)
		return nil, err
	}

	_, customErr := restoreWf.Run(ctx, params, volume)

	if customErr != nil {
		// Check if the error is a ContinueAsNewError - if so, don't revert
		if workflow.IsContinueAsNewError(customErr.OriginalErr) {
			return nil, customErr
		}
		restoreWf.Logger.Errorf("RestoreFilesFromBackupWorkflow completed with error: %v", customErr.OriginalErr.Error())
		restoreWf.Status = WorkflowStatusFailed
		err2 := restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			restoreWf.Logger.Errorf("Failed to update job status to ERROR for RestoreFilesFromBackupWorkflow: %v", err2)
			return nil, err2
		}
		return nil, customErr
	}

	restoreWf.Status = WorkflowStatusCompleted
	err = restoreWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		restoreWf.Logger.Errorf("Failed to update job status to DONE for RestoreFilesFromBackupWorkflow: %v", err)
		return nil, ConvertToVSAError(err)
	}

	return nil, nil
}

// Setup initializes the workflow with the necessary parameters and sets up a query handler for status updates.
func (wf *RestoreFilesFromBackupWorkflowStruct) Setup(ctx workflow.Context, input interface{}) error {
	restoreFilesParams := input.(*commonparams.RestoreFilesFromBackupParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = restoreFilesParams.AccountName
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

// Run executes the restore files from backup workflow.
func (wf *RestoreFilesFromBackupWorkflowStruct) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*commonparams.RestoreFilesFromBackupParams)
	volume := args[1].(*datamodel.Volume)

	log := wf.Logger
	log.Infof("Starting restore files from backup workflow for volume %s", volume.UUID)
	log.Infof("Restoring %d files from backup path: %s", len(params.SourceFileList), params.BackupPath)

	adcActivity := &activities.ADCActivity{}
	backupActivity := &activities.BackupActivity{}
	sfrActivity := &activities.SFRActivity{}
	volumeActivity := &activities.VolumeCreateActivity{}
	ontapRestoreActivity := &activities.OntapModeRestoreActivity{}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    time.Duration(sfrWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var backupVault *datamodel.BackupVault
	var backup *datamodel.Backup
	restoreCountIncremented := false
	rollbackManager := commonparams.NewRollbackManager()

	defer func() {
		// Capture the workflow error before any cleanup operations
		workflowErr := err

		// Decrement backup restore count after the workflow is complete
		if restoreCountIncremented {
			if decrementErr := workflow.ExecuteActivity(ctx, backupActivity.UpdateBackupRestoreCount,
				backupVault.UUID,
				backup.UUID,
				volume.Account.Name, activities.BackupRestoreCountDecrement).Get(ctx, nil); decrementErr != nil {
				log.Errorf("Failed to revert backup restore count: %v", decrementErr)
			}
		}

		// Check for ContinueAsNewError - if so, don't execute rollback
		if workflowErr != nil && workflow.IsContinueAsNewError(workflowErr) {
			// Don't execute rollback for ContinueAsNew - let the new execution handle it
			return
		}

		// Always restore volume to READY/Available state after workflow completes
		// The orchestrator sets volume to RESTORING before starting workflow,
		// so we restore it to READY regardless of success or failure
		if workflowErr != nil {
			log.Infof("SFR workflow failed, restoring volume %s from RESTORING state back to READY (original state was: %s)", volume.UUID, datamodel.LifeCycleStateREADY)
		} else {
			log.Infof("SFR workflow completed, restoring volume %s from RESTORING state back to READY", volume.UUID)
		}
		if params.IsExpertModeRestore {
			expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
			if err2 := workflow.ExecuteActivity(ctx, expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB, volume.UUID, datamodel.LifeCycleStateAvailable).Get(ctx, nil); err2 != nil {
				log.Errorf("Failed to restore expert mode volume state to AVAILABLE: %v", err2)
			}
		} else {
			if err2 := workflow.ExecuteActivity(ctx, volumeActivity.UpdateVolumeStateInDB, volume.UUID, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Get(ctx, nil); err2 != nil {
				log.Errorf("Failed to restore volume state to READY: %v", err2)
			}
		}

		if workflowErr != nil {
			// Execute rollback manager cleanup if there was a workflow error
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, workflowErr)
		}
	}()

	// Validate and remove duplicate files from SourceFileList
	var uniqueFiles []string
	err = workflow.ExecuteActivity(ctx, sfrActivity.ValidateAndDeduplicateFileList, params.SourceFileList).Get(ctx, &uniqueFiles)
	if err != nil {
		return nil, ConvertToVSAError(fmt.Errorf("failed to validate and deduplicate file list: %w", err))
	}

	// Update params with deduplicated file list
	params.SourceFileList = uniqueFiles
	log.Infof("Restoring %d unique files after deduplication", len(params.SourceFileList))

	// Propagate customer JWT for CVP/SDE calls (e.g. FetchProtocolsForBackup on remote backup conversion).
	// Skip in local env, and in a VCP-only region (CVP_HOST not set; no SDE/CVP dependency).
	if !env.IsLocalEnv() && utils.IsCVPHostConfigured() {
		var token string
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetAuthJWTToken, volume.Account.Name).Get(ctx, &token)
		if err != nil {
			log.Errorf("Failed to get token for account %s: %v", volume.Account.Name, err)
			return nil, ConvertToVSAError(err)
		}
		ctx = workflow.WithValue(ctx, middleware.AuthorizationToken, token)
	}

	// Extract volume region from pool vendor ID
	location, err := utils.GetLocationFromVendorID(volume.Pool.VendorID)
	if err != nil {
		log.Errorf("Failed to get location from vendor ID: %v", err)
		return nil, ConvertToVSAError(err)
	}
	volumeRegion, _, err := utils.ParseRegionAndZone(location)
	if err != nil {
		log.Errorf("Failed to parse region and zone: %v", err)
		return nil, ConvertToVSAError(err)
	}
	log.Infof("Volume region: %s", volumeRegion)

	// Fetch backup vault metadata from VCP DB or CVP/SDE
	err = workflow.ExecuteActivity(ctx, volumeActivity.FetchBackupVaultMetadataForRestore,
		params.BackupPath, volume, volumeRegion).Get(ctx, &backupVault)
	if err != nil {
		log.Errorf("Failed to fetch backup vault metadata for restore: %v", err)
		return nil, ConvertToVSAError(err)
	}

	if backupVault == nil {
		log.Errorf("Backup vault metadata is nil after fetch")
		return nil, ConvertToVSAError(fmt.Errorf("failed to fetch backup vault metadata: received nil response"))
	}
	log.Infof("Successfully fetched backup vault metadata: vault='%s'", backupVault.Name)

	// Fetch backup metadata from VCP DB or CVP/SDE
	err = workflow.ExecuteActivity(ctx, volumeActivity.FetchBackupMetadataForRestore,
		params.BackupPath, backupVault, volume, volumeRegion).Get(ctx, &backup)
	if err != nil {
		log.Errorf("Failed to fetch backup metadata for restore: %v", err)
		ce := ConvertToVSAError(err)
		if customerrors.IsNotFoundErr(err) || (ce != nil && ce.TrackingID == vsaerrors.ErrResourceNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrBadRequest, err)
		}
		return nil, ce
	}

	if backup == nil {
		log.Errorf("Backup metadata is nil after fetch")
		return nil, ConvertToVSAError(fmt.Errorf("failed to fetch backup metadata: received nil response"))
	}

	if backup.Attributes == nil {
		err = fmt.Errorf("backup attributes are missing for backup '%s'", backup.Name)
		log.Errorf("%v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrSFRSnapshotIDMissing, err)
	}

	if strings.TrimSpace(backup.Attributes.SnapshotID) == "" {
		backupName := backup.Name
		if backupName == "" {
			pathComponents := strings.Split(params.BackupPath, "/")
			if len(pathComponents) == 8 {
				backupName = pathComponents[7]
			}
		}
		if backupName == "" {
			err = fmt.Errorf("backup snapshot ID is missing for backup '%s' and backup name could not be determined for source-path fallback", backup.Name)
			log.Errorf("%v", err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSFRSnapshotIDMissing, err)
		}
		if backupVault.CrossRegionBackupVaultName == nil || strings.TrimSpace(*backupVault.CrossRegionBackupVaultName) == "" {
			err = fmt.Errorf("backup snapshot ID is missing for backup '%s' and cross-region source backup vault path is not set (cannot load snapshot from source backup path)", backup.Name)
			log.Errorf("%v", err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSFRSnapshotIDMissing, err)
		}

		sourceBackupPath := fmt.Sprintf("%s/backups/%s", *backupVault.CrossRegionBackupVaultName, backupName)

		var sourceBackupVault *datamodel.BackupVault
		fallbackErr := workflow.ExecuteActivity(ctx, volumeActivity.FetchBackupVaultMetadataForRestore,
			sourceBackupPath, volume, volumeRegion).Get(ctx, &sourceBackupVault)
		if fallbackErr != nil {
			err = fmt.Errorf("failed to fetch source backup vault metadata for snapshot ID fallback (path=%s): %w", sourceBackupPath, fallbackErr)
			log.Errorf("%v", err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSFRSnapshotIDMissing, err)
		}
		if sourceBackupVault == nil {
			err = fmt.Errorf("source backup vault metadata is nil after fallback fetch (path=%s)", sourceBackupPath)
			log.Errorf("%v", err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSFRSnapshotIDMissing, err)
		}

		var sourceBackup *datamodel.Backup
		fallbackErr = workflow.ExecuteActivity(ctx, volumeActivity.FetchBackupMetadataForRestore,
			sourceBackupPath, sourceBackupVault, volume, volumeRegion).Get(ctx, &sourceBackup)
		if fallbackErr != nil {
			err = fmt.Errorf("failed to fetch source backup metadata for snapshot ID fallback (path=%s): %w", sourceBackupPath, fallbackErr)
			log.Errorf("%v", err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSFRSnapshotIDMissing, err)
		}
		if sourceBackup == nil || sourceBackup.Attributes == nil || strings.TrimSpace(sourceBackup.Attributes.SnapshotID) == "" {
			err = fmt.Errorf("source backup at path %s did not provide a snapshot ID (needed because destination backup '%s' has none)", sourceBackupPath, backup.Name)
			log.Errorf("%v", err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSFRSnapshotIDMissing, err)
		}
		backup.Attributes.SnapshotID = sourceBackup.Attributes.SnapshotID
		log.Infof("Updated backup metadata snapshotID from source backup path fallback, snapshotID='%s'", backup.Attributes.SnapshotID)
	}

	log.Infof("Successfully fetched backup metadata: backup='%s', state='%s'", backup.Name, backup.State)

	// Validate backup is in available or ready state before proceeding with restore
	// SDE backups use READY state, VCP backups use AVAILABLE state
	if backup.State != datamodel.LifeCycleStateAvailable && backup.State != datamodel.LifeCycleStateREADY {
		err = fmt.Errorf("cannot restore from backup '%s' which is not in available or ready state (current state: %s)",
			backup.Name, backup.State)
		log.Errorf("Backup state validation failed: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Validate protocol compatibility (SAN not supported for single file restore)
	if utils.IsSanProtocols(backup.Attributes.Protocols) {
		err = fmt.Errorf("single file restore is not supported from a backup of ISCSI volumes")
		log.Errorf("Protocol validation failed: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Fetch bucket metadata and ensure bucket details exist
	err = workflow.ExecuteActivity(ctx, volumeActivity.FetchBucketMetadataForRestore,
		backup, backupVault).Get(ctx, &backupVault)
	if err != nil {
		log.Errorf("Failed to fetch bucket metadata for restore: %v", err)
		return nil, ConvertToVSAError(err)
	}

	backup.BackupVault = backupVault
	backupVault.Account = volume.Account
	log.Infof("Backup metadata with all required details fetched successfully: backup='%s', vault='%s'", backup.Name, backupVault.Name)

	roles := commonparams.BackupHMACServiceAccountRoles

	// Increment restore count to indicate that a volume restoration is in-progress for the backup
	err = workflow.ExecuteActivity(ctx, backupActivity.UpdateBackupRestoreCount,
		backupVault.UUID,
		backup.UUID,
		volume.Account.Name, activities.BackupRestoreCountIncrement).Get(ctx, nil)
	if err != nil {
		log.Errorf("Failed to update backup restore count: %v", err)
		return nil, ConvertToVSAError(err)
	}
	restoreCountIncremented = true

	// Execute VPC pool restoration activity to handle cross-project permissions
	err = workflow.ExecuteActivity(ctx, volumeActivity.CrossPoolOrVPCRestorationActivity, volume.Pool, backup).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Generate deterministic timestamp for resource naming
	var saTimestamp string
	err = workflow.ExecuteActivity(ctx, adcActivity.GenerateResourceTimestamp).Get(ctx, &saTimestamp)
	if err != nil {
		log.Errorf("Failed to generate resource timestamp: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Get bucket details for HMAC key creation
	bucketDetails, err := getBucketDetailsForBucket(backupVault, backup.Attributes.BucketName)
	if err != nil {
		log.Errorf("Failed to get bucket details: %v", err)
		return nil, ConvertToVSAError(err)
	}

	if backup.Attributes.EndpointUUID == "" || bucketDetails.BucketName == "" {
		return nil, ConvertToVSAError(fmt.Errorf("endpoint UUID or bucket name is not available"))
	}

	// Use SA-specific retry policy for IAM eventual consistency around service account operations.
	saRetryPolicy, err := populateServiceAccountRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	saAO := workflow.ActivityOptions{
		StartToCloseTimeout: saRetryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    time.Duration(sfrWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        saRetryPolicy.InitialInterval,
			BackoffCoefficient:     saRetryPolicy.BackoffCoefficient,
			MaximumInterval:        saRetryPolicy.MaximumInterval,
			MaximumAttempts:        int32(saRetryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	saCtx := workflow.WithActivityOptions(ctx, saAO)

	// Step 1: Create service account for ADC operations
	serviceAccountID := fmt.Sprintf("adc-sa-%s", saTimestamp)
	serviceAccountDisplayName := fmt.Sprintf("ADC Service Account for SFR %s", backup.UUID)

	var serviceAccount *hyperscalermodels.ServiceAccount
	err = workflow.ExecuteActivity(saCtx, adcActivity.CreateServiceAccount,
		bucketDetails.TenantProjectNumber, serviceAccountID, serviceAccountDisplayName).Get(saCtx, &serviceAccount)
	if err != nil {
		log.Errorf("Failed to create service account: %v", err)
		return nil, ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(adcActivity.DeleteSA, bucketDetails.TenantProjectNumber, serviceAccountID)

	// Step 2: Check if service account is created
	var isCreated bool
	err = workflow.ExecuteActivity(saCtx, adcActivity.IsServiceAccountCreated, serviceAccount.Email).Get(saCtx, &isCreated)
	if err != nil {
		log.Errorf("Failed to check if service account is created: %v", err)
		return nil, ConvertToVSAError(err)
	}

	if !isCreated {
		log.Errorf("Service account is not created")
		err = fmt.Errorf("service account is not created")
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}

	// Step 3: Attach roles to service account
	err = workflow.ExecuteActivity(saCtx, adcActivity.AttachRolesToServiceAccount,
		bucketDetails.TenantProjectNumber, serviceAccount.Email, roles).Get(saCtx, nil)
	if err != nil {
		log.Errorf("Failed to attach roles to service account: %v", err)
		return nil, ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(adcActivity.RemoveRolesFromServiceAccount, bucketDetails.TenantProjectNumber, serviceAccountID, roles)

	// Step 4: Create HMAC keys for ADC operations
	var encodedHmacKeys *commonparams.HmacKeys
	err = workflow.ExecuteActivity(saCtx, adcActivity.CreateHmacKeys, &commonparams.HmacKeyCreateParams{
		ServiceAccount: serviceAccount.Email,
		ProjectNumber:  bucketDetails.TenantProjectNumber,
	}).Get(saCtx, &encodedHmacKeys)
	if err != nil {
		log.Errorf("Failed to create HMAC keys: %v", err)
		return nil, ConvertToVSAError(err)
	}

	cloudRunConfig := &hyperscalermodels.CloudRunServiceConfig{
		ProjectID:     adcProjectID,
		LocationID:    adcRegion,
		ServiceName:   fmt.Sprintf("adc-svc-%s", saTimestamp),
		Image:         adcImage,
		Description:   fmt.Sprintf("ADC Cloud Run service for SFR %s", backup.UUID),
		ContainerPort: int64(adcRestPort),
		Labels: map[string]string{
			"app":        "adc",
			"component":  "backup",
			"managed-by": "vsa-control-plane",
		},
		Annotations: map[string]string{
			"description": "ADC service for single file restore operations",
		},
		Ingress: "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER",
		EnvVars: map[string]string{
			"RUN_REST":       "1",
			"REST_PORT":      fmt.Sprintf("%d", adcRestPort),
			"PROVIDER":       "GoogleCloud",
			"LOG_LEVEL":      "2",
			"ENABLE_COPY":    "1",
			"LOG_TO_CONSOLE": "1",
			"CA_FILE":        "adc-cert.crt",
			"CERT_PATH":      "/home/ADC/cert/",
		},
		VolumeMounts: []hyperscalermodels.VolumeMount{
			{
				Name:      "adc-cert",
				MountPath: "/home/ADC/cert",
			},
		},
		Volumes: []hyperscalermodels.Volume{
			{
				Name:       "adc-cert",
				VolumeType: "secret",
				Source: hyperscalermodels.VolumeSource{
					SecretName: adcCertSecret,
					Items: []hyperscalermodels.SecretItem{
						{
							Path:    "adc-cert.crt",
							Version: "latest",
						},
					},
				},
			},
		},
		StartupProbe: &hyperscalermodels.ProbeConfig{
			InitialDelaySeconds: 0,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			FailureThreshold:    30,
			TCPSocket:           &hyperscalermodels.TCPSocketAction{Port: int64(adcRestPort)},
		},
	}

	var cloudRunResponse *hyperscalermodels.CloudRunOperationResponse
	err = workflow.ExecuteActivity(ctx, adcActivity.DeployADCCloudRunService, cloudRunConfig).Get(ctx, &cloudRunResponse)
	if err != nil {
		log.Errorf("Failed to deploy ADC Cloud Run service: %v", err)
		return nil, ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(adcActivity.CleanupADCCloudRunService, adcProjectID, adcRegion, cloudRunConfig.ServiceName)

	// Step 6: Wait for Cloud Run service to be ready
	var isReady bool
	attempts := 0
	adcMaxCloudRunAttempts := 20
	for !isReady && attempts < adcMaxCloudRunAttempts {
		err = workflow.ExecuteActivity(ctx, adcActivity.CheckOperationStatus, cloudRunResponse.OperationName).Get(ctx, &isReady)
		if err != nil {
			log.Errorf("Failed to check Cloud Run operation status: %v", err)
			return nil, ConvertToVSAError(err)
		}
		if !isReady {
			attempts++
			err = workflow.Sleep(ctx, time.Second*10)
			if err != nil {
				return nil, ConvertToVSAError(fmt.Errorf("failed to sleep during Cloud Run deployment: %w", err))
			}
		}
	}

	if !isReady {
		return nil, ConvertToVSAError(fmt.Errorf("cloud run service deployment timed out after %d attempts", adcMaxCloudRunAttempts))
	}

	// Step 7: Get Cloud Run service URL
	var serviceURL string
	err = workflow.ExecuteActivity(ctx, adcActivity.GetADCServiceURL, adcProjectID, adcRegion, cloudRunConfig.ServiceName).Get(ctx, &serviceURL)
	if err != nil {
		log.Errorf("Failed to get ADC service URL: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Wait for service account and HMAC keys to be ready
	err = workflow.Sleep(ctx, time.Second*90)
	if err != nil {
		log.Errorf("Failed to sleep after ADC service deployment: %v", err)
	}

	// Step 8: Get inode numbers for files using ADC
	adcParams := &commonparams.ADCParams{
		ADCName:          backup.Name,
		DestEndpointUUID: backup.Attributes.EndpointUUID,
		SnapshotUUID:     backup.Attributes.SnapshotID,
		BucketName:       backup.Attributes.BucketName,
		AccessKey:        encodedHmacKeys.AccessKey,
		SecretKey:        encodedHmacKeys.SecretKey,
		ProvideType:      adcProvideType,
		ServerURL:        adcStorageURL,
		AccountName:      backupVault.Account.Name,
		Port:             int64(adcPort),
	}

	// Get inode numbers and sizes for files using ADC
	var fileInodeSizeMap map[string]*activities.FileInodeAndSize
	err = workflow.ExecuteActivity(ctx, adcActivity.GetFileInodeNumbers, adcParams, serviceURL, params.SourceFileList).Get(ctx, &fileInodeSizeMap)
	if err != nil {
		log.Errorf("Failed to get inode numbers and sizes for files: %v", err)
		return nil, ConvertToVSAError(err)
	}

	if len(fileInodeSizeMap) == 0 {
		errorMsg := "No files found in backup for the specified file list"
		log.Errorf("SFR workflow failed: %s", errorMsg)
		err = fmt.Errorf("%s", errorMsg)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrNoSFRFilesFound, err)
	}

	// Step 9: Resolve ONTAP node and prepare snapmirror restore
	var node *models.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.ResolveOntapNodeForPool, volume.Pool).Get(ctx, &node)
	if err != nil {
		log.Errorf("Failed to resolve ONTAP node: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// For expert mode flexgroup backup: verify restore target constituent count matches backup
	if params.IsExpertModeRestore && backup.Attributes != nil && backup.Attributes.OntapVolumeStyle == "flexgroup" {
		var restoreTargetConstituentCount int32
		err = workflow.ExecuteActivity(ctx, ontapRestoreActivity.FetchConstituentCountForLargeVolume, volume, node).Get(ctx, &restoreTargetConstituentCount)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		err = workflow.ExecuteActivity(ctx, ontapRestoreActivity.VerifyCVCountForLargeVolume, backup, restoreTargetConstituentCount).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	var objStoreName string
	err = workflow.ExecuteActivity(ctx, backupActivity.GenerateObjectStoreNameForRestore, backupVault, backup).Get(ctx, &objStoreName)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	var bucketDetailsFromBackup *datamodel.BucketDetails
	err = workflow.ExecuteActivity(ctx, backupActivity.GetBucketDetailsFromBackupActivity, backupVault, backup).Get(ctx, &bucketDetailsFromBackup)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	if params.IsExpertModeRestore && volume.Pool != nil && volume.Pool.IsDedicatedCapacity() {
		err = workflow.ExecuteActivity(ctx, volumeActivity.PopulateBucketDetailsForTemporaryRestoreActivity,
			bucketDetailsFromBackup, backup, backupVault, params.BackupPath).Get(ctx, &bucketDetailsFromBackup)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}
	bucketName := bucketDetailsFromBackup.BucketName

	// Get snapmirror source and destination paths
	var smDestinationPath string
	err = workflow.ExecuteActivity(ctx, backupActivity.GetSmSourcePathActivity, volume).Get(ctx, &smDestinationPath)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	var smSourcePath string
	if volume.LargeVolumeAttributes != nil && volume.LargeVolumeAttributes.LargeCapacity && volume.UUID == backup.VolumeUUID {
		smSourcePath = fmt.Sprintf("%s:/objstore/%s", objStoreName, backup.VolumeUUID)
	} else {
		smSourcePath = fmt.Sprintf("%s:/objstore/%s", objStoreName, backup.Attributes.SnapshotID)
	}

	// Wait before starting snapmirror restore
	err = workflow.Sleep(ctx, 60*time.Second)
	if err != nil {
		return nil, ConvertToVSAError(fmt.Errorf("failed to sleep before starting snapmirror restore: %w", err))
	}

	objStore := &commonparams.CloudTarget{}
	var temporaryHmac *commonparams.TemporaryRestoreHmacHandle
	if params.IsExpertModeRestore && volume.Pool != nil && volume.Pool.IsDedicatedCapacity() {
		temporaryHmac, err = provisionTemporaryRestoreHmacIfNeeded(ctx, rollbackManager, volume.Pool, bucketDetailsFromBackup, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		err = workflow.ExecuteActivity(ctx, backupActivity.GetOrCreateObjectStoreForRestore,
			node, objStoreName, bucketName, bucketDetailsFromBackup, volume.Pool, temporaryHmac).Get(ctx, &objStore)
	} else {
		err = workflow.ExecuteActivity(ctx, backupActivity.GetOrCreateObjectStore, node, objStoreName, bucketName).Get(ctx, &objStore)
	}
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(volumeActivity.DeleteRestoreObjectStore, node, objStoreName)

	snapmirrorRelationship := &commonparams.SnapmirrorRelationship{}
	SnapmirrorRelationshipParams := &commonparams.SnapmirrorRelationshipParams{
		SourcePath:      smSourcePath,
		DestinationPath: smDestinationPath,
		SourceUUID:      &backup.Attributes.EndpointUUID,
		IsRestore:       true,
	}

	err = workflow.ExecuteActivity(ctx, backupActivity.SnapmirrorGetOrCreate, node, &SnapmirrorRelationshipParams).Get(ctx, &snapmirrorRelationship)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(activities.BackupActivity.DeleteSnapmirror, node, snapmirrorRelationship.UUID)

	// Step 10: Create snapmirror transfer with inode numbers
	// Build files list with inode numbers (matching cloud-backup-service pattern)
	// In cloud-backup-service: spec.FileList is replaced with inode numbers, and destinations are built separately
	transferFiles := make([]*commonparams.SnapmirrorTransferFile, 0, len(params.SourceFileList))
	missingFiles := make([]string, 0)

	for _, sourceFile := range params.SourceFileList {
		fileInfo, ok := fileInodeSizeMap[sourceFile]
		if !ok || fileInfo == nil {
			log.Errorf("Inode number not found for file %s - file not present in backup", sourceFile)
			missingFiles = append(missingFiles, sourceFile)
			continue
		}

		// Build destination path matching cloud-backup-service buildDstFileList pattern
		// If RestoreFilePath is provided, use it + filename; otherwise use original path
		var destinationPath string
		if params.RestoreFilePath != "" {
			// Extract just the filename from the source path (like filepath.Base)
			// Find last '/' to get filename
			filename := sourceFile
			if lastSlash := strings.LastIndex(sourceFile, "/"); lastSlash >= 0 {
				filename = sourceFile[lastSlash+1:]
			}
			destinationPath = fmt.Sprintf("%s/%s", params.RestoreFilePath, filename)
		} else {
			// Restore to original location
			destinationPath = sourceFile
		}

		transferFiles = append(transferFiles, &commonparams.SnapmirrorTransferFile{
			SourcePath:      fileInfo.Inode, // Use inode number as source_path (matches cloud-backup-service pattern)
			DestinationPath: destinationPath,
		})
	}

	if len(transferFiles) == 0 {
		// If no files were found, fail immediately
		errorMsg := fmt.Sprintf("No files found in backup. The following file(s) are not present in the backup: %s", strings.Join(missingFiles, ", "))
		log.Errorf("SFR workflow failed: %s", errorMsg)
		return nil, ConvertToVSAError(fmt.Errorf("%s", errorMsg))
	}

	// Log warning if some files are missing but continue with transfer for found files
	if len(missingFiles) > 0 {
		log.Warnf("Some files are not present in backup and will be skipped. Missing files: %s. Continuing with transfer for %d file(s) that were found.", strings.Join(missingFiles, ", "), len(transferFiles))
	}

	err = workflow.ExecuteActivity(ctx, sfrActivity.SnapmirrorTransferWithFiles, node, snapmirrorRelationship.UUID, backup.Attributes.SnapshotName, transferFiles).Get(ctx, nil)
	if err != nil {
		log.Errorf("Failed to initiate snapmirror transfer with files: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Step 11: Poll transfer status
	var transferStatus *activities.SnapmirrorTransferStatus
	waitTime := time.Second * 10
	maxWaitTime := time.Hour * 24
	startTime := workflow.Now(ctx)

	for {
		err = workflow.ExecuteActivity(ctx, backupActivity.GetSnapmirrorTransferStatus, node, snapmirrorRelationship.UUID, backup.Attributes.SnapshotName).Get(ctx, &transferStatus)
		if err != nil {
			log.Errorf("Failed to get snapmirror transfer status: %v", err)
			return nil, ConvertToVSAError(err)
		}

		if transferStatus != nil && transferStatus.Status == activities.SmStatusSuccess {
			log.Infof("Snapmirror transfer completed successfully")
			break
		}

		if transferStatus != nil && transferStatus.Status == activities.SmStatusFailed {
			return nil, ConvertToVSAError(fmt.Errorf("snapmirror transfer failed"))
		}

		elapsed := workflow.Now(ctx).Sub(startTime)
		if elapsed > maxWaitTime {
			return nil, ConvertToVSAError(fmt.Errorf("snapmirror transfer exceeded maximum wait time"))
		}

		err = workflow.Sleep(ctx, waitTime)
		if err != nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to sleep during transfer polling: %w", err))
		}
	}

	// Wait for 30 seconds before proceeding
	err = workflow.Sleep(ctx, 30*time.Second)
	if err != nil {
		return nil, ConvertToVSAError(fmt.Errorf("failed to sleep before getting the snapmirror: %w", err))
	}

	// Get snapmirror relationship to check health status
	var smRelationship *commonparams.SnapmirrorRelationship
	err = workflow.ExecuteActivity(ctx, backupActivity.GetSnapmirror, node, smSourcePath, smDestinationPath).Get(ctx, &smRelationship)
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
			errMsg := fmt.Sprintf("snapmirror relationship is unhealthy. Reasons: %v", *smRelationship.UnhealthyReason)
			log.Errorf(errMsg)
			err = fmt.Errorf("%s", errMsg)

			// Check if error matches any known patterns and return appropriate error code
			if matchedErr := matchErrorPattern(errMsg, snapmirrorErrorPatternMap); matchedErr != nil {
				return nil, matchedErr
			}
			return nil, ConvertToVSAError(err)
		}
	}

	// Wait for 60 seconds before proceeding
	err = workflow.Sleep(ctx, 60*time.Second)
	if err != nil {
		return nil, ConvertToVSAError(fmt.Errorf("failed to sleep before deleting object store: %w", err))
	}

	// Delete object store for cross VPC after transfer completes
	var ontapAsyncResponse *vsa.OntapAsyncResponse
	err = workflow.ExecuteActivity(ctx, volumeActivity.DeleteRestoreObjectStore, node, objStoreName).Get(ctx, &ontapAsyncResponse)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	if ontapAsyncResponse != nil {
		err = WaitForONTAPJob(ctx, ontapAsyncResponse, node, time.Minute*10)
		if err != nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to delete cloud endpoint: %w", err))
		}
	}
	if temporaryHmac != nil {
		if cleanupErr := executeTemporaryRestoreCleanup(ctx, temporaryHmac); cleanupErr != nil {
			return nil, cleanupErr
		}
	}

	// Step 12: Cleanup Cloud Run service
	var cleanupResponse *hyperscalermodels.CloudRunOperationResponse
	err = workflow.ExecuteActivity(ctx, adcActivity.CleanupADCCloudRunService, adcProjectID, adcRegion, cloudRunConfig.ServiceName).Get(ctx, &cleanupResponse)
	if err != nil {
		log.Errorf("Failed to cleanup ADC Cloud Run service: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Wait for Cloud Run service cleanup to complete
	var isCleanupReady bool
	cleanupAttempts := 0
	for !isCleanupReady && cleanupAttempts < adcMaxCloudRunAttempts {
		err = workflow.ExecuteActivity(ctx, adcActivity.CheckOperationStatus, cleanupResponse.OperationName).Get(ctx, &isCleanupReady)
		if err != nil {
			log.Errorf("Failed to check Cloud Run cleanup operation status: %v", err)
			return nil, ConvertToVSAError(err)
		}
		if !isCleanupReady {
			cleanupAttempts++
			err := workflow.Sleep(ctx, time.Second*10)
			if err != nil {
				return nil, ConvertToVSAError(fmt.Errorf("failed to sleep during Cloud Run cleanup: %w", err))
			}
		}
	}

	if !isCleanupReady {
		log.Warnf("Cloud Run service cleanup timed out after %d attempts, but continuing with workflow", adcMaxCloudRunAttempts)
	}

	// Step 13: Remove roles from service account
	err = workflow.ExecuteActivity(ctx, adcActivity.RemoveRolesFromServiceAccount, bucketDetails.TenantProjectNumber, serviceAccountID, roles).Get(ctx, nil)
	if err != nil {
		log.Errorf("Failed to remove roles from service account: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Step 14: Cleanup service account
	err = workflow.ExecuteActivity(ctx, adcActivity.DeleteSA, bucketDetails.TenantProjectNumber, serviceAccountID).Get(ctx, nil)
	if err != nil {
		log.Errorf("Failed to cleanup service account: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Step 15: Populate SfrMetadata with file count and total size
	// Get job ID (int64) from job UUID (wf.ID is the job UUID)
	var job *datamodel.Job
	var jobID *int64
	commonActivity := &activities.CommonActivities{}
	err = workflow.ExecuteActivity(ctx, commonActivity.GetJob, wf.ID).Get(ctx, &job)
	if err != nil {
		log.Warnf("Failed to get job by UUID %s: %v, continuing without job ID", wf.ID, err)
	} else if job != nil {
		jobID = &job.ID
		// Populate SfrMetadata even if job ID is not available (jobID will be nil)
		err = workflow.ExecuteActivity(ctx, sfrActivity.PopulateSfrMetadataActivity, fileInodeSizeMap, volume, backup, jobID).Get(ctx, nil)
		if err != nil {
			log.Errorf("Failed to populate SfrMetadata: %v", err)
			// Don't fail the workflow if metadata population fails, just log the error
			log.Warnf("Continuing despite SfrMetadata population failure")
		}
	}

	// Step 16: Check if any files were missing and fail with detailed error
	if len(missingFiles) > 0 {
		errorMsg := fmt.Sprintf("Transfer completed for %d file(s), but the following file(s) are not present in the backup: %s", len(transferFiles), strings.Join(missingFiles, ", "))
		log.Errorf("SFR workflow failed: %s", errorMsg)
		err = fmt.Errorf("%s", errorMsg)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrSFRFilesMissing, err)
	}

	log.Infof("Restore files from backup workflow completed successfully for %d files", len(transferFiles))
	err = nil
	return nil, nil
}
