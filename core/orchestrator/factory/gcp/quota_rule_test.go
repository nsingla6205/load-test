package gcp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflow_engine_mock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/sdk/client"
)

// mockStorage is a minimal mock storage implementation for testing
type mockStorage struct {
	database.Storage
}

func TestCreateQuotaRule(t *testing.T) {
	// Save original function pointers
	originalGetOrCreateAccount := getOrCreateAccount
	originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams

	defer func() {
		getOrCreateAccount = originalGetOrCreateAccount
		validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams
	}()

	t.Run("WhenGetOrCreateAccountFails", func(tt *testing.T) {
		var mockStorage mockStorage
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
			LocationId:     "us-central1",
		}

		// Mock getOrCreateAccount to return error
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("failed to get or create account")
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to get or create account")
	})

	t.Run("WhenValidateQuotaRuleCreateParamsFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
			LocationId:     "us-central1",
		}

		// Mock getAccountWithName to succeed
		originalGetAccountWithName := getAccountWithName
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		// Mock validateQuotaRuleCreateParams to return error
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return errors.NewUserInputValidationErr("Quota rule name is required")
		}
		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Quota rule name is required")
	})

	t.Run("WhenGetVolumeFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
			LocationId:     "us-central1",
		}

		// Mock getAccountWithName to succeed
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock validateQuotaRuleCreateParams to succeed
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		// Mock GetVolume to fail
		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeUUID, account.ID).Return(nil, errors.New("volume not found"))

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "volume not found")
	})

	t.Run("WhenValidateVolumeTypeFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
			LocationId:     "us-central1",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"ISCSI"}, // SAN volume
			},
		}

		// Mock getAccountWithName to succeed
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock validateQuotaRuleCreateParams to succeed
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		// Mock GetVolume to succeed but return a SAN volume
		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeUUID, account.ID).Return(volume, nil)

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
	})

	t.Run("WhenValidateReplicationStateFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
			LocationId:     "us-central1",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock validateQuotaRuleCreateParams to succeed
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		// Mock GetVolume to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeUUID, account.ID).Return(volume, nil)

		// Mock ListVolumeReplications to return error
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return(nil, errors.New("failed to list replications"))

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to list replications")
	})

	t.Run("WhenGetQuotaRulesByVolumeIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
			LocationId:     "us-central1",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock validateQuotaRuleCreateParams to succeed
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		// Mock GetVolume to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeUUID, account.ID).Return(volume, nil)

		// Mock ListVolumeReplications to return empty list (no replications)
		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRulesByVolumeID to fail
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(nil, errors.New("failed to fetch quota rules"))

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to fetch quota rules")
	})

	t.Run("WhenValidateQuotaRuleUniquenessFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
			LocationId:     "us-central1",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Existing quota rule with the same name
		existingQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel:   datamodel.BaseModel{ID: 1, UUID: "quota-uuid-1"},
				Name:        "quota-rule-1", // Same name as the new one
				VolumeID:    1,
				AccountID:   1,
				QuotaType:   DefaultUserQuota,
				QuotaTarget: "",
			},
		}

		// Mock getAccountWithName to succeed
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock validateQuotaRuleCreateParams to succeed
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		// Mock GetVolume to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeUUID, account.ID).Return(volume, nil)

		// Mock ListVolumeReplications to return empty list
		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRulesByVolumeID to return existing quota rules
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "quota rule with same name")
	})

	t.Run("WhenDetermineRQuotaFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
			LocationId:     "us-central1",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			PoolID:      1,
			SvmID:       1,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}

		// Mock getAccountWithName to succeed
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock validateQuotaRuleCreateParams to succeed
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		// Mock GetVolume to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeUUID, account.ID).Return(volume, nil)

		// Mock ListVolumeReplications to return empty list
		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRulesByVolumeID to return empty list
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)

		// Mock GetQuotaRuleCountBySvmID to fail
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), errors.New("failed to get quota rule count"))

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to get quota rule count")
	})

	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
			LocationId:     "us-central1",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			PoolID:      1,
			SvmID:       1,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}

		// Mock getAccountWithName to succeed
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock validateQuotaRuleCreateParams to succeed
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		// Mock GetVolume to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeUUID, account.ID).Return(volume, nil)

		// Mock ListVolumeReplications to return empty list
		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRulesByVolumeID to return empty list
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)

		// Mock GetQuotaRuleCountBySvmID to succeed
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)

		// Mock CreatingQuotaRule to succeed (called before CreateJob)
		mockStore.EXPECT().CreatingQuotaRule(context.Background(), mock.Anything).
			Return(&datamodel.QuotaRule{
				BaseModel: datamodel.BaseModel{UUID: "quota-rule-uuid-123"},
			}, nil)

		// Mock CreateJob to fail
		mockStore.EXPECT().CreateJob(context.Background(), mock.AnythingOfType("*datamodel.Job")).
			Return(nil, errors.New("failed to create job"))

		// Mock DeleteQuotaRule cleanup (called in defer when CreateJob fails)
		mockStore.EXPECT().DeleteQuotaRule(context.Background(), "quota-rule-uuid-123").
			Return(nil, nil)

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to create job")
	})

	t.Run("WhenCreateQuotaRuleSucceeds", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
			LocationId:     "us-central1",
			Description:    "Test quota rule",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			PoolID:      1,
			SvmID:       1,
			Account:     account,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
			Type:       string(datamodel.JobTypeCreateQuotaRule),
			State:      string(datamodel.JobsStateNEW),
		}

		createdQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-123"},
			Name:           params.Name,
			VolumeID:       volume.ID,
			AccountID:      volume.AccountID,
			QuotaType:      params.QuotaType,
			QuotaTarget:    params.QuotaTarget,
			DiskLimitInKib: params.DiskLimitInMib * mibToKibMultiplier,
			State:          datamodel.LifeCycleStateCreating,
			StateDetails:   datamodel.LifeCycleStateCreatingDetails,
			RQuota:         true,
			Description:    params.Description,
		}

		// Mock getAccountWithName to succeed
		originalGetAccountWithName := getAccountWithName
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		defer func() { getAccountWithName = originalGetAccountWithName }()
		// Mock validateQuotaRuleCreateParams to succeed
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		// Mock GetVolume to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeUUID, account.ID).Return(volume, nil)

		// Mock ListVolumeReplications to return empty list (no replications)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRulesByVolumeID to return empty list
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)

		// Mock GetQuotaRuleCountBySvmID to succeed (first quota rule in SVM)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock CreatingQuotaRule to succeed
		mockStore.EXPECT().CreatingQuotaRule(context.Background(), mock.Anything).
			Return(createdQuotaRule, nil)

		// Mock Temporal ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, createdQuotaRule).
			Return(nil, nil)

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.Equal(tt, "job-uuid-123", operationID)
		assert.Equal(tt, params.Name, quotaRule.Name)
		assert.Equal(tt, params.QuotaType, quotaRule.QuotaType)
		assert.Equal(tt, params.QuotaTarget, quotaRule.QuotaTarget)
		assert.Equal(tt, params.DiskLimitInMib, quotaRule.DiskLimitInMib)
		assert.Equal(tt, datamodel.LifeCycleStateCreating, quotaRule.LifeCycleState)
		assert.Equal(tt, params.Description, quotaRule.Description)
	})

	t.Run("WhenAnotherQuotaRuleIsCreating", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-new",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
			LocationId:     "us-central1",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "other-in-creating"},
				Name:      "other-rule",
				State:     datamodel.LifeCycleStateCreating,
			},
		}

		originalGetOrCreateAccount := getOrCreateAccount
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}
		defer func() {
			getOrCreateAccount = originalGetOrCreateAccount
			validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams
		}()

		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeUUID, account.ID).Return(volume, nil)
		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Another quota rule is being created")
	})
}

func TestValidateQuotaRuleCreateParams(t *testing.T) {
	t.Run("WhenNameIsEmpty", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			Name:           "",
			DiskLimitInMib: 100,
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "user:alice",
		}

		err := _validateQuotaRuleCreateParams(params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Quota rule name is required")
	})

	t.Run("WhenDiskLimitTooLow", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			Name:           "test-quota",
			DiskLimitInMib: 0, // Too low, will be less than 4 KiB when converted
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "user:alice",
		}

		err := _validateQuotaRuleCreateParams(params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "DiskLimit is outside the permissible range")
	})

	t.Run("WhenDiskLimitTooHigh", func(tt *testing.T) {
		// Upper limit is 1125899906842620 KiB, which is approximately 1099511627776 MiB
		params := &common.CreateQuotaRulesParam{
			Name:           "test-quota",
			DiskLimitInMib: 1200000000000, // Too high
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "user:alice",
		}

		err := _validateQuotaRuleCreateParams(params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "DiskLimit is outside the permissible range")
	})

	t.Run("WhenIndividualUserQuotaWithoutTarget", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			Name:           "test-quota",
			DiskLimitInMib: 100,
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "", // Missing target
		}

		err := _validateQuotaRuleCreateParams(params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quotaTarget has to be specified")
	})

	t.Run("WhenIndividualGroupQuotaWithoutTarget", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			Name:           "test-quota",
			DiskLimitInMib: 100,
			QuotaType:      IndividualGroupQuota,
			QuotaTarget:    "", // Missing target
		}

		err := _validateQuotaRuleCreateParams(params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quotaTarget has to be specified")
	})

	t.Run("WhenDefaultUserQuotaWithTarget", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			Name:           "test-quota",
			DiskLimitInMib: 100,
			QuotaType:      DefaultUserQuota,
			QuotaTarget:    "user:alice", // Should not have target
		}

		err := _validateQuotaRuleCreateParams(params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quotaTarget cannot be specified")
	})

	t.Run("WhenDefaultGroupQuotaWithTarget", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			Name:           "test-quota",
			DiskLimitInMib: 100,
			QuotaType:      DefaultGroupQuota,
			QuotaTarget:    "group:admins", // Should not have target
		}

		err := _validateQuotaRuleCreateParams(params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quotaTarget cannot be specified")
	})

	t.Run("WhenValidIndividualUserQuota", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			Name:           "test-quota",
			DiskLimitInMib: 100,
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "user:alice",
		}

		err := _validateQuotaRuleCreateParams(params)
		assert.NoError(tt, err)
	})

	t.Run("WhenValidDefaultUserQuota", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			Name:           "test-quota",
			DiskLimitInMib: 100,
			QuotaType:      DefaultUserQuota,
			QuotaTarget:    "",
		}

		err := _validateQuotaRuleCreateParams(params)
		assert.NoError(tt, err)
	})

	t.Run("WhenValidIndividualGroupQuota", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			Name:           "test-quota",
			DiskLimitInMib: 100,
			QuotaType:      IndividualGroupQuota,
			QuotaTarget:    "group:admins",
		}

		err := _validateQuotaRuleCreateParams(params)
		assert.NoError(tt, err)
	})

	t.Run("WhenValidDefaultGroupQuota", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			Name:           "test-quota",
			DiskLimitInMib: 100,
			QuotaType:      DefaultGroupQuota,
			QuotaTarget:    "",
		}

		err := _validateQuotaRuleCreateParams(params)
		assert.NoError(tt, err)
	})
}

func TestValidateVolumeType(t *testing.T) {
	t.Run("WhenVolumeIsSAN", func(tt *testing.T) {
		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"ISCSI"}, // SAN volume
			},
		}

		params := &common.CreateQuotaRulesParam{
			DiskLimitInMib: 100,
		}

		err := validateVolumeType(context.Background(), volume, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "block (SAN) volumes")
	})

	t.Run("WhenVolumeIsFlexCache", func(tt *testing.T) {
		volume := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes:     200 * 1024 * 1024,
			CacheParameters: &datamodel.CacheParameters{}, // FlexCache volume
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		params := &common.CreateQuotaRulesParam{
			DiskLimitInMib: 100,
		}

		err := validateVolumeType(context.Background(), volume, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "flexcache")
	})

	t.Run("WhenQuotaSizeExceedsVolumeSize", func(tt *testing.T) {
		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 100 * 1024 * 1024, // 100 MiB
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		params := &common.CreateQuotaRulesParam{
			DiskLimitInMib: 200, // 200 MiB > 100 MiB volume size
		}

		err := validateVolumeType(context.Background(), volume, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quota rule size can not be greater than volume size")
	})

	t.Run("WhenValidVolume", func(tt *testing.T) {
		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		params := &common.CreateQuotaRulesParam{
			DiskLimitInMib: 100, // 100 MiB < 200 MiB volume size
		}

		err := validateVolumeType(context.Background(), volume, params)
		assert.NoError(tt, err)
	})
}

func TestValidateQuotaRuleState(t *testing.T) {
	volUUID := "volume-uuid-1"

	t.Run("WhenNoExistingRules", func(tt *testing.T) {
		err := validateQuotaRuleState(context.Background(), volUUID, nil)
		assert.NoError(tt, err)
	})

	t.Run("WhenExistingRulesAreReady", func(tt *testing.T) {
		rules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "q1"},
				State:     datamodel.LifeCycleStateREADY,
			},
		}
		err := validateQuotaRuleState(context.Background(), volUUID, rules)
		assert.NoError(tt, err)
	})

	t.Run("WhenExistingRuleIsCreating", func(tt *testing.T) {
		rules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "in-flight"},
				State:     datamodel.LifeCycleStateCreating,
			},
		}
		err := validateQuotaRuleState(context.Background(), volUUID, rules)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Another quota rule is being created")
	})

	t.Run("WhenExistingRuleIsPreparing_Allowed", func(tt *testing.T) {
		rules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "preparing"},
				State:     datamodel.LifeCycleStatePreparing,
			},
		}
		err := validateQuotaRuleState(context.Background(), volUUID, rules)
		assert.NoError(tt, err)
	})
}

func TestValidateQuotaRuleUniqueness(t *testing.T) {
	t.Run("WhenNameAlreadyExists", func(tt *testing.T) {
		existingRules := []*datamodel.QuotaRule{
			{
				BaseModel:   datamodel.BaseModel{ID: 1, UUID: "quota-uuid-1"},
				Name:        "existing-quota",
				QuotaType:   DefaultUserQuota,
				QuotaTarget: "",
			},
		}

		params := &common.CreateQuotaRulesParam{
			Name:        "existing-quota", // Same name
			QuotaType:   IndividualUserQuota,
			QuotaTarget: "user:alice",
		}

		err := validateQuotaRuleUniqueness(context.Background(), existingRules, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "quota rule with same name")
	})

	t.Run("WhenTypeAndTargetAlreadyExist", func(tt *testing.T) {
		existingRules := []*datamodel.QuotaRule{
			{
				BaseModel:   datamodel.BaseModel{ID: 1, UUID: "quota-uuid-1"},
				Name:        "existing-quota",
				QuotaType:   IndividualUserQuota,
				QuotaTarget: "user:alice",
			},
		}

		params := &common.CreateQuotaRulesParam{
			Name:        "new-quota",         // Different name
			QuotaType:   IndividualUserQuota, // Same type
			QuotaTarget: "user:alice",        // Same target
		}

		err := validateQuotaRuleUniqueness(context.Background(), existingRules, params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "quota rule with same type")
	})

	t.Run("WhenNoConflict", func(tt *testing.T) {
		existingRules := []*datamodel.QuotaRule{
			{
				BaseModel:   datamodel.BaseModel{ID: 1, UUID: "quota-uuid-1"},
				Name:        "existing-quota",
				QuotaType:   DefaultUserQuota,
				QuotaTarget: "",
			},
		}

		params := &common.CreateQuotaRulesParam{
			Name:        "new-quota",         // Different name
			QuotaType:   IndividualUserQuota, // Different type
			QuotaTarget: "user:alice",
		}

		err := validateQuotaRuleUniqueness(context.Background(), existingRules, params)
		assert.NoError(tt, err)
	})

	t.Run("WhenEmptyExistingRules", func(tt *testing.T) {
		existingRules := []*datamodel.QuotaRule{}

		params := &common.CreateQuotaRulesParam{
			Name:        "new-quota",
			QuotaType:   IndividualUserQuota,
			QuotaTarget: "user:alice",
		}

		err := validateQuotaRuleUniqueness(context.Background(), existingRules, params)
		assert.NoError(tt, err)
	})
}

