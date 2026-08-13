package gcp

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	vsaerror "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflow_engine_mock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/sdk/client"
)

func TestCreateBackup(t *testing.T) {
	temporal := workflow_engine_mock.NewMockTemporalTestClient(t)

	t.Run("WhenValidateCreateBackupParamsFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store, err := database.NewTestStorage(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err, "Failed to ClearInMemoryDB")

		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			VolumeUUID:    "testVolumeUUID",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
		}
		originalValidateCreateBackupParams := validateCreateBackupParams
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return errors.New("validation failed")
		}
		defer func() { validateCreateBackupParams = originalValidateCreateBackupParams }()

		_, _, err = createBackup(ctx, store, temporal, params)
		assert.EqualError(tt, err, "validation failed")
	})

	t.Run("WhenFailsWithVolumeNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store, err := database.NewTestStorage(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err, "Failed to ClearInMemoryDB")

		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			VolumeUUID:    "testVolumeUUID",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
		}
		originalValidateCreateBackupParams := validateCreateBackupParams
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return nil
		}
		defer func() { validateCreateBackupParams = originalValidateCreateBackupParams }()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "testAccountUUID"}}, nil
		}
		_, _, err = createBackup(ctx, store, temporal, params)
		assertErrContainsOriginal(tt, err, "volume not found")
	})

	t.Run("WhenFailsWithValidation", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store, err := database.NewTestStorage(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err, "Failed to ClearInMemoryDB")

		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			VolumeUUID:    "testVolumeUUID",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
		}
		originalValidateCreateBackupParams := validateCreateBackupParams
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return errors.New("validation failed")
		}
		defer func() { validateCreateBackupParams = originalValidateCreateBackupParams }()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "testAccountUUID"}}, nil
		}
		_, _, err = createBackup(ctx, store, temporal, params)
		assert.EqualError(tt, err, "validation failed")
	})
}

func TestOrchestrator_CreateBackup(t *testing.T) {
	t.Run("CallsCreateBackup", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{}

		createBackup = func(ctx context.Context, se database.Storage, temporalClient client.Client, params *common.CreateBackupParams) (*models.Backup, string, error) {
			return &models.Backup{}, "job-id", nil
		}

		o := &GCPOrchestrator{storage: store, temporal: temporal}
		backup, jobID, err := o.CreateBackup(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, backup)
		assert.Equal(tt, "job-id", jobID)
	})
}

func TestOrchestrator_ListBackups(t *testing.T) {
	t.Run("CallsGetBackups", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		params := &common.GetBackupsParams{}

		getBackups = func(ctx context.Context, se database.Storage, params *common.GetBackupsParams, filters [][]interface{}) ([]*datamodel.Backup, error) {
			return []*datamodel.Backup{}, nil
		}

		o := &GCPOrchestrator{storage: store}
		backups, err := o.ListBackups(ctx, params.BackupVaultID, "account", nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, backups)
	})
}

func Test_createBackup(t *testing.T) {
	t.Run("FailsValidation", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{}
		originalValidateCreateBackupParams := validateCreateBackupParams
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return errors.New("validation failed")
		}
		defer func() { validateCreateBackupParams = originalValidateCreateBackupParams }()

		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.EqualError(tt, err, "validation failed")
	})

	t.Run("FailsVolumeFetch", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{}
		originalValidateCreateBackupParams := validateCreateBackupParams
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return nil
		}
		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
			},
			Name: "test_account",
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}
		defer func() {
			validateCreateBackupParams = originalValidateCreateBackupParams
			getOrCreateAccount = _getOrCreateAccount
		}()

		store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, mock.Anything).Return(nil, errors.New("volume not found"))

		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.EqualError(tt, err, "volume not found")
	})
}

// Test_createBackup_VolumeFetching tests the volume fetching logic in _createBackup
// This specifically tests lines 179-201 which handle fetching from ExpertModeVolumes or regular Volumes table
func Test_createBackup_VolumeFetching(t *testing.T) {
	ctx := context.Background()
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "account-uuid",
		},
		Name: "test-account",
	}

	// Setup common mocks that are needed for all tests
	setupCommonMocks := func(t *testing.T) (*database.MockStorage, *workflow_engine_mock.MockTemporalTestClient) {
		store := database.NewMockStorage(t)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		// Mock getOrCreateAccount to return account
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		t.Cleanup(func() {
			getOrCreateAccount = originalGetOrCreateAccount
		})

		// Mock validateCreateBackupParams to succeed
		originalValidateCreateBackupParams := validateCreateBackupParams
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return nil
		}
		t.Cleanup(func() {
			validateCreateBackupParams = originalValidateCreateBackupParams
		})

		return store, temporal
	}

	t.Run("ExpertModeVolume_WhenGetExpertModeVolumeByUUIDSucceedsWithAccountSet", func(t *testing.T) {
		store, temporal := setupCommonMocks(t)
		params := &common.CreateBackupParams{
			VolumeUUID:         "expert-volume-uuid",
			BackupVaultID:      "vault-id",
			BackupName:         "test-backup",
			AccountName:        "test-account",
			IsExpertModeVolume: true,
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
		}
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
		}
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: params.VolumeUUID,
				ID:   1,
			},
			AccountID: account.ID,
			Account:   account, // Account already set
			Pool:      pool,    // Pool preloaded
			PoolID:    pool.ID,
			State:     datamodel.LifeCycleStateREADY,
			Name:      "expert-vol",
			Svm:       svm,
		}

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: params.BackupVaultID},
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: expertModeVol.Name, Protocols: []string{}},
			Description:  params.Description,
			Type:         params.BackupType,
		}

		store.On("GetExpertModeVolumeByExternalUUID", ctx, params.VolumeUUID).Return(expertModeVol, nil)
		store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
			return j.JobAttributes != nil && j.JobAttributes.ResourceUUID == backup.UUID
		})).Return(job, nil)
		// Mock workflow execution to succeed
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		_, _, err := _createBackup(ctx, store, temporal, params)
		// Verify that Account is not overwritten when already set
		assert.NoError(t, err)
		assert.Equal(t, account, expertModeVol.Account)
		store.AssertExpectations(t)
	})

	t.Run("ExpertModeVolume_WhenGetExpertModeVolumeByUUIDSucceedsWithAccountNil", func(t *testing.T) {
		store, temporal := setupCommonMocks(t)
		params := &common.CreateBackupParams{
			VolumeUUID:         "expert-volume-uuid",
			BackupVaultID:      "vault-id",
			BackupName:         "test-backup",
			AccountName:        "test-account",
			IsExpertModeVolume: true,
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
		}
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "account-uuid",
			}}
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: params.VolumeUUID,
				ID:   1,
			},
			AccountID: account.ID,
			Account:   account, // Account not set - should be set by _createBackup
			Pool:      pool,    // Pool preloaded
			PoolID:    pool.ID,
			State:     datamodel.LifeCycleStateREADY,
			Name:      "expert-vol",
			Svm:       svm,
		}

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: params.BackupVaultID},
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: expertModeVol.Name, Protocols: []string{}},
			Description:  params.Description,
			Type:         params.BackupType,
		}

		store.On("GetExpertModeVolumeByExternalUUID", ctx, params.VolumeUUID).Return(expertModeVol, nil)
		store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
			return j.JobAttributes != nil && j.JobAttributes.ResourceUUID == backup.UUID
		})).Return(job, nil)
		// Mock workflow execution to succeed
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		_, _, err := _createBackup(ctx, store, temporal, params)
		// Verify that Account was set when it was nil
		assert.NoError(t, err)
		assert.Equal(t, account, expertModeVol.Account)
		store.AssertExpectations(t)
	})

	t.Run("ExpertModeVolume_WhenGetExpertModeVolumeByUUIDFails", func(t *testing.T) {
		store, temporal := setupCommonMocks(t)
		params := &common.CreateBackupParams{
			VolumeUUID:         "expert-volume-uuid",
			BackupVaultID:      "vault-id",
			AccountName:        "test-account",
			IsExpertModeVolume: true,
		}

		expectedErr := vsaerror.NewNotFoundErr("Expert mode volume", &params.VolumeUUID)
		store.On("GetExpertModeVolumeByExternalUUID", ctx, params.VolumeUUID).Return(nil, expectedErr)

		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		store.AssertExpectations(t)
	})

	t.Run("RegularVolume_WhenGetVolumeWithAccountIDSucceeds", func(t *testing.T) {
		store, temporal := setupCommonMocks(t)
		params := &common.CreateBackupParams{
			VolumeUUID:         "regular-volume-uuid",
			BackupVaultID:      "vault-id",
			BackupName:         "test-backup",
			AccountName:        "test-account",
			IsExpertModeVolume: false,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: params.VolumeUUID,
				ID:   1,
			},
			AccountID: account.ID,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
			Name:      "regular-vol",
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFS"},
			},
		}

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: params.BackupVaultID},
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: volume.Name, Protocols: volume.VolumeAttributes.Protocols},
			Description:  params.Description,
			Type:         params.BackupType,
		}

		store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, account.ID).Return(volume, nil)
		store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
			return j.JobAttributes != nil && j.JobAttributes.ResourceUUID == backup.UUID
		})).Return(job, nil)
		// Mock workflow execution to succeed
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.NoError(t, err)
		store.AssertExpectations(t)
	})

	t.Run("RegularVolume_WhenGetVolumeWithAccountIDFails", func(t *testing.T) {
		store, temporal := setupCommonMocks(t)
		params := &common.CreateBackupParams{
			VolumeUUID:         "regular-volume-uuid",
			BackupVaultID:      "vault-id",
			AccountName:        "test-account",
			IsExpertModeVolume: false,
		}

		expectedErr := vsaerror.NewNotFoundErr("Volume", &params.VolumeUUID)
		store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, account.ID).Return(nil, expectedErr)

		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		store.AssertExpectations(t)
	})

	// Vault-switch guard: expert mode volume with a different current vault.
	t.Run("ExpertModeVolume_VaultSwitchCheck_GetBackupVaultFails", func(t *testing.T) {
		store, temporal := setupCommonMocks(t)
		params := &common.CreateBackupParams{
			VolumeUUID:         "expert-volume-uuid",
			BackupVaultID:      "new-vault-id",
			AccountName:        "test-account",
			IsExpertModeVolume: true,
		}
		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}}
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}
		expertVol := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "expert-volume-uuid",
			Svm:          svm,
			Pool:         pool,
			VolumeAttributes: &datamodel.ExpertModeVolumeAttributes{
				Protocols: []string{"NFS"},
			},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "current-vault-id"},
		}
		store.On("GetExpertModeVolumeByExternalUUID", ctx, params.VolumeUUID).Return(expertVol, nil)
		store.On("GetBackupVault", ctx, "current-vault-id").Return(nil, errors.New("vault lookup failed"))

		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "vault lookup failed")
		store.AssertExpectations(t)
	})

	t.Run("ExpertModeVolume_VaultSwitchCheck_GetBackupCountFails", func(t *testing.T) {
		store, temporal := setupCommonMocks(t)
		params := &common.CreateBackupParams{
			VolumeUUID:         "expert-volume-uuid",
			BackupVaultID:      "new-vault-id",
			AccountName:        "test-account",
			IsExpertModeVolume: true,
		}
		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}}
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}
		expertVol := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "expert-volume-uuid",
			Svm:          svm,
			Pool:         pool,
			VolumeAttributes: &datamodel.ExpertModeVolumeAttributes{
				Protocols: []string{"NFS"},
			},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "current-vault-id"},
		}
		currentVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "current-vault-id"}}
		store.On("GetExpertModeVolumeByExternalUUID", ctx, params.VolumeUUID).Return(expertVol, nil)
		store.On("GetBackupVault", ctx, "current-vault-id").Return(currentVault, nil)
		store.On("GetBackupCountByVolumeAndVault", ctx, "expert-volume-uuid", int64(10)).
			Return(int64(0), errors.New("count query failed"))

		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "count query failed")
		store.AssertExpectations(t)
	})
}

func Test_validateCreateBackupParams(t *testing.T) {
	t.Run("FailsBackupCreatingState", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		params := &common.CreateBackupParams{}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(true, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(tt, err, "A backup operation from the same volume is currently in progress. Please wait for it to complete before starting a new backup")
	})

	t.Run("FailsVolumeState", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		params := &common.CreateBackupParams{}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolume", ctx, params.VolumeUUID).Return(&datamodel.Volume{State: "NOT_READY"}, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(tt, err, "Volume is not in available state")
	})
}

func Test_getBackups(t *testing.T) {
	t.Run("RetrievesBackups", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		params := &common.GetBackupsParams{BackupVaultID: "BackupVaultID"}

		store.On("GetBackupsByBackupVaultOwnerIDAndFilter", ctx, params.BackupVaultID, int64(0), [][]interface{}(nil)).Return([]*datamodel.Backup{}, nil)

		backups, err := _getBackups(ctx, store, params, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, backups)
	})
}

func Test_createBackupEdgeCases(t *testing.T) {
	ctx := context.Background()
	params := &common.CreateBackupParams{
		BackupName:    "testBackup",
		VolumeUUID:    "testVolumeUUID",
		BackupVaultID: "testVaultID",
		Description:   "desc",
		BackupType:    "FULL",
	}

	t.Run("Success", func(t *testing.T) {
		store := database.NewMockStorage(t)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		volume := &datamodel.Volume{
			Name:             "vol",
			Account:          account,
			VolumeAttributes: &datamodel.VolumeAttributes{Protocols: []string{"NFS"}},
			State:            "READY",
			DataProtection:   &datamodel.DataProtection{BackupVaultID: "testVaultID"},
		}
		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "testVaultID"},
			AccountID:        1,
			SourceRegionName: func() *string { s := "us-east1"; return &s }(),
		}
		job := &datamodel.Job{WorkflowID: "wf-id", BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: "vol", Protocols: []string{"NFS"}},
		}
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return nil
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateCreateBackupParams = _validateCreateBackupParams
		}()
		// store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, int64(1)).Return(volume, nil)
		store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
			return j.JobAttributes != nil && j.JobAttributes.ResourceUUID == backup.UUID
		})).Return(job, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		_, jobID, err := _createBackup(ctx, store, temporal, params)
		assert.NoError(t, err)
		assert.Equal(t, "job-uuid", jobID)
	})

	t.Run("FailsValidation", func(t *testing.T) {
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return errors.New("validation failed")
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateCreateBackupParams = _validateCreateBackupParams
		}()
		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "validation failed")
	})

	t.Run("FailsBackupVaultFetch", func(t *testing.T) {
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}
		volume := &datamodel.Volume{
			Name:             "vol",
			Account:          account,
			VolumeAttributes: &datamodel.VolumeAttributes{Protocols: []string{"NFS"}},
			State:            "READY",
			DataProtection:   &datamodel.DataProtection{BackupVaultID: "testVaultID"},
		}
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return nil
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateCreateBackupParams = _validateCreateBackupParams
		}()
		// store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, int64(1)).Return(volume, nil)
		store.On("GetBackupVault", ctx, params.BackupVaultID).Return(nil, errors.New("backup vault not found"))
		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "backup vault not found")
	})

	t.Run("FailsJobCreation", func(t *testing.T) {
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		volume := &datamodel.Volume{
			Name:             "vol",
			Account:          account,
			VolumeAttributes: &datamodel.VolumeAttributes{Protocols: []string{"NFS"}},
			State:            "READY",
			DataProtection:   &datamodel.DataProtection{BackupVaultID: "testVaultID"},
		}
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return nil
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateCreateBackupParams = _validateCreateBackupParams
		}()
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 1}, AccountID: 1, SourceRegionName: func() *string { s := "us-east1"; return &s }()}
		backup := &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: "backup-uuid"}, Name: params.BackupName}
		store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, int64(1)).Return(volume, nil)
		store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("job create failed"))
		store.On("DeleteBackup", ctx, backup.UUID).Return(backup, nil)
		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "job create failed")
	})
}

func TestDeleteBackup(t *testing.T) {
	t.Run("onSuccess", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		params := &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
			return nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateBackupDeleteParams = _validateBackupDeleteParams
		}()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: params.BackupUUID},
			BackupVault: &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: params.BackupVaultUUID}},
		}
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(backup, nil)
		conditions := [][]interface{}{{"volume_attributes->>'restored_backup_id' = ?", backup.UUID}, {"state = ?", "RESTORING"}}
		store.On("ListVolumes", ctx, conditions).Return(nil, nil)
		store.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}, nil)
		store.On("UpdateBackupState", ctx, mock.Anything).Return(backup, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
		_, jobUUID, err := deleteBackup(ctx, store, temporal, params)
		assert.NoError(t, err)
		assert.Equal(t, "job-uuid", jobUUID)
	})
	t.Run("onGetAccountError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		_, _, err := deleteBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "account not found")
	})
	t.Run("onValidationError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
			return errors.New("validation failed")
		}
		defer func() { validateBackupDeleteParams = _validateBackupDeleteParams }()

		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(&datamodel.Backup{State: datamodel.LifeCycleStateAvailable, Attributes: &datamodel.BackupAttributes{DeleteInitiated: false}}, nil)
		_, _, err := deleteBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "validation failed")
	})
	t.Run("onGetBackupError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
			return nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateBackupDeleteParams = _validateBackupDeleteParams
		}()

		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(nil, errors.New("backup not found"))

		_, _, err := deleteBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "backup not found")
	})
	t.Run("onBackupStateDeletingWithJobs", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: params.BackupUUID},
			State:     datamodel.LifeCycleStateDeleting,
		}
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(backup, nil)

		// Mock GetJobsWithCondition to return jobs
		mockJobs := []*datamodel.Job{
			{BaseModel: datamodel.BaseModel{UUID: "job-uuid-1"}, State: string(datamodel.JobsStateNEW)},
			{BaseModel: datamodel.BaseModel{UUID: "job-uuid-2"}, State: string(datamodel.JobsStatePROCESSING)},
		}
		store.On("GetJobsWithCondition", ctx, mock.Anything).Return(mockJobs, nil)

		_, jobUUID, err := deleteBackup(ctx, store, temporal, params)
		assert.NoError(t, err)
		assert.Equal(t, "job-uuid-1", jobUUID) // Should return first job
	})
	t.Run("onBackupStateDeletingWithNoJobs", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: params.BackupUUID},
			State:     datamodel.LifeCycleStateDeleting,
		}
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(backup, nil)

		// Mock GetJobsWithCondition to return empty jobs
		store.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{}, nil)

		// Should continue to normal flow since no jobs found
		validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
			return nil
		}
		conditions := [][]interface{}{{"volume_attributes->>'restored_backup_id' = ?", backup.UUID}, {"state = ?", "RESTORING"}}
		store.On("ListVolumes", ctx, conditions).Return(nil, nil)
		store.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}, nil)
		store.On("UpdateBackupState", ctx, mock.Anything).Return(backup, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
		defer func() { validateBackupDeleteParams = _validateBackupDeleteParams }()

		_, jobUUID, err := deleteBackup(ctx, store, temporal, params)
		assert.NoError(t, err)
		assert.Equal(t, "job-uuid", jobUUID)
	})
	t.Run("onBackupStateDeletingWithGetJobsError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: params.BackupUUID},
			State:     datamodel.LifeCycleStateDeleting,
		}
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(backup, nil)

		// Mock GetJobsWithCondition to return error
		store.On("GetJobsWithCondition", ctx, mock.Anything).Return(nil, errors.New("database error"))

		_, _, err := deleteBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "database error")
	})
	t.Run("onJobCreationError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
			return nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateBackupDeleteParams = _validateBackupDeleteParams
		}()
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(&datamodel.Backup{}, nil)
		conditions := [][]interface{}{{"volume_attributes->>'restored_backup_id' = ?", ""}, {"state = ?", "RESTORING"}}
		store.On("ListVolumes", ctx, conditions).Return(nil, nil)
		store.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("job creation failed"))

		_, _, err := deleteBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "job creation failed")
	})
	t.Run("onUpdateBackupStateError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
			return nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateBackupDeleteParams = _validateBackupDeleteParams
		}()
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(&datamodel.Backup{}, nil)
		conditions := [][]interface{}{{"volume_attributes->>'restored_backup_id' = ?", ""}, {"state = ?", "RESTORING"}}
		store.On("ListVolumes", ctx, conditions).Return(nil, nil)
		store.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{}, nil)
		store.On("UpdateBackupState", ctx, mock.Anything).Return(nil, errors.New("update state failed"))

		store.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		_, _, err := deleteBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "update state failed")
	})
	t.Run("onWorkflowExecutionError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
			return nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateBackupDeleteParams = _validateBackupDeleteParams
		}()
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(&datamodel.Backup{}, nil)
		conditions := [][]interface{}{{"volume_attributes->>'restored_backup_id' = ?", ""}, {"state = ?", "RESTORING"}}
		store.On("ListVolumes", ctx, conditions).Return(nil, nil)
		store.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{}, nil)
		store.On("UpdateBackupState", ctx, mock.Anything).Return(&datamodel.Backup{}, nil)
		store.On("UpdateBackupState", ctx, mock.Anything).Return(&datamodel.Backup{}, nil)
		store.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow execution failed")).Once()

		_, _, err := deleteBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "workflow execution failed")
	})
}

