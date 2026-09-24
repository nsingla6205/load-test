package googlePusher

import (
	"context"
	stderrors "errors"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/bizops"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/servicecontrol/v1"
	"google.golang.org/api/transport"
)

const (
	NorthAmericaContinent               = "northamerica"
	EuropeContinent                     = "europe"
	RegionPartsCount                    = 2
	ZonePartsCount                      = 3
	VolumeReplicationSchedule10Minutely = "10Minutely"
	VolumeReplicationScheduleHourly     = "Hourly"
	VolumeReplicationScheduleDaily      = "Daily"
	ReplicationModeOntap                = "ONTAP"
	ReplicationTypeDataMigration        = "DATA_MIGRATION"
	BackupModeOntap                     = "ONTAP"
	BackupModeDefault                   = "DEFAULT"
)

var (
	googleContinents                = env.GetString("GOOGLE_CONTINENTS", "")
	useNewLabelForHybridReplication = env.GetBool("USE_NEW_LABEL_FOR_HYBRID_REPLICATION", false)
	getContinentMap                 = bizops.GetContinentMap
	getResourceUUID                 = _getResourceUUID
	getFrequency                    = _getFrequency
	getReplicationType              = _getReplicationType
	getContinent                    = _getContinent
	getSourceRegion                 = _getSourceRegion
	getDestinationRegion            = _getDestinationRegion
	getServiceLevel                 = _getServiceLevel

	// allowedEmptyLabels defines labels that are allowed to have empty values in metrics
	allowedEmptyLabels = []string{
		"/replication/source_service_level",
		"/replication/destination_service_level",
		"/replication/source_continent",
		"/replication/source_region",
		"/replication/destination_region",
		"/replication/frequency",
		"/replication/mode",
		"/backups/mode",
	}
)

type Operation servicecontrol.Operation

type MetricValue *servicecontrol.MetricValue
type MetricValueSet servicecontrol.MetricValueSet

var (
	re = regexp.MustCompile(`^\d+$`)
)

type ResourceInfo struct {
	CustomerId   string
	ResourceId   string
	ResourceName string
}

type GoogleMetricsClient struct {
	rootURL                 string
	nameAndKeyLabelOfMetric map[metadata.CombinedKeyResourceTypeMeasuredType]common.Triple
	logger                  log.Logger
	config                  *common.TelemetryConfig
	mockMode                bool
}

func NewGoogleMetricsClient(ctx context.Context, rootURL string, config *common.TelemetryConfig) *GoogleMetricsClient {
	mockMode := env.GetBool("MOCK_GOOGLE_METRICS", false)
	logger := util.GetLogger(ctx)
	if mockMode {
		logger.Info("GoogleMetricsClient initialized in MOCK mode - metrics will not be sent to Google")
	}
	return &GoogleMetricsClient{
		rootURL:                 rootURL,
		nameAndKeyLabelOfMetric: common.CreateMetricsMappingMap(),
		logger:                  logger,
		config:                  config,
		mockMode:                mockMode,
	}
}

// ReportMetrics reports metrics and returns how many were dropped before a Service Control operation was built.
func (client *GoogleMetricsClient) ReportMetrics(ctx context.Context, metrics []common.GoogleMetric, operationStartTime, operationEndTime int64, wg *sync.WaitGroup, resultChan chan []common.MetricsResult) (dropped int) {
	defer wg.Done()
	defer close(resultChan)

	// Extract correlation ID from context for logging
	logger := util.GetLogger(ctx)
	correlationID := "unknown"
	if loggerFields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		if corrIDStr, exists := loggerFields["requestCorrelationID"].(string); exists {
			correlationID = corrIDStr
		}
	}

	logger.Infof("Starting metrics reporting with correlation ID: %s, metrics count: %d", correlationID, len(metrics))

	if len(metrics) == 0 {
		logger.Info("No metrics to report")
		return 0
	}
	operationsToPush, dropped := client.createOperationsForMetrics(metrics, operationStartTime, operationEndTime)

	if len(operationsToPush) == 0 {
		logger.Debugf("No operations to push for metrics")
		return dropped
	}

	var operationBatchList [][]*Operation
	operationMap := make(map[string]*Operation)

	logger.Infof("Operation batch size is %d", client.config.OperationBatchSize)
	var tempOperationList []*Operation
	var i int64 = 0

	for operation := range operationsToPush {
		if operation != nil {
			tempOperationList = append(tempOperationList, operation)
			i++
			if i == client.config.OperationBatchSize {
				operationBatchList = append(operationBatchList, tempOperationList)
				tempOperationList = []*Operation{}
				i = 0
			}
			operationMap[operation.OperationId] = operation
		}
	}

	if i != 0 && i < client.config.OperationBatchSize {
		operationBatchList = append(operationBatchList, tempOperationList)
	}
	client.reportOperationList(ctx, operationBatchList, operationsToPush, operationMap, resultChan)
	return dropped
}

func appendExceptionResults(
	results []common.MetricsResult,
	operationList []*Operation,
	operationsToPush map[*Operation][]common.GoogleMetric,
	err error,
) []common.MetricsResult {
	for _, operation := range operationList {
		for _, reportedMetric := range operationsToPush[operation] {
			results = append(results, common.MetricsResult{
				GoogleMetric:  reportedMetric,
				Exception:     err,
				OperationID:   operation.OperationId,
				OperationName: operation.OperationName,
			})
		}
	}
	return results
}

