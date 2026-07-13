package gcp

import (
	"context"
	"database/sql"
	stderrors "errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	commonfactory "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/validators"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"gorm.io/gorm"
)

func setup(t *testing.T) (context.Context, database.Storage, *GCPOrchestrator, *workflowenginemock.MockTemporalTestClient) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
	store, err := database.SetupStorageForTest(mockLogger)
	assert.NoError(t, err)
	err = database.ClearInMemoryDB(store.DB())
	assert.NoError(t, err)

	temporal := workflowenginemock.NewMockTemporalTestClient(t)

	orch := &GCPOrchestrator{storage: store, temporal: temporal}
	return ctx, store, orch, temporal
}
func createDBPools(t *testing.T, store database.Storage) ([]*datamodel.Pool, *datamodel.Account) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err := store.DB().Create(account).Error
	if err != nil {
		t.Fatalf("Failed to create account: %v", err)
	}
	pool1 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid1"},
		Name:      "test_pool_1",
		AccountID: account.ID,
		VendorID:  "test-vendor-id",
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 64,
			Iops:            1024, // datamodel.PoolAttributes expects int64, not pointer
			PrimaryZone:     "us-central1-a",
		},
		DeploymentName: "dep1",
	}
	deletedPool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID:      "test-pool-uuid-deleted",
			DeletedAt: &gorm.DeletedAt{Time: time.Now(), Valid: true}, // Simulate a deleted pool
		},
		Name:      "test_pool_2",
		AccountID: account.ID,
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 128,
			Iops:            2048,
			PrimaryZone:     "us-central1-b",
			SecondaryZone:   "us-central1-c",
		},
		DeploymentName: "dep-deleted",
	}
	pool2 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: "test-pool-uuid2",
		},
		Name:      "test_pool_2",
		AccountID: account.ID,
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 128,
			Iops:            2048,
			PrimaryZone:     "us-central1-b",
			SecondaryZone:   "us-central1-c",
		},
		DeploymentName: "dep2",
	}

	err = store.DB().Create(pool1).Error
	assert.NoError(t, err)
	err = store.DB().Create(deletedPool).Error
	assert.NoError(t, err)
	err = store.DB().Create(pool2).Error
	assert.NoError(t, err)

	var pools []*datamodel.Pool
	store.DB().Find(&pools)

	return []*datamodel.Pool{pool1, deletedPool, pool2}, account
}

func TestConvertDatastorePoolToModel_ValidPool_ReturnsCorrectModelWIthAccountID(t *testing.T) {
	deletedAt := gorm.DeletedAt{Time: time.Now(), Valid: true}
	now := time.Now()
	accountName := "test-account"
	datastorePool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: now,
			UpdatedAt: now,
			DeletedAt: &deletedAt,
		},
		Name:             "Test Pool",
		Description:      "Test Description",
		SizeInBytes:      1024,
		State:            "active",
		StateDetails:     "running",
		AllowAutoTiering: true,
		Network:          "test-network",
		ServiceLevel:     "FLEX",
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 64,
			Iops:            1024,
			PrimaryZone:     "us-central1-a",
			SecondaryZone:   "us-central1-b",
			// Add all other fields that might be accessed in conversion
			Labels: &datamodel.JSONB{"env": "test"}},
		Account: &datamodel.Account{Name: accountName},
		// Add any other fields that might be accessed in conversion
	}

	dbPoolView := database.ConvertPoolToPoolView(datastorePool)
	result := commonfactory.ConvertDatastorePoolToModel(dbPoolView, accountName)

	assert.Equal(t, datastorePool.UUID, result.UUID)
	assert.Equal(t, datastorePool.CreatedAt, result.CreatedAt)
	assert.Equal(t, datastorePool.UpdatedAt, result.UpdatedAt)
	assert.Equal(t, &deletedAt.Time, result.DeletedAt)
	assert.Equal(t, accountName, result.AccountName)
	assert.Equal(t, datastorePool.Name, result.Name)
	assert.Equal(t, datastorePool.Description, result.Description)
	assert.Equal(t, uint64(datastorePool.SizeInBytes), result.SizeInBytes)
	assert.Equal(t, datastorePool.State, result.State)
	assert.Equal(t, datastorePool.StateDetails, result.StateDetails)
	assert.Equal(t, datastorePool.AllowAutoTiering, result.AllowAutoTiering)
	assert.Equal(t, datastorePool.Network, result.VendorSubNetID)
	assert.Equal(t, datastorePool.ServiceLevel, result.ServiceLevel)
	assert.Equal(t, float64(datastorePool.PoolAttributes.ThroughputMibps), result.TotalThroughputMibps)
	assert.Equal(t, float64(0), result.UtilizedThroughputMibps)
	assert.Equal(t, datastorePool.PoolAttributes.Iops, result.TotalIops)
	assert.Equal(t, int64(0), result.UtilizedIops)
}

func TestConvertDatastorePoolToModel_ValidPool_ReturnsCorrectModel(t *testing.T) {
	deletedAt := gorm.DeletedAt{Time: time.Now(), Valid: true}
	accountName := "test-account"
	datastorePool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			DeletedAt: &deletedAt,
		},
		Name:             "Test Pool",
		Description:      "Test Description",
		SizeInBytes:      1024,
		State:            "active",
		StateDetails:     "running",
		AllowAutoTiering: true,
		Network:          "test-network",
		ServiceLevel:     "FLEX",
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 64,
			Iops:            1024, // datamodel.PoolAttributes expects int64, not pointer
			PrimaryZone:     "us-central1-a",
			SecondaryZone:   "us-central1-b",
			// Add all other fields that might be accessed in conversion
			Labels: &datamodel.JSONB{"env": "test"}},
		Account: &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: accountName},
		// Add any other fields that might be accessed in conversion
	}

	dbPoolView := database.ConvertPoolToPoolView(datastorePool)
	result := commonfactory.ConvertDatastorePoolToModel(dbPoolView, accountName)

	assert.Equal(t, datastorePool.UUID, result.UUID)
	assert.Equal(t, datastorePool.CreatedAt, result.CreatedAt)
	assert.Equal(t, datastorePool.UpdatedAt, result.UpdatedAt)
	assert.Equal(t, &deletedAt.Time, result.DeletedAt)
	assert.Equal(t, accountName, result.AccountName)
	assert.Equal(t, datastorePool.Name, result.Name)
	assert.Equal(t, datastorePool.Description, result.Description)
	assert.Equal(t, uint64(datastorePool.SizeInBytes), result.SizeInBytes)
	assert.Equal(t, datastorePool.State, result.State)
	assert.Equal(t, datastorePool.StateDetails, result.StateDetails)
	assert.Equal(t, datastorePool.AllowAutoTiering, result.AllowAutoTiering)
	assert.Equal(t, datastorePool.Network, result.VendorSubNetID)
	assert.Equal(t, datastorePool.ServiceLevel, result.ServiceLevel)
	assert.Equal(t, float64(datastorePool.PoolAttributes.ThroughputMibps), result.TotalThroughputMibps)
	assert.Equal(t, float64(0), result.UtilizedThroughputMibps)
	assert.Equal(t, datastorePool.PoolAttributes.Iops, result.TotalIops)
	assert.Equal(t, int64(0), result.UtilizedIops)
}

func TestConvertDatastorePoolToModel_NilDeletedAt_ReturnsNilDeletedAt(t *testing.T) {
	datastorePool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			DeletedAt: nil,
		},
		Name:             "Test Pool",
		Description:      "Test Description",
		SizeInBytes:      1024,
		State:            "active",
		StateDetails:     "running",
		AllowAutoTiering: true,
		Network:          "test-network",
		ServiceLevel:     "FLEX",
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 64,
			Iops:            1024, // datamodel.PoolAttributes expects int64, not pointer
			PrimaryZone:     "us-central1-a",
			SecondaryZone:   "us-central1-b",
		},
	}
	accountName := "test-account"
	dbPoolView := database.ConvertPoolToPoolView(datastorePool)
	result := commonfactory.ConvertDatastorePoolToModel(dbPoolView, accountName)

	assert.Nil(t, result.DeletedAt)
}

func TestConvertDatastorePoolToModel_InvalidDeletedAt_ReturnsNilDeletedAt(t *testing.T) {
	invalidDeletedAt := gorm.DeletedAt{Time: time.Now(), Valid: false}
	datastorePool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			DeletedAt: &invalidDeletedAt,
		},
		Name:             "Test Pool",
		Description:      "Test Description",
		SizeInBytes:      1024,
		State:            "active",
		StateDetails:     "running",
		AllowAutoTiering: true,
		Network:          "test-network",
		ServiceLevel:     "FLEX",
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 64,
			Iops:            1024, // datamodel.PoolAttributes expects int64, not pointer
			PrimaryZone:     "us-central1-a",
			SecondaryZone:   "",
		},
	}
	accountName := "test-account"
	dbPoolView := database.ConvertPoolToPoolView(datastorePool)
	result := commonfactory.ConvertDatastorePoolToModel(dbPoolView, accountName)

	assert.Nil(t, result.DeletedAt)
}

func TestConvertDatastorePoolToModel_WithKms(t *testing.T) {
	invalidDeletedAt := gorm.DeletedAt{Time: time.Now(), Valid: false}
	datastorePool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			DeletedAt: &invalidDeletedAt,
		},
		Name:             "Test Pool",
		Description:      "Test Description",
		SizeInBytes:      1024,
		State:            "active",
		StateDetails:     "running",
		AllowAutoTiering: true,
		Network:          "test-network",
		ServiceLevel:     "FLEX",
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 64,
			Iops:            1024, // datamodel.PoolAttributes expects int64, not pointer
			PrimaryZone:     "us-central1-a",
			SecondaryZone:   "",
		},
		KmsConfigID: sql.NullInt64{Valid: true, Int64: 1},
		KmsConfig: &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				UUID: "kms-config-uuid",
			},
			Name:              "Test KMS Config",
			Description:       "Test KMS Config Description",
			State:             "active",
			StateDetails:      "running",
			KeyRing:           "test-key-ring",
			KeyRingLocation:   "us-central1",
			KeyName:           "test-key-name",
			AccountID:         1,
			CustomerProjectID: "test-customer-project-id",
			KeyProjectID:      "test-key-project-id",
		},
	}
	accountName := "test-account"
	dbPoolView := database.ConvertPoolToPoolView(datastorePool)
	result := commonfactory.ConvertDatastorePoolToModel(dbPoolView, accountName)

	assert.Nil(t, result.DeletedAt)
	assert.Equal(t, result.KmsConfig.KeyRingLocation, datastorePool.KmsConfig.KeyRingLocation)
	assert.Equal(t, result.KmsConfig.KeyRing, datastorePool.KmsConfig.KeyRing)
	assert.Equal(t, result.KmsConfig.KeyName, datastorePool.KmsConfig.KeyName)
	assert.Equal(t, result.KmsConfig.CustomerProjectID, datastorePool.KmsConfig.CustomerProjectID)
	assert.Equal(t, result.KmsConfig.KeyProjectID, datastorePool.KmsConfig.KeyProjectID)
	assert.Equal(t, result.KmsConfig.UUID, datastorePool.KmsConfig.UUID)
}

func TestConvertDatastorePoolToModel_WithAssetMetadata(t *testing.T) {
	t.Run("WhenPoolHasAssetMetadata", func(tt *testing.T) {
		datastorePool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid-with-assets",
			},
			Name:         "Test Pool With Assets",
			Description:  "Test Description With Assets",
			SizeInBytes:  1024,
			State:        "active",
			StateDetails: "running",
			Network:      "test-network",
			ServiceLevel: "FLEX",
			AssetMetadata: &datamodel.AssetMetadata{
				ChildAssets: []datamodel.ChildAsset{
					{
						AssetType:  "compute",
						AssetNames: []string{"instance-1", "instance-2"},
					},
					{
						AssetType:  "storage",
						AssetNames: []string{"bucket-1"},
					},
				},
			},
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 64,
				Iops:            1024,
				PrimaryZone:     "us-central1-a",
				SecondaryZone:   "us-central1-b",
			},
		}
		accountName := "test-account"

		dbPoolView := database.ConvertPoolToPoolView(datastorePool)
		result := commonfactory.ConvertDatastorePoolToModel(dbPoolView, accountName)

		assert.NotNil(t, result.AssetMetadata)
		assert.Len(t, result.AssetMetadata.ChildAssets, 2)

		// Check first asset
		firstAsset := result.AssetMetadata.ChildAssets[0]
		assert.Equal(t, "compute", firstAsset.AssetType)
		assert.Equal(t, []string{"instance-1", "instance-2"}, firstAsset.AssetNames)

		// Check second asset
		secondAsset := result.AssetMetadata.ChildAssets[1]
		assert.Equal(t, "storage", secondAsset.AssetType)
		assert.Equal(t, []string{"bucket-1"}, secondAsset.AssetNames)
	})

	t.Run("WhenPoolHasEmptyAssetMetadata", func(tt *testing.T) {
		datastorePool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid-empty-assets",
			},
			Name:         "Test Pool Empty Assets",
			Description:  "Test Description Empty Assets",
			SizeInBytes:  2048,
			State:        "active",
			StateDetails: "running",
			Network:      "test-network",
			ServiceLevel: "FLEX",
			AssetMetadata: &datamodel.AssetMetadata{
				ChildAssets: []datamodel.ChildAsset{},
			},
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 128,
				Iops:            2048,
				PrimaryZone:     "us-central1-a",
				SecondaryZone:   "us-central1-b",
			},
		}
		accountName := "test-account"

		dbPoolView := database.ConvertPoolToPoolView(datastorePool)
		result := commonfactory.ConvertDatastorePoolToModel(dbPoolView, accountName)

		assert.NotNil(t, result.AssetMetadata)
		assert.Empty(t, result.AssetMetadata.ChildAssets)
	})

	t.Run("WhenPoolHasSingleChildAsset", func(tt *testing.T) {
		datastorePool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid-single-asset",
			},
			Name:         "Test Pool Single Asset",
			Description:  "Test Description Single Asset",
			SizeInBytes:  4096,
			State:        "active",
			StateDetails: "running",
			Network:      "test-network",
			ServiceLevel: "FLEX",
			AssetMetadata: &datamodel.AssetMetadata{
				ChildAssets: []datamodel.ChildAsset{
					{
						AssetType:  "database",
						AssetNames: []string{"db-1", "db-2", "db-3"},
					},
				},
			},
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 256,
				Iops:            4096,
				PrimaryZone:     "us-central1-a",
				SecondaryZone:   "us-central1-b",
			},
		}
		accountName := "test-account"

		dbPoolView := database.ConvertPoolToPoolView(datastorePool)
		result := commonfactory.ConvertDatastorePoolToModel(dbPoolView, accountName)

		assert.NotNil(t, result.AssetMetadata)
		assert.Len(t, result.AssetMetadata.ChildAssets, 1)

		// Check single asset
		asset := result.AssetMetadata.ChildAssets[0]
		assert.Equal(t, "database", asset.AssetType)
		assert.Equal(t, []string{"db-1", "db-2", "db-3"}, asset.AssetNames)
	})

	t.Run("WhenPoolHasNilAssetMetadata", func(tt *testing.T) {
		datastorePool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid-nil-assets",
			},
			Name:          "Test Pool Nil Assets",
			Description:   "Test Description Nil Assets",
			SizeInBytes:   8192,
			State:         "active",
			StateDetails:  "running",
			Network:       "test-network",
			ServiceLevel:  "FLEX",
			AssetMetadata: nil, // This is the key test case
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 512,
				Iops:            8192,
				PrimaryZone:     "us-central1-a",
				SecondaryZone:   "us-central1-b",
			},
		}
		accountName := "test-account"

		dbPoolView := database.ConvertPoolToPoolView(datastorePool)
		result := commonfactory.ConvertDatastorePoolToModel(dbPoolView, accountName)

		assert.Nil(t, result.AssetMetadata)
	})
}

func TestConvertDatastorePoolToModel_WithActiveDirectory(t *testing.T) {
	t.Run("WhenPoolHasActiveDirectory", func(tt *testing.T) {
		datastorePool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			Name:             "Test Pool",
			Description:      "Test Description",
			SizeInBytes:      1024,
			State:            "active",
			StateDetails:     "running",
			AllowAutoTiering: true,
			Network:          "test-network",
			ServiceLevel:     "FLEX",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 64,
				Iops:            1024,
				PrimaryZone:     "us-central1-a",
				SecondaryZone:   "",
			},
			ActiveDirectoryID: sql.NullInt64{Valid: true, Int64: 42},
			ActiveDirectory: &datamodel.ActiveDirectory{
				BaseModel: datamodel.BaseModel{
					UUID: "ad-uuid",
				},
				AdName: "test-ad",
			},
		}
		accountName := "test-account"

		dbPoolView := database.ConvertPoolToPoolView(datastorePool)
		result := commonfactory.ConvertDatastorePoolToModel(dbPoolView, accountName)

		assert.Equal(t, "test-uuid", result.UUID)
		assert.Equal(t, accountName, result.AccountName)
		assert.Equal(t, "Test Pool", result.Name)
		assert.Equal(t, "Test Description", result.Description)
		assert.Equal(t, uint64(1024), result.SizeInBytes)
		assert.Equal(t, "active", result.State)
		assert.Equal(t, "running", result.StateDetails)
		assert.Equal(t, true, result.AllowAutoTiering)

		// Check ActiveDirectory fields
		assert.Equal(t, "ad-uuid", result.ActiveDirectoryConfigId)
		assert.Equal(t, "test-ad", result.ActiveDirectoryResourceId)
	})

	t.Run("WhenPoolHasNoActiveDirectory", func(tt *testing.T) {
		datastorePool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			Name:             "Test Pool",
			Description:      "Test Description",
			SizeInBytes:      1024,
			State:            "active",
			StateDetails:     "running",
			AllowAutoTiering: true,
			Network:          "test-network",
			ServiceLevel:     "FLEX",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 64,
				Iops:            1024,
				PrimaryZone:     "us-central1-a",
				SecondaryZone:   "",
			},
			ActiveDirectoryID: sql.NullInt64{Valid: false, Int64: 0},
			ActiveDirectory:   nil,
		}
		accountName := "test-account"

		dbPoolView := database.ConvertPoolToPoolView(datastorePool)
		result := commonfactory.ConvertDatastorePoolToModel(dbPoolView, accountName)

		assert.Equal(t, "test-uuid", result.UUID)
		assert.Equal(t, accountName, result.AccountName)
		assert.Equal(t, "Test Pool", result.Name)
		assert.Equal(t, "Test Description", result.Description)
		assert.Equal(t, uint64(1024), result.SizeInBytes)
		assert.Equal(t, "active", result.State)
		assert.Equal(t, "running", result.StateDetails)
		assert.Equal(t, true, result.AllowAutoTiering)

		// Check ActiveDirectory fields are empty
		assert.Empty(t, result.ActiveDirectoryConfigId)
		assert.Empty(t, result.ActiveDirectoryResourceId)
	})
}

func TestCreatePool(t *testing.T) {
	t.Run("WhenGetOrCreateAccountFails", func(tt *testing.T) {
		ctx, se, _, temporal := setup(tt)
		params := &common.CreatePoolParams{
			AccountName:      "test_account",
			Region:           "test_region",
			PrimaryZone:      "test_zone",
			SecondaryZone:    "",
			Name:             "test_pool",
			VendorID:         "test_vendor",
			SizeInBytes:      1024,
			AllowAutoTiering: true,
			VendorSubNetID:   "test_network",
			CustomPerformanceParams: func() *common.CustomPerformanceParams {
				iopsValue := int64(1024)
				return &common.CustomPerformanceParams{
					Enabled:         true,
					ThroughputMibps: 64,
					Iops:            &iopsValue,
				}
			}(),
		}
		originalnodePassword := env.NodePassword
		env.NodePassword = "password"
		defer func() {
			env.NodePassword = originalnodePassword
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		_, _, err := createPool(ctx, se, temporal, params)
		assert.EqualError(tt, err, "account not found")
	})
	t.Run("WhenValidatePoolParamFails", func(tt *testing.T) {
		ctx, se, _, temporal := setup(tt)
		iopsValue := int64(1024)
		params := &common.CreatePoolParams{
			AccountName:      "test_account",
			Region:           "test_region",
			PrimaryZone:      "test_zone",
			SecondaryZone:    "",
			Name:             "test_pool",
			VendorID:         "test_vendor",
			SizeInBytes:      1024,
			AllowAutoTiering: true,
			VendorSubNetID:   "test_network",
			CustomPerformanceParams: &common.CustomPerformanceParams{
				Enabled:         true,
				ThroughputMibps: 64,
				Iops:            &iopsValue, // common.CustomPerformanceParams expects *int64
			},
		}

		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
			},
			Name: "test_account",
		}
		originalnodePassword := env.NodePassword
		env.NodePassword = "password"
		defer func() {
			env.NodePassword = originalnodePassword
			ValidateCreatePoolParams = _validateCreatePoolParams
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}
		ValidateCreatePoolParams = func(params *common.CreatePoolParams, logger log.Logger) error {
			return errors.New("invalid pool params")
		}

		pool, _, err := createPool(ctx, se, temporal, params)
		assert.EqualError(tt, err, "invalid pool params")
		assert.Nil(tt, pool, "Expected nil, got %v", pool)
	})
	t.Run("WhenCreatePoolFails", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		params := &common.CreatePoolParams{
			AccountName:      "test_account",
			Region:           "test_region",
			PrimaryZone:      "test_zone1",
			SecondaryZone:    "test_zone2",
			Name:             "test_pool_1",
			VendorID:         "test-vendor-id", // Changed to match the vendor_id in createDBPools
			SizeInBytes:      1024,
			AllowAutoTiering: true,
			VendorSubNetID:   "test_network",
			CustomPerformanceParams: func() *common.CustomPerformanceParams {
				iopsValue := int64(1024)
				return &common.CustomPerformanceParams{
					Enabled:         true,
					ThroughputMibps: 64,
					Iops:            &iopsValue,
				}
			}(),
		}

		_, account := createDBPools(t, store)

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		ValidateCreatePoolParams = func(params *common.CreatePoolParams, logger log.Logger) error {
			return nil
		}

		_, _, err := createPool(ctx, store, temporal, params)
		assert.EqualError(tt, err.(*vsaerrors.CustomError).OriginalErr, "pool already exists")
		assert.EqualError(tt, err, "Invalid input parameters provided")
	})
	t.Run("WhenCreatePoolSucceeds", func(tt *testing.T) {
		ctx, _, orch, temporal := setup(tt)
		label := "label"
		labels := make(datamodel.JSONB)
		labels["test"] = label

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		params := &common.CreatePoolParams{
			AccountName:      "test_account",
			Region:           "test_region",
			PrimaryZone:      "test_zone",
			SecondaryZone:    "",
			Name:             "test_pool",
			VendorID:         "test_vendor",
			SizeInBytes:      1024,
			AllowAutoTiering: true,
			VendorSubNetID:   "test_network",
			CustomPerformanceParams: func() *common.CustomPerformanceParams {
				iopsValue := int64(1024)
				return &common.CustomPerformanceParams{
					Enabled:         true,
					ThroughputMibps: 64,
					Iops:            &iopsValue,
				}
			}(),
			Labels: &labels,
			Mode:   common.ONTAPMode,
		}

		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
			},
			Name: "test_account",
		}
		originalnodePassword := env.NodePassword
		env.NodePassword = "password"

		authType := env.AuthType
		env.AuthType = env.USERNAME_PWD
		defer func() {
			env.AuthType = authType // Reset to original value after test
			env.NodePassword = originalnodePassword
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}
		ValidateCreatePoolParams = func(params *common.CreatePoolParams, logger log.Logger) error {
			return nil
		}

		pool, _, err := orch.CreatePool(ctx, params)
		if err != nil {
			t.Errorf("Expected nil, got error")
		}
		assert.Equal(t, pool.Name, params.Name)
		assert.Equal(t, pool.VendorSubNetID, params.VendorSubNetID)
		assert.Equal(t, pool.AccountName, params.AccountName)
		assert.Equal(t, pool.PoolAttributes.Labels["test"], label)
	})
	t.Run("WhenCreatePoolSucceeds_LargeCapacity_UsesLVWorkflowTimeout", func(tt *testing.T) {
		ctx, _, orch, temporal := setup(tt)

		temporal.EXPECT().ExecuteWorkflow(
			mock.Anything,
			mock.MatchedBy(func(opts interface{}) bool {
				v := reflect.ValueOf(opts)
				f := v.FieldByName("WorkflowRunTimeout")
				if !f.IsValid() {
					return false
				}
				timeout, ok := f.Interface().(time.Duration)
				if !ok {
					return false
				}
				want := workflowengine.GetCreatePoolWorkflowRunTimeout(true)
				return want != nil && timeout == *want
			}),
			mock.Anything,
			mock.Anything,
			mock.Anything,
		).Return(nil, nil)

		params := &common.CreatePoolParams{
			AccountName:      "test_account",
			Region:           "test_region",
			PrimaryZone:      "test_zone",
			Name:             "test_pool_lv",
			VendorID:         "test_vendor",
			SizeInBytes:      1024,
			AllowAutoTiering: true,
			VendorSubNetID:   "test_network",
			LargeCapacity:    true,
			CustomPerformanceParams: func() *common.CustomPerformanceParams {
				iopsValue := int64(1024)
				return &common.CustomPerformanceParams{
					Enabled:         true,
					ThroughputMibps: 64,
					Iops:            &iopsValue,
				}
			}(),
			Mode: common.ONTAPMode,
		}

		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
			},
			Name: "test_account",
		}
		originalnodePassword := env.NodePassword
		env.NodePassword = "password"

		authType := env.AuthType
		env.AuthType = env.USERNAME_PWD
		defer func() {
			env.AuthType = authType
			env.NodePassword = originalnodePassword
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}
		ValidateCreatePoolParams = func(params *common.CreatePoolParams, logger log.Logger) error {
			return nil
		}

		_, _, err := orch.CreatePool(ctx, params)
		assert.NoError(tt, err)
	})
	t.Run("WhenCreatePoolSucceedsWithCert", func(tt *testing.T) {
		ctx, _, orch, temporal := setup(tt)
		mockLogger := log.NewLogger()
		// Create a PersistenceStore instance with the in-memory database
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			t.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		params := &common.CreatePoolParams{
			AccountName:      "test_account",
			Region:           "test_region",
			PrimaryZone:      "test_zone",
			SecondaryZone:    "",
			Name:             "test_pool",
			VendorID:         "test_vendor",
			SizeInBytes:      1024,
			AllowAutoTiering: true,
			VendorSubNetID:   "test_network",
			CustomPerformanceParams: func() *common.CustomPerformanceParams {
				iopsValue := int64(1024)
				return &common.CustomPerformanceParams{
					Enabled:         true,
					ThroughputMibps: 64,
					Iops:            &iopsValue,
				}
			}(),
			Mode: common.ONTAPMode,
		}

		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
			},
			Name: "test_account",
		}
		authType := env.AuthType
		env.AuthType = env.USER_CERTIFICATE
		defer func() {
			env.AuthType = authType // Reset to original value after test
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}
		ValidateCreatePoolParams = func(params *common.CreatePoolParams, logger log.Logger) error {
			return nil
		}

		pool, _, err := orch.CreatePool(ctx, params)
		if err != nil {
			t.Errorf("Expected nil, got error")
		}
		assert.Equal(t, pool.Name, params.Name)
		assert.Equal(t, pool.VendorSubNetID, params.VendorSubNetID)
		assert.Equal(t, pool.AccountName, params.AccountName)
	})
	t.Run("WhenCreatePoolSucceedsWithPasswordInSecretManager", func(tt *testing.T) {
		ctx, _, orch, temporal := setup(tt)
		mockLogger := log.NewLogger()
		// Create a PersistenceStore instance with the in-memory database
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			t.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		params := &common.CreatePoolParams{
			AccountName:      "test_account",
			Region:           "test_region",
			PrimaryZone:      "test_zone",
			SecondaryZone:    "",
			Name:             "test-pool-uuid",
			VendorID:         "test_vendor",
			SizeInBytes:      1024,
			AllowAutoTiering: true,
			VendorSubNetID:   "test_network",
			CustomPerformanceParams: func() *common.CustomPerformanceParams {
				iopsValue := int64(1024)
				return &common.CustomPerformanceParams{
					Enabled:         true,
					ThroughputMibps: 64,
					Iops:            &iopsValue,
				}
			}(),
			Mode: common.ONTAPMode,
		}

		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
			},
			Name: "test_account",
		}
		authType := env.AuthType
		env.AuthType = env.USERNAME_PWD_SEC_MGR
		defer func() {
			env.AuthType = authType // Reset to original value after test
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}
		ValidateCreatePoolParams = func(params *common.CreatePoolParams, logger log.Logger) error {
			return nil
		}

		pool, _, err := orch.CreatePool(ctx, params)
		if err != nil {
			t.Errorf("Expected nil, got error")
		}
		assert.Equal(t, pool.Name, params.Name)
		assert.Equal(t, pool.VendorSubNetID, params.VendorSubNetID)
		assert.Equal(t, pool.AccountName, params.AccountName)
	})
	t.Run("WhenUpdateKmsConfigStateFails", func(tt *testing.T) {
		ctx, store, orch, temporal := setup(tt)
		mockStorage := new(database.MockStorage)
		orch.storage = mockStorage
		iops := int64(1024)
		params := &common.CreatePoolParams{
			AccountName:      "test_account",
			Region:           "test_region",
			PrimaryZone:      "test_zone1",
			SecondaryZone:    "test_zone2",
			Name:             "test_pool_1",
			VendorID:         "test_vendor",
			SizeInBytes:      1024,
			AllowAutoTiering: true,
			VendorSubNetID:   "test_network",
			CustomPerformanceParams: &common.CustomPerformanceParams{
				Enabled:         true,
				ThroughputMibps: 64,
				Iops:            &iops,
			},
			KmsConfig: &models.KmsConfig{State: datamodel.LifeCycleStateInUse},
		}

		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
			},
			Name: "test_account",
		}
		originalnodePassword := env.NodePassword
		env.NodePassword = "password"

		authType := env.AuthType
		env.AuthType = env.USERNAME_PWD
		defer func() {
			env.AuthType = authType // Reset to original value after test
			env.NodePassword = originalnodePassword
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}

		mockStorage.On("UpdateKmsConfigState", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("some error")).Once()
		ValidateCreatePoolParams = func(params *common.CreatePoolParams, logger log.Logger) error {
			return nil
		}

		_, _, err := createPool(ctx, store, temporal, params)
		assert.Error(tt, err)
	})
}

