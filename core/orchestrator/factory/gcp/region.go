package gcp

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

// CreateRegion creates a new region in the database.
func (o *GCPOrchestrator) CreateRegion(ctx context.Context, region *datamodel.Region) (*datamodel.Region, error) {
	return o.storage.CreateRegion(ctx, region)
}

// ListRegions lists all active regions.
func (o *GCPOrchestrator) ListRegions(ctx context.Context) ([]*datamodel.Region, error) {
	return o.storage.ListRegions(ctx)
}

// UpdateRegion updates mutable fields of a region by UUID.
func (o *GCPOrchestrator) UpdateRegion(ctx context.Context, region *datamodel.Region) (*datamodel.Region, string, error) {
	updatedRegion, err := o.storage.UpdateRegion(ctx, region)
	if err != nil {
		return nil, "", err
	}
	return updatedRegion, "", nil
}
