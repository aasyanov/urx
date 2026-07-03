package retryx

import (
	"context"
	"errors"
	"testing"
	"time"
)

func BenchmarkDo_Success(b *testing.B) {
	ctx := context.Background()
	fn := func(context.Context, RetryController) (int, error) { return 1, nil }
	b.ResetTimer()
	for b.Loop() {
		_, _ = Do(ctx, fn)
	}
}

func BenchmarkDo_SuccessAfterOneRetry(b *testing.B) {
	ctx := context.Background()
	failOnce := errors.New("transient")
	opts := []Option{WithBackoff(time.Nanosecond), WithJitter(false), WithMaxAttempts(3)}
	b.ResetTimer()
	for b.Loop() {
		n := 0
		_, _ = Do(ctx, func(context.Context, RetryController) (int, error) {
			n++
			if n == 1 {
				return 0, failOnce
			}
			return 1, nil
		}, opts...)
	}
}

func BenchmarkBackoff(b *testing.B) {
	cfg := config{backoff: 100 * time.Millisecond, maxBackoff: 10 * time.Second, jitter: true}
	b.ResetTimer()
	for b.Loop() {
		_ = backoff(&cfg, 3)
	}
}

func BenchmarkDo_Success_Parallel(b *testing.B) {
	ctx := context.Background()
	fn := func(context.Context, RetryController) (int, error) { return 1, nil }
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Do(ctx, fn)
		}
	})
}
