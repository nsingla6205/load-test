package processor

import (
	"context"
	"errors"
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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
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
	metricsRecorder      monitoring.MetricsRecorder
}

func NewMetricsProcessor(
	vcpDatastore database.Storage, telemetryDatastore metricdb.Storage,
	sink performance.Sink, metricsProvider collector.VolumeMetricsProvider,
	billingProvider *aggregator.BillingProvider, bizopsProvider bizops.BizOpsProvider,
	metricsRecorder monitoring.MetricsRecorder,
) MetricsProcessor {
	return MetricsProcessor{
		vcpDatastore:         vcpDatastore,
		telemetryDatastore:   telemetryDatastore,
		sink:                 sink,
		googleMetricProvider: metricsProvider,
		billingProvider:      billingProvider,
		bizopsProvider:       bizopsProvider,
		metricsRecorder:      metricsRecorder,
	}
}

func (mp *MetricsProcessor) ProcessPerformanceMetrics(ctx context.Context, timestamp time.Time) error {
	logger := util.GetLogger(ctx)
	telemetryConfig := common.LoadConfig()
	runStartedAt := time.Now()
	runStatus := "success"
	if mp.metricsRecorder != nil {
		defer func() {
			mp.metricsRecorder.RecordPerformanceRunDuration(&monitoring.MetricRecorderParams{
				JobStatus:       runStatus,
				LatencyDuration: time.Since(runStartedAt).Seconds(),
			})
		}()
	}

	logger.Infof("Process %s!\n", "Performance Metrics")
	var processingErr error
	if telemetryConfig.EnableVolumeMetrics {
		var fanoutErr error
		if mp.googleMetricProvider == nil {
			logger.Error("Volume metrics provider is nil")
			fanoutErr = errors.New("volume metrics provider is nil")
		} else if err := mp.processRawMetrics(ctx, timestamp); err != nil {
			fanoutErr = err
		}
		processingErr = errors.Join(processingErr, fanoutErr)
	}

	allHydratedMetricsDataModel, allHydratedMetrics, err := mp.collectAndProcessMetrics(ctx, telemetryConfig, timestamp)
	if err != nil {
		runStatus = "failure"
		return errors.Join(processingErr, fmt.Errorf("failed to collect and process metrics: %w", err))
	}

	// Store metrics in database. Invalid records are filtered out and logged so
	// a single bad metric cannot drop the whole batch. Sink delivery remains
	// independent, but a persist failure is returned after all synchronous work.
	var persistErr error
	if mp.telemetryDatastore != nil {
		validMetrics, invalidMetrics := filterValidHydratedMetrics(allHydratedMetricsDataModel)
		if len(invalidMetrics) > 0 {
			logger.Errorf("Dropping %d invalid hydrated metrics before persist", len(invalidMetrics))
		}
		if len(validMetrics) > 0 {
			if err := mp.telemetryDatastore.CreateHydratedMetricsBatch(ctx, validMetrics, int(telemetryConfig.PushBatchSize)); err != nil {
				logger.Errorf("Failed to insert hydrated metrics batch: %v", err)
				persistErr = fmt.Errorf("failed to insert hydrated metrics batch: %w", err)
				processingErr = errors.Join(processingErr, persistErr)
			}
		}
	} else {
		logger.Error("TelemetryDatastore is nil, cannot store metrics")
		persistErr = errors.New("telemetry datastore is nil")
		processingErr = errors.Join(processingErr, persistErr)
	}

	logger.Infof("Starting to deliver %d metrics to sink", len(allHydratedMetrics))
	var deliveryErr error
	var sinkFailedCount int
	if mp.sink != nil {
		var err error
		sinkFailedCount, err = mp.sink.DeliverMetrics(ctx, allHydratedMetrics)
		if err != nil {
			deliveryErr = fmt.Errorf("failed to deliver performance metrics: %w", err)
			processingErr = errors.Join(processingErr, deliveryErr)
		} else if sinkFailedCount > 0 {
			runStatus = "partial"
			logger.Warnf("Performance metrics sink reported %d delivery failures without hard error", sinkFailedCount)
		}
	} else {
		logger.Error("Sink is nil, cannot deliver metrics")
		deliveryErr = fmt.Errorf("performance metrics sink is nil")
		processingErr = errors.Join(processingErr, deliveryErr)
	}

	if processingErr != nil {
		runStatus = "failure"
	}
	return processingErr
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

func (mp *MetricsProcessor) processRawMetrics(ctx context.Context, timestamp time.Time) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Processing Raw Metrics")
	if err := collector.CollectVolumeMetrics(ctx, logger, mp.googleMetricProvider, timestamp); err != nil {
		return fmt.Errorf("collect raw metrics failed: %w", err)
	}
	return nil
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
	if mp.metricsRecorder != nil {
		mp.metricsRecorder.RecordReportSuccess(
			monitoring.ReportUsageAggregation,
			monitoring.SinkTypeDatabase,
			time.Now().UTC(),
		)
	}
	return nil
}

func (mp *MetricsProcessor) CollectMetrics(ctx context.Context, projectId string, timestamp time.Time) error {
	telemetryConfig := common.LoadConfig()
	logger := util.GetLogger(ctx)
	results, collectErr := mp.googleMetricProvider.CollectProjectMetrics(ctx, logger, projectId, timestamp)
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
	if collectErr != nil {
		logger.Error("Failed to get project metrics", "error", collectErr.Error())
		return nil
	}
	logger.Debugf("Successfully collected %d metrics %v", len(validResults), projectId)
	return nil
}

func (mp *MetricsProcessor) ProcessBizOps(ctx context.Context, params *utils.BizOpsReportParams) error {
	logger := util.GetLogger(ctx)
	if params == nil {
		return fmt.Errorf("BizOps report params must not be nil")
	}
	err := mp.bizopsProvider.ProcessBizOps(ctx, logger, params)
	if err != nil {
		logger.Error("Failed to process BizOps metrics", "error", err.Error())
		return err
	}
	if params.SinkType == utils.BizOpsSinkGCS && mp.metricsRecorder != nil {
		mp.metricsRecorder.RecordReportSuccess(
			monitoring.ReportBizOps,
			monitoring.SinkTypeGCS,
			time.Now().UTC(),
		)
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
