package quotax

import (
	"context"
	"testing"
)

func BenchmarkAllow_Hit(b *testing.B) {
	q := New(WithRate(1e9), WithBurst(1e9))
	defer q.Close()
	q.Allow("k") // create the bucket so the loop hits the fast path

	b.ResetTimer()
	for b.Loop() {
		_ = q.Allow("k")
	}
}

func BenchmarkAllow_Hit_Parallel(b *testing.B) {
	q := New(WithRate(1e9), WithBurst(1e9))
	defer q.Close()
	q.Allow("k")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = q.Allow("k")
		}
	})
}

func BenchmarkAllow_DistinctKeys_Parallel(b *testing.B) {
	q := New(WithRate(1e9), WithBurst(1e9))
	defer q.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var i uint64
		for pb.Next() {
			i++
			q.Allow(keyFor(i % 1024))
		}
	})
}

func BenchmarkExecute(b *testing.B) {
	q := New(WithRate(1e9), WithBurst(1e9))
	defer q.Close()
	q.Allow("k")
	ctx := context.Background()
	fn := func(context.Context, QuotaController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _ = Execute(q, ctx, "k", fn)
	}
}

func BenchmarkExecute_Parallel(b *testing.B) {
	q := New(WithRate(1e9), WithBurst(1e9))
	defer q.Close()
	q.Allow("k")
	ctx := context.Background()
	fn := func(context.Context, QuotaController) (int, error) { return 1, nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Execute(q, ctx, "k", fn)
		}
	})
}

func BenchmarkTryExecute(b *testing.B) {
	q := New(WithRate(1e9), WithBurst(1e9))
	defer q.Close()
	q.Allow("k")
	ctx := context.Background()
	fn := func(context.Context, QuotaController) (int, error) { return 1, nil }

	b.ResetTimer()
	for b.Loop() {
		_, _, _ = TryExecute(q, ctx, "k", fn)
	}
}

// keyFor builds a small set of distinct keys without allocating in the loop by
// precomputing a fixed table.
func keyFor(n uint64) string { return benchKeys[n%uint64(len(benchKeys))] }

var benchKeys = func() []string {
	const n = 1024
	ks := make([]string, n)
	for i := range ks {
		ks[i] = "key-" + string(rune('A'+i%26)) + string(rune('a'+i/26%26))
	}
	return ks
}()
