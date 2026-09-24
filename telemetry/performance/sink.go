package performance

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/googlePusher"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	HTTPForbiddenError = "403"
	HTTPNotFoundError  = "404"
)

// Sink is any destination for the metrics.
// failedCount is the number of metrics that failed validation or Google delivery.
// err is set only for hard failures (total delivery failure, missing all results, context abort).
type Sink interface {
	DeliverMetrics(ctx context.Context, metrics []entity.HydratedMetric) (failedCount int, err error)
}

// GoogleSink is responsible for delivering metrics to Google.
type GoogleSink struct {
	metricClient    googlePusher.GoogleMetricsClient
	logger          log.Logger
	metricsRecorder monitoring.MetricsRecorder
}

func (s *GoogleSink) processMetricsResults(results []common.MetricsResult) {
	s.logger.Warn("processMetricsResults not implemented")
}

func NewSink(ctx context.Context, config *common.TelemetryConfig, metricsRecorder monitoring.MetricsRecorder) *GoogleSink {
	return &GoogleSink{
		metricClient:    *googlePusher.NewGoogleMetricsClient(ctx, config.PerformanceRootUrl, config),
		logger:          util.GetLogger(ctx),
		metricsRecorder: metricsRecorder,
	}
}

func (s *GoogleSink) DeliverMetrics(ctx context.Context, hydratedMetrics []entity.HydratedMetric) (failedCount int, err error) {
	validMetrics := s.FilterAndConvertToGoogleMetrics(hydratedMetrics)
	invalidMetricCount := len(hydratedMetrics) - len(validMetrics)
	if invalidMetricCount > 0 {
		s.logger.Warnf("Skipped %d invalid performance metrics before delivery", invalidMetricCount)
		s.metricsRecorder.RecordSinkDelivered(&monitoring.MetricRecorderParams{
			SinkType:       "performance",
			FailedQuantity: float64(invalidMetricCount),
		})
		failedCount += invalidMetricCount
	}

	pushFailedCount, pushErr := s.push(ctx, validMetrics)
	failedCount += pushFailedCount
	return failedCount, pushErr
}

// FilterAndConvertToGoogleMetrics filters out invalid metrics from the provided list and convert to google metrics.
// Parameters:
// - metrics: The list of hydrated metrics to filter.
// Returns:
// - A list of valid hydrated metrics.
func (s *GoogleSink) FilterAndConvertToGoogleMetrics(metrics []entity.HydratedMetric) []common.GoogleMetric {
	var warnings []string
	var validMetrics []common.GoogleMetric

	for _, m := range metrics {
		metric := m
		googleMetric := common.GoogleMetric{Record: &metric}
		if s.isValidHydratedMetric(m, &warnings) {
			validMetrics = append(validMetrics, googleMetric)
		} else {
			s.logger.Infof("Ignoring invalid metric: %v, Warnings: %s", m, strings.Join(warnings, "; "))
		}
	}
	return validMetrics
}

// isValidHydratedMetric checks if the given metric is valid.
// Parameters:
// - metric: The hydrated metric to check.
// - warnings: A list to record any warnings encountered during validation.
// Returns:
// - A boolean indicating whether the metric is valid.
func (s *GoogleSink) isValidHydratedMetric(metric entity.HydratedMetric, warnings *[]string) bool {
	var reason strings.Builder
	reason.WriteString("Invalid metric: ")
	isValid := true

	if metric.MeasuredType == metadata.UnknownMeasuredType {
		reason.WriteString("Measured type not defined in the MeasuredType enum; ")
		isValid = false
	}
	if metric.MeasuredType == "" {
		reason.WriteString("Measured Type is empty")
		isValid = false
	}
	if !isValid {
		*warnings = append(*warnings, reason.String())
	}
	return isValid
}

