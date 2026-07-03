package toutx

import (
	"context"
	"testing"
	"time"
)

func BenchmarkExecute_Fast(b *testing.B) {
	ctx := context.Background()
	fn := func(context.Context, TimeoutController) (int, error) { return 1, nil }
	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(ctx, time.Second, fn)
	}
}

func BenchmarkExecute_WithTimer(b *testing.B) {
	ctx := context.Background()
	timer := New(WithTimeout(time.Second), WithOp("bench"))
	fn := func(context.Context, TimeoutController) (int, error) { return 1, nil }
	opt := WithTimer(timer)
	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(ctx, 0, fn, opt)
	}
}

func BenchmarkExecute_Fast_Parallel(b *testing.B) {
	ctx := context.Background()
	fn := func(context.Context, TimeoutController) (int, error) { return 1, nil }
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(ctx, time.Second, fn)
		}
	})
}
