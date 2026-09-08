package backgroundactivities

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metricsdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
)

func TestMetricsCleanupActivity_CleanupHydratedMetricsTableActivity_Success(t *testing.T) {
	// Setup
	mockDB := metricsdb.NewMockStorage(t)
	activity := &MetricsCleanupActivity{MetricsDB: mockDB}
	ctx := context.Background()

	// Expected cutoff time (approximately 1 day ago)
	expectedRowsDeleted := int64(100)

	// Mock the delete operation
	mockDB.On("DeleteHydratedMetricsOlderThan", mock.Anything, mock.MatchedBy(func(cutoff time.Time) bool {
		// Verify cutoff is approximately 1 day ago (within 1 minute tolerance)
		dayAgo := time.Now().AddDate(0, 0, -1)
		diff := cutoff.Sub(dayAgo).Abs()
		return diff < time.Minute
	})).Return(expectedRowsDeleted, nil)

	// Execute
	err := activity.CleanupHydratedMetricsTableActivity(ctx)

	// Assert
	assert.NoError(t, err)
	mockDB.AssertExpectations(t)
}

func TestMetricsCleanupActivity_CleanupHydratedMetricsTableActivity_Error(t *testing.T) {
	// Setup
	mockDB := metricsdb.NewMockStorage(t)
	activity := &MetricsCleanupActivity{MetricsDB: mockDB}
	ctx := context.Background()

	expectedError := errors.New("database error")

	// Mock the delete operation to return error
	mockDB.On("DeleteHydratedMetricsOlderThan", mock.Anything, mock.AnythingOfType("time.Time")).Return(int64(0), expectedError)

	// Execute
	err := activity.CleanupHydratedMetricsTableActivity(ctx)

	// Assert
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockDB.AssertExpectations(t)
}

func TestMetricsCleanupActivity_CleanupAggregatedUsageTableActivity_Success(t *testing.T) {
	t.Setenv("AGGREGATED_USAGE_RETENTION_DAYS", "30")

	// Setup
	mockDB := metricsdb.NewMockStorage(t)
	activity := &MetricsCleanupActivity{MetricsDB: mockDB}
	ctx := context.Background()

	// Expected cutoff time (approximately 30 days ago)
	expectedRowsDeleted := int64(500)

	// Mock the delete operation
	mockDB.On("DeleteAggregatedUsageOlderThan", mock.Anything, mock.MatchedBy(func(cutoff time.Time) bool {
		// Verify cutoff is approximately 30 days ago (within 1 minute tolerance)
		monthAgo := time.Now().AddDate(0, 0, -30)
		diff := cutoff.Sub(monthAgo).Abs()
		return diff < time.Minute
	})).Return(expectedRowsDeleted, nil)

	// Execute
	err := activity.CleanupAggregatedUsageTableActivity(ctx)

	// Assert
	assert.NoError(t, err)
	mockDB.AssertExpectations(t)
}

func TestMetricsCleanupActivity_CleanupAggregatedUsageTableActivity_NonPositiveRetentionUsesDefault(t *testing.T) {
	t.Setenv("AGGREGATED_USAGE_RETENTION_DAYS", "0")

	mockDB := metricsdb.NewMockStorage(t)
	activity := &MetricsCleanupActivity{MetricsDB: mockDB}
	mockDB.On("DeleteAggregatedUsageOlderThan", mock.Anything, mock.MatchedBy(func(cutoff time.Time) bool {
		defaultCutoff := time.Now().AddDate(0, 0, -30)
		return cutoff.Sub(defaultCutoff).Abs() < time.Minute
	})).Return(int64(0), nil)

	err := activity.CleanupAggregatedUsageTableActivity(context.Background())

	assert.NoError(t, err)
	mockDB.AssertExpectations(t)
}

func TestMetricsCleanupActivity_CleanupAggregatedUsageTableActivity_Error(t *testing.T) {
	// Setup
	mockDB := metricsdb.NewMockStorage(t)
	activity := &MetricsCleanupActivity{MetricsDB: mockDB}
	ctx := context.Background()

	expectedError := errors.New("database error")

	// Mock the delete operation to return error
	mockDB.On("DeleteAggregatedUsageOlderThan", mock.Anything, mock.AnythingOfType("time.Time")).Return(int64(0), expectedError)

	// Execute
	err := activity.CleanupAggregatedUsageTableActivity(ctx)

	// Assert
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockDB.AssertExpectations(t)
}

