package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"gorm.io/gorm"
)

func setupIPRangeStore(t *testing.T) *DataStoreRepository {
	t.Helper()

	db, err := SetupTestDB()
	require.NoError(t, err)

	store := NewDataStoreRepository(gormwrapper.New(db))
	require.NoError(t, ClearInMemoryDB(store.db.GORM()))
	return store
}

func seedIPRangeReferences(t *testing.T, store *DataStoreRepository) (*datamodel.Region, *datamodel.Account, *datamodel.Host) {
	t.Helper()

	region := &datamodel.Region{
		BaseModel: datamodel.BaseModel{UUID: "region-uuid-1"},
		Name:      "region-1",
	}
	require.NoError(t, store.db.GORM().Create(region).Error)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: "account-uuid-1"},
		Name:      "account-1",
	}
	require.NoError(t, store.db.GORM().Create(account).Error)

	host := &datamodel.Host{
		BaseModel: datamodel.BaseModel{UUID: "host-uuid-1"},
		Name:      "host-1",
	}
	require.NoError(t, store.db.GORM().Create(host).Error)

	return region, account, host
}

func TestCreateIPRange(t *testing.T) {
	t.Run("FailsWhenNetworkMissing", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)

		_, err := store.CreateIPRange(context.Background(), &datamodel.IPRange{
			StartIP:     "10.0.0.10",
			EndIP:       "10.0.0.20",
			Gateway:     "10.0.0.1",
			Netmask:     "255.255.255.0",
			RegionUUID:  region.UUID,
			AccountUUID: account.UUID,
		})

		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
	})

	t.Run("FailsWhenRegionNotFound", func(t *testing.T) {
		store := setupIPRangeStore(t)
		_, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-1"

		_, err := store.CreateIPRange(context.Background(), &datamodel.IPRange{
			StartIP:     "10.0.0.10",
			EndIP:       "10.0.0.20",
			Gateway:     "10.0.0.1",
			Netmask:     "255.255.255.0",
			RegionUUID:  "missing-region",
			AccountUUID: account.UUID,
			Network:     &network,
		})

		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
	})
}

func TestGetIPRangeByUUID(t *testing.T) {
	t.Run("ReturnsRecordWhenExists", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, host := seedIPRangeReferences(t, store)
		network := "vpc-1"
		record := &datamodel.IPRange{
			BaseModel:  datamodel.BaseModel{UUID: "ip-range-uuid-1"},
			StartIP:    "10.0.0.10",
			EndIP:      "10.0.0.20",
			Gateway:    "10.0.0.1",
			Netmask:    "255.255.255.0",
			Network:    &network,
			RegionID:   sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID:  sql.NullInt64{Int64: account.ID, Valid: true},
			HostID:     sql.NullInt64{Int64: host.ID, Valid: true},
			LifType:    LifTypeDataLIF,
			RegionUUID: region.UUID,
		}
		require.NoError(t, store.db.GORM().Create(record).Error)

		got, err := store.GetIPRangeByUUID(context.Background(), record.UUID)
		require.NoError(t, err)
		assert.Equal(t, record.UUID, got.UUID)
		assert.Equal(t, record.StartIP, got.StartIP)
	})

	t.Run("ReturnsNotFoundWhenMissing", func(t *testing.T) {
		store := setupIPRangeStore(t)

		_, err := store.GetIPRangeByUUID(context.Background(), "missing-ip-range")
		require.Error(t, err)

		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
	})
}

func TestGetIPRanges(t *testing.T) {
	t.Run("FiltersByAccountAndNetworkAndPopulatesAccountAndRegionReferences", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, host := seedIPRangeReferences(t, store)
		network := "vpc-1"
		otherNetwork := "vpc-2"

		ipr1 := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-1"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
			HostID:    sql.NullInt64{Int64: host.ID, Valid: true},
		}
		ipr2 := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-2"},
			StartIP:   "10.0.1.10",
			EndIP:     "10.0.1.20",
			Gateway:   "10.0.1.1",
			Netmask:   "255.255.255.0",
			Network:   &otherNetwork,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(ipr1).Error)
		require.NoError(t, store.db.GORM().Create(ipr2).Error)

		got, err := store.GetIPRanges(context.Background(), &account.UUID, &network)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "ip-range-1", got[0].UUID)
		assert.Equal(t, region.UUID, got[0].RegionUUID)
		assert.Equal(t, region.Name, got[0].RegionName)
		assert.Equal(t, account.UUID, got[0].AccountUUID)
		assert.Equal(t, account.Name, got[0].AccountName)
		assert.Empty(t, got[0].HostUUID)
	})
}

