// Package ctxx provides distributed-trace propagation through [context.Context].
//
// Every request entering the system should call [WithTrace] once at the
// boundary (HTTP handler, gRPC interceptor, message consumer). Downstream
// code reads identifiers with [TraceFromContext] and creates child spans
// with [WithSpan]:
//
//	ctx := ctxx.WithTrace(ctx)                     // entry point
//	traceID, spanID := ctxx.TraceFromContext(ctx)   // read
//	ctx = ctxx.WithSpan(ctx)                        // child operation
//
// All functions are safe to call with a nil [context.Context].
//
package ctxx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

// --- Context keys ---

// ctxKey is the unexported type for context keys, preventing collisions with other packages.
type ctxKey string

// Context value keys for trace propagation.
const (
	traceIDKey  ctxKey = "trace_id"
	spanIDKey   ctxKey = "span_id"
	traceCtxKey ctxKey = "trace_ctx"
)

// --- Trace context ---

// HeaderTraceparent is the W3C trace context header name.
const HeaderTraceparent = "traceparent"

const (
	defaultTraceFlags = "01" // sampled
	version00         = "00"
)

// TraceContext is the minimal W3C-compatible trace state carried by this package.
type TraceContext struct {
	TraceID    string
	SpanID     string
	TraceFlags string
}

// --- Trace propagation ---

// WithTrace returns a context carrying TraceID and SpanID.
// If ctx already contains both identifiers they are preserved unchanged.
// A nil ctx is treated as [context.Background].
func WithTrace(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if tc, ok := traceContextFromContext(ctx); ok {
		return setTraceContext(ctx, tc)
	}
	return setTraceContext(ctx, TraceContext{
		TraceID:    newTraceID(),
		SpanID:     newSpanID(),
		TraceFlags: defaultTraceFlags,
	})
}

// WithSpan returns a context with a new SpanID while preserving the existing
// TraceID. If no TraceID is present, both TraceID and SpanID are generated
// (equivalent to [WithTrace]).
// A nil ctx is treated as [context.Background].
func WithSpan(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	tc, ok := traceContextFromContext(ctx)
	if !ok {
		return WithTrace(ctx)
	}
	tc.SpanID = newSpanID()
	return setTraceContext(ctx, tc)
}

// TraceFromContext extracts TraceID and SpanID from ctx.
// Returns empty strings when ctx is nil or the values are absent.
func TraceFromContext(ctx context.Context) (traceID, spanID string) {
	if tc, ok := traceContextFromContext(ctx); ok {
		return tc.TraceID, tc.SpanID
	}
	return "", ""
}

// TraceFlagsFromContext returns W3C trace-flags (2 hex chars), or empty when absent.
func TraceFlagsFromContext(ctx context.Context) string {
	if tc, ok := traceContextFromContext(ctx); ok {
		return tc.TraceFlags
	}
	return ""
}

// ParseTraceparent parses a W3C traceparent header value:
// "00-<trace-id>-<span-id>-<trace-flags>".
func ParseTraceparent(value string) (traceID, spanID, traceFlags string, ok bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 {
		return "", "", "", false
	}
	version := strings.ToLower(parts[0])
	traceID = strings.ToLower(parts[1])
	spanID = strings.ToLower(parts[2])
	traceFlags = strings.ToLower(parts[3])

	if version != version00 {
		return "", "", "", false
	}
	if !validTraceID(traceID) || !validSpanID(spanID) || !validTraceFlags(traceFlags) {
		return "", "", "", false
	}
	return traceID, spanID, traceFlags, true
}

// FormatTraceparent returns a W3C traceparent string for valid identifiers.
// Returns an empty string if any field is invalid.
func FormatTraceparent(traceID, spanID, traceFlags string) string {
	traceID = strings.ToLower(traceID)
	spanID = strings.ToLower(spanID)
	traceFlags = strings.ToLower(traceFlags)
	if !validTraceID(traceID) || !validSpanID(spanID) || !validTraceFlags(traceFlags) {
		return ""
	}
	return version00 + "-" + traceID + "-" + spanID + "-" + traceFlags
}

