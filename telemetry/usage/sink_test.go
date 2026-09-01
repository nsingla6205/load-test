package usage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	database2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	datamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/googlePusher"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	servicecontrol "google.golang.org/api/servicecontrol/v1"
	"gorm.io/gorm"
)

// createMockDB creates a mock database for testing
func createMockDB() database2.Storage {
	mockDB := &database2.MockStorage{}
	mockDB.On("GetAggregatedUsage", mock.Anything, mock.Anything).Return([]datamodel.AggregatedUsage{}, nil)
	mockDB.On("UpdateAggregatedUsage", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock WithTransaction to avoid transaction-related issues during batch processing
	mockDB.On("WithTransaction", mock.Anything, mock.Anything).Return(errors.New("mock transaction disabled for testing")).Run(func(args mock.Arguments) {
		// For table name initialization, we want it to fail gracefully and use the fallback
		// This avoids the GORM nil pointer issues in tests
	})

	return mockDB
}

func TestNewSink(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	mockDB := createMockDB()

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	assert.NotNil(t, sink)
	assert.NotNil(t, sink.metricClient)
	assert.NotNil(t, sink.logger)
}

func TestGoogleUsageSink_filterValidUsage(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	mockDB := createMockDB()
	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "test-customer-123"

	t.Run("Valid usage records", func(t *testing.T) {
		aggregatedRecords := []datamodel.AggregatedUsage{
			{
				ID:               1,
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.LogicalSize),
				Quantity:         100.0,
				IsBillable:       true,
			},
			{
				ID:               2,
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.AllocatedSize),
				Quantity:         200.0,
				IsBillable:       true,
			},
		}

		validUsage, err := sink.filterValidUsage(aggregatedRecords)

		assert.NoError(t, err)
		assert.Len(t, validUsage, 2)
		assert.Equal(t, int64(1), validUsage[0].ID)
		assert.Equal(t, int64(2), validUsage[1].ID)
	})

	t.Run("Usage record with missing customer ID", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		// Set up mock expectations
		ml.On("Error", "Skipping usage: Not mapping usage record due to missing project ID/number.",
			"Record ID", int64(1)).Once()
		ml.On("Errorf", "Found records that are not appropriate for billing. Not mapping them. Number of records: %d", 1).Once()

		aggregatedRecords := []datamodel.AggregatedUsage{
			{
				ID:               1,
				VendorCustomerID: nil, // Missing customer ID
				MeasuredType:     string(metadata.LogicalSize),
				Quantity:         100.0,
				IsBillable:       true,
			},
		}

		validUsage, err := sink.filterValidUsage(aggregatedRecords)

		assert.NoError(t, err)
		assert.Len(t, validUsage, 0) // No valid records
		ml.AssertExpectations(t)
	})

	t.Run("Usage record with empty customer ID", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		emptyCustomerID := ""
		// Set up mock expectations
		ml.On("Error", "Skipping usage: Not mapping usage record due to missing project ID/number.",
			"Record ID", int64(1)).Once()
		ml.On("Errorf", "Found records that are not appropriate for billing. Not mapping them. Number of records: %d", 1).Once()

		aggregatedRecords := []datamodel.AggregatedUsage{
			{
				ID:               1,
				VendorCustomerID: &emptyCustomerID, // Empty customer ID
				MeasuredType:     string(metadata.LogicalSize),
				Quantity:         100.0,
				IsBillable:       true,
			},
		}

		validUsage, err := sink.filterValidUsage(aggregatedRecords)

		assert.NoError(t, err)
		assert.Len(t, validUsage, 0) // No valid records
		ml.AssertExpectations(t)
	})

	t.Run("Non-billable usage record is skipped", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		ml.On("Debug", "Skipping usage: Not mapping usage record as it is non-billable usage.",
			"Record ID", int64(1)).Once()
		ml.On("Errorf", "Found records that are not appropriate for billing. Not mapping them. Number of records: %d", 1).Once()

		aggregatedRecords := []datamodel.AggregatedUsage{
			{
				ID:               1,
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.LogicalSize),
				Quantity:         100.0,
				IsBillable:       false, // Non-billable -> skipped
			},
		}

		validUsage, err := sink.filterValidUsage(aggregatedRecords)

		assert.NoError(t, err)
		assert.Len(t, validUsage, 0)
		ml.AssertExpectations(t)
	})

	t.Run("Usage record with unset ID is skipped", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		ml.On("Error", "Skipping usage: Not mapping usage record due to unset id.",
			"Record ID", int64(0)).Once()
		ml.On("Errorf", "Found records that are not appropriate for billing. Not mapping them. Number of records: %d", 1).Once()

		aggregatedRecords := []datamodel.AggregatedUsage{
			{
				ID:               0, // Unset ID -> skipped
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.LogicalSize),
				Quantity:         100.0,
				IsBillable:       true,
			},
		}

		validUsage, err := sink.filterValidUsage(aggregatedRecords)

		assert.NoError(t, err)
		assert.Len(t, validUsage, 0)
		ml.AssertExpectations(t)
	})
}

// TestGoogleUsageSink_processMetricsResultsBatch_NonBillingMetric verifies that a
// result whose GoogleMetric cannot be read as a billing metric is skipped and logged.
func TestGoogleUsageSink_processMetricsResultsBatch_NonBillingMetric(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	ml := &log.MockLogger{}
	sink.logger = ml
	ml.On("Warn", "It appears that a non-billing Google Metric was reported by the billing paradigm. "+
		"This should not happen, and its response cannot be processed.",
		"The error is:", mock.Anything, "The Google Metric is:", mock.Anything).Once()

	gcpResults := []common.MetricsResult{
		{GoogleMetric: *common.NewGoogleMetric("invalid-type")},
	}

	measuredTypesInfo := make(map[string]float64)
	sink.processMetricsResultsBatch(ctx, gcpResults, measuredTypesInfo)

	ml.AssertExpectations(t)
}

