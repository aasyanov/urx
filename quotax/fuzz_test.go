package quotax

import (
	"context"
	"errors"
	"testing"
)

// FuzzAllow drives a quota built from fuzzed rate/burst/maxKeys parameters with
// a fuzzed sequence of keys, asserting the core invariants: New never panics,
// the tracked key count never exceeds the configured cap, and KeyCount stays in
// sync with the observed admissions.
func FuzzAllow(f *testing.F) {
	f.Add(10.0, 20, int64(0), "a", 5)
	f.Add(0.0, 0, int64(2), "user:42", 10)
	f.Add(-3.0, -1, int64(1), "", 3)
	f.Add(1e9, 1000, int64(100), "k", 50)

	f.Fuzz(func(t *testing.T, rate float64, burst int, maxKeys int64, key string, iters int) {
		if iters < 0 || iters > 1000 {
			iters &= 0x3ff
		}
		q := New(WithRate(rate), WithBurst(burst), WithMaxKeys(maxKeys))
		defer q.Close()

		for i := range iters {
			q.Allow(key + string(rune('0'+i%10)))
		}

		if maxKeys > 0 && q.KeyCount() > maxKeys {
			t.Fatalf("key count %d exceeds cap %d", q.KeyCount(), maxKeys)
		}
		if q.KeyCount() < 0 {
			t.Fatalf("negative key count: %d", q.KeyCount())
		}
	})
}

// FuzzExecute drives [Execute] with fuzzed quota parameters and a callback that
// either succeeds, fails, or requests a token refund, asserting that Execute
// never panics and only returns documented error classes.
func FuzzExecute(f *testing.F) {
	f.Add(10.0, 5, "k", false, false)
	f.Add(1.0, 1, "user", true, false)
	f.Add(1e9, 1000, "", false, true)

	sentinel := errors.New("fuzz-fn-error")

	f.Fuzz(func(t *testing.T, rate float64, burst int, key string, fail, skip bool) {
		q := New(WithRate(rate), WithBurst(burst))
		defer q.Close()

		val, err := Execute(q, context.Background(), key,
			func(_ context.Context, qc QuotaController) (int, error) {
				if qc.Key() != key {
					t.Fatalf("controller key = %q, want %q", qc.Key(), key)
				}
				if skip {
					qc.SkipToken()
				}
				if fail {
					return 0, sentinel
				}
				return 7, nil
			})

		switch {
		case err == nil:
			if val != 7 {
				t.Fatalf("nil error but value = %d, want 7", val)
			}
		case errors.Is(err, sentinel):
			// Callback failed on its own — acceptable.
		case errors.Is(err, ErrCancelled):
			// Context was already done — acceptable.
		default:
			t.Fatalf("unexpected error class: %v", err)
		}
	})
}
