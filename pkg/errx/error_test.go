package errx

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// --- RetryClass ---

func TestRetryClass_Values(t *testing.T) {
	if RetryNone != 0 {
		t.Fatalf("RetryNone = %d, want 0", RetryNone)
	}
	if RetrySafe != 1 {
		t.Fatalf("RetrySafe = %d, want 1", RetrySafe)
	}
	if RetryUnsafe != 2 {
		t.Fatalf("RetryUnsafe = %d, want 2", RetryUnsafe)
	}
}

func TestRetryClass_Retryable(t *testing.T) {
	tests := []struct {
		r    RetryClass
		want bool
	}{
		{RetryNone, false},
		{RetrySafe, true},
		{RetryUnsafe, true},
		{RetryClass(255), false},
	}
	for _, tt := range tests {
		if got := tt.r.Retryable(); got != tt.want {
			t.Errorf("RetryClass(%d).Retryable() = %v, want %v", tt.r, got, tt.want)
		}
	}
}

func TestRetryClass_String(t *testing.T) {
	tests := []struct {
		r    RetryClass
		want string
	}{
		{RetryNone, "none"},
		{RetrySafe, "safe"},
		{RetryUnsafe, "unsafe"},
		{RetryClass(99), "none"},
	}
	for _, tt := range tests {
		if got := tt.r.String(); got != tt.want {
			t.Errorf("RetryClass(%d).String() = %q, want %q", tt.r, got, tt.want)
		}
	}
}

func TestRetryClass_MarshalText(t *testing.T) {
	for _, r := range []RetryClass{RetryNone, RetrySafe, RetryUnsafe} {
		b, err := r.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText() error: %v", err)
		}
		if string(b) != r.String() {
			t.Errorf("MarshalText() = %q, want %q", b, r.String())
		}
	}
}

// --- Severity ---

func TestSeverity_Values(t *testing.T) {
	if SeverityInfo != 0 || SeverityWarn != 1 || SeverityError != 2 || SeverityCritical != 3 {
		t.Fatal("Severity iota values are wrong")
	}
}

