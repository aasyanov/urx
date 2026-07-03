package ratex

import "context"

// RateController provides per-call context and control to the [Execute] and
// [TryExecute] callback. The implementation is private; callers interact only
// through this interface. A RateController is bound to a single call and must
// not be retained after the callback returns.
type RateController interface {
	// Tokens returns the number of tokens that remained in the bucket
	// immediately after this call's token was consumed (fractional). Use it to
	// gauge how close the limiter is to throttling and to degrade gracefully
	// while spare capacity is low.
	Tokens() float64

	// Rate returns the limiter's sustained rate in requests per second.
	Rate() float64

	// Burst returns the limiter's bucket capacity.
	Burst() int

	// Waited reports whether the call blocked waiting for a token before being
	// admitted. It is always false for [TryExecute].
	Waited() bool

	// SkipToken asks the limiter to refund the token consumed by this call.
	// Use it when the callback turns out to be a no-op that should not count
	// against the budget (for example, a cache hit that performed no
	// downstream work). Safe to call multiple times; only one token is
	// refunded.
	SkipToken()
}

// execution is the private implementation of [RateController]. It is created
// once per call and accessed only from the calling goroutine, so it needs no
// synchronization.
type execution struct {
	tokens    float64
	rate      float64
	burst     int
	waited    bool
	skipToken bool
}

// Tokens implements [RateController].
func (e *execution) Tokens() float64 { return e.tokens }

// Rate implements [RateController].
func (e *execution) Rate() float64 { return e.rate }

// Burst implements [RateController].
func (e *execution) Burst() int { return e.burst }

// Waited implements [RateController].
func (e *execution) Waited() bool { return e.waited }

// SkipToken implements [RateController].
func (e *execution) SkipToken() { e.skipToken = true }

// compile-time assertion that execution satisfies the public interface.
var _ RateController = (*execution)(nil)

// RateFunc is the unit of work run by [Execute] and [TryExecute]. It receives
// the call context and a [RateController], and runs under panic recovery: a
// panicking function becomes a [*panix.PanicError].
type RateFunc[T any] func(ctx context.Context, rc RateController) (T, error)
