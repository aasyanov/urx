package validx

import (
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// ============================================================
// Nil pointer safety
// ============================================================

func assertNilPointer(t *testing.T, err *errx.Error, fn string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected CodeNilPointer error, got nil", fn)
	}
	if err.Code != CodeNilPointer {
		t.Fatalf("%s: expected code %s, got %s", fn, CodeNilPointer, err.Code)
	}
}

func TestClamp_NilPointer(t *testing.T) {
	assertNilPointer(t, Clamp[int]("x", nil, 0, 100), "Clamp")
}

func TestClampTime_NilPointer(t *testing.T) {
	now := time.Now()
	assertNilPointer(t, ClampTime("x", nil, now, now.Add(time.Hour)), "ClampTime")
}

func TestDefault_NilPointer(t *testing.T) {
	assertNilPointer(t, Default[int]("x", nil, 42), "Default")
}

func TestDefaultStr_NilPointer(t *testing.T) {
	assertNilPointer(t, DefaultStr("x", nil, "hello"), "DefaultStr")
}

func TestDefaultTime_NilPointer(t *testing.T) {
	assertNilPointer(t, DefaultTime("x", nil, time.Now()), "DefaultTime")
}

func TestDefaultOneOf_NilPointer(t *testing.T) {
	assertNilPointer(t, DefaultOneOf[string]("x", nil, []string{"a"}, "a"), "DefaultOneOf")
}

func assertFixed(t *testing.T, err *errx.Error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected CodeFixed error, got nil")
	}
	if err.Code != CodeFixed {
		t.Fatalf("expected code %s, got %s", CodeFixed, err.Code)
	}
	if err.Severity != errx.SeverityInfo {
		t.Fatalf("expected SeverityInfo, got %v", err.Severity)
	}
	if err.Meta["fixed"] != true {
		t.Fatalf("expected meta[fixed]=true, got %v", err.Meta["fixed"])
	}
}

// ============================================================
// Clamp
// ============================================================

func TestClamp_Int_InRange(t *testing.T) {
	v := 50
	assertNil(t, Clamp("port", &v, 1, 100))
	if v != 50 {
		t.Fatalf("expected 50, got %d", v)
	}
}

func TestClamp_Int_BelowMin(t *testing.T) {
	v := -5
	err := Clamp("port", &v, 0, 65535)
	assertFixed(t, err)
	if v != 0 {
		t.Fatalf("expected clamped to 0, got %d", v)
	}
	if err.Meta["from"] != -5 {
		t.Fatalf("expected from=-5, got %v", err.Meta["from"])
	}
}

func TestClamp_Int_AboveMax(t *testing.T) {
	v := 99999
	err := Clamp("port", &v, 0, 65535)
	assertFixed(t, err)
	if v != 65535 {
		t.Fatalf("expected clamped to 65535, got %d", v)
	}
}

func TestClamp_Int_Boundary(t *testing.T) {
	v := 0
	assertNil(t, Clamp("x", &v, 0, 100))
	v = 100
	assertNil(t, Clamp("x", &v, 0, 100))
}

func TestClamp_Float(t *testing.T) {
	v := 0.001
	err := Clamp("rate", &v, 0.1, 100.0)
	assertFixed(t, err)
	if v != 0.1 {
		t.Fatalf("expected 0.1, got %f", v)
	}
}

func TestClamp_Float_AboveMax(t *testing.T) {
	v := 999.9
	err := Clamp("rate", &v, 0.1, 100.0)
	assertFixed(t, err)
	if v != 100.0 {
		t.Fatalf("expected 100.0, got %f", v)
	}
}

func TestClamp_String(t *testing.T) {
	v := "aaa"
	assertNil(t, Clamp("level", &v, "aaa", "zzz"))
	v = "zzz"
	assertNil(t, Clamp("level", &v, "aaa", "zzz"))
}

// ============================================================
// Clamp — time.Duration
// ============================================================

func TestClamp_Duration_InRange(t *testing.T) {
	v := 5 * time.Second
	assertNil(t, Clamp("timeout", &v, time.Second, time.Minute))
	if v != 5*time.Second {
		t.Fatalf("expected 5s, got %v", v)
	}
}

func TestClamp_Duration_BelowMin(t *testing.T) {
	v := 100 * time.Millisecond
	err := Clamp("timeout", &v, time.Second, time.Minute)
	assertFixed(t, err)
	if v != time.Second {
		t.Fatalf("expected 1s, got %v", v)
	}
}

