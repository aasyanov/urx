package retryx

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrExhausted_WrapsSentinelAndCause(t *testing.T) {
	cause := errors.New("upstream down")
	err := errExhausted(3, cause)
	require.ErrorIs(t, err, ErrExhausted)
	require.ErrorIs(t, err, cause)
	assert.ErrorContains(t, err, "attempts=3")
}

func TestErrCancelled_WrapsSentinelAndCause(t *testing.T) {
	err := errCancelled(context.Canceled)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestErrAborted_WrapsSentinelAndCause(t *testing.T) {
	cause := errors.New("permanent")
	err := errAborted(2, cause)
	require.ErrorIs(t, err, ErrAborted)
	require.ErrorIs(t, err, cause)
	assert.ErrorContains(t, err, "attempt=2")
}

func TestErrMaxElapsed_WrapsSentinelAndCause(t *testing.T) {
	cause := errors.New("upstream down")
	err := errMaxElapsed(cause)
	require.ErrorIs(t, err, ErrMaxElapsed)
	require.ErrorIs(t, err, cause)
}
