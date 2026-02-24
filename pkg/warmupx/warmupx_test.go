package warmupx

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// ============================================================
// Strategy.String
// ============================================================

func TestStrategy_String(t *testing.T) {
	tests := []struct {
		s    Strategy
		want string
	}{
		{Linear, "linear"},
		{Exponential, "exponential"},
		{Logarithmic, "logarithmic"},
		{Step, "step"},
		{Strategy(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("Strategy(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

// ============================================================
// Default config
// ============================================================

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	if cfg.strategy != Linear {
		t.Fatalf("expected Linear, got %v", cfg.strategy)
	}
	if cfg.minCap != 0.1 {
		t.Fatalf("expected 0.1, got %v", cfg.minCap)
	}
	if cfg.maxCap != 1.0 {
		t.Fatalf("expected 1.0, got %v", cfg.maxCap)
	}
	if cfg.duration != time.Minute {
		t.Fatalf("expected 1m, got %v", cfg.duration)
	}
	if cfg.stepCount != 10 {
		t.Fatalf("expected 10, got %v", cfg.stepCount)
	}
	if cfg.expFactor != 3.0 {
		t.Fatalf("expected 3.0, got %v", cfg.expFactor)
	}
}

// ============================================================
// New — options
// ============================================================

func TestNew_Defaults(t *testing.T) {
	w := New()
	if w.capacity != 0.1 {
		t.Fatalf("expected initial capacity 0.1, got %v", w.capacity)
	}
}

func TestNew_WithStrategy(t *testing.T) {
	w := New(WithStrategy(Exponential))
	if w.cfg.strategy != Exponential {
		t.Fatal("expected Exponential")
	}
}

func TestNew_WithMinCapacity(t *testing.T) {
	w := New(WithMinCapacity(0.3))
	if w.cfg.minCap != 0.3 {
		t.Fatalf("expected 0.3, got %v", w.cfg.minCap)
	}
	if w.capacity != 0.3 {
		t.Fatal("initial capacity should equal minCap")
	}
}

func TestNew_WithMinCapacity_Invalid(t *testing.T) {
	w := New(WithMinCapacity(-0.5))
	if w.cfg.minCap != 0.1 {
		t.Fatalf("negative minCap should be ignored, got %v", w.cfg.minCap)
	}
	w2 := New(WithMinCapacity(2.0))
	if w2.cfg.minCap != 0.1 {
		t.Fatalf("minCap > 1 should be ignored, got %v", w2.cfg.minCap)
	}
}

func TestNew_WithMaxCapacity(t *testing.T) {
	w := New(WithMaxCapacity(0.8))
	if w.cfg.maxCap != 0.8 {
		t.Fatalf("expected 0.8, got %v", w.cfg.maxCap)
	}
}

func TestNew_WithMaxCapacity_Invalid(t *testing.T) {
	w := New(WithMaxCapacity(0))
	if w.cfg.maxCap != 1.0 {
		t.Fatalf("zero maxCap should be ignored, got %v", w.cfg.maxCap)
	}
}

func TestNew_MaxCapLessThanMinCap(t *testing.T) {
	w := New(WithMinCapacity(0.8), WithMaxCapacity(0.3))
	if w.cfg.maxCap < w.cfg.minCap {
		t.Fatal("maxCap should be clamped to minCap")
	}
}

func TestNew_WithDuration(t *testing.T) {
	w := New(WithDuration(5 * time.Second))
	if w.cfg.duration != 5*time.Second {
		t.Fatal("expected 5s")
	}
}

func TestNew_WithDuration_Invalid(t *testing.T) {
	w := New(WithDuration(-1))
	if w.cfg.duration != time.Minute {
		t.Fatal("negative duration should be ignored")
	}
}

func TestNew_WithInterval(t *testing.T) {
	w := New(WithInterval(500 * time.Millisecond))
	if w.cfg.interval != 500*time.Millisecond {
		t.Fatalf("expected 500ms, got %v", w.cfg.interval)
	}
}

func TestNew_Interval_DefaultCalculation(t *testing.T) {
	w := New(WithDuration(10 * time.Second))
	if w.cfg.interval != 100*time.Millisecond {
		t.Fatalf("expected 100ms (10s/100), got %v", w.cfg.interval)
	}
}

func TestNew_Interval_ClampMin(t *testing.T) {
	w := New(WithInterval(1 * time.Millisecond))
	if w.cfg.interval != 10*time.Millisecond {
		t.Fatalf("expected 10ms (min), got %v", w.cfg.interval)
	}
}

func TestNew_Interval_ClampMax(t *testing.T) {
	w := New(WithInterval(5 * time.Second))
	if w.cfg.interval != 1*time.Second {
		t.Fatalf("expected 1s (max), got %v", w.cfg.interval)
	}
}

func TestNew_WithStepCount(t *testing.T) {
	w := New(WithStepCount(5))
	if w.cfg.stepCount != 5 {
		t.Fatal("expected 5")
	}
}

func TestNew_WithStepCount_Invalid(t *testing.T) {
	w := New(WithStepCount(0))
	if w.cfg.stepCount != 10 {
		t.Fatal("zero stepCount should be ignored")
	}
}

func TestNew_WithExpFactor(t *testing.T) {
	w := New(WithExpFactor(5.0))
	if w.cfg.expFactor != 5.0 {
		t.Fatal("expected 5.0")
	}
}

func TestNew_WithExpFactor_Invalid(t *testing.T) {
	w := New(WithExpFactor(-1))
	if w.cfg.expFactor != 3.0 {
		t.Fatal("negative expFactor should be ignored")
	}
}

func TestNew_WithCallbacks(t *testing.T) {
	called := false
	w := New(
		WithOnCapacityChange(func(_, _ float64) {}),
		WithOnComplete(func() { called = true }),
	)
	if w.cfg.onCapChange == nil {
		t.Fatal("onCapChange should be set")
	}
	w.cfg.onComplete()
	if !called {
		t.Fatal("onComplete should be callable")
	}
}

// ============================================================
// Start / Stop lifecycle
// ============================================================

func TestStart_Stop(t *testing.T) {
	w := New(WithDuration(1 * time.Second))
	w.Start()
	if !w.IsWarming() {
		t.Fatal("expected warming after Start")
	}
	w.Stop()
	if w.IsWarming() {
		t.Fatal("expected not warming after Stop")
	}
}

func TestStartAt(t *testing.T) {
	w := New(WithDuration(1 * time.Second))
	w.StartAt(0.5)
	if w.Capacity() != 0.5 {
		t.Fatalf("expected 0.5, got %v", w.Capacity())
	}
	w.Stop()
}

func TestReset(t *testing.T) {
	w := New(WithMinCapacity(0.2), WithDuration(1*time.Second))
	w.Start()
	time.Sleep(15 * time.Millisecond)
	w.Reset()
	if w.Capacity() != 0.2 {
		t.Fatalf("after reset, expected minCap 0.2, got %v", w.Capacity())
	}
	w.Stop()
}

func TestStartAt_MultipleRestarts(t *testing.T) {
	w := New(WithDuration(1 * time.Second))
	for i := 0; i < 10; i++ {
		w.StartAt(float64(i) / 10.0)
		time.Sleep(5 * time.Millisecond)
	}
	w.Stop()
}

// ============================================================
// Completion
// ============================================================

func TestCompletion(t *testing.T) {
	w := New(
		WithDuration(50*time.Millisecond),
		WithMinCapacity(0.1),
		WithMaxCapacity(1.0),
	)
	w.Start()
	defer w.Stop()
	time.Sleep(120 * time.Millisecond)

	if !w.IsComplete() {
		t.Fatal("expected complete")
	}
	if w.Capacity() != 1.0 {
		t.Fatalf("expected 1.0, got %v", w.Capacity())
	}
	if w.Progress() != 1.0 {
		t.Fatalf("expected progress 1.0, got %v", w.Progress())
	}
}

func TestCompletion_Callbacks(t *testing.T) {
	var mu sync.Mutex
	var changes []float64
	completeCalled := false

	w := New(
		WithDuration(50*time.Millisecond),
		WithOnCapacityChange(func(_, newCap float64) {
			mu.Lock()
			changes = append(changes, newCap)
			mu.Unlock()
		}),
		WithOnComplete(func() {
			mu.Lock()
			completeCalled = true
			mu.Unlock()
		}),
	)
	w.Start()
	time.Sleep(120 * time.Millisecond)
	w.Stop()

	mu.Lock()
	defer mu.Unlock()
	if !completeCalled {
		t.Fatal("onComplete not called")
	}
	if len(changes) == 0 {
		t.Fatal("onCapChange not called")
	}
}

// ============================================================
// Progress
// ============================================================

func TestProgress_BeforeStart(t *testing.T) {
	w := New()
	if w.Progress() != 0.0 {
		t.Fatalf("expected 0 before start, got %v", w.Progress())
	}
}

func TestProgress_DuringWarmup(t *testing.T) {
	w := New(WithDuration(100 * time.Millisecond))
	w.Start()
	time.Sleep(50 * time.Millisecond)
	p := w.Progress()
	if p <= 0 || p > 1.0 {
		t.Fatalf("progress during warmup should be in (0, 1], got %v", p)
	}
	w.Stop()
}

// ============================================================
// Strategies
// ============================================================

func TestAllStrategies_ReachMaxCapacity(t *testing.T) {
	for _, s := range []Strategy{Linear, Exponential, Logarithmic, Step} {
		t.Run(s.String(), func(t *testing.T) {
			w := New(
				WithStrategy(s),
				WithDuration(50*time.Millisecond),
				WithStepCount(5),
				WithExpFactor(2.0),
			)
			w.Start()
			time.Sleep(120 * time.Millisecond)
			if w.Capacity() != 1.0 {
				t.Fatalf("strategy %s: final capacity = %v, want 1.0", s, w.Capacity())
			}
			w.Stop()
		})
	}
}

func TestCalculate_Linear(t *testing.T) {
	w := New(WithMinCapacity(0.0), WithMaxCapacity(1.0))
	if got := w.calculate(0.5); math.Abs(got-0.5) > 0.001 {
		t.Fatalf("linear(0.5) = %v, want ~0.5", got)
	}
}

func TestCalculate_Exponential(t *testing.T) {
	w := New(WithStrategy(Exponential), WithMinCapacity(0.0), WithMaxCapacity(1.0))
	got := w.calculate(1.0)
	if got < 0.99 {
		t.Fatalf("exponential(1.0) = %v, want >= 0.99", got)
	}
}

func TestCalculate_Logarithmic(t *testing.T) {
	w := New(WithStrategy(Logarithmic), WithMinCapacity(0.0), WithMaxCapacity(1.0))
	got := w.calculate(0.5)
	if got <= 0.5 {
		t.Fatalf("logarithmic(0.5) = %v, want > 0.5 (fast start)", got)
	}
}

func TestCalculate_Step(t *testing.T) {
	w := New(WithStrategy(Step), WithMinCapacity(0.0), WithMaxCapacity(1.0), WithStepCount(4))
	if got := w.calculate(0.0); got != 0.0 {
		t.Fatalf("step(0.0) = %v, want 0.0", got)
	}
	if got := w.calculate(0.3); math.Abs(got-0.25) > 0.001 {
		t.Fatalf("step(0.3) = %v, want ~0.25 (1 step of 4)", got)
	}
}

func TestCalculate_DefaultFallback(t *testing.T) {
	w := New()
	w.cfg.strategy = Strategy(99)
	got := w.calculate(0.5)
	expected := w.cfg.minCap + (w.cfg.maxCap-w.cfg.minCap)*0.5
	if math.Abs(got-expected) > 0.001 {
		t.Fatalf("default fallback(0.5) = %v, want ~%v", got, expected)
	}
}

func TestCalculate_ClampToMax(t *testing.T) {
	w := New(WithStrategy(Exponential), WithMinCapacity(0.0), WithMaxCapacity(0.5), WithExpFactor(10))
	got := w.calculate(1.0)
	if got > 0.5 {
		t.Fatalf("should clamp to 0.5, got %v", got)
	}
}

// ============================================================
// Allow
// ============================================================

func TestAllow_FullCapacity(t *testing.T) {
	w := New(WithMinCapacity(1.0), WithMaxCapacity(1.0))
	w.Start()
	defer w.Stop()

	for i := 0; i < 100; i++ {
		if !w.Allow() {
			t.Fatal("100% capacity should always allow")
		}
	}
}

func TestAllow_ZeroCapacity(t *testing.T) {
	w := New(WithMinCapacity(0.0), WithMaxCapacity(0.0))
	w.Start()
	defer w.Stop()

	allowed := 0
	for i := 0; i < 100; i++ {
		if w.Allow() {
			allowed++
		}
	}
	if allowed > 0 {
		t.Fatalf("0%% capacity should never allow, got %d", allowed)
	}
}

func TestAllow_Probabilistic(t *testing.T) {
	w := New(WithMinCapacity(0.5), WithMaxCapacity(0.5))
	w.Start()
	defer w.Stop()

	allowed := 0
	total := 2000
	for i := 0; i < total; i++ {
		if w.Allow() {
			allowed++
		}
	}
	rate := float64(allowed) / float64(total)
	if rate < 0.35 || rate > 0.65 {
		t.Fatalf("50%% capacity: allow rate = %v, expected ~0.5", rate)
	}

	st := w.Stats()
	if st.Allowed+st.Rejected != int64(total) {
		t.Fatal("allowed + rejected should equal total")
	}
}

// ============================================================
// AllowOrError
// ============================================================

func TestAllowOrError_Allowed(t *testing.T) {
	w := New(WithMinCapacity(1.0), WithMaxCapacity(1.0))
	w.Start()
	defer w.Stop()

	if err := w.AllowOrError(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestAllowOrError_Rejected(t *testing.T) {
	w := New(WithMinCapacity(0.001), WithMaxCapacity(0.001))
	w.Start()
	defer w.Stop()

	var rejected int
	for i := 0; i < 100; i++ {
		err := w.AllowOrError()
		if err != nil {
			rejected++
			var xe *errx.Error
			if !errors.As(err, &xe) {
				t.Fatalf("expected *errx.Error, got %T", err)
			}
			if xe.Domain != DomainWarmup {
				t.Fatalf("expected domain %s, got %s", DomainWarmup, xe.Domain)
			}
			if xe.Code != CodeRejected {
				t.Fatalf("expected code %s, got %s", CodeRejected, xe.Code)
			}
			if !xe.Retryable() {
				t.Fatal("expected retryable")
			}
		}
	}
	if rejected < 90 {
		t.Fatalf("expected >= 90 rejections, got %d", rejected)
	}
}

// ============================================================
// MaxRequests
// ============================================================

func TestMaxRequests(t *testing.T) {
	w := New(WithMinCapacity(0.5), WithMaxCapacity(0.5))
	w.Start()
	defer w.Stop()

	got := w.MaxRequests(100)
	if got != 50 {
		t.Fatalf("MaxRequests(100) at 50%% = %d, want 50", got)
	}
}

func TestMaxRequests_RoundsUp(t *testing.T) {
	w := New(WithMinCapacity(0.33), WithMaxCapacity(0.33))
	got := w.MaxRequests(10)
	if got != 4 {
		t.Fatalf("MaxRequests(10) at 33%% = %d, want 4 (ceil)", got)
	}
}

// ============================================================
// WaitForCompletion
// ============================================================

func TestWaitForCompletion_Success(t *testing.T) {
	w := New(WithDuration(50 * time.Millisecond))
	w.Start()
	defer w.Stop()

	err := w.WaitForCompletion(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if !w.IsComplete() {
		t.Fatal("expected complete")
	}
}

func TestWaitForCompletion_ContextCancelled(t *testing.T) {
	w := New(WithDuration(10 * time.Second))
	w.Start()
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := w.WaitForCompletion(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestWaitForCompletion_AlreadyComplete(t *testing.T) {
	w := New(WithDuration(10 * time.Millisecond))
	w.Start()
	time.Sleep(50 * time.Millisecond)

	err := w.WaitForCompletion(context.Background())
	if err != nil {
		t.Fatalf("expected nil for already-complete, got %v", err)
	}
	w.Stop()
}

// ============================================================
// Stats / ResetStats
// ============================================================

func TestStats(t *testing.T) {
	w := New(
		WithStrategy(Exponential),
		WithMinCapacity(0.2),
		WithMaxCapacity(0.9),
		WithDuration(1*time.Second),
	)
	w.Start()
	time.Sleep(15 * time.Millisecond)
	st := w.Stats()

	if st.Strategy != "exponential" {
		t.Fatalf("expected exponential, got %s", st.Strategy)
	}
	if st.MinCapacity != 0.2 {
		t.Fatal("bad minCap")
	}
	if st.MaxCapacity != 0.9 {
		t.Fatal("bad maxCap")
	}
	if !st.IsWarming {
		t.Fatal("expected warming")
	}
	if st.Duration != time.Second {
		t.Fatal("bad duration")
	}
	if st.Elapsed <= 0 {
		t.Fatal("elapsed should be > 0")
	}
	w.Stop()
}

func TestStats_BeforeStart(t *testing.T) {
	w := New()
	st := w.Stats()
	if st.Elapsed != 0 || st.Remaining != 0 {
		t.Fatal("elapsed/remaining should be 0 before start")
	}
}

func TestResetStats(t *testing.T) {
	w := New(WithMinCapacity(0.5), WithMaxCapacity(0.5))
	w.Start()
	w.Allow()
	w.Allow()
	w.ResetStats()
	st := w.Stats()
	if st.Allowed != 0 || st.Rejected != 0 {
		t.Fatalf("expected zeros after ResetStats, got %+v", st)
	}
	w.Stop()
}

// ============================================================
// Concurrent safety
// ============================================================

func TestConcurrent_ReadOps(t *testing.T) {
	w := New(WithMinCapacity(0.5), WithDuration(100*time.Millisecond))
	w.Start()
	defer w.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Allow()
			w.Capacity()
			w.Progress()
			w.IsWarming()
			w.IsComplete()
			w.Stats()
			w.MaxRequests(100)
		}()
	}
	wg.Wait()
}

func TestConcurrent_StartStopReset(t *testing.T) {
	w := New(WithDuration(1 * time.Second))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); w.Start() }()
		go func() { defer wg.Done(); w.Stop() }()
		go func() { defer wg.Done(); w.Reset() }()
	}
	wg.Wait()
	w.Stop()
}

// ============================================================
// Error constructors
// ============================================================

func TestErrRejected(t *testing.T) {
	e := errRejected(0.35, 0.42)
	if e.Domain != DomainWarmup || e.Code != CodeRejected {
		t.Fatalf("expected WARMUP/REJECTED, got %s/%s", e.Domain, e.Code)
	}
	if !e.Retryable() {
		t.Fatal("expected retryable")
	}
	if e.Severity != errx.SeverityWarn {
		t.Fatal("expected SeverityWarn")
	}
}

func TestDomainConstant(t *testing.T) {
	if DomainWarmup != "WARMUP" {
		t.Fatalf("expected WARMUP, got %s", DomainWarmup)
	}
}

func TestCodeConstant(t *testing.T) {
	if CodeRejected != "REJECTED" {
		t.Fatalf("expected REJECTED, got %s", CodeRejected)
	}
}

// ============================================================
// tick: early return when not warming
// ============================================================

func TestTick_NotWarming(t *testing.T) {
	w := New()
	w.tick()
	if w.capacity != 0.1 {
		t.Fatal("tick on stopped warmer should be a no-op")
	}
}

func TestTick_AlreadyComplete(t *testing.T) {
	w := New()
	w.warming = true
	w.complete = true
	w.tick()
}

func TestMaxRequests_NegativeBase(t *testing.T) {
	w := New()
	w.Start()
	defer w.Stop()
	time.Sleep(20 * time.Millisecond)

	if got := w.MaxRequests(-10); got != 0 {
		t.Fatalf("expected 0 for negative base, got %d", got)
	}
	if got := w.MaxRequests(0); got != 0 {
		t.Fatalf("expected 0 for zero base, got %d", got)
	}
}
