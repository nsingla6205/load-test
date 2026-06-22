package orchestrator

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

const (
	// CorrelationIDKey is the context key carrying the inbound request's
	// correlation id (e.g. the x-correlation-id header value).
	CorrelationIDKey string = "x-correlation-id"
	// DeploymentIDKey is the context key carrying the deployment id associated
	// with the inbound request.
	DeploymentIDKey string = "x-deployment-id"
)

// Fields is a structured set of log key/value pairs shared by the workflow
// logger adapters.
type Fields map[string]interface{}

// std backs the package-level helpers used during backend wiring (e.g. the
// one-time propagator construction logs). It is the repo slog-wrapper logger.
// Workflow-time logging goes through the engine-specific UnifiedLogger
// adapters, not this logger.
var std = log.NewLogger()

// Infof logs a formatted message at info level on the package logger.
func Infof(format string, args ...interface{}) {
	std.Infof(format, args...)
}
