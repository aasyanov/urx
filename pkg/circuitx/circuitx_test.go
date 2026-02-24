package circuitx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- State.String ---

func TestState_String(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{Closed, "closed"},
		{Open, "open"},
		{HalfOpen, "half_open"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

// --- New: defaults ---

func TestNew_Defaults(t *testing.T) {
	cb := New()
	if cb.State() != Closed {
		t.Fatalf("expected Closed, got %v", cb.State())
	}
	if cb.Failures() != 0 {
		t.Fatalf("expected 0 failures, got %d", cb.Failures())
	}
}

// --- New: with options ---

func TestNew_WithOptions(t *testing.T) {
	cb := New(WithMaxFailures(3), WithResetTimeout(50*time.Millisecond))
	if cb.cfg.maxFailures != 3 {
		t.Fatalf("expected maxFailures=3, got %d", cb.cfg.maxFailures)
	}
	if cb.cfg.resetTimeout != 50*time.Millisecond {
		t.Fatalf("expected resetTimeout=50ms, got %v", cb.cfg.resetTimeout)
	}
}

// --- New: invalid options ---

func TestNew_InvalidOptions(t *testing.T) {
	cb := New(WithMaxFailures(-1), WithResetTimeout(-1))
	if cb.cfg.maxFailures != 5 {
		t.Fatalf("expected maxFailures=5 (negative ignored), got %d", cb.cfg.maxFailures)
	}
	if cb.cfg.resetTimeout != 10*time.Second {
		t.Fatalf("expected default resetTimeout, got %v", cb.cfg.resetTimeout)
	}
}

// --- Execute: success ---

func TestExecute_Success(t *testing.T) {
	cb := New()
	_, err := Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if cb.Failures() != 0 {
		t.Fatalf("expected 0 failures, got %d", cb.Failures())
	}
}

// --- Execute: failure increments counter ---

func TestExecute_FailureIncrementsCounter(t *testing.T) {
	cb := New(WithMaxFailures(5))
	for i := range 3 {
		_, err := Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
			return struct{}{}, errors.New("fail")
		})
		if err == nil {
			t.Fatalf("attempt %d: expected error", i)
		}
	}
	if cb.Failures() != 3 {
		t.Fatalf("expected 3 failures, got %d", cb.Failures())
	}
	if cb.State() != Closed {
		t.Fatalf("expected Closed (below threshold), got %v", cb.State())
	}
}

// --- Execute: opens after max failures ---

func TestExecute_OpensAfterMaxFailures(t *testing.T) {
	cb := New(WithMaxFailures(3))
	for range 3 {
		Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
			return struct{}{}, errors.New("fail")
		})
	}
	if cb.State() != Open {
		t.Fatalf("expected Open, got %v", cb.State())
	}
}

// --- Execute: rejects when open ---

func TestExecute_RejectsWhenOpen(t *testing.T) {
	cb := New(WithMaxFailures(1))
	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("fail")
	})

	_, err := Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		t.Fatal("should not be called")
		return struct{}{}, nil
	})
	if err == nil {
		t.Fatal("expected error when open")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != DomainCircuit || xe.Code != CodeOpen {
		t.Fatalf("expected CIRCUIT/OPEN, got %s/%s", xe.Domain, xe.Code)
	}
}

// --- Execute: half-open transition ---

func TestExecute_HalfOpenTransition(t *testing.T) {
	cb := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond))

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("fail")
	})
	if cb.State() != Open {
		t.Fatalf("expected Open, got %v", cb.State())
	}

	time.Sleep(30 * time.Millisecond)
	if cb.State() != HalfOpen {
		t.Fatalf("expected HalfOpen after timeout, got %v", cb.State())
	}
}

// --- Execute: half-open success resets ---

func TestExecute_HalfOpenSuccessResets(t *testing.T) {
	cb := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond))

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("fail")
	})
	time.Sleep(30 * time.Millisecond)

	_, err := Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if cb.State() != Closed {
		t.Fatalf("expected Closed after half-open success, got %v", cb.State())
	}
	if cb.Failures() != 0 {
		t.Fatalf("expected 0 failures after reset, got %d", cb.Failures())
	}
}

// --- Execute: half-open failure reopens ---

func TestExecute_HalfOpenFailureReopens(t *testing.T) {
	cb := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond))

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("fail")
	})
	time.Sleep(30 * time.Millisecond)

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("still broken")
	})
	if cb.State() != Open {
		t.Fatalf("expected Open after half-open failure, got %v", cb.State())
	}
}

// --- Execute: half-open single probe enforcement ---

