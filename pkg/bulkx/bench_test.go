package bulkx

import (
	"context"
	"testing"
)

// --- BenchmarkExecute_Success ---

func BenchmarkExecute_Success(b *testing.B) {
	bh := New()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		Execute[struct{}](bh, ctx, func(ctx context.Context, bc BulkController) (struct{}, error) {
			return struct{}{}, nil
		})
	}
}

// --- BenchmarkTryExecute_Success ---

func BenchmarkTryExecute_Success(b *testing.B) {
	bh := New()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		TryExecute[struct{}](bh, ctx, func(ctx context.Context, bc BulkController) (struct{}, error) {
			return struct{}{}, nil
		})
	}
}

// --- BenchmarkTryExecute_NoSlot ---

func BenchmarkTryExecute_NoSlot(b *testing.B) {
	bh := New(WithMaxConcurrent(1))
	ctx := context.Background()
	blocker := make(chan struct{})
	go Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		<-blocker
		return struct{}{}, nil
	})
	b.ReportAllocs()
	for b.Loop() {
		TryExecute[struct{}](bh, ctx, func(ctx context.Context, bc BulkController) (struct{}, error) {
			return struct{}{}, nil
		})
	}
	close(blocker)
}

// --- BenchmarkActive ---

func BenchmarkActive(b *testing.B) {
	bh := New()
	b.ReportAllocs()
	for b.Loop() {
		bh.Active()
	}
}