func TestUpdatePool_ActiveDirectoryConfigId(t *testing.T) {
	t.Run("WhenActiveDirectoryConfigIdIsValid", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		_, account := createDBPools(tt, store)

		// Create Active Directory
		ad := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "550e8400-e29b-41d4-a716-446655440000",
			},
			AdName:    "test-active-directory",
			AccountId: account.ID,
		}
		err := store.DB().Create(ad).Error
		assert.NoError(tt, err)

		params := &common.UpdatePoolParams{
			AccountName:             "test_account",
			PoolId:                  "test-pool-uuid1",
			ActiveDirectoryConfigId: "550e8400-e29b-41d4-a716-446655440000",
			IfADExistsInVCP:         true,
			SizeInBytes:             uint64(2 * utils.TiBInBytes), // Set a valid size
			QosType:                 "auto",                       // Set a valid QOS type
			TotalThroughputMibps:    128,                          // Set a valid throughput
		}

		originalGetAccountWithName := getAccountWithName
		originalValidateAndSetUpdatePoolParams := ValidateAndSetUpdatePoolParams

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		ValidateAndSetUpdatePoolParams = func(params *common.UpdatePoolParams, pool *datamodel.Pool) error {
			return nil
		}

		// Mock workflow execution
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, params, mock.Anything, mock.Anything).
			Return(nil, nil)

		defer func() {
			getAccountWithName = originalGetAccountWithName
			ValidateAndSetUpdatePoolParams = originalValidateAndSetUpdatePoolParams
		}()

		pool, _, err := _updatePool(ctx, store, temporal, params)
		assert.NoError(tt, err, "Expected no error on updating pool with valid ActiveDirectoryConfigId")
		assert.Equal(tt, "test-pool-uuid1", pool.UUID)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, pool.State)

		// Verify the ActiveDirectoryID was set in the database
		var updatedPool datamodel.Pool
		err = store.DB().First(&updatedPool, "uuid = ?", "test-pool-uuid1").Error
		assert.NoError(tt, err)
		assert.Equal(tt, ad.ID, updatedPool.ActiveDirectoryID.Int64)
	})

	t.Run("WhenPOPoolRejectsActiveDirectoryConfigId", func(tt *testing.T) {
		ctx, store, _, _ := setup(tt)
		_, account := createDBPools(tt, store)

		err := store.DB().Model(&datamodel.Pool{}).
			Where("uuid = ?", "test-pool-uuid1").
			Update("service_level", common.ServiceLevelExtreme).Error
		assert.NoError(tt, err)

		params := &common.UpdatePoolParams{
			AccountName:             "test_account",
			PoolId:                  "test-pool-uuid1",
			ActiveDirectoryConfigId: "550e8400-e29b-41d4-a716-446655440000",
			IfADExistsInVCP:         true,
			SizeInBytes:             uint64(2 * utils.TiBInBytes),
			QosType:                 "auto",
			TotalThroughputMibps:    128,
		}

		originalGetAccountWithName := getAccountWithName
		originalValidateAndSetUpdatePoolParams := ValidateAndSetUpdatePoolParams

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		ValidateAndSetUpdatePoolParams = func(params *common.UpdatePoolParams, pool *datamodel.Pool) error {
			return nil
		}

		defer func() {
			getAccountWithName = originalGetAccountWithName
			ValidateAndSetUpdatePoolParams = originalValidateAndSetUpdatePoolParams
		}()

		_, _, err = _updatePool(ctx, store, nil, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), common.POValidationActiveDirectoryMsg)
	})

	t.Run("WhenActiveDirectoryConfigIdNotFound", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		_, account := createDBPools(tt, store)

		params := &common.UpdatePoolParams{
			AccountName:             "test_account",
			PoolId:                  "test-pool-uuid1",
			ActiveDirectoryConfigId: "non-existent-ad-uuid",
			IfADExistsInVCP:         true,
			SizeInBytes:             uint64(2 * utils.TiBInBytes), // Set a valid size
			QosType:                 "auto",                       // Set a valid QOS type
			TotalThroughputMibps:    128,                          // Set a valid throughput
		}

		originalGetAccountWithName := getAccountWithName

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		_, _, err := _updatePool(ctx, store, temporal, params)
		assert.Error(tt, err, "Expected error when ActiveDirectoryConfigId not found")
		// The error occurs during validation, so it should be the validation error message
		assert.Contains(tt, err.Error(), "Active Directory Config with ID")
	})

	t.Run("WhenActiveDirectoryConfigIdAlreadyAssociated", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(tt, store)

		// Create Active Directory
		ad := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "550e8400-e29b-41d4-a716-446655440000",
			},
			AdName:    "test-active-directory",
			AccountId: account.ID,
		}
		err := store.DB().Create(ad).Error
		assert.NoError(tt, err)

		// Associate AD with first pool
		pools[0].ActiveDirectoryID = sql.NullInt64{Valid: true, Int64: ad.ID}
		err = store.DB().Save(pools[0]).Error
		assert.NoError(tt, err)

		// Create another Active Directory
		ad2 := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "550e8400-e29b-41d4-a716-446655440001",
			},
			AdName:    "test-active-directory-2",
			AccountId: account.ID,
		}
		err = store.DB().Create(ad2).Error
		assert.NoError(tt, err)

		params := &common.UpdatePoolParams{
			AccountName:             "test_account",
			PoolId:                  "test-pool-uuid1",
			ActiveDirectoryConfigId: "550e8400-e29b-41d4-a716-446655440001", // Different AD
			IfADExistsInVCP:         true,
			SizeInBytes:             uint64(2 * utils.TiBInBytes), // Set a valid size
			QosType:                 "auto",                       // Set a valid QOS type
			TotalThroughputMibps:    128,                          // Set a valid throughput
		}

		originalGetAccountWithName := getAccountWithName

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		_, _, err = _updatePool(ctx, store, temporal, params)
		assert.Error(tt, err, "Expected error when trying to change ActiveDirectory configuration")
		assert.Contains(tt, err.Error(), "Active Directory configuration cannot be changed")
	})

	t.Run("WhenActiveDirectoryConfigIdIsEmpty", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		_, account := createDBPools(tt, store)

		params := &common.UpdatePoolParams{
			AccountName: "test_account",
			PoolId:      "test-pool-uuid1",
			// ActiveDirectoryConfigId is empty
			SizeInBytes:          uint64(2 * utils.TiBInBytes), // Set a valid size
			QosType:              "auto",                       // Set a valid QOS type
			TotalThroughputMibps: 128,                          // Set a valid throughput
		}

		originalGetAccountWithName := getAccountWithName
		originalValidateAndSetUpdatePoolParams := ValidateAndSetUpdatePoolParams

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		ValidateAndSetUpdatePoolParams = func(params *common.UpdatePoolParams, pool *datamodel.Pool) error {
			return nil
		}

		// Mock workflow execution
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, params, mock.Anything, mock.Anything).
			Return(nil, nil)

		defer func() {
			getAccountWithName = originalGetAccountWithName
			ValidateAndSetUpdatePoolParams = originalValidateAndSetUpdatePoolParams
		}()

		pool, _, err := _updatePool(ctx, store, temporal, params)
		assert.NoError(tt, err, "Expected no error when ActiveDirectoryConfigId is empty")
		assert.Equal(tt, "test-pool-uuid1", pool.UUID)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, pool.State)
	})

	t.Run("WhenActiveDirectoryConfigIdProvidedButNotCheckedInVCP", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		_, account := createDBPools(tt, store)

		params := &common.UpdatePoolParams{
			AccountName:             "test_account",
			PoolId:                  "test-pool-uuid1",
			ActiveDirectoryConfigId: "non-existent-ad-uuid",
			IfADExistsInVCP:         false, // should skip VCP lookup
			SizeInBytes:             uint64(2 * utils.TiBInBytes),
			QosType:                 "auto",
			TotalThroughputMibps:    128,
		}

		originalGetAccountWithName := getAccountWithName
		originalValidateAndSetUpdatePoolParams := ValidateAndSetUpdatePoolParams

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		ValidateAndSetUpdatePoolParams = func(params *common.UpdatePoolParams, pool *datamodel.Pool) error {
			return nil
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, params, mock.Anything, mock.Anything).
			Return(nil, nil)

		defer func() {
			getAccountWithName = originalGetAccountWithName
			ValidateAndSetUpdatePoolParams = originalValidateAndSetUpdatePoolParams
		}()

		pool, _, err := _updatePool(ctx, store, temporal, params)
		assert.NoError(tt, err, "Expected no error when AD check is skipped")
		assert.Equal(tt, "test-pool-uuid1", pool.UUID)
	})
}

func TestUpdatePool(t *testing.T) {
	t.Run("WhenGetOrCreateAccountFails", func(tt *testing.T) {
		ctx, se, _, temporal := setup(tt)
		params := &common.UpdatePoolParams{
			AccountName: "test_account",
			PoolId:      "test-pool-id",
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		_, _, err := _updatePool(ctx, se, temporal, params)
		assert.EqualError(tt, err, "account not found")
	})
	t.Run("WhenPoolNotFound", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)

		params := &common.UpdatePoolParams{
			AccountName: "test_account",
			PoolId:      "non-existent-pool",
		}

		// Return a valid account
		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-uuid", ID: 1},
			Name:      "test_account",
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		_, _, err := _updatePool(ctx, store, temporal, params)
		assert.Contains(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "pool not found")
	})
	t.Run("WhenValidatePoolParamsFails", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		params := &common.UpdatePoolParams{
			AccountName: "test_account",
			PoolId:      "test-pool-uuid1",
		}

		_, account := createDBPools(t, store)

		originalGetAccountWithName := getAccountWithName
		originalValidateAndSetUpdatePoolParams := ValidateAndSetUpdatePoolParams

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		ValidateAndSetUpdatePoolParams = func(params *common.UpdatePoolParams, pool *datamodel.Pool) error {
			return errors.New("invalid pool params")
		}

		defer func() {
			getAccountWithName = originalGetAccountWithName
			ValidateAndSetUpdatePoolParams = originalValidateAndSetUpdatePoolParams
		}()

		_, _, err := _updatePool(ctx, store, temporal, params)
		assert.EqualError(tt, err, "invalid pool params")
	})
	t.Run("WhenUpdatePoolSucceeds", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		params := &common.UpdatePoolParams{
			AccountName:          "test_account",
			PoolId:               "test-pool-uuid1",
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		pools, account := createDBPools(t, store)

		originalGetAccountWithName := getAccountWithName
		originalValidateAndSetUpdatePoolParams := ValidateAndSetUpdatePoolParams

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		ValidateAndSetUpdatePoolParams = func(params *common.UpdatePoolParams, pool *datamodel.Pool) error {
			return nil
		}

		defer func() {
			getAccountWithName = originalGetAccountWithName
			ValidateAndSetUpdatePoolParams = originalValidateAndSetUpdatePoolParams
		}()

		pool, _, err := _updatePool(ctx, store, temporal, params)
		assert.NoError(tt, err, "Expected no error on updating pool")
		assert.Equal(tt, pools[0].Name, pool.Name)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, pool.State)
	})
	t.Run("WhenUpdatePoolSucceeds_LargeCapacity_UsesLVWorkflowTimeout", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)

		// Create an LV pool in the database
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 100, UUID: "test-account-uuid-lv"},
			Name:      "test_account_lv",
		}
		err := store.DB().Create(account).Error
		assert.NoError(tt, err)

		lvPool := &datamodel.Pool{
			BaseModel:     datamodel.BaseModel{UUID: "test-pool-uuid-lv"},
			Name:          "test_pool_lv",
			AccountID:     account.ID,
			VendorID:      "test-vendor-id-lv",
			LargeCapacity: true,
			State:         datamodel.LifeCycleStateREADY,
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 64,
				Iops:            1024,
				PrimaryZone:     "us-central1-a",
			},
			DeploymentName: "dep-lv",
		}
		err = store.DB().Create(lvPool).Error
		assert.NoError(tt, err)

		params := &common.UpdatePoolParams{
			AccountName:          "test_account_lv",
			PoolId:               "test-pool-uuid-lv",
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(true),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		temporal.EXPECT().ExecuteWorkflow(
			mock.Anything,
			mock.MatchedBy(func(opts interface{}) bool {
				v := reflect.ValueOf(opts)
				f := v.FieldByName("WorkflowRunTimeout")
				if !f.IsValid() {
					return false
				}
				timeout, ok := f.Interface().(time.Duration)
				if !ok {
					return false
				}
				want := workflowengine.GetUpdatePoolWorkflowRunTimeout(true)
				return want != nil && timeout == *want
			}),
			mock.Anything,
			mock.Anything,
			mock.Anything,
			mock.Anything,
		).Return(nil, nil)

		originalGetAccountWithName := getAccountWithName
		originalValidateAndSetUpdatePoolParams := ValidateAndSetUpdatePoolParams

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		ValidateAndSetUpdatePoolParams = func(params *common.UpdatePoolParams, pool *datamodel.Pool) error {
			return nil
		}

		defer func() {
			getAccountWithName = originalGetAccountWithName
			ValidateAndSetUpdatePoolParams = originalValidateAndSetUpdatePoolParams
		}()

		pool, _, err := _updatePool(ctx, store, temporal, params)
		assert.NoError(tt, err, "Expected no error on updating LV pool")
		assert.Equal(tt, lvPool.Name, pool.Name)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, pool.State)
	})
	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		params := &common.UpdatePoolParams{
			AccountName:          "test_account",
			PoolId:               "test-pool-uuid1",
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		_, account := createDBPools(t, store)

		originalGetAccountWithName := getAccountWithName
		originalValidateAndSetUpdatePoolParams := ValidateAndSetUpdatePoolParams

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		ValidateAndSetUpdatePoolParams = func(params *common.UpdatePoolParams, pool *datamodel.Pool) error {
			return nil
		}

		defer func() {
			getAccountWithName = originalGetAccountWithName
			ValidateAndSetUpdatePoolParams = originalValidateAndSetUpdatePoolParams
		}()

		// Fail workflow execution.
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, params, mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("workflow error"))

		_, _, err := _updatePool(ctx, store, temporal, params)
		assert.EqualError(tt, err, "workflow error")
	})
}

func TestGetPool(t *testing.T) {
	t.Run("WhenPoolDoesNotExist", func(tt *testing.T) {
		ctx, _, orch, _ := setup(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test_account"}, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		_, err := orch.DescribePool(ctx, "non-existent-uuid", "")
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), "pool not found")
		} else {
			tt.Errorf("Expected custom error, got %v", err)
		}
	})

	t.Run("WhenPoolExists", func(tt *testing.T) {
		ctx, store, orch, _ := setup(tt)

		pools, account := createDBPools(t, store)

		result, err := orch.DescribePool(ctx, "test-pool-uuid1", "test_account")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, pools[0].Name, result.Name)
		assert.Equal(tt, account.Name, result.AccountName)
	})
}

func TestGetPoolByVendorID(t *testing.T) {
	t.Run("WhenPoolDoesNotExist", func(tt *testing.T) {
		ctx, _, orch, _ := setup(tt)

		_, err := orch.GetPoolByVendorID(ctx, "non-existent-vendor-id", "")
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), "pool not found")
		} else {
			tt.Errorf("Expected custom error, got %v", err)
		}
	})

	t.Run("WhenPoolExists", func(tt *testing.T) {
		ctx, store, orch, _ := setup(tt)

		pools, account := createDBPools(t, store)

		result, err := orch.GetPoolByVendorID(ctx, "test-vendor-id", "test_account")
		assert.NoError(tt, err)
		assert.Equal(tt, pools[0].Name, result.Name)
		assert.Equal(tt, account.Name, result.AccountName)
	})
}

// Test the new helper functions
func TestDeletePool(t *testing.T) {
	t.Run("DeletePool_WhenGetAccountWithNameFails_ReturnsError", func(tt *testing.T) {
		ctx, se, _, temporal := setup(tt)

		params := &common.DeletePoolParams{
			AccountName: "test_account",
			PoolID:      "test_pool_id",
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		_, _, err := deletePool(ctx, temporal, se, params)
		assert.EqualError(tt, err, "account not found")
	})
	t.Run("DeletePool_WhenPoolNotFound_ReturnsNotFoundError", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)

		// Clear the in-memory database
		err := database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err)

		params := &common.DeletePoolParams{
			AccountName: "test_account",
			PoolID:      "non_existent_pool_id",
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test_account"}, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		_, _, err = deletePool(ctx, temporal, store, params)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), "pool not found")
		} else {
			tt.Errorf("Expected custom error, got %v", err)
		}
	})
	t.Run("DeletePool_WhenAllConditionsMet_ReturnsSuccess", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, jobID, err := _deletePool(ctx, temporal, store, params)
		assert.NoError(tt, err)
		assert.Equal(tt, pool.Name, result.Name, "Expected pool name to match")
		assert.NotEmpty(tt, jobID)
	})
	t.Run("DeletePool_WhenPoolInCreatingStateWithMatchingCorrelationID_SkipsStateUpdateAndContinues", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]
		correlationID := "test-correlation-id"

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Add correlation ID to context
		ctx = context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)

		// Mock GetPool to return a pool in CREATING state
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
				Name:      pool.Name,
				AccountID: account.ID,
				State:     datamodel.LifeCycleStateCreating,
				Account:   account, // Required for convertDatastorePoolToModel
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone:     "us-central1-a",
					SecondaryZone:   "us-central1-b",
					ThroughputMibps: 64,
					Iops:            1024,
				},
			},
		}, nil)

		// Mock GetJobByResourceUUID for DELETE_POOL (called first in ValidateCorrelationIDForCreatingResource)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeDeletePool)).Return(nil, nil)

		// Mock GetJobByResourceUUID to return a job with matching correlation ID
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeCreatePool)).Return(&datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "job-uuid"},
			CorrelationID: correlationID,
		}, nil)

		// Mock CreateJob
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "delete-job-uuid"},
			WorkflowID: "test-workflow-id",
		}, nil)

		// Mock ExecuteWorkflow
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		_, _, err := _deletePool(ctx, temporal, mockStorage, params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("DeletePool_WhenPoolInCreatingStateWithMismatchedCorrelationID_ReturnsConflictError", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]
		correlationID := "test-correlation-id"

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Add correlation ID to context
		ctx = context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)

		// Mock GetPool to return a pool in CREATING state
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
				Name:      pool.Name,
				AccountID: account.ID,
				State:     datamodel.LifeCycleStateCreating,
			},
		}, nil)

		// Mock GetJobByResourceUUID for DELETE_POOL (called first in ValidateCorrelationIDForCreatingResource)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeDeletePool)).Return(nil, nil)

		// Mock GetJobByResourceUUID to return a job with different correlation ID
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeCreatePool)).Return(&datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "job-uuid"},
			CorrelationID: "different-correlation-id",
		}, nil)

		_, _, err := _deletePool(ctx, temporal, mockStorage, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Error deleting pool - pool is already transitioning between states")
	})
	t.Run("DeletePool_WhenPoolInCreatingStateAndGetJobByResourceUUIDFails_ReturnsConflictError", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Mock GetPool to return a pool in CREATING state
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
				Name:      pool.Name,
				AccountID: account.ID,
				State:     datamodel.LifeCycleStateCreating,
			},
		}, nil)

		// Mock GetJobByResourceUUID for DELETE_POOL (called first in ValidateCorrelationIDForCreatingResource)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeDeletePool)).Return(nil, nil)

		// Mock GetJobByResourceUUID to return an error
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeCreatePool)).Return(nil, errors.New("job not found"))

		_, _, err := _deletePool(ctx, temporal, mockStorage, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Error deleting pool - pool is already transitioning between states")
	})
	t.Run("DeletePool_WhenPoolHasActiveVolumes_ReturnsBadRequestError", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Mock GetPool to return a pool with active volumes (VolumeCount > 0)
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
				Name:      pool.Name,
				AccountID: account.ID,
			},
			VolumeCount: 5, // Has active volumes to trigger line 549
		}, nil)

		_, _, err := _deletePool(ctx, temporal, mockStorage, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsBadRequestErr(err), "Expected BadRequestErr")
		assert.Contains(tt, err.Error(), "pool cannot be deleted with active volumes")
	})
	t.Run("DeletePool_WhenPoolInCreatingStateAndGetJobByResourceUUIDReturnsError_ReturnsConflictError", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Mock GetPool to return a pool in CREATING state
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
				Name:      pool.Name,
				AccountID: account.ID,
				State:     datamodel.LifeCycleStateCreating, // Triggers lines 553-557
			},
		}, nil)

		// Mock GetJobByResourceUUID for DELETE_POOL (called first in ValidateCorrelationIDForCreatingResource)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeDeletePool)).Return(nil, nil)

		// Mock GetJobByResourceUUID to return an error (triggers lines 554-557)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeCreatePool)).Return(nil, errors.New("database error"))

		_, _, err := _deletePool(ctx, temporal, mockStorage, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsConflictErr(err), "Expected ConflictErr")
		assert.Contains(tt, err.Error(), "Error deleting pool - pool is already transitioning between states")
	})
	t.Run("DeletePool_WhenPoolInCreatingStateWithNonEmptyMismatchedCorrelationID_ReturnsConflictError", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]
		correlationID := "test-correlation-id-123"

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Add correlation ID to context using the proper key
		ctx = context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)

		// Mock GetPool to return a pool in CREATING state
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
				Name:      pool.Name,
				AccountID: account.ID,
				State:     datamodel.LifeCycleStateCreating, // Triggers lines 553-566
			},
		}, nil)

		// Mock GetJobByResourceUUID for DELETE_POOL (called first in ValidateCorrelationIDForCreatingResource)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeDeletePool)).Return(nil, nil)

		// Mock GetJobByResourceUUID to return a job with different correlation ID
		// This triggers lines 559-560, 562 (correlationID != "" && createJob.CorrelationID != correlationID)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeCreatePool)).Return(&datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "job-uuid"},
			CorrelationID: "different-correlation-id-456", // Different from context correlation ID
		}, nil)

		_, _, err := _deletePool(ctx, temporal, mockStorage, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsConflictErr(err), "Expected ConflictErr")
		assert.Contains(tt, err.Error(), "Error deleting pool - pool is already transitioning between states")
	})
	t.Run("DeletePool_WhenPoolInCreatingStateWithMatchingCorrelationID_LogsAndSkipsStateUpdate", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]
		correlationID := "matching-correlation-id-789"

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Add correlation ID to context using the proper key
		ctx = context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)

		// Mock GetPool to return a pool in CREATING state
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
				Name:      pool.Name,
				AccountID: account.ID,
				State:     datamodel.LifeCycleStateCreating, // Triggers lines 553-566
				Account:   account,                          // Required for convertDatastorePoolToModel
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone:     "us-central1-a",
					SecondaryZone:   "us-central1-b",
					ThroughputMibps: 64,
					Iops:            1024,
				},
			},
		}, nil)

		// Mock GetJobByResourceUUID for DELETE_POOL (called first in ValidateCorrelationIDForCreatingResource)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeDeletePool)).Return(nil, nil)

		// Mock GetJobByResourceUUID to return a job with matching correlation ID
		// This triggers line 565 (logger.Infof when correlation ID matches)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeCreatePool)).Return(&datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "job-uuid"},
			CorrelationID: correlationID, // Same as context correlation ID
		}, nil)

		// Mock CreateJob
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "delete-job-uuid"},
			WorkflowID: "test-workflow-id",
		}, nil)

		// Mock ExecuteWorkflow
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		_, _, err := _deletePool(ctx, temporal, mockStorage, params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("DeletePool_WhenPoolNotInCreatingStateAndDeletingPoolFails_ReturnsError", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Mock GetPool to return a pool NOT in CREATING state (e.g., READY state)
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
				Name:      pool.Name,
				AccountID: account.ID,
				State:     datamodel.LifeCycleStateREADY, // NOT in CREATING state, triggers line 601-604
			},
			VolumeCount: 0, // No volumes, so delete is allowed
		}, nil)

		// Mock GetJobByResourceUUID for DELETE_POOL (check for existing delete job when in non-transitional state)
		// Since LargeCapacity is not set in the mock, it defaults to false, which means PoolCategoryStandard
		deleteJobType := models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationDelete, models.PoolCategoryStandard)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(deleteJobType)).Return(nil, nil)

		// Mock CreateJob
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "delete-job-uuid"},
			WorkflowID: "test-workflow-id",
		}, nil)

		// Mock DeletingPool to return an error (triggers line 603)
		deletingPoolError := errors.New("failed to mark pool as deleting")
		mockStorage.On("DeletingPool", ctx, mock.Anything).Return(deletingPoolError)

		// Mock UpdateJob - when DeletingPool fails, the defer function will call UpdateJob to mark the job as ERROR
		mockStorage.On("UpdateJob", ctx, "delete-job-uuid", string(datamodel.JobsStateERROR), 0, "failed to mark pool as deleting").Return(nil)

		_, _, err := _deletePool(ctx, temporal, mockStorage, params)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "failed to mark pool as deleting")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DeletePool_WhenPoolInCreatingStateWithExistingDeleteJob_ReturnsExistingJobUUID", func(tt *testing.T) {
		// Test for line 594: When existingDeleteJobUUID is not empty, return it immediately
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]
		correlationID := "test-correlation-id-123"

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Add correlation ID to context
		ctx = context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)

		existingDeleteJob := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "existing-delete-job-uuid"},
			CorrelationID: correlationID,
			Type:          string(datamodel.JobTypeDeletePool),
			State:         string(datamodel.JobsStatePROCESSING),
		}

		// Mock GetPool to return a pool in CREATING state
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
				Name:      pool.Name,
				AccountID: account.ID,
				State:     datamodel.LifeCycleStateCreating,
				Account:   account, // Required for convertDatastorePoolToModel
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone:     "us-central1-a",
					SecondaryZone:   "us-central1-b",
					ThroughputMibps: 64,
					Iops:            1024,
				},
			},
			VolumeCount: 0,
		}, nil)

		// ValidateCorrelationIDForCreatingResource returns existingDeleteJobUUID when delete job is in progress
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeDeletePool)).Return(existingDeleteJob, nil)

		result, jobUUID, err := _deletePool(ctx, temporal, mockStorage, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "existing-delete-job-uuid", jobUUID)
		assert.NotNil(tt, result)
		assert.Equal(tt, pool.UUID, result.UUID)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("DeletePool_WhenLargeCapacityPoolInCreatingStateWithMatchingCorrelationID_UsesCreateLargePoolJobType", func(tt *testing.T) {
		// Test that large capacity pools in CREATING state use CREATE_LARGE_POOL job type
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]
		correlationID := "test-correlation-id-large"

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Add correlation ID to context
		ctx = context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)

		// Mock GetPool to return a large capacity pool in CREATING state
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:     datamodel.BaseModel{UUID: pool.UUID},
				Name:          pool.Name,
				AccountID:     account.ID,
				State:         datamodel.LifeCycleStateCreating,
				LargeCapacity: true,    // Large capacity pool
				Account:       account, // Required for convertDatastorePoolToModel
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone:     "us-central1-a",
					SecondaryZone:   "us-central1-b",
					ThroughputMibps: 64,
					Iops:            1024,
				},
			},
			VolumeCount: 0,
		}, nil)

		// Mock GetJobByResourceUUID for DELETE_LARGE_POOL (called first in ValidateCorrelationIDForCreatingResource)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeDeleteLargePool)).Return(nil, nil)

		// Mock GetJobByResourceUUID to return a CREATE_LARGE_POOL job with matching correlation ID
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeCreateLargePool)).Return(&datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "create-large-pool-job-uuid"},
			CorrelationID: correlationID,
			Type:          string(datamodel.JobTypeCreateLargePool),
		}, nil)

		// Mock CreateJob
		mockStorage.On("CreateJob", ctx, mock.MatchedBy(func(job *datamodel.Job) bool {
			// Verify that the delete job type is DELETE_LARGE_POOL
			return job.Type == string(datamodel.JobTypeDeleteLargePool)
		})).Return(&datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "delete-large-pool-job-uuid"},
			WorkflowID: "test-workflow-id",
		}, nil)

		// Mock ExecuteWorkflow
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		_, _, err := _deletePool(ctx, temporal, mockStorage, params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("DeletePool_WhenLargeCapacityPoolInCreatingStateWithExistingDeleteJob_UsesDeleteLargePoolJobType", func(tt *testing.T) {
		// Test that large capacity pools in CREATING state with existing delete job use DELETE_LARGE_POOL
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]
		correlationID := "test-correlation-id-large-delete"

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Add correlation ID to context
		ctx = context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)

		existingDeleteJob := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "existing-delete-large-pool-job-uuid"},
			CorrelationID: correlationID,
			Type:          string(datamodel.JobTypeDeleteLargePool),
			State:         string(datamodel.JobsStatePROCESSING),
		}

		// Mock GetPool to return a large capacity pool in CREATING state
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:     datamodel.BaseModel{UUID: pool.UUID},
				Name:          pool.Name,
				AccountID:     account.ID,
				State:         datamodel.LifeCycleStateCreating,
				LargeCapacity: true,    // Large capacity pool
				Account:       account, // Required for convertDatastorePoolToModel
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone:     "us-central1-a",
					SecondaryZone:   "us-central1-b",
					ThroughputMibps: 64,
					Iops:            1024,
				},
			},
			VolumeCount: 0,
		}, nil)

		// ValidateCorrelationIDForCreatingResource returns existingDeleteJobUUID when delete job is in progress
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeDeleteLargePool)).Return(existingDeleteJob, nil)

		result, jobUUID, err := _deletePool(ctx, temporal, mockStorage, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "existing-delete-large-pool-job-uuid", jobUUID)
		assert.NotNil(tt, result)
		assert.Equal(tt, pool.UUID, result.UUID)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("DeletePool_WhenStandardPoolInCreatingState_UsesCreatePoolJobType", func(tt *testing.T) {
		// Test that standard pools in CREATING state use CREATE_POOL job type (explicit verification)
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]
		correlationID := "test-correlation-id-standard"

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Add correlation ID to context
		ctx = context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)

		// Mock GetPool to return a standard pool (LargeCapacity=false) in CREATING state
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:     datamodel.BaseModel{UUID: pool.UUID},
				Name:          pool.Name,
				AccountID:     account.ID,
				State:         datamodel.LifeCycleStateCreating,
				LargeCapacity: false,   // Standard pool
				Account:       account, // Required for convertDatastorePoolToModel
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone:     "us-central1-a",
					SecondaryZone:   "us-central1-b",
					ThroughputMibps: 64,
					Iops:            1024,
				},
			},
			VolumeCount: 0,
		}, nil)

		// Mock GetJobByResourceUUID for DELETE_POOL (called first in ValidateCorrelationIDForCreatingResource)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeDeletePool)).Return(nil, nil)

		// Mock GetJobByResourceUUID to return a CREATE_POOL job with matching correlation ID
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(datamodel.JobTypeCreatePool)).Return(&datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "create-pool-job-uuid"},
			CorrelationID: correlationID,
			Type:          string(datamodel.JobTypeCreatePool),
		}, nil)

		// Mock CreateJob - verify it uses DELETE_POOL job type
		mockStorage.On("CreateJob", ctx, mock.MatchedBy(func(job *datamodel.Job) bool {
			// Verify that the delete job type is DELETE_POOL for standard pools
			return job.Type == string(datamodel.JobTypeDeletePool)
		})).Return(&datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "delete-pool-job-uuid"},
			WorkflowID: "test-workflow-id",
		}, nil)

		// Mock ExecuteWorkflow
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		_, _, err := _deletePool(ctx, temporal, mockStorage, params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DeletePool_WhenPoolInTransitionalState_ReturnsConflictError", func(tt *testing.T) {
		// Test for lines 598-599: When pool is in transitional state (not DELETING), return error
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Mock GetPool to return a pool in transitional state (not DELETING)
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
				Name:      pool.Name,
				AccountID: account.ID,
				State:     datamodel.LifeCycleStateUpdating, // Transitional state (not DELETING)
			},
			VolumeCount: 0,
		}, nil)

		// Note: GetJobByResourceUUID is not called when pool is in transitional state
		// because the function returns early at line 599 in pool.go

		_, _, err := _deletePool(ctx, temporal, mockStorage, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "pool is in transition state and cannot be deleted")
		assert.Contains(tt, err.Error(), datamodel.LifeCycleStateUpdating)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DeletePool_WhenPoolInDeletingStateWithExistingJob_ReturnsExistingJobUUID", func(tt *testing.T) {
		// Test for line 604: When existingJobUUID is not empty, return it immediately
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "existing-job-uuid"},
			Type:      string(datamodel.JobTypeDeletePool),
			State:     string(datamodel.JobsStateNEW),
		}

		// Mock GetPool to return a pool in DELETING state
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
				Name:      pool.Name,
				AccountID: account.ID,
				State:     datamodel.LifeCycleStateDeleting,
				Account:   account, // Required for convertDatastorePoolToModel
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone:     "us-central1-a",
					SecondaryZone:   "us-central1-b",
					ThroughputMibps: 64,
					Iops:            1024,
				},
			},
			VolumeCount: 0,
		}, nil)

		// GetExistingDeleteJobForDeletingState returns existingJobUUID when delete job is in progress
		deleteJobType := models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationDelete, models.PoolCategoryStandard)
		mockStorage.On("GetJobByResourceUUID", ctx, pool.UUID, string(deleteJobType)).Return(existingJob, nil)

		result, jobUUID, err := _deletePool(ctx, temporal, mockStorage, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "existing-job-uuid", jobUUID)
		assert.NotNil(tt, result)
		assert.Equal(tt, pool.UUID, result.UUID)
		mockStorage.AssertExpectations(tt)
	})
}

