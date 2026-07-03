package envx

import (
	"fmt"
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
// uint, float64, time.Duration, time.Time (RFC3339), and []string
// (comma-separated).
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
		v, err := strconv.ParseUint(raw, base10, bitSize64)
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
		return zero, fmt.Sprintf("unsupported type %T", zero)
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
