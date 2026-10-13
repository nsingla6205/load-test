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

	name, hosts, err := validateCreateIgroupParams(req)
	if err != nil {
		return &vcmserver.V1CreateIgroupBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	_, err = h.Orchestrator.CreateHostGroup(ctx, &commonparams.CreateHostGroupParams{
		Name:        name,
		Hosts:       hosts,
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

	igroupUUID := strings.TrimSpace(queryParams.UUID)
	if igroupUUID == "" {
		return &vcmserver.V1UpdateIgroupBadRequest{
			Code:    400,
			Message: "uuid is required",
		}, nil
	}

	maskedReq, err := applyIgroupUpdateMask(req, queryParams.UpdateMask)
	if err != nil {
		return &vcmserver.V1UpdateIgroupBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	hosts, err := prepareIgroupHosts(igroupUUID, maskedReq.Hosts)
	if err != nil {
		return &vcmserver.V1UpdateIgroupBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	_, _, err = h.Orchestrator.UpdateHostGroup(ctx, &commonparams.UpdateHostGroupParams{
		HostGroupUUID: igroupUUID,
		Hosts:         &hosts,
		OperationID:   requestID,
		Deadline:      deadline,
	})
	if err != nil {
		logger.ErrorContext(ctx, "Igroup update failed", "uuid", igroupUUID, "error", err)
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

	igroupUUID := strings.TrimSpace(queryParams.UUID)
	if igroupUUID == "" {
		return &vcmserver.V1DeleteIgroupBadRequest{
			Code:    400,
			Message: "uuid is required",
		}, nil
	}

	_, err = h.Orchestrator.DeleteHostGroup(ctx, &commonparams.DeleteHostGroupParams{
		Name:        igroupUUID,
		OperationID: requestID,
		Deadline:    deadline,
	})
	if err != nil {
		logger.ErrorContext(ctx, "Igroup delete failed", "uuid", igroupUUID, "error", err)
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
	igroupUUID := strings.TrimSpace(params.UUID)
	if igroupUUID == "" {
		return &vcmserver.V1DescribeIgroupBadRequest{
			Code:    400,
			Message: "uuid is required",
		}, nil
	}

	igroup, err := h.Orchestrator.GetHostGroup(ctx, igroupUUID, "")
	if err != nil {
		if customerrors.IsNotFoundErrForObjectTypeInChain(err, "HostGroup") {
			return &vcmserver.V1DescribeIgroupNotFound{
				Code:    404,
				Message: "igroup not found",
			}, nil
		}
		logger.ErrorContext(ctx, "Igroup describe failed", "uuid", igroupUUID, "error", err)
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
		UUID:   vcmserver.NewOptString(igroup.UUID),
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
	mapped := vcmserver.IgroupV1OsType(utils.MapONTAPOSTypeToAPIEnum(osType))
	switch mapped {
	case vcmserver.IgroupV1OsTypeLINUX, vcmserver.IgroupV1OsTypeWINDOWS, vcmserver.IgroupV1OsTypeESXI:
		return mapped
	default:
		return vcmserver.IgroupV1OsTypeOSTYPEUNSPECIFIED
	}
}

func validateCreateIgroupParams(req *vcmserver.IgroupV1) (string, []string, error) {
	if req == nil {
		return "", nil, fmt.Errorf("request body is required")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return "", nil, fmt.Errorf("name is required")
	}
	if req.OsType == vcmserver.IgroupV1OsTypeOSTYPEUNSPECIFIED || req.OsType == "" {
		return "", nil, fmt.Errorf("igroup %q: osType must be set", name)
	}
	hosts, err := prepareIgroupHosts(name, req.Hosts)
	if err != nil {
		return "", nil, err
	}
	return name, hosts, nil
}

// prepareIgroupHosts trims host strings, rejects blanks, removes duplicates, then applies the host limit.
func prepareIgroupHosts(igroupName string, hosts []string) ([]string, error) {
	if len(hosts) == 0 {
		return nil, fmt.Errorf("igroup %q: hosts must be non-empty", igroupName)
	}

	normalized := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			return nil, fmt.Errorf("igroup %q: hosts must not contain empty values", igroupName)
		}
		normalized = append(normalized, host)
	}
	normalized = utils.DeduplicateSlice(normalized)
	maxHosts := env.GetInt("MAX_HOSTS_PER_HOSTGROUP", 128)
	if len(normalized) > maxHosts {
		return nil, fmt.Errorf("igroup %q: cannot have more than %d hosts", igroupName, maxHosts)
	}
	return normalized, nil
}

// applyIgroupUpdateMask copies hosts from the body when updateMask is exactly "hosts".
func applyIgroupUpdateMask(req *vcmserver.IgroupUpdateV1, updateMask string) (*vcmserver.IgroupUpdateV1, error) {
	fields := make([]string, 0, 1)
	for _, part := range strings.Split(updateMask, ",") {
		field := strings.TrimSpace(part)
		if field == "" {
			continue
		}
		fields = append(fields, field)
	}
	if len(fields) == 0 {
		return nil, customerrors.NewUserInputValidationErr("updateMask must include at least one field")
	}
	if len(fields) != 1 || fields[0] != "hosts" {
		return nil, customerrors.NewUserInputValidationErr("updateMask must include only hosts")
	}
	if req == nil || len(req.Hosts) == 0 {
		return nil, missingUpdateMaskField("hosts")
	}
	return &vcmserver.IgroupUpdateV1{Hosts: req.Hosts}, nil
}
