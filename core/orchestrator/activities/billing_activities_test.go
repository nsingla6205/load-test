package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

var errLedger = errors.New("ledger failure")

// init force-enables the billing-ledger feature flag for the activities test binary.
// The flag defaults to false in production (opt-in rollout), but the existing unit tests
// exercise the real ledger read/write paths and assert datastore interactions, so they must
// run with the ledger turned on. Tests that verify the disabled path flip it locally and restore.
func init() {
	billingLedgerEnabled = true
}

// backupChainBytes returns a pointer to the given size for DataProtection wiring.
func backupChainBytes(size int64) *int64 {
	return &size
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string {
	return &s
}

// billablePool returns a pool that passes the pool-level billing checks for account-1.
func billablePool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:           "pool-1",
		SizeInBytes:    500,
		DeploymentName: "deployment-1",
		PoolAttributes: &datamodel.PoolAttributes{AccountName: "account-1"},
	}
}

// billableVolume returns a backup-enabled volume that passes the volume-level billing checks for account-1.
func billableVolume() *datamodel.Volume {
	return &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-uuid-1"},
		Name:             "vol-1",
		SizeInBytes:      2048,
		VolumeAttributes: &datamodel.VolumeAttributes{AccountName: "account-1", DeploymentName: "deployment-1"},
		DataProtection:   &datamodel.DataProtection{BackupChainBytes: backupChainBytes(10)},
	}
}

// crossRegionVault returns a cross-region vault whose backup region differs from its source region.
func crossRegionVault() *datamodel.BackupVault {
	return &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: 10},
		Name:             "cr-vault",
		BackupVaultType:  CrossRegionBackupType,
		SourceRegionName: strPtr("us-central1"),
		BackupRegionName: strPtr("us-east1"),
	}
}

// crossRegionBackup returns a completed backup in vault 10 with billable transfer bytes.
func crossRegionBackup() *datamodel.Backup {
	return &datamodel.Backup{
		VolumeUUID:    "vol-1",
		BackupVaultID: 10,
		Attributes: &datamodel.BackupAttributes{
			AccountIdentifier:  "project-1",
			VolumeName:         "my-volume",
			TotalTransferBytes: 5000,
		},
	}
}

// TestBillingLedgerDisabled_NoOp verifies that when ENABLE_BILLING_LEDGER is off every ledger
// entrypoint returns nil without touching the datastore (mockSE has no expectations set).
func TestBillingLedgerDisabled_NoOp(t *testing.T) {
	ctx := context.Background()
	prev := billingLedgerEnabled
	billingLedgerEnabled = false
	t.Cleanup(func() { billingLedgerEnabled = prev })

	mockSE := database.NewMockStorage(t)
	activity := &BillingActivity{SE: mockSE}

	assert.NoError(t, activity.CreatePoolBillingLedger(ctx, &datamodel.Pool{Name: "pool-1", SizeInBytes: 100}))
	assert.NoError(t, activity.CreateVolumeBillingLedger(ctx, &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-1"}}))
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, &datamodel.Backup{
		VolumeUUID:    "vol-1",
		BackupVaultID: 10,
		Attributes:    &datamodel.BackupAttributes{TotalTransferBytes: 5000},
	}))
	assert.NoError(t, activity.DeletePoolBillingLedgers(ctx, &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}}))
	assert.NoError(t, activity.DeleteVolumeBillingLedger(ctx, &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-1"}}))
	assert.NoError(t, activity.DeleteCrossRegionBackupTransferBillingLedger(ctx, &datamodel.Backup{VolumeUUID: "vol-1"}))
}

func TestCreateCrossRegionBackupTransferBillingLedger(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	mockSE.On("GetBackupVaultById", ctx, int64(10)).Return(crossRegionVault(), nil)
	mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
		return l.ResourceUUID == "vol-1" &&
			l.ResourceName == "my-volume" &&
			l.ResourceType == metadata.Backup &&
			l.MeasuredType == metadata.CbsCrossRegionVolumeBackupTransferBytes &&
			l.Quantity == 5000 &&
			l.ConsumerID == "project-1" &&
			l.DeploymentName == "cr-vault"
	})).Return(&datamodel.BillingLedger{}, nil)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, crossRegionBackup()))
}

