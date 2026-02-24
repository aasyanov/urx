// Package validx provides composable, pure-function field validators that
// return [*errx.Error] with domain VALIDATION.
//
// No reflection, no struct tags. Callers compose validators explicitly:
//
//	if err := validx.Collect(
//	    validx.Required("email", req.Email),
//	    validx.Email("email", req.Email),
//	    validx.MinLen("password", req.Password, 8),
//	); err != nil {
//	    // err is *errx.MultiError with all validation failures
//	}
package validx

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/aasyanov/urx/pkg/errx"
)

// Required checks that value is non-empty (after trimming whitespace).
func Required(field, value string) *errx.Error {
	if strings.TrimSpace(value) == "" {
		return errRequired(field)
	}
	return nil
}

// MinLen checks that value has at least min runes.
func MinLen(field, value string, min int) *errx.Error {
	if len([]rune(value)) < min {
		return errTooShort(field, min)
	}
	return nil
}

// MaxLen checks that value has at most max runes.
func MaxLen(field, value string, max int) *errx.Error {
	if len([]rune(value)) > max {
		return errTooLong(field, max)
	}
	return nil
}

// Between checks that value is within [min, max] inclusive.
func Between(field string, value, min, max int) *errx.Error {
	if value < min || value > max {
		return errOutOfRange(field, min, max)
	}
	return nil
}

// Match checks that value matches the given regular expression pattern.
func Match(field, value, pattern string) *errx.Error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return errInvalidFormat(field, pattern)
	}
	if !re.MatchString(value) {
		return errInvalidFormat(field, pattern)
	}
	return nil
}

// OneOf checks that value is one of the allowed values.
func OneOf(field, value string, allowed []string) *errx.Error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return errInvalidValue(field, allowed)
}

// emailRe is a pragmatic email regex: local@domain with at least one dot
// in the domain part. Not RFC 5322 compliant, but covers real-world usage.
var emailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// Email checks that value looks like a valid email address.
func Email(field, value string) *errx.Error {
	if !emailRe.MatchString(value) {
		return errInvalidFormat(field, "email")
	}
	return nil
}

// URL checks that value is a valid absolute URL with a scheme.
func URL(field, value string) *errx.Error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errInvalidFormat(field, "url")
	}
	return nil
}

// Collect gathers all non-nil validation errors into a [*errx.MultiError].
// Returns nil if all validations passed.
func Collect(errs ...*errx.Error) error {
	var collected []error
	for _, e := range errs {
		if e != nil {
			collected = append(collected, e)
		}
	}
	if len(collected) == 0 {
		return nil
	}
	return errx.NewMulti(collected...)
}