func isContextCancellation(err error) bool {
	return stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded)
}

func sendMetricsResults(ctx context.Context, resultChan chan []common.MetricsResult, results []common.MetricsResult) bool {
	select {
	case resultChan <- results:
		return true
	case <-ctx.Done():
		return false
	}
}

func (client *GoogleMetricsClient) reportOperationList(ctx context.Context, operationBatchList [][]*Operation, operationsToPush map[*Operation][]common.GoogleMetric, operationMap map[string]*Operation, resultChan chan []common.MetricsResult) {
	logger := util.GetLogger(ctx)

	for _, operationList := range operationBatchList {
		if err := ctx.Err(); err != nil {
			break
		}

		var results []common.MetricsResult
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("Exception %v is thrown while reporting the operation list %v", r, operationList)
					results = appendExceptionResults(results, operationList, operationsToPush, fmt.Errorf("%v", r))
				}
			}()

			logger.Debugf("For service: %s; Google Metrics Operation list is %v", client.config.PusherServiceName, spew.Sdump(operationList))

			var serviceControl *servicecontrol.Service
			var err error
			// Only create service control client if not in mock mode
			if !client.mockMode {
				serviceControl, err = createServiceControlClient(ctx, client.config.PusherServiceProject, client.rootURL, logger)
				if err != nil {
					logger.Errorf("Could not create Service Control Client: %v", err)
					if isContextCancellation(err) {
						// Cancellation is a job-level retry signal. Do not emit per-record
						// exceptions or increment error_count; leave records eligible for retry.
						return
					}
					results = appendExceptionResults(results, operationList, operationsToPush, err)
					return
				}
			}
			response, err := client.reportOperation(ctx, operationList, serviceControl, logger)
			if err != nil {
				logger.Errorf("Error reporting operation list: %v", err)
				if isContextCancellation(err) {
					// Cancellation is a job-level retry signal. Do not emit per-record
					// exceptions or increment error_count; leave records eligible for retry.
					return
				}
				results = appendExceptionResults(results, operationList, operationsToPush, err)
				return
			}

			logger.Infof("Report response: %v", response.ReportErrors)
			responseWithError := &common.ReportResponse{}
			responseWithNoError := &common.ReportResponse{}
			if len(response.ReportErrors) != 0 {
				for _, reportError := range response.ReportErrors {
					responseWithError.ReportErrors = append(responseWithError.ReportErrors, reportError)
					operation := operationMap[reportError.OperationId]
					for _, reportedMetric := range operationsToPush[operationMap[reportError.OperationId]] {
						results = append(results, common.MetricsResult{
							GoogleMetric:   reportedMetric,
							ReportResponse: responseWithError,
							OperationID:    reportError.OperationId,
							OperationName:  operation.OperationName,
						})
					}
					operationList = removeOperation(operationList, operationMap[reportError.OperationId])
				}
			}

			for _, operation := range operationList {
				for _, reportedMetric := range operationsToPush[operation] {
					responseWithNoError.ReportErrors = nil
					results = append(results, common.MetricsResult{
						GoogleMetric:   reportedMetric,
						ReportResponse: responseWithNoError,
						OperationID:    operation.OperationId,
						OperationName:  operation.OperationName,
					})
				}
			}
		}()
		if !sendMetricsResults(ctx, resultChan, results) {
			return
		}
		if err := ctx.Err(); err != nil {
			break
		}
	}
}

func (client *GoogleMetricsClient) createOperationsForMetrics(metrics []common.GoogleMetric, opStart, opEnd int64) (map[*Operation][]common.GoogleMetric, int) {
	metricsByVolume := make(map[ResourceInfo][]common.GoogleMetric)
	for _, metric := range metrics {
		customerId, err := metric.GetCustomerId()
		if err != nil {
			client.logger.Errorf("Error getting customer ID: %v", err)
			continue
		}

		resourceName, err := metric.GetResourceName()
		if err != nil {
			client.logger.Errorf("Error getting resource name: %v", err)
			continue
		}

		resourceId, err := metric.GetResourceUUID()
		if err != nil {
			client.logger.Errorf("Error getting resource ID: %v", err)
			continue
		}

		info := ResourceInfo{
			CustomerId:   customerId,
			ResourceName: resourceName,
			ResourceId:   resourceId,
		}
		metricsByVolume[info] = append(metricsByVolume[info], metric)
	}

	operationAndMetrics := make(map[*Operation][]common.GoogleMetric)
	submitted := 0

	for info, googleMetrics := range metricsByVolume {
		if len(googleMetrics) == 0 {
			client.logger.Debugf("There were zero Google metrics to report for resource %s", info.ResourceId)
			continue
		}

		totalDropCount := 0
		partitionedMetrics := partitionMetrics(googleMetrics, client.logger)
		for _, partition := range partitionedMetrics {
			// Create separate operation for each metric in the partition
			for _, metric := range partition {
				operationId := client.generateOperationId(metric, opStart, opEnd)
				operation, droppedMetrics, err := client.createOperationForMetric(operationId, []common.GoogleMetric{metric}, info.CustomerId, info.ResourceId, opStart, opEnd)
				if err != nil {
					client.logger.Errorf("Operation creation for %s failed: %v", info.ResourceId, err)
					continue
				}

				totalDropCount += len(droppedMetrics)

				if operation != nil && len(operation.MetricValueSets) > 0 {
					operationAndMetrics[operation] = []common.GoogleMetric{metric}
					submitted++
				} else {
					client.logger.Warn("Operation creation method succeeded, but no operation returned. ResourceId", info.ResourceId)
				}
			}
		}

		if totalDropCount > 0 {
			client.logger.Infof("Dropped %d ignored or invalid metrics from operations this run.", totalDropCount)
		}
	}
	return operationAndMetrics, len(metrics) - submitted
}