// TestDeleteBackup_CrossRegionBackupVault tests the _deleteBackup function with CROSS_REGION backup vault
func TestDeleteBackup_CrossRegionBackupVault(t *testing.T) {
	t.Run("WhenRemoteBackupIsRestoring_ReturnsError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		externalBackupVaultUUID := "external-vault-uuid"
		externalBackupUUID := "external-backup-uuid"
		backupRegionName := "us-central1"
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "test-backup-uuid"},
			ExternalUUID: externalBackupUUID,
			State:        datamodel.LifeCycleStateAvailable,
			Attributes:   &datamodel.BackupAttributes{DeleteInitiated: false},
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "test-vault-uuid"},
				BackupVaultType:  "CROSS_REGION",
				ExternalUUID:     &externalBackupVaultUUID,
				BackupRegionName: &backupRegionName,
			},
		}

		params := &common.DeleteBackupParams{
			BackupUUID:      backup.UUID,
			BackupVaultUUID: "test-vault-uuid",
			AccountName:     "test-account",
		}

		// Mock the validation function
		validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
			return nil
		}
		defer func() { validateBackupDeleteParams = _validateBackupDeleteParams }()

		// Mock account lookup
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		// Mock backup retrieval
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)

		// Mock ListVolumes to return empty (no local volumes restoring)
		conditions := [][]interface{}{{"volume_attributes->>'restored_backup_id' = ?", backup.UUID}, {"state = ?", datamodel.LifeCycleStateRestoring}}
		store.On("ListVolumes", ctx, conditions).Return([]*datamodel.Volume{}, nil)

		// Mock fetchRemoteBackupFromVCP to return a backup that is restoring
		originalFetchRemoteBackup := fetchRemoteBackupFromVCP
		defer func() { fetchRemoteBackupFromVCP = originalFetchRemoteBackup }()

		remoteBackup := googleproxyclient.InternalBackupV1beta{
			IsRestoring: googleproxyclient.NewOptBool(true),
		}
		fetchRemoteBackupFromVCP = func(ctx context.Context, backupUUID, backupVaultUUID, projectNumber, region string) (googleproxyclient.InternalBackupV1beta, error) {
			return remoteBackup, nil
		}

		// Act
		result, jobID, err := deleteBackup(ctx, store, temporal, params)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Cannot delete backup as restore is in progress for this backup in remote region")
		assert.Nil(t, result)
		assert.Equal(t, "", jobID)
		store.AssertExpectations(t)
	})

	t.Run("WhenRemoteBackupIsNotRestoring_Succeeds", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		externalBackupVaultUUID := "external-vault-uuid"
		externalBackupUUID := "external-backup-uuid"
		backupRegionName := "us-central1"
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "test-backup-uuid"},
			ExternalUUID: externalBackupUUID,
			State:        datamodel.LifeCycleStateAvailable,
			Attributes:   &datamodel.BackupAttributes{DeleteInitiated: false},
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "test-vault-uuid"},
				BackupVaultType:  "CROSS_REGION",
				ExternalUUID:     &externalBackupVaultUUID,
				BackupRegionName: &backupRegionName,
			},
		}

		params := &common.DeleteBackupParams{
			BackupUUID:      backup.UUID,
			BackupVaultUUID: "test-vault-uuid",
			AccountName:     "test-account",
		}

		// Mock the validation function
		validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
			return nil
		}
		defer func() { validateBackupDeleteParams = _validateBackupDeleteParams }()

		// Mock account lookup
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		// Mock backup retrieval
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)

		// Mock ListVolumes to return empty (no local volumes restoring)
		conditions := [][]interface{}{{"volume_attributes->>'restored_backup_id' = ?", backup.UUID}, {"state = ?", datamodel.LifeCycleStateRestoring}}
		store.On("ListVolumes", ctx, conditions).Return([]*datamodel.Volume{}, nil)

		// Mock fetchRemoteBackupFromVCP to return a backup that is NOT restoring
		originalFetchRemoteBackup := fetchRemoteBackupFromVCP
		defer func() { fetchRemoteBackupFromVCP = originalFetchRemoteBackup }()

		remoteBackup := googleproxyclient.InternalBackupV1beta{
			IsRestoring: googleproxyclient.NewOptBool(false),
		}
		fetchRemoteBackupFromVCP = func(ctx context.Context, backupUUID, backupVaultUUID, projectNumber, region string) (googleproxyclient.InternalBackupV1beta, error) {
			return remoteBackup, nil
		}

		// Mock job creation and workflow execution for successful deletion
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		store.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		store.On("UpdateBackupState", ctx, mock.Anything).Return(backup, nil)
		temporal.EXPECT().ExecuteWorkflow(
			mock.Anything,
			mock.Anything,
			mock.Anything,
			params,
		).Return(nil, nil).Once()

		// Act
		result, jobID, err := deleteBackup(ctx, store, temporal, params)

		// Assert
		assert.NoError(t, err)
		assert.Nil(t, result)
		assert.Equal(t, "job-uuid", jobID)
		store.AssertExpectations(t)
		temporal.AssertExpectations(t)
	})

	t.Run("WhenFetchRemoteBackupFails_ReturnsError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		externalBackupVaultUUID := "external-vault-uuid"
		externalBackupUUID := "external-backup-uuid"
		backupRegionName := "us-central1"
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "test-backup-uuid"},
			ExternalUUID: externalBackupUUID,
			State:        datamodel.LifeCycleStateAvailable,
			Attributes:   &datamodel.BackupAttributes{DeleteInitiated: false},
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "test-vault-uuid"},
				BackupVaultType:  "CROSS_REGION",
				ExternalUUID:     &externalBackupVaultUUID,
				BackupRegionName: &backupRegionName,
			},
		}

		params := &common.DeleteBackupParams{
			BackupUUID:      backup.UUID,
			BackupVaultUUID: "test-vault-uuid",
			AccountName:     "test-account",
		}

		// Mock the validation function
		validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
			return nil
		}
		defer func() { validateBackupDeleteParams = _validateBackupDeleteParams }()

		// Mock account lookup
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		// Mock backup retrieval
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)

		// Mock ListVolumes to return empty (no local volumes restoring)
		conditions := [][]interface{}{{"volume_attributes->>'restored_backup_id' = ?", backup.UUID}, {"state = ?", datamodel.LifeCycleStateRestoring}}
		store.On("ListVolumes", ctx, conditions).Return([]*datamodel.Volume{}, nil)

		// Mock fetchRemoteBackupFromVCP to return an error
		originalFetchRemoteBackup := fetchRemoteBackupFromVCP
		defer func() { fetchRemoteBackupFromVCP = originalFetchRemoteBackup }()

		expectedError := errors.New("failed to fetch remote backup")
		fetchRemoteBackupFromVCP = func(ctx context.Context, backupUUID, backupVaultUUID, projectNumber, region string) (googleproxyclient.InternalBackupV1beta, error) {
			return googleproxyclient.InternalBackupV1beta{}, expectedError
		}

		// Act
		result, jobID, err := deleteBackup(ctx, store, temporal, params)

		// Assert
		assert.Error(t, err)
		assert.Equal(t, expectedError, err)
		assert.Nil(t, result)
		assert.Equal(t, "", jobID)
		store.AssertExpectations(t)
	})
}

func TestValidateBackupDeleteParams(t *testing.T) {
	originalSwitching := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(originalSwitching)
	utils.SetEnableBackupVaultSwitchingForTest(false)

	t.Run("OnSuccess", func(t *testing.T) {
		// Ensure immutable backup feature is disabled for this test
		originalValue := utils.IsImmutableBackupEnabled()
		defer utils.SetImmutableBackupEnabledForTest(originalValue)
		utils.SetImmutableBackupEnabledForTest(false)

		ctx := context.Background()
		store := database.NewMockStorage(t)
		backup := &datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
			VolumeUUID: "volumeUUID1",
			BackupVault: &datamodel.BackupVault{
				BackupVaultType: "IN_REGION",
			},
		}
		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
		store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
		store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
		err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			BackupVaultUUID: "testVaultID",
			AccountName:     "testAccount",
		})
		assert.Nil(t, err)
	})

	t.Run("OnSuccessWithImmutableBackupEnabled", func(t *testing.T) {
		// Enable immutable backup feature for this test
		originalValue := utils.IsImmutableBackupEnabled()
		defer utils.SetImmutableBackupEnabledForTest(originalValue)
		utils.SetImmutableBackupEnabledForTest(true)

		ctx := context.Background()
		store := database.NewMockStorage(t)
		backup := &datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
			VolumeUUID: "volumeUUID1",
			BackupVault: &datamodel.BackupVault{
				BackupVaultType: "IN_REGION",
			},
		}
		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
		store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
		store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
		store.On("GetBackupVault", ctx, "testVaultID").Return(&datamodel.BackupVault{
			ImmutableAttributes: nil, // No immutable attributes for this test
		}, nil)
		err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			BackupVaultUUID: "testVaultID",
			AccountName:     "testAccount",
		})
		assert.Nil(t, err)
	})
	t.Run("OnLatestBackupError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		backup := &datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
			VolumeUUID: "volumeUUID1",
			BackupVault: &datamodel.BackupVault{
				BackupVaultType: "IN_REGION",
			},
		}
		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
		store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(true, nil)
		store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
		err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			BackupVaultUUID: "testVaultID",
			AccountName:     "testAccount",
		})
		assert.NotNil(t, err)
		assert.EqualError(t, err, "Cannot delete latest backup")
	})

	t.Run("OnBackupNotFound", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(nil, vsaerror.NewNotFoundErr("backup", nil))
		err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			BackupVaultUUID: "testVaultID",
			AccountName:     "testAccount",
		})
		assert.NotNil(t, err)
		assert.EqualError(t, err, "Backup not found")
	})
	t.Run("OnGetBackupDBError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(nil, errors.New("failed"))
		err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			BackupVaultUUID: "testVaultID",
			AccountName:     "testAccount",
		})
		assert.NotNil(t, err)
		assert.EqualError(t, err, "failed")
	})
	t.Run("IsLatestBackupDBError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		backup := &datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
			VolumeUUID: "volumeUUID1",
			BackupVault: &datamodel.BackupVault{
				BackupVaultType: "IN_REGION",
			},
		}
		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
		store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, errors.New("error checking latest backup"))
		err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			BackupVaultUUID: "testVaultID",
			AccountName:     "testAccount",
		})
		assert.NotNil(t, err)
		assert.EqualError(t, err, "error checking latest backup")
	})
	t.Run("OnBackupCountByVolumeIDError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		backup := &datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
			VolumeUUID: "volumeUUID1",
			BackupVault: &datamodel.BackupVault{
				BackupVaultType: "IN_REGION",
			},
		}
		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
		store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
		store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(0), errors.New("error counting backups"))
		err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			BackupVaultUUID: "testVaultID",
			AccountName:     "testAccount",
		})
		assert.NotNil(t, err)
		assert.EqualError(t, err, "error counting backups")
	})
	t.Run("OnBackupInTransitionState", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		backup := &datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
			VolumeUUID: "volumeUUID1",
		}
		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(true, nil)
		err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			BackupVaultUUID: "testVaultID",
			AccountName:     "testAccount",
		})
		assert.NotNil(t, err)
		assert.EqualError(t, err, "A backup operation from the same volume is currently in progress. Please wait for it to complete before starting a new backup")
	})
	t.Run("OnBackupInTransitionStateCheckError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		backup := &datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
			VolumeUUID: "volumeUUID1",
		}
		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, errors.New("transition state check error"))
		err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			BackupVaultUUID: "testVaultID",
			AccountName:     "testAccount",
		})
		assert.NotNil(t, err)
		assert.EqualError(t, err, "transition state check error")
	})

	t.Run("OnBackupDeleteFromDestinationRegion", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		backup := &datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
			VolumeUUID: "volumeUUID1",
			BackupVault: &datamodel.BackupVault{
				BackupVaultType:  activities.CrossRegionBackupType,
				BackupRegionName: nillable.ToPointer("us-central1"),
			},
		}
		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
		err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			BackupVaultUUID: "testVaultID",
			AccountName:     "testAccount",
			Region:          "us-central1",
		})
		assert.NotNil(t, err)
		assert.EqualError(t, err, "Cannot delete backup from the destination region")
	})

	// Immutable backup test cases
	t.Run("ImmutableBackupTests", func(t *testing.T) {
		t.Run("OnSuccessWithNonImmutableBackupVault", func(t *testing.T) {
			// Enable immutable backup feature for this test
			originalValue := utils.IsImmutableBackupEnabled()
			defer utils.SetImmutableBackupEnabledForTest(originalValue)
			utils.SetImmutableBackupEnabledForTest(true)

			ctx := context.Background()
			store := database.NewMockStorage(t)
			backup := &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
				VolumeUUID: "volumeUUID1",
				BackupVault: &datamodel.BackupVault{
					BackupVaultType: "IN_REGION",
				},
			}
			store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
			store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
			store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
			store.On("GetBackupVault", ctx, "testVaultID").Return(&datamodel.BackupVault{
				ImmutableAttributes: nil, // No immutable attributes
			}, nil)
			err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
				BackupUUID:      "testBackupUUID",
				BackupVaultUUID: "testVaultID",
				AccountName:     "testAccount",
			})
			assert.Nil(t, err)
		})

		t.Run("OnSuccessWithImmutableBackupVaultButNoRetentionPeriod", func(t *testing.T) {
			// Enable immutable backup feature for this test
			originalValue := utils.IsImmutableBackupEnabled()
			defer utils.SetImmutableBackupEnabledForTest(originalValue)
			utils.SetImmutableBackupEnabledForTest(true)

			ctx := context.Background()
			store := database.NewMockStorage(t)
			backup := &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
				VolumeUUID: "volumeUUID1",
				BackupVault: &datamodel.BackupVault{
					BackupVaultType: "IN_REGION",
				},
			}
			store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
			store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
			store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
			minRetDuration := int64(0)
			store.On("GetBackupVault", ctx, "testVaultID").Return(&datamodel.BackupVault{
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &minRetDuration,
					IsDailyBackupImmutable:                 false,
					IsWeeklyBackupImmutable:                false,
					IsMonthlyBackupImmutable:               false,
					IsAdhocBackupImmutable:                 false,
				},
			}, nil)
			err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
				BackupUUID:      "testBackupUUID",
				BackupVaultUUID: "testVaultID",
				AccountName:     "testAccount",
			})
			assert.Nil(t, err)
		})

		t.Run("OnFailureWithImmutableDailyBackupNotExpired", func(t *testing.T) {
			// Enable immutable backup feature for this test
			originalValue := utils.IsImmutableBackupEnabled()
			defer utils.SetImmutableBackupEnabledForTest(originalValue)
			utils.SetImmutableBackupEnabledForTest(true)

			ctx := context.Background()
			store := database.NewMockStorage(t)
			backupCreatedTime := time.Now().AddDate(0, 0, -5) // Created 5 days ago
			minRetDuration := int64(10)                       // 10 days retention (not expired)
			scheduleTag := common.ScheduleTagDaily
			backup := &datamodel.Backup{
				BaseModel:   datamodel.BaseModel{UUID: "testBackupUUID", CreatedAt: backupCreatedTime},
				VolumeUUID:  "volumeUUID1",
				Type:        common.BackupTypeSCHEDULED,
				ScheduleTag: &scheduleTag,
				BackupVault: &datamodel.BackupVault{
					BackupVaultType: "IN_REGION",
				},
			}
			store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
			store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
			store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
			store.On("GetBackupVault", ctx, "testVaultID").Return(&datamodel.BackupVault{
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &minRetDuration,
					IsDailyBackupImmutable:                 true,
					IsWeeklyBackupImmutable:                false,
					IsMonthlyBackupImmutable:               false,
					IsAdhocBackupImmutable:                 false,
				},
			}, nil)
			err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
				BackupUUID:      "testBackupUUID",
				BackupVaultUUID: "testVaultID",
				AccountName:     "testAccount",
			})
			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), "Cannot delete backup before minimum retention period")
		})

		t.Run("OnSuccessWithImmutableDailyBackupExpired", func(t *testing.T) {
			// Enable immutable backup feature for this test
			originalValue := utils.IsImmutableBackupEnabled()
			defer utils.SetImmutableBackupEnabledForTest(originalValue)
			utils.SetImmutableBackupEnabledForTest(true)

			ctx := context.Background()
			store := database.NewMockStorage(t)
			backupCreatedTime := time.Now().AddDate(0, 0, -15) // Created 15 days ago
			minRetDuration := int64(10)                        // 10 days retention (expired)
			scheduleTag := common.ScheduleTagDaily
			backup := &datamodel.Backup{
				BaseModel:   datamodel.BaseModel{UUID: "testBackupUUID", CreatedAt: backupCreatedTime},
				VolumeUUID:  "volumeUUID1",
				Type:        common.BackupTypeSCHEDULED,
				ScheduleTag: &scheduleTag,
				BackupVault: &datamodel.BackupVault{
					BackupVaultType: "IN_REGION",
				},
			}
			store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
			store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
			store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
			store.On("GetBackupVault", ctx, "testVaultID").Return(&datamodel.BackupVault{
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &minRetDuration,
					IsDailyBackupImmutable:                 true,
					IsWeeklyBackupImmutable:                false,
					IsMonthlyBackupImmutable:               false,
					IsAdhocBackupImmutable:                 false,
				},
			}, nil)
			err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
				BackupUUID:      "testBackupUUID",
				BackupVaultUUID: "testVaultID",
				AccountName:     "testAccount",
			})
			assert.Nil(t, err)
		})

		t.Run("OnSuccessWithNonImmutableBackupType", func(t *testing.T) {
			// Enable immutable backup feature for this test
			originalValue := utils.IsImmutableBackupEnabled()
			defer utils.SetImmutableBackupEnabledForTest(originalValue)
			utils.SetImmutableBackupEnabledForTest(true)

			ctx := context.Background()
			store := database.NewMockStorage(t)
			backupCreatedTime := time.Now().AddDate(0, 0, -5) // Created 5 days ago
			minRetDuration := int64(10)                       // 10 days retention
			scheduleTag := common.ScheduleTagWeekly           // Weekly backup but weekly immutable is disabled
			backup := &datamodel.Backup{
				BaseModel:   datamodel.BaseModel{UUID: "testBackupUUID", CreatedAt: backupCreatedTime},
				VolumeUUID:  "volumeUUID1",
				Type:        common.BackupTypeSCHEDULED,
				ScheduleTag: &scheduleTag,
				BackupVault: &datamodel.BackupVault{
					BackupVaultType: "IN_REGION",
				},
			}
			store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
			store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
			store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
			store.On("GetBackupVault", ctx, "testVaultID").Return(&datamodel.BackupVault{
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &minRetDuration,
					IsDailyBackupImmutable:                 true,  // Daily is immutable
					IsWeeklyBackupImmutable:                false, // Weekly is not immutable
					IsMonthlyBackupImmutable:               false,
					IsAdhocBackupImmutable:                 false,
				},
			}, nil)
			err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
				BackupUUID:      "testBackupUUID",
				BackupVaultUUID: "testVaultID",
				AccountName:     "testAccount",
			})
			assert.Nil(t, err) // Should succeed because weekly backup is not immutable
		})

		t.Run("OnFailureWithImmutableManualBackupNotExpired", func(t *testing.T) {
			// Enable immutable backup feature for this test
			originalValue := utils.IsImmutableBackupEnabled()
			defer utils.SetImmutableBackupEnabledForTest(originalValue)
			utils.SetImmutableBackupEnabledForTest(true)

			ctx := context.Background()
			store := database.NewMockStorage(t)
			backupCreatedTime := time.Now().AddDate(0, 0, -5) // Created 5 days ago
			minRetDuration := int64(10)                       // 10 days retention (not expired)
			backup := &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID", CreatedAt: backupCreatedTime},
				VolumeUUID: "volumeUUID1",
				Type:       common.BackupTypeMANUAL,
				BackupVault: &datamodel.BackupVault{
					BackupVaultType: "IN_REGION",
				},
			}
			store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
			store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
			store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
			store.On("GetBackupVault", ctx, "testVaultID").Return(&datamodel.BackupVault{
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &minRetDuration,
					IsDailyBackupImmutable:                 false,
					IsWeeklyBackupImmutable:                false,
					IsMonthlyBackupImmutable:               false,
					IsAdhocBackupImmutable:                 true, // Manual backup is immutable
				},
			}, nil)
			err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
				BackupUUID:      "testBackupUUID",
				BackupVaultUUID: "testVaultID",
				AccountName:     "testAccount",
			})
			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), "Cannot delete backup before minimum retention period")
		})

		t.Run("OnBackupVaultNotFound", func(t *testing.T) {
			// Enable immutable backup feature for this test
			originalValue := utils.IsImmutableBackupEnabled()
			defer utils.SetImmutableBackupEnabledForTest(originalValue)
			utils.SetImmutableBackupEnabledForTest(true)

			ctx := context.Background()
			store := database.NewMockStorage(t)
			backup := &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
				VolumeUUID: "volumeUUID1",
				BackupVault: &datamodel.BackupVault{
					BackupVaultType: "IN_REGION",
				},
			}
			store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
			store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
			store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
			store.On("GetBackupVault", ctx, "testVaultID").Return(nil, vsaerror.NewNotFoundErr("backup vault", nil))
			err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
				BackupUUID:      "testBackupUUID",
				BackupVaultUUID: "testVaultID",
				AccountName:     "testAccount",
			})
			assert.NotNil(t, err)
			assert.EqualError(t, err, "Backup vault not found")
		})

		t.Run("OnGetBackupVaultError", func(t *testing.T) {
			// Enable immutable backup feature for this test
			originalValue := utils.IsImmutableBackupEnabled()
			defer utils.SetImmutableBackupEnabledForTest(originalValue)
			utils.SetImmutableBackupEnabledForTest(true)

			ctx := context.Background()
			store := database.NewMockStorage(t)
			backup := &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
				VolumeUUID: "volumeUUID1",
				BackupVault: &datamodel.BackupVault{
					BackupVaultType: "IN_REGION",
				},
			}
			store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
			store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
			store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
			store.On("GetBackupVault", ctx, "testVaultID").Return(nil, errors.New("database error"))
			err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
				BackupUUID:      "testBackupUUID",
				BackupVaultUUID: "testVaultID",
				AccountName:     "testAccount",
			})
			assert.NotNil(t, err)
			assert.EqualError(t, err, "database error")
		})
	})
}

