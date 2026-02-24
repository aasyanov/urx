package toutx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainTimeout is the [errx] domain for all timeout errors.
const DomainTimeout = "TIMEOUT"

// --- Code constants ---

const (
	// CodeDeadlineExceeded indicates the operation did not complete within the
	// configured timeout.
	CodeDeadlineExceeded = "DEADLINE_EXCEEDED"

	// CodeCancelled indicates the parent context was cancelled before the
	// operation completed.
	CodeCancelled = "CANCELLED"
)

// --- Error constructors ---

// errDeadlineExceeded builds a structured error when the operation timed out.
func errDeadlineExceeded(op string) *errx.Error {
	return errx.New(DomainTimeout, CodeDeadlineExceeded, "timeout exceeded",
		errx.WithOp(op),
	)
}

// errCancelled wraps a parent-context cancellation as a structured timeout error.
func errCancelled(op string, cause error) *errx.Error {
	return errx.Wrap(cause, DomainTimeout, CodeCancelled, "cancelled",
		errx.WithOp(op),
	)
}
