package hedgex

import (
	"context"
	"errors"
	"testing"
	"time"
)

// FuzzExecute drives Execute with fuzzed configuration and a function whose
// success/failure is controlled by the input. The oracle: Execute must always
// terminate (the test timeout guards against a hang), never panic, and return
// a well-formed result — either a nil error with the function's value or a
// non-nil error when every copy failed.
func FuzzExecute(f *testing.F) {
	f.Add(3, int64(time.Millisecond), int64(time.Second), true)
	f.Add(1, int64(0), int64(0), false)
	f.Add(0, int64(-1), int64(-1), true)
	f.Add(10, int64(time.Microsecond), int64(time.Microsecond), false)

	f.Fuzz(func(t *testing.T, parallel int, delayNs, maxDelayNs int64, succeed bool) {
		// Normalize inputs into a small positive range so every case stays fast
		// and avoids falling back to the package defaults (100 ms / 1 s), which
		// can make fuzz iterations occasionally hit the watchdog on slow CI.
		parallel = fuzzParallel(parallel, 16)
		delay := fuzzDuration(delayNs, 5*time.Millisecond)
		maxDelay := fuzzDuration(maxDelayNs, 10*time.Millisecond)

		h := New(
			WithMaxParallel(parallel),
			WithDelay(delay),
			WithMaxDelay(maxDelay),
		)

		// The hedger must never be unusable regardless of input.
		if h.MaxParallel() < 1 {
			t.Fatalf("maxParallel must be floored to >= 1, got %d", h.MaxParallel())
		}
		if h.MaxDelay() < h.Delay() {
			t.Fatalf("maxDelay %v must be >= delay %v", h.MaxDelay(), h.Delay())
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		got, err := Execute(h, ctx, func(context.Context, HedgeController) (int, error) {
			if succeed {
				return 42, nil
			}
			return 0, errSentinel
		})
		if succeed {
			if err != nil {
				t.Fatalf("expected success, got error: %v", err)
			}
			if got != 42 {
				t.Fatalf("expected value 42, got %d", got)
			}
			return
		}
		if err == nil {
			t.Fatalf("expected failure, got success with value %d", got)
		}
	})
}

// FuzzDelays asserts the delay schedule is always non-decreasing and correctly
// sized regardless of the delay/maxDelay/count combination.
func FuzzDelays(f *testing.F) {
	f.Add(int64(time.Millisecond), int64(time.Second), 4)
	f.Add(int64(0), int64(0), 1)
	f.Add(int64(time.Second), int64(time.Millisecond), 8)

	f.Fuzz(func(t *testing.T, delayNs, maxDelayNs int64, count int) {
		if count > 64 {
			count = 64
		}
		delay := time.Duration(delayNs % int64(time.Hour))
		maxDelay := time.Duration(maxDelayNs % int64(time.Hour))

		h := New(WithDelay(delay), WithMaxDelay(maxDelay), WithMaxParallel(count))
		ds := h.delays(count)

		if count <= 1 {
			if ds != nil {
				t.Fatalf("expected nil delays for count=%d, got %v", count, ds)
			}
			return
		}
		if len(ds) != count-1 {
			t.Fatalf("expected %d delays, got %d", count-1, len(ds))
		}
		for i := 1; i < len(ds); i++ {
			if ds[i] < ds[i-1] {
				t.Fatalf("delays not monotonic at %d: %v < %v", i, ds[i], ds[i-1])
			}
		}
	})
}

// FuzzExecuteMulti drives ExecuteMulti with fuzzed configuration and a mix of
// nil and non-nil backends. The oracle matches [FuzzExecute]: no panic, always
// terminates, and success/failure matches the succeed flag.
func FuzzExecuteMulti(f *testing.F) {
	f.Add(3, int64(time.Millisecond), int64(time.Second), true, byte(0))
	f.Add(2, int64(0), int64(0), false, byte(1))
	f.Add(4, int64(time.Microsecond), int64(time.Millisecond), true, byte(2))
	f.Add(0, int64(-1), int64(-1), true, byte(1))

	f.Fuzz(func(t *testing.T, parallel int, delayNs, maxDelayNs int64, succeed bool, nilPattern byte) {
		parallel = fuzzParallel(parallel, 8)
		delay := fuzzDuration(delayNs, 5*time.Millisecond)
		maxDelay := fuzzDuration(maxDelayNs, 10*time.Millisecond)

		h := New(
			WithMaxParallel(parallel),
			WithDelay(delay),
			WithMaxDelay(maxDelay),
		)

		fns := make([]HedgeFunc[int], parallel)
		for i := range fns {
			if nilPattern&(1<<uint(i%8)) != 0 {
				continue
			}
			fns[i] = func(context.Context, HedgeController) (int, error) {
				if succeed {
					return 42, nil
				}
				return 0, errSentinel
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		got, err := ExecuteMulti(h, ctx, fns)
		if !anyNonNil(fns) {
			if err == nil || !errors.Is(err, ErrNilFunc) {
				t.Fatalf("expected ErrNilFunc for all-nil slice, got %v", err)
			}
			return
		}
		if succeed {
			if err != nil {
				t.Fatalf("expected success, got error: %v", err)
			}
			if got != 42 {
				t.Fatalf("expected value 42, got %d", got)
			}
			return
		}
		if err == nil {
			t.Fatalf("expected failure, got success with value %d", got)
		}
	})
}

func fuzzParallel(n, max int) int {
	if max < 1 {
		return 1
	}
	return int(uint(n)%uint(max)) + 1
}

func fuzzDuration(n int64, max time.Duration) time.Duration {
	const min = time.Microsecond
	if max <= min {
		return min
	}
	span := int64(max - min)
	d := n % span
	if d < 0 {
		d += span
	}
	return min + time.Duration(d)
}