func TestClamp_Duration_AboveMax(t *testing.T) {
	v := 10 * time.Minute
	err := Clamp("timeout", &v, time.Second, time.Minute)
	assertFixed(t, err)
	if v != time.Minute {
		t.Fatalf("expected 1m, got %v", v)
	}
}

// ============================================================
// ClampTime
// ============================================================

func TestClampTime_InRange(t *testing.T) {
	now := time.Now()
	min := now.Add(-time.Hour)
	max := now.Add(time.Hour)
	v := now
	assertNil(t, ClampTime("ts", &v, min, max))
	if !v.Equal(now) {
		t.Fatal("value should not change")
	}
}

func TestClampTime_BeforeMin(t *testing.T) {
	now := time.Now()
	min := now.Add(-time.Hour)
	max := now.Add(time.Hour)
	v := now.Add(-2 * time.Hour)
	err := ClampTime("ts", &v, min, max)
	assertFixed(t, err)
	if !v.Equal(min) {
		t.Fatalf("expected clamped to min, got %v", v)
	}
}

func TestClampTime_AfterMax(t *testing.T) {
	now := time.Now()
	min := now.Add(-time.Hour)
	max := now.Add(time.Hour)
	v := now.Add(2 * time.Hour)
	err := ClampTime("ts", &v, min, max)
	assertFixed(t, err)
	if !v.Equal(max) {
		t.Fatalf("expected clamped to max, got %v", v)
	}
}

func TestClampTime_Boundary(t *testing.T) {
	now := time.Now()
	min := now
	max := now.Add(time.Hour)
	v := now
	assertNil(t, ClampTime("ts", &v, min, max))
	v = max
	assertNil(t, ClampTime("ts", &v, min, max))
}

// ============================================================
// BetweenTime
// ============================================================

func TestBetweenTime_InRange(t *testing.T) {
	now := time.Now()
	assertNil(t, BetweenTime("ts", now, now.Add(-time.Hour), now.Add(time.Hour)))
}

func TestBetweenTime_BeforeMin(t *testing.T) {
	now := time.Now()
	assertCode(t, BetweenTime("ts", now.Add(-2*time.Hour), now.Add(-time.Hour), now), CodeOutOfRange)
}

func TestBetweenTime_AfterMax(t *testing.T) {
	now := time.Now()
	assertCode(t, BetweenTime("ts", now.Add(2*time.Hour), now, now.Add(time.Hour)), CodeOutOfRange)
}

func TestBetweenTime_Boundary(t *testing.T) {
	now := time.Now()
	max := now.Add(time.Hour)
	assertNil(t, BetweenTime("ts", now, now, max))
	assertNil(t, BetweenTime("ts", max, now, max))
}

// ============================================================
// Default
// ============================================================

func TestDefault_NonZero(t *testing.T) {
	v := 42
	assertNil(t, Default("retries", &v, 3))
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
}

func TestDefault_Zero(t *testing.T) {
	v := 0
	err := Default("retries", &v, 3)
	assertFixed(t, err)
	if v != 3 {
		t.Fatalf("expected 3, got %d", v)
	}
}

func TestDefault_Duration(t *testing.T) {
	var d time.Duration
	err := Default("timeout", &d, 30*time.Second)
	assertFixed(t, err)
	if d != 30*time.Second {
		t.Fatalf("expected 30s, got %v", d)
	}
}

func TestDefault_Duration_NonZero(t *testing.T) {
	d := 5 * time.Second
	assertNil(t, Default("timeout", &d, 30*time.Second))
	if d != 5*time.Second {
		t.Fatalf("expected 5s, got %v", d)
	}
}

func TestDefault_Bool(t *testing.T) {
	v := false
	err := Default("verbose", &v, true)
	assertFixed(t, err)
	if !v {
		t.Fatal("expected true")
	}
}

func TestDefault_String(t *testing.T) {
	v := ""
	err := Default("env", &v, "production")
	assertFixed(t, err)
	if v != "production" {
		t.Fatalf("expected production, got %s", v)
	}
}

// ============================================================
// DefaultStr
// ============================================================

func TestDefaultStr_NonEmpty(t *testing.T) {
	v := "staging"
	assertNil(t, DefaultStr("env", &v, "production"))
	if v != "staging" {
		t.Fatalf("expected staging, got %s", v)
	}
}

func TestDefaultStr_Empty(t *testing.T) {
	v := ""
	err := DefaultStr("env", &v, "production")
	assertFixed(t, err)
	if v != "production" {
		t.Fatalf("expected production, got %s", v)
	}
}

