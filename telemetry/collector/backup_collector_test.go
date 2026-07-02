package collector

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	datamodel2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

type mockBackupStorage struct {
	mock.Mock
	database.Storage
}

func (m *mockBackupStorage) GetBackupMetrics(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Backup, error) {
	args := m.Called(ctx, conditions, pagination)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*datamodel.Backup), args.Error(1)
}

// GetBackupChainMetrics routes through the mock when a test has set an explicit
// expectation (m.On("GetBackupChainMetrics", ...)); otherwise it falls back to the
// GetBackupMetrics mock so existing tests that only registered the legacy method keep
// working when they enable EnableGcbdrBackupBilling.
func (m *mockBackupStorage) GetBackupChainMetrics(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Backup, error) {
	for _, e := range m.ExpectedCalls {
		if e.Method == "GetBackupChainMetrics" {
			args := m.Called(ctx, conditions, pagination)
			if args.Get(0) == nil {
				return nil, args.Error(1)
			}
			return args.Get(0).([]*datamodel.Backup), args.Error(1)
		}
	}
	return m.GetBackupMetrics(ctx, conditions, pagination)
}

func Test_GetBackupMetrics_ReturnsMetrics(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableFilesBackupBilling: true, // Enable files backup billing to include in HydratedMetricsDataModel
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-1"},
			Name:                    "Backup1",
			VolumeUUID:              "volume-uuid-1",
			LatestLogicalBackupSize: 1024,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Account1",
				VolumeName:        "Volume1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-1"},
				Name:      "BackupVault1",
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "account-uuid-1"},
					Name:      "Account1",
				},
			},
		},
	}

	// Mock the first call to return backups, subsequent calls return empty
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.HydratedMetrics, 1)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Check metric
	assert.Equal(t, metadata.BackupLogicalSize, result.HydratedMetrics[0].MeasuredType)
	assert.Equal(t, float64(1024), result.HydratedMetrics[0].Quantity)
	assert.Equal(t, "volume-uuid-1", derefString(result.HydratedMetrics[0].Metadata.ResourceUUID))
	assert.Equal(t, metadata.Backup, result.HydratedMetrics[0].Metadata.ResourceType)
	assert.Equal(t, "volume-uuid-1", derefString(result.HydratedMetrics[0].Metadata.ResourceName))
	assert.Equal(t, "us-east-1", derefString(result.HydratedMetrics[0].Metadata.RegionName))
	assert.Equal(t, "Account1", derefString(result.HydratedMetrics[0].Metadata.AccountName))

	// Check hydrated metrics data model
	assert.Equal(t, string(metadata.BackupLogicalSize), result.HydratedMetricsDataModel[0].MeasuredType)
	assert.Equal(t, string(metadata.Backup), result.HydratedMetricsDataModel[0].ResourceType)
	assert.Equal(t, "Account1", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "volume-uuid-1", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, "us-east-1", result.HydratedMetricsDataModel[0].Location)
	assert.Equal(t, float64(1024), result.HydratedMetricsDataModel[0].Quantity)

	// Verify the type is correct
	assert.IsType(t, datamodel2.HydratedMetrics{}, result.HydratedMetricsDataModel[0])
}

func Test_GetBackupMetrics_MultipleBackups(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableFilesBackupBilling: true, // Enable files backup billing to include in HydratedMetricsDataModel
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-1"},
			Name:                    "Backup1",
			VolumeUUID:              "volume-uuid-1",
			LatestLogicalBackupSize: 1024,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Account1",
				VolumeName:        "Volume1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-1"},
				Name:      "BackupVault1",
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "account-uuid-1"},
					Name:      "Account1",
				},
			},
		},
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-2"},
			Name:                    "Backup2",
			VolumeUUID:              "volume-uuid-2",
			LatestLogicalBackupSize: 2048,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Account2",
				VolumeName:        "Volume2",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-2"},
				Name:      "BackupVault2",
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "account-uuid-2"},
					Name:      "Account2",
				},
			},
		},
	}

	// Mock the first call to return backups, subsequent calls return empty
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.HydratedMetrics, 2)
	assert.Len(t, result.HydratedMetricsDataModel, 2)

	// Check first backup metric
	assert.Equal(t, metadata.BackupLogicalSize, result.HydratedMetrics[0].MeasuredType)
	assert.Equal(t, float64(1024), result.HydratedMetrics[0].Quantity)
	assert.Equal(t, "volume-uuid-1", derefString(result.HydratedMetrics[0].Metadata.ResourceUUID))
	assert.Equal(t, "volume-uuid-1", derefString(result.HydratedMetrics[0].Metadata.ResourceName))
	assert.Equal(t, "Account1", derefString(result.HydratedMetrics[0].Metadata.AccountName))

	// Check second backup metric
	assert.Equal(t, metadata.BackupLogicalSize, result.HydratedMetrics[1].MeasuredType)
	assert.Equal(t, float64(2048), result.HydratedMetrics[1].Quantity)
	assert.Equal(t, "volume-uuid-2", derefString(result.HydratedMetrics[1].Metadata.ResourceUUID))
	assert.Equal(t, "volume-uuid-2", derefString(result.HydratedMetrics[1].Metadata.ResourceName))
	assert.Equal(t, "Account2", derefString(result.HydratedMetrics[1].Metadata.AccountName))

	// Check hydrated metrics - Backup1
	assert.Equal(t, "Account1", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "volume-uuid-1", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, string(metadata.BackupLogicalSize), result.HydratedMetricsDataModel[0].MeasuredType)
	assert.Equal(t, float64(1024), result.HydratedMetricsDataModel[0].Quantity)

	// Check hydrated metrics - Backup2
	assert.Equal(t, "Account2", result.HydratedMetricsDataModel[1].ConsumerID)
	assert.Equal(t, "volume-uuid-2", result.HydratedMetricsDataModel[1].ResourceName)
	assert.Equal(t, string(metadata.BackupLogicalSize), result.HydratedMetricsDataModel[1].MeasuredType)
	assert.Equal(t, float64(2048), result.HydratedMetricsDataModel[1].Quantity)
}

func Test_GetBackupMetrics_EmptyBackups(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	// Mock to return empty results immediately
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetrics)
	assert.Empty(t, result.HydratedMetricsDataModel)
}

func Test_GetBackupMetrics_GetBackupMetricsError(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.Error(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetrics)
	assert.Empty(t, result.HydratedMetricsDataModel)
}

func Test_GetBackupMetrics_NilAttributes(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-1"},
			Name:                    "Backup1",
			VolumeUUID:              "volume-uuid-1",
			LatestLogicalBackupSize: 1024,
			Attributes:              nil, // Nil attributes should be skipped
		},
	}

	// Mock the first call to return backups, subsequent calls return empty
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// With nil attributes, the backup should be skipped entirely
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 0)
}

func Test_GetBackupMetrics_MixedValidAndNilAttributes(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableFilesBackupBilling: true, // Enable files backup billing to include in HydratedMetricsDataModel
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-1"},
			Name:                    "Backup1",
			VolumeUUID:              "volume-uuid-1",
			LatestLogicalBackupSize: 1024,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Account1",
				VolumeName:        "Volume1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-1"},
				Name:      "BackupVault1",
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "account-uuid-1"},
					Name:      "Account1",
				},
			},
		},
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-2"},
			Name:                    "Backup2",
			VolumeUUID:              "volume-uuid-2",
			LatestLogicalBackupSize: 2048,
			Attributes:              nil, // This should be skipped
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-2"},
				Name:      "BackupVault2",
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "account-uuid-2"},
					Name:      "Account2",
				},
			},
		},
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-3"},
			Name:                    "Backup3",
			VolumeUUID:              "volume-uuid-3",
			LatestLogicalBackupSize: 4096,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Account3",
				VolumeName:        "Volume3",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-3"},
				Name:      "BackupVault3",
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "account-uuid-3"},
					Name:      "Account3",
				},
			},
		},
	}

	// Mock the first call to return backups, subsequent calls return empty
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Only 2 valid backups should be processed (the one with nil attributes should be skipped)
	assert.Len(t, result.HydratedMetrics, 2)
	assert.Len(t, result.HydratedMetricsDataModel, 2)

	// Check first valid backup metric
	assert.Equal(t, metadata.BackupLogicalSize, result.HydratedMetrics[0].MeasuredType)
	assert.Equal(t, float64(1024), result.HydratedMetrics[0].Quantity)
	assert.Equal(t, "volume-uuid-1", derefString(result.HydratedMetrics[0].Metadata.ResourceUUID))
	assert.Equal(t, "volume-uuid-1", derefString(result.HydratedMetrics[0].Metadata.ResourceName))
	assert.Equal(t, "Account1", derefString(result.HydratedMetrics[0].Metadata.AccountName))

	// Check second valid backup metric
	assert.Equal(t, metadata.BackupLogicalSize, result.HydratedMetrics[1].MeasuredType)
	assert.Equal(t, float64(4096), result.HydratedMetrics[1].Quantity)
	assert.Equal(t, "volume-uuid-3", derefString(result.HydratedMetrics[1].Metadata.ResourceUUID))
	assert.Equal(t, "volume-uuid-3", derefString(result.HydratedMetrics[1].Metadata.ResourceName))
	assert.Equal(t, "Account3", derefString(result.HydratedMetrics[1].Metadata.AccountName))

	// Check hydrated metrics - Backup1
	assert.Equal(t, "Account1", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "volume-uuid-1", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, string(metadata.BackupLogicalSize), result.HydratedMetricsDataModel[0].MeasuredType)
	assert.Equal(t, float64(1024), result.HydratedMetricsDataModel[0].Quantity)

	// Check hydrated metrics - Backup3
	assert.Equal(t, "Account3", result.HydratedMetricsDataModel[1].ConsumerID)
	assert.Equal(t, "volume-uuid-3", result.HydratedMetricsDataModel[1].ResourceName)
	assert.Equal(t, string(metadata.BackupLogicalSize), result.HydratedMetricsDataModel[1].MeasuredType)
	assert.Equal(t, float64(4096), result.HydratedMetricsDataModel[1].Quantity)
}

