package backgroundactivities

import (
	"context"
	"encoding/base64"
	"encoding/json"
	errstd "errors"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/metricsinterface"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	getGcpService                     = hyperscaler.ResolveCloudService
	gcpServiceCreateServiceAccountKey = kms_activities.GcpServiceCreateServiceAccountKey
	deleteServiceAccountKeyWithRetry  = func(ctx context.Context, cloudService hyperscaler.Services, keyName string) error {
		return cloudService.DeleteServiceAccountKey(ctx, keyName)
	}
	listPoolsByKmsConfigId            = ListPoolsByKmsConfigId
	syncKeyWithOntap                  = _syncKeyWithOntap
	extractKeyID                      = _extractKeyID
	extractKeyIDFromRawBase64         = _extractKeyIDFromRawBase64
	vcpNamespace                      = env.GetString("VCP_KUBERNETES_NAMESPACE", "vcp")
)

type kmsRotationLocker interface {
	Lock(ctx context.Context, logger log.Logger) error
	ClientID() string
	RenewLock(ctx context.Context) error
	Unlock(ctx context.Context) error
}

var newKmsRotationLocker = func(ctx context.Context, name string, options ...utils.Option) (kmsRotationLocker, error) {
	return utils.NewLocker(ctx, name, options...)
}

const (
	serviceNameCmek = "cmek"
	keyPrefix       = "/keys/"

	// Key limit thresholds - GCP allows max 10 keys per service account
	// These limits prevent hitting GCP's hard limit if DeleteOldSAKeyFromGCPActivity fails repeatedly
	maxTotalKeysBeforeRotation    = 8 // Leave buffer for operational flexibility
	maxPendingDeletionKeysAllowed = 5 // Trigger error if delete activity keeps failing

	// KMS rotation lock - per config to avoid concurrent create-SA-key for same config
	kmsRotationLockNamePrefix = "vcp-kms-"
)

// KMS rotation lock timing. Configurable via env (e.g. "2s", "30s", "10m").
var (
	kmsRotationLockMaxAcquireWait = env.GetDuration("KMS_ROTATION_LOCK_MAX_ACQUIRE_WAIT", 10*time.Minute)
	kmsRotationLockRetryWait      = env.GetDuration("KMS_ROTATION_LOCK_RETRY_WAIT", 2*time.Second)
	kmsRotationLockTTL            = env.GetDuration("KMS_ROTATION_LOCK_TTL", 30*time.Second)
)

// isLocalSetup returns true when LOCAL_SETUP env is true (default false). When true, acquire/release/renew lock are no-ops.
func isLocalSetup() bool {
	return env.GetBool("LOCAL_SETUP", false)
}

type RotateKmsSAKeyActivity struct {
	SE             database.Storage
	MetricsEmitter metricsinterface.KmsMetricsEmitter
}

func (a *RotateKmsSAKeyActivity) ListKmsConfigs(ctx context.Context) ([]*datamodel.KmsConfig, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	conditions := [][]interface{}{
		{"state in ?", []string{string(gcpserver.KmsConfigV1betaKmsStateINUSE), string(gcpserver.KmsConfigV1betaKmsStateREADY)}}}
	kmsConfigs, err := se.GetMultipleKmsConfigs(ctx, conditions)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to list kms service accounts: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return kmsConfigs, nil
}

// GetKmsConfigServiceAccount retrieves the service account for a specific KMS config
func (a *RotateKmsSAKeyActivity) GetKmsConfigServiceAccount(ctx context.Context, kmsConfigID string) (*datamodel.ServiceAccount, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Get KMS config by ID with service account preloaded
	kmsConfig, err := se.GetKmsConfigByUUID(ctx, kmsConfigID)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to get KMS config: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if kmsConfig.ServiceAccountID == nil || *kmsConfig.ServiceAccountID == 0 {
		logger.Error("KMS_KEY_ROTATION: No service account associated with KMS config", "kmsConfigID", kmsConfigID)
		return nil, errors.New("no service account associated with KMS config")
	}

	// Return the service account that should be preloaded
	if kmsConfig.ServiceAccount == nil {
		logger.Error("KMS_KEY_ROTATION: Service account not preloaded for KMS config", "kmsConfigID", kmsConfigID)
		return nil, errors.New("service account not found for KMS config")
	}

	return kmsConfig.ServiceAccount, nil
}

// GetKmsConfig retrieves a KMS config by its ID
func (a *RotateKmsSAKeyActivity) GetKmsConfig(ctx context.Context, kmsConfigID string) (*datamodel.KmsConfig, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	kmsConfig, err := se.GetKmsConfigByUUID(ctx, kmsConfigID)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to get KMS config: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return kmsConfig, nil
}

