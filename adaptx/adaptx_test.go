package adaptx

import (
	"context"
	"errors"
	"math"
	"sync"
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

// testClock is a mutex-protected fake clock for windowed algorithm tests.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Unix(1, 0)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func adaptOpts(clk *testClock, extra ...Option) []Option {
	opts := []Option{
		WithWarmupSamples(0),
		WithJitter(0),
		WithSampleWindow(time.Second),
		withClock(clk.Now),
	}
	return append(opts, extra...)
}

func saturate(l *Limiter) {
	l.inFlight.Store(int32(l.Limit()))
}

// shutdown zeros in-flight (including test-only saturations) so Close does not
// wait out DefaultCloseTimeout for work that was never admitted.
func shutdown(l *Limiter) {
	l.inFlight.Store(0)
	_ = l.Close()
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
	assert.Equal(t, DefaultUtilization, l.cfg.utilization)
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

func TestNewConfig_SkipsNilOption(t *testing.T) {
	cfg := newConfig([]Option{nil, WithInitialLimit(7), nil})
	assert.Equal(t, 7, cfg.initialLimit)
	assert.Equal(t, DefaultUtilization, cfg.utilization)
}

func TestNew_NilOptionIgnored(t *testing.T) {
	l := New(nil, WithInitialLimit(4))
	defer l.Close()
	assert.Equal(t, 4, l.Limit())
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
		return 0, nil
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), l.Stats().Success, "still counted as success")
	assert.Equal(t, time.Duration(0), l.Stats().AvgLat, "no latency recorded")
}

func TestExecute_SkipSamplePreservesFailureCount(t *testing.T) {
	l := New(WithWarmupSamples(0))
	defer l.Close()
	sentinel := errors.New("fail")
	_, err := Execute(l, bg(), func(_ context.Context, ac AdaptController) (int, error) {
		ac.SkipSample()
		return 0, sentinel
	})
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, int64(1), l.Stats().Failures)
	assert.Equal(t, int64(0), l.Stats().Success)
	assert.Equal(t, time.Duration(0), l.Stats().AvgLat)
}

// --- Context handling ---

func TestExecute_CancelledContext(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	defer l.Close()
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
	require.NoError(t, l.Close())
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
	require.ErrorIs(t, l.CloseWithTimeout(0), ErrDrainTimeout)

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

	done := make(chan error, 1)
	go func() {
		done <- l.CloseWithTimeout(time.Second)
	}()
	testx.Eventually(t, func() bool { return l.IsClosed() }, time.Second)
	rel(true, time.Millisecond)

	require.NoError(t, <-done)
	assert.Equal(t, 0, l.InFlight())
}

func TestCloseWithTimeout_SecondCallReturnsErrClosed(t *testing.T) {
	l := New()
	require.NoError(t, l.CloseWithTimeout(time.Second))
	require.ErrorIs(t, l.CloseWithTimeout(time.Second), ErrClosed)
}

// --- Algorithm adaptation ---

func TestAIMD_IncreasesOnSuccess(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(AIMD), WithInitialLimit(10), WithMaxLimit(100),
		WithIncreaseRate(1))...)
	defer shutdown(l)

	saturate(l)
	l.record(true, time.Millisecond)
	assert.Equal(t, 10, l.Limit(), "no adjust until the window closes")

	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	assert.Equal(t, 11, l.Limit(), "one additive step after a saturated success window")
	assert.Positive(t, l.Stats().Increases)
}

func TestAIMD_DecreasesOnFailure(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(AIMD), WithInitialLimit(20), WithMinLimit(1),
		WithDecreaseRatio(0.5))...)
	defer l.Close()

	for i := 0; i < 4; i++ {
		l.record(false, time.Millisecond)
	}
	assert.Equal(t, 20, l.Limit(), "failures in an open window do not cut yet")

	clk.Advance(time.Second)
	l.record(false, time.Millisecond)
	assert.Equal(t, 10, l.Limit(), "five failures in one window → one multiplicative decrease")
	assert.Equal(t, int64(1), l.Stats().Decreases)
}

func TestAIMD_NoIncreaseWhenUnderutilized(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(AIMD), WithInitialLimit(10), WithMaxLimit(100),
		WithIncreaseRate(1), WithUtilization(0.9))...)
	defer shutdown(l)

	l.record(true, time.Millisecond)
	for i := 0; i < 5; i++ {
		clk.Advance(time.Second)
		l.record(true, time.Millisecond)
	}
	assert.Equal(t, 10, l.Limit(), "serial successes at inFlight=0 never meet the utilization gate")
}

