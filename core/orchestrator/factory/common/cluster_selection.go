package common

import (
	"context"
	"sort"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ValidateBatchClusterSelectionParams validates mode-specific cluster selection parameters.
func ValidateBatchClusterSelectionParams(params *commonparams.BatchUpgradeClusterParams) error {
	if params == nil {
		return customerrors.NewBadRequestErr("Batch cluster upgrade selection parameters are required")
	}

	switch params.SelectionType {
	case string(models.SelectionTypePoolList):
		if len(params.PoolIDs) == 0 {
			return customerrors.NewBadRequestErr("poolIds must contain at least one pool when selectionType is poolList")
		}
	case string(models.SelectionTypePercentage):
		if params.Percentage < 1 || params.Percentage > 100 {
			return customerrors.NewBadRequestErr("percentage must be between 1 and 100 when selectionType is percentage")
		}
	default:
		return customerrors.NewBadRequestErr("selectionType must be either percentage or poolList")
	}

	return nil
}

// SelectClustersForBatchUpgrade selects clusters for batch upgrade based on the provided criteria.
// It queries the database, applies filters, then applies selection logic (percentage or pool list).
func SelectClustersForBatchUpgrade(ctx context.Context, se database.Storage, params *commonparams.BatchUpgradeClusterParams) ([]models.SelectedCluster, error) {
	if err := ValidateBatchClusterSelectionParams(params); err != nil {
		return nil, err
	}

	switch params.SelectionType {
	case string(models.SelectionTypePoolList):
		return selectByPoolList(ctx, se, params.PoolIDs)
	case string(models.SelectionTypePercentage):
		return selectByPercentage(ctx, se, params)
	default:
		return nil, customerrors.NewBadRequestErr("selectionType must be either percentage or poolList")
	}
}

// selectByPoolList selects clusters by explicit pool IDs
func selectByPoolList(ctx context.Context, se database.Storage, poolIDs []string) ([]models.SelectedCluster, error) {
	logger := util.GetLogger(ctx)

	pools, err := se.GetPoolsByUUIDs(ctx, poolIDs)
	if err != nil {
		logger.Error("Failed to retrieve pools for batch upgrade selection", "error", err)
		return nil, err
	}

	poolsByUUID := make(map[string]*datamodel.Pool, len(pools))
	for _, pool := range pools {
		if pool != nil {
			poolsByUUID[pool.UUID] = pool
		}
	}

	var selected []models.SelectedCluster
	for _, poolID := range poolIDs {
		pool := poolsByUUID[poolID]
		if pool == nil {
			logger.Warn("Pool not found for batch upgrade, skipping", "poolId", poolID)
			continue
		}

		if !commonparams.IsPoolStateEligibleForClusterUpgrade(pool.State) {
			logger.Warn("Pool is not in a valid state for upgrade, skipping", "poolId", poolID, "state", pool.State)
			continue
		}

		currentVersion := ""
		if pool.BuildInfo != nil {
			currentVersion = pool.BuildInfo.OntapVersion
		} else {
			currentVersion = env.CurrentOntapVersionDetails
		}

		selected = append(selected, models.SelectedCluster{
			ClusterID:      pool.UUID,
			PoolID:         pool.UUID,
			CurrentVersion: currentVersion,
			Priority:       len(selected) + 1,
		})
	}

	return selected, nil
}

// selectByPercentage selects clusters by percentage of eligible clusters
func selectByPercentage(ctx context.Context, se database.Storage, params *commonparams.BatchUpgradeClusterParams) ([]models.SelectedCluster, error) {
	logger := util.GetLogger(ctx)

	filter := buildClusterFilter(params.Filters)
	pools, err := se.ListPoolsSelective(ctx, filter, database.PoolPreloadOptions{})
	if err != nil {
		logger.Error("Failed to list pools for batch upgrade selection", "error", err)
		return nil, err
	}

	// Filter to only READY and DISABLED pools
	var eligiblePools []*datamodel.PoolView
	for _, pool := range pools {
		if commonparams.IsPoolStateEligibleForClusterUpgrade(pool.State) {
			eligiblePools = append(eligiblePools, pool)
		}
	}

	// Sort by creation date for deterministic selection
	sort.Slice(eligiblePools, func(i, j int) bool {
		return eligiblePools[i].CreatedAt.Before(eligiblePools[j].CreatedAt)
	})

	totalCount := len(eligiblePools)
	if totalCount == 0 {
		logger.Info("No eligible clusters found for batch upgrade")
		return nil, nil
	}

	selectedCount := (totalCount * params.Percentage) / 100
	if selectedCount == 0 && params.Percentage > 0 && totalCount > 0 {
		selectedCount = 1
	}

	if params.MaxClusters > 0 && selectedCount > params.MaxClusters {
		selectedCount = params.MaxClusters
	}

	if selectedCount > totalCount {
		selectedCount = totalCount
	}

	selected := make([]models.SelectedCluster, 0, selectedCount)
	for i := 0; i < selectedCount; i++ {
		pool := eligiblePools[i]
		currentVersion := ""
		if pool.BuildInfo != nil {
			currentVersion = pool.BuildInfo.OntapVersion
		} else {
			currentVersion = env.CurrentOntapVersionDetails
		}

		selected = append(selected, models.SelectedCluster{
			ClusterID:      pool.UUID,
			PoolID:         pool.UUID,
			CurrentVersion: currentVersion,
			Priority:       i + 1,
		})
	}

	logger.Info("Selected clusters for batch upgrade",
		"totalEligible", totalCount,
		"selectedCount", len(selected),
		"percentage", params.Percentage,
		"maxClusters", params.MaxClusters)

	return selected, nil
}

// buildClusterFilter builds a database filter from cluster selection filters
func buildClusterFilter(filters *commonparams.ClusterFilters) *dbutils.Filter {
	if filters == nil {
		return dbutils.CreateFilterWithConditions()
	}

	var conditions []*dbutils.FilterCondition

	if len(filters.Zones) > 0 {
		conditions = append(conditions, dbutils.NewFilterCondition("zone", "IN", filters.Zones))
	}

	if len(filters.OntapVersions) > 0 {
		conditions = append(conditions, dbutils.NewFilterCondition("build_info->>'ontapVersion'", "IN", filters.OntapVersions))
	}

	if len(filters.ClusterStates) > 0 {
		conditions = append(conditions, dbutils.NewFilterCondition("state", "IN", filters.ClusterStates))
	}

	return dbutils.CreateFilterWithConditions(conditions...)
}