func TestMultiplePools(t *testing.T) {
	t.Run("ReturnsErrorWhenAccountDoesNotExist", func(tt *testing.T) {
		ctx, _, orch, _ := setup(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.NewNotFoundErr("account not found", nil)
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		_, err := orch.GetMultiplePools(ctx, "non-existent-account", []string{"uuid1", "uuid2"})
		if err != nil {
			t.Errorf("Expected nil, got error: %v", err)
		}
	})
	t.Run("ReturnsErrorWhenNoPoolsMatchUUIDs", func(tt *testing.T) {
		ctx, store, orch, _ := setup(tt)
		_, account := createDBPools(t, store)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		pools, err := orch.GetMultiplePools(ctx, account.Name, []string{"non-existent-uuid"})
		assert.NoError(tt, err)
		if len(pools) != 0 {
			tt.Fatalf("Expected 0 pools, got %v", len(pools))
		}
	})
	t.Run("WhenGetMultiplePoolsReturnsError", func(tt *testing.T) {
		accountName := "test_account"
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		orch := &GCPOrchestrator{
			storage: mockStorage,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: accountName}, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage.On("ListPools", ctx, mock.Anything).Return(nil, errors.New("list pools error"))
		_, err := orch.GetMultiplePools(ctx, accountName, []string{"uuid1", "uuid2"})
		assert.EqualError(tt, err, "list pools error")
	})
	t.Run("ReturnsPoolsSuccessfullyWhenUUIDsMatch", func(tt *testing.T) {
		ctx, store, orch, _ := setup(tt)

		dbPools, account := createDBPools(t, store)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		result, err := orch.GetMultiplePools(ctx, account.Name, []string{"test-pool-uuid1", "test-pool-uuid2"})
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		assert.Len(tt, result, 2, "Expected 2 pools, got %d", len(result))
		assert.Equal(tt, dbPools[0].Name, result[0].Name, "Returned pool does not match expected pool")
		assert.Equal(tt, dbPools[1].Name, result[1].Name, "Returned pool does not match expected pool")
	})
	t.Run("WhenPoolsHaveONTAPMode_SetsQuotaInBytesAndVolumeCount", func(tt *testing.T) {
		accountName := "test_account"
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		orch := &GCPOrchestrator{
			storage: mockStorage,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      accountName,
		}

		// Create pools with ONTAP mode (using PoolView since ListPools returns PoolView)
		ontapPool1 := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "ontap-pool-uuid1",
				},
				Name:           "ontap_pool_1",
				AccountID:      account.ID,
				APIAccessMode:  common.ONTAPMode,
				PoolAttributes: &datamodel.PoolAttributes{},
			},
		}
		ontapPool2 := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   2,
					UUID: "ontap-pool-uuid2",
				},
				Name:           "ontap_pool_2",
				AccountID:      account.ID,
				APIAccessMode:  common.ONTAPMode,
				PoolAttributes: &datamodel.PoolAttributes{},
			},
		}
		// Create a pool with different mode (should not call GetExpertModePoolUsedCapacityAndVolumeCount)
		nonOntapPool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   3,
					UUID: "non-ontap-pool-uuid",
				},
				Name:           "non_ontap_pool",
				AccountID:      account.ID,
				APIAccessMode:  common.DEFAULTMode,
				PoolAttributes: &datamodel.PoolAttributes{},
			},
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		expectedSize1 := int64(1099511627776) // 1TB
		expectedCount1 := int64(5)
		expectedSize2 := int64(214748364800) // 200GB
		expectedCount2 := int64(3)

		// Mock ListPools to return the pools
		mockStorage.On("ListPools", ctx, mock.Anything).Return([]*datamodel.PoolView{ontapPool1, ontapPool2, nonOntapPool}, nil)

		// Mock GetExpertModePoolUsedCapacityAndVolumeCount for ONTAP pools only
		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(1)).Return(&database.ExpertModePoolCapacity{TotalSize: expectedSize1, VolumeCount: expectedCount1}, nil).Once()
		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(2)).Return(&database.ExpertModePoolCapacity{TotalSize: expectedSize2, VolumeCount: expectedCount2}, nil).Once()

		result, err := orch.GetMultiplePools(ctx, accountName, []string{"ontap-pool-uuid1", "ontap-pool-uuid2", "non-ontap-pool-uuid"})
		assert.NoError(tt, err)
		assert.Len(tt, result, 3, "Expected 3 pools, got %d", len(result))

		// Find the pools in the result
		var resultOntapPool1, resultOntapPool2, resultNonOntapPool *models.Pool
		for _, pool := range result {
			if pool.UUID == "ontap-pool-uuid1" {
				resultOntapPool1 = pool
			} else if pool.UUID == "ontap-pool-uuid2" {
				resultOntapPool2 = pool
			} else if pool.UUID == "non-ontap-pool-uuid" {
				resultNonOntapPool = pool
			}
		}

		// Verify ONTAP pool 1 has QuotaInBytes and VolumeCount set (mapped to PoolAttributes)
		assert.NotNil(tt, resultOntapPool1, "ONTAP pool 1 should be in result")
		assert.NotNil(tt, resultOntapPool1.PoolAttributes, "PoolAttributes should be set for ONTAP pool 1")
		assert.Equal(tt, float64(expectedSize1), resultOntapPool1.PoolAttributes.AllocatedBytes, "AllocatedBytes should be set for ONTAP pool 1")
		assert.Equal(tt, expectedCount1, resultOntapPool1.PoolAttributes.NumberOfVolumes, "NumberOfVolumes should be set for ONTAP pool 1")

		// Verify ONTAP pool 2 has QuotaInBytes and VolumeCount set (mapped to PoolAttributes)
		assert.NotNil(tt, resultOntapPool2, "ONTAP pool 2 should be in result")
		assert.NotNil(tt, resultOntapPool2.PoolAttributes, "PoolAttributes should be set for ONTAP pool 2")
		assert.Equal(tt, float64(expectedSize2), resultOntapPool2.PoolAttributes.AllocatedBytes, "AllocatedBytes should be set for ONTAP pool 2")
		assert.Equal(tt, expectedCount2, resultOntapPool2.PoolAttributes.NumberOfVolumes, "NumberOfVolumes should be set for ONTAP pool 2")

		// Verify non-ONTAP pool does NOT have QuotaInBytes and VolumeCount set (should be 0/default)
		assert.NotNil(tt, resultNonOntapPool, "Non-ONTAP pool should be in result")
		if resultNonOntapPool.PoolAttributes != nil {
			assert.Equal(tt, float64(0), resultNonOntapPool.PoolAttributes.AllocatedBytes, "AllocatedBytes should not be set for non-ONTAP pool")
			assert.Equal(tt, int64(0), resultNonOntapPool.PoolAttributes.NumberOfVolumes, "NumberOfVolumes should not be set for non-ONTAP pool")
		}

		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenGetExpertModePoolUsedCapacityAndVolumeCountReturnsError_ReturnsError", func(tt *testing.T) {
		accountName := "test_account"
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		orch := &GCPOrchestrator{
			storage: mockStorage,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      accountName,
		}

		ontapPool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "ontap-pool-uuid",
				},
				Name:           "ontap_pool",
				AccountID:      account.ID,
				APIAccessMode:  common.ONTAPMode,
				PoolAttributes: &datamodel.PoolAttributes{},
			},
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		expectedError := errors.New("failed to get expert mode capacity")

		// Mock ListPools to return the pool
		mockStorage.On("ListPools", ctx, mock.Anything).Return([]*datamodel.PoolView{ontapPool}, nil)

		// Mock GetExpertModePoolUsedCapacityAndVolumeCount to return error
		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(1)).Return((*database.ExpertModePoolCapacity)(nil), expectedError).Once()

		result, err := orch.GetMultiplePools(ctx, accountName, []string{"ontap-pool-uuid"})
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.EqualError(tt, err, expectedError.Error())

		mockStorage.AssertExpectations(tt)
	})
}

func TestListPools(t *testing.T) {
	t.Run("ReturnsErrorWhenAccountDoesNotExist", func(tt *testing.T) {
		ctx, _, orch, _ := setup(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.NewNotFoundErr("account not found", nil)
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		_, err := orch.ListPools(ctx, "non-existent-account", false)
		if err == nil {
			tt.Errorf("Expected error, got nil")
		}
		if !errors.IsNotFoundErr(err) {
			tt.Errorf("Expected not found error, got %v", err)
		}
	})
	t.Run("ReturnsEmptyListWhenNoPoolsExist", func(tt *testing.T) {
		ctx, store, orch, _ := setup(tt)
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(tt, err)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		pools, err := orch.ListPools(ctx, account.Name, false)
		assert.NoError(tt, err)
		assert.Empty(tt, pools)
	})
	t.Run("WhenListPoolsReturnsError", func(tt *testing.T) {
		accountName := "test_account"
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		orch := &GCPOrchestrator{
			storage: mockStorage,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: accountName}, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage.On("ListPools", ctx, mock.Anything).Return(nil, errors.New("list pools error"))
		_, err := orch.ListPools(ctx, accountName, false)
		assert.EqualError(tt, err, "list pools error")
	})
	t.Run("ReturnsPoolsSuccessfully", func(tt *testing.T) {
		ctx, store, orch, _ := setup(tt)

		dbPools, account := createDBPools(tt, store)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		pools, err := orch.ListPools(ctx, account.Name, false)
		assert.NoError(tt, err)
		assert.Len(tt, pools, 2, "Expected 2 pools, got %d", len(pools))
		assert.Equal(tt, dbPools[0].Name, pools[0].Name, "Returned pool does not match expected pool")
		assert.Equal(tt, dbPools[1].Name, pools[1].Name, "Returned pool does not match expected pool")
	})
	t.Run("ReturnsPoolsSuccessfullyWithDeleted", func(tt *testing.T) {
		ctx, store, orch, _ := setup(tt)

		dbPools, account := createDBPools(tt, store)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		pools, err := orch.ListPools(ctx, account.Name, true)
		assert.NoError(tt, err)

		assert.Len(tt, pools, 3, "Expected 2 pools, got %d", len(pools))
		assert.Equal(tt, dbPools[0].Name, pools[0].Name, "Returned pool does not match expected pool")
		assert.Equal(tt, dbPools[1].Name, pools[1].Name, "Returned pool does not match expected pool")
		assert.Equal(tt, dbPools[2].Name, pools[2].Name, "Returned pool does not match expected pool")
	})
}

func TestListAllPools(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
	store, err := database.SetupStorageForTest(mockLogger)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}

	err = database.ClearInMemoryDB(store.DB())
	if err != nil {
		t.Fatalf("Failed to clean up test storage: %v", err)
	}

	// Create two pools, one deleted and one not deleted
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.DB().Create(account).Error
	if err != nil {
		t.Fatalf("Failed to create account: %v", err)
	}

	pool1 := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid1"},
		Name:           "test_pool_1",
		AccountID:      account.ID,
		PoolAttributes: &datamodel.PoolAttributes{},
		DeploymentName: "dep1",
	}
	pool2 := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid2", DeletedAt: &gorm.DeletedAt{Time: time.Now(), Valid: true}},
		Name:           "test_pool_2",
		AccountID:      account.ID,
		PoolAttributes: &datamodel.PoolAttributes{},
		DeploymentName: "dep2",
	}
	err = store.DB().Create(pool1).Error
	if err != nil {
		t.Fatalf("Failed to create pool1: %v", err)
	}
	err = store.DB().Create(pool2).Error
	if err != nil {
		t.Fatalf("Failed to create pool2: %v", err)
	}

	orch := GCPOrchestrator{storage: store}

	pools, err := orch.ListAllPools(ctx)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(pools) != 1 {
		t.Errorf("Expected 1 pool (non-deleted), got %d", len(pools))
	}
	if pools[0].Name != pool1.Name {
		t.Errorf("Returned pool does not match expected pool")
	}
}

func TestListAllPools_ErrorFromStorage(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
	mockStorage := new(database.MockStorage)

	// Simulate error from ListPools
	mockStorage.EXPECT().ListPools(ctx, (*utils2.Filter)(nil)).Return(nil, errors.New("db error"))

	orch := GCPOrchestrator{storage: mockStorage}

	pools, err := orch.ListAllPools(ctx)
	if err == nil {
		t.Errorf("Expected error, got nil")
	}
	if pools != nil {
		t.Errorf("Expected nil pools, got %v", pools)
	}
}

func TestGetPoolByName(t *testing.T) {
	queryDepthOne := 1
	queryDepthZero := 0
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		se := database.Storage(nil)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		_, err := GetPoolByName(ctx, se, "test-pool", "test-account", queryDepthOne)
		assert.EqualError(tt, err, "account not found")
	})
	t.Run("WhenGetPoolByNameFails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage.On("GetPoolByName", ctx, mock.Anything).Return(nil, errors.New("pool not found"))
		_, err := GetPoolByName(ctx, mockStorage, "test-pool", "test-account", queryDepthOne)
		assert.EqualError(tt, err, "pool not found")
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		poolResp := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid", ID: 1},
			Name:      "test-pool",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 64,
				Iops:            1024, // datamodel.PoolAttributes expects int64, not pointer
				PrimaryZone:     "us-central1-a",
			},
		}
		poolView := database.ConvertPoolToPoolView(poolResp)
		mockStorage.On("GetPoolByName", ctx, mock.Anything).Return(poolView, nil)

		_, err := GetPoolByName(ctx, mockStorage, "test-pool", "test-account", queryDepthOne)
		assert.NoError(tt, err)
	})
	t.Run("WhenSuccessQueryDepthZero", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		poolResp := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid", ID: 1},
			Name:      "test-pool",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 64,
				Iops:            1024, // datamodel.PoolAttributes expects int64, not pointer
				PrimaryZone:     "us-central1-a",
			},
		}
		nodeResp := []*datamodel.Node{
			{
				BaseModel: datamodel.BaseModel{UUID: "test-node-uuid", ID: 1},
				Name:      "test-node",
			},
		}
		poolView := database.ConvertPoolToPoolView(poolResp)
		mockStorage.On("GetPoolByName", ctx, mock.Anything).Return(poolView, nil)
		mockStorage.On("GetNodesByPoolID", ctx, mock.Anything).Return(nodeResp, nil)

		_, err := GetPoolByName(ctx, mockStorage, "test-pool", "test-account", queryDepthZero)
		assert.NoError(tt, err)
	})
}

func TestConvertDatastorePoolsToModelWithoutAccountNameParam_ReturnsCorrectModels(t *testing.T) {
	poolView1 := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID:        1,
				UUID:      "mock-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
				DeletedAt: nil,
			},
			Name:             "mock-pool",
			Description:      "Mock pool description",
			State:            "ACTIVE",
			StateDetails:     "Mock state details",
			VendorID:         "mock-vendor-id",
			ServiceLevel:     "mock-service-level",
			SizeInBytes:      1024 * 1024 * 1024, // 1 GiB
			UsedBytes:        512 * 1024 * 1024,  // 512 MiB
			Network:          "mock-network",
			AllowAutoTiering: true,
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				HotTierSizeInBytes:      256 * 1024 * 1024, // 256 MiB
				EnableHotTierAutoResize: false,
				BucketName:              "mock-bucket-name",
			},
			AccountID: 1,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "mock-account-uuid",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				Name:         "mock-account",
				Description:  "Mock account description",
				State:        "ACTIVE",
				StateDetails: "Mock account state details",
				Tags:         "mock-tags",
			},
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 100,
				Iops:            1000,
				PrimaryZone:     "mock-primary-zone",
				SecondaryZone:   "mock-secondary-zone",
			},
			ClusterDetails: datamodel.ClusterDetails{
				ExternalName:          "mock-external-name",
				OntapVersion:          "mock-ontap-version",
				RegionalTenantProject: "mock-regional-tenant-project",
				SnHostProject:         "mock-sn-host-project",
				Network:               "mock-cluster-network",
			},
			QosType:          "mock-qos-type",
			ServiceAccountId: "mock-service-account-id",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "mock-password",
				SecretID:      "mock-secret-id",
				CertificateID: "mock-certificate-id",
			},
		},
		Throughput:   100.5,
		QuotaInBytes: 2048 * 1024 * 1024, // 2 GiB
		VolumeCount:  5,
	}
	pools := []*datamodel.PoolView{poolView1}

	result := convertDatastorePoolsToModelWithoutAccountNameParam(pools)

	assert.Len(t, result, 1)
	assert.Equal(t, "mock-pool", result[0].Name)
	assert.Equal(t, "mock-account", result[0].AccountName)
}

// TestCreatePool_WorkflowFailure_JobMarkedAsErrored tests that jobs are marked as errored when workflow fails to start
func TestCreatePool_WorkflowFailure_JobMarkedAsErrored(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflowenginemock.MockTemporalTestClient)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid"},
		Type:      string(datamodel.JobTypeCreatePool),
		State:     string(datamodel.JobsStateNEW),
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		Name:      "test-pool",
		AccountID: account.ID,
	}

	params := &common.CreatePoolParams{
		AccountName:    "test-account",
		Name:           "test-pool",
		VendorID:       "vendor-id",
		VendorSubNetID: "subnet-id",
		SizeInBytes:    2 * utils.TiBInBytes, // 2TiB (minimum)
		ServiceLevel:   ServiceLevelNameFLEX,
		QosType:        utils.QosTypeAuto,
		Region:         "us-central1",
		PrimaryZone:    "us-central1-a",
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 64,
			Iops:            nillable.ToPointer(int64(1024)), // datamodel.PoolAttributes expects int64, not pointer
		},
	}

	// Setup mocks
	mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
	mockStorage.On("CreatingPool", ctx, mock.Anything).Return(pool, nil)

	// Mock workflow execution to fail
	workflowError := errors.New("workflow execution failed")
	mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowError)

	// Mock job update to errored state
	mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed").Return(nil)

	// Mock pool state update to errored state (called by defer function)
	mockStorage.On("DeletePool", ctx, mock.Anything).Return(nil)

	// Execute test
	result, jobID, err := _createPool(ctx, mockStorage, mockTemporal, params)

	// Verify results
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "workflow execution failed", err.Error())

	// Verify job was marked as errored
	mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed")
	mockStorage.AssertCalled(t, "DeletePool", ctx, pool)
}

// TestUpdatePool_WorkflowFailure_JobMarkedAsErrored tests that jobs are marked as errored when workflow fails to start
func TestUpdatePool_WorkflowFailure_JobMarkedAsErrored(t *testing.T) {
	// Use the setup function like other tests do
	ctx, store, _, temporal := setup(t)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	params := &common.UpdatePoolParams{
		PoolId:               "pool-uuid",
		AccountName:          "test-account",
		SizeInBytes:          3 * utils.TiBInBytes, // 3TiB
		TotalThroughputMibps: 128,
		TotalIops:            nillable.ToPointer(int64(2048)),
	}

	// Create a pool in the database
	err := store.DB().Create(account).Error
	assert.NoError(t, err)

	err = store.DB().Create(&datamodel.Pool{
		BaseModel:    datamodel.BaseModel{UUID: "pool-uuid"},
		Name:         "test-pool",
		AccountID:    account.ID,
		State:        datamodel.LifeCycleStateREADY,
		StateDetails: datamodel.LifeCycleStateReadyDetails,
	}).Error
	assert.NoError(t, err)

	// Mock the functions like other working tests
	originalGetAccountWithName := getAccountWithName
	originalValidateUpdatePoolParams := ValidateAndSetUpdatePoolParams

	defer func() {
		getAccountWithName = originalGetAccountWithName
		ValidateAndSetUpdatePoolParams = originalValidateUpdatePoolParams
	}()

	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	ValidateAndSetUpdatePoolParams = func(params *common.UpdatePoolParams, pool *datamodel.Pool) error {
		return nil
	}

	// Mock workflow execution to fail
	workflowError := errors.New("workflow execution failed")
	temporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowError)

	// Execute test
	result, jobID, err := _updatePool(ctx, store, temporal, params)

	// Verify results
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "workflow execution failed", err.Error())

	// Verify job was created and marked as errored
	var jobs []datamodel.Job
	store.DB().Find(&jobs)
	assert.Len(t, jobs, 1)
	assert.Equal(t, string(datamodel.JobsStateERROR), jobs[0].State)

	// Verify pool was also marked as errored
	var pools []datamodel.Pool
	store.DB().Find(&pools)
	assert.Len(t, pools, 1)
	assert.Equal(t, datamodel.LifeCycleStateREADY, pools[0].State)
	assert.Equal(t, datamodel.LifeCycleStateReadyDetails, pools[0].StateDetails)
}

// TestDeletePool_WorkflowFailure_JobMarkedAsErrored tests that jobs are marked as errored when workflow fails to start
func TestDeletePool_WorkflowFailure_JobMarkedAsErrored(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflowenginemock.MockTemporalTestClient)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid"},
		Type:      string(datamodel.JobTypeDeletePool),
		State:     string(datamodel.JobsStateNEW),
	}

	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "pool-uuid"},
			Name:         "test-pool",
			AccountID:    account.ID,
			Account:      account,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		},
		VolumeCount: 0, // No volumes so it can be deleted
	}
	dbpool := database.ConvertPoolViewToPool(poolView)

	params := &common.DeletePoolParams{
		PoolID:      "pool-uuid",
		AccountName: "test-account",
	}

	// Mock getAccountWithName function using helper
	originalGetAccountWithName := getAccountWithName
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountWithName = originalGetAccountWithName }()

	// Setup mocks
	mockStorage.On("GetPool", ctx, "pool-uuid", account.ID).Return(poolView, nil)
	// Mock GetJobByResourceUUID to check for existing delete job (called for non-CREATING, non-DELETING states)
	mockStorage.On("GetJobByResourceUUID", ctx, "pool-uuid", string(datamodel.JobTypeDeletePool)).Return(nil, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
	mockStorage.On("DeletingPool", ctx, mock.Anything).Return(nil)

	// Mock workflow execution to fail
	workflowError := errors.New("workflow execution failed")
	mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowError)

	// Mock job update to errored state
	mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed").Return(nil)

	// Mock pool state update to errored state (called by defer function)
	mockStorage.On("UpdatePoolState", ctx, dbpool, dbpool.State, dbpool.StateDetails).Return(nil, nil)

	// Execute test
	result, jobID, err := _deletePool(ctx, mockTemporal, mockStorage, params)

	// Verify results
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "workflow execution failed", err.Error())

	// Verify job was marked as errored
	mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed")
	mockStorage.AssertCalled(t, "UpdatePoolState", ctx, dbpool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails)
}

// TestCreatePool_WorkflowFailure_JobUpdateFails tests error handling when both workflow and job update fail
func TestCreatePool_WorkflowFailure_JobUpdateFails(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflowenginemock.MockTemporalTestClient)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid"},
		Type:      string(datamodel.JobTypeCreatePool),
		State:     string(datamodel.JobsStateNEW),
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		Name:      "test-pool",
		AccountID: account.ID,
	}

	params := &common.CreatePoolParams{
		AccountName:    "test-account",
		Name:           "test-pool",
		VendorID:       "vendor-id",
		VendorSubNetID: "subnet-id",
		SizeInBytes:    2 * utils.TiBInBytes, // 2TiB (minimum)
		ServiceLevel:   ServiceLevelNameFLEX,
		QosType:        utils.QosTypeAuto,
		Region:         "us-central1",
		PrimaryZone:    "us-central1-a",
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 64,
			Iops:            nillable.ToPointer(int64(1024)), // datamodel.PoolAttributes expects int64, not pointer
		},
	}

	// Setup mocks
	mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
	mockStorage.On("CreatingPool", ctx, mock.Anything).Return(pool, nil)

	// Mock workflow execution to fail
	workflowError := errors.New("workflow execution failed")
	mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowError)

	// Mock job update to also fail
	jobUpdateError := errors.New("job update failed")
	mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed").Return(jobUpdateError)

	// Mock pool state update to errored state (called by defer function)
	mockStorage.On("DeletePool", ctx, mock.Anything).Return(nil)

	// Execute test - should still return the original workflow error, not the job update error
	result, jobID, err := _createPool(ctx, mockStorage, mockTemporal, params)

	// Verify results - should still get the original workflow error
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "workflow execution failed", err.Error())

	// Verify job update was attempted (even though it failed)
	mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed")
	mockStorage.AssertCalled(t, "DeletePool", ctx, pool)
}

// TestCreatePool_CreatePoolInDBFailsWithGenericError tests error handling when CreatePoolInDB fails with generic error (Line 74)
func TestCreatePool_CreatePoolInDBFailsWithGenericError(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflowenginemock.MockTemporalTestClient)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	params := &common.CreatePoolParams{
		AccountName:    "test-account",
		Name:           "test-pool",
		VendorID:       "vendor-id",
		VendorSubNetID: "subnet-id",
		SizeInBytes:    2 * utils.TiBInBytes, // 2TiB (minimum)
		ServiceLevel:   ServiceLevelNameFLEX,
		QosType:        utils.QosTypeAuto,
		Region:         "us-central1",
		PrimaryZone:    "us-central1-a",
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 64,
			Iops:            nillable.ToPointer(int64(1024)), // datamodel.PoolAttributes expects int64, not pointer
		},
	}

	// Setup mocks
	mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)

	// Mock CreatePoolInDB (CreatingPool) to fail with generic error (not conflict)
	dbError := errors.New("database connection error")
	mockStorage.On("CreatingPool", ctx, mock.Anything).Return(nil, dbError)

	// Execute test
	result, jobID, err := _createPool(ctx, mockStorage, mockTemporal, params)

	// Verify results - should return the generic error message (Line 74)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "unable to process request, please try again later", err.Error())
}

