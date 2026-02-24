package busx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainBus is the [errx] domain for all event bus errors.
const DomainBus = "BUS"

// --- Code constants ---

const (
	// CodeClosed indicates the bus has been shut down.
	CodeClosed = "CLOSED"

	// CodePublishFailed indicates a handler panicked during event dispatch.
	CodePublishFailed = "PUBLISH_FAILED"

	// CodeNilHandler indicates a nil handler was passed to Subscribe.
	CodeNilHandler = "NIL_HANDLER"
)

// --- Error constructors ---

// errClosed builds a structured error when the bus has been shut down.
func errClosed(op string) *errx.Error {
	return errx.New(DomainBus, CodeClosed, op+" called on a closed bus")
}

// errNilHandler builds a structured error for a nil handler registration.
func errNilHandler() *errx.Error {
	return errx.New(DomainBus, CodeNilHandler, "nil handler")
}

// errPublishFailed wraps a handler panic as a structured publish error.
func errPublishFailed(event string, cause error) *errx.Error {
	return errx.Wrap(cause, DomainBus, CodePublishFailed,
		"handler panicked",
		errx.WithMeta("event", event),
	)
}
