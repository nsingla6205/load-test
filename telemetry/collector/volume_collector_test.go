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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	datamodel2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

type mockVolumeStorage struct {
	mock.Mock
	database.Storage
}

func (m *mockVolumeStorage) ListVolumesWithAccounts(ctx context.Context) ([]*datamodel.Volume, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*datamodel.Volume), args.Error(1)
}

func (m *mockVolumeStorage) ListVolumesForTelemetryMetrics(ctx context.Context) ([]*database.VolumeMetricsData, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*database.VolumeMetricsData), args.Error(1)
}

func (m *mockVolumeStorage) ListExpertModeVolumesForTelemetryMetrics(ctx context.Context, pagination *utils.Pagination) ([]*database.ExpertModeVolumeMetricsData, error) {
	args := m.Called(ctx, pagination)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*database.ExpertModeVolumeMetricsData), args.Error(1)
}

func (m *mockVolumeStorage) GetBackupVault(ctx context.Context, backupVaultID string) (*datamodel.BackupVault, error) {
	args := m.Called(ctx, backupVaultID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*datamodel.BackupVault), args.Error(1)
}

func (m *mockVolumeStorage) GetSfrMetricsByTimeRange(ctx context.Context, startTime, endTime time.Time) (map[string]datamodel.SfrMetricsAggregate, error) {
	args := m.Called(ctx, startTime, endTime)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]datamodel.SfrMetricsAggregate), args.Error(1)
}

func (m *mockVolumeStorage) GetMultipleBackupVaults(ctx context.Context, conditions [][]interface{}) ([]*datamodel.BackupVault, error) {
	args := m.Called(ctx, conditions)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*datamodel.BackupVault), args.Error(1)
}

func (m *mockVolumeStorage) ListAccountsForTelemetry(ctx context.Context, pagination *utils.Pagination) ([]*database.AccountTelemetryData, error) {
	args := m.Called(ctx, pagination)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*database.AccountTelemetryData), args.Error(1)
}

// GetBackupMetrics is provided here so mockVolumeStorage satisfies the database.Storage
// interface; returns empty by default since the volume collector does not call it directly.
func (m *mockVolumeStorage) GetBackupMetrics(ctx context.Context, conditions [][]interface{}, pagination *utils.Pagination) ([]*datamodel.Backup, error) {
	for _, e := range m.ExpectedCalls {
		if e.Method == "GetBackupMetrics" {
			args := m.Called(ctx, conditions, pagination)
			if args.Get(0) == nil {
				return nil, args.Error(1)
			}
			return args.Get(0).([]*datamodel.Backup), args.Error(1)
		}
	}
	return []*datamodel.Backup{}, nil
}

// GetBackupChainMetrics is used by the backup collector (not the volume collector).
// Provided here so mockVolumeStorage satisfies the database.Storage interface; returns
// empty by default.
func (m *mockVolumeStorage) GetBackupChainMetrics(ctx context.Context, conditions [][]interface{}, pagination *utils.Pagination) ([]*datamodel.Backup, error) {
	for _, e := range m.ExpectedCalls {
		if e.Method == "GetBackupChainMetrics" {
			args := m.Called(ctx, conditions, pagination)
			if args.Get(0) == nil {
				return nil, args.Error(1)
			}
			return args.Get(0).([]*datamodel.Backup), args.Error(1)
		}
	}
	return []*datamodel.Backup{}, nil
}

// GetDistinctVolumeGCBDRVaultPairs satisfies the Storage interface. The volume collector
// no longer calls this directly (detached-vault BMF is handled by collectDetachedVaultBMF
// via GetBackupChainMetrics). Tests that need to stub the method set an explicit
// expectation; all other tests return an empty slice automatically.
func (m *mockVolumeStorage) GetDistinctVolumeGCBDRVaultPairs(ctx context.Context) ([]database.VolumeVaultPair, error) {
	for _, e := range m.ExpectedCalls {
		if e.Method == "GetDistinctVolumeGCBDRVaultPairs" {
			args := m.Called(ctx)
			if args.Get(0) == nil {
				return nil, args.Error(1)
			}
			return args.Get(0).([]database.VolumeVaultPair), args.Error(1)
		}
	}
	return []database.VolumeVaultPair{}, nil
}

func Test_GetVolumeMetrics_ReturnsMetrics(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	// Create poolMetadataMap for testing
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)
	poolMetadataMap[1] = metadata.ResourceMetadata{
		ResourceType: metadata.Volume,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// VolumeAllocatedThroughput should be in separate field
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)
	// BackupEnabledVolumeAllocatedSize should only be in HydratedMetricsDataModel when EnableBackupBillingMetrics is true
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Check hydrated metrics data model
	assert.Equal(t, string(metadata.BackupEnabledVolumeAllocatedSize), result.HydratedMetricsDataModel[0].MeasuredType)
	assert.Equal(t, string(metadata.Volume), result.HydratedMetricsDataModel[0].ResourceType)
	assert.Equal(t, "Account1", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "Volume1", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, "us-east-1", result.HydratedMetricsDataModel[0].Location)
	assert.Equal(t, float64(2048), result.HydratedMetricsDataModel[0].Quantity)

	// Verify the type is correct
	assert.IsType(t, datamodel2.HydratedMetrics{}, result.HydratedMetricsDataModel[0])
}

func Test_GetVolumeMetrics_MultipleVolumes(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	// Create poolMetadataMap for testing
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	backupChainBytes1 := int64(1024)
	backupChainBytes2 := int64(2048)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment-1",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes1,
			},
		},
		{
			UUID:        "volume-uuid-2",
			Name:        "Volume2",
			SizeInBytes: 4096,
			PoolID:      2,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account2",
				DeploymentName: "test-deployment-2",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes2,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// VolumeAllocatedThroughput should be in separate field (2 volumes)
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 0)
	// BackupEnabledVolumeAllocatedSize should only be in HydratedMetricsDataModel when EnableBackupBillingMetrics is true (2 volumes)
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 2)

	// Check hydrated metrics - Volume1
	assert.Equal(t, "Account1", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "Volume1", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, string(metadata.BackupEnabledVolumeAllocatedSize), result.HydratedMetricsDataModel[0].MeasuredType)
	assert.Equal(t, float64(2048), result.HydratedMetricsDataModel[0].Quantity)

	// Check hydrated metrics - Volume2
	assert.Equal(t, "Account2", result.HydratedMetricsDataModel[1].ConsumerID)
	assert.Equal(t, "Volume2", result.HydratedMetricsDataModel[1].ResourceName)
	assert.Equal(t, string(metadata.BackupEnabledVolumeAllocatedSize), result.HydratedMetricsDataModel[1].MeasuredType)
	assert.Equal(t, float64(4096), result.HydratedMetricsDataModel[1].Quantity)
}

func Test_GetVolumeMetrics_EmptyVolumes(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)
	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetrics)
	assert.Empty(t, result.HydratedMetricsDataModel)
}

func Test_GetVolumeMetrics_CmekBackupBillingDisabled_SkipsCmekVaults(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
				Protocols:      []string{"NFSv3"},
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID:    "bv-1",
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel: datamodel.BaseModel{UUID: "bv-1"},
			CmekAttributes: &datamodel.CmekAttributes{
				KmsConfigResourcePath: nillable.GetStringPtr("projects/test/locations/us/keyRings/kr/cryptoKeys/key/cryptoKeyVersions/1"),
			},
		},
	}
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(backupVaults, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true
	config.EnableCmekBackupBilling = false

	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// CMEK billing disabled: no BackupEnabledVolumeAllocatedSize metrics for CMEK vaults
	assert.Len(t, result.HydratedMetricsDataModel, 0)
}

func Test_GetVolumeMetrics_CmekBackupBillingEnabled_IncludesCmekVaults(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
				Protocols:      []string{"NFSv3"},
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID:    "bv-1",
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel: datamodel.BaseModel{UUID: "bv-1"},
			CmekAttributes: &datamodel.CmekAttributes{
				KmsConfigResourcePath: nillable.GetStringPtr("projects/test/locations/us/keyRings/kr/cryptoKeys/key/cryptoKeyVersions/1"),
			},
		},
	}
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(backupVaults, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true
	config.EnableCmekBackupBilling = true

	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// CMEK billing enabled: BackupEnabledVolumeAllocatedSize metric should be present
	require.Len(t, result.HydratedMetricsDataModel, 1)
	assert.Equal(t, string(metadata.BackupEnabledVolumeAllocatedSize), result.HydratedMetricsDataModel[0].MeasuredType)
	assert.Equal(t, float64(2048), result.HydratedMetricsDataModel[0].Quantity)
}

func Test_GetVolumeMetrics_CmekBackupBillingDisabled_VaultNotFound_SkipsBilling(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
				Protocols:      []string{"NFSv3"},
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID:    "bv-1",
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	// Simulate vault not returned from GetMultipleBackupVaults
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true
	config.EnableCmekBackupBilling = false
	// Make sure CRB gating does not interfere; this is a pure CMEK case.
	config.EnableCrossRegionBackupBillingMetrics = true

	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Vault not found while CMEK billing disabled: we conservatively skip billing.
	assert.Len(t, result.HydratedMetricsDataModel, 0)
}

func Test_GetVolumeMetrics_GcbdrBackupBillingDisabled_SkipsGcbdrVaults(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
				Protocols:      []string{"NFSv3"},
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID:    "bv-1",
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel:   datamodel.BaseModel{UUID: "bv-1"},
			ServiceType: datamodel.ServiceTypeCrossProject,
		},
	}
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(backupVaults, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true
	config.EnableGcbdrBackupBilling = false

	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	assert.Len(t, result.HydratedMetricsDataModel, 0)
}

func Test_GetVolumeMetrics_GcbdrBackupBillingEnabled_IncludesGcbdrVaults(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
				Protocols:      []string{"NFSv3"},
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID:    "bv-1",
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel:   datamodel.BaseModel{UUID: "bv-1"},
			ServiceType: datamodel.ServiceTypeCrossProject,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{UUID: "vault-account-uuid"},
				Name:      "VaultOwnerProject",
			},
		},
	}
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(backupVaults, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true
	// The CrossProject billing account redirect is gated by EnableGcbdrBackupBilling.
	config.EnableGcbdrBackupBilling = true

	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	require.Len(t, result.HydratedMetricsDataModel, 1)
	assert.Equal(t, string(metadata.BackupEnabledVolumeAllocatedSize), result.HydratedMetricsDataModel[0].MeasuredType)
	assert.Equal(t, float64(2048), result.HydratedMetricsDataModel[0].Quantity)
	assert.Equal(t, "VaultOwnerProject", result.HydratedMetricsDataModel[0].ConsumerID,
		"CrossProject vault should bill to vault's owning project")
}