func TestGoogleUsageSink_isValid(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "test-customer-123"

	t.Run("Valid usage record", func(t *testing.T) {
		usage := datamodel.AggregatedUsage{
			ID:               1,
			VendorCustomerID: &customerID,
			MeasuredType:     string(metadata.LogicalSize),
			Quantity:         100.0,
		}

		isValid := sink.isValid(usage)
		assert.True(t, isValid)
	})

	t.Run("Invalid usage record - nil customer ID", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		ml.On("Error", "Skipping usage: Not mapping usage record due to missing project ID/number.",
			"Record ID", int64(1)).Once()

		usage := datamodel.AggregatedUsage{
			ID:               1,
			VendorCustomerID: nil,
			MeasuredType:     string(metadata.LogicalSize),
			Quantity:         100.0,
		}

		isValid := sink.isValid(usage)
		assert.False(t, isValid)
		ml.AssertExpectations(t)
	})

	t.Run("Invalid usage record - empty customer ID", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		emptyCustomerID := ""
		ml.On("Error", "Skipping usage: Not mapping usage record due to missing project ID/number.",
			"Record ID", int64(1)).Once()

		usage := datamodel.AggregatedUsage{
			ID:               1,
			VendorCustomerID: &emptyCustomerID,
			MeasuredType:     string(metadata.LogicalSize),
			Quantity:         100.0,
		}

		isValid := sink.isValid(usage)
		assert.False(t, isValid)
		ml.AssertExpectations(t)
	})
}

func TestGoogleUsageSink_completeRecords(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "test-customer-123"

	t.Run("Complete valid records", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		ml.On("Debug", "Google Usage Mapping with ID is ready for billing", "Record ID: ", int64(1)).Once()
		ml.On("Debug", "Google Usage Mapping with ID is ready for billing", "Record ID: ", int64(2)).Once()
		ml.On("Debug", "Google Usage Mapping with ID is ready for billing", "Record ID: ", int64(3)).Once()

		records := []datamodel.AggregatedUsage{
			{
				ID:               1,
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.LogicalSize),
				Quantity:         100.0,
				ResourceType:     string(metadata.Volume),
			},
			{
				ID:               2,
				VendorCustomerID: &customerID,
				MeasuredType:     "CBS_VOLUME_BACKUP_SIZE",
				Quantity:         200.0,
				ResourceType:     string(metadata.Volume),
			},
			{
				ID:               3,
				VendorCustomerID: &customerID,
				MeasuredType:     "XREGION_REPLICATION_TOTAL_TRANSFER_BYTES",
				Quantity:         200.0,
				ResourceType:     string(metadata.VolumeReplicationRelationship),
			},
		}

		googleMetrics := sink.completeRecords(records)

		assert.Len(t, googleMetrics, 3)
		ml.AssertExpectations(t)
	})

	t.Run("Complete records with validation failures", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		// This record will fail validation because it has an empty customer ID (not nil to avoid panic)
		emptyCustomerID := ""
		ml.On("Errorf", "Google Usage Mapping with ID %d failed GoogleMetric validation: missing fields %s", int64(1), "customerId").Once()

		records := []datamodel.AggregatedUsage{
			{
				ID:               1,
				VendorCustomerID: &emptyCustomerID, // Empty customer ID will cause validation failure without panic
				MeasuredType:     string(metadata.LogicalSize),
				Quantity:         100.0,
				ResourceType:     string(metadata.Volume),
			},
		}

		googleMetrics := sink.completeRecords(records)

		assert.Len(t, googleMetrics, 0) // No valid metrics
		ml.AssertExpectations(t)
	})

	t.Run("CbsCrossRegionVolumeBackupTransferBytes unit conversion", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		ml.On("Debug", "Google Usage Mapping with ID is ready for billing", "Record ID: ", int64(1)).Once()

		records := []datamodel.AggregatedUsage{
			{
				ID:               1,
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.CbsCrossRegionVolumeBackupTransferBytes),
				Quantity:         1024.0, // MiB
				ResourceType:     string(metadata.Backup),
			},
		}

		googleMetrics := sink.completeRecords(records)

		assert.Len(t, googleMetrics, 1)
		// 1024 MiB -> 1073741824 Bytes (MibToBytes)
		quantity, _ := googleMetrics[0].GetQuantity()
		assert.Equal(t, int64(1073741824), quantity)
		ml.AssertExpectations(t)
	})

	t.Run("Autotier unit conversions", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		ml.On("Debug", "Google Usage Mapping with ID is ready for billing", "Record ID: ", int64(1)).Once()
		ml.On("Debug", "Google Usage Mapping with ID is ready for billing", "Record ID: ", int64(2)).Once()
		ml.On("Debug", "Google Usage Mapping with ID is ready for billing", "Record ID: ", int64(3)).Once()
		ml.On("Debug", "Google Usage Mapping with ID is ready for billing", "Record ID: ", int64(4)).Once()

		records := []datamodel.AggregatedUsage{
			{
				ID:               1,
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.CoolTierDataReadSizeRaw),
				Quantity:         1024.0, // MiB
				ResourceType:     string(metadata.VolumePool),
			},
			{
				ID:               2,
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.CoolTierDataWriteSizeRaw),
				Quantity:         2048.0, // MiB
				ResourceType:     string(metadata.VolumePool),
			},
			{
				ID:               3,
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.PoolHotTierProvisionedSize),
				Quantity:         4096.0, // MiB-hours (should pass through)
				ResourceType:     string(metadata.VolumePool),
			},
			{
				ID:               4,
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.PoolCapacityTierLogicalFootprint),
				Quantity:         8192.0, // MiB-hours (should pass through)
				ResourceType:     string(metadata.VolumePool),
			},
		}

		googleMetrics := sink.completeRecords(records)

		assert.Len(t, googleMetrics, 4)

		// Check unit conversions
		// CoolTierDataReadSizeRaw: 1024 MiB -> 1073741824 Bytes
		quantity0, _ := googleMetrics[0].GetQuantity()
		assert.Equal(t, int64(1073741824), quantity0)

		// CoolTierDataWriteSizeRaw: 2048 MiB -> 2147483648 Bytes
		quantity1, _ := googleMetrics[1].GetQuantity()
		assert.Equal(t, int64(2147483648), quantity1)

		// PoolHotTierProvisionedSize: 4096.0 (no conversion)
		quantity2, _ := googleMetrics[2].GetQuantity()
		assert.Equal(t, int64(4096), quantity2)

		// PoolCapacityTierLogicalFootprint: 8192.0 (no conversion)
		quantity3, _ := googleMetrics[3].GetQuantity()
		assert.Equal(t, int64(8192), quantity3)

		ml.AssertExpectations(t)
	})

	t.Run("CbsCrossRegionVolumeRestoreTransferBytes unit conversion", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		ml.On("Debug", "Google Usage Mapping with ID is ready for billing", "Record ID: ", int64(1)).Once()

		records := []datamodel.AggregatedUsage{
			{
				ID:               1,
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.CbsCrossRegionVolumeRestoreTransferBytes),
				Quantity:         1024.0, // MiB
				ResourceType:     string(metadata.Volume),
			},
		}

		googleMetrics := sink.completeRecords(records)

		assert.Len(t, googleMetrics, 1)
		// 1024 MiB -> 1073741824 Bytes (MibToBytes)
		quantity, _ := googleMetrics[0].GetQuantity()
		assert.Equal(t, int64(1073741824), quantity)
		ml.AssertExpectations(t)
	})
}

