package endpoints

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/api/ontap-proxy-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/handlers"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/middleware"
	proxyutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var jsonMarshal = json.Marshal

// V1GetQoSPolicy implements GET /api/storage/qos/policies/{uuid}.
// Looks up the policy via GetQoSPolicy (typed ONTAP REST client) and returns the ogen QoSPolicy.
func (h Handler) V1GetQoSPolicy(
	ctx context.Context,
	params oasgenserver.V1GetQoSPolicyParams,
) (oasgenserver.V1GetQoSPolicyRes, error) {
	logger := util.GetLogger(ctx)
	logger.InfoContext(ctx, "Processing get QoS policy request",
		"projectNumber", params.ProjectNumber,
		"poolId", params.PoolId.String(),
		"uuid", params.UUID.String(),
	)

	ctx, err := setupCredentialsForHandler(
		ctx,
		params.ProjectNumber,
		params.PoolId.String(),
		middleware.CredentialTypeExpertModeUser,
	)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to setup credentials", "error", err)
		return &oasgenserver.V1GetQoSPolicyUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: fmt.Sprintf("authentication error: %s", err.Error()),
		}, nil
	}
	if err := ensureCertificateOrPassword(ctx); err != nil {
		logger.ErrorContext(ctx, "Failed to setup certificate/password", "error", err)
		return &oasgenserver.V1GetQoSPolicyUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: fmt.Sprintf("authentication error: %s", err.Error()),
		}, nil
	}

	ontapClient, err := newOntapClientFromContext(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to get ONTAP client", "error", err)
		if res, ok := v1GetQoSPolicyResFromProxyHTTP(ctx, err); ok {
			return res, nil
		}
		return &oasgenserver.V1GetQoSPolicyInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("failed to connect to ONTAP: %s", err.Error()),
		}, nil
	}

	policy, err := ontapClient.GetQoSPolicy(ctx, params.UUID.String())
	if err != nil {
		logger.ErrorContext(ctx, "QoS policy lookup failed", "uuid", params.UUID.String(), "error", err)
		if res, ok := v1GetQoSPolicyResFromProxyHTTP(ctx, err); ok {
			return res, nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return &oasgenserver.V1GetQoSPolicyNotFound{
				Code:    http.StatusNotFound,
				Message: fmt.Sprintf("QoS policy not found: %s", params.UUID.String()),
			}, nil
		}
		return &oasgenserver.V1GetQoSPolicyInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("ONTAP operation failed: %s", err.Error()),
		}, nil
	}

	return qosPolicyInfoToOAS(policy), nil
}

// V1UpdateQoSPolicy implements PATCH /api/storage/qos/policies/{uuid}.
func (h Handler) V1UpdateQoSPolicy(
	ctx context.Context,
	req *oasgenserver.QoSPolicyUpdate,
	params oasgenserver.V1UpdateQoSPolicyParams,
) (oasgenserver.V1UpdateQoSPolicyRes, error) {
	ctx, ontapClient, policy, status, message := prepareQoSPolicyMutation(
		ctx,
		params.ProjectNumber,
		params.PoolId.String(),
		params.UUID.String(),
	)
	if status != 0 {
		return updateQoSPolicyError(status, message), nil
	}
	if fixed, ok := req.Fixed.Get(); ok &&
		(fixed.MinThroughputIops.IsSet() || fixed.MinThroughputMbps.IsSet()) {
		return updateQoSPolicyError(http.StatusBadRequest, "fixed QoS minimum throughput is not supported"), nil
	}
	if proxyutils.IsReservedQoSPolicyName(policy.Name, policy.SVM.Name) {
		return updateQoSPolicyError(http.StatusForbidden, fmt.Sprintf("QoS policy %q is reserved (vserver-wide) and cannot be modified", policy.Name)), nil
	}
	if newName, ok := req.Name.Get(); ok && proxyutils.IsReservedQoSPolicyName(newName, policy.SVM.Name) {
		return updateQoSPolicyError(http.StatusForbidden, fmt.Sprintf("renaming QoS policy to %q is not allowed; it is the reserved vserver-wide name", newName)), nil
	}

	body, err := jsonMarshal(req)
	if err != nil {
		return updateQoSPolicyError(http.StatusInternalServerError, fmt.Sprintf("failed to encode QoS policy update: %s", err.Error())), nil
	}
	responseBody, responseStatus, err := ontapClient.ExecuteAPI(
		ctx,
		http.MethodPatch,
		fmt.Sprintf("/api/storage/qos/policies/%s", params.UUID.String()),
		body,
	)
	if err != nil {
		return updateQoSPolicyError(http.StatusInternalServerError, fmt.Sprintf("ONTAP operation failed: %s", err.Error())), nil
	}
	switch responseStatus {
	case http.StatusOK:
		return &oasgenserver.V1UpdateQoSPolicyOK{}, nil
	case http.StatusAccepted:
		return &oasgenserver.V1UpdateQoSPolicyAccepted{}, nil
	default:
		return updateQoSPolicyError(responseStatus, ontapAPIErrorMessage(responseStatus, responseBody)), nil
	}
}

