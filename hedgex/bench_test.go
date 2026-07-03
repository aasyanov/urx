package hedgex

import (
	"context"
	"testing"
	"time"
)

func BenchmarkExecute_PrimaryWins(b *testing.B) {
	// Long delay so the original always wins before any hedge launches. This
	// measures the full hedged dispatch path (goroutine + channel + timer)
	// when no hedge actually fires — the common good case.
	h := New(WithDelay(time.Hour), WithMaxParallel(3))
	ctx := context.Background()
	fn := func(context.Context, HedgeController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(h, ctx, fn)
	}
}

func BenchmarkExecute_PrimaryWins_Parallel(b *testing.B) {
	h := New(WithDelay(time.Hour), WithMaxParallel(3))
	ctx := context.Background()
	fn := func(context.Context, HedgeController) (int, error) { return 1, nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(h, ctx, fn)
		}
	})
}

func BenchmarkExecute_NoHedging(b *testing.B) {
	// MaxParallel=1 disables hedging entirely (single copy).
	h := New(WithMaxParallel(1))
	ctx := context.Background()
	fn := func(context.Context, HedgeController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(h, ctx, fn)
	}
}

func BenchmarkExecuteMulti_PrimaryWins(b *testing.B) {
	h := New(WithDelay(time.Hour), WithMaxParallel(3))
	ctx := context.Background()
	fns := []HedgeFunc[int]{
		func(context.Context, HedgeController) (int, error) { return 1, nil },
		func(context.Context, HedgeController) (int, error) { return 2, nil },
		func(context.Context, HedgeController) (int, error) { return 3, nil },
	}

	b.ResetTimer()
	for b.Loop() {
		_, _ = ExecuteMulti(h, ctx, fns)
	}
}

func BenchmarkDelays(b *testing.B) {
	h := New(WithDelay(10*time.Millisecond), WithMaxDelay(time.Second), WithMaxParallel(8))
	b.ResetTimer()
	for b.Loop() {
		_ = h.delays(8)
	}
}
