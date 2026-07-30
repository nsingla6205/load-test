package api

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

func (h Handler) V1betaListProjectInventory(ctx context.Context, params oasgenserver.V1betaListProjectInventoryParams) (oasgenserver.V1betaListProjectInventoryRes, error) {
	logger := util.GetLogger(ctx)

	showVolumes := params.ShowVolumes.Or(false)
	logger.InfoContext(ctx, "Processing project inventory request",
		"projectNumber", params.ProjectNumber,
		"locationId", params.LocationId,
		"showVolumes", showVolumes)

	result, err := h.Orchestrator.GetProjectInventory(ctx, &commonparams.GetProjectInventoryParams{
		ProjectNumber: params.ProjectNumber,
		ShowVolumes:   showVolumes,
	})
	if err != nil {
		logger.ErrorContext(ctx, "GetProjectInventory failed", "error", err)
		return &oasgenserver.V1betaListProjectInventoryInternalServerError{
			Code:    float64(500),
			Message: fmt.Sprintf("failed to fetch project inventory: %s", err.Error()),
		}, nil
	}

	if len(result.Clusters) == 0 {
		logger.WarnContext(ctx, "No clusters found for project", "projectNumber", params.ProjectNumber)
		return &oasgenserver.V1betaListProjectInventoryNotFound{
			Code:    float64(404),
			Message: fmt.Sprintf("no hosts found for project %s", params.ProjectNumber),
		}, nil
	}

	logger.InfoContext(ctx, "Inventory assembled", "projectNumber", params.ProjectNumber, "clusterCount", len(result.Clusters))
	return &oasgenserver.ProjectInventory{Clusters: toAPIInventoryClusters(result.Clusters)}, nil
}

// toAPIInventoryClusters maps the domain InventoryCluster slice to the generated API type.
func toAPIInventoryClusters(clusters []commonparams.InventoryCluster) []oasgenserver.InventoryCluster {
	out := make([]oasgenserver.InventoryCluster, 0, len(clusters))
	for _, c := range clusters {
		out = append(out, toAPIInventoryCluster(c))
	}
	return out
}

func toAPIInventoryCluster(c commonparams.InventoryCluster) oasgenserver.InventoryCluster {
	inv := oasgenserver.InventoryCluster{
		Name:       c.Name,
		Aggregates: toAPIAggregates(c.Aggregates),
		Svms:       toAPISVMs(c.SVMs),
	}
	if c.Size != 0 {
		inv.Size = oasgenserver.NewOptInt64(c.Size)
		inv.UsedSize = oasgenserver.NewOptInt64(c.UsedSize)
		inv.AvailableSize = oasgenserver.NewOptInt64(c.AvailableSize)
	}
	return inv
}

func toAPIAggregates(aggrs []commonparams.InventoryAggregate) []oasgenserver.InventoryAggregate {
	out := make([]oasgenserver.InventoryAggregate, 0, len(aggrs))
	for _, a := range aggrs {
		agg := oasgenserver.InventoryAggregate{Name: a.Name}
		if uid, err := uuid.Parse(a.UUID); err == nil {
			agg.UUID = oasgenserver.NewOptUUID(uid)
		}
		if a.State != "" {
			agg.State = oasgenserver.NewOptString(a.State)
		}
		if a.VolumeCount != 0 {
			agg.VolumeCount = oasgenserver.NewOptInt64(a.VolumeCount)
		}
		if a.Size != 0 {
			agg.Size = oasgenserver.NewOptInt64(a.Size)
			agg.UsedSize = oasgenserver.NewOptInt64(a.UsedSize)
			agg.AvailableSize = oasgenserver.NewOptInt64(a.AvailableSize)
		}
		out = append(out, agg)
	}
	return out
}

func toAPISVMs(svms []commonparams.InventorySVM) []oasgenserver.InventorySvm {
	out := make([]oasgenserver.InventorySvm, 0, len(svms))
	for _, s := range svms {
		vols := make([]oasgenserver.InventoryVolume, 0, len(s.Volumes))
		for _, v := range s.Volumes {
			uid, err := uuid.Parse(v.UUID)
			if err != nil {
				continue
			}
			vols = append(vols, oasgenserver.InventoryVolume{Name: v.Name, UUID: uid})
		}
		out = append(out, oasgenserver.InventorySvm{Name: s.Name, Volumes: vols})
	}
	return out
}
