package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"gorm.io/gorm"
)

func seedDataLIFIPRange(t *testing.T, store *DataStoreRepository) (*datamodel.IPRange, *datamodel.Account, *datamodel.Host) {
	t.Helper()
	region, account, host := seedIPRangeReferences(t, store)
	network := "vpc-po-1"
	ipRange := &datamodel.IPRange{
		BaseModel: datamodel.BaseModel{UUID: "ip-range-po-1"},
		StartIP:   "10.0.0.10",
		EndIP:     "10.0.0.15",
		Gateway:   "10.0.0.1",
		Netmask:   "255.255.255.0",
		Network:   &network,
		RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
		AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		LifType:   LifTypeDataLIF,
	}
	require.NoError(t, store.db.GORM().Create(ipRange).Error)
	return ipRange, account, host
}

func validDataLifReservation(t *testing.T, ipRange *datamodel.IPRange, account *datamodel.Account, name, ip string) *datamodel.Lif {
	t.Helper()
	return &datamodel.Lif{
		Name:      name,
		AccountID: account.ID,
		IPAddress: ip,
		IPRangeID: sql.NullInt64{Int64: ipRange.ID, Valid: true},
		NodeID:    1,
	}
}

func fillDataLIFIPRange(t *testing.T, store *DataStoreRepository, ipRange *datamodel.IPRange, account *datamodel.Account) {
	t.Helper()
	for i, ip := range []string{"10.0.0.10", "10.0.0.11", "10.0.0.12", "10.0.0.13", "10.0.0.14", "10.0.0.15"} {
		_, err := store.CreateDataLifReservation(context.Background(), &datamodel.Lif{
			Name:      "lif-" + ip,
			AccountID: account.ID,
			IPAddress: ip,
			IPRangeID: sql.NullInt64{Int64: ipRange.ID, Valid: true},
			NodeID:    int64(i + 1),
		})
		require.NoError(t, err)
	}
}

func TestGetAllocatedIPsInIPRange(t *testing.T) {
	store := setupIPRangeStore(t)
	ipRange, account, _ := seedDataLIFIPRange(t, store)
	ctx := context.Background()

	t.Run("rejectsNonPositiveID", func(t *testing.T) {
		_, err := store.GetAllocatedIPsInIPRange(ctx, 0)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
	})

	t.Run("returnsEmptyWhenNoReservations", func(t *testing.T) {
		got, err := store.GetAllocatedIPsInIPRange(ctx, ipRange.ID)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("returnsActiveReservations", func(t *testing.T) {
		_, err := store.CreateDataLifReservation(ctx, validDataLifReservation(t, ipRange, account, "lif-a", "10.0.0.10"))
		require.NoError(t, err)
		_, err = store.CreateDataLifReservation(ctx, validDataLifReservation(t, ipRange, account, "lif-b", "10.0.0.11"))
		require.NoError(t, err)

		got, err := store.GetAllocatedIPsInIPRange(ctx, ipRange.ID)
		require.NoError(t, err)
		assert.True(t, got["10.0.0.10"])
		assert.True(t, got["10.0.0.11"])
	})

	t.Run("ignoresSoftDeletedReservations", func(t *testing.T) {
		created, err := store.CreateDataLifReservation(ctx, validDataLifReservation(t, ipRange, account, "lif-del", "10.0.0.12"))
		require.NoError(t, err)
		require.NoError(t, store.DeleteLif(ctx, created))

		got, err := store.GetAllocatedIPsInIPRange(ctx, ipRange.ID)
		require.NoError(t, err)
		assert.False(t, got["10.0.0.12"])
	})
}

func TestValidateIPAddressesAvailableInIPRange(t *testing.T) {
	ctx := context.Background()

	t.Run("succeedsWhenCapacityAvailable", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, _, _ := seedDataLIFIPRange(t, store)
		err := store.ValidateIPAddressesAvailableInIPRange(ctx, ipRange.UUID, 2)
		require.NoError(t, err)
	})

	t.Run("rejectsUnknownRangeUUID", func(t *testing.T) {
		store := setupIPRangeStore(t)
		seedDataLIFIPRange(t, store)
		err := store.ValidateIPAddressesAvailableInIPRange(ctx, "missing-range", 1)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
	})

	t.Run("rejectsNonPositiveCount", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, _, _ := seedDataLIFIPRange(t, store)
		err := store.ValidateIPAddressesAvailableInIPRange(ctx, ipRange.UUID, 0)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
	})

	t.Run("rejectsInvalidRangeBounds", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, _, _ := seedDataLIFIPRange(t, store)
		badRange := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{ID: ipRange.ID},
			StartIP:   "10.0.0.20",
			EndIP:     "10.0.0.10",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
		}
		err := store.validateIPAddressesAvailable(ctx, badRange, 1)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
	})

	t.Run("rejectsNilRange", func(t *testing.T) {
		store := setupIPRangeStore(t)
		err := store.validateIPAddressesAvailable(ctx, nil, 1)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
	})

	t.Run("failsWhenRangeFull", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, account, _ := seedDataLIFIPRange(t, store)
		fillDataLIFIPRange(t, store, ipRange, account)

		err := store.ValidateIPAddressesAvailableInIPRange(ctx, ipRange.UUID, 1)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
		assert.True(t, customerrors.IsUnavailableErr(customErr.Unwrap()))
	})
}