func Test_GetVolumeMetrics_CrossProjectVault_BillsToVaultProject(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-cp",
			Name:        "VolumeCrossProject",
			SizeInBytes: 4096,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "VolumeOwnerProject",
				DeploymentName: "test-deployment",
				Protocols:      []string{"NFSv3"},
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID:    "bv-cp",
				BackupChainBytes: &backupChainBytes,
			},
		},
		{
			UUID:        "volume-uuid-gcnv",
			Name:        "VolumeGCNV",
			SizeInBytes: 8192,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "VolumeOwnerProject",
				DeploymentName: "test-deployment",
				Protocols:      []string{"NFSv3"},
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID:    "bv-gcnv",
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel:   datamodel.BaseModel{UUID: "bv-cp"},
			ServiceType: datamodel.ServiceTypeCrossProject,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{UUID: "vault-account-uuid"},
				Name:      "VaultOwnerProject",
			},
		},
		{
			BaseModel:   datamodel.BaseModel{UUID: "bv-gcnv"},
			ServiceType: datamodel.ServiceTypeGCNV,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{UUID: "gcnv-account-uuid"},
				Name:      "GcnvVaultProject",
			},
		},
	}
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(backupVaults, nil)

	// All three billing flags are true — this is the scenario where the old prefetch
	// condition (!CRB || !CMEK || !GCBDR) evaluated to false and the vault map was
	// empty, causing the CrossProject account override to silently fall back to the
	// volume owner.
	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true
	config.EnableCrossRegionBackupBillingMetrics = true
	config.EnableCmekBackupBilling = true
	// The CrossProject billing account redirect is gated by EnableGcbdrBackupBilling.
	config.EnableGcbdrBackupBilling = true

	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	require.Len(t, result.HydratedMetricsDataModel, 2)

	// CrossProject vault: should bill to vault's project
	assert.Equal(t, "VaultOwnerProject", result.HydratedMetricsDataModel[0].ConsumerID,
		"CrossProject vault should bill to vault's owning project, not volume owner")
	assert.Equal(t, float64(4096), result.HydratedMetricsDataModel[0].Quantity)

	// GCNV vault: should bill to volume owner's project
	assert.Equal(t, "VolumeOwnerProject", result.HydratedMetricsDataModel[1].ConsumerID,
		"GCNV vault should bill to volume owner's project")
	assert.Equal(t, float64(8192), result.HydratedMetricsDataModel[1].Quantity)
}

// Test_GetVolumeMetrics_MultiVaultSameVolume_EmitsBMFPerVault verifies that when a
// volume has available backups across multiple vaults (GCBDR vault switching), BMF is
// emitted once per (volume, billing-project) pair. In this scenario:
//
//	V1 is currently attached to Vault3 (active, with chain bytes), but still has
//	  available backups in Vault1 and Vault2 (both detached).
//	  Vault1 and Vault2 both bill to Project1, so only one detached row is emitted for V1.
//	V2 is currently attached to Vault3 (active).
//
// Expected: 3 BMF rows — V1+Vault3 (active, Project2), V2+Vault3 (active, Project2),
// V1+Vault1 (detached, Project1). V1+Vault2 is deduplicated because (V1, Project1) was
// already emitted by V1+Vault1.
//
// Detached-vault rows are produced by collectDetachedVaultBMF (which calls
// GetBackupChainMetrics), not by the per-volume loop.
func Test_GetVolumeMetrics_MultiVaultSameVolume_EmitsBMFPerVault(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()

	v1ChainBytes := int64(500) // V1 currently attached to Vault3 with chain bytes
	v2ChainBytes := int64(600) // V2 currently attached to Vault3 with chain bytes
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume1",
			Name:        "V1",
			SizeInBytes: 1024,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Project4",
				DeploymentName: "v1-deployment",
				Protocols:      []string{"NFSv3"},
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID:    "vault3-uuid",
				BackupChainBytes: &v1ChainBytes,
			},
		},
		{
			UUID:        "volume2",
			Name:        "V2",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Project4",
				DeploymentName: "v2-deployment",
				Protocols:      []string{"NFSv3"},
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID:    "vault3-uuid",
				BackupChainBytes: &v2ChainBytes,
			},
		},
	}
	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	// Vault1 + Vault2 owned by Project1; Vault3 owned by Project2. All cross-project.
	vaults := []*datamodel.BackupVault{
		{
			BaseModel:   datamodel.BaseModel{UUID: "vault1-uuid"},
			Name:        "vault1",
			ServiceType: datamodel.ServiceTypeCrossProject,
			Account:     &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-p1"}, Name: "Project1"},
		},
		{
			BaseModel:   datamodel.BaseModel{UUID: "vault2-uuid"},
			Name:        "vault2",
			ServiceType: datamodel.ServiceTypeCrossProject,
			Account:     &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-p1"}, Name: "Project1"},
		},
		{
			BaseModel:   datamodel.BaseModel{UUID: "vault3-uuid"},
			Name:        "vault3",
			ServiceType: datamodel.ServiceTypeCrossProject,
			Account:     &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-p2"}, Name: "Project2"},
		},
	}
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(vaults, nil)

	// collectDetachedVaultBMF calls GetBackupChainMetrics (paginated) to find detached-vault
	// chain tips. Return one chain tip per (volume, vault) pair so the function groups them
	// and emits a BMF row. vault3 for both volumes is the active vault already covered by
	// the per-volume loop and will be deduplicated via emittedBMF.
	vault1 := vaults[0]
	vault2 := vaults[1]
	vault3 := vaults[2]
	chainTips := []*datamodel.Backup{
		{VolumeUUID: "volume1", BackupVault: vault1},
		{VolumeUUID: "volume1", BackupVault: vault2},
		{VolumeUUID: "volume1", BackupVault: vault3},
		{VolumeUUID: "volume2", BackupVault: vault3},
	}
	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *utils.Pagination) bool {
		return p.Offset == 0
	})).Return(chainTips, nil)
	m.On("GetBackupChainMetrics", mock.Anything, mock.Anything, mock.MatchedBy(func(p *utils.Pagination) bool {
		return p.Offset > 0
	})).Return([]*datamodel.Backup{}, nil)

	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableBackupBillingMetrics:            true,
		EnableFilesBackupBilling:              true,
		EnableGcbdrBackupBilling:              true,
		EnableCrossRegionBackupBillingMetrics: true,
		EnableCmekBackupBilling:               true,
	}
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)

	// 3 BMF rows total. Emission order: active-vault rows come from the per-volume loop;
	// detached-vault rows come from collectDetachedVaultBMF which runs after the loop:
	//   [0] V1 + Vault3 (active)   — Project2, quantity 1024
	//   [1] V2 + Vault3 (active)   — Project2, quantity 2048
	//   [2] V1 + Vault1 (detached) — Project1, quantity 1024  ← V1+Vault2 + V1+Vault3 deduplicated
	require.Len(t, result.HydratedMetricsDataModel, 3,
		"expected 3 BMF rows: V1 active, V2 active, V1 detached (Vault1 only; Vault2+Vault3 deduplicated)")

	// [0] V1 active vault
	assert.Equal(t, "Project2", result.HydratedMetricsDataModel[0].ConsumerID, "V1 active vault bills to Project2")
	assert.Equal(t, float64(1024), result.HydratedMetricsDataModel[0].Quantity)
	assert.Equal(t, "V1", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, "v1-deployment", result.HydratedMetricsDataModel[0].DeploymentName,
		"active-vault BMF uses volume's deployment name")

	// [1] V2 active vault
	assert.Equal(t, "Project2", result.HydratedMetricsDataModel[1].ConsumerID, "V2 active vault bills to Project2")
	assert.Equal(t, float64(2048), result.HydratedMetricsDataModel[1].Quantity)
	assert.Equal(t, "V2", result.HydratedMetricsDataModel[1].ResourceName)

	// [2] V1 detached: Vault1 is first-seen for (V1, Project1); Vault2 and Vault3 are skipped (deduped).
	assert.Equal(t, "Project1", result.HydratedMetricsDataModel[2].ConsumerID, "V1+Vault1 bills to Project1")
	assert.Equal(t, float64(1024), result.HydratedMetricsDataModel[2].Quantity, "detached row billed on volume size")
	assert.Equal(t, "V1", result.HydratedMetricsDataModel[2].ResourceName)
}

func Test_GetVolumeMetrics_GcbdrBackupBillingDisabled_VaultNotFound_SkipsBilling(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
				Protocols:      []string{"NFSv3"},
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID:    "bv-1",
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true
	config.EnableGcbdrBackupBilling = false
	config.EnableCrossRegionBackupBillingMetrics = true
	config.EnableCmekBackupBilling = true

	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	assert.Len(t, result.HydratedMetricsDataModel, 0)
}

func Test_GetVolumeMetrics_GcbdrBackupBillingDisabled_NonGcbdrVaultStillBilled(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
				Protocols:      []string{"NFSv3"},
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID:    "bv-1",
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel:   datamodel.BaseModel{UUID: "bv-1"},
			ServiceType: datamodel.ServiceTypeGCNV,
		},
	}
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(backupVaults, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true
	config.EnableGcbdrBackupBilling = false

	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	require.Len(t, result.HydratedMetricsDataModel, 1)
	assert.Equal(t, string(metadata.BackupEnabledVolumeAllocatedSize), result.HydratedMetricsDataModel[0].MeasuredType)
}

func Test_GetVolumeMetrics_ListVolumesWithAccountsError(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)
	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(nil, assert.AnError)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.Error(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetrics)
	assert.Empty(t, result.HydratedMetricsDataModel)
}

func Test_GetVolumeMetrics_FiltersVolumesWithZeroBackupChainBytes(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	zeroBackupChainBytes := int64(0)
	positiveBackupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment-1",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &zeroBackupChainBytes, // Should be filtered out
			},
		},
		{
			UUID:        "volume-uuid-2",
			Name:        "Volume2",
			SizeInBytes: 4096,
			PoolID:      2,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account2",
				DeploymentName: "test-deployment-2",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &positiveBackupChainBytes, // Should be included
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// VolumeAllocatedThroughput metrics should be generated for both volumes (2 total)
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 0)

	// Only one volume should be processed for BackupEnabledVolumeAllocatedSize (the one with positive backup chain bytes)
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Check that only the second volume is processed for backup billing
	// Metrics are only in HydratedMetricsDataModel, not in HydratedMetrics
}