func TestDefaultStr_Whitespace(t *testing.T) {
	v := "   "
	err := DefaultStr("env", &v, "production")
	assertFixed(t, err)
	if v != "production" {
		t.Fatalf("expected production, got %s", v)
	}
}

// ============================================================
// DefaultTime
// ============================================================

func TestDefaultTime_NonZero(t *testing.T) {
	now := time.Now()
	v := now
	assertNil(t, DefaultTime("created", &v, time.Unix(0, 0)))
	if !v.Equal(now) {
		t.Fatal("value should not change")
	}
}

func TestDefaultTime_Zero(t *testing.T) {
	var v time.Time
	def := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	err := DefaultTime("created", &v, def)
	assertFixed(t, err)
	if !v.Equal(def) {
		t.Fatalf("expected %v, got %v", def, v)
	}
}

// ============================================================
// DefaultOneOf
// ============================================================

func TestDefaultOneOf_Valid(t *testing.T) {
	v := "warn"
	assertNil(t, DefaultOneOf("level", &v, []string{"debug", "info", "warn", "error"}, "info"))
	if v != "warn" {
		t.Fatalf("expected warn, got %s", v)
	}
}

func TestDefaultOneOf_Invalid(t *testing.T) {
	v := "trace"
	err := DefaultOneOf("level", &v, []string{"debug", "info", "warn", "error"}, "info")
	assertFixed(t, err)
	if v != "info" {
		t.Fatalf("expected info, got %s", v)
	}
}

func TestDefaultOneOf_Int(t *testing.T) {
	v := 99
	err := DefaultOneOf("priority", &v, []int{1, 2, 3, 4, 5}, 3)
	assertFixed(t, err)
	if v != 3 {
		t.Fatalf("expected 3, got %d", v)
	}
}

func TestDefaultOneOf_AlreadyDefault(t *testing.T) {
	v := "info"
	assertNil(t, DefaultOneOf("level", &v, []string{"debug", "info", "warn"}, "info"))
}

// ============================================================
// Fix + Collect integration
// ============================================================

func TestFix_Collect_Integration(t *testing.T) {
	port := -1
	timeout := time.Duration(0)
	env := "  "
	level := "trace"

	err := Collect(
		Clamp("port", &port, 1024, 65535),
		Default("timeout", &timeout, 30*time.Second),
		DefaultStr("env", &env, "production"),
		DefaultOneOf("level", &level, []string{"debug", "info", "warn", "error"}, "info"),
	)

	if port != 1024 {
		t.Fatalf("expected port=1024, got %d", port)
	}
	if timeout != 30*time.Second {
		t.Fatalf("expected timeout=30s, got %v", timeout)
	}
	if env != "production" {
		t.Fatalf("expected env=production, got %s", env)
	}
	if level != "info" {
		t.Fatalf("expected level=info, got %s", level)
	}

	if err == nil {
		t.Fatal("expected MultiError with fix notices")
	}
	me, ok := err.(*errx.MultiError)
	if !ok {
		t.Fatalf("expected *errx.MultiError, got %T", err)
	}
	if me.Len() != 4 {
		t.Fatalf("expected 4 fix notices, got %d", me.Len())
	}
	if me.Severity() != errx.SeverityInfo {
		t.Fatalf("expected SeverityInfo, got %v", me.Severity())
	}
}

func TestFix_Collect_MixedWithValidation(t *testing.T) {
	port := -1
	email := "invalid"

	err := Collect(
		Clamp("port", &port, 1024, 65535),
		Email("email", email),
	)

	if port != 1024 {
		t.Fatalf("expected port=1024, got %d", port)
	}
	if err == nil {
		t.Fatal("expected error")
	}
	me := err.(*errx.MultiError)
	if me.Len() != 2 {
		t.Fatalf("expected 2 errors, got %d", me.Len())
	}
}

// ============================================================
// Meta on fix errors
// ============================================================

func TestFix_MetaField(t *testing.T) {
	v := -1
	e := Clamp("port", &v, 0, 100)
	if e == nil {
		t.Fatal("expected error")
	}
	if e.Meta["field"] != "port" {
		t.Fatalf("expected field=port, got %v", e.Meta["field"])
	}
	if e.Meta["from"] != -1 {
		t.Fatalf("expected from=-1, got %v", e.Meta["from"])
	}
	if e.Meta["to"] != 0 {
		t.Fatalf("expected to=0, got %v", e.Meta["to"])
	}
}
