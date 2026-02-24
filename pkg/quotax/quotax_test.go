package quotax

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
// Basic Allow
// ============================================================

func TestAllow_NewKey(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10))
	defer l.Close()

	if !l.Allow("user:1") {
		t.Fatal("first call should be allowed")
	}
	if l.KeyCount() != 1 {
		t.Fatalf("expected 1 key, got %d", l.KeyCount())
	}
}

func TestAllow_ExistingKey(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10))
	defer l.Close()

	l.Allow("user:1")
	l.Allow("user:1")

	if l.KeyCount() != 1 {
		t.Fatalf("expected 1 key, got %d", l.KeyCount())
	}
}

func TestAllow_BurstExhausted(t *testing.T) {
	l := New(WithRate(0.1), WithBurst(3))
	defer l.Close()

	for i := 0; i < 3; i++ {
		if !l.Allow("user:1") {
			t.Fatalf("call %d should be allowed", i)
		}
	}
	if l.Allow("user:1") {
		t.Fatal("fourth call should be rejected")
	}
}

func TestAllowN(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10))
	defer l.Close()

	if !l.AllowN("user:1", 5) {
		t.Fatal("AllowN(5) should succeed with burst 10")
	}
	if l.AllowN("user:1", 10) {
		t.Fatal("AllowN(10) should fail with 5 remaining")
	}
}

func TestAllow_MultipleKeys(t *testing.T) {
	l := New(WithRate(0.1), WithBurst(1))
	defer l.Close()

	if !l.Allow("a") {
		t.Fatal("key a should be allowed")
	}
	if l.Allow("a") {
		t.Fatal("key a should be exhausted")
	}
	if !l.Allow("b") {
		t.Fatal("key b should have its own bucket")
	}
}

// ============================================================
// AllowOrError
// ============================================================

func TestAllowOrError_Success(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10))
	defer l.Close()

	if err := l.AllowOrError("user:1"); err != nil {
		t.Fatal(err)
	}
}

func TestAllowOrError_Limited(t *testing.T) {
	l := New(WithRate(0.1), WithBurst(1))
	defer l.Close()

	l.Allow("user:1")
	err := l.AllowOrError("user:1")
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeLimited {
		t.Fatalf("expected code %s, got %s", CodeLimited, xe.Code)
	}
}

// ============================================================
// Wait
// ============================================================

func TestWait_Immediate(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10))
	defer l.Close()

	err := l.Wait(context.Background(), "user:1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestWait_ContextCancelled(t *testing.T) {
	l := New(WithRate(0.1), WithBurst(1))
	defer l.Close()

	l.Allow("user:1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := l.Wait(ctx, "user:1")
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeCancelled {
		t.Fatalf("expected code %s, got %s", CodeCancelled, xe.Code)
	}
}

func TestWaitN_EventuallyAllowed(t *testing.T) {
	l := New(WithRate(1000), WithBurst(2))
	defer l.Close()

	l.AllowN("user:1", 2)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := l.WaitN(ctx, "user:1", 1)
	if err != nil {
		t.Fatalf("should eventually be allowed: %v", err)
	}
}

// ============================================================
// MaxKeys
// ============================================================

func TestMaxKeys_Rejects(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10), WithMaxKeys(2))
	defer l.Close()

	l.Allow("a")
	l.Allow("b")

	if l.Allow("c") {
		t.Fatal("third key should be rejected")
	}
}

func TestMaxKeys_Callback(t *testing.T) {
	var called string
	l := New(
		WithRate(1000), WithBurst(10), WithMaxKeys(1),
		WithOnMaxKeys(func(key string) { called = key }),
	)
	defer l.Close()

	l.Allow("a")
	l.Allow("b")

	if called != "b" {
		t.Fatalf("expected callback for 'b', got '%s'", called)
	}
}

func TestMaxKeys_AllowOrError(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10), WithMaxKeys(1))
	defer l.Close()

	l.Allow("a")
	err := l.AllowOrError("b")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMaxKeys_WaitN(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10), WithMaxKeys(1))
	defer l.Close()

	l.Allow("a")
	err := l.WaitN(context.Background(), "b", 1)
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeMaxKeys {
		t.Fatalf("expected code %s, got %s", CodeMaxKeys, xe.Code)
	}
}

