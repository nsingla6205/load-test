package gcp

import (
	"context"
	"fmt"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

const (
	// POValidationAccountHostPinMsg is returned when the account is not pinned to the target host.
	POValidationAccountHostPinMsg = "account is not pinned to the requested host with provisioned credentials"
	// POValidationHostMgmtIPMsg is returned when the host row has no management IP.
	POValidationHostMgmtIPMsg = "host management IP is not configured"
)

// validatePOPoolHost resolves clusterName to a host, enforces account-host pin, checks
// efficiency-factor capacity, and sets params.HostUUID for persistence.
func validatePOPoolHost(ctx context.Context, se database.Storage, account *datamodel.Account, params *commonparams.CreatePoolParams) error {
	if params == nil || account == nil {
		return customerrors.NewUserInputValidationErr("invalid PO pool request")
	}

	host, err := se.GetHostByName(ctx, params.ClusterName)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return customerrors.NewUserInputValidationErr(
				fmt.Sprintf("clusterName %q does not match a registered host", params.ClusterName))
		}
		return err
	}
	if host.IPAddress == "" {
		return customerrors.NewUserInputValidationErr(POValidationHostMgmtIPMsg)
	}

	accountHost, err := se.GetAccountHostByAccountAndHost(ctx, account.ID, host.UUID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return customerrors.NewUserInputValidationErr(POValidationAccountHostPinMsg)
		}
		return err
	}
	if !accountHost.IsPinnedWithCredentials() {
		return customerrors.NewUserInputValidationErr(POValidationAccountHostPinMsg)
	}

	allocated, err := se.SumPoolSizeByHostUUID(ctx, host.UUID)
	if err != nil {
		return err
	}
	if capErr := commonparams.ValidatePOHostCapacity(host, allocated, params.SizeInBytes); capErr != nil {
		return customerrors.NewUserInputValidationErr(capErr.Error())
	}

	params.HostUUID = host.UUID
	params.ClusterName = host.Name
	return nil
}
