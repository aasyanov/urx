package fallx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// ============================================================
// Static strategy
// ============================================================

func TestStatic_PrimarySuccess(t *testing.T) {
	fb := New[string](WithStatic[string]("fallback"))
	val, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "primary", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if val != "primary" {
		t.Fatalf("expected primary, got %s", val)
	}
}

func TestStatic_PrimaryFails(t *testing.T) {
	fb := New[string](WithStatic[string]("fallback"))
	val, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("boom")
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if val != "fallback" {
		t.Fatalf("expected fallback, got %s", val)
	}
}

func TestStatic_ZeroValue(t *testing.T) {
	fb := New[int](WithStatic[int](42))
	val, err := fb.Do(context.Background(), func(ctx context.Context) (int, error) {
		return 0, errors.New("fail")
	})
	if err != nil {
		t.Fatal(err)
	}
	if val != 42 {
		t.Fatalf("expected 42, got %d", val)
	}
}

// ============================================================
// Func strategy
// ============================================================

func TestFunc_Success(t *testing.T) {
	fb := New[string](WithFunc(func(ctx context.Context, err error) (string, error) {
		return "recovered from: " + err.Error(), nil
	}))
	val, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("db down")
	})
	if err != nil {
		t.Fatal(err)
	}
	if val != "recovered from: db down" {
		t.Fatalf("unexpected value: %s", val)
	}
}

func TestFunc_FallbackFails(t *testing.T) {
	fb := New[string](WithFunc(func(ctx context.Context, err error) (string, error) {
		return "", errors.New("fallback also broken")
	}))
	_, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("primary fail")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeFuncFailed {
		t.Fatalf("expected code %s, got %s", CodeFuncFailed, xe.Code)
	}
}

func TestFunc_NilFunction_FallsBackToStatic(t *testing.T) {
	fb := New[string](WithFunc[string](nil))
	val, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	if err != nil {
		t.Fatalf("nil fn should be ignored (default static): %v", err)
	}
	if val != "" {
		t.Fatalf("expected zero value, got %s", val)
	}
}

func TestFunc_NoFuncConfigured(t *testing.T) {
	fb := &Fallback[string]{cfg: config[string]{strategy: StrategyFunc}}
	fb.shards = make([]*cacheShard[string], 1)
	fb.shards[0] = &cacheShard[string]{entries: make(map[string]*cacheEntry[string]), lru: make(lruHeap[string], 0)}

	_, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeNoFunc {
		t.Fatalf("expected code %s, got %s", CodeNoFunc, xe.Code)
	}
}

// ============================================================
// Cached strategy
// ============================================================

func TestCached_StoresAndReplays(t *testing.T) {
	fb := New[string](WithCached[string](time.Minute, 100))
	defer fb.Close()

	callCount := 0
	fn := func(ctx context.Context) (string, error) {
		callCount++
		if callCount == 1 {
			return "data-v1", nil
		}
		return "", errors.New("down")
	}

	val, err := fb.Do(context.Background(), fn)
	if err != nil || val != "data-v1" {
		t.Fatalf("first call: val=%s err=%v", val, err)
	}

	val, err = fb.Do(context.Background(), fn)
	if err != nil {
		t.Fatalf("second call should use cache: %v", err)
	}
	if val != "data-v1" {
		t.Fatalf("expected data-v1, got %s", val)
	}
}

func TestCached_NoCachedResult(t *testing.T) {
	fb := New[string](WithCached[string](time.Minute, 100))
	defer fb.Close()

	_, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeNoCached {
		t.Fatalf("expected code %s, got %s", CodeNoCached, xe.Code)
	}
}

func TestCached_TTLExpiration(t *testing.T) {
	fb := New[string](WithCached[string](20*time.Millisecond, 100))
	defer fb.Close()

	fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "fresh", nil
	})
	time.Sleep(30 * time.Millisecond)

	_, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error after TTL expiry")
	}
}

