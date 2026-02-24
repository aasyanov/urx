package testx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainTest is the [errx] domain for all simulated test errors.
const DomainTest = "TEST"

// --- Code constants ---

const (
	// CodeSimulated indicates a simulated failure from the [Simulator].
	CodeSimulated = "SIMULATED"
)

// --- Error constructors ---

// errSimulated builds a structured simulated error.
func errSimulated(msg string) *errx.Error {
	return errx.New(DomainTest, CodeSimulated, msg,
		errx.WithRetry(errx.RetrySafe),
	)
}
