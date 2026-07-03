// Package circuitx provides a thread-safe circuit breaker for production Go
// services.
//
// A [Breaker] monitors failures and moves between three states: [Closed]
// (healthy), [Open] (tripped), and [HalfOpen] (probing). While Closed, calls
// pass through and consecutive failures are counted; when the count reaches the
// configured threshold the breaker trips Open and rejects calls immediately
// with [ErrOpen] from [Execute] or (false, zero, nil) from [TryExecute], shedding
// load from a failing downstream. After a reset
// timeout it moves to HalfOpen and admits a bounded number of probe calls; a
// probe success closes the breaker, a probe failure re-opens it.
//
//	cb := circuitx.New(
//	    circuitx.WithMaxFailures(5),
//	    circuitx.WithResetTimeout(10*time.Second),
//	)
//
//	resp, err := circuitx.Execute(cb, ctx,
//	    func(ctx context.Context, cc circuitx.CircuitController) (*Response, error) {
//	        if cc.State() == circuitx.HalfOpen {
//	            return nil, client.HealthCheck(ctx)
//	        }
//	        return client.Call(ctx, req)
//	    })
//
// Because Go methods cannot have type parameters, the primary entry point is the
// package-level generic function [Execute], taking the [Breaker] as its first
// argument. [TryExecute] is the non-blocking variant: when the circuit rejects
// a call it returns (false, zero, nil) instead of [ErrOpen]. The callback
// receives a [CircuitController] exposing the circuit
// state and failure count at admission time, a [CircuitController.SkipFailure]
// method to keep business errors from tripping the breaker, and a
// [CircuitController.Trip] method to force the breaker open early.
//
// Each callback runs under [github.com/aasyanov/urx/panix]: a panicking function
// becomes a [*panix.PanicError] instead of crashing the process and is treated
// as a circuit failure.
//
// # Dependencies
//
// circuitx depends only on the Go standard library and the urx panix package.
package circuitx

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/panix"
)

// Breaker is a thread-safe circuit breaker. Create one with [New], run work with
// the package-level [Execute] or [TryExecute], inspect the circuit with [Breaker.State],
// [Breaker.Failures], or [Breaker.Stats], force it back to [Closed] with
// [Breaker.Reset], and release it with [Breaker.Close].
//
// A Breaker is safe for concurrent use from any number of goroutines. The state
// machine is driven by lock-free atomics; a single mutex serializes state
// transitions so the [WithOnStateChange] hook fires exactly once per edge.
type Breaker struct {
	cfg config

	state            atomic.Uint32 // current State
	failures         atomic.Int32  // consecutive failures in Closed
	lastOpen         atomic.Int64  // UnixNano of the last Open transition
	halfOpenInflight atomic.Int32  // probes currently admitted in HalfOpen

	successes atomic.Uint64
	totalFail atomic.Uint64
	rejected  atomic.Uint64
	trips     atomic.Uint64

	closed atomic.Bool

	mu sync.Mutex // serializes state transitions and the onStateChange hook
}

// New creates a [Breaker] with the given options applied on top of the package
// defaults ([DefaultMaxFailures] consecutive failures, [DefaultResetTimeout]
// reset, [DefaultHalfOpenMax] probe). Invalid options are clamped, so New never
// returns an unusable breaker. The breaker starts in the [Closed] state.
func New(opts ...Option) *Breaker {
	return &Breaker{cfg: newConfig(opts)}
}

// State returns the current circuit state. If the circuit is [Open] and the
// reset timeout has elapsed, State promotes it to [HalfOpen] (firing the
// [WithOnStateChange] hook) and returns [HalfOpen]; the compare-and-swap ensures
// only one caller drives the transition. State does not consume a probe slot —
// that happens in [Execute] and [TryExecute].
func (b *Breaker) State() State {
	s := State(b.state.Load())
	if s != Open {
		return s
	}
	last := time.Unix(0, b.lastOpen.Load())
	if time.Since(last) < b.cfg.resetTimeout {
		return Open
	}
	if b.state.CompareAndSwap(uint32(Open), uint32(HalfOpen)) {
		b.halfOpenInflight.Store(0)
		b.fireStateChange(Open, HalfOpen)
		return HalfOpen
	}
	// The CAS lost a race: another caller already promoted to HalfOpen, or a
	// concurrent Reset/trip changed the state. Report whatever is now live.
	return State(b.state.Load())
}

// Failures returns the current consecutive failure count.
func (b *Breaker) Failures() int {
	return int(b.failures.Load())
}

