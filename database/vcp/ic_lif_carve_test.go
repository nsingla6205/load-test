package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

func TestCarveFreeCIDRBlock_findsFirstFreeBlock(t *testing.T) {
	cidr, parent, err := CarveFreeCIDRBlock([]string{"10.44.0.0/16"}, nil, 6)
	require.NoError(t, err)
	assert.Equal(t, "10.44.0.0/29", cidr)
	assert.Equal(t, "10.44.0.0/16", parent)
}

func TestCarveFreeCIDRBlock_skipsReserved(t *testing.T) {
	cidr, parent, err := CarveFreeCIDRBlock([]string{"10.44.0.0/16"}, []string{"10.44.0.0/29"}, 6)
	require.NoError(t, err)
	assert.Equal(t, "10.44.0.8/29", cidr)
	assert.Equal(t, "10.44.0.0/16", parent)
}

func TestCarveFreeCIDRBlock_exhaustedReturnsConflict(t *testing.T) {
	_, _, err := CarveFreeCIDRBlock([]string{"10.44.0.0/29"}, []string{"10.44.0.0/29"}, 6)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func TestClaimCarvedInterclusterIPRange_idempotentWhenSubnetHasRow(t *testing.T) {
	store := setupIPRangeStore(t)
	region, account, _ := seedIPRangeReferences(t, store)
	parent := seedAddressRange(t, store, "ar-ic-1", "123", "vpc1", AddressRangeLifTypeInterclusterLIF)
	parent.AddressRangeCidr = "10.45.0.0/16"
	require.NoError(t, store.db.GORM().Save(parent).Error)

	claim := InterclusterCarveClaim{
		HostProjectNumber: "123",
		VPCName:           "vpc1",
		RequiredIPs:       6,
		Parents:           []InterclusterParentSpec{{ID: parent.ID, CIDR: "10.45.0.0/16"}},
		Network:           "vsa-subnet",
		AccountUUID:       account.UUID,
		RegionUUID:        region.UUID,
	}
	first, err := store.ClaimCarvedInterclusterIPRange(context.Background(), claim)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, "10.45.0.0/29", first.CarvedCIDR)
	require.NotNil(t, first.IPRange)
	assert.Equal(t, "255.255.255.248", first.IPRange.Netmask)

	second, err := store.ClaimCarvedInterclusterIPRange(context.Background(), claim)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, first.IPRange.ID, second.IPRange.ID)
	assert.Equal(t, first.CarvedCIDR, second.CarvedCIDR)
	assert.True(t, first.Inserted)
	assert.False(t, second.Inserted)
}

func TestClaimCarvedInterclusterIPRange_secondSubnetTakesNextBlock(t *testing.T) {
	store := setupIPRangeStore(t)
	region, account, _ := seedIPRangeReferences(t, store)
	parent := seedAddressRange(t, store, "ar-ic-1b", "123", "vpc1", AddressRangeLifTypeInterclusterLIF)
	parent.AddressRangeCidr = "10.45.0.0/16"
	require.NoError(t, store.db.GORM().Save(parent).Error)

	first, err := store.ClaimCarvedInterclusterIPRange(context.Background(), InterclusterCarveClaim{
		HostProjectNumber: "123",
		VPCName:           "vpc1",
		RequiredIPs:       6,
		Parents:           []InterclusterParentSpec{{ID: parent.ID, CIDR: "10.45.0.0/16"}},
		Network:           "vsa-subnet-a",
		AccountUUID:       account.UUID,
		RegionUUID:        region.UUID,
	})
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, "10.45.0.0/29", first.CarvedCIDR)

	second, err := store.ClaimCarvedInterclusterIPRange(context.Background(), InterclusterCarveClaim{
		HostProjectNumber: "123",
		VPCName:           "vpc1",
		RequiredIPs:       6,
		Parents:           []InterclusterParentSpec{{ID: parent.ID, CIDR: "10.45.0.0/16"}},
		Network:           "vsa-subnet-b",
		AccountUUID:       account.UUID,
		RegionUUID:        region.UUID,
	})
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, "10.45.0.8/29", second.CarvedCIDR)
	assert.NotEqual(t, first.IPRange.ID, second.IPRange.ID)
}

func TestAdoptInterclusterIPRange_idempotentWhenRowExists(t *testing.T) {
	store := setupIPRangeStore(t)
	region, account, _ := seedIPRangeReferences(t, store)
	parent := seedAddressRange(t, store, "ar-adopt-1", "123", "vpc1", AddressRangeLifTypeInterclusterLIF)
	parent.AddressRangeCidr = "10.45.0.0/16"
	require.NoError(t, store.db.GORM().Save(parent).Error)

	network := "vsa-subnet"
	existing, err := store.AdoptInterclusterIPRange(context.Background(), InterclusterAdoptClaim{
		HostProjectNumber: "123",
		VPCName:           "vpc1",
		CarvedCIDR:        "10.45.0.16/29",
		Parents:           []InterclusterParentSpec{{ID: parent.ID, CIDR: "10.45.0.0/16"}},
		Network:           network,
		AccountUUID:       account.UUID,
		RegionUUID:        region.UUID,
	})
	require.NoError(t, err)
	require.NotNil(t, existing)
	assert.True(t, existing.Inserted)

	again, err := store.AdoptInterclusterIPRange(context.Background(), InterclusterAdoptClaim{
		HostProjectNumber: "123",
		VPCName:           "vpc1",
		CarvedCIDR:        "10.45.0.16/29",
		Parents:           []InterclusterParentSpec{{ID: parent.ID, CIDR: "10.45.0.0/16"}},
		Network:           network,
		AccountUUID:       account.UUID,
		RegionUUID:        region.UUID,
	})
	require.NoError(t, err)
	assert.Equal(t, existing.IPRange.ID, again.IPRange.ID)
	assert.False(t, again.Inserted)
}

