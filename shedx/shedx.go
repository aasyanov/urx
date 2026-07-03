// Package shedx provides priority-based load shedding for industrial Go
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
// degradation. For tracked admission without a callback, use [Acquire] and
// release the returned [Token].
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
	// opExecute labels panics recovered while running an Execute callback and
	// is the default operation name when none is configured.
	opExecute = "shedx.Execute"

	// Progressive shedding cutoffs. overload is the fraction of the band above
	// the threshold, in [0, 1]: a priority is admitted while overload is below
	// its cutoff. Critical requests bypass these entirely.
	cutoffLow    = 0.25
	cutoffNormal = 0.60
	cutoffHigh   = 0.90
)

// Shedder is a thread-safe, priority-based load shedder. Create one with [New],
// admit work with [Execute] or [Acquire], inspect counters with
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
// [Execute] or [Acquire] for tracked admission. Returns false once the shedder
// is closed.
//
// Allow is a best-effort hint: it inspects the live in-flight count without
// reserving a slot, so a concurrent admission may change the outcome before the
// caller acts. Only [Execute] and [Acquire] enforce the capacity bound.
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
// [Execute] (for example, when in-flight tracking must span multiple
// statements). The caller owns the returned token and must release it.
func (s *Shedder) Acquire(priority Priority) (*Token, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	if _, ok := s.tryReserve(priority); !ok {
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
func (s *Shedder) tryReserve(priority Priority) (int64, bool) {
	for {
		cur := s.inflight.Load()
		next := cur + 1
		if !s.admits(priority, next) {
			return 0, false
		}
		if s.inflight.CompareAndSwap(cur, next) {
			return next, true
		}
	}
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
func Execute[T any](s *Shedder, ctx context.Context, priority Priority, fn func(ctx context.Context, sc ShedController) (T, error)) (T, error) {
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

	n, ok := s.tryReserve(priority)
	if !ok {
		s.shed.Add(1)
		return zero, errRejected(priority)
	}
	s.admitted.Add(1)
	defer s.inflight.Add(-1)

	sc := &execution{
		priority: priority,
		load:     float64(n-1) / float64(s.cfg.capacity),
		inFlight: n - 1,
		capacity: s.cfg.capacity,
	}

	val, err := panix.Safe(s.cfg.opOrDefault(), func() (T, error) {
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
	if priority >= PriorityCritical {
		return true
	}

	load := float64(inflight) / float64(s.cfg.capacity)
	if load < s.cfg.threshold {
		return true
	}

	overload := (load - s.cfg.threshold) / (thresholdCeil - s.cfg.threshold)
	switch priority {
	case PriorityLow:
		return overload < cutoffLow
	case PriorityNormal:
		return overload < cutoffNormal
	default:
		// PriorityHigh is the only remaining value below PriorityCritical, which
		// returned above; anything else is treated as high.
		return overload < cutoffHigh
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

// Stats holds a point-in-time snapshot of shedder counters.
type Stats struct {
	Capacity  int     `json:"capacity"`
	Threshold float64 `json:"threshold"`
	InFlight  int64   `json:"in_flight"`
	Admitted  int64   `json:"admitted"`
	Shed      int64   `json:"shed"`
	Degraded  int64   `json:"degraded"`
}

// Stats returns a snapshot of shedder statistics.
func (s *Shedder) Stats() Stats {
	return Stats{
		Capacity:  s.cfg.capacity,
		Threshold: s.cfg.threshold,
		InFlight:  s.inflight.Load(),
		Admitted:  s.admitted.Load(),
		Shed:      s.shed.Load(),
		Degraded:  s.degraded.Load(),
	}
}

// ResetStats zeroes the cumulative counters (admitted, shed, degraded). It does
// not affect the in-flight count or the closed state.
func (s *Shedder) ResetStats() {
	s.admitted.Store(0)
	s.shed.Store(0)
	s.degraded.Store(0)
}

// Close shuts the shedder down: subsequent [Execute] and [Acquire] calls return
// [ErrClosed]. In-flight operations are unaffected and their tokens may still
// be released. Close is idempotent and always returns nil; the error return
// satisfies the common closer contract used across urx.
func (s *Shedder) Close() error {
	s.closed.Store(true)
	return nil
}

// IsClosed reports whether [Shedder.Close] has been called.
func (s *Shedder) IsClosed() bool {
	return s.closed.Load()
}
