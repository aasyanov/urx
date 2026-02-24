// Package errx provides structured, classifiable errors with retry semantics,
// severity levels, distributed tracing, and first-class slog/JSON integration.
//
// Every [Error] carries a Domain.Code pair for machine-readable classification,
// a human-readable Message, optional Meta for arbitrary context, and an
// optional causal chain via [Error.Unwrap].
//
// Domain and Code values should use the provided constants to avoid typos:
//
//	err := errx.New(errx.DomainAuth, errx.CodeUnauthorized, "token expired",
//	    errx.WithRetry(errx.RetrySafe),
//	    errx.WithMeta("user_id", "u-123"),
//	)
//
//	wrapped := errx.Wrap(dbErr, errx.DomainRepo, errx.CodeNotFound, "select user",
//	    errx.WithOp("UserRepo.FindByID"),
//	    errx.WithCategory(errx.CategoryBusiness),
//	)
//
package errx

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// --- Domain constants ---

const (
	// DomainInternal identifies errors originating from internal infrastructure.
	DomainInternal = "INTERNAL"
	// DomainAuth identifies authentication and authorization errors.
	DomainAuth = "AUTH"
	// DomainRepo identifies data-access / repository layer errors.
	DomainRepo = "REPO"
)

// --- Code constants ---

const (
	// CodePanic indicates an error recovered from a panic via [panix].
	CodePanic = "PANIC"
	// CodeInvalidInput indicates the caller provided invalid arguments.
	CodeInvalidInput = "INVALID_INPUT"
	// CodeUnauthorized indicates missing or invalid credentials.
	CodeUnauthorized = "UNAUTHORIZED"
	// CodeForbidden indicates the caller lacks permission for the operation.
	CodeForbidden = "FORBIDDEN"
	// CodeNotFound indicates the requested resource does not exist.
	CodeNotFound = "NOT_FOUND"
	// CodeConflict indicates a state conflict (e.g. duplicate key).
	CodeConflict = "CONFLICT"
	// CodeRateLimit indicates the request was rejected by a rate limiter.
	CodeRateLimit = "RATE_LIMIT"
	// CodeInternal indicates an unexpected internal error.
	CodeInternal = "INTERNAL"
	// CodeContextCancelled indicates the operation's context was cancelled or timed out.
	CodeContextCancelled = "CONTEXT_CANCELLED"
)

// --- String labels ---

// Human-readable labels used by [RetryClass.String], [Severity.String],
// [Category.String], and [MultiError.Error].
const (
	labelNone     = "none"
	labelSafe     = "safe"
	labelUnsafe   = "unsafe"
	labelInfo     = "info"
	labelWarn     = "warn"
	labelError    = "error"
	labelCritical = "critical"
	labelBusiness = "business"
	labelSystem   = "system"
	labelSecurity = "security"
	labelUnknown  = "unknown"
	labelNoErrors = "no errors"
)

// --- Retry classification ---

// RetryClass indicates whether a failed operation may be retried.
type RetryClass uint8

const (
	// RetryNone means the operation must not be retried.
	RetryNone RetryClass = iota
	// RetrySafe means the operation is idempotent and safe to retry.
	RetrySafe
	// RetryUnsafe means the operation may be retried, but side-effects are possible.
	RetryUnsafe
)

// Retryable reports whether the class permits retrying.
func (r RetryClass) Retryable() bool {
	return r == RetrySafe || r == RetryUnsafe
}

// String returns a human-readable label.
func (r RetryClass) String() string {
	switch r {
	case RetrySafe:
		return labelSafe
	case RetryUnsafe:
		return labelUnsafe
	default:
		return labelNone
	}
}

// MarshalText implements [encoding.TextMarshaler] so JSON output is a string.
func (r RetryClass) MarshalText() ([]byte, error) {
	return []byte(r.String()), nil
}

// --- Severity ---

// Severity represents the impact level of an error.
type Severity uint8

// Severity levels from informational to critical.
const (
	SeverityInfo     Severity = iota // informational, no action required
	SeverityWarn                     // degraded but operational
	SeverityError                    // operation failed
	SeverityCritical                 // system-level failure, needs immediate attention
)

// String returns a human-readable label.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return labelInfo
	case SeverityWarn:
		return labelWarn
	case SeverityError:
		return labelError
	case SeverityCritical:
		return labelCritical
	default:
		return labelUnknown
	}
}

// MarshalText implements [encoding.TextMarshaler] so JSON output is a string.
func (s Severity) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// --- Category ---

// Category classifies the origin of an error.
type Category uint8

// Error origin categories.
const (
	CategoryBusiness Category = iota // domain / business-rule violation
	CategorySystem                   // infrastructure / runtime failure
	CategorySecurity                 // authentication / authorization issue
)

// String returns a human-readable label.
func (c Category) String() string {
	switch c {
	case CategoryBusiness:
		return labelBusiness
	case CategorySystem:
		return labelSystem
	case CategorySecurity:
		return labelSecurity
	default:
		return labelUnknown
	}
}

