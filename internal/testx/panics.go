package testx

import (
	"errors"
	"testing"

	"github.com/aasyanov/urx/panix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Panic error assertions
// ---------------------------------------------------------------------------

// RequirePanicError asserts that err is a *panix.PanicError with the given
// Op string, and returns the typed error for further inspection. Stops the
// test immediately if the assertion fails.
//
//	pe := testx.RequirePanicError(t, err, "circuitx.Execute")
//	assert.Contains(t, string(pe.Stack), "handler")
func RequirePanicError(t *testing.T, err error, wantOp string) *panix.PanicError {
	t.Helper()
	require.Error(t, err)
	var pe *panix.PanicError
	require.ErrorAs(t, err, &pe, "expected *panix.PanicError, got %T", err)
	assert.Equal(t, wantOp, pe.Op)
	assert.NotEmpty(t, pe.Stack, "PanicError should contain a stack trace")
	return pe
}

// AssertNotPanicError asserts that err is NOT a *panix.PanicError.
// Use when verifying that a regular error was returned (not a recovered panic).
func AssertNotPanicError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var pe *panix.PanicError
	assert.False(t, errors.As(err, &pe),
		"expected regular error, got *panix.PanicError: %v", err)
}
