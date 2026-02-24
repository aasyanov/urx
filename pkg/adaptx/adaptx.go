// Package adaptx provides adaptive concurrency limiting for industrial Go
// services.
//
// A [Limiter] adjusts its concurrency limit based on latency and error
// feedback using one of three algorithms: [AIMD], [Vegas], or [Gradient].
//
//	l := adaptx.New(
//	    adaptx.WithAlgorithm(adaptx.AIMD),
//	    adaptx.WithInitialLimit(10),
//	)
//	defer l.Close()
//
//	rows, err := adaptx.Do(l, ctx, func(ctx context.Context, ac adaptx.AdaptController) (*sql.Rows, error) {
//	    if ac.InFlight() > ac.Limit()/2 {
//	        return db.QueryContext(ctx, simpleSQL)
//	    }
//	    return db.QueryContext(ctx, complexSQL)
//	})
//
// Use cases: unknown or variable backend capacity, auto-scaling concurrency
// under changing load, reducing overload by lowering the limit when latency
// or errors increase.
//
package adaptx

import (
	"cmp"
	"context"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/panix"
)

// --- Algorithm ---

// Algorithm defines the adaptive strategy.
type Algorithm uint8

const (
	// AIMD — Additive Increase Multiplicative Decrease.
	// On success: limit += IncreaseRate.
	// On failure: limit *= DecreaseRatio.
	AIMD Algorithm = iota
	// Vegas — TCP-Vegas-like, estimates queue build-up from RTT.
	Vegas
	// Gradient — reacts to the trend of latency change.
	Gradient
)

const (
	labelAIMD     = "AIMD"
	labelVegas    = "Vegas"
	labelGradient = "Gradient"
	labelUnknown  = "unknown"
)

// String returns a human-readable label.
func (a Algorithm) String() string {
	switch a {
	case AIMD:
		return labelAIMD
	case Vegas:
		return labelVegas
	case Gradient:
		return labelGradient
	default:
		return labelUnknown
	}
}

// --- Configuration ---

type config struct {
	algorithm      Algorithm
	initialLimit   int
	minLimit       int
	maxLimit       int
	smoothing      float64
	increaseRate   float64
	decreaseRatio  float64
	targetLatency  time.Duration
	tolerance      float64
	sampleWindow   time.Duration
	warmupSamples  int
	minLatDecay    float64
	jitter         float64
	onLimitChange  func(oldLimit, newLimit int)
}

func defaultConfig() config {
	return config{
		algorithm:     AIMD,
		initialLimit:  10,
		minLimit:      1,
		maxLimit:      1000,
		smoothing:     0.2,
		increaseRate:  1.0,
		decreaseRatio: 0.5,
		targetLatency: 100 * time.Millisecond,
		tolerance:     0.1,
		sampleWindow:  1 * time.Second,
		warmupSamples: 10,
		minLatDecay:   0.001,
		jitter:        0.1,
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option func(*config)

// WithAlgorithm sets the adaptation algorithm.
func WithAlgorithm(a Algorithm) Option {
	return func(c *config) { c.algorithm = a }
}

// WithInitialLimit sets the starting concurrency limit.
func WithInitialLimit(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.initialLimit = n
		}
	}
}

// WithMinLimit sets the minimum concurrency limit (floor).
func WithMinLimit(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.minLimit = n
		}
	}
}

// WithMaxLimit sets the maximum concurrency limit (ceiling).
func WithMaxLimit(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxLimit = n
		}
	}
}

// WithSmoothing sets the EMA smoothing factor for latency (0, 1].
func WithSmoothing(f float64) Option {
	return func(c *config) {
		if f > 0 && f <= 1 {
			c.smoothing = f
		}
	}
}

// WithIncreaseRate sets the additive increase per success for [AIMD].
func WithIncreaseRate(r float64) Option {
	return func(c *config) {
		if r > 0 {
			c.increaseRate = r
		}
	}
}

// WithDecreaseRatio sets the multiplicative decrease factor (0, 1).
func WithDecreaseRatio(r float64) Option {
	return func(c *config) {
		if r > 0 && r < 1 {
			c.decreaseRatio = r
		}
	}
}

