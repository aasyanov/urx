// Package env2x provides reflection-based environment variable overlay for
// Go structs. It is an experimental companion to [envx] that trades
// compile-time type safety for zero-boilerplate binding.
//
// A single [Overlay] call walks the target struct via reflection, builds
// environment variable names from struct tags (default: yaml), and writes
// values into matching fields:
//
//	type Config struct {
//	    Host string `yaml:"host"`
//	    Port int    `yaml:"port"`
//	}
//
//	cfg := Config{Host: "localhost", Port: 8080}
//	env := env2x.New(env2x.WithPrefix("APP"))
//	result := env2x.Overlay(env, &cfg)
//	// reads APP_HOST, APP_PORT from the environment
//
// # Tag resolution
//
// By default the "yaml" struct tag is used so existing config structs
// work without additional annotation. Use [WithTag] to switch to a
// different tag (e.g. "env", "json").
//
// Tag values are converted to SCREAMING_SNAKE_CASE: "log_level" becomes
// "LOG_LEVEL". Fields tagged "-" are skipped entirely (useful for secrets
// that should never be listed). The ",inline" option flattens the path
// so nested fields appear at the parent level.
//
// # Nested structs
//
// The walker descends into nested structs (both value and pointer types).
// Path segments are joined with "_":
//
//	type Config struct {
//	    DB *DBConfig `yaml:"db"`
//	}
//	type DBConfig struct {
//	    Host string `yaml:"host"`
//	}
//	// with prefix "APP" -> reads APP_DB_HOST
//
// Nil pointers are allocated lazily only when matching variables exist.
//
// # Supported types
//
// string, int, int8–int64, uint, uint8–uint64, float32, float64, bool,
// and [time.Duration]. Numeric overflow is checked before assignment.
//
// # When to use env2x vs envx
//
//   - [envx] — explicit, generic, compile-time safe, N lines for N fields
//   - env2x — reflection, zero boilerplate, one line for any struct
//
// Both can be combined: env2x.Overlay for bulk fields, then
// envx.BindRequired for secrets that need explicit required-validation.
package env2x

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Configuration ---

type config struct {
	prefix string
	lookup func(string) (string, bool)
	tag    string
}

func defaultConfig() config {
	return config{
		lookup: os.LookupEnv,
		tag:    "yaml",
	}
}

// --- Options ---

// Option configures [New] behaviour.
type Option func(*config)

// WithPrefix sets a prefix prepended to all generated variable names.
// The prefix is uppercased and separated from the field segment by "_".
func WithPrefix(prefix string) Option {
	return func(c *config) {
		c.prefix = strings.TrimSuffix(strings.ToUpper(prefix), "_")
	}
}

// WithLookup replaces the default [os.LookupEnv] with a custom function.
// Useful for testing without touching real environment variables.
func WithLookup(fn func(string) (string, bool)) Option {
	return func(c *config) {
		if fn != nil {
			c.lookup = fn
		}
	}
}

// MapLookup returns a lookup function backed by a static map.
func MapLookup(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

// WithTag sets which struct tag to read for field names. Default: "yaml".
// Common alternatives: "json", "env".
func WithTag(tag string) Option {
	return func(c *config) {
		if tag != "" {
			c.tag = tag
		}
	}
}

// --- Env ---

// Env holds configuration for [Overlay]. Create with [New].
type Env struct {
	cfg config
}

// New creates an [Env] with the given options.
func New(opts ...Option) *Env {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return &Env{cfg: cfg}
}

// --- Result ---

// Result holds the outcome of an [Overlay] call.
type Result struct {
	// Available lists every environment variable name the struct could consume.
	Available []string
	// Found lists the subset of Available that are currently set.
	Found []string
	// Applied contains human-readable log entries for each successful assignment.
	Applied []string
	// Errors contains parse/assignment errors as [*errx.Error].
	Errors []error
}

// Err returns nil when no errors occurred, or an [*errx.MultiError]
// collecting all problems.
func (r *Result) Err() error {
	if len(r.Errors) == 0 {
		return nil
	}
	me := errx.NewMulti()
	for _, e := range r.Errors {
		me.Add(e)
	}
	return me
}

// --- Overlay ---

// Overlay walks target (must be a pointer to a struct) via reflection,
// reads environment variables according to struct tags, and writes their
// values into the corresponding fields. The returned [Result] describes
// what was discovered, applied, and any errors encountered.
func Overlay(env *Env, target any) *Result {
	r := &Result{
		Available: make([]string, 0),
		Found:     make([]string, 0),
		Applied:   make([]string, 0),
		Errors:    make([]error, 0),
	}

	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		r.Errors = append(r.Errors, errInvalidInput("target must be a non-nil pointer to struct"))
		return r
	}

	var prefix []string
	if env.cfg.prefix != "" {
		prefix = []string{env.cfg.prefix}
	}

	walk(env, v, prefix, "", r, make(map[uintptr]bool))
	return r
}

// --- Reflection walker ---

