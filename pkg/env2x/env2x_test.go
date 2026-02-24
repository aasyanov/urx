package env2x

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

func lookup(m map[string]string) func(string) (string, bool) {
	return MapLookup(m)
}

// --- Flat struct ---

type flatConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

func TestOverlay_FlatStruct(t *testing.T) {
	cfg := flatConfig{Host: "localhost", Port: 8080}
	env := New(
		WithPrefix("APP"),
		WithLookup(lookup(map[string]string{
			"APP_HOST": "0.0.0.0",
			"APP_PORT": "3000",
		})),
	)
	r := Overlay(env, &cfg)

	if cfg.Host != "0.0.0.0" {
		t.Fatalf("host = %q, want 0.0.0.0", cfg.Host)
	}
	if cfg.Port != 3000 {
		t.Fatalf("port = %d, want 3000", cfg.Port)
	}
	if len(r.Available) != 2 {
		t.Fatalf("available = %d, want 2", len(r.Available))
	}
	if len(r.Found) != 2 {
		t.Fatalf("found = %d, want 2", len(r.Found))
	}
	if len(r.Applied) != 2 {
		t.Fatalf("applied = %d, want 2", len(r.Applied))
	}
	if r.Err() != nil {
		t.Fatalf("unexpected error: %v", r.Err())
	}
}

func TestOverlay_FlatStruct_Partial(t *testing.T) {
	cfg := flatConfig{Host: "localhost", Port: 8080}
	env := New(
		WithPrefix("APP"),
		WithLookup(lookup(map[string]string{
			"APP_PORT": "9090",
		})),
	)
	r := Overlay(env, &cfg)

	if cfg.Host != "localhost" {
		t.Fatalf("host should keep default, got %q", cfg.Host)
	}
	if cfg.Port != 9090 {
		t.Fatalf("port = %d, want 9090", cfg.Port)
	}
	if len(r.Available) != 2 {
		t.Fatalf("available = %d, want 2", len(r.Available))
	}
	if len(r.Found) != 1 {
		t.Fatalf("found = %d, want 1", len(r.Found))
	}
}

// --- Nested struct ---

type dbConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type nestedConfig struct {
	DB *dbConfig `yaml:"db"`
}

func TestOverlay_NestedStruct(t *testing.T) {
	cfg := nestedConfig{DB: &dbConfig{Host: "localhost", Port: 5432}}
	env := New(
		WithPrefix("APP"),
		WithLookup(lookup(map[string]string{
			"APP_DB_HOST": "db.prod",
			"APP_DB_PORT": "5433",
		})),
	)
	r := Overlay(env, &cfg)

	if cfg.DB.Host != "db.prod" {
		t.Fatalf("db host = %q, want db.prod", cfg.DB.Host)
	}
	if cfg.DB.Port != 5433 {
		t.Fatalf("db port = %d, want 5433", cfg.DB.Port)
	}
	if r.Err() != nil {
		t.Fatalf("unexpected error: %v", r.Err())
	}
}

// --- Nil pointer lazy allocation ---

func TestOverlay_NilPointerAlloc(t *testing.T) {
	cfg := nestedConfig{} // DB is nil
	env := New(
		WithPrefix("APP"),
		WithLookup(lookup(map[string]string{
			"APP_DB_HOST": "allocated.host",
		})),
	)
	r := Overlay(env, &cfg)

	if cfg.DB == nil {
		t.Fatal("DB should have been allocated")
	}
	if cfg.DB.Host != "allocated.host" {
		t.Fatalf("db host = %q, want allocated.host", cfg.DB.Host)
	}
	if r.Err() != nil {
		t.Fatalf("unexpected error: %v", r.Err())
	}
}

func TestOverlay_NilPointerNoAlloc(t *testing.T) {
	cfg := nestedConfig{}
	env := New(
		WithPrefix("APP"),
		WithLookup(lookup(map[string]string{})),
	)
	Overlay(env, &cfg)

	if cfg.DB != nil {
		t.Fatal("DB should remain nil when no matching vars exist")
	}
}

// --- yaml:"-" skip ---

type secretConfig struct {
	Host   string `yaml:"host"`
	Secret string `yaml:"-"`
}

func TestOverlay_DashTagSkipped(t *testing.T) {
	cfg := secretConfig{Host: "localhost", Secret: "original"}
	env := New(
		WithPrefix("APP"),
		WithLookup(lookup(map[string]string{
			"APP_HOST":   "changed",
			"APP_SECRET": "hacked",
		})),
	)
	r := Overlay(env, &cfg)

	if cfg.Host != "changed" {
		t.Fatalf("host = %q, want changed", cfg.Host)
	}
	if cfg.Secret != "original" {
		t.Fatal("secret should not be overwritten when tag is -")
	}
	if len(r.Available) != 1 {
		t.Fatalf("available = %d, want 1 (Secret excluded)", len(r.Available))
	}
}

