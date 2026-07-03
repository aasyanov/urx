package warmupx

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrategy_String(t *testing.T) {
	tests := []struct {
		name string
		s    Strategy
		want string
	}{
		{"linear", Linear, labelLinear},
		{"exponential", Exponential, labelExponential},
		{"logarithmic", Logarithmic, labelLogarithmic},
		{"step", Step, labelStep},
		{"unknown", Strategy(99), labelUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.s.String())
		})
	}
}

func TestNew_Defaults(t *testing.T) {
	w := New()
	assert.Equal(t, DefaultStrategy, w.cfg.strategy)
	assert.InDelta(t, DefaultMinCapacity, w.cfg.minCap, 1e-9)
	assert.InDelta(t, DefaultMaxCapacity, w.cfg.maxCap, 1e-9)
	assert.Equal(t, DefaultDuration, w.cfg.duration)
	assert.Equal(t, DefaultStepCount, w.cfg.stepCount)
	assert.InDelta(t, DefaultExpFactor, w.cfg.expFactor, 1e-9)
	assert.InDelta(t, DefaultMinCapacity, w.Capacity(), 1e-9)
	assert.False(t, w.IsWarming())
	assert.False(t, w.IsComplete())
}

func TestWithMinCapacity(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want float64
	}{
		{"default", nil, DefaultMinCapacity},
		{"custom", WithMinCapacity(0.3), 0.3},
		{"zero", WithMinCapacity(0), 0},
		{"one", WithMinCapacity(1), 1},
		{"negative ignored", WithMinCapacity(-1), DefaultMinCapacity},
		{"above one ignored", WithMinCapacity(1.5), DefaultMinCapacity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			cfg := newConfig(opts)
			assert.InDelta(t, tt.want, cfg.minCap, 1e-9)
		})
	}
}

func TestWithMaxCapacity(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want float64
	}{
		{"default", nil, DefaultMaxCapacity},
		{"custom", WithMaxCapacity(0.8), 0.8},
		{"zero ignored", WithMaxCapacity(0), DefaultMaxCapacity},
		{"negative ignored", WithMaxCapacity(-0.2), DefaultMaxCapacity},
		{"above one ignored", WithMaxCapacity(2), DefaultMaxCapacity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			cfg := newConfig(opts)
			assert.InDelta(t, tt.want, cfg.maxCap, 1e-9)
		})
	}
}

func TestNewConfig_MaxClampedToMin(t *testing.T) {
	cfg := newConfig([]Option{WithMinCapacity(0.6), WithMaxCapacity(0.4)})
	assert.InDelta(t, 0.6, cfg.minCap, 1e-9)
	assert.InDelta(t, 0.6, cfg.maxCap, 1e-9)
}

func TestWithDuration(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want time.Duration
	}{
		{"default", nil, DefaultDuration},
		{"custom", WithDuration(5 * time.Second), 5 * time.Second},
		{"zero ignored", WithDuration(0), DefaultDuration},
		{"negative ignored", WithDuration(-time.Second), DefaultDuration},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			cfg := newConfig(opts)
			assert.Equal(t, tt.want, cfg.duration)
		})
	}
}

func TestWithInterval_Clamping(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want time.Duration
	}{
		{"derived from duration", []Option{WithDuration(30 * time.Second)}, 300 * time.Millisecond},
		{"clamped to min", []Option{WithDuration(100 * time.Millisecond)}, minInterval},
		{"clamped to max", []Option{WithDuration(10 * time.Minute)}, maxInterval},
		{"explicit override", []Option{WithInterval(50 * time.Millisecond)}, 50 * time.Millisecond},
		{"explicit clamped to min", []Option{WithInterval(time.Millisecond)}, minInterval},
		{"explicit clamped to max", []Option{WithInterval(5 * time.Second)}, maxInterval},
		{"zero interval ignored", []Option{WithDuration(30 * time.Second), WithInterval(0)}, 300 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newConfig(tt.opts)
			assert.Equal(t, tt.want, cfg.interval)
		})
	}
}