// Test for the assembleBackupMetadata function
func TestAssembleBackupMetadata(t *testing.T) {
	// Create test backup
	backup := &datamodel.Backup{
		VolumeUUID:              "test-volume-uuid",
		LatestLogicalBackupSize: 1024,
		Attributes: &datamodel.BackupAttributes{
			AccountIdentifier: "test-account",
			VolumeName:        "test-volume",
		},
	}

	// Create test config
	config := &common.TelemetryConfig{
		RegionName: "us-central1",
	}

	// Call the function
	resourceMetadata := assembleBackupMetadata(backup, config)

	// Assertions
	assert.Equal(t, "test-volume-uuid", derefString(resourceMetadata.ResourceUUID))
	assert.Equal(t, metadata.Backup, resourceMetadata.ResourceType)
	assert.Equal(t, int64(1024), derefInt64(resourceMetadata.SizeInBytes))
	assert.Equal(t, "us-central1", derefString(resourceMetadata.RegionName))
	assert.Equal(t, "test-volume-uuid", derefString(resourceMetadata.ResourceName))
	assert.Equal(t, "test-volume", derefString(resourceMetadata.ResourceDisplayName))
	assert.Equal(t, "test-account", derefString(resourceMetadata.AccountName))
}

func TestAssembleBackupMetadata_NilAttributes(t *testing.T) {
	// Create test backup with nil attributes
	backup := &datamodel.Backup{
		VolumeUUID:              "test-volume-uuid",
		LatestLogicalBackupSize: 1024,
		Attributes:              nil,
	}

	// Create test config
	config := &common.TelemetryConfig{
		RegionName: "us-central1",
	}

	// Call the function - this should panic due to nil attributes access
	defer func() {
		if r := recover(); r != nil {
			// Expected panic due to nil attributes access
			t.Logf("Expected panic occurred: %v", r)
		}
	}()

	resourceMetadata := assembleBackupMetadata(backup, config)

	// If we get here, the function handled nil attributes gracefully
	assert.Equal(t, "test-volume-uuid", derefString(resourceMetadata.ResourceUUID))
	assert.Equal(t, metadata.Volume, resourceMetadata.ResourceType)
	assert.Equal(t, int64(1024), derefInt64(resourceMetadata.SizeInBytes))
	assert.Equal(t, "us-central1", derefString(resourceMetadata.RegionName))
}

func derefInt64(i *int64) int64 {
	if i == nil {
		return 0
	}
	return *i
}

// Test that verifies the integration between GetBackupMetrics and setupHydratedMetricsDataModel
func TestGetBackupMetrics_HydratedMetricsDataModelIntegration(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "ap-south-1",
		EnableFilesBackupBilling: true, // Enable files backup billing to include in HydratedMetricsDataModel
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-integration"},
			Name:                    "IntegrationBackup",
			VolumeUUID:              "volume-uuid-integration",
			LatestLogicalBackupSize: 5000,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "IntegrationAccount",
				VolumeName:        "IntegrationVolume",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-integration"},
				Name:      "IntegrationBackupVault",
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "account-uuid-integration"},
					Name:      "IntegrationAccount",
				},
			},
		},
	}

	// Mock the first call to return backups, subsequent calls return empty
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify that BackupLogicalSize metric is converted to HydratedMetrics
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Find the BackupLogicalSize metric in the metrics slice
	var backupMetric *entity.HydratedMetric
	for i := range result.HydratedMetrics {
		if result.HydratedMetrics[i].MeasuredType == metadata.BackupLogicalSize {
			backupMetric = &result.HydratedMetrics[i]
			break
		}
	}
	assert.NotNil(t, backupMetric)

	// Verify the HydratedMetrics data model is correctly populated
	hmBackup := result.HydratedMetricsDataModel[0]
	assert.Equal(t, string(metadata.BackupLogicalSize), hmBackup.MeasuredType)
	assert.Equal(t, string(metadata.Backup), hmBackup.ResourceType)
	assert.Equal(t, "IntegrationAccount", hmBackup.ConsumerID)
	assert.Equal(t, "volume-uuid-integration", hmBackup.ResourceName)
	assert.Equal(t, "ap-south-1", hmBackup.Location)
	assert.Equal(t, float64(5000), hmBackup.Quantity)

	// Verify timestamp is recent (within last minute)
	timeDiff := time.Since(hmBackup.MetricTimestamp)
	assert.True(t, timeDiff < time.Minute, "Timestamp should be recent")
}

// Test that verifies backup with SAN protocol is included in HydratedMetricsDataModel even when EnableFilesBackupBilling is false
func Test_GetBackupMetrics_WithSANProtocol(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableFilesBackupBilling: false, // Disabled
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-san"},
			Name:                    "SANBackup",
			VolumeUUID:              "volume-uuid-san",
			LatestLogicalBackupSize: 2048,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "AccountSAN",
				VolumeName:        "VolumeSAN",
				Protocols:         []string{"ISCSI"}, // SAN protocol
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-san"},
				Name:      "BackupVaultSAN",
			},
		},
	}

	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.HydratedMetrics, 1)
	// Should be included because it's SAN protocol
	assert.Len(t, result.HydratedMetricsDataModel, 1)
	assert.Equal(t, "AccountSAN", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "volume-uuid-san", result.HydratedMetricsDataModel[0].ResourceName)
}

// Test that verifies backup with NAS protocol is NOT included in HydratedMetricsDataModel when EnableFilesBackupBilling is false
func Test_GetBackupMetrics_WithNASProtocol_NotIncluded(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableFilesBackupBilling: false, // Disabled
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-nas"},
			Name:                    "NASBackup",
			VolumeUUID:              "volume-uuid-nas",
			LatestLogicalBackupSize: 2048,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "AccountNAS",
				VolumeName:        "VolumeNAS",
				Protocols:         []string{"NFS"}, // NAS protocol
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-nas"},
				Name:      "BackupVaultNAS",
			},
		},
	}

	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.HydratedMetrics, 1) // Still creates the metric
	// Should NOT be included because it's NAS protocol and EnableFilesBackupBilling is false
	assert.Len(t, result.HydratedMetricsDataModel, 0)
}

// Test that verifies backup with NAS protocol IS included when EnableFilesBackupBilling is true
func Test_GetBackupMetrics_WithNASProtocol_IncludedWhenEnabled(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableFilesBackupBilling: true, // Enabled
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-nas"},
			Name:                    "NASBackup",
			VolumeUUID:              "volume-uuid-nas",
			LatestLogicalBackupSize: 2048,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "AccountNAS",
				VolumeName:        "VolumeNAS",
				Protocols:         []string{"NFS"}, // NAS protocol
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-nas"},
				Name:      "BackupVaultNAS",
			},
		},
	}

	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.HydratedMetrics, 1)
	// Should be included because EnableFilesBackupBilling is true
	assert.Len(t, result.HydratedMetricsDataModel, 1)
	assert.Equal(t, "AccountNAS", result.HydratedMetricsDataModel[0].ConsumerID)
}

// Test that verifies backup with no protocols is NOT included when EnableFilesBackupBilling is false
func Test_GetBackupMetrics_NoProtocols_NotIncluded(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableFilesBackupBilling: false, // Disabled
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-no-protocol"},
			Name:                    "NoProtocolBackup",
			VolumeUUID:              "volume-uuid-no-protocol",
			LatestLogicalBackupSize: 2048,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "AccountNoProtocol",
				VolumeName:        "VolumeNoProtocol",
				Protocols:         []string{}, // No protocols
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-no-protocol"},
				Name:      "BackupVaultNoProtocol",
			},
		},
	}

	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.HydratedMetrics, 1) // Still creates the metric
	// Should NOT be included because no protocols and EnableFilesBackupBilling is false
	assert.Len(t, result.HydratedMetricsDataModel, 0)
}

// Test that verifies mixed SAN and NAS protocols with EnableFilesBackupBilling disabled
func Test_GetBackupMetrics_MixedProtocols(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableFilesBackupBilling: false, // Disabled
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-san"},
			Name:                    "SANBackup",
			VolumeUUID:              "volume-uuid-san",
			LatestLogicalBackupSize: 1024,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "AccountSAN",
				VolumeName:        "VolumeSAN",
				Protocols:         []string{"ISCSI"}, // SAN protocol
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-san"},
				Name:      "BackupVaultSAN",
			},
		},
		{
			BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-nas"},
			Name:                    "NASBackup",
			VolumeUUID:              "volume-uuid-nas",
			LatestLogicalBackupSize: 2048,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "AccountNAS",
				VolumeName:        "VolumeNAS",
				Protocols:         []string{"NFS"}, // NAS protocol
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "vault-uuid-nas"},
				Name:      "BackupVaultNAS",
			},
		},
	}

	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
		return pagination.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.HydratedMetrics, 2) // Both create metrics
	// Only SAN protocol should be included
	assert.Len(t, result.HydratedMetricsDataModel, 1)
	assert.Equal(t, "AccountSAN", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "volume-uuid-san", result.HydratedMetricsDataModel[0].ResourceName)
}

