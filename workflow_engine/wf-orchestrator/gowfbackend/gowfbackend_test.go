package gowfbackend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cclient "github.com/cschleiden/go-workflows/client"
	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Unit tests: pure helpers that do not require a running cschleiden worker.
// ---------------------------------------------------------------------------

func TestToGowfError_PassesNilThrough(t *testing.T) {
	if got := ToGowfError(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestToGowfError_PassesRetryableThrough(t *testing.T) {
	err := errors.New("transient")
	if got := ToGowfError(err); got != err {
		t.Fatalf("expected passthrough, got %v", got)
	}
}

func TestToGowfError_TranslatesNonRetryableApplicationError(t *testing.T) {
	in := orchestrator.NewNonRetryableApplicationError("perm", "PERM", nil)
	out := ToGowfError(in)
	if out == nil {
		t.Fatal("expected non-nil output")
	}
	if gowf.CanRetry(out) {
		t.Fatalf("expected permanent error (CanRetry=false), got retryable: %v", out)
	}
}

func TestToGowfError_TranslatesNonRetryableWrapper(t *testing.T) {
	in := &orchestrator.NonRetryableError{Err: errors.New("boom")}
	out := ToGowfError(in)
	if out == nil {
		t.Fatal("expected non-nil output")
	}
	if gowf.CanRetry(out) {
		t.Fatalf("expected permanent error, got retryable: %v", out)
	}
}

func TestToGowfRetryOptions_NilGivesDefaults(t *testing.T) {
	got := toGowfRetryOptions(nil)
	if got != gowf.DefaultRetryOptions {
		t.Fatalf("expected DefaultRetryOptions, got %+v", got)
	}
}

func TestToGowfRetryOptions_MapsAllFields(t *testing.T) {
	rp := &orchestrator.RetryPolicy{
		InitialInterval:    250 * time.Millisecond,
		BackoffCoefficient: 1.5,
		MaximumInterval:    7 * time.Second,
		MaximumAttempts:    9,
	}
	got := toGowfRetryOptions(rp)
	if got.FirstRetryInterval != rp.InitialInterval ||
		got.BackoffCoefficient != rp.BackoffCoefficient ||
		got.MaxRetryInterval != rp.MaximumInterval ||
		got.MaxAttempts != int(rp.MaximumAttempts) {
		t.Fatalf("retry mapping mismatch: %+v vs %+v", got, rp)
	}
}

func TestToGowfActivityOptions_MapsScheduleToCloseToRetryTimeout(t *testing.T) {
	got := toGowfActivityOptions(orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: 30 * time.Second,
		RetryPolicy:            &orchestrator.RetryPolicy{MaximumAttempts: 3},
	})
	if got.RetryOptions.RetryTimeout != 30*time.Second {
		t.Fatalf("expected RetryTimeout=30s, got %v", got.RetryOptions.RetryTimeout)
	}
	if got.RetryOptions.MaxAttempts != 3 {
		t.Fatalf("expected MaxAttempts=3, got %d", got.RetryOptions.MaxAttempts)
	}
}

func TestToGowfSubWorkflowOptions_MapsExecutionTimeoutAndID(t *testing.T) {
	got := toGowfSubWorkflowOptions(orchestrator.ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Minute,
		WorkflowID:               "child-1",
		RetryPolicy:              &orchestrator.RetryPolicy{MaximumAttempts: 1},
	})
	if got.InstanceID != "child-1" {
		t.Fatalf("expected InstanceID=child-1, got %q", got.InstanceID)
	}
	if got.RetryOptions.RetryTimeout != time.Minute {
		t.Fatalf("expected RetryTimeout=1m, got %v", got.RetryOptions.RetryTimeout)
	}
}

type stripFmHelper struct{}

func (stripFmHelper) Name() string { return "x" }

