package api

import (
	"context"
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

func newRegion() *datamodel.Region {
	now := time.Now()
	return &datamodel.Region{
		BaseModel: datamodel.BaseModel{
			UUID:      "region-uuid-1",
			CreatedAt: now,
			UpdatedAt: now,
		},
		Name: "us-west1",
	}
}

func TestV1ListRegions_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	region := newRegion()
	mockOrch.On("ListRegions", mock.Anything).Return([]*datamodel.Region{region}, nil)

	result, err := handler.V1ListRegions(context.Background(), oasgenserver.V1ListRegionsParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1ListRegionsOKApplicationJSON)
	assert.True(t, ok)
	assert.Len(t, *res, 1)
	assert.Equal(t, "region-uuid-1", (*res)[0].UUID.Value)
	assert.Equal(t, "us-west1", (*res)[0].Name)
}

func TestV1ListRegions_OrchestratorError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("ListRegions", mock.Anything).Return(nil, errors.New("db error"))

	result, err := handler.V1ListRegions(context.Background(), oasgenserver.V1ListRegionsParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1ListRegionsInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), res.Code)
}

func TestV1CreateRegion_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	region := newRegion()
	mockOrch.On("CreateRegion", mock.Anything, mock.MatchedBy(func(r *datamodel.Region) bool {
		return r.Name == "us-west1"
	})).Return(region, nil)

	req := &oasgenserver.RegionV1{Name: "us-west1"}
	result, err := handler.V1CreateRegion(context.Background(), req, oasgenserver.V1CreateRegionParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.RegionV1)
	assert.True(t, ok)
	assert.Equal(t, "region-uuid-1", res.UUID.Value)
	assert.Equal(t, "us-west1", res.Name)
}

func TestV1CreateRegion_Conflict(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("CreateRegion", mock.Anything, mock.Anything).Return(nil, conflictErr())

	req := &oasgenserver.RegionV1{Name: "us-west1"}
	result, err := handler.V1CreateRegion(context.Background(), req, oasgenserver.V1CreateRegionParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1CreateRegionConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(409), res.Code)
}

func TestV1CreateRegion_InternalServerError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("CreateRegion", mock.Anything, mock.Anything).Return(nil, errors.New("unexpected"))

	req := &oasgenserver.RegionV1{Name: "us-west1"}
	result, err := handler.V1CreateRegion(context.Background(), req, oasgenserver.V1CreateRegionParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1CreateRegionInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), res.Code)
}

func TestV1UpdateRegion_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	region := newRegion()
	mockOrch.On("UpdateRegion", mock.Anything, mock.MatchedBy(func(r *datamodel.Region) bool {
		return r.UUID == "region-uuid-1" && r.Name == "us-west2"
	})).Return(region, "", nil)

	req := &oasgenserver.RegionV1{Name: "us-west2"}
	result, err := handler.V1UpdateRegion(context.Background(), req, oasgenserver.V1UpdateRegionParams{UUID: "region-uuid-1"})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.RegionV1)
	assert.True(t, ok)
	assert.Equal(t, "region-uuid-1", res.UUID.Value)
}

func TestV1UpdateRegion_NotFound(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("UpdateRegion", mock.Anything, mock.Anything).Return(nil, "", notFoundErr())

	result, err := handler.V1UpdateRegion(context.Background(), &oasgenserver.RegionV1{Name: "us-west2"}, oasgenserver.V1UpdateRegionParams{UUID: "region-uuid-1"})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1UpdateRegionNotFound)
	assert.True(t, ok)
	assert.Equal(t, float64(404), res.Code)
}

func TestV1UpdateRegion_Conflict(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("UpdateRegion", mock.Anything, mock.Anything).Return(nil, "", conflictErr())

	result, err := handler.V1UpdateRegion(context.Background(), &oasgenserver.RegionV1{Name: "us-west2"}, oasgenserver.V1UpdateRegionParams{UUID: "region-uuid-1"})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1UpdateRegionBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), res.Code)
}

func TestV1UpdateRegion_InternalServerError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("UpdateRegion", mock.Anything, mock.Anything).Return(nil, "", errors.New("unexpected"))

	result, err := handler.V1UpdateRegion(context.Background(), &oasgenserver.RegionV1{Name: "us-west2"}, oasgenserver.V1UpdateRegionParams{UUID: "region-uuid-1"})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1UpdateRegionInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), res.Code)
}

func TestV1DeleteRegion_NotImplemented(t *testing.T) {
	handler := NewHandler(factory.NewMockOrchestratorFactory(t))

	result, err := handler.V1DeleteRegion(context.Background(), oasgenserver.V1DeleteRegionParams{UUID: "region-uuid-1"})

	assert.ErrorIs(t, err, ht.ErrNotImplemented)
	assert.Nil(t, result)
}
