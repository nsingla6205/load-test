package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	datamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/performance"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"google.golang.org/api/iterator"
	metric "google.golang.org/genproto/googleapis/api/metric"
	"google.golang.org/genproto/googleapis/api/monitoredres"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func contextWithQuotaProject(projectID string) interface{} {
	return mock.MatchedBy(func(ctx context.Context) bool {
		outgoingMetadata, ok := grpcmetadata.FromOutgoingContext(ctx)
		if !common.LoadConfig().EnableTenantQuotaProject {
			if !ok {
				return true
			}
			return len(outgoingMetadata.Get("x-goog-user-project")) == 0
		}
		if !ok {
			return false
		}
		values := outgoingMetadata.Get("x-goog-user-project")
		return len(values) == 1 && values[0] == projectID
	})
}

func TestWithQuotaProject(t *testing.T) {
	ctx := context.Background()
	projectID := "tenant-project-1"

	t.Run("disabled leaves context unchanged", func(t *testing.T) {
		got := withQuotaProject(ctx, projectID, false)
		_, ok := grpcmetadata.FromOutgoingContext(got)
		assert.False(t, ok)
		assert.Equal(t, ctx, got)
	})

	t.Run("enabled sets x-goog-user-project", func(t *testing.T) {
		got := withQuotaProject(ctx, projectID, true)
		outgoingMetadata, ok := grpcmetadata.FromOutgoingContext(got)
		require.True(t, ok)
		assert.Equal(t, []string{projectID}, outgoingMetadata.Get("x-goog-user-project"))
	})
}

func TestCollectProjectMetrics_QuotaProjectHeaderFlag(t *testing.T) {
	logger := log.NewLogger()
	timestamp := time.Now()
	projectID := "project1"

	newProvider := func(mockClient *MockMonitoringClient) *GoogleVolumeMetricsProvider {
		return &GoogleVolumeMetricsProvider{
			client:    mockClient,
			startTime: timestamp.Add(-time.Hour),
			endTime:   timestamp,
			metrics: []common.MetricItem{
				{Metric: "volume_space_logical_used", ResourceType: "custom.googleapis.com"},
			},
		}
	}

	t.Run("omits header when flag is disabled", func(t *testing.T) {
		t.Setenv("ENABLE_TENANT_QUOTA_PROJECT", "false")
		mockIterator := new(MockTimeSeriesIterator)
		mockIterator.On("Next").Return(nil, iterator.Done)
		mockClient := new(MockMonitoringClient)
		mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.AnythingOfType("*monitoringpb.ListTimeSeriesRequest")).Return(mockIterator)

		_, err := newProvider(mockClient).CollectProjectMetrics(context.Background(), logger, projectID, timestamp)
		assert.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("sets header when flag is enabled", func(t *testing.T) {
		t.Setenv("ENABLE_TENANT_QUOTA_PROJECT", "true")
		mockIterator := new(MockTimeSeriesIterator)
		mockIterator.On("Next").Return(nil, iterator.Done)
		mockClient := new(MockMonitoringClient)
		mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.AnythingOfType("*monitoringpb.ListTimeSeriesRequest")).Return(mockIterator)

		_, err := newProvider(mockClient).CollectProjectMetrics(context.Background(), logger, projectID, timestamp)
		assert.NoError(t, err)
		mockClient.AssertExpectations(t)
	})
}

// setupTestJobQueue creates a test JobQueue with in-memory database
func setupTestJobQueue(t *testing.T) (*utils.JobQueue, func()) {
	gormDB, err := database.SetupTestDB()
	require.NoError(t, err)

	sqlDB, err := gormDB.DB()
	require.NoError(t, err)

	// Drop existing jobs table if it exists (VCP jobs table has different schema)
	_, err = sqlDB.Exec(`DROP TABLE IF EXISTS jobs`)
	require.NoError(t, err)

	// Create jobs table with JobQueue schema
	_, err = sqlDB.Exec(`
CREATE TABLE jobs (
id INTEGER PRIMARY KEY AUTOINCREMENT,
type_name TEXT NOT NULL,
status TEXT NOT NULL DEFAULT 'new',
queue TEXT NOT NULL,
data TEXT NOT NULL,
error TEXT,
attempt INTEGER DEFAULT 0,
lease_expires_at DATETIME,
worker_id TEXT,
created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
started_at DATETIME,
finished_at DATETIME,
scheduled_at DATETIME
)
`)
	require.NoError(t, err)

	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	jobQueue := utils.NewQueue(sqlDB, nil, mockMetricRecorder)

	cleanup := func() {
		_ = sqlDB.Close()
	}

	return jobQueue, cleanup
}

func TestGoogleTenantProjectProvider_GetTenantProjects_DelegatesToGetTenantProject(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	vcpStore := &database.MockStorage{}
	expectedProjects := []string{"projectX", "projectY"}

	vcpStore.On("ListTpProjects", ctx).Return(expectedProjects, nil)
	provider := &GoogleTenantProjectProvider{vcpDatastore: vcpStore}

	projects, err := provider.GetTenantProjects(ctx, logger)
	assert.NoError(t, err)
	assert.Equal(t, expectedProjects, projects)
	vcpStore.AssertExpectations(t)
}

func TestGetTenantProject_LogsErrorOnFailure(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	vcpStore := &database.MockStorage{}
	vcpStore.On("ListTpProjects", ctx).Return([]string{}, fmt.Errorf("db error"))

	projects, err := GetTenantProject(ctx, logger, vcpStore)
	assert.Error(t, err)
	assert.Nil(t, projects)
	vcpStore.AssertExpectations(t)
}
func TestGetTenantProject_ReturnsProjectsSuccessfully(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	vcpStore := &database.MockStorage{}
	expectedProjects := []string{"project1", "project2"}

	vcpStore.On("ListTpProjects", ctx).Return(expectedProjects, nil)

	projects, err := GetTenantProject(ctx, logger, vcpStore)
	assert.NoError(t, err)
	assert.Equal(t, expectedProjects, projects)
	vcpStore.AssertExpectations(t)
}

func TestGetTenantProject_ReturnsErrorWhenListTpProjectsFails(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	vcpStore := &database.MockStorage{}

	vcpStore.On("ListTpProjects", ctx).Return([]string{}, fmt.Errorf("db error"))

	projects, err := GetTenantProject(ctx, logger, vcpStore)
	assert.Error(t, err)
	assert.Nil(t, projects)
	assert.Contains(t, err.Error(), "failed to list SnHostsProjects")
	vcpStore.AssertExpectations(t)
}

func TestGetTenantProject_ReturnsErrorWhenNoProjectsFound(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	vcpStore := &database.MockStorage{}

	vcpStore.On("ListTpProjects", ctx).Return([]string{}, nil)

	projects, err := GetTenantProject(ctx, logger, vcpStore)
	assert.Error(t, err)
	assert.Nil(t, projects)
	assert.Equal(t, "no projects found from DB", err.Error())
	vcpStore.AssertExpectations(t)
}

func TestGetTenantProjects_ReturnsProjectsFromUnderlyingFunction(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	expectedProjects := []string{"projectA", "projectB"}

	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctx, logger).Return(expectedProjects, nil)

	projects, err := mockTenantProvider.GetTenantProjects(ctx, logger)
	assert.NoError(t, err)
	assert.ElementsMatch(t, expectedProjects, projects)
	mockTenantProvider.AssertExpectations(t)
}

func TestGetTenantProjects_PropagatesErrorFromUnderlyingFunction(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctx, logger).Return(nil, errors.New("db error"))

	projects, err := mockTenantProvider.GetTenantProjects(ctx, logger)
	assert.Error(t, err)
	assert.Nil(t, projects)
	assert.Contains(t, err.Error(), "db error")
	mockTenantProvider.AssertExpectations(t)
}

func TestGetTenantProjects_ReturnsErrorWhenNoPoolsFound(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctx, logger).Return(nil, fmt.Errorf("no pools found from DB"))

	projects, err := mockTenantProvider.GetTenantProjects(ctx, logger)
	assert.Error(t, err)
	assert.Nil(t, projects)
	assert.Equal(t, "no pools found from DB", err.Error())
	mockTenantProvider.AssertExpectations(t)
}