// TestCreateCrossRegionBackupTransferBillingLedger_NotBillable_DeletesLedger verifies that the
// volume's transfer-bytes entry is closed whenever its latest backup's transfer is not billable.
func TestCreateCrossRegionBackupTransferBillingLedger_NotBillable_DeletesLedger(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		mutateBackup func(*datamodel.Backup)
		vault        *datamodel.BackupVault
	}{
		{
			name:         "NoBackupVault",
			mutateBackup: func(b *datamodel.Backup) { b.BackupVaultID = 0 },
		},
		{
			name: "NonCrossRegionVault",
			vault: &datamodel.BackupVault{
				BaseModel:       datamodel.BaseModel{ID: 10},
				BackupVaultType: "STANDARD",
			},
		},
		{
			name: "MissingBackupRegion",
			vault: func() *datamodel.BackupVault {
				v := crossRegionVault()
				v.BackupRegionName = nil
				return v
			}(),
		},
		{
			name: "BackupRegionMatchesSourceRegion",
			vault: func() *datamodel.BackupVault {
				v := crossRegionVault()
				v.BackupRegionName = strPtr("us-central1")
				return v
			}(),
		},
		{
			name:         "ZeroTransferBytes",
			mutateBackup: func(b *datamodel.Backup) { b.Attributes.TotalTransferBytes = 0 },
			vault:        crossRegionVault(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			mockSE := database.NewMockStorage(tt)
			backup := crossRegionBackup()
			if tc.mutateBackup != nil {
				tc.mutateBackup(backup)
			}
			if tc.vault != nil {
				mockSE.On("GetBackupVaultById", ctx, int64(10)).Return(tc.vault, nil)
			}
			mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "vol-1",
				metadata.Backup, metadata.CbsCrossRegionVolumeBackupTransferBytes).Return(nil).Once()

			activity := &BillingActivity{SE: mockSE}
			assert.NoError(tt, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, backup))
		})
	}
}

func TestCreateCrossRegionBackupTransferBillingLedger_NilBackup_NoOp(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, nil))
}

func TestCreateCrossRegionBackupTransferBillingLedger_NotBillableWithoutVolume_NoOp(t *testing.T) {
	ctx := context.Background()
	// No expectations: there is no volume entry to delete.
	mockSE := database.NewMockStorage(t)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, &datamodel.Backup{
		Attributes: &datamodel.BackupAttributes{AccountIdentifier: "project-1"},
	}))
}

func TestCreateCrossRegionBackupTransferBillingLedger_VaultLookupFails(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	mockSE.On("GetBackupVaultById", ctx, int64(10)).Return(nil, errLedger)

	activity := &BillingActivity{SE: mockSE}
	assert.ErrorIs(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, crossRegionBackup()), errLedger)
}

// TestCreateCrossRegionBackupTransferBillingLedger_BillsHyperscalerDisabledAccount documents
// that the ledger bills cross-region transfer bytes regardless of the owning account's state:
// unlike the telemetry backup collector, it no longer excludes HYPERSCALERDISABLED accounts.
// No GetAccount stub is registered; the strict mock would fail the test if
// crossRegionTransferVault looked up the account.
func TestCreateCrossRegionBackupTransferBillingLedger_BillsHyperscalerDisabledAccount(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	mockSE.On("GetBackupVaultById", ctx, int64(10)).Return(crossRegionVault(), nil)
	mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
		return l.ConsumerID == "project-1"
	})).Return(&datamodel.BillingLedger{}, nil)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, crossRegionBackup()))
}

