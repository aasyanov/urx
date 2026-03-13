package envx

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

func lookup(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

// --- Bind defaults ---

func TestBind_StringDefault(t *testing.T) {
	env := New(WithLookup(lookup(nil)))
	v := Bind(env, "HOST", "localhost")
	if v.Value() != "localhost" {
		t.Fatalf("got %q, want %q", v.Value(), "localhost")
	}
	if v.Found() {
		t.Fatal("should not be found")
	}
}

func TestBind_IntDefault(t *testing.T) {
	env := New(WithLookup(lookup(nil)))
	v := Bind(env, "PORT", 8080)
	if v.Value() != 8080 {
		t.Fatalf("got %d, want %d", v.Value(), 8080)
	}
}

func TestBind_BoolDefault(t *testing.T) {
	env := New(WithLookup(lookup(nil)))
	v := Bind(env, "DEBUG", false)
	if v.Value() != false {
		t.Fatal("expected false")
	}
}

func TestBind_DurationDefault(t *testing.T) {
	env := New(WithLookup(lookup(nil)))
	v := Bind(env, "TIMEOUT", 5*time.Second)
	if v.Value() != 5*time.Second {
		t.Fatalf("got %v, want %v", v.Value(), 5*time.Second)
	}
}

// --- Bind from env ---

func TestBind_StringFromEnv(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"HOST": "0.0.0.0"})))
	v := Bind(env, "HOST", "localhost")
	if v.Value() != "0.0.0.0" {
		t.Fatalf("got %q, want %q", v.Value(), "0.0.0.0")
	}
	if !v.Found() {
		t.Fatal("should be found")
	}
}

func TestBind_IntFromEnv(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"PORT": "9090"})))
	v := Bind(env, "PORT", 8080)
	if v.Value() != 9090 {
		t.Fatalf("got %d, want %d", v.Value(), 9090)
	}
}

func TestBind_Int64FromEnv(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"SIZE": "1099511627776"})))
	v := Bind(env, "SIZE", int64(0))
	if v.Value() != 1099511627776 {
		t.Fatalf("got %d, want 1099511627776", v.Value())
	}
}

func TestBind_Float64FromEnv(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"RATE": "0.75"})))
	v := Bind(env, "RATE", 1.0)
	if v.Value() != 0.75 {
		t.Fatalf("got %f, want 0.75", v.Value())
	}
}

func TestBind_BoolFromEnv(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"DEBUG": "true"})))
	v := Bind(env, "DEBUG", false)
	if !v.Value() {
		t.Fatal("expected true")
	}
}

func TestBind_DurationFromEnv(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"TIMEOUT": "30s"})))
	v := Bind(env, "TIMEOUT", 5*time.Second)
	if v.Value() != 30*time.Second {
		t.Fatalf("got %v, want %v", v.Value(), 30*time.Second)
	}
}

// --- Prefix ---

func TestPrefix_Applied(t *testing.T) {
	env := New(
		WithPrefix("APP"),
		WithLookup(lookup(map[string]string{"APP_PORT": "3000"})),
	)
	v := Bind(env, "PORT", 8080)
	if v.Key() != "APP_PORT" {
		t.Fatalf("key = %q, want APP_PORT", v.Key())
	}
	if v.Value() != 3000 {
		t.Fatalf("got %d, want 3000", v.Value())
	}
}

func TestPrefix_TrailingUnderscore(t *testing.T) {
	env := New(
		WithPrefix("MY_APP_"),
		WithLookup(lookup(map[string]string{"MY_APP_HOST": "db"})),
	)
	v := Bind(env, "HOST", "localhost")
	if v.Key() != "MY_APP_HOST" {
		t.Fatalf("key = %q, want MY_APP_HOST", v.Key())
	}
	if v.Value() != "db" {
		t.Fatalf("got %q, want db", v.Value())
	}
}

func TestPrefix_Lowercase(t *testing.T) {
	env := New(
		WithPrefix("app"),
		WithLookup(lookup(map[string]string{"APP_DB": "pg"})),
	)
	v := Bind(env, "db", "sqlite")
	if v.Key() != "APP_DB" {
		t.Fatalf("key = %q, want APP_DB", v.Key())
	}
	if v.Value() != "pg" {
		t.Fatalf("got %q, want pg", v.Value())
	}
}