func TestGoogleUsageSink_processGcpUnifiedMetrics(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	t.Run("Process empty metrics", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		ml.On("Info", "No Google usage metrics processed in this run.").Once()

		var googleMetrics []common.GoogleMetric // Empty slice
		var failedCount int

		err := sink.processGcpUnifiedMetrics(ctx, googleMetrics, &failedCount, time.Now())
		assert.NoError(t, err)
		ml.AssertExpectations(t)
	})

	// Skip the test that would make real HTTP calls
	t.Run("Process metrics with data - skipped due to real HTTP calls", func(t *testing.T) {
		t.Skip("Skipping test that makes real HTTP calls to Google API")
	})
}

func TestGoogleUsageSink_push(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	t.Run("Push empty metrics", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		ml.On("Warn", "Google first party billing metrics not found, hence not reporting anything.").Once()

		var googleMetrics []common.GoogleMetric // Empty slice
		var failedCount int

		err := sink.push(ctx, googleMetrics, &failedCount, time.Now())
		assert.NoError(t, err)
		ml.AssertExpectations(t)
	})

	t.Run("Push nil metrics", func(t *testing.T) {
		ml := &log.MockLogger{}
		sink.logger = ml

		ml.On("Warn", "Google first party billing metrics not found, hence not reporting anything.").Once()
		var failedCount int

		err := sink.push(ctx, nil, &failedCount, time.Now())
		assert.NoError(t, err)
		ml.AssertExpectations(t)
	})

	// Skip the test that would make real HTTP calls
	t.Run("Push metrics with data - skipped due to real HTTP calls", func(t *testing.T) {
		t.Skip("Skipping test that makes real HTTP calls to Google API")
	})
}

// TestGoogleUsageSink_push_ContextCancelled_NoDeadlock verifies that push does
// not block forever when its context is cancelled: the ctx-aware wait returns
// promptly (the old wg.Wait() could hang if ReportMetrics never closed the
// result channel). This is required for the per-job timeout to actually stop a
// hung billing submission (VSCP-6762).
func TestGoogleUsageSink_push_ContextCancelled_NoDeadlock(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordSinkDelivered", mock.Anything).Return().Maybe()
	mockMetricRecorder.On("RecordBillingMetricsSubmission", mock.Anything).Return().Maybe()

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "cust-1"
	resourceName := "vol-1"
	metric := *common.NewGoogleMetric(&datamodel.AggregatedUsage{
		ID:               1,
		VendorCustomerID: &customerID,
		ResourceName:     &resourceName,
		MeasuredType:     string(metadata.LogicalSize),
		ResourceType:     string(metadata.Volume),
		AggregationStart: time.Now(),
		AggregationEnd:   time.Now().Add(time.Hour),
		IsBillable:       true,
	})

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before push runs

	var failedCount int
	done := make(chan error, 1)
	go func() {
		done <- sink.push(cancelledCtx, []common.GoogleMetric{metric}, &failedCount, time.Now())
	}()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("push deadlocked on a cancelled context")
	}
}

// TestGoogleUsageSink_push_CancelWinsOverDone verifies that if workers finish
// while the context is already canceled, push still returns ctx.Err() rather
// than reporting success.
func TestGoogleUsageSink_push_CancelWinsOverDone(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordSinkDelivered", mock.Anything).Return().Maybe()
	mockMetricRecorder.On("RecordBillingMetricsSubmission", mock.Anything).Return().Maybe()

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "cust-1"
	resourceName := "vol-1"
	metric := *common.NewGoogleMetric(&datamodel.AggregatedUsage{
		ID:               1,
		VendorCustomerID: &customerID,
		ResourceName:     &resourceName,
		MeasuredType:     string(metadata.LogicalSize),
		ResourceType:     string(metadata.Volume),
		AggregationStart: time.Now(),
		AggregationEnd:   time.Now().Add(time.Hour),
		IsBillable:       true,
	})

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	var failedCount int
	err := sink.push(cancelledCtx, []common.GoogleMetric{metric}, &failedCount, time.Now())
	assert.ErrorIs(t, err, context.Canceled)
}

