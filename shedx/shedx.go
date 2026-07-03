// Package shedx provides priority-based load shedding for production Go
// services.
//
// A [Shedder] tracks in-flight operations and rejects new ones when the system
// is overloaded. Each request carries a [Priority]; lower-priority requests are
// shed first, while [PriorityCritical] requests are never rejected. Shedding
// protects a service from collapse: instead of accepting more work than it can
// finish and failing everything, it sheds the least important work early so the
// rest succeeds.
//
//	s := shedx.New(
//	    shedx.WithCapacity(1000),
//	    shedx.WithThreshold(0.8),
//	)
//	defer s.Close()
//
//	resp, err := shedx.Execute(s, ctx, shedx.PriorityNormal,
//	    func(ctx context.Context, sc shedx.ShedController) (*Response, error) {
//	        if sc.Load() > 0.9 {
//	            sc.Shed()
//	            return cachedResponse(ctx) // degrade gracefully under load
//	        }
//	        return handler.Serve(ctx, req)
//	    })
//
// The callback receives a [ShedController] exposing the load snapshot at
// admission time and a [ShedController.Shed] method to record graceful
// degradation. [TryExecute] is the non-blocking variant: when a request
// would be shed it returns (false, zero, nil) instead of [ErrRejected]. For
// tracked admission without a callback, use [Acquire] and release the returned
// [Token].
//
// Each callback is wrapped with [github.com/aasyanov/urx/panix] for panic
// recovery; a panicking function yields a [*panix.PanicError] instead of
// crashing the process, and the in-flight slot is always released.
//
// # Dependencies
//
// shedx depends only on the Go standard library and the urx panix package.
package shedx

import (
	"context"
	"sync/atomic"

	"github.com/aasyanov/urx/panix"
)

const (
	// opExecute labels panics recovered while running an [Execute] callback and
	// is the default operation name when none is configured.
	opExecute = "shedx.Execute"

	// opTryExecute labels panics recovered while running a [TryExecute] callback.
	opTryExecute = "shedx.TryExecute"
)

// Shedder is a thread-safe, priority-based load shedder. Create one with [New],
// admit work with [Execute], [TryExecute], or [Acquire], inspect counters with
// [Shedder.Stats], and release resources with [Shedder.Close].
//
// It is safe for concurrent use from multiple goroutines. All admission state
// is held in lock-free atomics; there is no mutex on the hot path.
type Shedder struct {
	cfg      config
	inflight atomic.Int64
	admitted atomic.Int64
	shed     atomic.Int64
	degraded atomic.Int64
	closed   atomic.Bool
}

// New creates a [Shedder] with the given options applied on top of the package
// defaults ([DefaultCapacity] in-flight slots, [DefaultThreshold] load). A
// non-positive capacity is floored to 1 and an out-of-range threshold falls
// back to the default, so New never returns an unusable shedder.
func New(opts ...Option) *Shedder {
	return &Shedder{cfg: newConfig(opts)}
}

// Allow reports whether a request with the given priority would be admitted at
// the current load. It does not track the request or mutate any counter; use
// [Execute], [TryExecute], or [Acquire] for tracked admission. Returns false once the shedder
// is closed.
//
// Allow is a best-effort hint: it inspects the live in-flight count without
// reserving a slot, so a concurrent admission may change the outcome before the
// caller acts. Only [Execute], [TryExecute], and [Acquire] enforce the capacity bound.
func (s *Shedder) Allow(priority Priority) bool {
	if s.closed.Load() {
		return false
	}
	return s.admits(priority, s.inflight.Load()+1)
}

// Token represents one admitted, in-flight operation obtained from [Acquire].
// Release exactly once when the operation completes to free the in-flight slot.
type Token struct {
	shedder *Shedder
	done    atomic.Bool
}

// Acquire admits a request of the given priority and returns a [Token] that
// must be released with [Token.Release]. It returns [ErrClosed] if the shedder
// is closed and [ErrRejected] if the request is shed for its priority.
//
// Acquire is the building block for code that cannot use the callback form of
// [Execute] or [TryExecute] (for example, when in-flight tracking must span multiple
// statements). The caller owns the returned token and must release it.
func (s *Shedder) Acquire(priority Priority) (*Token, error) {
	if _, ok, closed := s.tryReserve(priority); !ok {
		if closed {
			return nil, ErrClosed
		}
		s.shed.Add(1)
		return nil, errRejected(priority)
	}
	s.admitted.Add(1)
	return &Token{shedder: s}, nil
}

