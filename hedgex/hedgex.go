// Package hedgex provides request hedging (speculative execution) for reducing
// tail latency in production Go services.
//
// A [Hedger] launches the same logical request as several copies with staggered
// delays and keeps the first successful result; the remaining in-flight copies
// are cancelled as soon as a winner arrives. Hedging trades a bounded amount of
// extra load for a dramatically tighter latency tail: a request that stalls on
// one slow backend is rescued by a fresh copy instead of dragging the p99.
//
//	h := hedgex.New(
//	    hedgex.WithDelay(50*time.Millisecond),
//	    hedgex.WithMaxParallel(3),
//	)
//
//	val, err := hedgex.Execute(h, ctx, func(ctx context.Context, hc hedgex.HedgeController) (string, error) {
//	    if hc.IsHedge() {
//	        return fetchFromReplica(ctx) // copy 2+ reads a replica
//	    }
//	    return fetchFromPrimary(ctx)
//	})
//
// The callback receives a [HedgeController] exposing which copy it is, how many
// copies were scheduled, and the elapsed time, plus a [HedgeController.Cancel]
// method so a copy can remove itself from the race when it knows it cannot win.
//
// Each copy runs under [github.com/aasyanov/urx/panix]: a panicking function
// becomes a [*panix.PanicError] instead of crashing the process and is treated
// as an ordinary copy failure.
//
// # Dependencies
//
// hedgex depends only on the Go standard library and the urx panix package.
package hedgex

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/panix"
)

// opExecute labels panics recovered while running a hedged function and is the
// default operation name when [WithOp] is not set.
const opExecute = "hedgex.Execute"

// Hedger runs functions with hedging (speculative execution) to cut tail
// latency. Create one with [New], run work with the package-level [Execute] or
// [ExecuteMulti], and inspect counters with [Hedger.Stats].
//
// A Hedger holds only immutable configuration plus lock-free atomic counters,
// so it is safe for concurrent use from any number of goroutines and may be
// shared across the lifetime of a service.
type Hedger struct {
	cfg config

	calls    atomic.Int64
	wins     atomic.Int64
	hedges   atomic.Int64
	failures atomic.Int64
}

// New creates a [Hedger] with the given options applied on top of the package
// defaults ([DefaultMaxParallel] copies, [DefaultDelay] stagger, [DefaultMaxDelay]
// window). Invalid options are clamped, so New never returns an unusable hedger:
// a non-positive parallelism floors to a single copy (no hedging) and a MaxDelay
// below the per-copy delay is raised to it.
func New(opts ...Option) *Hedger {
	return &Hedger{cfg: newConfig(opts)}
}

// MaxParallel returns the configured maximum number of concurrent copies.
func (h *Hedger) MaxParallel() int { return h.cfg.maxParallel }

// Delay returns the configured stagger between successive copies.
func (h *Hedger) Delay() time.Duration { return h.cfg.delay }

// MaxDelay returns the configured cap on the total stagger window.
func (h *Hedger) MaxDelay() time.Duration { return h.cfg.maxDelay }

// Execute runs fn with hedging: the original copy starts immediately and, if it
// has not returned a success within [WithDelay], a second copy is launched, and
// so on up to [WithMaxParallel]. The first copy to succeed wins and its value is
// returned; all other in-flight copies are then cancelled. Because Go methods
// cannot be generic, Execute is a package-level function taking the [Hedger] as
// its first argument.
//
// Execute returns [ErrNilFunc] if fn is nil, [ErrCancelled] if ctx is cancelled
// before any copy succeeds, and [ErrAllFailed] (wrapping the first failure) if
// every copy fails. Each copy runs under [panix.Safe], so a panic surfaces as a
// [*panix.PanicError] handled like any other copy failure.
//
// The callback receives the call ctx and a [HedgeController] exposing the copy's
// attempt number and a [HedgeController.Cancel] method to withdraw from the race.
func Execute[T any](h *Hedger, ctx context.Context, fn HedgeFunc[T]) (T, error) {
	var zero T
	if fn == nil {
		h.calls.Add(1)
		h.failures.Add(1)
		return zero, ErrNilFunc
	}
	// Fast path: with a single copy there is nothing to race, so skip the
	// fan-out slice entirely and run inline (see runSync).
	if h.cfg.maxParallel == 1 {
		h.calls.Add(1)
		if err := ctx.Err(); err != nil {
			h.failures.Add(1)
			return zero, errCancelled(err)
		}
		return runSync(h, ctx, fn)
	}
	fns := make([]HedgeFunc[T], h.cfg.maxParallel)
	for i := range fns {
		fns[i] = fn
	}
	return ExecuteMulti(h, ctx, fns)
}

