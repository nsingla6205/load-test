package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	clientmodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	datamodel2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/jobs"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

const (
	ReplicationScheduleOntapMode = "ONTAP_MODE"
	TransferTypeInitial          = "initialize"
	TransferTypeUpdate           = "update"

	// backupModeOntap and backupModeDefault are the /backups/mode label values forwarded to Google.
	// They must stay in sync with the identical constants in telemetry/collector/backup_collector.go.
	backupModeOntap       = "ONTAP"
	backupModeDefault     = "DEFAULT"
	backupModeMetadataKey = "backup_mode"
)

var (
	unifiedServiceType = "unified"
)

type ResourceKey struct {
	ResourceType   metadata.ResourceType
	ResourceName   string
	DeploymentName string
	ConsumerID     string
}
type CounterAggregationCacheResourceKey struct {
	ResourceUUID string
	MeasuredType metadata.MeasuredType
}

// CounterAggregationCacheValue holds the last persisted counter sample for a
// (resource_uuid, measured_type) pair, along with the transfer_type seen at that sample.
// LastCounterValue mirrors AggregatedUsage.LastCounterValue and is guaranteed non-nil when
// the entry is in the cache (entries with nil LastCounterValue are skipped by
// fetchAndCacheCounterValues). LastTransferType mirrors AggregatedUsage.LastTransferType
// and is nil when the metric has no transfer_type semantics (e.g., non-replication metrics)
// or when the persisted row predates the column.
type CounterAggregationCacheValue struct {
	LastCounterValue *float64
	LastTransferType *string
}

// CounterAggregationCache is the in-memory cache keyed by (resource_uuid, measured_type).
// A nil entry means the key is not present.
type CounterAggregationCache = map[CounterAggregationCacheResourceKey]*CounterAggregationCacheValue

type Labels map[string]interface{}

type ResourceData struct {
	UUID                  string
	AccountID             int64
	Labels                Labels
	VolumeReplicationInfo *VolumeReplicationInfo
	AllowAutoTiering      bool
	LargeCapacity         bool   // Track if pool/volume is large capacity
	VolumeStyle           string // Track volume style (FLEXVOL/FLEXGROUP)
	HasOnlyBlockVolumes   bool
	IsONTAPMode           bool    // True if pool has APIAccessMode == "ONTAP" (expert mode)
	PrimaryZone           string  // Pool's primary zone for AT billing location label
	BackupRegionName      *string // Destination region for cross-region backups
	CreatedAt             *time.Time
	FreeTrialEndAt        *time.Time
}

type VolumeReplicationInfo struct {
	ReplicationName       *string
	ReplicationType       string
	ReplicationSchedule   string
	SourceLocation        *string
	DestinationVolumeUUID *string
	DestinationLocation   *string
}

type ResourceCollection struct {
	PoolData                 map[ResourceKey]ResourceData
	VolumeData               map[ResourceKey]ResourceData
	BackupData               map[ResourceKey]ResourceData
	VolumeReplicationData    map[ResourceKey]ResourceData
	VolumeToDeploymentName   map[string]string
	DeploymentNameToPoolName map[string]string
	FreeTrialAccounts        map[int64]*time.Time
}

type OntapPoolInfo struct {
	UUID        string
	AccountID   int64
	AccountName string
	PrimaryZone string
}

type BillingProvider struct {
	metricsDB    database2.Storage
	vcpDataStore database.Storage
	config       *common.TelemetryConfig
	usageSink    common.UsageSink
	jobQueue     *utils.JobQueue
}

func NewBillingProvider(db database2.Storage, vcpDB database.Storage, config *common.TelemetryConfig, usageSink common.UsageSink) *BillingProvider {
	return &BillingProvider{
		metricsDB:    db,
		vcpDataStore: vcpDB,
		config:       config,
		usageSink:    usageSink,
	}
}

// GetUnsentGoogleUsages retrieves unsent Google usage records within the aggregation time window
func (p *BillingProvider) GetUnsentGoogleUsages(ctx context.Context, maxRetries int64, aggregationEndTime time.Time) ([]datamodel2.AggregatedUsage, error) {
	return p.getUnsentGoogleUsages(ctx, maxRetries, aggregationEndTime)
}

// GetUsageSink returns the usage sink for delivering metrics
func (p *BillingProvider) GetUsageSink() common.UsageSink {
	return p.usageSink
}

func (p *BillingProvider) SetJobQueue(q *utils.JobQueue) {
	p.jobQueue = q
}

func (p *BillingProvider) GetJobQueue() *utils.JobQueue {
	return p.jobQueue
}

// ProcessBillingMetrics processes raw metrics from cvt_metrics table and aggregates them
func (p *BillingProvider) ProcessBillingMetrics(ctx context.Context, aggregationEndTime time.Time) error {
	startTime := time.Now()
	logger := util.GetLogger(ctx)
	var aggregatedRecords []datamodel2.AggregatedUsage
	aggregationStartTime := aggregationEndTime.Add(-1 * time.Hour)
	logger.Infof("Processing metrics from %v to %v", aggregationStartTime, aggregationEndTime)

	// Fetch label values from VCP database at the start of each aggregator cycle
	resourceCollection, err := p.fetchResourceData(ctx, aggregationStartTime)
	if err != nil {
		logger.Errorf("Failed to fetch resource data: %v", err)
	}

	// Populate BackfillLimit for all DefaultAggregationJobDefinitions based on config
	p.populateBackfillLimit(logger)
	p.applyDataSourceAndFormatterOverrides(logger)

	// Pre-fetch all counter values for optimization
	counterCache, cacheErr := p.preloadCounterValues(ctx, aggregationStartTime, aggregationEndTime, logger)
	if cacheErr != nil {
		logger.Warnf("Failed to preload counter values : %v", cacheErr)
		counterCache = make(CounterAggregationCache) // Initialize empty cache
	}
	logger.Debugf("Counter cache loaded with %d entries: %v", len(counterCache), counterCache)

	// Process job definitions in two passes:
	// Pass 1: all jobs except pool-level AT aggregation (which depends on volume-level records in aggregatedRecords)
	// Pass 2: pool-level AT aggregation only (volume records now available in aggregatedRecords)
	for pass := 0; pass < 2; pass++ {
		for key, jobDef := range common.DefaultAggregationJobDefinitions {
			isPoolLevelATJob := p.config.EnableATVolumeBasedPoolBilling &&
				isPoolResourceType(key.ResourceType) &&
				(key.MeasuredType == metadata.CoolTierDataReadSizeRaw || key.MeasuredType == metadata.CoolTierDataWriteSizeRaw)

			if (pass == 0 && isPoolLevelATJob) || (pass == 1 && !isPoolLevelATJob) {
				continue
			}

			// Skip auto-tiering billing metrics if disabled
			if !p.config.EnableAutoTieringBillingMetrics && isAutoTieringBillingMetric(key.MeasuredType) {
				continue
			}

			var metrics []datamodel2.HydratedMetrics
			var err error

			if jobDef.AggregationType == common.IntegralAggregation || jobDef.AggregationType == common.CounterAggregation {
				// For counter aggregation and integral aggregation, we need:
				// 1. All records from current aggregation window
				// 2. Only the latest record from previous period (closest to aggregation start)
				// 3. Only the earliest record from next period (closest to aggregation end)
				if p.config.EnableBackupHistoryFormatter && key.ResourceType == metadata.Backup && key.MeasuredType == metadata.BackupLogicalSize {
					metrics, err = p.fetchBackupHistoryMetrics(ctx, aggregationStartTime, aggregationEndTime, jobDef.TimeSeriesFormatter.GetBackfillLimit(), resourceCollection)
				} else {
					metrics, err = p.fetchMetricsForCounterAndIntegralAggregation(ctx, aggregationStartTime, aggregationEndTime, key.ResourceType.String(), key.MeasuredType.String(), jobDef.TimeSeriesFormatter.GetBackfillLimit())
				}

				if err != nil {
					logger.Error("Failed to list metrics from source", "error", err.Error())
					continue
				}
			} else {
				if p.config.EnableATVolumeBasedPoolBilling && (key.ResourceType == metadata.VolumePool || key.ResourceType == metadata.VolumePoolRegionalHA) && (key.MeasuredType == metadata.CoolTierDataReadSizeRaw || key.MeasuredType == metadata.CoolTierDataWriteSizeRaw) {
					metrics, _ = p.fetchHistoricalVolumeSizeMetrics(ctx, aggregationStartTime, aggregationEndTime, jobDef.TimeSeriesFormatter.GetBackfillLimit(), key.MeasuredType, key.ResourceType, resourceCollection, aggregatedRecords)
				} else {
					// For Sum/First aggregation, fetch only records within the exact aggregation window
					metrics, err = p.fetchMetricsWithinWindow(ctx, aggregationStartTime, aggregationEndTime, key.ResourceType.String(), key.MeasuredType.String())
					if err != nil {
						logger.Error("Failed to list hydrated metrics", "error", err.Error())
						continue
					}
				}
			}
			logger.Debugf("Fetched %d metrics for aggregation - ResourceType: %s, MeasuredType: %s",
				len(metrics), key.ResourceType.String(), key.MeasuredType.String())
			// Group metrics by resource
			resourceGroups := p.groupMetricsByResource(metrics)

			// Process each resource group
			for resourceIdentifier, resourceMetrics := range resourceGroups {
				// Skip auto-tiering billing metrics for pools that don't meet criteria
				if isAutoTieringBillingMetric(key.MeasuredType) && isPoolResourceType(key.ResourceType) {
					if shouldSkip, reason := p.shouldSkipAutoTieringMetric(resourceIdentifier, resourceCollection, key.MeasuredType); shouldSkip {
						logger.Debugf("Skipping auto-tiering metric %s for pool %s - %s",
							key.MeasuredType, resourceIdentifier.ResourceName, reason)
						continue
					}
				}

				// Inject metricsDB into CounterMetricsFormatter if applicable
				if counterFormatter, ok := jobDef.TimeSeriesFormatter.(*common.CounterMetricsFormatter); ok {
					counterFormatter.MetricsDB = p.metricsDB
				}

				// Format the raw metrics into time series using the job definition's formatter.
				// The formatter groups metrics by metadata changes and applies trimming logic based on
				// aggregation type (Counter/Integral/etc). For counter metrics, it includes the last
				// datapoint from the previous period for delta calculation. For integral metrics, it
				// may include the first datapoint from the next period. Returns a slice of TimeSeries,
				// where each TimeSeries represents a continuous period with consistent metadata.
				resourceData := p.getResourceDataForAggregationUsage(resourceIdentifier, resourceIdentifier.ResourceType, resourceCollection)
				var resourceCreatedAt *time.Time
				if resourceData != nil && resourceData.CreatedAt != nil && !resourceData.CreatedAt.IsZero() {
					resourceCreatedAt = resourceData.CreatedAt
				}

				// Set createdAt on counter formatter without mutating the metrics slice
				if counterFormatter, ok := jobDef.TimeSeriesFormatter.(*common.CounterMetricsFormatter); ok {
					counterFormatter.CurrentCreatedAt = resourceCreatedAt
				}

				series := jobDef.TimeSeriesFormatter.Format(ctx, logger, resourceMetrics, aggregationStartTime, aggregationEndTime)
				if p.config.EnableFreeTrialBilling {
					series = applyFreeTrialSplit(series, resourceData, jobDef.AggregationType)
				}
				// loop through each series and process metrics
				for _, metricseries := range series {
					logger.Debugf("Collected timeseries %s, %s, %v for resource %s and customer id %s ", metricseries.AggregationStart, metricseries.AggregationEnd, metricseries.DataPoints, resourceIdentifier.ResourceName, resourceIdentifier.ConsumerID)
					if err := p.processMetricsWithJobDef(ctx, resourceIdentifier, metricseries, jobDef, metricseries.AggregationStart, metricseries.AggregationEnd, resourceCollection, &aggregatedRecords, counterCache, logger); err != nil {
						logger.Errorf("Failed to process metrics for resource %s and customer id %s : %v", resourceIdentifier.ResourceName, resourceIdentifier.ConsumerID, err)
						continue
					}
				}
			}
		}
	}

	// Batch save all aggregated usage records. Invalid records are filtered out
	// and logged rather than aborting the whole batch,
	validRecords, invalidRecords := filterValidAggregatedUsage(aggregatedRecords)
	if len(invalidRecords) > 0 {
		logger.Errorf("Dropping %d invalid aggregated usage records before persist", len(invalidRecords))
	}
	aggregatedRecords = validRecords
	if len(aggregatedRecords) > 0 {
		batchSize := p.config.PushBatchSize

		err := p.metricsDB.CreateAggregatedUsageBatch(ctx, aggregatedRecords, int(batchSize))
		if err != nil {
			logger.Errorf("Failed to batch save aggregated usage records: %v", err)
			return err
		}
		logger.Infof("Successfully saved %d aggregated usage records in batches of %d", len(aggregatedRecords), batchSize)
	}

	// Deliver all aggregated metrics at the end
	if len(aggregatedRecords) > 0 && p.jobQueue != nil {
		j := jobs.NewDeliverBillingMetrics(aggregationEndTime)
		err = p.jobQueue.Enqueue(ctx, j, utils.BillingRetryQueue)
		if err != nil {
			logger.Errorf("Failed to enqueue BillingRetry jobs: %v", err)
			return err
		}
	}

	elapsed := time.Since(startTime)
	logger.Infof("ProcessBillingMetrics completed successfully in %v (%.2f seconds). Processed %d aggregated records",
		elapsed, elapsed.Seconds(), len(aggregatedRecords))

	return nil
}

// populateBackfillLimit sets the BackfillLimit for all job definitions based on config
func (p *BillingProvider) populateBackfillLimit(logger log.Logger) {
	for key, jobDef := range common.DefaultAggregationJobDefinitions {
		// Set BackfillLimit based on aggregation type
		if jobDef.AggregationType == common.IntegralAggregation {
			jobDef.TimeSeriesFormatter.SetBackfillLimit(time.Duration(p.config.IntervalBackfillLimitMinutes) * time.Minute)
			logger.Debugf("Set BackfillLimit to %v for IntegralAggregation: %s/%s",
				jobDef.TimeSeriesFormatter.GetBackfillLimit(), key.ResourceType, key.MeasuredType)
		} else if jobDef.AggregationType == common.CounterAggregation {
			jobDef.TimeSeriesFormatter.SetBackfillLimit(time.Duration(p.config.CounterBackfillLimitMinutes) * time.Minute)
			logger.Debugf("Set BackfillLimit to %v for CounterAggregation: %s/%s",
				jobDef.TimeSeriesFormatter.GetBackfillLimit(), key.ResourceType, key.MeasuredType)
		}
	}
}