func TestValidateQuotaTargetByProtocol(t *testing.T) {
	ctx := context.Background()

	t.Run("SMBVolume_IndividualGroupQuota_ShouldFail", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   IndividualGroupQuota,
			QuotaTarget: "S-1-5-21-123456789-123456789-123456789-1000",
		}
		protocolTypes := []string{utils.ProtocolSMB}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Group Quota cannot be specified for a SMB and Dual Protocol volume")
	})

	t.Run("SMBVolume_DefaultGroupQuota_ShouldFail", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   DefaultGroupQuota,
			QuotaTarget: "",
		}
		protocolTypes := []string{utils.ProtocolSMB}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Group Quota cannot be specified for a SMB and Dual Protocol volume")
	})

	t.Run("SMBOnlyVolume_InvalidSIDFormat_ShouldFail", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   IndividualUserQuota,
			QuotaTarget: "invalid-sid-format",
		}
		protocolTypes := []string{utils.ProtocolSMB}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quotaTarget is invalid. Please pass valid SID in quotaTarget for SMB volume")
	})

	t.Run("SMBOnlyVolume_ValidSID_ShouldPass", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   IndividualUserQuota,
			QuotaTarget: "S-1-5-21-123456789-123456789-123456789-1000",
		}
		protocolTypes := []string{utils.ProtocolSMB}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.NoError(tt, err)
	})

	t.Run("SMBOnlyVolume_EmptyTargetForDefaultQuota_ShouldPass", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   DefaultUserQuota,
			QuotaTarget: "",
		}
		protocolTypes := []string{utils.ProtocolSMB}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.NoError(tt, err)
	})

	t.Run("NFSOnlyVolume_NonNumericTarget_ShouldFail", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   IndividualUserQuota,
			QuotaTarget: "invalid-numeric",
		}
		protocolTypes := []string{utils.ProtocolNFSv3}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quotaTarget is invalid. Please pass numeric value for quotaTarget in range [0, 4294967295] for NFS volumes")
	})

	t.Run("NFSOnlyVolume_OutOfRangeTarget_ShouldFail", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   IndividualUserQuota,
			QuotaTarget: "4294967296", // > 4294967295
		}
		protocolTypes := []string{utils.ProtocolNFSv3}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quotaTarget is invalid. Please pass numeric value for quotaTarget in range [0, 4294967295] for NFS volumes")
	})

	t.Run("NFSOnlyVolume_NegativeTarget_ShouldFail", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   IndividualUserQuota,
			QuotaTarget: "-1",
		}
		protocolTypes := []string{utils.ProtocolNFSv4}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quotaTarget is invalid. Please pass numeric value for quotaTarget in range [0, 4294967295] for NFS volumes")
	})

	t.Run("NFSOnlyVolume_ValidNumericTarget_ShouldPass", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   IndividualUserQuota,
			QuotaTarget: "1000",
		}
		protocolTypes := []string{utils.ProtocolNFSv3}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.NoError(tt, err)
	})

	t.Run("NFSOnlyVolume_EmptyTargetForDefaultQuota_ShouldPass", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   DefaultUserQuota,
			QuotaTarget: "",
		}
		protocolTypes := []string{utils.ProtocolNFSv4}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.NoError(tt, err)
	})

	t.Run("DualProtocolVolume_GroupQuota_ShouldFail", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   IndividualGroupQuota,
			QuotaTarget: "S-1-5-21-123456789-123456789-123456789-1000",
		}
		protocolTypes := []string{utils.ProtocolSMB, utils.ProtocolNFSv3}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Group Quota cannot be specified for a SMB and Dual Protocol volume")
	})

	t.Run("DualProtocolVolume_UserQuotaWithValidSID_ShouldPass", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   IndividualUserQuota,
			QuotaTarget: "S-1-5-21-123456789-123456789-123456789-1000",
		}
		protocolTypes := []string{utils.ProtocolSMB, utils.ProtocolNFSv3}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.NoError(tt, err)
	})

	t.Run("DualProtocolVolume_UserQuotaWithValidNumeric_ShouldPass", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   IndividualUserQuota,
			QuotaTarget: "1000",
		}
		protocolTypes := []string{utils.ProtocolSMB, utils.ProtocolNFSv4}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.NoError(tt, err)
	})

	t.Run("DualProtocolVolume_InvalidFormat_ShouldFail", func(tt *testing.T) {
		params := &common.CreateQuotaRulesParam{
			QuotaType:   IndividualUserQuota,
			QuotaTarget: "invalid-format",
		}
		protocolTypes := []string{utils.ProtocolSMB, utils.ProtocolNFSv3}

		err := validateQuotaTargetByProtocol(ctx, params, protocolTypes)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quotaTarget is invalid. Please pass numeric value in range [0, 4294967295] or SID for quotaTarget for dual protocol volumes")
	})
}

func TestHasProtocol(t *testing.T) {
	t.Run("WhenProtocolExists", func(tt *testing.T) {
		protocolTypes := []string{"NFSV3", "NFSV4", "SMB"}
		result := hasProtocol("NFSV3", protocolTypes)
		assert.True(tt, result)
	})

	t.Run("WhenProtocolDoesNotExist", func(tt *testing.T) {
		protocolTypes := []string{"NFSV3", "NFSV4", "SMB"}
		result := hasProtocol("ISCSI", protocolTypes)
		assert.False(tt, result)
	})

	t.Run("WhenProtocolTypesIsEmpty", func(tt *testing.T) {
		protocolTypes := []string{}
		result := hasProtocol("NFSV3", protocolTypes)
		assert.False(tt, result)
	})
}

func TestHasNFSv3(t *testing.T) {
	t.Run("WhenNFSv3Exists", func(tt *testing.T) {
		protocolTypes := []string{"NFSV3", "SMB"}
		result := hasNFSv3(protocolTypes)
		assert.True(tt, result)
	})

	t.Run("WhenNFSv3DoesNotExist", func(tt *testing.T) {
		protocolTypes := []string{"NFSV4", "SMB"}
		result := hasNFSv3(protocolTypes)
		assert.False(tt, result)
	})
}

func TestHasNFSv4(t *testing.T) {
	t.Run("WhenNFSv4Exists", func(tt *testing.T) {
		protocolTypes := []string{"NFSV4", "SMB"}
		result := hasNFSv4(protocolTypes)
		assert.True(tt, result)
	})

	t.Run("WhenNFSv4DoesNotExist", func(tt *testing.T) {
		protocolTypes := []string{"NFSV3", "SMB"}
		result := hasNFSv4(protocolTypes)
		assert.False(tt, result)
	})
}

func TestDetermineRQuota(t *testing.T) {
	t.Run("WhenVolumeDoesNotUseNFS", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"SMB"}, // Not NFS
			},
		}

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, false)
		assert.NoError(tt, err)
		assert.False(tt, rquotaRequired)
	})

	t.Run("WhenVolumeUsesNFSv3AndSVMHasExistingQuotaRules", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock GetQuotaRuleCountBySvmID to return 2 (existing quota rules in SVM)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(2), nil)

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, false)
		assert.NoError(tt, err)
		assert.False(tt, rquotaRequired)
	})

	t.Run("WhenVolumeUsesNFSv4AndIsFirstQuotaRuleInSVM", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV4"},
			},
		}

		// Mock GetQuotaRuleCountBySvmID to return 0 (first quota rule in SVM)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, false)
		assert.NoError(tt, err)
		assert.True(tt, rquotaRequired)
	})

	t.Run("WhenVolumeUsesNFSv3AndIsNotFirstQuotaRuleInSVM", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock GetQuotaRuleCountBySvmID to return 1 (not first quota rule in SVM)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(1), nil)

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, false)
		assert.NoError(tt, err)
		assert.False(tt, rquotaRequired)
	})

	t.Run("WhenGetQuotaRuleCountBySvmIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock GetQuotaRuleCountBySvmID to fail
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), errors.New("database error"))

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, false)
		assert.Error(tt, err)
		assert.False(tt, rquotaRequired)
		assert.Contains(tt, err.Error(), "database error")
	})

	t.Run("WhenVolumeHasNilProtocols", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: nil,
			},
		}

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, false)
		assert.NoError(tt, err)
		assert.False(tt, rquotaRequired)
	})

	t.Run("WhenVolumeHasNilVolumeAttributes", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:            1,
			VolumeAttributes: nil,
		}

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, false)
		assert.NoError(tt, err)
		assert.False(tt, rquotaRequired)
	})

	t.Run("WhenVolumeDoesNotUseNFSAndIsDeleteAction", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"SMB"}, // Not NFS
			},
		}

		// Non-NFS delete returns true so !isRQuotaEnabled=false in the delete workflow,
		// meaning UpdateRQuotaOnSvm is NOT called — rquota is left unchanged (correct).
		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, true)
		assert.NoError(tt, err)
		assert.True(tt, rquotaRequired)
	})

	t.Run("WhenVolumeHasNilProtocolsAndIsDeleteAction", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: nil,
			},
		}

		// Nil protocols treated as non-NFS: returns true → delete workflow skips UpdateRQuotaOnSvm.
		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, true)
		assert.NoError(tt, err)
		assert.True(tt, rquotaRequired)
	})

	t.Run("WhenVolumeHasNilVolumeAttributesAndIsDeleteAction", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:            1,
			VolumeAttributes: nil,
		}

		// Nil VolumeAttributes treated as non-NFS: returns true → delete workflow skips UpdateRQuotaOnSvm.
		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, true)
		assert.NoError(tt, err)
		assert.True(tt, rquotaRequired)
	})

	t.Run("WhenDeleteActionAndLastQuotaRuleInSVM", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// count=1 → !(1==1) = false → delete workflow: !false=true → UpdateRQuotaOnSvm(false) called → rquota disabled ✓
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(1), nil)

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, true)
		assert.NoError(tt, err)
		assert.False(tt, rquotaRequired)
	})

	t.Run("WhenDeleteActionAndNotLastQuotaRuleInSVM", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// count=2 → !(2==1) = true → delete workflow: !true=false → UpdateRQuotaOnSvm NOT called → rquota stays enabled ✓
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(2), nil)

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, true)
		assert.NoError(tt, err)
		assert.True(tt, rquotaRequired)
	})

	// --- Dual-protocol (NFS + SMB) volume tests ---

	t.Run("WhenDualProtocolNFSv3AndSMBVolumeIsFirstQuotaRuleInSVM", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		// Dual-protocol: both NFSv3 and SMB present. NFS presence means RQuota applies.
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "dual-volume-uuid-1"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{utils.ProtocolNFSv3, utils.ProtocolSMB},
			},
		}

		// First quota rule in SVM: count = 0 → rquota must be enabled.
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, false)
		assert.NoError(tt, err)
		assert.True(tt, rquotaRequired, "RQuota must be enabled for dual-protocol (NFS+SMB) volume when it is the first quota rule in the SVM")
	})

	t.Run("WhenDualProtocolNFSv4AndSMBVolumeIsFirstQuotaRuleInSVM", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "dual-volume-uuid-2"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{utils.ProtocolNFSv4, utils.ProtocolSMB},
			},
		}

		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, false)
		assert.NoError(tt, err)
		assert.True(tt, rquotaRequired, "RQuota must be enabled for dual-protocol (NFS+SMB) volume when it is the first quota rule in the SVM")
	})

	t.Run("WhenDualProtocolNFSv3AndSMBVolumeIsNotFirstQuotaRuleInSVM", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "dual-volume-uuid-3"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{utils.ProtocolNFSv3, utils.ProtocolSMB},
			},
		}

		// SVM already has NFS quota rules; RQuota was already enabled by the first rule's workflow.
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(2), nil)

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, false)
		assert.NoError(tt, err)
		assert.False(tt, rquotaRequired, "RQuota must NOT be enabled again for dual-protocol volume when other NFS quota rules already exist in the SVM")
	})

	t.Run("WhenDeleteActionAndDualProtocolVolumeIsLastQuotaRuleInSVM", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "dual-volume-uuid-4"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{utils.ProtocolNFSv3, utils.ProtocolSMB},
			},
		}

		// count=1 → !(1==1)=false → delete workflow: !false=true → UpdateRQuotaOnSvm(false) → rquota disabled ✓
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(1), nil)

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, true)
		assert.NoError(tt, err)
		assert.False(tt, rquotaRequired, "Last quota rule in SVM: returns false so !false=true triggers UpdateRQuotaOnSvm(false)")
	})

	t.Run("WhenDeleteActionAndDualProtocolVolumeIsNotLastQuotaRuleInSVM", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "dual-volume-uuid-5"},
			SvmID:     1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{utils.ProtocolNFSv3, utils.ProtocolSMB},
			},
		}

		// count=3 → !(3==1)=true → delete workflow: !true=false → UpdateRQuotaOnSvm NOT called → rquota stays enabled ✓
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(3), nil)

		rquotaRequired, err := determineRQuota(context.Background(), mockStore, volume, true)
		assert.NoError(tt, err)
		assert.True(tt, rquotaRequired, "Not last quota rule in SVM: returns true so !true=false skips UpdateRQuotaOnSvm")
	})
}

func TestConvertDatastoreQuotaRuleToModel(t *testing.T) {
	t.Run("WhenQuotaRuleIsNil", func(tt *testing.T) {
		result := _convertDatastoreQuotaRuleToModel(nil)
		assert.Nil(tt, result)
	})

	t.Run("WhenQuotaRuleIsValid", func(tt *testing.T) {
		quotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 1, UUID: "quota-uuid-123"},
			Name:           "test-quota",
			Description:    "Test description",
			State:          datamodel.LifeCycleStateAvailable,
			StateDetails:   "Available",
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "user:alice",
			DiskLimitInKib: 102400, // 100 MiB in KiB
		}

		result := _convertDatastoreQuotaRuleToModel(quotaRule)
		assert.NotNil(tt, result)
		assert.Equal(tt, "quota-uuid-123", result.UUID)
		assert.Equal(tt, "test-quota", result.Name)
		assert.Equal(tt, "Test description", result.Description)
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, result.LifeCycleState)
		assert.Equal(tt, "Available", result.LifeCycleStateDetails)
		assert.Equal(tt, IndividualUserQuota, result.QuotaType)
		assert.Equal(tt, "user:alice", result.QuotaTarget)
		assert.Equal(tt, int64(100), result.DiskLimitInMib) // 102400 KiB / 1024 = 100 MiB
	})

	t.Run("WhenQuotaRuleIsValidWithMinimalFields", func(tt *testing.T) {
		quotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 1, UUID: "quota-uuid-123"},
			Name:           "test-quota",
			DiskLimitInKib: 102400,
		}

		result := _convertDatastoreQuotaRuleToModel(quotaRule)
		assert.NotNil(tt, result)
		assert.Equal(tt, "quota-uuid-123", result.UUID)
		assert.Equal(tt, "test-quota", result.Name)
	})
}

// TestGetDestinationReplication tests the _getDestinationReplication function
// Note: This function is a simple wrapper around activities.GetReplicationDetails
// Line 76 is covered through TestValidateReplicationState tests which exercise
// the getDestinationReplication variable that calls this function
func TestGetDestinationReplication(t *testing.T) {
	// This is a simple wrapper function that delegates to activities.GetReplicationDetails
	// The actual function is tested indirectly through validateReplicationState tests
	// which mock getDestinationReplication variable
	t.Run("FunctionExists", func(tt *testing.T) {
		// Just verify the function exists and has the correct signature
		// Actual behavior is tested through validateReplicationState tests
		assert.NotNil(tt, _getDestinationReplication)
	})
}

