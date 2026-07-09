package api

import (
	"context"
	"fmt"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// V1ListHosts implements GET /v1/host.
func (h Handler) V1ListHosts(ctx context.Context, _ oasgenserver.V1ListHostsParams) (oasgenserver.V1ListHostsRes, error) {
	hosts, err := h.Orchestrator.ListHosts(ctx)
	if err != nil {
		if errors.IsNotImplementedYetErr(err) {
			return &oasgenserver.V1ListHostsInternalServerError{
				Code:    commonparams.HTTPStatusInternalServerError,
				Message: "list hosts is not implemented yet",
			}, nil
		}
		return &oasgenserver.V1ListHostsInternalServerError{
			Code:    commonparams.HTTPStatusInternalServerError,
			Message: fmt.Sprintf("failed to list hosts: %v", err),
		}, nil
	}

	result := make(oasgenserver.V1ListHostsOKApplicationJSON, 0, len(hosts))
	for _, host := range hosts {
		result = append(result, convertDatamodelHostToV1(host))
	}
	return &result, nil
}

// V1CreateHost creates a new host.
func (h Handler) V1CreateHost(ctx context.Context, req *oasgenserver.HostV1, _ oasgenserver.V1CreateHostParams) (oasgenserver.V1CreateHostRes, error) {
	if req == nil {
		return &oasgenserver.V1CreateHostBadRequest{
			Code:    commonparams.HTTPStatusBadRequest,
			Message: "request body is required",
		}, nil
	}

	orchestratorParams := &commonparams.CreateHostParams{
		Name:     req.Name,
		Username: req.Username,
		Password: log.Secret(req.Password),
	}

	if req.VendorID.IsSet() {
		orchestratorParams.VendorID = &req.VendorID.Value
	}
	if req.MaintenanceMode.IsSet() {
		orchestratorParams.MaintenanceMode = &req.MaintenanceMode.Value
	}
	if req.PublicAccess.IsSet() {
		orchestratorParams.PublicAccess = &req.PublicAccess.Value
	}
	if req.IpAddress.IsSet() {
		ip := req.IpAddress.Value
		orchestratorParams.IPAddress = &ip
	}
	if req.Port.IsSet() {
		port := int(req.Port.Value)
		orchestratorParams.Port = &port
	}
	if req.Protocol != "" {
		protocol := string(req.Protocol)
		orchestratorParams.Protocol = &protocol
	}
	if req.Label.IsSet() {
		label := req.Label.Value
		orchestratorParams.Label = &label
	}
	if req.EfficiencyFactor.IsSet() {
		efficiencyFactor := req.EfficiencyFactor.Value
		orchestratorParams.EfficiencyFactor = &efficiencyFactor
	}

	host, err := h.Orchestrator.CreateOntapHost(ctx, orchestratorParams)
	if err != nil {
		if errors.IsNotImplementedYetErr(err) {
			return &oasgenserver.V1CreateHostInternalServerError{
				Code:    commonparams.HTTPStatusInternalServerError,
				Message: "create host operation is not implemented yet",
			}, nil
		}
		if customErr := asVCPError(err); customErr != nil && customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
			return &oasgenserver.V1CreateHostConflict{
				Code:    commonparams.HTTPStatusConflict,
				Message: customErr.GetDetailMessage(),
			}, nil
		}
		if errors.IsConflictErr(err) {
			return &oasgenserver.V1CreateHostConflict{
				Code:    commonparams.HTTPStatusConflict,
				Message: err.Error(),
			}, nil
		}
		if errors.IsUserInputValidationErr(err) || errors.IsBadRequestErr(err) {
			return &oasgenserver.V1CreateHostBadRequest{
				Code:    commonparams.HTTPStatusBadRequest,
				Message: err.Error(),
			}, nil
		}
		if errors.IsNotFoundErr(err) {
			return &oasgenserver.V1CreateHostNotFound{
				Code:    commonparams.HTTPStatusNotFound,
				Message: err.Error(),
			}, nil
		}
		return &oasgenserver.V1CreateHostInternalServerError{
			Code:    commonparams.HTTPStatusInternalServerError,
			Message: fmt.Sprintf("failed to create host: %v", err),
		}, nil
	}

	response := convertDatamodelHostToV1(host)
	return &response, nil
}

