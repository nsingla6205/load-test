package database

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
)

func setupReserveICStore(t *testing.T) *DataStoreRepository {
	t.Helper()
	db, err := SetupTestDB()
	require.NoError(t, err, "Failed to set up test database")
	store := NewDataStoreRepository(gormwrapper.New(db))
	require.NoError(t, ClearInMemoryDB(store.db.GORM()), "Failed to clean up test database")
	return store
}

func seedICIPRange(t *testing.T, store *DataStoreRepository, id int64, startIP, endIP string) {
	t.Helper()
	network := fmt.Sprintf("host-proj-%d/test-vpc", id)
	ir := &datamodel.IPRange{
		BaseModel: datamodel.BaseModel{ID: id, UUID: fmt.Sprintf("ic-iprange-uuid-%d", id)},
		Network:   &network,
		StartIP:   startIP,
		EndIP:     endIP,
		Netmask:   "255.255.255.240",
		LifType:   "interclusterLIF",
	}
	require.NoError(t, store.db.GORM().Create(ir).Error, "Failed to seed IPRange")
}

func seedICLif(t *testing.T, store *DataStoreRepository, poolID, ipRangeID, accountID int64, ip string) {
	t.Helper()
	lif := &datamodel.Lif{
		BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
		Name:      fmt.Sprintf("ic-lif-seed-%s", ip),
		Type:      lifTypeInterclusterDB,
		IPAddress: ip,
		AccountID: accountID,
		PoolID:    sql.NullInt64{Int64: poolID, Valid: poolID != 0},
		IPRangeID: sql.NullInt64{Int64: ipRangeID, Valid: ipRangeID != 0},
		LifDetails: &datamodel.LifDetails{
			ProtocolType: lifTypeInterclusterDB,
		},
	}
	require.NoError(t, store.db.GORM().Create(lif).Error, "Failed to seed IC lif")
}

