package adaptx

import (
	"context"
	"testing"
)

func BenchmarkExecute(b *testing.B) {
	l := New(WithInitialLimit(1000), WithMaxLimit(1000), WithWarmupSamples(0))
	defer l.Close()
	ctx := context.Background()
	fn := func(context.Context, AdaptController) (int, error) { return 0, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(l, ctx, fn)
	}
}

func BenchmarkExecute_Parallel(b *testing.B) {
	l := New(WithInitialLimit(1000), WithMaxLimit(1000), WithWarmupSamples(0))
	defer l.Close()
	ctx := context.Background()
	fn := func(context.Context, AdaptController) (int, error) { return 0, nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(l, ctx, fn)
		}
	})
}

func BenchmarkAcquire(b *testing.B) {
	l := New(WithInitialLimit(1000), WithMaxLimit(1000), WithWarmupSamples(0))
	defer l.Close()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		rel, _ := l.Acquire(ctx)
		rel(true, 0)
	}
}

func BenchmarkAcquire_Parallel(b *testing.B) {
	l := New(WithInitialLimit(1000), WithMaxLimit(1000), WithWarmupSamples(0))
	defer l.Close()
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rel, _ := l.Acquire(ctx)
			rel(true, 0)
		}
	})
}

func BenchmarkTryAcquire(b *testing.B) {
	l := New(WithInitialLimit(1000), WithMaxLimit(1000), WithWarmupSamples(0))
	defer l.Close()

	b.ResetTimer()
	for b.Loop() {
		rel, ok := l.TryAcquire()
		if ok {
			rel(true, 0)
		}
	}
}

func BenchmarkTryExecute(b *testing.B) {
	l := New(WithInitialLimit(1000), WithMaxLimit(1000), WithWarmupSamples(0))
	defer l.Close()
	ctx := context.Background()
	fn := func(context.Context, AdaptController) (int, error) { return 0, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _, _ = TryExecute(l, ctx, fn)
	}
}

func BenchmarkAllow(b *testing.B) {
	l := New(WithInitialLimit(1000), WithMaxLimit(1000))
	defer l.Close()

	b.ResetTimer()
	for b.Loop() {
		_ = l.Allow()
	}
}

func BenchmarkLimit(b *testing.B) {
	l := New()
	defer l.Close()

	b.ResetTimer()
	for b.Loop() {
		_ = l.Limit()
	}
}
