package aggregator

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"gorm.io/gorm"
)

// MockMetricsStorage is a mock implementation of the database2.Storage interface
type MockMetricsStorage struct{}

func (m *MockMetricsStorage) CreateHydratedMetricsBatch(ctx context.Context, metrics []datamodel.HydratedMetrics, batchSize int) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockMetricsStorage) CreateBillingGcpUsage(ctx context.Context, b *interface{}) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockMetricsStorage) GetBillingGcpUsage(ctx context.Context, filter map[string]interface{}) ([]interface{}, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockMetricsStorage) SQLDB() *sql.DB {
	// TODO implement me
	panic("implement me")
}

func (m *MockMetricsStorage) Connect(isAdmin bool) error { return nil }
func (m *MockMetricsStorage) Close() error               { return nil }
func (m *MockMetricsStorage) HealthCheck() error         { return nil }
func (m *MockMetricsStorage) WithTransaction(ctx context.Context, fn func(dbutils.Transaction) error) error {
	return nil
}
func (m *MockMetricsStorage) Migrate(ctx context.Context) error       { return nil }
func (m *MockMetricsStorage) Rollback(ctx context.Context) error      { return nil }
func (m *MockMetricsStorage) DB() *gorm.DB                            { return nil }
func (m *MockMetricsStorage) SetupDatabase(ctx context.Context) error { return nil }

// DataStore methods
func (m *MockMetricsStorage) CreateHydratedMetrics(ctx context.Context, met *datamodel.HydratedMetrics) error {
	return nil
}
func (m *MockMetricsStorage) GetHydratedMetrics(ctx context.Context, filter map[string]interface{}) ([]datamodel.HydratedMetrics, error) {
	return nil, nil
}
func (m *MockMetricsStorage) GetHydratedMetricsWithPagination(ctx context.Context, conditions [][]interface{}, keyset *dbutils.Keyset) ([]datamodel.HydratedMetrics, error) {
	return nil, nil
}
func (m *MockMetricsStorage) UpdateHydratedMetrics(ctx context.Context, id string, updates map[string]interface{}) error {
	return nil
}
func (m *MockMetricsStorage) DeleteHydratedMetrics(ctx context.Context, id string) error { return nil }
func (m *MockMetricsStorage) DeleteHydratedMetricsOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	return 0, nil
}

// AggregatedUsage CRUD
func (m *MockMetricsStorage) CreateAggregatedUsage(ctx context.Context, a *datamodel.AggregatedUsage) error {
	return nil
}

func (m *MockMetricsStorage) CreateAggregatedUsageBatch(ctx context.Context, usages []datamodel.AggregatedUsage, batchSize int) error {
	return nil
}
func (m *MockMetricsStorage) GetAggregatedUsage(ctx context.Context, filter map[string]interface{}) ([]datamodel.AggregatedUsage, error) {
	return nil, nil
}
func (m *MockMetricsStorage) GetLatestAggregatedUsageForAllResources(ctx context.Context, aggregationType string, limit int, afterResourceUUID, afterMeasuredType string) ([]datamodel.AggregatedUsage, error) {
	return nil, nil
}
func (m *MockMetricsStorage) GetAggregatedUsageWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]datamodel.AggregatedUsage, error) {
	return nil, nil
}
func (m *MockMetricsStorage) UpdateAggregatedUsage(ctx context.Context, id int64, updates map[string]interface{}) error {
	return nil
}
func (m *MockMetricsStorage) DeleteAggregatedUsage(ctx context.Context, id int64) error { return nil }
func (m *MockMetricsStorage) DeleteAggregatedUsageOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	return 0, nil
}

func (m *MockMetricsStorage) DeleteJobsOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	return 0, nil
}