func Test_GetVolumeMetrics_ProcessesVolumesWithNilDataProtection(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	positiveBackupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			Throughput:  100, // Add throughput so VolumeAllocatedThroughput metric is generated
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment-1",
			},
			DataProtection: nil, // Should be processed (not filtered out)
		},
		{
			UUID:        "volume-uuid-2",
			Name:        "Volume2",
			SizeInBytes: 4096,
			Throughput:  200, // Add throughput so VolumeAllocatedThroughput metric is generated
			PoolID:      2,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account2",
				DeploymentName: "test-deployment-2",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &positiveBackupChainBytes, // Should be included
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Both volumes should generate VolumeAllocatedThroughput metrics
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 2)

	// Only volume with DataProtection and BackupChainBytes > 0 should be processed for backup billing
	// Volume1 has nil DataProtection, so only Volume2 should be included
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Verify that Volume2 (with valid DataProtection) is the one included
	assert.Equal(t, "Account2", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "Volume2", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, float64(4096), result.HydratedMetricsDataModel[0].Quantity)
}

func Test_GetVolumeMetrics_FiltersVolumesWithNilAccount(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			Throughput:  100,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "", // Empty account name should be filtered out
				DeploymentName: "test-deployment-1",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
		{
			UUID:        "volume-uuid-2",
			Name:        "Volume2",
			SizeInBytes: 4096,
			Throughput:  200,
			PoolID:      2,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account2",
				DeploymentName: "test-deployment-2",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Only one volume should generate VolumeAllocatedThroughput (the one with valid account)
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)

	// Only one volume should be processed (the one with valid account)
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Check that only the second volume is processed
	assert.Equal(t, "Account2", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "Volume2", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, float64(4096), result.HydratedMetricsDataModel[0].Quantity)
}

func Test_GetVolumeMetrics_FiltersVolumesWithMissingUUID(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "", // Missing UUID
			Name:        "Volume1",
			SizeInBytes: 2048,
			Throughput:  100,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment-1",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
		{
			UUID:        "volume-uuid-2",
			Name:        "Volume2",
			SizeInBytes: 4096,
			Throughput:  200,
			PoolID:      2,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account2",
				DeploymentName: "test-deployment-2",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Only one volume should generate VolumeAllocatedThroughput (the one with valid UUID)
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)

	// Only one volume should be processed (the one with valid UUID)
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Check that only the second volume is processed
	assert.Equal(t, "Account2", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "Volume2", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, float64(4096), result.HydratedMetricsDataModel[0].Quantity)
}

func Test_GetVolumeMetrics_FiltersVolumesWithMissingName(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "", // Missing name
			SizeInBytes: 2048,
			Throughput:  100,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment-1",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
		{
			UUID:        "volume-uuid-2",
			Name:        "Volume2",
			SizeInBytes: 4096,
			Throughput:  200,
			PoolID:      2,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account2",
				DeploymentName: "test-deployment-2",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Only one volume should generate VolumeAllocatedThroughput (the one with valid name)
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)

	// Only one volume should be processed (the one with valid name)
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Check that only the second volume is processed
	assert.Equal(t, "Account2", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "Volume2", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, float64(4096), result.HydratedMetricsDataModel[0].Quantity)
}

func Test_GetVolumeMetrics_FiltersVolumesWithMissingAccountName(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			Throughput:  100,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "", // Missing account name
				DeploymentName: "test-deployment-1",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
		{
			UUID:        "volume-uuid-2",
			Name:        "Volume2",
			SizeInBytes: 4096,
			Throughput:  200,
			PoolID:      2,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account2",
				DeploymentName: "test-deployment-2",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Only one volume should generate VolumeAllocatedThroughput (the one with valid account name)
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)

	// Only one volume should be processed (the one with valid account name)
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Check that only the second volume is processed
	assert.Equal(t, "Account2", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "Volume2", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, float64(4096), result.HydratedMetricsDataModel[0].Quantity)
}

// Test for the assembleVolumeMetadata function with VolumeMetricsData
func TestAssembleVolumeMetadata(t *testing.T) {
	// Create test volume using VolumeMetricsData
	backupChainBytes := int64(1024)
	volume := &database.VolumeMetricsData{
		UUID:        "test-volume-uuid",
		Name:        "test-volume",
		SizeInBytes: 2048,
		VolumeAttributes: &datamodel.VolumeAttributes{
			AccountName:    "test-account",
			DeploymentName: "test-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupChainBytes: &backupChainBytes,
		},
	}

	// Create test config
	config := &common.TelemetryConfig{
		RegionName: "us-central1",
	}

	// Call the function
	resourceMetadata := assembleVolumeMetadata(volume, config)

	// Assertions
	assert.Equal(t, "test-volume-uuid", derefString(resourceMetadata.ResourceUUID))
	assert.Equal(t, metadata.Volume, resourceMetadata.ResourceType)
	assert.Equal(t, int64(2048), derefInt64(resourceMetadata.SizeInBytes))
	assert.Equal(t, "us-central1", derefString(resourceMetadata.RegionName))
	assert.Equal(t, "test-volume", derefString(resourceMetadata.ResourceName))
	assert.Equal(t, "test-volume", derefString(resourceMetadata.ResourceDisplayName))
	assert.Equal(t, "test-account", derefString(resourceMetadata.AccountName))
	assert.Equal(t, "test-deployment", derefString(resourceMetadata.DeploymentName))
}

// Test that verifies the integration between GetVolumeMetrics and setupHydratedMetricsDataModel
func TestGetVolumeMetrics_HydratedMetricsDataModelIntegration(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "ap-south-1"}
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)
	poolMetadataMap[1] = metadata.ResourceMetadata{
		ResourceType: metadata.Volume,
	}

	backupChainBytes := int64(5000)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-integration",
			Name:        "IntegrationVolume",
			SizeInBytes: 10000,
			Throughput:  150, // Add throughput so VolumeAllocatedThroughput metric is generated
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "IntegrationAccount",
				DeploymentName: "integration-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify that VolumeAllocatedThroughput metric is generated
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)

	// Verify that BackupEnabledVolumeAllocatedSize metric is converted to HydratedMetrics
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Metrics are only in HydratedMetricsDataModel, not in HydratedMetrics

	// Verify the HydratedMetrics data model is correctly populated
	hmBackupVolumeAllocated := result.HydratedMetricsDataModel[0]
	assert.Equal(t, string(metadata.BackupEnabledVolumeAllocatedSize), hmBackupVolumeAllocated.MeasuredType)
	assert.Equal(t, string(metadata.Volume), hmBackupVolumeAllocated.ResourceType)
	assert.Equal(t, "IntegrationAccount", hmBackupVolumeAllocated.ConsumerID)
	assert.Equal(t, "IntegrationVolume", hmBackupVolumeAllocated.ResourceName)
	assert.Equal(t, "ap-south-1", hmBackupVolumeAllocated.Location)
	assert.Equal(t, float64(10000), hmBackupVolumeAllocated.Quantity)

	// Verify timestamp is recent (within last minute)
	timeDiff := time.Since(hmBackupVolumeAllocated.MetricTimestamp)
	assert.True(t, timeDiff < time.Minute, "Timestamp should be recent")
}

// Test for new VolumeAllocatedThroughput functionality
func Test_GetVolumeMetrics_WithThroughputMapping(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	// Create poolMetadataMap with throughput data
	poolThroughput := 250.5
	poolMetadata := metadata.ResourceMetadata{}
	poolMetadata.SetThroughput(poolThroughput)
	poolMetadata.SetResourceID(int64(10))
	poolMetadataMap := map[int64]metadata.ResourceMetadata{
		10: poolMetadata,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-throughput",
			Name:        "ThroughputVolume",
			SizeInBytes: 2048,
			Throughput:  100, // Volume has its own throughput, should use volume throughput
			PoolID:      10,  // Matches the pool in metadata map
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "ThroughputAccount",
				DeploymentName: "throughput-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// VolumeAllocatedThroughput should be in separate field
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)
	// BackupEnabledVolumeAllocatedSize should only be in HydratedMetricsDataModel when EnableBackupBillingMetrics is true
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Check VolumeAllocatedThroughput metric (in separate field)
	assert.Equal(t, metadata.VolumeAllocatedThroughput, result.VolumeAllocatedThroughputHydratedMetrics[0].MeasuredType)
	assert.Equal(t, float64(100), result.VolumeAllocatedThroughputHydratedMetrics[0].Quantity) // Should use volume throughput when available
	assert.Equal(t, "volume-uuid-throughput", derefString(result.VolumeAllocatedThroughputHydratedMetrics[0].Metadata.ResourceUUID))
	assert.Equal(t, "ThroughputVolume", derefString(result.VolumeAllocatedThroughputHydratedMetrics[0].Metadata.ResourceName))
	assert.Equal(t, "ThroughputAccount", derefString(result.VolumeAllocatedThroughputHydratedMetrics[0].Metadata.AccountName))
	assert.Equal(t, metadata.Volume, result.VolumeAllocatedThroughputHydratedMetrics[0].Metadata.ResourceType)

	// BackupEnabledVolumeAllocatedSize metric is only in HydratedMetricsDataModel, not in HydratedMetrics
}

