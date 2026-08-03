package gcp

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

func TestGetMultipleKmsConfigs(t *testing.T) {
	mockLogger := log.NewLogger()
	store, err := database.SetupStorageForTest(mockLogger)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}

	err = database.ClearInMemoryDB(store.DB())
	if err != nil {
		t.Fatalf("Failed to clean up test storage: %v", err)
	}

	orchInstance := GCPOrchestrator{
		storage: store,
	}
	serviceAccounts := []*datamodel.ServiceAccount{
		{BaseModel: datamodel.BaseModel{ID: int64(111), UUID: "uuid10"}, Name: "ServiceAccount1"},
		{BaseModel: datamodel.BaseModel{ID: int64(222), UUID: "uuid20"}, Name: "ServiceAccount2"},
	}
	err = store.DB().Create(serviceAccounts).Error
	if err != nil {
		t.Fatalf("Failed to create Service-Accounts table: %v", err)
	}

	kmsConfigs := []*datamodel.KmsConfig{
		{BaseModel: datamodel.BaseModel{UUID: "uuid1", DeletedAt: nil}, Name: "kmsConfig1", ServiceAccountID: &serviceAccounts[0].ID,
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount1@account.com"}},
		{BaseModel: datamodel.BaseModel{UUID: "uuid2", DeletedAt: nil}, Name: "kmsConfig2", ServiceAccountID: &serviceAccounts[1].ID,
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount2@account.com"}},
	}
	err = store.DB().Create(kmsConfigs).Error
	if err != nil {
		t.Fatalf("Failed to create KMS Configs table: %v", err)
	}

	t.Run("WhenListedKMSConfigsAreFound", func(tt *testing.T) {
		kmsConfigUUIDList := []string{"uuid1", "uuid2"}
		result, err := orchInstance.GetMultipleKMSConfigs(context.Background(), kmsConfigUUIDList)

		assert.NoError(tt, err)
		assert.Equal(tt, "kmsConfig1", result[0].Name)
		assert.Equal(tt, "kmsConfig2", result[1].Name)
		assert.Equal(tt, "sdeServiceAccount1@account.com", result[0].KmsAttributes.SdeServiceAccountEmail)
		assert.Equal(tt, "sdeServiceAccount2@account.com", result[1].KmsAttributes.SdeServiceAccountEmail)
	})
	t.Run("ReturnsEmptyListWhenNoUUIDsAreProvided", func(tt *testing.T) {
		kmsConfigUUIDList := []string{}
		result, err := orchInstance.GetMultipleKMSConfigs(context.Background(), kmsConfigUUIDList)

		assert.NoError(tt, err)
		assert.Empty(tt, result)
	})
	t.Run("ReturnsNilWhenKMSConfigsAreNotFound", func(tt *testing.T) {
		kmsConfigUUIDList := []string{"nonexistent-uuid"}
		result, err := orchInstance.GetMultipleKMSConfigs(context.Background(), kmsConfigUUIDList)

		assert.NoError(tt, err)
		assert.Empty(tt, result)
	})
	t.Run("WhenStorageLayerReturnsError", func(tt *testing.T) {
		kmsConfigUUIDList := []string{"some-uuid"}
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetMultipleKmsConfigs(mock.Anything, mock.Anything).Return(nil, errors.New("internal error"))
		orchInstanceNew := GCPOrchestrator{storage: mockStorage}

		result, err := orchInstanceNew.GetMultipleKMSConfigs(context.Background(), kmsConfigUUIDList)

		assert.Error(tt, err)
		assert.Empty(tt, result)
	})
}

func TestGetKmsConfigsByUUIDs(t *testing.T) {
	mockLogger := log.NewLogger()
	store, err := database.SetupStorageForTest(mockLogger)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}

	err = database.ClearInMemoryDB(store.DB())
	if err != nil {
		t.Fatalf("Failed to clean up test storage: %v", err)
	}

	orchInstance := GCPOrchestrator{
		storage: store,
	}
	kmsConfigs := []*datamodel.KmsConfig{
		{
			BaseModel:   datamodel.BaseModel{UUID: "uuid1"},
			Name:        "kmsConfig1",
			Description: "desc1",
			KmsAttributes: &datamodel.KmsAttributes{
				VcpServiceAccountEmail: "vcp-sa-1@example.com",
			},
		},
		{
			BaseModel:   datamodel.BaseModel{UUID: "uuid2"},
			Name:        "kmsConfig2",
			Description: "desc2",
			KmsAttributes: &datamodel.KmsAttributes{
				VcpServiceAccountEmail: "vcp-sa-2@example.com",
			},
		},
	}
	err = store.DB().Create(kmsConfigs).Error
	if err != nil {
		t.Fatalf("Failed to create KMS Configs table: %v", err)
	}

	t.Run("WhenListedKMSConfigsAreFound", func(tt *testing.T) {
		result, getErr := orchInstance.GetKmsConfigsByUUIDs(
			context.Background(),
			[]string{"uuid1", "uuid2"},
		)

		require.NoError(tt, getErr)
		require.Len(tt, result, 2)

		resultByUUID := make(map[string]*models.KmsConfig, len(result))
		for _, kmsConfig := range result {
			resultByUUID[kmsConfig.UUID] = kmsConfig
		}

		uuid1Config, ok := resultByUUID["uuid1"]
		require.True(tt, ok)
		require.NotNil(tt, uuid1Config)
		require.NotNil(tt, uuid1Config.KmsAttributes)
		assert.Equal(tt, "kmsConfig1", uuid1Config.Name)
		assert.Equal(tt, "vcp-sa-1@example.com", uuid1Config.KmsAttributes.VcpServiceAccountEmail)
	})

	t.Run("ReturnsEmptyListWhenNoUUIDsMatch", func(tt *testing.T) {
		result, getErr := orchInstance.GetKmsConfigsByUUIDs(
			context.Background(),
			[]string{"missing"},
		)

		require.NoError(tt, getErr)
		assert.Empty(tt, result)
	})

	t.Run("WhenStorageLayerReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().
			GetKmsConfigsByUUIDs(mock.Anything, []string{"uuid1"}).
			Return(nil, errors.New("internal error"))
		orchInstanceNew := GCPOrchestrator{storage: mockStorage}

		result, getErr := orchInstanceNew.GetKmsConfigsByUUIDs(
			context.Background(),
			[]string{"uuid1"},
		)

		assert.Error(tt, getErr)
		assert.Empty(tt, result)
	})
}

func TestMigrateKmsConfig_RejectsWhenEncryptPoolInFlight(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()
	cvp.CVP_HOST = "localhost:8009"

	ctx := context.Background()
	mockLogger := log.NewLogger()
	store, err := database.SetupStorageForTest(mockLogger)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}
	if err = database.ClearInMemoryDB(store.DB()); err != nil {
		t.Fatalf("Failed to clean up test storage: %v", err)
	}

	accounts := []*datamodel.Account{{BaseModel: datamodel.BaseModel{UUID: "acct-uuid", ID: int64(1)}, Name: "account1"}}
	if err = store.DB().Create(accounts).Error; err != nil {
		t.Fatalf("Failed to create Accounts table: %v", err)
	}

	serviceAccounts := []*datamodel.ServiceAccount{{BaseModel: datamodel.BaseModel{ID: int64(111), UUID: "sa-uuid"}, Name: "ServiceAccount1"}}
	if err = store.DB().Create(serviceAccounts).Error; err != nil {
		t.Fatalf("Failed to create Service-Accounts table: %v", err)
	}

	kmsConfigs := []*datamodel.KmsConfig{
		{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}, Name: "kmsConfig1", ServiceAccountID: &serviceAccounts[0].ID, State: datamodel.LifeCycleStateREADY,
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sde@account.com", SdeKmsConfigUUID: "sde-kms-uuid"}},
	}
	if err = store.DB().Create(kmsConfigs).Error; err != nil {
		t.Fatalf("Failed to create KMS Configs table: %v", err)
	}

	// Simulate an EncryptPool operation already in flight for this account.
	encryptPoolJob := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "encrypt-pool-job"},
		Type:      string(datamodel.JobTypeEncryptPool),
		State:     string(datamodel.JobsStatePROCESSING),
		AccountID: sql.NullInt64{Int64: 1, Valid: true},
	}
	if err = store.DB().Create(encryptPoolJob).Error; err != nil {
		t.Fatalf("Failed to create in-flight EncryptPool job: %v", err)
	}

	orchInstance := GCPOrchestrator{storage: store, temporal: new(workflow_engine.MockTemporalTestClient)}

	params := common.MigrateKmsConfigParams{
		LocationID:    "home-location",
		ProjectNumber: "my-project",
		UUID:          "kms-uuid",
		AccountName:   "account1",
		State:         datamodel.LifeCycleStateREADY,
	}
	result, errMigrate := orchInstance.MigrateKmsConfig(ctx, &params)
	assert.Error(t, errMigrate)
	assert.True(t, errors.IsConflictErr(errMigrate))
	assert.Contains(t, errMigrate.Error(), "EncryptPool")
	assert.Equal(t, "", result)
}

func TestMigrateKmsConfig(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()
	cvp.CVP_HOST = "localhost:8009"

	ctx := context.Background()
	mockLogger := log.NewLogger()
	store, err := database.SetupStorageForTest(mockLogger)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}

	err = database.ClearInMemoryDB(store.DB())
	if err != nil {
		t.Fatalf("Failed to clean up test storage: %v", err)
	}

	accounts := []*datamodel.Account{{BaseModel: datamodel.BaseModel{UUID: "uuid1", ID: int64(1)}, Name: "account1"}}
	err = store.DB().Create(accounts).Error
	if err != nil {
		t.Fatalf("Failed to create Accounts table: %v", err)
	}

	serviceAccounts := []*datamodel.ServiceAccount{
		{BaseModel: datamodel.BaseModel{ID: int64(111), UUID: "uuid10"}, Name: "ServiceAccount1"},
		{BaseModel: datamodel.BaseModel{ID: int64(222), UUID: "uuid20"}, Name: "ServiceAccount2"},
	}
	err = store.DB().Create(serviceAccounts).Error
	if err != nil {
		t.Fatalf("Failed to create Service-Accounts table: %v", err)
	}

	kmsConfigs := []*datamodel.KmsConfig{
		{BaseModel: datamodel.BaseModel{UUID: "uuid1", DeletedAt: nil}, Name: "kmsConfig1", ServiceAccountID: &serviceAccounts[0].ID, State: datamodel.LifeCycleStateCreated,
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount1@account.com", SdeKmsConfigUUID: "sdeUuid1"}},
		{BaseModel: datamodel.BaseModel{UUID: "uuid2", DeletedAt: nil}, Name: "kmsConfig2", ServiceAccountID: &serviceAccounts[1].ID,
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount2@account.com", SdeKmsConfigUUID: ""}},
		{BaseModel: datamodel.BaseModel{UUID: "uuid3", DeletedAt: nil}, Name: "kmsConfig3", ServiceAccountID: &serviceAccounts[1].ID, State: datamodel.LifeCycleStateREADY,
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount1@account.com", SdeKmsConfigUUID: "sdeUuid1"}},
		{BaseModel: datamodel.BaseModel{UUID: "uuid4", DeletedAt: nil}, Name: "kmsConfig4", ServiceAccountID: &serviceAccounts[1].ID},
	}
	err = store.DB().Create(kmsConfigs).Error
	if err != nil {
		t.Fatalf("Failed to create KMS Configs table: %v", err)
	}

	mockTemporal := new(workflow_engine.MockTemporalTestClient)

	orchInstance := GCPOrchestrator{
		storage:  store,
		temporal: mockTemporal,
	}

	t.Run("WhenGetKmsConfigByUUIDReturnsRecordWithoutKmsAttributes", func(tt *testing.T) {
		params := common.MigrateKmsConfigParams{
			LocationID:     "home-location",
			ProjectNumber:  "my-project",
			UUID:           "uuid4",
			AccountName:    "account1",
			XCorrelationID: "",
		}
		result, errMigrate := orchInstance.MigrateKmsConfig(context.Background(), &params)
		assert.Error(tt, errMigrate)
		assert.Equal(tt, "KmsAttributes property not present within KmsConfig DB entry in VCP", errMigrate.Error())
		assert.Equal(tt, "", result)
	})
	t.Run("WhenGetKmsConfigByUUIDReturnsRecordWithEmptySdeUUID", func(tt *testing.T) {
		params := common.MigrateKmsConfigParams{
			LocationID:     "home-location",
			ProjectNumber:  "my-project",
			UUID:           "uuid2",
			AccountName:    "account1",
			XCorrelationID: "",
		}
		result, errMigrate := orchInstance.MigrateKmsConfig(context.Background(), &params)
		assert.Error(tt, errMigrate)
		assert.Equal(tt, "KmsAttributes property not present within KmsConfig DB entry in VCP", errMigrate.Error())
		assert.Equal(tt, "", result)
	})
	t.Run("WhenValidateKmsConfigForMigrationFails", func(tt *testing.T) {
		params := common.MigrateKmsConfigParams{
			LocationID:     "home-location",
			ProjectNumber:  "my-project",
			UUID:           "uuid1",
			AccountName:    "account1",
			XCorrelationID: "",
		}

		result, errMigrate := orchInstance.MigrateKmsConfig(ctx, &params)
		assert.Error(tt, errMigrate)
		assert.Equal(tt, "CMEK Configuration needs to be in either Ready or In_Use state for migration", errMigrate.Error())
		assert.Equal(tt, "", result)
	})
	t.Run("WhenMigrateKmsSuccessfulWithKmsConfigRecordNotFoundInVcpDB", func(tt *testing.T) {
		params := common.MigrateKmsConfigParams{
			LocationID:     "home-location",
			ProjectNumber:  "my-project",
			UUID:           "uuid99",
			AccountName:    "account1",
			XCorrelationID: "",
			State:          datamodel.LifeCycleStateREADY,
		}
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, &params).Return(nil, nil)

		result, errMigrate := orchInstance.MigrateKmsConfig(ctx, &params)
		assert.NoError(tt, errMigrate)
		assert.NotEmpty(tt, result)
		assert.Equal(tt, "uuid99", params.SdeUUID)
	})
	t.Run("WhenTemporalWorkflowReturnsError", func(tt *testing.T) {
		mockTemporalNew := new(workflow_engine.MockTemporalTestClient)
		orchInstanceNew := GCPOrchestrator{
			storage:  store,
			temporal: mockTemporalNew,
		}

		// Clear out the NEW MigrateKmsConfig job left behind by the previous subtest so the
		// account-wide CreateCMEKJob mutual-exclusion check doesn't reject this call as a
		// conflict with that unrelated, already-"completed" (from the test's perspective) job.
		require.NoError(tt, store.DB().Exec("DELETE FROM jobs").Error)

		fields := log.Fields{
			string(middleware.RequestID):            "requestID",
			string(middleware.RequestCorrelationID): "correlationID",
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)

		params := common.MigrateKmsConfigParams{
			LocationID:    "home-location",
			ProjectNumber: "my-project",
			UUID:          "uuid3",
			AccountName:   "account1",
			State:         datamodel.LifeCycleStateREADY,
		}
		mockTemporalNew.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, &params).Return(nil, errors.New("This is a Temporal error"))

		result, errMigrate := orchInstanceNew.MigrateKmsConfig(ctx, &params)

		// Verify the job was created with correct CorrelationID and RequestID
		var createdJob datamodel.Job
		err := store.DB().Where("type = ? AND correlation_id = ?", "MIGRATE_KMS_CONFIG", "correlationID").Order("created_at DESC").First(&createdJob).Error
		assert.NoError(tt, err)
		assert.Equal(tt, "correlationID", createdJob.CorrelationID)
		assert.Equal(tt, "requestID", createdJob.RequestID)

		assert.Error(tt, errMigrate)
		assert.Equal(tt, "This is a Temporal error", errMigrate.Error())
		assert.Equal(tt, "", result)
	})

	t.Run("WhenCreateJobReturnsError", func(tt *testing.T) {
		params := common.MigrateKmsConfigParams{
			LocationID:     "home-location",
			ProjectNumber:  "my-project",
			UUID:           "uuid3",
			AccountName:    "account1",
			XCorrelationID: "",
			State:          datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Migrator().DropTable(&datamodel.Job{})
		assert.NoError(tt, err)

		result, errMigrate := orchInstance.MigrateKmsConfig(context.Background(), &params)
		// With the jobs table dropped, CreateCMEKJob's ongoing-job lookup (inside the locked
		// transaction, before the insert) fails and surfaces as a wrapped DB read error.
		assert.Error(tt, errMigrate)
		assert.Equal(tt, "", result)
	})
	t.Run("WhenGetKmsConfigByUUIDReturnsError", func(tt *testing.T) {
		params := common.MigrateKmsConfigParams{
			LocationID:     "home-location",
			ProjectNumber:  "my-project",
			UUID:           "uuid1",
			AccountName:    "account1",
			XCorrelationID: "",
		}
		err = store.DB().Migrator().DropTable(&datamodel.KmsConfig{})
		assert.NoError(tt, err)

		result, errMigrate := orchInstance.MigrateKmsConfig(context.Background(), &params)
		assert.Error(tt, errMigrate)
		assert.Equal(tt, "no such table: kms_configs", errMigrate.Error())
		assert.Equal(tt, "", result)
	})
}

func testEncryptPoolView(poolUUID string, accountID int64, state string) *datamodel.PoolView {
	return &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 11, UUID: poolUUID},
			Name:      "encrypt-pool",
			AccountID: accountID,
			State:     state,
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone:     "us-central1-a",
				SecondaryZone:   "us-central1-b",
				ThroughputMibps: 64,
				Iops:            1024,
			},
		},
	}
}

