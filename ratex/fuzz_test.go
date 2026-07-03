package ratex

import (
	"context"
	"errors"
	"testing"
)

// FuzzAllowN drives a limiter built from fuzzed rate/burst parameters with a
// fuzzed sequence of AllowN calls, asserting the core invariants: New never
// panics, the limiter always honours its floors, AllowN never admits more than
// the bucket holds, and a failed AllowN consumes nothing.
func FuzzAllowN(f *testing.F) {
	f.Add(10.0, 20, 1)
	f.Add(0.0, 0, 5)
	f.Add(-3.0, -1, 100)
	f.Add(1e9, 1000, 0)

	f.Fuzz(func(t *testing.T, rate float64, burst, n int) {
		l := New(WithRate(rate), WithBurst(burst))

		if l.Rate() < minRate {
			t.Fatalf("rate %v below floor %v", l.Rate(), minRate)
		}
		if l.Burst() < minBurst {
			t.Fatalf("burst %d below floor %d", l.Burst(), minBurst)
		}

		before := l.Tokens()
		ok := l.AllowN(n)
		after := l.Tokens()

		if !ok && after < before-0.0001 {
			t.Fatalf("failed AllowN consumed tokens: before=%v after=%v", before, after)
		}
		if after < 0 {
			t.Fatalf("token balance went negative: %v", after)
		}
		if after > float64(l.Burst())+0.0001 {
			t.Fatalf("token balance %v exceeds burst %d", after, l.Burst())
		}
	})
}

// FuzzExecute drives [Execute] with fuzzed limiter parameters and a callback
// that either succeeds, fails, or requests a token refund, asserting that
// Execute never panics and only returns documented error classes.
func FuzzExecute(f *testing.F) {
	f.Add(10.0, 5, false, false)
	f.Add(1.0, 1, true, false)
	f.Add(1e9, 1000, false, true)

	sentinel := errors.New("fuzz-fn-error")

	f.Fuzz(func(t *testing.T, rate float64, burst int, fail, skip bool) {
		l := New(WithRate(rate), WithBurst(burst))

		val, err := Execute(l, context.Background(),
			func(_ context.Context, rc RateController) (int, error) {
				if skip {
					rc.SkipToken()
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

// FuzzTryExecute drives [TryExecute] with fuzzed limiter parameters, asserting
// it never panics and only returns documented error classes.
func FuzzTryExecute(f *testing.F) {
	f.Add(10.0, 5)
	f.Add(1.0, 1)
	f.Add(1e9, 1000)

	f.Fuzz(func(t *testing.T, rate float64, burst int) {
		l := New(WithRate(rate), WithBurst(burst))

		ok, val, err := TryExecute(l, context.Background(),
			func(_ context.Context, rc RateController) (int, error) {
				if rc.Tokens() < 0 {
					t.Fatalf("negative tokens in controller: %v", rc.Tokens())
				}
				return 7, nil
			})

		switch {
		case err == nil && ok:
			if val != 7 {
				t.Fatalf("admitted but value = %d, want 7", val)
			}
		case err == nil && !ok:
			// No token available — acceptable.
		case errors.Is(err, ErrCancelled):
			// Context was already done — acceptable.
		default:
			t.Fatalf("unexpected error class: %v", err)
		}
	})
}
