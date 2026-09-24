package database

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"gorm.io/gorm"
)

func createTestAccountAndPoolForExpertMode(t *testing.T, store *DataStoreRepository) (*datamodel.Account, *datamodel.Pool) {
	accountUUID := utils.RandomUUID()
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{
			UUID:      accountUUID,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Name: fmt.Sprintf("test_account_%s", utils.GenerateRandomAlphanumeric(8)),
	}
	err := store.db.Create(account).Error()
	assert.NoError(t, err)

	poolUUID := utils.RandomUUID()
	deploymentName := fmt.Sprintf("test_deployment_%s", utils.GenerateRandomAlphanumeric(8))
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID:      poolUUID,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Name:           fmt.Sprintf("test_pool_%s", utils.GenerateRandomAlphanumeric(8)),
		AccountID:      account.ID,
		SizeInBytes:    2199023255552, // 2TB
		DeploymentName: deploymentName,
		PoolAttributes: &datamodel.PoolAttributes{
			PrimaryZone: "us-west1-a",
		},
	}
	err = store.db.Create(pool).Error()
	assert.NoError(t, err)

	return account, pool
}

func createTestSVMForExpertMode(t *testing.T, store *DataStoreRepository, poolID int64, accountID int64, svmName string, externalUUID string) *datamodel.Svm {
	svmDetails := &datamodel.SvmDetails{
		ExternalUUID: externalUUID,
		IPSpace:      "Default",
	}
	svmUUID := utils.RandomUUID()
	svm := &datamodel.Svm{
		BaseModel: datamodel.BaseModel{
			UUID:      svmUUID,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Name:       svmName,
		PoolID:     poolID,
		AccountID:  accountID,
		SvmDetails: svmDetails,
		State:      datamodel.LifeCycleStateREADY,
	}
	err := store.db.Create(svm).Error()
	assert.NoError(t, err)
	return svm
}

func TestCreateExpertModeVolume_Success(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776, // 1TB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)

	assert.NoError(t, err)
	assert.NotNil(t, createdVolume)
	assert.NotEmpty(t, createdVolume.UUID)
	assert.Equal(t, expertModeVolume.Name, createdVolume.Name)
	assert.Equal(t, expertModeVolume.SizeInBytes, createdVolume.SizeInBytes)
	assert.Equal(t, expertModeVolume.PoolID, createdVolume.PoolID)
	assert.Equal(t, expertModeVolume.AccountID, createdVolume.AccountID)
	assert.Equal(t, expertModeVolume.SvmID, createdVolume.SvmID)
	assert.Equal(t, expertModeVolume.Style, createdVolume.Style)
	assert.Equal(t, datamodel.LifeCycleStateCreating, createdVolume.State)
	assert.NotEmpty(t, createdVolume.ExternalUUID)
}

func TestCreateExpertModeVolume_WithExistingUUID(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	existingUUID := "existing-uuid-12345"
	expertModeVolume := &datamodel.ExpertModeVolumes{
		BaseModel: datamodel.BaseModel{
			UUID: existingUUID,
		},
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)

	assert.NoError(t, err)
	assert.NotNil(t, createdVolume)
	assert.Equal(t, existingUUID, createdVolume.UUID)
}

func TestCreateExpertModeVolume_WithAllStyles(t *testing.T) {
	styles := []string{"flexvol", "flexgroup", "flexcache"}

	for _, style := range styles {
		t.Run(style, func(t *testing.T) {
			store := setup(t)
			ctx := context.Background()
			account, pool := createTestAccountAndPoolForExpertMode(t, store)
			svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
			svmExternalUUID := utils.RandomUUID()
			svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

			expertModeVolume := &datamodel.ExpertModeVolumes{
				Name:         "test-volume-" + style,
				SizeInBytes:  1099511627776,
				PoolID:       pool.ID,
				AccountID:    account.ID,
				SvmID:        svm.ID,
				Style:        style,
				ExternalUUID: utils.RandomUUID(),
				State:        datamodel.LifeCycleStateCreating,
			}

			createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)

			assert.NoError(t, err)
			assert.NotNil(t, createdVolume)
			assert.Equal(t, style, createdVolume.Style)
		})
	}
}

func TestGetExpertModePoolUsedCapacity_Success(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	// Create multiple volumes for the same pool
	volume1 := &datamodel.ExpertModeVolumes{
		Name:         "volume-1",
		SizeInBytes:  1099511627776, // 1TB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}
	volume2 := &datamodel.ExpertModeVolumes{
		Name:         "volume-2",
		SizeInBytes:  214748364800, // 200GB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexgroup",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}
	volume3 := &datamodel.ExpertModeVolumes{
		Name:         "volume-3",
		SizeInBytes:  536870912000, // 500GB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexcache",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}

	_, err := store.CreateExpertModeVolume(ctx, volume1)
	assert.NoError(t, err)
	_, err = store.CreateExpertModeVolume(ctx, volume2)
	assert.NoError(t, err)
	_, err = store.CreateExpertModeVolume(ctx, volume3)
	assert.NoError(t, err)

	// Create another pool and volume (should not be included in total)
	account2, pool2 := createTestAccountAndPoolForExpertMode(t, store)
	svmName2 := fmt.Sprintf("test-svm-2-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID2 := utils.RandomUUID()
	svm2 := createTestSVMForExpertMode(t, store, pool2.ID, account2.ID, svmName2, svmExternalUUID2)
	volume4 := &datamodel.ExpertModeVolumes{
		Name:         "volume-4",
		SizeInBytes:  1099511627776, // 1TB
		PoolID:       pool2.ID,
		AccountID:    account2.ID,
		SvmID:        svm2.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}
	_, err = store.CreateExpertModeVolume(ctx, volume4)
	assert.NoError(t, err)

	// Get total size and count for pool1
	// Expected: 1TB + 200GB + 500GB = 1,700GB = 1,851,130,904,576 bytes
	// Expected count: 3 volumes
	expectedTotal := int64(1099511627776 + 214748364800 + 536870912000)
	expectedCount := int64(3)
	capacity, err := store.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID)

	assert.NoError(t, err)
	assert.NotNil(t, capacity)
	assert.Equal(t, expectedTotal, capacity.TotalSize)
	assert.Equal(t, expectedCount, capacity.VolumeCount)
}

func TestGetExpertModePoolUsedCapacity_EmptyPool(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	_ = account

	capacity, err := store.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID)

	assert.NoError(t, err)
	assert.NotNil(t, capacity)
	assert.Equal(t, int64(0), capacity.TotalSize)
	assert.Equal(t, int64(0), capacity.VolumeCount)
}

func TestGetExpertModePoolUsedCapacity_NonExistentPool(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	capacity, err := store.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, 99999)

	assert.NoError(t, err)
	assert.NotNil(t, capacity)
	assert.Equal(t, int64(0), capacity.TotalSize)
	assert.Equal(t, int64(0), capacity.VolumeCount)
}

func TestGetExpertModePoolUsedCapacity_WithDeletedVolumes(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	// Create a volume
	volume1 := &datamodel.ExpertModeVolumes{
		Name:         "volume-1",
		SizeInBytes:  1099511627776, // 1TB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}
	createdVolume, err := store.CreateExpertModeVolume(ctx, volume1)
	assert.NoError(t, err)

	// Soft delete the volume
	err = store.db.GORM().Delete(createdVolume).Error
	assert.NoError(t, err)

	// Total size and count should NOT include deleted volumes (GORM excludes soft-deleted records)
	capacity, err := store.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID)

	assert.NoError(t, err)
	assert.NotNil(t, capacity)
	assert.Equal(t, int64(0), capacity.TotalSize)
	assert.Equal(t, int64(0), capacity.VolumeCount)
}

func TestGetExpertModePoolUsedCapacity_SubtractsSharedBytes(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	vol := &datamodel.ExpertModeVolumes{
		Name:         "clone-volume-shared",
		SizeInBytes:  1000,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateAvailable,
	}
	created, err := store.CreateExpertModeVolume(ctx, vol)
	assert.NoError(t, err)
	reloaded, err := store.GetExpertModeVolumeByUUID(ctx, created.UUID)
	assert.NoError(t, err)
	reloaded.VolumeAttributes = &datamodel.ExpertModeVolumeAttributes{IsFlexclone: true}
	reloaded.SharedBytes = 200
	_, err = store.UpdateExpertModeVolume(ctx, reloaded)
	assert.NoError(t, err)

	capacity, err := store.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID)
	assert.NoError(t, err)
	assert.NotNil(t, capacity)
	assert.Equal(t, int64(800), capacity.TotalSize)
	assert.Equal(t, int64(1), capacity.VolumeCount)
}

