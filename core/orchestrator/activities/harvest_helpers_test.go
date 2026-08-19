package activities

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestDoHarvestHTTP_TimesOutOnSlowServer verifies a hung Harvest Farm no longer blocks the caller
// indefinitely: with the timeout configured to 1s, a 2s-sleeping server must fail fast (VSCP-6750).
func TestDoHarvestHTTP_TimesOutOnSlowServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	t.Setenv("HARVEST_POLLER_IO_TIMEOUT_SECONDS", "1")

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL, nil)
	assert.NoError(t, err)

	start := time.Now()
	_, err = doHarvestHTTP(context.Background(), req, nil)
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deadline exceeded", "expected request timeout error")
	assert.Less(t, elapsed, 2*time.Second, "request should time out before the server responds")
}

// TestDoHarvestHTTP_SucceedsOnFastServer verifies normal responses are unaffected by the helper.
func TestDoHarvestHTTP_SucceedsOnFastServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL, nil)
	assert.NoError(t, err)

	resp, err := doHarvestHTTP(context.Background(), req, nil)
	assert.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
