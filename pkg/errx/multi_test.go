package errx

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// --- NewMulti ---

func TestNewMulti_FiltersNils(t *testing.T) {
	e1 := New(DomainAuth, CodeUnauthorized, "a")
	me := NewMulti(nil, e1, nil)
	if me.Len() != 1 {
		t.Fatalf("Len = %d, want 1", me.Len())
	}
	if me.Errors[0] != e1 {
		t.Error("expected e1")
	}
}

func TestNewMulti_AllNils(t *testing.T) {
	me := NewMulti(nil, nil)
	if me.Len() != 0 {
		t.Fatalf("Len = %d, want 0", me.Len())
	}
	if me.Err() != nil {
		t.Error("Err() should be nil for empty MultiError")
	}
}

func TestNewMulti_Empty(t *testing.T) {
	me := NewMulti()
	if me.Len() != 0 {
		t.Fatalf("Len = %d, want 0", me.Len())
	}
}

func TestNewMulti_NoAliasing(t *testing.T) {
	errs := []error{
		New(DomainAuth, CodeUnauthorized, "a"),
		New(DomainRepo, CodeNotFound, "b"),
	}
	original := make([]error, len(errs))
	copy(original, errs)

	me := NewMulti(errs...)
	me.Add(New(DomainInternal, CodeInternal, "c"))

	if len(errs) != 2 {
		t.Fatalf("original slice was modified: len=%d", len(errs))
	}
	for i, e := range errs {
		if e != original[i] {
			t.Errorf("errs[%d] was modified", i)
		}
	}
}

// --- Add ---

func TestMultiError_Add(t *testing.T) {
	me := NewMulti()
	me.Add(nil)
	if me.Len() != 0 {
		t.Fatal("Add(nil) should be no-op")
	}

	e := New(DomainAuth, CodeUnauthorized, "x")
	me.Add(e)
	if me.Len() != 1 {
		t.Fatalf("Len = %d, want 1", me.Len())
	}
}

func TestMultiError_Add_ReAggregates(t *testing.T) {
	me := NewMulti()
	me.Add(New(DomainAuth, CodeUnauthorized, "x", WithRetry(RetrySafe)))
	if !me.Retryable() {
		t.Error("should be retryable after adding RetrySafe error")
	}
}

// --- Err ---

func TestMultiError_Err_NonEmpty(t *testing.T) {
	me := NewMulti(New(DomainAuth, CodeUnauthorized, "x"))
	err := me.Err()
	if err == nil {
		t.Fatal("Err() should not be nil")
	}
	if err != me {
		t.Error("Err() should return itself")
	}
}

func TestMultiError_Err_Empty(t *testing.T) {
	me := NewMulti()
	if me.Err() != nil {
		t.Error("Err() should be nil for empty")
	}
}

// --- Error string ---

func TestMultiError_Error_Empty(t *testing.T) {
	me := NewMulti()
	if got := me.Error(); got != "no errors" {
		t.Errorf("Error() = %q, want %q", got, "no errors")
	}
}

func TestMultiError_Error_Multiple(t *testing.T) {
	e1 := New(DomainAuth, CodeUnauthorized, "a")
	e2 := New(DomainRepo, CodeNotFound, "b")
	me := NewMulti(e1, e2)

	got := me.Error()
	if !strings.Contains(got, "[0]") || !strings.Contains(got, "[1]") {
		t.Errorf("Error() = %q, expected numbered list", got)
	}
	if !strings.Contains(got, "AUTH.UNAUTHORIZED") {
		t.Error("should contain first error")
	}
	if !strings.Contains(got, "REPO.NOT_FOUND") {
		t.Error("should contain second error")
	}
}

func TestMultiError_Error_PlainErrors(t *testing.T) {
	me := NewMulti(errors.New("plain1"), errors.New("plain2"))
	got := me.Error()
	if !strings.Contains(got, "plain1") || !strings.Contains(got, "plain2") {
		t.Errorf("Error() = %q", got)
	}
}

// --- Aggregate: Severity ---

func TestMultiError_Severity_Highest(t *testing.T) {
	e1 := New(DomainAuth, CodeUnauthorized, "a", WithSeverity(SeverityWarn))
	e2 := New(DomainRepo, CodeNotFound, "b", WithSeverity(SeverityCritical))
	me := NewMulti(e1, e2)
	if me.Severity() != SeverityCritical {
		t.Errorf("Severity = %v, want Critical", me.Severity())
	}
}

func TestMultiError_Severity_Default(t *testing.T) {
	me := NewMulti(errors.New("plain"))
	if me.Severity() != SeverityInfo {
		t.Errorf("Severity = %v, want Info (default)", me.Severity())
	}
}