func TestCreateCrossRegionBackupTransferBillingLedger_CreateFails(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	mockSE.On("GetBackupVaultById", ctx, int64(10)).Return(crossRegionVault(), nil)
	mockSE.On("CreateBillingLedger", ctx, mock.Anything).Return(nil, errLedger)

	activity := &BillingActivity{SE: mockSE}
	assert.ErrorIs(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, crossRegionBackup()), errLedger)
}

func TestCreateCrossRegionBackupTransferBillingLedger_NilAttributes_NoOp(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, &datamodel.Backup{VolumeUUID: "vol-1"}))
}

func TestCreateBillingLedgerEntry(t *testing.T) {
	ctx := context.Background()

	t.Run("CreatesEntryFromInput", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.ResourceName == "pool-1" &&
				l.ResourceUUID == "pool-uuid-1" &&
				l.ResourceType == metadata.VolumePool &&
				l.MeasuredType == metadata.PoolAllocatedSize &&
				l.Quantity == 100 &&
				l.ConsumerID == "consumer-1" &&
				l.DeploymentName == "deployment-1" &&
				l.UUID != ""
		})).Return(&datamodel.BillingLedger{}, nil)

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreateBillingLedgerEntry(ctx, BillingLedgerInput{
			ResourceName:   "pool-1",
			ResourceUUID:   "pool-uuid-1",
			ResourceType:   metadata.VolumePool,
			MeasuredType:   metadata.PoolAllocatedSize,
			Size:           100,
			ConsumerID:     "consumer-1",
			DeploymentName: "deployment-1",
		}))
	})

	t.Run("PropagatesCreateError", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.Anything).Return(nil, errLedger)

		activity := &BillingActivity{SE: mockSE}
		assert.ErrorIs(tt, activity.CreateBillingLedgerEntry(ctx, BillingLedgerInput{ResourceUUID: "pool-uuid-1"}), errLedger)
	})
}

func TestGetPoolResourceType(t *testing.T) {
	assert.Equal(t, metadata.VolumePool, getPoolResourceType(&datamodel.Pool{}))
	assert.Equal(t, metadata.VolumePool, getPoolResourceType(&datamodel.Pool{PoolAttributes: &datamodel.PoolAttributes{}}))
	assert.Equal(t, metadata.VolumePoolRegionalHA,
		getPoolResourceType(&datamodel.Pool{PoolAttributes: &datamodel.PoolAttributes{IsRegionalHA: true}}))
}

func TestGetPoolAccountName(t *testing.T) {
	assert.Equal(t, "", getPoolAccountName(&datamodel.Pool{}))
	assert.Equal(t, "account-1", getPoolAccountName(&datamodel.Pool{PoolAttributes: &datamodel.PoolAttributes{AccountName: "account-1"}}))
}

func TestHasBillableHotTier(t *testing.T) {
	assert.False(t, hasBillableHotTier(&datamodel.Pool{}))
	assert.False(t, hasBillableHotTier(&datamodel.Pool{AllowAutoTiering: true}))
	assert.False(t, hasBillableHotTier(&datamodel.Pool{AllowAutoTiering: true, AutoTieringConfig: &datamodel.AutoTieringConfig{}}))
	assert.False(t, hasBillableHotTier(&datamodel.Pool{AutoTieringConfig: &datamodel.AutoTieringConfig{HotTierSizeInBytes: 250}}))
	assert.True(t, hasBillableHotTier(&datamodel.Pool{AllowAutoTiering: true, AutoTieringConfig: &datamodel.AutoTieringConfig{HotTierSizeInBytes: 250}}))
}

func TestGetVolumeResourceType(t *testing.T) {
	assert.Equal(t, metadata.Volume, getVolumeResourceType(&datamodel.Volume{}))
	assert.Equal(t, metadata.VolumeRegionalHA,
		getVolumeResourceType(&datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{IsRegionalHA: true}}))
}

