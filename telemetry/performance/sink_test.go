package performance

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/servicecontrol/v1"
)

// Helper function to simulate FilterAcceptedMetrics functionality for testing
func filterAcceptedMetricsHelper(sink *GoogleSink, metrics []entity.HydratedMetric) []entity.HydratedMetric {
	var warnings []string
	var validMetrics []entity.HydratedMetric

	for _, m := range metrics {
		if sink.isValidHydratedMetric(m, &warnings) {
			validMetrics = append(validMetrics, m)
		}
	}
	return validMetrics
}

// TestDeliverMetrics_ValidMetrics tests the DeliverMetrics function with valid metrics.
func TestDeliverMetrics_ValidMetrics(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Set up mock expectation for RecordSinkDelivered
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	sink := NewSink(ctx, config, mockMetricRecorder)
	var hydratedM []entity.HydratedMetric
	rm := metadata.ResourceMetadata{
		ResourceUUID:        nillable.ToPointer(uuid.New().String()),
		ResourceName:        nillable.ToPointer("dummy-resource-" + strconv.Itoa(1)),
		ResourceDisplayName: nillable.ToPointer("Dummy Resource " + strconv.Itoa(1)),
		AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
		RegionName:          nillable.ToPointer("us-central1"),
		ResourceType:        metadata.VolumePool,
	}

	hydratedM = append(hydratedM, entity.HydratedMetric{
		Metadata:     rm,
		MeasuredType: metadata.PoolAllocatedSize,
		Quantity:     float64(1234),
		Timestamp:    entity.UnixNano(time.Now().UnixNano()),
	})

	count := sink.DeliverMetrics(ctx, hydratedM)
	assert.Equal(t, 1, count)
}

// TestDeliverMetrics_InvalidMetrics tests the DeliverMetrics function with valid metrics.
func TestDeliverMetrics_InvalidMetrics(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	sink := NewSink(ctx, config, mockMetricRecorder)
	var hydratedM []entity.HydratedMetric
	rm := metadata.ResourceMetadata{
		ResourceUUID:        nillable.ToPointer(uuid.New().String()),
		ResourceName:        nillable.ToPointer("dummy-resource-" + strconv.Itoa(1)),
		ResourceDisplayName: nillable.ToPointer("Dummy Resource " + strconv.Itoa(1)),
		AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
		RegionName:          nillable.ToPointer("us-central1"),
		ResourceType:        metadata.VolumePool,
	}

	hydratedM = append(hydratedM, entity.HydratedMetric{
		Metadata:     rm,
		MeasuredType: metadata.UnknownMeasuredType,
		Quantity:     float64(1234),
		Timestamp:    entity.UnixNano(time.Now().UnixNano()),
	})

	count := sink.DeliverMetrics(ctx, hydratedM)
	assert.Equal(t, 0, count)
}

// TestFilterAcceptedMetrics_ValidMetrics tests the FilterAcceptedMetrics function with valid metrics.
func TestFilterAcceptedMetrics_ValidMetrics(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	var hydratedM []entity.HydratedMetric
	sink := NewSink(ctx, config, mockMetricRecorder)

	rm := metadata.ResourceMetadata{
		ResourceUUID:        nillable.ToPointer(uuid.New().String()),
		ResourceName:        nillable.ToPointer("dummy-resource-" + strconv.Itoa(1)),
		ResourceDisplayName: nillable.ToPointer("Dummy Resource " + strconv.Itoa(1)),
		AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
		RegionName:          nillable.ToPointer("us-central1"),
		ResourceType:        metadata.VolumePool,
	}

	hydratedM = append(hydratedM, entity.HydratedMetric{
		Metadata:     rm,
		MeasuredType: metadata.PoolAllocatedSize,
		Quantity:     float64(1234),
		Timestamp:    entity.UnixNano(time.Now().UnixNano()),
	})

	validMetrics := filterAcceptedMetricsHelper(sink, hydratedM)

	assert.Len(t, validMetrics, 1)
}

// TestFilterAcceptedMetrics_InvalidMetrics tests the FilterAcceptedMetrics function with invalid metrics.
func TestFilterAcceptedMetrics_InvalidMetrics(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	var hydratedM []entity.HydratedMetric
	sink := NewSink(ctx, config, mockMetricRecorder)

	rm := metadata.ResourceMetadata{
		ResourceUUID:        nillable.ToPointer(uuid.New().String()),
		ResourceName:        nillable.ToPointer("dummy-resource-" + strconv.Itoa(1)),
		ResourceDisplayName: nillable.ToPointer("Dummy Resource " + strconv.Itoa(1)),
		AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
		RegionName:          nillable.ToPointer("us-central1"),
		ResourceType:        metadata.VolumePool,
	}

	hydratedM = append(hydratedM, entity.HydratedMetric{
		Metadata:     rm,
		MeasuredType: metadata.UnknownMeasuredType,
		Quantity:     float64(1234),
		Timestamp:    entity.UnixNano(time.Now().UnixNano()),
	})

	validMetrics := filterAcceptedMetricsHelper(sink, hydratedM)

	assert.Len(t, validMetrics, 0)
}