// applyDataSourceOverrides switches job data sources based on runtime config.
func (p *BillingProvider) applyDataSourceAndFormatterOverrides(logger log.Logger) {
	for key, jobDef := range common.DefaultAggregationJobDefinitions {
		if key.ResourceType == metadata.Backup && key.MeasuredType == metadata.BackupLogicalSize && p.config.EnableBackupHistoryFormatter {
			backfillLimit := jobDef.TimeSeriesFormatter.GetBackfillLimit()
			jobDef.TimeSeriesFormatter = &common.HistoricalMetricsFormatter{
				BackfillLimit: backfillLimit,
			}
			if logger != nil {
				logger.Debugf("Enabled backup history formatter for %s/%s", key.ResourceType, key.MeasuredType)
			}
			common.DefaultAggregationJobDefinitions[key] = jobDef
		}

		if key.ResourceType == metadata.VolumeReplicationRelationship && key.MeasuredType == metadata.XregionReplicationTotalTransferBytes && p.config.EnableCounterFormatter {
			backfillLimit := jobDef.TimeSeriesFormatter.GetBackfillLimit()
			jobDef.TimeSeriesFormatter = &common.CounterMetricsFormatter{
				BackfillLimit: backfillLimit,
				Config:        p.config,
			}
			if logger != nil {
				logger.Debugf("Enabled Counter formatter for %s/%s", key.ResourceType, key.MeasuredType)
			}
			common.DefaultAggregationJobDefinitions[key] = jobDef
		}
		// Override aggregation type for pool-level auto-tiering metrics when EnableATVolumeBasedPoolBilling is enabled
		if p.config.EnableATVolumeBasedPoolBilling &&
			(key.ResourceType == metadata.VolumePool || key.ResourceType == metadata.VolumePoolRegionalHA) &&
			(key.MeasuredType == metadata.CoolTierDataReadSizeRaw || key.MeasuredType == metadata.CoolTierDataWriteSizeRaw) {
			// Change to SumAggregation for pool-level metrics
			jobDef.AggregationType = common.SumAggregation
			// Use appropriate formatter for Sum aggregation
			jobDef.TimeSeriesFormatter = &common.SampledMetricsFormatter{
				Mode:          common.Point,
				BackfillLimit: 60 * time.Minute,
			}
			if logger != nil {
				logger.Debugf("Overridden aggregation type to SumAggregation for pool-level metric %s/%s", key.ResourceType, key.MeasuredType)
			}
			common.DefaultAggregationJobDefinitions[key] = jobDef
		}
	}
}

// fetchFreeTrialAccounts loads accounts that have both trial start and end set (see ListFreeTrialAccountsForBilling).
func (p *BillingProvider) fetchFreeTrialAccounts(ctx context.Context) (map[int64]*time.Time, error) {
	logger := util.GetLogger(ctx)
	out, err := p.vcpDataStore.ListFreeTrialAccountsForBilling(ctx)
	if err != nil {
		return nil, err
	}
	logger.Infof("Pre-fetched %d accounts in free trial", len(out))
	return out, nil
}

// fetchResourceData fetches label values from pool and volume tables in VCP database
func (p *BillingProvider) fetchResourceData(ctx context.Context, aggregationStartTime time.Time) (*ResourceCollection, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Fetching resource data from VCP database")

	// Create a new ResourceCollection for this aggregation cycle
	resourceCollection := &ResourceCollection{
		PoolData:                 make(map[ResourceKey]ResourceData),
		VolumeData:               make(map[ResourceKey]ResourceData),
		VolumeReplicationData:    make(map[ResourceKey]ResourceData),
		BackupData:               make(map[ResourceKey]ResourceData),
		VolumeToDeploymentName:   make(map[string]string),
		DeploymentNameToPoolName: make(map[string]string),
		FreeTrialAccounts:        make(map[int64]*time.Time),
	}

	if p.config.EnableFreeTrialBilling {
		freeTrialAccounts, err := p.fetchFreeTrialAccounts(ctx)
		if err != nil {
			logger.Warnf("Failed to fetch accounts for free trial prefetch: %v", err)
			freeTrialAccounts = make(map[int64]*time.Time)
		}
		resourceCollection.FreeTrialAccounts = freeTrialAccounts
	}

	var poolsDataError, volumeDataError, backupDataError, volumeReplicationDataError error

	// Fetch volume labels
	if err := p.fetchVolumeData(ctx, aggregationStartTime, resourceCollection); err != nil {
		logger.Errorf("Failed to fetch volume labels: %v", err)
		volumeDataError = err
	}

	// Fetch expert mode volume resource data so that BackupEnabledVolumeAllocatedSize metrics
	// emitted for expert mode volumes can be resolved during aggregation.
	if p.config.EnableExpertModeBackupBilling {
		if err := p.fetchExpertModeVolumeData(ctx, resourceCollection); err != nil {
			logger.Errorf("Failed to fetch expert mode volume resource data: %v", err)
		}
	}

	// Fetch pool labels
	if err := p.fetchPoolData(ctx, aggregationStartTime, resourceCollection); err != nil {
		logger.Errorf("Failed to fetch pool resource data: %v", err)
		poolsDataError = err
	}

	// Fetch backup data only if backup billing is enabled
	if p.config.EnableBackupBillingMetrics {
		if err := p.fetchBackupData(ctx, aggregationStartTime, resourceCollection); err != nil {
			logger.Errorf("Failed to fetch backup data: %v", err)
			backupDataError = err
		}
	}

	if p.config.EnableReplicationBillingMetrics {
		if err := p.fetchVolumeReplicationData(ctx, aggregationStartTime, resourceCollection); err != nil {
			logger.Errorf("Failed to fetch volume replication labels: %v", err)
			volumeReplicationDataError = err
		}
	}

	if p.config.EnableOntapModeReplicationBilling {
		if err := p.fetchOntapModePoolData(ctx, aggregationStartTime, resourceCollection); err != nil {
			logger.Errorf("Failed to fetch ONTAP mode pool data: %v", err)
		}
	}

	poolCount := len(resourceCollection.PoolData)
	volumeCount := len(resourceCollection.VolumeData)
	backupCount := len(resourceCollection.BackupData)
	volumeReplicationCount := len(resourceCollection.VolumeReplicationData)

	if poolsDataError != nil && volumeDataError != nil {
		logger.Errorf("Failed to fetch both pool and volume resource data. Pool error: %v, Volume error: %v, Backup error: %v", poolsDataError, volumeDataError, backupDataError)
		return nil, fmt.Errorf("failed to fetch any resource data")
	} else if poolsDataError != nil {
		logger.Warnf("Failed to fetch pool resource data: %v, but successfully fetched %d volume resource data ", poolsDataError, volumeCount)
	} else if volumeDataError != nil {
		logger.Warnf("Failed to fetch volume resource data: %v, but successfully fetched %d pool resource data", volumeDataError, poolCount)
	} else if backupDataError != nil {
		logger.Warnf("Failed to fetch backup resource data: %v", backupDataError)
	} else if volumeReplicationDataError != nil {
		logger.Warnf("Failed to fetch volume replication resource data: %v, but successfully fetched %d pool and %d volume resource data", volumeReplicationDataError, poolCount, volumeCount)
	} else {
		logger.Infof("Successfully fetched resource data for %d pools, %d volumes, %d backups, %d volume replication", poolCount, volumeCount, backupCount, volumeReplicationCount)
	}

	return resourceCollection, nil
}

// fetchPoolData fetches labels from pool table using pagination with optimized query
func (p *BillingProvider) fetchPoolData(ctx context.Context, aggregationStartTime time.Time, resourceCollection *ResourceCollection) error {
	logger := util.GetLogger(ctx)

	// Only fetch block-only pool IDs if:
	// 1. Auto-tiering billing is enabled (EnableAutoTieringBillingMetrics)
	// 2. Files auto-tiering billing is disabled (EnableFilesAutoTieringBilling = false)
	var blockOnlyPoolIDs map[int64]bool
	if p.config.EnableAutoTieringBillingMetrics && !p.config.EnableFilesAutoTieringBilling {
		var err error
		blockOnlyPoolIDs, err = p.vcpDataStore.GetBlockOnlyPoolIDs(ctx)
		if err != nil {
			logger.Warnf("Failed to get block-only pool IDs: %v", err)
			// Safe default: empty map means skip billing for all pools when files billing is disabled
			blockOnlyPoolIDs = make(map[int64]bool)
		}
		logger.Debugf("Fetched %d block-only pools", len(blockOnlyPoolIDs))
	} else {
		// Skip query: either auto-tiering billing is disabled, or files billing is enabled (all pools pass)
		blockOnlyPoolIDs = make(map[int64]bool)
	}

	offset := 0
	// Use configurable limit from config
	limit := p.config.PoolVolumeLabelPageSize
	totalProcessed := 0
	batchCount := 0
	aggregationEndTime := time.Now()

	for {
		// Create pagination with offset and limit
		pagination := &dbutils.Pagination{
			Offset: offset,
			Limit:  limit,
		}

		// Fetch paginated pools using optimized ListPoolsForResourceData
		// Use time.Now() for deleted_at upper bound to include recently deleted pools
		pools, err := p.vcpDataStore.ListPoolsForResourceData(ctx, aggregationStartTime, aggregationEndTime, pagination)
		if err != nil {
			return fmt.Errorf("failed to list pools (offset %d): %w", offset, err)
		}

		// Break if no records returned
		if len(pools) == 0 {
			break
		}

		// Process current batch
		for _, pool := range pools {
			// Skip pools with empty account name
			accountName := pool.GetAccountName()
			if accountName == "" {
				logger.Warnf("Skipping pool %s (%s) due to missing account name", pool.Name, pool.UUID)
				continue
			}

			// Extract and limit labels
			var limitedLabels Labels
			if labels := pool.GetLabels(); labels != nil {
				limitedLabels = p.limitLabels(labels)
			} else {
				limitedLabels = make(Labels)
			}

			primaryZone := ""
			if pool.PoolAttributes != nil {
				primaryZone = pool.PoolAttributes.PrimaryZone
			}
			var freeTrialEnd *time.Time
			if p.config.EnableFreeTrialBilling {
				freeTrialEnd = resourceCollection.FreeTrialAccounts[pool.AccountID]
			}
			poolResourceData := ResourceData{
				UUID:                pool.UUID,
				AccountID:           pool.AccountID,
				Labels:              limitedLabels,
				AllowAutoTiering:    pool.AllowAutoTiering,
				LargeCapacity:       pool.LargeCapacity,
				VolumeStyle:         "",                        // Empty for pools
				HasOnlyBlockVolumes: blockOnlyPoolIDs[pool.ID], // Set based on block-only pool IDs map
				IsONTAPMode:         pool.APIAccessMode == commonparams.ONTAPMode,
				PrimaryZone:         primaryZone,
				CreatedAt:           &pool.CreatedAt,
				FreeTrialEndAt:      freeTrialEnd,
			}
			resourceType := metadata.VolumePool
			if pool.IsRegionalHA() {
				resourceType = metadata.VolumePoolRegionalHA
			}
			id := ResourceKey{
				ResourceType:   resourceType,
				ResourceName:   pool.Name,
				DeploymentName: pool.DeploymentName,
				ConsumerID:     accountName,
			}
			resourceCollection.PoolData[id] = poolResourceData
			resourceCollection.DeploymentNameToPoolName[pool.DeploymentName] = pool.Name
		}

		totalProcessed += len(pools)
		batchCount++
		logger.Debugf("Processed %d pools in batch %d (offset: %d, total: %d)", len(pools), batchCount, offset, totalProcessed)

		// Update offset for next iteration
		offset += limit
	}

	logger.Infof("Fetched resource data for %d pools in %d batches", len(resourceCollection.PoolData), batchCount)
	return nil
}

// addVolumeDataEntry registers volume resource data under the volume-owner ConsumerID.
// For GCBDR (cross-project backup vault) volumes, also registers an alias under the vault
// owner's account so BMF metrics billed to the vault project resolve during aggregation.
// Free trial is preserved on the volume-owner entry (capacity, throughput, etc.) and cleared
// only on the vault alias so GCBDR backup billing (BMF or otherwise) stays commercial.
func addVolumeDataEntry(
	resourceCollection *ResourceCollection,
	resourceType metadata.ResourceType,
	volumeUUID, volumeName, deploymentName, volumeAccountName string,
	resourceData ResourceData,
	backupVaultServiceType, backupVaultAccountName string,
	backupVaultAccountID int64,
) {
	isGCBDR := backupVaultServiceType == datamodel.ServiceTypeCrossProject

	ownerKey := ResourceKey{
		ResourceType:   resourceType,
		ResourceName:   volumeName,
		DeploymentName: deploymentName,
		ConsumerID:     volumeAccountName,
	}
	resourceCollection.VolumeData[ownerKey] = resourceData
	resourceCollection.VolumeToDeploymentName[volumeUUID] = deploymentName

	if isGCBDR && backupVaultAccountName != "" && backupVaultAccountName != volumeAccountName {
		vaultKey := ResourceKey{
			ResourceType:   resourceType,
			ResourceName:   volumeName,
			DeploymentName: deploymentName,
			ConsumerID:     backupVaultAccountName,
		}
		vaultResourceData := resourceData
		vaultResourceData.FreeTrialEndAt = nil
		if backupVaultAccountID > 0 {
			vaultResourceData.AccountID = backupVaultAccountID
		}
		resourceCollection.VolumeData[vaultKey] = vaultResourceData
	}
}

