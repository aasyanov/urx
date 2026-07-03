package warmupx

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrRejected_WrapsSentinelAndContext(t *testing.T) {
	err := errRejected(0.42, 0.17)
	require.ErrorIs(t, err, ErrRejected)
	assert.Contains(t, err.Error(), "capacity=0.42")
	assert.Contains(t, err.Error(), "progress=0.17")
}

func TestErrCancelled_WrapsSentinelAndCause(t *testing.T) {
	err := errCancelled(context.Canceled)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestErrCancelled_WrapsDeadlineExceeded(t *testing.T) {
	err := errCancelled(context.DeadlineExceeded)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestErrCancelled_WrapsCustomCause(t *testing.T) {
	cause := errors.New("upstream cancelled")
	err := errCancelled(cause)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, cause)
}
