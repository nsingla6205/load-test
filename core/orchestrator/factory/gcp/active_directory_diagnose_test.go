package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	workflowEngineMock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/sdk/client"
)

// diagnoseTestData bundles the DB fixtures the happy path returns.
type diagnoseTestData struct {
	account   *datamodel.Account
	pool      *datamodel.Pool
	ad        *datamodel.ActiveDirectory
	createdJob *datamodel.Job
	params    *common.DiagnoseActiveDirectoryParams
}

func newDiagnoseTestData() diagnoseTestData {
	return diagnoseTestData{
		account:    &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "acct"},
		pool:       &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"}, Name: "pool-1", State: datamodel.LifeCycleStateREADY},
		ad:         &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{ID: 3, UUID: "ad-uuid"}, AdName: "corp", State: datamodel.LifeCycleStateREADY, ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{}},
		createdJob: &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-1"},
		params:     &common.DiagnoseActiveDirectoryParams{ProjectNumber: "pn", PoolUUID: "pool-uuid"},
	}
}

// withGetOrCreateAccount overrides the package-level account resolver.
func withGetOrCreateAccount(account *datamodel.Account, err error) func() {
	orig := getOrCreateAccount
	getOrCreateAccount = func(context.Context, database.Storage, string) (*datamodel.Account, error) {
		return account, err
	}
	return func() { getOrCreateAccount = orig }
}

func TestDiagnoseActiveDirectory_ValidationErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("nil params", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, nil)
		require.Error(t, err)
		assert.True(t, customerrors.IsUserInputValidationErr(err))
	})

	t.Run("empty pool uuid", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, &common.DiagnoseActiveDirectoryParams{ProjectNumber: "pn"})
		require.Error(t, err)
		assert.True(t, customerrors.IsUserInputValidationErr(err))
	})
}

func TestDiagnoseActiveDirectory_AccountError(t *testing.T) {
	ctx := context.Background()
	d := newDiagnoseTestData()
	defer withGetOrCreateAccount(nil, errors.New("account error"))()

	mockStorage := database.NewMockStorage(t)
	_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account error")
}

func TestDiagnoseActiveDirectory_PoolErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("pool not found", func(t *testing.T) {
		d := newDiagnoseTestData()
		defer withGetOrCreateAccount(d.account, nil)()
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").
			Return((*datamodel.Pool)(nil), customerrors.NewNotFoundErr("Pool", nil))

		_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
		require.Error(t, err)
		assert.True(t, customerrors.IsNotFoundErr(err))
	})

	t.Run("generic pool error", func(t *testing.T) {
		d := newDiagnoseTestData()
		defer withGetOrCreateAccount(d.account, nil)()
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").
			Return((*datamodel.Pool)(nil), errors.New("db down"))

		_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
		require.Error(t, err)
	})

	t.Run("nil pool", func(t *testing.T) {
		d := newDiagnoseTestData()
		defer withGetOrCreateAccount(d.account, nil)()
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return((*datamodel.Pool)(nil), nil)

		_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
		require.Error(t, err)
		assert.True(t, customerrors.IsNotFoundErr(err))
	})
}

// TestDiagnoseActiveDirectory_PoolStateGate covers the synchronous pool-state
// check. A diagnose raced against pool create/delete must be rejected as a 400
// here rather than accepted and failed later by the workflow.
func TestDiagnoseActiveDirectory_PoolStateGate(t *testing.T) {
	ctx := context.Background()

	blockedPoolStates := []string{
		datamodel.LifeCycleStateCreating,
		datamodel.LifeCycleStateUpdating,
		datamodel.LifeCycleStateDeleting,
		datamodel.LifeCycleStateDeleted,
		datamodel.LifeCycleStateError,
	}
	for _, state := range blockedPoolStates {
		t.Run("blocked when pool state "+state, func(t *testing.T) {
			d := newDiagnoseTestData()
			d.pool.State = state
			defer withGetOrCreateAccount(d.account, nil)()
			mockStorage := database.NewMockStorage(t)
			mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return(d.pool, nil)

			_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
			require.Error(t, err)
			assert.True(t, customerrors.IsUserInputValidationErr(err))
			assert.Contains(t, err.Error(), "pool-1")
			// The AD is never read and no job is created for a rejected pool.
			mockStorage.AssertNotCalled(t, "GetActiveDirectoryForPoolByPoolID", mock.Anything, mock.Anything)
			mockStorage.AssertNotCalled(t, "CreateJob", mock.Anything, mock.Anything)
			mockStorage.AssertNotCalled(t, "UpdateActiveDirectory", mock.Anything, mock.Anything)
		})
	}

	// DEGRADED is deliberately allowed: that is often exactly when an AD
	// connectivity check is wanted.
	for _, state := range []string{datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateDegraded} {
		t.Run("allowed when pool state "+state, func(t *testing.T) {
			d := newDiagnoseTestData()
			d.pool.State = state
			defer withGetOrCreateAccount(d.account, nil)()
			mockStorage := database.NewMockStorage(t)
			mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return(d.pool, nil)
			mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(7)).
				Return((*datamodel.ActiveDirectory)(nil), errors.New("db error"))

			// Reaching the AD lookup proves the pool gate let this state through.
			_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
			require.Error(t, err)
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestDiagnoseActiveDirectory_ADErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("get ad error", func(t *testing.T) {
		d := newDiagnoseTestData()
		defer withGetOrCreateAccount(d.account, nil)()
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return(d.pool, nil)
		mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(7)).
			Return((*datamodel.ActiveDirectory)(nil), errors.New("db error"))

		_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
		require.Error(t, err)
	})

	t.Run("ad not attached", func(t *testing.T) {
		d := newDiagnoseTestData()
		defer withGetOrCreateAccount(d.account, nil)()
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return(d.pool, nil)
		mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(7)).
			Return((*datamodel.ActiveDirectory)(nil), nil)

		_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
		require.Error(t, err)
		assert.True(t, customerrors.IsUserInputValidationErr(err))
	})

	blockedStates := []string{
		datamodel.LifeCycleStateError,
		datamodel.LifeCycleStateDisabled,
		datamodel.LifeCycleStateUpdating,
		datamodel.LifeCycleStateDeleting,
		datamodel.LifeCycleStateDiagnosing,
	}
	for _, state := range blockedStates {
		t.Run("blocked when state "+state, func(t *testing.T) {
			d := newDiagnoseTestData()
			d.ad.State = state
			defer withGetOrCreateAccount(d.account, nil)()
			mockStorage := database.NewMockStorage(t)
			mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return(d.pool, nil)
			mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(7)).Return(d.ad, nil)

			_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
			require.Error(t, err)
			assert.True(t, customerrors.IsUserInputValidationErr(err))
		})
	}
}