// --- Inline ---

type base struct {
	LogLevel string `yaml:"log_level"`
}

type inlineConfig struct {
	base  `yaml:",inline"`
	Port  int    `yaml:"port"`
}

func TestOverlay_Inline(t *testing.T) {
	cfg := inlineConfig{}
	cfg.LogLevel = "info"
	cfg.Port = 8080
	env := New(
		WithPrefix("APP"),
		WithLookup(lookup(map[string]string{
			"APP_LOG_LEVEL": "debug",
			"APP_PORT":      "3000",
		})),
	)
	r := Overlay(env, &cfg)

	if cfg.LogLevel != "debug" {
		t.Fatalf("log_level = %q, want debug", cfg.LogLevel)
	}
	if cfg.Port != 3000 {
		t.Fatalf("port = %d, want 3000", cfg.Port)
	}
	if r.Err() != nil {
		t.Fatalf("unexpected error: %v", r.Err())
	}
}

// --- All scalar types ---

type allTypes struct {
	S   string        `yaml:"s"`
	I   int           `yaml:"i"`
	I8  int8          `yaml:"i8"`
	I16 int16         `yaml:"i16"`
	I32 int32         `yaml:"i32"`
	I64 int64         `yaml:"i64"`
	U   uint          `yaml:"u"`
	U8  uint8         `yaml:"u8"`
	U16 uint16        `yaml:"u16"`
	U32 uint32        `yaml:"u32"`
	U64 uint64        `yaml:"u64"`
	F32 float32       `yaml:"f32"`
	F64 float64       `yaml:"f64"`
	B   bool          `yaml:"b"`
	D   time.Duration `yaml:"d"`
}

func TestOverlay_AllScalarTypes(t *testing.T) {
	cfg := allTypes{}
	env := New(WithLookup(lookup(map[string]string{
		"S":   "hello",
		"I":   "-42",
		"I8":  "127",
		"I16": "32000",
		"I32": "100000",
		"I64": "9999999999",
		"U":   "42",
		"U8":  "255",
		"U16": "65535",
		"U32": "4000000",
		"U64": "18000000000",
		"F32": "3.14",
		"F64": "2.718281828",
		"B":   "true",
		"D":   "5s",
	})))
	r := Overlay(env, &cfg)

	if r.Err() != nil {
		t.Fatalf("unexpected errors: %v", r.Err())
	}
	if cfg.S != "hello" {
		t.Errorf("S = %q", cfg.S)
	}
	if cfg.I != -42 {
		t.Errorf("I = %d", cfg.I)
	}
	if cfg.I8 != 127 {
		t.Errorf("I8 = %d", cfg.I8)
	}
	if cfg.I16 != 32000 {
		t.Errorf("I16 = %d", cfg.I16)
	}
	if cfg.I32 != 100000 {
		t.Errorf("I32 = %d", cfg.I32)
	}
	if cfg.I64 != 9999999999 {
		t.Errorf("I64 = %d", cfg.I64)
	}
	if cfg.U != 42 {
		t.Errorf("U = %d", cfg.U)
	}
	if cfg.U8 != 255 {
		t.Errorf("U8 = %d", cfg.U8)
	}
	if cfg.U16 != 65535 {
		t.Errorf("U16 = %d", cfg.U16)
	}
	if cfg.U32 != 4000000 {
		t.Errorf("U32 = %d", cfg.U32)
	}
	if cfg.U64 != 18000000000 {
		t.Errorf("U64 = %d", cfg.U64)
	}
	if cfg.F64 != 2.718281828 {
		t.Errorf("F64 = %f", cfg.F64)
	}
	if !cfg.B {
		t.Error("B should be true")
	}
	if cfg.D != 5*time.Second {
		t.Errorf("D = %v", cfg.D)
	}
	if len(r.Available) != 15 {
		t.Errorf("available = %d, want 15", len(r.Available))
	}
}

// --- Overflow ---

func TestOverlay_IntOverflow(t *testing.T) {
	type small struct {
		V int8 `yaml:"v"`
	}
	cfg := small{}
	env := New(WithLookup(lookup(map[string]string{"V": "200"})))
	r := Overlay(env, &cfg)

	if r.Err() == nil {
		t.Fatal("expected overflow error")
	}
	xe, ok := r.Errors[0].(*errx.Error)
	if !ok {
		t.Fatal("error should be *errx.Error")
	}
	if xe.Code != CodeParseFailed {
		t.Fatalf("code = %s, want %s", xe.Code, CodeParseFailed)
	}
}