// TestCreatePool_CreatePoolInDBFailsWithConflictError tests error handling when CreatePoolInDB fails with conflict error
func TestCreatePool_CreatePoolInDBFailsWithConflictError(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflowenginemock.MockTemporalTestClient)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	params := &common.CreatePoolParams{
		AccountName:    "test-account",
		Name:           "test-pool",
		VendorID:       "vendor-id",
		VendorSubNetID: "subnet-id",
		SizeInBytes:    2 * utils.TiBInBytes, // 2TiB (minimum)
		ServiceLevel:   ServiceLevelNameFLEX,
		QosType:        utils.QosTypeAuto,
		Region:         "us-central1",
		PrimaryZone:    "us-central1-a",
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 64,
			Iops:            nillable.ToPointer(int64(1024)), // datamodel.PoolAttributes expects int64, not pointer
		},
	}

	// Setup mocks
	mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)

	// Mock CreatePoolInDB (CreatingPool) to fail with conflict error
	conflictError := errors.NewConflictErr("pool already exists")
	mockStorage.On("CreatingPool", ctx, mock.Anything).Return(nil, conflictError)

	// Execute test
	result, jobID, err := _createPool(ctx, mockStorage, mockTemporal, params)

	// Verify results - should pass through the conflict error (not the generic message)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, conflictError, err)
}

// TestCreatePool_DeletePoolFailsInDefer tests error handling when UpdatePoolState fails in defer (Line 80)
func TestCreatePool_DeletePoolFailsInDefer(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflowenginemock.MockTemporalTestClient)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		Name:      "test-pool",
		AccountID: account.ID,
	}

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid"},
		Type:      string(datamodel.JobTypeCreatePool),
		State:     string(datamodel.JobsStateNEW),
	}

	params := &common.CreatePoolParams{
		AccountName:    "test-account",
		Name:           "test-pool",
		VendorID:       "vendor-id",
		VendorSubNetID: "subnet-id",
		SizeInBytes:    2 * utils.TiBInBytes, // 2TiB (minimum)
		ServiceLevel:   ServiceLevelNameFLEX,
		QosType:        utils.QosTypeAuto,
		Region:         "us-central1",
		PrimaryZone:    "us-central1-a",
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 64,
			Iops:            nillable.ToPointer(int64(1024)), // datamodel.PoolAttributes expects int64, not pointer
		},
	}

	// Setup mocks
	mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
	mockStorage.On("CreatingPool", ctx, mock.Anything).Return(pool, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)

	// Mock workflow execution to fail
	workflowError := errors.New("workflow execution failed")
	mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowError)

	// Mock UpdateJob to succeed
	mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed").Return(nil)

	// Mock DeletePool to fail (Line 80)
	poolStateError := errors.New("pool deletion failed")
	mockStorage.On("DeletePool", ctx, mock.Anything, mock.Anything, mock.Anything).Return(poolStateError)

	// Execute test
	result, jobID, err := _createPool(ctx, mockStorage, mockTemporal, params)

	// Verify results - should still return the original workflow error
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "workflow execution failed", err.Error())

	// Verify UpdatePoolState was called (even though it failed)
	mockStorage.AssertCalled(t, "DeletePool", ctx, mock.Anything)
}

// TestCreatePool_CreateJobFails tests error handling when CreateJob fails (Line 97)
func TestCreatePool_CreateJobFails(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflowenginemock.MockTemporalTestClient)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		Name:      "test-pool",
		AccountID: account.ID,
	}

	params := &common.CreatePoolParams{
		AccountName:    "test-account",
		Name:           "test-pool",
		VendorID:       "vendor-id",
		VendorSubNetID: "subnet-id",
		SizeInBytes:    2 * utils.TiBInBytes, // 2TiB (minimum)
		ServiceLevel:   ServiceLevelNameFLEX,
		QosType:        utils.QosTypeAuto,
		Region:         "us-central1",
		PrimaryZone:    "us-central1-a",
		CustomPerformanceParams: &common.CustomPerformanceParams{
			ThroughputMibps: 64,
			Iops:            nillable.ToPointer(int64(1024)), // datamodel.PoolAttributes expects int64, not pointer
		},
	}

	// Setup mocks
	mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
	mockStorage.On("CreatingPool", ctx, mock.Anything).Return(pool, nil)

	// Mock CreateJob to fail (Line 97)
	jobError := errors.New("job creation failed")
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, jobError)

	// Mock DeletePool for the defer function call (Line 80)
	mockStorage.On("DeletePool", ctx, mock.Anything).Return(nil)

	// Execute test
	result, jobID, err := _createPool(ctx, mockStorage, mockTemporal, params)

	// Verify results - should return the generic error message (Line 97)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "unable to process request, please try again later", err.Error())
}

// TestUpdatePool_UpdateJobFailsInDefer tests error handling when UpdateJob fails in defer (Line 235)
func TestUpdatePool_UpdateJobFailsInDefer(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflowenginemock.MockTemporalTestClient)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:   datamodel.BaseModel{UUID: "pool-uuid"},
			Name:        "test-pool",
			AccountID:   account.ID,
			Account:     account,
			SizeInBytes: 2 * utils.TiBInBytes,
			QosType:     utils.QosTypeAuto,
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 64,
				Iops:            1024, // datamodel.PoolAttributes expects int64, not pointer
			},
		},
	}

	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{UUID: "pool-uuid"},
		Name:        "test-pool",
		AccountID:   account.ID,
		SizeInBytes: 2 * utils.TiBInBytes,
		QosType:     utils.QosTypeAuto,
	}

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid"},
		Type:      string(datamodel.JobTypeUpdatePool),
		State:     string(datamodel.JobsStateNEW),
	}

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		PoolId:               "pool-uuid",
		SizeInBytes:          2 * utils.TiBInBytes,
		QosType:              utils.QosTypeAuto,
		TotalThroughputMibps: 64,
		TotalIops:            nillable.ToPointer(int64(1024)),
	}

	// Setup mocks - override the getAccountWithName function
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() {
		getAccountWithName = _getAccountWithName // restore original function
	}()

	mockStorage.On("GetPool", ctx, "pool-uuid", account.ID).Return(poolView, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
	mockStorage.On("UpdatingPool", ctx, mock.Anything).Return(pool, nil)

	// Mock workflow execution to fail
	workflowError := errors.New("workflow execution failed")
	mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowError)

	// Mock UpdateJob to fail (Line 235)
	jobUpdateError := errors.New("job update failed")
	mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed").Return(jobUpdateError)

	// Mock UpdatePoolState to succeed
	mockStorage.On("UpdatePoolState", ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	// Execute test
	result, jobID, err := _updatePool(ctx, mockStorage, mockTemporal, params)

	// Verify results - should still return the original workflow error
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "workflow execution failed", err.Error())

	// Verify UpdateJob was called (even though it failed)
	mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed")
}

// TestUpdatePool_UpdatePoolStateFailsInDefer tests error handling when UpdatePoolState fails in defer (Line 240)
func TestUpdatePool_UpdatePoolStateFailsInDefer(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflowenginemock.MockTemporalTestClient)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:   datamodel.BaseModel{UUID: "pool-uuid"},
			Name:        "test-pool",
			AccountID:   account.ID,
			Account:     account,
			SizeInBytes: 2 * utils.TiBInBytes,
			QosType:     utils.QosTypeAuto,
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 64,
				Iops:            1024, // datamodel.PoolAttributes expects int64, not pointer
			},
		},
	}

	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{UUID: "pool-uuid"},
		Name:        "test-pool",
		AccountID:   account.ID,
		SizeInBytes: 2 * utils.TiBInBytes,
		QosType:     utils.QosTypeAuto,
	}

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid"},
		Type:      string(datamodel.JobTypeUpdatePool),
		State:     string(datamodel.JobsStateNEW),
	}

	params := &common.UpdatePoolParams{
		AccountName:          "test-account",
		PoolId:               "pool-uuid",
		SizeInBytes:          2 * utils.TiBInBytes,
		QosType:              utils.QosTypeAuto,
		TotalThroughputMibps: 64,
		TotalIops:            nillable.ToPointer(int64(1024)),
	}

	// Setup mocks - override the getAccountWithName function
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() {
		getAccountWithName = _getAccountWithName // restore original function
	}()

	mockStorage.On("GetPool", ctx, "pool-uuid", account.ID).Return(poolView, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
	mockStorage.On("UpdatingPool", ctx, mock.Anything).Return(pool, nil)

	// Mock workflow execution to fail
	workflowError := errors.New("workflow execution failed")
	mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowError)

	// Mock UpdateJob to succeed
	mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed").Return(nil)

	// Mock UpdatePoolState to fail (Line 240)
	poolStateError := errors.New("pool state update failed")
	mockStorage.On("UpdatePoolState", ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, poolStateError)

	// Execute test
	result, jobID, err := _updatePool(ctx, mockStorage, mockTemporal, params)

	// Verify results - should still return the original workflow error
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "workflow execution failed", err.Error())

	// Verify UpdatePoolState was called (even though it failed)
	mockStorage.AssertCalled(t, "UpdatePoolState", ctx, mock.Anything, mock.Anything, mock.Anything)
}

// TestDeletePool_UpdateJobFailsInDefer tests error handling when UpdateJob fails in defer (Line 406)
func TestDeletePool_UpdateJobFailsInDefer(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflowenginemock.MockTemporalTestClient)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
			Name:      "test-pool",
			AccountID: account.ID,
			Account:   account,
		},
		VolumeCount: 0, // No volumes, so delete is allowed
	}

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid"},
		Type:      string(datamodel.JobTypeDeletePool),
		State:     string(datamodel.JobsStateNEW),
	}

	params := &common.DeletePoolParams{
		AccountName: "test-account",
		PoolID:      "pool-uuid",
	}

	// Setup mocks - override the getAccountWithName function
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() {
		getAccountWithName = _getAccountWithName // restore original function
	}()

	mockStorage.On("GetPool", ctx, "pool-uuid", account.ID).Return(poolView, nil)
	// Mock GetJobByResourceUUID for DELETE_POOL (check for existing delete job when in non-transitional state)
	deleteJobType := models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationDelete, models.PoolCategoryStandard)
	mockStorage.On("GetJobByResourceUUID", ctx, "pool-uuid", string(deleteJobType)).Return(nil, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
	mockStorage.On("DeletingPool", ctx, mock.Anything).Return(nil)

	// Mock workflow execution to fail
	workflowError := errors.New("workflow execution failed")
	mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowError)

	// Mock UpdateJob to fail (Line 406)
	jobUpdateError := errors.New("job update failed")
	mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed").Return(jobUpdateError)

	// Mock UpdatePoolState to succeed
	mockStorage.On("UpdatePoolState", ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	// Execute test
	result, jobID, err := _deletePool(ctx, mockTemporal, mockStorage, params)

	// Verify results - should still return the original workflow error
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "workflow execution failed", err.Error())

	// Verify UpdateJob was called (even though it failed)
	mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed")
}

// TestDeletePool_UpdatePoolStateFailsInDefer tests error handling when UpdatePoolState fails in defer (Line 419)
func TestDeletePool_UpdatePoolStateFailsInDefer(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflowenginemock.MockTemporalTestClient)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test-account",
	}

	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
			Name:      "test-pool",
			AccountID: account.ID,
			Account:   account,
		},
		VolumeCount: 0, // No volumes, so delete is allowed
	}

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid"},
		Type:      string(datamodel.JobTypeDeletePool),
		State:     string(datamodel.JobsStateNEW),
	}

	params := &common.DeletePoolParams{
		AccountName: "test-account",
		PoolID:      "pool-uuid",
	}

	// Setup mocks - override the getAccountWithName function
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() {
		getAccountWithName = _getAccountWithName // restore original function
	}()

	mockStorage.On("GetPool", ctx, "pool-uuid", account.ID).Return(poolView, nil)
	// Mock GetJobByResourceUUID for DELETE_POOL (check for existing delete job when in non-transitional state)
	deleteJobType := models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationDelete, models.PoolCategoryStandard)
	mockStorage.On("GetJobByResourceUUID", ctx, "pool-uuid", string(deleteJobType)).Return(nil, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
	mockStorage.On("DeletingPool", ctx, mock.Anything).Return(nil)

	// Mock workflow execution to fail
	workflowError := errors.New("workflow execution failed")
	mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowError)

	// Mock UpdateJob to succeed
	mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow execution failed").Return(nil)

	// Mock UpdatePoolState to fail (Line 419)
	poolStateError := errors.New("pool state update failed")
	mockStorage.On("UpdatePoolState", ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, poolStateError)

	// Execute test
	result, jobID, err := _deletePool(ctx, mockTemporal, mockStorage, params)

	// Verify results - should still return the original workflow error
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "workflow execution failed", err.Error())

	// Verify UpdatePoolState was called (even though it failed)
	mockStorage.AssertCalled(t, "UpdatePoolState", ctx, mock.Anything, mock.Anything, mock.Anything)
}

// Tests for the new unified validation function _validatePoolParams
func TestValidatePoolParams(t *testing.T) {
	t.Run("ValidCreateParams_StandardPool", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(2 * utils.TiBInBytes),
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.NoError(tt, err, "Valid standard pool params should pass validation")
	})

	t.Run("ValidCreateParams_LargeCapacityPool", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(100 * utils.TiBInBytes),
			ThroughputMibps:    1000,
			Iops:               nillable.ToPointer(int64(16000)), // Minimum IOPS for 1000 MiBps in large capacity
			AllowAutoTiering:   false,                            // Set to false since auto-tiering is globally disabled
			HotTierSizeInBytes: 0,                                // No hot tier size when auto-tiering is disabled
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      true,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.NoError(tt, err, "Valid large capacity pool params should pass validation")
	})

	t.Run("InvalidServiceLevel_ReturnsError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(2 * utils.TiBInBytes),
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, "Premium")
		assert.Error(tt, err)
		assert.EqualError(tt, err, "Given service level not supported. Supported service level is "+ServiceLevelNameFLEX)
	})

	t.Run("EmptyServiceLevel_NoValidationError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(2 * utils.TiBInBytes),
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, "")
		assert.NoError(tt, err, "Empty service level should not cause validation error")
	})

	t.Run("InvalidSize_ReturnsError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(1 * utils.GiBInBytes), // Below minimum
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Given pool size not supported")
	})

	t.Run("InvalidQosType_ReturnsError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(2 * utils.TiBInBytes),
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            "Manual", // Invalid QoS type (or unsupported when enableMqos is false)
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.Error(tt, err)
		// Message varies by ENABLE_MQOS: "Supported QoS type is auto" vs "Supported QoS types are auto and manual"
		assert.Contains(tt, err.Error(), "QoS type not supported")
	})

	t.Run("InvalidThroughput_ReturnsError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(2 * utils.TiBInBytes),
			ThroughputMibps:    -1, // Invalid negative throughput
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalThroughputMibps must be set and must be greater than 0")
	})

	t.Run("InvalidIOPS_ReturnsError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(2 * utils.TiBInBytes),
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(100)), // Below minimum IOPS
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.Contains(tt, err.Error(), "TotalIops must be between")
	})

	t.Run("InvalidAutoTiering_ReturnsError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(2 * utils.TiBInBytes),
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   true,
			HotTierSizeInBytes: uint64(3 * utils.TiBInBytes), // Hot tier larger than pool size
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Auto-Tiering feature is currently not enabled")
	})

	t.Run("ValidAutoTiering_NoError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(10 * utils.TiBInBytes),
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   false, // Set to false since auto-tiering is globally disabled
			HotTierSizeInBytes: 0,     // No hot tier size when auto-tiering is disabled
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.NoError(tt, err, "Valid params without auto-tiering should pass validation")
	})

	t.Run("LargeCapacityPool_ValidParams", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(100 * utils.TiBInBytes),
			ThroughputMibps:    1000,
			Iops:               nillable.ToPointer(int64(16000)), // Minimum IOPS for 1000 MiBps in large capacity
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      true,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.NoError(tt, err, "Valid large capacity pool params should pass validation")
	})

	t.Run("LargeCapacityPool_InvalidSize_ReturnsError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(5 * utils.TiBInBytes), // Below large capacity minimum (6 TiB)
			ThroughputMibps:    1000,
			Iops:               nillable.ToPointer(int64(16000)), // Minimum IOPS for 1000 MiBps in large capacity
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      true,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SizeInBytes must be at least")
	})

	// Additional edge cases
	t.Run("ZeroThroughput_ReturnsError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(2 * utils.TiBInBytes),
			ThroughputMibps:    0, // Zero throughput
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalThroughputMibps must be between")
	})

	t.Run("NilIOPS_CalculatedFromThroughput", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(2 * utils.TiBInBytes),
			ThroughputMibps:    128,
			Iops:               nil, // IOPS not set, should be calculated
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.NoError(tt, err, "IOPS should be calculated from throughput when not provided")
	})

	t.Run("MaximumValidSize_StandardPool", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(50 * utils.TiBInBytes), // Maximum for standard pool
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.NoError(tt, err, "Maximum valid size for standard pool should pass validation")
	})

	t.Run("MaximumValidSize_LargeCapacityPool", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(1000 * utils.TiBInBytes), // Maximum for large capacity pool
			ThroughputMibps:    1000,
			Iops:               nillable.ToPointer(int64(16000)), // Minimum IOPS for 1000 MiBps in large capacity
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      true,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.NoError(tt, err, "Maximum valid size for large capacity pool should pass validation")
	})

	t.Run("AutoTieringDisabled_WithHotTierSize_NoError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(10 * utils.TiBInBytes),
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   false,
			HotTierSizeInBytes: uint64(2 * utils.TiBInBytes), // Hot tier size set but auto-tiering disabled
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      false,
		}

		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.NoError(tt, err, "Hot tier size should be allowed when auto-tiering is disabled")
	})
	t.Run("AutoTieringEnabled_WithHotTierSize_LV_Error", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(5 * utils.TiBInBytes),
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   true,
			HotTierSizeInBytes: uint64(2 * utils.TiBInBytes), // Hot tier size set but auto-tiering disabled
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      true,
		}
		validators.AutoTieringEnabled = true
		defer func() {
			validators.AutoTieringEnabled = false
		}()
		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.EqualError(tt, err, "SizeInBytes must be at least 6TiB (6597069766656 bytes) for Large Capacity pools")
	})
	t.Run("AutoTieringEnabled_WithHotTierSize_LV_NoError", func(tt *testing.T) {
		perf := &validators.CustomPerformance{
			SizeInBytes:        uint64(6 * utils.TiBInBytes),
			ThroughputMibps:    128,
			Iops:               nillable.ToPointer(int64(2048)),
			AllowAutoTiering:   true,
			HotTierSizeInBytes: uint64(6 * utils.TiBInBytes), // Hot tier size set but auto-tiering disabled
			QosType:            utils.QosTypeAuto,
			LargeCapacity:      true,
		}
		validators.AutoTieringEnabled = true
		defer func() {
			validators.AutoTieringEnabled = false
		}()
		err := _validatePoolParams(perf, ServiceLevelNameFLEX)
		assert.NoError(tt, err, "SizeInBytes must be at least 6TiB (6597069766656 bytes) for Large Capacity pools")
	})
}

// Tests for the refactored _validateCreatePoolParams function
func TestValidateCreatePoolParamsRefactored(t *testing.T) {
	logger := log.NewLogger()
	t.Run("ValidParams_StandardPool", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "Valid create params should pass validation")
	})

	t.Run("ValidParams_LargeCapacityPool", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(100 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: true,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 1000,
				Iops:            nillable.ToPointer(int64(16000)), // Minimum IOPS for 1000 MiBps in large capacity
			},
			AllowAutoTiering:   true,
			HotTierSizeInBytes: uint64(10 * utils.TiBInBytes),
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err, "Auto-tiering feature is currently disabled")
		assert.Contains(tt, err.Error(), "Auto-Tiering feature is currently not enabled")
	})

	t.Run("InvalidServiceLevel_ReturnsError", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  "Premium", // Invalid service level
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "Given service level not supported. Supported service level is "+ServiceLevelNameFLEX)
	})

	t.Run("NilCustomPerformanceParams_ReturnsError", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			// CustomPerformanceParams is nil
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err, "Nil CustomPerformanceParams should cause validation error")
		assert.Contains(tt, err.Error(), "TotalThroughputMibps must be between")
	})

	// Additional test cases for create params
	t.Run("ValidParams_WithAutoTiering", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(10 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 256,
				Iops:            nillable.ToPointer(int64(4096)),
			},
			AllowAutoTiering:   true,
			HotTierSizeInBytes: uint64(2 * utils.TiBInBytes),
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err, "Auto-tiering feature is currently disabled")
		assert.Contains(tt, err.Error(), "Auto-Tiering feature is currently not enabled")
	})

	t.Run("ValidParams_WithLabels", func(tt *testing.T) {
		labels := &datamodel.JSONB{
			"environment": "production",
			"team":        "storage",
		}
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
			Labels: labels,
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "Valid create params with labels should pass validation")
	})

	t.Run("ValidParams_RegionalHA", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
			IsRegionalHA: true,
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "Valid create params with regional HA should pass validation")
	})

	// Additional edge cases and error scenarios for create params
	t.Run("InvalidSize_BelowMinimum_ReturnsError", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(1 * utils.GiBInBytes), // Below minimum
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Given pool size not supported")
	})

	t.Run("InvalidSize_AboveMaximum_StandardPool_ReturnsError", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(500 * utils.TiBInBytes), // Above maximum for standard pool (425 TiB)
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 1000,
				Iops:            nillable.ToPointer(int64(16000)), // Minimum IOPS for 1000 MiBps
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Given pool size not supported")
	})

	t.Run("InvalidSize_BelowMinimum_LargeCapacityPool_ReturnsError", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(5 * utils.TiBInBytes), // Below minimum for large capacity pool (6 TiB)
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: true,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 1000,
				Iops:            nillable.ToPointer(int64(16000)), // Minimum IOPS for 1000 MiBps in large capacity
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SizeInBytes must be at least")
	})

	t.Run("InvalidThroughput_BelowMinimum_ReturnsError", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: -1, // Below minimum
				Iops:            nillable.ToPointer(int64(2048)),
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalThroughputMibps must be set and must be greater than 0")
	})

	t.Run("InvalidThroughput_AboveMaximum_ReturnsError", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 10000, // Above maximum
				Iops:            nillable.ToPointer(int64(2048)),
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalThroughputMibps must be between")
	})

	t.Run("InvalidIOPS_BelowMinimum_ReturnsError", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(100)), // Below minimum
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalIops must be between")
	})

	t.Run("InvalidIOPS_AboveMaximum_ReturnsError", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(200000)), // Above maximum
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalIops must be between")
	})

	t.Run("InvalidAutoTiering_HotTierTooLarge_ReturnsError", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(5 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
			AllowAutoTiering:   true,
			HotTierSizeInBytes: uint64(10 * utils.TiBInBytes), // Hot tier larger than pool size
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err, "Auto-tiering should fail when globally disabled")
		assert.Contains(tt, err.Error(), "Auto-Tiering feature is currently not enabled")
	})

	t.Run("ValidParams_BoundarySize_StandardPool", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(50 * utils.TiBInBytes), // Boundary size for standard pool
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 256,
				Iops:            nillable.ToPointer(int64(4096)),
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "Boundary size for standard pool should pass validation")
	})

	t.Run("ValidParams_BoundarySize_LargeCapacityPool", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(100 * utils.TiBInBytes), // Boundary size for large capacity pool
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: true,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 1000,
				Iops:            nillable.ToPointer(int64(16000)), // Minimum IOPS for 1000 MiBps in large capacity
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "Boundary size for large capacity pool should pass validation")
	})

	t.Run("ValidParams_WithHotTierAutoResize", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(10 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 256,
				Iops:            nillable.ToPointer(int64(4096)),
			},
			AllowAutoTiering:        true,
			HotTierSizeInBytes:      uint64(2 * utils.TiBInBytes),
			EnableHotTierAutoResize: true,
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err, "Auto-tiering should fail when globally disabled")
		assert.Contains(tt, err.Error(), "Auto-Tiering feature is currently not enabled")
	})

	t.Run("ValidParams_WithKMSConfig", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
			KmsConfigId: "test-kms-config-id",
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "Valid create params with KMS config should pass validation")
	})

	t.Run("ValidParams_WithTags", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
			Tags: "environment=production,team=storage",
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "Valid create params with tags should pass validation")
	})

	// Tests for the new KMS configuration validation in _validateCreatePoolParams
	t.Run("ValidParams_WithValidKmsConfig_ReadyState", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
			KmsConfig: &models.KmsConfig{
				State: datamodel.LifeCycleStateREADY,
				ServiceAccount: &models.ServiceAccount{
					State: datamodel.AccountStateEnabled,
				},
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "Valid KMS config in READY state should pass validation")
	})

	t.Run("ValidParams_WithValidKmsConfig_InUseState", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
			KmsConfig: &models.KmsConfig{
				State: datamodel.LifeCycleStateInUse,
				ServiceAccount: &models.ServiceAccount{
					State: datamodel.AccountStateEnabled,
				},
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "Valid KMS config in IN_USE state should pass validation")
	})

	t.Run("InvalidParams_KmsConfigInvalidState", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
			KmsConfig: &models.KmsConfig{
				State: datamodel.LifeCycleStateCreating, // Invalid state for pool creation
				ServiceAccount: &models.ServiceAccount{
					State: datamodel.AccountStateEnabled,
				},
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Invalid KMS configuration state for pool creation: CREATING")
	})

	t.Run("InvalidParams_KmsConfigDisabledState", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
			KmsConfig: &models.KmsConfig{
				State: datamodel.LifeCycleStateDisabled, // Invalid state for pool creation
				ServiceAccount: &models.ServiceAccount{
					State: datamodel.AccountStateEnabled,
				},
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Invalid KMS configuration state for pool creation: DISABLED")
	})

	t.Run("ValidParams_WithNilKmsConfig", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
			KmsConfig: nil, // No KMS config should be valid
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "Nil KMS config should pass validation")
	})

	t.Run("ValidParams_WithKmsConfigNilServiceAccount", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
			KmsConfig: &models.KmsConfig{
				State:          datamodel.LifeCycleStateREADY,
				ServiceAccount: nil, // Nil service account should be valid
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "KMS config with nil service account should pass validation")
	})

	// Tests for ONTAP mode version validation
	t.Run("ONTAPMode_ValidVersion_ShouldPass", func(tt *testing.T) {
		// Set environment to return a valid version (>= 9.18)
		originalCurrent := env.CurrentOntapVersionDetails
		originalExperimental := env.ExperimentalOntapVersionDetails
		defer func() {
			env.CurrentOntapVersionDetails = originalCurrent
			env.ExperimentalOntapVersionDetails = originalExperimental
		}()
		env.CurrentOntapVersionDetails = "9.18.1" // Valid version >= 9.18
		env.ExperimentalOntapVersionDetails = ""

		params := &common.CreatePoolParams{
			AccountName:   "test-account",
			Mode:          common.ONTAPMode,
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "ONTAP mode with valid version should pass validation")
	})

	t.Run("ONTAPMode_InvalidVersion_ShouldFail", func(tt *testing.T) {
		// Set environment to return an invalid version (< 9.18)
		originalCurrent := env.CurrentOntapVersionDetails
		originalExperimental := env.ExperimentalOntapVersionDetails
		defer func() {
			env.CurrentOntapVersionDetails = originalCurrent
			env.ExperimentalOntapVersionDetails = originalExperimental
		}()
		env.CurrentOntapVersionDetails = "9.17.1" // Invalid version < 9.18
		env.ExperimentalOntapVersionDetails = ""

		params := &common.CreatePoolParams{
			AccountName:   "test-account",
			Mode:          common.ONTAPMode,
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err, "ONTAP mode with invalid version should fail validation")
		assert.Contains(tt, err.Error(), "ONTAP version")
		assert.Contains(tt, err.Error(), "below the minimum required version")
	})

	t.Run("ONTAPMode_PORequest_SkipsEnvVersionCheck", func(tt *testing.T) {
		originalCurrent := env.CurrentOntapVersionDetails
		originalExperimental := env.ExperimentalOntapVersionDetails
		defer func() {
			env.CurrentOntapVersionDetails = originalCurrent
			env.ExperimentalOntapVersionDetails = originalExperimental
		}()
		env.CurrentOntapVersionDetails = "9.17.1"
		env.ExperimentalOntapVersionDetails = ""

		params := &common.CreatePoolParams{
			AccountName:  "test-account",
			Mode:         common.ONTAPMode,
			ClusterName:  "vsim",
			ServiceLevel: common.ServiceLevelExtreme,
			SizeInBytes:  uint64(utils.TiBInBytes),
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "PO requests should not use env-based ONTAP version gating")
		assert.NotNil(tt, params.CustomPerformanceParams.Iops)
		assert.Equal(tt, int64(128), params.CustomPerformanceParams.ThroughputMibps)
	})

	t.Run("PORequest_RejectsActiveDirectory", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			AccountName:       "test-account",
			Mode:              common.ONTAPMode,
			ClusterName:       "vsim",
			ServiceLevel:      common.ServiceLevelExtreme,
			SizeInBytes:       uint64(utils.TiBInBytes),
			ActiveDirectoryId: "ad-uuid",
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), common.POValidationActiveDirectoryMsg)
	})

	t.Run("PORequest_RejectsLdapEnabled", func(tt *testing.T) {
		params := &common.CreatePoolParams{
			AccountName:  "test-account",
			Mode:         common.ONTAPMode,
			ClusterName:  "vsim",
			ServiceLevel: common.ServiceLevelExtreme,
			SizeInBytes:  uint64(utils.TiBInBytes),
			LdapEnabled:  true,
		}

		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), common.POValidationLdapEnabledMsg)
	})

	t.Run("NonPOOntap_AllowsActiveDirectoryAndLdap", func(tt *testing.T) {
		originalCurrent := env.CurrentOntapVersionDetails
		originalExperimental := env.ExperimentalOntapVersionDetails
		defer func() {
			env.CurrentOntapVersionDetails = originalCurrent
			env.ExperimentalOntapVersionDetails = originalExperimental
		}()
		env.CurrentOntapVersionDetails = "9.18.1"
		env.ExperimentalOntapVersionDetails = ""

		params := &common.CreatePoolParams{
			AccountName:       "test-account",
			Mode:              common.ONTAPMode,
			SizeInBytes:       uint64(2 * utils.TiBInBytes),
			ServiceLevel:      ServiceLevelNameFLEX,
			QosType:           utils.QosTypeAuto,
			ActiveDirectoryId: "ad-uuid",
			LdapEnabled:       true,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err)
	})

	t.Run("DEFAULTMode_ShouldNotValidateOntapVersion", func(tt *testing.T) {
		// Set environment to return an invalid version
		// This should not matter since we're in DEFAULT mode
		originalCurrent := env.CurrentOntapVersionDetails
		originalExperimental := env.ExperimentalOntapVersionDetails
		defer func() {
			env.CurrentOntapVersionDetails = originalCurrent
			env.ExperimentalOntapVersionDetails = originalExperimental
		}()
		env.CurrentOntapVersionDetails = "9.17.1" // Invalid version, but shouldn't be checked for DEFAULT mode
		env.ExperimentalOntapVersionDetails = ""

		params := &common.CreatePoolParams{
			AccountName:   "test-account",
			Mode:          common.DEFAULTMode,
			SizeInBytes:   uint64(2 * utils.TiBInBytes),
			ServiceLevel:  ServiceLevelNameFLEX,
			QosType:       utils.QosTypeAuto,
			LargeCapacity: false,
			CustomPerformanceParams: &common.CustomPerformanceParams{
				ThroughputMibps: 128,
				Iops:            nillable.ToPointer(int64(2048)),
			},
		}

		err := _validateCreatePoolParams(params, logger)
		assert.NoError(tt, err, "DEFAULT mode should not validate ONTAP version")
	})
}

