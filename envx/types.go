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

// fullKey applies the primary prefix and upper-cases name to form the
// lookup key. Fallback prefixes are not applied; see [Env.lookupName].
func (e *Env) fullKey(name string) string {
	return joinPrefix(e.cfg.prefix, strings.ToUpper(name))
}

func joinPrefix(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + keySeparator + name
}

// lookupName resolves name against the primary prefix then each fallback
// prefix (first-fill-wins). key is the actually found name, or the primary
// when nothing is set. tried lists every candidate, comma-separated, for
// [ErrMissing] diagnostics.
func (e *Env) lookupName(name string) (key, raw string, found bool, tried string) {
	name = strings.ToUpper(name)
	if len(e.cfg.fallbacks) == 0 {
		key = e.fullKey(name)
		raw, found = e.cfg.lookup(key)
		return key, raw, found, key
	}
	keys := e.candidateKeys(name)
	tried = strings.Join(keys, ", ")
	for _, k := range keys {
		raw, found = e.cfg.lookup(k)
		if found {
			return k, raw, true, tried
		}
	}
	return keys[0], "", false, tried
}

func (e *Env) candidateKeys(name string) []string {
	keys := make([]string, 0, 1+len(e.cfg.fallbacks))
	seen := make(map[string]bool, 1+len(e.cfg.fallbacks))
	add := func(k string) {
		if seen[k] {
			return
		}
		seen[k] = true
		keys = append(keys, k)
	}
	add(e.fullKey(name))
	for _, fb := range e.cfg.fallbacks {
		add(joinPrefix(fb, name))
	}
	return keys
}

// Var holds a typed value bound to an environment variable. Use [Var.Value]
// to read the resolved value and [Var.Ptr] to get a pointer (useful for
// binding the same field to a clix flag).
//
// When created by [BindTo] or [BindRequiredTo], [Var.Ptr] aliases the caller's
// target pointer so env, struct field, and clix flag share one memory location.
type Var[T any] struct {
	key      string
	value    T
	target   *T
	raw      string
	found    bool
	required bool
	parseErr string
	tried    string
}

// Value returns the resolved value: the parsed environment value when the
// variable was set and valid, otherwise the default. When the [Var] was
// created by [BindTo] or [BindRequiredTo], reads through the target pointer.
func (v *Var[T]) Value() T {
	if v.target != nil {
		return *v.target
	}
	return v.value
}

// Ptr returns a pointer to the resolved value. For [BindTo] and
// [BindRequiredTo] this is the same pointer passed to the bind call, so
// clix.AddFlag(v.Ptr(), ...) updates the overlaid struct field directly.
func (v *Var[T]) Ptr() *T {
	if v.target != nil {
		return v.target
	}
	return &v.value
}

// Found reports whether the variable was present in the environment.
func (v *Var[T]) Found() bool { return v.found }

// Key returns the full environment variable name that supplied the value
// (primary or fallback). When the variable was not set, Key is the primary
// candidate — the same name [BindRequired] reports as missing when no
// fallbacks are configured.
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
		return errMissing(v.tried)
	}
	return nil
}