func TestUpdateIPRangeNetwork(t *testing.T) {
	t.Run("UpdatesNetworkSuccessfully", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-old"
		record := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-update-1"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(record).Error)

		updatedNetwork := "vpc-new"
		got, err := store.UpdateIPRangeNetwork(context.Background(), &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: record.UUID},
			Network:   &updatedNetwork,
		})
		require.NoError(t, err)
		require.NotNil(t, got.Network)
		assert.Equal(t, updatedNetwork, *got.Network)
		assert.Equal(t, region.UUID, got.RegionUUID)
		assert.Equal(t, region.Name, got.RegionName)
		assert.Equal(t, account.UUID, got.AccountUUID)
		assert.Equal(t, account.Name, got.AccountName)
	})

	t.Run("ReturnsNotFoundWhenMissing", func(t *testing.T) {
		store := setupIPRangeStore(t)
		updatedNetwork := "vpc-new"

		_, err := store.UpdateIPRangeNetwork(context.Background(), &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "missing-ip-range"},
			Network:   &updatedNetwork,
		})
		require.Error(t, err)

		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
	})
}

func TestDeleteIPRange(t *testing.T) {
	t.Run("SoftDeletesExistingIPRange", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-1"
		record := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-del-1"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(record).Error)

		deleted, err := store.DeleteIPRange(context.Background(), record.UUID)
		require.NoError(t, err)
		require.NotNil(t, deleted.DeletedAt)
		assert.Equal(t, record.UUID, deleted.UUID)
		assert.Equal(t, region.UUID, deleted.RegionUUID)
		assert.Equal(t, region.Name, deleted.RegionName)
		assert.Equal(t, account.UUID, deleted.AccountUUID)
		assert.Equal(t, account.Name, deleted.AccountName)

		_, err = store.GetIPRangeByUUID(context.Background(), record.UUID)
		require.Error(t, err)
	})

	t.Run("ReturnsNotFoundWhenMissing", func(t *testing.T) {
		store := setupIPRangeStore(t)

		_, err := store.DeleteIPRange(context.Background(), "missing-ip-range")
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
	})
}

func TestGetRegionByID(t *testing.T) {
	store := setupIPRangeStore(t)
	region, _, _ := seedIPRangeReferences(t, store)

	t.Run("ReturnsRegionForMatchingID", func(t *testing.T) {
		got, err := store.getRegionByID(context.Background(), region.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, region.UUID, got.UUID)
	})

	t.Run("ReturnsReadErrorWhenLookupFails", func(t *testing.T) {
		require.NoError(t, store.db.GORM().Exec("DROP TABLE regions").Error)

		_, err := store.getRegionByID(context.Background(), region.ID)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
	})
}

func TestGetAccountByID(t *testing.T) {
	store := setupIPRangeStore(t)
	_, account, _ := seedIPRangeReferences(t, store)

	t.Run("ReturnsAccountForMatchingID", func(t *testing.T) {
		got, err := store.getAccountByID(context.Background(), account.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, account.UUID, got.UUID)
	})

	t.Run("ReturnsReadErrorWhenLookupFails", func(t *testing.T) {
		require.NoError(t, store.db.GORM().Exec("DROP TABLE accounts").Error)

		_, err := store.getAccountByID(context.Background(), account.ID)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
	})
}

func TestGetRegionByUUID(t *testing.T) {
	t.Run("ReturnsRegionWhenFound", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, _, _ := seedIPRangeReferences(t, store)

		got, err := store.getRegionByUUID(context.Background(), region.UUID)
		require.NoError(t, err)
		assert.Equal(t, region.UUID, got.UUID)
	})

	t.Run("ReturnsNotFoundWhenMissing", func(t *testing.T) {
		store := setupIPRangeStore(t)

		_, err := store.getRegionByUUID(context.Background(), "missing-region")
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.NotNil(t, customErr)
	})
}

