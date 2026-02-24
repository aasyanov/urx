package ctxx

import (
	"context"
	"net/http"
	"regexp"
	"testing"
)

var (
	traceIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)
	spanIDRe  = regexp.MustCompile(`^[0-9a-f]{16}$`)
	flagsRe   = regexp.MustCompile(`^[0-9a-f]{2}$`)
)

func isTraceID(s string) bool {
	return traceIDRe.MatchString(s) && s != "00000000000000000000000000000000"
}
func isSpanID(s string) bool  { return spanIDRe.MatchString(s) && s != "0000000000000000" }
func isFlags(s string) bool   { return flagsRe.MatchString(s) }
func nilCtx() context.Context { return nil }

// --- WithTrace ---

func TestWithTrace_NilCtx(t *testing.T) {
	ctx := WithTrace(nilCtx())
	traceID, spanID := TraceFromContext(ctx)
	if !isTraceID(traceID) {
		t.Errorf("traceID = %q, want 32-hex trace ID", traceID)
	}
	if !isSpanID(spanID) {
		t.Errorf("spanID = %q, want 16-hex span ID", spanID)
	}
	if f := TraceFlagsFromContext(ctx); !isFlags(f) {
		t.Errorf("trace flags = %q, want 2-hex flags", f)
	}
}

func TestWithTrace_EmptyCtx(t *testing.T) {
	ctx := WithTrace(context.Background())
	traceID, spanID := TraceFromContext(ctx)
	if !isTraceID(traceID) || !isSpanID(spanID) {
		t.Errorf("expected W3C IDs, got trace=%q span=%q", traceID, spanID)
	}
}

func TestWithTrace_PreservesExisting(t *testing.T) {
	ctx := WithTrace(context.Background())
	origTrace, origSpan := TraceFromContext(ctx)

	ctx2 := WithTrace(ctx)
	trace2, span2 := TraceFromContext(ctx2)

	if trace2 != origTrace {
		t.Errorf("traceID changed: %q -> %q", origTrace, trace2)
	}
	if span2 != origSpan {
		t.Errorf("spanID changed: %q -> %q", origSpan, span2)
	}
}

func TestWithTrace_UniquePerCall(t *testing.T) {
	ctx1 := WithTrace(context.Background())
	ctx2 := WithTrace(context.Background())

	t1, s1 := TraceFromContext(ctx1)
	t2, s2 := TraceFromContext(ctx2)

	if t1 == t2 {
		t.Error("two WithTrace calls produced the same traceID")
	}
	if s1 == s2 {
		t.Error("two WithTrace calls produced the same spanID")
	}
}

func TestWithTrace_PartialTrace_OnlyTraceID(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceIDKey, "existing-trace")
	ctx = WithTrace(ctx)
	traceID, spanID := TraceFromContext(ctx)
	if !isTraceID(traceID) {
		t.Errorf("traceID = %q, expected regenerated trace ID", traceID)
	}
	if !isSpanID(spanID) {
		t.Errorf("spanID = %q, expected regenerated span ID", spanID)
	}
}

func TestWithTrace_PartialTrace_OnlySpanID(t *testing.T) {
	ctx := context.WithValue(context.Background(), spanIDKey, "existing-span")
	ctx = WithTrace(ctx)
	traceID, spanID := TraceFromContext(ctx)
	if !isTraceID(traceID) {
		t.Errorf("traceID = %q, expected regenerated trace ID", traceID)
	}
	if !isSpanID(spanID) {
		t.Errorf("spanID = %q, expected regenerated span ID", spanID)
	}
}

// --- WithSpan ---

func TestWithSpan_NilCtx(t *testing.T) {
	ctx := WithSpan(nilCtx())
	traceID, spanID := TraceFromContext(ctx)
	if !isTraceID(traceID) {
		t.Errorf("traceID = %q, want trace ID", traceID)
	}
	if !isSpanID(spanID) {
		t.Errorf("spanID = %q, want span ID", spanID)
	}
}

func TestWithSpan_NoExistingTrace(t *testing.T) {
	ctx := WithSpan(context.Background())
	traceID, spanID := TraceFromContext(ctx)
	if !isTraceID(traceID) || !isSpanID(spanID) {
		t.Errorf("expected IDs, got trace=%q span=%q", traceID, spanID)
	}
}