// WithTargetLatency sets the target latency for [Vegas].
func WithTargetLatency(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.targetLatency = d
		}
	}
}

// WithTolerance sets the acceptable latency deviation for [Vegas] and
// [Gradient]. Default: 0.1 (10%).
func WithTolerance(f float64) Option {
	return func(c *config) {
		if f > 0 && f <= 1 {
			c.tolerance = f
		}
	}
}

// WithSampleWindow sets the window for statistics calculation.
func WithSampleWindow(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.sampleWindow = d
		}
	}
}

// WithWarmupSamples sets the number of samples before adaptation starts.
// Set to 0 to disable warmup.
func WithWarmupSamples(n int) Option {
	return func(c *config) {
		if n >= 0 {
			c.warmupSamples = n
		}
	}
}

// WithMinLatencyDecay sets the decay factor for RTT_min towards avg.
// Prevents sticking on anomalous minimums. 0 = disabled.
func WithMinLatencyDecay(f float64) Option {
	return func(c *config) {
		if f >= 0 && f <= 1 {
			c.minLatDecay = f
		}
	}
}

// WithJitter sets jitter on limit increases to prevent thundering herd.
func WithJitter(f float64) Option {
	return func(c *config) {
		if f >= 0 && f <= 1 {
			c.jitter = f
		}
	}
}

// WithOnLimitChange registers an async callback invoked when the limit changes.
func WithOnLimitChange(fn func(oldLimit, newLimit int)) Option {
	return func(c *config) { c.onLimitChange = fn }
}

// --- AdaptController ---

// AdaptController provides execution context to the function running inside
// the adaptive limiter. The implementation is private; callers interact only
// through this interface.
type AdaptController interface {
	// Limit returns the concurrency limit at admission time.
	Limit() int
	// InFlight returns the in-flight count at admission time.
	InFlight() int
	// Algorithm returns the active adaptation algorithm.
	Algorithm() Algorithm
	// SkipSample tells the limiter not to feed this call's result into the
	// adaptive algorithm. Use for outlier operations (cache misses, cold starts)
	// whose latency would mislead the algorithm. Safe to call multiple times.
	SkipSample()
}

// adaptExecution is the private implementation of [AdaptController].
type adaptExecution struct {
	limit      int
	inFlight   int
	algorithm  Algorithm
	skipSample bool
}

func (e *adaptExecution) Limit() int           { return e.limit }
func (e *adaptExecution) InFlight() int         { return e.inFlight }
func (e *adaptExecution) Algorithm() Algorithm { return e.algorithm }
func (e *adaptExecution) SkipSample()           { e.skipSample = true }

// --- Limiter ---

// Limiter provides adaptive concurrency limiting. It is safe for concurrent use.
type Limiter struct {
	cfg config

	mu       sync.Mutex
	limit    int
	inFlight int32
	closed   int32

	avgLat float64
	minLat float64

	samples     []sample
	head        int
	count       int
	countTotal  int
	maxSamples  int

	total    atomic.Int64
	success  atomic.Int64
	fail     atomic.Int64
	rejected atomic.Int64
	incr     atomic.Int64
	decr     atomic.Int64

	sem chan struct{}
}

type sample struct {
	latency time.Duration
	success bool
	ts      time.Time
}

// New creates a [Limiter] with the given options.
func New(opts ...Option) *Limiter {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxLimit < cfg.minLimit {
		cfg.maxLimit = cfg.minLimit
	}
	if cfg.initialLimit < cfg.minLimit {
		cfg.initialLimit = cfg.minLimit
	}
	if cfg.initialLimit > cfg.maxLimit {
		cfg.initialLimit = cfg.maxLimit
	}

	maxSamples := int(10000 * cfg.sampleWindow.Seconds())
	if maxSamples < 100 {
		maxSamples = 100
	}
	if maxSamples > 10000 {
		maxSamples = 10000
	}

	l := &Limiter{
		cfg:        cfg,
		limit:      cfg.initialLimit,
		minLat:     math.MaxFloat64,
		samples:    make([]sample, maxSamples),
		maxSamples: maxSamples,
		sem:        make(chan struct{}, cfg.maxLimit),
	}
	for i := 0; i < cfg.initialLimit; i++ {
		l.sem <- struct{}{}
	}
	return l
}

