package endpoints

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/api/ontap-proxy-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/middleware"
)

type wrappedMapper struct {
	name             string
	call             func(context.Context, error) (any, bool)
	explicitTypes    map[int]any
	default4xxType   any
	defaultOtherType any
}

func assertProxyHTTPResponse(t *testing.T, got any, wantType any, wantCode int, wantMsg string) {
	t.Helper()
	require.NotNil(t, got)
	require.Equal(t, reflect.TypeOf(wantType), reflect.TypeOf(got))

	v := reflect.ValueOf(got)
	require.Equal(t, reflect.Pointer, v.Kind())
	elem := v.Elem()
	require.True(t, elem.IsValid())

	codeField := elem.FieldByName("Code")
	require.True(t, codeField.IsValid())
	assert.Equal(t, wantCode, int(codeField.Int()))

	msgField := elem.FieldByName("Message")
	require.True(t, msgField.IsValid())
	assert.Equal(t, wantMsg, msgField.String())
}

func TestAsProxyHTTPError(t *testing.T) {
	t.Run("WhenErrorIsProxyHTTPError_ShouldReturnMappedProxyError", func(t *testing.T) {
		pe := &middleware.ProxyHTTPError{Status: http.StatusBadRequest, Message: "bad"}
		got, ok := asProxyHTTPError(pe)
		require.True(t, ok)
		require.Equal(t, pe, got)
	})

	t.Run("WhenErrorIsPlainError_ShouldReturnFalse", func(t *testing.T) {
		got, ok := asProxyHTTPError(fmt.Errorf("plain error"))
		require.False(t, ok)
		require.Nil(t, got)
	})
}

