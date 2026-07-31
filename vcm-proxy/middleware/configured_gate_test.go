package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

type stubConfiguredChecker struct {
	configured bool
}

func (s stubConfiguredChecker) Configured() bool {
	return s.configured
}

func TestConfiguredGate_AllowsWhenConfigured(t *testing.T) {
	nextCalled := false
	handler := ConfiguredGate(stubConfiguredChecker{configured: true}, DefaultAllowlist)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/pools", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, nextCalled)
	assert.Equal(t, http.StatusAccepted, rec.Code)
}

func TestConfiguredGate_BlocksUnconfiguredNonAllowlistedRoute(t *testing.T) {
	nextCalled := false
	handler := ConfiguredGate(stubConfiguredChecker{configured: false}, DefaultAllowlist)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/pools", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.False(t, nextCalled)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body vcmserver.ErrorResponseV1
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, float64(http.StatusServiceUnavailable), body.Code)
	assert.Equal(t, "VCM is not configured", body.Message)
	assert.False(t, body.Retriable.IsSet())
}

func TestConfiguredGate_AllowsUnconfiguredAllowlistedRoutes(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "health", method: http.MethodGet, path: "/health"},
		{name: "ready", method: http.MethodGet, path: "/ready"},
		{name: "setConfig", method: http.MethodPost, path: "/v1/config"},
		{name: "metrics", method: http.MethodGet, path: "/metrics"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			handler := ConfiguredGate(stubConfiguredChecker{configured: false}, DefaultAllowlist)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
			}))

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.True(t, nextCalled)
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}

func TestConfiguredGate_BlocksWrongMethodOnAllowlistedPath(t *testing.T) {
	nextCalled := false
	handler := ConfiguredGate(stubConfiguredChecker{configured: false}, DefaultAllowlist)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.False(t, nextCalled)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestConfiguredGate_NilCheckerAllowsAll(t *testing.T) {
	nextCalled := false
	handler := ConfiguredGate(nil, DefaultAllowlist)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/pools", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, nextCalled)
}

func TestIsAllowlisted(t *testing.T) {
	allowlist := DefaultAllowlist

	assert.True(t, isAllowlisted("/health", http.MethodGet, allowlist))
	assert.False(t, isAllowlisted("/health", http.MethodPost, allowlist))
	assert.True(t, isAllowlisted("/ready", http.MethodGet, allowlist))
	assert.False(t, isAllowlisted("/ready", http.MethodPost, allowlist))
	assert.True(t, isAllowlisted("/v1/config", http.MethodPost, allowlist))
	assert.False(t, isAllowlisted("/v1/pools", http.MethodPost, allowlist))
}
