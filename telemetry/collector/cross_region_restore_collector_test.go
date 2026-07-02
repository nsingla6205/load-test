package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	metricsdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	datamodel2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
)

// --- mock for vcp Storage (cross-region restore tests) ---

type mockCrossRegionVCPStorage struct {
	mock.Mock
	database.Storage
}

func (m *mockCrossRegionVCPStorage) GetJobsWithCondition(ctx context.Context, filter dbutils.Filter) ([]*datamodel.Job, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*datamodel.Job), args.Error(1)
}

func (m *mockCrossRegionVCPStorage) ListVolumesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Volume, error) {
	args := m.Called(ctx, conditions, pagination)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*datamodel.Volume), args.Error(1)
}

func (m *mockCrossRegionVCPStorage) GetBackupWithVaultByUUID(ctx context.Context, backupUUID string) (*datamodel.Backup, error) {
	args := m.Called(ctx, backupUUID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*datamodel.Backup), args.Error(1)
}

func (m *mockCrossRegionVCPStorage) GetBackupVaultByNameAndOwnerID(ctx context.Context, backupVaultName, ownerID string) (*datamodel.BackupVault, error) {
	args := m.Called(ctx, backupVaultName, ownerID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*datamodel.BackupVault), args.Error(1)
}

func (m *mockCrossRegionVCPStorage) GetBackupByNameAndBackupVaultID(ctx context.Context, backupName string, backupVaultID int64) (*datamodel.Backup, error) {
	args := m.Called(ctx, backupName, backupVaultID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*datamodel.Backup), args.Error(1)
}

func (m *mockCrossRegionVCPStorage) GetSfrMetadataByJobID(ctx context.Context, jobID int64) (*datamodel.SfrMetadata, error) {
	args := m.Called(ctx, jobID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*datamodel.SfrMetadata), args.Error(1)
}

// --- mock for metrics Storage (cross-region restore tests) ---

type mockCrossRegionMetricsStorage struct {
	mock.Mock
	metricsdb.Storage
}

func (m *mockCrossRegionMetricsStorage) GetRestoreTimestamp(ctx context.Context) (*datamodel2.RestoreTimestamp, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*datamodel2.RestoreTimestamp), args.Error(1)
}

func (m *mockCrossRegionMetricsStorage) UpdateRestoreTimestamp(ctx context.Context, lastProcessedAt time.Time) error {
	args := m.Called(ctx, lastProcessedAt)
	return args.Error(0)
}

// --- helpers ---

func strPtr(s string) *string {
	return &s
}

func newTestJob(uuid, jobType, state, resourceName string, accountID int64, updatedAt time.Time) *datamodel.Job {
	return &datamodel.Job{
		BaseModel:    datamodel.BaseModel{UUID: uuid, UpdatedAt: updatedAt},
		Type:         jobType,
		State:        state,
		ResourceName: resourceName,
		AccountID:    sql.NullInt64{Int64: accountID, Valid: true},
	}
}

func newTestJobWithAttrs(uuid, jobType, state, resourceName string, accountID int64, updatedAt time.Time, attrs map[string]interface{}) *datamodel.Job {
	job := newTestJob(uuid, jobType, state, resourceName, accountID, updatedAt)
	job.JobAttributes = &datamodel.JobAttributes{
		PayloadAttributes: attrs,
	}
	return job
}

func crossRegionAttrs(volumeUUID, accountName, deploymentName, protocols, backupRegionName string, backupSizeInBytes int64) map[string]interface{} {
	attrs := map[string]interface{}{
		"volume_uuid":          volumeUUID,
		"account_name":         accountName,
		"deployment_name":      deploymentName,
		"backup_size_in_bytes": float64(backupSizeInBytes),
		"backup_vault_type":    activities.CrossRegionBackupType,
		"backup_region_name":   backupRegionName,
	}
	if protocols != "" {
		attrs["protocols"] = protocols
	}
	return attrs
}

func newCrossRegionBackup(uuid, name string, sizeInBytes int64, backupRegionName string) *datamodel.Backup {
	return &datamodel.Backup{
		BaseModel:   datamodel.BaseModel{UUID: uuid},
		Name:        name,
		SizeInBytes: sizeInBytes,
		BackupVault: &datamodel.BackupVault{
			BackupVaultType:  activities.CrossRegionBackupType,
			BackupRegionName: strPtr(backupRegionName),
		},
	}
}

func newVolume(uuid, name string, accountName, deploymentName string, restoredBackupID string) *datamodel.Volume {
	return &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: uuid},
		Name:      name,
		Account:   &datamodel.Account{Name: accountName},
		Pool:      &datamodel.Pool{DeploymentName: deploymentName},
		VolumeAttributes: &datamodel.VolumeAttributes{
			RestoredBackupID: restoredBackupID,
			AccountName:      accountName,
			DeploymentName:   deploymentName,
			Protocols:        []string{"NFSV3"},
		},
	}
}