// ValidateKeyRotationRequiredResult contains the result of validation
type ValidateKeyRotationRequiredResult struct {
	RotationRequired bool
	CurrentKeyID     string
	Reason           string // Reason if rotation not required
	ServiceAccount   *datamodel.ServiceAccount
}

// isKmsConfigInValidState checks if KMS config is in a state that allows rotation
func isKmsConfigInValidState(state string) bool {
	return state == string(gcpserver.KmsConfigV1betaKmsStateINUSE) ||
		state == string(gcpserver.KmsConfigV1betaKmsStateREADY)
}

// determineRotationReason returns the reason why rotation is required based on key state
func determineRotationReason(activeKeys []datamodel.ServiceAccountKey) string {
	if len(activeKeys) > 1 {
		return "Last rotation didn't complete - multiple active keys exist"
	}
	for _, key := range activeKeys {
		if !key.IsPrimary {
			return "New key exists but not yet primary - rotation in progress"
		}
	}
	return "Rotation required - all validations passed"
}

// extractGlobalProjectIDFromEmail extracts the global project ID from a service account email
// and resolves it to project name if it's a project number.
// Service account email format: <name>@<globalProjectId>.iam.gserviceaccount.com
// If the extracted value is a project number (all digits), it uses the cloud service to resolve it to project name.
func extractGlobalProjectIDFromEmail(email string, cloudService hyperscaler.Services) (string, error) {
	// Find the @ symbol
	atIndex := strings.Index(email, "@")
	if atIndex == -1 {
		return "", fmt.Errorf("invalid email format: missing @ symbol")
	}

	// Get the domain part after @
	domain := email[atIndex+1:]

	// Find .iam.gserviceaccount.com suffix
	suffix := ".iam.gserviceaccount.com"
	if !strings.HasSuffix(domain, suffix) {
		return "", fmt.Errorf("invalid email format: missing .iam.gserviceaccount.com suffix")
	}

	// Extract project ID (everything before .iam.gserviceaccount.com)
	globalProjectID := domain[:len(domain)-len(suffix)]
	if globalProjectID == "" {
		return "", fmt.Errorf("invalid email format: empty project ID")
	}

	// If global project ID is a number, resolve it to project name
	// GCP API requires project name (not number) for key deletion
	if isProjectNumber(globalProjectID) {
		projectName, err := cloudService.ResolveProjectNumberToName(globalProjectID)
		if err != nil {
			return "", fmt.Errorf("failed to resolve project number %s to name: %w", globalProjectID, err)
		}
		return projectName, nil
	}

	return globalProjectID, nil
}

// isProjectNumber checks if the given string is a project number (all digits)
// GCP project numbers are numeric, while project IDs/names are alphanumeric with dashes
func isProjectNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ValidateKeyRotationRequiredActivity validates if key rotation is needed
// This activity is idempotent - it checks actual state and returns the same result on repeated calls
func (a *RotateKmsSAKeyActivity) ValidateKeyRotationRequiredActivity(ctx context.Context, serviceAccountUUID string, kmsConfigID string) (*ValidateKeyRotationRequiredResult, error) {
	logger := util.GetLogger(ctx)

	// 1. Fetch service account with keys
	serviceAccount, err := a.SE.GetServiceAccountWithKeys(ctx, serviceAccountUUID)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	// 2. Fetch KMS config
	kmsConfig, err := a.SE.GetKmsConfigByUUID(ctx, kmsConfigID)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	// 3. Validate KMS config state (only case where rotation is NOT required)
	if !isKmsConfigInValidState(kmsConfig.State) {
		return &ValidateKeyRotationRequiredResult{
			RotationRequired: false,
			Reason:           fmt.Sprintf("KMS config is not in valid state for rotation (current state: %s)", kmsConfig.State),
			ServiceAccount:   serviceAccount,
		}, nil
	}

	// 4. Extract current key ID
	currentKeyID, err := extractKeyID(serviceAccount.ServiceAccountPasswordLocation)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("failed to extract current key ID: %w", err))
	}

	// 5. Determine rotation reason based on key state
	activeKeys := serviceAccount.GetAllActiveKeys()
	reason := determineRotationReason(activeKeys)

	logger.Info("KMS_KEY_ROTATION: Validation complete",
		"rotationRequired", true,
		"reason", reason,
		"currentKeyID", currentKeyID)

	return &ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     currentKeyID,
		Reason:           reason,
		ServiceAccount:   serviceAccount,
	}, nil
}

// CreateServiceAccountKeyResult contains the result of creating a service account key
type CreateServiceAccountKeyResult struct {
	NewKeyID   string // GCP key ID
	NewKeyData string // Encrypted key data
	GcpKeyName string // GCP key resource name (full path)
	KeyExists  bool   // Whether key already existed
}

