// Package toutx provides a thread-safe timeout execution wrapper for
// industrial Go services.
//
// [Execute] runs a function within a deadline-scoped context. If the function
// does not complete before the timeout fires, the context is cancelled and
// [ErrDeadlineExceeded] is returned. If the parent context is cancelled first,
// [ErrCancelled] is returned wrapping the cause.
//
//	resp, err := toutx.Execute(ctx, 5*time.Second,
//	    func(ctx context.Context, tc toutx.TimeoutController) (*Response, error) {
//	        return client.Call(ctx, req)
//	    })
//
// The callback receives a [TimeoutController] exposing the deadline, the
// elapsed time, and the remaining budget, so it can self-throttle expensive
// sub-steps that would not finish in time.
//
// For stateful usage with pre-configured defaults, create a [Timer] via [New]
// and reuse it across calls:
//
//	t := toutx.New(
//	    toutx.WithTimeout(3*time.Second),
//	    toutx.WithOp("db.query"),
//	)
//	rows, err := toutx.Execute(ctx, 0, queryFn, toutx.WithTimer(t))
//
// Each call is wrapped with [github.com/aasyanov/urx/panix] for panic
// recovery; a panicking function yields a [*panix.PanicError] instead of
// crashing the process.
//
// # Dependencies
//
// toutx depends only on the Go standard library and the urx panix package.
package toutx

import (
	"context"
	"time"

	"github.com/aasyanov/urx/panix"
)

// opExecute labels panics recovered while running an Execute callback and is
// used as the default operation name when none is configured.
const opExecute = "toutx.Execute"

// Execute runs fn within a deadline derived from ctx. The timeout argument is
// applied first; pass 0 to rely entirely on options or a [Timer] (which
// default to [DefaultTimeout]). Later options override earlier values.
//
// The callback receives a deadline-scoped context and a [TimeoutController].
// If fn returns before the deadline, its result is returned unchanged. If the
// deadline fires first, Execute returns [ErrDeadlineExceeded]. If the parent
// ctx is cancelled first, Execute returns [ErrCancelled] wrapping ctx.Err().
//
// fn runs in a separate goroutine guarded by [panix.Safe]; a panic becomes a
// [*panix.PanicError]. Note that when the deadline fires, Execute returns
// immediately while fn may still be running — fn must honour its context to
// avoid leaking the goroutine.
func Execute[T any](
	ctx context.Context,
	timeout time.Duration,
	fn func(ctx context.Context, tc TimeoutController) (T, error),
	opts ...Option,
) (T, error) {
	cfg := newConfig(timeout, opts)
	op := cfg.opOrDefault()

	var zero T
	if fn == nil {
		return zero, errNilFunc(op)
	}

	if err := ctx.Err(); err != nil {
		return zero, errCancelled(op, err)
	}

	start := time.Now()
	tctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	tc := &execution{
		op:      op,
		timeout: cfg.timeout,
		start:   start,
	}

	type result struct {
		val T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, e := panix.Safe(op, func() (T, error) {
			return fn(tctx, tc)
		})
		done <- result{v, e}
	}()

	select {
	case r := <-done:
		return r.val, r.err
	case <-tctx.Done():
		// Parent cancellation and deadline expiry both close tctx; distinguish
		// them so callers see the precise cause.
		if cause := context.Cause(ctx); cause != nil {
			return zero, errCancelled(op, cause)
		}
		return zero, errDeadlineExceeded(op, cfg.timeout)
	}
}
