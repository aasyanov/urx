package testx

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// recordingT records Errorf calls so failure branches of eventually/never
// can be asserted without failing the enclosing test.
type recordingT struct {
	errors []string
}

func (r *recordingT) Helper() {}

func (r *recordingT) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func TestEventually_ImmediateTrue(t *testing.T) {
	Eventually(t, func() bool { return true }, 100*time.Millisecond)
}

func TestEventually_BecomesTrue(t *testing.T) {
	var ready atomic.Bool
	go func() {
		time.Sleep(50 * time.Millisecond)
		ready.Store(true)
	}()
	Eventually(t, ready.Load, 2*time.Second)
}

func TestEventually_TimesOut(t *testing.T) {
	rec := &recordingT{}
	eventually(rec, func() bool { return false }, 30*time.Millisecond)
	assert.Len(t, rec.errors, 1, "timeout should record exactly one error")
	assert.Contains(t, rec.errors[0], "condition not met")
}

func TestEventually_TimesOutWithMessage(t *testing.T) {
	rec := &recordingT{}
	eventually(rec, func() bool { return false }, 20*time.Millisecond, "circuit %s", "open")
	assert.Len(t, rec.errors, 1)
	assert.Contains(t, rec.errors[0], "circuit open")
}

func TestNever_StaysFalse(t *testing.T) {
	Never(t, func() bool { return false }, 100*time.Millisecond)
}

func TestNever_BecomesTrue(t *testing.T) {
	rec := &recordingT{}
	never(rec, func() bool { return true }, 100*time.Millisecond)
	assert.Len(t, rec.errors, 1, "condition becoming true should record one error")
	assert.Contains(t, rec.errors[0], "unexpectedly became true")
}

func TestFormatMsg(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want string
	}{
		{"empty", nil, ""},
		{"plain string", []any{"hello"}, ": hello"},
		{"format string", []any{"x=%d", 42}, ": x=42"},
		{"non-string first", []any{42}, ": 42"},
		{"non-string multiple", []any{1, 2}, ": 1 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatMsg(tt.args))
		})
	}
}
