// Package bulkx provides a thread-safe concurrency limiter for industrial Go
// services.
//
// A bulkhead limits the number of operations that may execute concurrently.
// [Execute] blocks until a slot is available, the context is cancelled, or
// the configured timeout fires. [TryExecute] is the non-blocking variant
// that returns immediately when no slot is available.
//
//	bh := bulkx.New(
//	    bulkx.WithMaxConcurrent(10),
//	    bulkx.WithTimeout(5*time.Second),
//	)
//
//	resp, err := bulkx.Execute(bh, ctx, func(ctx context.Context, bc bulkx.BulkController) (*Response, error) {
//	    if bc.Active() > 8 {
//	        return lightweightResponse(ctx)
//	    }
//	    return client.Call(ctx, req)
//	})
//
// Each call is wrapped with [panix.Safe] for panic recovery; panicked
// functions produce structured [errx.Error] values.
package bulkx

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/pkg/panix"
)

// --- Configuration ---

// config holds bulkhead parameters.
type config struct {
	maxConcurrent int
	timeout       time.Duration
}

// defaultConfig returns sensible bulkhead defaults (10 slots, 30 s timeout).
func defaultConfig() config {
	return config{
		maxConcurrent: 10,
		timeout:       30 * time.Second,
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option func(*config)

// WithMaxConcurrent sets the maximum number of operations that may execute
// simultaneously. Values <= 0 are ignored.
func WithMaxConcurrent(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxConcurrent = n
		}
	}
}

// WithTimeout sets the maximum duration [Execute] will wait to acquire a
// slot. Values <= 0 are ignored.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// --- BulkController ---

// BulkController provides execution context to the bulkhead callback.
// The implementation is private; callers interact only through this interface.
type BulkController interface {
	// Active returns the number of in-flight operations at admission time.
	Active() int
	// MaxConcurrent returns the configured slot count.
	MaxConcurrent() int
	// WaitedSlot reports whether this call went through the slow (timer)
	// path, meaning all slots were busy when the call arrived.
	WaitedSlot() bool
}

// bulkExecution is the private implementation of [BulkController].
type bulkExecution struct {
	active        int
	maxConcurrent int
	waitedSlot    bool
}

// Active implements [BulkController].
func (e *bulkExecution) Active() int { return e.active }

// MaxConcurrent implements [BulkController].
func (e *bulkExecution) MaxConcurrent() int { return e.maxConcurrent }

// WaitedSlot implements [BulkController].
func (e *bulkExecution) WaitedSlot() bool { return e.waitedSlot }

// --- Bulkhead ---

// Bulkhead is a thread-safe concurrency limiter. Create one with [New],
// execute operations with [Execute] or [TryExecute], and shut down with
// [Bulkhead.Close].
type Bulkhead struct {
	cfg      config
	sem      chan struct{}
	active   atomic.Int32
	executed atomic.Uint64
	rejected atomic.Uint64
	timeouts atomic.Uint64
	closed   atomic.Bool
}

// New creates a [Bulkhead] with the given options applied on top of
// sensible defaults (10 concurrent slots, 30 s timeout).
func New(opts ...Option) *Bulkhead {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxConcurrent <= 0 {
		cfg.maxConcurrent = 1
	}
	return &Bulkhead{
		cfg: cfg,
		sem: make(chan struct{}, cfg.maxConcurrent),
	}
}

// --- Execution ---

// run executes fn inside an already-acquired semaphore slot, maintaining
// counters and releasing the slot on return.
func run[T any](b *Bulkhead, ctx context.Context, bc BulkController, fn func(ctx context.Context, bc BulkController) (T, error)) (T, error) {
	defer func() { <-b.sem }()
	b.active.Add(1)
	defer b.active.Add(-1)
	b.executed.Add(1)
	return panix.Safe(ctx, "bulkx.Execute", func(ctx context.Context) (T, error) {
		return fn(ctx, bc)
	})
}

