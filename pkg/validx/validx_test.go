package validx

import (
	"errors"
	"testing"

	"github.com/aasyanov/urx/pkg/errx"
)

func assertNil(t *testing.T, err *errx.Error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func assertCode(t *testing.T, err *errx.Error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", code)
	}
	if err.Code != code {
		t.Fatalf("expected code %s, got %s", code, err.Code)
	}
	if err.Domain != DomainValidation {
		t.Fatalf("expected domain %s, got %s", DomainValidation, err.Domain)
	}
}

// ============================================================
// Required
// ============================================================

func TestRequired_Valid(t *testing.T) {
	assertNil(t, Required("name", "Alice"))
}

func TestRequired_Empty(t *testing.T) {
	assertCode(t, Required("name", ""), CodeRequired)
}

func TestRequired_Whitespace(t *testing.T) {
	assertCode(t, Required("name", "   "), CodeRequired)
}

// ============================================================
// MinLen
// ============================================================

func TestMinLen_Valid(t *testing.T) {
	assertNil(t, MinLen("pass", "12345678", 8))
}

func TestMinLen_TooShort(t *testing.T) {
	assertCode(t, MinLen("pass", "short", 8), CodeTooShort)
}

func TestMinLen_Unicode(t *testing.T) {
	assertNil(t, MinLen("name", "Привет", 3))
}

// ============================================================
// MaxLen
// ============================================================

func TestMaxLen_Valid(t *testing.T) {
	assertNil(t, MaxLen("bio", "hello", 100))
}

func TestMaxLen_TooLong(t *testing.T) {
	assertCode(t, MaxLen("bio", "abcdef", 3), CodeTooLong)
}

// ============================================================
// Between
// ============================================================

func TestBetween_Valid(t *testing.T) {
	assertNil(t, Between("age", 25, 18, 65))
}

func TestBetween_TooLow(t *testing.T) {
	assertCode(t, Between("age", 10, 18, 65), CodeOutOfRange)
}

func TestBetween_TooHigh(t *testing.T) {
	assertCode(t, Between("age", 100, 18, 65), CodeOutOfRange)
}

func TestBetween_Boundary(t *testing.T) {
	assertNil(t, Between("age", 18, 18, 65))
	assertNil(t, Between("age", 65, 18, 65))
}

// ============================================================
// Match
// ============================================================

func TestMatch_Valid(t *testing.T) {
	assertNil(t, Match("code", "ABC-123", `^[A-Z]+-\d+$`))
}

func TestMatch_Invalid(t *testing.T) {
	assertCode(t, Match("code", "abc", `^[A-Z]+$`), CodeInvalidFormat)
}

func TestMatch_BadPattern(t *testing.T) {
	assertCode(t, Match("code", "abc", `[invalid`), CodeInvalidFormat)
}

// ============================================================
// OneOf
// ============================================================

func TestOneOf_Valid(t *testing.T) {
	assertNil(t, OneOf("role", "admin", []string{"admin", "user", "guest"}))
}

func TestOneOf_Invalid(t *testing.T) {
	assertCode(t, OneOf("role", "root", []string{"admin", "user"}), CodeInvalidValue)
}

// ============================================================
// Email
// ============================================================

func TestEmail_Valid(t *testing.T) {
	assertNil(t, Email("email", "user@example.com"))
	assertNil(t, Email("email", "user+tag@sub.example.co.uk"))
}

func TestEmail_Invalid(t *testing.T) {
	assertCode(t, Email("email", "notanemail"), CodeInvalidFormat)
	assertCode(t, Email("email", "@example.com"), CodeInvalidFormat)
	assertCode(t, Email("email", "user@"), CodeInvalidFormat)
}

// ============================================================
// URL
// ============================================================

func TestURL_Valid(t *testing.T) {
	assertNil(t, URL("website", "https://example.com"))
	assertNil(t, URL("website", "http://localhost:8080/path"))
}

func TestURL_Invalid(t *testing.T) {
	assertCode(t, URL("website", "not-a-url"), CodeInvalidFormat)
	assertCode(t, URL("website", "ftp://"), CodeInvalidFormat)
}

// ============================================================
// Collect
// ============================================================

func TestCollect_AllValid(t *testing.T) {
	err := Collect(
		Required("name", "Alice"),
		MinLen("pass", "12345678", 8),
	)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCollect_SomeInvalid(t *testing.T) {
	err := Collect(
		Required("name", ""),
		MinLen("pass", "short", 8),
		Required("email", "ok@ok.com"),
	)
	if err == nil {
		t.Fatal("expected error")
	}
	var me *errx.MultiError
	if !errors.As(err, &me) {
		t.Fatalf("expected *errx.MultiError, got %T", err)
	}
	if me.Len() != 2 {
		t.Fatalf("expected 2 errors, got %d", me.Len())
	}
}

func TestCollect_Empty(t *testing.T) {
	err := Collect()
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// ============================================================
// Meta
// ============================================================

func TestMeta_FieldPresent(t *testing.T) {
	e := Required("username", "")
	if e == nil {
		t.Fatal("expected error")
	}
	if e.Meta["field"] != "username" {
		t.Fatalf("expected meta[field]=username, got %v", e.Meta["field"])
	}
}
