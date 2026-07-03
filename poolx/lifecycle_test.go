package poolx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloseSignal_ContextInterface(t *testing.T) {
	done := make(chan struct{})
	sig := closeSignal{done: done}

	deadline, ok := sig.Deadline()
	assert.False(t, ok)
	assert.True(t, deadline.IsZero())

	require.Nil(t, sig.Value("any-key"))

	require.NoError(t, sig.Err())
	require.NotNil(t, sig.Done())

	close(done)
	require.ErrorIs(t, sig.Err(), context.Canceled)
}

func TestCloseSignal_DeadlineBeforeCancel(t *testing.T) {
	sig := closeSignal{done: make(chan struct{})}
	_, ok := sig.Deadline()
	assert.False(t, ok)
}

func TestCloseSignal_ValueReturnsNil(t *testing.T) {
	sig := closeSignal{done: make(chan struct{})}
	assert.Nil(t, sig.Value(struct{}{}))
}

func TestCloseSignal_ErrOpenVsClosed(t *testing.T) {
	done := make(chan struct{})
	sig := closeSignal{done: done}

	require.Eventually(t, func() bool {
		return sig.Err() == nil
	}, time.Second, time.Millisecond)

	close(done)
	require.Eventually(t, func() bool {
		return sig.Err() != nil
	}, time.Second, time.Millisecond)
	require.ErrorIs(t, sig.Err(), context.Canceled)
}