func TestAIMD_FractionalIncreaseAccumulates(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(AIMD), WithInitialLimit(10), WithMaxLimit(100),
		WithIncreaseRate(0.5))...)
	defer shutdown(l)

	saturate(l)
	l.record(true, time.Millisecond)
	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	assert.Equal(t, 10, l.Limit(), "0.5 credit is not truncated to a 0-step forever")

	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	assert.Equal(t, 11, l.Limit(), "two windows at 0.5 credit → +1")
}

func TestVegas_AdaptsFromLatency(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(Vegas), WithInitialLimit(10), WithMaxLimit(100),
		WithMinLimit(1), WithMinLatencyDecay(0))...)
	defer l.Close()

	start := l.Limit()
	l.record(true, time.Millisecond)
	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	assert.Greater(t, l.Limit(), start, "queue≈0 (rtt≈minRTT) grows the limit")
	assert.Equal(t, Vegas.String(), l.Stats().Algorithm)
}

func TestVegas_QueueUsesRttDenominator(t *testing.T) {
	l := New(WithAlgorithm(Vegas), WithInitialLimit(10), WithMaxLimit(100),
		WithTolerance(0.4), WithTargetLatency(time.Nanosecond), WithDecreaseRatio(0.5))
	defer l.Close()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.minLat = float64(time.Millisecond.Nanoseconds())
	// queue = limit·(1 − min/rtt) = 10·(1 − 1ms/2ms) = 5.
	// α = limit·tol = 4, β = 8 → hold. The old /minLat formula would give
	// queue=10 and multiplicative-decrease to 5.
	assert.Equal(t, 10, l.vegas(float64((2 * time.Millisecond).Nanoseconds())))
}

func TestGradient_GrowsThenBacksOff(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(Gradient), WithInitialLimit(10), WithMaxLimit(100),
		WithMinLimit(1), WithSmoothing(0.5), WithTolerance(0.1), WithDecreaseRatio(0.5))...)
	defer l.Close()

	l.record(true, time.Millisecond)
	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	grew := l.Limit()
	assert.Greater(t, grew, 10, "first window at avgLat=0 takes a unit step")

	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	assert.Greater(t, l.Limit(), grew, "rtt at the average still grows")

	beforeBackoff := l.Limit()
	clk.Advance(time.Second)
	l.record(true, 50*time.Millisecond)
	assert.Less(t, l.Limit(), beforeBackoff, "window mean far above EMA backs off")
	assert.Equal(t, Gradient.String(), l.Stats().Algorithm)
}

func TestGradient_EMAInitFirstSample(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(Gradient), WithInitialLimit(10), WithMaxLimit(100),
		WithSmoothing(0.2))...)
	defer l.Close()

	const rtt = 10 * time.Millisecond
	l.record(true, rtt)
	clk.Advance(time.Second)
	l.record(true, rtt)

	l.mu.Lock()
	defer l.mu.Unlock()
	assert.InDelta(t, float64(rtt.Nanoseconds()), l.avgLat, 1,
		"first window sets avgLat to the mean, not smoothing·mean + (1-smoothing)·0")
}

func TestAdjust_OncePerWindow(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(AIMD), WithInitialLimit(10), WithMaxLimit(100),
		WithIncreaseRate(1))...)
	defer shutdown(l)

	saturate(l)
	for i := 0; i < 10; i++ {
		l.record(true, time.Millisecond)
	}
	assert.Equal(t, 10, l.Limit(), "ten samples in one window do not produce ten steps")

	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	assert.Equal(t, 11, l.Limit(), "the window closes once → one +1")
	assert.Equal(t, int64(1), l.Stats().Increases)
}

func TestLimit_NeverBelowMinOrAboveMax(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(AIMD), WithInitialLimit(5), WithMinLimit(3), WithMaxLimit(8),
		WithIncreaseRate(100), WithDecreaseRatio(0.1))...)
	defer shutdown(l)

	saturate(l)
	l.record(true, time.Millisecond)
	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	assert.Equal(t, 8, l.Limit())

	l.record(false, time.Millisecond)
	clk.Advance(time.Second)
	l.record(false, time.Millisecond)
	assert.Equal(t, 3, l.Limit())
}

