//go:build unix

package signalx

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrap_SignalCancelsContext(t *testing.T) {
	ctx, cancel := Trap(context.Background(), syscall.SIGUSR1)
	defer cancel()

	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR1))

	testx.Eventually(t, func() bool {
		return ctx.Err() != nil
	}, 2*time.Second)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestTrap_CustomSignalIgnoresOthers(t *testing.T) {
	ctx, cancel := Trap(context.Background(), syscall.SIGUSR1)
	defer cancel()

	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR2))
	testx.Never(t, func() bool { return ctx.Err() != nil }, 150*time.Millisecond)

	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR1))
	testx.Eventually(t, func() bool { return ctx.Err() != nil }, 2*time.Second)
}

func TestWait_EndToEndSignalShutdown(t *testing.T) {
	ResetHooks()
	t.Cleanup(ResetHooks)

	ctx, cancel := Trap(context.Background(), syscall.SIGUSR1)
	defer cancel()

	ran := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Wait(ctx, func(context.Context) { close(ran) })
	}()

	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR1))

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown hook did not run after signal")
	}

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after signal shutdown")
	}
}