// Reset forces the circuit back to [Closed] and clears the consecutive failure
// counter. It does not affect the cumulative [Stats] counters. Use it to clear a
// tripped breaker after an out-of-band recovery signal. Reset fires the
// [WithOnStateChange] hook when it changes the state. It runs under the
// transition mutex so it never races a concurrent trip or probe settlement.
func (b *Breaker) Reset() {
	b.mu.Lock()
	b.failures.Store(0)
	b.halfOpenInflight.Store(0)
	prev := State(b.state.Swap(uint32(Closed)))
	b.mu.Unlock()
	if prev != Closed {
		b.fireStateChange(prev, Closed)
	}
}

// Stats returns a snapshot of breaker statistics. It is safe to call
// concurrently with [Execute] and [TryExecute]; counters are read independently and may reflect a
// call in progress.
func (b *Breaker) Stats() Stats {
	return Stats{
		State:       b.State(),
		Failures:    int(b.failures.Load()),
		MaxFailures: b.cfg.maxFailures,
		Successes:   b.successes.Load(),
		TotalFail:   b.totalFail.Load(),
		Rejected:    b.rejected.Load(),
		Trips:       b.trips.Load(),
	}
}

// ResetStats zeroes the cumulative counters (successes, failures, rejected,
// trips). It does not affect the circuit state or the consecutive failure count.
func (b *Breaker) ResetStats() {
	b.successes.Store(0)
	b.totalFail.Store(0)
	b.rejected.Store(0)
	b.trips.Store(0)
}

// Close shuts the breaker down: subsequent [Execute] and [TryExecute] calls return [ErrClosed].
// Close is idempotent and always returns nil; the error return satisfies the
// common closer contract used across urx. It does not reset counters, so a
// final [Breaker.Stats] snapshot remains available for inspection.
func (b *Breaker) Close() error {
	b.closed.Store(true)
	return nil
}

// IsClosed reports whether [Breaker.Close] has been called. It is unrelated to
// the [Closed] circuit state reported by [Breaker.State].
func (b *Breaker) IsClosed() bool {
	return b.closed.Load()
}

// Execute runs fn within the circuit breaker. Because Go methods cannot have
// type parameters, Execute is a package-level generic function taking the
// [Breaker] as its first argument.
//
// Execute returns [ErrClosed] if the breaker is closed, [ErrNilFunc] if fn is
// nil, and [ErrCancelled] if ctx is already cancelled (the circuit is left
// untouched). If the circuit is [Open] — or [HalfOpen] with the probe budget
// already in use — the call is rejected with [ErrOpen] without invoking fn.
// Otherwise fn runs under [panix.Safe]: a panic becomes a [*panix.PanicError]
// and is treated as a failure.
//
// On success the breaker records the call and, if it was probing in [HalfOpen]
// or carrying failures in [Closed], resets to a clean [Closed]. On failure it
// records the failure and may trip to [Open] once the consecutive-failure
// threshold is reached, or immediately if the failure occurred in [HalfOpen] or
// the callback called [CircuitController.Trip]. A failure marked with
// [CircuitController.SkipFailure] is returned to the caller but not counted.
//
// The callback receives a [CircuitController] exposing the state and failure
// count at admission time.
func Execute[T any](b *Breaker, ctx context.Context, fn CircuitFunc[T]) (T, error) {
	var zero T
	if b.closed.Load() {
		return zero, ErrClosed
	}
	if fn == nil {
		return zero, ErrNilFunc
	}
	if err := ctx.Err(); err != nil {
		return zero, errCancelled(err)
	}

	state, ok := b.tryAdmit()
	if !ok {
		return zero, ErrOpen
	}
	return executeRun(b, ctx, b.cfg.opOrDefault(), state, fn)
}

// TryExecute attempts to run fn without blocking. If the circuit admits the call
// the function executes and TryExecute returns (true, val, err). If the circuit
// is [Open] — or [HalfOpen] with the probe budget already in use — it returns
// (false, zero, nil) without invoking fn and counts a rejection.
//
// Returns (false, zero, [ErrClosed]) if the breaker is closed,
// (false, zero, [ErrNilFunc]) if fn is nil, and (false, zero, [ErrCancelled])
// if ctx is already cancelled (the circuit is left untouched). When admitted,
// fn runs under [panix.Safe] with the same outcome semantics as [Execute].
func TryExecute[T any](b *Breaker, ctx context.Context, fn CircuitFunc[T]) (bool, T, error) {
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

	state, ok := b.tryAdmit()
	if !ok {
		return false, zero, nil
	}
	val, err := executeRun(b, ctx, b.cfg.opOrDefaultTry(), state, fn)
	return true, val, err
}

// tryAdmit evaluates circuit admission after guard checks. It returns the state
// at admission and whether the call was let through. A rejection increments
// the rejected counter.
func (b *Breaker) tryAdmit() (state State, ok bool) {
	state = b.State()
	if state == Open {
		b.rejected.Add(1)
		return state, false
	}

	if state == HalfOpen {
		if !b.tryAdmitProbe() {
			b.rejected.Add(1)
			return state, false
		}
	}
	return state, true
}