func TestLimit_GrowthIssuesPermits(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(AIMD), WithInitialLimit(2), WithMaxLimit(50),
		WithIncreaseRate(5))...)
	defer shutdown(l)

	saturate(l)
	l.record(true, time.Millisecond)
	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	assert.Greater(t, l.Limit(), 2)
	assert.GreaterOrEqual(t, len(l.sem), l.Limit()-l.InFlight()-1)
}

func TestOnLimitChange_Fires(t *testing.T) {
	clk := newTestClock()
	var called int
	l := New(adaptOpts(clk, WithAlgorithm(AIMD), WithInitialLimit(5), WithMaxLimit(50),
		WithIncreaseRate(1),
		WithOnLimitChange(func(_, _ int) { called++ }))...)
	defer shutdown(l)

	saturate(l)
	l.record(true, time.Millisecond)
	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	assert.Equal(t, 1, called, "hook runs synchronously on the adjusting goroutine")
}

func TestOnLimitChange_RecoversPanic(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(AIMD), WithInitialLimit(5), WithMaxLimit(50),
		WithIncreaseRate(1),
		WithOnLimitChange(func(_, _ int) { panic("hook boom") }))...)
	defer shutdown(l)

	saturate(l)
	l.record(true, time.Millisecond)
	clk.Advance(time.Second)
	require.NotPanics(t, func() { l.record(true, time.Millisecond) })
	assert.Greater(t, l.Limit(), 5)
}

// --- Warmup ---

func TestWarmup_DelaysAdaptation(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(AIMD), WithInitialLimit(10), WithMaxLimit(100),
		WithWarmupSamples(5), WithIncreaseRate(1))...)
	defer shutdown(l)

	saturate(l)
	start := l.Limit()
	for i := 0; i < 3; i++ {
		l.record(true, time.Millisecond)
	}
	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	assert.Equal(t, start, l.Limit(), "seen=4 < warmup=5, even after the window elapses")
}

// --- Stats ---

func TestStats_LatencyPercentiles(t *testing.T) {
	l := New(WithWarmupSamples(0), WithSampleWindow(time.Hour))
	defer l.Close()
	for i := 0; i < 20; i++ {
		rel, err := l.Acquire(bg())
		require.NoError(t, err)
		rel(true, time.Millisecond)
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
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(AIMD), WithInitialLimit(10), WithMaxLimit(100),
		WithIncreaseRate(5))...)
	defer shutdown(l)

	saturate(l)
	l.record(true, time.Millisecond)
	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	require.Positive(t, l.Stats().Success)
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
	assert.Equal(t, 0.0, l.increaseCredit)
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
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(Algorithm(99)), WithInitialLimit(10), WithMaxLimit(100),
		WithIncreaseRate(1))...)
	defer shutdown(l)

	start := l.Limit()
	saturate(l)
	l.record(true, time.Millisecond)
	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
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
		WithUtilization(0),
		WithUtilization(1.5),
		WithTargetLatency(-time.Second),
		WithTolerance(2),
		WithSampleWindow(-time.Second),
		WithWarmupSamples(-3),
		WithMinLatencyDecay(2),
		WithJitter(-1),
		WithOp(""),
		withClock(nil),
	})
	assert.Equal(t, DefaultInitialLimit, cfg.initialLimit)
	assert.Equal(t, DefaultMinLimit, cfg.minLimit)
	assert.Equal(t, DefaultMaxLimit, cfg.maxLimit)
	assert.Equal(t, DefaultSmoothing, cfg.smoothing)
	assert.Equal(t, DefaultIncreaseRate, cfg.increaseRate)
	assert.Equal(t, DefaultDecreaseRatio, cfg.decreaseRatio)
	assert.Equal(t, DefaultUtilization, cfg.utilization)
	assert.Equal(t, DefaultTargetLatency, cfg.targetLatency)
	assert.Equal(t, DefaultTolerance, cfg.tolerance)
	assert.Equal(t, DefaultSampleWindow, cfg.sampleWindow)
	assert.Equal(t, DefaultWarmupSamples, cfg.warmupSamples)
	assert.Equal(t, DefaultMinLatencyDecay, cfg.minLatDecay)
	assert.Equal(t, DefaultJitter, cfg.jitter)
	assert.Nil(t, cfg.clock)
	assert.Equal(t, opExecute, cfg.opOrDefault())
	assert.Equal(t, opTryExecute, cfg.opOrDefaultTry())
}

