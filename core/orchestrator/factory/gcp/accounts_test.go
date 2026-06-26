package gcp

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestGetOrCreateAccount(t *testing.T) {
	t.Run("WhenGetAccountWithNameFails", func(tt *testing.T) {
		ctx := context.Background()
		se := database.Storage(nil)

		dbAccount := &datamodel.Account{
			Name: "test_account",
		}

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		createAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}

		account, err := getOrCreateAccount(ctx, se, "test_account")
		if err != nil {
			t.Errorf("Expected nil, got Error %v", err)
		}
		if account.Name != "test_account" {
			t.Errorf("Expected account name 'test_account', got %v", account.Name)
		}
	})

	t.Run("WhenAccountIsDisabled", func(tt *testing.T) {
		ctx := context.Background()
		se := database.Storage(nil)

		disabledAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
			},
			Name:  "test_account",
			State: datamodel.AccountStateDisabled,
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return disabledAccount, nil
		}

		_, err := getOrCreateAccount(ctx, se, "test_account")
		if err == nil {
			t.Errorf("Expected error, got nil")
		}
		if err.Error() != "account is disabled" {
			t.Errorf("Expected error 'account is disabled', got %v", err)
		}
	})

	t.Run("WhenCreateAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		se := database.Storage(nil)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		createAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("failed to create account")
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		_, err := getOrCreateAccount(ctx, se, "test_account")
		if err == nil {
			t.Errorf("Expected error, got nil")
		}
		if err.Error() != "account not found" {
			t.Errorf("Expected error 'account not found', got %v", err)
		}
	})

	t.Run("WhenAccountIsCreatedSuccessfully", func(tt *testing.T) {
		ctx := context.Background()
		se := database.Storage(nil)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		createdAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
			},
			Name:  "test_account",
			State: datamodel.AccountStateEnabled,
		}
		createAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return createdAccount, nil
		}

		account, err := getOrCreateAccount(ctx, se, "test_account")
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if account.Name != "test_account" {
			t.Errorf("Expected account name 'test_account', got %v", account.Name)
		}
	})
}

func TestCreateAccount(t *testing.T) {
	testAccount := "test_account"
	t.Run("WhenAccountIsCreatedSuccessfully", func(tt *testing.T) {
		ctx := context.Background()
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

		account, err := _createAccount(ctx, store, testAccount)

		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}
		if account.Name != testAccount {
			tt.Errorf("Expected account name 'test_account', got %v", account.Name)
		}
	})
	t.Run("WhenCreateAccountFails", func(tt *testing.T) {
		ctx := context.Background()
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

		account, err := _createAccount(ctx, store, testAccount)
		if err != nil {
			tt.Errorf("Expected nil, got error")
		}
		if account.Name != testAccount {
			tt.Errorf("Expected account name 'test_account', got %v", account.Name)
		}

		_, err = _createAccount(ctx, store, testAccount)
		if err == nil {
			tt.Errorf("Expected error, got nil")
		}
		if err.Error() != "account already exists" {
			tt.Errorf("Expected error 'account already exists', got %v", err)
		}
	})
}

func TestGetAccountName(t *testing.T) {
	t.Run("WhenAccountIsNil", func(tt *testing.T) {
		result := getAccountName(nil)
		assert.Equal(tt, "", result, "Expected empty string when account is nil")
	})

	t.Run("WhenAccountIsNotNil", func(tt *testing.T) {
		account := &datamodel.Account{
			Name: "test_account",
		}
		result := getAccountName(account)
		assert.Equal(tt, "test_account", result, "Expected account name 'test_account'")
	})

	t.Run("WhenAccountHasEmptyName", func(tt *testing.T) {
		account := &datamodel.Account{
			Name: "",
		}
		result := getAccountName(account)
		assert.Equal(tt, "", result, "Expected empty string when account name is empty")
	})
}

func TestGetAccountWithName(t *testing.T) {
	testAccount := "test_account"
	t.Run("WhenAccountDoesNotExist", func(tt *testing.T) {
		ctx := context.Background()
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

		account, mainErr := _getAccountWithName(ctx, store, testAccount)
		assert.NotNil(tt, mainErr, "Expected an error when account does not exist")
		assert.EqualError(tt, mainErr, "Account not found")
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(mainErr, &customErr) {
			assert.Equal(tt, customErr.OriginalErr.Error(), "account not found")
			assert.Equal(tt, customErr.HttpCode, nillable.ToPointer(404), "Expected HTTP code 404 for not found error but got %v", customErr.HttpCode)
			assert.Equal(tt, customErr.TrackingID, 2101)
			assert.Equal(tt, customErr.Message, "Account not found")
			assert.Equal(tt, customErr.Retriable, false)
		} else {
			tt.Fatalf("Expected a CustomError, got %T", err)
		}
		if account != nil {
			tt.Errorf("Expected nil account, got %v", account)
		}
	})
	t.Run("WhenAccountExists", func(tt *testing.T) {
		ctx := context.Background()
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

		err = store.DB().Create(&datamodel.Account{Name: testAccount}).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		account, err := _getAccountWithName(ctx, store, testAccount)
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}
		if account.Name != testAccount {
			tt.Errorf("Expected account name '%s', got %v", testAccount, account.Name)
		}
	})
}

