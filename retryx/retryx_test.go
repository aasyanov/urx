package retryx

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/aasyanov/urx/panix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastOpts disables jitter and uses a tiny backoff so timing-sensitive tests
// run quickly and deterministically.
func fastOpts(extra ...Option) []Option {
	return append([]Option{
		WithBackoff(time.Millisecond),
		WithMaxBackoff(2 * time.Millisecond),
		WithJitter(false),
	}, extra...)
}

// --- Do: happy paths ---

func TestDo_SucceedsFirstTry(t *testing.T) {
	var calls atomic.Int64
	got, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		calls.Add(1)
		return 42, nil
	}, fastOpts()...)
	require.NoError(t, err)
	assert.Equal(t, 42, got)
	assert.Equal(t, int64(1), calls.Load())
}

func TestDo_SucceedsAfterFailures(t *testing.T) {
	sim := testx.FailUntil(2) // fail twice, then succeed
	got, err := Do(context.Background(), func(context.Context, RetryController) (string, error) {
		return "ok", sim.Call()
	}, fastOpts(WithMaxAttempts(5))...)
	require.NoError(t, err)
	assert.Equal(t, "ok", got)
	assert.Equal(t, int64(3), sim.Calls())
}

// --- Do: error paths ---

func TestDo_NilFunc(t *testing.T) {
	_, err := Do[int](context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestDo_Exhausted(t *testing.T) {
	sim := testx.AlwaysFail()
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		return 0, sim.Call()
	}, fastOpts(WithMaxAttempts(3))...)
	require.ErrorIs(t, err, ErrExhausted)
	require.ErrorIs(t, err, testx.ErrSimulated)
	assert.ErrorContains(t, err, "attempts=3")
	assert.Equal(t, int64(3), sim.Calls())
}

func TestDo_Aborted(t *testing.T) {
	sentinel := errors.New("permanent")
	var calls atomic.Int64
	_, err := Do(context.Background(), func(_ context.Context, rc RetryController) (int, error) {
		calls.Add(1)
		rc.Abort()
		return 0, sentinel
	}, fastOpts(WithMaxAttempts(5))...)
	require.ErrorIs(t, err, ErrAborted)
	require.ErrorIs(t, err, sentinel)
	assert.ErrorContains(t, err, "attempt=1")
	assert.Equal(t, int64(1), calls.Load())
}

func TestDo_NonRetryableStopsImmediately(t *testing.T) {
	permanent := errors.New("permanent")
	var calls atomic.Int64
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		calls.Add(1)
		return 0, permanent
	}, fastOpts(
		WithMaxAttempts(5),
		WithRetryIf(func(err error) bool { return !errors.Is(err, permanent) }),
	)...)
	require.ErrorIs(t, err, ErrExhausted)
	require.ErrorIs(t, err, permanent)
	assert.ErrorContains(t, err, "attempts=1")
	assert.Equal(t, int64(1), calls.Load(), "non-retryable error must stop after one attempt")
}

func TestDo_NonRetryableReportsStoppingAttempt(t *testing.T) {
	transient := errors.New("transient")
	permanent := errors.New("permanent")
	var calls atomic.Int64
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		n := calls.Add(1)
		if n <= 2 {
			return 0, transient
		}
		return 0, permanent
	}, fastOpts(
		WithMaxAttempts(5),
		WithRetryIf(func(err error) bool { return !errors.Is(err, permanent) }),
	)...)
	require.ErrorIs(t, err, ErrExhausted)
	require.ErrorIs(t, err, permanent)
	assert.ErrorContains(t, err, "attempts=3")
	assert.Equal(t, int64(3), calls.Load())
}

func TestDo_CancelledBeforeFirstAttempt(t *testing.T) {
	var calls atomic.Int64
	_, err := Do(testx.CancelledCtx(), func(context.Context, RetryController) (int, error) {
		calls.Add(1)
		return 0, nil
	}, fastOpts()...)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, calls.Load())
}

func TestDo_CancelledWithExpiredContext(t *testing.T) {
	var calls atomic.Int64
	_, err := Do(testx.ExpiredCtx(), func(context.Context, RetryController) (int, error) {
		calls.Add(1)
		return 0, nil
	}, fastOpts()...)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Zero(t, calls.Load())
}

