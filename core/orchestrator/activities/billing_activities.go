package activities

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// billingLedgerEnabled gates all billing-ledger reads/writes behind a feature flag so the
// ledger can be rolled out (and killed) per environment. Defaults to false: when disabled every
// ledger activity is a safe no-op. Read once at process start, consistent with other VCP flags.
var billingLedgerEnabled = env.GetBool("ENABLE_BILLING_LEDGER", false)

// BillingActivity handles billing-related operations
type BillingActivity struct {
	SE database.Storage
}

// ledgerDisabled reports whether billing-ledger operations are turned off via the feature flag.
// When disabled, the calling activity should return nil (no-op) before touching the datastore.
func ledgerDisabled(ctx context.Context, operation string) bool {
	if billingLedgerEnabled {
		return false
	}
	util.GetLogger(ctx).Debugf("Ledger: ENABLE_BILLING_LEDGER is disabled; skipping %s", operation)
	return true
}

// BillingLedgerInput contains the input parameters for creating a billing ledger entry
type BillingLedgerInput struct {
	ResourceName   string
	ResourceUUID   string
	ResourceType   metadata.ResourceType
	MeasuredType   metadata.MeasuredType
	Size           float64
	ConsumerID     string
	DeploymentName string
}

// CreateBillingLedgerEntry creates a billing ledger entry for any resource type
func (a *BillingActivity) CreateBillingLedgerEntry(ctx context.Context, input BillingLedgerInput) error {
	recordHeartbeat(ctx, fmt.Sprintf("Ledger: Creating billing ledger entry for %s/%s", input.ResourceType, input.MeasuredType))
	logger := util.GetLogger(ctx)

	ledger := &datamodel.BillingLedger{
		BaseModel: datamodel.BaseModel{
			UUID: utils.RandomUUID(),
		},
		ResourceName:   input.ResourceName,
		ResourceUUID:   input.ResourceUUID,
		ResourceType:   input.ResourceType,
		MeasuredType:   input.MeasuredType,
		Quantity:       input.Size,
		ConsumerID:     input.ConsumerID,
		DeploymentName: input.DeploymentName,
	}

	_, err := a.SE.CreateBillingLedger(ctx, ledger)
	if err != nil {
		logger.Errorf("Ledger: Failed to create billing ledger entry for %s/%s - Resource: %s, Error: %v",
			input.ResourceType, input.MeasuredType, input.ResourceUUID, err)
		return err
	}

	return nil
}

// getPoolResourceType returns the appropriate ResourceType based on whether pool is Regional HA
func getPoolResourceType(pool *datamodel.Pool) metadata.ResourceType {
	if pool.PoolAttributes != nil && pool.PoolAttributes.IsRegionalHA {
		return metadata.VolumePoolRegionalHA
	}
	return metadata.VolumePool
}

// getPoolAccountName returns the billing account recorded on the pool, or "" when absent. This
// is the ledger's full pool-billing eligibility check: unlike the telemetry pool collector, the
// ledger does not exclude HYPERSCALERDISABLED accounts, so a disabled account is still billed.
func getPoolAccountName(pool *datamodel.Pool) string {
	if pool.PoolAttributes != nil {
		return pool.PoolAttributes.AccountName
	}
	return ""
}

// hasBillableHotTier reports whether the pool's hot tier size is billable: auto-tiering is
// enabled and the configured hot tier size is positive.
func hasBillableHotTier(pool *datamodel.Pool) bool {
	return pool.AllowAutoTiering && pool.AutoTieringConfig != nil && pool.AutoTieringConfig.HotTierSizeInBytes > 0
}

// CreatePoolBillingLedger creates billing ledger entries for a pool.
// Creates entries for both ALLOCATED_SIZE and POOL_HOT_TIER_PROVISIONED_SIZE (if AT is enabled).
// Existing entries are deleted when the pool is not billable (see getPoolAccountName).
// Uses VOLUME_POOL_REGIONAL_HA resource type for Regional HA pools, VOLUME_POOL otherwise.
func (a *BillingActivity) CreatePoolBillingLedger(ctx context.Context, pool *datamodel.Pool) error {
	if ledgerDisabled(ctx, "CreatePoolBillingLedger") {
		return nil
	}
	resourceType := getPoolResourceType(pool)

	accountName := getPoolAccountName(pool)
	if accountName == "" {
		if err := a.DeleteBillingLedgerByMeasuredType(ctx, pool.UUID, resourceType, metadata.PoolAllocatedSize); err != nil {
			return err
		}
		return a.DeleteBillingLedgerByMeasuredType(ctx, pool.UUID, resourceType, metadata.PoolHotTierProvisionedSize)
	}

	// Create main pool billing ledger entry (ALLOCATED_SIZE)
	if err := a.CreateBillingLedgerEntry(ctx, BillingLedgerInput{
		ResourceName:   pool.Name,
		ResourceUUID:   pool.UUID,
		ResourceType:   resourceType,
		MeasuredType:   metadata.PoolAllocatedSize,
		Size:           float64(pool.SizeInBytes),
		ConsumerID:     accountName,
		DeploymentName: pool.DeploymentName,
	}); err != nil {
		return err
	}

	// Create hot tier billing ledger entry for AT pools
	return a.upsertPoolHotTierBillingLedger(ctx, pool, resourceType, accountName)
}

