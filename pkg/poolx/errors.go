package poolx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainPool is the [errx] domain for all pool errors.
const DomainPool = "POOL"

// --- Code constants ---

const (
	// CodeClosed indicates the pool or batch processor has been shut down.
	CodeClosed = "CLOSED"

	// CodeQueueFull indicates the worker pool's task queue is at capacity.
	CodeQueueFull = "QUEUE_FULL"

	// CodeFlushFailed indicates a batch flush operation failed.
	CodeFlushFailed = "FLUSH_FAILED"

	// CodeCancelled indicates the context was cancelled while waiting for a queue slot.
	CodeCancelled = "CANCELLED"
)

// --- Error constructors ---

// errClosed builds a structured error when a pool component has been shut down.
func errClosed(msg string) *errx.Error {
	return errx.New(DomainPool, CodeClosed, msg)
}

// errQueueFull builds a structured error when the worker pool queue is at capacity.
func errQueueFull() *errx.Error {
	return errx.New(DomainPool, CodeQueueFull, "worker pool queue is full")
}

// errFlushFailed wraps a batch flush failure as a structured error.
func errFlushFailed(cause error) *errx.Error {
	return errx.Wrap(cause, DomainPool, CodeFlushFailed, "batch flush failed")
}

// errCtxDone wraps a context cancellation as a structured pool error.
func errCtxDone(cause error) *errx.Error {
	return errx.Wrap(cause, DomainPool, CodeCancelled, "context cancelled while waiting for queue slot")
}