func Test_GetVolumeMetrics_WithManualQos_NoAssignedThroughput_ReturnsZero(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	poolMetadata := metadata.ResourceMetadata{}
	poolMetadata.SetResourceID(int64(40))
	poolMetadataMap := map[int64]metadata.ResourceMetadata{
		40: poolMetadata,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-manual-no-throughput",
			Name:        "ManualNoThroughputVolume",
			SizeInBytes: 2048,
			Throughput:  0,
			Iops:        1200,
			PoolID:      40,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "ManualAccount",
				DeploymentName: "manual-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)
	assert.Equal(t, float64(0), result.VolumeAllocatedThroughputHydratedMetrics[0].Quantity)
}

func Test_GetVolumeMetrics_WithManualQos_AutoGenVpgAssignment(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	poolMetadata := metadata.ResourceMetadata{}
	poolMetadata.SetResourceID(int64(50))
	poolMetadataMap := map[int64]metadata.ResourceMetadata{
		50: poolMetadata,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-autogen-vpg",
			Name:        "AutoGenVpgVolume",
			SizeInBytes: 4096,
			Throughput:  75,
			Iops:        2400,
			PoolID:      50,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "ManualAccount",
				DeploymentName: "manual-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)
	assert.Equal(t, float64(75), result.VolumeAllocatedThroughputHydratedMetrics[0].Quantity)
}

func Test_GetVolumeMetrics_WithManualQos_SharedVpgAssignment(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	poolMetadata := metadata.ResourceMetadata{}
	poolMetadata.SetResourceID(int64(60))
	poolMetadataMap := map[int64]metadata.ResourceMetadata{
		60: poolMetadata,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-manual-vpg-1",
			Name:        "ManualVpgVolume1",
			SizeInBytes: 1024,
			Throughput:  200,
			Iops:        3200,
			PoolID:      60,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "ManualAccount",
				DeploymentName: "manual-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
		{
			UUID:        "volume-uuid-manual-vpg-2",
			Name:        "ManualVpgVolume2",
			SizeInBytes: 2048,
			Throughput:  200,
			Iops:        3200,
			PoolID:      60,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "ManualAccount",
				DeploymentName: "manual-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 2)
	assert.Equal(t, float64(200), result.VolumeAllocatedThroughputHydratedMetrics[0].Quantity)
	assert.Equal(t, float64(200), result.VolumeAllocatedThroughputHydratedMetrics[1].Quantity)
}

func Test_GetVolumeMetrics_WithZeroVolumeThroughput(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	// Create poolMetadataMap with pool throughput for PoolID 20
	poolMetadata := metadata.ResourceMetadata{}
	poolMetadata.SetThroughput(300.0) // Set pool throughput
	poolMetadata.SetResourceID(int64(20))
	poolMetadataMap := map[int64]metadata.ResourceMetadata{
		20: poolMetadata,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-zero-throughput",
			Name:        "ZeroThroughputVolume",
			SizeInBytes: 2048,
			Throughput:  0, // Zero throughput should use pool throughput
			PoolID:      20,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "ZeroAccount",
				DeploymentName: "zero-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// VolumeAllocatedThroughput should be in separate field
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)
	// BackupEnabledVolumeAllocatedSize should only be in HydratedMetricsDataModel when EnableBackupBillingMetrics is true
	assert.Len(t, result.HydratedMetrics, 0)

	// Check VolumeAllocatedThroughput metric with zero volume throughput (should use pool throughput)
	assert.Equal(t, metadata.VolumeAllocatedThroughput, result.VolumeAllocatedThroughputHydratedMetrics[0].MeasuredType)
	assert.Equal(t, float64(300), result.VolumeAllocatedThroughputHydratedMetrics[0].Quantity) // Should use pool throughput
	assert.Equal(t, "volume-uuid-zero-throughput", derefString(result.VolumeAllocatedThroughputHydratedMetrics[0].Metadata.ResourceUUID))
}

func Test_GetVolumeMetrics_WithNilPoolThroughput(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	// Create poolMetadataMap with nil throughput (should default to 0.0)
	poolMetadata := metadata.ResourceMetadata{}
	poolMetadata.SetResourceID(int64(30))
	// Don't set throughput, so it remains nil
	poolMetadataMap := map[int64]metadata.ResourceMetadata{
		30: poolMetadata,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-nil-pool-throughput",
			Name:        "NilPoolThroughputVolume",
			SizeInBytes: 2048,
			Throughput:  150, // Non-zero volume throughput, should use volume throughput
			PoolID:      30,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "NilAccount",
				DeploymentName: "nil-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// VolumeAllocatedThroughput should be in separate field
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)
	// BackupEnabledVolumeAllocatedSize should only be in HydratedMetricsDataModel when EnableBackupBillingMetrics is true
	assert.Len(t, result.HydratedMetrics, 0)

	// Check VolumeAllocatedThroughput metric - should use volume throughput when available
	assert.Equal(t, metadata.VolumeAllocatedThroughput, result.VolumeAllocatedThroughputHydratedMetrics[0].MeasuredType)
	assert.Equal(t, float64(150), result.VolumeAllocatedThroughputHydratedMetrics[0].Quantity) // Should use volume throughput (150)
	assert.Equal(t, "volume-uuid-nil-pool-throughput", derefString(result.VolumeAllocatedThroughputHydratedMetrics[0].Metadata.ResourceUUID))
}

// Test_GetVolumeMetrics_WithResourceTypeMapping tests the resource type mapping logic for zonal vs regional volumes
func Test_GetVolumeMetrics_WithResourceTypeMapping(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	// Create poolMetadataMap with regional HA pool
	poolMetadata := metadata.ResourceMetadata{}
	poolMetadata.SetThroughput(200.0)
	poolMetadata.SetResourceID(int64(100))
	poolMetadata.SetResourceType(metadata.VolumePoolRegionalHA)
	poolMetadataMap := map[int64]metadata.ResourceMetadata{
		100: poolMetadata,
	}

	backupChainBytes := int64(1024)
	volume := &database.VolumeMetricsData{
		UUID:        "volume-uuid-regional",
		Name:        "RegionalVolume",
		SizeInBytes: 3000,
		Throughput:  150,
		PoolID:      100, // Maps to regional HA pool
		VolumeAttributes: &datamodel.VolumeAttributes{
			AccountName:    "RegionalAccount",
			DeploymentName: "regional-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupChainBytes: &backupChainBytes,
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{volume}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// VolumeAllocatedThroughput metric should be generated
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)

	// Check that resource type is correctly mapped to VolumeRegionalHA
	assert.Equal(t, metadata.VolumeRegionalHA, result.VolumeAllocatedThroughputHydratedMetrics[0].Metadata.ResourceType)
	assert.Equal(t, "RegionalVolume", derefString(result.VolumeAllocatedThroughputHydratedMetrics[0].Metadata.ResourceName))
	assert.Equal(t, float64(150), result.VolumeAllocatedThroughputHydratedMetrics[0].Quantity)
}

// Test_GetVolumeMetrics_BackupChainBytesEdgeCases tests various edge cases for backup chain bytes filtering
func Test_GetVolumeMetrics_BackupChainBytesEdgeCases(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

	negativeBackupChainBytes := int64(-100)
	zeroBackupChainBytes := int64(0)
	positiveBackupChainBytes := int64(1)

	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-negative",
			Name:        "VolumeNegative",
			SizeInBytes: 2000,
			Throughput:  100,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment-1",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &negativeBackupChainBytes, // Should be filtered out
			},
		},
		{
			UUID:        "volume-uuid-zero",
			Name:        "VolumeZero",
			SizeInBytes: 3000,
			Throughput:  150,
			PoolID:      2,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account2",
				DeploymentName: "test-deployment-2",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &zeroBackupChainBytes, // Should be filtered out
			},
		},
		{
			UUID:        "volume-uuid-one",
			Name:        "VolumeOne",
			SizeInBytes: 4000,
			Throughput:  200,
			PoolID:      3,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account3",
				DeploymentName: "test-deployment-3",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &positiveBackupChainBytes, // Should be included
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// All volumes should generate VolumeAllocatedThroughput metrics (throughput filtering is separate)
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 3)

	// Only volumes with positive backup chain bytes should be in backup billing metrics
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	// Check that only positive backup chain byte volume is included for backup billing
	assert.Equal(t, "VolumeOne", result.HydratedMetricsDataModel[0].ResourceName)
}

func Test_GetVolumeMetrics_SFRMetricsEnabled(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	config.SFRMetricsEnabled = true

	// Create poolMetadataMap for testing
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)
	poolMetadataMap[1] = metadata.ResourceMetadata{
		ResourceType: metadata.Volume,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	// Mock GetMultipleBackupVaults for backup billing metrics
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	// Mock GetSfrMetricsByTimeRange to return SFR metrics
	sfrMetricsMap := map[string]datamodel.SfrMetricsAggregate{
		"volume-uuid-1": {
			TotalSize:  10240,
			TotalCount: 25,
		},
	}
	m.On("GetSfrMetricsByTimeRange", mock.Anything, mock.Anything, mock.Anything).Return(sfrMetricsMap, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify SFR metrics are included
	assert.Len(t, result.SFRHydratedMetrics, 2) // One for TotalSize, one for TotalCount

	// Check SFR Total Size Restored Bytes metric
	var sizeMetric *entity.HydratedMetric
	var countMetric *entity.HydratedMetric
	for i := range result.SFRHydratedMetrics {
		if result.SFRHydratedMetrics[i].MeasuredType == metadata.SFRTotalSizeRestoredBytes {
			sizeMetric = &result.SFRHydratedMetrics[i]
		}
		if result.SFRHydratedMetrics[i].MeasuredType == metadata.SFRTotalFilesRestoredCount {
			countMetric = &result.SFRHydratedMetrics[i]
		}
	}

	assert.NotNil(t, sizeMetric, "SFR Total Size Restored Bytes metric should be present")
	assert.Equal(t, float64(10240), sizeMetric.Quantity)
	assert.Equal(t, "volume-uuid-1", derefString(sizeMetric.Metadata.ResourceUUID))

	assert.NotNil(t, countMetric, "SFR Total Files Restored Count metric should be present")
	assert.Equal(t, float64(25), countMetric.Quantity)
	assert.Equal(t, "volume-uuid-1", derefString(countMetric.Metadata.ResourceUUID))
}

func Test_GetVolumeMetrics_SFRMetricsEnabled_Error(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	config.SFRMetricsEnabled = true

	// Create poolMetadataMap for testing
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)
	poolMetadataMap[1] = metadata.ResourceMetadata{
		ResourceType: metadata.Volume,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	// Mock GetMultipleBackupVaults for backup billing metrics
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	// Mock GetSfrMetricsByTimeRange to return error
	m.On("GetSfrMetricsByTimeRange", mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err) // Error is logged but doesn't fail the function
	assert.NotNil(t, result)

	// SFR metrics should be empty when error occurs
	assert.Empty(t, result.SFRHydratedMetrics)
}

func Test_GetVolumeMetrics_SFRMetricsEnabled_NoMetricsForVolume(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	config.SFRMetricsEnabled = true

	// Create poolMetadataMap for testing
	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)
	poolMetadataMap[1] = metadata.ResourceMetadata{
		ResourceType: metadata.Volume,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	// Mock GetMultipleBackupVaults for backup billing metrics
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	// Mock GetSfrMetricsByTimeRange to return empty map (no metrics for this volume)
	sfrMetricsMap := map[string]datamodel.SfrMetricsAggregate{}
	m.On("GetSfrMetricsByTimeRange", mock.Anything, mock.Anything, mock.Anything).Return(sfrMetricsMap, nil)

	config.EnableBackupBillingMetrics = true
	config.EnableFilesBackupBilling = true // Enable files backup billing to include in HydratedMetricsDataModel

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// SFR metrics should be empty when volume not in map
	assert.Empty(t, result.SFRHydratedMetrics)
}

func Test_GetVolumeMetrics_Skip_CRB_BMF_Billing_Metrics(t *testing.T) {
	tests := []struct {
		name                                  string
		enableCrossRegionBackupBillingMetrics bool
		volumes                               []*database.VolumeMetricsData
		backupVault                           *datamodel.BackupVault
		backupVaultError                      error
		expectedHydratedMetricsCount          int
		expectedDataModelMetricsCount         int
		expectedThroughputMetricsCount        int
		description                           string
	}{
		{
			name:                                  "Flag disabled - skip cross-region volume BMF billing metrics",
			enableCrossRegionBackupBillingMetrics: false,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-1",
					Name:        "CrossRegionVolume1",
					SizeInBytes: 2048,
					Throughput:  100,
					PoolID:      1,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account1",
						DeploymentName: "test-deployment-1",
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(1024),
						BackupVaultID:    "backup-vault-1",
					},
				},
			},
			backupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "backup-vault-1"},
				Name:             "BackupVault1",
				SourceRegionName: stringPtr("us-east-1"),
				BackupRegionName: stringPtr("us-west-1"), // Different region
			},
			expectedHydratedMetricsCount:   0, // HydratedMetrics is not created for backup billing metrics
			expectedDataModelMetricsCount:  0, // HydratedMetricsDataModel should be skipped
			expectedThroughputMetricsCount: 1, // Throughput metric is independent
			description:                    "Cross-region volume should skip HydratedMetricsDataModel",
		},
		{
			name:                                  "Flag enabled - include cross-region volume BMF billing metrics",
			enableCrossRegionBackupBillingMetrics: true,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-2",
					Name:        "CrossRegionVolume2",
					SizeInBytes: 3072,
					Throughput:  150,
					PoolID:      2,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account2",
						DeploymentName: "test-deployment-2",
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(2048),
						BackupVaultID:    "backup-vault-2",
					},
				},
			},
			backupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "backup-vault-2"},
				Name:             "BackupVault2",
				BackupVaultType:  activities.CrossRegionBackupType,
				SourceRegionName: stringPtr("us-east-1"),
				BackupRegionName: stringPtr("eu-west-1"), // Different region
			},
			expectedHydratedMetricsCount:   0, // HydratedMetrics is not created for backup billing metrics
			expectedDataModelMetricsCount:  1, // HydratedMetricsDataModel should be included
			expectedThroughputMetricsCount: 1,
			description:                    "Cross-region volume should create HydratedMetricsDataModel when flag is enabled",
		},
		{
			name:                                  "Flag disabled - same region volume BMF billing metrics still included",
			enableCrossRegionBackupBillingMetrics: false,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-3",
					Name:        "SameRegionVolume",
					SizeInBytes: 4096,
					Throughput:  200,
					PoolID:      3,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account3",
						DeploymentName: "test-deployment-3",
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(3072),
						BackupVaultID:    "backup-vault-3",
					},
				},
			},
			backupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "backup-vault-3"},
				Name:             "BackupVault3",
				SourceRegionName: stringPtr("us-east-1"),
				BackupRegionName: stringPtr("us-east-1"), // Same region
			},
			expectedHydratedMetricsCount:   0, // HydratedMetrics is not created for backup billing metrics
			expectedDataModelMetricsCount:  1, // Should be included even with flag disabled
			expectedThroughputMetricsCount: 1,
			description:                    "Same region volume should always create HydratedMetricsDataModel",
		},
		{
			name:                                  "Flag disabled - nil BackupVaultID should include billing metrics",
			enableCrossRegionBackupBillingMetrics: false,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-4",
					Name:        "NoVaultVolume",
					SizeInBytes: 5120,
					Throughput:  250,
					PoolID:      4,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account4",
						DeploymentName: "test-deployment-4",
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(4096),
						BackupVaultID:    "", // No backup vault
					},
				},
			},
			expectedHydratedMetricsCount:   0, // HydratedMetrics is not created for backup billing metrics
			expectedDataModelMetricsCount:  1, // Should be included (no vault to check)
			expectedThroughputMetricsCount: 1,
			description:                    "Volume without BackupVaultID should create HydratedMetricsDataModel",
		},
		{
			name:                                  "Flag disabled - nil DataProtection should include billing metrics",
			enableCrossRegionBackupBillingMetrics: false,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-5",
					Name:        "NoDataProtectionVolume",
					SizeInBytes: 6144,
					Throughput:  300,
					PoolID:      5,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account5",
						DeploymentName: "test-deployment-5",
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(5120),
						// No BackupVaultID
					},
				},
			},
			expectedHydratedMetricsCount:   0, // HydratedMetrics is not created for backup billing metrics
			expectedDataModelMetricsCount:  1, // Should be included (no vault ID to check)
			expectedThroughputMetricsCount: 1,
			description:                    "Volume without BackupVaultID should create HydratedMetricsDataModel",
		},
		{
			name:                                  "Flag disabled - GetBackupVault error should skip BMF billing metrics",
			enableCrossRegionBackupBillingMetrics: false,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-6",
					Name:        "ErrorVaultVolume",
					SizeInBytes: 7168,
					Throughput:  350,
					PoolID:      6,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account6",
						DeploymentName: "test-deployment-6",
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(6144),
						BackupVaultID:    "backup-vault-error",
					},
				},
			},
			backupVaultError:               assert.AnError,
			expectedHydratedMetricsCount:   0, // HydratedMetrics is not created for backup billing metrics
			expectedDataModelMetricsCount:  0, // Should be skipped due to error
			expectedThroughputMetricsCount: 1,
			description:                    "GetBackupVault error should skip HydratedMetricsDataModel",
		},
		{
			name:                                  "Flag disabled - nil region names should include billing metrics",
			enableCrossRegionBackupBillingMetrics: false,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-7",
					Name:        "NilRegionVolume",
					SizeInBytes: 8192,
					Throughput:  400,
					PoolID:      7,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account7",
						DeploymentName: "test-deployment-7",
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(7168),
						BackupVaultID:    "backup-vault-7",
					},
				},
			},
			backupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "backup-vault-7"},
				Name:             "BackupVault7",
				SourceRegionName: nil, // Nil region
				BackupRegionName: stringPtr("us-west-1"),
			},
			expectedHydratedMetricsCount:   0, // HydratedMetrics is not created for backup billing metrics
			expectedDataModelMetricsCount:  1, // Should be included (cannot determine cross-region)
			expectedThroughputMetricsCount: 1,
			description:                    "Nil SourceRegionName should create HydratedMetricsDataModel",
		},
		{
			name:                                  "Flag disabled - mixed cross-region and same-region volumes",
			enableCrossRegionBackupBillingMetrics: false,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-8",
					Name:        "SameRegionVolume1",
					SizeInBytes: 9216,
					Throughput:  450,
					PoolID:      8,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account8",
						DeploymentName: "test-deployment-8",
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(8192),
						BackupVaultID:    "backup-vault-8",
					},
				},
				{
					UUID:        "volume-uuid-9",
					Name:        "CrossRegionVolume2",
					SizeInBytes: 10240,
					Throughput:  500,
					PoolID:      9,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account9",
						DeploymentName: "test-deployment-9",
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(9216),
						BackupVaultID:    "backup-vault-9",
					},
				},
			},
			backupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "backup-vault-9"},
				Name:             "BackupVault9",
				SourceRegionName: stringPtr("us-east-1"),
				BackupRegionName: stringPtr("ap-south-1"), // Different region for second volume
			},
			expectedHydratedMetricsCount:   0, // HydratedMetrics is not created for backup billing metrics
			expectedDataModelMetricsCount:  1, // Only same-region creates HydratedMetricsDataModel
			expectedThroughputMetricsCount: 2,
			description:                    "Mixed volumes should filter cross-region from HydratedMetricsDataModel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockVolumeStorage)
			ctx := context.Background()
			config := &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableBackupBillingMetrics:            true,
				EnableFilesBackupBilling:              true, // Enable files backup billing to include in metrics
				EnableCrossRegionBackupBillingMetrics: tt.enableCrossRegionBackupBillingMetrics,
			}
			poolMetadataMap := make(map[int64]metadata.ResourceMetadata)
			m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(tt.volumes, nil)
			m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

			// Mock GetMultipleBackupVaults call - fetches all backup vaults at once
			if tt.backupVaultError != nil {
				m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(nil, tt.backupVaultError)
			} else if tt.backupVault != nil {
				// For mixed volumes test, return both vaults
				if tt.name == "Flag disabled - mixed cross-region and same-region volumes" {
					sameRegionVault := &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "backup-vault-8"},
						Name:             "BackupVault8",
						SourceRegionName: stringPtr("us-east-1"),
						BackupRegionName: stringPtr("us-east-1"), // Same region
					}
					backupVaults := []*datamodel.BackupVault{sameRegionVault, tt.backupVault}
					m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(backupVaults, nil)
				} else {
					backupVaults := []*datamodel.BackupVault{tt.backupVault}
					m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(backupVaults, nil)
				}
			} else {
				// No backup vault needed - return empty slice
				m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
			}

			result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
			assert.NoError(t, err)
			assert.NotNil(t, result)

			// Verify counts
			assert.Len(t, result.HydratedMetrics, tt.expectedHydratedMetricsCount,
				"HydratedMetrics count mismatch: %s", tt.description)
			assert.Len(t, result.HydratedMetricsDataModel, tt.expectedDataModelMetricsCount,
				"HydratedMetricsDataModel count mismatch: %s", tt.description)
			assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, tt.expectedThroughputMetricsCount,
				"VolumeAllocatedThroughputHydratedMetrics count mismatch: %s", tt.description)

			// Additional validations for HydratedMetrics (BackupEnabledVolumeAllocatedSize)
			for i, metric := range result.HydratedMetrics {
				assert.Equal(t, metadata.BackupEnabledVolumeAllocatedSize, metric.MeasuredType,
					"HydratedMetrics[%d] should have BackupEnabledVolumeAllocatedSize type", i)
				assert.NotEmpty(t, derefString(metric.Metadata.ResourceUUID),
					"HydratedMetrics[%d] should have ResourceUUID", i)
				assert.NotEmpty(t, derefString(metric.Metadata.ResourceName),
					"HydratedMetrics[%d] should have ResourceName", i)
			}

			// Additional validations for HydratedMetricsDataModel
			if tt.expectedDataModelMetricsCount > 0 {
				for i, dataMetric := range result.HydratedMetricsDataModel {
					assert.Equal(t, string(metadata.BackupEnabledVolumeAllocatedSize), dataMetric.MeasuredType,
						"HydratedMetricsDataModel[%d] should have BackupEnabledVolumeAllocatedSize type", i)
					assert.NotEmpty(t, dataMetric.ConsumerID,
						"HydratedMetricsDataModel[%d] should have ConsumerID", i)
					assert.NotEmpty(t, dataMetric.ResourceName,
						"HydratedMetricsDataModel[%d] should have ResourceName", i)
				}
			}

			// Verify backup_region_name in JSONB metadata for cross-region volumes
			if tt.enableCrossRegionBackupBillingMetrics && tt.backupVault != nil && tt.backupVault.BackupRegionName != nil && tt.expectedDataModelMetricsCount > 0 {
				for _, dataMetric := range result.HydratedMetricsDataModel {
					if dataMetric.Metadata != nil {
						var extra map[string]string
						err := json.Unmarshal(dataMetric.Metadata, &extra)
						assert.NoError(t, err, "Metadata should be valid JSON")
						assert.Equal(t, *tt.backupVault.BackupRegionName, extra["backup_region_name"],
							"backup_region_name should match backup vault region")
					}
				}
			}

			// Verify throughput metrics are always generated
			for i, throughputMetric := range result.VolumeAllocatedThroughputHydratedMetrics {
				assert.Equal(t, metadata.VolumeAllocatedThroughput, throughputMetric.MeasuredType,
					"ThroughputMetrics[%d] should have VolumeAllocatedThroughput type", i)
			}
		})
	}
}