func TestGetBackupMetrics_Skipping_Cross_Region_Backups_Billing_Metrics(t *testing.T) {
	tests := []struct {
		name                                  string
		enableCrossRegionBackupBillingMetrics bool
		backups                               []*datamodel.Backup
		expectedHydratedMetricsCount          int
		expectedDataModelMetricsCount         int
		description                           string
	}{
		{
			name:                                  "Flag disabled - skip cross-region backup billing metrics",
			enableCrossRegionBackupBillingMetrics: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-1"},
					Name:                    "CrossRegionBackup1",
					VolumeUUID:              "volume-uuid-1",
					LatestLogicalBackupSize: 1024,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "Account1",
						VolumeName:        "Volume1",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-1"},
						Name:             "BackupVault1",
						BackupVaultType:  activities.CrossRegionBackupType, // Mark as cross-region
						SourceRegionName: stringPtr("us-east-1"),
						BackupRegionName: stringPtr("us-west-1"),
						Account: &datamodel.Account{
							BaseModel: datamodel.BaseModel{UUID: "account-uuid-1"},
							Name:      "Account1",
						},
					},
				},
			},
			expectedHydratedMetricsCount:  1, // HydratedMetrics is always created
			expectedDataModelMetricsCount: 0, // HydratedMetricsDataModel should be skipped
			description:                   "Cross-region backup should create HydratedMetrics but skip HydratedMetricsDataModel",
		},
		{
			name:                                  "Flag enabled - include cross-region backup billing metrics",
			enableCrossRegionBackupBillingMetrics: true,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-2"},
					Name:                    "CrossRegionBackup2",
					VolumeUUID:              "volume-uuid-2",
					LatestLogicalBackupSize: 2048,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "Account2",
						VolumeName:        "Volume2",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-2"},
						Name:             "BackupVault2",
						BackupVaultType:  activities.CrossRegionBackupType, // Mark as cross-region
						SourceRegionName: stringPtr("us-east-1"),
						BackupRegionName: stringPtr("eu-west-1"),
						Account: &datamodel.Account{
							BaseModel: datamodel.BaseModel{UUID: "account-uuid-2"},
							Name:      "Account2",
						},
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1, // HydratedMetricsDataModel should be included
			description:                   "Cross-region backup should create both HydratedMetrics and HydratedMetricsDataModel",
		},
		{
			name:                                  "Flag disabled - same region backup billing metrics still included",
			enableCrossRegionBackupBillingMetrics: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-3"},
					Name:                    "SameRegionBackup",
					VolumeUUID:              "volume-uuid-3",
					LatestLogicalBackupSize: 3072,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "Account3",
						VolumeName:        "Volume3",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-3"},
						Name:             "BackupVault3",
						BackupVaultType:  "IN_REGION", // Not cross-region
						SourceRegionName: stringPtr("us-east-1"),
						BackupRegionName: stringPtr("us-east-1"),
						Account: &datamodel.Account{
							BaseModel: datamodel.BaseModel{UUID: "account-uuid-3"},
							Name:      "Account3",
						},
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1, // Should be included even with flag disabled
			description:                   "Same region backup should always create both metrics",
		},
		{
			name:                                  "Flag disabled - nil BackupVault should include billing metrics",
			enableCrossRegionBackupBillingMetrics: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-4"},
					Name:                    "NilVaultBackup",
					VolumeUUID:              "volume-uuid-4",
					LatestLogicalBackupSize: 4096,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "Account4",
						VolumeName:        "Volume4",
					},
					BackupVault: nil, // Nil vault - cannot determine type, so billing is included
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1, // Should be included (cannot determine cross-region)
			description:                   "Nil BackupVault should create both metrics",
		},
		{
			name:                                  "Flag disabled - non-cross-region backup with type should include billing metrics",
			enableCrossRegionBackupBillingMetrics: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-5"},
					Name:                    "StandardBackup",
					VolumeUUID:              "volume-uuid-5",
					LatestLogicalBackupSize: 5120,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "Account5",
						VolumeName:        "Volume5",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-5"},
						Name:             "BackupVault5",
						BackupVaultType:  "IN_REGION", // Not cross-region
						SourceRegionName: nil,
						BackupRegionName: stringPtr("us-west-1"),
						Account: &datamodel.Account{
							BaseModel: datamodel.BaseModel{UUID: "account-uuid-5"},
							Name:      "Account5",
						},
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1, // Should be included (not cross-region type)
			description:                   "Standard backup vault type should create both metrics",
		},
		{
			name:                                  "Flag disabled - mixed cross-region and standard backups",
			enableCrossRegionBackupBillingMetrics: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-6"},
					Name:                    "StandardBackup1",
					VolumeUUID:              "volume-uuid-6",
					LatestLogicalBackupSize: 6144,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "Account6",
						VolumeName:        "Volume6",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-6"},
						Name:             "BackupVault6",
						BackupVaultType:  "IN_REGION", // Not cross-region
						SourceRegionName: stringPtr("us-east-1"),
						BackupRegionName: stringPtr("us-east-1"),
						Account: &datamodel.Account{
							BaseModel: datamodel.BaseModel{UUID: "account-uuid-6"},
							Name:      "Account6",
						},
					},
				},
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-7"},
					Name:                    "CrossRegionBackup2",
					VolumeUUID:              "volume-uuid-7",
					LatestLogicalBackupSize: 7168,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "Account7",
						VolumeName:        "Volume7",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-7"},
						Name:             "BackupVault7",
						BackupVaultType:  activities.CrossRegionBackupType, // Cross-region
						SourceRegionName: stringPtr("us-east-1"),
						BackupRegionName: stringPtr("ap-south-1"),
						Account: &datamodel.Account{
							BaseModel: datamodel.BaseModel{UUID: "account-uuid-7"},
							Name:      "Account7",
						},
					},
				},
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-8"},
					Name:                    "StandardBackup2",
					VolumeUUID:              "volume-uuid-8",
					LatestLogicalBackupSize: 8192,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "Account8",
						VolumeName:        "Volume8",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-8"},
						Name:             "BackupVault8",
						BackupVaultType:  "IN_REGION", // Not cross-region
						SourceRegionName: stringPtr("us-west-2"),
						BackupRegionName: stringPtr("us-west-2"),
						Account: &datamodel.Account{
							BaseModel: datamodel.BaseModel{UUID: "account-uuid-8"},
							Name:      "Account8",
						},
					},
				},
			},
			expectedHydratedMetricsCount:  3, // All create HydratedMetrics
			expectedDataModelMetricsCount: 2, // Only standard backups create HydratedMetricsDataModel
			description:                   "Mixed backups should filter cross-region from HydratedMetricsDataModel",
		},
		{
			name:                                  "Flag enabled - skip cross-region backup when BackupRegionName is nil",
			enableCrossRegionBackupBillingMetrics: true,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-nil-region"},
					Name:                    "CrossRegionNilRegion",
					VolumeUUID:              "volume-uuid-nil-region",
					LatestLogicalBackupSize: 4096,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "AccountNilRegion",
						VolumeName:        "VolumeNilRegion",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-nil-region"},
						Name:             "BackupVaultNilRegion",
						BackupVaultType:  activities.CrossRegionBackupType,
						SourceRegionName: stringPtr("us-east-1"),
						BackupRegionName: nil, // Nil backup region should be skipped
						Account: &datamodel.Account{
							BaseModel: datamodel.BaseModel{UUID: "account-uuid-nil-region"},
							Name:      "AccountNilRegion",
						},
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 0,
			description:                   "Cross-region backup with nil BackupRegionName should skip HydratedMetricsDataModel even when flag is enabled",
		},
		{
			name:                                  "Flag enabled - skip cross-region backup when BackupRegionName matches current region",
			enableCrossRegionBackupBillingMetrics: true,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-same-region"},
					Name:                    "CrossRegionSameRegion",
					VolumeUUID:              "volume-uuid-same-region",
					LatestLogicalBackupSize: 5120,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "AccountSameRegion",
						VolumeName:        "VolumeSameRegion",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-same-region"},
						Name:             "BackupVaultSameRegion",
						BackupVaultType:  activities.CrossRegionBackupType,
						SourceRegionName: stringPtr("eu-west-1"),
						BackupRegionName: stringPtr("us-east-1"), // Matches config.RegionName
						Account: &datamodel.Account{
							BaseModel: datamodel.BaseModel{UUID: "account-uuid-same-region"},
							Name:      "AccountSameRegion",
						},
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 0,
			description:                   "Cross-region backup with BackupRegionName matching current region should skip HydratedMetricsDataModel",
		},
		{
			name:                                  "Flag enabled - mixed cross-region and standard backups all included",
			enableCrossRegionBackupBillingMetrics: true,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-9"},
					Name:                    "StandardBackup3",
					VolumeUUID:              "volume-uuid-9",
					LatestLogicalBackupSize: 9216,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "Account9",
						VolumeName:        "Volume9",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-9"},
						Name:             "BackupVault9",
						BackupVaultType:  "IN_REGION", // Not cross-region
						SourceRegionName: stringPtr("eu-west-1"),
						BackupRegionName: stringPtr("eu-west-1"),
						Account: &datamodel.Account{
							BaseModel: datamodel.BaseModel{UUID: "account-uuid-9"},
							Name:      "Account9",
						},
					},
				},
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-10"},
					Name:                    "CrossRegionBackup3",
					VolumeUUID:              "volume-uuid-10",
					LatestLogicalBackupSize: 10240,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "Account10",
						VolumeName:        "Volume10",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "vault-uuid-10"},
						Name:             "BackupVault10",
						BackupVaultType:  activities.CrossRegionBackupType, // Cross-region
						SourceRegionName: stringPtr("eu-west-1"),
						BackupRegionName: stringPtr("ap-south-1"), // Different from config.RegionName (us-east-1)
						Account: &datamodel.Account{
							BaseModel: datamodel.BaseModel{UUID: "account-uuid-10"},
							Name:      "Account10",
						},
					},
				},
			},
			expectedHydratedMetricsCount:  2,
			expectedDataModelMetricsCount: 2, // All backups create HydratedMetricsDataModel when flag is enabled
			description:                   "All backups should create both metrics when flag is enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockBackupStorage)
			ctx := context.Background()
			config := &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableFilesBackupBilling:              true, // Enable files backup billing to include in HydratedMetricsDataModel
				EnableCrossRegionBackupBillingMetrics: tt.enableCrossRegionBackupBillingMetrics,
			}

			// Mock the first call to return backups, subsequent calls return empty
			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
				return pagination.Offset == 0
			})).Return(tt.backups, nil)
			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
				return pagination.Offset > 0
			})).Return([]*datamodel.Backup{}, nil)

			result, err := GetBackupMetrics(ctx, m, config, time.Now())
			assert.NoError(t, err)
			assert.NotNil(t, result)

			// Verify counts
			assert.Len(t, result.HydratedMetrics, tt.expectedHydratedMetricsCount,
				"HydratedMetrics count mismatch: %s", tt.description)
			assert.Len(t, result.HydratedMetricsDataModel, tt.expectedDataModelMetricsCount,
				"HydratedMetricsDataModel count mismatch: %s", tt.description)

			// Additional validations for HydratedMetrics (should always be created)
			for i, metric := range result.HydratedMetrics {
				assert.Equal(t, metadata.BackupLogicalSize, metric.MeasuredType,
					"HydratedMetrics[%d] should have BackupLogicalSize type", i)
				assert.Equal(t, tt.backups[i].VolumeUUID, derefString(metric.Metadata.ResourceUUID),
					"HydratedMetrics[%d] should have correct VolumeUUID", i)
				assert.Equal(t, float64(tt.backups[i].LatestLogicalBackupSize), metric.Quantity,
					"HydratedMetrics[%d] should have correct quantity", i)
			}

			// Additional validations for HydratedMetricsDataModel
			if tt.expectedDataModelMetricsCount > 0 {
				for i, dataMetric := range result.HydratedMetricsDataModel {
					assert.Equal(t, string(metadata.BackupLogicalSize), dataMetric.MeasuredType,
						"HydratedMetricsDataModel[%d] should have BackupLogicalSize type", i)
					assert.Equal(t, string(metadata.Backup), dataMetric.ResourceType,
						"HydratedMetricsDataModel[%d] should have Backup resource type", i)
					assert.NotEmpty(t, dataMetric.ConsumerID,
						"HydratedMetricsDataModel[%d] should have ConsumerID", i)
					assert.NotEmpty(t, dataMetric.ResourceName,
						"HydratedMetricsDataModel[%d] should have ResourceName", i)
				}
			}
		})
	}
}

