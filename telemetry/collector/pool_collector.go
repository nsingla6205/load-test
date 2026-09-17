package collector

import (
	"context"
	"fmt"
	"time"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	datamodel2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	BaseThroughputMibps = 64.0
	IopsFactor          = 16.0
)

// PoolMetricsResult holds the results from GetPoolMetrics operation
type PoolMetricsResult struct {
	// HydratedMetrics contains the traditional hydrated metrics
	HydratedMetrics []entity.HydratedMetric
	// HydratedMetricsDataModel contains the data model hydrated metrics
	HydratedMetricsDataModel []datamodel2.HydratedMetrics
	// PoolMetadataMap contains a map of pool IDs to its metadata
	PoolMetadataMap map[int64]metadata.ResourceMetadata
}

// GetPoolMetrics retrieves metrics for all pools from the database and returns them in a structured result.
func GetPoolMetrics(ctx context.Context, vcpDB database.Storage, config *common.TelemetryConfig, timestamp time.Time) (*PoolMetricsResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debug("Starting pool metrics collection")

	// Use optimized ListPoolsForMetrics which fetches only required fields
	pools, err := vcpDB.ListPoolsForMetrics(ctx)
	if err != nil {
		logger.Error("Failed to list pools for metrics", "error", err.Error())
		return &PoolMetricsResult{}, err
	}
	logger.Info(fmt.Sprintf("Found %d pools", len(pools)))

	if len(pools) == 0 {
		return &PoolMetricsResult{}, fmt.Errorf("no pools found from DB")
	}

	// Fetch all accounts and create a map of account name -> account state for efficient lookup
	accountStateMap := buildAccountStateMap(ctx, vcpDB)

	// Initialize a slice to hold the hydrated metrics
	var metrics []entity.HydratedMetric
	var hydratedMetrics []datamodel2.HydratedMetrics

	// Create a pool metadata map for all pools
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	expertModePoolIDs := make([]int64, 0)
	for _, pool := range pools {
		accountName := pool.GetAccountName()
		if accountName == "" || pool.PoolAttributes == nil || !isExpertModeMetricsPool(pool) {
			continue
		}
		if accountState, exists := accountStateMap[accountName]; exists && accountState == datamodel.AccountStateHyperscalerDisabled {
			continue
		}
		expertModePoolIDs = append(expertModePoolIDs, pool.ID)
	}

	expertModeCapacities := make(map[int64]*database.ExpertModePoolCapacity)
	var expertModeCapacityErr error
	if len(expertModePoolIDs) > 0 {
		expertModeCapacities, expertModeCapacityErr = vcpDB.GetExpertModePoolsUsedCapacityAndVolumeCount(ctx, expertModePoolIDs)
		if expertModeCapacityErr != nil {
			logger.Error("Failed to batch calculate expert mode pool allocated capacity; falling back to per-pool queries",
				"poolCount", len(expertModePoolIDs), "error", expertModeCapacityErr.Error())
		}
	}

	// Iterate over all pools and generate metrics
	for _, pool := range pools {
		accountName := pool.GetAccountName()
		if accountName == "" || pool.PoolAttributes == nil {
			logger.Warnf("Skipping pool %s (ID: %d) as it has no associated account or pool attributes", pool.Name, pool.ID)
			continue
		}
		// Skip metrics collection if account state is HYPERSCALERDISABLED
		if accountState, exists := accountStateMap[accountName]; exists && accountState == datamodel.AccountStateHyperscalerDisabled {
			logger.Warnf("Skipping pool %s (ID: %d) metrics collection as account %s is in HYPERSCALERDISABLED state", pool.Name, pool.ID, accountName)
			continue
		}
		// Assemble metadata for the pool
		poolMetadata := assemblePoolMetadata(pool, config)

		// Add to the pool metadata map
		poolMetadataMap[pool.ID] = poolMetadata

		allocatedUsed := float64(pool.QuotaInBytes)
		emitAllocatedUsed := true
		expertModeMetricsPool := isExpertModeMetricsPool(pool)
		if expertModeMetricsPool {
			capacity := expertModeCapacities[pool.ID]
			capacityErr := expertModeCapacityErr
			if expertModeCapacityErr != nil || capacity == nil {
				capacity, capacityErr = vcpDB.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID)
			}
			if capacityErr != nil {
				logger.Error("Failed to calculate expert mode pool allocated capacity; omitting AllocatedUsed metric",
					"poolID", pool.ID, "poolName", pool.Name, "error", capacityErr.Error())
				emitAllocatedUsed = false
			} else if capacity == nil {
				logger.Error("Expert mode pool allocated capacity result was nil; omitting AllocatedUsed metric",
					"poolID", pool.ID, "poolName", pool.Name)
				emitAllocatedUsed = false
			} else {
				allocatedUsed = float64(capacity.TotalSize)
			}
		}

		// Create metrics for the pool. Capacity always comes directly from the
		// pool record; only expert mode allocated capacity uses expert-mode volumes.
		type poolMetric struct {
			measureType metadata.MeasuredType
			value       float64
		}
		metricsToCollect := []poolMetric{
			{metadata.PoolAllocatedSize, float64(pool.SizeInBytes)},
		}
		if emitAllocatedUsed {
			metricsToCollect = append(metricsToCollect, poolMetric{metadata.AllocatedUsed, allocatedUsed})
		}
		if !expertModeMetricsPool {
			metricsToCollect = append(metricsToCollect,
				poolMetric{metadata.PoolTotalThroughputMibps, float64(pool.PoolAttributes.ThroughputMibps)},
				poolMetric{metadata.PoolTotalIops, float64(pool.PoolAttributes.Iops)},
			)
		}

		for _, m := range metricsToCollect {
			setupPoolMetric(&metrics, &hydratedMetrics, timestamp, poolMetadata, m.measureType, m.value, accountName)
		}

		// For auto-tiering enabled pools, collecting HotTierSizeInBytes
		if pool.AllowAutoTiering && pool.AutoTieringConfig != nil && pool.AutoTieringConfig.HotTierSizeInBytes > 0 {
			setupPoolMetricForBillingOnly(&hydratedMetrics, timestamp, poolMetadata,
				metadata.PoolHotTierProvisionedSize,
				float64(pool.AutoTieringConfig.HotTierSizeInBytes),
				accountName)
		}
	}

	// Return the structured result
	return &PoolMetricsResult{
		HydratedMetrics:          metrics,
		HydratedMetricsDataModel: hydratedMetrics,
		PoolMetadataMap:          poolMetadataMap,
	}, nil
}

