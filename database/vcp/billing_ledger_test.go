package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"gorm.io/gorm"
)

// newBillingLedgerTestStore returns a repository backed by a clean in-memory DB.
func newBillingLedgerTestStore(t *testing.T) *DataStoreRepository {
	t.Helper()

	db, err := SetupTestDB()
	require.NoError(t, err)

	store := NewDataStoreRepository(gormwrapper.New(db))
	require.NoError(t, ClearInMemoryDB(store.db.GORM()))

	return store
}

// closeTestStoreDB closes the underlying connection so subsequent queries fail.
func closeTestStoreDB(t *testing.T, store *DataStoreRepository) {
	t.Helper()

	sqlDB, err := store.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}

func TestCreateBillingLedger(t *testing.T) {
	ctx := context.Background()

	t.Run("CreatesEntryAndGeneratesUUID", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		created, err := store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			ResourceName:   "pool-1",
			ResourceUUID:   "pool-uuid-1",
			ResourceType:   metadata.VolumePool,
			MeasuredType:   metadata.PoolAllocatedSize,
			Quantity:       100,
			ConsumerID:     "consumer-1",
			DeploymentName: "deployment-1",
		})

		require.NoError(tt, err)
		require.NotNil(tt, created)
		assert.NotEmpty(tt, created.UUID, "UUID should be generated when not supplied")
		assert.False(tt, created.CreatedAt.IsZero())
		assert.Equal(tt, created.CreatedAt, created.UpdatedAt)
		assert.Equal(tt, float64(100), created.Quantity)
	})

	t.Run("KeepsCallerSuppliedUUID", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		created, err := store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			BaseModel:    datamodel.BaseModel{UUID: "explicit-uuid"},
			ResourceUUID: "pool-uuid-1",
			ResourceType: metadata.VolumePool,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     100,
		})

		require.NoError(tt, err)
		assert.Equal(tt, "explicit-uuid", created.UUID)
	})

	t.Run("ReturnsExistingWhenQuantityUnchanged", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		first, err := store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			ResourceUUID: "pool-uuid-1",
			ResourceType: metadata.VolumePool,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     100,
		})
		require.NoError(tt, err)

		second, err := store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			ResourceUUID: "pool-uuid-1",
			ResourceType: metadata.VolumePool,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     100,
		})
		require.NoError(tt, err)

		// The original row is returned untouched rather than being replaced.
		assert.Equal(tt, first.UUID, second.UUID)

		ledgers, err := store.GetBillingLedgersByResourceUUID(ctx, "pool-uuid-1")
		require.NoError(tt, err)
		assert.Len(tt, ledgers, 1)
	})

	t.Run("ReplacesEntryWhenQuantityChanged", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		first, err := store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			ResourceUUID: "pool-uuid-1",
			ResourceType: metadata.VolumePool,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     100,
		})
		require.NoError(tt, err)

		second, err := store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			ResourceUUID: "pool-uuid-1",
			ResourceType: metadata.VolumePool,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     250,
		})
		require.NoError(tt, err)
		assert.NotEqual(tt, first.UUID, second.UUID)

		active, err := store.getActiveBillingLedger(ctx, "pool-uuid-1", metadata.VolumePool, metadata.PoolAllocatedSize)
		require.NoError(tt, err)
		require.NotNil(tt, active)
		assert.Equal(tt, float64(250), active.Quantity, "only the new quantity should remain active")
	})

	t.Run("SkipsLookupWhenResourceUUIDEmpty", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		created, err := store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			ResourceName: "orphan",
			ResourceType: metadata.VolumePool,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     10,
		})

		require.NoError(tt, err)
		assert.NotEmpty(tt, created.UUID)
	})

	t.Run("ReturnsErrorWhenDBFails", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)
		closeTestStoreDB(tt, store)

		_, err := store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			ResourceUUID: "pool-uuid-1",
			ResourceType: metadata.VolumePool,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     100,
		})

		assert.Error(tt, err)
	})
}

func TestGetBillingLedgersByResourceUUID(t *testing.T) {
	ctx := context.Background()

	t.Run("ReturnsEntriesForResource", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		_, err := store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			ResourceUUID: "pool-uuid-1",
			ResourceType: metadata.VolumePool,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     100,
		})
		require.NoError(tt, err)

		_, err = store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			ResourceUUID: "pool-uuid-1",
			ResourceType: metadata.VolumePool,
			MeasuredType: metadata.PoolHotTierProvisionedSize,
			Quantity:     50,
		})
		require.NoError(tt, err)

		ledgers, err := store.GetBillingLedgersByResourceUUID(ctx, "pool-uuid-1")
		require.NoError(tt, err)
		assert.Len(tt, ledgers, 2)
	})

	t.Run("ReturnsEmptyForUnknownResource", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		ledgers, err := store.GetBillingLedgersByResourceUUID(ctx, "does-not-exist")
		require.NoError(tt, err)
		assert.Empty(tt, ledgers)
	})

	t.Run("ReturnsErrorWhenDBFails", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)
		closeTestStoreDB(tt, store)

		_, err := store.GetBillingLedgersByResourceUUID(ctx, "pool-uuid-1")
		assert.Error(tt, err)
	})
}

