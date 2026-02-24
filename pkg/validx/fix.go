package validx

import (
	"cmp"
	"strings"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// CodeNilPointer indicates a nil pointer was passed to a fix function.
const CodeNilPointer = "NIL_POINTER"

func errNilPointer(field, fn string) *errx.Error {
	return errx.New(DomainValidation, CodeNilPointer, field+" is nil",
		errx.WithMeta("field", field, "function", fn),
	)
}

// Clamp ensures *val is within [min, max]. If out of range, *val is clamped
// to the nearest bound and an informational [CodeFixed] error is returned.
// Returns nil if *val is already within range.
//
//	validx.Clamp("port", &cfg.Port, 1024, 65535)
//	validx.Clamp("rate", &cfg.Rate, 0.1, 100.0)
func Clamp[T cmp.Ordered](field string, val *T, min, max T) *errx.Error {
	if val == nil {
		return errNilPointer(field, "Clamp")
	}
	orig := *val
	if *val < min {
		*val = min
		return errFixed(field, orig, *val)
	}
	if *val > max {
		*val = max
		return errFixed(field, orig, *val)
	}
	return nil
}

// ClampTime ensures *val is within [min, max]. If out of range, *val is
// clamped to the nearest bound and an informational [CodeFixed] error is
// returned. Returns nil if *val is already within range.
//
//	validx.ClampTime("start", &cfg.Start, earliest, latest)
func ClampTime(field string, val *time.Time, min, max time.Time) *errx.Error {
	if val == nil {
		return errNilPointer(field, "ClampTime")
	}
	orig := *val
	if val.Before(min) {
		*val = min
		return errFixed(field, orig, *val)
	}
	if val.After(max) {
		*val = max
		return errFixed(field, orig, *val)
	}
	return nil
}

// BetweenTime checks that t is within [min, max] inclusive. Returns
// [CodeOutOfRange] if out of range, nil otherwise. This is the reject-only
// counterpart to [ClampTime].
func BetweenTime(field string, t, min, max time.Time) *errx.Error {
	if t.Before(min) || t.After(max) {
		return errOutOfRangeTime(field, min, max)
	}
	return nil
}

// Default sets *val to def if *val is the zero value for its type.
// Returns an informational [CodeFixed] error when defaulted, nil if
// *val was already non-zero.
//
//	validx.Default("timeout", &cfg.Timeout, 30*time.Second)
//	validx.Default("retries", &cfg.Retries, 3)
func Default[T comparable](field string, val *T, def T) *errx.Error {
	if val == nil {
		return errNilPointer(field, "Default")
	}
	var zero T
	if *val == zero {
		orig := *val
		*val = def
		return errFixed(field, orig, *val)
	}
	return nil
}

// DefaultStr sets *val to def if *val is empty or whitespace-only.
// Returns an informational [CodeFixed] error when defaulted, nil if
// *val was already non-empty. Mirrors [Required] semantics.
//
//	validx.DefaultStr("env", &cfg.Env, "production")
func DefaultStr(field string, val *string, def string) *errx.Error {
	if val == nil {
		return errNilPointer(field, "DefaultStr")
	}
	if strings.TrimSpace(*val) == "" {
		orig := *val
		*val = def
		return errFixed(field, orig, *val)
	}
	return nil
}

// DefaultTime sets *val to def if *val is the zero time.
// Returns an informational [CodeFixed] error when defaulted, nil if
// *val was already set.
//
//	validx.DefaultTime("created_at", &cfg.CreatedAt, time.Now())
func DefaultTime(field string, val *time.Time, def time.Time) *errx.Error {
	if val == nil {
		return errNilPointer(field, "DefaultTime")
	}
	if val.IsZero() {
		orig := *val
		*val = def
		return errFixed(field, orig, *val)
	}
	return nil
}

// DefaultOneOf sets *val to def if *val is not in the allowed set.
// Returns an informational [CodeFixed] error when defaulted, nil if
// *val was already valid.
//
//	validx.DefaultOneOf("level", &cfg.Level, []string{"debug","info","warn","error"}, "info")
func DefaultOneOf[T comparable](field string, val *T, allowed []T, def T) *errx.Error {
	if val == nil {
		return errNilPointer(field, "DefaultOneOf")
	}
	for _, a := range allowed {
		if *val == a {
			return nil
		}
	}
	orig := *val
	*val = def
	return errFixed(field, orig, *val)
}