// addBackupDataEntry registers backup resource data under the volume-owner ConsumerID.
// For GCBDR (cross-project backup vault) backups the collector attributes BackupLogicalSize to
// the vault owner, so an alias under the vault owner's account is registered as well. Without it
// the hydrated metric finds no resource data and is dropped before it reaches aggregated usage.
// Free trial is cleared on the vault alias so GCBDR backup billing stays commercial.
func addBackupDataEntry(
	logger log.Logger,
	resourceCollection *ResourceCollection,
	volumeUUID, deploymentName, volumeAccountName string,
	resourceData ResourceData,
	backupVaultServiceType, backupVaultAccountName string,
) {
	ownerKey := ResourceKey{
		ResourceType:   metadata.Backup,
		ResourceName:   volumeUUID,
		DeploymentName: deploymentName,
		ConsumerID:     volumeAccountName,
	}
	resourceCollection.BackupData[ownerKey] = resourceData

	isGCBDR := backupVaultServiceType == datamodel.ServiceTypeCrossProject
	if isGCBDR && backupVaultAccountName == "" {
		logger.Warnf("GCBDR backup %s (vault %s) missing vault account name; vault-owner resource alias not registered — metrics billed to the vault project may be dropped",
			volumeUUID, deploymentName)
		return
	}
	if isGCBDR && backupVaultAccountName != volumeAccountName {
		vaultKey := ownerKey
		vaultKey.ConsumerID = backupVaultAccountName
		vaultResourceData := resourceData
		vaultResourceData.FreeTrialEndAt = nil
		resourceCollection.BackupData[vaultKey] = vaultResourceData
	}
}

// fetchVolumeData fetches labels from volume table using pagination with optimized query
func (p *BillingProvider) fetchVolumeData(ctx context.Context, aggregationStartTime time.Time, resourceCollection *ResourceCollection) error {
	logger := util.GetLogger(ctx)

	offset := 0
	// Use configurable limit from config
	limit := p.config.PoolVolumeLabelPageSize
	totalProcessed := 0
	batchCount := 0
	aggregationEndTime := time.Now()

	for {
		// Create pagination with offset and limit
		pagination := &dbutils.Pagination{
			Offset: offset,
			Limit:  limit,
		}

		// Fetch paginated volumes using optimized ListVolumesForResourceData
		// Use time.Now() for deleted_at upper bound to include recently deleted volumes
		volumes, err := p.vcpDataStore.ListVolumesForResourceData(ctx, aggregationStartTime, aggregationEndTime, pagination)
		if err != nil {
			return fmt.Errorf("failed to list volumes (offset %d): %w", offset, err)
		}

		// Break if no records returned
		if len(volumes) == 0 {
			break
		}

		// Process current batch
		for _, volume := range volumes {
			// Skip volumes with missing account name or deployment name
			accountName := volume.GetAccountName()
			deploymentName := volume.GetDeploymentName()
			if accountName == "" {
				logger.Warnf("Skipping volume %s (%s) due to missing account name", volume.Name, volume.UUID)
				continue
			}
			if deploymentName == "" {
				logger.Warnf("Skipping volume %s (%s) due to missing deployment name", volume.Name, volume.UUID)
				continue
			}

			// Extract and limit labels
			var limitedLabels Labels
			if labels := volume.GetLabels(); labels != nil {
				limitedLabels = p.limitLabels(labels)
			} else {
				limitedLabels = make(Labels)
			}

			largeCapacity := volume.GetLargeCapacity()
			var freeTrialEnd *time.Time
			if p.config.EnableFreeTrialBilling {
				freeTrialEnd = resourceCollection.FreeTrialAccounts[volume.AccountID]
			}
			volumeResourceData := ResourceData{
				UUID:           volume.UUID,
				AccountID:      volume.AccountID,
				Labels:         limitedLabels,
				LargeCapacity:  largeCapacity,
				VolumeStyle:    getVolumeStyle(largeCapacity),
				CreatedAt:      &volume.CreatedAt,
				FreeTrialEndAt: freeTrialEnd,
			}
			resourceType := metadata.Volume
			if volume.IsRegionalHA() {
				resourceType = metadata.VolumeRegionalHA
			}
			addVolumeDataEntry(
				resourceCollection,
				resourceType,
				volume.UUID,
				volume.Name,
				deploymentName,
				accountName,
				volumeResourceData,
				volume.GetBackupVaultServiceType(),
				volume.GetBackupVaultAccountName(),
				volume.GetBackupVaultAccountID(),
			)
		}

		totalProcessed += len(volumes)
		batchCount++
		logger.Debugf("Processed %d volumes in batch %d (offset: %d, total: %d)", len(volumes), batchCount, offset, totalProcessed)

		// Update offset for next iteration
		offset += limit
	}

	logger.Infof("Fetched resource data for %d volumes in %d batches", len(resourceCollection.VolumeData), batchCount)
	return nil
}

// fetchExpertModeVolumeData populates resourceCollection.VolumeData with entries for expert mode
// volumes so that BackupEnabledVolumeAllocatedSize metrics emitted by the collector can be
// resolved during aggregation. Without this, processMetricsWithJobDef finds no resource data and
// silently drops every expert-mode backup billing metric.
func (p *BillingProvider) fetchExpertModeVolumeData(ctx context.Context, resourceCollection *ResourceCollection) error {
	logger := util.GetLogger(ctx)

	offset := 0
	limit := p.config.PoolVolumeLabelPageSize
	totalProcessed := 0
	batchCount := 0

	for {
		pagination := &dbutils.Pagination{
			Offset: offset,
			Limit:  limit,
		}

		volumes, err := p.vcpDataStore.ListExpertModeVolumesForTelemetryMetrics(ctx, pagination)
		if err != nil {
			return fmt.Errorf("failed to list expert mode volumes for resource data (offset %d): %w", offset, err)
		}

		if len(volumes) == 0 {
			break
		}

		for _, volume := range volumes {
			if volume.AccountName == "" {
				logger.Warnf("Skipping expert mode volume %s due to missing account name", volume.UUID)
				continue
			}
			if volume.DeploymentName == "" {
				logger.Warnf("Skipping expert mode volume %s due to missing deployment name", volume.UUID)
				continue
			}

			resourceType := metadata.Volume
			if volume.PoolIsRegionalHA {
				resourceType = metadata.VolumeRegionalHA
			}

			var freeTrialEnd *time.Time
			if p.config.EnableFreeTrialBilling {
				freeTrialEnd = resourceCollection.FreeTrialAccounts[volume.AccountID]
			}
			resourceData := ResourceData{
				UUID:           volume.UUID,
				AccountID:      volume.AccountID,
				FreeTrialEndAt: freeTrialEnd,
			}
			addVolumeDataEntry(
				resourceCollection,
				resourceType,
				volume.UUID,
				volume.Name,
				volume.DeploymentName,
				volume.AccountName,
				resourceData,
				volume.BackupVaultServiceType,
				volume.BackupVaultAccountName,
				volume.BackupVaultAccountID,
			)
		}

		totalProcessed += len(volumes)
		batchCount++
		logger.Debugf("Processed %d expert mode volumes in batch %d (offset: %d, total: %d)", len(volumes), batchCount, offset, totalProcessed)

		offset += limit
	}

	logger.Infof("Fetched resource data for %d expert mode volumes in %d batches", totalProcessed, batchCount)
	return nil
}

// fetchBackupData fetches backup data and constructs ResourceData and ResourceKey
func (p *BillingProvider) fetchBackupData(ctx context.Context, aggregationStartTime time.Time, resourceCollection *ResourceCollection) error {
	logger := util.GetLogger(ctx)

	// First, fetch all backup metadata entries to get volumeUUID -> labels mapping
	volumeLabelsMap, err := p.fetchBackupMetadata(ctx, aggregationStartTime)
	if err != nil {
		logger.Warnf("Failed to fetch backup metadata (table may not exist yet): %v", err)
		// Continue with empty labels map if metadata fetch fails
		volumeLabelsMap = make(map[string]Labels)
	}

	// Create conditions for backups including deleted backups where deleted_at is between aggregation start time and current time
	conditions := [][]interface{}{
		{"(backups.deleted_at IS NULL OR (backups.deleted_at >= ? AND backups.deleted_at <= ?))", aggregationStartTime, time.Now()},
	}

	offset := int32(0)
	// Use configurable limit from config
	limit := p.config.PoolVolumeLabelPageSize
	totalProcessed := 0
	batchCount := 0

	for {
		// Create pagination with offset and limit
		pagination := &dbutils.Pagination{
			Offset: int(offset),
			Limit:  int(limit),
		}

		// Fetch paginated backup metrics
		backups, err := p.vcpDataStore.GetBackupResourceDataForAggregation(ctx, conditions, pagination, resourceCollection.FreeTrialAccounts)
		if err != nil {
			return fmt.Errorf("failed to get backup metrics (offset %d): %w", offset, err)
		}

		// Break if no records returned
		if len(backups) == 0 {
			break
		}

		// Process current batch
		for _, backup := range backups {
			// Skip backups with nil Attributes or BackupVault to prevent panic
			if backup.Attributes == nil {
				logger.Warnf("Skipping backup %s due to nil Attributes", backup.UUID)
				continue
			}
			if backup.BackupVault == nil {
				logger.Warnf("Skipping backup %s due to nil BackupVault relationship", backup.UUID)
				continue
			}

			// Get labels from the backup metadata map
			labels := volumeLabelsMap[backup.VolumeUUID]
			if labels == nil {
				labels = make(Labels)
			}

			// Get VolumeStyle from backup attributes (authoritative source)
			largeCapacity := false
			volumeStyle := getVolumeStyle(largeCapacity) // Default
			ontapVolumeStyle := backup.Attributes.OntapVolumeStyle
			if ontapVolumeStyle != "" {
				volumeStyle = strings.ToUpper(ontapVolumeStyle)
				largeCapacity = strings.EqualFold(ontapVolumeStyle, database.OntapFgVolumeStyle)
			} else {
				logger.Warnf("Backup %s (volume: %s) missing OntapVolumeStyle, defaulting to FLEXVOL",
					backup.UUID, backup.VolumeUUID)
			}

			var freeTrialEnd *time.Time
			if p.config.EnableFreeTrialBilling && backup.BackupVault.Account != nil && backup.BackupVault.Account.AccountMetadata != nil && backup.BackupVault.Account.AccountMetadata.TrialMode != nil && backup.BackupVault.ServiceType != datamodel.ServiceTypeCrossProject {
				freeTrialEnd = backup.BackupVault.Account.AccountMetadata.TrialMode.EndTime
			}
			backupResourceData := ResourceData{
				UUID:             backup.VolumeUUID, // Using volume UUID
				AccountID:        backup.BackupVault.AccountID,
				Labels:           labels,
				LargeCapacity:    largeCapacity,
				VolumeStyle:      volumeStyle,
				BackupRegionName: backup.BackupVault.BackupRegionName,
				FreeTrialEndAt:   freeTrialEnd,
			}
			vaultAccountName := ""
			if backup.BackupVault.Account != nil {
				vaultAccountName = backup.BackupVault.Account.Name
			}
			addBackupDataEntry(
				logger,
				resourceCollection,
				backup.VolumeUUID,
				backup.BackupVault.Name,
				backup.Attributes.AccountIdentifier,
				backupResourceData,
				backup.BackupVault.ServiceType,
				vaultAccountName,
			)
		}

		totalProcessed += len(backups)
		batchCount++
		logger.Debugf("Processed %d backup metrics in batch %d (offset: %d, total: %d)", len(backups), batchCount, offset, totalProcessed)

		// Update offset for next iteration
		offset += int32(limit)
	}

	logger.Infof("Fetched resource data for %d backups with %d volume labels in %d batches", len(resourceCollection.BackupData), len(volumeLabelsMap), batchCount)
	return nil
}

// fetchBackupHistoryMetrics builds hydrated metrics from backup chain history entries.
//
// For GCBDR volumes, the same (volume, vault) pair can have multiple concurrently active
// rows — one per endpoint UUID. To produce a correct billing signal, rows that share the
// same resource key are merged into a single combined timeline using a sweep-line algorithm
// that sums sizes across all active endpoints at every boundary point.
func (p *BillingProvider) fetchBackupHistoryMetrics(ctx context.Context, aggregationStartTime, aggregationEndTime time.Time, backfillLimit time.Duration, resourceCollection *ResourceCollection) ([]datamodel2.HydratedMetrics, error) {
	startWindow := aggregationStartTime.Add(-backfillLimit)
	endWindow := aggregationEndTime.Add(backfillLimit)

	var histories []*datamodel.BackupChainHistory
	offset := 0
	limit := p.config.PoolVolumeLabelPageSize
	conditions := [][]interface{}{
		{"resource_uuid IS NOT NULL"},
		{"created_at <= ?", endWindow},
		{"(deleted_at IS NULL OR deleted_at >= ?)", startWindow},
	}

	for {
		pagination := &dbutils.Pagination{
			Offset: offset,
			Limit:  limit,
		}
		page, err := p.vcpDataStore.ListBackupChainHistoriesWithPagination(ctx, conditions, pagination)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		histories = append(histories, page...)
		offset += len(page)
		if len(page) < limit {
			break
		}
	}

	if len(histories) == 0 {
		return []datamodel2.HydratedMetrics{}, nil
	}

	// Group histories by (resourceUUID, deploymentName, consumerID).
	type historyGroupKey struct {
		resourceUUID   string
		deploymentName string
		consumerID     string
	}
	grouped := make(map[historyGroupKey][]*datamodel.BackupChainHistory)
	for _, h := range histories {
		if h.ResourceUUID == "" {
			continue
		}
		key := historyGroupKey{
			resourceUUID:   h.ResourceUUID,
			deploymentName: h.DeploymentName,
			consumerID:     h.ConsumerID,
		}
		grouped[key] = append(grouped[key], h)
	}

	var metrics []datamodel2.HydratedMetrics
	for key, rows := range grouped {
		// Detect whether any row in this group carries an endpoint UUID (GCBDR path).
		hasEndpoint := false
		for _, r := range rows {
			if r.EndpointUUID != nil && *r.EndpointUUID != "" {
				hasEndpoint = true
				break
			}
		}

		if !hasEndpoint {
			// Normal (non-GCBDR) path: one active row per volume at a time — emit directly.
			for _, h := range rows {
				backupMode := backupModeDefault
				if h.IsExpertModeBackup {
					backupMode = backupModeOntap
				}
				modeMetadata, err := json.Marshal(map[string]string{backupModeMetadataKey: backupMode})
				if err != nil {
					modeMetadata = nil
				}
				deletedAt := deletedAtPtr(h.DeletedAt)
				metrics = append(metrics, datamodel2.HydratedMetrics{
					MetricTimestamp: h.CreatedAt,
					MeasuredType:    metadata.MeasuredTypeValue(metadata.BackupLogicalSize),
					ConsumerID:      key.consumerID,
					ResourceType:    metadata.ResourceTypeValue(metadata.Backup),
					ResourceName:    key.resourceUUID,
					Location:        p.config.RegionName,
					Quantity:        float64(h.Size),
					DeploymentName:  key.deploymentName,
					DeletedAt:       deletedAt,
					Metadata:        modeMetadata,
				})
			}
			continue
		}

		// GCBDR path: multiple endpoints may be active simultaneously.
		// Use a sweep-line merge to sum sizes across all active endpoints at every
		// boundary point, producing a single combined timeline for this resource group.
		combined := mergeEndpointHistories(rows)
		for _, interval := range combined {
			backupMode := backupModeDefault
			if interval.isExpertMode {
				backupMode = backupModeOntap
			}
			modeMetadata, err := json.Marshal(map[string]string{backupModeMetadataKey: backupMode})
			if err != nil {
				modeMetadata = nil
			}
			metrics = append(metrics, datamodel2.HydratedMetrics{
				MetricTimestamp: interval.start,
				MeasuredType:    metadata.MeasuredTypeValue(metadata.BackupLogicalSize),
				ConsumerID:      key.consumerID,
				ResourceType:    metadata.ResourceTypeValue(metadata.Backup),
				ResourceName:    key.resourceUUID,
				Location:        p.config.RegionName,
				Quantity:        float64(interval.size),
				DeploymentName:  key.deploymentName,
				DeletedAt:       interval.end,
				Metadata:        modeMetadata,
			})
		}
	}

	return metrics, nil
}

