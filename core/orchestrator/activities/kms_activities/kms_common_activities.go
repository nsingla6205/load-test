package kms_activities

import (
	"context"
	"encoding/base64"
	"encoding/json"
	goErrors "errors"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/async"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/kms_configurations"
	cvpClientModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/retry"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

var (
	pollCvpOperationForWorkflow          = _pollCvpOperationForWorkflow
	getGcpService                        = hyperscaler.GetGCPService
	GcpServiceCreateServiceAccountKey    = _gcpServiceCreateServiceAccountKey
	DeleteServiceAccountKeysExcludingKey = _deleteServiceAccountKeysExcludingKey
	gcpGrantServiceAccountRole           = _gcpGrantServiceAccountRole
	gcpDisableServiceAccount             = _gcpDisableServiceAccount
	gcpEnableServiceAccount              = _gcpEnableServiceAccount
	retryDo                              = retry.RetryDoWithTimeout
	AccessCryptoKeyAndEncryptData        = _accessCryptoKeyAndEncryptData
	getImpersonatedKmsService            = google.GetImpersonatedKmsService
	getDirectKmsService                  = google.GetDirectKmsService
	synchronizeServiceAccountKeys        = _synchronizeServiceAccountKeys
	isServiceAccountKeyPresentInGCP      = _isServiceAccountKeyPresentInGCP
	extractPrivateKeyIDFromPassword      = _extractPrivateKeyID
	UpdateKmsConfigHealth                = _updateKmsConfigHealth
	FailedKmsConfigCreateActivity        = _failedKmsConfigCreateActivity
	getSignedJwtToken                    = auth.GetSignedJwtToken
)

const (
	serviceNameCmek                        = "cmek"
	ErrTypeKmsConfigNotFound               = "KmsConfigNotFound"
	ErrTypeKmsConfigNotReachableVsaCluster = "KmsConfigNotReachableVsaCluster"
	ErrTypeDNSExists                       = "DNSEntryExists"
	ErrTypeSignedTokenFailed               = "SignedTokenFailed"
	RetryTimeOutForGetCryptoKey            = 30 * time.Second
	RetryIntervalForGetCryptoKey           = 5 * time.Second
	GcpKmsConfigHealthError                = "specified key <key_name> in <key_ring> does not exist or service permissions are incorrect"
	GcpKmsConfigImpersonationHealthError   = "impersonate: status code 403"
	RetryTimeOutForDescribeSDEJob          = 1 * time.Minute
	RetryIntervalForDescribeSDEJob         = 10 * time.Second
)

type KmsConfigActivity struct {
	SE database.Storage
}

func _pollCvpOperationForWorkflow(ctx context.Context, cvpClient cvpapi.Cvp, operationParams *async.V1betaDescribeOperationParams) (*cvpClientModels.OperationV1beta, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Polling for operation %s", operationParams.OperationID)
	operationResponse, err := cvpClient.Async.V1betaDescribeOperation(operationParams)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError("failed to describe operation", "DescribeOperationError", err)
	}
	// Check if the operation is done
	if !*operationResponse.Payload.Done {
		logger.Debug("Operation in progress ", operationParams.OperationID)
		return nil, errors.New(fmt.Sprintf("operation %s in progress, trying again", operationParams.OperationID))
	} else if operationResponse.Payload.Error != nil {
		msg := fmt.Errorf("operation failed: %v", operationResponse.Payload.Error)
		return nil, temporal.NewNonRetryableApplicationError("operation failed", "OperationError", msg)
	}
	return operationResponse.Payload, nil
}

// PollKmsConfigOperationActivity polls the KMS configuration operation until it is done.
func (j *KmsConfigActivity) PollKmsConfigOperationActivity(ctx context.Context, params *common.PollKmsConfigParams) error {
	activity.RecordHeartbeat(ctx, "Starting PollKmsConfigOperationActivity")
	defer activity.RecordHeartbeat(ctx, "Finished PollKmsConfigOperationActivity")
	logger := util.GetLogger(ctx)

	// Generate a fresh JWT token to avoid token expiration during long-running workflows
	jwtToken, err := getSignedJwtToken(params.ProjectNumber)
	if err != nil {
		logger.Errorf("Failed to get signed token for PollKmsConfigOperationActivity: %v", err)
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeSignedTokenFailed, err)
	}

	cvpClient := createClient(logger, jwtToken)

	// Check if the operation is done
	if !params.OperationDone {
		activity.RecordHeartbeat(ctx, "Polling KMS configuration operation status")
		// Extract the operation UUID
		operationUUID := utils.GetOperationUUID(params.OperationUri)
		operationParams := async.NewV1betaDescribeOperationParams()
		operationParams.OperationID = operationUUID
		operationParams.ProjectNumber = params.ProjectNumber
		operationParams.LocationID = params.LocationID
		operationParams.XCorrelationID = &params.XCorrelationID
		_, err := pollCvpOperationForWorkflow(ctx, cvpClient, operationParams)
		if err != nil {
			return err
		}
	}
	return nil
}

