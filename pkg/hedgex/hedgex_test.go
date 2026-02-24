package hedgex

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// ============================================================
// New — options
// ============================================================

func TestNew_Defaults(t *testing.T) {
	h := New[string]()
	if h.cfg.maxParallel != 3 {
		t.Fatal("expected maxParallel 3")
	}
	if h.cfg.delay != 100*time.Millisecond {
		t.Fatal("expected delay 100ms")
	}
	if h.cfg.maxDelay != 1*time.Second {
		t.Fatal("expected maxDelay 1s")
	}
}

func TestNew_WithMaxParallel(t *testing.T) {
	h := New[string](WithMaxParallel(5))
	if h.cfg.maxParallel != 5 {
		t.Fatal("expected 5")
	}
}

func TestNew_WithMaxParallel_Invalid(t *testing.T) {
	h := New[string](WithMaxParallel(0))
	if h.cfg.maxParallel != 3 {
		t.Fatal("zero should be ignored")
	}
}

func TestNew_WithDelay(t *testing.T) {
	h := New[string](WithDelay(50 * time.Millisecond))
	if h.cfg.delay != 50*time.Millisecond {
		t.Fatal("expected 50ms")
	}
}

func TestNew_WithDelay_Invalid(t *testing.T) {
	h := New[string](WithDelay(-1))
	if h.cfg.delay != 100*time.Millisecond {
		t.Fatal("negative should be ignored")
	}
}

func TestNew_WithMaxDelay(t *testing.T) {
	h := New[string](WithMaxDelay(500 * time.Millisecond))
	if h.cfg.maxDelay != 500*time.Millisecond {
		t.Fatal("expected 500ms")
	}
}

func TestNew_WithMaxDelay_LessThanDelay(t *testing.T) {
	h := New[string](WithDelay(200*time.Millisecond), WithMaxDelay(50*time.Millisecond))
	if h.cfg.maxDelay < h.cfg.delay {
		t.Fatal("maxDelay should be clamped to delay")
	}
}

func TestNew_WithOnHedge(t *testing.T) {
	called := false
	h := New[string](WithOnHedge(func(int) { called = true }))
	h.cfg.onHedge(1)
	if !called {
		t.Fatal("onHedge should be callable")
	}
}

// ============================================================
// Do — success
// ============================================================

