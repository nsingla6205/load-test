package database

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	datamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	gormWrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupTestDataStoreRepository(t *testing.T) *DataStoreRepository {
	db, err := SetupInMemoryDB()
	require.NoError(t, err)

	// Manually create the unique constraint that was removed from GORM tags
	// This ensures duplicate prevention works in tests
	sqlDB, err := db.DB()
	require.NoError(t, err)
	_, err = sqlDB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_aggregated_usage_unique 
		ON aggregated_usages (resource_uuid, aggregation_end, aggregation_start, measured_type, resource_type)`)
	require.NoError(t, err)

	wrapper := gormWrapper.New(db)
	return NewDataStoreRepository(wrapper)
}

func TestDataStoreRepository_HydratedMetricsCRUD(t *testing.T) {
	repo := setupTestDataStoreRepository(t)
	ctx := context.Background()

	// Create multiple metrics for testing different query conditions
	metric1 := &datamodel.HydratedMetrics{
		MeasuredType: "test-type",
		ResourceType: "test-resource",
		ResourceName: "test-resource-name-1",
	}
	metric2 := &datamodel.HydratedMetrics{
		MeasuredType: "test-type-2",
		ResourceType: "test-resource",
		ResourceName: "test-resource-name-2",
	}

	// Create
	assert.NoError(t, repo.CreateHydratedMetrics(ctx, metric1))
	assert.NoError(t, repo.CreateHydratedMetrics(ctx, metric2))

	// Get by ID
	metrics, err := repo.GetHydratedMetrics(ctx, map[string]interface{}{"id": metric1.ID})
	assert.NoError(t, err)
	assert.NotEmpty(t, metrics)
	assert.Equal(t, "test-resource-name-1", metrics[0].ResourceName)

	// Test complex conditions - Query with conditions array
	complexFilter := map[string]interface{}{
		"conditions": [][]interface{}{
			{"measured_type = ?", "test-type"},
			{"resource_type = ?", "test-resource"},
		},
	}
	metrics, err = repo.GetHydratedMetrics(ctx, complexFilter)
	assert.NoError(t, err)
	assert.NotEmpty(t, metrics)
	assert.Equal(t, 1, len(metrics))
	assert.Equal(t, "test-resource-name-1", metrics[0].ResourceName)

	// Test complex conditions with empty condition array
	emptyCondFilter := map[string]interface{}{
		"conditions": [][]interface{}{{}},
	}
	metrics, err = repo.GetHydratedMetrics(ctx, emptyCondFilter)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(metrics)) // Should return all metrics

	// Update
	updates := map[string]interface{}{"measured_type": "updated-type"}
	assert.NoError(t, repo.UpdateHydratedMetrics(ctx, fmt.Sprintf("%d", metric1.ID), updates))

	// Get after update
	metrics, err = repo.GetHydratedMetrics(ctx, map[string]interface{}{"id": metric1.ID})
	assert.NoError(t, err)
	assert.Equal(t, "updated-type", metrics[0].MeasuredType)

	// Delete
	assert.NoError(t, repo.DeleteHydratedMetrics(ctx, fmt.Sprintf("%d", metric1.ID)))
	metrics, err = repo.GetHydratedMetrics(ctx, map[string]interface{}{"id": metric1.ID})
	assert.NoError(t, err)
	assert.Empty(t, metrics)
}

func TestDataStoreRepository_DeleteOlderThanInBatches(t *testing.T) {
	const oldRecordCount = metricsCleanupBatchSize + 1
	cutoff := time.Now()

	t.Run("hydrated metrics", func(t *testing.T) {
		repo := setupTestDataStoreRepository(t)
		oldMetrics := make([]datamodel.HydratedMetrics, oldRecordCount)
		for i := range oldMetrics {
			oldMetrics[i] = datamodel.HydratedMetrics{
				MetricTimestamp: cutoff.Add(-time.Hour),
				MeasuredType:    "test-measured",
				ResourceType:    "test-resource",
				Quantity:        1,
			}
		}
		newMetric := &datamodel.HydratedMetrics{
			MetricTimestamp: cutoff.Add(time.Hour),
			MeasuredType:    "test-measured",
			ResourceType:    "test-resource",
			Quantity:        1,
		}

		require.NoError(t, repo.db.GORM().CreateInBatches(&oldMetrics, 1000).Error)
		require.NoError(t, repo.CreateHydratedMetrics(context.Background(), newMetric))

		rowsAffected, err := repo.DeleteHydratedMetricsOlderThan(context.Background(), cutoff)
		require.NoError(t, err)
		assert.Equal(t, int64(oldRecordCount), rowsAffected)

		var remaining int64
		require.NoError(t, repo.db.GORM().Model(&datamodel.HydratedMetrics{}).Count(&remaining).Error)
		assert.Equal(t, int64(1), remaining)
	})

	t.Run("aggregated usage", func(t *testing.T) {
		repo := setupTestDataStoreRepository(t)
		oldUsages := make([]datamodel.AggregatedUsage, oldRecordCount)
		for i := range oldUsages {
			oldUsages[i] = datamodel.AggregatedUsage{
				ResourceUUID:     fmt.Sprintf("resource-%d", i),
				AccountID:        "test-account",
				VendorCustomerID: ptrString("test-customer"),
				AggregationStart: cutoff.Add(-2 * time.Hour),
				AggregationEnd:   cutoff.Add(-time.Hour),
				MeasuredType:     "test-measured",
				Quantity:         1,
				ResourceType:     "test-resource",
				AggregationType:  "sum",
			}
		}
		newUsage := &datamodel.AggregatedUsage{
			ResourceUUID:     "new-resource",
			AccountID:        "test-account",
			VendorCustomerID: ptrString("test-customer"),
			AggregationStart: cutoff,
			AggregationEnd:   cutoff.Add(time.Hour),
			MeasuredType:     "test-measured",
			Quantity:         1,
			ResourceType:     "test-resource",
			AggregationType:  "sum",
		}

		require.NoError(t, repo.db.GORM().CreateInBatches(&oldUsages, 1000).Error)
		require.NoError(t, repo.CreateAggregatedUsage(context.Background(), newUsage))

		rowsAffected, err := repo.DeleteAggregatedUsageOlderThan(context.Background(), cutoff)
		require.NoError(t, err)
		assert.Equal(t, int64(oldRecordCount), rowsAffected)

		var remaining int64
		require.NoError(t, repo.db.GORM().Model(&datamodel.AggregatedUsage{}).Count(&remaining).Error)
		assert.Equal(t, int64(1), remaining)
	})

	t.Run("jobs", func(t *testing.T) {
		repo := setupTestDataStoreRepository(t)
		oldJobs := make([]datamodel.Job, oldRecordCount)
		for i := range oldJobs {
			oldJobs[i] = datamodel.Job{
				TypeName:   "test-job",
				Status:     "completed",
				Queue:      "test-queue",
				FinishedAt: cutoff.Add(-time.Hour),
			}
		}
		newJob := &datamodel.Job{
			TypeName:   "test-job",
			Status:     "completed",
			Queue:      "test-queue",
			FinishedAt: cutoff.Add(time.Hour),
		}

		require.NoError(t, repo.db.GORM().CreateInBatches(&oldJobs, 1000).Error)
		require.NoError(t, repo.db.GORM().Create(newJob).Error)

		rowsAffected, err := repo.DeleteJobsOlderThan(context.Background(), cutoff)
		require.NoError(t, err)
		assert.Equal(t, int64(oldRecordCount), rowsAffected)

		var remaining int64
		require.NoError(t, repo.db.GORM().Model(&datamodel.Job{}).Count(&remaining).Error)
		assert.Equal(t, int64(1), remaining)
	})

	t.Run("returns error when the delete statement fails", func(t *testing.T) {
		repo := setupTestDataStoreRepository(t)

		rowsAffected, err := repo.deleteOlderThanInBatches(context.Background(), "missing_table", "finished_at", cutoff)
		require.Error(t, err)
		assert.Zero(t, rowsAffected)
	})

	t.Run("stops between batches when context is canceled", func(t *testing.T) {
		repo := setupTestDataStoreRepository(t)
		oldJobs := make([]datamodel.Job, oldRecordCount)
		for i := range oldJobs {
			oldJobs[i] = datamodel.Job{
				TypeName:   "test-job",
				Status:     "completed",
				Queue:      "test-queue",
				FinishedAt: cutoff.Add(-time.Hour),
			}
		}
		require.NoError(t, repo.db.GORM().CreateInBatches(&oldJobs, 1000).Error)

		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(metricsCleanupBatchPause/2, cancel)

		rowsAffected, err := repo.DeleteJobsOlderThan(ctx, cutoff)
		require.ErrorIs(t, err, context.Canceled)
		assert.Less(t, rowsAffected, int64(oldRecordCount))
	})
}

func TestDataStoreRepository_GetHydratedMetricsWithPagination(t *testing.T) {
	repo := setupTestDataStoreRepository(t)
	ctx := context.Background()
	baseTimestamp := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	metrics := []datamodel.HydratedMetrics{
		{
			MetricTimestamp: baseTimestamp,
			MeasuredType:    "allocated-size",
			ResourceType:    "volume",
			Quantity:        10,
			ResourceName:    "volume-1",
			ConsumerID:      "consumer-1",
			Location:        "us-central1",
			Metadata:        []byte(`{"pool_name":"pool-1"}`),
			DeploymentName:  "deployment-1",
		},
		{
			MetricTimestamp: baseTimestamp.Add(time.Minute),
			MeasuredType:    "allocated-size",
			ResourceType:    "volume",
			Quantity:        20,
			ResourceName:    "volume-2",
		},
		{
			MetricTimestamp: baseTimestamp.Add(time.Minute),
			MeasuredType:    "allocated-size",
			ResourceType:    "volume",
			Quantity:        30,
			ResourceName:    "volume-3",
		},
		{
			MetricTimestamp: baseTimestamp.Add(2 * time.Minute),
			MeasuredType:    "allocated-size",
			ResourceType:    "volume",
			Quantity:        40,
			ResourceName:    "volume-4",
		},
		{
			MetricTimestamp: baseTimestamp.Add(3 * time.Minute),
			MeasuredType:    "allocated-size",
			ResourceType:    "volume",
			Quantity:        50,
			ResourceName:    "volume-5",
		},
		{
			MetricTimestamp: baseTimestamp.Add(time.Minute),
			MeasuredType:    "logical-size",
			ResourceType:    "volume",
			Quantity:        60,
			ResourceName:    "excluded-volume",
		},
	}
	require.NoError(t, repo.CreateHydratedMetricsBatch(ctx, metrics, len(metrics)))

	conditions := [][]interface{}{
		{"resource_type = ?", "volume"},
		{"measured_type = ?", "allocated-size"},
	}

	firstPage, err := repo.GetHydratedMetricsWithPagination(ctx, conditions, &dbutils.Keyset{Limit: 2})
	require.NoError(t, err)
	require.Len(t, firstPage, 2)
	assert.Equal(t, []string{"volume-1", "volume-2"}, []string{firstPage[0].ResourceName, firstPage[1].ResourceName})
	assert.Equal(t, float64(10), firstPage[0].Quantity)
	assert.JSONEq(t, `{"pool_name":"pool-1"}`, string(firstPage[0].Metadata))
	assert.Equal(t, "consumer-1", firstPage[0].ConsumerID)
	assert.Equal(t, "us-central1", firstPage[0].Location)
	assert.Equal(t, "deployment-1", firstPage[0].DeploymentName)

	secondPage, err := repo.GetHydratedMetricsWithPagination(ctx, conditions, &dbutils.Keyset{
		Limit:          2,
		AfterTimestamp: firstPage[1].MetricTimestamp,
		AfterID:        firstPage[1].ID,
	})
	require.NoError(t, err)
	require.Len(t, secondPage, 2)
	assert.Equal(t, []string{"volume-3", "volume-4"}, []string{secondPage[0].ResourceName, secondPage[1].ResourceName})
	assert.Equal(t, firstPage[1].MetricTimestamp, secondPage[0].MetricTimestamp)
	assert.Greater(t, secondPage[0].ID, firstPage[1].ID)

	lastPage, err := repo.GetHydratedMetricsWithPagination(ctx, conditions, &dbutils.Keyset{
		Limit:          2,
		AfterTimestamp: secondPage[1].MetricTimestamp,
		AfterID:        secondPage[1].ID,
	})
	require.NoError(t, err)
	require.Len(t, lastPage, 1)
	assert.Equal(t, "volume-5", lastPage[0].ResourceName)

	emptyPage, err := repo.GetHydratedMetricsWithPagination(ctx, conditions, &dbutils.Keyset{
		Limit:          2,
		AfterTimestamp: lastPage[0].MetricTimestamp,
		AfterID:        lastPage[0].ID,
	})
	require.NoError(t, err)
	assert.Empty(t, emptyPage)

	defaultPage, err := repo.GetHydratedMetricsWithPagination(ctx, conditions, nil)
	require.NoError(t, err)
	assert.Len(t, defaultPage, 5)
}

func TestHydratedMetricsPageQuery_SQLShape(t *testing.T) {
	repo := setupTestDataStoreRepository(t)
	cursorTimestamp := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	keyset := &dbutils.Keyset{
		Limit:          25,
		AfterTimestamp: cursorTimestamp,
		AfterID:        42,
	}

	sql := repo.db.GORM().ToSQL(func(tx *gorm.DB) *gorm.DB {
		var result []datamodel.HydratedMetrics
		return hydratedMetricsPageQuery(
			tx.Where("resource_type = ?", "volume").Where("measured_type = ?", "allocated-size"),
			keyset,
		).Find(&result)
	})
	normalizedSQL := strings.ToLower(strings.Join(strings.Fields(sql), " "))

	assert.Contains(t, normalizedSQL, "select "+hydratedMetricsReadColumns)
	assert.Contains(t, normalizedSQL, "(metric_timestamp, id) > (")
	assert.Contains(t, normalizedSQL, "order by metric_timestamp asc, id asc")
	assert.Contains(t, normalizedSQL, "limit 25")
	assert.NotContains(t, normalizedSQL, "select *")
	assert.NotContains(t, normalizedSQL, " offset ")
}

func TestDataStoreRepository_GetHydratedMetricsWithPagination_RejectsPartialCursor(t *testing.T) {
	repo := setupTestDataStoreRepository(t)
	cursorTimestamp := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		keyset *dbutils.Keyset
	}{
		{name: "timestamp only", keyset: &dbutils.Keyset{Limit: 10, AfterTimestamp: cursorTimestamp}},
		{name: "ID only", keyset: &dbutils.Keyset{Limit: 10, AfterID: 42}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := repo.GetHydratedMetricsWithPagination(context.Background(), nil, tt.keyset)
			assert.EqualError(t, err, "keyset cursor requires both after timestamp and after ID")
		})
	}
}

func TestDataStoreRepository_AggregatedUsageCRUD(t *testing.T) {
	repo := setupTestDataStoreRepository(t)
	ctx := context.Background()

	usage := &datamodel.AggregatedUsage{
		ID:               1001,
		ResourceUUID:     "test-resource-uuid",
		AccountID:        "test-account",
		VendorCustomerID: ptrString("vendor-cust-123"),
		AggregationStart: time.Now(),
		AggregationEnd:   time.Now().Add(1 * time.Hour),
		MeasuredType:     "test-measured",
		ResourceType:     "test-resource",
		Quantity:         10.0,
		AggregationType:  "sum",
		IsBillable:       true,
		State:            datamodel.Unsubmitted,
		VolumeStyle:      "block",
		ServiceLevel:     "gold",
		ReplicationType:  "none",
		IsUnified:        false,
	}
	// Create
	assert.NoError(t, repo.CreateAggregatedUsage(ctx, usage))

	// Get
	usages, err := repo.GetAggregatedUsage(ctx, map[string]interface{}{"id": 1001})
	assert.NoError(t, err)
	assert.NotEmpty(t, usages)

	// Update
	updates := map[string]interface{}{"resource_type": "updated-resource"}
	assert.NoError(t, repo.UpdateAggregatedUsage(ctx, 1001, updates))

	// Get after update
	usages, err = repo.GetAggregatedUsage(ctx, map[string]interface{}{"id": 1001})
	assert.NoError(t, err)
	assert.Equal(t, "updated-resource", usages[0].ResourceType)

	// Delete
	assert.NoError(t, repo.DeleteAggregatedUsage(ctx, 1001))
	usages, err = repo.GetAggregatedUsage(ctx, map[string]interface{}{"id": 1001})
	assert.NoError(t, err)
	assert.Empty(t, usages)
}

func ptrString(s string) *string {
	return &s
}

func TestPtrString(t *testing.T) {
	s := "hello"
	ptr := ptrString(s)
	assert.NotNil(t, ptr)
	assert.Equal(t, "hello", *ptr)
}

// TestCreateAggregatedUsageBatch tests the batch creation functionality
func TestCreateAggregatedUsageBatch(t *testing.T) {
	repo := setupTestDataStoreRepository(t)
	ctx := context.Background()

	now := time.Now()

	// Test with empty slice
	err := repo.CreateAggregatedUsageBatch(ctx, []datamodel.AggregatedUsage{}, 10)
	assert.NoError(t, err)

	// Create multiple usage records for batch testing
	usages := []datamodel.AggregatedUsage{
		{
			ID:               1001,
			ResourceUUID:     "test-resource-uuid-1",
			AccountID:        "test-account-1",
			VendorCustomerID: ptrString("vendor-cust-123"),
			AggregationStart: now,
			AggregationEnd:   now.Add(1 * time.Hour),
			MeasuredType:     "test-measured-1",
			ResourceType:     "test-resource-1",
			Quantity:         10.0,
			AggregationType:  "sum",
			IsBillable:       true,
			State:            datamodel.Unsubmitted,
			VolumeStyle:      "block",
			ServiceLevel:     "gold",
			ReplicationType:  "none",
			IsUnified:        false,
		},
		{
			ID:               1002,
			ResourceUUID:     "test-resource-uuid-2",
			AccountID:        "test-account-2",
			VendorCustomerID: ptrString("vendor-cust-456"),
			AggregationStart: now,
			AggregationEnd:   now.Add(1 * time.Hour),
			MeasuredType:     "test-measured-2",
			ResourceType:     "test-resource-2",
			Quantity:         20.0,
			AggregationType:  "sum",
			IsBillable:       true,
			State:            datamodel.Unsubmitted,
			VolumeStyle:      "file",
			ServiceLevel:     "silver",
			ReplicationType:  "async",
			IsUnified:        true,
		},
		{
			ID:               1003,
			ResourceUUID:     "test-resource-uuid-3",
			AccountID:        "test-account-3",
			VendorCustomerID: ptrString("vendor-cust-789"),
			AggregationStart: now,
			AggregationEnd:   now.Add(1 * time.Hour),
			MeasuredType:     "test-measured-3",
			ResourceType:     "test-resource-3",
			Quantity:         30.0,
			AggregationType:  "avg",
			IsBillable:       false,
			State:            datamodel.Submitted,
			VolumeStyle:      "mixed",
			ServiceLevel:     "bronze",
			ReplicationType:  "sync",
			IsUnified:        false,
		},
	}

	// Create in batch
	err = repo.CreateAggregatedUsageBatch(ctx, usages, 2)
	assert.NoError(t, err)

	// Verify all records were created
	allUsages, err := repo.GetAggregatedUsage(ctx, map[string]interface{}{})
	assert.NoError(t, err)
	assert.Len(t, allUsages, 3)

	// Verify specific records
	usage1, err := repo.GetAggregatedUsage(ctx, map[string]interface{}{"id": 1001})
	assert.NoError(t, err)
	assert.Len(t, usage1, 1)
	assert.Equal(t, "test-resource-uuid-1", usage1[0].ResourceUUID)
	assert.Equal(t, "test-account-1", usage1[0].AccountID)

	usage2, err := repo.GetAggregatedUsage(ctx, map[string]interface{}{"id": 1002})
	assert.NoError(t, err)
	assert.Len(t, usage2, 1)
	assert.Equal(t, "test-resource-uuid-2", usage2[0].ResourceUUID)
	assert.Equal(t, "test-account-2", usage2[0].AccountID)

	usage3, err := repo.GetAggregatedUsage(ctx, map[string]interface{}{"id": 1003})
	assert.NoError(t, err)
	assert.Len(t, usage3, 1)
	assert.Equal(t, "test-resource-uuid-3", usage3[0].ResourceUUID)
	assert.Equal(t, "test-account-3", usage3[0].AccountID)
}

// TestGetHydratedMetrics_OrderAndLimit tests the order and limit functionality
func TestGetHydratedMetrics_OrderAndLimit(t *testing.T) {
	repo := setupTestDataStoreRepository(t)
	ctx := context.Background()

	// Create test metrics with different timestamps
	now := time.Now()
	metric1 := &datamodel.HydratedMetrics{
		MeasuredType:    "test-type",
		ResourceType:    "test-resource",
		ResourceName:    "resource-1",
		MetricTimestamp: now.Add(-2 * time.Hour),
		Quantity:        100.0,
	}
	metric2 := &datamodel.HydratedMetrics{
		MeasuredType:    "test-type",
		ResourceType:    "test-resource",
		ResourceName:    "resource-2",
		MetricTimestamp: now.Add(-1 * time.Hour),
		Quantity:        200.0,
	}
	metric3 := &datamodel.HydratedMetrics{
		MeasuredType:    "test-type",
		ResourceType:    "test-resource",
		ResourceName:    "resource-3",
		MetricTimestamp: now,
		Quantity:        300.0,
	}

	// Create metrics
	assert.NoError(t, repo.CreateHydratedMetrics(ctx, metric1))
	assert.NoError(t, repo.CreateHydratedMetrics(ctx, metric2))
	assert.NoError(t, repo.CreateHydratedMetrics(ctx, metric3))

	// Test with order by timestamp DESC
	filterWithOrder := map[string]interface{}{
		"measured_type": "test-type",
		"order":         "metric_timestamp DESC",
	}
	metrics, err := repo.GetHydratedMetrics(ctx, filterWithOrder)
	assert.NoError(t, err)
	assert.Len(t, metrics, 3)
	// Should be ordered newest first
	assert.Equal(t, "resource-3", metrics[0].ResourceName)
	assert.Equal(t, "resource-2", metrics[1].ResourceName)
	assert.Equal(t, "resource-1", metrics[2].ResourceName)

	// Test with order by resource_name ASC
	filterWithOrderAsc := map[string]interface{}{
		"measured_type": "test-type",
		"order":         "resource_name ASC",
	}
	metrics, err = repo.GetHydratedMetrics(ctx, filterWithOrderAsc)
	assert.NoError(t, err)
	assert.Len(t, metrics, 3)
	// Should be ordered alphabetically
	assert.Equal(t, "resource-1", metrics[0].ResourceName)
	assert.Equal(t, "resource-2", metrics[1].ResourceName)
	assert.Equal(t, "resource-3", metrics[2].ResourceName)

	// Test with limit
	filterWithLimit := map[string]interface{}{
		"measured_type": "test-type",
		"order":         "metric_timestamp DESC",
		"limit":         2,
	}
	metrics, err = repo.GetHydratedMetrics(ctx, filterWithLimit)
	assert.NoError(t, err)
	assert.Len(t, metrics, 2)
	// Should return only the 2 newest metrics
	assert.Equal(t, "resource-3", metrics[0].ResourceName)
	assert.Equal(t, "resource-2", metrics[1].ResourceName)

	// Test with zero limit (should be ignored)
	filterWithZeroLimit := map[string]interface{}{
		"measured_type": "test-type",
		"limit":         0,
	}
	metrics, err = repo.GetHydratedMetrics(ctx, filterWithZeroLimit)
	assert.NoError(t, err)
	assert.Len(t, metrics, 3) // Should return all metrics since limit 0 is ignored

	// Test with negative limit (should be ignored)
	filterWithNegativeLimit := map[string]interface{}{
		"measured_type": "test-type",
		"limit":         -1,
	}
	metrics, err = repo.GetHydratedMetrics(ctx, filterWithNegativeLimit)
	assert.NoError(t, err)
	assert.Len(t, metrics, 3) // Should return all metrics since negative limit is ignored

	// Test with empty order string (should be ignored)
	filterWithEmptyOrder := map[string]interface{}{
		"measured_type": "test-type",
		"order":         "",
	}
	metrics, err = repo.GetHydratedMetrics(ctx, filterWithEmptyOrder)
	assert.NoError(t, err)
	assert.Len(t, metrics, 3) // Should return all metrics

	// Test with complex conditions, order, and limit together
	complexFilter := map[string]interface{}{
		"conditions": [][]interface{}{
			{"measured_type = ?", "test-type"},
			{"quantity >= ?", 150.0},
		},
		"order": "quantity ASC",
		"limit": 1,
	}
	metrics, err = repo.GetHydratedMetrics(ctx, complexFilter)
	assert.NoError(t, err)
	assert.Len(t, metrics, 1)
	// Should return the metric with quantity 200.0 (smallest >= 150)
	assert.Equal(t, "resource-2", metrics[0].ResourceName)
	assert.Equal(t, 200.0, metrics[0].Quantity)
}

// TestDataStoreRepository_AggregatedUsageLastTransferType verifies that the new last_transfer_type
// column round-trips correctly through the GORM CRUD path for AggregatedUsage. Both the "value set"
// and "value nil" cases are covered, since the read path (preloadCounterValues) needs to handle both.
func TestDataStoreRepository_AggregatedUsageLastTransferType(t *testing.T) {
	repo := setupTestDataStoreRepository(t)
	ctx := context.Background()

	t.Run("non-nil LastTransferType round-trips", func(t *testing.T) {
		transferType := "update"
		counterValue := 1234.5
		usage := &datamodel.AggregatedUsage{
			ID:               3001,
			ResourceUUID:     "ltt-resource-1",
			AccountID:        "acct-1",
			VendorCustomerID: ptrString("vendor-1"),
			AggregationStart: time.Now(),
			AggregationEnd:   time.Now().Add(time.Hour),
			MeasuredType:     "XREGION_REPLICATION_TOTAL_TRANSFER_BYTES",
			ResourceType:     "VOLUME_REPLICATION_RELATIONSHIP",
			Quantity:         42,
			AggregationType:  "CounterAggregation",
			LastCounterValue: &counterValue,
			LastTransferType: &transferType,
			IsBillable:       true,
			State:            datamodel.Unsubmitted,
		}

		require.NoError(t, repo.CreateAggregatedUsage(ctx, usage))

		fetched, err := repo.GetAggregatedUsage(ctx, map[string]interface{}{"id": 3001})
		require.NoError(t, err)
		require.Len(t, fetched, 1)
		require.NotNil(t, fetched[0].LastTransferType)
		assert.Equal(t, "update", *fetched[0].LastTransferType)
		require.NotNil(t, fetched[0].LastCounterValue)
		assert.Equal(t, 1234.5, *fetched[0].LastCounterValue)
	})

	t.Run("nil LastTransferType round-trips as nil", func(t *testing.T) {
		// Models a legacy-style row (or a non-replication counter row): LastTransferType not set.
		counterValue := 99.0
		usage := &datamodel.AggregatedUsage{
			ID:               3002,
			ResourceUUID:     "ltt-resource-2",
			AccountID:        "acct-2",
			VendorCustomerID: ptrString("vendor-2"),
			AggregationStart: time.Now(),
			AggregationEnd:   time.Now().Add(time.Hour),
			MeasuredType:     "ALLOCATED_SIZE",
			ResourceType:     "VOLUME",
			Quantity:         7,
			AggregationType:  "CounterAggregation",
			LastCounterValue: &counterValue,
			IsBillable:       true,
			State:            datamodel.Unsubmitted,
		}

		require.NoError(t, repo.CreateAggregatedUsage(ctx, usage))

		fetched, err := repo.GetAggregatedUsage(ctx, map[string]interface{}{"id": 3002})
		require.NoError(t, err)
		require.Len(t, fetched, 1)
		assert.Nil(t, fetched[0].LastTransferType)
	})
}

func setupPostgresSQLMockRepository(t *testing.T) (*DataStoreRepository, sqlmock.Sqlmock) {
	t.Helper()

	dbSQL, mock, err := sqlmock.New()
	require.NoError(t, err)

	dialector := postgres.New(postgres.Config{Conn: dbSQL, PreferSimpleProtocol: true})
	gormDB, err := gorm.Open(dialector, &gorm.Config{})
	require.NoError(t, err)

	return NewDataStoreRepository(gormWrapper.New(gormDB)), mock
}

// TestDataStoreRepository_GetLatestAggregatedUsageForAllResources verifies the PostgreSQL DISTINCT ON
// query returns the expected columns and supports keyset pagination cursors.
func TestDataStoreRepository_GetLatestAggregatedUsageForAllResources(t *testing.T) {
	ctx := context.Background()

	t.Run("non-positive limit returns error without querying", func(t *testing.T) {
		repo := setupTestDataStoreRepository(t)

		results, err := repo.GetLatestAggregatedUsageForAllResources(ctx, "CounterAggregation", 0, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "limit must be positive")
		assert.Nil(t, results)
	})

	t.Run("first page without cursor", func(t *testing.T) {
		repo, mock := setupPostgresSQLMockRepository(t)
		defer func() {
			require.NoError(t, mock.ExpectationsWereMet())
		}()

		counterNew := 250.0
		updateTT := "update"
		counterOther := 500.0

		rows := sqlmock.NewRows([]string{
			"resource_uuid", "measured_type", "last_counter_value", "last_transfer_type",
		}).
			AddRow("latest-resource-1", "XREGION_REPLICATION_TOTAL_TRANSFER_BYTES", counterNew, updateTT).
			AddRow("latest-resource-2", "ALLOCATED_SIZE", counterOther, nil)

		mock.ExpectQuery(`(?s)SELECT DISTINCT ON \(resource_uuid, measured_type\)`).
			WithArgs("CounterAggregation", 100).
			WillReturnRows(rows)

		results, err := repo.GetLatestAggregatedUsageForAllResources(ctx, "CounterAggregation", 100, "", "")
		require.NoError(t, err)
		require.Len(t, results, 2)

		got := map[string]datamodel.AggregatedUsage{}
		for _, r := range results {
			got[r.ResourceUUID+"|"+r.MeasuredType] = r
		}

		r1Key := "latest-resource-1|XREGION_REPLICATION_TOTAL_TRANSFER_BYTES"
		require.Contains(t, got, r1Key)
		require.NotNil(t, got[r1Key].LastCounterValue)
		assert.Equal(t, counterNew, *got[r1Key].LastCounterValue)
		require.NotNil(t, got[r1Key].LastTransferType)
		assert.Equal(t, updateTT, *got[r1Key].LastTransferType)

		r2Key := "latest-resource-2|ALLOCATED_SIZE"
		require.Contains(t, got, r2Key)
		require.NotNil(t, got[r2Key].LastCounterValue)
		assert.Equal(t, counterOther, *got[r2Key].LastCounterValue)
		assert.Nil(t, got[r2Key].LastTransferType)
	})

	t.Run("keyset pagination with after cursor", func(t *testing.T) {
		repo, mock := setupPostgresSQLMockRepository(t)
		defer func() {
			require.NoError(t, mock.ExpectationsWereMet())
		}()

		counterValue := 900.0
		rows := sqlmock.NewRows([]string{
			"resource_uuid", "measured_type", "last_counter_value", "last_transfer_type",
		}).AddRow("latest-resource-3", "LOGICAL_SIZE", counterValue, nil)

		mock.ExpectQuery(`(?s)SELECT DISTINCT ON \(resource_uuid, measured_type\).*\(resource_uuid, measured_type\) >`).
			WithArgs("CounterAggregation", "latest-resource-1", "ALLOCATED_SIZE", 50).
			WillReturnRows(rows)

		results, err := repo.GetLatestAggregatedUsageForAllResources(
			ctx, "CounterAggregation", 50, "latest-resource-1", "ALLOCATED_SIZE")
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "latest-resource-3", results[0].ResourceUUID)
		require.NotNil(t, results[0].LastCounterValue)
		assert.Equal(t, counterValue, *results[0].LastCounterValue)
	})

	t.Run("database error is propagated", func(t *testing.T) {
		repo, mock := setupPostgresSQLMockRepository(t)
		defer func() {
			require.NoError(t, mock.ExpectationsWereMet())
		}()

		dbErr := fmt.Errorf("query failed")
		mock.ExpectQuery(`(?s)SELECT DISTINCT ON \(resource_uuid, measured_type\)`).
			WithArgs("CounterAggregation", 10).
			WillReturnError(dbErr)

		results, err := repo.GetLatestAggregatedUsageForAllResources(ctx, "CounterAggregation", 10, "", "")
		require.Error(t, err)
		assert.Nil(t, results)
		assert.ErrorIs(t, err, dbErr)
	})
}

// TestCreateAggregatedUsageBatch_DuplicatePrevention tests duplicate prevention using composite unique constraint
func TestCreateAggregatedUsageBatch_DuplicatePrevention(t *testing.T) {
	repo := setupTestDataStoreRepository(t)
	ctx := context.Background()

	now := time.Now()

	// Create a usage record
	usage := datamodel.AggregatedUsage{
		ID:               2001,
		ResourceUUID:     "duplicate-test-uuid",
		AccountID:        "duplicate-test-account",
		VendorCustomerID: ptrString("duplicate-vendor-123"),
		AggregationStart: now,
		AggregationEnd:   now.Add(1 * time.Hour),
		MeasuredType:     "test-measured-duplicate",
		ResourceType:     "test-resource-duplicate",
		Quantity:         15.0,
		AggregationType:  "sum",
		IsBillable:       true,
		State:            datamodel.Unsubmitted,
		VolumeStyle:      "block",
		ServiceLevel:     "gold",
		ReplicationType:  "none",
		IsUnified:        false,
	}

	// First batch - should succeed
	err := repo.CreateAggregatedUsageBatch(ctx, []datamodel.AggregatedUsage{usage}, 1)
	assert.NoError(t, err)

	// Verify first record was created
	usages, err := repo.GetAggregatedUsage(ctx, map[string]interface{}{"resource_uuid": "duplicate-test-uuid"})
	assert.NoError(t, err)
	assert.Len(t, usages, 1)
	assert.Equal(t, "duplicate-test-uuid", usages[0].ResourceUUID)

	// Second batch with same unique constraint fields but different ID and quantity - should be ignored due to ON CONFLICT DO NOTHING
	duplicateUsage := usage
	duplicateUsage.ID = 2002       // Different ID
	duplicateUsage.Quantity = 25.0 // Different quantity
	err = repo.CreateAggregatedUsageBatch(ctx, []datamodel.AggregatedUsage{duplicateUsage}, 1)
	assert.NoError(t, err) // Should not error, but should not insert

	// Verify still only one record exists (the original one)
	usages, err = repo.GetAggregatedUsage(ctx, map[string]interface{}{"resource_uuid": "duplicate-test-uuid"})
	assert.NoError(t, err)
	assert.Len(t, usages, 1)
	assert.Equal(t, "duplicate-test-uuid", usages[0].ResourceUUID)
	assert.Equal(t, 15.0, usages[0].Quantity)  // Original quantity, not the duplicate's quantity
	assert.Equal(t, int64(2001), usages[0].ID) // Original ID, not the duplicate's ID

	// Different resource UUID should create a new record
	differentUsage := usage
	differentUsage.ID = 2003
	differentUsage.ResourceUUID = "different-test-uuid"
	err = repo.CreateAggregatedUsageBatch(ctx, []datamodel.AggregatedUsage{differentUsage}, 1)
	assert.NoError(t, err)

	// Verify two records exist now
	allUsages, err := repo.GetAggregatedUsage(ctx, map[string]interface{}{})
	assert.NoError(t, err)
	// Filter for our test records to avoid interference from other tests
	testUsages := make([]datamodel.AggregatedUsage, 0)
	for _, u := range allUsages {
		if u.ResourceUUID == "duplicate-test-uuid" || u.ResourceUUID == "different-test-uuid" {
			testUsages = append(testUsages, u)
		}
	}
	assert.Len(t, testUsages, 2)
}
