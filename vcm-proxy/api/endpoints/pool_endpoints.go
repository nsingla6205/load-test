package api

import (
	"context"
	"time"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	weutil "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// PoolCreateV1 validates the create-pool request and provisions the ONTAP pool
// via the VCM orchestrator, which owns the durable job lifecycle. Returns the
// long-running Operation (AIP-151) with done=false; clients poll
// GET /v1/operations/{operationId} using the operation ID (== requestId).
func (h Handler) PoolCreateV1(ctx context.Context, req *vcmserver.PoolRequestV1) (vcmserver.PoolCreateV1Res, error) {
	logger := weutil.GetLogger(ctx)

	if req == nil || req.RequestId == "" || req.Pool.DeploymentId == "" {
		return &vcmserver.PoolCreateV1BadRequest{
			Code:    400,
			Message: "requestId and pool.deploymentId are required",
		}, nil
	}
	if req.Deadline.IsZero() || !req.Deadline.UTC().After(time.Now().UTC()) {
		return &vcmserver.PoolCreateV1BadRequest{
			Code:    400,
			Message: "deadline must be a future UTC timestamp",
		}, nil
	}

	logger.InfoContext(ctx, "received create-pool request",
		"deploymentId", req.Pool.DeploymentId, "requestId", req.RequestId, "deadline", req.Deadline)

	_, operationID, err := h.Orchestrator.CreatePool(ctx, toCreatePoolParams(req))
	if err != nil {
		// A duplicate requestId (an operation already exists for this
		// idempotency key) is a client conflict, not a retriable server error:
		// reject it with a clear, non-retriable message instead of returning a
		// 202 the caller would poll against a stale operation.
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) && customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
			message := customErr.Error()
			if customErr.OriginalErr != nil {
				message = customErr.OriginalErr.Error()
			}
			logger.WarnContext(ctx, "create-pool duplicate request rejected",
				"requestId", req.RequestId, "deploymentId", req.Pool.DeploymentId, "error", err)
			return &vcmserver.PoolCreateV1BadRequest{
				Code:      400,
				Message:   message,
				Retriable: vcmserver.NewOptBool(false),
			}, nil
		}
		logger.ErrorContext(ctx, "create-pool orchestrator error", "error", err)
		return &vcmserver.PoolCreateV1InternalServerError{
			Code:      500,
			Message:   "Internal error occurred",
			Retriable: vcmserver.NewOptBool(true),
		}, nil
	}

	// Prefer requestId as the operation ID; fall back to the dispatcher ID.
	if operationID == "" {
		operationID = req.RequestId
	}

	return &vcmserver.OperationV1{
		Name: "/v1/operations/" + operationID,
		Done: false,
	}, nil
}

// toCreatePoolParams maps the VCM create-pool request onto the shared
// CreatePoolParams (VCM-specific inputs on the nested VCM field).
func toCreatePoolParams(req *vcmserver.PoolRequestV1) *commonparams.CreatePoolParams {
	pool := &req.Pool
	vcm := &commonparams.VCMCreatePoolParams{
		DeploymentID:         pool.DeploymentId,
		RequestID:            req.RequestId,
		Deadline:             req.Deadline,
		RegionIdentifier:     pool.RegionIdentifier,
		PrimaryZone:          pool.PrimaryZone,
		SecondaryZone:        pool.SecondaryZone,
		MediatorZone:         pool.MediatorZone,
		ServiceTenantProject: pool.ServiceTenantProject,
		SNHostProject:        pool.SNHostProject,
		ServiceAccountEmail:  pool.ServiceAccountEmail,
		AutoTierBucketName:   pool.AutoTierBucketName,
		ProjectID:            pool.ProjectId.Or(""),
		Mode:                 string(pool.Mode.Or(vcmserver.PoolV1ModeDEFAULT)),
		QosType:              pool.QosType.Or(""),
		ThroughputMibps:      pool.ThroughputMibps.Or(0),
		Iops:                 pool.Iops.Or(0),
		SizeInBytes:          int64(pool.SizeInBytes.Or(0)),
		CaURI:                pool.CaUri.Or(""),
	}

	if nm, ok := pool.NetConfig.Nodemgmt.Get(); ok {
		vcm.NodeMgmt.VPC = nm.Vpc
		vcm.NodeMgmt.Subnet = nm.Subnet
	}
	if data, ok := pool.NetConfig.Data.Get(); ok {
		vcm.Data = &commonparams.VCMNetConfig{VPC: data.Vpc, Subnet: data.Subnet}
	}

	for _, c := range pool.OntapCredentials {
		vcm.OntapCredentials = append(vcm.OntapCredentials, commonparams.VCMOntapCredential{
			Type:          string(c.Type),
			SecretID:      c.SecretId.Or(""),
			CertificateID: c.CertificateId.Or(""),
		})
	}

	return &commonparams.CreatePoolParams{VCM: vcm}
}
