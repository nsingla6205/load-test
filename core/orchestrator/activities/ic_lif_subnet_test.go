package activities

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vmrs_decision "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/decision"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscaler_models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
)

func Test_maxHAPairsOnSubnet_standard28(t *testing.T) {
	// 16 addresses less the 4 GCP reserves, over 6 IPs per HA pair.
	assert.Equal(t, 2, maxHAPairsOnSubnet(28))
}

func Test_maxHAPairsOnSubnet_large26(t *testing.T) {
	assert.Equal(t, 10, maxHAPairsOnSubnet(26))
}

func Test_maxHAPairsOnSubnet_invalidDivisor(t *testing.T) {
	orig := totalIPPerHAPair
	t.Cleanup(func() { totalIPPerHAPair = orig })

	totalIPPerHAPair = 0
	assert.Equal(t, 0, maxHAPairsOnSubnet(28))

	totalIPPerHAPair = -1
	assert.Equal(t, 0, maxHAPairsOnSubnet(28))
}

func Test_requiredSecondaryIPs(t *testing.T) {
	assert.Equal(t, 0, requiredSecondaryIPs(0))
	assert.Equal(t, 6, requiredSecondaryIPs(3))
}

func Test_gcpSecondaryCarveBlockSize(t *testing.T) {
	block, prefix := gcpSecondaryCarveBlockSize(6)
	assert.Equal(t, 8, block)
	assert.Equal(t, 29, prefix)
}

func Test_carveFreeCIDRBlock_findsFirstFreeBlock(t *testing.T) {
	cidr, parent, err := carveFreeCIDRBlock([]string{"10.44.0.0/16"}, nil, 6)
	require.NoError(t, err)
	assert.Equal(t, "10.44.0.0/29", cidr)
	assert.Equal(t, "10.44.0.0/16", parent)
}

func Test_carveFreeCIDRBlock_skipsReserved(t *testing.T) {
	cidr, parent, err := carveFreeCIDRBlock([]string{"10.44.0.0/16"}, []string{"10.44.0.0/29"}, 6)
	require.NoError(t, err)
	assert.Equal(t, "10.44.0.8/29", cidr)
	assert.Equal(t, "10.44.0.0/16", parent)
}

func Test_carveFreeCIDRBlock_usesSecondParentWhenFirstExhausted(t *testing.T) {
	// First parent is fully reserved as a /29 block; second parent still free.
	cidr, parent, err := carveFreeCIDRBlock(
		[]string{"10.44.0.0/29", "10.45.0.0/16"},
		[]string{"10.44.0.0/29"},
		6,
	)
	require.NoError(t, err)
	assert.Equal(t, "10.45.0.0/29", cidr)
	assert.Equal(t, "10.45.0.0/16", parent)
}

func Test_carveFreeCIDRBlock_exhaustedReturnsConflict(t *testing.T) {
	cidr, parent, err := carveFreeCIDRBlock(
		[]string{"10.44.0.0/29"},
		[]string{"10.44.0.0/29"},
		6,
	)
	require.Error(t, err)
	assert.Empty(t, cidr)
	assert.Empty(t, parent)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func Test_gcpSecondaryRangeName(t *testing.T) {
	t.Run("withoutPoolUUID", func(t *testing.T) {
		name := gcpSecondaryRangeName("10.44.0.0/29", "")
		assert.LessOrEqual(t, len(name), 63)
		assert.Equal(t, "ic-lif-10-44-0-0-29", name)
	})

	t.Run("differsPerPoolForTheSameCIDR", func(t *testing.T) {
		// Two pools racing for the same block derive the same CIDR. If they also derived the same
		// name, the loser's patch would be a no-op against the winner's range instead of a conflict.
		first := gcpSecondaryRangeName("10.44.0.0/29", "pool-a")
		second := gcpSecondaryRangeName("10.44.0.0/29", "pool-b")
		assert.NotEqual(t, first, second)
		assert.True(t, strings.HasPrefix(first, "ic-lif-10-44-0-0-29-"))
		assert.True(t, strings.HasPrefix(second, "ic-lif-10-44-0-0-29-"))
	})

	t.Run("stableForTheSamePool", func(t *testing.T) {
		// A Temporal retry must rebuild the same name or the patch stops being idempotent.
		assert.Equal(t,
			gcpSecondaryRangeName("10.44.0.0/29", "pool-a"),
			gcpSecondaryRangeName("10.44.0.0/29", "pool-a"))
	})

	t.Run("keepsDiscriminatorWhenTruncating", func(t *testing.T) {
		name := gcpSecondaryRangeName(strings.Repeat("10.44.0.0/29", 12), "pool-a")
		assert.LessOrEqual(t, len(name), 63)
		discriminator := poolNameDiscriminator("pool-a")
		assert.True(t, strings.HasSuffix(name, "-"+discriminator))
	})
}

func Test_parentIDForCarvedCIDR(t *testing.T) {
	parents := []interclusterParentRange{
		{ID: 11, CIDR: "10.44.0.0/16"},
		{ID: 22, CIDR: "10.45.0.0/16"},
	}
	assert.Equal(t, int64(22), parentIDForCarvedCIDR(parents, "10.45.0.0/16", "10.45.0.0/29"))
	assert.Equal(t, int64(11), parentIDForCarvedCIDR(parents, "", "10.44.0.8/29"))
	assert.Equal(t, int64(0), parentIDForCarvedCIDR(parents, "", "10.99.0.0/29"))
}

func Test_buildSecondaryIpRangeSpecsForICLif_multiParentAttribution(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return true }

	mockSE := database.NewMockStorage(t)
	lifType := database.AddressRangeLifTypeInterclusterLIF
	mockSE.On("ListAddressRanges", mock.Anything, "123", "vpc1", (*string)(nil), &lifType).
		Return([]*datamodel.AddressRange{
			{BaseModel: datamodel.BaseModel{ID: 11}, AddressRangeCidr: "10.44.0.0/29", AddressRangeState: database.AddressRangeStateCreated},
			{BaseModel: datamodel.BaseModel{ID: 22}, AddressRangeCidr: "10.45.0.0/16", AddressRangeState: database.AddressRangeStateCreated},
		}, nil)
	mockSE.On("ListInterclusterIPRangesForVPC", mock.Anything, "123", "vpc1").
		Return([]*datamodel.IPRange{
			{StartIP: "10.44.0.0", EndIP: "10.44.0.7"}, // reserved whole first parent
		}, nil)

	params := commonparams.CreatePoolParams{
		VendorSubNetID:   "projects/123/global/networks/vpc1",
		SecondaryIpRange: []string{"10.44.0.0/29", "10.45.0.0/16"},
		Region:           "us-central1",
	}
	plan, err := buildSecondaryIpRangeSpecsForICLif(context.Background(), mockSE, params)
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Equal(t, "10.45.0.0/29", plan.carvedCIDR)
	assert.Equal(t, int64(22), plan.parentInterclusterRangeID)
}