// tryReserve atomically claims one in-flight slot iff the request is admitted
// at the resulting count, returning the post-reservation count on success or 0
// on shed. It uses a compare-and-swap loop so the slot is committed only when
// admission holds for the exact value being stored: unlike a plain add-then-
// rollback, the in-flight counter is never transiently inflated past what is
// admitted, so concurrent observers never see a count above the capacity bound.
// The loop is lock-free and retries only under genuine contention on the slot.
// When the shedder is closed the loop exits with closed=true so callers return
// [ErrClosed] rather than [ErrRejected]. A successful CAS is finalized by
// [commitReservation], which rolls back when [Shedder.Close] wins the race.
func (s *Shedder) tryReserve(priority Priority) (int64, bool, bool) {
	for {
		if s.closed.Load() {
			return 0, false, true
		}
		cur := s.inflight.Load()
		next := cur + 1
		if !s.admits(priority, next) {
			return 0, false, false
		}
		if s.inflight.CompareAndSwap(cur, next) {
			return s.commitReservation(next)
		}
	}
}

// commitReservation validates the shedder is still open after inflight was
// incremented. If [Shedder.Close] ran concurrently, the reservation is rolled
// back and callers receive closed=true so no operation is admitted after
// shutdown.
func (s *Shedder) commitReservation(next int64) (int64, bool, bool) {
	if s.closed.Load() {
		s.inflight.Add(-1)
		return 0, false, true
	}
	return next, true, false
}

// Release frees the in-flight slot held by the token. It is safe to call
// multiple times; only the first call has an effect. A nil token is a no-op.
func (t *Token) Release() {
	if t == nil || !t.done.CompareAndSwap(false, true) {
		return
	}
	t.shedder.inflight.Add(-1)
}

// Execute admits a request of the given priority and runs fn if admitted.
// Because Go methods cannot have type parameters, Execute is a package-level
// generic function taking the [Shedder] as its first argument.
//
// Execute returns [ErrClosed] if the shedder is closed, [ErrNilFunc] if fn is
// nil, [ErrCancelled] if ctx is already cancelled (no slot consumed), and
// [ErrRejected] (without invoking fn) if the request is shed. On admission the
// in-flight slot is held for the duration of fn and released even if fn panics:
// the callback runs under [panix.Safe], so a panic becomes a
// [*panix.PanicError].
//
// The callback receives the original ctx and a [ShedController] carrying the
// load snapshot at admission time and a [ShedController.Shed] method to record
// graceful degradation.
func Execute[T any](s *Shedder, ctx context.Context, priority Priority, fn ShedFunc[T]) (T, error) {
	var zero T
	if s.closed.Load() {
		return zero, ErrClosed
	}
	if fn == nil {
		return zero, ErrNilFunc
	}
	if err := ctx.Err(); err != nil {
		return zero, errCancelled(err)
	}

	n, ok, closed := s.tryReserve(priority)
	if !ok {
		if closed {
			return zero, ErrClosed
		}
		s.shed.Add(1)
		return zero, errRejected(priority)
	}
	s.admitted.Add(1)
	return runAfterAdmit(s, ctx, priority, n, s.cfg.opOrDefault(), fn)
}

// TryExecute attempts to run fn without blocking. If the request is admitted at
// the current load the function executes and TryExecute returns (true, val, err).
// If the request would be shed it returns (false, zero, nil) without invoking
// fn and increments the shed counter.
//
// Returns (false, zero, [ErrClosed]) if the shedder is closed,
// (false, zero, [ErrNilFunc]) if fn is nil, and (false, zero, [ErrCancelled])
// when ctx is already cancelled (no slot consumed). As with [Execute], the
// callback runs under [panix.Safe] and the in-flight slot is released even on
// panic.
func TryExecute[T any](s *Shedder, ctx context.Context, priority Priority, fn ShedFunc[T]) (bool, T, error) {
	var zero T
	if s.closed.Load() {
		return false, zero, ErrClosed
	}
	if fn == nil {
		return false, zero, ErrNilFunc
	}
	if err := ctx.Err(); err != nil {
		return false, zero, errCancelled(err)
	}

	n, ok, closed := s.tryReserve(priority)
	if !ok {
		if closed {
			return false, zero, ErrClosed
		}
		s.shed.Add(1)
		return false, zero, nil
	}
	s.admitted.Add(1)
	val, err := runAfterAdmit(s, ctx, priority, n, s.cfg.opOrDefaultTry(), fn)
	return true, val, err
}

