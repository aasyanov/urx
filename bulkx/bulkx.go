// Package bulkx provides a thread-safe concurrency limiter (bulkhead) for
// production Go services.
//
// A [Bulkhead] caps the number of operations that may execute concurrently,
// isolating a slow or failing dependency so it cannot exhaust the resources of
// the whole process. [Execute] blocks until a slot is available, the context
// is cancelled, or the configured timeout fires. [TryExecute] is the
// non-blocking variant that rejects immediately when no slot is free, and
// [Bulkhead.Acquire] hands back a [Token] for code that cannot use a callback.
//
//	bh := bulkx.New(
//	    bulkx.WithMaxConcurrent(10),
//	    bulkx.WithTimeout(5*time.Second),
//	)
//	defer bh.Close()
//
//	resp, err := bulkx.Execute(bh, ctx,
//	    func(ctx context.Context, bc bulkx.BulkController) (*Response, error) {
//	        if bc.Load() > 0.8 {
//	            return lightweightResponse(ctx) // shed work near saturation
//	        }
//	        return client.Call(ctx, req)
//	    })
//
// The callback receives a [BulkController] exposing the occupancy snapshot at
// admission time so it can adapt under pressure. Each callback is wrapped with
// [github.com/aasyanov/urx/panix] for panic recovery; a panicking function
// yields a [*panix.PanicError] instead of crashing the process, and the slot is
// always released.
//
// # Dependencies
//
// bulkx depends only on the Go standard library and the urx panix package.
package bulkx

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/panix"
)

// opExecute labels panics recovered while running an [Execute] callback and is
// the default operation name when none is configured via [WithOp].
const opExecute = "bulkx.Execute"

// opTryExecute labels panics recovered while running a [TryExecute] callback.
const opTryExecute = "bulkx.TryExecute"

// Bulkhead is a thread-safe concurrency limiter. Create one with [New], run
// operations with [Execute] or [TryExecute], obtain a manual slot with
// [Bulkhead.Acquire], inspect counters with [Bulkhead.Stats], and release
// resources with [Bulkhead.Close].
//
// It is safe for concurrent use from multiple goroutines. The slot count is
// enforced by a buffered channel semaphore; the surrounding counters live in
// lock-free atomics.
type Bulkhead struct {
	cfg       config
	sem       chan struct{}
	active    atomic.Int64
	waiters   atomic.Int64
	executed  atomic.Uint64
	rejected  atomic.Uint64
	timeouts  atomic.Uint64
	closed    atomic.Bool
	closeOnce sync.Once
	closedCh  chan struct{}
}

// New creates a [Bulkhead] with the given options applied on top of the package
// defaults ([DefaultMaxConcurrent] slots, [DefaultTimeout] wait). A non-positive
// slot count is floored to 1, so New never returns an unusable bulkhead.
func New(opts ...Option) *Bulkhead {
	cfg := newConfig(opts)
	return &Bulkhead{
		cfg:      cfg,
		sem:      make(chan struct{}, cfg.maxConcurrent),
		closedCh: make(chan struct{}),
	}
}

// Allow reports whether a slot is currently free without reserving it. It does
// not track anything or mutate any counter; use [Execute], [TryExecute], or
// [Bulkhead.Acquire] for tracked admission. Returns false once the bulkhead is
// closed.
//
// Allow is a best-effort hint: it inspects the live in-flight count without
// claiming a slot, so a concurrent admission may change the outcome before the
// caller acts. Only the tracked entry points enforce the concurrency bound.
func (b *Bulkhead) Allow() bool {
	if b.closed.Load() {
		return false
	}
	return int(b.active.Load()) < b.cfg.maxConcurrent
}

// Token represents one acquired, in-flight slot obtained from
// [Bulkhead.Acquire]. Release exactly once when the operation completes to free
// the slot.
type Token struct {
	bulkhead *Bulkhead
	done     atomic.Bool
}

// Acquire claims a slot, blocking until one is free, the context is cancelled,
// or the configured timeout elapses. It returns a [Token] that must be released
// with [Token.Release].
//
// Acquire is the building block for code that cannot use the callback form of
// [Execute] (for example, when slot ownership must span multiple statements).
// The caller owns the returned token and must release it.
//
// Returns [ErrClosed] if the bulkhead is closed, [ErrCancelled] if the context
// is cancelled while waiting, [ErrTimeout] if the timeout fires first, and
// [ErrWaitersExceeded] if [WithMaxWaiters] is set and the waiter cap is full.
func (b *Bulkhead) Acquire(ctx context.Context) (*Token, error) {
	if _, err := b.reserve(ctx); err != nil {
		return nil, err
	}
	b.active.Add(1)
	return &Token{bulkhead: b}, nil
}

// Release frees the slot held by the token. It is safe to call multiple times;
// only the first call has an effect. A nil token is a no-op.
func (t *Token) Release() {
	if t == nil || !t.done.CompareAndSwap(false, true) {
		return
	}
	t.bulkhead.releaseSlot()
}

