package healthx

import (
	"context"
	"errors"
	"testing"
	"time"
)

// FuzzReadiness drives [Checker.Readiness] with fuzzed timeout and check names.
// It never panics and always returns a report with a non-empty duration string.
func FuzzReadiness(f *testing.F) {
	f.Add(int64(100), "db")
	f.Add(int64(0), "")
	f.Add(int64(-1), "cache")

	ctx := context.Background()
	f.Fuzz(func(t *testing.T, timeoutMs int64, name string) {
		timeout := time.Duration(timeoutMs) * time.Millisecond
		c := New(WithTimeout(timeout))
		if name != "" {
			c.Register(name, func(context.Context) error { return nil })
		}

		rep := c.Readiness(ctx)
		if rep.Duration == "" {
			t.Fatal("empty duration")
		}
		if rep.Status != StatusUp && rep.Status != StatusDown {
			t.Fatalf("unexpected status %q", rep.Status)
		}
	})
}

// FuzzReadinessWithFailingCheck ensures a failing check marks readiness down
// without panicking when the check returns a sentinel error.
func FuzzReadinessWithFailingCheck(f *testing.F) {
	f.Add("svc")
	sentinel := errors.New("fuzz-check-fail")

	f.Fuzz(func(t *testing.T, name string) {
		if name == "" {
			t.Skip("empty name")
		}
		c := New(WithTimeout(time.Second))
		c.Register(name, func(context.Context) error { return sentinel })

		rep := c.Readiness(context.Background())
		if rep.Status != StatusDown {
			t.Fatalf("expected down, got %q", rep.Status)
		}
	})
}
