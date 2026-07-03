package panix

import "fmt"

// PanicError represents a recovered panic with a captured stack trace.
// It is safe for concurrent read access from multiple goroutines once
// constructed; instances are not mutated after recovery.
//
// Op identifies the call site using the "package.Function" convention.
// Value is the original value passed to panic(). Stack is the raw
// goroutine stack trace captured at recovery time.
//
// If the original panic value was an error, [PanicError.Unwrap] returns
// it, enabling [errors.Is] and [errors.As] chains through the cause.
type PanicError struct {
	// Op identifies the operation that panicked (e.g. "retryx.Do").
	Op string

	// Value is the original panic value.
	Value any

	// Stack is the goroutine stack trace captured at recovery time.
	// It is never nil for panics recovered by [Safe] or [SafeGo].
	Stack []byte
}

// Error returns a human-readable description including the operation
// and the panic value.
func (e *PanicError) Error() string {
	return fmt.Sprintf("panix: panic in %s: %v", e.Op, e.Value)
}

// Unwrap returns the original panic value if it implements the error
// interface, enabling [errors.Is] and [errors.As] on the panic cause.
// Returns nil if the panic value is not an error.
func (e *PanicError) Unwrap() error {
	if err, ok := e.Value.(error); ok {
		return err
	}
	return nil
}
