package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func newAccount() *datamodel.Account {
	now := time.Now()
	return &datamodel.Account{
		BaseModel: datamodel.BaseModel{
			UUID:      "account-uuid-1",
			CreatedAt: now,
			UpdatedAt: now,
		},
		Name:  "test-account",
		State: "ACTIVE",
	}
}

func TestV1ListAccounts_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	account := newAccount()
	mockOrch.On("ListAccounts", mock.Anything).Return([]*datamodel.Account{account}, nil)

	result, err := handler.V1ListAccounts(context.Background(), oasgenserver.V1ListAccountsParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1ListAccountsOKApplicationJSON)
	assert.True(t, ok)
	assert.Len(t, *res, 1)
	assert.Equal(t, "account-uuid-1", (*res)[0].UUID.Value)
	assert.Equal(t, "test-account", (*res)[0].Name)
	assert.Equal(t, "ACTIVE", (*res)[0].State.Value)
}

func TestV1ListAccounts_OrchestratorError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("ListAccounts", mock.Anything).Return(nil, errors.New("db error"))

	result, err := handler.V1ListAccounts(context.Background(), oasgenserver.V1ListAccountsParams{})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1ListAccountsInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), res.Code)
}

func TestV1DescribeAccount_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{
			UUID:      "account-uuid-1",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Name: "test-account",
	}
	mockOrch.On("GetAccountByUUID", mock.Anything, "account-uuid-1").Return(account, nil)

	result, err := handler.V1DescribeAccount(context.Background(), oasgenserver.V1DescribeAccountParams{UUID: "account-uuid-1"})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.AccountV1)
	assert.True(t, ok)
	assert.Equal(t, "account-uuid-1", res.UUID.Value)
	assert.Equal(t, "test-account", res.Name)
	assert.False(t, res.State.IsSet())
}

func TestV1DescribeAccount_NotFound(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("GetAccountByUUID", mock.Anything, "account-uuid-1").Return(nil, notFoundErr())

	result, err := handler.V1DescribeAccount(context.Background(), oasgenserver.V1DescribeAccountParams{UUID: "account-uuid-1"})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1DescribeAccountNotFound)
	assert.True(t, ok)
	assert.Equal(t, float64(404), res.Code)
	assert.Equal(t, "Account not found", res.Message)
}

func TestV1DescribeAccount_InternalServerError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("GetAccountByUUID", mock.Anything, "account-uuid-1").Return(nil, errors.New("unexpected"))

	result, err := handler.V1DescribeAccount(context.Background(), oasgenserver.V1DescribeAccountParams{UUID: "account-uuid-1"})

	assert.NoError(t, err)
	res, ok := result.(*oasgenserver.V1DescribeAccountInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), res.Code)
}