func TestMultiError_Severity_PanicOverrides(t *testing.T) {
	e1 := New(DomainAuth, CodeUnauthorized, "a", WithSeverity(SeverityWarn))
	pe := NewPanicError("op", "boom")
	me := NewMulti(e1, pe)
	if me.Severity() != SeverityCritical {
		t.Errorf("Severity = %v, want Critical (panic override)", me.Severity())
	}
}

// --- Aggregate: Retry ---

func TestMultiError_Retryable_None(t *testing.T) {
	me := NewMulti(New(DomainAuth, CodeUnauthorized, "a"))
	if me.Retryable() {
		t.Error("should not be retryable")
	}
}

func TestMultiError_Retryable_Safe(t *testing.T) {
	me := NewMulti(
		New(DomainAuth, CodeUnauthorized, "a"),
		New(DomainRepo, CodeNotFound, "b", WithRetry(RetrySafe)),
	)
	if !me.Retryable() {
		t.Error("should be retryable")
	}
}

func TestMultiError_Retryable_PanicOverrides(t *testing.T) {
	me := NewMulti(
		New(DomainAuth, CodeUnauthorized, "a", WithRetry(RetrySafe)),
		NewPanicError("op", "boom"),
	)
	if me.Retryable() {
		t.Error("panic should override retry to None")
	}
}

// --- Aggregate: IsPanic ---

func TestMultiError_IsPanic_False(t *testing.T) {
	me := NewMulti(New(DomainAuth, CodeUnauthorized, "a"))
	if me.IsPanic() {
		t.Error("should not be panic")
	}
}

func TestMultiError_IsPanic_True(t *testing.T) {
	me := NewMulti(
		New(DomainAuth, CodeUnauthorized, "a"),
		NewPanicError("op", "boom"),
	)
	if !me.IsPanic() {
		t.Error("should be panic")
	}
}

// --- Unwrap ---

func TestMultiError_Unwrap(t *testing.T) {
	e1 := New(DomainAuth, CodeUnauthorized, "a")
	e2 := New(DomainRepo, CodeNotFound, "b")
	me := NewMulti(e1, e2)

	errs := me.Unwrap()
	if len(errs) != 2 {
		t.Fatalf("Unwrap len = %d, want 2", len(errs))
	}
	if errs[0] != e1 || errs[1] != e2 {
		t.Error("Unwrap returned wrong errors")
	}
}

func TestMultiError_ErrorsIs(t *testing.T) {
	sentinel := errors.New("sentinel")
	e1 := Wrap(sentinel, DomainRepo, CodeInternal, "wrapped")
	me := NewMulti(e1)

	if !errors.Is(me, sentinel) {
		t.Error("errors.Is should find sentinel through MultiError.Unwrap")
	}
}

func TestMultiError_ErrorsAs(t *testing.T) {
	inner := New(DomainRepo, CodeNotFound, "x")
	me := NewMulti(inner)

	var xe *Error
	if !errors.As(me, &xe) {
		t.Fatal("errors.As should find *Error through MultiError")
	}
	if xe.Code != CodeNotFound {
		t.Errorf("Code = %q", xe.Code)
	}
}

// --- Mixed plain + structured errors ---

func TestMultiError_MixedErrors(t *testing.T) {
	plain := errors.New("plain")
	structured := New(DomainAuth, CodeUnauthorized, "structured",
		WithSeverity(SeverityWarn),
		WithRetry(RetryUnsafe),
	)
	me := NewMulti(plain, structured)

	if me.Len() != 2 {
		t.Fatalf("Len = %d", me.Len())
	}
	if me.Severity() != SeverityWarn {
		t.Errorf("Severity = %v", me.Severity())
	}
	if !me.Retryable() {
		t.Error("should be retryable")
	}
}

// --- Add after construction ---

func TestMultiError_AddMultiple(t *testing.T) {
	me := NewMulti()
	me.Add(New(DomainAuth, CodeUnauthorized, "a", WithSeverity(SeverityWarn)))
	me.Add(New(DomainRepo, CodeNotFound, "b", WithSeverity(SeverityCritical)))
	me.Add(nil)

	if me.Len() != 2 {
		t.Fatalf("Len = %d, want 2", me.Len())
	}
	if me.Severity() != SeverityCritical {
		t.Errorf("Severity = %v", me.Severity())
	}
}

// --- Interface compliance ---

var _ error = (*MultiError)(nil)

// --- Edge: wrapped MultiError ---

func TestMultiError_WrappedInFmt(t *testing.T) {
	e1 := New(DomainAuth, CodeUnauthorized, "a")
	me := NewMulti(e1)
	wrapped := fmt.Errorf("outer: %w", me)

	var me2 *MultiError
	if !errors.As(wrapped, &me2) {
		t.Fatal("errors.As should find *MultiError")
	}
	if me2.Len() != 1 {
		t.Errorf("Len = %d", me2.Len())
	}
}