// buildAccountStateMap fetches all accounts and creates a map of account name -> account state
// for efficient lookup. Returns an empty map if fetching fails (graceful degradation).
func buildAccountStateMap(ctx context.Context, vcpDB database.Storage) map[string]string {
	logger := util.GetLogger(ctx)
	accountStateMap := make(map[string]string)
	accountOffset := 0
	accountLimit := 1000 // Use reasonable page size

	for {
		pagination := &dbutils.Pagination{
			Offset: accountOffset,
			Limit:  accountLimit,
		}
		accounts, err := vcpDB.ListAccountsForTelemetry(ctx, pagination)
		if err != nil {
			logger.Warnf("Failed to fetch accounts for state check: %v, continuing without account state filtering", err)
			break
		}
		if len(accounts) == 0 {
			break
		}
		for _, account := range accounts {
			accountStateMap[account.Name] = account.State
		}
		accountOffset += accountLimit
		if len(accounts) < accountLimit {
			break
		}
	}
	logger.Debug(fmt.Sprintf("Fetched %d accounts for state filtering", len(accountStateMap)))
	return accountStateMap
}

// assemblePoolMetadata creates ResourceMetadata from the optimized PoolMetricsData structure
func assemblePoolMetadata(pool *database.PoolMetricsData, config *common.TelemetryConfig) metadata.ResourceMetadata {
	met := metadata.ResourceMetadata{}
	met.SetResourceUUID(pool.UUID)
	met.SetResourceName(pool.Name)
	met.SetResourceDisplayName(pool.Name)
	if pool.PoolAttributes.IsRegionalHA {
		met.SetResourceType(metadata.VolumePoolRegionalHA)
	} else {
		met.SetResourceType(metadata.VolumePool)
	}
	met.SetSizeInBytes(pool.SizeInBytes)
	met.SetRegionName(config.RegionName)
	met.SetAccountName(pool.GetAccountName())
	met.SetDeploymentName(pool.DeploymentName)
	met.SetThroughput(float64(pool.PoolAttributes.ThroughputMibps))
	met.SetResourceID(pool.ID)
	met.SetServiceLevel(pool.GetEffectiveServiceLevel())
	met.SetAPIAccessMode(pool.APIAccessMode)
	return met
}