func TestGetBackupMetrics_CmekBackupBilling_SkipsAndIncludes(t *testing.T) {
	tests := []struct {
		name                          string
		enableCmekBackupBilling       bool
		backups                       []*datamodel.Backup
		expectedHydratedMetricsCount  int
		expectedDataModelMetricsCount int
		description                   string
	}{
		{
			name:                    "CMEK billing disabled - skip CMEK backup billing metrics",
			enableCmekBackupBilling: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-cmek-1"},
					Name:                    "CmekBackup1",
					VolumeUUID:              "volume-uuid-cmek-1",
					LatestLogicalBackupSize: 1024,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "AccountCmek1",
						VolumeName:        "VolumeCmek1",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-cmek-1"},
						Name:      "BackupVaultCmek1",
						CmekAttributes: &datamodel.CmekAttributes{
							KmsConfigResourcePath: stringPtr("projects/p/locations/l/keyRings/r/cryptoKeys/k"),
						},
					},
				},
			},
			expectedHydratedMetricsCount:  1, // HydratedMetrics is always created
			expectedDataModelMetricsCount: 0, // CMEK backups should be skipped when billing disabled
			description:                   "CMEK backup should skip HydratedMetricsDataModel when CMEK billing is disabled",
		},
		{
			name:                    "CMEK billing enabled - include CMEK backup billing metrics",
			enableCmekBackupBilling: true,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-cmek-2"},
					Name:                    "CmekBackup2",
					VolumeUUID:              "volume-uuid-cmek-2",
					LatestLogicalBackupSize: 2048,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "AccountCmek2",
						VolumeName:        "VolumeCmek2",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-cmek-2"},
						Name:      "BackupVaultCmek2",
						CmekAttributes: &datamodel.CmekAttributes{
							KmsConfigResourcePath: stringPtr("projects/p2/locations/l2/keyRings/r2/cryptoKeys/k2"),
						},
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1, // Included when CMEK billing is enabled
			description:                   "CMEK backup should create both metrics when CMEK billing is enabled",
		},
		{
			name:                    "CMEK billing disabled - non-CMEK backups still billed",
			enableCmekBackupBilling: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-non-cmek-1"},
					Name:                    "NonCmekBackup1",
					VolumeUUID:              "volume-uuid-non-cmek-1",
					LatestLogicalBackupSize: 4096,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "AccountNonCmek1",
						VolumeName:        "VolumeNonCmek1",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-non-cmek-1"},
						Name:      "BackupVaultNonCmek1",
						// No CmekAttributes or empty path -> treated as non-CMEK
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1, // Non-CMEK backups should still be billed
			description:                   "Non-CMEK backup should create both metrics even when CMEK billing is disabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockBackupStorage)
			ctx := context.Background()
			config := &common.TelemetryConfig{
				RegionName:               "us-east-1",
				EnableFilesBackupBilling: true, // Enable files backup billing to include in HydratedMetricsDataModel
				EnableCmekBackupBilling:  tt.enableCmekBackupBilling,
			}

			// Mock the first call to return backups, subsequent calls return empty
			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
				return pagination.Offset == 0
			})).Return(tt.backups, nil)
			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
				return pagination.Offset > 0
			})).Return([]*datamodel.Backup{}, nil)

			result, err := GetBackupMetrics(ctx, m, config, time.Now())
			assert.NoError(t, err)
			assert.NotNil(t, result)

			// Verify counts
			assert.Len(t, result.HydratedMetrics, tt.expectedHydratedMetricsCount,
				"HydratedMetrics count mismatch: %s", tt.description)
			assert.Len(t, result.HydratedMetricsDataModel, tt.expectedDataModelMetricsCount,
				"HydratedMetricsDataModel count mismatch: %s", tt.description)

			// HydratedMetrics should always be BackupLogicalSize when present
			for i, metric := range result.HydratedMetrics {
				assert.Equal(t, metadata.BackupLogicalSize, metric.MeasuredType,
					"HydratedMetrics[%d] should have BackupLogicalSize type", i)
			}
		})
	}
}

// TestGetBackupMetrics_SkipBilling_Cascade validates the skipBilling decision
// cascade in GetBackupMetrics. The billing metric (HydratedMetricsDataModel) is
// gated by five sequential checks:
//
//	Gate 1: cross-region flag disabled + cross-region vault           → skip
//	Gate 2: cross-region flag enabled  + (nil or same-region backup)  → skip
//	Gate 3: CMEK billing disabled      + CMEK vault                   → skip
//	Gate 4: expert mode billing disabled + IsExpertModeBackup=true    → skip
//	Gate 5: skipBilling=false but files billing disabled & non-SAN    → not emitted
//
// Each sub-test targets one gate and confirms that subsequent gates are not
// reached (or that all gates pass and the metric is emitted).
func TestGetBackupMetrics_SkipBilling_Cascade(t *testing.T) {
	cmekPath := "projects/p/locations/l/keyRings/r/cryptoKeys/k"

	tests := []struct {
		name   string
		config *common.TelemetryConfig
		backup *datamodel.Backup
		// expectBilling is true when we expect a HydratedMetricsDataModel entry.
		expectBilling bool
		description   string
	}{
		{
			name: "Gate1: cross-region flag disabled skips before CMEK check",
			config: &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableCrossRegionBackupBillingMetrics: false,
				EnableCmekBackupBilling:               true,
				EnableFilesBackupBilling:              true,
			},
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-g1"}, VolumeUUID: "v-g1",
				LatestLogicalBackupSize: 100,
				Attributes:              &datamodel.BackupAttributes{AccountIdentifier: "acct", VolumeName: "vol"},
				BackupVault: &datamodel.BackupVault{
					BaseModel:        datamodel.BaseModel{UUID: "bv-g1"},
					Name:             "vault",
					BackupVaultType:  activities.CrossRegionBackupType,
					BackupRegionName: stringPtr("eu-west-1"),
					CmekAttributes:   &datamodel.CmekAttributes{KmsConfigResourcePath: &cmekPath},
				},
			},
			expectBilling: false,
			description:   "Gate 1 fires; CMEK and protocol gates are never evaluated",
		},
		{
			name: "Gate2: cross-region flag enabled, nil region skips before CMEK check",
			config: &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableCrossRegionBackupBillingMetrics: true,
				EnableCmekBackupBilling:               true,
				EnableFilesBackupBilling:              true,
			},
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-g2a"}, VolumeUUID: "v-g2a",
				LatestLogicalBackupSize: 200,
				Attributes:              &datamodel.BackupAttributes{AccountIdentifier: "acct", VolumeName: "vol"},
				BackupVault: &datamodel.BackupVault{
					BaseModel:        datamodel.BaseModel{UUID: "bv-g2a"},
					Name:             "vault",
					BackupVaultType:  activities.CrossRegionBackupType,
					BackupRegionName: nil,
				},
			},
			expectBilling: false,
			description:   "Gate 2 fires (nil region); downstream gates irrelevant",
		},
		{
			name: "Gate2: cross-region flag enabled, same region skips before CMEK check",
			config: &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableCrossRegionBackupBillingMetrics: true,
				EnableCmekBackupBilling:               true,
				EnableFilesBackupBilling:              true,
			},
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-g2b"}, VolumeUUID: "v-g2b",
				LatestLogicalBackupSize: 300,
				Attributes:              &datamodel.BackupAttributes{AccountIdentifier: "acct", VolumeName: "vol"},
				BackupVault: &datamodel.BackupVault{
					BaseModel:        datamodel.BaseModel{UUID: "bv-g2b"},
					Name:             "vault",
					BackupVaultType:  activities.CrossRegionBackupType,
					BackupRegionName: stringPtr("us-east-1"),
				},
			},
			expectBilling: false,
			description:   "Gate 2 fires (region matches); downstream gates irrelevant",
		},
		{
			name: "Gate3: passes cross-region gates, CMEK billing disabled skips",
			config: &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableCrossRegionBackupBillingMetrics: false,
				EnableCmekBackupBilling:               false,
				EnableFilesBackupBilling:              true,
			},
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-g3"}, VolumeUUID: "v-g3",
				LatestLogicalBackupSize: 400,
				Attributes:              &datamodel.BackupAttributes{AccountIdentifier: "acct", VolumeName: "vol"},
				BackupVault: &datamodel.BackupVault{
					BaseModel:       datamodel.BaseModel{UUID: "bv-g3"},
					Name:            "vault",
					BackupVaultType: "IN_REGION",
					CmekAttributes:  &datamodel.CmekAttributes{KmsConfigResourcePath: &cmekPath},
				},
			},
			expectBilling: false,
			description:   "Not cross-region so gates 1/2 pass; gate 3 fires on CMEK",
		},
		{
			name: "Gate4: passes cross-region and CMEK gates, expert mode billing disabled skips",
			config: &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableCrossRegionBackupBillingMetrics: false,
				EnableCmekBackupBilling:               true,
				EnableExpertModeBackupBilling:         false,
				EnableFilesBackupBilling:              true,
			},
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-g4em"}, VolumeUUID: "v-g4em",
				LatestLogicalBackupSize: 450,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "acct", VolumeName: "vol",
					IsExpertModeBackup: true,
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:       datamodel.BaseModel{UUID: "bv-g4em"},
					Name:            "vault",
					BackupVaultType: "IN_REGION",
				},
			},
			expectBilling: false,
			description:   "Not cross-region/CMEK so gates 1-3 pass; gate 4 fires on expert mode",
		},
		{
			name: "Gate5: all skip gates pass, but files billing disabled and NAS protocol blocks emission",
			config: &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableCrossRegionBackupBillingMetrics: false,
				EnableCmekBackupBilling:               true,
				EnableExpertModeBackupBilling:         true,
				EnableFilesBackupBilling:              false,
			},
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-g5"}, VolumeUUID: "v-g5",
				LatestLogicalBackupSize: 500,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "acct", VolumeName: "vol",
					Protocols: []string{"NFS"},
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:       datamodel.BaseModel{UUID: "bv-g5"},
					Name:            "vault",
					BackupVaultType: "IN_REGION",
				},
			},
			expectBilling: false,
			description:   "skipBilling=false but final protocol/files gate blocks NAS when files billing disabled",
		},
		{
			name: "Gate5: skipBilling false, files billing disabled but SAN protocol passes",
			config: &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableCrossRegionBackupBillingMetrics: false,
				EnableCmekBackupBilling:               true,
				EnableExpertModeBackupBilling:         true,
				EnableFilesBackupBilling:              false,
			},
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-g5san"}, VolumeUUID: "v-g5san",
				LatestLogicalBackupSize: 600,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "acct", VolumeName: "vol",
					Protocols: []string{"ISCSI"},
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:       datamodel.BaseModel{UUID: "bv-g5san"},
					Name:            "vault",
					BackupVaultType: "IN_REGION",
				},
			},
			expectBilling: true,
			description:   "skipBilling=false and SAN protocol passes final gate even with files billing disabled",
		},
		{
			name: "All gates pass: cross-region different region + no CMEK + expert mode enabled + files billing enabled",
			config: &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableCrossRegionBackupBillingMetrics: true,
				EnableCmekBackupBilling:               true,
				EnableExpertModeBackupBilling:         true,
				EnableFilesBackupBilling:              true,
			},
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-all"}, VolumeUUID: "v-all",
				LatestLogicalBackupSize: 700,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "acct", VolumeName: "vol",
					Protocols: []string{"NFS"},
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:        datamodel.BaseModel{UUID: "bv-all"},
					Name:             "vault",
					BackupVaultType:  activities.CrossRegionBackupType,
					BackupRegionName: stringPtr("eu-west-1"),
				},
			},
			expectBilling: true,
			description:   "Every gate passes; billing metric emitted",
		},
		{
			name: "All gates pass: in-region non-CMEK non-expert with files billing enabled",
			config: &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableCrossRegionBackupBillingMetrics: false,
				EnableCmekBackupBilling:               false,
				EnableExpertModeBackupBilling:         false,
				EnableFilesBackupBilling:              true,
			},
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-std"}, VolumeUUID: "v-std",
				LatestLogicalBackupSize: 800,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "acct", VolumeName: "vol",
					Protocols:          []string{"NFS"},
					IsExpertModeBackup: false,
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:       datamodel.BaseModel{UUID: "bv-std"},
					Name:            "vault",
					BackupVaultType: "IN_REGION",
				},
			},
			expectBilling: true,
			description:   "Standard in-region non-expert backup passes all gates",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockBackupStorage)
			ctx := context.Background()

			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
				return p.Offset == 0
			})).Return([]*datamodel.Backup{tt.backup}, nil)
			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
				return p.Offset > 0
			})).Return([]*datamodel.Backup{}, nil)

			result, err := GetBackupMetrics(ctx, m, tt.config, time.Now())
			assert.NoError(t, err)
			assert.NotNil(t, result)

			// HydratedMetrics (observability) should always be emitted regardless of billing skip.
			assert.Len(t, result.HydratedMetrics, 1, "%s: observability metric must always be present", tt.description)
			assert.Equal(t, metadata.BackupLogicalSize, result.HydratedMetrics[0].MeasuredType)

			if tt.expectBilling {
				assert.Len(t, result.HydratedMetricsDataModel, 1,
					"%s: billing metric should be emitted", tt.description)
				assert.Equal(t, string(metadata.BackupLogicalSize), result.HydratedMetricsDataModel[0].MeasuredType)
				assert.Equal(t, float64(tt.backup.LatestLogicalBackupSize), result.HydratedMetricsDataModel[0].Quantity)
			} else {
				assert.Empty(t, result.HydratedMetricsDataModel,
					"%s: billing metric should NOT be emitted", tt.description)
			}
		})
	}
}