// CreateServiceAccountKeyActivity creates a new service account key in GCP
// This activity is idempotent - checks if a new key already exists before creating
func (a *RotateKmsSAKeyActivity) CreateServiceAccountKeyActivity(ctx context.Context, serviceAccountUUID string, kmsConfig *datamodel.KmsConfig, currentKeyID string) (*CreateServiceAccountKeyResult, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Get fresh service account with keys
	serviceAccount, err := se.GetServiceAccountWithKeys(ctx, serviceAccountUUID)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to get service account: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	// Check if a new key (non-primary) already exists in the keys array
	// This makes the activity idempotent - if key already exists, return it
	if serviceAccount.ServiceAccountAttributes != nil && len(serviceAccount.ServiceAccountAttributes.Keys) > 0 {
		for _, key := range serviceAccount.ServiceAccountAttributes.Keys {
			// If we find a non-primary active key, that's our new key
			if !key.IsPrimary && key.IsActive && key.KeyID != currentKeyID {
				logger.Info("KMS_KEY_ROTATION: New key already exists in keys array - returning existing key",
					"newKeyID", key.KeyID,
					"currentKeyID", currentKeyID)
				return &CreateServiceAccountKeyResult{
					NewKeyID:   key.KeyID,
					NewKeyData: key.KeyData,
					GcpKeyName: "", // Not stored, but we can construct it if needed
					KeyExists:  true,
				}, nil
			}
		}
	}

	// Check for key accumulation - prevent hitting GCP's 10-key limit
	// This can happen if DeleteOldSAKeyFromGCPActivity repeatedly fails
	if serviceAccount.ServiceAccountAttributes != nil {
		totalKeys := len(serviceAccount.ServiceAccountAttributes.Keys)
		keysMarkedForDeletion := len(serviceAccount.GetKeysMarkedForDeletion())

		// Block rotation if too many keys are pending deletion
		if keysMarkedForDeletion >= maxPendingDeletionKeysAllowed {
			logger.Errorf("KMS_KEY_ROTATION: Too many keys pending deletion (%d) - cleanup required before rotation", keysMarkedForDeletion)
			// Emit metric for alerting (if metrics emitter is configured)
			if a.MetricsEmitter != nil {
				a.MetricsEmitter.EmitKmsKeyLimitReached(kmsConfig.UUID, "pending_deletion")
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("too many keys pending deletion (%d >= %d) - DeleteOldSAKeyFromGCPActivity may be failing repeatedly; investigate and cleanup before rotation",
					keysMarkedForDeletion, maxPendingDeletionKeysAllowed))
		}

		// Block rotation if approaching GCP's 10-key limit
		if totalKeys >= maxTotalKeysBeforeRotation {
			logger.Errorf("KMS_KEY_ROTATION: Too many total keys (%d) - cleanup required before rotation", totalKeys)
			// Emit metric for alerting (if metrics emitter is configured)
			if a.MetricsEmitter != nil {
				a.MetricsEmitter.EmitKmsKeyLimitReached(kmsConfig.UUID, "total_keys")
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("too many keys in service account (%d >= %d) - approaching GCP's 10-key limit; cleanup required",
					totalKeys, maxTotalKeysBeforeRotation))
		}
	}

	// No existing new key found - create a new one in GCP
	logger.Info("KMS_KEY_ROTATION: No existing new key found - creating new key in GCP",
		"serviceAccountEmail", serviceAccount.ServiceAccountEmail)

	cloudService, err := getGcpService(ctx)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to get cloud service: %v", err)
		return nil, err
	}

	// Create new service account key in GCP
	serviceAccountKey, err := gcpServiceCreateServiceAccountKey(cloudService, ctx, serviceAccount.ServiceAccountEmail)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to create service account key in cloud provider: %v", err)
		return nil, err
	}

	// Extract new key ID from the key data
	// PrivateKeyData from GCP API is base64-encoded JSON, not encrypted
	newKeyID, err := extractKeyIDFromRawBase64(serviceAccountKey.PrivateKeyData)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to extract new key ID: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("failed to extract new key ID: %w", err))
	}

	// Encrypt the new key
	secretPassword, err := utils.EncryptPassword(log.Secret(serviceAccountKey.PrivateKeyData))
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to encrypt service account key: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("failed to encrypt service account key: %w", err))
	}

	// Encrypt with KMS crypto key
	err = kms_activities.AccessCryptoKeyAndEncryptData(ctx, kmsConfig, *secretPassword, kms_activities.RetryTimeOutForGetCryptoKey, kms_activities.RetryIntervalForGetCryptoKey)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to encrypt key with KMS crypto key: %v", err)
		return nil, err
	}

	logger.Info("KMS_KEY_ROTATION: Successfully created new service account key in GCP",
		"newKeyID", newKeyID,
		"gcpKeyName", serviceAccountKey.Name)

	return &CreateServiceAccountKeyResult{
		NewKeyID:   newKeyID,
		NewKeyData: *secretPassword, // Already encrypted with KMS
		GcpKeyName: serviceAccountKey.Name,
		KeyExists:  false,
	}, nil
}

