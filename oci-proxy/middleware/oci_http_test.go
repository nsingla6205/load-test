package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/metrics"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// opcRequestIDHTTPHeader is the wire name for OPCRequestIDHeaderName (ContextString);
// net/http Header methods require string keys.
var opcRequestIDHTTPHeader = string(utilsmiddleware.OPCRequestIDHeaderName)
var opcWorkRequestIDHTTPHeader = string(utilsmiddleware.OPCWorkRequestIDHeaderName)

func TestOciPrepareRequestMiddleware_EchoesClientOPC(t *testing.T) {
	const clientID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set(opcRequestIDHTTPHeader, clientID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, clientID, rr.Header().Get(opcRequestIDHTTPHeader))
	require.Equal(t, clientID, req.Header.Get(log.RequestCorrelationID))
}

func TestOciPrepareRequestMiddleware_AllowsMissingOPCOnGet(t *testing.T) {
	called := false
	var correlation string
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		correlation = r.Header.Get(log.RequestCorrelationID)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1beta/workRequests/wf-1", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusOK, rr.Code)
	require.NotEmpty(t, correlation, "GET without opc-request-id should get a generated correlation id")
}

func TestOciPrepareRequestMiddleware_AllowsNonUUIDOPCOnGet(t *testing.T) {
	called := false
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "not-a-uuid", r.Header.Get(opcRequestIDHTTPHeader))
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1beta/workRequests/wf-1", nil)
	req.Header.Set(opcRequestIDHTTPHeader, "not-a-uuid")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "not-a-uuid", rr.Header().Get(opcRequestIDHTTPHeader))
}

func TestOciPrepareRequestMiddleware_RejectsMissingOPCOnCRUD(t *testing.T) {
	called := false
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1beta/pools", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.False(t, called)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Empty(t, rr.Header().Get(opcRequestIDHTTPHeader))
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var body ociserver.Error
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, float64(http.StatusBadRequest), body.Code)
	require.Equal(t, errMsgOpcRequestIDRequired, body.Message)
}

func TestOciPrepareRequestMiddleware_RejectsMissingWorkRequestIDOnCRUD(t *testing.T) {
	const opc = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	called := false
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPut, "/v1beta/pools/ocid1.pool.oc1..abc", nil)
	req.Header.Set(opcRequestIDHTTPHeader, opc)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.False(t, called)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var body ociserver.Error
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, float64(http.StatusBadRequest), body.Code)
	require.Equal(t, errMsgOpcWorkRequestIDRequired, body.Message)
}

func TestOciPrepareRequestMiddleware_AcceptsBothHeadersOnCRUD(t *testing.T) {
	const opc = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	const workID = "wr-12345"
	called := false
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, opc, r.Header.Get(opcRequestIDHTTPHeader))
		require.Equal(t, workID, r.Header.Get(opcWorkRequestIDHTTPHeader))
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1beta/pools", nil)
	req.Header.Set(opcRequestIDHTTPHeader, opc)
	req.Header.Set(opcWorkRequestIDHTTPHeader, workID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusCreated, rr.Code)
	require.Equal(t, opc, rr.Header().Get(opcRequestIDHTTPHeader))
	require.Equal(t, workID, rr.Header().Get(opcWorkRequestIDHTTPHeader))
}

func TestOciPrepareRequestMiddleware_RejectsMissingOPCOnAllMethods(t *testing.T) {
	for _, method := range []string{http.MethodOptions, http.MethodHead, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(tt *testing.T) {
			called := false
			h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))

			req := httptest.NewRequest(method, "/v1beta/pools", nil)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.False(tt, called)
			require.Equal(tt, http.StatusBadRequest, rr.Code)
			require.Contains(tt, rr.Body.String(), errMsgOpcRequestIDRequired)
		})
	}
}

func TestOciPrepareRequestMiddleware_SkipsMetrics(t *testing.T) {
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, "", rr.Header().Get(opcRequestIDHTTPHeader))
}

func TestOciPrepareRequestMiddleware_SkipsHealth(t *testing.T) {
	called := false
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "", rr.Header().Get(opcRequestIDHTTPHeader))
}

