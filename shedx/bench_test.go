package shedx

import (
	"context"
	"testing"
)

func BenchmarkExecute_Admit(b *testing.B) {
	s := New(WithCapacity(1_000_000))
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	fn := func(context.Context, ShedController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(s, ctx, PriorityNormal, fn)
	}
}

func BenchmarkExecute_Admit_Parallel(b *testing.B) {
	s := New(WithCapacity(1_000_000))
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	fn := func(context.Context, ShedController) (int, error) { return 1, nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(s, ctx, PriorityNormal, fn)
		}
	})
}

func BenchmarkExecute_Shed(b *testing.B) {
	// Hold the shedder at full load so every Normal request is shed before fn.
	s := New(WithCapacity(1), WithThreshold(0.5))
	defer func() { _ = s.Close() }()
	tok, _ := s.Acquire(PriorityCritical)
	defer tok.Release()

	ctx := context.Background()
	fn := func(context.Context, ShedController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(s, ctx, PriorityNormal, fn)
	}
}

func BenchmarkTryExecute_Admit(b *testing.B) {
	s := New(WithCapacity(1_000_000))
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	fn := func(context.Context, ShedController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _, _ = TryExecute(s, ctx, PriorityNormal, fn)
	}
}

func BenchmarkTryExecute_Admit_Parallel(b *testing.B) {
	s := New(WithCapacity(1_000_000))
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	fn := func(context.Context, ShedController) (int, error) { return 1, nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _, _ = TryExecute(s, ctx, PriorityNormal, fn)
		}
	})
}

func BenchmarkTryExecute_Shed(b *testing.B) {
	// Rejection path — measured with TryExecute to avoid ErrRejected allocation
	// in the benchmark loop.
	s := New(WithCapacity(1), WithThreshold(0.5))
	defer func() { _ = s.Close() }()
	tok, _ := s.Acquire(PriorityCritical)
	defer tok.Release()

	ctx := context.Background()
	fn := func(context.Context, ShedController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _, _ = TryExecute(s, ctx, PriorityNormal, fn)
	}
}

func BenchmarkTryExecute_Shed_Parallel(b *testing.B) {
	s := New(WithCapacity(1), WithThreshold(0.5))
	defer func() { _ = s.Close() }()
	tok, _ := s.Acquire(PriorityCritical)
	defer tok.Release()

	ctx := context.Background()
	fn := func(context.Context, ShedController) (int, error) { return 1, nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _, _ = TryExecute(s, ctx, PriorityNormal, fn)
		}
	})
}

func BenchmarkAcquire(b *testing.B) {
	s := New(WithCapacity(1_000_000))
	defer func() { _ = s.Close() }()

	b.ResetTimer()
	for b.Loop() {
		tok, _ := s.Acquire(PriorityNormal)
		tok.Release()
	}
}

func BenchmarkAcquire_Parallel(b *testing.B) {
	s := New(WithCapacity(1_000_000))
	defer func() { _ = s.Close() }()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tok, _ := s.Acquire(PriorityNormal)
			tok.Release()
		}
	})
}

func BenchmarkAllow(b *testing.B) {
	s := New(WithCapacity(1000))
	defer func() { _ = s.Close() }()

	b.ResetTimer()
	for b.Loop() {
		_ = s.Allow(PriorityNormal)
	}
}
