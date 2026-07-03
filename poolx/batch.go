package poolx

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/panix"
)

// opBatchFlush labels panics recovered while running a batch flush when
// [WithBatchOp] is not supplied.
const opBatchFlush = "poolx.Batch.Flush"

const componentBatch = "batch processor"

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
// full or the flush interval elapses. Automatic flushes pass a lifecycle
// context that cancels when [Batch.Close] is called. Returns [ErrNilFlush]
// when flush is nil.
//
// Default configuration: [DefaultBatchSize], [DefaultFlushInterval], no error
// handler.
func NewBatch[T any](flush func(ctx context.Context, items []T) error, opts ...BatchOption) (*Batch[T], error) {
	if flush == nil {
		return nil, ErrNilFlush
	}
	cfg := newBatchConfig(opts)

	b := &Batch[T]{
		flush: flush,
		cfg:   cfg,
		buf:   make([]T, 0, cfg.batchSize),
		done:  make(chan struct{}),
	}

	go b.ticker()
	return b, nil
}

func (b *Batch[T]) lifecycleCtx() context.Context {
	return closeSignal{done: b.done}
}

func (b *Batch[T]) ticker() {
	t := time.NewTicker(b.cfg.flushInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := b.Flush(b.lifecycleCtx()); err != nil && b.cfg.onError != nil {
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
// fails. Failed flushes restore items to the buffer for retry.
func (b *Batch[T]) Add(item T) error {
	if b.closed.Load() {
		return errClosed(componentBatch)
	}

	b.mu.Lock()
	b.buf = append(b.buf, item)
	shouldFlush := len(b.buf) >= b.cfg.batchSize
	b.mu.Unlock()

	if shouldFlush {
		return b.Flush(b.lifecycleCtx())
	}
	return nil
}

// Flush flushes the current buffer using ctx. It is a no-op when the buffer is
// empty. On failure, buffered items are restored for a later retry. Safe for
// concurrent use.
func (b *Batch[T]) Flush(ctx context.Context) error {
	b.mu.Lock()
	if len(b.buf) == 0 {
		b.mu.Unlock()
		return nil
	}
	batch := b.buf
	b.buf = make([]T, 0, b.cfg.batchSize)
	b.mu.Unlock()

	err := panix.SafeVoid(b.cfg.opOrDefault(), func() error {
		return b.flush(ctx, batch)
	})
	if err != nil {
		b.errors.Add(1)
		b.requeue(batch)
		return errFlushFailed(err)
	}

	b.items.Add(uint64(len(batch)))
	b.flushed.Add(1)
	return nil
}

func (b *Batch[T]) requeue(batch []T) {
	b.mu.Lock()
	b.buf = append(batch, b.buf...)
	b.mu.Unlock()
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
// signals lifecycle cancellation to in-flight automatic flushes. It is
// idempotent. The final flush uses a background context so shutdown is not
// aborted by lifecycle cancellation.
func (b *Batch[T]) Close() error {
	var err error
	b.closeOnce.Do(func() {
		b.closed.Store(true)
		close(b.done)
		err = b.Flush(context.Background())
	})
	return err
}

// IsClosed reports whether the batch processor has been closed.
func (b *Batch[T]) IsClosed() bool {
	return b.closed.Load()
}
