// Package retryx provides a configurable retry engine with exponential backoff,
// jitter, panic recovery, and structured error reporting for production Go
// services.
//
// The caller supplies a [RetryFunc] that receives the call context and a
// [RetryController], and returns a value and an error; retryx re-executes it
// until it succeeds, the attempts are exhausted, the error is deemed
// non-retryable, the context is cancelled, or the caller aborts. Each attempt
// receives the propagated context and a RetryController exposing the attempt
// number, the previous error, the elapsed time, and an
// [RetryController.Abort] method.
//
//	resp, err := retryx.Do(ctx, func(ctx context.Context, rc retryx.RetryController) (*Response, error) {
//	    resp, err := client.Call(ctx, req)
//	    if isPermanent(err) {
//	        rc.Abort() // do not retry a permanent failure
//	    }
//	    return resp, err
//	},
//	    retryx.WithMaxAttempts(5),
//	    retryx.WithBackoff(200*time.Millisecond),
//	)
//
// Retryability is decided by a [WithRetryIf] predicate when supplied; otherwise
// every error is retryable until the attempt budget is exhausted. Each attempt
// is guarded by [github.com/aasyanov/urx/panix]: a panicking function becomes a
// [*panix.PanicError] rather than crashing the process, and that panic is
// treated as a normal (retryable) failure.
//
// # Dependencies
//
// retryx depends only on the Go standard library and the urx panix package.
package retryx

import (
	"context"
	"math"
	"math/rand/v2"
	"time"

	"github.com/aasyanov/urx/panix"
)

const (
	// jitterFloor and jitterSpan define the multiplicative jitter window
	// [jitterFloor, jitterFloor+jitterSpan): random in [0.5, 1.5).
	jitterFloor = 0.5
	jitterSpan  = 1.0
)

// Do executes fn repeatedly until it returns a nil error, the attempt budget
// is exhausted, fn returns a non-retryable error, the context is cancelled, or
// the caller invokes [RetryController.Abort].
//
// On success Do returns fn's value and a nil error. On failure Do returns the
// zero value and one of the package sentinel errors, each wrapping the
// underlying cause:
//   - [ErrExhausted]: every attempt failed, or a non-retryable error stopped
//     the loop early
//   - [ErrCancelled]: the context was cancelled or expired
//   - [ErrAborted]: the caller called [RetryController.Abort]
//
// Each attempt runs under [panix.Safe]; a recovered panic is reported as a
// [*panix.PanicError] and handled like any other (retryable) failure.
//
// Do is safe for concurrent use from multiple goroutines: each call owns its
// resolved configuration, per-attempt [RetryController], and backoff timer;
// nothing is shared between calls.
func Do[T any](ctx context.Context, fn RetryFunc[T], opts ...Option) (T, error) {
	cfg := newConfig(opts)

	var zero T
	if fn == nil {
		return zero, ErrNilFunc
	}

	start := time.Now()
	var lastErr error

	for i := range cfg.maxAttempts {
		if err := ctx.Err(); err != nil {
			return zero, errCancelled(err)
		}

		rc := &attempt{number: i + 1, lastErr: lastErr, start: start}
		val, attemptErr := panix.Safe(cfg.opOrDefault(), func() (T, error) {
			return fn(ctx, rc)
		})
		if attemptErr == nil {
			return val, nil
		}
		lastErr = attemptErr

		if rc.aborted {
			return zero, errAborted(rc.number, lastErr)
		}
		if !isRetryable(&cfg, lastErr) {
			return zero, errExhausted(rc.number, lastErr)
		}
		if i == cfg.maxAttempts-1 {
			break
		}

		if cfg.onRetry != nil {
			cfg.onRetry(rc.number, lastErr)
		}
		if err := sleep(ctx, backoff(&cfg, i)); err != nil {
			return zero, errCancelled(err)
		}
	}

	return zero, errExhausted(cfg.maxAttempts, lastErr)
}

// sleep blocks for d or until ctx is cancelled, returning ctx.Err() in the
// latter case. A non-positive d returns immediately (after a cancellation
// check) so a zero backoff does not busy-spin on the timer.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		stopTimer(timer)
		return ctx.Err()
	}
}

// stopTimer stops t and drains its channel when the timer already fired, so
// the runtime can reclaim the timer goroutine promptly.
func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

// backoff computes the delay before the retry following attempt i (0-based)
// using exponential growth, a hard cap, and optional jitter. The cap is
// applied before jitter so the jittered delay can briefly exceed maxBackoff by
// up to the jitter span — the intended decorrelation behaviour.
func backoff(cfg *config, i int) time.Duration {
	d := float64(cfg.backoff) * math.Pow(2, float64(i))
	if d > float64(cfg.maxBackoff) {
		d = float64(cfg.maxBackoff)
	}
	if cfg.jitter {
		d *= jitterFloor + rand.Float64()*jitterSpan
	}
	return time.Duration(d)
}

// isRetryable decides whether err should trigger another attempt. Without a
// custom predicate every error is retryable.
func isRetryable(cfg *config, err error) bool {
	if cfg.retryIf != nil {
		return cfg.retryIf(err)
	}
	return true
}
