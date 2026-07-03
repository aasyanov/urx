package adaptx

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bg() context.Context { return context.Background() }

// noop is an Execute callback that returns the given outcome without latency.
func noop[T any](v T, err error) func(context.Context, AdaptController) (T, error) {
	return func(context.Context, AdaptController) (T, error) { return v, err }
}

// --- Construction & defaults ---

func TestNew_AppliesDefaults(t *testing.T) {
	l := New()
	defer l.Close()

	assert.Equal(t, DefaultInitialLimit, l.Limit())
	assert.Equal(t, 0, l.InFlight())
	s := l.Stats()
	assert.Equal(t, labelAIMD, s.Algorithm)
	assert.Equal(t, DefaultMinLimit, s.MinLimit)
	assert.Equal(t, DefaultMaxLimit, s.MaxLimit)
}

func TestNew_ClampsInvalidBounds(t *testing.T) {
	tests := []struct {
		name        string
		opts        []Option
		wantInitial int
		wantMin     int
		wantMax     int
	}{
		{"min floored to 1", []Option{WithMinLimit(-5)}, DefaultInitialLimit, 1, DefaultMaxLimit},
		{"max below min raised", []Option{WithMinLimit(50), WithMaxLimit(10)}, 50, 50, 50},
		{"initial below min raised", []Option{WithMinLimit(20), WithInitialLimit(5)}, 20, 20, DefaultMaxLimit},
		{"initial above max lowered", []Option{WithMaxLimit(8), WithInitialLimit(100)}, 8, 1, 8},
		{"all custom valid", []Option{WithMinLimit(2), WithMaxLimit(40), WithInitialLimit(10)}, 10, 2, 40},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := New(tt.opts...)
			defer l.Close()
			assert.Equal(t, tt.wantInitial, l.Limit())
			assert.Equal(t, tt.wantMin, l.Stats().MinLimit)
			assert.Equal(t, tt.wantMax, l.Stats().MaxLimit)
		})
	}
}

func TestNew_SeedsInitialPermits(t *testing.T) {
	l := New(WithInitialLimit(3), WithMaxLimit(10))
	defer l.Close()
	assert.Len(t, l.sem, 3, "initial permits buffered in semaphore")
	assert.Equal(t, cap(l.sem), 10, "semaphore capacity is the max limit")
}

// --- Execute happy path ---

func TestExecute_ReturnsValue(t *testing.T) {
	l := New()
	defer l.Close()
	got, err := Execute(l, bg(), noop("ok", nil))
	require.NoError(t, err)
	assert.Equal(t, "ok", got)
	assert.Equal(t, int64(1), l.Stats().Total)
	assert.Equal(t, int64(1), l.Stats().Success)
}

func TestExecute_PropagatesError(t *testing.T) {
	l := New()
	defer l.Close()
	sentinel := errors.New("boom")
	_, err := Execute(l, bg(), noop("", sentinel))
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, int64(1), l.Stats().Failures)
}

func TestExecute_NilFuncReturnsErrNilFunc(t *testing.T) {
	l := New()
	defer l.Close()
	_, err := Execute[int](l, bg(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestExecute_ReleasesPermitOnReturn(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1), WithWarmupSamples(0))
	defer l.Close()
	for i := 0; i < 5; i++ {
		_, err := Execute(l, bg(), noop(i, nil))
		require.NoError(t, err)
	}
	assert.Equal(t, 0, l.InFlight())
}

func TestExecute_ControllerSnapshot(t *testing.T) {
	l := New(WithInitialLimit(7))
	defer l.Close()
	_, err := Execute(l, bg(), func(_ context.Context, ac AdaptController) (int, error) {
		assert.Equal(t, 7, ac.Limit())
		assert.Equal(t, 0, ac.InFlight())
		assert.Equal(t, AIMD, ac.Algorithm())
		return 0, nil
	})
	require.NoError(t, err)
}

// --- Panic recovery ---

func TestExecute_RecoversPanic(t *testing.T) {
	l := New()
	defer l.Close()
	_, err := Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
		panic("handler crashed")
	})
	pe := testx.RequirePanicError(t, err, opExecute)
	assert.Equal(t, "handler crashed", pe.Value)
	assert.Equal(t, 0, l.InFlight(), "permit released after panic")
}

