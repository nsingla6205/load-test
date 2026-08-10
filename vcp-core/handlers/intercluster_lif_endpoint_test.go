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

func TestInterclusterLifToV1(t *testing.T) {
	now := time.Now().UTC()
	input := &datamodel.Lif{
		BaseModel: datamodel.BaseModel{
			UUID:      "lif-uuid",
			CreatedAt: now,
			UpdatedAt: now,
		},
		Name:        "ic-lif-1",
		IPAddress:   "10.0.0.1",
		SubnetMask:  "255.255.255.0",
		AccountUUID: "account-uuid",
		AccountName: "account-name",
		NodeUUID:    "node-uuid",
		HostUUID:    "host-uuid",
		IPRangeUUID: "ip-range-uuid",
	}

	got := interclusterLifToV1(input)
	assert.Equal(t, "lif-uuid", got.UUID.Value)
	assert.Equal(t, "ic-lif-1", got.Name.Value)
	assert.Equal(t, "10.0.0.1", got.IpAddress.Value)
	assert.Equal(t, "255.255.255.0", got.Netmask.Value)
	assert.Equal(t, "account-uuid", got.AccountUUID.Value)
	assert.Equal(t, "account-name", got.AccountName.Value)
	assert.Equal(t, "node-uuid", got.NodeUUID.Value)
	assert.Equal(t, "host-uuid", got.HostUUID.Value)
	assert.Equal(t, "ip-range-uuid", got.IpRangeUUID.Value)
}

func TestV1ListInterclusterLifs(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		h := Handler{Orchestrator: mockOrch}
		accountUUID := "account-uuid"
		ipRangeUUID := "ip-range-uuid"
		list := []*datamodel.Lif{{
			BaseModel:   datamodel.BaseModel{UUID: "lif-1"},
			Name:        "ic-lif-1",
			IPAddress:   "10.0.0.1",
			AccountUUID: accountUUID,
			HostUUID:    "host-uuid",
			IPRangeUUID: ipRangeUUID,
		}}
		mockOrch.EXPECT().GetInterclusterLifs(mock.Anything, &accountUUID, &ipRangeUUID).Return(list, nil)

		res, err := h.V1ListInterclusterLifs(context.Background(), oasgenserver.V1ListInterclusterLifsParams{
			AccountUUID: oasgenserver.NewOptString(accountUUID),
			IpRangeUUID: oasgenserver.NewOptString(ipRangeUUID),
		})
		assert.NoError(t, err)
		ok, okType := res.(*oasgenserver.V1ListInterclusterLifsOKApplicationJSON)
		assert.True(t, okType)
		assert.Len(t, *ok, 1)
		assert.Equal(t, "lif-1", (*ok)[0].UUID.Value)
	})

	t.Run("NotFound", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		h := Handler{Orchestrator: mockOrch}
		missing := "missing-account"
		mockOrch.EXPECT().GetInterclusterLifs(mock.Anything, &missing, (*string)(nil)).
			Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrAccountNotFound, stderrors.New("account not found")))

		res, err := h.V1ListInterclusterLifs(context.Background(), oasgenserver.V1ListInterclusterLifsParams{
			AccountUUID: oasgenserver.NewOptString(missing),
		})
		assert.NoError(t, err)
		_, ok := res.(*oasgenserver.V1ListInterclusterLifsNotFound)
		assert.True(t, ok)
	})

	t.Run("InternalError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		h := Handler{Orchestrator: mockOrch}
		mockOrch.EXPECT().GetInterclusterLifs(mock.Anything, (*string)(nil), (*string)(nil)).
			Return(nil, stderrors.New("db down"))

		res, err := h.V1ListInterclusterLifs(context.Background(), oasgenserver.V1ListInterclusterLifsParams{})
		assert.NoError(t, err)
		_, ok := res.(*oasgenserver.V1ListInterclusterLifsInternalServerError)
		assert.True(t, ok)
	})
}