// ExecuteMulti runs each function in fns as a distinct hedge backend, launched
// with the same staggered schedule as [Execute]. The first copy to succeed wins.
// len(fns) is capped at [WithMaxParallel]; a nil entry within the cap is skipped
// (its slot never launches). fns is not retained after ExecuteMulti returns.
//
// ExecuteMulti returns [ErrNilFunc] if fns is empty or every in-cap entry is nil,
// [ErrCancelled] if ctx is cancelled before any copy succeeds, and [ErrAllFailed]
// (wrapping the first failure) if every launched copy fails. Use it to hedge
// across heterogeneous backends (primary vs. replica vs. cache) rather than the
// same function repeated by [Execute].
func ExecuteMulti[T any](h *Hedger, ctx context.Context, fns []HedgeFunc[T]) (T, error) {
	var zero T
	h.calls.Add(1)

	if len(fns) > h.cfg.maxParallel {
		fns = fns[:h.cfg.maxParallel]
	}
	if !anyNonNil(fns) {
		h.failures.Add(1)
		return zero, ErrNilFunc
	}
	if err := ctx.Err(); err != nil {
		h.failures.Add(1)
		return zero, errCancelled(err)
	}

	backends := launchableCount(fns)
	if fn, single := lone(fns); single {
		return runSync(h, ctx, fn)
	}
	return dispatch(h, ctx, fns, backends)
}

// lone returns the sole launchable (non-nil) entry of fns and true when exactly
// one exists. The single-backend case has nothing to race and takes the
// synchronous fast path in [runSync].
func lone[T any](fns []HedgeFunc[T]) (HedgeFunc[T], bool) {
	var found HedgeFunc[T]
	count := 0
	for _, fn := range fns {
		if fn != nil {
			found = fn
			count++
			if count > 1 {
				return nil, false
			}
		}
	}
	return found, count == 1
}

// runSync executes a single backend inline, skipping the goroutine, channel,
// timer, and cancel context that the hedged path requires. With one backend
// there is nothing to race, so the synchronous path is both faster and cheaper
// while preserving panic safety and the controller contract. The copy is the
// original request: attempt 1 of 1 backend.
//
// runSync is a package-level generic function because Go methods cannot carry
// their own type parameters.
func runSync[T any](h *Hedger, ctx context.Context, fn HedgeFunc[T]) (T, error) {
	var zero T
	hc := &execution{attempt: 1, backends: 1, start: time.Now()}
	val, err := panix.Safe(h.cfg.opOrDefault(), func() (T, error) {
		return fn(ctx, hc)
	})
	if err != nil {
		h.failures.Add(1)
		return zero, errAllFailed(err)
	}
	if hc.isWithdrawn() {
		h.failures.Add(1)
		return zero, errAllFailed(context.Canceled)
	}
	h.wins.Add(1)
	return val, nil
}

// dispatch runs the staggered hedge loop: it launches the first copy, then
// launches each subsequent copy when its scheduled delay elapses unless a
// winner has already arrived, and returns the first success. Withdrawn copies
// (see [HedgeController.Cancel]) are reaped but never counted as winner or
// failure. The shared cancel() tears down every outstanding copy on return.
//
// dispatch is a package-level generic function because Go methods cannot carry
// their own type parameters.
func dispatch[T any](h *Hedger, ctx context.Context, fns []HedgeFunc[T], backends int) (T, error) {
	var zero T

	hedgeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultCh := make(chan result[T], backends)
	start := time.Now()
	delays := h.delays(backends)

	// next is the slice index of the next copy to launch; attempt is the
	// 1-based launch ordinal among non-nil entries; pending counts launched
	// copies whose result has not yet been reaped.
	var attempt int
	next, attempt, _ := launchNext(h, hedgeCtx, fns, 0, attempt, backends, start, resultCh)
	pending := 1

	var timer *time.Timer
	var timerCh <-chan time.Time
	if d, ok := delayFor(delays, attempt); ok {
		timer = time.NewTimer(d)
		timerCh = timer.C
		defer timer.Stop()
	}

	var firstErr error
	for {
		select {
		case <-ctx.Done():
			h.failures.Add(1)
			return zero, errCancelled(ctx.Err())

		case res := <-resultCh:
			if val, won := consumeResult(h, res, &pending, &firstErr, cancel); won {
				return val, nil
			}
			if pending > 0 {
				continue
			}
			if err := relaunchAfterFailure(h, hedgeCtx, fns, &next, &attempt, backends, start, resultCh, &pending, &firstErr, timer, &timerCh, delays); err != nil {
				h.failures.Add(1)
				return zero, err
			}
			continue

		case <-timerCh:
			var launched bool
			next, attempt, launched = launchNext(h, hedgeCtx, fns, next, attempt, backends, start, resultCh)
			if launched {
				pending++
			}
			armTimer(timer, &timerCh, delays, attempt, start)
		}
	}
}

