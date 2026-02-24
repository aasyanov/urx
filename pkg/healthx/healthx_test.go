package healthx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func nilCtx() context.Context { return nil }

// ============================================================
// Liveness
// ============================================================

func TestLiveness_Up(t *testing.T) {
	c := New()
	r := c.Liveness(context.Background())
	if r.Status != StatusUp {
		t.Fatalf("expected up, got %s", r.Status)
	}
}

func TestLiveness_Down(t *testing.T) {
	c := New()
	c.MarkDown()
	r := c.Liveness(context.Background())
	if r.Status != StatusDown {
		t.Fatalf("expected down, got %s", r.Status)
	}
}

func TestLiveness_MarkUpAfterDown(t *testing.T) {
	c := New()
	c.MarkDown()
	c.MarkUp()
	r := c.Liveness(context.Background())
	if r.Status != StatusUp {
		t.Fatalf("expected up after MarkUp, got %s", r.Status)
	}
}

// ============================================================
// Readiness
// ============================================================

func TestReadiness_NoChecks(t *testing.T) {
	c := New()
	r := c.Readiness(context.Background())
	if r.Status != StatusUp {
		t.Fatalf("expected up with no checks, got %s", r.Status)
	}
}

func TestReadiness_AllHealthy(t *testing.T) {
	c := New()
	c.Register("db", func(ctx context.Context) error { return nil })
	c.Register("cache", func(ctx context.Context) error { return nil })

	r := c.Readiness(context.Background())
	if r.Status != StatusUp {
		t.Fatalf("expected up, got %s", r.Status)
	}
	if len(r.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(r.Components))
	}
	for name, cs := range r.Components {
		if cs.Status != StatusUp {
			t.Errorf("component %s expected up, got %s", name, cs.Status)
		}
	}
}

func TestReadiness_OneUnhealthy(t *testing.T) {
	c := New()
	c.Register("db", func(ctx context.Context) error { return errors.New("connection refused") })
	c.Register("cache", func(ctx context.Context) error { return nil })

	r := c.Readiness(context.Background())
	if r.Status != StatusDown {
		t.Fatalf("expected down, got %s", r.Status)
	}
	if r.Components["db"].Status != StatusDown {
		t.Fatal("expected db to be down")
	}
	if r.Components["db"].Error == "" {
		t.Fatal("expected error message for db")
	}
	if r.Components["cache"].Status != StatusUp {
		t.Fatal("expected cache to be up")
	}
}

func TestReadiness_MarkedDown(t *testing.T) {
	c := New()
	c.Register("db", func(ctx context.Context) error { return nil })
	c.MarkDown()

	r := c.Readiness(context.Background())
	if r.Status != StatusDown {
		t.Fatalf("expected down when manually marked, got %s", r.Status)
	}
}

func TestReadiness_Timeout(t *testing.T) {
	c := New(WithTimeout(50 * time.Millisecond))
	c.Register("slow", func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return nil
		}
	})

	r := c.Readiness(context.Background())
	if r.Status != StatusDown {
		t.Fatalf("expected down due to timeout, got %s", r.Status)
	}
	if r.Components["slow"].Status != StatusDown {
		t.Fatal("expected slow component to be down")
	}
}

// ============================================================
// HTTP handlers
// ============================================================

func TestLiveHandler_200(t *testing.T) {
	c := New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	c.LiveHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var report Report
	json.NewDecoder(rec.Body).Decode(&report)
	if report.Status != StatusUp {
		t.Fatalf("expected up, got %s", report.Status)
	}
}

func TestLiveHandler_503(t *testing.T) {
	c := New()
	c.MarkDown()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	c.LiveHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestReadyHandler_200(t *testing.T) {
	c := New()
	c.Register("db", func(ctx context.Context) error { return nil })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)
	c.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestReadyHandler_503(t *testing.T) {
	c := New()
	c.Register("db", func(ctx context.Context) error { return errors.New("down") })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)
	c.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestReadyHandler_JSON(t *testing.T) {
	c := New()
	c.Register("db", func(ctx context.Context) error { return nil })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)
	c.ReadyHandler().ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}
}

func TestRegisterHandlers_StandardPaths(t *testing.T) {
	c := New()
	mux := http.NewServeMux()
	c.RegisterHandlers(mux)

	for _, path := range []string{"/healthz", "/livez", "/readyz"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rec.Code)
		}
	}
}

func TestRegisterHandlers_DownStatus(t *testing.T) {
	c := New()
	c.MarkDown()
	mux := http.NewServeMux()
	c.RegisterHandlers(mux)

	for _, path := range []string{"/healthz", "/livez", "/readyz"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: expected 503, got %d", path, rec.Code)
		}
	}
}

func TestRegisterHandlers_NilMux_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil mux")
		}
	}()
	c := New()
	c.RegisterHandlers(nil)
}

// ============================================================
// Concurrency
// ============================================================

func TestRegister_Concurrent(t *testing.T) {
	c := New()
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func(i int) {
			c.Register("check", func(ctx context.Context) error { return nil })
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 50; i++ {
		<-done
	}
}

// ============================================================
// Error constructors
// ============================================================

func TestErrUnhealthy(t *testing.T) {
	cause := errors.New("connection refused")
	e := errUnhealthy("postgres", cause)
	if e.Domain != DomainHealth || e.Code != CodeUnhealthy {
		t.Fatalf("expected HEALTH/UNHEALTHY, got %s/%s", e.Domain, e.Code)
	}
	if e.Meta["component"] != "postgres" {
		t.Fatalf("expected component=postgres, got %v", e.Meta["component"])
	}
}

func TestErrTimeout(t *testing.T) {
	e := errTimeout("redis")
	if e.Domain != DomainHealth || e.Code != CodeTimeout {
		t.Fatalf("expected HEALTH/TIMEOUT, got %s/%s", e.Domain, e.Code)
	}
}

func TestReadiness_NilContext(t *testing.T) {
	c := New()
	c.Register("ok", func(ctx context.Context) error { return nil })
	r := c.Readiness(nilCtx())
	if r.Status != StatusUp {
		t.Fatalf("expected up with nil ctx, got %s", r.Status)
	}
}

func TestRegister_NilCheck_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on nil check")
		}
	}()
	c := New()
	c.Register("bad", nil)
}