// --- Required ---

func TestBindRequired_Present(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"SECRET": "abc123"})))
	v := BindRequired[string](env, "SECRET")
	if err := env.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Value() != "abc123" {
		t.Fatalf("got %q, want abc123", v.Value())
	}
}

func TestBindRequired_Missing(t *testing.T) {
	env := New(WithLookup(lookup(nil)))
	_ = BindRequired[string](env, "SECRET")
	err := env.Validate()
	if err == nil {
		t.Fatal("expected error for missing required var")
	}
	me, ok := err.(*errx.MultiError)
	if !ok {
		t.Fatalf("expected *errx.MultiError, got %T", err)
	}
	if me.Len() != 1 {
		t.Fatalf("expected 1 error, got %d", me.Len())
	}
}

func TestBindRequired_MultipleErrors(t *testing.T) {
	env := New(WithPrefix("APP"), WithLookup(lookup(nil)))
	_ = BindRequired[string](env, "SECRET")
	_ = BindRequired[int](env, "PORT")
	_ = BindRequired[string](env, "DB_HOST")
	err := env.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	me := err.(*errx.MultiError)
	if me.Len() != 3 {
		t.Fatalf("expected 3 errors, got %d", me.Len())
	}
}

// --- Parse errors ---

func TestBind_InvalidInt(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"PORT": "abc"})))
	v := Bind(env, "PORT", 8080)
	err := env.Validate()
	if err == nil {
		t.Fatal("expected error for invalid int")
	}
	_ = v
}

func TestBind_InvalidBool(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"DEBUG": "nope"})))
	_ = Bind(env, "DEBUG", false)
	err := env.Validate()
	if err == nil {
		t.Fatal("expected error for invalid bool")
	}
}

func TestBind_InvalidDuration(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"TIMEOUT": "forever"})))
	_ = Bind(env, "TIMEOUT", time.Second)
	err := env.Validate()
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestBind_InvalidFloat64(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"RATE": "fast"})))
	_ = Bind(env, "RATE", 1.0)
	err := env.Validate()
	if err == nil {
		t.Fatal("expected error for invalid float64")
	}
}

// --- Validate OK ---

func TestValidate_NoBindings(t *testing.T) {
	env := New()
	if err := env.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_AllOptional(t *testing.T) {
	env := New(WithLookup(lookup(nil)))
	_ = Bind(env, "A", "default")
	_ = Bind(env, "B", 42)
	if err := env.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- MapLookup ---

func TestMapLookup(t *testing.T) {
	m := map[string]string{"FOO": "bar", "NUM": "42"}
	fn := MapLookup(m)
	v, ok := fn("FOO")
	if !ok || v != "bar" {
		t.Fatalf("got (%q, %v), want (bar, true)", v, ok)
	}
	v, ok = fn("MISSING")
	if ok {
		t.Fatalf("expected not found, got (%q, true)", v)
	}
}

// --- WithLookup nil guard ---

func TestWithLookup_Nil(t *testing.T) {
	env := New(WithLookup(nil))
	v := Bind(env, "HOME", "fallback")
	if v.Value() == "" {
		t.Fatal("nil lookup should fall back to os.Getenv")
	}
}

// --- Vars ---

func TestVars(t *testing.T) {
	env := New(WithPrefix("SVC"), WithLookup(lookup(nil)))
	_ = Bind(env, "PORT", 8080)
	_ = Bind(env, "HOST", "localhost")
	_ = BindRequired[string](env, "SECRET")
	vars := env.Vars()
	if len(vars) != 3 {
		t.Fatalf("expected 3 vars, got %d", len(vars))
	}
	for _, v := range vars {
		if !strings.HasPrefix(v, "SVC_") {
			t.Fatalf("expected SVC_ prefix, got %q", v)
		}
	}
}

// --- Ptr ---

func TestPtr(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"PORT": "9090"})))
	v := Bind(env, "PORT", 8080)
	p := v.Ptr()
	if *p != 9090 {
		t.Fatalf("got %d, want 9090", *p)
	}
}

// --- Error domains ---

