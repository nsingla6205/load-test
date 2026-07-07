package activities

import (
	"context"

	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	cvnclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvn"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

// CVNActivity groups Temporal activities that talk to Cloud Volumes Network (CVN).
type CVNActivity struct {
	// NewCVN builds a CVN SDK client for the given account. It is injected so
	// tests can supply a fake; production wiring uses the shared clients/cvn factory.
	NewCVN func(ctx context.Context, accountID string) (sdk.CVN, error)
}

// NewCVNActivity constructs a CVNActivity with production dependencies.
func NewCVNActivity() *CVNActivity {
	return &CVNActivity{NewCVN: cvnclient.NewFromContext}
}

// CreateDataPath ensures a CVN data path exists for the tenant and returns the
// southbound VLAN id and IP-range UUID needed to wire up the ONTAP netport.
//
// The underlying CVN CreateDataPathV1 call is idempotent, so this activity is
// safe to retry: re-invoking it for an account that already has a data path
// returns the existing path rather than creating a duplicate.
func (a *CVNActivity) CreateDataPath(ctx context.Context, params sdk.DataPathParams) (*sdk.DataPathResponse, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, sdk.EnsuringDataPathProgress)

	newCVN := a.NewCVN
	if newCVN == nil {
		newCVN = cvnclient.NewFromContext
	}

	cvn, err := newCVN(ctx, params.AccountUUID)
	if err != nil {
		logger.Errorf("failed to create CVN client for account %s: %v", params.AccountUUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	resp, err := cvn.CreateDataPathV1(&params)
	if err != nil {
		logger.Errorf("CVN CreateDataPathV1 failed for account %s: %v", params.AccountUUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, sdk.EnsuringDataPathProgressFinished)
	logger.Infof("CVN data path ensured for account %s: southbound vlan %d, ip range %s",
		params.AccountUUID, resp.SouthboundVlanID, resp.IPrangeUUID)
	return resp, nil
}
