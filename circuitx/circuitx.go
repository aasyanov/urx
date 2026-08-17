// Package circuitx provides a thread-safe circuit breaker for production Go
// services.
//
// A [Breaker] monitors failures and moves between three states: [Closed]
// (healthy), [Open] (tripped), and [HalfOpen] (probing). While Closed, calls
// pass through and consecutive failures are counted; when the count reaches the
// configured threshold the breaker trips Open and rejects calls immediately
// with [ErrOpen] from [Execute] or (false, zero, nil) from [TryExecute], shedding
// load from a failing downstream. After a reset
// timeout it moves to HalfOpen and admits a bounded number of probe calls;
// consecutive probe successes close the breaker (see [WithSuccessThreshold]),
// and a probe failure re-opens it.
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
	"errors"
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

	state             atomic.Uint32 // current State
	failures          atomic.Int32  // consecutive failures in Closed
	lastOpen          atomic.Int64  // UnixNano of the last Open transition
	halfOpenInflight  atomic.Int32  // probes currently admitted in HalfOpen
	halfOpenSuccesses atomic.Int32  // consecutive probe successes in HalfOpen
	generation        atomic.Uint64 // HalfOpen epoch; stale probes must not settle

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
		b.generation.Add(1)
		b.halfOpenInflight.Store(0)
		b.halfOpenSuccesses.Store(0)
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
// counter and the half-open probe budget. It always bumps the generation so
// in-flight probes admitted before Reset cannot heal, trip, or fail Closed.
// It does not affect the cumulative [Stats] counters. In-flight probes are
// allowed to finish; their deferred slot release is a no-op once the
// generation has moved, so the counter never goes negative. Use it to clear a
// tripped breaker after an out-of-band recovery signal. Reset fires the
// [WithOnStateChange] hook when it changes the state. It runs under the
// transition mutex so it never races a concurrent trip or probe settlement.
func (b *Breaker) Reset() {
	b.mu.Lock()
	b.failures.Store(0)
	prev := State(b.state.Swap(uint32(Closed)))
	b.generation.Add(1)
	b.halfOpenInflight.Store(0)
	b.halfOpenSuccesses.Store(0)
	b.mu.Unlock()
	if prev != Closed {
		b.fireStateChange(prev, Closed)
	}
}

