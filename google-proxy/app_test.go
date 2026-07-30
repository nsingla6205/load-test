package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/common"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	api "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/endpoints"
)

// TestGetDBConfig verifies that GetDBConfig maps all Config fields to the
// returned DbConfig correctly, including the MSIEnabled override.
func TestGetDBConfig(t *testing.T) {
	loc := time.UTC
	cfg := &common.Config{
		DBType:            "postgres",
		DBHost:            "localhost",
		DBPort:            "5432",
		DBUser:            "vcp",
		DBPassword:        "secret",
		DBName:            "vcpdb",
		DBSSLMode:         "disable",
		DBTimeZone:        loc,
		DBMaxOpenConns:    10,
		DBMaxIdleConns:    5,
		DBConnMaxLifetime: 30 * time.Minute,
		DBAdminUser:       "admin",
		DBAdminPassword:   "adminpass",
		MSIEnabled:        false,
	}

	got := GetDBConfig(cfg)

	assert.Equal(t, "postgres", got.Type)
	assert.Equal(t, "localhost", got.Host)
	assert.Equal(t, "5432", got.Port)
	assert.Equal(t, "vcp", got.User)
	assert.Equal(t, "secret", got.Password)
	assert.Equal(t, "vcpdb", got.Name)
	assert.Equal(t, "disable", got.SSLMode)
	assert.Equal(t, "UTC", got.TimeZone)
	assert.Equal(t, 10, got.MaxOpenConns)
	assert.Equal(t, 5, got.MaxIdleConns)
	assert.Equal(t, 30*time.Minute, got.ConnMaxLifetime)
	assert.Equal(t, "admin", got.AdminUser)
	assert.Equal(t, "adminpass", got.AdminPassword)
}

// TestGetDBConfig_MSIEnabled checks that when MSI is enabled the MSIDBUser
// overrides both the regular User and AdminUser.
func TestGetDBConfig_MSIEnabled(t *testing.T) {
	cfg := &common.Config{
		DBUser:      "vcp",
		DBAdminUser: "admin",
		MSIEnabled:  true,
		MSIDBUser:   "msi-user",
		DBTimeZone:  time.UTC,
	}

	got := GetDBConfig(cfg)

	assert.Equal(t, "msi-user", got.User, "User should be overridden by MSIDBUser")
	assert.Equal(t, "msi-user", got.AdminUser, "AdminUser should be overridden by MSIDBUser")
}

// TestSetupHTTPServer verifies that the returned *http.Server is configured
// with the address and timeouts from the supplied Config.
func TestSetupHTTPServer(t *testing.T) {
	cfg := &common.Config{
		GCPHost:           "0.0.0.0",
		GCPPort:           "8080",
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
	}

	srv := setupHTTPServer(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	require.NotNil(t, srv)
	assert.Equal(t, "0.0.0.0:8080", srv.Addr)
	assert.Equal(t, 5*time.Second, srv.ReadTimeout)
	assert.Equal(t, 10*time.Second, srv.WriteTimeout)
	assert.Equal(t, 30*time.Second, srv.IdleTimeout)
	assert.Equal(t, 2*time.Second, srv.ReadHeaderTimeout)
}

// TestSetupHTTPServer_MetricsEndpoint verifies that the /metrics route is
// registered and responds with 200.
func TestSetupHTTPServer_MetricsEndpoint(t *testing.T) {
	cfg := &common.Config{
		GCPHost:    "127.0.0.1",
		GCPPort:    "0",
		DBTimeZone: time.UTC,
	}
	srv := setupHTTPServer(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

// TestBuildHandler verifies that buildHandler constructs an *api.Handler with
// all fields wired correctly.
func TestBuildHandler(t *testing.T) {
	serverState := api.NewServerState()

	handler := buildHandler(nil, serverState)

	require.NotNil(t, handler)
	assert.Equal(t, serverState, handler.ServerState)
	assert.Nil(t, handler.Orchestrator)
}

// TestGetDBConfig_TimeZoneString verifies that the DBTimeZone Location is
// converted to its string name.
func TestGetDBConfig_TimeZoneString(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	cfg := &common.Config{DBTimeZone: loc}
	got := GetDBConfig(cfg)

	assert.Equal(t, "America/New_York", got.TimeZone)
}

// Ensure GetDBConfig returns a dbutils.DbConfig (compile-time type assertion).
var _ dbutils.DbConfig = dbutils.DbConfig{}
