package ratex

import (
	"context"
	"testing"
)

// --- BenchmarkAllow ---

func BenchmarkAllow(b *testing.B) {
	rl := New(WithRate(1000000), WithBurst(1000000))
	b.ReportAllocs()
	for b.Loop() {
		rl.Allow()
	}
}

// --- BenchmarkAllowN ---

func BenchmarkAllowN(b *testing.B) {
	rl := New(WithRate(1000000), WithBurst(1000000))
	b.ReportAllocs()
	for b.Loop() {
		rl.AllowN(5)
	}
}

// --- BenchmarkWait ---

func BenchmarkWait(b *testing.B) {
	rl := New(WithRate(1000000), WithBurst(1000000))
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		rl.Wait(ctx)
	}
}

// --- BenchmarkTokens ---

func BenchmarkTokens(b *testing.B) {
	rl := New()
	b.ReportAllocs()
	for b.Loop() {
		rl.Tokens()
	}
}

// --- BenchmarkAllow_Parallel ---

func BenchmarkAllow_Parallel(b *testing.B) {
	rl := New(WithRate(1000000), WithBurst(1000000))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rl.Allow()
		}
	})
}
