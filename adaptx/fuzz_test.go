package adaptx

import (
	"context"
	"errors"
	"testing"
	"time"
)

// FuzzNew asserts that no combination of option values can produce a limiter
// that violates its construction invariants (min ≤ initial ≤ max, min ≥ 1).
func FuzzNew(f *testing.F) {
	f.Add(10, 1, 1000, 0.2, 0.5, int64(time.Second))
	f.Add(-5, 0, -1, 2.0, 1.5, int64(0))
	f.Add(1000000, 500, 100, 0.0, 0.99, int64(time.Hour))

	f.Fuzz(func(t *testing.T, initial, minL, maxL int, smoothing, decrease float64, windowNS int64) {
		l := New(
			WithInitialLimit(initial),
			WithMinLimit(minL),
			WithMaxLimit(maxL),
			WithSmoothing(smoothing),
			WithDecreaseRatio(decrease),
			WithSampleWindow(time.Duration(windowNS)),
		)
		defer l.Close()

		lim := l.Limit()
		s := l.Stats()
		if s.MinLimit < minLimitFloor {
			t.Fatalf("min limit %d below floor %d", s.MinLimit, minLimitFloor)
		}
		if s.MaxLimit < s.MinLimit {
			t.Fatalf("max %d below min %d", s.MaxLimit, s.MinLimit)
		}
		if lim < s.MinLimit || lim > s.MaxLimit {
			t.Fatalf("limit %d outside [%d, %d]", lim, s.MinLimit, s.MaxLimit)
		}
		if c := l.ringCap; c < minSamples || c > maxSamples {
			t.Fatalf("ring capacity %d outside [%d, %d]", c, minSamples, maxSamples)
		}
	})
}

// FuzzExecute drives a limiter through a schedule of success/failure outcomes
// and asserts the limit stays within bounds and in-flight returns to zero. The
// invariant under test: no feedback sequence can corrupt the permit accounting.
func FuzzExecute(f *testing.F) {
	f.Add([]byte{1, 0, 1, 1, 0}, uint8(0))
	f.Add([]byte{0, 0, 0, 0}, uint8(1))
	f.Add([]byte{}, uint8(2))

	f.Fuzz(func(t *testing.T, outcomes []byte, algo uint8) {
		l := New(
			WithAlgorithm(Algorithm(algo)),
			WithInitialLimit(8),
			WithMinLimit(1),
			WithMaxLimit(64),
			WithWarmupSamples(0),
		)
		defer l.Close()

		ctx := context.Background()
		for _, b := range outcomes {
			ok := b&1 == 0
			_, _ = Execute(l, ctx, func(context.Context, AdaptController) (int, error) {
				if ok {
					return 1, nil
				}
				return 0, errors.New("fail")
			})

			lim := l.Limit()
			if lim < 1 || lim > 64 {
				t.Fatalf("limit %d escaped [1, 64]", lim)
			}
		}
		if inflight := l.InFlight(); inflight != 0 {
			t.Fatalf("in-flight = %d, want 0 after all releases", inflight)
		}
	})
}
