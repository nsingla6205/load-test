package database

import (
	"context"
	"database/sql"
	"time"

	datamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	gormWrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DataStoreRepository struct {
	db *gormWrapper.Wrapper
}

func NewDataStoreRepository(db *gormWrapper.Wrapper) *DataStoreRepository {
	return &DataStoreRepository{db: db}
}

// HydratedMetrics CRUD
func (r *DataStoreRepository) CreateHydratedMetrics(ctx context.Context, m *datamodel.HydratedMetrics) error {
	return r.db.GORM().WithContext(ctx).Create(m).Error
}

func (r *DataStoreRepository) CreateHydratedMetricsBatch(ctx context.Context, metrics []datamodel.HydratedMetrics, batchSize int) error {
	if len(metrics) == 0 {
		return nil
	}

	return r.db.GORM().WithContext(ctx).CreateInBatches(metrics, batchSize).Error
}
func (r *DataStoreRepository) GetHydratedMetrics(ctx context.Context, filter map[string]interface{}) ([]datamodel.HydratedMetrics, error) {
	var result []datamodel.HydratedMetrics
	db := r.db.GORM().WithContext(ctx)

	if len(filter) > 0 {
		// Check if we have complex conditions
		if conditions, ok := filter["conditions"]; ok {
			// Process each condition
			if condArr, ok := conditions.([][]interface{}); ok {
				for _, condition := range condArr {
					if len(condition) > 0 {
						// Apply each condition to the query
						db = db.Where(condition[0], condition[1:]...)
					}
				}
			}
			// Remove the conditions key from filter
			delete(filter, "conditions")
		}

		// Handle ordering if present
		if order, ok := filter["order"]; ok {
			if orderStr, ok := order.(string); ok && orderStr != "" {
				db = db.Order(orderStr)
			}
			// Remove the order key from filter
			delete(filter, "order")
		}

		// Handle limit if present
		if limit, ok := filter["limit"]; ok {
			if limitVal, ok := limit.(int); ok && limitVal > 0 {
				db = db.Limit(limitVal)
			}
			// Remove the limit key from filter
			delete(filter, "limit")
		}

		// Apply any remaining simple filters
		if len(filter) > 0 {
			db = db.Where(filter)
		}
	}
	err := db.Find(&result).Error
	return result, err
}

func (r *DataStoreRepository) GetHydratedMetricsWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]datamodel.HydratedMetrics, error) {
	return r.getHydratedMetricsWithPagination(r.db.ApplyFilter(conditions).GORM().WithContext(ctx), pagination)
}

