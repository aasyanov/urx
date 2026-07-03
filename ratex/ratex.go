// Package ratex provides a thread-safe token-bucket rate limiter for
// production Go services.
//
// A [Limiter] controls how many operations per unit of time are admitted.
// Tokens accrue at a sustained rate up to a burst capacity; each admitted
// operation consumes one token. The package offers three layers, from
// lowest to highest level:
//
//   - [Limiter.Allow] / [Limiter.AllowN]: non-blocking checks that consume
//     tokens when available.
//   - [Limiter.Wait] / [Limiter.WaitN]: block until tokens are available or
//     the context is done.
//   - [Execute] / [TryExecute]: run a function under the limiter with panic
//     recovery, handing the callback a [RateController].
//
//	rl := ratex.New(
//	    ratex.WithRate(100),
//	    ratex.WithBurst(20),
//	)
//
//	resp, err := ratex.Execute(rl, ctx, func(ctx context.Context, rc ratex.RateController) (*Response, error) {
//	    if rc.Tokens() < 5 {
//	        return cheapResponse(ctx) // throttle expensive work near the limit
//	    }
//	    return client.Call(ctx, req)
//	})
//
// The callback receives a [RateController] exposing the remaining tokens, the
// configured rate and burst, whether the call waited, and a
// [RateController.SkipToken] method to refund the token for no-op calls.
//
// Each callback is wrapped with [github.com/aasyanov/urx/panix]: a panicking
// function becomes a [*panix.PanicError] rather than crashing the process.
//
// # Dependencies
//
// ratex depends only on the Go standard library and the urx panix package.
package ratex

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/aasyanov/urx/panix"
)

const (
	// opExecute labels panics recovered while running an Execute callback.
	opExecute = "ratex.Execute"

	// opTryExecute labels panics recovered while running a TryExecute callback.
	opTryExecute = "ratex.TryExecute"

	// minDelay is the shortest backoff [Limiter.WaitN] sleeps between token
	// availability checks, preventing a busy-spin when the computed delay
	// rounds down to zero.
	minDelay = time.Millisecond
)

// Limiter is a thread-safe token-bucket rate limiter. Create one with [New],
// check with [Limiter.Allow] or [Limiter.AllowN], block with [Limiter.Wait] or
// [Limiter.WaitN], run functions with [Execute] or [TryExecute], and reset
// with [Limiter.Reset].
//
// A Limiter is safe for concurrent use from multiple goroutines.
type Limiter struct {
	cfg        config
	mu         sync.Mutex
	tokens     float64
	lastUpdate time.Time
	allowed    uint64
	limited    uint64
}

// New creates a [Limiter] with the given options applied on top of sensible
// defaults ([DefaultRate] req/s, burst [DefaultBurst]). A non-positive rate or
// burst is clamped to its floor so the returned limiter is always usable.
func New(opts ...Option) *Limiter {
	cfg := newConfig(opts)
	return &Limiter{
		cfg:        cfg,
		tokens:     float64(cfg.burst),
		lastUpdate: time.Now(),
	}
}

// --- Token management ---

// refill adds tokens accrued since the last update, capped at the bucket
// capacity. Must be called with mu held.
func (l *Limiter) refill() {
	now := time.Now()
	elapsed := now.Sub(l.lastUpdate)
	if elapsed <= 0 {
		return
	}
	add := elapsed.Seconds() * l.cfg.rate
	if add > 0 {
		l.tokens = math.Min(float64(l.cfg.burst), l.tokens+add)
		l.lastUpdate = now
	}
}

// take attempts to consume n tokens, refilling first. It reports success and
// the token balance after consumption. It is a pure bucket primitive: it does
// not touch the outcome counters, so the blocking paths can probe it freely
// without inflating [Stats]. Must be called with mu held.
func (l *Limiter) take(n float64) (ok bool, remaining float64) {
	l.refill()
	if l.tokens >= n {
		l.tokens -= n
		return true, l.tokens
	}
	return false, l.tokens
}

// --- Non-blocking checks ---

// Allow reports whether one request is admitted right now, consuming one token
// on success.
func (l *Limiter) Allow() bool {
	return l.AllowN(1)
}