func defaultConfig() *common.TelemetryConfig {
	return &common.TelemetryConfig{
		RegionName:                            "us-east4",
		EnableCrossRegionBackupBillingMetrics: true,
		EnableSFRCrossRegionRestoreBilling:    false,
		EnableFilesBackupBilling:              true,
	}
}

// =============================================================================
// ProcessRestoreBillingMetrics tests
// =============================================================================

func TestProcessRestoreBillingMetrics_GetRestoreTimestampError(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	metricsDB := new(mockCrossRegionMetricsStorage)
	ctx := context.Background()
	config := defaultConfig()

	metricsDB.On("GetRestoreTimestamp", mock.Anything).Return(nil, errors.New("db error"))

	result, err := ProcessRestoreBillingMetrics(ctx, vcpDB, metricsDB, config, time.Now())
	assert.Error(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetricsDataModel)
}

func TestProcessRestoreBillingMetrics_NoJobsFound(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	metricsDB := new(mockCrossRegionMetricsStorage)
	ctx := context.Background()
	config := defaultConfig()
	now := time.Now()

	metricsDB.On("GetRestoreTimestamp", mock.Anything).Return(nil, nil)
	metricsDB.On("UpdateRestoreTimestamp", mock.Anything, now).Return(nil)
	vcpDB.On("GetJobsWithCondition", mock.Anything, mock.Anything).Return([]*datamodel.Job{}, nil)

	result, err := ProcessRestoreBillingMetrics(ctx, vcpDB, metricsDB, config, now)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetricsDataModel)
	metricsDB.AssertCalled(t, "UpdateRestoreTimestamp", mock.Anything, now)
}

func TestProcessRestoreBillingMetrics_GetJobsError(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	metricsDB := new(mockCrossRegionMetricsStorage)
	ctx := context.Background()
	config := defaultConfig()
	now := time.Now()

	metricsDB.On("GetRestoreTimestamp", mock.Anything).Return(nil, nil)
	metricsDB.On("UpdateRestoreTimestamp", mock.Anything, now).Return(nil)
	vcpDB.On("GetJobsWithCondition", mock.Anything, mock.Anything).Return(nil, errors.New("job query failed"))

	result, err := ProcessRestoreBillingMetrics(ctx, vcpDB, metricsDB, config, now)
	assert.Error(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetricsDataModel)
}

func TestProcessRestoreBillingMetrics_ExistingTimestamp(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	metricsDB := new(mockCrossRegionMetricsStorage)
	ctx := context.Background()
	config := defaultConfig()
	now := time.Now()
	lastProcessed := now.Add(-30 * time.Minute)

	metricsDB.On("GetRestoreTimestamp", mock.Anything).Return(&datamodel2.RestoreTimestamp{LastProcessedAt: lastProcessed}, nil)
	metricsDB.On("UpdateRestoreTimestamp", mock.Anything, now).Return(nil)
	vcpDB.On("GetJobsWithCondition", mock.Anything, mock.Anything).Return([]*datamodel.Job{}, nil)

	result, err := ProcessRestoreBillingMetrics(ctx, vcpDB, metricsDB, config, now)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetricsDataModel)
	metricsDB.AssertCalled(t, "UpdateRestoreTimestamp", mock.Anything, now)
}

func TestProcessRestoreBillingMetrics_FullRestore_CrossRegion_Success(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	metricsDB := new(mockCrossRegionMetricsStorage)
	ctx := context.Background()
	config := defaultConfig()
	now := time.Now()
	jobUpdatedAt := now.Add(-5 * time.Minute)

	attrs := crossRegionAttrs("vol-1", "acct-1", "deploy-1", "NFSV3", "us-west2", 1024*1024)
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "restored-vol", 100, jobUpdatedAt, attrs)

	metricsDB.On("GetRestoreTimestamp", mock.Anything).Return(nil, nil)
	metricsDB.On("UpdateRestoreTimestamp", mock.Anything, now).Return(nil)
	vcpDB.On("GetJobsWithCondition", mock.Anything, mock.Anything).Return([]*datamodel.Job{job}, nil)

	result, err := ProcessRestoreBillingMetrics(ctx, vcpDB, metricsDB, config, now)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	hm := result.HydratedMetricsDataModel[0]
	assert.Equal(t, string(metadata.CbsCrossRegionVolumeRestoreTransferBytes), hm.MeasuredType)
	assert.Equal(t, float64(1024*1024), hm.Quantity)
	assert.Equal(t, "acct-1", hm.ConsumerID)
	assert.Equal(t, string(metadata.Volume), hm.ResourceType)

	var extra map[string]string
	assert.NoError(t, json.Unmarshal(hm.Metadata, &extra))
	assert.Equal(t, "us-west2", extra["backup_region_name"])
	assert.Equal(t, "us-east4", extra["source_region_name"])

	metricsDB.AssertCalled(t, "UpdateRestoreTimestamp", mock.Anything, now)
}