func TestDo_CancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sim := testx.AlwaysFail()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Do(ctx, func(context.Context, RetryController) (int, error) {
		return 0, sim.Call()
	}, WithMaxAttempts(100), WithBackoff(50*time.Millisecond), WithJitter(false))
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestDo_RecoversPanicAndRetries(t *testing.T) {
	var calls atomic.Int64
	got, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		if calls.Add(1) == 1 {
			panic("attempt 1 boom")
		}
		return 7, nil
	}, fastOpts(WithMaxAttempts(3))...)
	require.NoError(t, err)
	assert.Equal(t, 7, got)
	assert.Equal(t, int64(2), calls.Load())
}

func TestDo_PanicExhaustedReportsPanicError(t *testing.T) {
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		panic("always boom")
	}, fastOpts(WithMaxAttempts(2))...)
	require.ErrorIs(t, err, ErrExhausted)
	testx.RequirePanicError(t, err, opDo)
}

func TestDo_PanicNonRetryableStopsImmediately(t *testing.T) {
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		panic("non-retryable boom")
	}, fastOpts(
		WithMaxAttempts(5),
		WithRetryIf(func(err error) bool {
			var pe *panix.PanicError
			return !errors.As(err, &pe)
		}),
	)...)
	require.ErrorIs(t, err, ErrExhausted)
	assert.ErrorContains(t, err, "attempts=1")
	testx.RequirePanicError(t, err, opDo)
}

func TestDo_AbortAfterPanicReturnsAborted(t *testing.T) {
	_, err := Do(context.Background(), func(_ context.Context, rc RetryController) (int, error) {
		rc.Abort()
		panic("boom after abort")
	}, fastOpts(WithMaxAttempts(5))...)
	require.ErrorIs(t, err, ErrAborted)
	assert.ErrorContains(t, err, "attempt=1")
	testx.RequirePanicError(t, err, opDo)
}

func TestDo_CustomOpInPanic(t *testing.T) {
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		panic("boom")
	}, fastOpts(WithMaxAttempts(1), WithOp("api.fetch"))...)
	require.ErrorIs(t, err, ErrExhausted)
	testx.RequirePanicError(t, err, "api.fetch")
}

func TestDo_SingleAttemptNoRetry(t *testing.T) {
	// A non-positive budget degrades to exactly one execution with no backoff.
	sim := testx.AlwaysFail()
	var retries atomic.Int64
	start := time.Now()
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		return 0, sim.Call()
	}, WithMaxAttempts(0), WithBackoff(time.Hour), WithOnRetry(func(int, error) { retries.Add(1) }))

	require.ErrorIs(t, err, ErrExhausted)
	assert.ErrorContains(t, err, "attempts=1")
	assert.Equal(t, int64(1), sim.Calls(), "must execute exactly once")
	assert.Zero(t, retries.Load(), "no onRetry without a retry")
	assert.Less(t, time.Since(start), time.Second, "must not sleep when there is no retry")
}

func TestDo_CustomBackoffDelaysBetweenAttempts(t *testing.T) {
	sim := testx.FailUntil(1)
	start := time.Now()
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		return 1, sim.Call()
	},
		WithMaxAttempts(3),
		WithBackoff(40*time.Millisecond),
		WithMaxBackoff(time.Hour),
		WithJitter(false),
	)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond)
	assert.Equal(t, int64(2), sim.Calls())
}

// --- RetryController ---

func TestController_NumberLastErrElapsed(t *testing.T) {
	var seen []int
	var lastErrs []error
	sim := testx.FailUntil(2)

	_, err := Do(context.Background(), func(_ context.Context, rc RetryController) (int, error) {
		seen = append(seen, rc.Number())
		lastErrs = append(lastErrs, rc.LastErr())
		assert.GreaterOrEqual(t, rc.Elapsed(), time.Duration(0))
		if e := sim.Call(); e != nil {
			return 0, e
		}
		return 1, nil
	}, fastOpts(WithMaxAttempts(5))...)
	require.NoError(t, err)

	assert.Equal(t, []int{1, 2, 3}, seen)
	assert.Nil(t, lastErrs[0])
	require.Error(t, lastErrs[1])
	require.Error(t, lastErrs[2])
}