// StoreNewKeyInDBActivity stores the new key in ServiceAccount keys array
// This activity is idempotent - checks if key already exists before storing
func (a *RotateKmsSAKeyActivity) StoreNewKeyInDBActivity(ctx context.Context, serviceAccountUUID string, newKeyID string, newKeyData string, currentKeyID string) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Get fresh service account with keys
	serviceAccount, err := se.GetServiceAccountWithKeys(ctx, serviceAccountUUID)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to get service account: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	// Initialize attributes if nil
	if serviceAccount.ServiceAccountAttributes == nil {
		serviceAccount.ServiceAccountAttributes = &datamodel.ServiceAccountAttributes{
			Keys: []datamodel.ServiceAccountKey{},
		}
	}

	// Check if new key already exists in keys array (idempotent check)
	existingKey := serviceAccount.GetKeyByID(newKeyID)
	if existingKey != nil {
		logger.Info("KMS_KEY_ROTATION: New key already exists in keys array - skipping storage",
			"newKeyID", newKeyID)
		return nil // Already stored - idempotent
	}

	// Ensure old key is in the keys array if not already there
	oldKey := serviceAccount.GetKeyByID(currentKeyID)
	if oldKey == nil {
		// Add old key to keys array as primary
		oldKeyEntry := datamodel.ServiceAccountKey{
			KeyID:     currentKeyID,
			KeyData:   serviceAccount.ServiceAccountPasswordLocation,
			IsPrimary: true,
			IsActive:  true,
			CreatedAt: serviceAccount.CreatedAt, // Use service account creation time as fallback
		}
		err = se.AddKeyToServiceAccount(ctx, serviceAccount.UUID, oldKeyEntry)
		if err != nil {
			logger.Errorf("KMS_KEY_ROTATION: Failed to add old key to keys array: %v", err)
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		}
		logger.Info("KMS_KEY_ROTATION: Added old key to keys array", "oldKeyID", currentKeyID)
	}

	// Add new key to keys array (not primary yet)
	newKeyEntry := datamodel.ServiceAccountKey{
		KeyID:     newKeyID,
		KeyData:   newKeyData, // Already encrypted with KMS
		IsPrimary: false,      // Will be set to primary after all SVMs migrate
		IsActive:  true,
		CreatedAt: time.Now(),
	}

	err = se.AddKeyToServiceAccount(ctx, serviceAccount.UUID, newKeyEntry)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to add new key to keys array: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}

	err = se.UpdateServiceAccountPasswordLocation(ctx, serviceAccount.UUID, newKeyData)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to update service account password location with new key: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	logger.Info("KMS_KEY_ROTATION: Successfully stored new key in database",
		"newKeyID", newKeyID,
		"oldKeyID", currentKeyID)

	return nil
}

// AcquireKmsRotationLockActivity acquires a K8s lease-based lock for this KMS config before creating
// a new service account key. Returns the holder clientID; the same clientID must be passed to
// ReleaseKmsRotationLockActivity. Caller should retry on ErrAlreadyHeld (activity retries internally).
// When LOCAL_SETUP is true, no lock is acquired and a dummy clientID is returned (no-op).
func (a *RotateKmsSAKeyActivity) AcquireKmsRotationLockActivity(ctx context.Context, kmsConfigUUID string) (string, error) {
	if isLocalSetup() {
		return "", nil
	}
	logger := util.GetLogger(ctx)
	lockName := kmsRotationLockNamePrefix + kmsConfigUUID

	locker, err := newKmsRotationLocker(ctx, lockName,
		utils.Namespace(vcpNamespace),
		utils.InClusterConfig(),
		utils.TTL(kmsRotationLockTTL),
		utils.MaxLockAcquiringWait(kmsRotationLockMaxAcquireWait),
		utils.RetryWaitDuration(kmsRotationLockRetryWait))
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to create locker: %v", err)
		return "", vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("create locker: %w", err))
	}

	deadline := time.Now().Add(kmsRotationLockMaxAcquireWait)
	for time.Now().Before(deadline) {
		err = locker.Lock(ctx, logger)
		if err == nil {
			logger.Info("KMS_KEY_ROTATION: Acquired KMS rotation lock", "kmsConfigUUID", kmsConfigUUID)
			return locker.ClientID(), nil
		}
		if !errstd.Is(err, utils.ErrAlreadyHeld) {
			return "", vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, fmt.Errorf("acquire lock: %w", err))
		}
		// Lock held by another worker; wait before retrying so it has time to be released.
		logger.Info("KMS_KEY_ROTATION: KMS rotation lock held by another worker, waiting before retry",
			"kmsConfigUUID", kmsConfigUUID, "retryWait", kmsRotationLockRetryWait)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(kmsRotationLockRetryWait):
		}
	}
	return "", vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
		fmt.Errorf("could not acquire KMS rotation lock within %v (lease held by another worker)", kmsRotationLockMaxAcquireWait))
}