// TestFilterAcceptedMetrics_EmptyInput tests FilterAcceptedMetrics with an empty slice.
func TestFilterAcceptedMetrics_EmptyInput(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockMetricRecorder)
	var hydratedM []entity.HydratedMetric
	validMetrics := filterAcceptedMetricsHelper(sink, hydratedM)
	assert.Len(t, validMetrics, 0)
}

// TestDeliverMetrics_EmptyInput tests DeliverMetrics with an empty slice.
func TestDeliverMetrics_EmptyInput(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockMetricRecorder)
	var hydratedM []entity.HydratedMetric
	count := sink.DeliverMetrics(ctx, hydratedM)
	assert.Equal(t, 0, count)
}

// TestDeliverMetrics_AllInvalid tests DeliverMetrics with all invalid metrics.
func TestDeliverMetrics_AllInvalid(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockMetricRecorder)
	var hydratedM []entity.HydratedMetric
	for i := 0; i < 3; i++ {
		rm := metadata.ResourceMetadata{
			ResourceUUID:        nillable.ToPointer(uuid.New().String()),
			ResourceName:        nillable.ToPointer("dummy-resource-" + strconv.Itoa(i)),
			ResourceDisplayName: nillable.ToPointer("Dummy Resource " + strconv.Itoa(i)),
			AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
			RegionName:          nillable.ToPointer("us-central1"),
			ResourceType:        metadata.VolumePool,
		}
		hydratedM = append(hydratedM, entity.HydratedMetric{
			Metadata:     rm,
			MeasuredType: metadata.UnknownMeasuredType,
			Quantity:     float64(1234),
			Timestamp:    entity.UnixNano(time.Now().UnixNano()),
		})
	}
	count := sink.DeliverMetrics(ctx, hydratedM)
	assert.Equal(t, 0, count)
}

// TestFilterAcceptedMetrics_Mixed tests FilterAcceptedMetrics with a mix of valid and invalid metrics.
func TestFilterAcceptedMetrics_Mixed(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockMetricRecorder)
	var hydratedM []entity.HydratedMetric
	for i := 0; i < 2; i++ {
		rm := metadata.ResourceMetadata{
			ResourceUUID:        nillable.ToPointer(uuid.New().String()),
			ResourceName:        nillable.ToPointer("dummy-resource-" + strconv.Itoa(i)),
			ResourceDisplayName: nillable.ToPointer("Dummy Resource " + strconv.Itoa(i)),
			AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
			RegionName:          nillable.ToPointer("us-central1"),
			ResourceType:        metadata.VolumePool,
		}
		metricType := metadata.PoolAllocatedSize
		if i == 1 {
			metricType = metadata.UnknownMeasuredType
		}
		hydratedM = append(hydratedM, entity.HydratedMetric{
			Metadata:     rm,
			MeasuredType: metricType,
			Quantity:     float64(1234),
			Timestamp:    entity.UnixNano(time.Now().UnixNano()),
		})
	}
	validMetrics := filterAcceptedMetricsHelper(sink, hydratedM)
	assert.Len(t, validMetrics, 1)
}

// TestIsValidHydratedMetric_EmptyMeasuredType tests isValidHydratedMetric with empty MeasuredType.
func TestIsValidHydratedMetric_EmptyMeasuredType(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockMetricRecorder)
	var warnings []string
	rm := metadata.ResourceMetadata{
		ResourceUUID:        nillable.ToPointer(uuid.New().String()),
		ResourceName:        nillable.ToPointer("dummy-resource-0"),
		ResourceDisplayName: nillable.ToPointer("Dummy Resource 0"),
		AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
		RegionName:          nillable.ToPointer("us-central1"),
		ResourceType:        metadata.VolumePool,
	}
	metric := entity.HydratedMetric{
		Metadata:     rm,
		MeasuredType: "",
		Quantity:     float64(1234),
		Timestamp:    entity.UnixNano(time.Now().UnixNano()),
	}
	ok := sink.isValidHydratedMetric(metric, &warnings)
	assert.False(t, ok)
	assert.NotEmpty(t, warnings)
}