// generateOperationId creates a consistent UUIDv5-based operation ID from metric properties
func (client *GoogleMetricsClient) generateOperationId(metric common.GoogleMetric, opStart, opEnd int64) string {
	// Use a namespace UUID for VSA Control Plane metrics
	// This is a fixed namespace UUID that we define for this purpose
	namespace := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8") // DNS namespace UUID as base

	// Build a deterministic name string from metric properties
	// Use explicit placeholder for missing values to avoid collisions
	var nameBuilder strings.Builder

	// Resource UUID (required for uniqueness)
	if resourceUUID, err := metric.GetResourceUUID(); err == nil {
		nameBuilder.WriteString(resourceUUID)
	}
	nameBuilder.WriteString("|")

	// Resource Type (required for differentiation)
	if resourceType, err := metric.GetResourceType(); err == nil {
		nameBuilder.WriteString(string(resourceType))
	}
	nameBuilder.WriteString("|")

	// Measured Type (required for differentiation)
	if measuredType, err := metric.GetMeasuredType(); err == nil {
		nameBuilder.WriteString(string(measuredType))
	}
	nameBuilder.WriteString("|")

	// Start Time (required for temporal uniqueness)
	if startTime, err := metric.GetStartTime(); err == nil {
		nameBuilder.WriteString(fmt.Sprintf("%d", startTime))
	}
	nameBuilder.WriteString("|")

	// Customer ID (required for tenant isolation)
	if customerId, err := metric.GetCustomerId(); err == nil {
		nameBuilder.WriteString(customerId)
	}
	nameBuilder.WriteString("|")

	// Resource Name (required for resource identification)
	if resourceName, err := metric.GetResourceName(); err == nil {
		nameBuilder.WriteString(resourceName)
	}

	// Generate UUIDv5 based on namespace and name
	operationUUID := uuid.NewSHA1(namespace, []byte(nameBuilder.String()))
	return operationUUID.String()
}

func (client *GoogleMetricsClient) createOperationForMetric(operationId string, googleMetrics []common.GoogleMetric, customerId string, resourceUuid string, opStart int64, opEnd int64) (*Operation, []common.GoogleMetric, error) {
	if len(googleMetrics) == 0 {
		return nil, nil, nil
	}

	op := &Operation{}
	googleMetric := googleMetrics[0]
	metricType := googleMetric.GetType()
	dataCenter := client.config.RegionName

	err := SetCommonLabels(op, customerId, dataCenter, resourceUuid, googleMetric)
	if err != nil {
		return nil, nil, err
	}

	if metricType == common.BillingMetric {
		labels, err := googleMetric.GetLabels()
		if err != nil {
			client.logger.Errorf("Error getting billing labels: %v", err)
		}
		op.UserLabels = labels
	}

	op.OperationName = fmt.Sprintf("OperationMetrics_%s-%d", resourceUuid, opStart)
	op.OperationId = operationId
	op.StartTime = time.Unix(opStart, 0).Format(time.RFC3339)
	op.EndTime = time.Unix(opEnd, 0).Format(time.RFC3339)

	consumerId := toGoogleProject(customerId)
	if consumerId != "" {
		op.ConsumerId = consumerId
	} else {
		client.logger.Errorf("Consumer ID is null. Aborting operation creation.")
		return nil, nil, nil
	}

	var metricValueSets []*MetricValueSet
	metricsByName := make(map[string][]common.GoogleMetric)
	droppedMetrics := make(map[metadata.MeasuredType][]common.GoogleMetric)

	for _, value := range metadata.CombinedKeyResourceTypeMeasuredTypeMap {
		droppedMetrics[value.MeasuredType] = []common.GoogleMetric{}
	}

	for _, metric := range googleMetrics {
		googleMetricName, err := client.GetMetricName(metric)
		if err != nil {
			resourceType, _ := metric.GetResourceType()
			measuredType, _ := metric.GetMeasuredType()
			client.logger.Errorf("Valid google endpoint not found for metric for resource type %s and measured type %s", resourceType, measuredType)
		}
		if googleMetricName != "" {
			metricsByName[googleMetricName] = append(metricsByName[googleMetricName], metric)
		} else {
			measuredType, _ := metric.GetMeasuredType()
			droppedMetrics[measuredType] = append(droppedMetrics[measuredType], metric)
		}
	}

	for metricName, metric := range metricsByName {
		metricValueSet, err := client.createMetricValueSet(metricName, metric)

		if err != nil {
			return nil, nil, err
		}
		metricValueSets = append(metricValueSets, metricValueSet)
	}

	var serviceControlMetricValueSets []*servicecontrol.MetricValueSet
	for _, mvs := range metricValueSets {
		serviceControlMetricValueSets = append(serviceControlMetricValueSets, (*servicecontrol.MetricValueSet)(mvs))
	}

	logDroppedMetrics(droppedMetrics, client.logger)
	op.MetricValueSets = serviceControlMetricValueSets

	return op, flattenDroppedMetrics(droppedMetrics), nil
}

