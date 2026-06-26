package api

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func accountToV1(a *datamodel.Account) oasgenserver.AccountV1 {
	v := oasgenserver.AccountV1{
		UUID:      oasgenserver.NewOptString(a.UUID),
		CreatedAt: oasgenserver.NewOptDateTime(a.CreatedAt),
		UpdatedAt: oasgenserver.NewOptDateTime(a.UpdatedAt),
		Name:      a.Name,
	}
	if a.State != "" {
		v.State = oasgenserver.NewOptString(a.State)
	}
	return v
}

// V1ListAccounts lists all active accounts.
func (h Handler) V1ListAccounts(ctx context.Context, _ oasgenserver.V1ListAccountsParams) (oasgenserver.V1ListAccountsRes, error) {
	accounts, err := h.Orchestrator.ListAccounts(ctx)
	if err != nil {
		return &oasgenserver.V1ListAccountsInternalServerError{
			Message: fmt.Sprintf("Failed to list accounts: %v", err),
			Code:    500,
		}, nil
	}

	result := make(oasgenserver.V1ListAccountsOKApplicationJSON, 0, len(accounts))
	for _, account := range accounts {
		result = append(result, accountToV1(account))
	}
	return &result, nil
}

// V1DescribeAccount retrieves one account by UUID.
func (h Handler) V1DescribeAccount(ctx context.Context, params oasgenserver.V1DescribeAccountParams) (oasgenserver.V1DescribeAccountRes, error) {
	account, err := h.Orchestrator.GetAccountByUUID(ctx, params.UUID)
	if err != nil {
		if customErr := asVCPError(err); customErr != nil && customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError) {
			return &oasgenserver.V1DescribeAccountNotFound{Message: "Account not found", Code: 404}, nil
		}
		return &oasgenserver.V1DescribeAccountInternalServerError{
			Message: fmt.Sprintf("Failed to describe account: %v", err),
			Code:    500,
		}, nil
	}
	result := accountToV1(account)
	return &result, nil
}