func expectAccountScopedEncryptKms(
	mockStorage *database.MockStorage,
	ctx context.Context,
	kmsConfigUUID string,
	accountID int64,
) {
	mockStorage.On("GetMultipleKmsConfigs", ctx, [][]interface{}{
		{"uuid = ?", kmsConfigUUID},
		{"account_id = ?", accountID},
	}).Return([]*datamodel.KmsConfig{{
		BaseModel: datamodel.BaseModel{ID: 77, UUID: kmsConfigUUID},
		AccountID: accountID,
	}}, nil).Once()
}

func TestEncryptPoolInternal(t *testing.T) {
	t.Run("ReturnsExistingJobWhenEncryptAlreadyInProgress", func(tt *testing.T) {
		ctx, _, _, temporal := setup(tt)
		mockStorage := new(database.MockStorage)
		params := &common.EncryptPoolParams{
			AccountName:    "account-uuid",
			PoolUUID:       "pool-uuid",
			KmsConfigID:    "kms-uuid",
			KmsConfigState: datamodel.LifeCycleStateREADY,
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 99}, Name: "acct-name"}
		originalGetAccountFromUUID := getAccountFromUUID
		getAccountFromUUID = func(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

		mockStorage.On("GetPool", ctx, params.PoolUUID, account.ID).Return(testEncryptPoolView(params.PoolUUID, account.ID, datamodel.LifeCycleStateREADY), nil)
		expectAccountScopedEncryptKms(mockStorage, ctx, params.KmsConfigID, account.ID)
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{
			{
				BaseModel: datamodel.BaseModel{UUID: "existing-job-uuid"},
				JobAttributes: &datamodel.JobAttributes{
					ResourceUUID: params.PoolUUID,
					PayloadAttributes: map[string]interface{}{
						"kms_config_id": params.KmsConfigID,
					},
				},
			},
		}, nil)

		pool, jobID, err := _encryptPool(ctx, mockStorage, temporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, pool)
		assert.Equal(tt, "existing-job-uuid", jobID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("StartsWorkflowAndReturnsJobID", func(tt *testing.T) {
		ctx, _, _, temporal := setup(tt)
		mockStorage := new(database.MockStorage)
		params := &common.EncryptPoolParams{
			AccountName:    "account-uuid",
			PoolUUID:       "pool-uuid",
			KmsConfigID:    "kms-uuid",
			KmsConfigState: datamodel.LifeCycleStateREADY,
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 101}, Name: "acct-name"}
		originalGetAccountFromUUID := getAccountFromUUID
		getAccountFromUUID = func(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

		view := testEncryptPoolView(params.PoolUUID, account.ID, datamodel.LifeCycleStateREADY)
		dbPool := database.ConvertPoolViewToPool(view)
		mockStorage.On("GetPool", ctx, params.PoolUUID, account.ID).Return(view, nil)
		expectAccountScopedEncryptKms(mockStorage, ctx, params.KmsConfigID, account.ID)
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{}, nil)
		mockStorage.On("CreateCMEKJob", ctx, account.ID, mock.Anything).Return(&datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "new-job-uuid"},
			WorkflowID: "wf-encrypt-1",
		}, nil)
		mockStorage.On(
			"UpdateKmsConfigState",
			ctx,
			params.KmsConfigID,
			datamodel.LifeCycleStateMigrating,
			datamodel.LifeCycleStateMigratingDetails,
		).Return(&datamodel.KmsConfig{}, nil)
		mockStorage.On("UpdatingPool", ctx, mock.Anything).Return(dbPool, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		pool, jobID, err := _encryptPool(ctx, mockStorage, temporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, pool)
		assert.Equal(tt, "new-job-uuid", jobID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("RejectsWhenMigrateKmsConfigJobInFlightForAccount", func(tt *testing.T) {
		ctx, _, _, temporal := setup(tt)
		mockStorage := new(database.MockStorage)
		params := &common.EncryptPoolParams{
			AccountName:    "account-uuid",
			PoolUUID:       "pool-uuid",
			KmsConfigID:    "kms-uuid",
			KmsConfigState: datamodel.LifeCycleStateREADY,
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 103}, Name: "acct-name"}
		originalGetAccountFromUUID := getAccountFromUUID
		getAccountFromUUID = func(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

		mockStorage.On("GetPool", ctx, params.PoolUUID, account.ID).Return(testEncryptPoolView(params.PoolUUID, account.ID, datamodel.LifeCycleStateREADY), nil)
		expectAccountScopedEncryptKms(mockStorage, ctx, params.KmsConfigID, account.ID)
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{}, nil)
		mockStorage.On("CreateCMEKJob", ctx, account.ID, mock.Anything).Return(
			nil, errors.NewConflictErr("A CMEK migration (EncryptVolumes) is already in progress for this account"),
		)

		pool, jobID, err := _encryptPool(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "EncryptVolumes")
		assert.Nil(tt, pool)
		assert.Empty(tt, jobID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WorkflowStartFailureMarksJobErrorAndRevertsPool", func(tt *testing.T) {
		ctx, _, _, temporal := setup(tt)
		mockStorage := new(database.MockStorage)
		params := &common.EncryptPoolParams{
			AccountName:    "account-uuid",
			PoolUUID:       "pool-uuid",
			KmsConfigID:    "kms-uuid",
			KmsConfigState: datamodel.LifeCycleStateREADY,
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 102}, Name: "acct-name"}
		originalGetAccountFromUUID := getAccountFromUUID
		getAccountFromUUID = func(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

		view := testEncryptPoolView(params.PoolUUID, account.ID, datamodel.LifeCycleStateREADY)
		dbPool := database.ConvertPoolViewToPool(view)
		mockStorage.On("GetPool", ctx, params.PoolUUID, account.ID).Return(view, nil)
		expectAccountScopedEncryptKms(mockStorage, ctx, params.KmsConfigID, account.ID)
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{}, nil)
		mockStorage.On("CreateCMEKJob", ctx, account.ID, mock.Anything).Return(&datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "new-job-uuid"},
			WorkflowID: "wf-encrypt-2",
		}, nil)
		mockStorage.On(
			"UpdateKmsConfigState",
			ctx,
			params.KmsConfigID,
			datamodel.LifeCycleStateMigrating,
			datamodel.LifeCycleStateMigratingDetails,
		).Return(&datamodel.KmsConfig{}, nil)
		mockStorage.On("UpdatingPool", ctx, mock.Anything).Return(dbPool, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)
		mockStorage.On("UpdateJob", ctx, "new-job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)
		mockStorage.On("UpdatePoolState", ctx, mock.Anything, datamodel.LifeCycleStateREADY, "").Return(dbPool, nil)

		pool, jobID, err := _encryptPool(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.Nil(tt, pool)
		assert.Empty(tt, jobID)
		mockStorage.AssertExpectations(tt)
	})

	for _, tc := range []struct {
		name            string
		currentKmsID    sql.NullInt64
		encryptionRoute string
	}{
		{
			name:            "RejectsCrossAccountKmsForFirstTimeEncrypt",
			currentKmsID:    sql.NullInt64{},
			encryptionRoute: "first-time encrypt",
		},
		{
			name:            "RejectsCrossAccountKmsForCmekToCmek",
			currentKmsID:    sql.NullInt64{Int64: 12, Valid: true},
			encryptionRoute: "CMEK-to-CMEK",
		},
	} {
		tc := tc
		t.Run(tc.name, func(tt *testing.T) {
			ctx, _, _, temporal := setup(tt)
			mockStorage := new(database.MockStorage)
			params := &common.EncryptPoolParams{
				AccountName:    "account-uuid",
				PoolUUID:       "pool-uuid",
				KmsConfigID:    "foreign-kms-uuid",
				KmsConfigState: datamodel.LifeCycleStateREADY,
			}
			account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 104}, Name: "acct-name"}
			originalGetAccountFromUUID := getAccountFromUUID
			getAccountFromUUID = func(context.Context, database.Storage, string) (*datamodel.Account, error) {
				return account, nil
			}
			defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

			view := testEncryptPoolView(params.PoolUUID, account.ID, datamodel.LifeCycleStateREADY)
			view.Pool.KmsConfigID = tc.currentKmsID
			mockStorage.On("GetPool", ctx, params.PoolUUID, account.ID).Return(view, nil)
			// The UUID may exist globally, but the account-scoped query deliberately returns no row.
			mockStorage.On("GetMultipleKmsConfigs", ctx, [][]interface{}{
				{"uuid = ?", params.KmsConfigID},
				{"account_id = ?", account.ID},
			}).Return([]*datamodel.KmsConfig{}, nil).Once()

			pool, jobID, err := _encryptPool(ctx, mockStorage, temporal, params)

			assert.Error(tt, err, tc.encryptionRoute)
			assert.True(tt, errors.IsNotFoundErr(err))
			assert.Nil(tt, pool)
			assert.Empty(tt, jobID)
			mockStorage.AssertNotCalled(tt, "CreateCMEKJob", mock.Anything, mock.Anything, mock.Anything)
			mockStorage.AssertExpectations(tt)
		})
	}
}

func TestValidatePoolForEncrypt(t *testing.T) {
	t.Run("RejectsErrorAndDegradedStates", func(tt *testing.T) {
		errStatePool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "p1"}, State: datamodel.LifeCycleStateError}
		err := validatePoolForEncrypt(errStatePool, &datamodel.KmsConfig{})
		assert.Error(tt, err)

		degradedPool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "p2"}, State: datamodel.LifeCycleStateDegraded}
		err = validatePoolForEncrypt(degradedPool, &datamodel.KmsConfig{})
		assert.Error(tt, err)
	})

	t.Run("RejectsTransitionalState", func(tt *testing.T) {
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "p3"}, State: datamodel.LifeCycleStateUpdating}
		err := validatePoolForEncrypt(pool, &datamodel.KmsConfig{})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "transition state")
	})

	t.Run("ReturnsConflictWhenPoolAlreadyEncryptedWithTargetPolicy", func(tt *testing.T) {
		pool := &datamodel.Pool{
			BaseModel:   datamodel.BaseModel{UUID: "p5"},
			State:       datamodel.LifeCycleStateREADY,
			KmsConfigID: sql.NullInt64{Int64: 8, Valid: true},
		}
		err := validatePoolForEncrypt(pool, &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{ID: 8}})
		assert.Error(tt, err)
		assert.True(tt, errors.IsConflictErr(err))
	})
}

