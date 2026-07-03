package toutx

import (
	"context"
	"errors"
	"testing"
	"time"
)

// FuzzExecute drives [Execute] with fuzzed timeout and work durations,
// asserting the core invariants: Execute never panics, a function that
// completes within budget returns its value, and any error is one of the
// documented sentinels (or the function's own error).
func FuzzExecute(f *testing.F) {
	f.Add(int64(0), int64(0), int8(0))
	f.Add(int64(1_000_000), int64(0), int8(0))
	f.Add(int64(-1), int64(5_000_000), int8(0))
	f.Add(int64(1), int64(10_000_000), int8(0))
	f.Add(int64(1_000_000), int64(0), int8(1))

	f.Fuzz(func(t *testing.T, timeoutNanos, workNanos int64, flags int8) {
		// Bound the durations so the fuzzer cannot stall the test.
		timeout := clampDur(timeoutNanos, 20*time.Millisecond)
		work := clampDur(workNanos, 10*time.Millisecond)

		ctx := context.Background()
		if flags&1 != 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}

		sentinel := errors.New("fuzz-fn-error")
		fail := workNanos%2 == 0

		val, err := Execute(ctx, timeout,
			func(ctx context.Context, _ TimeoutController) (int, error) {
				if work > 0 {
					select {
					case <-time.After(work):
					case <-ctx.Done():
						return 0, ctx.Err()
					}
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
			// Function failed on its own — acceptable.
		case errors.Is(err, ErrDeadlineExceeded):
			// Work exceeded the budget — acceptable.
		case errors.Is(err, ErrCancelled):
			// Parent context was already cancelled — acceptable.
		case errors.Is(err, context.DeadlineExceeded):
			// Inner ctx fired before the function returned — acceptable.
		case errors.Is(err, context.Canceled):
			// Propagated ctx cancellation — acceptable.
		default:
			t.Fatalf("unexpected error class: %v", err)
		}
	})
}

// clampDur converts a fuzzed nanosecond count into a non-negative duration no
// larger than max.
func clampDur(nanos int64, maxDur time.Duration) time.Duration {
	if nanos <= 0 {
		return 0
	}
	d := time.Duration(nanos)
	if d > maxDur {
		return maxDur
	}
	return d
}