func TestValidateDataLifReservation(t *testing.T) {
	ipRange := &datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 1}}
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}

	cases := []struct {
		name    string
		lif     *datamodel.Lif
		wantMsg string
	}{
		{name: "nil lif", lif: nil, wantMsg: "lif must not be nil"},
		{name: "empty name", lif: validDataLifReservation(t, ipRange, account, " ", "10.0.0.10"), wantMsg: "lif name must not be empty"},
		{
			name: "empty ip",
			lif: func() *datamodel.Lif {
				l := validDataLifReservation(t, ipRange, account, "lif", "10.0.0.10")
				l.IPAddress = ""
				return l
			}(),
			wantMsg: "lif IP address must not be empty",
		},
		{
			name: "invalid account",
			lif: func() *datamodel.Lif {
				l := validDataLifReservation(t, ipRange, account, "lif", "10.0.0.10")
				l.AccountID = 0
				return l
			}(),
			wantMsg: "lif account id must be positive",
		},
		{
			name: "missing ip range",
			lif: func() *datamodel.Lif {
				l := validDataLifReservation(t, ipRange, account, "lif", "10.0.0.10")
				l.IPRangeID = sql.NullInt64{}
				return l
			}(),
			wantMsg: "lif ip_range_id must be set",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDataLifReservation(tc.lif)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

func TestCreateDataLifReservation(t *testing.T) {
	store := setupIPRangeStore(t)
	ipRange, account, _ := seedDataLIFIPRange(t, store)
	ctx := context.Background()

	t.Run("succeedsAndSetsMetadata", func(t *testing.T) {
		created, err := store.CreateDataLifReservation(ctx, validDataLifReservation(t, ipRange, account, "po-lif-ok", "10.0.0.10"))
		require.NoError(t, err)
		require.NotNil(t, created)
		assert.NotEmpty(t, created.UUID)
		assert.Equal(t, dataLIFType, created.Type)
		assert.Equal(t, "po-lif-ok", created.Name)
		assert.Equal(t, "10.0.0.10", created.IPAddress)
	})

	t.Run("wrapsValidationErrors", func(t *testing.T) {
		_, err := store.CreateDataLifReservation(ctx, nil)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
	})
}

func TestCreateDataLifReservationConflict(t *testing.T) {
	store := setupIPRangeStore(t)
	ipRange, account, _ := seedDataLIFIPRange(t, store)

	lif := validDataLifReservation(t, ipRange, account, "po-lif-1", "10.0.0.10")
	_, err := store.CreateDataLifReservation(context.Background(), lif)
	require.NoError(t, err)

	_, err = store.CreateDataLifReservation(context.Background(), validDataLifReservation(t, ipRange, account, "po-lif-2", "10.0.0.10"))
	require.Error(t, err)
	assert.True(t, customerrors.IsConflictErr(err))
}

func TestIsDataLifIPRangeUniqueViolation(t *testing.T) {
	t.Run("gorm duplicated key", func(t *testing.T) {
		assert.True(t, isDataLifIPRangeUniqueViolation(gorm.ErrDuplicatedKey))
	})

	t.Run("postgres unique constraint message", func(t *testing.T) {
		assert.True(t, isDataLifIPRangeUniqueViolation(
			errors.New(`pq: duplicate key value violates unique constraint "idx_lifs_ip_range_ip_active"`)))
	})

	t.Run("sqlite unique constraint message", func(t *testing.T) {
		assert.True(t, isDataLifIPRangeUniqueViolation(errors.New("UNIQUE constraint failed: lifs.ip_range_id")))
	})

	t.Run("other database errors", func(t *testing.T) {
		assert.False(t, isDataLifIPRangeUniqueViolation(errors.New("connection reset")))
	})
}

func TestGetLifsBySvmID(t *testing.T) {
	store := setupIPRangeStore(t)
	ipRange, account, _ := seedDataLIFIPRange(t, store)
	ctx := context.Background()

	svmA := &datamodel.Svm{BaseModel: datamodel.BaseModel{UUID: "svm-a"}, Name: "svm-a", AccountID: account.ID, PoolID: 1}
	svmB := &datamodel.Svm{BaseModel: datamodel.BaseModel{UUID: "svm-b"}, Name: "svm-b", AccountID: account.ID, PoolID: 2}
	require.NoError(t, store.db.GORM().Create(svmA).Error)
	require.NoError(t, store.db.GORM().Create(svmB).Error)

	t.Run("rejectsNonPositiveSvmID", func(t *testing.T) {
		_, err := store.GetLifsBySvmID(ctx, 0)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
	})

	t.Run("returnsOnlyMatchingSvmDataLifs", func(t *testing.T) {
		lifA := validDataLifReservation(t, ipRange, account, "lif-svm-a", "10.0.0.10")
		lifA.SvmID = sql.NullInt64{Int64: svmA.ID, Valid: true}
		_, err := store.CreateDataLifReservation(ctx, lifA)
		require.NoError(t, err)

		lifB := validDataLifReservation(t, ipRange, account, "lif-svm-b", "10.0.0.11")
		lifB.SvmID = sql.NullInt64{Int64: svmB.ID, Valid: true}
		_, err = store.CreateDataLifReservation(ctx, lifB)
		require.NoError(t, err)

		otherType := validDataLifReservation(t, ipRange, account, "lif-ic", "10.0.0.12")
		otherType.SvmID = sql.NullInt64{Int64: svmA.ID, Valid: true}
		otherType.Type = LifTypeInterclusterLIF
		require.NoError(t, store.db.GORM().Create(otherType).Error)

		got, err := store.GetLifsBySvmID(ctx, svmA.ID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "lif-svm-a", got[0].Name)
	})
}

func TestAllocateDataLifIPs(t *testing.T) {
	ctx := context.Background()

	t.Run("allocatesSequentialAddresses", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, account, _ := seedDataLIFIPRange(t, store)
		created, err := store.AllocateDataLifIPs(ctx, ipRange.UUID, []*datamodel.Lif{
			{Name: "po-lif-a", AccountID: account.ID, NodeID: 1},
			{Name: "po-lif-b", AccountID: account.ID, NodeID: 2},
		})
		require.NoError(t, err)
		require.Len(t, created, 2)
		assert.Equal(t, "10.0.0.10", created[0].IPAddress)
		assert.Equal(t, "10.0.0.11", created[1].IPAddress)
		assert.Equal(t, dataLIFType, created[0].Type)
		assert.Equal(t, ipRange.Netmask, created[0].SubnetMask)
		assert.Equal(t, ipRange.ID, created[0].IPRangeID.Int64)
	})

	t.Run("rejectsEmptyReservations", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, _, _ := seedDataLIFIPRange(t, store)
		_, err := store.AllocateDataLifIPs(ctx, ipRange.UUID, nil)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
	})

	t.Run("rejectsUnknownRangeUUID", func(t *testing.T) {
		store := setupIPRangeStore(t)
		_, account, _ := seedDataLIFIPRange(t, store)
		_, err := store.AllocateDataLifIPs(ctx, "missing-range", []*datamodel.Lif{
			{Name: "po-lif-a", AccountID: account.ID, NodeID: 1},
		})
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
	})

	t.Run("failsWhenRangeCannotSatisfyCount", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, account, _ := seedDataLIFIPRange(t, store)
		fillDataLIFIPRange(t, store, ipRange, account)

		_, err := store.AllocateDataLifIPs(ctx, ipRange.UUID, []*datamodel.Lif{
			{Name: "po-lif-full", AccountID: account.ID, NodeID: 99},
		})
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
	})

	t.Run("returnsExistingReservationsForSvmRetry", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, account, _ := seedDataLIFIPRange(t, store)
		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{UUID: "svm-po"}, Name: "svm-po", AccountID: account.ID, PoolID: 1}
		require.NoError(t, store.db.GORM().Create(svm).Error)

		input := []*datamodel.Lif{
			{Name: "po-lif-a", AccountID: account.ID, NodeID: 1, SvmID: sql.NullInt64{Int64: svm.ID, Valid: true}},
			{Name: "po-lif-b", AccountID: account.ID, NodeID: 2, SvmID: sql.NullInt64{Int64: svm.ID, Valid: true}},
		}
		first, err := store.AllocateDataLifIPs(ctx, ipRange.UUID, input)
		require.NoError(t, err)
		require.Len(t, first, 2)

		second, err := store.AllocateDataLifIPs(ctx, ipRange.UUID, input)
		require.NoError(t, err)
		require.Len(t, second, 2)
		assert.Equal(t, first[0].UUID, second[0].UUID)
		assert.Equal(t, first[1].UUID, second[1].UUID)
		assert.Equal(t, first[0].IPAddress, second[0].IPAddress)
		assert.Equal(t, first[1].IPAddress, second[1].IPAddress)

		allocated, err := store.GetAllocatedIPsInIPRange(ctx, ipRange.ID)
		require.NoError(t, err)
		assert.Len(t, allocated, 2)
	})

	t.Run("failsWhenExistingSvmReservationsArePartial", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, account, _ := seedDataLIFIPRange(t, store)
		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{UUID: "svm-partial"}, Name: "svm-partial", AccountID: account.ID, PoolID: 1}
		require.NoError(t, store.db.GORM().Create(svm).Error)

		lif := validDataLifReservation(t, ipRange, account, "po-lif-a", "10.0.0.10")
		lif.SvmID = sql.NullInt64{Int64: svm.ID, Valid: true}
		_, err := store.CreateDataLifReservation(ctx, lif)
		require.NoError(t, err)

		_, err = store.AllocateDataLifIPs(ctx, ipRange.UUID, []*datamodel.Lif{
			{Name: "po-lif-a", AccountID: account.ID, NodeID: 1, SvmID: sql.NullInt64{Int64: svm.ID, Valid: true}},
			{Name: "po-lif-b", AccountID: account.ID, NodeID: 2, SvmID: sql.NullInt64{Int64: svm.ID, Valid: true}},
		})
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.Contains(t, customErr.Unwrap().Error(), "inconsistent existing data LIF reservations")
	})

	t.Run("failsWhenExistingSvmReservationNodeMismatch", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, account, _ := seedDataLIFIPRange(t, store)
		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{UUID: "svm-node-mismatch"}, Name: "svm-node-mismatch", AccountID: account.ID, PoolID: 1}
		require.NoError(t, store.db.GORM().Create(svm).Error)

		lif := validDataLifReservation(t, ipRange, account, "po-lif-a", "10.0.0.10")
		lif.SvmID = sql.NullInt64{Int64: svm.ID, Valid: true}
		lif.NodeID = 1
		_, err := store.CreateDataLifReservation(ctx, lif)
		require.NoError(t, err)

		_, err = store.AllocateDataLifIPs(ctx, ipRange.UUID, []*datamodel.Lif{
			{Name: "po-lif-a", AccountID: account.ID, NodeID: 99, SvmID: sql.NullInt64{Int64: svm.ID, Valid: true}},
		})
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.Contains(t, customErr.Unwrap().Error(), "node_id")
	})
}

