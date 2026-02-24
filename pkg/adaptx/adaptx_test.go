package adaptx

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
// Algorithm.String
// ============================================================

func TestAlgorithm_String(t *testing.T) {
	tests := []struct {
		a    Algorithm
		want string
	}{
		{AIMD, "AIMD"},
		{Vegas, "Vegas"},
		{Gradient, "Gradient"},
		{Algorithm(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.a.String(); got != tt.want {
			t.Errorf("Algorithm(%d).String() = %q, want %q", tt.a, got, tt.want)
		}
	}
}

// ============================================================
// New — options
// ============================================================

func TestNew_Defaults(t *testing.T) {
	l := New()
	if l.limit != 10 {
		t.Fatalf("expected initial limit 10, got %d", l.limit)
	}
	if l.cfg.algorithm != AIMD {
		t.Fatal("expected AIMD")
	}
}

func TestNew_WithAlgorithm(t *testing.T) {
	l := New(WithAlgorithm(Vegas))
	if l.cfg.algorithm != Vegas {
		t.Fatal("expected Vegas")
	}
}

func TestNew_WithInitialLimit(t *testing.T) {
	l := New(WithInitialLimit(20))
	if l.limit != 20 {
		t.Fatal("expected 20")
	}
}

func TestNew_WithInitialLimit_Invalid(t *testing.T) {
	l := New(WithInitialLimit(0))
	if l.limit != 10 {
		t.Fatal("zero should be ignored")
	}
}

func TestNew_WithMinLimit(t *testing.T) {
	l := New(WithMinLimit(5))
	if l.cfg.minLimit != 5 {
		t.Fatal("expected 5")
	}
}

func TestNew_WithMaxLimit(t *testing.T) {
	l := New(WithMaxLimit(500))
	if l.cfg.maxLimit != 500 {
		t.Fatal("expected 500")
	}
}

func TestNew_LimitsClamping(t *testing.T) {
	l := New(WithMinLimit(50), WithMaxLimit(30))
	if l.cfg.maxLimit < l.cfg.minLimit {
		t.Fatal("maxLimit should be clamped to minLimit")
	}
}

func TestNew_InitialLimitClamped(t *testing.T) {
	l := New(WithInitialLimit(5000), WithMaxLimit(100))
	if l.limit > l.cfg.maxLimit {
		t.Fatal("initialLimit should be clamped to maxLimit")
	}
}

func TestNew_WithSmoothing(t *testing.T) {
	l := New(WithSmoothing(0.5))
	if l.cfg.smoothing != 0.5 {
		t.Fatal("expected 0.5")
	}
}

func TestNew_WithSmoothing_Invalid(t *testing.T) {
	l := New(WithSmoothing(0))
	if l.cfg.smoothing != 0.2 {
		t.Fatal("zero should be ignored")
	}
	l2 := New(WithSmoothing(1.5))
	if l2.cfg.smoothing != 0.2 {
		t.Fatal(">1 should be ignored")
	}
}

func TestNew_WithIncreaseRate(t *testing.T) {
	l := New(WithIncreaseRate(2.0))
	if l.cfg.increaseRate != 2.0 {
		t.Fatal("expected 2.0")
	}
}

func TestNew_WithDecreaseRatio(t *testing.T) {
	l := New(WithDecreaseRatio(0.7))
	if l.cfg.decreaseRatio != 0.7 {
		t.Fatal("expected 0.7")
	}
}

func TestNew_WithDecreaseRatio_Invalid(t *testing.T) {
	l := New(WithDecreaseRatio(0))
	if l.cfg.decreaseRatio != 0.5 {
		t.Fatal("zero should be ignored")
	}
	l2 := New(WithDecreaseRatio(1.0))
	if l2.cfg.decreaseRatio != 0.5 {
		t.Fatal("1.0 should be ignored")
	}
}

func TestNew_WithTargetLatency(t *testing.T) {
	l := New(WithTargetLatency(50 * time.Millisecond))
	if l.cfg.targetLatency != 50*time.Millisecond {
		t.Fatal("expected 50ms")
	}
}

func TestNew_WithTolerance(t *testing.T) {
	l := New(WithTolerance(0.2))
	if l.cfg.tolerance != 0.2 {
		t.Fatal("expected 0.2")
	}
}

func TestNew_WithSampleWindow(t *testing.T) {
	l := New(WithSampleWindow(2 * time.Second))
	if l.cfg.sampleWindow != 2*time.Second {
		t.Fatal("expected 2s")
	}
}

func TestNew_WithWarmupSamples(t *testing.T) {
	l := New(WithWarmupSamples(0))
	if l.cfg.warmupSamples != 0 {
		t.Fatal("expected 0")
	}
}

func TestNew_WithMinLatencyDecay(t *testing.T) {
	l := New(WithMinLatencyDecay(0.01))
	if l.cfg.minLatDecay != 0.01 {
		t.Fatal("expected 0.01")
	}
}

func TestNew_WithJitter(t *testing.T) {
	l := New(WithJitter(0.2))
	if l.cfg.jitter != 0.2 {
		t.Fatal("expected 0.2")
	}
}

func TestNew_WithOnLimitChange(t *testing.T) {
	called := false
	l := New(WithOnLimitChange(func(_, _ int) { called = true }))
	l.cfg.onLimitChange(1, 2)
	if !called {
		t.Fatal("expected callable")
	}
}

// ============================================================
// Acquire / Release
// ============================================================

func TestAcquire_Release(t *testing.T) {
	l := New(WithInitialLimit(5), WithWarmupSamples(0))
	defer l.Close()

	release, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.InFlight() != 1 {
		t.Fatal("expected inFlight 1")
	}
	release(true, time.Millisecond)
	if l.InFlight() != 0 {
		t.Fatal("expected inFlight 0 after release")
	}
}

func TestAcquire_DoubleRelease(t *testing.T) {
	l := New(WithInitialLimit(5))
	defer l.Close()

	release, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release(true, time.Millisecond)
	release(true, time.Millisecond)
	if l.InFlight() != 0 {
		t.Fatal("double release should be a no-op")
	}
}

func TestAcquire_Closed(t *testing.T) {
	l := New()
	l.Close()

	_, err := l.Acquire(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeClosed {
		t.Fatalf("expected CLOSED, got %v", err)
	}
}

func TestAcquire_ContextCancelled(t *testing.T) {
	l := New(WithInitialLimit(1))
	defer l.Close()

	r1, _ := l.Acquire(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := l.Acquire(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeCancelled {
		t.Fatalf("expected CANCELLED, got %v", err)
	}
	r1(true, time.Millisecond)
}

func TestAcquire_ContextTimeout(t *testing.T) {
	l := New(WithInitialLimit(1))
	defer l.Close()

	r1, _ := l.Acquire(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)
	_, err := l.Acquire(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeTimeout {
		t.Fatalf("expected TIMEOUT, got %v", err)
	}
	r1(true, time.Millisecond)
}

// ============================================================
// TryAcquire
// ============================================================

func TestTryAcquire_Success(t *testing.T) {
	l := New(WithInitialLimit(5))
	defer l.Close()

	release, ok := l.TryAcquire()
	if !ok {
		t.Fatal("expected ok")
	}
	release(true, time.Millisecond)
}

func TestTryAcquire_Full(t *testing.T) {
	l := New(WithInitialLimit(1), WithMaxLimit(1))
	defer l.Close()

	r1, ok := l.TryAcquire()
	if !ok {
		t.Fatal("expected ok")
	}
	_, ok2 := l.TryAcquire()
	if ok2 {
		t.Fatal("expected false when full")
	}
	r1(true, time.Millisecond)
}

func TestTryAcquire_Closed(t *testing.T) {
	l := New()
	l.Close()

	_, ok := l.TryAcquire()
	if ok {
		t.Fatal("expected false when closed")
	}
}

// ============================================================
// Do
// ============================================================

func TestDo_Success(t *testing.T) {
	l := New(WithInitialLimit(5))
	defer l.Close()

	_, err := Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	st := l.Stats()
	if st.Total != 1 || st.Success != 1 {
		t.Fatalf("expected 1 total 1 success, got %+v", st)
	}
}

func TestDo_Failure(t *testing.T) {
	l := New(WithInitialLimit(5))
	defer l.Close()

	_, err := Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) {
		return struct{}{}, errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st := l.Stats()
	if st.Failures != 1 {
		t.Fatal("expected 1 failure")
	}
}

func TestDo_PanicRecovery(t *testing.T) {
	l := New(WithInitialLimit(5))
	defer l.Close()

	_, err := Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) {
		panic("test panic")
	})
	if err == nil {
		t.Fatal("expected error from panic")
	}
	if l.InFlight() != 0 {
		t.Fatal("slot should be released after panic")
	}
}

// ============================================================
// AdaptController
// ============================================================

func TestAdaptController_Limit(t *testing.T) {
	l := New(WithInitialLimit(15))
	defer l.Close()
	var seen int
	Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) {
		seen = ac.Limit()
		return struct{}{}, nil
	})
	if seen != 15 {
		t.Fatalf("expected limit 15, got %d", seen)
	}
}

func TestAdaptController_InFlight(t *testing.T) {
	l := New(WithInitialLimit(10))
	defer l.Close()
	var seen int
	Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) {
		seen = ac.InFlight()
		return struct{}{}, nil
	})
	if seen < 1 {
		t.Fatalf("expected InFlight >= 1, got %d", seen)
	}
}