func (m *MockMetricsStorage) UpdateBillingGcpUsage(ctx context.Context, id int64, updates map[string]interface{}) error {
	return nil
}
func (m *MockMetricsStorage) DeleteBillingGcpUsage(ctx context.Context, id int64) error { return nil }
func (m *MockMetricsStorage) AggregateUsageForBizOps(ctx context.Context, bizopsAggrParams *datamodel.BizOpsAggregateParams) error {
	return nil
}
func (m *MockMetricsStorage) GetRestoreTimestamp(ctx context.Context) (*datamodel.RestoreTimestamp, error) {
	return nil, nil
}
func (m *MockMetricsStorage) UpdateRestoreTimestamp(ctx context.Context, lastProcessedAt time.Time) error {
	return nil
}
func TestCreateFilterWithConditions(t *testing.T) {
	config := &common.TelemetryConfig{}
	mockDB := &MockMetricsStorage{}
	processor := &BillingProvider{
		metricsDB: mockDB,
		config:    config,
	}
	startTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		startTime    time.Time
		endTime      time.Time
		resourceType string
		measuredType string
		expected     int // number of conditions expected
	}{
		{
			name:         "all parameters",
			startTime:    startTime,
			endTime:      endTime,
			resourceType: "VOLUME",
			measuredType: "ALLOCATED_SIZE",
			expected:     4,
		},
		{
			name:         "without resource type",
			startTime:    startTime,
			endTime:      endTime,
			resourceType: "",
			measuredType: "ALLOCATED_SIZE",
			expected:     3,
		},
		{
			name:         "without measured type",
			startTime:    startTime,
			endTime:      endTime,
			resourceType: "VOLUME",
			measuredType: "",
			expected:     3,
		},
		{
			name:         "only time range",
			startTime:    startTime,
			endTime:      endTime,
			resourceType: "",
			measuredType: "",
			expected:     2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := processor.CreateFilterWithConditions(
				tt.startTime,
				tt.endTime,
				tt.resourceType,
				tt.measuredType,
			)

			// Check we have a conditions key in the filter
			conditions, ok := filter["conditions"]
			assert.True(t, ok, "Expected 'conditions' key in filter")

			// Check we have the right number of conditions
			conditionsArr, ok := conditions.([][]interface{})
			assert.True(t, ok, "Expected conditions to be a [][]interface{}")
			assert.Equal(t, tt.expected, len(conditionsArr), "Unexpected number of conditions")
		})
	}
}

func TestCreateComplexFilter(t *testing.T) {
	config := &common.TelemetryConfig{}
	mockDB := &MockMetricsStorage{}
	processor := &BillingProvider{
		metricsDB: mockDB,
		config:    config,
	}
	startTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		options  map[string]interface{}
		expected int // number of conditions expected
	}{
		{
			name: "full options",
			options: map[string]interface{}{
				"startTime":    startTime,
				"endTime":      endTime,
				"resourceType": "VOLUME",
				"measuredType": "ALLOCATED_SIZE",
				"uuids":        []string{"uuid1", "uuid2"},
			},
			expected: 5,
		},
		{
			name: "only time range",
			options: map[string]interface{}{
				"startTime": startTime,
				"endTime":   endTime,
			},
			expected: 2,
		},
		{
			name: "only types",
			options: map[string]interface{}{
				"resourceType": "VOLUME",
				"measuredType": "ALLOCATED_SIZE",
			},
			expected: 2,
		},
		{
			name: "only uuids",
			options: map[string]interface{}{
				"uuids": []string{"uuid1", "uuid2"},
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := processor.CreateComplexFilter(tt.options)

			// Check we have a conditions key in the filter
			conditions, ok := filter["conditions"]
			assert.True(t, ok, "Expected 'conditions' key in filter")

			// Check we have the right number of conditions
			conditionsArr, ok := conditions.([][]interface{})
			assert.True(t, ok, "Expected conditions to be a [][]interface{}")
			assert.Equal(t, tt.expected, len(conditionsArr), "Unexpected number of conditions")
		})
	}
}