// V1DeleteQoSPolicy implements DELETE /api/storage/qos/policies/{uuid}.
func (h Handler) V1DeleteQoSPolicy(
	ctx context.Context,
	params oasgenserver.V1DeleteQoSPolicyParams,
) (oasgenserver.V1DeleteQoSPolicyRes, error) {
	ctx, ontapClient, policy, status, message := prepareQoSPolicyMutation(
		ctx,
		params.ProjectNumber,
		params.PoolId.String(),
		params.UUID.String(),
	)
	if status != 0 {
		return deleteQoSPolicyError(status, message), nil
	}
	if proxyutils.IsReservedQoSPolicyName(policy.Name, policy.SVM.Name) {
		return deleteQoSPolicyError(http.StatusForbidden, fmt.Sprintf("QoS policy %q is reserved (vserver-wide) and cannot be deleted", policy.Name)), nil
	}

	responseBody, responseStatus, err := ontapClient.ExecuteAPI(
		ctx,
		http.MethodDelete,
		fmt.Sprintf("/api/storage/qos/policies/%s", params.UUID.String()),
		nil,
	)
	if err != nil {
		return deleteQoSPolicyError(http.StatusInternalServerError, fmt.Sprintf("ONTAP operation failed: %s", err.Error())), nil
	}
	switch responseStatus {
	case http.StatusOK:
		return &oasgenserver.V1DeleteQoSPolicyOK{}, nil
	case http.StatusAccepted:
		return &oasgenserver.V1DeleteQoSPolicyAccepted{}, nil
	case http.StatusNoContent:
		return &oasgenserver.V1DeleteQoSPolicyNoContent{}, nil
	default:
		return deleteQoSPolicyError(responseStatus, ontapAPIErrorMessage(responseStatus, responseBody)), nil
	}
}

func prepareQoSPolicyMutation(
	ctx context.Context,
	projectNumber string,
	poolID string,
	policyUUID string,
) (context.Context, handlers.OntapClient, *handlers.QoSPolicyInfo, int, string) {
	ctx, err := setupCredentialsForHandler(
		ctx,
		projectNumber,
		poolID,
		middleware.CredentialTypeExpertModeUser,
	)
	if err != nil {
		return ctx, nil, nil, http.StatusUnauthorized, fmt.Sprintf("authentication error: %s", err.Error())
	}
	if err := ensureCertificateOrPassword(ctx); err != nil {
		return ctx, nil, nil, http.StatusUnauthorized, fmt.Sprintf("authentication error: %s", err.Error())
	}

	ontapClient, err := newOntapClientFromContext(ctx)
	if err != nil {
		if proxyErr, ok := asProxyHTTPErrorWithReconcile(ctx, err); ok {
			return ctx, nil, nil, proxyErr.Status, proxyErr.Message
		}
		return ctx, nil, nil, http.StatusInternalServerError, fmt.Sprintf("failed to connect to ONTAP: %s", err.Error())
	}
	policy, err := ontapClient.GetQoSPolicy(ctx, policyUUID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return ctx, nil, nil, http.StatusNotFound, fmt.Sprintf("QoS policy not found: %s", policyUUID)
		}
		return ctx, nil, nil, http.StatusInternalServerError, fmt.Sprintf("QoS policy lookup failed: %s", err.Error())
	}
	return ctx, ontapClient, policy, 0, ""
}

func ontapAPIErrorMessage(status int, body []byte) string {
	var response handlers.OntapErrorResponse
	if err := json.Unmarshal(body, &response); err == nil && response.Error != nil && response.Error.Message != "" {
		return response.Error.Message
	}
	if message := strings.TrimSpace(string(body)); message != "" {
		return message
	}
	return http.StatusText(status)
}

func updateQoSPolicyError(status int, message string) oasgenserver.V1UpdateQoSPolicyRes {
	switch status {
	case http.StatusUnauthorized:
		return &oasgenserver.V1UpdateQoSPolicyUnauthorized{Code: status, Message: message}
	case http.StatusForbidden:
		return &oasgenserver.V1UpdateQoSPolicyForbidden{Code: status, Message: message}
	case http.StatusNotFound:
		return &oasgenserver.V1UpdateQoSPolicyNotFound{Code: status, Message: message}
	case http.StatusBadRequest:
		return &oasgenserver.V1UpdateQoSPolicyBadRequest{Code: status, Message: message}
	default:
		if status >= 400 && status < 500 {
			return &oasgenserver.V1UpdateQoSPolicyBadRequest{Code: status, Message: message}
		}
		if status < 400 {
			status = http.StatusInternalServerError
		}
		return &oasgenserver.V1UpdateQoSPolicyInternalServerError{Code: status, Message: message}
	}
}