func TestSeverity_String(t *testing.T) {
	tests := []struct {
		s    Severity
		want string
	}{
		{SeverityInfo, "info"},
		{SeverityWarn, "warn"},
		{SeverityError, "error"},
		{SeverityCritical, "critical"},
		{Severity(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestSeverity_MarshalText(t *testing.T) {
	for _, s := range []Severity{SeverityInfo, SeverityWarn, SeverityError, SeverityCritical} {
		b, err := s.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText() error: %v", err)
		}
		if string(b) != s.String() {
			t.Errorf("MarshalText() = %q, want %q", b, s.String())
		}
	}
}

// --- Category ---

func TestCategory_Values(t *testing.T) {
	if CategoryBusiness != 0 || CategorySystem != 1 || CategorySecurity != 2 {
		t.Fatal("Category iota values are wrong")
	}
}

func TestCategory_String(t *testing.T) {
	tests := []struct {
		c    Category
		want string
	}{
		{CategoryBusiness, "business"},
		{CategorySystem, "system"},
		{CategorySecurity, "security"},
		{Category(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.c.String(); got != tt.want {
			t.Errorf("Category(%d).String() = %q, want %q", tt.c, got, tt.want)
		}
	}
}

func TestCategory_MarshalText(t *testing.T) {
	for _, c := range []Category{CategoryBusiness, CategorySystem, CategorySecurity} {
		b, err := c.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText() error: %v", err)
		}
		if string(b) != c.String() {
			t.Errorf("MarshalText() = %q, want %q", b, c.String())
		}
	}
}

// --- EnableStackTrace ---

func TestEnableStackTrace(t *testing.T) {
	defer EnableStackTrace(false)

	EnableStackTrace(true)
	e := New(DomainAuth, CodeUnauthorized, "test")
	if e.StackTrace() == "" {
		t.Fatal("expected stack trace when enabled")
	}

	EnableStackTrace(false)
	e2 := New(DomainAuth, CodeUnauthorized, "test2")
	if e2.StackTrace() != "" {
		t.Fatal("expected no stack trace when disabled")
	}
}

// --- Options ---

func TestWithOp(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x", WithOp("UserRepo.Find"))
	if e.Op != "UserRepo.Find" {
		t.Errorf("Op = %q, want %q", e.Op, "UserRepo.Find")
	}
}

func TestWithSeverity(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x", WithSeverity(SeverityWarn))
	if e.Severity != SeverityWarn {
		t.Errorf("Severity = %v, want %v", e.Severity, SeverityWarn)
	}
}

func TestWithCategory(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x", WithCategory(CategorySecurity))
	if e.Category != CategorySecurity {
		t.Errorf("Category = %v, want %v", e.Category, CategorySecurity)
	}
}

func TestWithRetry(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x", WithRetry(RetrySafe))
	if e.Retry != RetrySafe {
		t.Errorf("Retry = %v, want %v", e.Retry, RetrySafe)
	}
}

func TestWithTrace(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x", WithTrace("t-1", "s-1"))
	if e.TraceID != "t-1" || e.SpanID != "s-1" {
		t.Errorf("TraceID=%q SpanID=%q, want t-1/s-1", e.TraceID, e.SpanID)
	}
}

func TestWithMeta(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x",
		WithMeta("user_id", "u-1", "attempt", 3),
	)
	if e.Meta["user_id"] != "u-1" {
		t.Errorf("Meta[user_id] = %v, want u-1", e.Meta["user_id"])
	}
	if e.Meta["attempt"] != 3 {
		t.Errorf("Meta[attempt] = %v, want 3", e.Meta["attempt"])
	}
}

func TestWithMeta_NonStringKey(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x",
		WithMeta(42, "val", "ok", "yes"),
	)
	if _, exists := e.Meta["42"]; exists {
		t.Error("non-string key 42 should have been skipped")
	}
	if e.Meta["ok"] != "yes" {
		t.Errorf("Meta[ok] = %v, want yes", e.Meta["ok"])
	}
}

func TestWithMeta_OddCount(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x",
		WithMeta("key1", "val1", "orphan"),
	)
	if e.Meta["key1"] != "val1" {
		t.Error("expected key1 to be set")
	}
	if _, exists := e.Meta["orphan"]; exists {
		t.Error("orphan key should not be set")
	}
}

func TestWithMeta_Empty(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x", WithMeta())
	if e.Meta != nil {
		t.Error("expected nil Meta for empty WithMeta")
	}
}

func TestWithMetaMap(t *testing.T) {
	m := map[string]any{"a": 1, "b": "two"}
	e := New(DomainAuth, CodeUnauthorized, "x", WithMetaMap(m))
	if e.Meta["a"] != 1 || e.Meta["b"] != "two" {
		t.Errorf("Meta = %v, want %v", e.Meta, m)
	}
}

func TestWithMetaMap_Nil(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x", WithMetaMap(nil))
	if e.Meta != nil {
		t.Error("expected nil Meta for nil map")
	}
}

func TestWithMetaMap_Merge(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x",
		WithMeta("a", 1),
		WithMetaMap(map[string]any{"b": 2}),
	)
	if e.Meta["a"] != 1 || e.Meta["b"] != 2 {
		t.Errorf("Meta = %v, expected both a and b", e.Meta)
	}
}

// --- New ---

func TestNew_Defaults(t *testing.T) {
	before := time.Now()
	e := New(DomainAuth, CodeUnauthorized, "token expired")
	after := time.Now()

	if e.Domain != DomainAuth {
		t.Errorf("Domain = %q, want %q", e.Domain, DomainAuth)
	}
	if e.Code != CodeUnauthorized {
		t.Errorf("Code = %q, want %q", e.Code, CodeUnauthorized)
	}
	if e.Message != "token expired" {
		t.Errorf("Message = %q, want %q", e.Message, "token expired")
	}
	if e.Severity != SeverityError {
		t.Errorf("Severity = %v, want SeverityError", e.Severity)
	}
	if e.Category != CategorySystem {
		t.Errorf("Category = %v, want CategorySystem", e.Category)
	}
	if e.Timestamp.Before(before) || e.Timestamp.After(after) {
		t.Errorf("Timestamp %v not in [%v, %v]", e.Timestamp, before, after)
	}
	if e.Cause != nil {
		t.Error("expected nil Cause")
	}
	if e.Op != "" {
		t.Errorf("Op = %q, want empty", e.Op)
	}
	if e.Meta != nil {
		t.Error("expected nil Meta")
	}
	if e.isPanic {
		t.Error("expected isPanic=false")
	}
}

