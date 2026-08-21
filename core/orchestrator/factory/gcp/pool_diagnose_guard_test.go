package gcp

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestCreatePool_RejectsDiagnosingAD(t *testing.T) {
	ctx := context.Background()
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
	params := &common.CreatePoolParams{
		AccountName:       "acct",
		ActiveDirectoryId: "ad-uuid",
		ADExistsInVCP:     true,
		ActiveDirectory:   &models.ActiveDirectory{State: datamodel.LifeCycleStateDiagnosing},
	}

	origAccount := getOrCreateAccount
	origValidate := ValidateCreatePoolParams
	getOrCreateAccount = func(context.Context, database.Storage, string) (*datamodel.Account, error) {
		return account, nil
	}
	ValidateCreatePoolParams = func(*common.CreatePoolParams, log.Logger) error { return nil }
	defer func() {
		getOrCreateAccount = origAccount
		ValidateCreatePoolParams = origValidate
	}()

	_, _, err := _createPool(ctx, database.NewMockStorage(t), nil, params)
	require.Error(t, err)
	assert.True(t, customerrors.IsUserInputValidationErr(err))
}

func TestUpdatePool_RejectsWhenExistingADDiagnosing(t *testing.T) {
	ctx := context.Background()
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acct"}
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:         datamodel.BaseModel{ID: 7, UUID: "pool-uuid"},
			ActiveDirectoryID: sql.NullInt64{Int64: 3, Valid: true},
		},
	}
	diagnosingAD := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{ID: 3},
		State:     datamodel.LifeCycleStateDiagnosing,
	}
	params := &common.UpdatePoolParams{AccountName: "acct", PoolId: "pool-uuid"}

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetPool", mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(7)).Return(diagnosingAD, nil)

	origAccount := getAccountWithName
	origValidate := ValidateAndSetUpdatePoolParams
	getAccountWithName = func(context.Context, database.Storage, string) (*datamodel.Account, error) {
		return account, nil
	}
	ValidateAndSetUpdatePoolParams = func(*common.UpdatePoolParams, *datamodel.Pool) error { return nil }
	defer func() {
		getAccountWithName = origAccount
		ValidateAndSetUpdatePoolParams = origValidate
	}()

	_, _, err := _updatePool(ctx, mockStorage, nil, params)
	require.Error(t, err)
	assert.True(t, customerrors.IsUserInputValidationErr(err))
}

func TestUpdatePool_RejectsAttachingDiagnosingAD(t *testing.T) {
	ctx := context.Background()
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acct"}
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-uuid"}},
	}
	diagnosingAD := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{ID: 3, UUID: "ad-uuid"},
		State:     datamodel.LifeCycleStateDiagnosing,
	}
	params := &common.UpdatePoolParams{
		AccountName:             "acct",
		PoolId:                  "pool-uuid",
		ActiveDirectoryConfigId: "ad-uuid",
		IfADExistsInVCP:         true,
	}

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetPool", mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
	mockStorage.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, "ad-uuid", int64(1)).Return(diagnosingAD, nil)

	origAccount := getAccountWithName
	origValidate := ValidateAndSetUpdatePoolParams
	getAccountWithName = func(context.Context, database.Storage, string) (*datamodel.Account, error) {
		return account, nil
	}
	ValidateAndSetUpdatePoolParams = func(*common.UpdatePoolParams, *datamodel.Pool) error { return nil }
	defer func() {
		getAccountWithName = origAccount
		ValidateAndSetUpdatePoolParams = origValidate
	}()

	_, _, err := _updatePool(ctx, mockStorage, nil, params)
	require.Error(t, err)
	assert.True(t, customerrors.IsUserInputValidationErr(err))
}
