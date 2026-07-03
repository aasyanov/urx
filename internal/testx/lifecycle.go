package testx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Lifecycle assertions
// ---------------------------------------------------------------------------

// Closer is the interface that wraps the Close method.
// Matches bulkx.Bulkhead, shedx.Shedder, quotax.Quota, lrux.LRU, etc.
type Closer interface {
	Close() error
}

// AssertCloseIdempotent verifies that Close succeeds on first call and
// does not panic or return a different error on subsequent calls.
// Most urx components guarantee idempotent Close.
//
//	testx.AssertCloseIdempotent(t, limiter)
func AssertCloseIdempotent(t *testing.T, c Closer) {
	t.Helper()
	err := c.Close()
	require.NoError(t, err, "first Close should succeed")

	err2 := c.Close()
	assert.NoError(t, err2, "second Close should be idempotent (no error)")
}

// AssertOpAfterClose calls opName on a closed resource and asserts it
// returns wantErr. Use to verify that a closed component rejects work
// with the appropriate sentinel error.
//
//	testx.AssertOpAfterClose(t, func() error {
//	    _, err := circuitx.Execute(cb, ctx, fn)
//	    return err
//	}, bulkx.ErrClosed, "Execute")
func AssertOpAfterClose(t *testing.T, op func() error, wantErr error, opName string) {
	t.Helper()
	err := op()
	require.Error(t, err, "%s after Close should fail", opName)
	if wantErr != nil {
		require.ErrorIs(t, err, wantErr, "%s after Close should return %v", opName, wantErr)
	}
}