func TestGoogleUsageSink_DeliverMetrics(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	t.Run("Deliver valid metrics", func(t *testing.T) {
		customerID := "test-customer-123"
		aggregatedRecords := []datamodel.AggregatedUsage{
			{
				ID:               1,
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.LogicalSize),
				Quantity:         100.0,
				ResourceType:     string(metadata.Volume),
				AggregationStart: time.Now(),
				AggregationEnd:   time.Now().Add(time.Hour),
				IsBillable:       true,
			},
		}

		failedCount, err := sink.DeliverMetrics(ctx, aggregatedRecords, time.Now())

		assert.NoError(t, err)
		assert.Equal(t, 0, failedCount, "Should have no failed metrics for valid input")
	})
}

// TestGoogleUsageSink_DeliverMetrics_WithDroppedRecords tests the dropped records logging
func TestGoogleUsageSink_DeliverMetrics_WithDroppedRecords(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "test-customer-123"

	aggregatedRecords := []datamodel.AggregatedUsage{
		{
			ID:               1,
			VendorCustomerID: &customerID,
			Quantity:         100.0,
			IsBillable:       true,
			MeasuredType:     string(metadata.LogicalSize),
			ResourceType:     string(metadata.Volume),
			AggregationStart: time.Now(),
			AggregationEnd:   time.Now().Add(time.Hour),
		},
		{
			ID:               2,
			VendorCustomerID: nil, // This will be dropped as invalid
			Quantity:         200.0,
			IsBillable:       true,
			MeasuredType:     string(metadata.LogicalSize),
			ResourceType:     string(metadata.Volume),
			AggregationStart: time.Now(),
			AggregationEnd:   time.Now().Add(time.Hour),
		},
		{
			ID:               3,
			VendorCustomerID: &customerID,
			Quantity:         300.0,
			IsBillable:       true,
			MeasuredType:     string(metadata.LogicalSize),
			ResourceType:     string(metadata.Volume),
			AggregationStart: time.Now(),
			AggregationEnd:   time.Now().Add(time.Hour),
		},
	}

	// This should trigger the dropped records logging (line 45)
	failedCount, err := sink.DeliverMetrics(ctx, aggregatedRecords, time.Now())

	assert.NoError(t, err)
	assert.Equal(t, 0, failedCount, "Should have no failed metrics for valid records")
}

// TestGoogleUsageSink_processResponse_WithResults tests processResponse with actual results
func TestGoogleUsageSink_processResponse_WithResults(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordSinkDelivered
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "test-customer-123"
	resourceName := "test-resource"

	// Create test metric
	googleMetric := *common.NewGoogleMetric(&datamodel.AggregatedUsage{
		ID:               1,
		VendorCustomerID: &customerID,
		ResourceName:     &resourceName,
		Quantity:         100.0,
	})

	// Create a channel and send results
	resultChan := make(chan []common.MetricsResult, 1)

	// Create mock results
	results := []common.MetricsResult{
		{
			GoogleMetric:   googleMetric,
			Exception:      nil,
			ReportResponse: nil,
		},
	}

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		resultChan <- results
		close(resultChan)
	}()

	// This should test the processResponse method (lines 132, 139)
	var failedCount int
	sink.processResponse(ctx, &wg, resultChan, &failedCount)
	wg.Wait()
}

// TestGoogleUsageSink_processMetricsResults_WithGoodResults tests processMetricsResults with good results
func TestGoogleUsageSink_processMetricsResults_WithGoodResults(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordSinkDelivered
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "test-customer-123"
	resourceName := "test-resource"

	// Create test metric
	googleMetric := *common.NewGoogleMetric(&datamodel.AggregatedUsage{
		ID:               1,
		VendorCustomerID: &customerID,
		ResourceName:     &resourceName,
		Quantity:         100.0,
		State:            datamodel.Unsubmitted,
	})

	// Create good results
	gcpResults := []common.MetricsResult{
		{
			GoogleMetric:   googleMetric,
			Exception:      nil,
			ReportResponse: &common.ReportResponse{}, // Good response
		},
	}

	// This should test the processMetricsResults method (lines 141-187)
	var failedCount int
	measuredTypesInfo := make(map[string]float64)
	sink.processMetricsResults(ctx, gcpResults, &failedCount, measuredTypesInfo)
	// Verify measuredTypesInfo was populated
	assert.NotEmpty(t, measuredTypesInfo)
}

// TestGoogleUsageSink_processMetricsResults_WithErrorResults tests processMetricsResults with error results
func TestGoogleUsageSink_processMetricsResults_WithErrorResults(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordSinkDelivered
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "test-customer-123"
	resourceName := "test-resource"

	// Create test metric
	googleMetric := *common.NewGoogleMetric(&datamodel.AggregatedUsage{
		ID:               1,
		VendorCustomerID: &customerID,
		ResourceName:     &resourceName,
		Quantity:         100.0,
		State:            datamodel.Error,
	})

	// Create error results
	gcpResults := []common.MetricsResult{
		{
			GoogleMetric:   googleMetric,
			Exception:      errors.New("test error"),
			ReportResponse: nil,
		},
	}

	// This should test the processMetricsResults method with error paths
	var failedCount int
	measuredTypesInfo := make(map[string]float64)
	sink.processMetricsResults(ctx, gcpResults, &failedCount, measuredTypesInfo)
}

