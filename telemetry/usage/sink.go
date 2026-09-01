package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	database2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	datamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/googlePusher"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

// GoogleUsageSink is responsible for delivering metrics to Google.
type GoogleUsageSink struct {
	metricClient         googlePusher.GoogleMetricsClient
	metricsdb            database2.Storage
	logger               log.Logger
	config               *common.TelemetryConfig
	aggregatedUsageTable string // Cached table name for AggregatedUsage
	metricsRecorder      monitoring.MetricsRecorder
}

// updateInfo holds information needed for batch database updates
type updateInfo struct {
	id      int64
	updates map[string]interface{}
}

// Constants for bulk update query field count
// These fields are: id, state, error_message, error_count, submission
const bulkUpdateFieldCount = 5

func NewSink(ctx context.Context, config *common.TelemetryConfig, metricsdb database2.Storage, metricsRecorder monitoring.MetricsRecorder) *GoogleUsageSink {
	sink := &GoogleUsageSink{
		metricClient:    *googlePusher.NewGoogleMetricsClient(ctx, config.UsageRootUrl, config),
		logger:          util.GetLogger(ctx),
		metricsdb:       metricsdb,
		config:          config,
		metricsRecorder: metricsRecorder,
	}

	// Initialize cached table name for AggregatedUsage
	sink.initializeTableName()

	return sink
}

// initializeTableName computes and caches the table name for AggregatedUsage
func (s *GoogleUsageSink) initializeTableName() {
	// Get a temporary GORM connection to determine table name with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := s.metricsdb.WithTransaction(ctx, func(tx dbutils.Transaction) error {
		// Handle case where tx.GORM() might return nil (e.g., in tests)
		db := tx.GORM()
		if db == nil {
			s.logger.Warnf("GORM database is nil during table name initialization, using fallback")
			s.aggregatedUsageTable = "aggregated_usages"
			return nil
		}

		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(&datamodel.AggregatedUsage{}); err != nil {
			s.logger.Warnf("Error parsing GORM statement for table name during initialization: %v", err)
			// Fallback to hardcoded table name following VSA Control Plane patterns
			s.aggregatedUsageTable = "aggregated_usages"
		} else {
			s.aggregatedUsageTable = stmt.Schema.Table
		}
		return nil
	})

	if err != nil {
		s.logger.Warnf("Error initializing table name, using fallback: %v", err)
		s.aggregatedUsageTable = "aggregated_usages"
	}

	s.logger.Debugf("Initialized AggregatedUsage table name: %s", s.aggregatedUsageTable)
}

func (s *GoogleUsageSink) DeliverMetrics(ctx context.Context, aggregatedRecords []datamodel.AggregatedUsage, aggregationEndTime time.Time) (failedCount int, err error) {
	s.logger.Debug("MapAggregatedRecordsToBilling: Received Number of records to map", "Number of records:", len(aggregatedRecords))
	validUsage, err := s.filterValidUsage(aggregatedRecords)
	if err != nil {
		return 0, err
	}
	if len(validUsage) != len(aggregatedRecords) {
		s.logger.Errorf("In total, %d records were dropped during mapping because they were invalid.", len(aggregatedRecords)-len(validUsage))
	}
	googleMetrics := s.completeRecords(validUsage)
	var failed int
	if pushErr := s.processGcpUnifiedMetrics(ctx, googleMetrics, &failed, aggregationEndTime); pushErr != nil {
		return failed, pushErr
	}
	return failed, nil
}