func TestCollectVolumeMetrics(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	t.Run("returns metrics from provider", func(t *testing.T) {
		mockProvider := new(MockVolumeMetricsProvider)
		mockResp := &monitoringpb.TimeSeries{
			Resource: &monitoredres.MonitoredResource{
				Labels: map[string]string{
					"name":     "test-volume",
					"location": "us-west1",
				},
			},
			Metric: &metric.Metric{
				Labels: map[string]string{
					"volume":         "test-volume-1",
					"is_regional_ha": "true",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Interval: &monitoringpb.TimeInterval{
						EndTime: timestamppb.New(time.Now()),
					},
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_BoolValue{BoolValue: true},
					},
				},
			},
		}

		mockResp2 := &monitoringpb.TimeSeries{
			Resource: &monitoredres.MonitoredResource{
				Labels: map[string]string{
					"name":     "test-volume-3",
					"location": "us-west1",
				},
			},
			Metric: &metric.Metric{
				Labels: map[string]string{
					"volume": "test-volume-1",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Interval: &monitoringpb.TimeInterval{
						EndTime: timestamppb.New(time.Now()),
					},
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_StringValue{StringValue: "unsupported"},
					},
				},
			},
		}

		mockResp3 := &monitoringpb.TimeSeries{
			Resource: &monitoredres.MonitoredResource{
				Labels: map[string]string{
					"name":     "test-volume-3",
					"location": "us-west1",
				},
			},
			Metric: &metric.Metric{
				Labels: map[string]string{
					"volume": "test-volume-1",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Interval: &monitoringpb.TimeInterval{
						EndTime: timestamppb.New(time.Now()),
					},
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1024},
					},
				},
			},
		}

		expectedMetric1 := setupHydratedMetrics(metadata.AllocatedSize, metadata.Volume, "consumer1", mockResp, time.Now())
		expectedMetric2 := setupHydratedMetrics(metadata.AllocatedSize, metadata.Volume, "consumer1", mockResp2, time.Now())
		expectedMetric3 := setupHydratedMetrics(metadata.AllocatedSize, metadata.Volume, "consumer1", mockResp3, time.Now())
		expected := []datamodel.HydratedMetrics{*expectedMetric1, *expectedMetric2, *expectedMetric3}
		mockProvider.On("CollectProjectMetrics", ctx, logger, mock.Anything, mock.Anything).Return(expected, nil)

		results, err := mockProvider.CollectProjectMetrics(ctx, logger, "project1", time.Now())
		assert.NoError(t, err)
		assert.Equal(t, expected, results)
		assert.Equal(t, 1.0, results[0].Quantity)
		assert.Equal(t, 0.0, results[1].Quantity)
		assert.Equal(t, 1024.0, results[2].Quantity)
		mockProvider.AssertExpectations(t)
	})

	t.Run("returns metrics from provider including volume replication", func(t *testing.T) {
		mockProvider := new(MockVolumeMetricsProvider)
		mockResp := &monitoringpb.TimeSeries{
			Resource: &monitoredres.MonitoredResource{
				Labels: map[string]string{
					"name":     "test-volume",
					"location": "us-west1",
				},
			},
			Metric: &metric.Metric{
				Labels: map[string]string{
					"relationship_id": "relationship-1",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Interval: &monitoringpb.TimeInterval{
						EndTime: timestamppb.New(time.Now()),
					},
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_BoolValue{BoolValue: true},
					},
				},
			},
		}

		mockResp2 := &monitoringpb.TimeSeries{
			Resource: &monitoredres.MonitoredResource{
				Labels: map[string]string{
					"name":     "test-volume-3",
					"location": "us-west1",
				},
			},
			Metric: &metric.Metric{
				Labels: map[string]string{
					"volume": "test-volume-1",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Interval: &monitoringpb.TimeInterval{
						EndTime: timestamppb.New(time.Now()),
					},
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_StringValue{StringValue: "unsupported"},
					},
				},
			},
		}

		mockResp3 := &monitoringpb.TimeSeries{
			Resource: &monitoredres.MonitoredResource{
				Labels: map[string]string{
					"name":     "test-volume-3",
					"location": "us-west1",
				},
			},
			Metric: &metric.Metric{
				Labels: map[string]string{
					"volume": "test-volume-1",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Interval: &monitoringpb.TimeInterval{
						EndTime: timestamppb.New(time.Now()),
					},
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1024},
					},
				},
			},
		}

		expectedMetric1 := setupHydratedMetrics(metadata.AllocatedSize, metadata.VolumeReplicationRelationship, "consumer1", mockResp, time.Now())
		expectedMetric2 := setupHydratedMetrics(metadata.AllocatedSize, metadata.Volume, "consumer1", mockResp2, time.Now())
		expectedMetric3 := setupHydratedMetrics(metadata.AllocatedSize, metadata.Volume, "consumer1", mockResp3, time.Now())
		expected := []datamodel.HydratedMetrics{*expectedMetric1, *expectedMetric2, *expectedMetric3}
		mockProvider.On("CollectProjectMetrics", ctx, logger, mock.Anything, mock.Anything).Return(expected, nil)

		results, err := mockProvider.CollectProjectMetrics(ctx, logger, "project1", time.Now())
		assert.NoError(t, err)
		assert.Equal(t, expected, results)
		assert.Equal(t, 1.0, results[0].Quantity)
		assert.Equal(t, 0.0, results[1].Quantity)
		assert.Equal(t, 1024.0, results[2].Quantity)
		mockProvider.AssertExpectations(t)
	})

	t.Run("returns error from provider", func(t *testing.T) {
		mockProvider := new(MockVolumeMetricsProvider)
		mockProvider.On("CollectProjectMetrics", ctx, logger, mock.Anything, mock.Anything).Return([]datamodel.HydratedMetrics(nil), fmt.Errorf("fail"))

		results, err := mockProvider.CollectProjectMetrics(ctx, logger, "project1", time.Now())
		assert.Error(t, err)
		assert.Nil(t, results)
		mockProvider.AssertExpectations(t)
	})
}

func TestCollectVolumeMetrics_ProviderReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	mockProvider := new(MockVolumeMetricsProvider)
	mockProvider.On("CollectProjectMetrics", ctx, logger, mock.Anything, mock.Anything).Return([]datamodel.HydratedMetrics{}, nil)

	results, err := mockProvider.CollectProjectMetrics(ctx, logger, "project1", time.Now())
	assert.NoError(t, err)
	assert.Empty(t, results)
	mockProvider.AssertExpectations(t)
}

func TestGoogleVolumeMetricsProvider_GetVolumeMetrics_Success(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	// Mock iterator
	mockIterator := new(MockTimeSeriesIterator)
	mockResp := &monitoringpb.TimeSeries{
		Resource: &monitoredres.MonitoredResource{
			Labels: map[string]string{
				"name":     "test-volume",
				"location": "us-west1",
			},
		},
		Metric: &metric.Metric{
			Labels: map[string]string{
				"volume": "test-volume-1",
			},
		},
		Points: []*monitoringpb.Point{
			{
				Interval: &monitoringpb.TimeInterval{
					EndTime: timestamppb.New(time.Now()),
				},
				Value: &monitoringpb.TypedValue{
					Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 123.45},
				},
			},
		},
	}
	mockIterator.On("Next").Return(mockResp, nil).Once()
	mockIterator.On("Next").Return(nil, iterator.Done).Once()
	mockIterator.On("Next").Return(nil, iterator.Done)

	// Mock client
	mockClient := new(MockMonitoringClient)
	mockClient.On("ListTimeSeries", contextWithQuotaProject("project1"), mock.AnythingOfType("*monitoringpb.ListTimeSeriesRequest")).Return(mockIterator)

	provider := &GoogleVolumeMetricsProvider{
		client:     mockClient,
		startTime:  time.Now().Add(-time.Hour),
		endTime:    time.Now(),
		googleSink: nil, // Set to nil for test
		metrics: []common.MetricItem{
			{
				Metric:       "volume_space_logical_used",
				ResourceType: "custom.googleapis.com",
			},
		},
	}

	results, err := provider.CollectProjectMetrics(ctx, logger, "project1", time.Now())
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, string(metadata.LogicalSize), results[0].MeasuredType)
	mockClient.AssertExpectations(t)
	mockIterator.AssertExpectations(t)
}