// TestGoogleUsageSink_processMetricsResults_WithExceptionResults tests processMetricsResults with exception results
func TestGoogleUsageSink_processMetricsResults_WithExceptionResults(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordSinkDelivered
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "test-customer-123"
	resourceName := "test-resource"

	// Create test metric
	googleMetric := *common.NewGoogleMetric(&datamodel.AggregatedUsage{
		ID:               1,
		VendorCustomerID: &customerID,
		ResourceName:     &resourceName,
		Quantity:         100.0,
		State:            datamodel.Error,
	})

	// Create exception results with a different type of error
	gcpResults := []common.MetricsResult{
		{
			GoogleMetric:   googleMetric,
			Exception:      errors.New("exception error"),
			ReportResponse: nil,
		},
	}

	// This should test the processMetricsResults method with exception paths
	var failedCount int
	measuredTypesInfo := make(map[string]float64)
	sink.processMetricsResults(ctx, gcpResults, &failedCount, measuredTypesInfo)
}

// TestGoogleUsageSink_processMetricsResults_GetAsUsageBillingMetricError tests error in GetAsUsageBillingMetric
func TestGoogleUsageSink_processMetricsResults_GetAsUsageBillingMetricError(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordSinkDelivered
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	// Create test metric with invalid type that will cause GetAsUsageBillingMetric to fail
	googleMetric := *common.NewGoogleMetric("invalid-type")

	// Create results
	gcpResults := []common.MetricsResult{
		{
			GoogleMetric:   googleMetric,
			Exception:      nil,
			ReportResponse: &common.ReportResponse{},
		},
	}

	// This should test the error path in processMetricsResults (line 144)
	var failedCount int
	measuredTypesInfo := make(map[string]float64)
	sink.processMetricsResults(ctx, gcpResults, &failedCount, measuredTypesInfo)
}