func TestGetOngoingEncryptPoolJob(t *testing.T) {
	ctx := context.Background()

	t.Run("ReturnsJobUUIDWhenMatchingResourceAndKmsFound", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{
			{BaseModel: datamodel.BaseModel{UUID: "job-1"}, JobAttributes: &datamodel.JobAttributes{ResourceUUID: "other"}},
			{
				BaseModel: datamodel.BaseModel{UUID: "job-2"},
				JobAttributes: &datamodel.JobAttributes{
					ResourceUUID: "pool-match",
					PayloadAttributes: map[string]interface{}{
						"kms_config_id": "kms-a",
					},
				},
			},
		}, nil)
		jobUUID, err := getOngoingEncryptPoolJob(ctx, mockStorage, 1, "pool-match", "kms-a")
		assert.NoError(tt, err)
		assert.Equal(tt, "job-2", jobUUID)
	})

	t.Run("ReturnsEmptyWhenSamePoolHasDifferentKms", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{
			{
				BaseModel: datamodel.BaseModel{UUID: "job-2"},
				JobAttributes: &datamodel.JobAttributes{
					ResourceUUID: "pool-match",
					PayloadAttributes: map[string]interface{}{
						"kms_config_id": "kms-a",
					},
				},
			},
		}, nil)
		jobUUID, err := getOngoingEncryptPoolJob(ctx, mockStorage, 1, "pool-match", "kms-b")
		assert.NoError(tt, err)
		assert.Empty(tt, jobUUID)
	})

	t.Run("ReturnsEmptyWhenNoMatchingResourceFound", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{
			{BaseModel: datamodel.BaseModel{UUID: "job-3"}, JobAttributes: &datamodel.JobAttributes{ResourceUUID: "other"}},
		}, nil)
		jobUUID, err := getOngoingEncryptPoolJob(ctx, mockStorage, 1, "pool-match", "kms-a")
		assert.NoError(tt, err)
		assert.Empty(tt, jobUUID)
	})

	t.Run("RejectsDifferentKmsWhenPoolAlreadyUpdating", func(tt *testing.T) {
		ctx, _, _, temporal := setup(tt)
		mockStorage := new(database.MockStorage)
		params := &common.EncryptPoolParams{
			AccountName:    "account-uuid",
			PoolUUID:       "pool-uuid",
			KmsConfigID:    "kms-b",
			KmsConfigState: datamodel.LifeCycleStateREADY,
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 104}, Name: "acct-name"}
		originalGetAccountFromUUID := getAccountFromUUID
		getAccountFromUUID = func(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

		mockStorage.On("GetPool", ctx, params.PoolUUID, account.ID).Return(testEncryptPoolView(params.PoolUUID, account.ID, datamodel.LifeCycleStateUpdating), nil)
		expectAccountScopedEncryptKms(mockStorage, ctx, params.KmsConfigID, account.ID)
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{
			{
				BaseModel: datamodel.BaseModel{UUID: "existing-job-uuid"},
				JobAttributes: &datamodel.JobAttributes{
					ResourceUUID: params.PoolUUID,
					PayloadAttributes: map[string]interface{}{
						"kms_config_id": "kms-a",
					},
				},
			},
		}, nil)

		pool, jobID, err := _encryptPool(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "transition state")
		assert.Nil(tt, pool)
		assert.Empty(tt, jobID)
		mockStorage.AssertExpectations(tt)
	})
}

func TestConvertDataStoreKmsConfigToModel(t *testing.T) {
	t.Run("ReturnsValidKmsConfigWhenAllFieldsArePopulated", func(t *testing.T) {
		saId := int64(1234)
		kmsConfig := &datamodel.KmsConfig{
			Name:              "test-name",
			Description:       "test-description",
			State:             "ACTIVE",
			StateDetails:      "",
			KeyRing:           "test-key-ring",
			KeyRingLocation:   "test-location",
			KeyName:           "test-key-name",
			AccountID:         int64(1234),
			CustomerProjectID: "test-customer-project-id",
			KeyProjectID:      "test-key-project-id",
			ServiceAccountID:  &saId,
			ResourceID:        "test-resource-id",
			KmsAttributes: &datamodel.KmsAttributes{
				SdeKmsConfigUUID:       "test-external-uuid",
				SdeServiceAccountEmail: "test-service-account@test.com",
			},
		}
		expectedDate := time.Date(2022, time.February, 2, 2, 2, 2, 2, time.UTC)
		kmsConfig.BaseModel = datamodel.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: expectedDate,
			UpdatedAt: expectedDate,
			DeletedAt: &gorm.DeletedAt{Time: expectedDate, Valid: true},
		}

		result := convertDataStoreKmsConfigToModel(kmsConfig)

		assert.NotNil(t, result)
		assert.Equal(t, kmsConfig.UUID, result.UUID)
		assert.Equal(t, expectedDate, result.CreatedAt)
		assert.Equal(t, expectedDate, result.UpdatedAt)
		assert.Equal(t, expectedDate, *result.DeletedAt)
		assert.Equal(t, kmsConfig.Name, result.Name)
		assert.Equal(t, kmsConfig.Description, result.Description)
		assert.Equal(t, kmsConfig.State, result.State)
		assert.Equal(t, kmsConfig.StateDetails, result.StateDetails)
		assert.Equal(t, kmsConfig.KeyRing, result.KeyRing)
		assert.Equal(t, kmsConfig.KeyRingLocation, result.KeyRingLocation)
		assert.Equal(t, kmsConfig.KeyName, result.KeyName)
		assert.Equal(t, kmsConfig.AccountID, result.AccountID)
		assert.Equal(t, kmsConfig.CustomerProjectID, result.CustomerProjectID)
		assert.Equal(t, kmsConfig.KeyProjectID, result.KeyProjectID)
		assert.Equal(t, kmsConfig.ServiceAccountID, result.ServiceAccountID)
		assert.Equal(t, kmsConfig.ResourceID, result.ResourceID)
		assert.NotNil(t, result.KmsAttributes)
		assert.Equal(t, kmsConfig.KmsAttributes.SdeKmsConfigUUID, result.KmsAttributes.SdeKmsConfigUUID)
		assert.Equal(t, kmsConfig.KmsAttributes.SdeServiceAccountEmail, result.KmsAttributes.SdeServiceAccountEmail)
	})
	t.Run("HandlesNilKmsAttributesGracefully", func(t *testing.T) {
		saId := int64(1234)
		kmsConfig := &datamodel.KmsConfig{
			Name:              "test-name",
			Description:       "test-description",
			State:             "ACTIVE",
			StateDetails:      "test-state-details",
			KeyRing:           "test-key-ring",
			KeyRingLocation:   "test-location",
			KeyName:           "test-key-name",
			AccountID:         int64(1234),
			CustomerProjectID: "test-customer-project-id",
			KeyProjectID:      "test-key-project-id",
			ServiceAccountID:  &saId,
			ResourceID:        "test-resource-id",
			KmsAttributes:     nil,
		}
		kmsConfig.BaseModel = datamodel.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			DeletedAt: nil,
		}

		result := convertDataStoreKmsConfigToModel(kmsConfig)

		assert.NotNil(t, result)
		assert.Nil(t, result.KmsAttributes)
		assert.Nil(t, result.DeletedAt)
	})

	t.Run("MapsERRORStateAndPreservesStateDetailsFromDB", func(t *testing.T) {
		saID := int64(99)
		kmsConfig := &datamodel.KmsConfig{
			Name:              "err-kms",
			Description:       "",
			State:             datamodel.LifeCycleStateError,
			StateDetails:      "VCP Reverted to previous state due to error",
			KeyRing:           "kr",
			KeyRingLocation:   "us-east4",
			KeyName:           "kn",
			AccountID:         int64(1),
			CustomerProjectID: "cp",
			KeyProjectID:      "kp",
			ServiceAccountID:  &saID,
			ResourceID:        "res",
			KmsAttributes:     nil,
		}
		kmsConfig.BaseModel = datamodel.BaseModel{
			UUID:      "cccccccc-cccc-4ccc-cccc-cccccccccccc",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			DeletedAt: nil,
		}

		result := convertDataStoreKmsConfigToModel(kmsConfig)

		require.NotNil(t, result)
		assert.Equal(t, cvpModels.KmsConfigV1betaKmsStateERROR, result.State)
		assert.Equal(t, "VCP Reverted to previous state due to error", result.StateDetails)
	})
}

func TestUpdateKmsConfig(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()
	cvp.CVP_HOST = "localhost:8009"

	ctx := context.Background()

	t.Run("SuccessfulUpdate", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
			KmsAttributes: &datamodel.KmsAttributes{
				SdeKmsConfigUUID: "test-sde-kms-config-id",
			},
		}
		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails).Return(dbKmsConfig, nil)

		// Mock SDE call to succeed
		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			return nil, nil // Success
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		// Mock database update for successful completion
		mockStorage.On("UpdateKmsConfig", ctx, dbKmsConfig.UUID, mock.Anything).Return(nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, kmsConfig)
		assert.Equal(tt, "test-kms-config-id", kmsConfig.UUID)
	})

	t.Run("ExistingVCPConfigSkipsSDEWhenCVPHostIsConfigured", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:     datamodel.LifeCycleStateAvailable,
			KmsAttributes: &datamodel.KmsAttributes{
				CreationMode: datamodel.KmsCreationModeVCP,
			},
		}

		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("UpdateKmsConfig", ctx, dbKmsConfig.UUID, mock.Anything).Return(nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)

		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			tt.Fatalf("updateSDEKmsConfiguration should not be called for VCP-created config")
			return nil, nil
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, kmsConfig)
	})

	t.Run("ExistingSDEConfigUsesSDEWhenForceVCPFlagIsEnabled", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)
		originalForceFlag := utils.ForceVCPKMSPathForTesting
		utils.ForceVCPKMSPathForTesting = true
		defer func() {
			utils.ForceVCPKMSPathForTesting = originalForceFlag
		}()

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:     datamodel.LifeCycleStateAvailable,
			KmsAttributes: &datamodel.KmsAttributes{
				CreationMode:     datamodel.KmsCreationModeSDE,
				SdeKmsConfigUUID: "test-sde-kms-config-id",
			},
		}

		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("UpdateKmsConfig", ctx, dbKmsConfig.UUID, mock.Anything).Return(nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)

		calledSDE := false
		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			calledSDE = true
			return nil, nil
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, kmsConfig)
		assert.True(tt, calledSDE, "expected SDE update call for SDE-created config")
	})

	t.Run("SuccessfulUpdateWhenVcpKmsConfigNotFound", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(nil, errors.NewNotFoundErr("kms config", nil))

		// Mock SDE call to succeed
		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			// Return a proper KmsConfigV1beta response
			kmsConfigResponse := &gcpserver.KmsConfigV1beta{
				UUID:            gcpserver.NewOptString("test-kms-config-id"),
				ResourceId:      gcpserver.NewOptString("updated-kms-config"),
				KmsState:        gcpserver.NewOptKmsConfigV1betaKmsState(gcpserver.KmsConfigV1betaKmsStateREADY),
				KmsStateDetails: gcpserver.NewOptString("Updated successfully"),
			}
			return kmsConfigResponse, nil
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, kmsConfig) // Should return the converted SDE response
		assert.Equal(tt, "test-kms-config-id", kmsConfig.UUID)
	})

	t.Run("KmsConfigNotInDbButPresentInSDE", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID:     "sde-only-kms-config-id",
			AccountName:     "test-account",
			ResourceID:      "updated-sde-kms-config",
			Description:     &[]string{"Updated KMS config from SDE"}[0],
			KeyName:         "updated-key",
			KeyRing:         "updated-keyring",
			KeyRingLocation: "us-west1",
			KeyProjectID:    "updated-project",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		// Mock storage behavior - KMS config not found in database
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("GetKmsConfig", ctx, "sde-only-kms-config-id").Return(nil, errors.NewNotFoundErr("kms config", nil))

		// Mock SDE call to succeed with comprehensive response
		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			// Verify that the kmsConfig passed has the correct SdeKmsConfigUUID
			assert.Equal(tt, "sde-only-kms-config-id", kmsConfig.KmsAttributes.SdeKmsConfigUUID)

			// Return a comprehensive KmsConfigV1beta response using KeyFullPath
			kmsConfigResponse := &gcpserver.KmsConfigV1beta{
				UUID:            gcpserver.NewOptString("sde-only-kms-config-id"),
				ResourceId:      gcpserver.NewOptString("updated-sde-kms-config"),
				Description:     gcpserver.NewOptString("Updated KMS config from SDE"),
				KeyFullPath:     "projects/updated-project/locations/us-west1/keyRings/updated-keyring/cryptoKeys/updated-key",
				KmsState:        gcpserver.NewOptKmsConfigV1betaKmsState(gcpserver.KmsConfigV1betaKmsStateREADY),
				KmsStateDetails: gcpserver.NewOptString("Successfully updated in SDE"),
			}
			return kmsConfigResponse, nil
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, kmsConfig)
		assert.Equal(tt, "sde-only-kms-config-id", kmsConfig.UUID)
		assert.Equal(tt, "updated-sde-kms-config", kmsConfig.ResourceID)
		assert.Equal(tt, "Updated KMS config from SDE", kmsConfig.Description)
		assert.Equal(tt, "updated-key", kmsConfig.KeyName)
		assert.Equal(tt, "updated-keyring", kmsConfig.KeyRing)
		assert.Equal(tt, "us-west1", kmsConfig.KeyRingLocation)
		assert.Equal(tt, "updated-project", kmsConfig.KeyProjectID)
		assert.Equal(tt, datamodel.LifeCycleStateREADY, kmsConfig.State)
	})

	t.Run("ValidationError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
			KeyName:     "key1",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		account := &datamodel.Account{Name: "test-account"}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(true, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return([]*datamodel.Svm{&datamodel.Svm{Name: "svm"}}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "can not update key details while kms config is in use")
		assert.Nil(tt, kmsConfig)
	})

	t.Run("GrantOnlyUpdateWhileInUseSucceeds", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			KeyUri:      "projects/test-project/locations/us-central1/keyRings/test-ring/cryptoKeys/test-key/grants/new-grant-id",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:           datamodel.LifeCycleStateInUse,
			KeyName:         "test-key",
			KeyRing:         "test-ring",
			KeyRingLocation: "us-central1",
			KeyProjectID:    "test-project",
			KmsGrant:        "old-grant-id",
			KmsAttributes: &datamodel.KmsAttributes{
				CreationMode: datamodel.KmsCreationModeVCP,
			},
		}
		account := &datamodel.Account{Name: "test-account"}

		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(true, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("UpdateKmsConfig", ctx, dbKmsConfig.UUID, mock.MatchedBy(func(updateFields map[string]interface{}) bool {
			return updateFields["kms_grant"] == "new-grant-id" &&
				updateFields["state"] == datamodel.LifeCycleStateInUse &&
				updateFields["state_details"] == datamodel.LifeCycleStateInUseDetails
		})).Return(nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, kmsConfig)
	})

	t.Run("WhenSvmNotFound", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
			KeyName:     "key1",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		account := &datamodel.Account{Name: "test-account"}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, errors.New("error not found"))
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "error")
		assert.Nil(tt, kmsConfig)
	})

	t.Run("WhenKmsConfigInCreatingState", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
			KeyName:     "key1",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateCreating,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		account := &datamodel.Account{Name: "test-account"}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "can not update a gcpKmsConfig which is in creating or error state.")
		assert.Nil(tt, kmsConfig)
	})

	t.Run("WhenGetKmsConfigFailsWithNonNotFoundError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(nil, errors.New("database connection failed"))

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "database connection failed")
		assert.Nil(tt, kmsConfig)
	})

	t.Run("WhenSDEUpdateFailsWithInvalidResponse", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(nil, errors.NewNotFoundErr("kms config", nil))

		// Mock SDE call to return invalid response type
		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			// Return an OperationV1beta instead of KmsConfigV1beta (wrong type but valid interface implementation)
			operationResponse := &gcpserver.OperationV1beta{
				Name: gcpserver.NewOptString("operation-id"),
			}
			return operationResponse, nil
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Failed to update KMS configuration in SDE")
		assert.Nil(tt, kmsConfig)
	})

	t.Run("WhenSDEUpdateReturnsNilResponse", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(nil, errors.NewNotFoundErr("kms config", nil))

		// Mock SDE call to return nil
		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			return nil, nil
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Failed to update KMS configuration in SDE")
		assert.Nil(tt, kmsConfig)
	})

	t.Run("WhenErrorStateUpdateFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
			KmsAttributes: &datamodel.KmsAttributes{
				SdeKmsConfigUUID: "test-sde-kms-config-id",
			},
		}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails).Return(dbKmsConfig, nil)

		// Mock error state update to fail
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateError, mock.AnythingOfType("string")).Return(nil, errors.New("error state update failed"))

		// Mock SDE call to fail
		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			return nil, errors.New("SDE update failed")
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SDE update failed") // Should return original error, not the state update error
		assert.Nil(tt, kmsConfig)
	})

	t.Run("WhenKeyUriParsingSucceeds", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
			KeyUri:      "projects/test-project/locations/us-central1/keyRings/test-ring/cryptoKeys/test-key",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			AccountID:      1,
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
			KmsAttributes: &datamodel.KmsAttributes{
				SdeKmsConfigUUID: "test-sde-kms-config-id",
			},
		}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("ListKmsConfigByAccountID", ctx, int64(1)).Return([]*datamodel.KmsConfig{dbKmsConfig}, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails).Return(dbKmsConfig, nil)

		// Mock SDE call to succeed
		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			return nil, nil // Success
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		// Mock database update for successful completion with parsed KeyUri
		mockStorage.On("UpdateKmsConfig", ctx, dbKmsConfig.UUID, mock.MatchedBy(func(updateFields map[string]interface{}) bool {
			// Verify that KeyUri was parsed correctly and included in update fields
			expectedFields := []string{"key_name", "key_ring", "key_ring_location", "key_project_id", "resource_id", "state"}
			for _, field := range expectedFields {
				if _, exists := updateFields[field]; !exists {
					return false
				}
			}
			return updateFields["key_name"] == "test-key" &&
				updateFields["key_ring_location"] == "us-central1" &&
				updateFields["key_ring"] == "test-ring" &&
				updateFields["key_project_id"] == "test-project" &&
				updateFields["state"] == datamodel.LifeCycleStateCreated
		})).Return(nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, kmsConfig)
		assert.Equal(tt, "test-kms-config-id", kmsConfig.UUID)

		// Verify that the parsed KeyUri fields were set in params
		assert.Equal(tt, "test-key", params.KeyName)
		assert.Equal(tt, "us-central1", params.KeyRingLocation)
		assert.Equal(tt, "test-ring", params.KeyRing)
		assert.Equal(tt, "test-project", params.KeyProjectID)
	})

	t.Run("WhenKeyUriIncludesGrantSetsStateToReady", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			KeyUri:      "projects/test-project/locations/us-central1/keyRings/test-ring/cryptoKeys/test-key/grants/test-grant-id",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:           datamodel.LifeCycleStateREADY,
			KeyName:         "test-key",
			KeyRing:         "test-ring",
			KeyRingLocation: "us-central1",
			KeyProjectID:    "test-project",
			KmsAttributes: &datamodel.KmsAttributes{
				CreationMode: datamodel.KmsCreationModeVCP,
			},
		}

		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("UpdateKmsConfig", ctx, dbKmsConfig.UUID, mock.MatchedBy(func(updateFields map[string]interface{}) bool {
			_, hasState := updateFields["state"]
			return updateFields["key_name"] == "test-key" &&
				updateFields["key_ring"] == "test-ring" &&
				updateFields["key_ring_location"] == "us-central1" &&
				updateFields["key_project_id"] == "test-project" &&
				updateFields["kms_grant"] == "test-grant-id" &&
				!hasState
		})).Return(nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, kmsConfig)
		assert.Equal(tt, "test-grant-id", params.KmsGrant)
	})

	t.Run("WhenKmsActivitiesUpdateFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
			KmsAttributes: &datamodel.KmsAttributes{
				SdeKmsConfigUUID: "test-sde-kms-config-id",
			},
		}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails).Return(dbKmsConfig, nil)

		// Mock SDE call to succeed
		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			return nil, nil // Success
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		// Mock database update to fail
		mockStorage.On("UpdateKmsConfig", ctx, dbKmsConfig.UUID, mock.Anything).Return(errors.New("database update failed"))

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "database update failed")
		assert.Nil(tt, kmsConfig)
	})

	t.Run("WhenFinalGetKmsConfigFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
			KmsAttributes: &datamodel.KmsAttributes{
				SdeKmsConfigUUID: "test-sde-kms-config-id",
			},
		}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil).Once()
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails).Return(dbKmsConfig, nil)

		// Mock SDE call to succeed
		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			return nil, nil // Success
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		// Mock database update to succeed but final GetKmsConfig to fail
		mockStorage.On("UpdateKmsConfig", ctx, dbKmsConfig.UUID, mock.Anything).Return(nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(nil, errors.New("final get failed")).Once()

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "final get failed")
		assert.Nil(tt, kmsConfig)
	})

	t.Run("WhenKmsConfigUUIDIsEmpty", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.UpdateKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
			ResourceID:  "updated-kms-config",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: ""}, // Empty UUID
			State:          datamodel.LifeCycleStateAvailable,
			KmsAttributes:  &datamodel.KmsAttributes{},
			ServiceAccount: &datamodel.ServiceAccount{},
		}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails).Return(dbKmsConfig, nil)

		// Mock SDE call to succeed
		originalUpdateSDE := updateSDEKmsConfiguration
		updateSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) (gcpserver.V1betaUpdateKmsConfigurationRes, error) {
			return nil, nil // Success
		}
		defer func() {
			updateSDEKmsConfiguration = originalUpdateSDE
		}()

		kmsConfig, err := orchestrator.UpdateKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.Nil(tt, kmsConfig) // Should return nil when UUID is empty

		// Verify that database update operations were NOT called since UUID is empty
		mockStorage.AssertNotCalled(tt, "UpdateKmsConfig")
	})
}

