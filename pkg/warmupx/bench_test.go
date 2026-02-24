package warmupx

import (
	"testing"
	"time"
)

func BenchmarkAllow(b *testing.B) {
	w := New(WithMinCapacity(0.5), WithMaxCapacity(0.5), WithDuration(1*time.Hour))
	w.Start()
	defer w.Stop()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w.Allow()
		}
	})
}

func BenchmarkAllowOrError_Allowed(b *testing.B) {
	w := New(WithMinCapacity(1.0), WithMaxCapacity(1.0), WithDuration(1*time.Hour))
	w.Start()
	defer w.Stop()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w.AllowOrError()
		}
	})
}

func BenchmarkAllowOrError_Rejected(b *testing.B) {
	w := New(WithMinCapacity(0.001), WithMaxCapacity(0.001), WithDuration(1*time.Hour))
	w.Start()
	defer w.Stop()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w.AllowOrError()
		}
	})
}

func BenchmarkCapacity(b *testing.B) {
	w := New(WithDuration(1 * time.Hour))
	w.Start()
	defer w.Stop()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w.Capacity()
		}
	})
}

func BenchmarkProgress(b *testing.B) {
	w := New(WithDuration(1 * time.Hour))
	w.Start()
	defer w.Stop()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w.Progress()
		}
	})
}

func BenchmarkStats(b *testing.B) {
	w := New(WithDuration(1 * time.Hour))
	w.Start()
	defer w.Stop()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w.Stats()
		}
	})
}

func BenchmarkMaxRequests(b *testing.B) {
	w := New(WithMinCapacity(0.5), WithDuration(1*time.Hour))
	w.Start()
	defer w.Stop()

	b.ReportAllocs()
	for b.Loop() {
		w.MaxRequests(100)
	}
}
