package retryx

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Do: success on first attempt ---

func TestDo_SuccessFirst(t *testing.T) {
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

// --- Do: success on second attempt ---

func TestDo_SuccessSecond(t *testing.T) {
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		if calls < 2 {
			return struct{}{}, errors.New("transient")
		}
		return struct{}{}, nil
	}, WithMaxAttempts(3), WithBackoff(time.Millisecond), WithJitter(false))
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

// --- Do: all attempts exhausted ---

func TestDo_Exhausted(t *testing.T) {
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		return struct{}{}, errors.New("always fail")
	}, WithMaxAttempts(3), WithBackoff(time.Millisecond), WithJitter(false))

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}

	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != DomainRetry {
		t.Fatalf("expected domain %s, got %s", DomainRetry, xe.Domain)
	}
	if xe.Code != CodeExhausted {
		t.Fatalf("expected code %s, got %s", CodeExhausted, xe.Code)
	}
}

// --- Do: context cancelled before start ---

func TestDo_ContextCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Do[struct{}](ctx, func(rc RetryController) (struct{}, error) {
		t.Fatal("should not be called")
		return struct{}{}, nil
	})

	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeCancelled {
		t.Fatalf("expected code %s, got %s", CodeCancelled, xe.Code)
	}
}

// --- Do: context cancelled during backoff ---

func TestDo_ContextCancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int

	_, err := Do[struct{}](ctx, func(rc RetryController) (struct{}, error) {
		calls++
		if calls == 1 {
			go func() {
				time.Sleep(5 * time.Millisecond)
				cancel()
			}()
		}
		return struct{}{}, errors.New("fail")
	}, WithMaxAttempts(10), WithBackoff(time.Second), WithJitter(false))

	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeCancelled {
		t.Fatalf("expected code %s, got %s", CodeCancelled, xe.Code)
	}
}

// --- Do: abort ---

func TestDo_Abort(t *testing.T) {
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		rc.Abort()
		return struct{}{}, errors.New("permanent")
	}, WithMaxAttempts(5), WithBackoff(time.Millisecond))

	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeAborted {
		t.Fatalf("expected code %s, got %s", CodeAborted, xe.Code)
	}
}

// --- Do: non-retryable errx.Error stops immediately ---

func TestDo_NonRetryableErrx(t *testing.T) {
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		return struct{}{}, errx.New(errx.DomainAuth, errx.CodeForbidden, "forbidden",
			errx.WithRetry(errx.RetryNone))
	}, WithMaxAttempts(5), WithBackoff(time.Millisecond))

	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeExhausted {
		t.Fatalf("expected code %s, got %s", CodeExhausted, xe.Code)
	}
}

// --- Do: retryable errx.Error retries ---

func TestDo_RetryableErrx(t *testing.T) {
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		return struct{}{}, errx.New(errx.DomainRepo, errx.CodeInternal, "db timeout",
			errx.WithRetry(errx.RetrySafe))
	}, WithMaxAttempts(3), WithBackoff(time.Millisecond), WithJitter(false))

	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Do: custom RetryIf ---

func TestDo_CustomRetryIf(t *testing.T) {
	sentinel := errors.New("retryable")
	var calls int

	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		if calls < 3 {
			return struct{}{}, sentinel
		}
		return struct{}{}, errors.New("permanent")
	}, WithMaxAttempts(5), WithBackoff(time.Millisecond), WithJitter(false),
		WithRetryIf(func(err error) bool {
			return errors.Is(err, sentinel)
		}))

	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Do: OnRetry callback ---

func TestDo_OnRetry(t *testing.T) {
	var retryAttempts []int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		return struct{}{}, errors.New("fail")
	}, WithMaxAttempts(3), WithBackoff(time.Millisecond), WithJitter(false),
		WithOnRetry(func(attempt int, err error) {
			retryAttempts = append(retryAttempts, attempt)
		}))

	if err == nil {
		t.Fatal("expected error")
	}
	// OnRetry is called after attempt 1 and 2 (not after the last)
	if len(retryAttempts) != 2 {
		t.Fatalf("expected 2 OnRetry calls, got %d: %v", len(retryAttempts), retryAttempts)
	}
	if retryAttempts[0] != 1 || retryAttempts[1] != 2 {
		t.Fatalf("expected [1, 2], got %v", retryAttempts)
	}
}

// --- Do: attempt number is correct ---