func GetResponseforPollCvpOperation(ctx context.Context, responsePayloadName string, projectNumber string, locationID string) (*cvpClientModels.OperationV1beta, error) {
	logger := util.GetLogger(ctx)

	// Generate a fresh JWT token for each poll to avoid token expiration during long-running operations
	jwtToken, err := getSignedJwtToken(projectNumber)
	if err != nil {
		logger.Errorf("Failed to get signed token for SDE CMEK migration polling operation: %v", err)
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeSignedTokenFailed, err)
	}

	cvpClient := createClient(logger, jwtToken)

	operationUUID := utils.GetOperationUUID(responsePayloadName)
	operationParams := async.NewV1betaDescribeOperationParams()
	operationParams.OperationID = operationUUID
	operationParams.ProjectNumber = projectNumber
	operationParams.LocationID = locationID

	payload, err := pollCvpOperationForWorkflow(ctx, cvpClient, operationParams)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

// CreateVSAKmsConfigSAKeyActivity creates a service account key for the given KMS configuration.
func (j *KmsConfigActivity) CreateVSAKmsConfigSAKeyActivity(ctx context.Context, kmsConfig *datamodel.KmsConfig) (*datamodel.KmsConfig, error) {
	activity.RecordHeartbeat(ctx, "Starting CreateVSAKmsConfigSAKeyActivity")
	defer activity.RecordHeartbeat(ctx, "Finished CreateVSAKmsConfigSAKeyActivity")
	se := j.SE
	gcpService, err := getGcpService(ctx)
	if err != nil {
		return nil, err
	}
	vsaEmail := utils.RemovePrefix(kmsConfig.KmsAttributes.SdeServiceAccountEmail, SDEShortTermSAPrefix)
	if kmsConfig.KmsAttributes.IsVCPCreated() {
		vsaEmail = kmsConfig.KmsAttributes.VcpServiceAccountEmail
	}
	dbAccount, err := se.GetServiceAccountFromEmail(ctx, vsaEmail)
	if err != nil && !errors.IsNotFoundErr(err) {
		return nil, errors2.WrapAsTemporalApplicationError(errors2.NewVCPError(errors2.ErrGettingKmsServiceAccount, err))
	} else if errors.IsNotFoundErr(err) {
		activity.RecordHeartbeat(ctx, "Creating service account key for KMS config")
		serviceAccountKey, err := GcpServiceCreateServiceAccountKey(gcpService, ctx, vsaEmail)
		if err != nil {
			return nil, err
		}
		sa := &datamodel.ServiceAccount{
			Name:                           kmsConfig.Name,
			Description:                    kmsConfig.Description,
			AccountID:                      kmsConfig.AccountID,
			ServiceName:                    serviceNameCmek,
			ServiceAccountEmail:            vsaEmail,
			ServiceAccountPasswordLocation: serviceAccountKey.PrivateKeyData,
		}
		dbAccount, err = se.CreateKmsServiceAccount(ctx, sa)
		if err != nil {
			return nil, err
		}
	}
	// For accounts where db record already exists, check if the SA key needs re-synchronization.
	// Re-sync is needed when: (a) password is empty, or (b) VALIDATE_SA_KEY_IN_GCP is enabled and
	// the specific key stored in DB no longer exists in GCP (e.g. deleted from Google Console).
	password, err := utils.DecryptPassword(log.Secret(dbAccount.ServiceAccountPasswordLocation))
	if err != nil {
		return nil, errors2.WrapAsTemporalApplicationError(errors2.NewVCPError(errors2.ErrDecryptingServiceAccountPassword, err))
	}
	needsSync := false
	if password != nil && *password == "" {
		needsSync = true
	} else if password != nil && *password != "" && utils.ValidateSAKeyInGCP {
		// Extract the private_key_id from the stored key and verify it still exists in GCP
		keyID, extractErr := extractPrivateKeyIDFromPassword(*password)
		if extractErr != nil {
			util.GetLogger(ctx).Warnf("Failed to extract key ID from stored password for %s, re-synchronizing: %v", dbAccount.ServiceAccountEmail, extractErr)
			needsSync = true
		} else {
			keyExists, keyErr := isServiceAccountKeyPresentInGCP(ctx, gcpService, dbAccount.ServiceAccountEmail, keyID)
			if keyErr != nil {
				util.GetLogger(ctx).Warnf("Failed to validate SA key %s in GCP for %s, re-synchronizing: %v", keyID, dbAccount.ServiceAccountEmail, keyErr)
				needsSync = true
			} else if !keyExists {
				util.GetLogger(ctx).Warnf("SA key %s not found in GCP for %s, re-synchronizing", keyID, dbAccount.ServiceAccountEmail)
				needsSync = true
			}
		}
	}
	if needsSync {
		encryptedKey, err := synchronizeServiceAccountKeys(ctx, gcpService, dbAccount.ServiceAccountEmail)
		if err != nil {
			return nil, errors2.WrapAsTemporalApplicationError(errors2.NewVCPError(errors2.ErrorSynchronizingServiceAccountKey, err))
		}
		dbAccount, err = se.UpdateServiceAccountEmailAndKey(ctx, dbAccount.UUID, dbAccount.ServiceAccountEmail, *encryptedKey)
		if err != nil {
			return nil, err
		}
	}
	activity.RecordHeartbeat(ctx, "Updating service account state")
	_, err = se.UpdateServiceAccountState(ctx, dbAccount.UUID, datamodel.AccountStateEnabled, datamodel.LifeCycleStateAvailableDetails)
	if err != nil {
		return nil, err
	}

	kmsConfig.ServiceAccount = dbAccount
	err = se.UpdateKmsConfig(ctx, kmsConfig.UUID, map[string]interface{}{
		"ServiceAccountID": dbAccount.ID,
	})
	if err != nil {
		return nil, err
	}
	return kmsConfig, nil
}