// combinedInterval represents one segment of the merged GCBDR timeline.
type combinedInterval struct {
	start        time.Time
	end          *time.Time // nil means still active
	size         int64
	isExpertMode bool
}

// mergeEndpointHistories implements a sweep-line algorithm over a set of
// BackupChainHistory rows that may belong to different endpoints but share the
// same (volume, vault, consumer). Each row is an interval [CreatedAt, DeletedAt).
// The algorithm produces a non-overlapping, sorted list of intervals whose Size
// is the sum of all rows active during that sub-interval.
func mergeEndpointHistories(rows []*datamodel.BackupChainHistory) []combinedInterval {
	// Represent each boundary as a signed event: +size at start, -size at end.
	type event struct {
		t     time.Time
		delta int64 // positive = endpoint starts, negative = endpoint ends
	}
	var events []event
	for _, r := range rows {
		events = append(events, event{t: r.CreatedAt, delta: r.Size})
		if r.DeletedAt != nil && r.DeletedAt.Valid {
			events = append(events, event{t: r.DeletedAt.Time, delta: -r.Size})
		}
	}

	// Sort events by time; for equal times apply endings before beginnings so
	// a zero-size gap is not emitted when one endpoint closes and another opens.
	slices.SortFunc(events, func(a, b event) int {
		if a.t.Before(b.t) {
			return -1
		}
		if a.t.After(b.t) {
			return 1
		}
		// Same time: process endings (negative delta) first.
		if a.delta < b.delta {
			return -1
		}
		if a.delta > b.delta {
			return 1
		}
		return 0
	})

	// Collect all unique boundary timestamps.
	seen := make(map[time.Time]struct{})
	var boundaries []time.Time
	for _, e := range events {
		if _, ok := seen[e.t]; !ok {
			seen[e.t] = struct{}{}
			boundaries = append(boundaries, e.t)
		}
	}
	slices.SortFunc(boundaries, func(a, b time.Time) int {
		if a.Before(b) {
			return -1
		}
		if a.After(b) {
			return 1
		}
		return 0
	})

	// Walk the boundaries, maintaining a running total of active size.
	// Compute the running total at each boundary by replaying the events.
	// Build a map: boundary time → running total AFTER applying all events at that time.
	totalAtBoundary := make(map[time.Time]int64)
	runningTotal := int64(0)
	ei := 0
	for _, b := range boundaries {
		for ei < len(events) && !events[ei].t.After(b) {
			runningTotal += events[ei].delta
			ei++
		}
		totalAtBoundary[b] = runningTotal
	}

	// Determine the latest deleted_at across all rows; nil means at least one is still active.
	var latestEnd *time.Time
	for _, r := range rows {
		if r.DeletedAt == nil || !r.DeletedAt.Valid {
			latestEnd = nil
			break
		}
		if latestEnd == nil || r.DeletedAt.Time.After(*latestEnd) {
			t := r.DeletedAt.Time
			latestEnd = &t
		}
	}

	isExpertMode := false
	for _, r := range rows {
		if r.IsExpertModeBackup {
			isExpertMode = true
			break
		}
	}

	// Emit one combined interval per boundary segment.
	var result []combinedInterval
	for i, b := range boundaries {
		size := totalAtBoundary[b]
		if size <= 0 {
			continue
		}
		var end *time.Time
		if i+1 < len(boundaries) {
			t := boundaries[i+1]
			end = &t
		} else {
			end = latestEnd
		}
		result = append(result, combinedInterval{start: b, end: end, size: size, isExpertMode: isExpertMode})
	}
	return result
}

func deletedAtPtr(deletedAt *gorm.DeletedAt) *time.Time {
	if deletedAt == nil || !deletedAt.Valid {
		return nil
	}
	t := deletedAt.Time
	return &t
}

func (p *BillingProvider) fetchOntapModePoolData(ctx context.Context, aggregationStartTime time.Time, resourceCollection *ResourceCollection) error {
	logger := util.GetLogger(ctx)
	logger.Info("Fetching ONTAP mode pool data for CRR billing")

	// pre computing map to get source location for ONTAP mode CRR relationships.
	regionCodeToLocation, err := getRegionCodeToLocationMap(p.config.RegionNumberMap)
	if err != nil {
		logger.Warnf("Failed to parse REGION_NUMBER_MAP for ONTAP CRR source location lookup: %v", err)
		regionCodeToLocation = nil
	}

	offset := 0
	limit := p.config.PoolVolumeLabelPageSize
	totalPoolsProcessed := 0
	batchCount := 0
	aggregationEndTime := time.Now()

	// Map deployment_name -> pool info for ONTAP mode pools
	deploymentToPoolInfo := make(map[string]OntapPoolInfo)
	deploymentNames := make([]string, 0)

	for {
		pagination := &dbutils.Pagination{
			Offset: offset,
			Limit:  limit,
		}

		// Fetch paginated ONTAP mode pools
		pools, err := p.vcpDataStore.ListOntapModePoolsForResourceData(ctx, aggregationStartTime, aggregationEndTime, pagination)
		if err != nil {
			return fmt.Errorf("failed to list ONTAP mode pools (offset %d): %w", offset, err)
		}

		if len(pools) == 0 {
			break
		}

		// Collect pool info by deployment name
		for _, pool := range pools {
			if pool.DeploymentName != "" {
				primaryZone := ""
				if pool.PoolAttributes != nil {
					primaryZone = pool.PoolAttributes.PrimaryZone
				}
				deploymentToPoolInfo[pool.DeploymentName] = OntapPoolInfo{
					UUID:        pool.UUID,
					AccountID:   pool.AccountID,
					AccountName: pool.GetAccountName(),
					PrimaryZone: primaryZone,
				}
				deploymentNames = append(deploymentNames, pool.DeploymentName)
			}
		}

		totalPoolsProcessed += len(pools)
		batchCount++
		logger.Debugf("Processed %d ONTAP mode pools in batch %d (offset: %d, total: %d)", len(pools), batchCount, offset, totalPoolsProcessed)

		offset += limit
	}

	logger.Infof("Collected %d deployment names from %d ONTAP mode pools", len(deploymentToPoolInfo), totalPoolsProcessed)

	if len(deploymentToPoolInfo) == 0 {
		logger.Info("No ONTAP mode pools found, skipping hydrated_metrics fetch")
		return nil
	}

	// Batch-fetch hydrated_metrics for all deployment names with resource_type='VOLUME_REPLICATION_RELATIONSHIP'.
	// The result map is keyed by deployment name + resource name so multiple replications under the same
	// deployment are preserved.
	metricsByDeployment, err := p.fetchHydratedMetricsForOntapCrr(ctx, deploymentNames, aggregationStartTime)
	if err != nil {
		return fmt.Errorf("failed to fetch hydrated metrics for ONTAP CRR: %w", err)
	}

	for _, metric := range metricsByDeployment {
		deploymentName := metric.DeploymentName
		poolInfo, ok := deploymentToPoolInfo[deploymentName]
		if !ok {
			continue
		}

		var sourceLocation *string
		var sourceDeploymentName string
		var sourceDetails string
		if len(metric.Metadata) > 0 {
			var metadataMap map[string]string
			if err := json.Unmarshal(metric.Metadata, &metadataMap); err == nil {
				if sd, ok := metadataMap["source_details"]; ok && sd != "" {
					sourceDetails = sd
					if region := getSourceLocationFromSourceDetails(sd, regionCodeToLocation); region != "" {
						sourceLocation = &region
					}
					sourceDeploymentName = getDeploymentNameFromSourceDetails(sd)
				}
			}
		}

		repName := metric.ResourceName
		destinationLocation := metric.Location

		var replicationType string
		if sourceDetails != "" && !strings.HasPrefix(sourceDetails, "gcnv-") {
			replicationType = clientmodel.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION
		} else {
			replicationType = determineOntapReplicationType(sourceLocation, &destinationLocation, sourceDeploymentName, deploymentName, deploymentToPoolInfo, logger)
		}

		volRepInfo := VolumeReplicationInfo{
			ReplicationName:     &repName,
			ReplicationType:     replicationType,
			SourceLocation:      sourceLocation,
			DestinationLocation: &destinationLocation,
			ReplicationSchedule: ReplicationScheduleOntapMode,
		}

		id := ResourceKey{
			ResourceType:   metadata.VolumeReplicationRelationship,
			ResourceName:   metric.ResourceName,
			DeploymentName: deploymentName,
			ConsumerID:     poolInfo.AccountName,
		}

		var freeTrialEnd *time.Time
		if p.config.EnableFreeTrialBilling {
			freeTrialEnd = resourceCollection.FreeTrialAccounts[poolInfo.AccountID]
		}

		resourceCollection.VolumeReplicationData[id] = ResourceData{
			UUID:                  repName,
			AccountID:             poolInfo.AccountID,
			VolumeReplicationInfo: &volRepInfo,
			IsONTAPMode:           true,
			FreeTrialEndAt:        freeTrialEnd,
		}
		logger.Debugf("Fetched hydrated metric for ONTAP mode deployment %s (pool UUID: %s, replicationType: %s)", deploymentName, poolInfo.UUID, replicationType)
	}

	logger.Infof("Successfully fetched ONTAP mode CRR data for %d replication relationships", len(resourceCollection.VolumeReplicationData))
	return nil
}

// fetchHydratedMetricsForOntapCrr fetches the first hydrated_metrics entry per deployment/resource pair
// with resource_type='VOLUME_REPLICATION_RELATIONSHIP'.
func (p *BillingProvider) fetchHydratedMetricsForOntapCrr(ctx context.Context, deploymentNames []string, aggregationStartTime time.Time) (map[string]datamodel2.HydratedMetrics, error) {
	result := make(map[string]datamodel2.HydratedMetrics, len(deploymentNames))
	offset := 0
	limit := p.config.PoolVolumeLabelPageSize

	conditions := [][]interface{}{
		{"deployment_name IN ?", deploymentNames},
		{"resource_type = ?", metadata.VolumeReplicationRelationship.String()},
		{"metric_timestamp >= ?", aggregationStartTime},
	}

	for {
		pagination := &dbutils.Pagination{
			Offset: offset,
			Limit:  limit,
		}

		metrics, err := p.metricsDB.GetHydratedMetricsWithPagination(ctx, conditions, pagination)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch hydrated metrics (offset %d): %w", offset, err)
		}

		if len(metrics) == 0 {
			break
		}

		for i := range metrics {
			key := metrics[i].DeploymentName + "-" + metrics[i].ResourceName
			if _, exists := result[key]; !exists {
				result[key] = metrics[i]
			}
		}
		offset += len(metrics)

		if len(metrics) < limit {
			break
		}
	}

	return result, nil
}

