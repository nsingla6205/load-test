package api

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func TestConvertFromIPRangeV1(t *testing.T) {
	t.Run("AllFieldsSet", func(t *testing.T) {
		req := &oasgenserver.IPRangeV1{
			StartIP:     oasgenserver.NewOptString("10.0.0.10"),
			EndIP:       oasgenserver.NewOptString("10.0.0.20"),
			Gateway:     oasgenserver.NewOptString("10.0.0.1"),
			Netmask:     oasgenserver.NewOptString("255.255.255.0"),
			RegionUUID:  oasgenserver.NewOptString("region-uuid"),
			AccountUUID: oasgenserver.NewOptString("account-uuid"),
			Network:     oasgenserver.NewOptString("network-a"),
			LifType:     oasgenserver.NewOptNilIPRangeV1LifType(oasgenserver.IPRangeV1LifTypeDataLIF),
		}

		got := convertFromIPRangeV1(req)

		assert.Equal(t, "10.0.0.10", got.StartIP)
		assert.Equal(t, "10.0.0.20", got.EndIP)
		assert.Equal(t, "10.0.0.1", got.Gateway)
		assert.Equal(t, "255.255.255.0", got.Netmask)
		assert.Equal(t, "region-uuid", got.RegionUUID)
		assert.Equal(t, "account-uuid", got.AccountUUID)
		assert.NotNil(t, got.Network)
		assert.Equal(t, "network-a", *got.Network)
		assert.Equal(t, string(oasgenserver.IPRangeV1LifTypeDataLIF), got.LifType)
	})

	t.Run("OptionalFieldsUnset", func(t *testing.T) {
		req := &oasgenserver.IPRangeV1{
			LifType: oasgenserver.OptNilIPRangeV1LifType{Null: true},
		}

		got := convertFromIPRangeV1(req)

		assert.Equal(t, "", got.StartIP)
		assert.Equal(t, "", got.EndIP)
		assert.Equal(t, "", got.Gateway)
		assert.Equal(t, "", got.Netmask)
		assert.Equal(t, "", got.RegionUUID)
		assert.Equal(t, "", got.AccountUUID)
		assert.Nil(t, got.Network)
		assert.Equal(t, "", got.LifType)
	})
}

func TestIPRangeToV1(t *testing.T) {
	t.Run("WithOptionalFields", func(t *testing.T) {
		now := time.Now().UTC()
		network := "network-a"
		input := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{
				UUID:      "ip-range-uuid",
				CreatedAt: now,
				UpdatedAt: now,
			},
			StartIP:     "10.0.0.10",
			EndIP:       "10.0.0.20",
			Gateway:     "10.0.0.1",
			Netmask:     "255.255.255.0",
			RegionUUID:  "region-uuid",
			RegionName:  "region-name",
			AccountUUID: "account-uuid",
			AccountName: "account-name",
			Network:     &network,
			LifType:     string(oasgenserver.IPRangeV1LifTypeInterclusterLIF),
		}

		got := ipRangeToV1(input)

		assert.True(t, got.Network.IsSet())
		assert.Equal(t, "network-a", got.Network.Value)
		assert.True(t, got.LifType.IsSet())
		assert.Equal(t, oasgenserver.IPRangeV1LifTypeInterclusterLIF, got.LifType.Value)
		assert.Equal(t, "ip-range-uuid", got.UUID.Value)
		assert.Equal(t, "10.0.0.10", got.StartIP.Value)
	})

	t.Run("WithoutOptionalFields", func(t *testing.T) {
		got := ipRangeToV1(&datamodel.IPRange{})

		assert.False(t, got.Network.IsSet())
		assert.False(t, got.LifType.IsSet())
	})
}

