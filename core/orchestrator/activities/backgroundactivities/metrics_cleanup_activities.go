package backgroundactivities

import (
	"context"
	"time"

	metricsdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const defaultAggregatedUsageRetentionDays = 30

type MetricsCleanupActivity struct {
	SE        database.Storage
	MetricsDB metricsdb.Storage
}

// CleanupHydratedMetricsTableActivity removes hydrated_metrics records older than 1 day
func (m *MetricsCleanupActivity) CleanupHydratedMetricsTableActivity(ctx context.Context) error {
	logger := util.GetLogger(ctx)

	// Record start time and timestamp
	startTime := time.Now()
	logger.Infof("Starting cleanup of hydrated_metrics table - Start timestamp: %s", startTime.Format("2006-01-02 15:04:05.000 MST"))

	// Calculate cutoff time (1 day ago) and record time range for deletion
	cutoffTime := time.Now().AddDate(0, 0, -1) // 1 day ago
	logger.Infof("Cleanup configuration - Deleting records older than: %s", cutoffTime.Format("2006-01-02 15:04:05.000 MST"))

	// Perform deletion
	queryStartTime := time.Now()
	logger.Info("Executing DELETE query for hydrated_metrics table...")
	rowsAffected, err := m.MetricsDB.DeleteHydratedMetricsOlderThan(ctx, cutoffTime)
	queryDuration := time.Since(queryStartTime)
	if err != nil {
		logger.Errorf("Failed to perform cleanup of hydrated_metrics table after %v: %v", queryDuration, err)
		return err
	}

	// Log comprehensive cleanup results
	logger.Infof("Hydrated metrics table cleanup completed successfully:")
	logger.Infof("Number of Hydrated Metrics Records Deleted %d and Query execution time: %v", rowsAffected, queryDuration)

	return nil
}

// CleanupAggregatedUsageTableActivity removes aggregated_usage records older than the configured retention period.
func (m *MetricsCleanupActivity) CleanupAggregatedUsageTableActivity(ctx context.Context) error {
	logger := util.GetLogger(ctx)

	// Record start time and timestamp
	startTime := time.Now()
	logger.Infof("Starting cleanup of aggregated_usage table - Start timestamp: %s", startTime.Format("2006-01-02 15:04:05.000 MST"))

	retentionDays := env.GetInt("AGGREGATED_USAGE_RETENTION_DAYS", defaultAggregatedUsageRetentionDays)
	if retentionDays < 1 {
		logger.Warnf("AGGREGATED_USAGE_RETENTION_DAYS=%d is non-positive; using default retention of %d days", retentionDays, defaultAggregatedUsageRetentionDays)
		retentionDays = defaultAggregatedUsageRetentionDays
	}
	cutoffTime := time.Now().AddDate(0, 0, -retentionDays)
	logger.Infof("Cleanup configuration - Retaining aggregated_usage for %d days; deleting records older than: %s", retentionDays, cutoffTime.Format("2006-01-02 15:04:05.000 MST"))

	// Perform deletion
	queryStartTime := time.Now()
	logger.Info("Executing DELETE query for aggregated_usage table...")
	rowsAffected, err := m.MetricsDB.DeleteAggregatedUsageOlderThan(ctx, cutoffTime)
	queryDuration := time.Since(queryStartTime)
	if err != nil {
		logger.Errorf("Failed to perform cleanup of aggregated_usage table after %v: %v", queryDuration, err)
		return err
	}

	// Log comprehensive cleanup results
	logger.Infof("Aggregated Usage table cleanup completed successfully:")
	logger.Infof("Number of Aggregated Usage Records Deleted %d and Query execution time: %v", rowsAffected, queryDuration)
	return nil
}

// CleanupJobsTableActivity removes jobs records older than 1 day
func (m *MetricsCleanupActivity) CleanupJobsTableActivity(ctx context.Context) error {
	logger := util.GetLogger(ctx)

	// Record start time and timestamp
	startTime := time.Now()
	logger.Infof("Starting cleanup of jobs table - Start timestamp: %s", startTime.Format("2006-01-02 15:04:05.000 MST"))

	// Calculate cutoff time (1 day ago) and record time range for deletion
	cutoffTime := time.Now().AddDate(0, 0, -1) // 1 day ago
	logger.Infof("Cleanup configuration - Deleting records older than: %s", cutoffTime.Format("2006-01-02 15:04:05.000 MST"))

	// Perform deletion
	queryStartTime := time.Now()
	logger.Info("Executing DELETE query for jobs table...")
	rowsAffected, err := m.MetricsDB.DeleteJobsOlderThan(ctx, cutoffTime)
	queryDuration := time.Since(queryStartTime)
	if err != nil {
		logger.Errorf("Failed to perform cleanup of jobs table after %v: %v", queryDuration, err)
		return err
	}

	// Log comprehensive cleanup results
	logger.Infof("Jobs table cleanup completed successfully:")
	logger.Infof("Number of Jobs Records Deleted %d and Query execution time: %v", rowsAffected, queryDuration)
	return nil
}

// CleanupBackupChainHistoryActivity removes backup_chain_history records with deleted_at older than 7 days
func (m *MetricsCleanupActivity) CleanupBackupChainHistoryActivity(ctx context.Context) error {
	logger := util.GetLogger(ctx)

	// Record start time and timestamp
	startTime := time.Now()
	logger.Infof("Starting cleanup of backup_chain_history table - Start timestamp: %s", startTime.Format("2006-01-02 15:04:05.000 MST"))

	// Calculate cutoff time (7 days ago) and record time range for deletion
	cutoffTime := time.Now().AddDate(0, 0, -7) // 7 days ago
	logger.Infof("Cleanup configuration - Deleting records with deleted_at older than: %s", cutoffTime.Format("2006-01-02 15:04:05.000 MST"))

	// Perform deletion
	queryStartTime := time.Now()
	logger.Info("Executing DELETE query for backup_chain_history table...")
	rowsAffected, err := m.SE.DeleteBackupChainHistoryOlderThan(ctx, cutoffTime)
	queryDuration := time.Since(queryStartTime)
	if err != nil {
		logger.Errorf("Failed to perform cleanup of backup_chain_history table after %v: %v", queryDuration, err)
		return err
	}

	// Log comprehensive cleanup results
	logger.Infof("Backup chain history table cleanup completed successfully:")
	logger.Infof("Number of Backup Chain History Records Deleted %d and Query execution time: %v", rowsAffected, queryDuration)
	return nil
}
