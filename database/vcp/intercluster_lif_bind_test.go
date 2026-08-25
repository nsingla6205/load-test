package database

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// newPostgresMockICStore returns a repository backed by sqlmock so the database
// error branches can be exercised without a live Postgres instance.
func newPostgresMockICStore(t *testing.T) (*DataStoreRepository, sqlmock.Sqlmock) {
	t.Helper()

	dbSQL, mock, err := sqlmock.New()
	require.NoError(t, err)

	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn:                 dbSQL,
		PreferSimpleProtocol: true,
	}), &gorm.Config{})
	require.NoError(t, err)

	return NewDataStoreRepository(gormwrapper.New(gormDB)), mock
}

func requireVCPError(t *testing.T, err error, trackingID int) {
	t.Helper()

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr), "expected a CustomError, got %v", err)
	assert.True(t, customErr.IsError(trackingID), "unexpected error code in %v", err)
}

// TestBindInterclusterLifToNode covers the post-deploy step that links a
// reserved IC LIF row to the node that ended up owning the address.
func TestBindInterclusterLifToNode(t *testing.T) {
	const (
		accountID = int64(100)
		ipRangeID = int64(1)
		poolID    = int64(7)
		nodeID    = int64(42)
	)

	t.Run("NoOpWhenIdentifiersMissing", func(t *testing.T) {
		store := setupReserveICStore(t)
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.15")
		seedICLif(t, store, poolID, ipRangeID, accountID, "10.44.0.1")

		ctx := context.Background()
		require.NoError(t, store.BindInterclusterLifToNode(ctx, 0, "10.44.0.1", nodeID))
		require.NoError(t, store.BindInterclusterLifToNode(ctx, poolID, "10.44.0.1", 0))
		require.NoError(t, store.BindInterclusterLifToNode(ctx, poolID, "   ", nodeID))

		// None of the guarded calls may touch the reserved row.
		lifs, err := store.ListInterclusterLifsByPoolID(ctx, poolID)
		require.NoError(t, err)
		require.Len(t, lifs, 1)
		assert.Equal(t, int64(0), lifs[0].NodeID)
	})

	t.Run("NoOpWhenNoLifMatchesTheAddress", func(t *testing.T) {
		store := setupReserveICStore(t)
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.15")
		seedICLif(t, store, poolID, ipRangeID, accountID, "10.44.0.1")

		// An address that was never reserved must not be an error: deploy can
		// report LIFs the control plane did not carve.
		require.NoError(t, store.BindInterclusterLifToNode(context.Background(), poolID, "10.44.0.9", nodeID))
	})

	t.Run("BindsNodeIDToTheReservedLif", func(t *testing.T) {
		store := setupReserveICStore(t)
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.15")
		seedICLif(t, store, poolID, ipRangeID, accountID, "10.44.0.1")
		seedICLif(t, store, poolID, ipRangeID, accountID, "10.44.0.2")

		ctx := context.Background()
		require.NoError(t, store.BindInterclusterLifToNode(ctx, poolID, "10.44.0.1", nodeID))

		lifs, err := store.ListInterclusterLifsByPoolID(ctx, poolID)
		require.NoError(t, err)
		require.Len(t, lifs, 2)
		assert.Equal(t, nodeID, lifs[0].NodeID)
		// The sibling reservation stays unbound.
		assert.Equal(t, int64(0), lifs[1].NodeID)
	})

	t.Run("PopulatesLifDetailsWhenMissing", func(t *testing.T) {
		store := setupReserveICStore(t)
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.15")

		// A row without LifDetails must gain the intercluster protocol type
		// rather than panic on the nil pointer.
		lif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "ic-lif-no-details",
			Type:      lifTypeInterclusterDB,
			IPAddress: "10.44.0.3",
			AccountID: accountID,
		}
		lif.PoolID.Int64, lif.PoolID.Valid = poolID, true
		lif.IPRangeID.Int64, lif.IPRangeID.Valid = ipRangeID, true
		require.NoError(t, store.db.GORM().Create(lif).Error)

		ctx := context.Background()
		require.NoError(t, store.BindInterclusterLifToNode(ctx, poolID, "10.44.0.3", nodeID))

		lifs, err := store.ListInterclusterLifsByPoolID(ctx, poolID)
		require.NoError(t, err)
		require.Len(t, lifs, 1)
		assert.Equal(t, nodeID, lifs[0].NodeID)
		require.NotNil(t, lifs[0].LifDetails)
		assert.Equal(t, lifTypeInterclusterDB, lifs[0].LifDetails.ProtocolType)
	})

	t.Run("WrapsReadFailureAsDatabaseReadError", func(t *testing.T) {
		store, mock := newPostgresMockICStore(t)
		mock.ExpectQuery("SELECT .* FROM .*lifs").WillReturnError(fmt.Errorf("simulated read failure"))

		err := store.BindInterclusterLifToNode(context.Background(), poolID, "10.44.0.1", nodeID)
		require.Error(t, err)
		requireVCPError(t, err, vsaerrors.ErrDatabaseDataReadError)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestDeleteInterclusterLifsByPoolID covers the pool-delete release path that
// frees reserved IC LIF addresses back to the carved range.
func TestDeleteInterclusterLifsByPoolID(t *testing.T) {
	const (
		accountID = int64(100)
		ipRangeID = int64(1)
		poolID    = int64(7)
		otherPool = int64(8)
	)

	t.Run("NoOpWhenPoolIDZero", func(t *testing.T) {
		store := setupReserveICStore(t)
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.15")
		seedICLif(t, store, poolID, ipRangeID, accountID, "10.44.0.1")

		ctx := context.Background()
		require.NoError(t, store.DeleteInterclusterLifsByPoolID(ctx, 0))

		lifs, err := store.ListInterclusterLifsByPoolID(ctx, poolID)
		require.NoError(t, err)
		assert.Len(t, lifs, 1)
	})

	t.Run("SoftDeletesOnlyTheRequestedPoolReservations", func(t *testing.T) {
		store := setupReserveICStore(t)
		seedICIPRange(t, store, ipRangeID, "10.44.0.0", "10.44.0.15")
		seedICLif(t, store, poolID, ipRangeID, accountID, "10.44.0.1")
		seedICLif(t, store, poolID, ipRangeID, accountID, "10.44.0.2")
		seedICLif(t, store, otherPool, ipRangeID, accountID, "10.44.0.3")

		ctx := context.Background()
		require.NoError(t, store.DeleteInterclusterLifsByPoolID(ctx, poolID))

		released, err := store.ListInterclusterLifsByPoolID(ctx, poolID)
		require.NoError(t, err)
		assert.Empty(t, released)

		// A neighbouring pool sharing the same carved range keeps its IPs.
		retained, err := store.ListInterclusterLifsByPoolID(ctx, otherPool)
		require.NoError(t, err)
		require.Len(t, retained, 1)
		assert.Equal(t, "10.44.0.3", retained[0].IPAddress)
	})

	t.Run("IsIdempotentWhenNothingIsReserved", func(t *testing.T) {
		store := setupReserveICStore(t)

		require.NoError(t, store.DeleteInterclusterLifsByPoolID(context.Background(), poolID))
	})
}

// TestListInterclusterLifsByPoolID covers the guard and error mapping of the
// reservation read used by both bind and release.
func TestListInterclusterLifsByPoolID(t *testing.T) {
	t.Run("ReturnsNilWhenPoolIDZero", func(t *testing.T) {
		store := setupReserveICStore(t)

		lifs, err := store.ListInterclusterLifsByPoolID(context.Background(), 0)
		require.NoError(t, err)
		assert.Nil(t, lifs)
	})

	t.Run("WrapsReadFailureAsDatabaseReadError", func(t *testing.T) {
		store, mock := newPostgresMockICStore(t)
		mock.ExpectQuery("SELECT .* FROM .*lifs").WillReturnError(fmt.Errorf("simulated read failure"))

		lifs, err := store.ListInterclusterLifsByPoolID(context.Background(), 7)
		require.Error(t, err)
		assert.Nil(t, lifs)
		requireVCPError(t, err, vsaerrors.ErrDatabaseDataReadError)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestReserveInterclusterLifIPs_MalformedRangeBounds ensures a corrupt carved
// range surfaces a validation error instead of allocating garbage addresses.
func TestReserveInterclusterLifIPs_MalformedRangeBounds(t *testing.T) {
	store := setupReserveICStore(t)
	seedICIPRange(t, store, 1, "not-an-ip", "10.44.0.15")

	reserved, err := store.ReserveInterclusterLifIPs(context.Background(), 7, 100, 1, 2)
	require.Error(t, err)
	assert.Nil(t, reserved)
	requireVCPError(t, err, vsaerrors.ErrInputValidationError)
}

// Test_compareIPAddressesNumerically pins the ordering helper that keeps IC LIF
// addresses in numeric rather than lexicographic order.
func Test_compareIPAddressesNumerically(t *testing.T) {
	// ".10" sorts before ".2" lexicographically, so the numeric comparison is
	// what keeps HA pair indexes aligned with the reserved addresses.
	assert.Negative(t, compareIPAddressesNumerically("10.44.0.2", "10.44.0.10"))
	assert.Positive(t, compareIPAddressesNumerically("10.44.0.10", "10.44.0.2"))
	assert.Zero(t, compareIPAddressesNumerically("10.44.0.2", "10.44.0.2"))

	// Unparseable input falls back to a plain string comparison.
	assert.Negative(t, compareIPAddressesNumerically("not-an-ip", "zzz"))
	assert.Zero(t, compareIPAddressesNumerically("not-an-ip", "not-an-ip"))

	// IPv6 addresses are not converted; fall back to string order without panicking.
	assert.NotPanics(t, func() {
		_ = compareIPAddressesNumerically("2001:db8::1", "2001:db8::2")
	})
}

func Test_ipToUint32_nilSafe(t *testing.T) {
	assert.Zero(t, ipToUint32(nil))
	assert.Zero(t, ipToUint32(net.ParseIP("2001:db8::1")))
	assert.Equal(t, uint32(0x0a2c0002), ipToUint32(net.ParseIP("10.44.0.2")))
}

func Test_hostIPsInRange_InvalidBounds(t *testing.T) {
	t.Run("RejectsUnparseableAddresses", func(t *testing.T) {
		_, err := hostIPsInRange("not-an-ip", "10.44.0.15")
		require.Error(t, err)

		_, err = hostIPsInRange("10.44.0.0", "not-an-ip")
		require.Error(t, err)
	})

	t.Run("RejectsInvertedRange", func(t *testing.T) {
		_, err := hostIPsInRange("10.44.0.15", "10.44.0.0")
		require.Error(t, err)
	})
}