func (s *GoogleUsageSink) completeRecords(records []datamodel.AggregatedUsage) []common.GoogleMetric {
	var firstPartyGoogleMetrics []common.GoogleMetric
	for _, record := range records {
		switch record.MeasuredType {
		case metadata.MeasuredTypeValue(metadata.BackupLogicalSize):
			record.Quantity = float64(utils2.MibtoKib(record.Quantity))
		case metadata.MeasuredTypeValue(metadata.BackupEnabledVolumeAllocatedSize):
			record.Quantity = float64(utils2.MibHoursToGibHoursWithRoundOff(record.Quantity))
		case metadata.MeasuredTypeValue(metadata.XregionReplicationTotalTransferBytes):
			record.Quantity = float64(utils2.MibToBytes(record.Quantity))
		case metadata.MeasuredTypeValue(metadata.CbsCrossRegionVolumeRestoreTransferBytes):
			record.Quantity = float64(utils2.MibToBytes(record.Quantity))
		case metadata.MeasuredTypeValue(metadata.CbsCrossRegionVolumeBackupTransferBytes):
			record.Quantity = float64(utils2.MibToBytes(record.Quantity))
		case metadata.MeasuredTypeValue(metadata.CoolTierDataReadSizeRaw), metadata.MeasuredTypeValue(metadata.CoolTierDataWriteSizeRaw):
			record.Quantity = float64(utils2.MibToBytes(record.Quantity))
		case metadata.MeasuredTypeValue(metadata.PoolHotTierProvisionedSize), metadata.MeasuredTypeValue(metadata.PoolCapacityTierLogicalFootprint):
			// Tier sizes: Keep as MiB-hours, pass through as-is
		default:
			record.Quantity = float64(utils2.MibHoursToGibHours(record.Quantity))
		}

		googleMetric := &common.GoogleMetric{Record: &record}
		missingFields := googleMetric.Validate()

		if len(missingFields) == 0 {
			s.logger.Debug("Google Usage Mapping with ID is ready for billing", "Record ID: ", record.ID)
			firstPartyGoogleMetrics = append(firstPartyGoogleMetrics, *googleMetric)
		} else {
			record.State = datamodel.Invalid
			s.logger.Errorf("Google Usage Mapping with ID %d failed GoogleMetric validation: missing fields %s", record.ID, strings.Join(missingFields, ", "))
		}
	}
	return firstPartyGoogleMetrics
}

func (s *GoogleUsageSink) filterValidUsage(aggregatedRecords []datamodel.AggregatedUsage) ([]datamodel.AggregatedUsage, error) {
	validUsage := make([]datamodel.AggregatedUsage, 0, len(aggregatedRecords))
	for _, usage := range aggregatedRecords {
		u := usage
		if !u.IsBillable {
			s.logger.Debug("Skipping usage: Not mapping usage record as it is non-billable usage.",
				"Record ID", u.ID)
			continue
		}
		if u.ID == 0 {
			s.logger.Error("Skipping usage: Not mapping usage record due to unset id.", "Record ID", u.ID)
			continue
		}
		if s.isValid(u) {
			validUsage = append(validUsage, u)
		}
	}
	if len(validUsage) != len(aggregatedRecords) {
		s.logger.Errorf("Found records that are not appropriate for billing. Not mapping them. Number of records: %d", len(aggregatedRecords)-len(validUsage))
	}
	return validUsage, nil
}

func (s *GoogleUsageSink) isValid(usage datamodel.AggregatedUsage) bool {
	if usage.VendorCustomerID == nil || *usage.VendorCustomerID == "" {
		s.logger.Error("Skipping usage: Not mapping usage record due to missing project ID/number.",
			"Record ID", usage.ID)
		return false
	}
	return true
}

func (s *GoogleUsageSink) processGcpUnifiedMetrics(ctx context.Context, googleMetrics []common.GoogleMetric, failedCount *int, aggregationEndTime time.Time) error {
	if len(googleMetrics) == 0 {
		s.logger.Info("No Google usage metrics processed in this run.")
		*failedCount = 0
		return nil
	}

	return s.push(ctx, googleMetrics, failedCount, aggregationEndTime)
}