func TestOptions_OpOrDefaultTry(t *testing.T) {
	assert.Equal(t, opTryExecute, newConfig(nil).opOrDefaultTry())
	assert.Equal(t, "db.query", newConfig([]Option{WithOp("db.query")}).opOrDefaultTry())
	assert.Equal(t, opTryExecute, newConfig([]Option{WithOp("")}).opOrDefaultTry())
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

	rels := make([]func(bool, time.Duration), 0, 4)
	for i := 0; i < 4; i++ {
		rel, ok := l.TryAcquire()
		require.True(t, ok)
		rels = append(rels, rel)
	}
	require.Empty(t, l.sem, "all permits in flight")

	l.mu.Lock()
	l.limit = 4
	l.adjust(windowSnap{n: 1, fails: 1}) // AIMD failure: 4 → 2, no idle permits → debt = 2
	debt := l.debt
	l.mu.Unlock()
	assert.Equal(t, 2, debt, "shrink recorded debt for held permits")

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
	l.debt = 3
	l.adjust(windowSnap{n: 1, fails: 0, maxInFlight: 4}) // AIMD success: 4 → 5, growth of 1 pays debt
	debt := l.debt
	l.mu.Unlock()
	assert.Equal(t, 2, debt, "growth paid down one unit of debt")
}

// --- Latency-based backoff branches ---

func TestVegas_BacksOffUnderHighLatency(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(Vegas), WithInitialLimit(20), WithMaxLimit(100),
		WithMinLimit(1), WithMinLatencyDecay(0), WithDecreaseRatio(0.5))...)
	defer l.Close()

	l.record(true, time.Millisecond)
	clk.Advance(time.Second)
	l.record(true, time.Millisecond)
	before := l.Limit()

	clk.Advance(time.Second)
	l.record(true, 50*time.Millisecond)
	assert.Less(t, l.Limit(), before)
}

func TestGradient_BacksOffWhenLatencyRises(t *testing.T) {
	clk := newTestClock()
	l := New(adaptOpts(clk, WithAlgorithm(Gradient), WithInitialLimit(20), WithMaxLimit(100),
		WithSmoothing(0.5), WithTolerance(0.1), WithDecreaseRatio(0.5))...)
	defer l.Close()

	l.record(true, 2*time.Millisecond)
	clk.Advance(time.Second)
	l.record(true, 2*time.Millisecond)
	before := l.Limit()

	clk.Advance(time.Second)
	l.record(true, 40*time.Millisecond)
	assert.Less(t, l.Limit(), before)
}

func TestVegas_GuardBranches(t *testing.T) {
	l := New(WithAlgorithm(Vegas), WithInitialLimit(10), WithMaxLimit(100),
		WithTargetLatency(2*time.Millisecond), WithTolerance(0.1))
	defer l.Close()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.minLat = math.MaxFloat64
	assert.Equal(t, 10, l.vegas(float64(time.Millisecond.Nanoseconds())), "no minimum yet → hold")

	l.minLat = 1000
	assert.Equal(t, 10, l.vegas(0), "non-positive rtt → hold")

	// Hold band with /rtt math:
	// min=1e6, rtt=1.06383e6 → queue = 10·(1 − 1/1.06383) ≈ 0.6
	// α = 10·0.1·(1 − 1e6/2e6) = 0.5; β = 1.0 → hold.
	l.minLat = 1_000_000
	assert.Equal(t, 10, l.vegas(1_063_830))
}

func TestVegas_TargetLatencyScalesTargetBand(t *testing.T) {
	l := New(WithAlgorithm(Vegas), WithInitialLimit(10), WithMaxLimit(100),
		WithTolerance(0.1), WithMinLatencyDecay(0), WithDecreaseRatio(0.5))
	defer l.Close()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.minLat = 1_000_000
	rtt := 1_075_269.0 // queue ≈ 0.7

	l.cfg.targetLatency = 2 * time.Millisecond
	assert.Equal(t, 10, l.vegas(rtt), "scaled α=0.5, β=1.0 → hold at queue≈0.7")

	l.cfg.targetLatency = time.Nanosecond
	assert.Equal(t, 11, l.vegas(rtt), "target ≤ min → α=limit·tol=1.0 → queue 0.7 grows")
}