func TestNew_WithAllOptions(t *testing.T) {
	e := New(DomainRepo, CodeNotFound, "user not found",
		WithOp("UserRepo.Find"),
		WithSeverity(SeverityWarn),
		WithCategory(CategoryBusiness),
		WithRetry(RetrySafe),
		WithTrace("trace-1", "span-1"),
		WithMeta("id", "u-1"),
	)
	if e.Op != "UserRepo.Find" {
		t.Errorf("Op = %q", e.Op)
	}
	if e.Severity != SeverityWarn {
		t.Errorf("Severity = %v", e.Severity)
	}
	if e.Category != CategoryBusiness {
		t.Errorf("Category = %v", e.Category)
	}
	if e.Retry != RetrySafe {
		t.Errorf("Retry = %v", e.Retry)
	}
	if e.TraceID != "trace-1" || e.SpanID != "span-1" {
		t.Errorf("Trace = %q/%q", e.TraceID, e.SpanID)
	}
	if e.Meta["id"] != "u-1" {
		t.Errorf("Meta = %v", e.Meta)
	}
}

// --- Wrap ---

func TestWrap_NilError(t *testing.T) {
	e := Wrap(nil, DomainRepo, CodeNotFound, "should be nil")
	if e != nil {
		t.Fatal("Wrap(nil, ...) should return nil")
	}
}

func TestWrap_WithCause(t *testing.T) {
	cause := errors.New("db connection lost")
	e := Wrap(cause, DomainRepo, CodeInternal, "query failed",
		WithOp("UserRepo.Find"),
	)
	if e.Cause != cause {
		t.Error("Cause not set")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is should find cause")
	}
	if e.Op != "UserRepo.Find" {
		t.Errorf("Op = %q", e.Op)
	}
}

func TestWrap_StackTrace(t *testing.T) {
	defer EnableStackTrace(false)
	EnableStackTrace(true)

	cause := errors.New("boom")
	e := Wrap(cause, DomainRepo, CodeInternal, "wrapped")
	st := e.StackTrace()
	if st == "" {
		t.Fatal("expected stack trace")
	}
	if !strings.Contains(st, "TestWrap_StackTrace") {
		t.Errorf("stack trace should contain test function name, got:\n%s", st)
	}
}

// --- NewPanicError ---

func TestNewPanicError_StringValue(t *testing.T) {
	e := NewPanicError("myOp", "something broke")
	if e.Domain != DomainInternal {
		t.Errorf("Domain = %q", e.Domain)
	}
	if e.Code != CodePanic {
		t.Errorf("Code = %q", e.Code)
	}
	if e.Op != "myOp" {
		t.Errorf("Op = %q", e.Op)
	}
	if !e.isPanic {
		t.Error("expected isPanic=true")
	}
	if e.Severity != SeverityCritical {
		t.Errorf("Severity = %v", e.Severity)
	}
	if e.Category != CategorySystem {
		t.Errorf("Category = %v", e.Category)
	}
	if !strings.Contains(e.Message, "something broke") {
		t.Errorf("Message = %q", e.Message)
	}
	if e.Cause == nil {
		t.Fatal("expected non-nil Cause")
	}
	if e.Cause.Error() != "something broke" {
		t.Errorf("Cause = %q", e.Cause.Error())
	}
}

func TestNewPanicError_ErrorValue(t *testing.T) {
	sentinel := errors.New("sentinel")
	e := NewPanicError("op", sentinel)
	if !errors.Is(e, sentinel) {
		t.Error("errors.Is should find sentinel through Cause")
	}
	if e.Cause != sentinel {
		t.Error("Cause should be the original error, not a fmt.Errorf wrapper")
	}
}

func TestNewPanicError_IntValue(t *testing.T) {
	e := NewPanicError("op", 42)
	if !strings.Contains(e.Message, "42") {
		t.Errorf("Message = %q, should contain 42", e.Message)
	}
}

func TestNewPanicError_StackTrace(t *testing.T) {
	defer EnableStackTrace(false)
	EnableStackTrace(true)

	e := NewPanicError("op", "boom")
	if e.StackTrace() == "" {
		t.Fatal("expected stack trace when enabled")
	}
}

