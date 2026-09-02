package gcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowEngineMock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"golang.org/x/net/context"
)

func TestGetHostGroup(t *testing.T) {
	t.Run("WhenHostGroupDoesNotExist", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		hg, err := orch.GetHostGroup(ctx, "non-existent-uuid", account.Name)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "host group not found")
		assert.Nil(tt, hg, "Expected nil volume")
	})
	t.Run("WhenHostGroupExist", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{UUID: "test-hg-uuid"},
			Name:      "test_hg",
			AccountID: account.ID,
		}
		err = store.DB().Create(hg).Error
		assert.NoError(tt, err, "Failed to create hg")

		hgResp, err := orch.GetHostGroup(ctx, hg.UUID, account.Name)
		assert.NoError(tt, err, "Failed to get hg")
		assert.NotNil(tt, hgResp)
	})
}

func TestGetMultipleHostGroups(t *testing.T) {
	t.Run("WhenGetMultipleHostGroupsAccountNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		hgResp, err := orch.GetMultipleHostGroups(ctx, "account", []string{})
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "account not found")
		}

		assert.Len(tt, hgResp, 0)
	})
	t.Run("WhenGetMultipleHostGroupsSuccess", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		hg1 := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{UUID: "test-hg-uuid1"},
			Name:      "test_hg",
			AccountID: account.ID,
		}
		err = store.DB().Create(hg1).Error
		assert.NoError(tt, err, "Failed to create hg")

		hg2 := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{UUID: "test-hg-uuid2"},
			Name:      "test_hg",
			AccountID: account.ID,
		}
		err = store.DB().Create(hg2).Error
		assert.NoError(tt, err, "Failed to create hg")

		hgResp, err := orch.GetMultipleHostGroups(ctx, account.Name, []string{hg1.UUID, hg2.UUID})
		assert.Nil(tt, err, "some error")
		assert.Len(tt, hgResp, 2)
	})
	t.Run("WhenGetMultipleHostGroupsNoHG", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		hgResp, err := orch.GetMultipleHostGroups(ctx, account.Name, []string{"a", "b"})
		assert.Nil(tt, err, "some error")
		assert.Len(tt, hgResp, 0)
	})
}

func TestGetHostGroupsByUUIDs(t *testing.T) {
	t.Run("WhenNoHostGroupsFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		hgResp, err := orch.GetHostGroupsByUUIDs(ctx, []string{"non-existent-uuid"})
		assert.NoError(tt, err)
		assert.Len(tt, hgResp, 0)
	})

	t.Run("WhenHostGroupsFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		hg1 := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{UUID: "test-hg-uuid1"},
			Name:      "hg-one",
			AccountID: account.ID,
		}
		err = store.DB().Create(hg1).Error
		assert.NoError(tt, err)

		hg2 := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{UUID: "test-hg-uuid2"},
			Name:      "hg-two",
			AccountID: account.ID,
		}
		err = store.DB().Create(hg2).Error
		assert.NoError(tt, err)

		hgResp, err := orch.GetHostGroupsByUUIDs(ctx, []string{hg1.UUID, hg2.UUID})
		assert.NoError(tt, err)
		assert.Len(tt, hgResp, 2)
	})

	t.Run("WhenHostGroupHasAccount", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{UUID: "test-hg-uuid"},
			Name:      "hg-with-account",
			AccountID: account.ID,
		}
		err = store.DB().Create(hg).Error
		assert.NoError(tt, err)

		hgResp, err := orch.GetHostGroupsByUUIDs(ctx, []string{hg.UUID})
		assert.NoError(tt, err)
		assert.Len(tt, hgResp, 1)
		assert.Equal(tt, "test_account", hgResp[0].AccountName)
		assert.Equal(tt, "hg-with-account", hgResp[0].Name)
	})

	t.Run("WhenStorageReturnsError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		origFn := getHostGroupsByUUIDs
		defer func() { getHostGroupsByUUIDs = origFn }()

		getHostGroupsByUUIDs = func(ctx context.Context, storage database.Storage, hostGroupUUIDs []string) ([]*models.HostGroup, error) {
			return nil, customerrors.New("database error")
		}

		orch := GCPOrchestrator{}

		hgResp, err := orch.GetHostGroupsByUUIDs(ctx, []string{"uuid-1"})
		assert.Error(tt, err)
		assert.Nil(tt, hgResp)
		assert.EqualError(tt, err, "database error")
	})

	t.Run("WhenStorageReturnsErrorDirectly", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetHostGroupsByUUIDs(mock.Anything, []string{"uuid-1"}).
			Return(nil, customerrors.New("storage failure"))

		hgResp, err := _getHostGroupsByUUIDs(ctx, mockStorage, []string{"uuid-1"})
		assert.Error(tt, err)
		assert.Nil(tt, hgResp)
	})
}

