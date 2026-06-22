package orchestrator

import "context"

// HeartbeatFunc is the signature for recording activity heartbeats.
// The temporalbackend package overrides this with activity.RecordHeartbeat.
type HeartbeatFunc func(ctx context.Context, details ...interface{})

var heartbeatFn HeartbeatFunc = func(_ context.Context, _ ...interface{}) {}

// SetHeartbeatFunc installs the backend-specific heartbeat implementation.
// Called by temporalbackend during worker setup.
func SetHeartbeatFunc(fn HeartbeatFunc) {
	heartbeatFn = fn
}

// RecordHeartbeat records an activity heartbeat. In Temporal mode this delegates
// to activity.RecordHeartbeat; in library mode it is a no-op.
func RecordHeartbeat(ctx context.Context, details ...interface{}) {
	heartbeatFn(ctx, details...)
}