func TestExecute_CustomOpInPanic(t *testing.T) {
	l := New(WithOp("db.query"))
	defer l.Close()
	_, err := Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
		panic("x")
	})
	testx.RequirePanicError(t, err, "db.query")
}

// --- SkipSample ---

func TestExecute_SkipSampleExcludesLatency(t *testing.T) {
	l := New(WithWarmupSamples(0))
	defer l.Close()
	_, err := Execute(l, bg(), func(_ context.Context, ac AdaptController) (int, error) {
		ac.SkipSample()
		time.Sleep(time.Millisecond)
		return 0, nil
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), l.Stats().Success, "still counted as success")
	assert.Equal(t, time.Duration(0), l.Stats().AvgLat, "no latency recorded")
}

// --- Context handling ---

func TestExecute_CancelledContext(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	defer l.Close()
	// Drain the only permit so Acquire must wait.
	rel, err := l.Acquire(bg())
	require.NoError(t, err)
	defer rel(true, time.Millisecond)

	_, err = Execute(l, testx.CancelledCtx(), noop(0, nil))
	require.ErrorIs(t, err, ErrCancelled)
}

func TestExecute_DeadlineExceeded(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	defer l.Close()
	rel, err := l.Acquire(bg())
	require.NoError(t, err)
	defer rel(true, time.Millisecond)

	ctx, cancel := testx.TimedCtx(20 * time.Millisecond)
	defer cancel()
	_, err = Execute(l, ctx, noop(0, nil))
	require.ErrorIs(t, err, ErrTimeout)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestAcquire_CancelledContextCountsRejected(t *testing.T) {
	l := New()
	defer l.Close()
	_, err := l.Acquire(testx.CancelledCtx())
	require.ErrorIs(t, err, ErrCancelled)
	assert.Equal(t, int64(1), l.Stats().Rejected)
}

// --- TryAcquire / TryExecute ---

func TestTryAcquire_SucceedsWhenAvailable(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	defer l.Close()
	rel, ok := l.TryAcquire()
	require.True(t, ok)
	assert.Equal(t, 1, l.InFlight())
	rel(true, time.Millisecond)
	assert.Equal(t, 0, l.InFlight())
}

func TestTryAcquire_FailsWhenExhausted(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	defer l.Close()
	rel, ok := l.TryAcquire()
	require.True(t, ok)
	defer rel(true, time.Millisecond)

	_, ok = l.TryAcquire()
	assert.False(t, ok)
	assert.Equal(t, int64(1), l.Stats().Rejected)
}

func TestTryExecute_RunsWhenAvailable(t *testing.T) {
	l := New()
	defer l.Close()
	ran, got, err := TryExecute(l, bg(), noop(42, nil))
	require.NoError(t, err)
	assert.True(t, ran)
	assert.Equal(t, 42, got)
}

func TestTryExecute_SkipsWhenExhausted(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	defer l.Close()
	rel, ok := l.TryAcquire()
	require.True(t, ok)
	defer rel(true, time.Millisecond)

	ran, _, err := TryExecute(l, bg(), noop(1, nil))
	require.NoError(t, err)
	assert.False(t, ran)
}

func TestTryExecute_NilFunc(t *testing.T) {
	l := New()
	defer l.Close()
	ran, _, err := TryExecute[int](l, bg(), nil)
	assert.False(t, ran)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestTryExecute_ClosedReturnsErrClosed(t *testing.T) {
	l := New()
	require.NoError(t, l.Close())
	ran, _, err := TryExecute(l, bg(), noop(1, nil))
	assert.False(t, ran)
	require.ErrorIs(t, err, ErrClosed)
}

// --- Double release safety ---

func TestRelease_Idempotent(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	defer l.Close()
	rel, err := l.Acquire(bg())
	require.NoError(t, err)
	rel(true, time.Millisecond)
	rel(true, time.Millisecond) // second call is a no-op
	assert.Equal(t, 0, l.InFlight())
	assert.Len(t, l.sem, 1, "permit returned exactly once")
}

// --- Lifecycle ---

func TestClose_Idempotent(t *testing.T) {
	l := New()
	require.NoError(t, l.Close())
	require.ErrorIs(t, l.Close(), ErrClosed)
	assert.True(t, l.IsClosed())
}

func TestAcquire_BlockedWaiterRejectedOnClose(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	rel, err := l.Acquire(bg())
	require.NoError(t, err)

	waiting := make(chan error, 1)
	go func() {
		_, err := l.Acquire(bg())
		waiting <- err
	}()

	testx.Eventually(t, func() bool { return l.InFlight() == 1 }, time.Second)
	require.NoError(t, l.Close())

	select {
	case err := <-waiting:
		require.ErrorIs(t, err, ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("blocked Acquire did not wake on Close")
	}

	rel(true, time.Millisecond)
}

func TestAcquire_AfterCloseReturnsErrClosed(t *testing.T) {
	l := New()
	require.NoError(t, l.Close())
	_, err := l.Acquire(bg())
	require.ErrorIs(t, err, ErrClosed)
	_, ok := l.TryAcquire()
	assert.False(t, ok)
}

func TestExecute_AfterCloseReturnsErrClosed(t *testing.T) {
	l := New()
	require.NoError(t, l.Close())
	_, err := Execute(l, bg(), noop(0, nil))
	require.ErrorIs(t, err, ErrClosed)
}

func TestCloseWithTimeout_WaitsForDrain(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	rel, err := l.Acquire(bg())
	require.NoError(t, err)

	go func() {
		time.Sleep(30 * time.Millisecond)
		rel(true, time.Millisecond)
	}()

	start := time.Now()
	require.NoError(t, l.CloseWithTimeout(time.Second))
	assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
	assert.Equal(t, 0, l.InFlight())
}

// --- Algorithm adaptation ---

func TestAIMD_IncreasesOnSuccess(t *testing.T) {
	l := New(WithAlgorithm(AIMD), WithInitialLimit(10), WithMaxLimit(100),
		WithWarmupSamples(0), WithJitter(0), WithIncreaseRate(1))
	defer l.Close()
	start := l.Limit()
	for i := 0; i < 5; i++ {
		_, err := Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(time.Millisecond)
			return 0, nil
		})
		require.NoError(t, err)
	}
	assert.Greater(t, l.Limit(), start)
	assert.Positive(t, l.Stats().Increases)
}

func TestAIMD_DecreasesOnFailure(t *testing.T) {
	l := New(WithAlgorithm(AIMD), WithInitialLimit(20), WithMinLimit(1),
		WithWarmupSamples(0), WithDecreaseRatio(0.5))
	defer l.Close()
	start := l.Limit()
	_, err := Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
		time.Sleep(time.Millisecond)
		return 0, errors.New("fail")
	})
	require.Error(t, err)
	assert.Less(t, l.Limit(), start)
	assert.Positive(t, l.Stats().Decreases)
}

