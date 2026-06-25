package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/metrics"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/httphelpers"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

const (
	metricsPath = "/metrics"
	healthPath  = "/health"
)

func isOpcRequestIDExempt(path string) bool {
	return path == metricsPath || path == healthPath
}

const (
	errMsgOpcRequestIDRequired     = "Opc-Request-Id header is required"
	errMsgOpcWorkRequestIDRequired = "Opc-Work-Request-Id header is required"
)

func opcRequestIDRequired(method string) bool {
	return method != http.MethodGet
}

// WrapWithOCIAndLogging wires the OCI HTTP stack (outer → inner):
//  1. ociMetricsMiddleware — Prometheus counter + duration histogram; outermost so latency
//     covers the full middleware chain (request-prep, logging, handler).
//  2. ociPrepareRequestMiddleware — for non-exempt endpoints, requires a valid UUID opc-request-id,
//     sets x-correlation-id = opc-request-id, and echoes opc-request-id on the response.
//     Exempt endpoints (/metrics, /health) bypass this middleware and won't get an opc-request-id response header.
//     HeaderContextKey, ContextSLoggerKey and TemporalSLoggerKey share the same requestFields map,
//  3. httphelpers.LoggingHttpHandler — access logs (default logger; does not read ContextSLoggerKey).
//
// Apply auth and recover outside this wrapper.
func WrapWithOCIAndLogging(api http.Handler) http.Handler {
	api = httphelpers.LoggingHttpHandler(api)
	api = ociPrepareRequestMiddleware(api)
	api = ociMetricsMiddleware(api)
	return api
}

// ociPrepareRequestMiddleware sets OPC / correlation headers, attaches HeaderContextKey, wires the
// request logger (ContextSLoggerKey), and propagates the same fields on TemporalSLoggerKey for workflows.
func ociPrepareRequestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exempt := isOpcRequestIDExempt(r.URL.Path)

		opcID := strings.TrimSpace(r.Header.Get(string(utilsmiddleware.OPCRequestIDHeaderName)))
		opcWorkID := strings.TrimSpace(r.Header.Get(string(utilsmiddleware.OPCWorkRequestIDHeaderName)))

		if !exempt && opcRequestIDRequired(r.Method) {
			if err := validateOpcRequestID(opcID); err != nil {
				writeOpcRequestIDBadRequest(w, opcID, opcWorkID, err.Error())
				return
			}
			if err := validateOpcWorkRequestID(opcWorkID); err != nil {
				writeOpcRequestIDBadRequest(w, opcID, opcWorkID, err.Error())
				return
			}
		}

		if !exempt && opcID == "" {
			opcID = uuid.NewString()
		}

		if opcID != "" {
			r.Header.Set(string(utilsmiddleware.OPCRequestIDHeaderName), opcID)
			// Align with GCNV: x-correlation-id is the same as opc-request-id for tracing / GetCoRelationIDFromContext.
			r.Header.Set(log.RequestCorrelationID, opcID)
		}
		if opcWorkID != "" {
			r.Header.Set(string(utilsmiddleware.OPCWorkRequestIDHeaderName), opcWorkID)
		}

		requestFields := log.Fields{
			string(utilsmiddleware.RequestCorrelationID):    opcID,
			string(utilsmiddleware.RequestOpcWorkRequestID): opcWorkID,
			"traceMethod":             r.Method,
			"traceURL":                r.URL.String(),
		}
		logger := log.NewLogger().WithFields("requestFields", requestFields)
		ctx := r.Context()
		ctx = context.WithValue(ctx, utilsmiddleware.HeaderContextKey, r.Header)
		ctx = context.WithValue(ctx, utilsmiddleware.ContextSLoggerKey, logger)
		ctx = context.WithValue(ctx, utilsmiddleware.TemporalSLoggerKey, requestFields)

		rw := &opcRequestIDResponseWriter{
			ResponseWriter: w,
			opcID:          opcID,
			opcWorkID:      opcWorkID,
		}
		next.ServeHTTP(rw, r.WithContext(ctx))
	})
}

func validateOpcRequestID(opcID string) error {
	if opcID == "" {
		return errors.New(errMsgOpcRequestIDRequired)
	}
	return nil
}

func validateOpcWorkRequestID(opcWorkID string) error {
	if opcWorkID == "" {
		return errors.New(errMsgOpcWorkRequestIDRequired)
	}
	return nil
}

func writeOpcRequestIDBadRequest(w http.ResponseWriter, opcID, opcWorkID, message string) {
	if opcID != "" {
		w.Header().Set(string(utilsmiddleware.OPCRequestIDHeaderName), opcID)
	}
	if opcWorkID != "" {
		w.Header().Set(string(utilsmiddleware.OPCWorkRequestIDHeaderName), opcWorkID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(ociserver.Error{
		Code:    float64(http.StatusBadRequest),
		Message: message,
	})
}

// opcRequestIDResponseWriter sets opc-request-id on the HTTP response (OCI contract) when the handler
// commits headers via WriteHeader or Write.
type opcRequestIDResponseWriter struct {
	http.ResponseWriter
	opcID       string
	opcWorkID   string
	headerAdded bool
}

func (w *opcRequestIDResponseWriter) ensureHeader() {
	if w.headerAdded {
		return
	}
	if w.opcID != "" {
		w.ResponseWriter.Header().Set(string(utilsmiddleware.OPCRequestIDHeaderName), w.opcID)
	}
	if w.opcWorkID != "" {
		w.ResponseWriter.Header().Set(string(utilsmiddleware.OPCWorkRequestIDHeaderName), w.opcWorkID)
	}
	w.headerAdded = true
}

func (w *opcRequestIDResponseWriter) WriteHeader(code int) {
	w.ensureHeader()
	w.ResponseWriter.WriteHeader(code)
}

func (w *opcRequestIDResponseWriter) Write(b []byte) (int, error) {
	w.ensureHeader()
	return w.ResponseWriter.Write(b)
}

func ociMetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == metricsPath {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		wrapped := &statusCapturingWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		endpoint := metrics.NormalizeRoute(r.URL.Path)
		method := r.Method
		region := metrics.Region()
		statusCode := strconv.Itoa(wrapped.statusCode)
		metrics.APIRequestsTotal.WithLabelValues(endpoint, method, statusCode, region).Inc()
		metrics.APIRequestDurationSeconds.WithLabelValues(method, endpoint, region).Observe(time.Since(start).Seconds())
	})
}

type statusCapturingWriter struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (w *statusCapturingWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.statusCode = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusCapturingWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
