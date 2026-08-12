package gcp

import (
	"context"
	"strconv"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
)

// ListExpertModeVolumeTenancy returns a keyset-paginated tenancy snapshot for one cluster.
func (o *GCPOrchestrator) ListExpertModeVolumeTenancy(
	ctx context.Context,
	params commonparams.ListExpertModeVolumeTenancyParams,
) ([]commonparams.ExpertModeVolumeTenancy, string, error) {
	limit := commonparams.NormalizeExpertModeVolumeTenancyPageLimit(params.Limit)
	queryLimit := limit + 1

	rows, err := o.storage.ListExpertModeVolumeTenancyByCluster(ctx, params.ClusterName, params.CursorID, queryLimit)
	if err != nil {
		return nil, "", err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	items := make([]commonparams.ExpertModeVolumeTenancy, 0, len(rows))
	for _, row := range rows {
		items = append(items, mapExpertModeVolumeTenancyRow(row))
	}

	nextCursor := ""
	if hasMore && len(rows) > 0 {
		nextCursor = strconv.FormatInt(rows[len(rows)-1].CursorID, 10)
	}
	return items, nextCursor, nil
}

func mapExpertModeVolumeTenancyRow(row *database.ExpertModeVolumeTenancyRow) commonparams.ExpertModeVolumeTenancy {
	return commonparams.ExpertModeVolumeTenancy{
		UUID:           row.VolumeUUID,
		SVM:            row.SVMName,
		Volume:         row.VolumeName,
		Project:        row.Project,
		PoolName:       row.PoolName,
		TenantProject:  row.TenantProject,
		DeploymentName: row.DeploymentName,
	}
}