func TestGradient_GuardBranches(t *testing.T) {
	l := New(WithAlgorithm(Gradient), WithInitialLimit(10), WithMaxLimit(100))
	defer l.Close()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.avgLat = 0
	assert.Equal(t, 11, l.gradient(windowSnap{meanRTT: float64(time.Millisecond.Nanoseconds())}),
		"no average yet → step up")

	l.avgLat = float64(time.Millisecond.Nanoseconds())
	assert.Equal(t, int(float64(10)*l.cfg.decreaseRatio), l.gradient(windowSnap{fails: 1, meanRTT: 1e6}),
		"failure → multiplicative decrease")
}

func TestTryExecute_SkipSample(t *testing.T) {
	l := New(WithWarmupSamples(0))
	defer l.Close()
	ran, _, err := TryExecute(l, bg(), func(_ context.Context, ac AdaptController) (int, error) {
		ac.SkipSample()
		return 1, nil
	})
	require.NoError(t, err)
	assert.True(t, ran)
	assert.Equal(t, time.Duration(0), l.Stats().AvgLat)
}

func TestTryExecute_SkipSamplePreservesFailureCount(t *testing.T) {
	l := New(WithWarmupSamples(0))
	defer l.Close()
	sentinel := errors.New("fail")
	ran, _, err := TryExecute(l, bg(), func(_ context.Context, ac AdaptController) (int, error) {
		ac.SkipSample()
		return 0, sentinel
	})
	require.ErrorIs(t, err, sentinel)
	assert.True(t, ran)
	assert.Equal(t, int64(1), l.Stats().Failures)
	assert.Equal(t, int64(0), l.Stats().Success)
}

func TestTryExecute_RecoversPanic(t *testing.T) {
	l := New()
	defer l.Close()
	ran, _, err := TryExecute(l, bg(), func(context.Context, AdaptController) (int, error) {
		panic("kaboom")
	})
	assert.True(t, ran)
	testx.RequirePanicError(t, err, opTryExecute)
	assert.Equal(t, 0, l.InFlight())
}

func TestTryExecute_RecoversPanic_WithCustomOp(t *testing.T) {
	l := New(WithOp("api.search"))
	defer l.Close()
	ran, _, err := TryExecute(l, bg(), func(context.Context, AdaptController) (int, error) {
		panic("kaboom")
	})
	assert.True(t, ran)
	testx.RequirePanicError(t, err, "api.search")
}

func TestTryExecute_CancelledContext(t *testing.T) {
	l := New()
	defer l.Close()
	ran, _, err := TryExecute[int](l, testx.CancelledCtx(), noop(0, nil))
	assert.False(t, ran)
	require.ErrorIs(t, err, ErrCancelled)
}

