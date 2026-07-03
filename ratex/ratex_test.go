package ratex

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cancelAfterCtx returns a context whose [context.Context.Err] becomes
// non-nil after the n-th call, enabling deterministic tests of the wait-loop
// re-check points without relying on real timer scheduling.
type cancelAfterCtx struct {
	context.Context
	calls atomic.Int32
	after int32
	err   error
}

func (c *cancelAfterCtx) Err() error {
	if c.Context == nil {
		c.Context = context.Background()
	}
	if c.err == nil {
		c.err = context.Canceled
	}
	if c.calls.Add(1) >= c.after {
		return c.err
	}
	return c.Context.Err()
}

// --- Construction & options ---

func TestNew_Defaults(t *testing.T) {
	l := New()
	assert.Equal(t, DefaultRate, l.Rate())
	assert.Equal(t, DefaultBurst, l.Burst())
	assert.InDelta(t, float64(DefaultBurst), l.Tokens(), 0.01)
}

func TestWithRate(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want float64
	}{
		{"default", nil, DefaultRate},
		{"custom", WithRate(250), 250},
		{"zero ignored", WithRate(0), DefaultRate},
		{"negative ignored", WithRate(-5), DefaultRate},
		{"fractional", WithRate(0.5), minRate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []Option{}
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			assert.Equal(t, tt.want, New(opts...).Rate())
		})
	}
}

func TestWithBurst(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want int
	}{
		{"default", nil, DefaultBurst},
		{"custom", WithBurst(100), 100},
		{"zero clamped to floor", WithBurst(0), minBurst},
		{"negative clamped to floor", WithBurst(-3), minBurst},
		{"floor explicit", WithBurst(1), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []Option{}
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			assert.Equal(t, tt.want, New(opts...).Burst())
		})
	}
}

func TestNewConfig_FloorsRateBelowMin(t *testing.T) {
	l := New(WithRate(0.5))
	assert.Equal(t, minRate, l.Rate())
}

func TestLimiter_Delay_ReturnsMinWhenTokensAlreadyAvailable(t *testing.T) {
	l := New(WithRate(100), WithBurst(10))
	l.mu.Lock()
	l.tokens = 10
	l.mu.Unlock()
	assert.Equal(t, minDelay, l.delay(5))
}

func TestLimiter_Delay_ReturnsMinWhenComputedSubMillisecond(t *testing.T) {
	l := New(WithRate(1_000_000), WithBurst(10))
	l.mu.Lock()
	l.tokens = 0.999999
	l.mu.Unlock()
	assert.Equal(t, minDelay, l.delay(1))
}

func TestLimiter_Delay_ReturnsComputedDuration(t *testing.T) {
	l := New(WithRate(2), WithBurst(10))
	l.mu.Lock()
	l.tokens = 0
	l.mu.Unlock()
	assert.Equal(t, 2500*time.Millisecond, l.delay(5))
}

func TestWaitFor_SucceedsAfterSleeping(t *testing.T) {
	l := New(WithRate(1000), WithBurst(1))
	require.True(t, l.Allow())

	res, err := l.waitFor(context.Background(), 1)
	require.NoError(t, err)
	assert.True(t, res.waited)
	assert.GreaterOrEqual(t, res.remaining, 0.0)
}

func TestWaitFor_CancelledAfterTimerBeforeTake(t *testing.T) {
	l := New(WithRate(100_000), WithBurst(1))
	require.True(t, l.Allow())

	_, err := l.waitFor(&cancelAfterCtx{after: 2}, 1)
	require.ErrorIs(t, err, ErrCancelled)
	assert.Equal(t, uint64(1), l.Stats().Limited)
}

func TestWaitFor_CancelledOnSecondLoopIteration(t *testing.T) {
	l := New(WithRate(100_000), WithBurst(1))
	require.True(t, l.Allow())

	_, err := l.waitFor(&cancelAfterCtx{after: 3}, 1)
	require.ErrorIs(t, err, ErrCancelled)
}

