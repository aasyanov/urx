package ratex

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- New: defaults ---

func TestNew_Defaults(t *testing.T) {
	rl := New()
	if rl.cfg.rate != 10 {
		t.Fatalf("expected rate=10, got %v", rl.cfg.rate)
	}
	if rl.cfg.burst != 20 {
		t.Fatalf("expected burst=20, got %d", rl.cfg.burst)
	}
}

// --- New: with options ---

func TestNew_WithOptions(t *testing.T) {
	rl := New(WithRate(100), WithBurst(50))
	if rl.cfg.rate != 100 {
		t.Fatalf("expected rate=100, got %v", rl.cfg.rate)
	}
	if rl.cfg.burst != 50 {
		t.Fatalf("expected burst=50, got %d", rl.cfg.burst)
	}
}

// --- New: invalid options ignored ---

func TestNew_InvalidOptions(t *testing.T) {
	rl := New(WithRate(-1), WithBurst(-1))
	if rl.cfg.rate != 10 {
		t.Fatalf("expected rate=10 (negative ignored), got %v", rl.cfg.rate)
	}
	if rl.cfg.burst != 20 {
		t.Fatalf("expected burst=20 (negative ignored), got %d", rl.cfg.burst)
	}
}

// --- Allow: within burst ---

func TestAllow_WithinBurst(t *testing.T) {
	rl := New(WithRate(10), WithBurst(5))
	for i := range 5 {
		if !rl.Allow() {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}
}

// --- Allow: denied after burst ---

func TestAllow_DeniedAfterBurst(t *testing.T) {
	rl := New(WithRate(10), WithBurst(5))
	for range 5 {
		rl.Allow()
	}
	if rl.Allow() {
		t.Fatal("expected denial after burst exhausted")
	}
}

// --- Allow: refills over time ---

func TestAllow_RefillsOverTime(t *testing.T) {
	rl := New(WithRate(100), WithBurst(5))
	for range 5 {
		rl.Allow()
	}
	if rl.Allow() {
		t.Fatal("expected denial immediately after burst")
	}
	time.Sleep(60 * time.Millisecond)
	if !rl.Allow() {
		t.Fatal("expected allow after refill time")
	}
}

// --- AllowN: batch ---

func TestAllowN_Batch(t *testing.T) {
	rl := New(WithRate(10), WithBurst(10))
	if !rl.AllowN(5) {
		t.Fatal("expected 5 to be allowed")
	}
	if !rl.AllowN(5) {
		t.Fatal("expected remaining 5 to be allowed")
	}
	if rl.AllowN(1) {
		t.Fatal("expected denial after burst")
	}
}

// --- AllowN: too many ---

func TestAllowN_TooMany(t *testing.T) {
	rl := New(WithRate(10), WithBurst(5))
	if rl.AllowN(6) {
		t.Fatal("expected denial when requesting more than burst")
	}
	if !rl.AllowN(5) {
		t.Fatal("tokens should not be consumed on failed AllowN")
	}
}

// --- Wait: immediate ---

func TestWait_Immediate(t *testing.T) {
	rl := New(WithRate(10), WithBurst(5))
	err := rl.Wait(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// --- Wait: blocks until token available ---

func TestWait_BlocksUntilAvailable(t *testing.T) {
	rl := New(WithRate(100), WithBurst(1))
	rl.Allow()

	start := time.Now()
	err := rl.Wait(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if elapsed < 5*time.Millisecond {
		t.Fatalf("expected some wait, got %v", elapsed)
	}
}

// --- Wait: context cancellation ---

func TestWait_ContextCancelled(t *testing.T) {
	rl := New(WithRate(1), WithBurst(1))
	rl.Allow()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := rl.Wait(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != DomainRate || xe.Code != CodeCancelled {
		t.Fatalf("expected RATE/CANCELLED, got %s/%s", xe.Domain, xe.Code)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded in chain, got %v", err)
	}
}

// --- WaitN: blocks for multiple ---

func TestWaitN_BlocksForMultiple(t *testing.T) {
	rl := New(WithRate(100), WithBurst(2))
	rl.AllowN(2)

	start := time.Now()
	err := rl.WaitN(context.Background(), 2)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if elapsed < 10*time.Millisecond {
		t.Fatalf("expected wait for 2 tokens, got %v", elapsed)
	}
}

// --- WaitN: context cancellation ---

func TestWaitN_ContextCancelled(t *testing.T) {
	rl := New(WithRate(1), WithBurst(1))
	rl.Allow()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rl.WaitN(ctx, 5)
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != DomainRate || xe.Code != CodeCancelled {
		t.Fatalf("expected RATE/CANCELLED, got %s/%s", xe.Domain, xe.Code)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled in chain, got %v", err)
	}
}

// --- Tokens: reports available ---

func TestTokens_ReportsAvailable(t *testing.T) {
	rl := New(WithRate(10), WithBurst(10))
	if rl.Tokens() < 9.9 {
		t.Fatalf("expected ~10 tokens initially, got %v", rl.Tokens())
	}
	rl.AllowN(5)
	if rl.Tokens() > 5.1 {
		t.Fatalf("expected ~5 tokens after consuming 5, got %v", rl.Tokens())
	}
}

// --- Reset ---

func TestReset(t *testing.T) {
	rl := New(WithRate(10), WithBurst(5))
	for range 5 {
		rl.Allow()
	}
	if rl.Allow() {
		t.Fatal("expected denial after burst")
	}
	rl.Reset()
	for i := range 5 {
		if !rl.Allow() {
			t.Fatalf("request %d should be allowed after reset", i+1)
		}
	}
}

// --- Concurrent safety ---

func TestAllow_ConcurrentSafety(t *testing.T) {
	rl := New(WithRate(1000000), WithBurst(1000))
	var allowed atomic.Int64
	var wg sync.WaitGroup

	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if rl.Allow() {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if allowed.Load() == 0 {
		t.Fatal("expected some requests to be allowed")
	}
	if allowed.Load() > 10000 {
		t.Fatalf("allowed %d, expected at most 10000", allowed.Load())
	}
}

// --- Domain/Code constants ---

func TestDomainConstant(t *testing.T) {
	if DomainRate != "RATE" {
		t.Fatalf("expected RATE, got %s", DomainRate)
	}
}

func TestCodeConstants(t *testing.T) {
	if CodeLimited != "LIMITED" {
		t.Fatalf("expected LIMITED, got %s", CodeLimited)
	}
	if CodeCancelled != "CANCELLED" {
		t.Fatalf("expected CANCELLED, got %s", CodeCancelled)
	}
}

// --- Stats ---

func TestStats_Initial(t *testing.T) {
	rl := New(WithRate(100), WithBurst(20))
	s := rl.Stats()
	if s.Rate != 100 {
		t.Fatalf("expected Rate=100, got %f", s.Rate)
	}
	if s.Burst != 20 {
		t.Fatalf("expected Burst=20, got %d", s.Burst)
	}
	if s.Allowed != 0 || s.Limited != 0 {
		t.Fatalf("expected all zeros, got %+v", s)
	}
}

func TestStats_AfterAllow(t *testing.T) {
	rl := New(WithRate(100), WithBurst(10))
	for i := 0; i < 5; i++ {
		rl.Allow()
	}
	s := rl.Stats()
	if s.Allowed != 5 {
		t.Fatalf("expected Allowed=5, got %d", s.Allowed)
	}
}

func TestStats_AfterLimited(t *testing.T) {
	rl := New(WithRate(1), WithBurst(1))
	rl.Allow()
	rl.Allow()
	s := rl.Stats()
	if s.Allowed != 1 {
		t.Fatalf("expected Allowed=1, got %d", s.Allowed)
	}
	if s.Limited != 1 {
		t.Fatalf("expected Limited=1, got %d", s.Limited)
	}
}

func TestStats_Tokens(t *testing.T) {
	rl := New(WithRate(100), WithBurst(10))
	rl.AllowN(3)
	s := rl.Stats()
	if s.Tokens > 7.1 || s.Tokens < 6.9 {
		t.Fatalf("expected Tokens~7, got %f", s.Tokens)
	}
}

func TestResetStats(t *testing.T) {
	rl := New(WithRate(100), WithBurst(10))
	rl.Allow()
	rl.ResetStats()
	s := rl.Stats()
	if s.Allowed != 0 || s.Limited != 0 {
		t.Fatalf("expected all zeros after reset, got %+v", s)
	}
}

func TestAllowN_PanicsOnZero(t *testing.T) {
	rl := New()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for AllowN(0)")
		}
	}()
	rl.AllowN(0)
}

func TestAllowN_PanicsOnNegative(t *testing.T) {
	rl := New()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for AllowN(-1)")
		}
	}()
	rl.AllowN(-1)
}

func TestWaitN_PanicsOnZero(t *testing.T) {
	rl := New()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for WaitN(0)")
		}
	}()
	rl.WaitN(context.Background(), 0)
}

func TestWaitN_PanicsOnNegative(t *testing.T) {
	rl := New()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for WaitN(-1)")
		}
	}()
	rl.WaitN(context.Background(), -1)
}