func flattenDroppedMetrics(droppedMetrics map[metadata.MeasuredType][]common.GoogleMetric) []common.GoogleMetric {
	var result []common.GoogleMetric
	for _, droppedMetric := range droppedMetrics {
		result = append(result, droppedMetric...)
	}
	return result
}

func partitionMetrics(googleMetrics []common.GoogleMetric, logger log.Logger) [][]common.GoogleMetric {
	if !hasDuplicateMeasuredTypes(googleMetrics, logger) {
		return [][]common.GoogleMetric{googleMetrics}
	}

	metricsByMeasuredType := make(map[metadata.MeasuredType][]common.GoogleMetric)
	for _, metric := range googleMetrics {
		measuredType, err := metric.GetMeasuredType()
		if err != nil {
			logger.Debug("Error getting measured type in partition metrics method.", "error", err, "metric", metric)
			continue
		}
		metricsByMeasuredType[measuredType] = append(metricsByMeasuredType[measuredType], metric)
	}

	var partitionedMetrics [][]common.GoogleMetric
	for len(metricsByMeasuredType) > 0 {
		var partition []common.GoogleMetric
		for _, value := range metadata.CombinedKeyResourceTypeMeasuredTypeMap {
			if metricsOfType, exists := metricsByMeasuredType[value.MeasuredType]; exists && len(metricsOfType) > 0 {
				metric := metricsOfType[0]
				metricsByMeasuredType[value.MeasuredType] = metricsOfType[1:]
				if len(metricsByMeasuredType[value.MeasuredType]) == 0 {
					delete(metricsByMeasuredType, value.MeasuredType)
				}
				partition = append(partition, metric)
			}
		}
		partitionedMetrics = append(partitionedMetrics, partition)
	}
	return partitionedMetrics
}

func hasDuplicateMeasuredTypes(googleMetrics []common.GoogleMetric, logger log.Logger) bool {
	measuredTypeMap := make(map[metadata.MeasuredType]bool)
	for _, metric := range googleMetrics {
		measuredType, err := metric.GetMeasuredType()
		if err != nil {
			logger.Debug("Error getting measured type in has duplicate measured types method.", "error", err, "metric", metric)
			continue
		}
		val, ok := measuredTypeMap[measuredType]
		if ok && val {
			return true
		}
		measuredTypeMap[measuredType] = true
	}
	return false
}

func logDroppedMetrics(droppedMetrics map[metadata.MeasuredType][]common.GoogleMetric, logger log.Logger) {
	for measuredType, metric := range droppedMetrics {
		logger.Debugf("Dropped %d metric of unsupported metric type %s.", len(metric), measuredType)
	}
}

func (client *GoogleMetricsClient) createMetricValueSet(metricName string, metrics []common.GoogleMetric) (*MetricValueSet, error) {
	if len(metrics) == 0 {
		return nil, nil
	}

	metricValueSet := &MetricValueSet{
		MetricName:   metricName,
		MetricValues: []*servicecontrol.MetricValue{},
	}

	for _, metric := range metrics {
		metricValue, err := client.CreateMetricValue(metric)
		if err != nil {
			return nil, fmt.Errorf("error creating metric value for metric %s", metricName)
		}
		metricValueSet.MetricValues = append(metricValueSet.MetricValues, metricValue)
	}
	return metricValueSet, nil
}