func TestProcessRestoreBillingMetrics_UpdateTimestampError(t *testing.T) {
	metricsDB := new(mockCrossRegionMetricsStorage)
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()
	now := time.Now()

	metricsDB.On("GetRestoreTimestamp", mock.Anything).Return(nil, nil)
	metricsDB.On("UpdateRestoreTimestamp", mock.Anything, now).Return(errors.New("update failed"))

	result, err := ProcessRestoreBillingMetrics(ctx, vcpDB, metricsDB, config, now)
	assert.Error(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetricsDataModel)
}

func TestProcessRestoreBillingMetrics_MultipleJobs_TimestampAdvancedToNow(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	metricsDB := new(mockCrossRegionMetricsStorage)
	ctx := context.Background()
	config := defaultConfig()
	now := time.Now()

	job1Updated := now.Add(-10 * time.Minute)
	job2Updated := now.Add(-5 * time.Minute)

	attrs1 := crossRegionAttrs("vol-uuid-1", "acct-1", "deploy-1", "NFSV3", "us-west2", 500)
	attrs2 := crossRegionAttrs("vol-uuid-2", "acct-1", "deploy-1", "NFSV3", "us-west2", 1000)
	job1 := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, job1Updated, attrs1)
	job2 := newTestJobWithAttrs("job-2", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-2", 100, job2Updated, attrs2)

	metricsDB.On("GetRestoreTimestamp", mock.Anything).Return(nil, nil)
	metricsDB.On("UpdateRestoreTimestamp", mock.Anything, now).Return(nil)
	vcpDB.On("GetJobsWithCondition", mock.Anything, mock.Anything).Return([]*datamodel.Job{job1, job2}, nil)

	result, err := ProcessRestoreBillingMetrics(ctx, vcpDB, metricsDB, config, now)
	assert.NoError(t, err)
	assert.Len(t, result.HydratedMetricsDataModel, 2)

	metricsDB.AssertCalled(t, "UpdateRestoreTimestamp", mock.Anything, now)
}

// =============================================================================
// processRestoreJob tests
// =============================================================================

