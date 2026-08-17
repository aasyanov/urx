package hedgex

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
	assert.Equal(t, DefaultHedgeProbability, newConfig(nil).hedgeProb)
}

func TestNew_NilOptionIgnored(t *testing.T) {
	h := New(nil, WithMaxParallel(2))
	assert.Equal(t, 2, h.MaxParallel())
}

func TestWithMaxParallel(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want int
	}{
		{"default", nil, DefaultMaxParallel},
		{"custom", WithMaxParallel(5), 5},
		{"zero ignored", WithMaxParallel(0), DefaultMaxParallel},
		{"negative ignored", WithMaxParallel(-1), DefaultMaxParallel},
		{"one disables hedging", WithMaxParallel(1), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			cfg := newConfig(opts)
			assert.Equal(t, tt.want, cfg.maxParallel)
		})
	}
}

func TestWithDelay(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want time.Duration
	}{
		{"default", nil, DefaultDelay},
		{"custom", WithDelay(50 * time.Millisecond), 50 * time.Millisecond},
		{"zero ignored", WithDelay(0), DefaultDelay},
		{"negative ignored", WithDelay(-time.Second), DefaultDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			cfg := newConfig(opts)
			assert.Equal(t, tt.want, cfg.delay)
		})
	}
}

func TestWithMaxDelay(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want time.Duration
	}{
		{"default", nil, DefaultMaxDelay},
		{"custom", []Option{WithMaxDelay(2 * time.Second)}, 2 * time.Second},
		{"zero ignored", []Option{WithMaxDelay(0)}, DefaultMaxDelay},
		{"raised to delay", []Option{WithDelay(200 * time.Millisecond), WithMaxDelay(50 * time.Millisecond)}, 200 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newConfig(tt.opts)
			assert.Equal(t, tt.want, cfg.maxDelay)
		})
	}
}

func TestWithOp(t *testing.T) {
	assert.Equal(t, "db.read", newConfig([]Option{WithOp("db.read")}).opOrDefault())
	assert.Equal(t, opExecute, newConfig([]Option{WithOp("")}).opOrDefault())
	assert.Equal(t, opExecute, newConfig(nil).opOrDefault())
}

func TestWithOnHedge(t *testing.T) {
	var n int
	cfg := newConfig([]Option{WithOnHedge(func(int) { n++ })})
	require.NotNil(t, cfg.onHedge)
	cfg.onHedge(2)
	assert.Equal(t, 1, n)
}

func TestWithHedgeProbability(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want float64
	}{
		{"default", nil, DefaultHedgeProbability},
		{"custom", WithHedgeProbability(0.5), 0.5},
		{"zero ignored", WithHedgeProbability(0), DefaultHedgeProbability},
		{"negative ignored", WithHedgeProbability(-1), DefaultHedgeProbability},
		{"clamped above one", WithHedgeProbability(1.5), DefaultHedgeProbability},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			cfg := newConfig(opts)
			assert.Equal(t, tt.want, cfg.hedgeProb)
		})
	}
}

func TestExecute_HedgeProbability_SkipsFanout(t *testing.T) {
	h := New(
		WithDelay(5*time.Millisecond),
		WithMaxParallel(3),
		WithHedgeProbability(0.5),
		withRand(func() float64 { return 0.99 }),
	)
	var hedges atomic.Int32
	fn := func(_ context.Context, hc HedgeController) (int, error) {
		if hc.IsHedge() {
			hedges.Add(1)
			return 2, nil
		}
		time.Sleep(30 * time.Millisecond)
		return 1, nil
	}
	got, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	assert.Equal(t, 1, got)
	assert.Zero(t, hedges.Load())
	assert.Equal(t, int64(0), h.Stats().Hedges)
}

