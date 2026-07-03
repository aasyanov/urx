package bulkx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fill acquires n slots and returns their tokens so the caller can hold the
// bulkhead at a chosen occupancy. It fails the test if any acquire is rejected.
func fill(t *testing.T, b *Bulkhead, n int) []*Token {
	t.Helper()
	tokens := make([]*Token, 0, n)
	for range n {
		tok, err := b.Acquire(context.Background())
		require.NoError(t, err)
		tokens = append(tokens, tok)
	}
	return tokens
}

func release(tokens []*Token) {
	for _, tok := range tokens {
		tok.Release()
	}
}

// --- Construction & defaults ---

func TestNew_AppliesDefaults(t *testing.T) {
	b := New()
	defer func() { require.NoError(t, b.Close()) }()
	assert.Equal(t, DefaultMaxConcurrent, b.MaxConcurrent())
	assert.Equal(t, DefaultTimeout, b.cfg.timeout)
}

func TestNew_OptionValidation(t *testing.T) {
	tests := []struct {
		name            string
		opts            []Option
		wantConcurrency int
		wantTimeout     time.Duration
	}{
		{"defaults", nil, DefaultMaxConcurrent, DefaultTimeout},
		{"custom concurrency", []Option{WithMaxConcurrent(50)}, 50, DefaultTimeout},
		{"zero concurrency ignored then floored", []Option{WithMaxConcurrent(0)}, DefaultMaxConcurrent, DefaultTimeout},
		{"negative concurrency ignored", []Option{WithMaxConcurrent(-5)}, DefaultMaxConcurrent, DefaultTimeout},
		{"custom timeout", []Option{WithTimeout(time.Second)}, DefaultMaxConcurrent, time.Second},
		{"zero timeout ignored", []Option{WithTimeout(0)}, DefaultMaxConcurrent, DefaultTimeout},
		{"negative timeout ignored", []Option{WithTimeout(-time.Second)}, DefaultMaxConcurrent, DefaultTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(tt.opts...)
			defer func() { require.NoError(t, b.Close()) }()
			assert.Equal(t, tt.wantConcurrency, b.MaxConcurrent())
			assert.Equal(t, tt.wantTimeout, b.cfg.timeout)
		})
	}
}

func TestNewConfig_ConcurrencyFlooredToMin(t *testing.T) {
	// WithMaxConcurrent ignores n<=0, but a direct out-of-range config still
	// floors to keep the semaphore channel usable.
	cfg := newConfig([]Option{func(c *config) { c.maxConcurrent = -10 }})
	assert.Equal(t, minConcurrent, cfg.maxConcurrent)
}

func TestWithOp_OverridesDefault(t *testing.T) {
	assert.Equal(t, opExecute, newConfig(nil).opOrDefault())
	assert.Equal(t, "api.search", newConfig([]Option{WithOp("api.search")}).opOrDefault())
	assert.Equal(t, opExecute, newConfig([]Option{WithOp("")}).opOrDefault())
}

// --- Execute: happy path ---

func TestExecute_RunsAndReturns(t *testing.T) {
	b := New(WithMaxConcurrent(10))
	defer func() { require.NoError(t, b.Close()) }()

	got, err := Execute(b, context.Background(),
		func(context.Context, BulkController) (int, error) {
			return 42, nil
		})
	require.NoError(t, err)
	assert.Equal(t, 42, got)
	assert.Equal(t, uint64(1), b.Stats().Executed)
}

func TestExecute_ReleasesSlotAfterReturn(t *testing.T) {
	b := New(WithMaxConcurrent(10))
	defer func() { require.NoError(t, b.Close()) }()

	_, err := Execute(b, context.Background(),
		func(_ context.Context, bc BulkController) (int, error) {
			assert.Equal(t, 1, b.Active())
			assert.Equal(t, 1, bc.Active()) // snapshot includes self
			assert.Equal(t, 10, bc.MaxConcurrent())
			assert.False(t, bc.WaitedSlot())
			assert.InEpsilon(t, 0.1, bc.Load(), 1e-9)
			return 1, nil
		})
	require.NoError(t, err)
	assert.Equal(t, 0, b.Active())
}

func TestExecute_PropagatesError(t *testing.T) {
	b := New()
	defer func() { require.NoError(t, b.Close()) }()

	sentinel := errors.New("boom")
	_, err := Execute(b, context.Background(),
		func(context.Context, BulkController) (int, error) {
			return 0, sentinel
		})
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, 0, b.Active())
}

