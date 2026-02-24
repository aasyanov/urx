package shedx

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Execute: success ---

func TestExecute_Success(t *testing.T) {
	s := New(WithCapacity(10))
	_, err := Execute[struct{}](s, context.Background(), PriorityNormal, func(ctx context.Context, sc ShedController) (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// --- Execute: function error ---

func TestExecute_FuncError(t *testing.T) {
	s := New()
	sentinel := errors.New("business error")
	_, err := Execute[struct{}](s, context.Background(), PriorityNormal, func(ctx context.Context, sc ShedController) (struct{}, error) {
		return struct{}{}, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

// --- Execute: panic recovery ---

func TestExecute_PanicRecovery(t *testing.T) {
	s := New()
	_, err := Execute[struct{}](s, context.Background(), PriorityNormal, func(ctx context.Context, sc ShedController) (struct{}, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected error from panic")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if !xe.IsPanic() {
		t.Fatal("expected panic error")
	}
}

// --- Execute: closed shedder ---

func TestExecute_Closed(t *testing.T) {
	s := New()
	s.Close()
	_, err := Execute[struct{}](s, context.Background(), PriorityCritical, func(ctx context.Context, sc ShedController) (struct{}, error) {
		return struct{}{}, nil
	})
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

// --- Allow: closed ---

func TestAllow_Closed(t *testing.T) {
	s := New()
	s.Close()
	if s.Allow(PriorityCritical) {
		t.Fatal("expected false when closed")
	}
}

// --- Critical priority: never shed ---

func TestExecute_CriticalNeverShed(t *testing.T) {
	s := New(WithCapacity(1))
	// Saturate to 100% load.
	s.inflight.Store(1)

	if !s.Allow(PriorityCritical) {
		t.Fatal("critical should always be admitted")
	}
}

// --- Low priority: shed above threshold ---

func TestExecute_LowPriorityShed(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.5))
	// Load = 90/100 = 0.9, overload = (0.9-0.5)/(1-0.5) = 0.8 > 0.25
	s.inflight.Store(90)

	if s.Allow(PriorityLow) {
		t.Fatal("low priority should be shed at high load")
	}
}

// --- Normal priority: admitted at moderate overload ---

func TestExecute_NormalPriorityAdmitted(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.5))
	// Load = 60/100 = 0.6, overload = (0.6-0.5)/(1-0.5) = 0.2 < 0.6
	s.inflight.Store(60)

	if !s.Allow(PriorityNormal) {
		t.Fatal("normal priority should be admitted at moderate overload")
	}
}

// --- Normal priority: shed at extreme overload ---

func TestExecute_NormalPriorityShed(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.5))
	// Load = 95/100 = 0.95, overload = (0.95-0.5)/(1-0.5) = 0.9 > 0.6
	s.inflight.Store(95)

	if s.Allow(PriorityNormal) {
		t.Fatal("normal priority should be shed at extreme overload")
	}
}

// --- High priority: admitted at high overload ---

func TestExecute_HighPriorityAdmitted(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.5))
	// Load = 90/100 = 0.9, overload = (0.9-0.5)/(1-0.5) = 0.8 < 0.9
	s.inflight.Store(90)

	if !s.Allow(PriorityHigh) {
		t.Fatal("high priority should be admitted at high overload")
	}
}

// --- High priority: shed at extreme overload ---

func TestExecute_HighPriorityShed(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.5))
	// Load = 99/100 = 0.99, overload = (0.99-0.5)/(1-0.5) = 0.98 > 0.9
	s.inflight.Store(99)

	if s.Allow(PriorityHigh) {
		t.Fatal("high priority should be shed at extreme overload")
	}
}

// --- Below threshold: all admitted ---

func TestExecute_BelowThreshold(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.8))
	s.inflight.Store(50)

	for _, p := range []Priority{PriorityLow, PriorityNormal, PriorityHigh} {
		if !s.Allow(p) {
			t.Fatalf("all priorities should be admitted below threshold, failed for %s", p)
		}
	}
}

// --- Execute: rejected error structure ---

