package envx

import (
	"encoding"
	"fmt"
	"math/bits"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// bitSize32 and bitSize64 select the integer/float width for strconv.
const (
	bitSize32 = 32
	bitSize64 = 64
	base10    = 10
)

// timeLayout is the timestamp format for [time.Time] bindings, matching clix.
const timeLayout = time.RFC3339

// parse converts raw into type T, returning a non-empty diagnostic string on
// failure (kept as a string rather than an error so [Var] stays allocation-
// light and comparable). Supported types: string, bool, int, int32, int64,
// uint, float64, exact [time.Duration] (ParseDuration, unit required),
// [time.Time] and named types convertible to it (RFC3339), []string
// (comma-separated), defined types whose underlying kind is a supported
// builtin (named int64, including type MyDur time.Duration, parse as
// integers only — "5s" is invalid), and types whose pointer implements
// [encoding.TextUnmarshaler].
func parse[T any](raw string) (T, string) {
	var zero T
	switch any(zero).(type) {
	case string:
		return any(raw).(T), ""
	case bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return zero, fmt.Sprintf("expected bool: %s", raw)
		}
		return any(v).(T), ""
	case int:
		v, err := strconv.Atoi(raw)
		if err != nil {
			return zero, fmt.Sprintf("expected int: %s", raw)
		}
		return any(v).(T), ""
	case int32:
		v, err := strconv.ParseInt(raw, base10, bitSize32)
		if err != nil {
			return zero, fmt.Sprintf("expected int32: %s", raw)
		}
		return any(int32(v)).(T), ""
	case int64:
		v, err := strconv.ParseInt(raw, base10, bitSize64)
		if err != nil {
			return zero, fmt.Sprintf("expected int64: %s", raw)
		}
		return any(v).(T), ""
	case uint:
		v, err := strconv.ParseUint(raw, base10, bits.UintSize)
		if err != nil {
			return zero, fmt.Sprintf("expected uint: %s", raw)
		}
		return any(uint(v)).(T), ""
	case float64:
		v, err := strconv.ParseFloat(raw, bitSize64)
		if err != nil {
			return zero, fmt.Sprintf("expected float64: %s", raw)
		}
		return any(v).(T), ""
	case time.Duration:
		v, err := time.ParseDuration(raw)
		if err != nil {
			return zero, fmt.Sprintf("expected duration: %s", raw)
		}
		return any(v).(T), ""
	case time.Time:
		v, err := time.Parse(timeLayout, raw)
		if err != nil {
			return zero, fmt.Sprintf("expected time (RFC3339): %s", raw)
		}
		return any(v).(T), ""
	case []string:
		return any(parseList(raw)).(T), ""
	default:
		diag := parseInto(&zero, raw)
		return zero, diag
	}
}

// parseInto writes a successful parse of raw into ptr (*T). On failure it
// leaves the pointed-to value unchanged and returns a diagnostic. Reflect
// and [encoding.TextUnmarshaler] live here so the exact type-switch in
// [parse] stays allocation-free for builtin T.
func parseInto(ptr any, raw string) string {
	rv := reflect.ValueOf(ptr)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return "unsupported type"
	}
	elem := rv.Elem()

	tmp := reflect.New(elem.Type())
	tmp.Elem().Set(elem)

	if u, ok := tmp.Interface().(encoding.TextUnmarshaler); ok {
		if err := u.UnmarshalText([]byte(raw)); err != nil {
			return err.Error()
		}
		elem.Set(tmp.Elem())
		return ""
	}

	if diag := setFromString(tmp.Elem(), raw); diag != "" {
		return diag
	}
	elem.Set(tmp.Elem())
	return ""
}

// durationType is the exact [time.Duration] identity. Walk/BindField must
// parse it with ParseDuration only — the same rule as [parse]'s type-switch
// and clix — never as a raw int64 (which would accept "90" as 90ns).
var durationType = reflect.TypeOf(time.Duration(0))

// timeType is the exact [time.Time] identity. Named types convertible to
// time.Time (type Stamp time.Time) do not inherit Time's UnmarshalText;
// setFromString parses RFC3339 then Convert.
var timeType = reflect.TypeOf(time.Time{})

// setFromString assigns raw onto elem by underlying kind. Struct, map, and
// non-string slices are unsupported (except []string / string-element slices
// and named types convertible to [time.Time]). Named int64 — including
// type MyDur time.Duration — is ParseInt only; there is no ParseDuration
// fallback (that would turn ID=5s into 5e9).
func setFromString(elem reflect.Value, raw string) string {
	if elem.Type() == durationType {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Sprintf("expected duration: %s", raw)
		}
		elem.SetInt(int64(d))
		return ""
	}
	if elem.Kind() == reflect.Struct && elem.Type().ConvertibleTo(timeType) {
		tm, err := time.Parse(timeLayout, raw)
		if err != nil {
			return fmt.Sprintf("expected time (RFC3339): %s", raw)
		}
		elem.Set(reflect.ValueOf(tm).Convert(elem.Type()))
		return ""
	}
	switch elem.Kind() {
	case reflect.String:
		elem.SetString(raw)
		return ""
	case reflect.Bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Sprintf("expected bool: %s", raw)
		}
		elem.SetBool(v)
		return ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(raw, base10, elem.Type().Bits())
		if err != nil {
			return fmt.Sprintf("expected %s: %s", elem.Kind(), raw)
		}
		elem.SetInt(v)
		return ""
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(raw, base10, elem.Type().Bits())
		if err != nil {
			return fmt.Sprintf("expected %s: %s", elem.Kind(), raw)
		}
		elem.SetUint(v)
		return ""
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(raw, elem.Type().Bits())
		if err != nil {
			return fmt.Sprintf("expected %s: %s", elem.Kind(), raw)
		}
		elem.SetFloat(v)
		return ""
	case reflect.Slice:
		if elem.Type().Elem().Kind() != reflect.String {
			return fmt.Sprintf("unsupported type %s", elem.Type())
		}
		list := parseList(raw)
		sl := reflect.MakeSlice(elem.Type(), len(list), len(list))
		for i, s := range list {
			sl.Index(i).SetString(s)
		}
		elem.Set(sl)
		return ""
	default:
		return fmt.Sprintf("unsupported type %s", elem.Type())
	}
}

// parseList splits a comma-separated value into trimmed, non-empty elements.
// An empty or whitespace-only raw string yields an empty (non-nil) slice.
func parseList(raw string) []string {
	parts := strings.Split(raw, listSeparator)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
