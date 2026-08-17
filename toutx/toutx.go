// Package toutx provides a thread-safe timeout execution wrapper for
// production Go services.
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
	"errors"
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
// If fn returns before the deadline, its result is returned unchanged — including
// when that result is context.DeadlineExceeded or context.Canceled produced by
// fn itself while this call's timeout context is still live. If the
// deadline fires first, Execute returns [ErrDeadlineExceeded] unless fn's
// result is already available. If the parent ctx is cancelled first, Execute
// returns [ErrCancelled] wrapping ctx.Err().
//
// fn runs in a separate goroutine guarded by [panix.Safe]; a panic becomes a
// [*panix.PanicError]. Note that when the deadline fires, Execute returns
// immediately while fn may still be running — fn must honour its context to
// avoid leaking the goroutine.
//
// Execute is safe for concurrent use from multiple goroutines: each call owns
// its resolved configuration, deadline context, goroutine, and result channel;
// nothing is shared between calls.
func Execute[T any](
	ctx context.Context,
	timeout time.Duration,
	fn TimeoutFunc[T],
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
	tctx, cancel := context.WithTimeoutCause(ctx, cfg.timeout, ErrDeadlineExceeded)
	defer cancel()

	tc := &execution{
		op:           op,
		timeout:      cfg.timeout,
		startUnix:    start.UnixNano(),
		deadlineUnix: resolveDeadline(tctx, start, cfg.timeout).UnixNano(),
	}

	done := make(chan execResult[T], 1)
	go func() {
		v, e := panix.Safe(op, func() (T, error) {
			return fn(tctx, tc)
		})
		done <- execResult[T]{v, e}
	}()

	return awaitResult(done, tctx, ctx, op, cfg.timeout)
}

// resolveDeadline returns the absolute instant at which tctx expires. When the
// context reports no deadline (defensive — [context.WithTimeout] always sets
// one), the configured timeout is added to start.
func resolveDeadline(tctx context.Context, start time.Time, timeout time.Duration) time.Time {
	if deadline, ok := tctx.Deadline(); ok {
		return deadline
	}
	return start.Add(timeout)
}

// awaitResult returns fn's outcome when it finishes before the deadline. When
// the deadline fires first it re-checks done once so a result that lands in the
// same instant as expiry is not discarded.
func awaitResult[T any](
	done <-chan execResult[T],
	tctx context.Context,
	parent context.Context,
	op string,
	timeout time.Duration,
) (T, error) {
	var zero T
	select {
	case r := <-done:
		return normalizeResult(zero, tctx, parent, op, timeout, r)
	case <-tctx.Done():
		select {
		case r := <-done:
			return normalizeResult(zero, tctx, parent, op, timeout, r)
		default:
			if cause := context.Cause(parent); cause != nil {
				return zero, errCancelled(op, cause)
			}
			return zero, errDeadlineExceeded(op, timeout)
		}
	}
}

// normalizeResult maps a finished callback outcome to the package error
// vocabulary. A nil callback error is returned as-is so a result that lands
// in the same instant as expiry is not discarded.
//
// context.DeadlineExceeded is remapped to [ErrDeadlineExceeded] only when
// tctx has already expired (this call's timeout fired). If fn returns
// DeadlineExceeded while tctx is still live, the original error and value
// pass through. context.Canceled is remapped to [ErrCancelled] only when the
// parent is already done; if the parent is live and tctx is done, that
// Canceled is our timeout and becomes [ErrDeadlineExceeded]. An inner
// Canceled while both are live propagates unchanged.
func normalizeResult[T any](zero T, tctx, parent context.Context, op string, timeout time.Duration, r execResult[T]) (T, error) {
	if r.err == nil {
		return r.val, nil
	}
	if errors.Is(r.err, context.Canceled) {
		if parent.Err() == nil && tctx.Err() == nil {
			return r.val, r.err
		}
		if parent.Err() != nil {
			cause := context.Cause(parent)
			if cause == nil {
				cause = context.Canceled
			}
			return zero, errCancelled(op, cause)
		}
		return zero, errDeadlineExceeded(op, timeout)
	}
	if errors.Is(r.err, context.DeadlineExceeded) {
		if tctx.Err() == nil {
			return r.val, r.err
		}
		if cause := context.Cause(parent); cause != nil && !errors.Is(cause, context.DeadlineExceeded) {
			return zero, errCancelled(op, cause)
		}
		return zero, errDeadlineExceeded(op, timeout)
	}
	return r.val, r.err
}

type execResult[T any] struct {
	val T
	err error
}