func TestWithStepCount(t *testing.T) {
	assert.Equal(t, DefaultStepCount, newConfig(nil).stepCount)
	assert.Equal(t, 5, newConfig([]Option{WithStepCount(5)}).stepCount)
	assert.Equal(t, DefaultStepCount, newConfig([]Option{WithStepCount(0)}).stepCount)
	assert.Equal(t, DefaultStepCount, newConfig([]Option{WithStepCount(-3)}).stepCount)
}

func TestWithExpFactor(t *testing.T) {
	assert.InDelta(t, DefaultExpFactor, newConfig(nil).expFactor, 1e-9)
	assert.InDelta(t, 6.0, newConfig([]Option{WithExpFactor(6)}).expFactor, 1e-9)
	assert.InDelta(t, DefaultExpFactor, newConfig([]Option{WithExpFactor(0)}).expFactor, 1e-9)
	assert.InDelta(t, DefaultExpFactor, newConfig([]Option{WithExpFactor(-1)}).expFactor, 1e-9)
}

func TestWithOp(t *testing.T) {
	assert.Equal(t, "api.serve", newConfig([]Option{WithOp("api.serve")}).opOrDefault())
	assert.Equal(t, opExecute, newConfig([]Option{WithOp("")}).opOrDefault())
	assert.Equal(t, opExecute, newConfig(nil).opOrDefault())
}

func TestWithOp_TryDefault(t *testing.T) {
	assert.Equal(t, opTryExecute, newConfig(nil).opOrDefaultTry())
	assert.Equal(t, "api.serve", newConfig([]Option{WithOp("api.serve")}).opOrDefaultTry())
	assert.Equal(t, opTryExecute, newConfig([]Option{WithOp("")}).opOrDefaultTry())
}

func TestWithStrategy(t *testing.T) {
	w := New(WithStrategy(Step))
	assert.Equal(t, Step, w.Strategy())
}

func TestWarmer_StartCompletes(t *testing.T) {
	w := New(WithDuration(80*time.Millisecond), WithInterval(10*time.Millisecond))
	w.Start()
	defer w.Stop()

	assert.True(t, w.IsWarming())
	testx.Eventually(t, w.IsComplete, 2*time.Second)
	assert.InDelta(t, DefaultMaxCapacity, w.Capacity(), 1e-9)
	assert.InDelta(t, 1.0, w.Progress(), 1e-9)
	assert.False(t, w.IsWarming())
}

func TestWarmer_StartAt_ClampsCapacity(t *testing.T) {
	w := New(WithMinCapacity(0.2), WithMaxCapacity(0.8))

	w.StartAt(0.05)
	assert.InDelta(t, 0.2, w.Capacity(), 1e-9)
	w.Stop()

	w.StartAt(0.95)
	assert.InDelta(t, 0.8, w.Capacity(), 1e-9)
	w.Stop()

	w.StartAt(0.5)
	assert.InDelta(t, 0.5, w.Capacity(), 1e-9)
	w.Stop()
}

func TestWarmer_StopRetainsCapacity(t *testing.T) {
	w := New(WithDuration(time.Second), WithInterval(10*time.Millisecond), WithMinCapacity(0.1))
	w.Start()
	testx.Eventually(t, func() bool { return w.Capacity() > 0.1 }, 2*time.Second)
	w.Stop()

	cap := w.Capacity()
	assert.False(t, w.IsWarming())
	testx.Never(t, func() bool { return w.Capacity() != cap }, 100*time.Millisecond)
}

func TestWarmer_StopRetainsProgress(t *testing.T) {
	w := New(WithDuration(time.Second), WithInterval(10*time.Millisecond), WithMinCapacity(0.1))
	w.Start()
	testx.Eventually(t, func() bool { return w.Progress() > 0.05 }, 2*time.Second)
	w.Stop()

	frozen := w.Progress()
	assert.False(t, w.IsWarming())
	assert.Greater(t, frozen, 0.0)
	testx.Never(t, func() bool { return w.Progress() != frozen }, 100*time.Millisecond)
}