// CreatePoolHotTierBillingLedger creates a billing ledger entry for a pool's hot tier size.
// Deletes an existing hot-tier ledger when the pool is not billable or has no billable hot
// tier, so a pool that previously had auto-tiering stops being billed for hot-tier size.
// Uses VOLUME_POOL_REGIONAL_HA resource type for Regional HA pools, VOLUME_POOL otherwise.
func (a *BillingActivity) CreatePoolHotTierBillingLedger(ctx context.Context, pool *datamodel.Pool) error {
	if ledgerDisabled(ctx, "CreatePoolHotTierBillingLedger") {
		return nil
	}
	accountName := getPoolAccountName(pool)
	return a.upsertPoolHotTierBillingLedger(ctx, pool, getPoolResourceType(pool), accountName)
}

// upsertPoolHotTierBillingLedger writes the hot-tier entry for a pool already resolved to
// accountName, or deletes it when accountName is empty or the hot tier is not billable.
func (a *BillingActivity) upsertPoolHotTierBillingLedger(ctx context.Context, pool *datamodel.Pool, resourceType metadata.ResourceType, accountName string) error {
	if accountName == "" || !hasBillableHotTier(pool) {
		return a.DeleteBillingLedgerByMeasuredType(ctx, pool.UUID, resourceType, metadata.PoolHotTierProvisionedSize)
	}

	return a.CreateBillingLedgerEntry(ctx, BillingLedgerInput{
		ResourceName:   pool.Name,
		ResourceUUID:   pool.UUID,
		ResourceType:   resourceType,
		MeasuredType:   metadata.PoolHotTierProvisionedSize,
		Size:           float64(pool.AutoTieringConfig.HotTierSizeInBytes),
		ConsumerID:     accountName,
		DeploymentName: pool.DeploymentName,
	})
}

// getVolumeResourceType returns the appropriate ResourceType based on whether volume is Regional HA
func getVolumeResourceType(volume *datamodel.Volume) metadata.ResourceType {
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.IsRegionalHA {
		return metadata.VolumeRegionalHA
	}
	return metadata.Volume
}

// isBackupEnabledVolume returns true if the volume has backup enabled for billing purposes.
func isBackupEnabledVolume(volume *datamodel.Volume) bool {
	return volume.DataProtection != nil &&
		volume.DataProtection.BackupChainBytes != nil &&
		*volume.DataProtection.BackupChainBytes > 0
}

// getVolumeAccountName returns the owning account recorded on the volume, or "" when absent.
func getVolumeAccountName(volume *datamodel.Volume) string {
	if volume.VolumeAttributes != nil {
		return volume.VolumeAttributes.AccountName
	}
	return ""
}

// getVolumeDeploymentName returns the deployment name recorded on the volume, or "" when absent.
func getVolumeDeploymentName(volume *datamodel.Volume) string {
	if volume.VolumeAttributes != nil {
		return volume.VolumeAttributes.DeploymentName
	}
	return ""
}

// volumeBackupBillingAccount returns the account the backup-enabled volume allocated size is
// billed to, or "" when the volume is not billable. Mirrors the volume collector except for
// account state: the volume needs a name and an owning account name; a HYPERSCALERDISABLED
// account is still billed by the ledger (unlike the telemetry collector); a volume whose
// attached vault cannot be resolved is not billed; and a cross-project vault is billed to the
// vault's owning account instead of the volume owner.
func (a *BillingActivity) volumeBackupBillingAccount(ctx context.Context, volume *datamodel.Volume) (string, error) {
	accountName := getVolumeAccountName(volume)
	if volume.Name == "" || accountName == "" {
		return "", nil
	}
	if volume.DataProtection.BackupVaultID == "" {
		return accountName, nil
	}
	backupVault, err := a.SE.GetBackupVault(ctx, volume.DataProtection.BackupVaultID)
	if err != nil {
		if utilErrors.IsNotFoundErr(err) {
			return "", nil
		}
		return "", err
	}
	if backupVault == nil {
		return "", nil
	}
	if backupVault.ServiceType == datamodel.ServiceTypeCrossProject &&
		backupVault.Account != nil && backupVault.Account.Name != "" {
		return backupVault.Account.Name, nil
	}
	return accountName, nil
}