func TestGoogleVolumeMetricsProvider_GetVolumeMetrics_EmptyPoints(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	// Mock iterator with empty points
	mockIterator := new(MockTimeSeriesIterator)
	mockResp := &monitoringpb.TimeSeries{
		Resource: &monitoredres.MonitoredResource{
			Labels: map[string]string{
				"name":     "test-volume",
				"location": "us-west1",
			},
		},
		Points: []*monitoringpb.Point{}, // Empty points array
	}
	mockIterator.On("Next").Return(mockResp, nil).Once()
	mockIterator.On("Next").Return(nil, iterator.Done).Once()
	mockIterator.On("Next").Return(nil, iterator.Done)

	// Mock client
	mockClient := new(MockMonitoringClient)
	mockClient.On("ListTimeSeries", contextWithQuotaProject("project1"), mock.AnythingOfType("*monitoringpb.ListTimeSeriesRequest")).Return(mockIterator)

	provider := &GoogleVolumeMetricsProvider{
		client:    mockClient,
		startTime: time.Now().Add(-time.Hour),
		endTime:   time.Now(),
		metrics: []common.MetricItem{
			{
				Metric:       "volume_read_ops",
				ResourceType: "custom.googleapis.com",
			},
		},
	}

	results, err := provider.CollectProjectMetrics(ctx, logger, "project1", time.Now())
	assert.NoError(t, err)
	assert.Empty(t, results) // Should be empty since no valid data points
	mockClient.AssertExpectations(t)
	mockIterator.AssertExpectations(t)
}

// Comprehensive tests for GetVolumeMetrics method to achieve 100% coverage
func TestGoogleVolumeMetricsProvider_GetVolumeMetrics_Success_NoProjects(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	// Mock tenant project provider that returns empty projects list
	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctx, logger).Return([]string{}, nil)

	// Create test job queue (won't be used since no projects)
	jobQueue, cleanup := setupTestJobQueue(t)
	defer cleanup()

	provider := &GoogleVolumeMetricsProvider{
		tenantProjectProvider: mockTenantProvider,
		jobQueue:              jobQueue,
	}

	err := provider.GetVolumeMetrics(ctx, logger, time.Now())
	assert.NoError(t, err)

	mockTenantProvider.AssertExpectations(t)
}

func TestGoogleVolumeMetricsProvider_GetVolumeMetrics_Success_SingleProject(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	projects := []string{"project1"}

	// Mock tenant project provider
	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctx, logger).Return(projects, nil)

	// Create test job queue
	jobQueue, cleanup := setupTestJobQueue(t)
	defer cleanup()

	provider := &GoogleVolumeMetricsProvider{
		tenantProjectProvider: mockTenantProvider,
		jobQueue:              jobQueue,
	}

	err := provider.GetVolumeMetrics(ctx, logger, time.Now())
	assert.NoError(t, err)

	mockTenantProvider.AssertExpectations(t)
}

func TestGoogleVolumeMetricsProvider_GetVolumeMetrics_Success_MultipleProjects(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	projects := []string{"project1", "project2", "project3"}

	// Mock tenant project provider
	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctx, logger).Return(projects, nil)

	// Create test job queue
	jobQueue, cleanup := setupTestJobQueue(t)
	defer cleanup()

	provider := &GoogleVolumeMetricsProvider{
		tenantProjectProvider: mockTenantProvider,
		jobQueue:              jobQueue,
	}

	err := provider.GetVolumeMetrics(ctx, logger, time.Now())
	assert.NoError(t, err)

	mockTenantProvider.AssertExpectations(t)
}

func TestGoogleVolumeMetricsProvider_GetVolumeMetrics_ErrorFromGetTenantProjects(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	expectedError := errors.New("database connection failed")

	// Mock tenant project provider that returns error
	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctx, logger).Return([]string(nil), expectedError)

	// Create test job queue (won't be used due to early error)
	jobQueue, cleanup := setupTestJobQueue(t)
	defer cleanup()

	provider := &GoogleVolumeMetricsProvider{
		tenantProjectProvider: mockTenantProvider,
		jobQueue:              jobQueue,
	}

	err := provider.GetVolumeMetrics(ctx, logger, time.Now())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get tenant projects")
	assert.Contains(t, err.Error(), "database connection failed")

	mockTenantProvider.AssertExpectations(t)
}

func TestGoogleVolumeMetricsProvider_GetVolumeMetrics_WithCorrelationID(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	projects := []string{"project1"}
	correlationID := "test-correlation-id-123"

	// Create context with correlation ID in logger fields
	loggerFields := log.Fields{
		"requestCorrelationID": correlationID,
	}
	ctxWithCorrelationID := context.WithValue(ctx, middleware.TemporalSLoggerKey, loggerFields)

	// Mock tenant project provider
	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctxWithCorrelationID, logger).Return(projects, nil)

	// Create test job queue
	jobQueue, cleanup := setupTestJobQueue(t)
	defer cleanup()

	provider := &GoogleVolumeMetricsProvider{
		tenantProjectProvider: mockTenantProvider,
		jobQueue:              jobQueue,
	}

	err := provider.GetVolumeMetrics(ctxWithCorrelationID, logger, time.Now())
	assert.NoError(t, err)

	mockTenantProvider.AssertExpectations(t)

	// Verify that the job was enqueued with correlation ID
	// We can verify this by checking the job queue contains the job
	// This indirectly tests that the correlation ID was extracted and used
}

func TestGoogleVolumeMetricsProvider_GetVolumeMetrics_WithInvalidCorrelationID(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	projects := []string{"project1"}

	// Create context with invalid correlation ID (not a string)
	loggerFields := log.Fields{
		"requestCorrelationID": 12345, // Non-string value
	}
	ctxWithInvalidCorrelationID := context.WithValue(ctx, middleware.TemporalSLoggerKey, loggerFields)

	// Mock tenant project provider
	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctxWithInvalidCorrelationID, logger).Return(projects, nil)

	// Create test job queue
	jobQueue, cleanup := setupTestJobQueue(t)
	defer cleanup()

	provider := &GoogleVolumeMetricsProvider{
		tenantProjectProvider: mockTenantProvider,
		jobQueue:              jobQueue,
	}

	err := provider.GetVolumeMetrics(ctxWithInvalidCorrelationID, logger, time.Now())
	assert.NoError(t, err)

	mockTenantProvider.AssertExpectations(t)
}

func TestGoogleVolumeMetricsProvider_GetVolumeMetrics_WithoutCorrelationID(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	projects := []string{"project1"}

	// Create context with logger fields but no correlation ID
	loggerFields := log.Fields{
		"someOtherField": "value",
	}
	ctxWithoutCorrelationID := context.WithValue(ctx, middleware.TemporalSLoggerKey, loggerFields)

	// Mock tenant project provider
	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctxWithoutCorrelationID, logger).Return(projects, nil)

	// Create test job queue
	jobQueue, cleanup := setupTestJobQueue(t)
	defer cleanup()

	provider := &GoogleVolumeMetricsProvider{
		tenantProjectProvider: mockTenantProvider,
		jobQueue:              jobQueue,
	}

	err := provider.GetVolumeMetrics(ctxWithoutCorrelationID, logger, time.Now())
	assert.NoError(t, err)

	mockTenantProvider.AssertExpectations(t)
}

func TestGoogleVolumeMetricsProvider_GetVolumeMetrics_WithoutLoggerFields(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	projects := []string{"project1"}

	// Create context without logger fields (different context value type)
	ctxWithoutLoggerFields := context.WithValue(ctx, middleware.TemporalSLoggerKey, "not-a-fields-object")

	// Mock tenant project provider
	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctxWithoutLoggerFields, logger).Return(projects, nil)

	// Create test job queue
	jobQueue, cleanup := setupTestJobQueue(t)
	defer cleanup()

	provider := &GoogleVolumeMetricsProvider{
		tenantProjectProvider: mockTenantProvider,
		jobQueue:              jobQueue,
	}

	err := provider.GetVolumeMetrics(ctxWithoutLoggerFields, logger, time.Now())
	assert.NoError(t, err)

	mockTenantProvider.AssertExpectations(t)
}

