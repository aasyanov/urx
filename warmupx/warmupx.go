// Package warmupx provides gradual capacity ramp-up (slow start) with
// probabilistic admission control for production Go services.
//
// A [Warmer] increases its admission capacity from a minimum to a maximum over
// a configurable duration following one of four strategies — [Linear],
// [Exponential], [Logarithmic], or [Step]. While warming, it admits requests
// probabilistically: at capacity 0.5 roughly half of [Warmer.Allow] calls
// return true. This protects a cold instance (empty caches, unprimed JIT, cold
// connection pools) from a full traffic spike at startup.
//
//	w := warmupx.New(
//	    warmupx.WithDuration(30*time.Second),
//	    warmupx.WithStrategy(warmupx.Exponential),
//	)
//	w.Start()
//	defer w.Stop()
//
//	out, err := warmupx.Execute(w, ctx, func(ctx context.Context, wc warmupx.WarmupController) (Result, error) {
//	    return serve(ctx, wc.Capacity())
//	})
//
// Because Go methods cannot have type parameters, [Execute] and [TryExecute] are
// package-level generic functions taking the [Warmer] as their first argument.
// [TryExecute] is the non-blocking variant: when probabilistic admission fails
// it returns (false, zero, nil) instead of [ErrRejected].
//
// The [Execute] callback receives a [WarmupController] exposing the capacity
// and progress at admission time plus a [WarmupController.Reject] control, so a
// handler can scale work to the instance's readiness or opt out late.
//
// Typical uses: cold start, post-deploy rollout, circuit-breaker recovery, and
// freshly auto-scaled instances.
//
// # Dependencies
//
// warmupx depends only on the Go standard library and the urx panix package.
package warmupx

import (
	"context"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/panix"
)

// opExecute labels panics recovered while running an [Execute] callback.
const opExecute = "warmupx.Execute"

// opTryExecute labels panics recovered while running a [TryExecute] callback.
const opTryExecute = "warmupx.TryExecute"

// Warmer provides gradual capacity ramp-up with probabilistic admission
// control. It is safe for concurrent use from multiple goroutines.
//
// Create one with [New], begin ramping with [Warmer.Start], and gate work with
// [Warmer.Allow], [Warmer.AllowOrError], [Execute], or [TryExecute]. Call
// [Warmer.Stop] to halt the ramp.
type Warmer struct {
	cfg config

	mu       sync.RWMutex
	start    time.Time
	capacity float64
	warming  bool
	complete bool

	// gen identifies the active warmup run. Start/StartAt bump it so a stale
	// loop goroutine from a previous run becomes a no-op once it sees a newer
	// generation, even before its stop channel is observed.
	gen        uint64
	stopCh     chan struct{}
	completeCh chan struct{}

	allowed  atomic.Int64
	rejected atomic.Int64
}

// New creates a [Warmer] with the given options applied on top of the package
// defaults: [Linear] strategy ramping from [DefaultMinCapacity] to
// [DefaultMaxCapacity] over [DefaultDuration]. The warmer does not ramp until
// [Warmer.Start] is called.
func New(opts ...Option) *Warmer {
	cfg := newConfig(opts)
	return &Warmer{
		cfg:        cfg,
		capacity:   cfg.minCap,
		completeCh: make(chan struct{}),
	}
}

// Start begins (or restarts) the warmup from the configured minimum capacity.
func (w *Warmer) Start() {
	w.StartAt(w.cfg.minCap)
}

// StartAt begins (or restarts) the warmup from the given capacity. Any
// in-progress ramp is stopped first. The capacity is clamped to the configured
// [minimum, maximum] range.
func (w *Warmer) StartAt(capacity float64) {
	if capacity < w.cfg.minCap {
		capacity = w.cfg.minCap
	}
	if capacity > w.cfg.maxCap {
		capacity = w.cfg.maxCap
	}

	w.mu.Lock()
	w.stopLocked()

	w.gen++
	gen := w.gen
	w.start = time.Now()
	w.capacity = capacity
	w.warming = true
	w.complete = false
	w.stopCh = make(chan struct{})
	w.completeCh = make(chan struct{})
	stopCh := w.stopCh
	w.mu.Unlock()

	go w.loop(gen, stopCh)
}