func _gcpServiceCreateServiceAccountKey(cloudService hyperscaler.Services, ctx context.Context, email string) (*hyperscalermodels.ServiceAccountKey, error) {
	// Create a service account key for the given service account email
	return cloudService.CreateServiceAccountKey(ctx, email)
}

func _deleteServiceAccountKeysExcludingKey(ctx context.Context, gcpService *google.GcpServices, email, keyToExclude string) error {
	return gcpService.DeleteServiceAccountKeysExcludingKey(ctx, email, keyToExclude)
}

func _gcpGrantServiceAccountRole(ctx context.Context, gcpService *google.GcpServices, serviceAccountEmail, member, role string) error {
	return gcpService.GrantServiceAccountRole(ctx, serviceAccountEmail, member, role)
}

func _gcpDisableServiceAccount(gcpService *google.GcpServices, saEmail string) error {
	return gcpService.DisableServiceAccount(saEmail)
}

func _gcpEnableServiceAccount(gcpService *google.GcpServices, saEmail string) error {
	return gcpService.EnableServiceAccount(saEmail)
}

// GrantRoleActivity grants the specified role to the service account for the given KMS configuration.
func (j *KmsConfigActivity) GrantRoleActivity(ctx context.Context, kmsConfig *datamodel.KmsConfig) error {
	activity.RecordHeartbeat(ctx, "Starting GrantRoleActivity")
	defer activity.RecordHeartbeat(ctx, "Finished GrantRoleActivity")
	gcpService, err := getGcpService(ctx)
	if err != nil {
		return err
	}
	activity.RecordHeartbeat(ctx, "Granting service account role")
	return gcpGrantServiceAccountRole(ctx, gcpService, kmsConfig.KmsAttributes.SdeServiceAccountEmail, kmsConfig.ServiceAccount.ServiceAccountEmail, TokenCreatorRole)
}