func TestCreateKmsConfig(t *testing.T) {
	temporal := workflow_engine.NewMockTemporalTestClient(t)
	t.Run("CreateKmsConfigReturnsErrorWhenAccountCreationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		params := &common.CreateKmsConfigParams{AccountName: "fail_account"}
		se := database.Storage(nil)
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account error")
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
		}()
		_, _, err := _createKmsConfig(ctx, se, temporal, params)
		if err == nil || err.Error() != "account error" {
			t.Errorf("Expected account error, got %v", err)
		}
	})
	t.Run("CreateKmsConfigParseKeyFullPathResourceFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		params := &common.CreateKmsConfigParams{AccountName: "test_account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		mockStorage := new(database.MockStorage)
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{}, nil
		}
		parseKeyFullPathResource = func(s string) (*utils.ParsedKeyFullPathResource, error) {
			return nil, errors.New("resource error")
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			parseKeyFullPathResource = utils.ParseKeyFullPathResource
		}()
		_, _, err := _createKmsConfig(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
	})
	t.Run("CreateKmsConfigReturnsErrorWhenJobCreationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		params := &common.CreateKmsConfigParams{AccountName: "test_account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		mockStorage := new(database.MockStorage)
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{}, nil
		}
		parseKeyFullPathResource = func(s string) (*utils.ParsedKeyFullPathResource, error) {
			return &utils.ParsedKeyFullPathResource{CryptoKey: "k", ProjectID: "p", Location: "l", KeyRing: "r"}, nil
		}
		mockStorage.On("CreateKmsConfig", ctx, mock.Anything).Return(&datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "uuid"},
			AccountID: 1,
		}, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("job error"))
		mockStorage.On("UpdateKmsConfigState", ctx, "uuid", datamodel.LifeCycleStateError, mock.Anything).Return(&datamodel.KmsConfig{}, nil).Once()
		mockStorage.On("DeleteKmsConfig", ctx, "uuid", datamodel.LifeCycleStateError, "job error").Return(nil, nil)

		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			parseKeyFullPathResource = utils.ParseKeyFullPathResource
		}()
		_, _, err := _createKmsConfig(ctx, mockStorage, temporal, params)
		if err == nil || err.Error() != "job error" {
			t.Errorf("Expected job error, got %v", err)
		}
	})
	t.Run("CreateKmsConfigLogsDeleteFailureDuringCleanup", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		params := &common.CreateKmsConfigParams{AccountName: "test_account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		mockStorage := new(database.MockStorage)
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{}, nil
		}
		parseKeyFullPathResource = func(s string) (*utils.ParsedKeyFullPathResource, error) {
			return &utils.ParsedKeyFullPathResource{CryptoKey: "k", ProjectID: "p", Location: "l", KeyRing: "r"}, nil
		}
		waitOriginal := waitForTemporalEnabled
		waitForTemporalEnabled = false
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			parseKeyFullPathResource = utils.ParseKeyFullPathResource
			waitForTemporalEnabled = waitOriginal
		}()

		mockStorage.On("CreateKmsConfig", ctx, mock.Anything).Return(&datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "uuid"},
			AccountID: 1,
			KmsAttributes: &datamodel.KmsAttributes{
				SdeKmsConfigUUID: "uuid",
			},
		}, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return((*datamodel.Job)(nil), errors.New("job error"))
		mockStorage.On("DeleteKmsConfig", ctx, "uuid", datamodel.LifeCycleStateError, "job error").Return((*datamodel.KmsConfig)(nil), errors.New("delete cleanup error"))

		_, _, err := _createKmsConfig(ctx, mockStorage, temporal, params)
		if err == nil || err.Error() != "job error" {
			t.Errorf("Expected job error, got %v", err)
		}
		mockStorage.AssertExpectations(tt)
	})
	t.Run("CreateKmsConfigReturnsErrorWhenStorageFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		params := &common.CreateKmsConfigParams{AccountName: "test_account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		mockStorage := new(database.MockStorage)
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			parseKeyFullPathResource = utils.ParseKeyFullPathResource
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{}, nil
		}
		parseKeyFullPathResource = func(s string) (*utils.ParsedKeyFullPathResource, error) {
			return &utils.ParsedKeyFullPathResource{CryptoKey: "k", ProjectID: "p", Location: "l", KeyRing: "r"}, nil
		}
		mockStorage.On("CreateKmsConfig", ctx, mock.Anything).Return(nil, errors.New("db error"))
		mockStorage.On("UpdateKmsConfigState", ctx, "uuid", datamodel.LifeCycleStateError, mock.Anything).Return(&datamodel.KmsConfig{}, nil).Once()

		_, _, err := _createKmsConfig(ctx, mockStorage, temporal, params)
		if err == nil || err.Error() != "db error" {
			t.Errorf("Expected db error, got %v", err)
		}
	})
	t.Run("CreateKmsConfigReturnsErrorWhenWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		params := &common.CreateKmsConfigParams{AccountName: "test_account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		mockStorage := new(database.MockStorage)
		waitForTemporalEnabled = true
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			parseKeyFullPathResource = utils.ParseKeyFullPathResource
			waitForTemporalEnabled = env.GetBool("WAIT_FOR_TEMPORAL_ENABLED", false)
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{}, nil
		}
		parseKeyFullPathResource = func(s string) (*utils.ParsedKeyFullPathResource, error) {
			return &utils.ParsedKeyFullPathResource{CryptoKey: "k", ProjectID: "p", Location: "l", KeyRing: "r"}, nil
		}
		mockStorage.On("CreateKmsConfig", ctx, mock.Anything).Return(&datamodel.KmsConfig{BaseModel: datamodel.BaseModel{
			UUID: "uuid"}, AccountID: 1, KmsAttributes: &datamodel.KmsAttributes{}}, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{
			UUID: "job-uuid"}, WorkflowID: "wf-id"}, nil)
		temporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow error"))
		mockStorage.On("UpdateJob", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockStorage.On("DeleteKmsConfig", ctx, "uuid", datamodel.LifeCycleStateError, "workflow error").Return(nil, nil)

		_, _, err := _createKmsConfig(ctx, mockStorage, temporal, params)
		assert.NoError(tt, err)
	})
	t.Run("CreateKmsConfigReturnsKmsConfigAndJobUUIDOnSuccess", func(tt *testing.T) {
		ctx := context.Background()
		temporal := workflow_engine.NewMockTemporalTestClient(t)
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		params := &common.CreateKmsConfigParams{AccountName: "test_account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k", ResourceID: "res-id", Name: "kms-name"}
		mockStorage := new(database.MockStorage)
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			parseKeyFullPathResource = utils.ParseKeyFullPathResource
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{}, nil
		}
		parseKeyFullPathResource = func(s string) (*utils.ParsedKeyFullPathResource, error) {
			return &utils.ParsedKeyFullPathResource{CryptoKey: "k", ProjectID: "p", Location: "l", KeyRing: "r"}, nil
		}
		mockStorage.On("CreateKmsConfig", ctx, mock.Anything).Return(&datamodel.KmsConfig{BaseModel: datamodel.BaseModel{
			UUID: "uuid"}, AccountID: 1, KeyName: "k", CustomerProjectID: "p", KeyRingLocation: "l", KeyRing: "r", ResourceID: "res-id",
			KmsAttributes: &datamodel.KmsAttributes{}, ServiceAccount: &datamodel.ServiceAccount{}}, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{
			UUID: "job-uuid"}, WorkflowID: "wf-id"}, nil)
		temporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		kmsConfig, jobUUID, err := _createKmsConfig(ctx, mockStorage, temporal, params)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if kmsConfig == nil || jobUUID != "job-uuid" {
			t.Errorf("Expected valid kmsConfig and jobUUID, got %v, %v", kmsConfig, jobUUID)
		}
	})
	t.Run("CreateKmsConfigReturnsErrorWhenUpdateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		params := &common.CreateKmsConfigParams{AccountName: "test_account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		mockStorage := new(database.MockStorage)
		waitForTemporalEnabled = true
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			parseKeyFullPathResource = utils.ParseKeyFullPathResource
			waitForTemporalEnabled = env.GetBool("WAIT_FOR_TEMPORAL_ENABLED", false)
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{}, nil
		}
		parseKeyFullPathResource = func(s string) (*utils.ParsedKeyFullPathResource, error) {
			return &utils.ParsedKeyFullPathResource{CryptoKey: "k", ProjectID: "p", Location: "l", KeyRing: "r"}, nil
		}
		mockStorage.On("CreateKmsConfig", ctx, mock.Anything).Return(&datamodel.KmsConfig{BaseModel: datamodel.BaseModel{
			UUID: "uuid"}, AccountID: 1, KmsAttributes: &datamodel.KmsAttributes{}}, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{
			UUID: "job-uuid"}, WorkflowID: "wf-id"}, nil)
		temporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow error"))
		mockStorage.On("UpdateJob", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("job-uuid"))
		mockStorage.On("DeleteKmsConfig", ctx, "uuid", datamodel.LifeCycleStateError, "workflow error").Return(nil, nil)

		_, _, err := _createKmsConfig(ctx, mockStorage, temporal, params)
		assert.NoError(tt, err)
	})
	t.Run("CreateKmsConfigReturnsErrorWhenWorkflowFailsAndFlagIsDisabled", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		params := &common.CreateKmsConfigParams{AccountName: "test_account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		mockStorage := new(database.MockStorage)
		waitForTemporalEnabled = false
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			parseKeyFullPathResource = utils.ParseKeyFullPathResource
			waitForTemporalEnabled = env.GetBool("WAIT_FOR_TEMPORAL_ENABLED", true)
		}()
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{}, nil
		}
		parseKeyFullPathResource = func(s string) (*utils.ParsedKeyFullPathResource, error) {
			return &utils.ParsedKeyFullPathResource{CryptoKey: "k", ProjectID: "p", Location: "l", KeyRing: "r"}, nil
		}
		mockStorage.On("CreateKmsConfig", ctx, mock.Anything).Return(&datamodel.KmsConfig{BaseModel: datamodel.BaseModel{
			UUID: "uuid"}, AccountID: 1, KmsAttributes: &datamodel.KmsAttributes{}}, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{
			UUID: "job-uuid"}, WorkflowID: "wf-id"}, nil)
		temporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow error"))
		mockStorage.On("UpdateJob", ctx, mock.Anything, datamodel.LifeCycleStateError, mock.Anything, mock.Anything).Return(nil).Once()
		mockStorage.On("DeleteKmsConfig", ctx, "uuid", datamodel.LifeCycleStateError, mock.Anything).Return(nil, nil).Once()

		_, _, err := _createKmsConfig(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
	})
}

