package testx

import (
	"context"
	"time"
)

// ---------------------------------------------------------------------------
// Context helpers
// ---------------------------------------------------------------------------

// CancelledCtx returns a context that is already cancelled.
func CancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// TimedCtx returns a context with the given timeout and its cancel func.
// Always defer cancel() in the caller.
func TimedCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// DeadlineCtx returns a context with a deadline set to now+d and its cancel func.
func DeadlineCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithDeadline(context.Background(), time.Now().Add(d))
}

// ExpiredCtx returns a context whose deadline has already passed.
// The internal cancel func is invoked immediately to release timer
// resources; the returned context reports [context.DeadlineExceeded].
func ExpiredCtx() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	cancel()
	return ctx
}