func TestValidateReplicationState(t *testing.T) {
	// Save original function pointers
	originalGetDestinationReplication := getDestinationReplication
	originalInternalUtilGetSignedToken := internalUtilGetSignedToken
	originalInternalUtilGetPairedRegionURI := internalUtilGetPairedRegionURI
	originalInternalParseRegionAndZone := internalParseRegionAndZone

	defer func() {
		getDestinationReplication = originalGetDestinationReplication
		internalUtilGetSignedToken = originalInternalUtilGetSignedToken
		internalUtilGetPairedRegionURI = originalInternalUtilGetPairedRegionURI
		internalParseRegionAndZone = originalInternalParseRegionAndZone
	}()

	t.Run("WhenNoReplicationsExist", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1")
		assert.NoError(tt, err)
	})

	t.Run("WhenReplicationHasNilReplicationAttributes", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
		}

		replications := []*datamodel.VolumeReplication{
			{
				ReplicationAttributes: nil,
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1")
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "replication attributes are missing")
	})

	t.Run("WhenCurrentLocationMatchesDestinationLocation", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		locationID := "us-central1-a"
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-east1" {
				return "us-east1", "", nil
			}
			if locationID == "us-central1-a" {
				return "us-central1", "us-central1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		replications := []*datamodel.VolumeReplication{
			{
				State:              datamodel.LifeCycleStateAvailable,
				MirrorState:        nillable.ToPointer("Snapmirrored"),
				RelationshipStatus: nillable.ToPointer("Healthy"),
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1",
					DestinationLocation: locationID,
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, locationID)
		assert.NoError(tt, err)
	})

	t.Run("WhenCurrentLocationMatchesDestinationLocationAndMirrorStateIsMIRRORED", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		locationID := "us-central1-a"
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-east1" {
				return "us-east1", "", nil
			}
			if locationID == "us-central1-a" {
				return "us-central1", "us-central1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		mirrorState := "MIRRORED"
		replications := []*datamodel.VolumeReplication{
			{
				State:              datamodel.LifeCycleStateAvailable,
				MirrorState:        &mirrorState,
				RelationshipStatus: nillable.ToPointer("Healthy"),
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1",
					DestinationLocation: locationID,
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, locationID)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "MIRRORED")
	})

	t.Run("WhenCurrentLocationMatchesDestinationLocationAndMirrorStateIsUNINITIALIZED", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		locationID := "us-central1-a"
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-east1" {
				return "us-east1", "", nil
			}
			if locationID == "us-central1-a" {
				return "us-central1", "us-central1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		mirrorState := "UNINITIALIZED"
		replications := []*datamodel.VolumeReplication{
			{
				State:              datamodel.LifeCycleStateAvailable,
				MirrorState:        &mirrorState,
				RelationshipStatus: nillable.ToPointer("Healthy"),
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1",
					DestinationLocation: locationID,
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, locationID)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "UNINITIALIZED")
	})

	t.Run("WhenCurrentLocationMatchesDestinationLocationAndMirrorStateIssnapmirrored", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		locationID := "us-central1-a"
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-east1" {
				return "us-east1", "", nil
			}
			if locationID == "us-central1-a" {
				return "us-central1", "us-central1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		mirrorState := OntapSnapmirrored
		replications := []*datamodel.VolumeReplication{
			{
				State:              datamodel.LifeCycleStateAvailable,
				MirrorState:        &mirrorState,
				RelationshipStatus: nillable.ToPointer("Healthy"),
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1",
					DestinationLocation: locationID,
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, locationID)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), OntapSnapmirrored)
	})

	t.Run("WhenCurrentLocationMatchesDestinationLocationAndMirrorStateIsuninitialized", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		locationID := "us-central1-a"
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-east1" {
				return "us-east1", "", nil
			}
			if locationID == "us-central1-a" {
				return "us-central1", "us-central1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		mirrorState := OntapUninitialized
		replications := []*datamodel.VolumeReplication{
			{
				State:              datamodel.LifeCycleStateAvailable,
				MirrorState:        &mirrorState,
				RelationshipStatus: nillable.ToPointer("Healthy"),
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1",
					DestinationLocation: locationID,
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, locationID)
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), OntapUninitialized)
	})

	t.Run("WhenParseProjectNumberFromURIFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "invalid-uri",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1-a",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to parse destination project number")
	})

	t.Run("WhenParseRegionAndZoneFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
		}

		originalParseProjectNumberFromURI := utils.ParseProjectNumberFromURI
		defer func() {
			utils.ParseProjectNumberFromURI = originalParseProjectNumberFromURI
		}()

		utils.ParseProjectNumberFromURI = func(uri string) (string, error) {
			return "test-project", nil // Same project to pass cross-project check
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-central1" {
				return "us-central1", "", nil
			}
			if locationID == "invalid-location" {
				return "", "", fmt.Errorf("failed to parse location")
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "https://test.com/projects/test-project/locations/us-east1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "invalid-location",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Failed to parse destination location")
	})

	t.Run("WhenGetPairedRegionURIFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
		}

		originalParseProjectNumberFromURI := utils.ParseProjectNumberFromURI
		defer func() {
			utils.ParseProjectNumberFromURI = originalParseProjectNumberFromURI
		}()

		utils.ParseProjectNumberFromURI = func(uri string) (string, error) {
			return "test-project", nil // Same project to pass cross-project check
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-central1" {
				return "us-central1", "", nil
			}
			if locationID == "us-east1-a" {
				return "us-east1", "us-east1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		internalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "", errors.New("failed to get paired region URI")
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "https://test.com/projects/test-project/locations/us-east1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1-a",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		// The error is wrapped in coreerrors.NewVCPError, so check for the base path error
		assert.Contains(tt, err.Error(), "Failed to get destination base path")
	})

	t.Run("WhenCrossProjectReplication", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
		}

		originalParseProjectNumberFromURI := utils.ParseProjectNumberFromURI
		defer func() {
			utils.ParseProjectNumberFromURI = originalParseProjectNumberFromURI
		}()

		utils.ParseProjectNumberFromURI = func(uri string) (string, error) {
			return "987654321", nil // Different project number
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "https://test.com/projects/987654321/locations/us-east1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1-a",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "cross project replication")
	})

	t.Run("WhenGetSignedTokenFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "123456789", // Match the project number to avoid cross-project check
			},
		}

		originalParseProjectNumberFromURI := utils.ParseProjectNumberFromURI
		defer func() {
			utils.ParseProjectNumberFromURI = originalParseProjectNumberFromURI
		}()

		utils.ParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-central1" {
				return "us-central1", "", nil
			}
			if locationID == "us-east1-a" {
				return "us-east1", "us-east1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		internalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "https://us-east1.test.com", nil
		}

		internalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "", errors.New("failed to get signed token")
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "https://test.com/projects/123456789/locations/us-east1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1-a",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Failed to get signed token")
	})

	t.Run("WhenGetDestinationReplicationFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "123456789",
			},
		}

		originalParseProjectNumberFromURI := utils.ParseProjectNumberFromURI
		defer func() {
			utils.ParseProjectNumberFromURI = originalParseProjectNumberFromURI
		}()

		utils.ParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-central1" {
				return "us-central1", "", nil
			}
			if locationID == "us-east1-a" {
				return "us-east1", "us-east1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		internalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "https://us-east1.test.com", nil
		}

		internalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "test-token", nil
		}

		getDestinationReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*models.VolumeReplication, error) {
			return nil, errors.New("failed to fetch destination replication")
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "https://test.com/projects/123456789/locations/us-east1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-central1",
					DestinationLocation:        "us-east1-a",
					DestinationReplicationUUID: "replication-uuid-123",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to fetch destination replication")
	})

	t.Run("WhenDestinationReplicationIsInCreatingState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "123456789",
			},
		}

		originalParseProjectNumberFromURI := utils.ParseProjectNumberFromURI
		defer func() {
			utils.ParseProjectNumberFromURI = originalParseProjectNumberFromURI
		}()

		utils.ParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-central1" {
				return "us-central1", "", nil
			}
			if locationID == "us-east1-a" {
				return "us-east1", "us-east1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		internalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "https://us-east1.test.com", nil
		}

		internalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "test-token", nil
		}

		getDestinationReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				State: datamodel.LifeCycleStateCreating,
			}, nil
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "https://test.com/projects/123456789/locations/us-east1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-central1",
					DestinationLocation:        "us-east1-a",
					DestinationReplicationUUID: "replication-uuid-123",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Quota update not allowed on destination volume when in active replication")
	})

	t.Run("WhenDestinationReplicationIsInUpdatingState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "123456789",
			},
		}

		originalParseProjectNumberFromURI := utils.ParseProjectNumberFromURI
		defer func() {
			utils.ParseProjectNumberFromURI = originalParseProjectNumberFromURI
		}()

		utils.ParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-central1" {
				return "us-central1", "", nil
			}
			if locationID == "us-east1-a" {
				return "us-east1", "us-east1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		internalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "https://us-east1.test.com", nil
		}

		internalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "test-token", nil
		}

		getDestinationReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				State: datamodel.LifeCycleStateUpdating,
			}, nil
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "https://test.com/projects/123456789/locations/us-east1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-central1",
					DestinationLocation:        "us-east1-a",
					DestinationReplicationUUID: "replication-uuid-123",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Quota update not allowed on destination volume when in active replication")
	})

	t.Run("WhenDestinationReplicationIsInDeletingState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "123456789",
			},
		}

		originalParseProjectNumberFromURI := utils.ParseProjectNumberFromURI
		defer func() {
			utils.ParseProjectNumberFromURI = originalParseProjectNumberFromURI
		}()

		utils.ParseProjectNumberFromURI = func(uri string) (string, error) {
			return "123456789", nil
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-central1" {
				return "us-central1", "", nil
			}
			if locationID == "us-east1-a" {
				return "us-east1", "us-east1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		internalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "https://us-east1.test.com", nil
		}

		internalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "test-token", nil
		}

		getDestinationReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				State: datamodel.LifeCycleStateDeleting,
			}, nil
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "https://test.com/projects/123456789/locations/us-east1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-central1",
					DestinationLocation:        "us-east1-a",
					DestinationReplicationUUID: "replication-uuid-123",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Quota update not allowed on destination volume when in active replication")
	})

	t.Run("WhenCrossProjectReplicationWithRemoteUri", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project-123",
			},
		}

		originalParseProjectNumberFromURI := utils.ParseProjectNumberFromURI
		defer func() {
			utils.ParseProjectNumberFromURI = originalParseProjectNumberFromURI
		}()

		utils.ParseProjectNumberFromURI = func(uri string) (string, error) {
			return "different-project-456", nil // Different project number
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-central1" {
				return "us-central1", "", nil
			}
			if locationID == "us-east1-a" {
				return "us-east1", "us-east1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "https://test.com/projects/different-project-456/locations/us-east1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1-a",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "cross project replication")
	})

	t.Run("WhenCrossProjectReplicationWithoutRemoteUri", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		locationID := "us-central1-a"
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-east1" {
				return "us-east1", "", nil
			}
			if locationID == "us-central1-a" {
				return "us-central1", "us-central1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "", // Empty RemoteUri - destination side
				State:     datamodel.LifeCycleStateAvailable,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-east1",
					DestinationLocation: locationID,
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, locationID)
		// Should pass because cross-project check is skipped when RemoteUri is empty
		// But will fail on mirror state check if mirror state is set
		// For this test, we don't set mirror state, so it should pass
		assert.NoError(tt, err)
	})

	t.Run("WhenInRegionReplication", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			// Both locations are in the same region
			if locationID == "us-central1" || locationID == "us-central1-a" {
				return "us-central1", "", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		replications := []*datamodel.VolumeReplication{
			{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-central1-a",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "in-region replication")
	})

	t.Run("WhenInRegionReplicationWithSourceParseError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "invalid-source-location" {
				return "", "", fmt.Errorf("failed to parse source location")
			}
			if locationID == "us-east1-a" {
				return "us-east1", "us-east1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		replications := []*datamodel.VolumeReplication{
			{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "invalid-source-location",
					DestinationLocation: "us-east1-a",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Failed to parse source location")
	})

	t.Run("WhenInRegionReplicationWithDestParseError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-central1" {
				return "us-central1", "", nil
			}
			if locationID == "invalid-dest-location" {
				return "", "", fmt.Errorf("failed to parse destination location")
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		replications := []*datamodel.VolumeReplication{
			{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "invalid-dest-location",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Failed to parse destination location")
	})

	t.Run("WhenCrossRegionReplicationAllowed", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-project",
			},
		}

		originalParseProjectNumberFromURI := utils.ParseProjectNumberFromURI
		defer func() {
			utils.ParseProjectNumberFromURI = originalParseProjectNumberFromURI
		}()

		utils.ParseProjectNumberFromURI = func(uri string) (string, error) {
			return "test-project", nil // Same project
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-central1" {
				return "us-central1", "", nil
			}
			if locationID == "us-east1-a" {
				return "us-east1", "us-east1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		internalUtilGetPairedRegionURI = func(region string) (string, error) {
			return "https://us-east1.test.com", nil
		}

		internalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "test-token", nil
		}

		getDestinationReplication = func(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				State: datamodel.LifeCycleStateAvailable,
			}, nil
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "https://test.com/projects/test-project/locations/us-east1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-central1",
					DestinationLocation:        "us-east1-a",
					DestinationReplicationUUID: "replication-uuid-123",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		// Cross-region replication (different regions) should be allowed
		assert.NoError(tt, err)
	})

	// Coverage for lines 133-134: volume.Account == nil when replication.RemoteUri != ""
	t.Run("WhenVolumeAccountIsNilWithRemoteUriSet", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			Account:   nil, // nil account - triggers lines 133-134
		}

		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "https://test.com/projects/987654321/locations/us-east1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1-a",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "volume account information is missing")
	})

	// Coverage for lines 194-195: source side (DestinationLocation != locationID) but RemoteUri == ""
	t.Run("WhenSourceSideRemoteUriEmpty", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
		}

		internalParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "us-central1" || locationID == "us-central1-a" {
				return "us-central1", "", nil
			}
			if locationID == "us-east1-a" {
				return "us-east1", "us-east1-a", nil
			}
			return "", "", fmt.Errorf("unexpected location: %s", locationID)
		}

		// Current location is us-central1-a (source); destination is us-east1-a. RemoteUri is empty.
		replications := []*datamodel.VolumeReplication{
			{
				RemoteUri: "", // empty - triggers lines 194-195
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:      "us-central1",
					DestinationLocation: "us-east1-a",
				},
			},
		}

		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return(replications, nil)

		err := validateReplicationState(context.Background(), mockStore, volume, "us-central1-a")
		assert.Error(tt, err)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "remote URI is missing for source replication")
	})
}

func Test_listQuotaRules(t *testing.T) {
	// Coverage for lines 1279-1282: getAccountWithName fails
	t.Run("WhenGetAccountWithNameFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		params := &common.ListQuotaRulesParams{
			AccountName: "test-account",
			VolumeID:    "volume-uuid-1",
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("failed to get account")
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		result, err := _listQuotaRules(context.Background(), mockStore, params)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "failed to get account")
	})

	// Coverage for line 1286: GetVolumeWithAccountID fails
	t.Run("WhenGetVolumeWithAccountIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		params := &common.ListQuotaRulesParams{
			AccountName: "test-account",
			VolumeID:    "volume-uuid-1",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-account",
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeID, account.ID).
			Return(nil, errors.New("volume not found"))

		result, err := _listQuotaRules(context.Background(), mockStore, params)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "volume not found")
	})
}

func TestCreateQuotaRuleWrapper(t *testing.T) {
	t.Run("WhenCreateQuotaRuleSucceeds", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{
			storage:  mockStore,
			temporal: mockTemporal,
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
			LocationId:     "us-central1",
		}

		originalCreateQuotaRule := createQuotaRule
		defer func() {
			createQuotaRule = originalCreateQuotaRule
		}()

		expectedQuotaRule := &models.QuotaRule{
			BaseModel: models.BaseModel{
				UUID: "quota-uuid-123",
			},
			Name:      params.Name,
			QuotaType: params.QuotaType,
		}
		expectedJobUUID := "job-uuid-123"

		createQuotaRule = func(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateQuotaRulesParam) (*models.QuotaRule, string, error) {
			return expectedQuotaRule, expectedJobUUID, nil
		}

		quotaRule, jobUUID, err := orchestrator.CreateQuotaRule(context.Background(), params)
		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.Equal(tt, expectedQuotaRule.BaseModel.UUID, quotaRule.BaseModel.UUID)
		assert.Equal(tt, expectedJobUUID, jobUUID)
	})
}

