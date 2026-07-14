package common

import (
	"context"
	"strings"

	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

// DataLifIPRangeStore loads data LIF ip_ranges and validates capacity (CVS preSelectIPRange parity).
type DataLifIPRangeStore interface {
	GetDataLIFIPRanges(ctx context.Context, accountUUID, network string) ([]*datamodel.IPRange, error)
	ValidateIPAddressesAvailableInIPRange(ctx context.Context, ipRangeUUID string, ipAddressCount int) error
}

// SecondaryIPRangeClient requests an additional secondary range from CVN when existing ranges are full.
type SecondaryIPRangeClient interface {
	CreateSecondaryIPRangeWithPolling(params *sdk.SecondaryIPRangeParams) (string, error)
}

// PreSelectDataLifIPRangeParams mirrors CVS preSelectIPRange inputs for PO pool create.
type PreSelectDataLifIPRangeParams struct {
	AccountUUID    string
	VendorSubnetID string
	HostUUID       string
	SubnetMaskSize int64
	IPAddressCount int
}

// IsDataLifIPRangeExhausted reports whether err means the range cannot satisfy the requested count.
// ValidateIPAddressesAvailableInIPRange wraps unavailable errors in VCPError, so callers must unwrap.
func IsDataLifIPRangeExhausted(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), ErrNoIPAddressesAvailable) {
		return true
	}
	if customerrors.IsUnavailableErr(err) {
		return true
	}
	var vcpErr *vsaerrors.CustomError
	if vsaerrors.As(err, &vcpErr) && vcpErr.Unwrap() != nil {
		return IsDataLifIPRangeExhausted(vcpErr.Unwrap())
	}
	return false
}

// PreSelectDataLifIPRange selects an existing data LIF ip_range with free addresses, or asks CVN
// for a secondary range when all existing ranges are exhausted. Returns "" when no ranges exist
// yet (first pool on the host/network), letting CreateDataPathV1 provision the initial range.
func PreSelectDataLifIPRange(
	ctx context.Context,
	store DataLifIPRangeStore,
	cvn SecondaryIPRangeClient,
	params PreSelectDataLifIPRangeParams,
) (string, error) {
	ipRanges, err := store.GetDataLIFIPRanges(ctx, params.AccountUUID, params.VendorSubnetID)
	if err != nil || len(ipRanges) == 0 {
		return "", err
	}

	for _, ipRange := range ipRanges {
		validateErr := store.ValidateIPAddressesAvailableInIPRange(ctx, ipRange.UUID, params.IPAddressCount)
		if validateErr != nil {
			if !IsDataLifIPRangeExhausted(validateErr) {
				return "", validateErr
			}
			continue
		}
		return ipRange.UUID, nil
	}

	remoteAccountID := params.VendorSubnetID
	secParams := &sdk.SecondaryIPRangeParams{
		AccountUUID:     params.AccountUUID,
		RemoteAccountID: &remoteAccountID,
		SubnetMaskSize:  params.SubnetMaskSize,
	}
	return cvn.CreateSecondaryIPRangeWithPolling(secParams)
}
