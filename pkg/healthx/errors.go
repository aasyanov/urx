package healthx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainHealth is the [errx] domain for all health-check errors.
const DomainHealth = "HEALTH"

// --- Code constants ---

const (
	// CodeUnhealthy indicates a component's health check returned an error.
	CodeUnhealthy = "UNHEALTHY"

	// CodeTimeout indicates a component's health check exceeded its timeout.
	CodeTimeout = "TIMEOUT"
)

// --- Error constructors ---

// errUnhealthy wraps a component check failure as a structured health error.
func errUnhealthy(name string, cause error) *errx.Error {
	return errx.Wrap(cause, DomainHealth, CodeUnhealthy, name+" is unhealthy",
		errx.WithMeta("component", name),
	)
}

// errTimeout builds a structured error when a component check exceeds its timeout.
func errTimeout(name string) *errx.Error {
	return errx.New(DomainHealth, CodeTimeout, name+" health check timed out",
		errx.WithMeta("component", name),
	)
}