// push reports billing metrics to Google and records per-record results. ReportMetrics
// and processResponse honor ctx at blocking points; on cancel push returns ctx.Err()
// immediately (no wg.Wait hang). Partial localFailed is not published on cancel.
func (s *GoogleUsageSink) push(ctx context.Context, googleMetrics []common.GoogleMetric, failedCount *int, aggregationEndTime time.Time) error {
	if len(googleMetrics) == 0 {
		s.logger.Warn("Google first party billing metrics not found, hence not reporting anything.")
		*failedCount = 0
		return nil
	}

	wg := sync.WaitGroup{}
	// Buffer one batch so ReportMetrics can deliver while processResponse is in DB work.
	fpResultChan := make(chan []common.MetricsResult, 1)
	operationTime := aggregationEndTime.Add(time.Duration(s.config.TargetMinute) * time.Minute).Unix()

	s.logger.Debug("Reporting First Party Google Billing Metrics", "Google Metric First Party list count: ", len(googleMetrics))

	// localFailed is owned by this invocation; processResponse writes only to it,
	// and we copy it to the caller's pointer solely on the success (done) path.
	var localFailed int
	wg.Add(2)
	go s.metricClient.ReportMetrics(ctx, googleMetrics, operationTime, operationTime, &wg, fpResultChan)
	go s.processResponse(ctx, &wg, fpResultChan, &localFailed)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		if err := ctx.Err(); err != nil {
			return err
		}
		*failedCount = localFailed
		return nil
	case <-ctx.Done():
		s.logger.Warnf("usage push aborted by context: %v", ctx.Err())
		return ctx.Err()
	}
}

func (s *GoogleUsageSink) processResponse(ctx context.Context, wg *sync.WaitGroup, resultChan chan []common.MetricsResult, failedCount *int) {
	defer wg.Done()

	// Extract correlation ID from context for logging
	logger := util.GetLogger(ctx)
	correlationID := "unknown"
	if loggerFields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		if corrIDStr, exists := loggerFields["requestCorrelationID"].(string); exists {
			correlationID = corrIDStr
		}
	}

	logger.Infof("Processing usage metrics results with correlation ID: %s", correlationID)

	totalFailedCount := 0
	measuredTypesInfo := make(map[string]float64)
	finish := func() {
		logger.Info("Finished processing Google Usage Metrics Results")

		if failedCount != nil {
			*failedCount = totalFailedCount
		}
		for measuredType, count := range measuredTypesInfo {
			metricRecorderParams := &monitoring.MetricRecorderParams{
				SinkType:           "usage",
				MeasuredType:       getMeasuredTypesWithUnits(metadata.MeasuredTypeFromValue(measuredType)),
				SubmittedQuantity:  count,
				SubmittedTimeStamp: time.Now(),
			}
			s.metricsRecorder.RecordBillingMetricsSubmission(metricRecorderParams)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case result, ok := <-resultChan:
			if !ok {
				finish()
				return
			}
			var batchFailedCount int
			s.processMetricsResults(ctx, result, &batchFailedCount, measuredTypesInfo)
			totalFailedCount += batchFailedCount
		}
	}
}

func (s *GoogleUsageSink) processMetricsResults(ctx context.Context, gcpResults []common.MetricsResult, failedCount *int, measuredTypesInfo map[string]float64) {
	goodResults, errorResults, exceptionResults := common.FilterErrorResults(gcpResults)

	s.logger.Infof("%d metrics were successfully reported.", len(goodResults))
	s.logger.Infof("%d metrics were not reported.", len(errorResults)+len(exceptionResults))

	// Count failed records (both errors and exceptions)
	failedRecordsCount := len(errorResults) + len(exceptionResults)

	// Check feature flag to determine update strategy
	if s.config.EnableBatchUsageUpdates {
		s.processMetricsResultsBatch(ctx, gcpResults, measuredTypesInfo)
	} else {
		s.processMetricsResultsSingle(ctx, gcpResults, measuredTypesInfo)
	}

	// Record metrics delivered to sink - track COUNT of records
	metricRecorderParams := &monitoring.MetricRecorderParams{
		SinkType:          "usage",
		SubmittedQuantity: float64(len(goodResults)),
		FailedQuantity:    float64(failedRecordsCount),
	}
	s.metricsRecorder.RecordSinkDelivered(metricRecorderParams)

	// Set failed count if pointer is provided
	if failedCount != nil {
		*failedCount = failedRecordsCount
	}
}

