// Package panix provides panic recovery primitives for Go.
//
// panix converts panics into structured [*PanicError] values with captured
// stack traces. It is the foundational safety layer for all urx execution
// wrappers: [Safe] guards a synchronous call, [SafeGo] launches a
// panic-safe goroutine, and [Wrap] creates a reusable panic-safe wrapper.
//
// # Quick Start
//
//	val, err := panix.Safe("myop", func() (string, error) {
//	    return riskyCall()
//	})
//	if err != nil {
//	    var pe *panix.PanicError
//	    if errors.As(err, &pe) {
//	        log.Printf("panic in %s: %v\n%s", pe.Op, pe.Value, pe.Stack)
//	    }
//	}
//
// # Zero Dependencies
//
// panix depends only on the Go standard library.
package panix

import (
	"context"
	"runtime"
)

const (
	// defaultStackSize is the initial buffer size for runtime.Stack.
	defaultStackSize = 4096

	// maxStackSize caps the stack trace capture to avoid unbounded allocation.
	maxStackSize = 64 * 1024
)

// Safe executes fn and recovers any panic, converting it into a [*PanicError]
// with a captured stack trace. If fn returns a non-nil error without panicking,
// that error is returned as-is.
//
// Safe is safe for concurrent use from multiple goroutines; it holds no
// shared mutable state.
//
// The op parameter identifies the call site in error messages and should use
// the "package.Function" convention (e.g. "retryx.Do", "bulkx.Execute").
// A nil fn panics at call time and is recovered as a [*PanicError].
func Safe[T any](op string, fn func() (T, error)) (val T, err error) {
	defer func() {
		if r := recover(); r != nil {
			var zero T
			val = zero
			err = &PanicError{
				Op:    op,
				Value: r,
				Stack: captureStack(),
			}
		}
	}()
	return fn()
}

// SafeVoid executes fn and recovers any panic, converting it into a
// [*PanicError] with a captured stack trace. Use SafeVoid for functions
// that return only an error, avoiding the generic type parameter.
//
// SafeVoid is safe for concurrent use from multiple goroutines.
func SafeVoid(op string, fn func() error) error {
	_, err := Safe(op, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}

// SafeGo launches fn in a new goroutine with panic recovery. If fn panics,
// the recovered [*PanicError] is passed to the onError callback (if non-nil).
// A nil ctx is treated as [context.Background]. A nil fn panics at call time
// and is recovered like any other panic in the goroutine.
//
// SafeGo never re-panics and never crashes the process. If onError is nil,
// panics in fn are silently recovered. Panics raised by onError are also
// recovered via an internal [SafeVoid] wrapper; they are not propagated and
// any work onError did not complete (for example a channel send) is lost.
//
// SafeGo is safe for concurrent use: each call launches an independent
// goroutine with no shared package-level state.
func SafeGo(ctx context.Context, op string, fn func(ctx context.Context), onError func(ctx context.Context, err error)) {
	if ctx == nil {
		ctx = context.Background()
	}
	safeCtx := ctx
	go func() {
		err := SafeVoid(op, func() error {
			fn(safeCtx)
			return nil
		})
		if err != nil && onError != nil {
			_ = SafeVoid(op+".onError", func() error {
				onError(safeCtx, err)
				return nil
			})
		}
	}()
}

// Wrap returns a panic-safe version of fn. Each call to the returned
// function runs fn under [Safe] with the given op label. The returned
// closure is safe for concurrent use.
func Wrap[T any](op string, fn func() (T, error)) func() (T, error) {
	return func() (T, error) {
		return Safe(op, fn)
	}
}

// WrapVoid returns a panic-safe version of fn. Each call to the returned
// function runs fn under [SafeVoid] with the given op label. The returned
// closure is safe for concurrent use.
func WrapVoid(op string, fn func() error) func() error {
	return func() error {
		return SafeVoid(op, fn)
	}
}

// captureStack collects the current goroutine's stack trace, growing the
// buffer until the full trace fits or [maxStackSize] is reached.
func captureStack() []byte {
	return captureStackLimited(maxStackSize)
}

func captureStackLimited(cap int) []byte {
	buf := make([]byte, min(defaultStackSize, cap))
	for {
		n := runtime.Stack(buf, false)
		if n < len(buf) {
			return buf[:n]
		}
		if len(buf) >= cap {
			return buf
		}
		buf = make([]byte, min(len(buf)*2, cap))
	}
}