func TestOciPrepareRequestMiddleware_DoesNotSetXRequestID(t *testing.T) {
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get(log.RequestID))
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1beta/pools", nil)
	req.Header.Set(opcRequestIDHTTPHeader, "33333333-3333-3333-3333-333333333333")
	req.Header.Set(opcWorkRequestIDHTTPHeader, "wr-33333")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestOciPrepareRequestMiddleware_SetsContextLogger(t *testing.T) {
	const clientOPC = "cccccccc-cccc-4ccc-cccc-cccccccccccc"
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := r.Context().Value(utilsmiddleware.ContextSLoggerKey)
		require.NotNil(t, v)
		_, ok := v.(log.Logger)
		require.True(t, ok, "ContextSLoggerKey should hold log.Logger")
		hdr, ok := r.Context().Value(utilsmiddleware.HeaderContextKey).(http.Header)
		require.True(t, ok)
		require.Equal(t, clientOPC, hdr.Get(opcRequestIDHTTPHeader))
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1beta/pools", nil)
	req.Header.Set(opcRequestIDHTTPHeader, clientOPC)
	req.Header.Set(opcWorkRequestIDHTTPHeader, "wr-ctxlogger")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, clientOPC, rr.Header().Get(opcRequestIDHTTPHeader))
}

func TestOciPrepareRequestMiddleware_SetsTemporalFields(t *testing.T) {
	const opc = "11111111-1111-1111-1111-111111111111"
	const workID = "wr-temporal"
	var temporal log.Fields
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := r.Context().Value(utilsmiddleware.TemporalSLoggerKey).(log.Fields)
		require.True(t, ok)
		temporal = v
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1beta/pools", nil)
	req.Header.Set(opcRequestIDHTTPHeader, opc)
	req.Header.Set(opcWorkRequestIDHTTPHeader, workID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, opc, temporal["requestCorrelationID"])
	require.Equal(t, workID, temporal["requestOpcWorkRequestID"])
	require.Equal(t, http.MethodPost, temporal["traceMethod"])
	require.Equal(t, "/v1beta/pools", temporal["traceURL"])
}

func TestOciPrepareRequestMiddleware_SetsOpcWorkRequestIDContextKey(t *testing.T) {
	const opc = "44444444-4444-4444-4444-444444444444"
	const workID = "wr-propagate"
	var propagated any
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		propagated = r.Context().Value(utilsmiddleware.OPCWorkRequestIDHeaderName)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1beta/pools", nil)
	req.Header.Set(opcRequestIDHTTPHeader, opc)
	req.Header.Set(opcWorkRequestIDHTTPHeader, workID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, workID, propagated, "opc-work-request-id must be attached under the shared context key")
}

func TestOciPrepareRequestMiddleware_OmitsContextKeyWithoutWorkID(t *testing.T) {
	// GET requests do not require opc-work-request-id; when absent, the context
	// key must not be set (so downstream resolution stays empty rather than blank).
	var present bool
	h := ociPrepareRequestMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Context().Value(utilsmiddleware.OPCWorkRequestIDHeaderName).(string)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1beta/pools", nil)
	req.Header.Set(opcRequestIDHTTPHeader, "55555555-5555-5555-5555-555555555555")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.False(t, present, "context key must be absent when there is no opc-work-request-id")
}

func TestWrapWithOCIAndLogging_InnerHandlerSeesTemporal(t *testing.T) {
	const opc = "22222222-2222-2222-2222-222222222222"
	var temporal log.Fields
	var ctxLoggerOK bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := r.Context().Value(utilsmiddleware.TemporalSLoggerKey).(log.Fields)
		require.True(t, ok)
		temporal = v
		_, ctxLoggerOK = r.Context().Value(utilsmiddleware.ContextSLoggerKey).(log.Logger)
		w.WriteHeader(http.StatusOK)
	})
	h := WrapWithOCIAndLogging(inner)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/pools", nil)
	req.Header.Set(opcRequestIDHTTPHeader, opc)
	req.Header.Set(opcWorkRequestIDHTTPHeader, "wr-wrap")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, opc, temporal["requestCorrelationID"])
	require.True(t, ctxLoggerOK, "inner handler should see ContextSLoggerKey from ociPrepareRequestMiddleware")
}

// --- ociMetricsMiddleware tests ---

func TestOciMetricsMiddleware_IncrementsCounterOnSuccess(t *testing.T) {
	region := metrics.Region()
	before := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues("/v1beta/pools", "POST", "200", region))

	h := ociMetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1beta/pools", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	after := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues("/v1beta/pools", "POST", "200", region))
	assert.Equal(t, before+1, after)
}

