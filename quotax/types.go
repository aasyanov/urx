package quotax

import (
	"context"

	"github.com/aasyanov/urx/ratex"
)

// QuotaController provides per-call, per-key context and control to the
// [Execute] and [TryExecute] callback. The implementation is private; callers
// interact only through this interface. A QuotaController is bound to a single
// call and must not be retained after the callback returns.
type QuotaController interface {
	// Key returns the key the call was admitted under.
	Key() string

	// Tokens returns the number of tokens that remained in the key's bucket
	// immediately after this call's token was consumed (fractional). Use it to
	// gauge how close this key is to throttling and to degrade gracefully while
	// the key's spare capacity is low.
	Tokens() float64

	// Rate returns the per-key sustained rate in requests per second.
	Rate() float64

	// Burst returns the per-key bucket capacity.
	Burst() int

	// Waited reports whether the call blocked waiting for a token before being
	// admitted. It is always false for [TryExecute].
	Waited() bool

	// SkipToken asks the limiter to refund the token consumed by this call to
	// the key's bucket. Use it when the callback turns out to be a no-op that
	// should not count against the key's budget (for example, a cache hit that
	// performed no downstream work). Safe to call multiple times; only one
	// token is refunded.
	SkipToken()
}

// execution is the private implementation of [QuotaController]. It is created
// once per call and accessed only from the calling goroutine, so it needs no
// synchronization. The per-key token refund is delegated to the underlying
// [ratex.RateController], which owns the key's bucket.
type execution struct {
	key    string
	tokens float64
	rate   float64
	burst  int
	waited bool
	inner  ratex.RateController
}

// Key implements [QuotaController].
func (e *execution) Key() string { return e.key }

// Tokens implements [QuotaController].
func (e *execution) Tokens() float64 { return e.tokens }

// Rate implements [QuotaController].
func (e *execution) Rate() float64 { return e.rate }

// Burst implements [QuotaController].
func (e *execution) Burst() int { return e.burst }

// Waited implements [QuotaController].
func (e *execution) Waited() bool { return e.waited }

// SkipToken implements [QuotaController] by delegating to the underlying
// ratex controller, which refunds the token to the key's bucket.
func (e *execution) SkipToken() {
	if e.inner != nil {
		e.inner.SkipToken()
	}
}

// compile-time assertion that execution satisfies the public interface.
var _ QuotaController = (*execution)(nil)

// QuotaFunc is the unit of work run by [Execute] and [TryExecute]. It receives
// the call context and a [QuotaController], and runs under panic recovery: a
// panicking function becomes a [*panix.PanicError].
type QuotaFunc[T any] func(ctx context.Context, qc QuotaController) (T, error)

// Stats holds a point-in-time snapshot of per-key limiter counters.
type Stats struct {
	// Keys is the number of currently tracked keys.
	Keys int64 `json:"keys"`

	// Allowed is the cumulative number of admitted requests across all keys.
	Allowed int64 `json:"allowed"`

	// Limited is the cumulative number of denied requests across all keys,
	// including requests denied because the [WithMaxKeys] cap was reached.
	Limited int64 `json:"limited"`
}