func TestDo_AttemptNumber(t *testing.T) {
	var numbers []int
	Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		numbers = append(numbers, rc.Number())
		return struct{}{}, errors.New("fail")
	}, WithMaxAttempts(3), WithBackoff(time.Millisecond), WithJitter(false))

	if len(numbers) != 3 {
		t.Fatalf("expected 3, got %d", len(numbers))
	}
	for i, n := range numbers {
		if n != i+1 {
			t.Fatalf("attempt %d: expected number %d, got %d", i, i+1, n)
		}
	}
}

// --- Do: single attempt (maxAttempts=1) ---

func TestDo_SingleAttempt(t *testing.T) {
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		return struct{}{}, errors.New("fail")
	}, WithMaxAttempts(1))

	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Do: maxAttempts <= 0 treated as 1 ---

func TestDo_ZeroMaxAttempts(t *testing.T) {
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		return struct{}{}, errors.New("fail")
	}, WithMaxAttempts(0))

	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDo_NegativeMaxAttempts(t *testing.T) {
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		return struct{}{}, errors.New("fail")
	}, WithMaxAttempts(-5))

	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Options: defaults ---

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	if cfg.maxAttempts != 3 {
		t.Fatalf("expected 3, got %d", cfg.maxAttempts)
	}
	if cfg.backoff != 100*time.Millisecond {
		t.Fatalf("expected 100ms, got %v", cfg.backoff)
	}
	if cfg.maxBackoff != 10*time.Second {
		t.Fatalf("expected 10s, got %v", cfg.maxBackoff)
	}
	if !cfg.jitter {
		t.Fatal("expected jitter=true")
	}
}

// --- Options: validation ---

func TestWithMaxAttempts_ZeroAndNegative(t *testing.T) {
	cfg := defaultConfig()
	WithMaxAttempts(0)(&cfg)
	if cfg.maxAttempts != 0 {
		t.Fatalf("expected 0, got %d", cfg.maxAttempts)
	}
	WithMaxAttempts(-1)(&cfg)
	if cfg.maxAttempts != -1 {
		t.Fatalf("expected -1, got %d", cfg.maxAttempts)
	}
}

func TestWithBackoff_Invalid(t *testing.T) {
	cfg := defaultConfig()
	WithBackoff(0)(&cfg)
	if cfg.backoff != 100*time.Millisecond {
		t.Fatalf("expected unchanged, got %v", cfg.backoff)
	}
	WithBackoff(-time.Second)(&cfg)
	if cfg.backoff != 100*time.Millisecond {
		t.Fatalf("expected unchanged, got %v", cfg.backoff)
	}
}

func TestWithMaxBackoff(t *testing.T) {
	cfg := defaultConfig()
	WithMaxBackoff(5 * time.Second)(&cfg)
	if cfg.maxBackoff != 5*time.Second {
		t.Fatalf("expected 5s, got %v", cfg.maxBackoff)
	}
	WithMaxBackoff(0)(&cfg)
	if cfg.maxBackoff != 5*time.Second {
		t.Fatalf("expected unchanged 5s, got %v", cfg.maxBackoff)
	}
	WithMaxBackoff(-time.Second)(&cfg)
	if cfg.maxBackoff != 5*time.Second {
		t.Fatalf("expected unchanged 5s, got %v", cfg.maxBackoff)
	}
}

// --- Backoff: exponential growth ---

func TestBackoff_Exponential(t *testing.T) {
	cfg := &config{
		backoff:    100 * time.Millisecond,
		maxBackoff: 10 * time.Second,
		jitter:     false,
	}

	d0 := backoff(cfg, 0) // 100ms * 2^0 = 100ms
	d1 := backoff(cfg, 1) // 100ms * 2^1 = 200ms
	d2 := backoff(cfg, 2) // 100ms * 2^2 = 400ms

	if d0 != 100*time.Millisecond {
		t.Fatalf("expected 100ms, got %v", d0)
	}
	if d1 != 200*time.Millisecond {
		t.Fatalf("expected 200ms, got %v", d1)
	}
	if d2 != 400*time.Millisecond {
		t.Fatalf("expected 400ms, got %v", d2)
	}
}

// --- Backoff: capped at maxBackoff ---

func TestBackoff_Capped(t *testing.T) {
	cfg := &config{
		backoff:    100 * time.Millisecond,
		maxBackoff: 500 * time.Millisecond,
		jitter:     false,
	}

	d10 := backoff(cfg, 10) // would be 100ms * 2^10 = 102.4s, capped to 500ms
	if d10 != 500*time.Millisecond {
		t.Fatalf("expected 500ms, got %v", d10)
	}
}