func TestController_AbortIdempotent(t *testing.T) {
	_, err := Do(context.Background(), func(_ context.Context, rc RetryController) (int, error) {
		rc.Abort()
		rc.Abort()
		return 0, errors.New("x")
	}, fastOpts()...)
	require.ErrorIs(t, err, ErrAborted)
}

// --- onRetry callback ---

func TestDo_OnRetryInvokedPerFailure(t *testing.T) {
	sim := testx.FailUntil(2)
	var retries atomic.Int64
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		return 0, sim.Call()
	}, fastOpts(
		WithMaxAttempts(5),
		WithOnRetry(func(int, error) { retries.Add(1) }),
	)...)
	require.NoError(t, err)
	// 2 failures => 2 onRetry calls (the 3rd attempt succeeds).
	assert.Equal(t, int64(2), retries.Load())
}

func TestDo_OnRetryNotCalledOnLastAttempt(t *testing.T) {
	sim := testx.AlwaysFail()
	var retries atomic.Int64
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		return 0, sim.Call()
	}, fastOpts(
		WithMaxAttempts(3),
		WithOnRetry(func(int, error) { retries.Add(1) }),
	)...)
	require.ErrorIs(t, err, ErrExhausted)
	// 3 attempts, but onRetry fires only between attempts => 2 calls.
	assert.Equal(t, int64(2), retries.Load())
}

func TestDo_CancelledOnLastAttemptReturnsErrCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int64
	_, err := Do(ctx, func(context.Context, RetryController) (int, error) {
		if calls.Add(1) == 2 {
			cancel()
			return 0, errors.New("last fail")
		}
		return 0, errors.New("fail")
	}, fastOpts(WithMaxAttempts(2))...)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrExhausted)
	assert.Equal(t, int64(2), calls.Load())
}

func TestDo_LastAttemptCtxErrNotExhausted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	var calls atomic.Int64
	_, err := Do(ctx, func(ctx context.Context, _ RetryController) (int, error) {
		calls.Add(1)
		<-ctx.Done()
		return 0, ctx.Err()
	}, WithMaxAttempts(1))
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrExhausted)
	assert.Equal(t, int64(1), calls.Load())
}

func TestDo_OnRetryPanicBecomesPanicError(t *testing.T) {
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		return 0, errors.New("fail")
	}, fastOpts(
		WithMaxAttempts(5),
		WithOnRetry(func(int, error) { panic("hook boom") }),
	)...)
	testx.RequirePanicError(t, err, opDo)
	assert.NotErrorIs(t, err, ErrExhausted)
}

func TestDo_OnRetryPanicDoesNotContinueRetrying(t *testing.T) {
	var calls atomic.Int64
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		calls.Add(1)
		return 0, errors.New("fail")
	}, fastOpts(
		WithMaxAttempts(5),
		WithOnRetry(func(int, error) { panic("hook boom") }),
	)...)
	testx.RequirePanicError(t, err, opDo)
	assert.Equal(t, int64(1), calls.Load(), "OnRetry panic must stop the retry loop")
}

func TestDo_MaxElapsedStopsBeforeNextAttempt(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	var afterFirst atomic.Bool
	cause := errors.New("still failing")
	var calls atomic.Int64
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		calls.Add(1)
		afterFirst.Store(true)
		return 0, cause
	},
		WithMaxAttempts(5),
		WithMaxElapsed(time.Minute),
		WithBackoff(time.Hour),
		WithJitter(false),
		withClock(func() time.Time {
			if afterFirst.Load() {
				return base.Add(time.Hour)
			}
			return base
		}),
	)
	require.ErrorIs(t, err, ErrMaxElapsed)
	require.ErrorIs(t, err, cause)
	assert.NotErrorIs(t, err, ErrExhausted)
	assert.Equal(t, int64(1), calls.Load())
}

func TestDo_MaxElapsedShortensSleep(t *testing.T) {
	cause := errors.New("fail")
	var calls atomic.Int64
	start := time.Now()
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		calls.Add(1)
		return 0, cause
	},
		WithMaxAttempts(5),
		WithMaxElapsed(40*time.Millisecond),
		WithBackoff(time.Hour),
		WithJitter(false),
	)
	require.ErrorIs(t, err, ErrMaxElapsed)
	require.ErrorIs(t, err, cause)
	assert.Equal(t, int64(1), calls.Load())
	assert.Less(t, time.Since(start), time.Second, "sleep must be clamped to remaining max-elapsed")
}