// --- Error interface ---

func TestError_ErrorString_NoCause(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "denied")
	want := "AUTH.UNAUTHORIZED: denied"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestError_ErrorString_WithCause(t *testing.T) {
	cause := errors.New("timeout")
	e := Wrap(cause, DomainRepo, CodeInternal, "query failed")
	want := "REPO.INTERNAL: query failed | cause: timeout"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestError_Unwrap(t *testing.T) {
	cause := errors.New("root")
	e := Wrap(cause, DomainRepo, CodeInternal, "wrapped")
	if e.Unwrap() != cause {
		t.Error("Unwrap should return cause")
	}

	e2 := New(DomainAuth, CodeUnauthorized, "no cause")
	if e2.Unwrap() != nil {
		t.Error("Unwrap should return nil when no cause")
	}
}

func TestError_Retryable(t *testing.T) {
	tests := []struct {
		retry RetryClass
		want  bool
	}{
		{RetryNone, false},
		{RetrySafe, true},
		{RetryUnsafe, true},
	}
	for _, tt := range tests {
		e := New(DomainAuth, CodeUnauthorized, "x", WithRetry(tt.retry))
		if got := e.Retryable(); got != tt.want {
			t.Errorf("Retryable() with %v = %v, want %v", tt.retry, got, tt.want)
		}
	}
}

func TestError_IsPanic(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "normal")
	if e.IsPanic() {
		t.Error("regular error should not be panic")
	}

	pe := NewPanicError("op", "boom")
	if !pe.IsPanic() {
		t.Error("panic error should report IsPanic=true")
	}
}

// --- StackTrace ---

func TestStackTrace_Empty(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x")
	if e.StackTrace() != "" {
		t.Error("expected empty stack trace when disabled")
	}
}

func TestStackTrace_Captured(t *testing.T) {
	defer EnableStackTrace(false)
	EnableStackTrace(true)

	e := New(DomainAuth, CodeUnauthorized, "x")
	st := e.StackTrace()
	if st == "" {
		t.Fatal("expected non-empty stack trace")
	}
	if !strings.Contains(st, "TestStackTrace_Captured") {
		t.Errorf("stack should contain calling function, got:\n%s", st)
	}
}

func TestStackTrace_NilFuncForPC(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x")
	e.stack = []uintptr{0}
	got := e.StackTrace()
	if got != "" {
		t.Errorf("expected empty string for invalid PC, got %q", got)
	}
}

// --- As helper ---

func TestAs_Found(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x")
	xe, ok := As(e)
	if !ok || xe != e {
		t.Error("As should find *Error directly")
	}
}

func TestAs_Wrapped(t *testing.T) {
	inner := New(DomainAuth, CodeUnauthorized, "inner")
	wrapped := fmt.Errorf("outer: %w", inner)
	xe, ok := As(wrapped)
	if !ok || xe != inner {
		t.Error("As should find *Error through wrapping")
	}
}

func TestAs_NotFound(t *testing.T) {
	plain := errors.New("plain error")
	xe, ok := As(plain)
	if ok || xe != nil {
		t.Error("As should return nil, false for plain error")
	}
}

func TestAs_Nil(t *testing.T) {
	xe, ok := As(nil)
	if ok || xe != nil {
		t.Error("As(nil) should return nil, false")
	}
}

// --- errors.Is / errors.As chain ---

func TestErrorsIs_Chain(t *testing.T) {
	sentinel := errors.New("sentinel")
	e1 := Wrap(sentinel, DomainRepo, CodeInternal, "level1")
	e2 := Wrap(e1, DomainAuth, CodeUnauthorized, "level2")

	if !errors.Is(e2, sentinel) {
		t.Error("errors.Is should traverse the chain to sentinel")
	}
	if !errors.Is(e2, e1) {
		t.Error("errors.Is should find e1 in chain")
	}
}

func TestErrorsAs_Chain(t *testing.T) {
	inner := New(DomainRepo, CodeNotFound, "inner")
	outer := fmt.Errorf("wrap: %w", inner)

	var xe *Error
	if !errors.As(outer, &xe) {
		t.Fatal("errors.As should find *Error")
	}
	if xe.Code != CodeNotFound {
		t.Errorf("Code = %q, want %q", xe.Code, CodeNotFound)
	}
}