// --- Backoff: jitter produces variation ---

func TestBackoff_Jitter(t *testing.T) {
	cfg := &config{
		backoff:    100 * time.Millisecond,
		maxBackoff: 10 * time.Second,
		jitter:     true,
	}

	seen := make(map[time.Duration]bool)
	for i := 0; i < 100; i++ {
		d := backoff(cfg, 0)
		seen[d] = true
		// With jitter [0.5, 1.5), delay should be in [50ms, 150ms)
		if d < 50*time.Millisecond || d >= 150*time.Millisecond {
			t.Fatalf("jitter out of range: %v", d)
		}
	}
	if len(seen) < 2 {
		t.Fatal("jitter should produce variation")
	}
}

// --- Backoff: jitter on capped value ---

func TestBackoff_JitterOnCapped(t *testing.T) {
	cfg := &config{
		backoff:    100 * time.Millisecond,
		maxBackoff: 200 * time.Millisecond,
		jitter:     true,
	}

	for i := 0; i < 100; i++ {
		d := backoff(cfg, 10) // uncapped would be huge, capped to 200ms, then jitter
		// With jitter [0.5, 1.5), delay should be in [100ms, 300ms)
		if d < 100*time.Millisecond || d >= 300*time.Millisecond {
			t.Fatalf("jitter on capped out of range: %v", d)
		}
	}
}

// --- isRetryable: custom predicate ---

func TestIsRetryable_Custom(t *testing.T) {
	cfg := &config{retryIf: func(err error) bool { return false }}
	if isRetryable(cfg, errors.New("x")) {
		t.Fatal("expected false")
	}
}

// --- isRetryable: errx.Error ---

func TestIsRetryable_ErrxRetryable(t *testing.T) {
	cfg := &config{}
	xe := errx.New(errx.DomainRepo, errx.CodeInternal, "fail",
		errx.WithRetry(errx.RetrySafe))
	if !isRetryable(cfg, xe) {
		t.Fatal("expected true")
	}
}

func TestIsRetryable_ErrxNonRetryable(t *testing.T) {
	cfg := &config{}
	xe := errx.New(errx.DomainAuth, errx.CodeForbidden, "denied",
		errx.WithRetry(errx.RetryNone))
	if isRetryable(cfg, xe) {
		t.Fatal("expected false")
	}
}

// --- isRetryable: unknown error defaults to true ---

func TestIsRetryable_UnknownError(t *testing.T) {
	cfg := &config{}
	if !isRetryable(cfg, errors.New("unknown")) {
		t.Fatal("expected true for unknown errors")
	}
}

// --- Error structure: exhausted wraps last error ---

func TestExhausted_WrapsLastError(t *testing.T) {
	last := errors.New("last")
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		if calls == 3 {
			return struct{}{}, last
		}
		return struct{}{}, errors.New("transient")
	}, WithMaxAttempts(3), WithBackoff(time.Millisecond), WithJitter(false))

	if !errors.Is(err, last) {
		t.Fatal("expected exhausted error to wrap last error")
	}
}

// --- Error structure: cancelled wraps ctx.Err ---

func TestCancelled_WrapsCtxErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Do[struct{}](ctx, func(rc RetryController) (struct{}, error) { return struct{}{}, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("expected to wrap context.Canceled")
	}
}

// --- Error structure: aborted has attempt in meta ---

func TestAborted_HasAttemptMeta(t *testing.T) {
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		rc.Abort()
		return struct{}{}, errors.New("stop")
	}, WithMaxAttempts(5))

	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Meta["attempt"] != 1 {
		t.Fatalf("expected attempt=1, got %v", xe.Meta["attempt"])
	}
}

// --- Error structure: exhausted has attempts in meta ---

func TestExhausted_HasAttemptsMeta(t *testing.T) {
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		return struct{}{}, errors.New("fail")
	}, WithMaxAttempts(3), WithBackoff(time.Millisecond), WithJitter(false))

	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Meta["attempts"] != 3 {
		t.Fatalf("expected attempts=3, got %v", xe.Meta["attempts"])
	}
}

// --- Concurrent safety ---