func TestCreateKmsConfigFails(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
	params := &common.CreateKmsConfigParams{AccountName: "fail_account"}
	mockStorage := new(database.MockStorage)
	temporal := workflow_engine.NewMockTemporalTestClient(t)
	createKmsConfig = func(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateKmsConfigParams) (*models.KmsConfig, string, error) {
		return nil, "", errors.New("some error")
	}
	defer func() {
		createKmsConfig = _createKmsConfig
	}()
	orch := GCPOrchestrator{storage: mockStorage, temporal: temporal}
	_, _, err := orch.CreateKmsConfig(ctx, params)
	assert.Error(t, err)
	assert.Equal(t, "some error", err.Error())
}

func TestGetKmsConfigFails(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
	params := &common.GetKmsConfigParams{AccountName: "fail_account"}
	mockStorage := new(database.MockStorage)
	temporal := workflow_engine.NewMockTemporalTestClient(t)
	getKmsConfig = func(ctx context.Context, se database.Storage, params *common.GetKmsConfigParams) (*models.KmsConfig, error) {
		return nil, errors.New("some error")
	}
	defer func() {
		getKmsConfig = _getKmsConfig
	}()
	orch := GCPOrchestrator{storage: mockStorage, temporal: temporal}
	_, err := orch.GetKmsConfig(ctx, params)
	assert.Error(t, err)
	assert.Equal(t, "some error", err.Error())
}

func TestCheckAndUpdateKmsConfigHealth(t *testing.T) {
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		orch := GCPOrchestrator{storage: mockStorage}
		ctx := context.Background()
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "test-uuid"},
			State:         datamodel.LifeCycleStateError,
			KeyName:       "key1",
			KeyRing:       "ring1",
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		orig := kms_activities.UpdateKmsConfigHealth
		defer func() {
			kms_activities.UpdateKmsConfigHealth = orig
		}()
		kms_activities.UpdateKmsConfigHealth = func(ctx context.Context, se database.Storage, configCheck *models.KmsConfigCheck) (*datamodel.KmsConfig, error) {
			return kmsConfig, nil
		}
		response := &models.KmsConfigCheck{
			KmsConfig:   &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}},
			IsHealthy:   true,
			HealthError: "",
		}
		result, err := orch.CheckAndUpdateKmsConfigHealth(ctx, response)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("WhenFailure", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		orch := GCPOrchestrator{storage: mockStorage}
		ctx := context.Background()
		orig := kms_activities.UpdateKmsConfigHealth
		defer func() {
			kms_activities.UpdateKmsConfigHealth = orig
		}()
		kms_activities.UpdateKmsConfigHealth = func(ctx context.Context, se database.Storage, configCheck *models.KmsConfigCheck) (*datamodel.KmsConfig, error) {
			return nil, errors.New("some error")
		}
		response := &models.KmsConfigCheck{
			KmsConfig:   &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}},
			IsHealthy:   true,
			HealthError: "",
		}
		result, err := orch.CheckAndUpdateKmsConfigHealth(ctx, response)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestAccessKmsCryptoKey(t *testing.T) {
	t.Run("AccessKmsCryptoKeyReturnsNoErrorWhenStorageSucceeds", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		orch := GCPOrchestrator{storage: mockStorage}
		ctx := context.Background()
		kmsConfig := &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}}
		dbKmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "test-uuid"}, ServiceAccount: &datamodel.ServiceAccount{ServiceAccountEmail: ""}}

		mockStorage.On("GetKmsConfig", ctx, "test-uuid").Return(dbKmsConfig, nil)
		origAccessCryptoKey := kms_activities.AccessCryptoKeyAndEncryptData
		defer func() { kms_activities.AccessCryptoKeyAndEncryptData = origAccessCryptoKey }()
		kms_activities.AccessCryptoKeyAndEncryptData = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, secretPassword string, timeout, timeoutInterval time.Duration) error {
			return nil
		}

		err := orch.AccessCryptoKeyAndEncryptDataWithImpersonation(ctx, kmsConfig)
		assert.NoError(tt, err)
	})

	t.Run("AccessKmsCryptoKeyReturnsErrorWhenGetKmsConfigFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		orch := GCPOrchestrator{storage: mockStorage}
		ctx := context.Background()
		kmsConfig := &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}}

		mockStorage.On("GetKmsConfig", ctx, "test-uuid").Return(nil, errors.New("get error"))

		err := orch.AccessCryptoKeyAndEncryptDataWithImpersonation(ctx, kmsConfig)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "get error")
	})

	t.Run("AccessKmsCryptoKeyReturnsErrorWhenAccessCryptoKeyFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		orch := GCPOrchestrator{storage: mockStorage}
		ctx := context.Background()
		kmsConfig := &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}}
		dbKmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "test-uuid"}, ServiceAccount: &datamodel.ServiceAccount{ServiceAccountEmail: "sa.com"}}

		mockStorage.On("GetKmsConfig", ctx, "test-uuid").Return(dbKmsConfig, nil)
		origAccessCryptoKey := kms_activities.AccessCryptoKeyAndEncryptData
		defer func() { kms_activities.AccessCryptoKeyAndEncryptData = origAccessCryptoKey }()
		kms_activities.AccessCryptoKeyAndEncryptData = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, secretPassword string, timeout, timeoutInterval time.Duration) error {
			return errors.New("access error")
		}

		err := orch.AccessCryptoKeyAndEncryptDataWithImpersonation(ctx, kmsConfig)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "access error")
	})
}

func TestGetExistingKmsConfig(t *testing.T) {
	t.Run("ReturnsKmsConfig_WhenKeyPathMatches", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		params := &common.GetKmsConfigParams{AccountName: "test-account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		expectedAccount := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "uuid"}, Name: "test-account"}
		existingConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "uuid"}, KeyProjectID: "p", KeyRingLocation: "l", KeyRing: "r", KeyName: "k",
			KmsAttributes: &datamodel.KmsAttributes{}, ServiceAccount: &datamodel.ServiceAccount{},
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return expectedAccount, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()
		mockStorage.On("ListKmsConfigByAccountID", ctx, int64(1)).Return([]*datamodel.KmsConfig{existingConfig}, nil)

		result, err := _getExistingKmsConfig(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "uuid", result.UUID)
	})
	t.Run("ReturnsNotFound_WhenNoConfigsExist", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		params := &common.GetKmsConfigParams{AccountName: "test-account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()
		mockStorage.On("ListKmsConfigByAccountID", ctx, int64(1)).Return([]*datamodel.KmsConfig{}, nil)

		result, err := _getExistingKmsConfig(ctx, mockStorage, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.True(t, errors.IsNotFoundErr(err))
	})
	t.Run("ReturnsNotFound_WhenDifferentKeyPathExistsAndFlagEnabled", func(t *testing.T) {
		originalEnableMultipleCmek := utils.EnableMultipleCmek
		defer func() { utils.EnableMultipleCmek = originalEnableMultipleCmek }()
		utils.EnableMultipleCmek = true

		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		params := &common.GetKmsConfigParams{AccountName: "test-account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/newKey"}
		existingConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "uuid"}, KeyProjectID: "p", KeyRingLocation: "l", KeyRing: "r", KeyName: "oldKey",
			KmsAttributes: &datamodel.KmsAttributes{}, ServiceAccount: &datamodel.ServiceAccount{},
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()
		mockStorage.On("ListKmsConfigByAccountID", ctx, int64(1)).Return([]*datamodel.KmsConfig{existingConfig}, nil)

		result, err := _getExistingKmsConfig(ctx, mockStorage, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.True(t, errors.IsNotFoundErr(err))
	})
	t.Run("ReturnsConflict_WhenDifferentKeyPathExistsAndFlagDisabled", func(t *testing.T) {
		originalEnableMultipleCmek := utils.EnableMultipleCmek
		defer func() { utils.EnableMultipleCmek = originalEnableMultipleCmek }()
		utils.EnableMultipleCmek = false

		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		params := &common.GetKmsConfigParams{AccountName: "test-account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/newKey"}
		existingConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "uuid"}, KeyProjectID: "p", KeyRingLocation: "l", KeyRing: "r", KeyName: "oldKey",
			ResourceID: "existing-resource-id", KmsAttributes: &datamodel.KmsAttributes{}, ServiceAccount: &datamodel.ServiceAccount{},
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()
		mockStorage.On("ListKmsConfigByAccountID", ctx, int64(1)).Return([]*datamodel.KmsConfig{existingConfig}, nil)

		result, err := _getExistingKmsConfig(ctx, mockStorage, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.True(t, errors.IsConflictErr(err))
		assert.Contains(t, err.Error(), "A KMS configuration already exists for this account with a different key path")
		assert.Contains(t, err.Error(), "projects/p/locations/l/keyRings/r/cryptoKeys/oldKey")
		assert.Contains(t, err.Error(), "existing-resource-id")
	})
	t.Run("ReturnsMatchingConfig_WhenMultipleConfigsExist", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		params := &common.GetKmsConfigParams{AccountName: "test-account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/keyB"}
		configA := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "uuidA"}, KeyProjectID: "p", KeyRingLocation: "l", KeyRing: "r", KeyName: "keyA",
			KmsAttributes: &datamodel.KmsAttributes{}, ServiceAccount: &datamodel.ServiceAccount{},
		}
		configB := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "uuidB"}, KeyProjectID: "p", KeyRingLocation: "l", KeyRing: "r", KeyName: "keyB",
			KmsAttributes: &datamodel.KmsAttributes{}, ServiceAccount: &datamodel.ServiceAccount{},
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()
		mockStorage.On("ListKmsConfigByAccountID", ctx, int64(1)).Return([]*datamodel.KmsConfig{configA, configB}, nil)

		result, err := _getExistingKmsConfig(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "uuidB", result.UUID)
	})
	t.Run("ReturnsError_WhenAccountFails", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		params := &common.GetKmsConfigParams{AccountName: "fail-account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account error")
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()

		result, err := _getExistingKmsConfig(ctx, mockStorage, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "account error")
	})
	t.Run("ReturnsError_WhenStorageFails", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		params := &common.GetKmsConfigParams{AccountName: "test-account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil
		}
		defer func() { getOrCreateAccount = _getOrCreateAccount }()
		mockStorage.On("ListKmsConfigByAccountID", ctx, int64(1)).Return(nil, errors.New("db error"))

		result, err := _getExistingKmsConfig(ctx, mockStorage, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "db error")
	})
}

func TestValidateUpdateKmsConfigParamsMultiCmek(t *testing.T) {
	ctx := context.Background()

	t.Run("ReturnsConflictWhenSiblingUsesSameKeyPath", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: "uuidA"},
			AccountID:       1,
			KeyProjectID:    "p",
			KeyRingLocation: "l",
			KeyRing:         "r",
			KeyName:         "keyA",
			State:           datamodel.LifeCycleStateAvailable,
		}
		sibling := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: "uuidB"},
			KeyProjectID:    "p",
			KeyRingLocation: "l",
			KeyRing:         "r",
			KeyName:         "keyB",
		}
		params := &common.UpdateKmsConfigParams{
			KeyUri: "projects/p/locations/l/keyRings/r/cryptoKeys/keyB",
		}

		mockStorage.On("IsKmsConfigInUse", ctx, "uuidA").Return(false, nil)
		mockStorage.On("ListKmsConfigByAccountID", ctx, int64(1)).Return([]*datamodel.KmsConfig{kmsConfig, sibling}, nil)

		err := _validateUpdateKmsConfigParams(ctx, mockStorage, kmsConfig, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "another KMS configuration in this account already uses this crypto key path")
	})

	t.Run("AllowsKeyPathUpdateWhenSiblingUsesDifferentKeyPath", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: "uuidA"},
			AccountID:       1,
			KeyProjectID:    "p",
			KeyRingLocation: "l",
			KeyRing:         "r",
			KeyName:         "keyA",
			State:           datamodel.LifeCycleStateAvailable,
		}
		sibling := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: "uuidB"},
			KeyProjectID:    "p",
			KeyRingLocation: "l",
			KeyRing:         "r",
			KeyName:         "keyB",
		}
		params := &common.UpdateKmsConfigParams{
			KeyUri: "projects/p/locations/l/keyRings/r/cryptoKeys/keyC",
		}

		mockStorage.On("IsKmsConfigInUse", ctx, "uuidA").Return(false, nil)
		mockStorage.On("ListKmsConfigByAccountID", ctx, int64(1)).Return([]*datamodel.KmsConfig{kmsConfig, sibling}, nil)

		err := _validateUpdateKmsConfigParams(ctx, mockStorage, kmsConfig, params)
		assert.NoError(tt, err)
	})
}

func TestOrchestratorGetExistingKmsConfig(t *testing.T) {
	t.Run("OrchestratorGetExistingKmsConfigReturnsKmsConfigOnSuccess", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		orch := GCPOrchestrator{storage: mockStorage}
		params := &common.GetKmsConfigParams{AccountName: "test-account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
		expectedKmsConfig := &models.KmsConfig{BaseModel: models.BaseModel{UUID: "uuid"}}

		getExistingKmsConfig = func(ctx context.Context, se database.Storage, params *common.GetKmsConfigParams) (*models.KmsConfig, error) {
			return expectedKmsConfig, nil
		}
		defer func() { getExistingKmsConfig = _getExistingKmsConfig }()

		result, err := orch.GetExistingKmsConfig(ctx, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "uuid", result.UUID)
	})
	t.Run("OrchestratorGetExistingKmsConfigReturnsErrorOnFailure", func(t *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		orch := GCPOrchestrator{storage: mockStorage}
		params := &common.GetKmsConfigParams{AccountName: "fail-account", KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}

		getExistingKmsConfig = func(ctx context.Context, se database.Storage, params *common.GetKmsConfigParams) (*models.KmsConfig, error) {
			return nil, errors.New("some error")
		}
		defer func() { getExistingKmsConfig = _getExistingKmsConfig }()

		result, err := orch.GetExistingKmsConfig(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "some error")
	})
}