// reserve performs the three-phase slot acquisition shared by [Execute] and
// [Bulkhead.Acquire]:
//
//	(1) fast-reject if the bulkhead is closed or the context is already done,
//	(2) optimistic non-blocking attempt that allocates no timer,
//	(3) slow path with a timer when all slots are busy.
//
// On success the semaphore slot is held and the caller must release it; the
// returned waited flag reports whether acquisition went through the slow path.
// On failure no slot is consumed and the relevant counter is incremented.
func (b *Bulkhead) reserve(ctx context.Context) (waited bool, err error) {
	if b.closed.Load() {
		return false, ErrClosed
	}
	if cerr := ctx.Err(); cerr != nil {
		b.rejected.Add(1)
		return false, errCancelled(cerr)
	}

	if b.waiters.Load() == 0 {
		select {
		case b.sem <- struct{}{}:
			if b.waiters.Load() > 0 {
				<-b.sem
				return b.reserveSlow(ctx)
			}
			return b.finishReserve(ctx, false)
		default:
		}
	}

	return b.reserveSlow(ctx)
}

// reserveSlow waits for a slot with a timer. It always counts this caller as a
// waiter so [TryExecute] and the reserve fast path cannot barge ahead. When
// [WithMaxWaiters] is set, excess waiters are rejected immediately.
func (b *Bulkhead) reserveSlow(ctx context.Context) (waited bool, err error) {
	n := b.waiters.Add(1)
	defer b.waiters.Add(-1)
	if b.cfg.maxWaiters > 0 && n > int64(b.cfg.maxWaiters) {
		b.rejected.Add(1)
		return false, ErrWaitersExceeded
	}

	timer := time.NewTimer(slotWait(ctx, b.cfg.timeout))
	select {
	case b.sem <- struct{}{}:
		stopTimer(timer)
		return b.finishReserve(ctx, true)
	case <-ctx.Done():
		stopTimer(timer)
		b.rejected.Add(1)
		return false, errCancelled(ctx.Err())
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			b.rejected.Add(1)
			return false, errCancelled(err)
		}
		b.timeouts.Add(1)
		return false, ErrTimeout
	case <-b.closedCh:
		stopTimer(timer)
		b.rejected.Add(1)
		return false, ErrClosed
	}
}

// finishReserve validates the bulkhead is still open and the context is still
// live after a semaphore send. A closed bulkhead or a cancelled context refunds
// the slot so no operation is admitted after shutdown or cancel.
func (b *Bulkhead) finishReserve(ctx context.Context, waited bool) (bool, error) {
	if _, err := b.commitSlot(waited); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		b.releaseUncommittedSlot()
		b.rejected.Add(1)
		return false, errCancelled(err)
	}
	return waited, nil
}

// slotWait returns min(timeout, time remaining until ctx's deadline). A
// deadline that has already passed yields a zero duration so the timer fires
// immediately; the caller still re-checks ctx.Err() on timer expiry.
func slotWait(ctx context.Context, timeout time.Duration) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return timeout
	}
	remaining := time.Until(deadline)
	if remaining < timeout {
		if remaining < 0 {
			return 0
		}
		return remaining
	}
	return timeout
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

// commitSlot validates the bulkhead is still open after a semaphore send.
// If [Bulkhead.Close] ran concurrently, the slot is released and [ErrClosed]
// is returned so no operation is admitted after shutdown.
func (b *Bulkhead) commitSlot(waited bool) (bool, error) {
	if !b.closed.Load() {
		return waited, nil
	}
	<-b.sem
	b.rejected.Add(1)
	return false, ErrClosed
}

// releaseUncommittedSlot returns a semaphore token that was acquired but not
// yet handed to a callback or token owner. Active is not decremented because
// it was never incremented for this reservation.
func (b *Bulkhead) releaseUncommittedSlot() {
	<-b.sem
}

// releaseSlot drops the in-flight count first, then frees the semaphore. The
// order is load-bearing: freeing the semaphore first lets another goroutine
// claim a slot and increment active before this caller decrements, so
// [Bulkhead.Active] can briefly exceed maxConcurrent. Token.Release and the
// [run] defer share this helper.
func (b *Bulkhead) releaseSlot() {
	b.active.Add(-1)
	<-b.sem
}

// run executes fn inside an already-held semaphore slot, maintaining counters
// and releasing the slot on return — even if fn panics.
func run[T any](b *Bulkhead, ctx context.Context, waited bool, op string, fn func(ctx context.Context, bc BulkController) (T, error)) (T, error) {
	active := b.active.Add(1)
	defer b.releaseSlot()
	b.executed.Add(1)

	bc := &execution{
		active:        int(active),
		maxConcurrent: b.cfg.maxConcurrent,
		waitedSlot:    waited,
	}
	return panix.Safe(op, func() (T, error) {
		return fn(ctx, bc)
	})
}

