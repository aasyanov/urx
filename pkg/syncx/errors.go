package syncx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainSync is the [errx] domain for all sync errors.
const DomainSync = "SYNC"

// --- Code constants ---

const (
	// CodeInitFailed indicates that a [Lazy] initializer returned an error.
	CodeInitFailed = "INIT_FAILED"
)

// --- Error constructors ---

// errInitFailed wraps a [Lazy] initializer failure as a structured sync error.
func errInitFailed(cause error) *errx.Error {
	return errx.Wrap(cause, DomainSync, CodeInitFailed, "lazy init failed")
}