func TestWarmer_StopRetainsProgressInStatsAndRejections(t *testing.T) {
	w := New(WithDuration(time.Second), WithInterval(10*time.Millisecond), WithMinCapacity(0.1))
	w.Start()
	testx.Eventually(t, func() bool { return w.Progress() > 0.05 }, 2*time.Second)
	w.Stop()

	progress := w.Progress()
	s := w.Stats()
	assert.InDelta(t, progress, s.Progress, 1e-9)
	assert.InDelta(t, w.Capacity(), s.Capacity, 1e-9)

	err := w.AllowOrError()
	if err != nil {
		require.ErrorIs(t, err, ErrRejected)
		assert.Contains(t, err.Error(), "progress=")
	}
}

func TestWarmer_StopIdempotent(t *testing.T) {
	w := New(WithDuration(time.Second))
	w.Start()
	assert.NotPanics(t, func() {
		w.Stop()
		w.Stop()
		w.Stop()
	})
}

func TestWarmer_StopIdempotent_PreservesFrozenProgress(t *testing.T) {
	w := New(WithDuration(time.Second), WithInterval(10*time.Millisecond), WithMinCapacity(0.1))
	w.Start()
	testx.Eventually(t, func() bool { return w.Progress() > 0.05 }, 2*time.Second)
	w.Stop()
	frozen := w.Progress()
	w.Stop()
	assert.InDelta(t, frozen, w.Progress(), 1e-9)
}

func TestWarmer_StopWithoutStart(t *testing.T) {
	w := New()
	assert.NotPanics(t, w.Stop)
	assert.False(t, w.IsComplete())
	assert.InDelta(t, 0.0, w.Progress(), 1e-9)
}

func TestWarmer_RestartBumpsGeneration(t *testing.T) {
	w := New(WithDuration(time.Second), WithInterval(10*time.Millisecond))
	w.Start()
	w.Start()
	w.Start()
	defer w.Stop()
	assert.True(t, w.IsWarming())
}

func TestWarmer_Reset(t *testing.T) {
	w := New(WithDuration(time.Second), WithInterval(10*time.Millisecond), WithMinCapacity(0.1))
	w.Start()
	testx.Eventually(t, func() bool { return w.Capacity() > 0.2 }, 2*time.Second)
	w.Reset()
	defer w.Stop()
	assert.InDelta(t, 0.1, w.Capacity(), 0.05)
	assert.True(t, w.IsWarming())
}

func TestWarmer_OnCompleteCallback(t *testing.T) {
	var fired atomic.Bool
	w := New(
		WithDuration(60*time.Millisecond),
		WithInterval(10*time.Millisecond),
		WithOnComplete(func() { fired.Store(true) }),
	)
	w.Start()
	defer w.Stop()
	testx.Eventually(t, fired.Load, 2*time.Second)
}

func TestWarmer_CompletionFiresBothCallbacks(t *testing.T) {
	var changed, completed atomic.Bool
	var lastNewCap atomic.Uint64
	w := New(
		WithDuration(40*time.Millisecond),
		WithInterval(10*time.Millisecond),
		WithMinCapacity(0.1),
		WithMaxCapacity(1),
		WithOnCapacityChange(func(_, newCap float64) {
			changed.Store(true)
			lastNewCap.Store(math.Float64bits(newCap))
		}),
		WithOnComplete(func() { completed.Store(true) }),
	)
	w.Start()
	defer w.Stop()

	testx.Eventually(t, func() bool { return completed.Load() && changed.Load() }, 2*time.Second)
	assert.InDelta(t, 1.0, math.Float64frombits(lastNewCap.Load()), 1e-9,
		"final capacity-change delivers maxCap")
}

func TestWarmer_OnCapacityChangeCallback(t *testing.T) {
	var count atomic.Int64
	w := New(
		WithDuration(200*time.Millisecond),
		WithInterval(10*time.Millisecond),
		WithMinCapacity(0),
		WithMaxCapacity(1),
		WithStrategy(Linear),
		WithOnCapacityChange(func(_, _ float64) { count.Add(1) }),
	)
	w.Start()
	defer w.Stop()
	testx.Eventually(t, func() bool { return count.Load() > 0 }, 2*time.Second)
}