func TestV1CreateIPRange(t *testing.T) {
	t.Run("ValidationError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		req := &oasgenserver.IPRangeV1{
			StartIP: oasgenserver.NewOptString("bad-ip"),
			EndIP:   oasgenserver.NewOptString("10.0.0.20"),
			Gateway: oasgenserver.NewOptString("10.0.0.1"),
			Netmask: oasgenserver.NewOptString("255.255.255.0"),
		}

		res, err := handler.V1CreateIPRange(context.Background(), req, oasgenserver.V1CreateIPRangeParams{})

		assert.NoError(t, err)
		badReq, ok := res.(*oasgenserver.V1CreateIPRangeBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(400), badReq.Code)
		assert.Contains(t, badReq.Message, "Error creating IP range")
	})

	t.Run("ConflictError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		req := validCreateIPRangeRequest()
		conflictErr := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, stderrors.New("already exists"))

		mockOrch.EXPECT().CreateIPRange(mock.Anything, mock.AnythingOfType("*datamodel.IPRange")).Return(nil, conflictErr)

		res, err := handler.V1CreateIPRange(context.Background(), req, oasgenserver.V1CreateIPRangeParams{})

		assert.NoError(t, err)
		conflict, ok := res.(*oasgenserver.V1CreateIPRangeConflict)
		assert.True(t, ok)
		assert.Equal(t, float64(409), conflict.Code)
		assert.NotEmpty(t, conflict.Message)
	})

	t.Run("NonConflictCustomError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		req := validCreateIPRangeRequest()
		nonConflictErr := vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, stderrors.New("db read failure"))

		mockOrch.EXPECT().CreateIPRange(mock.Anything, mock.AnythingOfType("*datamodel.IPRange")).Return(nil, nonConflictErr)

		res, err := handler.V1CreateIPRange(context.Background(), req, oasgenserver.V1CreateIPRangeParams{})

		assert.NoError(t, err)
		badReq, ok := res.(*oasgenserver.V1CreateIPRangeInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), badReq.Code)
		assert.Contains(t, badReq.Message, "Error creating IP range")
	})

	t.Run("Success", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		req := validCreateIPRangeRequest()
		network := "network-a"
		created := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "created-ip-range"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
		}

		mockOrch.EXPECT().CreateIPRange(mock.Anything, mock.AnythingOfType("*datamodel.IPRange")).Return(created, nil)

		res, err := handler.V1CreateIPRange(context.Background(), req, oasgenserver.V1CreateIPRangeParams{})

		assert.NoError(t, err)
		out, ok := res.(*oasgenserver.IPRangeV1)
		assert.True(t, ok)
		assert.Equal(t, "created-ip-range", out.UUID.Value)
		assert.Equal(t, "10.0.0.10", out.StartIP.Value)
		assert.True(t, out.Network.IsSet())
		assert.Equal(t, "network-a", out.Network.Value)
	})
}

func TestV1ListIPRanges(t *testing.T) {
	t.Run("SuccessWithFilters", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		accountUUID := "account-uuid"
		network := "network-a"
		params := oasgenserver.V1ListIPRangesParams{
			AccountUUID: oasgenserver.NewOptString(accountUUID),
			Network:     oasgenserver.NewOptString(network),
		}
		list := []*datamodel.IPRange{
			{BaseModel: datamodel.BaseModel{UUID: "ir-1"}, StartIP: "10.0.0.10"},
		}

		mockOrch.EXPECT().GetIPRanges(mock.Anything, &accountUUID, &network).Return(list, nil)

		res, err := handler.V1ListIPRanges(context.Background(), params)

		assert.NoError(t, err)
		out, ok := res.(*oasgenserver.V1ListIPRangesOKApplicationJSON)
		assert.True(t, ok)
		assert.Len(t, *out, 1)
		assert.Equal(t, "ir-1", (*out)[0].UUID.Value)
	})

	t.Run("InternalServerErrorWithoutFilters", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1ListIPRangesParams{}

		mockOrch.EXPECT().GetIPRanges(mock.Anything, (*string)(nil), (*string)(nil)).Return(nil, stderrors.New("db down"))

		res, err := handler.V1ListIPRanges(context.Background(), params)

		assert.NoError(t, err)
		internal, ok := res.(*oasgenserver.V1ListIPRangesInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), internal.Code)
		assert.Contains(t, internal.Message, "Error listing IP ranges")
	})
}

