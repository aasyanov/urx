package testx

import (
	"errors"
	"sync"
	"testing"

	"github.com/aasyanov/urx/pkg/errx"
)

// ============================================================
// FailMode.String
// ============================================================

func TestFailMode_String(t *testing.T) {
	tests := []struct {
		m    FailMode
		want string
	}{
		{FailNever, "never"},
		{FailAlways, "always"},
		{FailPattern, "pattern"},
		{FailAfterN, "after_n"},
		{FailUntilN, "until_n"},
		{FailEveryN, "every_n"},
		{FailMode(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.m.String(); got != tt.want {
			t.Errorf("FailMode(%d).String() = %q, want %q", tt.m, got, tt.want)
		}
	}
}

// ============================================================
// Default config
// ============================================================

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	if cfg.mode != FailNever {
		t.Fatalf("expected FailNever, got %v", cfg.mode)
	}
	if cfg.msg != "simulated failure" {
		t.Fatalf("expected default message, got %q", cfg.msg)
	}
}

// ============================================================
// FailNever
// ============================================================

func TestNeverFail(t *testing.T) {
	s := NeverFail()
	for i := 0; i < 100; i++ {
		if err := s.Call(); err != nil {
			t.Fatalf("call %d: expected nil, got %v", i, err)
		}
	}
	st := s.Stats()
	if st.Calls != 100 {
		t.Fatalf("expected 100 calls, got %d", st.Calls)
	}
	if st.Failures != 0 {
		t.Fatalf("expected 0 failures, got %d", st.Failures)
	}
}

// ============================================================
// FailAlways
// ============================================================

func TestAlwaysFail(t *testing.T) {
	s := AlwaysFail()
	for i := 0; i < 10; i++ {
		err := s.Call()
		if err == nil {
			t.Fatalf("call %d: expected error", i)
		}
		var xe *errx.Error
		if !errors.As(err, &xe) {
			t.Fatalf("expected *errx.Error, got %T", err)
		}
		if xe.Domain != DomainTest {
			t.Fatalf("expected domain %s, got %s", DomainTest, xe.Domain)
		}
		if xe.Code != CodeSimulated {
			t.Fatalf("expected code %s, got %s", CodeSimulated, xe.Code)
		}
		if !xe.Retryable() {
			t.Fatal("expected retryable error")
		}
	}
	st := s.Stats()
	if st.Calls != 10 || st.Failures != 10 {
		t.Fatalf("expected 10/10, got %d/%d", st.Calls, st.Failures)
	}
}

// ============================================================
// FailPattern
// ============================================================

func TestFailPattern(t *testing.T) {
	s := Pattern("SSFS")
	results := make([]bool, 8)
	for i := range results {
		results[i] = s.Call() == nil
	}
	want := []bool{true, true, false, true, true, true, false, true}
	for i, w := range want {
		if results[i] != w {
			t.Fatalf("call %d: expected success=%v, got %v", i+1, w, results[i])
		}
	}
}

func TestFailPattern_Empty(t *testing.T) {
	s := New(WithFailPattern(""))
	for i := 0; i < 5; i++ {
		if err := s.Call(); err != nil {
			t.Fatalf("empty pattern should never fail, got %v", err)
		}
	}
}

func TestFailPattern_AllFail(t *testing.T) {
	s := Pattern("FFF")
	for i := 0; i < 6; i++ {
		if err := s.Call(); err == nil {
			t.Fatalf("call %d: expected error", i)
		}
	}
}

func TestFailPattern_CaseInsensitive(t *testing.T) {
	s := Pattern("sf")
	if err := s.Call(); err != nil {
		t.Fatal("expected success for 's'")
	}
	if err := s.Call(); err == nil {
		t.Fatal("expected failure for 'f'")
	}
}

// ============================================================
// FailAfterN
// ============================================================

func TestFailAfterN(t *testing.T) {
	s := FailAfter(3)
	for i := 1; i <= 3; i++ {
		if err := s.Call(); err != nil {
			t.Fatalf("call %d: expected success, got %v", i, err)
		}
	}
	for i := 4; i <= 6; i++ {
		if err := s.Call(); err == nil {
			t.Fatalf("call %d: expected failure", i)
		}
	}
}

func TestFailAfterN_Zero(t *testing.T) {
	s := New(WithFailAfterN(0))
	for i := 0; i < 5; i++ {
		if err := s.Call(); err == nil {
			t.Fatalf("call %d: n=0 should fail from the start", i)
		}
	}
}

// ============================================================
// FailUntilN
// ============================================================

func TestFailUntilN(t *testing.T) {
	s := FailUntil(3)
	for i := 1; i <= 3; i++ {
		if err := s.Call(); err == nil {
			t.Fatalf("call %d: expected failure", i)
		}
	}
	for i := 4; i <= 6; i++ {
		if err := s.Call(); err != nil {
			t.Fatalf("call %d: expected success, got %v", i, err)
		}
	}
}

// ============================================================
// FailEveryN
// ============================================================

func TestFailEveryN(t *testing.T) {
	s := FailEvery(3)
	for i := 1; i <= 9; i++ {
		err := s.Call()
		shouldFail := i%3 == 0
		if shouldFail && err == nil {
			t.Fatalf("call %d: expected failure", i)
		}
		if !shouldFail && err != nil {
			t.Fatalf("call %d: expected success, got %v", i, err)
		}
	}
}

func TestFailEveryN_Zero(t *testing.T) {
	s := New(WithFailEveryN(0))
	for i := 0; i < 5; i++ {
		if err := s.Call(); err != nil {
			t.Fatalf("n=0 should never fail, got %v", err)
		}
	}
}

// ============================================================
// WithMessage
// ============================================================

func TestWithMessage(t *testing.T) {
	s := New(WithFailAlways(), WithMessage("db timeout"))
	err := s.Call()
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Message != "db timeout" {
		t.Fatalf("expected message 'db timeout', got %q", xe.Message)
	}
}

func TestWithMessage_Empty(t *testing.T) {
	s := New(WithFailAlways(), WithMessage(""))
	err := s.Call()
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Message != "simulated failure" {
		t.Fatalf("empty message should keep default, got %q", xe.Message)
	}
}

// ============================================================
// WithErrorFunc
// ============================================================

func TestWithErrorFunc(t *testing.T) {
	customErr := func() *errx.Error {
		return errx.New(errx.DomainRepo, errx.CodeInternal, "connection refused",
			errx.WithRetry(errx.RetryNone),
		)
	}
	s := New(WithFailAlways(), WithErrorFunc(customErr))
	err := s.Call()

	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != errx.DomainRepo {
		t.Fatalf("expected domain %s, got %s", errx.DomainRepo, xe.Domain)
	}
	if xe.Retryable() {
		t.Fatal("expected non-retryable error")
	}
}

// ============================================================
// Stats / Reset
// ============================================================

func TestStats(t *testing.T) {
	s := New(WithFailPattern("SF"))
	s.Call()
	s.Call()
	s.Call()
	st := s.Stats()
	if st.Calls != 3 {
		t.Fatalf("expected 3 calls, got %d", st.Calls)
	}
	if st.Failures != 1 {
		t.Fatalf("expected 1 failure, got %d", st.Failures)
	}
}

func TestReset(t *testing.T) {
	s := FailUntil(2)
	s.Call()
	s.Call()
	s.Call()

	s.Reset()
	st := s.Stats()
	if st.Calls != 0 || st.Failures != 0 {
		t.Fatalf("expected zeros after reset, got %+v", st)
	}

	if err := s.Call(); err == nil {
		t.Fatal("after reset, FailUntil(2) should fail on call 1")
	}
}

// ============================================================
// Concurrent safety
// ============================================================

func TestConcurrent_Safety(t *testing.T) {
	s := New(WithFailPattern("SSSF"))
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Call()
		}()
	}
	wg.Wait()
	st := s.Stats()
	if st.Calls != 100 {
		t.Fatalf("expected 100 calls, got %d", st.Calls)
	}
}

