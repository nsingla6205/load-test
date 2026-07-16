package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/davecgh/go-spew/spew"
	metricdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	datamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/aggregator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/bizops"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/collector"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/performance"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type MetricsProcessor struct {
	common.VCPProcessor
	vcpDatastore         database.Storage
	telemetryDatastore   metricdb.Storage
	sink                 performance.Sink
	googleMetricProvider collector.VolumeMetricsProvider
	billingProvider      *aggregator.BillingProvider
	bizopsProvider       bizops.BizOpsProvider
}

func NewMetricsProcessor(
	vcpDatastore database.Storage, telemetryDatastore metricdb.Storage,
	sink performance.Sink, metricsProvider collector.VolumeMetricsProvider,
	billingProvider *aggregator.BillingProvider, bizopsProvider bizops.BizOpsProvider,
) MetricsProcessor {
	return MetricsProcessor{vcpDatastore: vcpDatastore, telemetryDatastore: telemetryDatastore, sink: sink, googleMetricProvider: metricsProvider, billingProvider: billingProvider, bizopsProvider: bizopsProvider}
}

func (mp *MetricsProcessor) ProcessPerformanceMetrics(ctx context.Context) error {
	logger := util.GetLogger(ctx)
	telemetryConfig := common.LoadConfig()
	logger.Infof("Process %s!\n", "Performance Metrics")
	currentTimestamp := time.Now().Truncate(time.Minute)
	// The hydration/delivery work is intentionally fire-and-forget (it outlives
	// the job's Perform), so detach it from the job context's cancellation but
	// keep the same absolute deadline when the job context has one.
	// Run metrics collection and processing in a goroutine
	go func() {
		asyncCtx, cancel := asyncJobContext(ctx)
		defer cancel()
		asyncLogger := util.GetLogger(asyncCtx)

		// Collect and process all metrics
		allHydratedMetricsDataModel, allHydratedMetrics, err := mp.collectAndProcessMetrics(asyncCtx, telemetryConfig, currentTimestamp)
		if err != nil {
			asyncLogger.Errorf("Failed to collect and process metrics: %v", err)
			return
		}

		// Store metrics in database. Invalid records are filtered out and logged
		// so a single bad metric cannot drop the whole batch, and a persist
		// failure must not block sink delivery below.
		if mp.telemetryDatastore != nil {
			validMetrics, invalidMetrics := filterValidHydratedMetrics(allHydratedMetricsDataModel)
			if len(invalidMetrics) > 0 {
				asyncLogger.Errorf("Dropping %d invalid hydrated metrics before persist", len(invalidMetrics))
			}
			if len(validMetrics) > 0 {
				if err := mp.telemetryDatastore.CreateHydratedMetricsBatch(asyncCtx, validMetrics, int(telemetryConfig.PushBatchSize)); err != nil {
					asyncLogger.Errorf("Failed to insert hydrated metrics batch: %v", err)
				}
			}
		} else {
			asyncLogger.Error("TelemetryDatastore is nil, cannot store metrics")
		}

		// Deliver metrics to sink independently of the persist outcome above.
		asyncLogger.Infof("Starting to deliver %d metrics to sink", len(allHydratedMetrics))
		if mp.sink != nil {
			mp.sink.DeliverMetrics(asyncCtx, allHydratedMetrics)
		} else {
			asyncLogger.Error("Sink is nil, cannot deliver metrics")
		}
	}()

	// Process raw metrics asynchronously if enabled
	if telemetryConfig.EnableVolumeMetrics {
		go func(ctx context.Context) {
			metricClient := mp.googleMetricProvider.GetClient()
			if metricClient == nil {
				logger.Error("Metric client is nil")
				return
			}

			asyncCtx, cancel := asyncJobContext(ctx)
			defer cancel()
			mp.processRawMetrics(asyncCtx, currentTimestamp)
		}(ctx)
	}
	return nil
}

// asyncJobContext detaches from job cancellation but keeps ctx values and its
// absolute deadline (JOB_MAX_RUNTIME from job start) when present.
func asyncJobContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(base, deadline)
	}
	return base, func() {}
}

