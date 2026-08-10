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
	if ctx == nil || m == nil {
		return
	}
	d, ok := ctx.Value(scheduleToCloseCtxKey{}).(time.Duration)
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

// applyActivityContextDeadline ensures the activity's context.Context argument
// has a deadline when lifecycle-manager long-poll activities run under
// go-workflows. It scans every argument for the first context.Context rather
// than assuming it is arg[0], so activities with a non-standard argument layout
// are still bounded. If the context already has a deadline (e.g. tests, or a
// future engine that sets one), it is left unchanged.
func applyActivityContextDeadline(args []reflect.Value) (context.CancelFunc, []reflect.Value) {
	for i := range args {
		if !args[i].IsValid() {
			continue
		}
		c, ok := args[i].Interface().(context.Context)
		if !ok || c == nil {
			continue
		}
		if _, hasDeadline := c.Deadline(); hasDeadline {
			return nil, args
		}
		d, ok := c.Value(scheduleToCloseCtxKey{}).(time.Duration)
		if !ok || d <= 0 {
			return nil, args
		}
		newCtx, cancel := context.WithTimeout(c, d)
		newArgs := make([]reflect.Value, len(args))
		copy(newArgs, args)
		newArgs[i] = reflect.ValueOf(newCtx)
		return cancel, newArgs
	}
	return nil, args
}

// DefaultBackendOptions returns backend options required for lifecycle-manager
// workflows on go-workflows: correlation/deployment propagation plus
// schedule-to-close propagation for activities.
func DefaultBackendOptions() []backend.BackendOption {
	return []backend.BackendOption{
		backend.WithContextPropagator(NewContextMapPropagator()),
	}
}
