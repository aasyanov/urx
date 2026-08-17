package bulkx

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrors_SentinelsComparableWithIs(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"timeout", ErrTimeout, ErrTimeout},
		{"closed", ErrClosed, ErrClosed},
		{"cancelled", ErrCancelled, ErrCancelled},
		{"nil func", ErrNilFunc, ErrNilFunc},
		{"waiters exceeded", ErrWaitersExceeded, ErrWaitersExceeded},
		{"cancelled wraps cause", errCancelled(context.Canceled), ErrCancelled},
		{"cancelled wraps deadline", errCancelled(context.DeadlineExceeded), context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.err, tt.want)
		})
	}
}

func TestErrCancelled_WrapsCustomCause(t *testing.T) {
	cause := errors.New("upstream cancelled")
	err := errCancelled(cause)
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, cause)
}
