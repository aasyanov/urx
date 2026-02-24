package circuitx

import (
	"context"
	"errors"
	"testing"
	"time"
)

// --- BenchmarkExecute_Success ---

func BenchmarkExecute_Success(b *testing.B) {
	cb := New()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		Execute[struct{}](cb, ctx, func(ctx context.Context, cc CircuitController) (struct{}, error) {
			return struct{}{}, nil
		})
	}
}

// --- BenchmarkExecute_Failure ---

func BenchmarkExecute_Failure(b *testing.B) {
	cb := New(WithMaxFailures(1<<30)) // never open
	ctx := context.Background()
	fail := errors.New("fail")
	b.ReportAllocs()
	for b.Loop() {
		Execute[struct{}](cb, ctx, func(ctx context.Context, cc CircuitController) (struct{}, error) {
			return struct{}{}, fail
		})
	}
}

// --- BenchmarkExecute_Open ---

func BenchmarkExecute_Open(b *testing.B) {
	cb := New(WithMaxFailures(1), WithResetTimeout(time.Hour))
	Execute[struct{}](cb, context.Background(), func(ctx context.Context, cc CircuitController) (struct{}, error) {
		return struct{}{}, errors.New("trip")
	})
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		Execute[struct{}](cb, ctx, func(ctx context.Context, cc CircuitController) (struct{}, error) {
			return struct{}{}, nil
		})
	}
}

// --- BenchmarkState ---

func BenchmarkState(b *testing.B) {
	cb := New()
	b.ReportAllocs()
	for b.Loop() {
		cb.State()
	}
}
