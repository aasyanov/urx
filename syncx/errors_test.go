package syncx

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrInitFailed_WrapsSentinelAndCause(t *testing.T) {
	cause := errors.New("connection refused")
	err := errInitFailed(cause)
	require.ErrorIs(t, err, ErrInitFailed)
	require.ErrorIs(t, err, cause)
}

func TestErrNilInit_IsComparable(t *testing.T) {
	require.ErrorIs(t, ErrNilInit, ErrNilInit)
}

func TestErrNilFunc_IsComparable(t *testing.T) {
	require.ErrorIs(t, ErrNilFunc, ErrNilFunc)
}