// AllowN reports whether n requests are admitted right now. It consumes n
// tokens on success; on failure no tokens are consumed. Values of n < 1 are
// treated as 1. A request larger than the bucket capacity always fails, since
// the bucket can never hold that many tokens.
func (l *Limiter) AllowN(n int) bool {
	if n < 1 {
		n = 1
	}
	l.mu.Lock()
	ok, _ := l.take(float64(n))
	if ok {
		l.allowed++
	} else {
		l.limited++
	}
	l.mu.Unlock()
	return ok
}

// --- Blocking checks ---

// Wait blocks until one token is available or ctx is done. It returns nil once
// a token has been consumed, or [ErrCancelled] wrapping ctx.Err() if the
// context is cancelled first.
func (l *Limiter) Wait(ctx context.Context) error {
	return l.WaitN(ctx, 1)
}

// WaitN blocks until n tokens are available or ctx is done. It returns nil once
// the tokens have been consumed, or [ErrCancelled] wrapping ctx.Err() if the
// context is cancelled first. Values of n < 1 are treated as 1.
//
// A request for more than the bucket capacity can never be satisfied; WaitN
// blocks until ctx is cancelled in that case.
func (l *Limiter) WaitN(ctx context.Context, n int) error {
	if n < 1 {
		n = 1
	}
	need := float64(n)
	for {
		if err := ctx.Err(); err != nil {
			l.countLimited()
			return errCancelled(err)
		}

		l.mu.Lock()
		ok, _ := l.take(need)
		if ok {
			l.allowed++
		}
		l.mu.Unlock()
		if ok {
			if err := ctx.Err(); err != nil {
				l.refund(need)
				l.countLimited()
				return errCancelled(err)
			}
			return nil
		}

		timer := time.NewTimer(l.delay(n))
		select {
		case <-ctx.Done():
			stopTimer(timer)
			l.countLimited()
			return errCancelled(ctx.Err())
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				l.countLimited()
				return errCancelled(err)
			}
		}
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

// countLimited records a single denied request under the lock.
func (l *Limiter) countLimited() {
	l.mu.Lock()
	l.limited++
	l.mu.Unlock()
}

// delay returns the estimated time until n tokens are available, with a
// [minDelay] floor so the wait loop never busy-spins. Must not hold mu.
func (l *Limiter) delay(n int) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refill()

	deficit := float64(n) - l.tokens
	if deficit <= 0 {
		return minDelay
	}
	d := time.Duration(deficit / l.cfg.rate * float64(time.Second))
	if d < minDelay {
		return minDelay
	}
	return d
}

// --- Execution ---

// Execute runs fn under the limiter, blocking until a token is available or
// ctx is done. Because Go methods cannot have type parameters, Execute is a
// package-level generic function that takes the [Limiter] as its first
// argument.
//
// On admission fn receives a [RateController] exposing the remaining tokens,
// the configured rate and burst, whether the call waited, and a
// [RateController.SkipToken] hook to refund the token for no-op calls. The
// callback is wrapped with [panix.Safe]; a panic becomes a [*panix.PanicError].
//
// Execute returns [ErrNilFunc] if fn is nil, or [ErrCancelled] wrapping
// ctx.Err() if the context is cancelled before a token is acquired.
func Execute[T any](l *Limiter, ctx context.Context, fn func(ctx context.Context, rc RateController) (T, error)) (T, error) {
	var zero T
	if fn == nil {
		return zero, ErrNilFunc
	}

	waited, remaining, err := l.acquire(ctx)
	if err != nil {
		return zero, err
	}
	return run(l, ctx, opExecute, waited, remaining, fn)
}

// TryExecute attempts to run fn without blocking. If a token is immediately
// available the function executes and TryExecute returns (true, val, err). If
// no token is available it returns (false, zero, nil) without executing fn.
//
// Returns (false, zero, [ErrNilFunc]) if fn is nil, or (false, zero,
// [ErrCancelled]) when ctx is already done — including after a token was taken
// but before fn runs, in which case the token is refunded.
func TryExecute[T any](l *Limiter, ctx context.Context, fn func(ctx context.Context, rc RateController) (T, error)) (bool, T, error) {
	var zero T
	if fn == nil {
		return false, zero, ErrNilFunc
	}
	if err := ctx.Err(); err != nil {
		return false, zero, errCancelled(err)
	}

	l.mu.Lock()
	ok, remaining := l.take(1)
	if ok {
		l.allowed++
	} else {
		l.limited++
	}
	l.mu.Unlock()
	if !ok {
		return false, zero, nil
	}

	if err := ctx.Err(); err != nil {
		l.refund(1)
		l.countLimited()
		return false, zero, errCancelled(err)
	}

	val, err := run(l, ctx, opTryExecute, false, remaining, fn)
	return true, val, err
}

