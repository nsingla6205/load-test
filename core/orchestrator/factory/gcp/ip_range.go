package gcp

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

// CreateIPRange creates a new IP range in the database.
func (o *GCPOrchestrator) CreateIPRange(ctx context.Context, ir *datamodel.IPRange) (*datamodel.IPRange, error) {
	return o.storage.CreateIPRange(ctx, ir)
}

// GetIPRanges returns IP ranges filtered by accountUUID and network.
func (o *GCPOrchestrator) GetIPRanges(ctx context.Context, accountUUID, network *string) ([]*datamodel.IPRange, error) {
	return o.storage.GetIPRanges(ctx, accountUUID, network)
}

// UpdateIPRangeNetwork updates the network of an existing IP range.
func (o *GCPOrchestrator) UpdateIPRangeNetwork(ctx context.Context, ir *datamodel.IPRange) (*datamodel.IPRange, error) {
	return o.storage.UpdateIPRangeNetwork(ctx, ir)
}

// DeleteIPRange soft-deletes an IP range by UUID.
func (o *GCPOrchestrator) DeleteIPRange(ctx context.Context, irID string) (*datamodel.IPRange, error) {
	return o.storage.DeleteIPRange(ctx, irID)
}