func TestExecute_HalfOpenSingleProbe(t *testing.T) {
	cb := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond))

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("trip")
	})
	time.Sleep(30 * time.Millisecond)

	probeStarted := make(chan struct{})
	probeRelease := make(chan struct{})
	go Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		close(probeStarted)
		<-probeRelease
		return struct{}{}, nil
	})
	<-probeStarted

	_, err := Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		t.Fatal("concurrent probe should not run")
		return struct{}{}, nil
	})
	close(probeRelease)

	if err == nil {
		t.Fatal("expected error for concurrent half-open call")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeOpen {
		t.Fatalf("expected OPEN, got %s", xe.Code)
	}
}

// --- Execute: panic recovery ---

func TestExecute_PanicRecovery(t *testing.T) {
	cb := New(WithMaxFailures(5))
	_, err := Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected error from panic")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error from panic, got %T", err)
	}
	if cb.Failures() != 1 {
		t.Fatalf("expected 1 failure after panic, got %d", cb.Failures())
	}
}

// --- Reset ---

func TestReset(t *testing.T) {
	cb := New(WithMaxFailures(1))
	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("fail")
	})
	if cb.State() != Open {
		t.Fatalf("expected Open, got %v", cb.State())
	}
	cb.Reset()
	if cb.State() != Closed {
		t.Fatalf("expected Closed after reset, got %v", cb.State())
	}
	if cb.Failures() != 0 {
		t.Fatalf("expected 0 failures after reset, got %d", cb.Failures())
	}
}

// --- Domain/Code constants ---

func TestDomainConstant(t *testing.T) {
	if DomainCircuit != "CIRCUIT" {
		t.Fatalf("expected CIRCUIT, got %s", DomainCircuit)
	}
}

func TestCodeConstants(t *testing.T) {
	codes := map[string]string{
		"CodeOpen": CodeOpen,
	}
	want := map[string]string{
		"CodeOpen": "OPEN",
	}
	for name, got := range codes {
		if got != want[name] {
			t.Errorf("%s = %q, want %q", name, got, want[name])
		}
	}
}

// --- Error constructors ---

func TestErrOpen(t *testing.T) {
	e := errOpen()
	if e.Domain != DomainCircuit || e.Code != CodeOpen {
		t.Fatalf("expected CIRCUIT/OPEN, got %s/%s", e.Domain, e.Code)
	}
}

// --- CircuitController: state visible to callback ---

func TestCircuitController_StateClosed(t *testing.T) {
	cb := New()
	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		if cc.State() != Closed {
			t.Fatalf("expected Closed, got %v", cc.State())
		}
		return struct{}{}, nil
	})
}

func TestCircuitController_StateHalfOpen(t *testing.T) {
	cb := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond))
	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("trip")
	})
	time.Sleep(30 * time.Millisecond)

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		if cc.State() != HalfOpen {
			t.Fatalf("expected HalfOpen, got %v", cc.State())
		}
		return struct{}{}, nil
	})
}

// --- CircuitController: failures visible to callback ---

func TestCircuitController_Failures(t *testing.T) {
	cb := New(WithMaxFailures(5))
	for range 3 {
		Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
			return struct{}{}, errors.New("fail")
		})
	}

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		if cc.Failures() != 3 {
			t.Fatalf("expected 3 failures, got %d", cc.Failures())
		}
		return struct{}{}, nil
	})
}

// --- CircuitController: SkipFailure prevents circuit from counting error ---

func TestCircuitController_SkipFailure(t *testing.T) {
	cb := New(WithMaxFailures(2))

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		cc.SkipFailure()
		return struct{}{}, errors.New("business error")
	})

	if cb.Failures() != 0 {
		t.Fatalf("expected 0 failures (skipped), got %d", cb.Failures())
	}
	if cb.State() != Closed {
		t.Fatalf("expected Closed after skipped failure, got %v", cb.State())
	}
}

func TestCircuitController_SkipFailure_DoesNotPreventOpen(t *testing.T) {
	cb := New(WithMaxFailures(2))

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("infra error")
	})
	if cb.Failures() != 1 {
		t.Fatalf("expected 1 failure, got %d", cb.Failures())
	}

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		cc.SkipFailure()
		return struct{}{}, errors.New("business error")
	})
	if cb.Failures() != 1 {
		t.Fatalf("expected 1 failure (second skipped), got %d", cb.Failures())
	}

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("infra error 2")
	})
	if cb.State() != Open {
		t.Fatalf("expected Open after 2 real failures, got %v", cb.State())
	}
}