func (r *DataStoreRepository) getHydratedMetricsWithPagination(db *gorm.DB, pagination *dbutils.Pagination) ([]datamodel.HydratedMetrics, error) {
	var result []datamodel.HydratedMetrics

	// Apply pagination using the dbutils.Paginate scope
	err := db.Scopes(dbutils.Paginate(pagination)).Find(&result).Error
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (r *DataStoreRepository) UpdateHydratedMetrics(ctx context.Context, id string, updates map[string]interface{}) error {
	return r.db.GORM().WithContext(ctx).Model(&datamodel.HydratedMetrics{}).Where("id = ?", id).Updates(updates).Error
}

func (r *DataStoreRepository) DeleteHydratedMetrics(ctx context.Context, id string) error {
	return r.db.GORM().WithContext(ctx).Where("id = ?", id).Delete(&datamodel.HydratedMetrics{}).Error
}

// AggregatedUsage CRUD
func (r *DataStoreRepository) CreateAggregatedUsage(ctx context.Context, a *datamodel.AggregatedUsage) error {
	return r.db.GORM().WithContext(ctx).Create(a).Error
}

func (r *DataStoreRepository) CreateAggregatedUsageBatch(ctx context.Context, usages []datamodel.AggregatedUsage, batchSize int) error {
	if len(usages) == 0 {
		return nil
	}

	// Use ON CONFLICT DO NOTHING to prevent duplicate entries based on the unique constraint
	// This ensures that if the same aggregated usage record is not processed multiple times
	return r.db.GORM().WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(usages, batchSize).Error
}

func (r *DataStoreRepository) GetAggregatedUsage(ctx context.Context, filter map[string]interface{}) ([]datamodel.AggregatedUsage, error) {
	var result []datamodel.AggregatedUsage
	db := r.db.GORM().WithContext(ctx)

	if len(filter) > 0 {
		// Check if we have complex conditions
		if conditions, ok := filter["conditions"]; ok {
			// Process each condition
			if condArr, ok := conditions.([][]interface{}); ok {
				for _, condition := range condArr {
					if len(condition) > 0 {
						// Apply each condition to the query
						db = db.Where(condition[0], condition[1:]...)
					}
				}
			}
			// Remove the conditions key from filter
			delete(filter, "conditions")
		}

		// Handle ordering if present
		if order, ok := filter["order"]; ok {
			if orderStr, ok := order.(string); ok && orderStr != "" {
				db = db.Order(orderStr)
			}
			// Remove the order key from filter
			delete(filter, "order")
		}

		// Handle limit if present
		if limit, ok := filter["limit"]; ok {
			if limitVal, ok := limit.(int); ok && limitVal > 0 {
				db = db.Limit(limitVal)
			}
			// Remove the limit key from filter
			delete(filter, "limit")
		}

		// Apply any remaining simple filters
		if len(filter) > 0 {
			db = db.Where(filter)
		}
	}
	err := db.Find(&result).Error
	return result, err
}

// GetLatestAggregatedUsageForAllResources retrieves the latest aggregated usage records for all resources
// filtered by aggregation type. It uses a window function to get the most recent record per resource_uuid
// and measured_type combination.
//
// Performance optimization:
// This query is optimized with a single composite index:
// - idx_aggregated_usages_latest_for_resources: (aggregation_type, resource_uuid, measured_type, created_at DESC)
//
// This single index efficiently supports:
// - WHERE clause filtering on aggregation_type (first column enables index scan)
// - PARTITION BY resource_uuid, measured_type in the window function (columns 2-3)
// - ORDER BY created_at DESC for selecting the latest record (column 4)
//
// See migration: database/metrics/migrations/post/0002_add_indexes_for_latest_aggregated_usage.up.sql
func (r *DataStoreRepository) GetLatestAggregatedUsageForAllResources(ctx context.Context, aggregationType string, limit, offset int) ([]datamodel.AggregatedUsage, error) {
	var results []datamodel.AggregatedUsage
	query := `SELECT resource_uuid, measured_type, last_counter_value, last_transfer_type FROM (
		SELECT resource_uuid, measured_type, last_counter_value, last_transfer_type, ROW_NUMBER() OVER (
			PARTITION BY resource_uuid, measured_type 
			ORDER BY created_at DESC
		) as rn
		FROM aggregated_usages 
		WHERE aggregation_type = ? AND last_counter_value IS NOT NULL
	) ranked WHERE rn = 1
	LIMIT ? OFFSET ?`
	err := r.db.GORM().WithContext(ctx).Raw(query, aggregationType, limit, offset).Scan(&results).Error
	return results, err
}

func (r *DataStoreRepository) UpdateAggregatedUsage(ctx context.Context, id int64, updates map[string]interface{}) error {
	return r.db.GORM().WithContext(ctx).Model(&datamodel.AggregatedUsage{}).Where("id = ?", id).Updates(updates).Error
}

func (r *DataStoreRepository) DeleteAggregatedUsage(ctx context.Context, id int64) error {
	return r.db.GORM().WithContext(ctx).Where("id = ?", id).Delete(&datamodel.AggregatedUsage{}).Error
}

func (r *DataStoreRepository) DeleteJobsOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	result := r.db.GORM().WithContext(ctx).Where("finished_at < ?", olderThan).Delete(&datamodel.Job{})
	return result.RowsAffected, result.Error
}

func (r *DataStoreRepository) GetRestoreTimestamp(ctx context.Context) (*datamodel.RestoreTimestamp, error) {
	var restoreTimestamp datamodel.RestoreTimestamp
	result := r.db.GORM().WithContext(ctx).First(&restoreTimestamp)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, result.Error
	}
	return &restoreTimestamp, nil
}