func TestWithSpan_PreservesTraceID(t *testing.T) {
	ctx := WithTrace(context.Background())
	origTrace, origSpan := TraceFromContext(ctx)

	ctx2 := WithSpan(ctx)
	newTrace, newSpan := TraceFromContext(ctx2)

	if newTrace != origTrace {
		t.Errorf("traceID changed: %q -> %q", origTrace, newTrace)
	}
	if newSpan == origSpan {
		t.Error("spanID should be different after WithSpan")
	}
	if !isSpanID(newSpan) {
		t.Errorf("new spanID = %q, want span ID", newSpan)
	}
}

func TestWithSpan_MultipleSpans(t *testing.T) {
	ctx := WithTrace(context.Background())
	origTrace, _ := TraceFromContext(ctx)

	spans := make(map[string]bool)
	for range 10 {
		ctx2 := WithSpan(ctx)
		_, span := TraceFromContext(ctx2)
		if spans[span] {
			t.Fatalf("duplicate spanID: %q", span)
		}
		spans[span] = true

		trace, _ := TraceFromContext(ctx2)
		if trace != origTrace {
			t.Fatalf("traceID changed in span iteration")
		}
	}
}

// --- TraceFromContext ---

func TestTraceFromContext_NilCtx(t *testing.T) {
	traceID, spanID := TraceFromContext(nilCtx())
	if traceID != "" || spanID != "" {
		t.Errorf("expected empty strings, got trace=%q span=%q", traceID, spanID)
	}
}

func TestTraceFromContext_EmptyCtx(t *testing.T) {
	traceID, spanID := TraceFromContext(context.Background())
	if traceID != "" || spanID != "" {
		t.Errorf("expected empty strings, got trace=%q span=%q", traceID, spanID)
	}
}

func TestTraceFromContext_WithValues(t *testing.T) {
	ctx := WithTrace(context.Background())
	traceID, spanID := TraceFromContext(ctx)
	if traceID == "" || spanID == "" {
		t.Error("expected non-empty trace identifiers")
	}
}

func TestTraceFromContext_WrongValueType(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceIDKey, 12345)
	ctx = context.WithValue(ctx, spanIDKey, struct{}{})
	traceID, spanID := TraceFromContext(ctx)
	if traceID != "" || spanID != "" {
		t.Errorf("non-string values should yield empty, got trace=%q span=%q", traceID, spanID)
	}
}

// --- MustTraceFromContext ---

func TestMustTraceFromContext_NilCtx(t *testing.T) {
	traceID, spanID, ctx := MustTraceFromContext(nilCtx())
	if !isTraceID(traceID) || !isSpanID(spanID) {
		t.Errorf("expected IDs, got trace=%q span=%q", traceID, spanID)
	}
	if ctx == nil {
		t.Fatal("returned context should not be nil")
	}
	t2, s2 := TraceFromContext(ctx)
	if t2 != traceID || s2 != spanID {
		t.Error("returned context should contain the generated IDs")
	}
}

func TestMustTraceFromContext_EmptyCtx(t *testing.T) {
	traceID, spanID, ctx := MustTraceFromContext(context.Background())
	if !isTraceID(traceID) || !isSpanID(spanID) {
		t.Errorf("expected IDs, got trace=%q span=%q", traceID, spanID)
	}
	t2, s2 := TraceFromContext(ctx)
	if t2 != traceID || s2 != spanID {
		t.Error("returned context should contain the generated IDs")
	}
}

func TestMustTraceFromContext_ExistingTrace(t *testing.T) {
	origCtx := WithTrace(context.Background())
	origTrace, origSpan := TraceFromContext(origCtx)

	traceID, spanID, ctx := MustTraceFromContext(origCtx)
	if traceID != origTrace || spanID != origSpan {
		t.Error("should return existing IDs")
	}
	if ctx != origCtx {
		t.Error("should return the original context when IDs exist")
	}
}

func TestMustTraceFromContext_PartialTrace(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceIDKey, "only-trace")
	traceID, spanID, newCtx := MustTraceFromContext(ctx)
	if !isTraceID(traceID) || !isSpanID(spanID) {
		t.Errorf("partial trace should regenerate, got trace=%q span=%q", traceID, spanID)
	}
	if newCtx == ctx {
		t.Error("should return a new context when IDs were missing")
	}
}