// RenewKmsRotationLockActivity renews the K8s lease so the lock is not considered expired while the holder
// is still working. Call periodically (e.g. every TTL/2) between Acquire and Release. clientID must be the
// value returned by AcquireKmsRotationLockActivity for the same kmsConfigUUID.
// When LOCAL_SETUP is true, this is a no-op.
func (a *RotateKmsSAKeyActivity) RenewKmsRotationLockActivity(ctx context.Context, kmsConfigUUID string, clientID string) error {
	if isLocalSetup() {
		return nil
	}
	lockName := kmsRotationLockNamePrefix + kmsConfigUUID
	locker, err := newKmsRotationLocker(ctx, lockName,
		utils.Namespace(vcpNamespace),
		utils.InClusterConfig(),
		utils.ClientID(clientID))
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("create locker for renew: %w", err))
	}
	if err := locker.RenewLock(ctx); err != nil {
		return fmt.Errorf("renew KMS rotation lock: %w", err)
	}
	return nil
}

// ReleaseKmsRotationLockActivity releases the K8s lease lock acquired by AcquireKmsRotationLockActivity.
// clientID must be the value returned by AcquireKmsRotationLockActivity for the same kmsConfigUUID.
// When LOCAL_SETUP is true, this is a no-op.
func (a *RotateKmsSAKeyActivity) ReleaseKmsRotationLockActivity(ctx context.Context, kmsConfigUUID string, clientID string) error {
	if isLocalSetup() {
		return nil
	}
	logger := util.GetLogger(ctx)
	lockName := kmsRotationLockNamePrefix + kmsConfigUUID

	locker, err := newKmsRotationLocker(ctx, lockName,
		utils.Namespace(vcpNamespace),
		utils.InClusterConfig(),
		utils.ClientID(clientID))
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to create locker for release: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("create locker for release: %w", err))
	}
	if err := locker.Unlock(ctx); err != nil {
		logger.Warnf("KMS_KEY_ROTATION: Failed to release lock: %v", err)
		return nil
	}
	logger.Info("KMS_KEY_ROTATION: Released KMS rotation lock", "kmsConfigUUID", kmsConfigUUID)
	return nil
}

// BatchPoolsForKeyRotationActivity gets all pools that need migration for the given KMS config
// This is a read-only operation - always safe to re-execute (idempotent)
func (a *RotateKmsSAKeyActivity) BatchPoolsForKeyRotationActivity(ctx context.Context, kmsConfigID int64) ([]*datamodel.Pool, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Get all pools using this KMS config
	// This is a read operation - always safe to re-execute
	poolsView, err := listPoolsByKmsConfigId(ctx, kmsConfigID, se)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to list pools by kms config id: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	var validPools []*datamodel.Pool
	for _, poolView := range poolsView {
		switch poolView.State {
		case datamodel.LifeCycleStateDeleting:
			logger.Info("KMS_KEY_ROTATION: Skipping pool in Deleting state", "poolUUID", poolView.UUID, "poolName", poolView.Name, "state", poolView.State)
			continue
		case datamodel.LifeCycleStateError:
			// if pools has no active volume do not consider it for migration
			if poolView.VolumeCount <= 0 {
				logger.Info("KMS_KEY_ROTATION: Skipping errored pool with no active volumes", "poolUUID", poolView.UUID, "poolName", poolView.Name, "state", poolView.State, "volumeCount", poolView.VolumeCount)
				continue
			}
			logger.Info("KMS_KEY_ROTATION: Considering errored pool for migration", "poolUUID", poolView.UUID, "poolName", poolView.Name)
		case datamodel.LifeCycleStateCreating:
			logger.Warn("Skipping key rotation due to pool in Creating state", "poolName", poolView.Name, "poolUUID", poolView.UUID)
			return nil, errors.NewConflictErr(utils.StoragePoolCreatingStateError)
		}
		validPools = append(validPools, database.ConvertPoolViewToPool(poolView))
	}

	logger.Info("KMS_KEY_ROTATION: Batched pools for migration",
		"kmsConfigID", kmsConfigID,
		"poolCount", len(validPools))

	return validPools, nil
}