func TestWaitFor_CancelledViaContextDoneDuringSelect(t *testing.T) {
	l := New(WithRate(0.0001), WithBurst(1))
	require.True(t, l.Allow())

	ctx, cancel := testx.TimedCtx(20 * time.Millisecond)
	defer cancel()

	_, err := l.waitFor(ctx, 1)
	require.ErrorIs(t, err, ErrCancelled)
}

func TestStopTimer_DrainsAlreadyFiredTimer(t *testing.T) {
	timer := time.NewTimer(0)
	<-timer.C
	stopTimer(timer) // must not block; covers the drain branch
}

func TestStopTimer_StopsPendingTimer(t *testing.T) {
	timer := time.NewTimer(time.Hour)
	require.True(t, timer.Stop())
	stopTimer(timer)
}

// --- Release ---

func TestLimiter_Release_AfterAllowNRefundsTokens(t *testing.T) {
	l := New(WithRate(1), WithBurst(5))
	require.True(t, l.AllowN(3))
	assert.InDelta(t, 2.0, l.Tokens(), 0.01)

	l.Release(3)
	assert.InDelta(t, 5.0, l.Tokens(), 0.01)
	assert.Zero(t, l.Stats().Allowed, "Release must roll back the admission count")
}

func TestLimiter_Release_CapsAtBurst(t *testing.T) {
	l := New(WithRate(1), WithBurst(2))
	require.True(t, l.Allow())
	l.Release(5)
	assert.InDelta(t, 2.0, l.Tokens(), 0.01)
}

func TestLimiter_Release_DoesNotDriveAllowedNegative(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	l.Release(1)
	assert.Zero(t, l.Stats().Allowed)
}

func TestWaitN_CancelAfterTakeRefundsMultipleTokens(t *testing.T) {
	l := New(WithRate(1), WithBurst(5))

	err := l.WaitN(&cancelAfterCtx{after: 2}, 3)
	require.ErrorIs(t, err, ErrCancelled)
	assert.InDelta(t, 5.0, l.Tokens(), 0.01, "WaitN must refund all n tokens on cancel-after-take")
	s := l.Stats()
	assert.Zero(t, s.Allowed)
	assert.Equal(t, uint64(1), s.Limited)
}

// --- Allow / AllowN ---

func TestLimiter_AllowConsumesBurst(t *testing.T) {
	l := New(WithRate(1), WithBurst(3))
	for i := range 3 {
		assert.True(t, l.Allow(), "token %d should be admitted", i)
	}
	assert.False(t, l.Allow(), "bucket should be empty after burst")
}

func TestLimiter_AllowN(t *testing.T) {
	l := New(WithRate(1), WithBurst(10))
	assert.True(t, l.AllowN(7))
	assert.False(t, l.AllowN(5), "only 3 tokens left, 5 should fail")
	assert.True(t, l.AllowN(3))
}

func TestLimiter_AllowN_NonPositiveTreatedAsOne(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	assert.True(t, l.AllowN(0), "n<1 should be treated as 1")
	assert.False(t, l.AllowN(-5), "bucket now empty")
}

func TestLimiter_AllowN_FailureConsumesNothing(t *testing.T) {
	l := New(WithRate(1), WithBurst(2))
	assert.False(t, l.AllowN(5))
	assert.True(t, l.AllowN(2), "the two original tokens must remain")
}

func TestLimiter_Refill(t *testing.T) {
	l := New(WithRate(1000), WithBurst(1))
	require.True(t, l.Allow())
	require.False(t, l.Allow())
	testx.Eventually(t, l.Allow, time.Second)
}

func TestLimiter_RefillCapsAtBurst(t *testing.T) {
	l := New(WithRate(1_000_000), WithBurst(5))
	time.Sleep(5 * time.Millisecond)
	assert.LessOrEqual(t, l.Tokens(), float64(5))
}

// --- Wait / WaitN ---