// WithTraceparent parses and stores a W3C traceparent value in context.
// If value is invalid, a new trace is generated via [WithTrace].
func WithTraceparent(ctx context.Context, value string) context.Context {
	traceID, spanID, traceFlags, ok := ParseTraceparent(value)
	if !ok {
		return WithTrace(ctx)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return setTraceContext(ctx, TraceContext{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: traceFlags,
	})
}

// InjectTraceparent writes traceparent to HTTP headers if trace exists in ctx.
func InjectTraceparent(ctx context.Context, h http.Header) {
	if h == nil {
		return
	}
	tc, ok := traceContextFromContext(ctx)
	if !ok {
		return
	}
	h.Set(HeaderTraceparent, FormatTraceparent(tc.TraceID, tc.SpanID, tc.TraceFlags))
}

// ExtractTraceparent reads traceparent from HTTP headers into context.
// If header is missing/invalid, it generates a new trace via [WithTrace].
func ExtractTraceparent(ctx context.Context, h http.Header) context.Context {
	if h == nil {
		return WithTrace(ctx)
	}
	return WithTraceparent(ctx, h.Get(HeaderTraceparent))
}

// MustTraceFromContext returns TraceID, SpanID, and a (possibly new) context.
// If ctx is missing trace identifiers, new ones are generated via [WithTrace]
// and the enriched context is returned. A nil ctx is treated as
// [context.Background].
func MustTraceFromContext(ctx context.Context) (traceID, spanID string, newCtx context.Context) {
	traceID, spanID = TraceFromContext(ctx)
	if traceID == "" || spanID == "" {
		newCtx = WithTrace(ctx)
		traceID, spanID = TraceFromContext(newCtx)
		return
	}
	newCtx = ctx
	return
}

func traceContextFromContext(ctx context.Context) (TraceContext, bool) {
	if ctx == nil {
		return TraceContext{}, false
	}
	if v := ctx.Value(traceCtxKey); v != nil {
		if tc, ok := v.(TraceContext); ok && validTraceID(tc.TraceID) && validSpanID(tc.SpanID) {
			if !validTraceFlags(tc.TraceFlags) {
				tc.TraceFlags = defaultTraceFlags
			}
			tc.TraceID = strings.ToLower(tc.TraceID)
			tc.SpanID = strings.ToLower(tc.SpanID)
			tc.TraceFlags = strings.ToLower(tc.TraceFlags)
			return tc, true
		}
	}

	// Legacy fallback: trace_id / span_id as separate string keys.
	var traceID, spanID string
	if v := ctx.Value(traceIDKey); v != nil {
		traceID, _ = v.(string)
	}
	if v := ctx.Value(spanIDKey); v != nil {
		spanID, _ = v.(string)
	}
	traceID = strings.ToLower(traceID)
	spanID = strings.ToLower(spanID)
	if validTraceID(traceID) && validSpanID(spanID) {
		return TraceContext{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: defaultTraceFlags,
		}, true
	}
	return TraceContext{}, false
}

func setTraceContext(ctx context.Context, tc TraceContext) context.Context {
	return context.WithValue(ctx, traceCtxKey, tc)
}

func newTraceID() string { return newHexID(16) }

func newSpanID() string { return newHexID(8) }

func newHexID(bytesLen int) string {
	for {
		buf := make([]byte, bytesLen)
		if _, err := rand.Read(buf); err != nil {
			// Fallback still keeps shape valid even in the unlikely RNG failure case.
			return strings.Repeat("f", bytesLen*2)
		}
		if !allZero(buf) {
			return hex.EncodeToString(buf)
		}
	}
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func validTraceID(s string) bool {
	return validHexLen(s, 32) && !allZeroHex(s)
}

func validSpanID(s string) bool {
	return validHexLen(s, 16) && !allZeroHex(s)
}

func validTraceFlags(s string) bool {
	return validHexLen(s, 2)
}

func validHexLen(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

func allZeroHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '0' {
			return false
		}
	}
	return true
}
