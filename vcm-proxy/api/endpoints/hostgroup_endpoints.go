package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// V1CreateIgroup validates the request and starts igroup creation.
func (h Handler) V1CreateIgroup(ctx context.Context, req *vcmserver.IgroupV1, queryParams vcmserver.V1CreateIgroupParams) (vcmserver.V1CreateIgroupRes, error) {
	logger := util.GetLogger(ctx)
	requestID, deadline, err := parseRequestIDAndTimeout(queryParams.RequestId, queryParams.Timeout)
	if err != nil {
		return &vcmserver.V1CreateIgroupBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	if err := validateCreateIgroupParams(req); err != nil {
		return &vcmserver.V1CreateIgroupBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	name := strings.TrimSpace(req.Name)
	_, err = h.Orchestrator.CreateHostGroup(ctx, &commonparams.CreateHostGroupParams{
		Name:        name,
		Hosts:       utils.DeduplicateSlice(req.Hosts),
		OSType:      string(req.OsType),
		OperationID: requestID,
		Deadline:    deadline,
	})
	if err != nil {
		logger.ErrorContext(ctx, "Igroup create failed", "name", name, "error", err)
		return &vcmserver.V1CreateIgroupInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	return &vcmserver.OperationV1{
		Name: "/v1/operations/" + requestID,
		Done: false,
	}, nil
}

// V1UpdateIgroup validates the request and starts igroup update.
func (h Handler) V1UpdateIgroup(ctx context.Context, req *vcmserver.IgroupUpdateV1, queryParams vcmserver.V1UpdateIgroupParams) (vcmserver.V1UpdateIgroupRes, error) {
	logger := util.GetLogger(ctx)
	requestID, deadline, err := parseRequestIDAndTimeout(queryParams.RequestId, queryParams.Timeout)
	if err != nil {
		return &vcmserver.V1UpdateIgroupBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	name, err := validateUpdateIgroupParams(queryParams.Name, req)
	if err != nil {
		return &vcmserver.V1UpdateIgroupBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	hosts := utils.DeduplicateSlice(req.Hosts)
	_, _, err = h.Orchestrator.UpdateHostGroup(ctx, &commonparams.UpdateHostGroupParams{
		Name:        name,
		Hosts:       &hosts,
		OperationID: requestID,
		Deadline:    deadline,
	})
	if err != nil {
		logger.ErrorContext(ctx, "Igroup update failed", "name", name, "error", err)
		return &vcmserver.V1UpdateIgroupInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	return &vcmserver.OperationV1{
		Name: "/v1/operations/" + requestID,
		Done: false,
	}, nil
}

// V1DeleteIgroup validates the request and starts igroup deletion.
func (h Handler) V1DeleteIgroup(ctx context.Context, queryParams vcmserver.V1DeleteIgroupParams) (vcmserver.V1DeleteIgroupRes, error) {
	logger := util.GetLogger(ctx)
	requestID, deadline, err := parseRequestIDAndTimeout(queryParams.RequestId, queryParams.Timeout)
	if err != nil {
		return &vcmserver.V1DeleteIgroupBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	name := strings.TrimSpace(queryParams.Name)
	if name == "" {
		return &vcmserver.V1DeleteIgroupBadRequest{
			Code:    400,
			Message: "name is required",
		}, nil
	}

	_, err = h.Orchestrator.DeleteHostGroup(ctx, &commonparams.DeleteHostGroupParams{
		Name:        name,
		OperationID: requestID,
		Deadline:    deadline,
	})
	if err != nil {
		logger.ErrorContext(ctx, "Igroup delete failed", "name", name, "error", err)
		return &vcmserver.V1DeleteIgroupInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	return &vcmserver.OperationV1{
		Name: "/v1/operations/" + requestID,
		Done: false,
	}, nil
}

// V1DescribeIgroup validates the request and returns igroup details.
func (h Handler) V1DescribeIgroup(ctx context.Context, params vcmserver.V1DescribeIgroupParams) (vcmserver.V1DescribeIgroupRes, error) {
	logger := util.GetLogger(ctx)
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return &vcmserver.V1DescribeIgroupBadRequest{
			Code:    400,
			Message: "name is required",
		}, nil
	}

	igroup, err := h.Orchestrator.GetHostGroup(ctx, name, "")
	if err != nil {
		if customerrors.IsNotFoundErrForObjectTypeInChain(err, "HostGroup") {
			return &vcmserver.V1DescribeIgroupNotFound{
				Code:    404,
				Message: "igroup not found",
			}, nil
		}
		logger.ErrorContext(ctx, "Igroup describe failed", "name", name, "error", err)
		return &vcmserver.V1DescribeIgroupInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	return convertToIgroupV1(igroup), nil
}

// V1ListIgroups lists igroups from ONTAP.
func (h Handler) V1ListIgroups(ctx context.Context, _ vcmserver.V1ListIgroupsParams) (vcmserver.V1ListIgroupsRes, error) {
	logger := util.GetLogger(ctx)

	// Here, account and UUID filters are unused; list all igroups on the deployment SVM.
	igroups, err := h.Orchestrator.GetMultipleHostGroups(ctx, "", nil)
	if err != nil {
		logger.ErrorContext(ctx, "Igroup list failed", "error", err)
		return &vcmserver.V1ListIgroupsInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	return &vcmserver.V1ListIgroupsOK{
		Igroups: convertHostGroupsToIgroupV1(igroups),
	}, nil
}

func convertToIgroupV1(igroup *models.HostGroup) *vcmserver.IgroupV1 {
	if igroup == nil {
		return nil
	}
	return &vcmserver.IgroupV1{
		Name:   igroup.Name,
		OsType: mapCanonicalOSTypeToV1(igroup.OSType),
		Hosts:  igroup.Hosts,
	}
}

func convertHostGroupsToIgroupV1(igroups []*models.HostGroup) []vcmserver.IgroupV1 {
	out := make([]vcmserver.IgroupV1, 0, len(igroups))
	for _, igroup := range igroups {
		if converted := convertToIgroupV1(igroup); converted != nil {
			out = append(out, *converted)
		}
	}
	return out
}

func mapCanonicalOSTypeToV1(osType string) vcmserver.IgroupV1OsType {
	switch utils.MapONTAPOSTypeToAPIEnum(osType) {
	case utils.APIOSTypeLinux:
		return vcmserver.IgroupV1OsTypeLINUX
	case utils.APIOSTypeWindows:
		return vcmserver.IgroupV1OsTypeWINDOWS
	case utils.APIOSTypeESXI:
		return vcmserver.IgroupV1OsTypeESXI
	default:
		return vcmserver.IgroupV1OsTypeOSTYPEUNSPECIFIED
	}
}

func validateCreateIgroupParams(req *vcmserver.IgroupV1) error {
	if req == nil {
		return fmt.Errorf("request body is required")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if req.OsType == vcmserver.IgroupV1OsTypeOSTYPEUNSPECIFIED || req.OsType == "" {
		return fmt.Errorf("igroup %q: osType must be set", name)
	}
	if len(req.Hosts) == 0 {
		return fmt.Errorf("igroup %q: hosts must be non-empty", name)
	}
	return validateIgroupHostsCount(name, req.Hosts)
}

func validateUpdateIgroupParams(name string, req *vcmserver.IgroupUpdateV1) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if req == nil || len(req.Hosts) == 0 {
		return "", fmt.Errorf("hosts must be non-empty")
	}
	if err := validateIgroupHostsCount(name, req.Hosts); err != nil {
		return "", err
	}
	return name, nil
}

func validateIgroupHostsCount(igroupName string, hosts []string) error {
	maxHosts := env.GetInt("MAX_HOSTS_PER_HOSTGROUP", 128)
	if len(hosts) > maxHosts {
		return fmt.Errorf("igroup %q: cannot have more than %d hosts", igroupName, maxHosts)
	}
	return nil
}
