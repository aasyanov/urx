// Package retryx provides a configurable retry engine with exponential backoff,
// jitter, and structured error reporting for industrial Go services.
//
// The caller supplies a function that returns an error; retryx re-executes it
// until it succeeds, the attempts are exhausted, or the context is cancelled.
// Each attempt receives a [RetryController] that exposes the current attempt
// number and an [RetryController.Abort] method to stop retrying early.
//
//	resp, err := retryx.Do(ctx, func(rc retryx.RetryController) (*Response, error) {
//	    resp, err := client.Call(ctx, req)
//	    if isPermError(err) {
//	        rc.Abort()
//	    }
//	    return resp, err
//	},
//	    retryx.WithMaxAttempts(5),
//	    retryx.WithBackoff(200*time.Millisecond),
//	)
//
// Retryability is determined by [errx.Error.Retryable] when the error is an
// [*errx.Error], or by a custom [WithRetryIf] predicate. Errors that are not
// retryable stop the loop immediately.
package retryx

import (
	"context"
	"math"
	"math/rand/v2"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/panix"
)

// --- Configuration ---

// config holds retry parameters.
type config struct {
	maxAttempts int
	backoff     time.Duration
	maxBackoff  time.Duration
	jitter      bool
	retryIf     func(error) bool
	onRetry     func(attempt int, err error)
}

// defaultConfig returns sensible retry defaults (3 attempts, 100 ms backoff, 10 s cap, jitter on).
func defaultConfig() config {
	return config{
		maxAttempts: 3,
		backoff:     100 * time.Millisecond,
		maxBackoff:  10 * time.Second,
		jitter:      true,
	}
}

// --- Options ---

// Option configures [Do] behavior.
type Option func(*config)

// WithMaxAttempts sets the maximum number of attempts (including the first).
// Values <= 0 are treated as 1 by [Do] (execute once, no retry).
func WithMaxAttempts(n int) Option {
	return func(c *config) { c.maxAttempts = n }
}

// WithBackoff sets the base backoff duration for exponential backoff.
// The actual delay for attempt i is: min(base * 2^i, maxBackoff) * jitter.
func WithBackoff(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.backoff = d
		}
	}
}

// WithMaxBackoff sets the upper bound for backoff duration.
func WithMaxBackoff(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.maxBackoff = d
		}
	}
}

// WithJitter enables or disables random jitter on backoff.
// Jitter multiplies the delay by a random factor in [0.5, 1.5).
func WithJitter(enabled bool) Option {
	return func(c *config) { c.jitter = enabled }
}

// WithRetryIf sets a custom predicate that decides whether an error is
// retryable. When set, this overrides the default [errx.Error.Retryable]
// check. Return true to retry, false to stop.
func WithRetryIf(fn func(error) bool) Option {
	return func(c *config) { c.retryIf = fn }
}

// WithOnRetry sets a callback invoked after each failed attempt (before the
// backoff sleep). Useful for logging or metrics. The attempt number is 1-based.
func WithOnRetry(fn func(attempt int, err error)) Option {
	return func(c *config) { c.onRetry = fn }
}

// --- RetryController ---

// RetryController provides per-attempt context and control to the retried
// function. The implementation is private; callers interact only through this
// interface.
type RetryController interface {
	// Number returns the 1-based attempt number.
	Number() int
	// Abort signals that the retry loop should stop after this attempt,
	// regardless of the error's retryability. Safe to call multiple times.
	Abort()
}

// attempt is the private implementation of [RetryController].
type attempt struct {
	number  int
	aborted bool
}

// Number implements [RetryController].
func (a *attempt) Number() int { return a.number }

// Abort implements [RetryController].
func (a *attempt) Abort() { a.aborted = true }

// --- Core retry logic ---

// Do executes fn repeatedly until it succeeds, the attempts are exhausted,
// the context is cancelled, or the caller calls [RetryController.Abort].
//
// On success (fn returns a nil error), Do returns the value and nil.
//
// On failure, Do returns zero T and a structured [*errx.Error] with one of:
//   - [CodeExhausted]: all attempts failed (wraps the last error)
//   - [CodeCancelled]: context was cancelled (wraps ctx.Err())
//   - [CodeAborted]: caller called Abort (wraps the last error)
//
// If fn returns a non-retryable error (determined by [WithRetryIf] or
// [errx.Error.Retryable]), the loop stops immediately and returns
// [CodeExhausted] wrapping that error.
func Do[T any](ctx context.Context, fn func(rc RetryController) (T, error), opts ...Option) (T, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxAttempts <= 0 {
		cfg.maxAttempts = 1
	}

	var zero T
	var lastErr error

	for i := 0; i < cfg.maxAttempts; i++ {
		if err := ctx.Err(); err != nil {
			return zero, errCancelled(err)
		}

		a := &attempt{number: i + 1}
		val, attemptErr := panix.Safe(ctx, "retryx.Do", func(ctx context.Context) (T, error) {
			return fn(a)
		})
		lastErr = attemptErr

		if lastErr == nil {
			return val, nil
		}

		if a.aborted {
			return zero, errAborted(a.number, lastErr)
		}

		if !isRetryable(&cfg, lastErr) {
			return zero, errExhausted(i+1, lastErr)
		}

		if i >= cfg.maxAttempts-1 {
			break
		}

		if cfg.onRetry != nil {
			cfg.onRetry(a.number, lastErr)
		}

		delay := backoff(&cfg, i)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			timer.Stop()
		case <-ctx.Done():
			timer.Stop()
			return zero, errCancelled(ctx.Err())
		}
	}

	return zero, errExhausted(cfg.maxAttempts, lastErr)
}

// --- Backoff calculation ---

// backoff computes the sleep duration for the given attempt using exponential backoff with optional jitter.
func backoff(cfg *config, attempt int) time.Duration {
	d := float64(cfg.backoff) * math.Pow(2, float64(attempt))

	if cfg.jitter {
		d *= 0.5 + rand.Float64() // [0.5, 1.5)
	}

	if time.Duration(d) > cfg.maxBackoff {
		d = float64(cfg.maxBackoff)
		if cfg.jitter {
			d *= 0.5 + rand.Float64()
		}
	}

	return time.Duration(d)
}

// --- Retryability check ---

// isRetryable decides whether err should trigger another attempt.
func isRetryable(cfg *config, err error) bool {
	if cfg.retryIf != nil {
		return cfg.retryIf(err)
	}

	if xe, ok := errx.As(err); ok {
		return xe.Retryable()
	}

	// Unknown errors are retryable by default (conservative: retry rather than fail)
	return true
}