func TestCircuitController_SkipFailure_HalfOpen(t *testing.T) {
	cb := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond))

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("trip")
	})
	time.Sleep(30 * time.Millisecond)

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		cc.SkipFailure()
		return struct{}{}, errors.New("business error during probe")
	})

	if cb.State() == Open {
		t.Fatal("expected circuit NOT to reopen after SkipFailure in HalfOpen")
	}
}

func TestCircuitController_SkipFailure_Idempotent(t *testing.T) {
	cb := New(WithMaxFailures(1))
	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		cc.SkipFailure()
		cc.SkipFailure()
		cc.SkipFailure()
		return struct{}{}, errors.New("biz")
	})
	if cb.Failures() != 0 {
		t.Fatalf("expected 0 failures, got %d", cb.Failures())
	}
}

// --- CircuitController: success still resets after SkipFailure history ---

func TestCircuitController_SuccessResetsAfterSkips(t *testing.T) {
	cb := New(WithMaxFailures(5))

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("real fail")
	})
	if cb.Failures() != 1 {
		t.Fatalf("expected 1, got %d", cb.Failures())
	}

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, nil
	})
	if cb.Failures() != 0 {
		t.Fatalf("expected 0 after success, got %d", cb.Failures())
	}
}

// --- Stats ---

func TestStats_Initial(t *testing.T) {
	cb := New(WithMaxFailures(3))
	s := cb.Stats()
	if s.State != Closed {
		t.Fatalf("expected Closed, got %s", s.State)
	}
	if s.MaxFailures != 3 {
		t.Fatalf("expected MaxFailures=3, got %d", s.MaxFailures)
	}
	if s.Successes != 0 || s.TotalFail != 0 || s.Rejected != 0 {
		t.Fatalf("expected all zeros, got %+v", s)
	}
}

func TestStats_AfterSuccesses(t *testing.T) {
	cb := New()
	for i := 0; i < 3; i++ {
		_, _ = Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
			return struct{}{}, nil
		})
	}
	s := cb.Stats()
	if s.Successes != 3 {
		t.Fatalf("expected Successes=3, got %d", s.Successes)
	}
}

func TestStats_AfterFailures(t *testing.T) {
	cb := New(WithMaxFailures(10))
	for i := 0; i < 4; i++ {
		_, _ = Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
			return struct{}{}, errors.New("fail")
		})
	}
	s := cb.Stats()
	if s.TotalFail != 4 {
		t.Fatalf("expected TotalFail=4, got %d", s.TotalFail)
	}
	if s.Failures != 4 {
		t.Fatalf("expected Failures=4, got %d", s.Failures)
	}
}

func TestStats_Rejected(t *testing.T) {
	cb := New(WithMaxFailures(1), WithResetTimeout(time.Hour))
	_, _ = Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("trip")
	})
	_, _ = Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, nil
	})
	s := cb.Stats()
	if s.Rejected < 1 {
		t.Fatalf("expected Rejected>=1, got %d", s.Rejected)
	}
}

func TestStats_SkipFailureNotCounted(t *testing.T) {
	cb := New()
	_, _ = Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		cc.SkipFailure()
		return struct{}{}, errors.New("business error")
	})
	s := cb.Stats()
	if s.TotalFail != 0 {
		t.Fatalf("expected TotalFail=0 after SkipFailure, got %d", s.TotalFail)
	}
}

func TestStats_Trips(t *testing.T) {
	cb := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond))

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("trip 1")
	})
	s := cb.Stats()
	if s.Trips != 1 {
		t.Fatalf("expected Trips=1, got %d", s.Trips)
	}

	time.Sleep(30 * time.Millisecond)
	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, nil
	})
	if cb.State() != Closed {
		t.Fatalf("expected Closed after probe success, got %v", cb.State())
	}

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("trip 2")
	})
	s = cb.Stats()
	if s.Trips != 2 {
		t.Fatalf("expected Trips=2, got %d", s.Trips)
	}
}

func TestStats_TripsHalfOpenFailure(t *testing.T) {
	cb := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond))

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("trip")
	})
	time.Sleep(30 * time.Millisecond)

	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("probe fails")
	})

	s := cb.Stats()
	if s.Trips != 2 {
		t.Fatalf("expected Trips=2 (initial + reopen), got %d", s.Trips)
	}
}

func TestResetStats(t *testing.T) {
	cb := New(WithMaxFailures(1))
	_, _ = Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("trip")
	})
	cb.ResetStats()
	s := cb.Stats()
	if s.Successes != 0 || s.TotalFail != 0 || s.Rejected != 0 || s.Trips != 0 {
		t.Fatalf("expected all zeros after reset, got %+v", s)
	}
}
