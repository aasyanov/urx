package signalx

import (
	"context"
	"testing"
	"time"
)

var benchSinkCtx context.Context

func BenchmarkContext(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		ctx, cancel := Context(context.Background())
		cancel()
		benchSinkCtx = ctx
	}
}

func BenchmarkWait_NoHooks(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Wait(ctx, time.Second)
	}
}

func BenchmarkWait_3Hooks(b *testing.B) {
	nop := func(ctx context.Context) {}
	b.ReportAllocs()
	for b.Loop() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Wait(ctx, time.Second, nop, nop, nop)
	}
}

func BenchmarkOnShutdown(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		ResetHooks()
		OnShutdown(func(ctx context.Context) {})
	}
}