func TestWarmer_OnCapacityChange_SkipsBelowEpsilon(t *testing.T) {
	var count atomic.Int64
	w := New(
		WithDuration(100*time.Second),
		WithInterval(10*time.Millisecond),
		WithMinCapacity(0.5),
		WithMaxCapacity(0.505),
		WithOnCapacityChange(func(_, _ float64) { count.Add(1) }),
	)
	w.Start()
	time.Sleep(80 * time.Millisecond)
	w.Stop()
	assert.Equal(t, int64(0), count.Load(), "sub-epsilon capacity deltas must not fire the callback")
}

func TestWarmer_OnCapacityChange_SkipsWhenUnchangedOnComplete(t *testing.T) {
	var count atomic.Int64
	w := New(
		WithDuration(40*time.Millisecond),
		WithInterval(10*time.Millisecond),
		WithMinCapacity(1),
		WithMaxCapacity(1),
		WithOnCapacityChange(func(_, _ float64) { count.Add(1) }),
		WithOnComplete(func() {}),
	)
	w.Start()
	defer w.Stop()
	testx.Eventually(t, w.IsComplete, 2*time.Second)
	assert.Equal(t, int64(0), count.Load(), "completion must not fire onCapacityChange when capacity is unchanged")
}

func TestWarmer_Progress(t *testing.T) {
	w := New()
	assert.InDelta(t, 0.0, w.Progress(), 1e-9)

	w.Start()
	defer w.Stop()
	p := w.Progress()
	assert.GreaterOrEqual(t, p, 0.0)
	assert.LessOrEqual(t, p, 1.0)
}

func TestWarmer_Allow_FullCapacity(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	for range 100 {
		assert.True(t, w.Allow())
	}
	assert.Equal(t, int64(100), w.Stats().Allowed)
}

func TestWarmer_Allow_ZeroCapacity(t *testing.T) {
	w := New(WithMinCapacity(0), WithMaxCapacity(1))
	for range 100 {
		assert.False(t, w.Allow())
	}
	assert.Equal(t, int64(100), w.Stats().Rejected)
}

func TestWarmer_Allow_ProbabilisticDistribution(t *testing.T) {
	w := New(WithMinCapacity(0.5), WithMaxCapacity(0.5))
	const n = 20000
	allowed := 0
	for range n {
		if w.Allow() {
			allowed++
		}
	}
	ratio := float64(allowed) / float64(n)
	assert.InDelta(t, 0.5, ratio, 0.05)
}

func TestWarmer_AllowOrError(t *testing.T) {
	full := New(WithMinCapacity(1), WithMaxCapacity(1))
	require.NoError(t, full.AllowOrError())

	empty := New(WithMinCapacity(0), WithMaxCapacity(1))
	err := empty.AllowOrError()
	require.ErrorIs(t, err, ErrRejected)
	assert.Contains(t, err.Error(), "capacity=")
	assert.Contains(t, err.Error(), "progress=")
}

func TestWarmer_MaxRequests(t *testing.T) {
	tests := []struct {
		name     string
		capacity float64
		base     int
		want     int
	}{
		{"full", 1.0, 100, 100},
		{"half rounds up", 0.5, 100, 50},
		{"tiny rounds up to one", 0.01, 100, 1},
		{"zero base", 1.0, 0, 0},
		{"negative base", 1.0, -5, 0},
		{"zero capacity", 0, 100, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := New(WithMinCapacity(tt.capacity), WithMaxCapacity(1))
			assert.Equal(t, tt.want, w.MaxRequests(tt.base))
		})
	}
}

func TestWarmer_WaitForCompletion_NeverStarted(t *testing.T) {
	w := New()
	require.NoError(t, w.WaitForCompletion(context.Background()))
}

func TestWarmer_WaitForCompletion(t *testing.T) {
	w := New(WithDuration(60*time.Millisecond), WithInterval(10*time.Millisecond))
	w.Start()
	defer w.Stop()
	require.NoError(t, w.WaitForCompletion(context.Background()))
	assert.True(t, w.IsComplete())
}