func TestDiagnoseActiveDirectory_CreateJobError(t *testing.T) {
	ctx := context.Background()
	d := newDiagnoseTestData()
	defer withGetOrCreateAccount(d.account, nil)()

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return(d.pool, nil)
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(7)).Return(d.ad, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return((*datamodel.Job)(nil), errors.New("job create failed"))

	_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
	require.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDiagnoseActiveDirectory_FlipStateError(t *testing.T) {
	ctx := context.Background()
	d := newDiagnoseTestData()
	defer withGetOrCreateAccount(d.account, nil)()

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return(d.pool, nil)
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(7)).Return(d.ad, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(d.createdJob, nil)
	mockStorage.On("UpdateActiveDirectory", mock.Anything, mock.Anything).
		Return((*datamodel.ActiveDirectory)(nil), errors.New("flip failed"))
	mockStorage.On("UpdateJob", mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

	_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
	require.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDiagnoseActiveDirectory_WorkflowStartErrorRevertsState(t *testing.T) {
	ctx := context.Background()
	d := newDiagnoseTestData()
	defer withGetOrCreateAccount(d.account, nil)()

	temporal := workflowEngineMock.NewMockTemporalTestClient(t)
	// Non-retryable error message so WorkflowExecutor invokes ExecuteWorkflow once.
	temporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("workflow start failed"))

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return(d.pool, nil)
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(7)).Return(d.ad, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(d.createdJob, nil)
	// Flip to diagnosing, then revert -> two UpdateActiveDirectory calls.
	mockStorage.On("UpdateActiveDirectory", mock.Anything, mock.Anything).Return(d.ad, nil).Times(2)
	mockStorage.On("UpdateJob", mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

	_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, temporal, d.params)
	require.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDiagnoseActiveDirectory_FlipStateJobUpdateError(t *testing.T) {
	ctx := context.Background()
	d := newDiagnoseTestData()
	defer withGetOrCreateAccount(d.account, nil)()

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return(d.pool, nil)
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(7)).Return(d.ad, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(d.createdJob, nil)
	mockStorage.On("UpdateActiveDirectory", mock.Anything, mock.Anything).
		Return((*datamodel.ActiveDirectory)(nil), errors.New("flip failed"))
	mockStorage.On("UpdateJob", mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).
		Return(errors.New("job update failed"))

	_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, nil, d.params)
	require.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDiagnoseActiveDirectory_WorkflowStartRevertFailures(t *testing.T) {
	ctx := context.Background()
	d := newDiagnoseTestData()
	defer withGetOrCreateAccount(d.account, nil)()

	temporal := workflowEngineMock.NewMockTemporalTestClient(t)
	// Non-retryable error message so WorkflowExecutor invokes ExecuteWorkflow once.
	temporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("workflow start failed"))

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return(d.pool, nil)
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(7)).Return(d.ad, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(d.createdJob, nil)
	mockStorage.On("UpdateActiveDirectory", mock.Anything, mock.Anything).Return(d.ad, nil).Times(2)
	mockStorage.On("UpdateJob", mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).
		Return(errors.New("job update failed"))

	_, _, err := _diagnoseActiveDirectory(ctx, mockStorage, temporal, d.params)
	require.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDiagnoseActiveDirectory_Success(t *testing.T) {
	ctx := context.Background()
	d := newDiagnoseTestData()
	defer withGetOrCreateAccount(d.account, nil)()

	temporal := workflowEngineMock.NewMockTemporalTestClient(t)
	temporal.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetPoolByUUID", mock.Anything, "pool-uuid").Return(d.pool, nil)
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(7)).Return(d.ad, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.MatchedBy(func(j *datamodel.Job) bool {
		return j.Type == string(datamodel.JobTypeDiagnoseActiveDirectory) && j.ResourceName == "corp"
	})).Return(d.createdJob, nil)
	mockStorage.On("UpdateActiveDirectory", mock.Anything, mock.MatchedBy(func(ad *datamodel.ActiveDirectory) bool {
		return ad.State == datamodel.LifeCycleStateDiagnosing
	})).Return(d.ad, nil).Once()

	ad, jobUUID, err := _diagnoseActiveDirectory(ctx, mockStorage, temporal, d.params)
	require.NoError(t, err)
	require.NotNil(t, ad)
	assert.Equal(t, "job-uuid", jobUUID)
	assert.Equal(t, datamodel.LifeCycleStateDiagnosing, ad.State)
	assert.Equal(t, datamodel.LifeCycleStateDiagnosingDetails, ad.StateDetails)
	assert.Equal(t, int64(123), d.params.AccountId)
	mockStorage.AssertExpectations(t)
}

// TestOrchestratorDiagnoseActiveDirectory exercises the public wrapper, which
// delegates to the diagnoseActiveDirectory package var.
func TestOrchestratorDiagnoseActiveDirectory(t *testing.T) {
	ctx := context.Background()

	t.Run("delegates and returns", func(t *testing.T) {
		orig := diagnoseActiveDirectory
		diagnoseActiveDirectory = func(context.Context, database.Storage, client.Client, *common.DiagnoseActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
			return &models.ActiveDirectory{AdName: "corp"}, "job-uuid", nil
		}
		defer func() { diagnoseActiveDirectory = orig }()

		o := &GCPOrchestrator{storage: database.NewMockStorage(t)}
		ad, jobUUID, err := o.DiagnoseActiveDirectory(ctx, &common.DiagnoseActiveDirectoryParams{PoolUUID: "p"})
		require.NoError(t, err)
		assert.Equal(t, "job-uuid", jobUUID)
		assert.Equal(t, "corp", ad.AdName)
	})

	t.Run("propagates error", func(t *testing.T) {
		orig := diagnoseActiveDirectory
		diagnoseActiveDirectory = func(context.Context, database.Storage, client.Client, *common.DiagnoseActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
			return nil, "", errors.New("diagnose failed")
		}
		defer func() { diagnoseActiveDirectory = orig }()

		o := &GCPOrchestrator{storage: database.NewMockStorage(t)}
		_, _, err := o.DiagnoseActiveDirectory(ctx, &common.DiagnoseActiveDirectoryParams{PoolUUID: "p"})
		require.Error(t, err)
	})
}

func TestUpdateActiveDirectory_RejectsWhenDiagnosing(t *testing.T) {
	ctx := context.Background()
	updateParams := &common.UpdateActiveDirectoryParams{
		AccountId:                  "123",
		ActiveDirectoryId:          "ad-uuid",
		LocationId:                 "us-central1",
		XCorrelationId:             "corr",
		Username:                   nillable.GetStringPtr("admin@test.local"),
		Domain:                     nillable.GetStringPtr("test.local"),
		DNS:                        nillable.GetStringPtr("10.0.0.1"),
		NetBIOS:                    nillable.GetStringPtr("TEST"),
		SecurityOperators:          []string{"sec"},
		BackupOperators:            []string{"bak"},
		Administrators:             []string{"adm"},
		OrganizationalUnit:         nillable.GetStringPtr("OU=test"),
		Site:                       nillable.GetStringPtr("site"),
		KdcIP:                      nillable.GetStringPtr("10.0.0.2"),
		KdcHostname:                nillable.GetStringPtr("kdc.test.local"),
		AesEncryption:              nillable.GetBoolPtr(true),
		EncryptDCConnections:       nillable.GetBoolPtr(true),
		LdapSigning:                nillable.GetBoolPtr(true),
		AllowLocalNFSUsersWithLdap: nillable.GetBoolPtr(false),
	}
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}

	origRegion := utils.ParseAndValidateRegionAndZone
	utils.ParseAndValidateRegionAndZone = func(string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	defer func() { utils.ParseAndValidateRegionAndZone = origRegion }()

	origAccount := getOrCreateAccount
	origGetAD := getActiveDirectory
	getOrCreateAccount = func(context.Context, database.Storage, string) (*datamodel.Account, error) {
		return account, nil
	}
	getActiveDirectory = func(context.Context, *common.GetADParams, database.Storage) (*models.ActiveDirectory, error) {
		return &models.ActiveDirectory{
			BaseModel: models.BaseModel{UUID: "ad-uuid"},
			AdName:    "corp",
			State:     datamodel.LifeCycleStateDiagnosing,
		}, nil
	}
	defer func() {
		getOrCreateAccount = origAccount
		getActiveDirectory = origGetAD
	}()

	_, _, err := _updateActiveDirectory(ctx, database.NewMockStorage(t), nil, updateParams)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ActiveDirectoryDiagnosingBlockedMessage)
}
