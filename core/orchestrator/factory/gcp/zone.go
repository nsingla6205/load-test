package gcp

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

// CreateZone creates a new zone in the database.
func (o *GCPOrchestrator) CreateZone(ctx context.Context, zone *datamodel.Zone, regionUUID string) (*datamodel.Zone, error) {
	return o.storage.CreateZone(ctx, zone, regionUUID)
}

// ListZones lists all zones.
func (o *GCPOrchestrator) ListZones(ctx context.Context) ([]*datamodel.Zone, error) {
	return o.storage.ListZones(ctx)
}

// UpdateZone updates zone fields by UUID.
func (o *GCPOrchestrator) UpdateZone(ctx context.Context, zone *datamodel.Zone, regionUUID string) (*datamodel.Zone, error) {
	return o.storage.UpdateZone(ctx, zone, regionUUID)
}

