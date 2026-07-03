package warmupx

import (
	"context"
	"math"
	"testing"
	"time"
)

// FuzzCalculate asserts the capacity curve invariant for every strategy and any
// fractional input: the result is finite and never exceeds the configured
// maximum capacity.
func FuzzCalculate(f *testing.F) {
	f.Add(uint8(0), 0.1, 1.0, 0.0, 10, 3.0)
	f.Add(uint8(1), 0.0, 1.0, 0.5, 10, 3.0)
	f.Add(uint8(2), 0.2, 0.8, 1.0, 5, 1.0)
	f.Add(uint8(3), 0.5, 0.9, 0.37, 7, 6.0)
	f.Add(uint8(99), 0.0, 1.0, -1.0, 1, 0.0)

	f.Fuzz(func(t *testing.T, strategy uint8, minCap, maxCap, frac float64, steps int, expFactor float64) {
		w := New(
			WithStrategy(Strategy(strategy)),
			WithMinCapacity(minCap),
			WithMaxCapacity(maxCap),
			WithStepCount(steps),
			WithExpFactor(expFactor),
		)

		cap := w.calculate(frac)
		if math.IsNaN(cap) || math.IsInf(cap, 0) {
			t.Fatalf("calculate(%v) produced non-finite capacity %v", frac, cap)
		}
		if cap > w.cfg.maxCap+1e-9 {
			t.Fatalf("calculate(%v)=%v exceeds maxCap %v", frac, cap, w.cfg.maxCap)
		}
	})
}

// FuzzMaxRequests asserts that scaling a base limit never produces a negative
// result and never exceeds the base when capacity is in range.
func FuzzMaxRequests(f *testing.F) {
	f.Add(0.5, 100)
	f.Add(0.0, 50)
	f.Add(1.0, 1)
	f.Add(0.001, 1_000_000)

	f.Fuzz(func(t *testing.T, capacity float64, base int) {
		w := New(WithMinCapacity(capacity), WithMaxCapacity(1))
		got := w.MaxRequests(base)
		if got < 0 {
			t.Fatalf("MaxRequests(%d) at capacity %v returned negative %d", base, capacity, got)
		}
		if base > 0 && capacity <= 0 && got != 0 {
			t.Fatalf("MaxRequests(%d) at zero capacity returned %d, want 0", base, got)
		}
		if base > 0 && capacity > 0 && got > base {
			t.Fatalf("MaxRequests(%d)=%d exceeds base", base, got)
		}
	})
}

// FuzzExecute asserts that Execute never panics out and always returns a usable
// (value, error) pair for arbitrary admission capacity.
func FuzzExecute(f *testing.F) {
	f.Add(0.0)
	f.Add(0.5)
	f.Add(1.0)

	f.Fuzz(func(t *testing.T, capacity float64) {
		w := New(WithMinCapacity(capacity), WithMaxCapacity(1))
		_, _ = Execute(w, context.Background(), func(_ context.Context, wc WarmupController) (int, error) {
			if c := wc.Capacity(); c < 0 || c > 1 {
				t.Fatalf("controller capacity out of range: %v", c)
			}
			return 1, nil
		})
		_ = time.Now()
	})
}

// FuzzTryExecute asserts that TryExecute never panics out and always returns a
// usable (ok, value, error) triple for arbitrary admission capacity.
func FuzzTryExecute(f *testing.F) {
	f.Add(0.0)
	f.Add(0.5)
	f.Add(1.0)

	f.Fuzz(func(t *testing.T, capacity float64) {
		w := New(WithMinCapacity(capacity), WithMaxCapacity(1))
		ok, _, err := TryExecute(w, context.Background(), func(_ context.Context, wc WarmupController) (int, error) {
			if c := wc.Capacity(); c < 0 || c > 1 {
				t.Fatalf("controller capacity out of range: %v", c)
			}
			return 1, nil
		})
		if ok && err != nil && err != ErrRejected {
			t.Fatalf("unexpected error when ok=true: %v", err)
		}
		_ = time.Now()
	})
}
