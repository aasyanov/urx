package shedx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainShed is the [errx] domain for all load shedding errors.
const DomainShed = "SHED"

// --- Code constants ---

const (
	// CodeRejected indicates the request was rejected due to load shedding.
	CodeRejected = "REJECTED"

	// CodeClosed indicates the shedder has been shut down.
	CodeClosed = "CLOSED"
)

// --- Error constructors ---

// errRejected builds a structured error when a request is shed due to overload.
func errRejected(priority Priority) *errx.Error {
	return errx.New(DomainShed, CodeRejected, "request shed",
		errx.WithSeverity(errx.SeverityWarn),
		errx.WithMeta("priority", priority.String()),
	)
}

// errClosed builds a structured error when the shedder has been shut down.
func errClosed() *errx.Error {
	return errx.New(DomainShed, CodeClosed, "shedder closed")
}
