package hedgex

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

var errSentinel = errors.New("hedgex_test: simulated failure")

func okFn[T any](v T) HedgeFunc[T] {
	return func(context.Context, HedgeController) (T, error) { return v, nil }
}

func errFn[T any](err error) HedgeFunc[T] {
	return func(context.Context, HedgeController) (T, error) {
		var zero T
		return zero, err
	}
}

func TestNew_Defaults(t *testing.T) {
	h := New()
	assert.Equal(t, DefaultMaxParallel, h.MaxParallel())
	assert.Equal(t, DefaultDelay, h.Delay())
	assert.Equal(t, DefaultMaxDelay, h.MaxDelay())
}

func TestExecute_ImmediateSuccess(t *testing.T) {
	h := New(WithDelay(time.Hour)) // hedges never fire
	got, err := Execute(h, context.Background(), okFn("primary"))
	require.NoError(t, err)
	assert.Equal(t, "primary", got)

	s := h.Stats()
	assert.Equal(t, int64(1), s.Calls)
	assert.Equal(t, int64(1), s.Wins)
	assert.Equal(t, int64(0), s.Hedges)
	assert.Equal(t, int64(0), s.Failures)
}

func TestExecute_NilFunc(t *testing.T) {
	h := New()
	_, err := Execute(h, context.Background(), HedgeFunc[int](nil))
	require.ErrorIs(t, err, ErrNilFunc)
	assert.Equal(t, int64(1), h.Stats().Failures)
}

func TestExecute_AlreadyCancelled(t *testing.T) {
	h := New()
	_, err := Execute(h, testx.CancelledCtx(), okFn(1))
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int64(1), h.Stats().Failures)
}

func TestExecute_AllFail(t *testing.T) {
	sentinel := errors.New("backend down")
	h := New(WithDelay(time.Millisecond), WithMaxParallel(3))
	_, err := Execute(h, context.Background(), errFn[int](sentinel))
	require.ErrorIs(t, err, ErrAllFailed)
	require.ErrorIs(t, err, sentinel)

	s := h.Stats()
	assert.Equal(t, int64(1), s.Calls)
	assert.Equal(t, int64(1), s.Failures)
	assert.Equal(t, int64(2), s.Hedges) // 3 copies - original
}

func TestExecute_HedgeWinsWhenPrimaryStalls(t *testing.T) {
	// Primary blocks until its context is cancelled; the hedge returns fast.
	var primaryStarted atomic.Bool
	fn := func(ctx context.Context, hc HedgeController) (string, error) {
		if hc.IsHedge() {
			return "hedge", nil
		}
		primaryStarted.Store(true)
		<-ctx.Done()
		return "", ctx.Err()
	}

	h := New(WithDelay(20*time.Millisecond), WithMaxParallel(2))
	got, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	assert.Equal(t, "hedge", got)
	assert.True(t, primaryStarted.Load())
	assert.Equal(t, int64(1), h.Stats().Hedges)
}

func TestExecute_PrimaryWinsBeforeHedge(t *testing.T) {
	// Primary returns within the delay window, so no hedge should launch.
	fn := func(_ context.Context, hc HedgeController) (string, error) {
		if hc.IsHedge() {
			return "hedge", nil
		}
		return "primary", nil
	}
	h := New(WithDelay(50*time.Millisecond), WithMaxParallel(3))
	got, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	assert.Equal(t, "primary", got)
	assert.Equal(t, int64(0), h.Stats().Hedges)
}

func TestExecute_ContextCancelledMidFlight(t *testing.T) {
	h := New(WithDelay(time.Millisecond), WithMaxParallel(2))
	ctx, cancel := context.WithCancel(context.Background())

	fn := func(ctx context.Context, _ HedgeController) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Execute(h, ctx, fn)
	require.ErrorIs(t, err, ErrCancelled)
}

func TestExecute_PanicBecomesError(t *testing.T) {
	h := New(WithDelay(time.Hour))
	_, err := Execute(h, context.Background(), func(context.Context, HedgeController) (int, error) {
		panic("boom")
	})
	pe := testx.RequirePanicError(t, err, opExecute)
	assert.Equal(t, "boom", pe.Value)
	require.ErrorIs(t, err, ErrAllFailed)
}

