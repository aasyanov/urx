package envx

import (
	"encoding"
	"iter"
	"reflect"
	"strings"
)

// tag names and walk punctuation. keySeparator is defined in options.go.
const (
	tagEnv    = "env"
	tagYAML   = "yaml"
	tagJSON   = "json"
	tagTOML   = "toml"
	tagIgnore = "-"
	tagInline = "inline"
	pathDot   = "."
	kebabDash = "-"
)

type keySource uint8

const (
	keysFromEnv keySource = iota
	keysFromYAML
	keysFromJSON
	keysFromTOML
)

type walkConfig struct {
	source keySource
}

// Field is one bindable leaf yielded by [Walk]. Ptr is always a non-nil
// pointer to the field (*T). Walk never writes through Ptr; the caller
// decides whether to [BindField].
type Field struct {
	Key  string // relative name before prefix: "SERVER_PORT"
	Path string // Go path: "Server.Port"
	Ptr  any    // *T on the field; never nil for a yielded field
}

// WalkOption configures [Walk]. The default key source is the `env` struct
// tag (allowlist): fields without `env` are skipped.
type WalkOption func(*walkConfig)

// KeysFromEnvTag makes [Walk] yield only fields tagged `env:"NAME"` or
// structs tagged `env:",inline"`. This is the default.
func KeysFromEnvTag() WalkOption {
	return func(c *walkConfig) { c.source = keysFromEnv }
}

// KeysFromYAML derives relative keys from `yaml` tags the way yamlio does:
// tag name, kebab-case to `_`, UPPER, `,inline` flatten. Opt-in for YAML
// product configs; not the library default (cfgx also loads JSON/TOML).
func KeysFromYAML() WalkOption {
	return func(c *walkConfig) { c.source = keysFromYAML }
}

// KeysFromJSON derives relative keys from `json` tags (UPPER, kebab to `_`,
// inline flatten). Empty tags fall back to the Go field name.
func KeysFromJSON() WalkOption {
	return func(c *walkConfig) { c.source = keysFromJSON }
}

// KeysFromTOML derives relative keys from `toml` tags (UPPER, kebab to `_`,
// inline flatten). Empty tags fall back to the Go field name.
func KeysFromTOML() WalkOption {
	return func(c *walkConfig) { c.source = keysFromTOML }
}

// Walk yields bindable exported leaves of dst. It does not read the
// environment and does not mutate dst: a nil pointer field is skipped, not
// allocated. dst must be a non-nil pointer to struct — a programming error
// otherwise, matching [BindTo].
//
// Default key source is [KeysFromEnvTag] (allowlist). Slice and map fields
// are not descended except a leaf []string (or string-element slice).
// Two fields that alias the same pointer are walked once: the second path
// is skipped (cycle / diamond termination). An interface holding a
// non-pointer struct value yields no leaves: the boxed value is not
// addressable, and Walk does not allocate a copy ([BindField] would write
// the copy, not the original). Named types convertible to [time.Time] are
// not Walk leaves (*T does not implement [encoding.TextUnmarshaler]); use
// [Bind]/[BindTo].
func Walk(dst any, opts ...WalkOption) iter.Seq[Field] {
	return func(yield func(Field) bool) {
		cfg := walkConfig{source: keysFromEnv}
		for _, o := range opts {
			if o != nil {
				o(&cfg)
			}
		}
		rv := reflect.ValueOf(dst)
		if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
			panic("envx: Walk dst must be a non-nil pointer to struct")
		}
		walkValue(rv, "", "", &cfg, make(map[uintptr]bool), yield)
	}
}

func walkValue(v reflect.Value, keyPrefix, goPath string, cfg *walkConfig, visited map[uintptr]bool, yield func(Field) bool) bool {
	v, ok := derefWalk(v, visited)
	if !ok {
		return true
	}
	if v.Kind() != reflect.Struct {
		return true
	}

	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, skip, inline := walkFieldMeta(sf, cfg.source)
		if skip {
			continue
		}
		fv := v.Field(i)
		childKey := keyPrefix
		if !inline {
			childKey = joinEnvKey(keyPrefix, name)
		}
		childPath := joinGoPath(goPath, sf.Name)

		if isBindableLeaf(sf.Type) {
			ptr, ok := leafPtr(fv)
			if !ok {
				continue
			}
			if !yield(Field{Key: childKey, Path: childPath, Ptr: ptr}) {
				return false
			}
			continue
		}
		if isStructOrPtrToStruct(sf.Type) || sf.Type.Kind() == reflect.Interface {
			if !walkValue(fv, childKey, childPath, cfg, visited, yield) {
				return false
			}
		}
	}
	return true
}

// derefWalk follows pointers/interfaces. visited records pointer identity so
// cycles terminate; a diamond alias (two fields, one struct) is skipped on
// the second path.
func derefWalk(v reflect.Value, visited map[uintptr]bool) (reflect.Value, bool) {
	for {
		if !v.IsValid() {
			return v, false
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return v, false
			}
			if v.Kind() == reflect.Pointer {
				ptr := v.Pointer()
				if visited[ptr] {
					return v, false
				}
				visited[ptr] = true
			}
			v = v.Elem()
		default:
			return v, true
		}
	}
}

func walkFieldMeta(sf reflect.StructField, source keySource) (name string, skip, inline bool) {
	tag := sf.Tag.Get(tagKey(source))
	if source == keysFromEnv && tag == "" {
		return "", true, false
	}
	if tag == tagIgnore {
		return "", true, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, p := range parts[1:] {
		if p == tagInline {
			inline = true
		}
	}
	if name == tagIgnore {
		return "", true, false
	}
	if inline && name == "" {
		return "", false, true
	}
	if name == "" {
		if source == keysFromYAML && !sf.Anonymous {
			return "", true, false
		}
		if sf.Anonymous {
			return "", false, true
		}
		name = sf.Name
	}
	if source == keysFromEnv {
		return strings.ToUpper(name), false, inline
	}
	return strings.ToUpper(strings.ReplaceAll(name, kebabDash, keySeparator)), false, inline
}

func tagKey(source keySource) string {
	switch source {
	case keysFromYAML:
		return tagYAML
	case keysFromJSON:
		return tagJSON
	case keysFromTOML:
		return tagTOML
	default:
		return tagEnv
	}
}

func joinEnvKey(parent, seg string) string {
	if seg == "" {
		return parent
	}
	if parent == "" {
		return seg
	}
	return parent + keySeparator + seg
}

func joinGoPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + pathDot + name
}

func isStructOrPtrToStruct(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}

func isBindableLeaf(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	case reflect.Slice:
		return t.Elem().Kind() == reflect.String
	}
	return false
}

func leafPtr(fv reflect.Value) (any, bool) {
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			return nil, false
		}
		return fv.Interface(), true
	}
	if !fv.CanAddr() {
		return nil, false
	}
	return fv.Addr().Interface(), true
}

var (
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)