func TestMetricsCleanupActivity_CleanupJobsTableActivity_Success(t *testing.T) {
	// Setup
	mockDB := metricsdb.NewMockStorage(t)
	activity := &MetricsCleanupActivity{MetricsDB: mockDB}
	ctx := context.Background()

	// Expected cutoff time (approximately 1 day ago)
	expectedRowsDeleted := int64(50)

	// Mock the delete operation
	mockDB.On("DeleteJobsOlderThan", mock.Anything, mock.MatchedBy(func(cutoff time.Time) bool {
		// Verify cutoff is approximately 1 day ago (within 1 minute tolerance)
		dayAgo := time.Now().AddDate(0, 0, -1)
		diff := cutoff.Sub(dayAgo).Abs()
		return diff < time.Minute
	})).Return(expectedRowsDeleted, nil)

	// Execute
	err := activity.CleanupJobsTableActivity(ctx)

	// Assert
	assert.NoError(t, err)
	mockDB.AssertExpectations(t)
}

func TestMetricsCleanupActivity_CleanupJobsTableActivity_Error(t *testing.T) {
	// Setup
	mockDB := metricsdb.NewMockStorage(t)
	activity := &MetricsCleanupActivity{MetricsDB: mockDB}
	ctx := context.Background()

	expectedError := errors.New("database error")

	// Mock the delete operation to return error
	mockDB.On("DeleteJobsOlderThan", mock.Anything, mock.AnythingOfType("time.Time")).Return(int64(0), expectedError)

	// Execute
	err := activity.CleanupJobsTableActivity(ctx)

	// Assert
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockDB.AssertExpectations(t)
}

func TestMetricsCleanupActivity_CleanupBackupChainHistoryActivity_Success(t *testing.T) {
	// Setup
	mockSE := database.NewMockStorage(t)
	activity := &MetricsCleanupActivity{SE: mockSE}
	ctx := context.Background()

	// Expected cutoff time (approximately 7 days ago)
	expectedRowsDeleted := int64(25)

	// Mock the delete operation
	mockSE.On("DeleteBackupChainHistoryOlderThan", mock.Anything, mock.MatchedBy(func(cutoff time.Time) bool {
		// Verify cutoff is approximately 7 days ago (within 1 minute tolerance)
		weekAgo := time.Now().AddDate(0, 0, -7)
		diff := cutoff.Sub(weekAgo).Abs()
		return diff < time.Minute
	})).Return(expectedRowsDeleted, nil)

	// Execute
	err := activity.CleanupBackupChainHistoryActivity(ctx)

	// Assert
	assert.NoError(t, err)
	mockSE.AssertExpectations(t)
}

func TestMetricsCleanupActivity_CleanupBackupChainHistoryActivity_Error(t *testing.T) {
	// Setup
	mockSE := database.NewMockStorage(t)
	activity := &MetricsCleanupActivity{SE: mockSE}
	ctx := context.Background()

	expectedError := errors.New("database error")

	// Mock the delete operation to return error
	mockSE.On("DeleteBackupChainHistoryOlderThan", mock.Anything, mock.AnythingOfType("time.Time")).Return(int64(0), expectedError)

	// Execute
	err := activity.CleanupBackupChainHistoryActivity(ctx)

	// Assert
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockSE.AssertExpectations(t)
}

func TestMetricsCleanupActivity_CleanupBackupChainHistoryActivity_ZeroRecordsDeleted(t *testing.T) {
	// Setup
	mockSE := database.NewMockStorage(t)
	activity := &MetricsCleanupActivity{SE: mockSE}
	ctx := context.Background()

	// Mock the delete operation with zero records deleted
	mockSE.On("DeleteBackupChainHistoryOlderThan", mock.Anything, mock.MatchedBy(func(cutoff time.Time) bool {
		// Verify cutoff is approximately 7 days ago (within 1 minute tolerance)
		weekAgo := time.Now().AddDate(0, 0, -7)
		diff := cutoff.Sub(weekAgo).Abs()
		return diff < time.Minute
	})).Return(int64(0), nil)

	// Execute
	err := activity.CleanupBackupChainHistoryActivity(ctx)

	// Assert
	assert.NoError(t, err)
	mockSE.AssertExpectations(t)
}