func TestExecute_HedgeProbability_AllowsFanout(t *testing.T) {
	h := New(
		WithDelay(5*time.Millisecond),
		WithMaxParallel(2),
		WithHedgeProbability(0.5),
		withRand(func() float64 { return 0.1 }),
	)
	fn := func(ctx context.Context, hc HedgeController) (int, error) {
		if hc.IsHedge() {
			return 2, nil
		}
		<-ctx.Done()
		return 0, ctx.Err()
	}
	got, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	assert.Equal(t, 2, got)
	assert.Greater(t, h.Stats().Hedges, int64(0))
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

func TestController_Cancel_CancelsCopyContext(t *testing.T) {
	sawCancel := make(chan struct{})
	fn := func(ctx context.Context, hc HedgeController) (string, error) {
		if hc.IsHedge() {
			<-sawCancel
			return "hedge", nil
		}
		hc.Cancel()
		select {
		case <-ctx.Done():
			close(sawCancel)
			return "", ctx.Err()
		case <-time.After(time.Second):
			close(sawCancel)
			t.Error("copy context was not cancelled")
			return "", errors.New("copy context live after Cancel")
		}
	}
	h := New(WithDelay(10*time.Millisecond), WithMaxParallel(2))
	got, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	assert.Equal(t, "hedge", got)
}

func TestController_Cancel_DoesNotCancelSibling(t *testing.T) {
	fn := func(ctx context.Context, hc HedgeController) (string, error) {
		if !hc.IsHedge() {
			hc.Cancel()
			<-ctx.Done()
			return "", ctx.Err()
		}
		select {
		case <-ctx.Done():
			return "", errors.New("sibling context was cancelled")
		default:
		}
		return "hedge", nil
	}
	h := New(WithDelay(15*time.Millisecond), WithMaxParallel(2))
	got, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	assert.Equal(t, "hedge", got)
}

func TestController_Cancel_WithdrawnCopyStillReaped(t *testing.T) {
	fn := func(ctx context.Context, hc HedgeController) (int, error) {
		hc.Cancel()
		<-ctx.Done()
		return 0, ctx.Err()
	}
	h := New(WithDelay(5*time.Millisecond), WithMaxParallel(3))
	_, err := Execute(h, context.Background(), fn)
	require.ErrorIs(t, err, ErrAllFailed)
}

func TestExecute_FirstWinStillCancelsLosers(t *testing.T) {
	loserDone := make(chan struct{})
	fn := func(ctx context.Context, hc HedgeController) (string, error) {
		if hc.IsHedge() {
			return "hedge", nil
		}
		<-ctx.Done()
		close(loserDone)
		return "", ctx.Err()
	}
	h := New(WithDelay(10*time.Millisecond), WithMaxParallel(2))
	got, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	assert.Equal(t, "hedge", got)
	select {
	case <-loserDone:
	case <-time.After(time.Second):
		t.Fatal("losing copy's context was not cancelled")
	}
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

func TestExecuteMulti_AlreadyCancelled(t *testing.T) {
	h := New()
	_, err := ExecuteMulti(h, testx.CancelledCtx(), []HedgeFunc[int]{okFn(1)})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int64(1), h.Stats().Failures)
}

func TestExecute_HedgeFailsWhilePrimaryInFlight(t *testing.T) {
	// Hedge fails while the primary is still in flight; dispatch must wait for
	// the primary (pending > 0 continue) rather than launching copy 3 early.
	var primaryRunning atomic.Bool
	fn := func(ctx context.Context, hc HedgeController) (string, error) {
		if hc.IsHedge() {
			return "", errSentinel
		}
		primaryRunning.Store(true)
		time.Sleep(80 * time.Millisecond)
		return "primary", nil
	}
	h := New(WithDelay(15*time.Millisecond), WithMaxParallel(2))
	got, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	assert.Equal(t, "primary", got)
	assert.True(t, primaryRunning.Load())
	assert.Equal(t, int64(1), h.Stats().Hedges)
}

func TestExecuteMulti_BackendsCountsLaunchable(t *testing.T) {
	var backends atomic.Int32
	var attempt atomic.Int32
	primary := func(ctx context.Context, hc HedgeController) (int, error) {
		backends.Store(int32(hc.Backends()))
		<-ctx.Done()
		return 0, ctx.Err()
	}
	hedge := func(_ context.Context, hc HedgeController) (int, error) {
		attempt.Store(int32(hc.Attempt()))
		return 42, nil
	}
	fns := []HedgeFunc[int]{primary, nil, hedge}
	h := New(WithDelay(10*time.Millisecond), WithMaxParallel(3))
	got, err := ExecuteMulti(h, context.Background(), fns)
	require.NoError(t, err)
	assert.Equal(t, 42, got)
	assert.Equal(t, int32(2), backends.Load())
	assert.Equal(t, int32(2), attempt.Load())
}

func TestLaunchableCount(t *testing.T) {
	assert.Equal(t, 0, launchableCount([]HedgeFunc[int]{nil, nil}))
	assert.Equal(t, 2, launchableCount([]HedgeFunc[int]{okFn(1), nil, okFn(2)}))
}

func TestFirstNonNil(t *testing.T) {
	assert.Nil(t, firstNonNil([]HedgeFunc[int]{nil, nil}))
	got := firstNonNil([]HedgeFunc[int]{nil, okFn(7)})
	require.NotNil(t, got)
	v, err := got(context.Background(), &execution{})
	require.NoError(t, err)
	assert.Equal(t, 7, v)
}

func TestHedgerRand_DefaultSource(t *testing.T) {
	h := New(WithHedgeProbability(0.5))
	r := h.rand()
	assert.GreaterOrEqual(t, r, 0.0)
	assert.Less(t, r, 1.0)
}

func TestDelayFor(t *testing.T) {
	delays := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	d, ok := delayFor(delays, 1)
	assert.True(t, ok)
	assert.Equal(t, 100*time.Millisecond, d)
	d, ok = delayFor(delays, 2)
	assert.True(t, ok)
	assert.Equal(t, 200*time.Millisecond, d)
	_, ok = delayFor(delays, 0)
	assert.False(t, ok)
	_, ok = delayFor(delays, 3)
	assert.False(t, ok)
}

func TestResetTimer_RearmAfterFired(t *testing.T) {
	timer := time.NewTimer(time.Nanosecond)
	time.Sleep(2 * time.Millisecond)
	select {
	case <-timer.C:
	default:
	}
	start := time.Now()
	resetTimer(timer, 50*time.Millisecond, start)
	select {
	case <-timer.C:
		t.Fatal("timer must not fire immediately after rearm")
	default:
	}
	timer.Stop()
}

func TestResetTimer_DrainDefaultWhenAlreadyExpired(t *testing.T) {
	timer := time.NewTimer(time.Nanosecond)
	time.Sleep(2 * time.Millisecond)
	select {
	case <-timer.C:
	default:
	}
	resetTimer(timer, 10*time.Millisecond, time.Now())
	timer.Stop()
}

func TestDispatch_PendingContinueWhenHedgeFailsFirst(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	record := func(s string) {
		mu.Lock()
		order = append(order, s)
		mu.Unlock()
	}

	h := New(WithDelay(20*time.Millisecond), WithMaxParallel(2))
	fns := []HedgeFunc[string]{
		func(ctx context.Context, _ HedgeController) (string, error) {
			record("primary-start")
			select {
			case <-time.After(100 * time.Millisecond):
				record("primary-win")
				return "primary", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
		func(context.Context, HedgeController) (string, error) {
			record("hedge-fail")
			return "", errSentinel
		},
	}
	got, err := ExecuteMulti(h, context.Background(), fns)
	require.NoError(t, err)
	assert.Equal(t, "primary", got)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, order, 3)
	assert.Equal(t, "primary-start", order[0])
	assert.Equal(t, "hedge-fail", order[1])
	assert.Equal(t, "primary-win", order[2])
}

func TestRun_DropsResultWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := make(chan result[int])
	hc := &execution{attempt: 1, backends: 1, start: time.Now()}
	run(New(), ctx, ctx, func(context.Context, HedgeController) (int, error) {
		return 1, nil
	}, hc, ch)
	select {
	case <-ch:
		t.Fatal("result must be dropped when hedge context is cancelled before send")
	default:
	}
}

func TestExecuteMulti_LeadingNilSchedulesHedge(t *testing.T) {
	// A nil leading slot must not skew the delay schedule for the second
	// launchable backend.
	primary := func(ctx context.Context, _ HedgeController) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	fns := []HedgeFunc[string]{nil, primary, okFn("replica")}
	h := New(WithDelay(10*time.Millisecond), WithMaxParallel(3))
	got, err := ExecuteMulti(h, context.Background(), fns)
	require.NoError(t, err)
	assert.Equal(t, "replica", got)
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
	assert.Equal(t, int32(2), fired.Load())
}

func TestExecute_OnHedgeRunsSynchronously(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	h := New(
		WithDelay(10*time.Millisecond),
		WithMaxParallel(2),
		WithOnHedge(func(int) {
			mu.Lock()
			order = append(order, "hook")
			mu.Unlock()
		}),
	)
	fn := func(ctx context.Context, hc HedgeController) (int, error) {
		if hc.IsHedge() {
			mu.Lock()
			order = append(order, "hedge")
			mu.Unlock()
			return 2, nil
		}
		<-ctx.Done()
		return 0, ctx.Err()
	}
	_, err := Execute(h, context.Background(), fn)
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"hook", "hedge"}, order)
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
		_, err := Execute(h, context.Background(), func(ctx context.Context, hc HedgeController) (int, error) {
			if hc.IsHedge() {
				return hc.Attempt(), nil
			}
			<-ctx.Done()
			return 0, ctx.Err()
		})
		return err
	})
	assert.Greater(t, h.Stats().Hedges, int64(0))
}
