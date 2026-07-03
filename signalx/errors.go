package signalx

import "errors"

var (
	// ErrShutdownTimeout is returned by [Wait] when the registered shutdown
	// hooks do not all complete before the configured timeout elapses.
	// It is safe to compare with == or [errors.Is].
	ErrShutdownTimeout = errors.New("signalx: shutdown timed out")

	// ErrHookPanic is returned by [Wait] when one or more shutdown hooks
	// panic. The returned error joins every hook failure; use [errors.Is]
	// to test for it and [errors.As] to extract the underlying
	// [*panix.PanicError] values. It is safe to compare with == or
	// [errors.Is].
	ErrHookPanic = errors.New("signalx: shutdown hook panicked")
)