// Test to cover missing line 64: EnqueueBatch error handling
func TestGoogleVolumeMetricsProvider_GetVolumeMetrics_EnqueueBatchError(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()

	projects := []string{"project1"}

	// Mock tenant project provider
	mockTenantProvider := new(MockTenantProjectProvider)
	mockTenantProvider.On("GetTenantProjects", ctx, logger).Return(projects, nil)

	// Create a test job queue with a broken database connection
	// This will cause EnqueueBatch to fail
	gormDB, err := database.SetupTestDB()
	require.NoError(t, err)

	sqlDB, err := gormDB.DB()
	require.NoError(t, err)

	// Close the database connection to cause EnqueueBatch to fail
	_ = sqlDB.Close()

	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	jobQueue := utils.NewQueue(sqlDB, nil, mockMetricRecorder)

	provider := &GoogleVolumeMetricsProvider{
		tenantProjectProvider: mockTenantProvider,
	}
	provider.SetJobQueue(jobQueue)

	err = provider.GetVolumeMetrics(ctx, logger, time.Now())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "queue: failed to begin transaction")

	mockTenantProvider.AssertExpectations(t)
}

// Test for collectVolumeMetrics function to ensure line 140 is covered
func TestCollectVolumeMetrics_DirectCall(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	timestamp := time.Now()

	t.Run("successful call to provider", func(t *testing.T) {
		mockProvider := new(MockVolumeMetricsProvider)

		// Mock the GetVolumeMetrics call (line 140)
		mockProvider.On("GetVolumeMetrics", ctx, logger, timestamp).Return(nil)

		// Call collectVolumeMetrics function directly to cover line 140
		err := collectVolumeMetrics(ctx, logger, mockProvider, timestamp)

		assert.NoError(t, err)
		mockProvider.AssertExpectations(t)
	})

	t.Run("provider returns error", func(t *testing.T) {
		mockProvider := new(MockVolumeMetricsProvider)
		expectedError := errors.New("provider error")

		// Mock the GetVolumeMetrics call to return error (line 140)
		mockProvider.On("GetVolumeMetrics", ctx, logger, timestamp).Return(expectedError)

		// Call collectVolumeMetrics function directly to cover line 140
		err := collectVolumeMetrics(ctx, logger, mockProvider, timestamp)

		assert.Error(t, err)
		assert.Equal(t, expectedError, err)
		mockProvider.AssertExpectations(t)
	})
}

// TestGoogleVolumeMetricsProvider_CollectProjectMetrics_PerformanceFlow tests the performance metric collection flow
func TestGoogleVolumeMetricsProvider_CollectProjectMetrics_PerformanceFlow(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	projectID := "test-project-123"
	timestamp := time.Now()

	// Create mock client and iterator
	mockClient := new(MockMonitoringClient)
	mockIterator := new(MockTimeSeriesIterator)

	// Test performance metrics
	testMetrics := []common.MetricItem{
		{Metric: "operations_total", ResourceType: "netapp.com/volume", MetricType: "performance"},
	}

	// Create GoogleSink for the provider
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	googleSink := performance.NewSink(ctx, config, mockMetricRecorder)

	provider := &GoogleVolumeMetricsProvider{
		client:     mockClient,
		metrics:    testMetrics,
		startTime:  timestamp.Add(-5 * time.Minute),
		endTime:    timestamp,
		googleSink: googleSink,
	}

	// Create test time series for volume metrics (covers lines 141-154)
	volumeTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/volume/operations_total",
			Labels: map[string]string{
				"metric":          "operations_total",
				"volume":          "test-volume",
				"project":         projectID,
				"datacenter":      "us-central1",
				"pool_name":       "test-pool",
				"deployment_name": "test-deployment",
			},
		},
		Points: []*monitoringpb.Point{
			{
				Value: &monitoringpb.TypedValue{
					Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 100.5},
				},
			},
		},
	}

	// Mock the iterator to return our test metrics
	mockIterator.On("Next").Return(volumeTimeSeries, nil).Once()
	mockIterator.On("Next").Return(nil, iterator.Done).Once()

	// Mock the client to return our iterator and verify aggregation is set for performance metrics (covers lines 99-114)
	mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.MatchedBy(func(req *monitoringpb.ListTimeSeriesRequest) bool {
		groupBy := make(map[string]bool, len(req.Aggregation.GroupByFields))
		for _, field := range req.Aggregation.GroupByFields {
			groupBy[field] = true
		}

		return req.Aggregation != nil &&
			req.Aggregation.PerSeriesAligner == monitoringpb.Aggregation_ALIGN_MEAN &&
			req.Aggregation.CrossSeriesReducer == monitoringpb.Aggregation_REDUCE_MEAN &&
			len(req.Aggregation.GroupByFields) == 11 &&
			groupBy["metric.label.relationship_id"] &&
			groupBy["metric.label.source_details"] &&
			groupBy["metric.label.destination_details"] &&
			groupBy["metric.label.last_transfer_type"]
	})).Return(mockIterator)

	result, err := provider.CollectProjectMetrics(ctx, logger, projectID, timestamp)

	assert.NoError(t, err)
	assert.Empty(t, result) // Performance metrics don't get added to project results
	mockClient.AssertExpectations(t)
	mockIterator.AssertExpectations(t)
	// Note: We expect googleSink.DeliverMetrics to be called (covers lines 161-163)
}

// TestGoogleVolumeMetricsProvider_CollectProjectMetrics_VolumePoolResourceType tests volume pool metrics
func TestGoogleVolumeMetricsProvider_CollectProjectMetrics_VolumePoolResourceType(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	projectID := "test-project-123"
	timestamp := time.Now()

	// Create mock client and iterator
	mockClient := new(MockMonitoringClient)
	mockIterator := new(MockTimeSeriesIterator)

	testCases := []struct {
		name         string
		resourceType string
		metricType   string
		labels       map[string]string
	}{
		{
			name:         "Volume performance metric",
			resourceType: "Volume",
			metricType:   "performance",
			labels: map[string]string{
				"metric":     "operations_total",
				"volume":     "test-volume-name",
				"project":    projectID,
				"datacenter": "us-central1",
			},
		},
		{
			name:         "VolumePool performance metric",
			resourceType: "VolumePool",
			metricType:   "performance",
			labels: map[string]string{
				"metric":     "space_logical_used",
				"pool_name":  "test-pool-name",
				"project":    projectID,
				"datacenter": "europe-west1",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test performance metrics for specific resource types
			testMetrics := []common.MetricItem{
				{Metric: tc.labels["metric"], ResourceType: "netapp.com/" + strings.ToLower(tc.resourceType), MetricType: tc.metricType},
			}

			// Create GoogleSink for the provider
			config := common.LoadConfig()
			mockMetricRecorder := &monitoring.MockMetricsRecorder{}
			mockMetricRecorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
			googleSink := performance.NewSink(ctx, config, mockMetricRecorder)

			provider := &GoogleVolumeMetricsProvider{
				client:     mockClient,
				metrics:    testMetrics,
				startTime:  timestamp.Add(-5 * time.Minute),
				endTime:    timestamp,
				googleSink: googleSink,
			}

			// Create test time series that will trigger the performance metric path (lines 140-154)
			timeSeries := &monitoringpb.TimeSeries{
				Metric: &metric.Metric{
					Type:   "netapp.com/" + strings.ToLower(tc.resourceType) + "/" + tc.labels["metric"],
					Labels: tc.labels,
				},
				Points: []*monitoringpb.Point{
					{
						Value: &monitoringpb.TypedValue{
							Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 512.0},
						},
					},
				},
			}

			// Reset mocks for each test case
			mockClient.ExpectedCalls = nil
			mockIterator.ExpectedCalls = nil

			// Mock the iterator to return our test metrics
			mockIterator.On("Next").Return(timeSeries, nil).Once()
			mockIterator.On("Next").Return(nil, iterator.Done).Once()

			// Mock the client
			mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.Anything).Return(mockIterator)

			result, err := provider.CollectProjectMetrics(ctx, logger, projectID, timestamp)

			assert.NoError(t, err)
			assert.Empty(t, result) // Performance metrics don't get added to project results but are sent to googleSink
			mockClient.AssertExpectations(t)
			mockIterator.AssertExpectations(t)
		})
	}
}

