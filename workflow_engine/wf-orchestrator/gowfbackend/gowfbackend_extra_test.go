package gowfbackend

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/cschleiden/go-workflows/backend/sqlite"
	gowf "github.com/cschleiden/go-workflows/workflow"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// ---------------------------------------------------------------------------
// backend.go / continue_as_new.go pure helpers
// ---------------------------------------------------------------------------

func TestNewBackend_ReturnsGowfBackend(t *testing.T) {
	b := NewBackend()
	if b == nil {
		t.Fatal("expected non-nil backend")
	}
	if !IsGoWorkflowsBackend(b) {
		t.Fatal("NewBackend must be detected as the go-workflows backend")
	}
}

func TestGowfBackend_NewDisconnectedContext(t *testing.T) {
	b := NewBackend()
	ctx := orchestrator.NewContextWithBackend(b, context.Background())
	ctx = ctx.WithOperationBudgetCheck(func() bool { return true })

	disconnected := b.NewDisconnectedContext(ctx)
	if disconnected == nil {
		t.Fatal("expected non-nil disconnected context")
	}
	if disconnected == ctx {
		t.Fatal("expected a shallow copy, not the same pointer")
	}
	if !disconnected.OperationBudgetExceeded() {
		t.Fatal("disconnected context should preserve operation budget state")
	}
}

type notGowfBackend struct{ orchestrator.Backend }

func TestIsGoWorkflowsBackend_FalseForOther(t *testing.T) {
	if IsGoWorkflowsBackend(notGowfBackend{}) {
		t.Fatal("expected false for non-gowf backend")
	}
}

func TestIsContinueAsNewError(t *testing.T) {
	if isContinueAsNewError(nil) {
		t.Fatal("nil must not be a continue-as-new error")
	}
	if isContinueAsNewError(errors.New("plain")) {
		t.Fatal("plain error must not be a continue-as-new error")
	}
}

func TestDispatchName(t *testing.T) {
	if got := dispatchName(nil); got != "" {
		t.Fatalf("nil -> %q, want empty", got)
	}
	if got := dispatchName("explicit"); got != "explicit" {
		t.Fatalf("string passthrough -> %q", got)
	}
	got := dispatchName(TestDispatchName)
	if !strings.HasSuffix(got, "TestDispatchName") {
		t.Fatalf("func -> %q, want suffix TestDispatchName", got)
	}
}

// ---------------------------------------------------------------------------
// future.go
// ---------------------------------------------------------------------------

