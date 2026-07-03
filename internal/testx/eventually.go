package testx

import (
	"fmt"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Async state assertion
// ---------------------------------------------------------------------------

const defaultEventuallyInterval = 10 * time.Millisecond

// errorfHelper is the minimal subset of [testing.TB] used by [Eventually]
// and [Never]. Accepting an interface (rather than *testing.T) lets the
// failure branches be unit-tested with a recording fake.
type errorfHelper interface {
	Helper()
	Errorf(format string, args ...any)
}

// Eventually polls cond until it returns true or timeout expires.
// Use for async state transitions: circuit breaker open→half-open,
// warmup completion, goroutine counter drain.
//
//	testx.Eventually(t, func() bool {
//	    return breaker.State() == circuitx.HalfOpen
//	}, 2*time.Second)
func Eventually(t *testing.T, cond func() bool, timeout time.Duration, msgAndArgs ...any) {
	t.Helper()
	eventually(t, cond, timeout, msgAndArgs...)
}

func eventually(t errorfHelper, cond func() bool, timeout time.Duration, msgAndArgs ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("testx.Eventually: condition not met within %s%s", timeout, formatMsg(msgAndArgs))
			return
		}
		time.Sleep(defaultEventuallyInterval)
	}
}

// Never asserts that cond never becomes true within duration.
// Inverse of [Eventually]. Use to verify that a state transition does NOT
// happen (e.g., circuit stays closed, goroutine does not leak).
//
//	testx.Never(t, func() bool {
//	    return breaker.State() == circuitx.Open
//	}, 200*time.Millisecond)
func Never(t *testing.T, cond func() bool, duration time.Duration, msgAndArgs ...any) {
	t.Helper()
	never(t, cond, duration, msgAndArgs...)
}

func never(t errorfHelper, cond func() bool, duration time.Duration, msgAndArgs ...any) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if cond() {
			t.Errorf("testx.Never: condition unexpectedly became true within %s%s", duration, formatMsg(msgAndArgs))
			return
		}
		time.Sleep(defaultEventuallyInterval)
	}
}

// formatMsg renders optional msgAndArgs into a ": context" suffix. The
// first element is treated as a format string when it is a string and
// additional args are present; otherwise the elements are joined.
func formatMsg(msgAndArgs []any) string {
	if len(msgAndArgs) == 0 {
		return ""
	}
	if format, ok := msgAndArgs[0].(string); ok {
		if len(msgAndArgs) > 1 {
			return ": " + fmt.Sprintf(format, msgAndArgs[1:]...)
		}
		return ": " + format
	}
	return ": " + fmt.Sprint(msgAndArgs...)
}
