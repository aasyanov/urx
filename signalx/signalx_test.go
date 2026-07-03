package signalx

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/aasyanov/urx/panix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrap_NilParentUsesBackground(t *testing.T) {
	var nilParent context.Context
	ctx, cancel := Trap(nilParent)
	defer cancel()

	require.NotNil(t, ctx)
	assert.NoError(t, ctx.Err())
}

func TestTrap_CancelStopsWatcher(t *testing.T) {
	ctx, cancel := Trap(context.Background())
	cancel()

	testx.Eventually(t, func() bool {
		return ctx.Err() != nil
	}, time.Second)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestTrap_ParentCancelPropagates(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	ctx, cancel := Trap(parent)
	defer cancel()

	parentCancel()

	testx.Eventually(t, func() bool {
		return ctx.Err() != nil
	}, time.Second)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestTrap_DoubleCancelIdempotent(t *testing.T) {
	_, cancel := Trap(context.Background())
	assert.NotPanics(t, func() {
		cancel()
		cancel()
	})
}

func TestWait_RunsHooksInOrder(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	var order []int
	var mu sync.Mutex
	record := func(n int) func(context.Context) {
		return func(context.Context) {
			mu.Lock()
			order = append(order, n)
			mu.Unlock()
		}
	}

	OnShutdown(record(1))
	OnShutdown(record(2))

	err := Wait(testx.CancelledCtx(), record(3), record(4))
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3, 4}, order)
}

func TestWait_NoHooksReturnsNil(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	err := Wait(testx.CancelledCtx())
	assert.NoError(t, err)
}

func TestWait_NilCtxUsesBackgroundWithCancelledChild(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	var nilCtx context.Context
	done := make(chan error, 1)
	go func() {
		done <- Wait(nilCtx, func(context.Context) {})
	}()

	select {
	case <-done:
		t.Fatal("Wait returned before context was done")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWait_BlocksUntilContextDone(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	ctx, cancel := context.WithCancel(context.Background())
	var ran atomic.Bool
	done := make(chan error, 1)
	go func() {
		done <- Wait(ctx, func(context.Context) { ran.Store(true) })
	}()

	testx.Never(t, ran.Load, 100*time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
		assert.True(t, ran.Load())
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after cancel")
	}
}

func TestWait_HookReceivesTimeout(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	var hasDeadline bool
	err := WaitWith(testx.CancelledCtx(), []Option{WithTimeout(time.Second)},
		func(ctx context.Context) {
			_, hasDeadline = ctx.Deadline()
		})
	require.NoError(t, err)
	assert.True(t, hasDeadline)
}

func TestWait_ZeroTimeoutDisablesDeadline(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	var hasDeadline bool
	err := WaitWith(testx.CancelledCtx(), []Option{WithTimeout(0)},
		func(ctx context.Context) {
			_, hasDeadline = ctx.Deadline()
		})
	require.NoError(t, err)
	assert.False(t, hasDeadline)
}

func TestWait_TimeoutStopsRemainingHooks(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	var secondRan atomic.Bool
	err := WaitWith(testx.CancelledCtx(), []Option{WithTimeout(20 * time.Millisecond)},
		func(context.Context) { time.Sleep(60 * time.Millisecond) },
		func(context.Context) { secondRan.Store(true) },
	)
	require.ErrorIs(t, err, ErrShutdownTimeout)
	assert.False(t, secondRan.Load(), "second hook must not run after timeout")
}

func TestWait_HookObservesDeadlineCancellation(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	var observedDone bool
	err := WaitWith(testx.CancelledCtx(), []Option{WithTimeout(30 * time.Millisecond)},
		func(ctx context.Context) {
			<-ctx.Done()
			observedDone = true
		},
	)
	require.ErrorIs(t, err, ErrShutdownTimeout)
	assert.True(t, observedDone, "hook must observe shutdown context cancellation")
}

func TestWait_NegativeTimeoutDisablesDeadline(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	var hasDeadline bool
	err := WaitWith(testx.CancelledCtx(), []Option{WithTimeout(-time.Second)},
		func(ctx context.Context) {
			_, hasDeadline = ctx.Deadline()
		})
	require.NoError(t, err)
	assert.False(t, hasDeadline, "non-positive timeout must yield no deadline")
}

func TestWait_HookPanicReturnsErrHookPanic(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	var secondRan atomic.Bool
	err := Wait(testx.CancelledCtx(),
		func(context.Context) { panic("boom") },
		func(context.Context) { secondRan.Store(true) },
	)
	require.ErrorIs(t, err, ErrHookPanic)
	assert.True(t, secondRan.Load(), "remaining hooks run after a panicking hook")

	pe := testx.RequirePanicError(t, err, opWait)
	assert.Equal(t, "boom", pe.Value)
}

func TestWait_MultipleHookPanicsJoined(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	err := Wait(testx.CancelledCtx(),
		func(context.Context) { panic("first") },
		func(context.Context) { panic("second") },
	)
	require.ErrorIs(t, err, ErrHookPanic)

	var pe *panix.PanicError
	require.ErrorAs(t, err, &pe)
}

func TestResetHooks_ClearsRegistry(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	var ran atomic.Bool
	OnShutdown(func(context.Context) { ran.Store(true) })
	ResetHooks()

	err := Wait(testx.CancelledCtx())
	require.NoError(t, err)
	assert.False(t, ran.Load())
}

func TestOnShutdown_ConcurrentRegistration(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	const goroutines = 50
	testx.HammerVoid(goroutines, 1, func() {
		OnShutdown(func(context.Context) {})
	})

	globalMu.Lock()
	count := len(globalHooks)
	globalMu.Unlock()
	assert.Equal(t, goroutines, count)
}

func TestWait_ConcurrentWaitersShareGlobalHooks(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	var calls atomic.Int64
	OnShutdown(func(context.Context) { calls.Add(1) })

	const waiters = 20
	var wg sync.WaitGroup
	wg.Add(waiters)
	for range waiters {
		go func() {
			defer wg.Done()
			_ = Wait(testx.CancelledCtx())
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(waiters), calls.Load())
}

func TestWithTimeout(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want time.Duration
	}{
		{name: "default", opt: nil, want: defaultShutdownTimeout},
		{name: "custom", opt: WithTimeout(5 * time.Second), want: 5 * time.Second},
		{name: "zero", opt: WithTimeout(0), want: 0},
		{name: "negative", opt: WithTimeout(-time.Second), want: -time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = []Option{tt.opt}
			}
			cfg := newConfig(opts)
			assert.Equal(t, tt.want, cfg.timeout)
		})
	}
}
