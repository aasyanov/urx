package errx

import (
	"encoding/json"
	"errors"
	"testing"
)

// --- Construction ---

func BenchmarkNew_Minimal(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		New(DomainAuth, CodeUnauthorized, "denied")
	}
}

func BenchmarkNew_AllOptions(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		New(DomainAuth, CodeUnauthorized, "denied",
			WithOp("UserRepo.Find"),
			WithSeverity(SeverityWarn),
			WithCategory(CategoryBusiness),
			WithRetry(RetrySafe),
			WithTrace("trace-1", "span-1"),
			WithMeta("user_id", "u-123", "attempt", 3),
		)
	}
}

func BenchmarkWrap(b *testing.B) {
	cause := errors.New("db timeout")
	b.ReportAllocs()
	for b.Loop() {
		Wrap(cause, DomainRepo, CodeInternal, "query failed",
			WithOp("UserRepo.Find"),
		)
	}
}

func BenchmarkWrap_Nil(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Wrap(nil, DomainRepo, CodeInternal, "noop")
	}
}

func BenchmarkNewPanicError_String(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		NewPanicError("op", "something broke")
	}
}

func BenchmarkNewPanicError_Error(b *testing.B) {
	cause := errors.New("sentinel")
	b.ReportAllocs()
	for b.Loop() {
		NewPanicError("op", cause)
	}
}

// --- Construction with stack trace ---

func BenchmarkNew_WithStack(b *testing.B) {
	EnableStackTrace(true)
	defer EnableStackTrace(false)
	b.ReportAllocs()
	for b.Loop() {
		New(DomainAuth, CodeUnauthorized, "denied")
	}
}

// --- Error() string ---

func BenchmarkError_String_NoCause(b *testing.B) {
	e := New(DomainAuth, CodeUnauthorized, "denied")
	b.ReportAllocs()
	for b.Loop() {
		_ = e.Error()
	}
}

func BenchmarkError_String_WithCause(b *testing.B) {
	e := Wrap(errors.New("timeout"), DomainRepo, CodeInternal, "query failed")
	b.ReportAllocs()
	for b.Loop() {
		_ = e.Error()
	}
}

// --- JSON ---

func BenchmarkMarshalJSON_Minimal(b *testing.B) {
	e := New(DomainAuth, CodeUnauthorized, "denied")
	b.ReportAllocs()
	for b.Loop() {
		json.Marshal(e)
	}
}

func BenchmarkMarshalJSON_Full(b *testing.B) {
	e := Wrap(errors.New("db"), DomainRepo, CodeInternal, "query",
		WithOp("Find"),
		WithSeverity(SeverityCritical),
		WithCategory(CategorySecurity),
		WithRetry(RetrySafe),
		WithTrace("t", "s"),
		WithMeta("k", "v"),
	)
	b.ReportAllocs()
	for b.Loop() {
		json.Marshal(e)
	}
}

// --- slog LogValue ---

func BenchmarkLogValue_Minimal(b *testing.B) {
	e := New(DomainAuth, CodeUnauthorized, "denied")
	b.ReportAllocs()
	for b.Loop() {
		e.LogValue()
	}
}

func BenchmarkLogValue_Full(b *testing.B) {
	e := Wrap(errors.New("db"), DomainRepo, CodeInternal, "query",
		WithOp("Find"),
		WithSeverity(SeverityCritical),
		WithCategory(CategorySecurity),
		WithRetry(RetrySafe),
		WithTrace("t", "s"),
		WithMeta("k", "v"),
	)
	b.ReportAllocs()
	for b.Loop() {
		e.LogValue()
	}
}

// --- As helper ---

func BenchmarkAs_Found(b *testing.B) {
	e := New(DomainAuth, CodeUnauthorized, "x")
	b.ReportAllocs()
	for b.Loop() {
		As(e)
	}
}

func BenchmarkAs_NotFound(b *testing.B) {
	plain := errors.New("plain")
	b.ReportAllocs()
	for b.Loop() {
		As(plain)
	}
}

func BenchmarkAs_DeepChain(b *testing.B) {
	inner := New(DomainAuth, CodeUnauthorized, "x")
	var err error = inner
	for range 10 {
		err = Wrap(err, DomainRepo, CodeInternal, "wrap")
	}
	b.ReportAllocs()
	for b.Loop() {
		As(err)
	}
}

// --- StackTrace formatting ---

func BenchmarkStackTrace_Format(b *testing.B) {
	EnableStackTrace(true)
	defer EnableStackTrace(false)
	e := New(DomainAuth, CodeUnauthorized, "x")
	b.ReportAllocs()
	for b.Loop() {
		_ = e.StackTrace()
	}
}

// --- MultiError ---

func BenchmarkNewMulti_3(b *testing.B) {
	e1 := New(DomainAuth, CodeUnauthorized, "a")
	e2 := New(DomainRepo, CodeNotFound, "b")
	e3 := New(DomainInternal, CodeInternal, "c")
	b.ReportAllocs()
	for b.Loop() {
		NewMulti(e1, e2, e3)
	}
}

func BenchmarkNewMulti_WithNils(b *testing.B) {
	e1 := New(DomainAuth, CodeUnauthorized, "a")
	b.ReportAllocs()
	for b.Loop() {
		NewMulti(nil, e1, nil, nil)
	}
}

func BenchmarkMultiError_Add(b *testing.B) {
	e := New(DomainAuth, CodeUnauthorized, "x")
	b.ReportAllocs()
	for b.Loop() {
		me := NewMulti()
		me.Add(e)
	}
}

func BenchmarkMultiError_Error_10(b *testing.B) {
	me := NewMulti()
	for range 10 {
		me.Add(New(DomainAuth, CodeUnauthorized, "err"))
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = me.Error()
	}
}

// --- Options ---

func BenchmarkWithMeta_2Pairs(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		New(DomainAuth, CodeUnauthorized, "x",
			WithMeta("user_id", "u-1", "attempt", 3),
		)
	}
}

func BenchmarkWithMetaMap(b *testing.B) {
	m := map[string]any{"a": 1, "b": "two", "c": true}
	b.ReportAllocs()
	for b.Loop() {
		New(DomainAuth, CodeUnauthorized, "x", WithMetaMap(m))
	}
}