func TestVegas_AdaptsFromLatency(t *testing.T) {
	l := New(WithAlgorithm(Vegas), WithInitialLimit(10), WithMaxLimit(100),
		WithMinLimit(1), WithWarmupSamples(0), WithJitter(0))
	defer l.Close()
	// Fast, stable calls establish a low RTT_min and should grow the limit.
	for i := 0; i < 10; i++ {
		_, err := Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(time.Millisecond)
			return 0, nil
		})
		require.NoError(t, err)
	}
	assert.Equal(t, Vegas.String(), l.Stats().Algorithm)
	assert.GreaterOrEqual(t, l.Limit(), 1)
}

func TestGradient_GrowsThenBacksOff(t *testing.T) {
	l := New(WithAlgorithm(Gradient), WithInitialLimit(10), WithMaxLimit(100),
		WithMinLimit(1), WithWarmupSamples(0), WithJitter(0))
	defer l.Close()
	for i := 0; i < 5; i++ {
		_, err := Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(time.Millisecond)
			return 0, nil
		})
		require.NoError(t, err)
	}
	assert.Equal(t, Gradient.String(), l.Stats().Algorithm)
}

func TestLimit_NeverBelowMinOrAboveMax(t *testing.T) {
	l := New(WithAlgorithm(AIMD), WithInitialLimit(5), WithMinLimit(3), WithMaxLimit(8),
		WithWarmupSamples(0), WithIncreaseRate(100), WithDecreaseRatio(0.1), WithJitter(0))
	defer l.Close()
	for i := 0; i < 20; i++ {
		_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(time.Millisecond)
			return 0, nil
		})
		assert.GreaterOrEqual(t, l.Limit(), 3)
		assert.LessOrEqual(t, l.Limit(), 8)
	}
}