func TestOverlay_UintOverflow(t *testing.T) {
	type small struct {
		V uint8 `yaml:"v"`
	}
	cfg := small{}
	env := New(WithLookup(lookup(map[string]string{"V": "300"})))
	r := Overlay(env, &cfg)

	if r.Err() == nil {
		t.Fatal("expected overflow error")
	}
}

// --- Parse error ---

func TestOverlay_ParseError(t *testing.T) {
	cfg := flatConfig{}
	env := New(WithPrefix("X"), WithLookup(lookup(map[string]string{
		"X_PORT": "not_a_number",
	})))
	r := Overlay(env, &cfg)

	if r.Err() == nil {
		t.Fatal("expected parse error")
	}
	if len(r.Found) != 1 {
		t.Fatalf("found = %d, want 1", len(r.Found))
	}
	if len(r.Applied) != 0 {
		t.Fatalf("applied = %d, want 0 (failed)", len(r.Applied))
	}
}

// --- Unsupported type ---

type withSlice struct {
	Tags []string `yaml:"tags"`
}

func TestOverlay_UnsupportedTypeIgnored(t *testing.T) {
	cfg := withSlice{Tags: []string{"a"}}
	env := New(WithLookup(lookup(map[string]string{
		"TAGS": "b,c",
	})))
	r := Overlay(env, &cfg)

	if len(r.Available) != 0 {
		t.Fatalf("slice fields should not appear in available, got %v", r.Available)
	}
	if r.Err() != nil {
		t.Fatalf("no error expected for skipped slice: %v", r.Err())
	}
}

// --- Custom tag ---

type envTagConfig struct {
	Host string `env:"HOST"`
	Port int    `env:"PORT"`
}

func TestOverlay_CustomTag(t *testing.T) {
	cfg := envTagConfig{Host: "default", Port: 80}
	env := New(
		WithPrefix("MY"),
		WithTag("env"),
		WithLookup(lookup(map[string]string{
			"MY_HOST": "custom.host",
			"MY_PORT": "9999",
		})),
	)
	r := Overlay(env, &cfg)

	if cfg.Host != "custom.host" {
		t.Fatalf("host = %q", cfg.Host)
	}
	if cfg.Port != 9999 {
		t.Fatalf("port = %d", cfg.Port)
	}
	if r.Err() != nil {
		t.Fatalf("unexpected error: %v", r.Err())
	}
}

// --- No prefix ---

func TestOverlay_NoPrefix(t *testing.T) {
	cfg := flatConfig{}
	env := New(WithLookup(lookup(map[string]string{
		"HOST": "bare",
		"PORT": "1234",
	})))
	r := Overlay(env, &cfg)

	if cfg.Host != "bare" {
		t.Fatalf("host = %q", cfg.Host)
	}
	if cfg.Port != 1234 {
		t.Fatalf("port = %d", cfg.Port)
	}
	if r.Err() != nil {
		t.Fatalf("unexpected error: %v", r.Err())
	}
}

// --- Non-pointer target ---

func TestOverlay_NonPointerTarget(t *testing.T) {
	cfg := flatConfig{}
	env := New()
	r := Overlay(env, cfg) // not a pointer

	if r.Err() == nil {
		t.Fatal("expected error for non-pointer target")
	}
}

// --- Nil pointer target ---

func TestOverlay_NilPointerTarget(t *testing.T) {
	var cfg *flatConfig
	env := New()
	r := Overlay(env, cfg)

	if r.Err() == nil {
		t.Fatal("expected error for nil pointer target")
	}
}

// --- Result.Err aggregation ---

func TestResult_Err_MultipleErrors(t *testing.T) {
	type multi struct {
		A int  `yaml:"a"`
		B bool `yaml:"b"`
	}
	cfg := multi{}
	env := New(WithLookup(lookup(map[string]string{
		"A": "xyz",
		"B": "notbool",
	})))
	r := Overlay(env, &cfg)

	if r.Err() == nil {
		t.Fatal("expected errors")
	}
	if len(r.Errors) != 2 {
		t.Fatalf("errors = %d, want 2", len(r.Errors))
	}
}

// --- No tag falls back to field name ---

type noTagConfig struct {
	Hostname string
	MaxConn  int `yaml:"max_conn"`
}