// CreateVolumeBillingLedger creates or updates a billing ledger entry for backup-enabled volume allocated size.
// Deletes existing ledger entries when backup is not enabled or the volume is not billable
// (see volumeBackupBillingAccount).
// Uses VOLUME_REGIONAL_HA resource type for Regional HA volumes, VOLUME otherwise.
func (a *BillingActivity) CreateVolumeBillingLedger(ctx context.Context, volume *datamodel.Volume) error {
	if ledgerDisabled(ctx, "CreateVolumeBillingLedger") {
		return nil
	}
	resourceType := getVolumeResourceType(volume)

	if !isBackupEnabledVolume(volume) {
		return a.DeleteBillingLedgerByMeasuredType(ctx, volume.UUID, resourceType, metadata.BackupEnabledVolumeAllocatedSize)
	}

	billingAccount, err := a.volumeBackupBillingAccount(ctx, volume)
	if err != nil {
		return err
	}
	if billingAccount == "" {
		return a.DeleteBillingLedgerByMeasuredType(ctx, volume.UUID, resourceType, metadata.BackupEnabledVolumeAllocatedSize)
	}

	return a.CreateBillingLedgerEntry(ctx, BillingLedgerInput{
		ResourceName:   volume.Name,
		ResourceUUID:   volume.UUID,
		ResourceType:   resourceType,
		MeasuredType:   metadata.BackupEnabledVolumeAllocatedSize,
		Size:           float64(volume.SizeInBytes),
		ConsumerID:     billingAccount,
		DeploymentName: getVolumeDeploymentName(volume),
	})
}

// crossRegionTransferVault returns the backup's vault when the backup's transfer bytes are
// billable, or nil when they are not. Mirrors the backup collector except for account state:
// the vault must be a cross-region vault whose backup region is set and differs from its
// source region, and the backup must have transferred bytes. A HYPERSCALERDISABLED owning
// account is still billed by the ledger (unlike the telemetry collector).
func (a *BillingActivity) crossRegionTransferVault(ctx context.Context, backup *datamodel.Backup) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)

	// A backup without an associated vault cannot be cross-region; skip the lookup entirely.
	// GetBackupVaultById would otherwise query "id = 0" and return a not-found error.
	if backup.BackupVaultID == 0 {
		logger.Debugf("Ledger: Backup %s for volume %s has no backup vault; transfer bytes not billable", backup.UUID, backup.VolumeUUID)
		return nil, nil
	}

	backupVault, err := a.SE.GetBackupVaultById(ctx, backup.BackupVaultID)
	if err != nil {
		return nil, err
	}
	if backupVault == nil || backupVault.BackupVaultType != CrossRegionBackupType {
		logger.Debugf("Ledger: Backup %s for volume %s is not cross-region; transfer bytes not billable", backup.UUID, backup.VolumeUUID)
		return nil, nil
	}
	if backupVault.BackupRegionName == nil || *backupVault.BackupRegionName == "" ||
		(backupVault.SourceRegionName != nil && *backupVault.SourceRegionName == *backupVault.BackupRegionName) {
		logger.Warnf("Ledger: Backup %s for volume %s is in vault %s without a distinct backup region; transfer bytes not billable", backup.UUID, backup.VolumeUUID, backupVault.Name)
		return nil, nil
	}
	if backup.Attributes.GetTotalTransferBytes() <= 0 {
		return nil, nil
	}

	return backupVault, nil
}