func TestExecute_RejectedError(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.5))
	s.inflight.Store(99)

	_, err := Execute[struct{}](s, context.Background(), PriorityLow, func(ctx context.Context, sc ShedController) (struct{}, error) {
		t.Fatal("should not execute")
		return struct{}{}, nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != DomainShed {
		t.Fatalf("expected domain %s, got %s", DomainShed, xe.Domain)
	}
	if xe.Code != CodeRejected {
		t.Fatalf("expected code %s, got %s", CodeRejected, xe.Code)
	}
}

// --- ShedController ---

func TestShedController_Priority(t *testing.T) {
	s := New(WithCapacity(100))
	var seen Priority
	Execute[struct{}](s, context.Background(), PriorityHigh, func(ctx context.Context, sc ShedController) (struct{}, error) {
		seen = sc.Priority()
		return struct{}{}, nil
	})
	if seen != PriorityHigh {
		t.Fatalf("expected PriorityHigh, got %v", seen)
	}
}

func TestShedController_Load(t *testing.T) {
	s := New(WithCapacity(100))
	s.inflight.Store(50)
	var load float64
	Execute[struct{}](s, context.Background(), PriorityNormal, func(ctx context.Context, sc ShedController) (struct{}, error) {
		load = sc.Load()
		return struct{}{}, nil
	})
	if load != 0.5 {
		t.Fatalf("expected load 0.5, got %f", load)
	}
}

func TestShedController_InFlight(t *testing.T) {
	s := New(WithCapacity(100))
	s.inflight.Store(25)
	var inf int64
	Execute[struct{}](s, context.Background(), PriorityNormal, func(ctx context.Context, sc ShedController) (struct{}, error) {
		inf = sc.InFlight()
		return struct{}{}, nil
	})
	if inf != 25 {
		t.Fatalf("expected inflight 25, got %d", inf)
	}
}

// --- InFlight tracking ---

func TestInFlight_Tracking(t *testing.T) {
	s := New(WithCapacity(100))
	if s.InFlight() != 0 {
		t.Fatalf("expected 0, got %d", s.InFlight())
	}

	started := make(chan struct{})
	release := make(chan struct{})

	go func() {
		_, _ = Execute[struct{}](s, context.Background(), PriorityNormal, func(ctx context.Context, sc ShedController) (struct{}, error) {
			close(started)
			<-release
			return struct{}{}, nil
		})
	}()

	<-started
	if s.InFlight() != 1 {
		t.Fatalf("expected 1, got %d", s.InFlight())
	}
	close(release)
}

// --- Stats ---

func TestStats(t *testing.T) {
	s := New(WithCapacity(500), WithThreshold(0.7))
	st := s.Stats()
	if st.Capacity != 500 {
		t.Fatalf("expected capacity 500, got %d", st.Capacity)
	}
	if st.Threshold != 0.7 {
		t.Fatalf("expected threshold 0.7, got %f", st.Threshold)
	}
	if st.InFlight != 0 || st.Admitted != 0 || st.Shed != 0 {
		t.Fatalf("expected zeros, got %+v", st)
	}
}

// --- Stats: counters increment ---

func TestStats_Counters(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.5))

	_, _ = Execute[struct{}](s, context.Background(), PriorityNormal, func(ctx context.Context, sc ShedController) (struct{}, error) {
		return struct{}{}, nil
	})
	st := s.Stats()
	if st.Admitted != 1 {
		t.Fatalf("expected admitted=1, got %d", st.Admitted)
	}

	s.inflight.Store(99)
	_, _ = Execute[struct{}](s, context.Background(), PriorityLow, func(ctx context.Context, sc ShedController) (struct{}, error) {
		return struct{}{}, nil
	})
	st = s.Stats()
	if st.Shed != 1 {
		t.Fatalf("expected shed=1, got %d", st.Shed)
	}
}

// --- Concurrent safety ---

func TestConcurrent_Safety(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.5))
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		p := Priority(i % 4)
		go func() {
			defer wg.Done()
			_, _ = Execute[struct{}](s, context.Background(), p, func(ctx context.Context, sc ShedController) (struct{}, error) {
				return struct{}{}, nil
			})
		}()
	}
	wg.Wait()
}

// --- Lifecycle ---

func TestClose_Idempotent(t *testing.T) {
	s := New()
	s.Close()
	s.Close()
	if !s.IsClosed() {
		t.Fatal("expected closed")
	}
}

// --- defaultConfig ---

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	if cfg.capacity != 1000 {
		t.Fatalf("expected 1000, got %d", cfg.capacity)
	}
	if cfg.threshold != 0.8 {
		t.Fatalf("expected 0.8, got %f", cfg.threshold)
	}
}

// --- Options: validation ---

func TestWithCapacity_Invalid(t *testing.T) {
	cfg := defaultConfig()
	WithCapacity(0)(&cfg)
	if cfg.capacity != 1000 {
		t.Fatalf("expected unchanged, got %d", cfg.capacity)
	}
	WithCapacity(-1)(&cfg)
	if cfg.capacity != 1000 {
		t.Fatalf("expected unchanged, got %d", cfg.capacity)
	}
}