func Test_buildSecondaryIpRangeSpecsForICLif_exhausted(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return true }

	mockSE := database.NewMockStorage(t)
	lifType := database.AddressRangeLifTypeInterclusterLIF
	mockSE.On("ListAddressRanges", mock.Anything, "123", "vpc1", (*string)(nil), &lifType).
		Return([]*datamodel.AddressRange{
			{BaseModel: datamodel.BaseModel{ID: 11}, AddressRangeCidr: "10.44.0.0/29", AddressRangeState: database.AddressRangeStateCreated},
		}, nil)
	mockSE.On("ListInterclusterIPRangesForVPC", mock.Anything, "123", "vpc1").
		Return([]*datamodel.IPRange{
			{StartIP: "10.44.0.0", EndIP: "10.44.0.7"},
		}, nil)

	params := commonparams.CreatePoolParams{
		VendorSubNetID:   "projects/123/global/networks/vpc1",
		SecondaryIpRange: []string{"10.44.0.0/29"},
		Region:           "us-central1",
	}
	plan, err := buildSecondaryIpRangeSpecsForICLif(context.Background(), mockSE, params)
	require.Error(t, err)
	assert.Nil(t, plan)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func Test_CheckInterclusterCarveAvailability_disabled(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return false }

	// No storage calls are expected: the check must not touch the database when the feature is off.
	pa := &PoolActivity{SE: database.NewMockStorage(t)}
	params := commonparams.CreatePoolParams{SecondaryIpRange: []string{"10.45.0.0/16"}}
	require.NoError(t, pa.CheckInterclusterCarveAvailability(context.Background(), params))
}

func Test_CheckInterclusterCarveAvailability_noParentRange(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return true }

	pa := &PoolActivity{SE: database.NewMockStorage(t)}
	require.NoError(t, pa.CheckInterclusterCarveAvailability(context.Background(), commonparams.CreatePoolParams{}))
}

func Test_CheckInterclusterCarveAvailability_exhausted(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return true }

	mockSE := database.NewMockStorage(t)
	lifType := database.AddressRangeLifTypeInterclusterLIF
	mockSE.On("ListAddressRanges", mock.Anything, "123", "vpc1", (*string)(nil), &lifType).
		Return([]*datamodel.AddressRange{
			{BaseModel: datamodel.BaseModel{ID: 11}, AddressRangeCidr: "10.44.0.0/29", AddressRangeState: database.AddressRangeStateCreated},
		}, nil)
	mockSE.On("ListInterclusterIPRangesForVPC", mock.Anything, "123", "vpc1").
		Return([]*datamodel.IPRange{
			{StartIP: "10.44.0.0", EndIP: "10.44.0.7"},
		}, nil)

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{
		VendorSubNetID:   "projects/123/global/networks/vpc1",
		SecondaryIpRange: []string{"10.44.0.0/29"},
		Region:           "us-central1",
	}
	err := pa.CheckInterclusterCarveAvailability(context.Background(), params)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
	mockSE.AssertExpectations(t)
}

func Test_CheckInterclusterCarveAvailability_freeBlockAvailable(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return true }

	mockSE := database.NewMockStorage(t)
	lifType := database.AddressRangeLifTypeInterclusterLIF
	mockSE.On("ListAddressRanges", mock.Anything, "123", "vpc1", (*string)(nil), &lifType).
		Return([]*datamodel.AddressRange{
			{BaseModel: datamodel.BaseModel{ID: 11}, AddressRangeCidr: "10.44.0.0/24", AddressRangeState: database.AddressRangeStateCreated},
		}, nil)
	mockSE.On("ListInterclusterIPRangesForVPC", mock.Anything, "123", "vpc1").
		Return([]*datamodel.IPRange{}, nil)

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{
		VendorSubNetID:   "projects/123/global/networks/vpc1",
		SecondaryIpRange: []string{"10.44.0.0/24"},
		Region:           "us-central1",
	}
	require.NoError(t, pa.CheckInterclusterCarveAvailability(context.Background(), params))
	mockSE.AssertExpectations(t)
}

func Test_EnsureInterclusterSecondaryRangeOp_disabled(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return false }

	pa := &PoolActivity{SE: database.NewMockStorage(t)}
	params := commonparams.CreatePoolParams{SecondaryIpRange: []string{"10.45.0.0/16"}}
	result, err := pa.EnsureInterclusterSecondaryRangeOp(context.Background(), params, "pool-uuid", "host-project", "vsa-subnet")
	require.NoError(t, err)
	assert.Equal(t, &commonparams.EnsureICSecondaryRangeResult{}, result)
}

func expectGetSubnetworkEmpty(t *testing.T, mockSvc *hyperscaler2.MockServices, hostProject, region, subnetName string) {
	t.Helper()
	mockSvc.On("GetSubnetwork", hostProject, region, subnetName).
		Return(&hyperscaler_models.Subnet{}, nil)
}