// --- MarshalJSON ---

func TestMarshalJSON_Minimal(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "denied")
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if m["domain"] != "AUTH" {
		t.Errorf("domain = %v", m["domain"])
	}
	if m["code"] != "UNAUTHORIZED" {
		t.Errorf("code = %v", m["code"])
	}
	if m["message"] != "denied" {
		t.Errorf("message = %v", m["message"])
	}
	if m["severity"] != "error" {
		t.Errorf("severity = %v", m["severity"])
	}
	if m["category"] != "system" {
		t.Errorf("category = %v", m["category"])
	}
	if m["retry"] != "none" {
		t.Errorf("retry = %v", m["retry"])
	}
	if _, ok := m["timestamp"]; !ok {
		t.Error("missing timestamp")
	}
	if _, ok := m["cause"]; ok {
		t.Error("cause should be omitted when nil")
	}
	if _, ok := m["op"]; ok {
		t.Error("op should be omitted when empty")
	}
	if _, ok := m["meta"]; ok {
		t.Error("meta should be omitted when nil")
	}
	if _, ok := m["stack"]; ok {
		t.Error("stack should be omitted when empty")
	}
	if _, ok := m["is_panic"]; ok {
		t.Error("is_panic should be omitted when false")
	}
}

func TestMarshalJSON_Full(t *testing.T) {
	defer EnableStackTrace(false)
	EnableStackTrace(true)

	cause := errors.New("db down")
	e := Wrap(cause, DomainRepo, CodeInternal, "query failed",
		WithOp("UserRepo.Find"),
		WithSeverity(SeverityCritical),
		WithCategory(CategorySecurity),
		WithRetry(RetrySafe),
		WithTrace("t-1", "s-1"),
		WithMeta("key", "val"),
	)

	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if m["op"] != "UserRepo.Find" {
		t.Errorf("op = %v", m["op"])
	}
	if m["severity"] != "critical" {
		t.Errorf("severity = %v", m["severity"])
	}
	if m["category"] != "security" {
		t.Errorf("category = %v", m["category"])
	}
	if m["retry"] != "safe" {
		t.Errorf("retry = %v", m["retry"])
	}
	if m["trace_id"] != "t-1" {
		t.Errorf("trace_id = %v", m["trace_id"])
	}
	if m["span_id"] != "s-1" {
		t.Errorf("span_id = %v", m["span_id"])
	}
	if m["cause"] != "db down" {
		t.Errorf("cause = %v", m["cause"])
	}
	meta, ok := m["meta"].(map[string]any)
	if !ok || meta["key"] != "val" {
		t.Errorf("meta = %v", m["meta"])
	}
	if _, ok := m["stack"]; !ok {
		t.Error("missing stack when enabled")
	}
}

func TestMarshalJSON_RecursiveCause_ErrxWrapsErrx(t *testing.T) {
	inner := New("DB", "CONN_FAILED", "connection refused",
		WithSeverity(SeverityError),
		WithMeta("host", "db.prod"),
	)
	outer := Wrap(inner, "SERVICE", "USER_LOOKUP", "find user failed",
		WithSeverity(SeverityCritical),
	)

	b, err := json.Marshal(outer)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if m["domain"] != "SERVICE" {
		t.Errorf("domain = %v", m["domain"])
	}

	cause, ok := m["cause"].(map[string]any)
	if !ok {
		t.Fatalf("cause should be an object, got %T: %v", m["cause"], m["cause"])
	}
	if cause["domain"] != "DB" {
		t.Errorf("cause.domain = %v", cause["domain"])
	}
	if cause["code"] != "CONN_FAILED" {
		t.Errorf("cause.code = %v", cause["code"])
	}
	if cause["severity"] != "error" {
		t.Errorf("cause.severity = %v", cause["severity"])
	}
	causeMeta, ok := cause["meta"].(map[string]any)
	if !ok || causeMeta["host"] != "db.prod" {
		t.Errorf("cause.meta = %v", cause["meta"])
	}
}

