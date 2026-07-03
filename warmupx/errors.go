package warmupx

import (
	"errors"
	"fmt"
)

var (
	// ErrRejected is returned by [Warmer.AllowOrError] and [Execute] when a
	// request is not admitted because the warmer is still below full capacity.
	// The returned error wraps ErrRejected with the capacity and progress at
	// the moment of rejection. Safe to compare with == or [errors.Is].
	ErrRejected = errors.New("warmupx: request rejected during warmup")

	// ErrNilFunc is returned by [Execute] when the supplied function is nil.
	// Safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("warmupx: nil function")
)

// errRejected wraps [ErrRejected] with the capacity and progress observed when
// the request was rejected.
func errRejected(capacity, progress float64) error {
	return fmt.Errorf("%w (capacity=%.2f, progress=%.2f)", ErrRejected, capacity, progress)
}
