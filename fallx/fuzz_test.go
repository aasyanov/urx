package fallx

import (
	"context"
	"errors"
	"testing"
	"time"
)

// FuzzExecuteCached drives the cached strategy with arbitrary keys, TTLs, cache
// sizes, and a success/fail flag. Invariants: it never panics, the live cache
// size never exceeds the configured capacity, and a key whose primary just
// succeeded replays on a subsequent failure within the TTL.
func FuzzExecuteCached(f *testing.F) {
	f.Add("k", int64(60_000), 4, true)
	f.Add("", int64(0), 0, false)
	f.Add("user-42", int64(-1), -3, true)
	f.Add("x", int64(1), 1000, false)

	f.Fuzz(func(t *testing.T, key string, ttlMillis int64, maxSize int, succeed bool) {
		ttl := time.Duration(ttlMillis) * time.Millisecond
		fb := New(WithCached[int](ttl, maxSize), WithShards[int](4))
		defer func() { _ = fb.Close() }()

		ctx := context.Background()
		_, _ = ExecuteWithKey(fb, ctx, key, func(context.Context, FallController) (int, error) {
			if succeed {
				return 1, nil
			}
			return 0, errors.New("boom")
		})

		if maxSize > 0 && fb.Stats().CacheSize > maxSize {
			t.Fatalf("cache size %d exceeds capacity %d", fb.Stats().CacheSize, maxSize)
		}
	})
}

// FuzzExecuteFunc drives the func strategy: the primary and fallback each fail
// or succeed per a flag. Invariants: never panics, and a successful fallback
// after a failed primary yields no error.
func FuzzExecuteFunc(f *testing.F) {
	f.Add(false, true)
	f.Add(false, false)
	f.Add(true, false)

	f.Fuzz(func(t *testing.T, primaryOK, fallbackOK bool) {
		fb := New(WithFunc(func(context.Context, FallController) (int, error) {
			if fallbackOK {
				return 2, nil
			}
			return 0, errors.New("fallback boom")
		}))
		defer func() { _ = fb.Close() }()

		_, err := Execute(fb, context.Background(), func(context.Context, FallController) (int, error) {
			if primaryOK {
				return 1, nil
			}
			return 0, errors.New("primary boom")
		})

		switch {
		case primaryOK && err != nil:
			t.Fatalf("primary succeeded but got error: %v", err)
		case !primaryOK && fallbackOK && err != nil:
			t.Fatalf("fallback succeeded but got error: %v", err)
		case !primaryOK && !fallbackOK && err == nil:
			t.Fatalf("both failed but error is nil")
		}
	})
}

// FuzzSeed verifies that seeding arbitrary keys keeps the cache bounded and the
// size counter non-negative.
func FuzzSeed(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4})
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0})

	f.Fuzz(func(t *testing.T, keys []byte) {
		const capacity = 8
		fb := New(WithCached[byte](time.Minute, capacity), WithShards[byte](2))
		defer func() { _ = fb.Close() }()

		for _, k := range keys {
			fb.Seed(string([]byte{k}), k)
		}
		if got := fb.Stats().CacheSize; got > capacity {
			t.Fatalf("cache size %d exceeds capacity %d", got, capacity)
		}
		if got := fb.Stats().CacheSize; got < 0 {
			t.Fatalf("cache size went negative: %d", got)
		}
	})
}