func TestProcessRestoreJob_InvalidAccountID(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-no-acct"},
		AccountID: sql.NullInt64{Valid: false},
	}

	result := processRestoreJob(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestProcessRestoreJob_FailedFullRestore_Skipped(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-fail", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateERROR), "vol-fail", 100, time.Now())

	result := processRestoreJob(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestProcessRestoreJob_SFRJob_Dispatches(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()
	now := time.Now()

	job := newTestJob("job-sfr", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, now)
	job.BaseModel.ID = 42

	sfrMeta := &datamodel.SfrMetadata{
		FilesSize:  2048,
		FileCount:  5,
		BackupUUID: "sfr-backup-1",
		VolumeUUID: "sfr-vol-uuid",
	}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(42)).Return(sfrMeta, nil)

	backup := newCrossRegionBackup("sfr-backup-1", "sfr-backup-1", 10000, "eu-west1")
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "sfr-backup-1").Return(backup, nil)

	vol := newVolume("sfr-vol-uuid", "sfr-vol", "sfr-acct", "sfr-deploy", "")
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	result := processRestoreJob(ctx, vcpDB, config, job, now)
	assert.NotNil(t, result)
	assert.Equal(t, float64(2048), result.Quantity)
}

// =============================================================================
// createCrossRegionRestoreMetrics tests (attributes-based)
// =============================================================================

func TestCreateCrossRegionRestoreMetrics_NilJobAttributes_Skipped(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, time.Now())
	result := createCrossRegionRestoreMetrics(ctx, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateCrossRegionRestoreMetrics_MissingBackupSize_Skipped(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()

	attrs := map[string]interface{}{
		"volume_uuid":        "vol-uuid",
		"account_name":       "acct",
		"deployment_name":    "deploy",
		"protocols":          "NFSV3",
		"backup_vault_type":  activities.CrossRegionBackupType,
		"backup_region_name": "us-west2",
	}
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, time.Now(), attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateCrossRegionRestoreMetrics_Int64BackupSize_Success(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()

	attrs := map[string]interface{}{
		"volume_uuid":          "vol-uuid",
		"account_name":         "acct",
		"deployment_name":      "deploy",
		"protocols":            "ISCSI",
		"backup_size_in_bytes": int64(2048),
		"backup_vault_type":    activities.CrossRegionBackupType,
		"backup_region_name":   "us-west2",
	}
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, time.Now(), attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, time.Now())
	assert.NotNil(t, result)
	assert.Equal(t, float64(2048), result.Quantity)
}

func TestCreateCrossRegionRestoreMetrics_NotCrossRegion(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()

	attrs := map[string]interface{}{
		"volume_uuid": "vol-uuid", "account_name": "acct", "deployment_name": "deploy",
		"protocols": "NFSV3", "backup_size_in_bytes": float64(1024),
		"backup_vault_type": "IN_REGION", "backup_region_name": "us-west2",
	}
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, time.Now(), attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateCrossRegionRestoreMetrics_BackupRegionEmpty(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()

	attrs := crossRegionAttrs("vol-uuid", "acct", "deploy", "NFSV3", "", 1024)
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, time.Now(), attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateCrossRegionRestoreMetrics_BackupRegionMatchesCurrentRegion(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()

	attrs := crossRegionAttrs("vol-uuid", "acct", "deploy", "NFSV3", config.RegionName, 1024)
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, time.Now(), attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateCrossRegionRestoreMetrics_ZeroBackupSize(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()

	attrs := crossRegionAttrs("vol-uuid", "acct", "deploy", "NFSV3", "us-west2", 0)
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, time.Now(), attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateCrossRegionRestoreMetrics_Success(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()
	now := time.Now()

	attrs := crossRegionAttrs("vol-uuid", "acct", "deploy", "NFSV3", "us-west2", 5000)
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, now, attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, now)
	assert.NotNil(t, result)
	assert.Equal(t, string(metadata.CbsCrossRegionVolumeRestoreTransferBytes), result.MeasuredType)
	assert.Equal(t, float64(5000), result.Quantity)
	assert.Equal(t, "acct", result.ConsumerID)
	assert.Equal(t, string(metadata.Volume), result.ResourceType)
}

// =============================================================================
// createCrossRegionRestoreMetrics — protocol / EnableFilesBackupBilling gating
// =============================================================================

func TestCreateCrossRegionRestoreMetrics_MissingProtocols_Skipped(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()
	now := time.Now()

	attrs := crossRegionAttrs("vol-uuid", "acct", "deploy", "", "us-west2", 5000)
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, now, attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, now)
	assert.Nil(t, result)
}

func TestCreateCrossRegionRestoreMetrics_SANProtocol_BillingDisabled_EmitsMetric(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()
	config.EnableFilesBackupBilling = false
	now := time.Now()

	attrs := crossRegionAttrs("vol-uuid", "acct", "deploy", "ISCSI", "us-west2", 5000)
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, now, attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, now)
	assert.NotNil(t, result)
	assert.Equal(t, float64(5000), result.Quantity)
	assert.Equal(t, string(metadata.CbsCrossRegionVolumeRestoreTransferBytes), result.MeasuredType)
}

func TestCreateCrossRegionRestoreMetrics_NASProtocol_BillingEnabled_EmitsMetric(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()
	config.EnableFilesBackupBilling = true
	now := time.Now()

	attrs := crossRegionAttrs("vol-uuid", "acct", "deploy", "NFSV3", "us-west2", 5000)
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, now, attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, now)
	assert.NotNil(t, result)
	assert.Equal(t, float64(5000), result.Quantity)
	assert.Equal(t, string(metadata.CbsCrossRegionVolumeRestoreTransferBytes), result.MeasuredType)
}

func TestCreateCrossRegionRestoreMetrics_NASProtocol_BillingDisabled_Skipped(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()
	config.EnableFilesBackupBilling = false
	now := time.Now()

	attrs := crossRegionAttrs("vol-uuid", "acct", "deploy", "NFSV3", "us-west2", 5000)
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, now, attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, now)
	assert.Nil(t, result)
}

// =============================================================================
// createSfrCrossRegionRestoreMetrics tests
// =============================================================================

func TestCreateSfrCrossRegionRestoreMetrics_SfrMetadataNotFound(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-sfr-1", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, time.Now())
	job.BaseModel.ID = 10

	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(10)).Return(nil, errors.New("not found"))

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateSfrCrossRegionRestoreMetrics_ZeroFileSize(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-sfr-2", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, time.Now())
	job.BaseModel.ID = 11

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 0, BackupUUID: "b-1", VolumeUUID: "v-1"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(11)).Return(sfrMeta, nil)

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateSfrCrossRegionRestoreMetrics_BackupNotFound(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-sfr-3", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, time.Now())
	job.BaseModel.ID = 12

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 100, BackupUUID: "b-missing", VolumeUUID: "v-1"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(12)).Return(sfrMeta, nil)

	vol := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "v-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{},
	}
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "b-missing").Return(nil, errors.New("not found"))

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateSfrCrossRegionRestoreMetrics_BackupNotFound_HardError(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-sfr-3b", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, time.Now())
	job.BaseModel.ID = 30

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 100, BackupUUID: "b-err", VolumeUUID: "v-1"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(30)).Return(sfrMeta, nil)

	vol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "v-1"}}
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "b-err").Return(nil, errors.New("connection refused"))

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateSfrCrossRegionRestoreMetrics_BackupVaultNil(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-sfr-4", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, time.Now())
	job.BaseModel.ID = 13

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 100, BackupUUID: "b-1", VolumeUUID: "v-1"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(13)).Return(sfrMeta, nil)

	vol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "v-1"}}
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	backup := &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: "b-1"}, BackupVault: nil}
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "b-1").Return(backup, nil)

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateSfrCrossRegionRestoreMetrics_NotCrossRegion(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-sfr-5", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, time.Now())
	job.BaseModel.ID = 14

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 100, BackupUUID: "b-1", VolumeUUID: "v-1"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(14)).Return(sfrMeta, nil)

	vol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "v-1"}}
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	backup := &datamodel.Backup{
		BaseModel:   datamodel.BaseModel{UUID: "b-1"},
		BackupVault: &datamodel.BackupVault{BackupVaultType: "IN_REGION"},
	}
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "b-1").Return(backup, nil)

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateSfrCrossRegionRestoreMetrics_BackupRegionNil(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-sfr-nil-region", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, time.Now())
	job.BaseModel.ID = 20

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 100, BackupUUID: "b-1", VolumeUUID: "v-1"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(20)).Return(sfrMeta, nil)

	vol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "v-1"}}
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	backup := &datamodel.Backup{
		BaseModel: datamodel.BaseModel{UUID: "b-1"},
		BackupVault: &datamodel.BackupVault{
			BackupVaultType:  activities.CrossRegionBackupType,
			BackupRegionName: nil,
		},
	}
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "b-1").Return(backup, nil)

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateSfrCrossRegionRestoreMetrics_BackupRegionMatchesCurrentRegion(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-sfr-same-region", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, time.Now())
	job.BaseModel.ID = 21

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 100, BackupUUID: "b-1", VolumeUUID: "v-1"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(21)).Return(sfrMeta, nil)

	vol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "v-1"}}
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	backup := newCrossRegionBackup("b-1", "b-1", 5000, config.RegionName)
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "b-1").Return(backup, nil)

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateSfrCrossRegionRestoreMetrics_VolumeNotFound(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-sfr-6", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, time.Now())
	job.BaseModel.ID = 15

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 100, BackupUUID: "b-1", VolumeUUID: "v-missing"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(15)).Return(sfrMeta, nil)

	backup := newCrossRegionBackup("b-1", "b-1", 5000, "eu-west1")
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "b-1").Return(backup, nil)

	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{}, nil)

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateSfrCrossRegionRestoreMetrics_Success(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()
	now := time.Now()

	job := newTestJob("job-sfr-ok", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, now)
	job.BaseModel.ID = 16

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 4096, FileCount: 10, BackupUUID: "sfr-b-1", VolumeUUID: "sfr-v-uuid"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(16)).Return(sfrMeta, nil)

	backup := newCrossRegionBackup("sfr-b-1", "sfr-b-1", 10000, "eu-west1")
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "sfr-b-1").Return(backup, nil)

	vol := newVolume("sfr-v-uuid", "sfr-vol", "sfr-acct", "sfr-deploy", "")
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, now)
	assert.NotNil(t, result)
	assert.Equal(t, float64(4096), result.Quantity)
	assert.Equal(t, string(metadata.CbsCrossRegionVolumeRestoreTransferBytes), result.MeasuredType)
	assert.Equal(t, "sfr-acct", result.ConsumerID)
}

