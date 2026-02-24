package panix

import (
	"context"
	"testing"

	"github.com/aasyanov/urx/pkg/ctxx"
)

func BenchmarkSafe_NoPanic(b *testing.B) {
	ctx := ctxx.WithTrace(context.Background())
	fn := func(ctx context.Context) (struct{}, error) { return struct{}{}, nil }
	b.ReportAllocs()
	for b.Loop() {
		_, _ = Safe[struct{}](ctx, "op", fn)
	}
}

func BenchmarkSafe_Panic(b *testing.B) {
	ctx := ctxx.WithTrace(context.Background())
	fn := func(ctx context.Context) (struct{}, error) { panic("boom") }
	b.ReportAllocs()
	for b.Loop() {
		_, _ = Safe[struct{}](ctx, "op", fn)
	}
}

func BenchmarkSafe_NilCtx(b *testing.B) {
	ctx := nilCtx()
	fn := func(ctx context.Context) (struct{}, error) { return struct{}{}, nil }
	b.ReportAllocs()
	for b.Loop() {
		_, _ = Safe[struct{}](ctx, "op", fn)
	}
}

func BenchmarkSafeGo_NoPanic(b *testing.B) {
	ctx := ctxx.WithTrace(context.Background())
	fn := func(ctx context.Context) {}
	b.ReportAllocs()
	for b.Loop() {
		SafeGo(ctx, "op", fn)
	}
}

func BenchmarkSafeGo_WithOnError(b *testing.B) {
	ctx := ctxx.WithTrace(context.Background())
	fn := func(ctx context.Context) {}
	handler := WithOnError(func(ctx context.Context, err error) {})
	b.ReportAllocs()
	for b.Loop() {
		SafeGo(ctx, "op", fn, handler)
	}
}

func BenchmarkWrap_NoPanic(b *testing.B) {
	ctx := ctxx.WithTrace(context.Background())
	wrapped := Wrap[struct{}](func(ctx context.Context) (struct{}, error) { return struct{}{}, nil }, "op")
	b.ReportAllocs()
	for b.Loop() {
		_, _ = wrapped(ctx)
	}
}

func BenchmarkWrap_Panic(b *testing.B) {
	ctx := ctxx.WithTrace(context.Background())
	wrapped := Wrap[struct{}](func(ctx context.Context) (struct{}, error) { panic("boom") }, "op")
	b.ReportAllocs()
	for b.Loop() {
		_, _ = wrapped(ctx)
	}
}
