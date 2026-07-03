package warmupx

import (
	"context"
	"testing"
)

func BenchmarkWarmer_Allow(b *testing.B) {
	w := New(WithMinCapacity(0.5), WithMaxCapacity(0.5))
	b.ResetTimer()
	for b.Loop() {
		w.Allow()
	}
}

func BenchmarkWarmer_Allow_Parallel(b *testing.B) {
	w := New(WithMinCapacity(0.5), WithMaxCapacity(0.5))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w.Allow()
		}
	})
}

func BenchmarkWarmer_Capacity(b *testing.B) {
	w := New()
	b.ResetTimer()
	for b.Loop() {
		_ = w.Capacity()
	}
}

func BenchmarkWarmer_MaxRequests(b *testing.B) {
	w := New(WithMinCapacity(0.5), WithMaxCapacity(0.5))
	b.ResetTimer()
	for b.Loop() {
		_ = w.MaxRequests(1000)
	}
}

func BenchmarkExecute(b *testing.B) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	ctx := context.Background()
	fn := func(_ context.Context, _ WarmupController) (int, error) { return 1, nil }
	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(w, ctx, fn)
	}
}

func BenchmarkExecute_Parallel(b *testing.B) {
	w := New(WithMinCapacity(1), WithMaxCapacity(1))
	ctx := context.Background()
	fn := func(_ context.Context, _ WarmupController) (int, error) { return 1, nil }
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(w, ctx, fn)
		}
	})
}

func BenchmarkWarmer_Stats(b *testing.B) {
	w := New()
	w.Start()
	defer w.Stop()
	b.ResetTimer()
	for b.Loop() {
		_ = w.Stats()
	}
}