// TestValidateBackupDeleteParams_BackupVaultSwitching verifies that when
// EnableBackupVaultSwitching=true the factory skips the latest-backup check
// entirely (the snapmirror+endpoint validation now runs inside the workflow).
func TestValidateBackupDeleteParams_BackupVaultSwitching(t *testing.T) {
	originalSwitching := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(originalSwitching)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	originalImmutable := utils.IsImmutableBackupEnabled()
	defer utils.SetImmutableBackupEnabledForTest(originalImmutable)
	utils.SetImmutableBackupEnabledForTest(false)

	params := &common.DeleteBackupParams{
		BackupUUID:      "testBackupUUID",
		BackupVaultUUID: "testVaultID",
		AccountName:     "testAccount",
	}

	t.Run("SkipsLatestBackupCheckWhenVaultSwitchingEnabled", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "testBackupUUID"},
			VolumeUUID:    "volumeUUID1",
			BackupVaultID: 42,
			Attributes:    &datamodel.BackupAttributes{EndpointUUID: "some-endpoint"},
			BackupVault: &datamodel.BackupVault{
				BackupVaultType: "IN_REGION",
			},
		}
		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)

		err := validateBackupDeleteParams(ctx, store, params)
		assert.NoError(t, err)
		store.AssertExpectations(t)
		store.AssertNotCalled(t, "IsLatestBackupInVaultAndInEndpoint")
		store.AssertNotCalled(t, "BackupCountByVolumeIDVaultAndEndpoint")
		store.AssertNotCalled(t, "IsLatestBackup")
		store.AssertNotCalled(t, "BackupCountByVolumeID")
	})
}

func TestValidateBackupDeleteParams_ImmutabilityChecks(t *testing.T) {
	// Pin the legacy code path; new switching-enabled coverage lives in
	// TestValidateBackupDeleteParams_BackupVaultSwitching.
	originalSwitching := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(originalSwitching)
	utils.SetEnableBackupVaultSwitchingForTest(false)

	t.Run("OnSuccessWithNonImmutableBackupVault", func(t *testing.T) {
		// Enable immutable backup feature for this test
		utils.SetImmutableBackupEnabledForTest(true)
		defer utils.SetImmutableBackupEnabledForTest(false)

		ctx := context.Background()
		store := database.NewMockStorage(t)
		backup := &datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "testBackupUUID"},
			VolumeUUID: "volumeUUID1",
			Type:       common.BackupTypeSCHEDULED,
			Name:       "daily-backup-20230101",
			BackupVault: &datamodel.BackupVault{
				BackupVaultType: "IN_REGION",
			},
		}
		backupVault := &datamodel.BackupVault{
			ImmutableAttributes: nil, // No immutable attributes
		}

		store.On("GetBackup", ctx, "testVaultID", "testBackupUUID", "testAccount").Return(backup, nil)
		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, backup.VolumeUUID).Return(false, nil)
		store.On("IsLatestBackup", ctx, backup.UUID, backup.VolumeUUID).Return(false, nil)
		store.On("BackupCountByVolumeID", ctx, backup.VolumeUUID).Return(int64(2), nil)
		store.On("GetBackupVault", ctx, "testVaultID").Return(backupVault, nil)

		err := validateBackupDeleteParams(ctx, store, &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			BackupVaultUUID: "testVaultID",
			AccountName:     "testAccount",
		})
		assert.Nil(t, err)
	})
}

func TestGetBackupsUnderBackupVault(t *testing.T) {
	t.Run("WhenGetBackupsFails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		orch := &GCPOrchestrator{storage: mockStorage}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, ownerID string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		getBackups = func(ctx context.Context, se database.Storage, params *common.GetBackupsParams, filters [][]interface{}) ([]*datamodel.Backup, error) {
			return nil, errors.New("failed to get backups")
		}
		defer func() { getBackups = _getBackups }()
		mockStorage.On("GetBackupsByBackupVaultOwnerIDAndFilter", ctx, "backupVaultID", int64(1), [][]interface{}{
			{"uuid in ?", []string{"backupUUID"}},
		}).Return(nil, errors.New("failed to get backups"))
		backups, err := orch.GetBackupsUnderBackupVault(ctx, "backupVaultID", "ownerID", []string{"backupUUID"})
		assert.Nil(tt, backups)
		assert.EqualError(tt, err, "failed to get backups")
	})

	t.Run("WhenGetBackupsSucceeds", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		orch := &GCPOrchestrator{storage: mockStorage}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, ownerID string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		expectedBackups := []*datamodel.Backup{
			{BaseModel: datamodel.BaseModel{UUID: "backupUUID1"}},
			{BaseModel: datamodel.BaseModel{UUID: "backupUUID2"}},
		}
		getBackups = func(ctx context.Context, se database.Storage, params *common.GetBackupsParams, filters [][]interface{}) ([]*datamodel.Backup, error) {
			return expectedBackups, nil
		}
		defer func() { getBackups = _getBackups }()
		// Set up the mock expectation for GetBackupsByBackupVaultOwnerIDAndFilter
		mockStorage.On("GetBackupsByBackupVaultOwnerIDAndFilter", ctx, "backupVaultID", int64(1), [][]interface{}{
			{"uuid in ?", []string{"backupUUID"}},
		}).Return(expectedBackups, nil)
		backups, err := orch.GetBackupsUnderBackupVault(ctx, "backupVaultID", "ownerID", []string{"backupUUID"})
		assert.NoError(tt, err)
		assert.Equal(tt, expectedBackups, backups)
	})
}

func TestUpdateBackup(t *testing.T) {
	t.Run("onGetAccountError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.UpdateBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		_, _, err := updateBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "account not found")
	})

	t.Run("onGetBackupError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.UpdateBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(nil, errors.New("backup not found"))

		_, _, err := updateBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "backup not found")
	})

	t.Run("onWorkflowExecutionError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.UpdateBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
			Description:     "Updated description",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: params.BackupUUID},
			BackupVault: &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: params.BackupVaultUUID}},
			State:       datamodel.LifeCycleStateAvailable,
		}
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(backup, nil)
		store.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}, nil)
		store.On("UpdateBackupState", ctx, mock.Anything).Return(backup, nil)
		store.On("UpdateBackupState", ctx, mock.Anything).Return(&datamodel.Backup{}, nil)
		store.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow execution failed")).Once()

		_, _, err := updateBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "workflow execution failed")
	})

	t.Run("onInvalidBackupState", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.UpdateBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: params.BackupUUID},
			BackupVault: &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: params.BackupVaultUUID}},
			State:       datamodel.LifeCycleStateCreating,
		}
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(backup, nil)

		_, _, err := updateBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "Backup can only be updated when in AVAILABLE state, current state: CREATING")
	})

	t.Run("onUpdatingBackupFromDestinationRegion", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.UpdateBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
			Region:          "us-central1",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: params.BackupUUID},
			BackupVault: &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: params.BackupVaultUUID}, BackupVaultType: activities.CrossRegionBackupType, BackupRegionName: nillable.ToPointer("us-central1")},
			State:       datamodel.LifeCycleStateAvailable,
		}
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(backup, nil)

		_, _, err := updateBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "Cannot update backup from the destination region")
	})

	t.Run("onJobCreationError", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		params := &common.UpdateBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
			Description:     "Updated description",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()
		backup := &datamodel.Backup{
			BaseModel:   datamodel.BaseModel{UUID: params.BackupUUID},
			BackupVault: &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: params.BackupVaultUUID}},
			State:       datamodel.LifeCycleStateAvailable,
		}
		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(backup, nil)
		store.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("job creation failed"))

		_, _, err := updateBackup(ctx, store, temporal, params)
		assert.EqualError(t, err, "job creation failed")
	})
}

func TestValidateSnapshotForBackup_SnapshotAlreadyUsed_Integration(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	t.Run("SnapshotAlreadyUsedForAvailableBackup", func(t *testing.T) {
		store, err := database.NewTestStorage(mockLogger)
		assert.NoError(t, err, "Failed to create test storage")
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(t, err, "Failed to clear in-memory DB")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-account",
		}
		err = store.DB().Create(account).Error
		assert.NoError(t, err)

		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:             "test-vault",
			AccountID:        account.ID,
			SourceRegionName: func() *string { s := "us-east1"; return &s }(),
		}
		err = store.DB().Create(backupVault).Error
		assert.NoError(t, err)

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-volume",
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: backupVault.UUID,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFS"},
			},
		}
		err = store.DB().Create(volume).Error
		assert.NoError(t, err)

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-snapshot",
			VolumeID:  volume.ID,
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateREADY,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				ExternalUUID: "ext-snapshot-uuid",
			},
		}
		err = store.DB().Create(snapshot).Error
		assert.NoError(t, err)

		existingBackup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:          "existing-backup",
			VolumeUUID:    volume.UUID,
			BackupVaultID: backupVault.ID,
			State:         datamodel.LifeCycleStateAvailable,
			Attributes: &datamodel.BackupAttributes{
				SnapshotID: snapshot.SnapshotAttributes.ExternalUUID,
			},
		}
		err = store.DB().Create(existingBackup).Error
		assert.NoError(t, err)

		params := &common.CreateBackupParams{
			BackupName:          "new-backup",
			VolumeUUID:          volume.UUID,
			BackupVaultID:       backupVault.UUID,
			UseExistingSnapshot: true,
			SnapshotID:          snapshot.UUID,
		}

		vol, err := store.GetVolume(ctx, volume.UUID)
		assert.NoError(t, err)

		err = _validateSnapshotForBackup(ctx, store, params, vol)
		assert.EqualError(t, err, "This snapshot has already been used to create a backup")
	})

	t.Run("SnapshotUsedForNonAvailableBackup_ShouldPass", func(t *testing.T) {
		store, err := database.NewTestStorage(mockLogger)
		assert.NoError(t, err, "Failed to create test storage")
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(t, err, "Failed to clear in-memory DB")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-account",
		}
		err = store.DB().Create(account).Error
		assert.NoError(t, err)

		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:             "test-vault",
			AccountID:        account.ID,
			SourceRegionName: func() *string { s := "us-east1"; return &s }(),
		}
		err = store.DB().Create(backupVault).Error
		assert.NoError(t, err)

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-volume",
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: backupVault.UUID,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFS"},
			},
		}
		err = store.DB().Create(volume).Error
		assert.NoError(t, err)

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-snapshot",
			VolumeID:  volume.ID,
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateREADY,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				ExternalUUID: "ext-snapshot-uuid",
			},
		}
		err = store.DB().Create(snapshot).Error
		assert.NoError(t, err)

		assert.NoError(t, err)

		params := &common.CreateBackupParams{
			BackupName:          "new-backup",
			VolumeUUID:          volume.UUID,
			BackupVaultID:       backupVault.UUID,
			UseExistingSnapshot: true,
			SnapshotID:          snapshot.UUID,
		}

		vol, err := store.GetVolume(ctx, volume.UUID)
		assert.NoError(t, err)

		err = _validateSnapshotForBackup(ctx, store, params, vol)
		assert.NoError(t, err)
	})

	t.Run("MultipleBackupsCreationAttempts_BlocksSecondAttempt", func(t *testing.T) {
		store, err := database.NewTestStorage(mockLogger)
		assert.NoError(t, err, "Failed to create test storage")
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(t, err, "Failed to clear in-memory DB")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-account",
		}
		err = store.DB().Create(account).Error
		assert.NoError(t, err)

		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:             "test-vault",
			AccountID:        account.ID,
			SourceRegionName: func() *string { s := "us-east1"; return &s }(),
		}
		err = store.DB().Create(backupVault).Error
		assert.NoError(t, err)

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-volume",
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: backupVault.UUID,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFS"},
			},
		}
		err = store.DB().Create(volume).Error
		assert.NoError(t, err)

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-snapshot",
			VolumeID:  volume.ID,
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateREADY,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				ExternalUUID: "ext-snapshot-uuid",
			},
		}
		err = store.DB().Create(snapshot).Error
		assert.NoError(t, err)

		firstBackup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:          "first-backup",
			VolumeUUID:    volume.UUID,
			BackupVaultID: backupVault.ID,
			State:         datamodel.LifeCycleStateAvailable,
			Attributes: &datamodel.BackupAttributes{
				SnapshotID: snapshot.SnapshotAttributes.ExternalUUID,
			},
		}
		err = store.DB().Create(firstBackup).Error
		assert.NoError(t, err)

		params := &common.CreateBackupParams{
			BackupName:          "second-backup",
			VolumeUUID:          volume.UUID,
			BackupVaultID:       backupVault.UUID,
			UseExistingSnapshot: true,
			SnapshotID:          snapshot.UUID,
		}

		vol, err := store.GetVolume(ctx, volume.UUID)
		assert.NoError(t, err)

		err = _validateSnapshotForBackup(ctx, store, params, vol)
		assert.EqualError(t, err, "This snapshot has already been used to create a backup")
	})

	t.Run("DifferentSnapshots_AllowMultipleBackups", func(t *testing.T) {
		store, err := database.NewTestStorage(mockLogger)
		assert.NoError(t, err, "Failed to create test storage")
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(t, err, "Failed to clear in-memory DB")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-account",
		}
		err = store.DB().Create(account).Error
		assert.NoError(t, err)

		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:             "test-vault",
			AccountID:        account.ID,
			SourceRegionName: func() *string { s := "us-east1"; return &s }(),
		}
		err = store.DB().Create(backupVault).Error
		assert.NoError(t, err)

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-volume",
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: backupVault.UUID,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFS"},
			},
		}
		err = store.DB().Create(volume).Error
		assert.NoError(t, err)

		snapshot1 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-snapshot-1",
			VolumeID:  volume.ID,
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateREADY,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				ExternalUUID: "ext-snapshot-uuid-1",
			},
		}
		err = store.DB().Create(snapshot1).Error
		assert.NoError(t, err)

		backup1 := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:          "backup-1",
			VolumeUUID:    volume.UUID,
			BackupVaultID: backupVault.ID,
			State:         datamodel.LifeCycleStateAvailable,
			Attributes: &datamodel.BackupAttributes{
				SnapshotID: snapshot1.SnapshotAttributes.ExternalUUID,
			},
		}
		err = store.DB().Create(backup1).Error
		assert.NoError(t, err)

		snapshot2 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "test-snapshot-2",
			VolumeID:  volume.ID,
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateREADY,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				ExternalUUID: "ext-snapshot-uuid-2",
			},
		}
		err = store.DB().Create(snapshot2).Error
		assert.NoError(t, err)

		params := &common.CreateBackupParams{
			BackupName:          "backup-2",
			VolumeUUID:          volume.UUID,
			BackupVaultID:       backupVault.UUID,
			UseExistingSnapshot: true,
			SnapshotID:          snapshot2.UUID,
		}

		vol, err := store.GetVolume(ctx, volume.UUID)
		assert.NoError(t, err)

		// This should succeed because snapshot2 has not been used for any backup yet
		err = _validateSnapshotForBackup(ctx, store, params, vol)
		assert.NoError(t, err)
	})
}

func TestValidateCreateBackupParams_VolumeValidations(t *testing.T) {
	ctx := context.Background()

	t.Run("VolumeNotInReadyState", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID: "test-volume-uuid",
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolume", ctx, params.VolumeUUID).Return(&datamodel.Volume{
			State: datamodel.LifeCycleStateCreating,
		}, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Volume is not in available state")
	})

	t.Run("VolumeIsSplittingThinClone", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID: "test-volume-uuid",
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolume", ctx, params.VolumeUUID).Return(&datamodel.Volume{
			State: datamodel.LifeCycleStateREADY,
			VolumeAttributes: &datamodel.VolumeAttributes{
				CloneParentInfo: &datamodel.CloneParentInfo{
					ParentVolumeUUID: "parent-vol-uuid",
					State:            datamodel.CloneStateSplitting,
				},
			},
		}, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Backup is not allowed when volume is splitting")
	})

	t.Run("VolumeIsClonedButNotSplitting", func(t *testing.T) {
		// Verify a CLONED (non-splitting) thin clone is NOT blocked by the splitting check.
		// The volume has no DataProtection so the next guard fires, proving the splitting
		// check was passed successfully.
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID: "test-volume-uuid",
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolume", ctx, params.VolumeUUID).Return(&datamodel.Volume{
			State: datamodel.LifeCycleStateREADY,
			VolumeAttributes: &datamodel.VolumeAttributes{
				CloneParentInfo: &datamodel.CloneParentInfo{
					ParentVolumeUUID: "parent-vol-uuid",
					State:            datamodel.CloneStateCloned,
				},
			},
			DataProtection: nil,
		}, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Volume does not have any backup vault associated with it")
	})

	t.Run("VolumeWithoutDataProtection", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID: "test-volume-uuid",
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolume", ctx, params.VolumeUUID).Return(&datamodel.Volume{
			State:          datamodel.LifeCycleStateREADY,
			DataProtection: nil,
		}, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Volume does not have any backup vault associated with it")
	})

	t.Run("VolumeWithDifferentBackupVault", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:    "test-volume-uuid",
			BackupVaultID: "requested-vault-id",
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolume", ctx, params.VolumeUUID).Return(&datamodel.Volume{
			State: datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "different-vault-id",
			},
		}, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Volume does not have the specified backup vault associated with it")
	})

	t.Run("DestinationVolumeWithoutSnapshotID", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:    "test-volume-uuid",
			BackupVaultID: "vault-id",
			SnapshotID:    "", // No snapshot ID provided
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolume", ctx, params.VolumeUUID).Return(&datamodel.Volume{
			State: datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-id",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				IsDataProtection: true,
			},
		}, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Backup creation is not supported for destination volumes without specifying an existing snapshot. Please use an existing snapshot to create backups or create a snapshot on the source volume and back that up on this volume once it has been replicated to this volume")
	})

	t.Run("BackupNameWithSnapmirrorPrefix", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:    "test-volume-uuid",
			BackupVaultID: "vault-id",
			BackupName:    "snapmirror.12345678.2023-01-01_010101",
			SnapshotID:    "snapshot-id",
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolume", ctx, params.VolumeUUID).Return(&datamodel.Volume{
			State: datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-id",
			},
		}, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Backups cannot be created from snapshots resulting from volume replication. Please use a non-replication snapshot and update the backup name to a non-replication snapshot name")
	})
}

// TestValidateCreateBackupParams_RegularVolume_ValidateSnapshotError tests the regular volume path when validateSnapshotForBackup fails
func TestValidateCreateBackupParams_RegularVolume_ValidateSnapshotError(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenValidateSnapshotForBackupFails", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:         "test-volume-uuid",
			BackupVaultID:      "vault-id",
			BackupName:         "test-backup",
			AccountName:        "test-account",
			IsExpertModeVolume: false,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "account-uuid",
			},
			Name: params.AccountName,
		}

		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: params.VolumeUUID,
				ID:   1,
			},
			State: datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: params.BackupVaultID,
			},
			AccountID: 1,
			Account:   account,
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolume", ctx, params.VolumeUUID).Return(vol, nil)

		// Mock validateSnapshotForBackup to fail
		originalValidateSnapshotForBackup := validateSnapshotForBackup
		defer func() { validateSnapshotForBackup = originalValidateSnapshotForBackup }()
		validateSnapshotForBackup = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams, vol *datamodel.Volume) error {
			return vsaerror.NewUserInputValidationErr("Snapshot validation failed")
		}

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Failed to validate snapshot for backup: Snapshot validation failed")
	})

	t.Run("WhenRegularVolumeValidationSucceeds", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:         "test-volume-uuid",
			BackupVaultID:      "vault-id",
			BackupName:         "test-backup",
			AccountName:        "test-account",
			IsExpertModeVolume: false,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "account-uuid",
			},
			Name: params.AccountName,
		}

		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: params.VolumeUUID,
				ID:   1,
			},
			State: datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: params.BackupVaultID,
			},
			AccountID: 1,
			Account:   account,
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolume", ctx, params.VolumeUUID).Return(vol, nil)

		// Mock validateSnapshotForBackup to succeed
		originalValidateSnapshotForBackup := validateSnapshotForBackup
		defer func() { validateSnapshotForBackup = originalValidateSnapshotForBackup }()
		validateSnapshotForBackup = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams, vol *datamodel.Volume) error {
			return nil
		}

		err := _validateCreateBackupParams(ctx, store, params)
		assert.NoError(t, err)
	})
}

func TestValidateCreateBackupParams_ExistingSnapshot(t *testing.T) {
	ctx := context.Background()

	t.Run("UseExistingSnapshotWithoutSnapshotID", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:          "test-volume-uuid",
			BackupVaultID:       "vault-id",
			BackupName:          "test-backup",
			UseExistingSnapshot: true,
			SnapshotID:          "", // Missing snapshot ID
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-id",
			},
			AccountID: 1,
		}
		store.On("GetVolume", ctx, params.VolumeUUID).Return(vol, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Failed to validate snapshot for backup: Missing value for 'SnapshotID'")
	})

	t.Run("UseExistingSnapshotNotFound", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:          "test-volume-uuid",
			BackupVaultID:       "vault-id",
			BackupName:          "test-backup",
			UseExistingSnapshot: true,
			SnapshotID:          "snapshot-id",
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-id",
			},
			AccountID: 1,
		}
		store.On("GetVolume", ctx, params.VolumeUUID).Return(vol, nil)
		store.On("GetSnapshotByUUID", ctx, params.SnapshotID, vol.AccountID, vol.ID).Return(nil, vsaerror.NewNotFoundErr("snapshot", nil))

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Failed to validate snapshot for backup: Snapshot not found")
	})

	t.Run("UseExistingSnapshotNotReady", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:          "test-volume-uuid",
			BackupVaultID:       "vault-id",
			BackupName:          "test-backup",
			UseExistingSnapshot: true,
			SnapshotID:          "snapshot-id",
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-id",
			},
			AccountID: 1,
		}
		store.On("GetVolume", ctx, params.VolumeUUID).Return(vol, nil)
		store.On("GetSnapshotByUUID", ctx, params.SnapshotID, vol.AccountID, vol.ID).Return(&datamodel.Snapshot{
			State: datamodel.LifeCycleStateCreating,
		}, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Failed to validate snapshot for backup: Snapshot is not in available state")
	})

	t.Run("UseExistingSnapshotWithSnapmirrorName", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:          "test-volume-uuid",
			BackupVaultID:       "vault-id",
			BackupName:          "test-backup",
			UseExistingSnapshot: true,
			SnapshotID:          "snapshot-id",
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-id",
			},
			AccountID: 1,
		}
		store.On("GetVolume", ctx, params.VolumeUUID).Return(vol, nil)
		store.On("GetSnapshotByUUID", ctx, params.SnapshotID, vol.AccountID, vol.ID).Return(&datamodel.Snapshot{
			State: datamodel.LifeCycleStateREADY,
			Name:  "snapmirror.12345678.2023-01-01_010101",
		}, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Failed to validate snapshot for backup: Backups cannot be created from snapshots resulting from volume replication. Please use a non-replication snapshot.")
	})
}