// fetchVolumeReplicationData fetches labels from volume replication table using pagination
func (p *BillingProvider) fetchVolumeReplicationData(ctx context.Context, aggregationStartTime time.Time, resourceCollection *ResourceCollection) error {
	logger := util.GetLogger(ctx)

	offset := 0
	// Use configurable limit from config
	limit := p.config.PoolVolumeLabelPageSize
	totalProcessed := 0
	batchCount := 0

	// Create conditions for volume replications including deleted ones where deleted_at is between aggregation start time and current time
	conditions := [][]interface{}{
		{"(deleted_at IS NULL OR (deleted_at >= ? AND deleted_at <= ?))", aggregationStartTime, time.Now()},
	}

	for {
		// Create pagination with offset and limit
		pagination := &dbutils.Pagination{
			Offset: offset,
			Limit:  limit,
		}

		// Fetch paginated volume replications using ListVolumeReplicationsWithPagination
		volumeReplications, err := p.vcpDataStore.ListVolumeReplicationsWithPagination(ctx, conditions, pagination)
		if err != nil {
			return fmt.Errorf("failed to list volume replications (offset %d): %w", offset, err)
		}

		// Break if no records returned
		if len(volumeReplications) == 0 {
			break
		}

		// Process current batch
		for _, volumeReplication := range volumeReplications {
			// Skip volume replications with nil Account to prevent panic
			if volumeReplication.Account == nil {
				logger.Warnf("Skipping volume replication %s (%s) due to nil Account relationship", volumeReplication.Name, volumeReplication.UUID)
				continue
			}

			// Check if volume is nil - must be checked before any Volume access
			if volumeReplication.Volume == nil {
				logger.Warnf("Skipping volume replication %s (%s) due to nil Volume relationship", volumeReplication.Name, volumeReplication.UUID)
				continue
			}

			// Check if we need to skip files volumes
			if !p.config.EnableFilesReplicationBillingMetrics {
				if volumeReplication.Volume.VolumeAttributes == nil || volumeReplication.Volume.VolumeAttributes.Protocols == nil {
					logger.Warnf("Skipping volume replication %s (%s) due to missing volume attributes", volumeReplication.Name, volumeReplication.UUID)
					continue
				}
				if !slices.Contains(volumeReplication.Volume.VolumeAttributes.Protocols, "ISCSI") {
					logger.Debugf("Skipping volume replication %s (%s) - volume protocol is not ISCSI (protocols: %v)", volumeReplication.Name, volumeReplication.UUID, volumeReplication.Volume.VolumeAttributes.Protocols)
					continue
				}
			}

			// Check if we need to skip bidirectional replications
			if !p.config.EnableBidirectionalReplicationBillingMetrics {
				if volumeReplication.ReplicationAttributes != nil && (volumeReplication.ReplicationAttributes.ReplicationType == string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION) || volumeReplication.ReplicationAttributes.ReplicationType == string(datamodel.HybridReplicationParametersReplicationTypeONPREM)) {
					logger.Debugf("Skipping volume replication %s (%s) - bidirectional replication type", volumeReplication.Name, volumeReplication.UUID)
					continue
				}
			}

			// Check if we need to skip in-region replications
			if !p.config.EnableInRegionReplicationBillingMetrics {
				if volumeReplication.ReplicationAttributes != nil && (volumeReplication.ReplicationAttributes.ReplicationType == string(googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTRAZONEREPLICATION) ||
					volumeReplication.ReplicationAttributes.ReplicationType == string(googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTERZONEREPLICATION)) {
					logger.Debugf("Skipping volume replication %s (%s) - in-region replication type", volumeReplication.Name, volumeReplication.UUID)
					continue
				}
			}

			var limitedLabels Labels
			var volRepInfo *VolumeReplicationInfo
			if volumeReplication.ReplicationAttributes != nil {
				if volumeReplication.ReplicationAttributes.Labels != nil {
					limitedLabels = p.limitLabels(volumeReplication.ReplicationAttributes.Labels)
				} else {
					limitedLabels = make(Labels)
				}
				volRepInfo = &VolumeReplicationInfo{
					ReplicationName:       &volumeReplication.Name,
					ReplicationSchedule:   volumeReplication.ReplicationAttributes.ReplicationSchedule,
					ReplicationType:       volumeReplication.ReplicationAttributes.ReplicationType,
					SourceLocation:        &volumeReplication.ReplicationAttributes.SourceLocation,
					DestinationVolumeUUID: &volumeReplication.ReplicationAttributes.DestinationVolumeUUID,
					DestinationLocation:   &volumeReplication.ReplicationAttributes.DestinationLocation,
				}
			}

			// Get LargeCapacity from volume's LargeVolumeAttributes (authoritative source)
			largeCapacity := false
			if volumeReplication.Volume.LargeVolumeAttributes != nil {
				largeCapacity = volumeReplication.Volume.LargeVolumeAttributes.LargeCapacity
			} else {
				logger.Warnf("VolumeReplication %s missing Volume.LargeVolumeAttributes, defaulting to FLEXVOL",
					volumeReplication.UUID)
			}
			volumeStyle := getVolumeStyle(largeCapacity)

			var replicationFreeTrialEnd *time.Time
			if p.config.EnableFreeTrialBilling && volumeReplication.Account != nil && volumeReplication.Account.AccountMetadata != nil && volumeReplication.Account.AccountMetadata.TrialMode != nil {
				replicationFreeTrialEnd = volumeReplication.Account.AccountMetadata.TrialMode.EndTime
			}

			volumeReplicationResourceData := ResourceData{
				UUID:                  volumeReplication.UUID,
				AccountID:             volumeReplication.AccountID,
				Labels:                limitedLabels,
				VolumeReplicationInfo: volRepInfo,
				LargeCapacity:         largeCapacity,
				VolumeStyle:           volumeStyle,
				CreatedAt:             &volumeReplication.CreatedAt,
				FreeTrialEndAt:        replicationFreeTrialEnd,
			}
			id := ResourceKey{
				ResourceType:   metadata.VolumeReplicationRelationship,
				ResourceName:   volumeReplication.ReplicationAttributes.ExternalUUID,
				DeploymentName: volumeReplication.Volume.Pool.DeploymentName,
				ConsumerID:     volumeReplication.Account.Name,
			}
			logger.Debugf("Volume Replication name %s", volumeReplication.Name)
			resourceCollection.VolumeReplicationData[id] = volumeReplicationResourceData
		}

		totalProcessed += len(volumeReplications)
		batchCount++
		logger.Debugf("Processed %d volume replications in batch %d (offset: %d, total: %d)", len(volumeReplications), batchCount, offset, totalProcessed)

		// Update offset for next iteration
		offset += limit
	}

	logger.Infof("Fetched resource data for %d volume replications in %d batches", len(resourceCollection.VolumeReplicationData), batchCount)
	return nil
}

// fetchBackupMetadata fetches all backup metadata entries and returns volumeUUID -> labels mapping
func (p *BillingProvider) fetchBackupMetadata(ctx context.Context, aggregationStartTime time.Time) (map[string]Labels, error) {
	logger := util.GetLogger(ctx)

	// Create conditions for backup metadata with labels including deleted metadata where deleted_at is between aggregation start time and current time
	conditions := [][]interface{}{
		{"labels IS NOT NULL"},
		{"(deleted_at IS NULL OR (deleted_at >= ? AND deleted_at <= ?))", aggregationStartTime, time.Now()},
	}

	offset := int32(0)
	// Use configurable limit from config
	limit := p.config.PoolVolumeLabelPageSize
	totalProcessed := 0
	batchCount := 0

	// Create volumeUUID -> labels mapping
	volumeLabelsMap := make(map[string]Labels)

	for {
		// Create pagination with offset and limit
		pagination := &dbutils.Pagination{
			Offset: int(offset),
			Limit:  int(limit),
		}

		// Fetch paginated backup metadata using interface method
		backupMetadataList, err := p.vcpDataStore.GetBackupMetadata(ctx, conditions, pagination)
		if err != nil {
			// Check if it's a table not found error
			if strings.Contains(err.Error(), "does not exist") {
				logger.Warnf("Backup metadata table does not exist yet, returning empty labels map")
				return make(map[string]Labels), nil
			}
			return nil, fmt.Errorf("failed to fetch backup metadata (offset %d): %w", offset, err)
		}

		// Break if no records returned
		if len(backupMetadataList) == 0 {
			break
		}

		// Process current batch
		for _, backupMetadata := range backupMetadataList {
			if backupMetadata.Labels != nil && backupMetadata.VolumeUUID != "" {
				// Convert JSONB to Labels map
				labels := p.limitLabels(backupMetadata.Labels)
				volumeLabelsMap[backupMetadata.VolumeUUID] = labels
			}
		}

		totalProcessed += len(backupMetadataList)
		batchCount++
		logger.Debugf("Processed %d backup metadata entries in batch %d (offset: %d, total: %d)", len(backupMetadataList), batchCount, offset, totalProcessed)

		// Update offset for next iteration
		offset += int32(limit)
	}

	logger.Infof("Fetched %d backup metadata entries with labels in %d batches", len(volumeLabelsMap), batchCount)
	return volumeLabelsMap, nil
}

// limitLabels limits the number of labels to the configured maximum
func (p *BillingProvider) limitLabels(labels *datamodel.JSONB) Labels {
	if labels == nil {
		return make(Labels)
	}

	limitedLabels := make(Labels)
	count := 0

	for key, value := range *labels {
		if count >= p.config.GoogleBillingLabelsMaxEntries {
			break
		}
		limitedLabels[key] = value
		count++
	}

	return limitedLabels
}

// getVolumeStyle extracts volume style from LargeCapacity flag (FLEXVOL or FLEXGROUP)
func getVolumeStyle(largeCapacity bool) string {
	if largeCapacity {
		return "FLEXGROUP"
	}
	return "FLEXVOL"
}

// getResourceDataForAggregationUsage returns the billing labels for a given resource
func (p *BillingProvider) getResourceDataForAggregationUsage(id ResourceKey, resourceType metadata.ResourceType, resourceCollection *ResourceCollection) *ResourceData {
	var resourceData ResourceData
	var found bool

	// Get labels based on resource type
	switch resourceType {
	case metadata.VolumePool:
		resourceData, found = resourceCollection.PoolData[id]
	case metadata.Volume:
		resourceData, found = resourceCollection.VolumeData[id]
	case metadata.Backup:
		resourceData, found = resourceCollection.BackupData[id]
	case metadata.VolumePoolRegionalHA:
		resourceData, found = resourceCollection.PoolData[id]
	case metadata.VolumeRegionalHA:
		resourceData, found = resourceCollection.VolumeData[id]
	case metadata.VolumeReplicationRelationship:
		resourceData, found = resourceCollection.VolumeReplicationData[id]
	default:
		return nil
	}
	if !found {
		return nil
	}
	return &resourceData
}

// CreateFilterWithConditions creates a filter map with conditions for metrics queries
func (p *BillingProvider) CreateFilterWithConditions(startTime time.Time, endTime time.Time, resourceType string, measuredType string) map[string]interface{} {
	conditions := [][]interface{}{
		{"metric_timestamp >= ?", startTime},
		{"metric_timestamp <= ?", endTime},
	}

	if resourceType != "" {
		conditions = append(conditions, []interface{}{"resource_type = ?", resourceType})
	}

	if measuredType != "" {
		conditions = append(conditions, []interface{}{"measured_type = ?", measuredType})
	}

	return map[string]interface{}{
		"conditions": conditions,
	}
}

// CreateComplexFilter creates a filter with multiple optional conditions
func (p *BillingProvider) CreateComplexFilter(options map[string]interface{}) map[string]interface{} {
	var conditions [][]interface{}

	// Add time range conditions if present
	if startTime, ok := options["startTime"].(time.Time); ok {
		conditions = append(conditions, []interface{}{"metric_timestamp >= ?", startTime})
	}

	if endTime, ok := options["endTime"].(time.Time); ok {
		conditions = append(conditions, []interface{}{"metric_timestamp <= ?", endTime})
	}

	// Add resource type condition if present
	if resourceType, ok := options["resourceType"].(string); ok && resourceType != "" {
		conditions = append(conditions, []interface{}{"resource_type = ?", resourceType})
	}

	// Add measured type condition if present
	if measuredType, ok := options["measuredType"].(string); ok && measuredType != "" {
		conditions = append(conditions, []interface{}{"measured_type = ?", measuredType})
	}

	// Add UUID filter if present
	if uuids, ok := options["uuids"].([]string); ok && len(uuids) > 0 {
		conditions = append(conditions, []interface{}{"uuid in ?", uuids})
	}

	filter := map[string]interface{}{
		"conditions": conditions,
	}

	// Add ordering if present
	if order, ok := options["order"].(string); ok && order != "" {
		filter["order"] = order
	}

	// Add limit if present
	if limit, ok := options["limit"].(int); ok && limit > 0 {
		filter["limit"] = limit
	}

	return filter
}

func (p *BillingProvider) groupMetricsByResource(metrics []datamodel2.HydratedMetrics) map[ResourceKey][]entity.HydratedMetric {
	groups := make(map[ResourceKey][]entity.HydratedMetric)
	for _, metric := range metrics {
		if metric.ResourceName != "" {
			identifier := ResourceKey{
				ResourceName:   metric.ResourceName,
				DeploymentName: metric.DeploymentName,
				ConsumerID:     metric.ConsumerID,
				ResourceType:   metadata.ResourceTypeFromValue(metric.ResourceType),
			}
			resMeta := metadata.ResourceMetadata{
				ResourceType:   metadata.ResourceTypeFromValue(metric.ResourceType),
				ResourceName:   &metric.ResourceName,
				DeploymentName: &metric.DeploymentName,
				AccountName:    &metric.ConsumerID,
				RegionName:     &metric.Location,
				DeletedAt:      metric.DeletedAt,
			}
			if len(metric.Metadata) > 0 {
				var extra map[string]string
				if err := json.Unmarshal(metric.Metadata, &extra); err == nil {
					if v, ok := extra["backup_region_name"]; ok {
						resMeta.SetBackupRegionName(v)
					}
					if v, ok := extra["pool_name"]; ok {
						resMeta.SetPoolName(v)
					}
					if sourceDetails, ok := extra["source_details"]; ok && !strings.HasPrefix(sourceDetails, "gcnv-") {
						if v, ok := extra["last_transfer_type"]; ok {
							resMeta.SetTransferType(v)
						}
					}
					if v, ok := extra["backup_mode"]; ok && v != "" {
						resMeta.SetServiceLevel(v)
					}
				}
			}
			hydratedMetric := entity.HydratedMetric{
				Quantity:     metric.Quantity,
				MeasuredType: metadata.MeasuredTypeFromValue(metric.MeasuredType),
				Timestamp:    entity.UnixNano(metric.MetricTimestamp.UnixNano()),
				Metadata:     resMeta,
			}
			groups[identifier] = append(groups[identifier], hydratedMetric)
		}
	}
	return groups
}

// fetchMetricsForCounterAndIntegralAggregation fetches metrics for counter aggregation using pagination
// This gets all records from the current aggregation window plus the latest record from the previous period
func (p *BillingProvider) fetchMetricsForCounterAndIntegralAggregation(ctx context.Context, aggregationStartTime, aggregationEndTime time.Time, resourceType, measuredType string, backfillLimit time.Duration) ([]datamodel2.HydratedMetrics, error) {
	// Create a complex filter that sorts by resource and timestamp

	//	Look ahead 1 hour before and after the currenbt aggregation cycle. This is used in Forward and Backward aggregation scenarios. Forward aggregation
	//	is used for integral aggregation where we need the latest record after the aggregation end time. Backward aggregation is used for counter aggregation
	//	where we need the latest record before the aggregation start time.
	filter := p.CreateComplexFilter(map[string]interface{}{
		"startTime":    aggregationStartTime.Add(-backfillLimit), // Look back 1 hour before aggregation start
		"endTime":      aggregationEndTime.Add(backfillLimit),    // Look ahead 1 hour after aggregation end
		"resourceType": resourceType,
		"measuredType": measuredType,
		"order":        "resource_name, deployment_name, consumer_id, metric_timestamp ASC", // Database sorts for us
	})
	// Fetch all metrics using pagination to handle large datasets efficiently
	allMetrics, err := p.fetchAllHydratedMetricsWithPagination(ctx, filter)
	if err != nil {
		return nil, err
	}
	return allMetrics, nil
}

// fetchMetricsWithinWindow fetches hydrated metrics strictly within the aggregation window.
// Used for Sum and First aggregation types that don't need boundary records from adjacent periods.
func (p *BillingProvider) fetchMetricsWithinWindow(ctx context.Context, aggregationStartTime, aggregationEndTime time.Time, resourceType, measuredType string) ([]datamodel2.HydratedMetrics, error) {
	filter := p.CreateComplexFilter(map[string]interface{}{
		"startTime":    aggregationStartTime,
		"endTime":      aggregationEndTime,
		"resourceType": resourceType,
		"measuredType": measuredType,
		"order":        "resource_name, deployment_name, consumer_id, metric_timestamp ASC",
	})
	return p.fetchAllHydratedMetricsWithPagination(ctx, filter)
}