func TestDo_MaxElapsedZeroUnchanged(t *testing.T) {
	sim := testx.AlwaysFail()
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		return 0, sim.Call()
	}, fastOpts(WithMaxAttempts(3), WithMaxElapsed(0))...)
	require.ErrorIs(t, err, ErrExhausted)
	assert.NotErrorIs(t, err, ErrMaxElapsed)
	assert.Equal(t, int64(3), sim.Calls())
}

func TestDo_DelayFuncOverridesExponential(t *testing.T) {
	var (
		seenAttempt int
		seenErr     error
		calls       atomic.Int64
	)
	cause := errors.New("transient")
	start := time.Now()
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		calls.Add(1)
		return 0, cause
	},
		WithMaxAttempts(2),
		WithBackoff(time.Hour),
		WithJitter(false),
		WithDelayFunc(func(attempt int, err error) time.Duration {
			seenAttempt = attempt
			seenErr = err
			return 0
		}),
	)
	require.ErrorIs(t, err, ErrExhausted)
	assert.Equal(t, 1, seenAttempt, "DelayFunc attempt is 1-based like OnRetry")
	require.ErrorIs(t, seenErr, cause)
	assert.Equal(t, int64(2), calls.Load())
	assert.Less(t, time.Since(start), time.Second, "DelayFunc must replace hour-long exponential backoff")
}

func TestDo_DelayFuncNilIgnored(t *testing.T) {
	sim := testx.FailUntil(1)
	start := time.Now()
	_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
		return 1, sim.Call()
	},
		WithMaxAttempts(3),
		WithBackoff(40*time.Millisecond),
		WithMaxBackoff(time.Hour),
		WithJitter(false),
		WithDelayFunc(nil),
	)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond)
	assert.Equal(t, int64(2), sim.Calls())
}

// --- Options ---

func TestNewConfig_Defaults(t *testing.T) {
	cfg := newConfig(nil)
	assert.Equal(t, DefaultMaxAttempts, cfg.maxAttempts)
	assert.Equal(t, DefaultBackoff, cfg.backoff)
	assert.Equal(t, DefaultMaxBackoff, cfg.maxBackoff)
	assert.Equal(t, jitterModeMultiplicative, cfg.jitterMode)
	assert.Zero(t, cfg.maxElapsed)
	assert.Nil(t, cfg.retryIf)
	assert.Nil(t, cfg.onRetry)
	assert.Nil(t, cfg.delayFunc)
}

func TestNewConfig_AttemptFloor(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want int
	}{
		{name: "zero floors to 1", opt: WithMaxAttempts(0), want: minAttempts},
		{name: "negative floors to 1", opt: WithMaxAttempts(-5), want: minAttempts},
		{name: "custom kept", opt: WithMaxAttempts(7), want: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newConfig([]Option{tt.opt})
			assert.Equal(t, tt.want, cfg.maxAttempts)
		})
	}
}

func TestOptions_IgnoreNonPositive(t *testing.T) {
	cfg := newConfig([]Option{
		WithBackoff(-1),
		WithMaxBackoff(0),
		WithMaxElapsed(0),
		WithMaxElapsed(-time.Second),
	})
	assert.Equal(t, DefaultBackoff, cfg.backoff)
	assert.Equal(t, DefaultMaxBackoff, cfg.maxBackoff)
	assert.Zero(t, cfg.maxElapsed)
}

func TestNewConfig_SkipsNilOption(t *testing.T) {
	cfg := newConfig([]Option{
		WithMaxAttempts(5),
		nil,
		WithJitter(false),
	})
	assert.Equal(t, 5, cfg.maxAttempts)
	assert.Equal(t, jitterModeOff, cfg.jitterMode)
}

