package database

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

func seedAddressRange(t *testing.T, store *DataStoreRepository, uuid, hostProjectNumber, vpcName, lifType string) *datamodel.AddressRange {
	t.Helper()

	ar := &datamodel.AddressRange{
		BaseModel:         datamodel.BaseModel{UUID: uuid},
		Name:              uuid,
		AddressRangeCidr:  "10.60.0.0/24",
		Network:           fmt.Sprintf("projects/%s/global/networks/%s", hostProjectNumber, vpcName),
		VpcName:           vpcName,
		HostProjectNumber: hostProjectNumber,
		LifType:           lifType,
		AddressRangeState: AddressRangeStateCreated,
	}
	require.NoError(t, store.db.GORM().Create(ar).Error)
	return ar
}

func seedCarvedIPRange(t *testing.T, store *DataStoreRepository, uuid string, addressRangeID int64, startIP, endIP, lifType string) *datamodel.IPRange {
	t.Helper()

	network := "host-proj/test-vpc"
	ir := &datamodel.IPRange{
		BaseModel:      datamodel.BaseModel{UUID: uuid},
		Network:        &network,
		StartIP:        startIP,
		EndIP:          endIP,
		LifType:        lifType,
		AddressRangeID: sql.NullInt64{Int64: addressRangeID, Valid: addressRangeID != 0},
	}
	require.NoError(t, store.db.GORM().Create(ir).Error)
	return ir
}

// TestListInterclusterIPRangesForVPC covers the lookup that finds already-carved
// IC LIF blocks so a second pool in the same VPC reuses them instead of carving
// a duplicate secondary range.
func TestListInterclusterIPRangesForVPC(t *testing.T) {
	const (
		hostProjectNumber = "1234567890"
		vpcName           = "test-vpc"
	)

	t.Run("ReturnsNilWhenVPCHasNoInterclusterAddressRange", func(t *testing.T) {
		store := setupIPRangeStore(t)
		// A dataLIF address range in the same VPC must not qualify.
		seedAddressRange(t, store, "ar-data-1", hostProjectNumber, vpcName, "dataLIF")

		ranges, err := store.ListInterclusterIPRangesForVPC(context.Background(), hostProjectNumber, vpcName)
		require.NoError(t, err)
		assert.Nil(t, ranges)
	})

	t.Run("ReturnsCarvedRangesLinkedToTheInterclusterAddressRange", func(t *testing.T) {
		store := setupIPRangeStore(t)
		icAR := seedAddressRange(t, store, "ar-ic-1", hostProjectNumber, vpcName, AddressRangeLifTypeInterclusterLIF)
		carved := seedCarvedIPRange(t, store, "ir-ic-1", icAR.ID, "10.60.0.0", "10.60.0.15", LifTypeInterclusterLIF)

		ranges, err := store.ListInterclusterIPRangesForVPC(context.Background(), hostProjectNumber, vpcName)
		require.NoError(t, err)
		require.Len(t, ranges, 1)
		assert.Equal(t, carved.UUID, ranges[0].UUID)
		assert.Equal(t, "10.60.0.0", ranges[0].StartIP)
	})

	t.Run("ExcludesRangesOfOtherLifTypesAndOtherAddressRanges", func(t *testing.T) {
		store := setupIPRangeStore(t)
		icAR := seedAddressRange(t, store, "ar-ic-2", hostProjectNumber, vpcName, AddressRangeLifTypeInterclusterLIF)
		dataAR := seedAddressRange(t, store, "ar-data-2", hostProjectNumber, vpcName, "dataLIF")

		wanted := seedCarvedIPRange(t, store, "ir-ic-2", icAR.ID, "10.60.0.0", "10.60.0.15", LifTypeInterclusterLIF)
		// Same parent but a data range: filtered out by lif_type.
		seedCarvedIPRange(t, store, "ir-data-2", icAR.ID, "10.61.0.0", "10.61.0.15", "dataLIF")
		// Intercluster type but parented by a dataLIF address range: filtered
		// out by address_range_id.
		seedCarvedIPRange(t, store, "ir-ic-orphan", dataAR.ID, "10.62.0.0", "10.62.0.15", LifTypeInterclusterLIF)

		ranges, err := store.ListInterclusterIPRangesForVPC(context.Background(), hostProjectNumber, vpcName)
		require.NoError(t, err)
		require.Len(t, ranges, 1)
		assert.Equal(t, wanted.UUID, ranges[0].UUID)
	})

	t.Run("ScopesResultsToTheRequestedVPC", func(t *testing.T) {
		store := setupIPRangeStore(t)
		otherAR := seedAddressRange(t, store, "ar-ic-other", hostProjectNumber, "other-vpc", AddressRangeLifTypeInterclusterLIF)
		seedCarvedIPRange(t, store, "ir-ic-other", otherAR.ID, "10.63.0.0", "10.63.0.15", LifTypeInterclusterLIF)

		ranges, err := store.ListInterclusterIPRangesForVPC(context.Background(), hostProjectNumber, vpcName)
		require.NoError(t, err)
		assert.Nil(t, ranges)
	})
}

// TestDeleteIPRangeByID covers the release path used when the last pool on a
// carved IC LIF block goes away.
func TestDeleteIPRangeByID(t *testing.T) {
	t.Run("ReturnsValidationErrorWhenIDMissing", func(t *testing.T) {
		store := setupIPRangeStore(t)

		deleted, err := store.DeleteIPRangeByID(context.Background(), 0)
		require.Error(t, err)
		assert.Nil(t, deleted)
		requireVCPError(t, err, vsaerrors.ErrInputValidationError)
	})

	t.Run("ReturnsNotFoundWhenRangeDoesNotExist", func(t *testing.T) {
		store := setupIPRangeStore(t)

		deleted, err := store.DeleteIPRangeByID(context.Background(), 9999)
		require.Error(t, err)
		assert.Nil(t, deleted)
		requireVCPError(t, err, vsaerrors.ErrDatabaseDataNotFoundError)
	})

	t.Run("SoftDeletesAndEnrichesAccountAndRegion", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)

		network := "host-proj/test-vpc"
		record := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ir-delete-by-id"},
			Network:   &network,
			StartIP:   "10.60.0.0",
			EndIP:     "10.60.0.15",
			LifType:   LifTypeInterclusterLIF,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(record).Error)

		ctx := context.Background()
		deleted, err := store.DeleteIPRangeByID(ctx, record.ID)
		require.NoError(t, err)
		require.NotNil(t, deleted.DeletedAt)
		assert.Equal(t, record.UUID, deleted.UUID)
		assert.Equal(t, region.UUID, deleted.RegionUUID)
		assert.Equal(t, region.Name, deleted.RegionName)
		assert.Equal(t, account.UUID, deleted.AccountUUID)
		assert.Equal(t, account.Name, deleted.AccountName)

		// The soft-deleted block must no longer be offered for reuse.
		_, err = store.DeleteIPRangeByID(ctx, record.ID)
		require.Error(t, err)
		requireVCPError(t, err, vsaerrors.ErrDatabaseDataNotFoundError)
	})
}
