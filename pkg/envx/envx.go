// Package envx provides typed environment variable reading with injectable
// lookup for industrial Go services.
//
// An [Env] reads variables through a configurable lookup function (defaults
// to [os.LookupEnv]) and converts them to typed values via generic [Bind].
// All unresolved required variables are collected into a single
// [errx.MultiError] on [Env.Validate].
//
//	env := envx.New(envx.WithPrefix("APP"))
//
//	port := envx.Bind(env, "PORT", 8080)
//	host := envx.Bind(env, "DB_HOST", "localhost")
//	secret := envx.BindRequired[string](env, "SECRET")
//
//	if err := env.Validate(); err != nil {
//	    log.Fatal(err)  // APP_SECRET is required
//	}
//
//	fmt.Println(port.Value())    // 8080 or from APP_PORT
//	fmt.Println(secret.Value())  // from APP_SECRET
//
// For testing, inject a custom lookup:
//
//	env := envx.New(
//	    envx.WithPrefix("APP"),
//	    envx.WithLookup(envx.MapLookup(map[string]string{
//	        "APP_PORT":   "9090",
//	        "APP_SECRET": "test-key",
//	    })),
//	)
package envx

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Configuration ---

type config struct {
	prefix string
	lookup func(string) (string, bool)
}

func defaultConfig() config {
	return config{
		lookup: os.LookupEnv,
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option func(*config)

// WithPrefix sets a prefix prepended to all variable names.
// Example: WithPrefix("APP") makes Bind(env, "PORT", 0) read "APP_PORT".
func WithPrefix(prefix string) Option {
	return func(c *config) {
		c.prefix = strings.TrimSuffix(strings.ToUpper(prefix), "_")
	}
}

// WithLookup sets the function used to read environment variables.
// Default: [os.LookupEnv]. Override for testing or custom sources.
func WithLookup(fn func(string) (string, bool)) Option {
	return func(c *config) {
		if fn != nil {
			c.lookup = fn
		}
	}
}

// MapLookup returns a lookup function backed by a static map.
// Useful for testing without touching real environment.
func MapLookup(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

// --- Env ---

// Env holds configuration and tracks bound variables. Create with [New].
type Env struct {
	cfg  config
	vars []validator
}

type validator interface {
	validate() *errx.Error
	name() string
}

// New creates an [Env] with the given options.
func New(opts ...Option) *Env {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return &Env{cfg: cfg}
}

// Validate checks all required variables. Returns *errx.MultiError if any
// are missing or invalid, nil otherwise.
func (e *Env) Validate() error {
	me := errx.NewMulti()
	for _, v := range e.vars {
		if err := v.validate(); err != nil {
			me.Add(err)
		}
	}
	return me.Err()
}

// Vars returns the names of all bound variables (with prefix).
func (e *Env) Vars() []string {
	names := make([]string, len(e.vars))
	for i, v := range e.vars {
		names[i] = v.name()
	}
	return names
}

func (e *Env) fullKey(name string) string {
	name = strings.ToUpper(name)
	if e.cfg.prefix != "" {
		return e.cfg.prefix + "_" + name
	}
	return name
}

// --- Var ---

// Var holds a typed value bound to an environment variable.
// Use [Value] to read the resolved value and [Ptr] to get a pointer
// (useful for integration with [clix] flags).
type Var[T any] struct {
	key      string
	value    T
	raw      string
	found    bool
	required bool
	parseErr string
}

// Value returns the resolved value (env override or default).
func (v *Var[T]) Value() T { return v.value }

// Ptr returns a pointer to the resolved value. Useful for binding
// the same variable to a CLI flag.
func (v *Var[T]) Ptr() *T { return &v.value }

// Found reports whether the variable was present in the environment.
func (v *Var[T]) Found() bool { return v.found }

// Key returns the full environment variable name (with prefix).
func (v *Var[T]) Key() string { return v.key }

func (v *Var[T]) name() string { return v.key }

func (v *Var[T]) validate() *errx.Error {
	if v.parseErr != "" {
		return errInvalid(v.key, v.parseErr)
	}
	if v.required && !v.found {
		return errMissing(v.key)
	}
	return nil
}

// --- Bind ---

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

// Bind reads an environment variable and converts it to type T.
// If the variable is not set, defaultVal is used. Supported types:
// string, int, int64, float64, bool, time.Duration.
func Bind[T any](env *Env, name string, defaultVal T) *Var[T] {
	return bindVar(env, name, defaultVal, false)
}

// BindRequired reads a required environment variable. If the variable
// is not set, [Env.Validate] will report it as missing.
func BindRequired[T any](env *Env, name string) *Var[T] {
	var zero T
	return bindVar(env, name, zero, true)
}

// BindTo reads an environment variable and writes the value directly into
// *target if the variable is set. If it is not set, *target keeps its
// current value (serving as the default). This is the preferred way to
// overlay env vars onto a config struct loaded by [cfgx]:
//
//	envx.BindTo(env, "PORT", &cfg.Port)
//	envx.BindTo(env, "HOST", &cfg.Host)
func BindTo[T any](env *Env, name string, target *T) *Var[T] {
	if target == nil {
		panic("envx: BindTo target must not be nil")
	}
	v := Bind(env, name, *target)
	*target = v.value
	return v
}

// --- Parsing ---

func parse[T any](raw string) (T, string) {
	var zero T
	switch any(zero).(type) {
	case string:
		return any(raw).(T), ""
	case int:
		v, err := strconv.Atoi(raw)
		if err != nil {
			return zero, fmt.Sprintf("expected int: %s", raw)
		}
		return any(v).(T), ""
	case int64:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return zero, fmt.Sprintf("expected int64: %s", raw)
		}
		return any(v).(T), ""
	case float64:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return zero, fmt.Sprintf("expected float64: %s", raw)
		}
		return any(v).(T), ""
	case bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return zero, fmt.Sprintf("expected bool: %s", raw)
		}
		return any(v).(T), ""
	case time.Duration:
		v, err := time.ParseDuration(raw)
		if err != nil {
			return zero, fmt.Sprintf("expected duration: %s", raw)
		}
		return any(v).(T), ""
	default:
		return zero, fmt.Sprintf("unsupported type %T", zero)
	}
}
