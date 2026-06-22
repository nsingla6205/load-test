package orchestrator

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validActivity(_ context.Context, _ int) error            { return nil }
func variadicActivity(_ context.Context, _ ...int) error      { return nil }
func ptrArgActivity(_ context.Context, _ *int) error          { return nil }
func ifaceArgActivity(_ context.Context, _ interface{}) error { return nil }
func mixedVariadicActivity(_ context.Context, _ string, _ ...int) error {
	return nil
}

func TestExecuteActivity_ValidDelegatesToBackend(t *testing.T) {
	ctx, b := newFakeContext()
	b.activityFuture = &readyFuture{result: futureResult{value: 11}}

	fut := ExecuteActivity(ctx, validActivity, 5)
	assert.Same(t, b.activityFuture, fut)
	assert.NotNil(t, b.execActivityFn)
	require.Len(t, b.execActivityArg, 1)
	assert.Equal(t, 5, b.execActivityArg[0])
}

func TestExecuteActivity_InvalidReturnsReadyFutureError(t *testing.T) {
	ctx, b := newFakeContext()

	// Wrong arg type -> validation fails before backend dispatch.
	fut := ExecuteActivity(ctx, validActivity, "not-an-int")
	assert.Nil(t, b.execActivityFn, "backend must not be called on validation failure")

	err := fut.Get(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "activity expects")
}

func TestExecuteActivity_StringActivityDelegates(t *testing.T) {
	ctx, b := newFakeContext()
	fut := ExecuteActivity(ctx, "named.activity", "anything", 1, 2)
	assert.Same(t, b.activityFuture, fut)
	assert.Equal(t, "named.activity", b.execActivityFn)
}

func TestExecuteChildWorkflow_DelegatesToBackend(t *testing.T) {
	ctx, b := newFakeContext()
	b.childFuture = &readyFuture{result: futureResult{value: "child"}}

	fut := ExecuteChildWorkflow(ctx, "child.wf", 1, 2)
	assert.Same(t, b.childFuture, fut)
	assert.Equal(t, "child.wf", b.execChildFn)
	require.Len(t, b.execChildArgs, 2)
}

func TestValidateActivityInvocation(t *testing.T) {
	tests := []struct {
		name      string
		fn        interface{}
		args      []interface{}
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "nil activity",
			fn:        nil,
			wantErr:   true,
			errSubstr: "nil activity",
		},
		{
			name:    "string activity is allowed",
			fn:      "some.activity",
			args:    []interface{}{1, 2, 3},
			wantErr: false,
		},
		{
			name:      "non-function activity",
			fn:        42,
			wantErr:   true,
			errSubstr: "must be a function",
		},
		{
			name:      "first param not context",
			fn:        func(_ int) error { return nil },
			wantErr:   true,
			errSubstr: "first parameter must implement context.Context",
		},
		{
			name:      "zero params",
			fn:        func() error { return nil },
			wantErr:   true,
			errSubstr: "first parameter must implement context.Context",
		},
		{
			name:      "non-variadic arg count too few",
			fn:        validActivity,
			args:      []interface{}{},
			wantErr:   true,
			errSubstr: "want 1",
		},
		{
			name:      "non-variadic arg count too many",
			fn:        validActivity,
			args:      []interface{}{1, 2},
			wantErr:   true,
			errSubstr: "want 1",
		},
		{
			name:    "non-variadic valid",
			fn:      validActivity,
			args:    []interface{}{7},
			wantErr: false,
		},
		{
			name:      "arg type mismatch",
			fn:        validActivity,
			args:      []interface{}{"string-instead-of-int"},
			wantErr:   true,
			errSubstr: "activity expects",
		},
		{
			name:      "nil arg for non-nil-assignable type",
			fn:        validActivity,
			args:      []interface{}{nil},
			wantErr:   true,
			errSubstr: "not nil-assignable",
		},
		{
			name:    "nil arg for pointer type is allowed",
			fn:      ptrArgActivity,
			args:    []interface{}{nil},
			wantErr: false,
		},
		{
			name:    "nil arg for interface type is allowed",
			fn:      ifaceArgActivity,
			args:    []interface{}{nil},
			wantErr: false,
		},
		{
			name:    "variadic with zero extra args",
			fn:      variadicActivity,
			args:    []interface{}{},
			wantErr: false,
		},
		{
			name:    "variadic with valid extra args",
			fn:      variadicActivity,
			args:    []interface{}{1, 2, 3},
			wantErr: false,
		},
		{
			name:      "variadic with wrong element type",
			fn:        variadicActivity,
			args:      []interface{}{"bad"},
			wantErr:   true,
			errSubstr: "activity expects",
		},
		{
			name:      "variadic missing required leading arg",
			fn:        mixedVariadicActivity,
			args:      []interface{}{},
			wantErr:   true,
			errSubstr: "at least",
		},
		{
			name:    "variadic with required arg and no variadic values",
			fn:      mixedVariadicActivity,
			args:    []interface{}{"required"},
			wantErr: false,
		},
		{
			name:    "variadic with required arg and variadic values",
			fn:      mixedVariadicActivity,
			args:    []interface{}{"required", 1, 2},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateActivityInvocation(tt.fn, tt.args...)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errSubstr != "" {
					assert.Contains(t, err.Error(), tt.errSubstr)
				}
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestIsNilAssignableType(t *testing.T) {
	tests := []struct {
		name string
		typ  reflect.Type
		want bool
	}{
		{"interface", reflect.TypeOf((*interface{})(nil)).Elem(), true},
		{"pointer", reflect.TypeOf((*int)(nil)), true},
		{"map", reflect.TypeOf(map[string]int(nil)), true},
		{"slice", reflect.TypeOf([]int(nil)), true},
		{"func", reflect.TypeOf(func() {}), true},
		{"chan", reflect.TypeOf(make(chan int)), true},
		{"int", reflect.TypeOf(0), false},
		{"string", reflect.TypeOf(""), false},
		{"struct", reflect.TypeOf(struct{}{}), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNilAssignableType(tt.typ))
		})
	}
}

func TestActivityParamType_FixedAndVariadic(t *testing.T) {
	fixed := reflect.TypeOf(validActivity)
	// arg index 0 maps to param index 1 (int).
	assert.Equal(t, reflect.TypeOf(0), activityParamType(fixed, 0))

	variadic := reflect.TypeOf(variadicActivity)
	// Any extra arg for the variadic slice maps to the element type (int).
	assert.Equal(t, reflect.TypeOf(0), activityParamType(variadic, 0))
	assert.Equal(t, reflect.TypeOf(0), activityParamType(variadic, 5))
}

func TestRuntimeFuncName(t *testing.T) {
	name := runtimeFuncName(validActivity)
	assert.True(t, strings.HasSuffix(name, "validActivity"), "got %q", name)
}
