package orchestrator

import "reflect"

// Future represents an asynchronous operation whose result can be retrieved
// via Get. In temporal mode it wraps a Temporal future; in library mode it
// wraps a goroutine result channel.
type Future interface {
	Get(ctx *Context, valuePtr interface{}) error
}

// futureResult holds the outcome of a local async operation.
type futureResult struct {
	value interface{}
	err   error
}

// localFuture is backed by a goroutine that sends a single result on ch.
type localFuture struct {
	ch     <-chan futureResult
	result *futureResult
}

type readyFuture struct {
	result futureResult
}

func (f *localFuture) Get(ctx *Context, valuePtr interface{}) error {
	if f.result == nil {
		select {
		case res := <-f.ch:
			f.result = &res
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.result.err != nil {
		return f.result.err
	}
	if valuePtr != nil && f.result.value != nil {
		assignResult(valuePtr, f.result.value)
	}
	return nil
}

func (f *readyFuture) Get(ctx *Context, valuePtr interface{}) error {
	// readyFuture is used for validation failures detected before any async work
	// starts, so Get simply returns the precomputed result immediately.
	if f.result.err != nil {
		return f.result.err
	}
	if valuePtr != nil && f.result.value != nil {
		assignResult(valuePtr, f.result.value)
	}
	return nil
}

// assignResult copies src into the value pointed to by dstPtr using reflection.
// dstPtr must be a non-nil pointer; src must be assignable to the pointed-to type.
func assignResult(dstPtr interface{}, src interface{}) {
	dstVal := reflect.ValueOf(dstPtr)
	if dstVal.Kind() != reflect.Ptr || dstVal.IsNil() {
		return
	}
	srcVal := reflect.ValueOf(src)
	if !srcVal.IsValid() {
		return
	}
	target := dstVal.Elem()
	if srcVal.Type().AssignableTo(target.Type()) {
		target.Set(srcVal)
	} else if srcVal.Kind() == reflect.Ptr && !srcVal.IsNil() && srcVal.Elem().Type().AssignableTo(target.Type()) {
		target.Set(srcVal.Elem())
	}
}