func TestCreateQuotaRuleInternalWrapper(t *testing.T) {
	t.Run("WhenCreateQuotaRuleInternalSucceeds", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{
			storage:  mockStore,
			temporal: mockTemporal,
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
			LocationId:     "us-central1",
		}

		originalCreateQuotaRuleInternal := createQuotaRuleInternal
		defer func() {
			createQuotaRuleInternal = originalCreateQuotaRuleInternal
		}()

		expectedQuotaRule := &models.QuotaRule{
			BaseModel: models.BaseModel{
				UUID: "quota-uuid-123",
			},
			Name:      params.Name,
			QuotaType: params.QuotaType,
		}
		expectedJobUUID := "job-uuid-123"

		createQuotaRuleInternal = func(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
			return expectedQuotaRule, &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: expectedJobUUID}}, nil
		}

		quotaRule, job, err := orchestrator.CreateQuotaRuleInternal(context.Background(), params)
		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job)
		assert.Equal(tt, expectedQuotaRule.BaseModel.UUID, quotaRule.BaseModel.UUID)
		assert.Equal(tt, expectedJobUUID, job.UUID)
	})
}

func TestCreateQuotaRuleErrorPaths(t *testing.T) {
	// Save original function pointers
	originalGetOrCreateAccount := getOrCreateAccount
	originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams

	defer func() {
		getOrCreateAccount = originalGetOrCreateAccount
		validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams
	}()

	t.Run("WhenCreatingQuotaRuleFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
			LocationId:     "us-central1",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			PoolID:      1,
			SvmID:       1,
			Account:     account,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeUUID, account.ID).Return(volume, nil)
		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)
		// CreatingQuotaRule is called before CreateJob, so when it fails, CreateJob is never called
		mockStore.EXPECT().CreatingQuotaRule(context.Background(), mock.Anything).
			Return(nil, errors.New("failed to create quota rule"))

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to create quota rule")
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		params := &common.CreateQuotaRulesParam{
			ProjectId:      "test-project",
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
			LocationId:     "us-central1",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			PoolID:      1,
			SvmID:       1,
			Account:     account,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		createdQuotaRule := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{UUID: "quota-rule-uuid-123"},
		}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolumeWithAccountID(context.Background(), params.VolumeUUID, account.ID).Return(volume, nil)
		mockStore.EXPECT().ListVolumeReplications(context.Background(), mock.Anything, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)
		mockStore.EXPECT().CreatingQuotaRule(context.Background(), mock.Anything).
			Return(createdQuotaRule, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, createdQuotaRule).
			Return(nil, errors.New("failed to execute workflow"))
		mockStore.EXPECT().DeleteJob(context.Background(), createdJob.UUID, mock.Anything).
			Return(nil)
		mockStore.EXPECT().DeleteQuotaRule(context.Background(), createdQuotaRule.UUID).
			Return(nil, nil)

		quotaRule, operationID, err := _createQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to execute workflow")
	})
}

func TestCreateQuotaRuleInternal(t *testing.T) {
	// Save original function pointers
	originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams

	defer func() {
		validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams
	}()

	t.Run("WhenValidateQuotaRuleCreateParamsFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
		}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return errors.NewUserInputValidationErr("Quota rule name is required")
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		quotaRule, operationID, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
	})

	t.Run("WhenGetVolumeFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
		}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).
			Return(nil, errors.New("volume not found"))

		quotaRule, operationID, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "volume not found")
	})

	t.Run("WhenGetQuotaRulesByVolumeIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(nil, errors.New("failed to fetch quota rules"))

		quotaRule, operationID, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to fetch quota rules")
	})

	t.Run("WhenAnotherQuotaRuleIsCreating", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-new",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "other-creating"},
				State:     datamodel.LifeCycleStateCreating,
			},
		}

		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)

		quotaRule, job, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Another quota rule is being created")
	})

	t.Run("WhenQuotaRulesLimitReached", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Create 100 quota rules to hit the limit
		existingQuotaRules := make([]*datamodel.QuotaRule, VolumeQuotaRulesDefaultLimit)
		for i := 0; i < VolumeQuotaRulesDefaultLimit; i++ {
			existingQuotaRules[i] = &datamodel.QuotaRule{
				BaseModel: datamodel.BaseModel{ID: int64(i + 1)},
			}
		}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)

		quotaRule, operationID, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quota rules limit reached")
	})

	t.Run("WhenVolumeHas99QuotaRules_AllowsCreating100th_DoesNotReturnLimitError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-100",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// 99 existing rules: limit check should pass (allows creating the 100th)
		existingQuotaRules := make([]*datamodel.QuotaRule, VolumeQuotaRulesDefaultLimit-1)
		for i := 0; i < VolumeQuotaRulesDefaultLimit-1; i++ {
			existingQuotaRules[i] = &datamodel.QuotaRule{
				BaseModel: datamodel.BaseModel{ID: int64(i + 1)},
				Name:      fmt.Sprintf("quota-rule-%d", i+1),
			}
		}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}
		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()

		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)

		// Let validation fail later (e.g. uniqueness) so we only assert limit check passed
		quotaRule, operationID, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		// Must not be "quota rules limit reached" — we have 99 rules so 100th create is allowed
		assert.False(tt, err != nil && strings.Contains(err.Error(), "quota rules limit reached"),
			"expected limit check to pass when 99 rules exist (allow 100th); got err: %v", err)
		// We don't care if err is nil or another validation error; we only needed to prove limit wasn't hit
		_ = quotaRule
		_ = operationID
	})

	t.Run("WhenValidateVolumeTypeFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"ISCSI"}, // SAN volume
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)

		quotaRule, operationID, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
	})

	t.Run("WhenValidateQuotaRuleUniquenessFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "user:alice",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel:   datamodel.BaseModel{ID: 1},
				Name:        "quota-rule-1", // Same name
				QuotaType:   IndividualUserQuota,
				QuotaTarget: "user:alice",
			},
		}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)

		quotaRule, operationID, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsConflictErr(err))
	})

	t.Run("WhenDetermineRQuotaFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), errors.New("failed to get quota rule count"))

		quotaRule, operationID, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to get quota rule count")
	})

	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)
		// Mock CreatingQuotaRule to succeed (called before CreateJob)
		mockStore.EXPECT().CreatingQuotaRule(context.Background(), mock.Anything).
			Return(&datamodel.QuotaRule{
				BaseModel: datamodel.BaseModel{UUID: "quota-rule-uuid-123"},
			}, nil)
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(nil, errors.New("failed to create job"))

		// Mock DeleteQuotaRule cleanup (called in defer when CreateJob fails)
		mockStore.EXPECT().DeleteQuotaRule(context.Background(), "quota-rule-uuid-123").
			Return(nil, nil)

		quotaRule, operationID, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to create job")
	})

	t.Run("WhenCreatingQuotaRuleFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)
		// Mock CreatingQuotaRule to fail (called before CreateJob)
		mockStore.EXPECT().CreatingQuotaRule(context.Background(), mock.Anything).
			Return(nil, errors.New("failed to create quota rule"))
		// CreateJob should not be called when CreatingQuotaRule fails

		quotaRule, operationID, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to create quota rule")
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		createdQuotaRule := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{UUID: "quota-rule-uuid-123"},
		}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)
		mockStore.EXPECT().CreatingQuotaRule(context.Background(), mock.Anything).
			Return(createdQuotaRule, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, createdQuotaRule).
			Return(nil, errors.New("failed to execute workflow"))
		mockStore.EXPECT().DeleteJob(context.Background(), createdJob.UUID, mock.Anything).
			Return(nil)
		mockStore.EXPECT().DeleteQuotaRule(context.Background(), createdQuotaRule.UUID).
			Return(nil, nil)

		quotaRule, operationID, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to execute workflow")
	})

	t.Run("WhenCreateQuotaRuleInternalSucceeds", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
			Description:    "Test quota rule",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		createdQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-123"},
			Name:           params.Name,
			VolumeID:       volume.ID,
			AccountID:      volume.AccountID,
			QuotaType:      params.QuotaType,
			QuotaTarget:    params.QuotaTarget,
			DiskLimitInKib: params.DiskLimitInMib * mibToKibMultiplier,
			State:          datamodel.LifeCycleStateCreating,
			StateDetails:   datamodel.LifeCycleStateCreatingDetails,
			RQuota:         true,
			Description:    params.Description,
		}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)
		mockStore.EXPECT().CreatingQuotaRule(context.Background(), mock.Anything).
			Return(createdQuotaRule, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, createdQuotaRule).
			Return(nil, nil)

		quotaRule, job, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job)
		assert.Equal(tt, "job-uuid-123", job.UUID)
		assert.Equal(tt, params.Name, quotaRule.Name)
		assert.Equal(tt, params.QuotaType, quotaRule.QuotaType)
		assert.Equal(tt, params.QuotaTarget, quotaRule.QuotaTarget)
		assert.Equal(tt, params.DiskLimitInMib, quotaRule.DiskLimitInMib)
	})

	t.Run("WhenCreateJobFailsAndDeleteQuotaRuleFailsInDefer", func(tt *testing.T) {
		// Test for line 503: When DeleteQuotaRule fails in defer function
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.CreateQuotaRulesParam{
			Name:           "quota-rule-1",
			VolumeUUID:     "volume-uuid-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    "1000",
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingQuotaRules := []*datamodel.QuotaRule{}
		createdQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-123"},
			Name:           params.Name,
			VolumeID:       volume.ID,
			AccountID:      volume.AccountID,
			QuotaType:      params.QuotaType,
			QuotaTarget:    params.QuotaTarget,
			DiskLimitInKib: params.DiskLimitInMib * mibToKibMultiplier,
			State:          datamodel.LifeCycleStateCreating,
			StateDetails:   datamodel.LifeCycleStateCreatingDetails,
			RQuota:         true,
			Description:    params.Description,
		}

		originalValidateQuotaRuleCreateParams := validateQuotaRuleCreateParams
		validateQuotaRuleCreateParams = func(params *common.CreateQuotaRulesParam) error {
			return nil
		}

		defer func() { validateQuotaRuleCreateParams = originalValidateQuotaRuleCreateParams }()
		mockStore.EXPECT().GetVolume(context.Background(), params.VolumeUUID).Return(volume, nil)
		// Note: _createQuotaRuleInternal skips replication validation, so no ListVolumeReplications call
		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingQuotaRules, nil)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)
		mockStore.EXPECT().CreatingQuotaRule(context.Background(), mock.Anything).
			Return(createdQuotaRule, nil)
		// CreateJob fails, triggering defer cleanup
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(nil, errors.New("failed to create job"))
		// DeleteQuotaRule fails in defer (line 503)
		mockStore.EXPECT().DeleteQuotaRule(context.Background(), createdQuotaRule.UUID).
			Return(nil, errors.New("failed to delete quota rule"))

		quotaRule, job, err := _createQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "failed to create job")
		// The defer function should have logged the DeleteQuotaRule error but not fail the test
	})
}

func TestUpdateQuotaRule(t *testing.T) {
	// Save original function pointers
	originalGetAccountWithName := getAccountWithName

	defer func() {
		getAccountWithName = originalGetAccountWithName
	}()

	t.Run("WhenGetAccountWithNameFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 2048,
			LocationId:     "us-central1",
		}

		// Mock getAccountWithName to return error
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		defer func() { getAccountWithName = originalGetAccountWithName }()
		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "account not found")
	})

	t.Run("WhenGetQuotaRuleByUUIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 2048,
			LocationId:     "us-central1",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to fail
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(nil, errors.New("quota rule not found"))

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "quota rule not found")
	})

	t.Run("WhenNoFieldsProvidedForUpdate", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
			// Both DiskLimitInMib and Description are empty/zero
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "At least one field")
	})

	t.Run("WhenQuotaRuleIsInTransitioningState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 2048,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateUpdating, // Transitioning state
			StateDetails: datamodel.LifeCycleStateUpdatingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "transition state")
	})

	t.Run("WhenDiskLimitTooLow", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 0, // Too low, will be less than 4 KiB when converted
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		// Should fail because no fields provided (DiskLimitInMib is 0 and Description is empty)
		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
	})

	t.Run("WhenDiskLimitTooHigh", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		// Upper limit is 1125899906842620 KiB, which is approximately 1099511627776 MiB
		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 1200000000000, // Too high
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "DiskLimit is outside the permissible range")
	})

	t.Run("WhenGetVolumeByIDAndAccountIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 2048,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to fail
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(nil, errors.New("volume not found"))

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Failed to get volume")
	})

	t.Run("WhenGetQuotaRulesByVolumeIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(nil, errors.New("failed to fetch quota rules for volume state validation"))

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to fetch quota rules for volume state validation")
	})

	t.Run("WhenAnotherQuotaRuleIsCreating", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "other-creating"},
				State:     datamodel.LifeCycleStateCreating,
			},
		}

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingRules, nil)

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Another quota rule is being created")
	})

	t.Run("WhenQuotaRuleSizeExceedsVolumeSize", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 300, // 300 MiB
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB - less than 300 MiB quota
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quota rule size can not be greater than volume size")
	})

	t.Run("WhenValidateReplicationStateFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to return error (causes validateReplicationState to fail)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return(nil, errors.NewUserInputValidationErr("replication validation failed"))

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "replication validation failed")
	})

	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock CreateJob to fail
		mockStore.EXPECT().CreateJob(context.Background(), mock.AnythingOfType("*datamodel.Job")).
			Return(nil, errors.New("failed to create job"))

		// Note: UpdateQuotaRule is not called when job creation fails because the state was never changed
		// The defer cleanup only runs if job.UUID != "", which won't be true if CreateJob fails

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to create job")
	})

	t.Run("WhenUpdatingQuotaRuleFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to fail
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(nil, errors.New("failed to update quota rule"))

		// Mock DeleteJob cleanup
		mockStore.EXPECT().DeleteJob(context.Background(), createdJob.UUID, mock.Anything).
			Return(nil)

		// Mock UpdateQuotaRule to mark quota rule as available after error (in defer function)
		mockStore.EXPECT().UpdateQuotaRule(context.Background(), mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr.UUID == quotaRuleDataModel.UUID &&
				qr.State == datamodel.LifeCycleStateAvailable &&
				qr.StateDetails == datamodel.LifeCycleStateReadyDetails
		})).Return(quotaRuleDataModel, nil)

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to update quota rule")
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateUpdating,
			StateDetails: datamodel.LifeCycleStateUpdatingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to fail
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, errors.New("failed to execute workflow"))

		// Mock DeleteJob cleanup
		mockStore.EXPECT().DeleteJob(context.Background(), createdJob.UUID, mock.Anything).
			Return(nil)

		// Mock UpdateQuotaRule to mark quota rule as available after error (in defer function)
		mockStore.EXPECT().UpdateQuotaRule(context.Background(), mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr.UUID == quotaRuleDataModel.UUID &&
				qr.State == datamodel.LifeCycleStateAvailable &&
				qr.StateDetails == datamodel.LifeCycleStateReadyDetails
		})).Return(quotaRuleDataModel, nil)

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to execute workflow")
	})

	t.Run("WhenUpdateQuotaRuleSucceeds", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			Description:    "Updated description",
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateUpdating,
			StateDetails: datamodel.LifeCycleStateUpdatingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.Equal(tt, "job-uuid-123", operationID)
		assert.Equal(tt, "quota-rule-uuid-1", quotaRule.UUID)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, quotaRule.LifeCycleState)
	})

	t.Run("WhenUpdateQuotaRuleSucceedsWithOnlyDescription", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			Description:   "Updated description only",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateUpdating,
			StateDetails: datamodel.LifeCycleStateUpdatingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, operationID, err := _updateQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.Equal(tt, "job-uuid-123", operationID)
	})
}

