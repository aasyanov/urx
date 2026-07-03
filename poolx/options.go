package poolx

import "time"

const (
	// DefaultWorkers is the worker goroutine count when [WithWorkers] is not
	// supplied.
	DefaultWorkers = 4

	// DefaultQueueSize is the task queue capacity when [WithQueueSize] is not
	// supplied.
	DefaultQueueSize = 64

	// DefaultBatchSize is the flush threshold when [WithBatchSize] is not
	// supplied.
	DefaultBatchSize = 100

	// DefaultFlushInterval is the periodic flush interval when
	// [WithFlushInterval] is not supplied.
	DefaultFlushInterval = time.Second
)

// WorkerOption configures a [WorkerPool] created with [NewWorkerPool].
type WorkerOption func(*workerConfig)

// workerConfig holds resolved worker-pool parameters.
type workerConfig struct {
	workers   int
	queueSize int
	op        string
}

func newWorkerConfig(opts []WorkerOption) workerConfig {
	cfg := workerConfig{
		workers:   DefaultWorkers,
		queueSize: DefaultQueueSize,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func (c workerConfig) opOrDefault() string {
	if c.op != "" {
		return c.op
	}
	return opWorker
}

// WithWorkers sets the number of worker goroutines.
// Default: [DefaultWorkers]. Non-positive values are ignored.
func WithWorkers(n int) WorkerOption {
	return func(c *workerConfig) {
		if n > 0 {
			c.workers = n
		}
	}
}

// WithQueueSize sets the task queue capacity.
// Default: [DefaultQueueSize]. Non-positive values are ignored.
func WithQueueSize(n int) WorkerOption {
	return func(c *workerConfig) {
		if n > 0 {
			c.queueSize = n
		}
	}
}

// WithWorkerOp sets the logical operation name attached to panic reports from
// worker tasks (e.g. "api.ingest", "worker.process"). Default: [opWorker].
// An empty name is ignored.
func WithWorkerOp(op string) WorkerOption {
	return func(c *workerConfig) {
		if op != "" {
			c.op = op
		}
	}
}

// BatchOption configures a [Batch] created with [NewBatch].
type BatchOption func(*batchConfig)

// batchConfig holds resolved batch-processor parameters.
type batchConfig struct {
	batchSize     int
	flushInterval time.Duration
	onError       func(error)
	op            string
}

func newBatchConfig(opts []BatchOption) batchConfig {
	cfg := batchConfig{
		batchSize:     DefaultBatchSize,
		flushInterval: DefaultFlushInterval,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func (c batchConfig) opOrDefault() string {
	if c.op != "" {
		return c.op
	}
	return opBatchFlush
}

// WithBatchSize sets the maximum number of items buffered before an automatic
// flush is triggered. Default: [DefaultBatchSize]. Non-positive values are
// ignored.
func WithBatchSize(n int) BatchOption {
	return func(c *batchConfig) {
		if n > 0 {
			c.batchSize = n
		}
	}
}

// WithFlushInterval sets the periodic flush interval. Default:
// [DefaultFlushInterval]. Non-positive values are ignored.
func WithFlushInterval(d time.Duration) BatchOption {
	return func(c *batchConfig) {
		if d > 0 {
			c.flushInterval = d
		}
	}
}

// WithErrorHandler registers a callback invoked with the error from any flush
// that fails — including periodic ticker flushes whose errors are otherwise
// unobservable. Default: nil. The handler must not block.
func WithErrorHandler(fn func(error)) BatchOption {
	return func(c *batchConfig) { c.onError = fn }
}

// WithBatchOp sets the logical operation name attached to panic reports from
// flush callbacks (e.g. "db.batch_insert"). Default: [opBatchFlush]. An empty
// name is ignored.
func WithBatchOp(op string) BatchOption {
	return func(c *batchConfig) {
		if op != "" {
			c.op = op
		}
	}
}

// ObjectOption configures an [ObjectPool] created with [NewObjectPool].
type ObjectOption[T any] func(*objectConfig[T])

// objectConfig holds resolved object-pool parameters.
type objectConfig[T any] struct {
	reset func(T)
}

func newObjectConfig[T any](opts []ObjectOption[T]) objectConfig[T] {
	var cfg objectConfig[T]
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithReset registers a hook invoked on every [ObjectPool.Put] before the
// object is returned to the pool. Use it to clear mutable state (e.g.
// buf.Reset()) so the next [ObjectPool.Get] sees a clean instance.
// Default: nil.
func WithReset[T any](fn func(T)) ObjectOption[T] {
	return func(c *objectConfig[T]) { c.reset = fn }
}