// acquire blocks until one token is consumed or ctx is done. It reports whether
// the call had to wait and the token balance after consumption.
func (l *Limiter) acquire(ctx context.Context) (waited bool, remaining float64, err error) {
	if err := ctx.Err(); err != nil {
		l.countLimited()
		return false, 0, errCancelled(err)
	}

	l.mu.Lock()
	ok, rem := l.take(1)
	if ok {
		l.allowed++
	}
	l.mu.Unlock()
	if ok {
		if err := ctx.Err(); err != nil {
			l.refund(1)
			l.countLimited()
			return false, 0, errCancelled(err)
		}
		return false, rem, nil
	}

	for {
		timer := time.NewTimer(l.delay(1))
		select {
		case <-ctx.Done():
			stopTimer(timer)
			l.countLimited()
			return false, 0, errCancelled(ctx.Err())
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				l.countLimited()
				return false, 0, errCancelled(err)
			}
		}

		l.mu.Lock()
		ok, rem := l.take(1)
		if ok {
			l.allowed++
		}
		l.mu.Unlock()
		if ok {
			if err := ctx.Err(); err != nil {
				l.refund(1)
				l.countLimited()
				return false, 0, errCancelled(err)
			}
			return true, rem, nil
		}
	}
}

// run invokes fn under panic recovery and refunds the consumed token when the
// callback requests it via [RateController.SkipToken].
func run[T any](l *Limiter, ctx context.Context, op string, waited bool, remaining float64, fn func(ctx context.Context, rc RateController) (T, error)) (T, error) {
	rc := &execution{
		tokens: remaining,
		rate:   l.cfg.rate,
		burst:  l.cfg.burst,
		waited: waited,
	}
	val, err := panix.Safe(op, func() (T, error) {
		return fn(ctx, rc)
	})
	if rc.skipToken {
		l.refund(1)
	}
	return val, err
}

// refund returns n tokens to the bucket, capped at capacity, and rolls back the
// allowed counter so a skipped call does not inflate throughput statistics.
func (l *Limiter) refund(n float64) {
	l.mu.Lock()
	l.tokens = math.Min(float64(l.cfg.burst), l.tokens+n)
	if l.allowed > 0 {
		l.allowed--
	}
	l.mu.Unlock()
}

// Release returns n tokens to the bucket and reverses the admission counter
// for a previously consumed request that is being aborted.
func (l *Limiter) Release(n float64) {
	l.refund(n)
}

// --- Accessors ---

// Tokens returns the current number of available tokens (fractional) after
// accounting for accrual since the last operation.
func (l *Limiter) Tokens() float64 {
	l.mu.Lock()
	l.refill()
	t := l.tokens
	l.mu.Unlock()
	return t
}

// Rate returns the configured sustained rate in requests per second.
func (l *Limiter) Rate() float64 { return l.cfg.rate }

// Burst returns the configured bucket capacity.
func (l *Limiter) Burst() int { return l.cfg.burst }

// --- Statistics ---

// Stats holds a point-in-time snapshot of rate limiter counters.
type Stats struct {
	Rate    float64 `json:"rate"`
	Burst   int     `json:"burst"`
	Tokens  float64 `json:"tokens"`
	Allowed uint64  `json:"allowed"`
	Limited uint64  `json:"limited"`
}

// Stats returns a snapshot of rate limiter statistics.
func (l *Limiter) Stats() Stats {
	l.mu.Lock()
	l.refill()
	s := Stats{
		Rate:    l.cfg.rate,
		Burst:   l.cfg.burst,
		Tokens:  l.tokens,
		Allowed: l.allowed,
		Limited: l.limited,
	}
	l.mu.Unlock()
	return s
}

// ResetStats zeroes the allowed and limited counters without changing the token
// balance.
func (l *Limiter) ResetStats() {
	l.mu.Lock()
	l.allowed = 0
	l.limited = 0
	l.mu.Unlock()
}

// --- Lifecycle ---

// Reset restores the bucket to its initial full state and clears the accrual
// clock. Counters are left untouched; use [Limiter.ResetStats] for those.
func (l *Limiter) Reset() {
	l.mu.Lock()
	l.tokens = float64(l.cfg.burst)
	l.lastUpdate = time.Now()
	l.mu.Unlock()
}