func TestCreateDataLifReservationWithRetry(t *testing.T) {
	ctx := context.Background()

	t.Run("retriesAfterConflictAndPicksNextIP", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, account, _ := seedDataLIFIPRange(t, store)
		_, err := store.CreateDataLifReservation(ctx, validDataLifReservation(t, ipRange, account, "existing", "10.0.0.10"))
		require.NoError(t, err)

		stale := validDataLifReservation(t, ipRange, account, "retry-lif", "10.0.0.10")
		created, err := store.createDataLifReservationWithRetry(ctx, stale, ipRange.ID, map[string]bool{})
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.11", created.IPAddress)
	})

	t.Run("failsWhenRangeExhaustedDuringRetry", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, account, _ := seedDataLIFIPRange(t, store)
		fillDataLIFIPRange(t, store, ipRange, account)

		stale := validDataLifReservation(t, ipRange, account, "retry-full", "10.0.0.10")
		_, err := store.createDataLifReservationWithRetry(ctx, stale, ipRange.ID, map[string]bool{})
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
	})

	t.Run("propagatesNonConflictErrors", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, _, _ := seedDataLIFIPRange(t, store)
		_, err := store.createDataLifReservationWithRetry(ctx, nil, ipRange.ID, map[string]bool{})
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
	})

	t.Run("failsWhenIPRangeMissingDuringRetry", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, account, _ := seedDataLIFIPRange(t, store)
		_, err := store.CreateDataLifReservation(ctx, validDataLifReservation(t, ipRange, account, "existing-for-missing", "10.0.0.10"))
		require.NoError(t, err)

		stale := validDataLifReservation(t, ipRange, account, "retry-missing-range", "10.0.0.10")
		_, err = store.createDataLifReservationWithRetry(ctx, stale, 99999, map[string]bool{})
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
	})
}

