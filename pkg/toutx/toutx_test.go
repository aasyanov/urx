package toutx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Execute: success within timeout ---

func TestExecute_Success(t *testing.T) {
	_, err := Execute[struct{}](context.Background(), time.Second, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// --- Execute: function error returned as-is ---

func TestExecute_FuncError(t *testing.T) {
	sentinel := errors.New("business error")
	_, err := Execute[struct{}](context.Background(), time.Second, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

// --- Execute: deadline exceeded ---

func TestExecute_DeadlineExceeded(t *testing.T) {
	_, err := Execute[struct{}](context.Background(), 20*time.Millisecond, func(ctx context.Context) (struct{}, error) {
		time.Sleep(200 * time.Millisecond)
		return struct{}{}, nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != DomainTimeout {
		t.Fatalf("expected domain %s, got %s", DomainTimeout, xe.Domain)
	}
	if xe.Code != CodeDeadlineExceeded {
		t.Fatalf("expected code %s, got %s", CodeDeadlineExceeded, xe.Code)
	}
}

// --- Execute: parent context cancelled ---

func TestExecute_ParentCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := Execute[struct{}](ctx, 5*time.Second, func(ctx context.Context) (struct{}, error) {
		time.Sleep(200 * time.Millisecond)
		return struct{}{}, nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeCancelled {
		t.Fatalf("expected code %s, got %s", CodeCancelled, xe.Code)
	}
	if !errors.Is(xe, context.Canceled) {
		t.Fatal("expected to wrap context.Canceled")
	}
}

// --- Execute: panic recovery ---

func TestExecute_PanicRecovery(t *testing.T) {
	_, err := Execute[struct{}](context.Background(), time.Second, func(ctx context.Context) (struct{}, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected error from panic")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if !xe.IsPanic() {
		t.Fatal("expected panic error")
	}
}

// --- Execute: WithOp attaches operation name ---

func TestExecute_WithOp(t *testing.T) {
	_, err := Execute[struct{}](context.Background(), 10*time.Millisecond, func(ctx context.Context) (struct{}, error) {
		time.Sleep(100 * time.Millisecond)
		return struct{}{}, nil
	}, WithOp("db.query"))

	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Op != "db.query" {
		t.Fatalf("expected op=db.query, got %s", xe.Op)
	}
}

// --- Execute: function respects derived context ---

func TestExecute_FuncRespectsContext(t *testing.T) {
	_, err := Execute[struct{}](context.Background(), 20*time.Millisecond, func(ctx context.Context) (struct{}, error) {
		<-ctx.Done()
		return struct{}{}, ctx.Err()
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Timer: reusable execution ---

func TestTimer_Execute(t *testing.T) {
	tm := New(WithTimeout(time.Second), WithOp("svc.call"))
	_, err := Execute[struct{}](context.Background(), 0, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, nil
	}, WithTimer(tm))
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestTimer_DeadlineExceeded(t *testing.T) {
	tm := New(WithTimeout(20 * time.Millisecond), WithOp("slow.op"))
	_, err := Execute[struct{}](context.Background(), 0, func(ctx context.Context) (struct{}, error) {
		time.Sleep(200 * time.Millisecond)
		return struct{}{}, nil
	}, WithTimer(tm))
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeDeadlineExceeded {
		t.Fatalf("expected code %s, got %s", CodeDeadlineExceeded, xe.Code)
	}
	if xe.Op != "slow.op" {
		t.Fatalf("expected op=slow.op, got %s", xe.Op)
	}
}

// --- defaultConfig ---

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	if cfg.timeout != 30*time.Second {
		t.Fatalf("expected 30s, got %v", cfg.timeout)
	}
	if cfg.op != "" {
		t.Fatalf("expected empty op, got %s", cfg.op)
	}
}

// --- Options: validation ---

func TestWithTimeout_Invalid(t *testing.T) {
	cfg := defaultConfig()
	WithTimeout(0)(&cfg)
	if cfg.timeout != 30*time.Second {
		t.Fatalf("expected unchanged, got %v", cfg.timeout)
	}
	WithTimeout(-time.Second)(&cfg)
	if cfg.timeout != 30*time.Second {
		t.Fatalf("expected unchanged, got %v", cfg.timeout)
	}
}

func TestWithTimeout_Valid(t *testing.T) {
	cfg := defaultConfig()
	WithTimeout(5 * time.Second)(&cfg)
	if cfg.timeout != 5*time.Second {
		t.Fatalf("expected 5s, got %v", cfg.timeout)
	}
}

// --- Domain/Code constants ---

func TestDomainConstant(t *testing.T) {
	if DomainTimeout != "TIMEOUT" {
		t.Fatalf("expected TIMEOUT, got %s", DomainTimeout)
	}
}

func TestCodeConstants(t *testing.T) {
	codes := map[string]string{
		"CodeDeadlineExceeded": CodeDeadlineExceeded,
		"CodeCancelled":        CodeCancelled,
	}
	want := map[string]string{
		"CodeDeadlineExceeded": "DEADLINE_EXCEEDED",
		"CodeCancelled":        "CANCELLED",
	}
	for name, got := range codes {
		if got != want[name] {
			t.Errorf("%s = %q, want %q", name, got, want[name])
		}
	}
}

// --- Error constructors ---

func TestErrDeadlineExceeded(t *testing.T) {
	e := errDeadlineExceeded("test.op")
	if e.Domain != DomainTimeout || e.Code != CodeDeadlineExceeded {
		t.Fatalf("expected TIMEOUT/DEADLINE_EXCEEDED, got %s/%s", e.Domain, e.Code)
	}
	if e.Op != "test.op" {
		t.Fatalf("expected op=test.op, got %s", e.Op)
	}
}

func TestErrCancelled(t *testing.T) {
	cause := context.Canceled
	e := errCancelled("test.op", cause)
	if e.Domain != DomainTimeout || e.Code != CodeCancelled {
		t.Fatalf("expected TIMEOUT/CANCELLED, got %s/%s", e.Domain, e.Code)
	}
	if !errors.Is(e, cause) {
		t.Fatal("expected to wrap cause")
	}
}