func TestIsBackupEnabledVolume(t *testing.T) {
	assert.False(t, isBackupEnabledVolume(&datamodel.Volume{}))
	assert.False(t, isBackupEnabledVolume(&datamodel.Volume{DataProtection: &datamodel.DataProtection{}}))
	assert.False(t, isBackupEnabledVolume(&datamodel.Volume{
		DataProtection: &datamodel.DataProtection{BackupChainBytes: backupChainBytes(0)},
	}))
	assert.True(t, isBackupEnabledVolume(&datamodel.Volume{
		DataProtection: &datamodel.DataProtection{BackupChainBytes: backupChainBytes(10)},
	}))
}

func TestGetVolumeAttributeFields(t *testing.T) {
	assert.Equal(t, "", getVolumeAccountName(&datamodel.Volume{}))
	assert.Equal(t, "", getVolumeDeploymentName(&datamodel.Volume{}))

	volume := &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{AccountName: "account-1", DeploymentName: "deployment-1"}}
	assert.Equal(t, "account-1", getVolumeAccountName(volume))
	assert.Equal(t, "deployment-1", getVolumeDeploymentName(volume))
}

func TestCreatePoolBillingLedger(t *testing.T) {
	ctx := context.Background()

	t.Run("CreatesAllocatedSizeOnlyForNonATPool", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolAllocatedSize &&
				l.Quantity == 500 &&
				l.ConsumerID == "account-1" &&
				l.DeploymentName == "deployment-1"
		})).Return(&datamodel.BillingLedger{}, nil).Once()
		mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "pool-uuid-1",
			metadata.VolumePool, metadata.PoolHotTierProvisionedSize).Return(nil).Once()

		activity := &BillingActivity{SE: mockSE}
		require.NoError(tt, activity.CreatePoolBillingLedger(ctx, billablePool()))
	})

	t.Run("CreatesHotTierEntryForATPool", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolAllocatedSize
		})).Return(&datamodel.BillingLedger{}, nil).Once()
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolHotTierProvisionedSize && l.Quantity == 250 && l.ConsumerID == "account-1"
		})).Return(&datamodel.BillingLedger{}, nil).Once()

		pool := billablePool()
		pool.AllowAutoTiering = true
		pool.AutoTieringConfig = &datamodel.AutoTieringConfig{HotTierSizeInBytes: 250}

		activity := &BillingActivity{SE: mockSE}
		require.NoError(tt, activity.CreatePoolBillingLedger(ctx, pool))
	})

	t.Run("DeletesHotTierEntryForZeroHotTierSize", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolAllocatedSize
		})).Return(&datamodel.BillingLedger{}, nil).Once()
		mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "pool-uuid-1",
			metadata.VolumePool, metadata.PoolHotTierProvisionedSize).Return(nil).Once()

		pool := billablePool()
		pool.AllowAutoTiering = true
		pool.AutoTieringConfig = &datamodel.AutoTieringConfig{}

		activity := &BillingActivity{SE: mockSE}
		require.NoError(tt, activity.CreatePoolBillingLedger(ctx, pool))
	})

	notBillable := []struct {
		name string
		pool func() *datamodel.Pool
	}{
		{
			name: "NoPoolAttributes",
			pool: func() *datamodel.Pool {
				p := billablePool()
				p.PoolAttributes = nil
				return p
			},
		},
		{
			name: "NoAccountName",
			pool: func() *datamodel.Pool {
				p := billablePool()
				p.PoolAttributes.AccountName = ""
				return p
			},
		},
	}
	for _, tc := range notBillable {
		t.Run("DeletesEntriesWhenNotBillable/"+tc.name, func(tt *testing.T) {
			mockSE := database.NewMockStorage(tt)
			mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "pool-uuid-1",
				metadata.VolumePool, metadata.PoolAllocatedSize).Return(nil).Once()
			mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "pool-uuid-1",
				metadata.VolumePool, metadata.PoolHotTierProvisionedSize).Return(nil).Once()

			activity := &BillingActivity{SE: mockSE}
			require.NoError(tt, activity.CreatePoolBillingLedger(ctx, tc.pool()))
		})
	}

	// TestCreatePoolBillingLedger/BillsHyperscalerDisabledAccount documents that a pool is still
	// billed when its account is HYPERSCALERDISABLED: unlike the telemetry pool collector, the
	// ledger no longer excludes disabled accounts. No GetAccount stub is registered; the strict
	// mock would fail the test if CreatePoolBillingLedger looked up the account.
	t.Run("BillsHyperscalerDisabledAccount", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolAllocatedSize && l.ConsumerID == "account-1"
		})).Return(&datamodel.BillingLedger{}, nil).Once()
		mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "pool-uuid-1",
			metadata.VolumePool, metadata.PoolHotTierProvisionedSize).Return(nil).Once()

		activity := &BillingActivity{SE: mockSE}
		require.NoError(tt, activity.CreatePoolBillingLedger(ctx, billablePool()))
	})

	t.Run("PropagatesAllocatedSizeError", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.Anything).Return(nil, errLedger).Once()

		activity := &BillingActivity{SE: mockSE}
		assert.ErrorIs(tt, activity.CreatePoolBillingLedger(ctx, billablePool()), errLedger)
	})

	t.Run("PropagatesHotTierError", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolAllocatedSize
		})).Return(&datamodel.BillingLedger{}, nil).Once()
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolHotTierProvisionedSize
		})).Return(nil, errLedger).Once()

		pool := billablePool()
		pool.AllowAutoTiering = true
		pool.AutoTieringConfig = &datamodel.AutoTieringConfig{HotTierSizeInBytes: 250}

		activity := &BillingActivity{SE: mockSE}
		assert.ErrorIs(tt, activity.CreatePoolBillingLedger(ctx, pool), errLedger)
	})
}

