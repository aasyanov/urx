package envx

import (
	"errors"
	"fmt"
)

var (
	// ErrMissing is reported by [Env.Validate] for a variable bound with
	// [BindRequired] or [BindRequiredTo] that was not present in the
	// environment. Safe to compare with == or [errors.Is].
	ErrMissing = errors.New("envx: required environment variable not set")

	// ErrInvalid is reported by [Env.Validate] when a variable was present
	// but its value could not be parsed into the requested type. Safe to
	// compare with == or [errors.Is].
	ErrInvalid = errors.New("envx: invalid environment variable value")
)

func errMissing(name string) error {
	return fmt.Errorf("%w: %s", ErrMissing, name)
}

func errInvalid(name, reason string) error {
	return fmt.Errorf("%w: %s (%s)", ErrInvalid, name, reason)
}
