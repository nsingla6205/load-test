package gcp

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

// ListSvmsForHardwareClusters returns SVMs for EXTREME service-level hardware cluster pools.
func (o *GCPOrchestrator) ListSvmsForHardwareClusters(ctx context.Context) ([]*datamodel.SvmView, error) {
	return o.storage.ListSvmsForHardwareClusters(ctx)
}
