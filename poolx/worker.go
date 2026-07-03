package poolx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/aasyanov/urx/panix"
)

const (
	// defaultWorkers is the worker goroutine count when [WithWorkers] is not
	// supplied.
	defaultWorkers = 4

	// defaultQueueSize is the task queue capacity when [WithQueueSize] is not
	// supplied.
	defaultQueueSize = 64
)

// opWorker labels panics recovered while running a worker task.
const opWorker = "poolx.WorkerPool"

// componentWorkerPool names the WorkerPool in [ErrClosed] messages.
const componentWorkerPool = "worker pool"

// WorkerOption configures a [WorkerPool] created with [NewWorkerPool].
type WorkerOption func(*workerConfig)

type workerConfig struct {
	workers   int
	queueSize int
}

func defaultWorkerConfig() workerConfig {
	return workerConfig{workers: defaultWorkers, queueSize: defaultQueueSize}
}

// WithWorkers sets the number of worker goroutines.
// Default: 4. Non-positive values are ignored and the default is kept.
func WithWorkers(n int) WorkerOption {
	return func(c *workerConfig) {
		if n > 0 {
			c.workers = n
		}
	}
}

// WithQueueSize sets the task queue capacity.
// Default: 64. Non-positive values are ignored and the default is kept.
func WithQueueSize(n int) WorkerOption {
	return func(c *workerConfig) {
		if n > 0 {
			c.queueSize = n
		}
	}
}

// WorkerPool manages a fixed set of goroutines that process submitted tasks
// from a bounded queue. Tasks run under panic recovery so a panicking task
// cannot crash a worker. It is safe for concurrent use.
//
// Create with [NewWorkerPool] and release resources with [WorkerPool.Close].
type WorkerPool struct {
	cfg   workerConfig
	tasks chan func()
	done  chan struct{}
	wg    sync.WaitGroup

	closeOnce sync.Once
	closed    atomic.Bool

	submitted atomic.Uint64
	completed atomic.Uint64
	failed    atomic.Uint64
	panics    atomic.Uint64
}

// NewWorkerPool creates and starts a [WorkerPool].
// Default configuration: 4 workers, 64-slot queue.
func NewWorkerPool(opts ...WorkerOption) *WorkerPool {
	cfg := defaultWorkerConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	wp := &WorkerPool{
		cfg:   cfg,
		tasks: make(chan func(), cfg.queueSize),
		done:  make(chan struct{}),
	}

	wp.wg.Add(cfg.workers)
	for range cfg.workers {
		go wp.worker()
	}
	return wp
}

// worker processes tasks until the pool is closed and the queue is drained.
// It prioritizes draining queued work: on shutdown it keeps running tasks
// until the queue is empty, then exits.
func (wp *WorkerPool) worker() {
	defer wp.wg.Done()
	for {
		select {
		case fn := <-wp.tasks:
			fn()
		case <-wp.done:
			// Drain any tasks enqueued before close, then exit.
			for {
				select {
				case fn := <-wp.tasks:
					fn()
				default:
					return
				}
			}
		}
	}
}

// runTask executes fn under panic recovery and records the outcome in the
// pool counters: a panic increments both failed and panics; a non-nil error
// increments failed; success increments completed.
func (wp *WorkerPool) runTask(ctx context.Context, fn func(ctx context.Context) error) error {
	err := panix.SafeVoid(opWorker, func() error {
		return fn(ctx)
	})
	switch {
	case err == nil:
		wp.completed.Add(1)
	case isPanic(err):
		wp.panics.Add(1)
		wp.failed.Add(1)
	default:
		wp.failed.Add(1)
	}
	return err
}

// Submit enqueues a task for asynchronous execution. It blocks while the
// queue is full, releasing as soon as a slot opens, ctx is cancelled, or the
// pool is closed.
//
// Returns [ErrClosed] if the pool is closed and [ErrCancelled] (joined with
// the context cause) if ctx is cancelled before a slot becomes available.
// The task's own error or panic is recorded in [WorkerPool.Stats], not
// returned by Submit.
func (wp *WorkerPool) Submit(ctx context.Context, fn func(ctx context.Context) error) error {
	if wp.closed.Load() {
		return errClosed(componentWorkerPool)
	}

	task := func() { _ = wp.runTask(ctx, fn) }

	select {
	case wp.tasks <- task:
		wp.submitted.Add(1)
		return nil
	case <-wp.done:
		return errClosed(componentWorkerPool)
	case <-ctx.Done():
		return errCancelled(ctx.Err())
	}
}