func (client *GoogleMetricsClient) CreateMetricValue(metric common.GoogleMetric) (MetricValue, error) {
	metricValue := &servicecontrol.MetricValue{}
	mibToKibConverter := 1024
	metricMeasuredType, _ := metric.GetMeasuredType()
	metricResourceType, _ := metric.GetResourceType()

	switch metricMeasuredType {
	case metadata.VolumeAllocatedThroughput:
		volAllocatedThroughputInMibPerSec, _ := metric.GetDoubleQuantity()
		volAllocatedThroughputInKibPerSec := int64(volAllocatedThroughputInMibPerSec * float64(mibToKibConverter))
		metricValue.Int64Value = &volAllocatedThroughputInKibPerSec
	case metadata.AverageReadLatency, metadata.AverageWriteLatency, metadata.AverageOtherLatency:
		val, _ := metric.GetDoubleQuantity()
		metricValue.DoubleValue = &val
		client.logger.Debugf("Set Metric Value for MeasuredType %s as %f", metricMeasuredType, val)
	default:
		val, _ := metric.GetQuantity()
		metricValue.Int64Value = nillable.ToPointer(val)

		client.logger.Debugf("Set Metric Value for MeasuredType %s as %d", metricMeasuredType, val)
	}

	startTime, err := metric.GetStartTime()
	if err != nil {
		client.logger.Errorf("error getting start time for MeasuredType %s", metricMeasuredType)
	}

	var endTime int64
	if metric.GetType() == common.HydratedMetric {
		secondsRemaining := startTime % 60
		startTime -= secondsRemaining
		endTime = startTime + 59
	} else {
		endTime, err = metric.GetEndTime()
		if err != nil {
			return nil, err
		}
	}

	metricValue.StartTime = time.Unix(startTime, 0).Format(time.RFC3339)
	metricValue.EndTime = time.Unix(endTime, 0).Format(time.RFC3339)

	labelKeys := GetLabelKey(metric)
	valueLabels := make(map[string]string)
	if labelKeys != nil {
		for _, labelKey := range labelKeys {
			metricLabelValue, err := GetLabelValue(labelKey, metric, client.logger)
			if err != nil {
				return nil, err
			}
			// Include label if it has a value or if it's allowed to be empty
			if metricLabelValue != "" || isAllowedEmptyLabel(labelKey) {
				valueLabels[labelKey] = metricLabelValue
			}
		}
	}

	// Add metric-specific labels from Triple (Middle/Right) for performance metrics
	if metric.GetType() == common.HydratedMetric {
		nameAndKeyLabel, exists := client.nameAndKeyLabelOfMetric[metadata.CombinedKeyResourceTypeMeasuredType{ResourceType: metricResourceType, MeasuredType: metricMeasuredType}]
		if exists && nameAndKeyLabel.Middle != "" && nameAndKeyLabel.Right != "" {
			valueLabels[nameAndKeyLabel.Middle] = nameAndKeyLabel.Right
		}

		// Add labels from Tags in ResourceMetadata (for custom metric labels like backup_crypto_key_version)
		// Only process tags for backup vault metrics
		if metricResourceType == metadata.BackupVault {
			hydratedMetric, err := metric.GetAsHydratedMetric()
			if err == nil && hydratedMetric.Metadata.Tags != nil {
				for key, value := range hydratedMetric.Metadata.Tags {
					if value != "" {
						valueLabels[key] = value
					}
				}
			}
		}
	}

	metricValue.Labels = valueLabels

	client.logger.Debugf("Created metric value - StartTime: %s, EndTime: %s, Labels: %d, Value: %v",
		metricValue.StartTime, metricValue.EndTime, len(metricValue.Labels), getMetricValueSummary(metricValue))
	return metricValue, nil
}

// getMetricValueSummary returns a user-friendly summary of the metric value
func getMetricValueSummary(mv *servicecontrol.MetricValue) interface{} {
	if mv.Int64Value != nil {
		return *mv.Int64Value
	}
	if mv.DoubleValue != nil {
		return *mv.DoubleValue
	}
	if mv.StringValue != nil {
		return *mv.StringValue
	}
	if mv.BoolValue != nil {
		return *mv.BoolValue
	}
	return "unknown"
}

func SetCommonLabels(op *Operation, consumerId, dataCenter, resourceId string, googleMetric common.GoogleMetric) error {
	labels := make(map[string]string)

	metricType := googleMetric.GetType()
	if metricType == common.BillingMetric {
		location := dataCenter
		// For AT billing metrics on zonal pools, use zone instead of region
		measuredType, _ := googleMetric.GetMeasuredType()
		if isATBillingMeasuredType(measuredType) {
			if zone, err := googleMetric.GetZone(); err == nil && zone != "" {
				location = zone
			}
		}
		labels["cloud.googleapis.com/location"] = location
	} else {
		labels["location"] = dataCenter
		labels["resource_container"] = "projects/" + consumerId
		labels["name"], _ = googleMetric.GetResourceName()
	}
	op.Labels = labels
	return nil
}

// isATBillingMeasuredType returns true if the measured type is an auto-tiering billing metric.
func isATBillingMeasuredType(measuredType metadata.MeasuredType) bool {
	switch measuredType {
	case metadata.CoolTierDataReadSizeRaw,
		metadata.CoolTierDataWriteSizeRaw,
		metadata.PoolHotTierProvisionedSize,
		metadata.PoolCapacityTierLogicalFootprint:
		return true
	}
	return false
}