func TestWarmer_WaitForCompletion_AlreadyComplete(t *testing.T) {
	w := New(WithDuration(40*time.Millisecond), WithInterval(10*time.Millisecond))
	w.Start()
	defer w.Stop()
	testx.Eventually(t, w.IsComplete, 2*time.Second)
	require.NoError(t, w.WaitForCompletion(context.Background()))
}

func TestWarmer_WaitForCompletion_ContextCancelled(t *testing.T) {
	w := New(WithDuration(time.Hour))
	w.Start()
	defer w.Stop()
	err := w.WaitForCompletion(testx.CancelledCtx())
	require.ErrorIs(t, err, context.Canceled)
}

func TestWarmer_WaitForCompletion_StoppedMidRamp(t *testing.T) {
	w := New(WithDuration(time.Hour), WithInterval(10*time.Millisecond))
	w.Start()
	testx.Eventually(t, w.IsWarming, 2*time.Second)
	w.Stop()

	err := w.WaitForCompletion(testx.CancelledCtx())
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, w.IsComplete())
}

func TestWarmer_WaitForCompletion_Timeout(t *testing.T) {
	w := New(WithDuration(time.Hour))
	w.Start()
	defer w.Stop()
	ctx, cancel := testx.TimedCtx(50 * time.Millisecond)
	defer cancel()
	err := w.WaitForCompletion(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExecute_NilFunc(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	_, err := Execute[int](w, context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestExecute_AdmitsAtFullCapacity(t *testing.T) {
	w := New(WithStrategy(Step), WithMinCapacity(1), WithMaxCapacity(1))
	got, err := Execute(w, context.Background(), func(_ context.Context, wc WarmupController) (string, error) {
		assert.InDelta(t, 1.0, wc.Capacity(), 1e-9)
		assert.InDelta(t, 0.0, wc.Progress(), 1e-9)
		assert.Equal(t, Step, wc.Strategy())
		return "ok", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", got)
	assert.Equal(t, int64(1), w.Stats().Allowed)
}

func TestExecute_ControllerProgressReflectsRamp(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1), WithDuration(time.Hour))
	w.Start()
	defer w.Stop()

	var seen float64
	_, err := Execute(w, context.Background(), func(_ context.Context, wc WarmupController) (int, error) {
		seen = wc.Progress()
		return 0, nil
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, seen, 0.0)
	assert.LessOrEqual(t, seen, 1.0)
}

func TestExecute_RejectsAtZeroCapacity(t *testing.T) {
	w := New(WithMinCapacity(0), WithMaxCapacity(1))
	var called atomic.Bool
	_, err := Execute(w, context.Background(), func(_ context.Context, _ WarmupController) (int, error) {
		called.Store(true)
		return 1, nil
	})
	require.ErrorIs(t, err, ErrRejected)
	assert.False(t, called.Load(), "callback must not run when rejected")
	assert.Equal(t, int64(1), w.Stats().Rejected)
}

func TestExecute_PropagatesError(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	sentinel := errors.New("boom")
	_, err := Execute(w, context.Background(), func(_ context.Context, _ WarmupController) (int, error) {
		return 0, sentinel
	})
	require.ErrorIs(t, err, sentinel)
	s := w.Stats()
	assert.Equal(t, int64(0), s.Allowed)
	assert.Equal(t, int64(0), s.Rejected)
}

func TestExecute_ControllerReject(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	got, err := Execute(w, context.Background(), func(_ context.Context, wc WarmupController) (string, error) {
		wc.Reject()
		return "discarded", nil
	})
	require.ErrorIs(t, err, ErrRejected)
	assert.Empty(t, got)
	s := w.Stats()
	assert.Equal(t, int64(0), s.Allowed)
	assert.Equal(t, int64(1), s.Rejected)
}

func TestExecute_RecoversPanic(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	_, err := Execute(w, context.Background(), func(_ context.Context, _ WarmupController) (int, error) {
		panic("handler crashed")
	})
	pe := testx.RequirePanicError(t, err, opExecute)
	assert.Equal(t, "handler crashed", pe.Value)
	s := w.Stats()
	assert.Equal(t, int64(0), s.Allowed)
	assert.Equal(t, int64(0), s.Rejected)
}

func TestExecute_RecoversPanic_WithCustomOp(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1), WithOp("batch.process"))
	_, err := Execute(w, context.Background(), func(context.Context, WarmupController) (int, error) {
		panic("boom")
	})
	testx.RequirePanicError(t, err, "batch.process")
}

func TestExecute_ContextVisibleToCallback(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "v")
	_, err := Execute(w, ctx, func(c context.Context, _ WarmupController) (int, error) {
		assert.Equal(t, "v", c.Value(ctxKey{}))
		return 0, nil
	})
	require.NoError(t, err)
}

func TestExecute_ReturnsErrCancelledOnCancelledContext(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	called := false
	_, err := Execute(w, testx.CancelledCtx(), func(context.Context, WarmupController) (int, error) {
		called = true
		return 1, nil
	})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, called, "fn must not run for a cancelled context")
	s := w.Stats()
	assert.Equal(t, int64(0), s.Allowed)
	assert.Equal(t, int64(0), s.Rejected, "cancelled request must not attempt admission")
}

func TestExecute_ReturnsErrCancelledOnExpiredDeadline(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	_, err := Execute(w, testx.ExpiredCtx(), func(context.Context, WarmupController) (int, error) {
		return 1, nil
	})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// --- TryExecute ---

func TestTryExecute_RunsWhenAdmitted(t *testing.T) {
	w := New(WithStrategy(Step), WithMinCapacity(1), WithMaxCapacity(1))
	ok, got, err := TryExecute(w, context.Background(), func(_ context.Context, wc WarmupController) (string, error) {
		assert.InDelta(t, 1.0, wc.Capacity(), 1e-9)
		assert.InDelta(t, 0.0, wc.Progress(), 1e-9)
		assert.Equal(t, Step, wc.Strategy())
		return "ok", nil
	})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "ok", got)
	assert.Equal(t, int64(1), w.Stats().Allowed)
}

func TestTryExecute_SkipsWhenRejected(t *testing.T) {
	w := New(WithMinCapacity(0), WithMaxCapacity(1))
	var called atomic.Bool
	ok, _, err := TryExecute(w, context.Background(), func(_ context.Context, _ WarmupController) (int, error) {
		called.Store(true)
		return 1, nil
	})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.False(t, errors.Is(err, ErrRejected), "probabilistic rejection must not surface ErrRejected")
	assert.False(t, called.Load(), "callback must not run when rejected")
	assert.Equal(t, int64(1), w.Stats().Rejected)
}

func TestTryExecute_NilFunc(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	ok, _, err := TryExecute[int](w, context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
	assert.False(t, ok)
}

func TestTryExecute_ReturnsErrCancelledOnCancelledContext(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	called := false
	ok, _, err := TryExecute(w, testx.CancelledCtx(), func(context.Context, WarmupController) (int, error) {
		called = true
		return 1, nil
	})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, ok)
	assert.False(t, called, "fn must not run for a cancelled context")
	s := w.Stats()
	assert.Equal(t, int64(0), s.Allowed)
	assert.Equal(t, int64(0), s.Rejected, "cancelled request must not attempt admission")
}

func TestTryExecute_ReturnsErrCancelledOnExpiredDeadline(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	ok, _, err := TryExecute(w, testx.ExpiredCtx(), func(context.Context, WarmupController) (int, error) {
		return 1, nil
	})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, ok)
}