func TestValidateCreateBackupParams_NewSnapshot(t *testing.T) {
	ctx := context.Background()

	t.Run("NotUseExistingSnapshotWithSnapshotID", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:          "test-volume-uuid",
			BackupVaultID:       "vault-id",
			BackupName:          "test-backup",
			UseExistingSnapshot: false,
			SnapshotID:          "snapshot-id", // Should not be set
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-id",
			},
			AccountID: 1,
		}
		store.On("GetVolume", ctx, params.VolumeUUID).Return(vol, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Failed to validate snapshot for backup: Cannot set Snapshot ID when useExistingSnapshot is false")
	})

	t.Run("BackupNameConflictsWithExistingSnapshot", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:          "test-volume-uuid",
			BackupVaultID:       "vault-id",
			BackupName:          "existing-snapshot-name",
			UseExistingSnapshot: false,
			SnapshotID:          "",
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-id",
			},
			AccountID: 1,
		}
		store.On("GetVolume", ctx, params.VolumeUUID).Return(vol, nil)
		store.On("GetSnapshotByNameAndVolumeId", ctx, params.BackupName, vol.AccountID, vol.ID).Return(&datamodel.Snapshot{
			Name: "existing-snapshot-name",
		}, nil)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.EqualError(t, err, "Failed to validate snapshot for backup: Backup creation failed because the name conflicts with an existing snapshot. Please use the existing snapshot or choose a new backup name. A new name will create a new snapshot for the backup")
	})

	t.Run("ValidNewSnapshot", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:          "test-volume-uuid",
			BackupVaultID:       "vault-id",
			BackupName:          "new-backup",
			UseExistingSnapshot: false,
			SnapshotID:          "",
		}

		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "vault-id",
			},
			AccountID: 1,
		}
		store.On("GetVolume", ctx, params.VolumeUUID).Return(vol, nil)
		store.On("GetSnapshotByNameAndVolumeId", ctx, params.BackupName, vol.AccountID, vol.ID).Return(nil, vsaerror.NewNotFoundErr("snapshot", nil))

		err := _validateCreateBackupParams(ctx, store, params)
		assert.NoError(t, err)
	})
}

// TestBackupCreationIntegration covers comprehensive backup creation scenarios
// including validation, edge cases, and error conditions
func TestBackupCreationIntegration(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	// Helper function to create common test objects
	createCommonTestObjects := func() (*datamodel.Account, *datamodel.Volume, *datamodel.BackupVault) {
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test-account",
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "test-vault-id",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols:        []string{"NFS"},
				IsDataProtection: false,
			},
		}

		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-vault-id"},
			Name:             "test-vault",
			AccountID:        1,
			SourceRegionName: func() *string { s := "us-east1"; return &s }(),
		}

		return account, volume, backupVault
	}

	t.Run("PositiveScenarios", func(t *testing.T) {
		t.Run("CreateBackupWithoutExistingSnapshot_Success", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				Description:         "Test backup",
				BackupType:          "MANUAL",
				UseExistingSnapshot: false,
				SnapshotID:          "",
			}

			job := &datamodel.Job{
				BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
				WorkflowID: "workflow-id",
			}

			backup := &datamodel.Backup{
				BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
				Name:          params.BackupName,
				VolumeUUID:    params.VolumeUUID,
				BackupVaultID: backupVault.ID,
				BackupVault:   backupVault, // Add the BackupVault relationship
				State:         datamodel.LifeCycleStateCreating,
				StateDetails:  datamodel.LifeCycleStateCreatingDetails,
				Attributes: &datamodel.BackupAttributes{
					VolumeName:          volume.Name,
					AccountIdentifier:   account.Name,
					Protocols:           volume.VolumeAttributes.Protocols,
					UseExistingSnapshot: false,
				},
				Description: params.Description,
				Type:        params.BackupType,
			}

			// Mock the validation and creation flow
			validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
				return nil
			}
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				validateCreateBackupParams = _validateCreateBackupParams
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls
			store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, account.ID).Return(volume, nil)
			store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
			store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
			store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
				return j.JobAttributes != nil && j.JobAttributes.ResourceUUID == backup.UUID
			})).Return(job, nil)

			// Mock successful workflow execution
			temporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

			// Act
			result, jobID, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, job.UUID, jobID)
			assert.Equal(t, backup.Name, result.Name)
			assert.Equal(t, backup.State, result.LifeCycleState)
			// Note: UseExistingSnapshot is not populated in the models.Backup conversion
		})

		t.Run("CreateBackupWithExistingSnapshot_ValidSnapshot_Success", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			existingSnapshot := &datamodel.Snapshot{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "snapshot-uuid"},
				Name:      "existing-snapshot",
				State:     datamodel.LifeCycleStateREADY,
				VolumeID:  volume.ID,
				AccountID: account.ID,
				SnapshotAttributes: &datamodel.SnapshotAttributes{
					ExternalUUID: "external-snapshot-uuid",
				},
			}

			params := &common.CreateBackupParams{
				BackupName:          "test-backup-with-snapshot",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				Description:         "Backup with existing snapshot",
				BackupType:          "MANUAL",
				UseExistingSnapshot: true,
				SnapshotID:          existingSnapshot.UUID,
			}

			job := &datamodel.Job{
				BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
				WorkflowID: "workflow-id",
			}

			backup := &datamodel.Backup{
				BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
				Name:          params.BackupName,
				VolumeUUID:    params.VolumeUUID,
				BackupVaultID: backupVault.ID,
				BackupVault:   backupVault, // Add the BackupVault relationship
				State:         datamodel.LifeCycleStateCreating,
				StateDetails:  datamodel.LifeCycleStateCreatingDetails,
				Attributes: &datamodel.BackupAttributes{
					VolumeName:          volume.Name,
					AccountIdentifier:   account.Name,
					Protocols:           volume.VolumeAttributes.Protocols,
					UseExistingSnapshot: true,
					SnapshotName:        existingSnapshot.Name,
					SnapshotID:          existingSnapshot.SnapshotAttributes.ExternalUUID,
				},
				Description: params.Description,
				Type:        params.BackupType,
			}

			// Mock functions
			validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
				return nil
			}
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				validateCreateBackupParams = _validateCreateBackupParams
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls
			store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, account.ID).Return(volume, nil)
			store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
			store.On("GetSnapshotByUUID", ctx, params.SnapshotID, account.ID, volume.ID).Return(existingSnapshot, nil)
			store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
			store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
				return j.JobAttributes != nil && j.JobAttributes.ResourceUUID == backup.UUID
			})).Return(job, nil)

			// Mock successful workflow execution
			temporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

			// Act
			result, jobID, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, job.UUID, jobID)
			assert.Equal(t, backup.Name, result.Name)
			// Note: UseExistingSnapshot is not populated in the models.Backup conversion
			assert.Equal(t, existingSnapshot.Name, result.SnapshotName)
			// Note: models.Backup doesn't have SnapshotID field - it only has SnapshotName
		})
	})

	t.Run("NegativeScenarios", func(t *testing.T) {
		t.Run("CreateBackupWithExistingSnapshot_SnapshotNotAvailable", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			notAvailableSnapshot := &datamodel.Snapshot{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "snapshot-uuid"},
				Name:      "creating-snapshot",
				State:     datamodel.LifeCycleStateCreating, // Not in READY state
				VolumeID:  volume.ID,
				AccountID: account.ID,
				SnapshotAttributes: &datamodel.SnapshotAttributes{
					ExternalUUID: "external-snapshot-uuid",
				},
			}

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				UseExistingSnapshot: true,
				SnapshotID:          notAvailableSnapshot.UUID,
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls for validation
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)
			store.On("GetSnapshotByUUID", ctx, params.SnapshotID, account.ID, volume.ID).Return(notAvailableSnapshot, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Snapshot is not in available state")
		})

		t.Run("CreateBackupWithoutExistingSnapshot_BackupNameConflictsWithSnapshot", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			conflictingSnapshot := &datamodel.Snapshot{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "conflicting-name",
			}

			params := &common.CreateBackupParams{
				BackupName:          "conflicting-name", // Same as existing snapshot
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				UseExistingSnapshot: false,
				SnapshotID:          "",
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls for validation
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)
			store.On("GetSnapshotByNameAndVolumeId", ctx, params.BackupName, volume.AccountID, volume.ID).Return(conflictingSnapshot, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Backup creation failed because the name conflicts with an existing snapshot")
		})

		t.Run("CreateBackupWithSnapmirrorPrefix_ShouldFail", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			params := &common.CreateBackupParams{
				BackupName:          "snapmirror.12345678.2023-01-01_010101", // Snapmirror prefix
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				UseExistingSnapshot: false,
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls for validation
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Backups cannot be created from snapshots resulting from volume replication")
		})

		t.Run("CreateBackupWithExistingSnapshot_SnapshotNotFound", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				UseExistingSnapshot: true,
				SnapshotID:          "nonexistent-snapshot-uuid",
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls for validation
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)
			store.On("GetSnapshotByUUID", ctx, params.SnapshotID, account.ID, volume.ID).Return(nil, vsaerror.NewNotFoundErr("snapshot", nil))

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Snapshot not found")
		})

		t.Run("CreateBackupWithExistingSnapshot_SnapshotWithSnapmirrorName", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			snapmirrorSnapshot := &datamodel.Snapshot{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "snapshot-uuid"},
				Name:      "snapmirror.12345678.2023-01-01_010101", // Snapmirror name
				State:     datamodel.LifeCycleStateREADY,
				VolumeID:  volume.ID,
				AccountID: account.ID,
			}

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				UseExistingSnapshot: true,
				SnapshotID:          snapmirrorSnapshot.UUID,
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls for validation
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)
			store.On("GetSnapshotByUUID", ctx, params.SnapshotID, account.ID, volume.ID).Return(snapmirrorSnapshot, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Backups cannot be created from snapshots resulting from volume replication")
		})

		t.Run("CreateBackupWithExistingSnapshot_SnapshotInUseByActiveBackup", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			existingSnapshot := &datamodel.Snapshot{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "snapshot-uuid"},
				Name:      "existing-snapshot",
				State:     datamodel.LifeCycleStateREADY,
				VolumeID:  volume.ID,
				AccountID: account.ID,
			}

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				UseExistingSnapshot: true,
				SnapshotID:          existingSnapshot.UUID,
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls - simulate snapshot being used by active backup
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(true, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "A backup operation from the same volume is currently in progress")
		})

		t.Run("CreateBackupWithDataProtectionVolume_WithoutSnapshotID", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, _, backupVault := createCommonTestObjects()

			// Create data protection volume (replication destination)
			dataProtectionVolume := &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
				Name:      "test-dp-volume",
				AccountID: 1,
				Account:   account,
				State:     datamodel.LifeCycleStateREADY,
				DataProtection: &datamodel.DataProtection{
					BackupVaultID: "test-vault-id",
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					Protocols:        []string{"NFS"},
					IsDataProtection: true, // This is a data protection volume
				},
			}

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          dataProtectionVolume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				UseExistingSnapshot: false,
				SnapshotID:          "", // No snapshot ID provided for DP volume
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls for validation
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(dataProtectionVolume, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Backup creation is not supported for destination volumes without specifying an existing snapshot")
		})

		t.Run("CreateBackupWithoutExistingSnapshot_ButSnapshotIDProvided", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				UseExistingSnapshot: false,           // Not using existing snapshot
				SnapshotID:          "snapshot-uuid", // But snapshot ID is provided
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls for validation
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Cannot set Snapshot ID when useExistingSnapshot is false")
		})

		t.Run("CreateBackupWithExistingSnapshot_MissingSnapshotID", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				UseExistingSnapshot: true, // Using existing snapshot
				SnapshotID:          "",   // But no snapshot ID provided
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls for validation
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Missing value for 'SnapshotID'")
		})

		t.Run("CreateBackup_VolumeNotInReadyState", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			// Change volume state to not ready
			volume.State = datamodel.LifeCycleStateCreating

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				UseExistingSnapshot: false,
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls for validation
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Volume is not in available state")
		})

		t.Run("CreateBackup_VolumeWithoutDataProtection", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, _ := createCommonTestObjects()

			// Remove data protection from volume
			volume.DataProtection = nil

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       "vault-id",
				AccountName:         account.Name,
				UseExistingSnapshot: false,
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls for validation
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Volume does not have any backup vault associated with it")
		})

		t.Run("CreateBackup_VolumeWithDifferentBackupVault", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, _ := createCommonTestObjects()

			// Set different backup vault ID in volume
			volume.DataProtection.BackupVaultID = "different-vault-id"

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       "requested-vault-id", // Different from volume's vault
				AccountName:         account.Name,
				UseExistingSnapshot: false,
			}

			// Mock validation functions
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls for validation
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Volume does not have the specified backup vault associated with it")
		})
	})

	t.Run("EdgeCases", func(t *testing.T) {
		t.Run("CreateBackup_AccountNotFound", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)

			params := &common.CreateBackupParams{
				BackupName:    "test-backup",
				VolumeUUID:    "volume-uuid",
				BackupVaultID: "vault-id",
				AccountName:   "nonexistent-account",
			}

			// Mock account not found
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return nil, vsaerror.NewNotFoundErr("account", nil)
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
		})

		t.Run("CreateBackup_VolumeNotFound", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, _, _ := createCommonTestObjects()

			params := &common.CreateBackupParams{
				BackupName:    "test-backup",
				VolumeUUID:    "nonexistent-volume-uuid",
				BackupVaultID: "vault-id",
				AccountName:   account.Name,
			}

			// Mock functions
			validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
				return nil
			}
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				validateCreateBackupParams = _validateCreateBackupParams
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock volume not found
			store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, account.ID).Return(nil, vsaerror.NewNotFoundErr("volume", nil))

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
		})

		t.Run("CreateBackup_BackupVaultNotFound", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, _ := createCommonTestObjects()

			params := &common.CreateBackupParams{
				BackupName:    "test-backup",
				VolumeUUID:    volume.UUID,
				BackupVaultID: "nonexistent-vault-id",
				AccountName:   account.Name,
			}

			// Mock functions
			validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
				return nil
			}
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				validateCreateBackupParams = _validateCreateBackupParams
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls
			store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, account.ID).Return(volume, nil)
			store.On("GetBackupVault", ctx, params.BackupVaultID).Return(nil, vsaerror.NewNotFoundErr("backup vault", nil))

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "Backup vault not found")
		})

		t.Run("CreateBackup_JobCreationFailure", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			params := &common.CreateBackupParams{
				BackupName:    "test-backup",
				VolumeUUID:    volume.UUID,
				BackupVaultID: backupVault.UUID,
				AccountName:   account.Name,
			}

			// Mock functions
			validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
				return nil
			}
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				validateCreateBackupParams = _validateCreateBackupParams
				getOrCreateAccount = _getOrCreateAccount
			}()

			backup := &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: "backup-uuid"}, Name: params.BackupName}

			// Mock storage calls
			store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, account.ID).Return(volume, nil)
			store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
			store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
			store.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))
			store.On("DeleteBackup", ctx, backup.UUID).Return(backup, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "failed to create job")
		})

		t.Run("CreateBackup_BackupCreationFailure", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			params := &common.CreateBackupParams{
				BackupName:    "test-backup",
				VolumeUUID:    volume.UUID,
				BackupVaultID: backupVault.UUID,
				AccountName:   account.Name,
			}

			// Mock functions
			validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
				return nil
			}
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				validateCreateBackupParams = _validateCreateBackupParams
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls
			store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, account.ID).Return(volume, nil)
			store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
			store.On("CreateBackup", ctx, mock.Anything).Return(nil, errors.New("failed to create backup"))

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "failed to create backup")
		})

		t.Run("CreateBackup_WorkflowExecutionFailure", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			params := &common.CreateBackupParams{
				BackupName:    "test-backup",
				VolumeUUID:    volume.UUID,
				BackupVaultID: backupVault.UUID,
				AccountName:   account.Name,
			}

			job := &datamodel.Job{
				BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
				WorkflowID: "workflow-id",
			}

			backup := &datamodel.Backup{
				BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
				Name:          params.BackupName,
				VolumeUUID:    params.VolumeUUID,
				BackupVaultID: backupVault.ID,
				BackupVault:   backupVault, // Add the BackupVault relationship
				State:         datamodel.LifeCycleStateCreating,
				StateDetails:  datamodel.LifeCycleStateCreatingDetails,
				Attributes: &datamodel.BackupAttributes{
					VolumeName:          volume.Name,
					AccountIdentifier:   account.Name,
					Protocols:           volume.VolumeAttributes.Protocols,
					UseExistingSnapshot: false,
				},
			}

			// Mock functions
			validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
				return nil
			}
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				validateCreateBackupParams = _validateCreateBackupParams
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock storage calls
			store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, account.ID).Return(volume, nil)
			store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
			store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
			store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
				return j.JobAttributes != nil && j.JobAttributes.ResourceUUID == backup.UUID
			})).Return(job, nil)

			// Mock workflow execution failure
			temporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow execution failed"))

			// Mock error handling - add DeleteBackup and UpdateJob mocks
			store.On("DeleteBackup", ctx, backup.UUID).Return(backup, nil)
			store.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, "workflow execution failed").Return(nil, nil)

			// Act
			_, _, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "workflow execution failed")
		})
	})

	t.Run("ValidationSequence", func(t *testing.T) {
		t.Run("CreateBackup_FullValidationSequence", func(t *testing.T) {
			store := database.NewMockStorage(t)
			temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
			account, volume, backupVault := createCommonTestObjects()

			params := &common.CreateBackupParams{
				BackupName:          "test-backup",
				VolumeUUID:          volume.UUID,
				BackupVaultID:       backupVault.UUID,
				AccountName:         account.Name,
				UseExistingSnapshot: false,
				SnapshotID:          "",
				Description:         "Integration test backup",
				BackupType:          "MANUAL",
			}

			job := &datamodel.Job{
				BaseModel:    datamodel.BaseModel{UUID: "job-uuid"},
				WorkflowID:   "workflow-id",
				Type:         string(datamodel.JobTypeCreateBackup),
				State:        string(datamodel.JobsStateNEW),
				ResourceName: params.BackupName,
				AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			}

			backup := &datamodel.Backup{
				BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
				Name:          params.BackupName,
				VolumeUUID:    params.VolumeUUID,
				BackupVaultID: backupVault.ID,
				BackupVault:   backupVault, // Add the BackupVault relationship
				State:         datamodel.LifeCycleStateCreating,
				StateDetails:  datamodel.LifeCycleStateCreatingDetails,
				Attributes: &datamodel.BackupAttributes{
					VolumeName:          volume.Name,
					AccountIdentifier:   account.Name,
					Protocols:           volume.VolumeAttributes.Protocols,
					UseExistingSnapshot: false,
				},
				Description: params.Description,
				Type:        params.BackupType,
			}

			// Use real validation function to test the complete validation sequence
			getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() {
				getOrCreateAccount = _getOrCreateAccount
			}()

			// Mock all the validation calls in sequence
			store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
			store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)
			store.On("GetSnapshotByNameAndVolumeId", ctx, params.BackupName, volume.AccountID, volume.ID).Return(nil, vsaerror.NewNotFoundErr("snapshot", nil))

			// Mock the creation calls
			store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, account.ID).Return(volume, nil)
			store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
			store.On("CreateBackup", ctx, mock.MatchedBy(func(b *datamodel.Backup) bool {
				return b.Name == params.BackupName &&
					b.VolumeUUID == params.VolumeUUID &&
					b.State == datamodel.LifeCycleStateCreating
			})).Return(backup, nil)
			store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
				return j.Type == string(datamodel.JobTypeCreateBackup) &&
					j.State == string(datamodel.JobsStateNEW) &&
					j.ResourceName == params.BackupName &&
					j.JobAttributes != nil &&
					j.JobAttributes.ResourceUUID == backup.UUID
			})).Return(job, nil)

			// Mock successful workflow execution
			temporal.EXPECT().ExecuteWorkflow(
				ctx,
				mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
					return opts.TaskQueue != "" && opts.ID == job.WorkflowID
				}),
				mock.Anything, // workflow function
				params,        // workflow parameters
				backup,
				backupVault,
				volume,
			).Return(nil, nil)

			// Act
			result, jobID, err := _createBackup(ctx, store, temporal, params)

			// Assert
			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, job.UUID, jobID)
			assert.Equal(t, backup.Name, result.Name)
			assert.Equal(t, backup.State, result.LifeCycleState)
			assert.Equal(t, backup.Description, *result.Description)
			assert.Equal(t, backup.Type, result.Type)

			// Verify all mocks were called as expected
			store.AssertExpectations(t)
			temporal.AssertExpectations(t)
		})
	})
}