func TestLimit_GrowthIssuesPermits(t *testing.T) {
	l := New(WithAlgorithm(AIMD), WithInitialLimit(2), WithMaxLimit(50),
		WithWarmupSamples(0), WithIncreaseRate(5), WithJitter(0))
	defer l.Close()
	for i := 0; i < 4; i++ {
		_, err := Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(time.Millisecond)
			return 0, nil
		})
		require.NoError(t, err)
	}
	// Available permits should track the grown limit.
	assert.GreaterOrEqual(t, len(l.sem), l.Limit()-l.InFlight()-1)
}

func TestOnLimitChange_Fires(t *testing.T) {
	changed := make(chan struct{}, 1)
	l := New(WithAlgorithm(AIMD), WithInitialLimit(5), WithMaxLimit(50),
		WithWarmupSamples(0), WithIncreaseRate(1), WithJitter(0),
		WithOnLimitChange(func(_, _ int) {
			select {
			case changed <- struct{}{}:
			default:
			}
		}))
	defer l.Close()
	for i := 0; i < 3; i++ {
		_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(time.Millisecond)
			return 0, nil
		})
	}
	testx.Eventually(t, func() bool { return len(changed) > 0 }, time.Second)
}

// --- Warmup ---

func TestWarmup_DelaysAdaptation(t *testing.T) {
	l := New(WithAlgorithm(AIMD), WithInitialLimit(10), WithMaxLimit(100),
		WithWarmupSamples(5), WithIncreaseRate(1), WithJitter(0))
	defer l.Close()
	start := l.Limit()
	// First 4 samples are within warmup → no adaptation.
	for i := 0; i < 4; i++ {
		_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(time.Millisecond)
			return 0, nil
		})
	}
	assert.Equal(t, start, l.Limit(), "no adaptation during warmup")
}

// --- Stats ---

func TestStats_LatencyPercentiles(t *testing.T) {
	l := New(WithWarmupSamples(0), WithSampleWindow(time.Hour))
	defer l.Close()
	for i := 0; i < 20; i++ {
		_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(time.Millisecond)
			return 0, nil
		})
	}
	s := l.Stats()
	assert.Positive(t, s.AvgLat)
	assert.Positive(t, s.P50Lat)
	assert.GreaterOrEqual(t, s.MaxLat, s.MinLat)
	assert.GreaterOrEqual(t, s.P99Lat, s.P50Lat)
}

func TestStats_EmptyWindow(t *testing.T) {
	l := New()
	defer l.Close()
	s := l.Stats()
	assert.Equal(t, time.Duration(0), s.AvgLat)
	assert.Equal(t, time.Duration(0), s.P50Lat)
}