// filterMetricsForCounterAndIntegralAggregationSorted filters metrics to include only the latest record before
// aggregationStartTime and all records within the aggregation window
// Assumes metrics are already sorted by timestamp DESC (latest first)
func (p *BillingProvider) filterMetricsForCounterAndIntegralAggregationSorted(metrics []datamodel2.HydratedMetrics, aggregationStartTime time.Time) []datamodel2.HydratedMetrics {
	var result []datamodel2.HydratedMetrics
	var foundPreviousRecord bool

	// Since metrics are already sorted by timestamp DESC, we can process them in order
	for _, metric := range metrics {
		if metric.MetricTimestamp.Before(aggregationStartTime) {
			// This is a record from before the aggregation window
			// Since data is sorted DESC, this is the latest record before the window
			if !foundPreviousRecord {
				result = append(result, metric)
				foundPreviousRecord = true
			}
			// Skip any other records from before the window (they're older)
		} else {
			// This record is within the aggregation window - include it
			result = append(result, metric)
		}
	}

	return result
}

// appendReplicationNonBillableSegment persists a non-billable aggregated row for hybrid replications
// counter bytes before the first strictly positive sample, then the caller appends the billable row.
// The skipped row is appended first so it loses GetLatestAggregatedUsageForAllResources (created_at DESC)
// for last_counter_value. When segmentSplitAt falls within (start, end], aggregation windows are split so
// skipped uses [start, splitAt) and the billable row uses [splitAt, end] for idx_aggregated_usage_unique.
// skippedSegmentEndTransferType is persisted as LastTransferType on the non-billable row so the next
// aggregation cycle can carry the baseline transfer_type forward via the counter cache.
func appendReplicationNonBillableSegment(
	aggregated *datamodel2.AggregatedUsage,
	skippedPrePositiveQuantity float64,
	skippedSegmentEndCounter *float64,
	skippedSegmentEndTransferType *string,
	segmentSplitAt *time.Time,
	start, end time.Time,
	resourceKey ResourceKey,
	aggregatedRecords *[]datamodel2.AggregatedUsage,
	logger log.Logger,
) {
	skippedRec := *aggregated
	skippedRec.Quantity = skippedPrePositiveQuantity
	skippedRec.IsBillable = false
	skippedRec.LastCounterValue = skippedSegmentEndCounter
	skippedRec.LastTransferType = skippedSegmentEndTransferType
	if st := segmentSplitAt; st != nil {
		splitAt := *st
		if splitAt.After(start) && !splitAt.After(end) {
			skippedRec.AggregationStart = start
			skippedRec.AggregationEnd = splitAt
			aggregated.AggregationStart = splitAt
			aggregated.AggregationEnd = end
		}
	}
	logger.Debugf("Recording non-billable replication pre-first-positive segment quantity=%.6f MiB for resource %s",
		skippedPrePositiveQuantity, resourceKey.ResourceName)
	*aggregatedRecords = append(*aggregatedRecords, skippedRec)
}

func (p *BillingProvider) processMetricsWithJobDef(ctx context.Context, resourceKey ResourceKey, metrics common.TimeSeries, jobDef common.AggregationJobDefinition, start, end time.Time, resourceCollection *ResourceCollection, aggregatedRecords *[]datamodel2.AggregatedUsage, counterCache CounterAggregationCache, logger log.Logger) error {
	if len(metrics.DataPoints) == 0 {
		logger.Infof("No metrics found for resource key %s and customer id %s", resourceKey, resourceKey.ConsumerID)
		return nil
	}

	// Get resource data for the resource
	resourceData := p.getResourceDataForAggregationUsage(resourceKey, metrics.Metadata.ResourceType, resourceCollection)
	if resourceData == nil {
		logger.Warnf("No resource data found for resource name %s, deployment name :%s, customer ID : %s", resourceKey.ResourceName, resourceKey.DeploymentName, resourceKey.ConsumerID)
		return nil
	}

	// Calculate aggregated value based on job type
	var quantity float64
	var lastCounterValue *float64
	var lastTransferType *string
	var skippedQty float64
	var skippedSegmentEndCounter *float64
	var counterDeltaRes counterDeltaWithHistoryResult
	replicationTypeForSkip := ""
	if resourceData.VolumeReplicationInfo != nil {
		replicationTypeForSkip = resourceData.VolumeReplicationInfo.ReplicationType
	}
	skipBaselineBillingForHybridReplication := shouldSkipBaselineBillingForHybridReplication(metrics, replicationTypeForSkip, p.config.SkipHybridReplicationBaselineBilling)
	switch jobDef.AggregationType {
	case common.IntegralAggregation:
		quantity = common.Integral(metrics.DataPoints)
	case common.CounterAggregation:
		// Use the new method that considers previous aggregated counter values
		counterDeltaRes = p.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, metrics.DataPoints, metrics.MeasuredType, start, counterCache, resourceData.UUID, logger, skipBaselineBillingForHybridReplication)
		quantity = counterDeltaRes.billed
		lastCounterValue = counterDeltaRes.lastCounter
		lastTransferType = counterDeltaRes.lastTransferType
		skippedQty = counterDeltaRes.skippedQty
		skippedSegmentEndCounter = counterDeltaRes.skippedSegmentEndCounter
	case common.SumAggregation:
		quantity = common.Sum(metrics.DataPoints)
	case common.FirstAggregation:
		quantity = common.First(metrics.DataPoints)
	default:
		return fmt.Errorf("unsupported job type: %s", jobDef.AggregationType)
	}

	// Initialize with default values
	var billingLabelsJSON *string
	resourceUUID := resourceData.UUID
	accountID := strconv.FormatInt(resourceData.AccountID, 10)

	// Only marshal labels if they exist
	if len(resourceData.Labels) > 0 {
		labelsBytes, err := json.Marshal(resourceData.Labels)
		if err != nil {
			logger.Errorf("Failed to marshal billing labels for resource name %s, deployment name :%s : %v", resourceKey.ResourceName, resourceKey.DeploymentName, err)
		} else {
			labelsStr := string(labelsBytes)
			billingLabelsJSON = &labelsStr
		}
	}

	if metrics.MeasuredType != metadata.PoolTotalIops && metrics.MeasuredType != metadata.PoolTotalThroughputMibps {
		quantity = BytesToMiB(quantity)
		skippedQty = BytesToMiB(skippedQty)
	}

	// Determine base billability
	isBillable := common.IsBillableMetric(ctx, metrics.Metadata.ResourceType, metrics.MeasuredType)

	// Disable billing for Large Volumes pools for CRR, Auto Tiering, and Backup features
	// Only applies when EnableLargeVolumesBilling feature flag is disabled (default: false until GA)
	// resourceData is guaranteed to be non-nil at this point (checked above)
	if isBillable && !p.config.EnableLargeVolumesBilling {
		// Check if this is a Large Volumes pool/volume
		isLargeVolumesPool := resourceData.LargeCapacity

		// Check if this is a feature we want to disable for Large Volumes pools
		isCRRMetric := metrics.Metadata.ResourceType == metadata.VolumeReplicationRelationship
		isBackupMetric := metrics.MeasuredType == metadata.BackupLogicalSize ||
			metrics.MeasuredType == metadata.BackupEnabledVolumeAllocatedSize

		if isLargeVolumesPool && (isCRRMetric || isBackupMetric || isAutoTieringBillingMetric(metrics.MeasuredType)) {
			logger.Debugf("Disabling billing for Large Volumes pool resource %s (type: %s, measured: %s)",
				resourceKey.ResourceName, metrics.Metadata.ResourceType, metrics.MeasuredType)
			return nil
		}
	}
	billingMode := datamodel2.BillingModeCommercial
	if p.config.EnableFreeTrialBilling {
		billingMode = determineBillingMode(resourceData, end)
	}

	// Create aggregated record with all available fields
	aggregated := &datamodel2.AggregatedUsage{
		ResourceUUID:           resourceUUID,
		AccountID:              accountID,
		VendorCustomerID:       &resourceKey.ConsumerID,
		AggregationStart:       start,
		AggregationEnd:         end,
		MeasuredType:           metadata.MeasuredTypeValue(metrics.MeasuredType),
		ResourceType:           metadata.ResourceTypeValue(resourceKey.ResourceType),
		Quantity:               quantity,
		ResourceName:           &resourceKey.ResourceName,
		RegionName:             metrics.Metadata.RegionName,
		LastCounterValue:       lastCounterValue,
		LastTransferType:       lastTransferType,
		SourceRegion:           metrics.Metadata.RegionName,
		DestinationRegion:      nil,
		BillingLabels:          billingLabelsJSON,
		ReplicationDstVolumeID: nil,
		DoubleEncryption:       nil,
		State:                  datamodel2.Unsubmitted,
		ErrorCount:             0,
		ErrorMessage:           nil,
		IsBillable:             isBillable,
		BillingMode:            billingMode,
		AggregationType:        string(jobDef.AggregationType),
		ServiceLevel:           unifiedServiceType,
		VolumeStyle:            resourceData.VolumeStyle,
	}

	// Set zone for zonal pools (available for all metrics, used by AT billing location label)
	if resourceData.PrimaryZone != "" && resourceKey.ResourceType == metadata.VolumePool {
		aggregated.Zone = &resourceData.PrimaryZone
	}

	// Propagate backup mode (ONTAP / DEFAULT) from the hydrated metric into ServiceLevel so
	// GoogleMetricsClient can emit it as the /backups/mode label.
	if isBackupBillingMeasuredType(metadata.MeasuredTypeFromValue(aggregated.MeasuredType)) && metrics.Metadata.ServiceLevel != nil && *metrics.Metadata.ServiceLevel != "" {
		aggregated.ServiceLevel = *metrics.Metadata.ServiceLevel
	}

	if metadata.MeasuredTypeEquals(aggregated.MeasuredType, metadata.BackupLogicalSize) {
		if resourceData.BackupRegionName != nil && *resourceData.BackupRegionName != "" {
			aggregated.DestinationRegion = resourceData.BackupRegionName
		} else {
			aggregated.DestinationRegion = metrics.Metadata.RegionName
		}
	}

	if metadata.MeasuredTypeEquals(aggregated.MeasuredType, metadata.BackupEnabledVolumeAllocatedSize) {
		if metrics.Metadata.BackupRegionName != nil && *metrics.Metadata.BackupRegionName != "" {
			aggregated.DestinationRegion = metrics.Metadata.BackupRegionName
		}
	}

	if metadata.MeasuredTypeEquals(aggregated.MeasuredType, metadata.CbsCrossRegionVolumeRestoreTransferBytes) {
		aggregated.SourceRegion = metrics.Metadata.BackupRegionName
		aggregated.DestinationRegion = metrics.Metadata.RegionName
	}

	if metadata.MeasuredTypeEquals(aggregated.MeasuredType, metadata.CbsCrossRegionVolumeBackupTransferBytes) {
		aggregated.DestinationRegion = resourceData.BackupRegionName
	}

	if metadata.ResourceTypeEquals(aggregated.ResourceType, metadata.VolumeReplicationRelationship) {
		if resourceData.VolumeReplicationInfo != nil {
			aggregated.ServiceLevel = setServiceLevelForCRR(resourceData.VolumeReplicationInfo.ReplicationSchedule)
			aggregated.ResourceName = resourceData.VolumeReplicationInfo.ReplicationName
			aggregated.SourceRegion = resourceData.VolumeReplicationInfo.SourceLocation
			aggregated.DestinationRegion = resourceData.VolumeReplicationInfo.DestinationLocation
			aggregated.ReplicationDstVolumeID = resourceData.VolumeReplicationInfo.DestinationVolumeUUID
			aggregated.ReplicationType = resourceData.VolumeReplicationInfo.ReplicationType

			// when aggregated quantity is zero and also skippedPrePositiveBytes is zero, i.e, baseline transfer was happening for entire aggregation window.
			// then skip billing for replication transfer if it's hybrid migration or on-prem replication as for these replication types, baseline transfer should not be billed.
			if quantity == 0 && skippedQty == 0 && isHybridMigrationOrOnPremReplicationType(aggregated.ReplicationType) {
				aggregated.IsBillable = false
				logger.Infof("Setting IsBillable=false for replication baseline transfer (replication_type=%s), resource %s, deployment %s",
					aggregated.ReplicationType, resourceKey.ResourceName, resourceKey.DeploymentName)
			}
		} else {
			logger.Infof("No resourceData found for resource name %s, deployment name :%s", resourceKey.ResourceName, resourceKey.DeploymentName)
		}
	}

	logger.Debugf("Processing metrics for resource %s and customer id %s with aggregation type %s and %v", resourceKey.ResourceName, resourceKey.ConsumerID, jobDef.AggregationType, aggregated)
	// Format labels for better readability
	labelsInfo := "none"
	if len(resourceData.Labels) > 0 {
		// Create a formatted string of key-value pairs
		var labelPairs []string
		for key, value := range resourceData.Labels {
			labelPairs = append(labelPairs, fmt.Sprintf("%s=%v", key, value))
		}
		labelsInfo = fmt.Sprintf("[%s]", strings.Join(labelPairs, ", "))
	}

	logger.Debugf("Processing metrics for resource %s (customer: %s, type: %s, labels: %s)",
		resourceKey.ResourceName, resourceKey.ConsumerID, jobDef.AggregationType, labelsInfo)

	if skippedQty > 0 && skipBaselineBillingForHybridReplication &&
		jobDef.AggregationType == common.CounterAggregation {
		appendReplicationNonBillableSegment(
			aggregated,
			skippedQty,
			skippedSegmentEndCounter,
			counterDeltaRes.skippedSegmentEndTransferType,
			counterDeltaRes.segmentSplitAt,
			start,
			end,
			resourceKey,
			aggregatedRecords,
			logger,
		)
	}

	// Only append the billable record if quantity is greater than zero or if there were no skipped pre-positive bytes.
	// This prevents creating a zero-quantity billable record when there is a non-billable segment with quantity in the same aggregation window, which can happen for hybrid replication metrics.
	if quantity == 0 && skippedQty > 0 && isHybridMigrationOrOnPremReplicationType(aggregated.ReplicationType) {
		logger.Debugf("Skipping billable record with zero quantity for resource %s since there is a non-billable segment with quantity %.6f MiB in the same aggregation window for hybrid replication", resourceKey.ResourceName, skippedQty)
	} else {
		*aggregatedRecords = append(*aggregatedRecords, *aggregated)
	}

	return nil
}