// Helper function for int64 pointers
func intPtr(i int64) *int64 {
	return &i
}

// Test_GetVolumeMetrics_CRB_With_SFR_Metrics tests that SFR performance metrics are collected
// even when CRB billing metrics are skipped
func Test_GetVolumeMetrics_CRB_With_SFR_Metrics(t *testing.T) {
	tests := []struct {
		name                                  string
		enableCrossRegionBackupBillingMetrics bool
		enableSFRMetrics                      bool
		volumes                               []*database.VolumeMetricsData
		backupVault                           *datamodel.BackupVault
		sfrMetricsMap                         map[string]datamodel.SfrMetricsAggregate
		expectedHydratedMetricsCount          int
		expectedDataModelMetricsCount         int
		expectedSFRMetricsCount               int
		description                           string
	}{
		{
			name:                                  "CRB volume - billing metrics skipped but SFR metrics collected",
			enableCrossRegionBackupBillingMetrics: false,
			enableSFRMetrics:                      true,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-crb-sfr",
					Name:        "CRBVolumeWithSFR",
					SizeInBytes: 2048,
					PoolID:      1,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account1",
						DeploymentName: "test-deployment-1",
						Protocols:      []string{"ISCSI"}, // SAN protocol volume
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(1024),
						BackupVaultID:    "backup-vault-crb",
					},
				},
			},
			backupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "backup-vault-crb"},
				Name:             "BackupVaultCRB",
				SourceRegionName: stringPtr("us-east-1"),
				BackupRegionName: stringPtr("us-west-1"), // Different region
			},
			sfrMetricsMap: map[string]datamodel.SfrMetricsAggregate{
				"volume-uuid-crb-sfr": {
					TotalSize:  5120,
					TotalCount: 10,
				},
			},
			expectedHydratedMetricsCount:  0, // No billing HydratedMetrics (BackupEnabledVolumeAllocatedSize doesn't create them)
			expectedDataModelMetricsCount: 0, // DataModel metrics should be skipped for CRB
			expectedSFRMetricsCount:       2, // SFR metrics should STILL be collected (size + count)
			description:                   "CRB volume should skip billing but collect SFR performance metrics",
		},
		{
			name:                                  "CRB volume - SFR disabled, billing metrics skipped",
			enableCrossRegionBackupBillingMetrics: false,
			enableSFRMetrics:                      false,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-crb-no-sfr",
					Name:        "CRBVolumeNoSFR",
					SizeInBytes: 2048,
					PoolID:      2,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account2",
						DeploymentName: "test-deployment-2",
						Protocols:      []string{"ISCSI"}, // SAN protocol volume
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(1024),
						BackupVaultID:    "backup-vault-crb-2",
					},
				},
			},
			backupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "backup-vault-crb-2"},
				Name:             "BackupVaultCRB2",
				SourceRegionName: stringPtr("us-east-1"),
				BackupRegionName: stringPtr("eu-west-1"), // Different region
			},
			sfrMetricsMap:                 map[string]datamodel.SfrMetricsAggregate{}, // Empty, no SFR data
			expectedHydratedMetricsCount:  0,                                          // No billing HydratedMetrics
			expectedDataModelMetricsCount: 0,                                          // Skipped for CRB
			expectedSFRMetricsCount:       0,                                          // No SFR metrics since disabled
			description:                   "CRB volume with SFR disabled should skip both billing and SFR metrics",
		},
		{
			name:                                  "Same region volume - billing and SFR metrics both collected",
			enableCrossRegionBackupBillingMetrics: false,
			enableSFRMetrics:                      true,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-same-region-sfr",
					Name:        "SameRegionVolumeWithSFR",
					SizeInBytes: 3072,
					PoolID:      3,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account3",
						DeploymentName: "test-deployment-3",
						Protocols:      []string{"ISCSI"}, // SAN protocol volume
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(2048),
						BackupVaultID:    "backup-vault-same",
					},
				},
			},
			backupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "backup-vault-same"},
				Name:             "BackupVaultSame",
				SourceRegionName: stringPtr("us-east-1"),
				BackupRegionName: stringPtr("us-east-1"), // Same region
			},
			sfrMetricsMap: map[string]datamodel.SfrMetricsAggregate{
				"volume-uuid-same-region-sfr": {
					TotalSize:  8192,
					TotalCount: 15,
				},
			},
			expectedHydratedMetricsCount:  0, // No billing HydratedMetrics
			expectedDataModelMetricsCount: 1, // Should be included for same-region
			expectedSFRMetricsCount:       2, // SFR metrics should be collected
			description:                   "Same region volume should collect both billing and SFR metrics",
		},
		{
			name:                                  "CRB flag enabled - billing and SFR metrics both collected",
			enableCrossRegionBackupBillingMetrics: true,
			enableSFRMetrics:                      true,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-crb-enabled",
					Name:        "CRBEnabledVolume",
					SizeInBytes: 4096,
					PoolID:      4,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account4",
						DeploymentName: "test-deployment-4",
						Protocols:      []string{"ISCSI"}, // SAN protocol volume
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(3072),
						BackupVaultID:    "backup-vault-crb-enabled",
					},
				},
			},
			backupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "backup-vault-crb-enabled"},
				Name:             "BackupVaultCRBEnabled",
				SourceRegionName: stringPtr("us-east-1"),
				BackupRegionName: stringPtr("ap-south-1"), // Different region
			},
			sfrMetricsMap: map[string]datamodel.SfrMetricsAggregate{
				"volume-uuid-crb-enabled": {
					TotalSize:  12288,
					TotalCount: 20,
				},
			},
			expectedHydratedMetricsCount:  0, // No billing HydratedMetrics
			expectedDataModelMetricsCount: 1, // Should be included when flag is enabled
			expectedSFRMetricsCount:       2, // SFR metrics should be collected
			description:                   "CRB enabled flag should collect both billing and SFR metrics",
		},
		{
			name:                                  "Mixed volumes - CRB skips billing but all collect SFR",
			enableCrossRegionBackupBillingMetrics: false,
			enableSFRMetrics:                      true,
			volumes: []*database.VolumeMetricsData{
				{
					UUID:        "volume-uuid-mixed-1",
					Name:        "MixedVolume1CRB",
					SizeInBytes: 2048,
					PoolID:      5,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account5",
						DeploymentName: "test-deployment-5",
						Protocols:      []string{"ISCSI"}, // SAN protocol volume
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(1024),
						BackupVaultID:    "backup-vault-mixed-crb",
					},
				},
				{
					UUID:        "volume-uuid-mixed-2",
					Name:        "MixedVolume2Same",
					SizeInBytes: 3072,
					PoolID:      6,
					VolumeAttributes: &datamodel.VolumeAttributes{
						AccountName:    "Account6",
						DeploymentName: "test-deployment-6",
						Protocols:      []string{"ISCSI"}, // SAN protocol volume
					},
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: intPtr(2048),
						BackupVaultID:    "backup-vault-mixed-same",
					},
				},
			},
			backupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "backup-vault-mixed-crb"},
				Name:             "BackupVaultMixedCRB",
				SourceRegionName: stringPtr("us-east-1"),
				BackupRegionName: stringPtr("eu-west-1"), // Different region for first volume
			},
			sfrMetricsMap: map[string]datamodel.SfrMetricsAggregate{
				"volume-uuid-mixed-1": {
					TotalSize:  2048,
					TotalCount: 5,
				},
				"volume-uuid-mixed-2": {
					TotalSize:  4096,
					TotalCount: 8,
				},
			},
			expectedHydratedMetricsCount:  0, // No billing HydratedMetrics
			expectedDataModelMetricsCount: 1, // Only same-region volume
			expectedSFRMetricsCount:       4, // Both volumes should have SFR metrics (2 metrics each)
			description:                   "Mixed volumes: CRB skips billing but both collect SFR metrics",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockVolumeStorage)
			ctx := context.Background()
			config := &common.TelemetryConfig{
				RegionName:                            "us-east-1",
				EnableBackupBillingMetrics:            true,
				EnableCrossRegionBackupBillingMetrics: tt.enableCrossRegionBackupBillingMetrics,
				SFRMetricsEnabled:                     tt.enableSFRMetrics,
			}
			poolMetadataMap := make(map[int64]metadata.ResourceMetadata)

			m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(tt.volumes, nil)
			m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

			// Mock GetMultipleBackupVaults
			if tt.backupVault != nil {
				if tt.name == "Mixed volumes - CRB skips billing but all collect SFR" {
					// For mixed test, return both vaults
					sameRegionVault := &datamodel.BackupVault{
						BaseModel:        datamodel.BaseModel{UUID: "backup-vault-mixed-same"},
						Name:             "BackupVaultMixedSame",
						SourceRegionName: stringPtr("us-east-1"),
						BackupRegionName: stringPtr("us-east-1"), // Same region
					}
					backupVaults := []*datamodel.BackupVault{tt.backupVault, sameRegionVault}
					m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(backupVaults, nil)
				} else {
					backupVaults := []*datamodel.BackupVault{tt.backupVault}
					m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(backupVaults, nil)
				}
			} else {
				m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
			}

			// Mock SFR metrics if enabled
			if tt.enableSFRMetrics {
				m.On("GetSfrMetricsByTimeRange", mock.Anything, mock.Anything, mock.Anything).Return(tt.sfrMetricsMap, nil)
			}

			result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
			assert.NoError(t, err)
			assert.NotNil(t, result)

			// Verify counts
			assert.Len(t, result.HydratedMetrics, tt.expectedHydratedMetricsCount,
				"HydratedMetrics count mismatch: %s", tt.description)
			assert.Len(t, result.HydratedMetricsDataModel, tt.expectedDataModelMetricsCount,
				"HydratedMetricsDataModel count mismatch: %s", tt.description)
			assert.Len(t, result.SFRHydratedMetrics, tt.expectedSFRMetricsCount,
				"SFRHydratedMetrics count mismatch: %s", tt.description)

			// Verify SFR metrics have correct types
			if tt.expectedSFRMetricsCount > 0 {
				sizeMetricFound := false
				countMetricFound := false
				for _, sfrMetric := range result.SFRHydratedMetrics {
					if sfrMetric.MeasuredType == metadata.SFRTotalSizeRestoredBytes {
						sizeMetricFound = true
						assert.Greater(t, sfrMetric.Quantity, float64(0), "SFR size metric should have positive quantity")
					}
					if sfrMetric.MeasuredType == metadata.SFRTotalFilesRestoredCount {
						countMetricFound = true
						assert.Greater(t, sfrMetric.Quantity, float64(0), "SFR count metric should have positive quantity")
					}
				}
				assert.True(t, sizeMetricFound, "SFR Total Size Restored Bytes metric should be present")
				assert.True(t, countMetricFound, "SFR Total Files Restored Count metric should be present")
			}
		})
	}
}