func TestTryExecute_PropagatesError(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	sentinel := errors.New("boom")
	ok, _, err := TryExecute(w, context.Background(), func(_ context.Context, _ WarmupController) (int, error) {
		return 0, sentinel
	})
	require.True(t, ok)
	require.ErrorIs(t, err, sentinel)
	s := w.Stats()
	assert.Equal(t, int64(0), s.Allowed)
	assert.Equal(t, int64(0), s.Rejected)
}

func TestTryExecute_ControllerReject(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	ok, got, err := TryExecute(w, context.Background(), func(_ context.Context, wc WarmupController) (string, error) {
		wc.Reject()
		return "discarded", nil
	})
	require.True(t, ok, "fn ran before late reject")
	require.ErrorIs(t, err, ErrRejected)
	assert.Empty(t, got)
	s := w.Stats()
	assert.Equal(t, int64(0), s.Allowed)
	assert.Equal(t, int64(1), s.Rejected)
}

func TestTryExecute_RecoversPanic(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	ok, _, err := TryExecute(w, context.Background(), func(_ context.Context, _ WarmupController) (int, error) {
		panic("handler crashed")
	})
	require.True(t, ok, "fn ran before panicking")
	pe := testx.RequirePanicError(t, err, opTryExecute)
	assert.Equal(t, "handler crashed", pe.Value)
	s := w.Stats()
	assert.Equal(t, int64(0), s.Allowed)
	assert.Equal(t, int64(0), s.Rejected)
}

