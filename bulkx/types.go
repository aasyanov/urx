package bulkx

import "context"

// BulkController exposes the admission context to the [Execute] callback. The
// implementation is private; callers interact only through this interface. A
// BulkController is bound to a single [Execute] call and must not be retained
// after the callback returns.
//
// Admission is decided before the callback runs: by the time the callback
// executes the slot is already held. The controller therefore exposes the
// occupancy snapshot taken at admission so the callback can adapt its behavior
// under pressure (for example, return a lighter response when the bulkhead is
// nearly full).
type BulkController interface {
	// Active returns the number of in-flight operations captured at admission
	// time, including this one. It is in [1, MaxConcurrent].
	Active() int

	// MaxConcurrent returns the configured slot count.
	MaxConcurrent() int

	// Load returns the occupancy fraction (active/maxConcurrent) captured at
	// admission time, in (0, 1].
	Load() float64

	// WaitedSlot reports whether this call went through the slow (timer) path,
	// meaning all slots were busy when the call arrived and it had to wait for
	// one to free up.
	WaitedSlot() bool
}

// execution is the private implementation of [BulkController]. It is created
// once per [Execute] call and accessed only from the callback goroutine, so it
// needs no synchronization.
type execution struct {
	active        int
	maxConcurrent int
	waitedSlot    bool
}

// Active implements [BulkController].
func (e *execution) Active() int { return e.active }

// MaxConcurrent implements [BulkController].
func (e *execution) MaxConcurrent() int { return e.maxConcurrent }

// Load implements [BulkController].
func (e *execution) Load() float64 { return float64(e.active) / float64(e.maxConcurrent) }

// WaitedSlot implements [BulkController].
func (e *execution) WaitedSlot() bool { return e.waitedSlot }

// compile-time assertion that execution satisfies the public interface.
var _ BulkController = (*execution)(nil)

// BulkFunc is the unit of work run by [Execute] and [TryExecute]. It receives
// the call context and a [BulkController], and runs under panic recovery: a
// panicking function becomes a [*panix.PanicError].
type BulkFunc[T any] func(ctx context.Context, bc BulkController) (T, error)
