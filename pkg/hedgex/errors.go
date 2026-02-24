package hedgex

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// DomainHedge is the [errx] domain for all hedging errors.
const DomainHedge = "HEDGE"

const (
	// CodeAllFailed indicates every hedged attempt returned an error.
	CodeAllFailed = "ALL_FAILED"
	// CodeNoFunctions indicates an empty function list was provided.
	CodeNoFunctions = "NO_FUNCTIONS"
	// CodeCancelled indicates the parent context was cancelled.
	CodeCancelled = "CANCELLED"
)

func errAllFailed(cause error) *errx.Error {
	return errx.Wrap(cause, DomainHedge, CodeAllFailed, "all hedged requests failed",
		errx.WithRetry(errx.RetrySafe),
	)
}

func errNoFunctions() *errx.Error {
	return errx.New(DomainHedge, CodeNoFunctions, "no functions provided")
}

func errCancelled(cause error) *errx.Error {
	return errx.Wrap(cause, DomainHedge, CodeCancelled, "hedging cancelled")
}