func TestCreatePoolHotTierBillingLedger(t *testing.T) {
	ctx := context.Background()

	t.Run("DeletesLedgerForNonATPools", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "pool-uuid-1",
			metadata.VolumePool, metadata.PoolHotTierProvisionedSize).Return(nil).Twice()
		activity := &BillingActivity{SE: mockSE}

		nonAT := billablePool()
		assert.NoError(tt, activity.CreatePoolHotTierBillingLedger(ctx, nonAT))

		noConfig := billablePool()
		noConfig.AllowAutoTiering = true
		assert.NoError(tt, activity.CreatePoolHotTierBillingLedger(ctx, noConfig))
	})

	t.Run("DeletesLedgerWhenPoolNotBillable", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "pool-uuid-1",
			metadata.VolumePool, metadata.PoolHotTierProvisionedSize).Return(nil).Once()

		pool := billablePool()
		pool.PoolAttributes = nil
		pool.AllowAutoTiering = true
		pool.AutoTieringConfig = &datamodel.AutoTieringConfig{HotTierSizeInBytes: 250}

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreatePoolHotTierBillingLedger(ctx, pool))
	})

	t.Run("CreatesLedgerForBillableHotTier", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolHotTierProvisionedSize && l.Quantity == 250 && l.ConsumerID == "account-1"
		})).Return(&datamodel.BillingLedger{}, nil).Once()

		pool := billablePool()
		pool.AllowAutoTiering = true
		pool.AutoTieringConfig = &datamodel.AutoTieringConfig{HotTierSizeInBytes: 250}

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreatePoolHotTierBillingLedger(ctx, pool))
	})
}

