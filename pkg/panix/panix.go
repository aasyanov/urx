// Package panix provides panic-safe execution wrappers for industrial Go services.
//
// [Safe] executes a function and converts any recovered panic into a structured
// [errx.Error] with trace identifiers from the context. The generic signature
// supports both value-returning and error-only functions:
//
//	user, err := panix.Safe(ctx, "UserRepo.Find", func(ctx context.Context) (User, error) {
//	    return repo.Find(ctx, id)
//	})
//
// [SafeGo] does the same for goroutines, with an optional error callback.
// [Wrap] returns a panic-safe version of any function.
//
// All functions are safe to call with a nil [context.Context].
package panix

import (
	"context"

	"github.com/aasyanov/urx/pkg/ctxx"
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Config & options ---

// Option configures [SafeGo] behavior.
type Option func(*config)

// config holds [SafeGo] options.
type config struct {
	onError func(ctx context.Context, err error)
}

// WithOnError sets a callback invoked when [SafeGo]'s function panics.
// The callback receives the original context and the recovered error
// (which is an [*errx.Error]).
func WithOnError(fn func(ctx context.Context, err error)) Option {
	return func(c *config) {
		c.onError = fn
	}
}

// --- Panic-safe execution ---

// Safe executes fn and recovers any panic, converting it into a structured
// [*errx.Error] with TraceID/SpanID extracted from ctx.
// If fn returns a non-nil error without panicking, that error is returned as-is.
// A nil ctx is treated as [context.Background].
func Safe[T any](ctx context.Context, op string, fn func(ctx context.Context) (T, error)) (val T, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if r := recover(); r != nil {
			traceID, spanID, _ := ctxx.MustTraceFromContext(ctx)
			e := errx.NewPanicError(op, r)
			e.TraceID = traceID
			e.SpanID = spanID
			var zero T
			val = zero
			err = e
		}
	}()
	return fn(ctx)
}

// SafeGo launches a goroutine with panic recovery. If the function panics or
// returns an error, and [WithOnError] was provided, the callback is invoked.
// Without a callback, errors are silently discarded (fire-and-forget).
// A nil ctx is treated as [context.Background].
func SafeGo(ctx context.Context, op string, fn func(ctx context.Context), opts ...Option) {
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}
	safeCtx := ctx
	if safeCtx == nil {
		safeCtx = context.Background()
	}
	go func() {
		_, err := Safe[struct{}](safeCtx, op, func(ctx context.Context) (struct{}, error) {
			fn(ctx)
			return struct{}{}, nil
		})
		if err != nil && cfg.onError != nil {
			cfg.onError(safeCtx, err)
		}
	}()
}

// --- Middleware wrapper ---

// Wrap returns a panic-safe version of fn. Each call to the returned function
// delegates to [Safe] with the given operation name.
func Wrap[T any](fn func(ctx context.Context) (T, error), op string) func(ctx context.Context) (T, error) {
	return func(ctx context.Context) (T, error) {
		return Safe(ctx, op, fn)
	}
}
