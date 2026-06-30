package gcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflow_engine_mock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
)

func TestNewGCPOrchestrator(t *testing.T) {
	t.Run("Success_CreatesOrchestrator", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		orch := NewGCPOrchestrator(mockStorage, mockTemporal)

		assert.NotNil(tt, orch)
		assert.Equal(tt, mockStorage, orch.storage)
		assert.Equal(tt, mockTemporal, orch.temporal)
	})
}

func TestGCPOrchestrator_GetPoolNodesAndCredentials(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &GCPOrchestrator{}
		ctx := context.Background()

		nodes, creds, err := orch.GetPoolNodesAndCredentials(ctx, "pool-uuid")

		assert.Error(tt, err)
		assert.True(tt, utilserrors.IsNotImplementedYetErr(err))
		assert.Nil(tt, nodes)
		assert.Nil(tt, creds)
	})
}