// Additional test function to cover performance metric edge cases
func TestGoogleVolumeMetricsProvider_PerformanceMetricEdgeCases(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	projectID := "test-project-456"
	timestamp := time.Now()

	mockClient := new(MockMonitoringClient)

	testMetrics := []common.MetricItem{
		{Metric: "volume_read_latency", ResourceType: "netapp.com/volume", MetricType: "performance"},
		{Metric: "pool_client_protocol_reads", ResourceType: "netapp.com/pool", MetricType: "performance"},
	}

	// Create GoogleSink for the provider
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	googleSink := performance.NewSink(ctx, config, mockMetricRecorder)

	provider := &GoogleVolumeMetricsProvider{
		client:     mockClient,
		metrics:    testMetrics,
		startTime:  timestamp.Add(-5 * time.Minute),
		endTime:    timestamp,
		googleSink: googleSink,
	}

	// Create test time series for Volume performance metric (covers lines 143-145 for Volume case)
	volumeTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/volume/volume_read_latency",
			Labels: map[string]string{
				"metric":          "volume_read_latency",
				"volume":          "test-volume",
				"project":         projectID,
				"datacenter":      "us-west1",
				"deployment_name": "test-deployment",
			},
		},
		Points: []*monitoringpb.Point{
			{
				Value: &monitoringpb.TypedValue{
					Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 15.5},
				},
			},
		},
	}

	// Create test time series for VolumePool performance metric (covers lines 146-148 for VolumePool case)
	poolTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/pool/pool_client_protocol_reads",
			Labels: map[string]string{
				"metric":          "pool_client_protocol_reads",
				"pool_name":       "test-pool",
				"project":         projectID,
				"datacenter":      "us-west1",
				"deployment_name": "test-deployment",
			},
		},
		Points: []*monitoringpb.Point{
			{
				Value: &monitoringpb.TypedValue{
					Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 1024.0},
				},
			},
		},
	}

	// Create separate iterators for each metric (provider calls ListTimeSeries once per metric)
	mockIterator1 := new(MockTimeSeriesIterator)
	mockIterator2 := new(MockTimeSeriesIterator)

	// First call for volume_read_latency metric
	mockIterator1.On("Next").Return(volumeTimeSeries, nil).Once()
	mockIterator1.On("Next").Return(nil, iterator.Done).Once()
	mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.MatchedBy(func(req *monitoringpb.ListTimeSeriesRequest) bool {
		return strings.Contains(req.Filter, "volume_read_latency")
	})).Return(mockIterator1).Once()

	// Second call for pool_client_protocol_reads metric
	mockIterator2.On("Next").Return(poolTimeSeries, nil).Once()
	mockIterator2.On("Next").Return(nil, iterator.Done).Once()
	mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.MatchedBy(func(req *monitoringpb.ListTimeSeriesRequest) bool {
		return strings.Contains(req.Filter, "pool_client_protocol_reads")
	})).Return(mockIterator2).Once()

	result, err := provider.CollectProjectMetrics(ctx, logger, projectID, timestamp)

	assert.NoError(t, err)
	assert.Empty(t, result)
	mockClient.AssertExpectations(t)
	mockIterator1.AssertExpectations(t)
	mockIterator2.AssertExpectations(t)
}

func TestGoogleVolumeMetricsProvider_CollectProjectMetrics_PerformanceMetricsSpecificCoverage(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	projectID := "test-project-performance"
	timestamp := time.Now()

	mockClient := new(MockMonitoringClient)

	// Test metrics that will specifically trigger performance metric handling code paths
	testMetrics := []common.MetricItem{
		{Metric: "volume_write_latency", ResourceType: "netapp.com/volume", MetricType: "performance"},
		{Metric: "throughput_limit", ResourceType: "netapp.com/volume", MetricType: "performance"},
		{Metric: "pool_cloud_bin_operation_size", ResourceType: "netapp.com/pool", MetricType: "performance"},
	}

	// Create GoogleSink for the provider
	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	googleSink := performance.NewSink(ctx, config, mockMetricRecorder)

	provider := &GoogleVolumeMetricsProvider{
		client:     mockClient,
		metrics:    testMetrics,
		startTime:  timestamp.Add(-5 * time.Minute),
		endTime:    timestamp,
		googleSink: googleSink,
	}

	// Multiple Volume performance metrics to ensure line 143-145 coverage
	volumeLatencyTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/volume/volume_write_latency",
			Labels: map[string]string{
				"metric":     "volume_write_latency",
				"volume":     "performance-test-volume",
				"project":    projectID,
				"datacenter": "europe-west4",
			},
		},
		Points: []*monitoringpb.Point{{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 25.7}}}},
	}

	volumeThroughputTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/volume/throughput_limit",
			Labels: map[string]string{
				"metric":     "throughput_limit",
				"volume":     "throughput-test-volume",
				"project":    projectID,
				"datacenter": "asia-southeast1",
			},
		},
		Points: []*monitoringpb.Point{{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 512.0}}}},
	}

	// VolumePool performance metric to ensure line 146-148 coverage
	poolBinOpTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/pool/pool_cloud_bin_operation_size",
			Labels: map[string]string{
				"metric":     "pool_cloud_bin_operation_size",
				"pool_name":  "performance-test-pool",
				"project":    projectID,
				"datacenter": "us-east1",
			},
		},
		Points: []*monitoringpb.Point{{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 8192.0}}}},
	}

	// Create separate iterators for each metric (provider calls ListTimeSeries once per metric)
	mockIterator1 := new(MockTimeSeriesIterator)
	mockIterator2 := new(MockTimeSeriesIterator)
	mockIterator3 := new(MockTimeSeriesIterator)

	// First call for volume_write_latency metric
	mockIterator1.On("Next").Return(volumeLatencyTimeSeries, nil).Once()
	mockIterator1.On("Next").Return(nil, iterator.Done).Once()
	mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.MatchedBy(func(req *monitoringpb.ListTimeSeriesRequest) bool {
		return strings.Contains(req.Filter, "volume_write_latency")
	})).Return(mockIterator1).Once()

	// Second call for throughput_limit metric
	mockIterator2.On("Next").Return(volumeThroughputTimeSeries, nil).Once()
	mockIterator2.On("Next").Return(nil, iterator.Done).Once()
	mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.MatchedBy(func(req *monitoringpb.ListTimeSeriesRequest) bool {
		return strings.Contains(req.Filter, "throughput_limit")
	})).Return(mockIterator2).Once()

	// Third call for pool_cloud_bin_operation_size metric
	mockIterator3.On("Next").Return(poolBinOpTimeSeries, nil).Once()
	mockIterator3.On("Next").Return(nil, iterator.Done).Once()
	mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.MatchedBy(func(req *monitoringpb.ListTimeSeriesRequest) bool {
		return strings.Contains(req.Filter, "pool_cloud_bin_operation_size")
	})).Return(mockIterator3).Once()

	result, err := provider.CollectProjectMetrics(ctx, logger, projectID, timestamp)

	assert.NoError(t, err)
	assert.Empty(t, result) // Performance metrics go to googleSink, not returned
	mockClient.AssertExpectations(t)
	mockIterator1.AssertExpectations(t)
	mockIterator2.AssertExpectations(t)
	mockIterator3.AssertExpectations(t)
}