// executeRun runs fn after admission and settles the outcome on the breaker.
// The caller must have already passed guard checks and won admission via tryAdmit.
func executeRun[T any](b *Breaker, ctx context.Context, op string, state State, fn CircuitFunc[T]) (T, error) {
	var zero T
	if state == HalfOpen {
		defer b.halfOpenInflight.Add(-1)
	}

	cc := &execution{
		state:       state,
		failures:    int(b.failures.Load()),
		maxFailures: b.cfg.maxFailures,
	}

	val, err := panix.Safe(op, func() (T, error) {
		return fn(ctx, cc)
	})

	if err != nil && !cc.skipFailure {
		b.totalFail.Add(1)
		b.recordFailure(cc.tripped)
		return zero, err
	}

	if cc.tripped {
		b.openFrom(state)
		return val, err
	}

	if err != nil {
		return val, err
	}

	b.successes.Add(1)
	b.recordSuccess()
	return val, nil
}

// recordSuccess settles a successful call. A probe success in [HalfOpen] heals
// the breaker to [Closed]; a success in [Closed] clears any accumulated
// consecutive failures. The state-changing work happens under the transition
// mutex and is re-checked against the live state, so a success can never clobber
// a trip that a concurrent failure committed between admission and settlement:
// when the live state is [Open] the success is ignored and the breaker stays
// open.
func (b *Breaker) recordSuccess() {
	// Fast path: a healthy Closed breaker with no pending failures has nothing
	// to settle, so the common success avoids the mutex entirely. Both loads are
	// atomic; if either is stale the slow path below re-checks under the lock.
	if State(b.state.Load()) == Closed && b.failures.Load() == 0 {
		return
	}

	b.mu.Lock()
	switch State(b.state.Load()) {
	case HalfOpen:
		// Probe succeeded: the downstream has recovered.
		b.failures.Store(0)
		b.state.Store(uint32(Closed))
		b.mu.Unlock()
		b.fireStateChange(HalfOpen, Closed)
		return
	case Closed:
		// A run of failures interrupted by this success before reaching the
		// threshold is forgiven, without any state change.
		b.failures.Store(0)
	}
	b.mu.Unlock()
}

// tryAdmitProbe reserves one of the halfOpenMax probe slots, returning false
// when the budget is exhausted. It uses a compare-and-swap loop so the in-flight
// probe count never exceeds the configured budget under concurrent admission.
func (b *Breaker) tryAdmitProbe() bool {
	for {
		cur := b.halfOpenInflight.Load()
		if int(cur) >= b.cfg.halfOpenMax {
			return false
		}
		if b.halfOpenInflight.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// recordFailure registers a counted failure and trips the circuit when
// warranted: any failure observed in [HalfOpen] re-opens immediately, a forced
// Trip opens immediately, and a failure in [Closed] opens once the consecutive
// count reaches the threshold. The failure count is incremented and evaluated
// under the transition mutex so it stays consistent with a concurrent success
// reset and the trip decision sees a stable count.
func (b *Breaker) recordFailure(forced bool) {
	b.mu.Lock()
	live := State(b.state.Load())
	if live == Open {
		// Already tripped by a concurrent failure; nothing to do.
		b.mu.Unlock()
		return
	}

	count := b.failures.Add(1)
	if !forced && live == Closed && int(count) < b.cfg.maxFailures {
		// Below threshold in Closed: record the failure, stay closed.
		b.mu.Unlock()
		return
	}

	// Trip: forced, a probe failure in HalfOpen, or the threshold was reached.
	b.state.Store(uint32(Open))
	b.lastOpen.Store(time.Now().UnixNano())
	b.trips.Add(1)
	b.mu.Unlock()
	b.fireStateChange(live, Open)
}

// openFrom trips the circuit to [Open] unconditionally (used by the forced
// [CircuitController.Trip] path on a successful or skipped call). The mutex makes
// the edge atomic so the trip count and the [WithOnStateChange] hook fire
// exactly once even under concurrent transitions.
func (b *Breaker) openFrom(state State) {
	b.mu.Lock()
	prev := State(b.state.Load())
	if prev == Open {
		b.mu.Unlock()
		return
	}
	b.state.Store(uint32(Open))
	b.lastOpen.Store(time.Now().UnixNano())
	b.trips.Add(1)
	b.mu.Unlock()

	from := state
	if prev != state {
		from = prev
	}
	b.fireStateChange(from, Open)
}

// fireStateChange invokes the configured [WithOnStateChange] hook, if any, for
// the from→to edge. A nil hook is a no-op.
func (b *Breaker) fireStateChange(from, to State) {
	if b.cfg.onStateChange != nil {
		b.cfg.onStateChange(from, to)
	}
}