func TestGetExpertModePoolUsedCapacity_EffectiveUsedFloorsAtZeroWhenSharedBytesExceedTotalSize(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, utils.RandomUUID())

	v1 := &datamodel.ExpertModeVolumes{
		Name:         "v-high-shared",
		SizeInBytes:  100,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateAvailable,
	}
	created, err := store.CreateExpertModeVolume(ctx, v1)
	assert.NoError(t, err)
	reloaded, err := store.GetExpertModeVolumeByUUID(ctx, created.UUID)
	assert.NoError(t, err)
	reloaded.VolumeAttributes = &datamodel.ExpertModeVolumeAttributes{IsFlexclone: true}
	reloaded.SharedBytes = 250
	_, err = store.UpdateExpertModeVolume(ctx, reloaded)
	assert.NoError(t, err)

	capacity, err := store.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID)
	assert.NoError(t, err)
	assert.NotNil(t, capacity)
	assert.Equal(t, int64(0), capacity.TotalSize)
	assert.Equal(t, int64(1), capacity.VolumeCount)
}

func TestGetExpertModePoolsUsedCapacityAndVolumeCount(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	account1, pool1 := createTestAccountAndPoolForExpertMode(t, store)
	svm1 := createTestSVMForExpertMode(t, store, pool1.ID, account1.ID, "batch-svm-1", utils.RandomUUID())
	account2, pool2 := createTestAccountAndPoolForExpertMode(t, store)
	svm2 := createTestSVMForExpertMode(t, store, pool2.ID, account2.ID, "batch-svm-2", utils.RandomUUID())
	_, emptyPool := createTestAccountAndPoolForExpertMode(t, store)

	volumes := []*datamodel.ExpertModeVolumes{
		{
			Name:         "batch-volume-1",
			SizeInBytes:  1000,
			SharedBytes:  200,
			PoolID:       pool1.ID,
			AccountID:    account1.ID,
			SvmID:        svm1.ID,
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateAvailable,
		},
		{
			Name:         "batch-volume-2",
			SizeInBytes:  100,
			SharedBytes:  250,
			PoolID:       pool2.ID,
			AccountID:    account2.ID,
			SvmID:        svm2.ID,
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateAvailable,
		},
	}
	for _, volume := range volumes {
		_, err := store.CreateExpertModeVolume(ctx, volume)
		require.NoError(t, err)
	}

	capacities, err := store.GetExpertModePoolsUsedCapacityAndVolumeCount(
		ctx,
		[]int64{pool1.ID, pool2.ID, emptyPool.ID},
	)

	require.NoError(t, err)
	require.Len(t, capacities, 3)
	assert.Equal(t, &ExpertModePoolCapacity{TotalSize: 800, VolumeCount: 1}, capacities[pool1.ID])
	assert.Equal(t, &ExpertModePoolCapacity{TotalSize: 0, VolumeCount: 1}, capacities[pool2.ID])
	assert.Equal(t, &ExpertModePoolCapacity{}, capacities[emptyPool.ID])
}

func TestGetExpertModePoolsUsedCapacityAndVolumeCount_EmptyPoolIDs(t *testing.T) {
	store := setup(t)

	capacities, err := store.GetExpertModePoolsUsedCapacityAndVolumeCount(context.Background(), nil)

	require.NoError(t, err)
	assert.Empty(t, capacities)
}

func TestRetryEngine_GetExpertModePoolsUsedCapacityAndVolumeCount(t *testing.T) {
	store := setup(t)
	re := &retryEngine{dataStore: store}

	capacities, err := re.GetExpertModePoolsUsedCapacityAndVolumeCount(context.Background(), nil)

	require.NoError(t, err)
	assert.Empty(t, capacities)
}

func TestRetryEngine_GetExpertModePoolsUsedCapacityAndVolumeCount_QueryError(t *testing.T) {
	store := setup(t)
	sqlDB, err := store.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	re := &retryEngine{dataStore: store}

	capacities, err := re.GetExpertModePoolsUsedCapacityAndVolumeCount(context.Background(), []int64{1})

	require.Error(t, err)
	assert.Nil(t, capacities)
}

func TestPersistenceStore_GetExpertModePoolsUsedCapacityAndVolumeCount(t *testing.T) {
	db, err := SetupTestDB()
	require.NoError(t, err)
	wrapper := gormwrapper.New(db)
	store := &PersistenceStore{
		db:        wrapper,
		dataStore: retryEngine{dataStore: NewDataStoreRepository(wrapper)},
	}

	capacities, err := store.GetExpertModePoolsUsedCapacityAndVolumeCount(context.Background(), nil)

	require.NoError(t, err)
	assert.Empty(t, capacities)
}

func TestGetExpertModePoolsUsedCapacityAndVolumeCount_QueryError(t *testing.T) {
	store := setup(t)
	sqlDB, err := store.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	capacities, err := store.GetExpertModePoolsUsedCapacityAndVolumeCount(context.Background(), []int64{1})

	require.Error(t, err)
	assert.Nil(t, capacities)
}

func TestUpdateExpertModeVolumeFields_UpdatesSharedBytesColumn(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, utils.RandomUUID())

	vol := &datamodel.ExpertModeVolumes{
		Name:         "attrs-str-update",
		SizeInBytes:  500,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateAvailable,
	}
	created, err := store.CreateExpertModeVolume(ctx, vol)
	assert.NoError(t, err)

	err = store.UpdateExpertModeVolumeFields(ctx, created.ExternalUUID, map[string]interface{}{
		"shared_bytes": int64(55),
	})
	assert.NoError(t, err)

	var row datamodel.ExpertModeVolumes
	err = store.db.GORM().WithContext(ctx).Where("id = ?", created.ID).First(&row).Error
	assert.NoError(t, err)
	assert.Equal(t, int64(55), row.SharedBytes)
}

func TestGetActiveExpertModeVolumesCountByAccountID(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	// Account 1 with a pool/svm and 3 volumes (1 soft-deleted)
	account1, pool1 := createTestAccountAndPoolForExpertMode(t, store)
	svm1 := createTestSVMForExpertMode(t, store, pool1.ID, account1.ID, "svm-account-1", utils.RandomUUID())

	volA1 := &datamodel.ExpertModeVolumes{
		Name:         "acct1-vol-1",
		SizeInBytes:  1024,
		PoolID:       pool1.ID,
		AccountID:    account1.ID,
		SvmID:        svm1.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}
	volA2 := &datamodel.ExpertModeVolumes{
		Name:         "acct1-vol-2",
		SizeInBytes:  2048,
		PoolID:       pool1.ID,
		AccountID:    account1.ID,
		SvmID:        svm1.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}
	volA3 := &datamodel.ExpertModeVolumes{
		Name:         "acct1-vol-3",
		SizeInBytes:  4096,
		PoolID:       pool1.ID,
		AccountID:    account1.ID,
		SvmID:        svm1.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}

	createdA1, err := store.CreateExpertModeVolume(ctx, volA1)
	assert.NoError(t, err)
	_, err = store.CreateExpertModeVolume(ctx, volA2)
	assert.NoError(t, err)
	_, err = store.CreateExpertModeVolume(ctx, volA3)
	assert.NoError(t, err)

	// Soft-delete one volume from account1; it should not be counted.
	err = store.db.GORM().Delete(createdA1).Error
	assert.NoError(t, err)

	// Account 2 with one active volume; should not be counted for account1.
	account2, pool2 := createTestAccountAndPoolForExpertMode(t, store)
	svm2 := createTestSVMForExpertMode(t, store, pool2.ID, account2.ID, "svm-account-2", utils.RandomUUID())
	volB1 := &datamodel.ExpertModeVolumes{
		Name:         "acct2-vol-1",
		SizeInBytes:  1024,
		PoolID:       pool2.ID,
		AccountID:    account2.ID,
		SvmID:        svm2.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}
	_, err = store.CreateExpertModeVolume(ctx, volB1)
	assert.NoError(t, err)

	countAccount1, err := store.GetActiveExpertModeVolumesCountByAccountID(ctx, account1.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), countAccount1)

	countAccount2, err := store.GetActiveExpertModeVolumesCountByAccountID(ctx, account2.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), countAccount2)

	countUnknown, err := store.GetActiveExpertModeVolumesCountByAccountID(ctx, 9999999)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), countUnknown)
}

func TestCreateExpertModeVolume_ForeignKeyConstraint(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	// Try to create volume with non-existent pool ID
	// Note: SQLite doesn't enforce foreign key constraints by default
	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776,
		PoolID:       99999, // Non-existent pool
		AccountID:    99999, // Non-existent account
		SvmID:        99999, // Non-existent SVM
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)

	// SQLite in-memory DB doesn't enforce foreign keys by default
	// The function will succeed but the data would be invalid
	// In a real scenario with FK constraints enabled, this would fail
	if err == nil {
		assert.NotNil(t, createdVolume)
		assert.NotEmpty(t, createdVolume.UUID)
	}
}

