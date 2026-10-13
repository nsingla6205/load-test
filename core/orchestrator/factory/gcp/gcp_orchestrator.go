package gcp

import (
	"context"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/client"
)

// GCPOrchestrator is the GCP implementation of OrchestratorFactory
type GCPOrchestrator struct {
	storage  database.Storage
	temporal client.Client
}

// NewGCPOrchestrator creates a new GCP orchestrator
func NewGCPOrchestrator(storage database.Storage, temporalClient client.Client) *GCPOrchestrator {
	return &GCPOrchestrator{
		storage:  storage,
		temporal: temporalClient,
	}
}

// GetPoolWithNodes is not implemented for GCP; the OCI workflow response
// is the only consumer of this lookup today. Wired into the interface so the GCP
// orchestrator continues to satisfy OrchestratorFactory.
func (o *GCPOrchestrator) GetPoolWithNodes(_ context.Context, _ string) (*datamodel.Pool, []*datamodel.Node, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

// ListPoolNasLifs returns NAS LIFs stored in the pool VLM config.
func (o *GCPOrchestrator) ListPoolNasLifs(ctx context.Context, poolUUID string) ([]commonparams.PoolNasLif, error) {
	return commonparams.ListPoolNasLifs(ctx, o.storage, poolUUID)
}

// UpdatePoolNasLifIPs persists new NAS LIF addresses in the pool VLM config and lifs table.
func (o *GCPOrchestrator) UpdatePoolNasLifIPs(ctx context.Context, poolUUID string, updates []commonparams.NasLifIPUpdate) ([]commonparams.NasLifIPUpdateResult, error) {
	return commonparams.UpdatePoolNasLifIPs(ctx, o.storage, poolUUID, updates)
}
