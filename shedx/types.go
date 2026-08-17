package shedx

import "context"

// Priority defines request importance. Higher values are shed last;
// [PriorityCritical] requests are never shed.
type Priority uint8

const (
	// PriorityLow is for background work: pre-fetches, analytics, batch jobs.
	// It is shed first when the system is mildly overloaded.
	PriorityLow Priority = iota
	// PriorityNormal is for regular user requests. It is shed after
	// [PriorityLow] but before [PriorityHigh].
	PriorityNormal
	// PriorityHigh is for important operations such as paid-tier traffic or
	// writes. It is shed only under severe overload.
	PriorityHigh
	// PriorityCritical is for health checks, authentication, and control-plane
	// traffic. It is never shed, regardless of load.
	PriorityCritical
)

// String labels for [Priority] values.
const (
	labelLow      = "low"
	labelNormal   = "normal"
	labelHigh     = "high"
	labelCritical = "critical"
	labelUnknown  = "unknown"
)

// String returns a human-readable label for the priority.
func (p Priority) String() string {
	switch p {
	case PriorityLow:
		return labelLow
	case PriorityNormal:
		return labelNormal
	case PriorityHigh:
		return labelHigh
	case PriorityCritical:
		return labelCritical
	default:
		return labelUnknown
	}
}

// ShedController exposes the admission context to the [Execute] and [TryExecute]
// callback and lets it report graceful degradation. The implementation is private; callers
// interact only through this interface. A ShedController is bound to a single
// [Execute] or [TryExecute] call and must not be retained after the callback returns.
//
// Load shedding is decided at admission: by the time the callback runs the
// request is already admitted. The controller therefore exposes the load
// snapshot so the callback can choose a degraded path (for example, serve a
// cached response instead of a fresh query) and call [ShedController.Shed] to
// record that it did so.
type ShedController interface {
	// Priority returns the priority the request was admitted with.
	Priority() Priority

	// Load returns the load fraction (inflight/capacity) captured at admission
	// time, in [0, 1+]. The denominator is the configured capacity, so values
	// above 1 are possible for [PriorityCritical] requests admitted past
	// capacity.
	Load() float64

	// InFlight returns the number of in-flight operations captured at admission
	// time, excluding this one.
	InFlight() int64

	// Capacity returns the configured maximum number of in-flight operations.
	Capacity() int

	// Shed records that the callback served a degraded response because of
	// load. It does not abort execution — the callback still returns normally.
	// Use it to count graceful degradations in [Stats]. Safe to call multiple
	// times; only the first call is counted. Shed does not release the in-flight
	// slot; use [ShedController.SkipSlot] for an early release.
	Shed()

	// SkipSlot releases the in-flight slot immediately so a cheap or cached
	// path does not occupy capacity for the rest of the callback. Idempotent
	// with the deferred release after the callback returns. It does not record
	// degradation — use [ShedController.Shed] for that.
	SkipSlot()
}

// execution is the private implementation of [ShedController]. It is created
// once per [Execute] or [TryExecute] call and accessed only from the callback goroutine, so it
// needs no synchronization.
type execution struct {
	shedder  *Shedder
	priority Priority
	load     float64
	inFlight int64
	capacity int
	degraded bool
	released bool
}

// Priority implements [ShedController].
func (e *execution) Priority() Priority { return e.priority }

// Load implements [ShedController].
func (e *execution) Load() float64 { return e.load }

// InFlight implements [ShedController].
func (e *execution) InFlight() int64 { return e.inFlight }

// Capacity implements [ShedController].
func (e *execution) Capacity() int { return e.capacity }

// Shed implements [ShedController].
func (e *execution) Shed() { e.degraded = true }

// SkipSlot implements [ShedController].
func (e *execution) SkipSlot() {
	if e.released {
		return
	}
	e.released = true
	e.shedder.inflight.Add(-1)
}

// compile-time assertion that execution satisfies the public interface.
var _ ShedController = (*execution)(nil)

// ShedFunc is the unit of work run by [Execute] and [TryExecute]. It receives the call context
// and a [ShedController], and runs under panic recovery: a panicking function
// becomes a [*panix.PanicError].
type ShedFunc[T any] func(ctx context.Context, sc ShedController) (T, error)