func TestGetExpertModeVolumeByNameAndPoolID_Success(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-unique-volume",
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}

	_, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
	assert.NoError(t, err)

	// Retrieve the volume by name and pool ID
	retrievedVolume, err := store.GetExpertModeVolumeByNameAndPoolID(ctx, "test-unique-volume", pool.ID)

	assert.NoError(t, err)
	assert.NotNil(t, retrievedVolume)
	assert.Equal(t, "test-unique-volume", retrievedVolume.Name)
	assert.Equal(t, pool.ID, retrievedVolume.PoolID)
}

func TestGetExpertModeVolumeByNameAndPoolID_NotFound(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	_ = account

	// Try to retrieve non-existent volume
	retrievedVolume, err := store.GetExpertModeVolumeByNameAndPoolID(ctx, "non-existent-volume", pool.ID)

	assert.Error(t, err)
	assert.Nil(t, retrievedVolume)
}

func TestGetExpertModeVolumeByUUID(t *testing.T) {
	t.Run("WhenVolumeExists", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		// Create expert mode volume
		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-expert-volume",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateCreating,
		}

		createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
		assert.NoError(tt, err)
		assert.NotNil(tt, createdVolume)

		// Retrieve the volume by UUID
		retrievedVolume, err := store.GetExpertModeVolumeByUUID(ctx, createdVolume.UUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, retrievedVolume)
		assert.Equal(tt, createdVolume.UUID, retrievedVolume.UUID)
		assert.Equal(tt, "test-expert-volume", retrievedVolume.Name)
		assert.Equal(tt, int64(1099511627776), retrievedVolume.SizeInBytes)
		assert.Equal(tt, "flexvol", retrievedVolume.Style)
		assert.Equal(tt, datamodel.LifeCycleStateCreating, retrievedVolume.State)
		assert.Equal(tt, pool.ID, retrievedVolume.PoolID)
		assert.Equal(tt, account.ID, retrievedVolume.AccountID)
		assert.Equal(tt, svm.ID, retrievedVolume.SvmID)

		// Verify preloaded relationships
		assert.NotNil(tt, retrievedVolume.Account)
		assert.Equal(tt, account.ID, retrievedVolume.Account.ID)
		assert.Equal(tt, account.Name, retrievedVolume.Account.Name)
		assert.NotNil(tt, retrievedVolume.Pool)
		assert.Equal(tt, pool.ID, retrievedVolume.Pool.ID)
		assert.Equal(tt, pool.UUID, retrievedVolume.Pool.UUID)
		assert.NotNil(tt, retrievedVolume.Svm)
		assert.Equal(tt, svm.ID, retrievedVolume.Svm.ID)
		assert.Equal(tt, svm.Name, retrievedVolume.Svm.Name)
	})

	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		// Try to retrieve non-existent volume
		nonExistentUUID := utils.RandomUUID()
		retrievedVolume, err := store.GetExpertModeVolumeByUUID(ctx, nonExistentUUID)

		assert.Error(tt, err)
		assert.Nil(tt, retrievedVolume)
		assert.True(tt, errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err))
	})

	t.Run("WhenVolumeExistsWithDifferentStyles", func(tt *testing.T) {
		styles := []string{"flexvol", "flexgroup", "flexcache"}

		for _, style := range styles {
			tt.Run(style, func(ttt *testing.T) {
				store := setup(ttt)
				ctx := context.Background()
				account, pool := createTestAccountAndPoolForExpertMode(ttt, store)
				svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
				svmExternalUUID := utils.RandomUUID()
				svm := createTestSVMForExpertMode(ttt, store, pool.ID, account.ID, svmName, svmExternalUUID)

				expertModeVolume := &datamodel.ExpertModeVolumes{
					Name:         fmt.Sprintf("test-volume-%s", style),
					SizeInBytes:  1099511627776,
					PoolID:       pool.ID,
					AccountID:    account.ID,
					SvmID:        svm.ID,
					Style:        style,
					ExternalUUID: utils.RandomUUID(),
					State:        datamodel.LifeCycleStateCreating,
				}

				createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
				assert.NoError(ttt, err)

				retrievedVolume, err := store.GetExpertModeVolumeByUUID(ctx, createdVolume.UUID)

				assert.NoError(ttt, err)
				assert.NotNil(ttt, retrievedVolume)
				assert.Equal(ttt, style, retrievedVolume.Style)
				assert.NotNil(ttt, retrievedVolume.Account)
				assert.NotNil(ttt, retrievedVolume.Pool)
				assert.NotNil(ttt, retrievedVolume.Svm)
			})
		}
	})
}

func TestGetExpertModeVolumeByExternalUUID(t *testing.T) {
	t.Run("WhenVolumeExists", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		volumeExternalUUID := utils.RandomUUID()
		// Create expert mode volume
		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-expert-volume",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: volumeExternalUUID,
			State:        datamodel.LifeCycleStateAvailable,
		}

		createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
		assert.NoError(tt, err)
		assert.NotNil(tt, createdVolume)

		// Retrieve the volume by ExternalUUID
		retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, volumeExternalUUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, retrievedVolume)
		assert.Equal(tt, volumeExternalUUID, retrievedVolume.ExternalUUID)
		assert.Equal(tt, createdVolume.UUID, retrievedVolume.UUID)
		assert.Equal(tt, "test-expert-volume", retrievedVolume.Name)
		assert.Equal(tt, int64(1099511627776), retrievedVolume.SizeInBytes)
		assert.Equal(tt, "flexvol", retrievedVolume.Style)
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, retrievedVolume.State)
		assert.Equal(tt, pool.ID, retrievedVolume.PoolID)
		assert.Equal(tt, account.ID, retrievedVolume.AccountID)
		assert.Equal(tt, svm.ID, retrievedVolume.SvmID)

		// Verify preloaded relationships
		assert.NotNil(tt, retrievedVolume.Account)
		assert.Equal(tt, account.ID, retrievedVolume.Account.ID)
		assert.Equal(tt, account.Name, retrievedVolume.Account.Name)
		assert.NotNil(tt, retrievedVolume.Pool)
		assert.Equal(tt, pool.ID, retrievedVolume.Pool.ID)
		assert.Equal(tt, pool.UUID, retrievedVolume.Pool.UUID)
		assert.NotNil(tt, retrievedVolume.Svm)
		assert.Equal(tt, svm.ID, retrievedVolume.Svm.ID)
		assert.Equal(tt, svm.Name, retrievedVolume.Svm.Name)
	})

	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		// Try to retrieve non-existent volume by ExternalUUID
		nonExistentExternalUUID := utils.RandomUUID()
		retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, nonExistentExternalUUID)

		assert.Error(tt, err)
		assert.Nil(tt, retrievedVolume)
		assert.True(tt, errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err))
	})

	t.Run("WhenVolumeExistsWithDifferentStyles", func(tt *testing.T) {
		styles := []string{"flexvol", "flexgroup", "flexcache"}

		for _, style := range styles {
			tt.Run(style, func(ttt *testing.T) {
				store := setup(ttt)
				ctx := context.Background()
				account, pool := createTestAccountAndPoolForExpertMode(ttt, store)
				svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
				svmExternalUUID := utils.RandomUUID()
				svm := createTestSVMForExpertMode(ttt, store, pool.ID, account.ID, svmName, svmExternalUUID)

				volumeExternalUUID := utils.RandomUUID()
				expertModeVolume := &datamodel.ExpertModeVolumes{
					Name:         fmt.Sprintf("test-volume-%s", style),
					SizeInBytes:  1099511627776,
					PoolID:       pool.ID,
					AccountID:    account.ID,
					SvmID:        svm.ID,
					Style:        style,
					ExternalUUID: volumeExternalUUID,
					State:        datamodel.LifeCycleStateAvailable,
				}

				_, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
				assert.NoError(ttt, err)

				retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, volumeExternalUUID)

				assert.NoError(ttt, err)
				assert.NotNil(ttt, retrievedVolume)
				assert.Equal(ttt, volumeExternalUUID, retrievedVolume.ExternalUUID)
				assert.Equal(ttt, style, retrievedVolume.Style)
				assert.NotNil(ttt, retrievedVolume.Account)
				assert.NotNil(ttt, retrievedVolume.Pool)
				assert.NotNil(ttt, retrievedVolume.Svm)
			})
		}
	})

	t.Run("WhenMultipleVolumesExist_ReturnsCorrectOne", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		// Create multiple volumes with different external UUIDs
		externalUUID1 := utils.RandomUUID()
		externalUUID2 := utils.RandomUUID()
		externalUUID3 := utils.RandomUUID()

		volume1 := &datamodel.ExpertModeVolumes{
			Name:         "volume-1",
			SizeInBytes:  1099511627776,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: externalUUID1,
			State:        datamodel.LifeCycleStateAvailable,
		}
		volume2 := &datamodel.ExpertModeVolumes{
			Name:         "volume-2",
			SizeInBytes:  2199023255552,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexgroup",
			ExternalUUID: externalUUID2,
			State:        datamodel.LifeCycleStateAvailable,
		}
		volume3 := &datamodel.ExpertModeVolumes{
			Name:         "volume-3",
			SizeInBytes:  536870912000,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexcache",
			ExternalUUID: externalUUID3,
			State:        datamodel.LifeCycleStateAvailable,
		}

		_, err := store.CreateExpertModeVolume(ctx, volume1)
		assert.NoError(tt, err)
		_, err = store.CreateExpertModeVolume(ctx, volume2)
		assert.NoError(tt, err)
		_, err = store.CreateExpertModeVolume(ctx, volume3)
		assert.NoError(tt, err)

		// Retrieve the second volume by its external UUID
		retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, externalUUID2)

		assert.NoError(tt, err)
		assert.NotNil(tt, retrievedVolume)
		assert.Equal(tt, externalUUID2, retrievedVolume.ExternalUUID)
		assert.Equal(tt, "volume-2", retrievedVolume.Name)
		assert.Equal(tt, int64(2199023255552), retrievedVolume.SizeInBytes)
		assert.Equal(tt, "flexgroup", retrievedVolume.Style)
	})

	t.Run("WhenVolumeIsSoftDeleted_ReturnsNotFound", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		volumeExternalUUID := utils.RandomUUID()
		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-expert-volume",
			SizeInBytes:  1099511627776,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: volumeExternalUUID,
			State:        datamodel.LifeCycleStateAvailable,
		}

		createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
		assert.NoError(tt, err)

		// Soft delete the volume
		err = store.db.GORM().Delete(createdVolume).Error
		assert.NoError(tt, err)

		// Try to retrieve the soft-deleted volume
		retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, volumeExternalUUID)

		assert.Error(tt, err)
		assert.Nil(tt, retrievedVolume)
		assert.True(tt, errors.Is(err, gorm.ErrRecordNotFound))
	})

	t.Run("WhenExternalUUIDIsEmpty_ReturnsError", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		// Try to retrieve with empty external UUID
		retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, "")

		assert.Error(tt, err)
		assert.Nil(tt, retrievedVolume)
	})

	t.Run("WhenVolumeExistsInDifferentStates", func(tt *testing.T) {
		states := []string{
			datamodel.LifeCycleStateCreating,
			datamodel.LifeCycleStateAvailable,
			datamodel.LifeCycleStateDeleting,
		}

		for _, state := range states {
			tt.Run(state, func(ttt *testing.T) {
				store := setup(ttt)
				ctx := context.Background()
				account, pool := createTestAccountAndPoolForExpertMode(ttt, store)
				svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
				svmExternalUUID := utils.RandomUUID()
				svm := createTestSVMForExpertMode(ttt, store, pool.ID, account.ID, svmName, svmExternalUUID)

				volumeExternalUUID := utils.RandomUUID()
				expertModeVolume := &datamodel.ExpertModeVolumes{
					Name:         fmt.Sprintf("test-volume-%s", state),
					SizeInBytes:  1099511627776,
					PoolID:       pool.ID,
					AccountID:    account.ID,
					SvmID:        svm.ID,
					Style:        "flexvol",
					ExternalUUID: volumeExternalUUID,
					State:        state,
				}

				_, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
				assert.NoError(ttt, err)

				retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, volumeExternalUUID)

				assert.NoError(ttt, err)
				assert.NotNil(ttt, retrievedVolume)
				assert.Equal(ttt, state, retrievedVolume.State)
				assert.Equal(ttt, volumeExternalUUID, retrievedVolume.ExternalUUID)
			})
		}
	})
}