func TestCached_KeyFunc(t *testing.T) {
	type ctxKey struct{}
	fb := New[string](
		WithCached[string](time.Minute, 100),
		WithKeyFunc[string](func(ctx context.Context) string {
			if v, ok := ctx.Value(ctxKey{}).(string); ok {
				return v
			}
			return "default"
		}),
	)
	defer fb.Close()

	ctx1 := context.WithValue(context.Background(), ctxKey{}, "user-1")
	ctx2 := context.WithValue(context.Background(), ctxKey{}, "user-2")

	fb.Do(ctx1, func(ctx context.Context) (string, error) { return "data-for-1", nil })
	fb.Do(ctx2, func(ctx context.Context) (string, error) { return "data-for-2", nil })

	val, err := fb.Do(ctx1, func(ctx context.Context) (string, error) { return "", errors.New("fail") })
	if err != nil || val != "data-for-1" {
		t.Fatalf("user-1: val=%s err=%v", val, err)
	}

	val, err = fb.Do(ctx2, func(ctx context.Context) (string, error) { return "", errors.New("fail") })
	if err != nil || val != "data-for-2" {
		t.Fatalf("user-2: val=%s err=%v", val, err)
	}
}

func TestCached_DoWithKey(t *testing.T) {
	fb := New[string](WithCached[string](time.Minute, 100))
	defer fb.Close()

	fb.DoWithKey(context.Background(), "k1", func(ctx context.Context) (string, error) {
		return "val1", nil
	})

	val, err := fb.DoWithKey(context.Background(), "k1", func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	if err != nil || val != "val1" {
		t.Fatalf("expected val1, got %s, err=%v", val, err)
	}
}

func TestCached_MaxSize_Eviction(t *testing.T) {
	fb := New[int](
		WithCached[int](time.Minute, 5),
		WithShards[int](1),
	)
	defer fb.Close()

	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("k%d", i)
		fb.DoWithKey(context.Background(), key, func(ctx context.Context) (int, error) {
			return i, nil
		})
	}

	if fb.cacheSize.Load() > 5 {
		t.Fatalf("cache should be bounded to 5, got %d", fb.cacheSize.Load())
	}
}

func TestCached_Seed(t *testing.T) {
	fb := New[string](WithCached[string](time.Minute, 100))
	defer fb.Close()

	fb.Seed("warm", "pre-loaded")

	val, err := fb.DoWithKey(context.Background(), "warm", func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	if err != nil || val != "pre-loaded" {
		t.Fatalf("expected pre-loaded, got %s, err=%v", val, err)
	}
}

func TestCached_SeedWithTTL(t *testing.T) {
	fb := New[string](WithCached[string](time.Hour, 100))
	defer fb.Close()

	fb.SeedWithTTL("short", "expires-soon", 20*time.Millisecond)
	time.Sleep(30 * time.Millisecond)

	_, err := fb.DoWithKey(context.Background(), "short", func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error after seed TTL")
	}
}

func TestCached_ClearCache(t *testing.T) {
	fb := New[string](WithCached[string](time.Minute, 100))
	defer fb.Close()

	fb.Seed("k1", "v1")
	fb.ClearCache()

	_, err := fb.DoWithKey(context.Background(), "k1", func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error after cache clear")
	}
	if fb.cacheSize.Load() != 0 {
		t.Fatalf("expected 0 cache size, got %d", fb.cacheSize.Load())
	}
}

func TestCached_UpdateExistingKey(t *testing.T) {
	fb := New[string](WithCached[string](time.Minute, 100))
	defer fb.Close()

	fb.Do(context.Background(), func(ctx context.Context) (string, error) { return "v1", nil })
	fb.Do(context.Background(), func(ctx context.Context) (string, error) { return "v2", nil })

	val, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	if err != nil || val != "v2" {
		t.Fatalf("expected v2, got %s, err=%v", val, err)
	}

	if fb.cacheSize.Load() != 1 {
		t.Fatalf("should have 1 entry, got %d", fb.cacheSize.Load())
	}
}

// ============================================================
// Panic recovery
// ============================================================

func TestDo_PrimaryPanic(t *testing.T) {
	fb := New[string](WithStatic[string]("safe"))
	val, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		panic("boom")
	})
	if err != nil {
		t.Fatalf("static fallback should succeed: %v", err)
	}
	if val != "safe" {
		t.Fatalf("expected safe, got %s", val)
	}
}

