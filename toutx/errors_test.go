package toutx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrDeadlineExceeded_WrapsSentinelAndOp(t *testing.T) {
	err := errDeadlineExceeded("db.query", 3*time.Second)
	require.ErrorIs(t, err, ErrDeadlineExceeded)
	assert.ErrorContains(t, err, "op=db.query")
	assert.ErrorContains(t, err, "timeout=3s")
}

func TestErrCancelled_WrapsSentinelOpAndCause(t *testing.T) {
	cause := errors.New("upstream stopped")
	err := errCancelled("api.fetch", cause)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, cause)
	assert.ErrorContains(t, err, "op=api.fetch")
}

func TestErrCancelled_WrapsContextCanceled(t *testing.T) {
	err := errCancelled("op", context.Canceled)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestErrNilFunc_WrapsSentinelAndOp(t *testing.T) {
	err := errNilFunc("custom.op")
	require.ErrorIs(t, err, ErrNilFunc)
	assert.ErrorContains(t, err, "op=custom.op")
}