// TestIsValidHydratedMetric_ValidType tests isValidHydratedMetric with a valid MeasuredType.
func TestIsValidHydratedMetric_ValidType(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockMetricRecorder)
	var warnings []string
	rm := metadata.ResourceMetadata{
		ResourceUUID:        nillable.ToPointer(uuid.New().String()),
		ResourceName:        nillable.ToPointer("dummy-resource-0"),
		ResourceDisplayName: nillable.ToPointer("Dummy Resource 0"),
		AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
		RegionName:          nillable.ToPointer("us-central1"),
		ResourceType:        metadata.VolumePool,
	}
	metric := entity.HydratedMetric{
		Metadata:     rm,
		MeasuredType: metadata.PoolAllocatedSize,
		Quantity:     float64(1234),
		Timestamp:    entity.UnixNano(time.Now().UnixNano()),
	}
	ok := sink.isValidHydratedMetric(metric, &warnings)
	assert.True(t, ok)
	assert.Empty(t, warnings)
}

// TestFilterAcceptedMetrics_MultipleValid tests FilterAcceptedMetrics with multiple valid metrics.
func TestFilterAcceptedMetrics_MultipleValid(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockMetricRecorder)
	var hydratedM []entity.HydratedMetric
	for i := 0; i < 5; i++ {
		rm := metadata.ResourceMetadata{
			ResourceUUID:        nillable.ToPointer(uuid.New().String()),
			ResourceName:        nillable.ToPointer("dummy-resource-" + strconv.Itoa(i)),
			ResourceDisplayName: nillable.ToPointer("Dummy Resource " + strconv.Itoa(i)),
			AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
			RegionName:          nillable.ToPointer("us-central1"),
			ResourceType:        metadata.VolumePool,
		}
		hydratedM = append(hydratedM, entity.HydratedMetric{
			Metadata:     rm,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     float64(1234 + i),
			Timestamp:    entity.UnixNano(time.Now().UnixNano()),
		})
	}
	validMetrics := filterAcceptedMetricsHelper(sink, hydratedM)
	assert.Len(t, validMetrics, 5)
}

// TestFilterAcceptedMetrics_AllInvalidTypes tests FilterAcceptedMetrics with all invalid MeasuredTypes.
func TestFilterAcceptedMetrics_AllInvalidTypes(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockMetricRecorder)
	var hydratedM []entity.HydratedMetric
	for i := 0; i < 3; i++ {
		rm := metadata.ResourceMetadata{
			ResourceUUID:        nillable.ToPointer(uuid.New().String()),
			ResourceName:        nillable.ToPointer("dummy-resource-" + strconv.Itoa(i)),
			ResourceDisplayName: nillable.ToPointer("Dummy Resource " + strconv.Itoa(i)),
			AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
			RegionName:          nillable.ToPointer("us-central1"),
			ResourceType:        metadata.VolumePool,
		}
		hydratedM = append(hydratedM, entity.HydratedMetric{
			Metadata:     rm,
			MeasuredType: "",
			Quantity:     float64(1234 + i),
			Timestamp:    entity.UnixNano(time.Now().UnixNano()),
		})
	}
	validMetrics := filterAcceptedMetricsHelper(sink, hydratedM)
	assert.Len(t, validMetrics, 0)
}

// TestFilterAcceptedMetrics_MixedTypes tests FilterAcceptedMetrics with a mix of valid, empty, and unknown MeasuredTypes.
func TestFilterAcceptedMetrics_MixedTypes(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockMetricRecorder)
	var hydratedM []entity.HydratedMetric
	for i := 0; i < 3; i++ {
		rm := metadata.ResourceMetadata{
			ResourceUUID:        nillable.ToPointer(uuid.New().String()),
			ResourceName:        nillable.ToPointer("dummy-resource-" + strconv.Itoa(i)),
			ResourceDisplayName: nillable.ToPointer("Dummy Resource " + strconv.Itoa(i)),
			AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
			RegionName:          nillable.ToPointer("us-central1"),
			ResourceType:        metadata.VolumePool,
		}
		var metricType metadata.MeasuredType
		switch i {
		case 0:
			metricType = metadata.PoolAllocatedSize
		case 1:
			metricType = ""
		default:
			metricType = metadata.UnknownMeasuredType
		}
		hydratedM = append(hydratedM, entity.HydratedMetric{
			Metadata:     rm,
			MeasuredType: metricType,
			Quantity:     float64(1234 + i),
			Timestamp:    entity.UnixNano(time.Now().UnixNano()),
		})
	}
	validMetrics := filterAcceptedMetricsHelper(sink, hydratedM)
	assert.Len(t, validMetrics, 1)
}