func TestGetBackupMetrics_CrossRegionTransferBytes(t *testing.T) {
	tests := []struct {
		name                       string
		backup                     *datamodel.Backup
		enableCRBBilling           bool
		regionName                 string
		expectedTransferBytesCount int
		description                string
	}{
		{
			name:             "emits CbsCrossRegionVolumeBackupTransferBytes when conditions met",
			enableCRBBilling: true,
			regionName:       "us-east-1",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-crb"}, VolumeUUID: "v-crb",
				LatestLogicalBackupSize: 1024,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier:  "acct-crb",
					VolumeName:         "vol-crb",
					TotalTransferBytes: 5000,
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:        datamodel.BaseModel{UUID: "bv-crb"},
					Name:             "vault-crb",
					BackupVaultType:  activities.CrossRegionBackupType,
					BackupRegionName: stringPtr("eu-west-1"),
				},
			},
			expectedTransferBytesCount: 1,
			description:                "cross-region backup with transfer bytes > 0 and different region should emit transfer metric",
		},
		{
			name:             "skips when BackupRegionName is nil",
			enableCRBBilling: true,
			regionName:       "us-east-1",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-nil-region"}, VolumeUUID: "v-nil-region",
				LatestLogicalBackupSize: 1024,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier:  "acct",
					VolumeName:         "vol",
					TotalTransferBytes: 5000,
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:        datamodel.BaseModel{UUID: "bv-nil"},
					Name:             "vault",
					BackupVaultType:  activities.CrossRegionBackupType,
					BackupRegionName: nil,
				},
			},
			expectedTransferBytesCount: 0,
			description:                "nil BackupRegionName should skip transfer metric",
		},
		{
			name:             "skips when BackupRegionName matches current region",
			enableCRBBilling: true,
			regionName:       "us-east-1",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-same-region"}, VolumeUUID: "v-same-region",
				LatestLogicalBackupSize: 1024,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier:  "acct",
					VolumeName:         "vol",
					TotalTransferBytes: 5000,
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:        datamodel.BaseModel{UUID: "bv-same"},
					Name:             "vault",
					BackupVaultType:  activities.CrossRegionBackupType,
					BackupRegionName: stringPtr("us-east-1"),
				},
			},
			expectedTransferBytesCount: 0,
			description:                "BackupRegionName matching current region should skip transfer metric",
		},
		{
			name:             "skips when feature flag disabled",
			enableCRBBilling: false,
			regionName:       "us-east-1",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-flag-off"}, VolumeUUID: "v-flag-off",
				LatestLogicalBackupSize: 1024,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier:  "acct",
					VolumeName:         "vol",
					TotalTransferBytes: 5000,
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:        datamodel.BaseModel{UUID: "bv-off"},
					Name:             "vault",
					BackupVaultType:  activities.CrossRegionBackupType,
					BackupRegionName: stringPtr("eu-west-1"),
				},
			},
			expectedTransferBytesCount: 0,
			description:                "disabled flag should skip transfer metric",
		},
		{
			name:             "skips when TotalTransferBytes is 0",
			enableCRBBilling: true,
			regionName:       "us-east-1",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-zero"}, VolumeUUID: "v-zero",
				LatestLogicalBackupSize: 1024,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier:  "acct",
					VolumeName:         "vol",
					TotalTransferBytes: 0,
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:        datamodel.BaseModel{UUID: "bv-zero"},
					Name:             "vault",
					BackupVaultType:  activities.CrossRegionBackupType,
					BackupRegionName: stringPtr("eu-west-1"),
				},
			},
			expectedTransferBytesCount: 0,
			description:                "zero transfer bytes should skip transfer metric",
		},
		{
			name:             "skips when vault type is not cross-region",
			enableCRBBilling: true,
			regionName:       "us-east-1",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-in-region"}, VolumeUUID: "v-in-region",
				LatestLogicalBackupSize: 1024,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier:  "acct",
					VolumeName:         "vol",
					TotalTransferBytes: 5000,
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:        datamodel.BaseModel{UUID: "bv-ir"},
					Name:             "vault",
					BackupVaultType:  "IN_REGION",
					BackupRegionName: stringPtr("eu-west-1"),
				},
			},
			expectedTransferBytesCount: 0,
			description:                "non-cross-region vault should skip transfer metric",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockBackupStorage)
			ctx := context.Background()
			config := &common.TelemetryConfig{
				RegionName:                            tt.regionName,
				EnableFilesBackupBilling:              true,
				EnableCrossRegionBackupBillingMetrics: tt.enableCRBBilling,
			}

			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
				return p.Offset == 0
			})).Return([]*datamodel.Backup{tt.backup}, nil)
			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
				return p.Offset > 0
			})).Return([]*datamodel.Backup{}, nil)

			result, err := GetBackupMetrics(ctx, m, config, time.Now())
			assert.NoError(t, err)
			assert.NotNil(t, result)

			var transferBytesCount int
			for _, dm := range result.HydratedMetricsDataModel {
				if dm.MeasuredType == string(metadata.CbsCrossRegionVolumeBackupTransferBytes) {
					transferBytesCount++
					assert.Equal(t, float64(tt.backup.Attributes.TotalTransferBytes), dm.Quantity)
					assert.NotNil(t, dm.Metadata, "Metadata should contain backup_region_name")
				}
			}
			assert.Equal(t, tt.expectedTransferBytesCount, transferBytesCount, tt.description)
		})
	}
}