func TestExecute_WaitedSlotOnSlowPath(t *testing.T) {
	b := New(WithMaxConcurrent(1), WithTimeout(time.Second))
	defer func() { require.NoError(t, b.Close()) }()

	// Hold the only slot, then start a second Execute that must wait for it.
	tok := fill(t, b, 1)[0]

	var waited atomic.Bool
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		_, _ = Execute(b, context.Background(),
			func(_ context.Context, bc BulkController) (int, error) {
				waited.Store(bc.WaitedSlot())
				return 1, nil
			})
	}()

	// Wait for the goroutine to start and reach the blocking slow path (its
	// optimistic attempt fails because the only slot is held), then free a
	// slot so it is admitted via the timer select with waitedSlot == true.
	<-started
	testx.Eventually(t, func() bool { return !b.Allow() }, time.Second)
	time.Sleep(50 * time.Millisecond)
	tok.Release()
	<-done
	assert.True(t, waited.Load(), "second call should report it waited for a slot")
}

// --- Execute: error paths ---

func TestExecute_ReturnsErrClosedAfterClose(t *testing.T) {
	b := New()
	require.NoError(t, b.Close())
	testx.AssertOpAfterClose(t, func() error {
		_, err := Execute(b, context.Background(),
			func(context.Context, BulkController) (int, error) { return 1, nil })
		return err
	}, ErrClosed, "Execute")
}

func TestExecute_ReturnsErrNilFunc(t *testing.T) {
	b := New()
	defer func() { require.NoError(t, b.Close()) }()

	_, err := Execute[int](b, context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestExecute_ReturnsErrCancelledOnCancelledContext(t *testing.T) {
	b := New()
	defer func() { require.NoError(t, b.Close()) }()

	called := false
	_, err := Execute(b, testx.CancelledCtx(),
		func(context.Context, BulkController) (int, error) {
			called = true
			return 1, nil
		})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, called, "fn must not run for a cancelled context")
	assert.Equal(t, 0, b.Active(), "cancelled request must not consume a slot")
	assert.Equal(t, uint64(1), b.Stats().Rejected)
}

func TestExecute_ReturnsErrCancelledOnExpiredDeadline(t *testing.T) {
	b := New()
	defer func() { require.NoError(t, b.Close()) }()

	_, err := Execute(b, testx.ExpiredCtx(),
		func(context.Context, BulkController) (int, error) { return 1, nil })
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExecute_TimesOutWaitingForSlot(t *testing.T) {
	b := New(WithMaxConcurrent(1), WithTimeout(20*time.Millisecond))
	defer func() { require.NoError(t, b.Close()) }()

	tokens := fill(t, b, 1)
	defer release(tokens)

	_, err := Execute(b, context.Background(),
		func(context.Context, BulkController) (int, error) { return 1, nil })
	require.ErrorIs(t, err, ErrTimeout)
	assert.Equal(t, uint64(1), b.Stats().Timeouts)
}

func TestExecute_CancelledWhileWaiting(t *testing.T) {
	b := New(WithMaxConcurrent(1), WithTimeout(time.Minute))
	defer func() { require.NoError(t, b.Close()) }()

	tokens := fill(t, b, 1)
	defer release(tokens)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := Execute(b, ctx,
			func(context.Context, BulkController) (int, error) { return 1, nil })
		errCh <- err
	}()

	testx.Eventually(t, func() bool { return b.Stats().Active == 1 }, time.Second)
	cancel()
	require.ErrorIs(t, <-errCh, ErrCancelled)
}

func TestExecute_RecoversPanic(t *testing.T) {
	b := New(WithOp("bulkx.test"))
	defer func() { require.NoError(t, b.Close()) }()

	_, err := Execute(b, context.Background(),
		func(context.Context, BulkController) (int, error) {
			panic("kaboom")
		})
	testx.RequirePanicError(t, err, "bulkx.test")
	assert.Equal(t, 0, b.Active(), "slot released even on panic")
}

// --- TryExecute ---

func TestTryExecute_RunsWhenSlotFree(t *testing.T) {
	b := New(WithMaxConcurrent(2))
	defer func() { require.NoError(t, b.Close()) }()

	ok, got, err := TryExecute(b, context.Background(),
		func(context.Context, BulkController) (int, error) { return 7, nil })
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 7, got)
}

func TestTryExecute_RejectsWhenFull(t *testing.T) {
	b := New(WithMaxConcurrent(1))
	defer func() { require.NoError(t, b.Close()) }()

	tokens := fill(t, b, 1)
	defer release(tokens)

	called := false
	ok, _, err := TryExecute(b, context.Background(),
		func(context.Context, BulkController) (int, error) {
			called = true
			return 1, nil
		})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.False(t, called, "fn must not run when no slot is free")
	assert.Equal(t, uint64(1), b.Stats().Rejected)
}

func TestTryExecute_ReturnsErrClosedAfterClose(t *testing.T) {
	b := New()
	require.NoError(t, b.Close())
	ok, _, err := TryExecute(b, context.Background(),
		func(context.Context, BulkController) (int, error) { return 1, nil })
	require.ErrorIs(t, err, ErrClosed)
	assert.False(t, ok)
}