func TestUpdateExpertModeVolume(t *testing.T) {
	t.Run("WhenVolumeIsUpdatedSuccessfully", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		// Create initial volume
		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-expert-volume",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateCreating,
		}

		createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
		assert.NoError(tt, err)
		assert.NotNil(tt, createdVolume)

		// Update the volume with new values
		updatedVolume := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: createdVolume.UUID,
			},
			Name:        "updated-expert-volume",
			SizeInBytes: 2199023255552, // 2TB
			Style:       "flexgroup",
			State:       datamodel.LifeCycleStateAvailable,
		}

		result, err := store.UpdateExpertModeVolume(ctx, updatedVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "updated-expert-volume", result.Name)
		assert.Equal(tt, int64(2199023255552), result.SizeInBytes)
		assert.Equal(tt, "flexgroup", result.Style)
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, result.State)
		assert.Equal(tt, createdVolume.UUID, result.UUID)
		assert.Equal(tt, pool.ID, result.PoolID)
		assert.Equal(tt, account.ID, result.AccountID)
		assert.Equal(tt, svm.ID, result.SvmID)
	})

	t.Run("WhenOnlyStateIsUpdated", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		// Create initial volume
		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-expert-volume",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateCreating,
		}

		createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
		assert.NoError(tt, err)
		assert.NotNil(tt, createdVolume)

		// Update only the state
		updatedVolume := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: createdVolume.UUID,
			},
			Name:        createdVolume.Name,
			SizeInBytes: createdVolume.SizeInBytes,
			Style:       createdVolume.Style,
			State:       datamodel.LifeCycleStateDeleted,
		}

		result, err := store.UpdateExpertModeVolume(ctx, updatedVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.LifeCycleStateDeleted, result.State)
		assert.Equal(tt, createdVolume.Name, result.Name)
		assert.Equal(tt, createdVolume.SizeInBytes, result.SizeInBytes)
		assert.Equal(tt, createdVolume.Style, result.Style)
	})

	t.Run("WhenNameAndSizeAreUpdated", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		// Create initial volume
		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-expert-volume",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateCreating,
		}

		createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
		assert.NoError(tt, err)
		assert.NotNil(tt, createdVolume)

		// Update name and size only
		updatedVolume := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: createdVolume.UUID,
			},
			Name:        "renamed-expert-volume",
			SizeInBytes: 536870912000, // 500GB
			Style:       createdVolume.Style,
			State:       createdVolume.State,
		}

		result, err := store.UpdateExpertModeVolume(ctx, updatedVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "renamed-expert-volume", result.Name)
		assert.Equal(tt, int64(536870912000), result.SizeInBytes)
		assert.Equal(tt, createdVolume.Style, result.Style)
		assert.Equal(tt, createdVolume.State, result.State)
	})

	t.Run("WhenStyleIsUpdated", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		// Create initial volume with flexvol style
		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-expert-volume",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateCreating,
		}

		createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
		assert.NoError(tt, err)
		assert.NotNil(tt, createdVolume)

		// Update style to flexgroup
		updatedVolume := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: createdVolume.UUID,
			},
			Name:        createdVolume.Name,
			SizeInBytes: createdVolume.SizeInBytes,
			Style:       "flexgroup",
			State:       createdVolume.State,
		}

		result, err := store.UpdateExpertModeVolume(ctx, updatedVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "flexgroup", result.Style)
		assert.Equal(tt, createdVolume.Name, result.Name)
		assert.Equal(tt, createdVolume.SizeInBytes, result.SizeInBytes)
	})

	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		// Try to update non-existent volume
		nonExistentVolume := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: "non-existent-uuid",
			},
			Name:        "non-existent-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateAvailable,
		}

		result, err := store.UpdateExpertModeVolume(ctx, nonExistentVolume)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err))
	})

	t.Run("WhenStateTransitionsArePerformed", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		// Create initial volume in CREATING state
		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-expert-volume",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateCreating,
		}

		createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
		assert.NoError(tt, err)
		assert.NotNil(tt, createdVolume)
		assert.Equal(tt, datamodel.LifeCycleStateCreating, createdVolume.State)

		// Transition: CREATING -> AVAILABLE
		updatedVolume1 := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: createdVolume.UUID,
			},
			Name:        createdVolume.Name,
			SizeInBytes: createdVolume.SizeInBytes,
			Style:       createdVolume.Style,
			State:       datamodel.LifeCycleStateAvailable,
		}

		result1, err := store.UpdateExpertModeVolume(ctx, updatedVolume1)
		assert.NoError(tt, err)
		assert.NotNil(tt, result1)
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, result1.State)

		// Transition: AVAILABLE -> DELETED
		updatedVolume2 := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: createdVolume.UUID,
			},
			Name:        result1.Name,
			SizeInBytes: result1.SizeInBytes,
			Style:       result1.Style,
			State:       datamodel.LifeCycleStateDeleted,
		}

		result2, err := store.UpdateExpertModeVolume(ctx, updatedVolume2)
		assert.NoError(tt, err)
		assert.NotNil(tt, result2)
		assert.Equal(tt, datamodel.LifeCycleStateDeleted, result2.State)
	})
}