// V1DescribeHost implements GET /v1/host/{uuid}.
func (h Handler) V1DescribeHost(ctx context.Context, params oasgenserver.V1DescribeHostParams) (oasgenserver.V1DescribeHostRes, error) {
	host, err := h.Orchestrator.GetHost(ctx, params.UUID)
	if err != nil {
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError) {
				return &oasgenserver.V1DescribeHostNotFound{
					Message: "Host not found",
					Code:    commonparams.HTTPStatusNotFound,
				}, nil
			}
		}
		if errors.IsNotImplementedYetErr(err) {
			return &oasgenserver.V1DescribeHostInternalServerError{
				Code:    commonparams.HTTPStatusInternalServerError,
				Message: "host get is not implemented yet",
			}, nil
		}
		return &oasgenserver.V1DescribeHostInternalServerError{
			Message: fmt.Sprintf("failed to get host: %v", err),
			Code:    commonparams.HTTPStatusInternalServerError,
		}, nil
	}

	response := convertDatamodelHostToV1(host)
	return &response, nil
}

// V1UpdateHost implements PUT /v1/host/{uuid}.
func (h Handler) V1UpdateHost(ctx context.Context, req *oasgenserver.HostV1, params oasgenserver.V1UpdateHostParams) (oasgenserver.V1UpdateHostRes, error) {
	if req == nil {
		return &oasgenserver.V1UpdateHostBadRequest{
			Code:    commonparams.HTTPStatusBadRequest,
			Message: "request body is required",
		}, nil
	}

	updateParams := &commonparams.UpdateHostParams{
		HostUUID: params.UUID,
		CreateHostParams: commonparams.CreateHostParams{
			Name:     req.Name,
			Username: req.Username,
			Password: log.Secret(req.Password),
		},
	}
	if req.ExternalClusterId.IsSet() {
		updateParams.ExternalUUID = req.ExternalClusterId.Value
	}
	if req.ExternalName.IsSet() {
		updateParams.ExternalName = req.ExternalName.Value
	}
	if req.VendorID.IsSet() {
		updateParams.VendorID = &req.VendorID.Value
	}
	if req.MaintenanceMode.IsSet() {
		updateParams.MaintenanceMode = &req.MaintenanceMode.Value
	}
	if req.PublicAccess.IsSet() {
		updateParams.PublicAccess = &req.PublicAccess.Value
	}
	if req.IpAddress.IsSet() {
		ip := req.IpAddress.Value
		updateParams.IPAddress = &ip
	}
	if req.Port.IsSet() {
		port := int(req.Port.Value)
		updateParams.Port = &port
	}
	if req.Protocol != "" {
		protocol := string(req.Protocol)
		updateParams.Protocol = &protocol
	}
	if req.Label.IsSet() {
		label := req.Label.Value
		updateParams.Label = &label
	}
	if req.EfficiencyFactor.IsSet() {
		efficiencyFactor := req.EfficiencyFactor.Value
		updateParams.EfficiencyFactor = &efficiencyFactor
	}

	host, err := h.Orchestrator.UpdateHost(ctx, updateParams)
	if err != nil {
		if errors.IsNotImplementedYetErr(err) {
			return &oasgenserver.V1UpdateHostInternalServerError{
				Code:    commonparams.HTTPStatusInternalServerError,
				Message: "host update is not implemented yet",
			}, nil
		}
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError) {
				return &oasgenserver.V1UpdateHostNotFound{
					Message: "Host not found",
					Code:    commonparams.HTTPStatusNotFound,
				}, nil
			}
			if customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
				return &oasgenserver.V1UpdateHostConflict{
					Code:    commonparams.HTTPStatusConflict,
					Message: customErr.GetDetailMessage(),
				}, nil
			}
		}
		if errors.IsConflictErr(err) {
			return &oasgenserver.V1UpdateHostConflict{
				Code:    commonparams.HTTPStatusConflict,
				Message: err.Error(),
			}, nil
		}
		if errors.IsUserInputValidationErr(err) || errors.IsBadRequestErr(err) {
			return &oasgenserver.V1UpdateHostBadRequest{
				Code:    commonparams.HTTPStatusBadRequest,
				Message: err.Error(),
			}, nil
		}
		if errors.IsNotFoundErr(err) {
			return &oasgenserver.V1UpdateHostNotFound{
				Code:    commonparams.HTTPStatusNotFound,
				Message: err.Error(),
			}, nil
		}
		return &oasgenserver.V1UpdateHostInternalServerError{
			Code:    commonparams.HTTPStatusInternalServerError,
			Message: fmt.Sprintf("failed to update host: %v", err),
		}, nil
	}

	response := convertDatamodelHostToV1(host)
	return &response, nil
}