// Test_GetVolumeMetrics_SkipsDisabledAccounts tests that volumes with HYPERSCALERDISABLED accounts are skipped
func Test_GetVolumeMetrics_SkipsDisabledAccounts(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                 "us-east-1",
		EnableBackupBillingMetrics: true,
		EnableFilesBackupBilling:   true,
	}

	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)
	poolMetadataMap[1] = metadata.ResourceMetadata{
		ResourceType: metadata.Volume,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "DisabledAccount",
				DeploymentName: "test-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
		{
			UUID:        "volume-uuid-2",
			Name:        "Volume2",
			SizeInBytes: 4096,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "EnabledAccount",
				DeploymentName: "test-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	accounts := []*database.AccountTelemetryData{
		{
			ID:    1,
			Name:  "DisabledAccount",
			State: datamodel.AccountStateHyperscalerDisabled,
		},
		{
			ID:    2,
			Name:  "EnabledAccount",
			State: "ENABLED",
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.MatchedBy(func(p *utils.Pagination) bool {
		return p != nil && p.Offset == 0 && p.Limit == 1000
	})).Return(accounts, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Should only have metrics for Volume2 (EnabledAccount), not Volume1 (DisabledAccount)
	// Volume2 should have: VolumeAllocatedThroughput metric and BackupEnabledVolumeAllocatedSize hydrated metric
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1, "Should have throughput metric for enabled account volume only")
	assert.Len(t, result.HydratedMetricsDataModel, 1, "Should have hydrated metric for enabled account volume only")

	// Verify all metrics belong to Volume2
	for _, metric := range result.VolumeAllocatedThroughputHydratedMetrics {
		assert.Equal(t, "Volume2", derefString(metric.Metadata.ResourceName))
		assert.Equal(t, "EnabledAccount", derefString(metric.Metadata.AccountName))
	}

	for _, metric := range result.HydratedMetricsDataModel {
		assert.Equal(t, "Volume2", metric.ResourceName)
		assert.Equal(t, "EnabledAccount", metric.ConsumerID)
	}

	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_AccountFetchFailure tests graceful degradation when account fetch fails
func Test_GetVolumeMetrics_AccountFetchFailure(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                 "us-east-1",
		EnableBackupBillingMetrics: true,
		EnableFilesBackupBilling:   true,
	}

	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)
	poolMetadataMap[1] = metadata.ResourceMetadata{
		ResourceType: metadata.Volume,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "Account1",
				DeploymentName: "test-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	// Should still process volumes even if account fetch fails (graceful degradation)
	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Should have metrics for all volumes since account filtering failed
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	m.AssertExpectations(t)
}

// --- Expert mode volume backup management fee billing tests ---

// Test_GetVolumeMetrics_ExpertModeVolumesBillingIncluded verifies that expert mode volumes
// with active backup chains emit BackupEnabledVolumeAllocatedSize when EnableExpertModeBackupBilling=true.
func Test_GetVolumeMetrics_ExpertModeVolumesBillingIncluded(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                    "us-east-1",
		EnableBackupBillingMetrics:    true,
		EnableFilesBackupBilling:      true,
		EnableExpertModeBackupBilling: true,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{
		1: {ResourceType: metadata.Volume},
	}

	backupChainBytes := int64(2048)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:           "emv-uuid-1",
			Name:           "ExpertVolume1",
			SizeInBytes:    4096,
			PoolID:         1,
			AccountName:    "Account1",
			DeploymentName: "test-deployment",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
			VolumeAttributes: &datamodel.ExpertModeVolumeAttributes{
				Protocols: []string{"NFS"},
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, result.HydratedMetricsDataModel, 1)
	assert.Equal(t, string(metadata.BackupEnabledVolumeAllocatedSize), result.HydratedMetricsDataModel[0].MeasuredType)
	assert.Equal(t, string(metadata.Volume), result.HydratedMetricsDataModel[0].ResourceType)
	assert.Equal(t, "Account1", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "ExpertVolume1", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, float64(4096), result.HydratedMetricsDataModel[0].Quantity)

	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumesBillingDisabled verifies that when
// EnableExpertModeBackupBilling=false and there are no regular volumes, the function
// exits early and expert mode volumes are not processed.
func Test_GetVolumeMetrics_ExpertModeVolumesBillingDisabled(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                    "us-east-1",
		EnableBackupBillingMetrics:    true,
		EnableFilesBackupBilling:      true,
		EnableExpertModeBackupBilling: false,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	// No regular volumes + expert mode disabled → early return; only ListVolumesForTelemetryMetrics is called.
	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumesNoBackupChain verifies that expert mode volumes
// without a backup chain are skipped.
func Test_GetVolumeMetrics_ExpertModeVolumesNoBackupChain(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                    "us-east-1",
		EnableBackupBillingMetrics:    true,
		EnableFilesBackupBilling:      true,
		EnableExpertModeBackupBilling: true,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:         "emv-uuid-no-backup",
			Name:         "ExpertVolumeNoBackup",
			SizeInBytes:  4096,
			AccountName:  "Account1",
			BackupConfig: nil,
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumesRegionalHA verifies that expert mode volumes in
// regional HA pools emit the correct resource type.
func Test_GetVolumeMetrics_ExpertModeVolumesRegionalHA(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                    "us-east-1",
		EnableBackupBillingMetrics:    true,
		EnableFilesBackupBilling:      true,
		EnableExpertModeBackupBilling: true,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{
		10: {ResourceType: metadata.VolumePoolRegionalHA},
	}

	backupChainBytes := int64(512)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:        "emv-rha-1",
			Name:        "ExpertVolumeRHA",
			SizeInBytes: 8192,
			PoolID:      10,
			AccountName: "AccountRHA",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	require.Len(t, result.HydratedMetricsDataModel, 1)
	assert.Equal(t, string(metadata.VolumeRegionalHA), result.HydratedMetricsDataModel[0].ResourceType)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumesSkipCrossRegion verifies that expert mode volumes
// with cross-region backup vaults are skipped when CRB billing is disabled.
func Test_GetVolumeMetrics_ExpertModeVolumesSkipCrossRegion(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	src := "us-east-1"
	dst := "us-west-1"
	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableBackupBillingMetrics:            true,
		EnableFilesBackupBilling:              true,
		EnableExpertModeBackupBilling:         true,
		EnableCrossRegionBackupBillingMetrics: false,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	backupChainBytes := int64(1024)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:        "emv-crb-1",
			Name:        "ExpertVolumeCRB",
			SizeInBytes: 2048,
			AccountName: "Account1",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
				BackupVaultID:    "vault-crb-uuid",
			},
		},
	}

	crbVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "vault-crb-uuid"},
		SourceRegionName: &src,
		BackupRegionName: &dst,
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{crbVault}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumesMissingAccountSkipped verifies that expert mode
// volumes with no account name are skipped.
func Test_GetVolumeMetrics_ExpertModeVolumesMissingAccountSkipped(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                    "us-east-1",
		EnableBackupBillingMetrics:    true,
		EnableFilesBackupBilling:      true,
		EnableExpertModeBackupBilling: true,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	backupChainBytes := int64(512)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:        "emv-no-account",
			Name:        "ExpertVolumeNoAccount",
			SizeInBytes: 1024,
			AccountName: "",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumeDBError verifies that a DB error from
// ListExpertModeVolumesForTelemetryMetrics is logged but does not fail the overall call.
func Test_GetVolumeMetrics_ExpertModeVolumeDBError(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                    "us-east-1",
		EnableBackupBillingMetrics:    true,
		EnableFilesBackupBilling:      true,
		EnableExpertModeBackupBilling: true,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)

	// DB error is logged and expert mode metrics are skipped; the rest of the result is intact.
	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumes_HyperscalerDisabledSkipped verifies that expert mode
// volumes whose account is in HYPERSCALERDISABLED state are skipped (lines 286-287).
func Test_GetVolumeMetrics_ExpertModeVolumes_HyperscalerDisabledSkipped(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                    "us-east-1",
		EnableBackupBillingMetrics:    true,
		EnableFilesBackupBilling:      true,
		EnableExpertModeBackupBilling: true,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	backupChainBytes := int64(1024)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:        "emv-disabled-acct",
			Name:        "ExpertVolumeDisabledAcct",
			SizeInBytes: 2048,
			AccountName: "DisabledAccount",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	accounts := []*database.AccountTelemetryData{
		{
			ID:    1,
			Name:  "DisabledAccount",
			State: datamodel.AccountStateHyperscalerDisabled,
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return(accounts, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumes_FilesBackupBillingDisabledNonSanSkipped verifies that
// expert mode volumes with a non-SAN protocol are skipped when EnableFilesBackupBilling=false (line 296).
func Test_GetVolumeMetrics_ExpertModeVolumes_FilesBackupBillingDisabledNonSanSkipped(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                    "us-east-1",
		EnableBackupBillingMetrics:    true,
		EnableFilesBackupBilling:      false,
		EnableExpertModeBackupBilling: true,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	backupChainBytes := int64(512)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:        "emv-nfs-no-files-billing",
			Name:        "ExpertVolumeNFS",
			SizeInBytes: 1024,
			AccountName: "Account1",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
			VolumeAttributes: &datamodel.ExpertModeVolumeAttributes{
				Protocols: []string{"NFS"},
			},
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumes_CRBDisabledVaultNotInMapSkipped verifies that when
// CRB billing is disabled and the backup vault is not found in the map, the volume is skipped
// with an error log (lines 317-318).
func Test_GetVolumeMetrics_ExpertModeVolumes_CRBDisabledVaultNotInMapSkipped(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableBackupBillingMetrics:            true,
		EnableFilesBackupBilling:              true,
		EnableExpertModeBackupBilling:         true,
		EnableCrossRegionBackupBillingMetrics: false,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	backupChainBytes := int64(1024)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:        "emv-crb-notinmap",
			Name:        "ExpertVolumeCRBMissing",
			SizeInBytes: 2048,
			AccountName: "Account1",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
				BackupVaultID:    "vault-missing-uuid",
			},
		},
	}

	// Vault is not returned, so backupVaultMap does not contain "vault-missing-uuid".
	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumes_CMEKDisabledVaultFoundWithCmekSkipped verifies that when
// CMEK billing is disabled and the vault has CMEK attributes, the volume is skipped (lines 324-327).
func Test_GetVolumeMetrics_ExpertModeVolumes_CMEKDisabledVaultFoundWithCmekSkipped(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	kmsPath := "projects/p/locations/us/keyRings/kr/cryptoKeys/key"
	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableBackupBillingMetrics:            true,
		EnableFilesBackupBilling:              true,
		EnableExpertModeBackupBilling:         true,
		EnableCrossRegionBackupBillingMetrics: true, // CRB check skipped
		EnableCmekBackupBilling:               false,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	backupChainBytes := int64(1024)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:        "emv-cmek-skip",
			Name:        "ExpertVolumeCMEK",
			SizeInBytes: 2048,
			AccountName: "Account1",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
				BackupVaultID:    "vault-cmek-uuid",
			},
		},
	}

	cmekVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "vault-cmek-uuid"},
		CmekAttributes: &datamodel.CmekAttributes{
			KmsConfigResourcePath: &kmsPath,
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{cmekVault}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumes_CMEKDisabledVaultNotInMapSkipped verifies that when
// CMEK billing is disabled and the vault is not in the map, the volume is skipped with an error
// log (lines 330-331).
func Test_GetVolumeMetrics_ExpertModeVolumes_CMEKDisabledVaultNotInMapSkipped(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableBackupBillingMetrics:            true,
		EnableFilesBackupBilling:              true,
		EnableExpertModeBackupBilling:         true,
		EnableCrossRegionBackupBillingMetrics: true, // CRB check skipped
		EnableCmekBackupBilling:               false,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	backupChainBytes := int64(1024)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:        "emv-cmek-notinmap",
			Name:        "ExpertVolumeCMEKMissing",
			SizeInBytes: 2048,
			AccountName: "Account1",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
				BackupVaultID:    "vault-cmek-missing-uuid",
			},
		},
	}

	// Vault is not returned, so backupVaultMap does not contain "vault-cmek-missing-uuid".
	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumes_GCBDRDisabledCrossProjectVaultSkipped verifies that when
// GCBDR billing is disabled and the vault has ServiceType=CrossProject, the volume is skipped
// (lines 337-340).
func Test_GetVolumeMetrics_ExpertModeVolumes_GCBDRDisabledCrossProjectVaultSkipped(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableBackupBillingMetrics:            true,
		EnableFilesBackupBilling:              true,
		EnableExpertModeBackupBilling:         true,
		EnableCrossRegionBackupBillingMetrics: true, // CRB check skipped
		EnableCmekBackupBilling:               true, // CMEK check skipped
		EnableGcbdrBackupBilling:              false,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	backupChainBytes := int64(1024)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:        "emv-gcbdr-skip",
			Name:        "ExpertVolumeGCBDR",
			SizeInBytes: 2048,
			AccountName: "Account1",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
				BackupVaultID:    "vault-gcbdr-uuid",
			},
		},
	}

	gcbdrVault := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{UUID: "vault-gcbdr-uuid"},
		ServiceType: datamodel.ServiceTypeCrossProject,
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{gcbdrVault}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumes_GCBDRDisabledVaultNotInMapSkipped verifies that when
// GCBDR billing is disabled and the vault is not in the map, the volume is skipped with an error
// log (lines 343-344).
func Test_GetVolumeMetrics_ExpertModeVolumes_GCBDRDisabledVaultNotInMapSkipped(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableBackupBillingMetrics:            true,
		EnableFilesBackupBilling:              true,
		EnableExpertModeBackupBilling:         true,
		EnableCrossRegionBackupBillingMetrics: true, // CRB check skipped
		EnableCmekBackupBilling:               true, // CMEK check skipped
		EnableGcbdrBackupBilling:              false,
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	backupChainBytes := int64(1024)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:        "emv-gcbdr-notinmap",
			Name:        "ExpertVolumeGCBDRMissing",
			SizeInBytes: 2048,
			AccountName: "Account1",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
				BackupVaultID:    "vault-gcbdr-missing-uuid",
			},
		},
	}

	// Vault is not returned, so backupVaultMap does not contain "vault-gcbdr-missing-uuid".
	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	assert.Empty(t, result.HydratedMetricsDataModel)
	m.AssertExpectations(t)
}