func TestProxyHTTPResponseMappers(t *testing.T) {
	ctx := context.Background()
	msg := "mapped message"

	mappers := []wrappedMapper{
		{
			name: "WhenMapperIsSnaplockFileDelete_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := snaplockFileDeleteResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.SnaplockFileDeleteUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.SnaplockFileDeleteForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.SnaplockFileDeleteNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.SnaplockFileDeleteBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.SnaplockFileDeleteBadRequest)(nil),
			defaultOtherType: (*oasgenserver.SnaplockFileDeleteInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsPrivateCLI_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1PrivateCliResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1PrivateCliUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1PrivateCliForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1PrivateCliNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1PrivateCliBadRequest)(nil),
			},
			defaultOtherType: (*oasgenserver.V1PrivateCliInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsListEventRetentionPolicies_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1ListEventRetentionPoliciesResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1ListEventRetentionPoliciesUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1ListEventRetentionPoliciesForbidden)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1ListEventRetentionPoliciesBadRequest)(nil),
				http.StatusNotFound:     (*oasgenserver.V1ListEventRetentionPoliciesBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1ListEventRetentionPoliciesBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1ListEventRetentionPoliciesInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsCreateEventRetentionPolicy_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1CreateEventRetentionPolicyResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1CreateEventRetentionPolicyUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1CreateEventRetentionPolicyForbidden)(nil),
				http.StatusConflict:     (*oasgenserver.V1CreateEventRetentionPolicyConflict)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1CreateEventRetentionPolicyBadRequest)(nil),
				http.StatusNotFound:     (*oasgenserver.V1CreateEventRetentionPolicyBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1CreateEventRetentionPolicyBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1CreateEventRetentionPolicyInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsGetEventRetentionPolicy_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1GetEventRetentionPolicyResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1GetEventRetentionPolicyUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1GetEventRetentionPolicyForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1GetEventRetentionPolicyNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1GetEventRetentionPolicyBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1GetEventRetentionPolicyBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1GetEventRetentionPolicyInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsUpdateEventRetentionPolicy_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1UpdateEventRetentionPolicyResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1UpdateEventRetentionPolicyUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1UpdateEventRetentionPolicyForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1UpdateEventRetentionPolicyNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1UpdateEventRetentionPolicyBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1UpdateEventRetentionPolicyBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1UpdateEventRetentionPolicyInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsDeleteEventRetentionPolicy_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1DeleteEventRetentionPolicyResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1DeleteEventRetentionPolicyUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1DeleteEventRetentionPolicyForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1DeleteEventRetentionPolicyNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1DeleteEventRetentionPolicyBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1DeleteEventRetentionPolicyBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1DeleteEventRetentionPolicyInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsListEventRetentionOperations_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1ListEventRetentionOperationsResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1ListEventRetentionOperationsUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1ListEventRetentionOperationsForbidden)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1ListEventRetentionOperationsBadRequest)(nil),
				http.StatusNotFound:     (*oasgenserver.V1ListEventRetentionOperationsBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1ListEventRetentionOperationsBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1ListEventRetentionOperationsInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsCreateEventRetentionOperation_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1CreateEventRetentionOperationResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1CreateEventRetentionOperationUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1CreateEventRetentionOperationForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1CreateEventRetentionOperationNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1CreateEventRetentionOperationBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1CreateEventRetentionOperationBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1CreateEventRetentionOperationInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsGetEventRetentionOperation_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1GetEventRetentionOperationResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1GetEventRetentionOperationUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1GetEventRetentionOperationForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1GetEventRetentionOperationNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1GetEventRetentionOperationBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1GetEventRetentionOperationBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1GetEventRetentionOperationInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsAbortEventRetentionOperation_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1AbortEventRetentionOperationResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1AbortEventRetentionOperationUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1AbortEventRetentionOperationForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1AbortEventRetentionOperationNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1AbortEventRetentionOperationBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1AbortEventRetentionOperationBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1AbortEventRetentionOperationInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsSnaplockLitigationBegin_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1SnaplockLitigationBeginResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1SnaplockLitigationBeginUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1SnaplockLitigationBeginForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1SnaplockLitigationBeginNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1SnaplockLitigationBeginBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1SnaplockLitigationBeginBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1SnaplockLitigationBeginInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsSnaplockLitigationCollectionGet_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1SnaplockLitigationCollectionGetResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1SnaplockLitigationCollectionGetUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1SnaplockLitigationCollectionGetForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1SnaplockLitigationCollectionGetNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1SnaplockLitigationCollectionGetBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1SnaplockLitigationCollectionGetBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1SnaplockLitigationCollectionGetInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsSnaplockLitigationEnd_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1SnaplockLitigationEndResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1SnaplockLitigationEndUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1SnaplockLitigationEndForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1SnaplockLitigationEndNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1SnaplockLitigationEndBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1SnaplockLitigationEndBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1SnaplockLitigationEndInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsSnaplockLitigationGet_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1SnaplockLitigationGetResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1SnaplockLitigationGetUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1SnaplockLitigationGetForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1SnaplockLitigationGetNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1SnaplockLitigationGetBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1SnaplockLitigationGetBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1SnaplockLitigationGetInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsSnaplockLitigationOperationCreate_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1SnaplockLitigationOperationCreateResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1SnaplockLitigationOperationCreateUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1SnaplockLitigationOperationCreateForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1SnaplockLitigationOperationCreateNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1SnaplockLitigationOperationCreateBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1SnaplockLitigationOperationCreateBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1SnaplockLitigationOperationCreateInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsSnaplockLitigationOperationGet_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1SnaplockLitigationOperationGetResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1SnaplockLitigationOperationGetUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1SnaplockLitigationOperationGetForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1SnaplockLitigationOperationGetNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1SnaplockLitigationOperationGetBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1SnaplockLitigationOperationGetBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1SnaplockLitigationOperationGetInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsSnaplockLitigationOperationAbort_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1SnaplockLitigationOperationAbortResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1SnaplockLitigationOperationAbortUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1SnaplockLitigationOperationAbortForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1SnaplockLitigationOperationAbortNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1SnaplockLitigationOperationAbortBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1SnaplockLitigationOperationAbortBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1SnaplockLitigationOperationAbortInternalServerError)(nil),
		},
		{
			name: "WhenMapperIsGetQoSPolicy_ShouldMapStatusesCorrectly",
			call: func(ctx context.Context, err error) (any, bool) {
				r, ok := v1GetQoSPolicyResFromProxyHTTP(ctx, err)
				return r, ok
			},
			explicitTypes: map[int]any{
				http.StatusUnauthorized: (*oasgenserver.V1GetQoSPolicyUnauthorized)(nil),
				http.StatusForbidden:    (*oasgenserver.V1GetQoSPolicyForbidden)(nil),
				http.StatusNotFound:     (*oasgenserver.V1GetQoSPolicyNotFound)(nil),
				http.StatusBadRequest:   (*oasgenserver.V1GetQoSPolicyBadRequest)(nil),
			},
			default4xxType:   (*oasgenserver.V1GetQoSPolicyBadRequest)(nil),
			defaultOtherType: (*oasgenserver.V1GetQoSPolicyInternalServerError)(nil),
		},
	}

	for _, mapper := range mappers {
		mapper := mapper
		t.Run(mapper.name, func(t *testing.T) {
			// Non-proxy errors are not mapped.
			got, ok := mapper.call(ctx, fmt.Errorf("plain transport error"))
			require.False(t, ok)
			require.Nil(t, got)

			for status, wantType := range mapper.explicitTypes {
				got, ok = mapper.call(ctx, &middleware.ProxyHTTPError{Status: status, Message: msg})
				require.True(t, ok)
				assertProxyHTTPResponse(t, got, wantType, status, msg)
			}

			if mapper.default4xxType != nil {
				got, ok = mapper.call(ctx, &middleware.ProxyHTTPError{Status: http.StatusTeapot, Message: msg})
				require.True(t, ok)
				assertProxyHTTPResponse(t, got, mapper.default4xxType, http.StatusTeapot, msg)
			}

			// c < 400 should be normalized to 500 for all mappers.
			got, ok = mapper.call(ctx, &middleware.ProxyHTTPError{Status: 399, Message: msg})
			require.True(t, ok)
			assertProxyHTTPResponse(t, got, mapper.defaultOtherType, http.StatusInternalServerError, msg)

			// 5xx remains unchanged and maps to "internal" response type.
			got, ok = mapper.call(ctx, &middleware.ProxyHTTPError{Status: http.StatusBadGateway, Message: msg})
			require.True(t, ok)
			assertProxyHTTPResponse(t, got, mapper.defaultOtherType, http.StatusBadGateway, msg)
		})
	}
}
