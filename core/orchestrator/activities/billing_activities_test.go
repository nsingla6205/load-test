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

	backup := &datamodel.Backup{
		VolumeUUID:    "vol-1",
		BackupVaultID: 10,
		Attributes: &datamodel.BackupAttributes{
			AccountIdentifier:  "project-1",
			VolumeName:         "my-volume",
			TotalTransferBytes: 5000,
		},
	}
	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{ID: 10},
		Name:            "cr-vault",
		BackupVaultType: CrossRegionBackupType,
	}

	mockSE.On("GetBackupVaultById", ctx, int64(10)).Return(backupVault, nil)
	mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
		return l.ResourceUUID == "vol-1" &&
			l.ResourceType == metadata.Backup &&
			l.MeasuredType == metadata.CbsCrossRegionVolumeBackupTransferBytes &&
			l.Quantity == 5000
	})).Return(&datamodel.BillingLedger{}, nil)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, backup))
}

func TestCreateCrossRegionBackupTransferBillingLedger_NilBackup_NoOp(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, nil))
}

func TestCreateCrossRegionBackupTransferBillingLedger_NoBackupVault_NoOp(t *testing.T) {
	ctx := context.Background()
	// No expectations: GetBackupVaultById must not be called when there is no vault.
	mockSE := database.NewMockStorage(t)

	backup := &datamodel.Backup{
		VolumeUUID:    "vol-1",
		BackupVaultID: 0,
		Attributes:    &datamodel.BackupAttributes{AccountIdentifier: "project-1"},
	}

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, backup))
}

func TestCreateCrossRegionBackupTransferBillingLedger_InRegion_LeavesLedgerUnchanged(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	backup := &datamodel.Backup{
		VolumeUUID:    "vol-1",
		BackupVaultID: 10,
		Attributes:    &datamodel.BackupAttributes{AccountIdentifier: "project-1"},
	}
	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{ID: 10},
		Name:            "vault",
		BackupVaultType: "STANDARD",
	}

	mockSE.On("GetBackupVaultById", ctx, int64(10)).Return(backupVault, nil)

	activity := &BillingActivity{SE: mockSE}
	assert.NoError(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, backup))
}

func TestCreateCrossRegionBackupTransferBillingLedger_VaultLookupFails(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	backup := &datamodel.Backup{
		VolumeUUID:    "vol-1",
		BackupVaultID: 10,
		Attributes:    &datamodel.BackupAttributes{AccountIdentifier: "project-1"},
	}

	mockSE.On("GetBackupVaultById", ctx, int64(10)).Return(nil, errLedger)

	activity := &BillingActivity{SE: mockSE}
	assert.ErrorIs(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, backup), errLedger)
}

func TestCreateCrossRegionBackupTransferBillingLedger_CreateFails(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)

	backup := &datamodel.Backup{
		VolumeUUID:    "vol-1",
		BackupVaultID: 10,
		Attributes:    &datamodel.BackupAttributes{AccountIdentifier: "project-1"},
	}
	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{ID: 10},
		BackupVaultType: CrossRegionBackupType,
	}

	mockSE.On("GetBackupVaultById", ctx, int64(10)).Return(backupVault, nil)
	mockSE.On("CreateBillingLedger", ctx, mock.Anything).Return(nil, errLedger)

	activity := &BillingActivity{SE: mockSE}
	assert.ErrorIs(t, activity.CreateCrossRegionBackupTransferBillingLedger(ctx, backup), errLedger)
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

func TestExtractConsumerID(t *testing.T) {
	assert.Equal(t, "account-name", extractConsumerID(&datamodel.Account{Name: "account-name"}, "vendor-id"))
	assert.Equal(t, "vendor-id", extractConsumerID(nil, "vendor-id"))
	assert.Equal(t, "vendor-id", extractConsumerID(&datamodel.Account{}, "vendor-id"))
}

