package toutx

import (
	"context"
	"errors"
	"testing"
	"time"
)

// --- Execute: success (fast path) ---

func BenchmarkExecute_Success(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		Execute[struct{}](ctx, time.Second, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, nil
		})
	}
}

// --- Execute: function error ---

func BenchmarkExecute_FuncError(b *testing.B) {
	ctx := context.Background()
	fail := errors.New("fail")
	b.ReportAllocs()
	for b.Loop() {
		Execute[struct{}](ctx, time.Second, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, fail
		})
	}
}

// --- Timer: reusable execution ---

func BenchmarkTimer_Execute(b *testing.B) {
	tm := New(WithTimeout(time.Second), WithOp("bench.op"))
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		Execute[struct{}](ctx, 0, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, nil
		}, WithTimer(tm))
	}
}
