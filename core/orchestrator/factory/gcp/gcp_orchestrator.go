package gcp

import (
	"context"

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

// GetPoolNodesAndCredentials is not implemented for GCP; the OCI workflow response
// is the only consumer of this lookup today. Wired into the interface so the GCP
// orchestrator continues to satisfy OrchestratorFactory.
func (o *GCPOrchestrator) GetPoolNodesAndCredentials(_ context.Context, _ string) ([]*datamodel.Node, *datamodel.PoolCredentials, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}