// TestDeleteBackup_DeleteBackupFailure tests error handling when DeleteBackup fails for error state backup
func TestDeleteBackup_DeleteBackupFailure(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)
	temporal := new(workflow_engine_mock.MockTemporalTestClient)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "testAccountUUID"}, Name: "test-account"}
	backup := &datamodel.Backup{
		BaseModel: datamodel.BaseModel{UUID: "test-backup-uuid"},
		State:     datamodel.LifeCycleStateError,
		Attributes: &datamodel.BackupAttributes{
			DeleteInitiated: false,
		},
	}

	params := &common.DeleteBackupParams{
		BackupUUID:      backup.UUID,
		BackupVaultUUID: "test-vault-uuid",
		AccountName:     "test-account",
	}

	// Mock the validation function
	validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
		return nil
	}
	defer func() { validateBackupDeleteParams = _validateBackupDeleteParams }()

	// Mock account lookup
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = _getOrCreateAccount }()

	// Mock backup retrieval
	store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)

	// Mock DeleteBackup to fail
	expectedError := errors.New("failed to delete backup")
	store.On("DeleteBackup", ctx, backup.UUID).Return(nil, expectedError)

	// Act
	result, jobID, err := deleteBackup(ctx, store, temporal, params)

	// Assert
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	assert.Nil(t, result)
	assert.Equal(t, "", jobID)
	store.AssertExpectations(t)
}

// TestDeleteBackup_ListVolumesFailure tests error handling when ListVolumes fails
func TestDeleteBackup_ListVolumesFailure(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)
	temporal := new(workflow_engine_mock.MockTemporalTestClient)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "testAccountUUID"}, Name: "test-account"}
	backup := &datamodel.Backup{
		BaseModel: datamodel.BaseModel{UUID: "test-backup-uuid"},
		State:     datamodel.LifeCycleStateAvailable,
		Attributes: &datamodel.BackupAttributes{
			DeleteInitiated: false,
		},
	}

	params := &common.DeleteBackupParams{
		BackupUUID:      backup.UUID,
		BackupVaultUUID: "test-vault-uuid",
		AccountName:     "test-account",
	}

	// Mock the validation function
	validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
		return nil
	}
	defer func() { validateBackupDeleteParams = _validateBackupDeleteParams }()

	// Mock account lookup
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = _getOrCreateAccount }()

	// Mock backup retrieval
	store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)

	// Mock ListVolumes to fail
	expectedError := errors.New("failed to list volumes")
	store.On("ListVolumes", ctx, mock.AnythingOfType("[][]interface {}")).Return(nil, expectedError)

	// Act
	result, jobID, err := deleteBackup(ctx, store, temporal, params)

	// Assert
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	assert.Nil(t, result)
	assert.Equal(t, "", jobID)
	store.AssertExpectations(t)
}

func TestConvertDatastoreBackupToModel(t *testing.T) {
	t.Run("WhenBackupVaultIsNil", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name:         "test-backup",
			VolumeUUID:   "test-volume-uuid",
			State:        "AVAILABLE",
			StateDetails: "Backup is available",
			Description:  "Test backup description",
			Type:         "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "test-volume",
				Protocols:  []string{"NFS", "SMB"},
			},
		}

		result := convertDatastoreBackupToModel(backup)

		assert.Equal(t, "test-backup-uuid", result.BackupID)
		assert.Equal(t, "test-backup", result.Name)
		assert.Equal(t, "test-volume-uuid", result.VolumeID)
		assert.Equal(t, "AVAILABLE", result.LifeCycleState)
		assert.Equal(t, "Backup is available", result.LifeCycleStateDetails)
		assert.Equal(t, "Test backup description", *result.Description)
		assert.Equal(t, "MANUAL", result.Type)
		assert.Equal(t, "test-volume", result.VolumeName)
		assert.Equal(t, []string{"NFS", "SMB"}, result.Protocols)
		assert.Equal(t, int64(0), *result.MinimumEnforcedRetentionDuration)
		assert.False(t, result.IsBackupImmutable)
	})

	t.Run("WhenBackupVaultImmutableAttributesIsNil", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name:         "test-backup",
			VolumeUUID:   "test-volume-uuid",
			State:        "AVAILABLE",
			StateDetails: "Backup is available",
			Description:  "Test backup description",
			Type:         "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "test-volume",
				Protocols:  []string{"NFS"},
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "test-vault-uuid",
				},
				SourceRegionName: stringPtr("us-east1"),
			},
		}

		result := convertDatastoreBackupToModel(backup)

		assert.Equal(t, "test-backup-uuid", result.BackupID)
		assert.Equal(t, "test-backup", result.Name)
		assert.Equal(t, "test-volume-uuid", result.VolumeID)
		assert.Equal(t, "AVAILABLE", result.LifeCycleState)
		assert.Equal(t, "Backup is available", result.LifeCycleStateDetails)
		assert.Equal(t, "Test backup description", *result.Description)
		assert.Equal(t, "MANUAL", result.Type)
		assert.Equal(t, "test-volume", result.VolumeName)
		assert.Equal(t, []string{"NFS"}, result.Protocols)
		assert.Equal(t, "test-vault-uuid", result.BackupVaultID)
		assert.Equal(t, "us-east1", result.Region)
		assert.Equal(t, int64(0), *result.MinimumEnforcedRetentionDuration)
		assert.False(t, result.IsBackupImmutable)
	})

	t.Run("WhenBackupVaultHasImmutableAttributes", func(t *testing.T) {
		minRetentionDuration := int64(30)
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name:         "test-backup",
			VolumeUUID:   "test-volume-uuid",
			State:        "AVAILABLE",
			StateDetails: "Backup is available",
			Description:  "Test backup description",
			Type:         "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "test-volume",
				Protocols:  []string{"NFS", "SMB"},
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "test-vault-uuid",
				},
				SourceRegionName: stringPtr("us-east1"),
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &minRetentionDuration,
					IsAdhocBackupImmutable:                 true,
				},
			},
		}

		result := convertDatastoreBackupToModel(backup)

		assert.Equal(t, "test-backup-uuid", result.BackupID)
		assert.Equal(t, "test-backup", result.Name)
		assert.Equal(t, "test-volume-uuid", result.VolumeID)
		assert.Equal(t, "AVAILABLE", result.LifeCycleState)
		assert.Equal(t, "Backup is available", result.LifeCycleStateDetails)
		assert.Equal(t, "Test backup description", *result.Description)
		assert.Equal(t, "MANUAL", result.Type)
		assert.Equal(t, "test-volume", result.VolumeName)
		assert.Equal(t, []string{"NFS", "SMB"}, result.Protocols)
		assert.Equal(t, "test-vault-uuid", result.BackupVaultID)
		assert.Equal(t, "us-east1", result.Region)
		assert.Equal(t, int64(30), *result.MinimumEnforcedRetentionDuration)
		assert.True(t, result.IsBackupImmutable)
	})

	t.Run("WhenBackupVaultHasImmutableAttributesButBackupIsNotImmutable", func(t *testing.T) {
		minRetentionDuration := int64(30)
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name:         "test-backup",
			VolumeUUID:   "test-volume-uuid",
			State:        "AVAILABLE",
			StateDetails: "Backup is available",
			Description:  "Test backup description",
			Type:         "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "test-volume",
				Protocols:  []string{"NFS"},
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "test-vault-uuid",
				},
				SourceRegionName: stringPtr("us-east1"),
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &minRetentionDuration,
					IsAdhocBackupImmutable:                 false,
				},
			},
		}

		result := convertDatastoreBackupToModel(backup)

		assert.Equal(t, "test-backup-uuid", result.BackupID)
		assert.Equal(t, "test-backup", result.Name)
		assert.Equal(t, "test-volume-uuid", result.VolumeID)
		assert.Equal(t, "AVAILABLE", result.LifeCycleState)
		assert.Equal(t, "Backup is available", result.LifeCycleStateDetails)
		assert.Equal(t, "Test backup description", *result.Description)
		assert.Equal(t, "MANUAL", result.Type)
		assert.Equal(t, "test-volume", result.VolumeName)
		assert.Equal(t, []string{"NFS"}, result.Protocols)
		assert.Equal(t, "test-vault-uuid", result.BackupVaultID)
		assert.Equal(t, "us-east1", result.Region)
		assert.Equal(t, int64(30), *result.MinimumEnforcedRetentionDuration)
		assert.False(t, result.IsBackupImmutable)
	})

	t.Run("WhenBackupVaultHasImmutableAttributesWithZeroRetention", func(t *testing.T) {
		minRetentionDuration := int64(0)
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name:         "test-backup",
			VolumeUUID:   "test-volume-uuid",
			State:        "AVAILABLE",
			StateDetails: "Backup is available",
			Description:  "Test backup description",
			Type:         "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "test-volume",
				Protocols:  []string{"NFS"},
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "test-vault-uuid",
				},
				SourceRegionName: stringPtr("us-east1"),
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &minRetentionDuration,
					IsAdhocBackupImmutable:                 true,
				},
			},
		}

		result := convertDatastoreBackupToModel(backup)

		assert.Equal(t, "test-backup-uuid", result.BackupID)
		assert.Equal(t, "test-backup", result.Name)
		assert.Equal(t, "test-volume-uuid", result.VolumeID)
		assert.Equal(t, "AVAILABLE", result.LifeCycleState)
		assert.Equal(t, "Backup is available", result.LifeCycleStateDetails)
		assert.Equal(t, "Test backup description", *result.Description)
		assert.Equal(t, "MANUAL", result.Type)
		assert.Equal(t, "test-volume", result.VolumeName)
		assert.Equal(t, []string{"NFS"}, result.Protocols)
		assert.Equal(t, "test-vault-uuid", result.BackupVaultID)
		assert.Equal(t, "us-east1", result.Region)
		assert.Equal(t, int64(0), *result.MinimumEnforcedRetentionDuration)
		assert.True(t, result.IsBackupImmutable)
	})

	t.Run("WhenProtocolsIsNil", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name:         "test-backup",
			VolumeUUID:   "test-volume-uuid",
			State:        "AVAILABLE",
			StateDetails: "Backup is available",
			Description:  "Test backup description",
			Type:         "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				VolumeName: "test-volume",
				Protocols:  nil,
			},
		}

		result := convertDatastoreBackupToModel(backup)

		assert.Equal(t, "test-backup-uuid", result.BackupID)
		assert.Equal(t, "test-backup", result.Name)
		assert.Equal(t, "test-volume-uuid", result.VolumeID)
		assert.Equal(t, "AVAILABLE", result.LifeCycleState)
		assert.Equal(t, "Backup is available", result.LifeCycleStateDetails)
		assert.Equal(t, "Test backup description", *result.Description)
		assert.Equal(t, "MANUAL", result.Type)
		assert.Equal(t, "test-volume", result.VolumeName)
		assert.Equal(t, []string{}, result.Protocols)
		assert.Equal(t, int64(0), *result.MinimumEnforcedRetentionDuration)
		assert.False(t, result.IsBackupImmutable)
	})
}

func TestDeleteBackupInternal(t *testing.T) {
	t.Run("WhenBackupDeletedSuccessfully_ReturnsNoError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(t)

		// Override hydrationEnabled to false
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = false
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "test-vault-uuid",
			BackupUUID:      "test-backup-uuid",
			AccountName:     "test-account",
		}

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name: "test-backup",
			Attributes: &datamodel.BackupAttributes{
				RestoreVolumeCount: 0,
			},
		}

		// Mock storage calls
		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		store.On("DeleteBackup", ctx, backup.UUID).Return(&datamodel.Backup{}, nil)

		o := &GCPOrchestrator{storage: store}
		result, err := o.DeleteBackupInternal(ctx, params)

		assert.NoError(t, err)
		assert.Equal(t, "", result)
		store.AssertExpectations(t)
	})

	t.Run("WhenGetBackupFails_ReturnsError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(t)

		// Override hydrationEnabled to false
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = false
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "test-vault-uuid",
			BackupUUID:      "test-backup-uuid",
			AccountName:     "test-account",
		}

		expectedError := errors.New("backup not found")
		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(nil, expectedError)

		o := &GCPOrchestrator{storage: store}
		result, err := o.DeleteBackupInternal(ctx, params)

		assert.Error(t, err)
		assert.Equal(t, expectedError, err)
		assert.Equal(t, "", result)
		store.AssertExpectations(t)
	})

	t.Run("WhenDeleteBackupFails_ReturnsError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(t)

		// Override hydrationEnabled to false
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = false
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "test-vault-uuid",
			BackupUUID:      "test-backup-uuid",
			AccountName:     "test-account",
		}

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name: "test-backup",
			Attributes: &datamodel.BackupAttributes{
				RestoreVolumeCount: 0,
			},
		}

		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		expectedError := errors.New("failed to delete backup")
		store.On("DeleteBackup", ctx, backup.UUID).Return(nil, expectedError)

		o := &GCPOrchestrator{storage: store}
		result, err := o.DeleteBackupInternal(ctx, params)

		assert.Error(t, err)
		assert.Equal(t, expectedError, err)
		assert.Equal(t, "", result)
		store.AssertExpectations(t)
	})

	t.Run("WhenBackupNotFoundError_ReturnsError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(t)

		// Override hydrationEnabled to false
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = false
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "test-vault-uuid",
			BackupUUID:      "test-backup-uuid",
			AccountName:     "test-account",
		}

		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).
			Return(nil, vsaerror.NewNotFoundErr("backup", nil))

		o := &GCPOrchestrator{storage: store}
		result, err := o.DeleteBackupInternal(ctx, params)

		assert.NoError(t, err)
		assert.Equal(t, "", result)
		store.AssertExpectations(t)
	})

	t.Run("WhenBackupHasRestoreVolumeCountGreaterThanZero_ReturnsUserInputValidationError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(t)

		// Override hydrationEnabled to false
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = false
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "test-vault-uuid",
			BackupUUID:      "test-backup-uuid",
			AccountName:     "test-account",
		}

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name: "test-backup",
			Attributes: &datamodel.BackupAttributes{
				RestoreVolumeCount: 2,
			},
		}

		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)

		o := &GCPOrchestrator{storage: store}
		result, err := o.DeleteBackupInternal(ctx, params)

		assert.Error(t, err)
		assert.True(t, vsaerror.IsUserInputValidationErr(err))
		assert.Contains(t, err.Error(), "Cannot delete the backup as it is being used to restore a volume")
		assert.Equal(t, "", result)
		store.AssertExpectations(t)
	})

	t.Run("WhenHydrationEnabledAndBackupVaultIsNil_ReturnsError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(t)

		// Override hydrationEnabled to true
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "test-vault-uuid",
			BackupUUID:      "test-backup-uuid",
			AccountName:     "test-account",
		}

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name: "test-backup",
			Attributes: &datamodel.BackupAttributes{
				RestoreVolumeCount: 0,
			},
			BackupVault: nil, // This should trigger the error
		}

		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		store.On("DeleteBackup", ctx, backup.UUID).Return(&datamodel.Backup{}, nil)

		o := &GCPOrchestrator{storage: store}
		result, err := o.DeleteBackupInternal(ctx, params)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Could not find the backup vault associated with the backup")
		assert.Equal(t, "", result)
		store.AssertExpectations(t)
	})

	t.Run("WhenHydrationEnabledAndBackupVaultExistsButHydrationFails_ReturnsError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(t)

		// Override hydrationEnabled to true
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		// Override hydrateDeletedBackupsToCCFE to return an error
		originalHydrateDeletedBackupsToCCFE := hydrateDeletedBackupsToCCFE
		hydrateDeletedBackupsToCCFE = func(ctx context.Context, params *common.DeleteBackupParams, backup *datamodel.Backup) error {
			return errors.New("failed to hydrate deleted backup to CCFE")
		}
		defer func() { hydrateDeletedBackupsToCCFE = originalHydrateDeletedBackupsToCCFE }()

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "test-vault-uuid",
			BackupUUID:      "test-backup-uuid",
			AccountName:     "test-account",
		}

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name: "test-backup",
			Attributes: &datamodel.BackupAttributes{
				RestoreVolumeCount: 0,
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "test-vault-uuid",
				},
				Name: "test-vault",
			},
		}

		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		store.On("DeleteBackup", ctx, backup.UUID).Return(&datamodel.Backup{}, nil)

		o := &GCPOrchestrator{storage: store}
		result, err := o.DeleteBackupInternal(ctx, params)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to hydrate deleted backup to CCFE")
		assert.Equal(t, "", result)
		store.AssertExpectations(t)
	})

	t.Run("WhenHydrationEnabledAndBackupVaultExistsAndHydrationSucceeds_ReturnsNoError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(t)

		// Override hydrationEnabled to true
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		// Override hydrateDeletedBackupsToCCFE to return success
		originalHydrateDeletedBackupsToCCFE := hydrateDeletedBackupsToCCFE
		hydrateDeletedBackupsToCCFE = func(ctx context.Context, params *common.DeleteBackupParams, backup *datamodel.Backup) error {
			return nil
		}
		defer func() { hydrateDeletedBackupsToCCFE = originalHydrateDeletedBackupsToCCFE }()

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "test-vault-uuid",
			BackupUUID:      "test-backup-uuid",
			AccountName:     "test-account",
		}

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name: "test-backup",
			Attributes: &datamodel.BackupAttributes{
				RestoreVolumeCount: 0,
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "test-vault-uuid",
				},
				Name: "test-vault",
			},
		}

		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		store.On("DeleteBackup", ctx, backup.UUID).Return(&datamodel.Backup{}, nil)

		o := &GCPOrchestrator{storage: store}
		result, err := o.DeleteBackupInternal(ctx, params)

		assert.NoError(t, err)
		assert.Equal(t, "", result)
		store.AssertExpectations(t)
	})

	t.Run("WhenHydrationDisabled_SkipsHydration", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(t)

		// Override hydrationEnabled to false
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = false
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "test-vault-uuid",
			BackupUUID:      "test-backup-uuid",
			AccountName:     "test-account",
		}

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "test-backup-uuid",
			},
			Name: "test-backup",
			Attributes: &datamodel.BackupAttributes{
				RestoreVolumeCount: 0,
			},
			BackupVault: nil, // Even with nil BackupVault, should not error when hydration is disabled
		}

		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		store.On("DeleteBackup", ctx, backup.UUID).Return(&datamodel.Backup{}, nil)

		o := &GCPOrchestrator{storage: store}
		result, err := o.DeleteBackupInternal(ctx, params)

		assert.NoError(t, err)
		assert.Equal(t, "", result)
		store.AssertExpectations(t)
	})
}

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}

