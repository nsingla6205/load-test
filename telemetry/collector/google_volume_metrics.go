package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	datamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/jobs"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var CollectVolumeMetrics = collectVolumeMetrics

const (
	// poolMetricAlignmentPeriod is the alignment period requested from Cloud
	// Monitoring. Harvest reports write data and write ops as totals over this
	// window, so pool rates are derived from it.
	poolMetricAlignmentPeriod = 5 * time.Minute
	bytesPerMebibyte          = 1024 * 1024
)

// poolAggregationKey identifies one pool-level series within a collection pass.
type poolAggregationKey struct {
	AccountName    string
	DeploymentName string
	PoolName       string
	Location       string
	MeasuredType   metadata.MeasuredType
}

func (g *GoogleTenantProjectProvider) GetTenantProjects(ctx context.Context, logger log.Logger) ([]string, error) {
	return GetTenantProject(ctx, logger, g.vcpDatastore)
}

func (g *GoogleVolumeMetricsProvider) GetVolumeMetrics(ctx context.Context, logger log.Logger, timestamp time.Time) error {
	projects, err := g.tenantProjectProvider.GetTenantProjects(ctx, logger)
	if err != nil {
		return fmt.Errorf("failed to get tenant projects: %v", err)
	}
	logger.Infof("Got projects: %v", projects)

	// Extract correlation ID from context for propagation to jobs
	correlationID := ""
	if loggerFields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		if corrIDStr, exists := loggerFields["requestCorrelationID"].(string); exists {
			correlationID = corrIDStr
		}
	}

	// Prepare all jobs for batch enqueuing
	var jobsToEnqueue []utils.Job
	for _, projectID := range projects {
		j := jobs.NewCollectMetrics(projectID, timestamp)
		if correlationID != "" {
			j.CorrelationID = correlationID
		}
		jobsToEnqueue = append(jobsToEnqueue, j)
	}

	// Batch enqueue all jobs
	if len(jobsToEnqueue) > 0 {
		if correlationID != "" {
			logger.Infof("Batch enqueueing %d CollectMetrics jobs with correlation ID: %s", len(jobsToEnqueue), correlationID)
		} else {
			logger.Infof("Batch enqueueing %d CollectMetrics jobs", len(jobsToEnqueue))
		}

		err = g.jobQueue.EnqueueBatch(ctx, jobsToEnqueue, utils.CollectionQueue)
		if err != nil {
			logger.Errorf("Failed to batch enqueue CollectMetrics jobs: %v", err)
			return err
		}
		logger.Infof("Successfully batch enqueued %d CollectMetrics jobs", len(jobsToEnqueue))
	}

	return nil
}