func TestOciMetricsMiddleware_NormalizesPoolOCIDPath(t *testing.T) {
	region := metrics.Region()
	normalized := "/v1beta/pools/{poolOCID}"
	before := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues(normalized, "DELETE", "404", region))

	h := ociMetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	req := httptest.NewRequest(http.MethodDelete, "/v1beta/pools/ocid1.pool.oc1..abc", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	after := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues(normalized, "DELETE", "404", region))
	assert.Equal(t, before+1, after)
}

func TestOciMetricsMiddleware_Captures5xxStatus(t *testing.T) {
	region := metrics.Region()
	before := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues("/v1beta/pools", "POST", "500", region))

	h := ociMetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1beta/pools", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	after := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues("/v1beta/pools", "POST", "500", region))
	assert.Equal(t, before+1, after)
}

func TestOciMetricsMiddleware_DefaultsTo200WhenNoWriteHeader(t *testing.T) {
	region := metrics.Region()
	before := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues("/v1beta/pools", "GET", "200", region))

	h := ociMetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1beta/pools", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	after := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues("/v1beta/pools", "GET", "200", region))
	assert.Equal(t, before+1, after)
}

func TestOciMetricsMiddleware_SkipsMetricsPath(t *testing.T) {
	region := metrics.Region()
	before := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues("/metrics", "GET", "200", region))

	h := ociMetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	after := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues("/metrics", "GET", "200", region))
	assert.Equal(t, before, after)
}

func TestOciMetricsMiddleware_RecordsDuration(t *testing.T) {
	beforeCount := testutil.CollectAndCount(metrics.APIRequestDurationSeconds)

	h := ociMetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1beta/pools", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	afterCount := testutil.CollectAndCount(metrics.APIRequestDurationSeconds)
	assert.GreaterOrEqual(t, afterCount, beforeCount)
}

func TestOciMetricsMiddleware_MultipleRequestsAccumulate(t *testing.T) {
	region := metrics.Region()
	before := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues("/v1beta/pools", "GET", "200", region))

	h := ociMetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1beta/pools", nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	after := testutil.ToFloat64(metrics.APIRequestsTotal.WithLabelValues("/v1beta/pools", "GET", "200", region))
	assert.Equal(t, before+3, after)
}

// --- statusCapturingWriter tests ---

func TestStatusCapturingWriter_CapturesFirstWriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	w := &statusCapturingWriter{ResponseWriter: rr, statusCode: http.StatusOK}

	w.WriteHeader(http.StatusNotFound)
	w.WriteHeader(http.StatusInternalServerError)

	assert.Equal(t, http.StatusNotFound, w.statusCode)
	assert.True(t, w.wroteHeader)
}

func TestStatusCapturingWriter_DefaultsTo200(t *testing.T) {
	rr := httptest.NewRecorder()
	w := &statusCapturingWriter{ResponseWriter: rr, statusCode: http.StatusOK}

	assert.Equal(t, http.StatusOK, w.statusCode)
	assert.False(t, w.wroteHeader)
}

func TestStatusCapturingWriter_WriteSetsFlagButKeepsDefault(t *testing.T) {
	rr := httptest.NewRecorder()
	w := &statusCapturingWriter{ResponseWriter: rr, statusCode: http.StatusOK}

	n, err := w.Write([]byte("hello"))

	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.True(t, w.wroteHeader)
	assert.Equal(t, http.StatusOK, w.statusCode)
}

func TestStatusCapturingWriter_WriteAfterWriteHeaderKeepsOriginal(t *testing.T) {
	rr := httptest.NewRecorder()
	w := &statusCapturingWriter{ResponseWriter: rr, statusCode: http.StatusOK}

	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte("body"))

	assert.Equal(t, http.StatusCreated, w.statusCode)
}

func TestOciMetricsMiddleware_DurationUsesNormalizedEndpoint(t *testing.T) {
	h := ociMetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodDelete, "/v1beta/pools/ocid-op-test", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	region := metrics.Region()
	assert.NotPanics(t, func() {
		metrics.APIRequestDurationSeconds.WithLabelValues("DELETE", "/v1beta/pools/{poolOCID}", region)
	})
	assert.GreaterOrEqual(t, testutil.CollectAndCount(metrics.APIRequestDurationSeconds), 1)
}
