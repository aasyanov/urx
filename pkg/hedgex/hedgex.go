// Package hedgex provides hedging (speculative execution) for reducing tail
// latency in industrial Go services.
//
// A [Hedger] launches the same request against one or more backends with
// staggered delays, and returns the first successful result. Remaining
// in-flight requests are cancelled when a winner arrives.
//
//	h := hedgex.New[string](
//	    hedgex.WithDelay(100 * time.Millisecond),
//	    hedgex.WithMaxParallel(3),
//	)
//
//	val, err := h.Do(ctx, func(ctx context.Context, hc hedgex.HedgeController) (string, error) {
//	    if hc.IsHedge() {
//	        return fetchFromReplica(ctx)
//	    }
//	    return fetchFromPrimary(ctx)
//	})
//
package hedgex

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Configuration ---

type config struct {
	maxParallel int
	delay       time.Duration
	maxDelay    time.Duration
	onHedge     func(attempt int)
}

func defaultConfig() config {
	return config{
		maxParallel: 3,
		delay:       100 * time.Millisecond,
		maxDelay:    1 * time.Second,
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option func(*config)

// WithMaxParallel sets the maximum number of parallel requests.
// Default: 3.
func WithMaxParallel(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxParallel = n
		}
	}
}

// WithDelay sets the wait time before launching the next hedge request.
// Default: 100ms.
func WithDelay(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.delay = d
		}
	}
}

// WithMaxDelay caps the total stagger window. Requests beyond MaxDelay
// are spread evenly to avoid bursts. Default: 1s.
func WithMaxDelay(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.maxDelay = d
		}
	}
}

// WithOnHedge registers an async callback invoked when a hedge request
// is launched. attempt is 2 for the first hedge, 3 for the second, etc.
func WithOnHedge(fn func(attempt int)) Option {
	return func(c *config) { c.onHedge = fn }
}

// --- HedgeController ---

// HedgeController provides execution context to the hedged function.
// The implementation is private; callers interact only through this interface.
type HedgeController interface {
	// Attempt returns the 1-based attempt number.
	// 1 = original request, 2 = first hedge, 3 = second hedge, etc.
	Attempt() int
	// IsHedge reports whether this is a speculative hedge copy (Attempt > 1).
	IsHedge() bool
}

// hedgeExecution is the private implementation of [HedgeController].
type hedgeExecution struct {
	attempt int
}

func (e *hedgeExecution) Attempt() int { return e.attempt }
func (e *hedgeExecution) IsHedge() bool { return e.attempt > 1 }

// --- Hedger ---

// Hedger provides hedging (speculative execution) for reducing tail latency.
// It is safe for concurrent use.
type Hedger[T any] struct {
	cfg config

	calls    atomic.Int64
	wins     atomic.Int64
	failures atomic.Int64
	hedges   atomic.Int64
}

// New creates a [Hedger] with the given options.
func New[T any](opts ...Option) *Hedger[T] {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxDelay < cfg.delay {
		cfg.maxDelay = cfg.delay
	}
	return &Hedger[T]{cfg: cfg}
}

// Do executes fn with hedging: if the first call doesn't complete within
// [WithDelay], a second copy is launched, and so on up to [WithMaxParallel].
// The first successful result wins; remaining in-flight calls are cancelled.
//
// The callback receives a [HedgeController] that exposes the attempt number
// (1 = original, 2+ = hedge) so the function can adapt its behavior for
// speculative copies (e.g. use a read replica, skip writes).
func (h *Hedger[T]) Do(ctx context.Context, fn func(ctx context.Context, hc HedgeController) (T, error)) (T, error) {
	fns := make([]func(ctx context.Context, hc HedgeController) (T, error), h.cfg.maxParallel)
	for i := range fns {
		fns[i] = fn
	}
	return h.DoMulti(ctx, fns)
}

