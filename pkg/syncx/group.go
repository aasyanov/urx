package syncx

import (
	"context"
	"sync"

	"github.com/aasyanov/urx/pkg/panix"
)

// --- Group options ---

// GroupOption configures [NewGroup] behavior.
type GroupOption func(*groupConfig)

// groupConfig holds [Group] options.
type groupConfig struct {
	limit int
}

// WithLimit sets the maximum number of goroutines that may run concurrently.
// Values <= 0 mean unlimited.
func WithLimit(n int) GroupOption {
	return func(c *groupConfig) {
		if n > 0 {
			c.limit = n
		}
	}
}

// --- Group ---

// Group is an error-group with [panix.Safe] panic recovery and optional
// concurrency limiting. It collects the first non-nil error returned by
// any goroutine and cancels the derived context.
type Group struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	sem    chan struct{}
	once   sync.Once
	err    error
}

// NewGroup creates a [Group] and a derived context. When any goroutine
// launched via [Group.Go] returns a non-nil error (or panics), the derived
// context is cancelled.
func NewGroup(ctx context.Context, opts ...GroupOption) (*Group, context.Context) {
	cfg := groupConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	ctx, cancel := context.WithCancel(ctx)
	g := &Group{ctx: ctx, cancel: cancel}
	if cfg.limit > 0 {
		g.sem = make(chan struct{}, cfg.limit)
	}
	return g, ctx
}

// Go launches fn in a new goroutine, wrapping it with [panix.Safe] for
// panic recovery. If a concurrency limit was set, Go blocks until a slot
// is available.
func (g *Group) Go(fn func(ctx context.Context) error) {
	g.wg.Add(1)

	if g.sem != nil {
		g.sem <- struct{}{}
	}

	go func() {
		defer g.wg.Done()
		if g.sem != nil {
			defer func() { <-g.sem }()
		}

		if _, err := panix.Safe[struct{}](g.ctx, "syncx.Group", func(ctx context.Context) (struct{}, error) {
			return struct{}{}, fn(ctx)
		}); err != nil {
			g.once.Do(func() {
				g.err = err
				g.cancel()
			})
		}
	}()
}

// Wait blocks until all goroutines launched via [Group.Go] have completed.
// It returns the first non-nil error (if any).
func (g *Group) Wait() error {
	g.wg.Wait()
	g.cancel()
	return g.err
}
