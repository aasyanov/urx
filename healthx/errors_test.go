package healthx

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrTimeout_WrapsSentinel(t *testing.T) {
	err := errTimeout("postgres")
	require.ErrorIs(t, err, ErrTimeout)
}

func TestErrUnhealthy_WrapsSentinelAndCause(t *testing.T) {
	cause := errors.New("connection refused")
	err := errUnhealthy("redis", cause)
	require.ErrorIs(t, err, ErrUnhealthy)
	require.ErrorIs(t, err, cause)
}