func TestGetActiveBillingLedger(t *testing.T) {
	ctx := context.Background()

	t.Run("ReturnsNilWhenNoEntryExists", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		ledger, err := store.getActiveBillingLedger(ctx, "missing-uuid", metadata.VolumePool, metadata.PoolAllocatedSize)
		require.NoError(tt, err)
		assert.Nil(tt, ledger)
	})

	t.Run("IgnoresOtherMeasuredTypes", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		_, err := store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			ResourceUUID: "pool-uuid-1",
			ResourceType: metadata.VolumePool,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     100,
		})
		require.NoError(tt, err)

		ledger, err := store.getActiveBillingLedger(ctx, "pool-uuid-1", metadata.VolumePool, metadata.PoolHotTierProvisionedSize)
		require.NoError(tt, err)
		assert.Nil(tt, ledger)
	})

	t.Run("ReturnsErrorWhenDBFails", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)
		closeTestStoreDB(tt, store)

		_, err := store.getActiveBillingLedger(ctx, "pool-uuid-1", metadata.VolumePool, metadata.PoolAllocatedSize)
		assert.Error(tt, err)
	})
}

func TestDeleteBillingLedgersByResourceUUIDAndMeasuredType(t *testing.T) {
	ctx := context.Background()

	t.Run("DeletesMatchingEntries", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		_, err := store.CreateBillingLedger(ctx, &datamodel.BillingLedger{
			ResourceUUID: "pool-uuid-1",
			ResourceType: metadata.VolumePool,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     100,
		})
		require.NoError(tt, err)

		err = store.DeleteBillingLedgersByResourceUUIDAndMeasuredType(ctx, "pool-uuid-1", metadata.VolumePool, metadata.PoolAllocatedSize)
		require.NoError(tt, err)

		active, err := store.getActiveBillingLedger(ctx, "pool-uuid-1", metadata.VolumePool, metadata.PoolAllocatedSize)
		require.NoError(tt, err)
		assert.Nil(tt, active)
	})

	t.Run("SucceedsWhenNothingMatches", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		err := store.DeleteBillingLedgersByResourceUUIDAndMeasuredType(ctx, "pool-uuid-1", metadata.VolumePool, metadata.PoolAllocatedSize)
		assert.NoError(tt, err)
	})

	t.Run("ReturnsValidationErrorWhenResourceUUIDEmpty", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)

		err := store.DeleteBillingLedgersByResourceUUIDAndMeasuredType(ctx, "", metadata.VolumePool, metadata.PoolAllocatedSize)
		require.Error(tt, err)
		cerr, ok := err.(*vsaerrors.CustomError)
		if assert.True(tt, ok, "expected *CustomError, got %T", err) {
			assert.Equal(tt, vsaerrors.ErrInputValidationError, cerr.TrackingID)
		}
	})

	t.Run("ReturnsErrorWhenDBFails", func(tt *testing.T) {
		store := newBillingLedgerTestStore(tt)
		closeTestStoreDB(tt, store)

		err := store.DeleteBillingLedgersByResourceUUIDAndMeasuredType(ctx, "pool-uuid-1", metadata.VolumePool, metadata.PoolAllocatedSize)
		assert.Error(tt, err)
	})
}

// TestListBillingLedgersWithPagination_IncludesDeletedRows verifies that a history-based
// formatter can replay a pool's full active/replaced billing window from the ledger: an active
// row (still accruing) and a previously replaced/deleted row within the query window must both be
// returned, mirroring ListBackupChainHistoriesWithPagination's soft-delete inclusion semantics.
func TestListBillingLedgersWithPagination_IncludesDeletedRows(t *testing.T) {
	store := newBillingLedgerTestStore(t)
	ctx := context.Background()

	start := time.Date(2026, 2, 18, 10, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Hour)
	deletedAt := start.Add(10 * time.Minute)

	activeLedger := &datamodel.BillingLedger{
		BaseModel: datamodel.BaseModel{
			UUID:      "ledger-active",
			CreatedAt: start.Add(-10 * time.Minute),
		},
		ResourceUUID:   "pool-uuid-1",
		ConsumerID:     "consumer-id",
		DeploymentName: "deployment-name",
		ResourceType:   metadata.VolumePool,
		MeasuredType:   metadata.PoolAllocatedSize,
		Quantity:       100,
	}
	deletedLedger := &datamodel.BillingLedger{
		BaseModel: datamodel.BaseModel{
			UUID:      "ledger-deleted",
			CreatedAt: start.Add(-20 * time.Minute),
			DeletedAt: &gorm.DeletedAt{Time: deletedAt, Valid: true},
		},
		ResourceUUID:   "pool-uuid-1",
		ConsumerID:     "consumer-id",
		DeploymentName: "deployment-name",
		ResourceType:   metadata.VolumePool,
		MeasuredType:   metadata.PoolAllocatedSize,
		Quantity:       50,
	}

	require.NoError(t, store.db.Create(activeLedger).Error())
	require.NoError(t, store.db.Create(deletedLedger).Error())

	conditions := [][]interface{}{
		{"resource_uuid IS NOT NULL"},
		{"resource_type = ?", metadata.VolumePool},
		{"measured_type = ?", metadata.PoolAllocatedSize},
		{"created_at <= ?", end},
		{"(deleted_at IS NULL OR deleted_at >= ?)", start},
	}
	pagination := &dbutils.Pagination{Offset: 0, Limit: 10}

	results, err := store.ListBillingLedgersWithPagination(ctx, conditions, pagination)
	require.NoError(t, err)
	require.Len(t, results, 2)

	uuids := map[string]bool{results[0].UUID: true, results[1].UUID: true}
	assert.True(t, uuids["ledger-active"])
	assert.True(t, uuids["ledger-deleted"])
}

func TestListBillingLedgersWithPagination_ReturnsErrorWhenDBFails(t *testing.T) {
	store := newBillingLedgerTestStore(t)
	closeTestStoreDB(t, store)

	_, err := store.ListBillingLedgersWithPagination(context.Background(), nil, &dbutils.Pagination{Offset: 0, Limit: 10})
	assert.Error(t, err)
}