// MigratePoolToNewKeyActivity migrates a single pool's SVM to use the new key
// This activity is idempotent - updating ONTAP with the same key multiple times is safe
// Accepts encrypted key data to avoid logging passwords in Temporal activity logs
func (a *RotateKmsSAKeyActivity) MigratePoolToNewKeyActivity(ctx context.Context, poolUUID string, encryptedNewKeyData string, encryptedOldKeyData string, newKeyID string) (*SvmMigrationResult, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Get pool by UUID
	pool, err := se.GetPoolByUUID(ctx, poolUUID)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to get pool: %v", err)
		return &SvmMigrationResult{
			SvmUUID: "",
			Success: false,
			Error:   fmt.Sprintf("Failed to get pool: %v", err),
		}, nil // Return error in result, not as error (to allow other pools to continue)
	}

	switch pool.State {
	// Rare case but if below state happens then consider it as success
	case datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeleting:
		logger.Info(fmt.Sprintf("KMS_KEY_ROTATION: pool %s in %s state skipping migration", pool.Name, pool.State))
		return &SvmMigrationResult{
			SvmUUID: "",
			Success: true,
		}, nil
	}

	// Get the SVM for this pool
	svm, err := se.GetSvmForPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to get SVM for pool: %v", err)
		return &SvmMigrationResult{
			SvmUUID: "",
			Success: false,
			Error:   fmt.Sprintf("Failed to get SVM for pool: %v", err),
		}, nil
	}

	// Check if SVM is already using the new key (idempotent check)
	// Initialize SvmDetails if nil
	if svm.SvmDetails == nil {
		svm.SvmDetails = &datamodel.SvmDetails{}
	}
	if svm.SvmDetails.CurrentKmsKeyID == newKeyID {
		logger.Info("KMS_KEY_ROTATION: SVM is already using the new key - skipping migration",
			"poolUUID", pool.UUID,
			"svmUUID", svm.UUID,
			"keyID", newKeyID)
		return &SvmMigrationResult{
			SvmUUID: svm.UUID,
			Success: true,
		}, nil
	}

	// Decrypt the new key data inside the activity to avoid logging passwords in Temporal
	// The decrypted value is base64-encoded JSON that syncKeyWithOntap will decode
	decryptedNewKeyData, err := utils.DecryptPassword(log.Secret(encryptedNewKeyData))
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to decrypt new key data: %v", err)
		return &SvmMigrationResult{
			SvmUUID: svm.UUID,
			Success: false,
			Error:   fmt.Sprintf("Failed to decrypt new key data: %v", err),
		}, nil
	}

	// Use the existing syncKeyWithOntap logic
	// This will update ONTAP with the new key and verify reachability
	// newKeyData should be base64-encoded (decryptedNewKeyData is already base64-encoded JSON)
	// oldKeyData is encrypted and syncKeyWithOntap will decrypt it
	err = syncKeyWithOntap(ctx, se, *decryptedNewKeyData, encryptedOldKeyData, pool)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to sync key with ONTAP for pool %s: %v", pool.Name, err)
		return &SvmMigrationResult{
			SvmUUID: svm.UUID,
			Success: false,
			Error:   err.Error(),
		}, nil // Return error in result, not as error (to allow other pools to continue)
	}

	// Update SVM to track which key it's using
	err = se.UpdateSvmCurrentKmsKeyID(ctx, svm.UUID, newKeyID)
	if err != nil {
		logger.Warnf("KMS_KEY_ROTATION: Failed to update SVM current key ID (non-fatal): %v", err)
		// Non-fatal - migration succeeded, just tracking failed
	} else {
		logger.Info("KMS_KEY_ROTATION: Updated SVM current key ID",
			"svmUUID", svm.UUID,
			"keyID", newKeyID)
	}

	logger.Info("KMS_KEY_ROTATION: Successfully migrated pool to new key",
		"poolUUID", pool.UUID,
		"poolName", pool.Name,
		"svmUUID", svm.UUID,
		"newKeyID", newKeyID)

	return &SvmMigrationResult{
		SvmUUID: svm.UUID,
		Success: true,
	}, nil
}