// TrySubmit enqueues a task without blocking. Returns [ErrQueueFull] if the
// queue is at capacity, [ErrClosed] if the pool is closed. As with [Submit],
// the task's own error or panic is recorded in [WorkerPool.Stats].
func (wp *WorkerPool) TrySubmit(ctx context.Context, fn func(ctx context.Context) error) error {
	if wp.closed.Load() {
		return errClosed(componentWorkerPool)
	}

	task := func() { _ = wp.runTask(ctx, fn) }

	select {
	case wp.tasks <- task:
		wp.submitted.Add(1)
		return nil
	case <-wp.done:
		return errClosed(componentWorkerPool)
	default:
		return ErrQueueFull
	}
}

// SubmitWait enqueues a task and blocks until it has run, returning the
// task's own result. It blocks while the queue is full, releasing when a
// slot opens, ctx is cancelled, or the pool is closed.
//
// Returns [ErrClosed] if the pool is closed and [ErrCancelled] if ctx is
// cancelled before the task is enqueued. If the task panics, the returned
// error is a [*panix.PanicError]; otherwise the task's error is returned
// verbatim. The outcome is also recorded in [WorkerPool.Stats].
func (wp *WorkerPool) SubmitWait(ctx context.Context, fn func(ctx context.Context) error) error {
	if wp.closed.Load() {
		return errClosed(componentWorkerPool)
	}

	resultCh := make(chan error, 1)
	task := func() { resultCh <- wp.runTask(ctx, fn) }

	select {
	case wp.tasks <- task:
		wp.submitted.Add(1)
	case <-wp.done:
		return errClosed(componentWorkerPool)
	case <-ctx.Done():
		return errCancelled(ctx.Err())
	}

	// The task is queued. Wait for its result, but do not block forever if
	// the pool shuts down before a worker picks it up. When shutdown wins the
	// select race against a completed result, drain the buffered result first.
	select {
	case err := <-resultCh:
		return err
	case <-ctx.Done():
		return errCancelled(ctx.Err())
	case <-wp.done:
		select {
		case err := <-resultCh:
			return err
		default:
			return errClosed(componentWorkerPool)
		}
	}
}

// Stats returns a point-in-time snapshot of the pool counters.
func (wp *WorkerPool) Stats() WorkerStats {
	return WorkerStats{
		Workers:   wp.cfg.workers,
		QueueSize: wp.cfg.queueSize,
		Pending:   len(wp.tasks),
		Submitted: wp.submitted.Load(),
		Completed: wp.completed.Load(),
		Failed:    wp.failed.Load(),
		Panics:    wp.panics.Load(),
	}
}

// ResetStats zeroes the submitted, completed, failed, and panics counters.
// It does not affect the live worker or queue configuration.
func (wp *WorkerPool) ResetStats() {
	wp.submitted.Store(0)
	wp.completed.Store(0)
	wp.failed.Store(0)
	wp.panics.Store(0)
}

// drainOrphanedTasks runs tasks that were enqueued after workers exited but
// before Close finished. The task channel is never closed, so this final drain
// guarantees every accepted task executes exactly once.
func (wp *WorkerPool) drainOrphanedTasks() {
	for {
		select {
		case fn := <-wp.tasks:
			fn()
		default:
			return
		}
	}
}

// Close stops accepting new tasks and blocks until all in-flight and queued
// tasks have completed. It is idempotent: subsequent calls are no-ops.
func (wp *WorkerPool) Close() {
	wp.closeOnce.Do(func() {
		wp.closed.Store(true)
		close(wp.done)
		wp.wg.Wait()
		wp.drainOrphanedTasks()
	})
}

// IsClosed reports whether the pool has been closed.
func (wp *WorkerPool) IsClosed() bool {
	return wp.closed.Load()
}

// isPanic reports whether err originates from a recovered panic.
func isPanic(err error) bool {
	var pe *panix.PanicError
	return errors.As(err, &pe)
}