func TestMarshalJSON_RecursiveCause_DeepChain(t *testing.T) {
	plain := errors.New("connection refused")
	level1 := Wrap(plain, "DB", "CONN", "pg connect failed")
	level2 := Wrap(level1, "REPO", "QUERY", "select user failed")
	level3 := Wrap(level2, "SERVICE", "FIND", "user lookup failed")

	b, err := json.Marshal(level3)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	// level 3: SERVICE.FIND
	if m["domain"] != "SERVICE" {
		t.Fatalf("top domain = %v", m["domain"])
	}
	c2, ok := m["cause"].(map[string]any)
	if !ok {
		t.Fatalf("level 2 cause should be object, got %T", m["cause"])
	}

	// level 2: REPO.QUERY
	if c2["domain"] != "REPO" {
		t.Fatalf("level 2 domain = %v", c2["domain"])
	}
	c1, ok := c2["cause"].(map[string]any)
	if !ok {
		t.Fatalf("level 1 cause should be object, got %T", c2["cause"])
	}

	// level 1: DB.CONN
	if c1["domain"] != "DB" {
		t.Fatalf("level 1 domain = %v", c1["domain"])
	}

	// level 0: plain string
	causeStr, ok := c1["cause"].(string)
	if !ok {
		t.Fatalf("leaf cause should be string, got %T: %v", c1["cause"], c1["cause"])
	}
	if causeStr != "connection refused" {
		t.Errorf("leaf cause = %q", causeStr)
	}
}

func TestMarshalJSON_RecursiveCause_PlainErrorIsString(t *testing.T) {
	plain := errors.New("timeout")
	e := Wrap(plain, "NET", "TIMEOUT", "request timed out")

	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	causeStr, ok := m["cause"].(string)
	if !ok {
		t.Fatalf("plain error cause should be string, got %T", m["cause"])
	}
	if causeStr != "timeout" {
		t.Errorf("cause = %q", causeStr)
	}
}

func TestMarshalJSON_PanicError(t *testing.T) {
	e := NewPanicError("op", "boom")
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if m["is_panic"] != true {
		t.Errorf("is_panic = %v, want true", m["is_panic"])
	}
	if m["severity"] != "critical" {
		t.Errorf("severity = %v", m["severity"])
	}
}

func TestMarshalJSON_EnumStrings(t *testing.T) {
	tests := []struct {
		severity Severity
		category Category
		retry    RetryClass
		wantSev  string
		wantCat  string
		wantRet  string
	}{
		{SeverityInfo, CategoryBusiness, RetryNone, "info", "business", "none"},
		{SeverityWarn, CategorySystem, RetrySafe, "warn", "system", "safe"},
		{SeverityError, CategorySecurity, RetryUnsafe, "error", "security", "unsafe"},
		{SeverityCritical, CategoryBusiness, RetryNone, "critical", "business", "none"},
	}
	for _, tt := range tests {
		e := New(DomainAuth, CodeUnauthorized, "x",
			WithSeverity(tt.severity),
			WithCategory(tt.category),
			WithRetry(tt.retry),
		)
		b, _ := json.Marshal(e)
		var m map[string]any
		json.Unmarshal(b, &m)

		if m["severity"] != tt.wantSev {
			t.Errorf("severity = %v, want %v", m["severity"], tt.wantSev)
		}
		if m["category"] != tt.wantCat {
			t.Errorf("category = %v, want %v", m["category"], tt.wantCat)
		}
		if m["retry"] != tt.wantRet {
			t.Errorf("retry = %v, want %v", m["retry"], tt.wantRet)
		}
	}
}

// --- LogValue (slog.LogValuer) ---

func TestLogValue_Minimal(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "denied")
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("LogValue kind = %v, want Group", v.Kind())
	}

	attrs := v.Group()
	m := attrMap(attrs)

	assertAttr(t, m, "domain", "AUTH")
	assertAttr(t, m, "code", "UNAUTHORIZED")
	assertAttr(t, m, "message", "denied")
	assertAttr(t, m, "severity", "error")
	assertAttr(t, m, "category", "system")

	if _, ok := m["op"]; ok {
		t.Error("op should not be present when empty")
	}
	if _, ok := m["retry"]; ok {
		t.Error("retry should not be present when RetryNone")
	}
}