func TestAdaptController_Algorithm(t *testing.T) {
	l := New(WithAlgorithm(Vegas))
	defer l.Close()
	var seen Algorithm
	Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) {
		seen = ac.Algorithm()
		return struct{}{}, nil
	})
	if seen != Vegas {
		t.Fatalf("expected Vegas, got %v", seen)
	}
}

func TestAdaptController_SkipSample(t *testing.T) {
	l := New(WithInitialLimit(10), WithWarmupSamples(0), WithJitter(0))
	defer l.Close()

	initial := l.Limit()

	Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) {
		ac.SkipSample()
		time.Sleep(100 * time.Millisecond)
		return struct{}{}, errors.New("cache miss")
	})

	if l.Limit() != initial+1 {
		t.Fatalf("SkipSample should cause neutral feedback (success=true, latency=0), expected limit %d, got %d", initial+1, l.Limit())
	}
}

// ============================================================
// Algorithms
// ============================================================

func TestAIMD_Increase(t *testing.T) {
	l := New(WithAlgorithm(AIMD), WithInitialLimit(10), WithWarmupSamples(0), WithJitter(0))
	defer l.Close()

	initial := l.Limit()
	for i := 0; i < 5; i++ {
		Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) { return struct{}{}, nil })
	}
	if l.Limit() <= initial {
		t.Fatal("AIMD should increase on success")
	}
}

