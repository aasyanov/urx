package panix

import (
	"context"
	"errors"
	"testing"
)

func BenchmarkSafe_NoPanic(b *testing.B) {
	fn := func() (int, error) { return 42, nil }
	for b.Loop() {
		Safe("bench.op", fn)
	}
}

func BenchmarkSafe_NoPanic_Error(b *testing.B) {
	sentinel := errors.New("err")
	fn := func() (int, error) { return 0, sentinel }
	for b.Loop() {
		Safe("bench.op", fn)
	}
}

func BenchmarkSafe_Panic(b *testing.B) {
	fn := func() (int, error) { panic("boom") }
	for b.Loop() {
		Safe("bench.op", fn)
	}
}

func BenchmarkSafeVoid_NoPanic(b *testing.B) {
	fn := func() error { return nil }
	for b.Loop() {
		SafeVoid("bench.op", fn)
	}
}

func BenchmarkSafeVoid_Panic(b *testing.B) {
	fn := func() error { panic("boom") }
	for b.Loop() {
		SafeVoid("bench.op", fn)
	}
}

func BenchmarkSafe_NoPanic_Parallel(b *testing.B) {
	fn := func() (int, error) { return 42, nil }
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			Safe("bench.op", fn)
		}
	})
}

func BenchmarkSafe_Panic_Parallel(b *testing.B) {
	fn := func() (int, error) { panic("boom") }
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			Safe("bench.op", fn)
		}
	})
}

func BenchmarkSafeVoid_NoPanic_Parallel(b *testing.B) {
	fn := func() error { return nil }
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			SafeVoid("bench.op", fn)
		}
	})
}

func BenchmarkWrap_NoPanic(b *testing.B) {
	wrapped := Wrap("bench.op", func() (int, error) { return 42, nil })
	for b.Loop() {
		wrapped()
	}
}

func BenchmarkWrap_Panic(b *testing.B) {
	wrapped := Wrap("bench.op", func() (int, error) { panic("boom") })
	for b.Loop() {
		wrapped()
	}
}

func BenchmarkWrapVoid_NoPanic(b *testing.B) {
	wrapped := WrapVoid("bench.op", func() error { return nil })
	for b.Loop() {
		wrapped()
	}
}

func BenchmarkWrapVoid_Panic(b *testing.B) {
	wrapped := WrapVoid("bench.op", func() error { panic("boom") })
	for b.Loop() {
		wrapped()
	}
}

func BenchmarkSafeGo_NoPanic(b *testing.B) {
	ctx := context.Background()
	done := make(chan struct{}, 1)
	fn := func(_ context.Context) {
		done <- struct{}{}
	}
	b.ResetTimer()
	for b.Loop() {
		SafeGo(ctx, "bench.op", fn, nil)
		<-done
	}
}

func BenchmarkSafeGo_Panic(b *testing.B) {
	ctx := context.Background()
	done := make(chan struct{}, 1)
	fn := func(_ context.Context) {
		panic("boom")
	}
	onError := func(_ context.Context, _ error) {
		done <- struct{}{}
	}
	b.ResetTimer()
	for b.Loop() {
		SafeGo(ctx, "bench.op", fn, onError)
		<-done
	}
}

func BenchmarkCaptureStack(b *testing.B) {
	for b.Loop() {
		captureStack()
	}
}