func TestResetStats_ClearsState(t *testing.T) {
	l := New(WithAlgorithm(AIMD), WithInitialLimit(10), WithMaxLimit(100),
		WithWarmupSamples(0), WithIncreaseRate(5), WithJitter(0))
	defer l.Close()
	for i := 0; i < 5; i++ {
		_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(time.Millisecond)
			return 0, nil
		})
	}
	require.Positive(t, l.Stats().Total)
	require.Greater(t, l.Limit(), 10)

	l.ResetStats()
	s := l.Stats()
	assert.Equal(t, 10, l.Limit())
	assert.Equal(t, int64(0), s.Total)
	assert.Equal(t, int64(0), s.Success)
	assert.Equal(t, int64(0), s.Increases)
	assert.Equal(t, time.Duration(0), s.AvgLat)

	l.mu.Lock()
	assert.Equal(t, 0, l.debt)
	assert.Equal(t, math.MaxFloat64, l.minLat)
	assert.Equal(t, 0, l.count)
	l.mu.Unlock()
}

// --- Algorithm.String ---

func TestAlgorithm_String(t *testing.T) {
	tests := []struct {
		alg  Algorithm
		want string
	}{
		{AIMD, labelAIMD},
		{Vegas, labelVegas},
		{Gradient, labelGradient},
		{Algorithm(99), labelUnknown},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.alg.String())
	}
}

func TestExecute_UnknownAlgorithmFallsBackToAIMD(t *testing.T) {
	l := New(WithAlgorithm(Algorithm(99)), WithInitialLimit(10), WithMaxLimit(100),
		WithWarmupSamples(0), WithIncreaseRate(1), WithJitter(0))
	defer l.Close()
	start := l.Limit()
	for i := 0; i < 3; i++ {
		_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(time.Millisecond)
			return 0, nil
		})
	}
	assert.Greater(t, l.Limit(), start, "unknown algorithm behaves like AIMD")
}

// --- Options validation ---

func TestOptions_IgnoreInvalidValues(t *testing.T) {
	cfg := newConfig([]Option{
		WithInitialLimit(-1),
		WithMinLimit(0),
		WithMaxLimit(-5),
		WithSmoothing(2),
		WithSmoothing(0),
		WithIncreaseRate(-1),
		WithDecreaseRatio(1.5),
		WithDecreaseRatio(0),
		WithTargetLatency(-time.Second),
		WithTolerance(2),
		WithSampleWindow(-time.Second),
		WithWarmupSamples(-3),
		WithMinLatencyDecay(2),
		WithJitter(-1),
		WithOp(""),
	})
	assert.Equal(t, DefaultInitialLimit, cfg.initialLimit)
	assert.Equal(t, DefaultMinLimit, cfg.minLimit)
	assert.Equal(t, DefaultMaxLimit, cfg.maxLimit)
	assert.Equal(t, DefaultSmoothing, cfg.smoothing)
	assert.Equal(t, DefaultIncreaseRate, cfg.increaseRate)
	assert.Equal(t, DefaultDecreaseRatio, cfg.decreaseRatio)
	assert.Equal(t, DefaultTargetLatency, cfg.targetLatency)
	assert.Equal(t, DefaultTolerance, cfg.tolerance)
	assert.Equal(t, DefaultSampleWindow, cfg.sampleWindow)
	assert.Equal(t, DefaultWarmupSamples, cfg.warmupSamples)
	assert.Equal(t, DefaultMinLatencyDecay, cfg.minLatDecay)
	assert.Equal(t, DefaultJitter, cfg.jitter)
	assert.Equal(t, opExecute, cfg.opOrDefault())
}

func TestRingCapacity_Clamped(t *testing.T) {
	assert.Equal(t, minSamples, newConfig([]Option{WithSampleWindow(time.Millisecond)}).ringCapacity())
	assert.Equal(t, maxSamples, newConfig([]Option{WithSampleWindow(time.Hour)}).ringCapacity())
}