func TestError_MissingDomain(t *testing.T) {
	env := New(WithLookup(lookup(nil)))
	_ = BindRequired[string](env, "KEY")
	err := env.Validate()
	me := err.(*errx.MultiError)
	xe, ok := me.Errors[0].(*errx.Error)
	if !ok {
		t.Fatal("expected *errx.Error")
	}
	if xe.Domain != DomainEnv {
		t.Fatalf("domain = %q, want %q", xe.Domain, DomainEnv)
	}
	if xe.Code != CodeMissing {
		t.Fatalf("code = %q, want %q", xe.Code, CodeMissing)
	}
}

func TestError_InvalidDomain(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"PORT": "abc"})))
	_ = Bind(env, "PORT", 0)
	err := env.Validate()
	me := err.(*errx.MultiError)
	xe, ok := me.Errors[0].(*errx.Error)
	if !ok {
		t.Fatal("expected *errx.Error")
	}
	if xe.Code != CodeInvalid {
		t.Fatalf("code = %q, want %q", xe.Code, CodeInvalid)
	}
}

// --- Mixed valid and invalid ---

func TestValidate_Mixed(t *testing.T) {
	env := New(WithPrefix("APP"), WithLookup(lookup(map[string]string{
		"APP_PORT":   "9090",
		"APP_DEBUG":  "true",
		"APP_RATE":   "not_float",
	})))
	port := Bind(env, "PORT", 8080)
	debug := Bind(env, "DEBUG", false)
	_ = Bind(env, "RATE", 1.0)
	_ = BindRequired[string](env, "SECRET")

	err := env.Validate()
	if err == nil {
		t.Fatal("expected validation errors")
	}
	me := err.(*errx.MultiError)
	if me.Len() != 2 {
		t.Fatalf("expected 2 errors (RATE invalid + SECRET missing), got %d", me.Len())
	}

	if port.Value() != 9090 {
		t.Fatalf("port = %d, want 9090", port.Value())
	}
	if !debug.Value() {
		t.Fatal("debug should be true")
	}
}

// --- Empty string is found ---

func TestBind_EmptyStringIsFound(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"HOST": ""})))
	v := Bind(env, "HOST", "default")
	if !v.Found() {
		t.Fatal("empty string should be found")
	}
	if v.Value() != "" {
		t.Fatalf("got %q, want empty string", v.Value())
	}
}

func TestBindRequired_EmptyStringIsFound(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"TOKEN": ""})))
	v := BindRequired[string](env, "TOKEN")
	if err := env.Validate(); err != nil {
		t.Fatalf("empty string should satisfy required: %v", err)
	}
	if !v.Found() {
		t.Fatal("empty string should be found")
	}
	if v.Value() != "" {
		t.Fatalf("got %q, want empty string", v.Value())
	}
}

// --- BindTo ---

func TestBindTo_Overrides(t *testing.T) {
	env := New(WithPrefix("APP"), WithLookup(lookup(map[string]string{
		"APP_PORT": "9090",
		"APP_HOST": "db.prod",
	})))
	port := 8080
	host := "localhost"
	BindTo(env, "PORT", &port)
	BindTo(env, "HOST", &host)
	if port != 9090 {
		t.Fatalf("port = %d, want 9090", port)
	}
	if host != "db.prod" {
		t.Fatalf("host = %q, want db.prod", host)
	}
}

func TestBindTo_KeepsDefault(t *testing.T) {
	env := New(WithLookup(lookup(nil)))
	port := 8080
	v := BindTo(env, "PORT", &port)
	if port != 8080 {
		t.Fatalf("port = %d, want 8080 (default)", port)
	}
	if v.Found() {
		t.Fatal("should not be found")
	}
}

func TestBindTo_Validates(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"PORT": "abc"})))
	port := 8080
	BindTo(env, "PORT", &port)
	err := env.Validate()
	if err == nil {
		t.Fatal("expected error for invalid int")
	}
}

func TestBindTo_Bool(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"DEBUG": "true"})))
	debug := false
	BindTo(env, "DEBUG", &debug)
	if !debug {
		t.Fatal("debug should be true")
	}
}

func TestBindTo_NilTarget_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil target")
		}
		msg := strings.ToLower(fmt.Sprint(r))
		if !strings.Contains(msg, "must not be nil") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	env := New(WithLookup(lookup(map[string]string{"PORT": "9090"})))
	BindTo[int](env, "PORT", nil)
}
