package poolx

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/aasyanov/urx/pkg/panix"
)

// --- Worker pool config ---

// Worker pool default values.
const (
	defaultWorkers   = 4
	defaultQueueSize = 64
)

// WorkerOption configures [NewWorkerPool].
type WorkerOption func(*workerConfig)

// workerConfig holds worker pool parameters.
type workerConfig struct {
	workers   int
	queueSize int
}

// defaultWorkerConfig returns sensible pool defaults (4 workers, 64-slot queue).
func defaultWorkerConfig() workerConfig {
	return workerConfig{workers: defaultWorkers, queueSize: defaultQueueSize}
}

// WithWorkers sets the number of worker goroutines.
func WithWorkers(n int) WorkerOption {
	return func(c *workerConfig) {
		if n > 0 {
			c.workers = n
		}
	}
}

// WithQueueSize sets the task queue capacity.
func WithQueueSize(n int) WorkerOption {
	return func(c *workerConfig) {
		if n > 0 {
			c.queueSize = n
		}
	}
}

// --- WorkerPool ---

// WorkerPool manages a fixed set of goroutines that process submitted tasks.
type WorkerPool struct {
	cfg    workerConfig
	tasks  chan func()
	wg     sync.WaitGroup
	closed atomic.Bool

	submitted atomic.Uint64
	completed atomic.Uint64
	failed    atomic.Uint64
	panics    atomic.Uint64
}

// WorkerStats holds point-in-time counters for a [WorkerPool].
type WorkerStats struct {
	Workers   int    `json:"workers"`
	QueueSize int    `json:"queue_size"`
	Pending   int    `json:"pending"`
	Submitted uint64 `json:"submitted"`
	Completed uint64 `json:"completed"`
	Failed    uint64 `json:"failed"`
	Panics    uint64 `json:"panics"`
}

// NewWorkerPool creates and starts a [WorkerPool].
func NewWorkerPool(opts ...WorkerOption) *WorkerPool {
	cfg := defaultWorkerConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	wp := &WorkerPool{
		cfg:   cfg,
		tasks: make(chan func(), cfg.queueSize),
	}

	wp.wg.Add(cfg.workers)
	for range cfg.workers {
		go wp.worker()
	}
	return wp
}

// worker is the long-running goroutine that processes tasks from the queue.
func (wp *WorkerPool) worker() {
	defer wp.wg.Done()
	for fn := range wp.tasks {
		fn()
	}
}

// Submit enqueues a task for execution. It blocks if the queue is full,
// but respects context cancellation while waiting. Returns an error if
// the pool is closed or the context is cancelled before a queue slot opens.
func (wp *WorkerPool) Submit(ctx context.Context, fn func(ctx context.Context) error) error {
	if wp.closed.Load() {
		return errClosed("worker pool is closed")
	}

	task := func() {
		_, err := panix.Safe[struct{}](ctx, "poolx.WorkerPool", func(ctx context.Context) (struct{}, error) {
			return struct{}{}, fn(ctx)
		})
		if err != nil {
			wp.failed.Add(1)
			if xe, ok := asErrx(err); ok && xe.IsPanic() {
				wp.panics.Add(1)
			}
		} else {
			wp.completed.Add(1)
		}
	}

	wp.submitted.Add(1)
	select {
	case wp.tasks <- task:
		return nil
	case <-ctx.Done():
		wp.submitted.Add(^uint64(0))
		return errCtxDone(ctx.Err())
	}
}

// TrySubmit attempts to enqueue a task without blocking. Returns
// [CodeQueueFull] if the queue is at capacity.
func (wp *WorkerPool) TrySubmit(ctx context.Context, fn func(ctx context.Context) error) error {
	if wp.closed.Load() {
		return errClosed("worker pool is closed")
	}

	wp.submitted.Add(1)
	task := func() {
		_, err := panix.Safe[struct{}](ctx, "poolx.WorkerPool", func(ctx context.Context) (struct{}, error) {
			return struct{}{}, fn(ctx)
		})
		if err != nil {
			wp.failed.Add(1)
			if xe, ok := asErrx(err); ok && xe.IsPanic() {
				wp.panics.Add(1)
			}
		} else {
			wp.completed.Add(1)
		}
	}

	select {
	case wp.tasks <- task:
		return nil
	default:
		wp.submitted.Add(^uint64(0))
		return errQueueFull()
	}
}

// Stats returns point-in-time counters.
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

// Close shuts down the pool. It stops accepting new tasks and waits for
// in-flight tasks to complete.
func (wp *WorkerPool) Close() {
	if wp.closed.CompareAndSwap(false, true) {
		close(wp.tasks)
		wp.wg.Wait()
	}
}

// IsClosed reports whether the pool has been closed.
func (wp *WorkerPool) IsClosed() bool {
	return wp.closed.Load()
}