func TestDeleteHostGroups(t *testing.T) {
	t.Run("WhenDeleteHostGroupsNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		defer func() {
			deleteHostGroup = _deleteHostGroup
		}()

		deleteHostGroup = func(ctx context.Context, storage database.Storage, hostGroupUUID string, accountID string) (*models.HostGroup, error) {
			return nil, customerrors.NewNotFoundErr("host group", nil)
		}
		_, err = orch.DeleteHostGroup(ctx, &common.DeleteHostGroupParams{
			Name:        "non-existent-uuid",
			AccountName: account.Name,
		})
		assert.EqualError(tt, err, "host group not found")
	})
}

func TestUpdateHostGroup(t *testing.T) {
	t.Run("WhenUpdateHostGroupAccountNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		params := &common.UpdateHostGroupParams{
			AccountName:   "non-existent-account",
			HostGroupUUID: "test-hg-uuid",
			Description:   nillable.GetStringPtr("Updated description"),
			Hosts:         &[]string{"a", "b"},
		}

		_, _, err = orch.UpdateHostGroup(ctx, params)
		assert.Error(tt, err, "Expected error when account is not found")
	})
	t.Run("WhenUpdateHostGroupWorkflowFails", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{UUID: "test-hg-uuid"},
			Name:      "test_hg",
			AccountID: account.ID,
		}
		err = store.DB().Create(hg).Error
		assert.NoError(tt, err, "Failed to create host group")
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, vsaerrors.New("some error")).Once()

		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		params := &common.UpdateHostGroupParams{
			AccountName:   account.Name,
			HostGroupUUID: hg.UUID,
			Description:   nillable.GetStringPtr("Updated description"),
			Hosts:         &[]string{"a", "b"},
		}

		hgResp, jobUUID, err := orch.UpdateHostGroup(ctx, params)
		assert.EqualError(tt, err, "some error", "Expected error when workflow execution fails")
		assert.Nil(tt, hgResp, "Host group response should be nil")
		assert.Emptyf(tt, jobUUID, "Job UUID should be empty when workflow execution fails")
	})
	t.Run("WhenUpdateHostGroupDBFails", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{UUID: "test-hg-uuid"},
			Name:      "test_hg",
			AccountID: account.ID,
		}

		temporal := workflowEngineMock.NewMockTemporalTestClient(t)

		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		params := &common.UpdateHostGroupParams{
			AccountName:   account.Name,
			HostGroupUUID: hg.UUID,
			Description:   nillable.GetStringPtr("Updated description"),
			Hosts:         &[]string{"a", "b"},
		}

		hgResp, jobUUID, err := orch.UpdateHostGroup(ctx, params)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "host group not found", "Expected error when workflow execution fails")
		assert.Nil(tt, hgResp, "Host group response should be nil")
		assert.Emptyf(tt, jobUUID, "Job UUID should be empty when workflow execution fails")
	})

	t.Run("WhenUpdateHostGroupSuccess", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{UUID: "test-hg-uuid"},
			Name:      "test_hg",
			AccountID: account.ID,
		}
		err = store.DB().Create(hg).Error
		assert.NoError(tt, err, "Failed to create host group")
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		params := &common.UpdateHostGroupParams{
			AccountName:   account.Name,
			HostGroupUUID: hg.UUID,
			Description:   nillable.GetStringPtr("Updated description"),
			Hosts:         &[]string{"a", "b"},
		}

		hgResp, jobUUID, err := orch.UpdateHostGroup(ctx, params)
		assert.NoError(tt, err, "Failed to update host group")
		assert.NotNil(tt, hgResp, "Host group response should not be nil")
		assert.NotEmpty(tt, jobUUID, "Job UUID should not be empty")
		assert.Equal(tt, "Updated description", hgResp.Description, "Host group description should be updated")
	})
}
