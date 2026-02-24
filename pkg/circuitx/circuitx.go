// Package circuitx provides a thread-safe circuit breaker for industrial Go services.
//
// The circuit breaker monitors failures and transitions between Closed, Open,
// and HalfOpen states. When the failure threshold is reached the circuit opens,
// rejecting calls immediately with a structured [errx.Error]. After a cooldown
// period the circuit moves to HalfOpen, allowing a single probe call to
// determine whether the downstream has recovered.
//
//	cb := circuitx.New(
//	    circuitx.WithMaxFailures(5),
//	    circuitx.WithResetTimeout(10*time.Second),
//	)
//
//	resp, err := circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (*Response, error) {
//	    if cc.State() == circuitx.HalfOpen {
//	        return nil, client.HealthCheck(ctx)
//	    }
//	    return client.Call(ctx, req)
//	})
//
// The callback receives a [CircuitController] that exposes the circuit state
// and failure count at the moment the call was admitted, and a
// [CircuitController.SkipFailure] method to prevent business errors from
// tripping the breaker.
//
// Each handler is wrapped with [panix.Safe] for panic recovery; panicked
// handlers produce structured [errx.Error] values.
//
package circuitx

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/pkg/panix"
)

// --- State ---

// State represents the circuit breaker state.
type State uint8

const (
	// Closed means the circuit is healthy; calls pass through.
	Closed State = iota
	// Open means the circuit has tripped; calls are rejected immediately.
	Open
	// HalfOpen means the circuit is probing; one call is allowed through.
	HalfOpen
)

// String labels for [State] values.
const (
	labelClosed   = "closed"
	labelOpen     = "open"
	labelHalfOpen = "half_open"
	labelUnknown  = "unknown"
)

// String returns a human-readable label.
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

// --- Configuration ---

// config holds circuit breaker parameters.
type config struct {
	maxFailures  int
	resetTimeout time.Duration
}

// defaultConfig returns sensible breaker defaults (5 failures, 10 s reset).
func defaultConfig() config {
	return config{
		maxFailures:  5,
		resetTimeout: 10 * time.Second,
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option func(*config)

// WithMaxFailures sets the number of consecutive failures before the circuit
// opens. Values <= 0 are treated as 1.
func WithMaxFailures(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxFailures = n
		}
	}
}

// WithResetTimeout sets the duration the circuit stays open before
// transitioning to HalfOpen. Values <= 0 are ignored.
func WithResetTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.resetTimeout = d
		}
	}
}

// --- Breaker ---

// Breaker is a thread-safe circuit breaker. Create one with [New], execute
// operations with [Breaker.Execute], and inspect state with [Breaker.State],
// [Breaker.Failures], or [Breaker.Reset].
type Breaker struct {
	cfg       config
	state     atomic.Uint32
	failures  atomic.Int32
	lastOpen  atomic.Int64
	probing   atomic.Bool // true while a single HalfOpen probe is in flight
	successes atomic.Uint64
	totalFail atomic.Uint64
	rejected  atomic.Uint64
	trips     atomic.Uint64 // Closed/HalfOpen -> Open transition count
	mu        sync.Mutex
}

// New creates a [Breaker] with the given options applied on top of
// sensible defaults (5 failures, 10 s reset timeout).
func New(opts ...Option) *Breaker {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxFailures <= 0 {
		cfg.maxFailures = 1
	}
	return &Breaker{cfg: cfg}
}

// --- State & accessors ---

// State returns the current circuit state. If the circuit is Open and the
// reset timeout has elapsed, it transitions to HalfOpen via [atomic.Uint32.CompareAndSwap],
// ensuring only one caller triggers the transition.
func (b *Breaker) State() State {
	s := State(b.state.Load())
	if s == Open {
		last := time.Unix(0, b.lastOpen.Load())
		if time.Since(last) >= b.cfg.resetTimeout {
			b.state.CompareAndSwap(uint32(Open), uint32(HalfOpen))
			return HalfOpen
		}
	}
	return s
}

// Failures returns the current consecutive failure count.
func (b *Breaker) Failures() int {
	return int(b.failures.Load())
}

