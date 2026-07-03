package circuitx

import (
	"context"
	"testing"
	"time"
)

// FuzzExecute drives a breaker through an arbitrary sequence of outcomes and
// asserts the state machine never panics and never reports an out-of-range
// state. Each input byte selects an action: even bytes succeed, odd bytes fail;
// the low bits also exercise SkipFailure and Trip so every controller path is
// reachable from fuzzed input.
func FuzzExecute(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 1, 0, 1})
	f.Add([]byte{1, 1, 1, 1, 1, 1})
	f.Add([]byte{2, 3, 5, 7, 11})
	f.Add([]byte{0xff, 0x00, 0x80, 0x01})

	f.Fuzz(func(t *testing.T, data []byte) {
		cb := New(
			WithMaxFailures(3),
			WithResetTimeout(time.Millisecond),
			WithHalfOpenMax(2),
		)
		ctx := context.Background()

		for _, bset := range data {
			fail := bset&1 == 1
			skip := bset&2 == 2
			trip := bset&4 == 4

			_, _ = Execute(cb, ctx, func(_ context.Context, cc CircuitController) (int, error) {
				// Controller accessors must never panic.
				_ = cc.State()
				_ = cc.Failures()
				_ = cc.MaxFailures()
				if skip {
					cc.SkipFailure()
				}
				if trip {
					cc.Trip()
				}
				if fail {
					return 0, errBoom
				}
				return 1, nil
			})

			// Invariant: state is always one of the three valid values.
			switch cb.State() {
			case Closed, Open, HalfOpen:
			default:
				t.Fatalf("invalid state: %v", cb.State())
			}

			// Invariant: consecutive failures never exceed the threshold while
			// Closed (reaching it trips to Open and clears the path).
			if cb.State() == Closed && cb.Failures() > cb.cfg.maxFailures {
				t.Fatalf("failures %d exceed max %d in Closed", cb.Failures(), cb.cfg.maxFailures)
			}
		}

		// Stats counters must be internally consistent (no negative wraparound).
		s := cb.Stats()
		if s.Trips > s.TotalFail+s.Successes+s.Rejected+uint64(len(data)) {
			t.Fatalf("trip count %d implausible for %d actions", s.Trips, len(data))
		}
	})
}