func TestSetCrossRegionRegionMetadata(t *testing.T) {
	t.Run("sets metadata when hm and region are non-nil", func(t *testing.T) {
		hm := &datamodel2.HydratedMetrics{}
		rm := metadata.ResourceMetadata{}
		rm.SetBackupRegionName("eu-west-1")

		setCrossRegionRegionMetadata(log.NewLogger(), hm, rm)

		assert.NotNil(t, hm.Metadata)
		var parsed map[string]string
		err := json.Unmarshal(hm.Metadata, &parsed)
		assert.NoError(t, err)
		assert.Equal(t, "eu-west-1", parsed["backup_region_name"])
	})

	t.Run("no-op when hm is nil", func(t *testing.T) {
		rm := metadata.ResourceMetadata{}
		rm.SetBackupRegionName("eu-west-1")
		setCrossRegionRegionMetadata(log.NewLogger(), nil, rm)
	})

	t.Run("no-op when BackupRegionName is nil", func(t *testing.T) {
		hm := &datamodel2.HydratedMetrics{}
		rm := metadata.ResourceMetadata{}

		setCrossRegionRegionMetadata(log.NewLogger(), hm, rm)
		assert.Nil(t, hm.Metadata)
	})
}

func TestAssembleBackupMetadata_WithBackupVaultRegion(t *testing.T) {
	backup := &datamodel.Backup{
		VolumeUUID:              "vol-uuid",
		LatestLogicalBackupSize: 2048,
		Attributes: &datamodel.BackupAttributes{
			AccountIdentifier: "test-account",
			VolumeName:        "test-volume",
		},
		BackupVault: &datamodel.BackupVault{
			Name:             "vault-name",
			BackupRegionName: stringPtr("eu-west-1"),
			BackupVaultType:  activities.CrossRegionBackupType,
		},
	}
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	rm := assembleBackupMetadata(backup, config)

	assert.NotNil(t, rm.BackupRegionName)
	assert.Equal(t, "eu-west-1", *rm.BackupRegionName)
	assert.Equal(t, "vault-name", derefString(rm.DeploymentName))
}

func TestGetBackupMetrics_GcbdrBackupBilling_SkipsAndIncludes(t *testing.T) {
	tests := []struct {
		name                          string
		enableGcbdrBackupBilling      bool
		backups                       []*datamodel.Backup
		expectedHydratedMetricsCount  int
		expectedDataModelMetricsCount int
		description                   string
	}{
		{
			name:                     "GCBDR billing disabled - skip GCBDR backup billing metrics",
			enableGcbdrBackupBilling: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-gcbdr-1"},
					Name:                    "GcbdrBackup1",
					VolumeUUID:              "volume-uuid-gcbdr-1",
					LatestLogicalBackupSize: 1024,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "AccountGcbdr1",
						VolumeName:        "VolumeGcbdr1",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:   datamodel.BaseModel{UUID: "vault-uuid-gcbdr-1"},
						Name:        "BackupVaultGcbdr1",
						ServiceType: datamodel.ServiceTypeCrossProject,
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 0,
			description:                   "GCBDR backup should skip HydratedMetricsDataModel when GCBDR billing is disabled",
		},
		{
			name:                     "GCBDR billing enabled - include GCBDR backup billing metrics",
			enableGcbdrBackupBilling: true,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-gcbdr-2"},
					Name:                    "GcbdrBackup2",
					VolumeUUID:              "volume-uuid-gcbdr-2",
					LatestLogicalBackupSize: 2048,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "AccountGcbdr2",
						VolumeName:        "VolumeGcbdr2",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:   datamodel.BaseModel{UUID: "vault-uuid-gcbdr-2"},
						Name:        "BackupVaultGcbdr2",
						ServiceType: datamodel.ServiceTypeCrossProject,
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1,
			description:                   "GCBDR backup should create both metrics when GCBDR billing is enabled",
		},
		{
			name:                     "GCBDR billing disabled - non-GCBDR backups still billed",
			enableGcbdrBackupBilling: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-non-gcbdr-1"},
					Name:                    "NonGcbdrBackup1",
					VolumeUUID:              "volume-uuid-non-gcbdr-1",
					LatestLogicalBackupSize: 4096,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier: "AccountNonGcbdr1",
						VolumeName:        "VolumeNonGcbdr1",
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel:   datamodel.BaseModel{UUID: "vault-uuid-non-gcbdr-1"},
						Name:        "BackupVaultNonGcbdr1",
						ServiceType: datamodel.ServiceTypeGCNV,
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1,
			description:                   "Non-GCBDR backup should create both metrics even when GCBDR billing is disabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockBackupStorage)
			ctx := context.Background()
			config := &common.TelemetryConfig{
				RegionName:               "us-east-1",
				EnableFilesBackupBilling: true,
				EnableGcbdrBackupBilling: tt.enableGcbdrBackupBilling,
			}

			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
				return pagination.Offset == 0
			})).Return(tt.backups, nil)
			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
				return pagination.Offset > 0
			})).Return([]*datamodel.Backup{}, nil)

			result, err := GetBackupMetrics(ctx, m, config, time.Now())
			assert.NoError(t, err)
			assert.NotNil(t, result)

			assert.Len(t, result.HydratedMetrics, tt.expectedHydratedMetricsCount,
				"HydratedMetrics count mismatch: %s", tt.description)
			assert.Len(t, result.HydratedMetricsDataModel, tt.expectedDataModelMetricsCount,
				"HydratedMetricsDataModel count mismatch: %s", tt.description)

			for i, metric := range result.HydratedMetrics {
				assert.Equal(t, metadata.BackupLogicalSize, metric.MeasuredType,
					"HydratedMetrics[%d] should have BackupLogicalSize type", i)
			}
		})
	}
}

func TestGetBackupMetrics_ExpertModeBackupBilling_SkipsAndIncludes(t *testing.T) {
	tests := []struct {
		name                          string
		enableExpertModeBackupBilling bool
		backups                       []*datamodel.Backup
		expectedHydratedMetricsCount  int
		expectedDataModelMetricsCount int
		description                   string
	}{
		{
			name:                          "Expert mode billing disabled - skip expert mode backup billing metrics",
			enableExpertModeBackupBilling: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-em-1"},
					Name:                    "ExpertModeBackup1",
					VolumeUUID:              "volume-uuid-em-1",
					LatestLogicalBackupSize: 1024,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier:  "AccountEM1",
						VolumeName:         "VolumeEM1",
						IsExpertModeBackup: true,
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-em-1"},
						Name:      "BackupVaultEM1",
					},
				},
			},
			expectedHydratedMetricsCount:  1, // observability metric always emitted
			expectedDataModelMetricsCount: 0, // billing skipped when flag is disabled
			description:                   "Expert mode backup should skip HydratedMetricsDataModel when billing flag is disabled",
		},
		{
			name:                          "Expert mode billing enabled - include expert mode backup billing metrics",
			enableExpertModeBackupBilling: true,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-em-2"},
					Name:                    "ExpertModeBackup2",
					VolumeUUID:              "volume-uuid-em-2",
					LatestLogicalBackupSize: 2048,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier:  "AccountEM2",
						VolumeName:         "VolumeEM2",
						IsExpertModeBackup: true,
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-em-2"},
						Name:      "BackupVaultEM2",
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1, // billing included when flag is enabled
			description:                   "Expert mode backup should create both metrics when billing flag is enabled",
		},
		{
			name:                          "Expert mode billing disabled - non-expert mode backups still billed",
			enableExpertModeBackupBilling: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-non-em-1"},
					Name:                    "NonExpertModeBackup1",
					VolumeUUID:              "volume-uuid-non-em-1",
					LatestLogicalBackupSize: 4096,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier:  "AccountNonEM1",
						VolumeName:         "VolumeNonEM1",
						IsExpertModeBackup: false,
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-non-em-1"},
						Name:      "BackupVaultNonEM1",
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1, // non-expert mode backups are not affected by the flag
			description:                   "Non-expert mode backup should create both metrics even when expert mode billing flag is disabled",
		},
		{
			name:                          "Expert mode billing disabled - mixed expert and non-expert backups",
			enableExpertModeBackupBilling: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-em-mix-1"},
					Name:                    "ExpertModeBackupMix1",
					VolumeUUID:              "volume-uuid-em-mix-1",
					LatestLogicalBackupSize: 1024,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier:  "AccountEMMix1",
						VolumeName:         "VolumeEMMix1",
						IsExpertModeBackup: true,
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-em-mix-1"},
						Name:      "BackupVaultEMMix1",
					},
				},
				{
					BaseModel:               datamodel.BaseModel{UUID: "backup-uuid-em-mix-2"},
					Name:                    "NonExpertModeBackupMix2",
					VolumeUUID:              "volume-uuid-em-mix-2",
					LatestLogicalBackupSize: 2048,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier:  "AccountEMMix2",
						VolumeName:         "VolumeEMMix2",
						IsExpertModeBackup: false,
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-em-mix-2"},
						Name:      "BackupVaultEMMix2",
					},
				},
			},
			expectedHydratedMetricsCount:  2, // both get observability metrics
			expectedDataModelMetricsCount: 1, // only non-expert mode backup gets billing metric
			description:                   "Mixed backups: expert mode skipped, non-expert mode billed when flag is disabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockBackupStorage)
			ctx := context.Background()
			config := &common.TelemetryConfig{
				RegionName:                    "us-east-1",
				EnableFilesBackupBilling:      true,
				EnableExpertModeBackupBilling: tt.enableExpertModeBackupBilling,
			}

			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
				return pagination.Offset == 0
			})).Return(tt.backups, nil)
			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(pagination *dbutils.Pagination) bool {
				return pagination.Offset > 0
			})).Return([]*datamodel.Backup{}, nil)

			result, err := GetBackupMetrics(ctx, m, config, time.Now())
			assert.NoError(t, err)
			assert.NotNil(t, result)

			assert.Len(t, result.HydratedMetrics, tt.expectedHydratedMetricsCount,
				"HydratedMetrics count mismatch: %s", tt.description)
			assert.Len(t, result.HydratedMetricsDataModel, tt.expectedDataModelMetricsCount,
				"HydratedMetricsDataModel count mismatch: %s", tt.description)

			for i, metric := range result.HydratedMetrics {
				assert.Equal(t, metadata.BackupLogicalSize, metric.MeasuredType,
					"HydratedMetrics[%d] should have BackupLogicalSize type", i)
			}
		})
	}
}