func (p *BillingProvider) getUnsentGoogleUsages(ctx context.Context, maxRetries int64, aggregationEndTime time.Time) ([]datamodel2.AggregatedUsage, error) {
	aggregationStartTime := aggregationEndTime.Add(-1 * time.Hour)
	var allRecords []datamodel2.AggregatedUsage

	// Get records with UNSUBMITTED state within the aggregation time window
	unsubmittedConditions := [][]interface{}{
		{"state = ?", datamodel2.Unsubmitted},
		{"is_billable = ?", true},
		{"aggregation_end <= ?", aggregationEndTime},
		{"aggregation_start >= ?", aggregationStartTime},
		{"billing_mode = ?", datamodel2.BillingModeCommercial},
	}
	unsubmittedRecords, err := p.fetchAllRecordsWithPagination(ctx, unsubmittedConditions)
	if err != nil {
		return nil, err
	}
	allRecords = append(allRecords, unsubmittedRecords...)

	// Get records with ERROR state within the aggregation time window
	errorConditions := [][]interface{}{
		{"state = ?", datamodel2.Error},
		{"aggregation_end <= ?", aggregationEndTime},
		{"aggregation_start >= ?", aggregationStartTime},
	}
	errorRecords, err := p.fetchAllRecordsWithPagination(ctx, errorConditions)
	if err != nil {
		return nil, err
	}

	// Filter error records by error_count in memory
	for _, record := range errorRecords {
		if int64(record.ErrorCount) < maxRetries {
			allRecords = append(allRecords, record)
		}
	}

	return allRecords, nil
}

// fetchAllRecordsWithPagination fetches all aggregated usage records with pagination to handle large datasets efficiently
func (p *BillingProvider) fetchAllRecordsWithPagination(ctx context.Context, conditions [][]interface{}) ([]datamodel2.AggregatedUsage, error) {
	var allRecords []datamodel2.AggregatedUsage
	pageSize := p.config.PoolVolumeLabelPageSize
	offset := 0

	for {
		pagination := &dbutils.Pagination{
			Limit:  pageSize,
			Offset: offset,
		}

		records, err := p.metricsDB.GetAggregatedUsageWithPagination(ctx, conditions, pagination)
		if err != nil {
			return nil, err
		}

		if len(records) == 0 {
			break // No more records
		}

		allRecords = append(allRecords, records...)
		offset += len(records)

		// Break if we get fewer records than page size (last page)
		if len(records) < pageSize {
			break
		}
	}

	return allRecords, nil
}

// fetchAllHydratedMetricsWithPagination fetches all hydrated metrics with pagination to handle large datasets efficiently
func (p *BillingProvider) fetchAllHydratedMetricsWithPagination(ctx context.Context, filter map[string]interface{}) ([]datamodel2.HydratedMetrics, error) {
	var allMetrics []datamodel2.HydratedMetrics
	pageSize := p.config.PoolVolumeLabelPageSize
	offset := 0

	// Extract conditions from filter if present
	var conditions [][]interface{}
	if conditionsFromFilter, ok := filter["conditions"]; ok {
		if condArr, ok := conditionsFromFilter.([][]interface{}); ok {
			conditions = condArr
		}
	}

	for {
		pagination := &dbutils.Pagination{
			Limit:  pageSize,
			Offset: offset,
		}

		metrics, err := p.metricsDB.GetHydratedMetricsWithPagination(ctx, conditions, pagination)
		if err != nil {
			return nil, err
		}

		if len(metrics) == 0 {
			break // No more records
		}

		allMetrics = append(allMetrics, metrics...)
		offset += len(metrics)

		// Break if we get fewer records than page size (last page)
		if len(metrics) < pageSize {
			break
		}
	}

	return allMetrics, nil
}

// BytesToMiB converts bytes to MiB (Mebibytes)
func BytesToMiB(bytes float64) float64 {
	const bytesInMiB = 1024 * 1024 // 1024^2
	return bytes / float64(bytesInMiB)
}

func setServiceLevelForCRR(schedule string) string {
	switch schedule {
	case vsa.VolumeReplicationSchedule10Minutely:
		return "1"
	case vsa.VolumeReplicationScheduleHourly:
		return "2"
	case vsa.VolumeReplicationScheduleDaily:
		return "3"
	case ReplicationScheduleOntapMode:
		return "4"
	default:
		return ""
	}
}

// isBackupBillingMeasuredType returns true for backup-related measured types that carry a
// /backups/mode label (ONTAP vs DEFAULT).
func isBackupBillingMeasuredType(t metadata.MeasuredType) bool {
	switch t {
	case metadata.BackupLogicalSize,
		metadata.BackupEnabledVolumeAllocatedSize,
		metadata.CbsCrossRegionVolumeBackupTransferBytes:
		return true
	default:
		return false
	}
}

// isHybridMigrationOrOnPremReplicationType is true for hybrid replication API types where
// transfer-byte billing may skip until the first positive counter sample (initialize/update).
func isHybridMigrationOrOnPremReplicationType(replicationType string) bool {
	switch replicationType {
	case string(clientmodel.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION),
		string(clientmodel.VolumeReplicationCVPV1betaHybridReplicationTypeMIGRATION):
		return true
	default:
		return false
	}
}

func shouldSkipBaselineBillingForHybridReplication(metrics common.TimeSeries, replicationType string, skipHybridReplicationBaselineBilling bool) bool {
	if metrics.Metadata.ResourceType == metadata.VolumeReplicationRelationship && isHybridMigrationOrOnPremReplicationType(replicationType) && skipHybridReplicationBaselineBilling {
		return true
	}
	return false
}

// replicationCounterPointsSplitTillFirstUpdate partitions points at the first positive sample whose
// transfer_type is "update". prefix ends at that sample (inclusive); suffix starts at the same sample.
// split is false when:
//   - the input is empty,
//   - the first sample is already positive with no transfer_type (e.g., a synthetic baseline
//     prepended from a legacy cached counter with no transfer_type information), or
//   - no positive "update" sample exists in the input (e.g., the window is entirely baseline).
//
// The function never dereferences a nil TransferType.
func replicationCounterPointsSplitTillFirstUpdate(points []common.DataPoint) (prefix []common.DataPoint, suffix []common.DataPoint, split bool) {
	if len(points) == 0 {
		return nil, points, false
	}
	if points[0].Quantity > 0 && points[0].TransferType == nil {
		return nil, points, false
	}

	splitIndex := -1
	for i, point := range points {
		if point.Quantity > 0 && point.TransferType != nil && *point.TransferType == TransferTypeUpdate {
			splitIndex = i
			break
		}
	}
	if splitIndex == -1 {
		return nil, points, false
	}

	return points[:splitIndex+1], points[splitIndex:], true
}

func isPoolResourceType(rt metadata.ResourceType) bool {
	return rt == metadata.VolumePool || rt == metadata.VolumePoolRegionalHA
}

// isAutoTieringBillingMetric returns true if the measured type is an auto-tiering billing metric
func isAutoTieringBillingMetric(measuredType metadata.MeasuredType) bool {
	switch measuredType {
	case metadata.CoolTierDataReadSizeRaw,
		metadata.CoolTierDataWriteSizeRaw,
		metadata.PoolHotTierProvisionedSize,
		metadata.PoolCapacityTierLogicalFootprint:
		return true
	}
	return false
}

// shouldSkipAutoTieringMetric determines if an auto-tiering metric should be skipped for a given pool.
func (p *BillingProvider) shouldSkipAutoTieringMetric(resourceIdentifier ResourceKey, resourceCollection *ResourceCollection, measuredType metadata.MeasuredType) (bool, string) {
	poolData, found := resourceCollection.PoolData[resourceIdentifier]

	if !found || !poolData.AllowAutoTiering {
		return true, "pool data not found or AllowAutoTiering disabled"
	}

	// Skip ONTAP mode (expert mode) pools unless ONTAP mode billing is enabled for Autotiering Metrics
	if poolData.IsONTAPMode && !p.config.EnableONTAPModeAutoTieringBilling {
		return true, "ONTAP mode pool with EnableONTAPModeAutoTieringBilling=false"
	}

	if !p.config.EnableFilesAutoTieringBilling && !poolData.HasOnlyBlockVolumes {
		return true, "not block-only pool with EnableFilesAutoTieringBilling=false"
	}

	return false, ""
}

// fetchAndCacheCounterValues fetches all latest aggregated counter values using pagination and builds the cache
func (p *BillingProvider) fetchAndCacheCounterValues(ctx context.Context, aggregationType string, pageSize int, logger log.Logger) (CounterAggregationCache, error) {
	result := make(CounterAggregationCache)
	offset := 0
	totalProcessed := 0
	batchCount := 0
	cachedCount := 0

	// Use pagination to fetch all records and build cache
	for {
		// Fetch paginated records using the database method
		usageRecords, err := p.metricsDB.GetLatestAggregatedUsageForAllResources(ctx, aggregationType, pageSize, offset)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch latest counter records from aggregated_usages (offset %d): %v", offset, err)
		}

		// Break if no records returned
		if len(usageRecords) == 0 {
			break
		}

		// Process current batch and populate cache
		for i := range usageRecords {
			record := &usageRecords[i]
			if record.LastCounterValue == nil {
				continue
			}

			// Use CounterAggregationCacheResourceKey as the cache key
			cacheKey := CounterAggregationCacheResourceKey{
				ResourceUUID: record.ResourceUUID,
				MeasuredType: metadata.MeasuredTypeFromValue(record.MeasuredType),
			}
			result[cacheKey] = &CounterAggregationCacheValue{
				LastCounterValue: record.LastCounterValue,
				LastTransferType: record.LastTransferType,
			}
			cachedCount++
			logger.Debugf("Cached counter value %.2f for ResourceUUID %s, MeasuredType %s from DB query",
				*record.LastCounterValue, record.ResourceUUID, record.MeasuredType)
		}

		totalProcessed += len(usageRecords)
		batchCount++
		logger.Debugf("Processed %d records in batch %d (offset: %d, total processed: %d, cached: %d)",
			len(usageRecords), batchCount, offset, totalProcessed, cachedCount)

		// Update offset for next iteration
		offset += pageSize
	}

	logger.Infof("Preloaded %d counter values into cache from %d total records in %d batches", cachedCount, totalProcessed, batchCount)
	return result, nil
}

// preloadCounterValues fetches the latest counter values for all resources directly from the database
// using a single query with window functions and pagination
func (p *BillingProvider) preloadCounterValues(ctx context.Context, aggregationStartTime, aggregationEndTime time.Time, logger log.Logger) (CounterAggregationCache, error) {
	// Get page size from config
	pageSize := p.config.PoolVolumeLabelPageSize

	// Fetch and cache counter values with pagination
	return p.fetchAndCacheCounterValues(ctx, "CounterAggregation", pageSize, logger)
}

// counterDeltaWithHistoryResult holds counter deltas for the current window, including an optional
// non-billable replication segment (bytes before the first positive counter sample) for persistence.
type counterDeltaWithHistoryResult struct {
	billed                   float64
	skippedQty               float64
	lastCounter              *float64
	skippedSegmentEndCounter *float64
	// lastTransferType is the transfer_type of the last data point that participated in the
	// billable delta. It mirrors lastCounter and is persisted on the billable AggregatedUsage row
	// as last_transfer_type so subsequent aggregation cycles can carry it forward via the cache.
	lastTransferType *string
	// skippedSegmentEndTransferType is the transfer_type of the split-point sample (the first
	// positive update sample). It mirrors skippedSegmentEndCounter and is persisted on the
	// non-billable baseline AggregatedUsage row as last_transfer_type.
	skippedSegmentEndTransferType *string
	// segmentSplitAt is the timestamp of the first strictly positive counter sample after a leading
	// non-positive run. Used to give skipped vs billed rows distinct
	// (aggregation_start, aggregation_end) for the aggregated_usages unique index.
	segmentSplitAt *time.Time
}

