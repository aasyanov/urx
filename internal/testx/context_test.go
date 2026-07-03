package testx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelledCtx(t *testing.T) {
	ctx := CancelledCtx()
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestTimedCtx(t *testing.T) {
	ctx, cancel := TimedCtx(100 * time.Millisecond)
	defer cancel()

	require.NoError(t, ctx.Err(), "should not be expired yet")

	deadline, ok := ctx.Deadline()
	assert.True(t, ok, "should have a deadline")
	assert.WithinDuration(t, time.Now().Add(100*time.Millisecond), deadline, 50*time.Millisecond)
}

func TestDeadlineCtx(t *testing.T) {
	ctx, cancel := DeadlineCtx(200 * time.Millisecond)
	defer cancel()

	require.NoError(t, ctx.Err())

	deadline, ok := ctx.Deadline()
	assert.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(200*time.Millisecond), deadline, 50*time.Millisecond)
}

func TestExpiredCtx(t *testing.T) {
	ctx := ExpiredCtx()
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}
