package ctxx

import (
	"context"
	"testing"
)

func BenchmarkWithTrace_New(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		WithTrace(ctx)
	}
}

func BenchmarkWithTrace_Existing(b *testing.B) {
	ctx := WithTrace(context.Background())
	b.ReportAllocs()
	for b.Loop() {
		WithTrace(ctx)
	}
}

func BenchmarkWithTrace_Nil(b *testing.B) {
	ctx := nilCtx()
	b.ReportAllocs()
	for b.Loop() {
		WithTrace(ctx)
	}
}

func BenchmarkWithSpan(b *testing.B) {
	ctx := WithTrace(context.Background())
	b.ReportAllocs()
	for b.Loop() {
		WithSpan(ctx)
	}
}

func BenchmarkWithSpan_NoTrace(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		WithSpan(ctx)
	}
}

func BenchmarkTraceFromContext(b *testing.B) {
	ctx := WithTrace(context.Background())
	b.ReportAllocs()
	for b.Loop() {
		TraceFromContext(ctx)
	}
}

func BenchmarkTraceFromContext_Nil(b *testing.B) {
	ctx := nilCtx()
	b.ReportAllocs()
	for b.Loop() {
		TraceFromContext(ctx)
	}
}

func BenchmarkTraceFromContext_Empty(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		TraceFromContext(ctx)
	}
}

func BenchmarkMustTraceFromContext_Existing(b *testing.B) {
	ctx := WithTrace(context.Background())
	b.ReportAllocs()
	for b.Loop() {
		MustTraceFromContext(ctx)
	}
}

func BenchmarkMustTraceFromContext_Generate(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		MustTraceFromContext(ctx)
	}
}
