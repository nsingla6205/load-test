package orchestrator

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestCorrelationIDFromContext(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() context.Context
		want string
	}{
		{
			name: "nil context",
			ctx:  func() context.Context { return nil },
			want: "",
		},
		{
			name: "empty context",
			ctx:  context.Background,
			want: "",
		},
		{
			name: "propagator key",
			ctx: func() context.Context {
				return context.WithValue(context.Background(), CorrelationIDKey, "corr-propagator")
			},
			want: "corr-propagator",
		},
		{
			name: "middleware string key",
			ctx: func() context.Context {
				return context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-middleware")
			},
			want: "corr-middleware",
		},
		{
			name: "middleware http header",
			ctx: func() context.Context {
				header := http.Header{}
				header.Set(string(middleware.CorrelationIDName), "corr-header")
				return context.WithValue(context.Background(), middleware.CorrelationContextKey, header)
			},
			want: "corr-header",
		},
		{
			name: "logger field map",
			ctx: func() context.Context {
				fields := log.Fields{RequestCorrelationID: "corr-fields"}
				return context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
			},
			want: "corr-fields",
		},
		{
			name: "propagator key wins over middleware key",
			ctx: func() context.Context {
				ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-middleware")
				return context.WithValue(ctx, CorrelationIDKey, "corr-propagator")
			},
			want: "corr-propagator",
		},
		{
			name: "empty values are skipped",
			ctx: func() context.Context {
				ctx := context.WithValue(context.Background(), CorrelationIDKey, "")
				return context.WithValue(ctx, middleware.CorrelationContextKey, "corr-middleware")
			},
			want: "corr-middleware",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CorrelationIDFromContext(tt.ctx()))
		})
	}
}

// TestWithRequestContext_BridgesMiddlewareKey covers the gap that left VCM
// workflow and activity logs without a correlation id: the HTTP middleware
// writes a typed key that the workflow context propagators never read.
func TestWithRequestContext_BridgesMiddlewareKey(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-123")

	assert.Nil(t, ctx.Value(CorrelationIDKey), "precondition: propagator key unset")

	bridged := WithRequestContext(ctx)
	assert.Equal(t, "corr-123", bridged.Value(CorrelationIDKey))
}

func TestWithRequestContext_NoCorrelationIsNoOp(t *testing.T) {
	ctx := WithRequestContext(context.Background())
	assert.Nil(t, ctx.Value(CorrelationIDKey))
}

func TestWithCorrelationID(t *testing.T) {
	assert.Equal(t, "corr-1", WithCorrelationID(context.Background(), "corr-1").Value(CorrelationIDKey))
	assert.Nil(t, WithCorrelationID(context.Background(), "").Value(CorrelationIDKey))
}

func TestWithDeploymentID(t *testing.T) {
	ctx := WithDeploymentID(context.Background(), "deploy-1")
	assert.Equal(t, "deploy-1", ctx.Value(DeploymentIDKey))
	assert.Equal(t, "deploy-1", DeploymentIDFromContext(ctx))

	assert.Nil(t, WithDeploymentID(context.Background(), "").Value(DeploymentIDKey))
	assert.Equal(t, "", DeploymentIDFromContext(context.Background()))
	assert.Equal(t, "", DeploymentIDFromContext(nil))
}