// Comprehensive tests for the _validateAndSetUpdatePoolParams function
func TestValidateUpdatePoolParamsComprehensive(t *testing.T) {
	// Test 1: Valid standard pool update parameters
	t.Run("ValidParams_StandardPool", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid update params should pass validation")
	})

	// Test 2: Valid large capacity pool update parameters
	t.Run("ValidParams_LargeCapacityPool", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(200 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(true),
			TotalThroughputMibps: 2000,
			TotalIops:            nillable.ToPointer(int64(32000)), // Minimum IOPS for 2000 MiBps in large capacity
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(100 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    true,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid large capacity update params should pass validation")
	})

	// Test 3: Auto-tiering enabled for pool that didn't have it - valid hot tier size
	t.Run("AutoTieringEnabled_ValidHotTierSize", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(10 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			AllowAutoTiering:     true,
			HotTierSizeInBytes:   uint64(8 * utils.TiBInBytes), // Less than pool size
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(10 * utils.TiBInBytes),
			AllowAutoTiering: false, // Pool didn't have auto-tiering
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err, "Hot tier size validation should fail")
		assert.Contains(tt, err.Error(), "Hot tier size cannot be less than existing pool size")
	})

	// Test 4: Auto-tiering enabled for pool that didn't have it - hot tier too small
	t.Run("AutoTieringEnabled_HotTierTooSmall", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(10 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			AllowAutoTiering:     true,
			HotTierSizeInBytes:   uint64(5 * utils.TiBInBytes), // Less than pool size
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(10 * utils.TiBInBytes),
			AllowAutoTiering: false, // Pool didn't have auto-tiering
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Hot tier size cannot be less than existing pool size")
	})

	// Test 5: Auto-tiering enabled for pool that already had it - valid hot tier size increase
	t.Run("AutoTieringEnabled_ValidHotTierIncrease", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(20 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 512,
			TotalIops:            nillable.ToPointer(int64(8192)),
			AllowAutoTiering:     true,
			HotTierSizeInBytes:   uint64(15 * utils.TiBInBytes), // Greater than existing hot tier
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(20 * utils.TiBInBytes),
			AllowAutoTiering: true, // Pool already had auto-tiering
			LargeCapacity:    false,
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				HotTierSizeInBytes: int64(10 * utils.TiBInBytes), // Existing hot tier size
			},
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err, "Auto-tiering feature is currently disabled")
		assert.Contains(tt, err.Error(), "Auto-Tiering feature is currently not enabled")
	})

	// Test 6: Auto-tiering enabled for pool that already had it - hot tier size decrease (invalid)
	t.Run("AutoTieringEnabled_InvalidHotTierDecrease", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(20 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 512,
			TotalIops:            nillable.ToPointer(int64(8192)),
			AllowAutoTiering:     true,
			HotTierSizeInBytes:   uint64(8 * utils.TiBInBytes), // Less than existing hot tier
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(20 * utils.TiBInBytes),
			AllowAutoTiering: true, // Pool already had auto-tiering
			LargeCapacity:    false,
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				HotTierSizeInBytes: int64(10 * utils.TiBInBytes), // Existing hot tier size
			},
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Hot tier size must be greater than existing hot tier size")
	})

	// Test 7: Auto-tiering disabled for pool that had it (not supported)
	t.Run("AutoTieringDisabled_NotSupported", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(20 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 512,
			TotalIops:            nillable.ToPointer(int64(8192)),
			AllowAutoTiering:     false, // Trying to disable auto-tiering
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(20 * utils.TiBInBytes),
			AllowAutoTiering: true, // Pool had auto-tiering enabled
			LargeCapacity:    false,
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				HotTierSizeInBytes: int64(10 * utils.TiBInBytes),
			},
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Auto tiering disable operation is not supported")
	})

	// Test 8: Invalid pool size below minimum
	t.Run("InvalidSize_BelowMinimum", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(1 * utils.GiBInBytes), // Below minimum
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 128,
			TotalIops:            nillable.ToPointer(int64(2048)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Given pool size not supported")
	})

	// Test 9: Invalid pool size above maximum
	t.Run("InvalidSize_AboveMaximum", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(500 * utils.TiBInBytes), // Above maximum (425 TiB)
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 128,
			TotalIops:            nillable.ToPointer(int64(2048)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Given pool size not supported")
	})

	// Test 10: Invalid QoS type (not "auto" or "manual")
	t.Run("InvalidQosType_ChangeFromAutoToManual", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              "Manual", // Invalid QoS type
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Given QoS type not supported")
	})

	// Test 10a: Allow qosType change from auto to manual (workflow performs transition)
	t.Run("AllowUpdatePoolWhenQosTypeChangesFromAutoToManual", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeManual,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
			QosType:          utils.QosTypeAuto,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "QosType transition from auto to manual should be allowed")
	})

	// Test 10b: Allow qosType change from manual to auto (workflow performs transition)
	t.Run("AllowUpdatePoolWhenQosTypeChangesFromManualToAuto", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
			QosType:          utils.QosTypeManual,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "QosType transition from manual to auto should be allowed")
	})

	// Test 11: Invalid throughput below minimum
	t.Run("InvalidThroughput_BelowMinimum", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 32, // Below minimum (64 MiBps)
			TotalIops:            nillable.ToPointer(int64(2048)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalThroughputMibps must be between")
	})

	// Test 12: Invalid throughput above maximum
	t.Run("InvalidThroughput_AboveMaximum", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 6000, // Above maximum (5120 MiBps)
			TotalIops:            nillable.ToPointer(int64(2048)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalThroughputMibps must be between")
	})

	// Test 13: Invalid IOPS below minimum
	t.Run("InvalidIOPS_BelowMinimum", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(500)), // Below minimum (1024 IOPS)
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalIops must be between")
	})

	// Test 14: Invalid IOPS above maximum
	t.Run("InvalidIOPS_AboveMaximum", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(200000)), // Above maximum (160000 IOPS)
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalIops must be between")
	})

	t.Run("InvalidThroughput_ReturnsError", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: -1, // Invalid negative throughput
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalThroughputMibps must be set and must be greater than")
	})

	// Test 15: IOPS calculated from throughput when not provided
	t.Run("NilIOPS_CalculatedFromThroughput", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 128, // IOPS will be calculated from this
			TotalIops:            nil, // IOPS not set, should be calculated
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "IOPS should be calculated from throughput when not provided")
	})

	// Test 16: Valid update with labels
	t.Run("ValidUpdate_WithLabels", func(tt *testing.T) {
		labels := &datamodel.JSONB{
			"environment": "production",
			"team":        "storage",
		}
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			Labels:               labels,
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid update with labels should pass")
	})

	// Test 17: Valid update with description
	t.Run("ValidUpdate_WithDescription", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			Description:          "Updated pool description",
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid update with description should pass")
	})

	// Test 18: Valid update with vendor ID
	t.Run("ValidUpdate_WithVendorID", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			VendorID:             "updated-vendor-id",
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid update with vendor ID should pass")
	})

	// Test 19: Valid update with custom performance enabled
	t.Run("ValidUpdate_WithCustomPerformanceEnabled", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:              uint64(4 * utils.TiBInBytes),
			QosType:                  utils.QosTypeAuto,
			LargeCapacity:            nillable.ToPointer(false),
			TotalThroughputMibps:     256,
			TotalIops:                nillable.ToPointer(int64(4096)),
			CustomPerformanceEnabled: true,
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid update with custom performance enabled should pass")
	})

	t.Run("AutoTieringEnabled_ValidParams", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(10 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			AllowAutoTiering:     true,
			HotTierSizeInBytes:   uint64(2 * utils.TiBInBytes),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(5 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err, "Hot tier size validation should fail")
		assert.Contains(tt, err.Error(), "Hot tier size cannot be less than existing pool size")
	})

	// Additional test cases for update params
	t.Run("ValidParams_WithLabels", func(tt *testing.T) {
		labels := &datamodel.JSONB{
			"environment": "staging",
			"team":        "devops",
		}
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			Labels:               labels,
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid update params with labels should pass validation")
	})

	t.Run("ValidParams_WithoutQosType", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              "",
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			Description:          "Updated pool description",
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
			QosType:          utils.QosTypeAuto,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid update params with description should pass validation")
	})
	t.Run("ValidParams_WithDescription", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			Description:          "Updated pool description",
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid update params with description should pass validation")
	})

	t.Run("ValidParams_WithVendorID", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			VendorID:             "updated-vendor-id",
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid update params with vendor ID should pass validation")
	})

	t.Run("ValidParams_WithCustomPerformanceEnabled", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:              uint64(4 * utils.TiBInBytes),
			QosType:                  utils.QosTypeAuto,
			LargeCapacity:            nillable.ToPointer(false),
			TotalThroughputMibps:     256,
			TotalIops:                nillable.ToPointer(int64(4096)),
			CustomPerformanceEnabled: true,
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid update params with custom performance enabled should pass validation")
	})

	// Additional edge cases and error scenarios
	t.Run("ZeroThroughput_ReturnsError", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 0, // Zero throughput
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalThroughputMibps must be between")
	})

	t.Run("InvalidIOPS_BelowMinimum_ReturnsError", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(100)), // Below minimum IOPS
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalIops must be between")
	})

	t.Run("InvalidIOPS_AboveMaximum_ReturnsError", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(200000)), // Above maximum IOPS
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalIops must be between")
	})

	t.Run("InvalidThroughput_AboveMaximum_ReturnsError", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 10000, // Above maximum throughput
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TotalThroughputMibps must be between")
	})

	t.Run("InvalidAutoTiering_HotTierTooLarge_ReturnsError", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(10 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			AllowAutoTiering:     true,
			HotTierSizeInBytes:   uint64(15 * utils.TiBInBytes), // Hot tier larger than pool size
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(5 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Auto-Tiering feature is currently not enabled")
	})

	t.Run("FailsWhenLargeCapacityChanged_RegularPoolToLargeCapacity", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(true), // Trying to change from regular to large capacity
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false, // Pool is regular capacity
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Given large capacity value is not supported. Large capacity cannot be changed for existing pool")
	})

	t.Run("FailsWhenLargeCapacityChanged_LargeCapacityPoolToRegular", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(200 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false), // Trying to change from large capacity to regular
			TotalThroughputMibps: 2000,
			TotalIops:            nillable.ToPointer(int64(32000)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(100 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    true, // Pool is large capacity
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Given large capacity value is not supported. Large capacity cannot be changed for existing pool")
	})

	t.Run("PassesWhenLargeCapacityMatches_RegularPool", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false), // Matches pool's large capacity setting
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false, // Pool is regular capacity
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Large capacity matches - should pass validation")
	})

	t.Run("PassesWhenLargeCapacityMatches_LargeCapacityPool", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(200 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(true), // Matches pool's large capacity setting
			TotalThroughputMibps: 2000,
			TotalIops:            nillable.ToPointer(int64(32000)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(100 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    true, // Pool is large capacity
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Large capacity matches - should pass validation")
	})

	t.Run("PassesWhenLargeCapacityNotProvided", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nil, // Not provided - should pass validation
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Large capacity not provided - should pass validation")
	})

	t.Run("ValidParams_BoundarySize_StandardPool", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(50 * utils.TiBInBytes), // Boundary size for standard pool
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Boundary size for standard pool should pass validation")
	})

	t.Run("ValidParams_BoundarySize_LargeCapacityPool", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(100 * utils.TiBInBytes), // Boundary size for large capacity pool
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(true),
			TotalThroughputMibps: 1000,
			TotalIops:            nillable.ToPointer(int64(16000)), // Minimum IOPS for 1000 MiBps in large capacity
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(50 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    true,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Boundary size for large capacity pool should pass validation")
	})

	t.Run("ValidParams_WithHotTierAutoResize", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:             uint64(10 * utils.TiBInBytes),
			QosType:                 utils.QosTypeAuto,
			LargeCapacity:           nillable.ToPointer(false),
			TotalThroughputMibps:    256,
			TotalIops:               nillable.ToPointer(int64(4096)),
			AllowAutoTiering:        true,
			HotTierSizeInBytes:      uint64(2 * utils.TiBInBytes),
			EnableHotTierAutoResize: true,
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(5 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.Error(tt, err, "Hot tier size validation should fail")
		assert.Contains(tt, err.Error(), "Hot tier size cannot be less than existing pool size")
	})

	t.Run("ValidParams_WithZoneChanges", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false),
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
			Zone:                 "us-central1-b",
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false,
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		assert.NoError(tt, err, "Valid update params with zone changes should pass validation")
	})

	// Test for the specific line: perf.LargeCapacity = pool.LargeCapacity
	t.Run("LargeCapacityFieldIsSetFromExistingPool", func(tt *testing.T) {
		// Test case 1: Update params specify LargeCapacity=false but pool has LargeCapacity=true
		// This should now fail with the new validation that prevents changing LargeCapacity
		params := &common.UpdatePoolParams{
			SizeInBytes:          uint64(20 * utils.TiBInBytes), // Use valid large capacity size
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false), // Update params specify standard capacity
			TotalThroughputMibps: 1000,
			TotalIops:            nillable.ToPointer(int64(16000)),
		}

		pool := &datamodel.Pool{
			SizeInBytes:      int64(15 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    true, // Existing pool is large capacity
		}

		err := _validateAndSetUpdatePoolParams(params, pool)
		// Should fail validation because we can't change LargeCapacity
		assert.Error(tt, err, "Cannot change LargeCapacity for existing pool")
		assert.Contains(tt, err.Error(), "Large capacity cannot be changed for existing pool")

		// Test case 2: Update params specify LargeCapacity=true but pool has LargeCapacity=false
		// This should now fail with the new validation that prevents changing LargeCapacity
		params2 := &common.UpdatePoolParams{
			SizeInBytes:          uint64(4 * utils.TiBInBytes), // Use valid standard capacity size
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(true), // Update params specify large capacity
			TotalThroughputMibps: 256,
			TotalIops:            nillable.ToPointer(int64(4096)),
		}

		pool2 := &datamodel.Pool{
			SizeInBytes:      int64(2 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false, // Existing pool is standard capacity
		}

		err2 := _validateAndSetUpdatePoolParams(params2, pool2)
		// Should fail validation because we can't change LargeCapacity
		assert.Error(tt, err2, "Cannot change LargeCapacity for existing pool")
		assert.Contains(tt, err2.Error(), "Large capacity cannot be changed for existing pool")

		// Test case 3: We don't try to change LargeCapacity, but provide invalid parameters for the pool type
		params3 := &common.UpdatePoolParams{
			SizeInBytes:          uint64(500 * utils.TiBInBytes), // Exceeds standard pool maximum (425 TiB)
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nil,                              // Don't specify a new value (use existing)
			TotalThroughputMibps: 2000,                             // Large capacity throughput
			TotalIops:            nillable.ToPointer(int64(32000)), // Large capacity IOPS
		}

		pool3 := &datamodel.Pool{
			SizeInBytes:      int64(100 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false, // Existing pool is standard capacity
		}

		err3 := _validateAndSetUpdatePoolParams(params3, pool3)
		// Should still fail validation because the size parameters are incompatible with standard pool
		assert.Error(tt, err3, "Validation should fail when large capacity params are used with standard capacity pool")
	})

	// Test specifically for the validation that prevents changing LargeCapacity for existing pools
	t.Run("PreventChangingLargeCapacityForExistingPool", func(tt *testing.T) {
		// Test case 1: Attempt to change from standard to large capacity
		standardPool := &datamodel.Pool{
			SizeInBytes:      int64(10 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    false, // Standard capacity pool
		}

		paramsToLarge := &common.UpdatePoolParams{
			SizeInBytes:          uint64(20 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(true), // Try to change to large capacity
			TotalThroughputMibps: 1000,
			TotalIops:            nillable.ToPointer(int64(16000)),
		}

		err1 := _validateAndSetUpdatePoolParams(paramsToLarge, standardPool)
		assert.Error(tt, err1, "Changing from standard to large capacity should fail")
		assert.True(tt, errors.IsUserInputValidationErr(err1))
		assert.Contains(tt, err1.Error(), "Large capacity cannot be changed for existing pool")

		// Test case 2: Attempt to change from large to standard capacity
		largePool := &datamodel.Pool{
			SizeInBytes:      int64(100 * utils.TiBInBytes),
			AllowAutoTiering: false,
			LargeCapacity:    true, // Large capacity pool
		}

		paramsToStandard := &common.UpdatePoolParams{
			SizeInBytes:          uint64(50 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false), // Try to change to standard capacity
			TotalThroughputMibps: 500,
			TotalIops:            nillable.ToPointer(int64(8000)),
		}

		err2 := _validateAndSetUpdatePoolParams(paramsToStandard, largePool)
		assert.Error(tt, err2, "Changing from large to standard capacity should fail")
		assert.True(tt, errors.IsUserInputValidationErr(err2))
		assert.Contains(tt, err2.Error(), "Large capacity cannot be changed for existing pool")

		// Test case 3: No change in LargeCapacity (standard to standard)
		noChangeStandard := &common.UpdatePoolParams{
			SizeInBytes:          uint64(15 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(false), // Same capacity type as pool
			TotalThroughputMibps: 300,
			TotalIops:            nillable.ToPointer(int64(4800)),
		}

		err3 := _validateAndSetUpdatePoolParams(noChangeStandard, standardPool)
		assert.NoError(tt, err3, "No change in capacity type should pass validation")

		// Test case 4: No change in LargeCapacity (large to large)
		noChangeLarge := &common.UpdatePoolParams{
			SizeInBytes:          uint64(120 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nillable.ToPointer(true), // Same capacity type as pool
			TotalThroughputMibps: 1200,
			TotalIops:            nillable.ToPointer(int64(19200)),
		}

		err4 := _validateAndSetUpdatePoolParams(noChangeLarge, largePool)
		assert.NoError(tt, err4, "No change in capacity type should pass validation")

		// Test case 5: LargeCapacity field omitted (nil)
		omittedCapacity := &common.UpdatePoolParams{
			SizeInBytes:          uint64(120 * utils.TiBInBytes),
			QosType:              utils.QosTypeAuto,
			LargeCapacity:        nil, // Field omitted
			TotalThroughputMibps: 1200,
			TotalIops:            nillable.ToPointer(int64(19200)),
		}

		err5 := _validateAndSetUpdatePoolParams(omittedCapacity, largePool)
		assert.NoError(tt, err5, "Omitted LargeCapacity should pass validation")
	})
}

func TestOrchestrator_GetExpertModePoolCreds(t *testing.T) {
	t.Run("WhenSuccessful", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:         "test-pool-uuid",
			AccountID:    account.ID,
			ServiceLevel: "FLEX",
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{
					{
						SecretID:      "test-secret-id",
						CertificateID: "test-cert-id",
						Password:      "test-password",
						AuthType:      2, // USER_CERTIFICATE
						Username:      "test-user_gadmin",
					},
				},
			},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		node1 := &datamodel.Node{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-node-1-uuid"},
			Name:            "test-node-1",
			PoolID:          pool.ID,
			EndpointAddress: "10.0.0.1",
			HostDNSName:     "host1.example.com",
		}
		err = store.DB().Create(node1).Error
		assert.NoError(t, err)

		node2 := &datamodel.Node{
			BaseModel:       datamodel.BaseModel{ID: 2, UUID: "test-node-2-uuid"},
			Name:            "test-node-2",
			PoolID:          pool.ID,
			EndpointAddress: "10.0.0.2",
			HostDNSName:     "host2.example.com",
		}
		err = store.DB().Create(node2).Error
		assert.NoError(t, err)

		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "gadmin")

		assert.NoError(t, err)
		assert.NotNil(t, credentials)
		assert.Equal(t, "test-user_gadmin", credentials.Username)
		assert.Equal(t, "test-secret-id", credentials.SecretID)
		assert.Equal(t, "test-cert-id", credentials.CertificateID)
		assert.Equal(t, "test-password", credentials.Password)
		assert.Equal(t, 2, credentials.AuthType)
		assert.NotNil(t, credentials.OntapEndpoints)
		assert.Len(t, credentials.OntapEndpoints, 2)
		assert.Equal(t, "10.0.0.1", credentials.OntapEndpoints[0].IP)
		assert.Equal(t, "host1.example.com", credentials.OntapEndpoints[0].DNS)
		assert.Equal(t, "10.0.0.2", credentials.OntapEndpoints[1].IP)
		assert.Equal(t, "host2.example.com", credentials.OntapEndpoints[1].DNS)
	})
	t.Run("WhenUserNotFound", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:         "test-pool-uuid",
			AccountID:    account.ID,
			ServiceLevel: "FLEX",
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{
					{
						SecretID:      "test-secret-id",
						CertificateID: "test-cert-id",
						Password:      "test-password",
						AuthType:      2, // USER_CERTIFICATE
						Username:      "different-user",
					},
				},
			},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		node1 := &datamodel.Node{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-node-1-uuid"},
			Name:            "test-node-1",
			PoolID:          pool.ID,
			EndpointAddress: "10.0.0.1",
			HostDNSName:     "host1.example.com",
		}
		err = store.DB().Create(node1).Error
		assert.NoError(t, err)

		node2 := &datamodel.Node{
			BaseModel:       datamodel.BaseModel{ID: 2, UUID: "test-node-2-uuid"},
			Name:            "test-node-2",
			PoolID:          pool.ID,
			EndpointAddress: "10.0.0.2",
			HostDNSName:     "host2.example.com",
		}
		err = store.DB().Create(node2).Error
		assert.NoError(t, err)

		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "gadmin")

		assert.Error(t, err)
		assert.Nil(t, credentials)
		assert.Contains(t, err.Error(), "expert mode user not found")
	})
	t.Run("WhenAccountNotFound", func(t *testing.T) {
		ctx, _, orch, _ := setup(t)

		// Mock getAccountWithName to return error
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.NewNotFoundErr("account not found", nil)
		}
		defer func() { getAccountWithName = _getAccountWithName }()

		// Execute
		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "non-existent-account", "gadmin")

		// Assert
		assert.Error(t, err)
		assert.Nil(t, credentials)
		assert.Contains(t, err.Error(), "account not found")
	})
	t.Run("WhenPoolNotFound", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		// Create test account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		// Mock getAccountWithName to return the test account
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = _getAccountWithName }()

		// Execute
		credentials, err := orch.GetExpertModePoolCreds(ctx, "non-existent-pool-uuid", "test_account", "gadmin")

		// Assert
		assert.Error(t, err)
		assert.Nil(t, credentials)
		assert.Contains(t, err.Error(), "Pool not found")
	})
	t.Run("WhenPoolInCreatingState", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:         "test-pool-uuid",
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateCreating,
			ServiceLevel: "FLEX",
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{
					{
						SecretID:      "test-secret-id",
						CertificateID: "test-cert-id",
						Password:      "test-password",
						AuthType:      2,
						Username:      "test-user_gadmin",
					},
				},
			},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "gadmin")

		assert.Error(t, err)
		assert.Nil(t, credentials)
		var customErr *vsaerrors.CustomError
		assert.True(t, stderrors.As(err, &customErr) && customErr.IsError(vsaerrors.ErrPoolInCreatingState))
		assert.Contains(t, err.Error(), "creating state")
	})
	t.Run("WhenPoolInDeletingState", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:         "test-pool-uuid",
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateDeleting,
			ServiceLevel: "FLEX",
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{
					{
						SecretID:      "test-secret-id",
						CertificateID: "test-cert-id",
						Password:      "test-password",
						AuthType:      2,
						Username:      "test-user_gadmin",
					},
				},
			},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "gadmin")

		assert.Error(t, err)
		assert.Nil(t, credentials)
		var customErr *vsaerrors.CustomError
		assert.True(t, stderrors.As(err, &customErr) && customErr.IsError(vsaerrors.ErrPoolInDeletingState))
		assert.Contains(t, err.Error(), "deleting state")
	})
	t.Run("WhenPoolHasNoCredentials", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:             datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:                  "test-pool-uuid",
			AccountID:             account.ID,
			ServiceLevel:          "FLEX",
			ExpertModeCredentials: nil, // No credentials
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		// Execute
		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "gadmin")

		// Assert
		assert.NoError(t, err)
		assert.Nil(t, credentials) // Should return nil when no credentials
	})
	t.Run("WhenPoolHasEmptyCredentials", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:         "test-pool-uuid",
			AccountID:    account.ID,
			ServiceLevel: "FLEX",
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{
					{
						SecretID:      "",
						CertificateID: "",
						Password:      "",
						AuthType:      0,
						Username:      "test-user_gadmin",
					},
				},
			},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		// Execute
		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "gadmin")

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, credentials)
		assert.Equal(t, "", credentials.SecretID)
		assert.Equal(t, "", credentials.CertificateID)
		assert.Equal(t, "", credentials.Password)
		assert.Equal(t, 0, credentials.AuthType)
	})
	t.Run("WhenDatabaseErrorOccurs", func(t *testing.T) {
		ctx, _, orch, _ := setup(t)

		// Mock getAccountWithName to return error
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("database connection failed")
		}
		defer func() { getAccountWithName = _getAccountWithName }()

		// Execute
		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "gadmin")

		// Assert
		assert.Error(t, err)
		assert.Nil(t, credentials)
		assert.Contains(t, err.Error(), "database connection failed")
	})
	t.Run("WhenUserNameIsEmpty", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:         "test-pool-uuid",
			AccountID:    account.ID,
			ServiceLevel: "FLEX",
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{
					{
						SecretID:      "test-secret-id",
						CertificateID: "test-cert-id",
						Password:      "test-password",
						AuthType:      1,
						Username:      "test-user_gadmin",
					},
				},
			},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		// Execute with empty userName
		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "")

		// Assert - should still work even with empty userName
		assert.Error(t, err)
		assert.Nil(t, credentials)
		assert.Equal(t, "expert mode user not found", err.Error())
	})
	t.Run("WhenContextIsCancelled", func(t *testing.T) {
		ctx, _, orch, _ := setup(t)

		// Create cancelled context
		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel() // Cancel immediately

		// Execute
		credentials, err := orch.GetExpertModePoolCreds(cancelledCtx, "test-pool-uuid", "test_account", "gadmin")

		// Assert
		assert.Error(t, err)
		assert.Nil(t, credentials)
		// The exact error depends on the database driver, but it should be an error
	})
	t.Run("WhenPoolCredentialsAreNil", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:             datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:                  "test-pool-uuid",
			AccountID:             account.ID,
			ServiceLevel:          "FLEX",
			ExpertModeCredentials: nil, // Explicitly nil
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		// Execute
		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "gadmin")

		// Assert
		assert.NoError(t, err)
		assert.Nil(t, credentials)
	})
	t.Run("WhenUserNameIsAdmin_ShouldReturnPoolCredentials", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:         "test-pool-uuid",
			AccountID:    account.ID,
			ServiceLevel: "FLEX",
			PoolCredentials: &datamodel.PoolCredentials{
				SecretID:      "pool-secret-id",
				CertificateID: "pool-cert-id",
				Password:      "pool-password",
				AuthType:      1, // USER_PASSWORD
				Username:      AdminUserName,
			},
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{
					{
						SecretID:      "expert-secret-id",
						CertificateID: "expert-cert-id",
						Password:      "expert-password",
						AuthType:      2,
						Username:      "admin", // Even if admin exists in expert mode, should use PoolCredentials
					},
				},
			},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		node1 := &datamodel.Node{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-node-1-uuid"},
			Name:            "test-node-1",
			PoolID:          pool.ID,
			EndpointAddress: "10.0.0.1",
			HostDNSName:     "host1.example.com",
		}
		err = store.DB().Create(node1).Error
		assert.NoError(t, err)

		// Execute with admin userName
		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "admin")

		// Assert - should return PoolCredentials, not ExpertModeCredentials
		assert.NoError(t, err)
		assert.NotNil(t, credentials)
		assert.Equal(t, AdminUserName, credentials.Username)
		assert.Equal(t, "pool-secret-id", credentials.SecretID)
		assert.Equal(t, "pool-cert-id", credentials.CertificateID)
		assert.Equal(t, "pool-password", credentials.Password)
		assert.Equal(t, 1, credentials.AuthType)
		assert.NotNil(t, credentials.OntapEndpoints)
		assert.Len(t, credentials.OntapEndpoints, 1)
		assert.Equal(t, "10.0.0.1", credentials.OntapEndpoints[0].IP)
	})
	t.Run("WhenUserNameIsAdmin_AndPoolCredentialsNil_ShouldReturnNil", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:       datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:            "test-pool-uuid",
			AccountID:       account.ID,
			ServiceLevel:    "FLEX",
			PoolCredentials: nil, // No pool credentials
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		// Execute with admin userName
		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "admin")

		// Assert - should return nil when PoolCredentials is nil
		assert.NoError(t, err)
		assert.Nil(t, credentials)
	})
	t.Run("WhenUserNameIsAdmin_WithCertificateAuth_ShouldUseHostDNS", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:         "test-pool-uuid",
			AccountID:    account.ID,
			ServiceLevel: "FLEX",
			PoolCredentials: &datamodel.PoolCredentials{
				SecretID:      "pool-secret-id",
				CertificateID: "pool-cert-id",
				Password:      "pool-password",
				AuthType:      2, // USER_CERTIFICATE - should use HostDNS
			},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		node1 := &datamodel.Node{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-node-1-uuid"},
			Name:            "test-node-1",
			PoolID:          pool.ID,
			EndpointAddress: "10.0.0.1",
			HostDNSName:     "host1.example.com",
		}
		err = store.DB().Create(node1).Error
		assert.NoError(t, err)

		node2 := &datamodel.Node{
			BaseModel:       datamodel.BaseModel{ID: 2, UUID: "test-node-2-uuid"},
			Name:            "test-node-2",
			PoolID:          pool.ID,
			EndpointAddress: "10.0.0.2",
			HostDNSName:     "host2.example.com",
		}
		err = store.DB().Create(node2).Error
		assert.NoError(t, err)

		// Execute with admin userName
		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "admin")

		// Assert - should use HostDNS when AuthType is USER_CERTIFICATE
		assert.NoError(t, err)
		assert.NotNil(t, credentials)
		assert.Equal(t, 2, credentials.AuthType)
		assert.Len(t, credentials.OntapEndpoints, 2)
		assert.Equal(t, "10.0.0.1", credentials.OntapEndpoints[0].IP)
		assert.Equal(t, "host1.example.com", credentials.OntapEndpoints[0].DNS) // Should use HostDNSName
		assert.Equal(t, "10.0.0.2", credentials.OntapEndpoints[1].IP)
		assert.Equal(t, "host2.example.com", credentials.OntapEndpoints[1].DNS) // Should use HostDNSName
	})
	t.Run("WhenUserNameIsNotAdmin_ShouldUseExpertModeCredentials", func(t *testing.T) {
		ctx, store, orch, _ := setup(t)

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:         "test-pool-uuid",
			AccountID:    account.ID,
			ServiceLevel: "FLEX",
			PoolCredentials: &datamodel.PoolCredentials{
				SecretID:      "pool-secret-id",
				CertificateID: "pool-cert-id",
				Password:      "pool-password",
				AuthType:      1,
			},
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{
					{
						SecretID:      "expert-secret-id",
						CertificateID: "expert-cert-id",
						Password:      "expert-password",
						AuthType:      2,
						Username:      "custom-user_gadmin",
					},
				},
			},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		node1 := &datamodel.Node{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-node-1-uuid"},
			Name:            "test-node-1",
			PoolID:          pool.ID,
			EndpointAddress: "10.0.0.1",
			HostDNSName:     "host1.example.com",
		}
		err = store.DB().Create(node1).Error
		assert.NoError(t, err)

		// Execute with non-admin userName
		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "gadmin")

		// Assert - should return ExpertModeCredentials, not PoolCredentials
		assert.NoError(t, err)
		assert.NotNil(t, credentials)
		assert.Equal(t, "custom-user_gadmin", credentials.Username)
		assert.Equal(t, "expert-secret-id", credentials.SecretID)
		assert.Equal(t, "expert-cert-id", credentials.CertificateID)
		assert.Equal(t, "expert-password", credentials.Password)
		assert.Equal(t, 2, credentials.AuthType)
	})
	t.Run("WhenOldPoolWithGcnvadminUsername_ShouldReturnCredentials", func(t *testing.T) {
		// Test backward compatibility: Old pools created before suffix-based approach
		// have expert mode username set to "gcnvadmin" (the old fixed value)
		ctx, store, orch, _ := setup(t)

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.DB().Create(account).Error
		assert.NoError(t, err)

		pool := &datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:         "test-pool-uuid",
			AccountID:    account.ID,
			ServiceLevel: "FLEX",
			PoolCredentials: &datamodel.PoolCredentials{
				SecretID:      "pool-secret-id",
				CertificateID: "pool-cert-id",
				Password:      "pool-password",
				AuthType:      1,
			},
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{
					{
						SecretID:      "expert-secret-id",
						CertificateID: "expert-cert-id",
						Password:      "expert-password",
						AuthType:      2,
						Username:      "gcnvadmin", // Old hardcoded username from before suffix-based approach
					},
				},
			},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(t, err)

		node1 := &datamodel.Node{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-node-1-uuid"},
			Name:            "test-node-1",
			PoolID:          pool.ID,
			EndpointAddress: "10.0.0.1",
			HostDNSName:     "host1.example.com",
		}
		err = store.DB().Create(node1).Error
		assert.NoError(t, err)

		// Execute with any non-admin userName - should find credentials by matching "gcnvadmin"
		credentials, err := orch.GetExpertModePoolCreds(ctx, "test-pool-uuid", "test_account", "gcnvadmin")

		// Assert - should return ExpertModeCredentials with old "gcnvadmin" username
		assert.NoError(t, err)
		assert.NotNil(t, credentials)
		assert.Equal(t, "gcnvadmin", credentials.Username)
		assert.Equal(t, "expert-secret-id", credentials.SecretID)
		assert.Equal(t, "expert-cert-id", credentials.CertificateID)
		assert.Equal(t, "expert-password", credentials.Password)
		assert.Equal(t, 2, credentials.AuthType)
		assert.NotNil(t, credentials.OntapEndpoints)
		assert.Len(t, credentials.OntapEndpoints, 1)
	})
}

