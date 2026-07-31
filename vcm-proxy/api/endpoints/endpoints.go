package api

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
)

const notReadyMessage = "VCM is not ready to serve API requests"

// ServerState tracks whether the server is shutting down.
type ServerState struct {
	shuttingDown atomic.Bool
}

// NewServerState creates a new ServerState instance.
func NewServerState() *ServerState {
	return &ServerState{}
}

// SetShuttingDown marks the server as shutting down.
func (s *ServerState) SetShuttingDown() {
	s.shuttingDown.Store(true)
}

// IsShuttingDown returns true if the server is in the process of shutting down.
func (s *ServerState) IsShuttingDown() bool {
	return s.shuttingDown.Load()
}

// Handler implements the ogen-generated vcmserver.Handler interface.
type Handler struct {
	vcmserver.UnimplementedHandler
	Orchestrator factory.OrchestratorFactory
	ServerState  *ServerState
	ConfigStore  *vcmconfig.Store
}

// GetHealth returns the server health status.
func (h Handler) GetHealth(ctx context.Context) (vcmserver.GetHealthRes, error) {
	if h.ServerState != nil && h.ServerState.IsShuttingDown() {
		return &vcmserver.ErrorResponseV1{
			Code:    500,
			Message: "Server is shutting down",
		}, nil
	}
	return &vcmserver.HealthResponse{
		Status: vcmserver.NewOptString("healthy"),
	}, nil
}

// GetReady reports whether VCM is configured and its database validation succeeded.
func (h Handler) GetReady(ctx context.Context) (vcmserver.GetReadyRes, error) {
	if h.ConfigStore != nil && h.ConfigStore.Configured() {
		return &vcmserver.GetReadyOK{}, nil
	}
	return &vcmserver.ErrorResponseV1{
		Code:    http.StatusServiceUnavailable,
		Message: notReadyMessage,
	}, nil
}
