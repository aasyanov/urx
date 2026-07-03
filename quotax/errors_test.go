package quotax

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrLimited_WrapsSentinelAndKey(t *testing.T) {
	err := errLimited("user:42")
	require.ErrorIs(t, err, ErrLimited)
	require.ErrorContains(t, err, "user:42")
}

func TestErrMaxKeys_WrapsSentinelAndKey(t *testing.T) {
	err := errMaxKeys("tenant:acme")
	require.ErrorIs(t, err, ErrMaxKeys)
	require.ErrorContains(t, err, "tenant:acme")
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