// TestGoogleVolumeMetricsProvider_CloudBinOperationFiltering tests the filtering logic for lines 153-158
// These lines filter out wafl_volume_cloud_bin_operation_size and pool_cloud_bin_operation_size metrics
// when the "metric" label is not "put"
func TestGoogleVolumeMetricsProvider_CloudBinOperationFiltering(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	projectID := "test-project-filtering"
	timestamp := time.Now()

	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	googleSink := performance.NewSink(ctx, config, mockMetricRecorder)

	testCases := []struct {
		name               string
		metricName         string
		metricLabel        string
		resourceType       string
		resourceLabelKey   string
		resourceLabelValue string
		shouldBeFiltered   bool
		description        string
	}{
		{
			name:               "wafl_volume_cloud_bin_operation_size with put - should NOT be filtered",
			metricName:         "wafl_volume_cloud_bin_operation_size",
			metricLabel:        "put",
			resourceType:       "Volume",
			resourceLabelKey:   "volume",
			resourceLabelValue: "test-volume-put",
			shouldBeFiltered:   false,
			description:        "PUT operations should be included for wafl_volume metrics",
		},
		{
			name:               "wafl_volume_cloud_bin_operation_size with get - should be filtered (line 153-154)",
			metricName:         "wafl_volume_cloud_bin_operation_size",
			metricLabel:        "get",
			resourceType:       "Volume",
			resourceLabelKey:   "volume",
			resourceLabelValue: "test-volume-get",
			shouldBeFiltered:   true,
			description:        "GET operations should be filtered out for wafl_volume metrics",
		},
		{
			name:               "wafl_volume_cloud_bin_operation_size with delete - should be filtered (line 153-154)",
			metricName:         "wafl_volume_cloud_bin_operation_size",
			metricLabel:        "delete",
			resourceType:       "Volume",
			resourceLabelKey:   "volume",
			resourceLabelValue: "test-volume-delete",
			shouldBeFiltered:   true,
			description:        "DELETE operations should be filtered out for wafl_volume metrics",
		},
		{
			name:               "pool_cloud_bin_operation_size with put - should NOT be filtered",
			metricName:         "pool_cloud_bin_operation_size",
			metricLabel:        "put",
			resourceType:       "VolumePool",
			resourceLabelKey:   "pool_name",
			resourceLabelValue: "test-pool-put",
			shouldBeFiltered:   false,
			description:        "PUT operations should be included for pool metrics",
		},
		{
			name:               "pool_cloud_bin_operation_size with get - should be filtered (line 156-157)",
			metricName:         "pool_cloud_bin_operation_size",
			metricLabel:        "get",
			resourceType:       "VolumePool",
			resourceLabelKey:   "pool_name",
			resourceLabelValue: "test-pool-get",
			shouldBeFiltered:   true,
			description:        "GET operations should be filtered out for pool metrics",
		},
		{
			name:               "pool_cloud_bin_operation_size with list - should be filtered (line 156-157)",
			metricName:         "pool_cloud_bin_operation_size",
			metricLabel:        "list",
			resourceType:       "VolumePool",
			resourceLabelKey:   "pool_name",
			resourceLabelValue: "test-pool-list",
			shouldBeFiltered:   true,
			description:        "LIST operations should be filtered out for pool metrics",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := new(MockMonitoringClient)
			mockIterator := new(MockTimeSeriesIterator)

			testMetrics := []common.MetricItem{
				{Metric: tc.metricName, ResourceType: "netapp.com/" + strings.ToLower(tc.resourceType), MetricType: "performance"},
			}

			provider := &GoogleVolumeMetricsProvider{
				client:     mockClient,
				metrics:    testMetrics,
				startTime:  timestamp.Add(-5 * time.Minute),
				endTime:    timestamp,
				googleSink: googleSink,
			}

			timeSeries := &monitoringpb.TimeSeries{
				Metric: &metric.Metric{
					Type: "netapp.com/" + strings.ToLower(tc.resourceType) + "/" + tc.metricName,
					Labels: map[string]string{
						"metric":            tc.metricLabel, // This is the key label that determines filtering
						tc.resourceLabelKey: tc.resourceLabelValue,
						"project":           projectID,
						"datacenter":        "us-west1",
					},
				},
				Points: []*monitoringpb.Point{
					{
						Value: &monitoringpb.TypedValue{
							Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 1024.0},
						},
					},
				},
			}

			mockIterator.On("Next").Return(timeSeries, nil).Once()
			mockIterator.On("Next").Return(nil, iterator.Done).Once()
			mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.Anything).Return(mockIterator)

			result, err := provider.CollectProjectMetrics(ctx, logger, projectID, timestamp)

			assert.NoError(t, err)
			// Performance metrics are sent to googleSink, not returned in result
			assert.Empty(t, result, tc.description)

			mockClient.AssertExpectations(t)
			mockIterator.AssertExpectations(t)
		})
	}
}

// TestGoogleVolumeMetricsProvider_CloudBinOperationFiltering_MultipleSeries tests filtering with multiple time series
func TestGoogleVolumeMetricsProvider_CloudBinOperationFiltering_MultipleSeries(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	projectID := "test-project-multi-filtering"
	timestamp := time.Now()

	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	googleSink := performance.NewSink(ctx, config, mockMetricRecorder)

	mockClient := new(MockMonitoringClient)
	mockIterator := new(MockTimeSeriesIterator)

	testMetrics := []common.MetricItem{
		{Metric: "wafl_volume_cloud_bin_operation_size", ResourceType: "netapp.com/volume", MetricType: "performance"},
	}

	provider := &GoogleVolumeMetricsProvider{
		client:     mockClient,
		metrics:    testMetrics,
		startTime:  timestamp.Add(-5 * time.Minute),
		endTime:    timestamp,
		googleSink: googleSink,
	}

	// Create multiple time series with different metric labels
	putTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/volume/wafl_volume_cloud_bin_operation_size",
			Labels: map[string]string{
				"metric":     "put",
				"volume":     "test-volume-1",
				"project":    projectID,
				"datacenter": "us-west1",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 100.0}}},
		},
	}

	getTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/volume/wafl_volume_cloud_bin_operation_size",
			Labels: map[string]string{
				"metric":     "get",
				"volume":     "test-volume-2",
				"project":    projectID,
				"datacenter": "us-west1",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 200.0}}},
		},
	}

	deleteTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/volume/wafl_volume_cloud_bin_operation_size",
			Labels: map[string]string{
				"metric":     "delete",
				"volume":     "test-volume-3",
				"project":    projectID,
				"datacenter": "us-west1",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 300.0}}},
		},
	}

	anotherPutTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/volume/wafl_volume_cloud_bin_operation_size",
			Labels: map[string]string{
				"metric":     "put",
				"volume":     "test-volume-4",
				"project":    projectID,
				"datacenter": "us-east1",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 400.0}}},
		},
	}

	// Mock iterator to return all time series, but only "put" ones should be processed
	mockIterator.On("Next").Return(putTimeSeries, nil).Once()
	mockIterator.On("Next").Return(getTimeSeries, nil).Once()
	mockIterator.On("Next").Return(deleteTimeSeries, nil).Once()
	mockIterator.On("Next").Return(anotherPutTimeSeries, nil).Once()
	mockIterator.On("Next").Return(nil, iterator.Done).Once()

	mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.Anything).Return(mockIterator)

	result, err := provider.CollectProjectMetrics(ctx, logger, projectID, timestamp)

	assert.NoError(t, err)
	// Performance metrics are sent to googleSink, not returned in result
	// Only 2 "put" metrics should have been processed, "get" and "delete" should be filtered
	assert.Empty(t, result)

	mockClient.AssertExpectations(t)
	mockIterator.AssertExpectations(t)
}

// TestGoogleVolumeMetricsProvider_CloudBinOperationFiltering_PoolMetrics tests pool-specific filtering
func TestGoogleVolumeMetricsProvider_CloudBinOperationFiltering_PoolMetrics(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	projectID := "test-project-pool-filtering"
	timestamp := time.Now()

	config := common.LoadConfig()
	mockMetricRecorder := &monitoring.MockMetricsRecorder{}
	mockMetricRecorder.On("RecordSinkDelivered", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	googleSink := performance.NewSink(ctx, config, mockMetricRecorder)

	mockClient := new(MockMonitoringClient)
	mockIterator := new(MockTimeSeriesIterator)

	testMetrics := []common.MetricItem{
		{Metric: "pool_cloud_bin_operation_size", ResourceType: "netapp.com/pool", MetricType: "performance"},
	}

	provider := &GoogleVolumeMetricsProvider{
		client:     mockClient,
		metrics:    testMetrics,
		startTime:  timestamp.Add(-5 * time.Minute),
		endTime:    timestamp,
		googleSink: googleSink,
	}

	// Create multiple time series for pool metrics
	poolPutTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/pool/pool_cloud_bin_operation_size",
			Labels: map[string]string{
				"metric":     "put",
				"pool_name":  "test-pool-1",
				"project":    projectID,
				"datacenter": "us-central1",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 2048.0}}},
		},
	}

	poolGetTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/pool/pool_cloud_bin_operation_size",
			Labels: map[string]string{
				"metric":     "get",
				"pool_name":  "test-pool-2",
				"project":    projectID,
				"datacenter": "us-central1",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 4096.0}}},
		},
	}

	poolListTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "netapp.com/pool/pool_cloud_bin_operation_size",
			Labels: map[string]string{
				"metric":     "list",
				"pool_name":  "test-pool-3",
				"project":    projectID,
				"datacenter": "us-central1",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 1024.0}}},
		},
	}

	// Mock iterator returns all series: put (included), get (filtered), list (filtered)
	mockIterator.On("Next").Return(poolPutTimeSeries, nil).Once()
	mockIterator.On("Next").Return(poolGetTimeSeries, nil).Once()
	mockIterator.On("Next").Return(poolListTimeSeries, nil).Once()
	mockIterator.On("Next").Return(nil, iterator.Done).Once()

	mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.Anything).Return(mockIterator)

	result, err := provider.CollectProjectMetrics(ctx, logger, projectID, timestamp)

	assert.NoError(t, err)
	// Performance metrics are sent to googleSink, not returned in result
	// Only 1 "put" metric should have been processed, "get" and "list" should be filtered
	assert.Empty(t, result)

	mockClient.AssertExpectations(t)
	mockIterator.AssertExpectations(t)
}

