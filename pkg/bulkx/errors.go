package bulkx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainBulk is the [errx] domain for all bulkhead errors.
const DomainBulk = "BULK"

// --- Code constants ---

const (
	// CodeTimeout indicates that acquiring a concurrency slot timed out.
	CodeTimeout = "TIMEOUT"

	// CodeClosed indicates the bulkhead has been shut down.
	CodeClosed = "CLOSED"

	// CodeCancelled indicates the context was cancelled while waiting for a slot.
	CodeCancelled = "CANCELLED"
)

// --- Error constructors ---

// errTimeout builds a structured error for a bulkhead slot acquisition timeout.
func errTimeout() *errx.Error {
	return errx.New(DomainBulk, CodeTimeout, "bulkhead timeout")
}

// errClosed builds a structured error when the bulkhead has been shut down.
func errClosed() *errx.Error {
	return errx.New(DomainBulk, CodeClosed, "bulkhead closed")
}

// errCancelled wraps a context cancellation as a structured bulkhead error.
func errCancelled(cause error) *errx.Error {
	return errx.Wrap(cause, DomainBulk, CodeCancelled, "bulkhead wait cancelled")
}