func TestDeleteLifsBySvmID(t *testing.T) {
	store := setupIPRangeStore(t)
	ipRange, account, _ := seedDataLIFIPRange(t, store)
	ctx := context.Background()

	t.Run("rejectsNonPositiveSvmID", func(t *testing.T) {
		err := store.DeleteLifsBySvmID(ctx, 0)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
	})

	t.Run("noOpWhenNoLifs", func(t *testing.T) {
		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{UUID: "svm-empty"}, Name: "svm-empty", AccountID: account.ID, PoolID: 3}
		require.NoError(t, store.db.GORM().Create(svm).Error)
		require.NoError(t, store.DeleteLifsBySvmID(ctx, svm.ID))
	})

	t.Run("softDeletesAllSvmDataLifs", func(t *testing.T) {
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "svm-po-1"},
			Name:      "svm_po_test",
			AccountID: account.ID,
			PoolID:    1,
		}
		require.NoError(t, store.db.GORM().Create(svm).Error)

		_, err := store.CreateDataLifReservation(ctx, &datamodel.Lif{
			Name:      "po-lif-del",
			AccountID: account.ID,
			IPAddress: "10.0.0.10",
			IPRangeID: sql.NullInt64{Int64: ipRange.ID, Valid: true},
			SvmID:     sql.NullInt64{Int64: svm.ID, Valid: true},
			NodeID:    1,
			Type:      dataLIFType,
		})
		require.NoError(t, err)

		require.NoError(t, store.DeleteLifsBySvmID(ctx, svm.ID))

		lifs, err := store.GetLifsBySvmID(ctx, svm.ID)
		require.NoError(t, err)
		assert.Empty(t, lifs)

		allocated, err := store.GetAllocatedIPsInIPRange(ctx, ipRange.ID)
		require.NoError(t, err)
		assert.Empty(t, allocated)
	})
}