func Test_EnsureInterclusterSecondaryRangeOp_skipsWhenSubnetAlreadyCarved(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	origResolve := resolveCloudServiceForICLif
	t.Cleanup(func() {
		icLifAddressSpaceEnabled = origEnabled
		resolveCloudServiceForICLif = origResolve
	})
	icLifAddressSpaceEnabled = func() bool { return true }

	subnetName := "vsa-subnet"
	accountUUID := "acct-uuid"
	existing := &datamodel.IPRange{
		BaseModel: datamodel.BaseModel{ID: 55},
		LifType:   database.LifTypeInterclusterLIF,
		StartIP:   "10.45.0.0",
		EndIP:     "10.45.0.7",
	}
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAccount", mock.Anything, "acct").
		Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: accountUUID}}, nil)
	mockSE.On("GetIPRanges", mock.Anything, &accountUUID, &subnetName).
		Return([]*datamodel.IPRange{existing}, nil)

	mockSvc := hyperscaler2.NewMockServices(t)
	expectGetSubnetworkEmpty(t, mockSvc, "host-project", "us-central1", subnetName)
	mockSvc.On("AddSubnetSecondaryRangeOp", "host-project", "us-central1", subnetName, mock.Anything, "10.45.0.0/29").
		Return("", nil)
	resolveCloudServiceForICLif = func(context.Context) (hyperscaler2.Services, error) {
		return mockSvc, nil
	}

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{
		AccountName:      "acct",
		Region:           "us-central1",
		SecondaryIpRange: []string{"10.45.0.0/16"},
	}
	result, err := pa.EnsureInterclusterSecondaryRangeOp(context.Background(), params, "pool-uuid", "host-project", subnetName)
	require.NoError(t, err)
	assert.Equal(t, int64(55), result.IPRangeID)
	assert.Equal(t, "10.45.0.0/29", result.CarvedSecondaryCIDR)
	assert.False(t, result.ReleaseClaimOnPatchFailure)
	mockSE.AssertNotCalled(t, "ClaimCarvedInterclusterIPRange", mock.Anything, mock.Anything)
}

func Test_EnsureInterclusterSecondaryRangeOp_carvesAndPatches(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	origResolve := resolveCloudServiceForICLif
	t.Cleanup(func() {
		icLifAddressSpaceEnabled = origEnabled
		resolveCloudServiceForICLif = origResolve
	})
	icLifAddressSpaceEnabled = func() bool { return true }

	subnetName := "vsa-subnet"
	accountUUID := "acct-uuid"
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAccount", mock.Anything, "acct").
		Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: accountUUID}}, nil)
	mockSE.On("GetIPRanges", mock.Anything, &accountUUID, &subnetName).Return(nil, nil)
	lifType := database.AddressRangeLifTypeInterclusterLIF
	mockSE.On("ListAddressRanges", mock.Anything, "123", "vpc1", (*string)(nil), &lifType).
		Return([]*datamodel.AddressRange{
			{BaseModel: datamodel.BaseModel{ID: 22}, AddressRangeCidr: "10.45.0.0/16", AddressRangeState: database.AddressRangeStateCreated},
		}, nil)
	mockSE.On("GetRegionByName", mock.Anything, "us-central1").
		Return(&datamodel.Region{BaseModel: datamodel.BaseModel{UUID: "region-uuid"}}, nil)
	mockSE.On("ClaimCarvedInterclusterIPRange", mock.Anything, mock.Anything).
		Return(&database.InterclusterCarveResult{
			IPRange:    &datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 77}},
			CarvedCIDR: "10.45.0.0/29",
			ParentID:   22,
			Inserted:   true,
		}, nil)

	mockSvc := hyperscaler2.NewMockServices(t)
	expectGetSubnetworkEmpty(t, mockSvc, "host-project", "us-central1", subnetName)
	// The requested name carries the pool discriminator, so match on the CIDR-derived stem rather
	// than restating the hash here; gcpSecondaryRangeName has its own test.
	rangeNameForPool := mock.MatchedBy(func(name string) bool {
		return strings.HasPrefix(name, "ic-lif-10-45-0-0-29-") && len(name) <= 63
	})
	mockSvc.On("AddSubnetSecondaryRangeOp", "host-project", "us-central1", subnetName, rangeNameForPool, "10.45.0.0/29").
		Return("patch-op-1", nil)
	resolveCloudServiceForICLif = func(context.Context) (hyperscaler2.Services, error) {
		return mockSvc, nil
	}

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{
		AccountName:      "acct",
		Region:           "us-central1",
		VendorSubNetID:   "projects/123/global/networks/vpc1",
		SecondaryIpRange: []string{"10.45.0.0/16"},
	}
	result, err := pa.EnsureInterclusterSecondaryRangeOp(context.Background(), params, "pool-uuid", "host-project", subnetName)
	require.NoError(t, err)
	assert.Equal(t, "patch-op-1", result.OperationName)
	assert.Equal(t, "host-project", result.Project)
	assert.Equal(t, "10.45.0.0/29", result.CarvedSecondaryCIDR)
	assert.Equal(t, int64(22), result.ParentInterclusterRangeID)
	assert.Equal(t, int64(77), result.IPRangeID)
	assert.True(t, result.ReleaseClaimOnPatchFailure)
	mockSvc.AssertExpectations(t)
}

func Test_EnsureInterclusterSecondaryRangeOp_failsWithoutSnHostProject(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return true }

	subnetName := "vsa-subnet"
	accountUUID := "acct-uuid"
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAccount", mock.Anything, "acct").
		Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: accountUUID}}, nil)
	mockSE.On("GetIPRanges", mock.Anything, &accountUUID, &subnetName).Return(nil, nil)

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{
		AccountName:      "acct",
		Region:           "us-central1",
		VendorSubNetID:   "projects/123/global/networks/vpc1",
		SecondaryIpRange: []string{"10.45.0.0/16"},
	}
	_, err := pa.EnsureInterclusterSecondaryRangeOp(context.Background(), params, "pool-uuid", "", subnetName)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
}

func persistICLifRangeMocks(t *testing.T) *database.MockStorage {
	t.Helper()
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetRegionByName", mock.Anything, "us-central1").
		Return(&datamodel.Region{BaseModel: datamodel.BaseModel{UUID: "region-uuid"}}, nil)
	mockSE.On("GetAccount", mock.Anything, "acct").
		Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-uuid"}}, nil)
	mockSE.On("GetIPRanges", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	mockSE.On("CreateIPRange", mock.Anything, mock.Anything).
		Return(&datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 77}}, nil)
	return mockSE
}