func TestCreateVolumeBillingLedger(t *testing.T) {
	ctx := context.Background()

	t.Run("DeletesLedgerWhenBackupNotEnabled", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "vol-uuid-1",
			metadata.Volume, metadata.BackupEnabledVolumeAllocatedSize).Return(nil)

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreateVolumeBillingLedger(ctx, &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-1"},
		}))
	})

	t.Run("CreatesLedgerForBackupEnabledVolume", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.ResourceUUID == "vol-uuid-1" &&
				l.ResourceName == "vol-1" &&
				l.ResourceType == metadata.Volume &&
				l.MeasuredType == metadata.BackupEnabledVolumeAllocatedSize &&
				l.Quantity == 2048 &&
				l.ConsumerID == "account-1" &&
				l.DeploymentName == "deployment-1"
		})).Return(&datamodel.BillingLedger{}, nil)

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreateVolumeBillingLedger(ctx, billableVolume()))
	})

	t.Run("UsesRegionalHAResourceType", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.ResourceType == metadata.VolumeRegionalHA
		})).Return(&datamodel.BillingLedger{}, nil)

		volume := billableVolume()
		volume.VolumeAttributes.IsRegionalHA = true

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreateVolumeBillingLedger(ctx, volume))
	})

	t.Run("CreatesLedgerWhenVaultResolves", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("GetBackupVault", ctx, "vault-uuid-1").Return(&datamodel.BackupVault{}, nil)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.ConsumerID == "account-1"
		})).Return(&datamodel.BillingLedger{}, nil)

		volume := billableVolume()
		volume.DataProtection.BackupVaultID = "vault-uuid-1"

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreateVolumeBillingLedger(ctx, volume))
	})

	t.Run("CreatesLedgerForCrossRegionVault", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("GetBackupVault", ctx, "vault-uuid-1").Return(&datamodel.BackupVault{BackupVaultType: CrossRegionBackupType}, nil)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.BackupEnabledVolumeAllocatedSize && l.ConsumerID == "account-1"
		})).Return(&datamodel.BillingLedger{}, nil)

		volume := billableVolume()
		volume.DataProtection.BackupVaultID = "vault-uuid-1"

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreateVolumeBillingLedger(ctx, volume))
	})

	t.Run("BillsCrossProjectVaultOwner", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("GetBackupVault", ctx, "vault-uuid-1").Return(&datamodel.BackupVault{
			ServiceType: datamodel.ServiceTypeCrossProject,
			Account:     &datamodel.Account{Name: "vault-owner"},
		}, nil)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.ConsumerID == "vault-owner"
		})).Return(&datamodel.BillingLedger{}, nil)

		volume := billableVolume()
		volume.DataProtection.BackupVaultID = "vault-uuid-1"

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreateVolumeBillingLedger(ctx, volume))
	})

	// TestCreateVolumeBillingLedger/BillsHyperscalerDisabledAccount documents that a
	// backup-enabled volume is still billed when its account is HYPERSCALERDISABLED: unlike the
	// telemetry volume collector, the ledger no longer excludes disabled accounts. No GetAccount
	// stub is registered; the strict mock would fail the test if volumeBackupBillingAccount
	// looked up the account.
	t.Run("BillsHyperscalerDisabledAccount", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.ConsumerID == "account-1"
		})).Return(&datamodel.BillingLedger{}, nil)

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreateVolumeBillingLedger(ctx, billableVolume()))
	})

	notBillable := []struct {
		name        string
		volume      func() *datamodel.Volume
		vaultLookup bool
	}{
		{
			name: "NoVolumeName",
			volume: func() *datamodel.Volume {
				v := billableVolume()
				v.Name = ""
				return v
			},
		},
		{
			name: "NoAccountName",
			volume: func() *datamodel.Volume {
				v := billableVolume()
				v.VolumeAttributes.AccountName = ""
				return v
			},
		},
		{
			name: "VaultNotFound",
			volume: func() *datamodel.Volume {
				v := billableVolume()
				v.DataProtection.BackupVaultID = "vault-uuid-1"
				return v
			},
			vaultLookup: true,
		},
	}
	for _, tc := range notBillable {
		t.Run("DeletesLedgerWhenNotBillable/"+tc.name, func(tt *testing.T) {
			mockSE := database.NewMockStorage(tt)
			if tc.vaultLookup {
				mockSE.On("GetBackupVault", ctx, "vault-uuid-1").Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))
			}
			mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "vol-uuid-1",
				metadata.Volume, metadata.BackupEnabledVolumeAllocatedSize).Return(nil).Once()

			activity := &BillingActivity{SE: mockSE}
			assert.NoError(tt, activity.CreateVolumeBillingLedger(ctx, tc.volume()))
		})
	}

	t.Run("PropagatesVaultLookupError", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("GetBackupVault", ctx, "vault-uuid-1").Return(nil, errLedger)

		volume := billableVolume()
		volume.DataProtection.BackupVaultID = "vault-uuid-1"

		activity := &BillingActivity{SE: mockSE}
		assert.ErrorIs(tt, activity.CreateVolumeBillingLedger(ctx, volume), errLedger)
	})
}

