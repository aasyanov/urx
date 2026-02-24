// Package warmupx provides gradual capacity ramp-up (slow start) for
// industrial Go services.
//
// A [Warmer] increases its capacity from a minimum to a maximum over a
// configurable duration using one of four strategies: [Linear],
// [Exponential], [Logarithmic], or [Step].
//
//	w := warmupx.New(
//	    warmupx.WithDuration(30 * time.Second),
//	    warmupx.WithStrategy(warmupx.Exponential),
//	)
//	w.Start()
//	defer w.Stop()
//
//	if w.Allow() {
//	    // handle request
//	}
//
// Use cases: cold start, post-deploy rollout, circuit-breaker recovery,
// auto-scaling new instances.
package warmupx

import (
	"context"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

// --- Strategy ---

// Strategy defines the capacity ramp-up curve.
type Strategy uint8

const (
	// Linear increases capacity uniformly: capacity(t) = min + delta*t.
	Linear Strategy = iota
	// Exponential increases fast then flattens: capacity(t) = min + delta*(1 - e^(-k*t*5)).
	Exponential
	// Logarithmic increases fast at start, slow at end.
	Logarithmic
	// Step increases in discrete jumps.
	Step
)

const (
	labelLinear      = "linear"
	labelExponential = "exponential"
	labelLogarithmic = "logarithmic"
	labelStep        = "step"
	labelUnknown     = "unknown"
)

// String returns a human-readable label.
func (s Strategy) String() string {
	switch s {
	case Linear:
		return labelLinear
	case Exponential:
		return labelExponential
	case Logarithmic:
		return labelLogarithmic
	case Step:
		return labelStep
	default:
		return labelUnknown
	}
}

// --- Configuration ---

type config struct {
	strategy    Strategy
	minCap      float64
	maxCap      float64
	duration    time.Duration
	interval    time.Duration
	stepCount   int
	expFactor   float64
	onCapChange func(oldCap, newCap float64)
	onComplete  func()
}

func defaultConfig() config {
	return config{
		strategy:  Linear,
		minCap:    0.1,
		maxCap:    1.0,
		duration:  1 * time.Minute,
		stepCount: 10,
		expFactor: 3.0,
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option func(*config)

// WithStrategy sets the ramp-up strategy.
func WithStrategy(s Strategy) Option {
	return func(c *config) { c.strategy = s }
}

// WithMinCapacity sets the starting capacity (clamped to [0, 1]).
func WithMinCapacity(v float64) Option {
	return func(c *config) {
		if v >= 0 && v <= 1 {
			c.minCap = v
		}
	}
}

// WithMaxCapacity sets the target capacity (clamped to [0, 1]).
func WithMaxCapacity(v float64) Option {
	return func(c *config) {
		if v > 0 && v <= 1 {
			c.maxCap = v
		}
	}
}

// WithDuration sets the total warmup duration.
func WithDuration(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.duration = d
		}
	}
}

// WithInterval overrides the capacity-update interval.
// Default: Duration/100, clamped to [10ms, 1s].
func WithInterval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.interval = d
		}
	}
}

// WithStepCount sets the number of steps for the [Step] strategy.
func WithStepCount(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.stepCount = n
		}
	}
}

// WithExpFactor sets the exponential factor for the [Exponential] strategy.
// Higher values mean faster initial ramp.
func WithExpFactor(f float64) Option {
	return func(c *config) {
		if f > 0 {
			c.expFactor = f
		}
	}
}

// WithOnCapacityChange registers a callback invoked asynchronously when
// capacity changes by more than 1%. May be delivered out-of-order.
func WithOnCapacityChange(fn func(oldCap, newCap float64)) Option {
	return func(c *config) { c.onCapChange = fn }
}

// WithOnComplete registers a callback invoked asynchronously when warmup
// finishes.
func WithOnComplete(fn func()) Option {
	return func(c *config) { c.onComplete = fn }
}

// --- Warmer ---

// Warmer provides gradual capacity ramp-up with probabilistic admission
// control. It is safe for concurrent use.
type Warmer struct {
	cfg config

	mu       sync.RWMutex
	start    time.Time
	capacity float64
	warming  bool
	complete bool

	allowed  atomic.Int64
	rejected atomic.Int64

	stopCh     chan struct{}
	stopClosed atomic.Bool
	completeCh chan struct{}
}

// New creates a [Warmer] with the given options. Defaults: Linear strategy,
// 10% → 100% over 1 minute.
func New(opts ...Option) *Warmer {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxCap < cfg.minCap {
		cfg.maxCap = cfg.minCap
	}

	if cfg.interval <= 0 {
		cfg.interval = cfg.duration / 100
	}
	if cfg.interval < 10*time.Millisecond {
		cfg.interval = 10 * time.Millisecond
	}
	if cfg.interval > 1*time.Second {
		cfg.interval = 1 * time.Second
	}

	return &Warmer{
		cfg:        cfg,
		capacity:   cfg.minCap,
		completeCh: make(chan struct{}),
	}
}

// Start begins the warmup from [WithMinCapacity].
func (w *Warmer) Start() {
	w.StartAt(w.cfg.minCap)
}