func Test_PersistInterclusterIPRange_marksParentInUse(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return true }

	mockSE := persistICLifRangeMocks(t)
	mockSE.On("MarkAddressRangeInUseByID", mock.Anything, int64(11)).Return(true, nil)

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{Region: "us-central1", AccountName: "acct"}
	id, err := pa.PersistInterclusterIPRange(context.Background(), params, "pool-uuid", "vsa-subnet", "10.44.0.0/29", 11)
	require.NoError(t, err)
	assert.Equal(t, int64(77), id)
	mockSE.AssertExpectations(t)
}

func Test_PersistInterclusterIPRange_markFailureDoesNotFailCarve(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return true }

	mockSE := persistICLifRangeMocks(t)
	mockSE.On("MarkAddressRangeInUseByID", mock.Anything, int64(11)).
		Return(false, errors.New("db unavailable"))

	// The carved ip_ranges row is what actually blocks deletion, so a failed state flip
	// must not abort pool creation.
	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{Region: "us-central1", AccountName: "acct"}
	id, err := pa.PersistInterclusterIPRange(context.Background(), params, "pool-uuid", "vsa-subnet", "10.44.0.0/29", 11)
	require.NoError(t, err)
	assert.Equal(t, int64(77), id)
	mockSE.AssertExpectations(t)
}

func Test_resolveLargeCapacityHAPairs_honoursExperimentalAllowlist(t *testing.T) {
	// The carve used to take this count from a VMRS decision run with an empty VLM config, so
	// Deployment.Labels["account_id"] was never set and an allowlisted account was carved for the
	// default count while its cluster was deployed with the larger one.
	origAllowlist := os.Getenv("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS")
	origPairs := os.Getenv("EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY")
	t.Cleanup(func() {
		utils.SetExperimentalCustomHaLargeVolumeAllowlistedAccountsForTesting(origAllowlist)
		if origPairs == "" {
			_ = os.Unsetenv("EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY")
			return
		}
		_ = os.Setenv("EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY", origPairs)
	})

	utils.SetExperimentalCustomHaLargeVolumeAllowlistedAccountsForTesting("allowlisted-acct")
	require.NoError(t, os.Setenv("EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY", "12"))

	assert.Equal(t, 12, resolveLargeCapacityHAPairs(commonparams.CreatePoolParams{AccountName: "allowlisted-acct"}))
	assert.Equal(t, vmrs_decision.LVHaPair, resolveLargeCapacityHAPairs(commonparams.CreatePoolParams{AccountName: "other-acct"}))
	assert.Equal(t, vmrs_decision.LVHaPair, resolveLargeCapacityHAPairs(commonparams.CreatePoolParams{}))
}

func Test_standardDataSubnetPrefix(t *testing.T) {
	assert.Equal(t, 28, standardDataSubnetPrefix())

	t.Setenv(dataSubnetCidrBlockEnv, "26")
	assert.Equal(t, 26, standardDataSubnetPrefix())

	t.Setenv(dataSubnetCidrBlockEnv, "99")
	assert.Equal(t, defaultDataSubnetPrefix, standardDataSubnetPrefix())
}

func Test_resolveHAPairsForSecondaryCarve(t *testing.T) {
	// A large-capacity pool owns its data subnet, so the carve follows its configured HA pair
	// count. Standard pools share a fixed-size subnet, so the carve covers everything it can hold.
	assert.Equal(t, vmrs_decision.LVHaPair,
		resolveHAPairsForSecondaryCarve(commonparams.CreatePoolParams{LargeCapacity: true}))
	assert.Equal(t, maxHAPairsOnSubnet(standardDataSubnetPrefix()),
		resolveHAPairsForSecondaryCarve(commonparams.CreatePoolParams{LargeCapacity: false}))
}

func Test_resolveHAPairsForSecondaryCarve_respectsDataSubnetCidrBlockEnv(t *testing.T) {
	t.Setenv(dataSubnetCidrBlockEnv, "26")
	assert.Equal(t, maxHAPairsOnSubnet(26),
		resolveHAPairsForSecondaryCarve(commonparams.CreatePoolParams{LargeCapacity: false}))
}

func Test_carveAndPersist_concurrent_noDuplicateCIDR(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return true }

	const concurrentPools = 16
	// CreateIPRange's overlap check is a read followed by a write in one transaction, so every
	// caller can carve the same block before any of them inserts. This stands in for the partial
	// unique index that settles it: the first claim on a block wins and the rest are rejected.
	var mu sync.Mutex
	claimed := map[string]bool{}

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetRegionByName", mock.Anything, "us-central1").
		Return(&datamodel.Region{BaseModel: datamodel.BaseModel{UUID: "region-uuid"}}, nil)
	mockSE.On("GetAccount", mock.Anything, "acct").
		Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-uuid"}}, nil)
	mockSE.On("GetIPRanges", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	mockSE.On("CreateIPRange", mock.Anything, mock.Anything).
		Return(func(_ context.Context, ir *datamodel.IPRange) (*datamodel.IPRange, error) {
			mu.Lock()
			defer mu.Unlock()
			block := ir.StartIP + "-" + ir.EndIP
			if claimed[block] {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
					errors.New("IP range already exists (concurrent duplicate detected)"))
			}
			claimed[block] = true
			return &datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: int64(len(claimed))}}, nil
		})

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{Region: "us-central1", AccountName: "acct"}

	var wg sync.WaitGroup
	errs := make([]error, concurrentPools)
	ids := make([]int64, concurrentPools)
	start := make(chan struct{})
	for i := 0; i < concurrentPools; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			// Every pool derives the same CIDR, which is exactly the race being guarded.
			ids[idx], errs[idx] = pa.PersistInterclusterIPRange(
				context.Background(), params, fmt.Sprintf("pool-%d", idx), "vsa-subnet", "10.44.0.0/29", 0)
		}(i)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for i := 0; i < concurrentPools; i++ {
		if errs[i] == nil {
			succeeded++
			assert.NotZero(t, ids[i])
			continue
		}
		// Losers must fail closed rather than quietly claim the winner's block.
		assert.Zero(t, ids[i])
	}
	assert.Equal(t, 1, succeeded, "exactly one pool may claim a given block")
	assert.Len(t, claimed, 1)
}