func TestMaxKeys_AfterRemove(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10), WithMaxKeys(1))
	defer l.Close()

	l.Allow("a")
	l.Remove("a")

	if !l.Allow("b") {
		t.Fatal("should allow after removing a key")
	}
}

func TestMaxKeys_AfterEviction(t *testing.T) {
	l := New(
		WithRate(1000), WithBurst(10), WithMaxKeys(1),
		WithEvictionTTL(10*time.Millisecond),
	)
	defer l.Close()

	l.Allow("a")
	time.Sleep(20 * time.Millisecond)
	l.ForceEviction()

	if !l.Allow("b") {
		t.Fatal("should allow after eviction")
	}
}

// ============================================================
// Remove / Exists
// ============================================================

func TestRemove(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10))
	defer l.Close()

	l.Allow("user:1")
	if !l.Remove("user:1") {
		t.Fatal("should return true for existing key")
	}
	if l.KeyCount() != 0 {
		t.Fatalf("expected 0 keys, got %d", l.KeyCount())
	}
}

func TestRemove_NonExistent(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10))
	defer l.Close()

	if l.Remove("ghost") {
		t.Fatal("should return false for non-existent key")
	}
}

func TestExists(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10))
	defer l.Close()

	l.Allow("user:1")

	if !l.Exists("user:1") {
		t.Fatal("should exist")
	}
	if l.Exists("ghost") {
		t.Fatal("should not exist")
	}
}

// ============================================================
// Reset
// ============================================================

func TestReset(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10))
	defer l.Close()

	l.Allow("a")
	l.Allow("b")
	l.Allow("c")
	l.Reset()

	if l.KeyCount() != 0 {
		t.Fatalf("expected 0 after reset, got %d", l.KeyCount())
	}
}

// ============================================================
// Stats
// ============================================================

func TestStats(t *testing.T) {
	l := New(WithRate(0.1), WithBurst(1))
	defer l.Close()

	l.Allow("a")
	l.Allow("a")
	l.Allow("b")

	s := l.Stats()
	if s.Keys != 2 {
		t.Fatalf("expected 2 keys, got %d", s.Keys)
	}
	if s.Allowed != 2 {
		t.Fatalf("expected 2 allowed, got %d", s.Allowed)
	}
	if s.Limited != 1 {
		t.Fatalf("expected 1 limited, got %d", s.Limited)
	}
}

func TestResetStats(t *testing.T) {
	l := New(WithRate(1000), WithBurst(10))
	defer l.Close()

	l.Allow("a")
	l.ResetStats()

	s := l.Stats()
	if s.Allowed != 0 || s.Limited != 0 {
		t.Fatalf("expected zeroes, got %+v", s)
	}
	if s.Keys != 1 {
		t.Fatal("keys should not be affected by ResetStats")
	}
}

// ============================================================
// Eviction
// ============================================================

func TestEviction_StaleKeys(t *testing.T) {
	l := New(
		WithRate(1000), WithBurst(10),
		WithEvictionTTL(10*time.Millisecond),
		WithEvictionInterval(time.Hour),
	)
	defer l.Close()

	l.Allow("stale")
	time.Sleep(20 * time.Millisecond)
	l.ForceEviction()

	if l.Exists("stale") {
		t.Fatal("stale key should be evicted")
	}
	if l.KeyCount() != 0 {
		t.Fatalf("expected 0 keys, got %d", l.KeyCount())
	}
}

func TestEviction_PreservesActive(t *testing.T) {
	l := New(
		WithRate(1000), WithBurst(10),
		WithEvictionTTL(50*time.Millisecond),
		WithEvictionInterval(time.Hour),
	)
	defer l.Close()

	l.Allow("active")
	time.Sleep(10 * time.Millisecond)
	l.Allow("active")
	l.ForceEviction()

	if !l.Exists("active") {
		t.Fatal("active key should not be evicted")
	}
}

