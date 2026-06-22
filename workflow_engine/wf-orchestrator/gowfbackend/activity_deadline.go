package gowfbackend

import (
	"context"
	"reflect"
	"strconv"
	"time"

	"github.com/cschleiden/go-workflows/backend"
	gowf "github.com/cschleiden/go-workflows/workflow"
)

// scheduleToCloseMetadataKey is the wire key written into activity metadata
// so the activity worker can reconstruct ScheduleToCloseTimeout on the Go
// context. Temporal sets this via the SDK; cschleiden does not.
const scheduleToCloseMetadataKey = "scheduleToCloseTimeoutNanos"

// scheduleToCloseCtxKey is the context value key used on the activity side
// after metadata extract (and optionally stamped on the workflow ctx at
// schedule time in ExecuteActivity).
type scheduleToCloseCtxKey struct{}

// stampScheduleToCloseOnWorkflow attaches ScheduleToCloseTimeout to the
// cschleiden workflow context so InjectFromWorkflow can copy it into activity
// metadata.
func stampScheduleToCloseOnWorkflow(wf gowf.Context, timeout time.Duration) gowf.Context {
	if timeout <= 0 {
		return wf
	}
	return gowf.WithValue(wf, scheduleToCloseCtxKey{}, timeout)
}

func injectScheduleToCloseFromWorkflow(ctx gowf.Context, m *gowf.Metadata) {
	if m == nil {
		return
	}
	v := ctx.Value(scheduleToCloseCtxKey{})
	d, ok := v.(time.Duration)
	if !ok || d <= 0 {
		return
	}
	m.Set(scheduleToCloseMetadataKey, strconv.FormatInt(d.Nanoseconds(), 10))
}

func extractScheduleToCloseToContext(ctx context.Context, m *gowf.Metadata) context.Context {
	if m == nil {
		return ctx
	}
	raw := m.Get(scheduleToCloseMetadataKey)
	if raw == "" {
		return ctx
	}
	nanos, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || nanos <= 0 {
		return ctx
	}
	return context.WithValue(ctx, scheduleToCloseCtxKey{}, time.Duration(nanos))
}

// applyActivityContextDeadline ensures the first context.Context argument has a
// deadline when lifecycle-manager long-poll activities run under go-workflows.
// If the context already has a deadline (e.g. tests, or a future engine that
// sets one), it is left unchanged.
func applyActivityContextDeadline(args []reflect.Value) (context.CancelFunc, []reflect.Value) {
	if len(args) == 0 || !args[0].IsValid() {
		return nil, args
	}
	ctx, ok := args[0].Interface().(context.Context)
	if !ok {
		return nil, args
	}
	if _, has := ctx.Deadline(); has {
		return nil, args
	}
	d, ok := ctx.Value(scheduleToCloseCtxKey{}).(time.Duration)
	if !ok || d <= 0 {
		return nil, args
	}
	ctxWith, cancel := context.WithTimeout(ctx, d)
	args[0] = reflect.ValueOf(ctxWith)
	return cancel, args
}

// DefaultBackendOptions returns backend options required for lifecycle-manager
// workflows on go-workflows: correlation/deployment propagation plus
// schedule-to-close propagation for activities.
func DefaultBackendOptions() []backend.BackendOption {
	return []backend.BackendOption{
		backend.WithContextPropagator(NewContextMapPropagator()),
	}
}
