// Package ratex provides a thread-safe token-bucket rate limiter for
// industrial Go services.
//
// A [Limiter] controls how many operations per unit of time are allowed
// through. [Limiter.Allow] is the non-blocking check; [Limiter.Wait] blocks
// until a token is available or the context is cancelled.
//
//	rl := ratex.New(
//	    ratex.WithRate(100),
//	    ratex.WithBurst(20),
//	)
//
//	if !rl.Allow() {
//	    // rejected
//	}
//
//	// or block until allowed:
//	if err := rl.Wait(ctx); err != nil {
//	    // context cancelled or timed out
//	}
package ratex

import (
	"context"
	"math"
	"sync"
	"time"
)

// --- Configuration ---

// config holds rate limiter parameters.
type config struct {
	rate  float64
	burst int
}

// defaultConfig returns sensible limiter defaults (10 req/s, burst 20).
func defaultConfig() config {
	return config{
		rate:  10,
		burst: 20,
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option func(*config)

// WithRate sets the sustained rate in requests per second. Values <= 0 are
// ignored.
func WithRate(r float64) Option {
	return func(c *config) {
		if r > 0 {
			c.rate = r
		}
	}
}

// WithBurst sets the maximum number of tokens the bucket can hold, allowing
// short bursts above the sustained rate. Values <= 0 are ignored.
func WithBurst(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.burst = n
		}
	}
}

// --- Limiter ---

// Limiter is a thread-safe token-bucket rate limiter. Create one with [New],
// check with [Limiter.Allow] or [Limiter.AllowN], block with [Limiter.Wait],
// and reset with [Limiter.Reset].
type Limiter struct {
	cfg        config
	mu         sync.Mutex
	tokens     float64
	lastUpdate time.Time
	allowed    uint64
	limited    uint64
}

// New creates a [Limiter] with the given options applied on top of
// sensible defaults (10 req/s, burst 20).
func New(opts ...Option) *Limiter {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.rate <= 0 {
		cfg.rate = 1
	}
	if cfg.burst <= 0 {
		cfg.burst = 1
	}
	return &Limiter{
		cfg:        cfg,
		tokens:     float64(cfg.burst),
		lastUpdate: time.Now(),
	}
}

// --- Token management ---

// refill adds tokens accumulated since the last update. Must be called with
// mu held.
func (l *Limiter) refill() {
	now := time.Now()
	elapsed := now.Sub(l.lastUpdate)
	add := elapsed.Seconds() * l.cfg.rate
	if add > 0 {
		l.tokens = math.Min(float64(l.cfg.burst), l.tokens+add)
		l.lastUpdate = now
	}
}

// --- Checking ---

// Allow reports whether one request is allowed right now. It consumes one
// token on success.
func (l *Limiter) Allow() bool {
	return l.AllowN(1)
}

// AllowN reports whether n requests are allowed right now. It consumes n
// tokens on success; on failure no tokens are consumed.
// Panics if n < 1.
func (l *Limiter) AllowN(n int) bool {
	if n < 1 {
		panic("ratex: AllowN requires n >= 1")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()

	need := float64(n)
	if l.tokens >= need {
		l.tokens -= need
		l.allowed++
		return true
	}
	l.limited++
	return false
}

// --- Blocking ---

// Wait blocks until one token is available or ctx is done.
func (l *Limiter) Wait(ctx context.Context) error {
	return l.WaitN(ctx, 1)
}

// WaitN blocks until n tokens are available or ctx is done.
// Panics if n < 1.
func (l *Limiter) WaitN(ctx context.Context, n int) error {
	if n < 1 {
		panic("ratex: WaitN requires n >= 1")
	}
	for {
		if l.AllowN(n) {
			return nil
		}

		delay := l.delay(n)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errCancelled(ctx.Err())
		case <-timer.C:
			timer.Stop()
		}
	}
}

// delay returns the estimated time until n tokens will be available. Must not
// hold mu.
func (l *Limiter) delay(n int) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()
	deficit := float64(n) - l.tokens
	if deficit <= 0 {
		return 0
	}
	d := time.Duration(deficit / l.cfg.rate * float64(time.Second))
	if d < time.Millisecond {
		d = time.Millisecond
	}
	return d
}

// --- Accessors ---

// Tokens returns the current number of available tokens (fractional).
func (l *Limiter) Tokens() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refill()
	return l.tokens
}

// --- Statistics ---

// Stats holds a point-in-time snapshot of rate limiter counters.
type Stats struct {
	Rate    float64 `json:"rate"`
	Burst   int     `json:"burst"`
	Tokens  float64 `json:"tokens"`
	Allowed uint64  `json:"allowed"`
	Limited uint64  `json:"limited"`
}

// Stats returns a snapshot of rate limiter statistics.
func (l *Limiter) Stats() Stats {
	l.mu.Lock()
	l.refill()
	s := Stats{
		Rate:    l.cfg.rate,
		Burst:   l.cfg.burst,
		Tokens:  l.tokens,
		Allowed: l.allowed,
		Limited: l.limited,
	}
	l.mu.Unlock()
	return s
}

// ResetStats zeroes all counters.
func (l *Limiter) ResetStats() {
	l.mu.Lock()
	l.allowed = 0
	l.limited = 0
	l.mu.Unlock()
}

// --- Lifecycle ---

// Reset restores the bucket to its initial full state.
func (l *Limiter) Reset() {
	l.mu.Lock()
	l.tokens = float64(l.cfg.burst)
	l.lastUpdate = time.Now()
	l.mu.Unlock()
}