func TestReadyFutureErr_GetReturnsError(t *testing.T) {
	want := errors.New("preresolved")
	f := readyFutureErr(want)
	if got := f.Get(nil, nil); got != want {
		t.Fatalf("Get -> %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// register.go name helpers
// ---------------------------------------------------------------------------

func TestFuncName_NilAndNonFunc(t *testing.T) {
	if got := FuncName(nil); got != "" {
		t.Fatalf("nil -> %q", got)
	}
	if got := FuncName(42); got != "" {
		t.Fatalf("non-func -> %q", got)
	}
}

func TestShortFuncName(t *testing.T) {
	if got := ShortFuncName(nil); got != "" {
		t.Fatalf("nil -> %q", got)
	}
	got := ShortFuncName(TestShortFuncName)
	if got != "TestShortFuncName" {
		t.Fatalf("short name -> %q", got)
	}
}

func TestActivityIdentifier(t *testing.T) {
	if got := activityIdentifier(nil); got != "" {
		t.Fatalf("nil -> %q", got)
	}
	if got := activityIdentifier("name"); got != "name" {
		t.Fatalf("string -> %q", got)
	}
	got := activityIdentifier(TestActivityIdentifier)
	if !strings.HasSuffix(got, "TestActivityIdentifier") {
		t.Fatalf("func -> %q", got)
	}
}

// ---------------------------------------------------------------------------
// register.go wrapper builders + panics
// ---------------------------------------------------------------------------

func TestBuildWorkflowWrapper_Valid(t *testing.T) {
	w := BuildWorkflowWrapper(func(_ *orchestrator.Context, n int) (int, error) { return n, nil })
	if w == nil {
		t.Fatal("expected non-nil wrapper")
	}
}

func TestBuildWorkflowWrapper_Panics(t *testing.T) {
	cases := []struct {
		name string
		fn   interface{}
	}{
		{"non-function", 42},
		{"wrong first param", func(_ context.Context) error { return nil }},
		{"no error return", func(_ *orchestrator.Context) int { return 0 }},
		{"too many returns", func(_ *orchestrator.Context) (int, int, error) { return 0, 0, nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected panic")
				}
			}()
			BuildWorkflowWrapper(tc.fn)
		})
	}
}

// ---------------------------------------------------------------------------
// wrap.go generic wrappers (smoke: outer body returns a closure)
// ---------------------------------------------------------------------------

func TestGenericWrappers_ReturnNonNil(t *testing.T) {
	if WrapWorkflow0(func(_ *orchestrator.Context) (int, error) { return 0, nil }) == nil {
		t.Fatal("WrapWorkflow0")
	}
	if WrapWorkflow0Err(func(_ *orchestrator.Context) error { return nil }) == nil {
		t.Fatal("WrapWorkflow0Err")
	}
	if WrapWorkflow1(func(_ *orchestrator.Context, _ int) (int, error) { return 0, nil }) == nil {
		t.Fatal("WrapWorkflow1")
	}
	if WrapWorkflow1Err(func(_ *orchestrator.Context, _ int) error { return nil }) == nil {
		t.Fatal("WrapWorkflow1Err")
	}
	if WrapWorkflow2(func(_ *orchestrator.Context, _ int, _ int) (int, error) { return 0, nil }) == nil {
		t.Fatal("WrapWorkflow2")
	}
	if WrapWorkflow2Err(func(_ *orchestrator.Context, _ int, _ int) error { return nil }) == nil {
		t.Fatal("WrapWorkflow2Err")
	}
}

// ---------------------------------------------------------------------------
// activitywrap.go marshalResult
// ---------------------------------------------------------------------------

func TestMarshalResult(t *testing.T) {
	raw, err := marshalResult(nil)
	if err != nil || raw != nil {
		t.Fatalf("nil -> (%v, %v), want (nil, nil)", raw, err)
	}

	raw, err = marshalResult(map[string]int{"a": 1})
	if err != nil {
		t.Fatalf("valid -> unexpected error %v", err)
	}
	if string(raw) != `{"a":1}` {
		t.Fatalf("valid -> %s", raw)
	}

	if _, err = marshalResult(make(chan int)); err == nil {
		t.Fatal("channel must produce a marshal error")
	}
}

// ---------------------------------------------------------------------------
// activity_deadline.go
// ---------------------------------------------------------------------------

func TestApplyActivityContextDeadline_EmptyArgs(t *testing.T) {
	cancel, out := applyActivityContextDeadline(nil)
	if cancel != nil || out != nil {
		t.Fatal("empty args must be returned unchanged")
	}
}

func TestApplyActivityContextDeadline_NonContextArg(t *testing.T) {
	args := []reflect.Value{reflect.ValueOf(42)}
	cancel, out := applyActivityContextDeadline(args)
	if cancel != nil {
		t.Fatal("expected nil cancel for non-context arg")
	}
	if out[0].Interface() != 42 {
		t.Fatal("args must be returned unchanged")
	}
}

func TestApplyActivityContextDeadline_AlreadyHasDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	args := []reflect.Value{reflect.ValueOf(ctx)}
	c, _ := applyActivityContextDeadline(args)
	if c != nil {
		t.Fatal("must not add a deadline when one already exists")
	}
}