// collectAndProcessMetrics collects metrics from all sources and aggregates them
func (mp *MetricsProcessor) collectAndProcessMetrics(ctx context.Context, telemetryConfig *common.TelemetryConfig, timestamp time.Time) ([]datamodel.HydratedMetrics, []entity.HydratedMetric, error) {
	logger := util.GetLogger(ctx)

	// hydrated metrics data model for database storage
	var allHydratedMetricsDataModel []datamodel.HydratedMetrics

	// hydrated metrics for sink delivery
	var allHydratedMetrics []entity.HydratedMetric

	// Collect pool metrics
	poolMetricsResult, err := collector.GetPoolMetrics(ctx, mp.vcpDatastore, telemetryConfig, timestamp)
	if err != nil {
		logger.Error("Failed to get pool metrics", "error", err.Error())
		return nil, nil, err
	}

	logger.Infof("Pool metrics collected %d", len(poolMetricsResult.HydratedMetrics))

	// Collect backup metrics if enabled
	var backupMetricsResult *collector.BackupMetricsResult
	if telemetryConfig.EnableBackupMetrics || telemetryConfig.EnableBackupBillingMetrics {
		backupMetricsResult, err = collector.GetBackupMetrics(ctx, mp.vcpDatastore, telemetryConfig, timestamp)
		if err != nil {
			logger.Error("Failed to get backup metrics", "error", err.Error())
			return nil, nil, err
		}
	}

	// Collect backup vault CMEK rotation metrics if enabled
	var backupVaultMetricsResult *collector.BackupVaultMetricsResult
	if telemetryConfig.EnableBackupVaultMetrics {
		backupVaultMetricsResult, err = collector.GetBackupVaultMetrics(ctx, mp.vcpDatastore, telemetryConfig, timestamp)
		if err != nil {
			logger.Error("Failed to get backup vault metrics", "error", err.Error())
			return nil, nil, err
		}
		logger.Infof("Backup vault metrics collected %d", len(backupVaultMetricsResult.HydratedMetrics))
	}

	// Collect volume metrics
	volumeMetricsResult, err := collector.GetVolumeMetrics(ctx, mp.vcpDatastore, telemetryConfig, poolMetricsResult.PoolMetadataMap, timestamp)
	if err != nil {
		logger.Error("Failed to get volume metrics", "error", err.Error())
		return nil, nil, err
	}

	// Aggregate backup metrics for sink delivery
	if telemetryConfig.EnableBackupMetrics {
		allHydratedMetrics = append(allHydratedMetrics, backupMetricsResult.HydratedMetrics...)
	}

	// Aggregate backup vault metrics for sink delivery
	if telemetryConfig.EnableBackupVaultMetrics && backupVaultMetricsResult != nil {
		allHydratedMetrics = append(allHydratedMetrics, backupVaultMetricsResult.HydratedMetrics...)
	}

	// Aggregate backup and volume metrics for database storage
	if telemetryConfig.EnableBackupBillingMetrics {
		allHydratedMetricsDataModel = append(allHydratedMetricsDataModel, backupMetricsResult.HydratedMetricsDataModel...)
		allHydratedMetricsDataModel = append(allHydratedMetricsDataModel, volumeMetricsResult.HydratedMetricsDataModel...)
	}

	if telemetryConfig.SFRMetricsEnabled {
		allHydratedMetrics = append(allHydratedMetrics, volumeMetricsResult.SFRHydratedMetrics...)
	}

	// Aggregate cross-region backup & restore billing metrics if enabled
	if telemetryConfig.EnableCrossRegionBackupBillingMetrics {
		crossRegionRestoreResult, err := collector.ProcessRestoreBillingMetrics(ctx, mp.vcpDatastore, mp.telemetryDatastore, telemetryConfig, timestamp)
		if err != nil {
			logger.Error("Failed to get cross-region restore metrics", "error", err.Error())
		} else if crossRegionRestoreResult != nil {
			allHydratedMetricsDataModel = append(allHydratedMetricsDataModel, crossRegionRestoreResult.HydratedMetricsDataModel...)
		}
	}

	// Aggregate pool metrics for database storage
	allHydratedMetricsDataModel = append(allHydratedMetricsDataModel, poolMetricsResult.HydratedMetricsDataModel...)

	// Aggregate pool and volume metrics for sink delivery
	allHydratedMetrics = append(allHydratedMetrics, poolMetricsResult.HydratedMetrics...)
	allHydratedMetrics = append(allHydratedMetrics, volumeMetricsResult.VolumeAllocatedThroughputHydratedMetrics...)
	return allHydratedMetricsDataModel, allHydratedMetrics, nil
}

func (mp *MetricsProcessor) processRawMetrics(ctx context.Context, timestamp time.Time) {
	logger := util.GetLogger(ctx)
	logger.Infof("Processing Raw Metrics")
	mp.googleMetricProvider.RefreshTimeWindow()
	err := collector.CollectVolumeMetrics(ctx, logger, mp.googleMetricProvider, timestamp)
	if err != nil {
		logger.Errorf("CollectRawMetrics failed: %v", err)
		return
	}
}