func Test_PersistInterclusterIPRange_noParentSkipsMark(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origEnabled })
	icLifAddressSpaceEnabled = func() bool { return true }

	mockSE := persistICLifRangeMocks(t)

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{Region: "us-central1", AccountName: "acct"}
	_, err := pa.PersistInterclusterIPRange(context.Background(), params, "pool-uuid", "vsa-subnet", "10.44.0.0/29", 0)
	require.NoError(t, err)
	mockSE.AssertNotCalled(t, "MarkAddressRangeInUseByID", mock.Anything, mock.Anything)
}

func Test_icLifDataSubnetStillExists(t *testing.T) {
	origRegion := Region
	origResolve := resolveCloudServiceForICLif
	t.Cleanup(func() {
		Region = origRegion
		resolveCloudServiceForICLif = origResolve
	})
	Region = "us-central1"

	t.Run("missingHostProjectAssumesStillExists", func(t *testing.T) {
		stillExists, err := _icLifDataSubnetStillExists(context.Background(), "", "vsa-subnet")
		require.NoError(t, err)
		assert.True(t, stillExists)
	})

	t.Run("subnetFound", func(t *testing.T) {
		mockSvc := hyperscaler2.NewMockServices(t)
		mockSvc.On("GetSubnetwork", "host-project", Region, "vsa-subnet").
			Return(&hyperscaler_models.Subnet{Name: "vsa-subnet"}, nil)
		resolveCloudServiceForICLif = func(context.Context) (hyperscaler2.Services, error) {
			return mockSvc, nil
		}

		stillExists, err := _icLifDataSubnetStillExists(context.Background(), "host-project", "vsa-subnet")
		require.NoError(t, err)
		assert.True(t, stillExists)
	})

	t.Run("subnetNotFound", func(t *testing.T) {
		mockSvc := hyperscaler2.NewMockServices(t)
		mockSvc.On("GetSubnetwork", "host-project", Region, "vsa-subnet").
			Return(nil, errors.New("googleapi: Error 404: notFound"))
		resolveCloudServiceForICLif = func(context.Context) (hyperscaler2.Services, error) {
			return mockSvc, nil
		}

		stillExists, err := _icLifDataSubnetStillExists(context.Background(), "host-project", "vsa-subnet")
		require.NoError(t, err)
		assert.False(t, stillExists)
	})
}

func Test_isSecondaryRangePatchConflict(t *testing.T) {
	assert.False(t, isSecondaryRangePatchConflict(nil))
	assert.False(t, isSecondaryRangePatchConflict(errors.New("transient")))

	conflict := vsaerrors.WrapAsNonRetryableTemporalApplicationError(
		vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, errors.New("ip range overlaps")))
	assert.True(t, isSecondaryRangePatchConflict(conflict))
}

func Test_EnsureInterclusterSecondaryRangeOp_releasesClaimOnGCPConflictThenRecarves(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	origResolve := resolveCloudServiceForICLif
	t.Cleanup(func() {
		icLifAddressSpaceEnabled = origEnabled
		resolveCloudServiceForICLif = origResolve
	})
	icLifAddressSpaceEnabled = func() bool { return true }

	subnetName := "vsa-subnet"
	accountUUID := "acct-uuid"
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAccount", mock.Anything, "acct").
		Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: accountUUID}}, nil)
	mockSE.On("GetIPRanges", mock.Anything, &accountUUID, &subnetName).Return(nil, nil)
	lifType := database.AddressRangeLifTypeInterclusterLIF
	mockSE.On("ListAddressRanges", mock.Anything, "123", "vpc1", (*string)(nil), &lifType).
		Return([]*datamodel.AddressRange{
			{BaseModel: datamodel.BaseModel{ID: 22}, AddressRangeCidr: "10.45.0.0/16", AddressRangeState: database.AddressRangeStateCreated},
		}, nil)
	mockSE.On("GetRegionByName", mock.Anything, "us-central1").
		Return(&datamodel.Region{BaseModel: datamodel.BaseModel{UUID: "region-uuid"}}, nil)
	mockSE.On("ClaimCarvedInterclusterIPRange", mock.Anything, mock.MatchedBy(func(c database.InterclusterCarveClaim) bool {
		return len(c.ExtraReservedCIDRs) == 0
	})).
		Return(&database.InterclusterCarveResult{
			IPRange:    &datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 77}},
			CarvedCIDR: "10.45.0.0/29",
			ParentID:   22,
			Inserted:   true,
		}, nil).Once()
	mockSE.On("DeleteIPRangeByID", mock.Anything, int64(77)).
		Return(&datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 77}}, nil).Once()
	mockSE.On("ClaimCarvedInterclusterIPRange", mock.Anything, mock.MatchedBy(func(c database.InterclusterCarveClaim) bool {
		return len(c.ExtraReservedCIDRs) == 1 && c.ExtraReservedCIDRs[0] == "10.45.0.0/29"
	})).
		Return(&database.InterclusterCarveResult{
			IPRange:    &datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 78}},
			CarvedCIDR: "10.45.0.8/29",
			ParentID:   22,
			Inserted:   true,
		}, nil).Once()

	conflictErr := vsaerrors.WrapAsNonRetryableTemporalApplicationError(
		vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, errors.New("secondary range conflict")))

	mockSvc := hyperscaler2.NewMockServices(t)
	expectGetSubnetworkEmpty(t, mockSvc, "host-project", "us-central1", subnetName)
	mockSvc.On("AddSubnetSecondaryRangeOp", "host-project", "us-central1", subnetName, mock.Anything, "10.45.0.0/29").
		Return("", conflictErr).Once()
	mockSvc.On("AddSubnetSecondaryRangeOp", "host-project", "us-central1", subnetName, mock.Anything, "10.45.0.8/29").
		Return("patch-op-2", nil).Once()
	resolveCloudServiceForICLif = func(context.Context) (hyperscaler2.Services, error) {
		return mockSvc, nil
	}

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{
		AccountName:      "acct",
		Region:           "us-central1",
		VendorSubNetID:   "projects/123/global/networks/vpc1",
		SecondaryIpRange: []string{"10.45.0.0/16"},
	}
	result, err := pa.EnsureInterclusterSecondaryRangeOp(context.Background(), params, "pool-uuid", "host-project", subnetName)
	require.NoError(t, err)
	assert.Equal(t, "patch-op-2", result.OperationName)
	assert.Equal(t, "10.45.0.8/29", result.CarvedSecondaryCIDR)
	assert.Equal(t, int64(78), result.IPRangeID)
	assert.True(t, result.ReleaseClaimOnPatchFailure)
	mockSvc.AssertExpectations(t)
	mockSE.AssertExpectations(t)
}

