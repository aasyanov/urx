package ratex

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestErrors_IsSentinel(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrCancelled", ErrCancelled},
		{"ErrNilFunc", ErrNilFunc},
		{"ErrExceedsBurst", ErrExceedsBurst},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.err, tt.err)
			require.True(t, errors.Is(tt.err, tt.err))
		})
	}
}