// TestFilterFunctionality tests that our filter functions generate the expected GORM conditions
func TestFilterFunctionality(t *testing.T) {
	// Create a processor instance to test the filter methods
	processor := &BillingProvider{}

	startTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC)

	// Test CreateFilterWithConditions
	t.Run("CreateFilterWithConditions", func(t *testing.T) {
		// Test case 1: All parameters
		filter := processor.CreateFilterWithConditions(startTime, endTime, "VOLUME", "ALLOCATED_SIZE")
		conditions, ok := filter["conditions"].([][]interface{})
		assert.True(t, ok, "Expected conditions key")
		assert.Equal(t, 4, len(conditions), "Expected 4 conditions")
		assert.Equal(t, "metric_timestamp >= ?", conditions[0][0])
		assert.Equal(t, "metric_timestamp <= ?", conditions[1][0])
		assert.Equal(t, "resource_type = ?", conditions[2][0])
		assert.Equal(t, "measured_type = ?", conditions[3][0])

		// Test case 2: Without resource type
		filter = processor.CreateFilterWithConditions(startTime, endTime, "", "ALLOCATED_SIZE")
		conditions, ok = filter["conditions"].([][]interface{})
		assert.True(t, ok, "Expected conditions key")
		assert.Equal(t, 3, len(conditions), "Expected 3 conditions")

		// Test case 3: Without measured type
		filter = processor.CreateFilterWithConditions(startTime, endTime, "VOLUME", "")
		conditions, ok = filter["conditions"].([][]interface{})
		assert.True(t, ok, "Expected conditions key")
		assert.Equal(t, 3, len(conditions), "Expected 3 conditions")

		// Test case 4: Only time range
		filter = processor.CreateFilterWithConditions(startTime, endTime, "", "")
		conditions, ok = filter["conditions"].([][]interface{})
		assert.True(t, ok, "Expected conditions key")
		assert.Equal(t, 2, len(conditions), "Expected 2 conditions")
	})

	// Test CreateFilterWithUUIDs using CreateComplexFilter
	t.Run("CreateFilterWithUUIDs", func(t *testing.T) {
		uuids := []string{"uuid1", "uuid2", "uuid3"}
		options := map[string]interface{}{
			"uuids": uuids,
		}
		filter := processor.CreateComplexFilter(options)
		conditions, ok := filter["conditions"].([][]interface{})
		assert.True(t, ok, "Expected conditions key")
		assert.Equal(t, 1, len(conditions), "Expected 1 condition")
		assert.Equal(t, "uuid in ?", conditions[0][0])
		assert.Equal(t, uuids, conditions[0][1])

		// Test with empty UUIDs
		options = map[string]interface{}{
			"uuids": []string{},
		}
		filter = processor.CreateComplexFilter(options)
		conditions, ok = filter["conditions"].([][]interface{})
		assert.True(t, ok, "Expected conditions key")
		assert.Equal(t, 0, len(conditions), "Expected 0 conditions for empty UUIDs")
	})

	// Test CreateComplexFilter
	t.Run("CreateComplexFilter", func(t *testing.T) {
		// Test case 1: All parameters with UUIDs
		options := map[string]interface{}{
			"startTime":    startTime,
			"endTime":      endTime,
			"resourceType": "VOLUME",
			"measuredType": "ALLOCATED_SIZE",
			"uuids":        []string{"uuid1", "uuid2"},
		}
		filter := processor.CreateComplexFilter(options)
		conditions, ok := filter["conditions"].([][]interface{})
		assert.True(t, ok, "Expected conditions key")
		assert.Equal(t, 5, len(conditions), "Expected 5 conditions")

		// Test case 2: Only time range
		options = map[string]interface{}{
			"startTime": startTime,
			"endTime":   endTime,
		}
		filter = processor.CreateComplexFilter(options)
		conditions, ok = filter["conditions"].([][]interface{})
		assert.True(t, ok, "Expected conditions key")
		assert.Equal(t, 2, len(conditions), "Expected 2 conditions")

		// Test case 3: Only UUIDs
		options = map[string]interface{}{
			"uuids": []string{"uuid1", "uuid2", "uuid3"},
		}
		filter = processor.CreateComplexFilter(options)
		conditions, ok = filter["conditions"].([][]interface{})
		assert.True(t, ok, "Expected conditions key")
		assert.Equal(t, 1, len(conditions), "Expected 1 condition")

		// Test case 4: Empty options
		options = map[string]interface{}{}
		filter = processor.CreateComplexFilter(options)
		conditions, ok = filter["conditions"].([][]interface{})
		assert.True(t, ok, "Expected conditions key")
		assert.Equal(t, 0, len(conditions), "Expected 0 conditions")
	})
}

// Just a simple representation of the filter creation methods
func CreateFilterWithConditions(startTime time.Time, endTime time.Time, resourceType string, measuredType string) map[string]interface{} {
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