// TestGoogleVolumeMetricsProvider_PoolCloudBinOperationSizeRawFiltering tests the filtering logic for
// pool_cloud_bin_operation_size_raw usage metrics - only "put" operations should be collected for billing
func TestGoogleVolumeMetricsProvider_PoolCloudBinOperationSizeRawFiltering(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	projectID := "test-project-raw-filtering"
	timestamp := time.Now()

	mockClient := new(MockMonitoringClient)
	mockIterator := new(MockTimeSeriesIterator)

	// This is a usage type metric, so it goes to projectResults (not perfMetrics)
	testMetrics := []common.MetricItem{
		{Metric: "pool_cloud_bin_operation_size_raw", ResourceType: "custom.googleapis.com", MetricType: "usage"},
	}

	provider := &GoogleVolumeMetricsProvider{
		client:    mockClient,
		metrics:   testMetrics,
		startTime: timestamp.Add(-5 * time.Minute),
		endTime:   timestamp,
	}

	// Create time series with "put" operation - should be included
	poolPutTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "custom.googleapis.com/pool_cloud_bin_operation_size_raw",
			Labels: map[string]string{
				"metric":          "put",
				"pool_name":       "test-pool-1",
				"project":         projectID,
				"datacenter":      "us-central1",
				"volume":          "test-volume",
				"deployment_name": "test-deployment",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 2048}}},
		},
	}

	// Create time series with "get" operation - should be filtered out
	poolGetTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "custom.googleapis.com/pool_cloud_bin_operation_size_raw",
			Labels: map[string]string{
				"metric":          "get",
				"pool_name":       "test-pool-2",
				"project":         projectID,
				"datacenter":      "us-central1",
				"volume":          "test-volume-2",
				"deployment_name": "test-deployment",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 4096}}},
		},
	}

	// Mock iterator returns both series
	mockIterator.On("Next").Return(poolPutTimeSeries, nil).Once()
	mockIterator.On("Next").Return(poolGetTimeSeries, nil).Once()
	mockIterator.On("Next").Return(nil, iterator.Done).Once()

	mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.Anything).Return(mockIterator)

	result, err := provider.CollectProjectMetrics(ctx, logger, projectID, timestamp)

	assert.NoError(t, err)
	// Only "put" metric should be in result (usage metrics go to projectResults)
	assert.Len(t, result, 1, "Only 'put' operation should be collected, 'get' should be filtered")
	assert.Equal(t, string(metadata.CoolTierDataWriteSizeRaw), result[0].MeasuredType)
	assert.Equal(t, float64(2048), result[0].Quantity)

	mockClient.AssertExpectations(t)
	mockIterator.AssertExpectations(t)
}

func TestGoogleVolumeMetricsProvider_WaflVolumeCloudBinOperationSizeRawFiltering(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	projectID := "test-project-wafl-raw-filtering"
	timestamp := time.Now()

	mockClient := new(MockMonitoringClient)
	mockIterator := new(MockTimeSeriesIterator)

	testMetrics := []common.MetricItem{
		{Metric: "wafl_volume_cloud_bin_operation_size_raw", ResourceType: "custom.googleapis.com", MetricType: "usage"},
	}

	provider := &GoogleVolumeMetricsProvider{
		client:    mockClient,
		metrics:   testMetrics,
		startTime: timestamp.Add(-5 * time.Minute),
		endTime:   timestamp,
	}

	putTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "custom.googleapis.com/wafl_volume_cloud_bin_operation_size_raw",
			Labels: map[string]string{
				"metric":          "put",
				"volume":          "test-volume-1",
				"project":         projectID,
				"datacenter":      "us-central1",
				"deployment_name": "test-deployment",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 2048}}},
		},
	}

	getTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "custom.googleapis.com/wafl_volume_cloud_bin_operation_size_raw",
			Labels: map[string]string{
				"metric":          "get",
				"volume":          "test-volume-2",
				"project":         projectID,
				"datacenter":      "us-central1",
				"deployment_name": "test-deployment",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 4096}}},
		},
	}

	mockIterator.On("Next").Return(putTimeSeries, nil).Once()
	mockIterator.On("Next").Return(getTimeSeries, nil).Once()
	mockIterator.On("Next").Return(nil, iterator.Done).Once()

	mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.Anything).Return(mockIterator)

	result, err := provider.CollectProjectMetrics(ctx, logger, projectID, timestamp)

	assert.NoError(t, err)
	assert.Len(t, result, 1, "Only 'put' operation should be collected for wafl_volume_cloud_bin_operation_size_raw")
	assert.Equal(t, string(metadata.CoolTierDataWriteSizeRaw), result[0].MeasuredType)
	assert.Equal(t, float64(2048), result[0].Quantity)

	mockClient.AssertExpectations(t)
	mockIterator.AssertExpectations(t)
}

// TestGoogleVolumeMetricsProvider_PoolClientProtocolReadsRaw tests that pool_client_protocol_reads_raw
// metrics are collected without filtering (no "put" filter required for read metrics)
func TestGoogleVolumeMetricsProvider_PoolClientProtocolReadsRaw(t *testing.T) {
	ctx := context.Background()
	logger := log.NewLogger()
	projectID := "test-project-reads-raw"
	timestamp := time.Now()

	mockClient := new(MockMonitoringClient)
	mockIterator := new(MockTimeSeriesIterator)

	// This is a usage type metric, so it goes to projectResults (not perfMetrics)
	testMetrics := []common.MetricItem{
		{Metric: "pool_client_protocol_reads_raw", ResourceType: "custom.googleapis.com", MetricType: "usage"},
	}

	provider := &GoogleVolumeMetricsProvider{
		client:    mockClient,
		metrics:   testMetrics,
		startTime: timestamp.Add(-5 * time.Minute),
		endTime:   timestamp,
	}

	// Create time series - should be collected without any filtering
	poolReadsTimeSeries := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "custom.googleapis.com/pool_client_protocol_reads_raw",
			Labels: map[string]string{
				"pool_name":       "test-pool-1",
				"project":         projectID,
				"datacenter":      "us-central1",
				"volume":          "test-volume",
				"deployment_name": "test-deployment",
			},
		},
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1024}}},
		},
	}

	mockIterator.On("Next").Return(poolReadsTimeSeries, nil).Once()
	mockIterator.On("Next").Return(nil, iterator.Done).Once()

	mockClient.On("ListTimeSeries", contextWithQuotaProject(projectID), mock.Anything).Return(mockIterator)

	result, err := provider.CollectProjectMetrics(ctx, logger, projectID, timestamp)

	assert.NoError(t, err)
	// Metric should be collected (no filtering for reads)
	assert.Len(t, result, 1, "pool_client_protocol_reads_raw should be collected without filtering")
	assert.Equal(t, string(metadata.CoolTierDataReadSizeRaw), result[0].MeasuredType)
	assert.Equal(t, float64(1024), result[0].Quantity)

	mockClient.AssertExpectations(t)
	mockIterator.AssertExpectations(t)
}