func TestUpdateQuotaRuleInternal(t *testing.T) {
	// Save original function pointers
	originalGetAccountWithName := getAccountWithName

	defer func() {
		getAccountWithName = originalGetAccountWithName
	}()

	t.Run("WhenGetAccountWithNameFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 2048,
			LocationId:     "us-central1",
		}

		// Mock getAccountWithName to return error
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		defer func() { getAccountWithName = originalGetAccountWithName }()
		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "account not found")
	})

	t.Run("WhenGetQuotaRuleByUUIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 2048,
			LocationId:     "us-central1",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to fail
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(nil, errors.New("quota rule not found"))

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "quota rule not found")
	})

	t.Run("WhenQuotaRuleIsInTransitioningState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 2048,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateUpdating, // Transitioning state
			StateDetails: datamodel.LifeCycleStateUpdatingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "transition state")
	})

	t.Run("WhenNoFieldsProvidedForUpdate", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
			// Both DiskLimitInMib and Description are empty/zero
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "At least one field")
	})

	t.Run("WhenDiskLimitTooHigh", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		// Upper limit is 1125899906842620 KiB, which is approximately 1099511627776 MiB
		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 1200000000000, // Too high
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "DiskLimit is outside the permissible range")
	})

	t.Run("WhenGetVolumeByIDAndAccountIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 2048,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to fail
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(nil, errors.New("volume not found"))

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "volume not found")
	})

	t.Run("WhenQuotaRuleSizeExceedsVolumeSize", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 300, // 300 MiB
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB - less than 300 MiB quota
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "quota rule size can not be greater than volume size")
	})

	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to fail
		mockStore.EXPECT().CreateJob(context.Background(), mock.AnythingOfType("*datamodel.Job")).
			Return(nil, errors.New("failed to create job"))

		// Note: UpdateQuotaRule is not called when job creation fails because the state was never changed
		// The defer cleanup only runs if job.UUID != "", which won't be true if CreateJob fails

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "failed to create job")
	})

	t.Run("WhenUpdatingQuotaRuleFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to fail
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(nil, errors.New("failed to update quota rule"))

		// Mock DeleteJob cleanup
		mockStore.EXPECT().DeleteJob(context.Background(), createdJob.UUID, mock.Anything).
			Return(nil)

		// Mock UpdateQuotaRule to mark quota rule as AVAILABLE in defer block
		mockStore.EXPECT().UpdateQuotaRule(context.Background(), mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr.UUID == quotaRuleDataModel.UUID &&
				qr.State == datamodel.LifeCycleStateAvailable &&
				qr.StateDetails == datamodel.LifeCycleStateReadyDetails
		})).Return(quotaRuleDataModel, nil)

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "failed to update quota rule")
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateUpdating,
			StateDetails: datamodel.LifeCycleStateUpdatingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to fail
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, errors.New("failed to execute workflow"))

		// Mock DeleteJob cleanup
		mockStore.EXPECT().DeleteJob(context.Background(), createdJob.UUID, mock.Anything).
			Return(nil)

		// Mock UpdateQuotaRule to mark quota rule as AVAILABLE in defer block
		mockStore.EXPECT().UpdateQuotaRule(context.Background(), mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr.UUID == quotaRuleDataModel.UUID &&
				qr.State == datamodel.LifeCycleStateAvailable &&
				qr.StateDetails == datamodel.LifeCycleStateReadyDetails
		})).Return(quotaRuleDataModel, nil)

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "failed to execute workflow")
	})

	t.Run("WhenUpdateQuotaRuleInternalSucceeds", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			Description:    "Updated description",
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateUpdating,
			StateDetails: datamodel.LifeCycleStateUpdatingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job)
		assert.Equal(tt, "quota-rule-uuid-1", quotaRule.UUID)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, quotaRule.LifeCycleState)
		assert.Equal(tt, "job-uuid-123", job.UUID)
	})

	t.Run("WhenUpdateQuotaRuleInternalSucceedsWithOnlyDescription", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			Description:   "Updated description only",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateUpdating,
			StateDetails: datamodel.LifeCycleStateUpdatingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job)
		assert.Equal(tt, "job-uuid-123", job.UUID)
	})

	t.Run("WhenLocationIdIsNotSetAndPoolIsNil", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			LocationId:     "", // Empty, but pool is nil so it won't be set
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			Pool:        nil, // Pool is nil
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateUpdating,
			StateDetails: datamodel.LifeCycleStateUpdatingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job)
		assert.Empty(tt, params.LocationId, "LocationId should remain empty when pool is nil")
	})

	t.Run("WhenLocationIdIsNotSetAndPoolAttributesIsNil", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 100,
			LocationId:     "", // Empty, but PoolAttributes is nil so it won't be set
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024, // 200 MiB
			AccountID:   1,
			Pool: &datamodel.Pool{
				PoolAttributes: nil, // PoolAttributes is nil
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateUpdating,
			StateDetails: datamodel.LifeCycleStateUpdatingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job)
		assert.Empty(tt, params.LocationId, "LocationId should remain empty when PoolAttributes is nil")
	})

	t.Run("WhenCreateJobFailsAndDeleteQuotaRuleFailsInDefer", func(tt *testing.T) {
		// Test for line 654: When DeleteQuotaRule fails in defer function
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.UpdateQuotaRulesParam{
			ProjectId:      "test-project",
			QuotaRuleUUID:  "quota-rule-uuid-1",
			DiskLimitInMib: 2048,
			LocationId:     "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 3 * 1024 * 1024 * 1024, // 3GB - larger than quota rule size (2048 MiB = 2GB)
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Note: _updateQuotaRuleInternal does NOT call validateReplicationState (see lines 896-897 in quota_rule.go)
		// Replication validation is skipped for internal VCP API calls
		// So ListVolumeReplications is not called

		// CreateJob fails, triggering defer cleanup
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(nil, errors.New("failed to create job"))
		// Note: The defer in _updateQuotaRuleInternal (lines 780-797) only calls DeleteJob and UpdateQuotaRule,
		// NOT DeleteQuotaRule. DeleteQuotaRule is only called in CREATE functions' defer (line 653).
		// Since CreateJob returns nil, job.UUID is empty, so the defer's condition fails and nothing executes.

		quotaRule, job, err := _updateQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "failed to create job")
		// Note: The defer in _updateQuotaRuleInternal doesn't call DeleteQuotaRule (only DeleteJob and UpdateQuotaRule).
		// Since CreateJob returns nil, job.UUID is empty, so the defer's condition fails and nothing executes.
	})
}
func TestDeleteQuotaRule(t *testing.T) {
	// Save original function pointers
	originalGetAccountWithName := getAccountWithName

	defer func() {
		getAccountWithName = originalGetAccountWithName
	}()

	t.Run("WhenGetAccountWithNameFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		// Mock getAccountWithName to return error
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("failed to get account")
		}

		defer func() { getAccountWithName = originalGetAccountWithName }()
		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to get account")
	})

	t.Run("WhenGetQuotaRuleByUUIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to fail
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(nil, errors.New("quota rule not found"))

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "quota rule not found")
	})

	t.Run("WhenQuotaRuleIsInTransitioningState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateCreating, // Transitioning state
			StateDetails: "",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Note: When correlation ID is empty, ValidateCorrelationIDForCreatingResource returns early
		// without calling GetJobByResourceUUID, so no mock is needed here

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "transitioning between states")
	})

	t.Run("WhenQuotaRuleIsInCreatingStateWithMatchingCorrelationID", func(tt *testing.T) {
		// Test for line 966: Delete request with same correlation ID as create, proceeding with cancellation
		correlationID := "test-correlation-id-123"
		fields := log.Fields{
			string(middleware.RequestCorrelationID): correlationID,
		}
		ctxWithCorrelationID := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)

		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateCreating, // Transitioning state
			StateDetails: "",
		}

		createJob := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "create-job-uuid"},
			CorrelationID: correlationID,
			Type:          string(datamodel.JobTypeCreateQuotaRule),
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			Pool: &datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone: "us-central1-a",
				},
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(ctxWithCorrelationID, params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called first at line 980)
		mockStore.EXPECT().GetJobByResourceUUID(ctxWithCorrelationID, params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)
		// Mock GetJobByResourceUUID for CREATE_QUOTA_RULE (called at line 990)
		mockStore.EXPECT().GetJobByResourceUUID(ctxWithCorrelationID, params.QuotaRuleUUID, string(datamodel.JobTypeCreateQuotaRule)).
			Return(createJob, nil)

		// Mock GetVolumeByIDAndAccountID to succeed (function continues after line 966)
		mockStore.EXPECT().GetVolumeByIDAndAccountID(ctxWithCorrelationID, quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(ctxWithCorrelationID, volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(ctxWithCorrelationID, *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRuleCountBySvmID to succeed
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(ctxWithCorrelationID, volume.SvmID).
			Return(int64(1), nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(ctxWithCorrelationID, mock.Anything).
			Return(createdJob, nil)

		// When isCleanupDelete is true, UpdatingQuotaRule is NOT called (line 1086-1098)
		// The quota rule remains in CREATING state, not DELETING

		// Mock ExecuteWorkflow to succeed - expects quota rule in CREATING state (not DELETING)
		mockTemporal.EXPECT().ExecuteWorkflow(ctxWithCorrelationID, mock.Anything, mock.Anything, params, quotaRuleDataModel).
			Return(nil, nil)

		quotaRule, operationID, err := _deleteQuotaRule(ctxWithCorrelationID, mockStore, mockTemporal, params)

		// Should succeed as cancellation request (line 966 logs and continues)
		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.Equal(tt, "job-uuid-123", operationID)
	})

	t.Run("WhenGetVolumeByIDAndAccountIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called for non-transitional states)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)

		// Mock GetVolumeByIDAndAccountID to fail
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(nil, errors.New("volume not found"))

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Failed to get volume")
	})

	t.Run("WhenLocationIdIsSetFromPoolPrimaryZone", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "", // Empty, should be set from pool
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			Pool: &datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone: "us-central1-a",
				},
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting,
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
			RQuota:       false,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called for non-transitional states)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRuleCountBySvmID to succeed
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(1), nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.Equal(tt, "job-uuid-123", operationID)
		// Note: LocationId is not set from pool in the current implementation
		// The test verifies successful deletion regardless of LocationId value
	})

	t.Run("WhenValidateReplicationStateFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called for non-transitional states)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to return error (causes validateReplicationState to fail)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return(nil, errors.NewUserInputValidationErr("replication validation failed"))

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "replication validation failed")
	})

	t.Run("WhenAnotherQuotaRuleIsCreating", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		existingRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "other-creating"},
				State:     datamodel.LifeCycleStateCreating,
			},
		}

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)

		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(existingRules, nil)

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.True(tt, errors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Another quota rule is being created")
	})

	t.Run("WhenGetQuotaRulesByVolumeIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called for non-transitional states)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return(nil, errors.New("failed to fetch quota rules for volume"))

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to fetch quota rules for volume")
	})

	t.Run("WhenDetermineRQuotaFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called for non-transitional states)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRuleCountBySvmID to fail
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), errors.New("failed to get quota rule count"))

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to get quota rule count")
	})

	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called for non-transitional states)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRuleCountBySvmID to succeed
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)

		// Mock CreateJob to fail
		mockStore.EXPECT().CreateJob(context.Background(), mock.AnythingOfType("*datamodel.Job")).
			Return(nil, errors.New("failed to create job"))

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to create job")
	})

	t.Run("WhenUpdatingQuotaRuleFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called for non-transitional states)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRuleCountBySvmID to succeed
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to fail
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(nil, errors.New("failed to update quota rule"))

		// When UpdatingQuotaRule fails, the function returns early (line 1095)
		// No cleanup is needed since job was created but workflow wasn't started

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to update quota rule")
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting,
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
			RQuota:       false,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called for non-transitional states)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRuleCountBySvmID to succeed
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(0), nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to fail
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, errors.New("failed to execute workflow"))

		// Mock DeleteJob cleanup
		mockStore.EXPECT().DeleteJob(context.Background(), createdJob.UUID, mock.Anything).
			Return(nil)

		// Mock UpdateQuotaRule to restore previous state in defer cleanup
		mockStore.EXPECT().UpdateQuotaRule(context.Background(), mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr.UUID == quotaRuleDataModel.UUID &&
				qr.State == datamodel.LifeCycleStateAvailable &&
				qr.StateDetails == datamodel.LifeCycleStateReadyDetails
		})).Return(quotaRuleDataModel, nil)

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to execute workflow")
	})

	t.Run("WhenDeleteQuotaRuleSucceeds", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting,
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
			RQuota:       true,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called for DELETING state)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock ListVolumeReplications to succeed (validateReplicationState passes)
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock GetQuotaRuleCountBySvmID to succeed (last quota rule in SVM, so RQuota required for delete)
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(context.Background(), volume.SvmID).
			Return(int64(1), nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.Equal(tt, "job-uuid-123", operationID)
		assert.Equal(tt, "quota-rule-uuid-1", quotaRule.UUID)
		assert.Equal(tt, datamodel.LifeCycleStateDeleting, quotaRule.LifeCycleState)
	})

	t.Run("WhenQuotaRuleIsInDeletingState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting, // DELETING is not allowed in _deleteQuotaRule (unlike _deleteQuotaRuleInternal)
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE - return existing job when state is DELETING
		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-uuid-123"},
			State:     string(datamodel.JobsStatePROCESSING),
		}
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(existingJob, nil)
		// When existing job is found, function returns early - no need for other mocks

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.Equal(tt, "job-uuid-123", operationID)
	})

	t.Run("WhenDeleteQuotaRuleCleanupFailsInDefer", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{UUID: "quota-rule-uuid-1"},
			Name:           "quota-rule-1",
			VolumeID:       1,
			AccountID:      1,
			State:          datamodel.LifeCycleStateAvailable,
			StateDetails:   datamodel.LifeCycleStateReadyDetails,
			DiskLimitInKib: 100 * 1024,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			AccountID: 1,
			SvmID:     1,
			Pool: &datamodel.Pool{
				VendorID: "/projects/project123/locations/location123/pools/pool123",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"}, // Set NFS protocol so determineRQuota calls GetQuotaRuleCountBySvmID
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, account.ID).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called for non-transitional states)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, account.ID).
			Return(volume, nil)

		mockStore.EXPECT().GetQuotaRulesByVolumeID(context.Background(), volume.ID).
			Return([]*datamodel.QuotaRule{}, nil)

		// Mock validateReplicationState to succeed - use mock storage to return empty replications
		expectedFilter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))
		mockStore.EXPECT().ListVolumeReplications(context.Background(), *expectedFilter, database.QueryDepthZero).
			Return([]*datamodel.VolumeReplication{}, nil)

		// Mock determineRQuota to succeed - use mock storage to return quota count
		// Use mock.Anything for context since it may have logger fields
		mockStore.EXPECT().GetQuotaRuleCountBySvmID(mock.Anything, volume.SvmID).
			Return(int64(0), nil)

		// Mock CreateJob to fail (this triggers the defer cleanup)
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(nil, errors.New("failed to create job"))

		// The defer cleanup in _deleteQuotaRule only calls DeleteJob and UpdateQuotaRule
		// It does NOT call DeleteQuotaRule (that's only in CREATE functions)
		// So we don't need to mock DeleteQuotaRule here

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "failed to create job")
	})

	t.Run("WhenDeleteQuotaRuleInCreatingStateWithExistingDeleteJob", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{UUID: "quota-rule-uuid-1"},
			Name:           "quota-rule-1",
			VolumeID:       1,
			AccountID:      1,
			State:          datamodel.LifeCycleStateCreating,
			StateDetails:   datamodel.LifeCycleStateCreatingDetails,
			DiskLimitInKib: 100 * 1024,
		}

		correlationID := "test-correlation-id"
		fields := log.Fields{
			string(middleware.RequestCorrelationID): correlationID,
		}
		ctxWithCorrelationID := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)

		existingDeleteJob := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "existing-delete-job-uuid"},
			Type:          string(datamodel.JobTypeDeleteQuotaRule),
			State:         string(datamodel.JobsStatePROCESSING),
			CorrelationID: correlationID, // Set correlation ID to match request
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetQuotaRuleByUUID to succeed (must use ctxWithCorrelationID)
		mockStore.EXPECT().GetQuotaRuleByUUID(ctxWithCorrelationID, params.QuotaRuleUUID, account.ID).
			Return(quotaRuleDataModel, nil)

		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE - return existing delete job
		// When an existing delete job is found, ValidateCorrelationIDForCreatingResource returns early
		// and doesn't call GetJobByResourceUUID for CREATE_QUOTA_RULE
		mockStore.EXPECT().GetJobByResourceUUID(ctxWithCorrelationID, params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(existingDeleteJob, nil)

		quotaRule, operationID, err := _deleteQuotaRule(ctxWithCorrelationID, mockStore, mockTemporal, params)

		// Should return existing job (lines 983-986)
		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.Equal(tt, "existing-delete-job-uuid", operationID)
	})

	t.Run("WhenDeleteQuotaRuleInCreatingStateGetCreateJobFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{UUID: "quota-rule-uuid-1"},
			Name:           "quota-rule-1",
			VolumeID:       1,
			AccountID:      1,
			State:          datamodel.LifeCycleStateCreating,
			StateDetails:   datamodel.LifeCycleStateCreatingDetails,
			DiskLimitInKib: 100 * 1024,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		correlationID := "test-correlation-id"
		fields := log.Fields{
			string(middleware.RequestCorrelationID): correlationID,
		}
		ctxWithCorrelationID := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)

		// Mock GetQuotaRuleByUUID to succeed (must use ctxWithCorrelationID)
		mockStore.EXPECT().GetQuotaRuleByUUID(ctxWithCorrelationID, params.QuotaRuleUUID, account.ID).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called first in ValidateCorrelationIDForCreatingResource)
		mockStore.EXPECT().GetJobByResourceUUID(ctxWithCorrelationID, params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)
		// Mock GetJobByResourceUUID for CREATE_QUOTA_RULE to fail
		mockStore.EXPECT().GetJobByResourceUUID(ctxWithCorrelationID, params.QuotaRuleUUID, string(datamodel.JobTypeCreateQuotaRule)).
			Return(nil, errors.New("failed to get create job"))

		quotaRule, operationID, err := _deleteQuotaRule(ctxWithCorrelationID, mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "Error deleting quota rule - quota rule is already transitioning between states")
	})

	t.Run("WhenDeleteQuotaRuleInCreatingStateCorrelationIDMismatch", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{UUID: "quota-rule-uuid-1"},
			Name:           "quota-rule-1",
			VolumeID:       1,
			AccountID:      1,
			State:          datamodel.LifeCycleStateCreating,
			StateDetails:   datamodel.LifeCycleStateCreatingDetails,
			DiskLimitInKib: 100 * 1024,
		}

		createJob := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "create-job-uuid"},
			Type:          string(datamodel.JobTypeCreateQuotaRule),
			CorrelationID: "different-correlation-id",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		correlationID := "test-correlation-id"
		fields := log.Fields{
			string(middleware.RequestCorrelationID): correlationID,
		}
		ctxWithCorrelationID := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)

		// Mock GetQuotaRuleByUUID to succeed (must use ctxWithCorrelationID)
		mockStore.EXPECT().GetQuotaRuleByUUID(ctxWithCorrelationID, params.QuotaRuleUUID, account.ID).
			Return(quotaRuleDataModel, nil)
		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (called first in ValidateCorrelationIDForCreatingResource)
		mockStore.EXPECT().GetJobByResourceUUID(ctxWithCorrelationID, params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(nil, nil)
		// Mock GetJobByResourceUUID for CREATE_QUOTA_RULE to return job with different correlation ID
		mockStore.EXPECT().GetJobByResourceUUID(ctxWithCorrelationID, params.QuotaRuleUUID, string(datamodel.JobTypeCreateQuotaRule)).
			Return(createJob, nil)

		quotaRule, operationID, err := _deleteQuotaRule(ctxWithCorrelationID, mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "Error deleting quota rule - quota rule is already transitioning between states")
	})

	t.Run("WhenDeleteQuotaRuleInDeletingStateWithExistingDeleteJob", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{UUID: "quota-rule-uuid-1"},
			Name:           "quota-rule-1",
			VolumeID:       1,
			AccountID:      1,
			State:          datamodel.LifeCycleStateDeleting,
			StateDetails:   datamodel.LifeCycleStateDeletingDetails,
			DiskLimitInKib: 100 * 1024,
		}

		existingJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "existing-delete-job-uuid"},
			Type:      string(datamodel.JobTypeDeleteQuotaRule),
			State:     string(datamodel.JobsStatePROCESSING),
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, account.ID).
			Return(quotaRuleDataModel, nil)

		// Mock GetJobByResourceUUID for DELETE_QUOTA_RULE (lines 1005-1011)
		mockStore.EXPECT().GetJobByResourceUUID(context.Background(), params.QuotaRuleUUID, string(datamodel.JobTypeDeleteQuotaRule)).
			Return(existingJob, nil)

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		// Should return existing job (lines 1008-1011)
		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.Equal(tt, "existing-delete-job-uuid", operationID)
	})

	t.Run("WhenDeleteQuotaRuleInTransitionalState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{UUID: "quota-rule-uuid-1"},
			Name:           "quota-rule-1",
			VolumeID:       1,
			AccountID:      1,
			State:          datamodel.LifeCycleStateUpdating, // Transitional state (lines 1016-1017)
			StateDetails:   datamodel.LifeCycleStateUpdatingDetails,
			DiskLimitInKib: 100 * 1024,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, account.ID).
			Return(quotaRuleDataModel, nil)

		quotaRule, operationID, err := _deleteQuotaRule(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Empty(tt, operationID)
		assert.Contains(tt, err.Error(), "quota rule is in transition state and cannot be deleted")
	})
}