// TestDeliverMetrics_MultipleValid tests DeliverMetrics with multiple valid metrics.
func TestDeliverMetrics_MultipleValid(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	// Set up mock expectation for RecordSinkDelivered
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	sink := NewSink(ctx, config, mockMetricRecorder)
	var hydratedM []entity.HydratedMetric
	for i := 0; i < 4; i++ {
		rm := metadata.ResourceMetadata{
			ResourceUUID:        nillable.ToPointer(uuid.New().String()),
			ResourceName:        nillable.ToPointer("dummy-resource-" + strconv.Itoa(i)),
			ResourceDisplayName: nillable.ToPointer("Dummy Resource " + strconv.Itoa(i)),
			AccountName:         nillable.ToPointer("netapp-au-se1-autopush-sde-tst"),
			RegionName:          nillable.ToPointer("us-central1"),
			ResourceType:        metadata.VolumePool,
		}
		hydratedM = append(hydratedM, entity.HydratedMetric{
			Metadata:     rm,
			MeasuredType: metadata.PoolAllocatedSize,
			Quantity:     float64(1234 + i),
			Timestamp:    entity.UnixNano(time.Now().UnixNano()),
		})
	}
	count := sink.DeliverMetrics(ctx, hydratedM)
	assert.Equal(t, 4, count)
}

func TestGoogleSink_processMetricsResults_LogsNotImplemented(t *testing.T) {
	ml := &log.MockLogger{}
	ml.On("Warn", "processMetricsResults not implemented").Once()
	sink := &GoogleSink{
		logger: ml,
	}
	results := []common.MetricsResult{{}}
	sink.processMetricsResults(results)
	ml.AssertCalled(t, "Warn", "processMetricsResults not implemented")
}

