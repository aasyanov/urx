package envx

// bindVar is the shared core: it resolves the key (primary then fallbacks),
// reads the raw value, parses it when present, records any parse error for
// [Env.Validate], and registers the [Var] on env.
func bindVar[T any](env *Env, name string, defaultVal T, required bool) *Var[T] {
	key, raw, found, tried := env.lookupName(name)

	v := &Var[T]{
		key:      key,
		value:    defaultVal,
		raw:      raw,
		found:    found,
		required: required,
		tried:    tried,
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
// exact [time.Duration] (ParseDuration, unit required), [time.Time] and
// named types convertible to it (RFC3339), []string (comma-separated),
// defined types whose underlying kind is a supported builtin (named int64
// parses as an integer, not a duration), and types whose pointer implements
// encoding.TextUnmarshaler.
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

// BindField overlays one [Field] from [Walk] onto env using the same lookup
// (prefix + fallbacks), parse, default-keep, and [Env.Validate] path as
// [BindTo]. It does not return a typed [Var]: clix flag aliasing stays on
// [BindTo] / [Var.Ptr].
//
// Panics if env or f.Ptr is nil — programming errors, matching [BindTo].
func BindField(env *Env, f Field) {
	if env == nil {
		panic("envx: BindField env must not be nil")
	}
	if f.Ptr == nil {
		panic("envx: BindField pointer must not be nil")
	}

	key, raw, found, _ := env.lookupName(f.Key)
	v := &fieldVar{key: key}
	if found {
		if diag := parseInto(f.Ptr, raw); diag != "" {
			v.parseErr = diag
		}
	}
	env.vars = append(env.vars, v)
}

// fieldVar is the type-erased [validator] registered by [BindField].
type fieldVar struct {
	key      string
	parseErr string
}

func (v *fieldVar) name() string { return v.key }

func (v *fieldVar) validate() error {
	if v.parseErr != "" {
		return errInvalid(v.key, v.parseErr)
	}
	return nil
}