func TestOverlay_NoTagFallback(t *testing.T) {
	cfg := noTagConfig{}
	env := New(WithLookup(lookup(map[string]string{
		"HOSTNAME": "fallback.host",
		"MAX_CONN": "100",
	})))
	r := Overlay(env, &cfg)

	if cfg.Hostname != "fallback.host" {
		t.Fatalf("Hostname = %q", cfg.Hostname)
	}
	if cfg.MaxConn != 100 {
		t.Fatalf("MaxConn = %d", cfg.MaxConn)
	}
	if r.Err() != nil {
		t.Fatalf("unexpected error: %v", r.Err())
	}
}

// --- Deeply nested ---

type l3 struct {
	Value string `yaml:"value"`
}
type l2 struct {
	Inner *l3 `yaml:"inner"`
}
type l1 struct {
	Mid l2 `yaml:"mid"`
}

func TestOverlay_DeeplyNested(t *testing.T) {
	cfg := l1{}
	env := New(
		WithPrefix("X"),
		WithLookup(lookup(map[string]string{
			"X_MID_INNER_VALUE": "deep",
		})),
	)
	r := Overlay(env, &cfg)

	if cfg.Mid.Inner == nil {
		t.Fatal("Inner should be allocated")
	}
	if cfg.Mid.Inner.Value != "deep" {
		t.Fatalf("value = %q, want deep", cfg.Mid.Inner.Value)
	}
	if r.Err() != nil {
		t.Fatalf("unexpected error: %v", r.Err())
	}
}

// --- Duration parse error ---

func TestOverlay_DurationParseError(t *testing.T) {
	type cfg struct {
		Timeout time.Duration `yaml:"timeout"`
	}
	c := cfg{}
	env := New(WithLookup(lookup(map[string]string{
		"TIMEOUT": "not_a_duration",
	})))
	r := Overlay(env, &c)

	if r.Err() == nil {
		t.Fatal("expected parse error for bad duration")
	}
}

// --- Bool parse error ---

func TestOverlay_BoolParseError(t *testing.T) {
	type cfg struct {
		Debug bool `yaml:"debug"`
	}
	c := cfg{}
	env := New(WithLookup(lookup(map[string]string{
		"DEBUG": "maybe",
	})))
	r := Overlay(env, &c)

	if r.Err() == nil {
		t.Fatal("expected parse error for bad bool")
	}
}

// --- Hyphen in tag converted to underscore ---

type hyphenConfig struct {
	LogLevel string `yaml:"log-level"`
}

func TestOverlay_HyphenToUnderscore(t *testing.T) {
	cfg := hyphenConfig{}
	env := New(
		WithPrefix("APP"),
		WithLookup(lookup(map[string]string{
			"APP_LOG_LEVEL": "warn",
		})),
	)
	r := Overlay(env, &cfg)

	if cfg.LogLevel != "warn" {
		t.Fatalf("LogLevel = %q, want warn", cfg.LogLevel)
	}
	if r.Err() != nil {
		t.Fatalf("unexpected error: %v", r.Err())
	}
}

// --- MapLookup ---

func TestMapLookup(t *testing.T) {
	fn := MapLookup(map[string]string{"A": "1"})
	v, ok := fn("A")
	if !ok || v != "1" {
		t.Fatalf("MapLookup(A) = %q, %v", v, ok)
	}
	_, ok = fn("B")
	if ok {
		t.Fatal("MapLookup(B) should return false")
	}
}

// --- Internal helper coverage ---

func TestOverlay_NotSettableField(t *testing.T) {
	type cfg struct {
		port int `yaml:"port"` //nolint:unused // unexported field is the point of this test
	}
	c := cfg{}
	env := New(WithLookup(lookup(map[string]string{
		"PORT": "8080",
	})))
	r := Overlay(env, &c)
	if r.Err() == nil {
		t.Fatal("expected not settable error")
	}
	if len(r.Errors) == 0 {
		t.Fatal("expected at least one error")
	}
	var xe *errx.Error
	if !errors.As(r.Errors[0], &xe) {
		t.Fatalf("expected *errx.Error, got %T", r.Errors[0])
	}
	if xe.Code != CodeNotSettable {
		t.Fatalf("code = %q, want %q", xe.Code, CodeNotSettable)
	}
}