func TestValidateTrialModeParams(t *testing.T) {
	start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
	end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)

	t.Run("nil trial is valid", func(t *testing.T) {
		assert.NoError(t, validateTrialModeParams(nil))
	})

	t.Run("valid window", func(t *testing.T) {
		s, e := start, end
		assert.NoError(t, validateTrialModeParams(&commonparams.TrialModeParams{Start: &s, End: &e}))
	})

	t.Run("missing start", func(t *testing.T) {
		e := end
		err := validateTrialModeParams(&commonparams.TrialModeParams{End: &e})
		assert.True(t, errors.IsUserInputValidationErr(err))
	})

	t.Run("zero start time", func(t *testing.T) {
		var zero time.Time
		e := end
		err := validateTrialModeParams(&commonparams.TrialModeParams{Start: &zero, End: &e})
		assert.True(t, errors.IsUserInputValidationErr(err))
	})

	t.Run("end before start", func(t *testing.T) {
		s, e := end, start
		err := validateTrialModeParams(&commonparams.TrialModeParams{Start: &s, End: &e})
		assert.True(t, errors.IsUserInputValidationErr(err))
		assert.Contains(t, err.Error(), "trialMode startTime must be before endTime")
	})
}

func withTrialAccountSyncEnabled(t *testing.T) {
	t.Helper()
	require.NoError(t, os.Setenv("TRIAL_ACCOUNT_SYNC_ENABLED", "true"))
	t.Cleanup(func() { _ = os.Unsetenv("TRIAL_ACCOUNT_SYNC_ENABLED") })
}

func TestPersistAccountTrialMetadataIfSet_PassesAccountToStorageWithoutRefetch(t *testing.T) {
	withTrialAccountSyncEnabled(t)

	ctx := context.Background()
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "project-1",
	}
	defer stubGetOrCreateAccount(account)()

	start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
	end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)
	s, e := start, end
	trial := &commonparams.TrialModeParams{Start: &s, End: &e}

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		UpdateAccountTrialMetadata(ctx, account, &datamodel.AccountTrialMode{StartTime: &start, EndTime: &end}).
		Return(nil).
		Once()
	mockStorage.AssertNotCalled(t, "GetAccountByUUID", mock.Anything, mock.Anything)

	o := &GCPOrchestrator{storage: mockStorage}
	require.NoError(t, o.PersistAccountTrialMetadataIfSet(ctx, account.Name, trial))
}

func TestPersistAccountTrialMetadataIfSet_InvalidTrialDoesNotUpdateStorage(t *testing.T) {
	ctx := context.Background()
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "project-1",
	}
	defer stubGetOrCreateAccount(account)()

	start := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)
	end := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
	s, e := start, end
	trial := &commonparams.TrialModeParams{Start: &s, End: &e}

	mockStorage := database.NewMockStorage(t)
	mockStorage.AssertNotCalled(t, "UpdateAccountTrialMetadata", mock.Anything, mock.Anything, mock.Anything)

	o := &GCPOrchestrator{storage: mockStorage}
	err := o.PersistAccountTrialMetadataIfSet(ctx, account.Name, trial)
	require.Error(t, err)
	assert.True(t, errors.IsUserInputValidationErr(err))
}

func TestPersistAccountTrialMetadataIfSet_OmittedTrialOnSecondCallDoesNotUpdate(t *testing.T) {
	withTrialAccountSyncEnabled(t)

	ctx := context.Background()
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "project-1",
	}
	defer stubGetOrCreateAccount(account)()

	start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
	end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)
	s, e := start, end
	trial := &commonparams.TrialModeParams{Start: &s, End: &e}

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		UpdateAccountTrialMetadata(ctx, account, &datamodel.AccountTrialMode{StartTime: &start, EndTime: &end}).
		Return(nil).
		Once()

	o := &GCPOrchestrator{storage: mockStorage}
	require.NoError(t, o.PersistAccountTrialMetadataIfSet(ctx, account.Name, trial))
	require.NoError(t, o.PersistAccountTrialMetadataIfSet(ctx, account.Name, nil))
}

func TestGCPOrchestrator_ListAccounts(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := context.Background()

	created, err := store.CreateAccount(ctx, &datamodel.Account{Name: "test-account"})
	require.NoError(t, err)

	accounts, err := orch.ListAccounts(ctx)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, created.Name, accounts[0].Name)
	assert.Equal(t, created.UUID, accounts[0].UUID)
}

func TestGCPOrchestrator_GetAccountByUUID(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := context.Background()

	created, err := store.CreateAccount(ctx, &datamodel.Account{Name: "test-account"})
	require.NoError(t, err)

	account, err := orch.GetAccountByUUID(ctx, created.UUID)
	require.NoError(t, err)
	assert.Equal(t, created.Name, account.Name)
	assert.Equal(t, created.UUID, account.UUID)
}

func TestPersistAccountTrialMetadataIfSet_SkipsPersistenceWhenDisabled(t *testing.T) {
	ctx := context.Background()
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "project-1",
	}
	defer stubGetOrCreateAccount(account)()

	t.Cleanup(func() { _ = os.Unsetenv("TRIAL_ACCOUNT_SYNC_ENABLED") })
	require.NoError(t, os.Unsetenv("TRIAL_ACCOUNT_SYNC_ENABLED"))

	start := time.Date(2025, 5, 14, 11, 0, 0, 0, time.UTC)
	end := time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC)
	s, e := start, end
	trial := &commonparams.TrialModeParams{Start: &s, End: &e}

	mockStorage := database.NewMockStorage(t)
	mockStorage.AssertNotCalled(t, "UpdateAccountTrialMetadata", mock.Anything, mock.Anything, mock.Anything)

	o := &GCPOrchestrator{storage: mockStorage}
	require.NoError(t, o.PersistAccountTrialMetadataIfSet(ctx, account.Name, trial))
}
