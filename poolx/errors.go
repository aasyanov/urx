package poolx

import (
	"errors"
	"fmt"
)

var (
	// ErrClosed is returned by [WorkerPool.Submit], [WorkerPool.TrySubmit],
	// [WorkerPool.SubmitWait], and [Batch.Add] when the pool or batch has
	// already been closed. It is safe to compare with == or [errors.Is].
	ErrClosed = errors.New("poolx: closed")

	// ErrQueueFull is returned by [WorkerPool.TrySubmit] when the task queue
	// is at capacity and the task cannot be enqueued without blocking.
	// It is safe to compare with == or [errors.Is].
	ErrQueueFull = errors.New("poolx: worker pool queue is full")

	// ErrCancelled is returned by [WorkerPool.Submit] and
	// [WorkerPool.SubmitWait] when the context is cancelled before a queue
	// slot becomes available. The joined error carries the underlying
	// [context.Context] cause. It is safe to compare with == or [errors.Is].
	ErrCancelled = errors.New("poolx: context cancelled")

	// ErrFlushFailed is returned by [Batch.Flush], [Batch.Add], and
	// [Batch.Close] when the user-supplied flush function returns an error
	// or panics. The joined error carries the underlying cause. It is safe
	// to compare with == or [errors.Is].
	ErrFlushFailed = errors.New("poolx: batch flush failed")
)

// errClosed wraps [ErrClosed] with the name of the closed component.
func errClosed(component string) error {
	return fmt.Errorf("%w: %s", ErrClosed, component)
}

// errCancelled wraps [ErrCancelled] with the context cause.
func errCancelled(cause error) error {
	return fmt.Errorf("%w: %w", ErrCancelled, cause)
}

// errFlushFailed wraps [ErrFlushFailed] with the flush cause.
func errFlushFailed(cause error) error {
	return fmt.Errorf("%w: %w", ErrFlushFailed, cause)
}
