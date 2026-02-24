package panix

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/ctxx"
	"github.com/aasyanov/urx/pkg/errx"
)

func nilCtx() context.Context { return nil }

// --- Safe -- no panic ---

func TestSafe_NoPanic(t *testing.T) {
	_, err := Safe[struct{}](context.Background(), "op", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestSafe_FnReturnsError(t *testing.T) {
	sentinel := errors.New("business error")
	_, err := Safe[struct{}](context.Background(), "op", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

// --- Safe -- panic recovery ---

func TestSafe_PanicString(t *testing.T) {
	_, err := Safe[struct{}](context.Background(), "myOp", func(ctx context.Context) (struct{}, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	xe, ok := errx.As(err)
	if !ok {
		t.Fatal("expected *errx.Error")
	}
	if !xe.IsPanic() {
		t.Error("expected IsPanic=true")
	}
	if xe.Op != "myOp" {
		t.Errorf("Op = %q, want myOp", xe.Op)
	}
	if xe.Domain != errx.DomainInternal {
		t.Errorf("Domain = %q", xe.Domain)
	}
	if xe.Code != errx.CodePanic {
		t.Errorf("Code = %q", xe.Code)
	}
	if xe.Severity != errx.SeverityCritical {
		t.Errorf("Severity = %v", xe.Severity)
	}
}

func TestSafe_PanicError(t *testing.T) {
	sentinel := errors.New("sentinel")
	_, err := Safe[struct{}](context.Background(), "op", func(ctx context.Context) (struct{}, error) {
		panic(sentinel)
	})
	xe, ok := errx.As(err)
	if !ok {
		t.Fatal("expected *errx.Error")
	}
	if !errors.Is(xe, sentinel) {
		t.Error("errors.Is should find sentinel through Cause")
	}
}

func TestSafe_PanicInt(t *testing.T) {
	_, err := Safe[struct{}](context.Background(), "op", func(ctx context.Context) (struct{}, error) {
		panic(42)
	})
	xe, ok := errx.As(err)
	if !ok {
		t.Fatal("expected *errx.Error")
	}
	if !xe.IsPanic() {
		t.Error("expected IsPanic=true")
	}
}

// --- Safe -- trace propagation ---

func TestSafe_TracePropagation(t *testing.T) {
	ctx := ctxx.WithTrace(context.Background())
	origTrace, origSpan := ctxx.TraceFromContext(ctx)

	_, err := Safe[struct{}](ctx, "op", func(ctx context.Context) (struct{}, error) {
		panic("boom")
	})
	xe, ok := errx.As(err)
	if !ok {
		t.Fatal("expected *errx.Error")
	}
	if xe.TraceID != origTrace {
		t.Errorf("TraceID = %q, want %q", xe.TraceID, origTrace)
	}
	if xe.SpanID != origSpan {
		t.Errorf("SpanID = %q, want %q", xe.SpanID, origSpan)
	}
}

func TestSafe_TracePropagation_NoTrace(t *testing.T) {
	_, err := Safe[struct{}](context.Background(), "op", func(ctx context.Context) (struct{}, error) {
		panic("boom")
	})
	xe, ok := errx.As(err)
	if !ok {
		t.Fatal("expected *errx.Error")
	}
	if xe.TraceID == "" {
		t.Error("expected generated TraceID")
	}
	if xe.SpanID == "" {
		t.Error("expected generated SpanID")
	}
}

// --- Safe -- nil ctx ---

func TestSafe_NilCtx_NoPanic(t *testing.T) {
	_, err := Safe[struct{}](nilCtx(), "op", func(ctx context.Context) (struct{}, error) {
		if ctx == nil {
			t.Error("ctx should not be nil inside fn")
		}
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestSafe_NilCtx_Panic(t *testing.T) {
	_, err := Safe[struct{}](nilCtx(), "op", func(ctx context.Context) (struct{}, error) {
		panic("boom")
	})
	xe, ok := errx.As(err)
	if !ok {
		t.Fatal("expected *errx.Error")
	}
	if xe.TraceID == "" || xe.SpanID == "" {
		t.Error("expected generated trace IDs even with nil ctx")
	}
}

// --- SafeGo -- no panic ---

func TestSafeGo_NoPanic(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	executed := false

	SafeGo(context.Background(), "op", func(ctx context.Context) {
		executed = true
		wg.Done()
	})

	wg.Wait()
	if !executed {
		t.Error("fn should have been executed")
	}
}

// --- SafeGo -- panic with onError ---

func TestSafeGo_Panic_WithOnError(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	var captured error

	SafeGo(context.Background(), "op", func(ctx context.Context) {
		panic("boom")
	}, WithOnError(func(ctx context.Context, err error) {
		captured = err
		wg.Done()
	}))

	wg.Wait()
	xe, ok := errx.As(captured)
	if !ok {
		t.Fatal("expected *errx.Error in onError callback")
	}
	if !xe.IsPanic() {
		t.Error("expected IsPanic=true")
	}
}

// --- SafeGo -- panic without onError (fire-and-forget) ---

func TestSafeGo_Panic_NoOnError(t *testing.T) {
	done := make(chan struct{})

	SafeGo(context.Background(), "op", func(ctx context.Context) {
		defer func() { close(done) }()
		panic("boom")
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SafeGo goroutine did not complete in time")
	}
}

// --- SafeGo -- fn error with onError ---

func TestSafeGo_FnError_WithOnError(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	var captured error

	SafeGo(context.Background(), "op", func(ctx context.Context) {
		// SafeGo wraps fn in a func that returns nil, so non-panic errors
		// from the void fn are not propagated. This test verifies that
		// only panics trigger onError.
		wg.Done()
	}, WithOnError(func(ctx context.Context, err error) {
		captured = err
	}))

	wg.Wait()
	time.Sleep(10 * time.Millisecond)
	if captured != nil {
		t.Error("onError should not be called when fn succeeds")
	}
}

// --- SafeGo -- nil ctx ---

func TestSafeGo_NilCtx(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	SafeGo(nilCtx(), "op", func(ctx context.Context) {
		if ctx == nil {
			t.Error("ctx should not be nil inside fn")
		}
		wg.Done()
	})

	wg.Wait()
}

func TestSafeGo_NilCtx_OnErrorGetsNonNilContext(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	var gotNil bool

	SafeGo(nilCtx(), "op", func(ctx context.Context) {
		panic("boom")
	}, WithOnError(func(ctx context.Context, err error) {
		gotNil = (ctx == nil)
		wg.Done()
	}))

	wg.Wait()
	if gotNil {
		t.Fatal("onError received nil context")
	}
}

// --- Wrap ---

func TestWrap_NoPanic(t *testing.T) {
	fn := Wrap[struct{}](func(ctx context.Context) (struct{}, error) {
		return struct{}{}, nil
	}, "op")

	_, err := fn(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWrap_Panic(t *testing.T) {
	fn := Wrap[struct{}](func(ctx context.Context) (struct{}, error) {
		panic("boom")
	}, "op")

	_, err := fn(context.Background())
	xe, ok := errx.As(err)
	if !ok {
		t.Fatal("expected *errx.Error")
	}
	if !xe.IsPanic() {
		t.Error("expected IsPanic=true")
	}
}

func TestWrap_FnReturnsError(t *testing.T) {
	sentinel := errors.New("fail")
	fn := Wrap[struct{}](func(ctx context.Context) (struct{}, error) {
		return struct{}{}, sentinel
	}, "op")

	_, err := fn(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

// --- WithOnError option ---

func TestWithOnError(t *testing.T) {
	called := false
	handler := func(ctx context.Context, err error) { called = true }

	var cfg config
	WithOnError(handler)(&cfg)

	if cfg.onError == nil {
		t.Fatal("onError should be set")
	}
	cfg.onError(context.Background(), errors.New("test"))
	if !called {
		t.Error("handler should have been called")
	}
}

// --- Interface compliance ---

func TestSafe_ReturnsErrorInterface(t *testing.T) {
	_, err := Safe[struct{}](context.Background(), "op", func(ctx context.Context) (struct{}, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected non-nil error")
	}
}