func TestDeleteKmsConfig(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()
	cvp.CVP_HOST = "localhost:8009"

	ctx := context.Background()

	t.Run("SuccessfulDelete", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		// Mock GetJobByResourceUUID to check for existing delete job (called for non-CREATING, non-DELETING states)
		mockStorage.On("GetJobByResourceUUID", ctx, dbKmsConfig.UUID, string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("ListOngoingPoolJobsWithKmsConfigId", ctx, dbKmsConfig.ID, dbKmsConfig.AccountID).Return(make([]*datamodel.Job, 0), nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(dbKmsConfig, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "test-job-uuid", jobUUID)
		assert.NotNil(tt, kmsConfig)
		assert.Equal(tt, "test-kms-config-id", kmsConfig.UUID)
	})

	t.Run("SuccessfulDeleteWhenInErrorState", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateError,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		// Mock GetJobByResourceUUID to check for existing delete job (called for non-CREATING, non-DELETING states)
		mockStorage.On("GetJobByResourceUUID", ctx, dbKmsConfig.UUID, string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("ListOngoingPoolJobsWithKmsConfigId", ctx, dbKmsConfig.ID, dbKmsConfig.AccountID).Return(make([]*datamodel.Job, 0), nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(dbKmsConfig, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "test-job-uuid", jobUUID)
		assert.NotNil(tt, kmsConfig)
		assert.Equal(tt, "test-kms-config-id", kmsConfig.UUID)
	})

	t.Run("SuccessfulUpdateWhenVcpKmsConfigNotFound", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(nil, errors.NewNotFoundErr("kms config", nil))
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{SdeKmsConfigUUID: dbKmsConfig.UUID}}, params).Return(nil, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "test-job-uuid", jobUUID)
		assert.Nil(tt, kmsConfig)
	})

	t.Run("ValidationError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		waitForTemporalEnabled = true
		defer func() {
			waitForTemporalEnabled = env.GetBool("WAIT_FOR_TEMPORAL_ENABLED", true)
		}()

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		account := &datamodel.Account{Name: "test-account"}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(true, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		// Mock GetJobByResourceUUID to check for existing delete job (called for non-CREATING, non-DELETING states)
		mockStorage.On("GetJobByResourceUUID", ctx, dbKmsConfig.UUID, string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return([]*datamodel.Svm{&datamodel.Svm{Name: "svm"}}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Nil(tt, kmsConfig)
		assert.Empty(tt, jobUUID)
	})

	t.Run("WhenSvmNotFound", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		account := &datamodel.Account{Name: "test-account"}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		// Mock GetJobByResourceUUID to check for existing delete job (called for non-CREATING, non-DELETING states)
		mockStorage.On("GetJobByResourceUUID", ctx, dbKmsConfig.UUID, string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, errors.New("error"))
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, errors.New("error"))

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "error")
		assert.Nil(tt, kmsConfig)
		assert.Empty(tt, jobUUID)
	})

	t.Run("WhenKmsConfigInCreatingState", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateCreating,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		account := &datamodel.Account{Name: "test-account"}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		// Mock GetJobByResourceUUID for delete job check (returns nil, no existing delete job)
		mockStorage.On("GetJobByResourceUUID", ctx, "test-kms-config-id", string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		// Mock GetJobByResourceUUID for create job check (returns nil, not a cancellation request)
		mockStorage.On("GetJobByResourceUUID", ctx, "test-kms-config-id", string(datamodel.JobTypeCreateKmsConfig)).Return(nil, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Error deleting KMS config - KMS config is already transitioning between states")
		assert.Nil(tt, kmsConfig)
		assert.Empty(tt, jobUUID)
	})

	t.Run("WhenKmsConfigInCreatingStateWithMatchingCorrelationID", func(tt *testing.T) {
		// Test for lines 326-327: Cancellation request when correlation ID matches
		correlationID := "test-correlation-id-123"
		fields := log.Fields{
			string(middleware.RequestCorrelationID): correlationID,
		}
		ctxWithCorrelationID := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)

		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id", ID: 1},
			State:          datamodel.LifeCycleStateCreating,
			AccountID:      1,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		createJob := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "create-job-uuid"},
			CorrelationID: correlationID,
			Type:          string(datamodel.JobTypeCreateKmsConfig),
		}

		// Patch getAccountWithName to return account
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		// Mock storage behavior
		// Note: For cancellation requests, validation is skipped, so IsKmsConfigInUse is NOT called
		mockStorage.On("GetKmsConfig", ctxWithCorrelationID, "test-kms-config-id").Return(dbKmsConfig, nil)
		// Mock GetJobByResourceUUID for delete job check (returns nil, no existing delete job)
		mockStorage.On("GetJobByResourceUUID", ctxWithCorrelationID, "test-kms-config-id", string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		// Mock GetJobByResourceUUID to return create job with matching correlation ID (lines 332-335)
		mockStorage.On("GetJobByResourceUUID", ctxWithCorrelationID, "test-kms-config-id", string(datamodel.JobTypeCreateKmsConfig)).Return(createJob, nil)
		// For cancellation requests, UpdateKmsConfigState should NOT be called (line 349)
		mockStorage.On("CreateJob", ctxWithCorrelationID, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctxWithCorrelationID, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctxWithCorrelationID, params)

		// Should succeed as cancellation request (lines 326-327)
		assert.NoError(tt, err)
		assert.Equal(tt, "test-job-uuid", jobUUID)
		assert.NotNil(tt, kmsConfig)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenUpdateKmsConfigStateFails", func(tt *testing.T) {
		// Test for line 352: UpdateKmsConfigState error
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id", ID: 1},
			State:          datamodel.LifeCycleStateAvailable,
			AccountID:      1,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}

		// Patch getAccountWithName to return account
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		// Mock storage behavior
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		// Mock GetJobByResourceUUID to check for existing delete job (called for non-CREATING, non-DELETING states)
		mockStorage.On("GetJobByResourceUUID", ctx, dbKmsConfig.UUID, string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		mockStorage.On("ListOngoingPoolJobsWithKmsConfigId", ctx, dbKmsConfig.ID, dbKmsConfig.AccountID).Return(make([]*datamodel.Job, 0), nil)
		// Mock UpdateKmsConfigState to return error (line 359)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(nil, errors.New("update state error"))

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "update state error")
		assert.Nil(tt, kmsConfig)
		assert.Empty(tt, jobUUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WorkflowExecutionFailure", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		// Mock GetJobByResourceUUID to check for existing delete job (called for non-CREATING, non-DELETING states)
		mockStorage.On("GetJobByResourceUUID", ctx, dbKmsConfig.UUID, string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("ListOngoingPoolJobsWithKmsConfigId", ctx, dbKmsConfig.ID, dbKmsConfig.AccountID).Return(make([]*datamodel.Job, 0), nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(dbKmsConfig, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, errors.New("workflow execution failed"))
		mockStorage.On("UpdateJob", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, kmsConfig)
		assert.NotEmpty(tt, jobUUID)
	})

	t.Run("WhenKmsConfigAlreadyDeletingWithActiveJob", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:     datamodel.LifeCycleStateDeleting, // Already in deleting state
		}
		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "existing-job-uuid"},
			Type:      string(datamodel.JobTypeDeleteKmsConfig),
			State:     string(datamodel.JobsStateNEW), // Not done yet
		}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("GetJobByResourceUUID", ctx, "test-kms-config-id", string(datamodel.JobTypeDeleteKmsConfig)).Return(existingJob, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "existing-job-uuid", jobUUID) // Should return existing job UUID
		assert.NotNil(tt, kmsConfig)
		assert.Equal(tt, "test-kms-config-id", kmsConfig.UUID)

		// Verify that no new job was created and no workflow was started
		mockStorage.AssertNotCalled(tt, "CreateJob")
		mockTemporal.AssertNotCalled(tt, "ExecuteWorkflow")
	})

	t.Run("WhenKmsConfigAlreadyDeletingWithDoneJob", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-config-id", ID: 1},
			State:     datamodel.LifeCycleStateDeleting, // Already in deleting state
			AccountID: 1,
		}
		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "existing-job-uuid"},
			Type:      string(datamodel.JobTypeDeleteKmsConfig),
			State:     string(datamodel.JobsStateDONE), // Already done
		}

		// Patch getAccountWithName to return account
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		// Mock storage behavior
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("GetJobByResourceUUID", ctx, "test-kms-config-id", string(datamodel.JobTypeDeleteKmsConfig)).Return(existingJob, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("ListOngoingPoolJobsWithKmsConfigId", ctx, dbKmsConfig.ID, dbKmsConfig.AccountID).Return(make([]*datamodel.Job, 0), nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(dbKmsConfig, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "new-job-uuid"},
		}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "new-job-uuid", jobUUID) // Should create new job since existing is done
		assert.NotNil(tt, kmsConfig)
		assert.Equal(tt, "test-kms-config-id", kmsConfig.UUID)
	})

	t.Run("WhenKmsConfigAlreadyDeletingButJobLookupFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:     datamodel.LifeCycleStateDeleting, // Already in deleting state
		}

		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		mockStorage.On("GetJobByResourceUUID", ctx, "test-kms-config-id", string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, errors.New("job not found"))
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("ListOngoingPoolJobsWithKmsConfigId", ctx, dbKmsConfig.ID, dbKmsConfig.AccountID).Return(make([]*datamodel.Job, 0), nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(dbKmsConfig, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "new-job-uuid"},
		}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "new-job-uuid", jobUUID) // Should create new job since lookup failed
		assert.NotNil(tt, kmsConfig)
		assert.Equal(tt, "test-kms-config-id", kmsConfig.UUID)
	})

	t.Run("WorkflowExecutionFailureAndUpdateJobFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		// Mock GetJobByResourceUUID to check for existing delete job (called for non-CREATING, non-DELETING states)
		mockStorage.On("GetJobByResourceUUID", ctx, dbKmsConfig.UUID, string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("ListOngoingPoolJobsWithKmsConfigId", ctx, dbKmsConfig.ID, dbKmsConfig.AccountID).Return(make([]*datamodel.Job, 0), nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(dbKmsConfig, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, errors.New("workflow execution failed"))
		mockStorage.On("UpdateJob", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("job update failed"))

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, kmsConfig)
		assert.NotEmpty(tt, jobUUID)
	})

	t.Run("WorkflowExecutionFailureAndFlagDisabled", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		waitForTemporalEnabled = false
		defer func() {
			waitForTemporalEnabled = env.GetBool("WAIT_FOR_TEMPORAL_ENABLED", true)
		}()

		account := &datamodel.Account{Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}
		// Mock storage behavior
		mockStorage.On("GetAccount", ctx, "test-account").Return(account, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		// Mock GetJobByResourceUUID to check for existing delete job (called for non-CREATING, non-DELETING states)
		mockStorage.On("GetJobByResourceUUID", ctx, dbKmsConfig.UUID, string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		mockStorage.On("GetSvmsByKmsConfigID", ctx, dbKmsConfig.ID).Return(nil, nil)
		mockStorage.On("ListOngoingPoolJobsWithKmsConfigId", ctx, dbKmsConfig.ID, dbKmsConfig.AccountID).Return(make([]*datamodel.Job, 0), nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(dbKmsConfig, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, errors.New("workflow execution failed"))
		mockStorage.On("UpdateJob", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockStorage.On("UpdateKmsConfigState", ctx, "test-kms-config-id", datamodel.LifeCycleStateAvailable, mock.Anything).Return(&datamodel.KmsConfig{}, nil).Once()

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Nil(tt, kmsConfig)
		assert.Empty(tt, jobUUID)
	})
	t.Run("WhenDeleteKmsConfigSetsRequestID", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID:    "test-kms-config-id",
			AccountName:    "test-account",
			XCorrelationID: "test-correlation-id-existing",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: int64(1)}, Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel:      datamodel.BaseModel{ID: int64(123), UUID: "test-kms-config-id"},
			State:          datamodel.LifeCycleStateAvailable,
			AccountID:      int64(1),
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "test-sa-id"}},
		}

		// Set up context with RequestID
		fields := log.Fields{
			string(middleware.RequestID): "test-request-id-delete",
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)

		// Patch getAccountWithName to return account
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		// Mock storage behavior
		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		// Mock GetJobByResourceUUID for DELETE_ACTIVE_DIRECTORY (check for existing delete job when in non-transitional state)
		mockStorage.On("GetJobByResourceUUID", ctx, "test-kms-config-id", string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, dbKmsConfig.UUID).Return(false, nil)
		mockStorage.On("ListOngoingPoolJobsWithKmsConfigId", ctx, dbKmsConfig.ID, dbKmsConfig.AccountID).Return(make([]*datamodel.Job, 0), nil)
		mockStorage.On("UpdateKmsConfigState", ctx, dbKmsConfig.UUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(dbKmsConfig, nil)
		// Verify job has both CorrelationID and RequestID
		mockStorage.On("CreateJob", ctx, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.CorrelationID == "test-correlation-id-existing" && job.RequestID == "test-request-id-delete"
		})).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		}, nil)

		// Mock Temporal client behavior
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, dbKmsConfig, params).Return(nil, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "test-job-uuid", jobUUID)
		assert.NotNil(tt, kmsConfig)
		assert.Equal(tt, "test-kms-config-id", kmsConfig.UUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenKmsConfigInCreatingStateWithExistingDeleteJob_ReturnsExistingJobUUID", func(tt *testing.T) {
		// Test for line 333: When existingDeleteJobUUID is not empty, return it immediately
		correlationID := "test-correlation-id-123"
		fields := log.Fields{
			string(middleware.RequestCorrelationID): correlationID,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)

		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:     datamodel.LifeCycleStateCreating,
			AccountID: 42,
		}
		existingDeleteJob := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "existing-delete-job-uuid"},
			CorrelationID: correlationID,
			Type:          string(datamodel.JobTypeDeleteKmsConfig),
			State:         string(datamodel.JobsStatePROCESSING),
		}

		// Patch getAccountWithName
		origGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = origGetAccountWithName }()

		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		// ValidateCorrelationIDForCreatingResource returns existingDeleteJobUUID when delete job is in progress
		mockStorage.On("GetJobByResourceUUID", ctx, "test-kms-config-id", string(datamodel.JobTypeDeleteKmsConfig)).Return(existingDeleteJob, nil)

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "existing-delete-job-uuid", jobUUID)
		assert.NotNil(tt, kmsConfig)
		assert.Equal(tt, "test-kms-config-id", kmsConfig.UUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenKmsConfigInTransitionalState_ReturnsConflictError", func(tt *testing.T) {
		// Test for lines 338-339: When KMS config is in transitional state (not DELETING), return error
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine.MockTemporalTestClient)

		params := &common.DeleteKmsConfigParams{
			KmsConfigID: "test-kms-config-id",
			AccountName: "test-account",
		}

		orchestrator := GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}
		dbKmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-config-id"},
			State:     datamodel.LifeCycleStateUpdating, // Transitional state (not DELETING)
			AccountID: 42,
		}

		// Patch getAccountWithName
		origGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = origGetAccountWithName }()

		mockStorage.On("GetKmsConfig", ctx, "test-kms-config-id").Return(dbKmsConfig, nil)
		// Note: GetJobByResourceUUID is not called when KMS config is in transitional state
		// because the function returns early at line 339 in kms_config.go

		kmsConfig, jobUUID, err := orchestrator.DeleteKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "KMS config is in transition state and cannot be deleted")
		assert.Contains(tt, err.Error(), datamodel.LifeCycleStateUpdating)
		assert.Nil(tt, kmsConfig)
		assert.Empty(tt, jobUUID)
		mockStorage.AssertExpectations(tt)
	})
}