// =============================================================================
// createSfrCrossRegionRestoreMetrics — protocol / EnableFilesBackupBilling gating
// =============================================================================

func TestCreateSfrCrossRegionRestoreMetrics_NilProtocols_Skipped(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()

	job := newTestJob("job-sfr-np", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, time.Now())
	job.BaseModel.ID = 50

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 4096, BackupUUID: "sfr-b-1", VolumeUUID: "sfr-v-uuid"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(50)).Return(sfrMeta, nil)

	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "sfr-v-uuid"},
		Name:      "sfr-vol",
		Account:   &datamodel.Account{Name: "sfr-acct"},
		Pool:      &datamodel.Pool{DeploymentName: "sfr-deploy"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			AccountName: "sfr-acct",
		},
	}
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	backup := newCrossRegionBackup("sfr-b-1", "sfr-b-1", 10000, "eu-west1")
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "sfr-b-1").Return(backup, nil)

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, time.Now())
	assert.Nil(t, result)
}

func TestCreateSfrCrossRegionRestoreMetrics_SANProtocol_BillingDisabled_EmitsMetric(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()
	config.EnableFilesBackupBilling = false
	now := time.Now()

	job := newTestJob("job-sfr-san", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, now)
	job.BaseModel.ID = 51

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 4096, FileCount: 5, BackupUUID: "sfr-b-1", VolumeUUID: "sfr-v-uuid"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(51)).Return(sfrMeta, nil)

	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "sfr-v-uuid"},
		Name:      "sfr-vol",
		Account:   &datamodel.Account{Name: "sfr-acct"},
		Pool:      &datamodel.Pool{DeploymentName: "sfr-deploy"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			AccountName: "sfr-acct",
			Protocols:   []string{"ISCSI"},
		},
	}
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	backup := newCrossRegionBackup("sfr-b-1", "sfr-b-1", 10000, "eu-west1")
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "sfr-b-1").Return(backup, nil)

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, now)
	assert.NotNil(t, result)
	assert.Equal(t, float64(4096), result.Quantity)
	assert.Equal(t, string(metadata.CbsCrossRegionVolumeRestoreTransferBytes), result.MeasuredType)
}

