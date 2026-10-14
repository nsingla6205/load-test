package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func TestV1DeleteGcpKmsConfigReturnsTheLoadedConfig(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	mockOrch.On("DeleteKmsConfigCore", mock.Anything, kmsConfigUUID).Return(&models.KmsConfig{
		BaseModel:  models.BaseModel{UUID: kmsConfigUUID},
		ResourceID: "cmek-east4",
	}, &models.Job{BaseModel: models.BaseModel{UUID: "job-1"}}, nil)

	response, err := handler.V1DeleteGcpKmsConfig(context.Background(), oasgenserver.V1DeleteGcpKmsConfigParams{KmsConfigId: kmsConfigUUID})

	assert.NoError(t, err)
	got, ok := response.(*oasgenserver.GcpKmsConfigV1)
	assert.True(t, ok)
	assert.True(t, got.UUID.IsSet())
	assert.Equal(t, kmsConfigUUID, got.UUID.Value)
	assert.Equal(t, "cmek-east4", got.ResourceId.Value)
	assert.Equal(t, "job-1", got.Jobs[0].JobId.Value)
}

func TestV1DeleteGcpKmsConfigReturnsNotFound(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	mockOrch.On("DeleteKmsConfigCore", mock.Anything, kmsConfigUUID).Return((*models.KmsConfig)(nil), (*models.Job)(nil), utilserrors.NewNotFoundErr("KMS Configuration", nil))

	response, err := handler.V1DeleteGcpKmsConfig(context.Background(), oasgenserver.V1DeleteGcpKmsConfigParams{KmsConfigId: kmsConfigUUID})

	assert.NoError(t, err)
	got, ok := response.(*oasgenserver.V1DeleteGcpKmsConfigNotFound)
	assert.True(t, ok)
	assert.Equal(t, float64(http.StatusNotFound), got.Code)
	assert.Equal(t, "KMS Configuration not found", got.Message)
}

func TestV1DeleteGcpKmsConfigReturnsInternalError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	mockOrch.On("DeleteKmsConfigCore", mock.Anything, kmsConfigUUID).Return((*models.KmsConfig)(nil), (*models.Job)(nil), errors.New("database unavailable"))

	response, err := handler.V1DeleteGcpKmsConfig(context.Background(), oasgenserver.V1DeleteGcpKmsConfigParams{KmsConfigId: kmsConfigUUID})

	assert.NoError(t, err)
	got, ok := response.(*oasgenserver.V1DeleteGcpKmsConfigInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(http.StatusInternalServerError), got.Code)
	assert.Equal(t, "database unavailable", got.Message)
}

func TestV1DeleteGcpKmsConfigReturnsConflictWhenAPoolUsesTheConfig(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	mockOrch.On("DeleteKmsConfigCore", mock.Anything, kmsConfigUUID).Return((*models.KmsConfig)(nil), (*models.Job)(nil), utilserrors.NewConflictErr("can not delete this policy as it is still in use by one or more pools"))

	response, err := handler.V1DeleteGcpKmsConfig(context.Background(), oasgenserver.V1DeleteGcpKmsConfigParams{KmsConfigId: kmsConfigUUID})

	assert.NoError(t, err)
	got, ok := response.(*oasgenserver.V1DeleteGcpKmsConfigConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(http.StatusConflict), got.Code)
	assert.Equal(t, "can not delete this policy as it is still in use by one or more pools", got.Message)
}