func TestTryExecute_RecoversPanic_WithCustomOp(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1), WithOp("batch.process"))
	ok, _, err := TryExecute(w, context.Background(), func(context.Context, WarmupController) (int, error) {
		panic("boom")
	})
	require.True(t, ok)
	testx.RequirePanicError(t, err, "batch.process")
}

func TestTryExecute_ContextVisibleToCallback(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "v")
	ok, _, err := TryExecute(w, ctx, func(c context.Context, _ WarmupController) (int, error) {
		assert.Equal(t, "v", c.Value(ctxKey{}))
		return 0, nil
	})
	require.True(t, ok)
	require.NoError(t, err)
}

func TestWarmer_Stats(t *testing.T) {
	w := New(
		WithStrategy(Exponential),
		WithMinCapacity(0.2),
		WithMaxCapacity(0.9),
		WithDuration(time.Second),
	)
	s := w.Stats()
	assert.Equal(t, labelExponential, s.Strategy)
	assert.InDelta(t, 0.2, s.MinCapacity, 1e-9)
	assert.InDelta(t, 0.9, s.MaxCapacity, 1e-9)
	assert.Equal(t, time.Second, s.Duration)
	assert.Equal(t, time.Duration(0), s.Elapsed)
}

func TestWarmer_Stats_ElapsedClamped(t *testing.T) {
	w := New(WithDuration(40*time.Millisecond), WithInterval(10*time.Millisecond))
	w.Start()
	defer w.Stop()
	testx.Eventually(t, w.IsComplete, 2*time.Second)
	s := w.Stats()
	assert.LessOrEqual(t, s.Elapsed, s.Duration)
	assert.GreaterOrEqual(t, s.Remaining, time.Duration(0))
	assert.InDelta(t, 1.0, s.Progress, 1e-9)
}

func TestWarmer_ResetStats(t *testing.T) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	w.Allow()
	w.Allow()
	require.Equal(t, int64(2), w.Stats().Allowed)
	w.ResetStats()
	assert.Equal(t, int64(0), w.Stats().Allowed)
	assert.Equal(t, int64(0), w.Stats().Rejected)
}

func TestWarmer_Calculate_ClampsOvershoot(t *testing.T) {
	// A Step strategy whose discrete jumps do not evenly divide the range can
	// land above maxCap at t=1; calculate must clamp it back down.
	w := New(WithStrategy(Step), WithMinCapacity(0), WithMaxCapacity(0.7), WithStepCount(3))
	for i := 0; i <= 100; i++ {
		v := w.calculate(float64(i) / 100)
		assert.LessOrEqual(t, v, 0.7+1e-9)
	}
	assert.InDelta(t, 0.7, w.calculate(1.0), 1e-9)
}

func TestWarmer_Calculate_ClampsToMaxCap(t *testing.T) {
	// Force the defensive overshoot guard: a maxCap below the value the curve
	// would otherwise produce for the configured min/delta. newConfig never
	// yields this, so the field is set directly (white-box) to prove the clamp.
	w := New(WithStrategy(Linear), WithMinCapacity(0.2), WithMaxCapacity(1))
	w.cfg.maxCap = 0.5
	assert.InDelta(t, 0.5, w.calculate(1.0), 1e-9, "value above maxCap is clamped")
}