func toGoogleProject(customerId string) string {
	if customerId == "" {
		return ""
	}

	if startsWith(customerId, "project:") || startsWith(customerId, "project_number:") {
		return customerId
	} else {
		if isNumeric(customerId) {
			return "project_number:" + customerId
		} else {
			return "project:" + customerId
		}
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func isNumeric(s string) bool {
	return re.MatchString(s)
}

func createServiceControlClient(ctx context.Context, serviceProject string, rootUrl string, logger log.Logger) (*servicecontrol.Service, error) {
	httpClient, _, err := transport.NewHTTPClient(ctx, option.WithTokenSource(google.ComputeTokenSource("", servicecontrol.ServicecontrolScope)))
	if err != nil {
		logger.Errorf("Failed to create http client for creating service control: %v", err)
		return nil, err
	}

	service, err := servicecontrol.NewService(ctx,
		option.WithHTTPClient(httpClient),
		option.WithEndpoint(rootUrl),
		option.WithScopes(servicecontrol.ServicecontrolScope),
	)
	if err != nil {
		logger.Errorf("Failed to create service control client: %v", err)
		return nil, err
	}

	logger.Infof("Created client with workload identity: serviceProject: %s, rootUrl: %s", serviceProject, rootUrl)
	return service, nil
}

func (client *GoogleMetricsClient) reportOperation(ctx context.Context, operationBatchList []*Operation, serviceControl *servicecontrol.Service, logger log.Logger) (*servicecontrol.ReportResponse, error) {
	if operationBatchList == nil {
		return nil, errors.New("operation batch list was nil. This operation list was dropped")
	}
	var serviceControlOperations []*servicecontrol.Operation
	for _, op := range operationBatchList {
		serviceControlOperations = append(serviceControlOperations, (*servicecontrol.Operation)(op))
	}
	req := &servicecontrol.ReportRequest{
		Operations: serviceControlOperations,
	}

	// If mock mode is enabled, simulate API call with latency distribution and error rate
	if client.mockMode {
		// Initialize random source with current time for better randomness
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))

		// Simulate network latency: 90% in [100ms, 200ms), 10% in [200ms, 400ms)
		var latency time.Duration
		if rng.Float64() < 0.9 {
			// 90% of requests: [100ms, 200ms)
			latency = time.Duration(100+rng.Intn(100)) * time.Millisecond
		} else {
			// 10% of requests: [200ms, 400ms)
			latency = time.Duration(200+rng.Intn(200)) * time.Millisecond
		}
		// Honor ctx so a cancelled/timed-out job aborts the simulated call too.
		select {
		case <-time.After(latency):
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		logger.Infof("MOCK MODE: Simulating report of %d operations to Google Service Control API (latency: %v)",
			len(serviceControlOperations), latency)

		// Simulate 1% error rate with different HTTP errors
		var reportErrors []*servicecontrol.ReportError
		// List of possible HTTP error codes for variety
		errorCodes := []int64{400, 401, 403, 404, 429, 500, 502, 503, 504}
		errorMessages := map[int64]string{
			400: "MOCK MODE: Simulated bad request error",
			401: "MOCK MODE: Simulated unauthorized error",
			403: "MOCK MODE: Simulated forbidden error",
			404: "MOCK MODE: Simulated not found error",
			429: "MOCK MODE: Simulated rate limit exceeded error",
			500: "MOCK MODE: Simulated internal server error",
			502: "MOCK MODE: Simulated bad gateway error",
			503: "MOCK MODE: Simulated service unavailable error",
			504: "MOCK MODE: Simulated gateway timeout error",
		}

		for _, op := range operationBatchList {
			// 1% chance of error
			if rng.Float64() < 0.01 {
				// Randomly select an error code
				errorCode := errorCodes[rng.Intn(len(errorCodes))]
				errorMessage := errorMessages[errorCode]

				logger.Warnf("MOCK MODE: Simulating %d error for operation: OperationId=%s, OperationName=%s",
					errorCode, op.OperationId, op.OperationName)
				reportErrors = append(reportErrors, &servicecontrol.ReportError{
					OperationId: op.OperationId,
					Status: &servicecontrol.Status{
						Code:    errorCode,
						Message: errorMessage,
					},
				})
			} else {
				logger.Debugf("MOCK MODE: Successfully simulated operation: OperationId=%s, OperationName=%s, ConsumerId=%s, MetricValueSets=%d",
					op.OperationId, op.OperationName, op.ConsumerId, len(op.MetricValueSets))
			}
		}

		// Return mock response with simulated errors
		return &servicecontrol.ReportResponse{
			ReportErrors: reportErrors,
		}, nil
	}

	logger.Info("Reporting operation to service control")
	report := serviceControl.Services.Report(client.config.PusherServiceName, req)
	// Thread ctx so a cancelled/timed-out job aborts the billing push instead of
	// hanging indefinitely on a stuck Google ServiceControl call.
	return report.Context(ctx).Do()
}

func removeOperation(operations []*Operation, operation *Operation) []*Operation {
	if operation == nil {
		return operations
	}
	for i, op := range operations {
		if op.OperationId == operation.OperationId {
			return append(operations[:i], operations[i+1:]...)
		}
	}
	return operations
}

func (client *GoogleMetricsClient) GetMetricName(metric common.GoogleMetric) (string, error) {
	resourceType, _ := metric.GetResourceType()
	measuredType, _ := metric.GetMeasuredType()
	if metric.GetType() == common.BillingMetric {
		jobDef, exists := common.DefaultAggregationJobDefinitions[metadata.CombinedKeyResourceTypeMeasuredType{ResourceType: resourceType, MeasuredType: measuredType}]
		if !exists {
			client.logger.Warnf("No job definition found for resource type %s and measured type %s", resourceType, measuredType)
			return "", fmt.Errorf("unsupported measured type or resource type received: %s, %s", measuredType, resourceType) // If the key does not exist
		}
		return common.BillingMetricsNamePrefix + jobDef.SKU, nil
	} else {
		nameAndKeyLabel, exists := client.nameAndKeyLabelOfMetric[metadata.CombinedKeyResourceTypeMeasuredType{ResourceType: resourceType, MeasuredType: measuredType}]
		if !exists {
			return "", fmt.Errorf("unsupported measured type or resource type received: %s, %s", measuredType, resourceType)
		}

		var metricsName string
		switch resourceType {
		case metadata.VolumePool, metadata.VolumePoolRegionalHA:
			metricsName = metadata.MetricsNamePrefixPoolFirstParty + nameAndKeyLabel.Left
		case metadata.Volume, metadata.VolumeRegionalHA:
			metricsName = metadata.MetricsNamePrefixVolumeFirstParty + nameAndKeyLabel.Left
		case metadata.Backup:
			metricsName = metadata.MetricsNamePrefixVolumeFirstParty + nameAndKeyLabel.Left
		case metadata.BackupVault:
			metricsName = metadata.MetricsNamePrefixBackupVaultFirstParty + nameAndKeyLabel.Left
		default:
			return "", fmt.Errorf("unrecognized resource type: %s", resourceType)
		}
		return metricsName, nil
	}
}

func GetLabelKey(metric common.GoogleMetric) []string {
	if metric.GetType() != common.BillingMetric {
		return nil
	}
	metricMeasuredType, _ := metric.GetMeasuredType()
	metricResourceType, _ := metric.GetResourceType()
	switch metricResourceType {
	case metadata.VolumeReplicationRelationship:
		return []string{
			"/resource_id",
			"/replication/frequency",
			"/replication/source_continent",
			"/replication/destination_continent",
			"/replication/source_region",
			"/replication/destination_region",
			"/replication/source_service_level",
			"/replication/destination_service_level",
			"/replication/replication_type",
			"/replication/mode",
		}
	case metadata.Volume:
		switch metricMeasuredType {
		case metadata.BackupEnabledVolumeAllocatedSize:
			return []string{"/resource_id", "/backups/mode"}
		case metadata.CbsCrossRegionVolumeRestoreTransferBytes:
			return []string{"/resource_id", "/backups/source_continent", "/backups/destination_continent"}
		}
	case metadata.VolumeRegionalHA:
		switch metricMeasuredType {
		case metadata.BackupEnabledVolumeAllocatedSize:
			return []string{"/resource_id", "/backups/mode"}
		}
	case metadata.Backup:
		switch metricMeasuredType {
		case metadata.BackupLogicalSize:
			return []string{"/resource_id", "/backups/location", "/backups/mode"}
		case metadata.CbsCrossRegionVolumeBackupTransferBytes:
			return []string{"/resource_id", "/backups/source_continent", "/backups/destination_continent", "/backups/mode"}
		}
	case metadata.VolumePool, metadata.VolumePoolRegionalHA:
		switch metricMeasuredType {
		case metadata.CoolTierDataReadSizeRaw, metadata.CoolTierDataWriteSizeRaw:
			return []string{"/resource_id", "/storage/location", "/netapp/auto_tier_transfer_type", "/storage/service_level"}
		case metadata.PoolHotTierProvisionedSize:
			return []string{"/resource_id", "/storage/location", "/storage/service_level"}
		case metadata.PoolCapacityTierLogicalFootprint:
			return []string{"/resource_id", "/storage/location", "/storage/service_level"}
		}
	}
	return nil
}

func GetLabelValue(key string, metric common.GoogleMetric, logger log.Logger) (string, error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Infof("Recovered in GetLabelValue: %v", r)
		}
	}()

	metricResourceType, _ := metric.GetResourceType()
	switch metricResourceType {
	case metadata.Backup:
		switch key {
		case "/resource_id":
			return metric.GetResourceUUID()
		case "/backups/location":
			if destinationRegion, err := getDestinationRegion(metric); err == nil && destinationRegion != "" {
				return destinationRegion, nil
			}
			return metric.GetRegion()
		case "/backups/source_continent":
			sourceRegion, err := getSourceRegion(metric)
			return getContinent(sourceRegion), err
		case "/backups/destination_continent":
			destinationRegion, err := getDestinationRegion(metric)
			return getContinent(destinationRegion), err
		case "/backups/mode":
			return getBackupMode(metric), nil
		}
	case metadata.Volume:
		switch key {
		case "/resource_id":
			return metric.GetResourceUUID()
		case "/backups/source_continent":
			sourceRegion, err := getSourceRegion(metric)
			return getContinent(sourceRegion), err
		case "/backups/destination_continent":
			destinationRegion, err := getDestinationRegion(metric)
			return getContinent(destinationRegion), err
		case "/backups/mode":
			return getBackupMode(metric), nil
		}
	case metadata.VolumeRegionalHA:
		switch key {
		case "/resource_id":
			return metric.GetResourceUUID()
		case "/backups/mode":
			return getBackupMode(metric), nil
		}
	case metadata.VolumeReplicationRelationship:
		repType, err := getReplicationType(metric)
		if err != nil {
			return "", err
		}
		switch key {
		case "/resource_id":
			return getResourceUUID(metric)
		case "/replication/frequency":
			serviceLevel, err := getServiceLevel(metric)
			return getFrequency(serviceLevel), err
		case "/replication/source_continent":
			if repType == string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION) || repType == string(datamodel.HybridReplicationParametersReplicationTypeONPREM) {
				return "", nil
			}
			sourceRegion, err := getSourceRegion(metric)
			return getContinent(sourceRegion), err
		case "/replication/destination_continent":
			destinationRegion, err := getDestinationRegion(metric)
			return getContinent(destinationRegion), err
		case "/replication/source_region":
			if repType == string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION) || repType == string(datamodel.HybridReplicationParametersReplicationTypeONPREM) {
				return "", nil
			}
			sourceRegion, err := getSourceRegion(metric)
			return extractRegionValue(sourceRegion), err
		case "/replication/destination_region":
			destinationRegion, err := getDestinationRegion(metric)
			return extractRegionValue(destinationRegion), err
		case "/replication/source_service_level":
			if repType == string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION) || repType == string(datamodel.HybridReplicationParametersReplicationTypeONPREM) {
				return "", nil
			}
			return "FLEX_UNIFIED", nil
		case "/replication/destination_service_level":
			return "FLEX_UNIFIED", nil
		case "/replication/replication_type":
			if useNewLabelForHybridReplication && (repType == string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION) || repType == string(datamodel.HybridReplicationParametersReplicationTypeONPREM)) {
				return ReplicationTypeDataMigration, nil
			}
			return repType, nil
		case "/replication/mode":
			serviceLevel, err := getServiceLevel(metric)
			if err == nil && serviceLevel == "4" {
				return ReplicationModeOntap, nil
			}
			return "", err
		}
	case metadata.VolumePool:
		switch key {
		case "/resource_id":
			return metric.GetResourceUUID()
		case "/storage/location":
			if zone, err := metric.GetZone(); err == nil && zone != "" {
				return zone, nil
			}
			return metric.GetRegion()
		case "/storage/service_level":
			return "FLEX", nil
		case "/netapp/auto_tier_transfer_type":
			measuredType, _ := metric.GetMeasuredType()
			if measuredType == metadata.CoolTierDataReadSizeRaw {
				return "COOL_TIER_DATA_READ_SIZE", nil
			}
			return "COOL_TIER_DATA_WRITE_SIZE", nil
		}
	case metadata.VolumePoolRegionalHA:
		switch key {
		case "/resource_id":
			return metric.GetResourceUUID()
		case "/storage/location":
			return metric.GetRegion()
		case "/storage/service_level":
			return "FLEX", nil
		case "/netapp/auto_tier_transfer_type":
			measuredType, _ := metric.GetMeasuredType()
			if measuredType == metadata.CoolTierDataReadSizeRaw {
				return "COOL_TIER_DATA_READ_SIZE", nil
			}
			return "COOL_TIER_DATA_WRITE_SIZE", nil
		}
	}
	return "", nil
}

