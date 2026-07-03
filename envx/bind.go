package envx

// bindVar is the shared core: it resolves the key, reads the raw value,
// parses it when present, records any parse error for [Env.Validate], and
// registers the [Var] on env.
func bindVar[T any](env *Env, name string, defaultVal T, required bool) *Var[T] {
	key := env.fullKey(name)
	raw, found := env.cfg.lookup(key)

	v := &Var[T]{
		key:      key,
		value:    defaultVal,
		raw:      raw,
		found:    found,
		required: required,
	}

	if v.found {
		parsed, parseErr := parse[T](raw)
		if parseErr != "" {
			v.parseErr = parseErr
		} else {
			v.value = parsed
		}
	}

	env.vars = append(env.vars, v)
	return v
}

// Bind reads an environment variable and converts it to type T. When the
// variable is not set, defaultVal is used. A value that fails to parse is
// reported by [Env.Validate] as [ErrInvalid].
//
// Supported types: string, bool, int, int32, int64, uint, float64,
// [time.Duration], [time.Time] (RFC3339), and []string (comma-separated).
func Bind[T any](env *Env, name string, defaultVal T) *Var[T] {
	return bindVar(env, name, defaultVal, false)
}

// BindRequired reads a required environment variable. When the variable is
// not set, [Env.Validate] reports it as [ErrMissing]. The resolved value is
// the zero value of T until the variable is provided.
func BindRequired[T any](env *Env, name string) *Var[T] {
	var zero T
	return bindVar(env, name, zero, true)
}

// attachTarget links the [Var] to the caller's destination so [Var.Value] and
// [Var.Ptr] read and write through the same memory as the overlaid field.
func (v *Var[T]) attachTarget(target *T) {
	v.target = target
	*target = v.value
}

// BindTo reads an environment variable and writes the resolved value into
// *target. When the variable is not set, *target keeps its current value
// (serving as the default). This is the preferred way to overlay env vars
// onto a config struct loaded by cfgx:
//
//	port := envx.BindTo(env, "PORT", &cfg.Port)
//	clix.AddFlag(port.Ptr(), "port", "p", cfg.Port, "listen port")
//
// [Var.Ptr] returns target, so clix and the struct field stay in sync.
//
// Panics if target is nil — a nil destination is a programming error.
func BindTo[T any](env *Env, name string, target *T) *Var[T] {
	if target == nil {
		panic("envx: BindTo target must not be nil")
	}
	v := bindVar(env, name, *target, false)
	v.attachTarget(target)
	return v
}

// BindRequiredTo is the required counterpart of [BindTo]: it writes the
// resolved value into *target and marks the variable required, so
// [Env.Validate] reports [ErrMissing] when it is absent. The current value
// of *target is used as the fallback until the variable is provided.
// [Var.Ptr] aliases target, same as [BindTo].
//
// Panics if target is nil — a nil destination is a programming error.
func BindRequiredTo[T any](env *Env, name string, target *T) *Var[T] {
	if target == nil {
		panic("envx: BindRequiredTo target must not be nil")
	}
	v := bindVar(env, name, *target, true)
	v.attachTarget(target)
	return v
}