func TestFunc_FallbackPanic(t *testing.T) {
	fb := New[string](WithFunc(func(ctx context.Context, err error) (string, error) {
		panic("fallback boom")
	}))
	_, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error from fallback panic")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
}

// ============================================================
// OnFallback callback
// ============================================================

func TestOnFallback_Called(t *testing.T) {
	var called bool
	var gotStrategy Strategy
	fb := New[string](
		WithStatic[string]("x"),
		WithOnFallback[string](func(err error, s Strategy) {
			called = true
			gotStrategy = s
		}),
	)
	fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	if !called {
		t.Fatal("OnFallback not called")
	}
	if gotStrategy != StrategyStatic {
		t.Fatalf("expected StrategyStatic, got %v", gotStrategy)
	}
}

func TestOnFallback_NotCalledOnSuccess(t *testing.T) {
	called := false
	fb := New[string](
		WithStatic[string]("x"),
		WithOnFallback[string](func(err error, s Strategy) { called = true }),
	)
	fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "ok", nil
	})
	if called {
		t.Fatal("OnFallback should not be called on success")
	}
}

// ============================================================
// Stats
// ============================================================

func TestStats(t *testing.T) {
	fb := New[string](WithStatic[string]("x"))

	fb.Do(context.Background(), func(ctx context.Context) (string, error) { return "ok", nil })
	fb.Do(context.Background(), func(ctx context.Context) (string, error) { return "", errors.New("fail") })
	fb.Do(context.Background(), func(ctx context.Context) (string, error) { return "", errors.New("fail") })

	s := fb.Stats()
	if s.TotalCalls != 3 {
		t.Fatalf("expected 3 total, got %d", s.TotalCalls)
	}
	if s.PrimarySuccess != 1 {
		t.Fatalf("expected 1 primary success, got %d", s.PrimarySuccess)
	}
	if s.FallbackUsed != 2 {
		t.Fatalf("expected 2 fallback used, got %d", s.FallbackUsed)
	}
	if s.FallbackSuccess != 2 {
		t.Fatalf("expected 2 fallback success, got %d", s.FallbackSuccess)
	}
}

func TestResetStats(t *testing.T) {
	fb := New[string](WithStatic[string]("x"))
	fb.Do(context.Background(), func(ctx context.Context) (string, error) { return "", errors.New("fail") })
	fb.ResetStats()
	s := fb.Stats()
	if s.TotalCalls != 0 || s.FallbackUsed != 0 {
		t.Fatalf("expected zeroes after reset: %+v", s)
	}
}

func TestCachedStats(t *testing.T) {
	fb := New[string](WithCached[string](time.Minute, 100))
	defer fb.Close()

	fb.Do(context.Background(), func(ctx context.Context) (string, error) { return "ok", nil })
	fb.Do(context.Background(), func(ctx context.Context) (string, error) { return "", errors.New("fail") })

	s := fb.Stats()
	if s.CacheHits != 1 {
		t.Fatalf("expected 1 cache hit, got %d", s.CacheHits)
	}
	if s.CacheSize != 1 {
		t.Fatalf("expected 1 cache size, got %d", s.CacheSize)
	}
}

// ============================================================
// Close / lifecycle
// ============================================================

func TestClose_Idempotent(t *testing.T) {
	fb := New[string](WithCached[string](time.Minute, 100))
	fb.Close()
	fb.Close()
}