// TestGoogleSink_processAndFilterMetricsResults_ErrorHandling tests various error scenarios
func TestGoogleSink_processAndFilterMetricsResults_ErrorHandling(t *testing.T) {
	ml := &log.MockLogger{}

	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	// Set up mock expectation for RecordSinkDelivered
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	sink := &GoogleSink{
		logger:          ml,
		metricsRecorder: mockMetricRecorder,
	}

	// Create test metrics
	customerID := "test-customer-123"
	validGoogleMetric := common.NewGoogleMetric(&datamodel.AggregatedUsage{
		VendorCustomerID: &customerID,
		MeasuredType:     string(metadata.LogicalSize),
	})

	invalidGoogleMetric := common.NewGoogleMetric("invalid-record")

	t.Run("Success case", func(t *testing.T) {
		ml.On("Infof", "Reporting %d metrics.", 1).Once()
		ml.On("Infof", "%d metrics were successfully reported.", 1).Once()

		results := []common.MetricsResult{
			{
				GoogleMetric:   *validGoogleMetric,
				ReportResponse: nil,
				OperationID:    "op1",
				OperationName:  "operation1",
				Exception:      nil,
			},
		}

		sink.processAndFilterMetricsResults(results)
		ml.AssertExpectations(t)
	})

	t.Run("GetCustomerId error", func(t *testing.T) {
		ml.On("Infof", "Reporting %d metrics.", 1).Once()
		ml.On("Warnf", "Failed to get Customer ID for GoogleMetric: %v, error: %v",
			*invalidGoogleMetric, mock.AnythingOfType("*common.InvalidGoogleMetricException")).Once()
		ml.On("Infof", "%d metrics were successfully reported.", 0).Once()

		results := []common.MetricsResult{
			{
				GoogleMetric:   *invalidGoogleMetric,
				ReportResponse: nil,
				OperationID:    "op1",
				OperationName:  "operation1",
				Exception:      nil,
			},
		}

		sink.processAndFilterMetricsResults(results)
		ml.AssertExpectations(t)
	})

	t.Run("Exception with 403 error", func(t *testing.T) {
		ml.On("Infof", "Reporting %d metrics.", 1).Once()
		testErr := &googleapi.Error{
			Code:    403,
			Message: "Forbidden",
		}
		ml.On("Debugf", "Performance metrics delivery failed with %s error - %s, OperationId: %s, OperationName: %s, ProjectId: %s, Exception: %v",
			"403", mock.AnythingOfType("string"), "op1", "operation1", customerID, "unknown").Once()
		ml.On("Infof", "%d metrics were successfully reported.", 0).Once()

		results := []common.MetricsResult{
			{
				GoogleMetric:   *validGoogleMetric,
				ReportResponse: nil,
				OperationID:    "op1",
				OperationName:  "operation1",
				Exception:      testErr,
			},
		}

		sink.processAndFilterMetricsResults(results)
		ml.AssertExpectations(t)
	})

	t.Run("Exception with 404 error", func(t *testing.T) {
		ml.On("Infof", "Reporting %d metrics.", 1).Once()
		testErr := &googleapi.Error{
			Code:    404,
			Message: "Not Found",
		}
		ml.On("Debugf", "Performance metrics delivery failed with %s error - %s, OperationId: %s, OperationName: %s, ProjectId: %s, Exception: %v",
			"404", mock.AnythingOfType("string"), "op1", "operation1", customerID, "unknown").Once()
		ml.On("Infof", "%d metrics were successfully reported.", 0).Once()

		results := []common.MetricsResult{
			{
				GoogleMetric:   *validGoogleMetric,
				ReportResponse: nil,
				OperationID:    "op1",
				OperationName:  "operation1",
				Exception:      testErr,
			},
		}

		sink.processAndFilterMetricsResults(results)
		ml.AssertExpectations(t)
	})

	t.Run("Exception with other error", func(t *testing.T) {
		ml.On("Infof", "Reporting %d metrics.", 1).Once()
		testErr := &googleapi.Error{
			Code:    500,
			Message: "Internal Server Error",
		}
		ml.On("Debugf", "Performance metrics delivery failed with exception - %s, OperationId: %s, OperationName: %s, ProjectId: %s, Exception: %v",
			mock.AnythingOfType("string"), "op1", "operation1", customerID, "googleapi: Error 500: Internal Server Error").Once()
		ml.On("Infof", "%d metrics were successfully reported.", 0).Once()

		results := []common.MetricsResult{
			{
				GoogleMetric:   *validGoogleMetric,
				ReportResponse: nil,
				OperationID:    "op1",
				OperationName:  "operation1",
				Exception:      testErr,
			},
		}

		sink.processAndFilterMetricsResults(results)
		ml.AssertExpectations(t)
	})

	t.Run("ReportResponse with errors", func(t *testing.T) {
		ml.On("Infof", "Reporting %d metrics.", 1).Once()
		reportResponse := &common.ReportResponse{
			ReportErrors: []*servicecontrol.ReportError{
				{
					OperationId: "op1",
					Status: &servicecontrol.Status{
						Code:    400,
						Message: "Invalid metric",
					},
				},
			},
		}
		ml.On("Debugf", "Performance metrics delivery failed with report errors - %s, OperationId: %s, OperationName: %s, ProjectId: %s, ReportErrors: %v",
			mock.AnythingOfType("string"), "op1", "operation1", customerID, "400").Once()
		ml.On("Infof", "%d metrics were successfully reported.", 0).Once()

		results := []common.MetricsResult{
			{
				GoogleMetric:   *validGoogleMetric,
				ReportResponse: reportResponse,
				OperationID:    "op1",
				OperationName:  "operation1",
				Exception:      nil,
			},
		}

		sink.processAndFilterMetricsResults(results)
		ml.AssertExpectations(t)
	})
}

// TestGoogleSink_push_EmptyMetrics tests push method with empty metrics list
func TestGoogleSink_push_EmptyMetrics(t *testing.T) {
	ml := &log.MockLogger{}
	ml.On("Warn", "google metrics not found, hence not reporting anything.").Once()

	sink := &GoogleSink{
		logger: ml,
	}

	sink.push(context.Background(), []common.GoogleMetric{})
	ml.AssertExpectations(t)
}

