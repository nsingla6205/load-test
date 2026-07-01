package backgroundactivities

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// PasswordGenerationResponse represents the response from password generation
// Note: NewPassword is not included in JSON serialization to prevent logging in Temporal workflow history
type PasswordGenerationResponse struct {
	NewPassword string `json:"-"` // Excluded from JSON serialization for security
	Timestamp   string `json:"timestamp"`
	NewSecretID string `json:"new_secret_id"`
}

// PasswordRotationResourcesNew tracks resources created during password rotation for cleanup
type PasswordRotationResourcesNew struct {
	NewSecretID  string
	NewPassword  string
	OldSecretID  string
	Pool         interface{} // Will be converted to *datamodel.Pool in activities
	GcpService   interface{} // Will be converted to hyperscaler2.Services in activities
	CacheUpdated bool
}

// ============================================================================
// PASSWORD GENERATION ACTIVITIES
// ============================================================================

// GenerateNewPassword generates a new password for password rotation
func (a *RotateVcpToVsaCertificateActivity) GenerateNewPassword(ctx context.Context, poolUUID string) (*PasswordGenerationResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Infof("Generating new password for pool: %s", poolUUID)

	// Get pool details
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(poolViews) == 0 {
		logger.Errorf("Pool %s not found", poolUUID)
		return nil, fmt.Errorf("pool %s not found", poolUUID)
	}

	pool := ConvertPoolViewToPool(poolViews[0])
	logger.Infof("Pool Details - ID: %d, Name: %s, State: %s, DeploymentName: %s",
		pool.ID, pool.Name, pool.State, pool.DeploymentName)

	if pool.PoolCredentials != nil {
		logger.Infof("Current Pool Credentials - AuthType: %d, SecretID: %s, SecretIDNew: %s",
			pool.PoolCredentials.AuthType, pool.PoolCredentials.SecretID, pool.PoolCredentials.SecretIDNew)
	} else {
		logger.Warnf("Pool credentials are nil for pool %s", poolUUID)
	}

	// Generate new password
	timestamp := time.Now().Format("20060102-150405")
	logger.Infof("Generating new password with timestamp: %s", timestamp)

	newPassword, err := utils.GenerateStrongPassword(16)
	if err != nil {
		logger.Errorf("Failed to generate new password: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrPasswordRotationFailed, err)
	}

	// Add timestamp suffix to ensure uniqueness and avoid password history conflicts
	newPassword = newPassword + timestamp[len(timestamp)-4:]

	// Create new secret ID with the SAME timestamp to ensure consistency
	newSecretID := fmt.Sprintf("%s-secret-%s", pool.DeploymentName, timestamp)
	logger.Infof("Generated new secret ID: %s", newSecretID)

	response := &PasswordGenerationResponse{
		NewPassword: newPassword,
		Timestamp:   timestamp,
		NewSecretID: newSecretID,
	}

	logger.Infof("Password generation completed for pool: %s", poolUUID)

	return response, nil
}

// ============================================================================
// VSA CONNECTIVITY ACTIVITIES
// ============================================================================

// TestPasswordConnectivity tests password connectivity to VSA cluster
func (a *RotateVcpToVsaCertificateActivity) TestPasswordConnectivity(ctx context.Context, poolUUID, password string) error {
	logger := util.GetLogger(ctx)

	// Get pool details
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(poolViews) == 0 {
		logger.Errorf("Pool %s not found", poolUUID)
		return fmt.Errorf("pool %s not found", poolUUID)
	}

	pool := ConvertPoolViewToPool(poolViews[0])

	// Test password connectivity using the existing method
	err = a.testPasswordConnectivity(ctx, pool, password)
	if err != nil {
		logger.Errorf("Password connectivity test failed for pool %s: %v", poolUUID, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrPasswordConnectivityTestFailed, err)
	}

	return nil
}

// UpdateVSAPassword updates the VSA cluster with new password
// Password will be retrieved from database/cache internally
func (a *RotateVcpToVsaCertificateActivity) UpdateVSAPassword(ctx context.Context, poolUUID string) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Updating VSA password for pool: %s", poolUUID)

	// Get pool details
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(poolViews) == 0 {
		logger.Errorf("Pool %s not found", poolUUID)
		return fmt.Errorf("pool %s not found", poolUUID)
	}

	pool := ConvertPoolViewToPool(poolViews[0])
	logger.Infof("Pool Details - ID: %d, Name: %s, State: %s, DeploymentName: %s",
		pool.ID, pool.Name, pool.State, pool.DeploymentName)

	if pool.PoolCredentials != nil {
		logger.Infof("Current Pool Credentials - AuthType: %d, SecretID: %s, SecretIDNew: %s",
			pool.PoolCredentials.AuthType, pool.PoolCredentials.SecretID, pool.PoolCredentials.SecretIDNew)
	} else {
		logger.Errorf("Pool credentials are nil for pool %s", poolUUID)
		return fmt.Errorf("pool credentials are nil for pool %s", poolUUID)
	}

	// Retrieve new password from Secret Manager using secret_id_new
	if pool.PoolCredentials.SecretIDNew == "" {
		logger.Errorf("SecretIDNew is empty for pool %s - cannot retrieve new password", poolUUID)
		return fmt.Errorf("secret_id_new is empty for pool %s", poolUUID)
	}

	logger.Infof("Retrieving new password from Secret Manager using secret_id_new: %s", pool.PoolCredentials.SecretIDNew)
	newPassword, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, pool.PoolCredentials.SecretIDNew)
	if err != nil {
		logger.Errorf("Failed to retrieve new password from Secret Manager: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	// Update VSA cluster with new password using the existing method
	logger.Infof("Updating VSA cluster with new password...")
	err = a.updateVSAPassword(ctx, pool, newPassword)
	if err != nil {
		logger.Errorf("Failed to update VSA cluster with new password: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrVSAClusterPasswordUpdateFailed, err)
	}

	logger.Infof("VSA password update completed for pool: %s", poolUUID)
	return nil
}

// ============================================================================
// DATABASE CONNECTIVITY ACTIVITIES
// ============================================================================

// SwapSecretIDs swaps secret_id and secret_id_new in the database
func (a *RotateVcpToVsaCertificateActivity) SwapSecretIDs(ctx context.Context, poolUUID string) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Swapping secret IDs for pool: %s", poolUUID)

	// Get pool details
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(poolViews) == 0 {
		logger.Errorf("Pool %s not found", poolUUID)
		return fmt.Errorf("pool %s not found", poolUUID)
	}

	pool := ConvertPoolViewToPool(poolViews[0])
	logger.Infof("Pool Details - ID: %d, Name: %s, State: %s, DeploymentName: %s",
		pool.ID, pool.Name, pool.State, pool.DeploymentName)

	if pool.PoolCredentials != nil {
		logger.Infof("BEFORE SWAP - Pool Credentials - AuthType: %d, SecretID: %s, SecretIDNew: %s",
			pool.PoolCredentials.AuthType, pool.PoolCredentials.SecretID, pool.PoolCredentials.SecretIDNew)
	} else {
		logger.Errorf("Pool credentials are nil for pool %s", poolUUID)
		return fmt.Errorf("pool credentials are nil for pool %s", poolUUID)
	}

	// Swap secret IDs using the existing method
	logger.Infof("Swapping secret IDs in database...")
	err = a.swapSecretIDs(ctx, pool)
	if err != nil {
		logger.Errorf("Failed to swap secret IDs in database: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrPasswordSecretIDSwapFailed, err)
	}

	// Get updated pool details to verify the swap
	updatedPoolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Warnf("Failed to get updated pool details for verification: %v", err)
	} else if len(updatedPoolViews) > 0 {
		updatedPool := ConvertPoolViewToPool(updatedPoolViews[0])
		if updatedPool.PoolCredentials != nil {
			logger.Infof("AFTER SWAP - Pool Credentials - AuthType: %d, SecretID: %s, SecretIDNew: %s",
				updatedPool.PoolCredentials.AuthType, updatedPool.PoolCredentials.SecretID, updatedPool.PoolCredentials.SecretIDNew)
		}
	}

	logger.Infof("Secret ID swap completed for pool: %s", poolUUID)
	return nil
}

// UpdateCacheWithNewSecret updates cache with new secret ID and password
// Secret ID and password will be retrieved from database/cache internally
func (a *RotateVcpToVsaCertificateActivity) UpdateCacheWithNewSecret(ctx context.Context, poolUUID string) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Updating cache with new secret for pool: %s", poolUUID)

	// Get pool details to retrieve secret information
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(poolViews) == 0 {
		logger.Errorf("Pool %s not found", poolUUID)
		return fmt.Errorf("pool %s not found", poolUUID)
	}

	pool := ConvertPoolViewToPool(poolViews[0])

	// Get the new secret ID from the database (should be in secret_id field after swap)
	newSecretID := pool.PoolCredentials.SecretID
	if newSecretID == "" {
		logger.Errorf("SecretID is empty for pool %s - cannot update cache", poolUUID)
		return fmt.Errorf("secret_id is empty for pool %s", poolUUID)
	}

	logger.Infof("New Secret ID: %s", newSecretID)

	// Retrieve new password from Secret Manager
	logger.Infof("Retrieving new password from Secret Manager using secret_id: %s", newSecretID)
	newPassword, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, newSecretID)
	if err != nil {
		logger.Errorf("Failed to retrieve new password from Secret Manager: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	// Update cache to use new secret ID
	logger.Infof("Adding new secret to user auth cache...")
	common.AddToUserAuthCache(newSecretID, newPassword)

	logger.Infof("Cache updated with new secret ID: %s", newSecretID)
	logger.Infof("Cache update completed for pool: %s", poolUUID)
	return nil
}

// GetOldSecretID gets the old secret ID from the database after swap
// After swap, the old secret ID is stored in secret_id_new field
func (a *RotateVcpToVsaCertificateActivity) GetOldSecretID(ctx context.Context, poolUUID string) (string, error) {
	logger := util.GetLogger(ctx)
	logger.Infof("Getting old secret ID for pool: %s", poolUUID)

	// Get pool details
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return "", vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(poolViews) == 0 {
		logger.Errorf("Pool %s not found", poolUUID)
		return "", fmt.Errorf("pool %s not found", poolUUID)
	}

	pool := ConvertPoolViewToPool(poolViews[0])
	logger.Infof("Pool Details - ID: %d, Name: %s, State: %s, DeploymentName: %s",
		pool.ID, pool.Name, pool.State, pool.DeploymentName)

	if pool.PoolCredentials != nil {
		logger.Infof("Pool Credentials - AuthType: %d, SecretID: %s, SecretIDNew: %s",
			pool.PoolCredentials.AuthType, pool.PoolCredentials.SecretID, pool.PoolCredentials.SecretIDNew)
	} else {
		logger.Errorf("Pool credentials are nil for pool %s", poolUUID)
		return "", fmt.Errorf("pool credentials are nil for pool %s", poolUUID)
	}

	// After swap, the old secret ID is stored in secret_id_new field
	oldSecretID := pool.PoolCredentials.SecretIDNew
	logger.Infof("Old secret ID retrieved from secret_id_new: %s", oldSecretID)

	logger.Infof("Retrieved old secret ID for pool: %s", poolUUID)
	return oldSecretID, nil
}

// RemoveOldSecretFromCache removes old secret from cache
func (a *RotateVcpToVsaCertificateActivity) RemoveOldSecretFromCache(ctx context.Context, poolUUID, oldSecretID string) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Removing old secret from cache for pool: %s", poolUUID)

	// Remove old secret from cache
	logger.Infof("Removing old secret from user auth cache...")
	common.RemoveFromUserAuthCache(oldSecretID)

	logger.Infof("Old secret removed from cache: %s", oldSecretID)
	logger.Infof("Old secret removed from cache for pool: %s", poolUUID)
	return nil
}