// FailedKmsConfigCreateActivity updates the KMS configuration state to "error" with the provided error message.
func (j *KmsConfigActivity) FailedKmsConfigCreateActivity(ctx context.Context, kmsConfig *datamodel.KmsConfig, errMsg string, location string) error {
	activity.RecordHeartbeat(ctx, "Starting FailedKmsConfigCreateActivity")
	defer activity.RecordHeartbeat(ctx, "Finished FailedKmsConfigCreateActivity")
	return _failedKmsConfigCreateActivity(ctx, j.SE, kmsConfig, errMsg, location)
}

func _failedKmsConfigCreateActivity(ctx context.Context, se database.Storage, kmsConfig *datamodel.KmsConfig, errMsg, location string) error {
	logger := util.GetLogger(ctx)

	// DB cleanup: mark KMS config as deleted and service account as error
	_, err := se.DeleteKmsConfig(ctx, kmsConfig.UUID, datamodel.LifeCycleStateDeleted, errMsg)
	if err != nil {
		return err
	}

	if kmsConfig.ServiceAccount != nil {
		_, err = se.UpdateServiceAccountState(ctx, kmsConfig.ServiceAccount.UUID, datamodel.LifeCycleStateError, errMsg)
		if err != nil {
			return err
		}
	}

	// VCP-created configs have no SDE counterpart to delete
	if kmsConfig.KmsAttributes != nil && kmsConfig.KmsAttributes.IsVCPCreated() {
		return nil
	}

	// SDE path: delete the KMS config from SDE via CVP client
	jwtToken, err := getSignedJwtToken(kmsConfig.CustomerProjectID)
	if err != nil {
		logger.Errorf("Failed to get signed token for FailedKmsConfigCreateActivity: %v", err)
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeSignedTokenFailed, err)
	}

	cvpClient := createClient(logger, jwtToken)

	deleteParams := &kms_configurations.V1betaDeleteKmsConfigurationParams{
		KmsConfigID:   kmsConfig.UUID,
		ProjectNumber: kmsConfig.CustomerProjectID,
		LocationID:    location,
	}
	response, _, cvpErr := cvpClient.KmsConfigurations.V1betaDeleteKmsConfiguration(deleteParams)
	if cvpErr != nil {
		switch cvpErr.(type) {
		case *kms_configurations.V1betaDeleteKmsConfigurationNotFound:
			return temporal.NewNonRetryableApplicationError("failed to delete KMS configuration", ErrTypeKmsConfigNotFound, err)
		}
		return cvpErr
	}

	if response != nil && !*response.Payload.Done {
		operationUUID := utils.GetOperationUUID(response.Payload.Name)
		operationParams := async.NewV1betaDescribeOperationParams()
		operationParams.OperationID = operationUUID
		operationParams.ProjectNumber = kmsConfig.CustomerProjectID
		operationParams.LocationID = location

		err = retryDo(ctx, RetryTimeOutForDescribeSDEJob, RetryIntervalForDescribeSDEJob, "PollCvpOperationForWorkflow", func(attempt int) (bool, error) {
			_, err = pollCvpOperationForWorkflow(ctx, cvpClient, operationParams)
			if err != nil {
				return true, retry.NewRetriableErr(err.Error())
			}
			return false, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// CreatedKmsConfigActivity updates the KMS configuration state to created
func (j *KmsConfigActivity) CreatedKmsConfigActivity(ctx context.Context, kmsConfig *datamodel.KmsConfig) error {
	activity.RecordHeartbeat(ctx, "Starting CreatedKmsConfigActivity")
	defer activity.RecordHeartbeat(ctx, "Finished CreatedKmsConfigActivity")
	se := j.SE
	kmsConfig.State = datamodel.LifeCycleStateCreated
	kmsConfig.StateDetails = datamodel.LifeCycleStateCreatedDetails
	activity.RecordHeartbeat(ctx, "Updating KMS configuration to created state")
	_, err := se.UpdateKmsConfigState(ctx, kmsConfig.UUID, kmsConfig.State, kmsConfig.StateDetails)
	if err != nil {
		return err
	}
	_, err = se.UpdateServiceAccountState(ctx, kmsConfig.ServiceAccount.UUID, datamodel.AccountStateEnabled, datamodel.LifeCycleStateReadyDetails)
	return err
}

func (j *KmsConfigActivity) UpdatePoolWithKmsConfigActivity(ctx context.Context, pool *datamodel.Pool, kmsConfigID string) (*datamodel.Pool, error) {
	activity.RecordHeartbeat(ctx, "Starting UpdatePoolWithKmsConfigActivity")
	defer activity.RecordHeartbeat(ctx, "Finished UpdatePoolWithKmsConfigActivity")
	se := j.SE
	activity.RecordHeartbeat(ctx, "Updating pool with KMS configuration")
	return se.UpdatePoolWithKmsConfigID(ctx, pool, kmsConfigID)
}

func (j *KmsConfigActivity) AccessCryptoKeyAndEncryptDataWithImpersonationActivity(ctx context.Context, kmsConfig *datamodel.KmsConfig) error {
	activity.RecordHeartbeat(ctx, "Starting AccessCryptoKeyAndEncryptDataWithImpersonationActivity")
	defer activity.RecordHeartbeat(ctx, "Finished AccessCryptoKeyAndEncryptDataWithImpersonationActivity")
	err := AccessCryptoKeyAndEncryptData(ctx, kmsConfig, kmsConfig.ServiceAccount.ServiceAccountPasswordLocation, RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey)
	if err != nil {
		return err
	}
	return nil
}

// safeRecordHeartbeat safely records a heartbeat only if the context is an activity context.
// This prevents panics when the function is called from non-activity contexts (e.g., HTTP handlers).
func safeRecordHeartbeat(ctx context.Context, details ...interface{}) {
	defer func() {
		if r := recover(); r != nil {
			// Ignore panic - we're not in an activity context, so RecordHeartbeat panicked
		}
	}()
	activity.RecordHeartbeat(ctx, details...)
}

func _accessCryptoKeyAndEncryptData(ctx context.Context, kmsConfig *datamodel.KmsConfig, secretPassword string, timeout, timeoutInterval time.Duration) error {
	logger := util.GetLogger(ctx)

	// Process the service account credentials to get the scope credentials
	scopeCreds, err := utils.ProcessCredentials(ctx, secretPassword)
	if err != nil {
		return err
	}

	isVCPCreated := kmsConfig.KmsAttributes != nil && kmsConfig.KmsAttributes.IsVCPCreated()
	safeRecordHeartbeat(ctx, "Accessing crypto key")
	// Define the name of the crypto key you want to get details about.
	cryptoKeyPath := utils.ParsedKeyFullPathResource{
		ProjectID: kmsConfig.KeyProjectID,
		Location:  kmsConfig.KeyRingLocation,
		KeyRing:   kmsConfig.KeyRing,
		CryptoKey: kmsConfig.KeyName,
	}.String()

	accessMethod := "impersonation"
	if isVCPCreated {
		accessMethod = "direct"
	}

	// Get the crypto key details.
	var errAccess error
	var encryptTestData func() error
	if isVCPCreated {
		kmsService, errDirect := getDirectKmsService(ctx, scopeCreds)
		if errDirect != nil {
			return fmt.Errorf("failed to create direct KMS service: %w", errDirect)
		}
		// When a grant is present, the caller does not have direct read access to the
		// crypto key resource, so skip the CryptoKeys.Get + ValidateKeyProperties step
		// and rely on the encryption test below to verify usability.
		if kmsConfig.KmsGrant != "" {
			logger.Debugf("Grant %s present for crypto key %s, skipping CryptoKeys.Get and key property validation", kmsConfig.KmsGrant, cryptoKeyPath)
		} else {
			errAccess = retryDo(ctx, timeout, timeoutInterval, "AccessCryptoKeyAndEncryptData", func(attempt int) (bool, error) {
				cryptoKey, errGetCrypto := kmsService.Projects.Locations.KeyRings.CryptoKeys.Get(cryptoKeyPath).Context(ctx).Do()
				if errGetCrypto != nil {
					if msg, ok := utils.IsKmsKeyUnreachable(errGetCrypto); ok {
						return false, errors2.NewVCPError(errors2.ErrKMSKeyUnreachable, goErrors.New(msg))
					}
					if msg, ok := utils.IsKmsPermissionDenied(errGetCrypto); ok {
						return false, errors2.NewVCPError(errors2.ErrKMSPermissionDenied, goErrors.New(msg))
					}
					return true, retry.NewRetriableErr(fmt.Sprintf("Projects.Locations.KeyRings.CryptoKeys.Get: %v", errGetCrypto))
				}
				errValidate := utils.ValidateKeyProperties(cryptoKey, kmsConfig.KeyName, kmsConfig.KeyRing)
				if errValidate != nil {
					// Validation failures (e.g., disabled/destroyed key) are user-facing and should not be retried.
					return false, errValidate
				}
				return false, nil
			})
		}
		// For grant-based access, encrypt against the grant-scoped resource path so the
		// grant is honored; otherwise use the plain crypto key path.
		encryptKeyPath := cryptoKeyPath
		if kmsConfig.KmsGrant != "" {
			encryptKeyPath = utils.ParsedKeyFullPathResource{
				ProjectID: kmsConfig.KeyProjectID,
				Location:  kmsConfig.KeyRingLocation,
				KeyRing:   kmsConfig.KeyRing,
				CryptoKey: kmsConfig.KeyName,
				Grant:     kmsConfig.KmsGrant,
			}.StringWithGrant()
		}
		encryptTestData = func() error {
			plainText := "test"
			req := utils.ReturnEncryptRequest(plainText)
			safeRecordHeartbeat(ctx, "Verifying encryption capability")
			errEncrypt := retryDo(ctx, RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey, "AccessCryptoKeyAndEncryptData", func(attempt int) (bool, error) {
				_, err := kmsService.Projects.Locations.KeyRings.CryptoKeys.Encrypt(encryptKeyPath, req).Do()
				if err != nil {
					if msg, ok := utils.IsKmsKeyUnreachable(err); ok {
						return false, errors2.NewVCPError(errors2.ErrKMSKeyUnreachable, goErrors.New(msg))
					}
					if msg, ok := utils.IsKmsPermissionDenied(err); ok {
						return false, errors2.NewVCPError(errors2.ErrKMSPermissionDenied, goErrors.New(msg))
					}
					return true, retry.NewRetriableErr(fmt.Sprintf("Projects.Locations.KeyRings.CryptoKeys.Encrypt: %v", err))
				}
				logger.Debugf("Successfully encrypted test data with crypto key %s using %s access", encryptKeyPath, accessMethod)
				return false, nil
			})
			return errEncrypt
		}
	} else {
		kmsService, errImpersonated := getImpersonatedKmsService(ctx, kmsConfig.KmsAttributes.SdeServiceAccountEmail, scopeCreds)
		if errImpersonated != nil {
			return fmt.Errorf("failed to create KMS service: %w", errImpersonated)
		}
		// When a grant is present, the caller does not have direct read access to the
		// crypto key resource, so skip the CryptoKeys.Get + ValidateKeyProperties step
		// and rely on the encryption test below to verify usability.
		if kmsConfig.KmsGrant != "" {
			logger.Debugf("Grant %s present for crypto key %s, skipping CryptoKeys.Get and key property validation", kmsConfig.KmsGrant, cryptoKeyPath)
		} else {
			errAccess = retryDo(ctx, timeout, timeoutInterval, "AccessCryptoKeyAndEncryptData", func(attempt int) (bool, error) {
				cryptoKey, errGetCrypto := kmsService.Projects.Locations.KeyRings.CryptoKeys.Get(cryptoKeyPath).Context(ctx).Do()
				if errGetCrypto != nil {
					if msg, ok := utils.IsKmsKeyUnreachable(errGetCrypto); ok {
						return false, errors2.NewVCPError(errors2.ErrKMSKeyUnreachable, goErrors.New(msg))
					}
					if msg, ok := utils.IsKmsPermissionDenied(errGetCrypto); ok {
						return false, errors2.NewVCPError(errors2.ErrKMSPermissionDenied, goErrors.New(msg))
					}
					return true, retry.NewRetriableErr(fmt.Sprintf("Projects.Locations.KeyRings.CryptoKeys.Get: %v", errGetCrypto))
				}
				errValidate := utils.ValidateKeyProperties(cryptoKey, kmsConfig.KeyName, kmsConfig.KeyRing)
				if errValidate != nil {
					// Validation failures (e.g., disabled/destroyed key) are user-facing and should not be retried.
					return false, errValidate
				}
				return false, nil
			})
		}
		// For grant-based access, encrypt against the grant-scoped resource path so the
		// grant is honored; otherwise use the plain crypto key path.
		encryptKeyPath := cryptoKeyPath
		if kmsConfig.KmsGrant != "" {
			encryptKeyPath = utils.ParsedKeyFullPathResource{
				ProjectID: kmsConfig.KeyProjectID,
				Location:  kmsConfig.KeyRingLocation,
				KeyRing:   kmsConfig.KeyRing,
				CryptoKey: kmsConfig.KeyName,
				Grant:     kmsConfig.KmsGrant,
			}.StringWithGrant()
		}
		encryptTestData = func() error {
			plainText := "test"
			req := utils.ReturnEncryptRequest(plainText)
			safeRecordHeartbeat(ctx, "Verifying encryption capability")
			errEncrypt := retryDo(ctx, RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey, "AccessCryptoKeyAndEncryptData", func(attempt int) (bool, error) {
				_, err := kmsService.Projects.Locations.KeyRings.CryptoKeys.Encrypt(encryptKeyPath, req).Do()
				if err != nil {
					if msg, ok := utils.IsKmsKeyUnreachable(err); ok {
						return false, errors2.NewVCPError(errors2.ErrKMSKeyUnreachable, goErrors.New(msg))
					}
					if msg, ok := utils.IsKmsPermissionDenied(err); ok {
						return false, errors2.NewVCPError(errors2.ErrKMSPermissionDenied, goErrors.New(msg))
					}
					return true, retry.NewRetriableErr(fmt.Sprintf("Projects.Locations.KeyRings.CryptoKeys.Encrypt: %v", err))
				}
				logger.Debugf("Successfully encrypted test data with crypto key %s using %s access", encryptKeyPath, accessMethod)
				return false, nil
			})
			return errEncrypt
		}
	}
	if errAccess != nil {
		logger.Errorf("Failed to access crypto key %s - %s", cryptoKeyPath, errAccess.Error())
		return errAccess
	}

	errEncrypt := encryptTestData()
	if errEncrypt != nil {
		logger.Errorf("Failed to encrypt data using KMS key: %v", errEncrypt)
		return errEncrypt
	}

	return nil
}

func _synchronizeServiceAccountKeys(ctx context.Context, cloudService hyperscaler.Services, email string) (*string, error) {
	// If db password is empty, then delete and recreate key
	// Delete existing keys
	err := cloudService.DeleteAllServiceAccountKeys(ctx, email)
	if err != nil {
		return nil, err
	}

	// Create key and update db
	key, err := cloudService.CreateServiceAccountKey(ctx, email)
	if err != nil {
		return nil, err
	}

	return &key.PrivateKeyData, nil
}

// _isServiceAccountKeyPresentInGCP checks whether the specific SA key (by keyID) still exists in GCP.
// This guards against the case where a key is deleted from the Google Console
// but the encrypted key data still exists in our DB.
func _isServiceAccountKeyPresentInGCP(ctx context.Context, gcpService *google.GcpServices, email, keyID string) (bool, error) {
	return gcpService.IsServiceAccountKeyPresent(ctx, email, keyID)
}

// _extractPrivateKeyID extracts the private_key_id from a base64-encoded GCP service account key JSON.
// The decrypted password from DB is the raw PrivateKeyData returned by GCP, which is base64-encoded JSON.
func _extractPrivateKeyID(keyData string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(keyData)
	if err != nil {
		return "", fmt.Errorf("failed to base64 decode key data: %w", err)
	}
	var keyJSON struct {
		PrivateKeyID string `json:"private_key_id"`
	}
	if err := json.Unmarshal(decoded, &keyJSON); err != nil {
		return "", fmt.Errorf("failed to parse key JSON: %w", err)
	}
	if keyJSON.PrivateKeyID == "" {
		return "", fmt.Errorf("private_key_id not found in key JSON")
	}
	return keyJSON.PrivateKeyID, nil
}

func (j *KmsConfigActivity) VerifyVsaKmsReachabilityActivity(ctx context.Context, kmsConfigUUID string, getVerifyError bool) error {
	activity.RecordHeartbeat(ctx, "Starting VerifyVsaKmsReachabilityActivity")
	defer activity.RecordHeartbeat(ctx, "Finished VerifyVsaKmsReachabilityActivity")
	se := j.SE

	kmsConfig, err := se.GetKmsConfigByUUID(ctx, kmsConfigUUID)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return temporal.NewNonRetryableApplicationError("KMS configuration not found", ErrTypeKmsConfigNotFound, err)
		}
		return err
	}

	activity.RecordHeartbeat(ctx, "Verifying KMS reachability from VSA")
	// Access Crypto key and encrypt data
	errAccessAndEncrypt := AccessCryptoKeyAndEncryptData(
		ctx, kmsConfig, kmsConfig.ServiceAccount.ServiceAccountPasswordLocation,
		RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey)

	// Prepare KmsConfig check model based on the access check
	kmsConfigCheck := &models.KmsConfigCheck{}
	kmsConfigCheck.IsHealthy = true
	kmsConfigCheck.ProxyType = models.ProxyTypeVcp
	if errAccessAndEncrypt != nil {
		kmsConfigCheck.HealthError = errAccessAndEncrypt.Error()
		kmsConfigCheck.IsHealthy = false
	}
	kmsConfigCheck.KmsConfig = &models.KmsConfig{
		BaseModel: models.BaseModel{
			UUID: kmsConfig.UUID,
		},
	}

	activity.RecordHeartbeat(ctx, "Updating KMS configuration health status")
	// Update the KmsConfig health status in the database
	_, err = UpdateKmsConfigHealth(ctx, se, kmsConfigCheck)
	if err != nil {
		return err
	}

	// for some cases just want check to pass even if there is issue with AccessCryptoKeyAndEncryptData e.g pool/volume create operation
	// for create pool operation error is returned
	if getVerifyError {
		if errAccessAndEncrypt != nil {
			return errors2.WrapAsTemporalApplicationError(errAccessAndEncrypt)
		}
		return nil
	}
	return nil
}

func _updateKmsConfigHealth(ctx context.Context, se database.Storage, configCheck *models.KmsConfigCheck) (*datamodel.KmsConfig, error) {
	kmsConfig, err := se.GetKmsConfigByUUID(ctx, configCheck.KmsConfig.UUID)
	if err != nil {
		return nil, err
	}
	kmsConfigInUse, err := se.IsKmsConfigInUse(ctx, kmsConfig.UUID)
	if err != nil {
		return nil, err
	}

	state := datamodel.LifeCycleStateUnknown
	stateDetails := datamodel.LifeCycleStateUnknownDetails

	switch configCheck.IsHealthy {
	case true:
		state = datamodel.LifeCycleStateREADY
		stateDetails = datamodel.LifeCycleStateReadyDetails
		// keep the state as in use if the KMS config is in use (in use meaning that there are SVMs using this KMS config)
		if kmsConfigInUse {
			state = datamodel.LifeCycleStateInUse
			stateDetails = datamodel.LifeCycleStateAvailableDetails
		}
	case false:
		// If the KMS config is in error state, do not update the state to ready.
		state = datamodel.LifeCycleStateError
		stateDetails = configCheck.HealthError
		if !kmsConfigInUse {
			healthErrorMessage := strings.Replace(strings.Replace(GcpKmsConfigHealthError, "<key_name>", kmsConfig.KeyName, 1), "<key_ring>", kmsConfig.KeyRing, 1)
			// Keep the state as created if the health error message indicates that the key does not exist or service permissions are incorrect.
			if strings.Contains(stateDetails, healthErrorMessage) || strings.Contains(stateDetails, GcpKmsConfigImpersonationHealthError) {
				state = datamodel.LifeCycleStateCreated
			}
		}
	}

	// Update the KMS config state and details
	kmsConfig, err = se.UpdateKmsConfigState(ctx, kmsConfig.UUID, state, stateDetails)
	if err != nil {
		return nil, err
	}

	// Update the KMS config Attributes with the health check response for Cvp proxy type
	if configCheck.ProxyType == models.ProxyTypeCvp {
		kmsConfig.KmsAttributes.SdeKmsConfigIsHealthy = configCheck.IsHealthy
		kmsConfig.KmsAttributes.SdeKmsConfigHealthError = configCheck.HealthError
		kmsConfig, err = se.UpdateKmsConfigAttributes(ctx, kmsConfig.UUID, kmsConfig.KmsAttributes)
		if err != nil {
			return nil, err
		}
	}
	return kmsConfig, nil
}

func (j *KmsConfigActivity) GetSignedTokenActivity(ctx context.Context, projectNumber string) (string, error) {
	activity.RecordHeartbeat(ctx, "Starting GetSignedTokenActivity")
	defer activity.RecordHeartbeat(ctx, "Finished GetSignedTokenActivity")
	return auth.GetSignedJwtToken(projectNumber)
}