// Test_GetVolumeMetrics_ExpertModeVolumes_CRBVaultSetsCrossRegionMetadata verifies that when a
// qualifying expert mode volume references a CrossRegion backup vault with a BackupRegionName, the
// cross-region metadata is set on the emitted metric (lines 362, 365-366).
func Test_GetVolumeMetrics_ExpertModeVolumes_CRBVaultSetsCrossRegionMetadata(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	backupRegion := "us-west-1"
	config := &common.TelemetryConfig{
		RegionName:                            "us-east-1",
		EnableBackupBillingMetrics:            true,
		EnableFilesBackupBilling:              true,
		EnableExpertModeBackupBilling:         true,
		EnableCrossRegionBackupBillingMetrics: true,  // CRB billing check skipped; vault map still populated via GCBDR=false
		EnableCmekBackupBilling:               true,  // CMEK check skipped
		EnableGcbdrBackupBilling:              false, // ensures vaults are fetched and GCBDR check runs (vault is not CrossProject, so not skipped)
	}

	poolMetadataMap := map[int64]metadata.ResourceMetadata{}

	backupChainBytes := int64(4096)
	expertVolumes := []*database.ExpertModeVolumeMetricsData{
		{
			UUID:        "emv-crb-region",
			Name:        "ExpertVolumeCRBRegion",
			SizeInBytes: 8192,
			AccountName: "Account1",
			BackupConfig: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
				BackupVaultID:    "vault-crb-region-uuid",
			},
		},
	}

	crbVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "vault-crb-region-uuid"},
		BackupVaultType:  activities.CrossRegionBackupType,
		BackupRegionName: &backupRegion,
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return([]*database.VolumeMetricsData{}, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{crbVault}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return(expertVolumes, nil).Once()
	m.On("ListExpertModeVolumesForTelemetryMetrics", mock.Anything, mock.Anything).Return([]*database.ExpertModeVolumeMetricsData{}, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	require.NoError(t, err)

	require.Len(t, result.HydratedMetricsDataModel, 1)
	hm := result.HydratedMetricsDataModel[0]
	assert.Equal(t, string(metadata.BackupEnabledVolumeAllocatedSize), hm.MeasuredType)
	assert.Equal(t, "Account1", hm.ConsumerID)
	assert.Equal(t, float64(8192), hm.Quantity)
	m.AssertExpectations(t)
}

// --- End expert mode volume billing tests ---

// Test_GetVolumeMetrics_AccountNotInMap tests that volumes with accounts not in the map are still processed
func Test_GetVolumeMetrics_AccountNotInMap(t *testing.T) {
	m := new(mockVolumeStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{
		RegionName:                 "us-east-1",
		EnableBackupBillingMetrics: true,
		EnableFilesBackupBilling:   true,
	}

	poolMetadataMap := make(map[int64]metadata.ResourceMetadata)
	poolMetadataMap[1] = metadata.ResourceMetadata{
		ResourceType: metadata.Volume,
	}

	backupChainBytes := int64(1024)
	volumes := []*database.VolumeMetricsData{
		{
			UUID:        "volume-uuid-1",
			Name:        "Volume1",
			SizeInBytes: 2048,
			PoolID:      1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "UnknownAccount",
				DeploymentName: "test-deployment",
			},
			DataProtection: &datamodel.DataProtection{
				BackupChainBytes: &backupChainBytes,
			},
		},
	}

	accounts := []*database.AccountTelemetryData{
		{
			ID:    1,
			Name:  "OtherAccount",
			State: "ENABLED",
		},
	}

	m.On("ListVolumesForTelemetryMetrics", mock.Anything).Return(volumes, nil)
	m.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return([]*datamodel.BackupVault{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return(accounts, nil)

	result, err := GetVolumeMetrics(ctx, m, config, poolMetadataMap, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Should still process volumes even if account is not in the map (unknown accounts are allowed)
	assert.Len(t, result.VolumeAllocatedThroughputHydratedMetrics, 1)
	assert.Len(t, result.HydratedMetricsDataModel, 1)

	m.AssertExpectations(t)
}