func (g *GoogleVolumeMetricsProvider) CollectProjectMetrics(ctx context.Context, logger log.Logger, projectID string, timestamp time.Time) ([]datamodel.HydratedMetrics, error) {
	var projectResults []datamodel.HydratedMetrics
	var perfMetrics []entity.HydratedMetric
	poolMetrics := make(map[poolAggregationKey]float64)
	projectName := fmt.Sprintf("projects/%s", projectID)
	telemetryConfig := common.LoadConfig()
	env := telemetryConfig.Environment
	for _, metric := range g.metrics {
		resourceType := "k8s_cluster"
		if env == "dev" {
			resourceType = "generic_task"
		}
		filter := fmt.Sprintf(`metric.type="%s/%s" AND resource.type="%s"`, metric.ResourceType, metric.Metric, resourceType)
		req := &monitoringpb.ListTimeSeriesRequest{
			Name:   projectName,
			Filter: filter,
			Interval: &monitoringpb.TimeInterval{
				StartTime: timestamppb.New(g.startTime),
				EndTime:   timestamppb.New(g.endTime),
			},
			View:     monitoringpb.ListTimeSeriesRequest_FULL,
			PageSize: telemetryConfig.PageSize,
		}

		agg := &monitoringpb.Aggregation{
			AlignmentPeriod: durationpb.New(poolMetricAlignmentPeriod),
			GroupByFields: []string{
				"metric.label.metric",
				"metric.label.volume",
				"metric.label.project",
				"metric.label.datacenter",
				"metric.label.pool_name",
				"metric.label.deployment_name",
				"metric.label.is_regional_ha",
				"metric.label.relationship_id",
				"metric.label.source_details",
				"metric.label.destination_details",
				"metric.label.last_transfer_type",
			},
		}
		if metric.MetricType == "performance" {
			agg.PerSeriesAligner = monitoringpb.Aggregation_ALIGN_MEAN
			agg.CrossSeriesReducer = monitoringpb.Aggregation_REDUCE_MEAN
		} else {
			agg.PerSeriesAligner = monitoringpb.Aggregation_ALIGN_MAX
			agg.CrossSeriesReducer = monitoringpb.Aggregation_REDUCE_MAX
		}
		req.Aggregation = agg

		it := g.client.ListTimeSeries(withQuotaProject(ctx, projectID, telemetryConfig.EnableTenantQuotaProject), req)
		for {
			resp, err := it.Next()
			if errors.Is(err, iterator.Done) {
				break
			}
			if err != nil {
				if status.Code(err) == codes.NotFound {
					break
				}
				logger.Errorf("Error retrieving time series data for metric %s in project %s: %v", metric, projectID, err)
				break
			}
			if len(resp.Points) == 0 {
				logger.Warnf("No data points found for metric %s in project %s for region %s", metric, projectID, resp.Resource.Labels["location"])
				continue
			}
			measuredType, exists := metadata.NewMeasuredType(metric.Metric)
			if !exists {
				logger.Warnf("Unknown measured type for metric %s", metric)
				continue
			}
			resourceType := metadata.CombinedKeyResourceTypeMeasuredTypeMap[metric.Metric].ResourceType

			if metric.MetricType == "performance" {
				value := extractValue(resp.Points[0].Value)
				if isPoolAggregationSource(measuredType) {
					if resp.Metric.Labels["pool_name"] == "" || resp.Metric.Labels["deployment_name"] == "" {
						logger.Warnf(
							"Skipping pool aggregation for metric %s with missing pool_name or deployment_name in project %s",
							metric.Metric,
							projectID,
						)
					} else {
						accumulatePoolMetric(poolMetrics, resp, measuredType, value)
					}
				}

				// volume_write_ops is an aggregate-only source. Harvest already
				// exports the first-party per-volume operation-count series.
				if isAggregateOnlyPerformanceMetric(measuredType) {
					continue
				}

				met := metadata.ResourceMetadata{}
				met.SetAccountName(resp.Metric.Labels["project"])
				met.SetResourceType(resourceType)
				switch resourceType {
				case metadata.Volume:
					met.SetResourceName(resp.Metric.Labels["volume"])
					met.SetResourceDisplayName(resp.Metric.Labels["volume"])
				case metadata.VolumePool:
					met.SetResourceName(resp.Metric.Labels["pool_name"])
					met.SetResourceDisplayName(resp.Metric.Labels["pool_name"])
				}
				met.SetRegionName(resp.Metric.Labels["datacenter"])
				if metric.Metric == "wafl_volume_cloud_bin_operation_size" && resp.Metric.Labels["metric"] != "put" {
					continue
				}
				if metric.Metric == "pool_cloud_bin_operation_size" && resp.Metric.Labels["metric"] != "put" {
					continue
				}
				perfMetrics = append(perfMetrics, setupHydratedMetric(timestamp, met, measuredType, value))
				continue
			}

			// Filter for "put" operations on pool_cloud_bin_operation_size_raw for billing
			if metric.Metric == "pool_cloud_bin_operation_size_raw" && resp.Metric.Labels["metric"] != "put" {
				continue
			}

			// Filter for "put" operations on wafl_volume_cloud_bin_operation_size_raw for billing
			if metric.Metric == "wafl_volume_cloud_bin_operation_size_raw" && resp.Metric.Labels["metric"] != "put" {
				continue
			}

			if metrics := setupHydratedMetrics(measuredType, resourceType, projectID, resp, timestamp); metrics != nil {
				projectResults = append(projectResults, *metrics)
			}
		}
	}
	perfMetrics = append(perfMetrics, buildPoolAggregatedMetrics(timestamp, poolMetrics)...)
	if g.googleSink != nil {
		g.googleSink.DeliverMetrics(ctx, perfMetrics)
	}
	return projectResults, nil
}