func TestDo_ConcurrentSafe(t *testing.T) {
	var total atomic.Int32
	done := make(chan struct{})

	for i := 0; i < 10; i++ {
		go func() {
			Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
				total.Add(1)
				return struct{}{}, nil
			})
			done <- struct{}{}
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	if n := total.Load(); n != 10 {
		t.Fatalf("expected 10, got %d", n)
	}
}

// --- Real timing: backoff actually waits ---

func TestDo_BackoffTiming(t *testing.T) {
	start := time.Now()
	var calls int
	Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		return struct{}{}, errors.New("fail")
	}, WithMaxAttempts(3), WithBackoff(20*time.Millisecond), WithJitter(false))

	elapsed := time.Since(start)
	// 2 backoff waits: 20ms + 40ms = 60ms minimum
	if elapsed < 50*time.Millisecond {
		t.Fatalf("expected at least ~60ms, got %v", elapsed)
	}
}

// --- Abort on second attempt ---

func TestDo_AbortSecondAttempt(t *testing.T) {
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		if calls == 2 {
			rc.Abort()
		}
		return struct{}{}, errors.New("fail")
	}, WithMaxAttempts(5), WithBackoff(time.Millisecond), WithJitter(false))

	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}

	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeAborted {
		t.Fatalf("expected code %s, got %s", CodeAborted, xe.Code)
	}
}

// --- OnRetry not called on last attempt ---

func TestDo_OnRetryNotCalledOnLast(t *testing.T) {
	var retryCalls int
	Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		return struct{}{}, errors.New("fail")
	}, WithMaxAttempts(1), WithOnRetry(func(attempt int, err error) {
		retryCalls++
	}))

	if retryCalls != 0 {
		t.Fatalf("expected 0, got %d", retryCalls)
	}
}

// --- Success after retry with errx.Error ---

func TestDo_SuccessAfterRetryWithErrx(t *testing.T) {
	var calls int
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		if calls < 3 {
			return struct{}{}, errx.New(errx.DomainRepo, errx.CodeInternal, "timeout",
				errx.WithRetry(errx.RetrySafe))
		}
		return struct{}{}, nil
	}, WithMaxAttempts(5), WithBackoff(time.Millisecond), WithJitter(false))

	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3, got %d", calls)
	}
}

// --- Error constructors ---

func TestErrExhausted(t *testing.T) {
	cause := errors.New("cause")
	xe := errExhausted(3, cause)
	if xe.Domain != DomainRetry || xe.Code != CodeExhausted {
		t.Fatalf("unexpected: %s.%s", xe.Domain, xe.Code)
	}
	if !errors.Is(xe, cause) {
		t.Fatal("expected to wrap cause")
	}
}

func TestErrCancelled(t *testing.T) {
	xe := errCancelled(context.Canceled)
	if xe.Domain != DomainRetry || xe.Code != CodeCancelled {
		t.Fatalf("unexpected: %s.%s", xe.Domain, xe.Code)
	}
}

func TestErrAborted(t *testing.T) {
	cause := errors.New("stop")
	xe := errAborted(2, cause)
	if xe.Domain != DomainRetry || xe.Code != CodeAborted {
		t.Fatalf("unexpected: %s.%s", xe.Domain, xe.Code)
	}
	if xe.Meta["attempt"] != 2 {
		t.Fatalf("expected attempt=2, got %v", xe.Meta["attempt"])
	}
}

// --- WithRetryIf overrides errx check ---

func TestDo_RetryIfOverridesErrx(t *testing.T) {
	var calls int
	// errx says non-retryable, but custom RetryIf says retry
	_, err := Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		calls++
		return struct{}{}, errx.New(errx.DomainAuth, errx.CodeForbidden, "denied",
			errx.WithRetry(errx.RetryNone))
	}, WithMaxAttempts(3), WithBackoff(time.Millisecond), WithJitter(false),
		WithRetryIf(func(err error) bool { return true }))

	if calls != 3 {
		t.Fatalf("expected 3 calls (custom RetryIf overrides), got %d", calls)
	}
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- WithOnRetry receives correct error ---

func TestDo_OnRetryReceivesError(t *testing.T) {
	var received []error
	sentinel := errors.New("sentinel")

	Do[struct{}](context.Background(), func(rc RetryController) (struct{}, error) {
		return struct{}{}, sentinel
	}, WithMaxAttempts(3), WithBackoff(time.Millisecond), WithJitter(false),
		WithOnRetry(func(attempt int, err error) {
			received = append(received, err)
		}))

	for _, err := range received {
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel, got %v", err)
		}
	}
}