func Test_fetchRemoteBackupFromVCP(t *testing.T) {
	// Common test data
	backupUUID := "test-backup-uuid"
	backupVaultUUID := "test-backup-vault-uuid"
	projectNumber := "123456789"
	region := "us-central1"
	basePath := "https://example.com"
	jwtToken := "test-jwt-token"

	t.Run("Success", func(t *testing.T) {
		// Arrange
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		// Mock getRemoteRegionConfig
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(region, projectNumber string) (string, string, error) {
			return basePath, jwtToken, nil
		}

		// Mock googleproxyclient.GetGProxyClient
		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mockClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockClient
		}

		// Mock V1betaInternalDescribeBackup
		expectedBackup := googleproxyclient.InternalBackupV1beta{
			ResourceId:  googleproxyclient.NewOptString(backupUUID),
			IsRestoring: googleproxyclient.NewOptBool(false),
		}
		mockResponse := &googleproxyclient.V1betaInternalDescribeBackupOK{
			Backups: []googleproxyclient.InternalBackupV1beta{expectedBackup},
		}
		mockInvoker.On("V1betaInternalDescribeBackup", mock.Anything, mock.Anything).Return(mockResponse, nil)

		// Act
		result, err := _fetchRemoteBackupFromVCP(ctx, backupUUID, backupVaultUUID, projectNumber, region)

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, expectedBackup.ResourceId, result.ResourceId)
		assert.Equal(t, expectedBackup.IsRestoring, result.IsRestoring)
		mockInvoker.AssertExpectations(t)
	})

	t.Run("GetRemoteRegionConfigError", func(t *testing.T) {
		// Arrange
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())
		expectedError := errors.New("failed to get region config")

		// Mock getRemoteRegionConfig to return error
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(region, projectNumber string) (string, string, error) {
			return "", "", expectedError
		}

		// Act
		result, err := _fetchRemoteBackupFromVCP(ctx, backupUUID, backupVaultUUID, projectNumber, region)

		// Assert
		assert.Error(t, err)
		assert.Equal(t, expectedError, err)
		assert.Equal(t, googleproxyclient.InternalBackupV1beta{}, result)
	})

	t.Run("V1betaInternalDescribeBackupError", func(t *testing.T) {
		// Arrange
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		// Mock getRemoteRegionConfig
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(region, projectNumber string) (string, string, error) {
			return basePath, jwtToken, nil
		}

		// Mock googleproxyclient.GetGProxyClient
		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mockClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockClient
		}

		// Mock V1betaInternalDescribeBackup to return error
		expectedError := errors.New("failed to fetch backup")
		mockInvoker.On("V1betaInternalDescribeBackup", mock.Anything, mock.Anything).Return(nil, expectedError)

		// Act
		result, err := _fetchRemoteBackupFromVCP(ctx, backupUUID, backupVaultUUID, projectNumber, region)

		// Assert
		assert.Error(t, err)
		assert.False(t, vsaerror.IsNotFoundErr(err))
		assert.Equal(t, expectedError, err)
		assert.Equal(t, googleproxyclient.InternalBackupV1beta{}, result)
		mockInvoker.AssertExpectations(t)
	})

	t.Run("HTTPNotFoundResponse", func(t *testing.T) {
		// Arrange
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(region, projectNumber string) (string, string, error) {
			return basePath, jwtToken, nil
		}

		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mockClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockClient
		}

		mockInvoker.On("V1betaInternalDescribeBackup", mock.Anything, mock.Anything).Return(
			&googleproxyclient.V1betaInternalDescribeBackupNotFound{Message: "backup not found"}, nil)

		result, err := _fetchRemoteBackupFromVCP(ctx, backupUUID, backupVaultUUID, projectNumber, region)

		assert.Error(t, err)
		assert.True(t, vsaerror.IsNotFoundErr(err))
		assert.Contains(t, err.Error(), "remote backup")
		assert.Equal(t, googleproxyclient.InternalBackupV1beta{}, result)
		mockInvoker.AssertExpectations(t)
	})

	t.Run("UnexpectedResponseType", func(t *testing.T) {
		// Arrange
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		// Mock getRemoteRegionConfig
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(region, projectNumber string) (string, string, error) {
			return basePath, jwtToken, nil
		}

		// Mock googleproxyclient.GetGProxyClient
		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mockClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockClient
		}

		// Mock V1betaInternalDescribeBackup to return a non-OK, non-NotFound API error body
		unexpectedResponse := &googleproxyclient.V1betaInternalDescribeBackupBadRequest{Message: "invalid request"}
		mockInvoker.On("V1betaInternalDescribeBackup", mock.Anything, mock.Anything).Return(unexpectedResponse, nil)

		// Act
		result, err := _fetchRemoteBackupFromVCP(ctx, backupUUID, backupVaultUUID, projectNumber, region)

		// Assert
		assert.Error(t, err)
		assert.False(t, vsaerror.IsNotFoundErr(err))
		assert.Contains(t, err.Error(), "remote describe backup failed")
		assert.Equal(t, googleproxyclient.InternalBackupV1beta{}, result)
		mockInvoker.AssertExpectations(t)
	})

	t.Run("EmptyBackupsArray", func(t *testing.T) {
		// Arrange
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		// Mock getRemoteRegionConfig
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(region, projectNumber string) (string, string, error) {
			return basePath, jwtToken, nil
		}

		// Mock googleproxyclient.GetGProxyClient
		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mockClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockClient
		}

		// Mock V1betaInternalDescribeBackup to return empty backups array
		mockResponse := &googleproxyclient.V1betaInternalDescribeBackupOK{
			Backups: []googleproxyclient.InternalBackupV1beta{},
		}
		mockInvoker.On("V1betaInternalDescribeBackup", mock.Anything, mock.Anything).Return(mockResponse, nil)

		// Act
		result, err := _fetchRemoteBackupFromVCP(ctx, backupUUID, backupVaultUUID, projectNumber, region)

		// Assert
		assert.Error(t, err)
		assert.True(t, vsaerror.IsNotFoundErr(err))
		assert.Contains(t, err.Error(), "remote backup")
		assert.Equal(t, googleproxyclient.InternalBackupV1beta{}, result)
		mockInvoker.AssertExpectations(t)
	})

	t.Run("MultipleBackups_ReturnsFirst", func(t *testing.T) {
		// Arrange
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		// Mock getRemoteRegionConfig
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(region, projectNumber string) (string, string, error) {
			return basePath, jwtToken, nil
		}

		// Mock googleproxyclient.GetGProxyClient
		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mockClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockClient
		}

		// Mock V1betaInternalDescribeBackup with multiple backups
		firstBackup := googleproxyclient.InternalBackupV1beta{
			ResourceId:  googleproxyclient.NewOptString("first-backup-uuid"),
			IsRestoring: googleproxyclient.NewOptBool(false),
		}
		secondBackup := googleproxyclient.InternalBackupV1beta{
			ResourceId:  googleproxyclient.NewOptString("second-backup-uuid"),
			IsRestoring: googleproxyclient.NewOptBool(true),
		}
		mockResponse := &googleproxyclient.V1betaInternalDescribeBackupOK{
			Backups: []googleproxyclient.InternalBackupV1beta{firstBackup, secondBackup},
		}
		mockInvoker.On("V1betaInternalDescribeBackup", mock.Anything, mock.Anything).Return(mockResponse, nil)

		// Act
		result, err := _fetchRemoteBackupFromVCP(ctx, backupUUID, backupVaultUUID, projectNumber, region)

		// Assert
		assert.NoError(t, err)
		// Should return the first backup
		assert.Equal(t, firstBackup.ResourceId, result.ResourceId)
		assert.Equal(t, firstBackup.IsRestoring, result.IsRestoring)
		mockInvoker.AssertExpectations(t)
	})

	t.Run("NoCorrelationIDInContext", func(t *testing.T) {
		// Arrange
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())
		// No correlation ID in context

		// Mock getRemoteRegionConfig
		originalGetRemoteRegionConfig := common.GetRemoteRegionConfig
		defer func() { common.GetRemoteRegionConfig = originalGetRemoteRegionConfig }()
		common.GetRemoteRegionConfig = func(region, projectNumber string) (string, string, error) {
			return basePath, jwtToken, nil
		}

		// Mock googleproxyclient.GetGProxyClient
		mockInvoker := googleproxyclient.NewMockInvoker(t)
		mockClient := &googleproxyclient.ProxyClient{
			Invoker: mockInvoker,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mockClient
		}

		// Mock V1betaInternalDescribeBackup
		expectedBackup := googleproxyclient.InternalBackupV1beta{
			ResourceId:  googleproxyclient.NewOptString(backupUUID),
			IsRestoring: googleproxyclient.NewOptBool(false),
		}
		mockResponse := &googleproxyclient.V1betaInternalDescribeBackupOK{
			Backups: []googleproxyclient.InternalBackupV1beta{expectedBackup},
		}
		// Should still work even without correlation ID
		mockInvoker.On("V1betaInternalDescribeBackup", mock.Anything, mock.Anything).Return(mockResponse, nil)

		// Act
		result, err := _fetchRemoteBackupFromVCP(ctx, backupUUID, backupVaultUUID, projectNumber, region)

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, expectedBackup.ResourceId, result.ResourceId)
		mockInvoker.AssertExpectations(t)
	})
}

func Test_hydrateDeletedBackupsToCCFE(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	t.Run("WhenSuccessful", func(tt *testing.T) {
		// Arrange
		params := &common.DeleteBackupParams{
			AccountName:     "test-account",
			Region:          "us-central1",
			BackupVaultUUID: "backup-vault-uuid",
			BackupUUID:      "backup-uuid",
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			Name: "test-backup",
			BackupVault: &datamodel.BackupVault{
				Name: "test-backup-vault",
			},
		}

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}

		// Mock common.HydrateDeletedBackups
		originalHydrateDeletedBackups := common.HydrateDeletedBackups
		defer func() { common.HydrateDeletedBackups = originalHydrateDeletedBackups }()
		common.HydrateDeletedBackups = func(ctx context.Context, logger log.Logger, names []string, backupVaultName string, location string, projectId string, token string) error {
			assert.Equal(tt, "test-backup-vault", backupVaultName)
			assert.Equal(tt, "us-central1", location)
			assert.Equal(tt, "test-account", projectId)
			assert.Equal(tt, "mock-token", token)
			assert.NotEmpty(tt, names)
			return nil
		}

		// Act
		err := _hydrateDeletedBackupsToCCFE(ctx, params, backup)

		// Assert
		assert.NoError(tt, err)
	})

	t.Run("WhenTokenGenerationFails", func(tt *testing.T) {
		// Arrange
		params := &common.DeleteBackupParams{
			AccountName:     "test-account",
			Region:          "us-central1",
			BackupVaultUUID: "backup-vault-uuid",
			BackupUUID:      "backup-uuid",
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			Name: "test-backup",
			BackupVault: &datamodel.BackupVault{
				Name: "test-backup-vault",
			},
		}

		expectedError := errors.New("token generation failed")

		// Mock auth.GenerateCallbackToken to return error
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "", expectedError
		}

		// Act
		err := _hydrateDeletedBackupsToCCFE(ctx, params, backup)

		// Assert
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})

	t.Run("WhenHydrationFails", func(tt *testing.T) {
		// Arrange
		params := &common.DeleteBackupParams{
			AccountName:     "test-account",
			Region:          "us-central1",
			BackupVaultUUID: "backup-vault-uuid",
			BackupUUID:      "backup-uuid",
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			Name: "test-backup",
			BackupVault: &datamodel.BackupVault{
				Name: "test-backup-vault",
			},
		}

		expectedError := errors.New("hydration failed")

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}

		// Mock common.HydrateDeletedBackups to return error
		originalHydrateDeletedBackups := common.HydrateDeletedBackups
		defer func() { common.HydrateDeletedBackups = originalHydrateDeletedBackups }()
		common.HydrateDeletedBackups = func(ctx context.Context, logger log.Logger, names []string, backupVaultName string, location string, projectId string, token string) error {
			return expectedError
		}

		// Act
		err := _hydrateDeletedBackupsToCCFE(ctx, params, backup)

		// Assert
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})

	t.Run("WhenCrossProjectCrossRegionDestination_SkipsHydration", func(tt *testing.T) {
		destinationRegion := "us-east1"
		params := &common.DeleteBackupParams{
			AccountName:     "test-account",
			Region:          destinationRegion,
			BackupVaultUUID: "backup-vault-uuid",
			BackupUUID:      "backup-uuid",
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Name:      "test-backup",
			BackupVault: &datamodel.BackupVault{
				Name:             "crb-dest-vault",
				BackupRegionName: &destinationRegion,
				BackupVaultType:  CrossRegionBackupType,
				ServiceType:      datamodel.ServiceTypeCrossProject,
			},
		}

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			tt.Fatalf("GenerateCallbackToken should not be called when hydration is skipped")
			return "", nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		hydrationCalled := false
		originalHydrateDeletedBackups := common.HydrateDeletedBackups
		common.HydrateDeletedBackups = func(ctx context.Context, logger log.Logger, names []string, backupVaultName string, location string, projectId string, token string) error {
			hydrationCalled = true
			return nil
		}
		defer func() { common.HydrateDeletedBackups = originalHydrateDeletedBackups }()

		err := _hydrateDeletedBackupsToCCFE(ctx, params, backup)
		assert.NoError(tt, err)
		assert.False(tt, hydrationCalled, "hydration should be skipped for cross-project cross-region destination backup deletion")
	})

	t.Run("WhenCrossRegionButNotCrossProject_HydrationOccurs", func(tt *testing.T) {
		destinationRegion := "us-east1"
		params := &common.DeleteBackupParams{
			AccountName:     "test-account",
			Region:          destinationRegion,
			BackupVaultUUID: "backup-vault-uuid",
			BackupUUID:      "backup-uuid",
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Name:      "test-backup",
			BackupVault: &datamodel.BackupVault{
				Name:             "cross-region-vault",
				BackupRegionName: &destinationRegion,
				BackupVaultType:  CrossRegionBackupType,
				ServiceType:      datamodel.ServiceTypeGCNV,
			},
		}

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		hydrationCalled := false
		originalHydrateDeletedBackups := common.HydrateDeletedBackups
		common.HydrateDeletedBackups = func(ctx context.Context, logger log.Logger, names []string, backupVaultName string, location string, projectId string, token string) error {
			hydrationCalled = true
			return nil
		}
		defer func() { common.HydrateDeletedBackups = originalHydrateDeletedBackups }()

		err := _hydrateDeletedBackupsToCCFE(ctx, params, backup)
		assert.NoError(tt, err)
		assert.True(tt, hydrationCalled, "hydration should occur for non-cross-project cross-region backup deletion")
	})
}

func Test_hydrateCreatedBackupsToCCFE(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	t.Run("WhenSuccessful", func(tt *testing.T) {
		// Arrange
		params := &common.CreateBackupParams{
			AccountName:   "test-account",
			Region:        "us-central1",
			BackupVaultID: "backup-vault-uuid",
			BackupName:    "test-backup",
			BackupUUID:    "backup-uuid",
		}
		regionName := "us-central1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			Name: "test-backup",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "test-account",
				VolumeName:        "test-volume",
				SourceVolumeZone:  "",
			},
			BackupVault: &datamodel.BackupVault{
				Name:             "test-backup-vault",
				SourceRegionName: &regionName,
			},
		}

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}

		// Mock common.HydrateCreatedBackups
		originalHydrateCreatedBackups := common.HydrateCreatedBackups
		defer func() { common.HydrateCreatedBackups = originalHydrateCreatedBackups }()
		common.HydrateCreatedBackups = func(ctx context.Context, logger log.Logger, requests []models.Request, backupVaultName string, location string, projectId string, token string) error {
			assert.Equal(tt, "test-backup-vault", backupVaultName)
			assert.Equal(tt, "us-central1", location)
			assert.Equal(tt, "test-account", projectId)
			assert.Equal(tt, "mock-token", token)
			assert.NotEmpty(tt, requests)
			return nil
		}

		// Act
		err := _hydrateCreatedBackupsToCCFE(ctx, params, backup)

		// Assert
		assert.NoError(tt, err)
	})

	t.Run("WhenTokenGenerationFails", func(tt *testing.T) {
		// Arrange
		params := &common.CreateBackupParams{
			AccountName:   "test-account",
			Region:        "us-central1",
			BackupVaultID: "backup-vault-uuid",
			BackupName:    "test-backup",
			BackupUUID:    "backup-uuid",
		}
		regionName := "us-central1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			Name: "test-backup",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "test-account",
				VolumeName:        "test-volume",
				SourceVolumeZone:  "",
			},
			BackupVault: &datamodel.BackupVault{
				Name:             "test-backup-vault",
				SourceRegionName: &regionName,
			},
		}

		expectedError := errors.New("token generation failed")

		// Mock auth.GenerateCallbackToken to return error
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "", expectedError
		}

		// Act
		err := _hydrateCreatedBackupsToCCFE(ctx, params, backup)

		// Assert
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})

	t.Run("WhenHydrationFails", func(tt *testing.T) {
		// Arrange
		params := &common.CreateBackupParams{
			AccountName:   "test-account",
			Region:        "us-central1",
			BackupVaultID: "backup-vault-uuid",
			BackupName:    "test-backup",
			BackupUUID:    "backup-uuid",
		}
		regionName := "us-central1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: "backup-uuid",
			},
			Name: "test-backup",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "test-account",
				VolumeName:        "test-volume",
				SourceVolumeZone:  "",
			},
			BackupVault: &datamodel.BackupVault{
				Name:             "test-backup-vault",
				SourceRegionName: &regionName,
			},
		}

		expectedError := errors.New("hydration failed")

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "mock-token", nil
		}

		// Mock common.HydrateCreatedBackups to return error
		originalHydrateCreatedBackups := common.HydrateCreatedBackups
		defer func() { common.HydrateCreatedBackups = originalHydrateCreatedBackups }()
		common.HydrateCreatedBackups = func(ctx context.Context, logger log.Logger, requests []models.Request, backupVaultName string, location string, projectId string, token string) error {
			return expectedError
		}

		// Act
		err := _hydrateCreatedBackupsToCCFE(ctx, params, backup)

		// Assert
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
	})

	t.Run("WhenCrossProjectCrossRegionDestination_SkipsHydration", func(tt *testing.T) {
		destinationRegion := "us-east1"
		params := &common.CreateBackupParams{
			AccountName:   "test-account",
			Region:        destinationRegion,
			BackupVaultID: "backup-vault-uuid",
			BackupName:    "test-backup",
			BackupUUID:    "backup-uuid",
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Name:      "test-backup",
			BackupVault: &datamodel.BackupVault{
				Name:             "crb-dest-vault",
				BackupRegionName: &destinationRegion,
				BackupVaultType:  CrossRegionBackupType,
				ServiceType:      datamodel.ServiceTypeCrossProject,
			},
		}

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			tt.Fatalf("GenerateCallbackToken should not be called when hydration is skipped")
			return "", nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		hydrationCalled := false
		originalHydrateCreatedBackups := common.HydrateCreatedBackups
		common.HydrateCreatedBackups = func(ctx context.Context, logger log.Logger, requests []models.Request, backupVaultName string, location string, projectID string, token string) error {
			hydrationCalled = true
			return nil
		}
		defer func() { common.HydrateCreatedBackups = originalHydrateCreatedBackups }()

		err := _hydrateCreatedBackupsToCCFE(ctx, params, backup)
		assert.NoError(tt, err)
		assert.False(tt, hydrationCalled, "hydration should be skipped for cross-project cross-region destination backup")
	})

	t.Run("WhenCrossRegionButNotCrossProject_HydrationOccurs", func(tt *testing.T) {
		destinationRegion := "us-east1"
		sourceRegion := "us-central1"
		params := &common.CreateBackupParams{
			AccountName:   "test-account",
			Region:        destinationRegion,
			BackupVaultID: "backup-vault-uuid",
			BackupName:    "test-backup",
			BackupUUID:    "backup-uuid",
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Name:      "test-backup",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "test-account",
				VolumeName:        "test-volume",
				SourceVolumeZone:  "",
			},
			BackupVault: &datamodel.BackupVault{
				Name:             "cross-region-vault",
				BackupRegionName: &destinationRegion,
				SourceRegionName: &sourceRegion,
				BackupVaultType:  CrossRegionBackupType,
				ServiceType:      datamodel.ServiceTypeGCNV,
			},
		}

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		hydrationCalled := false
		originalHydrateCreatedBackups := common.HydrateCreatedBackups
		common.HydrateCreatedBackups = func(ctx context.Context, logger log.Logger, requests []models.Request, backupVaultName string, location string, projectID string, token string) error {
			hydrationCalled = true
			return nil
		}
		defer func() { common.HydrateCreatedBackups = originalHydrateCreatedBackups }()

		err := _hydrateCreatedBackupsToCCFE(ctx, params, backup)
		assert.NoError(tt, err)
		assert.True(tt, hydrationCalled, "hydration should occur for non-cross-project cross-region backup")
	})

	t.Run("WhenCrossProjectButNotCrossRegion_HydrationOccurs", func(tt *testing.T) {
		region := "us-central1"
		params := &common.CreateBackupParams{
			AccountName:   "test-account",
			Region:        region,
			BackupVaultID: "backup-vault-uuid",
			BackupName:    "test-backup",
			BackupUUID:    "backup-uuid",
		}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Name:      "test-backup",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier: "test-account",
				VolumeName:        "test-volume",
				SourceVolumeZone:  "",
			},
			BackupVault: &datamodel.BackupVault{
				Name:             "cross-project-vault",
				SourceRegionName: &region,
				BackupRegionName: &region,
				BackupVaultType:  "STANDARD",
				ServiceType:      datamodel.ServiceTypeCrossProject,
			},
		}

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		hydrationCalled := false
		originalHydrateCreatedBackups := common.HydrateCreatedBackups
		common.HydrateCreatedBackups = func(ctx context.Context, logger log.Logger, requests []models.Request, backupVaultName string, location string, projectID string, token string) error {
			hydrationCalled = true
			return nil
		}
		defer func() { common.HydrateCreatedBackups = originalHydrateCreatedBackups }()

		err := _hydrateCreatedBackupsToCCFE(ctx, params, backup)
		assert.NoError(tt, err)
		assert.True(tt, hydrationCalled, "hydration should occur for cross-project non-cross-region backup")
	})
}

func TestShouldSkipBackupDestinationHydration(t *testing.T) {
	destinationRegion := "us-east1"

	crbDestParams := &common.CreateBackupParams{Region: destinationRegion}
	crbDestBackup := &datamodel.Backup{
		BackupVault: &datamodel.BackupVault{
			BackupRegionName: &destinationRegion,
			BackupVaultType:  CrossRegionBackupType,
			ServiceType:      datamodel.ServiceTypeCrossProject,
		},
	}

	t.Run("WhenBackupIsNil_ReturnsFalse", func(tt *testing.T) {
		assert.False(tt, shouldSkipBackupDestinationHydration(nil, crbDestParams))
	})

	t.Run("WhenParamsIsNil_ReturnsFalse", func(tt *testing.T) {
		assert.False(tt, shouldSkipBackupDestinationHydration(crbDestBackup, nil))
	})

	t.Run("WhenBackupVaultIsNil_ReturnsFalse", func(tt *testing.T) {
		backup := &datamodel.Backup{}
		assert.False(tt, shouldSkipBackupDestinationHydration(backup, crbDestParams))
	})

	t.Run("WhenBackupRegionNameIsNil_ReturnsFalse", func(tt *testing.T) {
		backup := &datamodel.Backup{
			BackupVault: &datamodel.BackupVault{
				BackupVaultType: CrossRegionBackupType,
				ServiceType:     datamodel.ServiceTypeCrossProject,
			},
		}
		assert.False(tt, shouldSkipBackupDestinationHydration(backup, crbDestParams))
	})

	t.Run("WhenNotCrossProject_ReturnsFalse", func(tt *testing.T) {
		backup := &datamodel.Backup{
			BackupVault: &datamodel.BackupVault{
				BackupRegionName: &destinationRegion,
				BackupVaultType:  CrossRegionBackupType,
				ServiceType:      datamodel.ServiceTypeGCNV,
			},
		}
		assert.False(tt, shouldSkipBackupDestinationHydration(backup, crbDestParams))
	})

	t.Run("WhenNotCrossRegion_ReturnsFalse", func(tt *testing.T) {
		backup := &datamodel.Backup{
			BackupVault: &datamodel.BackupVault{
				BackupRegionName: &destinationRegion,
				BackupVaultType:  InRegionBackupType,
				ServiceType:      datamodel.ServiceTypeCrossProject,
			},
		}
		assert.False(tt, shouldSkipBackupDestinationHydration(backup, crbDestParams))
	})

	t.Run("WhenRequestRegionIsNotDestinationRegion_ReturnsFalse", func(tt *testing.T) {
		sourceParams := &common.CreateBackupParams{Region: "us-central1"}
		assert.False(tt, shouldSkipBackupDestinationHydration(crbDestBackup, sourceParams))
	})

	t.Run("WhenCrossProjectCrossRegionDestination_ReturnsTrue", func(tt *testing.T) {
		assert.True(tt, shouldSkipBackupDestinationHydration(crbDestBackup, crbDestParams))
	})
}

