package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

// Test_addressRangeCIDRsOverlap pins the guard that keeps two interclusterLIF
// address ranges in the same VPC from claiming the same addresses.
func Test_addressRangeCIDRsOverlap(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		{name: "IdenticalRangesOverlap", a: "10.60.0.0/24", b: "10.60.0.0/24", expected: true},
		{name: "ContainedRangeOverlaps", a: "10.60.0.0/24", b: "10.60.0.16/28", expected: true},
		{name: "ContainingRangeOverlaps", a: "10.60.0.16/28", b: "10.60.0.0/24", expected: true},
		{name: "AdjacentRangesDoNotOverlap", a: "10.60.0.0/28", b: "10.60.0.16/28", expected: false},
		{name: "DisjointRangesDoNotOverlap", a: "10.60.0.0/24", b: "10.61.0.0/24", expected: false},
		// Unparseable input must fail open to "no overlap" so a malformed CIDR
		// is rejected by validation rather than by the overlap guard.
		{name: "InvalidFirstCIDRDoesNotOverlap", a: "not-a-cidr", b: "10.60.0.0/24", expected: false},
		{name: "InvalidSecondCIDRDoesNotOverlap", a: "10.60.0.0/24", b: "not-a-cidr", expected: false},
		// IPv6 has no 32-bit bounds, so the comparison cannot claim an overlap.
		{name: "IPv6RangeDoesNotOverlapIPv4", a: "2001:db8::/32", b: "10.60.0.0/24", expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, addressRangeCIDRsOverlap(tc.a, tc.b))
		})
	}
}

// TestCreateAddressRange_RejectsOverlappingInterclusterRange covers the
// multiple-IC-ranges-per-VPC rule: more than one is allowed, but only when the
// CIDRs are disjoint.
func TestCreateAddressRange_RejectsOverlappingInterclusterRange(t *testing.T) {
	const (
		hostProjectNumber = "1234567890"
		vpcName           = "test-vpc"
	)

	newICRange := func(name, cidr string) *datamodel.AddressRange {
		return &datamodel.AddressRange{
			Name:              name,
			AddressRangeCidr:  cidr,
			Network:           fmt.Sprintf("projects/%s/global/networks/%s", hostProjectNumber, vpcName),
			VpcName:           vpcName,
			HostProjectNumber: hostProjectNumber,
			LifType:           AddressRangeLifTypeInterclusterLIF,
		}
	}

	t.Run("AllowsSecondDisjointInterclusterRange", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ctx := context.Background()

		_, err := store.CreateAddressRange(ctx, newICRange("ic-range-1", "10.60.0.0/28"))
		require.NoError(t, err)

		created, err := store.CreateAddressRange(ctx, newICRange("ic-range-2", "10.60.0.16/28"))
		require.NoError(t, err)
		assert.Equal(t, AddressRangeStateCreated, created.AddressRangeState)
	})

	t.Run("RejectsOverlappingInterclusterRange", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ctx := context.Background()

		_, err := store.CreateAddressRange(ctx, newICRange("ic-range-1", "10.60.0.0/24"))
		require.NoError(t, err)

		_, err = store.CreateAddressRange(ctx, newICRange("ic-range-overlap", "10.60.0.16/28"))
		require.Error(t, err)
		requireVCPError(t, err, vsaerrors.ErrResourceStateConflictError)
	})
}

// TestMarkAddressRangeInUseByID covers the CREATED -> IN_USE transition that
// claims an interclusterLIF range for the first pool that carves from it.
func TestMarkAddressRangeInUseByID(t *testing.T) {
	const (
		hostProjectNumber = "1234567890"
		vpcName           = "test-vpc"
	)

	t.Run("ReturnsValidationErrorWhenIDMissing", func(t *testing.T) {
		store := setupIPRangeStore(t)

		marked, err := store.MarkAddressRangeInUseByID(context.Background(), 0)
		require.Error(t, err)
		assert.False(t, marked)
		requireVCPError(t, err, vsaerrors.ErrInputValidationError)
	})

	t.Run("MarksCreatedRangeInUseOnlyOnce", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ar := seedAddressRange(t, store, "ar-mark-1", hostProjectNumber, vpcName, AddressRangeLifTypeInterclusterLIF)

		ctx := context.Background()
		marked, err := store.MarkAddressRangeInUseByID(ctx, ar.ID)
		require.NoError(t, err)
		assert.True(t, marked)

		// The transition is guarded on the CREATED state, so a concurrent second
		// claim reports no rows affected instead of re-claiming.
		marked, err = store.MarkAddressRangeInUseByID(ctx, ar.ID)
		require.NoError(t, err)
		assert.False(t, marked)
	})
}

// TestReleaseInterclusterAddressRangeIfUnused covers the IN_USE -> CREATED
// release that only fires once every carved ip_range has been removed.
func TestReleaseInterclusterAddressRangeIfUnused(t *testing.T) {
	const (
		hostProjectNumber = "1234567890"
		vpcName           = "test-vpc"
	)

	t.Run("ReturnsValidationErrorWhenIDMissing", func(t *testing.T) {
		store := setupIPRangeStore(t)

		released, err := store.ReleaseInterclusterAddressRangeIfUnused(context.Background(), 0)
		require.Error(t, err)
		assert.False(t, released)
		requireVCPError(t, err, vsaerrors.ErrInputValidationError)
	})

	t.Run("DoesNotReleaseWhileRangesAreStillCarved", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ar := seedAddressRange(t, store, "ar-release-1", hostProjectNumber, vpcName, AddressRangeLifTypeInterclusterLIF)
		seedCarvedIPRange(t, store, "ir-release-1", ar.ID, "10.60.0.0", "10.60.0.15", LifTypeInterclusterLIF)

		ctx := context.Background()
		marked, err := store.MarkAddressRangeInUseByID(ctx, ar.ID)
		require.NoError(t, err)
		require.True(t, marked)

		released, err := store.ReleaseInterclusterAddressRangeIfUnused(ctx, ar.ID)
		require.NoError(t, err)
		assert.False(t, released)
	})

	t.Run("ReleasesOnceTheLastCarvedRangeIsDeleted", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ar := seedAddressRange(t, store, "ar-release-2", hostProjectNumber, vpcName, AddressRangeLifTypeInterclusterLIF)
		ir := seedCarvedIPRange(t, store, "ir-release-2", ar.ID, "10.60.0.0", "10.60.0.15", LifTypeInterclusterLIF)

		ctx := context.Background()
		marked, err := store.MarkAddressRangeInUseByID(ctx, ar.ID)
		require.NoError(t, err)
		require.True(t, marked)

		_, err = store.DeleteIPRangeByID(ctx, ir.ID)
		require.NoError(t, err)

		released, err := store.ReleaseInterclusterAddressRangeIfUnused(ctx, ar.ID)
		require.NoError(t, err)
		assert.True(t, released)
	})
}