func TestCreatePool_JobTypeSelection(t *testing.T) {
	// Test the job type determination logic directly using the helper functions from line 90

	t.Run("LargeCapacityPool_UsesCreateLargePoolJobType", func(tt *testing.T) {
		jobType := models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationCreate, models.PoolCategoryLargeCapacity)
		assert.Equal(tt, datamodel.JobTypeCreateLargePool, jobType)
	})

	t.Run("StandardPool_UsesCreatePoolJobType", func(tt *testing.T) {
		jobType := models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationCreate, models.PoolCategoryStandard)
		assert.Equal(tt, datamodel.JobTypeCreatePool, jobType)
	})

	t.Run("DefaultPool_UsesCreatePoolJobType", func(tt *testing.T) {
		jobType := models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationCreate, models.PoolCategoryDefault)
		assert.Equal(tt, datamodel.JobTypeCreatePool, jobType) // Default maps to standard
	})
}

func TestGetResourceJobType_PoolOperations_ComprehensiveMapping(t *testing.T) {
	// Test all combinations of pool operations and capacity types

	testCases := []struct {
		name            string
		operation       models.ResourceOperation
		isLargeCapacity bool
		expectedJobType datamodel.JobType
	}{
		// CREATE operations
		{
			name:            "Create_RegularCapacity",
			operation:       models.ResourceOperationCreate,
			isLargeCapacity: false,
			expectedJobType: datamodel.JobTypeCreatePool,
		},
		{
			name:            "Create_LargeCapacity",
			operation:       models.ResourceOperationCreate,
			isLargeCapacity: true,
			expectedJobType: datamodel.JobTypeCreateLargePool,
		},
		// UPDATE operations
		{
			name:            "Update_RegularCapacity",
			operation:       models.ResourceOperationUpdate,
			isLargeCapacity: false,
			expectedJobType: datamodel.JobTypeUpdatePool,
		},
		{
			name:            "Update_LargeCapacity",
			operation:       models.ResourceOperationUpdate,
			isLargeCapacity: true,
			expectedJobType: datamodel.JobTypeUpdateLargePool,
		},
		// DELETE operations
		{
			name:            "Delete_RegularCapacity",
			operation:       models.ResourceOperationDelete,
			isLargeCapacity: false,
			expectedJobType: datamodel.JobTypeDeletePool,
		},
		{
			name:            "Delete_LargeCapacity",
			operation:       models.ResourceOperationDelete,
			isLargeCapacity: true,
			expectedJobType: datamodel.JobTypeDeleteLargePool,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(tt *testing.T) {
			poolCategory := models.GetPoolCategory(tc.isLargeCapacity)
			jobType := models.GetResourceJobType(models.ResourceTypePool, tc.operation, poolCategory)
			assert.Equal(tt, tc.expectedJobType, jobType,
				"Expected operation %s with largeCapacity=%t to return %s, got %s",
				tc.operation, tc.isLargeCapacity, tc.expectedJobType, jobType)
		})
	}
}

func TestGetResourceJobType_PoolOperations_EdgeCases(t *testing.T) {
	// Test edge cases and boundary conditions for pool operations

	t.Run("InvalidOperation_FallsBackToCreatePool", func(tt *testing.T) {
		// Test what happens with invalid operation
		jobType := models.GetResourceJobType(models.ResourceTypePool, "INVALID_OPERATION", models.PoolCategoryStandard)
		assert.Equal(tt, datamodel.JobTypeCreatePool, jobType, "Should fallback to CREATE_POOL for invalid operation")
	})

	t.Run("InvalidOperation_WithLargeCapacity_FallsBackToCreatePool", func(tt *testing.T) {
		// Test what happens with invalid operation and large capacity
		jobType := models.GetResourceJobType(models.ResourceTypePool, "INVALID_OPERATION", models.PoolCategoryLargeCapacity)
		assert.Equal(tt, datamodel.JobTypeCreatePool, jobType, "Should fallback to CREATE_POOL for invalid operation even with large capacity")
	})

	t.Run("EmptyOperation_FallsBackToCreatePool", func(tt *testing.T) {
		// Test what happens with empty operation
		jobType := models.GetResourceJobType(models.ResourceTypePool, "", models.PoolCategoryStandard)
		assert.Equal(tt, datamodel.JobTypeCreatePool, jobType, "Should fallback to CREATE_POOL for empty operation")
	})
}

func TestGetResourceJobType_PoolOperations_AllValidCombinations(t *testing.T) {
	// Comprehensive test of all valid pool operation and capacity combinations

	testCases := []struct {
		name            string
		operation       models.ResourceOperation
		isLargeCapacity bool
		expectedJobType datamodel.JobType
		description     string
	}{
		{
			name:            "CreateRegular",
			operation:       models.ResourceOperationCreate,
			isLargeCapacity: false,
			expectedJobType: datamodel.JobTypeCreatePool,
			description:     "Create operation with regular capacity should return CREATE_POOL",
		},
		{
			name:            "CreateLarge",
			operation:       models.ResourceOperationCreate,
			isLargeCapacity: true,
			expectedJobType: datamodel.JobTypeCreateLargePool,
			description:     "Create operation with large capacity should return CREATE_LARGE_POOL",
		},
		{
			name:            "UpdateRegular",
			operation:       models.ResourceOperationUpdate,
			isLargeCapacity: false,
			expectedJobType: datamodel.JobTypeUpdatePool,
			description:     "Update operation with regular capacity should return UPDATE_POOL",
		},
		{
			name:            "UpdateLarge",
			operation:       models.ResourceOperationUpdate,
			isLargeCapacity: true,
			expectedJobType: datamodel.JobTypeUpdateLargePool,
			description:     "Update operation with large capacity should return UPDATE_LARGE_POOL",
		},
		{
			name:            "DeleteRegular",
			operation:       models.ResourceOperationDelete,
			isLargeCapacity: false,
			expectedJobType: datamodel.JobTypeDeletePool,
			description:     "Delete operation with regular capacity should return DELETE_POOL",
		},
		{
			name:            "DeleteLarge",
			operation:       models.ResourceOperationDelete,
			isLargeCapacity: true,
			expectedJobType: datamodel.JobTypeDeleteLargePool,
			description:     "Delete operation with large capacity should return DELETE_LARGE_POOL",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(tt *testing.T) {
			poolCategory := models.GetPoolCategory(tc.isLargeCapacity)
			jobType := models.GetResourceJobType(models.ResourceTypePool, tc.operation, poolCategory)
			assert.Equal(tt, tc.expectedJobType, jobType, tc.description)
		})
	}
}

func TestGetResourceJobType_PoolOperations_OperationConstants(t *testing.T) {
	// Test that the pool operation constants are correctly defined and used

	t.Run("OperationConstantsAreDefined", func(tt *testing.T) {
		// Verify all operation constants exist and have expected values
		assert.Equal(tt, models.ResourceOperation("CREATE"), models.ResourceOperationCreate)
		assert.Equal(tt, models.ResourceOperation("UPDATE"), models.ResourceOperationUpdate)
		assert.Equal(tt, models.ResourceOperation("DELETE"), models.ResourceOperationDelete)
	})

	t.Run("JobTypeConstantsAreDefined", func(tt *testing.T) {
		// Verify all job type constants exist and have expected values
		assert.Equal(tt, datamodel.JobType("CREATE_POOL"), datamodel.JobTypeCreatePool)
		assert.Equal(tt, datamodel.JobType("CREATE_LARGE_POOL"), datamodel.JobTypeCreateLargePool)
		assert.Equal(tt, datamodel.JobType("UPDATE_POOL"), datamodel.JobTypeUpdatePool)
		assert.Equal(tt, datamodel.JobType("UPDATE_LARGE_POOL"), datamodel.JobTypeUpdateLargePool)
		assert.Equal(tt, datamodel.JobType("DELETE_POOL"), datamodel.JobTypeDeletePool)
		assert.Equal(tt, datamodel.JobType("DELETE_LARGE_POOL"), datamodel.JobTypeDeleteLargePool)
	})
}

func TestGetResourceJobType_PoolOperations_CapacityFlagBehavior(t *testing.T) {
	// Test the behavior of the isLargeCapacity flag across all pool operations

	operations := []models.ResourceOperation{
		models.ResourceOperationCreate,
		models.ResourceOperationUpdate,
		models.ResourceOperationDelete,
	}

	for _, operation := range operations {
		t.Run(fmt.Sprintf("%s_CapacityFlagDifference", operation), func(tt *testing.T) {
			regularJobType := models.GetResourceJobType(models.ResourceTypePool, operation, models.PoolCategoryStandard)
			largeJobType := models.GetResourceJobType(models.ResourceTypePool, operation, models.PoolCategoryLargeCapacity)

			// Verify that the capacity flag actually changes the result
			assert.NotEqual(tt, regularJobType, largeJobType,
				"Regular and large capacity should return different job types for operation %s", operation)

			// Verify the job types contain the expected patterns
			regularStr := string(regularJobType)
			largeStr := string(largeJobType)

			assert.NotContains(tt, regularStr, "LARGE",
				"Regular capacity job type should not contain 'LARGE' for operation %s", operation)
			assert.Contains(tt, largeStr, "LARGE",
				"Large capacity job type should contain 'LARGE' for operation %s", operation)
		})
	}
}

func TestGetResourceJobType_PoolOperations_Consistency(t *testing.T) {
	// Test consistency and predictability of the pool job type function

	t.Run("FunctionIsIdempotent", func(tt *testing.T) {
		// Test that calling the function multiple times with same inputs returns same result
		operation := models.ResourceOperationCreate
		isLargeCapacity := true

		poolCategory := models.GetPoolCategory(isLargeCapacity)
		result1 := models.GetResourceJobType(models.ResourceTypePool, operation, poolCategory)
		result2 := models.GetResourceJobType(models.ResourceTypePool, operation, poolCategory)
		result3 := models.GetResourceJobType(models.ResourceTypePool, operation, poolCategory)

		assert.Equal(tt, result1, result2, "Function should be idempotent")
		assert.Equal(tt, result2, result3, "Function should be idempotent")
	})

	t.Run("JobTypeMapping_IsComplete", func(tt *testing.T) {
		// Verify that every valid operation has both regular and large capacity mappings
		operations := []models.ResourceOperation{
			models.ResourceOperationCreate,
			models.ResourceOperationUpdate,
			models.ResourceOperationDelete,
		}

		for _, operation := range operations {
			regularJobType := models.GetResourceJobType(models.ResourceTypePool, operation, models.PoolCategoryStandard)
			largeJobType := models.GetResourceJobType(models.ResourceTypePool, operation, models.PoolCategoryLargeCapacity)

			// Neither should fallback to the default CREATE_POOL (unless it's actually CREATE operation)
			if operation != models.ResourceOperationCreate {
				assert.NotEqual(tt, datamodel.JobTypeCreatePool, regularJobType,
					"Operation %s should not fallback to CREATE_POOL", operation)
				assert.NotEqual(tt, datamodel.JobTypeCreatePool, largeJobType,
					"Operation %s with large capacity should not fallback to CREATE_POOL", operation)
			}

			// Both should return valid, non-empty job types
			assert.NotEmpty(tt, string(regularJobType), "Should return non-empty job type for %s regular", operation)
			assert.NotEmpty(tt, string(largeJobType), "Should return non-empty job type for %s large", operation)
		}
	})
}

func TestGetResourceJobType_Comprehensive(t *testing.T) {
	// Test the new generic resource job type function

	t.Run("PoolOperations", func(tt *testing.T) {
		// Test all pool operations with both capacity types
		poolTestCases := []struct {
			name            string
			operation       models.ResourceOperation
			isLargeCapacity bool
			expectedJobType datamodel.JobType
			description     string
		}{
			{
				name:            "Pool_Create_Regular",
				operation:       models.ResourceOperationCreate,
				isLargeCapacity: false,
				expectedJobType: datamodel.JobTypeCreatePool,
				description:     "Pool create with regular capacity",
			},
			{
				name:            "Pool_Create_Large",
				operation:       models.ResourceOperationCreate,
				isLargeCapacity: true,
				expectedJobType: datamodel.JobTypeCreateLargePool,
				description:     "Pool create with large capacity",
			},
			{
				name:            "Pool_Update_Regular",
				operation:       models.ResourceOperationUpdate,
				isLargeCapacity: false,
				expectedJobType: datamodel.JobTypeUpdatePool,
				description:     "Pool update with regular capacity",
			},
			{
				name:            "Pool_Update_Large",
				operation:       models.ResourceOperationUpdate,
				isLargeCapacity: true,
				expectedJobType: datamodel.JobTypeUpdateLargePool,
				description:     "Pool update with large capacity",
			},
			{
				name:            "Pool_Delete_Regular",
				operation:       models.ResourceOperationDelete,
				isLargeCapacity: false,
				expectedJobType: datamodel.JobTypeDeletePool,
				description:     "Pool delete with regular capacity",
			},
			{
				name:            "Pool_Delete_Large",
				operation:       models.ResourceOperationDelete,
				isLargeCapacity: true,
				expectedJobType: datamodel.JobTypeDeleteLargePool,
				description:     "Pool delete with large capacity",
			},
		}

		for _, tc := range poolTestCases {
			tt.Run(tc.name, func(ttt *testing.T) {
				poolCategory := models.GetPoolCategory(tc.isLargeCapacity)
				jobType := models.GetResourceJobType(models.ResourceTypePool, tc.operation, poolCategory)
				assert.Equal(ttt, tc.expectedJobType, jobType, tc.description)
			})
		}
	})

	t.Run("SubnetOperations", func(tt *testing.T) {
		// Test subnet operations (only CREATE is supported)
		subnetTestCases := []struct {
			name            string
			operation       models.ResourceOperation
			isLargeCapacity bool
			expectedJobType datamodel.JobType
			description     string
		}{
			{
				name:            "Subnet_Create_Regular",
				operation:       models.ResourceOperationCreate,
				isLargeCapacity: false,
				expectedJobType: datamodel.JobTypeCreateSubnet,
				description:     "Subnet create with regular capacity",
			},
			{
				name:            "Subnet_Create_Large",
				operation:       models.ResourceOperationCreate,
				isLargeCapacity: true,
				expectedJobType: datamodel.JobTypeCreateLargeSubnet,
				description:     "Subnet create with large capacity",
			},
		}

		for _, tc := range subnetTestCases {
			tt.Run(tc.name, func(ttt *testing.T) {
				poolCategory := models.GetPoolCategory(tc.isLargeCapacity)
				jobType := models.GetResourceJobType(models.ResourceTypeSubnet, tc.operation, poolCategory)
				assert.Equal(ttt, tc.expectedJobType, jobType, tc.description)
			})
		}
	})

	t.Run("EdgeCases", func(tt *testing.T) {
		// Test invalid combinations fall back to default

		t.Run("InvalidResourceType", func(ttt *testing.T) {
			jobType := models.GetResourceJobType("INVALID_RESOURCE", models.ResourceOperationCreate, models.PoolCategoryStandard)
			assert.Equal(ttt, datamodel.JobTypeCreatePool, jobType, "Should fallback to CREATE_POOL for invalid resource type")
		})

		t.Run("InvalidOperation", func(ttt *testing.T) {
			jobType := models.GetResourceJobType(models.ResourceTypePool, "INVALID_OPERATION", models.PoolCategoryStandard)
			assert.Equal(ttt, datamodel.JobTypeCreatePool, jobType, "Should fallback to CREATE_POOL for invalid operation")
		})

		t.Run("UnsupportedSubnetOperation", func(ttt *testing.T) {
			// Subnets don't support UPDATE operations
			jobType := models.GetResourceJobType(models.ResourceTypeSubnet, models.ResourceOperationUpdate, models.PoolCategoryStandard)
			assert.Equal(ttt, datamodel.JobTypeCreatePool, jobType, "Should fallback to CREATE_POOL for unsupported subnet operation")
		})
	})
	t.Run("KmsConfigIsNotReady", func(tt *testing.T) {
		logger := log.NewLogger()
		ipos := int64(160001)
		params := &common.CreatePoolParams{
			SizeInBytes:             uint64(1 * utils.TiBInBytes),
			ServiceLevel:            ServiceLevelNameFLEX,
			QosType:                 utils.QosTypeAuto,
			CustomPerformanceParams: &common.CustomPerformanceParams{ThroughputMibps: 64, Iops: &ipos}, // Just above 160000 IOPS
			KmsConfig: &models.KmsConfig{
				State: datamodel.LifeCycleStateKeyCheckPending,
			},
		}
		defer func() {
			ValidatePoolParams = _validatePoolParams
		}()
		ValidatePoolParams = func(perf *validators.CustomPerformance, serviceLevel string) error {
			return nil
		}
		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "KMS configuration state")
		assert.Contains(tt, err.Error(), "KEY_CHECK_PENDING")
	})
	t.Run("KmsConfigIsCreatesState", func(tt *testing.T) {
		logger := log.NewLogger()
		ipos := int64(160001)
		params := &common.CreatePoolParams{
			SizeInBytes:             uint64(1 * utils.TiBInBytes),
			ServiceLevel:            ServiceLevelNameFLEX,
			CustomPerformanceParams: &common.CustomPerformanceParams{ThroughputMibps: 64, Iops: &ipos}, // Just above 160000 IOPS
			KmsConfig: &models.KmsConfig{
				State: datamodel.LifeCycleStateCreated,
			},
		}
		defer func() {
			ValidatePoolParams = _validatePoolParams
		}()
		ValidatePoolParams = func(perf *validators.CustomPerformance, serviceLevel string) error {
			return nil
		}
		err := _validateCreatePoolParams(params, logger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "KMS configuration state")
		assert.Contains(tt, err.Error(), "KEY_CHECK_PENDING")
	})
}

func TestCreatePoolInDB_TrueZonal(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
	store, err := database.SetupStorageForTest(mockLogger)
	assert.NoError(t, err)
	err = database.ClearInMemoryDB(store.DB())
	assert.NoError(t, err)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.DB().Create(account).Error
	assert.NoError(t, err)

	iopsValue := int64(1024)
	params := &common.CreatePoolParams{
		AccountName:    "test_account",
		Region:         "us-central1",
		Name:           "true-zonal-pool",
		VendorID:       "/projects/test/locations/us-central1/pools/true-zonal-pool",
		ServiceLevel:   "FLEX",
		SizeInBytes:    1073741824,
		PrimaryZone:    "us-central1-a",
		VendorSubNetID: "projects/test/networks/test",
		IsTrueZonal:    true,
		IsPreview:      true,
		SecondaryZone:  "us-central1-a",
		LargeCapacity:  true,
		CustomPerformanceParams: &common.CustomPerformanceParams{
			Enabled:         true,
			ThroughputMibps: 64,
			Iops:            &iopsValue,
		},
	}

	pool, err := commonfactory.CreatePoolInDB(ctx, store, params, account, mockLogger, nil, TieringFullnessThresholdOntapDefault)
	assert.NoError(t, err)
	assert.NotNil(t, pool)
	assert.NotNil(t, pool.PoolAttributes)
	assert.True(t, pool.PoolAttributes.IsTrueZonal)
	assert.True(t, pool.IsPreview)
	assert.Equal(t, "us-central1-a", pool.PoolAttributes.SecondaryZone)
}

func TestCreatePoolInDB_ActiveDirectoryConfigId(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
	store, err := database.SetupStorageForTest(mockLogger)
	assert.NoError(t, err)
	err = database.ClearInMemoryDB(store.DB())
	assert.NoError(t, err)

	// Create account
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.DB().Create(account).Error
	assert.NoError(t, err)

	t.Run("CreatePoolWithoutActiveDirectoryConfigId", func(tt *testing.T) {
		iopsValue := int64(1024)
		params := &common.CreatePoolParams{
			AccountName:    "test_account",
			Region:         "us-central1",
			Name:           "test-pool-no-ad",
			Description:    "Test pool without AD",
			VendorID:       "/projects/test/locations/us-central1/pools/test-pool-no-ad",
			ServiceLevel:   "FLEX",
			SizeInBytes:    1073741824,
			PrimaryZone:    "us-central1-a",
			VendorSubNetID: "projects/test/networks/test",
			// ActiveDirectoryId: "", // Empty
			CustomPerformanceParams: &common.CustomPerformanceParams{
				Enabled:         true,
				ThroughputMibps: 64,
				Iops:            &iopsValue,
			},
		}

		pool, err := commonfactory.CreatePoolInDB(ctx, store, params, account, mockLogger, nil, TieringFullnessThresholdOntapDefault)
		assert.NoError(tt, err)
		assert.NotNil(tt, pool)
		// ActiveDirectoryID should be 0 (null in database)
		assert.Equal(tt, int64(0), pool.ActiveDirectoryID.Int64)
	})
}