// formatMetricForLogging formats a GoogleMetric in a user-friendly way for logging
func formatMetricForLogging(metric common.GoogleMetric) string {
	switch metric.GetType() {
	case common.HydratedMetric:
		hydratedMetric, err := metric.GetAsHydratedMetric()
		if err != nil {
			return fmt.Sprintf("HydratedMetric (error: %v)", err)
		}

		resourceName := "unknown"
		if hydratedMetric.Metadata.ResourceName != nil {
			resourceName = *hydratedMetric.Metadata.ResourceName
		}

		accountName := "unknown"
		if hydratedMetric.Metadata.AccountName != nil {
			accountName = *hydratedMetric.Metadata.AccountName
		}

		regionName := "unknown"
		if hydratedMetric.Metadata.RegionName != nil {
			regionName = *hydratedMetric.Metadata.RegionName
		}

		timestamp := hydratedMetric.Timestamp.ToTime().Format(time.RFC3339)

		return fmt.Sprintf("Resource: %s, Type: %s, MeasuredType: %s, Quantity: %.2f, Account: %s, Region: %s, Timestamp: %s",
			resourceName, hydratedMetric.Metadata.ResourceType, hydratedMetric.MeasuredType, hydratedMetric.Quantity, accountName, regionName, timestamp)

	case common.BillingMetric:
		billingMetric, err := metric.GetAsUsageBillingMetric()
		if err != nil {
			return fmt.Sprintf("BillingMetric (error: %v)", err)
		}

		resourceName := "unknown"
		if billingMetric.ResourceName != nil {
			resourceName = *billingMetric.ResourceName
		}

		customerID := "unknown"
		if billingMetric.VendorCustomerID != nil {
			customerID = *billingMetric.VendorCustomerID
		}

		regionName := "unknown"
		if billingMetric.RegionName != nil {
			regionName = *billingMetric.RegionName
		}

		return fmt.Sprintf("Resource: %s, Type: %s, MeasuredType: %s, Quantity: %.2f, CustomerID: %s, Region: %s, AggregationType: %s",
			resourceName, billingMetric.ResourceType, billingMetric.MeasuredType, billingMetric.Quantity, customerID, regionName, billingMetric.AggregationType)

	default:
		return fmt.Sprintf("Unknown metric type: %d", metric.GetType())
	}
}

// push sends the given Google metrics and returns failedCount plus err for hard failures only.
// Partial Google rejections are counted but do not fail the whole job because replaying the
// window is not idempotent. They remain observable and require targeted remediation.
func (s *GoogleSink) push(ctx context.Context, googleMetricList []common.GoogleMetric) (failedCount int, err error) {
	if len(googleMetricList) == 0 {
		s.logger.Warn("google metrics not found, hence not reporting anything.")
		return 0, nil
	}

	s.logger.Debugf("Reporting First Party Google Metrics, Google Metric First Party list count: %d", len(googleMetricList))
	resultChan := make(chan []common.MetricsResult, 200)
	var wg sync.WaitGroup
	wg.Add(1)
	operationTime := time.Now().Unix()
	droppedCh := make(chan int, 1)
	go func() {
		droppedCh <- s.metricClient.ReportMetrics(ctx, googleMetricList, operationTime, operationTime, &wg, resultChan)
	}()

	summary := s.processResponse(ctx, resultChan)
	dropped := <-droppedCh

	failedCount = s.pushFailedCount(summary, len(googleMetricList))
	submitted := len(googleMetricList) - dropped
	if ctx.Err() != nil && summary.total < submitted {
		return failedCount, ctx.Err()
	}
	if submitted == 0 {
		return failedCount, nil
	}
	if summary.total == 0 {
		return failedCount, fmt.Errorf("received delivery results for 0 of %d performance metrics", submitted)
	}
	if summary.succeeded == 0 {
		return failedCount, fmt.Errorf("all %d reported performance metrics failed delivery", summary.total)
	}
	if failedCount > 0 {
		s.logger.Warnf(
			"Performance sink partial delivery: %d failed, %d succeeded of %d metrics",
			failedCount,
			summary.succeeded,
			len(googleMetricList),
		)
	}
	return failedCount, nil
}