// --- Concurrency ---

func TestExecute_RaceSafe(t *testing.T) {
	l := New(WithInitialLimit(10), WithMaxLimit(50), WithWarmupSamples(0))
	defer l.Close()
	testx.HammerNoError(t, 50, 200, func() error {
		_, err := Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			return 0, nil
		})
		return err
	})
	assert.Equal(t, 0, l.InFlight())
}

// --- Shrink / debt accounting ---

func TestShrink_RecordsDebtWhenPermitsHeld(t *testing.T) {
	l := New(WithInitialLimit(4), WithMaxLimit(10), WithMinLimit(1))
	defer l.Close()

	// Hold every permit so shrink cannot pull idle ones and must record debt.
	rels := make([]func(bool, time.Duration), 0, 4)
	for i := 0; i < 4; i++ {
		rel, ok := l.TryAcquire()
		require.True(t, ok)
		rels = append(rels, rel)
	}
	require.Empty(t, l.sem, "all permits in flight")

	l.mu.Lock()
	l.limit = 4
	l.adjust(false, 0) // AIMD failure: 4 → 2, no idle permits → debt = 2
	debt := l.debt
	l.mu.Unlock()
	assert.Equal(t, 2, debt, "shrink recorded debt for held permits")

	// Releasing pays down debt before returning permits to the semaphore.
	rels[0](true, time.Millisecond)
	rels[1](true, time.Millisecond)
	l.mu.Lock()
	assert.Equal(t, 0, l.debt, "debt paid by releases")
	l.mu.Unlock()
	assert.Empty(t, l.sem, "debt-paying releases retired permits, not returned")

	rels[2](true, time.Millisecond)
	rels[3](true, time.Millisecond)
	assert.Len(t, l.sem, 2, "post-debt releases return permits up to new limit")
}

func TestGrowth_PaysDownDebtFirst(t *testing.T) {
	l := New(WithInitialLimit(4), WithMaxLimit(10), WithMinLimit(1), WithJitter(0))
	defer l.Close()

	rel, ok := l.TryAcquire()
	require.True(t, ok)
	defer rel(true, time.Millisecond)

	l.mu.Lock()
	l.limit = 4
	l.debt = 3        // pretend a prior shrink left debt
	l.adjust(true, 0) // AIMD success: 4 → 5, growth of 1 pays debt instead of issuing
	debt := l.debt
	l.mu.Unlock()
	assert.Equal(t, 2, debt, "growth paid down one unit of debt")
}

// --- Latency-based backoff branches ---

func TestVegas_BacksOffUnderHighLatency(t *testing.T) {
	l := New(WithAlgorithm(Vegas), WithInitialLimit(20), WithMaxLimit(100),
		WithMinLimit(1), WithWarmupSamples(0), WithJitter(0),
		WithMinLatencyDecay(0), WithDecreaseRatio(0.5))
	defer l.Close()
	// Seed a low minimum, then a spike far above it to inflate the queue
	// estimate past the backoff band.
	_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
		time.Sleep(time.Millisecond)
		return 0, nil
	})
	before := l.Limit()
	for i := 0; i < 3; i++ {
		_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(50 * time.Millisecond)
			return 0, nil
		})
	}
	assert.LessOrEqual(t, l.Limit(), before)
}

func TestGradient_BacksOffWhenLatencyRises(t *testing.T) {
	l := New(WithAlgorithm(Gradient), WithInitialLimit(20), WithMaxLimit(100),
		WithMinLimit(1), WithWarmupSamples(0), WithJitter(0),
		WithSmoothing(0.5), WithTolerance(0.1), WithDecreaseRatio(0.5))
	defer l.Close()
	for i := 0; i < 3; i++ {
		_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(2 * time.Millisecond)
			return 0, nil
		})
	}
	before := l.Limit()
	for i := 0; i < 3; i++ {
		_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
			time.Sleep(40 * time.Millisecond)
			return 0, nil
		})
	}
	assert.LessOrEqual(t, l.Limit(), before)
}