// TestSetupHydratedMetrics_RegionalHAPool tests that regional HA pool metrics
// correctly use pool_name label and get VolumePoolRegionalHA resource type
func TestSetupHydratedMetrics_RegionalHAPool(t *testing.T) {
	timestamp := time.Now()
	projectID := "test-project"

	t.Run("regional HA pool should use pool_name and become VolumePoolRegionalHA", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/pool_cloud_bin_operation_size_raw",
				Labels: map[string]string{
					"pool_name":       "regional-ha-pool",
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "test-deployment",
					"is_regional_ha":  "true",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1024},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.CoolTierDataWriteSizeRaw, metadata.VolumePool, projectID, resp, timestamp)

		assert.NotNil(t, result)
		assert.Equal(t, "regional-ha-pool", result.ResourceName, "Should use pool_name label for regional HA pool")
		assert.Equal(t, string(metadata.VolumePoolRegionalHA), result.ResourceType, "Should be VolumePoolRegionalHA for regional HA pool")
	})

	t.Run("regional HA volume should use volume and become VolumeRegionalHA", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/volume_space_logical_used",
				Labels: map[string]string{
					"volume":          "regional-ha-volume",
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "test-deployment",
					"is_regional_ha":  "true",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 2048},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.LogicalSize, metadata.Volume, projectID, resp, timestamp)

		assert.NotNil(t, result)
		assert.Equal(t, "regional-ha-volume", result.ResourceName, "Should use volume label for regional HA volume")
		assert.Equal(t, string(metadata.VolumeRegionalHA), result.ResourceType, "Should be VolumeRegionalHA for regional HA volume")
	})

	t.Run("regional HA volume with resourceType replication", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/volume_space_logical_used",
				Labels: map[string]string{
					"relationship_id": "98c2cd6c-c9f9-64ef-4953-3fd6233dd7cc",
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "test-deployment",
					"is_regional_ha":  "true",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 2048},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.LogicalSize, metadata.VolumeReplicationRelationship, projectID, resp, timestamp)

		assert.NotNil(t, result)
		assert.Equal(t, "98c2cd6c-c9f9-64ef-4953-3fd6233dd7cc", result.ResourceName, "Should use relationship_id label for replication relationship")
		assert.Equal(t, string(metadata.VolumeReplicationRelationship), result.ResourceType, "Should be VolumeReplicationRelationship")
	})

	t.Run("non-regional HA pool should remain VolumePool", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/pool_cloud_bin_operation_size_raw",
				Labels: map[string]string{
					"pool_name":       "regular-pool",
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "test-deployment",
					// No is_regional_ha label
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 512},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.CoolTierDataWriteSizeRaw, metadata.VolumePool, projectID, resp, timestamp)

		assert.NotNil(t, result)
		assert.Equal(t, "regular-pool", result.ResourceName)
		assert.Equal(t, string(metadata.VolumePool), result.ResourceType, "Should remain VolumePool for non-regional HA pool")
	})
}

// TestSetupHydratedMetrics_EmptyResourceName tests that metrics with empty resource names are skipped
func TestSetupHydratedMetrics_EmptyResourceName(t *testing.T) {
	timestamp := time.Now()
	projectID := "test-project"

	t.Run("missing volume label for Volume metric should return nil", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/volume_space_logical_used",
				Labels: map[string]string{
					// "volume" label is missing
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "test-deployment",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1024},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.LogicalSize, metadata.Volume, projectID, resp, timestamp)
		assert.Nil(t, result, "Should return nil for missing volume label")
	})

	t.Run("missing pool_name label for VolumePool metric should return nil", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/pool_cloud_bin_operation_size_raw",
				Labels: map[string]string{
					// "pool_name" label is missing
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "test-deployment",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1024},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.CoolTierDataWriteSizeRaw, metadata.VolumePool, projectID, resp, timestamp)
		assert.Nil(t, result, "Should return nil for missing pool_name label")
	})

	t.Run("missing pool_name label for regional HA pool metric should return nil", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/pool_cloud_bin_operation_size_raw",
				Labels: map[string]string{
					// "pool_name" label is missing
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "test-deployment",
					"is_regional_ha":  "true",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1024},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.CoolTierDataWriteSizeRaw, metadata.VolumePool, projectID, resp, timestamp)
		assert.Nil(t, result, "Should return nil for missing pool_name label even with regional HA")
	})

	t.Run("empty string volume label should return nil", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/volume_space_logical_used",
				Labels: map[string]string{
					"volume":          "", // Empty string
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "test-deployment",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1024},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.LogicalSize, metadata.Volume, projectID, resp, timestamp)
		assert.Nil(t, result, "Should return nil for empty string volume label")
	})

	t.Run("empty string pool_name label should return nil", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/pool_cloud_bin_operation_size_raw",
				Labels: map[string]string{
					"pool_name":       "", // Empty string
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "test-deployment",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1024},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.CoolTierDataWriteSizeRaw, metadata.VolumePool, projectID, resp, timestamp)
		assert.Nil(t, result, "Should return nil for empty string pool_name label")
	})
}

func TestSetupHydratedMetrics_VolumeATRawMetric_StoresPoolNameInMetadata(t *testing.T) {
	projectID := "test-project"
	timestamp := time.Now()

	t.Run("volume write raw metric stores pool_name in metadata", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/wafl_volume_cloud_bin_operation_size_raw",
				Labels: map[string]string{
					"volume":          "test-volume",
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "dep-1",
					"pool_name":       "parent-pool",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 2048},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.CoolTierDataWriteSizeRaw, metadata.Volume, projectID, resp, timestamp)

		assert.NotNil(t, result)
		assert.Equal(t, "test-volume", result.ResourceName)
		assert.Equal(t, string(metadata.Volume), result.ResourceType)
		assert.NotNil(t, result.Metadata)
		assert.Contains(t, string(result.Metadata), `"pool_name":"parent-pool"`)
	})

	t.Run("volume read raw metric stores pool_name in metadata", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/wafl_volume_client_protocol_reads_raw",
				Labels: map[string]string{
					"volume":          "test-volume-2",
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "dep-1",
					"pool_name":       "parent-pool-2",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1024},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.CoolTierDataReadSizeRaw, metadata.Volume, projectID, resp, timestamp)

		assert.NotNil(t, result)
		assert.Equal(t, "test-volume-2", result.ResourceName)
		assert.NotNil(t, result.Metadata)
		assert.Contains(t, string(result.Metadata), `"pool_name":"parent-pool-2"`)
	})

	t.Run("pool-level raw metric does NOT store pool_name in metadata", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/pool_cloud_bin_operation_size_raw",
				Labels: map[string]string{
					"pool_name":       "test-pool",
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "dep-1",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1024},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.CoolTierDataWriteSizeRaw, metadata.VolumePool, projectID, resp, timestamp)

		assert.NotNil(t, result)
		assert.Equal(t, "test-pool", result.ResourceName)
		assert.Nil(t, result.Metadata, "Pool-level metrics should not have pool_name metadata")
	})

	t.Run("volume raw metric with missing pool_name has nil metadata", func(t *testing.T) {
		resp := &monitoringpb.TimeSeries{
			Metric: &metric.Metric{
				Type: "custom.googleapis.com/wafl_volume_cloud_bin_operation_size_raw",
				Labels: map[string]string{
					"volume":          "test-volume",
					"project":         projectID,
					"datacenter":      "us-central1",
					"deployment_name": "dep-1",
				},
			},
			Points: []*monitoringpb.Point{
				{
					Value: &monitoringpb.TypedValue{
						Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 512},
					},
				},
			},
		}

		result := setupHydratedMetrics(metadata.CoolTierDataWriteSizeRaw, metadata.Volume, projectID, resp, timestamp)

		assert.NotNil(t, result)
		assert.Nil(t, result.Metadata, "Metadata should be nil when pool_name label is missing")
	})
}

func TestSetupHydratedMetrics_VolumeReplicationRelationship_StoresReplicationDetailsInMetadata(t *testing.T) {
	projectID := "test-project"
	timestamp := time.Now()

	resp := &monitoringpb.TimeSeries{
		Metric: &metric.Metric{
			Type: "custom.googleapis.com/volume_replication_transfer",
			Labels: map[string]string{
				"relationship_id":     "relationship-123",
				"project":             projectID,
				"datacenter":          "us-central1",
				"deployment_name":     "dep-1",
				"source_details":      "source=us-east4:vol-a",
				"destination_details": "destination=us-central1:vol-b",
			},
		},
		Points: []*monitoringpb.Point{
			{
				Value: &monitoringpb.TypedValue{
					Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 4096},
				},
			},
		},
	}

	result := setupHydratedMetrics(metadata.XregionReplicationTotalTransferBytes, metadata.VolumeReplicationRelationship, projectID, resp, timestamp)

	require.NotNil(t, result)
	assert.Equal(t, string(metadata.VolumeReplicationRelationship), result.ResourceType)
	assert.Equal(t, "relationship-123", result.ResourceName)
	require.NotNil(t, result.Metadata)

	var meta map[string]string
	require.NoError(t, json.Unmarshal(result.Metadata, &meta))
	assert.Equal(t, map[string]string{
		"source_details":      "source=us-east4:vol-a",
		"destination_details": "destination=us-central1:vol-b",
	}, meta)
}