func TestAIMD_Decrease(t *testing.T) {
	l := New(WithAlgorithm(AIMD), WithInitialLimit(20), WithWarmupSamples(0), WithJitter(0))
	defer l.Close()

	Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) { return struct{}{}, errors.New("fail") })
	if l.Limit() >= 20 {
		t.Fatal("AIMD should decrease on failure")
	}
}

func TestVegas(t *testing.T) {
	l := New(WithAlgorithm(Vegas), WithInitialLimit(10), WithWarmupSamples(0), WithJitter(0))
	defer l.Close()

	for i := 0; i < 5; i++ {
		Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) {
			time.Sleep(time.Millisecond)
			return struct{}{}, nil
		})
	}
	if l.Limit() == 0 {
		t.Fatal("Vegas should produce a non-zero limit")
	}
}

func TestGradient_Increase(t *testing.T) {
	l := New(WithAlgorithm(Gradient), WithInitialLimit(10), WithWarmupSamples(0), WithJitter(0))
	defer l.Close()

	initial := l.Limit()
	lat := 10 * time.Millisecond
	for i := 0; i < 20; i++ {
		l.record(true, lat)
	}
	if l.Limit() <= initial {
		t.Fatalf("Gradient should increase on stable latency, got %d <= %d", l.Limit(), initial)
	}
}

func TestGradient_Decrease(t *testing.T) {
	l := New(WithAlgorithm(Gradient), WithInitialLimit(20), WithWarmupSamples(0), WithJitter(0))
	defer l.Close()

	Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) { return struct{}{}, errors.New("fail") })
	if l.Limit() >= 20 {
		t.Fatal("Gradient should decrease on failure")
	}
}

func TestDefaultFallback(t *testing.T) {
	l := New(WithWarmupSamples(0), WithJitter(0))
	l.cfg.algorithm = Algorithm(99)
	defer l.Close()

	Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) { return struct{}{}, nil })
}

// ============================================================
// Vegas edge cases
// ============================================================

func TestVegas_NoBaseline(t *testing.T) {
	l := New(WithAlgorithm(Vegas))
	l.minLat = math.MaxFloat64
	got := l.vegas(time.Millisecond)
	if got != l.limit {
		t.Fatal("should return current limit when no baseline")
	}
}

func TestVegas_ZeroLatency(t *testing.T) {
	l := New(WithAlgorithm(Vegas))
	l.minLat = 1000
	got := l.vegas(0)
	if got != l.limit {
		t.Fatal("should return current limit for zero latency")
	}
}