// Reset forces the circuit back to Closed and clears the failure counter.
func (b *Breaker) Reset() {
	b.failures.Store(0)
	b.state.Store(uint32(Closed))
}

// --- Statistics ---

// Stats holds a point-in-time snapshot of circuit breaker counters.
type Stats struct {
	State       State  `json:"state"`
	Failures    int    `json:"failures"`
	MaxFailures int    `json:"max_failures"`
	Successes   uint64 `json:"successes"`
	TotalFail   uint64 `json:"total_failures"`
	Rejected    uint64 `json:"rejected"`
	Trips       uint64 `json:"trips"`
}

// Stats returns a snapshot of circuit breaker statistics.
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

// ResetStats zeroes all counters (does not affect circuit state).
func (b *Breaker) ResetStats() {
	b.successes.Store(0)
	b.totalFail.Store(0)
	b.rejected.Store(0)
	b.trips.Store(0)
}

// --- CircuitController ---

// CircuitController provides execution context and control to the circuit
// breaker callback. The implementation is private; callers interact only
// through this interface.
type CircuitController interface {
	// State returns the circuit state at the moment the call was admitted
	// (either [Closed] or [HalfOpen]).
	State() State
	// Failures returns the consecutive failure count at the moment the call
	// was admitted.
	Failures() int
	// SkipFailure tells the breaker not to count the returned error as a
	// circuit failure. Use this for business-logic errors (e.g. "not found")
	// that should not trip the breaker. Safe to call multiple times.
	SkipFailure()
}

// execution is the private implementation of [CircuitController].
type execution struct {
	state       State
	failures    int
	skipFailure bool
}

func (e *execution) State() State  { return e.state }
func (e *execution) Failures() int { return e.failures }
func (e *execution) SkipFailure()  { e.skipFailure = true }

// --- Core execution ---

// Execute runs fn within the circuit breaker. Because Go methods cannot have
// type parameters, Execute is a package-level generic function that takes the
// [Breaker] as its first argument.
//
// If the circuit is Open the call is rejected immediately with [CodeOpen].
// In HalfOpen state only a single probe call is admitted; concurrent callers
// receive [CodeOpen] until the probe completes. Each call is wrapped with
// [panix.Safe] for panic recovery. On success in HalfOpen state the circuit
// resets to Closed.
//
// The callback receives a [CircuitController] that exposes the circuit state
// and failure count at admission time, and a [CircuitController.SkipFailure]
// method to prevent business errors from tripping the breaker.
func Execute[T any](b *Breaker, ctx context.Context, fn func(ctx context.Context, cc CircuitController) (T, error)) (T, error) {
	var zero T
	state := b.State()
	if state == Open {
		b.rejected.Add(1)
		return zero, errOpen()
	}

	if state == HalfOpen {
		if !b.probing.CompareAndSwap(false, true) {
			b.rejected.Add(1)
			return zero, errOpen()
		}
		defer b.probing.Store(false)
	}

	cc := &execution{
		state:    state,
		failures: int(b.failures.Load()),
	}

	val, err := panix.Safe(ctx, "circuitx.Execute", func(ctx context.Context) (T, error) {
		return fn(ctx, cc)
	})

	if err != nil {
		if !cc.skipFailure {
			b.totalFail.Add(1)
			b.recordFailure(state)
		}
		return zero, err
	}

	b.successes.Add(1)
	if state == HalfOpen || b.failures.Load() > 0 {
		b.Reset()
	}
	return val, nil
}

// recordFailure increments the failure counter and transitions to Open when the threshold is reached.
func (b *Breaker) recordFailure(state State) {
	count := b.failures.Add(1)

	if int(count) >= b.cfg.maxFailures {
		b.mu.Lock()
		if b.state.Load() != uint32(Open) {
			b.state.Store(uint32(Open))
			b.lastOpen.Store(time.Now().UnixNano())
			b.trips.Add(1)
		}
		b.mu.Unlock()
		return
	}

	if state == HalfOpen {
		b.state.Store(uint32(Open))
		b.lastOpen.Store(time.Now().UnixNano())
		b.trips.Add(1)
	}
}
