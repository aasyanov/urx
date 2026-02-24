package circuitx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainCircuit is the [errx] domain for all circuit breaker errors.
const DomainCircuit = "CIRCUIT"

// --- Code constants ---

const (
	// CodeOpen indicates the circuit breaker is open and rejecting calls.
	CodeOpen = "OPEN"
)

// --- Error constructors ---

// errOpen builds a structured error when the circuit breaker is open.
func errOpen() *errx.Error {
	return errx.New(DomainCircuit, CodeOpen, "circuit breaker is open",
		errx.WithSeverity(errx.SeverityWarn),
	)
}
