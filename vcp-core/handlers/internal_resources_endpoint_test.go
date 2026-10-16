package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/handlers/opsinventory"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func newOpsInventoryHandler(t *testing.T) (*Handler, *readstore.MockReadStore) {
	store := readstore.NewMockReadStore(t)
	handler := NewHandler(factory.NewMockOrchestratorFactory(t))
	handler.OpsInventory = opsinventory.NewHandler(store, opsinventory.NewReaders(store, nil),
		opsinventory.WithCloudCheck(func() bool { return false }))
	return handler, store
}

func TestV1GetInternalResource(t *testing.T) {
	t.Run("WhenNotConfigured", func(t *testing.T) {
		handler := NewHandler(factory.NewMockOrchestratorFactory(t))
		res, err := handler.V1GetInternalResource(context.Background(), &oasgenserver.OpsGetResourceRequestV1{}, oasgenserver.V1GetInternalResourceParams{})
		require.NoError(t, err)
		notImplemented, ok := res.(*oasgenserver.V1GetInternalResourceNotImplemented)
		require.True(t, ok)
		assert.Equal(t, float64(501), notImplemented.Code)
	})

	t.Run("WhenConfiguredItDelegates", func(t *testing.T) {
		handler, store := newOpsInventoryHandler(t)
		store.EXPECT().GetVolumeByUUID(mock.Anything, "vol-1").
			Return(&datamodel.Volume{BaseModel: datamodel.BaseModel{ID: 1, UUID: "vol-1"}, AccountID: 1}, nil)
		req := &oasgenserver.OpsGetResourceRequestV1{
			ResourceType: oasgenserver.OpsResourceTypeV1Volume,
			Identity:     oasgenserver.OpsIdentityV1{UUID: oasgenserver.NewOptString("vol-1")},
		}
		res, err := handler.V1GetInternalResource(context.Background(), req, oasgenserver.V1GetInternalResourceParams{ProjectNumber: "-", LocationId: "-"})
		require.NoError(t, err)
		resource, ok := res.(*oasgenserver.OpsResourceV1)
		require.True(t, ok)
		assert.Equal(t, "vol-1", resource.OpsVolumeV1.UUID)
	})

	t.Run("WhenConfiguredAndNotFound", func(t *testing.T) {
		handler, store := newOpsInventoryHandler(t)
		store.EXPECT().GetVolumeByUUID(mock.Anything, "missing").Return(nil, readstore.ErrNotFound)
		req := &oasgenserver.OpsGetResourceRequestV1{
			ResourceType: oasgenserver.OpsResourceTypeV1Volume,
			Identity:     oasgenserver.OpsIdentityV1{UUID: oasgenserver.NewOptString("missing")},
		}
		res, err := handler.V1GetInternalResource(context.Background(), req, oasgenserver.V1GetInternalResourceParams{ProjectNumber: "-", LocationId: "-"})
		require.NoError(t, err)
		_, ok := res.(*oasgenserver.V1GetInternalResourceNotFound)
		assert.True(t, ok)
	})
}

func TestV1ListInternalResources(t *testing.T) {
	t.Run("WhenNotConfigured", func(t *testing.T) {
		handler := NewHandler(factory.NewMockOrchestratorFactory(t))
		res, err := handler.V1ListInternalResources(context.Background(), &oasgenserver.OpsListResourcesRequestV1{}, oasgenserver.V1ListInternalResourcesParams{})
		require.NoError(t, err)
		notImplemented, ok := res.(*oasgenserver.V1ListInternalResourcesNotImplemented)
		require.True(t, ok)
		assert.Equal(t, float64(501), notImplemented.Code)
	})

	t.Run("WhenConfiguredItDelegates", func(t *testing.T) {
		handler, store := newOpsInventoryHandler(t)
		store.EXPECT().GetAccountByName(mock.Anything, "1111").Return(&datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "1111"}, nil)
		store.EXPECT().ListVolumes(mock.Anything, mock.Anything).Return([]datamodel.Volume{{BaseModel: datamodel.BaseModel{ID: 1, UUID: "vol-1"}}}, nil)
		req := &oasgenserver.OpsListResourcesRequestV1{ResourceType: oasgenserver.OpsResourceTypeV1Volume}
		params := oasgenserver.V1ListInternalResourcesParams{ProjectNumber: "1111", LocationId: "-"}
		res, err := handler.V1ListInternalResources(context.Background(), req, params)
		require.NoError(t, err)
		page, ok := res.(*oasgenserver.OpsListResourcesResponseV1)
		require.True(t, ok)
		assert.Len(t, page.Resources, 1)
	})

	t.Run("WhenConfiguredAndInvalid", func(t *testing.T) {
		handler, _ := newOpsInventoryHandler(t)
		req := &oasgenserver.OpsListResourcesRequestV1{ResourceType: oasgenserver.OpsResourceTypeV1Volume}
		params := oasgenserver.V1ListInternalResourcesParams{ProjectNumber: "-", LocationId: "-"}
		res, err := handler.V1ListInternalResources(context.Background(), req, params)
		require.NoError(t, err)
		_, ok := res.(*oasgenserver.V1ListInternalResourcesBadRequest)
		assert.True(t, ok)
	})
}