// consumeResult applies one copy outcome. It returns won=true when the copy
// succeeded and the dispatch loop should return its value.
func consumeResult[T any](h *Hedger, res result[T], pending *int, firstErr *error, cancel context.CancelFunc) (T, bool) {
	var zero T
	*pending--
	if res.withdrawn {
		return zero, false
	}
	if res.err == nil {
		cancel()
		h.wins.Add(1)
		return res.value, true
	}
	if *firstErr == nil {
		*firstErr = res.err
	}
	return zero, false
}

// relaunchAfterFailure launches the next copy immediately when no copy remains
// in flight. Returns nil when a new copy started, or an error when every
// launchable copy finished without a win.
func relaunchAfterFailure[T any](
	h *Hedger,
	ctx context.Context,
	fns []HedgeFunc[T],
	next, attempt *int,
	backends int,
	start time.Time,
	resultCh chan<- result[T],
	pending *int,
	firstErr *error,
	timer *time.Timer,
	timerCh *<-chan time.Time,
	delays []time.Duration,
) error {
	if *next >= len(fns) {
		return errAllFailed(orWithdrawn(*firstErr))
	}
	var launched bool
	*next, *attempt, launched = launchNext(h, ctx, fns, *next, *attempt, backends, start, resultCh)
	if !launched {
		return errAllFailed(orWithdrawn(*firstErr))
	}
	*pending++
	armTimer(timer, timerCh, delays, *attempt, start)
	return nil
}

// armTimer rearms or disables the stagger timer for the next scheduled copy.
func armTimer(timer *time.Timer, timerCh *<-chan time.Time, delays []time.Duration, launchedAttempt int, start time.Time) {
	if d, ok := delayFor(delays, launchedAttempt); ok {
		resetTimer(timer, d, start)
		*timerCh = timer.C
		return
	}
	*timerCh = nil
}

// launchNext starts the next launchable copy at or after slice index from. It
// returns the index immediately after the one launched (or len(fns) when only
// nil entries remained), the updated 1-based launch ordinal, and whether a copy
// was actually launched. It fires the WithOnHedge hook and increments the hedge
// counter for copies beyond the original.
func launchNext[T any](h *Hedger, ctx context.Context, fns []HedgeFunc[T], from, attempt, backends int, start time.Time, ch chan<- result[T]) (next int, attemptOut int, launched bool) {
	for i := from; i < len(fns); i++ {
		if fns[i] == nil {
			continue
		}
		attempt++
		if attempt > 1 {
			h.hedges.Add(1)
			h.fireOnHedge(attempt)
		}
		hc := &execution{attempt: attempt, backends: backends, start: start}
		go run(h, ctx, fns[i], hc, ch)
		return i + 1, attempt, true
	}
	return len(fns), attempt, false
}

// fireOnHedge invokes the configured launch hook asynchronously under panic
// recovery so a slow or panicking hook never blocks or crashes the dispatch
// loop.
func (h *Hedger) fireOnHedge(attempt int) {
	if h.cfg.onHedge == nil {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		h.cfg.onHedge(attempt)
	}()
}

