package orchestrator

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadyFuture_ReturnsError(t *testing.T) {
	ctx, _ := newFakeContext()
	f := &readyFuture{result: futureResult{err: errors.New("boom")}}
	assert.EqualError(t, f.Get(ctx, nil), "boom")
}

func TestReadyFuture_AssignsValue(t *testing.T) {
	ctx, _ := newFakeContext()
	f := &readyFuture{result: futureResult{value: 42}}
	var out int
	require.NoError(t, f.Get(ctx, &out))
	assert.Equal(t, 42, out)
}

func TestReadyFuture_NilValuePtrIgnoresValue(t *testing.T) {
	ctx, _ := newFakeContext()
	f := &readyFuture{result: futureResult{value: "ignored"}}
	require.NoError(t, f.Get(ctx, nil))
}

func TestLocalFuture_GetFromChannel(t *testing.T) {
	ctx, _ := newFakeContext()
	ch := make(chan futureResult, 1)
	ch <- futureResult{value: "hello"}
	f := &localFuture{ch: ch}

	var out string
	require.NoError(t, f.Get(ctx, &out))
	assert.Equal(t, "hello", out)
}

func TestLocalFuture_CachesResult(t *testing.T) {
	ctx, _ := newFakeContext()
	ch := make(chan futureResult, 1)
	ch <- futureResult{value: 7}
	f := &localFuture{ch: ch}

	var first int
	require.NoError(t, f.Get(ctx, &first))
	assert.Equal(t, 7, first)

	// Second Get must use the cached result rather than block on the channel.
	var second int
	require.NoError(t, f.Get(ctx, &second))
	assert.Equal(t, 7, second)
}

func TestLocalFuture_ReturnsError(t *testing.T) {
	ctx, _ := newFakeContext()
	ch := make(chan futureResult, 1)
	ch <- futureResult{err: errors.New("activity failed")}
	f := &localFuture{ch: ch}

	var out int
	assert.EqualError(t, f.Get(ctx, &out), "activity failed")
}

func TestLocalFuture_CancelledContext(t *testing.T) {
	ctx, b := newFakeContext()
	b.err = errors.New("ctx cancelled")
	close(b.doneCh) // Done() returns an already-closed channel.

	// Empty channel -> select falls through to ctx.Done().
	f := &localFuture{ch: make(chan futureResult)}
	var out int
	assert.EqualError(t, f.Get(ctx, &out), "ctx cancelled")
}

func TestAssignResult_DirectAssign(t *testing.T) {
	var dst int
	assignResult(&dst, 99)
	assert.Equal(t, 99, dst)
}

func TestAssignResult_PointerSourceDeref(t *testing.T) {
	src := 5
	var dst int
	assignResult(&dst, &src)
	assert.Equal(t, 5, dst)
}

func TestAssignResult_NonPointerDestinationNoop(t *testing.T) {
	// Passing a non-pointer must not panic and must be a no-op.
	assert.NotPanics(t, func() { assignResult(123, 456) })
}

func TestAssignResult_NilPointerNoop(t *testing.T) {
	var dst *int
	assert.NotPanics(t, func() { assignResult(dst, 1) })
}

func TestAssignResult_InvalidSourceNoop(t *testing.T) {
	var dst int
	assert.NotPanics(t, func() { assignResult(&dst, nil) })
	assert.Equal(t, 0, dst)
}

func TestAssignResult_TypeMismatchNoop(t *testing.T) {
	var dst int
	// string is not assignable to int -> silently ignored.
	assignResult(&dst, "not-an-int")
	assert.Equal(t, 0, dst)
}

func TestAssignResult_StructValue(t *testing.T) {
	type payload struct{ N int }
	var dst payload
	assignResult(&dst, payload{N: 3})
	assert.Equal(t, payload{N: 3}, dst)
}