func TestCreatePoolIntegration_ActiveDirectoryConfigId(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
	store, err := database.SetupStorageForTest(mockLogger)
	assert.NoError(t, err)
	err = database.ClearInMemoryDB(store.DB())
	assert.NoError(t, err)

	// Create account
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.DB().Create(account).Error
	assert.NoError(t, err)

	t.Run("CompleteFlow_CreatePoolWithActiveDirectoryConfigId", func(tt *testing.T) {
		// Create Active Directory first
		ad := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{
				UUID: "550e8400-e29b-41d4-a716-446655440000",
			},
			AdName:    "test-active-directory",
			AccountId: account.ID,
		}
		err = store.DB().Create(ad).Error
		require.NoError(tt, err)
		ad, err := store.GetActiveDirectoryByUuidAndAccountId(ctx, ad.UUID, account.ID)
		require.NoError(tt, err)
		require.NotNil(tt, ad, "ActiveDirectory should be found after create")
		// Test the complete flow
		iopsValue := int64(1024)
		params := &common.CreatePoolParams{
			AccountName:       "test_account",
			Region:            "us-central1",
			Name:              "integration-test-pool",
			Description:       "Integration test pool with AD",
			VendorID:          "/projects/test/locations/us-central1/pools/integration-test-pool",
			ServiceLevel:      "FLEX",
			SizeInBytes:       1073741824,
			PrimaryZone:       "us-central1-a",
			VendorSubNetID:    "projects/test/networks/test",
			ActiveDirectoryId: "550e8400-e29b-41d4-a716-446655440000",
			ADExistsInVCP:     true, // AD exists in VCP, so ActiveDirectoryID should be set
			ActiveDirectory: &models.ActiveDirectory{
				BaseModel: models.BaseModel{
					ID:   ad.ID,
					UUID: ad.UUID,
				},
			},
			CustomPerformanceParams: &common.CustomPerformanceParams{
				Enabled:         true,
				ThroughputMibps: 64,
				Iops:            &iopsValue,
			},
		}

		// Mock the validation functions to avoid complex setup
		originalValidateCreatePoolParams := ValidateCreatePoolParams
		defer func() { ValidateCreatePoolParams = originalValidateCreatePoolParams }()
		ValidateCreatePoolParams = func(params *common.CreatePoolParams, logger log.Logger) error {
			return nil
		}

		originalValidatePoolParams := ValidatePoolParams
		defer func() { ValidatePoolParams = originalValidatePoolParams }()
		ValidatePoolParams = func(perf *validators.CustomPerformance, serviceLevel string) error {
			return nil
		}

		pool, err := commonfactory.CreatePoolInDB(ctx, store, params, account, mockLogger, nil, TieringFullnessThresholdOntapDefault)
		require.NoError(tt, err)
		require.NotNil(tt, pool)
		require.True(tt, pool.ActiveDirectoryID.Valid, "ActiveDirectoryID should be set when AD exists in VCP")
		assert.Equal(tt, ad.ID, pool.ActiveDirectoryID.Int64)

		// Verify the pool was created in the database with the AD association
		var dbPool datamodel.Pool
		err = store.DB().Preload("ActiveDirectory").First(&dbPool, "uuid = ?", pool.UUID).Error
		require.NoError(tt, err)
		require.NotNil(tt, dbPool.ActiveDirectory, "dbPool.ActiveDirectory should be preloaded")
		assert.Equal(tt, params.Name, dbPool.Name)
		assert.Equal(tt, ad.ID, dbPool.ActiveDirectoryID.Int64)
		assert.Equal(tt, ad.UUID, dbPool.ActiveDirectory.UUID)
	})

	t.Run("CompleteFlow_CreatePoolWithoutActiveDirectoryConfigId", func(tt *testing.T) {
		iopsValue := int64(1024)
		params := &common.CreatePoolParams{
			AccountName:       "test_account",
			Region:            "us-central1",
			Name:              "integration-test-pool-no-ad",
			Description:       "Integration test pool without AD",
			VendorID:          "/projects/test/locations/us-central1/pools/integration-test-pool-no-ad",
			ServiceLevel:      "FLEX",
			SizeInBytes:       1073741824,
			PrimaryZone:       "us-central1-a",
			VendorSubNetID:    "projects/test/networks/test",
			ActiveDirectoryId: "", // Empty
			CustomPerformanceParams: &common.CustomPerformanceParams{
				Enabled:         true,
				ThroughputMibps: 64,
				Iops:            &iopsValue,
			},
		}

		// Mock the validation functions
		originalValidateCreatePoolParams := ValidateCreatePoolParams
		defer func() { ValidateCreatePoolParams = originalValidateCreatePoolParams }()
		ValidateCreatePoolParams = func(params *common.CreatePoolParams, logger log.Logger) error {
			return nil
		}

		originalValidatePoolParams := ValidatePoolParams
		defer func() { ValidatePoolParams = originalValidatePoolParams }()
		ValidatePoolParams = func(perf *validators.CustomPerformance, serviceLevel string) error {
			return nil
		}

		pool, err := commonfactory.CreatePoolInDB(ctx, store, params, account, mockLogger, nil, TieringFullnessThresholdOntapDefault)
		require.NoError(tt, err)
		require.NotNil(tt, pool)

		// Verify the ActiveDirectoryID is 0 (null in database)
		assert.Equal(tt, int64(0), pool.ActiveDirectoryID.Int64)

		// Verify the pool was created in the database
		var dbPool datamodel.Pool
		err = store.DB().Preload("ActiveDirectory").First(&dbPool, "uuid = ?", pool.UUID).Error
		require.NoError(tt, err)
		assert.Equal(tt, int64(0), dbPool.ActiveDirectoryID.Int64)
	})

	t.Run("CompleteFlow_CreatePoolWithADFromSDE_NotInVCP", func(tt *testing.T) {
		// This test simulates when AD is from SDE/CVP (not yet synced to VCP)
		// ActiveDirectoryId is set (UUID), but ADExistsInVCP is false
		// The pool should be created WITHOUT ActiveDirectoryID set
		// The AD sync will happen later in the workflow
		iopsValue := int64(1024)
		params := &common.CreatePoolParams{
			AccountName:       "test_account",
			Region:            "us-central1",
			Name:              "integration-test-pool-ad-from-sde",
			Description:       "Integration test pool with AD from SDE",
			VendorID:          "/projects/test/locations/us-central1/pools/integration-test-pool-ad-from-sde",
			ServiceLevel:      "FLEX",
			SizeInBytes:       1073741824,
			PrimaryZone:       "us-central1-a",
			VendorSubNetID:    "projects/test/networks/test",
			ActiveDirectoryId: "sde-ad-uuid-not-in-vcp", // UUID from SDE
			ADExistsInVCP:     false,                    // AD does NOT exist in VCP yet
			ActiveDirectory: &models.ActiveDirectory{
				BaseModel: models.BaseModel{
					ID:   0, // ID is 0 because AD is not in VCP database
					UUID: "sde-ad-uuid-not-in-vcp",
				},
				AdName: "sde-active-directory",
			},
			CustomPerformanceParams: &common.CustomPerformanceParams{
				Enabled:         true,
				ThroughputMibps: 64,
				Iops:            &iopsValue,
			},
		}

		// Mock the validation functions
		originalValidateCreatePoolParams := ValidateCreatePoolParams
		defer func() { ValidateCreatePoolParams = originalValidateCreatePoolParams }()
		ValidateCreatePoolParams = func(params *common.CreatePoolParams, logger log.Logger) error {
			return nil
		}

		originalValidatePoolParams := ValidatePoolParams
		defer func() { ValidatePoolParams = originalValidatePoolParams }()
		ValidatePoolParams = func(perf *validators.CustomPerformance, serviceLevel string) error {
			return nil
		}

		pool, err := commonfactory.CreatePoolInDB(ctx, store, params, account, mockLogger, nil, TieringFullnessThresholdOntapDefault)
		require.NoError(tt, err)
		require.NotNil(tt, pool)

		// Verify the ActiveDirectoryID is NOT set (should be null/invalid)
		// This is the key assertion - when AD is from SDE, we should NOT set the FK
		assert.False(tt, pool.ActiveDirectoryID.Valid, "ActiveDirectoryID should not be valid when AD is from SDE")
		assert.Equal(tt, int64(0), pool.ActiveDirectoryID.Int64)

		// Verify the pool was created in the database without AD reference
		var dbPool datamodel.Pool
		err = store.DB().First(&dbPool, "uuid = ?", pool.UUID).Error
		require.NoError(tt, err)
		assert.Equal(tt, params.Name, dbPool.Name)
		assert.False(tt, dbPool.ActiveDirectoryID.Valid, "DB ActiveDirectoryID should not be valid when AD is from SDE")
	})
}

// setupGCPCredentialsForTest is a helper function that mimics setupGCPCredentials from pool.go
func setupGCPCredentialsForTest(poolObj *datamodel.Pool, params *common.CreatePoolParams, accountName string) {
	userName := utils.GenerateUniqueUsername(poolObj.DeploymentName)
	switch env.AuthType {
	case env.USER_CERTIFICATE:
		poolObj.PoolCredentials = &datamodel.PoolCredentials{
			SecretID:      fmt.Sprintf("%s-secret", poolObj.DeploymentName),
			CertificateID: fmt.Sprintf("%s-cert", poolObj.DeploymentName),
			Password:      "",
			AuthType:      env.USER_CERTIFICATE,
			CaURI:         env.BuildCaURI(env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName),
			Username:      fmt.Sprintf("%s%s", userName, VCP_ADMIN_CERT_UN_SUFFIX),
		}
	case env.USERNAME_PWD_SEC_MGR:
		poolObj.PoolCredentials = &datamodel.PoolCredentials{
			SecretID:      fmt.Sprintf("%s-secret", poolObj.DeploymentName),
			CertificateID: "",
			Password:      "",
			AuthType:      env.USERNAME_PWD_SEC_MGR,
			Username:      AdminUserName,
		}
	default:
		poolObj.PoolCredentials = &datamodel.PoolCredentials{
			SecretID:      "",
			CertificateID: "",
			Password:      env.NodePassword,
			AuthType:      env.USERNAME_PWD,
			Username:      AdminUserName,
		}
	}

	if params.Mode == common.ONTAPMode {
		expUserName := fmt.Sprintf("%s_%s", userName, env.ExpertModeUserSuffix)
		poolObj.ExpertModeCredentials = createExpertModeUser(poolObj, expUserName)
	}
}

func TestCreatePoolInDB_PoolCredentials(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
	store, err := database.SetupStorageForTest(mockLogger)
	assert.NoError(t, err)
	err = database.ClearInMemoryDB(store.DB())
	assert.NoError(t, err)

	// Create account
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.DB().Create(account).Error
	assert.NoError(t, err)

	// Mock the validation functions
	originalValidateCreatePoolParams := ValidateCreatePoolParams
	defer func() { ValidateCreatePoolParams = originalValidateCreatePoolParams }()
	ValidateCreatePoolParams = func(params *common.CreatePoolParams, logger log.Logger) error {
		return nil
	}

	originalValidatePoolParams := ValidatePoolParams
	defer func() { ValidatePoolParams = originalValidatePoolParams }()
	ValidatePoolParams = func(perf *validators.CustomPerformance, serviceLevel string) error {
		return nil
	}

	t.Run("WhenAuthTypeIsUSERNAME_PWD_SEC_MGR_ShouldSetCorrectPoolCredentials", func(tt *testing.T) {
		// Save original values
		originalAuthType := env.AuthType
		originalNodePassword := env.NodePassword
		defer func() {
			env.AuthType = originalAuthType
			env.NodePassword = originalNodePassword
		}()

		// Set AuthType to USERNAME_PWD_SEC_MGR
		env.AuthType = env.USERNAME_PWD_SEC_MGR

		iopsValue := int64(1024)
		params := &common.CreatePoolParams{
			AccountName:    "test_account",
			Region:         "us-central1",
			Name:           "test-pool-sec-mgr",
			Description:    "Test pool with secret manager",
			VendorID:       "/projects/test/locations/us-central1/pools/test-pool-sec-mgr",
			ServiceLevel:   "FLEX",
			SizeInBytes:    1073741824,
			PrimaryZone:    "us-central1-a",
			VendorSubNetID: "projects/test/networks/test",
			CustomPerformanceParams: &common.CustomPerformanceParams{
				Enabled:         true,
				ThroughputMibps: 64,
				Iops:            &iopsValue,
			},
		}

		pool, err := commonfactory.CreatePoolInDB(ctx, store, params, account, mockLogger, setupGCPCredentialsForTest, TieringFullnessThresholdOntapDefault)
		assert.NoError(tt, err)
		assert.NotNil(tt, pool)
		assert.NotNil(tt, pool.PoolCredentials)

		// Verify PoolCredentials for USERNAME_PWD_SEC_MGR case
		assert.Equal(tt, env.USERNAME_PWD_SEC_MGR, pool.PoolCredentials.AuthType)
		assert.Equal(tt, fmt.Sprintf("%s-secret", pool.DeploymentName), pool.PoolCredentials.SecretID)
		assert.Equal(tt, "", pool.PoolCredentials.CertificateID)
		assert.Equal(tt, "", pool.PoolCredentials.Password)
		assert.Equal(tt, AdminUserName, pool.PoolCredentials.Username)
	})

	t.Run("WhenAuthTypeIsDefault_ShouldSetCorrectPoolCredentials", func(tt *testing.T) {
		// Save original values
		originalAuthType := env.AuthType
		originalNodePassword := env.NodePassword
		defer func() {
			env.AuthType = originalAuthType
			env.NodePassword = originalNodePassword
		}()

		// Set AuthType to USERNAME_PWD (default case)
		env.AuthType = env.USERNAME_PWD
		env.NodePassword = "test-node-password"

		iopsValue := int64(1024)
		params := &common.CreatePoolParams{
			AccountName:    "test_account",
			Region:         "us-central1",
			Name:           "test-pool-default",
			Description:    "Test pool with default auth",
			VendorID:       "/projects/test/locations/us-central1/pools/test-pool-default",
			ServiceLevel:   "FLEX",
			SizeInBytes:    1073741824,
			PrimaryZone:    "us-central1-a",
			VendorSubNetID: "projects/test/networks/test",
			CustomPerformanceParams: &common.CustomPerformanceParams{
				Enabled:         true,
				ThroughputMibps: 64,
				Iops:            &iopsValue,
			},
		}

		pool, err := commonfactory.CreatePoolInDB(ctx, store, params, account, mockLogger, setupGCPCredentialsForTest, TieringFullnessThresholdOntapDefault)
		assert.NoError(tt, err)
		assert.NotNil(tt, pool)
		assert.NotNil(tt, pool.PoolCredentials)

		// Verify PoolCredentials for default case
		assert.Equal(tt, env.USERNAME_PWD, pool.PoolCredentials.AuthType)
		assert.Equal(tt, "", pool.PoolCredentials.SecretID)
		assert.Equal(tt, "", pool.PoolCredentials.CertificateID)
		assert.Equal(tt, env.NodePassword, pool.PoolCredentials.Password)
		assert.Equal(tt, AdminUserName, pool.PoolCredentials.Username)
	})
}

// TestMergeUpdateParamsIntoPoolModel tests the mergeUpdateParamsIntoPoolModel function
// Only pool size and auto tiering parameters are updated from params if provided.
// All other fields remain from poolModel.
func TestMergeUpdateParamsIntoPoolModel(t *testing.T) {
	basePoolModel := &models.Pool{
		BaseModel: models.BaseModel{
			UUID: "test-pool-uuid",
		},
		Name:             "test-pool",
		Description:      "original description",
		SizeInBytes:      2 * utils.TiBInBytes,
		QosType:          "auto",
		AllowAutoTiering: false,
		PoolAttributes: &models.PoolAttributes{
			Labels: map[string]string{
				"env":  "dev",
				"team": "storage",
			},
		},
		CustomPerformanceParams: &models.CustomPerformanceParams{
			Enabled:    true,
			Throughput: 64.0,
			Iops:       1024,
		},
		ActiveDirectoryConfigId: "original-ad-id",
	}

	t.Run("OnlySizeInBytesUpdated_OtherFieldsUnchanged", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes: 4 * utils.TiBInBytes, // Provided in params
		}

		merged := mergeUpdateParamsIntoPoolModel(basePoolModel, params)

		// SizeInBytes should be updated from params
		assert.Equal(tt, uint64(4*utils.TiBInBytes), merged.SizeInBytes)
		// All other fields should remain from poolModel
		assert.Equal(tt, basePoolModel.Description, merged.Description)
		assert.Equal(tt, basePoolModel.QosType, merged.QosType)
		assert.Equal(tt, basePoolModel.AllowAutoTiering, merged.AllowAutoTiering)
		assert.Equal(tt, basePoolModel.Name, merged.Name)
		assert.Equal(tt, basePoolModel.CustomPerformanceParams.Throughput, merged.CustomPerformanceParams.Throughput)
		assert.Equal(tt, basePoolModel.CustomPerformanceParams.Iops, merged.CustomPerformanceParams.Iops)
	})

	t.Run("SizeInBytesAndAutoTieringUpdated", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:             4 * utils.TiBInBytes,
			AllowAutoTiering:        true,
			HotTierSizeInBytes:      2 * utils.TiBInBytes,
			EnableHotTierAutoResize: true,
		}

		merged := mergeUpdateParamsIntoPoolModel(basePoolModel, params)

		// SizeInBytes should be updated from params
		assert.Equal(tt, uint64(4*utils.TiBInBytes), merged.SizeInBytes)
		// AutoTieringConfig should be created and updated
		assert.NotNil(tt, merged.AutoTieringConfig)
		assert.Equal(tt, uint64(2*utils.TiBInBytes), merged.AutoTieringConfig.HotTierSizeInBytes)
		assert.Equal(tt, true, merged.AutoTieringConfig.EnableHotTierAutoResize)
		// Other fields remain from poolModel
		assert.Equal(tt, basePoolModel.Description, merged.Description)
		assert.Equal(tt, basePoolModel.QosType, merged.QosType)
	})

	t.Run("DescriptionNotUpdated_RemainsFromPoolModel", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes: 4 * utils.TiBInBytes,
			Description: "updated description", // Provided but not used
		}

		merged := mergeUpdateParamsIntoPoolModel(basePoolModel, params)

		// Description should remain from poolModel, not updated from params
		assert.Equal(tt, basePoolModel.Description, merged.Description)
		assert.Equal(tt, "original description", merged.Description)
	})

	t.Run("LabelsNotUpdated_RemainFromPoolModel", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes: 4 * utils.TiBInBytes,
			Labels: &datamodel.JSONB{
				"env":  "prod",
				"team": "platform",
				"new":  "value",
			},
		}

		merged := mergeUpdateParamsIntoPoolModel(basePoolModel, params)

		// Labels should remain from poolModel, not updated from params
		assert.NotNil(tt, merged.PoolAttributes)
		assert.NotNil(tt, merged.PoolAttributes.Labels)
		assert.Equal(tt, "dev", merged.PoolAttributes.Labels["env"])
		assert.Equal(tt, "storage", merged.PoolAttributes.Labels["team"])
		assert.NotContains(tt, merged.PoolAttributes.Labels, "new")
	})

	t.Run("ThroughputAndIOPSNotUpdated_RemainFromPoolModel", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:              4 * utils.TiBInBytes,
			TotalThroughputMibps:     128,
			TotalIops:                nillable.ToPointer(int64(2048)),
			CustomPerformanceEnabled: true,
		}

		merged := mergeUpdateParamsIntoPoolModel(basePoolModel, params)

		// Throughput and IOPS should remain from poolModel, not updated from params
		assert.NotNil(tt, merged.CustomPerformanceParams)
		assert.Equal(tt, 64.0, merged.CustomPerformanceParams.Throughput)  // Original value
		assert.Equal(tt, int64(1024), merged.CustomPerformanceParams.Iops) // Original value
	})

	t.Run("AutoTieringConfigUpdated_ParamsUsedDirectly", func(tt *testing.T) {
		poolWithAutoTiering := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      2 * utils.TiBInBytes,
			AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      1 * utils.TiBInBytes,
				EnableHotTierAutoResize: false,
				BucketName:              "original-bucket",
			},
		}

		params := &common.UpdatePoolParams{
			Description:             poolWithAutoTiering.Description,
			SizeInBytes:             poolWithAutoTiering.SizeInBytes,
			AllowAutoTiering:        true,
			HotTierSizeInBytes:      2 * utils.TiBInBytes, // Params contain merged value
			EnableHotTierAutoResize: true,                 // Params contain merged value
		}

		merged := mergeUpdateParamsIntoPoolModel(poolWithAutoTiering, params)

		// Params already contain merged values, so they are used directly without conditional checks
		assert.NotNil(tt, merged.AutoTieringConfig)
		// HotTierSizeInBytes from params is used directly (merged value from request or existing pool)
		assert.Equal(tt, uint64(2*utils.TiBInBytes), merged.AutoTieringConfig.HotTierSizeInBytes)
		// EnableHotTierAutoResize from params is used directly (merged value from request or existing pool)
		assert.Equal(tt, true, merged.AutoTieringConfig.EnableHotTierAutoResize)
		// Existing fields from deep copy should be preserved (BucketName not in params)
		assert.Equal(tt, "original-bucket", merged.AutoTieringConfig.BucketName)
	})

	t.Run("ActiveDirectoryConfigIdNotUpdated_RemainsFromPoolModel", func(tt *testing.T) {
		basePoolModelWithAD := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:                      "test-pool",
			Description:               basePoolModel.Description,
			SizeInBytes:               basePoolModel.SizeInBytes,
			ActiveDirectoryConfigId:   "original-ad-id",
			ActiveDirectoryResourceId: "original-ad-resource-id",
		}

		params := &common.UpdatePoolParams{
			SizeInBytes:             4 * utils.TiBInBytes,
			ActiveDirectoryConfigId: "new-ad-id", // Provided but not used
		}

		merged := mergeUpdateParamsIntoPoolModel(basePoolModelWithAD, params)

		// ActiveDirectoryConfigId should remain from poolModel, not updated from params
		assert.Equal(tt, "original-ad-id", merged.ActiveDirectoryConfigId)
		assert.Equal(tt, "original-ad-resource-id", merged.ActiveDirectoryResourceId)
	})

	t.Run("ShallowCopyVerification_OriginalNotMutated", func(tt *testing.T) {
		originalDescription := basePoolModel.Description
		originalSize := basePoolModel.SizeInBytes
		originalLabels := make(map[string]string)
		for k, v := range basePoolModel.PoolAttributes.Labels {
			originalLabels[k] = v
		}

		params := &common.UpdatePoolParams{
			SizeInBytes: 4 * utils.TiBInBytes,
		}

		merged := mergeUpdateParamsIntoPoolModel(basePoolModel, params)

		// Modify merged pool
		merged.Description = "modified description"
		merged.SizeInBytes = 8 * utils.TiBInBytes
		if merged.PoolAttributes != nil && merged.PoolAttributes.Labels != nil {
			merged.PoolAttributes.Labels["modified"] = "value"
		}

		// Original should not be affected (shallow copy prevents mutation)
		assert.Equal(tt, originalDescription, basePoolModel.Description)
		assert.Equal(tt, originalSize, basePoolModel.SizeInBytes)
		assert.Equal(tt, originalLabels["env"], basePoolModel.PoolAttributes.Labels["env"])
		assert.NotContains(tt, basePoolModel.PoolAttributes.Labels, "modified")
	})

	t.Run("AutoTieringConfigCreated_WhenNotExists", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes:             4 * utils.TiBInBytes,
			AllowAutoTiering:        true,
			HotTierSizeInBytes:      1 * utils.TiBInBytes,
			EnableHotTierAutoResize: true,
		}

		merged := mergeUpdateParamsIntoPoolModel(basePoolModel, params)

		// AutoTieringConfig should be created since it doesn't exist in poolModel
		assert.NotNil(tt, merged.AutoTieringConfig)
		assert.Equal(tt, uint64(1*utils.TiBInBytes), merged.AutoTieringConfig.HotTierSizeInBytes)
		assert.Equal(tt, true, merged.AutoTieringConfig.EnableHotTierAutoResize)
	})

	t.Run("AutoTieringConfigPreservesExistingFields", func(tt *testing.T) {
		poolWithAutoTiering := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      2 * utils.TiBInBytes,
			AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      1 * utils.TiBInBytes,
				EnableHotTierAutoResize: false,
				BucketName:              "original-bucket",
				HotTierConsumption:      100,
				ColdTierConsumption:     200,
			},
		}

		params := &common.UpdatePoolParams{
			SizeInBytes:             4 * utils.TiBInBytes,
			AllowAutoTiering:        true,
			HotTierSizeInBytes:      2 * utils.TiBInBytes,
			EnableHotTierAutoResize: true,
		}

		merged := mergeUpdateParamsIntoPoolModel(poolWithAutoTiering, params)

		// Updated fields from params
		assert.NotNil(tt, merged.AutoTieringConfig)
		assert.Equal(tt, uint64(2*utils.TiBInBytes), merged.AutoTieringConfig.HotTierSizeInBytes)
		assert.Equal(tt, true, merged.AutoTieringConfig.EnableHotTierAutoResize)
		// Existing fields preserved from shallow copy
		assert.Equal(tt, "original-bucket", merged.AutoTieringConfig.BucketName)
		assert.Equal(tt, int64(100), merged.AutoTieringConfig.HotTierConsumption)
		assert.Equal(tt, int64(200), merged.AutoTieringConfig.ColdTierConsumption)
	})

	t.Run("SizeInBytesNotUpdated_WhenZero", func(tt *testing.T) {
		params := &common.UpdatePoolParams{
			SizeInBytes: 0, // Zero value means not provided
		}

		merged := mergeUpdateParamsIntoPoolModel(basePoolModel, params)

		// SizeInBytes should remain from poolModel when params.SizeInBytes is 0
		assert.Equal(tt, basePoolModel.SizeInBytes, merged.SizeInBytes)
		assert.Equal(tt, uint64(2*utils.TiBInBytes), merged.SizeInBytes)
	})
}

func TestCreateExpertModeUser(t *testing.T) {
	poolObj := &datamodel.Pool{
		DeploymentName: "test-deployment",
	}
	userName := "expertuser"

	// Save and restore env.AuthType and env.NodePassword for isolation
	origAuthType := env.AuthType
	origNodePassword := env.NodePassword
	defer func() {
		env.AuthType = origAuthType
		env.NodePassword = origNodePassword
	}()

	t.Run("USER_CERTIFICATE", func(tt *testing.T) {
		env.AuthType = env.USER_CERTIFICATE
		creds := createExpertModeUser(poolObj, userName)
		assert.NotNil(tt, creds)
		assert.Len(tt, creds.ExpertModeCredential, 1)
		c := creds.ExpertModeCredential[0]
		assert.Equal(tt, env.USER_CERTIFICATE, c.AuthType)
		assert.Equal(tt, userName, c.Username)
		assert.Contains(tt, c.CertificateID, "test-deployment-cert-expertuser")
		assert.Empty(tt, c.SecretID)
		assert.Empty(tt, c.Password)
	})

	t.Run("USERNAME_PWD_SEC_MGR", func(tt *testing.T) {
		env.AuthType = env.USERNAME_PWD_SEC_MGR
		creds := createExpertModeUser(poolObj, userName)
		assert.NotNil(tt, creds)
		assert.Len(tt, creds.ExpertModeCredential, 1)
		c := creds.ExpertModeCredential[0]
		assert.Equal(tt, env.USERNAME_PWD_SEC_MGR, c.AuthType)
		assert.Equal(tt, userName, c.Username)
		assert.Contains(tt, c.SecretID, "test-deployment-secret-expertuser")
		assert.Empty(tt, c.CertificateID)
		assert.Empty(tt, c.Password)
	})

	t.Run("Default USERNAME_PWD", func(tt *testing.T) {
		env.AuthType = env.USERNAME_PWD
		env.NodePassword = "nodepwd"
		creds := createExpertModeUser(poolObj, userName)
		assert.NotNil(tt, creds)
		assert.Len(tt, creds.ExpertModeCredential, 1)
		c := creds.ExpertModeCredential[0]
		assert.Equal(tt, env.USERNAME_PWD, c.AuthType)
		assert.Equal(tt, userName, c.Username)
		assert.Empty(tt, c.SecretID)
		assert.Empty(tt, c.CertificateID)
		assert.Equal(tt, "nodepwd", c.Password)
	})
}