// --- Context key isolation ---

func TestContextKeys_NoCollision(t *testing.T) {
	ctx := context.WithValue(context.Background(), "trace_id", "plain-string-key") //nolint:staticcheck // SA1029: intentionally testing plain string key
	ctx = WithTrace(ctx)
	traceID, spanID := TraceFromContext(ctx)
	if !isTraceID(traceID) || !isSpanID(spanID) {
		t.Error("typed ctxKey should not collide with plain string key")
	}
}

// --- traceparent ---

func TestFormatAndParseTraceparent_Roundtrip(t *testing.T) {
	ctx := WithTrace(context.Background())
	traceID, spanID := TraceFromContext(ctx)
	flags := TraceFlagsFromContext(ctx)
	tp := FormatTraceparent(traceID, spanID, flags)
	if tp == "" {
		t.Fatal("expected non-empty traceparent")
	}
	rtTrace, rtSpan, rtFlags, ok := ParseTraceparent(tp)
	if !ok {
		t.Fatal("expected parse success")
	}
	if rtTrace != traceID || rtSpan != spanID || rtFlags != flags {
		t.Fatalf("roundtrip mismatch: %s/%s/%s vs %s/%s/%s", traceID, spanID, flags, rtTrace, rtSpan, rtFlags)
	}
}

func TestParseTraceparent_Invalid(t *testing.T) {
	cases := []string{
		"",
		"00-xyz-123-01",
		"00-00000000000000000000000000000000-1111111111111111-01",
		"00-11111111111111111111111111111111-0000000000000000-01",
		"01-11111111111111111111111111111111-1111111111111111-01",
	}
	for _, tc := range cases {
		if _, _, _, ok := ParseTraceparent(tc); ok {
			t.Fatalf("expected invalid traceparent: %q", tc)
		}
	}
}

func TestInjectExtractTraceparent(t *testing.T) {
	src := WithTrace(context.Background())
	h := http.Header{}
	InjectTraceparent(src, h)

	dst := ExtractTraceparent(context.Background(), h)
	srcTrace, srcSpan := TraceFromContext(src)
	dstTrace, dstSpan := TraceFromContext(dst)
	if srcTrace != dstTrace || srcSpan != dstSpan {
		t.Fatalf("expected propagated IDs, got src=%s/%s dst=%s/%s", srcTrace, srcSpan, dstTrace, dstSpan)
	}
}

func TestWithTraceparent_InvalidGeneratesTrace(t *testing.T) {
	ctx := WithTraceparent(context.Background(), "bad-header")
	traceID, spanID := TraceFromContext(ctx)
	if !isTraceID(traceID) || !isSpanID(spanID) {
		t.Fatalf("expected generated IDs, got %q %q", traceID, spanID)
	}
}

// --- Additional coverage ---

func TestTraceFlagsFromContext_NilCtx(t *testing.T) {
	f := TraceFlagsFromContext(nilCtx())
	if f != "" {
		t.Errorf("expected empty flags for nil ctx, got %q", f)
	}
}

func TestTraceFlagsFromContext_EmptyCtx(t *testing.T) {
	f := TraceFlagsFromContext(context.Background())
	if f != "" {
		t.Errorf("expected empty flags for empty ctx, got %q", f)
	}
}

func TestFormatTraceparent_InvalidInputs(t *testing.T) {
	cases := []struct {
		traceID, spanID, flags string
	}{
		{"short", "1111111111111111", "01"},
		{"11111111111111111111111111111111", "short", "01"},
		{"11111111111111111111111111111111", "1111111111111111", "zz"},
		{"00000000000000000000000000000000", "1111111111111111", "01"},
		{"11111111111111111111111111111111", "0000000000000000", "01"},
	}
	for _, tc := range cases {
		if got := FormatTraceparent(tc.traceID, tc.spanID, tc.flags); got != "" {
			t.Errorf("FormatTraceparent(%q, %q, %q) = %q, want empty", tc.traceID, tc.spanID, tc.flags, got)
		}
	}
}

func TestWithTraceparent_NilCtx_ValidHeader(t *testing.T) {
	tp := "00-11111111111111111111111111111111-2222222222222222-01"
	ctx := WithTraceparent(nilCtx(), tp)
	traceID, spanID := TraceFromContext(ctx)
	if traceID != "11111111111111111111111111111111" {
		t.Errorf("traceID = %q, want 111...", traceID)
	}
	if spanID != "2222222222222222" {
		t.Errorf("spanID = %q, want 222...", spanID)
	}
}