func TestCreateIPRangeAdditionalBranches(t *testing.T) {
	t.Run("FailsWhenAccountNotFound", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, _, _ := seedIPRangeReferences(t, store)
		network := "vpc-1"

		_, err := store.CreateIPRange(context.Background(), &datamodel.IPRange{
			StartIP:     "10.0.0.10",
			EndIP:       "10.0.0.20",
			Gateway:     "10.0.0.1",
			Netmask:     "255.255.255.0",
			RegionUUID:  region.UUID,
			AccountUUID: "missing-account",
			Network:     &network,
		})

		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.NotNil(t, customErr)
	})

	t.Run("ReturnsErrorWhenTransactionStartFails", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-1"

		origStartTransaction := startTransaction
		startTransaction = func(_ *gorm.DB) (*gorm.DB, error) {
			return nil, errors.New("start tx failed")
		}
		t.Cleanup(func() { startTransaction = origStartTransaction })

		_, err := store.CreateIPRange(context.Background(), &datamodel.IPRange{
			StartIP:     "10.0.0.10",
			EndIP:       "10.0.0.20",
			Gateway:     "10.0.0.1",
			Netmask:     "255.255.255.0",
			RegionUUID:  region.UUID,
			AccountUUID: account.UUID,
			Network:     &network,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "start tx failed")
	})

	t.Run("ReturnsReadErrorWhenOverlapQueryFails", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-1"

		_, err := store.CreateIPRange(context.Background(), &datamodel.IPRange{
			StartIP:     "10.0.0.10",
			EndIP:       "10.0.0.20",
			Gateway:     "10.0.0.1",
			Netmask:     "255.255.255.0",
			RegionUUID:  region.UUID,
			AccountUUID: account.UUID,
			Network:     &network,
		})

		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
	})

	t.Run("PopulatesReferenceFieldsOnDryRunPath", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-1"

		origStartTransaction := startTransaction
		startTransaction = func(db *gorm.DB) (*gorm.DB, error) {
			return db.Session(&gorm.Session{DryRun: true}).Begin(), nil
		}
		t.Cleanup(func() { startTransaction = origStartTransaction })

		created, err := store.CreateIPRange(context.Background(), &datamodel.IPRange{
			StartIP:     "10.0.0.10",
			EndIP:       "10.0.0.20",
			Gateway:     "10.0.0.1",
			Netmask:     "255.255.255.0",
			RegionUUID:  region.UUID,
			AccountUUID: account.UUID,
			Network:     &network,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, created.UUID)
		assert.Equal(t, LifTypeDataLIF, created.LifType)
		assert.Equal(t, region.UUID, created.RegionUUID)
		assert.Equal(t, region.Name, created.RegionName)
		assert.Equal(t, account.UUID, created.AccountUUID)
		assert.Equal(t, account.Name, created.AccountName)
	})
}

func TestIPRangeReadErrorBranches(t *testing.T) {
	t.Run("GetIPRangeByUUIDReturnsReadErrorOnTableFailure", func(t *testing.T) {
		store := setupIPRangeStore(t)
		require.NoError(t, store.db.GORM().Exec("DROP TABLE ip_ranges").Error)

		_, err := store.GetIPRangeByUUID(context.Background(), "any")
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
	})

	t.Run("GetIPRangesReturnsAccountLookupError", func(t *testing.T) {
		store := setupIPRangeStore(t)
		missingAccount := "missing-account"

		_, err := store.GetIPRanges(context.Background(), &missingAccount, nil)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.NotNil(t, customErr)
	})

	t.Run("GetIPRangesReturnsReadErrorWhenFindFails", func(t *testing.T) {
		store := setupIPRangeStore(t)
		require.NoError(t, store.db.GORM().Exec("DROP TABLE ip_ranges").Error)

		_, err := store.GetIPRanges(context.Background(), nil, nil)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
	})

	t.Run("GetIPRangesReturnsRegionLookupError", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-1"
		record := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-populate-error"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(record).Error)
		require.NoError(t, store.db.GORM().Exec("DROP TABLE regions").Error)

		_, err := store.GetIPRanges(context.Background(), nil, nil)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
	})
}