func TestSetField_PointerScalarAlloc(t *testing.T) {
	type cfg struct {
		Port *int
	}
	c := cfg{}
	f := reflect.ValueOf(&c).Elem().Field(0)
	if err := setField(f, "9090", "PORT", "cfg.Port"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Port == nil || *c.Port != 9090 {
		t.Fatalf("port ptr not set correctly: %+v", c.Port)
	}
}

func TestSetField_PointerUnsupportedType(t *testing.T) {
	type cfg struct {
		M *map[string]string
	}
	c := cfg{}
	f := reflect.ValueOf(&c).Elem().Field(0)
	err := setField(f, "x", "M", "cfg.M")
	if err == nil {
		t.Fatal("expected unsupported type error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeUnsupportedType {
		t.Fatalf("code = %q, want %q", xe.Code, CodeUnsupportedType)
	}
}

func TestSetField_UnsupportedKind(t *testing.T) {
	type cfg struct {
		M map[string]string
	}
	c := cfg{}
	f := reflect.ValueOf(&c).Elem().Field(0)
	err := setField(f, "x", "M", "cfg.M")
	if err == nil {
		t.Fatal("expected unsupported type error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeUnsupportedType {
		t.Fatalf("code = %q, want %q", xe.Code, CodeUnsupportedType)
	}
}

func TestHasMatchingVars_NonStructType(t *testing.T) {
	env := New(WithLookup(lookup(map[string]string{"X": "1"})))
	if hasMatchingVars(env, nil, reflect.TypeFor[int]()) {
		t.Fatal("non-struct types should not have matching vars")
	}
}

func TestHasMatchingVars_PointerStructType(t *testing.T) {
	type inner struct {
		Host string `yaml:"host"`
	}
	env := New(WithPrefix("APP"), WithLookup(lookup(map[string]string{
		"APP_HOST": "db.local",
	})))
	if !hasMatchingVars(env, []string{"APP"}, reflect.TypeFor[*inner]()) {
		t.Fatal("expected matching vars for pointer-to-struct type")
	}
}

func TestHasMatchingVars_SkipsDashTag(t *testing.T) {
	type cfg struct {
		Secret string `yaml:"-"`
		Name   string `yaml:"name"`
	}
	env := New(WithPrefix("APP"), WithLookup(lookup(map[string]string{
		"APP_SECRET": "x",
	})))
	if hasMatchingVars(env, []string{"APP"}, reflect.TypeFor[cfg]()) {
		t.Fatal("dash-tagged fields must be ignored")
	}
}

func TestHasMatchingVars_InlinePath(t *testing.T) {
	type inlined struct {
		Host string `yaml:"host"`
	}
	type cfg struct {
		inlined `yaml:",inline"`
	}
	env := New(WithPrefix("APP"), WithLookup(lookup(map[string]string{
		"APP_HOST": "db",
	})))
	if !hasMatchingVars(env, []string{"APP"}, reflect.TypeFor[cfg]()) {
		t.Fatal("inline field should be resolved on parent path")
	}
}

func TestHasMatchingVars_RecursiveNestedFalse(t *testing.T) {
	type l2 struct {
		Port int `yaml:"port"`
	}
	type l1 struct {
		Inner l2 `yaml:"inner"`
	}
	env := New(WithPrefix("APP"), WithLookup(lookup(map[string]string{})))
	if hasMatchingVars(env, []string{"APP"}, reflect.TypeFor[l1]()) {
		t.Fatal("expected false when no matching nested vars exist")
	}
}

func TestIsScalar_Cases(t *testing.T) {
	if isScalar(nil) {
		t.Fatal("nil type should be non-scalar")
	}
	if !isScalar(reflect.TypeFor[int]()) {
		t.Fatal("int should be scalar")
	}
	if !isScalar(reflect.TypeFor[*int]()) {
		t.Fatal("*int should be scalar")
	}
	if isScalar(reflect.TypeFor[map[string]string]()) {
		t.Fatal("map should be non-scalar")
	}
	if isScalar(reflect.TypeFor[[]string]()) {
		t.Fatal("slice should be non-scalar")
	}
	if isScalar(reflect.TypeFor[interface{}]()) {
		t.Fatal("interface should be non-scalar")
	}
}

func TestOverlay_NilTarget_ReturnsErrxError(t *testing.T) {
	env := New()
	r := Overlay(env, nil)
	if len(r.Errors) == 0 {
		t.Fatal("expected error for nil target")
	}
	var ex *errx.Error
	if !errors.As(r.Errors[0], &ex) {
		t.Fatalf("expected *errx.Error, got %T", r.Errors[0])
	}
	if ex.Code != CodeInvalidInput {
		t.Fatalf("expected code %s, got %s", CodeInvalidInput, ex.Code)
	}
	if ex.Domain != DomainEnv2 {
		t.Fatalf("expected domain %s, got %s", DomainEnv2, ex.Domain)
	}
}
