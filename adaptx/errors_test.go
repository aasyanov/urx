package adaptx

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrors_Sentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrClosed", ErrClosed},
		{"ErrTimeout", ErrTimeout},
		{"ErrCancelled", ErrCancelled},
		{"ErrNilFunc", ErrNilFunc},
		{"ErrDrainTimeout", ErrDrainTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.err, tt.err)
			require.Equal(t, tt.err, tt.err)
		})
	}
}

func TestErrTimeout_WrapsCause(t *testing.T) {
	err := errTimeout(context.DeadlineExceeded)
	require.ErrorIs(t, err, ErrTimeout)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestErrCancelled_WrapsCause(t *testing.T) {
	err := errCancelled(context.Canceled)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestErrCancelled_WrapsCustomCause(t *testing.T) {
	cause := errors.New("upstream cancelled")
	err := errCancelled(cause)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, cause)
}