// runAfterAdmit executes fn after a slot has been reserved. The caller must
// have incremented admitted; this helper decrements inflight on return.
func runAfterAdmit[T any](s *Shedder, ctx context.Context, priority Priority, n int64, op string, fn ShedFunc[T]) (T, error) {
	defer s.inflight.Add(-1)

	sc := &execution{
		priority: priority,
		load:     float64(n-1) / float64(s.cfg.capacity),
		inFlight: n - 1,
		capacity: s.cfg.capacity,
	}

	val, err := panix.Safe(op, func() (T, error) {
		return fn(ctx, sc)
	})
	if sc.degraded {
		s.degraded.Add(1)
	}
	return val, err
}

// admits decides whether a request at the given priority is admitted given the
// post-reservation in-flight count (inclusive of the candidate). Critical
// requests always pass. Below the threshold everything passes; above it, each
// priority is admitted only while the overload fraction stays under its cutoff.
// It is pure: the count is supplied by the caller after reserving a slot.
func (s *Shedder) admits(priority Priority, inflight int64) bool {
	if priority == PriorityCritical {
		return true
	}

	load := float64(inflight) / float64(s.cfg.capacity)
	if load < s.cfg.threshold {
		return true
	}

	band := thresholdCeil - s.cfg.threshold
	if band <= 0 {
		return false
	}
	overload := (load - s.cfg.threshold) / band
	switch priority {
	case PriorityLow:
		return overload < s.cfg.cutoffLow
	case PriorityNormal:
		return overload < s.cfg.cutoffNormal
	default:
		// Values outside the named constants (including corrupt casts) are
		// treated like PriorityHigh — shed under severe overload only.
		return overload < s.cfg.cutoffHigh
	}
}

// Load returns the current load fraction (inflight/capacity), in [0, 1+].
func (s *Shedder) Load() float64 {
	return float64(s.inflight.Load()) / float64(s.cfg.capacity)
}

// InFlight returns the number of operations currently executing.
func (s *Shedder) InFlight() int64 {
	return s.inflight.Load()
}

// Capacity returns the configured maximum number of in-flight operations.
func (s *Shedder) Capacity() int {
	return s.cfg.capacity
}

// Threshold returns the configured load fraction at which shedding begins.
func (s *Shedder) Threshold() float64 {
	return s.cfg.threshold
}

// Stats holds a point-in-time snapshot of shedder configuration and counters.
type Stats struct {
	// Capacity is the configured maximum number of in-flight operations.
	Capacity int `json:"capacity"`
	// Threshold is the load fraction at which shedding begins.
	Threshold float64 `json:"threshold"`
	// CutoffLow is the overload fraction below which [PriorityLow] is admitted.
	CutoffLow float64 `json:"cutoff_low"`
	// CutoffNormal is the overload fraction below which [PriorityNormal] is admitted.
	CutoffNormal float64 `json:"cutoff_normal"`
	// CutoffHigh is the overload fraction below which [PriorityHigh] is admitted.
	CutoffHigh float64 `json:"cutoff_high"`
	// InFlight is the number of operations currently executing.
	InFlight int64 `json:"in_flight"`
	// Admitted is the cumulative count of admitted operations since creation or [Shedder.ResetStats].
	Admitted int64 `json:"admitted"`
	// Shed is the cumulative count of rejected admissions since creation or [Shedder.ResetStats].
	Shed int64 `json:"shed"`
	// Degraded is the cumulative count of graceful degradations recorded via [ShedController.Shed].
	Degraded int64 `json:"degraded"`
}

// Stats returns a snapshot of shedder statistics.
func (s *Shedder) Stats() Stats {
	return Stats{
		Capacity:     s.cfg.capacity,
		Threshold:    s.cfg.threshold,
		CutoffLow:    s.cfg.cutoffLow,
		CutoffNormal: s.cfg.cutoffNormal,
		CutoffHigh:   s.cfg.cutoffHigh,
		InFlight:     s.inflight.Load(),
		Admitted:     s.admitted.Load(),
		Shed:         s.shed.Load(),
		Degraded:     s.degraded.Load(),
	}
}

// ResetStats zeroes the cumulative counters (admitted, shed, degraded). It does
// not affect the in-flight count or the closed state.
func (s *Shedder) ResetStats() {
	s.admitted.Store(0)
	s.shed.Store(0)
	s.degraded.Store(0)
}

// Close shuts the shedder down: subsequent [Execute], [TryExecute], and
// [Acquire] calls return [ErrClosed]. Optimistic CAS reservations re-check
// closed via [commitReservation] and roll back when shutdown wins the race.
// In-flight operations are unaffected and their tokens may still be released.
// Close is idempotent and always returns nil; the error return satisfies the
// common closer contract used across urx.
func (s *Shedder) Close() error {
	s.closed.Store(true)
	return nil
}

// IsClosed reports whether [Shedder.Close] has been called.
func (s *Shedder) IsClosed() bool {
	return s.closed.Load()
}