// run executes one copy under panic recovery and reports its outcome on ch.
// A copy that withdrew via [HedgeController.Cancel] is flagged so the dispatch
// loop ignores its result. If the hedge context is already cancelled (a sibling
// won) the buffered send still succeeds; the select guards only against the
// channel being abandoned, which never happens while dispatch is looping.
func run[T any](h *Hedger, ctx context.Context, fn HedgeFunc[T], hc *execution, ch chan<- result[T]) {
	val, err := panix.Safe(h.cfg.opOrDefault(), func() (T, error) {
		return fn(ctx, hc)
	})
	select {
	case ch <- result[T]{value: val, err: err, withdrawn: hc.isWithdrawn()}:
	case <-ctx.Done():
	}
}

// Stats holds a point-in-time snapshot of hedger counters.
type Stats struct {
	// Calls is the total number of [Execute]/[ExecuteMulti] invocations.
	Calls int64 `json:"calls"`
	// Wins is the number of calls that returned a successful result.
	Wins int64 `json:"wins"`
	// Hedges is the number of speculative copies launched beyond the original.
	Hedges int64 `json:"hedges"`
	// Failures is the number of calls that returned an error (all copies
	// failed, no function, or context cancellation).
	Failures int64 `json:"failures"`
}

// Stats returns a snapshot of hedger statistics. It is safe to call concurrently
// with [Execute]; the counters are read independently and may reflect a call in
// progress.
func (h *Hedger) Stats() Stats {
	return Stats{
		Calls:    h.calls.Load(),
		Wins:     h.wins.Load(),
		Hedges:   h.hedges.Load(),
		Failures: h.failures.Load(),
	}
}

// ResetStats zeroes all cumulative counters. It does not affect any in-flight
// call.
func (h *Hedger) ResetStats() {
	h.calls.Store(0)
	h.wins.Store(0)
	h.hedges.Store(0)
	h.failures.Store(0)
}

// launchableCount returns the number of non-nil entries in fns.
func launchableCount[T any](fns []HedgeFunc[T]) int {
	n := 0
	for _, fn := range fns {
		if fn != nil {
			n++
		}
	}
	return n
}

// anyNonNil reports whether fns has at least one launchable entry.
func anyNonNil[T any](fns []HedgeFunc[T]) bool {
	for _, fn := range fns {
		if fn != nil {
			return true
		}
	}
	return false
}

// orWithdrawn supplies a fallback cause when every copy withdrew and left no
// real error to wrap.
func orWithdrawn(firstErr error) error {
	if firstErr != nil {
		return firstErr
	}
	return context.Canceled
}

// delayFor returns the launch delay for the copy after launchedAttempt (the
// 1-based ordinal of the most recently launched copy), or false when no further
// copy is scheduled. delays[i] holds the launch time of copy i+2, so after copy
// k launches the next schedule entry is delays[k-1].
func delayFor(delays []time.Duration, launchedAttempt int) (time.Duration, bool) {
	idx := launchedAttempt - 1
	if idx < 0 || idx >= len(delays) {
		return 0, false
	}
	return delays[idx], true
}

// resetTimer rearms timer to fire when the absolute schedule point next (measured
// from start) is reached, clamping to zero if that point has already passed.
func resetTimer(timer *time.Timer, next time.Duration, start time.Time) {
	d := next - time.Since(start)
	if d < 0 {
		d = 0
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

// delays returns the cumulative launch times (relative to start) for copies
// 2..count. delays[i] is when copy i+2 should launch. The first delay window
// grows linearly by the base delay until it would exceed maxDelay; from that
// point on copies are spread thinly (spreadDelay apart) so a large MaxParallel
// does not collapse into a synchronized burst at the cap.
func (h *Hedger) delays(count int) []time.Duration {
	if count <= 1 {
		return nil
	}
	ds := make([]time.Duration, count-1)

	capIdx := -1
	for i := range ds {
		if h.cfg.delay*time.Duration(i+1) >= h.cfg.maxDelay {
			capIdx = i
			break
		}
	}

	if capIdx == -1 {
		for i := range ds {
			ds[i] = h.cfg.delay * time.Duration(i+1)
		}
		return ds
	}

	for i := 0; i < capIdx; i++ {
		ds[i] = h.cfg.delay * time.Duration(i+1)
	}
	spread := h.cfg.delay / spreadDivisor
	if spread < minSpread {
		spread = minSpread
	}
	for i := capIdx; i < len(ds); i++ {
		ds[i] = h.cfg.maxDelay + time.Duration(i-capIdx)*spread
	}
	return ds
}