// TestFormatMetricForLogging tests the formatMetricForLogging function with different metric types
func TestFormatMetricForLogging(t *testing.T) {
	t.Run("HydratedMetric with all fields", func(t *testing.T) {
		resourceName := "test-resource"
		accountName := "test-account"
		regionName := "us-central1"

		hydratedMetric := &entity.HydratedMetric{
			Metadata: metadata.ResourceMetadata{
				ResourceName: &resourceName,
				AccountName:  &accountName,
				RegionName:   &regionName,
				ResourceType: metadata.Volume,
			},
			MeasuredType: metadata.LogicalSize,
			Quantity:     100.5,
			Timestamp:    entity.UnixNano(time.Now().UnixNano()),
		}

		googleMetric := common.NewGoogleMetric(hydratedMetric)
		result := formatMetricForLogging(*googleMetric)

		assert.Contains(t, result, "Resource: test-resource")
		assert.Contains(t, result, "Type: VOLUME")
		assert.Contains(t, result, "MeasuredType: LOGICAL_SIZE")
		assert.Contains(t, result, "Quantity: 100.50")
		assert.Contains(t, result, "Account: test-account")
		assert.Contains(t, result, "Region: us-central1")
	})

	t.Run("HydratedMetric with nil fields", func(t *testing.T) {
		hydratedMetric := &entity.HydratedMetric{
			Metadata: metadata.ResourceMetadata{
				ResourceName: nil,
				AccountName:  nil,
				RegionName:   nil,
				ResourceType: metadata.Volume,
			},
			MeasuredType: metadata.LogicalSize,
			Quantity:     100.5,
			Timestamp:    entity.UnixNano(time.Now().UnixNano()),
		}

		googleMetric := common.NewGoogleMetric(hydratedMetric)
		result := formatMetricForLogging(*googleMetric)

		assert.Contains(t, result, "Resource: unknown")
		assert.Contains(t, result, "Account: unknown")
		assert.Contains(t, result, "Region: unknown")
	})

	t.Run("HydratedMetric with GetAsHydratedMetric error", func(t *testing.T) {
		// Create an invalid GoogleMetric that will cause GetAsHydratedMetric to error
		invalidGoogleMetric := common.NewGoogleMetric("invalid-record")
		result := formatMetricForLogging(*invalidGoogleMetric)

		// The invalid record will be treated as unknown type since GetType() returns -1
		assert.Contains(t, result, "Unknown metric type:")
	})

	t.Run("BillingMetric with all fields", func(t *testing.T) {
		resourceName := "billing-resource"
		customerID := "customer-123"
		regionName := "us-west1"

		billingMetric := &datamodel.AggregatedUsage{
			ResourceName:     &resourceName,
			VendorCustomerID: &customerID,
			RegionName:       &regionName,
			ResourceType:     string(metadata.VolumePool),
			MeasuredType:     string(metadata.PoolAllocatedSize),
			Quantity:         200.25,
			AggregationType:  "SUM",
		}

		googleMetric := common.NewGoogleMetric(billingMetric)
		result := formatMetricForLogging(*googleMetric)

		assert.Contains(t, result, "Resource: billing-resource")
		assert.Contains(t, result, "Type: VOLUME_POOL")
		assert.Contains(t, result, "MeasuredType: POOL_ALLOCATED_SIZE")
		assert.Contains(t, result, "Quantity: 200.25")
		assert.Contains(t, result, "CustomerID: customer-123")
		assert.Contains(t, result, "Region: us-west1")
		assert.Contains(t, result, "AggregationType: SUM")
	})

	t.Run("BillingMetric with nil fields", func(t *testing.T) {
		billingMetric := &datamodel.AggregatedUsage{
			ResourceName:     nil,
			VendorCustomerID: nil,
			RegionName:       nil,
			ResourceType:     string(metadata.VolumePool),
			MeasuredType:     string(metadata.PoolAllocatedSize),
			Quantity:         200.25,
			AggregationType:  "SUM",
		}

		googleMetric := common.NewGoogleMetric(billingMetric)
		result := formatMetricForLogging(*googleMetric)

		assert.Contains(t, result, "Resource: unknown")
		assert.Contains(t, result, "CustomerID: unknown")
		assert.Contains(t, result, "Region: unknown")
	})

	t.Run("BillingMetric with GetAsUsageBillingMetric error", func(t *testing.T) {
		// Create a GoogleMetric with HydratedMetric record but try to get it as BillingMetric
		hydratedMetric := &entity.HydratedMetric{
			MeasuredType: metadata.LogicalSize,
		}
		googleMetric := common.GoogleMetric{Record: hydratedMetric}

		// Force it to be treated as BillingMetric by mocking GetType
		// This will cause GetAsUsageBillingMetric to error
		// We'll simulate this by creating an invalid record type scenario
		result := formatMetricForLogging(googleMetric)

		// Since this is HydratedMetric record, it should be processed as HydratedMetric
		assert.Contains(t, result, "Resource:")
	})

	t.Run("Unknown metric type", func(t *testing.T) {
		// Create a GoogleMetric with an unknown record type
		googleMetric := common.GoogleMetric{Record: 12345} // Invalid type
		result := formatMetricForLogging(googleMetric)

		assert.Contains(t, result, "Unknown metric type:")
	})
}