// CompleteKeyRotationActivity completes the key rotation by setting new key as primary
// This activity is idempotent - checks if new key is already primary before updating
// The old key is marked for deletion (IsPrimary=false, IsActive=false) instead of being removed,
// so that DeleteOldSAKeyFromGCPActivity can find and delete it from GCP
func (a *RotateKmsSAKeyActivity) CompleteKeyRotationActivity(ctx context.Context, serviceAccountUUID string, kmsConfigUUID string, newKeyID string, oldKeyID string) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Get fresh service account with keys
	serviceAccount, err := se.GetServiceAccountWithKeys(ctx, serviceAccountUUID)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to get service account: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	// Check if new key is already primary (idempotent check)
	newKey := serviceAccount.GetKeyByID(newKeyID)
	if newKey == nil {
		logger.Errorf("KMS_KEY_ROTATION: New key not found in keys array: %s", newKeyID)
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("new key %s not found in keys array", newKeyID))
	}

	if newKey.IsPrimary {
		logger.Info("KMS_KEY_ROTATION: New key is already primary - rotation already completed",
			"newKeyID", newKeyID)
		return nil // Already completed - idempotent
	}

	// Set new key as primary and update ServiceAccountPasswordLocation
	err = se.SetPrimaryKeyForServiceAccount(ctx, serviceAccountUUID, newKeyID)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to set new key as primary: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	// Mark old key for deletion (IsPrimary=false, IsActive=false)
	// The key is NOT removed from the JSON array - it will be removed by DeleteOldSAKeyFromGCPActivity
	// after successful deletion from GCP. This ensures retry capability if GCP deletion fails.
	err = se.MarkKeyForDeletion(ctx, serviceAccountUUID, oldKeyID)
	if err != nil && !strings.Contains(err.Error(), "key not found") {
		logger.Warnf("KMS_KEY_ROTATION: Failed to mark old key for deletion: %v", err)
		// Non-fatal - old key can be cleaned up later by DeleteOldSAKeyFromGCPActivity
	} else {
		logger.Info("KMS_KEY_ROTATION: Marked old key for deletion", "oldKeyID", oldKeyID)
	}

	// Note: SVMs' CurrentKmsKeyID is already updated during migration (MigratePoolToNewKeyActivity)
	// No need to update again here - all SVMs should already have the new key ID set

	logger.Info("KMS_KEY_ROTATION: Successfully completed key rotation",
		"newKeyID", newKeyID,
		"oldKeyID", oldKeyID)

	return nil
}

// DeleteOldSAKeyFromGCPActivity deletes old service account keys from GCP that are marked for deletion
// This activity finds all keys with IsPrimary=false AND IsActive=false, deletes them from GCP,
// and only then removes them from the JSON. This ensures retry capability if GCP deletion fails.
// This activity is idempotent - if the key is already deleted from GCP, it will still remove it from JSON
// This should be called after CompleteKeyRotationActivity
func (a *RotateKmsSAKeyActivity) DeleteOldSAKeyFromGCPActivity(ctx context.Context, serviceAccountUUID string, kmsConfigUUID string, oldKeyID string) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Get service account to access email and keys
	serviceAccount, err := se.GetServiceAccountWithKeys(ctx, serviceAccountUUID)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to get service account: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	// Get all keys marked for deletion (IsPrimary=false AND IsActive=false)
	keysToDelete := serviceAccount.GetKeysMarkedForDeletion()

	// If no keys marked for deletion, nothing to do
	if len(keysToDelete) == 0 {
		logger.Info("KMS_KEY_ROTATION: No keys marked for deletion found", "serviceAccountUUID", serviceAccountUUID)
		return nil
	}

	// Get cloud service
	cloudService, err := getGcpService(ctx)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to get cloud service: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("failed to get cloud service: %w", err))
	}

	// Extract global project ID from service account email
	// If it's a project number, it will be resolved to project name
	globalProjectID, err := extractGlobalProjectIDFromEmail(serviceAccount.ServiceAccountEmail, cloudService)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Could not extract global project ID from service account email: %v (email: %s)", err, serviceAccount.ServiceAccountEmail)
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("could not extract global project ID from service account email: %w", err))
	}

	// Delete each key marked for deletion from GCP, then remove from JSON
	for _, key := range keysToDelete {
		keyName := fmt.Sprintf("projects/%s/serviceAccounts/%s%s%s", globalProjectID, serviceAccount.ServiceAccountEmail, keyPrefix, key.KeyID)

		// Delete the key from GCP
		// This is idempotent - if key is already deleted, GCP API will return 404 which we treat as success
		err = deleteServiceAccountKeyWithRetry(ctx, cloudService, keyName)
		if err != nil {
			// Check if error is "not found" - this means key is already deleted from GCP (idempotent)
			if !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "not found") {
				logger.Errorf("KMS_KEY_ROTATION: Failed to delete key from cloud provider: %v (keyName: %s)", err, keyName)
				return vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("failed to delete key from cloud provider: %w", err))
			}
			logger.Info("KMS_KEY_ROTATION: Key already deleted from GCP (idempotent)", "keyID", key.KeyID, "keyName", keyName)
		} else {
			logger.Info("KMS_KEY_ROTATION: Successfully deleted key from GCP", "keyID", key.KeyID, "keyName", keyName)
		}

		// Only after successful GCP deletion (or if already deleted), remove from JSON
		err = se.RemoveKeyFromServiceAccount(ctx, serviceAccountUUID, key.KeyID)
		if err != nil && !strings.Contains(err.Error(), "key not found") {
			logger.Warnf("KMS_KEY_ROTATION: Failed to remove key from JSON after GCP deletion: %v", err)
			// Non-fatal - key is already deleted from GCP, JSON cleanup can happen later
		} else {
			logger.Info("KMS_KEY_ROTATION: Removed key from JSON", "keyID", key.KeyID)
		}
	}

	logger.Info("KMS_KEY_ROTATION: Successfully processed all keys marked for deletion",
		"keysProcessed", len(keysToDelete))

	return nil
}