func isPoolAggregationSource(measuredType metadata.MeasuredType) bool {
	return measuredType == metadata.WriteIo || measuredType == metadata.WriteOps
}

func isAggregateOnlyPerformanceMetric(measuredType metadata.MeasuredType) bool {
	return measuredType == metadata.WriteOps
}

func poolUsedMeasuredType(source metadata.MeasuredType) (metadata.MeasuredType, bool) {
	switch source {
	case metadata.WriteIo:
		return metadata.PoolUsedThroughputMibps, true
	case metadata.WriteOps:
		return metadata.PoolUsedIops, true
	default:
		return "", false
	}
}

// normalizePoolMetricValue converts Harvest's per-window totals into the rate
// units declared by the storage pool metrics: MiB/s for throughput_limit and
// operations per second for iops_limit.
func normalizePoolMetricValue(source metadata.MeasuredType, value float64) (float64, bool) {
	alignmentSeconds := poolMetricAlignmentPeriod.Seconds()
	switch source {
	case metadata.WriteIo:
		return value / alignmentSeconds / bytesPerMebibyte, true
	case metadata.WriteOps:
		return value / alignmentSeconds, true
	default:
		return 0, false
	}
}

// accumulatePoolMetric adds one volume series value into its pool bucket.
// Series missing pool_name or deployment_name are ignored.
func accumulatePoolMetric(acc map[poolAggregationKey]float64, series *monitoringpb.TimeSeries, measuredType metadata.MeasuredType, value float64) {
	if series == nil || series.Metric == nil {
		return
	}

	labels := series.Metric.Labels
	if labels["pool_name"] == "" || labels["deployment_name"] == "" {
		return
	}

	poolMeasuredType, ok := poolUsedMeasuredType(measuredType)
	if !ok {
		return
	}
	normalizedValue, ok := normalizePoolMetricValue(measuredType, value)
	if !ok {
		return
	}

	key := poolAggregationKey{
		AccountName:    labels["project"],
		DeploymentName: labels["deployment_name"],
		PoolName:       labels["pool_name"],
		Location:       labels["datacenter"],
		MeasuredType:   poolMeasuredType,
	}
	acc[key] += normalizedValue
}

// buildPoolAggregatedMetrics converts accumulated sums into pool-level hydrated metrics.
func buildPoolAggregatedMetrics(timestamp time.Time, acc map[poolAggregationKey]float64) []entity.HydratedMetric {
	metrics := make([]entity.HydratedMetric, 0, len(acc))
	for key, value := range acc {
		resourceMetadata := metadata.ResourceMetadata{}
		resourceMetadata.SetAccountName(key.AccountName)
		resourceMetadata.SetDeploymentName(key.DeploymentName)
		resourceMetadata.SetResourceName(key.PoolName)
		resourceMetadata.SetResourceDisplayName(key.PoolName)
		resourceMetadata.SetResourceType(metadata.VolumePool)
		resourceMetadata.SetRegionName(key.Location)

		metrics = append(metrics, setupHydratedMetric(timestamp, resourceMetadata, key.MeasuredType, value))
	}
	return metrics
}

func GetTenantProject(ctx context.Context, logger log.Logger, vcpDatastore database.Storage) ([]string, error) {
	projects, err := vcpDatastore.ListTpProjects(ctx)
	if err != nil {
		logger.Error("Failed to list SnHostsProjects", "error", err.Error())
		return nil, fmt.Errorf("failed to list SnHostsProjects: %v", err)
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("no projects found from DB")
	}

	return projects, nil
}

const quotaProjectHeader = "x-goog-user-project"

func withQuotaProject(ctx context.Context, projectID string, enabled bool) context.Context {
	if !enabled {
		return ctx
	}
	return grpcmetadata.AppendToOutgoingContext(ctx, quotaProjectHeader, projectID)
}

func collectVolumeMetrics(ctx context.Context, logger log.Logger, provider VolumeMetricsProvider, timestamp time.Time) error {
	return provider.GetVolumeMetrics(ctx, logger, timestamp)
}