// ============================================================
// Convenience constructors
// ============================================================

func TestConvenience_AlwaysFail(t *testing.T) {
	s := AlwaysFail()
	if s.cfg.mode != FailAlways {
		t.Fatal("expected FailAlways")
	}
}

func TestConvenience_NeverFail(t *testing.T) {
	s := NeverFail()
	if s.cfg.mode != FailNever {
		t.Fatal("expected FailNever")
	}
}

func TestConvenience_FailAfter(t *testing.T) {
	s := FailAfter(5)
	if s.cfg.mode != FailAfterN || s.cfg.n != 5 {
		t.Fatal("expected FailAfterN with n=5")
	}
}

func TestConvenience_FailUntil(t *testing.T) {
	s := FailUntil(3)
	if s.cfg.mode != FailUntilN || s.cfg.n != 3 {
		t.Fatal("expected FailUntilN with n=3")
	}
}

func TestConvenience_FailEvery(t *testing.T) {
	s := FailEvery(4)
	if s.cfg.mode != FailEveryN || s.cfg.n != 4 {
		t.Fatal("expected FailEveryN with n=4")
	}
}

func TestConvenience_Pattern(t *testing.T) {
	s := Pattern("SFS")
	if s.cfg.mode != FailPattern || s.cfg.pattern != "SFS" {
		t.Fatal("expected FailPattern with SFS")
	}
}

// ============================================================
// Error constructors
// ============================================================

func TestErrSimulated(t *testing.T) {
	e := errSimulated("test error")
	if e.Domain != DomainTest || e.Code != CodeSimulated {
		t.Fatalf("expected TEST/SIMULATED, got %s/%s", e.Domain, e.Code)
	}
	if e.Message != "test error" {
		t.Fatalf("expected 'test error', got %q", e.Message)
	}
	if !e.Retryable() {
		t.Fatal("expected retryable")
	}
}

func TestDomainConstant(t *testing.T) {
	if DomainTest != "TEST" {
		t.Fatalf("expected TEST, got %s", DomainTest)
	}
}

func TestCodeConstant(t *testing.T) {
	if CodeSimulated != "SIMULATED" {
		t.Fatalf("expected SIMULATED, got %s", CodeSimulated)
	}
}