func TestOptions_ApplyCustomValues(t *testing.T) {
	retryIf := func(error) bool { return true }
	onRetry := func(int, error) {}

	tests := []struct {
		name string
		opts []Option
		want config
	}{
		{
			name: "custom backoff",
			opts: []Option{WithBackoff(500 * time.Millisecond)},
			want: config{maxAttempts: DefaultMaxAttempts, backoff: 500 * time.Millisecond, maxBackoff: DefaultMaxBackoff, jitterMode: jitterModeMultiplicative},
		},
		{
			name: "custom max backoff",
			opts: []Option{WithMaxBackoff(30 * time.Second)},
			want: config{maxAttempts: DefaultMaxAttempts, backoff: DefaultBackoff, maxBackoff: 30 * time.Second, jitterMode: jitterModeMultiplicative},
		},
		{
			name: "jitter disabled",
			opts: []Option{WithJitter(false)},
			want: config{maxAttempts: DefaultMaxAttempts, backoff: DefaultBackoff, maxBackoff: DefaultMaxBackoff, jitterMode: jitterModeOff},
		},
		{
			name: "jitter re-enabled",
			opts: []Option{WithJitter(false), WithJitter(true)},
			want: config{maxAttempts: DefaultMaxAttempts, backoff: DefaultBackoff, maxBackoff: DefaultMaxBackoff, jitterMode: jitterModeMultiplicative},
		},
		{
			name: "custom max attempts",
			opts: []Option{WithMaxAttempts(9)},
			want: config{maxAttempts: 9, backoff: DefaultBackoff, maxBackoff: DefaultMaxBackoff, jitterMode: jitterModeMultiplicative},
		},
		{
			name: "custom op",
			opts: []Option{WithOp("api.fetch")},
			want: config{maxAttempts: DefaultMaxAttempts, backoff: DefaultBackoff, maxBackoff: DefaultMaxBackoff, jitterMode: jitterModeMultiplicative, op: "api.fetch"},
		},
		{
			name: "retryIf and onRetry wired",
			opts: []Option{WithRetryIf(retryIf), WithOnRetry(onRetry)},
			want: config{maxAttempts: DefaultMaxAttempts, backoff: DefaultBackoff, maxBackoff: DefaultMaxBackoff, jitterMode: jitterModeMultiplicative, retryIf: retryIf, onRetry: onRetry},
		},
		{
			name: "equal jitter",
			opts: []Option{WithEqualJitter()},
			want: config{maxAttempts: DefaultMaxAttempts, backoff: DefaultBackoff, maxBackoff: DefaultMaxBackoff, jitterMode: jitterModeEqual},
		},
		{
			name: "max elapsed",
			opts: []Option{WithMaxElapsed(time.Second)},
			want: config{maxAttempts: DefaultMaxAttempts, backoff: DefaultBackoff, maxBackoff: DefaultMaxBackoff, jitterMode: jitterModeMultiplicative, maxElapsed: time.Second},
		},
		{
			name: "delay func",
			opts: []Option{WithDelayFunc(func(int, error) time.Duration { return time.Second })},
			want: config{maxAttempts: DefaultMaxAttempts, backoff: DefaultBackoff, maxBackoff: DefaultMaxBackoff, jitterMode: jitterModeMultiplicative},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newConfig(tt.opts)
			assert.Equal(t, tt.want.maxAttempts, got.maxAttempts)
			assert.Equal(t, tt.want.backoff, got.backoff)
			assert.Equal(t, tt.want.maxBackoff, got.maxBackoff)
			assert.Equal(t, tt.want.jitterMode, got.jitterMode)
			assert.Equal(t, tt.want.maxElapsed, got.maxElapsed)
			assert.Equal(t, tt.want.op, got.op)
			if tt.want.retryIf != nil {
				assert.NotNil(t, got.retryIf)
			} else {
				assert.Nil(t, got.retryIf)
			}
			if tt.want.onRetry != nil {
				assert.NotNil(t, got.onRetry)
			} else {
				assert.Nil(t, got.onRetry)
			}
			if tt.name == "delay func" {
				assert.NotNil(t, got.delayFunc)
			} else {
				assert.Nil(t, got.delayFunc)
			}
		})
	}
}

func TestNewConfig_OpOrDefault(t *testing.T) {
	assert.Equal(t, opDo, newConfig(nil).opOrDefault())
	assert.Equal(t, "api.fetch", newConfig([]Option{WithOp("api.fetch")}).opOrDefault())
	assert.Equal(t, opDo, newConfig([]Option{WithOp("")}).opOrDefault())
}

