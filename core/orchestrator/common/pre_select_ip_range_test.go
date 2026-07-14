package common_test

import (
	"context"
	"errors"
	"testing"

	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func TestIsDataLifIPRangeExhausted(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		assert.False(t, common.IsDataLifIPRangeExhausted(nil))
	})

	t.Run("wrapped unavailable", func(t *testing.T) {
		inner := customerrors.NewUnavailableErr(common.ErrNoIPAddressesAvailable)
		wrapped := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, inner)
		assert.True(t, common.IsDataLifIPRangeExhausted(wrapped))
	})

	t.Run("other error", func(t *testing.T) {
		assert.False(t, common.IsDataLifIPRangeExhausted(errors.New("database read failed")))
	})
}

func TestPreSelectDataLifIPRange(t *testing.T) {
	ctx := context.Background()
	params := common.PreSelectDataLifIPRangeParams{
		AccountUUID:    "acct",
		VendorSubnetID: "projects/p/global/networks/net",
		HostUUID:       "host-1",
		SubnetMaskSize: 27,
		IPAddressCount: 2,
	}

	t.Run("no ranges returns empty", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetDataLIFIPRanges", mock.Anything, params.AccountUUID, params.VendorSubnetID).
			Return(nil, nil).Once()

		uuid, err := common.PreSelectDataLifIPRange(ctx, mockSE, nil, params)
		require.NoError(t, err)
		assert.Empty(t, uuid)
	})

	t.Run("reuses first range with capacity", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetDataLIFIPRanges", mock.Anything, params.AccountUUID, params.VendorSubnetID).
			Return([]*datamodel.IPRange{
				{BaseModel: datamodel.BaseModel{UUID: "range-1"}},
				{BaseModel: datamodel.BaseModel{UUID: "range-2"}},
			}, nil).Once()
		mockSE.On("ValidateIPAddressesAvailableInIPRange", mock.Anything, "range-1", params.IPAddressCount).
			Return(vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				customerrors.NewUnavailableErr(common.ErrNoIPAddressesAvailable))).Once()
		mockSE.On("ValidateIPAddressesAvailableInIPRange", mock.Anything, "range-2", params.IPAddressCount).
			Return(nil).Once()

		uuid, err := common.PreSelectDataLifIPRange(ctx, mockSE, nil, params)
		require.NoError(t, err)
		assert.Equal(t, "range-2", uuid)
	})

	t.Run("requests secondary range when all exhausted", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetDataLIFIPRanges", mock.Anything, params.AccountUUID, params.VendorSubnetID).
			Return([]*datamodel.IPRange{{BaseModel: datamodel.BaseModel{UUID: "range-1"}}}, nil).Once()
		mockSE.On("ValidateIPAddressesAvailableInIPRange", mock.Anything, "range-1", params.IPAddressCount).
			Return(vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				customerrors.NewUnavailableErr(common.ErrNoIPAddressesAvailable))).Once()

		mockCVN := sdk.NewMockCVN(t)
		defer mockCVN.CloseMockCVN()

		remoteAccountID := params.VendorSubnetID
		expectedSecParams := &sdk.SecondaryIPRangeParams{
			AccountUUID:     params.AccountUUID,
			RemoteAccountID: &remoteAccountID,
			SubnetMaskSize:  params.SubnetMaskSize,
		}

		var gotUUID string
		var gotErr error
		go func() {
			defer mockCVN.MockCVNDone()
			gotUUID, gotErr = common.PreSelectDataLifIPRange(ctx, mockSE, mockCVN, params)
		}()

		mockCVN.AssertCreateSecondaryIPRangeWithPolling(expectedSecParams, "range-new", nil)
		mockCVN.AssertMockCVNDone()

		require.NoError(t, gotErr)
		assert.Equal(t, "range-new", gotUUID)
	})

	t.Run("propagates non-exhaustion validation errors", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetDataLIFIPRanges", mock.Anything, params.AccountUUID, params.VendorSubnetID).
			Return([]*datamodel.IPRange{{BaseModel: datamodel.BaseModel{UUID: "range-1"}}}, nil).Once()
		mockSE.On("ValidateIPAddressesAvailableInIPRange", mock.Anything, "range-1", params.IPAddressCount).
			Return(errors.New("invalid range bounds")).Once()

		_, err := common.PreSelectDataLifIPRange(ctx, mockSE, nil, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid range bounds")
	})
}
