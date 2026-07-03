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
	done := make(chan execResult[int], 1)
	done <- execResult[int]{val: 99}

	tctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Nanosecond)

	got, err := awaitResult(done, tctx, context.Background(), "op", time.Nanosecond)
	require.NoError(t, err)
	assert.Equal(t, 99, got)
}

func TestAwaitResult_MapsCallbackDeadlineError(t *testing.T) {
	done := make(chan execResult[int], 1)
	done <- execResult[int]{err: context.DeadlineExceeded}

	tctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

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
