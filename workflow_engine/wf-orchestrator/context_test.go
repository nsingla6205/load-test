package orchestrator

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewContextWithBackend_InitialState(t *testing.T) {
	b := newFakeBackend()
	ctx := NewContextWithBackend(b, "opaque")

	assert.Equal(t, "opaque", ctx.BackendCtx())
	assert.Equal(t, Backend(b), ctx.Backend())
	assert.NotNil(t, ctx.Values())
	assert.Empty(t, ctx.Values())
	assert.Nil(t, ctx.ActivityOpts())
	assert.Nil(t, ctx.ChildOpts())
	assert.Nil(t, ctx.Info())
}

func TestContext_ValueDelegatesToBackend(t *testing.T) {
	ctx, b := newFakeContext()
	b.value = "from-backend"

	assert.Equal(t, "from-backend", ctx.Value("any-key"))
	assert.Equal(t, "any-key", b.valueKey)
}

func TestContext_DoneAndErrDelegate(t *testing.T) {
	ctx, b := newFakeContext()
	b.err = errors.New("cancelled")

	var want <-chan struct{} = b.doneCh
	assert.Equal(t, want, ctx.Done())
	assert.EqualError(t, ctx.Err(), "cancelled")

	// Closing the channel should be observable through Done().
	close(b.doneCh)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("expected Done channel to be closed")
	}
}

func TestWithValue_ReturnsCopyWithValue(t *testing.T) {
	ctx, _ := newFakeContext()

	c2 := WithValue(ctx, "k", "v")
	require.NotSame(t, ctx, c2)
	assert.Equal(t, "v", c2.Value("k"))
	// Original context is unchanged.
	assert.Empty(t, ctx.Values())
}

func TestContext_WithActivityOptions(t *testing.T) {
	ctx, _ := newFakeContext()
	opts := ActivityOptions{
		ScheduleToCloseTimeout: 30 * time.Second,
		ActivityID:             "act-1",
		HeartbeatTimeout:       10 * time.Second,
		RetryPolicy:            &RetryPolicy{MaximumAttempts: 4},
	}

	c2 := ctx.WithActivityOptions(opts)
	require.NotNil(t, c2.ActivityOpts())
	assert.Equal(t, opts, *c2.ActivityOpts())
	// Original is untouched.
	assert.Nil(t, ctx.ActivityOpts())
}

func TestContext_WithChildWorkflowOptions(t *testing.T) {
	ctx, _ := newFakeContext()
	opts := ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Minute,
		WorkflowID:               "child-1",
		RetryPolicy:              &RetryPolicy{MaximumAttempts: 2},
	}

	c2 := ctx.WithChildWorkflowOptions(opts)
	require.NotNil(t, c2.ChildOpts())
	assert.Equal(t, opts, *c2.ChildOpts())
	assert.Nil(t, ctx.ChildOpts())
}

func TestNow_DelegatesToBackend(t *testing.T) {
	ctx, b := newFakeContext()
	want := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	b.now = want

	assert.Equal(t, want, Now(ctx))
}

func TestGetInfo_DelegatesToBackend(t *testing.T) {
	ctx, b := newFakeContext()
	b.info = WorkflowInfo{
		WorkflowExecutionTimeout: 5 * time.Minute,
		WorkflowStartTime:        time.Unix(1000, 0),
		WorkflowExecution:        WorkflowExecution{ID: "wf-1", RunID: "run-1"},
	}

	got := GetInfo(ctx)
	assert.Equal(t, b.info, got)
	assert.Equal(t, "wf-1", got.WorkflowExecution.ID)
	assert.Equal(t, "run-1", got.WorkflowExecution.RunID)
}

func TestSleep_DelegatesToBackend(t *testing.T) {
	ctx, b := newFakeContext()
	require.NoError(t, Sleep(ctx, 2*time.Second))
	assert.Equal(t, 2*time.Second, b.sleptFor)

	b.sleepErr = errors.New("interrupted")
	assert.EqualError(t, Sleep(ctx, time.Second), "interrupted")
}

func TestContext_ShallowCopyIndependentMutation(t *testing.T) {
	ctx, _ := newFakeContext()
	ctx.SetActivityOpts(&ActivityOptions{ActivityID: "orig"})

	cp := ctx.ShallowCopy()
	cp.SetActivityOpts(&ActivityOptions{ActivityID: "copy"})

	assert.Equal(t, "orig", ctx.ActivityOpts().ActivityID)
	assert.Equal(t, "copy", cp.ActivityOpts().ActivityID)
}

func TestContext_SettersAndGetters(t *testing.T) {
	ctx, _ := newFakeContext()

	ctx.SetBackendCtx("new-backend-ctx")
	assert.Equal(t, "new-backend-ctx", ctx.BackendCtx())

	actOpts := &ActivityOptions{ActivityID: "a"}
	ctx.SetActivityOpts(actOpts)
	assert.Equal(t, actOpts, ctx.ActivityOpts())

	childOpts := &ChildWorkflowOptions{WorkflowID: "w"}
	ctx.SetChildOpts(childOpts)
	assert.Equal(t, childOpts, ctx.ChildOpts())

	values := map[any]any{"x": 1}
	ctx.SetValues(values)
	assert.Equal(t, values, ctx.Values())
}

func TestContext_OperationBudgetCheck(t *testing.T) {
	ctx, _ := newFakeContext()
	assert.False(t, ctx.OperationBudgetExceeded())

	ctx = ctx.WithOperationBudgetCheck(func() bool { return true })
	assert.True(t, ctx.OperationBudgetExceeded())

	cleared := ctx.ClearOperationBudgetCheck()
	assert.False(t, cleared.OperationBudgetExceeded())
	assert.True(t, ctx.OperationBudgetExceeded())
}

func TestContext_WorkflowStartTime(t *testing.T) {
	ctx, b := newFakeContext()
	start := time.Unix(1700, 0)
	b.info = WorkflowInfo{WorkflowStartTime: start}
	assert.Equal(t, start, ctx.WorkflowStartTime())
}

func TestNewDisconnectedContext_DelegatesToBackend(t *testing.T) {
	ctx, _ := newFakeContext()
	ctx = ctx.WithOperationBudgetCheck(func() bool { return true })
	disconnected := NewDisconnectedContext(ctx)
	require.NotSame(t, ctx, disconnected)
	assert.True(t, disconnected.OperationBudgetExceeded())
}
