package retryx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainRetry is the [errx] domain for all retry errors.
const DomainRetry = "RETRY"

// --- Code constants ---

const (
	// CodeExhausted indicates all retry attempts have been exhausted.
	CodeExhausted = "EXHAUSTED"

	// CodeCancelled indicates the retry loop was cancelled via context.
	CodeCancelled = "CANCELLED"

	// CodeAborted indicates the caller explicitly aborted the retry loop.
	CodeAborted = "ABORTED"
)

// --- Error constructors ---

// errExhausted wraps the last error when all retry attempts have been used.
func errExhausted(attempts int, cause error) *errx.Error {
	return errx.Wrap(cause, DomainRetry, CodeExhausted,
		"all retry attempts exhausted",
		errx.WithMeta("attempts", attempts),
	)
}

// errCancelled wraps a context cancellation as a structured retry error.
func errCancelled(cause error) *errx.Error {
	return errx.Wrap(cause, DomainRetry, CodeCancelled,
		"retry cancelled by context",
	)
}

// errAborted wraps the last error when the caller explicitly aborted the retry loop.
func errAborted(attempt int, cause error) *errx.Error {
	return errx.Wrap(cause, DomainRetry, CodeAborted,
		"retry aborted by caller",
		errx.WithMeta("attempt", attempt),
	)
}
