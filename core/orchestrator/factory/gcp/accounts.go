package gcp

import (
	"context"
	"errors"
	"time"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

var (
	createAccount      = _createAccount
	getOrCreateAccount = _getOrCreateAccount
	getAccountWithName = _getAccountWithName
	getAccountFromUUID = _getAccountFromUUID
)

// Returns an account if exists. Creates a new account if it does not exist.
func _getOrCreateAccount(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
	account, err := getAccountWithName(ctx, se, accountName)
	if err == nil {
		if account.DeletedAt != nil || account.State == datamodel.AccountStateDisabled {
			// Resurrect account
			return nil, errors.New("account is disabled")
		}
		return account, nil
	}

	account, err = createAccount(ctx, se, accountName)
	if err != nil {
		// Get account in create account race scenario
		return getAccountWithName(ctx, se, accountName)
	}
	return account, nil
}

// Creates a new account.
func _createAccount(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
	var tags string
	createdDate := time.Now()
	dbAccount := &datamodel.Account{
		BaseModel: datamodel.BaseModel{
			UUID:      utils.RandomUUID(),
			CreatedAt: createdDate,
			UpdatedAt: createdDate,
		},
		Name:  accountName,
		State: datamodel.AccountStateEnabled,
		Tags:  tags,
	}

	createdAccount, err := se.CreateAccount(ctx, dbAccount)
	if err != nil {
		return nil, err
	}
	return createdAccount, nil
}

// Return an account with filtering of account Name.
func _getAccountWithName(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
	account, err := se.GetAccount(ctx, accountName)
	if err != nil {
		return nil, err
	}
	return account, nil
}

func (o *GCPOrchestrator) GetAccount(ctx context.Context, accountName string) (*datamodel.Account, error) {
	se := o.storage
	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		return nil, err
	}
	if account.DeletedAt != nil || account.State == datamodel.AccountStateDisabled {
		return nil, customerrors.NewNotFoundErr("account not found or disabled", nil)
	}
	return account, nil
}

func (o *GCPOrchestrator) PersistAccountTrialMetadataIfSet(ctx context.Context, accountName string, trial *commonparams.TrialModeParams) error {
	account, err := getOrCreateAccount(ctx, o.storage, accountName)
	if err != nil {
		return err
	}
	return persistAccountTrialMetadataIfSet(ctx, o.storage, account, trial)
}

// Return an account with filtering of account UUID.
func _getAccountFromUUID(ctx context.Context, se database.Storage, accountUUID string) (*datamodel.Account, error) {
	account, err := se.GetAccountByUUID(ctx, accountUUID)
	if err != nil {
		return nil, err
	}
	return account, nil
}

// ListAccounts lists all active accounts.
func (o *GCPOrchestrator) ListAccounts(ctx context.Context) ([]*datamodel.Account, error) {
	return o.storage.GetAccounts(ctx, false, nil)
}

// GetAccountByUUID returns an account by UUID.
func (o *GCPOrchestrator) GetAccountByUUID(ctx context.Context, accountUUID string) (*datamodel.Account, error) {
	return o.storage.GetAccountByUUID(ctx, accountUUID)
}

// validateTrialModeParams returns an error when trialMode is present but invalid.
func validateTrialModeParams(trial *commonparams.TrialModeParams) error {
	if trial == nil {
		return nil
	}
	if trial.Start == nil || trial.End == nil {
		return customerrors.NewUserInputValidationErr("trialMode startTime and endTime must be set when trialMode is provided")
	}
	if trial.Start.IsZero() || trial.End.IsZero() {
		return customerrors.NewUserInputValidationErr("trialMode startTime and endTime must be set when trialMode is provided")
	}
	if !trial.Start.Before(*trial.End) {
		return customerrors.NewUserInputValidationErr("trialMode startTime must be before endTime")
	}
	return nil
}

// persistAccountTrialMetadataIfSet validates trialMode then writes trial window to account_metadata
// when TRIAL_ACCOUNT_SYNC_ENABLED is true (same flag as the background trial sync admin job).
func persistAccountTrialMetadataIfSet(ctx context.Context, se database.Storage, account *datamodel.Account, trial *commonparams.TrialModeParams) error {
	if trial == nil {
		return nil
	}
	if err := validateTrialModeParams(trial); err != nil {
		return err
	}
	if !env.GetBool("TRIAL_ACCOUNT_SYNC_ENABLED", false) {
		return nil
	}

	return se.UpdateAccountTrialMetadata(ctx, account, &datamodel.AccountTrialMode{
		StartTime: trial.Start,
		EndTime:   trial.End,
	})
}

// getAccountName safely gets the account name, returning empty string if account is nil
func getAccountName(account *datamodel.Account) string {
	if account == nil {
		return ""
	}
	return account.Name
}