func TestUpdateDeleteIPRangeErrorBranches(t *testing.T) {
	t.Run("UpdateIPRangeNetworkReturnsUpdateErrorWhenSaveFails", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-old"
		record := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-update-save-fail"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(record).Error)
		require.NoError(t, store.db.GORM().Exec(
			"CREATE TRIGGER fail_ip_range_update BEFORE UPDATE ON ip_ranges BEGIN SELECT RAISE(FAIL, 'forced update failure'); END;",
		).Error)

		updatedNetwork := "vpc-new"
		_, err := store.UpdateIPRangeNetwork(context.Background(), &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: record.UUID},
			Network:   &updatedNetwork,
		})
		require.Error(t, err)
	})

	t.Run("UpdateIPRangeNetworkReturnsNotFoundAfterSaveWhenRecordSoftDeleted", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-old"
		record := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-update-not-found"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(record).Error)
		require.NoError(t, store.db.GORM().Exec(
			"CREATE TRIGGER soft_delete_ip_range_after_update AFTER UPDATE ON ip_ranges BEGIN UPDATE ip_ranges SET deleted_at = CURRENT_TIMESTAMP WHERE id = NEW.id; END;",
		).Error)

		updatedNetwork := "vpc-new"
		_, err := store.UpdateIPRangeNetwork(context.Background(), &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: record.UUID},
			Network:   &updatedNetwork,
		})
		require.Error(t, err)
	})

	t.Run("UpdateIPRangeNetworkReturnsAccountLookupError", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-old"
		record := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-update-account-error"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(record).Error)
		require.NoError(t, store.db.GORM().Exec("DROP TABLE accounts").Error)

		updatedNetwork := "vpc-new"
		_, err := store.UpdateIPRangeNetwork(context.Background(), &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: record.UUID},
			Network:   &updatedNetwork,
		})
		require.Error(t, err)
	})

	t.Run("UpdateIPRangeNetworkReturnsRegionLookupError", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-old"
		record := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-update-populate-error"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(record).Error)
		require.NoError(t, store.db.GORM().Exec("DROP TABLE regions").Error)

		updatedNetwork := "vpc-new"
		_, err := store.UpdateIPRangeNetwork(context.Background(), &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: record.UUID},
			Network:   &updatedNetwork,
		})
		require.Error(t, err)
	})

	t.Run("DeleteIPRangeReturnsReadErrorWhenLookupFails", func(t *testing.T) {
		store := setupIPRangeStore(t)
		require.NoError(t, store.db.GORM().Exec("DROP TABLE ip_ranges").Error)

		_, err := store.DeleteIPRange(context.Background(), "irrelevant")
		require.Error(t, err)
	})

	t.Run("DeleteIPRangeReturnsRegionLookupError", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-1"
		record := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-delete-populate-error"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(record).Error)
		require.NoError(t, store.db.GORM().Exec("DROP TABLE regions").Error)

		_, err := store.DeleteIPRange(context.Background(), record.UUID)
		require.Error(t, err)
	})

	t.Run("DeleteIPRangeReturnsDeleteErrorWhenSaveFails", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-1"
		record := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-delete-save-fail"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(record).Error)
		require.NoError(t, store.db.GORM().Exec(
			"CREATE TRIGGER fail_ip_range_delete_update BEFORE UPDATE ON ip_ranges BEGIN SELECT RAISE(FAIL, 'forced delete failure'); END;",
		).Error)

		_, err := store.DeleteIPRange(context.Background(), record.UUID)
		require.Error(t, err)
	})
}

func TestIsIPRangeBlockUniqueViolation(t *testing.T) {
	// idx_ip_ranges_unique_ic_block is what catches two concurrent carves that both pass the
	// SELECT-then-INSERT overlap check, so its violation has to be recognised as a conflict rather
	// than reported as a generic insert failure.
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "nil", err: nil, expected: false},
		{name: "typedSentinel", err: gorm.ErrDuplicatedKey, expected: true},
		{name: "wrappedTypedSentinel", err: fmt.Errorf("insert failed: %w", gorm.ErrDuplicatedKey), expected: true},
		{name: "postgresMessage", err: errors.New(`ERROR: duplicate key value violates unique constraint "idx_ip_ranges_unique_ic_block"`), expected: true},
		{name: "sqliteMessage", err: errors.New("UNIQUE constraint failed: ip_ranges.start_ip"), expected: true},
		{name: "unrelated", err: errors.New("connection reset by peer"), expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isIPRangeBlockUniqueViolation(tt.err))
		})
	}
}

func TestGetRegionByUUIDReadError(t *testing.T) {
	store := setupIPRangeStore(t)
	require.NoError(t, store.db.GORM().Exec("DROP TABLE regions").Error)

	_, err := store.getRegionByUUID(context.Background(), "any")
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}
