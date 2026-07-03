package shedx

import (
	"errors"
	"fmt"
)

var (
	// ErrRejected is returned by [Execute] and [Acquire] when a request is shed
	// because the shedder is overloaded for its priority. [TryExecute] returns
	// (false, zero, nil) instead. The wrapped error carries the request priority.
	// Safe to compare with == or [errors.Is].
	ErrRejected = errors.New("shedx: request shed")

	// ErrClosed is returned by [Execute], [TryExecute], and [Acquire] after
	// [Shedder.Close] has been called. Safe to compare with == or [errors.Is].
	ErrClosed = errors.New("shedx: shedder closed")

	// ErrNilFunc is returned by [Execute] and [TryExecute] when the supplied
	// function is nil. Safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("shedx: nil function")

	// ErrCancelled is returned by [Execute] and [TryExecute] when the supplied
	// context is already cancelled (or its deadline has expired) at admission
	// time, so no in-flight slot is consumed and fn is never invoked. The joined
	// error carries ctx.Err(); reach it with [errors.Unwrap] or test it with
	// [errors.Is]. Safe to compare with == or [errors.Is].
	ErrCancelled = errors.New("shedx: context cancelled")
)

// errRejected wraps [ErrRejected] with the priority that was shed.
func errRejected(priority Priority) error {
	return fmt.Errorf("%w (priority=%s)", ErrRejected, priority)
}

// errCancelled wraps [ErrCancelled] joining the underlying context cause.
func errCancelled(cause error) error {
	return fmt.Errorf("%w: %w", ErrCancelled, cause)
}
