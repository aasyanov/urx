package toutx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAwaitResult_ReturnsDoneWhenReady(t *testing.T) {
	done := make(chan execResult[int], 1)
	done <- execResult[int]{val: 42}

	tctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	got, err := awaitResult(done, tctx, context.Background(), "op", time.Second)
	require.NoError(t, err)
	assert.Equal(t, 42, got)
}

func TestAwaitResult_DoneWinsWhenDeadlineAlsoReady(t *testing.T) {
	// Both the result and the deadline are ready; Go's select may take either
	// arm. Loop so the inner re-check of done after tctx.Done() is exercised.
	for range 50 {
		done := make(chan execResult[int], 1)
		done <- execResult[int]{val: 99}

		tctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		<-tctx.Done()

		got, err := awaitResult(done, tctx, context.Background(), "op", time.Nanosecond)
		cancel()
		require.NoError(t, err)
		assert.Equal(t, 99, got)
	}
}

func TestAwaitResult_MapsCallbackDeadlineError(t *testing.T) {
	tctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-tctx.Done()

	done := make(chan execResult[int], 1)
	done <- execResult[int]{err: context.DeadlineExceeded}

	_, err := awaitResult(done, tctx, context.Background(), "slow.op", 5*time.Millisecond)
	require.ErrorIs(t, err, ErrDeadlineExceeded)
	assert.ErrorContains(t, err, "slow.op")
}

func TestAwaitResult_MapsCallbackCancelError(t *testing.T) {
	done := make(chan execResult[int], 1)
	done <- execResult[int]{err: context.Canceled}

	parent, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("parent stopped"))

	tctx, tcancel := context.WithTimeout(parent, time.Minute)
	defer tcancel()

	_, err := awaitResult(done, tctx, parent, "op", time.Minute)
	require.ErrorIs(t, err, ErrCancelled)
}

func TestAwaitResult_DeadlineExceededWhenDoneEmpty(t *testing.T) {
	done := make(chan execResult[int], 1)

	tctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Nanosecond)

	_, err := awaitResult(done, tctx, context.Background(), "race.op", 5*time.Millisecond)
	require.ErrorIs(t, err, ErrDeadlineExceeded)
	assert.ErrorContains(t, err, "race.op")
}

func TestAwaitResult_ParentCancelWhenDoneEmpty(t *testing.T) {
	done := make(chan execResult[int], 1)

	parent, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("parent stopped"))

	tctx, tcancel := context.WithTimeout(parent, time.Minute)
	defer tcancel()

	_, err := awaitResult(done, tctx, parent, "op", time.Minute)
	require.ErrorIs(t, err, ErrCancelled)
}

func TestAwaitResult_MapsCallbackDeadlineErrorWhenParentCancelled(t *testing.T) {
	done := make(chan execResult[int], 1)
	done <- execResult[int]{err: context.DeadlineExceeded}

	parent, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("parent stopped")
	cancel(cause)

	tctx, tcancel := context.WithTimeout(parent, time.Minute)
	defer tcancel()

	_, err := awaitResult(done, tctx, parent, "race.op", time.Minute)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, cause)
}

func TestNormalizeResult_CanceledWithoutParentCause(t *testing.T) {
	tctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var zero int
	got, err := normalizeResult(zero, tctx, context.Background(), "op", time.Second,
		execResult[int]{val: 7, err: context.Canceled})
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrCancelled)
	assert.Equal(t, 7, got)
}

func TestNormalizeResult_CanceledWhenOurCtxDone(t *testing.T) {
	tctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-tctx.Done()
	got, err := normalizeResult(0, tctx, context.Background(), "op", time.Second,
		execResult[int]{val: 7, err: context.Canceled})
	require.ErrorIs(t, err, ErrDeadlineExceeded)
	assert.NotErrorIs(t, err, ErrCancelled)
	assert.Zero(t, got)
}

func TestNormalizeResult_InnerDeadlineExceededPropagates(t *testing.T) {
	tctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	got, err := normalizeResult(0, tctx, context.Background(), "op", time.Second,
		execResult[int]{val: 42, err: context.DeadlineExceeded})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, ErrDeadlineExceeded)
	assert.Equal(t, 42, got)
}

func TestResolveDeadline_FallsBackToStartPlusTimeout(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	const timeout = 7 * time.Second

	got := resolveDeadline(context.Background(), start, timeout)
	assert.Equal(t, start.Add(timeout), got)
}

func TestResolveDeadline_UsesContextDeadline(t *testing.T) {
	deadline := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	got := resolveDeadline(ctx, time.Now(), time.Minute)
	assert.Equal(t, deadline, got)
}