func Test_mergeUpdateParamsIntoPoolModel(t *testing.T) {
	t.Run("WhenNoAutoTieringParamsProvided", func(tt *testing.T) {
		// Arrange
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776, // 1 TB
			AllowAutoTiering: false,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      107374182400, // 100 GB
				EnableHotTierAutoResize: false,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-west1-a",
				Labels: map[string]string{
					"env": "test",
				},
			},
		}
		params := &common.UpdatePoolParams{
			PoolId:             "test-pool-uuid",
			SizeInBytes:        2199023255552, // 2 TB
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert
		assert.NotNil(tt, result)
		assert.NotEqual(tt, poolModel, result, "Should return a new pool pointer (shallow copy)")
		assert.Equal(tt, uint64(2199023255552), result.SizeInBytes, "SizeInBytes should be updated")
		// AutoTieringConfig should remain unchanged since no auto tiering params were provided
		assert.NotNil(tt, result.AutoTieringConfig)
		assert.Equal(tt, uint64(107374182400), result.AutoTieringConfig.HotTierSizeInBytes)
		assert.False(tt, result.AutoTieringConfig.EnableHotTierAutoResize)
		assert.False(tt, result.AllowAutoTiering)
		// Original pool should be unchanged
		assert.Equal(tt, uint64(1099511627776), poolModel.SizeInBytes)
	})

	t.Run("WhenAllowAutoTieringIsTrue", func(tt *testing.T) {
		// Arrange - Testing line 400 specifically
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: false,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      107374182400,
				EnableHotTierAutoResize: false,
			},
		}
		params := &common.UpdatePoolParams{
			PoolId:                  "test-pool-uuid",
			AllowAutoTiering:        true,         // This triggers line 399 condition
			HotTierSizeInBytes:      214748364800, // 200 GB
			EnableHotTierAutoResize: true,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert - Line 400 should set AllowAutoTiering from params
		assert.NotNil(tt, result)
		assert.True(tt, result.AllowAutoTiering, "Line 400: AllowAutoTiering should be set from params")
		assert.NotNil(tt, result.AutoTieringConfig)
		assert.Equal(tt, uint64(214748364800), result.AutoTieringConfig.HotTierSizeInBytes)
		assert.True(tt, result.AutoTieringConfig.EnableHotTierAutoResize)
		// Original pool should be unchanged
		assert.False(tt, poolModel.AllowAutoTiering)
	})

	t.Run("WhenHotTierSizeInBytesProvidedWithoutAllowAutoTiering", func(tt *testing.T) {
		// Arrange
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      107374182400,
				EnableHotTierAutoResize: false,
			},
		}
		params := &common.UpdatePoolParams{
			PoolId:                  "test-pool-uuid",
			AllowAutoTiering:        false,
			HotTierSizeInBytes:      214748364800, // This triggers line 399 condition
			EnableHotTierAutoResize: true,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert - Line 400 should set AllowAutoTiering to false from params
		assert.NotNil(tt, result)
		assert.False(tt, result.AllowAutoTiering, "Line 400: AllowAutoTiering should be false from params")
		assert.NotNil(tt, result.AutoTieringConfig)
		assert.Equal(tt, uint64(214748364800), result.AutoTieringConfig.HotTierSizeInBytes)
		assert.True(tt, result.AutoTieringConfig.EnableHotTierAutoResize)
	})

	t.Run("WhenAutoTieringConfigIsNil", func(tt *testing.T) {
		// Arrange
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:              "test-pool",
			SizeInBytes:       1099511627776,
			AllowAutoTiering:  false,
			AutoTieringConfig: nil, // Test case where config is nil
		}
		params := &common.UpdatePoolParams{
			PoolId:                  "test-pool-uuid",
			AllowAutoTiering:        true,
			HotTierSizeInBytes:      214748364800,
			EnableHotTierAutoResize: true,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert
		assert.NotNil(tt, result)
		assert.True(tt, result.AllowAutoTiering, "Line 400: AllowAutoTiering should be set to true")
		assert.NotNil(tt, result.AutoTieringConfig, "AutoTieringConfig should be created")
		assert.Equal(tt, uint64(214748364800), result.AutoTieringConfig.HotTierSizeInBytes)
		assert.True(tt, result.AutoTieringConfig.EnableHotTierAutoResize)
		// Original pool should still have nil config
		assert.Nil(tt, poolModel.AutoTieringConfig)
	})

	t.Run("WhenAutoTieringConfigExists", func(tt *testing.T) {
		// Arrange
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      107374182400,
				EnableHotTierAutoResize: false,
				BucketName:              "existing-bucket",
				HotTierConsumption:      50000000000,
				ColdTierConsumption:     57374182400,
			},
		}
		params := &common.UpdatePoolParams{
			PoolId:                  "test-pool-uuid",
			AllowAutoTiering:        true,
			HotTierSizeInBytes:      214748364800,
			EnableHotTierAutoResize: true,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert
		assert.NotNil(tt, result)
		assert.True(tt, result.AllowAutoTiering)
		assert.NotNil(tt, result.AutoTieringConfig)
		assert.NotEqual(tt, poolModel.AutoTieringConfig, result.AutoTieringConfig, "Should create a copy of AutoTieringConfig")
		// Updated fields
		assert.Equal(tt, uint64(214748364800), result.AutoTieringConfig.HotTierSizeInBytes)
		assert.True(tt, result.AutoTieringConfig.EnableHotTierAutoResize)
		// Preserved fields from existing config
		assert.Equal(tt, "existing-bucket", result.AutoTieringConfig.BucketName)
		assert.Equal(tt, int64(50000000000), result.AutoTieringConfig.HotTierConsumption)
		assert.Equal(tt, int64(57374182400), result.AutoTieringConfig.ColdTierConsumption)
		// Original should be unchanged
		assert.Equal(tt, uint64(107374182400), poolModel.AutoTieringConfig.HotTierSizeInBytes)
		assert.False(tt, poolModel.AutoTieringConfig.EnableHotTierAutoResize)
	})

	t.Run("WhenOnlyEnableHotTierAutoResizeIsUpdated", func(tt *testing.T) {
		// Arrange
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      107374182400,
				EnableHotTierAutoResize: false,
			},
		}
		params := &common.UpdatePoolParams{
			PoolId:                  "test-pool-uuid",
			AllowAutoTiering:        true,
			HotTierSizeInBytes:      0, // Not provided
			EnableHotTierAutoResize: true,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert
		assert.NotNil(tt, result)
		assert.True(tt, result.AllowAutoTiering)
		assert.NotNil(tt, result.AutoTieringConfig)
		// HotTierSizeInBytes should remain unchanged since params.HotTierSizeInBytes is 0
		assert.Equal(tt, uint64(107374182400), result.AutoTieringConfig.HotTierSizeInBytes)
		assert.True(tt, result.AutoTieringConfig.EnableHotTierAutoResize)
	})

	t.Run("WhenPoolAttributesWithLabelsAreCopied", func(tt *testing.T) {
		// Arrange
		originalLabels := map[string]string{
			"env":  "production",
			"team": "platform",
		}
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: false,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone:   "us-west1-a",
				SecondaryZone: "us-west1-b",
				IsRegionalHA:  true,
				Labels:        originalLabels,
			},
		}
		originalPoolAttributesPtr := poolModel.PoolAttributes
		params := &common.UpdatePoolParams{
			PoolId:             "test-pool-uuid",
			AllowAutoTiering:   true,
			HotTierSizeInBytes: 214748364800,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert
		assert.NotNil(tt, result)
		// PoolAttributes pointer should be different (shallow copy created)
		assert.True(tt, result.PoolAttributes != originalPoolAttributesPtr, "PoolAttributes pointer should be different")
		assert.Equal(tt, "us-west1-a", result.PoolAttributes.PrimaryZone)
		assert.Equal(tt, "us-west1-b", result.PoolAttributes.SecondaryZone)
		assert.True(tt, result.PoolAttributes.IsRegionalHA)
		assert.Equal(tt, 2, len(result.PoolAttributes.Labels))
		assert.Equal(tt, "production", result.PoolAttributes.Labels["env"])
		assert.Equal(tt, "platform", result.PoolAttributes.Labels["team"])
		// Mutating result labels should not affect original
		result.PoolAttributes.Labels["env"] = "staging"
		assert.Equal(tt, "production", originalLabels["env"], "Original labels should be unchanged")
	})

	t.Run("WhenPoolAttributesIsNil", func(tt *testing.T) {
		// Arrange
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: false,
			PoolAttributes:   nil,
		}
		params := &common.UpdatePoolParams{
			PoolId:             "test-pool-uuid",
			AllowAutoTiering:   true,
			HotTierSizeInBytes: 214748364800,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert
		assert.NotNil(tt, result)
		assert.Nil(tt, result.PoolAttributes, "PoolAttributes should remain nil")
		assert.True(tt, result.AllowAutoTiering)
	})

	t.Run("WhenDisablingAutoTiering", func(tt *testing.T) {
		// Arrange
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      107374182400,
				EnableHotTierAutoResize: true,
			},
		}
		params := &common.UpdatePoolParams{
			PoolId:                  "test-pool-uuid",
			AllowAutoTiering:        false,
			HotTierSizeInBytes:      214748364800, // Still updating hot tier size
			EnableHotTierAutoResize: false,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert - Line 400 should set AllowAutoTiering to false
		assert.NotNil(tt, result)
		assert.False(tt, result.AllowAutoTiering, "Line 400: AllowAutoTiering should be disabled")
		assert.NotNil(tt, result.AutoTieringConfig)
		assert.Equal(tt, uint64(214748364800), result.AutoTieringConfig.HotTierSizeInBytes)
		assert.False(tt, result.AutoTieringConfig.EnableHotTierAutoResize)
	})

	t.Run("WhenSizeInBytesIsZero", func(tt *testing.T) {
		// Arrange
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: false,
		}
		params := &common.UpdatePoolParams{
			PoolId:             "test-pool-uuid",
			SizeInBytes:        0, // Not provided
			AllowAutoTiering:   true,
			HotTierSizeInBytes: 214748364800,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert
		assert.NotNil(tt, result)
		assert.Equal(tt, uint64(1099511627776), result.SizeInBytes, "SizeInBytes should remain unchanged")
		assert.True(tt, result.AllowAutoTiering)
	})

	t.Run("WhenBothSizeAndAutoTieringUpdated", func(tt *testing.T) {
		// Arrange
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: false,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      107374182400,
				EnableHotTierAutoResize: false,
			},
		}
		params := &common.UpdatePoolParams{
			PoolId:                  "test-pool-uuid",
			SizeInBytes:             3298534883328, // 3 TB
			AllowAutoTiering:        true,
			HotTierSizeInBytes:      322122547200, // 300 GB
			EnableHotTierAutoResize: true,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert
		assert.NotNil(tt, result)
		assert.Equal(tt, uint64(3298534883328), result.SizeInBytes, "SizeInBytes should be updated")
		assert.True(tt, result.AllowAutoTiering, "Line 400: AllowAutoTiering should be updated")
		assert.NotNil(tt, result.AutoTieringConfig)
		assert.Equal(tt, uint64(322122547200), result.AutoTieringConfig.HotTierSizeInBytes)
		assert.True(tt, result.AutoTieringConfig.EnableHotTierAutoResize)
		// Original should be unchanged
		assert.Equal(tt, uint64(1099511627776), poolModel.SizeInBytes)
		assert.False(tt, poolModel.AllowAutoTiering)
	})

	t.Run("WhenAllFieldsRemainUnchanged", func(tt *testing.T) {
		// Arrange
		poolModel := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			Description:      "Test pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: true,
			Region:           "us-west1",
			Zone:             "us-west1-a",
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      107374182400,
				EnableHotTierAutoResize: true,
				BucketName:              "test-bucket",
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-west1-a",
				Labels: map[string]string{
					"env": "test",
				},
			},
		}
		originalPoolPtr := poolModel
		params := &common.UpdatePoolParams{
			PoolId:             "test-pool-uuid",
			SizeInBytes:        0,
			AllowAutoTiering:   false,
			HotTierSizeInBytes: 0,
		}

		// Act
		result := mergeUpdateParamsIntoPoolModel(poolModel, params)

		// Assert
		assert.NotNil(tt, result)
		assert.True(tt, result != originalPoolPtr, "Should return a different pool pointer (shallow copy)")
		// All fields should remain unchanged except the shallow copy
		assert.Equal(tt, poolModel.Name, result.Name)
		assert.Equal(tt, poolModel.Description, result.Description)
		assert.Equal(tt, poolModel.SizeInBytes, result.SizeInBytes)
		assert.Equal(tt, poolModel.AllowAutoTiering, result.AllowAutoTiering)
		assert.Equal(tt, poolModel.Region, result.Region)
		assert.Equal(tt, poolModel.Zone, result.Zone)
		// AutoTieringConfig should remain unchanged (no auto tiering params provided)
		assert.Equal(tt, poolModel.AutoTieringConfig.HotTierSizeInBytes, result.AutoTieringConfig.HotTierSizeInBytes)
		assert.Equal(tt, poolModel.AutoTieringConfig.EnableHotTierAutoResize, result.AutoTieringConfig.EnableHotTierAutoResize)
	})
}

// TestGetPoolDeploymentName tests the getPoolDeploymentName helper function
func TestGetPoolDeploymentName(t *testing.T) {
	t.Run("WhenPoolIsNil", func(tt *testing.T) {
		result := getPoolDeploymentName(nil)
		assert.Equal(tt, "", result)
	})

	t.Run("WhenDeploymentNameIsEmpty", func(tt *testing.T) {
		pool := &datamodel.Pool{
			DeploymentName: "",
		}
		result := getPoolDeploymentName(pool)
		assert.Equal(tt, "", result)
	})

	t.Run("WhenDeploymentNameIsSet", func(tt *testing.T) {
		pool := &datamodel.Pool{
			DeploymentName: "test-deployment-name",
		}
		result := getPoolDeploymentName(pool)
		assert.Equal(tt, "test-deployment-name", result)
	})
}

// TestGetPoolIsRegionalHA tests the getPoolIsRegionalHA helper function
func TestGetPoolIsRegionalHA(t *testing.T) {
	t.Run("WhenPoolIsNil", func(tt *testing.T) {
		result := getPoolIsRegionalHA(nil)
		assert.False(tt, result)
	})

	t.Run("WhenPoolAttributesIsNil", func(tt *testing.T) {
		pool := &datamodel.Pool{
			PoolAttributes: nil,
		}
		result := getPoolIsRegionalHA(pool)
		assert.False(tt, result)
	})

	t.Run("WhenIsRegionalHAIsFalse", func(tt *testing.T) {
		pool := &datamodel.Pool{
			PoolAttributes: &datamodel.PoolAttributes{
				IsRegionalHA: false,
			},
		}
		result := getPoolIsRegionalHA(pool)
		assert.False(tt, result)
	})

	t.Run("WhenIsRegionalHAIsTrue", func(tt *testing.T) {
		pool := &datamodel.Pool{
			PoolAttributes: &datamodel.PoolAttributes{
				IsRegionalHA: true,
			},
		}
		result := getPoolIsRegionalHA(pool)
		assert.True(tt, result)
	})
}

func TestMatchesCredential(t *testing.T) {
	t.Run("WhenUserNameIsGcnvadmin_MatchesExactGcnvadmin", func(tt *testing.T) {
		result := matchesCredential("gcnvadmin", "gcnvadmin")
		assert.True(tt, result, "Should match exact 'gcnvadmin'")
	})

	t.Run("WhenUserNameIsGcnvadmin_DoesNotMatchOtherUsernames", func(tt *testing.T) {
		result := matchesCredential("gcnvadmin", "test-user_gadmin")
		assert.False(tt, result, "Should not match other usernames when userName is 'gcnvadmin'")
	})

	t.Run("WhenUserNameIsGcnvadmin_DoesNotMatchGcnvadminWithSuffix", func(tt *testing.T) {
		result := matchesCredential("gcnvadmin", "gcnvadmin_gadmin")
		assert.False(tt, result, "Should not match 'gcnvadmin' with suffix")
	})

	t.Run("WhenUserNameIsExpertModeUserSuffix_MatchesCredentialEndingWithSuffix", func(tt *testing.T) {
		// env.ExpertModeUserSuffix is "gadmin"
		result := matchesCredential("gadmin", "test-user_gadmin")
		assert.True(tt, result, "Should match credential ending with '_gadmin'")
	})

	t.Run("WhenUserNameIsExpertModeUserSuffix_MatchesCredentialWithOnlySuffix", func(tt *testing.T) {
		result := matchesCredential("gadmin", "_gadmin")
		assert.True(tt, result, "Should match credential that is just '_gadmin'")
	})

	t.Run("WhenUserNameIsExpertModeUserSuffix_DoesNotMatchCredentialWithoutSuffix", func(tt *testing.T) {
		result := matchesCredential("gadmin", "test-user")
		assert.False(tt, result, "Should not match credential without '_gadmin' suffix")
	})

	t.Run("WhenUserNameIsExpertModeUserSuffix_DoesNotMatchCredentialWithDifferentSuffix", func(tt *testing.T) {
		result := matchesCredential("gadmin", "test-user_admin")
		assert.False(tt, result, "Should not match credential with different suffix")
	})

	t.Run("WhenUserNameIsExpertModeUserSuffix_DoesNotMatchExactGcnvadmin", func(tt *testing.T) {
		result := matchesCredential("gadmin", "gcnvadmin")
		assert.False(tt, result, "Should not match 'gcnvadmin' when userName is suffix")
	})

	t.Run("WhenUserNameIsOtherValue_ReturnsFalse", func(tt *testing.T) {
		result := matchesCredential("other-user", "test-user_gadmin")
		assert.False(tt, result, "Should return false for non-matching userName")
	})

	t.Run("WhenUserNameIsOtherValue_DoesNotMatchGcnvadmin", func(tt *testing.T) {
		result := matchesCredential("other-user", "gcnvadmin")
		assert.False(tt, result, "Should not match 'gcnvadmin' for other userName")
	})

	t.Run("WhenUserNameIsEmpty_ReturnsFalse", func(tt *testing.T) {
		result := matchesCredential("", "test-user_gadmin")
		assert.False(tt, result, "Should return false for empty userName")
	})

	t.Run("WhenCredUsernameIsEmpty_ReturnsFalse", func(tt *testing.T) {
		result := matchesCredential("gadmin", "")
		assert.False(tt, result, "Should return false for empty credUsername")
	})

	t.Run("WhenUserNameIsExpertModeUserSuffix_MatchesMultipleCredentialFormats", func(tt *testing.T) {
		testCases := []struct {
			credUsername string
			expected     bool
			description  string
		}{
			{"user1_gadmin", true, "standard format"},
			{"user2_gadmin", true, "another standard format"},
			{"_gadmin", true, "just suffix with underscore"},
			{"gadmin", false, "suffix without underscore"},
			{"user_gadmin_extra", false, "suffix not at end"},
		}

		for _, tc := range testCases {
			tt.Run(tc.description, func(t *testing.T) {
				result := matchesCredential("gadmin", tc.credUsername)
				assert.Equal(t, tc.expected, result, "Failed for: %s", tc.description)
			})
		}
	})
}

func TestEnrichSinglePoolWithExpertModeCapacity(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	t.Run("WhenONTAPModePoolAndCapacityIsRetrievedSuccessfully_UpdatesPoolFields", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "test-pool-uuid",
				},
				APIAccessMode: common.ONTAPMode,
			},
			QuotaInBytes: 0,
			VolumeCount:  0,
		}

		expectedCapacity := &database.ExpertModePoolCapacity{
			TotalSize:   1099511627776, // 1TB
			VolumeCount: 5,
		}

		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(1)).Return(expectedCapacity, nil).Once()

		err := enrichSinglePoolWithExpertModeCapacity(ctx, mockStorage, pool)
		assert.NoError(tt, err)
		assert.Equal(tt, uint64(1099511627776), pool.QuotaInBytes)
		assert.Equal(tt, int64(5), pool.VolumeCount)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenONTAPModePoolAndGetExpertModePoolUsedCapacityAndVolumeCountReturnsError_ReturnsError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "test-pool-uuid",
				},
				APIAccessMode: common.ONTAPMode,
			},
			QuotaInBytes: 100,
			VolumeCount:  2,
		}

		expectedError := errors.New("database error")
		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(1)).Return((*database.ExpertModePoolCapacity)(nil), expectedError).Once()

		err := enrichSinglePoolWithExpertModeCapacity(ctx, mockStorage, pool)
		assert.Error(tt, err)
		assert.EqualError(tt, err, expectedError.Error())
		// Pool fields should remain unchanged on error
		assert.Equal(tt, uint64(100), pool.QuotaInBytes)
		assert.Equal(tt, int64(2), pool.VolumeCount)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenONTAPModePoolAndCapacityIsNil_ReturnsNilWithoutError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "test-pool-uuid",
				},
				APIAccessMode: common.ONTAPMode,
			},
			QuotaInBytes: 100,
			VolumeCount:  2,
		}

		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(1)).Return((*database.ExpertModePoolCapacity)(nil), nil).Once()

		err := enrichSinglePoolWithExpertModeCapacity(ctx, mockStorage, pool)
		assert.NoError(tt, err)
		// Pool fields should remain unchanged when capacity is nil
		assert.Equal(tt, uint64(100), pool.QuotaInBytes)
		assert.Equal(tt, int64(2), pool.VolumeCount)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenONTAPModePoolAndCapacityIsZero_UpdatesPoolFieldsToZero", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "test-pool-uuid",
				},
				APIAccessMode: common.ONTAPMode,
			},
			QuotaInBytes: 100,
			VolumeCount:  2,
		}

		expectedCapacity := &database.ExpertModePoolCapacity{
			TotalSize:   0,
			VolumeCount: 0,
		}

		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(1)).Return(expectedCapacity, nil).Once()

		err := enrichSinglePoolWithExpertModeCapacity(ctx, mockStorage, pool)
		assert.NoError(tt, err)
		assert.Equal(tt, uint64(0), pool.QuotaInBytes)
		assert.Equal(tt, int64(0), pool.VolumeCount)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenNonONTAPModePool_DoesNotCallGetExpertModePoolUsedCapacityAndVolumeCount", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "test-pool-uuid",
				},
				APIAccessMode: common.DEFAULTMode,
			},
			QuotaInBytes: 100,
			VolumeCount:  5,
		}

		err := enrichSinglePoolWithExpertModeCapacity(ctx, mockStorage, pool)
		assert.NoError(tt, err)
		// Pool fields should remain unchanged
		assert.Equal(tt, uint64(100), pool.QuotaInBytes)
		assert.Equal(tt, int64(5), pool.VolumeCount)

		mockStorage.AssertExpectations(tt)
	})
}

func TestEnrichPoolsWithExpertModeCapacity(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	t.Run("WhenAllPoolsAreONTAPMode_EnrichesAllPools", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		pools := []*datamodel.PoolView{
			{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						ID:   1,
						UUID: "ontap-pool-1",
					},
					APIAccessMode: common.ONTAPMode,
				},
				QuotaInBytes: 0,
				VolumeCount:  0,
			},
			{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						ID:   2,
						UUID: "ontap-pool-2",
					},
					APIAccessMode: common.ONTAPMode,
				},
				QuotaInBytes: 0,
				VolumeCount:  0,
			},
		}

		capacity1 := &database.ExpertModePoolCapacity{
			TotalSize:   214748364800, // 200GB
			VolumeCount: 3,
		}
		capacity2 := &database.ExpertModePoolCapacity{
			TotalSize:   536870912000, // 500GB
			VolumeCount: 7,
		}

		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(1)).Return(capacity1, nil).Once()
		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(2)).Return(capacity2, nil).Once()

		err := enrichPoolsWithExpertModeCapacity(ctx, mockStorage, pools)
		assert.NoError(tt, err)
		assert.Equal(tt, uint64(214748364800), pools[0].QuotaInBytes)
		assert.Equal(tt, int64(3), pools[0].VolumeCount)
		assert.Equal(tt, uint64(536870912000), pools[1].QuotaInBytes)
		assert.Equal(tt, int64(7), pools[1].VolumeCount)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenPoolsHaveMixedModes_OnlyEnrichesONTAPModePools", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		pools := []*datamodel.PoolView{
			{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						ID:   1,
						UUID: "ontap-pool",
					},
					APIAccessMode: common.ONTAPMode,
				},
				QuotaInBytes: 0,
				VolumeCount:  0,
			},
			{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						ID:   2,
						UUID: "default-pool",
					},
					APIAccessMode: common.DEFAULTMode,
				},
				QuotaInBytes: 100,
				VolumeCount:  5,
			},
			{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						ID:   3,
						UUID: "ontap-pool-2",
					},
					APIAccessMode: common.ONTAPMode,
				},
				QuotaInBytes: 0,
				VolumeCount:  0,
			},
		}

		capacity1 := &database.ExpertModePoolCapacity{
			TotalSize:   107374182400, // 100GB
			VolumeCount: 2,
		}
		capacity3 := &database.ExpertModePoolCapacity{
			TotalSize:   322122547200, // 300GB
			VolumeCount: 4,
		}

		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(1)).Return(capacity1, nil).Once()
		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(3)).Return(capacity3, nil).Once()

		err := enrichPoolsWithExpertModeCapacity(ctx, mockStorage, pools)
		assert.NoError(tt, err)
		// ONTAP pool 1 should be enriched
		assert.Equal(tt, uint64(107374182400), pools[0].QuotaInBytes)
		assert.Equal(tt, int64(2), pools[0].VolumeCount)
		// DEFAULT mode pool should remain unchanged
		assert.Equal(tt, uint64(100), pools[1].QuotaInBytes)
		assert.Equal(tt, int64(5), pools[1].VolumeCount)
		// ONTAP pool 2 should be enriched
		assert.Equal(tt, uint64(322122547200), pools[2].QuotaInBytes)
		assert.Equal(tt, int64(4), pools[2].VolumeCount)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenNoPoolsAreONTAPMode_DoesNotCallGetExpertModePoolUsedCapacityAndVolumeCount", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		pools := []*datamodel.PoolView{
			{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						ID:   1,
						UUID: "default-pool-1",
					},
					APIAccessMode: common.DEFAULTMode,
				},
				QuotaInBytes: 100,
				VolumeCount:  5,
			},
			{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						ID:   2,
						UUID: "default-pool-2",
					},
					APIAccessMode: common.DEFAULTMode,
				},
				QuotaInBytes: 200,
				VolumeCount:  10,
			},
		}

		err := enrichPoolsWithExpertModeCapacity(ctx, mockStorage, pools)
		assert.NoError(tt, err)
		// Pools should remain unchanged
		assert.Equal(tt, uint64(100), pools[0].QuotaInBytes)
		assert.Equal(tt, int64(5), pools[0].VolumeCount)
		assert.Equal(tt, uint64(200), pools[1].QuotaInBytes)
		assert.Equal(tt, int64(10), pools[1].VolumeCount)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGetExpertModePoolUsedCapacityAndVolumeCountReturnsError_ReturnsError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		pools := []*datamodel.PoolView{
			{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						ID:   1,
						UUID: "ontap-pool-1",
					},
					APIAccessMode: common.ONTAPMode,
				},
				QuotaInBytes: 0,
				VolumeCount:  0,
			},
			{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						ID:   2,
						UUID: "ontap-pool-2",
					},
					APIAccessMode: common.ONTAPMode,
				},
				QuotaInBytes: 0,
				VolumeCount:  0,
			},
		}

		expectedError := errors.New("database error")
		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(1)).Return((*database.ExpertModePoolCapacity)(nil), expectedError).Once()

		err := enrichPoolsWithExpertModeCapacity(ctx, mockStorage, pools)
		assert.Error(tt, err)
		assert.EqualError(tt, err, expectedError.Error())
		// First pool should remain unchanged due to error
		assert.Equal(tt, uint64(0), pools[0].QuotaInBytes)
		assert.Equal(tt, int64(0), pools[0].VolumeCount)
		// Second pool should not be processed (error returned early)
		assert.Equal(tt, uint64(0), pools[1].QuotaInBytes)
		assert.Equal(tt, int64(0), pools[1].VolumeCount)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenEmptyPoolsSlice_ReturnsNilWithoutError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		pools := []*datamodel.PoolView{}

		err := enrichPoolsWithExpertModeCapacity(ctx, mockStorage, pools)
		assert.NoError(tt, err)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCapacityIsNil_ContinuesToNextPool", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		pools := []*datamodel.PoolView{
			{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						ID:   1,
						UUID: "ontap-pool-1",
					},
					APIAccessMode: common.ONTAPMode,
				},
				QuotaInBytes: 100,
				VolumeCount:  5,
			},
			{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						ID:   2,
						UUID: "ontap-pool-2",
					},
					APIAccessMode: common.ONTAPMode,
				},
				QuotaInBytes: 0,
				VolumeCount:  0,
			},
		}

		capacity2 := &database.ExpertModePoolCapacity{
			TotalSize:   107374182400, // 100GB
			VolumeCount: 3,
		}

		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(1)).Return((*database.ExpertModePoolCapacity)(nil), nil).Once()
		mockStorage.On("GetExpertModePoolUsedCapacityAndVolumeCount", ctx, int64(2)).Return(capacity2, nil).Once()

		err := enrichPoolsWithExpertModeCapacity(ctx, mockStorage, pools)
		assert.NoError(tt, err)
		// First pool should remain unchanged (capacity was nil)
		assert.Equal(tt, uint64(100), pools[0].QuotaInBytes)
		assert.Equal(tt, int64(5), pools[0].VolumeCount)
		// Second pool should be enriched
		assert.Equal(tt, uint64(107374182400), pools[1].QuotaInBytes)
		assert.Equal(tt, int64(3), pools[1].VolumeCount)

		mockStorage.AssertExpectations(tt)
	})
}

func TestDeletePool_PreviousStateAndDetailsInJobAttributes(t *testing.T) {
	t.Run("DeletePool_WhenAllConditionsMet_JobAttributesContainsPreviousStateAndDetails", func(tt *testing.T) {
		ctx, store, _, temporal := setup(tt)
		pools, account := createDBPools(t, store)
		pool := pools[0]

		previousState := datamodel.LifeCycleStateAvailable
		previousStateDetails := datamodel.LifeCycleStateAvailableDetails
		pool.State = previousState
		pool.StateDetails = previousStateDetails
		err := store.DB().Save(pool).Error
		assert.NoError(tt, err)

		params := &common.DeletePoolParams{
			AccountName: account.Name,
			PoolID:      pool.UUID,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetPool(ctx, params.PoolID, account.ID).Return(&datamodel.PoolView{
			Pool: *pool,
		}, nil)
		mockStorage.EXPECT().GetJobByResourceUUID(ctx, pool.UUID, mock.Anything).Return(nil, nil)
		mockStorage.EXPECT().CreateJob(ctx, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.JobAttributes != nil &&
				job.JobAttributes.PreviousState == previousState &&
				job.JobAttributes.PreviousStateDetails == previousStateDetails &&
				job.JobAttributes.ResourceUUID == pool.UUID
		})).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}, nil)
		mockStorage.EXPECT().DeletingPool(ctx, mock.Anything).Return(nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, jobID, err := _deletePool(ctx, temporal, mockStorage, params)
		assert.NoError(tt, err)
		assert.Equal(tt, pool.Name, result.Name)
		assert.NotEmpty(tt, jobID)
		mockStorage.AssertExpectations(tt)
	})
}

func TestGCPOrchestrator_CreateSvm_ReturnsNotImplemented(t *testing.T) {
	orch := &GCPOrchestrator{}
	_, err := orch.CreateSvm(context.Background(), &common.CreateSvmParams{})
	assert.Error(t, err)
	assert.True(t, errors.IsNotImplementedYetErr(err))
}

func TestGCPOrchestrator_DeleteSvm_ReturnsNotImplemented(t *testing.T) {
	orch := &GCPOrchestrator{}
	_, err := orch.DeleteSvm(context.Background(), &common.DeleteSvmParams{})
	assert.Error(t, err)
	assert.True(t, errors.IsNotImplementedYetErr(err))
}

func TestGCPOrchestrator_UpdateSvm_ReturnsNotImplemented(t *testing.T) {
	orch := &GCPOrchestrator{}
	_, err := orch.UpdateSvm(context.Background(), &common.UpdateSvmParams{})
	assert.Error(t, err)
	assert.True(t, errors.IsNotImplementedYetErr(err))
}

func TestGCPOrchestrator_PoolMaintenance_ReturnsNotImplemented(t *testing.T) {
	orch := &GCPOrchestrator{}

	_, startErr := orch.StartPoolMaintenance(context.Background(), &common.PoolMaintenanceParams{})
	assert.Error(t, startErr)
	assert.True(t, errors.IsNotImplementedYetErr(startErr))

	_, execErr := orch.ExecutePoolMaintenance(context.Background(), &common.PoolMaintenanceParams{})
	assert.Error(t, execErr)
	assert.True(t, errors.IsNotImplementedYetErr(execErr))

	_, finishErr := orch.FinishPoolMaintenance(context.Background(), &common.PoolMaintenanceParams{})
	assert.Error(t, finishErr)
	assert.True(t, errors.IsNotImplementedYetErr(finishErr))
}

func TestValidateCreatePoolParams_PORequest_NilCustomPerformance(t *testing.T) {
	logger := log.NewLogger()
	params := &common.CreatePoolParams{
		AccountName:  "test-account",
		Mode:         common.ONTAPMode,
		ClusterName:  "vsim",
		ServiceLevel: common.ServiceLevelExtreme,
		SizeInBytes:  uint64(utils.TiBInBytes),
	}
	err := _validateCreatePoolParams(params, logger)
	require.NoError(t, err)
	require.NotNil(t, params.CustomPerformanceParams)
	assert.Equal(t, int64(128), params.CustomPerformanceParams.ThroughputMibps)
	require.NotNil(t, params.CustomPerformanceParams.Iops)
	assert.Equal(t, int64(0), *params.CustomPerformanceParams.Iops)
	assert.False(t, params.CustomPerformanceParams.Enabled)
}

func TestCreatePool_PORequest_ValidatesHost(t *testing.T) {
	ctx, se, _, temporal := setup(t)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, _ database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	t.Cleanup(func() { getOrCreateAccount = origGetOrCreateAccount })

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetHostByName", mock.Anything, "missing-cluster").Return(nil,
		vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
			errors.NewNotFoundErr("Host", nil)))

	params := &common.CreatePoolParams{
		AccountName:  "test_account",
		Region:       "us-central1",
		PrimaryZone:  "us-central1-a",
		Name:         "po-pool",
		VendorID:     "vendor/po-pool",
		SizeInBytes:  uint64(utils.TiBInBytes),
		VendorSubNetID: "projects/p/global/networks/net",
		ClusterName:  "missing-cluster",
		ServiceLevel: common.ServiceLevelExtreme,
		Mode:         common.ONTAPMode,
	}

	_, _, err := createPool(ctx, mockSE, temporal, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `clusterName "missing-cluster" does not match a registered host`)
	mockSE.AssertExpectations(t)
	_ = se
}