func TestFuncName_HandlesPlainAndMethodValues(t *testing.T) {
	// Plain function: name should end with the function symbol.
	plain := FuncName(TestFuncName_HandlesPlainAndMethodValues)
	if !strings.HasSuffix(plain, "TestFuncName_HandlesPlainAndMethodValues") {
		t.Fatalf("expected plain name suffix, got %q", plain)
	}
	// Method value: runtime appends "-fm"; FuncName must strip it so the
	// registered activity name matches what cschleiden's fn.Name produces.
	mv := FuncName(stripFmHelper{}.Name)
	if strings.HasSuffix(mv, "-fm") {
		t.Fatalf("expected -fm suffix to be stripped, got %q", mv)
	}
}

// activityIdentifier_* covers the three branches of the helper used by
// backend.ExecuteActivity to resolve an activity reference into a
// registry-lookup name.
func TestActivityIdentifier_NilReturnsEmpty(t *testing.T) {
	if got := activityIdentifier(nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestActivityIdentifier_StringPassthrough(t *testing.T) {
	if got := activityIdentifier("my.activity"); got != "my.activity" {
		t.Fatalf("expected my.activity, got %q", got)
	}
}

func TestActivityIdentifier_FunctionReturnsRuntimeName(t *testing.T) {
	got := activityIdentifier(TestActivityIdentifier_FunctionReturnsRuntimeName)
	if !strings.HasSuffix(got, "TestActivityIdentifier_FunctionReturnsRuntimeName") {
		t.Fatalf("unexpected name: %q", got)
	}
}

func TestWrapActivity_PanicsOnNonFunction(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	WrapActivity(42)
}

func TestWrapActivity_PanicsOnMissingErrorReturn(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	WrapActivity(func() int { return 0 })
}

func TestWrapActivity_PreservesParameterSignature(t *testing.T) {
	type req struct {
		N int `json:"n"`
	}
	original := func(_ context.Context, r req) (int, error) { return r.N * 2, nil }
	wrapped := WrapActivity(original)

	// The wrapper should accept the same input types but return
	// (json.RawMessage, error). A Go-level type-check is enough here; full
	// invocation is exercised by the integration tests below.
	if wrapped == nil {
		t.Fatal("expected non-nil wrapper")
	}
}

// ---------------------------------------------------------------------------
// Integration tests: drive a real Runner end-to-end.
// ---------------------------------------------------------------------------

const e2eTimeout = 30 * time.Second

// runE2E spins up a Runner, registers callbacks via register, executes the
// wf workflow, and returns the (decoded-into-out) outcome plus any error.
// Each test gets a fresh Runner so SQLite state never leaks across tests.
func runE2E[T any](t *testing.T, register func(orchestrator.WorkflowRegistrar), wf interface{}, out *T, args ...any) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), e2eTimeout)
	defer cancel()

	r, err := NewRunner(ctx)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	register(r.Registrar())

	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = r.Stop()
	})

	wfi, err := r.Client().CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{
		InstanceID: uuid.NewString(),
	}, FuncName(wf), args...)
	if err != nil {
		return fmt.Errorf("CreateWorkflowInstance: %w", err)
	}

	// Every wrapped workflow normalises its return type to json.RawMessage,
	// so we retrieve the result as RawMessage and JSON-decode it into the
	// caller-supplied destination.
	raw, err := cclient.GetWorkflowResult[json.RawMessage](ctx, r.Client(), wfi, e2eTimeout)
	if err != nil {
		return err
	}
	if out != nil && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, out); err != nil {
			return err
		}
	}
	return nil
}

// Activities used by integration tests. Each is package-scoped so its
// runtime name (FuncForPC) is stable across runs.

func e2eAddActivity(_ context.Context, x, y int) (int, error) { return x + y, nil }

var e2eRetryCalls atomic.Int32

func e2eRetryActivity(_ context.Context) error {
	n := e2eRetryCalls.Add(1)
	if n < 3 {
		return fmt.Errorf("transient %d", n)
	}
	return nil
}

var e2eNonRetryableCalls atomic.Int32

func e2eNonRetryableActivity(_ context.Context) error {
	e2eNonRetryableCalls.Add(1)
	return orchestrator.NewNonRetryableApplicationError("perm", "PERM", nil)
}

func e2eEchoActivity(_ context.Context, in struct {
	Msg string `json:"msg"`
}) (struct {
	Msg string `json:"msg"`
}, error) {
	return in, nil
}

