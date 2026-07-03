package poolx

import (
	"errors"
	"fmt"
)

var (
	// ErrClosed is returned when an operation is attempted on a closed
	// [WorkerPool], [Batch], or when [Batch.Flush] is called after [Batch.Close].
	// It is safe to compare with == or [errors.Is].
	ErrClosed = errors.New("poolx: closed")

	// ErrQueueFull is returned by [WorkerPool.TrySubmit] when the task queue
	// is at capacity and the task cannot be enqueued without blocking.
	ErrQueueFull = errors.New("poolx: worker pool queue is full")

	// ErrCancelled is returned by [WorkerPool.Submit], [WorkerPool.TrySubmit],
	// and [WorkerPool.SubmitWait] when the supplied context is already
	// cancelled or its deadline has expired before the task is enqueued or
	// before a blocking wait completes. The joined error carries the
	// underlying context cause.
	ErrCancelled = errors.New("poolx: context cancelled")

	// ErrNilFunc is returned by [WorkerPool.Submit], [WorkerPool.TrySubmit],
	// and [WorkerPool.SubmitWait] when the task function is nil.
	ErrNilFunc = errors.New("poolx: nil function")

	// ErrNilFactory is returned by [NewObjectPool] when the factory function
	// is nil.
	ErrNilFactory = errors.New("poolx: nil factory")

	// ErrNilFlush is returned by [NewBatch] when the flush function is nil.
	ErrNilFlush = errors.New("poolx: nil flush function")

	// ErrFlushFailed is returned by [Batch.Flush], [Batch.Add], and
	// [Batch.Close] when the user-supplied flush function returns an error
	// or panics. The joined error carries the underlying cause. Buffered items
	// are restored to the internal buffer so a later flush can retry.
	ErrFlushFailed = errors.New("poolx: batch flush failed")
)

func errClosed(component string) error {
	return fmt.Errorf("%w: %s", ErrClosed, component)
}

func errCancelled(cause error) error {
	return fmt.Errorf("%w: %w", ErrCancelled, cause)
}

func errFlushFailed(cause error) error {
	return fmt.Errorf("%w: %w", ErrFlushFailed, cause)
}