func TestTryExecute_ReturnsErrNilFunc(t *testing.T) {
	b := New()
	defer func() { require.NoError(t, b.Close()) }()

	ok, _, err := TryExecute[int](b, context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
	assert.False(t, ok)
}

// --- Acquire / Token ---

func TestAcquire_TracksActive(t *testing.T) {
	b := New(WithMaxConcurrent(10))
	defer func() { require.NoError(t, b.Close()) }()

	tok, err := b.Acquire(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, b.Active())

	tok.Release()
	assert.Equal(t, 0, b.Active())
}

func TestAcquire_TimesOut(t *testing.T) {
	b := New(WithMaxConcurrent(1), WithTimeout(20*time.Millisecond))
	defer func() { require.NoError(t, b.Close()) }()

	tokens := fill(t, b, 1)
	defer release(tokens)

	_, err := b.Acquire(context.Background())
	require.ErrorIs(t, err, ErrTimeout)
}

func TestAcquire_ReturnsErrClosedAfterClose(t *testing.T) {
	b := New()
	require.NoError(t, b.Close())
	_, err := b.Acquire(context.Background())
	require.ErrorIs(t, err, ErrClosed)
}

func TestAcquire_BlockedWaiterRejectedOnClose(t *testing.T) {
	b := New(WithMaxConcurrent(1), WithTimeout(time.Minute))
	tokens := fill(t, b, 1)

	waiting := make(chan error, 1)
	go func() {
		_, err := b.Acquire(context.Background())
		waiting <- err
	}()

	testx.Eventually(t, func() bool { return b.Active() == 1 }, time.Second)
	require.NoError(t, b.Close())

	select {
	case err := <-waiting:
		require.ErrorIs(t, err, ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("blocked Acquire did not wake on Close")
	}

	release(tokens)
}

func TestAcquire_ReturnsErrCancelled(t *testing.T) {
	b := New()
	defer func() { require.NoError(t, b.Close()) }()

	_, err := b.Acquire(testx.CancelledCtx())
	require.ErrorIs(t, err, ErrCancelled)
}

func TestAcquire_CancelledWhileWaiting(t *testing.T) {
	b := New(WithMaxConcurrent(1), WithTimeout(time.Minute))
	defer func() { require.NoError(t, b.Close()) }()

	tokens := fill(t, b, 1)
	defer release(tokens)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := b.Acquire(ctx)
		errCh <- err
	}()

	// Let the goroutine reach the blocking slow path before cancelling.
	time.Sleep(50 * time.Millisecond)
	cancel()
	err := <-errCh
	require.ErrorIs(t, err, ErrCancelled)
	assert.Equal(t, uint64(1), b.Stats().Rejected)
}

func TestAcquire_AdmittedAfterWait(t *testing.T) {
	b := New(WithMaxConcurrent(1), WithTimeout(time.Minute))
	defer func() { require.NoError(t, b.Close()) }()

	tok := fill(t, b, 1)[0]

	errCh := make(chan error, 1)
	go func() {
		t2, err := b.Acquire(context.Background())
		if err == nil {
			t2.Release()
		}
		errCh <- err
	}()

	time.Sleep(50 * time.Millisecond)
	tok.Release() // free the slot so the waiter is admitted via the timer select
	require.NoError(t, <-errCh)
	assert.Equal(t, 0, b.Active())
}

func TestToken_ReleaseIsIdempotent(t *testing.T) {
	b := New(WithMaxConcurrent(10))
	defer func() { require.NoError(t, b.Close()) }()

	tok, err := b.Acquire(context.Background())
	require.NoError(t, err)
	tok.Release()
	tok.Release() // must not drive active negative or double-release the slot
	assert.Equal(t, 0, b.Active())

	// The slot must be reusable after a double release.
	tok2, err := b.Acquire(context.Background())
	require.NoError(t, err)
	tok2.Release()
}

func TestToken_NilReleaseIsNoop(t *testing.T) {
	var tok *Token
	assert.NotPanics(t, tok.Release)
}

// --- Allow ---

func TestAllow_ReflectsOccupancy(t *testing.T) {
	b := New(WithMaxConcurrent(2))
	defer func() { require.NoError(t, b.Close()) }()

	assert.True(t, b.Allow())
	tokens := fill(t, b, 2)
	assert.False(t, b.Allow(), "full bulkhead allows nothing")
	release(tokens)
	assert.True(t, b.Allow())

	require.NoError(t, b.Close())
	assert.False(t, b.Allow(), "closed bulkhead allows nothing")
}

func TestAllow_DoesNotConsumeSlot(t *testing.T) {
	b := New(WithMaxConcurrent(10))
	defer func() { require.NoError(t, b.Close()) }()

	for range 100 {
		b.Allow()
	}
	assert.Equal(t, 0, b.Active(), "Allow must not reserve a slot")
}

// --- Load ---

func TestLoad_ReflectsActive(t *testing.T) {
	b := New(WithMaxConcurrent(10))
	defer func() { require.NoError(t, b.Close()) }()

	assert.Equal(t, 0.0, b.Load())
	tokens := fill(t, b, 3)
	defer release(tokens)
	assert.InEpsilon(t, 0.3, b.Load(), 1e-9)
}

// --- Stats & lifecycle ---

func TestStats_Snapshot(t *testing.T) {
	b := New(WithMaxConcurrent(100))
	defer func() { require.NoError(t, b.Close()) }()

	tokens := fill(t, b, 3)
	defer release(tokens)

	st := b.Stats()
	assert.Equal(t, 100, st.MaxConcurrent)
	assert.Equal(t, 3, st.Active)
}

func TestResetStats_ZeroesCounters(t *testing.T) {
	b := New(WithMaxConcurrent(1), WithTimeout(10*time.Millisecond))
	defer func() { require.NoError(t, b.Close()) }()

	_, err := Execute(b, context.Background(),
		func(context.Context, BulkController) (int, error) { return 1, nil })
	require.NoError(t, err)

	tokens := fill(t, b, 1)
	_, _, _ = TryExecute(b, context.Background(),
		func(context.Context, BulkController) (int, error) { return 1, nil }) // rejected
	_, _ = Execute(b, context.Background(),
		func(context.Context, BulkController) (int, error) { return 1, nil }) // timeout
	release(tokens)

	b.ResetStats()
	st := b.Stats()
	assert.Equal(t, uint64(0), st.Executed)
	assert.Equal(t, uint64(0), st.Rejected)
	assert.Equal(t, uint64(0), st.Timeouts)
}

func TestClose_Idempotent(t *testing.T) {
	b := New()
	testx.AssertCloseIdempotent(t, b)
	assert.True(t, b.IsClosed())
}

// --- Concurrency ---

func TestExecute_RaceSafe(t *testing.T) {
	b := New(WithMaxConcurrent(16), WithTimeout(time.Second))
	defer func() { require.NoError(t, b.Close()) }()

	testx.HammerVoid(64, 200, func() {
		_, _ = Execute(b, context.Background(),
			func(_ context.Context, bc BulkController) (int, error) {
				_ = bc.Load()
				return 1, nil
			})
	})
	assert.Equal(t, 0, b.Active())
}

func TestAcquire_RaceSafe(t *testing.T) {
	b := New(WithMaxConcurrent(32), WithTimeout(time.Second))
	defer func() { require.NoError(t, b.Close()) }()

	testx.HammerVoid(32, 500, func() {
		tok, err := b.Acquire(context.Background())
		if err != nil {
			return
		}
		tok.Release()
	})
	assert.Equal(t, 0, b.Active())
}

// TestExecute_NeverExceedsMaxConcurrent is the contract that justifies the
// semaphore: with many goroutines racing for a tiny slot count, the observed
// active count must never exceed the configured maximum.
func TestExecute_NeverExceedsMaxConcurrent(t *testing.T) {
	const maxConcurrent = 8
	b := New(WithMaxConcurrent(maxConcurrent), WithTimeout(5*time.Second))
	defer func() { require.NoError(t, b.Close()) }()

	var maxSeen atomic.Int64
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_, _ = Execute(b, context.Background(),
					func(context.Context, BulkController) (int, error) {
						cur := int64(b.Active())
						for {
							prev := maxSeen.Load()
							if cur <= prev || maxSeen.CompareAndSwap(prev, cur) {
								break
							}
						}
						return 1, nil
					})
			}
		}()
	}
	wg.Wait()

	assert.LessOrEqual(t, maxSeen.Load(), int64(maxConcurrent),
		"active (%d) exceeded max concurrent (%d)", maxSeen.Load(), maxConcurrent)
	assert.Equal(t, 0, b.Active())
}

// --- BulkController ---

func TestExecute_ControllerSnapshot(t *testing.T) {
	b := New(WithMaxConcurrent(8))
	defer b.Close()

	var snapshot struct {
		active        int
		maxConcurrent int
		load          float64
		waitedSlot    bool
	}
	_, err := Execute(b, context.Background(), func(_ context.Context, bc BulkController) (int, error) {
		snapshot.active = bc.Active()
		snapshot.maxConcurrent = bc.MaxConcurrent()
		snapshot.load = bc.Load()
		snapshot.waitedSlot = bc.WaitedSlot()
		return 1, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, snapshot.active)
	assert.Equal(t, 8, snapshot.maxConcurrent)
	assert.InDelta(t, 1.0/8.0, snapshot.load, 0.01)
	assert.False(t, snapshot.waitedSlot)
}