func TestLogValue_Full(t *testing.T) {
	defer EnableStackTrace(false)
	EnableStackTrace(true)

	cause := errors.New("timeout")
	e := Wrap(cause, DomainRepo, CodeInternal, "query",
		WithOp("Find"),
		WithSeverity(SeverityCritical),
		WithCategory(CategorySecurity),
		WithRetry(RetrySafe),
		WithTrace("t-1", "s-1"),
		WithMeta("k", "v"),
	)

	attrs := e.LogValue().Group()
	m := attrMap(attrs)

	assertAttr(t, m, "op", "Find")
	assertAttr(t, m, "severity", "critical")
	assertAttr(t, m, "category", "security")
	assertAttr(t, m, "retry", "safe")
	assertAttr(t, m, "trace_id", "t-1")
	assertAttr(t, m, "span_id", "s-1")
	assertAttr(t, m, "cause", "timeout")

	if _, ok := m["meta"]; !ok {
		t.Error("meta should be present")
	}
	if _, ok := m["stack"]; !ok {
		t.Error("stack should be present when enabled")
	}
}

func TestLogValue_PanicFlag(t *testing.T) {
	e := NewPanicError("op", "boom")
	attrs := e.LogValue().Group()
	m := attrMap(attrs)
	if _, ok := m["panic"]; !ok {
		t.Error("panic attr should be present for panic errors")
	}
}

func TestLogValue_ZeroEnums(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x",
		WithSeverity(SeverityInfo),
		WithCategory(CategoryBusiness),
	)
	attrs := e.LogValue().Group()
	m := attrMap(attrs)

	assertAttr(t, m, "severity", "info")
	assertAttr(t, m, "category", "business")
}

// --- SlogLevel ---

func TestSlogLevel(t *testing.T) {
	tests := []struct {
		sev  Severity
		want slog.Level
	}{
		{SeverityInfo, slog.LevelInfo},
		{SeverityWarn, slog.LevelWarn},
		{SeverityError, slog.LevelError},
		{SeverityCritical, slog.LevelError + 4},
	}
	for _, tt := range tests {
		e := New(DomainAuth, CodeUnauthorized, "x", WithSeverity(tt.sev))
		if got := e.SlogLevel(); got != tt.want {
			t.Errorf("SlogLevel() for %v = %v, want %v", tt.sev, got, tt.want)
		}
	}
}

func TestSlogLevel_UnknownSeverity(t *testing.T) {
	e := New(DomainAuth, CodeUnauthorized, "x")
	e.Severity = Severity(99)
	if got := e.SlogLevel(); got != slog.LevelError {
		t.Errorf("SlogLevel() for unknown = %v, want LevelError", got)
	}
}

// --- Domain/Code constants ---

func TestDomainConstants(t *testing.T) {
	domains := map[string]string{
		"DomainInternal": DomainInternal,
		"DomainAuth":     DomainAuth,
		"DomainRepo":     DomainRepo,
	}
	for name, val := range domains {
		if val == "" {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestCodeConstants(t *testing.T) {
	codes := map[string]string{
		"CodePanic":            CodePanic,
		"CodeInvalidInput":     CodeInvalidInput,
		"CodeUnauthorized":     CodeUnauthorized,
		"CodeForbidden":        CodeForbidden,
		"CodeNotFound":         CodeNotFound,
		"CodeConflict":         CodeConflict,
		"CodeRateLimit":        CodeRateLimit,
		"CodeInternal":         CodeInternal,
		"CodeContextCancelled": CodeContextCancelled,
	}
	for name, val := range codes {
		if val == "" {
			t.Errorf("%s is empty", name)
		}
	}
}

// --- Interface compliance (compile-time) ---

var (
	_ error          = (*Error)(nil)
	_ slog.LogValuer = (*Error)(nil)
	_ json.Marshaler = (*Error)(nil)
)

// --- Helpers ---

func attrMap(attrs []slog.Attr) map[string]slog.Value {
	m := make(map[string]slog.Value, len(attrs))
	for _, a := range attrs {
		m[a.Key] = a.Value
	}
	return m
}

func assertAttr(t *testing.T, m map[string]slog.Value, key, want string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("missing attr %q", key)
		return
	}
	got := v.String()
	if got != want {
		t.Errorf("attr %q = %q, want %q", key, got, want)
	}
}