func TestApplyActivityContextDeadline_NoValueNoDeadline(t *testing.T) {
	args := []reflect.Value{reflect.ValueOf(context.Background())}
	c, _ := applyActivityContextDeadline(args)
	if c != nil {
		t.Fatal("must not add a deadline without the schedule-to-close value")
	}
}

func TestApplyActivityContextDeadline_AppliesDeadlineFromValue(t *testing.T) {
	ctx := context.WithValue(context.Background(), scheduleToCloseCtxKey{}, 50*time.Millisecond)
	args := []reflect.Value{reflect.ValueOf(ctx)}
	cancel, out := applyActivityContextDeadline(args)
	if cancel == nil {
		t.Fatal("expected a cancel func")
	}
	defer cancel()
	newCtx, ok := out[0].Interface().(context.Context)
	if !ok {
		t.Fatal("first arg should still be a context")
	}
	if _, has := newCtx.Deadline(); !has {
		t.Fatal("expected a deadline to be applied")
	}
}

func TestExtractScheduleToCloseToContext(t *testing.T) {
	base := context.Background()

	if got := extractScheduleToCloseToContext(base, nil); got != base {
		t.Fatal("nil metadata must return ctx unchanged")
	}

	empty := gowf.Metadata{}
	if got := extractScheduleToCloseToContext(base, &empty); got != base {
		t.Fatal("empty metadata must return ctx unchanged")
	}

	invalid := gowf.Metadata{scheduleToCloseMetadataKey: "not-a-number"}
	if got := extractScheduleToCloseToContext(base, &invalid); got != base {
		t.Fatal("invalid metadata must return ctx unchanged")
	}

	valid := gowf.Metadata{scheduleToCloseMetadataKey: strconv.FormatInt((2 * time.Second).Nanoseconds(), 10)}
	got := extractScheduleToCloseToContext(base, &valid)
	d, ok := got.Value(scheduleToCloseCtxKey{}).(time.Duration)
	if !ok || d != 2*time.Second {
		t.Fatalf("expected 2s on ctx, got %v (ok=%v)", d, ok)
	}
}

func TestInjectScheduleToCloseFromWorkflow_NilMetadata(t *testing.T) {
	// m == nil short-circuits before the workflow context is read, so a nil
	// context is safe here.
	injectScheduleToCloseFromWorkflow(nil, nil)
}

func TestDefaultBackendOptions_NonEmpty(t *testing.T) {
	if len(DefaultBackendOptions()) == 0 {
		t.Fatal("expected at least one backend option")
	}
}

// ---------------------------------------------------------------------------
// runner.go options + lifecycle
// ---------------------------------------------------------------------------

func TestNewRunner_WithBackendAndWorkerOptions(t *testing.T) {
	ctx := context.Background()
	b := sqlite.NewInMemoryBackend()

	r, err := NewRunner(ctx, WithBackend(b), WithWorkerOptions(nil))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if r.Backend() != b {
		t.Fatal("WithBackend must install the supplied backend")
	}

	// Stop without Start still closes the backend cleanly.
	if err := r.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestDiagnosticsHandler(t *testing.T) {
	r, err := NewRunner(context.Background())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop() })

	h, err := r.DiagnosticsHandler()
	if err != nil {
		t.Fatalf("DiagnosticsHandler: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil diagnostics handler")
	}
}

func TestDiagnosticsHandlerUnsupportedBackend(t *testing.T) {
	type noDiagBackend struct{ cbackend.Backend }
	r, err := NewRunner(context.Background(), WithBackend(noDiagBackend{
		Backend: sqlite.NewInMemoryBackend(),
	}))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop() })

	h, err := r.DiagnosticsHandler()
	if err == nil {
		t.Fatal("expected error for backend without diagnostics support")
	}
	if h != nil {
		t.Fatal("expected nil handler when diagnostics are unsupported")
	}
}

// ---------------------------------------------------------------------------
// registrar.go RegisterWorkflowWithName via a real (unstarted) worker
// ---------------------------------------------------------------------------