// CreateCrossRegionBackupTransferBillingLedger creates a billing ledger entry capturing the
// cross-region transfer bytes of a newly created backup. It is invoked whenever a backup is
// created, with that backup passed directly (it is the latest backup by definition). Because
// the collector bills transfer bytes from the volume's latest backup, the volume's entry is
// deleted when that backup's transfer bytes are not billable (see crossRegionTransferVault).
// It is a no-op when the backup or its attributes are nil. Uses VOLUME_UUID as the resource
// so the entry is keyed per volume. Deployment name is the backup vault name.
func (a *BillingActivity) CreateCrossRegionBackupTransferBillingLedger(ctx context.Context, backup *datamodel.Backup) error {
	if ledgerDisabled(ctx, "CreateCrossRegionBackupTransferBillingLedger") {
		return nil
	}
	logger := util.GetLogger(ctx)

	if backup == nil || backup.Attributes == nil {
		return nil
	}

	backupVault, err := a.crossRegionTransferVault(ctx, backup)
	if err != nil {
		return err
	}
	if backupVault == nil {
		if backup.VolumeUUID == "" {
			return nil
		}
		return a.DeleteBillingLedgerByMeasuredType(ctx, backup.VolumeUUID, metadata.Backup, metadata.CbsCrossRegionVolumeBackupTransferBytes)
	}

	ledger := &datamodel.BillingLedger{
		BaseModel:      datamodel.BaseModel{UUID: utils.RandomUUID()},
		ResourceName:   backup.Attributes.VolumeName,
		ResourceUUID:   backup.VolumeUUID,
		ResourceType:   metadata.Backup,
		MeasuredType:   metadata.CbsCrossRegionVolumeBackupTransferBytes,
		Quantity:       float64(backup.Attributes.GetTotalTransferBytes()),
		ConsumerID:     backup.Attributes.AccountIdentifier,
		DeploymentName: backupVault.Name,
	}
	if _, err := a.SE.CreateBillingLedger(ctx, ledger); err != nil {
		logger.Errorf("Ledger: Failed to create cross-region backup transfer billing ledger for volume %s: %v", backup.VolumeUUID, err)
		return err
	}
	return nil
}

// DeleteCrossRegionBackupTransferBillingLedger deletes the cross-region backup transfer-bytes
// billing ledger entry for the backup's volume. The transfer-bytes entry is keyed per volume
// (holding the latest backup's transfer bytes), so this should only be invoked once the volume no
// longer has any backups being billed (e.g. the last backup was deleted); otherwise the volume
// would stop accruing transfer billing while other backups still exist. It is a no-op when backup
// or its VolumeUUID is empty, and deleting a non-existent entry is itself a no-op.
func (a *BillingActivity) DeleteCrossRegionBackupTransferBillingLedger(ctx context.Context, backup *datamodel.Backup) error {
	if ledgerDisabled(ctx, "DeleteCrossRegionBackupTransferBillingLedger") {
		return nil
	}
	if backup == nil || backup.VolumeUUID == "" {
		return nil
	}
	return a.DeleteBillingLedgerByMeasuredType(ctx, backup.VolumeUUID, metadata.Backup, metadata.CbsCrossRegionVolumeBackupTransferBytes)
}

// DeleteVolumeBillingLedger deletes the backup-enabled volume allocated size billing ledger entry.
func (a *BillingActivity) DeleteVolumeBillingLedger(ctx context.Context, volume *datamodel.Volume) error {
	if ledgerDisabled(ctx, "DeleteVolumeBillingLedger") {
		return nil
	}
	resourceType := getVolumeResourceType(volume)
	return a.DeleteBillingLedgerByMeasuredType(ctx, volume.UUID, resourceType, metadata.BackupEnabledVolumeAllocatedSize)
}

// DeletePoolBillingLedgers deletes pool billing ledger entries (allocated size and hot tier when present).
func (a *BillingActivity) DeletePoolBillingLedgers(ctx context.Context, pool *datamodel.Pool) error {
	if ledgerDisabled(ctx, "DeletePoolBillingLedgers") {
		return nil
	}
	resourceType := getPoolResourceType(pool)
	if err := a.DeleteBillingLedgerByMeasuredType(ctx, pool.UUID, resourceType, metadata.PoolAllocatedSize); err != nil {
		return err
	}
	return a.DeleteBillingLedgerByMeasuredType(ctx, pool.UUID, resourceType, metadata.PoolHotTierProvisionedSize)
}

// DeleteBillingLedgerByMeasuredType deletes a billing ledger entry for a resource, resource type, and measured type.
func (a *BillingActivity) DeleteBillingLedgerByMeasuredType(ctx context.Context, resourceUUID string, resourceType metadata.ResourceType, measuredType metadata.MeasuredType) error {
	recordHeartbeat(ctx, "Ledger: Deleting billing ledger entry")
	logger := util.GetLogger(ctx)
	logger.Infof("Ledger: Deleting billing ledger for resource UUID: %s, resource type: %s, measured type: %s", resourceUUID, resourceType, measuredType)
	return a.SE.DeleteBillingLedgersByResourceUUIDAndMeasuredType(ctx, resourceUUID, resourceType, measuredType)
}
