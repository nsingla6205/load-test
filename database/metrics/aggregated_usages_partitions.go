package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	datamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"gorm.io/gorm"
)

const (
	aggregatedUsagesTableName            = "aggregated_usages"
	aggregatedUsagesPartitionNameLayout  = "aggregated_usages_2006_01_02"
	aggregatedUsagesForwardPartitionDays = 7
	defaultAggregatedUsageRetentionDays  = 7
)

// AggregatedUsageRetentionDays returns the configured aggregated usage
// retention. Values below one day are clamped to one.
func AggregatedUsageRetentionDays() int {
	days := env.GetInt("AGGREGATED_USAGES_RETENTION_DAYS", defaultAggregatedUsageRetentionDays)
	if days < 1 {
		return 1
	}
	return days
}

// DeleteAggregatedUsageOlderThan drops complete daily partitions before the
// cutoff day on partitioned Postgres tables. Other databases retain the
// existing row-delete behavior.
func (r *DataStoreRepository) DeleteAggregatedUsageOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	if r.db.GORM().Dialector.Name() == "postgres" {
		partitioned, err := r.isAggregatedUsagesPartitioned(ctx)
		if err != nil {
			return 0, fmt.Errorf("check aggregated_usages partitioning: %w", err)
		}
		if partitioned {
			return r.dropAggregatedUsagePartitionsOlderThan(ctx, olderThan)
		}
	}

	result := r.db.GORM().WithContext(ctx).
		Where("aggregation_end < ?", olderThan).
		Delete(&datamodel.AggregatedUsage{})
	return result.RowsAffected, result.Error
}

func (r *DataStoreRepository) isAggregatedUsagesPartitioned(ctx context.Context) (bool, error) {
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
	`, aggregatedUsagesTableName).Scan(&partitioned).Error
	return partitioned, err
}

func (r *DataStoreRepository) dropAggregatedUsagePartitionsOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	if err := r.ensureAggregatedUsageForwardPartitions(ctx); err != nil {
		return 0, err
	}

	cutoffDay := time.Date(
		olderThan.UTC().Year(),
		olderThan.UTC().Month(),
		olderThan.UTC().Day(),
		0, 0, 0, 0,
		time.UTC,
	)
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
	`, aggregatedUsagesTableName).Scan(&partitionNames).Error
	if err != nil {
		return 0, fmt.Errorf("list aggregated_usages partitions: %w", err)
	}

	var dropped int64
	for _, name := range partitionNames {
		day, ok := parseAggregatedUsagePartitionDay(name)
		if !ok || !day.Before(cutoffDay) {
			continue
		}

		err := r.db.GORM().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext(?))`, name).Error; err != nil {
				return fmt.Errorf("lock aggregated_usages partition %s: %w", name, err)
			}
			return tx.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", quotePGIdent(name))).Error
		})
		if err != nil {
			return dropped, fmt.Errorf("drop aggregated_usages partition %s: %w", name, err)
		}
		dropped++
	}
	return dropped, nil
}

func (r *DataStoreRepository) ensureAggregatedUsageForwardPartitions(ctx context.Context) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	end := today.AddDate(0, 0, aggregatedUsagesForwardPartitionDays)
	err := r.db.GORM().WithContext(ctx).Exec(
		`SELECT ensure_aggregated_usages_day_partitions(?::regclass, ?::date, ?::date)`,
		aggregatedUsagesTableName,
		today.Format("2006-01-02"),
		end.Format("2006-01-02"),
	).Error
	if err != nil {
		return fmt.Errorf("ensure aggregated_usages forward partitions: %w", err)
	}
	return nil
}

func (r *DataStoreRepository) ensureAggregatedUsagePartitionsForRange(ctx context.Context, tx *gorm.DB, start, end time.Time) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return fmt.Errorf("invalid aggregated_usages partition range: start=%s end=%s", start, end)
	}

	var partitioned bool
	if err := tx.WithContext(ctx).Raw(`
		SELECT EXISTS (
			SELECT 1
			FROM pg_partitioned_table pt
			JOIN pg_class c ON c.oid = pt.partrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relname = ?
			  AND n.nspname = current_schema()
		)
	`, aggregatedUsagesTableName).Scan(&partitioned).Error; err != nil {
		return fmt.Errorf("check aggregated_usages partitioning before insert: %w", err)
	}
	if !partitioned {
		return nil
	}

	startDay := start.UTC().Format("2006-01-02")
	endDay := end.UTC().Format("2006-01-02")
	if err := tx.WithContext(ctx).Exec(
		`SELECT ensure_aggregated_usages_day_partitions(?::regclass, ?::date, ?::date)`,
		aggregatedUsagesTableName,
		startDay,
		endDay,
	).Error; err != nil {
		return fmt.Errorf("ensure aggregated_usages partitions %s through %s: %w", startDay, endDay, err)
	}
	return nil
}

func parseAggregatedUsagePartitionDay(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, "aggregated_usages_") {
		return time.Time{}, false
	}
	day, err := time.ParseInLocation(aggregatedUsagesPartitionNameLayout, name, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}
