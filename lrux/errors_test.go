package lrux

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrClosed_IsSentinel(t *testing.T) {
	require.ErrorIs(t, ErrClosed, ErrClosed)
	require.Equal(t, "lrux: cache is closed", ErrClosed.Error())
}

func TestErrNotFound_IsSentinel(t *testing.T) {
	require.ErrorIs(t, ErrNotFound, ErrNotFound)
	require.Equal(t, "lrux: key not found", ErrNotFound.Error())
}

func TestErrClosed_NotEqualToErrNotFound(t *testing.T) {
	require.False(t, errors.Is(ErrClosed, ErrNotFound))
}
