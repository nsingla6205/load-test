package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
)

type testDBOpener struct{}

func (testDBOpener) Open(ctx context.Context, _ vcmserver.DbConfigV1) (vcmdb.Storage, error) {
	return vcmdb.Open(ctx, vcmdb.Config{
		Driver: vcmdb.DriverSQLite,
		SQLite: vcmdb.SQLConfig{DSN: "file:vcm-endpoints-test?mode=memory&cache=shared"},
	}, log.NewLogger(), true)
}

func configuredStore(t *testing.T) *vcmconfig.Store {
	t.Helper()

	store := vcmconfig.NewStoreWithOpener(log.NewLogger(), testDBOpener{})
	store.Apply(context.Background(), &vcmserver.SetConfigRequestV1{
		DbConfig: vcmserver.DbConfigV1{
			Project:  "project",
			Instance: "instance",
			Database: "database",
		},
	})
	require.Eventually(t, store.Configured, 2*time.Second, 10*time.Millisecond)
	return store
}

func serveEndpoint(t *testing.T, handler *Handler, path string) *httptest.ResponseRecorder {
	t.Helper()

	server, err := vcmserver.NewServer(handler)
	require.NoError(t, err)

	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func TestGetHealth(t *testing.T) {
	tests := []struct {
		name           string
		shuttingDown   bool
		expectedStatus int
	}{
		{
			name:           "process is running",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "process is shutting down",
			shuttingDown:   true,
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := NewServerState()
			if test.shuttingDown {
				state.SetShuttingDown()
			}

			response := serveEndpoint(t, &Handler{ServerState: state}, "/health")

			require.Equal(t, test.expectedStatus, response.Code)
			if !test.shuttingDown {
				var body vcmserver.HealthResponse
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
				status, ok := body.Status.Get()
				require.True(t, ok)
				assert.Equal(t, "healthy", status)
				return
			}

			var body vcmserver.ErrorResponseV1
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, float64(http.StatusInternalServerError), body.Code)
			assert.Equal(t, "Server is shutting down", body.Message)
		})
	}
}

func TestGetReady(t *testing.T) {
	tests := []struct {
		name           string
		store          func(t *testing.T) *vcmconfig.Store
		expectedStatus int
	}{
		{
			name:           "configuration store is unavailable",
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name: "database validation has not succeeded",
			store: func(t *testing.T) *vcmconfig.Store {
				return vcmconfig.NewStore(log.NewLogger(), 0)
			},
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "database validation succeeded",
			store:          configuredStore,
			expectedStatus: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var store *vcmconfig.Store
			if test.store != nil {
				store = test.store(t)
			}
			response := serveEndpoint(t, &Handler{ConfigStore: store}, "/ready")

			require.Equal(t, test.expectedStatus, response.Code)
			if test.expectedStatus == http.StatusOK {
				assert.Empty(t, response.Body.String())
				return
			}

			var body vcmserver.ErrorResponseV1
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, float64(http.StatusServiceUnavailable), body.Code)
			assert.Equal(t, notReadyMessage, body.Message)
		})
	}
}