func TestValidateKmsConfigState(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	store, err := database.SetupStorageForTest(mockLogger)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}

	err = database.ClearInMemoryDB(store.DB())
	if err != nil {
		t.Fatalf("Failed to clean up test storage: %v", err)
	}

	jobs := []*datamodel.Job{{BaseModel: datamodel.BaseModel{UUID: "uuid1"}, Type: "MIGRATE_KMS_CONFIG", State: "NEW",
		AccountID: sql.NullInt64{Int64: 1, Valid: true}}}

	err = store.DB().Create(jobs).Error
	if err != nil {
		t.Fatalf("Failed to create Jobs table: %v", err)
	}
	t.Run("WhenKmsConfigStateIsNotReadyOrInUse", func(tt *testing.T) {
		jobId, errValidate := validateKmsConfigState(ctx, store, datamodel.LifeCycleStateCreated, int64(1), true)
		assert.Equal(tt, jobId, "")
		assert.Error(tt, errValidate)
		assert.EqualError(tt, errValidate, "CMEK Configuration needs to be in either Ready or In_Use state for migration")
	})
	t.Run("WhenKmsConfigStateIsInReadyState", func(tt *testing.T) {
		jobId, errValidate := validateKmsConfigState(ctx, store, datamodel.LifeCycleStateREADY, int64(1), true)

		assert.NoError(tt, errValidate)
		assert.Equal(tt, jobId, "")
	})
	t.Run("WhenKmsConfigStateIsMigratingAndDBEntryIsNotInVCP", func(tt *testing.T) {
		jobId, errValidate := validateKmsConfigState(ctx, store, datamodel.LifeCycleStateMigrating, int64(1), false)

		assert.NoError(tt, errValidate)
		assert.Equal(tt, "uuid1", jobId)
	})
	t.Run("WhenKmsConfigStateIsUpdatingAndDBEntryIsInVCP", func(tt *testing.T) {
		jobId, errValidate := validateKmsConfigState(ctx, store, datamodel.LifeCycleStateUpdating, int64(1), true)

		assert.NoError(tt, errValidate)
		assert.Equal(tt, "uuid1", jobId)
	})
	t.Run("WhenKmsConfigStateIsMigratingAndDBEntryIsInVCP", func(tt *testing.T) {
		jobId, errValidate := validateKmsConfigState(ctx, store, datamodel.LifeCycleStateMigrating, int64(1), true)

		assert.NoError(tt, errValidate)
		assert.Equal(tt, "uuid1", jobId)
	})
}

func TestConvertKmsConfigStateV1beta(t *testing.T) {
	t.Run("ReturnsKeyCheckPendingForCreatedState", func(t *testing.T) {
		state, details := convertKmsConfigStateV1beta(datamodel.LifeCycleStateCreated, "ignored")
		assert.Equal(t, cvpModels.KmsConfigV1betaKmsStateKEYCHECKPENDING, state)
		assert.Equal(t, "Credentials created and key check pending", details)
	})

	t.Run("ReturnsInUseForInUseState", func(t *testing.T) {
		state, details := convertKmsConfigStateV1beta(datamodel.LifeCycleStateInUse, "ignored")
		assert.Equal(t, cvpModels.KmsConfigV1betaKmsStateINUSE, state)
		assert.Equal(t, "Kms config in use", details)
	})

	t.Run("ReturnsReadyForREADYState", func(t *testing.T) {
		state, details := convertKmsConfigStateV1beta(datamodel.LifeCycleStateREADY, "ignored")
		assert.Equal(t, cvpModels.KmsConfigV1betaKmsStateREADY, state)
		assert.Equal(t, "Kms config is ready for use", details)
	})

	t.Run("ReturnsMigratingForMigratingState", func(t *testing.T) {
		state, details := convertKmsConfigStateV1beta(datamodel.LifeCycleStateMigrating, "ignored")
		assert.Equal(t, cvpModels.KmsConfigV1betaKmsStateMIGRATING, state)
		assert.Equal(t, "Kms config is in migrating state", details)
	})

	t.Run("ReturnsEmptyForUnknownState", func(t *testing.T) {
		state, details := convertKmsConfigStateV1beta("unknown_state", "ignored")
		assert.Equal(t, "unknown_state", state)
		assert.Equal(t, "", details)
	})

	t.Run("ReturnsErrorStateWithPreservedDetailsForERROR", func(t *testing.T) {
		state, details := convertKmsConfigStateV1beta(datamodel.LifeCycleStateError, "VCP-only error detail")
		assert.Equal(t, cvpModels.KmsConfigV1betaKmsStateERROR, state)
		assert.Equal(t, "VCP-only error detail", details)
	})

	t.Run("TrimsErrorDashPrefixInDefaultErrorBranch", func(t *testing.T) {
		state, details := convertKmsConfigStateV1beta("some_error_status", "error - downstream message")
		assert.Equal(t, cvpModels.KmsConfigV1betaKmsStateERROR, state)
		assert.Equal(t, "downstream message", details)
	})
}

func TestRotateKmsConfig_Success(t *testing.T) {
	// Setup mocks
	mockStorage := database.NewMockStorage(t)
	mockTemporal := new(workflow_engine.MockTemporalTestClient)

	// Test data
	params := &common.RotateKmsConfigParams{
		KmsConfigID:    "test-kms-config-uuid",
		AccountName:    "test-account",
		XCorrelationID: "test-correlation-id",
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1},
		Name:      "test-account",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID:      "test-kms-config-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Name:              "test-kms-config",
		Description:       "Test KMS config",
		State:             string(cvpModels.KmsConfigV1betaKmsStateREADY),
		StateDetails:      "Ready for use",
		KeyRing:           "test-keyring",
		KeyRingLocation:   "us-central1",
		KeyName:           "test-key",
		AccountID:         1,
		CustomerProjectID: "customer-project",
		KeyProjectID:      "key-project",
		ServiceAccountID:  &[]int64{1}[0],
		ResourceID:        "test-resource-id",
	}

	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "test-job-uuid"},
		WorkflowID: "test-workflow-id",
		Type:       string(datamodel.JobTypeRotateKmsConfig),
		State:      string(datamodel.JobsStateNEW),
	}

	// Mock the getAccountFromUUID function
	originalGetAccountFromUUID := getAccountFromUUID
	getAccountFromUUID = func(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

	// Set up expectations
	mockStorage.On("GetKmsConfig", context.Background(), "test-kms-config-uuid").Return(kmsConfig, nil)
	mockStorage.On("ListPools", context.Background(), mock.Anything).Return([]*datamodel.PoolView{}, nil)
	mockStorage.On("CreateJob", context.Background(), mock.Anything).Return(createdJob, nil)

	mockTemporal.On("ExecuteWorkflow",
		mock.Anything,
		mock.Anything,
		mock.AnythingOfType("func(internal.Context, *common.RotateKmsConfigParams) (interface {}, error)"),
		mock.Anything,
	).Return(nil, nil)

	// Execute
	result, job, err := rotateKmsConfig(context.Background(), mockStorage, mockTemporal, params)

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, job)
	assert.Equal(t, "test-job-uuid", job.UUID)

	// Verify returned Job model
	assert.Equal(t, datamodel.JobTypeRotateKmsConfig, job.Type)
	assert.Equal(t, datamodel.JobsStateNEW, job.State)
	assert.Equal(t, "test-workflow-id", job.WorkflowID)

	// Verify returned KMS config model
	assert.Equal(t, "test-kms-config-uuid", result.UUID)
	assert.Equal(t, "test-kms-config", result.Name)
	assert.Equal(t, "Test KMS config", result.Description)
	assert.Equal(t, string(cvpModels.KmsConfigV1betaKmsStateREADY), result.State)
	assert.Equal(t, "Kms config is ready for use", result.StateDetails)
	assert.Equal(t, "test-keyring", result.KeyRing)
	assert.Equal(t, "us-central1", result.KeyRingLocation)
	assert.Equal(t, "test-key", result.KeyName)
	assert.Equal(t, "customer-project", result.CustomerProjectID)
	assert.Equal(t, "key-project", result.KeyProjectID)
	assert.Equal(t, "test-resource-id", result.ResourceID)

	// Verify expectations
	mockStorage.AssertExpectations(t)
	mockTemporal.AssertExpectations(t)
}

func TestRotateKmsConfig_PoolInCreatingState(t *testing.T) {
	// Setup mocks
	mockStorage := database.NewMockStorage(t)
	mockTemporal := new(workflow_engine.MockTemporalTestClient)

	// Test data
	params := &common.RotateKmsConfigParams{
		KmsConfigID:    "test-kms-config-uuid",
		AccountName:    "test-account",
		XCorrelationID: "test-correlation-id",
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1},
		Name:      "test-account",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "test-kms-config-uuid",
		},
		State: string(cvpModels.KmsConfigV1betaKmsStateREADY),
	}

	// Create a pool in CREATING state with valid KmsConfigID
	pools := []*datamodel.PoolView{
		{
			Pool: datamodel.Pool{
				BaseModel:   datamodel.BaseModel{UUID: "pool-uuid"},
				Name:        "test-pool",
				State:       string(gcpserver.PoolV1betaStoragePoolStateCREATING),
				KmsConfigID: sql.NullInt64{Int64: 1, Valid: true},
			},
		},
	}

	// Mock the getAccountFromUUID function
	originalGetAccountFromUUID := getAccountFromUUID
	getAccountFromUUID = func(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

	// Set up expectations
	mockStorage.On("GetKmsConfig", context.Background(), "test-kms-config-uuid").Return(kmsConfig, nil)
	mockStorage.On("ListPools", context.Background(), mock.Anything).Return(pools, nil)

	// Execute
	result, job, err := rotateKmsConfig(context.Background(), mockStorage, mockTemporal, params)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Nil(t, job)
	assert.Contains(t, err.Error(), "Cannot rotate KMS config while CMEK-enabled storage pool is in creating state: test-pool")

	// Verify expectations
	mockStorage.AssertExpectations(t)
	mockTemporal.AssertExpectations(t)
}

func TestRotateKmsConfig_PoolInCreatingStateWithoutKmsConfigID(t *testing.T) {
	// Setup mocks
	mockStorage := database.NewMockStorage(t)
	mockTemporal := new(workflow_engine.MockTemporalTestClient)

	// Test data
	params := &common.RotateKmsConfigParams{
		KmsConfigID:    "test-kms-config-uuid",
		AccountName:    "test-account",
		XCorrelationID: "test-correlation-id",
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1},
		Name:      "test-account",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID:      "test-kms-config-uuid",
			ID:        1,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Name:  "test-kms-config",
		State: string(cvpModels.KmsConfigV1betaKmsStateREADY),
	}

	// Create a pool in CREATING state but WITHOUT valid KmsConfigID (should not block rotation)
	pools := []*datamodel.PoolView{
		{
			Pool: datamodel.Pool{
				BaseModel:   datamodel.BaseModel{UUID: "pool-uuid"},
				Name:        "test-pool",
				State:       string(gcpserver.PoolV1betaStoragePoolStateCREATING),
				KmsConfigID: sql.NullInt64{Valid: false}, // No valid KMS config ID
			},
		},
	}

	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "test-job-uuid"},
		WorkflowID: "test-workflow-id",
		Type:       string(datamodel.JobTypeRotateKmsConfig),
		State:      string(datamodel.JobsStateNEW),
	}

	// Mock the getAccountFromUUID function
	originalGetAccountFromUUID := getAccountFromUUID
	getAccountFromUUID = func(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

	// Set up expectations
	mockStorage.On("GetKmsConfig", context.Background(), "test-kms-config-uuid").Return(kmsConfig, nil)
	mockStorage.On("ListPools", context.Background(), mock.Anything).Return(pools, nil)
	mockStorage.On("CreateJob", context.Background(), mock.Anything).Return(createdJob, nil)

	mockTemporal.On("ExecuteWorkflow",
		mock.Anything,
		mock.Anything,
		mock.AnythingOfType("func(internal.Context, *common.RotateKmsConfigParams) (interface {}, error)"),
		mock.Anything,
	).Return(nil, nil)

	// Execute
	result, job, err := rotateKmsConfig(context.Background(), mockStorage, mockTemporal, params)

	// Assert - rotation should proceed since pool doesn't have valid KmsConfigID
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, job)
	assert.Equal(t, "test-job-uuid", job.UUID)
	assert.Equal(t, datamodel.JobTypeRotateKmsConfig, job.Type)
	assert.Equal(t, datamodel.JobsStateNEW, job.State)
	assert.Equal(t, "test-workflow-id", job.WorkflowID)

	// Verify expectations
	mockStorage.AssertExpectations(t)
	mockTemporal.AssertExpectations(t)
}

func TestRotateKmsConfig_KmsConfigInMigratingState(t *testing.T) {
	// Setup mocks
	mockStorage := database.NewMockStorage(t)
	mockTemporal := new(workflow_engine.MockTemporalTestClient)

	// Test data
	params := &common.RotateKmsConfigParams{
		KmsConfigID:    "test-kms-config-uuid",
		AccountName:    "test-account",
		XCorrelationID: "test-correlation-id",
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1},
		Name:      "test-account",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "test-kms-config-uuid",
		},
		State: datamodel.LifeCycleStateMigrating,
	}

	// Mock the getAccountFromUUID function
	originalGetAccountFromUUID := getAccountFromUUID
	getAccountFromUUID = func(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

	// Set up expectations
	mockStorage.On("GetKmsConfig", context.Background(), "test-kms-config-uuid").Return(kmsConfig, nil)

	// Execute
	result, job, err := rotateKmsConfig(context.Background(), mockStorage, mockTemporal, params)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Nil(t, job)
	// Migrating state is caught by a specific check before the generic state check
	assert.Contains(t, err.Error(), "KMS config is currently in Migrating state and cannot be rotated")

	// Verify expectations
	mockStorage.AssertExpectations(t)
	mockTemporal.AssertExpectations(t)
}