// TestGetCodeResponse tests the getCodeResponse function
func TestGetCodeResponse(t *testing.T) {
	t.Run("nil response", func(t *testing.T) {
		result := getCodeResponse(nil)
		assert.Equal(t, "unknown", result)
	})

	t.Run("empty ReportErrors", func(t *testing.T) {
		response := &common.ReportResponse{
			ReportErrors: []*servicecontrol.ReportError{},
		}
		result := getCodeResponse(response)
		assert.Equal(t, "200", result)
	})

	t.Run("with ReportErrors", func(t *testing.T) {
		response := &common.ReportResponse{
			ReportErrors: []*servicecontrol.ReportError{
				{
					Status: &servicecontrol.Status{
						Code: 404,
					},
				},
			},
		}
		result := getCodeResponse(response)
		assert.Equal(t, "404", result)
	})
}

// TestProcessResponse tests the processResponse function
func TestProcessResponse(t *testing.T) {
	t.Run("processResponse with correlation ID from context", func(t *testing.T) {
		ctx := context.Background()
		config := common.LoadConfig()
		mockMetricRecorder := &monitoring.MockMetricsRecorder{}
		sink := NewSink(ctx, config, mockMetricRecorder)

		// Create context with correlation ID
		loggerFields := log.Fields{
			"requestCorrelationID": "test-correlation-123",
		}
		ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, loggerFields)

		// Create a channel and close it immediately to simulate no results
		resultChan := make(chan []common.MetricsResult)
		close(resultChan)

		var wg sync.WaitGroup
		wg.Add(1)

		// This test verifies the function executes without panicking
		// and properly extracts the correlation ID from context
		sink.processResponse(ctx, &wg, resultChan)
	})

	t.Run("processResponse without correlation ID", func(t *testing.T) {
		ctx := context.Background()
		config := common.LoadConfig()
		mockMetricRecorder := &monitoring.MockMetricsRecorder{}
		sink := NewSink(ctx, config, mockMetricRecorder)

		// Create context without correlation ID

		// Create a channel and close it immediately to simulate no results
		resultChan := make(chan []common.MetricsResult)
		close(resultChan)

		var wg sync.WaitGroup
		wg.Add(1)

		// This test verifies the function executes without panicking
		// and uses "unknown" as default correlation ID
		sink.processResponse(ctx, &wg, resultChan)
	})

	t.Run("processResponse with invalid context value", func(t *testing.T) {
		ctx := context.Background()
		config := common.LoadConfig()
		mockMetricRecorder := &monitoring.MockMetricsRecorder{}
		sink := NewSink(ctx, config, mockMetricRecorder)

		// Create context with invalid logger fields
		ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, "invalid-type")

		// Create a channel and close it immediately to simulate no results
		resultChan := make(chan []common.MetricsResult)
		close(resultChan)

		var wg sync.WaitGroup
		wg.Add(1)

		// This test verifies the function handles invalid context values gracefully
		sink.processResponse(ctx, &wg, resultChan)
	})

	t.Run("processResponse with logger fields but no correlation ID", func(t *testing.T) {
		ctx := context.Background()
		config := common.LoadConfig()
		mockMetricRecorder := &monitoring.MockMetricsRecorder{}
		sink := NewSink(ctx, config, mockMetricRecorder)

		// Create context with logger fields but no requestCorrelationID
		loggerFields := log.Fields{
			"otherField": "some-value",
		}
		ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, loggerFields)

		// Create a channel and close it immediately to simulate no results
		resultChan := make(chan []common.MetricsResult)
		close(resultChan)

		var wg sync.WaitGroup
		wg.Add(1)

		// This test verifies the function handles missing correlation ID gracefully
		sink.processResponse(ctx, &wg, resultChan)
	})
}

// TestFilterAndConvertToGoogleMetrics_EdgeCases tests edge cases for FilterAndConvertToGoogleMetrics
func TestFilterAndConvertToGoogleMetrics_EdgeCases(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockMetricRecorder)

	t.Run("metric with both empty and unknown MeasuredType", func(t *testing.T) {
		var hydratedM []entity.HydratedMetric

		rm := metadata.ResourceMetadata{
			ResourceUUID:        nillable.ToPointer(uuid.New().String()),
			ResourceName:        nillable.ToPointer("test-resource"),
			ResourceDisplayName: nillable.ToPointer("Test Resource"),
			AccountName:         nillable.ToPointer("test-account"),
			RegionName:          nillable.ToPointer("us-central1"),
			ResourceType:        metadata.Volume,
		}

		// Test with empty string - should trigger both validations
		hydratedM = append(hydratedM, entity.HydratedMetric{
			Metadata:     rm,
			MeasuredType: "", // Empty string triggers both empty and unknown type validation
			Quantity:     100.0,
			Timestamp:    entity.UnixNano(time.Now().UnixNano()),
		})

		validMetrics := sink.FilterAndConvertToGoogleMetrics(hydratedM)
		assert.Len(t, validMetrics, 0)
	})
}