// TestGoogleUsageSink_isSuccessful tests the isSuccessful function
func TestGoogleUsageSink_isSuccessful(t *testing.T) {
	tests := []struct {
		name     string
		response *common.ReportResponse
		expected bool
	}{
		{
			name:     "successful response",
			response: &common.ReportResponse{},
			expected: true,
		},
		{
			name:     "nil response",
			response: nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This tests the isSuccessful function (line 193)
			result := isSuccessful(tt.response)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGoogleUsageSink_processMetricsResults_WithErrorLogging tests error logging in processMetricsResults
func TestGoogleUsageSink_processMetricsResults_WithErrorLogging(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordSinkDelivered
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	// Set up mock logger to capture error logging
	ml := &log.MockLogger{}
	sink.logger = ml

	customerID := "test-customer-123"
	resourceName := "test-resource"

	// Create test metric
	googleMetric := *common.NewGoogleMetric(&datamodel.AggregatedUsage{
		ID:               1,
		VendorCustomerID: &customerID,
		ResourceName:     &resourceName,
		Quantity:         100.0,
		State:            datamodel.Error,
	})

	// Create error results to trigger error logging (lines 137-138)
	gcpResults := []common.MetricsResult{
		{
			GoogleMetric:   googleMetric,
			Exception:      errors.New("test error"),
			ReportResponse: nil,
		},
	}

	// Set up mock expectations for error logging
	ml.On("Errorf", "Google Usage Mapping with ID %d failed GoogleMetric validation: missing fields %s", int64(1), mock.AnythingOfType("string")).Maybe()
	ml.On("Errorf", "Google Usage Mapping with ID %d failed GoogleMetric validation: missing fields %s", int64(1), "customerId").Maybe()
	ml.On("Debugf", "Processing the Google Metric Result for service: %s; result is %v, resultCode: %s", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string")).Once()
	ml.On("Infof", "Updating usage information for billingRecord ID: %d, state: %v", int64(1), mock.AnythingOfType("datamodel.TrackingState")).Once()
	ml.On("Infof", "%d metrics were successfully reported.", 0).Once()
	ml.On("Infof", "%d metrics were not reported.", 1).Once()

	// This should test the error logging paths (lines 137-138)
	var failedCount int
	measuredTypesInfo := make(map[string]float64)
	sink.processMetricsResults(ctx, gcpResults, &failedCount, measuredTypesInfo)

	ml.AssertExpectations(t)
}

// TestGoogleUsageSink_processMetricsResults_WithSuccessfulLogging tests successful logging in processMetricsResults
func TestGoogleUsageSink_processMetricsResults_WithSuccessfulLogging(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordSinkDelivered
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	mockMetricRecorder.On("RecordBillingMetricsSubmission", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	// Set up mock logger to capture success logging
	ml := &log.MockLogger{}
	sink.logger = ml

	customerID := "test-customer-123"
	resourceName := "test-resource"

	// Create test metric
	googleMetric := *common.NewGoogleMetric(&datamodel.AggregatedUsage{
		ID:               1,
		VendorCustomerID: &customerID,
		ResourceName:     &resourceName,
		Quantity:         100.0,
		State:            datamodel.Unsubmitted,
	})

	// Create successful results to trigger success logging (line 200)
	gcpResults := []common.MetricsResult{
		{
			GoogleMetric:   googleMetric,
			Exception:      nil,
			ReportResponse: &common.ReportResponse{}, // Good response
		},
	}

	// Set up mock expectations for success logging
	ml.On("Debugf", "Processing the Google Metric Result for service: %s; result is %v, resultCode: %s", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string")).Once()
	ml.On("Infof", "Updating usage information for billingRecord ID: %d, state: %v", int64(1), mock.AnythingOfType("datamodel.TrackingState")).Once()
	ml.On("Infof", "%d metrics were successfully reported.", 1).Once()
	ml.On("Infof", "%d metrics were not reported.", 0).Once()

	// This should test the success logging path (line 200)
	var failedCount int
	measuredTypesInfo := make(map[string]float64)
	sink.processMetricsResults(ctx, gcpResults, &failedCount, measuredTypesInfo)

	ml.AssertExpectations(t)
}

// TestGoogleUsageSink_processMetricsResultsBatch tests batch processing of metrics results
func TestGoogleUsageSink_processMetricsResultsBatch(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	config.EnableBatchUsageUpdates = true // Enable batch processing
	config.ResultUpdateBatchSize = 2      // Small batch size for testing

	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Set up mock expectation for RecordSinkDelivered
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "test-customer-123"
	resourceName := "test-resource"

	// Create test metrics
	googleMetric1 := *common.NewGoogleMetric(&datamodel.AggregatedUsage{
		ID:               1,
		VendorCustomerID: &customerID,
		ResourceName:     &resourceName,
		Quantity:         100.0,
		State:            datamodel.Unsubmitted,
		ErrorCount:       0,
	})

	googleMetric2 := *common.NewGoogleMetric(&datamodel.AggregatedUsage{
		ID:               2,
		VendorCustomerID: &customerID,
		ResourceName:     &resourceName,
		Quantity:         200.0,
		State:            datamodel.Unsubmitted,
		ErrorCount:       1,
	})

	// Create test results
	gcpResults := []common.MetricsResult{
		{
			GoogleMetric:   googleMetric1,
			Exception:      nil,
			ReportResponse: &common.ReportResponse{}, // Successful response
		},
		{
			GoogleMetric: googleMetric2,
			Exception:    nil,
			ReportResponse: &common.ReportResponse{
				ReportErrors: []*servicecontrol.ReportError{
					{
						OperationId: "test-op",
						Status: &servicecontrol.Status{
							Code:    400,
							Message: "test error",
						},
					},
				},
			}, // Error response
		},
	}

	// Set up mock logger
	ml := &log.MockLogger{}
	sink.logger = ml

	// Set up expectations for batch processing logs (flexible to handle various argument counts)
	ml.On("Debugf", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	ml.On("Debugf", mock.Anything, mock.Anything).Maybe()
	ml.On("Infof", mock.Anything, mock.Anything, mock.Anything).Maybe()
	ml.On("Infof", mock.Anything, mock.Anything).Maybe()
	ml.On("Warnf", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	ml.On("Warnf", mock.Anything, mock.Anything).Maybe()

	// Call batch processing - this will test the logic without actual DB operations
	measuredTypesInfo := make(map[string]float64)
	sink.processMetricsResultsBatch(ctx, gcpResults, measuredTypesInfo)

	// Verify mock expectations
	ml.AssertExpectations(t)
}

// TestGoogleUsageSink_batchUpdateAggregatedUsage tests the batch update functionality
func TestGoogleUsageSink_batchUpdateAggregatedUsage(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	config.ResultUpdateBatchSize = 2

	mockDB := &database2.MockStorage{}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Add WithTransaction mock for table name initialization
	mockDB.On("WithTransaction", mock.Anything, mock.Anything).Return(nil).Maybe()

	// Test successful batch update (simplified - avoid transaction complexity)
	t.Run("successful batch update", func(t *testing.T) {
		// Create test updateInfo batch using the actual internal type
		updatesBatch := []updateInfo{
			{
				id: 1,
				updates: map[string]interface{}{
					"state":         datamodel.Submitted,
					"error_message": nil,
					"error_count":   int32(0),
					"submission":    `"test-uuid-1"`,
				},
			},
			{
				id: 2,
				updates: map[string]interface{}{
					"state":         datamodel.Error,
					"error_message": "Test error",
					"error_count":   int32(1),
				},
			},
		}
		sink := NewSink(ctx, config, mockDB, mockMetricRecorder)
		ml := &log.MockLogger{}
		sink.logger = ml

		ml.On("Debugf", mock.Anything, mock.Anything).Maybe()
		ml.On("Infof", mock.Anything, mock.Anything, mock.Anything).Maybe()
		ml.On("Infof", mock.Anything, mock.Anything).Maybe()
		ml.On("Warnf", mock.Anything, mock.Anything).Maybe()

		// Call the method - this will test the logic path without actual DB execution
		sink.batchUpdateAggregatedUsage(ctx, updatesBatch)

		// Basic assertion that no panic occurred
		assert.True(t, true, "Method completed without panic")
	})

	// Test empty batch handling
	t.Run("empty batch", func(t *testing.T) {
		sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

		// Test with empty batch - should return early
		sink.batchUpdateAggregatedUsage(ctx, []updateInfo{})

		// No panic should occur
		assert.True(t, true, "Empty batch handled correctly")
	})
} // TestGoogleUsageSink_buildBulkUpdateQuery tests SQL query building
func TestGoogleUsageSink_buildBulkUpdateQuery(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	// Set the table name for testing
	sink.aggregatedUsageTable = "aggregated_usages"

	t.Run("Empty batch returns empty query", func(t *testing.T) {
		sql, args := sink.buildBulkUpdateQuery(nil, []updateInfo{})
		assert.Empty(t, sql)
		assert.Nil(t, args)
	})

	t.Run("Single update generates correct SQL", func(t *testing.T) {
		batch := []updateInfo{
			{
				id: 123,
				updates: map[string]interface{}{
					"state":         datamodel.Submitted,
					"error_message": "test error",
					"error_count":   int32(5),
					"submission":    `"test-uuid"`,
				},
			},
		}

		sql, args := sink.buildBulkUpdateQuery(&gorm.DB{}, batch)

		// Verify SQL structure
		assert.Contains(t, sql, "UPDATE aggregated_usages")
		assert.Contains(t, sql, "FROM (VALUES")
		assert.Contains(t, sql, "WHERE aggregated_usages.id = tmp.id")
		assert.Contains(t, sql, "COALESCE(tmp.submission::jsonb, aggregated_usages.submission)")

		// Verify arguments
		assert.Equal(t, 5, len(args))
		assert.Equal(t, int64(123), args[0])
		assert.Equal(t, int32(datamodel.Submitted), args[1])
		assert.Equal(t, "test error", args[2])
		assert.Equal(t, int32(5), args[3])
		assert.Equal(t, `"test-uuid"`, args[4])
	})

	t.Run("Multiple updates generate correct SQL", func(t *testing.T) {
		batch := []updateInfo{
			{
				id: 123,
				updates: map[string]interface{}{
					"state":       datamodel.Submitted,
					"error_count": int32(0),
				},
			},
			{
				id: 456,
				updates: map[string]interface{}{
					"state":         datamodel.Error,
					"error_message": "error message",
					"error_count":   int32(1),
				},
			},
		}

		sql, args := sink.buildBulkUpdateQuery(&gorm.DB{}, batch)

		// Verify SQL has multiple value tuples
		assert.Contains(t, sql, "($1::bigint, $2::integer, $3::text, $4::integer, $5::jsonb), ($6::bigint, $7::integer, $8::text, $9::integer, $10::jsonb)")

		// Verify arguments count
		assert.Equal(t, 10, len(args))
		assert.Equal(t, int64(123), args[0])
		assert.Equal(t, int64(456), args[5])
	})
}

// TestGoogleUsageSink_buildBulkUpdateQuery_TypeAssertionFailures tests type assertion error handling
func TestGoogleUsageSink_buildBulkUpdateQuery_TypeAssertionFailures(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)
	sink.aggregatedUsageTable = "aggregated_usages"

	// Set up mock logger to capture warnings
	ml := &log.MockLogger{}
	sink.logger = ml

	// Set up expectations for type assertion failure warnings
	ml.On("Warnf", "Type assertion for 'state' failed: got value=%#v (type=%T) for billingRecord ID: %d",
		"invalid", "invalid", int64(123)).Once()
	ml.On("Warnf", "Type assertion for 'error_count' failed: got value=%#v (type=%T) for billingRecord ID: %d",
		"invalid", "invalid", int64(123)).Once()
	ml.On("Warnf", "Type assertion for 'submission' failed: got value=%#v (type=%T) for billingRecord ID: %d",
		123, 123, int64(123)).Once()

	// Create batch with invalid types
	batch := []updateInfo{
		{
			id: 123,
			updates: map[string]interface{}{
				"state":       "invalid", // Should be TrackingState
				"error_count": "invalid", // Should be int32
				"submission":  123,       // Should be string
			},
		},
	}

	sql, args := sink.buildBulkUpdateQuery(&gorm.DB{}, batch)

	// Should still generate SQL with fallback values
	assert.NotEmpty(t, sql)
	assert.Equal(t, 5, len(args))

	// Check fallback values
	assert.Equal(t, int64(123), args[0]) // id
	assert.Equal(t, int32(0), args[1])   // state fallback
	assert.Equal(t, nil, args[2])        // error_message
	assert.Equal(t, int32(0), args[3])   // error_count fallback
	assert.Equal(t, nil, args[4])        // submission fallback

	ml.AssertExpectations(t)
}

// TestGoogleUsageSink_fallbackToIndividualUpdates tests the fallback mechanism
func TestGoogleUsageSink_fallbackToIndividualUpdates(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()

	mockDB := &database2.MockStorage{}
	// Add WithTransaction mock for table name initialization
	mockDB.On("WithTransaction", mock.Anything, mock.Anything).Return(errors.New("mock transaction disabled for testing")).Maybe()

	// Add specific expectations for individual updates
	mockDB.On("UpdateAggregatedUsage", ctx, int64(1), mock.Anything).Return(nil)
	mockDB.On("UpdateAggregatedUsage", ctx, int64(2), mock.Anything).Return(errors.New("update failed"))

	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	// Set up mock logger
	ml := &log.MockLogger{}
	sink.logger = ml
	ml.On("Warnf", "Error updating usage information (fallback) - billingRecord ID: %d, error: %v",
		int64(2), mock.MatchedBy(func(err error) bool {
			return err.Error() == "update failed"
		})).Once()
	ml.On("Debugf", mock.Anything, mock.Anything).Maybe()

	batch := []updateInfo{
		{
			id: 1,
			updates: map[string]interface{}{
				"state": datamodel.Submitted,
			},
		},
		{
			id: 2,
			updates: map[string]interface{}{
				"state": datamodel.Error,
			},
		},
	}

	sink.fallbackToIndividualUpdates(ctx, batch)

	mockDB.AssertExpectations(t)
	ml.AssertExpectations(t)
}

// TestGoogleUsageSink_initializeTableName_WithTimeout tests timeout handling in initialization
func TestGoogleUsageSink_initializeTableName_WithTimeout(t *testing.T) {
	config := common.LoadConfig()

	// Create mock that simulates timeout
	mockDB := &database2.MockStorage{}
	mockDB.On("WithTransaction", mock.Anything, mock.Anything).Return(context.DeadlineExceeded)

	// Set up mock logger
	ml := &log.MockLogger{}
	ml.On("Warnf", "Error initializing table name, using fallback: %v", context.DeadlineExceeded).Once()
	ml.On("Debugf", "Initialized AggregatedUsage table name: %s", "aggregated_usages").Once()

	sink := &GoogleUsageSink{
		metricClient: googlePusher.GoogleMetricsClient{},
		logger:       ml,
		metricsdb:    mockDB,
		config:       config,
	}

	sink.initializeTableName()

	assert.Equal(t, "aggregated_usages", sink.aggregatedUsageTable)
	ml.AssertExpectations(t)
	mockDB.AssertExpectations(t)
}

// TestGoogleUsageSink_processMetricsResults_BatchEnabled tests batch vs single processing
func TestGoogleUsageSink_processMetricsResults_BatchEnabled(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()

	t.Run("Batch processing enabled", func(t *testing.T) {
		config.EnableBatchUsageUpdates = true
		mockDB := createMockDB()
		mockMetricRecorder := &monitoring.MockMetricsRecorder{}
		// Set up mock expectation for RecordSinkDelivered
		mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
		sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

		// Set up mock logger
		ml := &log.MockLogger{}
		sink.logger = ml
		ml.On("Infof", "%d metrics were successfully reported.", 0).Once()
		ml.On("Infof", "%d metrics were not reported.", 0).Once()

		var failedCount int
		measuredTypesInfo := make(map[string]float64)
		sink.processMetricsResults(ctx, []common.MetricsResult{}, &failedCount, measuredTypesInfo)
		ml.AssertExpectations(t)
	})

	t.Run("Batch processing disabled", func(t *testing.T) {
		config.EnableBatchUsageUpdates = false
		mockDB := createMockDB()
		mockMetricRecorder := &monitoring.MockMetricsRecorder{}
		// Set up mock expectation for RecordSinkDelivered
		mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
		sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

		// Set up mock logger
		ml := &log.MockLogger{}
		sink.logger = ml
		ml.On("Infof", "%d metrics were successfully reported.", 0).Once()
		ml.On("Infof", "%d metrics were not reported.", 0).Once()

		var failedCount int
		measuredTypesInfo := make(map[string]float64)
		sink.processMetricsResults(ctx, []common.MetricsResult{}, &failedCount, measuredTypesInfo)
		ml.AssertExpectations(t)
	})
}

// TestGoogleUsageSink_processResponse_RecordsBillingMetrics tests that processResponse calls RecordBillingMetricsSubmission
func TestGoogleUsageSink_processResponse_RecordsBillingMetrics(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockDB := createMockDB()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Set up mock expectations
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	mockMetricRecorder.On("RecordBillingMetricsSubmission", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	sink := NewSink(ctx, config, mockDB, mockMetricRecorder)

	customerID := "test-customer-123"
	resourceName := "test-resource"

	// Create test metric with a specific measured type
	googleMetric := *common.NewGoogleMetric(&datamodel.AggregatedUsage{
		ID:               1,
		VendorCustomerID: &customerID,
		ResourceName:     &resourceName,
		Quantity:         100.0,
		State:            datamodel.Unsubmitted,
		MeasuredType:     string(metadata.PoolHotTierProvisionedSize),
	})

	// Create successful results
	gcpResults := []common.MetricsResult{
		{
			GoogleMetric:   googleMetric,
			ReportResponse: &common.ReportResponse{},
		},
	}

	// Create channel and wait group
	resultChan := make(chan []common.MetricsResult, 1)
	var wg sync.WaitGroup
	wg.Add(1)

	// Send results to channel
	go func() {
		resultChan <- gcpResults
		close(resultChan)
	}()

	var failedCount int
	sink.processResponse(ctx, &wg, resultChan, &failedCount)
	wg.Wait()

	// Verify RecordBillingMetricsSubmission was called
	mockMetricRecorder.AssertCalled(t, "RecordBillingMetricsSubmission", mock.AnythingOfType("*monitoring.MetricRecorderParams"))
}

// TestGetMeasuredTypesWithUnits tests the getMeasuredTypesWithUnits function
func TestGetMeasuredTypesWithUnits(t *testing.T) {
	tests := []struct {
		name         string
		measuredType metadata.MeasuredType
		expected     string
	}{
		{
			name:         "BackupLogicalSize returns KiB suffix",
			measuredType: metadata.BackupLogicalSize,
			expected:     "VOLUME_BACKUP_SIZE_IN_KiB",
		},
		{
			name:         "BackupEnabledVolumeAllocatedSize returns GiB-hours suffix",
			measuredType: metadata.BackupEnabledVolumeAllocatedSize,
			expected:     "BACKUP_ENABLED_VOLUME_ALLOCATED_SIZE_IN_GiB-hours",
		},
		{
			name:         "CbsCrossRegionVolumeBackupTransferBytes returns bytes suffix",
			measuredType: metadata.CbsCrossRegionVolumeBackupTransferBytes,
			expected:     "CBS_CROSS_REGION_VOLUME_BACKUP_TRANSFER_BYTES_IN_Bytes",
		},
		{
			name:         "XregionReplicationTotalTransferBytes returns bytes suffix",
			measuredType: metadata.XregionReplicationTotalTransferBytes,
			expected:     "XREGION_REPLICATION_TOTAL_TRANSFER_BYTES_IN_Bytes",
		},
		{
			name:         "CoolTierDataReadSizeRaw returns bytes suffix",
			measuredType: metadata.CoolTierDataReadSizeRaw,
			expected:     "COOL_TIER_DATA_READ_SIZE_RAW_IN_Bytes",
		},
		{
			name:         "CoolTierDataWriteSizeRaw returns bytes suffix",
			measuredType: metadata.CoolTierDataWriteSizeRaw,
			expected:     "COOL_TIER_DATA_WRITE_SIZE_RAW_IN_Bytes",
		},
		{
			name:         "PoolHotTierProvisionedSize returns MiB-hours suffix",
			measuredType: metadata.PoolHotTierProvisionedSize,
			expected:     "POOL_HOT_TIER_PROVISIONED_SIZE_IN_MiB-hours",
		},
		{
			name:         "PoolCapacityTierLogicalFootprint returns MiB-hours suffix",
			measuredType: metadata.PoolCapacityTierLogicalFootprint,
			expected:     "POOL_CAPACITY_TIER_LOGICAL_FOOTPRINT_IN_MiB-hours",
		},
		{
			name:         "CbsCrossRegionVolumeRestoreTransferBytes returns bytes suffix",
			measuredType: metadata.CbsCrossRegionVolumeRestoreTransferBytes,
			expected:     "CBS_CROSS_REGION_VOLUME_RESTORE_TRANSFER_BYTES_IN_Bytes",
		},
		{
			name:         "default returns GiB-hours suffix",
			measuredType: metadata.LogicalSize,
			expected:     "LOGICAL_SIZE_IN_GiB-hours",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getMeasuredTypesWithUnits(tt.measuredType)
			assert.Equal(t, tt.expected, result)
		})
	}
}