func Test_EnsureInterclusterSecondaryRangeOp_releasesClaimOnNonConflictPatchFailure(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	origResolve := resolveCloudServiceForICLif
	t.Cleanup(func() {
		icLifAddressSpaceEnabled = origEnabled
		resolveCloudServiceForICLif = origResolve
	})
	icLifAddressSpaceEnabled = func() bool { return true }

	subnetName := "vsa-subnet"
	accountUUID := "acct-uuid"
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAccount", mock.Anything, "acct").
		Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: accountUUID}}, nil)
	mockSE.On("GetIPRanges", mock.Anything, &accountUUID, &subnetName).Return(nil, nil)
	lifType := database.AddressRangeLifTypeInterclusterLIF
	mockSE.On("ListAddressRanges", mock.Anything, "123", "vpc1", (*string)(nil), &lifType).
		Return([]*datamodel.AddressRange{
			{BaseModel: datamodel.BaseModel{ID: 22}, AddressRangeCidr: "10.45.0.0/16", AddressRangeState: database.AddressRangeStateCreated},
		}, nil)
	mockSE.On("GetRegionByName", mock.Anything, "us-central1").
		Return(&datamodel.Region{BaseModel: datamodel.BaseModel{UUID: "region-uuid"}}, nil)
	mockSE.On("ClaimCarvedInterclusterIPRange", mock.Anything, mock.Anything).
		Return(&database.InterclusterCarveResult{
			IPRange:    &datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 77}},
			CarvedCIDR: "10.45.0.0/29",
			ParentID:   22,
			Inserted:   true,
		}, nil)
	mockSE.On("DeleteIPRangeByID", mock.Anything, int64(77)).
		Return(&datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 77}}, nil)

	patchErr := vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, errors.New("stale fingerprint"))
	mockSvc := hyperscaler2.NewMockServices(t)
	expectGetSubnetworkEmpty(t, mockSvc, "host-project", "us-central1", subnetName)
	mockSvc.On("AddSubnetSecondaryRangeOp", "host-project", "us-central1", subnetName, mock.Anything, "10.45.0.0/29").
		Return("", patchErr)
	resolveCloudServiceForICLif = func(context.Context) (hyperscaler2.Services, error) {
		return mockSvc, nil
	}

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{
		AccountName:      "acct",
		Region:           "us-central1",
		VendorSubNetID:   "projects/123/global/networks/vpc1",
		SecondaryIpRange: []string{"10.45.0.0/16"},
	}
	_, err := pa.EnsureInterclusterSecondaryRangeOp(context.Background(), params, "pool-uuid", "host-project", subnetName)
	require.Error(t, err)
	mockSE.AssertCalled(t, "DeleteIPRangeByID", mock.Anything, int64(77))
}

func Test_EnsureInterclusterSecondaryRangeOp_doesNotReleaseReusedClaimOnPatchConflict(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	origResolve := resolveCloudServiceForICLif
	t.Cleanup(func() {
		icLifAddressSpaceEnabled = origEnabled
		resolveCloudServiceForICLif = origResolve
	})
	icLifAddressSpaceEnabled = func() bool { return true }

	subnetName := "vsa-subnet"
	accountUUID := "acct-uuid"
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAccount", mock.Anything, "acct").
		Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: accountUUID}}, nil)
	mockSE.On("GetIPRanges", mock.Anything, &accountUUID, &subnetName).Return(nil, nil)
	lifType := database.AddressRangeLifTypeInterclusterLIF
	mockSE.On("ListAddressRanges", mock.Anything, "123", "vpc1", (*string)(nil), &lifType).
		Return([]*datamodel.AddressRange{
			{BaseModel: datamodel.BaseModel{ID: 22}, AddressRangeCidr: "10.45.0.0/16", AddressRangeState: database.AddressRangeStateCreated},
		}, nil)
	mockSE.On("GetRegionByName", mock.Anything, "us-central1").
		Return(&datamodel.Region{BaseModel: datamodel.BaseModel{UUID: "region-uuid"}}, nil)
	mockSE.On("ClaimCarvedInterclusterIPRange", mock.Anything, mock.Anything).
		Return(&database.InterclusterCarveResult{
			IPRange:    &datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 77}},
			CarvedCIDR: "10.45.0.0/29",
			ParentID:   22,
			Inserted:   false,
		}, nil)

	conflictErr := vsaerrors.WrapAsNonRetryableTemporalApplicationError(
		vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, errors.New("secondary range conflict")))
	mockSvc := hyperscaler2.NewMockServices(t)
	expectGetSubnetworkEmpty(t, mockSvc, "host-project", "us-central1", subnetName)
	mockSvc.On("AddSubnetSecondaryRangeOp", "host-project", "us-central1", subnetName, mock.Anything, "10.45.0.0/29").
		Return("", conflictErr)
	resolveCloudServiceForICLif = func(context.Context) (hyperscaler2.Services, error) {
		return mockSvc, nil
	}

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{
		AccountName:      "acct",
		Region:           "us-central1",
		VendorSubNetID:   "projects/123/global/networks/vpc1",
		SecondaryIpRange: []string{"10.45.0.0/16"},
	}
	_, err := pa.EnsureInterclusterSecondaryRangeOp(context.Background(), params, "pool-uuid", "host-project", subnetName)
	require.Error(t, err)
	mockSE.AssertNotCalled(t, "DeleteIPRangeByID", mock.Anything, mock.Anything)
	mockSE.AssertNotCalled(t, "ClaimCarvedInterclusterIPRange", mock.Anything, mock.MatchedBy(func(c database.InterclusterCarveClaim) bool {
		return len(c.ExtraReservedCIDRs) > 0
	}))
	mockSE.AssertNotCalled(t, "AdoptInterclusterIPRange", mock.Anything, mock.Anything)
}