func walk(env *Env, current reflect.Value, path []string, parentField string, r *Result, visited map[uintptr]bool) {
	if current.Kind() == reflect.Ptr {
		if current.IsNil() {
			return
		}
		ptr := current.Pointer()
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		current = current.Elem()
	}

	if current.Kind() != reflect.Struct {
		return
	}

	typ := current.Type()
	for i := 0; i < current.NumField(); i++ {
		fieldVal := current.Field(i)
		fieldType := typ.Field(i)

		tag := fieldType.Tag.Get(env.cfg.tag)
		if tag == "-" {
			continue
		}

		tagName, inline := parseTag(tag)

		var fieldPath []string
		switch {
		case inline:
			fieldPath = copyPath(path)
		case tagName == "":
			fieldPath = append(copyPath(path), strings.ToUpper(fieldType.Name))
		default:
			fieldPath = append(copyPath(path), toScreamingSnake(tagName))
		}

		qualifiedName := fieldType.Name
		if parentField != "" {
			qualifiedName = parentField + "." + fieldType.Name
		}

		if isScalar(fieldVal.Type()) {
			envVar := strings.Join(fieldPath, "_")
			r.Available = append(r.Available, envVar)

			raw, ok := env.cfg.lookup(envVar)
			if !ok {
				continue
			}
			r.Found = append(r.Found, envVar)

			if !fieldVal.CanSet() {
				r.Errors = append(r.Errors, errNotSettable(envVar, qualifiedName))
				continue
			}

			if err := setField(fieldVal, raw, envVar, qualifiedName); err != nil {
				r.Errors = append(r.Errors, err)
				continue
			}

			r.Applied = append(r.Applied, fmt.Sprintf("%s=%s -> %s", envVar, raw, qualifiedName))
			continue
		}

		// Descend into nested structs.
		ftype := fieldVal.Type()
		if ftype.Kind() == reflect.Ptr {
			if ftype.Elem().Kind() != reflect.Struct {
				continue
			}
			if fieldVal.IsNil() {
				if !hasMatchingVars(env, fieldPath, ftype.Elem()) {
					continue
				}
				if fieldVal.CanSet() {
					fieldVal.Set(reflect.New(ftype.Elem()))
				}
			}
		} else if ftype.Kind() != reflect.Struct {
			continue
		}

		walk(env, fieldVal, fieldPath, qualifiedName, r, visited)
	}
}

// hasMatchingVars probes whether any env variable exists that would match
// a field inside structType. Used to decide if a nil pointer should be
// allocated.
func hasMatchingVars(env *Env, basePath []string, structType reflect.Type) bool {
	if structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}
	if structType.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < structType.NumField(); i++ {
		ft := structType.Field(i)
		tag := ft.Tag.Get(env.cfg.tag)
		if tag == "-" {
			continue
		}

		tagName, inline := parseTag(tag)

		var fp []string
		switch {
		case inline:
			fp = copyPath(basePath)
		case tagName == "":
			fp = append(copyPath(basePath), strings.ToUpper(ft.Name))
		default:
			fp = append(copyPath(basePath), toScreamingSnake(tagName))
		}

		envVar := strings.Join(fp, "_")
		if _, ok := env.cfg.lookup(envVar); ok {
			return true
		}

		inner := ft.Type
		if inner.Kind() == reflect.Ptr {
			inner = inner.Elem()
		}
		if inner.Kind() == reflect.Struct {
			if hasMatchingVars(env, fp, inner) {
				return true
			}
		}
	}
	return false
}

// --- Field assignment ---

var durationType = reflect.TypeOf(time.Duration(0))

func setField(field reflect.Value, raw, envVar, qualifiedName string) error {
	// Handle pointer-to-scalar: allocate if nil, then dereference.
	if field.Kind() == reflect.Ptr {
		if !isScalar(field.Type().Elem()) {
			return errUnsupportedType(envVar, qualifiedName, field.Type().String())
		}
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		field = field.Elem()
	}

	// time.Duration is int64 underneath but needs special parsing.
	if field.Type() == durationType {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return errParseFailed(envVar, qualifiedName, err.Error())
		}
		field.SetInt(int64(d))
		return nil
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return errParseFailed(envVar, qualifiedName, err.Error())
		}
		if field.OverflowInt(v) {
			return errParseFailed(envVar, qualifiedName,
				fmt.Sprintf("value %d overflows %s", v, field.Type()))
		}
		field.SetInt(v)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return errParseFailed(envVar, qualifiedName, err.Error())
		}
		if field.OverflowUint(v) {
			return errParseFailed(envVar, qualifiedName,
				fmt.Sprintf("value %d overflows %s", v, field.Type()))
		}
		field.SetUint(v)

	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return errParseFailed(envVar, qualifiedName, err.Error())
		}
		if field.OverflowFloat(v) {
			return errParseFailed(envVar, qualifiedName,
				fmt.Sprintf("value %f overflows %s", v, field.Type()))
		}
		field.SetFloat(v)

	case reflect.Bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return errParseFailed(envVar, qualifiedName, err.Error())
		}
		field.SetBool(v)

	default:
		return errUnsupportedType(envVar, qualifiedName, field.Type().String())
	}

	return nil
}

// --- Helpers ---

func parseTag(tag string) (name string, inline bool) {
	if tag == "" {
		return "", false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, p := range parts[1:] {
		if p == "inline" {
			inline = true
		}
	}
	return name, inline
}

func toScreamingSnake(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
}

func copyPath(src []string) []string {
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func isScalar(t reflect.Type) bool {
	if t == nil {
		return false
	}
	if t.Kind() == reflect.Ptr {
		return isScalar(t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct, reflect.Interface, reflect.Array, reflect.Slice, reflect.Map:
		return false
	default:
		return true
	}
}
