package warmupx

import "context"

// Strategy selects the capacity ramp-up curve a [Warmer] follows from its
// minimum to its maximum capacity over the configured duration.
type Strategy uint8

const (
	// Linear ramps capacity uniformly with elapsed time:
	// capacity(t) = min + delta*t, where t is fractional progress in [0, 1].
	Linear Strategy = iota

	// Exponential ramps fast at the start then flattens towards the maximum:
	// capacity(t) = min + delta*(1 - e^(-k*t)). Use it to admit a meaningful
	// share of traffic early while still protecting a cold instance.
	Exponential

	// Logarithmic ramps slowly at the start then accelerates towards the
	// maximum. Use it to keep a fragile instance protected for longer before
	// opening up.
	Logarithmic

	// Step ramps in a fixed number of discrete jumps rather than continuously.
	// Use it when downstream capacity is provisioned in discrete units.
	Step
)

// String labels for [Strategy] values.
const (
	labelLinear      = "linear"
	labelExponential = "exponential"
	labelLogarithmic = "logarithmic"
	labelStep        = "step"
	labelUnknown     = "unknown"
)

// String returns a human-readable label for the strategy, or "unknown" for an
// out-of-range value.
func (s Strategy) String() string {
	switch s {
	case Linear:
		return labelLinear
	case Exponential:
		return labelExponential
	case Logarithmic:
		return labelLogarithmic
	case Step:
		return labelStep
	default:
		return labelUnknown
	}
}

// WarmupController provides per-call admission context and control to
// [Execute] and [TryExecute] callbacks. The implementation is private; callers
// interact only through this interface. A WarmupController is bound to a single
// Execute or TryExecute call and must not be retained after the callback returns.
type WarmupController interface {
	// Capacity returns the warmer capacity in [0, 1] at the moment the call was
	// admitted. Use it to scale work to the instance's current readiness, for
	// example by shrinking a batch size or skipping an optional sub-step.
	Capacity() float64

	// Progress returns the warmup progress in [0, 1] at the moment the call was
	// admitted, where 1 means warmup has completed.
	Progress() float64

	// Strategy returns the ramp-up strategy the warmer is configured with.
	Strategy() Strategy

	// Reject signals that the admitted call must be treated as rejected
	// regardless of its result: [Execute] and admitted [TryExecute] discard the
	// callback's return value and return [ErrRejected]. Use it when the callback
	// determines, after admission, that the instance is not yet ready for the
	// work. Safe to call multiple times.
	Reject()
}

// execution is the private implementation of [WarmupController]. It is created
// once per [Execute] or [TryExecute] call and accessed only from the callback
// goroutine, so it needs no synchronization.
type execution struct {
	capacity float64
	progress float64
	strategy Strategy
	rejected bool
}

// Capacity implements [WarmupController].
func (e *execution) Capacity() float64 { return e.capacity }

// Progress implements [WarmupController].
func (e *execution) Progress() float64 { return e.progress }

// Strategy implements [WarmupController].
func (e *execution) Strategy() Strategy { return e.strategy }

// Reject implements [WarmupController].
func (e *execution) Reject() { e.rejected = true }

// compile-time assertion that execution satisfies the public interface.
var _ WarmupController = (*execution)(nil)

// WarmupFunc is the unit of work run by [Execute] and [TryExecute]. It receives
// the call context and a [WarmupController], and runs under panic recovery: a
// panicking function becomes a [*panix.PanicError].
type WarmupFunc[T any] func(ctx context.Context, wc WarmupController) (T, error)