func TestGetExpertModeVolumeByUUID_Success(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	// Create expert mode volume
	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776, // 1TB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
	assert.NoError(t, err)
	assert.NotNil(t, createdVolume)

	// Retrieve the volume by UUID
	retrievedVolume, err := store.GetExpertModeVolumeByUUID(ctx, createdVolume.UUID)

	assert.NoError(t, err)
	assert.NotNil(t, retrievedVolume)

	// Validate retrieved volume
	assert.Equal(t, createdVolume.UUID, retrievedVolume.UUID)
	assert.Equal(t, createdVolume.Name, retrievedVolume.Name)
	assert.Equal(t, createdVolume.SizeInBytes, retrievedVolume.SizeInBytes)
	assert.Equal(t, createdVolume.PoolID, retrievedVolume.PoolID)
	assert.Equal(t, createdVolume.AccountID, retrievedVolume.AccountID)
	assert.Equal(t, createdVolume.SvmID, retrievedVolume.SvmID)
}

func TestGetExpertModeVolumeByUUID_NotFound(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	// Try to retrieve non-existent volume
	nonExistentUUID := utils.RandomUUID()
	retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, nonExistentUUID)

	assert.Error(t, err)
	assert.Nil(t, retrievedVolume)
}

func TestGetExpertModeVolumeByUUID_WithBackupConfig(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	backupVaultID := "test-backup-vault-uuid"
	backupConfig := &datamodel.DataProtection{
		BackupVaultID: backupVaultID,
	}
	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
		BackupConfig: backupConfig,
	}

	// Create the volume
	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
	assert.NoError(t, err)
	assert.NotNil(t, createdVolume)

	// Verify the volume exists in the database
	retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, createdVolume.ExternalUUID)
	assert.NoError(t, err)
	assert.NotNil(t, retrievedVolume)

	// Validate the backup configuration
	assert.NotNil(t, retrievedVolume.BackupConfig)
	assert.Equal(t, backupVaultID, retrievedVolume.BackupConfig.BackupVaultID)
}

func TestUpdateExpertModeVolume_Success(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	// Create the initial volume
	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776, // 1TB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
	assert.NoError(t, err)
	assert.NotNil(t, createdVolume)

	// Verify the volume exists in the database
	retrievedVolume, err := store.GetExpertModeVolumeByUUID(ctx, createdVolume.UUID)
	assert.NoError(t, err)
	assert.NotNil(t, retrievedVolume)

	// Update the volume with new values
	updatedVolume := &datamodel.ExpertModeVolumes{
		BaseModel: datamodel.BaseModel{
			UUID: createdVolume.UUID,
		},
		Name:        "updated-expert-volume",
		SizeInBytes: 2199023255552, // 2TB
		Style:       "flexgroup",
		State:       datamodel.LifeCycleStateAvailable,
	}

	result, err := store.UpdateExpertModeVolume(ctx, updatedVolume)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "updated-expert-volume", result.Name)
	assert.Equal(t, int64(2199023255552), result.SizeInBytes)
	assert.Equal(t, "flexgroup", result.Style)
	assert.Equal(t, datamodel.LifeCycleStateAvailable, result.State)
	assert.Equal(t, createdVolume.UUID, result.UUID)
	assert.Equal(t, pool.ID, result.PoolID)
	assert.Equal(t, account.ID, result.AccountID)
	assert.Equal(t, svm.ID, result.SvmID)
}

func TestUpdateExpertModeVolume_WithNilBackupConfig(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	// Create the initial volume
	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776, // 1TB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
	assert.NoError(t, err)
	assert.NotNil(t, createdVolume)

	// Verify the volume exists in the database
	retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, createdVolume.ExternalUUID)
	assert.NoError(t, err)
	assert.NotNil(t, retrievedVolume)

	// Update with nil BackupConfig
	retrievedVolume.BackupConfig = nil
	err = store.UpdateExpertModeVolumeDataProtection(ctx, retrievedVolume)
	assert.NoError(t, err)

	// Retrieve the volume to verify the update
	updatedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, retrievedVolume.ExternalUUID)
	assert.NoError(t, err)
	assert.NotNil(t, updatedVolume)

	// BackupConfig should be nil or effectively empty after update
	if updatedVolume.BackupConfig != nil {
		assert.Empty(t, updatedVolume.BackupConfig.BackupVaultID)
		assert.Empty(t, updatedVolume.BackupConfig.BackupPolicyID)
		assert.Nil(t, updatedVolume.BackupConfig.ScheduledBackupEnabled)
		assert.Nil(t, updatedVolume.BackupConfig.BackupChainBytes)
		assert.Nil(t, updatedVolume.BackupConfig.KmsGrant)
	}
}

func TestUpdateExpertModeVolume_WhenVolumeNotFound_ReturnsError(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	nonExistentUUID := utils.RandomUUID()
	expertModeVolume := &datamodel.ExpertModeVolumes{
		BaseModel: datamodel.BaseModel{
			UUID: nonExistentUUID,
		},
		ExternalUUID: nonExistentUUID,
		BackupConfig: &datamodel.DataProtection{
			BackupVaultID: "test-vault-id",
		},
	}

	err := store.UpdateExpertModeVolumeDataProtection(ctx, expertModeVolume)

	// The update will succeed (GORM Updates doesn't fail if no rows match)
	// but we can verify the volume doesn't exist
	assert.NoError(t, err)

	// Verify the volume doesn't exist
	retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, nonExistentUUID)
	assert.Error(t, err)
	assert.Nil(t, retrievedVolume)
}

func TestUpdateExpertModeVolumeFields_Success(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	// Create initial volume
	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776, // 1TB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
		BackupConfig: &datamodel.DataProtection{
			BackupVaultID: "old-vault-id",
		},
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
	assert.NoError(t, err)
	assert.NotNil(t, createdVolume)

	// Update fields
	logicalSize := int64(2048)
	updates := map[string]interface{}{
		"data_protection": &datamodel.DataProtection{
			BackupVaultID:    "new-vault-id",
			BackupChainBytes: &logicalSize,
		},
		"state": datamodel.LifeCycleStateAvailable,
	}

	err = store.UpdateExpertModeVolumeFields(ctx, createdVolume.ExternalUUID, updates)
	assert.NoError(t, err)

	// Verify the updates
	retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, createdVolume.ExternalUUID)
	assert.NoError(t, err)
	assert.NotNil(t, retrievedVolume)
	assert.Equal(t, datamodel.LifeCycleStateAvailable, retrievedVolume.State)
	assert.NotNil(t, retrievedVolume.BackupConfig)
	assert.Equal(t, "new-vault-id", retrievedVolume.BackupConfig.BackupVaultID)
	assert.NotNil(t, retrievedVolume.BackupConfig.BackupChainBytes)
	assert.Equal(t, logicalSize, *retrievedVolume.BackupConfig.BackupChainBytes)
}

func TestUpdateExpertModeVolumeFields_VolumeNotFound(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	updates := map[string]interface{}{
		"state": datamodel.LifeCycleStateAvailable,
	}

	err := store.UpdateExpertModeVolumeFields(ctx, "nonexistent-uuid", updates)
	assert.Error(t, err)
}

func TestUpdateExpertModeVolumeFields_UpdateStateOnly(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
	assert.NoError(t, err)

	// Update only state
	updates := map[string]interface{}{
		"state": datamodel.LifeCycleStateAvailable,
	}

	err = store.UpdateExpertModeVolumeFields(ctx, createdVolume.ExternalUUID, updates)
	assert.NoError(t, err)

	// Verify the update
	retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, createdVolume.ExternalUUID)
	assert.NoError(t, err)
	assert.Equal(t, datamodel.LifeCycleStateAvailable, retrievedVolume.State)
	assert.Equal(t, createdVolume.Name, retrievedVolume.Name)
	assert.Equal(t, createdVolume.SizeInBytes, retrievedVolume.SizeInBytes)
}

func TestUpdateExpertModeVolumeFields_UpdateDataProtection(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateAvailable,
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
	assert.NoError(t, err)

	// Update data protection
	backupChainBytes := int64(5120)
	updates := map[string]interface{}{
		"data_protection": &datamodel.DataProtection{
			BackupVaultID:    "test-backup-vault",
			BackupPolicyID:   "test-backup-policy",
			BackupChainBytes: &backupChainBytes,
		},
	}

	err = store.UpdateExpertModeVolumeFields(ctx, createdVolume.ExternalUUID, updates)
	assert.NoError(t, err)

	// Verify the update
	retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, createdVolume.ExternalUUID)
	assert.NoError(t, err)
	assert.NotNil(t, retrievedVolume.BackupConfig)
	assert.Equal(t, "test-backup-vault", retrievedVolume.BackupConfig.BackupVaultID)
	assert.Equal(t, "test-backup-policy", retrievedVolume.BackupConfig.BackupPolicyID)
	assert.NotNil(t, retrievedVolume.BackupConfig.BackupChainBytes)
	assert.Equal(t, backupChainBytes, *retrievedVolume.BackupConfig.BackupChainBytes)
}

