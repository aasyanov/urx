package syncx

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/aasyanov/urx/panix"
)

// opGroup labels panics recovered while running a group task.
const opGroup = "syncx.Group"

// Group runs a set of goroutines as a unit, recovering panics via
// [panix.SafeVoid] and optionally bounding concurrency. It collects the first
// non-nil error (or recovered panic) returned by any task and cancels the
// derived context so siblings can observe the failure.
//
// A Group is the panic-safe analogue of golang.org/x/sync/errgroup: a task
// that panics is converted into a [*panix.PanicError] instead of crashing the
// process. It is safe for concurrent use; in particular [Group.Go] may be
// called from multiple goroutines.
//
// Create with [NewGroup]. A Group must not be reused after [Group.Wait].
type Group struct {
	// ctx is the derived context handed to every task. Storing it is
	// structurally necessary: tasks are launched asynchronously by Go/TryGo,
	// which do not receive a context, yet each must observe sibling
	// cancellation through the same derived context.
	ctx    context.Context
	cancel context.CancelFunc
	sem    chan struct{}
	wg     sync.WaitGroup
	once   sync.Once
	err    error

	started   atomic.Int64
	succeeded atomic.Int64
	failed    atomic.Int64
	panicked  atomic.Int64
}

// NewGroup creates a [Group] and a derived context. When any task launched
// via [Group.Go] returns a non-nil error or panics, the derived context is
// cancelled. The context is also cancelled once [Group.Wait] returns.
//
// Default configuration: unlimited concurrency. Use [WithLimit] to bound it.
func NewGroup(ctx context.Context, opts ...GroupOption) (*Group, context.Context) {
	cfg := newGroupConfig(opts)

	ctx, cancel := context.WithCancel(ctx)
	g := &Group{ctx: ctx, cancel: cancel}
	if cfg.limit > 0 {
		g.sem = make(chan struct{}, cfg.limit)
	}
	return g, ctx
}

// Go runs fn in a new goroutine under panic recovery. If a concurrency limit
// is configured, Go blocks until a slot is available. A nil fn is ignored.
//
// The ctx passed to fn is the group's derived context: it is cancelled as
// soon as any sibling task fails, allowing well-behaved tasks to abort early.
func (g *Group) Go(fn func(ctx context.Context) error) {
	if fn == nil {
		return
	}
	if g.sem != nil {
		g.sem <- struct{}{}
	}
	g.launch(fn)
}

// TryGo runs fn in a new goroutine only if a concurrency slot is immediately
// available, returning true if the task was started. With no configured limit
// it always starts the task and returns true. A nil fn is ignored and reports
// false.
func (g *Group) TryGo(fn func(ctx context.Context) error) bool {
	if fn == nil {
		return false
	}
	if g.sem != nil {
		select {
		case g.sem <- struct{}{}:
		default:
			return false
		}
	}
	g.launch(fn)
	return true
}

// launch starts fn in a goroutine, assuming any semaphore slot has already
// been acquired by the caller.
func (g *Group) launch(fn func(ctx context.Context) error) {
	g.wg.Add(1)
	g.started.Add(1)

	go func() {
		defer g.wg.Done()
		if g.sem != nil {
			defer func() { <-g.sem }()
		}

		err := panix.SafeVoid(opGroup, func() error {
			return fn(g.ctx)
		})
		switch {
		case err == nil:
			g.succeeded.Add(1)
			return
		case isPanic(err):
			g.panicked.Add(1)
			g.failed.Add(1)
		default:
			g.failed.Add(1)
		}

		g.once.Do(func() {
			g.err = err
			g.cancel()
		})
	}()
}

// Wait blocks until all goroutines launched via [Group.Go] or [Group.TryGo]
// have completed, then cancels the derived context. It returns the first
// non-nil error (or [*panix.PanicError]) reported by any task, or nil if all
// tasks succeeded.
func (g *Group) Wait() error {
	g.wg.Wait()
	g.cancel()
	return g.err
}

// Stats returns a point-in-time snapshot of the group's task counters. It is
// safe to call concurrently with task execution and with [Group.Wait].
func (g *Group) Stats() GroupStats {
	return GroupStats{
		Started:   g.started.Load(),
		Succeeded: g.succeeded.Load(),
		Failed:    g.failed.Load(),
		Panicked:  g.panicked.Load(),
	}
}