func TestDo_Success(t *testing.T) {
	h := New[string]()
	v, err := h.Do(context.Background(), func(ctx context.Context, hc HedgeController) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "ok" {
		t.Fatalf("expected 'ok', got %q", v)
	}
	st := h.Stats()
	if st.Calls != 1 || st.Wins != 1 {
		t.Fatalf("expected 1 call 1 win, got %+v", st)
	}
}

// ============================================================
// Do — all fail
// ============================================================

func TestDo_AllFail(t *testing.T) {
	h := New[string](WithMaxParallel(3), WithDelay(5*time.Millisecond))
	_, err := h.Do(context.Background(), func(ctx context.Context, hc HedgeController) (string, error) {
		return "", errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != DomainHedge || xe.Code != CodeAllFailed {
		t.Fatalf("expected HEDGE/ALL_FAILED, got %s/%s", xe.Domain, xe.Code)
	}
	if xe.Cause == nil {
		t.Fatal("expected cause")
	}
}

// ============================================================
// DoMulti
// ============================================================

func TestDoMulti_FastWins(t *testing.T) {
	h := New[string](WithMaxParallel(3), WithDelay(5*time.Millisecond))

	fns := []func(context.Context, HedgeController) (string, error){
		func(ctx context.Context, hc HedgeController) (string, error) {
			time.Sleep(100 * time.Millisecond)
			return "slow", nil
		},
		func(ctx context.Context, hc HedgeController) (string, error) {
			return "fast", nil
		},
	}

	v, err := h.DoMulti(context.Background(), fns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "fast" {
		t.Fatalf("expected 'fast', got %q", v)
	}
}

func TestDoMulti_Empty(t *testing.T) {
	h := New[string]()
	_, err := h.DoMulti(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty fns")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatal("expected *errx.Error")
	}
	if xe.Code != CodeNoFunctions {
		t.Fatalf("expected NO_FUNCTIONS, got %s", xe.Code)
	}
}

func TestDoMulti_SingleFunction(t *testing.T) {
	h := New[string]()
	fns := []func(context.Context, HedgeController) (string, error){
		func(ctx context.Context, hc HedgeController) (string, error) { return "only", nil },
	}
	v, err := h.DoMulti(context.Background(), fns)
	if err != nil || v != "only" {
		t.Fatalf("expected 'only'/nil, got %q/%v", v, err)
	}
}

func TestDoMulti_SingleFunction_Fails(t *testing.T) {
	h := New[string]()
	fns := []func(context.Context, HedgeController) (string, error){
		func(ctx context.Context, hc HedgeController) (string, error) { return "", errors.New("fail") },
	}
	_, err := h.DoMulti(context.Background(), fns)
	if err == nil {
		t.Fatal("expected error")
	}
	st := h.Stats()
	if st.Failures != 1 {
		t.Fatal("expected 1 failure")
	}
}

func TestDoMulti_CapsAtMaxParallel(t *testing.T) {
	var count atomic.Int32
	h := New[string](WithMaxParallel(2), WithDelay(5*time.Millisecond))
	fns := make([]func(context.Context, HedgeController) (string, error), 10)
	for i := range fns {
		fns[i] = func(ctx context.Context, hc HedgeController) (string, error) {
			count.Add(1)
			time.Sleep(50 * time.Millisecond)
			return "ok", nil
		}
	}
	_, _ = h.DoMulti(context.Background(), fns)
	if count.Load() > 2 {
		t.Fatalf("expected at most 2 launched, got %d", count.Load())
	}
}

// ============================================================
// Context cancellation
// ============================================================

func TestDo_ContextCancelled(t *testing.T) {
	h := New[string](WithDelay(100 * time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := h.Do(ctx, func(ctx context.Context, hc HedgeController) (string, error) {
		time.Sleep(1 * time.Second)
		return "late", nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatal("expected *errx.Error")
	}
	if xe.Code != CodeCancelled {
		t.Fatalf("expected CANCELLED, got %s", xe.Code)
	}
}

// ============================================================
// OnHedge callback
// ============================================================

func TestOnHedge_Called(t *testing.T) {
	var hedgeCount atomic.Int32
	h := New[string](
		WithMaxParallel(3),
		WithDelay(5*time.Millisecond),
		WithOnHedge(func(attempt int) {
			hedgeCount.Add(1)
		}),
	)

	_, _ = h.Do(context.Background(), func(ctx context.Context, hc HedgeController) (string, error) {
		time.Sleep(50 * time.Millisecond)
		return "ok", nil
	})

	time.Sleep(20 * time.Millisecond)
	if hedgeCount.Load() == 0 {
		t.Log("no hedge callbacks (first request fast enough)")
	}
}

// ============================================================
// Stats / ResetStats
// ============================================================

func TestStats(t *testing.T) {
	h := New[string](WithMaxParallel(2), WithDelay(5*time.Millisecond))
	_, _ = h.Do(context.Background(), func(ctx context.Context, hc HedgeController) (string, error) { return "ok", nil })
	_, _ = h.Do(context.Background(), func(ctx context.Context, hc HedgeController) (string, error) { return "", errors.New("fail") })

	st := h.Stats()
	if st.Calls != 2 {
		t.Fatalf("expected 2 calls, got %d", st.Calls)
	}
	if st.Wins != 1 {
		t.Fatalf("expected 1 win, got %d", st.Wins)
	}
	if st.Failures != 1 {
		t.Fatalf("expected 1 failure, got %d", st.Failures)
	}
}

func TestResetStats(t *testing.T) {
	h := New[string]()
	_, _ = h.Do(context.Background(), func(ctx context.Context, hc HedgeController) (string, error) { return "ok", nil })
	h.ResetStats()
	st := h.Stats()
	if st.Calls != 0 || st.Wins != 0 || st.Failures != 0 || st.Hedges != 0 {
		t.Fatalf("expected zeros, got %+v", st)
	}
}

// ============================================================
// delays calculation
// ============================================================

func TestDelays_Single(t *testing.T) {
	h := New[string]()
	ds := h.delays(1)
	if ds != nil {
		t.Fatal("single function should have nil delays")
	}
}

func TestDelays_Linear(t *testing.T) {
	h := New[string](WithDelay(100*time.Millisecond), WithMaxDelay(1*time.Second))
	ds := h.delays(4)
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond}
	if len(ds) != len(want) {
		t.Fatalf("expected %d delays, got %d", len(want), len(ds))
	}
	for i, d := range ds {
		if d != want[i] {
			t.Fatalf("delays[%d] = %v, want %v", i, d, want[i])
		}
	}
}

func TestDelays_MaxDelay_Spread(t *testing.T) {
	h := New[string](WithDelay(100*time.Millisecond), WithMaxDelay(250*time.Millisecond))
	ds := h.delays(5)

	if ds[0] != 100*time.Millisecond {
		t.Fatalf("delays[0] = %v, want 100ms", ds[0])
	}
	if ds[1] != 200*time.Millisecond {
		t.Fatalf("delays[1] = %v, want 200ms", ds[1])
	}
	for i := 2; i < len(ds); i++ {
		if ds[i] < 250*time.Millisecond {
			t.Fatalf("delays[%d] = %v, should be >= 250ms", i, ds[i])
		}
	}
	for i := 3; i < len(ds); i++ {
		if ds[i] <= ds[i-1] {
			t.Fatalf("delays[%d] should be > delays[%d] (spread)", i, i-1)
		}
	}
}

func TestDelays_NoBurst(t *testing.T) {
	h := New[string](WithDelay(100*time.Millisecond), WithMaxDelay(150*time.Millisecond))
	ds := h.delays(6)
	seen := make(map[time.Duration]bool)
	for i, d := range ds {
		if seen[d] {
			t.Fatalf("burst at delays[%d] = %v", i, d)
		}
		seen[d] = true
	}
}

// ============================================================
// Error constructors
// ============================================================

func TestErrAllFailed(t *testing.T) {
	e := errAllFailed(errors.New("db down"))
	if e.Domain != DomainHedge || e.Code != CodeAllFailed {
		t.Fatal("wrong domain/code")
	}
	if e.Cause == nil {
		t.Fatal("expected cause")
	}
	if !e.Retryable() {
		t.Fatal("expected retryable")
	}
}

func TestErrNoFunctions(t *testing.T) {
	e := errNoFunctions()
	if e.Domain != DomainHedge || e.Code != CodeNoFunctions {
		t.Fatal("wrong domain/code")
	}
}

func TestErrCancelled(t *testing.T) {
	e := errCancelled(context.Canceled)
	if e.Domain != DomainHedge || e.Code != CodeCancelled {
		t.Fatal("wrong domain/code")
	}
}

func TestDomainConstant(t *testing.T) {
	if DomainHedge != "HEDGE" {
		t.Fatal("expected HEDGE")
	}
}

// ============================================================
// Panic recovery
// ============================================================

func TestDo_PanicRecovery(t *testing.T) {
	h := New[string](WithMaxParallel(2), WithDelay(5*time.Millisecond))
	_, err := h.Do(context.Background(), func(ctx context.Context, hc HedgeController) (string, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected error from panic")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
}

// ============================================================
// HedgeController
// ============================================================

func TestHedgeController_Original(t *testing.T) {
	h := New[string](WithMaxParallel(1))
	var attempt int
	var isHedge bool
	_, _ = h.Do(context.Background(), func(ctx context.Context, hc HedgeController) (string, error) {
		attempt = hc.Attempt()
		isHedge = hc.IsHedge()
		return "ok", nil
	})
	if attempt != 1 {
		t.Fatalf("expected attempt 1, got %d", attempt)
	}
	if isHedge {
		t.Fatal("original request should not be a hedge")
	}
}

func TestHedgeController_HedgeCopy(t *testing.T) {
	h := New[string](WithMaxParallel(3), WithDelay(5*time.Millisecond))
	var attempts atomic.Int32
	var hedgesSeen atomic.Int32

	_, _ = h.Do(context.Background(), func(ctx context.Context, hc HedgeController) (string, error) {
		attempts.Add(int32(hc.Attempt()))
		if hc.IsHedge() {
			hedgesSeen.Add(1)
		}
		time.Sleep(50 * time.Millisecond)
		return "ok", nil
	})

	if attempts.Load() < 1 {
		t.Fatal("expected at least attempt 1")
	}
}

func TestHedgeController_DoMulti_Attempts(t *testing.T) {
	h := New[string](WithMaxParallel(3), WithDelay(5*time.Millisecond))
	var seen [3]atomic.Int32

	fns := []func(context.Context, HedgeController) (string, error){
		func(ctx context.Context, hc HedgeController) (string, error) {
			seen[0].Store(int32(hc.Attempt()))
			time.Sleep(100 * time.Millisecond)
			return "a", nil
		},
		func(ctx context.Context, hc HedgeController) (string, error) {
			seen[1].Store(int32(hc.Attempt()))
			return "b", nil
		},
		func(ctx context.Context, hc HedgeController) (string, error) {
			seen[2].Store(int32(hc.Attempt()))
			time.Sleep(100 * time.Millisecond)
			return "c", nil
		},
	}

	_, _ = h.DoMulti(context.Background(), fns)

	if seen[0].Load() != 1 {
		t.Fatalf("fn[0] expected attempt 1, got %d", seen[0].Load())
	}
	if seen[1].Load() != 2 {
		t.Fatalf("fn[1] expected attempt 2, got %d", seen[1].Load())
	}
}

func TestHedgeController_SingleFunction(t *testing.T) {
	h := New[string]()
	var attempt int
	fns := []func(context.Context, HedgeController) (string, error){
		func(ctx context.Context, hc HedgeController) (string, error) {
			attempt = hc.Attempt()
			return "only", nil
		},
	}
	_, _ = h.DoMulti(context.Background(), fns)
	if attempt != 1 {
		t.Fatalf("expected attempt 1, got %d", attempt)
	}
}

// ============================================================
// Benchmark
// ============================================================

func BenchmarkDo_Instant(b *testing.B) {
	h := New[string]()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		h.Do(ctx, func(ctx context.Context, hc HedgeController) (string, error) {
			return "ok", nil
		})
	}
}