func Test_findManagedICLifSecondary(t *testing.T) {
	cidr, name, err := findManagedICLifSecondary(
		[]hyperscaler_models.SecondaryIpRange{{RangeName: "ic-lif-10-66-66-16-29-old", IpCidrRange: "10.66.66.16/29"}},
		[]string{"10.66.66.0/24"},
	)
	require.NoError(t, err)
	assert.Equal(t, "10.66.66.16/29", cidr)
	assert.Equal(t, "ic-lif-10-66-66-16-29-old", name)
}

func Test_isSameCIDRSecondaryRangeConflict(t *testing.T) {
	conflict := vsaerrors.WrapAsNonRetryableTemporalApplicationError(
		vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("secondary range ic-lif-a (10.66.66.16/29) conflicts with existing range ic-lif-b (10.66.66.16/29) on subnet vsa-subnet")))
	assert.True(t, isSameCIDRSecondaryRangeConflict(conflict))
	assert.False(t, isSameCIDRSecondaryRangeConflict(errors.New("other")))
}

func Test_EnsureInterclusterSecondaryRangeOp_adoptsGCPRangeWhenDBEmpty(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	origResolve := resolveCloudServiceForICLif
	t.Cleanup(func() {
		icLifAddressSpaceEnabled = origEnabled
		resolveCloudServiceForICLif = origResolve
	})
	icLifAddressSpaceEnabled = func() bool { return true }

	subnetName := "vsa-subnet"
	accountUUID := "acct-uuid"
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAccount", mock.Anything, "acct").
		Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: accountUUID}}, nil)
	mockSE.On("GetIPRanges", mock.Anything, &accountUUID, &subnetName).Return(nil, nil)
	lifType := database.AddressRangeLifTypeInterclusterLIF
	mockSE.On("ListAddressRanges", mock.Anything, "123", "vpc1", (*string)(nil), &lifType).
		Return([]*datamodel.AddressRange{
			{BaseModel: datamodel.BaseModel{ID: 22}, AddressRangeCidr: "10.66.66.0/24", AddressRangeState: database.AddressRangeStateCreated},
		}, nil)
	mockSE.On("GetRegionByName", mock.Anything, "us-central1").
		Return(&datamodel.Region{BaseModel: datamodel.BaseModel{UUID: "region-uuid"}}, nil)
	mockSE.On("AdoptInterclusterIPRange", mock.Anything, mock.Anything).
		Return(&database.InterclusterCarveResult{
			IPRange:    &datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 88}},
			CarvedCIDR: "10.66.66.16/29",
			ParentID:   22,
		}, nil)

	mockSvc := hyperscaler2.NewMockServices(t)
	mockSvc.On("GetSubnetwork", "host-project", "us-central1", subnetName).
		Return(&hyperscaler_models.Subnet{
			SecondaryIpRanges: []hyperscaler_models.SecondaryIpRange{
				{RangeName: "ic-lif-10-66-66-16-29-old", IpCidrRange: "10.66.66.16/29"},
			},
		}, nil)
	resolveCloudServiceForICLif = func(context.Context) (hyperscaler2.Services, error) {
		return mockSvc, nil
	}

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{
		AccountName:      "acct",
		Region:           "us-central1",
		VendorSubNetID:   "projects/123/global/networks/vpc1",
		SecondaryIpRange: []string{"10.66.66.0/24"},
	}
	result, err := pa.EnsureInterclusterSecondaryRangeOp(context.Background(), params, "pool-uuid", "host-project", subnetName)
	require.NoError(t, err)
	assert.Equal(t, int64(88), result.IPRangeID)
	assert.Equal(t, "10.66.66.16/29", result.CarvedSecondaryCIDR)
	assert.Empty(t, result.OperationName)
	mockSE.AssertNotCalled(t, "ClaimCarvedInterclusterIPRange", mock.Anything, mock.Anything)
	mockSvc.AssertNotCalled(t, "AddSubnetSecondaryRangeOp", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func Test_EnsureInterclusterSecondaryRangeOp_sameCIDRConflictAdopts(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	origResolve := resolveCloudServiceForICLif
	t.Cleanup(func() {
		icLifAddressSpaceEnabled = origEnabled
		resolveCloudServiceForICLif = origResolve
	})
	icLifAddressSpaceEnabled = func() bool { return true }

	subnetName := "vsa-subnet"
	accountUUID := "acct-uuid"
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAccount", mock.Anything, "acct").
		Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: accountUUID}}, nil)
	mockSE.On("GetIPRanges", mock.Anything, &accountUUID, &subnetName).Return(nil, nil)
	lifType := database.AddressRangeLifTypeInterclusterLIF
	mockSE.On("ListAddressRanges", mock.Anything, "123", "vpc1", (*string)(nil), &lifType).
		Return([]*datamodel.AddressRange{
			{BaseModel: datamodel.BaseModel{ID: 22}, AddressRangeCidr: "10.66.66.0/24", AddressRangeState: database.AddressRangeStateCreated},
		}, nil)
	mockSE.On("GetRegionByName", mock.Anything, "us-central1").
		Return(&datamodel.Region{BaseModel: datamodel.BaseModel{UUID: "region-uuid"}}, nil)
	mockSE.On("ClaimCarvedInterclusterIPRange", mock.Anything, mock.Anything).
		Return(&database.InterclusterCarveResult{
			IPRange:    &datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 77}},
			CarvedCIDR: "10.66.66.16/29",
			ParentID:   22,
			Inserted:   true,
		}, nil)
	mockSE.On("DeleteIPRangeByID", mock.Anything, int64(77)).
		Return(&datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 77}}, nil)
	mockSE.On("AdoptInterclusterIPRange", mock.Anything, mock.Anything).
		Return(&database.InterclusterCarveResult{
			IPRange:    &datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 89}},
			CarvedCIDR: "10.66.66.16/29",
			ParentID:   22,
		}, nil)

	conflictErr := vsaerrors.WrapAsNonRetryableTemporalApplicationError(
		vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("secondary range ic-lif-10-66-66-16-29-new (10.66.66.16/29) conflicts with existing range ic-lif-10-66-66-16-29-old (10.66.66.16/29) on subnet vsa-subnet")))

	mockSvc := hyperscaler2.NewMockServices(t)
	mockSvc.On("GetSubnetwork", "host-project", "us-central1", subnetName).
		Return(&hyperscaler_models.Subnet{}, nil)
	mockSvc.On("AddSubnetSecondaryRangeOp", "host-project", "us-central1", subnetName, mock.Anything, "10.66.66.16/29").
		Return("", conflictErr)
	resolveCloudServiceForICLif = func(context.Context) (hyperscaler2.Services, error) {
		return mockSvc, nil
	}

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{
		AccountName:      "acct",
		Region:           "us-central1",
		VendorSubNetID:   "projects/123/global/networks/vpc1",
		SecondaryIpRange: []string{"10.66.66.0/24"},
	}
	result, err := pa.EnsureInterclusterSecondaryRangeOp(context.Background(), params, "pool-uuid", "host-project", subnetName)
	require.NoError(t, err)
	assert.Equal(t, int64(89), result.IPRangeID)
	assert.Equal(t, "10.66.66.16/29", result.CarvedSecondaryCIDR)
	mockSE.AssertNotCalled(t, "ClaimCarvedInterclusterIPRange", mock.Anything, mock.MatchedBy(func(c database.InterclusterCarveClaim) bool {
		return len(c.ExtraReservedCIDRs) > 0
	}))
}