// processMetricsResultsSingle processes updates one by one (fallback implementation)
func (s *GoogleUsageSink) processMetricsResultsSingle(ctx context.Context, gcpResults []common.MetricsResult, measuredTypesInfo map[string]float64) {
	for _, result := range gcpResults {
		billingRecord, err := result.GoogleMetric.GetAsUsageBillingMetric()
		if err != nil {
			s.logger.Warn("It appears that a non-billing Google Metric was reported by the billing paradigm. "+
				"This should not happen, and its response cannot be processed.", "The error is:", err,
				"The Google Metric is:", result)
			continue
		}

		resultCode := common.GetCode(&result)
		s.logger.Debugf("Processing the Google Metric Result for service: %s; result is %v, resultCode: %s", s.config.PusherServiceName, spew.Sdump(result), resultCode)
		billingRecord.Submission = nillable.ToPointer(result.GetOperationID())
		errorMessage := ""
		if isSuccessful(result.ReportResponse) {
			billingRecord.State = datamodel.Submitted
			billingRecord.ErrorMessage = nil
			// Track successful measured types
			measuredTypesInfo[billingRecord.MeasuredType] += billingRecord.Quantity
		} else {
			errorMessage = common.GetErrorMessage(&result)
			billingRecord.ErrorMessage = &errorMessage
			billingRecord.ErrorCount = billingRecord.ErrorCount + 1
			billingRecord.State = datamodel.Error
		}
		s.logger.Infof("Updating usage information for billingRecord ID: %d, state: %v", billingRecord.ID, billingRecord.State)

		// Create update map with only the fields that need to be updated
		updates := map[string]interface{}{
			"state":         billingRecord.State,
			"error_message": billingRecord.ErrorMessage,
			"error_count":   billingRecord.ErrorCount,
		}

		// Handle submission field - it's a JSONB field in database, so JSON-encode the string value
		if billingRecord.Submission != nil {
			// JSON-encode the UUID string for JSONB storage
			submissionJSON, err := json.Marshal(*billingRecord.Submission)
			if err != nil {
				s.logger.Warnf("Error marshaling submission for database update - submission: %s, error: %v", *billingRecord.Submission, err)
			} else {
				updates["submission"] = string(submissionJSON)
			}
		}

		if err := s.metricsdb.UpdateAggregatedUsage(ctx, billingRecord.ID, updates); err != nil {
			s.logger.Warnf("Error updating usage information - billingRecord ID: %d, error: %v", billingRecord.ID, err)
		}
	}
}

// processMetricsResultsBatch processes updates in batches using PostgreSQL bulk operations
func (s *GoogleUsageSink) processMetricsResultsBatch(ctx context.Context, gcpResults []common.MetricsResult, measuredTypesInfo map[string]float64) {
	// Collect all updates first
	updatesBatch := make([]updateInfo, 0, len(gcpResults))

	for _, result := range gcpResults {
		billingRecord, err := result.GoogleMetric.GetAsUsageBillingMetric()
		if err != nil {
			s.logger.Warn("It appears that a non-billing Google Metric was reported by the billing paradigm. "+
				"This should not happen, and its response cannot be processed.", "The error is:", err,
				"The Google Metric is:", result)
			continue
		}

		resultCode := common.GetCode(&result)
		s.logger.Debugf("Processing the Google Metric Result for service: %s; result is %v, resultCode: %s", s.config.PusherServiceName, spew.Sdump(result), resultCode)
		billingRecord.Submission = nillable.ToPointer(result.GetOperationID())
		errorMessage := ""
		if isSuccessful(result.ReportResponse) {
			billingRecord.State = datamodel.Submitted
			billingRecord.ErrorMessage = nil

			// Track successful measured types
			measuredTypesInfo[billingRecord.MeasuredType] += billingRecord.Quantity
		} else {
			errorMessage = common.GetErrorMessage(&result)
			billingRecord.ErrorMessage = &errorMessage
			billingRecord.ErrorCount = billingRecord.ErrorCount + 1
			billingRecord.State = datamodel.Error
		}
		s.logger.Debugf("Preparing batch update for billingRecord ID: %d, state: %v", billingRecord.ID, billingRecord.State)

		// Create update map with only the fields that need to be updated
		updates := map[string]interface{}{
			"state":         billingRecord.State,
			"error_message": billingRecord.ErrorMessage,
			"error_count":   billingRecord.ErrorCount,
		}

		// Handle submission field - it's a JSONB field in database, so JSON-encode the string value
		if billingRecord.Submission != nil {
			// JSON-encode the UUID string for JSONB storage
			submissionJSON, err := json.Marshal(*billingRecord.Submission)
			if err != nil {
				s.logger.Warnf("Error marshaling submission for database update - submission: %s, error: %v", *billingRecord.Submission, err)
			} else {
				updates["submission"] = string(submissionJSON)
			}
		}

		updatesBatch = append(updatesBatch, updateInfo{
			id:      billingRecord.ID,
			updates: updates,
		})
	}

	// Process updates in batch
	s.batchUpdateAggregatedUsage(ctx, updatesBatch)
}

