package hedgex

import (
	"context"
	"sync/atomic"
	"time"
)

// HedgeController exposes per-copy execution context to the hedged function
// and lets a copy remove itself from the race. The implementation is private;
// callers interact only through this interface. A HedgeController is bound to a
// single hedge copy within one [Execute] call and must not be retained after
// the function returns.
//
// Hedging launches the same logical request as several copies with staggered
// delays and keeps the first success. The controller therefore tells a copy
// which attempt it is (so it can adapt — read from a replica, skip writes) and
// lets it bow out via [HedgeController.Cancel] when it knows it cannot win
// (for example, the chosen backend is unreachable), freeing its slot without
// failing the whole call.
type HedgeController interface {
	// Attempt returns the 1-based launch ordinal of this copy among non-nil
	// backends: 1 is the original request, 2 the first hedge, and so on. Nil
	// slots in an [ExecuteMulti] slice are not counted.
	Attempt() int

	// IsHedge reports whether this copy is a speculative hedge (Attempt > 1)
	// rather than the original request.
	IsHedge() bool

	// Backends returns the number of launchable copies scheduled for this call:
	// non-nil entries after capping at [WithMaxParallel], excluding skipped nil
	// slots. Use it to size per-backend selection (replica index = Attempt-1).
	Backends() int

	// Elapsed returns the wall-clock time since the first copy was launched,
	// measured from the start of [Execute]. It lets a late hedge gauge how far
	// behind it started.
	Elapsed() time.Duration

	// Cancel withdraws this copy from the race and cancels this copy's
	// context so a well-behaved function can observe ctx.Done() and return.
	// The copy's eventual result is still reaped (it is neither winner nor
	// failure). Sibling copies keep a live context. On the synchronous
	// MaxParallel==1 path there is no per-copy context, so Cancel only sets
	// the withdrawn flag. Safe to call multiple times; only the first call
	// has an effect.
	Cancel()
}

// execution is the private implementation of [HedgeController]. One instance is
// created per copy. The attempt index, backend count, and start time are
// immutable for the copy's lifetime and read without synchronization; the
// withdrawn flag is an atomic because Cancel may be observed from the dispatch
// goroutine that reaps results.
type execution struct {
	attempt    int
	backends   int
	start      time.Time
	copyCancel context.CancelFunc
	withdrawn  atomic.Bool
}

// Attempt implements [HedgeController].
func (e *execution) Attempt() int { return e.attempt }

// IsHedge implements [HedgeController].
func (e *execution) IsHedge() bool { return e.attempt > 1 }

// Backends implements [HedgeController].
func (e *execution) Backends() int { return e.backends }

// Elapsed implements [HedgeController].
func (e *execution) Elapsed() time.Duration { return time.Since(e.start) }

// Cancel implements [HedgeController].
func (e *execution) Cancel() {
	if e.withdrawn.CompareAndSwap(false, true) && e.copyCancel != nil {
		e.copyCancel()
	}
}

// isWithdrawn reports whether the copy removed itself from the race.
func (e *execution) isWithdrawn() bool { return e.withdrawn.Load() }

// compile-time assertion that execution satisfies the public interface.
var _ HedgeController = (*execution)(nil)

// HedgeFunc is the unit of work hedged by [Execute] and [ExecuteMulti]. It runs
// under panic recovery and receives the call context and a [HedgeController].
type HedgeFunc[T any] func(ctx context.Context, hc HedgeController) (T, error)

// result carries the outcome of one copy back to the dispatch loop. withdrawn
// flags a copy that removed itself via [HedgeController.Cancel] so it is ignored
// as neither winner nor failure.
type result[T any] struct {
	value     T
	err       error
	withdrawn bool
}