// calculateCounterDeltaWithAggregatedHistory adds the last aggregated counter value
// as first data point and uses the existing CounterDelta logic
func (p *BillingProvider) calculateCounterDeltaWithAggregatedHistory(ctx context.Context, resourceKey ResourceKey, dataPoints []common.DataPoint, measuredType metadata.MeasuredType, aggregationStartTime time.Time, counterCache CounterAggregationCache, resourceUUID string, logger log.Logger, skipBaselineBillingForHybridReplication bool) counterDeltaWithHistoryResult {
	// Create the cache key using ResourceUUID and MeasuredType
	cacheKey := CounterAggregationCacheResourceKey{
		ResourceUUID: resourceUUID,
		MeasuredType: measuredType,
	}
	cachedEntry := counterCache[cacheKey]
	var lastAggregatedCounterValue *float64
	var lastAggregatedTransferType *string
	if cachedEntry != nil {
		lastAggregatedCounterValue = cachedEntry.LastCounterValue
		lastAggregatedTransferType = cachedEntry.LastTransferType
	}
	// If no data points, return 0 and lastAggregatedCounterValue
	if len(dataPoints) == 0 {
		return counterDeltaWithHistoryResult{billed: 0, lastCounter: lastAggregatedCounterValue}
	}

	pointsForDelta := dataPoints

	// If we have a previous aggregated counter value, add it as the first data point
	if lastAggregatedCounterValue != nil {
		// Create a synthetic data point with the last aggregated counter value. Use
		// aggregationStartTime - 1 minute to ensure it sorts before current cycle data.
		// TransferType carries the cached LastTransferType forward so the split function and
		// IsBillable logic see the same "are we still in baseline?" answer across windows.
		lastCounterDataPoint := common.DataPoint{
			Timestamp:    aggregationStartTime.Add(-1 * time.Minute),
			Quantity:     *lastAggregatedCounterValue,
			TransferType: lastAggregatedTransferType,
		}

		// Prepend the last counter value to the data points
		pointsForDelta = append([]common.DataPoint{lastCounterDataPoint}, dataPoints...)

		logger.Debugf("Added last counter value %.2f from cache as starting point for resource %s, measured type %s",
			*lastAggregatedCounterValue, resourceUUID, measuredType)
	} else if isCbsCrossRegionBackupTransferMetric(measuredType) || skipBaselineBillingForHybridReplication {
		// First aggregation window for a cross-region backup: no prior counter
		// in the cache yet, so use zero as the baseline. This ensures the full
		// initial transfer bytes are counted instead of being dropped.
		zeroBaseline := common.DataPoint{
			Timestamp: aggregationStartTime.Add(-1 * time.Minute),
			Quantity:  0,
		}
		pointsForDelta = append([]common.DataPoint{zeroBaseline}, dataPoints...)

		logger.Debugf("Using zero baseline for CBS cross-region backup transfer resource %s (no prior counter in cache)", resourceUUID)
	}

	if skipBaselineBillingForHybridReplication {
		prefix, suffix, split := replicationCounterPointsSplitTillFirstUpdate(pointsForDelta)
		if split {
			skippedQty, skippedEnd := common.CounterDelta(prefix, logger, measuredType, resourceUUID)
			billed, last := common.CounterDelta(suffix, logger, measuredType, resourceUUID)
			splitAt := suffix[0].Timestamp
			// suffix[0] is the split point (first positive update sample) - its transfer_type
			// is what we persist on the non-billable baseline row. suffix[len-1] is the last
			// sample in the window - its transfer_type rides with the billable row.
			return counterDeltaWithHistoryResult{
				billed:                        billed,
				skippedQty:                    skippedQty,
				lastCounter:                   last,
				skippedSegmentEndCounter:      skippedEnd,
				lastTransferType:              suffix[len(suffix)-1].TransferType,
				skippedSegmentEndTransferType: suffix[0].TransferType,
				segmentSplitAt:                &splitAt,
			}
		}

		// split=false: no positive "update" sample exists in pointsForDelta. If the cached
		// LastTransferType indicates we're still in baseline (or there is no cache entry yet
		// for a hybrid replication resource), treat the entire window as a continuation of
		// the baseline transfer so it remains non-billable. Without this branch, plain
		// CounterDelta would bill ongoing baseline bytes for hybrid replication, which is
		// wrong.
		if inBaselineMode(cachedEntry) {
			skippedQty, skippedEnd := common.CounterDelta(pointsForDelta, logger, measuredType, resourceUUID)
			lastTT := pointsForDelta[len(pointsForDelta)-1].TransferType
			return counterDeltaWithHistoryResult{
				billed:                        0,
				skippedQty:                    skippedQty,
				lastCounter:                   skippedEnd,
				skippedSegmentEndCounter:      skippedEnd,
				lastTransferType:              lastTT,
				skippedSegmentEndTransferType: lastTT,
				// keeps the full [start, end] window on the non-billable row.
			}
		}
	}

	// Use existing CounterDelta logic
	aggregate, lastCounter := common.CounterDelta(pointsForDelta, logger, measuredType, resourceUUID)
	// pointsForDelta[len-1] is always the last real data point (synthetic baselines are only
	// prepended at the front), so its transfer_type is the right value to persist.
	return counterDeltaWithHistoryResult{
		billed:           aggregate,
		lastCounter:      lastCounter,
		lastTransferType: pointsForDelta[len(pointsForDelta)-1].TransferType,
	}
}

// inBaselineMode reports whether a hybrid replication resource is still in its baseline
// (initial) transfer phase based on its cached last transfer type.
//   - cache miss (no entry yet) → treated as baseline; hybrid replication resources start their
//     lifecycle in baseline, so the very first aggregation cycle should not bill any movement.
//   - cached LastTransferType == "update" → baseline has already been crossed.
//   - cached LastTransferType == "initialize" → baseline
//   - cached LastTransferType == nil & LastCounterValue > 0(legacy row from before the column existed) → preserve
//     pre-LastTransferType behavior by falling through to plain CounterDelta. Otherwise legacy
//     past-baseline resources would lose one cycle of billing post-deploy.
func inBaselineMode(cachedEntry *CounterAggregationCacheValue) bool {
	if cachedEntry == nil {
		return true
	}
	if nillable.IsNilOrEmpty(cachedEntry.LastTransferType) && (cachedEntry.LastCounterValue != nil && *cachedEntry.LastCounterValue == 0) {
		return true
	}
	if cachedEntry.LastTransferType != nil && *cachedEntry.LastTransferType == TransferTypeInitial {
		return true
	}
	return false
}

// fetchHistoricalVolumeSizeMetrics fetches aggregated volume size metrics from aggregated_usages table
// for pool-level auto-tiering billing when EnableATVolumeBasedPoolBilling is enabled.

func determineBillingMode(resourceData *ResourceData, aggregationEnd time.Time) datamodel2.BillingMode {
	if resourceData == nil || resourceData.FreeTrialEndAt == nil {
		return datamodel2.BillingModeCommercial
	}

	if !aggregationEnd.After(*resourceData.FreeTrialEndAt) {
		return datamodel2.BillingModeFreeTrial
	}

	return datamodel2.BillingModeCommercial
}

func applyFreeTrialSplit(series []common.TimeSeries, resourceData *ResourceData, aggregationType common.JobType) []common.TimeSeries {
	if resourceData == nil || resourceData.FreeTrialEndAt == nil || len(series) == 0 {
		return series
	}
	out := make([]common.TimeSeries, 0, len(series))
	for _, s := range series {
		out = append(out, splitTimeSeriesByFreeTrial(s, resourceData.FreeTrialEndAt, aggregationType)...)
	}
	return out
}

func splitTimeSeriesByFreeTrial(series common.TimeSeries, freeTrialEndAt *time.Time, aggregationType common.JobType) []common.TimeSeries {
	if freeTrialEndAt == nil || !series.AggregationStart.Before(*freeTrialEndAt) || !series.AggregationEnd.After(*freeTrialEndAt) {
		return []common.TimeSeries{series}
	}

	points := make([]common.DataPoint, len(series.DataPoints))
	copy(points, series.DataPoints)
	sort.Slice(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
	if len(points) == 0 {
		return []common.TimeSeries{series}
	}

	splitIdx := -1
	for i := range points {
		if !points[i].Timestamp.After(*freeTrialEndAt) {
			splitIdx = i
		}
	}
	if splitIdx < 0 || splitIdx >= len(points)-1 {
		return []common.TimeSeries{series}
	}

	freeTrialSeries := series
	commercialSeries := series
	freeTrialSeries.AggregationEnd = *freeTrialEndAt
	commercialSeries.AggregationStart = *freeTrialEndAt

	if aggregationType == common.CounterAggregation {
		freeTrialSeries.DataPoints = append([]common.DataPoint{}, points[:splitIdx+1]...)
		commercialSeries.DataPoints = append([]common.DataPoint{points[splitIdx]}, points[splitIdx+1:]...)
		return []common.TimeSeries{freeTrialSeries, commercialSeries}
	}

	pre := append([]common.DataPoint{}, points[:splitIdx+1]...)
	post := append([]common.DataPoint{}, points[splitIdx+1:]...)
	lastBefore := pre[len(pre)-1]
	if !lastBefore.Timestamp.Equal(*freeTrialEndAt) {
		boundary := common.DataPoint{Timestamp: *freeTrialEndAt, Quantity: lastBefore.Quantity, TransferType: lastBefore.TransferType}
		pre = append(pre, boundary)
		post = append([]common.DataPoint{boundary}, post...)
	}
	freeTrialSeries.DataPoints = pre
	commercialSeries.DataPoints = post
	return []common.TimeSeries{freeTrialSeries, commercialSeries}
}

// This aggregates volume-level metrics to pool-level by summing volumes within each pool.
func (p *BillingProvider) fetchHistoricalVolumeSizeMetrics(ctx context.Context, aggregationStartTime, aggregationEndTime time.Time, backfillLimit time.Duration, measuredType metadata.MeasuredType, resourceType metadata.ResourceType, resourceCollection *ResourceCollection, aggregatedRecords []datamodel2.AggregatedUsage) ([]datamodel2.HydratedMetrics, error) {
	logger := util.GetLogger(ctx)

	queryResourceType := metadata.Volume
	if resourceType == metadata.VolumePoolRegionalHA {
		queryResourceType = metadata.VolumeRegionalHA
	}

	var allRecords []datamodel2.AggregatedUsage
	for _, record := range aggregatedRecords {
		// Match the conditions: measured_type, resource_type, aggregation window, and is_billable
		if metadata.MeasuredTypeEquals(record.MeasuredType, measuredType) &&
			metadata.ResourceTypeEquals(record.ResourceType, queryResourceType) &&
			!record.AggregationStart.Before(aggregationStartTime.UTC()) &&
			!record.AggregationEnd.After(aggregationEndTime.UTC()) &&
			!record.IsBillable {
			allRecords = append(allRecords, record)
		}
	}

	logger.Infof("Fetched %d volume-level auto-tiering aggregated records for pool-level aggregation", len(allRecords))

	var metrics []datamodel2.HydratedMetrics

	for _, record := range allRecords {
		deploymentName := resourceCollection.VolumeToDeploymentName[record.ResourceUUID]
		if deploymentName == "" {
			logger.Warnf("No deployment name found for volume with ResourceUUID %s, skipping record", record.ResourceUUID)
			continue
		}
		poolName := resourceCollection.DeploymentNameToPoolName[deploymentName]
		if poolName == "" {
			logger.Warnf("No pool name found for volume with ResourceUUID %s in deployment %s, skipping record", record.ResourceUUID, deploymentName)
			continue
		}
		if record.VendorCustomerID == nil || record.RegionName == nil {
			logger.Warnf("Missing VendorCustomerID or RegionName for volume with ResourceUUID %s, skipping record", record.ResourceUUID)
			continue
		}
		metrics = append(metrics, datamodel2.HydratedMetrics{
			MetricTimestamp: aggregationEndTime.Add(-1 * time.Minute), // Use a timestamp before the aggregation end time to ensure it falls within the window
			MeasuredType:    metadata.MeasuredTypeValue(measuredType),
			ConsumerID:      *record.VendorCustomerID,
			ResourceType:    metadata.ResourceTypeValue(resourceType),
			ResourceName:    poolName,
			Location:        *record.RegionName,
			Quantity:        record.Quantity * 1024 * 1024, // converting back to bytes so processMetricsWithJobDef's BytesToMiB yields the correct result
			DeploymentName:  deploymentName,
		})
	}

	return metrics, nil
}

func getRegionCodeToLocationMap(regionNumberMapJSON string) (map[string]string, error) {
	if regionNumberMapJSON == "" {
		return map[string]string{}, nil
	}

	regionNumberMap := make(map[string]string)
	if err := json.Unmarshal([]byte(regionNumberMapJSON), &regionNumberMap); err != nil {
		return nil, err
	}

	regionCodeToLocation := make(map[string]string, len(regionNumberMap))
	for region, code := range regionNumberMap {
		regionCodeToLocation[code] = region
	}

	return regionCodeToLocation, nil
}

// getSourceLocationFromSourceDetails extracts the region from source_details string.
// source_details format: "gcnv-<id>-r<region_code>_..." (the "r" prefix is stripped before lookup).
// Example: "gcnv-608f72ece2b7c43-r34_gcnv-608f72ece2b7c43-svm-01:srcvol20march..."
// The region code (e.g., "34") is extracted and mapped to the actual region (e.g., "us-central1").
func getSourceLocationFromSourceDetails(sourceDetails string, regionCodeToLocation map[string]string) string {
	if sourceDetails == "" {
		return ""
	}

	parts := strings.Split(sourceDetails, "_")
	dashParts := strings.Split(parts[0], "-")
	if len(dashParts) < 3 {
		return ""
	}

	regionCode := strings.TrimPrefix(dashParts[len(dashParts)-1], "r")
	return regionCodeToLocation[regionCode]
}

// getDeploymentNameFromSourceDetails extracts the deployment name from source_details string.
// source_details format: "gcnv-<id>-<region_code>_gcnv-<id>-svm-01:vol_..."
func getDeploymentNameFromSourceDetails(sourceDetails string) string {
	if sourceDetails == "" {
		return ""
	}

	// Split by '_' to get the first part: "gcnv-<id>-<region_code>"
	parts := strings.Split(sourceDetails, "_")
	// First part is like "gcnv-4d01d92cfc96fcd-r34"
	// Split by '-' and rejoin all parts except the last (region code)
	firstPart := parts[0]
	dashParts := strings.Split(firstPart, "-")
	if len(dashParts) < 3 {
		return ""
	}

	// Rejoin everything except the last element (region code) to get the deployment name
	return strings.Join(dashParts[:len(dashParts)-1], "-")
}

// determineOntapReplicationType determines the replication type for ONTAP mode CRR based on
// source/destination region and zone comparison.
func determineOntapReplicationType(sourceRegion, destinationRegion *string, sourceDeploymentName, destDeploymentName string, deploymentToPoolInfo map[string]OntapPoolInfo, logger log.Logger) string {
	// by default assuming cross-region replication if region information is missing
	if sourceRegion == nil || destinationRegion == nil {
		return string(googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeCROSSREGIONREPLICATION)
	}

	if *sourceRegion != *destinationRegion {
		return string(googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeCROSSREGIONREPLICATION)
	}

	srcPool, _ := deploymentToPoolInfo[sourceDeploymentName]
	dstPool, _ := deploymentToPoolInfo[destDeploymentName]

	if srcPool.PrimaryZone == dstPool.PrimaryZone {
		return string(googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTRAZONEREPLICATION)
	}
	return string(googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTERZONEREPLICATION)
}

func isCbsCrossRegionBackupTransferMetric(mt metadata.MeasuredType) bool {
	return mt == metadata.CbsCrossRegionVolumeBackupTransferBytes
}

func validateAggregatedUsageForPersist(record *datamodel2.AggregatedUsage, index int) error {
	if err := metadata.ValidateMeasuredTypeValue(record.MeasuredType); err != nil {
		return fmt.Errorf("aggregated usage record %d: %w", index, err)
	}
	if err := metadata.ValidateResourceTypeValue(record.ResourceType); err != nil {
		return fmt.Errorf("aggregated usage record %d: %w", index, err)
	}
	return nil
}

// filterValidAggregatedUsage splits records into those safe to persist and
// those with unknown persisted enum values.
func filterValidAggregatedUsage(records []datamodel2.AggregatedUsage) (valid, invalid []datamodel2.AggregatedUsage) {
	valid = make([]datamodel2.AggregatedUsage, 0, len(records))
	for i := range records {
		if err := validateAggregatedUsageForPersist(&records[i], i); err != nil {
			invalid = append(invalid, records[i])
			continue
		}
		valid = append(valid, records[i])
	}
	return valid, invalid
}