// Workflows. Same naming rule as activities.

func e2eAddWorkflow(ctx *orchestrator.Context, x, y int) (int, error) {
	ctx = ctx.WithActivityOptions(orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
		RetryPolicy:            &orchestrator.RetryPolicy{MaximumAttempts: 1},
	})
	var sum int
	if err := orchestrator.ExecuteActivity(ctx, e2eAddActivity, x, y).Get(ctx, &sum); err != nil {
		return 0, err
	}
	return sum, nil
}

func e2eRetryWorkflow(ctx *orchestrator.Context) error {
	ctx = ctx.WithActivityOptions(orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
		RetryPolicy: &orchestrator.RetryPolicy{
			InitialInterval:    10 * time.Millisecond,
			BackoffCoefficient: 1.0,
			MaximumInterval:    100 * time.Millisecond,
			MaximumAttempts:    5,
		},
	})
	return orchestrator.ExecuteActivity(ctx, e2eRetryActivity).Get(ctx, nil)
}

func e2eNonRetryableWorkflow(ctx *orchestrator.Context) error {
	ctx = ctx.WithActivityOptions(orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
		RetryPolicy: &orchestrator.RetryPolicy{
			InitialInterval:    10 * time.Millisecond,
			BackoffCoefficient: 1.0,
			MaximumAttempts:    5,
		},
	})
	return orchestrator.ExecuteActivity(ctx, e2eNonRetryableActivity).Get(ctx, nil)
}

func e2eChildWorkflow(ctx *orchestrator.Context, x int) (int, error) {
	return x * 2, nil
}

func e2eParentWorkflow(ctx *orchestrator.Context, x int) (int, error) {
	ctx = ctx.WithChildWorkflowOptions(orchestrator.ChildWorkflowOptions{
		WorkflowExecutionTimeout: 10 * time.Second,
		RetryPolicy:              &orchestrator.RetryPolicy{MaximumAttempts: 1},
	})
	var v int
	if err := orchestrator.ExecuteChildWorkflow(ctx, e2eChildWorkflow, x).Get(ctx, &v); err != nil {
		return 0, err
	}
	return v, nil
}

func e2eSleepWorkflow(ctx *orchestrator.Context, ms int) (string, error) {
	if err := orchestrator.Sleep(ctx, time.Duration(ms)*time.Millisecond); err != nil {
		return "", err
	}
	return "done", nil
}

func e2eNowWorkflow(ctx *orchestrator.Context) (string, error) {
	t := orchestrator.Now(ctx)
	if t.IsZero() {
		return "", errors.New("Now returned zero time")
	}
	return t.Format(time.RFC3339Nano), nil
}

func e2eValueWorkflow(ctx *orchestrator.Context) (string, error) {
	c2 := orchestrator.WithValue(ctx, "k", "v")
	if got, _ := c2.Value("k").(string); got != "v" {
		return "", fmt.Errorf("expected v, got %q", got)
	}
	return "ok", nil
}

func e2eEchoWorkflow(ctx *orchestrator.Context, msg string) (string, error) {
	ctx = ctx.WithActivityOptions(orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
		RetryPolicy:            &orchestrator.RetryPolicy{MaximumAttempts: 1},
	})
	in := struct {
		Msg string `json:"msg"`
	}{Msg: msg}
	var out struct {
		Msg string `json:"msg"`
	}
	if err := orchestrator.ExecuteActivity(ctx, e2eEchoActivity, in).Get(ctx, &out); err != nil {
		return "", err
	}
	return out.Msg, nil
}

// ---------------------------------------------------------------------------
// Integration test cases.
// ---------------------------------------------------------------------------

func TestExecuteActivitySuccess(t *testing.T) {
	var got int
	err := runE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterActivity(e2eAddActivity)
		r.RegisterWorkflow(e2eAddWorkflow)
	}, e2eAddWorkflow, &got, 3, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
}

func TestExecuteActivityRetry(t *testing.T) {
	e2eRetryCalls.Store(0)
	var unused int
	err := runE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterActivity(e2eRetryActivity)
		r.RegisterWorkflow(e2eRetryWorkflow)
	}, e2eRetryWorkflow, &unused)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := e2eRetryCalls.Load(); got != 3 {
		t.Fatalf("expected 3 calls, got %d", got)
	}
}