func TestInjectTraceparent_NilHeader(t *testing.T) {
	ctx := WithTrace(context.Background())
	InjectTraceparent(ctx, nil)
}

func TestInjectTraceparent_NoTrace(t *testing.T) {
	h := http.Header{}
	InjectTraceparent(context.Background(), h)
	if got := h.Get(HeaderTraceparent); got != "" {
		t.Errorf("expected no traceparent header, got %q", got)
	}
}

func TestExtractTraceparent_NilHeader(t *testing.T) {
	ctx := ExtractTraceparent(context.Background(), nil)
	traceID, spanID := TraceFromContext(ctx)
	if !isTraceID(traceID) || !isSpanID(spanID) {
		t.Fatalf("expected generated IDs for nil header, got %q %q", traceID, spanID)
	}
}

func TestValidHexLen_InvalidChars(t *testing.T) {
	if validHexLen("zzzz", 4) {
		t.Error("expected false for non-hex chars")
	}
	if validHexLen("GG", 2) {
		t.Error("expected false for uppercase non-hex")
	}
}

func TestTraceContextFromContext_InvalidTraceFlags_Normalized(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceCtxKey, TraceContext{
		TraceID:    "11111111111111111111111111111111",
		SpanID:     "2222222222222222",
		TraceFlags: "not-valid",
	})
	tc, ok := traceContextFromContext(ctx)
	if !ok {
		t.Fatal("expected valid trace context with bad flags to be normalized")
	}
	if tc.TraceFlags != defaultTraceFlags {
		t.Errorf("expected flags to be normalized to %q, got %q", defaultTraceFlags, tc.TraceFlags)
	}
}

func TestTraceContextFromContext_WrongValueType(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceCtxKey, "not-a-TraceContext")
	_, ok := traceContextFromContext(ctx)
	if ok {
		t.Error("expected false for wrong value type at traceCtxKey")
	}
}

func TestTraceContextFromContext_LegacyFallback(t *testing.T) {
	traceID := "aabbccddaabbccddaabbccddaabbccdd"
	spanID := "1122334411223344"
	ctx := context.WithValue(context.Background(), traceIDKey, traceID)
	ctx = context.WithValue(ctx, spanIDKey, spanID)
	tc, ok := traceContextFromContext(ctx)
	if !ok {
		t.Fatal("expected legacy fallback to find trace context")
	}
	if tc.TraceID != traceID {
		t.Errorf("traceID = %q, want %q", tc.TraceID, traceID)
	}
	if tc.SpanID != spanID {
		t.Errorf("spanID = %q, want %q", tc.SpanID, spanID)
	}
	if tc.TraceFlags != defaultTraceFlags {
		t.Errorf("traceFlags = %q, want default %q", tc.TraceFlags, defaultTraceFlags)
	}
}

func TestTraceContextFromContext_LegacyInvalid(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceIDKey, "short")
	ctx = context.WithValue(ctx, spanIDKey, "short")
	_, ok := traceContextFromContext(ctx)
	if ok {
		t.Error("expected false for invalid legacy trace IDs")
	}
}

func TestAllZero_EmptySlice(t *testing.T) {
	if !allZero([]byte{}) {
		t.Error("expected allZero(empty) = true")
	}
}

func TestTraceContextFromContext_UppercaseNormalization(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceCtxKey, TraceContext{
		TraceID:    "AABBCCDD11223344AABBCCDD11223344",
		SpanID:     "AABBCCDD11223344",
		TraceFlags: "0A",
	})
	tc, ok := traceContextFromContext(ctx)
	if !ok {
		t.Fatal("expected valid trace context with uppercase hex")
	}
	if tc.TraceID != "aabbccdd11223344aabbccdd11223344" {
		t.Errorf("traceID not lowercased: %q", tc.TraceID)
	}
	if tc.SpanID != "aabbccdd11223344" {
		t.Errorf("spanID not lowercased: %q", tc.SpanID)
	}
	if tc.TraceFlags != "0a" {
		t.Errorf("traceFlags not lowercased: %q", tc.TraceFlags)
	}
}
