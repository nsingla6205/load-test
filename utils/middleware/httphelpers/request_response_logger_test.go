package httphelpers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestLoggingHttpHandler_BindsCorrelationBeforeAccessLog(t *testing.T) {
	var gotCorrelation string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCorrelation = r.Header.Get(log.RequestCorrelationID)
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/pools", nil)
	req.Header.Set(log.RequestCorrelationID, "corr-1")
	req.Header.Set(log.RequestID, "req-1")

	rec := httptest.NewRecorder()
	LoggingHttpHandler(next).ServeHTTP(rec, req)

	require.Equal(t, "corr-1", gotCorrelation)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}
