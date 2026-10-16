package api

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/handlers/opsinventory"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

type Handler struct {
	oasgenserver.UnimplementedHandler
	Orchestrator factory.OrchestratorFactory
	// OpsInventory serves the read-only /v1/internal/projects/{projectNumber}/locations/{locationId}/resources API.
	// It reads the VCP DB through its own read store, not the orchestrator.
	OpsInventory *opsinventory.Handler
}

func NewHandler(orch factory.OrchestratorFactory) *Handler {
	return &Handler{
		Orchestrator: orch,
	}
}

func (h Handler) GetHealth(ctx context.Context) (oasgenserver.GetHealthRes, error) {
	return &oasgenserver.Health{}, nil
}
