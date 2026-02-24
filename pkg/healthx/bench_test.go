package healthx

import (
	"context"
	"net/http/httptest"
	"testing"
)

func BenchmarkLiveness(b *testing.B) {
	c := New()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		c.Liveness(ctx)
	}
}

func BenchmarkReadiness_3Checks(b *testing.B) {
	c := New()
	for _, name := range []string{"db", "cache", "queue"} {
		c.Register(name, func(ctx context.Context) error { return nil })
	}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		c.Readiness(ctx)
	}
}

func BenchmarkLiveHandler(b *testing.B) {
	c := New()
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/healthz", nil)
		c.LiveHandler().ServeHTTP(rec, req)
	}
}

func BenchmarkReadyHandler(b *testing.B) {
	c := New()
	c.Register("db", func(ctx context.Context) error { return nil })
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/readyz", nil)
		c.ReadyHandler().ServeHTTP(rec, req)
	}
}