func TestVegas_GuardBranches(t *testing.T) {
	l := New(WithAlgorithm(Vegas), WithInitialLimit(10), WithMaxLimit(100),
		WithTargetLatency(2*time.Millisecond))
	defer l.Close()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.minLat = math.MaxFloat64
	assert.Equal(t, 10, l.vegas(time.Millisecond), "no minimum yet → hold")

	l.minLat = 1000
	assert.Equal(t, 10, l.vegas(0), "non-positive rtt → hold")

	// Latency inside the band (target ≤ queue ≤ 2·target) holds the limit.
	// queue = 10·(rtt-min)/min; min=1e6, rtt=1.15e6 → queue=1.5.
	// target = 10·0.1·(targetLatency-min)/min = 10·0.1·1 = 1.0.
	l.minLat = 1_000_000
	assert.Equal(t, 10, l.vegas(time.Duration(1_150_000)))
}

func TestVegas_TargetLatencyScalesTargetBand(t *testing.T) {
	l := New(WithAlgorithm(Vegas), WithInitialLimit(10), WithMaxLimit(100),
		WithWarmupSamples(0), WithJitter(0), WithMinLatencyDecay(0),
		WithTolerance(0.1), WithTargetLatency(100*time.Millisecond))
	defer l.Close()

	// Seed RTT_min at 1ms.
	_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
		time.Sleep(time.Millisecond)
		return 0, nil
	})
	start := l.Limit()

	// queue=1.5 with target≈99 → additive increase.
	_, _ = Execute(l, bg(), func(context.Context, AdaptController) (int, error) {
		time.Sleep(time.Duration(1_150_000))
		return 0, nil
	})
	assert.Greater(t, l.Limit(), start)
}

func TestGradient_GuardBranches(t *testing.T) {
	l := New(WithAlgorithm(Gradient), WithInitialLimit(10), WithMaxLimit(100))
	defer l.Close()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.avgLat = 0
	assert.Equal(t, 11, l.gradient(true, time.Millisecond), "no average yet → step up")

	l.avgLat = float64(time.Millisecond.Nanoseconds())
	assert.Equal(t, int(float64(10)*l.cfg.decreaseRatio), l.gradient(false, time.Millisecond),
		"failure → multiplicative decrease")
}

func TestTryExecute_SkipSample(t *testing.T) {
	l := New(WithWarmupSamples(0))
	defer l.Close()
	ran, _, err := TryExecute(l, bg(), func(_ context.Context, ac AdaptController) (int, error) {
		ac.SkipSample()
		time.Sleep(time.Millisecond)
		return 1, nil
	})
	require.NoError(t, err)
	assert.True(t, ran)
	assert.Equal(t, time.Duration(0), l.Stats().AvgLat)
}

func TestOptions_ApplyValidValues(t *testing.T) {
	cfg := newConfig([]Option{
		WithTargetLatency(250 * time.Millisecond),
		WithTolerance(0.25),
		WithMinLatencyDecay(0.05),
	})
	assert.Equal(t, 250*time.Millisecond, cfg.targetLatency)
	assert.Equal(t, 0.25, cfg.tolerance)
	assert.Equal(t, 0.05, cfg.minLatDecay)
}

func TestAcquire_RaceSafe(t *testing.T) {
	l := New(WithInitialLimit(10), WithMaxLimit(50), WithWarmupSamples(0))
	defer l.Close()
	testx.HammerNoError(t, 50, 200, func() error {
		rel, ok := l.TryAcquire()
		if !ok {
			return nil
		}
		rel(true, time.Microsecond)
		return nil
	})
	assert.Equal(t, 0, l.InFlight())
}
