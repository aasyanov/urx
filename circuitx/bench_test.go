package circuitx

import (
	"context"
	"testing"
	"time"
)

func BenchmarkExecute_Closed(b *testing.B) {
	cb := New(WithMaxFailures(1_000_000))
	ctx := context.Background()
	fn := func(context.Context, CircuitController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(cb, ctx, fn)
	}
}

func BenchmarkExecute_Closed_Parallel(b *testing.B) {
	cb := New(WithMaxFailures(1_000_000))
	ctx := context.Background()
	fn := func(context.Context, CircuitController) (int, error) { return 1, nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(cb, ctx, fn)
		}
	})
}

func BenchmarkExecute_Open(b *testing.B) {
	// Hold the breaker Open so every call is rejected before fn runs.
	cb := New(WithMaxFailures(1), WithResetTimeout(time.Hour))
	ctx := context.Background()
	_, _ = Execute(cb, ctx, fail[int]())

	fn := func(context.Context, CircuitController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(cb, ctx, fn)
	}
}

func BenchmarkExecute_Open_Parallel(b *testing.B) {
	cb := New(WithMaxFailures(1), WithResetTimeout(time.Hour))
	ctx := context.Background()
	_, _ = Execute(cb, ctx, fail[int]())

	fn := func(context.Context, CircuitController) (int, error) { return 1, nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(cb, ctx, fn)
		}
	})
}

func BenchmarkState(b *testing.B) {
	cb := New()

	b.ResetTimer()
	for b.Loop() {
		_ = cb.State()
	}
}

func BenchmarkStats(b *testing.B) {
	cb := New()

	b.ResetTimer()
	for b.Loop() {
		_ = cb.Stats()
	}
}