func TestDeleteVolumeBillingLedger(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "vol-uuid-1",
		metadata.Volume, metadata.BackupEnabledVolumeAllocatedSize).Return(nil)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.DeleteVolumeBillingLedger(ctx, &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid-1"},
	}))
}

func TestDeleteCrossRegionBackupTransferBillingLedger(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "vol-uuid-1",
		metadata.Backup, metadata.CbsCrossRegionVolumeBackupTransferBytes).Return(nil)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.DeleteCrossRegionBackupTransferBillingLedger(ctx, &datamodel.Backup{VolumeUUID: "vol-uuid-1"}))
}

func TestDeleteCrossRegionBackupTransferBillingLedger_NilOrEmpty_NoOp(t *testing.T) {
	ctx := context.Background()
	// No expectations: delete must not be attempted without a volume UUID.
	mockSE := database.NewMockStorage(t)
	activity := &BillingActivity{SE: mockSE}

	assert.NoError(t, activity.DeleteCrossRegionBackupTransferBillingLedger(ctx, nil))
	assert.NoError(t, activity.DeleteCrossRegionBackupTransferBillingLedger(ctx, &datamodel.Backup{}))
}

func TestDeleteCrossRegionBackupTransferBillingLedger_PropagatesError(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "vol-uuid-1",
		metadata.Backup, metadata.CbsCrossRegionVolumeBackupTransferBytes).Return(errLedger)

	activity := &BillingActivity{SE: mockSE}
	assert.ErrorIs(t, activity.DeleteCrossRegionBackupTransferBillingLedger(ctx, &datamodel.Backup{VolumeUUID: "vol-uuid-1"}), errLedger)
}

func TestDeletePoolBillingLedgers(t *testing.T) {
	ctx := context.Background()

	t.Run("DeletesAllocatedAndHotTierEntries", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "pool-uuid-1",
			metadata.VolumePool, metadata.PoolAllocatedSize).Return(nil)
		mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "pool-uuid-1",
			metadata.VolumePool, metadata.PoolHotTierProvisionedSize).Return(nil)

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.DeletePoolBillingLedgers(ctx, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		}))
	})

	t.Run("PropagatesAllocatedSizeDeleteError", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "pool-uuid-1",
			metadata.VolumePool, metadata.PoolAllocatedSize).Return(errLedger)

		activity := &BillingActivity{SE: mockSE}
		assert.ErrorIs(tt, activity.DeletePoolBillingLedgers(ctx, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		}), errLedger)
	})
}

func TestDeleteBillingLedgerByMeasuredType(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	mockSE.On("DeleteBillingLedgersByResourceUUIDAndMeasuredType", ctx, "res-uuid-1",
		metadata.VolumePool, metadata.PoolAllocatedSize).Return(errLedger)

	activity := &BillingActivity{SE: mockSE}
	assert.ErrorIs(t, activity.DeleteBillingLedgerByMeasuredType(ctx, "res-uuid-1",
		metadata.VolumePool, metadata.PoolAllocatedSize), errLedger)
}
