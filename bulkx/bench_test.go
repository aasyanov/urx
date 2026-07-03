package bulkx

import (
	"context"
	"testing"
)

func BenchmarkExecute(b *testing.B) {
	bh := New(WithMaxConcurrent(1_000_000))
	defer func() { _ = bh.Close() }()
	ctx := context.Background()
	fn := func(context.Context, BulkController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(bh, ctx, fn)
	}
}

func BenchmarkExecute_Parallel(b *testing.B) {
	bh := New(WithMaxConcurrent(1_000_000))
	defer func() { _ = bh.Close() }()
	ctx := context.Background()
	fn := func(context.Context, BulkController) (int, error) { return 1, nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(bh, ctx, fn)
		}
	})
}

func BenchmarkExecute_Reject(b *testing.B) {
	// Hold the only slot so every TryExecute rejects immediately without blocking.
	bh := New(WithMaxConcurrent(1))
	defer func() { _ = bh.Close() }()
	tok, _ := bh.Acquire(context.Background())
	defer tok.Release()

	ctx := context.Background()
	fn := func(context.Context, BulkController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _, _ = TryExecute(bh, ctx, fn)
	}
}

func BenchmarkTryExecute(b *testing.B) {
	bh := New(WithMaxConcurrent(1_000_000))
	defer func() { _ = bh.Close() }()
	ctx := context.Background()
	fn := func(context.Context, BulkController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _, _ = TryExecute(bh, ctx, fn)
	}
}

func BenchmarkAcquire(b *testing.B) {
	bh := New(WithMaxConcurrent(1_000_000))
	defer func() { _ = bh.Close() }()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		tok, _ := bh.Acquire(ctx)
		tok.Release()
	}
}

func BenchmarkAcquire_Parallel(b *testing.B) {
	bh := New(WithMaxConcurrent(1_000_000))
	defer func() { _ = bh.Close() }()
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tok, _ := bh.Acquire(ctx)
			tok.Release()
		}
	})
}

func BenchmarkAllow(b *testing.B) {
	bh := New(WithMaxConcurrent(1000))
	defer func() { _ = bh.Close() }()

	b.ResetTimer()
	for b.Loop() {
		_ = bh.Allow()
	}
}