func TestGetPoolResourceType(t *testing.T) {
	assert.Equal(t, metadata.VolumePool, getPoolResourceType(&datamodel.Pool{}))
	assert.Equal(t, metadata.VolumePool, getPoolResourceType(&datamodel.Pool{PoolAttributes: &datamodel.PoolAttributes{}}))
	assert.Equal(t, metadata.VolumePoolRegionalHA,
		getPoolResourceType(&datamodel.Pool{PoolAttributes: &datamodel.PoolAttributes{IsRegionalHA: true}}))
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

func TestGetVolumePoolFields(t *testing.T) {
	assert.Equal(t, "", getVolumeVendorID(&datamodel.Volume{}))
	assert.Equal(t, "", getVolumeDeploymentName(&datamodel.Volume{}))

	volume := &datamodel.Volume{Pool: &datamodel.Pool{VendorID: "vendor-1", DeploymentName: "deployment-1"}}
	assert.Equal(t, "vendor-1", getVolumeVendorID(volume))
	assert.Equal(t, "deployment-1", getVolumeDeploymentName(volume))
}

func TestCreatePoolBillingLedger(t *testing.T) {
	ctx := context.Background()

	t.Run("CreatesAllocatedSizeOnlyForNonATPool", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolAllocatedSize && l.Quantity == 500
		})).Return(&datamodel.BillingLedger{}, nil).Once()

		activity := &BillingActivity{SE: mockSE}
		require.NoError(tt, activity.CreatePoolBillingLedger(ctx, &datamodel.Pool{
			Name:        "pool-1",
			SizeInBytes: 500,
			VendorID:    "vendor-1",
		}))
	})

	t.Run("CreatesHotTierEntryForATPool", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolAllocatedSize
		})).Return(&datamodel.BillingLedger{}, nil).Once()
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolHotTierProvisionedSize && l.Quantity == 250
		})).Return(&datamodel.BillingLedger{}, nil).Once()

		activity := &BillingActivity{SE: mockSE}
		require.NoError(tt, activity.CreatePoolBillingLedger(ctx, &datamodel.Pool{
			Name:              "pool-1",
			SizeInBytes:       500,
			AllowAutoTiering:  true,
			AutoTieringConfig: &datamodel.AutoTieringConfig{HotTierSizeInBytes: 250},
		}))
	})

	t.Run("PropagatesAllocatedSizeError", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.Anything).Return(nil, errLedger).Once()

		activity := &BillingActivity{SE: mockSE}
		assert.ErrorIs(tt, activity.CreatePoolBillingLedger(ctx, &datamodel.Pool{Name: "pool-1"}), errLedger)
	})

	t.Run("PropagatesHotTierError", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolAllocatedSize
		})).Return(&datamodel.BillingLedger{}, nil).Once()
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.MeasuredType == metadata.PoolHotTierProvisionedSize
		})).Return(nil, errLedger).Once()

		activity := &BillingActivity{SE: mockSE}
		assert.ErrorIs(tt, activity.CreatePoolBillingLedger(ctx, &datamodel.Pool{
			Name:              "pool-1",
			AllowAutoTiering:  true,
			AutoTieringConfig: &datamodel.AutoTieringConfig{HotTierSizeInBytes: 250},
		}), errLedger)
	})
}

func TestCreatePoolHotTierBillingLedger_SkipsNonATPools(t *testing.T) {
	ctx := context.Background()
	// No expectations: nothing should be written for non-AT pools.
	mockSE := database.NewMockStorage(t)
	activity := &BillingActivity{SE: mockSE}

	assert.NoError(t, activity.CreatePoolHotTierBillingLedger(ctx, &datamodel.Pool{AllowAutoTiering: false}))
	assert.NoError(t, activity.CreatePoolHotTierBillingLedger(ctx, &datamodel.Pool{AllowAutoTiering: true}))
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
				l.ResourceType == metadata.Volume &&
				l.MeasuredType == metadata.BackupEnabledVolumeAllocatedSize &&
				l.Quantity == 2048 &&
				l.ConsumerID == "vendor-1" &&
				l.DeploymentName == "deployment-1"
		})).Return(&datamodel.BillingLedger{}, nil)

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreateVolumeBillingLedger(ctx, &datamodel.Volume{
			BaseModel:      datamodel.BaseModel{UUID: "vol-uuid-1"},
			Name:           "vol-1",
			SizeInBytes:    2048,
			Pool:           &datamodel.Pool{VendorID: "vendor-1", DeploymentName: "deployment-1"},
			DataProtection: &datamodel.DataProtection{BackupChainBytes: backupChainBytes(10)},
		}))
	})

	t.Run("UsesRegionalHAResourceType", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("CreateBillingLedger", ctx, mock.MatchedBy(func(l *datamodel.BillingLedger) bool {
			return l.ResourceType == metadata.VolumeRegionalHA
		})).Return(&datamodel.BillingLedger{}, nil)

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreateVolumeBillingLedger(ctx, &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "vol-uuid-1"},
			VolumeAttributes: &datamodel.VolumeAttributes{IsRegionalHA: true},
			DataProtection:   &datamodel.DataProtection{BackupChainBytes: backupChainBytes(10)},
		}))
	})

	t.Run("PropagatesVaultLookupError", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("GetBackupVault", ctx, "vault-uuid-1").Return(nil, errLedger)

		activity := &BillingActivity{SE: mockSE}
		assert.ErrorIs(tt, activity.CreateVolumeBillingLedger(ctx, &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-1"},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: backupChainBytes(10),
				BackupVaultID:    "vault-uuid-1",
			},
		}), errLedger)
	})

	t.Run("CreatesLedgerWhenVaultResolves", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockSE.On("GetBackupVault", ctx, "vault-uuid-1").Return(&datamodel.BackupVault{}, nil)
		mockSE.On("CreateBillingLedger", ctx, mock.Anything).Return(&datamodel.BillingLedger{}, nil)

		activity := &BillingActivity{SE: mockSE}
		assert.NoError(tt, activity.CreateVolumeBillingLedger(ctx, &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-1"},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: backupChainBytes(10),
				BackupVaultID:    "vault-uuid-1",
			},
		}))
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
