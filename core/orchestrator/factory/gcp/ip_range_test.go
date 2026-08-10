package gcp

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
)

func strPtr(v string) *string {
	return &v
}

func TestGCPOrchestrator_IPRange_CreateIPRange(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		orch := &GCPOrchestrator{storage: mockStorage}

		input := &datamodel.IPRange{
			StartIP: "10.0.0.10",
			EndIP:   "10.0.0.20",
		}
		expected := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ir-1"},
			StartIP:   input.StartIP,
			EndIP:     input.EndIP,
		}

		mockStorage.EXPECT().CreateIPRange(ctx, input).Return(expected, nil).Once()

		got, err := orch.CreateIPRange(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, expected, got)
	})

	t.Run("storage error", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		orch := &GCPOrchestrator{storage: mockStorage}

		input := &datamodel.IPRange{StartIP: "10.0.0.10", EndIP: "10.0.0.20"}
		expectedErr := stderrors.New("create failed")
		mockStorage.EXPECT().CreateIPRange(ctx, input).Return(nil, expectedErr).Once()

		got, err := orch.CreateIPRange(ctx, input)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, expectedErr)
	})
}

func TestGCPOrchestrator_IPRange_GetIPRanges(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		orch := &GCPOrchestrator{storage: mockStorage}

		accountUUID := strPtr("acct-1")
		network := strPtr("projects/p/global/networks/vpc-1")
		expected := []*datamodel.IPRange{
			{BaseModel: datamodel.BaseModel{UUID: "ir-1"}, Network: network},
			{BaseModel: datamodel.BaseModel{UUID: "ir-2"}, Network: network},
		}
		mockStorage.EXPECT().GetIPRanges(ctx, accountUUID, network).Return(expected, nil).Once()

		got, err := orch.GetIPRanges(ctx, accountUUID, network)
		require.NoError(t, err)
		assert.Equal(t, expected, got)
	})

	t.Run("storage error", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		orch := &GCPOrchestrator{storage: mockStorage}

		accountUUID := strPtr("acct-1")
		network := strPtr("projects/p/global/networks/vpc-1")
		expectedErr := stderrors.New("list failed")
		mockStorage.EXPECT().GetIPRanges(ctx, accountUUID, network).Return(nil, expectedErr).Once()

		got, err := orch.GetIPRanges(ctx, accountUUID, network)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, expectedErr)
	})
}

func TestGCPOrchestrator_IPRange_UpdateIPRangeNetwork(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		orch := &GCPOrchestrator{storage: mockStorage}

		input := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ir-1"},
			Network:   strPtr("projects/p/global/networks/vpc-2"),
		}
		expected := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ir-1"},
			Network:   input.Network,
		}
		mockStorage.EXPECT().UpdateIPRangeNetwork(ctx, input).Return(expected, nil).Once()

		got, err := orch.UpdateIPRangeNetwork(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, expected, got)
	})

	t.Run("storage error", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		orch := &GCPOrchestrator{storage: mockStorage}

		input := &datamodel.IPRange{BaseModel: datamodel.BaseModel{UUID: "ir-1"}}
		expectedErr := stderrors.New("update failed")
		mockStorage.EXPECT().UpdateIPRangeNetwork(ctx, input).Return(nil, expectedErr).Once()

		got, err := orch.UpdateIPRangeNetwork(ctx, input)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, expectedErr)
	})
}

func TestGCPOrchestrator_IPRange_DeleteIPRange(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		orch := &GCPOrchestrator{storage: mockStorage}

		irID := "ir-1"
		expected := &datamodel.IPRange{BaseModel: datamodel.BaseModel{UUID: irID}}
		mockStorage.EXPECT().DeleteIPRange(ctx, irID).Return(expected, nil).Once()

		got, err := orch.DeleteIPRange(ctx, irID)
		require.NoError(t, err)
		assert.Equal(t, expected, got)
	})

	t.Run("storage error", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		orch := &GCPOrchestrator{storage: mockStorage}

		irID := "ir-1"
		expectedErr := stderrors.New("delete failed")
		mockStorage.EXPECT().DeleteIPRange(ctx, irID).Return(nil, expectedErr).Once()

		got, err := orch.DeleteIPRange(ctx, irID)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, expectedErr)
	})
}

func TestGCPOrchestrator_GetInterclusterLifs(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		orch := &GCPOrchestrator{storage: mockStorage}

		accountUUID := strPtr("acct-1")
		ipRangeUUID := strPtr("ip-range-1")
		expected := []*datamodel.Lif{{BaseModel: datamodel.BaseModel{UUID: "lif-1"}}}
		mockStorage.EXPECT().GetInterclusterLifs(ctx, accountUUID, ipRangeUUID).Return(expected, nil).Once()

		got, err := orch.GetInterclusterLifs(ctx, accountUUID, ipRangeUUID)
		require.NoError(t, err)
		assert.Equal(t, expected, got)
	})

	t.Run("storage error", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		orch := &GCPOrchestrator{storage: mockStorage}

		expectedErr := stderrors.New("list failed")
		mockStorage.EXPECT().GetInterclusterLifs(ctx, (*string)(nil), (*string)(nil)).Return(nil, expectedErr).Once()

		got, err := orch.GetInterclusterLifs(ctx, nil, nil)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, expectedErr)
	})
}