func TestCreateSfrCrossRegionRestoreMetrics_NASProtocol_BillingEnabled_EmitsMetric(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()
	config.EnableFilesBackupBilling = true
	now := time.Now()

	job := newTestJob("job-sfr-nas-en", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, now)
	job.BaseModel.ID = 52

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 4096, FileCount: 5, BackupUUID: "sfr-b-1", VolumeUUID: "sfr-v-uuid"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(52)).Return(sfrMeta, nil)

	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "sfr-v-uuid"},
		Name:      "sfr-vol",
		Account:   &datamodel.Account{Name: "sfr-acct"},
		Pool:      &datamodel.Pool{DeploymentName: "sfr-deploy"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			AccountName: "sfr-acct",
			Protocols:   []string{"NFSV3"},
		},
	}
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	backup := newCrossRegionBackup("sfr-b-1", "sfr-b-1", 10000, "eu-west1")
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "sfr-b-1").Return(backup, nil)

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, now)
	assert.NotNil(t, result)
	assert.Equal(t, float64(4096), result.Quantity)
	assert.Equal(t, string(metadata.CbsCrossRegionVolumeRestoreTransferBytes), result.MeasuredType)
}

func TestCreateSfrCrossRegionRestoreMetrics_NASProtocol_BillingDisabled_Skipped(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	config := defaultConfig()
	config.EnableFilesBackupBilling = false
	now := time.Now()

	job := newTestJob("job-sfr-nas-dis", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "vol-sfr", 100, now)
	job.BaseModel.ID = 53

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 4096, FileCount: 5, BackupUUID: "sfr-b-1", VolumeUUID: "sfr-v-uuid"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(53)).Return(sfrMeta, nil)

	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "sfr-v-uuid"},
		Name:      "sfr-vol",
		Account:   &datamodel.Account{Name: "sfr-acct"},
		Pool:      &datamodel.Pool{DeploymentName: "sfr-deploy"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			AccountName: "sfr-acct",
			Protocols:   []string{"NFSV3"},
		},
	}
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	backup := newCrossRegionBackup("sfr-b-1", "sfr-b-1", 10000, "eu-west1")
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "sfr-b-1").Return(backup, nil)

	result := createSfrCrossRegionRestoreMetrics(ctx, vcpDB, config, job, now)
	assert.Nil(t, result)
}

// =============================================================================
// fetchRestoreJobs tests
// =============================================================================

func TestFetchRestoreJobs_WithoutSFR(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	now := time.Now()
	since := now.Add(-1 * time.Hour)

	jobs := []*datamodel.Job{
		newTestJob("j-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "v-1", 1, now),
	}
	vcpDB.On("GetJobsWithCondition", mock.Anything, mock.Anything).Return(jobs, nil)

	result, err := fetchRestoreJobs(ctx, vcpDB, since, now, false)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestFetchRestoreJobs_WithSFR(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()
	now := time.Now()
	since := now.Add(-1 * time.Hour)

	jobs := []*datamodel.Job{
		newTestJob("j-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "v-1", 1, now),
		newTestJob("j-2", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "v-2", 1, now),
	}
	vcpDB.On("GetJobsWithCondition", mock.Anything, mock.Anything).Return(jobs, nil)

	result, err := fetchRestoreJobs(ctx, vcpDB, since, now, true)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
}

// =============================================================================
// assembleMetadata tests
// =============================================================================

func TestAssembleMetadata_WithPool(t *testing.T) {
	config := defaultConfig()
	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
		Name:      "test-vol",
		Account:   &datamodel.Account{Name: "acct-name"},
		Pool:      &datamodel.Pool{DeploymentName: "pool-deploy"},
	}
	backup := newCrossRegionBackup("b-1", "b-1", 9999, "us-west2")

	met := assembleMetadata(vol, backup, config)
	assert.Equal(t, "vol-uuid", *met.ResourceUUID)
	assert.Equal(t, metadata.Volume, met.ResourceType)
	assert.Equal(t, "test-vol", *met.ResourceName)
	assert.Equal(t, "us-east4", *met.RegionName)
	assert.Equal(t, int64(9999), *met.SizeInBytes)
	assert.Equal(t, "pool-deploy", *met.DeploymentName)
	assert.Equal(t, "acct-name", *met.AccountName)
	assert.Equal(t, "us-west2", *met.BackupRegionName)
	assert.Equal(t, "us-east4", *met.SourceRegionName)
}

func TestAssembleMetadata_WithVolumeAttributesDeployment(t *testing.T) {
	config := defaultConfig()
	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
		Name:      "test-vol",
		Pool:      nil,
		VolumeAttributes: &datamodel.VolumeAttributes{
			DeploymentName: "attr-deploy",
			AccountName:    "attr-acct",
		},
	}
	backup := newCrossRegionBackup("b-1", "b-1", 100, "eu-west1")

	met := assembleMetadata(vol, backup, config)
	assert.Equal(t, "attr-deploy", *met.DeploymentName)
}

func TestAssembleMetadata_EmptyDeployment(t *testing.T) {
	config := defaultConfig()
	vol := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-uuid"},
		Name:             "test-vol",
		Pool:             nil,
		VolumeAttributes: nil,
	}
	backup := newCrossRegionBackup("b-1", "b-1", 100, "eu-west1")

	met := assembleMetadata(vol, backup, config)
	assert.Equal(t, EmptyDeploymentName, *met.DeploymentName)
}