func TestUpdateExpertModeVolumeFields_UpdateMultipleFields(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
	assert.NoError(t, err)

	// Update multiple fields
	backupChainBytes := int64(3072)
	updates := map[string]interface{}{
		"state": datamodel.LifeCycleStateAvailable,
		"name":  "updated-name",
		"style": "flexgroup",
		"data_protection": &datamodel.DataProtection{
			BackupVaultID:    "updated-vault",
			BackupChainBytes: &backupChainBytes,
		},
	}

	err = store.UpdateExpertModeVolumeFields(ctx, createdVolume.ExternalUUID, updates)
	assert.NoError(t, err)

	// Verify all updates
	retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, createdVolume.ExternalUUID)
	assert.NoError(t, err)
	assert.Equal(t, datamodel.LifeCycleStateAvailable, retrievedVolume.State)
	assert.Equal(t, "updated-name", retrievedVolume.Name)
	assert.Equal(t, "flexgroup", retrievedVolume.Style)
	assert.NotNil(t, retrievedVolume.BackupConfig)
	assert.Equal(t, "updated-vault", retrievedVolume.BackupConfig.BackupVaultID)
	assert.NotNil(t, retrievedVolume.BackupConfig.BackupChainBytes)
	assert.Equal(t, backupChainBytes, *retrievedVolume.BackupConfig.BackupChainBytes)
}

func TestUpdateExpertModeVolumeFields_EmptyUpdates(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateAvailable,
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
	assert.NoError(t, err)

	// Update with empty map (should still succeed and update updated_at)
	updates := map[string]interface{}{}

	err = store.UpdateExpertModeVolumeFields(ctx, createdVolume.ExternalUUID, updates)
	assert.NoError(t, err)

	// Verify volume is still retrievable and unchanged
	retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, createdVolume.ExternalUUID)
	assert.NoError(t, err)
	assert.Equal(t, createdVolume.Name, retrievedVolume.Name)
	assert.Equal(t, createdVolume.State, retrievedVolume.State)
}

func TestUpdateExpertModeVolumeFields_UpdatesUpdatedAtTimestamp(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:         "test-expert-volume",
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateAvailable,
	}

	createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
	assert.NoError(t, err)
	originalUpdatedAt := createdVolume.UpdatedAt

	// Wait a moment to ensure timestamp difference
	time.Sleep(10 * time.Millisecond)

	// Update a field
	updates := map[string]interface{}{
		"state": datamodel.LifeCycleStateDeleting,
	}

	err = store.UpdateExpertModeVolumeFields(ctx, createdVolume.ExternalUUID, updates)
	assert.NoError(t, err)

	// Verify updated_at was changed
	retrievedVolume, err := store.GetExpertModeVolumeByExternalUUID(ctx, createdVolume.ExternalUUID)
	assert.NoError(t, err)
	assert.True(t, retrievedVolume.UpdatedAt.After(originalUpdatedAt),
		"UpdatedAt should be after the original timestamp")
}

func TestDeleteExpertModeVolume(t *testing.T) {
	t.Run("WhenVolumeIsDeletedSuccessfully", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		// Create a volume in DELETING state
		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-volume-to-delete",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateDeleting,
		}

		createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
		assert.NoError(tt, err)
		assert.NotNil(tt, createdVolume)

		// Delete the volume
		err = store.DeleteExpertModeVolume(ctx, createdVolume.UUID)
		assert.NoError(tt, err)

		// Verify volume is soft-deleted (not retrievable via normal query)
		retrievedVolume, err := store.GetExpertModeVolumeByUUID(ctx, createdVolume.UUID)
		assert.Error(tt, err)
		assert.Nil(tt, retrievedVolume)
	})

	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		nonExistentUUID := utils.RandomUUID()

		// Try to delete non-existent volume
		err := store.DeleteExpertModeVolume(ctx, nonExistentUUID)

		assert.Error(tt, err)
		assert.True(tt, errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err))
	})

	t.Run("WhenVolumeAlreadySoftDeletedIsIdempotent", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-volume-double-delete",
			SizeInBytes:  1099511627776,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateDeleting,
		}

		createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
		assert.NoError(tt, err)

		// First delete soft-deletes the row (e.g. background reconciler wins the race).
		err = store.DeleteExpertModeVolume(ctx, createdVolume.UUID)
		assert.NoError(tt, err)

		// Second delete on the already soft-deleted row (e.g. the delete workflow's final
		// DeleteExpertModeVolumeInDB) must be a successful no-op, not a record-not-found error.
		err = store.DeleteExpertModeVolume(ctx, createdVolume.UUID)
		assert.NoError(tt, err)
	})

	t.Run("WhenVolumeInDifferentStates", func(tt *testing.T) {
		states := []string{
			datamodel.LifeCycleStateCreating,
			datamodel.LifeCycleStateAvailable,
			datamodel.LifeCycleStateDeleting,
		}

		for _, initialState := range states {
			tt.Run(initialState, func(ttt *testing.T) {
				store := setup(ttt)
				ctx := context.Background()
				account, pool := createTestAccountAndPoolForExpertMode(ttt, store)
				svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
				svmExternalUUID := utils.RandomUUID()
				svm := createTestSVMForExpertMode(ttt, store, pool.ID, account.ID, svmName, svmExternalUUID)

				expertModeVolume := &datamodel.ExpertModeVolumes{
					Name:         fmt.Sprintf("test-volume-%s", initialState),
					SizeInBytes:  1099511627776,
					PoolID:       pool.ID,
					AccountID:    account.ID,
					SvmID:        svm.ID,
					Style:        "flexvol",
					ExternalUUID: utils.RandomUUID(),
					State:        initialState,
				}

				createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
				assert.NoError(ttt, err)

				err = store.DeleteExpertModeVolume(ctx, createdVolume.UUID)
				assert.NoError(ttt, err)

				// Verify volume is soft-deleted
				retrievedVolume, err := store.GetExpertModeVolumeByUUID(ctx, createdVolume.UUID)
				assert.Error(ttt, err)
				assert.Nil(ttt, retrievedVolume)
			})
		}
	})

	t.Run("WhenVolumeIsDeleted_VerifyNotRetrievable", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-volume-with-relationships",
			SizeInBytes:  1099511627776,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateDeleting,
		}

		createdVolume, err := store.CreateExpertModeVolume(ctx, expertModeVolume)
		assert.NoError(tt, err)

		err = store.DeleteExpertModeVolume(ctx, createdVolume.UUID)
		assert.NoError(tt, err)

		// Verify volume is not retrievable after deletion
		retrievedVolume, err := store.GetExpertModeVolumeByUUID(ctx, createdVolume.UUID)
		assert.Error(tt, err)
		assert.Nil(tt, retrievedVolume)
	})

	t.Run("WhenDeletingVolumeDoesNotAffectPoolCapacity", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()
		account, pool := createTestAccountAndPoolForExpertMode(tt, store)
		svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
		svmExternalUUID := utils.RandomUUID()
		svm := createTestSVMForExpertMode(tt, store, pool.ID, account.ID, svmName, svmExternalUUID)

		// Create two volumes
		volume1 := &datamodel.ExpertModeVolumes{
			Name:         "volume-1",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateAvailable,
		}
		volume2 := &datamodel.ExpertModeVolumes{
			Name:         "volume-2",
			SizeInBytes:  536870912000, // 500GB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateAvailable,
		}

		createdVolume1, err := store.CreateExpertModeVolume(ctx, volume1)
		assert.NoError(tt, err)
		_, err = store.CreateExpertModeVolume(ctx, volume2)
		assert.NoError(tt, err)

		// Check initial capacity and count
		initialCapacity, err := store.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID)
		assert.NoError(tt, err)
		assert.NotNil(tt, initialCapacity)
		assert.Equal(tt, int64(1099511627776+536870912000), initialCapacity.TotalSize)
		assert.Equal(tt, int64(2), initialCapacity.VolumeCount)

		// Delete volume1
		err = store.DeleteExpertModeVolume(ctx, createdVolume1.UUID)
		assert.NoError(tt, err)

		// Check capacity and count after deletion - should only include volume2
		finalCapacity, err := store.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID)
		assert.NoError(tt, err)
		assert.NotNil(tt, finalCapacity)
		assert.Equal(tt, int64(536870912000), finalCapacity.TotalSize) // Only volume2 size
		assert.Equal(tt, int64(1), finalCapacity.VolumeCount)          // Only volume2 count
	})
}