func TestGetBackupMetrics_CrossProjectVault_BillsToVaultProject(t *testing.T) {
	tests := []struct {
		name               string
		backup             *datamodel.Backup
		expectedConsumerID string
		description        string
	}{
		{
			name: "CrossProject vault with Account - bills to vault project",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-cp"}, VolumeUUID: "v-cp",
				LatestLogicalBackupSize: 1024,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "VolumeOwnerProject",
					VolumeName:        "vol",
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:   datamodel.BaseModel{UUID: "bv-cp"},
					Name:        "vault",
					ServiceType: datamodel.ServiceTypeCrossProject,
					Account: &datamodel.Account{
						BaseModel: datamodel.BaseModel{UUID: "vault-account-uuid"},
						Name:      "VaultOwnerProject",
					},
				},
			},
			expectedConsumerID: "VaultOwnerProject",
			description:        "CrossProject backup should bill to vault's owning project, not the volume owner",
		},
		{
			name: "GCNV vault - bills to volume owner (no override)",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-gcnv"}, VolumeUUID: "v-gcnv",
				LatestLogicalBackupSize: 2048,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "VolumeOwnerProject",
					VolumeName:        "vol",
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:   datamodel.BaseModel{UUID: "bv-gcnv"},
					Name:        "vault",
					ServiceType: datamodel.ServiceTypeGCNV,
					Account: &datamodel.Account{
						BaseModel: datamodel.BaseModel{UUID: "vault-account-uuid"},
						Name:      "VaultOwnerProject",
					},
				},
			},
			expectedConsumerID: "VolumeOwnerProject",
			description:        "GCNV backup should bill to volume owner's project (AccountIdentifier)",
		},
		{
			name: "CrossProject vault with nil Account - falls back to volume owner",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-cp-nil"}, VolumeUUID: "v-cp-nil",
				LatestLogicalBackupSize: 512,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "VolumeOwnerProject",
					VolumeName:        "vol",
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:   datamodel.BaseModel{UUID: "bv-cp-nil"},
					Name:        "vault",
					ServiceType: datamodel.ServiceTypeCrossProject,
					Account:     nil,
				},
			},
			expectedConsumerID: "VolumeOwnerProject",
			description:        "CrossProject vault with nil Account should fall back to volume owner",
		},
		{
			name: "CrossProject vault with empty Account name - falls back to volume owner",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "b-cp-empty"}, VolumeUUID: "v-cp-empty",
				LatestLogicalBackupSize: 256,
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "VolumeOwnerProject",
					VolumeName:        "vol",
				},
				BackupVault: &datamodel.BackupVault{
					BaseModel:   datamodel.BaseModel{UUID: "bv-cp-empty"},
					Name:        "vault",
					ServiceType: datamodel.ServiceTypeCrossProject,
					Account: &datamodel.Account{
						BaseModel: datamodel.BaseModel{UUID: "vault-account-uuid"},
						Name:      "",
					},
				},
			},
			expectedConsumerID: "VolumeOwnerProject",
			description:        "CrossProject vault with empty Account.Name should fall back to volume owner",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockBackupStorage)
			ctx := context.Background()
			config := &common.TelemetryConfig{
				RegionName:               "us-east-1",
				EnableFilesBackupBilling: true,
				EnableGcbdrBackupBilling: true,
			}

			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
				return p.Offset == 0
			})).Return([]*datamodel.Backup{tt.backup}, nil)
			m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
				return p.Offset > 0
			})).Return([]*datamodel.Backup{}, nil)

			result, err := GetBackupMetrics(ctx, m, config, time.Now())
			assert.NoError(t, err)
			assert.NotNil(t, result)

			require.Len(t, result.HydratedMetricsDataModel, 1, tt.description)
			assert.Equal(t, tt.expectedConsumerID, result.HydratedMetricsDataModel[0].ConsumerID,
				"%s: ConsumerID mismatch", tt.description)
		})
	}
}

func TestGetBackupMetrics_MultiVaultSameVolume_EmitsPerVaultBillingRows(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableFilesBackupBilling: true,
		EnableGcbdrBackupBilling: true,
	}

	// Volume1 is in Project4. Backups span 3 vaults across 2 projects.
	//   Backup1: Vault1 (Project1), endpoint-a  — size 100
	//   Backup2: Vault1 (Project1), endpoint-b  — size 200  (re-attached, different endpoint)
	//   Backup3: Vault2 (Project1), endpoint-c  — size 300  (new vault, same project)
	//   Backup4: Vault3 (Project2), endpoint-d  — size 400  (new vault, different project)
	backups := []*datamodel.Backup{
		{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "b1"}, VolumeUUID: "volume1",
			LatestLogicalBackupSize: 100,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Project4",
				VolumeName:        "vol1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:   datamodel.BaseModel{UUID: "vault1-uuid"},
				Name:        "vault1",
				ServiceType: datamodel.ServiceTypeCrossProject,
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "acct-p1"},
					Name:      "Project1",
				},
			},
		},
		{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: "b2"}, VolumeUUID: "volume1",
			LatestLogicalBackupSize: 200,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Project4",
				VolumeName:        "vol1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:   datamodel.BaseModel{UUID: "vault1-uuid"},
				Name:        "vault1",
				ServiceType: datamodel.ServiceTypeCrossProject,
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "acct-p1"},
					Name:      "Project1",
				},
			},
		},
		{
			BaseModel: datamodel.BaseModel{ID: 3, UUID: "b3"}, VolumeUUID: "volume1",
			LatestLogicalBackupSize: 300,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Project4",
				VolumeName:        "vol1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:   datamodel.BaseModel{UUID: "vault2-uuid"},
				Name:        "vault2",
				ServiceType: datamodel.ServiceTypeCrossProject,
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "acct-p1"},
					Name:      "Project1",
				},
			},
		},
		{
			BaseModel: datamodel.BaseModel{ID: 4, UUID: "b4"}, VolumeUUID: "volume1",
			LatestLogicalBackupSize: 400,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Project4",
				VolumeName:        "vol1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:   datamodel.BaseModel{UUID: "vault3-uuid"},
				Name:        "vault3",
				ServiceType: datamodel.ServiceTypeCrossProject,
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "acct-p2"},
					Name:      "Project2",
				},
			},
		},
	}

	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)

	// 3 billing rows: Vault1 (B1+B2), Vault2 (B3), Vault3 (B4)
	require.Len(t, result.HydratedMetricsDataModel, 3,
		"should emit one billing row per (volume, vault) — 3 vaults for the same volume")

	// 3 observation metrics matching the billing rows
	require.Len(t, result.HydratedMetrics, 3)

	// Row 0: Vault1 — B1(100) + B2(200) = 300, billed to Project1
	assert.Equal(t, "Project1", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, float64(300), result.HydratedMetricsDataModel[0].Quantity)
	assert.Equal(t, "vault1", result.HydratedMetricsDataModel[0].DeploymentName)
	assert.Equal(t, "volume1", result.HydratedMetricsDataModel[0].ResourceName)

	// Row 1: Vault2 — B3(300), billed to Project1
	assert.Equal(t, "Project1", result.HydratedMetricsDataModel[1].ConsumerID)
	assert.Equal(t, float64(300), result.HydratedMetricsDataModel[1].Quantity)
	assert.Equal(t, "vault2", result.HydratedMetricsDataModel[1].DeploymentName)
	assert.Equal(t, "volume1", result.HydratedMetricsDataModel[1].ResourceName)

	// Row 2: Vault3 — B4(400), billed to Project2
	assert.Equal(t, "Project2", result.HydratedMetricsDataModel[2].ConsumerID)
	assert.Equal(t, float64(400), result.HydratedMetricsDataModel[2].Quantity)
	assert.Equal(t, "vault3", result.HydratedMetricsDataModel[2].DeploymentName)
	assert.Equal(t, "volume1", result.HydratedMetricsDataModel[2].ResourceName)
}

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}

// ---------------------------------------------------------------------------
// Tests for getBackupMetricsPerVault (flag-on path: EnableGcbdrBackupBilling)
// ---------------------------------------------------------------------------

func TestGetBackupMetrics_PerVault_DbError_ReturnsError(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableGcbdrBackupBilling: true,
	}

	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset == 0
	})).Return(nil, assert.AnError)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.Error(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetrics)
}

func TestGetBackupMetrics_PerVault_EmptyBackups_ReturnsEmpty(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableGcbdrBackupBilling: true,
	}

	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetrics)
	assert.Empty(t, result.HydratedMetricsDataModel)
}

func TestGetBackupMetrics_PerVault_NilAttributes_Skipped(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableGcbdrBackupBilling: true,
		EnableFilesBackupBilling: true,
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{ID: 1, UUID: "b-nil-attr"},
			VolumeUUID:              "vol-nil-attr",
			LatestLogicalBackupSize: 512,
			Attributes:              nil, // nil attributes — should be skipped
		},
	}

	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetrics, "backup with nil attributes should be skipped entirely")
	assert.Empty(t, result.HydratedMetricsDataModel)
}

func TestGetBackupMetrics_PerVault_CrossRegionSkipped_FlagDisabled(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableGcbdrBackupBilling:              true,
		EnableFilesBackupBilling:              true,
		EnableCrossRegionBackupBillingMetrics: false,
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{ID: 1, UUID: "b-crb"},
			VolumeUUID:              "vol-crb",
			LatestLogicalBackupSize: 1024,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Account1",
				VolumeName:        "Volume1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "bv-crb"},
				BackupVaultType:  activities.CrossRegionBackupType,
				BackupRegionName: stringPtr("us-west-1"),
			},
		},
	}

	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	require.Len(t, result.HydratedMetrics, 1, "observation metric always emitted")
	assert.Empty(t, result.HydratedMetricsDataModel, "billing skipped when CRB flag disabled")
}