// ============================================================
// Gradient edge cases
// ============================================================

func TestGradient_NoAvg(t *testing.T) {
	l := New(WithAlgorithm(Gradient))
	l.avgLat = 0
	got := l.gradient(true, time.Millisecond)
	if got != l.limit+1 {
		t.Fatalf("expected limit+1 when no avg, got %d", got)
	}
}

// ============================================================
// Stats / ResetStats
// ============================================================

func TestStats(t *testing.T) {
	l := New(WithAlgorithm(AIMD), WithInitialLimit(10), WithWarmupSamples(0))
	defer l.Close()

	Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) {
		time.Sleep(time.Millisecond)
		return struct{}{}, nil
	})

	st := l.Stats()
	if st.Algorithm != "AIMD" {
		t.Fatal("wrong algorithm")
	}
	if st.Total != 1 {
		t.Fatal("expected 1 total")
	}
	if st.MinLimit != 1 {
		t.Fatal("wrong minLimit")
	}
}

func TestStats_EmptySamples(t *testing.T) {
	l := New()
	st := l.Stats()
	if st.AvgLat != 0 || st.P50Lat != 0 || st.P99Lat != 0 {
		t.Fatal("empty samples should have zero latencies")
	}
}

func TestResetStats(t *testing.T) {
	l := New(WithWarmupSamples(0))
	defer l.Close()

	Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) { return struct{}{}, nil })
	Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) { return struct{}{}, errors.New("x") })

	l.ResetStats()
	st := l.Stats()
	if st.Total != 0 || st.Success != 0 || st.Failures != 0 {
		t.Fatalf("expected zeros, got %+v", st)
	}
}

// ============================================================
// Close
// ============================================================

func TestClose_DoubleClose(t *testing.T) {
	l := New()
	err1 := l.Close()
	if err1 != nil {
		t.Fatal("first close should succeed")
	}
	err2 := l.Close()
	if err2 == nil {
		t.Fatal("second close should return error")
	}
}

// ============================================================
// Concurrent safety
// ============================================================

func TestConcurrent(t *testing.T) {
	l := New(WithInitialLimit(20), WithWarmupSamples(0))
	defer l.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) {
				time.Sleep(time.Millisecond)
				return struct{}{}, nil
			})
			l.Stats()
			l.Limit()
			l.InFlight()
		}()
	}
	wg.Wait()
}

// ============================================================
// Error constructors
// ============================================================

func TestErrLimitExceeded(t *testing.T) {
	e := errLimitExceeded()
	if e.Domain != DomainAdapt || e.Code != CodeLimitExceeded {
		t.Fatal("wrong domain/code")
	}
	if !e.Retryable() {
		t.Fatal("expected retryable")
	}
}

func TestErrTimeout(t *testing.T) {
	e := errTimeout(context.DeadlineExceeded)
	if e.Code != CodeTimeout {
		t.Fatal("wrong code")
	}
}

func TestErrCancelled(t *testing.T) {
	e := errCancelled(context.Canceled)
	if e.Code != CodeCancelled {
		t.Fatal("wrong code")
	}
}

func TestErrClosed(t *testing.T) {
	e := errClosed()
	if e.Code != CodeClosed {
		t.Fatal("wrong code")
	}
}

// ============================================================
// Warmup
// ============================================================

func TestWarmup_NoAdjustment(t *testing.T) {
	l := New(WithInitialLimit(10), WithWarmupSamples(5), WithJitter(0))
	defer l.Close()

	for i := 0; i < 4; i++ {
		Do[struct{}](l, context.Background(), func(ctx context.Context, ac AdaptController) (struct{}, error) { return struct{}{}, nil })
	}
	if l.Limit() != 10 {
		t.Fatal("limit should not change during warmup")
	}
}

// ============================================================
// Benchmark
// ============================================================

func BenchmarkDo(b *testing.B) {
	l := New(WithInitialLimit(100), WithWarmupSamples(0))
	defer l.Close()
	ctx := context.Background()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			Do[struct{}](l, ctx, func(ctx context.Context, ac AdaptController) (struct{}, error) { return struct{}{}, nil })
		}
	})
}

func BenchmarkTryAcquire(b *testing.B) {
	l := New(WithInitialLimit(100), WithWarmupSamples(0))
	defer l.Close()

	b.ReportAllocs()
	for b.Loop() {
		release, ok := l.TryAcquire()
		if ok {
			release(true, time.Microsecond)
		}
	}
}