// Stop halts the warmup. The current capacity is retained; subsequent
// admission decisions use it unchanged until the next [Warmer.Start]. Stop is
// idempotent.
func (w *Warmer) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
	w.warming = false
}

// stopLocked closes the active stop channel (if any) and clears it. The caller
// must hold w.mu.
func (w *Warmer) stopLocked() {
	if w.stopCh != nil {
		close(w.stopCh)
		w.stopCh = nil
	}
}

// Reset stops the warmer and restarts the ramp from the configured minimum
// capacity.
func (w *Warmer) Reset() {
	w.StartAt(w.cfg.minCap)
}

// --- Queries ---

// Capacity returns the current admission capacity in [0, 1].
func (w *Warmer) Capacity() float64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.capacity
}

// Strategy returns the configured ramp-up strategy.
func (w *Warmer) Strategy() Strategy { return w.cfg.strategy }

// IsWarming reports whether a ramp is currently in progress.
func (w *Warmer) IsWarming() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.warming
}

// IsComplete reports whether the warmup has reached full capacity.
func (w *Warmer) IsComplete() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.complete
}

// Progress returns the warmup progress in [0, 1], where 1 means the warmup has
// completed.
func (w *Warmer) Progress() float64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.progressLocked()
}

// --- Admission ---

// Allow reports whether a request should be admitted given the current
// capacity, using probabilistic admission: at capacity c approximately a
// fraction c of calls return true. It updates the allowed/rejected counters
// reported by [Warmer.Stats].
func (w *Warmer) Allow() bool {
	w.mu.RLock()
	capacity := w.capacity
	w.mu.RUnlock()

	if rand.Float64() < capacity {
		w.allowed.Add(1)
		return true
	}
	w.rejected.Add(1)
	return false
}

// AllowOrError returns nil if a request is admitted, or an error wrapping
// [ErrRejected] (carrying the capacity and progress that the admission
// decision was made against) if it is rejected.
func (w *Warmer) AllowOrError() error {
	w.mu.RLock()
	capacity, progress := w.capacity, w.progressLocked()
	w.mu.RUnlock()

	if rand.Float64() < capacity {
		w.allowed.Add(1)
		return nil
	}
	w.rejected.Add(1)
	return errRejected(capacity, progress)
}

// MaxRequests scales a base limit by the current capacity, rounding up. Returns
// at least 1 when baseLimit > 0 and capacity > 0. Returns 0 when baseLimit <= 0
// or capacity == 0.
func (w *Warmer) MaxRequests(baseLimit int) int {
	if baseLimit <= 0 {
		return 0
	}
	capacity := w.Capacity()
	if capacity <= 0 {
		return 0
	}
	return int(math.Ceil(float64(baseLimit) * capacity))
}

// WaitForCompletion blocks until the warmup completes or ctx is cancelled.
// It returns nil on completion or ctx.Err() if the context is cancelled first.
//
// If warmup has never been started, it returns nil immediately because there is
// no active ramp to wait on.
//
// A warmer halted with [Warmer.Stop] before completion never completes, so a
// waiter unblocks only when ctx is cancelled. A subsequent [Warmer.Start]
// begins a new run with its own completion signal; callers should re-invoke
// WaitForCompletion after restarting.
func (w *Warmer) WaitForCompletion(ctx context.Context) error {
	w.mu.RLock()
	if w.complete {
		w.mu.RUnlock()
		return nil
	}
	if !w.warming && w.start.IsZero() {
		w.mu.RUnlock()
		return nil
	}
	ch := w.completeCh
	w.mu.RUnlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	}
}