// ============================================================================
// COMPOSITE ACTIVITIES
// ============================================================================

// ValidateNewPasswordConnectivity validates new password connectivity
// Password will be retrieved from database/cache internally
func (a *RotateVcpToVsaCertificateActivity) ValidateNewPasswordConnectivity(ctx context.Context, poolUUID string) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Validating new password connectivity for pool: %s", poolUUID)

	// Get pool details
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(poolViews) == 0 {
		logger.Errorf("Pool %s not found", poolUUID)
		return fmt.Errorf("pool %s not found", poolUUID)
	}

	pool := ConvertPoolViewToPool(poolViews[0])
	logger.Infof("Pool Details - ID: %d, Name: %s, State: %s, DeploymentName: %s",
		pool.ID, pool.Name, pool.State, pool.DeploymentName)

	if pool.PoolCredentials != nil {
		logger.Infof("Current Pool Credentials - AuthType: %d, SecretID: %s, SecretIDNew: %s",
			pool.PoolCredentials.AuthType, pool.PoolCredentials.SecretID, pool.PoolCredentials.SecretIDNew)
	} else {
		logger.Errorf("Pool credentials are nil for pool %s", poolUUID)
		return fmt.Errorf("pool credentials are nil for pool %s", poolUUID)
	}

	// Retrieve new password from Secret Manager using secret_id_new
	if pool.PoolCredentials.SecretIDNew == "" {
		logger.Errorf("SecretIDNew is empty for pool %s - cannot retrieve new password", poolUUID)
		return fmt.Errorf("secret_id_new is empty for pool %s", poolUUID)
	}

	logger.Infof("Retrieving new password from Secret Manager using secret_id_new: %s", pool.PoolCredentials.SecretIDNew)
	newPassword, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, pool.PoolCredentials.SecretIDNew)
	if err != nil {
		logger.Errorf("Failed to retrieve new password from Secret Manager: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	logger.Infof("Testing new password connectivity...")
	// Test new password connectivity
	err = a.testPasswordConnectivity(ctx, pool, newPassword)
	if err != nil {
		logger.Errorf("New password failed connectivity test: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrPasswordConnectivityTestFailed, err)
	}

	logger.Infof("New password connectivity validated for pool: %s", poolUUID)
	return nil
}

// ListPoolsWithPasswordAuth returns one batch of pools that use password authentication (auth type USERNAME_PWD_SEC_MGR).
// Offset and limit support pagination to avoid Temporal activity result size limit.
func (a *RotateVcpToVsaCertificateActivity) ListPoolsWithPasswordAuth(ctx context.Context, offset, limit int) (*ListPoolsBatchResult, error) {
	logger := util.GetLogger(ctx)
	se := a.SE
	logger.Debugf("Listing pools with password authentication (offset=%d, limit=%d)", offset, limit)

	authTypeValue := fmt.Sprintf("%d", env.USERNAME_PWD_SEC_MGR)
	readyStates := env.GetCertificateRotationPoolStates()
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("pool_credentials->>'auth_type'", "=", authTypeValue),
		dbutils.NewFilterCondition("state", "in", readyStates),
	)
	pagination := &dbutils.Pagination{Limit: limit, Offset: offset}

	poolViews, err := se.ListPoolsWithFilterAndPaginationOrderedByUUID(ctx, filter, pagination)
	if err != nil {
		logger.Errorf("Failed to list pools with password authentication: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	hasMore := len(poolViews) == limit
	var pools []*datamodel.Pool
	for _, poolView := range poolViews {
		pools = append(pools, ConvertPoolViewToPool(poolView))
	}
	logger.Debugf("Retrieved batch of %d password auth pools (hasMore=%v)", len(pools), hasMore)
	return &ListPoolsBatchResult{Pools: pools, HasMore: hasMore}, nil
}