// --- Operations ---

// Acquire blocks until a slot is available or ctx is cancelled.
// Returns a release function that MUST be called exactly once with the
// operation outcome. The release function is double-call safe.
func (l *Limiter) Acquire(ctx context.Context) (release func(success bool, latency time.Duration), err error) {
	if atomic.LoadInt32(&l.closed) == 1 {
		return nil, errClosed()
	}

	select {
	case <-ctx.Done():
		return nil, l.ctxErr(ctx.Err())
	case <-l.sem:
	}

	if atomic.LoadInt32(&l.closed) == 1 {
		l.sem <- struct{}{}
		return nil, errClosed()
	}

	cur := atomic.AddInt32(&l.inFlight, 1)
	l.mu.Lock()
	lim := l.limit
	l.mu.Unlock()

	if int(cur) > lim {
		atomic.AddInt32(&l.inFlight, -1)
		l.sem <- struct{}{}
		l.rejected.Add(1)
		return nil, errLimitExceeded()
	}

	l.total.Add(1)

	var released int32
	return func(success bool, latency time.Duration) {
		if !atomic.CompareAndSwapInt32(&released, 0, 1) {
			return
		}
		l.record(success, latency)
		atomic.AddInt32(&l.inFlight, -1)
		select {
		case l.sem <- struct{}{}:
		default:
		}
	}, nil
}

// TryAcquire attempts to acquire a slot without blocking.
// Returns (nil, false) if no slot is available.
func (l *Limiter) TryAcquire() (release func(success bool, latency time.Duration), ok bool) {
	if atomic.LoadInt32(&l.closed) == 1 {
		return nil, false
	}

	select {
	case <-l.sem:
	default:
		l.rejected.Add(1)
		return nil, false
	}

	if atomic.LoadInt32(&l.closed) == 1 {
		l.sem <- struct{}{}
		return nil, false
	}

	cur := atomic.AddInt32(&l.inFlight, 1)
	l.mu.Lock()
	lim := l.limit
	l.mu.Unlock()

	if int(cur) > lim {
		atomic.AddInt32(&l.inFlight, -1)
		l.sem <- struct{}{}
		l.rejected.Add(1)
		return nil, false
	}

	l.total.Add(1)

	var released int32
	return func(success bool, latency time.Duration) {
		if !atomic.CompareAndSwapInt32(&released, 0, 1) {
			return
		}
		l.record(success, latency)
		atomic.AddInt32(&l.inFlight, -1)
		select {
		case l.sem <- struct{}{}:
		default:
		}
	}, true
}

// Do acquires a slot, executes fn with [panix.Safe] recovery, measures
// latency, and releases. Because Go methods cannot have type parameters,
// Do is a package-level generic function that takes the [Limiter] as its
// first argument. This is the recommended way to use the limiter.
func Do[T any](l *Limiter, ctx context.Context, fn func(ctx context.Context, ac AdaptController) (T, error)) (T, error) {
	var zero T
	release, err := l.Acquire(ctx)
	if err != nil {
		return zero, err
	}

	ac := &adaptExecution{
		limit:     l.Limit(),
		inFlight:  l.InFlight(),
		algorithm: l.cfg.algorithm,
	}

	start := time.Now()
	val, err := panix.Safe(ctx, "adaptx.Do", func(ctx context.Context) (T, error) {
		return fn(ctx, ac)
	})

	if ac.skipSample {
		release(true, 0)
	} else {
		release(err == nil, time.Since(start))
	}
	return val, err
}

// --- Queries ---

// Limit returns the current concurrency limit.
func (l *Limiter) Limit() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}

