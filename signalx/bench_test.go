package signalx

import (
	"context"
	"testing"
)

func BenchmarkWait_NoHooks(b *testing.B) {
	ResetHooks()

	b.ResetTimer()
	for b.Loop() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = Wait(ctx)
	}
}

func BenchmarkWait_SingleHook(b *testing.B) {
	ResetHooks()
	hook := func(context.Context) {}

	b.ResetTimer()
	for b.Loop() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = Wait(ctx, hook)
	}
}

func BenchmarkWait_TenHooks(b *testing.B) {
	ResetHooks()
	hooks := make([]func(context.Context), 10)
	for i := range hooks {
		hooks[i] = func(context.Context) {}
	}

	b.ResetTimer()
	for b.Loop() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = Wait(ctx, hooks...)
	}
}

func BenchmarkOnShutdown(b *testing.B) {
	ResetHooks()
	hook := func(context.Context) {}

	b.ResetTimer()
	for b.Loop() {
		OnShutdown(hook)
	}
	b.StopTimer()
	ResetHooks()
}

func BenchmarkRunHook(b *testing.B) {
	ctx := context.Background()
	hook := func(context.Context) {}

	b.ResetTimer()
	for b.Loop() {
		_ = runHook(ctx, hook)
	}
}

func BenchmarkWait_SingleHook_Parallel(b *testing.B) {
	ResetHooks()
	hook := func(context.Context) {}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = Wait(ctx, hook)
		}
	})
}
