package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	datamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
)

const (
	hydratedMetricsTableName            = "hydrated_metrics"
	hydratedMetricsPartitionNameLayout  = "hydrated_metrics_2006_01_02"
	hydratedMetricsForwardPartitionDays = 7
)

// DeleteHydratedMetricsOlderThan removes hydrated_metrics data older than olderThan.
//
// On a day-wise RANGE-partitioned Postgres table this first ensures partitions
// exist through today+7, then drops whole day partitions whose day is strictly
// before olderThan's calendar day (O(1) per day). On non-partitioned tables
// (and SQLite tests) it falls back to a row DELETE.
func (r *DataStoreRepository) DeleteHydratedMetricsOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	if r.db.GORM().Dialector.Name() == "postgres" {
		partitioned, err := r.isHydratedMetricsPartitioned(ctx)
		if err != nil {
			return 0, fmt.Errorf("check hydrated_metrics partitioning: %w", err)
		}
		if partitioned {
			return r.dropHydratedMetricsPartitionsOlderThan(ctx, olderThan)
		}
	}

	result := r.db.GORM().WithContext(ctx).Where("metric_timestamp < ?", olderThan).Delete(&datamodel.HydratedMetrics{})
	return result.RowsAffected, result.Error
}

func (r *DataStoreRepository) isHydratedMetricsPartitioned(ctx context.Context) (bool, error) {
	var partitioned bool
	err := r.db.GORM().WithContext(ctx).Raw(`
		SELECT EXISTS (
			SELECT 1
			FROM pg_partitioned_table pt
			JOIN pg_class c ON c.oid = pt.partrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relname = ?
			  AND n.nspname = current_schema()
		)
	`, hydratedMetricsTableName).Scan(&partitioned).Error
	return partitioned, err
}

func (r *DataStoreRepository) dropHydratedMetricsPartitionsOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	cutoffDay := time.Date(olderThan.Year(), olderThan.Month(), olderThan.Day(), 0, 0, 0, 0, time.UTC)

	if err := r.ensureHydratedMetricsForwardPartitions(ctx); err != nil {
		return 0, err
	}

	var partitionNames []string
	err := r.db.GORM().WithContext(ctx).Raw(`
		SELECT c.relname
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		JOIN pg_namespace n ON n.oid = p.relnamespace
		WHERE p.relname = ?
		  AND n.nspname = current_schema()
		ORDER BY c.relname
	`, hydratedMetricsTableName).Scan(&partitionNames).Error
	if err != nil {
		return 0, fmt.Errorf("list hydrated_metrics partitions: %w", err)
	}

	var dropped int64
	for _, name := range partitionNames {
		day, ok := parseHydratedMetricsPartitionDay(name)
		if !ok {
			continue
		}
		if !day.Before(cutoffDay) {
			continue
		}
		// DROP TABLE on a partition removes it from the parent (O(1) cleanup).
		if err := r.db.GORM().WithContext(ctx).Exec(
			fmt.Sprintf("DROP TABLE IF EXISTS %s", quotePGIdent(name)),
		).Error; err != nil {
			return dropped, fmt.Errorf("drop partition %s: %w", name, err)
		}
		dropped++
	}

	return dropped, nil
}

func (r *DataStoreRepository) ensureHydratedMetricsForwardPartitions(ctx context.Context) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	end := today.AddDate(0, 0, hydratedMetricsForwardPartitionDays)

	// Reuse the SQL helper created by migration 0007.
	err := r.db.GORM().WithContext(ctx).Exec(
		`SELECT ensure_hydrated_metrics_day_partitions(?::regclass, ?::date, ?::date)`,
		hydratedMetricsTableName,
		today.Format("2006-01-02"),
		end.Format("2006-01-02"),
	).Error
	if err != nil {
		return fmt.Errorf("ensure hydrated_metrics forward partitions: %w", err)
	}
	return nil
}

func parseHydratedMetricsPartitionDay(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, "hydrated_metrics_") {
		return time.Time{}, false
	}
	day, err := time.ParseInLocation(hydratedMetricsPartitionNameLayout, name, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}

func quotePGIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