func TestGetBackupMetrics_PerVault_CrossRegionSkipped_NilRegionName(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableGcbdrBackupBilling:              true,
		EnableFilesBackupBilling:              true,
		EnableCrossRegionBackupBillingMetrics: true,
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{ID: 1, UUID: "b-crb-nil"},
			VolumeUUID:              "vol-crb-nil",
			LatestLogicalBackupSize: 2048,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Account1",
				VolumeName:        "Volume1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "bv-crb-nil"},
				BackupVaultType:  activities.CrossRegionBackupType,
				BackupRegionName: nil, // nil → skip
			},
		},
	}

	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	require.Len(t, result.HydratedMetrics, 1)
	assert.Empty(t, result.HydratedMetricsDataModel, "billing skipped when BackupRegionName is nil")
}

func TestGetBackupMetrics_PerVault_CrossRegionSkipped_MatchingRegion(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableGcbdrBackupBilling:              true,
		EnableFilesBackupBilling:              true,
		EnableCrossRegionBackupBillingMetrics: true,
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{ID: 1, UUID: "b-crb-same"},
			VolumeUUID:              "vol-crb-same",
			LatestLogicalBackupSize: 4096,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Account1",
				VolumeName:        "Volume1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "bv-crb-same"},
				BackupVaultType:  activities.CrossRegionBackupType,
				BackupRegionName: stringPtr("us-east-1"), // matches config.RegionName → skip
			},
		},
	}

	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	require.Len(t, result.HydratedMetrics, 1)
	assert.Empty(t, result.HydratedMetricsDataModel, "billing skipped when BackupRegionName matches current region")
}

func TestGetBackupMetrics_PerVault_CmekSkipped(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableGcbdrBackupBilling: true,
		EnableFilesBackupBilling: true,
		EnableCmekBackupBilling:  false,
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{ID: 1, UUID: "b-cmek"},
			VolumeUUID:              "vol-cmek",
			LatestLogicalBackupSize: 8192,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Account1",
				VolumeName:        "Volume1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{UUID: "bv-cmek"},
				CmekAttributes: &datamodel.CmekAttributes{
					KmsConfigResourcePath: stringPtr("projects/p/locations/l/keyRings/r/cryptoKeys/k"),
				},
			},
		},
	}

	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	require.Len(t, result.HydratedMetrics, 1)
	assert.Empty(t, result.HydratedMetricsDataModel, "CMEK billing skipped when flag disabled")
}

func TestGetBackupMetrics_PerVault_CrossRegionTransferBilling(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableGcbdrBackupBilling:              true,
		EnableFilesBackupBilling:              true,
		EnableCrossRegionBackupBillingMetrics: true,
	}

	regionName := "us-west-2"
	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{ID: 1, UUID: "b-crb-xfer"},
			VolumeUUID:              "vol-crb-xfer",
			LatestLogicalBackupSize: 1024,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier:  "Account1",
				VolumeName:         "Volume1",
				TotalTransferBytes: 500,
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "bv-crb-xfer"},
				BackupVaultType:  activities.CrossRegionBackupType,
				BackupRegionName: &regionName,
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "acct-1"},
					Name:      "Account1",
				},
			},
		},
	}

	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := GetBackupMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	require.Len(t, result.HydratedMetrics, 1, "one BackupLogicalSize observation metric")
	require.Len(t, result.HydratedMetricsDataModel, 2,
		"one BackupLogicalSize billing row + one CbsCrossRegionVolumeBackupTransferBytes row")

	// Verify transfer billing row
	transferRow := result.HydratedMetricsDataModel[1]
	assert.Equal(t, string(metadata.CbsCrossRegionVolumeBackupTransferBytes), transferRow.MeasuredType)
	assert.Equal(t, float64(500), transferRow.Quantity)
	assert.NotNil(t, transferRow.Metadata, "cross-region region metadata should be set")
}

func TestGetBackupMetricsPerVault_GcbdrFlagDisabled_SkipsCrossProject(t *testing.T) {
	m := new(mockBackupStorage)
	ctx := context.Background()
	// Call getBackupMetricsPerVault directly with EnableGcbdrBackupBilling=false to
	// exercise the dead-code guard at the bottom of the grouping loop (lines 304-308).
	config := &common.TelemetryConfig{
		RegionName:               "us-east-1",
		EnableGcbdrBackupBilling: false,
		EnableFilesBackupBilling: true,
	}

	backups := []*datamodel.Backup{
		{
			BaseModel:               datamodel.BaseModel{ID: 1, UUID: "b-cp"},
			VolumeUUID:              "vol-cp",
			LatestLogicalBackupSize: 2048,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "Account1",
				VolumeName:        "Volume1",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:   datamodel.BaseModel{UUID: "bv-cp"},
				ServiceType: datamodel.ServiceTypeCrossProject,
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{UUID: "acct-cp"},
					Name:      "VaultProject",
				},
			},
		},
	}

	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset == 0
	})).Return(backups, nil)
	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
		return p.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	result, err := getBackupMetricsPerVault(ctx, m, config, time.Now())
	assert.NoError(t, err)
	require.Len(t, result.HydratedMetrics, 1, "observation metric always emitted")
	assert.Empty(t, result.HydratedMetricsDataModel,
		"cross-project billing skipped when EnableGcbdrBackupBilling is false")
}

func TestGetBackupMetricsPerVault_ExpertModeBackupBilling_SkipsAndIncludes(t *testing.T) {
	tests := []struct {
		name                          string
		enableExpertModeBackupBilling bool
		backups                       []*datamodel.Backup
		expectedHydratedMetricsCount  int
		expectedDataModelMetricsCount int
		description                   string
	}{
		{
			name:                          "Expert mode billing disabled - skip expert mode backup billing metrics",
			enableExpertModeBackupBilling: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{ID: 1, UUID: "backup-uuid-em-vault-1"},
					VolumeUUID:              "volume-uuid-em-vault-1",
					LatestLogicalBackupSize: 1024,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier:  "AccountEMVault1",
						VolumeName:         "VolumeEMVault1",
						IsExpertModeBackup: true,
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-em-vault-1"},
						Name:      "BackupVaultEMVault1",
					},
				},
			},
			expectedHydratedMetricsCount:  1, // observability metric always emitted
			expectedDataModelMetricsCount: 0, // billing skipped when flag is disabled
			description:                   "Expert mode backup should skip HydratedMetricsDataModel when billing flag is disabled (perVault path)",
		},
		{
			name:                          "Expert mode billing enabled - include expert mode backup billing metrics",
			enableExpertModeBackupBilling: true,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{ID: 1, UUID: "backup-uuid-em-vault-2"},
					VolumeUUID:              "volume-uuid-em-vault-2",
					LatestLogicalBackupSize: 2048,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier:  "AccountEMVault2",
						VolumeName:         "VolumeEMVault2",
						IsExpertModeBackup: true,
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-em-vault-2"},
						Name:      "BackupVaultEMVault2",
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1, // billing included when flag is enabled
			description:                   "Expert mode backup should create both metrics when billing flag is enabled (perVault path)",
		},
		{
			name:                          "Expert mode billing disabled - non-expert mode backups still billed",
			enableExpertModeBackupBilling: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{ID: 1, UUID: "backup-uuid-non-em-vault-1"},
					VolumeUUID:              "volume-uuid-non-em-vault-1",
					LatestLogicalBackupSize: 4096,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier:  "AccountNonEMVault1",
						VolumeName:         "VolumeNonEMVault1",
						IsExpertModeBackup: false,
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-non-em-vault-1"},
						Name:      "BackupVaultNonEMVault1",
					},
				},
			},
			expectedHydratedMetricsCount:  1,
			expectedDataModelMetricsCount: 1, // non-expert mode backups are not affected by the flag
			description:                   "Non-expert mode backup should create both metrics even when expert mode billing flag is disabled (perVault path)",
		},
		{
			name:                          "Expert mode billing disabled - mixed expert and non-expert backups",
			enableExpertModeBackupBilling: false,
			backups: []*datamodel.Backup{
				{
					BaseModel:               datamodel.BaseModel{ID: 2, UUID: "backup-uuid-em-vault-mix-1"},
					VolumeUUID:              "volume-uuid-em-vault-mix-1",
					LatestLogicalBackupSize: 1024,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier:  "AccountEMVaultMix1",
						VolumeName:         "VolumeEMVaultMix1",
						IsExpertModeBackup: true,
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-em-vault-mix-1"},
						Name:      "BackupVaultEMVaultMix1",
					},
				},
				{
					BaseModel:               datamodel.BaseModel{ID: 1, UUID: "backup-uuid-non-em-vault-mix-2"},
					VolumeUUID:              "volume-uuid-non-em-vault-mix-2",
					LatestLogicalBackupSize: 2048,
					Attributes: &datamodel.BackupAttributes{
						AccountIdentifier:  "AccountNonEMVaultMix2",
						VolumeName:         "VolumeNonEMVaultMix2",
						IsExpertModeBackup: false,
					},
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{UUID: "vault-uuid-non-em-vault-mix-2"},
						Name:      "BackupVaultNonEMVaultMix2",
					},
				},
			},
			expectedHydratedMetricsCount:  2, // both get observability metrics
			expectedDataModelMetricsCount: 1, // only non-expert mode backup gets billing metric
			description:                   "Mixed backups: expert mode skipped, non-expert mode billed when flag is disabled (perVault path)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockBackupStorage)
			ctx := context.Background()
			config := &common.TelemetryConfig{
				RegionName:                    "us-east-1",
				EnableFilesBackupBilling:      true,
				EnableGcbdrBackupBilling:      true,
				EnableExpertModeBackupBilling: tt.enableExpertModeBackupBilling,
			}

			m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
				return p.Offset == 0
			})).Return(tt.backups, nil)
			m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
				return p.Offset > 0
			})).Return([]*datamodel.Backup{}, nil)

			result, err := GetBackupMetrics(ctx, m, config, time.Now())
			assert.NoError(t, err)
			assert.NotNil(t, result)

			assert.Len(t, result.HydratedMetrics, tt.expectedHydratedMetricsCount,
				"HydratedMetrics count mismatch: %s", tt.description)
			assert.Len(t, result.HydratedMetricsDataModel, tt.expectedDataModelMetricsCount,
				"HydratedMetricsDataModel count mismatch: %s", tt.description)

			for i, metric := range result.HydratedMetrics {
				assert.Equal(t, metadata.BackupLogicalSize, metric.MeasuredType,
					"HydratedMetrics[%d] should have BackupLogicalSize type", i)
			}
		})
	}
}
