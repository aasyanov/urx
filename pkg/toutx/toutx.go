// Package toutx provides a thread-safe timeout execution wrapper for
// industrial Go services.
//
// [Execute] runs a function within a deadline-scoped context. If the function
// does not complete before the timeout fires, the context is cancelled and a
// structured [errx.Error] with [CodeDeadlineExceeded] is returned.
//
//	resp, err := toutx.Execute(ctx, 5*time.Second, func(ctx context.Context) (*Response, error) {
//	    return client.Call(ctx, req)
//	})
//
// For stateful usage with pre-configured defaults, create a [Timer] via [New]
// and pass it to [Execute] via [WithTimer]:
//
//	t := toutx.New(
//	    toutx.WithTimeout(3*time.Second),
//	    toutx.WithOp("db.query"),
//	)
//	rows, err := toutx.Execute(ctx, 0, func(ctx context.Context) (*sql.Rows, error) {
//	    return db.QueryContext(ctx, sql)
//	}, toutx.WithTimer(t))
//
// Each call is wrapped with [panix.Safe] for panic recovery; panicked
// functions produce structured [errx.Error] values.
package toutx

import (
	"context"
	"time"

	"github.com/aasyanov/urx/pkg/panix"
)

// --- Configuration ---

// config holds timeout parameters.
type config struct {
	timeout time.Duration
	op      string
}

// defaultConfig returns sensible timeout defaults (30 s).
func defaultConfig() config {
	return config{
		timeout: 30 * time.Second,
	}
}

// --- Options ---

// Option configures [Execute] behavior.
type Option func(*config)

// WithTimeout sets the maximum duration the function may execute.
// Values <= 0 are ignored.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithOp sets the logical operation name attached to timeout errors
// (e.g. "db.query", "http.fetch").
func WithOp(op string) Option {
	return func(c *config) {
		c.op = op
	}
}

// --- Timer ---

// Timer is a reusable set of pre-configured defaults.
// Create one with [New], pass to [Execute] via [WithTimer].
type Timer struct {
	cfg config
}

// New creates a [Timer] with the given options applied on top of
// sensible defaults (30 s timeout).
func New(opts ...Option) *Timer {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Timer{cfg: cfg}
}

// WithTimer applies a [Timer]'s pre-configured defaults. Per-call options
// that follow in the variadic list override the timer's values.
func WithTimer(t *Timer) Option {
	return func(c *config) {
		if t != nil {
			*c = t.cfg
		}
	}
}

// --- Package-level execution ---

// Execute runs fn within the given timeout. The timeout argument is applied
// first; use 0 to rely entirely on options or a [Timer].
//
// The function receives a deadline-scoped context derived from ctx. If fn
// does not complete before the deadline, the context is cancelled and
// [CodeDeadlineExceeded] is returned.
//
// The function is wrapped with [panix.Safe] for panic recovery.
func Execute[T any](ctx context.Context, timeout time.Duration, fn func(ctx context.Context) (T, error), opts ...Option) (T, error) {
	cfg := defaultConfig()
	if timeout > 0 {
		cfg.timeout = timeout
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	tctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	type result struct {
		val T
		err error
	}

	done := make(chan result, 1)
	go func() {
		v, e := panix.Safe(tctx, opOrDefault(cfg.op), fn)
		done <- result{v, e}
	}()

	select {
	case r := <-done:
		return r.val, r.err
	case <-tctx.Done():
		var zero T
		if ctx.Err() != nil {
			return zero, errCancelled(cfg.op, ctx.Err())
		}
		return zero, errDeadlineExceeded(cfg.op)
	}
}

// opOrDefault returns op if non-empty, otherwise the default operation name.
func opOrDefault(op string) string {
	if op != "" {
		return op
	}
	return "toutx.Execute"
}