func TestCarveFreeCIDRBlock_rejectsNonPositiveRequiredIPs(t *testing.T) {
	_, _, err := CarveFreeCIDRBlock([]string{"10.44.0.0/16"}, nil, 0)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
}

func TestCarveFreeCIDRBlock_skipsInvalidCandidateCIDR(t *testing.T) {
	cidr, parent, err := CarveFreeCIDRBlock([]string{"not-a-cidr", "10.44.0.0/16"}, nil, 6)
	require.NoError(t, err)
	assert.Equal(t, "10.44.0.0/29", cidr)
	assert.Equal(t, "10.44.0.0/16", parent)
}

func TestCidrFromStartEnd_errors(t *testing.T) {
	_, err := CidrFromStartEnd("bad", "10.0.0.7")
	require.Error(t, err)
	_, err = CidrFromStartEnd("10.0.0.10", "10.0.0.1")
	require.Error(t, err)
	_, err = CidrFromStartEnd("10.0.0.0", "10.0.0.2")
	require.Error(t, err)
}

func TestCidrToStartEndMask_errors(t *testing.T) {
	_, _, _, err := CidrToStartEndMask("not-a-cidr")
	require.Error(t, err)
}

func TestCidrToStartEndMask_dottedOctet(t *testing.T) {
	start, end, mask, err := CidrToStartEndMask("10.45.0.0/29")
	require.NoError(t, err)
	assert.Equal(t, "10.45.0.0", start)
	assert.Equal(t, "10.45.0.7", end)
	assert.Equal(t, "255.255.255.248", mask)

	_, _, mask28, err := CidrToStartEndMask("10.45.0.0/28")
	require.NoError(t, err)
	assert.Equal(t, "255.255.255.240", mask28)
}

func TestCidrContainsCIDR_invalidInputs(t *testing.T) {
	assert.False(t, CidrContainsCIDR("bad-parent", "10.0.0.0/29"))
	assert.False(t, CidrContainsCIDR("10.0.0.0/16", "bad-child"))
	assert.False(t, CidrContainsCIDR("10.0.0.0/16", "2001:db8::/32"))
}

func TestGcpSecondaryCarveBlockSize_minimumPrefix(t *testing.T) {
	block, prefix := GcpSecondaryCarveBlockSize(1)
	assert.Equal(t, 8, block)
	assert.Equal(t, 29, prefix)
}

func TestClaimCarvedInterclusterIPRange_validationErrors(t *testing.T) {
	store := setupIPRangeStore(t)
	region, account, _ := seedIPRangeReferences(t, store)
	parent := seedAddressRange(t, store, "ar-val-1", "123", "vpc1", AddressRangeLifTypeInterclusterLIF)

	_, err := store.ClaimCarvedInterclusterIPRange(context.Background(), InterclusterCarveClaim{
		AccountUUID: account.UUID,
		RegionUUID:  region.UUID,
		Parents:     []InterclusterParentSpec{{ID: parent.ID, CIDR: "10.45.0.0/16"}},
	})
	require.Error(t, err)

	_, err = store.ClaimCarvedInterclusterIPRange(context.Background(), InterclusterCarveClaim{
		Network:     "subnet-a",
		AccountUUID: account.UUID,
		RegionUUID:  region.UUID,
	})
	require.Error(t, err)
}

func TestAdoptInterclusterIPRange_validationErrors(t *testing.T) {
	store := setupIPRangeStore(t)
	region, account, _ := seedIPRangeReferences(t, store)
	parent := seedAddressRange(t, store, "ar-adopt-val", "123", "vpc1", AddressRangeLifTypeInterclusterLIF)

	_, err := store.AdoptInterclusterIPRange(context.Background(), InterclusterAdoptClaim{
		Network:     "subnet-a",
		AccountUUID: account.UUID,
		RegionUUID:  region.UUID,
		Parents:     []InterclusterParentSpec{{ID: parent.ID, CIDR: "10.45.0.0/16"}},
	})
	require.Error(t, err)

	_, err = store.AdoptInterclusterIPRange(context.Background(), InterclusterAdoptClaim{
		CarvedCIDR:  "10.99.0.0/29",
		Network:     "subnet-a",
		AccountUUID: account.UUID,
		RegionUUID:  region.UUID,
		Parents:     []InterclusterParentSpec{{ID: parent.ID, CIDR: "10.45.0.0/16"}},
	})
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func TestClaimCarvedInterclusterIPRange_extraReservedSkipsBlock(t *testing.T) {
	store := setupIPRangeStore(t)
	region, account, _ := seedIPRangeReferences(t, store)
	parent := seedAddressRange(t, store, "ar-ic-2", "123", "vpc1", AddressRangeLifTypeInterclusterLIF)
	parent.AddressRangeCidr = "10.45.0.0/16"
	require.NoError(t, store.db.GORM().Save(parent).Error)

	claimed, err := store.ClaimCarvedInterclusterIPRange(context.Background(), InterclusterCarveClaim{
		HostProjectNumber:  "123",
		VPCName:            "vpc1",
		RequiredIPs:        6,
		Parents:            []InterclusterParentSpec{{ID: parent.ID, CIDR: "10.45.0.0/16"}},
		Network:            "vsa-subnet-2",
		AccountUUID:        account.UUID,
		RegionUUID:         region.UUID,
		ExtraReservedCIDRs: []string{"10.45.0.0/29"},
	})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, "10.45.0.8/29", claimed.CarvedCIDR)
}
