package api

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	ht "github.com/ogen-go/ogen/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func newZone() *datamodel.Zone {
	now := time.Now()
	return &datamodel.Zone{
		BaseModel: datamodel.BaseModel{
			UUID:      "zone-uuid-1",
			CreatedAt: now,
			UpdatedAt: now,
		},
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}
}

func newZoneWithRegion() *datamodel.Zone {
	zone := newZone()
	zone.RegionID = sql.NullInt64{Int64: newRegion().ID, Valid: true}
	return zone
}

func TestV1ListZones_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	zone := newZoneWithRegion()
	mockOrch.On("ListZones", mock.Anything).Return([]*datamodel.Zone{zone}, nil)
	mockOrch.On("ListRegions", mock.Anything).Return([]*datamodel.Region{newRegion()}, nil)

	result, err := handler.V1ListZones(context.Background(), oasgenserver.V1ListZonesParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1ListZonesOKApplicationJSON)
	assert.True(t, ok)
	assert.Len(t, *res, 1)
	assert.Equal(t, "zone-uuid-1", (*res)[0].UUID.Value)
	assert.Equal(t, "region-uuid-1", (*res)[0].RegionUUID)
	assert.Equal(t, "us-west1", (*res)[0].RegionName.Value)
}

func TestV1ListZones_OrchestratorError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("ListZones", mock.Anything).Return(nil, errors.New("db error"))

	result, err := handler.V1ListZones(context.Background(), oasgenserver.V1ListZonesParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1ListZonesInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), res.Code)
}

func TestV1CreateZone_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	zone := newZone()
	mockOrch.On("CreateZone", mock.Anything, mock.MatchedBy(func(z *datamodel.Zone) bool {
		return z.Name == "us-west1-a" && z.Timezone == "America/Los_Angeles"
	}), "region-uuid-1").Return(zone, nil)
	mockOrch.On("ListRegions", mock.Anything).Return([]*datamodel.Region{newRegion()}, nil)

	req := &oasgenserver.ZoneV1{
		Name:       "us-west1-a",
		RegionUUID: "region-uuid-1",
		Timezone:   oasgenserver.NewOptString("America/Los_Angeles"),
	}
	result, err := handler.V1CreateZone(context.Background(), req, oasgenserver.V1CreateZoneParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.ZoneV1)
	assert.True(t, ok)
	assert.Equal(t, "zone-uuid-1", res.UUID.Value)
	assert.Equal(t, "us-west1-a", res.Name)
	assert.Equal(t, "region-uuid-1", res.RegionUUID)
	assert.Equal(t, "us-west1", res.RegionName.Value)
}

func TestV1CreateZone_Conflict(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("CreateZone", mock.Anything, mock.Anything, mock.Anything).Return(nil, conflictErr())

	req := &oasgenserver.ZoneV1{Name: "us-west1-a", RegionUUID: "region-uuid-1"}
	result, err := handler.V1CreateZone(context.Background(), req, oasgenserver.V1CreateZoneParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1CreateZoneConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(409), res.Code)
}

func TestV1CreateZone_NotFound(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("CreateZone", mock.Anything, mock.Anything, mock.Anything).Return(nil, notFoundErr())

	req := &oasgenserver.ZoneV1{Name: "us-west1-a", RegionUUID: "region-uuid-1"}
	result, err := handler.V1CreateZone(context.Background(), req, oasgenserver.V1CreateZoneParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1CreateZoneBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), res.Code)
}

func TestV1CreateZone_InternalServerError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("CreateZone", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("unexpected"))

	req := &oasgenserver.ZoneV1{Name: "us-west1-a", RegionUUID: "region-uuid-1"}
	result, err := handler.V1CreateZone(context.Background(), req, oasgenserver.V1CreateZoneParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1CreateZoneInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), res.Code)
}

func TestV1UpdateZone_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	zone := newZone()
	mockOrch.On("UpdateZone", mock.Anything, mock.MatchedBy(func(z *datamodel.Zone) bool {
		return z.UUID == "zone-uuid-1" && z.Name == "us-west1-b" && z.Timezone == "America/Los_Angeles"
	}), "region-uuid-1").Return(zone, nil)
	mockOrch.On("ListRegions", mock.Anything).Return([]*datamodel.Region{newRegion()}, nil)

	req := &oasgenserver.ZoneV1{
		Name:       "us-west1-b",
		RegionUUID: "region-uuid-1",
		Timezone:   oasgenserver.NewOptString("America/Los_Angeles"),
	}
	result, err := handler.V1UpdateZone(context.Background(), req, oasgenserver.V1UpdateZoneParams{UUID: "zone-uuid-1"})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.ZoneV1)
	assert.True(t, ok)
	assert.Equal(t, "zone-uuid-1", res.UUID.Value)
	assert.Equal(t, "region-uuid-1", res.RegionUUID)
}

func TestV1UpdateZone_NotFound(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("UpdateZone", mock.Anything, mock.Anything, mock.Anything).Return(nil, notFoundErr())

	result, err := handler.V1UpdateZone(context.Background(), &oasgenserver.ZoneV1{Name: "us-west1-b", RegionUUID: "region-uuid-1"}, oasgenserver.V1UpdateZoneParams{UUID: "zone-uuid-1"})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1UpdateZoneNotFound)
	assert.True(t, ok)
	assert.Equal(t, float64(404), res.Code)
}

func TestV1UpdateZone_Conflict(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("UpdateZone", mock.Anything, mock.Anything, mock.Anything).Return(nil, conflictErr())

	result, err := handler.V1UpdateZone(context.Background(), &oasgenserver.ZoneV1{Name: "us-west1-b", RegionUUID: "region-uuid-1"}, oasgenserver.V1UpdateZoneParams{UUID: "zone-uuid-1"})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1UpdateZoneBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), res.Code)
}

func TestV1UpdateZone_InternalServerError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("UpdateZone", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("unexpected"))

	result, err := handler.V1UpdateZone(context.Background(), &oasgenserver.ZoneV1{Name: "us-west1-b", RegionUUID: "region-uuid-1"}, oasgenserver.V1UpdateZoneParams{UUID: "zone-uuid-1"})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1UpdateZoneInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), res.Code)
}

func TestV1DeleteZone_NotImplemented(t *testing.T) {
	handler := NewHandler(factory.NewMockOrchestratorFactory(t))

	result, err := handler.V1DeleteZone(context.Background(), oasgenserver.V1DeleteZoneParams{UUID: "zone-uuid-1"})

	assert.ErrorIs(t, err, ht.ErrNotImplemented)
	assert.Nil(t, result)
}