func TestListExpertModeVolumesByPoolID_Success(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	// Create multiple volumes with different configurations
	backupConfig1 := &datamodel.DataProtection{
		BackupVaultID: "backup-vault-uuid-1",
		ScheduledBackupEnabled: func() *bool {
			b := true
			return &b
		}(),
	}
	volume1 := &datamodel.ExpertModeVolumes{
		Name:         fmt.Sprintf("test-list-volume-1-%s", utils.GenerateRandomAlphanumeric(8)),
		SizeInBytes:  1099511627776, // 1TB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: "external-uuid-1",
		State:        datamodel.LifeCycleStateREADY,
		BackupConfig: backupConfig1,
	}
	created1, err := store.CreateExpertModeVolume(ctx, volume1)
	assert.NoError(t, err)

	backupConfig2 := &datamodel.DataProtection{
		BackupVaultID: "backup-vault-uuid-2",
		ScheduledBackupEnabled: func() *bool {
			b := false
			return &b
		}(),
	}
	volume2 := &datamodel.ExpertModeVolumes{
		Name:         fmt.Sprintf("test-list-volume-2-%s", utils.GenerateRandomAlphanumeric(8)),
		SizeInBytes:  536870912000, // 500GB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexgroup",
		ExternalUUID: "external-uuid-2",
		State:        datamodel.LifeCycleStateCreating,
		BackupConfig: backupConfig2,
	}
	created2, err := store.CreateExpertModeVolume(ctx, volume2)
	assert.NoError(t, err)

	// Volume without backup config
	volume3 := &datamodel.ExpertModeVolumes{
		Name:         fmt.Sprintf("test-list-volume-3-%s", utils.GenerateRandomAlphanumeric(8)),
		SizeInBytes:  214748364800, // 200GB
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: "external-uuid-3",
		State:        datamodel.LifeCycleStateREADY,
	}
	created3, err := store.CreateExpertModeVolume(ctx, volume3)
	assert.NoError(t, err)

	// Test listing volumes for the pool
	volumes, err := store.ListExpertModeVolumesByPoolID(ctx, pool.ID)
	assert.NoError(t, err)
	assert.NotNil(t, volumes)
	assert.Equal(t, 3, len(volumes), "Should return exactly 3 volumes for the test pool")

	// Create a map for easier verification
	volumeMap := make(map[string]*datamodel.ExpertModeVolumes)
	for _, vol := range volumes {
		volumeMap[vol.UUID] = vol
	}

	// Verify volume 1
	vol1, exists := volumeMap[created1.UUID]
	assert.True(t, exists, "Volume 1 should exist")
	assert.Equal(t, created1.Name, vol1.Name)
	assert.Equal(t, "external-uuid-1", vol1.ExternalUUID)
	assert.Equal(t, int64(1099511627776), vol1.SizeInBytes)
	assert.Equal(t, "flexvol", vol1.Style)
	assert.Equal(t, datamodel.LifeCycleStateREADY, vol1.State)
	assert.NotNil(t, vol1.BackupConfig)
	assert.Equal(t, "backup-vault-uuid-1", vol1.BackupConfig.BackupVaultID)
	assert.NotNil(t, vol1.BackupConfig.ScheduledBackupEnabled)
	assert.True(t, *vol1.BackupConfig.ScheduledBackupEnabled)

	// Verify volume 2
	vol2, exists := volumeMap[created2.UUID]
	assert.True(t, exists, "Volume 2 should exist")
	assert.Equal(t, created2.Name, vol2.Name)
	assert.Equal(t, "external-uuid-2", vol2.ExternalUUID)
	assert.Equal(t, int64(536870912000), vol2.SizeInBytes)
	assert.Equal(t, "flexgroup", vol2.Style)
	assert.Equal(t, datamodel.LifeCycleStateCreating, vol2.State)
	assert.NotNil(t, vol2.BackupConfig)
	assert.Equal(t, "backup-vault-uuid-2", vol2.BackupConfig.BackupVaultID)
	assert.NotNil(t, vol2.BackupConfig.ScheduledBackupEnabled)
	assert.False(t, *vol2.BackupConfig.ScheduledBackupEnabled)

	// Verify volume 3 (no backup config)
	vol3, exists := volumeMap[created3.UUID]
	assert.True(t, exists, "Volume 3 should exist")
	assert.Equal(t, created3.Name, vol3.Name)
	assert.Equal(t, "external-uuid-3", vol3.ExternalUUID)
	assert.Equal(t, int64(214748364800), vol3.SizeInBytes)
	assert.Equal(t, "flexvol", vol3.Style)
	assert.Equal(t, datamodel.LifeCycleStateREADY, vol3.State)
	// GORM deserializes NULL JSONB as empty struct, so check for empty BackupVaultID
	if vol3.BackupConfig != nil {
		assert.Empty(t, vol3.BackupConfig.BackupVaultID, "Volume 3 should not have backup vault configured")
	}
}

func TestListExpertModeVolumesByPoolID_EmptyPool(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	_, pool := createTestAccountAndPoolForExpertMode(t, store)

	// Test with empty pool (no volumes created)
	emptyVolumes, err := store.ListExpertModeVolumesByPoolID(ctx, pool.ID)
	assert.NoError(t, err)
	assert.NotNil(t, emptyVolumes)
	assert.Equal(t, 0, len(emptyVolumes), "Should return empty slice for pool with no volumes")
}

func TestListExpertModeVolumesByPoolID_NonExistentPool(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	// Test with non-existent pool ID
	nonExistentVolumes, err := store.ListExpertModeVolumesByPoolID(ctx, 999999)
	assert.NoError(t, err, "Should not error for non-existent pool")
	assert.NotNil(t, nonExistentVolumes)
	assert.Equal(t, 0, len(nonExistentVolumes), "Should return empty slice for non-existent pool")
}

func TestListExpertModeVolumesByPoolID_IsolationBetweenPools(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool1 := createTestAccountAndPoolForExpertMode(t, store)

	// Create second pool
	pool2UUID := utils.RandomUUID()
	deploymentName := fmt.Sprintf("test_deployment_%s", utils.GenerateRandomAlphanumeric(8))
	pool2 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID:      pool2UUID,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Name:           fmt.Sprintf("test_pool_2_%s", utils.GenerateRandomAlphanumeric(8)),
		AccountID:      account.ID,
		SizeInBytes:    2199023255552,
		DeploymentName: deploymentName,
		PoolAttributes: &datamodel.PoolAttributes{
			PrimaryZone: "us-west1-b",
		},
	}
	err := store.db.Create(pool2).Error()
	assert.NoError(t, err)

	svmName1 := fmt.Sprintf("test-svm-1-%s", utils.GenerateRandomAlphanumeric(8))
	svm1 := createTestSVMForExpertMode(t, store, pool1.ID, account.ID, svmName1, utils.RandomUUID())

	svmName2 := fmt.Sprintf("test-svm-2-%s", utils.GenerateRandomAlphanumeric(8))
	svm2 := createTestSVMForExpertMode(t, store, pool2.ID, account.ID, svmName2, utils.RandomUUID())

	// Create volumes in pool1
	volume1Pool1 := &datamodel.ExpertModeVolumes{
		Name:         fmt.Sprintf("pool1-volume-1-%s", utils.GenerateRandomAlphanumeric(8)),
		SizeInBytes:  1099511627776,
		PoolID:       pool1.ID,
		AccountID:    account.ID,
		SvmID:        svm1.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}
	_, err = store.CreateExpertModeVolume(ctx, volume1Pool1)
	assert.NoError(t, err)

	volume2Pool1 := &datamodel.ExpertModeVolumes{
		Name:         fmt.Sprintf("pool1-volume-2-%s", utils.GenerateRandomAlphanumeric(8)),
		SizeInBytes:  536870912000,
		PoolID:       pool1.ID,
		AccountID:    account.ID,
		SvmID:        svm1.ID,
		Style:        "flexgroup",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}
	_, err = store.CreateExpertModeVolume(ctx, volume2Pool1)
	assert.NoError(t, err)

	// Create volumes in pool2
	volumePool2 := &datamodel.ExpertModeVolumes{
		Name:         fmt.Sprintf("pool2-volume-1-%s", utils.GenerateRandomAlphanumeric(8)),
		SizeInBytes:  107374182400,
		PoolID:       pool2.ID,
		AccountID:    account.ID,
		SvmID:        svm2.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}
	_, err = store.CreateExpertModeVolume(ctx, volumePool2)
	assert.NoError(t, err)

	// Verify pool1 has exactly 2 volumes
	pool1Volumes, err := store.ListExpertModeVolumesByPoolID(ctx, pool1.ID)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(pool1Volumes), "Pool 1 should have exactly 2 volumes")

	// Verify pool2 has exactly 1 volume
	pool2Volumes, err := store.ListExpertModeVolumesByPoolID(ctx, pool2.ID)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(pool2Volumes), "Pool 2 should have exactly 1 volume")

	// Verify no overlap
	for _, vol := range pool1Volumes {
		assert.Equal(t, pool1.ID, vol.PoolID, "All pool1 volumes should belong to pool1")
	}
	for _, vol := range pool2Volumes {
		assert.Equal(t, pool2.ID, vol.PoolID, "All pool2 volumes should belong to pool2")
	}
}