func TestV1DeleteIPRange(t *testing.T) {
	t.Run("NotFound", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1DeleteIPRangeParams{IpRangeUuid: "missing"}
		notFoundErr := vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, stderrors.New("missing"))

		mockOrch.EXPECT().DeleteIPRange(mock.Anything, "missing").Return(nil, notFoundErr)

		res, err := handler.V1DeleteIPRange(context.Background(), params)

		assert.NoError(t, err)
		notFound, ok := res.(*oasgenserver.V1DeleteIPRangeNotFound)
		assert.True(t, ok)
		assert.Equal(t, float64(404), notFound.Code)
		assert.Equal(t, "IP range not found", notFound.Message)
	})

	t.Run("InternalServerError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1DeleteIPRangeParams{IpRangeUuid: "ir-uuid"}

		mockOrch.EXPECT().DeleteIPRange(mock.Anything, "ir-uuid").Return(nil, stderrors.New("delete failed"))

		res, err := handler.V1DeleteIPRange(context.Background(), params)

		assert.NoError(t, err)
		internal, ok := res.(*oasgenserver.V1DeleteIPRangeInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), internal.Code)
		assert.Contains(t, internal.Message, "Error deleting IP range")
	})

	t.Run("Success", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1DeleteIPRangeParams{IpRangeUuid: "ir-uuid"}
		deleted := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ir-uuid"},
			StartIP:   "10.0.0.10",
		}

		mockOrch.EXPECT().DeleteIPRange(mock.Anything, "ir-uuid").Return(deleted, nil)

		res, err := handler.V1DeleteIPRange(context.Background(), params)

		assert.NoError(t, err)
		out, ok := res.(*oasgenserver.IPRangeV1)
		assert.True(t, ok)
		assert.Equal(t, "ir-uuid", out.UUID.Value)
		assert.Equal(t, "10.0.0.10", out.StartIP.Value)
	})
}

func TestV1UpdateIPRange(t *testing.T) {
	req := &oasgenserver.IPRangeUpdateV1{Network: "updated-network"}
	params := oasgenserver.V1UpdateIPRangeParams{IpRangeUuid: "ir-uuid"}

	t.Run("NotFound", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		notFoundErr := vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, stderrors.New("missing"))

		mockOrch.EXPECT().UpdateIPRangeNetwork(mock.Anything, mock.MatchedBy(func(ir *datamodel.IPRange) bool {
			return ir.UUID == "ir-uuid" && ir.Network != nil && *ir.Network == "updated-network"
		})).Return(nil, notFoundErr)

		res, err := handler.V1UpdateIPRange(context.Background(), req, params)

		assert.NoError(t, err)
		notFound, ok := res.(*oasgenserver.V1UpdateIPRangeNotFound)
		assert.True(t, ok)
		assert.Equal(t, float64(404), notFound.Code)
		assert.Equal(t, "IP range not found", notFound.Message)
	})

	t.Run("InternalServerError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		mockOrch.EXPECT().UpdateIPRangeNetwork(mock.Anything, mock.AnythingOfType("*datamodel.IPRange")).Return(nil, stderrors.New("update failed"))

		res, err := handler.V1UpdateIPRange(context.Background(), req, params)

		assert.NoError(t, err)
		internal, ok := res.(*oasgenserver.V1UpdateIPRangeInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), internal.Code)
		assert.Contains(t, internal.Message, "Error updating IP range")
	})

	t.Run("Success", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		updatedNetwork := "updated-network"
		updated := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ir-uuid"},
			Network:   &updatedNetwork,
		}

		mockOrch.EXPECT().UpdateIPRangeNetwork(mock.Anything, mock.AnythingOfType("*datamodel.IPRange")).Return(updated, nil)

		res, err := handler.V1UpdateIPRange(context.Background(), req, params)

		assert.NoError(t, err)
		out, ok := res.(*oasgenserver.IPRangeV1)
		assert.True(t, ok)
		assert.Equal(t, "ir-uuid", out.UUID.Value)
		assert.True(t, out.Network.IsSet())
		assert.Equal(t, "updated-network", out.Network.Value)
	})
}

func validCreateIPRangeRequest() *oasgenserver.IPRangeV1 {
	return &oasgenserver.IPRangeV1{
		StartIP: oasgenserver.NewOptString("10.0.0.10"),
		EndIP:   oasgenserver.NewOptString("10.0.0.20"),
		Gateway: oasgenserver.NewOptString("10.0.0.1"),
		Netmask: oasgenserver.NewOptString("255.255.255.0"),
	}
}