func TestLimiter_Wait_AcquiresImmediately(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	require.NoError(t, l.Wait(context.Background()))
}

func TestLimiter_Wait_BlocksThenSucceeds(t *testing.T) {
	l := New(WithRate(1000), WithBurst(1))
	require.True(t, l.Allow())

	start := time.Now()
	require.NoError(t, l.Wait(context.Background()))
	assert.Positive(t, time.Since(start), "Wait should have blocked for refill")
}

func TestLimiter_Wait_CancelledContext(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	require.True(t, l.Allow())

	err := l.Wait(testx.CancelledCtx())
	require.ErrorIs(t, err, ErrCancelled)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestLimiter_Wait_DeadlineExceeded(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	require.True(t, l.Allow())

	ctx, cancel := testx.TimedCtx(20 * time.Millisecond)
	defer cancel()
	err := l.Wait(ctx)
	require.ErrorIs(t, err, ErrCancelled)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestLimiter_WaitN_ImpossibleRequestCancellable(t *testing.T) {
	l := New(WithRate(1), WithBurst(2))
	ctx, cancel := testx.TimedCtx(30 * time.Millisecond)
	defer cancel()
	// 5 tokens can never fit in a burst-2 bucket; Wait must respect ctx.
	err := l.WaitN(ctx, 5)
	require.ErrorIs(t, err, ErrCancelled)
}

func TestLimiter_WaitN_CancelAfterTimerFires(t *testing.T) {
	l := New(WithRate(10), WithBurst(1))
	require.True(t, l.Allow())

	err := l.WaitN(&cancelAfterCtx{after: 2}, 1)
	require.ErrorIs(t, err, ErrCancelled)
}

func TestLimiter_WaitN_NonPositiveTreatedAsOne(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	require.NoError(t, l.WaitN(context.Background(), 0))
}

// --- Execute ---

func TestExecute_Success(t *testing.T) {
	l := New(WithRate(10), WithBurst(5))
	got, err := Execute(l, context.Background(),
		func(_ context.Context, rc RateController) (int, error) {
			assert.False(t, rc.Waited())
			assert.Equal(t, 10.0, rc.Rate())
			assert.Equal(t, 5, rc.Burst())
			assert.InDelta(t, 4.0, rc.Tokens(), 0.01)
			return 42, nil
		})
	require.NoError(t, err)
	assert.Equal(t, 42, got)
}

func TestExecute_NilFunc(t *testing.T) {
	l := New()
	_, err := Execute[int](l, context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestExecute_PropagatesFnError(t *testing.T) {
	l := New()
	sentinel := errors.New("boom")
	_, err := Execute(l, context.Background(),
		func(context.Context, RateController) (int, error) {
			return 0, sentinel
		})
	require.ErrorIs(t, err, sentinel)
}

func TestExecute_CancelledContext(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	require.True(t, l.Allow())

	called := false
	_, err := Execute(l, testx.CancelledCtx(),
		func(context.Context, RateController) (int, error) {
			called = true
			return 1, nil
		})
	require.ErrorIs(t, err, ErrCancelled)
	assert.False(t, called, "fn must not run when admission fails")
}

func TestExecute_CancelledWhileWaiting(t *testing.T) {
	// Slow refill + empty bucket forces acquire into its wait loop, where a
	// short deadline must surface ErrCancelled.
	l := New(WithRate(0.0001), WithBurst(1))
	require.True(t, l.Allow())

	ctx, cancel := testx.TimedCtx(30 * time.Millisecond)
	defer cancel()

	called := false
	_, err := Execute(l, ctx,
		func(context.Context, RateController) (int, error) {
			called = true
			return 1, nil
		})
	require.ErrorIs(t, err, ErrCancelled)
	assert.False(t, called)
}

func TestAcquire_CancelledAfterTimerBeforeTake(t *testing.T) {
	l := New(WithRate(100_000), WithBurst(1))
	require.True(t, l.Allow())

	ctx := &cancelAfterCtx{after: 2}

	_, _, err := l.acquire(ctx)
	require.ErrorIs(t, err, ErrCancelled)
	assert.Equal(t, uint64(1), l.Stats().Allowed, "initial Allow only")
}

func TestAcquire_CancelledAfterTakeRefundsToken(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	ctx := &cancelAfterCtx{after: 2}

	_, _, err := l.acquire(ctx)
	require.ErrorIs(t, err, ErrCancelled)
	assert.InDelta(t, 1.0, l.Tokens(), 0.01, "cancelled acquire must refund the token")
	s := l.Stats()
	assert.Equal(t, uint64(1), s.Limited)
	assert.Zero(t, s.Allowed)
}

func TestExecute_RejectsCancelledContextAfterTimer(t *testing.T) {
	l := New(WithRate(10), WithBurst(1))
	require.True(t, l.Allow())

	ctx := &cancelAfterCtx{after: 2}
	called := false
	_, err := Execute(l, ctx,
		func(context.Context, RateController) (int, error) {
			called = true
			return 1, nil
		})
	require.ErrorIs(t, err, ErrCancelled)
	assert.False(t, called, "fn must not run after ctx cancelled post-timer")
}

func TestWaitN_RejectsCancelledContextAfterTimer(t *testing.T) {
	l := New(WithRate(10), WithBurst(1))
	require.True(t, l.Allow())

	err := l.WaitN(&cancelAfterCtx{after: 2}, 1)
	require.ErrorIs(t, err, ErrCancelled)
	s := l.Stats()
	assert.Equal(t, uint64(1), s.Limited)
	assert.Equal(t, uint64(1), s.Allowed, "initial Allow only")
}

func TestWaitN_CancelledAfterTakeRefundsToken(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))

	err := l.WaitN(&cancelAfterCtx{after: 2}, 1)
	require.ErrorIs(t, err, ErrCancelled)
	assert.InDelta(t, 1.0, l.Tokens(), 0.01)
	s := l.Stats()
	assert.Equal(t, uint64(1), s.Limited)
	assert.Zero(t, s.Allowed)
}

func TestExecute_Waits(t *testing.T) {
	l := New(WithRate(0.0001), WithBurst(1))
	require.True(t, l.Allow())

	var waited bool
	_, err := Execute(l, context.Background(),
		func(_ context.Context, rc RateController) (int, error) {
			waited = rc.Waited()
			return 1, nil
		})
	require.NoError(t, err)
	assert.True(t, waited, "second call should have blocked for a refill")
}

func TestExecute_PanicBecomesError(t *testing.T) {
	l := New()
	_, err := Execute(l, context.Background(),
		func(context.Context, RateController) (int, error) {
			panic("kaboom")
		})
	testx.RequirePanicError(t, err, opExecute)
}

func TestExecute_SkipTokenRefunds(t *testing.T) {
	l := New(WithRate(0.0001), WithBurst(2))
	for range 2 {
		_, err := Execute(l, context.Background(),
			func(_ context.Context, rc RateController) (int, error) {
				rc.SkipToken()
				return 1, nil
			})
		require.NoError(t, err)
	}
	// Both tokens were refunded, so the bucket is still full.
	assert.InDelta(t, 2.0, l.Tokens(), 0.01)
	s := l.Stats()
	assert.Zero(t, s.Allowed, "skipped calls should not count as allowed")
}

// --- TryExecute ---

func TestTryExecute_Success(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	ok, got, err := TryExecute(l, context.Background(),
		func(context.Context, RateController) (string, error) {
			return "done", nil
		})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "done", got)
}

func TestTryExecute_NoTokenSkipsFn(t *testing.T) {
	l := New(WithRate(0.0001), WithBurst(1))
	require.True(t, l.Allow())

	called := false
	ok, _, err := TryExecute(l, context.Background(),
		func(context.Context, RateController) (int, error) {
			called = true
			return 1, nil
		})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.False(t, called)
}

func TestTryExecute_NilFunc(t *testing.T) {
	l := New()
	ok, _, err := TryExecute[int](l, context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
	assert.False(t, ok)
}

func TestTryExecute_CancelledContextBeforeTake(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	called := false
	ok, _, err := TryExecute(l, testx.CancelledCtx(),
		func(context.Context, RateController) (int, error) {
			called = true
			return 1, nil
		})
	require.ErrorIs(t, err, ErrCancelled)
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, ok)
	assert.False(t, called)
	assert.InDelta(t, 1.0, l.Tokens(), 0.01, "cancelled call must not consume a token")
}

func TestTryExecute_CancelledContextBeforeTakeDoesNotCountLimited(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	ctx := &cancelAfterCtx{after: 1}

	called := false
	ok, _, err := TryExecute(l, ctx,
		func(context.Context, RateController) (int, error) {
			called = true
			return 1, nil
		})
	require.ErrorIs(t, err, ErrCancelled)
	assert.False(t, ok)
	assert.False(t, called)
	assert.InDelta(t, 1.0, l.Tokens(), 0.01)
	assert.Zero(t, l.Stats().Limited, "cancelled before take must not count as limited")
}

func TestTryExecute_CancelledAfterTakeRefundsToken(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	ctx := &cancelAfterCtx{after: 2}

	called := false
	ok, _, err := TryExecute(l, ctx,
		func(context.Context, RateController) (int, error) {
			called = true
			return 1, nil
		})
	require.ErrorIs(t, err, ErrCancelled)
	assert.False(t, ok)
	assert.False(t, called)
	assert.InDelta(t, 1.0, l.Tokens(), 0.01)
	s := l.Stats()
	assert.Equal(t, uint64(1), s.Limited)
	assert.Zero(t, s.Allowed)
}

func TestTryExecute_NeverWaited(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	_, _, _ = TryExecute(l, context.Background(),
		func(_ context.Context, rc RateController) (int, error) {
			assert.False(t, rc.Waited())
			return 1, nil
		})
}

func TestTryExecute_PanicBecomesError(t *testing.T) {
	l := New()
	_, _, err := TryExecute(l, context.Background(),
		func(context.Context, RateController) (int, error) {
			panic("kaboom")
		})
	testx.RequirePanicError(t, err, opTryExecute)
}

func TestTryExecute_SkipTokenRefunds(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	before := l.Tokens()

	ok, _, err := TryExecute(l, context.Background(),
		func(_ context.Context, rc RateController) (int, error) {
			rc.SkipToken()
			return 1, nil
		})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.InDelta(t, before, l.Tokens(), 0.01)
	assert.Zero(t, l.Stats().Allowed)
}

// --- Stats & lifecycle ---

func TestLimiter_Stats(t *testing.T) {
	l := New(WithRate(5), WithBurst(2))
	require.True(t, l.Allow())
	require.True(t, l.Allow())
	require.False(t, l.Allow())

	s := l.Stats()
	assert.Equal(t, 5.0, s.Rate)
	assert.Equal(t, 2, s.Burst)
	assert.Equal(t, uint64(2), s.Allowed)
	assert.Equal(t, uint64(1), s.Limited)
}

func TestLimiter_Stats_BlockingWaitCountsAllowedOnce(t *testing.T) {
	// A successful blocking Wait must count as exactly one allowed and zero
	// limited, regardless of how many times the wait loop probed the bucket.
	l := New(WithRate(1000), WithBurst(1))
	require.True(t, l.Allow())

	ctx, cancel := testx.TimedCtx(time.Second)
	defer cancel()
	require.NoError(t, l.Wait(ctx))

	s := l.Stats()
	assert.Equal(t, uint64(2), s.Allowed)
	assert.Zero(t, s.Limited, "blocking probes must not inflate Limited")
}

func TestLimiter_Stats_CancelledWaitCountsLimitedOnce(t *testing.T) {
	l := New(WithRate(0.0001), WithBurst(1))
	require.True(t, l.Allow())

	ctx, cancel := testx.TimedCtx(20 * time.Millisecond)
	defer cancel()
	require.Error(t, l.Wait(ctx))

	s := l.Stats()
	assert.Equal(t, uint64(1), s.Allowed)
	assert.Equal(t, uint64(1), s.Limited, "a denied wait counts exactly once")
}

func TestExecute_Stats_WaitedCountsAllowedOnce(t *testing.T) {
	l := New(WithRate(1000), WithBurst(1))
	require.True(t, l.Allow())

	_, err := Execute(l, context.Background(),
		func(context.Context, RateController) (int, error) { return 1, nil })
	require.NoError(t, err)

	s := l.Stats()
	assert.Equal(t, uint64(2), s.Allowed)
	assert.Zero(t, s.Limited)
}

func TestExecute_Stats_CancelledCountsLimitedOnce(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	require.True(t, l.Allow())

	_, err := Execute(l, testx.CancelledCtx(),
		func(context.Context, RateController) (int, error) { return 1, nil })
	require.ErrorIs(t, err, ErrCancelled)

	s := l.Stats()
	assert.Equal(t, uint64(1), s.Limited)
}

func TestLimiter_ResetStats(t *testing.T) {
	l := New(WithRate(1), WithBurst(1))
	require.True(t, l.Allow())
	require.False(t, l.Allow())

	l.ResetStats()
	s := l.Stats()
	assert.Zero(t, s.Allowed)
	assert.Zero(t, s.Limited)
}

func TestLimiter_Reset(t *testing.T) {
	l := New(WithRate(0.0001), WithBurst(3))
	require.True(t, l.AllowN(3))
	require.False(t, l.Allow())

	l.Reset()
	assert.InDelta(t, 3.0, l.Tokens(), 0.01)
	assert.True(t, l.AllowN(3), "bucket should be full again")
}

// --- Concurrency ---

func TestLimiter_AllowRaceSafe(t *testing.T) {
	l := New(WithRate(1_000_000), WithBurst(1000))
	var admitted atomic.Int64
	testx.HammerVoid(50, 500, func() {
		if l.Allow() {
			admitted.Add(1)
		}
	})
	s := l.Stats()
	assert.Equal(t, uint64(admitted.Load()), s.Allowed)
}

func TestExecute_RaceSafe(t *testing.T) {
	l := New(WithRate(1_000_000), WithBurst(1000))
	testx.HammerNoError(t, 50, 200, func() error {
		_, _, err := TryExecute(l, context.Background(),
			func(context.Context, RateController) (int, error) { return 1, nil })
		return err
	})
}

// --- RateController ---

func TestExecute_ControllerSnapshot(t *testing.T) {
	l := New(WithRate(100), WithBurst(50))

	var snapshot struct {
		tokens float64
		rate   float64
		burst  int
		waited bool
	}
	_, err := Execute(l, context.Background(), func(_ context.Context, rc RateController) (int, error) {
		snapshot.tokens = rc.Tokens()
		snapshot.rate = rc.Rate()
		snapshot.burst = rc.Burst()
		snapshot.waited = rc.Waited()
		return 1, nil
	})
	require.NoError(t, err)
	assert.InDelta(t, 100.0, snapshot.rate, 0.01)
	assert.Equal(t, 50, snapshot.burst)
	assert.False(t, snapshot.waited, "non-blocking path should not report waiting")
	assert.GreaterOrEqual(t, snapshot.tokens, 0.0)
}

func TestController_SkipTokenRefunds(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10))
	before := l.Tokens()

	_, err := Execute(l, context.Background(), func(_ context.Context, rc RateController) (int, error) {
		rc.SkipToken()
		return 1, nil
	})
	require.NoError(t, err)
	after := l.Tokens()
	assert.InDelta(t, before, after, 1.5, "SkipToken should refund the consumed token")
}