func TestShouldSkipBackupDeleteDestinationHydration(t *testing.T) {
	destinationRegion := "us-east1"

	crbDestParams := &common.DeleteBackupParams{Region: destinationRegion}
	crbDestBackup := &datamodel.Backup{
		BackupVault: &datamodel.BackupVault{
			BackupRegionName: &destinationRegion,
			BackupVaultType:  CrossRegionBackupType,
			ServiceType:      datamodel.ServiceTypeCrossProject,
		},
	}

	t.Run("WhenBackupIsNil_ReturnsFalse", func(tt *testing.T) {
		assert.False(tt, shouldSkipBackupDeleteDestinationHydration(nil, crbDestParams))
	})

	t.Run("WhenParamsIsNil_ReturnsFalse", func(tt *testing.T) {
		assert.False(tt, shouldSkipBackupDeleteDestinationHydration(crbDestBackup, nil))
	})

	t.Run("WhenBackupVaultIsNil_ReturnsFalse", func(tt *testing.T) {
		backup := &datamodel.Backup{}
		assert.False(tt, shouldSkipBackupDeleteDestinationHydration(backup, crbDestParams))
	})

	t.Run("WhenBackupRegionNameIsNil_ReturnsFalse", func(tt *testing.T) {
		backup := &datamodel.Backup{
			BackupVault: &datamodel.BackupVault{
				BackupVaultType: CrossRegionBackupType,
				ServiceType:     datamodel.ServiceTypeCrossProject,
			},
		}
		assert.False(tt, shouldSkipBackupDeleteDestinationHydration(backup, crbDestParams))
	})

	t.Run("WhenNotCrossProject_ReturnsFalse", func(tt *testing.T) {
		backup := &datamodel.Backup{
			BackupVault: &datamodel.BackupVault{
				BackupRegionName: &destinationRegion,
				BackupVaultType:  CrossRegionBackupType,
				ServiceType:      datamodel.ServiceTypeGCNV,
			},
		}
		assert.False(tt, shouldSkipBackupDeleteDestinationHydration(backup, crbDestParams))
	})

	t.Run("WhenNotCrossRegion_ReturnsFalse", func(tt *testing.T) {
		backup := &datamodel.Backup{
			BackupVault: &datamodel.BackupVault{
				BackupRegionName: &destinationRegion,
				BackupVaultType:  InRegionBackupType,
				ServiceType:      datamodel.ServiceTypeCrossProject,
			},
		}
		assert.False(tt, shouldSkipBackupDeleteDestinationHydration(backup, crbDestParams))
	})

	t.Run("WhenRequestRegionIsNotDestinationRegion_ReturnsFalse", func(tt *testing.T) {
		sourceParams := &common.DeleteBackupParams{Region: "us-central1"}
		assert.False(tt, shouldSkipBackupDeleteDestinationHydration(crbDestBackup, sourceParams))
	})

	t.Run("WhenCrossProjectCrossRegionDestination_ReturnsTrue", func(tt *testing.T) {
		assert.True(tt, shouldSkipBackupDeleteDestinationHydration(crbDestBackup, crbDestParams))
	})
}

func TestOrchestrator_CreateBackupInternal(t *testing.T) {
	t.Run("CallsCreateBackupInternal", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{}

		createBackupInternal = func(ctx context.Context, se database.Storage, temporalClient client.Client, params *common.CreateBackupParams) (*models.Backup, string, error) {
			return &models.Backup{}, "", nil
		}

		o := &GCPOrchestrator{storage: store, temporal: temporal}
		backup, jobID, err := o.CreateBackupInternal(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, backup)
		assert.Equal(tt, "", jobID)
	})
}

func TestOrchestrator_UpdateBackupInternal(t *testing.T) {
	t.Run("CallsUpdateBackupInternal", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.UpdateBackupParams{}

		updateBackupInternal = func(ctx context.Context, se database.Storage, temporalClient client.Client, params *common.UpdateBackupParams) (*models.Backup, string, error) {
			return &models.Backup{}, "", nil
		}

		o := &GCPOrchestrator{storage: store, temporal: temporal}
		backup, jobID, err := o.UpdateBackupInternal(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, backup)
		assert.Equal(tt, "", jobID)
	})
}

func TestOrchestrator_GetBackupByExternalUUID(t *testing.T) {
	t.Run("CallsStorageGetBackupByExternalUUID", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		backupVaultUUID := "backup-vault-uuid"
		externalUUID := "external-uuid"
		accountName := "test-account"
		expectedBackup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Name:      "test-backup",
		}

		store.On("GetBackupByExternalUUID", ctx, backupVaultUUID, externalUUID, accountName).Return(expectedBackup, nil)

		o := &GCPOrchestrator{storage: store}
		backup, err := o.GetBackupByExternalUUID(ctx, backupVaultUUID, externalUUID, accountName)

		assert.NoError(tt, err)
		assert.Equal(tt, expectedBackup, backup)
		store.AssertExpectations(tt)
	})
}

func Test_createBackup_WithOptionalAttributes(t *testing.T) {
	t.Run("WithAllOptionalAttributes", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "testAccountUUID"}, Name: "testAccount"}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "testVolumeUUID"},
			Name:      "testVolume",
			Account:   account,
			AccountID: 1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFS"},
			},
			State: datamodel.LifeCycleStateREADY,
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "testVaultID",
			},
		}
		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "testVaultID"},
			AccountID:        1,
			SourceRegionName: func() *string { s := "us-east1"; return &s }(),
		}
		job := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID:   "workflow-id",
			Type:         string(datamodel.JobTypeCreateBackup),
			State:        string(datamodel.JobsStateNEW),
			ResourceName: "testBackup",
			AccountID:    sql.NullInt64{Int64: 1, Valid: true},
		}
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         "testBackup",
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: "testVolume", Protocols: []string{"NFS"}},
		}

		params := &common.CreateBackupParams{
			BackupName:               "testBackup",
			VolumeUUID:               "testVolumeUUID",
			BackupVaultID:            "testVaultID",
			AccountName:              "testAccount",
			Description:              "test description",
			BackupType:               "FULL",
			BucketName:               "test-bucket",
			EndpointUUID:             "endpoint-uuid",
			CompletionTime:           "2024-01-01T00:00:00Z",
			BackupPolicyName:         "test-policy",
			OntapVolumeStyle:         "flexvol",
			SourceVolumeZone:         "us-east1-a",
			ServiceAccountName:       "test-sa",
			SnapshotCreationTime:     "2024-01-01T00:00:00Z",
			ConstituentCountOfBackup: 2,
		}

		originalValidateCreateBackupParams := validateCreateBackupParams
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return nil
		}
		defer func() { validateCreateBackupParams = originalValidateCreateBackupParams }()

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		store.On("GetVolumeWithAccountID", ctx, params.VolumeUUID, int64(1)).Return(volume, nil)
		store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
			return j.JobAttributes != nil && j.JobAttributes.ResourceUUID == backup.UUID
		})).Return(job, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		result, jobID, err := _createBackup(ctx, store, temporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "job-uuid", jobID)
		store.AssertExpectations(tt)
	})
}

func Test_createBackupInternal(t *testing.T) {
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "testAccountUUID"}, Name: "testAccount"}
	backupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "testVaultID"},
		AccountID:        1,
		SourceRegionName: func() *string { s := "us-east1"; return &s }(),
	}

	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	t.Run("SuccessWithAllAttributes", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{
			BackupName:               "testBackup",
			BackupUUID:               "backup-uuid",
			VolumeUUID:               "testVolumeUUID",
			BackupVaultID:            "testVaultID",
			AccountName:              "testAccount",
			Description:              "test description",
			BackupType:               "FULL",
			VolumeName:               "testVolume",
			Protocols:                []string{"NFS"},
			BucketName:               "test-bucket",
			EndpointUUID:             "endpoint-uuid",
			CompletionTime:           "2024-01-01T00:00:00Z",
			BackupPolicyName:         "test-policy",
			OntapVolumeStyle:         "flexvol",
			SourceVolumeZone:         "us-east1-a",
			ServiceAccountName:       "test-sa",
			SnapshotCreationTime:     "2024-01-01T00:00:00Z",
			ConstituentCountOfBackup: 2,
			UseExistingSnapshot:      false,
			Region:                   "us-central1",
		}

		regionName := "us-central1"
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "test-vault-uuid"},
				Name:             "test-vault",
				SourceRegionName: &regionName,
			},
			Attributes: &datamodel.BackupAttributes{
				VolumeName:        params.VolumeName,
				AccountIdentifier: account.Name,
				Protocols:         params.Protocols,
			},
		}

		// Mock hydrateCreatedBackupsToCCFE to avoid calling real auth function
		originalHydrateCreatedBackupsToCCFE := hydrateCreatedBackupsToCCFE
		hydrateCreatedBackupsToCCFE = func(ctx context.Context, params *common.CreateBackupParams, backup *datamodel.Backup) error {
			return nil
		}
		defer func() { hydrateCreatedBackupsToCCFE = originalHydrateCreatedBackupsToCCFE }()

		store.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, params.BackupVaultID, int64(1)).Return(backupVault, nil)
		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultID, params.BackupUUID, params.AccountName).Return(nil, vsaerror.NewNotFoundErr("backup", &params.BackupUUID))
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("FinishBackup", ctx, backup).Return(backup, nil)

		result, jobID, err := _createBackupInternal(ctx, store, temporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", jobID)
		store.AssertExpectations(tt)
	})

	t.Run("FailsWhenAccountCreationFails", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("failed to get account")
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		_, _, err := _createBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get account")
	})

	t.Run("FailsWhenVolumeInfoMissing", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
			VolumeName:    "", // Missing volume name
			Protocols:     []string{},
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		_, _, err := _createBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Volume information")
	})

	t.Run("FailsWhenBackupVaultNotFound", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			BackupUUID:    "backup-uuid",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
			VolumeName:    "testVolume",
			Protocols:     []string{"NFS"},
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		// When backup vault is not found, the function returns early, so GetBackupByExternalUUID is never called
		store.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, params.BackupVaultID, int64(1)).Return(nil, vsaerror.NewNotFoundErr("backup vault", &params.BackupVaultID))

		_, _, err := _createBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Backup vault not found")
		store.AssertExpectations(tt)
	})

	t.Run("ReturnsExistingBackupWhenFound", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			BackupUUID:    "backup-uuid",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
			VolumeName:    "testVolume",
			Protocols:     []string{"NFS"},
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		existingBackup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         "testBackup",
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: "testVolume", Protocols: []string{"NFS"}},
		}

		store.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, params.BackupVaultID, int64(1)).Return(backupVault, nil)
		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultID, params.BackupUUID, params.AccountName).Return(existingBackup, nil)

		result, jobID, err := _createBackupInternal(ctx, store, temporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", jobID)
		store.AssertExpectations(tt)
	})

	t.Run("FailsWhenGetBackupByExternalUUIDReturnsNonNotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			BackupUUID:    "backup-uuid",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
			VolumeName:    "testVolume",
			Protocols:     []string{"NFS"},
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		store.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, params.BackupVaultID, int64(1)).Return(backupVault, nil)
		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultID, params.BackupUUID, params.AccountName).Return(nil, errors.New("database error"))

		_, _, err := _createBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "database error")
		store.AssertExpectations(tt)
	})

	t.Run("FailsWhenCreateBackupFails", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			BackupUUID:    "backup-uuid",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
			VolumeName:    "testVolume",
			Protocols:     []string{"NFS"},
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		store.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, params.BackupVaultID, int64(1)).Return(backupVault, nil)
		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultID, params.BackupUUID, params.AccountName).Return(nil, vsaerror.NewNotFoundErr("backup", &params.BackupUUID))
		store.On("CreateBackup", ctx, mock.Anything).Return(nil, errors.New("failed to create backup"))

		_, _, err := _createBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to create backup")
		store.AssertExpectations(tt)
	})

	t.Run("FailsWhenFinishBackupFails", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			BackupUUID:    "backup-uuid",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
			VolumeName:    "testVolume",
			Protocols:     []string{"NFS"},
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: params.VolumeName, Protocols: params.Protocols},
		}

		store.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, params.BackupVaultID, int64(1)).Return(backupVault, nil)
		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultID, params.BackupUUID, params.AccountName).Return(nil, vsaerror.NewNotFoundErr("backup", &params.BackupUUID))
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("FinishBackup", ctx, backup).Return(nil, errors.New("failed to finish backup"))

		_, _, err := _createBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to finish backup")
		store.AssertExpectations(tt)
	})

	t.Run("WhenHydrationDisabled_SkipsHydration", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		// Override hydrationEnabled to false
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = false
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			BackupUUID:    "backup-uuid",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
			VolumeName:    "testVolume",
			Protocols:     []string{"NFS"},
			Region:        "us-central1",
		}

		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			BackupVault:  backupVault,
			Attributes: &datamodel.BackupAttributes{
				VolumeName:        params.VolumeName,
				AccountIdentifier: account.Name,
				Protocols:         params.Protocols,
			},
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		store.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, params.BackupVaultID, int64(1)).Return(backupVault, nil)
		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultID, params.BackupUUID, params.AccountName).Return(nil, vsaerror.NewNotFoundErr("backup", &params.BackupUUID))
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("FinishBackup", ctx, backup).Return(backup, nil)

		result, jobID, err := _createBackupInternal(ctx, store, temporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", jobID)
		store.AssertExpectations(tt)
	})

	t.Run("WhenHydrationEnabledAndBackupVaultIsNil_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		// Override hydrationEnabled to true
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			BackupUUID:    "backup-uuid",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
			VolumeName:    "testVolume",
			Protocols:     []string{"NFS"},
			Region:        "us-central1",
		}

		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			BackupVault:  nil, // This should trigger the error
			Attributes: &datamodel.BackupAttributes{
				VolumeName:        params.VolumeName,
				AccountIdentifier: account.Name,
				Protocols:         params.Protocols,
			},
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		store.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, params.BackupVaultID, int64(1)).Return(backupVault, nil)
		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultID, params.BackupUUID, params.AccountName).Return(nil, vsaerror.NewNotFoundErr("backup", &params.BackupUUID))
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("FinishBackup", ctx, backup).Return(backup, nil)

		_, _, err := _createBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Could not find the backup vault associated with the backup")
		store.AssertExpectations(tt)
	})

	t.Run("WhenHydrationEnabledAndBackupVaultExistsButHydrationFails_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		// Override hydrationEnabled to true
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		// Override hydrateCreatedBackupsToCCFE to return an error
		originalHydrateCreatedBackupsToCCFE := hydrateCreatedBackupsToCCFE
		hydrateCreatedBackupsToCCFE = func(ctx context.Context, params *common.CreateBackupParams, backup *datamodel.Backup) error {
			return errors.New("failed to hydrate created backup to CCFE")
		}
		defer func() { hydrateCreatedBackupsToCCFE = originalHydrateCreatedBackupsToCCFE }()

		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			BackupUUID:    "backup-uuid",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
			VolumeName:    "testVolume",
			Protocols:     []string{"NFS"},
			Region:        "us-central1",
		}

		regionName := "us-central1"
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "test-vault-uuid"},
				Name:             "test-vault",
				SourceRegionName: &regionName,
			},
			Attributes: &datamodel.BackupAttributes{
				VolumeName:        params.VolumeName,
				AccountIdentifier: account.Name,
				Protocols:         params.Protocols,
			},
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		store.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, params.BackupVaultID, int64(1)).Return(backupVault, nil)
		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultID, params.BackupUUID, params.AccountName).Return(nil, vsaerror.NewNotFoundErr("backup", &params.BackupUUID))
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("FinishBackup", ctx, backup).Return(backup, nil)

		_, _, err := _createBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to hydrate created backup to CCFE")
		store.AssertExpectations(tt)
	})

	t.Run("WhenHydrationEnabledAndBackupVaultExistsAndHydrationSucceeds_ReturnsNoError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		// Override hydrationEnabled to true
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		// Override hydrateCreatedBackupsToCCFE to return success
		originalHydrateCreatedBackupsToCCFE := hydrateCreatedBackupsToCCFE
		hydrateCreatedBackupsToCCFE = func(ctx context.Context, params *common.CreateBackupParams, backup *datamodel.Backup) error {
			return nil
		}
		defer func() { hydrateCreatedBackupsToCCFE = originalHydrateCreatedBackupsToCCFE }()

		params := &common.CreateBackupParams{
			BackupName:    "testBackup",
			BackupUUID:    "backup-uuid",
			BackupVaultID: "testVaultID",
			AccountName:   "testAccount",
			VolumeName:    "testVolume",
			Protocols:     []string{"NFS"},
			Region:        "us-central1",
		}

		regionName := "us-central1"
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "test-vault-uuid"},
				Name:             "test-vault",
				SourceRegionName: &regionName,
			},
			Attributes: &datamodel.BackupAttributes{
				VolumeName:        params.VolumeName,
				AccountIdentifier: account.Name,
				Protocols:         params.Protocols,
			},
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		store.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, params.BackupVaultID, int64(1)).Return(backupVault, nil)
		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultID, params.BackupUUID, params.AccountName).Return(nil, vsaerror.NewNotFoundErr("backup", &params.BackupUUID))
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("FinishBackup", ctx, backup).Return(backup, nil)

		result, jobID, err := _createBackupInternal(ctx, store, temporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", jobID)
		store.AssertExpectations(tt)
	})
}

func Test_updateBackupInternal(t *testing.T) {
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "testAccountUUID"}, Name: "testAccount"}
	backupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "testVaultID"},
		AccountID:        1,
		SourceRegionName: func() *string { s := "us-east1"; return &s }(),
	}

	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	t.Run("Success", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.UpdateBackupParams{
			BackupVaultUUID: "testVaultID",
			BackupUUID:      "backup-uuid",
			AccountName:     "testAccount",
			Description:     "updated description",
		}

		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         "testBackup",
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			Description:  "old description",
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: "testVolume", Protocols: []string{"NFS"}},
		}
		updatedBackup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         "testBackup",
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			Description:  params.Description,
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: "testVolume", Protocols: []string{"NFS"}},
		}

		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		store.On("UpdateBackup", ctx, mock.Anything).Return(updatedBackup, nil)

		result, jobID, err := _updateBackupInternal(ctx, store, temporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", jobID)
		store.AssertExpectations(tt)
	})

	t.Run("FailsWhenAccountCreationFails", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.UpdateBackupParams{
			BackupVaultUUID: "testVaultID",
			BackupUUID:      "backup-uuid",
			AccountName:     "testAccount",
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("failed to get account")
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		_, _, err := _updateBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get account")
	})

	t.Run("FailsWhenBackupNotFound", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.UpdateBackupParams{
			BackupVaultUUID: "testVaultID",
			BackupUUID:      "backup-uuid",
			AccountName:     "testAccount",
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(nil, vsaerror.NewNotFoundErr("backup", &params.BackupUUID))

		_, _, err := _updateBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Backup not found")
		store.AssertExpectations(tt)
	})

	t.Run("FailsWhenBackupNotInAvailableState", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.UpdateBackupParams{
			BackupVaultUUID: "testVaultID",
			BackupUUID:      "backup-uuid",
			AccountName:     "testAccount",
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         "testBackup",
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: "testVolume", Protocols: []string{"NFS"}},
		}

		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)

		_, _, err := _updateBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Backup can only be updated when in AVAILABLE state")
		store.AssertExpectations(tt)
	})

	t.Run("FailsWhenUpdateBackupFails", func(tt *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(tt)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		params := &common.UpdateBackupParams{
			BackupVaultUUID: "testVaultID",
			BackupUUID:      "backup-uuid",
			AccountName:     "testAccount",
			Description:     "updated description",
		}

		origGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = origGetOrCreateAccount }()

		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         "testBackup",
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			Description:  "old description",
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: "testVolume", Protocols: []string{"NFS"}},
		}

		store.On("GetBackupByExternalUUID", ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName).Return(backup, nil)
		store.On("UpdateBackup", ctx, mock.Anything).Return(nil, errors.New("failed to update backup"))

		_, _, err := _updateBackupInternal(ctx, store, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to update backup")
		store.AssertExpectations(tt)
	})
}

func TestUpdateBackupLatestLogicalBackupSizeByVolume(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		volumeUUID := "test-volume-uuid"
		backupUUID := "test-backup-uuid"

		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", ctx, volumeUUID, backupUUID).Return(nil)

		err := orchestrator.UpdateBackupLatestLogicalBackupSizeByVolume(ctx, volumeUUID, backupUUID)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		volumeUUID := "test-volume-uuid"
		backupUUID := "test-backup-uuid"
		expectedError := errors.New("failed to update backup latest logical backup size")

		mockStorage.On("UpdateBackupLatestLogicalBackupSizeByVolume", ctx, volumeUUID, backupUUID).Return(expectedError)

		err := orchestrator.UpdateBackupLatestLogicalBackupSizeByVolume(ctx, volumeUUID, backupUUID)

		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})
}