func setupHydratedMetric(now time.Time, poolMetadata metadata.ResourceMetadata, metricType metadata.MeasuredType, value float64) entity.HydratedMetric {
	return entity.HydratedMetric{
		Timestamp:    entity.UnixNano(now.UnixNano()),
		Metadata:     poolMetadata,
		MeasuredType: metricType,
		Quantity:     value,
	}
}

func isExpertModeMetricsPool(pool *database.PoolMetricsData) bool {
	return pool != nil &&
		isExpertModeMetricsPoolValues(pool.APIAccessMode, pool.GetEffectiveServiceLevel())
}

func isExpertModeMetricsPoolValues(apiAccessMode, serviceLevel string) bool {
	return apiAccessMode == commonparams.ONTAPMode &&
		(serviceLevel == commonparams.ServiceLevelExtreme ||
			serviceLevel == commonparams.ServiceLevelPremium)
}

func setupHydratedMetricsDataModel(measuredType metadata.MeasuredType, resourceType metadata.ResourceType, projectID string, resourceMetadata metadata.ResourceMetadata, timestamp time.Time, quantity float64) *datamodel2.HydratedMetrics {
	logger := util.GetLogger(context.Background())

	if err := utils.ValidateResourceMetadata(resourceMetadata); err != nil {
		logger.Warn("Skipping metric", "error", err.Error())
		return nil
	}

	switch measuredType {
	case metadata.PoolTotalThroughputMibps:
		quantity = max(quantity-BaseThroughputMibps, 0)
		// Billable Throughput = Total Throughput set by the user - 64 (Minimum throughput included in the base price)
	case metadata.PoolTotalIops:
		if resourceMetadata.Throughput == nil {
			logger.Warn("Setting IOPS quantity to 0 due to nil Throughput")
			quantity = 0
		} else {
			throughput := *resourceMetadata.Throughput
			quantity = quantity - IopsFactor*throughput
			// Billable IOPS = Total IOPS set by the user - (16 * total throughput set by the user for the pool)
		}
	}
	return &datamodel2.HydratedMetrics{
		MetricTimestamp: timestamp,
		MeasuredType:    metadata.MeasuredTypeValue(measuredType),
		ConsumerID:      projectID,
		ResourceType:    metadata.ResourceTypeValue(resourceType),
		ResourceName:    *resourceMetadata.ResourceName,
		Location:        *resourceMetadata.RegionName,
		Quantity:        quantity,
		DeploymentName:  *resourceMetadata.DeploymentName,
	}
}

func setupPoolMetric(metrics *[]entity.HydratedMetric, hydratedMetrics *[]datamodel2.HydratedMetrics, timestamp time.Time, poolMetadata metadata.ResourceMetadata, measureType metadata.MeasuredType, value float64, accountName string) {
	metric := setupHydratedMetric(timestamp, poolMetadata, measureType, value)
	*metrics = append(*metrics, metric)
	if hydratedMetric := setupHydratedMetricsDataModel(metric.MeasuredType, metric.Metadata.ResourceType, accountName, poolMetadata, timestamp, value); hydratedMetric != nil {
		*hydratedMetrics = append(*hydratedMetrics, *hydratedMetric)
	}
}

// This is used for billing-only metrics that should not be sent to the performance sink (GoogleSink).
func setupPoolMetricForBillingOnly(hydratedMetrics *[]datamodel2.HydratedMetrics, timestamp time.Time, poolMetadata metadata.ResourceMetadata, measureType metadata.MeasuredType, value float64, accountName string) {
	if hydratedMetric := setupHydratedMetricsDataModel(measureType, poolMetadata.ResourceType, accountName, poolMetadata, timestamp, value); hydratedMetric != nil {
		*hydratedMetrics = append(*hydratedMetrics, *hydratedMetric)
	}
}
