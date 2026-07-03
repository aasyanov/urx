package signalx

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrShutdownTimeout_IsSentinel(t *testing.T) {
	require.Equal(t, "signalx: shutdown timed out", ErrShutdownTimeout.Error())
	require.ErrorIs(t, ErrShutdownTimeout, ErrShutdownTimeout)
}

func TestErrHookPanic_IsSentinel(t *testing.T) {
	require.Equal(t, "signalx: shutdown hook panicked", ErrHookPanic.Error())
	require.ErrorIs(t, ErrHookPanic, ErrHookPanic)
}