func Test_parseSecondaryCIDRs(t *testing.T) {
	parsed, err := parseSecondaryCIDRs([]string{" 10.44.0.0/16 ", ""})
	require.NoError(t, err)
	assert.Equal(t, []string{"10.44.0.0/16"}, parsed)

	_, err = parseSecondaryCIDRs([]string{"not-a-cidr"})
	require.Error(t, err)

	_, err = parseSecondaryCIDRs([]string{"", "  "})
	require.Error(t, err)
}

func Test_secondaryRangeCIDRPresent(t *testing.T) {
	secondaries := []hyperscaler_models.SecondaryIpRange{
		{IpCidrRange: "10.66.66.16/29"},
	}
	assert.True(t, secondaryRangeCIDRPresent(secondaries, "10.66.66.16/29"))
	assert.False(t, secondaryRangeCIDRPresent(secondaries, "10.66.66.24/29"))
	assert.False(t, secondaryRangeCIDRPresent(secondaries, ""))
}

func Test_findManagedICLifSecondary_multipleMatches(t *testing.T) {
	_, _, err := findManagedICLifSecondary(
		[]hyperscaler_models.SecondaryIpRange{
			{RangeName: "ic-lif-a", IpCidrRange: "10.66.66.16/29"},
			{RangeName: "ic-lif-b", IpCidrRange: "10.66.66.24/29"},
		},
		[]string{"10.66.66.0/24"},
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func Test_maxHAPairsOnSubnet_invalidPrefix(t *testing.T) {
	assert.Equal(t, 0, maxHAPairsOnSubnet(-1))
	assert.Equal(t, 0, maxHAPairsOnSubnet(33))
}

func Test_EnsureInterclusterSecondaryRangeOp_patchExistingSkipsWhenOnGCP(t *testing.T) {
	origEnabled := icLifAddressSpaceEnabled
	origResolve := resolveCloudServiceForICLif
	t.Cleanup(func() {
		icLifAddressSpaceEnabled = origEnabled
		resolveCloudServiceForICLif = origResolve
	})
	icLifAddressSpaceEnabled = func() bool { return true }

	subnetName := "vsa-subnet"
	accountUUID := "acct-uuid"
	existing := &datamodel.IPRange{
		BaseModel: datamodel.BaseModel{ID: 55},
		LifType:   database.LifTypeInterclusterLIF,
		StartIP:   "10.45.0.0",
		EndIP:     "10.45.0.7",
	}
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAccount", mock.Anything, "acct").
		Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: accountUUID}}, nil)
	mockSE.On("GetIPRanges", mock.Anything, &accountUUID, &subnetName).
		Return([]*datamodel.IPRange{existing}, nil)

	mockSvc := hyperscaler2.NewMockServices(t)
	mockSvc.On("GetSubnetwork", "host-project", "us-central1", subnetName).
		Return(&hyperscaler_models.Subnet{
			SecondaryIpRanges: []hyperscaler_models.SecondaryIpRange{
				{RangeName: "ic-lif-existing", IpCidrRange: "10.45.0.0/29"},
			},
		}, nil)
	resolveCloudServiceForICLif = func(context.Context) (hyperscaler2.Services, error) {
		return mockSvc, nil
	}

	pa := &PoolActivity{SE: mockSE}
	params := commonparams.CreatePoolParams{
		AccountName:      "acct",
		Region:           "us-central1",
		SecondaryIpRange: []string{"10.45.0.0/16"},
	}
	result, err := pa.EnsureInterclusterSecondaryRangeOp(context.Background(), params, "pool-uuid", "host-project", subnetName)
	require.NoError(t, err)
	assert.Equal(t, int64(55), result.IPRangeID)
	assert.Equal(t, "10.45.0.0/29", result.CarvedSecondaryCIDR)
	assert.Empty(t, result.OperationName)
	mockSvc.AssertNotCalled(t, "AddSubnetSecondaryRangeOp", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func Test_ReleaseInterclusterIPRangeClaim(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	mockSE.On("DeleteIPRangeByID", mock.Anything, int64(42)).
		Return(&datamodel.IPRange{BaseModel: datamodel.BaseModel{ID: 42}}, nil)

	pa := &PoolActivity{SE: mockSE}
	require.NoError(t, pa.ReleaseInterclusterIPRangeClaim(context.Background(), 42))
	require.NoError(t, pa.ReleaseInterclusterIPRangeClaim(context.Background(), 0))
}

func Test_poolNameDiscriminator_emptyUUID(t *testing.T) {
	assert.Empty(t, poolNameDiscriminator(""))
	assert.Empty(t, poolNameDiscriminator("   "))
}

func Test_sanitizeGCPResourceName_truncatesLongNames(t *testing.T) {
	name := sanitizeGCPResourceName(strings.Repeat("10.44.0.0/29", 20), icLifSecondaryRangeNamePrefix)
	assert.LessOrEqual(t, len(name), 63)
	assert.True(t, strings.HasPrefix(name, icLifSecondaryRangeNamePrefix))
}