func TestWithThreshold_Invalid(t *testing.T) {
	cfg := defaultConfig()
	WithThreshold(0)(&cfg)
	if cfg.threshold != 0.8 {
		t.Fatalf("expected unchanged, got %f", cfg.threshold)
	}
	WithThreshold(1.5)(&cfg)
	if cfg.threshold != 0.8 {
		t.Fatalf("expected unchanged, got %f", cfg.threshold)
	}
	WithThreshold(-0.1)(&cfg)
	if cfg.threshold != 0.8 {
		t.Fatalf("expected unchanged, got %f", cfg.threshold)
	}
}

func TestWithThreshold_Valid(t *testing.T) {
	cfg := defaultConfig()
	WithThreshold(0.5)(&cfg)
	if cfg.threshold != 0.5 {
		t.Fatalf("expected 0.5, got %f", cfg.threshold)
	}
	WithThreshold(1.0)(&cfg)
	if cfg.threshold != 1.0 {
		t.Fatalf("expected 1.0, got %f", cfg.threshold)
	}
}

// --- Priority: String ---

func TestPriority_String(t *testing.T) {
	tests := []struct {
		p    Priority
		want string
	}{
		{PriorityLow, "low"},
		{PriorityNormal, "normal"},
		{PriorityHigh, "high"},
		{PriorityCritical, "critical"},
		{Priority(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("Priority(%d).String() = %q, want %q", tt.p, got, tt.want)
		}
	}
}

// --- Domain/Code constants ---

func TestDomainConstant(t *testing.T) {
	if DomainShed != "SHED" {
		t.Fatalf("expected SHED, got %s", DomainShed)
	}
}

func TestCodeConstants(t *testing.T) {
	if CodeRejected != "REJECTED" {
		t.Fatalf("expected REJECTED, got %s", CodeRejected)
	}
	if CodeClosed != "CLOSED" {
		t.Fatalf("expected CLOSED, got %s", CodeClosed)
	}
}

// --- Error constructors ---

func TestErrRejected(t *testing.T) {
	e := errRejected(PriorityLow)
	if e.Domain != DomainShed || e.Code != CodeRejected {
		t.Fatalf("expected SHED/REJECTED, got %s/%s", e.Domain, e.Code)
	}
}

func TestErrClosed(t *testing.T) {
	e := errClosed()
	if e.Domain != DomainShed || e.Code != CodeClosed {
		t.Fatalf("expected SHED/CLOSED, got %s/%s", e.Domain, e.Code)
	}
}

// --- ResetStats ---

func TestResetStats(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.5))

	_, _ = Execute[struct{}](s, context.Background(), PriorityNormal, func(ctx context.Context, sc ShedController) (struct{}, error) {
		return struct{}{}, nil
	})
	s.inflight.Store(99)
	_, _ = Execute[struct{}](s, context.Background(), PriorityLow, func(ctx context.Context, sc ShedController) (struct{}, error) {
		return struct{}{}, nil
	})

	st := s.Stats()
	if st.Admitted == 0 || st.Shed == 0 {
		t.Fatalf("expected non-zero counters before reset, got %+v", st)
	}

	s.ResetStats()
	st = s.Stats()
	if st.Admitted != 0 {
		t.Fatalf("expected admitted=0 after reset, got %d", st.Admitted)
	}
	if st.Shed != 0 {
		t.Fatalf("expected shed=0 after reset, got %d", st.Shed)
	}
}

// --- New: invalid config fallback ---

func TestNew_InvalidCapacityFallback(t *testing.T) {
	// Bypass option guard to trigger New's own defense-in-depth fallback.
	forceZeroCap := func(c *config) { c.capacity = 0 }
	s := New(forceZeroCap)
	st := s.Stats()
	if st.Capacity != 1 {
		t.Fatalf("expected capacity fallback to 1, got %d", st.Capacity)
	}
}

func TestNew_InvalidThresholdFallback(t *testing.T) {
	// Bypass option guard to trigger New's own defense-in-depth fallback.
	forceZeroThreshold := func(c *config) { c.threshold = 0 }
	s := New(forceZeroThreshold)
	st := s.Stats()
	if st.Threshold != 0.8 {
		t.Fatalf("expected threshold fallback to 0.8, got %f", st.Threshold)
	}

	forceBadThreshold := func(c *config) { c.threshold = 1.5 }
	s2 := New(forceBadThreshold)
	st2 := s2.Stats()
	if st2.Threshold != 0.8 {
		t.Fatalf("expected threshold fallback to 0.8, got %f", st2.Threshold)
	}
}

// --- Load ---

func TestLoad(t *testing.T) {
	s := New(WithCapacity(100))
	s.inflight.Store(50)
	load := s.Load()
	if load != 0.5 {
		t.Fatalf("expected 0.5, got %f", load)
	}
}