// Execute runs fn only if the warmer admits the call, gating it through the
// same probabilistic admission as [Warmer.Allow]. Because Go methods cannot
// have type parameters, Execute is a package-level generic function taking the
// [Warmer] as its first argument.
//
// If the call is rejected, Execute returns the zero value and an error wrapping
// [ErrRejected] without invoking fn. If fn is nil, Execute returns [ErrNilFunc].
// If ctx is already cancelled or its deadline has expired, Execute returns
// [ErrCancelled] without attempting admission.
//
// On admission fn receives the call context and a [WarmupController] exposing
// the capacity and progress at admission time. fn runs under [panix.Safe]; a
// panic becomes a [*panix.PanicError]. If fn calls [WarmupController.Reject],
// Execute discards fn's result, returns [ErrRejected], and counts the call as
// rejected rather than allowed.
func Execute[T any](w *Warmer, ctx context.Context, fn WarmupFunc[T]) (T, error) {
	var zero T
	if fn == nil {
		return zero, ErrNilFunc
	}
	if err := ctx.Err(); err != nil {
		return zero, errCancelled(err)
	}

	capacity, progress, strategy, ok := w.tryAdmit()
	if !ok {
		return zero, errRejected(capacity, progress)
	}
	return executeRun(w, ctx, w.cfg.opOrDefault(), capacity, progress, strategy, fn)
}

// TryExecute attempts to run fn using the same probabilistic admission as
// [Execute]. If the call is admitted the function executes and TryExecute
// returns (true, val, err). If the call is rejected it returns (false, zero, nil)
// without invoking fn and increments the rejected counter.
//
// Returns (false, zero, [ErrNilFunc]) if fn is nil, and (false, zero,
// [ErrCancelled]) if ctx is already cancelled or its deadline has expired (no
// admission attempted). When admitted, fn runs under [panix.Safe] with the same
// outcome semantics as [Execute], including [WarmupController.Reject] and panic
// recovery.
func TryExecute[T any](w *Warmer, ctx context.Context, fn WarmupFunc[T]) (bool, T, error) {
	var zero T
	if fn == nil {
		return false, zero, ErrNilFunc
	}
	if err := ctx.Err(); err != nil {
		return false, zero, errCancelled(err)
	}

	capacity, progress, strategy, ok := w.tryAdmit()
	if !ok {
		return false, zero, nil
	}
	val, err := executeRun(w, ctx, w.cfg.opOrDefaultTry(), capacity, progress, strategy, fn)
	return true, val, err
}

// tryAdmit reads warmer state and applies probabilistic admission. It returns
// the capacity and progress observed at the decision point, the configured
// strategy, and whether the call was admitted.
func (w *Warmer) tryAdmit() (capacity, progress float64, strategy Strategy, ok bool) {
	w.mu.RLock()
	capacity, progress = w.capacity, w.progressLocked()
	strategy = w.cfg.strategy
	w.mu.RUnlock()

	if rand.Float64() >= capacity {
		w.rejected.Add(1)
		return capacity, progress, strategy, false
	}
	return capacity, progress, strategy, true
}

// executeRun runs fn after admission and settles counters. The caller must
// have already passed guard checks and won admission via tryAdmit.
func executeRun[T any](w *Warmer, ctx context.Context, op string, capacity, progress float64, strategy Strategy, fn WarmupFunc[T]) (T, error) {
	var zero T
	wc := &execution{capacity: capacity, progress: progress, strategy: strategy}
	val, err := panix.Safe(op, func() (T, error) {
		return fn(ctx, wc)
	})
	if err != nil {
		return zero, err
	}
	if wc.rejected {
		w.rejected.Add(1)
		return zero, errRejected(capacity, progress)
	}
	w.allowed.Add(1)
	return val, nil
}

// --- Stats ---

// Stats holds a point-in-time snapshot of warmer state.
type Stats struct {
	Strategy    string        `json:"strategy"`
	Capacity    float64       `json:"capacity"`
	MinCapacity float64       `json:"min_capacity"`
	MaxCapacity float64       `json:"max_capacity"`
	Progress    float64       `json:"progress"`
	IsWarming   bool          `json:"is_warming"`
	IsComplete  bool          `json:"is_complete"`
	Duration    time.Duration `json:"duration"`
	Elapsed     time.Duration `json:"elapsed"`
	Remaining   time.Duration `json:"remaining"`
	Allowed     int64         `json:"allowed"`
	Rejected    int64         `json:"rejected"`
}