func TestClose_RejectsCalls(t *testing.T) {
	fb := New[string](WithStatic[string]("x"))
	fb.Close()

	_, err := fb.Do(context.Background(), func(ctx context.Context) (string, error) {
		return "ok", nil
	})
	if err == nil {
		t.Fatal("expected error after close")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeClosed {
		t.Fatalf("expected code %s, got %s", CodeClosed, xe.Code)
	}
}

func TestClose_NonCached(t *testing.T) {
	fb := New[string](WithStatic[string]("x"))
	fb.Close()
}

// ============================================================
// Strategy stringer
// ============================================================

func TestStrategy_String(t *testing.T) {
	tests := []struct {
		s    Strategy
		want string
	}{
		{StrategyStatic, "static"},
		{StrategyFunc, "function"},
		{StrategyCached, "cached"},
		{Strategy(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Fatalf("Strategy(%d).String() = %s, want %s", tt.s, got, tt.want)
		}
	}
}

// ============================================================
// Error constants
// ============================================================

func TestErrorConstants(t *testing.T) {
	if DomainFallback != "FALLBACK" {
		t.Fatal("unexpected domain")
	}
	if CodeNoFunc != "NO_FUNC" {
		t.Fatal("unexpected code")
	}
}

// ============================================================
// Concurrency
// ============================================================

func TestConcurrency_Cached(t *testing.T) {
	fb := New[int](WithCached[int](time.Minute, 1000))
	defer fb.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", n%10)
			fb.DoWithKey(context.Background(), key, func(ctx context.Context) (int, error) {
				if n%3 == 0 {
					return 0, errors.New("fail")
				}
				return n, nil
			})
		}(i)
	}
	wg.Wait()

	s := fb.Stats()
	if s.TotalCalls != 100 {
		t.Fatalf("expected 100 total, got %d", s.TotalCalls)
	}
}

func TestConcurrency_Static(t *testing.T) {
	fb := New[string](WithStatic[string]("safe"))

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fb.Do(context.Background(), func(ctx context.Context) (string, error) {
				return "", errors.New("fail")
			})
		}()
	}
	wg.Wait()

	s := fb.Stats()
	if s.FallbackSuccess != 100 {
		t.Fatalf("expected 100 fallback successes, got %d", s.FallbackSuccess)
	}
}

// ============================================================
// WithShards option
// ============================================================

func TestWithShards(t *testing.T) {
	fb := New[string](
		WithCached[string](time.Minute, 100),
		WithShards[string](4),
	)
	defer fb.Close()

	if len(fb.shards) != 4 {
		t.Fatalf("expected 4 shards, got %d", len(fb.shards))
	}
}

func TestWithShards_InvalidClamped(t *testing.T) {
	fb := New[string](
		WithCached[string](time.Minute, 100),
		WithShards[string](0),
	)
	defer fb.Close()

	if len(fb.shards) != 16 {
		t.Fatalf("expected default 16 shards, got %d", len(fb.shards))
	}
}

// ============================================================
// Benchmark
// ============================================================

func BenchmarkDo_Static(b *testing.B) {
	fb := New[string](WithStatic[string]("fallback"))
	ctx := context.Background()
	fail := errors.New("fail")
	b.ReportAllocs()
	for b.Loop() {
		fb.Do(ctx, func(ctx context.Context) (string, error) {
			return "", fail
		})
	}
}

func BenchmarkDo_PrimarySuccess(b *testing.B) {
	fb := New[string](WithCached[string](time.Minute, 1000))
	defer fb.Close()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		fb.Do(ctx, func(ctx context.Context) (string, error) {
			return "ok", nil
		})
	}
}

func BenchmarkDo_CachedFallback(b *testing.B) {
	fb := New[string](WithCached[string](time.Minute, 1000))
	defer fb.Close()
	ctx := context.Background()
	fb.Seed("default", "cached-value")
	fail := errors.New("fail")
	b.ReportAllocs()
	for b.Loop() {
		fb.Do(ctx, func(ctx context.Context) (string, error) {
			return "", fail
		})
	}
}