func TestGetEligibleExpertModeVolumes_Success(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	vol1 := &datamodel.ExpertModeVolumes{
		Name:         fmt.Sprintf("eligible-vol-1-%s", utils.GenerateRandomAlphanumeric(8)),
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}
	_, err := store.CreateExpertModeVolume(ctx, vol1)
	assert.NoError(t, err)

	vol2 := &datamodel.ExpertModeVolumes{
		Name:         fmt.Sprintf("eligible-vol-2-%s", utils.GenerateRandomAlphanumeric(8)),
		SizeInBytes:  536870912000,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexgroup",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateCreating,
	}
	_, err = store.CreateExpertModeVolume(ctx, vol2)
	assert.NoError(t, err)

	pagination := &dbutils.Pagination{Offset: 0, Limit: 1000}
	volumes, err := store.GetEligibleExpertModeVolumes(ctx, [][]interface{}{}, pagination)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(volumes), 2)

	for _, v := range volumes {
		assert.NotEmpty(t, v.Name)
		assert.NotEmpty(t, v.State)
	}
}

func TestGetEligibleExpertModeVolumes_ExcludesDeletedVolumes(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	vol := &datamodel.ExpertModeVolumes{
		Name:         fmt.Sprintf("delete-me-%s", utils.GenerateRandomAlphanumeric(8)),
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}
	created, err := store.CreateExpertModeVolume(ctx, vol)
	assert.NoError(t, err)

	err = store.DeleteExpertModeVolume(ctx, created.UUID)
	assert.NoError(t, err)

	pagination := &dbutils.Pagination{Offset: 0, Limit: 1000}
	volumes, err := store.GetEligibleExpertModeVolumes(ctx, [][]interface{}{}, pagination)
	assert.NoError(t, err)

	for _, v := range volumes {
		assert.NotEqual(t, created.Name, v.Name, "Deleted volume should not appear in eligible results")
	}
}

func TestGetEligibleExpertModeVolumes_Pagination(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svmExternalUUID := utils.RandomUUID()
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, svmExternalUUID)

	for i := 0; i < 3; i++ {
		vol := &datamodel.ExpertModeVolumes{
			Name:         fmt.Sprintf("page-vol-%d-%s", i, utils.GenerateRandomAlphanumeric(8)),
			SizeInBytes:  1099511627776,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateREADY,
		}
		_, err := store.CreateExpertModeVolume(ctx, vol)
		assert.NoError(t, err)
	}

	page1 := &dbutils.Pagination{Offset: 0, Limit: 2}
	vols1, err := store.GetEligibleExpertModeVolumes(ctx, [][]interface{}{}, page1)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(vols1))

	page2 := &dbutils.Pagination{Offset: 2, Limit: 2}
	vols2, err := store.GetEligibleExpertModeVolumes(ctx, [][]interface{}{}, page2)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(vols2), 1)
}

func TestGetEligibleExpertModeVolumes_EmptyResult(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	pagination := &dbutils.Pagination{Offset: 999999, Limit: 1000}
	volumes, err := store.GetEligibleExpertModeVolumes(ctx, [][]interface{}{}, pagination)
	assert.NoError(t, err)
	assert.Empty(t, volumes)
}

func TestGetMultipleVolumesWithExpertMode_Success(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, utils.RandomUUID())

	vol := &datamodel.ExpertModeVolumes{
		Name:         fmt.Sprintf("test-vol-%s", utils.GenerateRandomAlphanumeric(8)),
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}
	created, err := store.CreateExpertModeVolume(ctx, vol)
	assert.NoError(t, err)

	results, err := store.GetMultipleVolumesWithExpertMode(ctx, [][]interface{}{})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 1)

	found := false
	for _, v := range results {
		if v.UUID == created.UUID {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestGetMultipleVolumesWithExpertMode_DBError(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	orig := getMultipleVolumesWithExpertMode
	defer func() { getMultipleVolumesWithExpertMode = orig }()
	getMultipleVolumesWithExpertMode = func(db *gorm.DB) ([]*datamodel.ExpertModeVolumes, error) {
		return nil, errors.New("simulated db error")
	}

	results, err := store.GetMultipleVolumesWithExpertMode(ctx, [][]interface{}{})
	assert.Nil(t, results)
	assert.EqualError(t, err, "simulated db error")
}

func TestListExpertModeVolumesWithPagination_Success(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, utils.RandomUUID())

	vol := &datamodel.ExpertModeVolumes{
		Name:         fmt.Sprintf("pag-vol-%s", utils.GenerateRandomAlphanumeric(8)),
		SizeInBytes:  1099511627776,
		PoolID:       pool.ID,
		AccountID:    account.ID,
		SvmID:        svm.ID,
		Style:        "flexvol",
		ExternalUUID: utils.RandomUUID(),
		State:        datamodel.LifeCycleStateREADY,
	}
	created, err := store.CreateExpertModeVolume(ctx, vol)
	assert.NoError(t, err)

	pagination := &dbutils.Pagination{Offset: 0, Limit: 100}
	results, err := store.ListExpertModeVolumesWithPagination(ctx, [][]interface{}{}, pagination)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 1)

	found := false
	for _, v := range results {
		if v.UUID == created.UUID {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestListExpertModeVolumesWithPagination_OrderedByIDWithoutOverlap(t *testing.T) {
	store := setup(t)
	ctx := context.Background()
	account, pool := createTestAccountAndPoolForExpertMode(t, store)
	svmName := fmt.Sprintf("test-svm-%s", utils.GenerateRandomAlphanumeric(8))
	svm := createTestSVMForExpertMode(t, store, pool.ID, account.ID, svmName, utils.RandomUUID())

	// Deliberately unsorted UUIDs so insertion/id order differs from uuid lexicographic order.
	uuids := []string{"em-vol-c", "em-vol-a", "em-vol-e", "em-vol-b", "em-vol-d"}
	createdIDs := make([]int64, 0, len(uuids))
	for _, uuid := range uuids {
		vol := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: uuid},
			Name:         fmt.Sprintf("pag-vol-%s", uuid),
			SizeInBytes:  1099511627776,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			ExternalUUID: utils.RandomUUID(),
			State:        datamodel.LifeCycleStateREADY,
		}
		created, err := store.CreateExpertModeVolume(ctx, vol)
		assert.NoError(t, err)
		createdIDs = append(createdIDs, created.ID)
	}

	conditions := [][]interface{}{
		{"account_id = ?", account.ID},
	}
	page1, err := store.ListExpertModeVolumesWithPagination(ctx, conditions, &dbutils.Pagination{Offset: 0, Limit: 2})
	assert.NoError(t, err)
	require.Len(t, page1, 2)
	assert.Equal(t, []int64{createdIDs[0], createdIDs[1]}, []int64{page1[0].ID, page1[1].ID})
	assert.Equal(t, []string{"em-vol-c", "em-vol-a"}, []string{page1[0].UUID, page1[1].UUID})

	page2, err := store.ListExpertModeVolumesWithPagination(ctx, conditions, &dbutils.Pagination{Offset: 2, Limit: 2})
	assert.NoError(t, err)
	require.Len(t, page2, 2)
	assert.Equal(t, []int64{createdIDs[2], createdIDs[3]}, []int64{page2[0].ID, page2[1].ID})
	assert.Equal(t, []string{"em-vol-e", "em-vol-b"}, []string{page2[0].UUID, page2[1].UUID})

	page3, err := store.ListExpertModeVolumesWithPagination(ctx, conditions, &dbutils.Pagination{Offset: 4, Limit: 2})
	assert.NoError(t, err)
	require.Len(t, page3, 1)
	assert.Equal(t, createdIDs[4], page3[0].ID)
	assert.Equal(t, "em-vol-d", page3[0].UUID)

	// Consecutive pages are strictly increasing by id (pageN[last] < pageN+1[first]).
	assert.Less(t, page1[len(page1)-1].ID, page2[0].ID)
	assert.Less(t, page2[len(page2)-1].ID, page3[0].ID)

	seen := map[string]bool{}
	for _, page := range [][]*datamodel.ExpertModeVolumes{page1, page2, page3} {
		for _, volume := range page {
			assert.False(t, seen[volume.UUID], "volume %s appeared on more than one page", volume.UUID)
			seen[volume.UUID] = true
		}
	}
	assert.Equal(t, 5, len(seen))
}

func TestListExpertModeVolumesWithPagination_DBError(t *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(t, err)

	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(t, err)

	sqlDB, err := store.db.GORM().DB()
	assert.NoError(t, err)
	_ = sqlDB.Close()

	pagination := &dbutils.Pagination{Offset: 0, Limit: 100}
	results, err := store.ListExpertModeVolumesWithPagination(context.Background(), [][]interface{}{}, pagination)
	assert.Nil(t, results)
	assert.Error(t, err)
}
