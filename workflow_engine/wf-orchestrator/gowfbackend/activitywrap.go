package gowfbackend

import (
	"encoding/json"
	"errors"
	"reflect"
)

// rawMessageType, errorType, and emptyRaw are kept as package-level cached
// reflect.Types so we do not pay the lookup cost on every wrapped invocation.
var (
	rawMessageType = reflect.TypeOf(json.RawMessage(nil))
	errorType      = reflect.TypeOf((*error)(nil)).Elem()
)

// WrapActivity returns a function with the same input parameter list as fn
// but with a normalised return signature of (json.RawMessage, error).
//
// Why json.RawMessage: orchestrator.Backend.ExecuteActivity is type-erased
// (returns Future, not Future[T]), but cschleiden's ExecuteActivity is
// generic in its result type. By forcing every wrapped activity to return
// json.RawMessage, the backend can call cschleidenwf.ExecuteActivity[json.RawMessage]
// uniformly and let gowfFuture.Get JSON-decode the bytes into whatever
// concrete type the workflow expects.
//
// The wrapper also routes the activity's returned error through ToGowfError,
// translating orchestrator's NonRetryable markers into cschleiden's
// permanent-error sentinel so its retry loop short-circuits correctly.
//
// Panic: WrapActivity panics if fn is not a function whose final return
// value implements error. This mirrors cschleiden's checkActivity contract
// and surfaces registration mistakes immediately instead of at first invocation.
func WrapActivity(fn interface{}) interface{} {
	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()

	if fnType.Kind() != reflect.Func {
		panic("gowfbackend.WrapActivity: argument must be a function")
	}
	if fnType.NumOut() == 0 || !fnType.Out(fnType.NumOut()-1).Implements(errorType) {
		panic("gowfbackend.WrapActivity: function must return error as last return value")
	}
	if fnType.NumOut() > 2 {
		panic("gowfbackend.WrapActivity: function must return at most (result, error)")
	}

	inTypes := make([]reflect.Type, fnType.NumIn())
	for i := 0; i < fnType.NumIn(); i++ {
		inTypes[i] = fnType.In(i)
	}
	outTypes := []reflect.Type{rawMessageType, errorType}
	wrappedType := reflect.FuncOf(inTypes, outTypes, fnType.IsVariadic())

	wrapped := reflect.MakeFunc(wrappedType, func(args []reflect.Value) []reflect.Value {
		cancel, args := applyActivityContextDeadline(args)
		if cancel != nil {
			defer cancel()
		}

		var callOut []reflect.Value
		if fnType.IsVariadic() {
			callOut = fnVal.CallSlice(args)
		} else {
			callOut = fnVal.Call(args)
		}

		// Extract the (optional) result and the trailing error.
		var (
			rawOut reflect.Value
			errOut reflect.Value
		)
		errIdx := len(callOut) - 1
		errVal, _ := callOut[errIdx].Interface().(error)

		if errVal != nil {
			rawOut = reflect.Zero(rawMessageType)
			errOut = reflect.ValueOf(ToGowfError(errVal))
			if !errOut.IsValid() {
				errOut = reflect.Zero(errorType)
			} else {
				// Re-box into the error interface type so the outgoing
				// signature matches exactly.
				errOut = errOut.Convert(errorType)
			}
			return []reflect.Value{rawOut, errOut}
		}

		// Success: marshal the result (if any) to JSON.
		var resultIface interface{}
		if len(callOut) == 2 && callOut[0].IsValid() && callOut[0].CanInterface() {
			resultIface = callOut[0].Interface()
		}

		raw, marshalErr := marshalResult(resultIface)
		if marshalErr != nil {
			rawOut = reflect.Zero(rawMessageType)
			errOut = reflect.ValueOf(marshalErr).Convert(errorType)
			return []reflect.Value{rawOut, errOut}
		}

		rawOut = reflect.ValueOf(raw)
		errOut = reflect.Zero(errorType)
		return []reflect.Value{rawOut, errOut}
	})

	return wrapped.Interface()
}

// marshalResult JSON-encodes a value, with two refinements over a plain
// json.Marshal:
//   - a nil result yields a nil RawMessage (avoids "null" leaking through
//     and being decoded into unexpected zero-values at the workflow side)
//   - any encoding error is wrapped so the cause is greppable in worker logs
func marshalResult(v interface{}) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, errors.New("gowfbackend: failed to marshal activity result: " + err.Error())
	}
	return b, nil
}
