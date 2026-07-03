package retryx

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// FuzzDo drives [Do] with a fuzzed attempt budget and failure count, asserting
// the core invariants: Do never panics, fn is invoked at most maxAttempts
// times, and the outcome matches whether a success was reachable within the
// budget.
func FuzzDo(f *testing.F) {
	f.Add(3, 0)
	f.Add(1, 5)
	f.Add(5, 2)
	f.Add(0, 0)
	f.Add(-1, 3)

	f.Fuzz(func(t *testing.T, maxAttempts, failCount int) {
		// Bound the inputs so the loop stays fast.
		if maxAttempts > 50 {
			maxAttempts = 50
		}
		if failCount < 0 {
			failCount = 0
		}
		if failCount > 100 {
			failCount = 100
		}

		transient := errors.New("transient")
		var calls atomic.Int64

		_, err := Do(context.Background(), func(context.Context, RetryController) (int, error) {
			if calls.Add(1) <= int64(failCount) {
				return 0, transient
			}
			return 1, nil
		},
			WithMaxAttempts(maxAttempts),
			WithBackoff(time.Nanosecond),
			WithMaxBackoff(time.Nanosecond),
			WithJitter(false),
		)

		effMax := maxAttempts
		if effMax < minAttempts {
			effMax = minAttempts
		}

		if got := calls.Load(); got > int64(effMax) {
			t.Fatalf("fn called %d times, exceeds budget %d", got, effMax)
		}

		// A success was reachable iff the budget outlasts the failure streak.
		if failCount < effMax {
			if err != nil {
				t.Fatalf("expected success within budget (max=%d, fails=%d), got %v", effMax, failCount, err)
			}
		} else if !errors.Is(err, ErrExhausted) {
			t.Fatalf("expected ErrExhausted (max=%d, fails=%d), got %v", effMax, failCount, err)
		}
	})
}
