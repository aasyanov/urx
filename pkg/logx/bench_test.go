package logx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/aasyanov/urx/pkg/ctxx"
	"github.com/aasyanov/urx/pkg/errx"
)

func BenchmarkHandler_WithTrace(b *testing.B) {
	inner := slog.NewJSONHandler(io.Discard, nil)
	h := NewHandler(inner)
	logger := slog.New(h)
	ctx := ctxx.WithTrace(context.Background())
	b.ReportAllocs()
	for b.Loop() {
		logger.InfoContext(ctx, "request")
	}
}

func BenchmarkErr_ErrxError(b *testing.B) {
	xe := errx.New("AUTH", "UNAUTHORIZED", "expired")
	b.ReportAllocs()
	for b.Loop() {
		_ = Err(xe)
	}
}

func BenchmarkErr_PlainError(b *testing.B) {
	e := errors.New("plain")
	b.ReportAllocs()
	for b.Loop() {
		_ = Err(e)
	}
}

func BenchmarkFromContext(b *testing.B) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ctx := WithLogger(context.Background(), logger)
	b.ReportAllocs()
	for b.Loop() {
		_ = FromContext(ctx)
	}
}