// SvmMigrationResult represents the result of migrating a single SVM to a new key
type SvmMigrationResult struct {
	SvmUUID string
	Success bool
	Error   string
}

func ListPoolsByKmsConfigId(ctx context.Context, kmsConfigId int64, se database.Storage) ([]*datamodel.PoolView, error) {
	logger := util.GetLogger(ctx)

	filter := dbutils.CreateFilterWithConditions(dbutils.NewFilterCondition("kms_config_id", "=", kmsConfigId))
	poolViews, err := se.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to list pools: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return poolViews, nil
}

func _syncKeyWithOntap(ctx context.Context, se database.Storage, newServiceAccountKey string, oldServiceAccountKey string, pool *datamodel.Pool) error {
	logger := util.GetLogger(ctx)

	svm, err := se.GetSvmForPoolID(ctx, pool.ID)
	if err != nil {
		return err
	}

	provider, err := GetOntapRestProviderForPool(ctx, se, pool)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to get ONTAP rest provider: %v", err)
		return err
	}

	sa, err := base64.StdEncoding.DecodeString(newServiceAccountKey)
	if err != nil {
		logger.Errorf("KMS_KEY_ROTATION: Failed to decode new service account key: %v", err)
		return err
	}

	serviceAccountKeySecret := log.Secret(sa)

	// Update ONTAP with new service account key
	_, _, err = provider.ModifyGcpKms(svm.SvmDetails.ExternalKmsConfigUUID, &serviceAccountKeySecret)
	if err != nil {
		return err
	}

	// Check the KMS configuration using the provider i.e ONTAP REST client on vsa cluster
	_, err = provider.IsGcpKmsReachable(vsa.GetKmsConfigParams{
		ExternalKmsConfigID: svm.SvmDetails.ExternalKmsConfigUUID,
	})

	if err != nil {
		// If new key fails reachability check, revert to old key
		logger.Warnf("KMS_KEY_ROTATION: New KMS key failed reachability check, reverting to old key. Error: %v", err)

		oldSa, decodeErr := utils.DecryptAndDecodeCredentials(oldServiceAccountKey)
		if decodeErr != nil {
			return err
		}

		oldServiceAccountKeySecret := log.Secret(oldSa)

		// Revert ONTAP to old service account key
		_, _, revertErr := provider.ModifyGcpKms(svm.SvmDetails.ExternalKmsConfigUUID, &oldServiceAccountKeySecret)
		if revertErr != nil {
			logger.Errorf("KMS_KEY_ROTATION: Failed to revert ONTAP to old service account key: %v", revertErr)
			// Return original error even if revert failed
		} else {
			logger.Infof("KMS_KEY_ROTATION: Successfully reverted ONTAP to old service account key for svm: %s", svm.UUID)
		}
		return err
	}
	logger.Infof("KMS_KEY_ROTATION: Checked reachability for svm: %s & pool: %s on ontap", svm.UUID, pool.Name)

	return nil
}

// _extractKeyID extracts key ID from encrypted service account key data
// This is used for keys that have been encrypted and stored in the database
func _extractKeyID(serviceAccountKey string) (string, error) {
	decryptKey, err := utils.DecryptPassword(log.Secret(serviceAccountKey))
	if err != nil {
		return "", errors.New("failed to decrypt service account key")
	}
	credentialsDecoded, err := base64.StdEncoding.DecodeString(*decryptKey)
	if err != nil {
		return "", err
	}
	var credentialsMap map[string]interface{}
	if err = json.Unmarshal(credentialsDecoded, &credentialsMap); err != nil {
		return "", fmt.Errorf("failed to unmarshal credentials: %v", err)
	}

	// Extract values from the map
	keyID, ok := credentialsMap["private_key_id"].(string)
	if !ok {
		return "", fmt.Errorf("key not found or not a string")
	}
	return keyID, nil
}

// _extractKeyIDFromRawBase64 extracts key ID directly from base64-encoded JSON
// This is used for raw PrivateKeyData returned by GCP API (which is base64-encoded JSON)
func _extractKeyIDFromRawBase64(base64EncodedData string) (string, error) {
	// GCP API returns PrivateKeyData as base64-encoded JSON
	credentialsDecoded, err := base64.StdEncoding.DecodeString(base64EncodedData)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 data: %w", err)
	}
	var credentialsMap map[string]interface{}
	if err = json.Unmarshal(credentialsDecoded, &credentialsMap); err != nil {
		return "", fmt.Errorf("failed to unmarshal credentials: %w", err)
	}

	// Extract values from the map
	keyID, ok := credentialsMap["private_key_id"].(string)
	if !ok {
		return "", fmt.Errorf("private_key_id not found or not a string")
	}
	return keyID, nil
}
