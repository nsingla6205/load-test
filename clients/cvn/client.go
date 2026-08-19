// Package cvn builds CVN (Cloud Volumes Network) SDK clients for the VCP worker.
// CVNActivity uses NewFromContext for Hardware pool datapath provisioning.
package cvn

import (
	"context"
	"fmt"
	"time"

	goopenapiRuntime "github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"
	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk/cvnapi"
	"github.com/greenqloud/cloud-volumes-network/app/pkg/swaggertransport"
	cvnlog "github.com/greenqloud/cloud-volumes-network/app/utils/log"
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

// NewCVNAPIFromContext builds the raw auto-generated CVN swagger client
// (*cvnapi.CVN) with the same transport, auth, and admin credentials as
// NewFromContext but without the SDK's convenience-method wrapper. Callers use
// this when they need to invoke a raw generated endpoint (e.g. Networks.
// CreateSecondaryIPRangeV1) without waiting for a new SDK release to expose a
// typed helper.
func NewCVNAPIFromContext(ctx context.Context, accountID string) (*cvnapi.CVN, error) {
	if cvnHost == "" {
		util.GetLogger(ctx).Errorf("CVN_HOST is not configured for account %s", accountID)
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCVNHostNotFound, fmt.Errorf("CVN_HOST is not configured")))
	}
	if aak == "" || ask == "" {
		return nil, fmt.Errorf("ADMIN_API_KEY and ADMIN_SECRET_KEY must be configured")
	}

	correlationID := utils.GetCoRelationIDFromContext(ctx)

	trace := cvnlog.WithPlatform(cvnlog.NewCorrelationTrace(correlationID, accountID), platformVCP)

	transport := swaggertransport.New(context.Background(), &swaggertransport.Params{
		Host:              cvnHost,
		BasePath:          cvnapi.DefaultBasePath,
		Trace:             trace,
		Auth:              buildCVNAuthHeaders(accountID, correlationID),
		RetryTimeout:      time.Second * time.Duration(cvnRetryTimeoutSeconds),
		RetryWaitInterval: time.Second * time.Duration(cvnRetryWaitIntervalSeconds),
	})
	return cvnapi.New(transport, strfmt.Default), nil
}

// buildCVNAuthHeaders returns the auth callback that swaggertransport invokes
// on every outbound request. It pins the exact wire contract: admin API +
// secret keys, the account identifier the CVN gateway routes on, the VCP
// platform tag, and the correlation id for cross-service tracing. Extracted
// as a named function so unit tests can exercise the header contract without
// spinning up a full swagger transport / TLS server.
func buildCVNAuthHeaders(accountID, correlationID string) func(req goopenapiRuntime.ClientRequest) error {
	return func(req goopenapiRuntime.ClientRequest) error {
		if err := req.SetHeaderParam("api-key", aak); err != nil {
			return err
		}
		if err := req.SetHeaderParam("secret-key", ask); err != nil {
			return err
		}
		if err := req.SetHeaderParam(cvnlog.AccountIdentifierKey, accountID); err != nil {
			return err
		}
		if err := req.SetHeaderParam(cvnlog.PlatformKey, platformVCP); err != nil {
			return err
		}
		return req.SetHeaderParam("x-correlation-id", correlationID)
	}
}