// ============================================================
// Close / lifecycle
// ============================================================

func TestClose_Idempotent(t *testing.T) {
	l := New()
	l.Close()
	l.Close()
}

func TestClose_RejectsAllow(t *testing.T) {
	l := New()
	l.Close()

	if l.Allow("a") {
		t.Fatal("should reject after close")
	}
}

func TestClose_RejectsWait(t *testing.T) {
	l := New()
	l.Close()

	err := l.Wait(context.Background(), "a")
	if err == nil {
		t.Fatal("should reject after close")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeClosed {
		t.Fatalf("expected code %s, got %s", CodeClosed, xe.Code)
	}
}

func TestClose_AllowOrError(t *testing.T) {
	l := New()
	l.Close()

	err := l.AllowOrError("a")
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeClosed {
		t.Fatalf("expected code %s, got %s", CodeClosed, xe.Code)
	}
}

// ============================================================
// Options
// ============================================================

func TestOptions_Defaults(t *testing.T) {
	l := New()
	defer l.Close()

	if l.cfg.rate != 10 {
		t.Fatalf("expected rate 10, got %f", l.cfg.rate)
	}
	if l.cfg.burst != 20 {
		t.Fatalf("expected burst 20, got %d", l.cfg.burst)
	}
	if l.cfg.shards != 64 {
		t.Fatalf("expected 64 shards, got %d", l.cfg.shards)
	}
}

func TestOptions_InvalidIgnored(t *testing.T) {
	l := New(WithRate(-1), WithBurst(-1), WithShards(-1))
	defer l.Close()

	if l.cfg.rate != 10 || l.cfg.burst != 20 || l.cfg.shards != 64 {
		t.Fatalf("negative values should be ignored: %+v", l.cfg)
	}
}

// ============================================================
// Error constants
// ============================================================

func TestErrorConstants(t *testing.T) {
	if DomainQuota != "QUOTA" {
		t.Fatal("unexpected domain")
	}
	if CodeLimited != "LIMITED" {
		t.Fatal("unexpected code")
	}
}

// ============================================================
// Concurrency
// ============================================================

func TestConcurrency(t *testing.T) {
	l := New(WithRate(10000), WithBurst(100))
	defer l.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("user:%d", n%10)
			for j := 0; j < 100; j++ {
				l.Allow(key)
			}
		}(i)
	}
	wg.Wait()

	s := l.Stats()
	if s.Keys != 10 {
		t.Fatalf("expected 10 keys, got %d", s.Keys)
	}
	total := s.Allowed + s.Limited
	if total != 10000 {
		t.Fatalf("expected 10000 total, got %d", total)
	}
}

func TestConcurrency_MaxKeys(t *testing.T) {
	l := New(WithRate(10000), WithBurst(100), WithMaxKeys(5))
	defer l.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("user:%d", n)
			l.Allow(key)
		}(i)
	}
	wg.Wait()

	if l.KeyCount() > 5 {
		t.Fatalf("expected max 5 keys, got %d", l.KeyCount())
	}
}

// ============================================================
// Benchmark
// ============================================================

func BenchmarkAllow_ExistingKey(b *testing.B) {
	l := New(WithRate(1000000), WithBurst(1000000))
	defer l.Close()
	l.Allow("hot-key")

	b.ReportAllocs()
	for b.Loop() {
		l.Allow("hot-key")
	}
}

func BenchmarkAllow_NewKeys(b *testing.B) {
	l := New(WithRate(1000000), WithBurst(1000000))
	defer l.Close()

	b.ReportAllocs()
	i := 0
	for b.Loop() {
		l.Allow(fmt.Sprintf("key-%d", i))
		i++
	}
}

func BenchmarkAllow_Parallel(b *testing.B) {
	l := New(WithRate(1000000), WithBurst(1000000))
	defer l.Close()

	for i := 0; i < 100; i++ {
		l.Allow(fmt.Sprintf("warmup-%d", i))
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			l.Allow(fmt.Sprintf("user:%d", i%100))
			i++
		}
	})
}