// Execute runs fn within the bulkhead. Because Go methods cannot have type
// parameters, Execute is a package-level generic function taking the [Bulkhead]
// as its first argument.
//
// It acquires a slot via a three-phase strategy: fast-reject on a cancelled
// context, an optimistic non-blocking attempt that allocates no timer, then a
// timed wait when all slots are busy. Once admitted the slot is held for the
// duration of fn and released even if fn panics: the callback runs under
// [panix.Safe], so a panic becomes a [*panix.PanicError].
//
// The callback receives the original ctx and a [BulkController] carrying the
// occupancy snapshot at admission time.
//
// Returns [ErrClosed] if the bulkhead is closed, [ErrNilFunc] if fn is nil,
// [ErrCancelled] if the context is cancelled before a slot is acquired (no slot
// consumed), [ErrTimeout] if the timeout fires before a slot frees up, or
// [ErrWaitersExceeded] if [WithMaxWaiters] is set and the waiter cap is full.
func Execute[T any](b *Bulkhead, ctx context.Context, fn func(ctx context.Context, bc BulkController) (T, error)) (T, error) {
	var zero T
	if b.closed.Load() {
		return zero, ErrClosed
	}
	if fn == nil {
		return zero, ErrNilFunc
	}
	waited, err := b.reserve(ctx)
	if err != nil {
		return zero, err
	}
	return run(b, ctx, waited, b.cfg.opOrDefault(), fn)
}

// TryExecute attempts to run fn without blocking. If a slot is immediately
// available and no caller is waiting on the slow path, the function executes
// and TryExecute returns (true, val, err). If no slot is free, or waiters are
// already queued, it returns (false, zero, nil) without invoking fn and counts
// a rejection. Fast-path callers never barge ahead of a waiter.
//
// Returns (false, zero, [ErrClosed]) if the bulkhead is closed,
// (false, zero, [ErrNilFunc]) if fn is nil, and (false, zero, [ErrCancelled])
// when ctx is already cancelled (no slot consumed). As with [Execute], the
// callback runs under [panix.Safe] and the slot is released even on panic.
func TryExecute[T any](b *Bulkhead, ctx context.Context, fn func(ctx context.Context, bc BulkController) (T, error)) (bool, T, error) {
	var zero T
	if b.closed.Load() {
		return false, zero, ErrClosed
	}
	if fn == nil {
		return false, zero, ErrNilFunc
	}
	if err := ctx.Err(); err != nil {
		return false, zero, errCancelled(err)
	}

	if b.waiters.Load() > 0 {
		b.rejected.Add(1)
		return false, zero, nil
	}

	select {
	case b.sem <- struct{}{}:
		if b.waiters.Load() > 0 {
			<-b.sem
			b.rejected.Add(1)
			return false, zero, nil
		}
		if _, err := b.finishReserve(ctx, false); err != nil {
			return false, zero, err
		}
		val, err := run(b, ctx, false, b.cfg.opOrDefaultTry(), fn)
		return true, val, err
	default:
		b.rejected.Add(1)
		return false, zero, nil
	}
}

// Active returns the number of operations currently executing inside the
// bulkhead.
func (b *Bulkhead) Active() int {
	return int(b.active.Load())
}

// MaxConcurrent returns the configured maximum number of concurrent operations.
func (b *Bulkhead) MaxConcurrent() int {
	return b.cfg.maxConcurrent
}

// Load returns the current occupancy fraction (active/maxConcurrent), in
// [0, 1].
func (b *Bulkhead) Load() float64 {
	return float64(b.active.Load()) / float64(b.cfg.maxConcurrent)
}

// Stats holds a point-in-time snapshot of bulkhead counters.
type Stats struct {
	MaxConcurrent int    `json:"max_concurrent"`
	Active        int    `json:"active"`
	Waiters       int    `json:"waiters"`
	Executed      uint64 `json:"executed"`
	Rejected      uint64 `json:"rejected"`
	Timeouts      uint64 `json:"timeouts"`
}

// Stats returns a snapshot of bulkhead statistics.
func (b *Bulkhead) Stats() Stats {
	return Stats{
		MaxConcurrent: b.cfg.maxConcurrent,
		Active:        int(b.active.Load()),
		Waiters:       int(b.waiters.Load()),
		Executed:      b.executed.Load(),
		Rejected:      b.rejected.Load(),
		Timeouts:      b.timeouts.Load(),
	}
}

// ResetStats zeroes the cumulative counters (executed, rejected, timeouts). It
// does not affect the active count or the closed state.
func (b *Bulkhead) ResetStats() {
	b.executed.Store(0)
	b.rejected.Store(0)
	b.timeouts.Store(0)
}

// Close shuts the bulkhead down: subsequent [Execute], [TryExecute], and
// [Bulkhead.Acquire] calls return [ErrClosed]. Goroutines blocked waiting for
// a slot on the slow path are released immediately with [ErrClosed]. In-flight
// operations are unaffected and their slots are released normally. Close is
// idempotent and always returns nil; the error return satisfies the common
// closer contract used across urx.
func (b *Bulkhead) Close() error {
	b.closed.Store(true)
	b.closeOnce.Do(func() { close(b.closedCh) })
	return nil
}

// IsClosed reports whether [Bulkhead.Close] has been called.
func (b *Bulkhead) IsClosed() bool {
	return b.closed.Load()
}
