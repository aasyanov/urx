package fallx

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errBench = errors.New("bench failure")

func BenchmarkExecute_StaticSuccess(b *testing.B) {
	fb := New(WithStatic(0))
	defer func() { _ = fb.Close() }()
	ctx := context.Background()
	fn := func(context.Context, FallController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(fb, ctx, fn)
	}
}

func BenchmarkExecute_StaticSuccess_Parallel(b *testing.B) {
	fb := New(WithStatic(0))
	defer func() { _ = fb.Close() }()
	ctx := context.Background()
	fn := func(context.Context, FallController) (int, error) { return 1, nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(fb, ctx, fn)
		}
	})
}

func BenchmarkExecute_StaticFallback(b *testing.B) {
	fb := New(WithStatic(-1))
	defer func() { _ = fb.Close() }()
	ctx := context.Background()
	fn := func(context.Context, FallController) (int, error) { return 0, errBench }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(fb, ctx, fn)
	}
}

func BenchmarkExecute_FuncFallback(b *testing.B) {
	fb := New(WithFunc(func(context.Context, FallController) (int, error) { return -1, nil }))
	defer func() { _ = fb.Close() }()
	ctx := context.Background()
	fn := func(context.Context, FallController) (int, error) { return 0, errBench }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(fb, ctx, fn)
	}
}

func BenchmarkExecute_CachedHit(b *testing.B) {
	fb := New(WithCached[int](time.Hour, 1024))
	defer func() { _ = fb.Close() }()
	ctx := context.Background()
	fb.Seed(DefaultKey, 7)
	fn := func(context.Context, FallController) (int, error) { return 0, errBench }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(fb, ctx, fn)
	}
}

func BenchmarkExecute_CachedHit_Parallel(b *testing.B) {
	fb := New(WithCached[int](time.Hour, 1024), WithShards[int](16))
	defer func() { _ = fb.Close() }()
	ctx := context.Background()
	fb.Seed(DefaultKey, 7)
	fn := func(context.Context, FallController) (int, error) { return 0, errBench }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(fb, ctx, fn)
		}
	})
}

func BenchmarkExecute_CachedStore(b *testing.B) {
	fb := New(WithCached[int](time.Hour, 1_000_000))
	defer func() { _ = fb.Close() }()
	ctx := context.Background()
	fn := func(context.Context, FallController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(fb, ctx, fn)
	}
}
