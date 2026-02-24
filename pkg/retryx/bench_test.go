package retryx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Do: success on first attempt (no backoff) ---

func BenchmarkDo_SuccessFirst(b *testing.B) {
	ctx := context.Background()
	for b.Loop() {
		Do[struct{}](ctx, func(rc RetryController) (struct{}, error) { return struct{}{}, nil })
	}
}

// --- Do: success on second attempt (1 backoff) ---

func BenchmarkDo_SuccessSecond(b *testing.B) {
	ctx := context.Background()
	opts := []Option{WithMaxAttempts(3), WithBackoff(time.Nanosecond), WithJitter(false)}
	for b.Loop() {
		i := 0
		Do[struct{}](ctx, func(rc RetryController) (struct{}, error) {
			i++
			if i < 2 {
				return struct{}{}, errors.New("transient")
			}
			return struct{}{}, nil
		}, opts...)
	}
}

// --- Do: all attempts exhausted ---

func BenchmarkDo_Exhausted(b *testing.B) {
	ctx := context.Background()
	fail := errors.New("fail")
	opts := []Option{WithMaxAttempts(3), WithBackoff(time.Nanosecond), WithJitter(false)}
	for b.Loop() {
		Do[struct{}](ctx, func(rc RetryController) (struct{}, error) { return struct{}{}, fail }, opts...)
	}
}

// --- Do: with errx.Error retryable ---

func BenchmarkDo_ErrxRetryable(b *testing.B) {
	ctx := context.Background()
	opts := []Option{WithMaxAttempts(3), WithBackoff(time.Nanosecond), WithJitter(false)}
	for b.Loop() {
		Do[struct{}](ctx, func(rc RetryController) (struct{}, error) {
			return struct{}{}, errx.New(errx.DomainRepo, errx.CodeInternal, "timeout",
				errx.WithRetry(errx.RetrySafe))
		}, opts...)
	}
}

// --- Do: with errx.Error non-retryable (stops immediately) ---

func BenchmarkDo_ErrxNonRetryable(b *testing.B) {
	ctx := context.Background()
	opts := []Option{WithMaxAttempts(5), WithBackoff(time.Nanosecond), WithJitter(false)}
	for b.Loop() {
		Do[struct{}](ctx, func(rc RetryController) (struct{}, error) {
			return struct{}{}, errx.New(errx.DomainAuth, errx.CodeForbidden, "denied",
				errx.WithRetry(errx.RetryNone))
		}, opts...)
	}
}

// --- Do: with abort ---

func BenchmarkDo_Abort(b *testing.B) {
	ctx := context.Background()
	fail := errors.New("stop")
	for b.Loop() {
		Do[struct{}](ctx, func(rc RetryController) (struct{}, error) {
			rc.Abort()
			return struct{}{}, fail
		}, WithMaxAttempts(5))
	}
}

// --- Do: with OnRetry callback ---

func BenchmarkDo_WithOnRetry(b *testing.B) {
	ctx := context.Background()
	fail := errors.New("fail")
	opts := []Option{
		WithMaxAttempts(3),
		WithBackoff(time.Nanosecond),
		WithJitter(false),
		WithOnRetry(func(attempt int, err error) {}),
	}
	for b.Loop() {
		Do[struct{}](ctx, func(rc RetryController) (struct{}, error) { return struct{}{}, fail }, opts...)
	}
}

// --- Do: with custom RetryIf ---

func BenchmarkDo_WithRetryIf(b *testing.B) {
	ctx := context.Background()
	fail := errors.New("fail")
	opts := []Option{
		WithMaxAttempts(3),
		WithBackoff(time.Nanosecond),
		WithJitter(false),
		WithRetryIf(func(err error) bool { return true }),
	}
	for b.Loop() {
		Do[struct{}](ctx, func(rc RetryController) (struct{}, error) { return struct{}{}, fail }, opts...)
	}
}

// --- Backoff calculation ---

func BenchmarkBackoff_NoJitter(b *testing.B) {
	cfg := &config{backoff: 100 * time.Millisecond, maxBackoff: 10 * time.Second, jitter: false}
	for b.Loop() {
		backoff(cfg, 3)
	}
}

func BenchmarkBackoff_WithJitter(b *testing.B) {
	cfg := &config{backoff: 100 * time.Millisecond, maxBackoff: 10 * time.Second, jitter: true}
	for b.Loop() {
		backoff(cfg, 3)
	}
}

// --- isRetryable ---

func BenchmarkIsRetryable_Plain(b *testing.B) {
	cfg := &config{}
	err := errors.New("plain")
	for b.Loop() {
		isRetryable(cfg, err)
	}
}

func BenchmarkIsRetryable_Errx(b *testing.B) {
	cfg := &config{}
	err := errx.New(errx.DomainRepo, errx.CodeInternal, "fail",
		errx.WithRetry(errx.RetrySafe))
	for b.Loop() {
		isRetryable(cfg, err)
	}
}

// --- DefaultConfig ---

func BenchmarkDefaultConfig(b *testing.B) {
	for b.Loop() {
		defaultConfig()
	}
}
