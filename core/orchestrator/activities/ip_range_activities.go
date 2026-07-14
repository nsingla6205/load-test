package activities

import (
	"context"
	"fmt"

	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	cvnclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvn"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/activity"
)

// IPRangeActivity handles data LIF ip_range pre-selection for PO pools (CVS preSelectIPRange parity).
type IPRangeActivity struct {
	SE database.Storage
	// NewCVN builds a CVN SDK client for the given account. Injected for tests.
	NewCVN func(ctx context.Context, accountID string) (sdk.CVN, error)
}

// NewIPRangeActivity constructs an IPRangeActivity with production CVN client wiring.
func NewIPRangeActivity() *IPRangeActivity {
	return &IPRangeActivity{NewCVN: cvnclient.NewFromContext}
}

func (a *IPRangeActivity) cvnForAccount(ctx context.Context, accountUUID string) (sdk.CVN, error) {
	newCVN := a.NewCVN
	if newCVN == nil {
		newCVN = cvnclient.NewFromContext
	}
	return newCVN(ctx, accountUUID)
}

// PreSelectDataLifIPRangeInput selects an existing data LIF ip_range or triggers CVN secondary range creation.
type PreSelectDataLifIPRangeInput struct {
	AccountUUID    string
	VendorSubnetID string
	HostUUID       string
	SubnetMaskSize int64
	IPAddressCount int
}

// PreSelectDataLifIPRange returns an ip_range UUID to pass to CreateDataPathV1, or "" when no ranges exist yet.
func (a *IPRangeActivity) PreSelectDataLifIPRange(ctx context.Context, in PreSelectDataLifIPRangeInput) (string, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Pre-selecting data LIF IP range for host %s", in.HostUUID))
	if a.SE == nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("IP range activity storage is not configured")))
	}
	cvnSDK, err := a.cvnForAccount(ctx, in.AccountUUID)
	if err != nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}
	selected, err := common.PreSelectDataLifIPRange(ctx, a.SE, cvnSDK, common.PreSelectDataLifIPRangeParams{
		AccountUUID:    in.AccountUUID,
		VendorSubnetID: in.VendorSubnetID,
		HostUUID:       in.HostUUID,
		SubnetMaskSize: in.SubnetMaskSize,
		IPAddressCount: in.IPAddressCount,
	})
	if err != nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return selected, nil
}