func TestWarmer_Calculate_ClampsInputDomain(t *testing.T) {
	w := New(WithStrategy(Logarithmic), WithMinCapacity(0.2), WithMaxCapacity(0.9))
	assert.InDelta(t, w.calculate(0), w.calculate(-5), 1e-9, "t<0 clamps to 0")
	assert.InDelta(t, w.calculate(1), w.calculate(5), 1e-9, "t>1 clamps to 1")
}

func TestWarmer_ProgressLocked_OverElapsedClampsToOne(t *testing.T) {
	w := New(WithDuration(time.Nanosecond))
	w.mu.Lock()
	w.warming = true
	w.start = time.Now().Add(-time.Hour)
	got := w.progressLocked()
	w.mu.Unlock()
	assert.InDelta(t, 1.0, got, 1e-9)
}

func TestWarmer_Calculate_Strategies(t *testing.T) {
	tests := []struct {
		name     string
		strategy Strategy
	}{
		{"linear", Linear},
		{"exponential", Exponential},
		{"logarithmic", Logarithmic},
		{"step", Step},
		{"unknown falls back to linear", Strategy(99)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := New(WithStrategy(tt.strategy), WithMinCapacity(0.1), WithMaxCapacity(1))
			at0 := w.calculate(0)
			at1 := w.calculate(1)
			assert.GreaterOrEqual(t, at0, 0.1, "capacity at t=0 starts at min")
			assert.LessOrEqual(t, at1, 1.0, "capacity at t=1 caps at max")
			assert.GreaterOrEqual(t, at1, at0, "capacity is non-decreasing")
		})
	}
}

func TestWarmer_Calculate_Monotonic(t *testing.T) {
	for _, s := range []Strategy{Linear, Exponential, Logarithmic, Step} {
		w := New(WithStrategy(s), WithMinCapacity(0), WithMaxCapacity(1))
		prev := -1.0
		for i := 0; i <= 100; i++ {
			cap := w.calculate(float64(i) / 100)
			assert.GreaterOrEqual(t, cap, prev, "strategy %s must be non-decreasing", s)
			assert.LessOrEqual(t, cap, 1.0)
			prev = cap
		}
	}
}

func TestWarmer_ConcurrentAccess(t *testing.T) {
	w := New(WithDuration(200*time.Millisecond), WithInterval(10*time.Millisecond))
	w.Start()
	defer w.Stop()

	testx.HammerVoid(50, 500, func() {
		w.Allow()
		_ = w.Capacity()
		_ = w.Progress()
		_ = w.Stats()
		_ = w.IsWarming()
	})
}

func TestWarmer_ConcurrentExecute(t *testing.T) {
	w := New(WithMinCapacity(0.5), WithMaxCapacity(0.5))
	testx.HammerVoid(50, 200, func() {
		_, _ = Execute(w, context.Background(), func(_ context.Context, _ WarmupController) (int, error) {
			return 1, nil
		})
	})
	s := w.Stats()
	assert.Equal(t, int64(50*200), s.Allowed+s.Rejected)
}

func TestWarmer_ConcurrentTryExecute(t *testing.T) {
	w := New(WithMinCapacity(0.5), WithMaxCapacity(0.5))
	testx.HammerVoid(50, 200, func() {
		_, _, _ = TryExecute(w, context.Background(), func(_ context.Context, _ WarmupController) (int, error) {
			return 1, nil
		})
	})
	s := w.Stats()
	assert.Equal(t, int64(50*200), s.Allowed+s.Rejected)
}

func TestWarmer_ConcurrentStartStop(t *testing.T) {
	w := New(WithDuration(50*time.Millisecond), WithInterval(10*time.Millisecond))
	testx.HammerVoid(20, 50, func() {
		w.Start()
		w.Stop()
	})
	w.Stop()
}