// Stats returns a snapshot of the warmer state and admission counters.
func (w *Warmer) Stats() Stats {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var elapsed, remaining time.Duration
	if !w.start.IsZero() {
		elapsed = time.Since(w.start)
		if elapsed > w.cfg.duration {
			elapsed = w.cfg.duration
		}
		remaining = w.cfg.duration - elapsed
	}

	return Stats{
		Strategy:    w.cfg.strategy.String(),
		Capacity:    w.capacity,
		MinCapacity: w.cfg.minCap,
		MaxCapacity: w.cfg.maxCap,
		Progress:    w.progressLocked(),
		IsWarming:   w.warming,
		IsComplete:  w.complete,
		Duration:    w.cfg.duration,
		Elapsed:     elapsed,
		Remaining:   remaining,
		Allowed:     w.allowed.Load(),
		Rejected:    w.rejected.Load(),
	}
}

// ResetStats zeroes the allowed and rejected counters without affecting the
// ramp state.
func (w *Warmer) ResetStats() {
	w.allowed.Store(0)
	w.rejected.Store(0)
}

// --- Internal ---

// progressLocked returns warmup progress in [0, 1]. The caller must hold w.mu.
func (w *Warmer) progressLocked() float64 {
	if w.complete {
		return 1.0
	}
	if !w.warming || w.start.IsZero() {
		return 0.0
	}
	p := float64(time.Since(w.start)) / float64(w.cfg.duration)
	if p > 1.0 {
		return 1.0
	}
	return p
}

// loop drives periodic capacity updates for the run identified by gen until
// stopCh is closed or warmup completes.
func (w *Warmer) loop(gen uint64, stopCh <-chan struct{}) {
	ticker := time.NewTicker(w.cfg.interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			if w.tick(gen) {
				return
			}
		}
	}
}

// tick advances the capacity for the active run. It returns true once warmup
// has completed (or the run is stale), signalling [Warmer.loop] to exit.
func (w *Warmer) tick(gen uint64) bool {
	w.mu.Lock()

	if w.gen != gen || !w.warming || w.complete {
		w.mu.Unlock()
		return true
	}

	elapsed := time.Since(w.start)
	if elapsed >= w.cfg.duration {
		old := w.capacity
		w.capacity = w.cfg.maxCap
		w.warming = false
		w.complete = true
		close(w.completeCh)
		onChange, onComplete := w.cfg.onCapChange, w.cfg.onComplete
		newCap := w.capacity
		w.mu.Unlock()

		if onChange != nil && old != newCap {
			go onChange(old, newCap)
		}
		if onComplete != nil {
			go onComplete()
		}
		return true
	}

	t := float64(elapsed) / float64(w.cfg.duration)
	old := w.capacity
	w.capacity = w.calculate(t)
	onChange := w.cfg.onCapChange
	newCap := w.capacity
	w.mu.Unlock()

	if onChange != nil && math.Abs(newCap-old) > capacityEpsilon {
		go onChange(old, newCap)
	}
	return false
}

// calculate maps fractional progress t in [0, 1] to a capacity in
// [minCap, maxCap] using the configured strategy. t is clamped to [0, 1] so the
// curve functions stay in their valid domain regardless of the caller.
func (w *Warmer) calculate(t float64) float64 {
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}

	base := w.cfg.minCap
	delta := w.cfg.maxCap - base

	var value float64
	switch w.cfg.strategy {
	case Linear:
		value = base + delta*t
	case Exponential:
		value = base + delta*(1-math.Exp(-w.cfg.expFactor*t))
	case Logarithmic:
		value = base + delta*math.Log(1+t*math.E)/math.Log(1+math.E)
	case Step:
		steps := math.Floor(t * float64(w.cfg.stepCount))
		value = base + (delta/float64(w.cfg.stepCount))*steps
	default:
		value = base + delta*t
	}

	if value > w.cfg.maxCap {
		return w.cfg.maxCap
	}
	return value
}