func TestAssembleMetadata_NoBackupRegion(t *testing.T) {
	config := defaultConfig()
	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
		Name:      "test-vol",
		Pool:      &datamodel.Pool{DeploymentName: "d"},
	}
	backup := &datamodel.Backup{
		BaseModel:   datamodel.BaseModel{UUID: "b-1"},
		SizeInBytes: 100,
		BackupVault: &datamodel.BackupVault{
			BackupVaultType:  activities.CrossRegionBackupType,
			BackupRegionName: nil,
		},
	}

	met := assembleMetadata(vol, backup, config)
	assert.Nil(t, met.BackupRegionName)
	assert.Equal(t, "us-east4", *met.SourceRegionName)
}

// =============================================================================
// getVolumeAccountName tests
// =============================================================================

func TestGetVolumeAccountName_FromAccount(t *testing.T) {
	vol := &datamodel.Volume{Account: &datamodel.Account{Name: "from-account"}}
	assert.Equal(t, "from-account", getVolumeAccountName(vol))
}

func TestGetVolumeAccountName_FromVolumeAttributes(t *testing.T) {
	vol := &datamodel.Volume{
		Account:          nil,
		VolumeAttributes: &datamodel.VolumeAttributes{AccountName: "from-attrs"},
	}
	assert.Equal(t, "from-attrs", getVolumeAccountName(vol))
}

func TestGetVolumeAccountName_Empty(t *testing.T) {
	vol := &datamodel.Volume{Account: nil, VolumeAttributes: nil}
	assert.Equal(t, "", getVolumeAccountName(vol))
}

func TestGetVolumeAccountName_EmptyAccountName(t *testing.T) {
	vol := &datamodel.Volume{
		Account:          &datamodel.Account{Name: ""},
		VolumeAttributes: &datamodel.VolumeAttributes{AccountName: "fallback"},
	}
	assert.Equal(t, "fallback", getVolumeAccountName(vol))
}

// =============================================================================
// setRegionMetadataForBilling tests
// =============================================================================

func TestSetRegionMetadataForBilling_NilHM(t *testing.T) {
	rm := metadata.ResourceMetadata{}
	rm.SetBackupRegionName("region")
	setRegionMetadataForBilling(nil, rm)
}

func TestSetRegionMetadataForBilling_NoBothRegions(t *testing.T) {
	hm := &datamodel2.HydratedMetrics{}
	rm := metadata.ResourceMetadata{}
	setRegionMetadataForBilling(hm, rm)
	assert.Nil(t, hm.Metadata)
}

func TestSetRegionMetadataForBilling_BackupRegionOnly(t *testing.T) {
	hm := &datamodel2.HydratedMetrics{}
	rm := metadata.ResourceMetadata{}
	rm.SetBackupRegionName("us-west2")
	setRegionMetadataForBilling(hm, rm)

	var extra map[string]string
	assert.NoError(t, json.Unmarshal(hm.Metadata, &extra))
	assert.Equal(t, "us-west2", extra["backup_region_name"])
	_, ok := extra["source_region_name"]
	assert.False(t, ok)
}

func TestSetRegionMetadataForBilling_SourceRegionOnly(t *testing.T) {
	hm := &datamodel2.HydratedMetrics{}
	rm := metadata.ResourceMetadata{}
	rm.SetSourceRegionName("us-east4")
	setRegionMetadataForBilling(hm, rm)

	var extra map[string]string
	assert.NoError(t, json.Unmarshal(hm.Metadata, &extra))
	assert.Equal(t, "us-east4", extra["source_region_name"])
}

func TestSetRegionMetadataForBilling_BothRegions(t *testing.T) {
	hm := &datamodel2.HydratedMetrics{}
	rm := metadata.ResourceMetadata{}
	rm.SetBackupRegionName("us-west2")
	rm.SetSourceRegionName("us-east4")
	setRegionMetadataForBilling(hm, rm)

	var extra map[string]string
	assert.NoError(t, json.Unmarshal(hm.Metadata, &extra))
	assert.Equal(t, "us-west2", extra["backup_region_name"])
	assert.Equal(t, "us-east4", extra["source_region_name"])
}