// DoMulti runs each function as a separate hedge backend. The first
// successful result wins. len(fns) is capped at [WithMaxParallel].
func (h *Hedger[T]) DoMulti(ctx context.Context, fns []func(ctx context.Context, hc HedgeController) (T, error)) (T, error) {
	var zero T
	h.calls.Add(1)

	if len(fns) == 0 {
		h.failures.Add(1)
		return zero, errNoFunctions()
	}

	if len(fns) > h.cfg.maxParallel {
		fns = fns[:h.cfg.maxParallel]
	}

	if len(fns) == 1 {
		hc := &hedgeExecution{attempt: 1}
		v, err := fns[0](ctx, hc)
		if err != nil {
			h.failures.Add(1)
			return zero, err
		}
		h.wins.Add(1)
		return v, nil
	}

	hedgeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultCh := make(chan result[T], len(fns))

	go run(hedgeCtx, fns[0], 0, resultCh)

	launched := 1
	completed := 0
	var firstErr error

	delays := h.delays(len(fns))

	var timer *time.Timer
	var timerCh <-chan time.Time
	if len(delays) > 0 {
		timer = time.NewTimer(delays[0])
		timerCh = timer.C
		defer timer.Stop()
	}

	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			h.failures.Add(1)
			return zero, errCancelled(ctx.Err())

		case res := <-resultCh:
			completed++
			if res.err == nil {
				cancel()
				h.wins.Add(1)
				return res.value, nil
			}
			if firstErr == nil {
				firstErr = res.err
			}
			if completed >= launched && launched >= len(fns) {
				h.failures.Add(1)
				return zero, errAllFailed(firstErr)
			}

		case <-timerCh:
			if launched < len(fns) {
				h.hedges.Add(1)
				if h.cfg.onHedge != nil {
					attempt := launched + 1
					go func() {
						defer func() { recover() }()
						h.cfg.onHedge(attempt)
					}()
				}
				go run(hedgeCtx, fns[launched], launched, resultCh)
				launched++

				nextIdx := launched - 1
				if launched < len(fns) && nextIdx < len(delays) {
					elapsed := time.Since(startTime)
					next := delays[nextIdx] - elapsed
					if next < 0 {
						next = 0
					}
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(next)
				} else {
					timerCh = nil
				}
			}
		}
	}
}

// --- Stats ---

// Stats holds a snapshot of hedger counters.
type Stats struct {
	Calls    int64 `json:"calls"`
	Wins     int64 `json:"wins"`
	Failures int64 `json:"failures"`
	Hedges   int64 `json:"hedges"`
}

// Stats returns a snapshot of call/win/failure/hedge counters.
func (h *Hedger[T]) Stats() Stats {
	return Stats{
		Calls:    h.calls.Load(),
		Wins:     h.wins.Load(),
		Failures: h.failures.Load(),
		Hedges:   h.hedges.Load(),
	}
}

// ResetStats zeroes all counters.
func (h *Hedger[T]) ResetStats() {
	h.calls.Store(0)
	h.wins.Store(0)
	h.failures.Store(0)
	h.hedges.Store(0)
}

// --- Internal ---

type result[T any] struct {
	value T
	err   error
}

func run[T any](ctx context.Context, fn func(context.Context, HedgeController) (T, error), idx int, ch chan<- result[T]) {
	var v T
	var err error

	hc := &hedgeExecution{attempt: idx + 1}
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = errx.NewPanicError("hedgex.Do", r)
			}
		}()
		v, err = fn(ctx, hc)
	}()

	select {
	case <-ctx.Done():
		return
	default:
	}
	select {
	case ch <- result[T]{value: v, err: err}:
	case <-ctx.Done():
	}
}

// delays returns cumulative launch times for hedge requests.
// delays[i] is when request i+1 should launch (relative to start).
func (h *Hedger[T]) delays(count int) []time.Duration {
	if count <= 1 {
		return nil
	}
	ds := make([]time.Duration, count-1)

	hitIdx := -1
	for i := range ds {
		d := h.cfg.delay * time.Duration(i+1)
		if d >= h.cfg.maxDelay {
			hitIdx = i
			break
		}
	}

	if hitIdx == -1 {
		for i := range ds {
			ds[i] = h.cfg.delay * time.Duration(i+1)
		}
		return ds
	}

	for i := 0; i < hitIdx; i++ {
		ds[i] = h.cfg.delay * time.Duration(i+1)
	}

	spread := h.cfg.delay / 4
	if spread < time.Millisecond {
		spread = time.Millisecond
	}
	for i := hitIdx; i < len(ds); i++ {
		ds[i] = h.cfg.maxDelay + time.Duration(i-hitIdx)*spread
	}
	return ds
}
