// Package shedx provides priority-based load shedding for industrial Go
// services.
//
// A [Shedder] tracks in-flight operations and rejects new ones when the
// system is overloaded. Requests carry a [Priority]; lower-priority requests
// are shed first while [PriorityCritical] requests are never rejected.
//
//	s := shedx.New(
//	    shedx.WithCapacity(1000),
//	    shedx.WithThreshold(0.8),
//	)
//	defer s.Close()
//
//	resp, err := shedx.Execute(s, ctx, shedx.PriorityNormal, func(ctx context.Context, sc shedx.ShedController) (*Response, error) {
//	    if sc.Load() > 0.9 {
//	        return quickResponse(ctx)
//	    }
//	    return handler.Serve(ctx, req)
//	})
//
// Each call is wrapped with [panix.Safe] for panic recovery; panicked
// functions produce structured [errx.Error] values.
package shedx

import (
	"context"
	"sync/atomic"

	"github.com/aasyanov/urx/pkg/panix"
)

// --- Priority ---

// Priority defines request priority levels. Higher values have higher
// priority and are shed last.
type Priority uint8

const (
	// PriorityLow — background tasks, pre-fetches, analytics.
	PriorityLow Priority = iota
	// PriorityNormal — regular user requests.
	PriorityNormal
	// PriorityHigh — important operations, paid-tier requests.
	PriorityHigh
	// PriorityCritical — health checks, auth, never shed.
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

// String returns a human-readable label.
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

// --- Configuration ---

// config holds shedder parameters.
type config struct {
	capacity  int
	threshold float64
}

// defaultConfig returns sensible shedder defaults (1000 capacity, 0.8 threshold).
func defaultConfig() config {
	return config{
		capacity:  1000,
		threshold: 0.8,
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option func(*config)

// WithCapacity sets the maximum number of in-flight operations.
// Values <= 0 are ignored.
func WithCapacity(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.capacity = n
		}
	}
}

// WithThreshold sets the load fraction at which shedding begins.
// Values outside (0, 1] are ignored.
func WithThreshold(t float64) Option {
	return func(c *config) {
		if t > 0 && t <= 1 {
			c.threshold = t
		}
	}
}

// --- ShedController ---

// ShedController provides execution context to the shedded function.
// The implementation is private; callers interact only through this interface.
type ShedController interface {
	// Priority returns the priority this request was admitted with.
	Priority() Priority
	// Load returns the load ratio (inflight/capacity) at admission time.
	Load() float64
	// InFlight returns the in-flight count at admission time.
	InFlight() int64
}

// shedExecution is the private implementation of [ShedController].
type shedExecution struct {
	priority Priority
	load     float64
	inFlight int64
}

// Priority implements [ShedController].
func (e *shedExecution) Priority() Priority { return e.priority }

// Load implements [ShedController].
func (e *shedExecution) Load() float64 { return e.load }

// InFlight implements [ShedController].
func (e *shedExecution) InFlight() int64 { return e.inFlight }

// --- Shedder ---

// Shedder is a thread-safe, priority-based load shedder. Create one with
// [New], execute operations with [Shedder.Execute] or check admission with
// [Shedder.Allow], and shut down with [Shedder.Close].
type Shedder struct {
	cfg      config
	inflight atomic.Int64
	admitted atomic.Int64
	shed     atomic.Int64
	closed   atomic.Bool
}

// New creates a [Shedder] with the given options applied on top of
// sensible defaults (1000 capacity, 0.8 threshold).
func New(opts ...Option) *Shedder {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.capacity <= 0 {
		cfg.capacity = 1
	}
	if cfg.threshold <= 0 || cfg.threshold > 1 {
		cfg.threshold = 0.8
	}
	return &Shedder{cfg: cfg}
}

// --- Admission ---

// Allow reports whether a request with the given priority should be admitted.
// It does NOT track the request; use [Shedder.Execute] for tracked execution.
func (s *Shedder) Allow(priority Priority) bool {
	if s.closed.Load() {
		return false
	}
	return s.shouldAdmit(priority)
}

// --- Execution ---

// Execute runs fn if the request is admitted based on current load and
// priority. Because Go methods cannot have type parameters, Execute is a
// package-level generic function that takes the [Shedder] as its first
// argument.
//
// If the request is shed, [CodeRejected] is returned immediately without
// executing fn. The function receives a [ShedController] with load/inflight
// snapshotted at admission time. The function is wrapped with [panix.Safe]
// for panic recovery.
func Execute[T any](s *Shedder, ctx context.Context, priority Priority, fn func(ctx context.Context, sc ShedController) (T, error)) (T, error) {
	var zero T
	if s.closed.Load() {
		return zero, errClosed()
	}

	if !s.shouldAdmit(priority) {
		s.shed.Add(1)
		return zero, errRejected(priority)
	}

	sc := &shedExecution{
		priority: priority,
		load:     s.Load(),
		inFlight: s.inflight.Load(),
	}

	s.inflight.Add(1)
	s.admitted.Add(1)
	defer s.inflight.Add(-1)

	return panix.Safe(ctx, "shedx.Execute", func(ctx context.Context) (T, error) {
		return fn(ctx, sc)
	})
}

// --- Admission logic ---

// shouldAdmit decides whether a request at the given priority should pass based on current load.
func (s *Shedder) shouldAdmit(priority Priority) bool {
	if priority >= PriorityCritical {
		return true
	}

	load := s.Load()
	if load < s.cfg.threshold {
		return true
	}

	// Above threshold: progressively shed by priority.
	// overload is 0..1 representing how far above threshold we are.
	overload := (load - s.cfg.threshold) / (1 - s.cfg.threshold)

	switch priority {
	case PriorityLow:
		return overload < 0.25
	case PriorityNormal:
		return overload < 0.60
	case PriorityHigh:
		return overload < 0.90
	default:
		return true
	}
}

// --- Accessors ---

// Load returns the current load fraction (inflight / capacity), in [0, 1+].
func (s *Shedder) Load() float64 {
	return float64(s.inflight.Load()) / float64(s.cfg.capacity)
}

// InFlight returns the number of operations currently executing.
func (s *Shedder) InFlight() int64 {
	return s.inflight.Load()
}

// Stats returns a snapshot of shedder statistics.
func (s *Shedder) Stats() Stats {
	return Stats{
		Capacity:  s.cfg.capacity,
		Threshold: s.cfg.threshold,
		InFlight:  s.inflight.Load(),
		Admitted:  s.admitted.Load(),
		Shed:      s.shed.Load(),
	}
}

// Stats holds shedder statistics.
type Stats struct {
	Capacity  int     `json:"capacity"`
	Threshold float64 `json:"threshold"`
	InFlight  int64   `json:"in_flight"`
	Admitted  int64   `json:"admitted"`
	Shed      int64   `json:"shed"`
}

// ResetStats zeroes all counters.
func (s *Shedder) ResetStats() {
	s.admitted.Store(0)
	s.shed.Store(0)
}

// --- Lifecycle ---

// Close shuts down the shedder, causing all subsequent [Execute] calls to
// return [CodeClosed]. Close is idempotent.
func (s *Shedder) Close() {
	s.closed.Swap(true)
}

// IsClosed reports whether the shedder has been shut down.
func (s *Shedder) IsClosed() bool {
	return s.closed.Load()
}