func deleteQoSPolicyError(status int, message string) oasgenserver.V1DeleteQoSPolicyRes {
	switch status {
	case http.StatusUnauthorized:
		return &oasgenserver.V1DeleteQoSPolicyUnauthorized{Code: status, Message: message}
	case http.StatusForbidden:
		return &oasgenserver.V1DeleteQoSPolicyForbidden{Code: status, Message: message}
	case http.StatusNotFound:
		return &oasgenserver.V1DeleteQoSPolicyNotFound{Code: status, Message: message}
	case http.StatusBadRequest:
		return &oasgenserver.V1DeleteQoSPolicyBadRequest{Code: status, Message: message}
	default:
		if status >= 400 && status < 500 {
			return &oasgenserver.V1DeleteQoSPolicyBadRequest{Code: status, Message: message}
		}
		if status < 400 {
			status = http.StatusInternalServerError
		}
		return &oasgenserver.V1DeleteQoSPolicyInternalServerError{Code: status, Message: message}
	}
}

func qosPolicyInfoToOAS(policy *handlers.QoSPolicyInfo) *oasgenserver.QoSPolicy {
	out := &oasgenserver.QoSPolicy{}
	if policy == nil {
		return out
	}
	if policy.UUID != "" {
		if id, err := uuid.Parse(policy.UUID); err == nil {
			out.UUID = oasgenserver.NewOptUUID(id)
		}
	}
	if policy.Name != "" {
		out.Name = oasgenserver.NewOptString(policy.Name)
	}
	if policy.Scope != "" {
		out.Scope = oasgenserver.NewOptString(policy.Scope)
	}
	if policy.PolicyClass != "" {
		out.PolicyClass = oasgenserver.NewOptString(policy.PolicyClass)
	}
	if policy.ObjectCount != 0 {
		out.ObjectCount = oasgenserver.NewOptInt(policy.ObjectCount)
	}
	if policy.Pgid != 0 {
		out.Pgid = oasgenserver.NewOptInt(policy.Pgid)
	}
	if policy.SVM.Name != "" || policy.SVM.UUID != "" {
		svm := oasgenserver.SvmRef{}
		if policy.SVM.Name != "" {
			svm.Name = oasgenserver.NewOptString(policy.SVM.Name)
		}
		if policy.SVM.UUID != "" {
			svm.UUID = oasgenserver.NewOptString(policy.SVM.UUID)
		}
		out.Svm = oasgenserver.NewOptSvmRef(svm)
	}
	if policy.Fixed != nil {
		fixed := oasgenserver.QoSPolicyFixed{}
		if policy.Fixed.MaxThroughputIops != nil {
			fixed.MaxThroughputIops = oasgenserver.NewOptInt(*policy.Fixed.MaxThroughputIops)
		}
		if policy.Fixed.MaxThroughputMbps != nil {
			fixed.MaxThroughputMbps = oasgenserver.NewOptInt(*policy.Fixed.MaxThroughputMbps)
		}
		if policy.Fixed.MinThroughputIops != nil {
			fixed.MinThroughputIops = oasgenserver.NewOptInt(*policy.Fixed.MinThroughputIops)
		}
		if policy.Fixed.MinThroughputMbps != nil {
			fixed.MinThroughputMbps = oasgenserver.NewOptInt(*policy.Fixed.MinThroughputMbps)
		}
		if policy.Fixed.CapacityShared != nil {
			fixed.CapacityShared = oasgenserver.NewOptBool(*policy.Fixed.CapacityShared)
		}
		out.Fixed = oasgenserver.NewOptQoSPolicyFixed(fixed)
	}
	if policy.Adaptive != nil {
		adaptive := oasgenserver.QoSPolicyAdaptive{}
		if policy.Adaptive.ExpectedIops != nil {
			adaptive.ExpectedIops = oasgenserver.NewOptInt(*policy.Adaptive.ExpectedIops)
		}
		if policy.Adaptive.PeakIops != nil {
			adaptive.PeakIops = oasgenserver.NewOptInt(*policy.Adaptive.PeakIops)
		}
		if policy.Adaptive.AbsoluteMinIops != nil {
			adaptive.AbsoluteMinIops = oasgenserver.NewOptInt(*policy.Adaptive.AbsoluteMinIops)
		}
		if policy.Adaptive.ExpectedIopsAllocation != "" {
			adaptive.ExpectedIopsAllocation = oasgenserver.NewOptString(policy.Adaptive.ExpectedIopsAllocation)
		}
		if policy.Adaptive.PeakIopsAllocation != "" {
			adaptive.PeakIopsAllocation = oasgenserver.NewOptString(policy.Adaptive.PeakIopsAllocation)
		}
		if policy.Adaptive.BlockSize != "" {
			adaptive.BlockSize = oasgenserver.NewOptString(policy.Adaptive.BlockSize)
		}
		out.Adaptive = oasgenserver.NewOptQoSPolicyAdaptive(adaptive)
	}
	return out
}
