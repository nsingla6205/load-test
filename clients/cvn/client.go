// Package cvn builds CVN (Cloud Volumes Network) SDK clients for the VCP worker.
// CVNActivity uses NewFromContext for Hardware pool datapath provisioning.
package cvn

import (
	"context"
	"fmt"

	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const platformVCP = "vcp"

var (
	cvnHost                     = env.GetString("CVN_HOST", "")
	aak                         = env.GetString("ADMIN_API_KEY", "")
	ask                         = env.GetString("ADMIN_SECRET_KEY", "")
	cvnBorderPollMinutes        = env.GetInt("CVN_BORDER_POLL_MINUTES", 60)
	cvnServicePollMinutes       = env.GetInt("CVN_SERVICE_POLL_MINUTES", 10)
	cvnNetworkPollMinutes       = env.GetInt("CVN_NETWORK_POLL_MINUTES", 70)
	cvnPollIntervalSeconds      = env.GetInt("CVN_POLL_INTERVAL_SECONDS", 15)
	cvnRetryTimeoutSeconds      = env.GetInt("CVN_RETRY_TIMEOUT_SECONDS", 180)
	cvnRetryWaitIntervalSeconds = env.GetInt("CVN_RETRY_WAIT_INTERVAL_SECONDS", 3)
	cvnTimeoutSeconds           = env.GetInt("CVN_TIMEOUT_SECONDS", 10)
	cvnNew                      = sdk.New
)

// NewFromContext builds a CVN SDK client using the same admin credentials as CVS.
func NewFromContext(ctx context.Context, accountID string) (sdk.CVN, error) {
	if cvnHost == "" {
		util.GetLogger(ctx).Errorf("CVN_HOST is not configured for account %s", accountID)
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCVNHostNotFound, fmt.Errorf("CVN_HOST is not configured")))
	}
	if aak == "" || ask == "" {
		return nil, fmt.Errorf("ADMIN_API_KEY and ADMIN_SECRET_KEY must be configured")
	}

	correlationID := utils.GetCoRelationIDFromContext(ctx)

	return cvnNew(&sdk.Config{
		Host:                             cvnHost,
		AAK:                              aak,
		ASK:                              ask,
		CorrelationID:                    correlationID,
		AccountID:                        accountID,
		Platform:                         platformVCP,
		BorderNetworkPollTimeoutMinutes:  cvnBorderPollMinutes,
		ServiceNetworkPollTimeoutMinutes: cvnServicePollMinutes,
		NetworkPollTimeoutMinutes:        cvnNetworkPollMinutes,
		PollIntervalSeconds:              cvnPollIntervalSeconds,
		RetryTimeoutSeconds:              cvnRetryTimeoutSeconds,
		RetryWaitIntervalSeconds:         cvnRetryWaitIntervalSeconds,
		TimeoutSeconds:                   cvnTimeoutSeconds,
	}), nil
}