func (mp *MetricsProcessor) ProcessUsageMetrics(ctx context.Context, timestamp time.Time) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Process %s!\n", "Usage Metrics")
	// Shift the aggregation cycle 15 mins prior from the aggregation trigger, to avoid 1 missed sample intermittently.
	// If aggregation is triggered at 1:45 -> It needs to aggregate data from 12:45 to 1:45. But as the current timestamp in also ~1:45.
	// There is a chance that the collection cycle is still running and record for 1:45 is still not hydrated. So aggregation will miss that record and will result into under billing.
	// With this approach, aggregator will aggregate data from 12:30 to 1:30 (This will make sure that 1:30 sample is always available as collection for 1:30 is already finished by 1:45).
	aggregationEndTime := timestamp
	err := mp.billingProvider.ProcessBillingMetrics(ctx, aggregationEndTime)
	if err != nil {
		logger.Error("Failed to aggregate hydrated metrics", "error", err.Error())
		return err
	}
	return nil
}

func (mp *MetricsProcessor) CollectMetrics(ctx context.Context, projectId string, timestamp time.Time) error {
	telemetryConfig := common.LoadConfig()
	logger := util.GetLogger(ctx)
	results, err := mp.googleMetricProvider.CollectProjectMetrics(ctx, logger, projectId, timestamp)
	if err != nil {
		logger.Error("Failed to get project metrics", "error", err.Error())
		return err
	}
	validResults, invalidResults := filterValidHydratedMetrics(results)
	if len(invalidResults) > 0 {
		logger.Errorf("Dropping %d invalid hydrated metrics before persist for project %v", len(invalidResults), projectId)
	}
	if len(validResults) > 0 {
		if err := mp.telemetryDatastore.CreateHydratedMetricsBatch(ctx, validResults, int(telemetryConfig.PushBatchSize)); err != nil {
			logger.Errorf("Failed to insert hydrated metrics batch: %v", err)
			return err
		}
	}
	logger.Debugf("Successfully collected %d metrics %v", len(validResults), projectId)
	return nil
}

func (mp *MetricsProcessor) ProcessBizOps(ctx context.Context, params *utils.BizOpsReportParams) error {
	logger := util.GetLogger(ctx)
	err := mp.bizopsProvider.ProcessBizOps(ctx, logger, params)
	if err != nil {
		logger.Error("Failed to process BizOps metrics", "error", err.Error())
		return err
	}
	logger.Infof("Successfully processed BizOps Report for start time: '%v' with timezone:'%s' ", params.StartDate, params.TimeZone)
	return nil
}

func (mp *MetricsProcessor) ProcessBillingSubmission(ctx context.Context, aggregationEndTime time.Time) error {
	logger := util.GetLogger(ctx)
	config := common.LoadConfig()
	maxRetries := config.MaxGoogleBillingPushRetry
	retryInterval := config.RetryIntervalSeconds
	logger.Infof("Processing Billing Retry for aggregation window ending at %v", aggregationEndTime)
	// Get unsent records from current aggregation window
	unsentRecords, err := mp.billingProvider.GetUnsentGoogleUsages(ctx, int64(maxRetries), aggregationEndTime)
	if err != nil {
		logger.Error("Failed to get unsent Google usages", "error", err)
		return err
	}

	if len(unsentRecords) == 0 {
		logger.Info("All billing records Submitted successfully")
		return nil
	}

	logger.Debugf("Starting retry processing for %d unsent billing records. Records: %s", len(unsentRecords), spew.Sdump(unsentRecords))
	// Deliver the metrics to sink
	failedCount, err := mp.billingProvider.GetUsageSink().DeliverMetrics(ctx, unsentRecords, aggregationEndTime)
	if err != nil {
		logger.Error("Failed to deliver metrics in retry attempt", "error", err)
		return err
	}

	successfulCount := len(unsentRecords) - failedCount
	logger.Info(fmt.Sprintf("Completed retry processing, processedRecords: %d, successfulRecords: %d, failedRecords: %d", len(unsentRecords), successfulCount, failedCount))

	if failedCount > 0 {
		select {
		case <-time.After(time.Duration(retryInterval) * time.Second):
			// Normal wait completed
		case <-ctx.Done():
			// Context cancelled, exit early
			return fmt.Errorf("billing retry interrupted by context cancellation: %w", ctx.Err())
		}
		return fmt.Errorf("billing retry processing completed with %d failed records", failedCount)
	}

	return nil
}

// filterValidHydratedMetrics splits metrics into those safe to persist and
// those with unknown persisted enum values. Filtering (rather than aborting on
// the first bad record) ensures a single invalid metric cannot drop an entire
// collection cycle. Callers are expected to log/count the invalid records.
func filterValidHydratedMetrics(metrics []datamodel.HydratedMetrics) (valid, invalid []datamodel.HydratedMetrics) {
	valid = make([]datamodel.HydratedMetrics, 0, len(metrics))
	for i := range metrics {
		metric := &metrics[i]
		if metadata.ValidateMeasuredTypeValue(metric.MeasuredType) != nil ||
			metadata.ValidateResourceTypeValue(metric.ResourceType) != nil {
			invalid = append(invalid, *metric)
			continue
		}
		valid = append(valid, *metric)
	}
	return valid, invalid
}
