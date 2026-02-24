package busx

import (
	"context"
	"testing"
)

var benchSink error

func nopHandler(_ context.Context, _ string, _ any) {}

// --- BenchmarkSubscribe ---

func BenchmarkSubscribe(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		bus := New()
		_, _ = bus.Subscribe("evt", nopHandler)
	}
}

// --- BenchmarkPublish_NoSubscribers ---

func BenchmarkPublish_NoSubscribers(b *testing.B) {
	bus := New()

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchSink = bus.Publish(context.Background(), "evt", nil)
	}
}

// --- BenchmarkPublish_1Handler ---

func BenchmarkPublish_1Handler(b *testing.B) {
	bus := New()
	bus.Subscribe("evt", nopHandler)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchSink = bus.Publish(context.Background(), "evt", nil)
	}
}

// --- BenchmarkPublish_10Handlers ---

func BenchmarkPublish_10Handlers(b *testing.B) {
	bus := New()
	for range 10 {
		bus.Subscribe("evt", nopHandler)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchSink = bus.Publish(context.Background(), "evt", nil)
	}
}

// --- BenchmarkPublish_100Handlers ---

func BenchmarkPublish_100Handlers(b *testing.B) {
	bus := New()
	for range 100 {
		bus.Subscribe("evt", nopHandler)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchSink = bus.Publish(context.Background(), "evt", nil)
	}
}

// --- BenchmarkUnsubscribe ---

func BenchmarkUnsubscribe(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		bus := New()
		id, _ := bus.Subscribe("evt", nopHandler)
		bus.Unsubscribe(id)
	}
}

// --- BenchmarkPublish_WithPanic ---

func BenchmarkPublish_WithPanic(b *testing.B) {
	bus := New()
	bus.Subscribe("evt", func(context.Context, string, any) {
		panic("bench")
	})

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchSink = bus.Publish(context.Background(), "evt", nil)
	}
}

// --- BenchmarkConcurrentPublish ---

func BenchmarkConcurrentPublish(b *testing.B) {
	bus := New()
	bus.Subscribe("evt", nopHandler)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			benchSink = bus.Publish(context.Background(), "evt", nil)
		}
	})
}

// --- BenchmarkConcurrentPublish_Contention ---

func BenchmarkConcurrentPublish_Contention(b *testing.B) {
	bus := New()
	for range 10 {
		bus.Subscribe("evt", nopHandler)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			benchSink = bus.Publish(context.Background(), "evt", nil)
		}
	})
}
