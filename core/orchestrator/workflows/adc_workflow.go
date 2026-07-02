package workflows

import (
	"fmt"
	"net/http"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	adcPort        = 443
	adcImage       = env.GetString("ADC_IMAGE", "")
	adcRegion      = env.GetString("ADC_REGION", "")
	adcProjectID   = env.GetString("ADC_PROJECT", "")
	adcProvideType = env.GetString("ADC_PROVIDE_TYPE", "GoogleCloud")
	adcStorageURL  = env.GetString("ADC_STORAGE_URL", "storage.googleapis.com")
	adcCertSecret  = env.GetString("ADC_CERT_SECRET_NAME", "adc-cert")

	adcMaxCloudRunAttempts         = 20
	adcWorkflowHeartbeatTimeoutSec = env.GetUint64("ADC_WORKFLOW_HEARTBEAT_TIMEOUT_SEC", 600)
)

// Progressive sleep phase constants
const (
	// First phase: 5 seconds sleep for first 5 minutes
	firstPhaseThreshold     = 5 * time.Minute
	firstPhaseSleepDuration = 5 * time.Second

	// Second phase: 10 seconds sleep for next 10 minutes (5-15 minutes total)
	secondPhaseThreshold     = 15 * time.Minute
	secondPhaseSleepDuration = 10 * time.Second

	// Third phase: 5 minutes sleep for next 1 hour (15 minutes - 1 hour 15 minutes total)
	thirdPhaseThreshold     = 75 * time.Minute
	thirdPhaseSleepDuration = 5 * time.Minute

	// Fourth phase: 10 minutes sleep for remaining time (up to 6 days max)
	fourthPhaseSleepDuration = 10 * time.Minute

	// Maximum time limit for polling operations
	maxPollingTimeLimit = 6 * 24 * time.Hour
)

type AdcWF struct {
	BaseWorkflow
	cloudDeletionIntiated bool
}

// Enforcing the WorkflowInterface on adcWorkflow
var _ WorkflowInterface = &AdcWF{}

// ADCWorkflow processes ADC (Application Data Controller) related requests from a customer.
func ADCWorkflow(ctx workflow.Context, params *common.DeleteBackupParams, backupVault *datamodel.BackupVault, backup *datamodel.Backup, account *datamodel.Account) (bool, error) {
	adcWf := new(AdcWF)
	// Create a wrapper struct to pass both arguments through the interface
	err := adcWf.Setup(ctx, params)
	if err != nil {
		return adcWf.cloudDeletionIntiated, err
	}
	adcWf.Status = WorkflowStatusRunning
	_, customErr := adcWf.Run(ctx, backupVault, backup, account)

	if customErr != nil {
		adcWf.Status = WorkflowStatusFailed
		return adcWf.cloudDeletionIntiated, customErr
	}
	adcWf.Status = WorkflowStatusCompleted
	return adcWf.cloudDeletionIntiated, nil
}