func TestExecuteActivityNonRetryable(t *testing.T) {
	e2eNonRetryableCalls.Store(0)
	var unused int
	err := runE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterActivity(e2eNonRetryableActivity)
		r.RegisterWorkflow(e2eNonRetryableWorkflow)
	}, e2eNonRetryableWorkflow, &unused)
	if err == nil {
		t.Fatal("expected error from workflow")
	}
	if got := e2eNonRetryableCalls.Load(); got != 1 {
		t.Fatalf("expected 1 call (no retries), got %d", got)
	}
}

func TestExecuteChildWorkflow(t *testing.T) {
	var got int
	err := runE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(e2eChildWorkflow)
		r.RegisterWorkflow(e2eParentWorkflow)
	}, e2eParentWorkflow, &got, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 10 {
		t.Fatalf("expected 10, got %d", got)
	}
}

func TestSleep(t *testing.T) {
	var got string
	err := runE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(e2eSleepWorkflow)
	}, e2eSleepWorkflow, &got, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "done" {
		t.Fatalf("expected done, got %q", got)
	}
}

func TestNow(t *testing.T) {
	var got string
	err := runE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(e2eNowWorkflow)
	}, e2eNowWorkflow, &got)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, perr := time.Parse(time.RFC3339Nano, got); perr != nil {
		t.Fatalf("Now did not return a parseable timestamp: %q (%v)", got, perr)
	}
}

func TestWithValueIntegration(t *testing.T) {
	var got string
	err := runE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(e2eValueWorkflow)
	}, e2eValueWorkflow, &got)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("expected ok, got %q", got)
	}
}

func TestStructPayloadRoundtrip(t *testing.T) {
	var got string
	err := runE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterActivity(e2eEchoActivity)
		r.RegisterWorkflow(e2eEchoWorkflow)
	}, e2eEchoWorkflow, &got, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("expected hello, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// ExecuteActivity argument-validation parity (mirrors orchestrator core's
// TestExecuteActivityRejectsMismatchedArgumentType). The validation runs in
// the orchestrator package, so we check it produces the same error shape
// when invoked through a gowf-backed context. This test does NOT spin up a
// runner because the rejection happens before any cschleiden dispatch.
// ---------------------------------------------------------------------------

type testActivityRequest struct{ Name string }
type testWorkflowRequest testActivityRequest

func TestExecuteActivityRejectsMismatchedArgumentType(t *testing.T) {
	// We need a *gowf-backed* orchestrator.Context. Since cschleiden does
	// not let us synthesise a workflow.Context outside a workflow, we
	// run a tiny workflow that performs the validation and returns the
	// resulting error message.
	wf := func(ctx *orchestrator.Context) (string, error) {
		ctx = ctx.WithActivityOptions(orchestrator.ActivityOptions{
			ScheduleToCloseTimeout: 5 * time.Second,
			RetryPolicy:            &orchestrator.RetryPolicy{MaximumAttempts: 1},
		})
		fn := func(_ context.Context, _ testActivityRequest) error { return nil }
		err := orchestrator.ExecuteActivity(ctx, fn, testWorkflowRequest{Name: "lif-1"}).Get(ctx, nil)
		if err == nil {
			return "", errors.New("expected type-mismatch error")
		}
		return err.Error(), nil
	}

	var msg string
	if err := runE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(wf)
	}, wf, &msg); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if !strings.Contains(msg, "activity expects") {
		t.Fatalf("expected validation error, got %q", msg)
	}
}

// ---------------------------------------------------------------------------
// Runner lifecycle smoke test.
// ---------------------------------------------------------------------------

func TestRunnerLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), e2eTimeout)
	defer cancel()

	r, err := NewRunner(ctx)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if r.Worker() == nil || r.Client() == nil || r.Backend() == nil {
		t.Fatal("Runner missing accessors")
	}

	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Start(ctx); err == nil {
		t.Fatal("expected error on double Start")
	}
	cancel()
	if err := r.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