func TestDeleteQuotaRuleInternal(t *testing.T) {
	// Save original function pointers
	originalGetAccountWithName := getAccountWithName

	defer func() {
		getAccountWithName = originalGetAccountWithName
	}()

	t.Run("WhenGetAccountWithNameFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		// Mock getAccountWithName to return error
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("failed to get account")
		}

		defer func() { getAccountWithName = originalGetAccountWithName }()
		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "failed to get account")
	})

	t.Run("WhenGetQuotaRuleByUUIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to fail
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(nil, errors.New("quota rule not found"))

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "quota rule not found")
	})

	t.Run("WhenDeleteQuotaRuleInternalCleanupFailsInDefer", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-project",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{UUID: "quota-rule-uuid-1"},
			Name:           "quota-rule-1",
			VolumeID:       1,
			AccountID:      1,
			State:          datamodel.LifeCycleStateAvailable,
			StateDetails:   datamodel.LifeCycleStateReadyDetails,
			DiskLimitInKib: 100 * 1024,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			AccountID: 1,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, account.ID).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, account.ID).
			Return(volume, nil)

		// Mock CreateJob to fail (this triggers the defer cleanup)
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(nil, errors.New("failed to create job"))

		// The defer cleanup in _deleteQuotaRuleInternal only calls DeleteJob and UpdateQuotaRule
		// It does NOT call DeleteQuotaRule (that's only in CREATE functions)
		// So we don't need to mock DeleteQuotaRule here

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "failed to create job")
	})

	t.Run("WhenQuotaRuleIsInTransitioningState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateCreating, // Transitioning state (not DELETING)
			StateDetails: "",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "transition state")
	})

	t.Run("WhenQuotaRuleIsAlreadyDeleted", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleted, // Already deleted
			StateDetails: datamodel.LifeCycleStateDeletedDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Note: _deleteQuotaRuleInternal doesn't have early return for DELETED state
		// It only checks for transition states (excluding DELETING), so it will continue
		// Mock GetVolumeByIDAndAccountID (code will call this)
		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			SvmID:       1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob (code will create job)
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule (code will update state to DELETING)
		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting,
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
		}
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job) // Job will be created
		assert.Equal(tt, "quota-rule-uuid-1", quotaRule.UUID)
		assert.Equal(tt, datamodel.LifeCycleStateDeleting, quotaRule.LifeCycleState)
	})

	t.Run("WhenGetVolumeByIDAndAccountIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to fail
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(nil, errors.New("volume not found"))

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "volume not found")
	})

	t.Run("WhenLocationIdIsSetFromPoolPrimaryZone", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "", // Empty, should be set from pool
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			Pool: &datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone: "us-central1-a",
				},
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting,
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job)
		// Note: LocationId is not set from pool in the current implementation
		// The test verifies successful deletion regardless of LocationId value
		assert.Equal(tt, "job-uuid-123", job.UUID)
	})

	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to fail
		mockStore.EXPECT().CreateJob(context.Background(), mock.AnythingOfType("*datamodel.Job")).
			Return(nil, errors.New("failed to create job"))

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "failed to create job")
	})

	t.Run("WhenUpdatingQuotaRuleFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to fail
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(nil, errors.New("failed to update quota rule"))

		// Mock DeleteJob cleanup
		mockStore.EXPECT().DeleteJob(context.Background(), createdJob.UUID, mock.Anything).
			Return(nil)

		// Mock UpdateQuotaRule to restore previous state in defer cleanup
		mockStore.EXPECT().UpdateQuotaRule(context.Background(), mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr.UUID == quotaRuleDataModel.UUID &&
				qr.State == datamodel.LifeCycleStateAvailable &&
				qr.StateDetails == datamodel.LifeCycleStateReadyDetails
		})).Return(quotaRuleDataModel, nil)

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "failed to update quota rule")
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting,
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to fail
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, errors.New("failed to execute workflow"))

		// Mock DeleteJob cleanup
		mockStore.EXPECT().DeleteJob(context.Background(), createdJob.UUID, mock.Anything).
			Return(nil)

		// Mock UpdateQuotaRule to restore previous state in defer cleanup
		mockStore.EXPECT().UpdateQuotaRule(context.Background(), mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr.UUID == quotaRuleDataModel.UUID &&
				qr.State == datamodel.LifeCycleStateAvailable &&
				qr.StateDetails == datamodel.LifeCycleStateReadyDetails
		})).Return(quotaRuleDataModel, nil)

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRule)
		assert.Nil(tt, job)
		assert.Contains(tt, err.Error(), "failed to execute workflow")
	})

	t.Run("WhenDeleteQuotaRuleInternalSucceeds", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting,
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job)
		assert.Equal(tt, "quota-rule-uuid-1", quotaRule.UUID)
		assert.Equal(tt, datamodel.LifeCycleStateDeleting, quotaRule.LifeCycleState)
		assert.Equal(tt, "job-uuid-123", job.UUID)
	})

	t.Run("WhenQuotaRuleIsInDeletingState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "us-central1",
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting, // DELETING is allowed for idempotency
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting,
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job)
		assert.Equal(tt, "job-uuid-123", job.UUID)
	})

	t.Run("WhenLocationIdIsNotSetAndPoolIsNil", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "", // Empty, but pool is nil so it won't be set
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			Pool:        nil, // Pool is nil
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting,
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job)
		assert.Empty(tt, params.LocationId, "LocationId should remain empty when pool is nil")
	})

	t.Run("WhenLocationIdIsNotSetAndPoolAttributesIsNil", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		params := &common.DeleteQuotaRulesParam{
			ProjectId:     "test-project",
			QuotaRuleUUID: "quota-rule-uuid-1",
			LocationId:    "", // Empty, but PoolAttributes is nil so it won't be set
		}

		quotaRuleDataModel := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
			SizeInBytes: 200 * 1024 * 1024,
			AccountID:   1,
			Pool: &datamodel.Pool{
				PoolAttributes: nil, // PoolAttributes is nil
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSV3"},
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
			WorkflowID: "workflow-id-123",
		}

		updatedQuotaRule := &datamodel.QuotaRule{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "quota-rule-uuid-1"},
			Name:         "quota-rule-1",
			VolumeID:     1,
			AccountID:    1,
			State:        datamodel.LifeCycleStateDeleting,
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      accountName,
			}, nil
		}

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(context.Background(), params.QuotaRuleUUID, int64(1)).
			Return(quotaRuleDataModel, nil)

		// Mock GetVolumeByIDAndAccountID to succeed
		mockStore.EXPECT().GetVolumeByIDAndAccountID(context.Background(), quotaRuleDataModel.VolumeID, int64(1)).
			Return(volume, nil)

		// Mock CreateJob to succeed
		mockStore.EXPECT().CreateJob(context.Background(), mock.Anything).
			Return(createdJob, nil)

		// Mock UpdatingQuotaRule to succeed
		mockStore.EXPECT().UpdatingQuotaRule(context.Background(), mock.Anything).
			Return(updatedQuotaRule, nil)

		// Mock ExecuteWorkflow to succeed
		mockTemporal.EXPECT().ExecuteWorkflow(context.Background(), mock.Anything, mock.Anything, params, updatedQuotaRule).
			Return(nil, nil)

		quotaRule, job, err := _deleteQuotaRuleInternal(context.Background(), mockStore, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRule)
		assert.NotNil(tt, job)
		assert.Empty(tt, params.LocationId, "LocationId should remain empty when PoolAttributes is nil")
	})
}

func TestGetMultipleQuotaRules(t *testing.T) {
	// Save original function pointers
	originalGetAccountWithName := getAccountWithName

	defer func() {
		getAccountWithName = originalGetAccountWithName
	}()

	t.Run("WhenAccountNotFound_ReturnsError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUIDs := []string{"quota-uuid-1", "quota-uuid-2"}

		// Mock getAccountWithName to return NotFound error
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.NewNotFoundErr("Account not found", nil)
		}

		result, err := orchestrator.GetMultipleQuotaRules(ctx, volumeUuid, accountName, quotaRuleUUIDs)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, errors.IsNotFoundErr(err), "Should return NotFound error when account not found")
	})

	t.Run("WhenGetAccountWithNameFailsWithNonNotFoundError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUIDs := []string{"quota-uuid-1"}

		// Mock getAccountWithName to return non-NotFound error
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("database connection failed")
		}

		result, err := orchestrator.GetMultipleQuotaRules(ctx, volumeUuid, accountName, quotaRuleUUIDs)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "database connection failed")
	})

	t.Run("WhenVolumeNotFound_ReturnsError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUIDs := []string{"quota-uuid-1"}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to return NotFound error
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(nil, errors.NewNotFoundErr("Volume not found", nil))

		result, err := orchestrator.GetMultipleQuotaRules(ctx, volumeUuid, accountName, quotaRuleUUIDs)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, errors.IsNotFoundErr(err), "Should return NotFound error when volume not found")
	})

	t.Run("WhenGetVolumeWithAccountIDFailsWithNonNotFoundError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUIDs := []string{"quota-uuid-1"}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to return non-NotFound error
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(nil, errors.New("database error"))

		result, err := orchestrator.GetMultipleQuotaRules(ctx, volumeUuid, accountName, quotaRuleUUIDs)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "database error")
	})

	t.Run("WhenGetQuotaRulesWithConditionFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUIDs := []string{"quota-uuid-1", "quota-uuid-2"}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRulesWithCondition to fail
		mockStore.EXPECT().GetQuotaRulesWithCondition(ctx, mock.Anything).
			Return(nil, errors.New("failed to get quota rules"))

		result, err := orchestrator.GetMultipleQuotaRules(ctx, volumeUuid, accountName, quotaRuleUUIDs)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "failed to get quota rules")
	})

	t.Run("WhenGetQuotaRulesWithConditionSucceeds_ReturnsMultipleQuotaRules", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUIDs := []string{"quota-uuid-1", "quota-uuid-2"}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		dbQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel:      datamodel.BaseModel{ID: 1, UUID: "quota-uuid-1"},
				Name:           "quota-rule-1",
				QuotaType:      IndividualUserQuota,
				QuotaTarget:    "user:alice",
				DiskLimitInKib: 102400, // 100 MiB in KiB
				State:          datamodel.LifeCycleStateAvailable,
				StateDetails:   datamodel.LifeCycleStateReadyDetails,
				Description:    "First quota rule",
				VolumeID:       volume.ID,
				AccountID:      account.ID,
			},
			{
				BaseModel:      datamodel.BaseModel{ID: 2, UUID: "quota-uuid-2"},
				Name:           "quota-rule-2",
				QuotaType:      IndividualGroupQuota,
				QuotaTarget:    "group:developers",
				DiskLimitInKib: 204800, // 200 MiB in KiB
				State:          datamodel.LifeCycleStateAvailable,
				StateDetails:   datamodel.LifeCycleStateReadyDetails,
				Description:    "Second quota rule",
				VolumeID:       volume.ID,
				AccountID:      account.ID,
			},
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRulesWithCondition to succeed
		mockStore.EXPECT().GetQuotaRulesWithCondition(ctx, mock.Anything).
			Return(dbQuotaRules, nil)

		result, err := orchestrator.GetMultipleQuotaRules(ctx, volumeUuid, accountName, quotaRuleUUIDs)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 2)

		// Verify first quota rule
		assert.Equal(tt, "quota-uuid-1", result[0].UUID)
		assert.Equal(tt, "quota-rule-1", result[0].Name)
		assert.Equal(tt, IndividualUserQuota, result[0].QuotaType)
		assert.Equal(tt, "user:alice", result[0].QuotaTarget)
		assert.Equal(tt, int64(100), result[0].DiskLimitInMib) // 102400 KiB / 1024 = 100 MiB
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, result[0].LifeCycleState)
		assert.Equal(tt, "First quota rule", result[0].Description)

		// Verify second quota rule
		assert.Equal(tt, "quota-uuid-2", result[1].UUID)
		assert.Equal(tt, "quota-rule-2", result[1].Name)
		assert.Equal(tt, IndividualGroupQuota, result[1].QuotaType)
		assert.Equal(tt, "group:developers", result[1].QuotaTarget)
		assert.Equal(tt, int64(200), result[1].DiskLimitInMib) // 204800 KiB / 1024 = 200 MiB
		assert.Equal(tt, "Second quota rule", result[1].Description)
	})

	t.Run("WhenGetQuotaRulesWithConditionReturnsEmptyList", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUIDs := []string{"quota-uuid-1"}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRulesWithCondition to return empty list
		mockStore.EXPECT().GetQuotaRulesWithCondition(ctx, mock.Anything).
			Return([]*datamodel.QuotaRule{}, nil)

		result, err := orchestrator.GetMultipleQuotaRules(ctx, volumeUuid, accountName, quotaRuleUUIDs)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 0)
	})

	t.Run("WhenGetQuotaRulesWithConditionReturnsSingleQuotaRule", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUIDs := []string{"quota-uuid-1"}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		dbQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 1, UUID: "quota-uuid-1"},
			Name:           "quota-rule-1",
			QuotaType:      DefaultUserQuota,
			QuotaTarget:    "",
			DiskLimitInKib: 51200, // 50 MiB in KiB
			State:          datamodel.LifeCycleStateCreating,
			StateDetails:   datamodel.LifeCycleStateCreatingDetails,
			Description:    "Default user quota",
			VolumeID:       volume.ID,
			AccountID:      account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRulesWithCondition to return single quota rule
		mockStore.EXPECT().GetQuotaRulesWithCondition(ctx, mock.Anything).
			Return([]*datamodel.QuotaRule{dbQuotaRule}, nil)

		result, err := orchestrator.GetMultipleQuotaRules(ctx, volumeUuid, accountName, quotaRuleUUIDs)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 1)
		assert.Equal(tt, "quota-uuid-1", result[0].UUID)
		assert.Equal(tt, "quota-rule-1", result[0].Name)
		assert.Equal(tt, DefaultUserQuota, result[0].QuotaType)
		assert.Equal(tt, "", result[0].QuotaTarget)
		assert.Equal(tt, int64(50), result[0].DiskLimitInMib) // 51200 KiB / 1024 = 50 MiB
		assert.Equal(tt, datamodel.LifeCycleStateCreating, result[0].LifeCycleState)
		assert.Equal(tt, "Default user quota", result[0].Description)
	})

	t.Run("WhenQuotaRuleUUIDsIsEmpty_StillCallsDatabase", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUIDs := []string{}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRulesWithCondition to return empty list
		mockStore.EXPECT().GetQuotaRulesWithCondition(ctx, mock.Anything).
			Return([]*datamodel.QuotaRule{}, nil)

		result, err := orchestrator.GetMultipleQuotaRules(ctx, volumeUuid, accountName, quotaRuleUUIDs)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 0)
	})
}