func TestGetDataLIFIPRanges(t *testing.T) {
	store := setupIPRangeStore(t)
	ipRange, account, _ := seedDataLIFIPRange(t, store)

	got, err := store.GetDataLIFIPRanges(context.Background(), account.UUID, "vpc-po-1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, ipRange.UUID, got[0].UUID)
	assert.Equal(t, account.UUID, got[0].AccountUUID)
}

func TestGetNodesByHostIDAndNames(t *testing.T) {
	store := setupIPRangeStore(t)
	_, _, host := seedIPRangeReferences(t, store)

	node1 := &datamodel.Node{
		BaseModel: datamodel.BaseModel{UUID: "node-1"},
		Name:      "node-a",
		NodeAttributes: &datamodel.NodeDetails{
			ExternalUUID: "ext-a",
			HostID:       host.ID,
		},
	}
	node2 := &datamodel.Node{
		BaseModel: datamodel.BaseModel{UUID: "node-2"},
		Name:      "node-b",
		NodeAttributes: &datamodel.NodeDetails{
			ExternalUUID: "ext-b",
			HostID:       host.ID,
		},
	}
	require.NoError(t, store.db.GORM().Create(node1).Error)
	require.NoError(t, store.db.GORM().Create(node2).Error)

	got, err := store.GetNodesByHostIDAndNames(context.Background(), host.ID, []string{"node-a", "node-b"})
	require.NoError(t, err)
	assert.Equal(t, node1.ID, got["node-a"])
	assert.Equal(t, node2.ID, got["node-b"])

	_, err = store.GetNodesByHostIDAndNames(context.Background(), host.ID, []string{"node-a", "missing"})
	require.Error(t, err)

	otherHost := &datamodel.Host{
		BaseModel: datamodel.BaseModel{UUID: "other-host"},
		Name:      "other-host",
	}
	require.NoError(t, store.db.GORM().Create(otherHost).Error)
	_, err = store.GetNodesByHostIDAndNames(context.Background(), otherHost.ID, []string{"node-a"})
	require.Error(t, err)
}