// V1DeleteHost implements DELETE /v1/host/{uuid}.
func (h Handler) V1DeleteHost(ctx context.Context, params oasgenserver.V1DeleteHostParams) (oasgenserver.V1DeleteHostRes, error) {
	deleted, err := h.Orchestrator.DeleteHost(ctx, params.UUID)
	if err != nil {
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError) {
				return &oasgenserver.V1DeleteHostNotFound{
					Message: "Host not found",
					Code:    commonparams.HTTPStatusNotFound,
				}, nil
			}
		}
		if errors.IsNotImplementedYetErr(err) {
			return &oasgenserver.V1DeleteHostInternalServerError{
				Code:    commonparams.HTTPStatusInternalServerError,
				Message: "host delete is not implemented yet",
			}, nil
		}
		return &oasgenserver.V1DeleteHostInternalServerError{
			Message: fmt.Sprintf("failed to delete host: %v", err),
			Code:    commonparams.HTTPStatusInternalServerError,
		}, nil
	}

	result := convertDatamodelHostToV1(deleted)
	ok := oasgenserver.V1DeleteHostOK(result)
	return &ok, nil
}

func convertDatamodelHostToV1(host *datamodel.Host) oasgenserver.HostV1 {
	if host == nil {
		return oasgenserver.HostV1{}
	}

	response := oasgenserver.HostV1{
		Name:     host.Name,
		Protocol: oasgenserver.HostV1Protocol(host.Protocol),
	}
	response.UUID = oasgenserver.NewOptString(host.UUID)
	response.CreatedAt = oasgenserver.NewOptDateTime(host.CreatedAt)
	response.UpdatedAt = oasgenserver.NewOptDateTime(host.UpdatedAt)
	response.ExternalClusterId = oasgenserver.NewOptString(host.ExternalUUID)
	response.ExternalName = oasgenserver.NewOptString(host.ExternalName)
	if host.VendorID != nil {
		response.VendorID = oasgenserver.NewOptString(*host.VendorID)
	}
	if host.Port != nil {
		response.Port = oasgenserver.NewOptInt32(int32(*host.Port))
	}
	if host.State != "" {
		response.LifeCycleState = oasgenserver.NewOptHostV1LifeCycleState(oasgenserver.HostV1LifeCycleState(host.State))
	}
	if host.StateDetails != "" {
		response.LifeCycleStateDetails = oasgenserver.NewOptString(host.StateDetails)
	}
	if host.IPAddress != "" {
		response.IpAddress = oasgenserver.NewOptString(host.IPAddress)
	}
	if host.Label != "" {
		response.Label = oasgenserver.NewOptString(host.Label)
	}
	response.Capacity = oasgenserver.NewOptInt64(host.Capacity)
	response.EfficiencyFactor = oasgenserver.NewOptFloat64(host.EfficiencyFactor)
	if host.Attributes != nil {
		response.MaintenanceMode = oasgenserver.NewOptBool(host.Attributes.MaintenanceMode)
		response.PublicAccess = oasgenserver.NewOptBool(host.Attributes.PublicAccess)
		if host.Attributes.Version != "" {
			response.OntapVersion = oasgenserver.NewOptString(host.Attributes.Version)
		}
		response.VersionTuple = oasgenserver.NewOptHostV1VersionTuple(oasgenserver.HostV1VersionTuple{
			Generation: oasgenserver.NewOptInt32(int32(host.Attributes.VersionGeneration)),
			Major:      oasgenserver.NewOptInt32(int32(host.Attributes.VersionMajor)),
			Minor:      oasgenserver.NewOptInt32(int32(host.Attributes.VersionMinor)),
		})
	}
	if host.DeletedAt != nil && host.DeletedAt.Valid {
		response.DeletedAt = oasgenserver.NewOptNilDateTime(host.DeletedAt.Time)
	}
	return response
}
