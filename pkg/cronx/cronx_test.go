package cronx

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met in %s", timeout)
}

// --- AddJob ---

func TestAddJob_Success(t *testing.T) {
	s := New()
	err := AddJob(s, "a", time.Hour, func(context.Context, JobController) (int, error) {
		return 1, nil
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if s.Stats().TotalJobs != 1 {
		t.Fatal("expected 1 job")
	}
}

func TestAddJob_NilFunc(t *testing.T) {
	s := New()
	err := AddJob[int](s, "x", time.Hour, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeNilFunc {
		t.Fatalf("expected NIL_FUNC, got %v", err)
	}
}

func TestAddJob_NilScheduler(t *testing.T) {
	err := AddJob((*Scheduler)(nil), "x", time.Hour, func(context.Context, JobController) (int, error) {
		return 1, nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeInvalidInput {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestAddJob_AfterStop(t *testing.T) {
	s := New()
	_ = s.Start(context.Background())
	_ = s.Stop(time.Second)

	err := AddJob(s, "x", time.Hour, func(context.Context, JobController) (int, error) {
		return 1, nil
	})
	if err == nil {
		t.Fatal("expected CLOSED")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeClosed {
		t.Fatalf("expected CLOSED, got %v", err)
	}
}

func TestAddJob_AutoName(t *testing.T) {
	s := New()
	_ = AddJob(s, "", time.Hour, func(context.Context, JobController) (int, error) { return 0, nil })
	_ = AddJob(s, "", time.Hour, func(context.Context, JobController) (int, error) { return 0, nil })
	st := s.Stats()
	if st.TotalJobs != 2 {
		t.Fatalf("expected 2 jobs, got %d", st.TotalJobs)
	}
}

func TestAddJob_AfterStart(t *testing.T) {
	s := New()
	_ = s.Start(context.Background())

	var ran atomic.Int64
	_ = AddJob(s, "hot", 10*time.Millisecond, func(context.Context, JobController) (int, error) {
		ran.Add(1)
		return 1, nil
	})
	waitFor(t, 200*time.Millisecond, func() bool { return ran.Load() >= 1 })
	_ = s.Stop(time.Second)
}

// --- Lifecycle ---

func TestStartStopLifecycle(t *testing.T) {
	s := New()
	if err := s.Stop(time.Second); err == nil {
		t.Fatal("expected NOT_STARTED")
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := s.Start(context.Background()); err == nil {
		t.Fatal("expected ALREADY_STARTED")
	}
	if err := s.Stop(time.Second); err != nil {
		t.Fatalf("unexpected stop error: %v", err)
	}
	if err := s.Stop(time.Second); err != nil {
		t.Fatalf("expected idempotent stop, got %v", err)
	}
}

func TestStartAfterStop(t *testing.T) {
	s := New()
	_ = s.Start(context.Background())
	_ = s.Stop(time.Second)
	err := s.Start(context.Background())
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeClosed {
		t.Fatalf("expected CLOSED, got %v", err)
	}
}

func TestStartNilContext(t *testing.T) {
	s := New()
	if err := s.Start(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = s.Stop(time.Second)
}

func TestStopZeroTimeout(t *testing.T) {
	s := New()
	_ = AddJob(s, "a", 10*time.Millisecond, func(context.Context, JobController) (int, error) {
		return 1, nil
	})
	_ = s.Start(context.Background())
	time.Sleep(30 * time.Millisecond)
	if err := s.Stop(0); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestIsClosed(t *testing.T) {
	s := New()
	if s.IsClosed() {
		t.Fatal("expected not closed")
	}
	_ = s.Start(context.Background())
	_ = s.Stop(time.Second)
	if !s.IsClosed() {
		t.Fatal("expected closed")
	}
}

// --- Execution ---

func TestEveryJob(t *testing.T) {
	s := New()
	var runs atomic.Int64
	_ = AddJob(s, "tick", 10*time.Millisecond, func(context.Context, JobController) (int, error) {
		runs.Add(1)
		return 1, nil
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return runs.Load() >= 3 })
	_ = s.Stop(time.Second)
}

func TestOnceJob(t *testing.T) {
	s := New()
	var runs atomic.Int64
	_ = AddJob(s, "once", 0, func(context.Context, JobController) (int, error) {
		runs.Add(1)
		return 1, nil
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return runs.Load() == 1 })
	time.Sleep(50 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Fatalf("expected 1 run, got %d", got)
	}
	_ = s.Stop(time.Second)
}

func TestPanicRecovery(t *testing.T) {
	s := New()
	_ = AddJob(s, "panic", 0, func(context.Context, JobController) (int, error) {
		panic("boom")
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return s.Stats().FailureRuns >= 1 })
	st := s.Stats()
	if st.FailureRuns != 1 {
		t.Fatalf("expected 1 failure, got %d", st.FailureRuns)
	}
	_ = s.Stop(time.Second)
}

func TestJobReturnsError(t *testing.T) {
	s := New()
	_ = AddJob(s, "err", 0, func(context.Context, JobController) (int, error) {
		return 0, errors.New("fail")
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return s.Stats().FailureRuns >= 1 })
	_ = s.Stop(time.Second)
}

// --- Controller ---

func TestController_RunNumber(t *testing.T) {
	s := New()
	var got atomic.Int64
	_ = AddJob(s, "rn", 0, func(_ context.Context, jc JobController) (int, error) {
		got.Store(jc.RunNumber())
		return 1, nil
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return got.Load() == 1 })
	_ = s.Stop(time.Second)
}

func TestController_Abort(t *testing.T) {
	s := New()
	var runs atomic.Int64
	_ = AddJob(s, "ab", 10*time.Millisecond, func(_ context.Context, jc JobController) (int, error) {
		n := runs.Add(1)
		if n >= 2 {
			jc.Abort()
		}
		return 1, nil
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return runs.Load() >= 2 })
	time.Sleep(60 * time.Millisecond)
	if got := runs.Load(); got > 3 {
		t.Fatalf("expected ~2 runs after abort, got %d", got)
	}
	_ = s.Stop(time.Second)
}

func TestController_Reschedule(t *testing.T) {
	s := New()
	var runs atomic.Int64
	_ = AddJob(s, "rs", 10*time.Millisecond, func(_ context.Context, jc JobController) (int, error) {
		n := runs.Add(1)
		if n == 1 {
			jc.Reschedule(time.Hour)
		}
		return 1, nil
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return runs.Load() >= 1 })
	time.Sleep(80 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Fatalf("expected 1 run after reschedule to 1h, got %d", got)
	}
	_ = s.Stop(time.Second)
}

func TestController_SkipError(t *testing.T) {
	s := New()
	_ = AddJob(s, "se", 0, func(_ context.Context, jc JobController) (int, error) {
		jc.SkipError()
		return 0, errors.New("ignored")
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return s.Stats().SuccessRuns >= 1 })
	st := s.Stats()
	if st.FailureRuns != 0 {
		t.Fatalf("expected 0 failures (skipped), got %d", st.FailureRuns)
	}
	if st.SuccessRuns != 1 {
		t.Fatalf("expected 1 success (skipped error), got %d", st.SuccessRuns)
	}
	_ = s.Stop(time.Second)
}

func TestController_LastRunTime(t *testing.T) {
	s := New()
	var firstRun atomic.Int64
	var prevRun atomic.Int64
	_ = AddJob(s, "lr", 10*time.Millisecond, func(_ context.Context, jc JobController) (int, error) {
		if jc.RunNumber() == 1 {
			firstRun.Store(time.Now().UnixNano())
		}
		if jc.RunNumber() == 2 {
			lr := jc.LastRunTime()
			if !lr.IsZero() {
				prevRun.Store(lr.UnixNano())
			}
		}
		return 1, nil
	})
	_ = s.Start(context.Background())
	waitFor(t, 250*time.Millisecond, func() bool { return prevRun.Load() > 0 })
	if prevRun.Load() <= 0 || firstRun.Load() <= 0 {
		t.Fatal("expected both first and previous run times")
	}
	// Previous run time should not be in the future.
	if prevRun.Load() > time.Now().UnixNano() {
		t.Fatal("previous run time points to future")
	}
	_ = s.Stop(time.Second)
}

// --- Stats ---

func TestStats(t *testing.T) {
	s := New(WithName("test"))
	_ = AddJob(s, "a", 10*time.Millisecond, func(context.Context, JobController) (int, error) {
		return 1, nil
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return s.Stats().TotalRuns >= 2 })

	st := s.Stats()
	if st.Name != "test" {
		t.Fatalf("expected name test, got %s", st.Name)
	}
	if st.TotalJobs != 1 || st.ActiveJobs != 1 {
		t.Fatalf("expected 1/1 jobs, got %d/%d", st.TotalJobs, st.ActiveJobs)
	}
	if len(st.Jobs) != 1 || st.Jobs[0].Name != "a" {
		t.Fatal("expected job stats")
	}
	_ = s.Stop(time.Second)
}

func TestResetStats(t *testing.T) {
	s := New()
	_ = AddJob(s, "r", 10*time.Millisecond, func(context.Context, JobController) (int, error) {
		return 1, nil
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return s.Stats().TotalRuns >= 2 })
	s.ResetStats()
	st := s.Stats()
	if st.TotalRuns != 0 || st.SuccessRuns != 0 || st.FailureRuns != 0 {
		t.Fatalf("expected zeroed, got %+v", st)
	}
	if st.Jobs[0].TotalRuns != 0 {
		t.Fatalf("expected zeroed job stats, got %d", st.Jobs[0].TotalRuns)
	}
	_ = s.Stop(time.Second)
}

// --- HealthCheck ---

func TestHealthCheck_Healthy(t *testing.T) {
	s := New()
	_ = AddJob(s, "h", 0, func(context.Context, JobController) (int, error) {
		return 1, nil
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return s.Stats().TotalRuns >= 1 })
	if err := s.HealthCheck(context.Background()); err != nil {
		t.Fatalf("expected healthy, got %v", err)
	}
	_ = s.Stop(time.Second)
}

func TestHealthCheck_NoRuns(t *testing.T) {
	s := New()
	if err := s.HealthCheck(context.Background()); err != nil {
		t.Fatalf("expected healthy with no runs, got %v", err)
	}
}

func TestHealthCheck_Unhealthy(t *testing.T) {
	s := New(WithFailureThreshold(0.1))
	_ = AddJob(s, "u", 10*time.Millisecond, func(context.Context, JobController) (int, error) {
		return 0, errors.New("fail")
	})
	_ = s.Start(context.Background())
	waitFor(t, 200*time.Millisecond, func() bool { return s.Stats().FailureRuns >= 2 })
	if err := s.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected unhealthy")
	}
	_ = s.Stop(time.Second)
}

// --- Options ---

func TestWithName(t *testing.T) {
	s := New(WithName(""))
	if s.cfg.name != "cronx" {
		t.Fatal("empty name should keep default")
	}
	s2 := New(WithName("custom"))
	if s2.cfg.name != "custom" {
		t.Fatalf("expected custom, got %s", s2.cfg.name)
	}
}

func TestStopTimeout(t *testing.T) {
	s := New()
	started := make(chan struct{})
	_ = AddJob(s, "slow", 10*time.Millisecond, func(_ context.Context, _ JobController) (int, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		time.Sleep(500 * time.Millisecond)
		return 1, nil
	})
	_ = s.Start(context.Background())
	<-started
	err := s.Stop(1 * time.Nanosecond)
	if err == nil {
		t.Fatal("expected SHUTDOWN_TIMEOUT")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeShutdownTimeout {
		t.Fatalf("expected SHUTDOWN_TIMEOUT, got %v", err)
	}
	time.Sleep(600 * time.Millisecond)
}

func TestWithFailureThreshold(t *testing.T) {
	s := New(WithFailureThreshold(0))
	if s.cfg.failureThreshold != 0.30 {
		t.Fatal("zero should keep default")
	}
	s2 := New(WithFailureThreshold(2.0))
	if s2.cfg.failureThreshold != 0.30 {
		t.Fatal(">1 should keep default")
	}
	s3 := New(WithFailureThreshold(0.5))
	if s3.cfg.failureThreshold != 0.5 {
		t.Fatalf("expected 0.5, got %f", s3.cfg.failureThreshold)
	}
}