// UpdateRestoreTimestamp updates the restore timestamp. If the record does not exist, it creates a new one.
// It is used to track the last processed timestamp.
func (r *DataStoreRepository) UpdateRestoreTimestamp(ctx context.Context, lastProcessedAt time.Time) error {
	var restoreTimestamp datamodel.RestoreTimestamp
	result := r.db.GORM().WithContext(ctx).First(&restoreTimestamp)
	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		return result.Error
	}
	if result.Error == gorm.ErrRecordNotFound {
		restoreTimestamp = datamodel.RestoreTimestamp{LastProcessedAt: lastProcessedAt}
		return r.db.GORM().WithContext(ctx).Create(&restoreTimestamp).Error
	}
	restoreTimestamp.LastProcessedAt = lastProcessedAt
	return r.db.GORM().WithContext(ctx).Save(&restoreTimestamp).Error
}

// GetAggregatedUsageWithPagination retrieves aggregated usage with dedicated pagination support
func (r *DataStoreRepository) GetAggregatedUsageWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]datamodel.AggregatedUsage, error) {
	return r.getAggregatedUsageWithPagination(r.db.ApplyFilter(conditions).GORM().WithContext(ctx), pagination)
}

func (r *DataStoreRepository) getAggregatedUsageWithPagination(db *gorm.DB, pagination *dbutils.Pagination) ([]datamodel.AggregatedUsage, error) {
	var result []datamodel.AggregatedUsage

	// Apply pagination using the dbutils.Paginate scope
	err := db.Scopes(dbutils.Paginate(pagination)).Find(&result).Error
	if err != nil {
		return nil, err
	}

	return result, nil
}

type (
	Storage interface {
		Connect(isAdmin bool) error
		Close() error
		HealthCheck() error
		WithTransaction(ctx context.Context, fn func(dbutils.Transaction) error) error
		Migrate(ctx context.Context) error
		Rollback(ctx context.Context) error
		DB() *gorm.DB
		SQLDB() *sql.DB
		SetupDatabase(ctx context.Context) error

		// Embed DataStore interface
		DataStore
	}

	// DataStore defines all operations
	DataStore interface {
		// HydratedMetrics CRUD
		CreateHydratedMetrics(ctx context.Context, m *datamodel.HydratedMetrics) error
		CreateHydratedMetricsBatch(ctx context.Context, metrics []datamodel.HydratedMetrics, batchSize int) error
		GetHydratedMetrics(ctx context.Context, filter map[string]interface{}) ([]datamodel.HydratedMetrics, error)
		GetHydratedMetricsWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]datamodel.HydratedMetrics, error)
		UpdateHydratedMetrics(ctx context.Context, id string, updates map[string]interface{}) error
		DeleteHydratedMetrics(ctx context.Context, id string) error
		DeleteHydratedMetricsOlderThan(ctx context.Context, olderThan time.Time) (int64, error)

		// AggregatedUsage CRUD
		CreateAggregatedUsage(ctx context.Context, a *datamodel.AggregatedUsage) error
		CreateAggregatedUsageBatch(ctx context.Context, usages []datamodel.AggregatedUsage, batchSize int) error
		GetAggregatedUsage(ctx context.Context, filter map[string]interface{}) ([]datamodel.AggregatedUsage, error)
		GetLatestAggregatedUsageForAllResources(ctx context.Context, aggregationType string, limit, offset int) ([]datamodel.AggregatedUsage, error)
		GetAggregatedUsageWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]datamodel.AggregatedUsage, error)
		UpdateAggregatedUsage(ctx context.Context, id int64, updates map[string]interface{}) error
		DeleteAggregatedUsage(ctx context.Context, id int64) error
		DeleteAggregatedUsageOlderThan(ctx context.Context, olderThan time.Time) (int64, error)
		AggregateUsageForBizOps(ctx context.Context, bizopsAggrParams *datamodel.BizOpsAggregateParams) error
		DeleteJobsOlderThan(ctx context.Context, olderThan time.Time) (int64, error)

		// RestoreTimestamp cursor for cross-region restore billing
		GetRestoreTimestamp(ctx context.Context) (*datamodel.RestoreTimestamp, error)
		UpdateRestoreTimestamp(ctx context.Context, lastProcessedAt time.Time) error
	}
)