// extractRegionValue returns the GCP region from a value that may be a region (e.g. us-central1)
// or a zone (e.g. us-central1-a). Other shapes are returned unchanged.
func extractRegionValue(location string) string {
	s := strings.TrimSpace(location)
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "-")
	if len(parts) == ZonePartsCount {
		return strings.Join(parts[:2], "-")
	}
	return s
}

// isAllowedEmptyLabel checks if a label key is allowed to have an empty value
func isAllowedEmptyLabel(labelKey string) bool {
	for _, allowedLabel := range allowedEmptyLabels {
		if labelKey == allowedLabel {
			return true
		}
	}
	return false
}

func _getResourceUUID(metric common.GoogleMetric) (string, error) {
	return metric.GetResourceUUID()
}

func _getReplicationType(metric common.GoogleMetric) (string, error) {
	return metric.GetReplicationType()
}

func _getSourceRegion(metric common.GoogleMetric) (string, error) {
	return metric.GetSourceRegion()
}

func _getDestinationRegion(metric common.GoogleMetric) (string, error) {
	return metric.GetDestinationRegion()
}

func _getServiceLevel(metric common.GoogleMetric) (string, error) {
	return metric.GetServiceLevel()
}

// getBackupMode returns BackupModeOntap when the metric's ServiceLevel is set to
// BackupModeOntap, and BackupModeDefault in all other cases.
func getBackupMode(metric common.GoogleMetric) string {
	serviceLevel, err := getServiceLevel(metric)
	if err == nil && serviceLevel == BackupModeOntap {
		return BackupModeOntap
	}
	return BackupModeDefault
}

func _getFrequency(serviceLevel string) string {
	switch serviceLevel {
	case "1":
		return VolumeReplicationSchedule10Minutely
	case "2":
		return VolumeReplicationScheduleHourly
	case "3":
		return VolumeReplicationScheduleDaily
	default:
		return ""
	}
}

// getContinent converts location to continent string
func _getContinent(location string) string {
	if location == "" {
		return ""
	}

	normalizedLocation := strings.ToLower(location)
	parts := strings.Split(normalizedLocation, "-")
	count := len(parts)

	if count != ZonePartsCount && count != RegionPartsCount {
		return ""
	}

	switch parts[0] {
	case "us":
		parts[0] = NorthAmericaContinent
	case "eu":
		parts[0] = EuropeContinent
	}

	// Special case for Indonesia
	if parts[0] == "asia" && len(parts) > 1 && parts[1] == "southeast2" {
		return "indonesia"
	}

	continentMap := getContinentMap(googleContinents)
	if continent, exists := continentMap[parts[0]]; exists {
		return continent
	}

	return parts[0]
}