func requireConflict(t *testing.T, err error) {
	t.Helper()
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

// Test_ReserveInterclusterLifIPs covers the concurrency-sensitive allocation
// logic: fresh reservation ordering, idempotent re-read, used-IP exclusion, and
// the insufficient-capacity conflict error.
func Test_ReserveInterclusterLifIPs(t *testing.T) {
	const (
		accountID = int64(100)
		ipRangeID = int64(1)
	)

	t.Run("FreshReservationPicksNFreeIPsInNumericOrder", func(t *testing.T) {
		store := setupReserveICStore(t)
		// Full /28 CIDR bounds: network/broadcast excluded → usable .1 .. .14
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.15")

		reserved, err := store.ReserveInterclusterLifIPs(context.Background(), 1, accountID, ipRangeID, 4)
		require.NoError(t, err)
		assert.Equal(t, []string{"10.44.0.1", "10.44.0.2", "10.44.0.3", "10.44.0.4"}, reserved)

		lifs, err := store.ListInterclusterLifsByPoolID(context.Background(), 1)
		require.NoError(t, err)
		require.Len(t, lifs, 4)
		// HAPairIndex groups consecutive IP pairs (VM1 + VM2) per HA pair.
		assert.Equal(t, 0, lifs[0].LifDetails.HAPairIndex)
		assert.Equal(t, 0, lifs[1].LifDetails.HAPairIndex)
		assert.Equal(t, 1, lifs[2].LifDetails.HAPairIndex)
		assert.Equal(t, 1, lifs[3].LifDetails.HAPairIndex)
		for _, lif := range lifs {
			assert.Equal(t, lifTypeInterclusterDB, lif.Type)
			assert.Equal(t, "255.255.255.255", lif.SubnetMask)
			require.NotNil(t, lif.LifDetails)
			assert.Equal(t, lifTypeInterclusterDB, lif.LifDetails.ProtocolType)
		}
	})

	t.Run("IdempotentReturnOfExistingPoolLifsInNumericOrder", func(t *testing.T) {
		store := setupReserveICStore(t)
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.31")
		// Pre-existing reservation for pool 5, inserted out of order and with a
		// pair (.2, .10) that would sort wrong lexicographically (".10" < ".2").
		seedICLif(t, store, 5, ipRangeID, accountID, "10.44.0.10")
		seedICLif(t, store, 5, ipRangeID, accountID, "10.44.0.2")

		reserved, err := store.ReserveInterclusterLifIPs(context.Background(), 5, accountID, ipRangeID, 2)
		require.NoError(t, err)
		// Numeric order, not lexicographic.
		assert.Equal(t, []string{"10.44.0.2", "10.44.0.10"}, reserved)

		// Idempotent: no new rows created.
		lifs, err := store.ListInterclusterLifsByPoolID(context.Background(), 5)
		require.NoError(t, err)
		assert.Len(t, lifs, 2)
	})

	t.Run("PartialReservationReturnsConflict", func(t *testing.T) {
		store := setupReserveICStore(t)
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.15")
		seedICLif(t, store, 5, ipRangeID, accountID, "10.44.0.1")
		seedICLif(t, store, 5, ipRangeID, accountID, "10.44.0.2")

		reserved, err := store.ReserveInterclusterLifIPs(context.Background(), 5, accountID, ipRangeID, 4)
		require.Error(t, err)
		assert.Nil(t, reserved)
		requireConflict(t, err)
	})

	t.Run("StaleDifferentIPRangeIDReturnsConflict", func(t *testing.T) {
		store := setupReserveICStore(t)
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.15")
		seedICIPRange(t, store, 2, "10.44.1.0", "10.44.1.15")
		seedICLif(t, store, 5, 2, accountID, "10.44.1.1")
		seedICLif(t, store, 5, 2, accountID, "10.44.1.2")

		reserved, err := store.ReserveInterclusterLifIPs(context.Background(), 5, accountID, ipRangeID, 2)
		require.Error(t, err)
		assert.Nil(t, reserved)
		requireConflict(t, err)
	})

	t.Run("SkipsAlreadyUsedIPsInBlock", func(t *testing.T) {
		store := setupReserveICStore(t)
		// Full /29: usable hosts are .1 .. .6 after excluding network/broadcast.
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.7")
		seedICLif(t, store, 999, ipRangeID, accountID, "10.44.0.1")
		seedICLif(t, store, 999, ipRangeID, accountID, "10.44.0.2")

		reserved, err := store.ReserveInterclusterLifIPs(context.Background(), 1, accountID, ipRangeID, 2)
		require.NoError(t, err)
		assert.Equal(t, []string{"10.44.0.3", "10.44.0.4"}, reserved)
	})

	t.Run("CIDRBoundsExcludeNetworkAndBroadcast", func(t *testing.T) {
		store := setupReserveICStore(t)
		// /29 full bounds → 6 usable hosts; request all 6 and ensure .0/.7 never reserved.
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.7")

		reserved, err := store.ReserveInterclusterLifIPs(context.Background(), 1, accountID, ipRangeID, 6)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"10.44.0.1", "10.44.0.2", "10.44.0.3",
			"10.44.0.4", "10.44.0.5", "10.44.0.6",
		}, reserved)
		for _, ip := range reserved {
			assert.NotEqual(t, "10.44.0.0", ip)
			assert.NotEqual(t, "10.44.0.7", ip)
		}
	})

	t.Run("InsufficientFreeIPsReturnsConflict", func(t *testing.T) {
		store := setupReserveICStore(t)
		// Only 2 candidates (span < 4 keeps inclusive bounds) but 4 requested.
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.1")

		reserved, err := store.ReserveInterclusterLifIPs(context.Background(), 1, accountID, ipRangeID, 4)
		require.Error(t, err)
		assert.Nil(t, reserved)
		requireConflict(t, err)
	})

	t.Run("InvalidInputReturnsValidationError", func(t *testing.T) {
		store := setupReserveICStore(t)
		_, err := store.ReserveInterclusterLifIPs(context.Background(), 0, accountID, ipRangeID, 4)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
	})

	t.Run("MissingIPRangeReturnsNotFound", func(t *testing.T) {
		store := setupReserveICStore(t)
		_, err := store.ReserveInterclusterLifIPs(context.Background(), 1, accountID, 404, 2)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
	})
}

func Test_hostIPsInRange(t *testing.T) {
	t.Run("ExcludesNetworkAndBroadcastForCIDRSizedSpan", func(t *testing.T) {
		ips, err := hostIPsInRange("10.44.0.0", "10.44.0.7")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"10.44.0.1", "10.44.0.2", "10.44.0.3",
			"10.44.0.4", "10.44.0.5", "10.44.0.6",
		}, ips)
	})

	t.Run("KeepsInclusiveBoundsForSmallSpan", func(t *testing.T) {
		ips, err := hostIPsInRange("10.44.0.0", "10.44.0.1")
		require.NoError(t, err)
		assert.Equal(t, []string{"10.44.0.0", "10.44.0.1"}, ips)
	})
}
