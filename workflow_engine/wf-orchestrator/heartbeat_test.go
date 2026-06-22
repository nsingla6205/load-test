package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecordHeartbeat_DefaultIsNoop(t *testing.T) {
	// Ensure the default installed function does not panic with no details.
	assert.NotPanics(t, func() {
		RecordHeartbeat(context.Background())
	})
}

func TestSetHeartbeatFunc_InstallsCustomImplementation(t *testing.T) {
	orig := heartbeatFn
	t.Cleanup(func() { heartbeatFn = orig })

	var (
		gotCtx     context.Context
		gotDetails []interface{}
		called     bool
	)
	SetHeartbeatFunc(func(ctx context.Context, details ...interface{}) {
		called = true
		gotCtx = ctx
		gotDetails = details
	})

	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "v")
	RecordHeartbeat(ctx, "detail-1", 2)

	assert.True(t, called)
	assert.Equal(t, ctx, gotCtx)
	assert.Equal(t, []interface{}{"detail-1", 2}, gotDetails)
}