// InFlight returns the number of in-flight requests.
func (l *Limiter) InFlight() int {
	return int(atomic.LoadInt32(&l.inFlight))
}

// --- Lifecycle ---

// CloseWithTimeout gracefully shuts down the limiter, waiting up to the
// given duration for in-flight requests to finish. After Close,
// Acquire/TryAcquire return [CodeClosed]. A zero timeout means no wait.
func (l *Limiter) CloseWithTimeout(timeout time.Duration) error {
	if !atomic.CompareAndSwapInt32(&l.closed, 0, 1) {
		return errClosed()
	}
	if timeout > 0 {
		deadline := time.Now().Add(timeout)
		for atomic.LoadInt32(&l.inFlight) > 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	for {
		select {
		case <-l.sem:
		default:
			return nil
		}
	}
}

// Close gracefully shuts down the limiter. Waits up to 30 seconds for
// in-flight requests to finish. For custom timeout, use [Limiter.CloseWithTimeout].
func (l *Limiter) Close() error {
	return l.CloseWithTimeout(30 * time.Second)
}

// --- Stats ---

// Stats holds a snapshot of the limiter state.
type Stats struct {
	Algorithm string `json:"algorithm"`
	Limit     int    `json:"limit"`
	MinLimit  int    `json:"min_limit"`
	MaxLimit  int    `json:"max_limit"`
	InFlight  int    `json:"in_flight"`
	Total     int64  `json:"total"`
	Success   int64  `json:"success"`
	Failures  int64  `json:"failures"`
	Rejected  int64  `json:"rejected"`
	Increases int64  `json:"increases"`
	Decreases int64  `json:"decreases"`
	AvgLat    time.Duration `json:"avg_latency"`
	MinLat    time.Duration `json:"min_latency"`
	MaxLat    time.Duration `json:"max_latency"`
	P50Lat    time.Duration `json:"p50_latency"`
	P99Lat    time.Duration `json:"p99_latency"`
}

// Stats returns a snapshot of the limiter state with latency percentiles.
func (l *Limiter) Stats() Stats {
	l.mu.Lock()
	lim := l.limit
	copies := make([]sample, l.count)
	for i := 0; i < l.count; i++ {
		idx := (l.head - l.count + i + l.maxSamples) % l.maxSamples
		copies[i] = l.samples[idx]
	}
	l.mu.Unlock()

	cutoff := time.Now().Add(-l.cfg.sampleWindow)
	var totalLat, minL, maxL time.Duration
	lats := make([]time.Duration, 0, len(copies))
	for _, s := range copies {
		if s.ts.After(cutoff) {
			lats = append(lats, s.latency)
			totalLat += s.latency
			if minL == 0 || s.latency < minL {
				minL = s.latency
			}
			if s.latency > maxL {
				maxL = s.latency
			}
		}
	}

	var avgL, p50, p99 time.Duration
	if len(lats) > 0 {
		avgL = totalLat / time.Duration(len(lats))
		slices.SortFunc(lats, func(a, b time.Duration) int { return cmp.Compare(a, b) })
		p50 = lats[int(float64(len(lats)-1)*0.50)]
		p99 = lats[int(float64(len(lats)-1)*0.99)]
	}

	return Stats{
		Algorithm: l.cfg.algorithm.String(),
		Limit:     lim,
		MinLimit:  l.cfg.minLimit,
		MaxLimit:  l.cfg.maxLimit,
		InFlight:  int(atomic.LoadInt32(&l.inFlight)),
		Total:     l.total.Load(),
		Success:   l.success.Load(),
		Failures:  l.fail.Load(),
		Rejected:  l.rejected.Load(),
		Increases: l.incr.Load(),
		Decreases: l.decr.Load(),
		AvgLat:    avgL,
		MinLat:    minL,
		MaxLat:    maxL,
		P50Lat:    p50,
		P99Lat:    p99,
	}
}

// ResetStats zeroes all counters and resets adaptive state.
func (l *Limiter) ResetStats() {
	l.mu.Lock()
	l.limit = l.cfg.initialLimit
	l.avgLat = 0
	l.minLat = math.MaxFloat64
	l.head = 0
	l.count = 0
	l.countTotal = 0
	for i := range l.samples {
		l.samples[i] = sample{}
	}
	l.mu.Unlock()

	l.total.Store(0)
	l.success.Store(0)
	l.fail.Store(0)
	l.rejected.Store(0)
	l.incr.Store(0)
	l.decr.Store(0)
}

// --- Internal ---

func (l *Limiter) ctxErr(err error) *errx.Error {
	if err == context.DeadlineExceeded {
		return errTimeout(err)
	}
	return errCancelled(err)
}

func (l *Limiter) record(success bool, latency time.Duration) {
	if success {
		l.success.Add(1)
	} else {
		l.fail.Add(1)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.samples[l.head] = sample{latency: latency, success: success, ts: time.Now()}
	l.head = (l.head + 1) % l.maxSamples
	if l.count < l.maxSamples {
		l.count++
	}
	l.countTotal++

	ns := float64(latency.Nanoseconds())
	l.avgLat = l.cfg.smoothing*ns + (1-l.cfg.smoothing)*l.avgLat

	if l.cfg.minLatDecay > 0 && l.minLat != math.MaxFloat64 {
		l.minLat += l.cfg.minLatDecay * (l.avgLat - l.minLat)
	}
	if ns < l.minLat && ns > 0 {
		l.minLat = ns
	}

	if l.countTotal < l.cfg.warmupSamples {
		return
	}
	l.adjust(success, latency)
}

func (l *Limiter) adjust(success bool, latency time.Duration) {
	old := l.limit
	var next int

	switch l.cfg.algorithm {
	case AIMD:
		next = l.aimd(success)
	case Vegas:
		next = l.vegas(latency)
	case Gradient:
		next = l.gradient(success, latency)
	default:
		next = l.aimd(success)
	}

	if next > old && l.cfg.jitter > 0 {
		inc := next - old
		j := int(float64(inc) * l.cfg.jitter * rand.Float64())
		if rand.Float64() < 0.5 {
			next -= j
		}
	}

	if next < l.cfg.minLimit {
		next = l.cfg.minLimit
	}
	if next > l.cfg.maxLimit {
		next = l.cfg.maxLimit
	}

	if next != old {
		l.limit = next
		if next > old {
			l.incr.Add(1)
			for i := 0; i < next-old; i++ {
				select {
				case l.sem <- struct{}{}:
				default:
				}
			}
		} else {
			l.decr.Add(1)
		}
		if l.cfg.onLimitChange != nil {
			go l.cfg.onLimitChange(old, next)
		}
	}
}

func (l *Limiter) aimd(success bool) int {
	if success {
		return l.limit + int(l.cfg.increaseRate)
	}
	return int(float64(l.limit) * l.cfg.decreaseRatio)
}

func (l *Limiter) vegas(latency time.Duration) int {
	if l.minLat == math.MaxFloat64 {
		return l.limit
	}
	rtt := float64(latency.Nanoseconds())
	if rtt <= 0 {
		return l.limit
	}
	queue := float64(l.limit) * (rtt - l.minLat) / l.minLat
	target := float64(l.limit) * l.cfg.tolerance

	if queue < target {
		return l.limit + 1
	}
	if queue > target*2 {
		return int(float64(l.limit) * l.cfg.decreaseRatio)
	}
	return l.limit
}

func (l *Limiter) gradient(success bool, latency time.Duration) int {
	if !success {
		return int(float64(l.limit) * l.cfg.decreaseRatio)
	}
	if l.avgLat <= 0 {
		return l.limit + 1
	}
	g := (float64(latency.Nanoseconds()) - l.avgLat) / l.avgLat

	if g < -l.cfg.tolerance {
		return l.limit + 2
	}
	if g <= l.cfg.tolerance {
		return l.limit + 1
	}
	f := 1 - g*l.cfg.decreaseRatio
	if f < l.cfg.decreaseRatio {
		f = l.cfg.decreaseRatio
	}
	return int(float64(l.limit) * f)
}