// Stats returns a read-only snapshot of breaker statistics. Unlike [Breaker.State],
// Stats does not promote [Open] to [HalfOpen] and never fires the
// [WithOnStateChange] hook. It is safe to call concurrently with [Execute] and
// [TryExecute]; counters are read independently and may reflect a call in
// progress.
func (b *Breaker) Stats() Stats {
	return Stats{
		State:       b.snapshotState(),
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
// On success the breaker records the call. A success in [Closed] clears the
// consecutive-failure counter. A success in [HalfOpen] heals to [Closed] only
// after [WithSuccessThreshold] consecutive probe successes (default 1) — one
// probe success does not always close the breaker. On a counted failure it
// records the failure and may trip to [Open] once the consecutive-failure
// threshold is reached, or immediately if the failure occurred in [HalfOpen] or
// the callback called [CircuitController.Trip]. The callback's return value is
// always passed through to the caller together with any error.
//
// A failure marked with [CircuitController.SkipFailure] is returned to the
// caller but not counted (SkipFailure always wins). After admission,
// [context.Canceled] is not counted as a downstream failure unless
// [WithCountCanceled] is set; [context.DeadlineExceeded] is still counted. When
// [WithFailureIf] is set it classifies remaining errors (it is not asked for a
// [context.Canceled] that is being ignored). A panic recovered by [panix.Safe]
// is always counted.
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

	state, gen, ok := b.tryAdmit()
	if !ok {
		return zero, ErrOpen
	}
	return executeRun(b, ctx, b.cfg.opOrDefault(), state, gen, fn)
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

	state, gen, ok := b.tryAdmit()
	if !ok {
		return false, zero, nil
	}
	val, err := executeRun(b, ctx, b.cfg.opOrDefaultTry(), state, gen, fn)
	return true, val, err
}

// tryAdmit evaluates circuit admission after guard checks. It returns the state
// and HalfOpen generation at admission and whether the call was let through.
// A rejection increments the rejected counter. Generation is captured here —
// immediately after [Breaker.State] may have promoted Open→HalfOpen — so a
// later Reset cannot be adopted by a probe that reserved a slot in an older
// epoch.
func (b *Breaker) tryAdmit() (state State, gen uint64, ok bool) {
	state = b.State()
	gen = b.generation.Load()
	if state == Open {
		b.rejected.Add(1)
		return state, gen, false
	}

	if state == HalfOpen {
		if !b.tryAdmitProbe() {
			b.rejected.Add(1)
			return state, gen, false
		}
	}
	return state, gen, true
}

// snapshotState returns the live circuit state without lazy Open→HalfOpen
// promotion. Read-only introspection paths such as [Breaker.Stats] use this so
// metrics polling never drives state transitions.
func (b *Breaker) snapshotState() State {
	return State(b.state.Load())
}

// releaseProbe frees one half-open probe slot belonging to admitGen. It is a
// no-op when admitGen does not match the live generation (the probe is stale
// after Open→HalfOpen or Reset-to-Closed). Otherwise it never drives the
// in-flight count below zero, so a concurrent [Breaker.Reset] that cleared the
// budget cannot be clobbered by a finishing probe's deferred release.
func (b *Breaker) releaseProbe(admitGen uint64) {
	if admitGen != b.generation.Load() {
		return
	}
	for {
		cur := b.halfOpenInflight.Load()
		if cur <= 0 {
			return
		}
		if b.halfOpenInflight.CompareAndSwap(cur, cur-1) {
			return
		}
	}
}

// executeRun runs fn after admission and settles the outcome on the breaker.
// The caller must have already passed guard checks and won admission via tryAdmit.
func executeRun[T any](b *Breaker, ctx context.Context, op string, state State, admitGen uint64, fn CircuitFunc[T]) (T, error) {
	if state == HalfOpen {
		defer b.releaseProbe(admitGen)
	}

	cc := &execution{
		state:       state,
		failures:    int(b.failures.Load()),
		maxFailures: b.cfg.maxFailures,
	}

	val, err := panix.Safe(op, func() (T, error) {
		return fn(ctx, cc)
	})

	if err != nil && !cc.skipFailure && b.countsAsFailure(err) {
		b.totalFail.Add(1)
		b.recordFailure(cc.tripped, admitGen)
		return val, err
	}

	if cc.tripped {
		b.openFrom(state, admitGen)
		return val, err
	}

	if err != nil {
		return val, err
	}

	b.successes.Add(1)
	b.recordSuccess(admitGen)
	return val, nil
}

// countsAsFailure reports whether err should drive the consecutive-failure
// state machine. [CircuitController.SkipFailure] is checked by the caller and
// always wins. A recovered [*panix.PanicError] always counts.
// [context.Canceled] does not count unless [WithCountCanceled] is set, and
// [WithFailureIf] is not consulted for an ignored cancel. Remaining errors —
// including [context.DeadlineExceeded] — count unless a [WithFailureIf]
// predicate returns false.
func (b *Breaker) countsAsFailure(err error) bool {
	var pe *panix.PanicError
	if errors.As(err, &pe) {
		return true
	}
	if errors.Is(err, context.Canceled) && !b.cfg.countCanceled {
		return false
	}
	if b.cfg.failureIf != nil {
		return b.cfg.failureIf(err)
	}
	return true
}

// recordSuccess settles a successful call. Consecutive probe successes in
// [HalfOpen] heal the breaker to [Closed] once [WithSuccessThreshold] is
// reached; a success in [Closed] clears any accumulated consecutive failures.
// A probe whose admit generation does not match the live generation is ignored
// so it cannot heal a newer HalfOpen epoch. The state-changing work happens
// under the transition mutex and is re-checked against the live state, so a
// success can never clobber a trip that a concurrent failure committed between
// admission and settlement: when the live state is [Open] the success is
// ignored and the breaker stays open.
func (b *Breaker) recordSuccess(admitGen uint64) {
	// Fast path: a healthy Closed breaker with no pending failures has nothing
	// to settle, so the common success avoids the mutex entirely. Both loads are
	// atomic; if either is stale the slow path below re-checks under the lock.
	if State(b.state.Load()) == Closed && b.failures.Load() == 0 {
		return
	}

	b.mu.Lock()
	if admitGen != b.generation.Load() {
		b.mu.Unlock()
		return
	}
	switch State(b.state.Load()) {
	case HalfOpen:
		count := b.halfOpenSuccesses.Add(1)
		if int(count) < b.cfg.successThreshold {
			b.mu.Unlock()
			return
		}
		// Enough consecutive probe successes: the downstream has recovered.
		// Bump generation so a leftover in-flight probe cannot fail Closed.
		b.halfOpenSuccesses.Store(0)
		b.failures.Store(0)
		b.generation.Add(1)
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
// count reaches the threshold. A probe whose admit generation does not match
// the live generation is ignored so it cannot re-open a newer epoch. The
// failure count is incremented and evaluated under the transition mutex so it
// stays consistent with a concurrent success reset and the trip decision sees a
// stable count.
func (b *Breaker) recordFailure(forced bool, admitGen uint64) {
	b.mu.Lock()
	if admitGen != b.generation.Load() {
		b.mu.Unlock()
		return
	}
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

	if live == HalfOpen {
		b.halfOpenSuccesses.Store(0)
	}

	// Trip: forced, a probe failure in HalfOpen, or the threshold was reached.
	b.state.Store(uint32(Open))
	b.lastOpen.Store(time.Now().UnixNano())
	b.trips.Add(1)
	b.mu.Unlock()
	b.fireStateChange(live, Open)
}

// openFrom trips the circuit to [Open] unconditionally (used by the forced
// [CircuitController.Trip] path on a successful or skipped call). A probe whose
// admit generation does not match the live generation is ignored so a stale
// Trip cannot force a newer HalfOpen epoch Open. The mutex makes the edge
// atomic so the trip count and the [WithOnStateChange] hook fire exactly once
// even under concurrent transitions.
func (b *Breaker) openFrom(state State, admitGen uint64) {
	b.mu.Lock()
	if admitGen != b.generation.Load() {
		b.mu.Unlock()
		return
	}
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
// the from→to edge. The hook runs synchronously on the driving goroutine; a
// panic is recovered so it cannot crash the caller. A nil hook is a no-op.
func (b *Breaker) fireStateChange(from, to State) {
	fn := b.cfg.onStateChange
	if fn == nil {
		return
	}
	defer func() { _ = recover() }()
	fn(from, to)
}