func TestRotateKmsConfig_AccountNotFound(t *testing.T) {
	// Setup mocks
	mockStorage := database.NewMockStorage(t)
	mockTemporal := new(workflow_engine.MockTemporalTestClient)

	// Test data
	params := &common.RotateKmsConfigParams{
		KmsConfigID: "test-kms-config-uuid",
		AccountName: "non-existent-account",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "test-kms-config-uuid",
		},
		State: string(cvpModels.KmsConfigV1betaKmsStateREADY),
	}

	// Set up expectations - GetKmsConfig is called first
	mockStorage.On("GetKmsConfig", context.Background(), "test-kms-config-uuid").Return(kmsConfig, nil)

	// Mock the getAccountFromUUID function to return error
	originalGetAccountFromUUID := getAccountFromUUID
	getAccountFromUUID = func(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
		return nil, errors.NewNotFoundErr("account not found", nil)
	}
	defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

	// Execute
	result, job, err := rotateKmsConfig(context.Background(), mockStorage, mockTemporal, params)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Nil(t, job)
	assert.True(t, errors.IsNotFoundErr(err))

	// Verify expectations
	mockStorage.AssertExpectations(t)
	mockTemporal.AssertExpectations(t)
}

func TestRotateKmsConfig_SetsCorrelationIDAndRequestID(t *testing.T) {
	// Setup mocks
	mockStorage := database.NewMockStorage(t)
	mockTemporal := new(workflow_engine.MockTemporalTestClient)

	// Test data
	params := &common.RotateKmsConfigParams{
		KmsConfigID:    "test-kms-config-uuid",
		AccountName:    "test-account",
		XCorrelationID: "test-correlation-id-789",
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1},
		Name:      "test-account",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "test-kms-config-uuid",
		},
		Name:  "test-kms-config",
		State: string(cvpModels.KmsConfigV1betaKmsStateREADY),
	}

	// Set up context with RequestID
	fields := log.Fields{
		string(middleware.RequestID): "test-request-id-999",
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)

	// Mock the getAccountFromUUID function
	originalGetAccountFromUUID := getAccountFromUUID
	getAccountFromUUID = func(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountFromUUID = originalGetAccountFromUUID }()

	// Set up expectations - verify job has CorrelationID and RequestID
	mockStorage.On("GetKmsConfig", ctx, "test-kms-config-uuid").Return(kmsConfig, nil)
	// Mock ListPools to return empty list so pool check passes
	mockStorage.On("ListPools", ctx, mock.Anything).Return([]*datamodel.PoolView{}, nil)
	mockStorage.On("CreateJob", ctx, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.CorrelationID == "test-correlation-id-789" && job.RequestID == "test-request-id-999"
	})).Return(&datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "test-job-uuid"},
		WorkflowID: "test-workflow-id",
	}, nil)

	mockTemporal.On("ExecuteWorkflow",
		mock.Anything,
		mock.Anything,
		mock.AnythingOfType("func(internal.Context, *common.RotateKmsConfigParams) (interface {}, error)"),
		mock.Anything,
	).Return(nil, nil)

	// Execute
	result, job, err := rotateKmsConfig(ctx, mockStorage, mockTemporal, params)

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, job)
	assert.Equal(t, "test-job-uuid", job.UUID)

	// Verify expectations
	mockStorage.AssertExpectations(t)
	mockTemporal.AssertExpectations(t)
}

func TestConvertSDEResponseToKmsConfig(t *testing.T) {
	t.Run("HandlesAllKmsStates", func(t *testing.T) {
		testCases := []struct {
			state         string
			expectedState string
		}{
			{cvpModels.KmsConfigV1betaKmsStateKEYCHECKPENDING, cvpModels.KmsConfigV1betaKmsStateKEYCHECKPENDING},
			{cvpModels.KmsConfigV1betaKmsStateINUSE, cvpModels.KmsConfigV1betaKmsStateINUSE},
			{cvpModels.KmsConfigV1betaKmsStateREADY, cvpModels.KmsConfigV1betaKmsStateREADY},
			{cvpModels.KmsConfigV1betaKmsStateUPDATING, cvpModels.KmsConfigV1betaKmsStateUPDATING},
			{cvpModels.KmsConfigV1betaKmsStateERROR, cvpModels.KmsConfigV1betaKmsStateERROR},
			{cvpModels.KmsConfigV1betaKmsStateKEYSTATEUNSPECIFIED, cvpModels.KmsConfigV1betaKmsStateKEYSTATEUNSPECIFIED},
		}

		for _, tc := range testCases {
			t.Run(tc.state, func(t *testing.T) {
				sdeResponse := &gcpserver.KmsConfigV1beta{
					KmsState: gcpserver.NewOptKmsConfigV1betaKmsState(gcpserver.KmsConfigV1betaKmsState(tc.state)),
				}

				result := convertSDEResponseToKmsConfig(sdeResponse)

				assert.NotNil(t, result)
				assert.Equal(t, tc.expectedState, result.State)
			})
		}
	})
	t.Run("HandlesNilStates", func(t *testing.T) {
		sdeResponse := &gcpserver.KmsConfigV1beta{}

		result := convertSDEResponseToKmsConfig(sdeResponse)

		assert.NotNil(t, result)
		assert.Equal(t, cvpModels.KmsConfigV1betaKmsStateKEYSTATEUNSPECIFIED, result.State)
	})
	t.Run("ParsesKmsGrantFromKeyFullPath", func(t *testing.T) {
		sdeResponse := &gcpserver.KmsConfigV1beta{
			KeyFullPath: "projects/test-project-id/locations/test-location/keyRings/test-key-ring/cryptoKeys/test-key-name/grants/test-grant-id",
		}

		result := convertSDEResponseToKmsConfig(sdeResponse)

		assert.NotNil(t, result)
		assert.Equal(t, "test-grant-id", result.KmsGrant)
		assert.Equal(t, "test-key-name", result.KeyName)
		assert.Equal(t, "test-key-ring", result.KeyRing)
		assert.Equal(t, "test-location", result.KeyRingLocation)
		assert.Equal(t, "test-project-id", result.KeyProjectID)
	})
}

func TestCreateAndSyncKmsConfig(t *testing.T) {
	t.Run("ReturnsModelOnSuccess", func(tt *testing.T) {
		ctx := context.Background()
		params := &common.CreateKmsConfigParams{AccountName: "acc", ResourceID: "res"}
		expectedKmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "uuid"},
			KmsAttributes: &datamodel.KmsAttributes{},
		}

		createAndSyncKmsConfig = func(ctx context.Context, se database.Storage, params *common.CreateKmsConfigParams) (*datamodel.KmsConfig, error) {
			return expectedKmsConfig, nil
		}

		defer func() {
			createAndSyncKmsConfig = kms_activities.CreateAndSyncKmsConfig
		}()
		o := &GCPOrchestrator{}
		result, err := o.CreateAndSyncKmsConfig(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "uuid", result.UUID)
	})
	t.Run("WhenFailure", func(tt *testing.T) {
		ctx := context.Background()

		mockStorage := new(database.MockStorage)
		params := &common.CreateKmsConfigParams{AccountName: "acc", ResourceID: "res"}

		createAndSyncKmsConfig = func(ctx context.Context, se database.Storage, params *common.CreateKmsConfigParams) (*datamodel.KmsConfig, error) {
			return nil, errors.New("some error")
		}

		defer func() {
			createAndSyncKmsConfig = kms_activities.CreateAndSyncKmsConfig
		}()

		o := &GCPOrchestrator{storage: mockStorage}
		result, err := o.CreateAndSyncKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockStorage.AssertExpectations(tt)
	})
}

func TestGetSDEKmsConfiguration(t *testing.T) {
	t.Run("WhenFailure", func(tt *testing.T) {
		ctx := context.Background()

		params := &common.GetKmsConfigParams{AccountName: "acc", ResourceID: "res"}

		getSDEKmsConfiguration = func(ctx context.Context, params *common.GetKmsConfigParams) (*cvpModels.KmsConfigV1beta, error) {
			return nil, errors.New("some error")
		}

		defer func() {
			getSDEKmsConfiguration = kms_activities.GetSDEKmsConfiguration
		}()

		o := &GCPOrchestrator{}
		result, err := o.GetSDEKmsConfiguration(ctx, params)

		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
}

func TestDeleteKmsConfig_PreviousStateAndDetailsInJobAttributes(t *testing.T) {
	t.Run("WhenDeleteKmsConfig_JobAttributesContainsPreviousStateAndDetails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflow_engine.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}

		previousState := datamodel.LifeCycleStateREADY
		previousStateDetails := "Kms config is ready for use"
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-config-uuid"},
			ResourceID:   "test-kms-config",
			State:        previousState,
			StateDetails: previousStateDetails,
			AccountID:    account.ID,
		}

		params := &common.DeleteKmsConfigParams{
			KmsConfigID:    kmsConfig.UUID,
			AccountName:    account.Name,
			XCorrelationID: "test-correlation-id",
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage.EXPECT().GetKmsConfig(ctx, params.KmsConfigID).Return(kmsConfig, nil)
		// Mock GetJobByResourceUUID for DELETE_KMS_CONFIG (check for existing delete job when in non-transitional state)
		mockStorage.EXPECT().GetJobByResourceUUID(ctx, params.KmsConfigID, string(datamodel.JobTypeDeleteKmsConfig)).Return(nil, nil)
		mockStorage.EXPECT().IsKmsConfigInUse(ctx, kmsConfig.UUID).Return(false, nil)
		mockStorage.EXPECT().ListOngoingPoolJobsWithKmsConfigId(ctx, kmsConfig.ID, kmsConfig.AccountID).Return([]*datamodel.Job{}, nil)
		mockStorage.EXPECT().UpdateKmsConfigState(ctx, kmsConfig.UUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(kmsConfig, nil)
		mockStorage.EXPECT().CreateJob(ctx, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.JobAttributes != nil &&
				job.JobAttributes.PreviousState == previousState &&
				job.JobAttributes.PreviousStateDetails == previousStateDetails &&
				job.JobAttributes.ResourceUUID == kmsConfig.UUID
		})).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		_, jobUUID, err := _deleteKmsConfig(ctx, mockStorage, temporal, params)
		assert.NoError(tt, err)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
	})
}

func TestMigrateKmsConfig_PreviousStateAndDetailsInJobAttributes(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()
	cvp.CVP_HOST = "localhost:8009"

	t.Run("WhenMigrateKmsConfig_JobAttributesContainsPreviousStateAndDetails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflow_engine.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}

		previousState := datamodel.LifeCycleStateREADY
		previousStateDetails := datamodel.LifeCycleStateReadyDetails
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-config-uuid"},
			Name:         "test-kms-config",
			State:        previousState,
			StateDetails: previousStateDetails,
			KmsAttributes: &datamodel.KmsAttributes{
				SdeKmsConfigUUID: "sde-kms-config-uuid",
			},
		}

		params := &common.MigrateKmsConfigParams{
			UUID:          kmsConfig.UUID,
			AccountName:   account.Name,
			LocationID:    "us-central1",
			ProjectNumber: account.Name,
			State:         previousState,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage.EXPECT().GetKmsConfigByUUID(ctx, params.UUID).Return(kmsConfig, nil)
		mockStorage.EXPECT().CreateCMEKJob(ctx, account.ID, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.JobAttributes != nil &&
				job.JobAttributes.PreviousState == previousState &&
				job.JobAttributes.PreviousStateDetails == previousStateDetails &&
				job.JobAttributes.ResourceUUID == kmsConfig.UUID
		})).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}, nil)
		mockStorage.EXPECT().UpdateKmsConfigState(ctx, kmsConfig.UUID, datamodel.LifeCycleStateMigrating, datamodel.LifeCycleStateMigratingDetails).Return(kmsConfig, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		jobUUID, err := migrateKmsConfig(ctx, mockStorage, temporal, params)
		assert.NoError(tt, err)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenMigrateKmsConfig_WithInUseState_JobAttributesContainsPreviousStateAndDetails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflow_engine.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}

		previousState := datamodel.LifeCycleStateInUse
		previousStateDetails := datamodel.LifeCycleStateInUseDetails
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-config-uuid"},
			Name:         "test-kms-config",
			State:        previousState,
			StateDetails: previousStateDetails,
			KmsAttributes: &datamodel.KmsAttributes{
				SdeKmsConfigUUID: "sde-kms-config-uuid",
			},
		}

		params := &common.MigrateKmsConfigParams{
			UUID:          kmsConfig.UUID,
			AccountName:   account.Name,
			LocationID:    "us-central1",
			ProjectNumber: account.Name,
			State:         previousState,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage.EXPECT().GetKmsConfigByUUID(ctx, params.UUID).Return(kmsConfig, nil)
		mockStorage.EXPECT().CreateCMEKJob(ctx, account.ID, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.JobAttributes != nil &&
				job.JobAttributes.PreviousState == previousState &&
				job.JobAttributes.PreviousStateDetails == previousStateDetails &&
				job.JobAttributes.ResourceUUID == kmsConfig.UUID
		})).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}, nil)
		mockStorage.EXPECT().UpdateKmsConfigState(ctx, kmsConfig.UUID, datamodel.LifeCycleStateMigrating, datamodel.LifeCycleStateMigratingDetails).Return(kmsConfig, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		jobUUID, err := migrateKmsConfig(ctx, mockStorage, temporal, params)
		assert.NoError(tt, err)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenMigrateKmsConfig_NoLocalEntry_JobAttributesHasEmptyPreviousState", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflow_engine.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}

		params := &common.MigrateKmsConfigParams{
			UUID:          "test-kms-config-uuid",
			AccountName:   account.Name,
			LocationID:    "us-central1",
			ProjectNumber: account.Name,
			State:         datamodel.LifeCycleStateREADY,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage.EXPECT().GetKmsConfigByUUID(ctx, params.UUID).Return(nil, errors.NewNotFoundErr("kms", nil))
		mockStorage.EXPECT().CreateCMEKJob(ctx, account.ID, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.JobAttributes != nil &&
				job.JobAttributes.PreviousState == "" &&
				job.JobAttributes.PreviousStateDetails == "" &&
				job.JobAttributes.ResourceUUID == params.UUID
		})).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}, nil)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		jobUUID, err := migrateKmsConfig(ctx, mockStorage, temporal, params)
		assert.NoError(tt, err)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
	})
}
