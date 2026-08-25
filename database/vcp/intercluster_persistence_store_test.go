package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func setupInterclusterStorage(t *testing.T) Storage {
	t.Helper()

	store, err := SetupStorageForTest(log.NewLogger())
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Logf("Error closing store: %v", err)
		}
	})
	require.NoError(t, ClearInMemoryDB(store.DB()))
	return store
}

// TestPersistenceStore_InterclusterAddressSpaceLifecycle drives the IC LIF
// address-space calls through the Storage facade in the order the pool
// workflows use them: discover a carved range, reserve addresses, bind them to
// nodes on deploy, then release everything on pool delete.
func TestPersistenceStore_InterclusterAddressSpaceLifecycle(t *testing.T) {
	const (
		hostProjectNumber = "1234567890"
		vpcName           = "test-vpc"
		accountID         = int64(100)
		poolID            = int64(7)
		nodeID            = int64(42)
	)

	store := setupInterclusterStorage(t)
	ctx := context.Background()

	region := &datamodel.Region{
		BaseModel: datamodel.BaseModel{UUID: "ps-region-uuid"},
		Name:      "ps-region",
	}
	require.NoError(t, store.DB().Create(region).Error)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: "ps-account-uuid"},
		Name:      "ps-account",
	}
	require.NoError(t, store.DB().Create(account).Error)

	addressRange := &datamodel.AddressRange{
		BaseModel:         datamodel.BaseModel{UUID: "ps-ar-uuid"},
		Name:              "ps-ar",
		AddressRangeCidr:  "10.60.0.0/24",
		Network:           "projects/1234567890/global/networks/test-vpc",
		VpcName:           vpcName,
		HostProjectNumber: hostProjectNumber,
		LifType:           AddressRangeLifTypeInterclusterLIF,
		AddressRangeState: AddressRangeStateCreated,
	}
	require.NoError(t, store.DB().Create(addressRange).Error)

	network := "1234567890/test-vpc"
	ipRange := &datamodel.IPRange{
		BaseModel:      datamodel.BaseModel{UUID: "ps-ir-uuid"},
		Network:        &network,
		StartIP:        "10.60.0.0",
		EndIP:          "10.60.0.15",
		LifType:        LifTypeInterclusterLIF,
		AddressRangeID: sql.NullInt64{Int64: addressRange.ID, Valid: true},
		RegionID:       sql.NullInt64{Int64: region.ID, Valid: true},
		AccountID:      sql.NullInt64{Int64: account.ID, Valid: true},
	}
	require.NoError(t, store.DB().Create(ipRange).Error)

	// A second pool in the same VPC finds the existing carve instead of
	// requesting a new secondary range from GCP.
	ranges, err := store.ListInterclusterIPRangesForVPC(ctx, hostProjectNumber, vpcName)
	require.NoError(t, err)
	require.Len(t, ranges, 1)
	assert.Equal(t, ipRange.UUID, ranges[0].UUID)

	marked, err := store.MarkAddressRangeInUseByID(ctx, addressRange.ID)
	require.NoError(t, err)
	assert.True(t, marked)

	// The /28 bounds exclude network and broadcast, so .1 and .2 are first.
	reserved, err := store.ReserveInterclusterLifIPs(ctx, poolID, accountID, ipRange.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.60.0.1", "10.60.0.2"}, reserved)

	lifs, err := store.ListInterclusterLifsByPoolID(ctx, poolID)
	require.NoError(t, err)
	require.Len(t, lifs, 2)

	require.NoError(t, store.BindInterclusterLifToNode(ctx, poolID, "10.60.0.1", nodeID))
	lifs, err = store.ListInterclusterLifsByPoolID(ctx, poolID)
	require.NoError(t, err)
	require.Len(t, lifs, 2)
	assert.Equal(t, nodeID, lifs[0].NodeID)

	// While the range is still carved, the address range stays IN_USE.
	released, err := store.ReleaseInterclusterAddressRangeIfUnused(ctx, addressRange.ID)
	require.NoError(t, err)
	assert.False(t, released)

	require.NoError(t, store.DeleteInterclusterLifsByPoolID(ctx, poolID))
	lifs, err = store.ListInterclusterLifsByPoolID(ctx, poolID)
	require.NoError(t, err)
	assert.Empty(t, lifs)

	deleted, err := store.DeleteIPRangeByID(ctx, ipRange.ID)
	require.NoError(t, err)
	require.NotNil(t, deleted.DeletedAt)
	assert.Equal(t, region.UUID, deleted.RegionUUID)
	assert.Equal(t, account.UUID, deleted.AccountUUID)

	// With no carved ranges left the address range returns to CREATED so it can
	// be reused or cleaned up.
	released, err = store.ReleaseInterclusterAddressRangeIfUnused(ctx, addressRange.ID)
	require.NoError(t, err)
	assert.True(t, released)
}

func TestPersistenceStore_InterclusterGuardsPropagate(t *testing.T) {
	store := setupInterclusterStorage(t)
	ctx := context.Background()

	// Each wrapper must surface the datastore's validation/no-op semantics
	// rather than swallowing them.
	_, err := store.DeleteIPRangeByID(ctx, 0)
	require.Error(t, err)

	_, err = store.MarkAddressRangeInUseByID(ctx, 0)
	require.Error(t, err)

	_, err = store.ReleaseInterclusterAddressRangeIfUnused(ctx, 0)
	require.Error(t, err)

	_, err = store.ReserveInterclusterLifIPs(ctx, 0, 0, 0, 0)
	require.Error(t, err)

	lifs, err := store.ListInterclusterLifsByPoolID(ctx, 0)
	require.NoError(t, err)
	assert.Nil(t, lifs)

	require.NoError(t, store.BindInterclusterLifToNode(ctx, 0, "", 0))
	require.NoError(t, store.DeleteInterclusterLifsByPoolID(ctx, 0))
}

func TestPersistenceStore_UpdatePoolSubnetNames(t *testing.T) {
	store := setupInterclusterStorage(t)

	// The wrapper must propagate the datastore lookup failure for an unknown
	// pool instead of reporting success.
	err := store.UpdatePoolSubnetNames(context.Background(), "missing-pool-uuid", "sn-host-project", "regional-tenant-project", []string{"vsa-subnet-1"})
	require.Error(t, err)
}
