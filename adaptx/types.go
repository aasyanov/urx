package adaptx

import (
	"context"
	"sync/atomic"
	"time"
)

// Algorithm selects the strategy a [Limiter] uses to move its concurrency
// limit in response to latency and error feedback.
type Algorithm uint8

const (
	// AIMD is Additive Increase / Multiplicative Decrease: the limit grows by a
	// fixed step on each success and is cut by a multiplicative factor on each
	// failure. It needs no latency target and is the safest default — the same
	// control law TCP congestion avoidance uses. Best when failures (not
	// latency) are the overload signal.
	AIMD Algorithm = iota

	// Vegas estimates queue build-up from round-trip time, in the spirit of
	// TCP Vegas. It compares observed latency against the best latency seen
	// (RTT_min) to infer how much work is queued, then grows the limit while
	// the estimated queue is small and shrinks it when the queue grows. Best
	// when a backend has a stable, measurable floor latency.
	Vegas

	// Gradient reacts to the trend of latency: it grows the limit while the
	// current sample is at or below the smoothed average and backs off in
	// proportion to how far latency has risen above it. Best for backends
	// whose floor latency drifts, where an absolute target would go stale.
	Gradient
)

// String returns a human-readable label for the algorithm.
func (a Algorithm) String() string {
	switch a {
	case AIMD:
		return labelAIMD
	case Vegas:
		return labelVegas
	case Gradient:
		return labelGradient
	default:
		return labelUnknown
	}
}

const (
	labelAIMD     = "AIMD"
	labelVegas    = "Vegas"
	labelGradient = "Gradient"
	labelUnknown  = "unknown"
)

// AdaptController exposes the admission snapshot to the [Execute] callback and
// lets it opt out of feeding its result into the adaptive algorithm. The
// implementation is private; callers interact only through this interface. An
// AdaptController is bound to a single [Execute] call and must not be retained
// after the callback returns.
//
// The concurrency limit is decided at admission: by the time the callback runs
// the request is already admitted. The controller therefore exposes the limit
// and in-flight count captured at admission so the callback can adapt its work
// to the observed pressure — for example, serve a cheaper query when the limiter
// is near saturation. [AdaptController.SkipSample] removes outlier calls (cache
// misses, cold starts) from the feedback signal so a single anomalous latency
// does not mislead the controller.
type AdaptController interface {
	// Limit returns the concurrency limit in effect at admission time.
	Limit() int

	// InFlight returns the number of operations in flight at admission time,
	// excluding this one.
	InFlight() int

	// Algorithm returns the active adaptation algorithm.
	Algorithm() Algorithm

	// SkipSample tells the limiter not to feed this call's latency and outcome
	// into the adaptive algorithm. Use it for outlier operations whose latency
	// would mislead the controller (cache misses, cold starts, admin calls).
	// The call still counts toward the success/failure totals in [Stats]. Safe
	// to call multiple times; only the first call has an effect.
	SkipSample()
}

// execution is the private implementation of [AdaptController]. One instance is
// created per [Execute] call and accessed only from the callback goroutine, so
// the snapshot fields need no synchronization; skipped is an atomic only so a
// callback that hands the controller to a helper goroutine before returning
// cannot race the [Execute] dispatcher reading it.
type execution struct {
	limit     int
	inFlight  int
	algorithm Algorithm
	skipped   atomic.Bool
}

// Limit implements [AdaptController].
func (e *execution) Limit() int { return e.limit }

// InFlight implements [AdaptController].
func (e *execution) InFlight() int { return e.inFlight }

// Algorithm implements [AdaptController].
func (e *execution) Algorithm() Algorithm { return e.algorithm }

// SkipSample implements [AdaptController].
func (e *execution) SkipSample() { e.skipped.Store(true) }

// isSkipped reports whether the callback opted this call out of the feedback.
func (e *execution) isSkipped() bool { return e.skipped.Load() }

// compile-time assertion that execution satisfies the public interface.
var _ AdaptController = (*execution)(nil)

// AdaptFunc is the unit of work run by [Execute] and [TryExecute]. It receives
// the call context and an [AdaptController], and runs under panic recovery: a
// panicking function becomes a [*panix.PanicError].
type AdaptFunc[T any] func(ctx context.Context, ac AdaptController) (T, error)

// sample is one recorded outcome retained in the limiter's ring buffer for
// percentile statistics and (for the latency-based algorithms) adaptation.
type sample struct {
	latency time.Duration
	ts      time.Time
	success bool
}
