package circuitx

import "context"

// State is the operating mode of a [Breaker]. A breaker moves between three
// states: [Closed] (healthy), [Open] (tripped), and [HalfOpen] (probing).
type State uint8

const (
	// Closed is the healthy state: calls pass through and failures are counted.
	// When consecutive failures reach the configured threshold the breaker
	// trips to [Open].
	Closed State = iota
	// Open is the tripped state: calls are rejected immediately — [Execute]
	// returns [ErrOpen], [TryExecute] returns (false, zero, nil) — without
	// invoking the function. After the reset timeout elapses the breaker moves
	// to [HalfOpen] and admits a bounded number of probes.
	Open
	// HalfOpen is the probing state: a limited number of probe calls are
	// admitted to test whether the downstream has recovered. A probe success
	// closes the breaker; a probe failure re-opens it.
	HalfOpen
)

// String label strings, kept out of [State.String] so there are no string
// literals in logic.
const (
	labelClosed   = "closed"
	labelOpen     = "open"
	labelHalfOpen = "half_open"
	labelUnknown  = "unknown"
)

// String returns the lowercase state name ("closed", "open", "half_open").
func (s State) String() string {
	switch s {
	case Closed:
		return labelClosed
	case Open:
		return labelOpen
	case HalfOpen:
		return labelHalfOpen
	default:
		return labelUnknown
	}
}

// CircuitController exposes the admission context to the [Execute] and
// [TryExecute] callbacks and lets it influence how the breaker treats the call's
// outcome. The implementation is private; callers interact only through this
// interface. A CircuitController is bound to a single [Execute] or [TryExecute]
// call and must not be retained after the callback returns.
//
// The breaker decides admission before the callback runs: by the time the
// callback executes the call has already been let through in either [Closed] or
// [HalfOpen] state. The controller therefore lets the callback adapt — run a
// cheap health probe while [State] reports [HalfOpen], exclude a business error
// from the failure count with [CircuitController.SkipFailure], or force the
// breaker open early with [CircuitController.Trip] when it detects an
// unrecoverable downstream condition.
type CircuitController interface {
	// State returns the circuit state at the moment the call was admitted,
	// either [Closed] or [HalfOpen]. A call is never admitted in [Open] state.
	State() State

	// Failures returns the consecutive failure count at the moment the call was
	// admitted, before this call's own outcome is recorded.
	Failures() int

	// MaxFailures returns the configured consecutive-failure threshold at which
	// the breaker trips from [Closed] to [Open].
	MaxFailures() int

	// SkipFailure tells the breaker not to count the returned error as a circuit
	// failure. Use it for business-logic errors (a "not found", a validation
	// rejection) that signal a healthy downstream and must not trip the breaker.
	// Safe to call multiple times; only the first call has an effect.
	SkipFailure()

	// Trip forces the breaker to [Open] regardless of the returned error or the
	// current failure count. Use it when the callback detects a condition that
	// makes further calls pointless (an authentication revocation, a hard
	// downstream shutdown). It takes effect after the callback returns; the
	// callback should still return promptly. [SkipFailure] does not suppress a
	// Trip. Safe to call multiple times; only the first call has an effect.
	Trip()
}

// CircuitFunc is the unit of work run by [Execute] and [TryExecute]. It receives the call context
// and a [CircuitController], and runs under panic recovery: a panicking function
// becomes a [*panix.PanicError] and is treated as a failure (subject to
// [CircuitController.SkipFailure]).
type CircuitFunc[T any] func(ctx context.Context, cc CircuitController) (T, error)

// execution is the private implementation of [CircuitController]. One instance
// is created per [Execute] or [TryExecute] call and accessed only from the
// goroutine running the callback, so it needs no synchronization. The
// skipFailure and tripped flags are read by the dispatch path after the
// callback returns.
type execution struct {
	state       State
	failures    int
	maxFailures int
	skipFailure bool
	tripped     bool
}

// State implements [CircuitController].
func (e *execution) State() State { return e.state }

// Failures implements [CircuitController].
func (e *execution) Failures() int { return e.failures }

// MaxFailures implements [CircuitController].
func (e *execution) MaxFailures() int { return e.maxFailures }

// SkipFailure implements [CircuitController].
func (e *execution) SkipFailure() { e.skipFailure = true }

// Trip implements [CircuitController].
func (e *execution) Trip() { e.tripped = true }

// compile-time assertion that execution satisfies the public interface.
var _ CircuitController = (*execution)(nil)

// Stats holds a point-in-time snapshot of breaker counters. All cumulative
// fields are reset by [Breaker.ResetStats]; State, Failures, and MaxFailures
// reflect the live circuit at snapshot time.
type Stats struct {
	// State is the circuit state at snapshot time.
	State State `json:"state"`
	// Failures is the current consecutive failure count.
	Failures int `json:"failures"`
	// MaxFailures is the configured failure threshold.
	MaxFailures int `json:"max_failures"`
	// Successes is the cumulative number of calls that succeeded.
	Successes uint64 `json:"successes"`
	// TotalFail is the cumulative number of calls counted as failures
	// (excluding those suppressed by [CircuitController.SkipFailure]).
	TotalFail uint64 `json:"total_failures"`
	// Rejected is the cumulative number of calls rejected by [Execute] with
	// [ErrOpen] or by [TryExecute] with (false, zero, nil).
	Rejected uint64 `json:"rejected"`
	// Trips is the cumulative number of transitions into [Open].
	Trips uint64 `json:"trips"`
}