func setupHydratedMetrics(measuredType metadata.MeasuredType, resourceType metadata.ResourceType, projectID string, resp *monitoringpb.TimeSeries, timestamp time.Time) *datamodel.HydratedMetrics {
	// Determine resource name based on ORIGINAL resource type (before HA modification)
	isPoolMetric := resourceType == metadata.VolumePool || resourceType == metadata.VolumePoolRegionalHA
	resourceName := resp.Metric.Labels["volume"]
	if isPoolMetric {
		// For pool-level metrics, use pool_name instead of volume
		resourceName = resp.Metric.Labels["pool_name"]
	}

	// Now modify resourceType for regional HA after we've determined resourceName
	if resp.Metric.Labels["is_regional_ha"] == "true" && resourceType != metadata.VolumeReplicationRelationship {
		if isPoolMetric {
			resourceType = metadata.VolumePoolRegionalHA
		} else {
			resourceType = metadata.VolumeRegionalHA
		}
	}

	// Skip metrics with empty resource name to prevent downstream billing/aggregation issues
	// Note: VolumeReplicationRelationship metrics get their resourceName from relationship_id below
	if resourceName == "" && resourceType != metadata.VolumeReplicationRelationship {
		util.GetLogger(context.Background()).Warn("Skipping metric with empty resource name",
			"resourceType", resourceType, "measuredType", measuredType, "projectID", projectID,
			"metricType", resp.Metric.Type, "datacenter", resp.Metric.Labels["datacenter"],
			"deploymentName", resp.Metric.Labels["deployment_name"])
		return nil
	}

	hydrateMetrics := datamodel.HydratedMetrics{
		MetricTimestamp: timestamp,
		MeasuredType:    metadata.MeasuredTypeValue(measuredType),
		ConsumerID:      resp.Metric.Labels["project"],
		ResourceType:    metadata.ResourceTypeValue(resourceType),
		ResourceName:    resourceName,
		Location:        resp.Metric.Labels["datacenter"],
		Quantity:        extractValue(resp.Points[0].Value),
		DeploymentName:  resp.Metric.Labels["deployment_name"],
	}
	if resourceType == metadata.VolumeReplicationRelationship {
		// TODO: need to update this to replication name
		hydrateMetrics.ResourceName = resp.Metric.Labels["relationship_id"]
		metaDataMap := map[string]string{}
		if sd := resp.Metric.Labels["source_details"]; sd != "" {
			metaDataMap["source_details"] = sd
		}
		if dd := resp.Metric.Labels["destination_details"]; dd != "" {
			metaDataMap["destination_details"] = dd
		}
		if tp := resp.Metric.Labels["last_transfer_type"]; tp != "" {
			metaDataMap["last_transfer_type"] = tp
		}
		if len(metaDataMap) > 0 {
			if metadataJSON, err := json.Marshal(metaDataMap); err == nil {
				hydrateMetrics.Metadata = metadataJSON
			}
		}
	}

	if isVolumeATRawMetric(resourceType, measuredType) {
		if poolName := resp.Metric.Labels["pool_name"]; poolName != "" {
			extra := map[string]string{"pool_name": poolName}
			if b, err := json.Marshal(extra); err == nil {
				hydrateMetrics.Metadata = b
			}
		}
	}

	return &hydrateMetrics
}

func isVolumeATRawMetric(rt metadata.ResourceType, mt metadata.MeasuredType) bool {
	return rt == metadata.Volume && (mt == metadata.CoolTierDataReadSizeRaw || mt == metadata.CoolTierDataWriteSizeRaw)
}

func extractValue(Value *monitoringpb.TypedValue) float64 {
	var quantity float64
	switch v := Value.Value.(type) {
	case *monitoringpb.TypedValue_DoubleValue:
		quantity = v.DoubleValue
	case *monitoringpb.TypedValue_Int64Value:
		quantity = float64(v.Int64Value)
	case *monitoringpb.TypedValue_BoolValue:
		if v.BoolValue {
			quantity = 1.0
		} else {
			quantity = 0.0
		}
	default:
		quantity = 0
	}
	return quantity
}