func extraRegNamedWorkflow(_ *orchestrator.Context) error { return nil }

func TestGowfRegistrar_RegisterWorkflowWithName(t *testing.T) {
	r, err := NewRunner(context.Background())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop() })

	reg := NewRegistrar(r.Worker())
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("RegisterWorkflowWithName panicked: %v", rec)
		}
	}()
	reg.RegisterWorkflowWithName("vlm.ExtraNamed", extraRegNamedWorkflow)
}

// ---------------------------------------------------------------------------
// ctx_propagator.go context-variant Inject/Extract + keyMap
// ---------------------------------------------------------------------------

func TestKeyMap_HasExpectedKeys(t *testing.T) {
	m := keyMap()
	if _, ok := m["correlationID"]; !ok {
		t.Fatal("missing correlationID")
	}
	if _, ok := m["deploymentID"]; !ok {
		t.Fatal("missing deploymentID")
	}
}

func TestContextMapPropagator_ContextVariants_RoundTrip(t *testing.T) {
	p := NewContextMapPropagator()

	ctx := context.WithValue(context.Background(), orchestrator.CorrelationIDKey, "corr-1")
	ctx = context.WithValue(ctx, orchestrator.DeploymentIDKey, "dep-1")

	m := gowf.Metadata{}
	if err := p.Inject(ctx, &m); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if m.Get("correlationID") != "corr-1" || m.Get("deploymentID") != "dep-1" {
		t.Fatalf("Inject did not set metadata: %v", m)
	}

	out, err := p.Extract(context.Background(), &m)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out.Value(orchestrator.CorrelationIDKey) != "corr-1" {
		t.Fatalf("Extract lost correlationID: %v", out.Value(orchestrator.CorrelationIDKey))
	}
}

func TestContextMapPropagator_Inject_SkipsMissingValues(t *testing.T) {
	p := NewContextMapPropagator()
	m := gowf.Metadata{}
	if err := p.Inject(context.Background(), &m); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("expected empty metadata, got %v", m)
	}
}

// ---------------------------------------------------------------------------
// Integration: runtime-only backend paths (Err, GetInfo child branch, Value
// fallback) and the workflow-context propagator variants. Driven through a
// real cschleiden runner via the runE2E helper from gowfbackend_test.go.
// ---------------------------------------------------------------------------

func e2eBackendExtrasWorkflow(ctx *orchestrator.Context) (string, error) {
	// Child options make GetInfo populate WorkflowExecutionTimeout.
	childCtx := ctx.WithChildWorkflowOptions(orchestrator.ChildWorkflowOptions{
		WorkflowExecutionTimeout: 5 * time.Second,
	})
	if info := orchestrator.GetInfo(childCtx); info.WorkflowExecutionTimeout != 5*time.Second {
		return "", errors.New("expected child workflow timeout on GetInfo")
	}

	// Err must be nil for a healthy workflow context.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Value fallback: a key absent from the local values map delegates to the
	// underlying cschleiden context (returns nil).
	if v := ctx.Value("absent-key"); v != nil {
		return "", errors.New("expected nil for absent key")
	}

	// Workflow-context propagator variants.
	p := NewContextMapPropagator()
	wf := gowf.WithValue(wfCtx(ctx), orchestrator.CorrelationIDKey, "corr-wf")
	m := gowf.Metadata{}
	if err := p.InjectFromWorkflow(wf, &m); err != nil {
		return "", err
	}
	if m.Get("correlationID") != "corr-wf" {
		return "", errors.New("InjectFromWorkflow did not propagate correlationID")
	}
	if _, err := p.ExtractToWorkflow(wf, &m); err != nil {
		return "", err
	}
	return "ok", nil
}

func TestBackendExtras_ViaRunner(t *testing.T) {
	var got string
	err := runE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(e2eBackendExtrasWorkflow)
	}, e2eBackendExtrasWorkflow, &got)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("expected ok, got %q", got)
	}
}