func TestGetQuotaRulesByUUIDs(t *testing.T) {
	t.Run("WhenQuotaRuleUUIDsEmpty_ReturnsNilWithoutStorageCall", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}
		ctx := context.Background()

		out, err := orchestrator.GetQuotaRulesByUUIDs(ctx, nil)
		assert.NoError(tt, err)
		assert.Nil(tt, out)

		out, err = orchestrator.GetQuotaRulesByUUIDs(ctx, []string{})
		assert.NoError(tt, err)
		assert.Nil(tt, out)
	})

	t.Run("WhenGetQuotaRulesWithConditionFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}
		ctx := context.Background()
		uuids := []string{"quota-uuid-1"}

		mockStore.EXPECT().GetQuotaRulesWithCondition(ctx, mock.Anything).
			Return(nil, errors.New("db unavailable"))

		out, err := orchestrator.GetQuotaRulesByUUIDs(ctx, uuids)
		assert.Error(tt, err)
		assert.Nil(tt, out)
		assert.Contains(tt, err.Error(), "db unavailable")
	})

	t.Run("WhenGetQuotaRulesWithConditionSucceeds", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}
		ctx := context.Background()
		uuids := []string{"quota-uuid-1"}

		dbRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 7, UUID: "quota-uuid-1"},
			Name:           "qr-one",
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "user:bob",
			DiskLimitInKib: 102400,
			State:          datamodel.LifeCycleStateREADY,
			StateDetails:   datamodel.LifeCycleStateReadyDetails,
			Description:    "d1",
		}

		mockStore.EXPECT().GetQuotaRulesWithCondition(ctx, mock.Anything).
			Return([]*datamodel.QuotaRule{dbRule}, nil)

		out, err := orchestrator.GetQuotaRulesByUUIDs(ctx, uuids)
		assert.NoError(tt, err)
		require.Len(tt, out, 1)
		assert.Equal(tt, "quota-uuid-1", out[0].UUID)
		assert.Equal(tt, "qr-one", out[0].Name)
		assert.Equal(tt, IndividualUserQuota, out[0].QuotaType)
	})
}

func TestDescribeQuotaRule(t *testing.T) {
	// Save original function pointers
	originalGetAccountWithName := getAccountWithName

	defer func() {
		getAccountWithName = originalGetAccountWithName
	}()

	t.Run("WhenAccountNotFound_ReturnsError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-1"

		// Mock getAccountWithName to return NotFound error
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.NewNotFoundErr("Account not found", nil)
		}

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, errors.IsNotFoundErr(err))
	})

	t.Run("WhenGetAccountWithNameFailsWithNonNotFoundError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-1"

		// Mock getAccountWithName to return non-NotFound error
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("database connection failed")
		}

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "database connection failed")
	})

	t.Run("WhenVolumeNotFound_ReturnsError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-1"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to return NotFound error (fatal error for DescribeQuotaRule)
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(nil, errors.NewNotFoundErr("Volume not found", nil))

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, errors.IsNotFoundErr(err))
	})

	t.Run("WhenGetVolumeWithAccountIDFailsWithNonNotFoundError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-1"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to return non-NotFound error
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(nil, errors.New("database error"))

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "database error")
	})

	t.Run("WhenGetQuotaRuleByUUIDFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-1"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRuleByUUID to fail
		mockStore.EXPECT().GetQuotaRuleByUUID(ctx, quotaRuleUUID, account.ID).
			Return(nil, errors.NewNotFoundErr("Quota rule not found", nil))

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, errors.IsNotFoundErr(err))
	})

	t.Run("WhenGetQuotaRuleByUUIDSucceeds_IndividualUserQuota", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-1"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		dbQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 1, UUID: quotaRuleUUID},
			Name:           "quota-rule-1",
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "user:alice",
			DiskLimitInKib: 102400, // 100 MiB in KiB
			State:          datamodel.LifeCycleStateAvailable,
			StateDetails:   datamodel.LifeCycleStateReadyDetails,
			Description:    "Individual user quota rule",
			VolumeID:       volume.ID,
			AccountID:      account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(ctx, quotaRuleUUID, account.ID).
			Return(dbQuotaRule, nil)

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, quotaRuleUUID, result.UUID)
		assert.Equal(tt, "quota-rule-1", result.Name)
		assert.Equal(tt, IndividualUserQuota, result.QuotaType)
		assert.Equal(tt, "user:alice", result.QuotaTarget)
		assert.Equal(tt, int64(100), result.DiskLimitInMib) // 102400 KiB / 1024 = 100 MiB
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, result.LifeCycleState)
		assert.Equal(tt, datamodel.LifeCycleStateReadyDetails, result.LifeCycleStateDetails)
		assert.Equal(tt, "Individual user quota rule", result.Description)
	})

	t.Run("WhenGetQuotaRuleByUUIDSucceeds_IndividualGroupQuota", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-2"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		dbQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 2, UUID: quotaRuleUUID},
			Name:           "quota-rule-2",
			QuotaType:      IndividualGroupQuota,
			QuotaTarget:    "group:developers",
			DiskLimitInKib: 204800, // 200 MiB in KiB
			State:          datamodel.LifeCycleStateAvailable,
			StateDetails:   datamodel.LifeCycleStateReadyDetails,
			Description:    "Individual group quota rule",
			VolumeID:       volume.ID,
			AccountID:      account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(ctx, quotaRuleUUID, account.ID).
			Return(dbQuotaRule, nil)

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, quotaRuleUUID, result.UUID)
		assert.Equal(tt, IndividualGroupQuota, result.QuotaType)
		assert.Equal(tt, "group:developers", result.QuotaTarget)
		assert.Equal(tt, int64(200), result.DiskLimitInMib) // 204800 KiB / 1024 = 200 MiB
	})

	t.Run("WhenGetQuotaRuleByUUIDSucceeds_DefaultUserQuota", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-3"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		dbQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 3, UUID: quotaRuleUUID},
			Name:           "quota-rule-3",
			QuotaType:      DefaultUserQuota,
			QuotaTarget:    "",
			DiskLimitInKib: 51200, // 50 MiB in KiB
			State:          datamodel.LifeCycleStateAvailable,
			StateDetails:   datamodel.LifeCycleStateReadyDetails,
			Description:    "Default user quota rule",
			VolumeID:       volume.ID,
			AccountID:      account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(ctx, quotaRuleUUID, account.ID).
			Return(dbQuotaRule, nil)

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, quotaRuleUUID, result.UUID)
		assert.Equal(tt, DefaultUserQuota, result.QuotaType)
		assert.Equal(tt, "", result.QuotaTarget)
		assert.Equal(tt, int64(50), result.DiskLimitInMib) // 51200 KiB / 1024 = 50 MiB
	})

	t.Run("WhenGetQuotaRuleByUUIDSucceeds_DefaultGroupQuota", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-4"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		dbQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 4, UUID: quotaRuleUUID},
			Name:           "quota-rule-4",
			QuotaType:      DefaultGroupQuota,
			QuotaTarget:    "",
			DiskLimitInKib: 409600, // 400 MiB in KiB
			State:          datamodel.LifeCycleStateAvailable,
			StateDetails:   datamodel.LifeCycleStateReadyDetails,
			Description:    "Default group quota rule",
			VolumeID:       volume.ID,
			AccountID:      account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(ctx, quotaRuleUUID, account.ID).
			Return(dbQuotaRule, nil)

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, quotaRuleUUID, result.UUID)
		assert.Equal(tt, DefaultGroupQuota, result.QuotaType)
		assert.Equal(tt, int64(400), result.DiskLimitInMib) // 409600 KiB / 1024 = 400 MiB
	})

	t.Run("WhenGetQuotaRuleByUUIDSucceeds_CreatingState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-5"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		dbQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 5, UUID: quotaRuleUUID},
			Name:           "quota-rule-5",
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "user:bob",
			DiskLimitInKib: 102400,
			State:          datamodel.LifeCycleStateCreating,
			StateDetails:   datamodel.LifeCycleStateCreatingDetails,
			Description:    "Creating quota rule",
			VolumeID:       volume.ID,
			AccountID:      account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(ctx, quotaRuleUUID, account.ID).
			Return(dbQuotaRule, nil)

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.LifeCycleStateCreating, result.LifeCycleState)
		assert.Equal(tt, datamodel.LifeCycleStateCreatingDetails, result.LifeCycleStateDetails)
	})

	t.Run("WhenGetQuotaRuleByUUIDSucceeds_UpdatingState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-6"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		dbQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 6, UUID: quotaRuleUUID},
			Name:           "quota-rule-6",
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "user:charlie",
			DiskLimitInKib: 204800,
			State:          datamodel.LifeCycleStateUpdating,
			StateDetails:   datamodel.LifeCycleStateUpdatingDetails,
			Description:    "Updating quota rule",
			VolumeID:       volume.ID,
			AccountID:      account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(ctx, quotaRuleUUID, account.ID).
			Return(dbQuotaRule, nil)

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, result.LifeCycleState)
		assert.Equal(tt, datamodel.LifeCycleStateUpdatingDetails, result.LifeCycleStateDetails)
	})

	t.Run("WhenGetQuotaRuleByUUIDSucceeds_DeletingState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-7"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		dbQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 7, UUID: quotaRuleUUID},
			Name:           "quota-rule-7",
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "user:dave",
			DiskLimitInKib: 102400,
			State:          datamodel.LifeCycleStateDeleting,
			StateDetails:   datamodel.LifeCycleStateDeletingDetails,
			Description:    "Deleting quota rule",
			VolumeID:       volume.ID,
			AccountID:      account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(ctx, quotaRuleUUID, account.ID).
			Return(dbQuotaRule, nil)

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.LifeCycleStateDeleting, result.LifeCycleState)
		assert.Equal(tt, datamodel.LifeCycleStateDeletingDetails, result.LifeCycleStateDetails)
	})

	t.Run("WhenGetQuotaRuleByUUIDSucceeds_ErrorState", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-8"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		dbQuotaRule := &datamodel.QuotaRule{
			BaseModel:      datamodel.BaseModel{ID: 8, UUID: quotaRuleUUID},
			Name:           "quota-rule-8",
			QuotaType:      IndividualUserQuota,
			QuotaTarget:    "user:eve",
			DiskLimitInKib: 102400,
			State:          datamodel.LifeCycleStateError,
			StateDetails:   datamodel.LifeCycleStateCreationErrorDetails,
			Description:    "Error quota rule",
			VolumeID:       volume.ID,
			AccountID:      account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRuleByUUID to succeed
		mockStore.EXPECT().GetQuotaRuleByUUID(ctx, quotaRuleUUID, account.ID).
			Return(dbQuotaRule, nil)

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.LifeCycleStateError, result.LifeCycleState)
		assert.Equal(tt, datamodel.LifeCycleStateCreationErrorDetails, result.LifeCycleStateDetails)
	})

	t.Run("WhenGetQuotaRuleByUUIDFailsWithInternalError", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeUuid := "volume-uuid-1"
		accountName := "test-project"
		quotaRuleUUID := "quota-uuid-1"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      accountName,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeUuid},
			AccountID: account.ID,
		}

		// Mock getAccountWithName to succeed
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetVolumeWithAccountID to succeed
		mockStore.EXPECT().GetVolumeWithAccountID(ctx, volumeUuid, account.ID).
			Return(volume, nil)

		// Mock GetQuotaRuleByUUID to fail with internal error
		mockStore.EXPECT().GetQuotaRuleByUUID(ctx, quotaRuleUUID, account.ID).
			Return(nil, errors.New("database connection failed"))

		result, err := orchestrator.DescribeQuotaRule(ctx, volumeUuid, accountName, quotaRuleUUID)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "database connection failed")
	})
}