func TestExecute_PanicWithCustomOp(t *testing.T) {
	h := New(WithDelay(time.Hour), WithOp("db.read"))
	_, err := Execute(h, context.Background(), func(context.Context, HedgeController) (int, error) {
		panic("crash")
	})
	testx.RequirePanicError(t, err, "db.read")
}

func TestController_Fields(t *testing.T) {
	h := New(WithDelay(time.Hour), WithMaxParallel(4))
	got, err := Execute(h, context.Background(), func(_ context.Context, hc HedgeController) (int, error) {
		assert.Equal(t, 1, hc.Attempt())
		assert.False(t, hc.IsHedge())
		assert.Equal(t, 4, hc.Backends())
		assert.GreaterOrEqual(t, hc.Elapsed(), time.Duration(0))
		return 7, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 7, got)
}

func TestController_HedgeAttemptNumbers(t *testing.T) {
	attempts := make(chan int, 3)
	fn := func(ctx context.Context, hc HedgeController) (int, error) {
		attempts <- hc.Attempt()
		assert.Equal(t, 3, hc.Backends())
		if hc.Attempt() == 3 {
			return hc.Attempt(), nil
		}
		<-ctx.Done()
		return 0, ctx.Err()
	}
	h := New(WithDelay(10*time.Millisecond), WithMaxParallel(3))
	got, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	assert.Equal(t, 3, got)

	close(attempts)
	var seen []int
	for a := range attempts {
		seen = append(seen, a)
	}
	assert.Contains(t, seen, 1)
	assert.Contains(t, seen, 3)
}

func TestExecute_ContextDeadlineExceeded(t *testing.T) {
	// Every copy stalls; the context deadline (not an explicit cancel) ends it.
	h := New(WithDelay(time.Millisecond), WithMaxParallel(2))
	ctx, cancel := testx.TimedCtx(30 * time.Millisecond)
	defer cancel()

	fn := func(ctx context.Context, _ HedgeController) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	_, err := Execute(h, ctx, fn)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestController_Cancel_Withdraws(t *testing.T) {
	// The primary withdraws immediately; only the hedge can win.
	fn := func(_ context.Context, hc HedgeController) (string, error) {
		if !hc.IsHedge() {
			hc.Cancel()
			return "withdrawn", nil // discarded despite nil error
		}
		return "hedge", nil
	}
	h := New(WithDelay(15*time.Millisecond), WithMaxParallel(2))
	got, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	assert.Equal(t, "hedge", got)
}

func TestController_Cancel_Idempotent(t *testing.T) {
	e := &execution{attempt: 1}
	e.Cancel()
	e.Cancel()
	assert.True(t, e.isWithdrawn())
}

func TestExecute_AllWithdraw(t *testing.T) {
	// Every copy withdraws; the call must fail rather than hang.
	fn := func(_ context.Context, hc HedgeController) (int, error) {
		hc.Cancel()
		return 0, nil
	}
	h := New(WithDelay(5*time.Millisecond), WithMaxParallel(3))
	_, err := Execute(h, context.Background(), fn)
	require.ErrorIs(t, err, ErrAllFailed)
}

func TestExecuteMulti_FirstSucceeds(t *testing.T) {
	h := New(WithDelay(time.Hour), WithMaxParallel(3))
	fns := []HedgeFunc[string]{okFn("a"), okFn("b")}
	got, err := ExecuteMulti(h, context.Background(), fns)
	require.NoError(t, err)
	assert.Equal(t, "a", got)
}

func TestExecuteMulti_Empty(t *testing.T) {
	h := New()
	_, err := ExecuteMulti(h, context.Background(), []HedgeFunc[int]{})
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestExecuteMulti_AllNil(t *testing.T) {
	h := New()
	_, err := ExecuteMulti(h, context.Background(), []HedgeFunc[int]{nil, nil})
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestExecuteMulti_SkipsNilGaps(t *testing.T) {
	// nil at index 1 sits between two real backends. The primary stalls so the
	// dispatch loop must skip the gap and launch the index-2 backend.
	primary := func(ctx context.Context, _ HedgeController) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	fns := []HedgeFunc[string]{primary, nil, okFn("third")}
	h := New(WithDelay(10*time.Millisecond), WithMaxParallel(3))
	got, err := ExecuteMulti(h, context.Background(), fns)
	require.NoError(t, err)
	assert.Equal(t, "third", got)
}

func TestExecuteMulti_CapsAtMaxParallel(t *testing.T) {
	var launched atomic.Int32
	mk := func(v int) HedgeFunc[int] {
		return func(ctx context.Context, _ HedgeController) (int, error) {
			launched.Add(1)
			if v == 0 {
				return v, nil
			}
			<-ctx.Done()
			return 0, ctx.Err()
		}
	}
	h := New(WithDelay(5*time.Millisecond), WithMaxParallel(2))
	fns := []HedgeFunc[int]{mk(0), mk(1), mk(2), mk(3)}
	got, err := ExecuteMulti(h, context.Background(), fns)
	require.NoError(t, err)
	assert.Equal(t, 0, got)
	assert.LessOrEqual(t, launched.Load(), int32(2))
}

func TestExecuteMulti_FastFailureRelaunchesNextCopy(t *testing.T) {
	// Three backends with a huge delay so only fast failures can advance the
	// schedule. The first fails immediately, relaunching copy 2 while copy 3 is
	// still scheduled (exercises the rearm-timer branch of immediate relaunch);
	// copy 2 then succeeds.
	fail := func(context.Context, HedgeController) (string, error) {
		return "", errSentinel
	}
	win := func(context.Context, HedgeController) (string, error) {
		return "second", nil
	}
	stall := func(ctx context.Context, _ HedgeController) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	h := New(WithDelay(time.Hour), WithMaxParallel(3))
	got, err := ExecuteMulti(h, context.Background(), []HedgeFunc[string]{fail, win, stall})
	require.NoError(t, err)
	assert.Equal(t, "second", got)
}

func TestExecuteMulti_TrailingNilAfterFailure(t *testing.T) {
	// Two real backends fail fast, then trailing nils. After the second
	// failure the immediate-relaunch path finds only nil slots and must report
	// failure rather than hang.
	fns := []HedgeFunc[int]{errFn[int](errSentinel), errFn[int](errSentinel), nil, nil}
	h := New(WithDelay(5*time.Millisecond), WithMaxParallel(4))
	_, err := ExecuteMulti(h, context.Background(), fns)
	require.ErrorIs(t, err, ErrAllFailed)
	require.ErrorIs(t, err, errSentinel)
}

func TestNewConfig_FloorsAndClamps(t *testing.T) {
	// A directly-seeded sub-floor parallelism is raised to minParallel, and a
	// maxDelay below the per-copy delay is raised to the delay. This exercises
	// the construction-time invariants independently of the option guards.
	cfg := newConfig([]Option{
		func(c *config) { c.maxParallel = -5 },
		func(c *config) { c.delay = 200 * time.Millisecond },
		func(c *config) { c.maxDelay = 50 * time.Millisecond },
	})
	assert.Equal(t, minParallel, cfg.maxParallel)
	assert.Equal(t, cfg.delay, cfg.maxDelay)
}

func TestExecute_DisabledHedging(t *testing.T) {
	// MaxParallel=1 => single copy, no hedges ever (synchronous fast path).
	h := New(WithMaxParallel(1), WithDelay(time.Millisecond))
	assert.Equal(t, 1, h.MaxParallel())
	got, err := Execute(h, context.Background(), func(_ context.Context, hc HedgeController) (int, error) {
		assert.Equal(t, 1, hc.Attempt())
		assert.False(t, hc.IsHedge())
		assert.Equal(t, 1, hc.Backends())
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, got)

	s := h.Stats()
	assert.Equal(t, int64(0), s.Hedges)
	assert.Equal(t, int64(1), s.Wins)
}

func TestExecute_SingleBackendCancelled(t *testing.T) {
	// MaxParallel=1 fast path must honor an already-cancelled context.
	h := New(WithMaxParallel(1))
	_, err := Execute(h, testx.CancelledCtx(), okFn(1))
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)

	s := h.Stats()
	assert.Equal(t, int64(1), s.Calls)
	assert.Equal(t, int64(1), s.Failures)
}

func TestExecute_SingleBackendFails(t *testing.T) {
	h := New(WithMaxParallel(1))
	_, err := Execute(h, context.Background(), errFn[int](errSentinel))
	require.ErrorIs(t, err, ErrAllFailed)
	require.ErrorIs(t, err, errSentinel)
	assert.Equal(t, int64(1), h.Stats().Failures)
}

func TestExecute_SingleBackendPanics(t *testing.T) {
	h := New(WithMaxParallel(1))
	_, err := Execute(h, context.Background(), func(context.Context, HedgeController) (int, error) {
		panic("sync boom")
	})
	pe := testx.RequirePanicError(t, err, opExecute)
	assert.Equal(t, "sync boom", pe.Value)
}

func TestExecute_SingleBackendWithdraws(t *testing.T) {
	h := New(WithMaxParallel(1))
	_, err := Execute(h, context.Background(), func(_ context.Context, hc HedgeController) (int, error) {
		hc.Cancel()
		return 99, nil
	})
	require.ErrorIs(t, err, ErrAllFailed)
}

func TestExecuteMulti_SingleNonNilTakesSyncPath(t *testing.T) {
	// One real backend among nils: still the synchronous path, attempt 1.
	fns := []HedgeFunc[int]{nil, func(_ context.Context, hc HedgeController) (int, error) {
		assert.Equal(t, 1, hc.Attempt())
		assert.Equal(t, 1, hc.Backends())
		return 5, nil
	}, nil}
	h := New(WithMaxParallel(3))
	got, err := ExecuteMulti(h, context.Background(), fns)
	require.NoError(t, err)
	assert.Equal(t, 5, got)
}

func TestExecute_OnHedgeCallback(t *testing.T) {
	var fired atomic.Int32
	h := New(
		WithDelay(10*time.Millisecond),
		WithMaxParallel(3),
		WithOnHedge(func(int) { fired.Add(1) }),
	)
	fn := func(ctx context.Context, hc HedgeController) (int, error) {
		if hc.Attempt() == 3 {
			return 3, nil
		}
		<-ctx.Done()
		return 0, ctx.Err()
	}
	_, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	testx.Eventually(t, func() bool { return fired.Load() == 2 }, time.Second)
}

func TestExecute_OnHedgePanicIsContained(t *testing.T) {
	h := New(
		WithDelay(10*time.Millisecond),
		WithMaxParallel(2),
		WithOnHedge(func(int) { panic("hook panic") }),
	)
	fn := func(_ context.Context, hc HedgeController) (int, error) {
		if hc.IsHedge() {
			return 2, nil
		}
		time.Sleep(50 * time.Millisecond)
		return 1, nil
	}
	got, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	assert.Contains(t, []int{1, 2}, got)
}

func TestStats_Reset(t *testing.T) {
	h := New(WithDelay(time.Hour))
	_, _ = Execute(h, context.Background(), okFn(1))
	require.Equal(t, int64(1), h.Stats().Calls)
	h.ResetStats()
	assert.Equal(t, Stats{}, h.Stats())
}

func TestDelays(t *testing.T) {
	tests := []struct {
		name     string
		delay    time.Duration
		maxDelay time.Duration
		count    int
		want     []time.Duration
	}{
		{"single copy no delays", 100 * time.Millisecond, time.Second, 1, nil},
		{"zero count", 100 * time.Millisecond, time.Second, 0, nil},
		{
			name:     "linear within cap",
			delay:    100 * time.Millisecond,
			maxDelay: time.Second,
			count:    3,
			want:     []time.Duration{100 * time.Millisecond, 200 * time.Millisecond},
		},
		{
			name:     "spread past cap",
			delay:    400 * time.Millisecond,
			maxDelay: 500 * time.Millisecond,
			count:    4,
			// copy2 at 400ms (<500 cap), copy3 hits cap at 500ms,
			// copy4 spread by delay/4=100ms => 600ms.
			want: []time.Duration{400 * time.Millisecond, 500 * time.Millisecond, 600 * time.Millisecond},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(WithDelay(tt.delay), WithMaxDelay(tt.maxDelay), WithMaxParallel(tt.count))
			got := h.delays(tt.count)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExecute_RaceSafe(t *testing.T) {
	h := New(WithDelay(time.Millisecond), WithMaxParallel(3))
	testx.HammerNoError(t, 50, 200, func() error {
		_, err := Execute(h, context.Background(), func(_ context.Context, hc HedgeController) (int, error) {
			if hc.IsHedge() {
				return hc.Attempt(), nil
			}
			return 1, nil
		})
		return err
	})
}
