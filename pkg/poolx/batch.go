package poolx

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/pkg/panix"
)

// --- Batch config ---

// Batch processor default values.
const (
	defaultBatchSize     = 100
	defaultFlushInterval = time.Second
)

// BatchOption configures [NewBatch].
type BatchOption func(*batchConfig)

// batchConfig holds batch processor parameters.
type batchConfig struct {
	batchSize     int
	flushInterval time.Duration
}

// defaultBatchConfig returns sensible batch defaults (100 items, 1 s interval).
func defaultBatchConfig() batchConfig {
	return batchConfig{batchSize: defaultBatchSize, flushInterval: defaultFlushInterval}
}

// WithBatchSize sets the maximum number of items buffered before an
// automatic flush.
func WithBatchSize(n int) BatchOption {
	return func(c *batchConfig) {
		if n > 0 {
			c.batchSize = n
		}
	}
}

// WithFlushInterval sets the periodic flush interval.
// Values <= 0 are ignored and the default (1 s) is kept.
func WithFlushInterval(d time.Duration) BatchOption {
	return func(c *batchConfig) {
		if d > 0 {
			c.flushInterval = d
		}
	}
}

// --- Batch ---

// Batch buffers items and flushes them in batches via a user-provided
// function. Flushing occurs when the buffer reaches [WithBatchSize] or
// every [WithFlushInterval], whichever comes first.
type Batch[T any] struct {
	flush  func([]T) error
	cfg    batchConfig
	mu     sync.Mutex
	buf    []T
	closed atomic.Bool
	done   chan struct{}

	flushed atomic.Uint64
	items   atomic.Uint64
	errors  atomic.Uint64
}

// BatchStats holds point-in-time counters for a [Batch].
type BatchStats struct {
	BatchSize     int    `json:"batch_size"`
	FlushInterval string `json:"flush_interval"`
	Buffered      int    `json:"buffered"`
	Flushed       uint64 `json:"flushed"`
	Items         uint64 `json:"items"`
	Errors        uint64 `json:"errors"`
}

// NewBatch creates a [Batch] that calls flush when the buffer is full or
// the interval elapses. Panics if flush is nil.
func NewBatch[T any](flush func([]T) error, opts ...BatchOption) *Batch[T] {
	if flush == nil {
		panic("poolx: nil flush function")
	}
	cfg := defaultBatchConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	b := &Batch[T]{
		flush: flush,
		cfg:   cfg,
		buf:   make([]T, 0, cfg.batchSize),
		done:  make(chan struct{}),
	}

	go b.ticker()
	return b
}

// ticker runs the periodic flush loop in a background goroutine.
func (b *Batch[T]) ticker() {
	t := time.NewTicker(b.cfg.flushInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			b.Flush()
		case <-b.done:
			return
		}
	}
}

// Add appends an item to the buffer. If the buffer reaches batch size,
// a flush is triggered. Returns an error if the batch is closed.
func (b *Batch[T]) Add(item T) error {
	if b.closed.Load() {
		return errClosed("batch processor is closed")
	}

	b.mu.Lock()
	b.buf = append(b.buf, item)
	shouldFlush := len(b.buf) >= b.cfg.batchSize
	b.mu.Unlock()

	if shouldFlush {
		return b.Flush()
	}
	return nil
}

// Flush forces a flush of the current buffer. Thread-safe.
func (b *Batch[T]) Flush() error {
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

	_, err := panix.Safe[struct{}](context.Background(), "poolx.Batch.Flush", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, b.flush(batch)
	})
	if err != nil {
		b.errors.Add(1)
		return errFlushFailed(err)
	}
	return nil
}

// Stats returns point-in-time counters.
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

// Close flushes remaining items and stops the periodic ticker.
func (b *Batch[T]) Close() error {
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(b.done)
	return b.Flush()
}

// IsClosed reports whether the batch processor has been closed.
func (b *Batch[T]) IsClosed() bool {
	return b.closed.Load()
}
