package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

const (
	backupChainHistoriesTableName          = "backup_chain_histories"
	backupChainHistoryPartitionNameLayout  = "backup_chain_histories_2006_01_02"
	backupChainHistoryForwardPartitionDays = 7
	defaultBackupChainHistoryRetentionDays = 7
)

// BackupChainHistoryRetentionDays returns how many days of tombstoned
// backup_chain_histories to keep. Values below one day are clamped to one.
func BackupChainHistoryRetentionDays() int {
	days := env.GetInt("BACKUP_CHAIN_HISTORY_RETENTION_DAYS", defaultBackupChainHistoryRetentionDays)
	if days < 1 {
		return 1
	}
	return days
}

// DeleteBackupChainHistoryOlderThan removes expired tombstones and then drops
// old daily partitions only when they are empty. Active history rows can have
// an old created_at and must never be removed by a partition-age decision.
func (d *DataStoreRepository) DeleteBackupChainHistoryOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	deleted, err := d.deleteBackupChainHistoryTombstones(ctx, olderThan)
	if err != nil {
		return deleted, err
	}
	if deleted > 0 {
		util.GetLogger(ctx).Infof("Ledger: Successfully deleted %d backup chain history entries older than %s",
			deleted, olderThan.UTC().Format(time.RFC3339))
	}

	if d.db.GORM().Dialector.Name() != "postgres" {
		return deleted, nil
	}

	partitioned, err := d.isBackupChainHistoryPartitioned(ctx)
	if err != nil {
		return deleted, fmt.Errorf("check backup_chain_histories partitioning: %w", err)
	}
	if !partitioned {
		return deleted, nil
	}

	dropped, err := d.dropEmptyBackupChainHistoryPartitionsOlderThan(ctx, olderThan)
	if err != nil {
		return deleted, err
	}
	return deleted + dropped, nil
}

func (d *DataStoreRepository) deleteBackupChainHistoryTombstones(ctx context.Context, olderThan time.Time) (int64, error) {
	db := d.db.GORM().WithContext(ctx)
	logger := util.GetLogger(ctx)
	batchSize := common.LoadConfig().PageSize
	var totalDeleted int64

	for {
		result := db.Unscoped().
			Where("deleted_at IS NOT NULL AND deleted_at < ?", olderThan).
			Limit(int(batchSize)).
			Delete(&datamodel.BackupChainHistory{})
		if result.Error != nil {
			logger.Warnf("Ledger: Failed to delete backup chain history older than %s (deleted %d rows so far): %v",
				olderThan.UTC().Format(time.RFC3339), totalDeleted, result.Error)
			return totalDeleted, result.Error
		}
		if result.RowsAffected == 0 {
			break
		}
		totalDeleted += result.RowsAffected
	}

	return totalDeleted, nil
}

func (d *DataStoreRepository) isBackupChainHistoryPartitioned(ctx context.Context) (bool, error) {
	var partitioned bool
	err := d.db.GORM().WithContext(ctx).Raw(`
		SELECT EXISTS (
			SELECT 1
			FROM pg_partitioned_table pt
			JOIN pg_class c ON c.oid = pt.partrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relname = ?
			  AND n.nspname = current_schema()
		)
	`, backupChainHistoriesTableName).Scan(&partitioned).Error
	return partitioned, err
}

func (d *DataStoreRepository) dropEmptyBackupChainHistoryPartitionsOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	if err := d.ensureBackupChainHistoryForwardPartitions(ctx); err != nil {
		return 0, err
	}

	cutoffDay := dayStartUTC(olderThan)
	var partitionNames []string
	err := d.db.GORM().WithContext(ctx).Raw(`
		SELECT c.relname
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		JOIN pg_namespace n ON n.oid = p.relnamespace
		WHERE p.relname = ?
		  AND n.nspname = current_schema()
		ORDER BY c.relname
	`, backupChainHistoriesTableName).Scan(&partitionNames).Error
	if err != nil {
		return 0, fmt.Errorf("list backup_chain_histories partitions: %w", err)
	}

	var dropped int64
	for _, name := range partitionNames {
		day, ok := parseBackupChainHistoryPartitionDay(name)
		if !ok || !day.Before(cutoffDay) {
			continue
		}

		didDrop := false
		err := d.db.GORM().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext(?))`, name).Error; err != nil {
				return fmt.Errorf("lock partition %s for cleanup: %w", name, err)
			}
			if err := tx.Exec(fmt.Sprintf(
				"LOCK TABLE %s IN ACCESS EXCLUSIVE MODE",
				quoteVCPPGIdent(name),
			)).Error; err != nil {
				return fmt.Errorf("lock partition table %s: %w", name, err)
			}

			var hasRows bool
			query := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s LIMIT 1)", quoteVCPPGIdent(name))
			if err := tx.Raw(query).Scan(&hasRows).Error; err != nil {
				return fmt.Errorf("check partition %s for rows: %w", name, err)
			}
			if hasRows {
				return nil
			}
			if err := tx.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", quoteVCPPGIdent(name))).Error; err != nil {
				return fmt.Errorf("drop empty partition %s: %w", name, err)
			}
			didDrop = true
			return nil
		})
		if err != nil {
			return dropped, err
		}
		if didDrop {
			dropped++
		}
	}
	return dropped, nil
}

func (d *DataStoreRepository) ensureBackupChainHistoryForwardPartitions(ctx context.Context) error {
	today := dayStartUTC(time.Now())
	end := today.AddDate(0, 0, backupChainHistoryForwardPartitionDays)
	err := d.db.GORM().WithContext(ctx).Exec(
		`SELECT ensure_backup_chain_histories_day_partitions(?::regclass, ?::date, ?::date)`,
		backupChainHistoriesTableName,
		today.Format("2006-01-02"),
		end.Format("2006-01-02"),
	).Error
	if err != nil {
		return fmt.Errorf("ensure backup_chain_histories forward partitions: %w", err)
	}
	return nil
}

func ensureBackupChainHistoryPartitionForTimestamp(ctx context.Context, tx *gorm.DB, timestamp time.Time) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
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
	`, backupChainHistoriesTableName).Scan(&partitioned).Error; err != nil {
		return fmt.Errorf("check backup_chain_histories partitioning before insert: %w", err)
	}
	if !partitioned {
		return nil
	}

	day := dayStartUTC(timestamp).Format("2006-01-02")
	if err := tx.WithContext(ctx).Exec(
		`SELECT ensure_backup_chain_histories_day_partitions(?::regclass, ?::date, ?::date)`,
		backupChainHistoriesTableName,
		day,
		day,
	).Error; err != nil {
		return fmt.Errorf("ensure backup_chain_histories partition for %s: %w", day, err)
	}
	return nil
}

func parseBackupChainHistoryPartitionDay(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, "backup_chain_histories_") {
		return time.Time{}, false
	}
	day, err := time.ParseInLocation(backupChainHistoryPartitionNameLayout, name, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}

func dayStartUTC(value time.Time) time.Time {
	utc := value.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

func quoteVCPPGIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