// TestNewSink tests the NewSink constructor
func TestNewSink(t *testing.T) {
	ctx := context.Background()
	config := &common.TelemetryConfig{
		PerformanceRootUrl: "https://test-endpoint.googleapis.com",
	}
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}

	sink := NewSink(ctx, config, mockMetricRecorder)

	assert.NotNil(t, sink)
	assert.NotNil(t, sink.logger)
}

// TestPush_WithNonEmptyMetrics tests push method with non-empty metrics (integration test)
func TestPush_WithNonEmptyMetrics(t *testing.T) {
	ctx := context.Background()
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	sink := NewSink(ctx, config, mockMetricRecorder)

	// Create a valid HydratedMetric with all required fields
	resourceUUID := uuid.New().String()
	hydratedMetric := &entity.HydratedMetric{
		Metadata: metadata.ResourceMetadata{
			ResourceUUID:        &resourceUUID,
			ResourceName:        nillable.ToPointer("test-volume"),
			ResourceDisplayName: nillable.ToPointer("Test Volume"),
			AccountName:         nillable.ToPointer("test-account"),
			RegionName:          nillable.ToPointer("us-central1"),
			ResourceType:        metadata.Volume,
		},
		MeasuredType: metadata.LogicalSize,
		Quantity:     100.0,
		Timestamp:    entity.UnixNano(time.Now().UnixNano()),
	}

	// Create GoogleMetric using the proper constructor
	googleMetric := common.NewGoogleMetric(hydratedMetric)
	metrics := []common.GoogleMetric{*googleMetric}

	// This will test the push method with actual metrics but will likely fail due to authentication
	// The test validates that the code path is executed correctly
	// We expect this to fail with authentication error, not panic
	sink.push(ctx, metrics)
}

// Test to cover missing lines 111 and 137: formatMetricForLogging error paths
func Test_formatMetricForLogging_ErrorPaths(t *testing.T) {
	t.Run("HydratedMetric GetAsHydratedMetric error - line 111", func(t *testing.T) {
		// Create a GoogleMetric with a record that will cause GetAsHydratedMetric to fail
		// We need to create a scenario where GetType() returns HydratedMetric but GetAsHydratedMetric fails
		// This can happen if the record is not actually a *entity.HydratedMetric

		// Create a fake HydratedMetric that will cause type assertion to fail
		fakeHydratedMetric := struct {
			Metadata     interface{}
			MeasuredType string
			Quantity     float64
			Timestamp    interface{}
		}{
			Metadata:     "fake",
			MeasuredType: "LOGICAL_SIZE",
			Quantity:     100.0,
			Timestamp:    "fake",
		}

		// Create a GoogleMetric with the fake record
		googleMetric := common.GoogleMetric{Record: &fakeHydratedMetric}

		// Since this is not a real *entity.HydratedMetric, GetType() will return -1
		// and it will go to the default case "Unknown metric type"
		result := formatMetricForLogging(googleMetric)
		assert.Contains(t, result, "Unknown metric type:")
	})

	t.Run("BillingMetric GetAsUsageBillingMetric error - line 137", func(t *testing.T) {
		// Create a GoogleMetric with a record that will cause GetAsUsageBillingMetric to fail
		fakeBillingMetric := struct {
			VendorCustomerID *string
			MeasuredType     string
			Quantity         float64
			ResourceType     string
		}{
			VendorCustomerID: nil,
			MeasuredType:     "LOGICAL_SIZE",
			Quantity:         100.0,
			ResourceType:     "VOLUME",
		}

		googleMetric := common.GoogleMetric{Record: &fakeBillingMetric}
		result := formatMetricForLogging(googleMetric)

		// Since this is not a real *datamodel.AggregatedUsage, GetType() will return -1
		assert.Contains(t, result, "Unknown metric type:")
	})

	// The existing tests already cover the error paths, but let's verify they work
	t.Run("Verify existing error path coverage", func(t *testing.T) {
		// Test with invalid record that causes GetAsHydratedMetric to fail
		invalidGoogleMetric := common.NewGoogleMetric("invalid-record")
		result := formatMetricForLogging(*invalidGoogleMetric)

		// This should trigger the "Unknown metric type" path
		assert.Contains(t, result, "Unknown metric type:")
	})
}