func (s *GoogleSink) pushFailedCount(summary deliverySummary, expected int) int {
	missingResultCount := expected - summary.total
	if missingResultCount > 0 {
		s.metricsRecorder.RecordSinkDelivered(&monitoring.MetricRecorderParams{
			SinkType:       "performance",
			FailedQuantity: float64(missingResultCount),
		})
	}
	return summary.failed + missingResultCount
}

type deliverySummary struct {
	total     int
	succeeded int
	failed    int
}

// processResponse consumes all Google delivery results and returns their aggregate outcome.
func (s *GoogleSink) processResponse(ctx context.Context, resultChan chan []common.MetricsResult) deliverySummary {
	// Extract correlation ID from context for logging
	logger := util.GetLogger(ctx)
	correlationID := "unknown"
	if loggerFields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		if corrIDStr, exists := loggerFields["requestCorrelationID"].(string); exists {
			correlationID = corrIDStr
		}
	}

	logger.Infof("Processing performance metrics results with correlation ID: %s", correlationID)

	var summary deliverySummary
	for result := range resultChan {
		batchSummary := s.processAndFilterMetricsResults(result)
		summary.total += batchSummary.total
		summary.succeeded += batchSummary.succeeded
		summary.failed += batchSummary.failed
	}

	logger.Info("Finished processing Google Performance Metrics Results")
	return summary
}

// processAndFilterMetricsResults - Process and Filter Metrics Result
// Parameters:
// - results: List of Metrics Result
func (s *GoogleSink) processAndFilterMetricsResults(results []common.MetricsResult) deliverySummary {
	var goodResults []common.MetricsResult

	s.logger.Infof("Reporting %d metrics.", len(results))

	for _, result := range results {
		id, err := result.GoogleMetric.GetCustomerId()
		if err != nil {
			s.logger.Warnf("Failed to get Customer ID for GoogleMetric: %v, error: %v", result.GoogleMetric, err)
			continue
		}

		if result.Exception != nil {
			resultCode := common.GetCode(&result)
			metricInfo := formatMetricForLogging(result.GoogleMetric)

			switch {
			case strings.Contains(resultCode, HTTPForbiddenError), strings.Contains(resultCode, HTTPNotFoundError):
				s.logger.Debugf("Performance metrics delivery failed with %s error - %s, OperationId: %s, OperationName: %s, ProjectId: %s, Exception: %v",
					resultCode, metricInfo, result.OperationID, result.OperationName, id, getCodeResponse(result.ReportResponse))
			default:
				s.logger.Warnf("Performance metrics delivery failed with exception - %s, OperationId: %s, OperationName: %s, ProjectId: %s, Exception: %v",
					metricInfo, result.OperationID, result.OperationName, id, result.GetException().Error())
			}
		} else if result.ReportResponse != nil && result.ReportResponse.ReportErrors != nil && len(result.ReportResponse.ReportErrors) > 0 {
			metricInfo := formatMetricForLogging(result.GoogleMetric)
			s.logger.Warnf("Performance metrics delivery failed with report errors - %s, OperationId: %s, OperationName: %s, ProjectId: %s, ReportErrors: %v",
				metricInfo, result.OperationID, result.OperationName, id, getCodeResponse(result.ReportResponse))
		} else {
			goodResults = append(goodResults, result)
		}
	}
	metricRecorderParams := &monitoring.MetricRecorderParams{
		SinkType:          "performance",
		SubmittedQuantity: float64(len(goodResults)),
		FailedQuantity:    float64(len(results) - len(goodResults)),
	}
	s.metricsRecorder.RecordSinkDelivered(metricRecorderParams)
	s.logger.Infof("%d metrics were successfully reported.", len(goodResults))
	return deliverySummary{
		total:     len(results),
		succeeded: len(goodResults),
		failed:    len(results) - len(goodResults),
	}
}

func getCodeResponse(response *common.ReportResponse) string {
	if response == nil {
		return "unknown"
	}
	if len(response.ReportErrors) == 0 {
		return "200"
	}
	return strconv.FormatInt(response.ReportErrors[0].Status.Code, 10)
}
