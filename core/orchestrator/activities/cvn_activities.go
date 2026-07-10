package activities

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	cvnclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvn"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/activity"
)

// CVNActivity wraps the CVN datapath client for the PO (Performance Offering)
// pool path. It creates the network datapath (border-switch VLAN + service-switch
// VXLAN + IP range).
type CVNActivity struct {
	// NewCVN builds a CVN SDK client for the given account. It is injected so
	// tests can supply a fake; production wiring uses the shared clients/cvn factory.
	NewCVN func(ctx context.Context, accountID string) (sdk.CVN, error)
	// SE reads ip_ranges rows written by CVN/CVI during the datapath pipeline.
	SE database.Storage
}

// NewCVNActivity constructs a CVNActivity with production CVN client wiring.
func NewCVNActivity() *CVNActivity {
	return &CVNActivity{NewCVN: cvnclient.NewFromContext}
}

func (a *CVNActivity) cvnForAccount(ctx context.Context, accountUUID string) (sdk.CVN, error) {
	newCVN := a.NewCVN
	if newCVN == nil {
		newCVN = cvnclient.NewFromContext
	}
	return newCVN(ctx, accountUUID)
}

// CreateDataPath creates (or ensures) the CVN datapath and waits for the
// CVN job to complete, returning the datapath outputs needed for PO pool wiring.
//
// The underlying CVN CreateDataPathV1 call is idempotent, so this activity is
// safe to retry: re-invoking it for an account that already has a data path
// returns the existing path rather than creating a duplicate.
//
// CIDR and gateway are read from the persisted ip_ranges row keyed by the
// IPrangeUUID returned from CreateDataPathV1 (same as SDE CVS after datapath).
func (a *CVNActivity) CreateDataPath(ctx context.Context, params common.DataPathParams) (*common.DataPathResponse, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Creating CVN datapath for pool %s", params.PoolUUID))
	if err := params.Validate(); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("invalid datapath params: %w", err))
	}
	if a.SE == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("CVN activity storage is not configured")))
	}
	localRegion := strings.TrimSpace(env.Region)
	if localRegion == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("LOCAL_REGION is not configured")))
	}
	zone, err := a.SE.GetZoneByRegionName(ctx, localRegion)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	params.ZoneUUID = zone.UUID

	cvnSDK, err := a.cvnForAccount(ctx, params.AccountUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	resp, err := createDataPathViaSDK(cvnSDK, params)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if resp == nil || resp.IpRangeUUID == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("CVN returned an empty datapath response for pool %s", params.PoolUUID)))
	}
	ir, err := a.SE.GetIPRangeByUUID(ctx, resp.IpRangeUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	cidr, gw, err := common.PODataPathFromIPRange(ir)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	resp.IPRange = cidr
	resp.Gateway = gw
	if resp.DatapathID == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("CVN returned an empty datapath response for pool %s", params.PoolUUID)))
	}
	return resp, nil
}

func createDataPathViaSDK(cvnSDK sdk.CVN, params common.DataPathParams) (*common.DataPathResponse, error) {
	remoteAccountID := params.RemoteAccountID
	sdkParams := &sdk.DataPathParams{
		AccountUUID:     params.AccountUUID,
		RemoteAccountID: &remoteAccountID,
		ZoneUUID:        params.ZoneUUID,
		MicrozoneUUID:   params.MicrozoneUUID,
		SubnetMaskSize:  int64(params.SubnetMaskSize),
		VlanBandwidth:   params.VlanBandwidth,
		IsMultiLIF:      params.IsMultiLIF,
		IsFlexGroup:     params.IsFlexGroup,
	}

	sdkResp, err := cvnSDK.CreateDataPathV1(sdkParams)
	if err != nil {
		return nil, err
	}
	if sdkResp == nil {
		return nil, fmt.Errorf("CVN returned nil datapath response")
	}

	return &common.DataPathResponse{
		DatapathID:  sdkResp.IPrangeUUID,
		VlanID:      strconv.Itoa(sdkResp.SouthboundVlanID),
		IpRangeUUID: sdkResp.IPrangeUUID,
	}, nil
}
