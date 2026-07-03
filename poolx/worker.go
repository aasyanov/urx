package poolx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/aasyanov/urx/panix"
)

// opWorker labels panics recovered while running a worker task when
// [WithWorkerOp] is not supplied.
const opWorker = "poolx.WorkerPool"

const componentWorkerPool = "worker pool"

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
// Default configuration: [DefaultWorkers] workers, [DefaultQueueSize]-slot queue.
func NewWorkerPool(opts ...WorkerOption) *WorkerPool {
	cfg := newWorkerConfig(opts)

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

func (wp *WorkerPool) worker() {
	defer wp.wg.Done()
	for {
		select {
		case fn := <-wp.tasks:
			fn()
		case <-wp.done:
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

func (wp *WorkerPool) rejectIfClosed() error {
	if wp.closed.Load() {
		return errClosed(componentWorkerPool)
	}
	select {
	case <-wp.done:
		return errClosed(componentWorkerPool)
	default:
		return nil
	}
}

func (wp *WorkerPool) validateSubmit(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return ErrNilFunc
	}
	if err := ctx.Err(); err != nil {
		return errCancelled(err)
	}
	return wp.rejectIfClosed()
}

func (wp *WorkerPool) runTask(ctx context.Context, fn func(context.Context) error) error {
	err := panix.SafeVoid(wp.cfg.opOrDefault(), func() error {
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
// queue is full, releasing when a slot opens, ctx is cancelled, or the pool
// is closed.
//
// Returns [ErrNilFunc], [ErrClosed], or [ErrCancelled]. The task's own error
// or panic is recorded in [WorkerPool.Stats], not returned by Submit.
func (wp *WorkerPool) Submit(ctx context.Context, fn func(context.Context) error) error {
	if err := wp.validateSubmit(ctx, fn); err != nil {
		return err
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

// TrySubmit enqueues a task without blocking. Returns [ErrQueueFull] when the
// queue is at capacity, [ErrNilFunc] when fn is nil, [ErrClosed] when the
// pool is closed, and [ErrCancelled] when ctx is already cancelled.
func (wp *WorkerPool) TrySubmit(ctx context.Context, fn func(context.Context) error) error {
	if err := wp.validateSubmit(ctx, fn); err != nil {
		return err
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
// slot opens, ctx is cancelled, or the pool is closed before enqueue.
//
// After the task is queued, SubmitWait waits for the result until ctx is
// cancelled. [WorkerPool.Close] drains every accepted task before returning,
// so a queued task always completes and its result is delivered unless the
// caller's ctx is cancelled first.
//
// Returns [ErrNilFunc], [ErrClosed], or [ErrCancelled]. A panicking task
// yields [*panix.PanicError]; a non-panic error is returned verbatim.
func (wp *WorkerPool) SubmitWait(ctx context.Context, fn func(context.Context) error) error {
	if err := wp.validateSubmit(ctx, fn); err != nil {
		return err
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

	select {
	case err := <-resultCh:
		return err
	case <-ctx.Done():
		return errCancelled(ctx.Err())
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
func (wp *WorkerPool) ResetStats() {
	wp.submitted.Store(0)
	wp.completed.Store(0)
	wp.failed.Store(0)
	wp.panics.Store(0)
}

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
// tasks have completed. It is idempotent.
func (wp *WorkerPool) Close() error {
	wp.closeOnce.Do(func() {
		wp.closed.Store(true)
		close(wp.done)
		wp.wg.Wait()
		wp.drainOrphanedTasks()
	})
	return nil
}

// IsClosed reports whether the pool has been closed.
func (wp *WorkerPool) IsClosed() bool {
	return wp.closed.Load()
}

func isPanic(err error) bool {
	var pe *panix.PanicError
	return errors.As(err, &pe)
}