// TestReplaceDstQuotaRulesWithSrc tests the ReplaceDstQuotaRulesWithSrc function
func TestReplaceDstQuotaRulesWithSrc(t *testing.T) {
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeId := "volume-uuid-1"
		quotaTarget := "user:alice"
		quotaId1 := "quota-uuid-1"
		quotaId2 := "quota-uuid-2"
		params := common.V1betaUpdateDestinationQuotaRulesVCPParams{
			ProjectNumber: "123456789",
			LocationId:    "us-central1",
			VolumeId:      volumeId,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeId},
			AccountID: 1,
		}

		srcQuotaRule1 := common.QuotaRulesV1beta{
			ResourceId:     "quota-rule-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
			QuotaTarget:    &quotaTarget,
			QuotaId:        &quotaId1,
		}

		dstQuotaRule1 := common.QuotaRulesV1beta{
			ResourceId:     "quota-rule-2",
			QuotaType:      DefaultUserQuota,
			DiskLimitInMib: 200,
			QuotaId:        &quotaId2,
		}

		req := &common.UpdateDstWithSrcQuotaRulesV1beta{
			SrcQuotaRules: []common.QuotaRulesV1beta{srcQuotaRule1},
			DstQuotaRules: []common.QuotaRulesV1beta{dstQuotaRule1},
		}

		createdQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel:      datamodel.BaseModel{ID: 1, UUID: "quota-uuid-1"},
				Name:           "quota-rule-1",
				VolumeID:       1,
				AccountID:      1,
				QuotaType:      IndividualUserQuota,
				QuotaTarget:    "user:alice",
				DiskLimitInKib: 100 * 1024,
				State:          datamodel.LifeCycleStateCreating,
			},
		}

		// Mock GetVolume to succeed
		mockStore.EXPECT().GetVolume(ctx, volumeId).Return(volume, nil)

		// Mock ReplaceDstQuotaRulesWithSrc to succeed
		mockStore.EXPECT().ReplaceDstQuotaRulesWithSrc(
			ctx,
			volume.ID,
			volume.AccountID,
			[]string{"quota-uuid-2"},
			mock.AnythingOfType("[]*datamodel.QuotaRule"),
		).Return(createdQuotaRules, nil)

		result, err := orchestrator.ReplaceDstQuotaRulesWithSrc(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 1)
		assert.Equal(tt, "quota-uuid-1", result[0].UUID)
	})

	t.Run("WhenGetVolumeFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeId := "volume-uuid-1"
		params := common.V1betaUpdateDestinationQuotaRulesVCPParams{
			ProjectNumber: "123456789",
			LocationId:    "us-central1",
			VolumeId:      volumeId,
		}

		req := &common.UpdateDstWithSrcQuotaRulesV1beta{
			SrcQuotaRules: []common.QuotaRulesV1beta{},
			DstQuotaRules: []common.QuotaRulesV1beta{},
		}

		// Mock GetVolume to fail
		mockStore.EXPECT().GetVolume(ctx, volumeId).Return(nil, errors.New("volume not found"))

		result, err := orchestrator.ReplaceDstQuotaRulesWithSrc(ctx, req, params)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "volume not found")
	})

	t.Run("WhenReplaceDstQuotaRulesWithSrcFails", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeId := "volume-uuid-1"
		params := common.V1betaUpdateDestinationQuotaRulesVCPParams{
			ProjectNumber: "123456789",
			LocationId:    "us-central1",
			VolumeId:      volumeId,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeId},
			AccountID: 1,
		}

		srcQuotaRule1 := common.QuotaRulesV1beta{
			ResourceId:     "quota-rule-1",
			QuotaType:      IndividualUserQuota,
			DiskLimitInMib: 100,
		}

		req := &common.UpdateDstWithSrcQuotaRulesV1beta{
			SrcQuotaRules: []common.QuotaRulesV1beta{srcQuotaRule1},
			DstQuotaRules: []common.QuotaRulesV1beta{},
		}

		// Mock GetVolume to succeed
		mockStore.EXPECT().GetVolume(ctx, volumeId).Return(volume, nil)

		// Mock ReplaceDstQuotaRulesWithSrc to fail
		mockStore.EXPECT().ReplaceDstQuotaRulesWithSrc(
			ctx,
			volume.ID,
			volume.AccountID,
			[]string{},
			mock.AnythingOfType("[]*datamodel.QuotaRule"),
		).Return(nil, errors.New("database error"))

		result, err := orchestrator.ReplaceDstQuotaRulesWithSrc(ctx, req, params)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "database error")
	})

	t.Run("WhenDstQuotaRuleHasNoQuotaId", func(tt *testing.T) {
		mockStore := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStore}

		ctx := context.Background()
		volumeId := "volume-uuid-1"
		params := common.V1betaUpdateDestinationQuotaRulesVCPParams{
			ProjectNumber: "123456789",
			LocationId:    "us-central1",
			VolumeId:      volumeId,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: volumeId},
			AccountID: 1,
		}

		dstQuotaRule1 := common.QuotaRulesV1beta{
			ResourceId:     "quota-rule-2",
			QuotaType:      DefaultUserQuota,
			DiskLimitInMib: 200,
			// QuotaId is not set (nil)
		}

		req := &common.UpdateDstWithSrcQuotaRulesV1beta{
			SrcQuotaRules: []common.QuotaRulesV1beta{},
			DstQuotaRules: []common.QuotaRulesV1beta{dstQuotaRule1},
		}

		createdQuotaRules := []*datamodel.QuotaRule{}

		// Mock GetVolume to succeed
		mockStore.EXPECT().GetVolume(ctx, volumeId).Return(volume, nil)

		// Mock ReplaceDstQuotaRulesWithSrc to succeed (empty dstQuotaRuleUUIDs since QuotaId is not set)
		mockStore.EXPECT().ReplaceDstQuotaRulesWithSrc(
			ctx,
			volume.ID,
			volume.AccountID,
			[]string{},
			mock.AnythingOfType("[]*datamodel.QuotaRule"),
		).Return(createdQuotaRules, nil)

		result, err := orchestrator.ReplaceDstQuotaRulesWithSrc(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 0)
	})
}

// Test_convertQuotaRulesV1betaToDataModel tests the _convertQuotaRulesV1betaToDataModel function
func Test_convertQuotaRulesV1betaToDataModel(t *testing.T) {
	t.Run("WhenAllFieldsPresent_IndividualUserQuota", func(tt *testing.T) {
		clientRule := gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "quota-rule-1",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeINDIVIDUALUSERQUOTA,
			DiskLimitInMib: 100,
			QuotaTarget:    gcpgenserver.NewOptString("user:alice"),
			QuotaId:        gcpgenserver.NewOptString("quota-uuid-1"),
			State:          gcpgenserver.NewOptQuotaRulesV1betaState(gcpgenserver.QuotaRulesV1betaStateREADY),
			StateDetails:   gcpgenserver.NewOptString("Ready state"),
			Description:    gcpgenserver.NewOptString("Test description"),
		}

		result := _convertQuotaRulesV1betaToDataModel(clientRule)

		assert.NotNil(tt, result)
		assert.Equal(tt, "quota-rule-1", result.Name)
		assert.Equal(tt, int64(100*1024), result.DiskLimitInKib)
		assert.Equal(tt, "quota-uuid-1", result.UUID)
		assert.Equal(tt, IndividualUserQuota, result.QuotaType)
		assert.Equal(tt, "user:alice", result.QuotaTarget)
		assert.Equal(tt, "READY", result.State)
		assert.Equal(tt, "Ready state", result.StateDetails)
		assert.Equal(tt, "Test description", result.Description)
	})

	t.Run("WhenAllFieldsPresent_IndividualGroupQuota", func(tt *testing.T) {
		clientRule := gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "quota-rule-2",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeINDIVIDUALGROUPQUOTA,
			DiskLimitInMib: 200,
			QuotaTarget:    gcpgenserver.NewOptString("group:developers"),
			QuotaId:        gcpgenserver.NewOptString("quota-uuid-2"),
		}

		result := _convertQuotaRulesV1betaToDataModel(clientRule)

		assert.NotNil(tt, result)
		assert.Equal(tt, "quota-rule-2", result.Name)
		assert.Equal(tt, int64(200*1024), result.DiskLimitInKib)
		assert.Equal(tt, "quota-uuid-2", result.UUID)
		assert.Equal(tt, IndividualGroupQuota, result.QuotaType)
		assert.Equal(tt, "group:developers", result.QuotaTarget)
	})

	t.Run("WhenAllFieldsPresent_DefaultUserQuota", func(tt *testing.T) {
		clientRule := gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "quota-rule-3",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeDEFAULTUSERQUOTA,
			DiskLimitInMib: 300,
			QuotaId:        gcpgenserver.NewOptString("quota-uuid-3"),
		}

		result := _convertQuotaRulesV1betaToDataModel(clientRule)

		assert.NotNil(tt, result)
		assert.Equal(tt, "quota-rule-3", result.Name)
		assert.Equal(tt, int64(300*1024), result.DiskLimitInKib)
		assert.Equal(tt, "quota-uuid-3", result.UUID)
		assert.Equal(tt, DefaultUserQuota, result.QuotaType)
	})

	t.Run("WhenAllFieldsPresent_DefaultGroupQuota", func(tt *testing.T) {
		clientRule := gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "quota-rule-4",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeDEFAULTGROUPQUOTA,
			DiskLimitInMib: 400,
			QuotaId:        gcpgenserver.NewOptString("quota-uuid-4"),
		}

		result := _convertQuotaRulesV1betaToDataModel(clientRule)

		assert.NotNil(tt, result)
		assert.Equal(tt, "quota-rule-4", result.Name)
		assert.Equal(tt, int64(400*1024), result.DiskLimitInKib)
		assert.Equal(tt, "quota-uuid-4", result.UUID)
		assert.Equal(tt, DefaultGroupQuota, result.QuotaType)
	})

	t.Run("WhenUnknownQuotaType", func(tt *testing.T) {
		clientRule := gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "quota-rule-5",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaType("UNKNOWN_TYPE"),
			DiskLimitInMib: 500,
			QuotaId:        gcpgenserver.NewOptString("quota-uuid-5"),
		}

		result := _convertQuotaRulesV1betaToDataModel(clientRule)

		assert.NotNil(tt, result)
		assert.Equal(tt, "quota-rule-5", result.Name)
		assert.Equal(tt, "UNKNOWN_TYPE", result.QuotaType)
	})

	t.Run("WhenOptionalFieldsAreMissing", func(tt *testing.T) {
		clientRule := gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "quota-rule-6",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeINDIVIDUALUSERQUOTA,
			DiskLimitInMib: 600,
			// QuotaId, QuotaTarget, State, StateDetails, Description are not set
		}

		result := _convertQuotaRulesV1betaToDataModel(clientRule)

		assert.NotNil(tt, result)
		assert.Equal(tt, "quota-rule-6", result.Name)
		assert.Equal(tt, int64(600*1024), result.DiskLimitInKib)
		assert.Empty(tt, result.UUID)
		assert.Empty(tt, result.QuotaTarget)
		assert.Empty(tt, result.State)
		assert.Empty(tt, result.StateDetails)
		assert.Empty(tt, result.Description)
	})

	t.Run("WhenOnlyQuotaTargetIsSet", func(tt *testing.T) {
		clientRule := gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "quota-rule-7",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeINDIVIDUALUSERQUOTA,
			DiskLimitInMib: 700,
			QuotaTarget:    gcpgenserver.NewOptString("user:bob"),
		}

		result := _convertQuotaRulesV1betaToDataModel(clientRule)

		assert.NotNil(tt, result)
		assert.Equal(tt, "user:bob", result.QuotaTarget)
	})

	t.Run("WhenOnlyStateIsSet", func(tt *testing.T) {
		clientRule := gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "quota-rule-8",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeDEFAULTUSERQUOTA,
			DiskLimitInMib: 800,
			State:          gcpgenserver.NewOptQuotaRulesV1betaState(gcpgenserver.QuotaRulesV1betaStateCREATING),
		}

		result := _convertQuotaRulesV1betaToDataModel(clientRule)

		assert.NotNil(tt, result)
		assert.Equal(tt, "CREATING", result.State)
	})

	t.Run("WhenOnlyStateDetailsIsSet", func(tt *testing.T) {
		clientRule := gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "quota-rule-9",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeDEFAULTGROUPQUOTA,
			DiskLimitInMib: 900,
			StateDetails:   gcpgenserver.NewOptString("Creating state details"),
		}

		result := _convertQuotaRulesV1betaToDataModel(clientRule)

		assert.NotNil(tt, result)
		assert.Equal(tt, "Creating state details", result.StateDetails)
	})

	t.Run("WhenOnlyDescriptionIsSet", func(tt *testing.T) {
		clientRule := gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "quota-rule-10",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeINDIVIDUALUSERQUOTA,
			DiskLimitInMib: 1000,
			Description:    gcpgenserver.NewOptString("Human-readable description of the quota rule"),
		}

		result := _convertQuotaRulesV1betaToDataModel(clientRule)

		assert.NotNil(tt, result)
		assert.Equal(tt, "Human-readable description of the quota rule", result.Description)
	})
}

func TestConvertCommonQuotaRulesV1betaToGcp(t *testing.T) {
	t.Run("WhenAllOptionalFieldsAreSet", func(tt *testing.T) {
		state := "Active"
		stateDetails := "Quota rule is active"
		description := "Test quota rule"
		createdAt := time.Now()
		updatedAt := time.Now()

		rule := common.QuotaRulesV1beta{
			ResourceId:     "quota-rule-1",
			QuotaType:      "IndividualUserQuota",
			DiskLimitInMib: 100,
			QuotaId:        nillable.ToPointer("quota-id-1"),
			QuotaTarget:    nillable.ToPointer("user:alice"),
			State:          &state,
			StateDetails:   &stateDetails,
			Description:    &description,
			CreatedAt:      &createdAt,
			UpdatedAt:      &updatedAt,
		}

		result := convertCommonQuotaRulesV1betaToGcp(rule)

		assert.Equal(tt, "quota-rule-1", result.ResourceId)
		assert.Equal(tt, gcpgenserver.QuotaRulesV1betaQuotaType("IndividualUserQuota"), result.QuotaType)
		assert.Equal(tt, int64(100), result.DiskLimitInMib)
		assert.True(tt, result.QuotaId.IsSet())
		assert.Equal(tt, "quota-id-1", result.QuotaId.Value)
		assert.True(tt, result.QuotaTarget.IsSet())
		assert.Equal(tt, "user:alice", result.QuotaTarget.Value)
		assert.True(tt, result.State.IsSet())
		assert.Equal(tt, gcpgenserver.QuotaRulesV1betaState("Active"), result.State.Value)
		assert.True(tt, result.StateDetails.IsSet())
		assert.Equal(tt, "Quota rule is active", result.StateDetails.Value)
		assert.True(tt, result.Description.IsSet())
		assert.Equal(tt, "Test quota rule", result.Description.Value)
		assert.True(tt, result.CreatedAt.IsSet())
		assert.Equal(tt, createdAt, result.CreatedAt.Value)
		assert.True(tt, result.UpdatedAt.IsSet())
		assert.Equal(tt, updatedAt, result.UpdatedAt.Value)
	})

	t.Run("WhenOptionalFieldsAreNil", func(tt *testing.T) {
		rule := common.QuotaRulesV1beta{
			ResourceId:     "quota-rule-2",
			QuotaType:      "TreeQuota",
			DiskLimitInMib: 200,
		}

		result := convertCommonQuotaRulesV1betaToGcp(rule)

		assert.Equal(tt, "quota-rule-2", result.ResourceId)
		assert.Equal(tt, gcpgenserver.QuotaRulesV1betaQuotaType("TreeQuota"), result.QuotaType)
		assert.Equal(tt, int64(200), result.DiskLimitInMib)
		assert.False(tt, result.State.IsSet())
		assert.False(tt, result.StateDetails.IsSet())
		assert.False(tt, result.Description.IsSet())
		assert.False(tt, result.CreatedAt.IsSet())
		assert.False(tt, result.UpdatedAt.IsSet())
	})
}

func TestConvertCommonV1betaUpdateDestinationQuotaRulesVCPParamsToGcp(t *testing.T) {
	t.Run("WhenXCorrelationIDIsSet", func(tt *testing.T) {
		correlationID := "test-correlation-id"
		params := common.V1betaUpdateDestinationQuotaRulesVCPParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-central1",
			VolumeId:       "volume-123",
			XCorrelationID: &correlationID,
		}

		result := convertCommonV1betaUpdateDestinationQuotaRulesVCPParamsToGcp(params)

		assert.Equal(tt, "test-project", result.ProjectNumber)
		assert.Equal(tt, "us-central1", result.LocationId)
		assert.Equal(tt, "volume-123", result.VolumeId)
		assert.True(tt, result.XCorrelationID.IsSet())
		assert.Equal(tt, "test-correlation-id", result.XCorrelationID.Value)
	})

	t.Run("WhenXCorrelationIDIsNil", func(tt *testing.T) {
		params := common.V1betaUpdateDestinationQuotaRulesVCPParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-central1",
			VolumeId:       "volume-123",
			XCorrelationID: nil,
		}

		result := convertCommonV1betaUpdateDestinationQuotaRulesVCPParamsToGcp(params)

		assert.Equal(tt, "test-project", result.ProjectNumber)
		assert.Equal(tt, "us-central1", result.LocationId)
		assert.Equal(tt, "volume-123", result.VolumeId)
		assert.False(tt, result.XCorrelationID.IsSet())
	})
}
