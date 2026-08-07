package factory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gcporch "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/gcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/gcp/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/oci"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
)

func TestGetOrchestratorForProvider(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockTemporal := workflowenginemock.NewMockTemporalTestClient(t)

	t.Run("returns_GCPOrchestrator_when_hyperscaler_gcp", func(tt *testing.T) {
		prev := env.Hyperscaler
		defer func() { env.Hyperscaler = prev }()
		env.Hyperscaler = "gcp"

		orch := GetOrchestratorForProvider(mockStorage, mockTemporal)
		_, ok := orch.(*gcporch.GCPOrchestrator)
		assert.True(tt, ok)
	})

	t.Run("returns_OCIOrchestrator_when_hyperscaler_oci", func(tt *testing.T) {
		prev := env.Hyperscaler
		defer func() { env.Hyperscaler = prev }()
		env.Hyperscaler = "oci"

		orch := GetOrchestratorForProvider(mockStorage, mockTemporal)
		_, ok := orch.(*oci.OCIOrchestrator)
		assert.True(tt, ok)
	})

	t.Run("returns_GCPOrchestrator_default_when_hyperscaler_unknown", func(tt *testing.T) {
		prev := env.Hyperscaler
		defer func() { env.Hyperscaler = prev }()
		env.Hyperscaler = "azure"

		orch := GetOrchestratorForProvider(mockStorage, mockTemporal)
		_, ok := orch.(*gcporch.GCPOrchestrator)
		require.True(tt, ok)
	})

	t.Run("GetHyperscaler_is_case_insensitive_for_switch", func(tt *testing.T) {
		prev := env.Hyperscaler
		defer func() { env.Hyperscaler = prev }()
		env.Hyperscaler = "OCI"

		orch := GetOrchestratorForProvider(mockStorage, mockTemporal)
		_, ok := orch.(*oci.OCIOrchestrator)
		assert.True(tt, ok)
	})
}

func TestGetVCMOrchestrator(t *testing.T) {
	orch := GetVCMOrchestrator(nil, nil, nil)
	require.NotNil(t, orch)
	_, ok := orch.(*vcm.VCMOrchestrator)
	assert.True(t, ok)
}
