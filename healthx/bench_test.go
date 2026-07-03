package healthx

import (
	"context"
	"testing"
)

func BenchmarkLiveness(b *testing.B) {
	c := New()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = c.Liveness(ctx)
	}
}

func BenchmarkReadiness_NoChecks(b *testing.B) {
	c := New()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = c.Readiness(ctx)
	}
}

func BenchmarkReadiness_OneCheck(b *testing.B) {
	c := New()
	c.Register("a", func(context.Context) error { return nil })
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = c.Readiness(ctx)
	}
}

func BenchmarkReadiness_TenChecks(b *testing.B) {
	c := New()
	for i := range 10 {
		c.Register(string(rune('a'+i)), func(context.Context) error { return nil })
	}
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = c.Readiness(ctx)
	}
}

func BenchmarkReadiness_Parallel(b *testing.B) {
	c := New()
	for i := range 4 {
		c.Register(string(rune('a'+i)), func(context.Context) error { return nil })
	}
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = c.Readiness(ctx)
		}
	})
}