// batchUpdateAggregatedUsage performs high-performance batch updates using PostgreSQL bulk operations
func (s *GoogleUsageSink) batchUpdateAggregatedUsage(ctx context.Context, updatesBatch []updateInfo) {
	if len(updatesBatch) == 0 {
		return
	}

	batchSize := s.config.ResultUpdateBatchSize
	for i := 0; i < len(updatesBatch); i += batchSize {
		end := i + batchSize
		if end > len(updatesBatch) {
			end = len(updatesBatch)
		}
		batch := updatesBatch[i:end]

		// Process batch updates within a transaction using PostgreSQL bulk update
		err := s.metricsdb.WithTransaction(ctx, func(tx dbutils.Transaction) error {
			sql, args := s.buildBulkUpdateQuery(tx.GORM(), batch)
			if strings.TrimSpace(sql) == "" {
				s.logger.Warnf("Skipping bulk update for batch %d-%d: empty SQL query generated", i+1, end)
				return nil
			}
			if err := tx.GORM().Exec(sql, args...).Error; err != nil {
				s.logger.Warnf("Error executing bulk update for batch %d-%d: %v", i+1, end, err)
				return err
			}
			return nil
		})

		if err != nil {
			s.logger.Warnf("Error executing batch update transaction for batch %d-%d: %v", i+1, end, err)
			// Fallback to individual updates if batch fails
			s.logger.Warnf("Falling back to individual updates for batch %d-%d", i+1, end)
			s.fallbackToIndividualUpdates(ctx, batch)
		}
	}

	s.logger.Infof("Completed batch update of %d usage records", len(updatesBatch))
}

