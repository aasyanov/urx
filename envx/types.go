package envx

import "strings"

// Env holds configuration and tracks bound variables. Create with [New].
// An Env is not safe for concurrent Bind calls; build it on one goroutine
// during startup, then read the resulting [Var] values freely.
type Env struct {
	cfg  config
	vars []validator
}

// validator is the internal contract every bound [Var] satisfies, allowing
// [Env.Validate] to collect failures without knowing the concrete type T.
type validator interface {
	validate() error
	name() string
}

// fullKey applies the prefix and upper-cases name to form the lookup key.
func (e *Env) fullKey(name string) string {
	name = strings.ToUpper(name)
	if e.cfg.prefix != "" {
		return e.cfg.prefix + keySeparator + name
	}
	return name
}

// Var holds a typed value bound to an environment variable. Use [Var.Value]
// to read the resolved value and [Var.Ptr] to get a pointer (useful for
// binding the same field to a clix flag).
type Var[T any] struct {
	key      string
	value    T
	raw      string
	found    bool
	required bool
	parseErr string
}

// Value returns the resolved value: the parsed environment value when the
// variable was set and valid, otherwise the default.
func (v *Var[T]) Value() T { return v.value }

// Ptr returns a pointer to the resolved value. Useful for binding the same
// variable to a CLI flag via clix.AddFlag(v.Ptr(), ...).
func (v *Var[T]) Ptr() *T { return &v.value }

// Found reports whether the variable was present in the environment.
func (v *Var[T]) Found() bool { return v.found }

// Key returns the full environment variable name (with prefix applied).
func (v *Var[T]) Key() string { return v.key }

// Raw returns the unparsed string read from the environment. It is empty
// when the variable was not set; check [Var.Found] to disambiguate an unset
// variable from one set to the empty string.
func (v *Var[T]) Raw() string { return v.raw }

func (v *Var[T]) name() string { return v.key }

func (v *Var[T]) validate() error {
	if v.parseErr != "" {
		return errInvalid(v.key, v.parseErr)
	}
	if v.required && !v.found {
		return errMissing(v.key)
	}
	return nil
}
