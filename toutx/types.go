package toutx

import (
	"context"
	"time"
)

// TimeoutController exposes per-call deadline context to the [Execute]
// callback. The implementation is private; callers interact only through this
// interface. A TimeoutController is bound to a single [Execute] call, must not
// be retained after the callback returns, and is accessed only from that call's
// callback goroutine — it is not safe for concurrent use across goroutines.
type TimeoutController interface {
	// Op returns the logical operation name for this call.
	Op() string

	// Timeout returns the total time budget configured for this call.
	Timeout() time.Duration

	// Deadline returns the absolute instant at which the call's context is
	// cancelled. It equals the value reported by the callback context's
	// [context.Context.Deadline].
	Deadline() time.Time

	// Elapsed returns the time that has passed since the call started.
	Elapsed() time.Duration

	// Remaining returns the time left before the deadline fires. It is never
	// negative: once the deadline has passed it returns zero. Use it to skip
	// sub-steps that cannot finish in the remaining budget.
	Remaining() time.Duration
}

// execution is the private implementation of [TimeoutController]. It is created
// once per [Execute] call and accessed only from the callback goroutine, so it
// needs no synchronization. Instants are stored as Unix nanoseconds so the
// struct stays within the package footprint budget.
type execution struct {
	op           string
	timeout      time.Duration
	startUnix    int64
	deadlineUnix int64
}

// Op implements [TimeoutController].
func (e *execution) Op() string { return e.op }

// Timeout implements [TimeoutController].
func (e *execution) Timeout() time.Duration { return e.timeout }

// Deadline implements [TimeoutController].
func (e *execution) Deadline() time.Time { return time.Unix(0, e.deadlineUnix) }

// Elapsed implements [TimeoutController].
func (e *execution) Elapsed() time.Duration {
	return time.Since(time.Unix(0, e.startUnix))
}

// Remaining implements [TimeoutController].
func (e *execution) Remaining() time.Duration {
	if d := time.Until(e.Deadline()); d > 0 {
		return d
	}
	return 0
}

// compile-time assertion that execution satisfies the public interface.
var _ TimeoutController = (*execution)(nil)

// TimeoutFunc is the unit of work run by [Execute]. It receives a
// deadline-scoped context and a [TimeoutController], and runs under panic
// recovery: a panicking function becomes a [*panix.PanicError].
type TimeoutFunc[T any] func(ctx context.Context, tc TimeoutController) (T, error)