// =============================================================================
// getSfrRestoredVolume tests
// =============================================================================

func TestGetSfrRestoredVolume_Success(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()

	vol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "sfr-vol-uuid"}}
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	result, err := getSfrRestoredVolume(ctx, vcpDB, "sfr-vol-uuid")
	assert.NoError(t, err)
	assert.Equal(t, "sfr-vol-uuid", result.UUID)
}

func TestGetSfrRestoredVolume_NotFound(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()

	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{}, nil)

	result, err := getSfrRestoredVolume(ctx, vcpDB, "missing-uuid")
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestGetSfrRestoredVolume_Error(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	ctx := context.Background()

	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("err"))

	result, err := getSfrRestoredVolume(ctx, vcpDB, "err-uuid")
	assert.Error(t, err)
	assert.Nil(t, result)
}

// =============================================================================
// Full SFR E2E through ProcessRestoreBillingMetrics
// =============================================================================

func TestProcessRestoreBillingMetrics_SFREnabled_Success(t *testing.T) {
	vcpDB := new(mockCrossRegionVCPStorage)
	metricsDB := new(mockCrossRegionMetricsStorage)
	ctx := context.Background()
	config := defaultConfig()
	config.EnableSFRCrossRegionRestoreBilling = true
	now := time.Now()
	jobUpdatedAt := now.Add(-3 * time.Minute)

	sfrJob := newTestJob("sfr-job-e2e", string(datamodel.JobTypeRestoreFilesBackup), string(datamodel.JobsStateDONE), "sfr-vol", 200, jobUpdatedAt)
	sfrJob.BaseModel.ID = 99

	metricsDB.On("GetRestoreTimestamp", mock.Anything).Return(nil, nil)
	metricsDB.On("UpdateRestoreTimestamp", mock.Anything, now).Return(nil)
	vcpDB.On("GetJobsWithCondition", mock.Anything, mock.Anything).Return([]*datamodel.Job{sfrJob}, nil)

	sfrMeta := &datamodel.SfrMetadata{FilesSize: 8192, FileCount: 3, BackupUUID: "sfr-bkp", VolumeUUID: "sfr-vol-uuid"}
	vcpDB.On("GetSfrMetadataByJobID", mock.Anything, int64(99)).Return(sfrMeta, nil)

	backup := newCrossRegionBackup("sfr-bkp", "sfr-bkp", 50000, "asia-east1")
	vcpDB.On("GetBackupWithVaultByUUID", mock.Anything, "sfr-bkp").Return(backup, nil)

	vol := newVolume("sfr-vol-uuid", "sfr-vol", "sfr-acct", "sfr-deploy", "")
	vcpDB.On("ListVolumesWithPagination", mock.Anything, mock.Anything, mock.Anything).Return([]*datamodel.Volume{vol}, nil)

	result, err := ProcessRestoreBillingMetrics(ctx, vcpDB, metricsDB, config, now)
	assert.NoError(t, err)
	assert.Len(t, result.HydratedMetricsDataModel, 1)
	assert.Equal(t, float64(8192), result.HydratedMetricsDataModel[0].Quantity)
	metricsDB.AssertCalled(t, "UpdateRestoreTimestamp", mock.Anything, now)
}

// =============================================================================
// Edge cases: volume with VolumeAttributes but empty DeploymentName
// =============================================================================

func TestCreateCrossRegionRestoreMetrics_EmptyDeploymentName_UsesDefault(t *testing.T) {
	ctx := context.Background()
	config := defaultConfig()
	now := time.Now()

	attrs := crossRegionAttrs("vol-uuid", "acct", "", "NFSV3", "us-west2", 5000)
	job := newTestJobWithAttrs("job-1", string(datamodel.JobTypeRestoreBackup), string(datamodel.JobsStateDONE), "vol-1", 100, now, attrs)

	result := createCrossRegionRestoreMetrics(ctx, config, job, now)
	assert.NotNil(t, result)
	assert.Equal(t, string(metadata.CbsCrossRegionVolumeRestoreTransferBytes), result.MeasuredType)
	assert.Equal(t, float64(5000), result.Quantity)
	assert.Equal(t, EmptyDeploymentName, result.DeploymentName)
}

func TestAssembleMetadata_VolumeAttrsEmptyDeployment(t *testing.T) {
	config := defaultConfig()
	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
		Name:      "test-vol",
		Pool:      nil,
		VolumeAttributes: &datamodel.VolumeAttributes{
			DeploymentName: "",
			AccountName:    "acct",
		},
	}
	backup := newCrossRegionBackup("b-1", "b-1", 100, "eu-west1")

	met := assembleMetadata(vol, backup, config)
	assert.Equal(t, EmptyDeploymentName, *met.DeploymentName)
}
