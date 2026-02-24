package ratex

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainRate is the [errx] domain for all rate limiter errors.
const DomainRate = "RATE"

// --- Code constants ---

const (
	// CodeLimited indicates the request was rejected by the rate limiter.
	CodeLimited = "LIMITED"

	// CodeCancelled indicates the wait was cancelled via context.
	CodeCancelled = "CANCELLED"
)

// --- Error constructors ---

// errCancelled wraps a context cancellation as a structured rate-limiter error.
func errCancelled(cause error) *errx.Error {
	return errx.Wrap(cause, DomainRate, CodeCancelled, "rate limiter wait cancelled")
}