func (wf *AdcWF) Setup(ctx workflow.Context, input interface{}) error {
	// Extract backupVault and backup from the input struct
	params := input.(*common.DeleteBackupParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = params.AccountName
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

func (wf *AdcWF) Run(ctx workflow.Context, args ...interface{}) (_ interface{}, error *vsaerrors.CustomError) {
	log := util.GetLogger(ctx)

	// Extract arguments
	backupVault := args[0].(*datamodel.BackupVault)
	backup := args[1].(*datamodel.Backup)
	account := args[2].(*datamodel.Account)
	backupVault.Account = account

	adcActivity := &activities.ADCActivity{}
	backupActivity := &activities.BackupActivity{}

	// Define roles that will be attached to service account
	roles := []string{
		"roles/storage.hmacKeyAdmin",
		"roles/storage.objectAdmin",
		"roles/storage.admin",
		"roles/iam.serviceAccountAdmin",
	}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    time.Duration(adcWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rollbackManager := common.NewRollbackManager()
	defer func() {
		if error != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

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
		HeartbeatTimeout:    time.Duration(adcWorkflowHeartbeatTimeoutSec) * time.Second,
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
	// Generate a short service account ID to comply with Google's 30-character limit
	serviceAccountID := fmt.Sprintf("adc-sa-%s", saTimestamp)
	serviceAccountDisplayName := fmt.Sprintf("ADC Service Account for %s", backup.UUID)

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
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, fmt.Errorf("service account is not created"))
	}

	// Step 2: Attach roles to service account
	err = workflow.ExecuteActivity(saCtx, adcActivity.AttachRolesToServiceAccount,
		bucketDetails.TenantProjectNumber, serviceAccount.Email, roles).Get(saCtx, nil)
	if err != nil {
		log.Errorf("Failed to attach roles to service account: %v", err)
		return nil, ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(adcActivity.RemoveRolesFromServiceAccount, bucketDetails.TenantProjectNumber, serviceAccountID, roles)

	// Step 2: Create HMAC keys for ADC operations
	var encodedHmacKeys *common.HmacKeys
	err = workflow.ExecuteActivity(saCtx, adcActivity.CreateHmacKeys, &common.HmacKeyCreateParams{
		ServiceAccount: serviceAccount.Email,
		ProjectNumber:  bucketDetails.TenantProjectNumber,
	}).Get(saCtx, &encodedHmacKeys)
	if err != nil {
		log.Errorf("Failed to create HMAC keys: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Step 3: Deploy ADC Cloud Run service
	cloudRunConfig := &hyperscalermodels.CloudRunServiceConfig{
		ProjectID:   adcProjectID,
		LocationID:  adcRegion,
		ServiceName: fmt.Sprintf("adc-svc-%s", saTimestamp),
		Image:       adcImage,
		Description: fmt.Sprintf("ADC Cloud Run service for %s", backup.UUID),
		Labels: map[string]string{
			"app":        "adc",
			"component":  "backup",
			"managed-by": "vsa-control-plane",
		},
		Annotations: map[string]string{
			"description": "ADC service for backup and restore operations",
		},
		Ingress: "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER", // Use internal load balancer ingress
		EnvVars: map[string]string{
			"RUN_REST":       "1",
			"REST_PORT":      "80",
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
			TCPSocket:           &hyperscalermodels.TCPSocketAction{Port: 80},
		},
	}

	var cloudRunResponse *hyperscalermodels.CloudRunOperationResponse
	err = workflow.ExecuteActivity(ctx, adcActivity.DeployADCCloudRunService, cloudRunConfig).Get(ctx, &cloudRunResponse)
	if err != nil {
		log.Errorf("Failed to deploy ADC Cloud Run service: %v", err)
		return nil, ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(adcActivity.CleanupADCCloudRunService, adcProjectID, adcRegion, cloudRunConfig.ServiceName)

	// Step 4: Wait for Cloud Run service to be ready
	var isReady bool
	attempts := 0
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

	// Step 5: Get Cloud Run service URL
	var serviceURL string
	err = workflow.ExecuteActivity(ctx, adcActivity.GetADCServiceURL, adcProjectID, adcRegion, cloudRunConfig.ServiceName).Get(ctx, &serviceURL)
	if err != nil {
		log.Errorf("Failed to get ADC service URL: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// wait for service account and HMAC keys to be ready
	err = workflow.Sleep(ctx, time.Second*90)
	if err != nil {
		log.Errorf("Failed to sleep after ADC service deployment: %v", err)
	}

	adcParams := &common.ADCParams{
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

	wf.cloudDeletionIntiated = true
	// Step 8: Process ADC delete request
	var adcResponse *common.ADCResponse
	err = workflow.ExecuteActivity(saCtx, adcActivity.InitialDeleteRequestWithCloudRun, adcParams, serviceURL).Get(saCtx, &adcResponse)
	if err != nil {
		log.Errorf("Failed to initiate ADC delete request: %v", err)
		return nil, ConvertToVSAError(err)
	}

	switch adcResponse.StatusCode {
	case http.StatusTemporaryRedirect:
		currentRedirectURL := adcResponse.RedirectURL
		pollingCompleted := false
		startTime := workflow.Now(ctx)

		for !pollingCompleted {
			var statusResponse common.ADCResponse
			err = workflow.ExecuteActivity(ctx, adcActivity.CheckDeleteStatusWithCloudRun, adcParams, serviceURL, currentRedirectURL).Get(ctx, &statusResponse)
			if err != nil {
				wf.Logger.Error("CheckDeleteStatus failed", "error", err)
				return nil, ConvertToVSAError(err)
			}
			switch statusResponse.StatusCode {
			case http.StatusOK:
				wf.Logger.Info("Backup deletion completed successfully")
				pollingCompleted = true
			case http.StatusNotFound:
				wf.Logger.Info("Backup not found")
				pollingCompleted = true
			case http.StatusTemporaryRedirect:
				if statusResponse.RedirectURL == "" {
					wf.Logger.Error("Received 307 redirect but no redirect URL provided")
					return nil, ConvertToVSAError(fmt.Errorf("received 307 redirect but no redirect URL provided"))
				}
				currentRedirectURL = statusResponse.RedirectURL
				wf.Logger.Debug("Following redirect", "redirectURL", currentRedirectURL)
			case http.StatusInternalServerError, http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
				wf.Logger.Error("ADC delete operation failed", "statusCode", statusResponse.StatusCode)
				return nil, ConvertToVSAError(fmt.Errorf("ADC delete operation failed with status code: %d", statusResponse.StatusCode))
			default:
				wf.Logger.Warn("Unexpected status code", "statusCode", statusResponse.StatusCode)
				// For any other status code, treat as error
				return nil, ConvertToVSAError(fmt.Errorf("ADC delete operation failed with unexpected status code: %d", statusResponse.StatusCode))
			}
			if !pollingCompleted {
				// Calculate progressive sleep duration based on elapsed time
				elapsed := workflow.Now(ctx).Sub(startTime)
				sleepDuration := calculateProgressiveSleepDuration(elapsed)

				// Check if we've exceeded the maximum time limit
				if elapsed > maxPollingTimeLimit {
					wf.Logger.Warn("Polling exceeded maximum time limit", "elapsed", elapsed, "maxLimit", maxPollingTimeLimit)
					return nil, ConvertToVSAError(fmt.Errorf("ADC delete operation exceeded maximum time limit of %v", maxPollingTimeLimit))
				}

				wf.Logger.Debug("Sleeping before next polling attempt", "sleepDuration", sleepDuration, "elapsed", elapsed)
				err = workflow.Sleep(ctx, sleepDuration)
				if err != nil {
					wf.Logger.Error("Sleep failed", "error", err)
					return nil, ConvertToVSAError(err)
				}
			}
		}
	case http.StatusOK:
		wf.Logger.Info("Backup deletion completed immediately")
	case http.StatusNotFound:
		wf.Logger.Info("Backup not found")
	default:
		return nil, ConvertToVSAError(fmt.Errorf("ADC delete request failed with status code: %d", adcResponse.StatusCode))
	}

	// Step 9: Calculate logical size for orphan backup deletion (non-blocking)
	logicalSizeCtx, cancel := workflow.WithCancel(ctx)
	defer cancel()

	logicalSizeCtx = workflow.WithActivityOptions(logicalSizeCtx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute * 5,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})

	// Check if backup is latest and update logical size if needed
	var isLatestBackup bool
	if utils.EnableBackupVaultSwitching {
		err = workflow.ExecuteActivity(logicalSizeCtx, backupActivity.IsLatestBackupInVaultActivity, backup.UUID, backup.VolumeUUID, backup.BackupVaultID).Get(logicalSizeCtx, &isLatestBackup)
	} else {
		err = workflow.ExecuteActivity(logicalSizeCtx, backupActivity.IsLatestBackupAnyStateActivity, backup.UUID, backup.VolumeUUID).Get(logicalSizeCtx, &isLatestBackup)
	}
	if err != nil {
		log.Warnf("Skipping logical size calculation due to error: %v", err)
	} else if !isLatestBackup {
		endpointUUID := ""
		if backup.Attributes != nil {
			endpointUUID = backup.Attributes.EndpointUUID
		}
		var sizeResult *activities.LogicalSizeUpdateResult
		err = workflow.ExecuteActivity(logicalSizeCtx, adcActivity.FetchLogicalSizeAndUpdateActivity, backup.VolumeUUID, endpointUUID, adcParams, serviceURL).Get(logicalSizeCtx, &sizeResult)
		if err != nil {
			log.Warnf("Failed to update logical size after 3 attempts: %v", err)
		} else if backupVault != nil && backupVault.ServiceType == activities.GCBDRServiceType && backup.ID > 0 && sizeResult != nil {
			delta := sizeResult.OldSize - sizeResult.NewSize
			if delta > 0 {
				if bpErr := workflow.ExecuteActivity(logicalSizeCtx, backupActivity.BackPropagateGcbdrChainSizesForDeletedBackupActivity, backup.VolumeUUID, adcParams.DestEndpointUUID, backup.ID, sizeResult.LatestBackupID, delta).Get(logicalSizeCtx, nil); bpErr != nil {
					log.Warnf("Failed to back-propagate GCBDR chain sizes (ADC) for backup %s: %v", backup.UUID, bpErr)
				}
			}
		}
	}

	if recalcErr := workflow.ExecuteActivity(logicalSizeCtx, backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity, backup.VolumeUUID).Get(logicalSizeCtx, nil); recalcErr != nil {
		log.Warnf("Failed to recalculate backup chain bytes: %v", recalcErr)
	}

	// Step 10: Cleanup Cloud Run service
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

	// Step 11: Remove roles from service account
	err = workflow.ExecuteActivity(ctx, adcActivity.RemoveRolesFromServiceAccount, bucketDetails.TenantProjectNumber, serviceAccountID, roles).Get(ctx, nil)
	if err != nil {
		log.Errorf("Failed to remove roles from service account: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Step 12: Cleanup service account
	err = workflow.ExecuteActivity(ctx, adcActivity.DeleteSA, bucketDetails.TenantProjectNumber, serviceAccountID).Get(ctx, nil)
	if err != nil {
		log.Errorf("Failed to cleanup service account: %v", err)
		return nil, ConvertToVSAError(err)
	}

	log.Infof("ADC workflow completed successfully for %s", backup.Name)
	return nil, nil
}

// calculateProgressiveSleepDuration calculates sleep duration based on elapsed time
// 5 sec for first 5 min, 10 sec for next 10 min, 5 min for 1 hr, then 10 min for 6 days max
func calculateProgressiveSleepDuration(elapsed time.Duration) time.Duration {
	// First phase: sleep for 5 seconds
	if elapsed < firstPhaseThreshold {
		return firstPhaseSleepDuration
	}

	// Second phase: sleep for 10 seconds
	if elapsed < secondPhaseThreshold {
		return secondPhaseSleepDuration
	}

	// Third phase: sleep for 5 minutes
	if elapsed < thirdPhaseThreshold {
		return thirdPhaseSleepDuration
	}

	// Fourth phase: sleep for 10 minutes (up to 6 days max)
	return fourthPhaseSleepDuration
}