// MarshalText implements [encoding.TextMarshaler] so JSON output is a string.
func (c Category) MarshalText() ([]byte, error) {
	return []byte(c.String()), nil
}

// --- Error ---

// Error is a structured error that carries classification, tracing,
// timestamps, and arbitrary metadata alongside the standard error interface.
//
// Domain, Code, Message, Severity, Category, and Timestamp are always
// populated by constructors. Everything else is optional.
type Error struct {
	Domain    string         `json:"domain"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Op        string         `json:"op,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
	Cause     error          `json:"-"`
	Retry     RetryClass     `json:"retry"`
	Severity  Severity       `json:"severity"`
	Category  Category       `json:"category"`
	TraceID   string         `json:"trace_id,omitempty"`
	SpanID    string         `json:"span_id,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	isPanic   bool
	stack     []uintptr
}

// enableStackTrace controls whether stack traces are captured globally.
var enableStackTrace atomic.Bool

// EnableStackTrace turns stack-trace capture on or off globally.
// Disabled by default for performance; enable in development or staging.
func EnableStackTrace(enabled bool) {
	enableStackTrace.Store(enabled)
}

// --- Functional options ---

// Option configures an [Error] during construction.
type Option func(*Error)

// WithOp sets the logical operation name (e.g. "UserRepo.FindByID").
func WithOp(op string) Option {
	return func(e *Error) {
		e.Op = op
	}
}

// WithSeverity overrides the default severity level.
func WithSeverity(s Severity) Option {
	return func(e *Error) {
		e.Severity = s
	}
}

// WithCategory overrides the default category.
func WithCategory(c Category) Option {
	return func(e *Error) {
		e.Category = c
	}
}

// WithRetry sets the retry classification.
func WithRetry(r RetryClass) Option {
	return func(e *Error) {
		e.Retry = r
	}
}

// WithTrace attaches distributed-tracing identifiers.
func WithTrace(traceID, spanID string) Option {
	return func(e *Error) {
		e.TraceID, e.SpanID = traceID, spanID
	}
}

// WithMeta merges key-value pairs into the error's metadata map.
// Keys must be strings; non-string keys are silently skipped.
//
//	errx.WithMeta("user_id", "u-123", "attempt", 3)
func WithMeta(kvs ...any) Option {
	return func(e *Error) {
		if len(kvs) == 0 {
			return
		}
		if e.Meta == nil {
			e.Meta = make(map[string]any, len(kvs)/2)
		}
		for i := 0; i+1 < len(kvs); i += 2 {
			if key, ok := kvs[i].(string); ok {
				e.Meta[key] = kvs[i+1]
			}
		}
	}
}

// WithMetaMap merges an entire map into the error's metadata.
func WithMetaMap(m map[string]any) Option {
	return func(e *Error) {
		if len(m) == 0 {
			return
		}
		if e.Meta == nil {
			e.Meta = make(map[string]any, len(m))
		}
		for k, v := range m {
			e.Meta[k] = v
		}
	}
}

// --- Constructors ---

// New creates a fresh [Error] with the given domain, code, and message.
// Defaults: Severity=Error, Category=System, Timestamp=now.
func New(domain, code, msg string, opts ...Option) *Error {
	e := &Error{
		Domain:    domain,
		Code:      code,
		Message:   msg,
		Severity:  SeverityError,
		Category:  CategorySystem,
		Timestamp: time.Now(),
	}
	for _, opt := range opts {
		opt(e)
	}
	if enableStackTrace.Load() {
		e.captureStack(2)
	}
	return e
}

// Wrap wraps an existing error with structured context.
// Returns nil when err is nil, making it safe to use unconditionally.
// Defaults: Severity=Error, Category=System, Timestamp=now.
func Wrap(err error, domain, code, msg string, opts ...Option) *Error {
	if err == nil {
		return nil
	}
	e := &Error{
		Domain:    domain,
		Code:      code,
		Message:   msg,
		Cause:     err,
		Severity:  SeverityError,
		Category:  CategorySystem,
		Timestamp: time.Now(),
	}
	for _, opt := range opts {
		opt(e)
	}
	if enableStackTrace.Load() {
		e.captureStack(2)
	}
	return e
}

// --- Panic error factory ---

// NewPanicError builds a critical [Error] from a recovered panic value.
// Intended for use by panic-recovery packages (e.g. panix):
//
//	defer func() {
//	    if r := recover(); r != nil {
//	        err = errx.NewPanicError("myOp", r)
//	    }
//	}()
func NewPanicError(op string, r any) *Error {
	var cause error
	if err, ok := r.(error); ok {
		cause = err
	} else {
		cause = fmt.Errorf("%v", r) //nolint:forbidigo // panic recovery
	}
	e := &Error{
		Domain:    DomainInternal,
		Code:      CodePanic,
		Message:   fmt.Sprintf("panic: %v", r),
		Op:        op,
		Cause:     cause,
		isPanic:   true,
		Severity:  SeverityCritical,
		Category:  CategorySystem,
		Timestamp: time.Now(),
	}
	if enableStackTrace.Load() {
		e.captureStack(3)
	}
	return e
}

// --- Stack trace ---

// captureStack records the call stack starting skip frames above the caller.
func (e *Error) captureStack(skip int) {
	const depth = 64
	e.stack = make([]uintptr, depth)
	n := runtime.Callers(skip, e.stack)
	e.stack = e.stack[:n]
}

// StackTrace returns a formatted stack trace, or an empty string if none
// was captured.
func (e *Error) StackTrace() string {
	if len(e.stack) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, pc := range e.stack {
		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}
		file, line := fn.FileLine(pc)
		fmt.Fprintf(&sb, "%s\n\t%s:%d\n", fn.Name(), file, line)
	}
	return sb.String()
}

// --- Error interface ---

// Error returns a human-readable string: "DOMAIN.CODE: message | cause: ...".
func (e *Error) Error() string {
	s := e.Domain + "." + e.Code + ": " + e.Message
	if e.Cause != nil {
		s += " | cause: " + e.Cause.Error()
	}
	return s
}

// Unwrap returns the underlying cause for use with [errors.Is] / [errors.As].
func (e *Error) Unwrap() error { return e.Cause }

// Retryable reports whether the error permits retrying.
func (e *Error) Retryable() bool { return e.Retry.Retryable() }

// IsPanic reports whether the error originated from a recovered panic.
func (e *Error) IsPanic() bool { return e.isPanic }

// --- As helper ---

// As extracts the first [*Error] from err's chain.
// Returns (nil, false) when no [*Error] is found.
func As(err error) (*Error, bool) {
	var xe *Error
	ok := errors.As(err, &xe)
	return xe, ok
}

// --- JSON serialization ---

// MarshalJSON produces a flat JSON object with all error fields.
// Cause is serialized as a string; stack trace is included only when captured.
func (e *Error) MarshalJSON() ([]byte, error) {
	type jsonError struct {
		Domain    string         `json:"domain"`
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Op        string         `json:"op,omitempty"`
		Meta      map[string]any `json:"meta,omitempty"`
		Cause     string         `json:"cause,omitempty"`
		Retry     RetryClass     `json:"retry"`
		Severity  Severity       `json:"severity"`
		Category  Category       `json:"category"`
		IsPanic   bool           `json:"is_panic,omitempty"`
		TraceID   string         `json:"trace_id,omitempty"`
		SpanID    string         `json:"span_id,omitempty"`
		Timestamp time.Time      `json:"timestamp"`
		Stack     string         `json:"stack,omitempty"`
	}
	je := jsonError{
		Domain:    e.Domain,
		Code:      e.Code,
		Message:   e.Message,
		Op:        e.Op,
		Meta:      e.Meta,
		Retry:     e.Retry,
		Severity:  e.Severity,
		Category:  e.Category,
		IsPanic:   e.isPanic,
		TraceID:   e.TraceID,
		SpanID:    e.SpanID,
		Timestamp: e.Timestamp,
		Stack:     e.StackTrace(),
	}
	if e.Cause != nil {
		je.Cause = e.Cause.Error()
	}
	return json.Marshal(je)
}

// --- slog integration ---

// LogValue implements [slog.LogValuer] so the error can be logged as a
// structured group: logger.Info("failed", "error", myErr).
func (e *Error) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("domain", e.Domain),
		slog.String("code", e.Code),
		slog.String("message", e.Message),
		slog.Time("timestamp", e.Timestamp),
	}
	if e.Op != "" {
		attrs = append(attrs, slog.String("op", e.Op))
	}
	attrs = append(attrs, slog.String("severity", e.Severity.String()), slog.String("category", e.Category.String()))
	if e.Retry != RetryNone {
		attrs = append(attrs, slog.String("retry", e.Retry.String()))
	}
	if e.TraceID != "" {
		attrs = append(attrs, slog.String("trace_id", e.TraceID))
	}
	if e.SpanID != "" {
		attrs = append(attrs, slog.String("span_id", e.SpanID))
	}
	if e.isPanic {
		attrs = append(attrs, slog.Bool("panic", true))
	}
	if len(e.Meta) > 0 {
		attrs = append(attrs, slog.Any("meta", e.Meta))
	}
	if e.Cause != nil {
		attrs = append(attrs, slog.String("cause", e.Cause.Error()))
	}
	if len(e.stack) > 0 {
		attrs = append(attrs, slog.String("stack", e.StackTrace()))
	}
	return slog.GroupValue(attrs...)
}

// SlogLevel maps [Severity] to an [slog.Level].
// SeverityCritical maps to ERROR+4 so alerting pipelines can distinguish it.
func (e *Error) SlogLevel() slog.Level {
	switch e.Severity {
	case SeverityInfo:
		return slog.LevelInfo
	case SeverityWarn:
		return slog.LevelWarn
	case SeverityCritical:
		return slog.LevelError + 4
	default:
		return slog.LevelError
	}
}
