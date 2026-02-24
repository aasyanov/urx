package cronx

import (
	"context"
	"testing"
	"time"
)

func BenchmarkScheduler_Stats(b *testing.B) {
	s := New()
	_ = AddJob(s, "bench", time.Hour, func(context.Context, JobController) (int, error) {
		return 1, nil
	})

	b.ReportAllocs()
	for b.Loop() {
		_ = s.Stats()
	}
}

func BenchmarkScheduler_AddJob(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		s := New()
		_ = AddJob(s, "j", time.Hour, func(context.Context, JobController) (int, error) {
			return 1, nil
		})
	}
}

func BenchmarkScheduler_HealthCheck(b *testing.B) {
	s := New()
	_ = AddJob(s, "h", time.Hour, func(context.Context, JobController) (int, error) {
		return 1, nil
	})
	b.ReportAllocs()
	for b.Loop() {
		_ = s.HealthCheck(context.Background())
	}
}
