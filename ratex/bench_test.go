package ratex

import (
	"context"
	"testing"
)

func BenchmarkAllow(b *testing.B) {
	l := New(WithRate(1e9), WithBurst(1e9))
	b.ResetTimer()
	for b.Loop() {
		_ = l.Allow()
	}
}

func BenchmarkAllow_Parallel(b *testing.B) {
	l := New(WithRate(1e9), WithBurst(1e9))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = l.Allow()
		}
	})
}

func BenchmarkExecute(b *testing.B) {
	l := New(WithRate(1e9), WithBurst(1e9))
	ctx := context.Background()
	fn := func(context.Context, RateController) (int, error) { return 1, nil }
	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(l, ctx, fn)
	}
}

func BenchmarkExecute_Parallel(b *testing.B) {
	l := New(WithRate(1e9), WithBurst(1e9))
	ctx := context.Background()
	fn := func(context.Context, RateController) (int, error) { return 1, nil }
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(l, ctx, fn)
		}
	})
}

func BenchmarkTryExecute(b *testing.B) {
	l := New(WithRate(1e9), WithBurst(1e9))
	ctx := context.Background()
	fn := func(context.Context, RateController) (int, error) { return 1, nil }
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = TryExecute(l, ctx, fn)
	}
}
