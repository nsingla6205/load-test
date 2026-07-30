package endpoints

import (
	"context"
	"errors"
	"net/http"

	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/api/ontap-proxy-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/middleware"
)

func asProxyHTTPError(err error) (*middleware.ProxyHTTPError, bool) {
	var pe *middleware.ProxyHTTPError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

func asProxyHTTPErrorWithReconcile(ctx context.Context, err error) (*middleware.ProxyHTTPError, bool) {
	err = middleware.TryReconcileHostLookupErrorIfApplicable(ctx, err)
	return asProxyHTTPError(err)
}

func snaplockFileDeleteResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.SnaplockFileDeleteRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.SnaplockFileDeleteUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.SnaplockFileDeleteForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.SnaplockFileDeleteNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.SnaplockFileDeleteBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.SnaplockFileDeleteBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.SnaplockFileDeleteInternalServerError{Code: c, Message: m}, true
	}
}

func v1PrivateCliResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1PrivateCliRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1PrivateCliUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1PrivateCliForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1PrivateCliNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1PrivateCliBadRequest{Code: c, Message: m}, true
	default:
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1PrivateCliInternalServerError{Code: c, Message: m}, true
	}
}

func v1ListEventRetentionPoliciesResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1ListEventRetentionPoliciesRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1ListEventRetentionPoliciesUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1ListEventRetentionPoliciesForbidden{Code: c, Message: m}, true
	case http.StatusBadRequest, http.StatusNotFound:
		return &oasgenserver.V1ListEventRetentionPoliciesBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1ListEventRetentionPoliciesBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1ListEventRetentionPoliciesInternalServerError{Code: c, Message: m}, true
	}
}

func v1CreateEventRetentionPolicyResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1CreateEventRetentionPolicyRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1CreateEventRetentionPolicyUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1CreateEventRetentionPolicyForbidden{Code: c, Message: m}, true
	case http.StatusConflict:
		return &oasgenserver.V1CreateEventRetentionPolicyConflict{Code: c, Message: m}, true
	case http.StatusBadRequest, http.StatusNotFound:
		return &oasgenserver.V1CreateEventRetentionPolicyBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1CreateEventRetentionPolicyBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1CreateEventRetentionPolicyInternalServerError{Code: c, Message: m}, true
	}
}

func v1GetEventRetentionPolicyResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1GetEventRetentionPolicyRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1GetEventRetentionPolicyUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1GetEventRetentionPolicyForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1GetEventRetentionPolicyNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1GetEventRetentionPolicyBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1GetEventRetentionPolicyBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1GetEventRetentionPolicyInternalServerError{Code: c, Message: m}, true
	}
}

func v1UpdateEventRetentionPolicyResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1UpdateEventRetentionPolicyRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1UpdateEventRetentionPolicyUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1UpdateEventRetentionPolicyForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1UpdateEventRetentionPolicyNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1UpdateEventRetentionPolicyBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1UpdateEventRetentionPolicyBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1UpdateEventRetentionPolicyInternalServerError{Code: c, Message: m}, true
	}
}

func v1DeleteEventRetentionPolicyResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1DeleteEventRetentionPolicyRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1DeleteEventRetentionPolicyUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1DeleteEventRetentionPolicyForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1DeleteEventRetentionPolicyNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1DeleteEventRetentionPolicyBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1DeleteEventRetentionPolicyBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1DeleteEventRetentionPolicyInternalServerError{Code: c, Message: m}, true
	}
}

func v1ListEventRetentionOperationsResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1ListEventRetentionOperationsRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1ListEventRetentionOperationsUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1ListEventRetentionOperationsForbidden{Code: c, Message: m}, true
	case http.StatusBadRequest, http.StatusNotFound:
		return &oasgenserver.V1ListEventRetentionOperationsBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1ListEventRetentionOperationsBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1ListEventRetentionOperationsInternalServerError{Code: c, Message: m}, true
	}
}

func v1CreateEventRetentionOperationResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1CreateEventRetentionOperationRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1CreateEventRetentionOperationUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1CreateEventRetentionOperationForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1CreateEventRetentionOperationNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1CreateEventRetentionOperationBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1CreateEventRetentionOperationBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1CreateEventRetentionOperationInternalServerError{Code: c, Message: m}, true
	}
}

func v1GetEventRetentionOperationResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1GetEventRetentionOperationRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1GetEventRetentionOperationUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1GetEventRetentionOperationForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1GetEventRetentionOperationNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1GetEventRetentionOperationBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1GetEventRetentionOperationBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1GetEventRetentionOperationInternalServerError{Code: c, Message: m}, true
	}
}

func v1AbortEventRetentionOperationResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1AbortEventRetentionOperationRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1AbortEventRetentionOperationUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1AbortEventRetentionOperationForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1AbortEventRetentionOperationNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1AbortEventRetentionOperationBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1AbortEventRetentionOperationBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1AbortEventRetentionOperationInternalServerError{Code: c, Message: m}, true
	}
}

func v1SnaplockLitigationBeginResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1SnaplockLitigationBeginRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1SnaplockLitigationBeginUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1SnaplockLitigationBeginForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1SnaplockLitigationBeginNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1SnaplockLitigationBeginBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1SnaplockLitigationBeginBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1SnaplockLitigationBeginInternalServerError{Code: c, Message: m}, true
	}
}

func v1SnaplockLitigationCollectionGetResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1SnaplockLitigationCollectionGetRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1SnaplockLitigationCollectionGetUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1SnaplockLitigationCollectionGetForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1SnaplockLitigationCollectionGetNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1SnaplockLitigationCollectionGetBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1SnaplockLitigationCollectionGetBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1SnaplockLitigationCollectionGetInternalServerError{Code: c, Message: m}, true
	}
}

func v1SnaplockLitigationEndResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1SnaplockLitigationEndRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1SnaplockLitigationEndUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1SnaplockLitigationEndForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1SnaplockLitigationEndNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1SnaplockLitigationEndBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1SnaplockLitigationEndBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1SnaplockLitigationEndInternalServerError{Code: c, Message: m}, true
	}
}

func v1SnaplockLitigationGetResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1SnaplockLitigationGetRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1SnaplockLitigationGetUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1SnaplockLitigationGetForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1SnaplockLitigationGetNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1SnaplockLitigationGetBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1SnaplockLitigationGetBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1SnaplockLitigationGetInternalServerError{Code: c, Message: m}, true
	}
}

func v1SnaplockLitigationOperationCreateResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1SnaplockLitigationOperationCreateRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1SnaplockLitigationOperationCreateUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1SnaplockLitigationOperationCreateForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1SnaplockLitigationOperationCreateNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1SnaplockLitigationOperationCreateBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1SnaplockLitigationOperationCreateBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1SnaplockLitigationOperationCreateInternalServerError{Code: c, Message: m}, true
	}
}

func v1SnaplockLitigationOperationGetResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1SnaplockLitigationOperationGetRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1SnaplockLitigationOperationGetUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1SnaplockLitigationOperationGetForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1SnaplockLitigationOperationGetNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1SnaplockLitigationOperationGetBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1SnaplockLitigationOperationGetBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1SnaplockLitigationOperationGetInternalServerError{Code: c, Message: m}, true
	}
}

func v1SnaplockLitigationOperationAbortResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1SnaplockLitigationOperationAbortRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1SnaplockLitigationOperationAbortUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1SnaplockLitigationOperationAbortForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1SnaplockLitigationOperationAbortNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1SnaplockLitigationOperationAbortBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1SnaplockLitigationOperationAbortBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1SnaplockLitigationOperationAbortInternalServerError{Code: c, Message: m}, true
	}
}

func v1GetQoSPolicyResFromProxyHTTP(ctx context.Context, err error) (oasgenserver.V1GetQoSPolicyRes, bool) {
	pe, ok := asProxyHTTPErrorWithReconcile(ctx, err)
	if !ok {
		return nil, false
	}
	c, m := pe.Status, pe.Message
	switch c {
	case http.StatusUnauthorized:
		return &oasgenserver.V1GetQoSPolicyUnauthorized{Code: c, Message: m}, true
	case http.StatusForbidden:
		return &oasgenserver.V1GetQoSPolicyForbidden{Code: c, Message: m}, true
	case http.StatusNotFound:
		return &oasgenserver.V1GetQoSPolicyNotFound{Code: c, Message: m}, true
	case http.StatusBadRequest:
		return &oasgenserver.V1GetQoSPolicyBadRequest{Code: c, Message: m}, true
	default:
		if c >= 400 && c < 500 {
			return &oasgenserver.V1GetQoSPolicyBadRequest{Code: c, Message: m}, true
		}
		if c < 400 {
			c = http.StatusInternalServerError
		}
		return &oasgenserver.V1GetQoSPolicyInternalServerError{Code: c, Message: m}, true
	}
}