// buildBulkUpdateQuery creates a PostgreSQL bulk update query using UPDATE ... FROM VALUES pattern
// This is much more efficient than individual UPDATE statements
func (s *GoogleUsageSink) buildBulkUpdateQuery(db *gorm.DB, batch []updateInfo) (string, []interface{}) {
	if len(batch) == 0 {
		return "", nil
	}

	// Build VALUES clause with placeholders
	// Each update has: id, state, error_message, error_count, submission
	placeholders := make([]string, 0, len(batch))
	args := make([]interface{}, 0, len(batch)*bulkUpdateFieldCount) // bulkUpdateFieldCount fields per update
	paramCounter := 1

	for _, update := range batch {
		idParam := paramCounter
		stateParam := paramCounter + 1
		errorMsgParam := paramCounter + 2
		errorCountParam := paramCounter + 3
		submissionParam := paramCounter + 4

		// Build placeholder: (id, state, error_message, error_count, submission)
		placeholders = append(placeholders, fmt.Sprintf(
			"($%d::bigint, $%d::integer, $%d::text, $%d::integer, $%d::jsonb)",
			idParam, stateParam, errorMsgParam, errorCountParam, submissionParam))

		// Extract values from updates map
		var state int32
		var errorMessage interface{}
		var errorCount int32
		var submission interface{}

		if val, ok := update.updates["state"]; ok {
			if stateVal, ok := val.(datamodel.TrackingState); ok {
				state = int32(stateVal)
			} else {
				s.logger.Warnf("Type assertion for 'state' failed: got value=%#v (type=%T) for billingRecord ID: %d", val, val, update.id)
				// Use zero value as fallback - caller should ensure proper type
			}
		}

		if val, ok := update.updates["error_message"]; ok {
			errorMessage = val // Can be nil for NULL
		} else {
			errorMessage = nil
		}

		if val, ok := update.updates["error_count"]; ok {
			if countVal, ok := val.(int32); ok {
				errorCount = countVal
			} else {
				s.logger.Warnf("Type assertion for 'error_count' failed: got value=%#v (type=%T) for billingRecord ID: %d", val, val, update.id)
				// Use zero value as fallback - caller should ensure proper type
			}
		}

		if val, ok := update.updates["submission"]; ok {
			// submission is already JSON-encoded string from the calling code
			if submissionStr, ok := val.(string); ok {
				submission = submissionStr
			} else {
				s.logger.Warnf("Type assertion for 'submission' failed: got value=%#v (type=%T) for billingRecord ID: %d", val, val, update.id)
				submission = nil
			}
		} else {
			submission = nil
		}

		// Add arguments
		args = append(args, update.id, state, errorMessage, errorCount, submission)
		paramCounter += bulkUpdateFieldCount
	}

	// Use cached table name for efficiency
	tableName := s.aggregatedUsageTable

	// Build the SQL query
	// Use COALESCE to preserve existing submission when NULL is provided
	sql := fmt.Sprintf(`
		UPDATE %s 
		SET 
			state = tmp.state,
			error_message = tmp.error_message,
			error_count = tmp.error_count,
			submission = COALESCE(tmp.submission::jsonb, %s.submission),
			updated_at = NOW()
		FROM (VALUES %s) AS tmp(id, state, error_message, error_count, submission)
		WHERE %s.id = tmp.id`,
		tableName, tableName, strings.Join(placeholders, ", "), tableName)

	return sql, args
}

// fallbackToIndividualUpdates falls back to individual updates if batch update fails
func (s *GoogleUsageSink) fallbackToIndividualUpdates(ctx context.Context, batch []updateInfo) {
	for _, update := range batch {
		if err := s.metricsdb.UpdateAggregatedUsage(ctx, update.id, update.updates); err != nil {
			s.logger.Warnf("Error updating usage information (fallback) - billingRecord ID: %d, error: %v", update.id, err)
		}
	}
}

func isSuccessful(response *common.ReportResponse) bool {
	return response != nil && (len(response.ReportErrors) == 0)
}

func getMeasuredTypesWithUnits(measuredType metadata.MeasuredType) string {
	switch measuredType {
	case metadata.BackupLogicalSize:
		return fmt.Sprintf("%s_IN_KiB", measuredType)
	case metadata.BackupEnabledVolumeAllocatedSize:
		return fmt.Sprintf("%s_IN_GiB-hours", measuredType)
	case metadata.XregionReplicationTotalTransferBytes:
		return fmt.Sprintf("%s_IN_Bytes", measuredType)
	case metadata.CbsCrossRegionVolumeRestoreTransferBytes:
		return fmt.Sprintf("%s_IN_Bytes", measuredType)
	case metadata.CbsCrossRegionVolumeBackupTransferBytes:
		return fmt.Sprintf("%s_IN_Bytes", measuredType)
	case metadata.CoolTierDataReadSizeRaw, metadata.CoolTierDataWriteSizeRaw:
		return fmt.Sprintf("%s_IN_Bytes", measuredType)
	case metadata.PoolHotTierProvisionedSize, metadata.PoolCapacityTierLogicalFootprint:
		return fmt.Sprintf("%s_IN_MiB-hours", measuredType)
	default:
		return fmt.Sprintf("%s_IN_GiB-hours", measuredType)
	}
}
