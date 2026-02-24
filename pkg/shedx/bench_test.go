package shedx

import (
	"context"
	"testing"
)

// --- Execute: admitted (fast path) ---

func BenchmarkExecute_Admitted(b *testing.B) {
	s := New(WithCapacity(10000))
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		Execute[struct{}](s, ctx, PriorityNormal, func(ctx context.Context, sc ShedController) (struct{}, error) {
			return struct{}{}, nil
		})
	}
}

// --- Execute: rejected (shed path) ---

func BenchmarkExecute_Rejected(b *testing.B) {
	s := New(WithCapacity(100), WithThreshold(0.5))
	s.inflight.Store(99)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		Execute[struct{}](s, ctx, PriorityLow, func(ctx context.Context, sc ShedController) (struct{}, error) {
			return struct{}{}, nil
		})
	}
}

// --- Allow: check only ---

func BenchmarkAllow(b *testing.B) {
	s := New(WithCapacity(1000))
	b.ReportAllocs()
	for b.Loop() {
		s.Allow(PriorityNormal)
	}
}
