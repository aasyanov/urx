package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/aasyanov/urx/pkg/ctxx"
	"github.com/aasyanov/urx/pkg/errx"
)

// --- FromContext / WithLogger ---

func TestFromContext_Default(t *testing.T) {
	l := FromContext(context.Background())
	if l == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestFromContext_Nil(t *testing.T) {
	l := FromContext(nil)
	if l == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestWithLogger_RoundTrip(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	ctx := WithLogger(context.Background(), logger)
	got := FromContext(ctx)
	if got != logger {
		t.Fatal("expected same logger")
	}
}

func TestWithLogger_NilContext(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	ctx := WithLogger(nil, logger)
	got := FromContext(ctx)
	if got != logger {
		t.Fatal("expected same logger after nil ctx normalization")
	}
}

// --- Err helper ---

func TestErr_Nil(t *testing.T) {
	attr := Err(nil)
	if attr.Key != "" {
		t.Fatalf("expected empty attr, got key=%q", attr.Key)
	}
}

func TestErr_PlainError(t *testing.T) {
	attr := Err(errors.New("plain"))
	if attr.Key != "error" {
		t.Fatalf("expected key=error, got %q", attr.Key)
	}
	if attr.Value.String() != "plain" {
		t.Fatalf("expected 'plain', got %q", attr.Value.String())
	}
}

func TestErr_ErrxError(t *testing.T) {
	xe := errx.New("AUTH", "UNAUTHORIZED", "token expired",
		errx.WithOp("AuthService.Verify"),
		errx.WithMeta("user_id", "u-123"),
	)
	attr := Err(xe)
	if attr.Key != "error" {
		t.Fatalf("expected key=error, got %q", attr.Key)
	}
	if attr.Value.Kind() != slog.KindGroup {
		t.Fatalf("expected group, got %v", attr.Value.Kind())
	}

	found := map[string]bool{}
	for _, a := range attr.Value.Group() {
		found[a.Key] = true
	}
	for _, key := range []string{"domain", "code", "message", "severity", "op", "meta"} {
		if !found[key] {
			t.Errorf("missing key %q in error group", key)
		}
	}
}

func TestErr_ErrxWrapped(t *testing.T) {
	cause := errors.New("db down")
	xe := errx.Wrap(cause, "REPO", "INTERNAL", "query failed")
	attr := Err(xe)
	found := map[string]bool{}
	for _, a := range attr.Value.Group() {
		found[a.Key] = true
	}
	if !found["cause"] {
		t.Error("missing 'cause' in error group")
	}
}

func TestErr_ErrxWithTraceAndPanic(t *testing.T) {
	xe := errx.New("AUTH", "UNAUTHORIZED", "expired",
		errx.WithTrace("tid-abc", "sid-xyz"),
	)
	attr := Err(xe)
	found := map[string]bool{}
	for _, a := range attr.Value.Group() {
		found[a.Key] = true
	}
	if !found["trace_id"] {
		t.Error("missing trace_id in error group")
	}
	if !found["span_id"] {
		t.Error("missing span_id in error group")
	}
}

func TestErr_ErrxPanic(t *testing.T) {
	xe := errx.NewPanicError("test.op", "boom")
	attr := Err(xe)
	found := map[string]bool{}
	for _, a := range attr.Value.Group() {
		found[a.Key] = true
		if a.Key == "panic" && !a.Value.Bool() {
			t.Error("expected panic=true")
		}
	}
	if !found["panic"] {
		t.Error("missing 'panic' in error group")
	}
}

// --- Handler: trace injection ---

func TestHandler_InjectsTrace(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := NewHandler(inner)
	logger := slog.New(h)

	ctx := ctxx.WithTrace(context.Background())
	traceID, spanID := ctxx.TraceFromContext(ctx)

	logger.InfoContext(ctx, "test message")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if record["trace_id"] != traceID {
		t.Errorf("expected trace_id=%s, got %v", traceID, record["trace_id"])
	}
	if record["span_id"] != spanID {
		t.Errorf("expected span_id=%s, got %v", spanID, record["span_id"])
	}
}

func TestHandler_NoTraceWithoutContext(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	h := NewHandler(inner)
	logger := slog.New(h)

	logger.Info("no trace")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if _, ok := record["trace_id"]; ok {
		t.Error("expected no trace_id without ctxx context")
	}
}

func TestHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	h := NewHandler(inner)
	h2 := h.WithAttrs([]slog.Attr{slog.String("service", "test")})

	logger := slog.New(h2)
	logger.Info("msg")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if record["service"] != "test" {
		t.Errorf("expected service=test, got %v", record["service"])
	}
}

func TestHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	h := NewHandler(inner)
	h2 := h.WithGroup("req")

	logger := slog.New(h2)
	logger.Info("msg", "method", "GET")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := record["req"]; !ok {
		t.Error("expected 'req' group in output")
	}
}