// Test_createVolumePayloadFromExpertModeVolume tests the createVolumePayloadFromExpertModeVolume function
// This covers missing lines: 102, 112-113, 115-117, 119-120, 123, 128, 133, 142
func Test_createVolumePayloadFromExpertModeVolume(t *testing.T) {
	ctx := context.Background()
	t.Run("WhenPoolHasVendorID_SetsVendorSubnetID", func(t *testing.T) {
		store := database.NewMockStorage(t)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			VendorID:  "vendor-123",
			Network:   "subnet-network-123",
		}
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
		}
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
				ID:   1,
			},
			PoolID:       1,
			Pool:         pool,
			ExternalUUID: "external-uuid",
			Svm:          svm,
			SvmID:        svm.ID,
		}
		volume, err := createVolumePayloadFromExpertModeVolume(ctx, store, expertModeVol)
		assert.NoError(t, err)
		assert.NotNil(t, volume)
		assert.Equal(t, "subnet-network-123", volume.VolumeAttributes.VendorSubnetID)
		store.AssertExpectations(t)
	})

	t.Run("WhenPoolHasNoVendorID_DoesNotSetVendorSubnetID", func(t *testing.T) {
		store := database.NewMockStorage(t)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			VendorID:  "", // Empty VendorID
			Network:   "subnet-network-123",
		}
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
		}
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
				ID:   1,
			},
			PoolID:       1,
			Pool:         pool,
			ExternalUUID: "external-uuid",
			Svm:          svm,
			SvmID:        svm.ID,
		}
		volume, err := createVolumePayloadFromExpertModeVolume(ctx, store, expertModeVol)
		assert.NoError(t, err)
		assert.NotNil(t, volume)
		assert.Empty(t, volume.VolumeAttributes.VendorSubnetID)
		store.AssertExpectations(t)
	})

	t.Run("Success_WithPreloadedPool", func(t *testing.T) {
		store := database.NewMockStorage(t)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			VendorID:  "vendor-1",
			Network:   "network-1",
		}
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
		}
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-uuid",
				ID:        1,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			PoolID:       1,
			Pool:         pool, // Pool preloaded
			AccountID:    1,
			Account:      account,
			State:        datamodel.LifeCycleStateREADY,
			Name:         "expert-vol",
			ExternalUUID: "external-uuid",
			Svm:          svm,
			SvmID:        svm.ID,
		}
		volume, err := createVolumePayloadFromExpertModeVolume(ctx, store, expertModeVol)
		assert.NoError(t, err)
		assert.NotNil(t, volume)
		assert.Equal(t, expertModeVol.UUID, volume.UUID)
		assert.Equal(t, expertModeVol.ID, volume.ID)
		assert.Equal(t, expertModeVol.Name, volume.Name)
		assert.Equal(t, expertModeVol.AccountID, volume.AccountID)
		assert.Equal(t, pool, volume.Pool)
		assert.Equal(t, svm.ID, volume.SvmID)
		assert.Equal(t, svm, volume.Svm)
		assert.Equal(t, "external-uuid", volume.VolumeAttributes.ExternalUUID)
		assert.Equal(t, "network-1", volume.VolumeAttributes.VendorSubnetID)
		store.AssertExpectations(t)
	})
}

// Test_createBackup_ExpertModeVolumeErrors tests error paths in _createBackup for expert mode volumes
// This covers missing lines: 334, 339
func Test_createBackup_ExpertModeVolumeErrors(t *testing.T) {
	ctx := context.Background()
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "account-uuid",
		},
		Name: "test-account",
	}

	setupCommonMocks := func(t *testing.T) (*database.MockStorage, *workflow_engine_mock.MockTemporalTestClient) {
		store := database.NewMockStorage(t)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		t.Cleanup(func() {
			getOrCreateAccount = originalGetOrCreateAccount
		})

		originalValidateCreateBackupParams := validateCreateBackupParams
		validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
			return nil
		}
		t.Cleanup(func() {
			validateCreateBackupParams = originalValidateCreateBackupParams
		})

		return store, temporal
	}

	t.Run("ExpertModeVolume_WhenWorkflowExecutionFails", func(t *testing.T) {
		store, temporal := setupCommonMocks(t)
		params := &common.CreateBackupParams{
			VolumeUUID:         "expert-volume-uuid",
			BackupVaultID:      "vault-id",
			BackupName:         "test-backup",
			AccountName:        "test-account",
			IsExpertModeVolume: true,
		}
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
		}
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			VendorID:  "abc",
		}
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: params.VolumeUUID,
				ID:   1,
			},
			AccountID: account.ID,
			Account:   account,
			PoolID:    pool.ID,
			State:     datamodel.LifeCycleStateREADY,
			Name:      "expert-vol",
			Svm:       svm,
			SvmID:     svm.ID,
			Pool:      pool,
		}

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: params.BackupVaultID},
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
			BackupVault:  backupVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: expertModeVol.Name, Protocols: []string{}},
			Description:  params.Description,
			Type:         params.BackupType,
		}

		store.On("GetExpertModeVolumeByExternalUUID", ctx, params.VolumeUUID).Return(expertModeVol, nil)
		store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
			return j.JobAttributes != nil && j.JobAttributes.ResourceUUID == backup.UUID
		})).Return(job, nil)

		// Mock workflow execution to fail
		workflowErr := errors.New("workflow execution failed")
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowErr).Once()

		// The defer block will try to delete the backup and update the job when error occurs
		store.On("DeleteBackup", ctx, backup.UUID).Return(nil, nil)
		store.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.Error(t, err)
		assert.Equal(t, workflowErr, err)
		store.AssertExpectations(t)
	})

	t.Run("ExpertModeVolume_VaultSwitch_BlockedWhenBackupsExist", func(t *testing.T) {
		store, temporal := setupCommonMocks(t)
		currentVaultID := "current-vault-uuid"
		newVaultID := "new-vault-uuid"
		params := &common.CreateBackupParams{
			VolumeUUID:         "expert-volume-uuid",
			BackupVaultID:      newVaultID,
			BackupName:         "test-backup",
			AccountName:        "test-account",
			IsExpertModeVolume: true,
		}
		currentVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 42, UUID: currentVaultID},
		}
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid", ID: 1},
			ExternalUUID: params.VolumeUUID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateREADY,
			Name:         "expert-vol",
			BackupConfig: &datamodel.DataProtection{BackupVaultID: currentVaultID},
		}

		store.On("GetExpertModeVolumeByExternalUUID", ctx, params.VolumeUUID).Return(expertModeVol, nil)
		store.On("GetBackupVault", ctx, currentVaultID).Return(currentVault, nil)
		store.On("GetBackupCountByVolumeAndVault", ctx, params.VolumeUUID, int64(42)).Return(int64(5), nil)

		_, _, err := _createBackup(ctx, store, temporal, params)
		assert.Error(t, err)
		assert.True(t, vsaerror.IsUserInputValidationErr(err))
		assert.Contains(t, err.Error(), "switching backup vault is not supported while backups exist")
		store.AssertExpectations(t)
	})

	t.Run("ExpertModeVolume_VaultSwitch_AllowedWhenNoBackupsExist", func(t *testing.T) {
		store, temporal := setupCommonMocks(t)
		currentVaultID := "current-vault-uuid"
		newVaultID := "new-vault-uuid"
		params := &common.CreateBackupParams{
			VolumeUUID:         "expert-volume-uuid",
			BackupVaultID:      newVaultID,
			BackupName:         "test-backup",
			AccountName:        "test-account",
			IsExpertModeVolume: true,
		}
		currentVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 42, UUID: currentVaultID},
		}
		newVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 99, UUID: newVaultID},
		}
		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}}
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, VendorID: "abc"}
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid", ID: 1},
			ExternalUUID: params.VolumeUUID,
			AccountID:    account.ID,
			Account:      account,
			PoolID:       pool.ID,
			Pool:         pool,
			SvmID:        svm.ID,
			Svm:          svm,
			State:        datamodel.LifeCycleStateREADY,
			Name:         "expert-vol",
			BackupConfig: &datamodel.DataProtection{BackupVaultID: currentVaultID},
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: "backup-uuid"},
			Name:         params.BackupName,
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
			BackupVault:  newVault,
			Attributes:   &datamodel.BackupAttributes{VolumeName: expertModeVol.Name, Protocols: []string{}},
		}

		store.On("GetExpertModeVolumeByExternalUUID", ctx, params.VolumeUUID).Return(expertModeVol, nil)
		store.On("GetBackupVault", ctx, currentVaultID).Return(currentVault, nil)
		store.On("GetBackupCountByVolumeAndVault", ctx, params.VolumeUUID, int64(42)).Return(int64(0), nil)
		store.On("GetBackupVault", ctx, newVaultID).Return(newVault, nil)
		store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
		store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
			return j.JobAttributes != nil && j.JobAttributes.ResourceUUID == backup.UUID
		})).Return(job, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		result, _, err := _createBackup(ctx, store, temporal, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		store.AssertExpectations(t)
	})
}

// Test_validateCreateBackupParams_RegularVolumeGetVolumeError tests the error path when GetVolume fails
// This covers missing line: 504
func Test_validateCreateBackupParams_RegularVolumeGetVolumeError(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenGetVolumeFails", func(t *testing.T) {
		store := database.NewMockStorage(t)
		params := &common.CreateBackupParams{
			VolumeUUID:         "test-volume-uuid",
			BackupVaultID:      "vault-id",
			BackupName:         "test-backup",
			AccountName:        "test-account",
			IsExpertModeVolume: false,
		}

		expectedErr := errors.New("database error")
		store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
		store.On("GetVolume", ctx, params.VolumeUUID).Return(nil, expectedErr)

		err := _validateCreateBackupParams(ctx, store, params)
		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		store.AssertExpectations(t)
	})
}

func TestDeleteBackup_PreviousStateAndDetailsInJobAttributes(t *testing.T) {
	t.Run("WhenDeleteBackup_JobAttributesContainsPreviousStateAndDetails", func(t *testing.T) {
		ctx := context.Background()
		store := database.NewMockStorage(t)
		temporal := new(workflow_engine_mock.MockTemporalTestClient)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"}
		params := &common.DeleteBackupParams{
			BackupUUID:      "testBackupUUID",
			AccountName:     "acc",
			BackupVaultUUID: "testVaultID",
		}

		previousState := datamodel.LifeCycleStateAvailable
		previousStateDetails := datamodel.LifeCycleStateAvailableDetails
		backup := &datamodel.Backup{
			BaseModel:    datamodel.BaseModel{UUID: params.BackupUUID},
			State:        previousState,
			StateDetails: previousStateDetails,
			BackupVault:  &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: params.BackupVaultUUID}},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		validateBackupDeleteParams = func(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
			return nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateBackupDeleteParams = _validateBackupDeleteParams
		}()

		store.On("GetBackup", ctx, params.BackupVaultUUID, params.BackupUUID, account.Name).Return(backup, nil)
		conditions := [][]interface{}{{"volume_attributes->>'restored_backup_id' = ?", backup.UUID}, {"state = ?", "RESTORING"}}
		store.On("ListVolumes", ctx, conditions).Return(nil, nil)
		store.On("CreateJob", ctx, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.JobAttributes != nil &&
				job.JobAttributes.PreviousState == previousState &&
				job.JobAttributes.PreviousStateDetails == previousStateDetails &&
				job.JobAttributes.ResourceUUID == backup.UUID
		})).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}, nil)
		store.On("UpdateBackupState", ctx, mock.Anything).Return(backup, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		_, jobUUID, err := deleteBackup(ctx, store, temporal, params)
		assert.NoError(t, err)
		assert.Equal(t, "job-uuid", jobUUID)
		store.AssertExpectations(t)
	})
}

func TestValidateCreateBackupParams_ExpertModeVolume_Success(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)

	params := &common.CreateBackupParams{
		VolumeUUID:         "test-volume-uuid",
		BackupVaultID:      "test-vault-id",
		BackupName:         "test-backup",
		AccountName:        "test-account",
		IsExpertModeVolume: true,
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}

	// Mock IsBackupInCreatingorDeletingStateByVolume to return false
	store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
	// Mock GetAccount
	store.On("GetAccount", ctx, params.AccountName).Return(account, nil)
	// Mock GetBackupsByBackupVaultOwnerIDAndFilter to return empty list (no duplicate)
	filters := [][]interface{}{{"name = ?", params.BackupName}}
	store.On("GetBackupsByBackupVaultOwnerIDAndFilter", ctx, params.BackupVaultID, account.ID, filters).Return([]*datamodel.Backup{}, nil)

	err := _validateCreateBackupParams(ctx, store, params)

	assert.NoError(t, err)
	store.AssertExpectations(t)
}

func TestValidateCreateBackupParams_ExpertModeVolume_BackupInProgress(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)

	params := &common.CreateBackupParams{
		VolumeUUID:         "test-volume-uuid",
		BackupVaultID:      "test-vault-id",
		BackupName:         "test-backup",
		AccountName:        "test-account",
		IsExpertModeVolume: true,
	}

	// Mock IsBackupInCreatingorDeletingStateByVolume to return true
	store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(true, nil)

	err := _validateCreateBackupParams(ctx, store, params)

	assert.EqualError(t, err, "A backup operation from the same volume is currently in progress. Please wait for it to complete before starting a new backup")
	store.AssertExpectations(t)
}

func TestValidateCreateBackupParams_ExpertModeVolume_DuplicateBackupName(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)

	params := &common.CreateBackupParams{
		VolumeUUID:         "test-volume-uuid",
		BackupVaultID:      "test-vault-id",
		BackupName:         "existing-backup",
		AccountName:        "test-account",
		IsExpertModeVolume: true,
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}

	existingBackup := &datamodel.Backup{
		BaseModel:     datamodel.BaseModel{UUID: "existing-backup-uuid"},
		Name:          "existing-backup",
		BackupVaultID: 1,
	}

	// Mock IsBackupInCreatingorDeletingStateByVolume to return false
	store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
	// Mock GetAccount
	store.On("GetAccount", ctx, params.AccountName).Return(account, nil)
	// Mock GetBackupsByBackupVaultOwnerIDAndFilter to return existing backup
	filters := [][]interface{}{{"name = ?", params.BackupName}}
	store.On("GetBackupsByBackupVaultOwnerIDAndFilter", ctx, params.BackupVaultID, account.ID, filters).Return([]*datamodel.Backup{existingBackup}, nil)

	err := _validateCreateBackupParams(ctx, store, params)

	assert.EqualError(t, err, "Backup with the same name already exists in the specified backup vault")
	store.AssertExpectations(t)
}

func TestValidateCreateBackupParams_ExpertModeVolume_GetAccountError(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)

	params := &common.CreateBackupParams{
		VolumeUUID:         "test-volume-uuid",
		BackupVaultID:      "test-vault-id",
		BackupName:         "test-backup",
		AccountName:        "test-account",
		IsExpertModeVolume: true,
	}

	// Mock IsBackupInCreatingorDeletingStateByVolume to return false
	store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
	// Mock GetAccount to return error
	store.On("GetAccount", ctx, params.AccountName).Return(nil, errors.New("database connection error"))

	err := _validateCreateBackupParams(ctx, store, params)

	assert.EqualError(t, err, "database connection error")
	store.AssertExpectations(t)
}

func TestValidateCreateBackupParams_ExpertModeVolume_GetBackupsError(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)

	params := &common.CreateBackupParams{
		VolumeUUID:         "test-volume-uuid",
		BackupVaultID:      "test-vault-id",
		BackupName:         "test-backup",
		AccountName:        "test-account",
		IsExpertModeVolume: true,
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}

	// Mock IsBackupInCreatingorDeletingStateByVolume to return false
	store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, nil)
	// Mock GetAccount
	store.On("GetAccount", ctx, params.AccountName).Return(account, nil)
	// Mock GetBackupsByBackupVaultOwnerIDAndFilter to return error
	filters := [][]interface{}{{"name = ?", params.BackupName}}
	store.On("GetBackupsByBackupVaultOwnerIDAndFilter", ctx, params.BackupVaultID, account.ID, filters).Return(nil, errors.New("query execution error"))

	err := _validateCreateBackupParams(ctx, store, params)

	assert.EqualError(t, err, "query execution error")
	store.AssertExpectations(t)
}

func TestValidateCreateBackupParams_ExpertModeVolume_BackupTransitionCheckError(t *testing.T) {
	ctx := context.Background()
	store := database.NewMockStorage(t)

	params := &common.CreateBackupParams{
		VolumeUUID:         "test-volume-uuid",
		BackupVaultID:      "test-vault-id",
		BackupName:         "test-backup",
		AccountName:        "test-account",
		IsExpertModeVolume: true,
	}

	// Mock IsBackupInCreatingorDeletingStateByVolume to return error
	store.On("IsBackupInCreatingorDeletingStateByVolume", ctx, params.VolumeUUID).Return(false, errors.New("database query failed"))

	err := _validateCreateBackupParams(ctx, store, params)

	assert.EqualError(t, err, "database query failed")
	store.AssertExpectations(t)
}

func TestListBackupsWithoutAccountFilter(t *testing.T) {
	ctx := context.Background()

	t.Run("SuccessfullyListsBackupsWithoutAccountFilter", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		backupVaultID := "test-vault-uuid"
		filters := [][]interface{}{{"name = ?", "test-backup"}}

		expectedBackups := []*datamodel.Backup{
			{
				BaseModel: datamodel.BaseModel{UUID: "backup-1-uuid"},
				Name:      "test-backup",
			},
		}

		mockStorage.EXPECT().
			GetBackupsByBackupVaultUUIDAndFilter(ctx, backupVaultID, filters).
			Return(expectedBackups, nil).
			Once()

		backups, err := orchestrator.ListBackupsWithoutAccountFilter(ctx, backupVaultID, filters)

		assert.NoError(tt, err)
		assert.Len(tt, backups, 1)
		assert.Equal(tt, "test-backup", backups[0].Name)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenDatabaseFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		backupVaultID := "test-vault-uuid"
		expectedError := errors.New("database error")

		mockStorage.EXPECT().
			GetBackupsByBackupVaultUUIDAndFilter(ctx, backupVaultID, [][]interface{}(nil)).
			Return(nil, expectedError).
			Once()

		backups, err := orchestrator.ListBackupsWithoutAccountFilter(ctx, backupVaultID, nil)

		assert.Error(tt, err)
		assert.Nil(tt, backups)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsEmptyListWhenNoBackups", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		backupVaultID := "test-vault-uuid"
		emptyBackups := []*datamodel.Backup{}

		mockStorage.EXPECT().
			GetBackupsByBackupVaultUUIDAndFilter(ctx, backupVaultID, [][]interface{}(nil)).
			Return(emptyBackups, nil).
			Once()

		backups, err := orchestrator.ListBackupsWithoutAccountFilter(ctx, backupVaultID, nil)

		assert.NoError(tt, err)
		assert.Empty(tt, backups)
		mockStorage.AssertExpectations(tt)
	})
}

// ===== GCBDR coverage: _createBackup with BackupVaultServiceType == GCBDR uses GetVolume =====

func TestCreateBackup_GCBDR_UsesGetVolumeWithoutAccountID(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	store := database.NewMockStorage(t)
	temporal := workflow_engine_mock.NewMockTemporalTestClient(t)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: 10, UUID: "vol-uuid"},
		Name:      "test-volume",
		Account:   account,
		AccountID: account.ID,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{"NFS"},
		},
	}

	backupVault := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: 5, UUID: "vault-uuid"},
		ServiceType: datamodel.ServiceTypeCrossProject,
		AccountID:   account.ID,
	}

	params := &common.CreateBackupParams{
		BackupName:             "gcbdr-backup",
		VolumeUUID:             volume.UUID,
		BackupVaultID:          backupVault.UUID,
		AccountName:            account.Name,
		Description:            "GCBDR test backup",
		BackupType:             "MANUAL",
		BackupVaultServiceType: datamodel.ServiceTypeCrossProject, // triggers GetVolume path
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "workflow-id",
	}

	backup := &datamodel.Backup{
		BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
		Name:          params.BackupName,
		VolumeUUID:    params.VolumeUUID,
		BackupVaultID: backupVault.ID,
		BackupVault:   backupVault,
		State:         datamodel.LifeCycleStateCreating,
		StateDetails:  datamodel.LifeCycleStateCreatingDetails,
		Attributes: &datamodel.BackupAttributes{
			VolumeName:        volume.Name,
			AccountIdentifier: account.Name,
			Protocols:         volume.VolumeAttributes.Protocols,
		},
		Description: params.Description,
		Type:        params.BackupType,
	}

	origValidate := validateCreateBackupParams
	validateCreateBackupParams = func(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
		return nil
	}
	origGetOrCreate := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() {
		validateCreateBackupParams = origValidate
		getOrCreateAccount = origGetOrCreate
	}()

	// GCBDR path: uses GetVolume (NOT GetVolumeWithAccountID)
	store.On("GetVolume", ctx, params.VolumeUUID).Return(volume, nil)
	store.On("GetBackupVault", ctx, params.BackupVaultID).Return(backupVault, nil)
	store.On("CreateBackup", ctx, mock.Anything).Return(backup, nil)
	store.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
		return j.JobAttributes != nil && j.JobAttributes.ResourceUUID == backup.UUID
	})).Return(job, nil)
	temporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	result, jobID, err := _createBackup(ctx, store, temporal, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, job.UUID, jobID)
	store.AssertExpectations(t)
}

// ===== GCBDR coverage: GetBackupVaultByUUIDWithoutAccount =====

func TestGetBackupVaultByUUIDWithoutAccount_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: mockTemporal,
	}

	bvUUID := "gcbdr-vault-uuid"
	backupVault := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: 1, UUID: bvUUID},
		Name:        "gcbdr-vault",
		ServiceType: datamodel.ServiceTypeCrossProject,
		Account:     &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "owner-uuid"}},
	}

	ctx := context.Background()
	mockStorage.On("GetBackupVault", ctx, bvUUID).Return(backupVault, nil)

	result, err := orchestrator.GetBackupVaultByUUIDWithoutAccount(ctx, bvUUID)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, bvUUID, result.BackupVaultID)
	assert.Equal(t, "gcbdr-vault", result.Name)
	mockStorage.AssertExpectations(t)
}

func TestGetBackupVaultByUUIDWithoutAccount_Error(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: mockTemporal,
	}

	bvUUID := "nonexistent-vault"
	ctx := context.Background()
	mockStorage.On("GetBackupVault", ctx, bvUUID).Return(nil, errors.New("not found"))

	result, err := orchestrator.GetBackupVaultByUUIDWithoutAccount(ctx, bvUUID)
	assert.Error(t, err)
	assert.Nil(t, result)
	mockStorage.AssertExpectations(t)
}