// Execute runs fn within the bulkhead. Because Go methods cannot have type
// parameters, Execute is a package-level generic function that takes the
// [Bulkhead] as its first argument.
//
// It uses a three-phase slot acquisition strategy:
// (1) fast-reject if the context is already cancelled,
// (2) optimistic non-blocking attempt to grab a slot without allocating a timer,
// (3) slow path with a timer when all slots are busy.
//
// The function is wrapped with [panix.Safe] for panic recovery.
//
// Returns [CodeClosed] if the bulkhead has been shut down, [CodeTimeout] if the
// timeout fires before a slot becomes available, or the context error if the
// context is cancelled while waiting.
func Execute[T any](b *Bulkhead, ctx context.Context, fn func(ctx context.Context, bc BulkController) (T, error)) (T, error) {
	var zero T
	if b.closed.Load() {
		return zero, errClosed()
	}

	select {
	case <-ctx.Done():
		b.rejected.Add(1)
		return zero, errCancelled(ctx.Err())
	default:
	}

	select {
	case b.sem <- struct{}{}:
		bc := &bulkExecution{active: int(b.active.Load()), maxConcurrent: b.cfg.maxConcurrent, waitedSlot: false}
		return run(b, ctx, bc, fn)
	default:
	}

	timer := time.NewTimer(b.cfg.timeout)
	defer timer.Stop()

	select {
	case b.sem <- struct{}{}:
		bc := &bulkExecution{active: int(b.active.Load()), maxConcurrent: b.cfg.maxConcurrent, waitedSlot: true}
		return run(b, ctx, bc, fn)
	case <-ctx.Done():
		b.rejected.Add(1)
		return zero, errCancelled(ctx.Err())
	case <-timer.C:
		b.timeouts.Add(1)
		return zero, errTimeout()
	}
}

// TryExecute attempts to run fn without blocking. If a concurrency slot is
// immediately available the function executes and TryExecute returns
// (true, val, nil). If no slot is available it returns (false, zero, nil)
// without executing fn.
//
// Returns (false, zero, errClosed) if the bulkhead has been shut down.
func TryExecute[T any](b *Bulkhead, ctx context.Context, fn func(ctx context.Context, bc BulkController) (T, error)) (bool, T, error) {
	var zero T
	if b.closed.Load() {
		return false, zero, errClosed()
	}

	select {
	case b.sem <- struct{}{}:
		bc := &bulkExecution{active: int(b.active.Load()), maxConcurrent: b.cfg.maxConcurrent, waitedSlot: false}
		val, err := run(b, ctx, bc, fn)
		return true, val, err
	default:
		b.rejected.Add(1)
		return false, zero, nil
	}
}

// --- Accessors ---

// Active returns the number of operations currently executing inside the
// bulkhead.
func (b *Bulkhead) Active() int {
	return int(b.active.Load())
}

// --- Statistics ---

// Stats holds a point-in-time snapshot of bulkhead counters.
type Stats struct {
	MaxConcurrent int    `json:"max_concurrent"`
	Active        int    `json:"active"`
	Executed      uint64 `json:"executed"`
	Rejected      uint64 `json:"rejected"`
	Timeouts      uint64 `json:"timeouts"`
}

// Stats returns a snapshot of bulkhead statistics.
func (b *Bulkhead) Stats() Stats {
	return Stats{
		MaxConcurrent: b.cfg.maxConcurrent,
		Active:        int(b.active.Load()),
		Executed:      b.executed.Load(),
		Rejected:      b.rejected.Load(),
		Timeouts:      b.timeouts.Load(),
	}
}

// ResetStats zeroes all counters.
func (b *Bulkhead) ResetStats() {
	b.executed.Store(0)
	b.rejected.Store(0)
	b.timeouts.Store(0)
}

// --- Lifecycle ---

// Close shuts down the bulkhead, causing all subsequent [Execute] and
// [TryExecute] calls to return [CodeClosed]. Close is idempotent.
func (b *Bulkhead) Close() {
	b.closed.Swap(true)
}

// IsClosed reports whether the bulkhead has been shut down.
func (b *Bulkhead) IsClosed() bool {
	return b.closed.Load()
}