// StartAt begins (or restarts) warmup at the given capacity.
func (w *Warmer) StartAt(capacity float64) {
	w.mu.Lock()

	if w.stopCh != nil && !w.stopClosed.Load() {
		w.stopClosed.Store(true)
		close(w.stopCh)
	}

	w.start = time.Now()
	w.capacity = capacity
	w.warming = true
	w.complete = false
	w.stopCh = make(chan struct{})
	w.stopClosed.Store(false)
	w.completeCh = make(chan struct{})

	stopCh := w.stopCh
	w.mu.Unlock()

	go w.loop(stopCh)
}

// Stop halts the warmup. Capacity stays at its current value.
func (w *Warmer) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopCh != nil && !w.stopClosed.Load() {
		w.stopClosed.Store(true)
		close(w.stopCh)
	}
	w.stopCh = nil
	w.warming = false
}

// Reset stops and restarts warmup from [WithMinCapacity].
func (w *Warmer) Reset() {
	w.Stop()
	w.Start()
}

// --- Queries ---

// Capacity returns the current capacity in [0, 1].
func (w *Warmer) Capacity() float64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.capacity
}

// IsWarming reports whether warmup is in progress.
func (w *Warmer) IsWarming() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.warming
}

// IsComplete reports whether warmup has finished.
func (w *Warmer) IsComplete() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.complete
}

// Progress returns the warmup progress in [0, 1].
func (w *Warmer) Progress() float64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.progressLocked()
}

// --- Admission ---

// Allow returns true if a request should be admitted based on the current
// capacity. Uses probabilistic admission: with capacity 0.5, approximately
// 50% of calls return true.
func (w *Warmer) Allow() bool {
	cap := w.Capacity()
	if rand.Float64() < cap {
		w.allowed.Add(1)
		return true
	}
	w.rejected.Add(1)
	return false
}

// AllowOrError returns nil if admitted, or an [*errx.Error] with code
// [CodeRejected] and capacity/progress metadata if rejected.
func (w *Warmer) AllowOrError() error {
	if w.Allow() {
		return nil
	}
	w.mu.RLock()
	cap := w.capacity
	prog := w.progressLocked()
	w.mu.RUnlock()
	return errRejected(cap, prog)
}

// MaxRequests scales a base limit by the current capacity, rounding up.
// Returns 0 if baseLimit <= 0.
func (w *Warmer) MaxRequests(baseLimit int) int {
	if baseLimit <= 0 {
		return 0
	}
	return int(math.Ceil(float64(baseLimit) * w.Capacity()))
}

// WaitForCompletion blocks until warmup finishes or ctx is cancelled.
func (w *Warmer) WaitForCompletion(ctx context.Context) error {
	w.mu.RLock()
	if w.complete {
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

// --- Stats ---

// Stats holds a snapshot of warmer state.
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

// Stats returns a snapshot of the warmer state.
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
		if remaining < 0 {
			remaining = 0
		}
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

// ResetStats zeroes the allowed/rejected counters.
func (w *Warmer) ResetStats() {
	w.allowed.Store(0)
	w.rejected.Store(0)
}

// --- Internal ---

func (w *Warmer) progressLocked() float64 {
	if w.complete {
		return 1.0
	}
	if !w.warming || w.start.IsZero() {
		return 0.0
	}
	p := float64(time.Since(w.start)) / float64(w.cfg.duration)
	if p > 1.0 {
		p = 1.0
	}
	return p
}

func (w *Warmer) loop(stopCh <-chan struct{}) {
	ticker := time.NewTicker(w.cfg.interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			w.tick()
		}
	}
}

func (w *Warmer) tick() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.warming || w.complete {
		return
	}

	elapsed := time.Since(w.start)
	if elapsed >= w.cfg.duration {
		old := w.capacity
		w.capacity = w.cfg.maxCap
		w.warming = false
		w.complete = true
		if w.completeCh != nil {
			close(w.completeCh)
		}
		if w.cfg.onCapChange != nil && old != w.capacity {
			go w.cfg.onCapChange(old, w.capacity)
		}
		if w.cfg.onComplete != nil {
			go w.cfg.onComplete()
		}
		return
	}

	t := float64(elapsed) / float64(w.cfg.duration)
	old := w.capacity
	w.capacity = w.calculate(t)

	if w.cfg.onCapChange != nil && math.Abs(w.capacity-old) > 0.01 {
		go w.cfg.onCapChange(old, w.capacity)
	}
}

func (w *Warmer) calculate(t float64) float64 {
	min := w.cfg.minCap
	delta := w.cfg.maxCap - min

	var cap float64
	switch w.cfg.strategy {
	case Linear:
		cap = min + delta*t
	case Exponential:
		cap = min + delta*(1-math.Exp(-w.cfg.expFactor*t*5))
	case Logarithmic:
		cap = min + delta*math.Log(1+t*math.E)/math.Log(1+math.E)
	case Step:
		steps := math.Floor(t * float64(w.cfg.stepCount))
		cap = min + (delta/float64(w.cfg.stepCount))*steps
	default:
		cap = min + delta*t
	}

	if cap > w.cfg.maxCap {
		cap = w.cfg.maxCap
	}
	return cap
}
