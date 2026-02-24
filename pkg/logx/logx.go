// Package logx provides a structured logging bridge between [log/slog],
// [ctxx] trace propagation, and [errx] structured errors.
//
// [NewHandler] wraps any [slog.Handler] and automatically injects TraceID
// and SpanID from context into every log record. [Err] converts an error
// (especially [*errx.Error]) into a rich [slog.Attr] group. [FromContext]
// and [WithLogger] store/retrieve a logger in context.
//
//	h := logx.NewHandler(slog.NewJSONHandler(os.Stdout, nil))
//	logger := slog.New(h)
//	ctx := logx.WithLogger(ctx, logger)
//
//	logx.FromContext(ctx).Error("request failed", logx.Err(err))
package logx

import (
	"context"
	"log/slog"

	"github.com/aasyanov/urx/pkg/ctxx"
	"github.com/aasyanov/urx/pkg/errx"
)

// --- Context keys ---

// ctxKey is the unexported type for context keys, preventing collisions with other packages.
type ctxKey string

// loggerKey is the context key under which the logger is stored.
const loggerKey ctxKey = "logx_logger"

// --- Context helpers ---

// WithLogger stores a [*slog.Logger] in the context.
// A nil ctx is treated as [context.Background].
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, loggerKey, l)
}

// FromContext returns the logger stored in ctx, or [slog.Default] if none.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// --- Error helper ---

// Err converts an error into a [slog.Attr]. If err is an [*errx.Error],
// it expands into a structured group with domain, code, severity, op,
// trace IDs, meta, and cause. Otherwise it returns a plain string attr.
// Returns an empty attr if err is nil.
func Err(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}

	xe, ok := errx.As(err)
	if !ok {
		return slog.String("error", err.Error())
	}

	attrs := []any{
		slog.String("domain", xe.Domain),
		slog.String("code", xe.Code),
		slog.String("message", xe.Message),
		slog.String("severity", xe.Severity.String()),
	}
	if xe.Op != "" {
		attrs = append(attrs, slog.String("op", xe.Op))
	}
	if xe.TraceID != "" {
		attrs = append(attrs, slog.String("trace_id", xe.TraceID))
	}
	if xe.SpanID != "" {
		attrs = append(attrs, slog.String("span_id", xe.SpanID))
	}
	if xe.IsPanic() {
		attrs = append(attrs, slog.Bool("panic", true))
	}
	if len(xe.Meta) > 0 {
		attrs = append(attrs, slog.Any("meta", xe.Meta))
	}
	if xe.Cause != nil {
		attrs = append(attrs, slog.String("cause", xe.Cause.Error()))
	}
	return slog.Group("error", attrs...)
}

// --- Handler ---

// Handler is a [slog.Handler] wrapper that injects trace_id and span_id
// from context into every log record.
type Handler struct {
	inner slog.Handler
}

// NewHandler creates a [Handler] wrapping inner. Every record passing
// through will be enriched with trace_id and span_id from context (if
// present).
func NewHandler(inner slog.Handler) *Handler {
	return &Handler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle injects trace_id and span_id from ctx, then delegates.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if ctx != nil {
		traceID, spanID := ctxx.TraceFromContext(ctx)
		if traceID != "" {
			r.AddAttrs(slog.String("trace_id", traceID))
		}
		if spanID != "" {
			r.AddAttrs(slog.String("span_id", spanID))
		}
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs returns a new Handler with the given attrs pre-applied.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup returns a new Handler with the given group name.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name)}
}
