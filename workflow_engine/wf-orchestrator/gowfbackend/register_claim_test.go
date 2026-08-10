package gowfbackend

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/cschleiden/go-workflows/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// claimShortAlias keeps the first owner and warns (rather than panicking) on a
// genuine collision. The worker pointer is only used as a map key here, so a nil
// worker with a test-unique short name keeps the global registry isolated.
func TestClaimShortAlias_FirstOwnerCollisionAndIdempotency(t *testing.T) {
	var w *worker.Worker
	const short = "TestClaimShortAlias_UniqueShortName"

	assert.True(t, claimShortAlias(w, "workflow", short, "owner-1"),
		"first claim of a short alias must register")
	assert.False(t, claimShortAlias(w, "workflow", short, "owner-1"),
		"same owner re-claiming is idempotent and must not re-register")
	assert.False(t, claimShortAlias(w, "workflow", short, "owner-2"),
		"a colliding owner must be skipped (first owner kept), not panic")
}

func TestApplyActivityContextDeadline_ContextNotFirstArg(t *testing.T) {
	// The generalized scan must find the context even when it is not arg[0].
	ctx := context.WithValue(context.Background(), scheduleToCloseCtxKey{}, 50*time.Millisecond)
	args := []reflect.Value{reflect.ValueOf(42), reflect.ValueOf(ctx)}

	cancel, out := applyActivityContextDeadline(args)
	require.NotNil(t, cancel, "expected a cancel func when a schedule-to-close value is present")
	defer cancel()

	assert.Equal(t, 42, out[0].Interface(), "non-context args must be preserved")
	newCtx, ok := out[1].Interface().(context.Context)
	require.True(t, ok)
	_, has := newCtx.Deadline()
	assert.True(t, has, "the context arg must gain a deadline")
}

func e2eAttemptWorkflow(ctx *orchestrator.Context) (int32, error) {
	return orchestrator.GetInfo(ctx).Attempt, nil
}

// go-workflows does not expose a per-run attempt counter, so the gowf backend
// reports the first attempt. This drives GetInfo through a real runner.
func TestGetInfo_Attempt_DefaultsToOne(t *testing.T) {
	var got int32
	err := runE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(e2eAttemptWorkflow)
	}, e2eAttemptWorkflow, &got)
	require.NoError(t, err)
	assert.Equal(t, int32(1), got)
}
