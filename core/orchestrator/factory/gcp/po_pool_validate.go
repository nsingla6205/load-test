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

// validatePOPoolHost resolves clusterName to a host, enforces account-host pin, and sets
// params.HostUUID for persistence.
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

	params.HostUUID = host.UUID
	params.ClusterName = host.Name
	return nil
}

// validatePOPoolSizeChange enforces PO shrink rules using expert-mode volume usage (grow is not host-capacity gated).
func validatePOPoolSizeChange(ctx context.Context, se database.Storage, pool *datamodel.Pool, newSize uint64) error {
	if pool == nil {
		return customerrors.NewUserInputValidationErr("invalid PO pool")
	}
	currentSize := uint64(pool.SizeInBytes)
	if newSize == currentSize {
		return nil
	}
	if err := commonparams.ValidatePOPoolHostUUID(pool); err != nil {
		return customerrors.NewNonRetryableErr(err.Error())
	}

	if newSize > currentSize {
		return nil
	}

	host, err := se.GetHost(ctx, pool.HostUUID)
	if err != nil {
		return err
	}

	model, err := datamodel.EffectiveHostProvisioningModel(host)
	if err != nil {
		return customerrors.NewBadRequestErr(err.Error())
	}
	if model == datamodel.ProvisioningModelThin {
		return nil
	}

	capacity, capErr := se.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID)
	if capErr != nil {
		return capErr
	}
	if shrinkErr := commonparams.ValidatePOPoolShrink(newSize, capacity.TotalSize); shrinkErr != nil {
		return customerrors.NewUserInputValidationErr(shrinkErr.Error())
	}
	return nil
}

// validatePOPoolUpdate validates PO update size change and rejects forbidden direct-call fields.
func validatePOPoolUpdate(ctx context.Context, se database.Storage, params *commonparams.UpdatePoolParams, pool *datamodel.Pool) error {
	if params == nil || pool == nil {
		return customerrors.NewUserInputValidationErr("invalid PO pool update request")
	}
	if params.ActiveDirectoryConfigId != "" {
		return customerrors.NewUserInputValidationErr(commonparams.POValidationActiveDirectoryMsg)
	}
	if params.LargeCapacity != nil && *params.LargeCapacity != pool.LargeCapacity {
		return customerrors.NewUserInputValidationErr(commonparams.POValidationLargeCapacityUpdateMsg)
	}
	if params.SizeInBytes != uint64(pool.SizeInBytes) {
		return validatePOPoolSizeChange(ctx, se, pool, params.SizeInBytes)
	}
	return nil
}
