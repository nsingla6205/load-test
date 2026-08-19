package common

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestResolveUpgradeCorrelationID_PrefersRequestCorrelationID(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "request-corr")
	assert.Equal(t, "request-corr", ResolveUpgradeCorrelationID(ctx, "job-uuid"))
}

func TestResolveUpgradeCorrelationID_FallsBackToJobUUID(t *testing.T) {
	assert.Equal(t, "job-uuid", ResolveUpgradeCorrelationID(context.Background(), "job-uuid"))
}

func TestWithUpgradeCorrelationContext_SetsPropagationKeys(t *testing.T) {
	ctx := WithUpgradeCorrelationContext(context.Background(), "corr-123")

	assert.Equal(t, "corr-123", utils.GetCoRelationIDFromContext(ctx))
	assert.Equal(t, "corr-123", ctx.Value(temporalCorrelationIDKey))

	fields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields)
	require.True(t, ok)
	assert.Equal(t, "corr-123", fields[string(middleware.RequestCorrelationID)])
}

func TestWithUpgradeCorrelationContext_PreservesExistingLoggerFields(t *testing.T) {
	existing := log.Fields{"requestID": "req-1"}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, existing)

	ctx = WithUpgradeCorrelationContext(ctx, "corr-123")

	fields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields)
	require.True(t, ok)
	assert.Equal(t, "corr-123", fields[string(middleware.RequestCorrelationID)])
	assert.Equal(t, "req-1", fields["requestID"])
	// Original map must not be mutated.
	assert.NotContains(t, existing, string(middleware.RequestCorrelationID))
}

func TestResolveUpgradeCorrelationID_FromHTTPHeader(t *testing.T) {
	header := http.Header{}
	header.Set(string(middleware.CorrelationIDName), "header-corr")
	ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, header)
	assert.Equal(t, "header-corr", ResolveUpgradeCorrelationID(ctx, "job-uuid"))
}

func TestResolveUpgradeCorrelationID_EmptyHeaderFallsBack(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, http.Header{})
	assert.Equal(t, "job-uuid", ResolveUpgradeCorrelationID(ctx, "job-uuid"))
}

func TestResolveUpgradeCorrelationID_IgnoresLoggerFieldsWithoutSynthesizing(t *testing.T) {
	existing := log.Fields{"requestID": "req-1"}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, existing)

	assert.Equal(t, "job-uuid", ResolveUpgradeCorrelationID(ctx, "job-uuid"))
	// Must not mutate logger fields or invent a correlation ID.
	assert.NotContains(t, existing, string(middleware.RequestCorrelationID))
	assert.Equal(t, "req-1", existing["requestID"])
}

func TestBuildBatchChildUpgradeCorrelationID(t *testing.T) {
	assert.Equal(t, "batch-uuid-child-cluster-uuid", BuildBatchChildUpgradeCorrelationID("batch-uuid", "cluster-uuid"))
}