func TestTryExecute_DeadlineExceeded(t *testing.T) {
	l := New()
	defer l.Close()
	ran, _, err := TryExecute[int](l, testx.ExpiredCtx(), noop(0, nil))
	assert.False(t, ran)
	require.ErrorIs(t, err, ErrTimeout)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestOptions_ApplyValidValues(t *testing.T) {
	cfg := newConfig([]Option{
		WithTargetLatency(250 * time.Millisecond),
		WithTolerance(0.25),
		WithMinLatencyDecay(0.05),
		WithUtilization(0.8),
	})
	assert.Equal(t, 250*time.Millisecond, cfg.targetLatency)
	assert.Equal(t, 0.25, cfg.tolerance)
	assert.Equal(t, 0.05, cfg.minLatDecay)
	assert.Equal(t, 0.8, cfg.utilization)
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

// --- Allow ---

func TestAllow_ReportsAvailability(t *testing.T) {
	l := New(WithInitialLimit(2), WithMaxLimit(2))
	defer l.Close()
	assert.True(t, l.Allow())

	rel, ok := l.TryAcquire()
	require.True(t, ok)
	assert.True(t, l.Allow())
	rel(true, time.Microsecond)
	assert.True(t, l.Allow())

	rel2, ok := l.TryAcquire()
	require.True(t, ok)
	rel3, ok := l.TryAcquire()
	require.True(t, ok)
	assert.False(t, l.Allow())
	rel2(true, time.Microsecond)
	rel3(true, time.Microsecond)
}

func TestAllow_ReturnsFalseWhenClosed(t *testing.T) {
	l := New()
	require.NoError(t, l.Close())
	assert.False(t, l.Allow())
}

// --- Close edge cases ---

func TestCloseWithTimeout_ZeroSkipsDrainWait(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	rel, err := l.Acquire(bg())
	require.NoError(t, err)

	start := time.Now()
	require.ErrorIs(t, l.CloseWithTimeout(0), ErrDrainTimeout)
	assert.Less(t, time.Since(start), 50*time.Millisecond)
	assert.True(t, l.IsClosed())

	rel(true, time.Millisecond)
}

func TestAcquire_ReturnsErrClosedWhenClosedAfterPermitTaken(t *testing.T) {
	l := New(WithInitialLimit(2), WithMaxLimit(2))
	rel, ok := l.TryAcquire()
	require.True(t, ok)

	require.ErrorIs(t, l.CloseWithTimeout(0), ErrDrainTimeout)

	_, err := l.Acquire(bg())
	require.ErrorIs(t, err, ErrClosed)

	rel(true, time.Millisecond)
}

func TestTryAcquire_ReturnsFalseWhenClosedAfterPermitTaken(t *testing.T) {
	l := New(WithInitialLimit(2), WithMaxLimit(2))
	rel, ok := l.TryAcquire()
	require.True(t, ok)

	require.ErrorIs(t, l.CloseWithTimeout(0), ErrDrainTimeout)

	_, ok = l.TryAcquire()
	assert.False(t, ok)

	rel(true, time.Millisecond)
}

// --- Internal edge paths ---

func TestReturnPermit_NoOpWhenSemaphoreFull(t *testing.T) {
	l := New(WithInitialLimit(3), WithMaxLimit(3))
	defer l.Close()
	assert.Len(t, l.sem, 3)

	l.mu.Lock()
	l.returnPermit() // sem already full → default branch
	l.mu.Unlock()
	assert.Len(t, l.sem, 3)
}

func TestResetStats_RaisesLimitWhenInFlightExceedsInitial(t *testing.T) {
	l := New(WithInitialLimit(2), WithMaxLimit(10))
	defer shutdown(l)
	l.inFlight.Add(3)

	l.ResetStats()
	assert.Equal(t, 3, l.Limit(), "limit raised to in-flight count")
	assert.Equal(t, int64(0), l.Stats().Total)
}

func TestGradient_FastStepWhenWellBelowAverage(t *testing.T) {
	l := New(WithAlgorithm(Gradient), WithInitialLimit(10), WithMaxLimit(100),
		WithTolerance(0.1))
	defer l.Close()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.avgLat = float64((10 * time.Millisecond).Nanoseconds())
	got := l.gradient(windowSnap{meanRTT: float64(time.Millisecond.Nanoseconds())})
	assert.Equal(t, 12, got)
}

func TestGradient_ProportionalBackoffWhenAboveAverage(t *testing.T) {
	l := New(WithAlgorithm(Gradient), WithInitialLimit(20), WithMaxLimit(100),
		WithTolerance(0.1), WithDecreaseRatio(0.5))
	defer l.Close()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.avgLat = float64((10 * time.Millisecond).Nanoseconds())
	got := l.gradient(windowSnap{meanRTT: float64((25 * time.Millisecond).Nanoseconds())})
	assert.Less(t, got, 20)
}

func TestAcquire_AbortsWhenClosedAfterTakingPermit(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	require.NoError(t, l.CloseWithTimeout(0))
	select {
	case l.sem <- struct{}{}:
	default:
		t.Fatal("expected room to seed permit")
	}

	_, err := l.Acquire(bg())
	require.ErrorIs(t, err, ErrClosed)
}

func TestTryAcquire_AbortsWhenClosedAfterTakingPermit(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	require.NoError(t, l.CloseWithTimeout(0))
	select {
	case l.sem <- struct{}{}:
	default:
		t.Fatal("expected room to seed permit")
	}

	_, ok := l.TryAcquire()
	assert.False(t, ok)
}

func TestReconcilePermitsLocked_ClampsNegativeWant(t *testing.T) {
	l := New(WithInitialLimit(2), WithMaxLimit(5))
	defer shutdown(l)
	l.inFlight.Store(5)
	l.mu.Lock()
	l.limit = 2
	l.reconcilePermitsLocked()
	assert.Empty(t, l.sem)
	l.mu.Unlock()
}