func TestNewConfig_WithClockNilIgnored(t *testing.T) {
	cfg := newConfig([]Option{withClock(nil)})
	assert.Nil(t, cfg.nowFn)
}

// --- Backoff ---

func TestBackoff_ExponentialWithoutJitter(t *testing.T) {
	cfg := config{backoff: 100 * time.Millisecond, maxBackoff: time.Hour, jitterMode: jitterModeOff}
	assert.Equal(t, 100*time.Millisecond, backoff(&cfg, 0))
	assert.Equal(t, 200*time.Millisecond, backoff(&cfg, 1))
	assert.Equal(t, 400*time.Millisecond, backoff(&cfg, 2))
}

func TestBackoff_CapsAtMax(t *testing.T) {
	cfg := config{backoff: time.Second, maxBackoff: 3 * time.Second, jitterMode: jitterModeOff}
	assert.Equal(t, 3*time.Second, backoff(&cfg, 10))
}

func TestBackoff_JitterWithinWindow(t *testing.T) {
	cfg := config{backoff: 100 * time.Millisecond, maxBackoff: time.Hour, jitterMode: jitterModeMultiplicative}
	base := 200 * time.Millisecond // attempt 1 nominal
	for range 1000 {
		d := backoff(&cfg, 1)
		assert.GreaterOrEqual(t, d, time.Duration(float64(base)*jitterFloor))
		assert.Less(t, d, time.Duration(float64(base)*(jitterFloor+jitterSpan)))
	}
}

func TestBackoff_EqualJitterWithinHalfToFull(t *testing.T) {
	cfg := config{backoff: 100 * time.Millisecond, maxBackoff: time.Hour, jitterMode: jitterModeEqual}
	base := 200 * time.Millisecond // attempt 1 nominal
	for range 1000 {
		d := backoff(&cfg, 1)
		assert.GreaterOrEqual(t, d, base/2)
		assert.Less(t, d, base)
	}
}

func TestBackoff_DefaultJitterUnchanged(t *testing.T) {
	cfg := newConfig(nil)
	assert.Equal(t, jitterModeMultiplicative, cfg.jitterMode)
	base := float64(DefaultBackoff)
	for range 200 {
		d := backoff(&cfg, 0)
		assert.GreaterOrEqual(t, d, time.Duration(base*jitterFloor))
		assert.Less(t, d, time.Duration(base*(jitterFloor+jitterSpan)))
	}
}

// --- sleep ---

func TestSleep_ZeroReturnsContextErr(t *testing.T) {
	assert.NoError(t, sleep(context.Background(), 0))
	assert.NoError(t, sleep(context.Background(), -time.Nanosecond))
	assert.ErrorIs(t, sleep(testx.CancelledCtx(), 0), context.Canceled)
}

func TestSleep_CompletesAfterDelay(t *testing.T) {
	start := time.Now()
	require.NoError(t, sleep(context.Background(), 10*time.Millisecond))
	assert.GreaterOrEqual(t, time.Since(start), 10*time.Millisecond)
}

func TestSleep_CancelledStopsTimer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := sleep(ctx, time.Hour)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), time.Second, "cancel must not wait out the full delay")
}

func TestStopTimer_StopsPendingTimer(t *testing.T) {
	timer := time.NewTimer(time.Hour)
	stopTimer(timer)
	assert.False(t, timer.Stop(), "already stopped")
}

func TestStopTimer_DrainsAlreadyFiredTimer(t *testing.T) {
	timer := time.NewTimer(time.Millisecond)
	<-timer.C
	stopTimer(timer) // Stop returns false with an empty channel; exercises default branch
}

// --- isRetryable ---

func TestIsRetryable(t *testing.T) {
	none := config{}
	assert.True(t, isRetryable(&none, errors.New("x")), "default retries all errors")

	custom := config{retryIf: func(err error) bool { return err.Error() == "yes" }}
	assert.True(t, isRetryable(&custom, errors.New("yes")))
	assert.False(t, isRetryable(&custom, errors.New("no")))
}

// --- Concurrency ---

func TestDo_ConcurrentRaceSafe(t *testing.T) {
	testx.HammerNoError(t, 50, 200, func() error {
		sim := testx.FailUntil(1)
		_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
			return 1, sim.Call()
		}, fastOpts(WithMaxAttempts(3))...)
		return err
	})
}
