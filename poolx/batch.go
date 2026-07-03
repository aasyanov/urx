package poolx

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/panix"
)

const (
	// defaultBatchSize is the flush threshold when [WithBatchSize] is not
	// supplied.
	defaultBatchSize = 100

	// defaultFlushInterval is the periodic flush interval when
	// [WithFlushInterval] is not supplied.
	defaultFlushInterval = time.Second
)

// opBatchFlush labels panics recovered while running a batch flush.
const opBatchFlush = "poolx.Batch.Flush"

// componentBatch names the Batch in [ErrClosed] messages.
const componentBatch = "batch processor"

// BatchOption configures a [Batch] created with [NewBatch].
type BatchOption func(*batchConfig)

type batchConfig struct {
	batchSize     int
	flushInterval time.Duration
	onError       func(error)
}

func defaultBatchConfig() batchConfig {
	return batchConfig{
		batchSize:     defaultBatchSize,
		flushInterval: defaultFlushInterval,
	}
}

// WithBatchSize sets the maximum number of items buffered before an automatic
// flush is triggered. Default: 100. Non-positive values are ignored.
func WithBatchSize(n int) BatchOption {
	return func(c *batchConfig) {
		if n > 0 {
			c.batchSize = n
		}
	}
}

// WithFlushInterval sets the periodic flush interval. Default: 1s.
// Non-positive values are ignored and the default is kept.
func WithFlushInterval(d time.Duration) BatchOption {
	return func(c *batchConfig) {
		if d > 0 {
			c.flushInterval = d
		}
	}
}

// WithErrorHandler registers a callback invoked with the error from any flush
// that fails — including the periodic ticker flushes whose errors are
// otherwise unobservable. Default: nil (ticker flush errors are counted in
// [BatchStats] but otherwise discarded). The handler must not block.
func WithErrorHandler(fn func(error)) BatchOption {
	return func(c *batchConfig) { c.onError = fn }
}

// Batch buffers items and flushes them in batches through a user-supplied,
// context-aware flush function. A flush occurs when the buffer reaches the
// configured size (see [WithBatchSize]) or the configured interval elapses
// (see [WithFlushInterval]), whichever comes first. It is safe for concurrent
// use.
//
// Create with [NewBatch] and release resources with [Batch.Close], which
// flushes any remaining buffered items.
type Batch[T any] struct {
	flush func(context.Context, []T) error
	cfg   batchConfig

	ctx    context.Context
	cancel context.CancelFunc

	mu  sync.Mutex
	buf []T

	closeOnce sync.Once
	closed    atomic.Bool
	done      chan struct{}

	flushed atomic.Uint64
	items   atomic.Uint64
	errors  atomic.Uint64
}

// NewBatch creates and starts a [Batch] that invokes flush when the buffer is
// full or the flush interval elapses. The flush function receives a context
// that is cancelled when [Batch.Close] is called, so a long-running flush can
// observe shutdown. Panics if flush is nil.
//
// Default configuration: batch size 100, flush interval 1s, no error handler.
func NewBatch[T any](flush func(ctx context.Context, items []T) error, opts ...BatchOption) *Batch[T] {
	if flush == nil {
		panic("poolx: NewBatch flush function is nil")
	}
	cfg := defaultBatchConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	b := &Batch[T]{
		flush:  flush,
		cfg:    cfg,
		ctx:    ctx,
		cancel: cancel,
		buf:    make([]T, 0, cfg.batchSize),
		done:   make(chan struct{}),
	}

	go b.ticker()
	return b
}

// ticker runs the periodic flush loop until the batch is closed.
func (b *Batch[T]) ticker() {
	t := time.NewTicker(b.cfg.flushInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := b.Flush(b.ctx); err != nil && b.cfg.onError != nil {
				b.cfg.onError(err)
			}
		case <-b.done:
			return
		}
	}
}

// Add appends an item to the buffer, triggering a flush if the buffer reaches
// the configured batch size. Returns [ErrClosed] if the batch is closed, or
// the flush error (joined with [ErrFlushFailed]) when a size-triggered flush
// fails.
func (b *Batch[T]) Add(item T) error {
	if b.closed.Load() {
		return errClosed(componentBatch)
	}

	b.mu.Lock()
	b.buf = append(b.buf, item)
	shouldFlush := len(b.buf) >= b.cfg.batchSize
	b.mu.Unlock()

	if shouldFlush {
		return b.Flush(b.ctx)
	}
	return nil
}

// Flush flushes the current buffer using ctx. It is a no-op when the buffer is
// empty. Returns the flush error joined with [ErrFlushFailed] on failure (a
// panicking flush is recovered and reported the same way). Safe for concurrent
// use.
func (b *Batch[T]) Flush(ctx context.Context) error {
	b.mu.Lock()
	if len(b.buf) == 0 {
		b.mu.Unlock()
		return nil
	}
	batch := b.buf
	b.buf = make([]T, 0, b.cfg.batchSize)
	b.mu.Unlock()

	b.items.Add(uint64(len(batch)))
	b.flushed.Add(1)

	err := panix.SafeVoid(opBatchFlush, func() error {
		return b.flush(ctx, batch)
	})
	if err != nil {
		b.errors.Add(1)
		return errFlushFailed(err)
	}
	return nil
}

// Stats returns a point-in-time snapshot of the batch counters.
func (b *Batch[T]) Stats() BatchStats {
	b.mu.Lock()
	buffered := len(b.buf)
	b.mu.Unlock()
	return BatchStats{
		BatchSize:     b.cfg.batchSize,
		FlushInterval: b.cfg.flushInterval.String(),
		Buffered:      buffered,
		Flushed:       b.flushed.Load(),
		Items:         b.items.Load(),
		Errors:        b.errors.Load(),
	}
}

// ResetStats zeroes the flushed, items, and errors counters.
func (b *Batch[T]) ResetStats() {
	b.flushed.Store(0)
	b.items.Store(0)
	b.errors.Store(0)
}

// Close stops the periodic ticker, flushes any remaining buffered items, and
// cancels the flush context for subsequent operations. It is idempotent:
// subsequent calls are no-ops returning nil. The final flush uses a background
// context so shutdown is not aborted by the cancelled lifecycle context.
func (b *Batch[T]) Close() error {
	var err error
	b.closeOnce.Do(func() {
		b.closed.Store(true)
		close(b.done)
		err = b.Flush(context.Background())
		b.cancel()
	})
	return err
}

// IsClosed reports whether the batch processor has been closed.
func (b *Batch[T]) IsClosed() bool {
	return b.closed.Load()
}
